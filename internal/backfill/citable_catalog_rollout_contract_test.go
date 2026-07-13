package backfill

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK1DevRolloutKeepsDefaultStateOutOfApplicationConfig(t *testing.T) {
	t.Parallel()

	workflow, err := os.ReadFile("../../.github/workflows/deploy-dev.yml")
	require.NoError(t, err)
	override, err := os.ReadFile("../../docker-compose.code-default.yml")
	require.NoError(t, err)

	text := string(workflow)
	normalizeDefault := strings.Index(text, `if [ "$RUNTIME_BOOK_RAG_MODE" = default ]; then`)
	unsetRuntimeOverride := strings.Index(text, `if [ "$CANDIDATE_BOOK_RAG_MODE" = default ]; then`)
	composeUpAfterOverride := strings.Index(
		text[unsetRuntimeOverride:],
		`up -d --force-recreate "${SERVICES[@]}"`,
	)

	require.NotEqual(t, -1, normalizeDefault)
	require.NotEqual(t, -1, unsetRuntimeOverride)
	require.NotEqual(t, -1, composeUpAfterOverride)
	assert.Less(t, normalizeDefault, unsetRuntimeOverride)
	assert.Contains(t, text, "RUNTIME_BOOK_RAG_MODE=unit")
	assert.Contains(t, text, "CANDIDATE_BOOK_RAG_MODE=default")
	assert.Contains(t, text, `EFFECTIVE_BOOK_RAG_MODE="$RUNTIME_BOOK_RAG_MODE"`)
	assert.Contains(t, text, "unset RAG_BOOK_CITATION_MODE")
	assert.Contains(t, text, "remove_env_value RAG_BOOK_CITATION_MODE")
	assert.Contains(t, text, `CANDIDATE_COMPOSE_ARGS=(-f docker-compose.code-default.yml)`)
	assert.Contains(t, text, `ROLLBACK_COMPOSE_ARGS=(-f docker-compose.code-default.yml)`)
	assert.Contains(t, string(override), "RAG_BOOK_CITATION_MODE: null")
	assert.Contains(t, text, `export RAG_BOOK_CITATION_MODE="$RUNTIME_BOOK_RAG_MODE"`)
	assert.NotContains(t, text, `export RAG_BOOK_CITATION_MODE="$CANDIDATE_BOOK_RAG_MODE"`)
	assert.Contains(t, text, `grep -qx "sha=$ACTUAL_SHA" "$ROLLOUT_EVIDENCE"`)
	assert.Contains(t, text, `\"citation_mode\":\"$EFFECTIVE_BOOK_RAG_MODE\"`)
	assert.Contains(t, text, `"legacy_fallback":true`)
	assert.Contains(t, text, `matched_after`)
	assert.Contains(t, text, `sha=%s`)
}

func TestK1DevRolloutKeepsLongCatalogCommandsObservable(t *testing.T) {
	t.Parallel()

	workflow, err := os.ReadFile("../../.github/workflows/deploy-dev.yml")
	require.NoError(t, err)

	text := string(workflow)
	cleanupBackfill := strings.Index(text, "stop_stale_container_processes app backfill")
	cleanupSnapshot := strings.Index(text, "stop_stale_container_processes db pg_dump")
	dockerPrune := strings.Index(text, "sudo docker container prune")
	gitFetch := strings.Index(text, `git fetch origin "$DEPLOY_BRANCH"`)

	require.NotEqual(t, -1, cleanupBackfill)
	require.NotEqual(t, -1, cleanupSnapshot)
	require.NotEqual(t, -1, dockerPrune)
	require.NotEqual(t, -1, gitFetch)
	assert.Less(t, cleanupBackfill, dockerPrune)
	assert.Less(t, cleanupSnapshot, dockerPrune)
	assert.Less(t, cleanupBackfill, gitFetch)
	assert.Less(t, cleanupSnapshot, gitFetch)
	assert.Contains(t, text, "run_with_heartbeat()")
	assert.Contains(t, text, "deploy heartbeat: $label still running")
	assert.Contains(t, text, "run_with_heartbeat k1-priority-catalog")
	assert.Contains(t, text, "run_with_heartbeat k1-full-catalog")
	assert.Contains(t, text, "run_with_heartbeat k1-determinism-rederive")
	assert.Contains(t, text, "run_with_heartbeat k1-catalog-verification")
	assert.Contains(t, text, "stop_stale_container_processes()")
	assert.Contains(t, text, `awk -v name="$process_name" 'NR > 1 && $2 == name { print $1 }'`)
	assert.Contains(t, text, "stop_stale_container_processes app backfill")
	assert.Contains(t, text, "stop_stale_container_processes db pg_dump")
	assert.Contains(t, text, `sudo kill -TERM "${stale_pids[@]}"`)
	assert.Contains(t, text, `sudo kill -KILL "${stale_pids[@]}"`)
	assert.Contains(t, text, "exec -T -e GOMEMLIMIT=640MiB -e GOGC=50 app")
	assert.Contains(t, text, "-o ConnectTimeout=30")
	assert.Contains(t, text, "-o ServerAliveCountMax=20")
	assert.Contains(t, text, "for keyscan_attempt in {1..12}")
	assert.Contains(t, text, "docker builder prune -af --filter 'until=1h'")
	assert.Contains(t, text, `"$DEPLOY_PATH/ops/backup/surau-predeploy-snapshot"`)
	assert.Contains(t, text, "less than 2GiB free after safe cleanup and snapshot")
	assert.Contains(t, text, `vacuumdb -U "$POSTGRES_USER"`)
	assert.Contains(t, text, `--table=citable_unit_catalog_queue' </dev/null`)
}

func TestK1DevRolloutRecoversOnlyKnownInterruptedFTSMigrationAfterSnapshot(t *testing.T) {
	t.Parallel()

	workflow, err := os.ReadFile("../../.github/workflows/deploy-dev.yml")
	require.NoError(t, err)

	text := string(workflow)
	snapshot := strings.Index(text, `"$DEPLOY_PATH/ops/backup/surau-predeploy-snapshot"`)
	recoveryGuard := strings.Index(text, `if [ "$schema_state" = "20260713000016:true" ]; then`)
	indexRecovery := strings.Index(text, "run_with_heartbeat k1-fts-index-recovery")
	build := strings.Index(text, `build "${SERVICES[@]}"`)

	require.NotEqual(t, -1, snapshot)
	require.NotEqual(t, -1, recoveryGuard)
	require.NotEqual(t, -1, indexRecovery)
	require.NotEqual(t, -1, build)
	assert.Less(t, snapshot, recoveryGuard, "no migration repair may precede the protected snapshot")
	assert.Less(t, recoveryGuard, indexRecovery)
	assert.Less(t, indexRecovery, build, "the candidate must not replace the app before the index is ready")
	assert.Contains(t, text, "DROP INDEX CONCURRENTLY IF EXISTS idx_citable_units_text_fts_interpretive")
	assert.Contains(t, text, "SET version=20260713000015, dirty=FALSE")
	assert.Contains(t, text, "WHERE version=20260713000016 AND dirty")
	assert.Contains(t, text, `rewound_state" != "20260713000015:false"`)
	assert.Contains(t, text, "PGOPTIONS=-c maintenance_work_mem=64MB")
	assert.Contains(t, text, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_citable_units_text_fts_interpretive")
	assert.Contains(t, text, `index_valid" != "t"`)
	assert.Contains(t, text, `completed_state" != "20260713000016:false"`)
	assert.Contains(t, text, "sudo docker compose --env-file .env.production -f docker-compose.prod.yml stop app")
	assert.Contains(t, text, "sudo docker compose --env-file .env.production -f docker-compose.prod.yml start app")
}
