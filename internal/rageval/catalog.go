//nolint:gocritic,wsl_v5 // Catalog snapshots are intentionally copied and validation reads as a linear schema audit.
package rageval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

const (
	EvalCatalogSchemaVersion = "eval-catalog-v1"
	CategoryStatusActive     = "active"
	CategoryStatusSeed       = "seed"
	RunnerHTTPBookRAG        = "http_bookrag"
	RunnerGoTest             = "go_test"
)

var errInvalidCatalog = errors.New("invalid evaluation catalog")

// EvalCatalog defines category ownership, active checks, future seeds, and the
// frozen BookRAG migration state used by U-6 gate decisions.
type EvalCatalog struct {
	SchemaVersion   string         `json:"schema_version"`
	MinimumPassRate float64        `json:"minimum_pass_rate"`
	Categories      []EvalCategory `json:"categories"`
	Cases           []CatalogCase  `json:"cases"`
	Seeds           []EvalSeed     `json:"seeds,omitempty"`
	MigrationState  MigrationState `json:"migration_state"`
	Hash            string         `json:"-"`
}

// EvalCategory is one pass-rate bucket and its activation contract.
type EvalCategory struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Status       string   `json:"status"`
	Blocking     bool     `json:"blocking"`
	Profiles     []string `json:"profiles,omitempty"`
	Owner        string   `json:"owner"`
	DependsOn    []string `json:"depends_on,omitempty"`
	MinimumCases int      `json:"minimum_cases,omitempty"`
}

// CatalogCase maps one logical W3 scenario to either an HTTP case or a named Go
// test. A profile may select a different runner without changing the scenario
// ID or its category history.
type CatalogCase struct {
	ID         string   `json:"id"`
	Categories []string `json:"categories"`
	Profiles   []string `json:"profiles"`
	Runner     string   `json:"runner"`
	SourceName string   `json:"source_name,omitempty"`
	Package    string   `json:"package,omitempty"`
	Test       string   `json:"test,omitempty"`
}

// EvalSeed reserves a future category without inventing corpus IDs before its
// owner initiative lands.
type EvalSeed struct {
	ID                 string            `json:"id"`
	Category           string            `json:"category"`
	Owner              string            `json:"owner"`
	DependsOn          []string          `json:"depends_on"`
	MinimumScenarios   int               `json:"minimum_scenarios"`
	RequiredAssertions []string          `json:"required_assertions"`
	Examples           []EvalSeedExample `json:"examples,omitempty"`
	Notes              string            `json:"notes,omitempty"`
}

// EvalSeedExample preserves an inactive bilingual question/Anchor pair without
// pretending the future retrieval path already exists.
type EvalSeedExample struct {
	ID             string            `json:"id"`
	Questions      map[string]string `json:"questions,omitempty"`
	ExpectedAnchor string            `json:"expected_anchor,omitempty"`
}

// CategorySummary is the deterministic pass-rate dashboard row.
type CategorySummary struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Status   string  `json:"status"`
	Blocking bool    `json:"blocking"`
	Total    int     `json:"total"`
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	PassRate float64 `json:"pass_rate"`
}

// MigrationState freezes serving on tree until a same-catalog parity artifact
// proves a candidate wins.
type MigrationState struct {
	Serving     string          `json:"serving"`
	TreeRetired bool            `json:"tree_retired"`
	Parity      *ParityEvidence `json:"parity,omitempty"`
}

// ParityEvidence is produced only by CompareReports.
type ParityEvidence struct {
	Passed       bool   `json:"passed"`
	CatalogHash  string `json:"catalog_hash"`
	CommitSHA    string `json:"commit_sha"`
	BaselineSHA  string `json:"baseline_sha"`
	CandidateSHA string `json:"candidate_sha"`
}

// LoadCatalog reads and validates an immutable evaluation catalog.
func LoadCatalog(path string) (EvalCatalog, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-selected catalog path is intentional.
	if err != nil {
		return EvalCatalog{}, fmt.Errorf("open catalog: %w", err)
	}

	var catalog EvalCatalog
	if err = json.Unmarshal(raw, &catalog); err != nil {
		return EvalCatalog{}, fmt.Errorf("decode catalog: %w", err)
	}
	if err := validateCatalog(catalog); err != nil {
		return EvalCatalog{}, err
	}

	sum := sha256.Sum256(raw)
	catalog.Hash = hex.EncodeToString(sum[:])

	return catalog, nil
}

//nolint:cyclop,funlen,gocognit,gocyclo // Each branch protects one independently versioned catalog invariant.
func validateCatalog(catalog EvalCatalog) error {
	if catalog.SchemaVersion != EvalCatalogSchemaVersion {
		return fmt.Errorf("%w: unsupported schema %q", errInvalidCatalog, catalog.SchemaVersion)
	}
	if catalog.MinimumPassRate <= 0 || catalog.MinimumPassRate > 100 {
		return fmt.Errorf("%w: minimum_pass_rate must be in (0,100]", errInvalidCatalog)
	}
	if catalog.MigrationState.Serving != "tree" || catalog.MigrationState.TreeRetired {
		return fmt.Errorf("%w: W3 must preserve tree serving", errInvalidCatalog)
	}

	categoryIDs := make(map[string]struct{}, len(catalog.Categories))
	for _, category := range catalog.Categories {
		if strings.TrimSpace(category.ID) == "" || strings.TrimSpace(category.Owner) == "" {
			return fmt.Errorf("%w: category id and owner are required", errInvalidCatalog)
		}
		if category.Status != CategoryStatusActive && category.Status != CategoryStatusSeed {
			return fmt.Errorf(
				"%w: category %q has invalid status %q",
				errInvalidCatalog,
				category.ID,
				category.Status,
			)
		}
		if _, duplicate := categoryIDs[category.ID]; duplicate {
			return fmt.Errorf("%w: duplicate category %q", errInvalidCatalog, category.ID)
		}
		categoryIDs[category.ID] = struct{}{}
	}

	caseKeys := make(map[string]struct{}, len(catalog.Cases))
	for _, definition := range catalog.Cases {
		if strings.TrimSpace(definition.ID) == "" || len(definition.Categories) == 0 ||
			len(definition.Profiles) == 0 {
			return fmt.Errorf("%w: case id, categories, and profiles are required", errInvalidCatalog)
		}
		for _, category := range definition.Categories {
			if _, ok := categoryIDs[category]; !ok {
				return fmt.Errorf(
					"%w: case %q references unknown category %q",
					errInvalidCatalog,
					definition.ID,
					category,
				)
			}
		}
		switch definition.Runner {
		case RunnerHTTPBookRAG:
			if strings.TrimSpace(definition.SourceName) == "" {
				return fmt.Errorf("%w: HTTP case %q requires source_name", errInvalidCatalog, definition.ID)
			}
		case RunnerGoTest:
			if strings.TrimSpace(definition.Package) == "" || strings.TrimSpace(definition.Test) == "" {
				return fmt.Errorf(
					"%w: Go test case %q requires package and test",
					errInvalidCatalog,
					definition.ID,
				)
			}
		default:
			return fmt.Errorf(
				"%w: case %q has unsupported runner %q",
				errInvalidCatalog,
				definition.ID,
				definition.Runner,
			)
		}

		for _, profile := range definition.Profiles {
			key := definition.ID + "\x00" + profile
			if _, duplicate := caseKeys[key]; duplicate {
				return fmt.Errorf(
					"%w: duplicate case/profile %q/%q",
					errInvalidCatalog,
					definition.ID,
					profile,
				)
			}
			caseKeys[key] = struct{}{}
		}
	}

	for _, seed := range catalog.Seeds {
		if _, ok := categoryIDs[seed.Category]; !ok {
			return fmt.Errorf(
				"%w: seed %q references unknown category %q",
				errInvalidCatalog,
				seed.ID,
				seed.Category,
			)
		}
		if seed.MinimumScenarios <= 0 || len(seed.DependsOn) == 0 || len(seed.RequiredAssertions) == 0 {
			return fmt.Errorf(
				"%w: seed %q requires dependency, quota, and assertions",
				errInvalidCatalog,
				seed.ID,
			)
		}
		exampleIDs := make(map[string]struct{}, len(seed.Examples))
		for _, example := range seed.Examples {
			if strings.TrimSpace(example.ID) == "" {
				return fmt.Errorf("%w: seed %q example id is required", errInvalidCatalog, seed.ID)
			}
			if _, duplicate := exampleIDs[example.ID]; duplicate {
				return fmt.Errorf(
					"%w: seed %q has duplicate example %q",
					errInvalidCatalog,
					seed.ID,
					example.ID,
				)
			}
			exampleIDs[example.ID] = struct{}{}
		}
	}

	return nil
}

func catalogCasesForProfile(catalog EvalCatalog, profile, runner string) []CatalogCase {
	result := make([]CatalogCase, 0)
	for _, definition := range catalog.Cases {
		if definition.Runner == runner && slices.Contains(definition.Profiles, profile) {
			result = append(result, definition)
		}
	}

	return result
}

func caseDefinitionBySource(catalog EvalCatalog, profile, sourceName string) (CatalogCase, bool) {
	for _, definition := range catalogCasesForProfile(catalog, profile, RunnerHTTPBookRAG) {
		if definition.SourceName == sourceName {
			return definition, true
		}
	}

	return CatalogCase{}, false
}

func categoryForID(catalog EvalCatalog, id string) (EvalCategory, bool) {
	for _, category := range catalog.Categories {
		if category.ID == id {
			return category, true
		}
	}

	return EvalCategory{}, false
}

//nolint:gocognit // Category roll-up intentionally keeps every fail/pass counter adjacent.
func refreshReport(report *EvalReport, catalog *EvalCatalog) {
	report.SchemaVersion = EvalReportSchemaVersion
	report.Total = len(report.Results)
	report.Passed = 0
	report.Failed = 0
	report.Warnings = 0

	for i := range report.Results {
		result := &report.Results[i]
		if result.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Warnings += len(result.Warnings)
	}
	report.PassRate = percent(report.Passed, report.Total)

	if catalog == nil {
		report.Categories = summarizeUncatalogued(report.Results)
		report.Judges = summarizeJudges(report.Results)

		return
	}

	report.CatalogHash = catalog.Hash
	report.MigrationState = catalog.MigrationState
	report.Categories = make([]CategorySummary, 0, len(catalog.Categories))
	for _, category := range catalog.Categories {
		if category.Status == CategoryStatusSeed || !profileMatches(category.Profiles, report.Profile) {
			continue
		}

		summary := CategorySummary{
			ID: category.ID, Label: category.Label, Status: category.Status, Blocking: category.Blocking,
		}
		for _, result := range report.Results {
			if !slices.Contains(result.Categories, category.ID) {
				continue
			}
			summary.Total++
			if result.Passed {
				summary.Passed++
			} else {
				summary.Failed++
			}
		}
		summary.PassRate = percent(summary.Passed, summary.Total)
		report.Categories = append(report.Categories, summary)
	}
	report.Judges = summarizeJudges(report.Results)
}

func summarizeUncatalogued(results []CaseResult) []CategorySummary {
	byID := make(map[string]*CategorySummary)
	for _, result := range results {
		for _, categoryID := range result.Categories {
			summary := byID[categoryID]
			if summary == nil {
				summary = &CategorySummary{ID: categoryID, Label: categoryID, Status: CategoryStatusActive}
				byID[categoryID] = summary
			}
			summary.Total++
			if result.Passed {
				summary.Passed++
			} else {
				summary.Failed++
			}
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	summaries := make([]CategorySummary, 0, len(ids))
	for _, id := range ids {
		summary := byID[id]
		summary.PassRate = percent(summary.Passed, summary.Total)
		summaries = append(summaries, *summary)
	}

	return summaries
}

func profileMatches(profiles []string, profile string) bool {
	return len(profiles) == 0 || profile == "" || slices.Contains(profiles, profile)
}

func percent(passed, total int) float64 {
	if total == 0 {
		return 0
	}

	return float64(passed) * 100 / float64(total)
}
