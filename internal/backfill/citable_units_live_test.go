package backfill

import (
	"context"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Live proof that the B-1 pilot backfill honors the F1-H runner contract
// (pause mid-run, resume without loss, one book per chunk) and that the
// rederive drill is a registry no-op over unchanged sources (AC-1 at the job
// level). Serial against SURAU_LIVE_PG like the other TestLive* suites.

// Fixture ids far outside the real corpus and the real pilot set.
var liveCitableFixtureBooks = []int{990201, 990202} //nolint:gochecknoglobals // test fixture set

func resetLiveCitableState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()
	// Deleting the book cascades citable_units (+ lineage) through the FK;
	// the guard trigger allows referential actions (pg_trigger_depth > 1).
	for _, bookID := range liveCitableFixtureBooks {
		_, err := pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, bookID)
		require.NoError(t, err)
	}

	_, err := pool.Exec(ctx,
		`DELETE FROM backfill_jobs WHERE job_name IN ('citable-units-kitab-pilot', 'citable-units-kitab-rederive')`)
	require.NoError(t, err)
}

func seedLiveCitableBooks(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()
	for _, bookID := range liveCitableFixtureBooks {
		_, err := pool.Exec(ctx, `
			INSERT INTO books (id, name, has_content) VALUES ($1, 'كتاب تجريبي للتشغيل', TRUE)`, bookID)
		require.NoError(t, err)

		for page := 1; page <= 3; page++ {
			_, err = pool.Exec(ctx, `
				INSERT INTO book_pages (book_id, page_id, content_html, content_text)
				VALUES ($1, $2, $3, $3)`,
				bookID, page, "فقرة أولى في الصفحة\nفقرة ثانية في الصفحة")
			require.NoError(t, err)
		}
	}
}

//nolint:paralleltest // serial by design: overrides the pilot set and shares checkpoint rows
func TestLiveCitableBackfillPauseResumeAndRederiveNoop(t *testing.T) {
	pool := liveBackfillPool(t)

	originalPilot := CitablePilotBooks
	CitablePilotBooks = liveCitableFixtureBooks

	t.Cleanup(func() { CitablePilotBooks = originalPilot })

	resetLiveCitableState(t, pool)
	t.Cleanup(func() { resetLiveCitableState(t, pool) })
	seedLiveCitableBooks(t, pool)

	pilotJob, err := ByName("citable-units-kitab-pilot")
	require.NoError(t, err)

	// ---- Run 1: cancel after the first book (graceful pause). ----
	runCtx, cancelRun := context.WithCancel(context.Background())
	runner := &Runner{
		Pool:      pool,
		Log:       logger.New("error"),
		ChunkSize: 1,
		Sleep:     time.Millisecond,
		AfterChunk: func(state State) {
			if state.RowsDone == 1 {
				cancelRun()
			}
		},
	}
	require.NoError(t, runner.Run(runCtx, pilotJob, false))
	cancelRun()

	paused := readLiveCitableState(t, pool, "citable-units-kitab-pilot")
	assert.Equal(t, StatusPaused, paused.Status)
	assert.Equal(t, int64(liveCitableFixtureBooks[0]), paused.LastCursor,
		"pause lands on a book boundary")
	assert.Equal(t, 1, countLiveDerivedBooks(t, pool),
		"exactly one book derived before the pause")

	// ---- Run 2: resume finishes the second book without redoing the first. ----
	runner.AfterChunk = nil
	require.NoError(t, runner.Run(context.Background(), pilotJob, false))

	final := readLiveCitableState(t, pool, "citable-units-kitab-pilot")
	assert.Equal(t, StatusCompleted, final.Status)
	assert.Equal(t, int64(2), final.RowsDone, "both books processed exactly once")
	assert.Equal(t, 2, countLiveDerivedBooks(t, pool))

	unitsPerBook := 6 // 2 paragraphs × 3 pages

	for _, bookID := range liveCitableFixtureBooks {
		var n int
		require.NoError(t, pool.QueryRow(context.Background(),
			`SELECT count(*) FROM citable_units WHERE book_id = $1 AND lifecycle = 'active'`, bookID).Scan(&n))
		assert.Equal(t, unitsPerBook, n, "book %d unit count", bookID)
	}

	// Sources unchanged → the pilot job's staleness predicate reports nothing
	// left to do.
	remaining, err := pilotJob.CountRemaining(context.Background(), pool)
	require.NoError(t, err)
	assert.Zero(t, remaining)

	// ---- Rederive drill: unconditional re-run must leave the registry
	// byte-identical (AC-1 at job level). ----
	before := liveCitableRegistryDump(t, pool)

	rederiveJob, err := ByName("citable-units-kitab-rederive")
	require.NoError(t, err)

	drill := &Runner{Pool: pool, Log: logger.New("error"), ChunkSize: 1, Sleep: time.Millisecond}
	require.NoError(t, drill.Run(context.Background(), rederiveJob, false))

	drillState := readLiveCitableState(t, pool, "citable-units-kitab-rederive")
	assert.Equal(t, StatusCompleted, drillState.Status)
	assert.Equal(t, int64(2), drillState.RowsDone)

	after := liveCitableRegistryDump(t, pool)
	assert.Equal(t, before, after,
		"rederive over unchanged sources must not change a single registry row")
}

func readLiveCitableState(t *testing.T, pool *pgxpool.Pool, jobName string) State {
	t.Helper()

	var s State
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT job_name, status, last_cursor, rows_total, rows_done
		FROM backfill_jobs WHERE job_name = $1`, jobName).
		Scan(&s.JobName, &s.Status, &s.LastCursor, &s.RowsTotal, &s.RowsDone))

	return s
}

func countLiveDerivedBooks(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()

	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM books WHERE id = ANY($1) AND units_derived_at IS NOT NULL`,
		liveCitableFixtureBooks).Scan(&n))

	return n
}

// liveCitableRegistryDump snapshots every registry column that must survive a
// rederive unchanged (everything except books.units_derived_at).
func liveCitableRegistryDump(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	var dump string
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT COALESCE(string_agg(
			id::text || '|' || anchor || '|' || ordinal || '|' || position || '|' ||
			kind || '|' || lifecycle || '|' || encode(content_hash, 'hex') || '|' ||
			occurrence || '|' || updated_at::text, E'\n' ORDER BY id), '')
		FROM citable_units WHERE book_id = ANY($1)`, liveCitableFixtureBooks).Scan(&dump))

	return dump
}
