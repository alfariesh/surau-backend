#!/usr/bin/env bash
set -euo pipefail

# One-time A-4 deploy bridge. It preserves the exact pre-A-4 JWT_SECRET bytes,
# pins the two features that historically derived keys from JWT_SECRET, and
# creates the root-owned runtime keyset before the new app container starts.

ENV_FILE="${ENV_FILE:-.env.production}"
APP_ENV="${APP_ENV:-prod}"
APP_IMAGE="${APP_IMAGE:-}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"
ALLOW_NO_RUNNING_SIGNER="${ALLOW_NO_RUNNING_SIGNER:-false}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "JWT bootstrap rejected: env file is missing" >&2
  exit 1
fi
if [[ ! "$APP_ENV" =~ ^[A-Za-z0-9_-]{1,32}$ ]]; then
  echo "JWT bootstrap rejected: APP_ENV is not a safe key-id component" >&2
  exit 1
fi
if [[ ! "$ALLOW_NO_RUNNING_SIGNER" =~ ^(true|false)$ ]]; then
  echo "JWT bootstrap rejected: ALLOW_NO_RUNNING_SIGNER must be true or false" >&2
  exit 1
fi
env_value() {
  local key="$1"
  sed -n "s/^${key}=//p" "$ENV_FILE" | tail -1
}

APP_IMAGE="${APP_IMAGE:-$(env_value APP_IMAGE)}"
APP_IMAGE="${APP_IMAGE:-surau-backend:latest}"
if [[ ! "$APP_IMAGE" =~ ^[A-Za-z0-9_./:@-]+$ ]]; then
  echo "JWT bootstrap rejected: APP_IMAGE contains unsupported characters" >&2
  exit 1
fi

# Values travel through the child environment, never argv. The temporary file
# is mode 0600 and atomically replaces the dotenv file.
set_env_value() {
  local key="$1"
  local value="$2"
  local temporary
  temporary="$(mktemp "${ENV_FILE}.tmp.XXXXXX")"
  chmod 0600 "$temporary"
  if ! ENV_KEY="$key" ENV_VALUE="$value" awk '
    BEGIN { key = ENVIRON["ENV_KEY"]; value = ENVIRON["ENV_VALUE"]; found = 0 }
    index($0, key "=") == 1 { print key "=" value; found = 1; next }
    { print }
    END { if (!found) print key "=" value }
  ' "$ENV_FILE" > "$temporary"; then
    rm -f "$temporary"
    return 1
  fi
  mv -f "$temporary" "$ENV_FILE"
}

legacy_secret="$(env_value JWT_SECRET)"
keyset_directory="$(env_value JWT_SECRETS_DIR)"
keyset_directory="${keyset_directory:-/var/lib/surau/secrets/jwt}"
if [[ "$keyset_directory" != /* ]]; then
  echo "JWT bootstrap rejected: JWT_SECRETS_DIR must be absolute" >&2
  exit 1
fi
keyset_file="$keyset_directory/keyset.json"

# Existing production secrets are generated URL-safe/hex strings. Refuse
# quoted, whitespace-bearing, shell-like, or multi-line dotenv values because
# copying those textually could change the bytes Compose gave the old app.
require_safe_legacy_secret() {
  if (( ${#legacy_secret} < 32 )) ||
     [[ ! "$legacy_secret" =~ ^[A-Za-z0-9_./:@%+=-]+$ ]]; then
    echo "JWT bootstrap rejected: JWT_SECRET must be an unquoted single-line safe value of at least 32 bytes" >&2
    exit 1
  fi
}

mfa_key="$(env_value MFA_ENCRYPTION_KEY)"
unsubscribe_secret="$(env_value EMAIL_UNSUBSCRIBE_TOKEN_SECRET)"
unsubscribe_keyset="$(env_value EMAIL_UNSUBSCRIBE_TOKEN_SECRETS)"
if [[ -z "$mfa_key" || ( -z "$unsubscribe_secret" && -z "$unsubscribe_keyset" ) ]] ||
   ! sudo test -f "$keyset_file"; then
  require_safe_legacy_secret
fi

# On the first A-4 upgrade, prove the dotenv seed is byte-identical to the
# signer already serving users. This catches shell-env/Compose drift before a
# mismatched keyset can replace the living process. No secret or hash is output.
if ! sudo test -f "$keyset_file"; then
  running_app_id="$(sudo -E docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" ps -q app)"
  if [[ -z "$running_app_id" ]]; then
    if [[ "$ALLOW_NO_RUNNING_SIGNER" != true ]]; then
      echo "JWT bootstrap rejected: no running legacy app signer was found" >&2
      exit 1
    fi
  elif [[ ! "$running_app_id" =~ ^[0-9a-f]{12,64}$ ]]; then
    echo "JWT bootstrap rejected: running app container ID is invalid" >&2
    exit 1
  else
    running_secret="$(
      sudo docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$running_app_id" \
        | sed -n 's/^JWT_SECRET=//p' | tail -1
    )"
    if [[ "$running_secret" != "$legacy_secret" ]]; then
      unset running_secret legacy_secret
      echo "JWT bootstrap rejected: dotenv JWT_SECRET differs from the living signer" >&2
      exit 1
    fi
    unset running_secret
  fi
fi

if [[ -z "$mfa_key" ]]; then
  set_env_value MFA_ENCRYPTION_KEY "$legacy_secret"
fi
if [[ -z "$unsubscribe_secret" && -z "$unsubscribe_keyset" ]]; then
  set_env_value EMAIL_UNSUBSCRIBE_TOKEN_SECRET "$legacy_secret"
fi

set_env_value JWT_SECRETS_DIR "$keyset_directory"
set_env_value JWT_KEYSET_FILE /run/secrets/surau-jwt/keyset.json

sudo install -d -o root -g root -m 0700 "$keyset_directory"

run_keyset_cli() {
  sudo -E docker run --rm \
    --network none \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --user 0:0 \
    --env JWT_SECRET \
    --volume "$keyset_directory:/keys" \
    --entrypoint /jwt-keyset \
    "$APP_IMAGE" "$@"
}

if sudo test -e "$keyset_file"; then
  if [[ -n "$legacy_secret" ]]; then
    unset legacy_secret JWT_SECRET
  fi
  run_keyset_cli status --file /keys/keyset.json >/dev/null
  echo "JWT keyset bootstrap already complete; existing keyset validated"
  exit 0
fi

legacy_kid="legacy-${APP_ENV}-$(date -u +%Y%m%dT%H%M%SZ)"
export JWT_SECRET="$legacy_secret"
run_keyset_cli bootstrap --file /keys/keyset.json --kid "$legacy_kid" >/dev/null
unset legacy_secret JWT_SECRET

run_keyset_cli status --file /keys/keyset.json >/dev/null
echo "JWT keyset bootstrap complete; derived MFA and unsubscribe keys are pinned"
