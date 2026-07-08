// Package diffcover computes test coverage over the lines a change touches
// (the F1-E ratchet: new code under the threshold fails the PR). It joins a
// unified diff against one or more Go cover profiles: a changed line counts
// toward the gate when it is statement-bearing, and counts as covered when any
// profile block containing it has a positive count (union semantics, so unit
// and live profiles both give credit).
package diffcover

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// hunkHeader captures the "+new_start,new_count" half of @@ -a,b +c,d @@.
var hunkHeader = regexp.MustCompile(`^@@ -[0-9]+(?:,[0-9]+)? \+([0-9]+)(?:,([0-9]+))? @@`)

// ParseUnifiedDiff extracts the added-line numbers per file from a unified
// diff (git diff -U0 output; larger contexts also parse). Keys are the
// post-image paths ("b/" side) relative to the repo root; deleted files have
// no post-image and are skipped.
func ParseUnifiedDiff(r io.Reader) (map[string][]int, error) {
	added := map[string][]int{}
	current := ""

	scanner := bufio.NewScanner(r)
	// Diff lines can exceed bufio's 64k default (minified assets, fixtures).
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)

	line := 0 // current post-image line number while walking a hunk

	for scanner.Scan() {
		text := scanner.Text()

		switch {
		case strings.HasPrefix(text, "+++ "):
			current = ""
			if path, ok := strings.CutPrefix(text, "+++ b/"); ok {
				current = strings.TrimSpace(path)
			}
		case strings.HasPrefix(text, "@@"):
			match := hunkHeader.FindStringSubmatch(text)
			if match == nil {
				return nil, fmt.Errorf("diffcover: malformed hunk header: %q", text)
			}

			start, err := strconv.Atoi(match[1])
			if err != nil {
				return nil, fmt.Errorf("diffcover: hunk start in %q: %w", text, err)
			}

			line = start
		case current == "":
			// Between a deletion-only file header and the next +++ marker.
		case strings.HasPrefix(text, "+"):
			added[current] = append(added[current], line)
			line++
		case strings.HasPrefix(text, " "):
			line++
			// "-" lines belong to the pre-image only: no post-image advance.
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("diffcover: reading diff: %w", err)
	}

	return added, nil
}
