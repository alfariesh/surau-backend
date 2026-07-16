#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-}"
EXPECTED_VERSION="${2:-}"
ENV_FILE="${ENV_FILE:-.env.production}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"
COMPOSE_OVERRIDE_FILE="${COMPOSE_OVERRIDE_FILE:-}"
STATE_DIR="${SURAU_DEPLOY_STATE_DIR:-/var/lib/surau/deploy}"
CADDY_CONFIG="${SURAU_CADDY_CONFIG:-/etc/caddy/Caddyfile}"
ACTIVE_PORT_FILE="$STATE_DIR/active-api-port"
ACTIVE_CONTAINER_FILE="$STATE_DIR/active-api-container"
ACTIVE_LOOPS_FILE="$STATE_DIR/active-background-loops"
PREVIOUS_PORT_FILE="$STATE_DIR/previous-api-port"
PREVIOUS_CONTAINER_FILE="$STATE_DIR/previous-api-container"
PREVIOUS_VERSION_FILE="$STATE_DIR/previous-api-version"
PREVIOUS_LOOPS_FILE="$STATE_DIR/previous-background-loops"
AVAILABILITY_URL="${AVAILABILITY_URL:-}"
AVAILABILITY_INTERVAL_SECONDS="${AVAILABILITY_INTERVAL_SECONDS:-0.25}"
AVAILABILITY_EVIDENCE_FILE="$STATE_DIR/last-api-availability.tsv"
AVAILABILITY_PID=""
AVAILABILITY_LOG=""
AVAILABILITY_STOP_FILE=""

die() {
  echo "blue-green deploy rejected: $*" >&2
  exit 1
}

valid_port() {
  [[ "$1" =~ ^(8080|18080|18081)$ ]]
}

valid_slot_port() {
  [[ "$1" =~ ^(18080|18081)$ ]]
}

valid_container() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ || "$1" =~ ^[a-f0-9]{12,64}$ ]]
}

valid_version() {
  [[ "$1" =~ ^[A-Za-z0-9._-]{1,64}$ ]]
}

valid_availability_url() {
  [[ -z "$1" || "$1" =~ ^https://[A-Za-z0-9.-]+/healthz$ ]]
}

availability_log_is_clean() {
  local path="$1"
  local samples failures
  samples="$(awk 'NF == 2 { count++ } END { print count + 0 }' "$path")"
  failures="$(awk 'NF != 2 || $2 != "200" { count++ } END { print count + 0 }' "$path")"
  [[ "$samples" -ge 3 && "$failures" == 0 ]]
}

start_availability_monitor() {
  valid_availability_url "$AVAILABILITY_URL" || die "availability URL is invalid"
  [[ "$AVAILABILITY_INTERVAL_SECONDS" =~ ^0[.][0-9]+$ ]] \
    || die "availability interval must be a fractional second"
  if [[ -z "$AVAILABILITY_URL" ]]; then
    return 0
  fi

  AVAILABILITY_LOG="$(mktemp)"
  AVAILABILITY_STOP_FILE="$(mktemp)"
  rm -f "$AVAILABILITY_STOP_FILE"
  (
    while [[ ! -e "$AVAILABILITY_STOP_FILE" ]]; do
      if status="$(curl --silent --show-error --output /dev/null \
        --write-out '%{http_code}' --connect-timeout 1 --max-time 2 \
        "$AVAILABILITY_URL" 2>/dev/null)"; then
        :
      else
        status=000
      fi
      printf '%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" "$status" \
        >>"$AVAILABILITY_LOG"
      sleep "$AVAILABILITY_INTERVAL_SECONDS"
    done
  ) &
  AVAILABILITY_PID=$!
}

stop_availability_monitor() {
  local enforce="$1"
  local clean=true samples failures
  if [[ -z "$AVAILABILITY_PID" ]]; then
    return 0
  fi

  touch "$AVAILABILITY_STOP_FILE"
  wait "$AVAILABILITY_PID" || true
  AVAILABILITY_PID=""
  samples="$(awk 'NF == 2 { count++ } END { print count + 0 }' "$AVAILABILITY_LOG")"
  failures="$(awk 'NF != 2 || $2 != "200" { count++ } END { print count + 0 }' "$AVAILABILITY_LOG")"
  availability_log_is_clean "$AVAILABILITY_LOG" || clean=false
  sudo install -D -m 0644 "$AVAILABILITY_LOG" "$AVAILABILITY_EVIDENCE_FILE"
  rm -f "$AVAILABILITY_LOG" "$AVAILABILITY_STOP_FILE"
  AVAILABILITY_LOG=""
  AVAILABILITY_STOP_FILE=""
  echo "availability_samples=$samples"
  echo "availability_failures=$failures"
  if [[ "$enforce" == true && "$clean" != true ]]; then
    return 1
  fi
}

cleanup_runtime() {
  local status=$?
  trap - EXIT
  if [[ -n "$AVAILABILITY_PID" ]]; then
    stop_availability_monitor false || true
  fi
  exit "$status"
}

choose_candidate_port() {
  case "$1" in
    8080|18081) echo 18080 ;;
    18080) echo 18081 ;;
    *) return 1 ;;
  esac
}

rewrite_caddy_config() {
  local source="$1"
  local destination="$2"
  local target_port="$3"
  local matches

  valid_port "$target_port" || return 1
  matches="$(grep -Ec '^[[:space:]]*reverse_proxy[[:space:]]+127[.]0[.]0[.]1:(8080|18080|18081)[[:space:]]*$' "$source")"
  [[ "$matches" == 1 ]] || return 1
  sed -E "s#^([[:space:]]*reverse_proxy[[:space:]]+127[.]0[.]0[.]1:)(8080|18080|18081)([[:space:]]*)\$#\\1${target_port}\\3#" \
    "$source" >"$destination"
  [[ "$(grep -Ec "^[[:space:]]*reverse_proxy[[:space:]]+127[.]0[.]0[.]1:${target_port}[[:space:]]*$" "$destination")" == 1 ]]
}

compose() {
  local args=(--env-file "$ENV_FILE" -f "$COMPOSE_FILE")
  if [[ -n "$COMPOSE_OVERRIDE_FILE" ]]; then
    args+=(-f "$COMPOSE_OVERRIDE_FILE")
  fi
  sudo -E docker compose "${args[@]}" "$@"
}

read_root_file() {
  local path="$1"
  if sudo test -f "$path"; then
    sudo cat "$path"
  fi
}

write_state() {
  local path="$1"
  local value="$2"
  local temporary
  temporary="$(mktemp)"
  printf '%s\n' "$value" >"$temporary"
  chmod 0644 "$temporary"
  sudo install -D -m 0644 "$temporary" "$path"
  rm -f "$temporary"
}

slot_marker() {
  echo "$STATE_DIR/slots/$1/background-loops-active"
}

ensure_slot_directory() {
  valid_slot_port "$1" || return 1
  sudo install -d -m 0755 "$STATE_DIR/slots/$1"
}

set_slot_activation() {
  local port="$1"
  local active="$2"
  local marker
  valid_slot_port "$port" || return 0
  ensure_slot_directory "$port"
  marker="$(slot_marker "$port")"
  if [[ "$active" == true ]]; then
    sudo install -m 0644 /dev/null "$marker"
  else
    sudo rm -f "$marker"
  fi
}

container_running() {
  [[ "$(sudo docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null || true)" == true ]]
}

container_version() {
  local port="$1"
  curl -fsS --max-time 5 "http://127.0.0.1:${port}/version" \
    | sed -n 's/.*"version":"\([A-Za-z0-9._-]*\)".*/\1/p'
}

wait_healthy() {
  local port="$1"
  local expected="$2"
  local version
  for _ in {1..60}; do
    version="$(container_version "$port" || true)"
    if curl -fsS --max-time 5 "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1 &&
       curl -fsS --max-time 5 "http://127.0.0.1:${port}/readyz" >/dev/null 2>&1 &&
       [[ "$version" == "$expected" ]]; then
      return 0
    fi
    sleep 2
  done
  return 1
}

switch_caddy() {
  local target_port="$1"
  local workdir original candidate backup
  valid_port "$target_port" || return 1
  workdir="$(mktemp -d)"
  original="$workdir/Caddyfile.original"
  candidate="$workdir/Caddyfile.candidate"
  backup="${CADDY_CONFIG}.surau-previous"
  sudo cp "$CADDY_CONFIG" "$original"
  sudo chown "$(id -u):$(id -g)" "$original"
  if ! rewrite_caddy_config "$original" "$candidate" "$target_port"; then
    rm -rf "$workdir"
    return 1
  fi
  if ! sudo caddy validate --config "$candidate" --adapter caddyfile >/dev/null; then
    rm -rf "$workdir"
    return 1
  fi
  sudo cp "$CADDY_CONFIG" "$backup"
  sudo install -m 0644 "$candidate" "${CADDY_CONFIG}.next"
  sudo mv -f "${CADDY_CONFIG}.next" "$CADDY_CONFIG"
  if ! sudo systemctl reload caddy; then
    sudo cp "$backup" "$CADDY_CONFIG"
    sudo systemctl reload caddy || true
    rm -rf "$workdir"
    return 1
  fi
  rm -rf "$workdir"
}

desired_background_loops() {
  local configured
  configured="$(sed -n 's/^BACKGROUND_LOOPS_ENABLED=//p' "$ENV_FILE" | tail -1)"
  configured="${configured:-true}"
  case "$configured" in
    true|false) echo "$configured" ;;
    *) return 1 ;;
  esac
}

activate_container_loops() {
  local container="$1"
  local port="$2"
  local enabled="$3"
  if [[ "$enabled" != true ]]; then
    set_slot_activation "$port" false
    return 0
  fi
  # The first rollout's legacy Compose container already starts with its
  # configured loop state and predates the signal activation contract.
  if [[ "$port" == 8080 ]]; then
    return 0
  fi
  set_slot_activation "$port" true
  sudo docker kill --signal USR1 "$container" >/dev/null
  for _ in {1..20}; do
    if sudo docker logs --tail=80 "$container" 2>&1 \
      | grep -Fq 'background loops activated'; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

pause_container_loops() {
  local container="$1"
  local port="$2"
  local enabled="$3"
  local paused_at
  set_slot_activation "$port" false
  if [[ "$enabled" != true || "$port" == 8080 ]]; then
    return 0
  fi

  paused_at="$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)"
  sudo docker kill --signal USR2 "$container" >/dev/null
  for _ in {1..120}; do
    if sudo docker logs --since "$paused_at" "$container" 2>&1 \
      | grep -Fq 'background loops paused'; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

resolve_active_state() {
  ACTIVE_PORT="$(read_root_file "$ACTIVE_PORT_FILE")"
  ACTIVE_PORT="${ACTIVE_PORT:-8080}"
  valid_port "$ACTIVE_PORT" || die "active API port state is invalid"

  ACTIVE_CONTAINER="$(read_root_file "$ACTIVE_CONTAINER_FILE")"
  if [[ -z "$ACTIVE_CONTAINER" && "$ACTIVE_PORT" == 8080 ]]; then
    ACTIVE_CONTAINER="$(compose ps -q app | head -1)"
  fi
  [[ -n "$ACTIVE_CONTAINER" ]] || die "active app container is unavailable"
  valid_container "$ACTIVE_CONTAINER" || die "active app container state is invalid"
  container_running "$ACTIVE_CONTAINER" || die "recorded active app container is not running"
}

record_successful_cutover() {
  local old_port="$1" old_container="$2" old_version="$3" old_loops="$4"
  local new_port="$5" new_container="$6" new_loops="$7"
  write_state "$PREVIOUS_PORT_FILE" "$old_port"
  write_state "$PREVIOUS_CONTAINER_FILE" "$old_container"
  write_state "$PREVIOUS_VERSION_FILE" "$old_version"
  write_state "$PREVIOUS_LOOPS_FILE" "$old_loops"
  write_state "$ACTIVE_PORT_FILE" "$new_port"
  write_state "$ACTIVE_CONTAINER_FILE" "$new_container"
  write_state "$ACTIVE_LOOPS_FILE" "$new_loops"
}

restore_previous_after_failure() {
  local old_port="$1" old_container="$2" old_version="$3" old_loops="$4"
  local candidate_port="$5" candidate_container="$6" candidate_loops="$7"
  echo "Candidate cutover failed; restoring previous app slot" >&2

  pause_container_loops "$candidate_container" "$candidate_port" "$candidate_loops" \
    || return 1
  set_slot_activation "$old_port" false
  if ! container_running "$old_container"; then
    sudo docker start "$old_container" >/dev/null || return 1
  fi
  wait_healthy "$old_port" "$old_version" || return 1
  switch_caddy "$old_port" || return 1
  sudo docker stop --time 15 "$candidate_container" >/dev/null || return 1
  activate_container_loops "$old_container" "$old_port" "$old_loops" || return 1
  write_state "$ACTIVE_PORT_FILE" "$old_port"
  write_state "$ACTIVE_CONTAINER_FILE" "$old_container"
  write_state "$ACTIVE_LOOPS_FILE" "$old_loops"
}

keep_candidate_after_restore_failure() {
  local old_port="$1" old_container="$2" old_version="$3" old_loops="$4"
  local candidate_port="$5" candidate_container="$6" candidate_version="$7"
  local candidate_loops="$8"

  echo "Previous slot restoration failed; retaining the healthy candidate slot" >&2
  set_slot_activation "$candidate_port" false
  if ! container_running "$candidate_container"; then
    sudo docker start "$candidate_container" >/dev/null || return 1
  fi
  wait_healthy "$candidate_port" "$candidate_version" || return 1
  switch_caddy "$candidate_port" || return 1
  if container_running "$old_container"; then
    sudo docker stop --time 15 "$old_container" >/dev/null || return 1
  fi
  activate_container_loops "$candidate_container" "$candidate_port" "$candidate_loops" \
    || return 1
  record_successful_cutover "$old_port" "$old_container" "$old_version" "$old_loops" \
    "$candidate_port" "$candidate_container" "$candidate_loops"
}

abort_candidate_cutover() {
  local reason="$1"
  shift
  local old_port="$1" old_container="$2" old_version="$3" old_loops="$4"
  local candidate_port="$5" candidate_container="$6" candidate_version="$7"
  local candidate_loops="$8"

  if ! restore_previous_after_failure "$old_port" "$old_container" "$old_version" \
    "$old_loops" "$candidate_port" "$candidate_container" "$candidate_loops"; then
    if ! keep_candidate_after_restore_failure "$old_port" "$old_container" "$old_version" \
      "$old_loops" "$candidate_port" "$candidate_container" "$candidate_version" \
      "$candidate_loops"; then
      echo "CRITICAL: neither the previous nor candidate app slot could be made authoritative" >&2
    fi
  fi
  die "$reason"
}

restore_current_after_rollback_failure() {
  local current_port="$1" current_container="$2" current_version="$3" current_loops="$4"
  local previous_port="$5" previous_container="$6" previous_version="$7" previous_loops="$8"

  echo "Rollback failed; restoring the pre-rollback app slot" >&2

  # A legacy target (port 8080) cannot start with its loops paused. Quiesce the
  # slot that rollback was targeting before a stopped legacy container starts,
  # so recovery never creates two background-loop writers.
  if [[ "$current_port" == 8080 ]] && container_running "$previous_container"; then
    pause_container_loops "$previous_container" "$previous_port" "$previous_loops" \
      || return 1
  fi

  set_slot_activation "$current_port" false
  if ! container_running "$current_container"; then
    sudo docker start "$current_container" >/dev/null || return 1
  fi
  wait_healthy "$current_port" "$current_version" || return 1
  switch_caddy "$current_port" || return 1

  if container_running "$previous_container"; then
    if valid_slot_port "$previous_port"; then
      # Once Caddy is back on the current slot, a hard stop is the safe fallback
      # if a loop does not acknowledge SIGUSR2.
      pause_container_loops "$previous_container" "$previous_port" "$previous_loops" \
        || sudo docker stop --time 15 "$previous_container" >/dev/null
    fi
    if container_running "$previous_container"; then
      sudo docker stop --time 15 "$previous_container" >/dev/null || return 1
    fi
  fi

  activate_container_loops "$current_container" "$current_port" "$current_loops" \
    || return 1
  record_successful_cutover "$previous_port" "$previous_container" "$previous_version" \
    "$previous_loops" "$current_port" "$current_container" "$current_loops"
}

abort_rollback() {
  local reason="$1"
  shift

  if ! restore_current_after_rollback_failure "$@"; then
    echo "CRITICAL: automatic restoration of the pre-rollback slot failed" >&2
  fi
  die "$reason"
}

deploy_candidate() {
  [[ -f "$ENV_FILE" ]] || die "environment file is missing"
  valid_version "$EXPECTED_VERSION" || die "expected version is invalid"
  resolve_active_state

  local candidate_port candidate_name candidate_id slot_dir old_version old_loops new_loops
  candidate_port="$(choose_candidate_port "$ACTIVE_PORT")" || die "cannot choose an inactive API slot"
  candidate_name="surau-api-slot-${candidate_port}"
  slot_dir="$STATE_DIR/slots/$candidate_port"
  old_version="$(container_version "$ACTIVE_PORT")"
  valid_version "$old_version" || die "active app version is unavailable"
  old_loops="$(read_root_file "$ACTIVE_LOOPS_FILE")"
  old_loops="${old_loops:-$(desired_background_loops)}"
  new_loops="$(desired_background_loops)" || die "BACKGROUND_LOOPS_ENABLED must be true or false"
  start_availability_monitor

  ensure_slot_directory "$candidate_port"
  set_slot_activation "$candidate_port" false
  sudo docker rm -f "$candidate_name" >/dev/null 2>&1 || true
  compose run -d --no-deps --use-aliases \
    --name "$candidate_name" \
    -p "127.0.0.1:${candidate_port}:8080" \
    -e BACKGROUND_LOOPS_ENABLED=false \
    -e BACKGROUND_LOOPS_ACTIVATION_FILE=/run/surau-deploy/background-loops-active \
    -v "$slot_dir:/run/surau-deploy:ro" \
    app >/dev/null
  candidate_id="$(sudo docker inspect -f '{{.Id}}' "$candidate_name")"

  if ! wait_healthy "$candidate_port" "$EXPECTED_VERSION"; then
    sudo docker logs --tail=120 "$candidate_id" >&2 || true
    sudo docker rm -f "$candidate_id" >/dev/null 2>&1 || true
    die "candidate never became healthy"
  fi
  sudo docker update --restart unless-stopped "$candidate_id" >/dev/null

  if ! switch_caddy "$candidate_port"; then
    sudo docker rm -f "$candidate_id" >/dev/null 2>&1 || true
    die "Caddy cutover failed validation or reload"
  fi
  if ! wait_healthy "$candidate_port" "$EXPECTED_VERSION"; then
    abort_candidate_cutover "candidate failed immediately after Caddy cutover" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$old_version" "$old_loops" \
      "$candidate_port" "$candidate_id" "$EXPECTED_VERSION" "$new_loops"
  fi

  if ! pause_container_loops "$ACTIVE_CONTAINER" "$ACTIVE_PORT" "$old_loops"; then
    abort_candidate_cutover "previous app background loops did not pause" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$old_version" "$old_loops" \
      "$candidate_port" "$candidate_id" "$EXPECTED_VERSION" "$new_loops"
  fi
  if ! sudo docker stop --time 15 "$ACTIVE_CONTAINER" >/dev/null; then
    abort_candidate_cutover "previous app did not stop cleanly" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$old_version" "$old_loops" \
      "$candidate_port" "$candidate_id" "$EXPECTED_VERSION" "$new_loops"
  fi
  if ! activate_container_loops "$candidate_id" "$candidate_port" "$new_loops"; then
    abort_candidate_cutover "candidate background loops did not activate" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$old_version" "$old_loops" \
      "$candidate_port" "$candidate_id" "$EXPECTED_VERSION" "$new_loops"
  fi

  record_successful_cutover "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$old_version" "$old_loops" \
    "$candidate_port" "$candidate_id" "$new_loops"
  if ! stop_availability_monitor true; then
    abort_candidate_cutover "API availability monitor observed a failed sample" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$old_version" "$old_loops" \
      "$candidate_port" "$candidate_id" "$EXPECTED_VERSION" "$new_loops"
  fi
  echo "active_port=$candidate_port"
  echo "active_container=$candidate_id"
  echo "previous_port=$ACTIVE_PORT"
  echo "previous_version=$old_version"
}

rollback_candidate() {
  resolve_active_state
  local previous_port previous_container previous_version previous_loops current_version current_loops
  local current_paused=false
  previous_port="$(read_root_file "$PREVIOUS_PORT_FILE")"
  previous_container="$(read_root_file "$PREVIOUS_CONTAINER_FILE")"
  previous_version="$(read_root_file "$PREVIOUS_VERSION_FILE")"
  previous_loops="$(read_root_file "$PREVIOUS_LOOPS_FILE")"
  valid_port "$previous_port" || die "previous API port state is invalid"
  valid_container "$previous_container" || die "previous app container state is invalid"
  valid_version "$previous_version" || die "previous app version state is invalid"
  [[ "$previous_loops" =~ ^(true|false)$ ]] || die "previous loop state is invalid"
  if [[ "$previous_port" == "$ACTIVE_PORT" || "$previous_container" == "$ACTIVE_CONTAINER" ]]; then
    die "previous app state points at the active slot"
  fi
  current_version="$(container_version "$ACTIVE_PORT")"
  valid_version "$current_version" || die "current app version is unavailable"
  current_loops="$(read_root_file "$ACTIVE_LOOPS_FILE")"
  [[ "$current_loops" =~ ^(true|false)$ ]] || die "current loop state is invalid"
  start_availability_monitor

  # The first rollback can target the pre-blue/green Compose container. It
  # starts its loops immediately, so pause the current slot before starting it.
  if [[ "$previous_port" == 8080 ]]; then
    if ! pause_container_loops "$ACTIVE_CONTAINER" "$ACTIVE_PORT" "$current_loops"; then
      abort_rollback "current app background loops did not pause" \
        "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$current_version" "$current_loops" \
        "$previous_port" "$previous_container" "$previous_version" "$previous_loops"
    fi
    current_paused=true
  fi

  set_slot_activation "$previous_port" false
  if ! container_running "$previous_container"; then
    if ! sudo docker start "$previous_container" >/dev/null; then
      abort_rollback "previous app did not start" \
        "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$current_version" "$current_loops" \
        "$previous_port" "$previous_container" "$previous_version" "$previous_loops"
    fi
  fi
  if ! wait_healthy "$previous_port" "$previous_version"; then
    abort_rollback "previous app did not become healthy" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$current_version" "$current_loops" \
      "$previous_port" "$previous_container" "$previous_version" "$previous_loops"
  fi
  if ! switch_caddy "$previous_port"; then
    abort_rollback "Caddy rollback failed" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$current_version" "$current_loops" \
      "$previous_port" "$previous_container" "$previous_version" "$previous_loops"
  fi

  if [[ "$current_paused" != true ]] &&
     ! pause_container_loops "$ACTIVE_CONTAINER" "$ACTIVE_PORT" "$current_loops"; then
    abort_rollback "current app background loops did not pause" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$current_version" "$current_loops" \
      "$previous_port" "$previous_container" "$previous_version" "$previous_loops"
  fi
  if ! sudo docker stop --time 15 "$ACTIVE_CONTAINER" >/dev/null; then
    abort_rollback "current app did not stop cleanly" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$current_version" "$current_loops" \
      "$previous_port" "$previous_container" "$previous_version" "$previous_loops"
  fi
  if ! activate_container_loops "$previous_container" "$previous_port" "$previous_loops"; then
    abort_rollback "previous app loops did not reactivate" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$current_version" "$current_loops" \
      "$previous_port" "$previous_container" "$previous_version" "$previous_loops"
  fi

  record_successful_cutover "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$current_version" \
    "$current_loops" "$previous_port" "$previous_container" "$previous_loops"
  if ! stop_availability_monitor true; then
    abort_rollback "API availability monitor observed a failed sample" \
      "$ACTIVE_PORT" "$ACTIVE_CONTAINER" "$current_version" "$current_loops" \
      "$previous_port" "$previous_container" "$previous_version" "$previous_loops"
  fi
  echo "active_port=$previous_port"
  echo "active_container=$previous_container"
  echo "rolled_back_to=$previous_version"
}

show_status() {
  resolve_active_state
  echo "active_port=$ACTIVE_PORT"
  echo "active_container=$ACTIVE_CONTAINER"
  echo "active_version=$(container_version "$ACTIVE_PORT")"
  echo "active_background_loops=$(read_root_file "$ACTIVE_LOOPS_FILE")"
  echo "previous_port=$(read_root_file "$PREVIOUS_PORT_FILE")"
  echo "previous_version=$(read_root_file "$PREVIOUS_VERSION_FILE")"
}

main() {
  case "$MODE" in
    deploy) deploy_candidate ;;
    rollback) rollback_candidate ;;
    status) show_status ;;
    *) die "usage: $0 deploy <expected-version> | rollback | status" ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  trap cleanup_runtime EXIT
  main "$@"
fi
