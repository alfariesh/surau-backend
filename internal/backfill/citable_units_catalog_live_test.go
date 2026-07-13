package backfill

import (
	"context"
	"testing"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var liveCatalogFixtureBooks = []int{990212, 990211} //nolint:gochecknoglobals // serial live fixture

const liveCatalogFormatterID = "c1a10000-0000-4000-8000-000000000001"

//nolint:paralleltest,wsl_v5 // serial seam; compact fixture setup remains readable as a lifecycle
func TestLiveCitableCatalogPauseResumeCoverageAndDeterminism(t *testing.T) {
	pool := liveBackfillPool(t)
	ctx := context.Background()

	originalFilter := CitableCatalogBookIDs
	CitableCatalogBookIDs = append([]int(nil), liveCatalogFixtureBooks...)
	t.Cleanup(func() { CitableCatalogBookIDs = originalFilter })

	resetLiveCatalogState(t, pool)
	t.Cleanup(func() { resetLiveCatalogState(t, pool) })
	seedLiveCatalogBooks(t, pool)

	job, err := ByName(citableCatalogJobName)
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(ctx)
	runner := &Runner{
		Pool:      pool,
		Log:       logger.New("error"),
		ChunkSize: 1,
		Sleep:     time.Millisecond,
		AfterChunk: func(state State) {
			if state.RowsDone == 1 {
				cancel()
			}
		},
	}
	require.NoError(t, runner.Run(runCtx, job, false))
	cancel()

	paused := readLiveCitableState(t, pool, citableCatalogJobName)
	assert.Equal(t, StatusPaused, paused.Status)
	assert.Equal(t, int64(1), paused.LastCursor, "checkpoint is the durable queue sequence")

	var firstBook int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT book_id FROM citable_unit_catalog_queue
WHERE job_name = $1 AND status = 'completed'
ORDER BY sequence LIMIT 1`, citableCatalogJobName).Scan(&firstBook))
	assert.Equal(t, liveCatalogFixtureBooks[1], firstBook,
		"O-4-2 tafsir category must run before the non-priority fixture")

	runner.AfterChunk = nil
	require.NoError(t, runner.Run(ctx, job, false))

	var target, materialized, missing, stale int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT COUNT(*)::int,
       COUNT(*) FILTER (
           WHERE b.units_derived_at IS NOT NULL
             AND b.units_stale_at IS NULL
             AND b.units_derivation_profile_version = $2
             AND EXISTS (
                 SELECT 1 FROM citable_units unit
                 WHERE unit.book_id = b.id AND unit.lifecycle = 'active'
             )
       )::int,
       COUNT(*) FILTER (WHERE b.units_derived_at IS NULL)::int,
       COUNT(*) FILTER (WHERE b.units_stale_at IS NOT NULL)::int
FROM book_publications publication
JOIN books b ON b.id = publication.book_id
WHERE publication.status = 'published' AND b.id = ANY($1)`,
		liveCatalogFixtureBooks, entity.KitabUnitDerivationProfileVersion).Scan(&target, &materialized, &missing, &stale))
	assert.Equal(t, 2, target)
	assert.Equal(t, target, materialized)
	assert.Zero(t, missing)
	assert.Zero(t, stale)

	before := liveCatalogRegistryDump(t, pool)

	rederive, err := ByName(citableCatalogRederiveJobName)
	require.NoError(t, err)
	drill := &Runner{Pool: pool, Log: logger.New("error"), ChunkSize: 1, Sleep: time.Millisecond}
	require.NoError(t, drill.Run(ctx, rederive, false))

	after := liveCatalogRegistryDump(t, pool)
	assert.Equal(t, before, after, "full-catalog rederive must be byte deterministic")

	verification, err := VerifyCitableCatalog(ctx, pool)
	require.NoError(t, err)
	assert.True(t, verification.Passed, "full catalog verifier = %+v", verification)
	assert.Equal(t, verification.TargetBooks, verification.MaterializedBooks)
	assert.Equal(t, verification.TargetBooks, verification.DeterminismVerifiedBooks)
	assert.Equal(t, verification.TargetBooks, verification.ParityVerifiedBooks)
	assert.Zero(t, verification.UncoveredCanonicalRunes)
	assert.Zero(t, verification.ParityMismatches)
	assert.Zero(t, verification.UnitAnchorsUnresolved)

	// A checksum algorithm upgrade must preserve already-proven materialization
	// only after the old source and registry evidence still match live data.
	unitRepo := persistent.NewCitableUnitRepo(&postgres.Postgres{
		Pool:    pool,
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	})
	legacyChecksum, err := unitRepo.CatalogLegacyRegistryChecksum(ctx, liveCatalogFixtureBooks[0])
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
UPDATE citable_unit_catalog_queue
SET result_checksum = $3, checksum_version = 1
WHERE job_name = $1 AND book_id = $2`,
		citableCatalogJobName, liveCatalogFixtureBooks[0], legacyChecksum[:])
	require.NoError(t, err)
	beforeChecksumUpgrade := liveCatalogRegistryDump(t, pool)
	require.NoError(t, runner.Run(ctx, job, true))
	assert.Equal(t, beforeChecksumUpgrade, liveCatalogRegistryDump(t, pool),
		"algorithm-only evidence upgrade must not reconcile or remint units")
	var upgradedChecksumVersion int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT checksum_version
FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`,
		citableCatalogJobName, liveCatalogFixtureBooks[0]).Scan(&upgradedChecksumVersion))
	assert.Equal(t, persistent.CitableCatalogChecksumVersion, upgradedChecksumVersion)

	_, err = pool.Exec(ctx, `
UPDATE citable_unit_catalog_queue
SET result_checksum = decode(repeat('00', 32), 'hex'), checksum_version = 1
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, liveCatalogFixtureBooks[0])
	require.NoError(t, err)
	beforeDriftedUpgrade := liveCatalogRegistryDump(t, pool)
	require.NoError(t, runner.Run(ctx, job, true))
	assert.Equal(t, beforeDriftedUpgrade, liveCatalogRegistryDump(t, pool),
		"drifted v1 evidence must be re-proven without changing deterministic units")
	var reprovedStatus string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT status
FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2 AND checksum_version = $3`,
		citableCatalogJobName, liveCatalogFixtureBooks[0],
		persistent.CitableCatalogChecksumVersion).Scan(&reprovedStatus))
	assert.Equal(t, "completed", reprovedStatus)

	// Release metadata is part of source provenance even when page text does
	// not change. The books trigger must make the delta visible and the runner
	// must refresh provenance without reminting textual identities.
	_, err = pool.Exec(ctx, `
UPDATE books SET major_release = 3, minor_release = 4
WHERE id = $1`, liveCatalogFixtureBooks[0])
	require.NoError(t, err)
	var releaseStale bool
	require.NoError(t, pool.QueryRow(ctx, `
SELECT units_stale_at IS NOT NULL FROM books WHERE id = $1`, liveCatalogFixtureBooks[0]).Scan(&releaseStale))
	assert.True(t, releaseStale)
	require.NoError(t, runner.Run(ctx, job, true))
	var releaseDetail string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT COALESCE(provenance_detail->>'release', '')
FROM citable_units
WHERE book_id = $1 AND lifecycle = 'active' AND provenance_class = 'source'
ORDER BY position, ordinal LIMIT 1`, liveCatalogFixtureBooks[0]).Scan(&releaseDetail))
	assert.Equal(t, "3.4", releaseDetail)
	require.NoError(t, drill.Run(ctx, rederive, true))
	releaseVerification, err := VerifyCitableCatalog(ctx, pool)
	require.NoError(t, err)
	assert.True(t, releaseVerification.Passed, "release-only delta verifier = %+v", releaseVerification)

	// A current profile-v2 book still needs durable raw evidence. Simulate a
	// missing queue row after a hook completed first; the raw job must enqueue a
	// no-op rather than skipping the book forever.
	beforeMissingQueue := liveCatalogRegistryDump(t, pool)
	_, err = pool.Exec(ctx, `
DELETE FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, liveCatalogFixtureBooks[0])
	require.NoError(t, err)
	require.NoError(t, runner.Run(ctx, job, true))
	assert.Equal(t, beforeMissingQueue, liveCatalogRegistryDump(t, pool))
	var rebuiltQueueStatus string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT status FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, liveCatalogFixtureBooks[0]).
		Scan(&rebuiltQueueStatus))
	assert.Equal(t, "completed", rebuiltQueueStatus)

	// A published edit that changes only markup must not remint the source
	// unit. Its formatter is retained as audit detail while source provenance,
	// UUID, and Anchor stay stable.
	var sourceID, sourceAnchor string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT id::text, anchor FROM citable_units
WHERE book_id = $1 AND lifecycle = 'active' AND content_role = 'book_page'
ORDER BY position, ordinal LIMIT 1`, liveCatalogFixtureBooks[0]).Scan(&sourceID, &sourceAnchor))
	var staleSource, staleRegistry []byte
	require.NoError(t, pool.QueryRow(ctx, `
SELECT source_fingerprint, result_checksum
FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, liveCatalogFixtureBooks[0]).
		Scan(&staleSource, &staleRegistry))
	_, err = pool.Exec(ctx, `
INSERT INTO book_page_edits (
    book_id, page_id, status, content_html, content_text, updated_by, published_at
) VALUES (
    $1, 1, 'published', '<section><p><strong>فقرة عربية أولى.</strong></p><p>فقرة عربية ثانية.</p></section>',
    E'فقرة عربية أولى.\nفقرة عربية ثانية.', $2, now()
	)`, liveCatalogFixtureBooks[0], liveCatalogFormatterID)
	require.NoError(t, err)
	registry := unitregistry.New(persistent.NewCitableUnitRepo(&postgres.Postgres{
		Pool:    pool,
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}))
	_, skipped, err := registry.ReconcileBookIfDerived(ctx, liveCatalogFixtureBooks[0])
	require.NoError(t, err)
	assert.False(t, skipped)

	var queueInvalidated int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT COUNT(*) FROM citable_unit_catalog_queue
WHERE book_id = $1
  AND job_name = ANY($2)
  AND status = 'pending'
  AND result_checksum IS NULL`, liveCatalogFixtureBooks[0],
		[]string{citableCatalogJobName, citableCatalogRederiveJobName}).Scan(&queueInvalidated))
	assert.Equal(t, 2, queueInvalidated,
		"the editorial hook must invalidate raw and determinism evidence atomically")

	// Even if stale queue rows are forged back to mutually equal/completed,
	// the verifier must compare them with the live source and registry checksum.
	_, err = pool.Exec(ctx, `
UPDATE citable_unit_catalog_queue
SET status = 'completed',
    source_fingerprint = $3,
    result_checksum = $4,
    finished_at = now(),
    updated_at = now()
WHERE book_id = $1 AND job_name = ANY($2)`, liveCatalogFixtureBooks[0],
		[]string{citableCatalogJobName, citableCatalogRederiveJobName}, staleSource, staleRegistry)
	require.NoError(t, err)
	staleEvidence, err := VerifyCitableCatalog(ctx, pool)
	require.NoError(t, err)
	assert.False(t, staleEvidence.Passed)
	assert.Equal(t, staleEvidence.TargetBooks-1, staleEvidence.DeterminismVerifiedBooks,
		"two equal queue checksums are not proof when they differ from the live registry")
	_, err = pool.Exec(ctx, `
UPDATE citable_unit_catalog_queue
SET status = 'pending',
    source_fingerprint = NULL,
    result_checksum = NULL,
    finished_at = NULL,
    updated_at = now()
WHERE book_id = $1 AND job_name = ANY($2)`, liveCatalogFixtureBooks[0],
		[]string{citableCatalogJobName, citableCatalogRederiveJobName})
	require.NoError(t, err)
	require.NoError(t, runner.Run(ctx, job, true))

	var formattedID, formattedAnchor, formattedProvenance, formatActor string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT id::text, anchor, provenance_class,
       COALESCE(provenance_detail->>'format_edit_actor_id', '')
FROM citable_units
WHERE book_id = $1 AND lifecycle = 'active' AND content_role = 'book_page'
ORDER BY position, ordinal LIMIT 1`, liveCatalogFixtureBooks[0]).
		Scan(&formattedID, &formattedAnchor, &formattedProvenance, &formatActor))
	assert.Equal(t, sourceID, formattedID)
	assert.Equal(t, sourceAnchor, formattedAnchor)
	assert.Equal(t, entity.ProvenanceClassSource, formattedProvenance)
	assert.Equal(t, liveCatalogFormatterID, formatActor)

	// A text edit may mint a successor, but an already-issued Anchor must still
	// resolve through lineage. This is the K-1 citation-survives-edit proof.
	var oldAnchor string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT anchor FROM citable_units
WHERE book_id = $1 AND lifecycle = 'active' AND content_role = 'book_page'
ORDER BY position, ordinal LIMIT 1`, liveCatalogFixtureBooks[0]).Scan(&oldAnchor))
	_, err = pool.Exec(ctx, `
UPDATE book_page_edits
SET content_html = '<p>فقرة عربية محررة.</p>',
    content_text = 'فقرة عربية محررة.',
    updated_at = now(),
    published_at = now()
	WHERE book_id = $1 AND page_id = 1 AND status = 'published'`, liveCatalogFixtureBooks[0])
	require.NoError(t, err)
	_, skipped, err = registry.ReconcileBookIfDerived(ctx, liveCatalogFixtureBooks[0])
	require.NoError(t, err)
	assert.False(t, skipped)
	require.NoError(t, runner.Run(ctx, job, true))

	anchorRepo := persistent.NewAnchorRepo(&postgres.Postgres{
		Pool:    pool,
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	})
	resolution, err := anchorRepo.ResolveCanonicalUnit(ctx, oldAnchor)
	require.NoError(t, err)
	assert.Equal(t, entity.UnitLifecycleSuperseded, resolution.Status)
	assert.NotEmpty(t, resolution.ActiveRecords)
	assert.NotEmpty(t, resolution.RedirectChain)
	assert.False(t, resolution.CycleDetected)

	// Re-prove determinism over the edited effective snapshot.
	require.NoError(t, drill.Run(ctx, rederive, true))

	var unfinished int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT COUNT(*) FROM citable_unit_catalog_queue
WHERE job_name = ANY($1) AND status <> 'completed'`,
		[]string{citableCatalogJobName, citableCatalogRederiveJobName}).Scan(&unfinished))
	assert.Zero(t, unfinished)
	finalVerification, err := VerifyCitableCatalog(ctx, pool)
	require.NoError(t, err)
	assert.True(t, finalVerification.Passed, "post-edit verifier = %+v", finalVerification)
}

// TestLiveCitableCatalogPriorityWave proves rollout is genuinely split: the
// first F1-H run commits only categories 3/7, and reopening the same durable
// queue then materializes the remaining published catalog without replaying
// the completed priority book.
//
//nolint:paralleltest,wsl_v5 // serial process-scoped catalog filters and fixed DB fixtures
func TestLiveCitableCatalogPriorityWave(t *testing.T) {
	pool := liveBackfillPool(t)
	ctx := context.Background()

	originalIDs := CitableCatalogBookIDs
	originalPriority := CitableCatalogPriorityOnly
	CitableCatalogBookIDs = append([]int(nil), liveCatalogFixtureBooks...)
	CitableCatalogPriorityOnly = true
	t.Cleanup(func() {
		CitableCatalogBookIDs = originalIDs
		CitableCatalogPriorityOnly = originalPriority
	})

	resetLiveCatalogState(t, pool)
	t.Cleanup(func() { resetLiveCatalogState(t, pool) })
	seedLiveCatalogBooks(t, pool)

	job, err := ByName(citableCatalogJobName)
	require.NoError(t, err)
	runner := &Runner{Pool: pool, Log: logger.New("error"), ChunkSize: 1, Sleep: time.Millisecond}
	require.NoError(t, runner.Run(ctx, job, false))

	priorityUnits := liveCatalogActiveUnitCount(t, pool, liveCatalogFixtureBooks[1])
	nonPriorityUnits := liveCatalogActiveUnitCount(t, pool, liveCatalogFixtureBooks[0])
	assert.Positive(t, priorityUnits)
	assert.Zero(t, nonPriorityUnits, "wave 1 must not materialize outside categories 3/7")

	CitableCatalogPriorityOnly = false
	require.NoError(t, runner.Run(ctx, job, true))
	assert.Positive(t, liveCatalogActiveUnitCount(t, pool, liveCatalogFixtureBooks[0]))
}

func liveCatalogActiveUnitCount(t *testing.T, pool *pgxpool.Pool, bookID int) int {
	t.Helper()

	var count int
	require.NoError(t, pool.QueryRow(context.Background(), `
SELECT COUNT(*) FROM citable_units
WHERE book_id = $1 AND corpus = 'kitab' AND lifecycle = 'active'`, bookID).Scan(&count))

	return count
}

//nolint:paralleltest,wsl_v5 // serial seam; compact fixture setup remains readable as a lifecycle
func TestLiveCitableCatalogQueueWithdrawRetryAndRevive(t *testing.T) {
	pool := liveBackfillPool(t)
	ctx := context.Background()

	originalFilter := CitableCatalogBookIDs
	CitableCatalogBookIDs = []int{liveCatalogFixtureBooks[0]}
	t.Cleanup(func() { CitableCatalogBookIDs = originalFilter })

	resetLiveCatalogState(t, pool)
	t.Cleanup(func() { resetLiveCatalogState(t, pool) })
	seedLiveCatalogBooks(t, pool)

	runner := &Runner{Pool: pool, Log: logger.New("error"), ChunkSize: 1, Sleep: time.Millisecond}
	initial, err := ByName(citableCatalogJobName)
	require.NoError(t, err)
	require.NoError(t, runner.Run(ctx, initial, false))

	bookID := liveCatalogFixtureBooks[0]
	_, err = pool.Exec(ctx, `
UPDATE citable_unit_catalog_queue
SET status = 'pending', result_checksum = NULL, finished_at = NULL, updated_at = now()
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
UPDATE book_publications SET status = 'hidden', updated_at = now()
WHERE book_id = $1`, bookID)
	require.NoError(t, err)

	job := &citableUnitsCatalogJob{}
	require.NoError(t, job.syncQueue(ctx, pool, false))
	var status string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT status FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID).Scan(&status))
	assert.Equal(t, catalogQueueCancelled, status)

	var currentPending int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM citable_unit_catalog_queue queued
JOIN book_publications publication ON publication.book_id = queued.book_id
JOIN books b ON b.id = queued.book_id
WHERE queued.job_name = $1
  AND publication.status = 'published'
  AND b.is_deleted = FALSE
  AND queued.status <> 'completed'`, citableCatalogJobName).Scan(&currentPending))
	assert.Zero(t, currentPending, "canceled historical rows are outside the current-target verifier")

	_, err = pool.Exec(ctx, `
UPDATE book_publications SET status = 'published', updated_at = now()
WHERE book_id = $1`, bookID)
	require.NoError(t, err)
	require.NoError(t, job.syncQueue(ctx, pool, false))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT status FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID).Scan(&status))
	assert.Equal(t, catalogQueuePending, status, "republishing revives a canceled durable item")

	revived, err := ByName(citableCatalogJobName)
	require.NoError(t, err)
	require.NoError(t, runner.Run(ctx, revived, true))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT status FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID).Scan(&status))
	assert.Equal(t, "completed", status)

	// A failed item is recovered once by a fresh process/job instance. The
	// same instance never silently requeues it again, preventing a tight
	// infinite retry loop; a later restart can retry after the source is fixed.
	_, err = pool.Exec(ctx, `
UPDATE book_pages SET updated_at = now() WHERE book_id = $1 AND page_id = 1`, bookID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
UPDATE citable_unit_catalog_queue
SET status = 'running', result_checksum = NULL, started_at = now() - interval '1 hour',
    finished_at = NULL, error = NULL, updated_at = now()
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID)
	require.NoError(t, err)

	runningProcess := &citableUnitsCatalogJob{}
	injectedFailure := ErrJobUnknown
	assert.ErrorIs(t, runningProcess.failQueueItem(ctx, pool, bookID, injectedFailure), injectedFailure)
	var failedFinishedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx, `
SELECT finished_at FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID).Scan(&failedFinishedAt))
	require.NotNil(t, failedFinishedAt, "failed attempts need an observable duration endpoint")
	require.NoError(t, runningProcess.syncQueue(ctx, pool, true))
	var retryStartedAt, retryFinishedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx, `
SELECT started_at, finished_at FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID).
		Scan(&retryStartedAt, &retryFinishedAt))
	assert.Nil(t, retryStartedAt)
	assert.Nil(t, retryFinishedAt, "a retry starts a fresh attempt duration")
	require.NoError(t, pool.QueryRow(ctx, `
SELECT status FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID).Scan(&status))
	assert.Equal(t, catalogQueuePending, status)
	_, err = pool.Exec(ctx, `
UPDATE citable_unit_catalog_queue SET status = 'failed', error = 'second injected failure'
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID)
	require.NoError(t, err)
	require.NoError(t, runningProcess.syncQueue(ctx, pool, false))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT status FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID).Scan(&status))
	assert.Equal(t, catalogQueueFailed, status)

	restartedProcess := &citableUnitsCatalogJob{}
	require.NoError(t, restartedProcess.syncQueue(ctx, pool, true))
	require.NoError(t, runner.Run(ctx, restartedProcess, true))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT status FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, citableCatalogJobName, bookID).Scan(&status))
	assert.Equal(t, "completed", status)
}

//nolint:wsl_v5 // one transaction seeds all FK-ordered live prerequisites
func seedLiveCatalogBooks(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// Clean migration databases do not contain the imported Shamela taxonomy.
	// Seed only missing fixture prerequisites; ON CONFLICT deliberately never
	// rewrites a real catalog category. The name guard below rejects drift.
	_, err = tx.Exec(ctx, `
INSERT INTO categories (id, name) VALUES
    (1, 'K-1 live fixture category'),
    (3, 'التفسير'),
    (7, 'شروح الحديث')
ON CONFLICT (id) DO NOTHING`)
	require.NoError(t, err)
	for id, expected := range catalogPriorityCategories {
		var actual string
		require.NoError(t, tx.QueryRow(ctx,
			`SELECT name FROM categories WHERE id = $1 AND is_deleted = FALSE`, id).Scan(&actual))
		require.Equal(t, expected, actual, "O-4-2 category %d must retain its canonical name", id)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO users (id, username, email, password_hash)
VALUES ($1, 'k1-live-formatter', 'k1-live-formatter@example.test', 'x')
ON CONFLICT (id) DO NOTHING`, liveCatalogFormatterID)
	require.NoError(t, err)

	for i, bookID := range liveCatalogFixtureBooks {
		categoryID := 1
		if i == 1 {
			categoryID = 3
		}

		_, err = tx.Exec(ctx, `
INSERT INTO books (id, name, category_id, has_content)
VALUES ($1, 'كتاب اختبار K-1', $2, TRUE)`, bookID, categoryID)
		require.NoError(t, err)

		_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `UPDATE books SET license_status = 'permitted' WHERE id = $1`, bookID)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
INSERT INTO book_publications (book_id, status, published_at)
VALUES ($1, 'published', now())`, bookID)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'origin'`)
		require.NoError(t, err)

		_, err = tx.Exec(ctx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<p>فقرة عربية أولى.</p><p>فقرة عربية ثانية.</p>',
        E'فقرة عربية أولى.\nفقرة عربية ثانية.')`, bookID)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
INSERT INTO book_headings (book_id, heading_id, page_id, ordinal, content)
VALUES ($1, 1, 1, 1, 'باب الاختبار')`, bookID)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
INSERT INTO book_heading_ranges (book_id, heading_id, start_page_id, end_page_id)
VALUES ($1, 1, 1, 1)`, bookID)
		require.NoError(t, err)
		if i == 0 {
			_, err = tx.Exec(ctx, `
INSERT INTO book_heading_summaries (
    book_id, heading_id, lang, summary, source, provenance_class
) VALUES ($1, 1, 'ar', '<p>ملخص أول.</p><p>ملخص ثان.</p>', 'k1-live', 'source')`, bookID)
			require.NoError(t, err)
		}
	}

	require.NoError(t, tx.Commit(ctx))
}

//nolint:wsl_v5 // one transaction removes only test-owned rows in FK order
func resetLiveCatalogState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `DELETE FROM book_license_audits WHERE book_id = ANY($1)`, liveCatalogFixtureBooks)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM books WHERE id = ANY($1)`, liveCatalogFixtureBooks)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, liveCatalogFormatterID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
DELETE FROM backfill_jobs
WHERE job_name = ANY($1)`, []string{citableCatalogJobName, citableCatalogRederiveJobName})
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func liveCatalogRegistryDump(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	var dump string
	require.NoError(t, pool.QueryRow(context.Background(), `
SELECT COALESCE(string_agg(
    id::text || '|' || anchor || '|' || ordinal || '|' || position || '|' ||
    kind || '|' || lifecycle || '|' || encode(content_hash, 'hex') || '|' ||
    content_role || '|' || language || '|' || review_status || '|' ||
    COALESCE(encode(source_document_hash, 'hex'), '') || '|' ||
    COALESCE(source_char_start::text, '') || '|' || COALESCE(source_char_end::text, ''),
    E'\n' ORDER BY id
), '')
FROM citable_units WHERE book_id = ANY($1)`, liveCatalogFixtureBooks).Scan(&dump))

	return dump
}
