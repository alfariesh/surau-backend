//nolint:gocritic,wsl_v5 // Report snapshots are immutable values and parser stages stay visibly ordered.
package rageval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	goTestScannerInitialSize = 64 * 1024
	goTestScannerMaxSize     = 4 * 1024 * 1024
	millisecondsPerSecond    = 1000
)

type goTestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

// LoadReport reads one previously generated report.
func LoadReport(path string) (EvalReport, error) {
	file, err := os.Open(path) // #nosec G304 -- operator-selected report path is intentional.
	if err != nil {
		return EvalReport{}, fmt.Errorf("open report: %w", err)
	}
	defer file.Close()

	var report EvalReport
	if err = json.NewDecoder(file).Decode(&report); err != nil {
		return EvalReport{}, fmt.Errorf("decode report: %w", err)
	}

	return report, nil
}

// MergeGoTestJSON maps named deterministic tests into the same logical U-6
// scenario report as HTTP results.
//
//nolint:cyclop,funlen,gocognit,gocyclo // Named-test terminal events and deterministic replacement order are handled together.
func MergeGoTestJSON(
	report EvalReport,
	catalog EvalCatalog,
	profile string,
	readers ...io.Reader,
) (EvalReport, error) {
	definitions := catalogCasesForProfile(catalog, profile, RunnerGoTest)
	byKey := make(map[string]CatalogCase, len(definitions))
	for _, definition := range definitions {
		byKey[goTestKey(definition.Package, definition.Test)] = definition
	}

	statuses := make(map[string]CaseResult, len(definitions))
	for _, reader := range readers {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, goTestScannerInitialSize), goTestScannerMaxSize)
		for scanner.Scan() {
			var event goTestEvent
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				continue
			}
			if event.Test == "" || (event.Action != "pass" && event.Action != "fail") {
				continue
			}

			definition, ok := matchGoTestDefinition(byKey, event.Package, event.Test)
			if !ok {
				continue
			}
			result := CaseResult{
				Name:       definition.ID,
				Categories: append([]string(nil), definition.Categories...),
				Runner:     RunnerGoTest,
				Question:   definition.Test,
				Passed:     event.Action == "pass",
				DurationMS: int64(event.Elapsed * millisecondsPerSecond),
				Attempt:    1,
			}
			if !result.Passed {
				result.Errors = []string{"named deterministic test failed"}
			}
			statuses[definition.ID] = result
		}
		if err := scanner.Err(); err != nil {
			return EvalReport{}, fmt.Errorf("scan go test JSON: %w", err)
		}
	}

	resultByName := make(map[string]CaseResult, len(report.Results)+len(statuses))
	order := make([]string, 0, len(report.Results)+len(statuses))
	for _, result := range report.Results {
		if _, exists := resultByName[result.Name]; !exists {
			order = append(order, result.Name)
		}
		resultByName[result.Name] = result
	}
	for _, definition := range definitions {
		result, ok := statuses[definition.ID]
		if !ok {
			continue
		}
		if _, exists := resultByName[result.Name]; !exists {
			order = append(order, result.Name)
		}
		resultByName[result.Name] = result
	}

	report.Results = report.Results[:0]
	for _, name := range order {
		report.Results = append(report.Results, resultByName[name])
	}
	report.SchemaVersion = EvalReportSchemaVersion
	report.Profile = profile
	if report.StartedAt.IsZero() {
		report.StartedAt = time.Now().UTC()
	}
	report.FinishedAt = time.Now().UTC()
	report.DurationMS = report.FinishedAt.Sub(report.StartedAt).Milliseconds()
	refreshReport(&report, &catalog)
	report.HumanSamples = selectHumanSamples(report.Results, "")

	return report, nil
}

func matchGoTestDefinition(
	definitions map[string]CatalogCase,
	pkg string,
	test string,
) (CatalogCase, bool) {
	if definition, ok := definitions[goTestKey(pkg, test)]; ok {
		return definition, true
	}
	for _, definition := range definitions {
		if definition.Test == test && strings.HasSuffix(pkg, strings.TrimPrefix(definition.Package, ".")) {
			return definition, true
		}
	}

	return CatalogCase{}, false
}

func goTestKey(pkg, test string) string {
	return strings.TrimSpace(pkg) + "\x00" + strings.TrimSpace(test)
}

//nolint:cyclop,funlen,gocognit,gocyclo // Markdown sections intentionally mirror the machine report without hidden templates.
func writeMarkdown(w io.Writer, report EvalReport) error {
	if _, err := fmt.Fprintf(
		w,
		"## U-6 evaluation — `%s`\n\n"+
			"Deterministic: **%d/%d passed (%.2f%%)** · failed: %d · warnings: %d\n\n",
		report.Profile,
		report.Passed,
		report.Total,
		report.PassRate,
		report.Failed,
		report.Warnings,
	); err != nil {
		return err
	}

	if _, err := fmt.Fprint(w, "| Category | Policy | Passed | Failed | Pass-rate |\n|---|---:|---:|---:|---:|\n"); err != nil {
		return err
	}
	for _, category := range report.Categories {
		policy := "≥ threshold"
		if category.Blocking {
			policy = "100% blocking"
		}
		if _, err := fmt.Fprintf(
			w,
			"| %s | %s | %d | %d | %.2f%% |\n",
			category.Label,
			policy,
			category.Passed,
			category.Failed,
			category.PassRate,
		); err != nil {
			return err
		}
	}

	if len(report.Judges) > 0 {
		if _, err := fmt.Fprint(
			w,
			"\n### Advisory LLM judge\n\n| Rubric | Passed | Failed | Skipped | Pass-rate |\n|---|---:|---:|---:|---:|\n",
		); err != nil {
			return err
		}
		for _, judge := range report.Judges {
			if _, err := fmt.Fprintf(
				w,
				"| %s | %d | %d | %d | %.2f%% |\n",
				judge.RubricVersion,
				judge.Passed,
				judge.Failed,
				judge.Skipped,
				judge.PassRate,
			); err != nil {
				return err
			}
		}
	}

	if len(report.HumanSamples) > 0 {
		if _, err := fmt.Fprint(w, "\n### Human sampling queue\n\n"); err != nil {
			return err
		}
		for _, sample := range report.HumanSamples {
			if _, err := fmt.Fprintf(
				w,
				"- `%s` — %s (%s)\n",
				sample.CaseID,
				sample.Judge.RubricVersion,
				sample.ReviewState,
			); err != nil {
				return err
			}
		}
	}

	return nil
}
