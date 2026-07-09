package backfill

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Masterminds/squirrel"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CitablePilotBooks is the fixed B-1 pilot set (phase-1b B-D10: pilot-first
// before the K-1 catalog industrialization): the four real eval-corpus books,
// led by 797 (متن الزبد, the Quran-citation-dense case).
//
//nolint:gochecknoglobals // immutable pilot set, part of the job definition
var CitablePilotBooks = []int{797, 7312, 12876, 22842}

// citableStaleWhereSQL selects pilot books that were never derived or whose
// source changed since derivation (importer bumps updated_at only for real
// changes, so this predicate has no false positives from no-op re-imports).
// $1 = pilot book ids.
const citableStaleWhereSQL = `
	b.id = ANY($1) AND b.is_deleted = FALSE AND b.has_content
	AND (
		b.units_derived_at IS NULL
		OR b.units_derived_at < GREATEST(
			COALESCE((SELECT MAX(updated_at) FROM book_pages WHERE book_id = b.id), 'epoch'::timestamptz),
			COALESCE((SELECT MAX(updated_at) FROM book_headings WHERE book_id = b.id), 'epoch'::timestamptz),
			COALESCE((SELECT MAX(updated_at) FROM book_page_edits WHERE book_id = b.id AND status = 'published'), 'epoch'::timestamptz))
	)`

// citableDerivedWhereSQL selects pilot books that already went through the
// initial derive — the determinism-drill population. $1 = pilot book ids.
const citableDerivedWhereSQL = `
	b.id = ANY($1) AND b.is_deleted = FALSE AND b.has_content
	AND b.units_derived_at IS NOT NULL`

// citableUnitsPilotJob derives citable units for pilot books that need work:
// never-derived books (initial backfill) and books whose source changed since
// derivation (post-re-import). One book per chunk — a whole-book reconcile is
// the atomic unit of work, so pause/resume lands on book boundaries.
type citableUnitsPilotJob struct{}

func (citableUnitsPilotJob) Name() string { return "citable-units-kitab-pilot" }

func (citableUnitsPilotJob) ProfileVersion() int { return searchtext.ProfileVersion }

func (citableUnitsPilotJob) CountRemaining(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	return citableCountWhere(ctx, pool, citableStaleWhereSQL, "citable-units-kitab-pilot")
}

func (citableUnitsPilotJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	_ int,
) (newCursor, processed int64, done bool, err error) {
	return citableProcessNextBook(ctx, pool, cursor, citableStaleWhereSQL, "citable-units-kitab-pilot")
}

// citableUnitsRederiveJob re-derives every already-derived pilot book
// unconditionally — the AC-1 determinism drill: over an unchanged source the
// run must leave the registry byte-identical (verify with a table snapshot
// diff; only books.units_derived_at moves). Also the recovery path after a
// deliberate parser/profile change (supersede wave absorbed by lineage).
type citableUnitsRederiveJob struct{}

func (citableUnitsRederiveJob) Name() string { return "citable-units-kitab-rederive" }

func (citableUnitsRederiveJob) ProfileVersion() int { return searchtext.ProfileVersion }

func (citableUnitsRederiveJob) CountRemaining(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	return citableCountWhere(ctx, pool, citableDerivedWhereSQL, "citable-units-kitab-rederive")
}

func (citableUnitsRederiveJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	_ int,
) (newCursor, processed int64, done bool, err error) {
	return citableProcessNextBook(ctx, pool, cursor, citableDerivedWhereSQL, "citable-units-kitab-rederive")
}

func citableCountWhere(ctx context.Context, pool *pgxpool.Pool, whereSQL, jobName string) (int64, error) {
	var remaining int64

	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM books b WHERE `+whereSQL, CitablePilotBooks).Scan(&remaining)
	if err != nil {
		return 0, fmt.Errorf("%s: count remaining: %w", jobName, err)
	}

	return remaining, nil
}

// citableProcessNextBook reconciles the next matching pilot book after the
// cursor through the single write service. The chunk size is deliberately
// ignored: one book = one atomic reconcile.
func citableProcessNextBook(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	whereSQL string,
	jobName string,
) (newCursor, processed int64, done bool, err error) {
	var bookID int

	err = pool.QueryRow(ctx,
		`SELECT b.id FROM books b WHERE b.id > $2 AND `+whereSQL+` ORDER BY b.id LIMIT 1`,
		CitablePilotBooks, cursor).Scan(&bookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return cursor, 0, true, nil
	}

	if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: select next book: %w", jobName, err)
	}

	svc := unitregistry.New(persistent.NewCitableUnitRepo(&postgres.Postgres{
		Pool:    pool,
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}))

	report, err := svc.ReconcileBook(ctx, bookID)
	if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: reconcile book %d: %w", jobName, bookID, err)
	}

	// One structured line per book: the reconcile report is the pilot's
	// determinism evidence (re-runs must show minted=0 superseded=0
	// tombstoned=0 updated=0). These jobs only execute under the backfill CLI,
	// so stdout is the operator console.
	fmt.Fprintf(os.Stdout,
		"%s: book=%d scopes=%d derived=%d matched=%d minted=%d updated=%d superseded=%d tombstoned=%d edges=%d/%d html=%d capped_gaps=%d attempts=%d\n",
		jobName, bookID, report.Scopes, report.Derived, report.Matched, report.Minted,
		report.Updated, report.Superseded, report.Tombstoned,
		report.EditEdges, report.MoveEdges, report.HTMLUnits, report.CappedGaps, report.Attempts)

	return int64(bookID), 1, false, nil
}
