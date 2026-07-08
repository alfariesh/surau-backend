package diffcover

import (
	"fmt"
	"strings"

	"golang.org/x/tools/cover"
)

// lineCoverage is the union view of one file across every profile: for each
// statement-bearing line, whether any block containing it has Count > 0.
type lineCoverage struct {
	stmtLines map[int]bool // line -> covered
}

// profileIndex maps repo-relative file paths to their union line coverage,
// and carries the repo-wide totals used for the headline number.
type profileIndex struct {
	files map[string]*lineCoverage

	totalStmts   int
	coveredStmts int
}

// loadProfiles parses and unions cover profiles. Profile file names are
// module-qualified import paths (module/dir/file.go); modulePath strips them
// back to repo-relative. Profiles must share one cover mode semantically;
// union-of-counts>0 is sound for set and atomic modes alike.
func loadProfiles(paths []string, modulePath string) (*profileIndex, error) {
	idx := &profileIndex{files: map[string]*lineCoverage{}}
	prefix := modulePath + "/"

	// The same block can appear in several profiles (unit + live); totals must
	// count each block once, with covered = max over profiles.
	type blockKey struct {
		file                                     string
		startLine, startCol, endLine, endCol int
	}

	type blockAgg struct {
		numStmt int
		covered bool
	}

	blocks := map[blockKey]*blockAgg{}

	for _, path := range paths {
		profiles, err := cover.ParseProfiles(path)
		if err != nil {
			return nil, fmt.Errorf("diffcover: parse profile %s: %w", path, err)
		}

		for _, profile := range profiles {
			rel, ok := strings.CutPrefix(profile.FileName, prefix)
			if !ok {
				// Foreign module entries (shouldn't happen) stay out of scope.
				continue
			}

			file := idx.files[rel]
			if file == nil {
				file = &lineCoverage{stmtLines: map[int]bool{}}
				idx.files[rel] = file
			}

			for _, block := range profile.Blocks {
				key := blockKey{rel, block.StartLine, block.StartCol, block.EndLine, block.EndCol}

				agg := blocks[key]
				if agg == nil {
					agg = &blockAgg{numStmt: block.NumStmt}
					blocks[key] = agg
				}

				if block.Count > 0 {
					agg.covered = true
				}

				for line := block.StartLine; line <= block.EndLine; line++ {
					file.stmtLines[line] = file.stmtLines[line] || block.Count > 0
				}
			}
		}
	}

	for _, agg := range blocks {
		idx.totalStmts += agg.numStmt
		if agg.covered {
			idx.coveredStmts += agg.numStmt
		}
	}

	return idx, nil
}
