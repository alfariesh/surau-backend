package bookrag

import (
	"context"
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCompactTOCTree(t *testing.T) {
	t.Parallel()

	parentID := 1
	tree := buildCompactTOCTree([]entity.RAGStructureNode{
		{HeadingID: 1, Title: "Root", StartPageID: 1, EndPageID: 5},
		{HeadingID: 2, ParentID: &parentID, Title: "Child", StartPageID: 2, EndPageID: 3},
	})

	require.Len(t, tree, 1)
	assert.Equal(t, 1, tree[0].ID)
	assert.Equal(t, "1-5", tree[0].Pages)
	require.Len(t, tree[0].Children, 1)
	assert.Equal(t, 2, tree[0].Children[0].ID)
}

func TestMergeCandidateHeadingsAndFocusPages(t *testing.T) {
	t.Parallel()

	structure := []entity.RAGStructureNode{
		{HeadingID: 1},
		{HeadingID: 2},
		{HeadingID: 3},
	}
	searchResults := []entity.RAGSearchResult{
		{HeadingID: 2, PageID: 12},
		{HeadingID: 3, PageID: 14},
		{HeadingID: 99, PageID: 20},
		{HeadingID: 2, PageID: 12},
	}

	headingIDs, lexicalIDs := mergeCandidateHeadings(structure, []int{1, 99, 2}, searchResults, 4)
	focusPages := focusPagesForHeadings(searchResults, headingIDs)

	assert.Equal(t, []int{1, 2, 3}, headingIDs)
	assert.Equal(t, []int{2, 3}, lexicalIDs)
	assert.Equal(t, []int{12, 14}, focusPages)
}

func TestRetrievalQueriesExpandsCommonTerms(t *testing.T) {
	t.Parallel()

	queries := retrievalQueries("Apa definisi hadis sahih?")

	assert.Contains(t, queries, "Apa definisi hadis sahih?")
	assert.Contains(t, queries, "الصحيح")
	assert.Contains(t, queries, "حديث صحيح")
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
	assert.Equal(t, []int{11}, repo.lastHeadingIDs)
	assert.Equal(t, []int{12}, repo.lastFocusPageIDs)
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
}

func (l *fakeLLM) Complete(_ context.Context, _ []entity.RAGChatMessage) (string, error) {
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
