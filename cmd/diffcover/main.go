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

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	var profiles profileList

	flags := flag.NewFlagSet("diffcover", flag.ContinueOnError)
	flags.SetOutput(stderr)
	diffPath := flags.String("diff", "-", "unified diff file (default stdin)")
	flags.Var(&profiles, "profile", "cover profile file (repeatable)")
	threshold := flags.Float64("threshold", 70, "minimum diff coverage percentage")
	repoRoot := flags.String("repo-root", ".", "repository root (for go.mod and changed files)")
	module := flags.String("module", "", "module path (default: read from go.mod)")
	scope := flags.String("scope", "internal/,pkg/", "comma-separated path prefixes the gate measures")
	markdownOut := flags.String("markdown-out", "", "write the markdown report to this file")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	if len(profiles) == 0 {
		fmt.Fprintln(stderr, "diffcover: at least one -profile is required")

		return 2
	}

	modulePath := *module
	if modulePath == "" {
		resolved, err := modulePathFromGoMod(filepath.Join(*repoRoot, "go.mod"))
		if err != nil {
			fmt.Fprintf(stderr, "diffcover: %v\n", err)

			return 2
		}

		modulePath = resolved
	}

	diff := io.Reader(os.Stdin)

	if *diffPath != "-" {
		file, err := os.Open(*diffPath)
		if err != nil {
			fmt.Fprintf(stderr, "diffcover: %v\n", err)

			return 2
		}
		defer file.Close()

		diff = file
	}

	result, err := diffcover.Analyze(diff, profiles, diffcover.Options{
		ModulePath: modulePath,
		ScopeDirs:  splitScope(*scope),
		RepoRoot:   *repoRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "diffcover: %v\n", err)

		return 2
	}

	fmt.Fprint(stdout, diffcover.RenderText(result, *threshold))

	markdown := diffcover.RenderMarkdown(result, *threshold)

	if *markdownOut != "" {
		if err := os.WriteFile(*markdownOut, []byte(markdown), 0o644); err != nil {
			fmt.Fprintf(stderr, "diffcover: write markdown: %v\n", err)

			return 2
		}
	}

	if summaryPath := os.Getenv("GITHUB_STEP_SUMMARY"); summaryPath != "" {
		if err := appendFile(summaryPath, markdown); err != nil {
			fmt.Fprintf(stderr, "diffcover: step summary: %v\n", err)
		}
	}

	if !result.Pass(*threshold) {
		fmt.Fprintf(stderr, "diffcover: FAIL — new code coverage %.1f%% is below the %.0f%% ratchet\n",
			result.DiffPercent(), *threshold)

		return 1
	}

	return 0
}

type profileList []string

func (p *profileList) String() string { return strings.Join(*p, ",") }

func (p *profileList) Set(value string) error {
	*p = append(*p, value)

	return nil
}

func modulePathFromGoMod(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	modulePath := modfile.ModulePath(data)
	if modulePath == "" {
		return "", fmt.Errorf("no module path in %s", path)
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
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.WriteString(file, content)

	return err
}
