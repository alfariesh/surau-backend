package unitregistry_test

import (
	"bytes"
	"crypto/sha256"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
)

func testSource() entity.BookUnitSource {
	return entity.BookUnitSource{
		BookID:     797,
		ReleaseKey: "3.1",
		Pages: []entity.BookUnitSourcePage{
			{PageID: 1, ContentHTML: "كلام أول قبل أول عنوان\nفقرة ثانية تحمل إشارة (¬١)\n__________\n(¬١) حاشية المقدمة"},
			{PageID: 2, ContentHTML: `<span data-type="title" id="toc-11">النوع الأول: الصحيح</span>` + "\n" +
				"إن الصحيح ما سنده اتصل\n<hr>\n(١) حاشية بلا مرجع في المتن"},
			{PageID: 3, ContentHTML: "فقرة تحت الصحيح في صفحة تالية"},
		},
		Headings: []entity.BookUnitSourceHeading{
			{HeadingID: 11, PageID: 2},
			{HeadingID: 12, PageID: 3},
		},
		LoadedAt: time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
	}
}

func TestDeriveBookScopesAndFootnotes(t *testing.T) {
	t.Parallel()

	src := testSource()

	units, stats, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}

	// Page 1 = front-matter (nil scope): 2 paragraphs + 1 marker-linked footnote.
	// Page 2 = anchor toc-11 switches scope mid-book: 1 paragraph + 1 fallback footnote.
	// Page 3 = heading 12 has no inline anchor: scope switches at page start.
	wants := []struct {
		heading  *int
		kind     string
		link     string
		parentIx int
	}{
		{nil, entity.UnitKindParagraph, "", -1},
		{nil, entity.UnitKindParagraph, "", -1},
		{nil, entity.UnitKindFootnote, entity.FootnoteLinkMarker, 1},
		{new(11), entity.UnitKindParagraph, "", -1},
		{new(11), entity.UnitKindFootnote, entity.FootnoteLinkFallback, 3},
		{new(12), entity.UnitKindParagraph, "", -1},
	}
	if len(units) != len(wants) {
		t.Fatalf("units = %d, want %d: %+v", len(units), len(wants), units)
	}

	for i, want := range wants {
		got := units[i]
		if (got.HeadingID == nil) != (want.heading == nil) ||
			(got.HeadingID != nil && *got.HeadingID != *want.heading) {
			t.Fatalf("unit[%d] heading = %v, want %v", i, got.HeadingID, want.heading)
		}

		if got.Kind != want.kind || got.FootnoteLink != want.link || got.ParentIdx != want.parentIx {
			t.Fatalf("unit[%d] = kind %q link %q parent %d, want %+v", i, got.Kind, got.FootnoteLink, got.ParentIdx, want)
		}

		if got.DocOrder != i {
			t.Fatalf("unit[%d] doc order = %d", i, got.DocOrder)
		}
	}

	// Scope positions restart per scope.
	if units[3].ScopePosition != 0 || units[4].ScopePosition != 1 || units[5].ScopePosition != 0 {
		t.Fatalf("scope positions = %d %d %d", units[3].ScopePosition, units[4].ScopePosition, units[5].ScopePosition)
	}

	if stats.Scopes != 3 || stats.Footnotes != 2 || stats.UnlinkedNotes != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestDeriveBookDeterministic(t *testing.T) {
	t.Parallel()

	src := testSource()

	first, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}

	second, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("DeriveBook is not deterministic")
	}
}

func TestDeriveBookRejectsHeadingIDZero(t *testing.T) {
	t.Parallel()

	src := testSource()

	src.Headings = append(src.Headings, entity.BookUnitSourceHeading{HeadingID: 0, PageID: 1})
	if _, _, err := unitregistry.DeriveBook(&src); err == nil || !strings.Contains(err.Error(), "front-matter sentinel") {
		t.Fatalf("err = %v, want front-matter sentinel error", err)
	}
}

func TestDeriveBookEditorialProvenance(t *testing.T) {
	t.Parallel()

	src := testSource()
	src.Pages[0].HasPublishedEdit = true
	src.Pages[0].EditActorID = "editor-1"

	units, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}

	if units[0].ProvenanceClass != entity.ProvenanceClassEditorial || units[0].EditActorID != "editor-1" {
		t.Fatalf("unit[0] provenance = %q/%q", units[0].ProvenanceClass, units[0].EditActorID)
	}

	if units[3].ProvenanceClass != entity.ProvenanceClassSource || units[3].ReleaseKey != "3.1" {
		t.Fatalf("unit[3] provenance = %q release %q", units[3].ProvenanceClass, units[3].ReleaseKey)
	}
}

//nolint:wsl_v5 // linear fixture mutations intentionally stay adjacent
func TestDeriveBookFormattingOnlyPublishedEditKeepsSourceProvenance(t *testing.T) {
	t.Parallel()

	src := simpleDeriverSource("<strong>نص ثابت</strong>")
	src.Pages[0].ContentText = "نص ثابت"
	src.Pages[0].RawContentHTML = "نص ثابت"
	src.Pages[0].RawContentText = "نص ثابت"
	src.Pages[0].HasPublishedEdit = true
	src.Pages[0].EditActorID = "formatter"

	units, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}
	if len(units) != 1 || units[0].ProvenanceClass != entity.ProvenanceClassSource ||
		units[0].FormattingEditActorID != "formatter" {
		t.Fatalf("format-only provenance = %+v", units)
	}
}

//nolint:wsl_v5 // provenance matrix remains adjacent to exact block assertions
func TestDeriveBookTextEditKeepsUnchangedBlocksAsSource(t *testing.T) {
	t.Parallel()

	src := simpleDeriverSource("<p>نص ثابت</p><p>نص جديد</p>")
	src.Pages[0].ContentText = "نص ثابت\nنص جديد"
	src.Pages[0].RawContentHTML = "<p>نص ثابت</p><p>نص قديم</p>"
	src.Pages[0].RawContentText = "نص ثابت\nنص قديم"
	src.Pages[0].HasPublishedEdit = true
	src.Pages[0].EditActorID = "editor"

	units, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("units = %+v, want two", units)
	}
	if units[0].ProvenanceClass != entity.ProvenanceClassSource || units[0].EditActorID != "" {
		t.Fatalf("unchanged unit provenance = %q/%q", units[0].ProvenanceClass, units[0].EditActorID)
	}
	if units[1].ProvenanceClass != entity.ProvenanceClassEditorial || units[1].EditActorID != "editor" {
		t.Fatalf("new unit provenance = %q/%q", units[1].ProvenanceClass, units[1].EditActorID)
	}
}

func TestDeriveBookIgnoresStrayAnchors(t *testing.T) {
	t.Parallel()

	src := testSource()
	src.Pages[2].ContentHTML = `<span id="toc-99">عنوان بلا صف</span>` + "\n" + src.Pages[2].ContentHTML

	units, stats, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}

	if stats.StrayAnchors != 1 {
		t.Fatalf("stray anchors = %d, want 1", stats.StrayAnchors)
	}
	// Scope on page 3 still comes from heading 12 (page-start switch).
	last := units[len(units)-1]
	if last.HeadingID == nil || *last.HeadingID != 12 {
		t.Fatalf("last unit scope = %v, want 12", last.HeadingID)
	}
}

// A heading-only Shamela page is still a published source document. K-1's
// full-catalog denominator requires at least one exact, citable book_page unit
// instead of silently treating the structural title as an empty page.
func TestDeriveBookHeadingOnlyPageGetsExactHTMLFallback(t *testing.T) {
	t.Parallel()

	src := entity.BookUnitSource{
		BookID: 7, ReleaseKey: "1.0",
		Pages: []entity.BookUnitSourcePage{{
			PageID:      9,
			ContentHTML: `<span data-type="title" id="toc-31">باب الإيمان</span>`,
			ContentText: "باب الإيمان",
		}},
		Headings: []entity.BookUnitSourceHeading{{HeadingID: 31, PageID: 9}},
	}

	units, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}

	if len(units) != 1 {
		t.Fatalf("units = %+v, want one structural fallback", units)
	}

	unit := units[0]
	if unit.HeadingID == nil || *unit.HeadingID != 31 || unit.PageID != 9 ||
		unit.Kind != entity.UnitKindHTML || unit.Text != "باب الإيمان" ||
		unit.SourceCharStart != 0 || unit.SourceCharEnd != len([]rune("باب الإيمان")) {
		t.Fatalf("fallback = %+v", unit)
	}
}

func TestDeriveBookStructuralFallbackParentsFootnoteWithoutLeakingIt(t *testing.T) {
	t.Parallel()

	src := entity.BookUnitSource{
		BookID: 8, ReleaseKey: "1.0",
		Pages: []entity.BookUnitSourcePage{{
			PageID:      10,
			ContentHTML: `<span id="toc-41">باب التوحيد</span><hr>(١) شرح الحاشية`,
		}},
		Headings: []entity.BookUnitSourceHeading{{HeadingID: 41, PageID: 10}},
	}

	units, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}

	if len(units) != 2 || units[0].Kind != entity.UnitKindHTML ||
		units[0].Text != "باب التوحيد" || strings.Contains(units[0].Text, "شرح الحاشية") {
		t.Fatalf("units = %+v, want isolated structural fallback plus footnote", units)
	}

	if units[1].Kind != entity.UnitKindFootnote || units[1].ParentIdx != 0 ||
		units[1].FootnoteLink != entity.FootnoteLinkFallback {
		t.Fatalf("footnote = %+v, want fallback parent", units[1])
	}
}

//nolint:wsl_v5 // metadata assertions stay beside each matching role
func TestDeriveBookPublishedAssetsPropagateRoleProvenanceReviewAndGeneration(t *testing.T) {
	t.Parallel()

	runID := "11111111-1111-4111-8111-111111111111"
	src := testSource()
	src.Assets = []entity.BookUnitSourceAsset{
		{
			HeadingID: 11, PageID: 2, ContentRole: entity.UnitContentRoleSectionTranslation,
			Language: "id", Content: "Terjemahan bagian yang sama", ProvenanceClass: entity.ProvenanceClassMachine,
			GenerationRunID: &runID, ReviewStatus: entity.UnitReviewStatusPending,
		},
		{
			HeadingID: 11, PageID: 2, ContentRole: entity.UnitContentRoleSectionTranslation,
			Language: "en", Content: "Terjemahan bagian yang sama", ProvenanceClass: entity.ProvenanceClassEditorial,
			ReviewStatus: entity.UnitReviewStatusApproved,
		},
		{
			HeadingID: 12, PageID: 3, ContentRole: entity.UnitContentRoleHeadingSummary,
			Language: "id", Content: "<strong>Ringkasan bab</strong>", ProvenanceClass: entity.ProvenanceClassMachine,
			GenerationRunID: &runID, ReviewStatus: entity.UnitReviewStatusApproved,
		},
		{
			HeadingID: 12, PageID: 3, ContentRole: entity.UnitContentRoleHeadingSummary,
			Language: "en", Content: "legacy is not relabelled", ProvenanceClass: "legacy_unknown",
			ReviewStatus: entity.UnitReviewStatusPending,
		},
	}

	units, stats, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}
	if stats.SkippedLegacyAssets != 1 {
		t.Fatalf("skipped legacy = %d, want 1", stats.SkippedLegacyAssets)
	}

	var translatedID, translatedEN, summary bool
	for _, unit := range units {
		switch {
		case unit.ContentRole == entity.UnitContentRoleSectionTranslation && unit.Language == "id":
			translatedID = true
			if unit.ProvenanceClass != entity.ProvenanceClassMachine || unit.GenerationRunID == nil ||
				*unit.GenerationRunID != runID || unit.ReviewStatus != entity.UnitReviewStatusPending {
				t.Fatalf("machine translation metadata = %+v", unit)
			}
		case unit.ContentRole == entity.UnitContentRoleSectionTranslation && unit.Language == "en":
			translatedEN = true
		case unit.ContentRole == entity.UnitContentRoleHeadingSummary:
			summary = true
			if unit.Kind != entity.UnitKindSummary || unit.Text != "Ringkasan bab" {
				t.Fatalf("summary = %+v", unit)
			}
		}
	}
	if !translatedID || !translatedEN || !summary {
		t.Fatalf("asset roles missing id=%v en=%v summary=%v", translatedID, translatedEN, summary)
	}
}

//nolint:wsl_v5 // ordered marker assertions mirror deterministic linkage
func TestDeriveBookRepeatedFootnoteMarkersLinkInOrder(t *testing.T) {
	t.Parallel()

	src := simpleDeriverSource("متن أول (١)\nمتن ثان (١)<hr>(١) شرح أول\n(١) شرح ثان")

	units, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}
	if len(units) != 4 || units[2].ParentIdx != 0 || units[3].ParentIdx != 1 {
		t.Fatalf("duplicate footnote parents = %+v", units)
	}
}

func simpleDeriverSource(content string) entity.BookUnitSource {
	return entity.BookUnitSource{
		BookID: 1, ReleaseKey: "1.0",
		Pages: []entity.BookUnitSourcePage{{PageID: 1, ContentHTML: content}},
	}
}

//nolint:wsl_v5 // raw/effective snapshot assertions are intentionally linear
func TestRawBookSourceSnapshotRestoresLegacyMentionDocument(t *testing.T) {
	t.Parallel()

	src := simpleDeriverSource("نص التحرير")
	src.Pages[0].ContentText = "نص التحرير"
	src.Pages[0].RawContentHTML = "النص الخام 😀"
	src.Pages[0].RawContentText = "النص الخام 😀"
	src.Pages[0].HasPublishedEdit = true
	src.Pages[0].EditActorID = "editor"
	src.Assets = []entity.BookUnitSourceAsset{{Content: "not legacy page content"}}

	raw := unitregistry.RawBookSourceSnapshot(&src)
	if raw.Pages[0].ContentHTML != "النص الخام 😀" || raw.Pages[0].ContentText != "النص الخام 😀" ||
		raw.Pages[0].HasPublishedEdit || raw.Pages[0].EditActorID != "" || len(raw.Assets) != 0 {
		t.Fatalf("raw snapshot = %+v", raw)
	}
	if src.Pages[0].ContentHTML != "نص التحرير" || !src.Pages[0].HasPublishedEdit {
		t.Fatalf("source mutated = %+v", src.Pages[0])
	}
}

//nolint:wsl_v5 // fail-closed fallback assertions mirror the derivation stages
func TestDeriveBookHTMLTextMismatchFallsBackToExactPersistedDocument(t *testing.T) {
	t.Parallel()

	src := simpleDeriverSource("<p>HTML yang tidak sama</p>")
	src.Pages[0].ContentText = "النص المرجعي 😀"

	units, stats, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}
	if stats.SourceAlignmentFallbacks != 1 || len(units) != 1 {
		t.Fatalf("fallback stats/units = %+v / %+v", stats, units)
	}
	if units[0].Text != src.Pages[0].ContentText || units[0].SourceCharStart != 0 ||
		units[0].SourceCharEnd != len([]rune(src.Pages[0].ContentText)) {
		t.Fatalf("fallback unit = %+v", units[0])
	}

	wantHash := sha256.Sum256([]byte(src.Pages[0].ContentText))
	if !bytes.Equal(units[0].SourceDocumentHash, wantHash[:]) {
		t.Fatalf("document hash = %x, want %x", units[0].SourceDocumentHash, wantHash)
	}
}

//nolint:wsl_v5 // all three emitted unit safety properties are checked together
func TestDeriveBookIsolatesInlineQuranQuoteFromInterpretiveNeighbours(t *testing.T) {
	t.Parallel()

	sourceText := "قال تعالى {إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ} [الكوثر:1] ثم شرح المعنى 😀"
	src := simpleDeriverSource(`<p>قال تعالى <strong>{إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ}</strong> ` +
		`[الكوثر:1] ثم شرح المعنى 😀</p>`)
	src.Pages[0].ContentText = sourceText

	units, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}
	if len(units) != 3 {
		t.Fatalf("units = %+v, want paragraph/quran_quote/paragraph", units)
	}
	wantKinds := []string{
		entity.UnitKindParagraph,
		entity.UnitKindQuranQuote,
		entity.UnitKindParagraph,
	}
	document := []rune(sourceText)
	for i, unit := range units {
		if unit.Kind != wantKinds[i] {
			t.Fatalf("unit[%d] kind = %q, want %q", i, unit.Kind, wantKinds[i])
		}
		if string(document[unit.SourceCharStart:unit.SourceCharEnd]) != unit.Text {
			t.Fatalf("unit[%d] span = %q, want %q", i,
				string(document[unit.SourceCharStart:unit.SourceCharEnd]), unit.Text)
		}
		if unit.Kind != entity.UnitKindQuranQuote &&
			(strings.Contains(unit.Text, "أَعْطَيْنَاكَ") || strings.Contains(unit.HTML, "أَعْطَيْنَاكَ")) {
			t.Fatalf("ayah leaked into eligible neighbor: %+v", unit)
		}
	}
}

//nolint:wsl_v5 // kind, marker, parent and exact rune span are one fail-closed contract
func TestDeriveBookQuranFootnoteFailsClosedWithMarkerLinkage(t *testing.T) {
	t.Parallel()

	src := simpleDeriverSource("متن يحمل إشارة (١)<hr>(١) قال تعالى " +
		"{إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ} [الكوثر:1] ثم شرح")
	units, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("units = %+v, want body plus fail-closed footnote", units)
	}
	note := units[1]
	if note.Kind != entity.UnitKindQuranQuote || note.Marker != "(١)" ||
		note.ParentIdx != 0 || note.FootnoteLink != entity.FootnoteLinkMarker {
		t.Fatalf("Quran footnote safety/linkage = %+v", note)
	}
	if !strings.Contains(note.Text, "ثم شرح") {
		t.Fatalf("footnote explanation was lost: %+v", note)
	}
}

//nolint:wsl_v5 // safety assertions remain next to each exact span check
func TestDeriveBookSemanticQuranFootnoteTextFallbackStillExcludesAyah(t *testing.T) {
	t.Parallel()

	const ayah = "إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ"
	sourceText := "متن يحمل إشارة (١) (١) مقدمة " + ayah + " ثم شرح"
	src := simpleDeriverSource(`متن يحمل إشارة (١)<hr>(١) مقدمة <blockquote data-type="quran-quote">` +
		ayah + `</blockquote> ثم شرح`)
	src.Pages[0].ContentText = sourceText

	units, stats, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}
	if stats.SourceAlignmentFallbacks != 1 {
		t.Fatalf("alignment fallbacks = %d, want 1", stats.SourceAlignmentFallbacks)
	}

	quranUnits := 0

	document := []rune(sourceText)
	for _, unit := range units {
		if string(document[unit.SourceCharStart:unit.SourceCharEnd]) != unit.Text {
			t.Fatalf("fallback span = %q want %q",
				string(document[unit.SourceCharStart:unit.SourceCharEnd]), unit.Text)
		}
		if unit.Kind == entity.UnitKindQuranQuote {
			quranUnits++

			continue
		}
		if strings.Contains(unit.Text, ayah) || strings.Contains(unit.HTML, ayah) {
			t.Fatalf("semantic footnote ayah leaked into eligible unit: %+v", unit)
		}
	}
	if quranUnits != 1 {
		t.Fatalf("quran units = %d, want exact isolated fallback: %+v", quranUnits, units)
	}
}
