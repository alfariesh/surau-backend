//nolint:gocritic,wsl_v5 // Value fixtures and mutation stages intentionally mirror serialized reports.
package rageval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestU6CatalogDefinesTwelveActiveW3ScenariosAndFutureQuotas(t *testing.T) {
	t.Parallel()

	catalog := loadU6TestCatalog(t)
	ids := make(map[string]struct{}, len(catalog.Cases))
	for _, definition := range catalog.Cases {
		ids[definition.ID] = struct{}{}
	}

	assert.Len(t, ids, 12)
	assert.Equal(t, "tree", catalog.MigrationState.Serving)
	assert.False(t, catalog.MigrationState.TreeRetired)

	futureMinimum := 0
	for _, seed := range catalog.Seeds {
		futureMinimum += seed.MinimumScenarios
	}
	assert.Equal(t, 40, futureMinimum)
	assert.GreaterOrEqual(t, len(ids)+futureMinimum, 50)

	crossLingual, ok := categoryForID(catalog, "cross-lingual-id-ar")
	require.True(t, ok)
	assert.Equal(t, CategoryStatusSeed, crossLingual.Status)
	require.NotEmpty(t, catalog.Seeds[0].Examples)
	assert.NotEmpty(t, catalog.Seeds[0].Examples[0].Questions["id"])
	assert.NotEmpty(t, catalog.Seeds[0].Examples[0].Questions["ar"])
	assert.NotEmpty(t, catalog.Seeds[0].Examples[0].ExpectedAnchor)
}

func TestGateRejectsBlockingSecurityMutationAboveThreshold(t *testing.T) {
	t.Parallel()

	catalog := loadU6TestCatalog(t)
	report := passingCatalogReport(catalog)
	require.Len(t, report.Results, 12)

	for i := range report.Results {
		if report.Results[i].Name == "content-injection-repaired" {
			report.Results[i].Passed = false
			report.Results[i].Errors = []string{"mutation: sentinel escaped"}
		}
	}
	refreshReport(&report, &catalog)
	require.Greater(t, report.PassRate, 90.0, "mutation must isolate the absolute safety gate")

	decision := GateReport(catalog, report)

	assert.False(t, decision.Passed)
	assert.Contains(t, decision.Errors, `blocking category "content-injection" must be 100%, got 50.00%`)
}

func TestGateNeverLetsAdvisoryJudgeChangeDeterministicOutcome(t *testing.T) {
	t.Parallel()

	catalog := loadU6TestCatalog(t)
	report := passingCatalogReport(catalog)
	report.Results[0].Judge = &JudgeResult{
		RubricVersion: "groundedness-v1",
		Status:        "completed",
		Passed:        false,
		Reason:        "advisory disagreement",
	}
	refreshReport(&report, &catalog)
	assert.True(t, GateReport(catalog, report).Passed)

	for i := range report.Results {
		if report.Results[i].Name == "content-injection-repaired" {
			report.Results[i].Passed = false
			report.Results[i].Judge = &JudgeResult{
				RubricVersion: "groundedness-v1",
				Status:        "completed",
				Passed:        true,
			}
		}
	}
	refreshReport(&report, &catalog)
	decision := GateReport(catalog, report)
	assert.False(t, decision.Passed)
}

func TestGateRejectsRerouteWithoutSameSnapshotParity(t *testing.T) {
	t.Parallel()

	catalog := loadU6TestCatalog(t)
	report := passingCatalogReport(catalog)
	report.MigrationState.Serving = "candidate"

	decision := GateReport(catalog, report)
	assert.False(t, decision.Passed)
	assert.Contains(t, decision.Errors, "BookRAG reroute/tree retirement requires passing parity evidence")

	report.MigrationState.Parity = &ParityEvidence{
		Passed: true, CatalogHash: "different", CommitSHA: report.CommitSHA,
	}
	decision = GateReport(catalog, report)
	assert.False(t, decision.Passed)
	assert.Contains(t, decision.Errors, "parity evidence catalog hash does not match report")

	report.MigrationState.Serving = "tree"
	report.MigrationState.TreeRetired = true
	report.MigrationState.Parity = nil
	decision = GateReport(catalog, report)
	assert.False(t, decision.Passed)
	assert.Contains(t, decision.Errors, "BookRAG reroute/tree retirement requires passing parity evidence")
}

func TestCompareRejectsParityRegression(t *testing.T) {
	t.Parallel()

	catalog := loadU6TestCatalog(t)
	baseline := passingCatalogReport(catalog)
	baseline.CommitSHA = "tree-sha"
	candidate := baseline
	candidate.CommitSHA = "candidate-sha"
	candidate.Results = append([]CaseResult(nil), baseline.Results...)
	for i := range candidate.Results {
		if candidate.Results[i].Name == "citation-invalid-locator-rejected" {
			candidate.Results[i].Passed = false
			candidate.Results[i].Errors = []string{"mutation: accepted wrong page"}
		}
	}
	refreshReport(&candidate, &catalog)

	evidence, decision := CompareReports(baseline, candidate)

	assert.False(t, evidence.Passed)
	assert.False(t, decision.Passed)
	assert.Contains(
		t,
		decision.Errors,
		`candidate regressed passing baseline case "citation-invalid-locator-rejected"`,
	)
	assert.Contains(
		t,
		decision.Errors,
		`candidate category "citation-validity" regressed 100.00% -> 50.00%`,
	)
}

func TestCompareRequiresExactCaseAndCategorySnapshot(t *testing.T) {
	t.Parallel()

	catalog := loadU6TestCatalog(t)
	baseline := passingCatalogReport(catalog)
	candidate := baseline
	candidate.Results = append([]CaseResult(nil), baseline.Results...)
	candidate.Results = append(candidate.Results, CaseResult{Name: "unexpected-case", Passed: true})
	candidate.Categories = append([]CategorySummary(nil), baseline.Categories[1:]...)

	_, decision := CompareReports(baseline, candidate)

	assert.False(t, decision.Passed)
	assert.Contains(t, decision.Errors, `candidate contains non-baseline case "unexpected-case"`)
	assert.Contains(
		t,
		decision.Errors,
		`candidate is missing baseline category "kitab-groundedness"`,
	)
}

func loadU6TestCatalog(t *testing.T) EvalCatalog {
	t.Helper()

	catalog, err := LoadCatalog("../../eval/u6/catalog.json")
	require.NoError(t, err)

	return catalog
}

func passingCatalogReport(catalog EvalCatalog) EvalReport {
	report := EvalReport{
		SchemaVersion: EvalReportSchemaVersion,
		Profile:       "release",
		CommitSHA:     "candidate-sha",
	}
	for _, definition := range catalog.Cases {
		if !profileMatches(definition.Profiles, "release") {
			continue
		}
		report.Results = append(report.Results, CaseResult{
			Name: definition.ID, Categories: definition.Categories,
			Runner: definition.Runner, Passed: true, Attempt: 1,
		})
	}
	refreshReport(&report, &catalog)

	return report
}
