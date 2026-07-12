package backfill

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const quranCitableStaleWhereSQL = `
    (s.units_derived_at IS NULL OR s.units_stale_at IS NOT NULL)`

const quranCitableDerivedWhereSQL = `
    s.units_derived_at IS NOT NULL`

type quranCitableUnitsJob struct{}

func (quranCitableUnitsJob) Name() string { return "citable-units-quran" }

func (quranCitableUnitsJob) ProfileVersion() int { return searchtext.ProfileVersion }

func (quranCitableUnitsJob) CountRemaining(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	return quranCitableCountWhere(ctx, pool, quranCitableStaleWhereSQL, "citable-units-quran")
}

func (quranCitableUnitsJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	_ int,
) (newCursor, processed int64, done bool, err error) {
	return quranCitableProcessNextSurah(
		ctx, pool, cursor, quranCitableStaleWhereSQL, "citable-units-quran", true,
	)
}

type quranCitableUnitsRederiveJob struct{}

func (quranCitableUnitsRederiveJob) Name() string { return "citable-units-quran-rederive" }

func (quranCitableUnitsRederiveJob) ProfileVersion() int { return searchtext.ProfileVersion }

func (quranCitableUnitsRederiveJob) CountRemaining(
	ctx context.Context,
	pool *pgxpool.Pool,
) (int64, error) {
	return quranCitableCountWhere(
		ctx, pool, quranCitableDerivedWhereSQL, "citable-units-quran-rederive",
	)
}

func (quranCitableUnitsRederiveJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	_ int,
) (newCursor, processed int64, done bool, err error) {
	return quranCitableProcessNextSurah(
		ctx, pool, cursor, quranCitableDerivedWhereSQL, "citable-units-quran-rederive", false,
	)
}

func quranCitableCountWhere(
	ctx context.Context,
	pool *pgxpool.Pool,
	whereSQL, jobName string,
) (int64, error) {
	var remaining int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM quran_surahs s WHERE `+whereSQL).Scan(&remaining); err != nil {
		return 0, fmt.Errorf("%s: count remaining: %w", jobName, err)
	}

	return remaining, nil
}

//nolint:wsl_v5 // select, reconcile, and progress output are one compact resumable job step
func quranCitableProcessNextSurah(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	whereSQL, jobName string,
	wrap bool,
) (newCursor, processed int64, done bool, err error) {
	var surahID int
	if err := pool.QueryRow(ctx, `
SELECT s.surah_id
FROM quran_surahs s
WHERE `+whereSQL+`
  AND ($2 OR s.surah_id > $1)
ORDER BY CASE WHEN s.surah_id > $1 THEN 0 ELSE 1 END, s.surah_id
LIMIT 1`, cursor, wrap).Scan(&surahID); errors.Is(err, pgx.ErrNoRows) {
		return cursor, 0, true, nil
	} else if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: select next surah: %w", jobName, err)
	}

	service := unitregistry.New(persistent.NewCitableUnitRepo(&postgres.Postgres{Pool: pool}))
	report, err := service.ReconcileQuranSurah(ctx, surahID)
	if err != nil {
		return cursor, 0, false, fmt.Errorf("%s: reconcile surah %d: %w", jobName, surahID, err)
	}

	fmt.Fprintf(os.Stdout,
		"%s: surah=%d ayahs=%d derived=%d matched=%d minted=%d updated=%d superseded=%d tombstoned=%d edges=%d attempts=%d\n",
		jobName, surahID, report.Ayahs, report.Derived, report.Matched, report.Minted,
		report.Updated, report.Superseded, report.Tombstoned, report.Edges, report.Attempts)

	return int64(surahID), 1, false, nil
}
