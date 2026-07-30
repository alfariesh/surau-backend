package rageval

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errReportFixtureRead = errors.New("fixture read failure")

func TestMergeGoTestJSONBuildsDashboardAndReviewPacket(t *testing.T) {
	t.Parallel()

	catalog := validTestCatalog()
	catalog.Hash = "catalog-sha"
	startedAt := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	report := EvalReport{
		SchemaVersion: EvalReportSchemaVersion,
		Profile:       "pr",
		StartedAt:     startedAt,
		Results: []CaseResult{
			{
				Name: "http-case", Categories: []string{"active"},
				Runner: RunnerHTTPBookRAG, Passed: true, Question: "Question?",
				Answer: "Supported [1].",
				Citations: []Citation{
					{Ref: "1", Anchor: "toc-1", Quote: "Supported"},
				},
				Judge: &JudgeResult{
					RubricVersion: "groundedness-v1", Status: "completed", Passed: true,
					Reason: "supported",
				},
			},
		},
	}
	events := strings.NewReader(
		"not-json\n" +
			`{"Action":"output","Package":"example/internal/check","Test":"TestCheck"}` + "\n" +
			`{"Action":"pass","Package":"example/internal/check","Test":"TestCheck","Elapsed":0.125}` + "\n" +
			`{"Action":"pass","Package":"unrelated","Test":"TestOther"}` + "\n",
	)

	merged, err := MergeGoTestJSON(report, catalog, "pr", events)

	require.NoError(t, err)
	assert.Equal(t, EvalReportSchemaVersion, merged.SchemaVersion)
	assert.Equal(t, "catalog-sha", merged.CatalogHash)
	assert.Equal(t, 2, merged.Total)
	assert.Equal(t, 2, merged.Passed)
	assert.Zero(t, merged.Failed)
	require.Len(t, merged.Categories, 1)
	assert.Equal(t, 100.0, merged.Categories[0].PassRate)
	require.Len(t, merged.Judges, 1)
	assert.Equal(t, 100.0, merged.Judges[0].PassRate)
	require.Len(t, merged.HumanSamples, 1)
	assert.Equal(t, "http-case", merged.HumanSamples[0].CaseID)
	assert.Equal(t, "pending_human_review", merged.HumanSamples[0].ReviewState)
	assert.GreaterOrEqual(t, merged.DurationMS, int64(0))

	var markdown bytes.Buffer
	require.NoError(t, writeMarkdown(&markdown, merged))
	assert.Contains(t, markdown.String(), "Deterministic: **2/2 passed (100.00%)**")
	assert.Contains(t, markdown.String(), "Advisory LLM judge")
	assert.Contains(t, markdown.String(), "Human sampling queue")
	assert.Contains(t, markdown.String(), "≥ threshold")
}

func TestMergeGoTestJSONRecordsFailureAndMatchesPackageSuffix(t *testing.T) {
	t.Parallel()

	catalog := validTestCatalog()
	events := strings.NewReader(
		`{"Action":"fail","Package":"prefix/example/internal/check","Test":"TestCheck","Elapsed":0.5}` + "\n",
	)

	report, err := MergeGoTestJSON(EvalReport{}, catalog, "pr", events)

	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	assert.False(t, report.Results[0].Passed)
	assert.Equal(t, int64(500), report.Results[0].DurationMS)
	assert.Equal(t, []string{"named deterministic test failed"}, report.Results[0].Errors)
	assert.False(t, report.StartedAt.IsZero())
}

func TestMergeGoTestJSONPropagatesScannerError(t *testing.T) {
	t.Parallel()

	_, err := MergeGoTestJSON(
		EvalReport{},
		validTestCatalog(),
		"pr",
		reportErrorReader{},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan go test JSON")
}

func TestLoadReportRoundTripAndErrors(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	reportPath := filepath.Join(tempDir, "report.json")
	raw, err := json.Marshal(EvalReport{SchemaVersion: EvalReportSchemaVersion, Total: 1})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(reportPath, raw, 0o600))

	report, err := LoadReport(reportPath)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Total)

	_, err = LoadReport(filepath.Join(tempDir, "missing.json"))
	assert.Error(t, err)

	brokenPath := filepath.Join(tempDir, "broken.json")
	require.NoError(t, os.WriteFile(brokenPath, []byte("{"), 0o600))
	_, err = LoadReport(brokenPath)
	assert.Error(t, err)
}

func TestWriteMarkdownShowsBlockingAndSkippedJudge(t *testing.T) {
	t.Parallel()

	report := EvalReport{
		Profile: "release", Total: 1, Passed: 1, PassRate: 100,
		Categories: []CategorySummary{
			{Label: "Safety", Blocking: true, Total: 1, Passed: 1, PassRate: 100},
		},
		Judges: []JudgeSummary{
			{RubricVersion: "groundedness-v1", Skipped: 1},
		},
	}

	var output bytes.Buffer
	require.NoError(t, writeMarkdown(&output, report))
	assert.Contains(t, output.String(), "100% blocking")
	assert.Contains(t, output.String(), "| groundedness-v1 | 0 | 0 | 1 | 0.00% |")
}

type reportErrorReader struct{}

func (reportErrorReader) Read([]byte) (int, error) {
	return 0, errReportFixtureRead
}
