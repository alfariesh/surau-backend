#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

MOCK_ROOT="$TMP_DIR/mock"
MOCK_BIN="$TMP_DIR/bin"
JWT_DIRECTORY="$TMP_DIR/jwt"
ENV_FILE="$TMP_DIR/.env.production"
mkdir -p "$MOCK_ROOT" "$MOCK_BIN" "$JWT_DIRECTORY"

fail() {
  echo "JWT rotation test failed: $*" >&2
  exit 1
}

cat > "$MOCK_BIN/sudo" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "-E" ]]; then
  shift
fi
case "${1:-}" in
  chown)
    exit 0
    ;;
  install)
    shift
    args=()
    while (( $# > 0 )); do
      case "$1" in
        -o|-g) shift 2 ;;
        *) args+=("$1"); shift ;;
      esac
    done
    exec install "${args[@]}"
    ;;
  *)
    exec "$@"
    ;;
esac
MOCK

cat > "$MOCK_BIN/date" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
counter_file="$MOCK_ROOT/date-counter"
counter="$(cat "$counter_file" 2>/dev/null || echo 0)"
counter=$((counter + 1))
printf '%s\n' "$counter" > "$counter_file"
case "${*: -1}" in
  +%Y%m%d%H%M%S) printf '20260715%06d\n' "$counter" ;;
  +%Y%m%dT%H%M%SZ) printf '20260715T%06dZ\n' "$counter" ;;
  +%Y-%m-%dT%H:%M:%S.%NZ) printf '2026-07-15T10:00:%02d.000000000Z\n' "$((counter % 60))" ;;
  +%FT%TZ) printf '2026-07-15T10:00:%02dZ\n' "$((counter % 60))" ;;
  +%s) printf '%s\n' "${MOCK_NOW_EPOCH:-1000}" ;;
  *) exec /bin/date "$@" ;;
esac
MOCK

cat > "$MOCK_BIN/sleep" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
exit 0
MOCK

cat > "$MOCK_BIN/openssl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ "${1:-}" == "rand" && "${2:-}" == "-hex" && "${3:-}" == "32" ]]
printf '%064d\n' 0
MOCK

cat > "$MOCK_BIN/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail

event() {
  printf '%s\n' "$1" >> "$MOCK_ROOT/events"
}

write_cli_state() {
  cat > "$CLI_STATE" <<EOF
STATE=$STATE
ACTIVE_KID=$ACTIVE_KID
LEGACY_KID=$LEGACY_KID
PREVIOUS_KID=$PREVIOUS_KID
NEXT_KID=$NEXT_KID
KEY_COUNT=$KEY_COUNT
EOF
  cat > "$KEYSET_FILE" <<EOF
{"version":1,"active_kid":"$ACTIVE_KID","legacy_kid":"$LEGACY_KID","keys":{"$ACTIVE_KID":"mock-secret"}}
EOF
  chmod 0600 "$CLI_STATE" "$KEYSET_FILE"
}

status_json() {
  local second=""
  if [[ "$KEY_COUNT" == "2" ]]; then
    if [[ "$ACTIVE_KID" == "$PREVIOUS_KID" ]]; then
      second="$NEXT_KID"
    else
      second="$PREVIOUS_KID"
    fi
  fi
  cat <<EOF
{
  "version": 1,
  "state": "$STATE",
  "active_kid": "$ACTIVE_KID",
  "legacy_kid": "$LEGACY_KID",
  "key_ids": [
    "$ACTIVE_KID"$(if [[ -n "$second" ]]; then printf ',\n    "%s"' "$second"; fi)
  ],
  "previous_active_kid": "$PREVIOUS_KID",
  "next_kid": "$NEXT_KID",
  "retirement_due": false
}
EOF
}

if [[ "${1:-}" == "compose" ]]; then
  shift
  while (( $# > 0 )); do
    case "$1" in
      --env-file|-f) shift 2 ;;
      *) break ;;
    esac
  done
  command="${1:-}"
  shift || true
  case "$command" in
    ps)
      printf '%s\n' '0123456789abcdef0123456789abcdef'
      ;;
    logs)
      cat "$MOCK_ROOT/reload-log" 2>/dev/null || true
      ;;
    exec)
      query="$*"
      case "$query" in
        *"SELECT statement_timestamp()"*) printf '%s\n' '2026-07-15 10:00:00.000000|3' ;;
        *"revoked_at >="*) printf '%s\n' '0' ;;
        *"SELECT count(*) FROM auth_sessions WHERE family_id"*)
          if [[ -f "$MOCK_ROOT/canary-deleted" ]]; then printf '%s\n' '0'; else printf '%s\n' '1'; fi
          ;;
        *"SELECT count(*) FROM users WHERE email"*)
          if [[ -f "$MOCK_ROOT/canary-deleted" ]]; then printf '%s\n' '0'; else printf '%s\n' '1'; fi
          ;;
        *"DELETE FROM users WHERE email"*) touch "$MOCK_ROOT/canary-deleted" ;;
        *"SELECT id::text"*) printf '%s\n' '00000000-0000-4000-8000-000000000001|user|true' ;;
        *) : ;;
      esac
      ;;
    *)
      echo "unexpected mocked docker compose command: $command" >&2
      exit 1
      ;;
  esac
  exit 0
fi

if [[ "${1:-}" == "kill" ]]; then
  shift
  [[ "${1:-}" == "--signal" && "${2:-}" == "HUP" ]]
  if [[ "${MOCK_RELOAD_FAIL:-0}" == "1" ]]; then
    event 'reload:failed-closed'
    rm -f "$MOCK_ROOT/reload-log"
    exit 0
  fi
  # Retired verifier material may not be loaded before the edge Worker has
  # received the same single-key set.
  # shellcheck disable=SC1090
  source "$MOCK_ROOT/cli-state"
  if [[ "$STATE" == "retired" && ! -f "$MOCK_ROOT/worker-updated" ]]; then
    event 'reload:blocked-before-worker'
    exit 1
  fi
  cp "$MOCK_ROOT/cli-state" "$MOCK_ROOT/runtime-state"
  expected="JWT keyset reloaded: active_kid=$ACTIVE_KID legacy_kid=$LEGACY_KID key_count=$KEY_COUNT"
  printf '%s\n' "$expected" > "$MOCK_ROOT/reload-log"
  event "reload:$STATE"
  exit 0
fi

if [[ "${1:-}" == "inspect" ]]; then
  case "${3:-}" in
    '{{.State.StartedAt}}') printf '%s\n' '2026-07-15T09:59:00.000000000Z' ;;
    '{{.RestartCount}}') printf '%s\n' '0' ;;
    '{{range .Config.Env}}{{println .}}{{end}}')
      printf '%s\n' \
        'HTTP_USE_PREFORK_MODE=false' \
        'JWT_KEYSET_FILE=/run/secrets/surau-jwt/keyset.json'
      ;;
    *) echo "unexpected mocked docker inspect format: ${3:-}" >&2; exit 1 ;;
  esac
  exit 0
fi

[[ "${1:-}" == "run" ]] || { echo "unexpected mocked docker command: $*" >&2; exit 1; }
shift
host_directory=""
while (( $# > 0 )); do
  case "$1" in
    --rm|--read-only) shift ;;
    --network|--cap-drop|--security-opt|--user|--entrypoint) shift 2 ;;
    --volume) host_directory="${2%%:/keys}"; shift 2 ;;
    *) image="$1"; shift; break ;;
  esac
done
[[ -n "${image:-}" && -n "$host_directory" ]]

CLI_STATE="$MOCK_ROOT/cli-state"
KEYSET_FILE="$host_directory/keyset.json"
# shellcheck disable=SC1090
source "$CLI_STATE"
command="${1:-}"
shift || true
file=""
new_kid=""
out=""
while (( $# > 0 )); do
  case "$1" in
    --file) file="$2"; shift 2 ;;
    --new-kid) new_kid="$2"; shift 2 ;;
    --overlap) shift 2 ;;
    --out) out="$2"; shift 2 ;;
    *) echo "unexpected mocked jwt-keyset argument: $1" >&2; exit 1 ;;
  esac
done
[[ "$file" == "/keys/keyset.json" ]]

case "$command" in
  status)
    status_json
    ;;
  prepare)
    event "cli:prepare:$new_kid"
    if [[ "$STATE" == "prepared" ]]; then
      [[ "$new_kid" == "$NEXT_KID" ]] || exit 1
    elif [[ "$STATE" == "stable" || "$STATE" == "retired" ]]; then
      PREVIOUS_KID="$ACTIVE_KID"
      NEXT_KID="$new_kid"
      STATE=prepared
      KEY_COUNT=2
      # Legacy no-kid compatibility is retained only for the migration from
      # the pre-A-4 binary; later rotations keep it permanently disabled.
      if [[ -n "$LEGACY_KID" ]]; then
        LEGACY_KID="$PREVIOUS_KID"
      fi
      write_cli_state
    else
      exit 1
    fi
    ;;
  activate)
    event 'cli:activate'
    [[ "$STATE" == "prepared" || "$STATE" == "rolled_back" || "$STATE" == "active" ]]
    STATE=active
    ACTIVE_KID="$NEXT_KID"
    KEY_COUNT=2
    write_cli_state
    ;;
  rollback)
    event 'cli:rollback'
    [[ "$STATE" == "active" || "$STATE" == "rolled_back" ]]
    STATE=rolled_back
    ACTIVE_KID="$PREVIOUS_KID"
    KEY_COUNT=2
    write_cli_state
    ;;
  retire)
    event 'cli:retire'
    if [[ "$STATE" == "retired" ]]; then
      exit 0
    fi
    [[ "$STATE" == "active" && "${MOCK_RETIRE_ALLOWED:-0}" == "1" ]] || exit 1
    STATE=retired
    ACTIVE_KID="$NEXT_KID"
    LEGACY_KID=""
    KEY_COUNT=1
    write_cli_state
    ;;
  export-worker)
    [[ "$out" == "/keys/worker-keyset.json" ]]
    status_json > "$host_directory/worker-keyset.json"
    chmod 0600 "$host_directory/worker-keyset.json"
    event "worker:export:$STATE"
    ;;
  *)
    echo "unexpected mocked jwt-keyset command: $command" >&2
    exit 1
    ;;
esac
MOCK

cat > "$MOCK_BIN/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail

base64url() {
  printf '%s' "$1" | base64 | tr -d '\n=' | tr '+/' '-_'
}

token_kid() {
  local token="$1" segment padding header
  segment="${token%%.*}"
  segment="${segment//-/+}"
  segment="${segment//_/\/}"
  padding=$(( (4 - ${#segment} % 4) % 4 ))
  segment+="$(printf '%*s' "$padding" '' | tr ' ' '=')"
  header="$(printf '%s' "$segment" | base64 --decode 2>/dev/null || true)"
  sed -n 's/.*"kid":"\([A-Za-z0-9_-]*\)".*/\1/p' <<<"$header" | head -1
}

issue_access() {
  local kid="$1" header payload
  if [[ -n "$kid" ]]; then
    header="{\"alg\":\"HS256\",\"typ\":\"JWT\",\"kid\":\"$kid\"}"
  else
    header='{"alg":"HS256","typ":"JWT"}'
  fi
  payload='{"iat":1000,"exp":1900}'
  printf '%s.%s.sig' "$(base64url "$header")" "$(base64url "$payload")"
}

event() {
  printf '%s\n' "$1" >> "$MOCK_ROOT/events"
}

method=GET
output=""
response_header=""
write_out=""
body_file=""
header_file=""
fail_http=false
url=""
while (( $# > 0 )); do
  case "$1" in
    --request) method="$2"; shift 2 ;;
    --output) output="$2"; shift 2 ;;
    --dump-header) response_header="$2"; shift 2 ;;
    --write-out) write_out="$2"; shift 2 ;;
    --data-binary) body_file="${2#@}"; shift 2 ;;
    --header)
      if [[ "$2" == @* ]]; then header_file="${2#@}"; fi
      shift 2
      ;;
    --fail) fail_http=true; shift ;;
    --silent|--show-error) shift ;;
    --connect-timeout|--max-time) shift 2 ;;
    http://*|https://*) url="$1"; shift ;;
    *) echo "unexpected mocked curl argument: $1" >&2; exit 1 ;;
  esac
done

path="/${url#*://*/}"
status=200
response='{}'
token=""
if [[ -n "$header_file" && -f "$header_file" ]]; then
  token="$(sed -n 's/^Authorization: Bearer //p' "$header_file" | head -1)"
fi

case "$method $path" in
  'GET /version') response='{"version":"test-v1"}' ;;
  'GET /healthz'|'GET /readyz') response='{}' ;;
  'POST /v1/auth/register')
    rm -f "$MOCK_ROOT/canary-deleted"
    status=201
    ;;
  'POST /v1/auth/login')
    # shellcheck disable=SC1090
    source "$MOCK_ROOT/runtime-state"
    kid="$ACTIVE_KID"
    if [[ "$STATE" == "pre-a4" ]]; then kid=""; fi
    access="$(issue_access "$kid")"
    counter="$(cat "$MOCK_ROOT/refresh-counter" 2>/dev/null || echo 0)"
    counter=$((counter + 1))
    printf '%s\n' "$counter" > "$MOCK_ROOT/refresh-counter"
    refresh="refresh-$counter"
    printf '%s\n' "$refresh" > "$MOCK_ROOT/current-refresh"
    response="{\"access_token\":\"$access\",\"refresh_token\":\"$refresh\",\"session_id\":\"00000000-0000-4000-8000-000000000001\"}"
    event "login:${kid:-no-kid}"
    ;;
  'POST /v1/auth/refresh')
    supplied="$(sed -n 's/.*"refresh_token":"\([^"]*\)".*/\1/p' "$body_file" | head -1)"
    current="$(cat "$MOCK_ROOT/current-refresh")"
    if [[ "$supplied" != "$current" ]]; then
      status=401
      response='{"error":"consumed refresh"}'
    else
      # shellcheck disable=SC1090
      source "$MOCK_ROOT/runtime-state"
      access="$(issue_access "$ACTIVE_KID")"
      counter="$(cat "$MOCK_ROOT/refresh-counter")"
      counter=$((counter + 1))
      printf '%s\n' "$counter" > "$MOCK_ROOT/refresh-counter"
      refresh="refresh-$counter"
      printf '%s\n' "$refresh" > "$MOCK_ROOT/current-refresh"
      response="{\"access_token\":\"$access\",\"refresh_token\":\"$refresh\",\"session_id\":\"00000000-0000-4000-8000-000000000001\"}"
      event "curl:refresh:$ACTIVE_KID"
    fi
    ;;
  'GET /v1/auth/sessions')
    # shellcheck disable=SC1090
    source "$MOCK_ROOT/runtime-state"
    kid="$(token_kid "$token")"
    accepted=false
    if [[ "$STATE" == "pre-a4" && -z "$kid" ]]; then
      accepted=true
    elif [[ -z "$kid" && -n "$LEGACY_KID" ]]; then
      accepted=true
    elif [[ -n "$kid" && "$KEY_COUNT" == "1" && "$kid" == "$ACTIVE_KID" ]]; then
      accepted=true
    elif [[ -n "$kid" && "$KEY_COUNT" == "2" && ( "$kid" == "$PREVIOUS_KID" || "$kid" == "$NEXT_KID" ) ]]; then
      accepted=true
    fi
    if [[ "$accepted" == true ]]; then
      response='{"total":1}'
    else
      status=401
      response='{"error":"invalid token"}'
      event "reject:${kid:+kid}$(if [[ -z "$kid" ]]; then printf 'no-kid'; fi)"
    fi
    ;;
  *)
    echo "unexpected mocked API request: $method $path" >&2
    exit 1
    ;;
esac

if [[ -n "$output" ]]; then
  printf '%s' "$response" > "$output"
else
  printf '%s' "$response"
fi
if [[ -n "$response_header" ]]; then
  printf 'HTTP/1.1 %s Mock\r\nX-Request-ID: mock-request\r\n\r\n' "$status" > "$response_header"
fi
if [[ -n "$write_out" ]]; then
  printf '%s' "$status"
fi
if [[ "$fail_http" == true && "$status" -ge 400 ]]; then
  exit 22
fi
MOCK

chmod 0755 "$MOCK_BIN/sudo" "$MOCK_BIN/date" "$MOCK_BIN/sleep" \
  "$MOCK_BIN/openssl" "$MOCK_BIN/docker" "$MOCK_BIN/curl"

cat > "$ENV_FILE" <<EOF
APP_IMAGE=surau-backend:test
JWT_SECRET=mock-legacy-secret-0123456789abcdef
JWT_SECRETS_DIR=$JWT_DIRECTORY
JWT_ACCESS_TOKEN_EXPIRY=15m
HTTP_USE_PREFORK_MODE=false
EOF
chmod 0600 "$ENV_FILE"

cat > "$MOCK_ROOT/runtime-state" <<'EOF'
STATE=pre-a4
ACTIVE_KID=dev-legacy
LEGACY_KID=dev-legacy
PREVIOUS_KID=dev-legacy
NEXT_KID=
KEY_COUNT=1
EOF
: > "$MOCK_ROOT/events"

run_rotation() {
  PATH="$MOCK_BIN:$PATH" \
    MOCK_ROOT="$MOCK_ROOT" \
    ENV_FILE="$ENV_FILE" \
    COMPOSE_FILE="$TMP_DIR/compose.yml" \
    APP_ENV=dev \
    EXPECTED_VERSION=test-v1 \
    API_URL=http://mock-api \
    "$SCRIPT_DIR/rotate-jwt-keyset.sh" "$@"
}

state_value() {
  local key="$1"
  sed -n "s/^${key}=//p" "$JWT_DIRECTORY/drill-dev.env" | tail -1
}

mock_state_value() {
  local file="$1" key="$2"
  sed -n "s/^${key}=//p" "$file" | tail -1
}

write_keyset_state() {
  local state="$1" active="$2" legacy="$3" previous="$4" next="$5" count="$6"
  cat > "$MOCK_ROOT/cli-state" <<EOF
STATE=$state
ACTIVE_KID=$active
LEGACY_KID=$legacy
PREVIOUS_KID=$previous
NEXT_KID=$next
KEY_COUNT=$count
EOF
  printf '%s\n' '{}' > "$JWT_DIRECTORY/keyset.json"
  chmod 0600 "$MOCK_ROOT/cli-state" "$JWT_DIRECTORY/keyset.json"
}

# The pre-A-4 binary starts without a keyset. Status remains safe. Deploy then
# bootstraps the stable legacy verifier before capturing from the still-living
# pre-A-4 signer, exactly as the one-time bridge does in production.
status_output="$(run_rotation status)"
[[ "$status_output" == *'keyset=not-installed legacy_capture=true'* ]] || fail 'pre-A-4 status was not reported'
write_keyset_state stable dev-legacy dev-legacy dev-legacy '' 1
capture_output="$(run_rotation capture-legacy)"
[[ "$capture_output" == *'legacy no-kid canary captured'* ]] || fail 'legacy canary was not captured'
[[ "$(state_value LEGACY_WAS_NO_KID)" == true ]] || fail 'legacy token was not recorded as no-kid'
[[ -z "$(state_value OLD_KID)" ]] || fail 'pre-A-4 token unexpectedly had kid'
resume_output="$(run_rotation capture-legacy)"
[[ "$resume_output" == *'capture resumed idempotently'* ]] || fail 'live legacy capture was not idempotent'

# Once the stored token has expired, recapture is allowed only if the living
# signer still emits no-kid. An A-4 signer must fail closed without replacing
# the validated legacy state.
cp "$MOCK_ROOT/runtime-state" "$MOCK_ROOT/pre-a4-runtime-state"
write_keyset_state stable dev-legacy dev-legacy dev-legacy '' 1
cp "$MOCK_ROOT/cli-state" "$MOCK_ROOT/runtime-state"
if MOCK_NOW_EPOCH=2000 run_rotation capture-legacy >/dev/null 2>&1; then
  fail 'expired legacy capture accepted an A-4 kid signer'
fi
cp "$MOCK_ROOT/pre-a4-runtime-state" "$MOCK_ROOT/runtime-state"
recapture_output="$(MOCK_NOW_EPOCH=2000 run_rotation capture-legacy)"
[[ "$recapture_output" == *'safely recaptured'* ]] || fail 'expired no-kid canary was not safely recaptured'
run_rotation status >/dev/null

# A prepare retry must reuse the persisted NEW_KID instead of generating a
# third key or changing the rotation identity.
run_rotation prepare >/dev/null
first_new_kid="$(state_value NEW_KID)"
[[ "$first_new_kid" =~ ^dev-[A-Za-z0-9TZ]+$ ]] || fail 'prepared kid is malformed'
run_rotation prepare >/dev/null
[[ "$(state_value NEW_KID)" == "$first_new_kid" ]] || fail 'prepare retry changed NEW_KID'
mapfile -t prepare_events < <(grep '^cli:prepare:' "$MOCK_ROOT/events")
[[ "${#prepare_events[@]}" == 2 && "${prepare_events[0]}" == "${prepare_events[1]}" ]] \
  || fail 'prepare retry did not call the CLI with the same kid'

# A reload error leaves the runtime on the old signer even though the
# crash-safe on-disk transition reached active. The active resume path reloads
# that exact state and does not prepare another key.
if MOCK_RELOAD_FAIL=1 run_rotation activate-drill >/dev/null 2>&1; then
  fail 'reload failure was accepted'
fi
[[ "$(mock_state_value "$MOCK_ROOT/runtime-state" STATE)" == prepared &&
   "$(mock_state_value "$MOCK_ROOT/runtime-state" ACTIVE_KID)" == dev-legacy ]] \
  || fail 'failed reload changed runtime state'
active_resume="$(run_rotation prepare)"
[[ "$active_resume" == *'overlap resume validated'* ]] || fail 'active transition did not resume safely'
[[ "$(grep -c '^cli:prepare:' "$MOCK_ROOT/events")" == 2 ]] || fail 'active resume prepared another key'

# Model a process interruption after rollback persisted but before reload.
# Prepare must resume rolled_back without creating a key.
write_keyset_state rolled_back dev-legacy dev-legacy dev-legacy "$first_new_kid" 2
rolled_back_resume="$(run_rotation prepare)"
[[ "$rolled_back_resume" == *'overlap resume validated'* ]] || fail 'rolled_back transition did not resume safely'
[[ "$(grep -c '^cli:prepare:' "$MOCK_ROOT/events")" == 2 ]] || fail 'rolled_back resume prepared another key'

run_rotation activate-drill >/dev/null

# Retirement rotates the single-use refresh credential before touching the
# verifier file. The app runtime remains dual-key until the Worker phase is
# explicitly acknowledged.
: > "$MOCK_ROOT/events"
MOCK_RETIRE_ALLOWED=1 run_rotation retire-prepare >/dev/null
refresh_line="$(grep -n '^curl:refresh:' "$MOCK_ROOT/events" | head -1 | cut -d: -f1)"
retire_line="$(grep -n '^cli:retire$' "$MOCK_ROOT/events" | head -1 | cut -d: -f1)"
[[ -n "$refresh_line" && -n "$retire_line" && "$refresh_line" -lt "$retire_line" ]] \
  || fail 'retirement did not refresh before staging CLI retire'
[[ "$(mock_state_value "$MOCK_ROOT/runtime-state" STATE)" == active &&
   "$(mock_state_value "$MOCK_ROOT/runtime-state" KEY_COUNT)" == 2 ]] \
  || fail 'retire-prepare changed the runtime verifier'
[[ "$(mock_state_value "$MOCK_ROOT/cli-state" STATE)" == retired &&
   "$(mock_state_value "$MOCK_ROOT/cli-state" KEY_COUNT)" == 1 ]] \
  || fail 'retire-prepare did not stage the single-key file'
[[ -f "$JWT_DIRECTORY/worker-keyset.json" ]] || fail 'retired Worker export is missing'

if MOCK_RETIRE_ALLOWED=1 run_rotation retire-finalize >/dev/null 2>&1; then
  fail 'runtime retirement happened before Worker acknowledgement'
fi
grep -Fxq 'reload:blocked-before-worker' "$MOCK_ROOT/events" \
  || fail 'missing Worker-before-finalize guard evidence'
[[ "$(mock_state_value "$MOCK_ROOT/runtime-state" STATE)" == active &&
   "$(mock_state_value "$MOCK_ROOT/runtime-state" KEY_COUNT)" == 2 ]] \
  || fail 'blocked finalize changed runtime state'

touch "$MOCK_ROOT/worker-updated"
MOCK_RETIRE_ALLOWED=1 run_rotation retire-finalize >/dev/null
grep -Fxq 'reject:no-kid' "$MOCK_ROOT/events" || fail 'legacy no-kid token was not rejected separately'
grep -Fqx 'JWT_SECRET=' "$ENV_FILE" || fail 'legacy dotenv secret was not cleared after finalize'

# Simulate a workflow killed after the canary row was deleted but before the
# root-owned state file was removed. Retire must resume without consuming the
# deleted canary's refresh token, status must stay readable, and cleanup must be
# idempotent.
touch "$MOCK_ROOT/canary-deleted"
MOCK_RETIRE_ALLOWED=1 run_rotation retire-prepare >/dev/null
MOCK_RETIRE_ALLOWED=1 run_rotation retire-finalize >/dev/null
recovery_status="$(run_rotation status)"
[[ "$recovery_status" == *'canary_session=cleanup-interrupted'* ]] \
  || fail 'interrupted cleanup was not reported safely'
run_rotation cleanup >/dev/null
[[ ! -e "$JWT_DIRECTORY/drill-dev.env" && ! -e "$JWT_DIRECTORY/worker-keyset.json" ]] \
  || fail 'cleanup left transient canary or Worker material'

# A future scheduled rotation starts from the retired keyset with a fresh
# canary. That canary is now a normal kid-bearing token; no-kid compatibility
# stays disabled and its old kid is rejected independently at retirement.
rm -f "$MOCK_ROOT/worker-updated"
run_rotation prepare >/dev/null
[[ "$(state_value LEGACY_WAS_NO_KID)" == false ]] || fail 'future canary unexpectedly used no-kid compatibility'
second_old_kid="$(state_value OLD_KID)"
[[ -n "$second_old_kid" && "$second_old_kid" == "$first_new_kid" ]] \
  || fail 'future rotation did not start from the retired active kid'
run_rotation activate-drill >/dev/null
MOCK_RETIRE_ALLOWED=1 run_rotation retire-prepare >/dev/null
touch "$MOCK_ROOT/worker-updated"
MOCK_RETIRE_ALLOWED=1 run_rotation retire-finalize >/dev/null
run_rotation cleanup >/dev/null

grep -Fxq 'reject:kid' "$MOCK_ROOT/events" || fail 'old kid-bearing token was not rejected separately'
grep -Fq 'event=retired' "$JWT_DIRECTORY/drill-dev-evidence.log" || fail 'sanitized retirement evidence is missing'

echo 'JWT rotation state-machine tests passed'
