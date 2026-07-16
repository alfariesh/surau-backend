#!/usr/bin/env bash
set -euo pipefail

# Fixed, read-only production diagnostics for GET /v1/quran/pages/{page}/ayahs.
# The GitHub workflow copies this script and the benchmark binary to /tmp.

if [ "$#" -ne 6 ]; then
  echo "usage: $0 <dev|prod> <baseline|postdeploy> <expected-version> <deploy-path> <bench-binary> <profile-sql>" >&2
  exit 2
fi

app_env="$1"
phase="$2"
expected_version="$3"
deploy_path="$4"
bench_binary="$5"
profile_sql="$6"

case "$app_env" in dev|prod) ;; *) echo "invalid environment" >&2; exit 2 ;; esac
case "$phase" in baseline|postdeploy) ;; *) echo "invalid phase" >&2; exit 2 ;; esac
if [[ ! "$expected_version" =~ ^(dev-[0-9a-f]{7}|[0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
  echo "invalid expected version" >&2
  exit 2
fi
for required_file in "$bench_binary" "$profile_sql"; do
  if [ ! -f "$required_file" ]; then
    echo "missing diagnostic input: $required_file" >&2
    exit 2
  fi
done

cd "$deploy_path"
umask 077
output_dir="$(mktemp -d "/tmp/surau-quran-page-${app_env}-${phase}.XXXXXX")"
diagnostics_complete=false
benchmark_gate_failed=false
diagnostics_failed=false
cleanup_failed_run() {
  if [ "$diagnostics_complete" != true ]; then
    rm -rf "$output_dir"
  fi
}
trap cleanup_failed_run EXIT
printf '%s\n' "$output_dir"

compose=(sudo docker compose --env-file .env.production -f docker-compose.prod.yml)
active_port=8080
active_port_file=/var/lib/surau/deploy/active-api-port
active_container_file=/var/lib/surau/deploy/active-api-container
if [ -f "$active_port_file" ]; then
  candidate_port="$(tr -d '[:space:]' < "$active_port_file")"
  if [[ "$candidate_port" =~ ^(18080|18081)$ ]]; then
    active_port="$candidate_port"
  else
    echo "invalid active API port state" >&2
    exit 1
  fi
fi
origin_url="http://127.0.0.1:${active_port}"
app_container_id="$(sudo cat "$active_container_file" 2>/dev/null || true)"
if [[ ! "$app_container_id" =~ ^[a-f0-9]{12,64}$ ]] ||
   [[ "$(sudo docker inspect -f '{{.State.Running}}' "$app_container_id" 2>/dev/null || true)" != true ]]; then
  app_container_id="$("${compose[@]}" ps -q app | head -1)"
fi
if [[ ! "$app_container_id" =~ ^[a-f0-9]{12,64}$ ]]; then
  echo "active app container is unavailable" >&2
  exit 1
fi

psql_readonly() {
  # Expansion belongs inside the database container, where these two env vars live.
  # shellcheck disable=SC2016
  "${compose[@]}" exec -T db sh -c \
    'psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' sh "$@"
}

guard_resources() {
  local connections cpu_value container_id
  connections="$(psql_readonly -Atc "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()")"
  if ! [[ "$connections" =~ ^[0-9]+$ ]] || [ "$connections" -ge 40 ]; then
    echo "abort: PostgreSQL connections are ${connections:-unknown}, safety limit is 39" >&2
    return 1
  fi

  container_id="$app_container_id"
  cpu_value="$(sudo docker stats --no-stream --format '{{.CPUPerc}}' "$container_id" | tr -d '%')"
  if ! [[ "$cpu_value" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    echo "abort: cannot read application CPU" >&2
    return 1
  fi
  awk -v cpu="$cpu_value" 'BEGIN { exit !(cpu < 85) }' || {
    echo "abort: application CPU is ${cpu_value}%, safety limit is 85%" >&2
    return 1
  }
}

capture_database_state() {
  local label="$1"
  psql_readonly >"$output_dir/database-${label}.txt" 2>&1 <<'SQL'
\pset pager off
\timing on
SELECT now() AS captured_at,
       current_setting('server_version') AS server_version,
       current_setting('shared_buffers') AS shared_buffers,
       current_setting('effective_cache_size') AS effective_cache_size,
       current_setting('work_mem') AS work_mem,
       current_setting('track_io_timing') AS track_io_timing,
       current_setting('max_connections') AS max_connections;

SELECT count(*) AS total_connections,
       count(*) FILTER (WHERE state = 'active') AS active_connections,
       count(*) FILTER (WHERE wait_event_type IS NOT NULL) AS waiting_connections
FROM pg_stat_activity
WHERE datname = current_database();

SELECT relname, n_live_tup, n_dead_tup, seq_scan, idx_scan,
       last_analyze, last_autoanalyze
FROM pg_stat_user_tables
WHERE relname IN (
  'quran_ayahs', 'quran_ayah_translations', 'quran_ayah_transliterations',
  'quran_citable_unit_bindings', 'citable_units', 'quran_translation_sources',
  'quran_transliteration_sources'
)
ORDER BY relname;

SELECT tablename, indexname, indexdef
FROM pg_indexes
WHERE tablename IN (
  'quran_ayahs', 'quran_ayah_translations', 'quran_ayah_transliterations',
  'quran_citable_unit_bindings', 'citable_units'
)
ORDER BY tablename, indexname;

SELECT indexrelname, idx_scan, idx_tup_read, idx_tup_fetch,
       pg_size_pretty(pg_relation_size(indexrelid)) AS index_size
FROM pg_stat_user_indexes
WHERE relname IN (
  'quran_ayahs', 'quran_ayah_translations', 'quran_ayah_transliterations',
  'quran_citable_unit_bindings', 'citable_units'
)
ORDER BY relname, indexrelname;

SELECT queryid, calls, plans,
       total_plan_time, min_plan_time, max_plan_time, mean_plan_time, stddev_plan_time,
       total_exec_time, min_exec_time, max_exec_time, mean_exec_time, stddev_exec_time,
       rows,
       shared_blks_hit, shared_blks_read, temp_blks_read, temp_blks_written,
       shared_blk_read_time, shared_blk_write_time,
       temp_blk_read_time, temp_blk_write_time,
       query
FROM pg_stat_statements
WHERE query ILIKE '%quran_ayah%'
   OR query ILIKE '%quran_citable_unit_bindings%'
   OR query ILIKE '%citable_units_with_effective_license%'
ORDER BY total_exec_time DESC
LIMIT 30;

SELECT count(*) AS ayahs,
       count(page_number) AS assigned,
       count(DISTINCT page_number) AS pages,
       min(page_number) AS min_page,
       max(page_number) AS max_page,
       md5(string_agg(ayah_key || ':' || page_number::text, '|' ORDER BY surah_id, ayah_number)) AS page_map_hash
FROM quran_ayahs;

SELECT count(*) AS bindings,
       count(DISTINCT unit_id) AS bound_units,
       md5(string_agg(unit_id::text || ':' || role || ':' || ordinal::text, '|' ORDER BY surah_id, ayah_number, ordinal)) AS binding_hash
FROM quran_citable_unit_bindings;

SELECT effective_license_status, count(*)
FROM citable_units_with_effective_license
WHERE corpus = 'quran'
GROUP BY effective_license_status
ORDER BY effective_license_status;

-- Semantic fingerprints exclude only operational timestamps that the
-- idempotent reconcile is allowed to refresh. They prove that deploy/profile
-- activity did not lose or rewrite Quran text, translations (including
-- footnotes), transliterations, Citable Unit identities/Anchors, bindings, or
-- source/effective licensing state.
WITH fingerprints AS (
    SELECT 'quran_surahs'::TEXT AS dataset,
           count(*) AS row_count,
           md5(COALESCE(string_agg(
               (to_jsonb(s) - 'updated_at' - 'units_derived_at' - 'units_stale_at')::TEXT,
               '|' ORDER BY surah_id
           ), '')) AS content_hash
    FROM quran_surahs s

    UNION ALL

    SELECT 'quran_ayahs', count(*), md5(COALESCE(string_agg(
        (to_jsonb(a) - 'updated_at')::TEXT,
        '|' ORDER BY surah_id, ayah_number
    ), ''))
    FROM quran_ayahs a

    UNION ALL

    SELECT 'quran_translations', count(*), md5(COALESCE(string_agg(
        (to_jsonb(t) - 'updated_at')::TEXT,
        '|' ORDER BY source_id, surah_id, ayah_number
    ), ''))
    FROM quran_ayah_translations t

    UNION ALL

    SELECT 'quran_transliterations', count(*), md5(COALESCE(string_agg(
        (to_jsonb(x) - 'updated_at')::TEXT,
        '|' ORDER BY source_id, surah_id, ayah_number
    ), ''))
    FROM quran_ayah_transliterations x

    UNION ALL

    SELECT 'quran_citable_units', count(*), md5(COALESCE(string_agg(
        (to_jsonb(u) - 'created_at' - 'updated_at')::TEXT,
        '|' ORDER BY id
    ), ''))
    FROM citable_units u
    WHERE corpus = 'quran'

    UNION ALL

    SELECT 'quran_citable_bindings', count(*), md5(COALESCE(string_agg(
        (to_jsonb(b) - 'created_at' - 'updated_at')::TEXT,
        '|' ORDER BY surah_id, ayah_number, ordinal
    ), ''))
    FROM quran_citable_unit_bindings b

    UNION ALL

    SELECT 'quran_translation_sources', count(*), md5(COALESCE(string_agg(
        to_jsonb(ts)::TEXT, '|' ORDER BY id
    ), ''))
    FROM quran_translation_sources ts

    UNION ALL

    SELECT 'quran_transliteration_sources', count(*), md5(COALESCE(string_agg(
        to_jsonb(xs)::TEXT, '|' ORDER BY id
    ), ''))
    FROM quran_transliteration_sources xs

    UNION ALL

    SELECT 'quran_script_sources', count(*), md5(COALESCE(string_agg(
        to_jsonb(ss)::TEXT, '|' ORDER BY id
    ), ''))
    FROM quran_script_sources ss

    UNION ALL

    SELECT 'quran_effective_licenses', count(*), md5(COALESCE(string_agg(
        concat_ws(':', id::TEXT, effective_license_status, license_source),
        '|' ORDER BY id
    ), ''))
    FROM citable_units_with_effective_license
    WHERE corpus = 'quran'
)
SELECT dataset, row_count, content_hash
FROM fingerprints
ORDER BY dataset;
SQL
}

capture_ingress_state() {
  local service service_state
  {
    for service in nginx caddy; do
      if command -v systemctl >/dev/null 2>&1; then
        service_state="$(sudo systemctl is-active "$service" 2>/dev/null || true)"
        printf '%s=%s\n' "$service" "${service_state:-unknown}"
      fi
    done
    printf '%s\n' listeners
    sudo ss -ltnH 2>/dev/null \
      | awk '$4 ~ /:(80|443|8080|18080|18081)$/ { print $1, $4 }' \
      || true

    if command -v nginx >/dev/null 2>&1 && sudo systemctl is-active --quiet nginx; then
      printf '%s\n' nginx-api-routing
      sudo nginx -T 2>/dev/null \
        | awk '
          /^# configuration file / { config = $0 }
          /^[[:space:]]*server_name[[:space:]].*api\.surau\.org/ { print config; print }
          /^[[:space:]]*proxy_pass[[:space:]]+http:\/\/127\.0\.0\.1:(8080|18080|18081)/ { print config; print }
        ' \
        || true
    fi
  } >"$output_dir/ingress.txt"
}

run_bench() {
  local name="$1" load_status
  shift
  guard_resources

  set +e
  "$bench_binary" \
    -base-url "$origin_url" \
    -expected-version "$expected_version" \
    -cache-policy origin-revalidate \
    -output "$output_dir/${name}.json" \
    "$@"
  load_status=$?
  set -e
  if [ "$load_status" -ne 0 ]; then
    printf '%s exit=%s\n' "$name" "$load_status" >>"$output_dir/benchmark-failures.txt"
    if [ "$phase" != baseline ]; then
      benchmark_gate_failed=true
    fi
  fi
}

run_profiled_bench() {
  local name="$1"
  shift
  local load_pid load_status runtime_sample sample_number

  guard_resources
  "$bench_binary" \
    -base-url "$origin_url" \
    -expected-version "$expected_version" \
    -cache-policy origin-revalidate \
    -output "$output_dir/${name}.json" \
    "$@" &
  load_pid=$!
  sample_number=0

  while kill -0 "$load_pid" 2>/dev/null && [ "$sample_number" -lt 20 ]; do
    sample_number=$((sample_number + 1))
    psql_readonly >>"$output_dir/${name}-activity.txt" <<'SQL'
WITH classified AS (
  SELECT state,
         COALESCE(wait_event_type, '') AS wait_event_type,
         COALESCE(wait_event, '') AS wait_event,
         CASE
           WHEN pid = pg_backend_pid() THEN 'diagnostic'
           WHEN query ILIKE '%quran_ayahs%'
             OR query ILIKE '%quran_citable_unit_bindings%'
             OR query ILIKE '%citable_units_with_effective_license%'
             THEN 'quran-reader'
           ELSE 'other'
         END AS query_class
  FROM pg_stat_activity
  WHERE datname = current_database()
)
SELECT clock_timestamp() AS captured_at,
       state,
       wait_event_type,
       wait_event,
       query_class,
       count(*) AS connections
FROM classified
GROUP BY state, wait_event_type, wait_event, query_class
ORDER BY state, wait_event_type, wait_event, query_class;
SQL
    runtime_sample="$(sudo docker stats --no-stream --format \
      '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.PIDs}}' \
      "$app_container_id")"
    printf '%s\n' "$runtime_sample" >>"$output_dir/${name}-runtime.txt"
    sleep 1
  done

  set +e
  wait "$load_pid"
  load_status=$?
  set -e
  if [ "$load_status" -ne 0 ]; then
    printf '%s exit=%s\n' "$name" "$load_status" >>"$output_dir/benchmark-failures.txt"
    if [ "$phase" != baseline ]; then
      echo "profiled benchmark $name failed" >&2
      benchmark_gate_failed=true
    fi
  fi
}

capture_tempo_traces() {
  local tempo_container_id tempo_ip trace_id
  tempo_container_id="$("${compose[@]}" ps -q tempo 2>/dev/null || true)"
  if [ -z "$tempo_container_id" ]; then
    printf 'Tempo container is not running.\n' >"$output_dir/tempo-unavailable.txt"
    return
  fi

  tempo_ip="$(sudo docker inspect -f \
    '{{range .NetworkSettings.Networks}}{{println .IPAddress}}{{end}}' \
    "$tempo_container_id" | sed -n '1p')"
  if [[ ! "$tempo_ip" =~ ^[0-9]+([.][0-9]+){3}$ ]]; then
    printf 'Tempo container address is unavailable.\n' >"$output_dir/tempo-unavailable.txt"
    return
  fi

  # The SDK exports in batches; allow one batch interval before reading traces.
  sleep 6
  while IFS= read -r trace_id; do
    if [[ "$trace_id" =~ ^[0-9a-f]{32}$ ]]; then
      curl -fsS --max-time 10 \
        "http://${tempo_ip}:3200/api/traces/${trace_id}" \
        >"$output_dir/trace-${trace_id}.json" || \
        printf 'trace %s was not available before the artifact deadline\n' "$trace_id" \
          >>"$output_dir/tempo-missing.txt"
    fi
  done < <(
    grep -hEo '"trace_id": "[0-9a-f]{32}"' "$output_dir"/*.json 2>/dev/null \
      | sed -E 's/.*"([0-9a-f]{32})"/\1/' | sort -u | head -30
  )
}

curl -fsS --max-time 10 "$origin_url/healthz" >/dev/null
curl -fsS --max-time 10 "$origin_url/readyz" >/dev/null
actual_version="$(curl -fsS --max-time 10 "$origin_url/version" \
  | sed -n 's/.*"version":"\([A-Za-z0-9._-]*\)".*/\1/p')"
if [ "$actual_version" != "$expected_version" ]; then
  echo "origin version mismatch: expected $expected_version, got $actual_version" >&2
  exit 1
fi

{
  date -u +'%Y-%m-%dT%H:%M:%SZ'
  printf 'environment=%s\nphase=%s\nversion=%s\norigin=%s\n' \
    "$app_env" "$phase" "$actual_version" "$origin_url"
  uname -a
  "${compose[@]}" ps
  sudo docker stats --no-stream --format 'table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.PIDs}}'
  printf '%s\n' 'selected_app_environment:'
  sudo docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' \
    "$app_container_id" \
    | grep -E '^(PG_POOL_MAX|GOMAXPROCS|OTEL_ENABLED|OTEL_TRACE_SAMPLE_RATIO|HTTP_USE_PREFORK_MODE)=' \
    || true
  sudo docker inspect -f \
    'cpu_quota={{.HostConfig.CpuQuota}} cpu_period={{.HostConfig.CpuPeriod}} cpuset={{.HostConfig.CpusetCpus}} memory={{.HostConfig.Memory}}' \
    "$app_container_id"
} >"$output_dir/runtime.txt"

capture_ingress_state
if sudo test -f /var/lib/surau/deploy/last-api-availability.tsv; then
  sudo cat /var/lib/surau/deploy/last-api-availability.tsv \
    | tee "$output_dir/deploy-availability.tsv" >/dev/null
fi

if ! capture_database_state before; then
  printf 'database-before exit=1\n' >>"$output_dir/diagnostic-failures.txt"
  diagnostics_failed=true
fi

run_bench origin-default \
  -pages 1,48,421,604 -requests 12 -warmups-per-page 1 -concurrency 1 \
  -query 'view=reader_minimal'
run_bench origin-worst-page \
  -pages 585 -requests 4 -warmups-per-page 0 -concurrency 1 \
  -query 'view=reader_minimal'
run_bench origin-no-translation-ar \
  -pages 1,585 -requests 4 -warmups-per-page 0 -concurrency 1 \
  -query 'view=reader_minimal&lang=ar&include_translation=false'
run_bench origin-no-translation-id \
  -pages 1,585 -requests 4 -warmups-per-page 0 -concurrency 1 \
  -query 'view=reader_minimal&lang=id&include_translation=false'
run_bench origin-full \
  -pages 1 -requests 2 -warmups-per-page 0 -concurrency 1 \
  -query 'view=full'
run_bench origin-audio \
  -pages 1 -requests 2 -warmups-per-page 0 -concurrency 1 \
  -query 'view=reader_minimal&include_audio=true'

for concurrency in 1 2 5; do
  run_bench "origin-ramp-c${concurrency}" \
    -pages 1,421,585,604 -requests 10 -warmups-per-page 0 \
    -concurrency "$concurrency" -query 'view=reader_minimal'
done

run_profiled_bench origin-ramp-c10 \
  -pages 1,421,585,604 -requests 10 -warmups-per-page 0 \
  -concurrency 10 -query 'view=reader_minimal'

if [ "$phase" = postdeploy ]; then
  run_bench origin-gate-mixed-c10 \
    -pages 1,48,421,585,604 -requests 70 -warmups-per-page 0 -concurrency 10 \
    -p95-budget-ms 200 -query 'view=reader_minimal'
  run_bench origin-gate-worst-c10 \
    -pages 585 -requests 70 -warmups-per-page 0 -concurrency 10 \
    -p95-budget-ms 200 -query 'view=reader_minimal'
fi

for page in 1 585; do
  set +e
  psql_readonly -v "page_number=$page" -f - \
    <"$profile_sql" >"$output_dir/explain-page-${page}.json.txt" 2>&1
  explain_status=$?
  set -e
  if [ "$explain_status" -ne 0 ]; then
    printf 'explain-page-%s exit=%s\n' "$page" "$explain_status" \
      >>"$output_dir/diagnostic-failures.txt"
    diagnostics_failed=true
  fi
done

app_pid="$(sudo docker inspect -f '{{.State.Pid}}' "$app_container_id")"
if command -v perf >/dev/null 2>&1 && [[ "$app_pid" =~ ^[0-9]+$ ]]; then
  set +e
  "$bench_binary" -base-url "$origin_url" -expected-version "$expected_version" \
    -cache-policy origin-revalidate \
    -pages 585 -requests 2 -warmups-per-page 0 -concurrency 1 \
    -query 'view=reader_minimal' -output "$output_dir/perf-load.json" &
  load_pid=$!
  sudo timeout 15s perf record -F 99 -g -p "$app_pid" -o "$output_dir/perf.data" -- sleep 15
  perf_status=$?
  wait "$load_pid"
  load_status=$?
  if [ "$perf_status" -eq 0 ]; then
    sudo chown "$(id -u):$(id -g)" "$output_dir/perf.data"
    perf report --stdio -i "$output_dir/perf.data" >"$output_dir/perf-report.txt"
  else
    printf 'perf attach unavailable (exit %s)\n' "$perf_status" >"$output_dir/perf-report.txt"
    rm -f "$output_dir/perf.data"
  fi
  set -e
  if [ "$load_status" -ne 0 ]; then
    printf 'perf-load exit=%s\n' "$load_status" >>"$output_dir/diagnostic-failures.txt"
  fi
else
  printf 'perf is not installed or the app PID is unavailable; use local Go CPU profile fallback.\n' \
    >"$output_dir/perf-report.txt"
fi

capture_tempo_traces

if ! capture_database_state after; then
  printf 'database-after exit=1\n' >>"$output_dir/diagnostic-failures.txt"
  diagnostics_failed=true
fi
guard_resources

tar -C "$output_dir" -czf "${output_dir}.tar.gz" .
diagnostics_complete=true
printf 'artifact=%s.tar.gz\n' "$output_dir"
if [ "$diagnostics_failed" = true ] || [ "$benchmark_gate_failed" = true ]; then
  exit 1
fi
