package unitregistry

import (
	"errors"
	"fmt"
	"sort"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/readerutil"
)

// errHeadingIDZero guards the front-matter sentinel: a real source heading with
// id 0 would collide with the NULL-scope anchor form kitab/{b}/h/0/u/{n}.
var errHeadingIDZero = errors.New("source heading id 0 collides with the front-matter sentinel")

// DerivedUnit is one identity-free unit produced by the kitab deriver; the
// planner assigns or mints identity (id/ordinal/anchor/occurrence) afterwards.
type DerivedUnit struct {
	HeadingID *int // nil = front-matter
	PageID    int
	Kind      string
	Text      string
	HTML      string
	Marker    string // footnotes only
	// ParentIdx is the index (into the derived slice) of the owning body unit
	// for footnotes; -1 when absent.
	ParentIdx       int
	FootnoteLink    string // marker | fallback | unlinked (footnotes only)
	ProvenanceClass string
	EditActorID     string
	ReleaseKey      string
	ContentHash     []byte
	DocOrder        int // book-global document order
	ScopePosition   int // display index within the owning scope
}

// DeriveStats surfaces parser-quality signals alongside the derived set.
type DeriveStats struct {
	Pages         int
	Scopes        int
	StrayAnchors  int // toc anchors with no matching heading row (ignored)
	HTMLUnits     int
	Footnotes     int
	UnlinkedNotes int
}

// DeriveBook turns one book's effective content into its derived unit list.
// Pure function of src (no clock, no randomness, no map-order dependence):
// derived-unit identity flows from it, so re-running over an unchanged source
// must produce a deeply equal slice — the root of the AC-1 determinism chain.
//
// Scope ownership is by NEAREST heading: content is segmented linearly at
// toc-anchor positions (heading ranges nest and therefore cannot own units);
// headings whose anchor never appears in page content switch scope at the top
// of their page. Content before the first heading is front-matter (nil scope).
//
//nolint:gocognit,gocyclo,cyclop,funlen // linear per-page walk: scope-switch guard, body-block loop, footnote-linking loop — distinct stages, not tangled
func DeriveBook(src *entity.BookUnitSource) ([]DerivedUnit, DeriveStats, error) {
	stats := DeriveStats{}

	headings := make([]entity.BookUnitSourceHeading, 0, len(src.Headings))
	headings = append(headings, src.Headings...)
	sort.SliceStable(headings, func(i, j int) bool { return headings[i].HeadingID < headings[j].HeadingID })

	headingIdx := make(map[int]int, len(headings))
	for i, h := range headings {
		if h.HeadingID == frontMatterHeadingID {
			return nil, stats, fmt.Errorf("book %d: %w", src.BookID, errHeadingIDZero)
		}

		headingIdx[h.HeadingID] = i
	}

	pages := make([]entity.BookUnitSourcePage, 0, len(src.Pages))
	pages = append(pages, src.Pages...)
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].PageID < pages[j].PageID })

	units := make([]DerivedUnit, 0)

	var scope *int

	hPtr := 0

	setScope := func(headingID int) {
		id := headingID
		scope = &id
	}

	for _, page := range pages {
		stats.Pages++
		structured := readerutil.StructureMixedContent(page.ContentHTML)

		anchorsOnPage := make(map[int]bool)

		for _, b := range structured.Blocks {
			if b.AnchorID > 0 {
				anchorsOnPage[b.AnchorID] = true
			}
		}

		// Headings due on or before this page whose anchor is not inline
		// anywhere switch scope at the top of the page.
		for hPtr < len(headings) &&
			headings[hPtr].PageID <= page.PageID &&
			!anchorsOnPage[headings[hPtr].HeadingID] {
			setScope(headings[hPtr].HeadingID)
			hPtr++
		}

		provClass := entity.ProvenanceClassSource
		actor := ""

		if page.HasPublishedEdit {
			provClass = entity.ProvenanceClassEditorial
			actor = page.EditActorID
		}

		pageBodyStart := len(units)

		for _, block := range structured.Blocks {
			if block.AnchorID > 0 {
				idx, known := headingIdx[block.AnchorID]
				if !known {
					stats.StrayAnchors++

					continue
				}

				setScope(block.AnchorID)

				if idx >= hPtr {
					hPtr = idx + 1
				}
				// The title line itself is not a unit: book_headings.content
				// is the source of truth for heading titles.

				continue
			}

			if !trimNonEmpty(block.Text) {
				continue
			}

			if block.Type == readerutil.SourceBlockHTML {
				stats.HTMLUnits++
			}

			units = append(units, DerivedUnit{
				HeadingID:       scope,
				PageID:          page.PageID,
				Kind:            block.Type,
				Text:            block.Text,
				HTML:            block.HTML,
				ParentIdx:       -1,
				ProvenanceClass: provClass,
				EditActorID:     actor,
				ReleaseKey:      src.ReleaseKey,
				ContentHash:     ContentHash(block.Type, "", block.Text),
			})
		}

		pageBodyEnd := len(units)

		for _, fn := range structured.Footnotes {
			if !trimNonEmpty(fn.Text) {
				continue
			}

			stats.Footnotes++

			parentIdx := -1
			link := entity.FootnoteLinkUnlinked

			for i := pageBodyStart; i < pageBodyEnd; i++ {
				if textReferencesMarker(units[i].Text, fn.Marker) {
					parentIdx = i
					link = entity.FootnoteLinkMarker

					break
				}
			}

			if parentIdx < 0 && pageBodyEnd > pageBodyStart {
				parentIdx = pageBodyEnd - 1
				link = entity.FootnoteLinkFallback
			}

			if link == entity.FootnoteLinkUnlinked {
				stats.UnlinkedNotes++
			}

			fnScope := scope
			if parentIdx >= 0 {
				fnScope = units[parentIdx].HeadingID
			}

			units = append(units, DerivedUnit{
				HeadingID:       fnScope,
				PageID:          page.PageID,
				Kind:            entity.UnitKindFootnote,
				Text:            fn.Text,
				HTML:            fn.HTML,
				Marker:          fn.Marker,
				ParentIdx:       parentIdx,
				FootnoteLink:    link,
				ProvenanceClass: provClass,
				EditActorID:     actor,
				ReleaseKey:      src.ReleaseKey,
				ContentHash:     ContentHash(entity.UnitKindFootnote, fn.Marker, fn.Text),
			})
		}
	}

	// Stamp document order and per-scope display positions.
	posByScope := make(map[int]int)
	seenScopes := make(map[int]bool)

	for i := range units {
		units[i].DocOrder = i
		key := scopeKeyOf(units[i].HeadingID)
		units[i].ScopePosition = posByScope[key]

		posByScope[key]++
		if !seenScopes[key] {
			seenScopes[key] = true
			stats.Scopes++
		}
	}

	return units, stats, nil
}
