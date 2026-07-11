package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:paralleltest // Uses one outer transaction against the shared live database.
func TestLiveAssetImportRollsBackRunRegistrationOnRegistryConflict(t *testing.T) {
	postgresURL := os.Getenv("SURAU_LIVE_PG")
	if postgresURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, postgresURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	existingRunID := uuid.NewString()
	newRunID := uuid.NewString()
	_, err = tx.Exec(ctx, `
INSERT INTO generation_runs (id, task_name, model_id, prompt_version)
VALUES ($1, 'catalog_translation', 'registered-model', 'catalog-translation-v1')`, existingRunID)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"kind":"category_translation","category_id":1,"lang":"id","name":"one","provenance_class":"machine","generation":{"run_id":%q,"model_id":"new-model","prompt_version":"catalog-translation-v1"}}`, newRunID) + "\n" +
		fmt.Sprintf(`{"kind":"category_translation","category_id":1,"lang":"id","name":"two","provenance_class":"machine","generation":{"run_id":%q,"model_id":"conflicting-model","prompt_version":"catalog-translation-v1"}}`, existingRunID)

	_, err = importAssets(ctx, tx, strings.NewReader(input))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 2")
	assert.Contains(t, err.Error(), "conflicts with the registered identity")

	var newRunCount int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT count(*) FROM generation_runs WHERE id = $1`, newRunID).Scan(&newRunCount))
	assert.Zero(t, newRunCount, "the whole nested import transaction must roll back")
}

//nolint:paralleltest // Uses one outer transaction against the shared live database.
func TestLiveAssetImportRollsBackEarlierAssetWhenLaterBatchRowFails(t *testing.T) {
	postgresURL := os.Getenv("SURAU_LIVE_PG")
	if postgresURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, postgresURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	categoryID := int(time.Now().UnixNano()%500_000_000) + 1_500_000_000
	_, err = tx.Exec(ctx, `INSERT INTO categories (id, name) VALUES ($1, 'atomic-reader-asset')`, categoryID)
	require.NoError(t, err)

	firstRunID := uuid.NewString()
	secondRunID := uuid.NewString()
	input := fmt.Sprintf(`{"kind":"category_translation","category_id":%d,"lang":"id","name":"must roll back","provenance_class":"machine","generation":{"run_id":%q,"model_id":"model-a","prompt_version":"catalog-translation-v1"}}`, categoryID, firstRunID) + "\n" +
		fmt.Sprintf(`{"kind":"category_translation","category_id":%d,"lang":"id","name":"missing parent","provenance_class":"machine","generation":{"run_id":%q,"model_id":"model-a","prompt_version":"catalog-translation-v1"}}`, categoryID+1, secondRunID)

	_, err = importAssets(ctx, tx, strings.NewReader(input))
	require.Error(t, err)

	var assetCount, runCount int
	require.NoError(t, tx.QueryRow(ctx, `
SELECT count(*) FROM category_translations
WHERE category_id = $1 AND lang = 'id'`, categoryID).Scan(&assetCount))
	require.NoError(t, tx.QueryRow(ctx, `
SELECT count(*) FROM generation_runs WHERE id = ANY($1::uuid[])`, []string{firstRunID, secondRunID}).Scan(&runCount))
	assert.Zero(t, assetCount, "the earlier batch row must roll back")
	assert.Zero(t, runCount, "run registration must roll back with the failed batch")
}

//nolint:paralleltest // Exercises every asset kind against one real transaction boundary.
func TestLiveAssetImportPersistsEveryAssetKindWithGenerationIdentity(t *testing.T) {
	postgresURL := os.Getenv("SURAU_LIVE_PG")
	if postgresURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, postgresURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	baseID := int(time.Now().UnixNano()%300_000_000) + 1_400_000_000
	categoryID := baseID
	authorID := baseID + 1
	bookID := baseID + 2
	translationRunID := uuid.NewString()
	summaryRunID := uuid.NewString()
	catalogRunID := uuid.NewString()
	runIDs := []string{translationRunID, summaryRunID, catalogRunID}

	_, err = tx.Exec(ctx,
		`INSERT INTO categories (id, name) VALUES ($1, 'asset-import-category')`, categoryID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx,
		`INSERT INTO authors (id, name) VALUES ($1, 'asset-import-author')`, authorID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO books (id, name, category_id, author_id, has_content)
VALUES ($1, 'asset-import-book', $2, $3, TRUE)`, bookID, categoryID, authorID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<p>source</p>', 'source')`, bookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_headings (book_id, heading_id, page_id, depth, ordinal, content)
VALUES ($1, 1, 1, 0, 1, 'heading')`, bookID)
	require.NoError(t, err)

	reviewedBy := "asset-import-reviewer"
	reviewedAt := time.Now().UTC().Truncate(time.Microsecond)
	metadata := json.RawMessage(`{"fixture":"all-kinds"}`)
	assets := []ReaderAsset{
		{
			Kind: "translation", BookID: bookID, HeadingID: 1, Lang: "id",
			Title: new("Terjemahan"), Content: "Isi terjemahan", Source: new("fixture"),
			Status: "reviewed", ReviewedBy: &reviewedBy, ReviewedAt: &reviewedAt, Metadata: metadata,
			ProvenanceClass: "machine", Generation: &ReaderAssetGeneration{
				RunID: translationRunID, ModelID: "fixture-model", PromptVersion: "reader-translation-v1",
			},
		},
		{
			Kind: "heading_summary", BookID: bookID, HeadingID: 1, Lang: "id",
			Summary: "Ringkasan", Source: new("fixture"), SummaryStatus: "reviewed",
			SummaryReviewedBy: &reviewedBy, SummaryReviewedAt: &reviewedAt, Metadata: metadata,
			ProvenanceClass: "machine", Generation: &ReaderAssetGeneration{
				RunID: summaryRunID, ModelID: "fixture-model", PromptVersion: "reader-summary-translation-v1",
			},
		},
		{
			Kind: "audio", BookID: bookID, HeadingID: 1, Lang: "id",
			URL: "https://example.test/audio.mp3", Narrator: new("Narrator"), Metadata: metadata,
		},
		{
			Kind: "book_metadata_translation", BookID: bookID, Lang: "id",
			DisplayTitle: new("Judul"), Description: new("Deskripsi"), Metadata: metadata,
			ProvenanceClass: "machine", Generation: &ReaderAssetGeneration{
				RunID: catalogRunID, ModelID: "fixture-model", PromptVersion: "catalog-translation-v1",
			},
		},
		{
			Kind: "author_translation", AuthorID: authorID, Lang: "id",
			Name: new("Nama Penulis"), Biography: new("Biografi"), Metadata: metadata,
			ProvenanceClass: "machine", Generation: &ReaderAssetGeneration{
				RunID: catalogRunID, ModelID: "fixture-model", PromptVersion: "catalog-translation-v1",
			},
		},
		{
			Kind: "category_translation", CategoryID: categoryID, Lang: "id",
			Name: new("Nama Kategori"), Metadata: metadata,
			ProvenanceClass: "machine", Generation: &ReaderAssetGeneration{
				RunID: catalogRunID, ModelID: "fixture-model", PromptVersion: "catalog-translation-v1",
			},
		},
	}

	var input strings.Builder

	for i := range assets {
		line, marshalErr := json.Marshal(assets[i])
		require.NoError(t, marshalErr)
		input.Write(line)
		input.WriteByte('\n')
	}

	expected := AssetStats{
		Translations: 1, Summaries: 1, Audio: 1, BookMetadataTranslations: 1,
		AuthorTranslations: 1, CategoryTranslations: 1,
	}

	for range 2 {
		stats, importErr := importAssets(ctx, tx, strings.NewReader(input.String()))
		require.NoError(t, importErr)
		assert.Equal(t, expected, stats)
	}

	textAssets := []struct {
		query string
		args  []any
		runID string
	}{
		{`SELECT provenance_class, generation_run_id::text FROM section_translations WHERE book_id = $1 AND heading_id = 1 AND lang = 'id'`, []any{bookID}, translationRunID},
		{`SELECT provenance_class, generation_run_id::text FROM book_heading_summaries WHERE book_id = $1 AND heading_id = 1 AND lang = 'id'`, []any{bookID}, summaryRunID},
		{`SELECT provenance_class, generation_run_id::text FROM book_metadata_translations WHERE book_id = $1 AND lang = 'id'`, []any{bookID}, catalogRunID},
		{`SELECT provenance_class, generation_run_id::text FROM author_translations WHERE author_id = $1 AND lang = 'id'`, []any{authorID}, catalogRunID},
		{`SELECT provenance_class, generation_run_id::text FROM category_translations WHERE category_id = $1 AND lang = 'id'`, []any{categoryID}, catalogRunID},
	}
	for _, asset := range textAssets {
		var provenanceClass, runID string
		require.NoError(t, tx.QueryRow(ctx, asset.query, asset.args...).Scan(&provenanceClass, &runID))
		assert.Equal(t, "machine", provenanceClass)
		assert.Equal(t, asset.runID, runID)
	}

	var audioCount int
	require.NoError(t, tx.QueryRow(ctx, `
SELECT count(*) FROM section_audio
WHERE book_id = $1 AND heading_id = 1 AND lang = 'id'`, bookID).Scan(&audioCount))
	assert.Equal(t, 1, audioCount)

	var registeredRunCount int
	require.NoError(t, tx.QueryRow(ctx, `
SELECT count(*) FROM generation_runs
WHERE id = ANY($1::uuid[])
  AND model_id = 'fixture-model'
  AND metadata = '{"source":"reader_asset_import"}'::jsonb`, runIDs).Scan(&registeredRunCount))
	assert.Equal(t, len(runIDs), registeredRunCount)
}
