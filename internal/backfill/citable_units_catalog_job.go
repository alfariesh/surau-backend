package backfill

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Masterminds/squirrel"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	citableCatalogJobName         = "citable-units-kitab-catalog"
	citableCatalogRederiveJobName = "citable-units-kitab-catalog-rederive"

	catalogQueuePending   = "pending"
	catalogQueueRunning   = "running"
	catalogQueueFailed    = "failed"
	catalogQueueCancelled = "cancelled" //nolint:misspell // persisted status uses British spelling in the migration
)

var errCatalogPriorityCategoryDrift = errors.New("O-4-2 priority category drifted")

// O-4-2 fixes the first wave to tafsir and hadith-commentary works. The name
// guard prevents a future taxonomy migration from silently changing what the
// numeric ids mean.
var catalogPriorityCategories = map[int]string{ //nolint:gochecknoglobals // charter-backed immutable taxonomy guard
	3: "التفسير",
	7: "شروح الحديث",
}

// CitableCatalogBookIDs limits the catalog population only in serial live
// tests. Production leaves it nil, which is asserted by the full-catalog
// coverage query and operational report.
//
//nolint:gochecknoglobals // same controlled live-test seam as CitablePilotBooks
var CitableCatalogBookIDs []int

// CitableCatalogPriorityOnly restricts the first operator wave to the
// charter-selected tafsir and hadith-commentary categories. The workflow runs
// this bounded wave before reopening the same durable queue to the remainder.
//
//nolint:gochecknoglobals // process-scoped CLI rollout switch; tests restore it serially
var CitableCatalogPriorityOnly bool

type citableUnitsCatalogJob struct {
	rederive  bool
	recovered bool
}

func (j citableUnitsCatalogJob) Name() string {
	if j.rederive {
		return citableCatalogRederiveJobName
	}

	return citableCatalogJobName
}

func (citableUnitsCatalogJob) ProfileVersion() int { return entity.KitabUnitDerivationProfileVersion }

func (j citableUnitsCatalogJob) CountRemaining(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	if j.rederive {
		var remaining int64
		if err := pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM book_publications publication
JOIN books b ON b.id = publication.book_id
LEFT JOIN citable_unit_catalog_queue rederive
  ON rederive.job_name = $1 AND rederive.book_id = b.id
LEFT JOIN citable_unit_catalog_queue catalog
  ON catalog.job_name = $2 AND catalog.book_id = b.id
WHERE publication.status = 'published'
  AND b.is_deleted = FALSE
  AND ($3::integer[] IS NULL OR b.id = ANY($3))
  AND (
      rederive.book_id IS NULL
      OR rederive.status <> 'completed'
      OR rederive.result_checksum IS DISTINCT FROM catalog.result_checksum
  )`, j.Name(), citableCatalogJobName, CitableCatalogBookIDs).Scan(&remaining); err != nil {
			return 0, fmt.Errorf("%s: count queue: %w", j.Name(), err)
		}

		return remaining, nil
	}

	var remaining int64

	err := pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM book_publications publication
JOIN books b ON b.id = publication.book_id
LEFT JOIN citable_unit_catalog_queue queued
  ON queued.job_name = $1 AND queued.book_id = b.id
WHERE publication.status = 'published'
  AND b.is_deleted = FALSE
  AND ($3::integer[] IS NULL OR b.id = ANY($3))
  AND (NOT $4::boolean OR b.category_id IN (3, 7))
  AND (
      queued.book_id IS NULL
      OR queued.status <> 'completed'
      OR b.units_derived_at IS NULL
      OR b.units_stale_at IS NOT NULL
      OR b.units_derivation_profile_version IS DISTINCT FROM $2
  )`, j.Name(), j.ProfileVersion(), CitableCatalogBookIDs, CitableCatalogPriorityOnly).Scan(&remaining)
	if err != nil {
		return 0, fmt.Errorf("%s: count remaining: %w", j.Name(), err)
	}

	return remaining, nil
}

func (j *citableUnitsCatalogJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	_ int,
) (newCursor, processed int64, done bool, err error) {
	if err := verifyCatalogPriorityCategories(ctx, pool); err != nil {
		return cursor, 0, false, fmt.Errorf("%s: %w", j.Name(), err)
	}

	if err := j.syncQueue(ctx, pool, !j.recovered); err != nil {
		return cursor, 0, false, err
	}

	j.recovered = true

	bookID, sequence, err := claimCatalogBook(
		ctx, pool, j.Name(), cursor, CitableCatalogPriorityOnly && !j.rederive,
	)
	// An editorial/import hook can invalidate an already-completed queue item
	// whose sequence is behind the persisted cursor. Wrap once so the same run
	// drains that delta instead of declaring a false completion.
	if errors.Is(err, pgx.ErrNoRows) && cursor > 0 {
		bookID, sequence, err = claimCatalogBook(
			ctx, pool, j.Name(), 0, CitableCatalogPriorityOnly && !j.rederive,
		)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return cursor, 0, true, nil
	}

	if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: claim next book: %w", j.Name(), err)
	}

	svc := unitregistry.New(persistent.NewCitableUnitRepo(&postgres.Postgres{
		Pool:    pool,
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}))

	result, err := svc.ReconcileCatalogBook(ctx, entity.CitableCatalogReconcileRequest{
		BookID:   bookID,
		JobName:  j.Name(),
		Rederive: j.rederive,
	})
	if err != nil {
		return cursor, 0, false, j.failQueueItem(ctx, pool, bookID, err)
	}

	report := result.Report

	fmt.Fprintf(os.Stdout,
		"%s: book=%d queue_sequence=%d scopes=%d derived=%d matched=%d minted=%d updated=%d superseded=%d tombstoned=%d checksum=%x attempts=%d\n",
		j.Name(), bookID, sequence, report.Scopes, report.Derived, report.Matched, report.Minted,
		report.Updated, report.Superseded, report.Tombstoned, result.RegistryChecksum, report.Attempts)

	return sequence, 1, false, nil
}

//nolint:funlen // three explicit SQL phases: withdraw, one-shot recovery, deterministic enqueue
func (j citableUnitsCatalogJob) syncQueue(ctx context.Context, pool *pgxpool.Pool, recoverFailed bool) error {
	// A queued book can be withdrawn before it is claimed. Preserve the row as
	// canceled evidence; never process or count it as current work.
	if _, err := pool.Exec(ctx, `
UPDATE citable_unit_catalog_queue queued
SET status = $2,
    error = 'book is no longer a published non-deleted catalog target',
    finished_at = now(),
    updated_at = now()
WHERE queued.job_name = $1
  AND queued.status = ANY($3)
  AND NOT EXISTS (
      SELECT 1
      FROM book_publications publication
      JOIN books b ON b.id = publication.book_id
      WHERE publication.book_id = queued.book_id
        AND publication.status = 'published'
        AND b.is_deleted = FALSE
  )`, j.Name(), catalogQueueCancelled,
		[]string{catalogQueuePending, catalogQueueRunning, catalogQueueFailed}); err != nil {
		return fmt.Errorf("%s: cancel withdrawn items: %w", j.Name(), err)
	}

	// A process death can leave one claimed item marked running. The F1-H
	// advisory lock guarantees no other runner exists while this reset runs.
	// Failed items stay failed for explicit operator inspection/reset; silently
	// requeueing them on every chunk creates an infinite retry loop.
	if _, err := pool.Exec(ctx, `
UPDATE citable_unit_catalog_queue
SET status = $2, started_at = NULL, finished_at = NULL, updated_at = now()
WHERE job_name = $1
  AND (status = $3 OR ($4 AND status = $5))
  AND EXISTS (
      SELECT 1 FROM book_publications publication
      JOIN books b ON b.id = publication.book_id
      WHERE publication.book_id = citable_unit_catalog_queue.book_id
        AND publication.status = 'published'
        AND b.is_deleted = FALSE
  )`, j.Name(), catalogQueuePending, catalogQueueRunning, recoverFailed, catalogQueueFailed); err != nil {
		return fmt.Errorf("%s: recover claimed items: %w", j.Name(), err)
	}

	_, err := pool.Exec(ctx, `
WITH reader_stats AS (
    SELECT book_id, COUNT(*) AS reader_count, MAX(updated_at) AS last_read_at
    FROM reading_progress
    GROUP BY book_id
), candidates AS (
    SELECT b.id AS book_id,
           ROW_NUMBER() OVER (
               ORDER BY CASE WHEN b.category_id IN (3, 7) THEN 0 ELSE 1 END,
                        COALESCE(reader_stats.reader_count, 0) DESC,
                        COALESCE(reader_stats.last_read_at, 'epoch'::timestamptz) DESC,
                        b.id
           ) AS queue_rank
    FROM book_publications publication
    JOIN books b ON b.id = publication.book_id
    LEFT JOIN reader_stats ON reader_stats.book_id = b.id
    LEFT JOIN citable_unit_catalog_queue queued
      ON queued.job_name = $1 AND queued.book_id = b.id
	LEFT JOIN citable_unit_catalog_queue catalog_result
	  ON catalog_result.job_name = 'citable-units-kitab-catalog'
	 AND catalog_result.book_id = b.id
    WHERE publication.status = 'published'
      AND b.is_deleted = FALSE
      AND ($5::integer[] IS NULL OR b.id = ANY($5))
      AND (NOT $6::boolean OR b.category_id IN (3, 7))
      AND (
          $2::boolean
          OR (NOT $2::boolean AND (queued.book_id IS NULL OR queued.status <> 'completed'))
          OR b.units_derived_at IS NULL
          OR b.units_stale_at IS NOT NULL
          OR b.units_derivation_profile_version IS DISTINCT FROM $3
      )
      AND (
		          ($2::boolean AND (
		              queued.book_id IS NULL
		              OR (
		                  queued.status = 'completed'
		                  AND queued.result_checksum IS DISTINCT FROM catalog_result.result_checksum
		              )
		              OR queued.status = 'cancelled'
		          ))
          OR (
              NOT $2::boolean
		              AND (queued.book_id IS NULL OR queued.status IN ('completed', 'cancelled'))
          )
      )
), sequence_base AS (
    SELECT COALESCE(MAX(sequence), 0) AS value
    FROM citable_unit_catalog_queue
    WHERE job_name = $1
)
INSERT INTO citable_unit_catalog_queue (
    job_name, book_id, sequence, status, attempts, updated_at
)
SELECT $1, candidates.book_id, sequence_base.value + candidates.queue_rank,
       $4, 0, now()
FROM candidates CROSS JOIN sequence_base
ON CONFLICT (job_name, book_id) DO UPDATE SET
    sequence = EXCLUDED.sequence,
    status = EXCLUDED.status,
    source_fingerprint = NULL,
    result_checksum = NULL,
    error = NULL,
    started_at = NULL,
    finished_at = NULL,
    updated_at = now()`, j.Name(), j.rederive, j.ProfileVersion(), catalogQueuePending,
		CitableCatalogBookIDs, CitableCatalogPriorityOnly && !j.rederive)
	if err != nil {
		return fmt.Errorf("%s: sync queue: %w", j.Name(), err)
	}

	return nil
}

func claimCatalogBook(
	ctx context.Context,
	pool *pgxpool.Pool,
	jobName string,
	cursor int64,
	priorityOnly bool,
) (bookID int, sequence int64, err error) {
	var (
		claimedBookID int
		claimedSeq    int64
	)

	err = pool.QueryRow(ctx, `
WITH withdrawn AS (
    UPDATE citable_unit_catalog_queue queued
    SET status = $5,
        error = 'book is no longer a published non-deleted catalog target',
        finished_at = now(),
        updated_at = now()
    WHERE queued.job_name = $1
      AND queued.status = $2
      AND NOT EXISTS (
          SELECT 1 FROM book_publications publication
          JOIN books b ON b.id = publication.book_id
          WHERE publication.book_id = queued.book_id
            AND publication.status = 'published'
            AND b.is_deleted = FALSE
      )
    RETURNING queued.book_id
), next_item AS (
    SELECT queued.book_id, queued.sequence
    FROM citable_unit_catalog_queue queued
    JOIN book_publications publication ON publication.book_id = queued.book_id
    JOIN books b ON b.id = queued.book_id
    WHERE queued.job_name = $1
      AND queued.status = $2
      AND queued.sequence > $3
      AND (NOT $6::boolean OR b.category_id IN (3, 7))
      AND publication.status = 'published'
      AND b.is_deleted = FALSE
      AND NOT EXISTS (SELECT 1 FROM withdrawn WHERE withdrawn.book_id = queued.book_id)
    ORDER BY queued.sequence
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE citable_unit_catalog_queue queued
SET status = $4,
    attempts = queued.attempts + 1,
    started_at = now(),
    finished_at = NULL,
    updated_at = now()
FROM next_item
WHERE queued.job_name = $1 AND queued.book_id = next_item.book_id
RETURNING queued.book_id, queued.sequence`,
		jobName, catalogQueuePending, cursor, catalogQueueRunning, catalogQueueCancelled, priorityOnly).
		Scan(&claimedBookID, &claimedSeq)

	return claimedBookID, claimedSeq, err
}

func (j citableUnitsCatalogJob) failQueueItem(
	ctx context.Context,
	pool *pgxpool.Pool,
	bookID int,
	cause error,
) error {
	_, persistErr := pool.Exec(ctx, `
UPDATE citable_unit_catalog_queue
SET status = $3, error = $4, finished_at = now(), updated_at = now()
WHERE job_name = $1 AND book_id = $2`, j.Name(), bookID, catalogQueueFailed, cause.Error())
	if persistErr != nil {
		return errors.Join(cause, fmt.Errorf("%s: persist failed book %d: %w", j.Name(), bookID, persistErr))
	}

	return cause
}

func verifyCatalogPriorityCategories(ctx context.Context, pool *pgxpool.Pool) error {
	for id, expected := range catalogPriorityCategories {
		var actual string
		if err := pool.QueryRow(ctx,
			`SELECT name FROM categories WHERE id = $1 AND is_deleted = FALSE`, id).Scan(&actual); err != nil {
			return fmt.Errorf("O-4-2 category %d: %w", id, err)
		}

		if actual != expected {
			return fmt.Errorf("%w: category %d got %q want %q",
				errCatalogPriorityCategoryDrift, id, actual, expected)
		}
	}

	return nil
}
