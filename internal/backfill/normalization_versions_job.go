package backfill

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alfariesh/surau-backend/internal/importer"
	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	authorsNameSearchVersionJobName           = "authors-name-search-v1-version"
	quranReferenceNormalizationVersionJobName = "quran-references-normalization-v1"
)

var (
	errAuthorNormalizationDrift = errors.New("legacy author name_search does not match search-key v1")
	errAuthorChunkWriteConflict = errors.New("author normalization chunk changed after row locking")
)

// authorsNameSearchVersionJob verifies legacy derived text before stamping v1.
// NULL derivatives are generated in the same versioned update; a mismatched
// non-NULL value aborts the chunk without writing any row.
type authorsNameSearchVersionJob struct{}

func (authorsNameSearchVersionJob) Name() string { return authorsNameSearchVersionJobName }

func (authorsNameSearchVersionJob) ProfileVersion() int { return quranutil.SearchKeyV1ProfileVersion }

func (authorsNameSearchVersionJob) CountRemaining(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var remaining int64

	err := pool.QueryRow(ctx, `
SELECT count(*)
FROM authors
WHERE name_search_normalization_version IS NULL`).Scan(&remaining)
	if err != nil {
		return 0, fmt.Errorf("%s: count remaining: %w", authorsNameSearchVersionJobName, err)
	}

	return remaining, nil
}

func (authorsNameSearchVersionJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	limit int,
) (newCursor, processed int64, done bool, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: begin chunk: %w", authorsNameSearchVersionJobName, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verification and stamping share one row lock. A concurrent official
	// writer either commits first and is skipped, or waits and writes last.
	rows, err := tx.Query(ctx, `
SELECT id, name, name_search
FROM authors
WHERE id > $1
  AND name_search_normalization_version IS NULL
ORDER BY id
LIMIT $2
FOR UPDATE`, cursor, limit)
	if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: select chunk: %w", authorsNameSearchVersionJobName, err)
	}
	defer rows.Close()

	ids, normalized, err := verifyAuthorNormalizationRows(rows, limit)
	if err != nil {
		return cursor, 0, false, err
	}

	rows.Close()

	if len(ids) == 0 {
		return cursor, 0, true, nil
	}

	processed, err = writeAuthorSearchVersionChunk(
		ctx,
		tx,
		authorsNameSearchVersionJobName,
		ids,
		normalized,
		quranutil.SearchKeyV1ProfileVersion,
	)
	if err != nil {
		return cursor, 0, false, err
	}

	return ids[len(ids)-1], processed, len(ids) < limit, nil
}

func verifyAuthorNormalizationRows(
	rows pgx.Rows,
	limit int,
) (ids []int64, normalized []string, err error) {
	ids = make([]int64, 0, limit)
	normalized = make([]string, 0, limit)

	for rows.Next() {
		var (
			id       int64
			name     string
			existing sql.NullString
		)

		if err := rows.Scan(&id, &name, &existing); err != nil {
			return nil, nil, fmt.Errorf("%s: scan: %w", authorsNameSearchVersionJobName, err)
		}

		value := quranutil.NormalizeKeyV1(name)
		if existing.Valid && existing.String != value {
			return nil, nil, fmt.Errorf(
				"%w: author %d has %q, expected %q",
				errAuthorNormalizationDrift,
				id,
				existing.String,
				value,
			)
		}

		ids = append(ids, id)
		normalized = append(normalized, value)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("%s: rows: %w", authorsNameSearchVersionJobName, err)
	}

	return ids, normalized, nil
}

// quranReferenceNormalizationVersionJob checkpoints by Work. Each Work is
// verified and stamped atomically through the guarded Cross-Reference writer.
type quranReferenceNormalizationVersionJob struct{}

func (quranReferenceNormalizationVersionJob) Name() string {
	return quranReferenceNormalizationVersionJobName
}

func (quranReferenceNormalizationVersionJob) ProfileVersion() int {
	return quranutil.SearchKeyV1ProfileVersion
}

func (quranReferenceNormalizationVersionJob) CountRemaining(
	ctx context.Context,
	pool *pgxpool.Pool,
) (int64, error) {
	var remaining int64

	err := pool.QueryRow(ctx, `
SELECT count(DISTINCT book_id)
FROM (
    SELECT book_id
    FROM quran_book_references
    WHERE normalization_version IS NULL
    UNION
    SELECT book_id
    FROM quran_cross_reference_bridge
    WHERE normalization_version IS NULL
) pending`).Scan(&remaining)
	if err != nil {
		return 0, fmt.Errorf("%s: count remaining: %w", quranReferenceNormalizationVersionJobName, err)
	}

	return remaining, nil
}

func (quranReferenceNormalizationVersionJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	_ int,
) (newCursor, processed int64, done bool, err error) {
	var bookID int

	err = pool.QueryRow(ctx, `
SELECT book_id
FROM (
    SELECT book_id
    FROM quran_book_references
    WHERE normalization_version IS NULL
    UNION
    SELECT book_id
    FROM quran_cross_reference_bridge
    WHERE normalization_version IS NULL
) pending
ORDER BY CASE WHEN book_id > $1 THEN 0 ELSE 1 END, book_id
LIMIT 1`, cursor).Scan(&bookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return cursor, 0, true, nil
	}

	if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: select Work: %w", quranReferenceNormalizationVersionJobName, err)
	}

	stamped, err := importer.StampQuranReferenceNormalizationV1ForBook(ctx, pool, bookID)
	if err != nil {
		return cursor, 0, false, fmt.Errorf(
			"%s: verify and stamp Work %d: %w",
			quranReferenceNormalizationVersionJobName,
			bookID,
			err,
		)
	}

	if stamped == 0 {
		// A versioned writer may win after the pending-Work selection but
		// before the guarded stamp transaction acquires its row locks. That is
		// successful convergence, not a failed backfill. Advance the cursor and
		// let the next iteration reselect any remaining Work.
		return int64(bookID), 0, false, nil
	}

	return int64(bookID), 1, false, nil
}
