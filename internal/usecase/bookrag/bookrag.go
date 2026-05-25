package bookrag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
)

const (
	defaultMaxCitations = 5
	maxCitationsLimit   = 10
	defaultCandidateCap = 10
	sourceTextLimit     = 4000
	translationLimit    = 1200
)

var citationMarkerRE = regexp.MustCompile(`\[(\d+)\]`)

// LLMClient is the minimal chat-completion interface needed by book RAG.
type LLMClient interface {
	Complete(ctx context.Context, messages []entity.RAGChatMessage) (string, error)
	Stream(ctx context.Context, messages []entity.RAGChatMessage, emit func(delta string) error) error
}

// Options configures the book RAG usecase.
type Options struct {
	MaxContextPages int
}

// UseCase provides PageIndex-like book RAG.
type UseCase struct {
	repo            repo.BookRAGRepo
	llm             LLMClient
	maxContextPages int
}

// New creates a book RAG usecase.
func New(r repo.BookRAGRepo, llm LLMClient, opts Options) *UseCase {
	maxContextPages := opts.MaxContextPages
	if maxContextPages <= 0 {
		maxContextPages = 8
	}

	return &UseCase{
		repo:            r,
		llm:             llm,
		maxContextPages: maxContextPages,
	}
}

// AskBook answers a question against one published book.
func (uc *UseCase) AskBook(
	ctx context.Context,
	bookID int,
	question string,
	lang string,
	maxCitations int,
	includeTrace bool,
) (entity.BookRAGResponse, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return entity.BookRAGResponse{}, entity.ErrInvalidQuestion
	}

	lang = normalizeLang(lang)
	maxCitations = clampMaxCitations(maxCitations)

	doc, err := uc.repo.GetRAGBookDocument(ctx, bookID, lang)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}

	structure, err := uc.repo.ListRAGStructure(ctx, bookID, lang)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}
	if len(structure) == 0 {
		return entity.BookRAGResponse{}, entity.ErrRAGEvidenceNotFound
	}

	treeSelection, err := uc.selectTreeNodes(ctx, doc, question, structure)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}

	searchResults, err := uc.searchRAGPages(ctx, bookID, question, lang, defaultCandidateCap)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}

	headingIDs, lexicalHeadingIDs := mergeCandidateHeadings(structure, treeSelection.HeadingIDs, searchResults, defaultCandidateCap)
	focusPageIDs := focusPagesForHeadings(searchResults, headingIDs)
	if len(headingIDs) == 0 {
		return entity.BookRAGResponse{}, entity.ErrRAGEvidenceNotFound
	}

	sources, err := uc.repo.GetRAGPageSources(ctx, bookID, headingIDs, focusPageIDs, lang, uc.maxContextPages)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}
	if len(sources) == 0 {
		return entity.BookRAGResponse{}, entity.ErrRAGEvidenceNotFound
	}

	assignSourceRefs(sources)

	answer, citations, repaired, err := uc.answerWithValidatedCitations(ctx, question, sources, maxCitations)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}

	var trace *entity.BookRAGTrace
	if includeTrace {
		trace = &entity.BookRAGTrace{
			TreeReasoning:      treeSelection.Reasoning,
			SelectedHeadingIDs: treeSelection.HeadingIDs,
			LexicalHeadingIDs:  lexicalHeadingIDs,
			FocusPageIDs:       focusPageIDs,
			SourceRefs:         sourceRefs(sources),
			Repaired:           repaired,
		}
	}

	return entity.BookRAGResponse{
		BookID:    bookID,
		Question:  question,
		Answer:    answer,
		Citations: citations,
		Trace:     trace,
	}, nil
}

// AskBookStream emits a validated answer over app-level stream events.
func (uc *UseCase) AskBookStream(
	ctx context.Context,
	bookID int,
	question string,
	lang string,
	maxCitations int,
	includeTrace bool,
	emit func(event string, payload any) error,
) error {
	if err := emit("meta", map[string]any{"book_id": bookID, "question": strings.TrimSpace(question)}); err != nil {
		return err
	}

	response, err := uc.AskBook(ctx, bookID, question, lang, maxCitations, includeTrace)
	if err != nil {
		_ = emit("error", map[string]any{"error": publicErrorMessage(err)})
		return err
	}

	for _, chunk := range splitAnswerChunks(response.Answer, 120) {
		if err = emit("delta", map[string]string{"text": chunk}); err != nil {
			return err
		}
	}
	if err = emit("citations", response.Citations); err != nil {
		return err
	}

	return emit("done", response)
}

type treeSelectionResult struct {
	Reasoning  string
	HeadingIDs []int
}

func (uc *UseCase) selectTreeNodes(
	ctx context.Context,
	doc entity.RAGBookDocument,
	question string,
	structure []entity.RAGStructureNode,
) (treeSelectionResult, error) {
	tree := buildCompactTOCTree(structure)
	treeJSON, err := json.Marshal(tree)
	if err != nil {
		return treeSelectionResult{}, fmt.Errorf("BookRAG - selectTreeNodes - Marshal tree: %w", err)
	}

	messages := []entity.RAGChatMessage{
		{
			Role: "system",
			Content: `You are a PageIndex-style retrieval planner for a classical Islamic book.
Choose the TOC nodes most likely to contain evidence for the user's question.
Return strict JSON only: {"thinking":"short reason","node_ids":[11,12]}. Use heading IDs as node_ids.`,
		},
		{
			Role: "user",
			Content: fmt.Sprintf(
				"Book: %s\nQuestion: %s\nCompact TOC tree JSON:\n%s",
				doc.Title,
				question,
				string(treeJSON),
			),
		},
	}

	raw, err := uc.llm.Complete(ctx, messages)
	if err != nil {
		return treeSelectionResult{}, err
	}

	selection := parseTreeSelection(raw)
	if len(selection.HeadingIDs) > 0 {
		return selection, nil
	}

	retryRaw, err := uc.llm.Complete(ctx, retryTreeSelectionMessages(doc, question, structure, raw))
	if err != nil {
		return selection, nil
	}

	retrySelection := parseTreeSelection(retryRaw)
	if len(retrySelection.HeadingIDs) == 0 {
		return selection, nil
	}
	if selection.Reasoning != "" {
		retrySelection.Reasoning = selection.Reasoning + "\nRetry: " + retrySelection.Reasoning
	}

	return retrySelection, nil
}

func retryTreeSelectionMessages(
	doc entity.RAGBookDocument,
	question string,
	structure []entity.RAGStructureNode,
	previous string,
) []entity.RAGChatMessage {
	var flat strings.Builder
	for _, node := range structure {
		flat.WriteString(fmt.Sprintf(
			"- id=%d depth=%d pages=%d-%d title=%s\n",
			node.HeadingID,
			node.Depth,
			node.StartPageID,
			node.EndPageID,
			node.Title,
		))
	}

	return []entity.RAGChatMessage{
		{
			Role: "system",
			Content: `Return strict JSON only: {"thinking":"short reason","node_ids":[11]}.
Choose 1-5 heading IDs from the provided flat TOC. Do not return an empty list if any heading is plausibly relevant.`,
		},
		{
			Role: "user",
			Content: fmt.Sprintf(
				"Book: %s\nQuestion: %s\nPrevious unusable response: %s\nFlat TOC:\n%s",
				doc.Title,
				question,
				previous,
				flat.String(),
			),
		},
	}
}

func (uc *UseCase) answerWithValidatedCitations(
	ctx context.Context,
	question string,
	sources []entity.RAGPageSource,
	maxCitations int,
) (string, []entity.BookRAGCitation, bool, error) {
	messages := answerMessages(question, sources, "")
	raw, err := uc.llm.Complete(ctx, messages)
	if err != nil {
		return "", nil, false, err
	}

	answer, citations, ok := parseAndValidateAnswer(raw, sources, maxCitations)
	if ok {
		return answer, citations, false, nil
	}

	repairMessages := answerMessages(question, sources, raw)
	repairedRaw, err := uc.llm.Complete(ctx, repairMessages)
	if err != nil {
		return "", nil, true, err
	}

	answer, citations, ok = parseAndValidateAnswer(repairedRaw, sources, maxCitations)
	if ok {
		return answer, citations, true, nil
	}

	return notFoundAnswer(question), []entity.BookRAGCitation{}, true, nil
}

func (uc *UseCase) searchRAGPages(
	ctx context.Context,
	bookID int,
	question string,
	lang string,
	limit int,
) ([]entity.RAGSearchResult, error) {
	results := make([]entity.RAGSearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, query := range retrievalQueries(question) {
		queryResults, err := uc.repo.SearchRAGPages(ctx, bookID, query, lang, limit)
		if err != nil {
			return nil, err
		}
		for _, result := range queryResults {
			key := fmt.Sprintf("%d/%d", result.HeadingID, result.PageID)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			results = append(results, result)
			if len(results) >= limit {
				return results, nil
			}
		}
	}

	return results, nil
}

func retrievalQueries(question string) []string {
	normalized := strings.ToLower(strings.TrimSpace(question))
	queries := []string{question}
	add := func(query string) {
		query = strings.TrimSpace(query)
		if query == "" {
			return
		}
		for _, existing := range queries {
			if existing == query {
				return
			}
		}
		queries = append(queries, query)
	}

	expansions := map[string][]string{
		"sahih":  {"الصحيح", "حديث صحيح"},
		"shahih": {"الصحيح", "حديث صحيح"},
		"ṣaḥīḥ":  {"الصحيح", "حديث صحيح"},
		"hasan":  {"الحسن", "حديث حسن"},
		"dhaif":  {"الضعيف", "حديث ضعيف"},
		"daif":   {"الضعيف", "حديث ضعيف"},
		"ضعيف":   {"الضعيف", "حديث ضعيف"},
		"mursal": {"المرسل"},
		"marfu":  {"المرفوع"},
		"mawquf": {"الموقوف"},
	}
	for token, values := range expansions {
		if strings.Contains(normalized, token) {
			for _, value := range values {
				add(value)
			}
		}
	}

	return queries
}

func answerMessages(question string, sources []entity.RAGPageSource, invalidAnswer string) []entity.RAGChatMessage {
	system := `You answer questions about one classical Islamic book.
Use only the SOURCE BLOCKS. Do not use outside knowledge.
Answer in the same language as the user's question.
Every factual claim must include citation markers like [1].
Each citation quote must be copied exactly from the Arabic source text in the matching source block.
If the sources do not contain enough evidence, say that the answer is not found in the provided sources.
Return strict JSON only:
{"answer":"... [1]","citations":[{"ref":"1","quote":"exact Arabic quote from source 1"}]}`
	if invalidAnswer != "" {
		system += "\nYou are repairing an invalid previous answer. Keep only citations whose quote appears exactly in the matching Arabic source."
	}

	var user strings.Builder
	user.WriteString("Question:\n")
	user.WriteString(question)
	user.WriteString("\n\nSOURCE BLOCKS:\n")
	for _, source := range sources {
		user.WriteString(formatSourceBlock(source))
		user.WriteString("\n")
	}
	if invalidAnswer != "" {
		user.WriteString("\nInvalid previous answer:\n")
		user.WriteString(invalidAnswer)
	}

	return []entity.RAGChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user.String()},
	}
}

func formatSourceBlock(source entity.RAGPageSource) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf(
		"[%s] heading_id=%d title=%q page_id=%d printed_page=%s part=%s\nArabic source:\n%s\n",
		source.Ref,
		source.HeadingID,
		source.HeadingTitle,
		source.PageID,
		nullableStringValue(source.PrintedPage),
		nullableStringValue(source.Part),
		clipText(source.ContentText, sourceTextLimit),
	))
	if source.TranslationText != nil && source.PageID == source.StartPageID {
		builder.WriteString("Translation aid:\n")
		builder.WriteString(clipText(*source.TranslationText, translationLimit))
		builder.WriteByte('\n')
	}

	return builder.String()
}

type compactTOCNode struct {
	ID       int              `json:"id"`
	Title    string           `json:"title"`
	Pages    string           `json:"pages"`
	Children []compactTOCNode `json:"children,omitempty"`
}

func buildCompactTOCTree(entries []entity.RAGStructureNode) []compactTOCNode {
	byID := make(map[int]entity.RAGStructureNode, len(entries))
	childrenByParent := make(map[int][]int, len(entries))
	rootIDs := make([]int, 0)

	for _, entry := range entries {
		byID[entry.HeadingID] = entry
	}
	for _, entry := range entries {
		if entry.ParentID != nil {
			if _, ok := byID[*entry.ParentID]; ok {
				childrenByParent[*entry.ParentID] = append(childrenByParent[*entry.ParentID], entry.HeadingID)
				continue
			}
		}
		rootIDs = append(rootIDs, entry.HeadingID)
	}

	var build func(id int) compactTOCNode
	build = func(id int) compactTOCNode {
		entry := byID[id]
		node := compactTOCNode{
			ID:    entry.HeadingID,
			Title: entry.Title,
			Pages: fmt.Sprintf("%d-%d", entry.StartPageID, entry.EndPageID),
		}
		for _, childID := range childrenByParent[id] {
			node.Children = append(node.Children, build(childID))
		}

		return node
	}

	tree := make([]compactTOCNode, 0, len(rootIDs))
	for _, rootID := range rootIDs {
		tree = append(tree, build(rootID))
	}

	return tree
}

func parseTreeSelection(raw string) treeSelectionResult {
	var data map[string]any
	if err := decodeLooseJSON(raw, &data); err != nil {
		return treeSelectionResult{Reasoning: strings.TrimSpace(raw)}
	}

	ids := extractIntSlice(data["node_ids"])
	if len(ids) == 0 {
		ids = extractIntSlice(data["heading_ids"])
	}

	return treeSelectionResult{
		Reasoning:  extractString(data["thinking"]),
		HeadingIDs: uniquePositiveInts(ids),
	}
}

func mergeCandidateHeadings(
	structure []entity.RAGStructureNode,
	treeHeadingIDs []int,
	searchResults []entity.RAGSearchResult,
	limit int,
) ([]int, []int) {
	if limit <= 0 {
		limit = defaultCandidateCap
	}

	known := make(map[int]struct{}, len(structure))
	for _, node := range structure {
		known[node.HeadingID] = struct{}{}
	}

	merged := make([]int, 0, limit)
	seen := make(map[int]struct{}, limit)
	add := func(id int) {
		if len(merged) >= limit || id <= 0 {
			return
		}
		if _, ok := known[id]; !ok {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		merged = append(merged, id)
	}

	for _, id := range treeHeadingIDs {
		add(id)
	}

	lexicalIDs := make([]int, 0)
	for _, result := range searchResults {
		if _, ok := known[result.HeadingID]; ok {
			lexicalIDs = append(lexicalIDs, result.HeadingID)
		}
		add(result.HeadingID)
	}

	return merged, uniquePositiveInts(lexicalIDs)
}

func focusPagesForHeadings(searchResults []entity.RAGSearchResult, headingIDs []int) []int {
	selected := make(map[int]struct{}, len(headingIDs))
	for _, id := range headingIDs {
		selected[id] = struct{}{}
	}

	pages := make([]int, 0)
	seen := make(map[int]struct{})
	for _, result := range searchResults {
		if _, ok := selected[result.HeadingID]; !ok {
			continue
		}
		if _, ok := seen[result.PageID]; ok {
			continue
		}
		seen[result.PageID] = struct{}{}
		pages = append(pages, result.PageID)
	}

	return pages
}

func assignSourceRefs(sources []entity.RAGPageSource) {
	for i := range sources {
		sources[i].Ref = strconv.Itoa(i + 1)
	}
}

type answerCompletion struct {
	Answer    string
	Citations []citationDraft
}

type citationDraft struct {
	Ref   string
	Quote string
}

func parseAndValidateAnswer(
	raw string,
	sources []entity.RAGPageSource,
	maxCitations int,
) (string, []entity.BookRAGCitation, bool) {
	completion, err := parseAnswerCompletion(raw)
	if err != nil || strings.TrimSpace(completion.Answer) == "" {
		return "", nil, false
	}

	sourceByRef := make(map[string]entity.RAGPageSource, len(sources))
	for _, source := range sources {
		sourceByRef[source.Ref] = source
	}

	markers := extractCitationMarkers(completion.Answer)
	if len(markers) == 0 {
		if answerSaysNotFound(completion.Answer) {
			return completion.Answer, []entity.BookRAGCitation{}, true
		}

		return "", nil, false
	}

	citationByRef := make(map[string]citationDraft, len(completion.Citations))
	for _, citation := range completion.Citations {
		ref := normalizeRef(citation.Ref)
		if ref != "" {
			citation.Ref = ref
			citationByRef[ref] = citation
		}
	}

	citations := make([]entity.BookRAGCitation, 0, len(markers))
	for _, ref := range markers {
		source, ok := sourceByRef[ref]
		if !ok {
			return "", nil, false
		}
		draft, ok := citationByRef[ref]
		if !ok || strings.TrimSpace(draft.Quote) == "" {
			return "", nil, false
		}
		if !containsNormalized(source.ContentText, draft.Quote) {
			return "", nil, false
		}

		citations = append(citations, entity.BookRAGCitation{
			Ref:          ref,
			BookID:       source.BookID,
			HeadingID:    source.HeadingID,
			HeadingTitle: source.HeadingTitle,
			PageID:       source.PageID,
			PrintedPage:  source.PrintedPage,
			Part:         source.Part,
			Anchor:       source.Anchor,
			Quote:        strings.TrimSpace(draft.Quote),
			URL:          source.URL,
		})
		if len(citations) >= maxCitations {
			break
		}
	}

	return completion.Answer, citations, true
}

func parseAnswerCompletion(raw string) (answerCompletion, error) {
	var data struct {
		Answer    string `json:"answer"`
		Citations []struct {
			Ref   string `json:"ref"`
			Quote string `json:"quote"`
		} `json:"citations"`
	}
	if err := decodeLooseJSON(raw, &data); err != nil {
		return answerCompletion{}, err
	}

	result := answerCompletion{
		Answer:    strings.TrimSpace(data.Answer),
		Citations: make([]citationDraft, 0, len(data.Citations)),
	}
	for _, citation := range data.Citations {
		result.Citations = append(result.Citations, citationDraft{
			Ref:   citation.Ref,
			Quote: citation.Quote,
		})
	}

	return result, nil
}

func decodeLooseJSON(raw string, target any) error {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		raw = raw[start : end+1]
	}

	return json.Unmarshal([]byte(raw), target)
}

func extractIntSlice(value any) []int {
	items, ok := value.([]any)
	if !ok {
		return nil
	}

	result := make([]int, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case float64:
			result = append(result, int(typed))
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
				result = append(result, parsed)
			}
		}
	}

	return result
}

func extractString(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}

	return ""
}

func extractCitationMarkers(answer string) []string {
	matches := citationMarkerRE.FindAllStringSubmatch(answer, -1)
	refs := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		ref := match[1]
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}

	return refs
}

func containsNormalized(source string, quote string) bool {
	source = normalizeEvidenceText(source)
	quote = normalizeEvidenceText(quote)
	if quote == "" {
		return false
	}

	return strings.Contains(source, quote)
}

func normalizeEvidenceText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizeRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "[")
	ref = strings.TrimSuffix(ref, "]")
	if ref == "" {
		return ""
	}
	if _, err := strconv.Atoi(ref); err != nil {
		return ""
	}

	return ref
}

func uniquePositiveInts(values []int) []int {
	result := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}

func sourceRefs(sources []entity.RAGPageSource) []string {
	refs := make([]string, 0, len(sources))
	for _, source := range sources {
		refs = append(refs, source.Ref)
	}

	return refs
}

func splitAnswerChunks(answer string, maxRunes int) []string {
	if maxRunes <= 0 {
		return []string{answer}
	}

	runes := []rune(answer)
	if len(runes) <= maxRunes {
		return []string{answer}
	}

	chunks := make([]string, 0, (len(runes)/maxRunes)+1)
	for start := 0; start < len(runes); start += maxRunes {
		end := start + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}

	return chunks
}

func clipText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return value
	}

	return string(runes[:maxRunes]) + "\n[truncated]"
}

func nullableStringValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func normalizeLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return "id"
	}

	return lang
}

func clampMaxCitations(maxCitations int) int {
	if maxCitations <= 0 {
		return defaultMaxCitations
	}
	if maxCitations > maxCitationsLimit {
		return maxCitationsLimit
	}

	return maxCitations
}

func publicErrorMessage(err error) string {
	switch {
	case errors.Is(err, entity.ErrInvalidQuestion):
		return "invalid question"
	case errors.Is(err, entity.ErrBookNotFound):
		return "book not found"
	case errors.Is(err, entity.ErrRAGNotConfigured):
		return "rag llm is not configured"
	case errors.Is(err, entity.ErrRAGEvidenceNotFound):
		return "rag evidence not found"
	default:
		return "internal server error"
	}
}

func notFoundAnswer(question string) string {
	if containsArabic(question) {
		return "لم أجد جوابا موثقا في المصادر المتاحة."
	}
	if looksEnglish(question) {
		return "I could not find a well-supported answer in the provided sources."
	}

	return "Saya belum menemukan jawaban yang cukup didukung oleh sumber yang tersedia."
}

func containsArabic(value string) bool {
	for _, r := range value {
		if r >= '\u0600' && r <= '\u06ff' {
			return true
		}
	}

	return false
}

func looksEnglish(value string) bool {
	lower := strings.ToLower(value)
	englishWords := []string{"what", "why", "how", "when", "where", "explain", "define"}
	for _, word := range englishWords {
		if strings.Contains(lower, word) {
			return true
		}
	}

	return false
}

func answerSaysNotFound(answer string) bool {
	lower := strings.ToLower(answer)
	phrases := []string{
		"tidak ditemukan",
		"belum menemukan",
		"not found",
		"could not find",
		"not contain",
		"لم أجد",
		"لا أجد",
	}
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}
