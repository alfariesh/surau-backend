package rageval

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateCatalogRejectsMalformedContracts(t *testing.T) {
	t.Parallel()

	testCases := map[string]func(*EvalCatalog){
		"schema": func(catalog *EvalCatalog) {
			catalog.SchemaVersion = "eval-catalog-v2"
		},
		"threshold": func(catalog *EvalCatalog) {
			catalog.MinimumPassRate = 0
		},
		"serving state": func(catalog *EvalCatalog) {
			catalog.MigrationState.Serving = "candidate"
		},
		"category identity": func(catalog *EvalCatalog) {
			catalog.Categories[0].Owner = ""
		},
		"category status": func(catalog *EvalCatalog) {
			catalog.Categories[0].Status = "disabled"
		},
		"duplicate category": func(catalog *EvalCatalog) {
			catalog.Categories = append(catalog.Categories, catalog.Categories[0])
		},
		"case identity": func(catalog *EvalCatalog) {
			catalog.Cases[0].ID = ""
		},
		"unknown case category": func(catalog *EvalCatalog) {
			catalog.Cases[0].Categories = []string{"unknown"}
		},
		"HTTP source": func(catalog *EvalCatalog) {
			catalog.Cases[0].SourceName = ""
		},
		"Go test target": func(catalog *EvalCatalog) {
			catalog.Cases[1].Test = ""
		},
		"runner": func(catalog *EvalCatalog) {
			catalog.Cases[0].Runner = "shell"
		},
		"duplicate case profile": func(catalog *EvalCatalog) {
			catalog.Cases = append(catalog.Cases, catalog.Cases[0])
		},
		"unknown seed category": func(catalog *EvalCatalog) {
			catalog.Seeds[0].Category = "unknown"
		},
		"seed contract": func(catalog *EvalCatalog) {
			catalog.Seeds[0].RequiredAssertions = nil
		},
		"seed example identity": func(catalog *EvalCatalog) {
			catalog.Seeds[0].Examples[0].ID = ""
		},
		"duplicate seed example": func(catalog *EvalCatalog) {
			catalog.Seeds[0].Examples = append(
				catalog.Seeds[0].Examples,
				catalog.Seeds[0].Examples[0],
			)
		},
	}

	for name, mutate := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalog := validTestCatalog()
			mutate(&catalog)

			assert.ErrorIs(t, validateCatalog(catalog), errInvalidCatalog)
		})
	}
}

func TestCatalogLookupHelpers(t *testing.T) {
	t.Parallel()

	catalog := validTestCatalog()

	httpCases := catalogCasesForProfile(catalog, "pr", RunnerHTTPBookRAG)
	assert.Len(t, httpCases, 1)

	definition, ok := caseDefinitionBySource(catalog, "pr", "http-source")
	assert.True(t, ok)
	assert.Equal(t, "http-case", definition.ID)

	_, ok = caseDefinitionBySource(catalog, "release", "http-source")
	assert.False(t, ok)

	_, ok = categoryForID(catalog, "missing")
	assert.False(t, ok)
}

func validTestCatalog() EvalCatalog {
	return EvalCatalog{
		SchemaVersion:   EvalCatalogSchemaVersion,
		MinimumPassRate: 90,
		MigrationState:  MigrationState{Serving: "tree"},
		Categories: []EvalCategory{
			{
				ID: "active", Label: "Active", Status: CategoryStatusActive,
				Profiles: []string{"pr"}, Owner: "U-6", MinimumCases: 1,
			},
			{
				ID: "future", Label: "Future", Status: CategoryStatusSeed,
				Owner: "U-1",
			},
		},
		Cases: []CatalogCase{
			{
				ID: "http-case", Categories: []string{"active"}, Profiles: []string{"pr"},
				Runner: RunnerHTTPBookRAG, SourceName: "http-source",
			},
			{
				ID: "go-case", Categories: []string{"active"}, Profiles: []string{"pr"},
				Runner: RunnerGoTest, Package: "example/internal/check", Test: "TestCheck",
			},
		},
		Seeds: []EvalSeed{
			{
				ID: "future-seed", Category: "future", Owner: "U-1",
				DependsOn: []string{"U-1"}, MinimumScenarios: 1,
				RequiredAssertions: []string{"same Anchor"},
				Examples:           []EvalSeedExample{{ID: "future-example"}},
			},
		},
	}
}
