package backfill

import (
	"context"
	"os"
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
	assert.Contains(t, text, "catalogParityQuoteRunes        = 256")
	assert.Contains(t, text, "catalogParityPageSourceRunes   = 4000")
	assert.Contains(t, text, "catalogParityWindowDivisor     = 2")
	assert.Contains(t, text, "catalogParityQuotesPerWindow   = 2")
	assert.Contains(t, text, "ragRepo.ResolveRAGUnitCitation(")
	assert.NotContains(t, text, "generate_series(")
	assert.NotContains(t, text, "SELECT COUNT(*)\n              FROM public_book_interpretive_citable_units peer")
	assert.Equal(t, 1, catalogParityMaxContextPages)
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
