#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=ops/deploy/blue-green-app.sh
source "$root/ops/deploy/blue-green-app.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

[[ "$(choose_candidate_port 8080)" == 18080 ]] || fail "first deploy slot"
[[ "$(choose_candidate_port 18080)" == 18081 ]] || fail "alternate from blue"
[[ "$(choose_candidate_port 18081)" == 18080 ]] || fail "alternate from green"
if choose_candidate_port 9000 >/dev/null 2>&1; then
  fail "invalid active port accepted"
fi

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT
source_config="$workdir/Caddyfile"
candidate_config="$workdir/Caddyfile.next"
cat >"$source_config" <<'CADDY'
api.example.test {
  handle_path /collab* {
    reverse_proxy 127.0.0.1:8090
  }
  handle /grafana* {
    reverse_proxy 127.0.0.1:3000
  }
  reverse_proxy 127.0.0.1:8080
}
CADDY

rewrite_caddy_config "$source_config" "$candidate_config" 18080 \
  || fail "valid Caddy rewrite failed"
grep -q '^  reverse_proxy 127.0.0.1:18080$' "$candidate_config" \
  || fail "API upstream was not switched"
grep -q '^    reverse_proxy 127.0.0.1:8090$' "$candidate_config" \
  || fail "collab upstream changed"
grep -q '^    reverse_proxy 127.0.0.1:3000$' "$candidate_config" \
  || fail "Grafana upstream changed"

cp "$source_config" "$workdir/Caddyfile.ambiguous"
printf '%s\n' 'reverse_proxy 127.0.0.1:18081' >>"$workdir/Caddyfile.ambiguous"
if rewrite_caddy_config "$workdir/Caddyfile.ambiguous" "$candidate_config" 18081; then
  fail "ambiguous API upstream was accepted"
fi
rewrite_caddy_config "$candidate_config" "$workdir/Caddyfile.rollback" 8080 \
  || fail "rollback to the legacy port was rejected"
if rewrite_caddy_config "$source_config" "$candidate_config" 9000; then
  fail "invalid target port was accepted"
fi

valid_container 'surau-api-slot-18080' || fail "valid container name rejected"
valid_container '0123456789abcdef0123456789abcdef' || fail "valid container id rejected"
if valid_container '../unsafe'; then
  fail "unsafe container name accepted"
fi
valid_version 'dev-abcdef0' || fail "valid version rejected"
if valid_version 'bad version'; then
  fail "unsafe version accepted"
fi
valid_availability_url 'https://api.example.test/healthz' \
  || fail "valid availability URL rejected"
if valid_availability_url 'https://api.example.test/readyz?secret=value'; then
  fail "unsafe availability URL accepted"
fi

cat >"$workdir/availability-clean.tsv" <<'EOF'
2026-07-16T00:00:00.000000000Z	200
2026-07-16T00:00:00.250000000Z	200
2026-07-16T00:00:00.500000000Z	200
EOF
availability_log_is_clean "$workdir/availability-clean.tsv" \
  || fail "clean availability evidence rejected"
cp "$workdir/availability-clean.tsv" "$workdir/availability-failed.tsv"
printf '%s\t%s\n' '2026-07-16T00:00:00.750000000Z' 000 \
  >>"$workdir/availability-failed.tsv"
if availability_log_is_clean "$workdir/availability-failed.tsv"; then
  fail "failed availability sample accepted"
fi

grep -q -- '--use-aliases' "$root/ops/deploy/blue-green-app.sh" \
  || fail "candidate must retain the app network alias"
grep -q 'BACKGROUND_LOOPS_ENABLED=false' "$root/ops/deploy/blue-green-app.sh" \
  || fail "candidate must start with background loops paused"
grep -q -- '--signal USR1' "$root/ops/deploy/blue-green-app.sh" \
  || fail "cutover must activate background loops without restart"
grep -q -- '--signal USR2' "$root/ops/deploy/blue-green-app.sh" \
  || fail "rollback must pause active background loops before overlap"
grep -q 'systemctl reload caddy' "$root/ops/deploy/blue-green-app.sh" \
  || fail "Caddy must reload without stopping its listener"
grep -q 'AVAILABILITY_URL' "$root/ops/deploy/blue-green-app.sh" \
  || fail "cutover must monitor the public health route"

# Exercise rollback recovery without Docker or root access. The recovery must
# restore the original Caddy target, stop the rejected slot, reactivate exactly
# the original writer, and persist the recovered active state.
declare -a recovery_events=()
current_running=true
previous_running=true
container_running() {
  case "$1" in
    current-container|old-container) [[ "$current_running" == true ]] ;;
    previous-container|candidate-container) [[ "$previous_running" == true ]] ;;
    *) return 1 ;;
  esac
}
set_slot_activation() { recovery_events+=("marker:$1:$2"); }
wait_healthy() { recovery_events+=("healthy:$1:$2"); }
switch_caddy() { recovery_events+=("caddy:$1"); }
pause_container_loops() { recovery_events+=("pause:$1:$2:$3"); }
activate_container_loops() { recovery_events+=("activate:$1:$2:$3"); }
write_state() { recovery_events+=("state:$1:$2"); }
sudo() {
  recovery_events+=("sudo:$*")
  if [[ "$1 $2 $3" == "docker stop --time" ]]; then
    previous_running=false
  elif [[ "$1 $2" == "docker start" ]]; then
    current_running=true
  fi
}

restore_current_after_rollback_failure \
  18080 current-container current-version true \
  18081 previous-container previous-version true \
  || fail "blue/green rollback recovery failed"
recovery_trace="$(printf '%s\n' "${recovery_events[@]}")"
grep -q '^caddy:18080$' <<<"$recovery_trace" \
  || fail "rollback recovery did not restore Caddy"
grep -q '^pause:previous-container:18081:true$' <<<"$recovery_trace" \
  || fail "rollback recovery did not quiesce the rejected writer"
grep -q '^activate:current-container:18080:true$' <<<"$recovery_trace" \
  || fail "rollback recovery did not reactivate the original writer"
grep -q "^state:${ACTIVE_PORT_FILE}:18080$" <<<"$recovery_trace" \
  || fail "rollback recovery did not persist the active port"

# Candidate-cutover recovery must not swallow health, Caddy, stop, or loop
# activation failures and then write a false active state. Its successful path
# quiesces the candidate before the previous writer becomes authoritative.
recovery_events=()
current_running=true
previous_running=true
restore_previous_after_failure \
  18080 old-container old-version true \
  18081 candidate-container true \
  || fail "candidate cutover recovery failed"
recovery_trace="$(printf '%s\n' "${recovery_events[@]}")"
pause_line="$(grep -n '^pause:candidate-container:18081:true$' <<<"$recovery_trace" | cut -d: -f1)"
caddy_line="$(grep -n '^caddy:18080$' <<<"$recovery_trace" | cut -d: -f1)"
activate_line="$(grep -n '^activate:old-container:18080:true$' <<<"$recovery_trace" | cut -d: -f1)"
[[ -n "$pause_line" && -n "$caddy_line" && -n "$activate_line" ]] \
  || fail "candidate cutover recovery omitted an authority transition"
[[ "$pause_line" -lt "$caddy_line" && "$caddy_line" -lt "$activate_line" ]] \
  || fail "candidate cutover recovery writer order is unsafe"
grep -q "^state:${ACTIVE_PORT_FILE}:18080$" <<<"$recovery_trace" \
  || fail "candidate cutover recovery did not persist the restored port"

# Restoring the legacy container is the only asymmetric case: it cannot start
# paused, so the rejected blue/green writer must be quiesced first.
recovery_events=()
current_running=false
previous_running=true
restore_current_after_rollback_failure \
  8080 current-container current-version true \
  18080 previous-container previous-version true \
  || fail "legacy rollback recovery failed"
recovery_trace="$(printf '%s\n' "${recovery_events[@]}")"
pause_line="$(grep -n '^pause:previous-container:18080:true$' <<<"$recovery_trace" | head -1 | cut -d: -f1)"
start_line="$(grep -n '^sudo:docker start current-container$' <<<"$recovery_trace" | cut -d: -f1)"
[[ -n "$pause_line" && -n "$start_line" && "$pause_line" -lt "$start_line" ]] \
  || fail "legacy recovery started before the blue/green writer paused"

echo "blue-green deploy tests passed"
