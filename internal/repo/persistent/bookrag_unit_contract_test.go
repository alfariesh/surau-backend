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
	exactQueryStart := strings.Index(querySource, "func (r *BookRAGRepo) searchRAGUnitsExact(")
	exactQueryEnd := strings.Index(querySource, "func (r *BookRAGRepo) searchRAGUnitsFuzzy(")

	require.NotEqual(t, -1, exactQueryStart)
	require.Greater(t, exactQueryEnd, exactQueryStart)

	exactQuery := querySource[exactQueryStart:exactQueryEnd]

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
	assert.Contains(t, querySource, "to_tsvector('simple'::regconfig",
		"common Arabic terms must use the bounded full-text index before any trigram fallback")
	assert.Contains(t, querySource, "plainto_tsquery('simple'::regconfig",
		"the unit search query must use the same immutable full-text configuration as its index")
	assert.Contains(t, querySource, "matches AS MATERIALIZED (",
		"the indexed unit match set must be isolated before publication joins and ranking")
	assert.Contains(t, querySource, "THEN 2::float8",
		"verbatim unit matches must outrank broader full-text matches on the same page")
	assert.Contains(t, querySource, "scored.id::text AS unit_id",
		"unit search must retain the exact lexical unit identity for source ordering")
	assert.Contains(t, querySource, "candidates AS MATERIALIZED (",
		"common terms must be bounded before per-row ranking")
	assert.Contains(t, querySource, "ragUnitExactCandidateLimit = 1024",
		"the exact candidate ceiling must remain explicit and reviewable")
	assert.Contains(t, querySource, "ORDER BY unit.page_id, unit.position, unit.ordinal, unit.id\n    LIMIT $4",
		"the FTS candidate window must be bounded before phrase ranking to preserve the retrieval p95")
	assert.NotContains(t, querySource, "ORDER BY (strpos(",
		"verbatim ranking must not force a full-book substring sort before the candidate limit")
	assert.Less(t, strings.Index(exactQuery, "LIMIT $4"), strings.Index(exactQuery, "THEN 2::float8"),
		"phrase scoring must execute only after the materialized FTS candidate limit")
	assert.Contains(t, exactQuery, "WHEN $5::boolean THEN CASE",
		"single-token common-term searches must bypass normalized phrase scoring")
	assert.Contains(t, querySource, "AND unit.interpretive_retrieval_eligible",
		"the base-table FTS fast path must reproduce the generated structural trust boundary")
	assert.Contains(t, querySource, "JOIN public_book_interpretive_citable_units eligible ON eligible.id = matches.id",
		"the indexed candidates must be authorized again by the structural retrieval view")
	assert.GreaterOrEqual(t, strings.Count(querySource, "pgx.QueryExecModeExec"), 3,
		"book/term-selective RAG queries must not degrade to a corpus-wide generic plan")
	assert.Contains(t, querySource, "if len(exact) > 0 {",
		"trigram fallback must run only when indexed full-text retrieval found no evidence")
	assert.Contains(t, querySource, "WITH exact_pages AS MATERIALIZED (",
		"legacy dual-write probes must use indexed verbatim pages before the broad fuzzy query")
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

func TestShouldRankRAGUnitExactPhraseOnlyForMultipleTokens(t *testing.T) {
	t.Parallel()

	assert.False(t, shouldRankRAGUnitExactPhrase("الله"))
	assert.False(t, shouldRankRAGUnitExactPhrase("  قال  "))
	assert.True(t, shouldRankRAGUnitExactPhrase("محمود بن إسماعيل"))
	assert.True(t, shouldRankRAGUnitExactPhrase("نص\nمتعدد"))
}
