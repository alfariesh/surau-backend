package perf

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuranPageProfileSQLIsReadOnlyAndBounded(t *testing.T) {
	t.Parallel()

	contents := readFile(t, "quran-page-profile.sql")
	assert.Contains(t, contents, "BEGIN READ ONLY")
	assert.Contains(t, contents, "statement_timeout = '20s'")
	assert.Contains(t, contents, "lock_timeout = '250ms'")
	assert.Contains(t, contents, "EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON)")
	assert.Contains(t, contents, "citable_units_with_effective_license")
	assert.Contains(t, contents, "effective_license_status = 'permitted'")
	assert.Contains(t, contents, "ROLLBACK;")

	writeStatement := regexp.MustCompile(`(?im)^\s*(INSERT|UPDATE|DELETE|MERGE|TRUNCATE|ALTER|CREATE|DROP|VACUUM|ANALYZE|REINDEX|CLUSTER|COPY)\b`)
	assert.False(t, writeStatement.MatchString(contents), "production profile SQL must remain read-only")
	assert.NotContains(t, strings.ToLower(contents), "pg_stat_statements_reset")
}

func TestQuranPageDiagnosticsKeepsHardSafetyGuards(t *testing.T) {
	t.Parallel()

	contents := readFile(t, "quran-page-diagnostics.sh")
	assert.Contains(t, contents, `case "$app_env" in dev|prod)`)
	assert.Contains(t, contents, `case "$phase" in baseline|postdeploy)`)
	assert.Contains(t, contents, `[0-9]+\.[0-9]+\.[0-9]+`)
	assert.Contains(t, contents, `connections" -ge 40`)
	assert.Contains(t, contents, `cpu < 85`)
	assert.Contains(t, contents, `-concurrency 10`)
	assert.Contains(t, contents, `-requests 70`)
	assert.Contains(t, contents, `-p95-budget-ms 200`)
	assert.Contains(t, contents, `run_profiled_bench origin-ramp-c10`)
	assert.Contains(t, contents, `FROM pg_stat_activity`)
	assert.Contains(t, contents, `capture_tempo_traces`)
	assert.Contains(t, contents, `head -30`)
	assert.Contains(t, contents, `PG_POOL_MAX|GOMAXPROCS|OTEL_ENABLED`)
	assert.Contains(t, contents, `if [ "$phase" != baseline ]`)
	assert.Contains(t, contents, `-cache-policy origin-revalidate`)
	assert.Contains(t, contents, `query_class`)
	assert.Contains(t, contents, `benchmark_gate_failed=true`)
	assert.Contains(t, contents, `diagnostics_complete=true`)
	assert.NotContains(t, contents, `regexp_replace(query`)
	assert.NotContains(t, contents, `query_shape`)
	assert.NotContains(t, contents, "pg_stat_statements_reset")
	assert.NotContains(t, contents, "source .env.production")
}

func TestQuranPagePerformanceWorkflowPreservesFailureEvidence(t *testing.T) {
	t.Parallel()

	contents := readFile(t, "../../.github/workflows/quran-page-performance.yml")
	assert.Contains(t, contents, `[[ "$VPS_HOST" =~ ^[A-Za-z0-9.-]+$ ]]`)
	assert.Contains(t, contents, `[[ "$VPS_USER" =~ ^[A-Za-z0-9_-]+$ ]]`)
	assert.Contains(t, contents, `[[ "$VPS_DEPLOY_PATH" =~ ^/[A-Za-z0-9_./-]+$ ]]`)
	assert.Contains(t, contents, `-cache-policy "$cache_policy"`)
	assert.Contains(t, contents, `remote_status=$?`)
	assert.Contains(t, contents, `scp "surau-perf:$artifact_path"`)
	assert.Contains(t, contents, `public-failures.txt`)
	assert.Contains(t, contents, `Enforce public postdeploy gate`)
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	contents, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(contents)
}
