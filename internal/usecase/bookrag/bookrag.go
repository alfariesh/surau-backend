package bookrag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/readerlang"
	"github.com/alfariesh/surau-backend/internal/repo"
)

const (
	defaultMaxCitations = 5
	maxCitationsLimit   = 10
	defaultCandidateCap = 10
	sourceTextLimit     = 4000
	translationLimit    = 1200
	arabicSearchMarks   = "\u064b\u064c\u064d\u064e\u064f\u0650\u0651\u0652\u0653\u0654\u0655\u0670\u0640"
	fallbackQuoteLimit  = 420

	defaultTreeFullMaxNodes     = 450
	defaultTreeBlockMaxNodes    = 120
	defaultTreeBeamSize         = 3
	defaultTreeMaxTurns         = 6
	defaultTreeMaxBlocksPerTurn = 6
)

var (
	citationMarkerRE       = regexp.MustCompile(`\[(\d+)\]`)
	arabicTokenRE          = regexp.MustCompile("[\\p{Arabic}\u064b\u064c\u064d\u064e\u064f\u0650\u0651\u0652\u0653\u0654\u0655\u0670\u0640]+")
	looseNodeIDsRE         = regexp.MustCompile(`(?i)(?:node_ids|heading_ids)\s*:\s*\[([^\]]+)\]`)
	looseSelectionDoneRE   = regexp.MustCompile(`(?i)done\s*:\s*(true|false)`)
	looseSelectionIDItemRE = regexp.MustCompile(`\d+`)
)

// LLMClient is the minimal chat-completion interface needed by book RAG.
type LLMClient interface {
	Complete(ctx context.Context, messages []entity.RAGChatMessage) (string, error)
	Stream(ctx context.Context, messages []entity.RAGChatMessage, emit func(delta string) error) error
}

// Options configures the book RAG usecase.
type Options struct {
	MaxContextPages      int
	TreeFullMaxNodes     int
	TreeBlockMaxNodes    int
	TreeBeamSize         int
	TreeMaxTurns         int
	TreeMaxBlocksPerTurn int
}

// UseCase provides PageIndex-like book RAG.
type UseCase struct {
	repo                 repo.BookRAGRepo
	llm                  LLMClient
	maxContextPages      int
	treeFullMaxNodes     int
	treeBlockMaxNodes    int
	treeBeamSize         int
	treeMaxTurns         int
	treeMaxBlocksPerTurn int
}

// New creates a book RAG usecase.
func New(r repo.BookRAGRepo, llm LLMClient, opts Options) *UseCase {
	maxContextPages := opts.MaxContextPages
	if maxContextPages <= 0 {
		maxContextPages = 8
	}
	treeFullMaxNodes := opts.TreeFullMaxNodes
	if treeFullMaxNodes <= 0 {
		treeFullMaxNodes = defaultTreeFullMaxNodes
	}
	treeBlockMaxNodes := opts.TreeBlockMaxNodes
	if treeBlockMaxNodes <= 0 {
		treeBlockMaxNodes = defaultTreeBlockMaxNodes
	}
	treeBeamSize := opts.TreeBeamSize
	if treeBeamSize <= 0 {
		treeBeamSize = defaultTreeBeamSize
	}
	treeMaxTurns := opts.TreeMaxTurns
	if treeMaxTurns <= 0 {
		treeMaxTurns = defaultTreeMaxTurns
	}
	treeMaxBlocksPerTurn := opts.TreeMaxBlocksPerTurn
	if treeMaxBlocksPerTurn <= 0 {
		treeMaxBlocksPerTurn = defaultTreeMaxBlocksPerTurn
	}

	return &UseCase{
		repo:                 r,
		llm:                  llm,
		maxContextPages:      maxContextPages,
		treeFullMaxNodes:     treeFullMaxNodes,
		treeBlockMaxNodes:    treeBlockMaxNodes,
		treeBeamSize:         treeBeamSize,
		treeMaxTurns:         treeMaxTurns,
		treeMaxBlocksPerTurn: treeMaxBlocksPerTurn,
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

	lang, err := readerlang.Normalize(lang)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}
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

	searchResults, err := uc.searchRAGPages(ctx, bookID, question, lang, defaultCandidateCap)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}

	treeSelection, err := uc.selectTreeNodes(ctx, doc, question, structure, searchResults)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}

	headingIDs, lexicalHeadingIDs := mergeCandidateHeadings(structure, treeSelection.HeadingIDs, searchResults, defaultCandidateCap)
	focusPageIDs := focusPagesForHeadings(structure, searchResults, headingIDs)
	if len(headingIDs) == 0 {
		return entity.BookRAGResponse{
			BookID:        bookID,
			RequestedLang: lang,
			Question:      question,
			Answer:        notFoundAnswer(question),
			Citations:     []entity.BookRAGCitation{},
			Trace:         buildTrace(includeTrace, treeSelection, lexicalHeadingIDs, focusPageIDs, nil, false),
		}, nil
	}

	sources, err := uc.repo.GetRAGPageSources(ctx, bookID, headingIDs, focusPageIDs, lang, uc.maxContextPages)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}
	if len(sources) == 0 {
		return entity.BookRAGResponse{
			BookID:        bookID,
			RequestedLang: lang,
			Question:      question,
			Answer:        notFoundAnswer(question),
			Citations:     []entity.BookRAGCitation{},
			Trace:         buildTrace(includeTrace, treeSelection, lexicalHeadingIDs, focusPageIDs, nil, false),
		}, nil
	}

	assignSourceRefs(sources)

	answer, citations, repaired, err := uc.answerWithValidatedCitations(ctx, question, sources, maxCitations)
	if err != nil {
		return entity.BookRAGResponse{}, err
	}

	return entity.BookRAGResponse{
		BookID:        bookID,
		RequestedLang: lang,
		Question:      question,
		Answer:        answer,
		Citations:     citations,
		Trace:         buildTrace(includeTrace, treeSelection, lexicalHeadingIDs, focusPageIDs, sources, repaired),
	}, nil
}

func buildTrace(
	includeTrace bool,
	treeSelection treeSelectionResult,
	lexicalHeadingIDs []int,
	focusPageIDs []int,
	sources []entity.RAGPageSource,
	repaired bool,
) *entity.BookRAGTrace {
	if !includeTrace {
		return nil
	}

	return &entity.BookRAGTrace{
		TreeReasoning:      treeSelection.Reasoning,
		SelectedHeadingIDs: treeSelection.HeadingIDs,
		LexicalHeadingIDs:  lexicalHeadingIDs,
		FocusPageIDs:       focusPageIDs,
		SourceRefs:         sourceRefs(sources),
		RetrievalMode:      treeSelection.RetrievalMode,
		TreeLLMCalls:       treeSelection.LLMCalls,
		TreeBlocks:         treeSelection.Blocks,
		TreeCandidateCount: treeSelection.CandidateCount,
		Repaired:           repaired,
	}
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
	Reasoning      string
	HeadingIDs     []int
	RetrievalMode  string
	LLMCalls       int
	Blocks         int
	CandidateCount int
	Done           bool
}

func (uc *UseCase) selectTreeNodes(
	ctx context.Context,
	doc entity.RAGBookDocument,
	question string,
	structure []entity.RAGStructureNode,
	searchResults []entity.RAGSearchResult,
) (treeSelectionResult, error) {
	if len(structure) <= uc.treeFullMaxNodes {
		return uc.selectTreeNodesFull(ctx, doc, question, structure)
	}

	return uc.selectTreeNodesByBlocks(ctx, doc, question, structure, searchResults)
}

func (uc *UseCase) selectTreeNodesFull(
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
	selection.RetrievalMode = "full_tree"
	selection.LLMCalls = 1
	selection.Blocks = 1
	selection.CandidateCount = len(structure)
	if len(selection.HeadingIDs) > 0 {
		return selection, nil
	}

	retryRaw, err := uc.llm.Complete(ctx, retryTreeSelectionMessages(doc, question, structure, raw))
	if err != nil {
		return selection, nil
	}
	selection.LLMCalls = 2

	retrySelection := parseTreeSelection(retryRaw)
	retrySelection.RetrievalMode = "full_tree"
	retrySelection.LLMCalls = 2
	retrySelection.Blocks = 1
	retrySelection.CandidateCount = len(structure)
	if len(retrySelection.HeadingIDs) == 0 {
		return selection, nil
	}
	if selection.Reasoning != "" {
		retrySelection.Reasoning = selection.Reasoning + "\nRetry: " + retrySelection.Reasoning
	}

	return retrySelection, nil
}

func (uc *UseCase) selectTreeNodesByBlocks(
	ctx context.Context,
	doc entity.RAGBookDocument,
	question string,
	structure []entity.RAGStructureNode,
	searchResults []entity.RAGSearchResult,
) (treeSelectionResult, error) {
	tree := buildRAGTreeIndex(structure, searchResults)
	beams := []int{0}
	finalHeadingIDs := make([]int, 0, uc.treeBeamSize)
	reasoning := make([]string, 0, uc.treeMaxTurns)
	var llmCalls int
	var blocksProcessed int
	var candidateCount int

	for turn := 0; turn < uc.treeMaxTurns; turn++ {
		candidates := tree.childrenForBeams(beams)
		if len(candidates) == 0 {
			break
		}

		candidates = prioritizeTreeCandidates(candidates, tree, question)
		maxCandidates := uc.treeBlockMaxNodes * uc.treeMaxBlocksPerTurn
		if len(candidates) > maxCandidates {
			candidates = candidates[:maxCandidates]
		}
		candidateCount += len(candidates)

		blocks := splitHeadingBlocks(candidates, uc.treeBlockMaxNodes)
		if len(blocks) > uc.treeMaxBlocksPerTurn {
			blocks = blocks[:uc.treeMaxBlocksPerTurn]
		}

		rankedThisTurn := make([]int, 0, uc.treeBeamSize*len(blocks))
		var done bool
		for i, block := range blocks {
			selection, err := uc.rankTreeBlock(
				ctx,
				doc,
				question,
				tree,
				beams,
				finalHeadingIDs,
				block,
				turn,
				i+1,
				len(blocks),
			)
			if err != nil {
				return treeSelectionResult{}, err
			}

			llmCalls++
			blocksProcessed++
			if selection.Reasoning != "" {
				reasoning = append(reasoning, selection.Reasoning)
			}
			rankedThisTurn = append(rankedThisTurn, selection.HeadingIDs...)
			if selection.Done && len(selection.HeadingIDs) > 0 {
				done = true
			}
		}

		rankedThisTurn = uniquePositiveInts(rankedThisTurn)
		if len(rankedThisTurn) == 0 {
			break
		}

		finalHeadingIDs = limitInts(rankedThisTurn, uc.treeBeamSize)
		if done || tree.allLeaves(finalHeadingIDs) || tree.allRangesNarrow(finalHeadingIDs, uc.maxContextPages) {
			break
		}

		nextBeams := tree.idsWithChildren(finalHeadingIDs)
		if len(nextBeams) == 0 {
			break
		}
		beams = nextBeams
	}

	if len(finalHeadingIDs) == 0 {
		finalHeadingIDs = lexicalFallbackHeadingIDs(searchResults, tree, uc.treeBeamSize)
	}

	return treeSelectionResult{
		Reasoning:      strings.Join(reasoning, "\n"),
		HeadingIDs:     finalHeadingIDs,
		RetrievalMode:  "block_tree",
		LLMCalls:       llmCalls,
		Blocks:         blocksProcessed,
		CandidateCount: candidateCount,
	}, nil
}

func (uc *UseCase) rankTreeBlock(
	ctx context.Context,
	doc entity.RAGBookDocument,
	question string,
	tree *ragTreeIndex,
	beams []int,
	previousSelected []int,
	candidateIDs []int,
	turn int,
	blockIndex int,
	blockCount int,
) (treeSelectionResult, error) {
	messages := treeBlockSelectionMessages(
		doc,
		question,
		tree,
		beams,
		previousSelected,
		candidateIDs,
		turn,
		blockIndex,
		blockCount,
		uc.treeBeamSize,
	)
	raw, err := uc.llm.Complete(ctx, messages)
	if err != nil {
		return treeSelectionResult{}, err
	}

	selection := parseTreeSelection(raw)
	selection.HeadingIDs = filterHeadingIDs(selection.HeadingIDs, candidateIDs)

	return selection, nil
}

func treeBlockSelectionMessages(
	doc entity.RAGBookDocument,
	question string,
	tree *ragTreeIndex,
	beams []int,
	previousSelected []int,
	candidateIDs []int,
	turn int,
	blockIndex int,
	blockCount int,
	beamSize int,
) []entity.RAGChatMessage {
	var user strings.Builder
	user.WriteString(fmt.Sprintf("Book: %s\n", doc.Title))
	user.WriteString(fmt.Sprintf("Question: %s\n", question))
	user.WriteString(fmt.Sprintf("Turn: %d\n", turn+1))
	user.WriteString("Current beams:\n")
	for _, beamID := range beams {
		user.WriteString("- ")
		user.WriteString(tree.nodeLabel(beamID))
		user.WriteByte('\n')
	}
	user.WriteString("Previous selected:\n")
	if len(previousSelected) == 0 {
		user.WriteString("(none)\n")
	} else {
		for _, id := range previousSelected {
			user.WriteString("- ")
			user.WriteString(tree.nodeLabel(id))
			user.WriteByte('\n')
		}
	}
	user.WriteString(fmt.Sprintf("Candidate block %d/%d:\n", blockIndex, blockCount))
	for _, id := range candidateIDs {
		node, ok := tree.byID[id]
		if !ok {
			continue
		}
		user.WriteString(fmt.Sprintf(
			"- id=%d depth=%d pages=%d-%d children=%d lexical_hint=%t title_match=%t title=%q summary=%q path=%q\n",
			node.HeadingID,
			node.Depth,
			node.StartPageID,
			node.EndPageID,
			len(tree.childrenByParent[id]),
			tree.lexicalBranch[id],
			titleMatchesQuestion(node.Title, question),
			node.Title,
			summarySnippet(node.Summary),
			tree.titlePath(id),
		))
	}
	user.WriteString(fmt.Sprintf("Pick up to %d candidate IDs from this block, best first.\n", beamSize))

	return []entity.RAGChatMessage{
		{
			Role: "system",
			Content: `You are a PageIndex-style block tree retrieval planner for one classical Islamic book.
Rank only candidate heading IDs from the current block.
Use semantic relevance, lexical hints, titles, page ranges, and path context.
Set done=true only if selected nodes are specific enough to fetch source pages; for broad sections with useful children, set done=false.
Return strict JSON only: {"thinking":"short reason","node_ids":[11,12],"done":false}.`,
		},
		{Role: "user", Content: user.String()},
	}
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
			"- id=%d depth=%d pages=%d-%d title=%s summary=%s\n",
			node.HeadingID,
			node.Depth,
			node.StartPageID,
			node.EndPageID,
			node.Title,
			summarySnippet(node.Summary),
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
		if len(citations) == 0 && answerSaysNotFound(answer) && sourceLikelyContainsAnswer(question, sources) {
			answer, citations, ok, err = uc.repairAnswer(ctx, question, sources, raw, maxCitations)
			if err != nil {
				return "", nil, true, err
			}
			if ok && len(citations) > 0 {
				return answer, citations, true, nil
			}
			if fallbackAnswer, fallbackCitations, fallbackOK := extractiveFallbackAnswer(question, sources, maxCitations); fallbackOK {
				return fallbackAnswer, fallbackCitations, true, nil
			}
			if ok {
				return answer, citations, true, nil
			}
			return notFoundAnswer(question), []entity.BookRAGCitation{}, true, nil
		}

		return answer, citations, false, nil
	}

	answer, citations, ok, err = uc.repairAnswer(ctx, question, sources, raw, maxCitations)
	if err != nil {
		return "", nil, true, err
	}
	if ok {
		if len(citations) == 0 && sourceLikelyContainsAnswer(question, sources) {
			if fallbackAnswer, fallbackCitations, fallbackOK := extractiveFallbackAnswer(question, sources, maxCitations); fallbackOK {
				return fallbackAnswer, fallbackCitations, true, nil
			}
		}
		return answer, citations, true, nil
	}

	if sourceLikelyContainsAnswer(question, sources) {
		if fallbackAnswer, fallbackCitations, fallbackOK := extractiveFallbackAnswer(question, sources, maxCitations); fallbackOK {
			return fallbackAnswer, fallbackCitations, true, nil
		}
	}

	return notFoundAnswer(question), []entity.BookRAGCitation{}, true, nil
}

func (uc *UseCase) repairAnswer(
	ctx context.Context,
	question string,
	sources []entity.RAGPageSource,
	invalidAnswer string,
	maxCitations int,
) (string, []entity.BookRAGCitation, bool, error) {
	repairMessages := answerMessages(question, sources, invalidAnswer)
	repairedRaw, err := uc.llm.Complete(ctx, repairMessages)
	if err != nil {
		return "", nil, false, err
	}

	answer, citations, ok := parseAndValidateAnswer(repairedRaw, sources, maxCitations)
	return answer, citations, ok, nil
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
	queries := make([]string, 0, 8)
	add := func(query string) {
		query = strings.TrimSpace(query)
		if query == "" {
			return
		}

		if slices.Contains(queries, query) {
			return
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
	arabicTokens := arabicTokenRE.FindAllString(question, -1)
	if len(arabicTokens) > 1 {
		add(strings.Join(arabicTokens, " "))
	}
	for i := 0; i+1 < len(arabicTokens); i++ {
		add(arabicTokens[i] + " " + arabicTokens[i+1])
	}
	for _, token := range arabicTokens {
		if len([]rune(token)) >= 3 {
			add(token)
		}
	}
	add(question)

	return queries
}

func sourceLikelyContainsAnswer(question string, sources []entity.RAGPageSource) bool {
	var haystack strings.Builder
	for _, source := range sources {
		haystack.WriteString(source.HeadingTitle)
		haystack.WriteByte(' ')
		haystack.WriteString(source.ContentText)
		haystack.WriteByte(' ')
		if source.TranslationText != nil {
			haystack.WriteString(*source.TranslationText)
			haystack.WriteByte(' ')
		}
	}
	normalizedSource := normalizeSearchText(haystack.String())
	if normalizedSource == "" {
		return false
	}

	for _, query := range retrievalQueries(question) {
		query = normalizeSearchText(query)
		queryLen := len([]rune(query))
		if queryLen < 3 || queryLen > 120 {
			continue
		}
		if strings.Contains(normalizedSource, query) {
			return true
		}
	}

	return false
}

func extractiveFallbackAnswer(
	question string,
	sources []entity.RAGPageSource,
	maxCitations int,
) (string, []entity.BookRAGCitation, bool) {
	maxCitations = clampMaxCitations(maxCitations)
	citations := make([]entity.BookRAGCitation, 0, 1)
	for _, source := range sources {
		quote := fallbackQuote(question, source)
		if quote == "" {
			continue
		}
		citations = append(citations, citationFromSource(source, quote))
		if len(citations) >= maxCitations {
			break
		}
	}
	if len(citations) == 0 {
		return "", nil, false
	}

	return fallbackAnswerText(question, citations[0]), citations, true
}

func fallbackQuote(question string, source entity.RAGPageSource) string {
	segments := sourceSegments(source.ContentText)
	if len(segments) == 0 {
		return ""
	}

	for _, query := range retrievalQueries(question) {
		normalizedQuery := normalizeSearchText(query)
		queryLen := len([]rune(normalizedQuery))
		if queryLen < 3 || queryLen > 120 {
			continue
		}
		for _, segment := range segments {
			if strings.Contains(normalizeSearchText(segment), normalizedQuery) {
				return trimFallbackQuote(segment, query)
			}
		}
	}

	for _, segment := range segments {
		if len([]rune(segment)) >= 12 {
			return clipExactRunes(segment, fallbackQuoteLimit)
		}
	}

	return ""
}

func sourceSegments(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	segments := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		segments = append(segments, line)
	}
	if len(segments) == 0 {
		content = strings.TrimSpace(content)
		if content != "" {
			segments = append(segments, content)
		}
	}

	return segments
}

func trimFallbackQuote(segment, query string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return ""
	}
	if index := strings.Index(segment, strings.TrimSpace(query)); index >= 0 {
		return quoteAroundByteIndex(segment, index, fallbackQuoteLimit)
	}

	return clipExactRunes(segment, fallbackQuoteLimit)
}

func quoteAroundByteIndex(value string, index, maxRunes int) string {
	runes := []rune(value)
	runeIndex := len([]rune(value[:index]))
	start := max(runeIndex-maxRunes/3, 0)

	end := min(start+maxRunes, len(runes))
	if end-start > maxRunes {
		start = end - maxRunes
	}

	return strings.TrimSpace(string(runes[start:end]))
}

func clipExactRunes(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return value
	}

	return strings.TrimSpace(string(runes[:maxRunes]))
}

func citationFromSource(source entity.RAGPageSource, quote string) entity.BookRAGCitation {
	return entity.BookRAGCitation{
		Ref:          source.Ref,
		BookID:       source.BookID,
		HeadingID:    source.HeadingID,
		HeadingTitle: source.HeadingTitle,
		PageID:       source.PageID,
		PrintedPage:  source.PrintedPage,
		Part:         source.Part,
		Anchor:       source.Anchor,
		Quote:        quote,
		URL:          source.URL,
	}
}

func fallbackAnswerText(question string, citation entity.BookRAGCitation) string {
	if looksEnglish(question) {
		return fmt.Sprintf("The source states: %s [%s].", citation.Quote, citation.Ref)
	}

	return fmt.Sprintf("Sumber menyebutkan: %s [%s].", citation.Quote, citation.Ref)
}

func answerMessages(question string, sources []entity.RAGPageSource, invalidAnswer string) []entity.RAGChatMessage {
	system := `You answer questions about one classical Islamic book.
Use only the SOURCE BLOCKS. Do not use outside knowledge.
Answer in the same language as the user's question.
Every factual claim must include citation markers like [1].
Each citation quote must be copied exactly from the Arabic source text in the matching source block.
Question spelling may omit or include Arabic diacritics, braces, or vowel endings; use normalized matches as evidence, but copy citation quotes exactly.
If the sources do not contain enough evidence, say that the answer is not found in the provided sources.
Return strict JSON only:
{"answer":"... [1]","citations":[{"ref":"1","quote":"exact Arabic quote from source 1"}]}`
	if invalidAnswer != "" {
		system += "\nYou are repairing a previous answer. Re-check the source blocks before saying not found. Keep only citations whose quote appears exactly in the matching Arabic source."
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
	Summary  string           `json:"summary,omitempty"`
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
			ID:      entry.HeadingID,
			Title:   entry.Title,
			Summary: summarySnippet(entry.Summary),
			Pages:   fmt.Sprintf("%d-%d", entry.StartPageID, entry.EndPageID),
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

type ragTreeIndex struct {
	byID             map[int]entity.RAGStructureNode
	childrenByParent map[int][]int
	parentByID       map[int]int
	rootIDs          []int
	lexicalSelf      map[int]bool
	lexicalBranch    map[int]bool
}

func buildRAGTreeIndex(
	entries []entity.RAGStructureNode,
	searchResults []entity.RAGSearchResult,
) *ragTreeIndex {
	tree := &ragTreeIndex{
		byID:             make(map[int]entity.RAGStructureNode, len(entries)),
		childrenByParent: make(map[int][]int, len(entries)),
		parentByID:       make(map[int]int, len(entries)),
		rootIDs:          make([]int, 0),
		lexicalSelf:      make(map[int]bool, len(searchResults)),
		lexicalBranch:    make(map[int]bool, len(searchResults)),
	}

	for _, entry := range entries {
		tree.byID[entry.HeadingID] = entry
	}
	for _, entry := range entries {
		parentID := 0
		if entry.ParentID != nil {
			if _, ok := tree.byID[*entry.ParentID]; ok {
				parentID = *entry.ParentID
			}
		}
		tree.parentByID[entry.HeadingID] = parentID
		tree.childrenByParent[parentID] = append(tree.childrenByParent[parentID], entry.HeadingID)
		if parentID == 0 {
			tree.rootIDs = append(tree.rootIDs, entry.HeadingID)
		}
	}
	for _, result := range searchResults {
		if _, ok := tree.byID[result.HeadingID]; !ok {
			continue
		}
		tree.lexicalSelf[result.HeadingID] = true
		for id := result.HeadingID; id != 0; id = tree.parentByID[id] {
			tree.lexicalBranch[id] = true
		}
	}

	return tree
}

func (t *ragTreeIndex) childrenForBeams(beams []int) []int {
	result := make([]int, 0)
	seen := make(map[int]struct{})
	for _, beamID := range beams {
		for _, childID := range t.childrenByParent[beamID] {
			if _, ok := seen[childID]; ok {
				continue
			}
			seen[childID] = struct{}{}
			result = append(result, childID)
		}
	}

	return result
}

func (t *ragTreeIndex) idsWithChildren(ids []int) []int {
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if len(t.childrenByParent[id]) == 0 {
			continue
		}
		result = append(result, id)
	}

	return result
}

func (t *ragTreeIndex) allLeaves(ids []int) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if len(t.childrenByParent[id]) > 0 {
			return false
		}
	}

	return true
}

func (t *ragTreeIndex) allRangesNarrow(ids []int, maxPages int) bool {
	if len(ids) == 0 || maxPages <= 0 {
		return false
	}
	for _, id := range ids {
		node, ok := t.byID[id]
		if !ok {
			return false
		}
		if node.EndPageID-node.StartPageID+1 > maxPages {
			return false
		}
	}

	return true
}

func (t *ragTreeIndex) nodeLabel(id int) string {
	if id == 0 {
		return "root"
	}
	node, ok := t.byID[id]
	if !ok {
		return fmt.Sprintf("id=%d", id)
	}

	return fmt.Sprintf("id=%d pages=%d-%d title=%s", id, node.StartPageID, node.EndPageID, node.Title)
}

func (t *ragTreeIndex) titlePath(id int) string {
	if id == 0 {
		return "root"
	}

	parts := make([]string, 0)
	for current := id; current != 0; current = t.parentByID[current] {
		node, ok := t.byID[current]
		if !ok {
			break
		}
		parts = append(parts, node.Title)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}

	return strings.Join(parts, " > ")
}

func prioritizeTreeCandidates(ids []int, tree *ragTreeIndex, question string) []int {
	result := append([]int(nil), ids...)
	sort.SliceStable(result, func(i, j int) bool {
		return treeCandidateScore(result[i], tree, question) > treeCandidateScore(result[j], tree, question)
	})

	return result
}

func treeCandidateScore(id int, tree *ragTreeIndex, question string) int {
	node, ok := tree.byID[id]
	if !ok {
		return 0
	}

	var score int
	if tree.lexicalSelf[id] {
		score += 1000
	}
	if tree.lexicalBranch[id] {
		score += 800
	}
	if titleMatchesQuestion(node.Title, question) {
		score += 200
	}
	if node.Summary != nil && titleMatchesQuestion(*node.Summary, question) {
		score += 150
	}

	return score
}

func summarySnippet(summary *string) string {
	if summary == nil {
		return ""
	}

	return clipText(*summary, 360)
}

func titleMatchesQuestion(title, question string) bool {
	title = normalizeSearchText(title)
	question = normalizeSearchText(question)
	if len([]rune(title)) < 2 || question == "" {
		return false
	}

	return strings.Contains(question, title) || strings.Contains(title, question)
}

func normalizeSearchText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		switch {
		case strings.ContainsRune(arabicSearchMarks, r):
			return -1
		case r >= '٠' && r <= '٩':
			return '0' + (r - '٠')
		case r >= '۰' && r <= '۹':
			return '0' + (r - '۰')
		default:
			return r
		}
	}, value)

	return strings.Join(strings.Fields(value), " ")
}

func splitHeadingBlocks(ids []int, blockSize int) [][]int {
	if blockSize <= 0 {
		blockSize = defaultTreeBlockMaxNodes
	}

	blocks := make([][]int, 0, (len(ids)/blockSize)+1)
	for start := 0; start < len(ids); start += blockSize {
		end := min(start+blockSize, len(ids))
		blocks = append(blocks, ids[start:end])
	}

	return blocks
}

func filterHeadingIDs(ids, allowedIDs []int) []int {
	allowed := make(map[int]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}

	result := make([]int, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := allowed[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}

	return result
}

func limitInts(ids []int, limit int) []int {
	if limit <= 0 || len(ids) <= limit {
		return ids
	}

	return ids[:limit]
}

func lexicalFallbackHeadingIDs(
	searchResults []entity.RAGSearchResult,
	tree *ragTreeIndex,
	limit int,
) []int {
	result := make([]int, 0, limit)
	seen := make(map[int]struct{}, limit)
	for _, searchResult := range searchResults {
		if len(result) >= limit {
			break
		}
		if _, ok := tree.byID[searchResult.HeadingID]; !ok {
			continue
		}
		if _, ok := seen[searchResult.HeadingID]; ok {
			continue
		}
		seen[searchResult.HeadingID] = struct{}{}
		result = append(result, searchResult.HeadingID)
	}

	return result
}

func parseTreeSelection(raw string) treeSelectionResult {
	var data map[string]any
	if err := decodeLooseJSON(raw, &data); err != nil {
		return treeSelectionResult{
			Reasoning:  strings.TrimSpace(raw),
			HeadingIDs: parseLooseSelectionIDs(raw),
			Done:       parseLooseSelectionDone(raw),
		}
	}

	ids := extractIntSlice(data["node_ids"])
	if len(ids) == 0 {
		ids = extractIntSlice(data["heading_ids"])
	}
	if len(ids) == 0 {
		ids = parseLooseSelectionIDs(raw)
	}

	return treeSelectionResult{
		Reasoning:  extractString(data["thinking"]),
		HeadingIDs: uniquePositiveInts(ids),
		Done:       extractBool(data["done"]) || parseLooseSelectionDone(raw),
	}
}

func parseLooseSelectionIDs(raw string) []int {
	match := looseNodeIDsRE.FindStringSubmatch(raw)
	if len(match) < 2 {
		return nil
	}

	items := looseSelectionIDItemRE.FindAllString(match[1], -1)
	ids := make([]int, 0, len(items))
	for _, item := range items {
		id, err := strconv.Atoi(item)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}

	return uniquePositiveInts(ids)
}

func parseLooseSelectionDone(raw string) bool {
	match := looseSelectionDoneRE.FindStringSubmatch(raw)
	if len(match) < 2 {
		return false
	}

	return strings.EqualFold(match[1], "true")
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

func focusPagesForHeadings(
	structure []entity.RAGStructureNode,
	searchResults []entity.RAGSearchResult,
	headingIDs []int,
) []int {
	selected := make(map[int]struct{}, len(headingIDs))
	for _, id := range headingIDs {
		selected[id] = struct{}{}
	}
	parentByID := make(map[int]int, len(structure))
	for _, node := range structure {
		if node.ParentID != nil {
			parentByID[node.HeadingID] = *node.ParentID
		}
	}

	pages := make([]int, 0)
	seen := make(map[int]struct{})
	for _, result := range searchResults {
		if !headingMatchesSelection(result.HeadingID, selected, parentByID) {
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

func headingMatchesSelection(headingID int, selected map[int]struct{}, parentByID map[int]int) bool {
	for current := headingID; current > 0; {
		if _, ok := selected[current]; ok {
			return true
		}
		parentID, ok := parentByID[current]
		if !ok || parentID == current {
			return false
		}
		current = parentID
	}

	return false
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

func extractBool(value any) bool {
	if flag, ok := value.(bool); ok {
		return flag
	}
	if text, ok := value.(string); ok {
		parsed, err := strconv.ParseBool(strings.TrimSpace(text))
		return err == nil && parsed
	}

	return false
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

func containsNormalized(source, quote string) bool {
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
		end := min(start+maxRunes, len(runes))
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
	case errors.Is(err, entity.ErrUnsupportedLanguage):
		return "unsupported language"
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
