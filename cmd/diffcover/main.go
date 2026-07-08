// Command diffcover is the F1-E coverage ratchet: it joins a unified diff with
// Go cover profiles and fails (exit 1) when the changed statements' coverage
// is below the threshold. Run from CI as:
//
//	git diff -U0 --no-color origin/main...HEAD > diff.patch
//	go run ./cmd/diffcover -diff diff.patch -profile coverage.txt -profile coverage-live.txt
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alfariesh/surau-backend/internal/ci/diffcover"
	"golang.org/x/mod/modfile"
)

const (
	defaultThreshold = 70

	exitPass  = 0
	exitBelow = 1
	exitUsage = 2
)

var errNoModulePath = errors.New("no module path in go.mod")

// reportFilePerm keeps CI report artifacts owner-only.
const reportFilePerm = 0o600

type cliOptions struct {
	diffPath    string
	profiles    profileList
	threshold   float64
	repoRoot    string
	module      string
	scope       string
	markdownOut string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, code := parseFlags(args, stderr)
	if code >= 0 {
		return code
	}

	diff, closeDiff, err := openDiff(opts.diffPath)
	if err != nil {
		fmt.Fprintf(stderr, "diffcover: %v\n", err)

		return exitUsage
	}
	defer closeDiff()

	result, err := diffcover.Analyze(diff, opts.profiles, diffcover.Options{
		ModulePath: opts.module,
		ScopeDirs:  splitScope(opts.scope),
		RepoRoot:   opts.repoRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "diffcover: %v\n", err)

		return exitUsage
	}

	fmt.Fprint(stdout, diffcover.RenderText(result, opts.threshold))

	if err := publishMarkdown(result, &opts, stderr); err != nil {
		return exitUsage
	}

	if !result.Pass(opts.threshold) {
		fmt.Fprintf(stderr, "diffcover: FAIL — new code coverage %.1f%% is below the %.0f%% ratchet\n",
			result.DiffPercent(), opts.threshold)

		return exitBelow
	}

	return exitPass
}

// parseFlags returns the parsed options; a non-negative code means "exit now".
func parseFlags(args []string, stderr io.Writer) (opts cliOptions, exitCode int) {
	flags := flag.NewFlagSet("diffcover", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.diffPath, "diff", "-", "unified diff file (default stdin)")
	flags.Var(&opts.profiles, "profile", "cover profile file (repeatable)")
	flags.Float64Var(&opts.threshold, "threshold", defaultThreshold, "minimum diff coverage percentage")
	flags.StringVar(&opts.repoRoot, "repo-root", ".", "repository root (for go.mod and changed files)")
	flags.StringVar(&opts.module, "module", "", "module path (default: read from go.mod)")
	flags.StringVar(&opts.scope, "scope", "internal/,pkg/", "comma-separated path prefixes the gate measures")
	flags.StringVar(&opts.markdownOut, "markdown-out", "", "write the markdown report to this file")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, exitPass
		}

		return opts, exitUsage
	}

	if len(opts.profiles) == 0 {
		fmt.Fprintln(stderr, "diffcover: at least one -profile is required")

		return opts, exitUsage
	}

	if opts.module == "" {
		resolved, err := modulePathFromGoMod(filepath.Join(opts.repoRoot, "go.mod"))
		if err != nil {
			fmt.Fprintf(stderr, "diffcover: %v\n", err)

			return opts, exitUsage
		}

		opts.module = resolved
	}

	return opts, -1
}

func openDiff(path string) (io.Reader, func(), error) {
	if path == "-" {
		return os.Stdin, func() {}, nil
	}

	file, err := os.Open(path) // #nosec G304 -- CI tool intentionally reads the operator-supplied diff file.
	if err != nil {
		return nil, nil, err
	}

	return file, func() { file.Close() }, nil
}

// publishMarkdown writes the markdown report to -markdown-out and appends it
// to the GitHub step summary when running in Actions.
func publishMarkdown(result diffcover.Result, opts *cliOptions, stderr io.Writer) error {
	markdown := diffcover.RenderMarkdown(result, opts.threshold)

	if opts.markdownOut != "" {
		if err := os.WriteFile(opts.markdownOut, []byte(markdown), reportFilePerm); err != nil {
			fmt.Fprintf(stderr, "diffcover: write markdown: %v\n", err)

			return err
		}
	}

	if summaryPath := os.Getenv("GITHUB_STEP_SUMMARY"); summaryPath != "" {
		if err := appendFile(summaryPath, markdown); err != nil {
			// Summary is presentation only; never fail the gate over it.
			fmt.Fprintf(stderr, "diffcover: step summary: %v\n", err)
		}
	}

	return nil
}

type profileList []string

func (p *profileList) String() string { return strings.Join(*p, ",") }

func (p *profileList) Set(value string) error {
	*p = append(*p, value)

	return nil
}

func modulePathFromGoMod(path string) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- reads the repo's own go.mod under -repo-root.
	if err != nil {
		return "", err
	}

	modulePath := modfile.ModulePath(data)
	if modulePath == "" {
		return "", fmt.Errorf("%w: %s", errNoModulePath, path)
	}

	return modulePath, nil
}

func splitScope(scope string) []string {
	parts := strings.Split(scope, ",")
	dirs := make([]string, 0, len(parts))

	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			dirs = append(dirs, trimmed)
		}
	}

	return dirs
}

func appendFile(path, content string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, reportFilePerm) // #nosec G703 G304 -- path comes from GITHUB_STEP_SUMMARY, set by the CI runner itself.
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(content)

	return err
}
