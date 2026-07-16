#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
cleanup() {
  chmod -R u+rwx "$TMP_DIR" 2>/dev/null || true
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$TMP_DIR/bin" "$TMP_DIR/secrets"

REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
go build -o "$TMP_DIR/bin/jwt-keyset-real" "$REPO_ROOT/cmd/jwt-keyset"

cat > "$TMP_DIR/bin/sudo" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "-E" ]]; then
  shift
fi
if [[ "${1:-}" == "install" ]]; then
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
if [[ "${1:-}" == "test" && -n "${MOCK_ROOT_ONLY_KEYSET_FILE:-}" &&
      "${3:-}" == "$MOCK_ROOT_ONLY_KEYSET_FILE" &&
      ( "${2:-}" == "-f" || "${2:-}" == "-e" ) ]]; then
  exit 0
fi
exec "$@"
MOCK

cat > "$TMP_DIR/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == compose ]]; then
  printf '%s\n' 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
  exit 0
fi
if [[ "$1" == inspect ]]; then
  printf 'JWT_SECRET=%s\n' "${MOCK_RUNNING_SECRET:?}"
  exit 0
fi
[[ "$1" == "run" ]]
shift
host_directory=""
jwt_secret_value="${JWT_SECRET-}"
unset JWT_SECRET
while (( $# > 0 )); do
  case "$1" in
    --rm|--read-only) shift ;;
    --network|--cap-drop|--security-opt|--user|--entrypoint)
      shift 2
      ;;
    --env)
      [[ "$2" == "JWT_SECRET" ]]
      export JWT_SECRET="$jwt_secret_value"
      shift 2
      ;;
    --volume)
      host_directory="${2%%:/keys}"
      shift 2
      ;;
    *)
      image="$1"
      shift
      break
      ;;
  esac
done
[[ "$image" == "surau-backend:latest" ]]
[[ -n "$host_directory" ]]
args=()
while (( $# > 0 )); do
  if [[ "$1" == "--file" ]]; then
    args+=("--file" "$host_directory/${2#/keys/}")
    shift 2
  else
    args+=("$1")
    shift
  fi
done
exec "$(dirname "$0")/jwt-keyset-real" "${args[@]}"
MOCK
chmod 0755 "$TMP_DIR/bin/sudo" "$TMP_DIR/bin/docker"

secret="legacy-secret-0123456789abcdef0123456789"
env_file="$TMP_DIR/env"
cat > "$env_file" <<EOF
JWT_SECRET=$secret
MFA_ENCRYPTION_KEY=
EMAIL_UNSUBSCRIBE_TOKEN_SECRET=
EMAIL_UNSUBSCRIBE_TOKEN_SECRETS=
JWT_SECRETS_DIR=$TMP_DIR/secrets
EOF
chmod 0600 "$env_file"

output="$(PATH="$TMP_DIR/bin:$PATH" MOCK_RUNNING_SECRET="$secret" ENV_FILE="$env_file" APP_ENV=dev \
  "$SCRIPT_DIR/bootstrap-jwt-keyset.sh")"
if [[ "$output" == *"$secret"* ]]; then
  echo "JWT bootstrap leaked the legacy secret" >&2
  exit 1
fi

grep -Fqx "MFA_ENCRYPTION_KEY=$secret" "$env_file"
grep -Fqx "EMAIL_UNSUBSCRIBE_TOKEN_SECRET=$secret" "$env_file"
grep -Fqx "JWT_SECRETS_DIR=$TMP_DIR/secrets" "$env_file"
grep -Fqx 'JWT_KEYSET_FILE=/run/secrets/surau-jwt/keyset.json' "$env_file"

keyset_file="$TMP_DIR/secrets/keyset.json"
[[ -f "$keyset_file" ]]
jq -e --arg secret "$secret" '
  .version == 1 and
  .active_kid == .legacy_kid and
  (.keys | length) == 1 and
  (.keys[.active_kid] == $secret)
' "$keyset_file" >/dev/null

permissions() {
  if [[ "$(uname -s)" == "Darwin" ]]; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}
[[ "$(permissions "$env_file")" == "600" ]]
[[ "$(permissions "$TMP_DIR/secrets")" == "700" ]]
[[ "$(permissions "$keyset_file")" == "600" ]]

# Retry is idempotent and must not replace either the keyset or pinned seeds.
checksum_before="$(shasum -a 256 "$keyset_file")"
output="$(PATH="$TMP_DIR/bin:$PATH" MOCK_RUNNING_SECRET="$secret" ENV_FILE="$env_file" APP_ENV=dev \
  "$SCRIPT_DIR/bootstrap-jwt-keyset.sh")"
checksum_after="$(shasum -a 256 "$keyset_file")"
[[ "$checksum_after" == "$checksum_before" ]]
if [[ "$output" == *"$secret"* ]]; then
  echo "JWT bootstrap retry leaked the legacy secret" >&2
  exit 1
fi

# The production key directory is root:root 0700. After retirement JWT_SECRET
# is deliberately empty, so every existence check must go through sudo rather
# than treating an unreadable, already-valid keyset as absent.
root_only_dir="$TMP_DIR/root-only"
root_only_keyset="$root_only_dir/keyset.json"
root_only_env="$TMP_DIR/root-only.env"
mkdir -p "$root_only_dir"
cp "$keyset_file" "$root_only_keyset"
cat > "$root_only_env" <<EOF
JWT_SECRET=
MFA_ENCRYPTION_KEY=dedicated-mfa-secret-0123456789abcdef
EMAIL_UNSUBSCRIBE_TOKEN_SECRET=dedicated-unsubscribe-secret-0123456789abcdef
EMAIL_UNSUBSCRIBE_TOKEN_SECRETS=
JWT_SECRETS_DIR=$root_only_dir
JWT_KEYSET_FILE=/run/secrets/surau-jwt/keyset.json
EOF
chmod 000 "$root_only_dir"
output="$(PATH="$TMP_DIR/bin:$PATH" MOCK_ROOT_ONLY_KEYSET_FILE="$root_only_keyset" \
  MOCK_RUNNING_SECRET="$secret" ENV_FILE="$root_only_env" APP_ENV=dev \
  "$SCRIPT_DIR/bootstrap-jwt-keyset.sh")"
[[ "$output" == *'existing keyset validated'* ]]
grep -Fqx 'JWT_SECRET=' "$root_only_env"
chmod 0700 "$root_only_dir"

# Ambiguous dotenv quoting fails before any secret file is created.
unsafe_dir="$TMP_DIR/unsafe"
unsafe_env="$TMP_DIR/unsafe.env"
cat > "$unsafe_env" <<EOF
JWT_SECRET="$secret"
MFA_ENCRYPTION_KEY=
EMAIL_UNSUBSCRIBE_TOKEN_SECRET=
EMAIL_UNSUBSCRIBE_TOKEN_SECRETS=
JWT_SECRETS_DIR=$unsafe_dir
EOF
if PATH="$TMP_DIR/bin:$PATH" MOCK_RUNNING_SECRET="$secret" ENV_FILE="$unsafe_env" APP_ENV=dev \
  "$SCRIPT_DIR/bootstrap-jwt-keyset.sh" >/dev/null 2>&1; then
  echo "quoted JWT_SECRET was accepted unexpectedly" >&2
  exit 1
fi
[[ ! -e "$unsafe_dir/keyset.json" ]]

# A dotenv value that differs from the signer already serving traffic fails
# before pinning derived keys or creating any keyset.
mismatch_dir="$TMP_DIR/mismatch"
mismatch_env="$TMP_DIR/mismatch.env"
cat > "$mismatch_env" <<EOF
JWT_SECRET=$secret
MFA_ENCRYPTION_KEY=
EMAIL_UNSUBSCRIBE_TOKEN_SECRET=
EMAIL_UNSUBSCRIBE_TOKEN_SECRETS=
JWT_SECRETS_DIR=$mismatch_dir
EOF
if PATH="$TMP_DIR/bin:$PATH" MOCK_RUNNING_SECRET="different-running-secret-0123456789abcdef" \
  ENV_FILE="$mismatch_env" APP_ENV=dev "$SCRIPT_DIR/bootstrap-jwt-keyset.sh" >/dev/null 2>&1; then
  echo "mismatched running JWT signer was accepted unexpectedly" >&2
  exit 1
fi
[[ ! -e "$mismatch_dir/keyset.json" ]]
grep -Fqx 'MFA_ENCRYPTION_KEY=' "$mismatch_env"
grep -Fqx 'EMAIL_UNSUBSCRIBE_TOKEN_SECRET=' "$mismatch_env"

echo "JWT keyset bootstrap tests passed"
