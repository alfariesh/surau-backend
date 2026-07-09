package readerutil

import (
	"regexp"
	"strconv"
	"strings"
)

// SourceFormatMixed marks content structured by StructureMixedContent.
const SourceFormatMixed = "mixed"

var (
	// Any element carrying a toc anchor id, e.g. <span data-type="title" id="toc-11">.
	// SanitizeHTML rewrites attributes quoted, but stay tolerant of raw forms.
	_anchorTagRE = regexp.MustCompile(`<([a-zA-Z][a-zA-Z0-9]*)\b[^>]*\bid=["']?toc-([0-9]+)["']?[^>]*>`)
	// Shamela's footnote separator in HTML form (raw sources write <hr><s0>;
	// <s0> is dropped by SanitizeHTML at import).
	_hrTagRE = regexp.MustCompile(`(?i)<hr\b[^>]*/?>`)
)

// StructureMixedContent structures line-oriented Shamela source content that may
// carry inline markup. It exists for the Citable Unit deriver (phase-1b B-1):
// unlike StructureSourceContent — whose output is a frozen reader contract and
// which collapses any tagged content into ONE html block — this walker keeps
// paragraph granularity on tagged pages:
//
//   - elements with id="toc-N" become heading blocks carrying AnchorID=N
//     (scope switches; the title text's source of truth is book_headings);
//   - an <hr> opens the page's footnote region (Shamela layout: footnotes sit
//     at the bottom of the page after <hr>), as does a plain-text separator
//     line; unlike structurePlainText, a blank line does NOT close the region;
//   - every other line is tag-stripped and classified by the existing
//     plain-text machine (paragraph/heading/quran_quote, footnote starts).
//
// The walk is a pure function of its input: derived unit identity depends on
// it, so any behavior change here shifts content hashes and must be release-
// gated (it triggers a supersede wave absorbed by lineage, per B-D3).
//
//nolint:gocognit,gocyclo,cyclop,funlen,nestif // line walker: split each line at anchor/hr tokens, then the shared plain-text footnote state machine — flat stages
func StructureMixedContent(content string) StructuredContent {
	lines := strings.Split(_lineBreakRE.ReplaceAllString(content, "\n"), "\n")
	blocks := make([]SourceBlock, 0, len(lines))
	footnotes := make([]SourceFootnote, 0)

	var currentFootnote *SourceFootnote

	inFootnotes := false

	flushFootnote := func() {
		if currentFootnote == nil {
			return
		}

		currentFootnote.Text = strings.TrimSpace(currentFootnote.Text)
		currentFootnote.HTML = footnoteHTML(*currentFootnote)
		footnotes = append(footnotes, *currentFootnote)
		currentFootnote = nil
	}

	processTextPiece := func(raw string) {
		line := strings.TrimSpace(_spaceRE.ReplaceAllString(PlainText(raw), " "))
		if line == "" {
			if inFootnotes {
				flushFootnote()
			}

			return
		}

		if isFootnoteSeparator(line) {
			flushFootnote()

			inFootnotes = true

			return
		}

		if inFootnotes {
			matches := _footnoteStartRE.FindStringSubmatch(line)
			if len(matches) > 0 {
				flushFootnote()

				currentFootnote = &SourceFootnote{
					Marker: _footnoteRefRE.FindString(line),
					Text:   strings.TrimSpace(matches[1]),
				}

				return
			}

			if currentFootnote != nil {
				if currentFootnote.Text != "" {
					currentFootnote.Text += "\n"
				}

				currentFootnote.Text += line

				return
			}
			// Stray text in the footnote region without a marker: keep it as
			// body content rather than losing it.
			blocks = append(blocks, sourceBlockForLine(line))

			return
		}

		blocks = append(blocks, sourceBlockForLine(line))
	}

	emitAnchorHeading := func(anchorID int, title string) {
		flushFootnote()

		inFootnotes = false

		block := SourceBlock{
			Type:     SourceBlockHeading,
			Text:     strings.TrimSpace(_spaceRE.ReplaceAllString(PlainText(title), " ")),
			AnchorID: anchorID,
		}
		block.HTML = blockHTML(block)
		blocks = append(blocks, block)
	}

	for _, rawLine := range lines {
		rest := rawLine
		for {
			anchorLoc := _anchorTagRE.FindStringSubmatchIndex(rest)
			hrLoc := _hrTagRE.FindStringIndex(rest)

			anchorAt, hrAt := -1, -1
			if len(anchorLoc) > 0 {
				anchorAt = anchorLoc[0]
			}

			if len(hrLoc) > 0 {
				hrAt = hrLoc[0]
			}

			if anchorAt < 0 && hrAt < 0 {
				break
			}

			if anchorAt >= 0 && (hrAt < 0 || anchorAt <= hrAt) {
				processTextPiece(rest[:anchorAt])

				tagName := rest[anchorLoc[2]:anchorLoc[3]]
				anchorID, err := strconv.Atoi(rest[anchorLoc[4]:anchorLoc[5]])
				after := rest[anchorLoc[1]:]
				title := after
				rest = ""

				if closeIdx := strings.Index(after, "</"+tagName+">"); closeIdx >= 0 {
					title = after[:closeIdx]
					rest = after[closeIdx+len("</"+tagName+">"):]
				}

				if err == nil {
					emitAnchorHeading(anchorID, title)
				} else {
					processTextPiece(title)
				}

				continue
			}

			processTextPiece(rest[:hrAt])
			flushFootnote()

			inFootnotes = true
			rest = rest[hrLoc[1]:]
		}

		processTextPiece(rest)
	}

	flushFootnote()

	text := PlainText(content)

	return StructuredContent{
		Format:    SourceFormatMixed,
		HTML:      semanticHTML(blocks, footnotes),
		Text:      text,
		Blocks:    blocks,
		Footnotes: footnotes,
	}
}
