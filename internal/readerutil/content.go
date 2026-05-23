package readerutil

import (
	"fmt"
	"html"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	_tagRE       = regexp.MustCompile(`<[^>]+>`)
	_spaceRE     = regexp.MustCompile(`[ \t\x{00a0}]+`)
	_lineBreakRE = regexp.MustCompile(`\r\n?`)
)

// ResolveBookDBPath returns the raw SQLite path for a book id.
func ResolveBookDBPath(sourceDir string, bookID int) string {
	bucket := bookID % 1000
	bucketName := fmt.Sprintf("%03d", bucket)

	return filepath.Join(sourceDir, "book", bucketName, fmt.Sprintf("%d.db", bookID))
}

// NormalizeContent removes known raw markers and returns safe-ish HTML plus plain text.
func NormalizeContent(content string) (string, string) {
	normalized := strings.TrimPrefix(content, "\ufeff")
	normalized = strings.TrimPrefix(normalized, "舄")
	normalized = _lineBreakRE.ReplaceAllString(normalized, "\n")
	normalized = strings.TrimSpace(normalized)

	return normalized, PlainText(normalized)
}

// PlainText strips simple HTML markup and normalizes whitespace for search/preview.
func PlainText(content string) string {
	text := _tagRE.ReplaceAllString(content, " ")
	text = html.UnescapeString(text)
	text = _lineBreakRE.ReplaceAllString(text, "\n")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(_spaceRE.ReplaceAllString(line, " "))
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// SourceHeading is the minimum raw title shape needed to build heading ranges.
type SourceHeading struct {
	ID        int
	ParentID  int
	PageID    int
	Content   string
	IsDeleted bool
}

// HeadingRange describes an inclusive page range plus optional HTML anchors.
type HeadingRange struct {
	BookID      int
	HeadingID   int
	StartPageID int
	EndPageID   int
	StartAnchor string
	EndAnchor   string
}

// DecoratedHeading includes depth and ordinal derived from the title tree.
type DecoratedHeading struct {
	SourceHeading
	Depth   int
	Ordinal int
}

// DecorateHeadings sorts headings by source id and derives tree depth.
func DecorateHeadings(headings []SourceHeading) []DecoratedHeading {
	sorted := make([]SourceHeading, 0, len(headings))
	sorted = append(sorted, headings...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})

	byID := make(map[int]SourceHeading, len(sorted))
	for _, heading := range sorted {
		byID[heading.ID] = heading
	}

	depthMemo := make(map[int]int, len(sorted))
	var depthOf func(id int, seen map[int]struct{}) int
	depthOf = func(id int, seen map[int]struct{}) int {
		if depth, ok := depthMemo[id]; ok {
			return depth
		}

		heading, ok := byID[id]
		if !ok || heading.ParentID == 0 {
			depthMemo[id] = 0
			return 0
		}

		if _, cycle := seen[id]; cycle {
			depthMemo[id] = 0
			return 0
		}

		seen[id] = struct{}{}
		depth := depthOf(heading.ParentID, seen) + 1
		depthMemo[id] = depth

		return depth
	}

	decorated := make([]DecoratedHeading, 0, len(sorted))
	for ordinal, heading := range sorted {
		decorated = append(decorated, DecoratedHeading{
			SourceHeading: heading,
			Depth:         depthOf(heading.ID, map[int]struct{}{}),
			Ordinal:       ordinal,
		})
	}

	return decorated
}

// BuildHeadingRanges creates section ranges that include descendants until the next heading
// at the same or a shallower depth.
func BuildHeadingRanges(bookID, lastPageID int, headings []DecoratedHeading) []HeadingRange {
	ranges := make([]HeadingRange, 0, len(headings))

	for i, heading := range headings {
		endPageID := lastPageID
		endAnchor := ""

		for j := i + 1; j < len(headings); j++ {
			if headings[j].Depth <= heading.Depth {
				endPageID = headings[j].PageID
				endAnchor = anchorFor(headings[j].ID)
				break
			}
		}

		if endPageID < heading.PageID {
			endPageID = heading.PageID
		}

		ranges = append(ranges, HeadingRange{
			BookID:      bookID,
			HeadingID:   heading.ID,
			StartPageID: heading.PageID,
			EndPageID:   endPageID,
			StartAnchor: anchorFor(heading.ID),
			EndAnchor:   endAnchor,
		})
	}

	return ranges
}

// SliceAnchoredHTML extracts one section from concatenated page HTML.
func SliceAnchoredHTML(content, startAnchor, endAnchor string) string {
	start := 0
	if startAnchor != "" {
		if idx := findAnchor(content, startAnchor); idx >= 0 {
			start = idx
		}
	}

	end := len(content)
	if endAnchor != "" {
		if idx := findAnchor(content[start:], endAnchor); idx >= 0 {
			end = start + idx
		}
	}

	if start > end {
		return strings.TrimSpace(content)
	}

	return strings.TrimSpace(content[start:end])
}

func anchorFor(id int) string {
	return "toc-" + strconv.Itoa(id)
}

func findAnchor(content, anchor string) int {
	patterns := []string{
		`id=` + anchor,
		`id="` + anchor + `"`,
		`id='` + anchor + `'`,
	}

	for _, pattern := range patterns {
		if idx := strings.Index(content, pattern); idx >= 0 {
			if tagStart := strings.LastIndex(content[:idx], "<"); tagStart >= 0 {
				return tagStart
			}

			return idx
		}
	}

	return -1
}
