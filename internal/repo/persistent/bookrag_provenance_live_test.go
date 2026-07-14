package persistent

import (
	"context"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	bookRAGProvenanceBookID              = -9_904_401
	bookRAGProvenanceProjectID           = "b4040100-0000-4000-8000-000000000001"
	bookRAGProvenanceRunID               = "b4040100-0000-4000-8000-000000000002"
	bookRAGProvenanceUnreviewedHeadingID = 9_904_411
	bookRAGProvenanceReviewedHeadingID   = 9_904_412
	bookRAGProvenanceSourceHeadingID     = 9_904_413

	bookRAGUnreviewedNeedle = "zzqxvunreviewed847263"
	bookRAGReviewedNeedle   = "ppkdreviewed529174"
	bookRAGSourceNeedle     = "mmnwsource638205"
)

// TestLiveBookRAGRetrievalEligibility proves PK-1/O-4-4 at the real query
// boundary. Machine-generated unreviewed text remains stored for the public
// reader but cannot influence metadata, tree planning, lexical retrieval, or
// evidence. Human-reviewed machine text remains eligible, and source text is
// eligible without being relabelled as reviewed.
//
//nolint:maintidx,paralleltest // serial live-DB fixture exercises all four BookRAG repository methods
func TestLiveBookRAGRetrievalEligibility(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()

	cleanupBookRAGProvenanceFixture(t, pg)
	t.Cleanup(func() { cleanupBookRAGProvenanceFixture(t, pg) })

	run := entity.GenerationRun{
		ID:            bookRAGProvenanceRunID,
		TaskName:      "reader-translation",
		ModelID:       "bookrag-provenance-live-model",
		PromptVersion: "reader-translation-v1",
		Metadata:      entity.RawJSON(`{"fixture":"bookrag-retrieval-eligibility"}`),
	}
	_, err = NewGenerationRunRepo(pg).RegisterOrVerify(ctx, &run)
	require.NoError(t, err)
	require.NoError(t, seedBookRAGProvenanceFixture(ctx, pg))

	repository := NewBookRAGRepo(pg)

	generatedDocument, err := repository.GetRAGBookDocument(ctx, bookRAGProvenanceBookID, "en")
	require.NoError(t, err)
	assert.Equal(t, "source book title", generatedDocument.Title)
	assert.Nil(t, generatedDocument.Description, "unreviewed machine metadata must not enter the RAG prompt")

	structure, err := repository.ListRAGStructure(ctx, bookRAGProvenanceBookID, "en")
	require.NoError(t, err)
	require.Len(t, structure, 3)

	unreviewedNode := requireRAGStructureNode(t, structure, bookRAGProvenanceUnreviewedHeadingID)
	assert.Equal(t, "source unreviewed heading", unreviewedNode.Title)
	assert.Nil(t, unreviewedNode.Summary)

	reviewedNode := requireRAGStructureNode(t, structure, bookRAGProvenanceReviewedHeadingID)
	assert.Equal(t, "reviewed machine heading", reviewedNode.Title)
	require.NotNil(t, reviewedNode.Summary)
	assert.Equal(t, "reviewed machine summary "+bookRAGReviewedNeedle, *reviewedNode.Summary)

	sourceNode := requireRAGStructureNode(t, structure, bookRAGProvenanceSourceHeadingID)
	assert.Equal(t, "source-authored translated heading", sourceNode.Title)
	require.NotNil(t, sourceNode.Summary)
	assert.Equal(t, "source-authored summary "+bookRAGSourceNeedle, *sourceNode.Summary)

	sources, err := repository.GetRAGPageSources(
		ctx,
		bookRAGProvenanceBookID,
		[]int{
			bookRAGProvenanceUnreviewedHeadingID,
			bookRAGProvenanceReviewedHeadingID,
			bookRAGProvenanceSourceHeadingID,
		},
		[]int{1, 2, 3},
		"en",
		10,
	)
	require.NoError(t, err)
	require.Len(t, sources, 3)

	rankedSources, err := repository.GetRAGPageSources(
		ctx,
		bookRAGProvenanceBookID,
		[]int{
			bookRAGProvenanceUnreviewedHeadingID,
			bookRAGProvenanceReviewedHeadingID,
			bookRAGProvenanceSourceHeadingID,
		},
		[]int{3, 1, 2},
		"en",
		2,
	)
	require.NoError(t, err)
	require.Len(t, rankedSources, 2)
	assert.Equal(t, 3, rankedSources[0].PageID, "the strongest lexical page must be the first source block")
	assert.Equal(t, 1, rankedSources[1].PageID, "focus-page rank must win over numeric page order")

	unreviewedSource := requireRAGPageSource(t, sources, bookRAGProvenanceUnreviewedHeadingID)
	assert.Nil(t, unreviewedSource.TranslationText)
	reviewedSource := requireRAGPageSource(t, sources, bookRAGProvenanceReviewedHeadingID)
	require.NotNil(t, reviewedSource.TranslationText)
	assert.Contains(t, *reviewedSource.TranslationText, bookRAGReviewedNeedle)
	sourceAuthored := requireRAGPageSource(t, sources, bookRAGProvenanceSourceHeadingID)
	require.NotNil(t, sourceAuthored.TranslationText)
	assert.Contains(t, *sourceAuthored.TranslationText, bookRAGSourceNeedle)

	unreviewedMatches, err := repository.SearchRAGPages(
		ctx,
		bookRAGProvenanceBookID,
		bookRAGUnreviewedNeedle,
		"en",
		10,
	)
	require.NoError(t, err)
	assert.Empty(t, unreviewedMatches, "unreviewed machine text must not influence lexical retrieval")

	reviewedMatches, err := repository.SearchRAGPages(
		ctx,
		bookRAGProvenanceBookID,
		bookRAGReviewedNeedle,
		"en",
		10,
	)
	require.NoError(t, err)
	assertRAGSearchContainsHeading(t, reviewedMatches, bookRAGProvenanceReviewedHeadingID)

	sourceMatches, err := repository.SearchRAGPages(
		ctx,
		bookRAGProvenanceBookID,
		bookRAGSourceNeedle,
		"en",
		10,
	)
	require.NoError(t, err)
	assertRAGSearchContainsHeading(t, sourceMatches, bookRAGProvenanceSourceHeadingID)

	_, err = pg.Pool.Exec(ctx, `
UPDATE book_metadata_translations
SET translation_status = 'reviewed',
    reviewed_by = 'bookrag-live-reviewer',
    reviewed_at = now()
WHERE book_id = $1 AND lang = 'en'`, bookRAGProvenanceBookID)
	require.NoError(t, err)

	reviewedDocument, err := repository.GetRAGBookDocument(ctx, bookRAGProvenanceBookID, "en")
	require.NoError(t, err)
	assert.Equal(t, "reviewed machine book title", reviewedDocument.Title)
	require.NotNil(t, reviewedDocument.Description)
	assert.Equal(t, "reviewed machine description", *reviewedDocument.Description)
}

func seedBookRAGProvenanceFixture(ctx context.Context, pg *postgres.Postgres) error {
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(ctx, tx)

	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `
INSERT INTO books (id, name, has_content, license_status)
VALUES ($1, 'source book title', true, 'unknown')`,
			args: []any{bookRAGProvenanceBookID},
		},
		{query: `SET LOCAL session_replication_role = 'replica'`},
		{
			query: `UPDATE books SET license_status = 'permitted' WHERE id = $1`,
			args:  []any{bookRAGProvenanceBookID},
		},
		{query: `SET LOCAL session_replication_role = 'origin'`},
		{
			query: `
INSERT INTO book_publications (book_id, status, published_at)
VALUES ($1, 'published', now())`,
			args: []any{bookRAGProvenanceBookID},
		},
		{
			query: `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<p>source page alpha</p>', 'source page alpha'),
       ($1, 2, '<p>source page beta</p>', 'source page beta'),
       ($1, 3, '<p>source page gamma</p>', 'source page gamma')`,
			args: []any{bookRAGProvenanceBookID},
		},
		{
			query: `
INSERT INTO book_headings (book_id, heading_id, page_id, depth, ordinal, content)
VALUES ($1, $2, 1, 0, 1, 'source unreviewed heading'),
       ($1, $3, 2, 0, 2, 'source reviewed heading'),
       ($1, $4, 3, 0, 3, 'source authored heading')`,
			args: []any{
				bookRAGProvenanceBookID,
				bookRAGProvenanceUnreviewedHeadingID,
				bookRAGProvenanceReviewedHeadingID,
				bookRAGProvenanceSourceHeadingID,
			},
		},
		{
			query: `
INSERT INTO book_heading_ranges (book_id, heading_id, start_page_id, end_page_id)
VALUES ($1, $2, 1, 1), ($1, $3, 2, 2), ($1, $4, 3, 3)`,
			args: []any{
				bookRAGProvenanceBookID,
				bookRAGProvenanceUnreviewedHeadingID,
				bookRAGProvenanceReviewedHeadingID,
				bookRAGProvenanceSourceHeadingID,
			},
		},
		{
			query: `
INSERT INTO book_production_projects (
    id, book_id, lang, workflow_status, publication_status, requires_review,
    requires_audio, published_at
)
VALUES ($1, $2, 'en', 'published', 'published', false, false, now())`,
			args: []any{bookRAGProvenanceProjectID, bookRAGProvenanceBookID},
		},
		{
			query: `
INSERT INTO book_metadata_translations (
    book_id, lang, display_title, description, translation_status,
    provenance_class, generation_run_id
)
VALUES (
    $1, 'en', 'reviewed machine book title', 'reviewed machine description',
    'generated', 'machine', $2
)`,
			args: []any{bookRAGProvenanceBookID, bookRAGProvenanceRunID},
		},
		{
			query: `
INSERT INTO section_translations (
    book_id, heading_id, lang, title, content, translation_status,
    reviewed_by, reviewed_at, provenance_class, generation_run_id
)
VALUES
    ($1, $2, 'en', 'unreviewed machine heading', $5, 'generated', NULL, NULL, 'machine', $8),
    ($1, $3, 'en', 'reviewed machine heading', $6, 'reviewed', 'bookrag-live-reviewer', now(), 'machine', $8),
    ($1, $4, 'en', 'source-authored translated heading', $7, 'generated', NULL, NULL, 'source', NULL)`,
			args: []any{
				bookRAGProvenanceBookID,
				bookRAGProvenanceUnreviewedHeadingID,
				bookRAGProvenanceReviewedHeadingID,
				bookRAGProvenanceSourceHeadingID,
				"unreviewed machine content " + bookRAGUnreviewedNeedle,
				"reviewed machine content " + bookRAGReviewedNeedle,
				"source-authored content " + bookRAGSourceNeedle,
				bookRAGProvenanceRunID,
			},
		},
		{
			query: `
INSERT INTO book_heading_summaries (
    book_id, heading_id, lang, summary, summary_status,
    reviewed_by, reviewed_at, provenance_class, generation_run_id
)
VALUES
    ($1, $2, 'en', $5, 'generated', NULL, NULL, 'machine', $8),
    ($1, $3, 'en', $6, 'reviewed', 'bookrag-live-reviewer', now(), 'machine', $8),
    ($1, $4, 'en', $7, 'generated', NULL, NULL, 'source', NULL)`,
			args: []any{
				bookRAGProvenanceBookID,
				bookRAGProvenanceUnreviewedHeadingID,
				bookRAGProvenanceReviewedHeadingID,
				bookRAGProvenanceSourceHeadingID,
				"unreviewed machine summary " + bookRAGUnreviewedNeedle,
				"reviewed machine summary " + bookRAGReviewedNeedle,
				"source-authored summary " + bookRAGSourceNeedle,
				bookRAGProvenanceRunID,
			},
		},
	}

	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func cleanupBookRAGProvenanceFixture(t *testing.T, pg *postgres.Postgres) {
	t.Helper()

	if _, err := pg.Pool.Exec(
		context.Background(),
		`DELETE FROM books WHERE id = $1`,
		bookRAGProvenanceBookID,
	); err != nil {
		t.Logf("cleanup BookRAG provenance fixture: %v", err)
	}
}

func requireRAGStructureNode(
	t *testing.T,
	nodes []entity.RAGStructureNode,
	headingID int,
) entity.RAGStructureNode {
	t.Helper()

	for index := range nodes {
		if nodes[index].HeadingID == headingID {
			return nodes[index]
		}
	}

	t.Fatalf("RAG structure omitted heading %d", headingID)

	return entity.RAGStructureNode{}
}

func requireRAGPageSource(
	t *testing.T,
	sources []entity.RAGPageSource,
	headingID int,
) entity.RAGPageSource {
	t.Helper()

	for index := range sources {
		if sources[index].HeadingID == headingID {
			return sources[index]
		}
	}

	t.Fatalf("RAG evidence omitted heading %d", headingID)

	return entity.RAGPageSource{}
}

func assertRAGSearchContainsHeading(
	t *testing.T,
	results []entity.RAGSearchResult,
	headingID int,
) {
	t.Helper()

	for index := range results {
		if results[index].HeadingID == headingID {
			return
		}
	}

	t.Fatalf("RAG lexical search omitted eligible heading %d: %+v", headingID, results)
}
