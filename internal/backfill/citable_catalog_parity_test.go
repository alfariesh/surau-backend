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

type catalogParityRepoStub struct {
	locators map[int]entity.RAGUnitLocator
	hits     []entity.RAGSearchResult
	sources  []entity.RAGPageSource
	err      error
}

func (r catalogParityRepoStub) ResolveRAGUnitCitation(
	_ context.Context,
	_, _, pageID int,
	_ string,
) (entity.RAGUnitLocator, error) {
	if r.err != nil {
		return entity.RAGUnitLocator{}, r.err
	}

	return r.locators[pageID], nil
}

func (r catalogParityRepoStub) SearchRAGPages(
	context.Context,
	int,
	string,
	string,
	int,
) ([]entity.RAGSearchResult, error) {
	return r.hits, r.err
}

func (r catalogParityRepoStub) GetRAGPageSources(
	context.Context,
	int,
	[]int,
	[]int,
	string,
	int,
) ([]entity.RAGPageSource, error) {
	return r.sources, r.err
}

func TestCatalogParitySamplesUseBoundedCandidatesAndExactResolver(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("citable_catalog_parity.go")
	require.NoError(t, err)

	text := string(source)
	assert.Contains(t, text, "LIMIT $2")
	assert.Contains(t, text, "catalogParityCandidatesPerBook = 64")
	assert.Contains(t, text, "catalogParityLegacyHits        = 10")
	assert.Contains(t, text, "catalogParityQuoteRunes        = 256")
	assert.Contains(t, text, "catalogParityPageSourceRunes   = 4000")
	assert.Contains(t, text, "catalogParityUnitSourceRunes   = 4000")
	assert.Contains(t, text, "catalogParityWindowDivisor     = 2")
	assert.Contains(t, text, "catalogParityQuotesPerWindow   = 8")
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
	assert.GreaterOrEqual(t, strings.Count(string(repoSource),
		"array_position($3::integer[]"), 2,
		"legacy and unit source selection must preserve ranked focus-page order")
	assert.Contains(t, string(repoSource),
		"ORDER BY focus_rank ASC, focus_position ASC, page_id ASC")
	assert.Contains(t, string(repoSource),
		"ORDER BY focus_rank ASC, focus_position ASC NULLS LAST, page_id ASC")
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

func TestCatalogParitySampleFollowsResolvableLegacyTopHit(t *testing.T) {
	t.Parallel()

	quote := "نص فريد للاختبار"
	repo := catalogParityRepoStub{
		locators: map[int]entity.RAGUnitLocator{
			1: {Found: true, UnitID: "unit-1", UnitAnchor: "kitab/7/h/11/u/1"},
			2: {Found: true, UnitID: "unit-2", UnitAnchor: "kitab/7/h/12/u/1"},
		},
		hits: []entity.RAGSearchResult{{HeadingID: 11, PageID: 1}},
		sources: []entity.RAGPageSource{{
			BookID: 7, HeadingID: 11, PageID: 1, ContentText: quote,
		}},
	}

	actualQuote, candidate, locator, err := catalogParityCandidateQuote(
		context.Background(),
		repo,
		catalogParityCandidate{
			bookID: 7, headingID: 12, pageID: 2, unitText: quote, pageText: quote,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, quote, actualQuote)
	assert.Equal(t, 11, candidate.headingID)
	assert.Equal(t, 1, candidate.pageID)
	assert.Equal(t, "unit-1", locator.UnitID)
}

func TestCatalogParityTopHitPropagatesRepositoryError(t *testing.T) {
	t.Parallel()

	candidate, locator, resolved, err := catalogParityResolveLegacyTopHit(
		context.Background(), catalogParityRepoStub{err: assert.AnError}, 7,
		[]entity.RAGSearchResult{{HeadingID: 11, PageID: 1}}, "نص",
	)
	require.ErrorIs(t, err, assert.AnError)
	assert.Empty(t, candidate)
	assert.Empty(t, locator)
	assert.False(t, resolved)
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

func TestCatalogParityDiagnosticsDoNotExposeQuoteContent(t *testing.T) {
	t.Parallel()

	errDiagnostic := catalogParityRequestDiagnostic(
		&catalogParitySample{bookID: 7},
		&entity.BookRAGResponse{Citations: []entity.BookRAGCitation{{}}},
		context.DeadlineExceeded,
		2,
	)
	assert.Equal(t, `book=7 class=request citations=1 llm_calls=2 error="context deadline exceeded"`, errDiagnostic)

	unitID := "unit-actual"
	unitAnchor := "kitab/7/h/9/u/1"
	locatorDiagnostic := catalogParityLocatorDiagnostic(
		&catalogParitySample{bookID: 7, headingID: 8, pageID: 3, unitID: "unit-expected"},
		&entity.BookRAGCitation{
			HeadingID:  9,
			PageID:     4,
			UnitID:     &unitID,
			UnitAnchor: &unitAnchor,
			Quote:      "must not be logged",
		},
	)
	assert.Equal(
		t,
		"book=7 class=locator expected_heading=8 expected_page=3 expected_unit=unit-expected "+
			"actual_heading=9 actual_page=4 actual_unit=unit-actual actual_anchor=kitab/7/h/9/u/1",
		locatorDiagnostic,
	)
	assert.NotContains(t, locatorDiagnostic, "must not be logged")
}
