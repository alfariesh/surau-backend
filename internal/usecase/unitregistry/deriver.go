package unitregistry

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/readerutil"
)

// errHeadingIDZero guards the front-matter sentinel: a real source heading with
// id 0 would collide with the NULL-scope anchor form kitab/{b}/h/0/u/{n}.
var (
	errHeadingIDZero   = errors.New("source heading id 0 collides with the front-matter sentinel")
	errSourceAlignment = errors.New("structured units cannot align to persisted content_text")
)

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
	ParentIdx             int
	FootnoteLink          string // marker | fallback | unlinked (footnotes only)
	ContentRole           string
	Language              string
	ReviewStatus          string
	SourceDocumentHash    []byte
	SourceCharStart       int
	SourceCharEnd         int
	ProvenanceClass       string
	GenerationRunID       *string
	EditActorID           string
	FormattingEditActorID string
	ReleaseKey            string
	ContentHash           []byte
	DocOrder              int // book-global document order
	ScopePosition         int // display index within the owning scope
}

// DeriveStats surfaces parser-quality signals alongside the derived set.
type DeriveStats struct {
	Pages                    int
	Scopes                   int
	StrayAnchors             int // toc anchors with no matching heading row (ignored)
	HTMLUnits                int
	Footnotes                int
	UnlinkedNotes            int
	SkippedLegacyAssets      int
	SourceAlignmentFallbacks int
}

// RawBookSourceSnapshot returns the imported page snapshot that existing
// knowledge_mentions offsets refer to. Callers that migrate those offsets can
// derive this snapshot first, bind exact spans, then reconcile the effective
// editorial source and preserve the transition through normal lineage.
// Production enrichments are intentionally excluded: their coordinates were
// never part of the legacy page-offset contract.
//
//nolint:wsl_v5 // shallow copy setup precedes the page rewrite loop
func RawBookSourceSnapshot(src *entity.BookUnitSource) entity.BookUnitSource {
	raw := *src
	raw.Pages = append([]entity.BookUnitSourcePage(nil), src.Pages...)
	raw.Assets = nil
	for i := range raw.Pages {
		raw.Pages[i].ContentHTML = raw.Pages[i].RawContentHTML
		raw.Pages[i].ContentText = raw.Pages[i].RawContentText
		raw.Pages[i].HasPublishedEdit = false
		raw.Pages[i].EditActorID = ""
	}

	return raw
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
//nolint:gocognit,gocyclo,cyclop,funlen,nlreturn,wsl_v5 // linear deterministic stages
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
		structured, alignmentFallback, err := structureBookPage(page.ContentHTML, page.ContentText)
		if err != nil {
			return nil, stats, fmt.Errorf("book %d page %d: %w", src.BookID, page.PageID, err)
		}
		if alignmentFallback {
			stats.SourceAlignmentFallbacks++
		}
		documentHash := sha256.Sum256([]byte(structured.Text))
		rawUnitCounts := rawPageUnitCounts(&page)

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

		formatActor := ""

		if page.HasPublishedEdit && page.RawContentText != "" && page.RawContentText == page.ContentText {
			formatActor = page.EditActorID
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
			provClass, actor := pageUnitProvenance(&page, rawUnitCounts,
				pageUnitSourceKey(block.Type, "", block.Text))

			units = append(units, DerivedUnit{
				HeadingID:             scope,
				PageID:                page.PageID,
				Kind:                  block.Type,
				Text:                  block.Text,
				HTML:                  block.HTML,
				ParentIdx:             -1,
				ContentRole:           entity.UnitContentRoleBookPage,
				Language:              "ar",
				ReviewStatus:          entity.UnitReviewStatusApproved,
				SourceDocumentHash:    documentHash[:],
				SourceCharStart:       block.SourceCharStart,
				SourceCharEnd:         block.SourceCharEnd,
				ProvenanceClass:       provClass,
				EditActorID:           actor,
				FormattingEditActorID: formatActor,
				ReleaseKey:            src.ReleaseKey,
				ContentHash:           ContentHash(block.Type, "", block.Text),
			})
		}

		pageBodyEnd := len(units)

		markerCursor := make(map[int]int)
		for _, fn := range structured.Footnotes {
			if !trimNonEmpty(fn.Text) {
				continue
			}

			stats.Footnotes++

			parentIdx := -1
			link := entity.FootnoteLinkUnlinked

			marker := markerValue(fn.Marker)
			start := pageBodyStart
			if marker >= 0 && markerCursor[marker] > start {
				start = markerCursor[marker]
			}
			for i := start; i < pageBodyEnd; i++ {
				if textReferencesMarker(units[i].Text, fn.Marker) {
					parentIdx = i
					link = entity.FootnoteLinkMarker
					markerCursor[marker] = i + 1

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

			kind := derivedFootnoteKind(&fn)
			provClass, actor := pageUnitProvenance(&page, rawUnitCounts,
				pageUnitSourceKey(kind, fn.Marker, fn.Text))
			units = append(units, DerivedUnit{
				HeadingID:             fnScope,
				PageID:                page.PageID,
				Kind:                  kind,
				Text:                  fn.Text,
				HTML:                  fn.HTML,
				Marker:                fn.Marker,
				ParentIdx:             parentIdx,
				FootnoteLink:          link,
				ContentRole:           entity.UnitContentRoleBookPage,
				Language:              "ar",
				ReviewStatus:          entity.UnitReviewStatusApproved,
				SourceDocumentHash:    documentHash[:],
				SourceCharStart:       fn.SourceCharStart,
				SourceCharEnd:         fn.SourceCharEnd,
				ProvenanceClass:       provClass,
				EditActorID:           actor,
				FormattingEditActorID: formatActor,
				ReleaseKey:            src.ReleaseKey,
				ContentHash:           ContentHash(kind, fn.Marker, fn.Text),
			})
		}
	}

	assets := append([]entity.BookUnitSourceAsset(nil), src.Assets...)
	sort.SliceStable(assets, func(i, j int) bool {
		if assets[i].HeadingID != assets[j].HeadingID {
			return assets[i].HeadingID < assets[j].HeadingID
		}
		if assets[i].ContentRole != assets[j].ContentRole {
			return assets[i].ContentRole < assets[j].ContentRole
		}
		return assets[i].Language < assets[j].Language
	})
	for _, asset := range assets {
		if asset.ProvenanceClass == "legacy_unknown" {
			stats.SkippedLegacyAssets++
			continue
		}
		if _, ok := headingIdx[asset.HeadingID]; !ok || !trimNonEmpty(asset.Content) {
			continue
		}
		appendAssetUnits(&units, &stats, src, &asset)
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

//nolint:funlen,gocognit,gocyclo,cyclop,nlreturn,wsl_v5 // deterministic asset block/footnote pass
func appendAssetUnits(
	units *[]DerivedUnit,
	stats *DeriveStats,
	src *entity.BookUnitSource,
	asset *entity.BookUnitSourceAsset,
) {
	headingID := asset.HeadingID
	if asset.ContentRole == entity.UnitContentRoleHeadingSummary {
		text := strings.TrimSpace(readerutil.PlainText(readerutil.SanitizeHTML(asset.Content)))
		if text == "" {
			return
		}
		hash := sha256.Sum256([]byte(text))
		*units = append(*units, DerivedUnit{
			HeadingID: &headingID, PageID: asset.PageID, Kind: entity.UnitKindSummary,
			Text: text, HTML: readerutil.SanitizeHTML(asset.Content), ParentIdx: -1,
			ContentRole: asset.ContentRole, Language: asset.Language, ReviewStatus: asset.ReviewStatus,
			SourceDocumentHash: hash[:], SourceCharStart: 0, SourceCharEnd: utf8.RuneCountInString(text),
			ProvenanceClass: asset.ProvenanceClass, GenerationRunID: asset.GenerationRunID,
			ReleaseKey: src.ReleaseKey, ContentHash: ContentHash(entity.UnitKindSummary, "", text),
		})
		return
	}

	structured := readerutil.StructureMixedContent(asset.Content)
	documentHash := sha256.Sum256([]byte(structured.Text))
	bodyStart := len(*units)
	for _, block := range structured.Blocks {
		if block.AnchorID > 0 || !trimNonEmpty(block.Text) {
			continue
		}
		if block.Type == readerutil.SourceBlockHTML {
			stats.HTMLUnits++
		}
		*units = append(*units, DerivedUnit{
			HeadingID: &headingID, PageID: asset.PageID, Kind: block.Type, Text: block.Text, HTML: block.HTML,
			ParentIdx: -1, ContentRole: asset.ContentRole, Language: asset.Language,
			ReviewStatus: asset.ReviewStatus, SourceDocumentHash: documentHash[:],
			SourceCharStart: block.SourceCharStart, SourceCharEnd: block.SourceCharEnd,
			ProvenanceClass: asset.ProvenanceClass, GenerationRunID: asset.GenerationRunID,
			ReleaseKey: src.ReleaseKey, ContentHash: ContentHash(block.Type, "", block.Text),
		})
	}
	bodyEnd := len(*units)
	markerCursor := make(map[int]int)
	for _, fn := range structured.Footnotes {
		if !trimNonEmpty(fn.Text) {
			continue
		}
		stats.Footnotes++
		parentIdx := -1
		link := entity.FootnoteLinkUnlinked
		marker := markerValue(fn.Marker)
		start := bodyStart
		if marker >= 0 && markerCursor[marker] > start {
			start = markerCursor[marker]
		}
		for i := start; i < bodyEnd; i++ {
			if textReferencesMarker((*units)[i].Text, fn.Marker) {
				parentIdx, link = i, entity.FootnoteLinkMarker
				markerCursor[marker] = i + 1
				break
			}
		}
		if parentIdx < 0 && bodyEnd > bodyStart {
			parentIdx, link = bodyEnd-1, entity.FootnoteLinkFallback
		}
		if link == entity.FootnoteLinkUnlinked {
			stats.UnlinkedNotes++
		}
		kind := derivedFootnoteKind(&fn)
		*units = append(*units, DerivedUnit{
			HeadingID: &headingID, PageID: asset.PageID, Kind: kind,
			Text: fn.Text, HTML: fn.HTML, Marker: fn.Marker, ParentIdx: parentIdx, FootnoteLink: link,
			ContentRole: asset.ContentRole, Language: asset.Language, ReviewStatus: asset.ReviewStatus,
			SourceDocumentHash: documentHash[:], SourceCharStart: fn.SourceCharStart, SourceCharEnd: fn.SourceCharEnd,
			ProvenanceClass: asset.ProvenanceClass, GenerationRunID: asset.GenerationRunID,
			ReleaseKey: src.ReleaseKey, ContentHash: ContentHash(kind, fn.Marker, fn.Text),
		})
	}
}

// structureBookPage owns the exact HTML→persisted-text alignment contract for
// both effective and raw provenance snapshots. Semantic Quran footnotes remain
// fail-closed even when legacy content_text forces the plain-text fallback.
//
//nolint:wsl_v5 // parsing, alignment, and fail-closed fallback form one ordered pipeline
func structureBookPage(contentHTML, contentText string) (readerutil.StructuredContent, bool, error) {
	content := contentHTML
	if strings.TrimSpace(content) == "" {
		content = contentText
	}
	structured := readerutil.StructureMixedContent(content)
	quranFootnoteFailClosed := hasQuranFootnote(structured.Footnotes)

	quotes := quranFootnoteQuotes(structured.Footnotes)
	if strings.TrimSpace(contentText) == "" || readerutil.AlignMixedSourceSpans(&structured, contentText) {
		return structured, false, nil
	}

	structured = readerutil.StructureMixedContent(contentText)
	if quranFootnoteFailClosed && !readerutil.IsolateExactQuranQuotes(&structured, quotes) {
		failClosedStructuredPage(&structured)
	}
	if !readerutil.AlignMixedSourceSpans(&structured, contentText) {
		return structured, true, errSourceAlignment
	}

	return structured, true, nil
}

//nolint:wsl_v5 // exact raw-block inventory mirrors the deriver loop
func rawPageUnitCounts(page *entity.BookUnitSourcePage) map[string]int {
	counts := make(map[string]int)
	if !page.HasPublishedEdit || (strings.TrimSpace(page.RawContentHTML) == "" &&
		strings.TrimSpace(page.RawContentText) == "") {
		return counts
	}
	structured, _, err := structureBookPage(page.RawContentHTML, page.RawContentText)
	if err != nil {
		return counts
	}

	for i := range structured.Blocks {
		block := &structured.Blocks[i]
		if block.AnchorID == 0 && trimNonEmpty(block.Text) {
			counts[pageUnitSourceKey(block.Type, "", block.Text)]++
		}
	}

	for i := range structured.Footnotes {
		footnote := &structured.Footnotes[i]
		if trimNonEmpty(footnote.Text) {
			counts[pageUnitSourceKey(derivedFootnoteKind(footnote), footnote.Marker, footnote.Text)]++
		}
	}

	return counts
}

func pageUnitSourceKey(kind, marker, text string) string {
	return kind + "\x00" + marker + "\x00" + text
}

//nolint:wsl_v5 // exact raw-match consumption precedes editorial fallback
func pageUnitProvenance(
	page *entity.BookUnitSourcePage,
	rawCounts map[string]int,
	key string,
) (provenanceClass, actorID string) {
	if !page.HasPublishedEdit {
		return entity.ProvenanceClassSource, ""
	}
	if rawCounts[key] > 0 {
		rawCounts[key]--

		return entity.ProvenanceClassSource, ""
	}

	return entity.ProvenanceClassEditorial, page.EditActorID
}

func hasQuranFootnote(footnotes []readerutil.SourceFootnote) bool {
	for i := range footnotes {
		if footnotes[i].ContainsQuran {
			return true
		}
	}

	return false
}

func quranFootnoteQuotes(footnotes []readerutil.SourceFootnote) []string {
	quotes := make([]string, 0)

	for i := range footnotes {
		if footnotes[i].ContainsQuran {
			quotes = append(quotes, footnotes[i].QuranQuotes...)
		}
	}

	return quotes
}

func failClosedStructuredPage(content *readerutil.StructuredContent) {
	for i := range content.Blocks {
		content.Blocks[i].Type = readerutil.SourceBlockQuranQuote
	}

	for i := range content.Footnotes {
		content.Footnotes[i].ContainsQuran = true
	}
}

func derivedFootnoteKind(footnote *readerutil.SourceFootnote) string {
	if footnote.ContainsQuran {
		return entity.UnitKindQuranQuote
	}

	return entity.UnitKindFootnote
}
