package readerutil

import (
	"bytes"
	stdhtml "html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// SourceFormatMixed marks content structured by StructureMixedContent.
const SourceFormatMixed = "mixed"

const htmlBreakTag = "br"

// Shamela's footnote separator in HTML form (raw sources write <hr><s0>;
// <s0> is dropped by SanitizeHTML at import).
var _hrTagRE = regexp.MustCompile(`(?i)<hr\b[^>]*/?>`)

type mixedWalker struct {
	blocks          []SourceBlock
	footnotes       []SourceFootnote
	currentFootnote *SourceFootnote
	inFootnotes     bool
}

// StructureMixedContent is the deterministic, tolerant kitab parser used by
// the Citable Unit deriver. The sanitized fragment is parsed into a DOM first,
// so nested/malformed markup is balanced by the HTML5 parser. Block elements,
// line breaks, toc anchors, and footnote separators then form explicit
// boundaries; inline markup remains attached to the emitted unit.
//
// StructuredContent.Text is the canonical source document for unit spans.
// SourceCharStart/End are assigned while constructing it and therefore never
// depend on ambiguous substring search (important for repeated Arabic quotes).
//
//nolint:wsl_v5 // staged parser setup is clearer kept adjacent
func StructureMixedContent(content string) StructuredContent {
	sanitized := SanitizeHTML(content)
	contextNode := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(sanitized), contextNode)
	if err != nil {
		// ParseFragment is deliberately very tolerant; retain a deterministic
		// no-loss fallback for the theoretical tokenizer error.
		return structureMixedFallback(sanitized)
	}

	w := mixedWalker{}
	w.walkNodes(nodes)
	w.flushFootnote()

	text := stampMixedSourceSpans(w.blocks, w.footnotes)

	return StructuredContent{
		Format:    SourceFormatMixed,
		HTML:      semanticHTML(w.blocks, w.footnotes),
		Text:      text,
		Blocks:    w.blocks,
		Footnotes: w.footnotes,
	}
}

func structureMixedFallback(sanitized string) StructuredContent {
	// Preserve the old line machine only as an error fallback. Replacing <hr>
	// with the plain separator retains footnotes even when a fragment cannot
	// be parsed, while PlainText guarantees no visible text is discarded.
	text := PlainText(_hrTagRE.ReplaceAllString(sanitized, "\n__________\n"))
	blocks, footnotes := structurePlainText(text)
	canonical := stampMixedSourceSpans(blocks, footnotes)

	return StructuredContent{
		Format:    SourceFormatMixed,
		HTML:      semanticHTML(blocks, footnotes),
		Text:      canonical,
		Blocks:    blocks,
		Footnotes: footnotes,
	}
}

//nolint:gocognit,gocyclo,cyclop,nestif // DOM token classification keeps nested structural boundaries explicit and auditable
func (w *mixedWalker) walkNodes(nodes []*html.Node) {
	inline := make([]*html.Node, 0)
	flushInline := func() {
		w.emitNodes(inline, false)
		inline = inline[:0]
	}

	for _, node := range nodes {
		if node.Type == html.ElementNode {
			if anchorID, ok := tocAnchorID(node); ok {
				flushInline()
				w.emitAnchor(anchorID, nodeText(node))

				continue
			}

			if isSemanticQuranNode(node) {
				flushInline()

				if w.inFootnotes && w.currentFootnote != nil {
					w.emitSemanticQuranFootnote(node)
				} else {
					w.emitSemanticQuranNode(node)
				}

				continue
			}

			tag := strings.ToLower(node.Data)
			switch tag {
			case "hr":
				flushInline()
				w.openFootnotes()

				continue
			case htmlBreakTag:
				flushInline()

				continue
			}

			if isBlockElement(tag) {
				flushInline()
				w.walkBlock(node)

				continue
			}

			// Inline formatting wrappers can contain a semantic Quran node or
			// toc boundary several levels down. Descend through the wrapper so
			// the safety boundary cannot be hidden by <strong>/<em>/<span>.
			if hasStructuralChild(childSlice(node)) {
				flushInline()
				w.walkNodes(childSlice(node))

				continue
			}
		}

		inline = append(inline, node)
	}

	flushInline()
}

func (w *mixedWalker) walkBlock(node *html.Node) {
	tag := strings.ToLower(node.Data)
	if tag == "pre" || tag == "table" {
		if containsSemanticQuranDescendant(node) {
			start := len(w.blocks)
			w.walkNodes(childSlice(node))

			for i := start; i < len(w.blocks); i++ {
				if w.blocks[i].Type == SourceBlockQuranQuote {
					continue
				}

				w.blocks[i].Type = SourceBlockHTML
				w.blocks[i].HTML = blockHTML(w.blocks[i])
			}

			return
		}

		w.emitNodes([]*html.Node{node}, true)

		return
	}

	children := childSlice(node)
	if !hasStructuralChild(children) {
		w.emitNodes([]*html.Node{node}, false)

		return
	}

	w.walkNodes(children)
}

func containsSemanticQuranDescendant(node *html.Node) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if isSemanticQuranNode(child) || containsSemanticQuranDescendant(child) {
			return true
		}
	}

	return false
}

func hasStructuralChild(nodes []*html.Node) bool {
	for _, node := range nodes {
		if node.Type != html.ElementNode {
			continue
		}

		if _, ok := tocAnchorID(node); ok {
			return true
		}

		if isSemanticQuranNode(node) {
			return true
		}

		tag := strings.ToLower(node.Data)
		if tag == "br" || tag == "hr" {
			return true
		}

		if isBlockElement(tag) {
			return true
		}

		if hasStructuralChild(childSlice(node)) {
			return true
		}
	}

	return false
}

func childSlice(node *html.Node) []*html.Node {
	children := make([]*html.Node, 0)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		children = append(children, child)
	}

	return children
}

//nolint:gocognit,gocyclo,cyclop,wsl_v5 // flat token classification keeps exact footnote and HTML boundary semantics together
func (w *mixedWalker) emitNodes(nodes []*html.Node, forceHTML bool) {
	if len(nodes) == 0 {
		return
	}

	rendered := renderNodes(nodes)
	text := nodeTextList(nodes)
	if rendered != "" {
		text = PlainText(rendered)
	}
	lines := strings.Split(_lineBreakRE.ReplaceAllString(text, "\n"), "\n")

	for _, rawLine := range lines {
		line := strings.TrimSpace(_spaceRE.ReplaceAllString(rawLine, " "))
		if line == "" {
			if w.inFootnotes && w.currentFootnote != nil && w.currentFootnote.Text != "" {
				w.currentFootnote.Text += "\n"
			}

			continue
		}

		if isFootnoteSeparator(line) {
			w.openFootnotes()

			continue
		}

		if w.inFootnotes {
			w.emitFootnoteLine(line)

			continue
		}

		blocks := sourceBlocksForMixedLine(line)
		for i := range blocks {
			if forceHTML && blocks[i].Type != SourceBlockQuranQuote {
				blocks[i].Type = SourceBlockHTML
				blocks[i].HTML = blockHTML(blocks[i])
			}
		}

		// Preserve formatting only when this DOM line maps to exactly one
		// unit. If an inline ayah was split out, retaining the whole rendered
		// node on either neighbor would smuggle the ayah back into an
		// eligible paragraph/html unit.
		if rendered != "" && len(lines) == 1 && len(blocks) == 1 {
			blocks[0].HTML = rendered
		}

		w.blocks = append(w.blocks, blocks...)
	}
}

// sourceBlocksForMixedLine isolates every explicit Shamela Quran citation in
// its own quran_quote block. Match indexes come from the original line (UTF-8
// byte indexes, as required by Go string slicing); source rune spans are
// stamped only after all blocks have been emitted. No normalization or fuzzy
// lookup participates in the split.
func sourceBlocksForMixedLine(line string) []SourceBlock {
	matches := _quranCitationRE.FindAllStringIndex(line, -1)
	if len(matches) == 0 {
		return []SourceBlock{sourceBlockForLine(line)}
	}

	blocks := make([]SourceBlock, 0, len(matches)*2+1)
	appendOrdinary := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}

		blocks = append(blocks, sourceBlockForLine(text))
	}

	cursor := 0
	for _, match := range matches {
		appendOrdinary(line[cursor:match[0]])

		quoteText := strings.TrimSpace(line[match[0]:match[1]])
		quote := sourceBlockForLine(quoteText)
		// The regex is the parser's explicit citation grammar. Assigning the
		// safety kind here avoids relying on heuristics such as "mostly quote".
		quote.Type = SourceBlockQuranQuote
		quote.HTML = blockHTML(quote)
		blocks = append(blocks, quote)

		cursor = match[1]
	}

	appendOrdinary(line[cursor:])

	return blocks
}

// IsolateExactQuranQuotes reapplies semantic Quran boundaries to a structured
// legacy plain-text document. It is used only after HTML/text alignment falls
// back: every supplied quote must match exactly (no normalization, fuzzy
// search, or model). It returns false if any safety snippet is absent so the
// caller can fail the larger page closed.
//
//nolint:gocognit,gocyclo,cyclop // exact block and footnote isolation deliberately share one all-or-nothing safety pass
func IsolateExactQuranQuotes(content *StructuredContent, quotes []string) bool {
	unique := uniqueExactQuranQuotes(quotes)
	if len(unique) == 0 {
		return true
	}

	found := make(map[string]bool, len(unique))
	blocks := make([]SourceBlock, 0, len(content.Blocks)+len(unique)*2)

	for _, block := range content.Blocks {
		ranges := exactQuranRanges(block.Text, unique, found)
		if len(ranges) == 0 || block.Type == SourceBlockQuranQuote {
			blocks = append(blocks, block)

			continue
		}

		cursor := 0
		for _, span := range ranges {
			appendOrdinarySplitBlock(&blocks, block.Type, block.Text[cursor:span.start])
			text := strings.TrimSpace(block.Text[span.start:span.end])
			quoteBlock := SourceBlock{
				Type:           SourceBlockQuranQuote,
				Text:           text,
				QuranCitations: quranCitations(text),
			}
			quoteBlock.HTML = blockHTML(quoteBlock)
			blocks = append(blocks, quoteBlock)
			cursor = span.end
		}

		appendOrdinarySplitBlock(&blocks, block.Type, block.Text[cursor:])
	}

	for i := range content.Footnotes {
		for _, quote := range unique {
			if strings.Contains(content.Footnotes[i].Text, quote) {
				appendFootnoteQuranQuote(&content.Footnotes[i], quote)
				found[quote] = true
			}
		}
	}

	content.Blocks = blocks
	content.Text = stampMixedSourceSpans(content.Blocks, content.Footnotes)
	content.HTML = semanticHTML(content.Blocks, content.Footnotes)

	for _, quote := range unique {
		if !found[quote] {
			return false
		}
	}

	return true
}

func uniqueExactQuranQuotes(quotes []string) []string {
	unique := make([]string, 0, len(quotes))
	seen := make(map[string]bool, len(quotes))

	for _, quote := range quotes {
		quote = strings.TrimSpace(quote)
		if quote == "" || seen[quote] {
			continue
		}

		seen[quote] = true
		unique = append(unique, quote)
	}

	// Prefer the longest boundary if semantic snippets overlap at the same
	// byte offset; normal source snippets are disjoint.
	sort.SliceStable(unique, func(i, j int) bool { return len(unique[i]) > len(unique[j]) })

	return unique
}

type exactQuranRange struct {
	start int
	end   int
}

func exactQuranRanges(text string, quotes []string, found map[string]bool) []exactQuranRange {
	candidates := make([]exactQuranRange, 0)

	for _, quote := range quotes {
		cursor := 0
		for cursor <= len(text)-len(quote) {
			relative := strings.Index(text[cursor:], quote)
			if relative < 0 {
				break
			}

			start := cursor + relative
			candidates = append(candidates, exactQuranRange{start: start, end: start + len(quote)})
			found[quote] = true
			cursor = start + len(quote)
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].start != candidates[j].start {
			return candidates[i].start < candidates[j].start
		}

		return candidates[i].end > candidates[j].end
	})

	out := make([]exactQuranRange, 0, len(candidates))
	for _, candidate := range candidates {
		if len(out) > 0 && candidate.start < out[len(out)-1].end {
			continue
		}

		out = append(out, candidate)
	}

	return out
}

func appendOrdinarySplitBlock(blocks *[]SourceBlock, originalType, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	block := sourceBlockForLine(text)
	if originalType == SourceBlockHTML {
		block.Type = SourceBlockHTML
		block.HTML = blockHTML(block)
	}

	*blocks = append(*blocks, block)
}

func (w *mixedWalker) emitSemanticQuranNode(node *html.Node) {
	rendered := renderNodes([]*html.Node{node})
	text := nodeText(node)
	lines := strings.Split(_lineBreakRE.ReplaceAllString(text, "\n"), "\n")

	for _, rawLine := range lines {
		line := strings.TrimSpace(_spaceRE.ReplaceAllString(rawLine, " "))
		if line == "" {
			continue
		}

		block := SourceBlock{
			Type:           SourceBlockQuranQuote,
			Text:           line,
			QuranCitations: quranCitations(line),
		}
		block.HTML = blockHTML(block)
		if rendered != "" && len(lines) == 1 {
			block.HTML = rendered
		}

		w.blocks = append(w.blocks, block)
	}
}

func (w *mixedWalker) emitSemanticQuranFootnote(node *html.Node) {
	text := nodeText(node)
	lines := strings.SplitSeq(_lineBreakRE.ReplaceAllString(text, "\n"), "\n")

	for rawLine := range lines {
		line := strings.TrimSpace(_spaceRE.ReplaceAllString(rawLine, " "))
		if line == "" {
			continue
		}

		if w.currentFootnote.Text != "" {
			w.currentFootnote.Text += "\n"
		}

		w.currentFootnote.Text += line
		appendFootnoteQuranQuote(w.currentFootnote, line)
	}
}

func isSemanticQuranNode(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}

	for _, attr := range node.Attr {
		if !strings.EqualFold(attr.Key, "data-type") {
			continue
		}

		value := strings.ToLower(strings.TrimSpace(attr.Val))

		value = strings.ReplaceAll(value, "_", "-")
		switch value {
		case "quran", "quran-ayah", "quran-quote", "ayah":
			return true
		}
	}

	return false
}

func (w *mixedWalker) emitFootnoteLine(line string) {
	matches := _footnoteStartRE.FindStringSubmatch(line)
	if len(matches) > 0 {
		w.flushFootnote()
		w.currentFootnote = &SourceFootnote{
			Marker: _footnoteRefRE.FindString(line),
			Text:   strings.TrimSpace(matches[1]),
		}

		return
	}

	if w.currentFootnote != nil {
		if w.currentFootnote.Text != "" {
			w.currentFootnote.Text += "\n"
		}

		w.currentFootnote.Text += line

		return
	}

	// An explicit separator can be followed by an unmarked continuation (for
	// example a footnote-of-footnote). Keep it in footnote document order with
	// an empty marker instead of moving it ahead of already parsed notes.
	w.currentFootnote = &SourceFootnote{Text: line}
}

func (w *mixedWalker) openFootnotes() {
	w.flushFootnote()
	w.inFootnotes = true
}

func (w *mixedWalker) flushFootnote() {
	if w.currentFootnote == nil {
		return
	}

	w.currentFootnote.Text = strings.TrimSpace(w.currentFootnote.Text)
	for _, match := range _quranCitationRE.FindAllString(w.currentFootnote.Text, -1) {
		appendFootnoteQuranQuote(w.currentFootnote, match)
	}

	w.currentFootnote.HTML = footnoteHTML(*w.currentFootnote)
	w.footnotes = append(w.footnotes, *w.currentFootnote)
	w.currentFootnote = nil
}

func (w *mixedWalker) emitAnchor(anchorID int, title string) {
	w.flushFootnote()
	w.inFootnotes = false

	text := strings.TrimSpace(_spaceRE.ReplaceAllString(title, " "))
	block := SourceBlock{Type: SourceBlockHeading, Text: text, AnchorID: anchorID}
	block.HTML = blockHTML(block)
	w.blocks = append(w.blocks, block)
}

func tocAnchorID(node *html.Node) (int, bool) {
	if node.Type != html.ElementNode {
		return 0, false
	}

	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, "id") && strings.HasPrefix(strings.ToLower(attr.Val), "toc-") {
			id, err := strconv.Atoi(attr.Val[len("toc-"):])
			if err == nil && id > 0 {
				return id, true
			}
		}
	}

	return 0, false
}

func nodeTextList(nodes []*html.Node) string {
	var out strings.Builder
	for _, node := range nodes {
		out.WriteString(nodeText(node))
	}

	return stdhtml.UnescapeString(out.String())
}

//nolint:wsl_v5 // recursive closure belongs next to its builder
func nodeText(node *html.Node) string {
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			out.WriteString(n.Data)
		case html.ElementNode:
			tag := strings.ToLower(n.Data)
			if tag == htmlBreakTag {
				out.WriteByte('\n')
			}
		case html.ErrorNode, html.DocumentNode, html.CommentNode, html.DoctypeNode, html.RawNode:
			// Comments/doctypes do not contribute visible source text.
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)

	return out.String()
}

func isBlockElement(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "dd", "div", "dl", "dt",
		"figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header",
		"li", "main", "nav", "ol", "p", "pre", "section", "table", "tbody", "td", "tfoot",
		"th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

func renderNodes(nodes []*html.Node) string {
	var out bytes.Buffer
	for _, node := range nodes {
		if err := html.Render(&out, node); err != nil {
			return ""
		}
	}

	return strings.TrimSpace(out.String())
}

// stampMixedSourceSpans constructs the exact canonical document and records
// spans as it appends text. Repeated strings are therefore unambiguous.
//
//nolint:wsl_v5 // rune cursor mutations intentionally stay next to their writes
func stampMixedSourceSpans(blocks []SourceBlock, footnotes []SourceFootnote) string {
	var out strings.Builder
	runePos := 0
	appendSeparator := func() {
		if out.Len() > 0 {
			out.WriteByte('\n')
			runePos++
		}
	}

	for i := range blocks {
		appendSeparator()
		blocks[i].SourceCharStart = runePos
		out.WriteString(blocks[i].Text)
		runePos += utf8.RuneCountInString(blocks[i].Text)
		blocks[i].SourceCharEnd = runePos
	}

	for i := range footnotes {
		appendSeparator()
		if footnotes[i].Marker != "" {
			out.WriteString(footnotes[i].Marker)
			runePos += utf8.RuneCountInString(footnotes[i].Marker)
			if footnotes[i].Text != "" {
				out.WriteByte(' ')
				runePos++
			}
		}

		footnotes[i].SourceCharStart = runePos
		out.WriteString(footnotes[i].Text)
		runePos += utf8.RuneCountInString(footnotes[i].Text)
		footnotes[i].SourceCharEnd = runePos
	}

	return out.String()
}

// AlignMixedSourceSpans projects already-tokenized blocks onto the persisted
// content_text document using exact rune equality and a forward-only cursor.
// It never normalizes, fuzzes, or asks a model; repeated quotes bind to their
// corresponding occurrence in document order. On any mismatch it leaves the
// canonical parser spans untouched and returns false.
//
//nolint:wsl_v5 // validation and cursor advance are one exact-match step
func AlignMixedSourceSpans(content *StructuredContent, sourceText string) bool {
	document := []rune(sourceText)
	cursor := 0
	starts := make([]int, len(content.Blocks))
	ends := make([]int, len(content.Blocks))

	for i := range content.Blocks {
		needle := []rune(content.Blocks[i].Text)
		start := runeIndexFrom(document, needle, cursor)
		if start < 0 {
			return false
		}
		starts[i], ends[i], cursor = start, start+len(needle), start+len(needle)
	}

	fnStarts := make([]int, len(content.Footnotes))
	fnEnds := make([]int, len(content.Footnotes))
	for i := range content.Footnotes {
		note := &content.Footnotes[i]
		if note.Marker != "" {
			marker := []rune(note.Marker)
			markerStart := runeIndexFrom(document, marker, cursor)
			if markerStart < 0 {
				return false
			}
			cursor = markerStart + len(marker)
		}
		if note.Text == "" {
			fnStarts[i], fnEnds[i] = cursor, cursor

			continue
		}

		needle := []rune(note.Text)
		start := runeIndexFrom(document, needle, cursor)
		if start < 0 {
			return false
		}
		fnStarts[i], fnEnds[i], cursor = start, start+len(needle), start+len(needle)
	}

	for i := range content.Blocks {
		content.Blocks[i].SourceCharStart = starts[i]
		content.Blocks[i].SourceCharEnd = ends[i]
	}
	for i := range content.Footnotes {
		content.Footnotes[i].SourceCharStart = fnStarts[i]
		content.Footnotes[i].SourceCharEnd = fnEnds[i]
	}
	content.Text = sourceText

	return true
}

//nolint:nlreturn,wsl_v5 // compact exact-match inner loop
func runeIndexFrom(haystack, needle []rune, from int) int {
	if len(needle) == 0 || from < 0 || from > len(haystack) {
		return -1
	}
	for i := from; i+len(needle) <= len(haystack); i++ {
		matched := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				matched = false
				break
			}
		}
		if matched {
			return i
		}
	}

	return -1
}
