//nolint:wsl_v5 // Advisory judge setup and assertions stay grouped by outcome.
package rageval

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/internal/evalrubric"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectHumanSamplesUsesTwentyPercentWithMinimumTwo(t *testing.T) {
	t.Parallel()

	results := make([]CaseResult, 10)
	for i := range results {
		results[i] = CaseResult{
			Name:     fmt.Sprintf("case-%02d", i),
			Question: "question",
			Judge: &JudgeResult{
				RubricVersion: evalrubric.GroundednessV1,
				Status:        "completed",
				Passed:        true,
			},
		}
	}

	first := SelectHumanSamples(results, "2026-07")
	second := SelectHumanSamples(results, "2026-07")
	assert.Equal(t, first, second)
	assert.Len(t, first, 2)

	assert.Len(t, SelectHumanSamples(results[:3], "2026-07"), 2)
	assert.Empty(t, SelectHumanSamples(nil, "2026-07"))
}

func TestEvaluateJudgeIsAdvisoryAndSameOrigin(t *testing.T) {
	t.Parallel()

	rubric, err := evalrubric.Resolve(evalrubric.GroundednessV1)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/eval/judge", r.URL.Path)
		assert.Equal(t, "machine-token", r.Header.Get("X-Internal-Token"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(
			w,
			`{"case_id":"case","rubric_version":"groundedness-v1",`+
				`"rubric_sha256":%q,"passed":false,"reason":"unsupported",`+
				`"inference":{"generation":{},"usage":{},"cost":{}}}`,
			rubric.SHA256,
		)
	}))
	defer server.Close()

	result := CaseResult{
		Answer: "answer",
		Citations: []Citation{{
			Ref: "1", Quote: "evidence", Anchor: "toc-1",
		}},
	}
	judged := evaluateJudge(t.Context(), server.URL, Options{
		Timeout: server.Client().Timeout, JudgeToken: "machine-token",
	}, GoldenCase{
		Name: "case", Question: "question",
		JudgeRubricVersion: evalrubric.GroundednessV1,
	}, result)

	assert.Equal(t, "completed", judged.Status)
	assert.False(t, judged.Passed)
	assert.Equal(t, rubric.SHA256, judged.RubricSHA256)
}

func TestEvaluateJudgeRejectsCrossOriginTokenTarget(t *testing.T) {
	t.Parallel()

	judged := evaluateJudge(t.Context(), "https://dev-api.surau.org", Options{
		JudgeURL:   "https://example.invalid/v1/eval/judge",
		JudgeToken: "must-not-leak",
	}, GoldenCase{
		Name: "case", Question: "question",
		JudgeRubricVersion: evalrubric.GroundednessV1,
	}, CaseResult{})

	assert.Equal(t, "error", judged.Status)
	assert.Contains(t, judged.Error, "must share")
}
