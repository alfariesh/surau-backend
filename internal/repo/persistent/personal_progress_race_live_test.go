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

	_, err = pg.Pool.Exec(ctx, `INSERT INTO quran_surahs (surah_id, ayah_count) VALUES (2, 286) ON CONFLICT (surah_id) DO NOTHING`)
	require.NoError(t, err)

	for _, n := range []int{1, 20} {
		_, err = pg.Pool.Exec(ctx,
			`INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key) VALUES (2, $1, $2) ON CONFLICT (surah_id, ayah_number) DO NOTHING`, n, fmt.Sprintf("2:%d", n))
		require.NoError(t, err)
	}

	// Fixed noon so the +N-second observed_at offsets can never cross a day boundary
	// and split the activity_date bucket.
	base := time.Date(2020, 1, 15, 12, 0, 0, 0, time.UTC)

	const workers = 8

	// runSaves performs the identical sequence — an initial save to 2:1, then `workers`
	// saves to 2:20 with increasing observed_at — either sequentially or concurrently,
	// and returns the day's accumulated quran_ayahs_read for that user.
	runSaves := func(userID, suffix string, concurrent bool) int {
		seedLiveUser(t, pg, userID, suffix)
		t.Cleanup(func() {
			// Runs before seedLiveUser's user-delete (LIFO): clear children first.
			if _, err := pg.Pool.Exec(context.Background(), `DELETE FROM reading_activity WHERE user_id = $1`, userID); err != nil {
				t.Logf("cleanup reading_activity: %v", err)
			}

			if _, err := pg.Pool.Exec(context.Background(), `DELETE FROM quran_reading_progress WHERE user_id = $1`, userID); err != nil {
				t.Logf("cleanup quran_reading_progress: %v", err)
			}
		})

		_, err := repo.SaveQuranProgress(ctx, entity.QuranReadingProgress{UserID: userID, AyahKey: "2:1", ObservedAt: base})
		require.NoError(t, err)

		save := func(i int) error {
			_, e := repo.SaveQuranProgress(ctx, entity.QuranReadingProgress{
				UserID: userID, AyahKey: "2:20", ObservedAt: base.Add(time.Duration(i+1) * time.Second),
			})

			return e
		}

		if concurrent {
			start := make(chan struct{})
			errs := make(chan error, workers)

			var wg sync.WaitGroup

			for i := range workers {
				wg.Add(1)

				go func(i int) {
					defer wg.Done()

					<-start

					errs <- save(i)
				}(i)
			}

			close(start)
			wg.Wait()
			close(errs)

			for e := range errs {
				require.NoError(t, e)
			}
		} else {
			for i := range workers {
				require.NoError(t, save(i))
			}
		}

		var ayahsRead int
		require.NoError(t, pg.Pool.QueryRow(ctx,
			`SELECT quran_ayahs_read FROM reading_activity WHERE user_id = $1 AND activity_date = $2`,
			userID, base.Format("2006-01-02")).Scan(&ayahsRead))

		return ayahsRead
	}

	// The concurrent run must not accumulate MORE than the same sequence run serially —
	// that upward drift is exactly the G8 double-count. (Without FOR UPDATE the concurrent
	// saves read a shared stale baseline and each re-add the delta, inflating the counter.)
	seqCount := runSaves("c1111111-1111-1111-1111-111111111111", "quran-seq", false)
	conCount := runSaves("c2222222-2222-2222-2222-222222222222", "quran-con", true)

	t.Logf("quran_ayahs_read: sequential=%d concurrent=%d", seqCount, conCount)
	assert.Positive(t, seqCount, "sequential baseline should count some ayahs")
	assert.LessOrEqual(t, conCount, seqCount, "concurrent saves must not accumulate more than sequential (no double-count)")
}
