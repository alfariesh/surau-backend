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

func TestCatalogParitySamplesTryWholeUnitThenBoundedExactQuotes(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("citable_catalog_parity.go")
	require.NoError(t, err)

	text := string(source)
	assert.Contains(t, text, "(unit.text, 0)")
	assert.Contains(t, text, "substring(unit.text FROM 1 FOR 512)")
	assert.Contains(t, text, "substring(unit.text FROM 3585 FOR 416)")
	assert.Contains(t, text, "strpos(peer.text, candidate.quote) > 0")
	assert.NotContains(t, text, "char_length(btrim(unit.text)) BETWEEN 4 AND 4000",
		"a valid citation is a bounded exact excerpt, not necessarily the whole long unit")
	assert.Equal(t, 1, strings.Count(text, "(unit.text, 0)"))
	assert.Contains(t, text, "ORDER BY candidate.priority")
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
