package importer

import (
	"context"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveQuranAssetIdentityPreflight proves a re-import that carries a
// different primary Quran text fails before the importer writes any source or
// corpus row. The database immutability trigger is exercised by the registry
// live suite as an independent second layer.
//
//nolint:paralleltest,wsl_v5 // serial self-owned live fixture
func TestLiveQuranAssetIdentityPreflight(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pool, err := pgxpool.New(t.Context(), databaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	ctx := context.Background()
	const (
		surahID = 109
		ayahKey = "109:999"
	)
	var existing int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM quran_surahs WHERE surah_id = $1`, surahID).Scan(&existing))
	if existing != 0 {
		t.Skip("Q-2 importer safety fixture requires unowned surah 109")
	}

	t.Cleanup(func() {
		_, cleanupErr := pool.Exec(context.Background(), `DELETE FROM quran_surahs WHERE surah_id = $1`, surahID)
		assert.NoError(t, cleanupErr)
	})
	_, err = pool.Exec(ctx, `
INSERT INTO quran_surahs (surah_id, name_latin, ayah_count)
	VALUES ($1, 'Q-2 import safety', 1)`, surahID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key, text_qpc_hafs)
VALUES ($1, 999, $2, 'نص أصلي ثابت')`, surahID, ayahKey)
	require.NoError(t, err)

	drift := "نص مستورد خاطئ"
	assets := quranAssetSet{
		ayahs: map[string]*quranAyahImport{
			ayahKey: {SurahID: surahID, AyahNumber: 999, AyahKey: ayahKey, TextQPCHafs: &drift},
		},
		translations:           map[string]*quranTranslationImport{},
		transliterationSources: map[string]*quranTransliterationSourceImport{},
		checksums:              map[string]string{},
	}
	opts := QuranAssetOptions{}.withDefaults()
	err = preflightQuranAssetIdentity(ctx, pool, &opts, &assets)
	assert.ErrorIs(t, err, entity.ErrQuranPrimaryTextDrift)

	var stored string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT text_qpc_hafs FROM quran_ayahs WHERE ayah_key = $1`, ayahKey).Scan(&stored))
	assert.Equal(t, "نص أصلي ثابت", stored)
}
