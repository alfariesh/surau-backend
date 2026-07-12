package backfill

import (
	"context"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveQuranCitableUnitsBackfillAndRederive proves the two registered Q-2
// jobs traverse surahs deterministically, resume from the integer cursor, pick
// up stale source additions, and rederive without minting duplicate units.
//
//nolint:paralleltest,wsl_v5 // serial linear live-DB fixture intentionally keeps setup and assertions adjacent
func TestLiveQuranCitableUnitsBackfillAndRederive(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}
	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	var existing int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*) FROM quran_surahs WHERE surah_id IN (111, 112)`).Scan(&existing))
	if existing != 0 {
		t.Skip("Q-2 backfill fixture surahs already exist")
	}

	const translationSourceID = "q2-backfill-translation"
	t.Cleanup(func() {
		tx, txErr := pg.Pool.Begin(context.Background())
		if txErr != nil {
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		cleanupExec := func(query string, args ...any) bool {
			_, cleanupErr := tx.Exec(context.Background(), query, args...)

			return assert.NoError(t, cleanupErr, "Q-2 backfill fixture cleanup")
		}
		if !cleanupExec(`SET LOCAL surau.registry_writer = 'unit-service'`) ||
			!cleanupExec(`
DELETE FROM citable_units u
USING quran_citable_unit_bindings b
WHERE b.unit_id = u.id AND b.surah_id IN (111, 112)`) ||
			!cleanupExec(`DELETE FROM quran_surahs WHERE surah_id IN (111, 112)`) ||
			!cleanupExec(`DELETE FROM quran_translation_sources WHERE id = $1`, translationSourceID) {
			return
		}
		assert.NoError(t, tx.Commit(context.Background()), "commit Q-2 backfill fixture cleanup")
	})

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_surahs (surah_id, name_latin, ayah_count) VALUES
	(111, 'Al-Masad Q-2 Backfill', 1),
	(112, 'Al-Ikhlas Q-2 Backfill', 1);
INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, text_qpc_hafs, text_imlaei_simple,
    search_text, page_number, juz_number, hizb_number
) VALUES
	(111, 1, '111:1', 'تَبَّتْ يَدَا أَبِي لَهَبٍ', 'تبت يدا أبي لهب', 'تبت يدا ابي لهب', 603, 30, 60),
	(112, 1, '112:1', 'قُلْ هُوَ اللَّهُ أَحَدٌ', 'قل هو الله أحد', 'قل هو الله احد', 604, 30, 60)
`)
	require.NoError(t, err)

	initial := quranCitableUnitsJob{}
	remaining, err := initial.CountRemaining(ctx, pg.Pool)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, remaining, int64(2))

	// Resume immediately before the two owned fixture surahs. Other live tests
	// may intentionally leave unrelated surah rows behind on the shared CI DB.
	cursor, processed, done, err := initial.ProcessChunk(ctx, pg.Pool, 110, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(111), cursor)
	assert.Equal(t, int64(1), processed)
	assert.False(t, done)

	cursor, processed, done, err = initial.ProcessChunk(ctx, pg.Pool, cursor, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(112), cursor)
	assert.Equal(t, int64(1), processed)
	assert.False(t, done)

	var targetRemaining int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*) FROM quran_surahs
WHERE surah_id IN (111, 112)
  AND (units_derived_at IS NULL OR units_stale_at IS NOT NULL)`).Scan(&targetRemaining))
	assert.Zero(t, targetRemaining)

	var initialUnits int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM quran_citable_unit_bindings b
JOIN citable_units u ON u.id = b.unit_id
WHERE b.surah_id IN (111, 112) AND u.corpus = 'quran'`).Scan(&initialUnits))
	assert.Equal(t, 2, initialUnits)

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_translation_sources (
    id, lang, name, translator, responsible_name, responsible_role,
    format, license_status
	) VALUES ($1, 'id', 'Q-2 Backfill Translation', 'Fixture Translator',
		      'Fixture Translator', 'translator', 'json', 'needs_review')
`, translationSourceID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayah_translations (
    source_id, surah_id, ayah_number, ayah_key, lang, text
) VALUES ($1, 111, 1, '111:1', 'id', 'Binasalah kedua tangan Abu Lahab')
`, translationSourceID)
	require.NoError(t, err)

	remaining, err = initial.CountRemaining(ctx, pg.Pool)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, remaining, int64(1))
	// The cursor already passed 111. The stale job must wrap instead of falsely
	// reporting completion with this newly stale surah left behind. Other live
	// fixtures may leave a higher pending surah, so drain until the owned row is
	// reached rather than assuming it is the immediate next item.
	wrappedToOwned := false
	for range 114 {
		cursor, processed, done, err = initial.ProcessChunk(ctx, pg.Pool, cursor, 100)
		require.NoError(t, err)
		assert.Equal(t, int64(1), processed)
		assert.False(t, done)
		if cursor == 111 {
			wrappedToOwned = true

			break
		}
	}
	assert.True(t, wrappedToOwned, "stale traversal must wrap to a surah behind the cursor")

	var afterTranslation int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM quran_citable_unit_bindings b
JOIN citable_units u ON u.id = b.unit_id
WHERE b.surah_id IN (111, 112) AND u.corpus = 'quran'`).Scan(&afterTranslation))
	assert.Equal(t, initialUnits+1, afterTranslation)

	rederive := quranCitableUnitsRederiveJob{}
	remaining, err = rederive.CountRemaining(ctx, pg.Pool)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, remaining, int64(2))
	cursor, processed, done, err = rederive.ProcessChunk(ctx, pg.Pool, 110, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(111), cursor)
	assert.Equal(t, int64(1), processed)
	assert.False(t, done)
	cursor, processed, done, err = rederive.ProcessChunk(ctx, pg.Pool, cursor, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(112), cursor)
	assert.Equal(t, int64(1), processed)
	assert.False(t, done)
	var finalUnits int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM quran_citable_unit_bindings b
JOIN citable_units u ON u.id = b.unit_id
WHERE b.surah_id IN (111, 112) AND u.corpus = 'quran'`).Scan(&finalUnits))
	assert.Equal(t, afterTranslation, finalUnits, "rederive must not duplicate deterministic units")
}
