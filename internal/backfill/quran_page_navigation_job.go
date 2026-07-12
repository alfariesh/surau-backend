package backfill

import (
	"context"
	"errors"
	"fmt"

	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	quranPageNavigationJobName = "quran-page-navigation-v1"
	quranPageCursorMultiplier  = int64(1000)
)

var (
	errQuranPageOutsideProfile = errors.New("ayah is outside frozen QPC page profile")
	errQuranPageWriteConflict  = errors.New("quran page backfill write conflict")
)

type quranPageChunk struct {
	ayahKeys    []string
	pageNumbers []int64
	lastCursor  int64
}

// quranPageNavigationJob repairs legacy Quran rows imported before the QPC
// page field was preserved. It only fills NULL values from the frozen map;
// an existing page assignment is never overwritten silently.
type quranPageNavigationJob struct{}

func (quranPageNavigationJob) Name() string { return quranPageNavigationJobName }

func (quranPageNavigationJob) ProfileVersion() int {
	return quranutil.QPCHafsPageMapProfileVersion
}

func (quranPageNavigationJob) CountRemaining(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var remaining int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM quran_ayahs WHERE page_number IS NULL`).
		Scan(&remaining); err != nil {
		return 0, fmt.Errorf("%s: count remaining: %w", quranPageNavigationJobName, err)
	}

	return remaining, nil
}

func (quranPageNavigationJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	limit int,
) (newCursor, processed int64, done bool, err error) {
	if limit <= 0 {
		limit = DefaultChunkSize
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: begin chunk: %w", quranPageNavigationJobName, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	chunk, err := loadQuranPageChunk(ctx, tx, cursor, limit)
	if err != nil {
		return cursor, 0, false, err
	}

	if len(chunk.ayahKeys) == 0 {
		return cursor, 0, true, nil
	}

	processed, err = applyQuranPageChunk(ctx, tx, &chunk)
	if err != nil {
		return cursor, 0, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return cursor, 0, false, fmt.Errorf("%s: commit chunk: %w", quranPageNavigationJobName, err)
	}

	return chunk.lastCursor, processed, len(chunk.ayahKeys) < limit, nil
}

//nolint:wsl_v5 // locked scan and deterministic page projection form one read phase
func loadQuranPageChunk(ctx context.Context, tx pgx.Tx, cursor int64, limit int) (quranPageChunk, error) {
	rows, err := tx.Query(ctx, `
SELECT surah_id, ayah_number, ayah_key,
       surah_id::bigint * $3::bigint + ayah_number AS cursor
FROM quran_ayahs
WHERE page_number IS NULL
  AND surah_id::bigint * $3::bigint + ayah_number > $1
ORDER BY surah_id, ayah_number
LIMIT $2
FOR UPDATE`, cursor, limit, quranPageCursorMultiplier)
	if err != nil {
		return quranPageChunk{}, fmt.Errorf("%s: select chunk: %w", quranPageNavigationJobName, err)
	}

	chunk := quranPageChunk{
		ayahKeys:    make([]string, 0, limit),
		pageNumbers: make([]int64, 0, limit),
		lastCursor:  cursor,
	}
	for rows.Next() {
		var (
			surahID    int
			ayahNumber int
			ayahKey    string
			rowCursor  int64
		)
		if err := rows.Scan(&surahID, &ayahNumber, &ayahKey, &rowCursor); err != nil {
			rows.Close()

			return quranPageChunk{}, fmt.Errorf("%s: scan chunk: %w", quranPageNavigationJobName, err)
		}

		page, ok := quranutil.QPCHafsPageNumber(surahID, ayahNumber)
		if !ok {
			rows.Close()

			return quranPageChunk{}, fmt.Errorf(
				"%s: %w: %s",
				quranPageNavigationJobName,
				errQuranPageOutsideProfile,
				ayahKey,
			)
		}
		chunk.ayahKeys = append(chunk.ayahKeys, ayahKey)
		chunk.pageNumbers = append(chunk.pageNumbers, int64(page))
		chunk.lastCursor = rowCursor
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return quranPageChunk{}, fmt.Errorf("%s: rows: %w", quranPageNavigationJobName, err)
	}

	return chunk, nil
}

func applyQuranPageChunk(ctx context.Context, tx pgx.Tx, chunk *quranPageChunk) (int64, error) {
	tag, err := tx.Exec(ctx, `
UPDATE quran_ayahs AS a
SET page_number = incoming.page_number::integer
FROM unnest($1::text[], $2::bigint[]) AS incoming(ayah_key, page_number)
WHERE a.ayah_key = incoming.ayah_key
  AND a.page_number IS NULL`, chunk.ayahKeys, chunk.pageNumbers)
	if err != nil {
		return 0, fmt.Errorf("%s: update chunk: %w", quranPageNavigationJobName, err)
	}

	if tag.RowsAffected() != int64(len(chunk.ayahKeys)) {
		return 0, fmt.Errorf(
			"%s: %w: updated %d of %d locked ayahs",
			quranPageNavigationJobName,
			errQuranPageWriteConflict,
			tag.RowsAffected(),
			len(chunk.ayahKeys),
		)
	}

	return tag.RowsAffected(), nil
}

var _ Job = quranPageNavigationJob{}
