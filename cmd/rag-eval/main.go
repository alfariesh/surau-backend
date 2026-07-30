//nolint:wsl_v5 // CLI parsing and exit-code handling intentionally stay linear and explicit.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/rageval"
)

const invalidConfigurationExitCode = 2

var errEmptyPath = errors.New("path must not be empty")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	command := "run"
	legacy := true
	if len(args) > 0 {
		switch args[0] {
		case "run", "report", "gate", "compare":
			command = args[0]
			args = args[1:]
			legacy = false
		}
	}

	switch command {
	case "run":
		return runEvaluation(ctx, args, stdout, stderr, legacy)
	case "report":
		return runReport(args, stdout, stderr)
	case "gate":
		return runGate(args, stdout, stderr)
	case "compare":
		return runCompare(args, stdout, stderr)
	default:
		return invalidConfigurationExitCode
	}
}

//nolint:cyclop,funlen,gocyclo // Legacy compatibility and explicit profile defaults share one CLI entrypoint.
func runEvaluation(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	legacy bool,
) int {
	serviceToken, err := serviceTokenFromEnvironment()
	if err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return invalidConfigurationExitCode
	}
	judgeToken, err := judgeTokenFromEnvironment()
	if err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return invalidConfigurationExitCode
	}

	opts := rageval.Options{
		ServiceToken: serviceToken,
		JudgeToken:   judgeToken,
		CommitSHA:    strings.TrimSpace(os.Getenv("RAG_EVAL_COMMIT_SHA")),
	}
	flags := flag.NewFlagSet("rag-eval run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.BaseURL, "base-url", envOrDefault("RAG_EVAL_BASE_URL", rageval.DefaultBaseURL), "BookRAG API base URL")
	flags.StringVar(&opts.CasesPath, "cases", rageval.DefaultCasesPath, "JSONL evaluation cases file")
	flags.StringVar(&opts.CatalogPath, "catalog", "", "versioned evaluation catalog")
	flags.StringVar(&opts.Profile, "profile", "", "execution profile: pr, scheduled, or release")
	flags.StringVar(&opts.Output, "output", "table", "output format: table, json, or markdown")
	flags.DurationVar(&opts.Timeout, "timeout", rageval.DefaultTimeout, "HTTP timeout per case")
	flags.BoolVar(&opts.FailFast, "fail-fast", false, "stop after first failed case")
	flags.IntVar(&opts.Limit, "limit", 0, "limit number of cases")
	defaultRetries := 0
	if legacy {
		defaultRetries = 1
	}
	flags.IntVar(&opts.Retries, "retries", defaultRetries, "retry failed cases")
	flags.BoolVar(&opts.StrictAnswer, "strict-answer", false, "treat answer_must_contain misses as failures")
	flags.StringVar(&opts.ExpectedCitationMode, "expected-citation-mode", "", "require this citation_mode in every response trace")
	flags.BoolVar(&opts.ForbidLegacyFallback, "forbid-legacy-fallback", false, "fail when any response trace reports legacy fallback")
	flags.StringVar(&opts.JudgeURL, "judge-url", "", "same-origin service judge endpoint")
	flags.StringVar(&opts.SampleMonth, "sample-month", "", "human sampling month in YYYY-MM")
	verbose := flags.Bool("verbose", false, "print per-case progress to stderr")
	if err = flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return invalidConfigurationExitCode
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "rag-eval: unexpected arguments: %s\n", strings.Join(flags.Args(), " "))

		return invalidConfigurationExitCode
	}
	if *verbose {
		opts.ProgressWriter = stderr
	}

	start := time.Now()
	summary, err := rageval.Run(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}
	if err = rageval.WriteSummary(stdout, summary, opts.Output); err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}

	if summary.Failed > 0 {
		fmt.Fprintf(
			stderr,
			"rag-eval: %d/%d failed in %s\n",
			summary.Failed,
			summary.Total,
			time.Since(start).Round(time.Millisecond),
		)

		return 1
	}

	return 0
}

//nolint:cyclop,funlen,gocyclo // Report assembly keeps every artifact/open/close failure explicit.
func runReport(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("rag-eval report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var catalogPath, profile, httpReportPath, output, commitSHA, sampleMonth string
	var goTestPaths pathList
	flags.StringVar(&catalogPath, "catalog", "", "versioned evaluation catalog")
	flags.StringVar(&profile, "profile", "", "execution profile")
	flags.StringVar(&httpReportPath, "http-report", "", "optional HTTP evaluation report")
	flags.Var(&goTestPaths, "go-test-json", "go test -json output; repeat for multiple files")
	flags.StringVar(&output, "output", "json", "output format: table, json, or markdown")
	flags.StringVar(&commitSHA, "commit-sha", strings.TrimSpace(os.Getenv("RAG_EVAL_COMMIT_SHA")), "evaluated commit SHA")
	flags.StringVar(&sampleMonth, "sample-month", "", "human sampling month in YYYY-MM")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return invalidConfigurationExitCode
	}
	if strings.TrimSpace(catalogPath) == "" || strings.TrimSpace(profile) == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "rag-eval: report requires -catalog and -profile with no positional arguments")

		return invalidConfigurationExitCode
	}

	catalog, err := rageval.LoadCatalog(catalogPath)
	if err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}
	report := rageval.EvalReport{
		SchemaVersion: rageval.EvalReportSchemaVersion,
		Profile:       profile,
		CommitSHA:     commitSHA,
	}
	if strings.TrimSpace(httpReportPath) != "" {
		report, err = rageval.LoadReport(httpReportPath)
		if err != nil {
			fmt.Fprintf(stderr, "rag-eval: %v\n", err)

			return 1
		}
		report.CommitSHA = commitSHA
	}

	readers := make([]io.Reader, 0, len(goTestPaths))
	files := make([]*os.File, 0, len(goTestPaths))
	for _, path := range goTestPaths {
		file, openErr := os.Open(path) // #nosec G304 -- operator-selected test artifact is intentional.
		if openErr != nil {
			closeFiles(files)
			fmt.Fprintf(stderr, "rag-eval: open go test report: %v\n", openErr)

			return 1
		}
		files = append(files, file)
		readers = append(readers, file)
	}
	report, err = rageval.MergeGoTestJSON(report, catalog, profile, readers...)
	closeFiles(files)
	if err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}
	report.HumanSamples = rageval.SelectHumanSamples(report.Results, sampleMonth)
	if err = rageval.WriteSummary(stdout, report, output); err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}

	return 0
}

func runGate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("rag-eval gate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var catalogPath, reportPath string
	flags.StringVar(&catalogPath, "catalog", "", "versioned evaluation catalog")
	flags.StringVar(&reportPath, "report", "", "evaluation report")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return invalidConfigurationExitCode
	}
	if catalogPath == "" || reportPath == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "rag-eval: gate requires -catalog and -report with no positional arguments")

		return invalidConfigurationExitCode
	}

	catalog, err := rageval.LoadCatalog(catalogPath)
	if err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}
	report, err := rageval.LoadReport(reportPath)
	if err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}
	decision := rageval.GateReport(catalog, report)
	if err = writeJSON(stdout, decision); err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}
	if !decision.Passed {
		return 1
	}

	return 0
}

func runCompare(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("rag-eval compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var baselinePath, candidatePath string
	flags.StringVar(&baselinePath, "baseline", "", "tree baseline report")
	flags.StringVar(&candidatePath, "candidate", "", "candidate report")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return invalidConfigurationExitCode
	}
	if baselinePath == "" || candidatePath == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "rag-eval: compare requires -baseline and -candidate with no positional arguments")

		return invalidConfigurationExitCode
	}

	baseline, err := rageval.LoadReport(baselinePath)
	if err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}
	candidate, err := rageval.LoadReport(candidatePath)
	if err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}
	evidence, decision := rageval.CompareReports(baseline, candidate)
	payload := struct {
		Evidence rageval.ParityEvidence `json:"evidence"`
		Decision rageval.GateDecision   `json:"decision"`
	}{Evidence: evidence, Decision: decision}
	if err = writeJSON(stdout, payload); err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)

		return 1
	}
	if !decision.Passed {
		return 1
	}

	return 0
}

type pathList []string

func (paths *pathList) String() string {
	return strings.Join(*paths, ",")
}

func (paths *pathList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errEmptyPath
	}
	*paths = append(*paths, value)

	return nil
}

func closeFiles(files []*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	return encoder.Encode(value)
}

func serviceTokenFromEnvironment() (string, error) {
	return tokenFromEnvironment("RAG_EVAL_SERVICE_TOKEN")
}

func judgeTokenFromEnvironment() (string, error) {
	return tokenFromEnvironment("RAG_EVAL_JUDGE_TOKEN")
}

func tokenFromEnvironment(key string) (string, error) {
	if path := os.Getenv(key + "_FILE"); path != "" {
		contents, err := os.ReadFile(path) // #nosec G304,G703 -- operator-selected secret file is the intended input.
		if err != nil {
			return "", fmt.Errorf("read %s_FILE: %w", key, err)
		}

		return strings.TrimSpace(string(contents)), nil
	}

	return strings.TrimSpace(os.Getenv(key)), nil
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
