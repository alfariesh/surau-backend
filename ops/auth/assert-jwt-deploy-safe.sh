#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="${ENV_FILE:-.env.production}"
APP_ENV="${APP_ENV:-}"

if [[ ! "$APP_ENV" =~ ^(dev|prod)$ ]]; then
  echo "JWT deploy guard rejected: APP_ENV must be dev or prod" >&2
  exit 1
fi
if [[ ! -f "$ENV_FILE" ]]; then
  echo "JWT deploy guard rejected: env file is missing" >&2
  exit 1
fi

env_value() {
  local key="$1"
  sed -n "s/^${key}=//p" "$ENV_FILE" | tail -1
}

JWT_DIRECTORY="$(env_value JWT_SECRETS_DIR)"
JWT_DIRECTORY="${JWT_DIRECTORY:-/var/lib/surau/secrets/jwt}"
APP_IMAGE="$(env_value APP_IMAGE)"
APP_IMAGE="${APP_IMAGE:-surau-backend:latest}"
KEYSET_FILE="$JWT_DIRECTORY/keyset.json"
STATE_FILE="$JWT_DIRECTORY/drill-${APP_ENV}.env"

if [[ "$JWT_DIRECTORY" != /* || ! "$APP_IMAGE" =~ ^[A-Za-z0-9_./:@-]+$ ]]; then
  echo "JWT deploy guard rejected: secret directory or image is invalid" >&2
  exit 1
fi
if ! sudo test -f "$KEYSET_FILE"; then
  if sudo test -e "$STATE_FILE"; then
    echo "JWT deploy guard rejected: drill state exists without a keyset" >&2
    exit 1
  fi
  echo capture-legacy
  exit 0
fi

status="$(sudo docker run --rm \
  --network none \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --user 0:0 \
  --volume "$JWT_DIRECTORY:/keys" \
  --entrypoint /jwt-keyset \
  "$APP_IMAGE" status --file /keys/keyset.json)"
rotation_state="$(sed -n 's/.*"state": "\([a-z_]*\)".*/\1/p' <<<"$status" | head -1)"

case "$rotation_state" in
  stable)
    # Initial A-4 bridge. capture-legacy itself validates or safely refreshes
    # the strictly scoped canary before the living process is replaced.
    echo capture-legacy
    ;;
  retired)
    if sudo test -e "$STATE_FILE"; then
      echo "JWT deploy guard rejected: retired drill cleanup is incomplete" >&2
      exit 1
    fi
    echo safe
    ;;
  prepared|active|rolled_back)
    echo "JWT deploy guard rejected: key rotation is still in progress" >&2
    exit 1
    ;;
  *)
    echo "JWT deploy guard rejected: keyset status is invalid" >&2
    exit 1
    ;;
esac
