package readerutil_test

import (
	"testing"

	"github.com/alfariesh/surau-backend/internal/readerutil"
	"github.com/stretchr/testify/assert"
)

func TestResolveBookDBPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		bookID int
		want   string
	}{
		{name: "regular bucket", bookID: 797, want: "/raw/book/797/797.db"},
		{name: "large id bucket", bookID: 11797, want: "/raw/book/797/11797.db"},
		{name: "multiple of thousand", bookID: 7000, want: "/raw/book/000/7000.db"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, readerutil.ResolveBookDBPath("/raw", tt.bookID))
		})
	}
}

func TestNormalizeContent(t *testing.T) {
	t.Parallel()

	html, text := readerutil.NormalizeContent("舄<span data-type='title' id=toc-1>باب العلم</span>\r\nنص&nbsp;الكتاب")

	assert.Equal(t, "<span data-type=\"title\" id=\"toc-1\">باب العلم</span>\nنص\u00a0الكتاب", html)
	assert.Equal(t, "باب العلم\nنص الكتاب", text)
}

func TestNormalizeContentSanitizesUnsafeHTML(t *testing.T) {
	t.Parallel()

	html, text := readerutil.NormalizeContent(
		`<span id="toc-1" onclick="alert(1)">باب</span><script>alert(1)</script><a href="javascript:alert(1)">رابط</a>`,
	)

	assert.Equal(t, `<span id="toc-1">باب</span><a>رابط</a>`, html)
	assert.Equal(t, "باب رابط", text)
}

func TestStructureSourceContentPlainText(t *testing.T) {
	t.Parallel()

	content := "المسألة الأولى\nنص أول (¬١)\n{قُلْ هُوَ اللَّهُ أَحَدٌ} [الإخلاص- ١]\n_________\n(¬١) حاشية أولى\nتتمة الحاشية\n\nنص آخر"

	got := readerutil.StructureSourceContent(content)

	assert.Equal(t, readerutil.SourceFormatPlainText, got.Format)
	assert.Contains(t, got.HTML, `<h3 dir="rtl" lang="ar">المسألة الأولى</h3>`)
	assert.Contains(t, got.HTML, `<p dir="rtl" lang="ar">نص أول <sup data-type="footnote-ref">(¬١)</sup></p>`)
	assert.Contains(t, got.HTML, `<blockquote data-type="quran-quote" dir="rtl" lang="ar">{قُلْ هُوَ اللَّهُ أَحَدٌ} [الإخلاص- ١]</blockquote>`)
	assert.Contains(t, got.HTML, `<section data-type="footnotes" dir="rtl" lang="ar">`)
	assert.Len(t, got.Blocks, 4)
	assert.Equal(t, readerutil.SourceBlockHeading, got.Blocks[0].Type)
	assert.Len(t, got.Blocks[2].QuranCitations, 1)
	assert.Equal(t, "الإخلاص- ١", got.Blocks[2].QuranCitations[0].Reference)
	assert.Len(t, got.Footnotes, 1)
	assert.Equal(t, "(¬١)", got.Footnotes[0].Marker)
	assert.Equal(t, "حاشية أولى\nتتمة الحاشية", got.Footnotes[0].Text)
	assert.Contains(t, got.Text, "_________")
}

func TestStructureSourceContentKeepsSemanticHTML(t *testing.T) {
	t.Parallel()

	got := readerutil.StructureSourceContent(`<p onclick="bad()">نص</p><script>alert(1)</script>`)

	assert.Equal(t, readerutil.SourceFormatHTML, got.Format)
	assert.Equal(t, "<p>نص</p>", got.HTML)
	assert.Equal(t, "نص", got.Text)
	assert.Len(t, got.Blocks, 1)
	assert.Equal(t, readerutil.SourceBlockHTML, got.Blocks[0].Type)
}

func TestDecorateHeadingsAndRanges(t *testing.T) {
	t.Parallel()

	headings := []readerutil.SourceHeading{
		{ID: 1, ParentID: 0, PageID: 1, Content: "root"},
		{ID: 2, ParentID: 1, PageID: 2, Content: "child"},
		{ID: 3, ParentID: 0, PageID: 5, Content: "next root"},
	}

	decorated := readerutil.DecorateHeadings(headings)
	ranges := readerutil.BuildHeadingRanges(10, 8, decorated)

	assert.Equal(t, 0, decorated[0].Depth)
	assert.Equal(t, 1, decorated[1].Depth)
	assert.Equal(t, []readerutil.HeadingRange{
		{BookID: 10, HeadingID: 1, StartPageID: 1, EndPageID: 5, StartAnchor: "toc-1", EndAnchor: "toc-3"},
		{BookID: 10, HeadingID: 2, StartPageID: 2, EndPageID: 5, StartAnchor: "toc-2", EndAnchor: "toc-3"},
		{BookID: 10, HeadingID: 3, StartPageID: 5, EndPageID: 8, StartAnchor: "toc-3"},
	}, ranges)
}

func TestSliceAnchoredHTML(t *testing.T) {
	t.Parallel()

	content := "intro <span data-type='title' id=toc-1>أول</span> body <span data-type='title' id='toc-2'>ثان</span> tail"

	got := readerutil.SliceAnchoredHTML(content, "toc-1", "toc-2")

	assert.Equal(t, "<span data-type='title' id=toc-1>أول</span> body", got)
}

func TestSliceSectionContentFallsBackToPlainHeading(t *testing.T) {
	t.Parallel()

	content := "بسم الله الرحمن الرحيم\nالمسألة الأولى\nنص المسألة\n\nالمسألة الثانية\nنص آخر"

	got := readerutil.SliceSectionContent(content, "toc-1", "toc-2", "المسألة الأولى [تفصيل]", "المسألة الثانية [تفصيل]")

	assert.Equal(t, "بسم الله الرحمن الرحيم\nالمسألة الأولى\nنص المسألة", got)
}

func TestSliceSectionContentTrimsLongPlainPrefix(t *testing.T) {
	t.Parallel()

	prefix := "هذه بقية طويلة من الباب السابق وفيها كلام كثير ينبغي ألا يدخل في المسألة الجديدة لأنها ليست منها"
	content := prefix + "\nالمسألة الأولى\nنص المسألة\n\nالمسألة الثانية\nنص آخر"

	got := readerutil.SliceSectionContent(content, "toc-1", "toc-2", "المسألة الأولى [تفصيل]", "المسألة الثانية [تفصيل]")

	assert.Equal(t, "المسألة الأولى\nنص المسألة", got)
}
