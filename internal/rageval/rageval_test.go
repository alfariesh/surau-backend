package rageval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeCases(t *testing.T) {
	t.Parallel()

	input := `
# comment
{"name":"hadith_sahih","book_id":797,"question":"Apa definisi hadis sahih?","expected_page_ids":[12]}

{"name":"not_found","book_id":797,"question":"Apa hukum bitcoin?","expect_not_found":true}
`

	cases, err := DecodeCases(strings.NewReader(input))

	require.NoError(t, err)
	require.Len(t, cases, 2)
	assert.Equal(t, "hadith_sahih", cases[0].Name)
	assert.Equal(t, []int{12}, cases[0].ExpectedPageIDs)
	assert.True(t, cases[1].ExpectNotFound)
}

func TestEvaluateCasePassesExpectedCitation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/books/797/rag", r.URL.Path)
		assert.Equal(t, "id", r.URL.Query().Get("lang"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"book_id":797,
			"question":"Apa definisi hadis sahih?",
			"answer":"Hadis sahih bersambung sanadnya [1].",
			"citations":[{"ref":"1","book_id":797,"heading_id":11,"heading_title":"الصحيح","page_id":12,"anchor":"toc-11","quote":"ما اتصل سنده","url":"/v1/books/797/toc/11/read?lang=id"}],
			"trace":{"retrieval_mode":"full_tree","tree_llm_calls":1,"repaired":false}
		}`)
	}))
	defer server.Close()

	result := EvaluateCase(context.Background(), server.Client(), server.URL, GoldenCase{
		Name:                  "hadith_sahih",
		BookID:                797,
		Question:              "Apa definisi hadis sahih?",
		ExpectedHeadingIDs:    []int{11},
		ExpectedPageIDs:       []int{12},
		ExpectedRetrievalMode: "full_tree",
		MaxTreeLLMCalls:       1,
		AnswerMustContain:     []string{"sanad"},
		QuoteMustContain:      []string{"اتصل"},
	}, false)

	assert.True(t, result.Passed, result.Errors)
	assert.Equal(t, 200, result.StatusCode)
	require.NotNil(t, result.Trace)
	assert.Equal(t, "full_tree", result.Trace.RetrievalMode)
}

func TestEvaluateCasePassesSSEContract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requestBody map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))
		assert.Equal(t, true, requestBody["stream"])

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: meta\ndata: {\"book_id\":797}\n\n")
		fmt.Fprint(w, "event: delta\ndata: {\"text\":\"Jawaban\"}\n\n")
		fmt.Fprint(w, "event: citations\ndata: [{\"ref\":\"1\",\"book_id\":797,\"heading_id\":11,\"page_id\":12,\"anchor\":\"toc-11\",\"quote\":\"x\",\"url\":\"/x\"}]\n\n")
		fmt.Fprint(w, `event: done
data: {"book_id":797,"answer":"Jawaban [1].","citations":[{"ref":"1","book_id":797,"heading_id":11,"page_id":12,"anchor":"toc-11","quote":"x","url":"/x"}],"trace":{"retrieval_mode":"full_tree","citation_mode":"dual","tree_llm_calls":1,"repaired":false}}

`)
	}))
	defer server.Close()

	result := EvaluateCase(context.Background(), server.Client(), server.URL, GoldenCase{
		Name: "stream", BookID: 797, Question: "question", Stream: true,
		ExpectedPageIDs: []int{12}, ExpectedRetrievalMode: "full_tree", ExpectedCitationMode: "dual",
		ForbidLegacyFallback: true, MaxTreeLLMCalls: 1,
	}, false)

	assert.True(t, result.Passed, result.Error, result.Errors)
	require.NotNil(t, result.Trace)
	assert.Equal(t, "dual", result.Trace.CitationMode)
}

func TestEvaluateCaseRejectsLegacyFallback(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"book_id":797,"answer":"Jawaban [1].","citations":[{"ref":"1","book_id":797,"heading_id":11,"page_id":12,"anchor":"toc-11","quote":"x","url":"/x"}],"trace":{"retrieval_mode":"full_tree","citation_mode":"unit","legacy_fallback":true}}`)
	}))
	defer server.Close()

	result := EvaluateCase(context.Background(), server.Client(), server.URL, GoldenCase{
		Name: "fallback", BookID: 797, Question: "question", ExpectedCitationMode: "unit",
		ForbidLegacyFallback: true,
	}, false)

	assert.False(t, result.Passed)
	assert.Contains(t, result.Errors, "legacy fallback is forbidden")
}

func TestEvaluateCaseRejectsIncompleteSSEContract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: meta\ndata: {}\n\n")
		fmt.Fprint(w, "event: done\ndata: {\"book_id\":797}\n\n")
	}))
	defer server.Close()

	result := EvaluateCase(context.Background(), server.Client(), server.URL, GoldenCase{
		Name: "stream", BookID: 797, Question: "question", Stream: true,
	}, false)

	assert.False(t, result.Passed)
	assert.Contains(t, result.Error, "missing required SSE event: delta")
}

func TestDecodeSSERAGResponseRejectsDivergentCitations(t *testing.T) {
	t.Parallel()

	_, err := decodeSSERAGResponse([]byte(`event: meta
data: {}

event: delta
data: {"text":"Jawaban"}

event: citations
data: [{"ref":"1","unit_id":"unit-1"}]

event: done
data: {"citations":[{"ref":"1","unit_id":"unit-2"}]}

`))

	require.ErrorIs(t, err, errSSECitationsDivergent)
}

func TestValidateResponseRequiresUnitCitationLocators(t *testing.T) {
	t.Parallel()

	unitID := "unit-1"
	unitAnchor := "kitab/797/h/11/u/42"
	testCase := GoldenCase{BookID: 797, RequireUnitCitations: true}

	errs, _ := validateResponse(testCase, ragResponse{
		BookID: 797,
		Answer: "Jawaban [1].",
		Citations: []Citation{{
			Ref: "1", BookID: 797, UnitID: &unitID, UnitAnchor: &unitAnchor,
		}},
	}, false)
	assert.Empty(t, errs)

	errs, _ = validateResponse(testCase, ragResponse{
		BookID:    797,
		Answer:    "Jawaban [1].",
		Citations: []Citation{{Ref: "1", BookID: 797}},
	}, false)
	assert.Contains(t, errs, "citation[0] missing unit_id/unit_anchor")
}

func TestEvaluateCaseRequiresUnitAnchorToResolveToCitationUnit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/v1/anchors/resolve" {
			assert.Equal(t, "kitab/797/h/11/u/42", r.URL.Query().Get("anchor"))
			fmt.Fprint(w, `{"boundaries":[{"active_targets":[{"unit_id":"unit-1"}]}]}`)

			return
		}

		fmt.Fprint(w, `{
			"book_id":797,
			"answer":"Jawaban [1].",
			"citations":[{"ref":"1","book_id":797,"heading_id":11,"page_id":12,"anchor":"toc-11","quote":"x","url":"/x","unit_id":"unit-1","unit_anchor":"kitab/797/h/11/u/42"}],
			"trace":{"retrieval_mode":"full_tree","tree_llm_calls":1,"repaired":false}
		}`)
	}))
	defer server.Close()

	result := EvaluateCase(context.Background(), server.Client(), server.URL, GoldenCase{
		Name: "unit_anchor", BookID: 797, Question: "question", RequireUnitCitations: true,
	}, false)

	assert.True(t, result.Passed, result.Errors)
}

func TestEvaluateCaseFailsWrongPage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"book_id":797,
			"answer":"Jawaban [1].",
			"citations":[{"ref":"1","book_id":797,"heading_id":11,"page_id":99,"anchor":"toc-11","quote":"x","url":"/x"}],
			"trace":{"retrieval_mode":"full_tree","tree_llm_calls":1,"repaired":false}
		}`)
	}))
	defer server.Close()

	result := EvaluateCase(context.Background(), server.Client(), server.URL, GoldenCase{
		Name:            "wrong_page",
		BookID:          797,
		Question:        "Apa definisi hadis sahih?",
		ExpectedPageIDs: []int{12},
	}, false)

	assert.False(t, result.Passed)
	assert.Contains(t, result.Errors, "expected citation page in [12]")
}

func TestEvaluateCasePassesNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"book_id":797,
			"answer":"Saya belum menemukan jawaban yang cukup didukung oleh sumber yang tersedia.",
			"citations":[],
			"trace":{"retrieval_mode":"full_tree","tree_llm_calls":2,"repaired":false}
		}`)
	}))
	defer server.Close()

	result := EvaluateCase(context.Background(), server.Client(), server.URL, GoldenCase{
		Name:           "not_found",
		BookID:         797,
		Question:       "Apa hukum bitcoin?",
		ExpectNotFound: true,
	}, false)

	assert.True(t, result.Passed, result.Errors)
}

func TestEvaluateCaseWarnsAnswerMustContainByDefault(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"book_id":797,
			"answer":"Jawaban valid [1].",
			"citations":[{"ref":"1","book_id":797,"heading_id":11,"page_id":12,"anchor":"toc-11","quote":"x","url":"/x"}],
			"trace":{"retrieval_mode":"full_tree","tree_llm_calls":1,"repaired":false}
		}`)
	}))
	defer server.Close()

	result := EvaluateCase(context.Background(), server.Client(), server.URL, GoldenCase{
		Name:              "soft_answer",
		BookID:            797,
		Question:          "Apa definisi hadis sahih?",
		ExpectedPageIDs:   []int{12},
		AnswerMustContain: []string{"sanad"},
	}, false)

	assert.True(t, result.Passed, result.Errors)
	assert.Contains(t, result.Warnings, `answer missing "sanad"`)
}

func TestEvaluateCaseFailsAnswerMustContainWhenStrict(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"book_id":797,
			"answer":"Jawaban valid [1].",
			"citations":[{"ref":"1","book_id":797,"heading_id":11,"page_id":12,"anchor":"toc-11","quote":"x","url":"/x"}],
			"trace":{"retrieval_mode":"full_tree","tree_llm_calls":1,"repaired":false}
		}`)
	}))
	defer server.Close()

	result := EvaluateCase(context.Background(), server.Client(), server.URL, GoldenCase{
		Name:              "strict_answer",
		BookID:            797,
		Question:          "Apa definisi hadis sahih?",
		ExpectedPageIDs:   []int{12},
		AnswerMustContain: []string{"sanad"},
	}, true)

	assert.False(t, result.Passed)
	assert.Contains(t, result.Errors, `answer missing "sanad"`)
	assert.Empty(t, result.Warnings)
}

func TestRunSummary(t *testing.T) {
	t.Parallel()

	casesFile := t.TempDir() + "/cases.jsonl"
	err := os.WriteFile(casesFile, []byte(`{"name":"ok","book_id":797,"question":"Apa definisi hadis sahih?","expected_page_ids":[12]}`+"\n"), 0o600)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"book_id":797,
			"answer":"Jawaban [1].",
			"citations":[{"ref":"1","book_id":797,"heading_id":11,"page_id":12,"anchor":"toc-11","quote":"x","url":"/x"}],
			"trace":{"retrieval_mode":"full_tree","tree_llm_calls":1,"repaired":false}
		}`)
	}))
	defer server.Close()

	summary, err := Run(context.Background(), Options{
		BaseURL:   server.URL,
		CasesPath: casesFile,
		Timeout:   time.Second,
	})

	require.NoError(t, err)
	assert.Equal(t, 1, summary.Total)
	assert.Equal(t, 1, summary.Passed)
	assert.Equal(t, 0, summary.Failed)
}

func TestRunRetriesFailedCase(t *testing.T) {
	t.Parallel()

	casesFile := t.TempDir() + "/cases.jsonl"
	err := os.WriteFile(casesFile, []byte(`{"name":"retry","book_id":797,"question":"Apa definisi hadis sahih?","expected_page_ids":[12]}`+"\n"), 0o600)
	require.NoError(t, err)

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			fmt.Fprint(w, `{"book_id":797,"answer":"Tidak ditemukan.","citations":[],"trace":{"retrieval_mode":"full_tree","tree_llm_calls":1,"repaired":false}}`)
			return
		}
		fmt.Fprint(w, `{
			"book_id":797,
			"answer":"Jawaban [1].",
			"citations":[{"ref":"1","book_id":797,"heading_id":11,"page_id":12,"anchor":"toc-11","quote":"x","url":"/x"}],
			"trace":{"retrieval_mode":"full_tree","tree_llm_calls":1,"repaired":false}
		}`)
	}))
	defer server.Close()

	summary, err := Run(context.Background(), Options{
		BaseURL:   server.URL,
		CasesPath: casesFile,
		Timeout:   time.Second,
		Retries:   1,
	})

	require.NoError(t, err)
	require.Len(t, summary.Results, 1)
	assert.Equal(t, 2, calls)
	assert.Equal(t, 2, summary.Results[0].Attempt)
	assert.Equal(t, 1, summary.Passed)
}

func TestRunSendsServiceIdentityToRAGAndAnchorResolver(t *testing.T) {
	t.Parallel()

	casesFile := t.TempDir() + "/cases.jsonl"
	err := os.WriteFile(casesFile, []byte(`{"name":"scoped","book_id":797,"question":"q","require_unit_citations":true}`+"\n"), 0o600)
	require.NoError(t, err)

	seen := make(map[string]string)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seen[request.URL.Path] = request.Header.Get("X-Internal-Token")
		w.Header().Set("Content-Type", "application/json")

		if request.URL.Path == "/v1/anchors/resolve" {
			fmt.Fprint(w, `{"boundaries":[{"active_targets":[{"unit_id":"unit-1"}]}]}`)

			return
		}

		fmt.Fprint(w, `{
			"book_id":797,"answer":"Jawaban [1].",
			"citations":[{"ref":"1","book_id":797,"heading_id":11,"page_id":12,"anchor":"toc-11","quote":"x","url":"/x","unit_id":"unit-1","unit_anchor":"kitab/797/h/11/u/42"}],
			"trace":{"retrieval_mode":"full_tree","citation_mode":"unit","tree_llm_calls":1,"repaired":false}
		}`)
	}))
	defer server.Close()

	summary, err := Run(context.Background(), Options{ // #nosec G101 -- fake token verifies header propagation
		BaseURL: server.URL, CasesPath: casesFile, Timeout: time.Second,
		ServiceToken: "surau-eval-test-token",
	})
	require.NoError(t, err)
	require.Equal(t, 1, summary.Passed)
	assert.Equal(t, "surau-eval-test-token", seen["/v1/books/797/rag"])
	assert.Equal(t, "surau-eval-test-token", seen["/v1/anchors/resolve"])
}

func TestServiceIdentityTransportDoesNotLeakAcrossOrigin(t *testing.T) {
	t.Parallel()

	externalHeader := make(chan string, 1)

	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		externalHeader <- request.Header.Get("X-Internal-Token")

		w.WriteHeader(http.StatusNoContent)
	}))
	defer external.Close()

	origin, err := url.Parse("https://dev-api.surau.org")
	require.NoError(t, err)

	client := &http.Client{Transport: serviceTokenTransport{
		token: "must-not-leak", origin: origin, next: http.DefaultTransport,
	}}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, external.URL, http.NoBody)
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)

	defer response.Body.Close()

	assert.Empty(t, <-externalHeader)
}
