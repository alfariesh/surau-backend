package importer

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveQuranAudioImportAtomicity verifies upsertQuranAudio is transactional: a
// failure while writing segments must roll back the recitation and tracks too, so a
// half-imported recitation (playable tracks, no segments) is never left behind. Gated
// on SURAU_LIVE_PG.
//
//	SURAU_LIVE_PG=postgres://... go test ./internal/importer/ -run TestLiveQuranAudioImportAtomicity -v
//
//nolint:paralleltest // serial live-DB integration check over dedicated throwaway rows (gated on SURAU_LIVE_PG)
func TestLiveQuranAudioImportAtomicity(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)

	defer pg.Close()

	ctx := context.Background()
	audioURLPtr := func(s string) *string { return &s }
	ayahPtr := func(n int) *int { return &n }

	const (
		surahID = 113 // Al-Falaq exists; we only touch throwaway ayah_numbers below.
		recID   = "audio-tx-test-recitation"
	)

	ayahNumbers := []int{901, 902}

	cleanup := func() {
		// Deleting the recitation cascades to its tracks + segments.
		if _, err := pg.Pool.Exec(ctx, `DELETE FROM quran_recitations WHERE id = $1`, recID); err != nil {
			t.Logf("cleanup recitation: %v", err)
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

	opts := QuranAssetOptions{LicenseStatus: "permitted"}

	recitationExists := func() bool {
		var exists bool
		require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM quran_recitations WHERE id = $1)`, recID).Scan(&exists))

		return exists
	}

	t.Run("segment FK failure rolls back recitation + tracks", func(t *testing.T) {
		assets := quranAssetSet{
			recitations: map[string]*quranRecitationImport{
				recID: {ID: recID, Name: "TX Test", Mode: "ayah", Format: "json"},
			},
			audioTracks: map[string]*quranAudioTrackImport{
				recID + ":ayah:113:901": {
					RecitationID: recID, TrackType: "ayah", TrackKey: "113:901",
					SurahID: surahID, AyahNumber: ayahPtr(901), AudioURL: audioURLPtr("https://example.test/901.mp3"),
				},
			},
			// This segment points at track_key "113:902", which was NOT inserted as a
			// track → FK violation on quran_audio_segments → the whole import must abort.
			audioSegments: []quranAudioSegmentImport{
				{RecitationID: recID, TrackType: "ayah", TrackKey: "113:902", SurahID: surahID, AyahNumber: 902, SegmentIndex: 1, TimestampFromMS: 0, TimestampToMS: 100},
			},
		}

		err := upsertQuranAudio(ctx, pg.Pool, opts, assets)
		require.Error(t, err, "segment FK violation must surface as an error")
		assert.False(t, recitationExists(), "recitation must be rolled back, not left half-imported")
	})

	t.Run("valid import commits recitation + track + segment", func(t *testing.T) {
		assets := quranAssetSet{
			recitations: map[string]*quranRecitationImport{
				recID: {ID: recID, Name: "TX Test", Mode: "ayah", Format: "json"},
			},
			audioTracks: map[string]*quranAudioTrackImport{
				recID + ":ayah:113:901": {
					RecitationID: recID, TrackType: "ayah", TrackKey: "113:901",
					SurahID: surahID, AyahNumber: ayahPtr(901), AudioURL: audioURLPtr("https://example.test/901.mp3"),
				},
			},
			audioSegments: []quranAudioSegmentImport{
				{RecitationID: recID, TrackType: "ayah", TrackKey: "113:901", SurahID: surahID, AyahNumber: 901, SegmentIndex: 1, TimestampFromMS: 0, TimestampToMS: 100},
			},
		}

		require.NoError(t, upsertQuranAudio(ctx, pg.Pool, opts, assets))
		assert.True(t, recitationExists(), "valid import must commit the recitation")

		var trackCount, segCount int
		require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM quran_audio_tracks WHERE recitation_id = $1`, recID).Scan(&trackCount))
		require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM quran_audio_segments WHERE recitation_id = $1`, recID).Scan(&segCount))
		assert.Equal(t, 1, trackCount)
		assert.Equal(t, 1, segCount)
	})
}
