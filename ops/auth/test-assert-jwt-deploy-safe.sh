#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
MOCK_BIN="$TMP_DIR/bin"
JWT_DIRECTORY="$TMP_DIR/jwt"
ENV_FILE="$TMP_DIR/.env.production"
mkdir -p "$MOCK_BIN" "$JWT_DIRECTORY"

cat > "$ENV_FILE" <<EOF
APP_IMAGE=surau-backend:test
JWT_SECRETS_DIR=$JWT_DIRECTORY
EOF

cat > "$MOCK_BIN/sudo" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
exec "$@"
MOCK

cat > "$MOCK_BIN/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ "${1:-}" == run ]]
printf '{"state": "%s"}\n' "${MOCK_KEYSET_STATE:?}"
MOCK
chmod +x "$MOCK_BIN/sudo" "$MOCK_BIN/docker"

run_guard() {
  PATH="$MOCK_BIN:$PATH" APP_ENV=dev ENV_FILE="$ENV_FILE" \
    MOCK_KEYSET_STATE="${1:-stable}" "$SCRIPT_DIR/assert-jwt-deploy-safe.sh"
}

[[ "$(run_guard stable)" == capture-legacy ]]
touch "$JWT_DIRECTORY/keyset.json"
[[ "$(run_guard stable)" == capture-legacy ]]
[[ "$(run_guard retired)" == safe ]]

touch "$JWT_DIRECTORY/drill-dev.env"
if run_guard retired >/dev/null 2>&1; then
  echo "deploy guard accepted incomplete retired cleanup" >&2
  exit 1
fi
rm -f "$JWT_DIRECTORY/drill-dev.env"

for state in prepared active rolled_back invalid; do
  if run_guard "$state" >/dev/null 2>&1; then
    echo "deploy guard accepted unsafe state: $state" >&2
    exit 1
  fi
done

assert_workflow_guard_order() {
  local workflow="$1"
  local checkout_pattern="$2"
  local build_pattern='docker compose --env-file .env.production -f docker-compose.prod.yml.* build'
  local preflight_line checkout_line build_line bootstrap_line postbuild_line

  preflight_line="$(grep -n -m1 'A4_PREDEPLOY_MODE=' "$workflow" | cut -d: -f1)"
  checkout_line="$(grep -n -m1 "$checkout_pattern" "$workflow" | cut -d: -f1)"
  build_line="$(grep -n -m1 "$build_pattern" "$workflow" | cut -d: -f1)"
  bootstrap_line="$(grep -n -m1 'bootstrap-jwt-keyset.sh' "$workflow" | cut -d: -f1)"
  postbuild_line="$(grep -n -m1 'A4_BRIDGE_MODE=' "$workflow" | cut -d: -f1)"

  if [[ ! "$preflight_line" =~ ^[0-9]+$ || ! "$checkout_line" =~ ^[0-9]+$ ||
        ! "$build_line" =~ ^[0-9]+$ || ! "$bootstrap_line" =~ ^[0-9]+$ ||
        ! "$postbuild_line" =~ ^[0-9]+$ || "$preflight_line" -ge "$checkout_line" ||
        "$build_line" -ge "$bootstrap_line" || "$bootstrap_line" -ge "$postbuild_line" ]]; then
    echo "deploy guard ordering regressed in $workflow" >&2
    exit 1
  fi
}

# The first guard must use the currently deployed checkout/image and run before
# any checkout mutation. The second guard remains after build/bootstrap as the
# TOCTOU check immediately before container replacement.
assert_workflow_guard_order \
  "$REPO_ROOT/.github/workflows/deploy-dev.yml" 'git fetch origin'
assert_workflow_guard_order \
  "$REPO_ROOT/.github/workflows/deploy-prod.yml" 'git fetch --tags'

rotation_workflow="$REPO_ROOT/.github/workflows/jwt-rotation.yml"
if grep -Fq '/var/lib/surau/secrets/jwt/worker-keyset.json' "$rotation_workflow"; then
  echo "JWT rotation workflow hardcodes the Worker export path" >&2
  exit 1
fi
if [[ "$(grep -Fc "\$jwt_directory/worker-keyset.json" "$rotation_workflow")" -lt 2 ]]; then
  echo "JWT rotation workflow does not reuse the validated directory for copy and cleanup" >&2
  exit 1
fi

echo "JWT deploy guard tests passed"
