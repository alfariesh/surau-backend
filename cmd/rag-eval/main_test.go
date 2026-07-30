//nolint:gocritic,wsl_v5 // Test fixtures use immutable catalog/report values for readable mutation setup.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alfariesh/surau-backend/internal/rageval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyCasesInvocationRemainsCompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(
			w,
			`{"book_id":1,"answer":"Saya belum menemukan jawaban dalam sumber.",`+
				`"citations":[],"trace":{"retrieval_mode":"full_tree"}}`,
		)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "legacy.jsonl")
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte(`{"name":"legacy","book_id":1,"question":"missing","expect_not_found":true}`+"\n"),
			0o600,
		),
	)
	t.Setenv("RAG_EVAL_SERVICE_TOKEN", "")
	t.Setenv("RAG_EVAL_SERVICE_TOKEN_FILE", "")
	t.Setenv("RAG_EVAL_JUDGE_TOKEN", "")
	t.Setenv("RAG_EVAL_JUDGE_TOKEN_FILE", "")
	var stdout, stderr bytes.Buffer

	exitCode := run(t.Context(), []string{
		"-base-url", server.URL,
		"-cases", path,
		"-retries", "0",
		"-output", "json",
	}, &stdout, &stderr)

	assert.Zero(t, exitCode, stderr.String())
	assert.Contains(t, stdout.String(), `"name": "legacy"`)
}

func TestReportAndGateCommandsRequireEveryNamedPRTest(t *testing.T) {
	t.Parallel()

	catalogPath := "../../eval/u6/catalog.json"
	catalog, err := rageval.LoadCatalog(catalogPath)
	require.NoError(t, err)

	var events strings.Builder
	for _, definition := range catalog.Cases {
		if definition.Runner != rageval.RunnerGoTest ||
			!containsString(definition.Profiles, "pr") {
			continue
		}
		fmt.Fprintf(
			&events,
			`{"Action":"pass","Package":%q,"Test":%q,"Elapsed":0.01}`+"\n",
			definition.Package,
			definition.Test,
		)
	}
	testOutputPath := filepath.Join(t.TempDir(), "tests.jsonl")
	require.NoError(t, os.WriteFile(testOutputPath, []byte(events.String()), 0o600))

	var reportJSON, stderr bytes.Buffer
	exitCode := run(t.Context(), []string{
		"report",
		"-catalog", catalogPath,
		"-profile", "pr",
		"-go-test-json", testOutputPath,
		"-commit-sha", "test-sha",
	}, &reportJSON, &stderr)
	require.Zero(t, exitCode, stderr.String())

	reportPath := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.WriteFile(reportPath, reportJSON.Bytes(), 0o600))
	var decision bytes.Buffer
	exitCode = run(t.Context(), []string{
		"gate", "-catalog", catalogPath, "-report", reportPath,
	}, &decision, &stderr)

	assert.Zero(t, exitCode, stderr.String())
	assert.Contains(t, decision.String(), `"passed": true`)
}

func TestGateCommandRejectsBlockingMutationWithNonZeroExit(t *testing.T) {
	t.Parallel()

	catalogPath := "../../eval/u6/catalog.json"
	catalog, err := rageval.LoadCatalog(catalogPath)
	require.NoError(t, err)

	report := rageval.EvalReport{
		SchemaVersion:  rageval.EvalReportSchemaVersion,
		CatalogHash:    catalog.Hash,
		CommitSHA:      "mutation-sha",
		Profile:        "release",
		MigrationState: catalog.MigrationState,
	}
	for _, definition := range catalog.Cases {
		result := rageval.CaseResult{
			Name: definition.ID, Categories: definition.Categories,
			Runner: definition.Runner, Passed: true,
		}
		if definition.ID == "content-injection-repaired" {
			result.Passed = false
			result.Errors = []string{"mutation: sentinel escaped"}
		}
		report.Results = append(report.Results, result)
	}
	report.Total = len(report.Results)
	report.Passed = report.Total - 1
	report.Failed = 1
	report.PassRate = float64(report.Passed) * 100 / float64(report.Total)
	report.Categories = mutationCategorySummaries(catalog, report.Results)
	require.Greater(t, report.PassRate, 90.0)

	reportPath := filepath.Join(t.TempDir(), "mutation.json")
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(reportPath, raw, 0o600))
	var stdout, stderr bytes.Buffer

	exitCode := run(t.Context(), []string{
		"gate", "-catalog", catalogPath, "-report", reportPath,
	}, &stdout, &stderr)

	assert.Equal(t, 1, exitCode)
	assert.Contains(t, stdout.String(), `"passed": false`)
	assert.Contains(t, stdout.String(), "content-injection")
}

func mutationCategorySummaries(
	catalog rageval.EvalCatalog,
	results []rageval.CaseResult,
) []rageval.CategorySummary {
	summaries := make([]rageval.CategorySummary, 0)
	for _, category := range catalog.Categories {
		if category.Status != rageval.CategoryStatusActive ||
			!containsString(category.Profiles, "release") {
			continue
		}
		summary := rageval.CategorySummary{
			ID: category.ID, Label: category.Label, Status: category.Status,
			Blocking: category.Blocking,
		}
		for _, result := range results {
			if !containsString(result.Categories, category.ID) {
				continue
			}
			summary.Total++
			if result.Passed {
				summary.Passed++
			} else {
				summary.Failed++
			}
		}
		summary.PassRate = float64(summary.Passed) * 100 / float64(summary.Total)
		summaries = append(summaries, summary)
	}

	return summaries
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}
