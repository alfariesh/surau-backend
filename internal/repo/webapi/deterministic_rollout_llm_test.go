package webapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeterministicRolloutLLMSelectsLexicalHeading(t *testing.T) {
	t.Parallel()

	client := NewDeterministicRolloutLLMClient()
	content, err := client.Complete(t.Context(), []entity.RAGChatMessage{
		{Role: "system", Content: "tree planner"},
		{Role: "user", Content: "- id=4 lexical_hint=false\n- id=9 lexical_hint=true"},
	})

	require.NoError(t, err)
	assert.JSONEq(t, `{"thinking":"deterministic rollout evidence","node_ids":[9],"done":true}`, content)
}

func TestDeterministicRolloutLLMSelectsCompactTreeHeading(t *testing.T) {
	t.Parallel()

	client := NewDeterministicRolloutLLMClient()
	content, err := client.Complete(t.Context(), []entity.RAGChatMessage{
		{Role: "system", Content: "tree planner"},
		{Role: "user", Content: `Compact TOC tree JSON:\n[{"id":3,"title":"x"}]`},
	})

	require.NoError(t, err)
	assert.JSONEq(t, `{"thinking":"deterministic rollout evidence","node_ids":[3],"done":true}`, content)
}

func TestDeterministicRolloutLLMCopiesQuestionAsExactQuote(t *testing.T) {
	t.Parallel()

	const quote = "ولد ليلة النحر سنة تسع وخمسمائة."

	client := NewDeterministicRolloutLLMClient()
	content, err := client.Complete(t.Context(), []entity.RAGChatMessage{
		{Role: "system", Content: "You answer questions about one classical Islamic book."},
		{Role: "user", Content: "Question:\n" + quote + "\n\nSOURCE BLOCKS:\n" +
			"[7] heading_id=4 title=\"مولده\" page_id=1 printed_page=3 part=\n" +
			"Arabic source:\nمولده:\n" + quote + "\n"},
	})

	require.NoError(t, err)

	var response struct {
		Answer    string `json:"answer"`
		Citations []struct {
			Ref   string `json:"ref"`
			Quote string `json:"quote"`
		} `json:"citations"`
	}
	require.NoError(t, json.Unmarshal([]byte(content), &response))
	assert.Contains(t, response.Answer, "[7]")
	require.Len(t, response.Citations, 1)
	assert.Equal(t, "7", response.Citations[0].Ref)
	assert.Equal(t, quote, response.Citations[0].Quote)
}

func TestDeterministicRolloutLLMStreamEmitsOneValidatedPayload(t *testing.T) {
	t.Parallel()

	client := NewDeterministicRolloutLLMClient()

	var emitted string

	err := client.Stream(context.Background(), []entity.RAGChatMessage{
		{Role: "user", Content: `[{"id":5}]`},
	}, func(content string) error {
		emitted += content

		return nil
	})

	require.NoError(t, err)
	assert.JSONEq(t, `{"thinking":"deterministic rollout evidence","node_ids":[5],"done":true}`, emitted)
}
