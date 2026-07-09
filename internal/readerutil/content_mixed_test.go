package readerutil_test

import (
	"reflect"
	"testing"

	"github.com/alfariesh/surau-backend/internal/readerutil"
)

// Shapes below mirror real Shamela pages of pilot book 797 (متن الزبد) after
// import-time NormalizeContent: page 1 is tag-free, page 12 carries a title
// anchor span and an <hr> footnote separator.

func TestStructureMixedContentPlainPage(t *testing.T) {
	t.Parallel()

	content := "باب المقدمة\n" +
		"الحمد لله رب العالمين القائل في محكم التنزيل. (¬١)\n" +
		"{الر كتاب أنزلناه إليك} [إبراهيم: 1]\n" +
		"__________\n" +
		"(¬١) سورة النحل\n" +
		"\n" +
		"(¬٢) رواه أحمد\n" +
		"تتمة الحاشية الثانية"

	got := readerutil.StructureMixedContent(content)

	if got.Format != readerutil.SourceFormatMixed {
		t.Fatalf("format = %q, want %q", got.Format, readerutil.SourceFormatMixed)
	}

	wantBlocks := []struct {
		blockType string
		text      string
		anchorID  int
	}{
		{readerutil.SourceBlockHeading, "باب المقدمة", 0},
		{readerutil.SourceBlockParagraph, "الحمد لله رب العالمين القائل في محكم التنزيل. (¬١)", 0},
		{readerutil.SourceBlockQuranQuote, "{الر كتاب أنزلناه إليك} [إبراهيم: 1]", 0},
	}
	if len(got.Blocks) != len(wantBlocks) {
		t.Fatalf("blocks = %d, want %d (%+v)", len(got.Blocks), len(wantBlocks), got.Blocks)
	}

	for i, want := range wantBlocks {
		if got.Blocks[i].Type != want.blockType || got.Blocks[i].Text != want.text || got.Blocks[i].AnchorID != want.anchorID {
			t.Fatalf("block[%d] = %+v, want %+v", i, got.Blocks[i], want)
		}
	}

	// Unlike structurePlainText, a blank line inside the footnote region must
	// not close it: both footnotes survive, with continuation joined.
	if len(got.Footnotes) != 2 {
		t.Fatalf("footnotes = %d, want 2 (%+v)", len(got.Footnotes), got.Footnotes)
	}

	if got.Footnotes[0].Marker != "(¬١)" || got.Footnotes[0].Text != "سورة النحل" {
		t.Fatalf("footnote[0] = %+v", got.Footnotes[0])
	}

	if got.Footnotes[1].Marker != "(¬٢)" || got.Footnotes[1].Text != "رواه أحمد\nتتمة الحاشية الثانية" {
		t.Fatalf("footnote[1] = %+v", got.Footnotes[1])
	}

	if len(got.Blocks[2].QuranCitations) != 1 || got.Blocks[2].QuranCitations[0].Reference != "إبراهيم: 1" {
		t.Fatalf("quran citations = %+v", got.Blocks[2].QuranCitations)
	}
}

func TestStructureMixedContentTaggedPage(t *testing.T) {
	t.Parallel()

	// Sanitized shape of book 797 page 12: title anchor, poem verse (a body
	// line that STARTS with a bare marker but is not a footnote), <hr>, blank,
	// then the footnote body.
	content := `<span data-type="title" id="toc-11">النوع الأول: الصحيح</span>` + "\n" +
		"(٢) إنَّ الصَّحيحَ مَا سَنَدُهُ اتَّصَلْ … بِلَا شُذُوذٍ وَ بضَابِطَيْنَ دَلّْ\n" +
		"<hr>\n" +
		"\n" +
		"(٢) بهذا البيت بدأ الشيخ الناظم بيان أنواع الحديث"

	got := readerutil.StructureMixedContent(content)

	if len(got.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2 (%+v)", len(got.Blocks), got.Blocks)
	}

	if got.Blocks[0].Type != readerutil.SourceBlockHeading || got.Blocks[0].AnchorID != 11 ||
		got.Blocks[0].Text != "النوع الأول: الصحيح" {
		t.Fatalf("anchor block = %+v", got.Blocks[0])
	}

	if got.Blocks[1].Type != readerutil.SourceBlockParagraph ||
		got.Blocks[1].Text != "(٢) إنَّ الصَّحيحَ مَا سَنَدُهُ اتَّصَلْ … بِلَا شُذُوذٍ وَ بضَابِطَيْنَ دَلّْ" {
		t.Fatalf("verse block = %+v", got.Blocks[1])
	}

	if len(got.Footnotes) != 1 {
		t.Fatalf("footnotes = %d, want 1 (%+v)", len(got.Footnotes), got.Footnotes)
	}

	if got.Footnotes[0].Marker != "(٢)" || got.Footnotes[0].Text != "بهذا البيت بدأ الشيخ الناظم بيان أنواع الحديث" {
		t.Fatalf("footnote = %+v", got.Footnotes[0])
	}
}

func TestStructureMixedContentMidLineTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    []struct {
			blockType string
			text      string
			anchorID  int
		}
		wantFootnotes int
	}{
		{
			name:    "anchor splits a line into before/heading/after",
			content: `نص قبل الفصل<span id="toc-7">فصل في الأدب</span>نص بعد الفصل`,
			want: []struct {
				blockType string
				text      string
				anchorID  int
			}{
				{readerutil.SourceBlockParagraph, "نص قبل الفصل", 0},
				{readerutil.SourceBlockHeading, "فصل في الأدب", 7},
				{readerutil.SourceBlockParagraph, "نص بعد الفصل", 0},
			},
		},
		{
			name:    "hr on the body line opens the footnote region",
			content: "آخر المتن في الصفحة<hr>(١) حاشية على السطر نفسه",
			want: []struct {
				blockType string
				text      string
				anchorID  int
			}{
				{readerutil.SourceBlockParagraph, "آخر المتن في الصفحة", 0},
			},
			wantFootnotes: 1,
		},
		{
			name:    "formatting-only markup is stripped, line stays one paragraph",
			content: `<span class="red">كلمة</span> عادية في السياق العام للفقرة`,
			want: []struct {
				blockType string
				text      string
				anchorID  int
			}{
				{readerutil.SourceBlockParagraph, "كلمة عادية في السياق العام للفقرة", 0},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := readerutil.StructureMixedContent(tc.content)
			if len(got.Blocks) != len(tc.want) {
				t.Fatalf("blocks = %d, want %d (%+v)", len(got.Blocks), len(tc.want), got.Blocks)
			}

			for i, want := range tc.want {
				if got.Blocks[i].Type != want.blockType || got.Blocks[i].Text != want.text ||
					got.Blocks[i].AnchorID != want.anchorID {
					t.Fatalf("block[%d] = %+v, want %+v", i, got.Blocks[i], want)
				}
			}

			if len(got.Footnotes) != tc.wantFootnotes {
				t.Fatalf("footnotes = %d, want %d", len(got.Footnotes), tc.wantFootnotes)
			}
		})
	}
}

func TestStructureMixedContentDeterministic(t *testing.T) {
	t.Parallel()

	content := `<span data-type="title" id="toc-3">باب</span>` + "\n" +
		"فقرة أولى (¬١)\nفقرة ثانية\n<hr>\n(¬١) حاشية\n\n(¬٢) أخرى"

	first := readerutil.StructureMixedContent(content)

	second := readerutil.StructureMixedContent(content)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("StructureMixedContent is not deterministic:\nfirst  = %+v\nsecond = %+v", first, second)
	}
}
