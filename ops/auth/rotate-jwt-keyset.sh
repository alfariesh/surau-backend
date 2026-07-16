#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-}"
ENV_FILE="${ENV_FILE:-.env.production}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"
APP_ENV="${APP_ENV:-}"
EXPECTED_VERSION="${EXPECTED_VERSION:-}"
APP_IMAGE="${APP_IMAGE:-}"
API_URL="${API_URL:-http://127.0.0.1:8080}"

case "$MODE" in
  capture-legacy|prepare|activate-drill|rollback|retire-prepare|retire-finalize|status|cleanup) ;;
  *)
    echo "usage: $0 capture-legacy|prepare|activate-drill|rollback|retire-prepare|retire-finalize|status|cleanup" >&2
    exit 2
    ;;
esac
if [[ ! -f "$ENV_FILE" ]]; then
  echo "JWT rotation rejected: env file is missing" >&2
  exit 1
fi
if [[ ! "$APP_ENV" =~ ^(dev|prod)$ ]]; then
  echo "JWT rotation rejected: APP_ENV must be dev or prod" >&2
  exit 1
fi
if [[ "$APP_ENV" == prod ]]; then
  PUBLIC_API_URL=https://api.surau.org
else
  # dev-api is a direct-origin/backend drill. The only configured Worker route
  # is production, so dev keys must never be uploaded to that Worker.
  PUBLIC_API_URL=https://dev-api.surau.org
fi
if [[ -z "$EXPECTED_VERSION" ]]; then
  echo "JWT rotation rejected: EXPECTED_VERSION is required" >&2
  exit 1
fi

env_value() {
  local key="$1"
  sed -n "s/^${key}=//p" "$ENV_FILE" | tail -1
}

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

APP_IMAGE="${APP_IMAGE:-$(env_value APP_IMAGE)}"
APP_IMAGE="${APP_IMAGE:-surau-backend:latest}"
if [[ ! "$APP_IMAGE" =~ ^[A-Za-z0-9_./:@-]+$ ]]; then
  echo "JWT rotation rejected: APP_IMAGE contains unsupported characters" >&2
  exit 1
fi

JWT_DIRECTORY="$(env_value JWT_SECRETS_DIR)"
JWT_DIRECTORY="${JWT_DIRECTORY:-/var/lib/surau/secrets/jwt}"
if [[ "$JWT_DIRECTORY" != /* ]]; then
  echo "JWT rotation rejected: JWT_SECRETS_DIR must be absolute" >&2
  exit 1
fi
KEYSET_FILE="$JWT_DIRECTORY/keyset.json"
STATE_FILE="$JWT_DIRECTORY/drill-${APP_ENV}.env"
EVIDENCE_FILE="$JWT_DIRECTORY/drill-${APP_ENV}-evidence.log"
WORKER_EXPORT_FILE="$JWT_DIRECTORY/worker-keyset.json"

compose() {
  sudo -E docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

db_psql() {
  # Expansion is intentionally deferred to the database container.
  # stdin must stay detached: deploy workflows run this script inside an SSH
  # heredoc, and `docker compose exec -T` would otherwise consume every remote
  # command that follows the rotation call while still returning success.
  # shellcheck disable=SC2016
  compose exec -T db sh -c \
    'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' sh "$@" \
    </dev/null
}

run_keyset_cli() {
  sudo docker run --rm \
    --network none \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --user 0:0 \
    --volume "$JWT_DIRECTORY:/keys" \
    --entrypoint /jwt-keyset \
    "$APP_IMAGE" "$@"
}

app_container_id() {
  compose ps -q app
}

app_container_started_at() {
  sudo docker inspect --format '{{.State.StartedAt}}' "$1"
}

app_container_restart_count() {
  sudo docker inspect --format '{{.RestartCount}}' "$1"
}

app_container_env_value() {
  local container_id="$1"
  local key="$2"
  sudo docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$container_id" \
    | sed -n "s/^${key}=//p" | tail -1
}

assert_living_app_reload_contract() {
  local container_id prefork_mode keyset_path
  container_id="$(app_container_id)"
  if [[ -z "$container_id" ]]; then
    echo "JWT rotation rejected: app container is unavailable" >&2
    exit 1
  fi
  prefork_mode="$(app_container_env_value "$container_id" HTTP_USE_PREFORK_MODE)"
  if [[ "$prefork_mode" != false ]]; then
    echo "JWT rotation rejected: living app must use HTTP_USE_PREFORK_MODE=false" >&2
    exit 1
  fi

  # The pre-A-4 process used for the one-time no-kid capture has no keyset
  # variable yet. Every reloadable A-4 phase must prove that the living process
  # reads the exact read-only mount targeted by Compose.
  if [[ "$MODE" != capture-legacy ]]; then
    keyset_path="$(app_container_env_value "$container_id" JWT_KEYSET_FILE)"
    if [[ "$keyset_path" != /run/secrets/surau-jwt/keyset.json ]]; then
      echo "JWT rotation rejected: living app JWT_KEYSET_FILE does not match the mounted keyset" >&2
      exit 1
    fi
  fi
}

assert_expected_version() {
  local version
  version="$(curl --fail --silent --show-error --max-time 10 "$API_URL/version")"
  if [[ "$version" != *"\"version\":\"$EXPECTED_VERSION\""* ]]; then
    echo "JWT rotation rejected: deployed version does not match EXPECTED_VERSION" >&2
    exit 1
  fi
  curl --fail --silent --show-error --max-time 10 "$API_URL/healthz" >/dev/null
  curl --fail --silent --show-error --max-time 10 "$API_URL/readyz" >/dev/null
}

assert_same_container() {
  local current started_at restart_count
  current="$(app_container_id)"
  if [[ -z "$current" ]]; then
    echo "JWT drill rejected: app container is unavailable" >&2
    exit 1
  fi
  started_at="$(app_container_started_at "$current")"
  restart_count="$(app_container_restart_count "$current")"
  if [[ "$current" != "$DRILL_CONTAINER_ID" ||
        "$started_at" != "$DRILL_CONTAINER_STARTED_AT" ||
        "$restart_count" != "$DRILL_CONTAINER_RESTART_COUNT" ]]; then
    echo "JWT drill rejected: app container changed or restarted during the rotation window" >&2
    exit 1
  fi
}

reload_app() {
  local before after before_started before_restarts after_started after_restarts
  local reload_started status active_kid legacy_kid key_count expected_log logs
  before="$(app_container_id)"
  if [[ -z "$before" ]]; then
    echo "JWT reload rejected: app container is unavailable" >&2
    exit 1
  fi
  before_started="$(app_container_started_at "$before")"
  before_restarts="$(app_container_restart_count "$before")"
  status="$(run_keyset_cli status --file /keys/keyset.json)"
  active_kid="$(sed -n 's/.*"active_kid": "\([^"]*\)".*/\1/p' <<<"$status" | head -1)"
  legacy_kid="$(sed -n 's/.*"legacy_kid": "\([^"]*\)".*/\1/p' <<<"$status" | head -1)"
  key_count="$(awk '
    /"key_ids": \[/ { inside = 1; next }
    inside && /]/ { print count + 0; exit }
    inside && /"/ { count++ }
  ' <<<"$status")"
  if [[ ! "$active_kid" =~ ^[A-Za-z0-9_-]{1,64}$ || ! "$key_count" =~ ^[12]$ ]]; then
    echo "JWT reload rejected: sanitized keyset status is invalid" >&2
    exit 1
  fi
  expected_log="JWT keyset reloaded: active_kid=$active_kid legacy_kid=$legacy_kid key_count=$key_count"
  reload_started="$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)"
  sudo docker kill --signal HUP "$before" >/dev/null
  for _ in {1..20}; do
    after="$(app_container_id)"
    after_started="$(app_container_started_at "$after")"
    after_restarts="$(app_container_restart_count "$after")"
    logs="$(compose logs --no-color --since "$reload_started" app 2>/dev/null || true)"
    if [[ "$after" == "$before" && "$after_started" == "$before_started" &&
          "$after_restarts" == "$before_restarts" ]] &&
       curl --fail --silent --show-error --max-time 5 "$API_URL/readyz" >/dev/null &&
       grep -Fq "$expected_log" <<<"$logs"; then
      return
    fi
    sleep 0.25
  done
  echo "JWT reload failed or recreated the app container" >&2
  exit 1
}

TMP_DIR="$(mktemp -d)"
chmod 0700 "$TMP_DIR"
trap 'rm -rf "$TMP_DIR"' EXIT
API_RESPONSE="$TMP_DIR/response.json"
API_REQUEST="$TMP_DIR/request.json"
API_HEADER="$TMP_DIR/header.txt"
API_RESPONSE_HEADER="$TMP_DIR/response-header.txt"
SESSION_SNAPSHOT_STARTED_AT=""
SESSION_SNAPSHOT_COUNT=0

api_request() {
  local method="$1"
  local path="$2"
  local expected_status="$3"
  local body="${4:-}"
  local access_token="${5:-}"
  local status
  local args=(
    --silent --show-error --connect-timeout 5 --max-time 20
    --request "$method" --output "$API_RESPONSE" --write-out '%{http_code}'
  )

  : > "$API_RESPONSE"
  chmod 0600 "$API_RESPONSE"
  if [[ -n "$body" ]]; then
    printf '%s' "$body" > "$API_REQUEST"
    chmod 0600 "$API_REQUEST"
    args+=(--header 'Content-Type: application/json' --data-binary "@$API_REQUEST")
  fi
  if [[ -n "$access_token" ]]; then
    printf 'Authorization: Bearer %s\n' "$access_token" > "$API_HEADER"
    chmod 0600 "$API_HEADER"
    args+=(--header "@$API_HEADER")
  fi

  status="$(curl "${args[@]}" "$API_URL$path")"
  : > "$API_REQUEST"
  : > "$API_HEADER"
  if [[ "$status" != "$expected_status" ]]; then
    echo "JWT canary request $method $path returned $status; expected $expected_status" >&2
    exit 1
  fi
}

worker_identity_smoke() {
  local access_token="$1"
  local expected_identity="$2"
  local status identity
  if [[ "$APP_ENV" != prod ]]; then
    return
  fi
  printf 'Authorization: Bearer %s\n' "$access_token" > "$API_HEADER"
  printf '%s' '{"question":"jwt rotation smoke"}' > "$API_REQUEST"
  chmod 0600 "$API_HEADER" "$API_REQUEST"
  : > "$API_RESPONSE_HEADER"
  status="$(curl --silent --show-error --connect-timeout 5 --max-time 20 \
    --request POST --output "$API_RESPONSE" --dump-header "$API_RESPONSE_HEADER" \
    --write-out '%{http_code}' --header "@$API_HEADER" \
    --header 'Content-Type: application/json' --data-binary "@$API_REQUEST" \
    "$PUBLIC_API_URL/v1/books/0/rag")"
  : > "$API_HEADER"
  : > "$API_REQUEST"
  identity="$(awk '
    tolower($1) == "x-surau-jwt-identity:" { value = $2; sub(/\r$/, "", value) }
    END { print value }
  ' "$API_RESPONSE_HEADER")"
  if [[ ! "$status" =~ ^[234][0-9][0-9]$ || "$identity" != "$expected_identity" ]]; then
    echo "JWT Worker live verifier smoke failed closed" >&2
    exit 1
  fi
}

public_auth_probe() {
  local access_token="$1"
  local expected_status="$2"
  local token_class="$3"
  local status request_id
  if [[ ! "$token_class" =~ ^[a-z0-9-]{1,40}$ || ! "$expected_status" =~ ^[0-9]{3}$ ]]; then
    echo "JWT public-route probe arguments are invalid" >&2
    exit 1
  fi
  printf 'Authorization: Bearer %s\n' "$access_token" > "$API_HEADER"
  chmod 0600 "$API_HEADER"
  : > "$API_RESPONSE_HEADER"
  status="$(curl --silent --show-error --connect-timeout 5 --max-time 20 \
    --request GET --output "$API_RESPONSE" --dump-header "$API_RESPONSE_HEADER" \
    --write-out '%{http_code}' --header "@$API_HEADER" \
    "$PUBLIC_API_URL/v1/auth/sessions")"
  : > "$API_HEADER"
  request_id="$(awk '
    tolower($1) == "x-request-id:" { value = $2; sub(/\r$/, "", value) }
    END { print value }
  ' "$API_RESPONSE_HEADER")"
  if [[ ! "$request_id" =~ ^[A-Za-z0-9._:-]{1,128}$ ]]; then
    request_id=absent
  fi
  if [[ "$status" != "$expected_status" ]]; then
    echo "JWT public route rejected $token_class with $status; expected $expected_status" >&2
    exit 1
  fi
  printf '%s event=public-auth-probe environment=%s token_class=%s status=%s request_id=%s\n' \
    "$(date -u +%FT%TZ)" "$APP_ENV" "$token_class" "$status" "$request_id" \
    | sudo tee -a "$EVIDENCE_FILE" >/dev/null
  sudo chown root:root "$EVIDENCE_FILE"
  sudo chmod 0600 "$EVIDENCE_FILE"
}

original_token_class() {
  if [[ "$LEGACY_WAS_NO_KID" == true ]]; then
    printf '%s\n' legacy-no-kid
  else
    printf '%s\n' old-kid-original
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

token_kid() {
  local token="$1"
  local segment padding header
  segment="${token%%.*}"
  segment="${segment//-/+}"
  segment="${segment//_/\/}"
  padding=$(( (4 - ${#segment} % 4) % 4 ))
  segment+="$(printf '%*s' "$padding" '' | tr ' ' '=')"
  header="$(printf '%s' "$segment" | base64 --decode 2>/dev/null || true)"
  sed -n 's/.*"kid":"\([A-Za-z0-9_-]*\)".*/\1/p' <<<"$header" | head -1
}

write_state() {
  local temporary
  for value in "$CANARY_EMAIL" "$CANARY_PASSWORD" "$OLD_ACCESS_TOKEN" \
    "$OLD_KID_ACCESS_TOKEN" \
    "$CURRENT_ACCESS_TOKEN" "$CURRENT_REFRESH_TOKEN" "$SESSION_ID" \
    "$OLD_KID" "$NEW_KID" "$DRILL_CONTAINER_ID" "$DRILL_CONTAINER_STARTED_AT" \
    "$DRILL_CONTAINER_RESTART_COUNT" "$SESSION_TOTAL_BASELINE" "$RUNTIME_FINALIZED"; do
    if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
      echo "JWT state rejected: unexpected newline" >&2
      exit 1
    fi
  done
  temporary="$(sudo mktemp "${STATE_FILE}.tmp.XXXXXX")"
  {
    printf 'CANARY_EMAIL=%s\n' "$CANARY_EMAIL"
    printf 'CANARY_PASSWORD=%s\n' "$CANARY_PASSWORD"
    printf 'OLD_ACCESS_TOKEN=%s\n' "$OLD_ACCESS_TOKEN"
    printf 'OLD_KID_ACCESS_TOKEN=%s\n' "$OLD_KID_ACCESS_TOKEN"
    printf 'CURRENT_ACCESS_TOKEN=%s\n' "$CURRENT_ACCESS_TOKEN"
    printf 'CURRENT_REFRESH_TOKEN=%s\n' "$CURRENT_REFRESH_TOKEN"
    printf 'SESSION_ID=%s\n' "$SESSION_ID"
    printf 'OLD_KID=%s\n' "$OLD_KID"
    printf 'NEW_KID=%s\n' "$NEW_KID"
    printf 'DRILL_CONTAINER_ID=%s\n' "$DRILL_CONTAINER_ID"
    printf 'DRILL_CONTAINER_STARTED_AT=%s\n' "$DRILL_CONTAINER_STARTED_AT"
    printf 'DRILL_CONTAINER_RESTART_COUNT=%s\n' "$DRILL_CONTAINER_RESTART_COUNT"
    printf 'SESSION_TOTAL_BASELINE=%s\n' "$SESSION_TOTAL_BASELINE"
    printf 'LEGACY_WAS_NO_KID=%s\n' "$LEGACY_WAS_NO_KID"
    printf 'RUNTIME_FINALIZED=%s\n' "$RUNTIME_FINALIZED"
  } | sudo tee "$temporary" >/dev/null
  sudo chown root:root "$temporary"
  sudo chmod 0600 "$temporary"
  sudo mv -f "$temporary" "$STATE_FILE"
}

state_value() {
  local key="$1"
  sudo sed -n "s/^${key}=//p" "$STATE_FILE" | tail -1
}

load_state() {
  if ! sudo test -f "$STATE_FILE"; then
    return 1
  fi
  CANARY_EMAIL="$(state_value CANARY_EMAIL)"
  CANARY_PASSWORD="$(state_value CANARY_PASSWORD)"
  OLD_ACCESS_TOKEN="$(state_value OLD_ACCESS_TOKEN)"
  OLD_KID_ACCESS_TOKEN="$(state_value OLD_KID_ACCESS_TOKEN)"
  CURRENT_ACCESS_TOKEN="$(state_value CURRENT_ACCESS_TOKEN)"
  CURRENT_REFRESH_TOKEN="$(state_value CURRENT_REFRESH_TOKEN)"
  SESSION_ID="$(state_value SESSION_ID)"
  OLD_KID="$(state_value OLD_KID)"
  NEW_KID="$(state_value NEW_KID)"
  DRILL_CONTAINER_ID="$(state_value DRILL_CONTAINER_ID)"
  DRILL_CONTAINER_STARTED_AT="$(state_value DRILL_CONTAINER_STARTED_AT)"
  DRILL_CONTAINER_RESTART_COUNT="$(state_value DRILL_CONTAINER_RESTART_COUNT)"
  SESSION_TOTAL_BASELINE="$(state_value SESSION_TOTAL_BASELINE)"
  LEGACY_WAS_NO_KID="$(state_value LEGACY_WAS_NO_KID)"
  RUNTIME_FINALIZED="$(state_value RUNTIME_FINALIZED)"
  if [[ ! "$CANARY_EMAIL" =~ ^jwt-rotation-canary-(dev|prod)-[0-9]{14}@example\.invalid$ ||
        ! "$CANARY_PASSWORD" =~ ^[0-9a-f]{64}$ ||
        ! "$OLD_ACCESS_TOKEN" =~ ^[A-Za-z0-9._-]+$ ||
        ! "$OLD_KID_ACCESS_TOKEN" =~ ^[A-Za-z0-9._-]*$ ||
        ! "$CURRENT_ACCESS_TOKEN" =~ ^[A-Za-z0-9._-]+$ ||
        ! "$CURRENT_REFRESH_TOKEN" =~ ^[A-Za-z0-9._-]+$ ||
        ! "$SESSION_ID" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ||
        ! "$OLD_KID" =~ ^[A-Za-z0-9_-]{0,64}$ ||
        ! "$NEW_KID" =~ ^[A-Za-z0-9_-]{0,64}$ ||
        ! "$DRILL_CONTAINER_ID" =~ ^[0-9a-f]{12,64}$ ||
        ! "$DRILL_CONTAINER_STARTED_AT" =~ ^[0-9TZ:._+-]+$ ||
        ! "$DRILL_CONTAINER_RESTART_COUNT" =~ ^[0-9]+$ ||
        ! "$SESSION_TOTAL_BASELINE" =~ ^[0-9]+$ ||
        ! "$LEGACY_WAS_NO_KID" =~ ^(true|false)$ ||
        ! "$RUNTIME_FINALIZED" =~ ^(true|false)$ ]]; then
    echo "JWT drill state failed strict validation" >&2
    exit 1
  fi
  return 0
}

record_evidence() {
  local event="$1"
  local active_kid="$2"
  local legacy_mode="$3"
  printf '%s event=%s version=%s active_kid=%s legacy_no_kid=%s container_unchanged=true container_restarts=0 session_family_active=true other_active_session_snapshot=%s unreplaced_session_revocations=0 unexpected_canary_401=0 runtime_finalized=%s\n' \
    "$(date -u +%FT%TZ)" "$event" "$EXPECTED_VERSION" "$active_kid" "$legacy_mode" "$SESSION_SNAPSHOT_COUNT" "$RUNTIME_FINALIZED" \
    | sudo tee -a "$EVIDENCE_FILE" >/dev/null
  sudo chown root:root "$EVIDENCE_FILE"
  sudo chmod 0600 "$EVIDENCE_FILE"
}

assert_active_session_family() {
  local active_count
  active_count="$(db_psql -Atc \
    "SELECT count(*) FROM auth_sessions WHERE family_id = '$SESSION_ID' AND revoked_at IS NULL AND expires_at > now();")"
  if [[ "$active_count" != "1" ]]; then
    echo "JWT canary session family is not active exactly once" >&2
    exit 1
  fi
}

assert_session_total() {
  local total
  api_request GET /v1/auth/sessions 200 "" "$CURRENT_ACCESS_TOKEN"
  total="$(json_integer_field total)"
  if [[ -z "$total" || "$total" != "$SESSION_TOTAL_BASELINE" ]]; then
    echo "JWT canary active-session total changed during drill" >&2
    exit 1
  fi
}

snapshot_other_active_sessions() {
  local snapshot
  snapshot="$(db_psql -Atc \
    "SELECT statement_timestamp()::timestamp(6)::text || '|' || count(*)::text
       FROM auth_sessions
      WHERE revoked_at IS NULL
        AND family_id <> '$SESSION_ID';")"
  SESSION_SNAPSHOT_STARTED_AT="${snapshot%%|*}"
  SESSION_SNAPSHOT_COUNT="${snapshot#*|}"
  if [[ ! "$SESSION_SNAPSHOT_STARTED_AT" =~ ^[0-9-]+\ [0-9:.]+$ ||
        ! "$SESSION_SNAPSHOT_COUNT" =~ ^[0-9]+$ ]]; then
    echo "JWT drill could not create a safe active-session snapshot" >&2
    exit 1
  fi
}

assert_snapshot_sessions_not_revoked() {
  local revoked_count
  if [[ -z "$SESSION_SNAPSHOT_STARTED_AT" ]]; then
    echo "JWT drill active-session snapshot is missing" >&2
    exit 1
  fi
  revoked_count="$(db_psql -Atc \
    "SELECT count(*)
      FROM auth_sessions
      WHERE family_id <> '$SESSION_ID'
        AND revoked_at >= '$SESSION_SNAPSHOT_STARTED_AT'::timestamp
        AND replaced_by_id IS NULL;")"
  if [[ "$revoked_count" != "0" ]]; then
    echo "JWT drill detected a pre-existing session revoked during this phase" >&2
    exit 1
  fi
}

refresh_canary() {
  local expected_kid="$1"
  local access refresh session kid
  api_request POST /v1/auth/refresh 200 \
    "{\"refresh_token\":\"$CURRENT_REFRESH_TOKEN\"}"
  access="$(json_string_field access_token)"
  refresh="$(json_string_field refresh_token)"
  session="$(json_string_field session_id)"
  kid="$(token_kid "$access")"
  if [[ -z "$access" || -z "$refresh" || "$session" != "$SESSION_ID" || "$kid" != "$expected_kid" ]]; then
    echo "JWT canary refresh returned an unexpected token family or signer" >&2
    exit 1
  fi
  CURRENT_ACCESS_TOKEN="$access"
  CURRENT_REFRESH_TOKEN="$refresh"
  # Persist each single-use refresh successor immediately. A later phase or
  # workflow retry must never reuse the just-consumed refresh token.
  write_state
}

create_canary() {
  local required_kid_mode="${1:-any}"
  local user_row
  CANARY_EMAIL="jwt-rotation-canary-${APP_ENV}-$(date -u +%Y%m%d%H%M%S)@example.invalid"
  CANARY_PASSWORD="$(openssl rand -hex 32)"
  api_request POST /v1/auth/register 201 \
    "{\"name\":\"JWT Rotation Canary\",\"email\":\"$CANARY_EMAIL\",\"password\":\"$CANARY_PASSWORD\"}"

  # The account still follows the public registration path. The invalid test
  # mailbox is then verified locally and its queued message is skipped so no
  # real person receives drill email.
  db_psql -c "
    UPDATE users
       SET email_verified = true, email_verified_at = now(), updated_at = now()
     WHERE email = '$CANARY_EMAIL' AND role = 'user';
    UPDATE email_verification_tokens
       SET used_at = COALESCE(used_at, now())
     WHERE user_id = (SELECT id FROM users WHERE email = '$CANARY_EMAIL');
    UPDATE email_messages
       SET status = 'skipped', error = 'A-4 rotation canary', updated_at = now()
     WHERE lower(recipient_email) = lower('$CANARY_EMAIL') AND status = 'queued';
  " >/dev/null
  user_row="$(db_psql -Atc \
    "SELECT id::text || '|' || role || '|' || email_verified::text FROM users WHERE email = '$CANARY_EMAIL';")"
  if [[ "$user_row" != *"|user|true" ]]; then
    echo "JWT canary was not provisioned as a verified non-admin user" >&2
    exit 1
  fi

  api_request POST /v1/auth/login 200 \
    "{\"email\":\"$CANARY_EMAIL\",\"password\":\"$CANARY_PASSWORD\"}"
  OLD_ACCESS_TOKEN="$(json_string_field access_token)"
  CURRENT_ACCESS_TOKEN="$OLD_ACCESS_TOKEN"
  CURRENT_REFRESH_TOKEN="$(json_string_field refresh_token)"
  SESSION_ID="$(json_string_field session_id)"
  OLD_KID="$(token_kid "$OLD_ACCESS_TOKEN")"
  if [[ "$required_kid_mode" == no-kid && -n "$OLD_KID" ]]; then
    db_psql -c "DELETE FROM users WHERE email = '$CANARY_EMAIL' AND role = 'user';" >/dev/null
    echo "legacy capture rejected: living signer already emits kid" >&2
    exit 1
  fi
  OLD_KID_ACCESS_TOKEN=""
  NEW_KID=""
  DRILL_CONTAINER_ID="$(app_container_id)"
  DRILL_CONTAINER_STARTED_AT="$(app_container_started_at "$DRILL_CONTAINER_ID")"
  DRILL_CONTAINER_RESTART_COUNT="$(app_container_restart_count "$DRILL_CONTAINER_ID")"
  RUNTIME_FINALIZED=false
  LEGACY_WAS_NO_KID=false
  if [[ -z "$OLD_KID" ]]; then
    LEGACY_WAS_NO_KID=true
  else
    OLD_KID_ACCESS_TOKEN="$OLD_ACCESS_TOKEN"
  fi
  api_request GET /v1/auth/sessions 200 "" "$OLD_ACCESS_TOKEN"
  SESSION_TOTAL_BASELINE="$(json_integer_field total)"
  if [[ -z "$OLD_ACCESS_TOKEN" || -z "$CURRENT_REFRESH_TOKEN" || -z "$SESSION_ID" ||
        -z "$DRILL_CONTAINER_ID" || -z "$SESSION_TOTAL_BASELINE" ]]; then
    echo "JWT canary login response was incomplete" >&2
    exit 1
  fi
  if [[ ! "$SESSION_ID" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]]; then
    echo "JWT canary returned an invalid session identifier" >&2
    exit 1
  fi
  assert_active_session_family
  write_state
}

token_expiry_epoch() {
  local token="$1"
  local remainder segment padding payload expires_at
  remainder="${token#*.}"
  if [[ "$remainder" == "$token" || "$remainder" != *.* ]]; then
    return 1
  fi
  segment="${remainder%%.*}"
  segment="${segment//-/+}"
  segment="${segment//_/\/}"
  padding=$(( (4 - ${#segment} % 4) % 4 ))
  segment+="$(printf '%*s' "$padding" '' | tr ' ' '=')"
  payload="$(printf '%s' "$segment" | base64 --decode 2>/dev/null)" || return 1
  expires_at="$(sed -n 's/.*"exp":\([0-9][0-9]*\).*/\1/p' <<<"$payload" | head -1)"
  [[ "$expires_at" =~ ^[0-9]+$ ]] || return 1
  printf '%s\n' "$expires_at"
}

recapture_legacy_canary() {
  local access refresh session kid
  api_request POST /v1/auth/login 200 \
    "{\"email\":\"$CANARY_EMAIL\",\"password\":\"$CANARY_PASSWORD\"}"
  access="$(json_string_field access_token)"
  refresh="$(json_string_field refresh_token)"
  session="$(json_string_field session_id)"
  kid="$(token_kid "$access")"
  if [[ -z "$access" || -z "$refresh" ||
        ! "$session" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ||
        -n "$kid" ]]; then
    echo "legacy recapture rejected: living signer no longer emits a valid no-kid token" >&2
    exit 1
  fi
  OLD_ACCESS_TOKEN="$access"
  OLD_KID_ACCESS_TOKEN=""
  CURRENT_ACCESS_TOKEN="$access"
  CURRENT_REFRESH_TOKEN="$refresh"
  SESSION_ID="$session"
  OLD_KID=""
  NEW_KID=""
  LEGACY_WAS_NO_KID=true
  RUNTIME_FINALIZED=false
  DRILL_CONTAINER_ID="$(app_container_id)"
  DRILL_CONTAINER_STARTED_AT="$(app_container_started_at "$DRILL_CONTAINER_ID")"
  DRILL_CONTAINER_RESTART_COUNT="$(app_container_restart_count "$DRILL_CONTAINER_ID")"
  api_request GET /v1/auth/sessions 200 "" "$OLD_ACCESS_TOKEN"
  SESSION_TOTAL_BASELINE="$(json_integer_field total)"
  assert_active_session_family
  write_state
}

keyset_status_field() {
  local field="$1"
  run_keyset_cli status --file /keys/keyset.json \
    | sed -n "s/.*\"${field}\": \"\([^\"]*\)\".*/\1/p" | head -1
}

keyset_status_boolean() {
  local field="$1"
  run_keyset_cli status --file /keys/keyset.json \
    | sed -n "s/.*\"${field}\": \(true\|false\).*/\1/p" | head -1
}

overlap_duration() {
  local ttl number unit seconds token token_seconds maximum_seconds
  ttl="$(env_value JWT_ACCESS_TOKEN_EXPIRY)"
  ttl="${ttl:-15m}"
  maximum_seconds=0
  if [[ "$ttl" =~ ^([0-9]+)(s|m|h)$ ]]; then
    number="${BASH_REMATCH[1]}"
    unit="${BASH_REMATCH[2]}"
    case "$unit" in
      s) seconds="$number" ;;
      m) seconds=$(( number * 60 )) ;;
      h) seconds=$(( number * 3600 )) ;;
    esac
    if (( seconds > 0 && seconds <= 86400 )); then
      maximum_seconds="$seconds"
    else
      maximum_seconds=0
    fi
  fi

  # The captured token is the authoritative TTL proof for the pre-A-4 binary.
  # This covers a config change between the legacy binary and the A-4 release.
  # Failure to decode safely falls back to the application's hard 24h cap.
  for token in "$OLD_ACCESS_TOKEN" "$OLD_KID_ACCESS_TOKEN"; do
    if [[ -z "$token" ]]; then
      continue
    fi
    token_seconds="$(token_lifetime_seconds "$token" || true)"
    if [[ ! "$token_seconds" =~ ^[0-9]+$ ]] || (( token_seconds <= 0 || token_seconds > 86400 )); then
      printf '86700s\n'
      return
    fi
    if (( token_seconds > maximum_seconds )); then
      maximum_seconds="$token_seconds"
    fi
  done
  if (( maximum_seconds <= 0 )); then
    printf '86700s\n'
    return
  fi
  printf '%ss\n' "$(( maximum_seconds + 300 ))"
}

token_lifetime_seconds() {
  local token="$1"
  local remainder segment padding payload issued_at expires_at
  remainder="${token#*.}"
  if [[ "$remainder" == "$token" || "$remainder" != *.* ]]; then
    return 1
  fi
  segment="${remainder%%.*}"
  segment="${segment//-/+}"
  segment="${segment//_/\/}"
  padding=$(( (4 - ${#segment} % 4) % 4 ))
  segment+="$(printf '%*s' "$padding" '' | tr ' ' '=')"
  payload="$(printf '%s' "$segment" | base64 --decode 2>/dev/null)" || return 1
  issued_at="$(sed -n 's/.*"iat":\([0-9][0-9]*\).*/\1/p' <<<"$payload" | head -1)"
  expires_at="$(sed -n 's/.*"exp":\([0-9][0-9]*\).*/\1/p' <<<"$payload" | head -1)"
  if [[ ! "$issued_at" =~ ^[0-9]+$ || ! "$expires_at" =~ ^[0-9]+$ ]] ||
     (( expires_at <= issued_at )); then
    return 1
  fi
  printf '%s\n' "$(( expires_at - issued_at ))"
}

export_worker_keyset() {
  run_keyset_cli export-worker --file /keys/keyset.json --out /keys/worker-keyset.json >/dev/null
  sudo chmod 0600 "$WORKER_EXPORT_FILE"
}

assert_expected_version
sudo install -d -o root -g root -m 0700 "$JWT_DIRECTORY"
assert_living_app_reload_contract

case "$MODE" in
  capture-legacy)
    if ! sudo test -f "$KEYSET_FILE" || [[ "$(keyset_status_field state)" != stable ]]; then
      echo "legacy capture rejected: initial stable keyset is required" >&2
      exit 1
    fi
    if load_state; then
      if [[ "$LEGACY_WAS_NO_KID" != true || -n "$OLD_KID" || -n "$NEW_KID" ]]; then
        echo "legacy capture rejected: existing state is not the initial no-kid bridge" >&2
        exit 1
      fi
      expires_at="$(token_expiry_epoch "$OLD_ACCESS_TOKEN" || true)"
      if [[ "$expires_at" =~ ^[0-9]+$ ]] && (( expires_at > $(date +%s) )); then
        api_request GET /v1/auth/sessions 200 "" "$OLD_ACCESS_TOKEN"
        DRILL_CONTAINER_ID="$(app_container_id)"
        DRILL_CONTAINER_STARTED_AT="$(app_container_started_at "$DRILL_CONTAINER_ID")"
        DRILL_CONTAINER_RESTART_COUNT="$(app_container_restart_count "$DRILL_CONTAINER_ID")"
        write_state
        public_auth_probe "$OLD_ACCESS_TOKEN" 200 legacy-no-kid
        record_evidence legacy-capture-resumed absent true
        echo "legacy no-kid canary remains live; capture resumed idempotently"
        exit 0
      fi
      recapture_legacy_canary
      public_auth_probe "$OLD_ACCESS_TOKEN" 200 "$(original_token_class)"
      record_evidence legacy-recaptured absent true
      echo "expired legacy canary was safely recaptured from the living no-kid signer"
      exit 0
    fi
    create_canary no-kid
    public_auth_probe "$OLD_ACCESS_TOKEN" 200 "$(original_token_class)"
    record_evidence legacy-captured absent true
    echo "legacy no-kid canary captured; session family is active"
    ;;

  prepare)
    if ! sudo test -f "$KEYSET_FILE"; then
      echo "JWT prepare rejected: keyset bootstrap has not completed" >&2
      exit 1
    fi
    rotation_state="$(keyset_status_field state)"
    if [[ "$rotation_state" == retired ]] && sudo test -f "$STATE_FILE"; then
      load_state
      if [[ "$RUNTIME_FINALIZED" != true ]]; then
        echo "JWT prepare rejected: retired file has not completed runtime finalization" >&2
        exit 1
      fi
      # A completed drill may leave only expired canary credentials if cleanup
      # was interrupted. Start the next rotation with a fresh normal session.
      sudo rm -f "$STATE_FILE"
    fi
    if ! load_state; then
      create_canary
    fi
    if [[ "$rotation_state" == active || "$rotation_state" == rolled_back ]]; then
      # Resume an interrupted start without trying to prepare a third key. The
      # following activate-drill phase is itself idempotent and will either
      # continue active state or reactivate a rolled-back signer safely.
      assert_same_container
      if [[ -z "$OLD_KID" || -z "$NEW_KID" ]]; then
        echo "JWT prepare resume rejected: persisted key IDs are incomplete" >&2
        exit 1
      fi
      snapshot_other_active_sessions
      reload_app
      assert_snapshot_sessions_not_revoked
      export_worker_keyset
      record_evidence overlap-resumed "$(keyset_status_field active_kid)" "$LEGACY_WAS_NO_KID"
      echo "JWT overlap resume validated; no additional key was prepared"
    else
      if [[ "$rotation_state" != stable && "$rotation_state" != retired && "$rotation_state" != prepared ]]; then
        echo "JWT prepare rejected: unsupported keyset state" >&2
        exit 1
      fi
      if [[ "$rotation_state" == prepared ]]; then
        assert_same_container
      else
        DRILL_CONTAINER_ID="$(app_container_id)"
        DRILL_CONTAINER_STARTED_AT="$(app_container_started_at "$DRILL_CONTAINER_ID")"
        DRILL_CONTAINER_RESTART_COUNT="$(app_container_restart_count "$DRILL_CONTAINER_ID")"
      fi
      api_request GET /v1/auth/sessions 200 "" "$OLD_ACCESS_TOKEN"
      OLD_KID="$(keyset_status_field active_kid)"
      NEW_KID="${NEW_KID:-${APP_ENV}-$(date -u +%Y%m%dT%H%M%SZ)}"
      # Persist the chosen kid before the crash-safe CLI transition so a workflow
      # retry resumes the same prepared rotation instead of inventing another ID.
      write_state
      snapshot_other_active_sessions
      run_keyset_cli prepare --file /keys/keyset.json --new-kid "$NEW_KID"
      reload_app
      assert_same_container
      api_request GET /v1/auth/sessions 200 "" "$OLD_ACCESS_TOKEN"
      public_auth_probe "$OLD_ACCESS_TOKEN" 200 "$(original_token_class)"
      refresh_canary "$OLD_KID"
      OLD_KID_ACCESS_TOKEN="$CURRENT_ACCESS_TOKEN"
      public_auth_probe "$OLD_KID_ACCESS_TOKEN" 200 old-kid
      assert_snapshot_sessions_not_revoked
      export_worker_keyset
      write_state
      record_evidence overlap-prepared "$OLD_KID" "$LEGACY_WAS_NO_KID"
      echo "JWT overlap prepared; old canary token remains valid"
    fi
    ;;

  activate-drill)
    load_state || { echo "JWT activation rejected: canary state is missing" >&2; exit 1; }
    assert_same_container
    activation_state="$(keyset_status_field state)"
    if [[ "$activation_state" == active && "$(keyset_status_boolean retirement_due)" == true ]]; then
      echo "JWT activation resume rejected: retirement gate is already due; continue forward with retire" >&2
      exit 1
    fi
    overlap="$(overlap_duration)"
    snapshot_other_active_sessions
    run_keyset_cli activate --file /keys/keyset.json --overlap "$overlap"
    reload_app
    if run_keyset_cli retire --file /keys/keyset.json >"$TMP_DIR/premature-retire.out" 2>&1; then
      echo "JWT retirement gate failed open" >&2
      exit 1
    fi
    api_request GET /v1/auth/sessions 200 "" "$OLD_ACCESS_TOKEN"
    api_request GET /v1/auth/sessions 200 "" "$OLD_KID_ACCESS_TOKEN"
    public_auth_probe "$OLD_ACCESS_TOKEN" 200 "$(original_token_class)"
    public_auth_probe "$OLD_KID_ACCESS_TOKEN" 200 old-kid
    worker_identity_smoke "$OLD_ACCESS_TOKEN" user
    worker_identity_smoke "$OLD_KID_ACCESS_TOKEN" user
    refresh_canary "$NEW_KID"
    first_new_token="$CURRENT_ACCESS_TOKEN"
    public_auth_probe "$first_new_token" 200 new-kid
    worker_identity_smoke "$first_new_token" user

    run_keyset_cli rollback --file /keys/keyset.json
    reload_app
    api_request GET /v1/auth/sessions 200 "" "$first_new_token"
    refresh_canary "$OLD_KID"
    rollback_old_token="$CURRENT_ACCESS_TOKEN"
    public_auth_probe "$rollback_old_token" 200 old-kid-rollback
    OLD_KID_ACCESS_TOKEN="$rollback_old_token"
    write_state
    worker_identity_smoke "$rollback_old_token" user

    run_keyset_cli activate --file /keys/keyset.json --overlap "$overlap"
    reload_app
    api_request GET /v1/auth/sessions 200 "" "$rollback_old_token"
    refresh_canary "$NEW_KID"
    public_auth_probe "$CURRENT_ACCESS_TOKEN" 200 new-kid-reactivated
    worker_identity_smoke "$CURRENT_ACCESS_TOKEN" user
    assert_same_container
    assert_active_session_family
    assert_session_total
    assert_snapshot_sessions_not_revoked
    export_worker_keyset
    write_state
    record_evidence overlap-active "$NEW_KID" "$LEGACY_WAS_NO_KID"
    echo "JWT signer activated after a safe rollback drill; old and new tokens remain valid"
    ;;

  rollback)
    load_state || { echo "JWT rollback rejected: canary state is missing" >&2; exit 1; }
    assert_same_container
    snapshot_other_active_sessions
    run_keyset_cli rollback --file /keys/keyset.json
    reload_app
    refresh_canary "$OLD_KID"
    public_auth_probe "$CURRENT_ACCESS_TOKEN" 200 old-kid-rollback
    assert_active_session_family
    assert_session_total
    assert_snapshot_sessions_not_revoked
    export_worker_keyset
    write_state
    record_evidence emergency-rollback "$OLD_KID" "$LEGACY_WAS_NO_KID"
    echo "JWT signer rolled back safely; both verifier keys remain"
    ;;

  retire-prepare)
    load_state || { echo "JWT retirement rejected: canary state is missing" >&2; exit 1; }
    assert_same_container
    if [[ "$(keyset_status_field state)" == retired && "$RUNTIME_FINALIZED" == true ]]; then
      # A prior successful finalize may have been interrupted while deleting
      # the canary. Recreate only the transient Worker export; never consume the
      # now-unusable refresh credential again.
      export_worker_keyset
      echo "JWT retirement already finalized; cleanup recovery is ready"
      exit 0
    fi
    # Access tokens from activation have expired by design at this gate. Rotate
    # the opaque refresh token first to prove the session is still continuous
    # and to obtain a fresh new-kid access token for the remaining assertions.
    refresh_canary "$NEW_KID"
    snapshot_other_active_sessions
    run_keyset_cli retire --file /keys/keyset.json
    assert_same_container
    assert_active_session_family
    assert_session_total
    assert_snapshot_sessions_not_revoked
    export_worker_keyset
    write_state
    record_evidence retirement-prepared "$NEW_KID" "$LEGACY_WAS_NO_KID"
    echo "JWT retirement file prepared; runtime remains dual-key until the Worker is updated"
    ;;

  retire-finalize)
    load_state || { echo "JWT retirement rejected: canary state is missing" >&2; exit 1; }
    assert_same_container
    if [[ "$(keyset_status_field state)" == retired && "$RUNTIME_FINALIZED" == true ]]; then
      run_keyset_cli retire --file /keys/keyset.json >/dev/null
      echo "JWT retirement runtime already finalized; continuing cleanup recovery"
      exit 0
    fi
    snapshot_other_active_sessions
    # Idempotently prove the on-disk gate is retired before changing runtime.
    run_keyset_cli retire --file /keys/keyset.json
    worker_identity_smoke "$OLD_ACCESS_TOKEN" guest
    worker_identity_smoke "$OLD_KID_ACCESS_TOKEN" guest
    worker_identity_smoke "$CURRENT_ACCESS_TOKEN" user
    reload_app
    api_request GET /v1/auth/sessions 401 "" "$OLD_ACCESS_TOKEN"
    api_request GET /v1/auth/sessions 401 "" "$OLD_KID_ACCESS_TOKEN"
    public_auth_probe "$OLD_ACCESS_TOKEN" 401 "$(original_token_class)-retired"
    public_auth_probe "$OLD_KID_ACCESS_TOKEN" 401 old-kid-retired
    refresh_canary "$NEW_KID"
    public_auth_probe "$CURRENT_ACCESS_TOKEN" 200 new-kid-retired
    worker_identity_smoke "$CURRENT_ACCESS_TOKEN" user
    assert_same_container
    assert_active_session_family
    assert_session_total
    assert_snapshot_sessions_not_revoked
    set_env_value JWT_SECRET ""
    RUNTIME_FINALIZED=true
    write_state
    record_evidence retired "$NEW_KID" false
    echo "JWT old key retired; old token rejected and the same canary session refreshed successfully"
    ;;

  status)
    if sudo test -f "$KEYSET_FILE"; then
      run_keyset_cli status --file /keys/keyset.json
    else
      echo "keyset=not-installed legacy_capture=true"
    fi
    if load_state; then
      assert_same_container
      if [[ "$RUNTIME_FINALIZED" == true ]]; then
        canary_user_count="$(db_psql -Atc \
          "SELECT count(*) FROM users WHERE email = '$CANARY_EMAIL' AND role = 'user';")"
        if [[ "$canary_user_count" == 1 ]]; then
          assert_active_session_family
          echo "canary_session=active runtime_finalized=true container_unchanged=true unexpected_canary_401=0"
        elif [[ "$canary_user_count" == 0 ]]; then
          echo "canary_session=cleanup-interrupted runtime_finalized=true container_unchanged=true unexpected_canary_401=0"
        else
          echo "JWT status rejected: canary cleanup state is ambiguous" >&2
          exit 1
        fi
      else
        assert_active_session_family
        echo "canary_session=active container_unchanged=true unexpected_canary_401=0"
      fi
    fi
    if sudo test -f "$EVIDENCE_FILE"; then
      sudo tail -20 "$EVIDENCE_FILE"
    fi
    ;;

  cleanup)
    if [[ "$(keyset_status_field state)" != retired ]]; then
      echo "JWT cleanup rejected: keyset has not completed retirement" >&2
      exit 1
    fi
    if load_state; then
      if [[ "$RUNTIME_FINALIZED" != true ]]; then
        echo "JWT cleanup rejected: runtime retirement is not finalized" >&2
        exit 1
      fi
      if [[ ! "$CANARY_EMAIL" =~ ^jwt-rotation-canary-(dev|prod)-[0-9]{14}@example\.invalid$ ]]; then
        echo "JWT cleanup rejected: canary identity is invalid" >&2
        exit 1
      fi
      db_psql -c "DELETE FROM users WHERE email = '$CANARY_EMAIL' AND role = 'user';" >/dev/null
    fi
    sudo rm -f "$WORKER_EXPORT_FILE"
    if sudo test -f "$STATE_FILE"; then
      # State is the recovery anchor. Remove it last so a killed cleanup can be
      # retried after the idempotent canary deletion and export removal.
      sudo rm -f "$STATE_FILE"
    fi
    echo "JWT drill canary credentials and transient Worker export removed"
    ;;
esac
