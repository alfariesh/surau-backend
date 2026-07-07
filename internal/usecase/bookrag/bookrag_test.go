package bookrag

import (
	"context"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCompactTOCTree(t *testing.T) {
	t.Parallel()

	parentID := 1
	summary := "Root summary"
	tree := buildCompactTOCTree([]entity.RAGStructureNode{
		{HeadingID: 1, Title: "Root", Summary: &summary, StartPageID: 1, EndPageID: 5},
		{HeadingID: 2, ParentID: &parentID, Title: "Child", StartPageID: 2, EndPageID: 3},
	})

	require.Len(t, tree, 1)
	assert.Equal(t, 1, tree[0].ID)
	assert.Equal(t, "Root summary", tree[0].Summary)
	assert.Equal(t, "1-5", tree[0].Pages)
	require.Len(t, tree[0].Children, 1)
	assert.Equal(t, 2, tree[0].Children[0].ID)
}

func TestMergeCandidateHeadingsAndFocusPages(t *testing.T) {
	t.Parallel()

	parentID := 1
	structure := []entity.RAGStructureNode{
		{HeadingID: 1},
		{HeadingID: 2, ParentID: &parentID},
		{HeadingID: 3},
	}
	searchResults := []entity.RAGSearchResult{
		{HeadingID: 2, PageID: 12},
		{HeadingID: 3, PageID: 14},
		{HeadingID: 99, PageID: 20},
		{HeadingID: 2, PageID: 12},
	}

	headingIDs, lexicalIDs := mergeCandidateHeadings(structure, []int{1, 99, 2}, searchResults, 4)
	focusPages := focusPagesForHeadings(structure, searchResults, headingIDs)

	assert.Equal(t, []int{1, 2, 3}, headingIDs)
	assert.Equal(t, []int{2, 3}, lexicalIDs)
	assert.Equal(t, []int{12, 14}, focusPages)
	assert.Equal(t, []int{12}, focusPagesForHeadings(structure, searchResults, []int{1}))
}

func TestRetrievalQueriesExpandsCommonTerms(t *testing.T) {
	t.Parallel()

	queries := retrievalQueries("Apa definisi hadis sahih dan اللَّهُ نُورُ?")

	assert.Contains(t, queries, "الصحيح")
	assert.Contains(t, queries, "حديث صحيح")
	assert.Contains(t, queries, "اللَّهُ نُورُ")
	assert.Contains(t, queries, "اللَّهُ")
	assert.Contains(t, queries, "نُورُ")
	assert.Contains(t, queries, "Apa definisi hadis sahih dan اللَّهُ نُورُ?")
}

func TestUseCaseSearchRAGPagesUsesExpandedQueries(t *testing.T) {
	t.Parallel()

	repo := &fakeBookRAGRepo{
		searchByQuery: map[string][]entity.RAGSearchResult{
			"الصحيح": {{HeadingID: 11, PageID: 12}},
		},
	}
	uc := New(repo, &fakeLLM{}, Options{})

	results, err := uc.searchRAGPages(context.Background(), 797, "Apa definisi hadis sahih?", "id", 10)

	require.NoError(t, err)
	assert.Equal(t, []entity.RAGSearchResult{{HeadingID: 11, PageID: 12}}, results)
}

func TestSelectTreeNodesUsesFullTreeForSmallTOC(t *testing.T) {
	t.Parallel()

	repo := &fakeBookRAGRepo{}
	summary := "This section discusses target evidence."
	llm := &fakeLLM{responses: []string{`{"thinking":"small tree","node_ids":[1]}`}}
	uc := New(repo, llm, Options{TreeFullMaxNodes: 10})

	selection, err := uc.selectTreeNodes(
		context.Background(),
		entity.RAGBookDocument{Title: "Book"},
		"question",
		[]entity.RAGStructureNode{{HeadingID: 1, Title: "Root", Summary: &summary, StartPageID: 1, EndPageID: 2}},
		nil,
	)

	require.NoError(t, err)
	assert.Equal(t, "full_tree", selection.RetrievalMode)
	assert.Equal(t, 1, selection.LLMCalls)
	assert.Equal(t, 1, selection.Blocks)
	assert.Equal(t, 1, selection.CandidateCount)
	require.Len(t, llm.messages, 1)
	assert.Contains(t, llm.messages[0][1].Content, "Compact TOC tree JSON")
	assert.Contains(t, llm.messages[0][1].Content, "This section discusses target evidence")
}

func TestSelectTreeNodesUsesBlockTreeForLargeTOC(t *testing.T) {
	t.Parallel()

	parentID := 1
	structure := []entity.RAGStructureNode{
		{HeadingID: 1, Title: "Chapter", StartPageID: 1, EndPageID: 20},
		{HeadingID: 2, ParentID: &parentID, Title: "Target section", StartPageID: 12, EndPageID: 12},
	}
	for id := 10; id < 30; id++ {
		structure = append(structure, entity.RAGStructureNode{
			HeadingID:   id,
			Title:       "Filler",
			StartPageID: id,
			EndPageID:   id,
		})
	}

	llm := &fakeLLM{responses: []string{
		`{"thinking":"choose chapter","node_ids":[1],"done":false}`,
		`{"thinking":"choose target","node_ids":[2],"done":true}`,
	}}
	uc := New(&fakeBookRAGRepo{}, llm, Options{
		MaxContextPages:      1,
		TreeFullMaxNodes:     2,
		TreeBlockMaxNodes:    5,
		TreeBeamSize:         2,
		TreeMaxTurns:         3,
		TreeMaxBlocksPerTurn: 1,
	})

	selection, err := uc.selectTreeNodes(
		context.Background(),
		entity.RAGBookDocument{Title: "Book"},
		"target question",
		structure,
		nil,
	)

	require.NoError(t, err)
	assert.Equal(t, "block_tree", selection.RetrievalMode)
	assert.Equal(t, []int{2}, selection.HeadingIDs)
	assert.Equal(t, 2, selection.LLMCalls)
	assert.Equal(t, 2, selection.Blocks)
	require.Len(t, llm.messages, 2)
	assert.NotContains(t, llm.messages[0][1].Content, "Compact TOC tree JSON")
	assert.NotContains(t, llm.messages[0][1].Content, "id=29")
}

func TestSelectTreeNodesPrioritizesLexicalHitInBlockMode(t *testing.T) {
	t.Parallel()

	structure := make([]entity.RAGStructureNode, 0, 20)
	for id := 1; id <= 20; id++ {
		structure = append(structure, entity.RAGStructureNode{
			HeadingID:   id,
			Title:       "Node",
			StartPageID: id,
			EndPageID:   id,
		})
	}
	llm := &fakeLLM{responses: []string{
		`{"thinking":"lexical hit wins","node_ids":[20],"done":true}`,
	}}
	uc := New(&fakeBookRAGRepo{}, llm, Options{
		TreeFullMaxNodes:     2,
		TreeBlockMaxNodes:    5,
		TreeBeamSize:         3,
		TreeMaxTurns:         2,
		TreeMaxBlocksPerTurn: 1,
	})

	selection, err := uc.selectTreeNodes(
		context.Background(),
		entity.RAGBookDocument{Title: "Book"},
		"question",
		structure,
		[]entity.RAGSearchResult{{HeadingID: 20, PageID: 20}},
	)

	require.NoError(t, err)
	assert.Equal(t, []int{20}, selection.HeadingIDs)
	require.Len(t, llm.messages, 1)
	assert.Contains(t, llm.messages[0][1].Content, "id=20")
}

func TestSelectTreeNodesIncludesSummaryInBlockMode(t *testing.T) {
	t.Parallel()

	summary := "Summary mentions the hidden target topic."
	structure := []entity.RAGStructureNode{
		{HeadingID: 1, Title: "Chapter", Summary: &summary, StartPageID: 1, EndPageID: 1},
	}
	llm := &fakeLLM{responses: []string{
		`{"thinking":"summary hit","node_ids":[1],"done":true}`,
	}}
	uc := New(&fakeBookRAGRepo{}, llm, Options{
		TreeFullMaxNodes:     0,
		TreeBlockMaxNodes:    1,
		TreeBeamSize:         1,
		TreeMaxTurns:         1,
		TreeMaxBlocksPerTurn: 1,
	})
	uc.treeFullMaxNodes = 0

	selection, err := uc.selectTreeNodes(
		context.Background(),
		entity.RAGBookDocument{Title: "Book"},
		"hidden target",
		structure,
		nil,
	)

	require.NoError(t, err)
	assert.Equal(t, "block_tree", selection.RetrievalMode)
	assert.Equal(t, []int{1}, selection.HeadingIDs)
	require.Len(t, llm.messages, 1)
	assert.Contains(t, llm.messages[0][1].Content, "Summary mentions the hidden target topic")
}

func TestSelectTreeNodesDoesNotSendFullJalalainLikeTOC(t *testing.T) {
	t.Parallel()

	const targetID = 6001
	structure := make([]entity.RAGStructureNode, 0, targetID)
	for id := 1; id <= targetID; id++ {
		structure = append(structure, entity.RAGStructureNode{
			HeadingID:   id,
			Title:       "Heading",
			StartPageID: id,
			EndPageID:   id,
		})
	}
	llm := &fakeLLM{responses: []string{
		`{"thinking":"lexical target","node_ids":[6001],"done":true}`,
	}}
	uc := New(&fakeBookRAGRepo{}, llm, Options{
		TreeFullMaxNodes:     450,
		TreeBlockMaxNodes:    120,
		TreeBeamSize:         3,
		TreeMaxTurns:         2,
		TreeMaxBlocksPerTurn: 1,
	})

	selection, err := uc.selectTreeNodes(
		context.Background(),
		entity.RAGBookDocument{Title: "Tafsir Jalalain"},
		"ayat target",
		structure,
		[]entity.RAGSearchResult{{HeadingID: targetID, PageID: targetID}},
	)

	require.NoError(t, err)
	assert.Equal(t, "block_tree", selection.RetrievalMode)
	assert.Equal(t, []int{targetID}, selection.HeadingIDs)
	assert.Equal(t, 1, selection.LLMCalls)
	assert.Equal(t, 120, selection.CandidateCount)
	require.Len(t, llm.messages, 1)
	assert.Contains(t, llm.messages[0][1].Content, "id=6001")
	assert.NotContains(t, llm.messages[0][1].Content, "Compact TOC tree JSON")
	assert.NotContains(t, llm.messages[0][1].Content, "id=5999")
}

func TestSplitHeadingBlocks(t *testing.T) {
	t.Parallel()

	blocks := splitHeadingBlocks([]int{1, 2, 3, 4, 5}, 2)

	assert.Equal(t, [][]int{{1, 2}, {3, 4}, {5}}, blocks)
}

func TestParseTreeSelectionAcceptsLooseNodeIDs(t *testing.T) {
	t.Parallel()

	selection := parseTreeSelection("reason\nnode_ids:[2849]\ndone:true")

	assert.Equal(t, []int{2849}, selection.HeadingIDs)
	assert.True(t, selection.Done)
}

func TestParseAndValidateAnswer(t *testing.T) {
	t.Parallel()

	sources := []entity.RAGPageSource{
		{
			Ref:          "1",
			BookID:       797,
			HeadingID:    11,
			HeadingTitle: "الصحيح",
			PageID:       12,
			Anchor:       "toc-11",
			URL:          "/v1/books/797/toc/11/read?lang=id",
			ContentText:  "الحديث الصحيح هو ما اتصل سنده بنقل العدل الضابط.",
		},
	}
	raw := `{"answer":"Hadis sahih disyaratkan bersambung sanadnya [1].","citations":[{"ref":"1","quote":"ما اتصل سنده بنقل العدل الضابط"}]}`

	answer, citations, ok := parseAndValidateAnswer(raw, sources, 5)

	require.True(t, ok)
	assert.Contains(t, answer, "[1]")
	require.Len(t, citations, 1)
	assert.Equal(t, "1", citations[0].Ref)
	assert.Equal(t, 12, citations[0].PageID)
}

func TestParseAndValidateAnswerRejectsMissingQuote(t *testing.T) {
	t.Parallel()

	sources := []entity.RAGPageSource{{Ref: "1", ContentText: "النص الأصلي"}}
	raw := `{"answer":"Jawaban [1].","citations":[{"ref":"1","quote":"kutipan yang tidak ada"}]}`

	_, _, ok := parseAndValidateAnswer(raw, sources, 5)

	assert.False(t, ok)
}

func TestSourceLikelyContainsAnswerNormalizesArabicMarks(t *testing.T) {
	t.Parallel()

	sources := []entity.RAGPageSource{
		{
			HeadingTitle: "النور",
			ContentText:  "{اللَّه نُور السَّمَاوَات وَالْأَرْض} أَيْ مُنَوِّرهمَا",
		},
	}

	ok := sourceLikelyContainsAnswer("Apa makna اللَّهُ نُورُ السَّمَاوَاتِ وَالأَرْضِ?", sources)

	assert.True(t, ok)
}

func TestUseCaseAskBook(t *testing.T) {
	t.Parallel()

	repo := &fakeBookRAGRepo{
		doc: entity.RAGBookDocument{BookID: 797, Title: "Book"},
		structure: []entity.RAGStructureNode{
			{HeadingID: 11, Title: "الصحيح", StartPageID: 12, EndPageID: 13},
		},
		searchResults: []entity.RAGSearchResult{{HeadingID: 11, PageID: 12}},
		sources: []entity.RAGPageSource{
			{
				BookID:       797,
				HeadingID:    11,
				HeadingTitle: "الصحيح",
				StartPageID:  12,
				EndPageID:    13,
				PageID:       12,
				Anchor:       "toc-11",
				URL:          "/v1/books/797/toc/11/read?lang=id",
				ContentText:  "الحديث الصحيح هو ما اتصل سنده.",
			},
		},
	}
	llm := &fakeLLM{responses: []string{
		`{"thinking":"title matches","node_ids":[11]}`,
		`{"answer":"Hadis sahih berkaitan dengan sanad yang bersambung [1].","citations":[{"ref":"1","quote":"ما اتصل سنده"}]}`,
	}}
	uc := New(repo, llm, Options{MaxContextPages: 4})

	response, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.NoError(t, err)
	assert.Equal(t, 797, response.BookID)
	assert.Contains(t, response.Answer, "[1]")
	require.Len(t, response.Citations, 1)
	assert.Equal(t, 11, response.Citations[0].HeadingID)
	require.NotNil(t, response.Trace)
	assert.False(t, response.Trace.Repaired)
	assert.Equal(t, "full_tree", response.Trace.RetrievalMode)
	assert.Equal(t, 1, response.Trace.TreeLLMCalls)
	assert.Equal(t, []int{11}, repo.lastHeadingIDs)
	assert.Equal(t, []int{12}, repo.lastFocusPageIDs)
}

func TestUseCaseAskBookReturnsNotFoundWhenNoCandidateHeadings(t *testing.T) {
	t.Parallel()

	repo := &fakeBookRAGRepo{
		doc: entity.RAGBookDocument{BookID: 797, Title: "Book"},
		structure: []entity.RAGStructureNode{
			{HeadingID: 11, Title: "الصحيح", StartPageID: 12, EndPageID: 13},
		},
	}
	llm := &fakeLLM{responses: []string{
		`{"thinking":"no relevant heading","node_ids":[]}`,
	}}
	uc := New(repo, llm, Options{MaxContextPages: 4})

	response, err := uc.AskBook(context.Background(), 797, "Apa hukum bitcoin?", "id", 5, true)

	require.NoError(t, err)
	assert.Contains(t, response.Answer, "belum menemukan")
	assert.Empty(t, response.Citations)
	require.NotNil(t, response.Trace)
	assert.Empty(t, response.Trace.SelectedHeadingIDs)
}

func TestUseCaseAskBookRepairsInvalidCitation(t *testing.T) {
	t.Parallel()

	repo := &fakeBookRAGRepo{
		doc: entity.RAGBookDocument{BookID: 797, Title: "Book"},
		structure: []entity.RAGStructureNode{
			{HeadingID: 11, Title: "الصحيح", StartPageID: 12, EndPageID: 13},
		},
		searchResults: []entity.RAGSearchResult{{HeadingID: 11, PageID: 12}},
		sources: []entity.RAGPageSource{
			{
				BookID:       797,
				HeadingID:    11,
				HeadingTitle: "الصحيح",
				StartPageID:  12,
				EndPageID:    13,
				PageID:       12,
				Anchor:       "toc-11",
				URL:          "/v1/books/797/toc/11/read?lang=id",
				ContentText:  "الحديث الصحيح هو ما اتصل سنده.",
			},
		},
	}
	llm := &fakeLLM{responses: []string{
		`{"thinking":"title matches","node_ids":[11]}`,
		`{"answer":"Jawaban [1].","citations":[{"ref":"1","quote":"tidak ada"}]}`,
		`{"answer":"Hadis sahih disebut dengan sanad yang bersambung [1].","citations":[{"ref":"1","quote":"ما اتصل سنده"}]}`,
	}}
	uc := New(repo, llm, Options{MaxContextPages: 4})

	response, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.NoError(t, err)
	require.NotNil(t, response.Trace)
	assert.True(t, response.Trace.Repaired)
	require.Len(t, response.Citations, 1)
}

func TestUseCaseAskBookRepairsFalseNotFoundWhenSourceMatches(t *testing.T) {
	t.Parallel()

	repo := &fakeBookRAGRepo{
		doc: entity.RAGBookDocument{BookID: 797, Title: "Book"},
		structure: []entity.RAGStructureNode{
			{HeadingID: 11, Title: "الصحيح", StartPageID: 12, EndPageID: 13},
		},
		searchResults: []entity.RAGSearchResult{{HeadingID: 11, PageID: 12}},
		sources: []entity.RAGPageSource{
			{
				BookID:       797,
				HeadingID:    11,
				HeadingTitle: "الصحيح",
				StartPageID:  12,
				EndPageID:    13,
				PageID:       12,
				Anchor:       "toc-11",
				URL:          "/v1/books/797/toc/11/read?lang=id",
				ContentText:  "الحديث الصحيح هو ما اتصل سنده.",
			},
		},
	}
	llm := &fakeLLM{responses: []string{
		`{"thinking":"title matches","node_ids":[11]}`,
		`{"answer":"Tidak ditemukan dalam sumber yang disediakan.","citations":[]}`,
		`{"answer":"Hadis sahih disebut dengan sanad yang bersambung [1].","citations":[{"ref":"1","quote":"ما اتصل سنده"}]}`,
	}}
	uc := New(repo, llm, Options{MaxContextPages: 4})

	response, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.NoError(t, err)
	require.NotNil(t, response.Trace)
	assert.True(t, response.Trace.Repaired)
	require.Len(t, response.Citations, 1)
	assert.Equal(t, 3, len(llm.messages))
}

func TestUseCaseAskBookFallsBackToExtractiveCitationWhenRepairStillRefuses(t *testing.T) {
	t.Parallel()

	repo := &fakeBookRAGRepo{
		doc: entity.RAGBookDocument{BookID: 797, Title: "Book"},
		structure: []entity.RAGStructureNode{
			{HeadingID: 11, Title: "الصحيح", StartPageID: 12, EndPageID: 12},
		},
		searchResults: []entity.RAGSearchResult{{HeadingID: 11, PageID: 12}},
		sources: []entity.RAGPageSource{
			{
				BookID:       797,
				HeadingID:    11,
				HeadingTitle: "الصحيح",
				StartPageID:  12,
				EndPageID:    12,
				PageID:       12,
				Anchor:       "toc-11",
				URL:          "/v1/books/797/toc/11/read?lang=id",
				ContentText:  "الحديث الصحيح هو ما اتصل سنده بلا شذوذ ولا علة.",
			},
		},
	}
	llm := &fakeLLM{responses: []string{
		`{"thinking":"title matches","node_ids":[11]}`,
		`{"answer":"Tidak ditemukan dalam sumber yang disediakan.","citations":[]}`,
		`{"answer":"Tidak ditemukan dalam sumber yang disediakan.","citations":[]}`,
	}}
	uc := New(repo, llm, Options{MaxContextPages: 4})

	response, err := uc.AskBook(context.Background(), 797, "Menurut kitab ini, bagaimana definisi الحديث الصحيح?", "id", 5, true)

	require.NoError(t, err)
	require.NotNil(t, response.Trace)
	assert.True(t, response.Trace.Repaired)
	require.Len(t, response.Citations, 1)
	assert.Equal(t, 11, response.Citations[0].HeadingID)
	assert.Equal(t, "الحديث الصحيح هو ما اتصل سنده بلا شذوذ ولا علة.", response.Citations[0].Quote)
	assert.Contains(t, response.Answer, "[1]")
}

type fakeBookRAGRepo struct {
	doc              entity.RAGBookDocument
	structure        []entity.RAGStructureNode
	searchResults    []entity.RAGSearchResult
	searchByQuery    map[string][]entity.RAGSearchResult
	sources          []entity.RAGPageSource
	lastHeadingIDs   []int
	lastFocusPageIDs []int
}

func (r *fakeBookRAGRepo) GetRAGBookDocument(
	_ context.Context,
	_ int,
	_ string,
) (entity.RAGBookDocument, error) {
	return r.doc, nil
}

func (r *fakeBookRAGRepo) ListRAGStructure(
	_ context.Context,
	_ int,
	_ string,
) ([]entity.RAGStructureNode, error) {
	return r.structure, nil
}

func (r *fakeBookRAGRepo) GetRAGPageSources(
	_ context.Context,
	_ int,
	headingIDs []int,
	focusPageIDs []int,
	_ string,
	_ int,
) ([]entity.RAGPageSource, error) {
	r.lastHeadingIDs = append([]int(nil), headingIDs...)
	r.lastFocusPageIDs = append([]int(nil), focusPageIDs...)

	return r.sources, nil
}

func (r *fakeBookRAGRepo) SearchRAGPages(
	_ context.Context,
	_ int,
	query string,
	_ string,
	_ int,
) ([]entity.RAGSearchResult, error) {
	if r.searchByQuery != nil {
		return r.searchByQuery[query], nil
	}

	return r.searchResults, nil
}

type fakeLLM struct {
	responses []string
	messages  [][]entity.RAGChatMessage
}

func (l *fakeLLM) Complete(_ context.Context, messages []entity.RAGChatMessage) (string, error) {
	l.messages = append(l.messages, messages)
	if len(l.responses) == 0 {
		return "", nil
	}

	response := l.responses[0]
	l.responses = l.responses[1:]

	return response, nil
}

func (l *fakeLLM) Stream(_ context.Context, _ []entity.RAGChatMessage, emit func(delta string) error) error {
	for _, response := range l.responses {
		if err := emit(response); err != nil {
			return err
		}
	}

	return nil
}
