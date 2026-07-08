package diffcover

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var errModulePathRequired = errors.New("diffcover: module path is required")

// fullCoverage is the percentage reported when nothing measurable changed.
const fullCoverage = 100.0

// Options configures one gate run.
type Options struct {
	// ModulePath strips profile file names down to repo-relative paths
	// (e.g. github.com/alfariesh/surau-backend).
	ModulePath string
	// ScopeDirs are the repo-relative prefixes the gate measures — aligned
	// with what `make test` profiles today.
	ScopeDirs []string
	// RepoRoot is where changed files are read from (working tree of the PR
	// HEAD checkout).
	RepoRoot string
}

// FileResult is the per-file verdict.
type FileResult struct {
	Path           string
	InProfiles     bool
	ChangedStmts   int
	CoveredStmts   int
	UncoveredLines []int
}

// Result aggregates the gate run.
type Result struct {
	Files []FileResult

	// Diff-scoped counters (the gate input).
	ChangedStmts int
	CoveredStmts int

	// Repo-wide counters over the union of all profiles (the headline
	// "total coverage" reported per PR).
	TotalStmts   int
	TotalCovered int
}

// DiffPercent is the coverage of changed statement-bearing lines. A diff that
// adds no measurable statements passes by definition.
func (r Result) DiffPercent() float64 {
	if r.ChangedStmts == 0 {
		return fullCoverage
	}

	return fullCoverage * float64(r.CoveredStmts) / float64(r.ChangedStmts)
}

// TotalPercent is the repo-wide statement coverage across the profile union.
func (r Result) TotalPercent() float64 {
	if r.TotalStmts == 0 {
		return 0
	}

	return fullCoverage * float64(r.TotalCovered) / float64(r.TotalStmts)
}

// Pass applies the ratchet threshold to the diff coverage.
func (r Result) Pass(threshold float64) bool {
	return r.DiffPercent() >= threshold
}

// Analyze joins the diff with the profiles under opts and scores every
// changed, in-scope, non-generated .go file. Files no profile knows about
// (packages never linked into a test binary — including build-tag-gated
// sources) are parsed for statement lines, all of which count as uncovered:
// new code without any test is exactly what the ratchet exists to catch.
func Analyze(diff io.Reader, profilePaths []string, opts Options) (Result, error) {
	if opts.ModulePath == "" {
		return Result{}, errModulePathRequired
	}

	added, err := ParseUnifiedDiff(diff)
	if err != nil {
		return Result{}, err
	}

	idx, err := loadProfiles(profilePaths, opts.ModulePath)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		TotalStmts:   idx.totalStmts,
		TotalCovered: idx.coveredStmts,
	}

	paths := make([]string, 0, len(added))
	for path := range added {
		paths = append(paths, path)
	}

	sort.Strings(paths)

	for _, path := range paths {
		fileResult, measurable, err := scoreChangedFile(path, added[path], idx, opts)
		if err != nil {
			return Result{}, err
		}

		if !measurable {
			continue
		}

		result.Files = append(result.Files, fileResult)
		result.ChangedStmts += fileResult.ChangedStmts
		result.CoveredStmts += fileResult.CoveredStmts
	}

	return result, nil
}

// scoreChangedFile applies the scope/generated exemptions and scores one
// changed file; measurable is false when the file contributes nothing to the
// gate (out of scope, generated, or no statement-bearing changes).
func scoreChangedFile(path string, addedLines []int, idx *profileIndex, opts Options) (FileResult, bool, error) {
	if !inScope(path, opts.ScopeDirs) {
		return FileResult{}, false, nil
	}

	src, err := os.ReadFile(filepath.Join(opts.RepoRoot, filepath.FromSlash(path)))
	if err != nil {
		return FileResult{}, false, fmt.Errorf("diffcover: read changed file: %w", err)
	}

	if isGeneratedSource(src) {
		return FileResult{}, false, nil
	}

	fileResult, err := scoreFile(path, src, addedLines, idx)
	if err != nil {
		return FileResult{}, false, err
	}

	return fileResult, fileResult.ChangedStmts > 0, nil
}

func scoreFile(path string, src []byte, addedLines []int, idx *profileIndex) (FileResult, error) {
	fileResult := FileResult{Path: path}

	coverage := idx.files[path]
	if coverage != nil {
		fileResult.InProfiles = true

		for _, line := range addedLines {
			covered, isStmt := coverage.stmtLines[line]
			if !isStmt {
				continue
			}

			fileResult.ChangedStmts++
			if covered {
				fileResult.CoveredStmts++
			} else {
				fileResult.UncoveredLines = append(fileResult.UncoveredLines, line)
			}
		}

		return fileResult, nil
	}

	stmts, err := stmtStartLines(path, src)
	if err != nil {
		return FileResult{}, fmt.Errorf("diffcover: parse %s: %w", path, err)
	}

	for _, line := range addedLines {
		if !stmts[line] {
			continue
		}

		fileResult.ChangedStmts++
		fileResult.UncoveredLines = append(fileResult.UncoveredLines, line)
	}

	return fileResult, nil
}

func inScope(path string, scopeDirs []string) bool {
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return false
	}

	for _, dir := range scopeDirs {
		if strings.HasPrefix(path, dir) {
			return true
		}
	}

	return false
}
