package backfill

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Live proof of the F1-H acceptance criterion: a real backfill can be
// STOPPED mid-run and RESUMED without losing progress and without
// reprocessing rows. Runs serially against SURAU_LIVE_PG (like the other
// TestLive* suites).

const (
	liveBackfillSeedBase  = int64(910_000_000)
	liveBackfillSeedCount = 500
)

func liveBackfillPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pool, err := pgxpool.New(context.Background(), url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}

func resetLiveBackfillState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()

	_, err := pool.Exec(ctx, `DELETE FROM authors WHERE id >= $1 AND id < $2`,
		liveBackfillSeedBase, liveBackfillSeedBase+10_000)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `DELETE FROM backfill_jobs WHERE job_name = 'authors-name-search'`)
	require.NoError(t, err)
}

func seedLiveBackfillAuthors(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()

	for start := 0; start < liveBackfillSeedCount; start += 100 {
		batch := &pgxBatchInsert{}
		for i := start; i < start+100 && i < liveBackfillSeedCount; i++ {
			batch.add(
				liveBackfillSeedBase+int64(i),
				fmt.Sprintf("مؤلف أحكام التجربة رقم %d", i),
			)
		}

		sqlText, args := batch.sql()
		_, err := pool.Exec(ctx, sqlText, args...)
		require.NoError(t, err)
	}
}

// pgxBatchInsert builds a multi-row INSERT for the synthetic authors.
type pgxBatchInsert struct {
	ids   []int64
	names []string
}

func (b *pgxBatchInsert) add(id int64, name string) {
	b.ids = append(b.ids, id)
	b.names = append(b.names, name)
}

func (b *pgxBatchInsert) sql() (sqlText string, args []any) {
	var values strings.Builder

	args = make([]any, 0, len(b.ids)*argsPerRow)

	for i := range b.ids {
		if i > 0 {
			values.WriteString(", ")
		}

		idIdx := len(args) + 1
		nameIdx := idIdx + 1

		fmt.Fprintf(&values, "(($%d)::int, ($%d)::text)", idIdx, nameIdx)

		args = append(args, b.ids[i], b.names[i])
	}

	return "INSERT INTO authors (id, name) VALUES " + values.String(), args
}

//nolint:paralleltest // serial by design: shares the authors-name-search checkpoint row and advisory lock with the other live test
func TestLiveBackfillAuthorsPauseResumeWithoutLoss(t *testing.T) {
	pool := liveBackfillPool(t)

	resetLiveBackfillState(t, pool)
	t.Cleanup(func() { resetLiveBackfillState(t, pool) })

	seedLiveBackfillAuthors(t, pool)

	job, err := ByName("authors-name-search")
	require.NoError(t, err)

	// Snapshot how many authors outside our seed still need the backfill —
	// on a corpus DB the job processes them too, so totals are relative.
	var preexistingPending int64
	require.NoError(t, pool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM authors WHERE name_search IS NULL AND id < $1`, liveBackfillSeedBase,
	).Scan(&preexistingPending))

	// ---- Run 1: cancel after 3 chunks (graceful pause). ----
	runCtx, cancelRun := context.WithCancel(context.Background())

	chunks := 0
	firstRunDone := int64(0)
	runner := &Runner{
		Pool:      pool,
		Log:       logger.New("error"),
		ChunkSize: 50,
		Sleep:     time.Millisecond,
		AfterChunk: func(state State) {
			chunks++
			firstRunDone = state.RowsDone

			if chunks == 3 {
				cancelRun()
			}
		},
	}

	require.NoError(t, runner.Run(runCtx, job, false))
	cancelRun()

	pausedState := readLiveBackfillState(t, pool)
	assert.Equal(t, StatusPaused, pausedState.Status)
	assert.Positive(t, pausedState.LastCursor, "pause must keep the cursor")
	assert.Equal(t, firstRunDone, pausedState.RowsDone)
	require.GreaterOrEqual(t, firstRunDone, int64(150), "three chunks of 50 should be checkpointed")

	var filledAfterPause int64
	require.NoError(t, pool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM authors WHERE name_search IS NOT NULL AND id >= $1`, liveBackfillSeedBase,
	).Scan(&filledAfterPause))
	assert.Less(t, filledAfterPause, int64(liveBackfillSeedCount), "pause must leave work remaining")

	// ---- Run 2: resume; must finish the rest WITHOUT redoing done rows. ----
	secondRunProcessed := int64(0)
	runner.AfterChunk = func(state State) { secondRunProcessed = state.RowsDone }

	require.NoError(t, runner.Run(context.Background(), job, false))

	finalState := readLiveBackfillState(t, pool)
	assert.Equal(t, StatusCompleted, finalState.Status)

	expectedTotal := preexistingPending + int64(liveBackfillSeedCount)
	assert.Equal(t, expectedTotal, finalState.RowsDone,
		"every pending row processed exactly once across both runs (no loss, no redo)")
	assert.Equal(t, expectedTotal, secondRunProcessed)
	assert.Greater(t, finalState.LastCursor, pausedState.LastCursor,
		"resume must continue past the paused cursor, not restart from zero")

	// Every seeded author is filled with the canonical normalized form.
	var mismatched int64
	require.NoError(t, pool.QueryRow(
		context.Background(), `
SELECT count(*) FROM authors
WHERE id >= $1 AND id < $2 AND name_search IS NULL`,
		liveBackfillSeedBase, liveBackfillSeedBase+int64(liveBackfillSeedCount),
	).Scan(&mismatched))
	assert.Zero(t, mismatched, "all seeded authors must be filled")

	var sampleNormalized string
	require.NoError(t, pool.QueryRow(
		context.Background(),
		`SELECT name_search FROM authors WHERE id = $1`, liveBackfillSeedBase,
	).Scan(&sampleNormalized))
	assert.Equal(t, searchtext.Normalize("مؤلف أحكام التجربة رقم 0"), sampleNormalized)

	// A completed job refuses to run again without -restart.
	err = runner.Run(context.Background(), job, false)
	require.ErrorIs(t, err, ErrJobCompleted)
}

//nolint:paralleltest // serial by design: takes the same advisory lock the resume test's runner uses
func TestLiveBackfillAdvisoryLockBlocksSecondInstance(t *testing.T) {
	pool := liveBackfillPool(t)

	resetLiveBackfillState(t, pool)
	t.Cleanup(func() { resetLiveBackfillState(t, pool) })

	// Hold the job's advisory lock on a dedicated conn, as a concurrent
	// instance would.
	conn, err := pool.Acquire(context.Background())
	require.NoError(t, err)

	defer conn.Release()

	var locked bool
	require.NoError(t, conn.QueryRow(
		context.Background(),
		`SELECT pg_try_advisory_lock($1)`, advisoryLockKey("authors-name-search"),
	).Scan(&locked))
	require.True(t, locked)

	defer func() {
		_, _ = conn.Exec(context.Background(), //nolint:errcheck // best-effort unlock; conn release drops it anyway
			`SELECT pg_advisory_unlock($1)`, advisoryLockKey("authors-name-search"))
	}()

	job, err := ByName("authors-name-search")
	require.NoError(t, err)

	runner := &Runner{Pool: pool, Log: logger.New("error")}

	err = runner.Run(context.Background(), job, false)
	require.ErrorIs(t, err, ErrLockHeld)
}

func readLiveBackfillState(t *testing.T, pool *pgxpool.Pool) State {
	t.Helper()

	state := State{JobName: "authors-name-search"}
	require.NoError(t, pool.QueryRow(context.Background(), `
SELECT status, last_cursor, rows_total, rows_done
FROM backfill_jobs
WHERE job_name = 'authors-name-search'`).
		Scan(&state.Status, &state.LastCursor, &state.RowsTotal, &state.RowsDone))

	return state
}
