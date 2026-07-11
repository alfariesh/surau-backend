package importer

import (
	"context"
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
