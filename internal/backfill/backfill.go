// Package backfill implements the F1-H resumable-backfill runner: chunked
// work over big tables with a per-chunk checkpoint in the backfill_jobs
// table, a Postgres advisory lock so only one instance of a job can run,
// graceful pause on SIGINT/SIGTERM (context cancel), and resume-from-cursor
// on the next invocation. Operational guide: docs/data-change-playbook.md.
package backfill

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Job statuses persisted in backfill_jobs.status.
const (
	StatusRunning   = "running"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// Default pacing: chunks small enough to keep row locks short, throttled so
// a backfill never monopolizes the database (no public-endpoint downtime).
const (
	DefaultChunkSize = 100
	DefaultSleep     = 200 * time.Millisecond

	postCancelOpTimeout = 5 * time.Second
)

var (
	// ErrLockHeld means another instance of the same job holds the advisory lock.
	ErrLockHeld = errors.New("backfill job already running elsewhere (advisory lock held)")
	// ErrJobCompleted means the job already completed; use restart to run again.
	ErrJobCompleted = errors.New("backfill job already completed (use -restart to run again)")
	// ErrJobUnknown means the job name is not registered.
	ErrJobUnknown = errors.New("unknown backfill job")
)

// Job is one resumable backfill unit. Implementations must be idempotent:
// processing only rows that still need work (e.g. WHERE derived IS NULL),
// keyset-ordered by an integer PK so an int64 cursor can resume them.
type Job interface {
	Name() string
	// ProfileVersion is stored on the checkpoint so profile changes are
	// visible and re-runnable (0 when not applicable).
	ProfileVersion() int
	// CountRemaining reports rows still needing work (start total + drift metric).
	CountRemaining(ctx context.Context, pool *pgxpool.Pool) (int64, error)
	// ProcessChunk handles up to limit pending rows with PK > cursor in PK
	// order, returning the new cursor, rows actually processed, and
	// done=true when nothing pending remains past the new cursor.
	ProcessChunk(ctx context.Context, pool *pgxpool.Pool, cursor int64, limit int) (int64, int64, bool, error)
}

// State is the persisted checkpoint snapshot handed to AfterChunk.
type State struct {
	JobName    string
	Status     string
	LastCursor int64
	RowsTotal  int64
	RowsDone   int64
}

// Runner drives one Job to completion (or pause) against Pool.
type Runner struct {
	Pool      *pgxpool.Pool
	Log       logger.Interface
	ChunkSize int
	Sleep     time.Duration
	// AfterChunk, when set, observes every persisted checkpoint (CLI
	// progress lines; tests use it to cancel mid-run).
	AfterChunk func(State)
}

// Run executes job until done or ctx is canceled. Cancellation is a
// graceful pause: the checkpoint keeps the last finished chunk and status
// becomes "paused"; a later Run resumes from the stored cursor.
func (r *Runner) Run(ctx context.Context, job Job, restart bool) error {
	chunk := r.ChunkSize
	if chunk <= 0 {
		chunk = DefaultChunkSize
	}

	sleep := r.Sleep
	if sleep < 0 {
		sleep = DefaultSleep
	}

	releaseLock, err := r.acquireLock(ctx, job.Name())
	if err != nil {
		return err
	}
	defer releaseLock()

	state, err := r.loadOrInitState(ctx, job, restart)
	if err != nil {
		return err
	}

	r.Log.Info(
		"backfill - %s: starting (cursor=%d done=%d/%d chunk=%d sleep=%s)",
		state.JobName, state.LastCursor, state.RowsDone, state.RowsTotal, chunk, sleep,
	)

	return r.runLoop(ctx, job, state, chunk, sleep)
}

func (r *Runner) runLoop(ctx context.Context, job Job, state State, chunk int, sleep time.Duration) error {
	for {
		if ctx.Err() != nil {
			return r.pause(state)
		}

		cursor, processed, done, err := job.ProcessChunk(ctx, r.Pool, state.LastCursor, chunk)
		if err != nil {
			if ctx.Err() != nil {
				return r.pause(state)
			}

			return r.fail(state, err)
		}

		state.LastCursor = cursor
		state.RowsDone += processed

		if done {
			return r.complete(state)
		}

		if err := r.checkpointChunk(ctx, &state); err != nil {
			return r.fail(state, err)
		}

		if err := sleepCtx(ctx, sleep); err != nil {
			return r.pause(state)
		}
	}
}

// checkpointChunk persists the just-finished chunk and notifies AfterChunk.
func (r *Runner) checkpointChunk(ctx context.Context, state *State) error {
	if err := r.saveCheckpoint(ctx, *state, StatusRunning); err != nil {
		return err
	}

	if r.AfterChunk != nil {
		state.Status = StatusRunning
		r.AfterChunk(*state)
	}

	return nil
}

func (r *Runner) pause(state State) error {
	// ctx is already canceled here — persist with a fresh short deadline.
	ctx, cancel := context.WithTimeout(context.Background(), postCancelOpTimeout)
	defer cancel()

	if err := r.saveCheckpoint(ctx, state, StatusPaused); err != nil {
		return fmt.Errorf("backfill %s: persist pause: %w", state.JobName, err)
	}

	r.Log.Info(
		"backfill - %s: paused at cursor=%d done=%d/%d — rerun the same command to resume",
		state.JobName, state.LastCursor, state.RowsDone, state.RowsTotal,
	)

	return nil
}

func (r *Runner) complete(state State) error {
	ctx, cancel := context.WithTimeout(context.Background(), postCancelOpTimeout)
	defer cancel()

	_, err := r.Pool.Exec(ctx, `
UPDATE backfill_jobs
SET status = $2, last_cursor = $3, rows_done = $4, error = NULL,
    updated_at = now(), finished_at = now()
WHERE job_name = $1`,
		state.JobName, StatusCompleted, state.LastCursor, state.RowsDone)
	if err != nil {
		return fmt.Errorf("backfill %s: persist completion: %w", state.JobName, err)
	}

	if r.AfterChunk != nil {
		state.Status = StatusCompleted
		r.AfterChunk(state)
	}

	r.Log.Info("backfill - %s: completed (rows_done=%d)", state.JobName, state.RowsDone)

	return nil
}

func (r *Runner) fail(state State, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), postCancelOpTimeout)
	defer cancel()

	_, err := r.Pool.Exec(ctx, `
UPDATE backfill_jobs
SET status = $2, last_cursor = $3, rows_done = $4, error = $5, updated_at = now()
WHERE job_name = $1`,
		state.JobName, StatusFailed, state.LastCursor, state.RowsDone, cause.Error())
	if err != nil {
		return errors.Join(cause, fmt.Errorf("backfill %s: persist failure: %w", state.JobName, err))
	}

	return fmt.Errorf("backfill %s: %w", state.JobName, cause)
}

func (r *Runner) saveCheckpoint(ctx context.Context, state State, status string) error {
	_, err := r.Pool.Exec(ctx, `
UPDATE backfill_jobs
SET status = $2, last_cursor = $3, rows_done = $4, updated_at = now()
WHERE job_name = $1`,
		state.JobName, status, state.LastCursor, state.RowsDone)
	if err != nil {
		return fmt.Errorf("backfill %s: save checkpoint: %w", state.JobName, err)
	}

	return nil
}

// acquireLock takes the per-job advisory lock on a DEDICATED connection —
// advisory locks are session-scoped, so the lock must live on a conn we hold
// for the whole run, not on an arbitrary pooled one. The returned release
// unlocks first and only then returns the conn to the pool (order matters:
// a released conn must never be used again).
func (r *Runner) acquireLock(ctx context.Context, jobName string) (func(), error) {
	conn, err := r.Pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("backfill %s: acquire conn: %w", jobName, err)
	}

	key := advisoryLockKey(jobName)

	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		conn.Release()

		return nil, fmt.Errorf("backfill %s: try lock: %w", jobName, err)
	}

	if !locked {
		conn.Release()

		return nil, fmt.Errorf("backfill %s: %w", jobName, ErrLockHeld)
	}

	release := func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), postCancelOpTimeout)
		defer cancel()

		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, key) //nolint:errcheck // best-effort: releasing the conn drops the session lock anyway
		conn.Release()
	}

	return release, nil
}

// loadOrInitState upserts the checkpoint row: new jobs start at cursor 0;
// paused/failed/stale-running jobs resume from the stored cursor with a
// refreshed total; completed jobs refuse to run unless restart is set.
func (r *Runner) loadOrInitState(ctx context.Context, job Job, restart bool) (State, error) {
	state := State{JobName: job.Name()}

	found := true

	err := r.Pool.QueryRow(ctx, `
SELECT status, last_cursor, rows_total, rows_done
FROM backfill_jobs
WHERE job_name = $1`, job.Name()).
		Scan(&state.Status, &state.LastCursor, &state.RowsTotal, &state.RowsDone)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		found = false
	case err != nil:
		return State{}, fmt.Errorf("backfill %s: load checkpoint: %w", job.Name(), err)
	}

	if found && state.Status == StatusCompleted && !restart {
		return State{}, fmt.Errorf("backfill %s: %w", job.Name(), ErrJobCompleted)
	}

	if !found || restart {
		state.LastCursor = 0
		state.RowsDone = 0
	}

	remaining, err := job.CountRemaining(ctx, r.Pool)
	if err != nil {
		return State{}, fmt.Errorf("backfill %s: count remaining: %w", job.Name(), err)
	}

	state.RowsTotal = state.RowsDone + remaining
	state.Status = StatusRunning

	_, err = r.Pool.Exec(ctx, `
INSERT INTO backfill_jobs (job_name, status, last_cursor, rows_total, rows_done, profile_version, error, started_at, updated_at, finished_at)
VALUES ($1, $2, $3, $4, $5, $6, NULL, now(), now(), NULL)
ON CONFLICT (job_name) DO UPDATE SET
    status = EXCLUDED.status,
    last_cursor = EXCLUDED.last_cursor,
    rows_total = EXCLUDED.rows_total,
    rows_done = EXCLUDED.rows_done,
    profile_version = EXCLUDED.profile_version,
    error = NULL,
    started_at = CASE WHEN backfill_jobs.status = 'completed' OR backfill_jobs.rows_done = 0 THEN now() ELSE backfill_jobs.started_at END,
    updated_at = now(),
    finished_at = NULL`,
		state.JobName, StatusRunning, state.LastCursor, state.RowsTotal, state.RowsDone, job.ProfileVersion())
	if err != nil {
		return State{}, fmt.Errorf("backfill %s: init checkpoint: %w", job.Name(), err)
	}

	return state, nil
}

// advisoryLockKey derives a stable int64 advisory-lock key from the job name.
func advisoryLockKey(jobName string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("surau-backfill:" + jobName))

	return int64(h.Sum64()) //nolint:gosec // deliberate wrap-around: stable opaque lock key
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
