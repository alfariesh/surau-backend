package readerutil

import (
	"fmt"
	stdhtml "html"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
)

var (
	_tagRE       = regexp.MustCompile(`<[^>]+>`)
	_spaceRE     = regexp.MustCompile(`[ \t\x{00a0}]+`)
	_lineBreakRE = regexp.MustCompile(`\r\n?`)
)

var (
	_footnoteRefRE   = regexp.MustCompile(`\((?:¬)?[0-9٠-٩۰-۹]+\)`)
	_footnoteStartRE = regexp.MustCompile(`^\((?:¬)?[0-9٠-٩۰-۹]+\)\s*(.*)$`)
	_quranCitationRE = regexp.MustCompile(`\{([^{}]{3,})\}\s*\[([^\[\]]{2,})\]`)
)

var (
	_allowedHTMLTags = map[string]struct{}{
		"a": {}, "b": {}, "blockquote": {}, "br": {}, "cite": {}, "code": {}, "dd": {}, "div": {},
		"dl": {}, "dt": {}, "em": {}, "h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {},
		"hr": {}, "i": {}, "li": {}, "ol": {}, "p": {}, "pre": {}, "small": {}, "span": {},
		"section": {}, "strong": {}, "sub": {}, "sup": {}, "table": {}, "tbody": {}, "td": {}, "tfoot": {},
		"th": {}, "thead": {}, "tr": {}, "u": {}, "ul": {},
	}
	_voidHTMLTags = map[string]struct{}{
		"br": {}, "hr": {},
	}
	_dropHTMLContentTags = map[string]struct{}{
		"embed": {}, "iframe": {}, "math": {}, "noscript": {}, "object": {}, "script": {},
		"style": {}, "svg": {}, "template": {},
	}
	_allowedGlobalHTMLAttrs = map[string]struct{}{
		"class": {}, "data-marker": {}, "data-type": {}, "dir": {}, "id": {}, "lang": {}, "title": {},
	}
	_allowedHTMLAttrsByTag = map[string]map[string]struct{}{
		"a": {"href": {}, "name": {}},
	}
	_safeHTMLIDRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_:.-]*$`)
)

const (
	SourceFormatHTML      = "html"
	SourceFormatPlainText = "plain_text"

	SourceBlockParagraph  = "paragraph"
	SourceBlockHeading    = "heading"
	SourceBlockQuranQuote = "quran_quote"
	SourceBlockHTML       = "html"
)

// StructuredContent is a reader-safe rendering contract derived from raw source content.
type StructuredContent struct {
	Format    string
	HTML      string
	Text      string
	Blocks    []SourceBlock
	Footnotes []SourceFootnote
}

// SourceBlock is one displayable source content block.
type SourceBlock struct {
	Type           string
	Text           string
	HTML           string
	QuranCitations []SourceQuranCitation
}

// SourceFootnote is one extracted source footnote.
type SourceFootnote struct {
	Marker string
	Text   string
	HTML   string
}

// SourceQuranCitation is a best-effort citation found inline in plain source text.
type SourceQuranCitation struct {
	Quote     string
	Reference string
}

// ResolveBookDBPath returns the raw SQLite path for a book id.
func ResolveBookDBPath(sourceDir string, bookID int) string {
	bucket := bookID % 1000
	bucketName := fmt.Sprintf("%03d", bucket)

	return filepath.Join(sourceDir, "book", bucketName, fmt.Sprintf("%d.db", bookID))
}

// NormalizeContent removes known raw markers and returns sanitized HTML plus plain text.
func NormalizeContent(content string) (string, string) {
	normalized := strings.TrimPrefix(content, "\ufeff")
	normalized = strings.TrimPrefix(normalized, "舄")
	normalized = _lineBreakRE.ReplaceAllString(normalized, "\n")
	normalized = strings.TrimSpace(normalized)
	normalized = SanitizeHTML(normalized)

	return normalized, PlainText(normalized)
}

// StructureSourceContent returns semantic HTML plus structured blocks for reader source content.
func StructureSourceContent(content string) StructuredContent {
	sanitized := SanitizeHTML(content)
	text := PlainText(sanitized)
	if strings.TrimSpace(sanitized) == "" {
		return StructuredContent{
			Format:    SourceFormatPlainText,
			HTML:      "",
			Text:      "",
			Blocks:    []SourceBlock{},
			Footnotes: []SourceFootnote{},
		}
	}

	if _tagRE.MatchString(sanitized) {
		return StructuredContent{
			Format: SourceFormatHTML,
			HTML:   sanitized,
			Text:   text,
			Blocks: []SourceBlock{
				{
					Type: SourceBlockHTML,
					Text: text,
					HTML: sanitized,
				},
			},
			Footnotes: []SourceFootnote{},
		}
	}

	blocks, footnotes := structurePlainText(text)

	return StructuredContent{
		Format:    SourceFormatPlainText,
		HTML:      semanticHTML(blocks, footnotes),
		Text:      text,
		Blocks:    blocks,
		Footnotes: footnotes,
	}
}

// SanitizeHTML keeps reader-safe markup while stripping scripts, event handlers, and unsafe links.
func SanitizeHTML(content string) string {
	tokenizer := xhtml.NewTokenizer(strings.NewReader(content))
	var out strings.Builder
	skipTag := ""
	skipDepth := 0

	for {
		tokenType := tokenizer.Next()
		if tokenType == xhtml.ErrorToken {
			break
		}

		token := tokenizer.Token()
		tag := strings.ToLower(token.Data)

		if skipDepth > 0 {
			if tag == skipTag {
				switch tokenType {
				case xhtml.StartTagToken:
					skipDepth++
				case xhtml.EndTagToken:
					skipDepth--
					if skipDepth == 0 {
						skipTag = ""
					}
				}
			}
			continue
		}

		switch tokenType {
		case xhtml.TextToken:
			out.WriteString(stdhtml.EscapeString(token.Data))
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			if _, drop := _dropHTMLContentTags[tag]; drop {
				if tokenType == xhtml.StartTagToken {
					skipTag = tag
					skipDepth = 1
				}
				continue
			}
			if _, ok := _allowedHTMLTags[tag]; !ok {
				continue
			}

			writeHTMLStartTag(&out, tag, sanitizeHTMLAttrs(tag, token.Attr))
			if tokenType == xhtml.SelfClosingTagToken {
				if _, void := _voidHTMLTags[tag]; !void {
					writeHTMLEndTag(&out, tag)
				}
			}
		case xhtml.EndTagToken:
			if _, ok := _allowedHTMLTags[tag]; ok {
				if _, void := _voidHTMLTags[tag]; !void {
					writeHTMLEndTag(&out, tag)
				}
			}
		}
	}

	return strings.TrimSpace(out.String())
}

// PlainText strips simple HTML markup and normalizes whitespace for search/preview.
func PlainText(content string) string {
	text := _tagRE.ReplaceAllString(content, " ")
	text = stdhtml.UnescapeString(text)
	text = _lineBreakRE.ReplaceAllString(text, "\n")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(_spaceRE.ReplaceAllString(line, " "))
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func structurePlainText(text string) ([]SourceBlock, []SourceFootnote) {
	lines := strings.Split(_lineBreakRE.ReplaceAllString(text, "\n"), "\n")
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

	for _, rawLine := range lines {
		line := strings.TrimSpace(_spaceRE.ReplaceAllString(rawLine, " "))
		if line == "" {
			flushFootnote()
			inFootnotes = false
			continue
		}

		if isFootnoteSeparator(line) {
			flushFootnote()
			inFootnotes = true
			continue
		}

		if inFootnotes {
			matches := _footnoteStartRE.FindStringSubmatch(line)
			if len(matches) > 0 {
				flushFootnote()
				currentFootnote = &SourceFootnote{
					Marker: _footnoteRefRE.FindString(line),
					Text:   strings.TrimSpace(matches[1]),
				}
				continue
			}

			if currentFootnote != nil {
				if currentFootnote.Text != "" {
					currentFootnote.Text += "\n"
				}
				currentFootnote.Text += line
				continue
			}

			inFootnotes = false
		}

		blocks = append(blocks, sourceBlockForLine(line))
	}

	flushFootnote()

	return blocks, footnotes
}

func sourceBlockForLine(line string) SourceBlock {
	citations := quranCitations(line)
	blockType := SourceBlockParagraph
	if len(citations) > 0 && isMostlyQuranQuote(line) {
		blockType = SourceBlockQuranQuote
	} else if isLikelyHeadingLine(line) {
		blockType = SourceBlockHeading
	}

	block := SourceBlock{
		Type:           blockType,
		Text:           line,
		QuranCitations: citations,
	}
	block.HTML = blockHTML(block)

	return block
}

func quranCitations(line string) []SourceQuranCitation {
	matches := _quranCitationRE.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return nil
	}

	citations := make([]SourceQuranCitation, 0, len(matches))
	for _, match := range matches {
		citations = append(citations, SourceQuranCitation{
			Quote:     strings.TrimSpace(match[1]),
			Reference: strings.TrimSpace(match[2]),
		})
	}

	return citations
}

func isFootnoteSeparator(line string) bool {
	if len([]rune(line)) < 3 {
		return false
	}

	for _, r := range line {
		switch r {
		case '_', '-', 'ـ', '—', '=':
			continue
		default:
			return false
		}
	}

	return true
}

func isLikelyHeadingLine(line string) bool {
	if _footnoteRefRE.MatchString(line) || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "{") {
		return false
	}
	if strings.ContainsAny(line, ".،؛:؟!?") {
		return false
	}

	runeCount := len([]rune(line))
	if runeCount <= 2 || runeCount > 100 {
		return false
	}

	for _, prefix := range []string{
		"باب ",
		"فصل",
		"كتاب ",
		"المسألة",
		"المقدمة",
		"مقدمة",
		"الخاتمة",
		"خاتمة",
		"تمهيد",
		"النوع ",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}

	return false
}

func isMostlyQuranQuote(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}

	lastBrace := strings.LastIndex(trimmed, "}")
	if lastBrace < 0 {
		return false
	}

	afterQuote := strings.TrimSpace(trimmed[lastBrace+1:])

	return afterQuote == "" || strings.HasPrefix(afterQuote, "[")
}

func semanticHTML(blocks []SourceBlock, footnotes []SourceFootnote) string {
	var out strings.Builder
	for _, block := range blocks {
		if block.HTML == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(block.HTML)
	}

	if len(footnotes) > 0 {
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(`<section data-type="footnotes" dir="rtl" lang="ar">`)
		out.WriteString("\n<ol>")
		for _, footnote := range footnotes {
			out.WriteByte('\n')
			out.WriteString(footnote.HTML)
		}
		out.WriteString("\n</ol>\n</section>")
	}

	return out.String()
}

func blockHTML(block SourceBlock) string {
	content := escapeWithFootnoteRefs(block.Text)
	switch block.Type {
	case SourceBlockHeading:
		return `<h3 dir="rtl" lang="ar">` + content + `</h3>`
	case SourceBlockQuranQuote:
		return `<blockquote data-type="quran-quote" dir="rtl" lang="ar">` + content + `</blockquote>`
	default:
		return `<p dir="rtl" lang="ar">` + content + `</p>`
	}
}

func footnoteHTML(footnote SourceFootnote) string {
	content := stdhtml.EscapeString(footnote.Text)
	content = strings.ReplaceAll(content, "\n", "<br>")

	return `<li data-marker="` +
		stdhtml.EscapeString(footnote.Marker) +
		`"><span data-type="footnote-marker">` +
		stdhtml.EscapeString(footnote.Marker) +
		`</span> ` +
		content +
		`</li>`
}

func escapeWithFootnoteRefs(text string) string {
	matches := _footnoteRefRE.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return stdhtml.EscapeString(text)
	}

	var out strings.Builder
	last := 0
	for _, match := range matches {
		out.WriteString(stdhtml.EscapeString(text[last:match[0]]))
		out.WriteString(`<sup data-type="footnote-ref">`)
		out.WriteString(stdhtml.EscapeString(text[match[0]:match[1]]))
		out.WriteString(`</sup>`)
		last = match[1]
	}
	out.WriteString(stdhtml.EscapeString(text[last:]))

	return out.String()
}

func writeHTMLStartTag(out *strings.Builder, tag string, attrs []xhtml.Attribute) {
	out.WriteByte('<')
	out.WriteString(tag)
	for _, attr := range attrs {
		out.WriteByte(' ')
		out.WriteString(attr.Key)
		out.WriteString(`="`)
		out.WriteString(stdhtml.EscapeString(attr.Val))
		out.WriteByte('"')
	}
	out.WriteByte('>')
}

func writeHTMLEndTag(out *strings.Builder, tag string) {
	out.WriteString("</")
	out.WriteString(tag)
	out.WriteByte('>')
}

func sanitizeHTMLAttrs(tag string, attrs []xhtml.Attribute) []xhtml.Attribute {
	safeAttrs := make([]xhtml.Attribute, 0, len(attrs))
	for _, attr := range attrs {
		key := strings.ToLower(strings.TrimSpace(attr.Key))
		value := strings.TrimSpace(attr.Val)
		if key == "" || strings.HasPrefix(key, "on") || key == "style" {
			continue
		}
		if !isAllowedHTMLAttr(tag, key) {
			continue
		}
		if key == "id" && !_safeHTMLIDRE.MatchString(value) {
			continue
		}
		if key == "href" && !isSafeHTMLHref(value) {
			continue
		}
		if key == "dir" && value != "rtl" && value != "ltr" && value != "auto" {
			continue
		}

		safeAttrs = append(safeAttrs, xhtml.Attribute{Key: key, Val: value})
	}

	return safeAttrs
}

func isAllowedHTMLAttr(tag, key string) bool {
	if _, ok := _allowedGlobalHTMLAttrs[key]; ok {
		return true
	}
	if attrs, ok := _allowedHTMLAttrsByTag[tag]; ok {
		_, allowed := attrs[key]
		return allowed
	}

	return false
}

func isSafeHTMLHref(value string) bool {
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "#") || strings.HasPrefix(value, "/") {
		return true
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return true
	default:
		return false
	}
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
	return SliceSectionContent(content, startAnchor, endAnchor, "", "")
}

// SliceSectionContent extracts one section using HTML anchors, with a title fallback for plain-text sources.
func SliceSectionContent(content, startAnchor, endAnchor, startTitle, endTitle string) string {
	start := 0
	if startAnchor != "" {
		if idx := findAnchor(content, startAnchor); idx >= 0 {
			start = idx
		}
	}

	if start == 0 && startTitle != "" && !_tagRE.MatchString(content) {
		if idx := findPlainHeading(content, startTitle); idx > 0 && shouldTrimPlainPrefix(content[:idx]) {
			start = idx
		}
	}

	end := len(content)
	if endAnchor != "" {
		if idx := findAnchor(content[start:], endAnchor); idx >= 0 {
			end = start + idx
		}
	}

	if end == len(content) && endTitle != "" && !_tagRE.MatchString(content[start:]) {
		if idx := findPlainHeading(content[start:], endTitle); idx > 0 {
			end = start + idx
		}
	}

	if start > end {
		return strings.TrimSpace(content)
	}

	return strings.TrimSpace(content[start:end])
}

func findPlainHeading(content, title string) int {
	for _, needle := range headingNeedles(title) {
		if idx := strings.Index(content, needle); idx >= 0 {
			return idx
		}
	}

	return -1
}

func headingNeedles(title string) []string {
	title = strings.TrimSpace(PlainText(title))
	if title == "" {
		return nil
	}

	seen := make(map[string]struct{})
	needles := make([]string, 0, 4)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if len([]rune(value)) < 3 {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		needles = append(needles, value)
	}

	add(title)
	for _, sep := range []string{"[", "(", "،", ":", " - "} {
		if idx := strings.Index(title, sep); idx > 0 {
			add(title[:idx])
		}
	}

	fields := strings.Fields(title)
	for i := min(len(fields), 4); i >= 2; i-- {
		add(strings.Join(fields[:i], " "))
	}

	return needles
}

func shouldTrimPlainPrefix(prefix string) bool {
	text := PlainText(prefix)
	if text == "" {
		return false
	}

	return len([]rune(text)) > 80
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
