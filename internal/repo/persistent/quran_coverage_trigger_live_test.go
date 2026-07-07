package persistent

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveCoverageCountTrigger verifies the coverage_count triggers added in
// 20260628000000: an INSERT into quran_ayah_translations increments the source's
// coverage_count, a DELETE decrements it (floored at 0), and a cascade delete of the
// source (which cascades to its translations) does not error. Gated on SURAU_LIVE_PG.
//
//	SURAU_LIVE_PG=postgres://... go test ./internal/repo/persistent/ -run TestLiveCoverageCountTrigger -v
//
//nolint:paralleltest // serial live-DB integration check over dedicated throwaway rows (gated on SURAU_LIVE_PG)
func TestLiveCoverageCountTrigger(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)

	defer pg.Close()

	ctx := context.Background()

	const (
		surahID  = 113 // Al-Falaq exists; we only touch throwaway ayah_numbers below.
		sourceID = "coverage-trigger-test-source"
	)

	ayahNumbers := []int{901, 902, 903}

	cleanup := func() {
		// Deleting the source cascades to its translations; then remove the throwaway ayahs.
		if _, err := pg.Pool.Exec(ctx, `DELETE FROM quran_translation_sources WHERE id = $1`, sourceID); err != nil {
			t.Logf("cleanup source: %v", err)
		}

		for _, n := range ayahNumbers {
			if _, err := pg.Pool.Exec(ctx, `DELETE FROM quran_ayahs WHERE surah_id = $1 AND ayah_number = $2`, surahID, n); err != nil {
				t.Logf("cleanup ayah %d: %v", n, err)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	_, err = pg.Pool.Exec(ctx, `INSERT INTO quran_surahs (surah_id, ayah_count) VALUES ($1, 5) ON CONFLICT (surah_id) DO NOTHING`, surahID)
	require.NoError(t, err)

	for _, n := range ayahNumbers {
		_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key, metadata)
VALUES ($1, $2, $3, '{}'::jsonb)
ON CONFLICT (surah_id, ayah_number) DO NOTHING`, surahID, n, fmt.Sprintf("%d:%d", surahID, n))
		require.NoError(t, err)
	}

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_translation_sources (id, lang, name, format, license_status, coverage_count)
VALUES ($1, 'id', 'Coverage Trigger Test', 'json', 'permitted', 0)`, sourceID)
	require.NoError(t, err)

	coverage := func() int {
		var count int
		require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT coverage_count FROM quran_translation_sources WHERE id = $1`, sourceID).Scan(&count))

		return count
	}

	assert.Equal(t, 0, coverage(), "fresh source starts at 0")

	// INSERT trigger increments once per new translation row.
	for _, n := range ayahNumbers {
		_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayah_translations (source_id, surah_id, ayah_number, ayah_key, lang, text)
VALUES ($1, $2, $3, $4, 'id', 'terjemah')`, sourceID, surahID, n, fmt.Sprintf("%d:%d", surahID, n))
		require.NoError(t, err)
	}

	assert.Equal(t, len(ayahNumbers), coverage(), "3 inserts → coverage_count = 3")

	// DELETE trigger decrements.
	_, err = pg.Pool.Exec(ctx, `DELETE FROM quran_ayah_translations WHERE source_id = $1 AND surah_id = $2 AND ayah_number = $3`, sourceID, surahID, ayahNumbers[0])
	require.NoError(t, err)
	assert.Equal(t, len(ayahNumbers)-1, coverage(), "1 delete → coverage_count = 2")

	// Deleting the source cascades to the remaining translations without error, and the
	// GREATEST(0, ...) floor keeps the (now-gone) count from going negative.
	_, err = pg.Pool.Exec(ctx, `DELETE FROM quran_translation_sources WHERE id = $1`, sourceID)
	require.NoError(t, err, "cascade delete of source + translations must not error")

	var remaining int
	require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM quran_ayah_translations WHERE source_id = $1`, sourceID).Scan(&remaining))
	assert.Equal(t, 0, remaining, "cascade removed the translations")
}
