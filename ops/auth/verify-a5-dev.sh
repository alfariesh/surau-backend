#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="${ENV_FILE:-.env.production}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"
APP_ENV="${APP_ENV:-}"
API_URL="${API_URL:-}"

if [[ "$APP_ENV" != dev ]]; then
  echo "A-5 canary rejected: APP_ENV must be dev" >&2
  exit 1
fi
if [[ ! -f "$ENV_FILE" ]]; then
  echo "A-5 canary rejected: env file is missing" >&2
  exit 1
fi
if [[ ! "$API_URL" =~ ^http://127\.0\.0\.1:(18080|18081)$ ]]; then
  echo "A-5 canary rejected: API_URL must be the active loopback dev slot" >&2
  exit 1
fi

WORK_DIR="$(mktemp -d /tmp/surau-a5-dev.XXXXXX)"
API_REQUEST="$WORK_DIR/request.json"
API_RESPONSE="$WORK_DIR/response.json"
CANARY_EMAIL=""

compose() {
  sudo -E docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

db_psql() {
  # Expansion is intentionally deferred to the database container and stdin
  # stays detached because deploy runs inside an SSH heredoc.
  # shellcheck disable=SC2016
  compose exec -T db sh -c \
    'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' sh "$@" \
    </dev/null
}

cleanup() {
  set +e
  if [[ "$CANARY_EMAIL" =~ ^a5-dev-canary-[0-9]{14}-[a-f0-9]{12}@example\.invalid$ ]]; then
    db_psql -c "
      UPDATE email_messages
         SET status = 'skipped', error = 'A-5 dev canary', updated_at = now()
       WHERE lower(recipient_email) = lower('$CANARY_EMAIL') AND status = 'queued';
      DELETE FROM users WHERE email = '$CANARY_EMAIL' AND role = 'user';
    " >/dev/null
  fi
  rm -f "$API_REQUEST" "$API_RESPONSE"
  rmdir "$WORK_DIR" 2>/dev/null || true
}
trap cleanup EXIT

api_request() {
  local method="$1"
  local path="$2"
  local expected_status="$3"
  local body="${4:-}"
  local access_token="${5:-}"
  local user_agent="${6:-}"
  local status
  local args=(
    --silent --show-error --connect-timeout 5 --max-time 20
    --request "$method" --output "$API_RESPONSE" --write-out '%{http_code}'
    --user-agent "$user_agent"
  )

  : > "$API_RESPONSE"
  chmod 0600 "$API_RESPONSE"
  if [[ -n "$body" ]]; then
    printf '%s' "$body" > "$API_REQUEST"
    chmod 0600 "$API_REQUEST"
    args+=(--header 'Content-Type: application/json' --data-binary "@$API_REQUEST")
  fi
  if [[ -n "$access_token" ]]; then
    args+=(--header "Authorization: Bearer $access_token")
  fi

  status="$(curl "${args[@]}" "$API_URL$path")"
  : > "$API_REQUEST"
  if [[ "$status" != "$expected_status" ]]; then
    echo "A-5 canary $method $path returned $status; expected $expected_status" >&2
    exit 1
  fi
}

json_string_field() {
  local field="$1"
  sed -n "s/.*\"${field}\":\"\([^\"]*\)\".*/\1/p" "$API_RESPONSE" | head -1
}

json_integer_field() {
  local field="$1"
  sed -n "s/.*\"${field}\":\([0-9][0-9]*\).*/\1/p" "$API_RESPONSE" | head -1
}

assert_uuid() {
  [[ "$1" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]]
}

CANARY_EMAIL="a5-dev-canary-$(date -u +%Y%m%d%H%M%S)-$(openssl rand -hex 6)@example.invalid"
CANARY_PASSWORD="$(openssl rand -hex 32)"
KNOWN_UA='Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/126.0.0.0 Safari/537.36'

api_request POST /v1/auth/register 201 \
  "{\"name\":\"A-5 Dev Canary\",\"email\":\"$CANARY_EMAIL\",\"password\":\"$CANARY_PASSWORD\"}"
db_psql -c "
  UPDATE users
     SET email_verified = true, email_verified_at = now(), updated_at = now()
   WHERE email = '$CANARY_EMAIL' AND role = 'user';
  UPDATE email_verification_tokens
     SET used_at = COALESCE(used_at, now())
   WHERE user_id = (SELECT id FROM users WHERE email = '$CANARY_EMAIL');
  UPDATE email_messages
     SET status = 'skipped', error = 'A-5 dev canary', updated_at = now()
   WHERE lower(recipient_email) = lower('$CANARY_EMAIL') AND status = 'queued';
" >/dev/null

api_request POST /v1/auth/login 200 \
  "{\"email\":\"$CANARY_EMAIL\",\"password\":\"$CANARY_PASSWORD\"}" "" "$KNOWN_UA"
KNOWN_ACCESS_TOKEN="$(json_string_field access_token)"
KNOWN_REFRESH_TOKEN="$(json_string_field refresh_token)"
KNOWN_SESSION_ID="$(json_string_field session_id)"
assert_uuid "$KNOWN_SESSION_ID"
[[ -n "$KNOWN_ACCESS_TOKEN" && -n "$KNOWN_REFRESH_TOKEN" ]]

# A second client deliberately omits metadata. Its public label must be a fixed
# safe fallback rather than raw/malformed client input.
api_request POST /v1/auth/login 200 \
  "{\"email\":\"$CANARY_EMAIL\",\"password\":\"$CANARY_PASSWORD\"}" "" ""
UNKNOWN_SESSION_ID="$(json_string_field session_id)"
assert_uuid "$UNKNOWN_SESSION_ID"

api_request GET /v1/auth/sessions 200 "" "$KNOWN_ACCESS_TOKEN" "$KNOWN_UA"
grep -Fq '"device_label":"Chrome di Mac"' "$API_RESPONSE"
grep -Fq '"device_label":"Perangkat tidak dikenal"' "$API_RESPONSE"
[[ "$(json_integer_field total)" == 2 ]]

NEW_LOGIN_NOTICES="$(db_psql -Atc \
  "SELECT count(*) FROM email_messages WHERE lower(recipient_email) = lower('$CANARY_EMAIL') AND template_key = 'auth_new_login';")"
if [[ ! "$NEW_LOGIN_NOTICES" =~ ^[0-9]+$ || "$NEW_LOGIN_NOTICES" -lt 1 ]]; then
  echo "A-5 canary did not preserve the new-device notification" >&2
  exit 1
fi
db_psql -c "
  UPDATE email_messages
     SET status = 'skipped', error = 'A-5 dev canary', updated_at = now()
   WHERE lower(recipient_email) = lower('$CANARY_EMAIL') AND status = 'queued';
" >/dev/null

# Turn the known session into a legacy 720h row that was active 13 days ago.
# It must still refresh and its successor must receive a fresh exact 336h span.
db_psql -c "
  UPDATE auth_sessions
     SET last_used_at = now() - interval '13 days',
         expires_at = now() + interval '30 days'
   WHERE family_id = '$KNOWN_SESSION_ID' AND revoked_at IS NULL;
" >/dev/null
api_request POST /v1/auth/refresh 200 \
  "{\"refresh_token\":\"$KNOWN_REFRESH_TOKEN\"}" "" "$KNOWN_UA"
ROTATED_REFRESH_TOKEN="$(json_string_field refresh_token)"
[[ -n "$ROTATED_REFRESH_TOKEN" ]]

SUCCESSOR_WINDOW="$(db_psql -Atc \
  "SELECT EXTRACT(EPOCH FROM (expires_at - last_used_at))::bigint FROM auth_sessions WHERE family_id = '$KNOWN_SESSION_ID' AND revoked_at IS NULL;")"
if [[ "$SUCCESSOR_WINDOW" != 1209600 ]]; then
  echo "A-5 canary successor window is $SUCCESSOR_WINDOW seconds; expected 1209600" >&2
  exit 1
fi

# A legacy row idle beyond 14 days is an ordinary expiry: hidden, rejected,
# and not recorded as refresh-token reuse.
db_psql -c "
  UPDATE auth_sessions
     SET last_used_at = now() - interval '14 days 1 minute',
         expires_at = now() + interval '30 days'
   WHERE family_id = '$KNOWN_SESSION_ID' AND revoked_at IS NULL;
" >/dev/null
REUSE_BEFORE="$(db_psql -Atc \
  "SELECT count(*) FROM auth_audit_logs WHERE event = 'refresh_reuse_detected' AND user_id = (SELECT id FROM users WHERE email = '$CANARY_EMAIL');")"
api_request GET /v1/auth/sessions 200 "" "$KNOWN_ACCESS_TOKEN" "$KNOWN_UA"
[[ "$(json_integer_field total)" == 1 ]]
api_request POST /v1/auth/refresh 401 \
  "{\"refresh_token\":\"$ROTATED_REFRESH_TOKEN\"}" "" "$KNOWN_UA"
REUSE_AFTER="$(db_psql -Atc \
  "SELECT count(*) FROM auth_audit_logs WHERE event = 'refresh_reuse_detected' AND user_id = (SELECT id FROM users WHERE email = '$CANARY_EMAIL');")"
if [[ "$REUSE_AFTER" != "$REUSE_BEFORE" ]]; then
  echo "A-5 canary misclassified ordinary inactivity expiry as token reuse" >&2
  exit 1
fi

echo "A-5 dev canary passed: sliding 336h, legacy compatibility, idle rejection, safe labels, and new-device notice"
