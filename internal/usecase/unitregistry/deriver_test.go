package unitregistry_test

import (
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
