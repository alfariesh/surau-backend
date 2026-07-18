#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

# The deploy workflows send their remote commands through an SSH heredoc.
# Guard the cutover database helper against consuming every command after it.
db_psql_body="$(sed -n '/^db_psql() {$/,/^}$/p' \
  "$SCRIPT_DIR/collab-dev-credentials.sh")"
if [[ "$db_psql_body" != *'</dev/null'* ]]; then
  echo "db_psql must detach stdin so SSH deploy scripts can continue" >&2
  exit 1
fi

mkdir -p "$TMP_DIR/bin"
cat > "$TMP_DIR/bin/sudo" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == "chown" ]]; then
  exit 0
fi
if [[ "$1" == "install" ]]; then
  shift
  args=()
  while (( $# > 0 )); do
    case "$1" in
      -o|-g) shift 2 ;;
      *) args+=("$1"); shift ;;
    esac
  done
  exec install "${args[@]}"
fi
exec "$@"
MOCK
chmod 0755 "$TMP_DIR/bin/sudo"

secrets_dir="$TMP_DIR/secrets"
env_file="$TMP_DIR/env"
cat > "$env_file" <<EOF
PG_URL=postgres://owner-a:owner-password@db:5432/surau
POSTGRES_DB=surau
COLLAB_SERVICE_TOKEN=legacy-token-at-least-32-bytes-aaaa
COLLAB_SECRETS_DIR=$secrets_dir
COLLAB_SECRETS_GID=$(id -g)
ALLOW_LEGACY_DB_CREDENTIALS=false
EOF

output="$(PATH="$TMP_DIR/bin:$PATH" ENV_FILE="$env_file" \
  "$SCRIPT_DIR/collab-dev-credentials.sh" prepare)"
if [[ "$output" == *"owner-password"* || "$output" == *"legacy-token"* ]]; then
  echo "prepare leaked a credential" >&2
  exit 1
fi
[[ "$(<"$secrets_dir/pg-url")" == "postgres://owner-a:owner-password@db:5432/surau" ]]
[[ "$(<"$secrets_dir/service-token")" == "legacy-token-at-least-32-bytes-aaaa" ]]
grep -qx 'ALLOW_LEGACY_DB_CREDENTIALS=true' "$env_file"

permissions() {
  if [[ "$(uname -s)" == "Darwin" ]]; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}
[[ "$(permissions "$secrets_dir")" == "750" ]]
[[ "$(permissions "$secrets_dir/pg-url")" == "640" ]]
[[ "$(permissions "$secrets_dir/service-token")" == "640" ]]

# Existing A/B files are stable across deploy retries; changed direct env
# values must never overwrite a credential that may already be active.
sed \
  -e 's/owner-password/replacement-must-not-win/' \
  -e 's/legacy-token-at-least-32-bytes-aaaa/replacement-token-must-not-win-aaaa/' \
  "$env_file" > "$env_file.next"
mv "$env_file.next" "$env_file"
PATH="$TMP_DIR/bin:$PATH" ENV_FILE="$env_file" \
  "$SCRIPT_DIR/collab-dev-credentials.sh" prepare >/dev/null
[[ "$(<"$secrets_dir/pg-url")" == "postgres://owner-a:owner-password@db:5432/surau" ]]
[[ "$(<"$secrets_dir/service-token")" == "legacy-token-at-least-32-bytes-aaaa" ]]

echo "collab DEV credential prepare tests passed"
