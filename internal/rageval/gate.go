//nolint:gocritic,wsl_v5 // Immutable reports are compared by value and gate checks stay linearly auditable.
package rageval

import (
	"fmt"
	"slices"
	"strings"
)

// GateDecision is the deterministic policy result. Advisory judge outcomes are
// intentionally absent from this type.
type GateDecision struct {
	Passed bool     `json:"passed"`
	Errors []string `json:"errors,omitempty"`
}

// GateReport enforces W3 thresholds and absolute safety categories.
//
//nolint:cyclop,funlen,gocognit,gocyclo // Every branch is an independent fail-closed acceptance criterion.
func GateReport(catalog EvalCatalog, report EvalReport) GateDecision {
	decision := GateDecision{Passed: true}
	fail := func(message string) {
		decision.Passed = false
		decision.Errors = append(decision.Errors, message)
	}

	if report.SchemaVersion != EvalReportSchemaVersion {
		fail(fmt.Sprintf("report schema=%q, want %q", report.SchemaVersion, EvalReportSchemaVersion))
	}
	if report.CatalogHash != catalog.Hash {
		fail("report catalog hash does not match the gate catalog")
	}
	recomputed := report
	refreshReport(&recomputed, &catalog)
	if report.Total != recomputed.Total || report.Passed != recomputed.Passed ||
		report.Failed != recomputed.Failed || report.PassRate != recomputed.PassRate {
		fail("report deterministic aggregates do not match its case results")
	}
	if recomputed.PassRate < catalog.MinimumPassRate {
		fail(fmt.Sprintf(
			"deterministic pass-rate %.2f%% is below %.2f%%",
			recomputed.PassRate,
			catalog.MinimumPassRate,
		))
	}

	requiredDefinitions := make(map[string]CatalogCase)
	for _, definition := range catalog.Cases {
		if slices.Contains(definition.Profiles, report.Profile) {
			requiredDefinitions[definition.ID] = definition
		}
	}
	resultNames := make(map[string]struct{}, len(report.Results))
	for _, result := range report.Results {
		if _, duplicate := resultNames[result.Name]; duplicate {
			fail(fmt.Sprintf("case %q produced duplicate results", result.Name))
		}
		resultNames[result.Name] = struct{}{}
		if _, ok := requiredDefinitions[result.Name]; !ok {
			fail(fmt.Sprintf("report contains unknown case %q for profile %q", result.Name, report.Profile))
		}
	}
	for id := range requiredDefinitions {
		if _, ok := resultNames[id]; !ok {
			fail(fmt.Sprintf("required case %q produced no result", id))
		}
	}

	for _, summary := range recomputed.Categories {
		category, ok := categoryForID(catalog, summary.ID)
		if !ok {
			fail(fmt.Sprintf("report contains unknown category %q", summary.ID))

			continue
		}
		if summary.Total < category.MinimumCases {
			fail(fmt.Sprintf(
				"category %q ran %d cases, minimum %d",
				summary.ID,
				summary.Total,
				category.MinimumCases,
			))
		}
		if category.Blocking && summary.PassRate != 100 {
			fail(fmt.Sprintf(
				"blocking category %q must be 100%%, got %.2f%%",
				summary.ID,
				summary.PassRate,
			))
		}
	}

	for _, category := range catalog.Categories {
		if category.Status != CategoryStatusActive || !profileMatches(category.Profiles, report.Profile) {
			continue
		}
		if !reportHasCategory(recomputed, category.ID) {
			fail(fmt.Sprintf("active category %q is missing from the report", category.ID))
		}
	}

	if report.MigrationState.Serving != "tree" || report.MigrationState.TreeRetired { //nolint:nestif // parity subchecks apply only to attempted serving changes.
		parity := report.MigrationState.Parity
		if parity == nil || !parity.Passed {
			fail("BookRAG reroute/tree retirement requires passing parity evidence")
		} else {
			if parity.CatalogHash != report.CatalogHash {
				fail("parity evidence catalog hash does not match report")
			}
			if strings.TrimSpace(parity.CommitSHA) == "" || parity.CommitSHA != report.CommitSHA {
				fail("parity evidence commit does not match report")
			}
		}
	}

	return decision
}

// CompareReports proves a candidate is not worse than the current tree on the
// same cases and catalog. It does not mutate serving state.
//
//nolint:cyclop,funlen,gocognit,gocyclo // Case and category regressions are deliberately checked independently.
func CompareReports(baseline, candidate EvalReport) (ParityEvidence, GateDecision) {
	decision := GateDecision{Passed: true}
	fail := func(message string) {
		decision.Passed = false
		decision.Errors = append(decision.Errors, message)
	}

	if baseline.CatalogHash == "" || baseline.CatalogHash != candidate.CatalogHash {
		fail("baseline and candidate must use the same catalog hash")
	}
	if baseline.Profile != candidate.Profile {
		fail("baseline and candidate must use the same profile")
	}

	baselineCases := casePassMap(baseline.Results)
	candidateCases := casePassMap(candidate.Results)
	for name, baselinePassed := range baselineCases {
		candidatePassed, ok := candidateCases[name]
		if !ok {
			fail(fmt.Sprintf("candidate is missing baseline case %q", name))
		} else if baselinePassed && !candidatePassed {
			fail(fmt.Sprintf("candidate regressed passing baseline case %q", name))
		}
	}
	for name := range candidateCases {
		if _, ok := baselineCases[name]; !ok {
			fail(fmt.Sprintf("candidate contains non-baseline case %q", name))
		}
	}

	baselineCategories := categorySummaryMap(baseline.Categories)
	candidateCategories := categorySummaryMap(candidate.Categories)
	for id, baselineCategory := range baselineCategories {
		candidateCategory, ok := candidateCategories[id]
		if !ok {
			fail(fmt.Sprintf("candidate is missing baseline category %q", id))

			continue
		}
		if candidateCategory.PassRate < baselineCategory.PassRate {
			fail(fmt.Sprintf(
				"candidate category %q regressed %.2f%% -> %.2f%%",
				candidateCategory.ID,
				baselineCategory.PassRate,
				candidateCategory.PassRate,
			))
		}
		if candidateCategory.Blocking && candidateCategory.PassRate != 100 {
			fail(fmt.Sprintf("candidate blocking category %q is not 100%%", candidateCategory.ID))
		}
		if candidateCategory.ID == "citation-validity" && candidateCategory.PassRate != 100 {
			fail("candidate citation-validity must be 100%")
		}
	}
	for id := range candidateCategories {
		if _, ok := baselineCategories[id]; !ok {
			fail(fmt.Sprintf("candidate contains non-baseline category %q", id))
		}
	}

	evidence := ParityEvidence{
		Passed:       decision.Passed,
		CatalogHash:  candidate.CatalogHash,
		CommitSHA:    candidate.CommitSHA,
		BaselineSHA:  baseline.CommitSHA,
		CandidateSHA: candidate.CommitSHA,
	}

	return evidence, decision
}

func reportHasCategory(report EvalReport, id string) bool {
	for _, summary := range report.Categories {
		if summary.ID == id {
			return true
		}
	}

	return false
}

func casePassMap(results []CaseResult) map[string]bool {
	result := make(map[string]bool, len(results))
	for _, item := range results {
		result[item.Name] = item.Passed
	}

	return result
}

func categorySummaryMap(categories []CategorySummary) map[string]CategorySummary {
	result := make(map[string]CategorySummary, len(categories))
	for _, category := range categories {
		result[category.ID] = category
	}

	return result
}
