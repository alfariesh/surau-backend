package backfill

import (
	"context"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCatalogParitySamplesUseBoundedPageUniqueExactQuotes(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("citable_catalog_parity.go")
	require.NoError(t, err)

	text := string(source)
	assert.Contains(t, text, "generate_series(1, 3841, 256)")
	assert.Contains(t, text, "substring(unit.text FROM starts.start_pos FOR 256)")
	assert.Contains(t, text, "char_length(candidate.quote) <= 256")
	assert.Contains(t, text, "strpos(peer.text, candidate.quote) > 0")
	assert.Contains(t, text, "peer.heading_id = unit.heading_id")
	assert.Contains(t, text, "peer.page_id = unit.page_id")
	assert.NotContains(t, text, "(unit.text, 0)")
	assert.Contains(t, text, "ORDER BY candidate.priority")
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
