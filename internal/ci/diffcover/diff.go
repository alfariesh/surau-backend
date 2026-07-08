// Package diffcover computes test coverage over the lines a change touches
// (the F1-E ratchet: new code under the threshold fails the PR). It joins a
// unified diff against one or more Go cover profiles: a changed line counts
// toward the gate when it is statement-bearing, and counts as covered when any
// profile block containing it has a positive count (union semantics, so unit
// and live profiles both give credit).
package diffcover

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var errMalformedHunk = errors.New("diffcover: malformed hunk header")

// hunkHeader captures the "+new_start,new_count" half of @@ -a,b +c,d @@.
var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

const (
	scannerInitialBuf = 1024
	scannerBufferCap  = 16 * 1024 * 1024
)

// ParseUnifiedDiff extracts the added-line numbers per file from a unified
// diff (git diff -U0 output; larger contexts also parse). Keys are the
// post-image paths ("b/" side) relative to the repo root; deleted files have
// no post-image and are skipped.
func ParseUnifiedDiff(r io.Reader) (map[string][]int, error) {
	added := map[string][]int{}
	current := ""

	scanner := bufio.NewScanner(r)
	// Diff lines can exceed bufio's 64k default (minified assets, fixtures).
	scanner.Buffer(make([]byte, 0, scannerInitialBuf), scannerBufferCap)

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
			start, err := parseHunkStart(text)
			if err != nil {
				return nil, err
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

// parseHunkStart returns the post-image start line of an @@ hunk header.
func parseHunkStart(text string) (int, error) {
	match := hunkHeader.FindStringSubmatch(text)
	if match == nil {
		return 0, fmt.Errorf("%w: %q", errMalformedHunk, text)
	}

	start, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", errMalformedHunk, text, err)
	}

	return start, nil
}
