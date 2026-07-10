package backfill

import (
	"context"
	"errors"
	"fmt"

	"github.com/alfariesh/surau-backend/internal/importer"
	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	quranCrossReferenceBridgeJobName   = "cross-references-quran-bridge"
	quranCrossReferenceFreezeJobName   = "cross-references-quran-freeze"
	quranCrossReferenceUnfreezeJobName = "cross-references-quran-unfreeze"
	scopeArgumentAfterCursor           = 2

	// A legacy row enters the content-to-content registry only when both ends
	// can be expressed by active canonical Anchors. Invalid non-public rows
	// remain available for editorial repair; invalid approved rows fail the
	// preflight instead of silently disappearing from the public set.
	mappableLegacyQuranSourceSQL = `EXISTS (
		SELECT 1
		FROM books source_book
		WHERE source_book.id = qbr.book_id
		  AND source_book.is_deleted = FALSE
	)`
	mappableLegacyQuranTargetSQL = `(
		(qbr.reference_kind = 'surah' AND qbr.surah_id IS NOT NULL)
		OR
		(qbr.reference_kind IN ('surah_ayah', 'quote')
		 AND qbr.surah_id IS NOT NULL
		 AND qbr.from_ayah_number IS NOT NULL
		 AND qbr.to_ayah_number IS NOT NULL)
		OR
		(qbr.reference_kind = 'ambiguous'
		 AND qbr.surah_id IS NOT NULL
		 AND (
			(qbr.from_ayah_number IS NULL AND qbr.to_ayah_number IS NULL)
			OR (qbr.from_ayah_number IS NOT NULL AND qbr.to_ayah_number IS NOT NULL)
		 ))
	)`
	mappableLegacyQuranPairSQL = `(` + mappableLegacyQuranSourceSQL + ` AND ` + mappableLegacyQuranTargetSQL + `)`
)

var (
	errNoQuranReferencesBridged = errors.New("selected book produced no Quran Cross-Reference bridge")
	errApprovedQuranAmbiguous   = errors.New("approved legacy Quran reference uses ambiguous kind")
	errApprovedQuranUnmappable  = errors.New("approved legacy Quran reference has no valid Anchor pair")
	errQuranBridgeIncomplete    = errors.New("bridge backfill is not completed")
)

// quranBridgeBookScope is nil in production (all books). Live tests override
// it with isolated fixture ids so they prove pause/resume without migrating a
// developer's real corpus as a side effect.
//
//nolint:gochecknoglobals // deliberate serial live-test seam, like CitablePilotBooks
var quranBridgeBookScope []int

// crossReferencesQuranBridgeJob migrates one complete book per chunk. A book
// is the checkpoint boundary, while every edge remains independently atomic
// across the legacy table, generic registry, and compatibility bridge.
type crossReferencesQuranBridgeJob struct{}

func (crossReferencesQuranBridgeJob) Name() string { return quranCrossReferenceBridgeJobName }

func (crossReferencesQuranBridgeJob) ProfileVersion() int { return searchtext.ProfileVersion }

func (crossReferencesQuranBridgeJob) CountRemaining(
	ctx context.Context,
	pool *pgxpool.Pool,
) (int64, error) {
	if err := preflightLegacyQuranReferences(ctx, pool, quranBridgeBookScope); err != nil {
		return 0, err
	}

	scopeSQL, args := legacyQuranBookScopeSQL(1, quranBridgeBookScope)

	var remaining int64

	err := pool.QueryRow(ctx, `
SELECT count(DISTINCT qbr.book_id)
FROM quran_book_references qbr
LEFT JOIN quran_cross_reference_bridge bridge
       ON bridge.cross_reference_id = qbr.id
WHERE bridge.cross_reference_id IS NULL
	AND `+mappableLegacyQuranPairSQL+scopeSQL, args...).Scan(&remaining)
	if err != nil {
		return 0, fmt.Errorf("%s: count remaining books: %w", quranCrossReferenceBridgeJobName, err)
	}

	return remaining, nil
}

func (crossReferencesQuranBridgeJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	_ int,
) (newCursor, processed int64, done bool, err error) {
	if err := preflightLegacyQuranReferences(ctx, pool, quranBridgeBookScope); err != nil {
		return cursor, 0, false, err
	}

	scopeSQL, scopeArgs := legacyQuranBookScopeSQL(scopeArgumentAfterCursor, quranBridgeBookScope)
	args := append([]any{cursor}, scopeArgs...)

	var bookID int

	err = pool.QueryRow(ctx, `
SELECT qbr.book_id
FROM quran_book_references qbr
LEFT JOIN quran_cross_reference_bridge bridge
       ON bridge.cross_reference_id = qbr.id
WHERE bridge.cross_reference_id IS NULL
	AND `+mappableLegacyQuranPairSQL+scopeSQL+`
ORDER BY CASE WHEN qbr.book_id > $1 THEN 0 ELSE 1 END,
         qbr.book_id
LIMIT 1`, args...).Scan(&bookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return cursor, 0, true, nil
	}

	if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: select next book: %w", quranCrossReferenceBridgeJobName, err)
	}

	bridged, err := importer.BridgeLegacyQuranReferencesForBook(ctx, pool, bookID)
	if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: bridge book %d: %w", quranCrossReferenceBridgeJobName, bookID, err)
	}

	if bridged == 0 {
		return cursor, 0, false, fmt.Errorf(
			"%w: %s book %d",
			errNoQuranReferencesBridged,
			quranCrossReferenceBridgeJobName,
			bookID,
		)
	}

	return int64(bookID), 1, false, nil
}

// crossReferencesQuranFreezeJob is the explicit contract switch. Operators
// run it only after the bridge job, SQL EXCEPT parity, old HTTP/embed parity,
// and public backlink smoke tests have all passed. Keeping it separate makes
// it impossible for an ordinary bridge run to freeze legacy writes early.
type crossReferencesQuranFreezeJob struct{}

func (crossReferencesQuranFreezeJob) Name() string { return quranCrossReferenceFreezeJobName }

func (crossReferencesQuranFreezeJob) ProfileVersion() int { return 0 }

func (crossReferencesQuranFreezeJob) CountRemaining(
	ctx context.Context,
	pool *pgxpool.Pool,
) (int64, error) {
	frozen, err := legacyQuranWritesFrozen(ctx, pool)
	if err != nil {
		return 0, err
	}

	if frozen {
		return 0, nil
	}

	return 1, nil
}

func (crossReferencesQuranFreezeJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	_ int64,
	_ int,
) (newCursor, processed int64, done bool, err error) {
	frozen, err := legacyQuranWritesFrozen(ctx, pool)
	if err != nil {
		return 0, 0, false, err
	}

	if frozen {
		return 0, 0, true, nil
	}

	if err := requireQuranBridgeCompleted(ctx, pool); err != nil {
		return 0, 0, false, err
	}

	if err := importer.FreezeLegacyQuranReferenceWrites(ctx, pool); err != nil {
		return 0, 0, false, fmt.Errorf("%s: %w", quranCrossReferenceFreezeJobName, err)
	}

	return 1, 1, true, nil
}

// crossReferencesQuranUnfreezeJob is the explicit rollback switch used just
// before deploying an old binary which still writes quran_book_references
// directly. Merely restarting the bridge job never invokes this transition.
type crossReferencesQuranUnfreezeJob struct{}

func (crossReferencesQuranUnfreezeJob) Name() string { return quranCrossReferenceUnfreezeJobName }

func (crossReferencesQuranUnfreezeJob) ProfileVersion() int { return 0 }

func (crossReferencesQuranUnfreezeJob) CountRemaining(
	ctx context.Context,
	pool *pgxpool.Pool,
) (int64, error) {
	frozen, err := legacyQuranWritesFrozen(ctx, pool)
	if err != nil {
		return 0, err
	}

	if !frozen {
		return 0, nil
	}

	return 1, nil
}

func (crossReferencesQuranUnfreezeJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	_ int64,
	_ int,
) (newCursor, processed int64, done bool, err error) {
	frozen, err := legacyQuranWritesFrozen(ctx, pool)
	if err != nil {
		return 0, 0, false, err
	}

	if !frozen {
		return 0, 0, true, nil
	}

	if err := importer.UnfreezeLegacyQuranReferenceWrites(ctx, pool); err != nil {
		return 0, 0, false, fmt.Errorf("%s: %w", quranCrossReferenceUnfreezeJobName, err)
	}

	return 1, 1, true, nil
}

func legacyQuranWritesFrozen(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var frozen bool

	err := pool.QueryRow(ctx, `
SELECT quran_legacy_frozen
FROM cross_reference_registry_state
WHERE id = TRUE`).Scan(&frozen)
	if err != nil {
		return false, fmt.Errorf("read cross-reference registry state: %w", err)
	}

	return frozen, nil
}

func requireQuranBridgeCompleted(ctx context.Context, pool *pgxpool.Pool) error {
	var status string

	err := pool.QueryRow(ctx, `
SELECT status
FROM backfill_jobs
WHERE job_name = $1`, quranCrossReferenceBridgeJobName).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: checkpoint is missing", errQuranBridgeIncomplete)
	}

	if err != nil {
		return fmt.Errorf("%s: read bridge checkpoint: %w", quranCrossReferenceFreezeJobName, err)
	}

	if status != StatusCompleted {
		return fmt.Errorf("%w: status is %s", errQuranBridgeIncomplete, status)
	}

	return nil
}

func preflightLegacyQuranReferences(ctx context.Context, pool *pgxpool.Pool, bookScope []int) error {
	scopeSQL, args := legacyQuranBookScopeSQL(1, bookScope)

	var id string

	err := pool.QueryRow(ctx, `
SELECT qbr.id::text
FROM quran_book_references qbr
WHERE qbr.review_status = 'approved'
  AND qbr.reference_kind = 'ambiguous'
	`+scopeSQL+`
ORDER BY qbr.id
LIMIT 1`, args...).Scan(&id)
	switch {
	case err == nil:
		return fmt.Errorf(
			"%w: %s preflight reference %s; review it before migration",
			errApprovedQuranAmbiguous,
			quranCrossReferenceBridgeJobName,
			id,
		)
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%s preflight: inspect approved ambiguous rows: %w", quranCrossReferenceBridgeJobName, err)
	}

	err = pool.QueryRow(ctx, `
SELECT qbr.id::text
FROM quran_book_references qbr
WHERE qbr.review_status = 'approved'
  AND NOT `+mappableLegacyQuranPairSQL+`
	`+scopeSQL+`
ORDER BY qbr.id
LIMIT 1`, args...).Scan(&id)
	switch {
	case err == nil:
		return fmt.Errorf(
			"%w: %s preflight reference %s",
			errApprovedQuranUnmappable,
			quranCrossReferenceBridgeJobName,
			id,
		)
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%s preflight: inspect approved unmappable rows: %w", quranCrossReferenceBridgeJobName, err)
	}

	return nil
}

func legacyQuranBookScopeSQL(argPosition int, bookScope []int) (sqlText string, args []any) {
	if len(bookScope) == 0 {
		return "", nil
	}

	return fmt.Sprintf(" AND qbr.book_id = ANY($%d)", argPosition), []any{bookScope}
}
