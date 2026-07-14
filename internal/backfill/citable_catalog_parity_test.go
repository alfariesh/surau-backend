package backfill

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCatalogParitySamplesUseBoundedCandidatesAndExactResolver(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("citable_catalog_parity.go")
	require.NoError(t, err)

	text := string(source)
	assert.Contains(t, text, "LIMIT $2")
	assert.Contains(t, text, "catalogParityCandidatesPerBook = 64")
	assert.Contains(t, text, "catalogParityLegacyHits        = 12")
	assert.Contains(t, text, "catalogParityQuoteRunes        = 256")
	assert.Contains(t, text, "catalogParityPageSourceRunes   = 4000")
	assert.Contains(t, text, "catalogParityUnitSourceRunes   = 4000")
	assert.Contains(t, text, "catalogParityWindowDivisor     = 2")
	assert.Contains(t, text, "catalogParityQuotesPerWindow   = 2")
	assert.Contains(t, text, "ragRepo.ResolveRAGUnitCitation(")
	assert.Contains(t, text, "ragRepo.SearchRAGPages(")
	assert.Contains(t, text, "pagePrefix")
	assert.NotContains(t, text, "generate_series(")
	assert.NotContains(t, text, "SELECT COUNT(*)\n              FROM public_book_interpretive_citable_units peer")
	assert.Equal(t, 1, catalogParityMaxContextPages)
}

func TestCatalogParityQuoteMustBeVisibleInsideLegacyPrompt(t *testing.T) {
	t.Parallel()

	candidate := catalogParityCandidate{
		pageText: strings.Repeat("ا", catalogParityPageSourceRunes) + "بukti di luar prompt",
	}

	assert.True(t, catalogParityQuoteVisibleInLegacySource(candidate, strings.Repeat("ا", 32)))
	assert.False(t, catalogParityQuoteVisibleInLegacySource(candidate, "بukti di luar prompt"))
}

func TestCatalogParitySearchUsesPerBookFTSAndSourceHeadingOrder(t *testing.T) {
	t.Parallel()

	repoSource, err := os.ReadFile("../repo/persistent/bookrag_postgres.go")
	require.NoError(t, err)
	assert.Contains(t, string(repoSource),
		"ORDER BY heading.depth DESC, heading.ordinal DESC, exact.page_id ASC, heading.heading_id ASC")

	migration, err := os.ReadFile("../../migrations/20260714000003_add_k1_book_unit_fts_index.up.sql")
	require.NoError(t, err)

	migrationSQL := string(migration)
	assert.Contains(t, migrationSQL, "'book' || book_id::text || ' '")
	assert.Equal(t, 1, strings.Count(migrationSQL, "CREATE INDEX CONCURRENTLY"))
	assert.Equal(t, 1, strings.Count(strings.TrimSpace(migrationSQL), ";"),
		"the concurrent-index migration must remain one SQL statement")
	assert.Contains(t, string(repoSource), "'book' || unit.book_id::text || ' '")
	assert.Contains(t, string(repoSource), "'book' || ($1::integer)::text")
}

func TestCatalogParityLegacyFirstHitMustMatchUnitLocator(t *testing.T) {
	t.Parallel()

	candidate := catalogParityCandidate{headingID: 11, pageID: 12}
	assert.True(t, catalogParityLegacyFirstHitMatches([]entity.RAGSearchResult{{HeadingID: 11, PageID: 12}}, candidate))
	assert.False(t, catalogParityLegacyFirstHitMatches([]entity.RAGSearchResult{
		{HeadingID: 11, PageID: 9},
		{HeadingID: 11, PageID: 12},
	}, candidate))
	assert.False(t, catalogParityLegacyFirstHitMatches(nil, candidate))
}

func TestCatalogParityStubSelectsRefForExpectedPage(t *testing.T) {
	t.Parallel()

	llm := &catalogParityLLM{headingID: 11, pageID: 12, quote: "نص مكرر"}
	response, err := llm.Complete(context.Background(), []entity.RAGChatMessage{
		{Role: "system", Content: "You answer questions about one classical Islamic book."},
		{Role: "user", Content: `Question:
نص مكرر

SOURCE BLOCKS:
[1] heading_id=11 title="باب" page_id=11 printed_page= part=
Arabic source:
نص مكرر

[2] heading_id=11 title="باب" page_id=12 printed_page= part=
Arabic source:
مقدمة نص مكرر خاتمة
`},
	})

	require.NoError(t, err)
	assert.JSONEq(t, `{"answer":"Bukti katalog [2].","citations":[{"ref":"2","quote":"نص مكرر"}]}`, response)
}

func TestCatalogParityStubRejectsQuoteOutsideRetrievedBlocks(t *testing.T) {
	t.Parallel()

	llm := &catalogParityLLM{headingID: 11, pageID: 12, quote: "نص بعد حد القطع"}
	response, err := llm.Complete(context.Background(), []entity.RAGChatMessage{
		{Role: "system", Content: "You answer questions about one classical Islamic book."},
		{Role: "user", Content: `SOURCE BLOCKS:
[1] heading_id=11 title="باب" page_id=12 printed_page= part=
Arabic source:
مقدمة فقط
`},
	})

	require.NoError(t, err)
	assert.JSONEq(t, `{"answer":"Bukti tidak ditemukan dalam source block.","citations":[]}`, response)
}
