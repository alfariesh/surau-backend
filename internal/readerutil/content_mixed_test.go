package readerutil_test

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

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

//nolint:wsl_v5 // table-driven assertions intentionally share the parsed fixture
func TestStructureMixedContentDOMBlocksUnicodeSpansAndNoLoss(t *testing.T) {
	t.Parallel()

	content := `<div>مقدمة 😀<p><strong>{إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ}</strong> [الكوثر: 1]</p>` +
		`<ul><li>بند أول</li><li>بند ثان</li></ul><table><tr><td>خلية</td></tr></table><pre>سطر أ` + "\n" +
		`سطر ب</pre>خاتمة</div><p><em>وسم غير مغلق`

	got := readerutil.StructureMixedContent(content)
	if len(got.Blocks) < 8 {
		t.Fatalf("blocks = %d, want nested block granularity: %+v", len(got.Blocks), got.Blocks)
	}

	wantPieces := []string{"مقدمة 😀", "{إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ} [الكوثر: 1]", "بند أول", "بند ثان", "خلية", "سطر أ", "سطر ب", "خاتمة", "وسم غير مغلق"}
	for _, piece := range wantPieces {
		if !strings.Contains(got.Text, piece) {
			t.Fatalf("canonical text lost %q: %q", piece, got.Text)
		}
	}

	quranFound, htmlFound := false, false
	runes := []rune(got.Text)
	for _, block := range got.Blocks {
		if block.SourceCharStart < 0 || block.SourceCharEnd > len(runes) || block.SourceCharStart >= block.SourceCharEnd {
			t.Fatalf("invalid source span for %+v (document runes=%d)", block, len(runes))
		}
		if string(runes[block.SourceCharStart:block.SourceCharEnd]) != block.Text {
			t.Fatalf("span %d:%d = %q, want %q", block.SourceCharStart, block.SourceCharEnd,
				string(runes[block.SourceCharStart:block.SourceCharEnd]), block.Text)
		}
		quranFound = quranFound || block.Type == readerutil.SourceBlockQuranQuote
		htmlFound = htmlFound || block.Type == readerutil.SourceBlockHTML
	}
	if !quranFound || !htmlFound {
		t.Fatalf("quran/html classification missing: %+v", got.Blocks)
	}
	if utf8.RuneCountInString(got.Text) == len(got.Text) {
		t.Fatal("fixture must exercise multi-byte rune offsets")
	}
}

//nolint:wsl_v5 // safety assertions intentionally stay beside each emitted neighbor
func TestStructureMixedContentIsolatesInlineQuranQuoteWithExactRuneSpans(t *testing.T) {
	t.Parallel()

	source := "قال تعالى {إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ} [الكوثر:1] ثم شرح المعنى 😀"
	got := readerutil.StructureMixedContent(source)
	wantText := []string{
		"قال تعالى",
		"{إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ} [الكوثر:1]",
		"ثم شرح المعنى 😀",
	}
	wantKinds := []string{
		readerutil.SourceBlockParagraph,
		readerutil.SourceBlockQuranQuote,
		readerutil.SourceBlockParagraph,
	}
	if len(got.Blocks) != len(wantText) {
		t.Fatalf("blocks = %+v, want three isolated units", got.Blocks)
	}
	for i := range wantText {
		if got.Blocks[i].Type != wantKinds[i] || got.Blocks[i].Text != wantText[i] {
			t.Fatalf("block[%d] = %+v, want kind=%s text=%q", i, got.Blocks[i], wantKinds[i], wantText[i])
		}
	}
	if strings.Contains(got.Blocks[0].HTML, "أَعْطَيْنَاكَ") || strings.Contains(got.Blocks[2].HTML, "أَعْطَيْنَاكَ") {
		t.Fatalf("ayah leaked into eligible neighbor HTML: %+v", got.Blocks)
	}

	if !readerutil.AlignMixedSourceSpans(&got, source) {
		t.Fatal("inline split must align exactly to the persisted source document")
	}
	runes := []rune(source)
	for _, block := range got.Blocks {
		if string(runes[block.SourceCharStart:block.SourceCharEnd]) != block.Text {
			t.Fatalf("source[%d:%d] = %q, want %q", block.SourceCharStart, block.SourceCharEnd,
				string(runes[block.SourceCharStart:block.SourceCharEnd]), block.Text)
		}
	}
}

func TestStructureMixedContentRepeatedInlineQuranQuotesKeepDocumentOrder(t *testing.T) {
	t.Parallel()

	quote := "{قُلْ هُوَ اللَّهُ أَحَدٌ} [الإخلاص:1]"
	source := "أول " + quote + " بينهما " + quote + " آخر"

	got := readerutil.StructureMixedContent(source)
	if !readerutil.AlignMixedSourceSpans(&got, source) {
		t.Fatal("repeated inline quotes must align exactly")
	}

	quranStarts := make([]int, 0, 2)

	for _, block := range got.Blocks {
		if block.Type == readerutil.SourceBlockQuranQuote {
			quranStarts = append(quranStarts, block.SourceCharStart)
		}
	}

	if len(quranStarts) != 2 || quranStarts[0] >= quranStarts[1] {
		t.Fatalf("repeated quote spans collapsed or reordered: %+v", got.Blocks)
	}
}

//nolint:wsl_v5 // table documents distinct DOM shapes that must all fail closed
func TestStructureMixedContentSemanticQuranDOMCannotLeakIntoEligibleBlock(t *testing.T) {
	t.Parallel()

	const ayah = "إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ"
	cases := []struct {
		name    string
		content string
	}{
		{
			name:    "semantic blockquote without braces",
			content: `<p>مقدمة</p><blockquote data-type="quran-quote"><strong>` + ayah + `</strong></blockquote><p>شرح</p>`,
		},
		{
			name:    "semantic inline nested below formatting wrappers",
			content: `<p>قال <strong><span data-type="quran_quote"><em>` + ayah + `</em></span></strong> ثم شرح</p>`,
		},
		{
			name:    "malformed nested inline is balanced by DOM parser",
			content: `<div>قبل <span data-type="quran-ayah"><strong>` + ayah + `</span> بعد</strong></div>`,
		},
		{
			name:    "table keeps non Quran pieces as html",
			content: `<table><tr><td>قبل {` + ayah + `} [الكوثر:1] بعد</td></tr></table>`,
		},
		{
			name: "semantic Quran nested in table",
			content: `<table><tr><td>قبل <span data-type="quran-quote">` + ayah +
				`</span> بعد</td></tr></table>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := readerutil.StructureMixedContent(tc.content)
			quranBlocks := 0
			introOutsideQuran := false
			explanationOutsideQuran := false
			for _, block := range got.Blocks {
				containsAyah := strings.Contains(block.Text, ayah)
				if block.Type == readerutil.SourceBlockQuranQuote {
					quranBlocks++
					if !containsAyah {
						t.Fatalf("quran block lost ayah: %+v", block)
					}

					continue
				}
				if containsAyah || strings.Contains(block.HTML, ayah) {
					t.Fatalf("ayah leaked into eligible %s block: %+v", block.Type, block)
				}
				introOutsideQuran = introOutsideQuran || strings.Contains(block.Text, "قبل") ||
					strings.Contains(block.Text, "مقدمة") || strings.Contains(block.Text, "قال")
				explanationOutsideQuran = explanationOutsideQuran || strings.Contains(block.Text, "بعد") ||
					strings.Contains(block.Text, "شرح")
			}
			if quranBlocks != 1 {
				t.Fatalf("quran blocks = %d, want 1: %+v", quranBlocks, got.Blocks)
			}
			if !introOutsideQuran {
				t.Fatalf("introductory text was lost or absorbed by Quran block: %+v", got.Blocks)
			}
			if !explanationOutsideQuran {
				t.Fatalf("explanatory text was lost or absorbed by Quran block: %+v", got.Blocks)
			}
		})
	}
}

var mixedQuranCitationTestRE = regexp.MustCompile(`\{[^{}]{3,}\}\s*\[[^\[\]]{2,}\]`)

func FuzzStructureMixedContentQuranIsolation(f *testing.F) {
	f.Add("قال تعالى {إنا أعطيناك الكوثر} [الكوثر:1] ثم شرح")
	f.Add(`<div>قبل <strong>{قل هو الله أحد} [الإخلاص:1]</strong> بعد</div>`)
	f.Add(`<table><tr><td>قبل {الحمد لله رب العالمين} [الفاتحة:2] بعد</td></tr></table>`)

	f.Fuzz(func(t *testing.T, content string) {
		if !utf8.ValidString(content) {
			t.Skip("PostgreSQL source text is valid UTF-8")
		}

		got := readerutil.StructureMixedContent(content)
		runes := []rune(got.Text)

		for _, block := range got.Blocks {
			if block.SourceCharStart < 0 || block.SourceCharEnd < block.SourceCharStart ||
				block.SourceCharEnd > len(runes) {
				t.Fatalf("invalid rune span for %+v in %q", block, got.Text)
			}

			if string(runes[block.SourceCharStart:block.SourceCharEnd]) != block.Text {
				t.Fatalf("span does not reproduce block text: %+v", block)
			}

			if block.Type != readerutil.SourceBlockQuranQuote && mixedQuranCitationTestRE.MatchString(block.Text) {
				t.Fatalf("recognized Quran citation leaked into %s: %q", block.Type, block.Text)
			}
		}

		for _, note := range got.Footnotes {
			if mixedQuranCitationTestRE.MatchString(note.Text) && !note.ContainsQuran {
				t.Fatalf("recognized Quran citation remained in eligible footnote: %+v", note)
			}
		}
	})
}

//nolint:wsl_v5 // repeated-note assertions stay next to the shared rune document
func TestStructureMixedContentRepeatedFootnotesKeepDistinctSpans(t *testing.T) {
	t.Parallel()

	got := readerutil.StructureMixedContent("متن (١)\nمتن آخر (١)<hr>(١) الأولى\n(١) الثانية")
	if len(got.Footnotes) != 2 {
		t.Fatalf("footnotes = %+v", got.Footnotes)
	}
	runes := []rune(got.Text)
	for _, note := range got.Footnotes {
		if string(runes[note.SourceCharStart:note.SourceCharEnd]) != note.Text {
			t.Fatalf("footnote span = %q, want %q", string(runes[note.SourceCharStart:note.SourceCharEnd]), note.Text)
		}
	}
	if got.Footnotes[0].SourceCharStart == got.Footnotes[1].SourceCharStart {
		t.Fatal("duplicate markers must not collapse source spans")
	}
}

//nolint:wsl_v5 // fail-closed flag, marker and exact span form one safety contract
func TestStructureMixedContentQuranCitationFailsWholeFootnoteClosed(t *testing.T) {
	t.Parallel()

	source := "متن يحمل (١)<hr>(١) قال تعالى {إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ} [الكوثر:1] ثم شرح"
	got := readerutil.StructureMixedContent(source)
	if len(got.Footnotes) != 1 {
		t.Fatalf("footnotes = %+v", got.Footnotes)
	}
	note := got.Footnotes[0]
	if note.Marker != "(١)" || !note.ContainsQuran {
		t.Fatalf("Quran footnote marker/safety = %+v", note)
	}
	if note.Text != "قال تعالى {إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ} [الكوثر:1] ثم شرح" {
		t.Fatalf("footnote content lost or duplicated: %q", note.Text)
	}
	runes := []rune(got.Text)
	if string(runes[note.SourceCharStart:note.SourceCharEnd]) != note.Text {
		t.Fatalf("footnote span = %q, want %q",
			string(runes[note.SourceCharStart:note.SourceCharEnd]), note.Text)
	}
}

func TestStructureMixedContentSemanticQuranInsideFootnoteIsNotBodyHTML(t *testing.T) {
	t.Parallel()

	const ayah = "إِنَّا أَعْطَيْنَاكَ الْكَوْثَرَ"

	content := `متن يحمل (١)<hr>(١) مقدمة <blockquote data-type="quran-quote">` + ayah +
		`</blockquote> ثم شرح`

	got := readerutil.StructureMixedContent(content)

	if len(got.Footnotes) != 1 || !got.Footnotes[0].ContainsQuran {
		t.Fatalf("semantic Quran footnote must fail closed: %+v", got.Footnotes)
	}

	if got.Footnotes[0].Marker != "(١)" || !strings.Contains(got.Footnotes[0].Text, "مقدمة") ||
		!strings.Contains(got.Footnotes[0].Text, ayah) || !strings.Contains(got.Footnotes[0].Text, "شرح") {
		t.Fatalf("semantic footnote lost marker/content: %+v", got.Footnotes[0])
	}

	for _, block := range got.Blocks {
		if strings.Contains(block.Text, ayah) || strings.Contains(block.HTML, ayah) {
			t.Fatalf("footnote ayah leaked into body block: %+v", block)
		}
	}
}

//nolint:wsl_v5 // exact offsets are checked immediately after alignment
func TestAlignMixedSourceSpansUsesExactRuneDocumentOrder(t *testing.T) {
	t.Parallel()

	structured := readerutil.StructureMixedContent("قول 😀\nقول 😀<hr>(١) شرح 😀")
	document := "مقدمة\nقول 😀\nفاصل\nقول 😀\n__________\n(١) شرح 😀\nخاتمة"
	if !readerutil.AlignMixedSourceSpans(&structured, document) {
		t.Fatal("exact alignment failed")
	}
	runes := []rune(document)
	if structured.Blocks[0].SourceCharStart == structured.Blocks[1].SourceCharStart {
		t.Fatal("repeated quote collapsed to one occurrence")
	}
	for _, block := range structured.Blocks {
		if string(runes[block.SourceCharStart:block.SourceCharEnd]) != block.Text {
			t.Fatalf("block alignment = %q want %q", string(runes[block.SourceCharStart:block.SourceCharEnd]), block.Text)
		}
	}
	note := structured.Footnotes[0]
	if string(runes[note.SourceCharStart:note.SourceCharEnd]) != note.Text {
		t.Fatalf("note alignment = %q want %q", string(runes[note.SourceCharStart:note.SourceCharEnd]), note.Text)
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
