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
