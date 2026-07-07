package persistent

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveDefaultRecitationPrefersFullerCoverage proves F01 end-to-end on real
// Postgres: coverage_percent is computed from playable tracks, a fuller recitation
// outranks a 1-track one, and defaultPlayableRecitationID never returns the 1-track
// recitation while a fuller one exists. Gated on SURAU_LIVE_PG.
//
//nolint:paralleltest // serial live-DB check over dedicated throwaway rows (gated on SURAU_LIVE_PG)
func TestLiveDefaultRecitationPrefersFullerCoverage(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	repo := NewQuranRepo(pg)
	ctx := context.Background()

	const surahID = 113

	fullID, partialID := "f01-full-cov", "f01-partial-cov"
	ayahNumbers := []int{1, 2, 3, 4, 5}

	cleanup := func() {
		for _, id := range []string{fullID, partialID} {
			if _, err := pg.Pool.Exec(ctx, `DELETE FROM quran_recitations WHERE id = $1`, id); err != nil {
				t.Logf("cleanup recitation %s: %v", id, err)
			}
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
		_, err = pg.Pool.Exec(ctx,
			`INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key, metadata) VALUES ($1, $2, $3, '{}'::jsonb) ON CONFLICT DO NOTHING`,
			surahID, n, fmt.Sprintf("%d:%d", surahID, n))
		require.NoError(t, err)
	}

	insertRecitation := func(id string, playableAyahs []int) {
		_, err := pg.Pool.Exec(ctx,
			`INSERT INTO quran_recitations (id, name, mode, format, license_status) VALUES ($1, $1, 'ayah', 'json', 'permitted')`, id)
		require.NoError(t, err)

		for _, n := range playableAyahs {
			_, err := pg.Pool.Exec(ctx,
				`INSERT INTO quran_audio_tracks (recitation_id, track_type, track_key, surah_id, ayah_number, audio_url)
				 VALUES ($1, 'ayah', $2, $3, $4, $5)`,
				id, fmt.Sprintf("%d:%d", surahID, n), surahID, n, fmt.Sprintf("https://example.test/%d.mp3", n))
			require.NoError(t, err)
		}
	}
	insertRecitation(fullID, ayahNumbers) // 5 playable tracks
	insertRecitation(partialID, []int{1}) // 1 playable track

	recitations, err := repo.ListRecitations(ctx)
	require.NoError(t, err)

	var full, partial *entity.QuranRecitation

	for i := range recitations {
		switch recitations[i].ID {
		case fullID:
			full = &recitations[i]
		case partialID:
			partial = &recitations[i]
		}
	}

	require.NotNil(t, full, "full recitation must be listed")
	require.NotNil(t, partial, "partial recitation must be listed")
	assert.Equal(t, 5, full.PlayableTrackCount)
	assert.Equal(t, 1, partial.PlayableTrackCount)
	// coverage_percent = playable_tracks / total_ayahs, so fuller > partial regardless
	// of the live DB's total ayah count.
	assert.Greater(t, full.CoveragePercent, partial.CoveragePercent, "fuller recitation must have higher coverage")

	// A 1-track recitation can never win the default while the fuller one exists.
	defaultID, err := repo.defaultPlayableRecitationID(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, partialID, defaultID, "a 1-track recitation must not become the default over a fuller one")
}
