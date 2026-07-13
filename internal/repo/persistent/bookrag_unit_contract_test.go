package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBookRAGUnitQueriesUseStructuralPublicView freezes the K-1 trust boundary:
// RAG must not reconstruct provenance/license eligibility in application SQL.
func TestBookRAGUnitQueriesUseStructuralPublicView(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("bookrag_postgres.go")
	require.NoError(t, err)

	querySource := string(source)
	assert.GreaterOrEqual(t, strings.Count(
		querySource,
		"FROM public_book_interpretive_citable_units cu",
	), 4, "preflight, unit retrieval, lexical search, and citation mapping must use the structural view")
	assert.GreaterOrEqual(t, strings.Count(
		querySource,
		"cu.content_role = 'book_page'",
	), 4, "translation/summary units must not fan out legacy page citations")
	assert.Contains(t, querySource, "strpos(cu.text, $4) > 0",
		"dual projection must be an exact quote match")
	assert.Contains(t, querySource, "chosen_pages AS (",
		"unit retrieval must cap pages before expanding every eligible unit on those pages")
	assert.Contains(t, querySource, "JOIN chosen_pages chosen ON chosen.page_id = cu.page_id",
		"all eligible units must be expanded only after the page cap")
	assert.NotContains(t, querySource, "cu.effective_license_status = 'permitted'",
		"B-4 grandfather visibility belongs to public_book_publications/view, not a stricter RAG override")
	assert.Contains(t, querySource, "FROM citable_units materialized",
		"preflight must distinguish missing materialization from structurally ineligible evidence")
	assert.Contains(t, querySource, "return entity.ErrRAGEvidenceNotFound",
		"eligibility denial must fail closed instead of activating legacy fallback")
}

func TestBookRAGStructuralViewExcludesMarkerLinkedQuranFootnotes(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile("../../../migrations/20260713000001_add_k1_citable_catalog.up.sql")
	require.NoError(t, err)

	schema := string(migration)
	assert.Contains(t, schema, "kind <> 'quran_quote'",
		"every Quran quote is structurally ineligible, including marker-linked footnotes")
	assert.Contains(t, schema, "interpretive_retrieval_eligible",
		"Book-RAG public view must inherit the generated eligibility boundary")
}
