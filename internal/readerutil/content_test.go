package readerutil_test

import (
	"testing"

	"github.com/evrone/go-clean-template/internal/readerutil"
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
