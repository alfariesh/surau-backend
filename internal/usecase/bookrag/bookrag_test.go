package bookrag

import (
	"context"
	"encoding/json"
	"strings"
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

	assert.Equal(t, "Apa definisi hadis sahih dan اللَّهُ نُورُ?", queries[0])
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

func TestParseAndValidateAnswerRejectsMarkersBeyondCitationLimit(t *testing.T) {
	t.Parallel()

	sources := []entity.RAGPageSource{
		{Ref: "1", ContentText: "bukti pertama"},
		{Ref: "2", ContentText: "bukti kedua"},
	}
	raw := `{"answer":"Jawaban [1] dan [2].","citations":[{"ref":"1","quote":"bukti pertama"},{"ref":"2","quote":"bukti kedua"}]}`

	answer, citations, ok := parseAndValidateAnswer(raw, sources, 1)

	assert.False(t, ok)
	assert.Empty(t, answer)
	assert.Empty(t, citations, "validator must not return an answer with a dangling marker")
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

func TestUnitCitationRequiresVerbatimQuote(t *testing.T) {
	t.Parallel()

	unitID := "unit-1"
	source := entity.RAGPageSource{
		Ref: "1", UnitID: &unitID, ContentText: "الحديث  الصحيح",
	}
	raw := `{"answer":"Jawaban [1].","citations":[{"ref":"1","quote":"الحديث الصحيح"}]}`

	_, _, ok := parseAndValidateAnswer(raw, []entity.RAGPageSource{source}, 5)

	assert.False(t, ok, "unit mode must not normalize or invent a quote before binding its Anchor")

	source.UnitID = nil
	_, _, ok = parseAndValidateAnswer(raw, []entity.RAGPageSource{source}, 5)
	assert.True(t, ok, "legacy validator remains byte-compatible during migration")
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

func TestUseCaseLegacyModeKeepsCitationJSONCompatible(t *testing.T) {
	t.Parallel()

	repository, llm := happyBookRAGFixture()
	uc := New(repository, llm, Options{CitationMode: CitationModeLegacy})

	response, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.NoError(t, err)
	require.Len(t, response.Citations, 1)
	payload, err := json.Marshal(response.Citations[0])
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "unit_id")
	assert.NotContains(t, string(payload), "unit_anchor")
	assert.Equal(t, 1, repository.pageSourceCalls)
	assert.Zero(t, repository.unitSourceCalls)
	assert.Empty(t, response.Trace.CitationMode)
}

func TestUseCaseDualModeAddsUnitLocatorWithoutSecondLLM(t *testing.T) {
	t.Parallel()

	repository, llm := happyBookRAGFixture()
	repository.unitLocator = entity.RAGUnitLocator{
		UnitID:     "unit-1",
		UnitAnchor: "kitab/797/h/11/u/42",
		Found:      true,
	}
	uc := New(repository, llm, Options{CitationMode: CitationModeDual})

	response, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.NoError(t, err)
	require.Len(t, response.Citations, 1)
	require.NotNil(t, response.Citations[0].UnitID)
	require.NotNil(t, response.Citations[0].UnitAnchor)
	assert.Equal(t, "unit-1", *response.Citations[0].UnitID)
	assert.Equal(t, 1, repository.resolveCalls)
	assert.Equal(t, 2, len(llm.messages), "dual projection must not call the LLM again")
	assert.Equal(t, CitationModeDual, response.Trace.CitationMode)
}

func TestProjectUnitCitationsRejectsMixedProjectionWithoutPartialAssignment(t *testing.T) {
	t.Parallel()

	repository := &fakeBookRAGRepo{
		unitLocators: []entity.RAGUnitLocator{
			{UnitID: "unit-1", UnitAnchor: "kitab/797/h/11/u/42", Found: true},
			{},
		},
	}
	uc := New(repository, &fakeLLM{}, Options{CitationMode: CitationModeDual})
	citations := []entity.BookRAGCitation{
		{Ref: "1", BookID: 797, HeadingID: 11, PageID: 12, Quote: "quote one"},
		{Ref: "2", BookID: 797, HeadingID: 11, PageID: 12, Quote: "quote two"},
	}

	err := uc.projectUnitCitations(context.Background(), 797, citations)

	var mismatchErr citationParityMismatchError
	require.ErrorAs(t, err, &mismatchErr)
	assert.Empty(t, materializationFallbackReason(err), "quote mismatch must never enter legacy fallback")
	assert.Nil(t, citations[0].UnitID, "projection must remain all-or-nothing")
	assert.Nil(t, citations[1].UnitID, "projection must remain all-or-nothing")
}

func TestUseCaseDualModeFallsBackWholeResponseOnMidRequestStale(t *testing.T) {
	t.Parallel()

	repository, llm := happyBookRAGFixture()
	repository.resolveErr = entity.ErrRAGUnitMaterializationStale
	uc := New(repository, llm, Options{
		CitationMode:   CitationModeDual,
		LegacyFallback: true,
	})

	response, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.NoError(t, err)
	require.Len(t, response.Citations, 1)
	assert.Nil(t, response.Citations[0].UnitID)
	assert.Nil(t, response.Citations[0].UnitAnchor)
	assert.True(t, response.Trace.LegacyFallback)
	assert.Equal(t, "stale", response.Trace.FallbackReason)
	assert.Equal(t, 2, len(llm.messages), "dual stale handling must not run a second answer pipeline")
}

func TestUseCaseDualModeDoesNotFallbackWhenDisabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		expected  error
		configure func(*fakeBookRAGRepo)
		llmCalls  int
	}{
		{
			name:     "preflight incomplete",
			expected: entity.ErrRAGUnitMaterializationIncomplete,
			configure: func(repository *fakeBookRAGRepo) {
				repository.materializationErr = entity.ErrRAGUnitMaterializationIncomplete
			},
		},
		{
			name:     "projection stale",
			expected: entity.ErrRAGUnitMaterializationStale,
			configure: func(repository *fakeBookRAGRepo) {
				repository.resolveErr = entity.ErrRAGUnitMaterializationStale
			},
			llmCalls: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository, llm := happyBookRAGFixture()
			test.configure(repository)
			uc := New(repository, llm, Options{
				CitationMode:   CitationModeDual,
				LegacyFallback: false,
			})

			_, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

			require.ErrorIs(t, err, test.expected)
			assert.Len(t, llm.messages, test.llmCalls)
		})
	}
}

func TestUseCaseUnitModeUsesUnitEvidence(t *testing.T) {
	t.Parallel()

	repository, llm := happyBookRAGFixture()
	unitID := "unit-1"
	unitAnchor := "kitab/797/h/11/u/42"
	repository.sources[0].UnitID = &unitID
	repository.sources[0].UnitAnchor = &unitAnchor
	uc := New(repository, llm, Options{CitationMode: CitationModeUnit})

	response, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.NoError(t, err)
	require.Len(t, response.Citations, 1)
	assert.Equal(t, 1, repository.unitSourceCalls)
	assert.Zero(t, repository.pageSourceCalls)
	assert.Equal(t, unitID, *response.Citations[0].UnitID)
	assert.Equal(t, unitAnchor, *response.Citations[0].UnitAnchor)
	assert.Equal(t, CitationModeUnit, response.Trace.CitationMode)
}

func TestUseCaseUnitModePromotesExactLexicalUnitWithinPage(t *testing.T) {
	t.Parallel()

	repository, llm := happyBookRAGFixture()
	openingID := "unit-opening"
	targetID := "unit-target"
	openingAnchor := "kitab/797/h/11/u/1"
	targetAnchor := "kitab/797/h/11/u/2"
	repository.searchResults = []entity.RAGSearchResult{{
		HeadingID: 11,
		PageID:    12,
		UnitID:    targetID,
	}}
	repository.sources = []entity.RAGPageSource{
		{
			BookID: 797, HeadingID: 11, PageID: 12, ContentText: "بسم الله الرحمن الرحيم",
			UnitID: &openingID, UnitAnchor: &openingAnchor,
		},
		{
			BookID: 797, HeadingID: 11, PageID: 12, ContentText: "الحديث الصحيح هو ما اتصل سنده.",
			UnitID: &targetID, UnitAnchor: &targetAnchor,
		},
	}
	uc := New(repository, llm, Options{CitationMode: CitationModeUnit})

	response, err := uc.AskBook(context.Background(), 797, "الحديث الصحيح هو ما اتصل سنده.", "id", 1, true)

	require.NoError(t, err)
	require.Len(t, response.Citations, 1)
	require.NotNil(t, response.Citations[0].UnitID)
	assert.Equal(t, targetID, *response.Citations[0].UnitID)
	require.Len(t, llm.messages, 2)
	answerPrompt := llm.messages[1][1].Content
	assert.Less(
		t,
		strings.Index(answerPrompt, "الحديث الصحيح هو ما اتصل سنده."),
		strings.Index(answerPrompt, "بسم الله الرحمن الرحيم"),
		"the exact lexical unit must be the first answer source",
	)
}

func TestUseCaseUnitModeFallsBackWholeRequestForTypedIncomplete(t *testing.T) {
	t.Parallel()

	repository, llm := happyBookRAGFixture()
	repository.materializationErr = entity.ErrRAGUnitMaterializationIncomplete
	uc := New(repository, llm, Options{
		CitationMode:   CitationModeUnit,
		LegacyFallback: true,
	})

	response, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.NoError(t, err)
	assert.Equal(t, 1, repository.pageSourceCalls)
	assert.Zero(t, repository.unitSourceCalls)
	assert.Equal(t, 2, len(llm.messages), "typed preflight fallback must not start a unit LLM pass")
	require.NotNil(t, response.Trace)
	assert.Equal(t, CitationModeUnit, response.Trace.CitationMode)
	assert.True(t, response.Trace.LegacyFallback)
	assert.Equal(t, "incomplete", response.Trace.FallbackReason)
}

func TestUseCaseUnitModeFallsBackWhenUnitLocatorIsMissing(t *testing.T) {
	t.Parallel()

	repository, llm := happyBookRAGFixture()
	repository.omitUnitLocators = true
	llm.responses = []string{
		`{"thinking":"unit tree","node_ids":[11]}`,
		`{"thinking":"legacy tree","node_ids":[11]}`,
		`{"answer":"Hadis sahih berkaitan dengan sanad yang bersambung [1].","citations":[{"ref":"1","quote":"ما اتصل سنده"}]}`,
	}
	uc := New(repository, llm, Options{
		CitationMode:   CitationModeUnit,
		LegacyFallback: true,
	})

	response, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.NoError(t, err)
	require.Len(t, response.Citations, 1)
	assert.Nil(t, response.Citations[0].UnitID)
	assert.True(t, response.Trace.LegacyFallback)
	assert.Equal(t, "incomplete", response.Trace.FallbackReason)
	assert.Equal(t, 3, len(llm.messages), "unit pass stops before answer LLM, then legacy runs one full pass")
}

func TestUseCaseUnitModeDoesNotFallbackForDatabaseError(t *testing.T) {
	t.Parallel()

	repository, llm := happyBookRAGFixture()
	databaseErr := assert.AnError
	repository.materializationErr = databaseErr
	uc := New(repository, llm, Options{
		CitationMode:   CitationModeUnit,
		LegacyFallback: true,
	})

	_, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.ErrorIs(t, err, databaseErr)
	assert.Zero(t, repository.pageSourceCalls)
	assert.Zero(t, repository.unitSourceCalls)
	assert.Empty(t, llm.messages)
}

func TestUseCaseUnitModeDoesNotFallbackForEligibilityDenial(t *testing.T) {
	t.Parallel()

	repository, llm := happyBookRAGFixture()
	repository.materializationErr = entity.ErrRAGEvidenceNotFound
	uc := New(repository, llm, Options{
		CitationMode:   CitationModeUnit,
		LegacyFallback: true,
	})

	_, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.ErrorIs(t, err, entity.ErrRAGEvidenceNotFound)
	assert.Zero(t, repository.pageSourceCalls)
	assert.Zero(t, repository.unitSourceCalls)
	assert.Empty(t, llm.messages)
}

func TestUseCaseUnitModeFallbackCanBeDisabled(t *testing.T) {
	t.Parallel()

	repository, llm := happyBookRAGFixture()
	repository.materializationErr = entity.ErrRAGUnitMaterializationStale
	uc := New(repository, llm, Options{CitationMode: CitationModeUnit})

	_, err := uc.AskBook(context.Background(), 797, "Apa definisi hadis sahih?", "id", 5, true)

	require.ErrorIs(t, err, entity.ErrRAGUnitMaterializationStale)
	assert.Zero(t, repository.pageSourceCalls)
	assert.Zero(t, repository.unitSourceCalls)
	assert.Empty(t, llm.messages)
}

func happyBookRAGFixture() (*fakeBookRAGRepo, *fakeLLM) {
	repository := &fakeBookRAGRepo{
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

	return repository, llm
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

func TestBoundUnitContextKeepsLegacyTotalRuneBudget(t *testing.T) {
	t.Parallel()

	unitID := "unit"
	unitAnchor := "kitab/1/h/1/u/1"
	sources := []entity.RAGPageSource{
		{ContentText: strings.Repeat("ا", 3000), UnitID: &unitID, UnitAnchor: &unitAnchor},
		{ContentText: strings.Repeat("ب", 3000), UnitID: &unitID, UnitAnchor: &unitAnchor},
		{ContentText: strings.Repeat("ت", 3000), UnitID: &unitID, UnitAnchor: &unitAnchor},
	}

	bounded := boundUnitContext(sources, 6000)
	require.Len(t, bounded, 2)
	assert.Empty(t, boundUnitContext(sources, 0))
}

type fakeBookRAGRepo struct {
	doc                entity.RAGBookDocument
	structure          []entity.RAGStructureNode
	searchResults      []entity.RAGSearchResult
	searchByQuery      map[string][]entity.RAGSearchResult
	sources            []entity.RAGPageSource
	lastHeadingIDs     []int
	lastFocusPageIDs   []int
	materializationErr error
	unitLocator        entity.RAGUnitLocator
	unitLocators       []entity.RAGUnitLocator
	resolveErr         error
	pageSourceCalls    int
	unitSourceCalls    int
	resolveCalls       int
	omitUnitLocators   bool
}

func (r *fakeBookRAGRepo) CheckRAGUnitMaterialization(_ context.Context, _ int) error {
	return r.materializationErr
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
	r.pageSourceCalls++
	r.lastHeadingIDs = append([]int(nil), headingIDs...)
	r.lastFocusPageIDs = append([]int(nil), focusPageIDs...)

	return r.sources, nil
}

func (r *fakeBookRAGRepo) GetRAGUnitSources(
	_ context.Context,
	_ int,
	headingIDs []int,
	focusPageIDs []int,
	_ string,
	_ int,
) ([]entity.RAGPageSource, error) {
	r.unitSourceCalls++

	r.lastHeadingIDs = append([]int(nil), headingIDs...)
	r.lastFocusPageIDs = append([]int(nil), focusPageIDs...)

	result := append([]entity.RAGPageSource(nil), r.sources...)
	for i := range result {
		if r.omitUnitLocators {
			continue
		}

		if result[i].UnitID == nil {
			unitID := "unit-" + result[i].Ref
			if result[i].Ref == "" {
				unitID = "unit-fixture"
			}

			unitAnchor := "kitab/797/h/11/u/42"
			result[i].UnitID = &unitID
			result[i].UnitAnchor = &unitAnchor
		}
	}

	return result, nil
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

func (r *fakeBookRAGRepo) SearchRAGUnits(
	_ context.Context,
	_ int,
	query string,
	_ int,
) ([]entity.RAGSearchResult, error) {
	if r.searchByQuery != nil {
		return r.searchByQuery[query], nil
	}

	return r.searchResults, nil
}

func (r *fakeBookRAGRepo) ResolveRAGUnitCitation(
	_ context.Context,
	_ int,
	_ int,
	_ int,
	_ string,
) (entity.RAGUnitLocator, error) {
	r.resolveCalls++
	if len(r.unitLocators) >= r.resolveCalls {
		return r.unitLocators[r.resolveCalls-1], r.resolveErr
	}

	return r.unitLocator, r.resolveErr
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
