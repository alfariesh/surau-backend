package persistent

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveSaveQuranProgressNoDoubleCount proves G8: concurrent saves for the same
// (user, surah) must not multiply the reading_activity delta. The `old` CTE takes a
// FOR UPDATE lock so the saves serialize and each computes its delta off the other's
// committed ayah_number. Gated on SURAU_LIVE_PG.
//
//	SURAU_LIVE_PG=postgres://... go test -race ./internal/repo/persistent/ -run TestLiveSaveQuranProgressNoDoubleCount -v
//
//nolint:paralleltest // serial live-DB concurrency check over dedicated throwaway rows (gated on SURAU_LIVE_PG)
func TestLiveSaveQuranProgressNoDoubleCount(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	repo := NewPersonalRepo(pg)
	ctx := context.Background()
	userID := "c1111111-1111-1111-1111-111111111111"

	seedLiveUser(t, pg, userID, "quran-progress-race")

	_, err = pg.Pool.Exec(ctx, `INSERT INTO quran_surahs (surah_id, ayah_count) VALUES (2, 286) ON CONFLICT (surah_id) DO NOTHING`)
	require.NoError(t, err)

	for _, n := range []int{1, 20} {
		_, err = pg.Pool.Exec(ctx,
			`INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key) VALUES (2, $1, $2) ON CONFLICT (surah_id, ayah_number) DO NOTHING`, n, fmt.Sprintf("2:%d", n))
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		if _, err := pg.Pool.Exec(context.Background(), `DELETE FROM reading_activity WHERE user_id = $1`, userID); err != nil {
			t.Logf("cleanup reading_activity: %v", err)
		}

		if _, err := pg.Pool.Exec(context.Background(), `DELETE FROM quran_reading_progress WHERE user_id = $1`, userID); err != nil {
			t.Logf("cleanup quran_reading_progress: %v", err)
		}
	})

	// Fixed noon so the +N-second observed_at offsets can never cross a day boundary
	// and split the activity_date bucket.
	base := time.Date(2020, 1, 15, 12, 0, 0, 0, time.UTC)

	// Establish progress at ayah 1 (delta 1).
	_, err = repo.SaveQuranProgress(ctx, entity.QuranReadingProgress{UserID: userID, AyahKey: "2:1", ObservedAt: base})
	require.NoError(t, err)

	// Fire K concurrent saves to ayah 20, released together to maximize overlap.
	const workers = 8

	start := make(chan struct{})
	errs := make(chan error, workers)

	var wg sync.WaitGroup

	for i := range workers {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			<-start

			_, e := repo.SaveQuranProgress(ctx, entity.QuranReadingProgress{
				UserID: userID, AyahKey: "2:20", ObservedAt: base.Add(time.Duration(i+1) * time.Second),
			})
			errs <- e
		}(i)
	}

	close(start)
	wg.Wait()
	close(errs)

	for e := range errs {
		require.NoError(t, e)
	}

	// Correct total: initial gives delta 1, then exactly one 1->20 transition (delta 19);
	// every other concurrent save sees ayah 20 already committed and adds 0. Without the
	// FOR UPDATE lock the concurrent saves would each re-add 19 and inflate the counter.
	var ayahsRead int
	require.NoError(t, pg.Pool.QueryRow(ctx,
		`SELECT quran_ayahs_read FROM reading_activity WHERE user_id = $1 AND activity_date = $2`,
		userID, base.Format("2006-01-02")).Scan(&ayahsRead))
	assert.Equal(t, 20, ayahsRead, "concurrent saves must not double-count quran_ayahs_read")
}
