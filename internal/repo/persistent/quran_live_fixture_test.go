package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

var errQuranSurahLiveFixtureClaimed = errors.New("quran surah live fixture was claimed concurrently")

func claimQuranSurahLiveFixture(
	ctx context.Context,
	pg *postgres.Postgres,
	preferredSurahID int,
	slugPrefix string,
	nameLatin string,
	ayahCount int,
	metadataMarker string,
) (surahID int, inserted bool, err error) {
	err = pg.Pool.QueryRow(ctx, `
SELECT surah.surah_id
FROM quran_surahs surah
JOIN quran_surah_slug_registry registry
  ON registry.surah_id = surah.surah_id AND registry.slug = surah.slug
WHERE surah.surah_id = $1`, preferredSurahID).Scan(&surahID)
	if err == nil {
		return surahID, false, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("find preferred fixture surah: %w", err)
	}

	err = pg.Pool.QueryRow(ctx, `
SELECT candidate.surah_id
FROM generate_series(1, 114) AS candidate(surah_id)
WHERE NOT EXISTS (
    SELECT 1 FROM quran_surahs surah WHERE surah.surah_id = candidate.surah_id
)
ORDER BY (candidate.surah_id = $1) DESC, candidate.surah_id DESC
LIMIT 1`, preferredSurahID).Scan(&surahID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = pg.Pool.QueryRow(ctx, `
SELECT surah.surah_id
FROM quran_surahs surah
JOIN quran_surah_slug_registry registry
  ON registry.surah_id = surah.surah_id AND registry.slug = surah.slug
ORDER BY (surah.surah_id = $1) DESC, surah.surah_id DESC
LIMIT 1`, preferredSurahID).Scan(&surahID)
		if err != nil {
			return 0, false, fmt.Errorf("find registered fixture surah: %w", err)
		}

		return surahID, false, nil
	}

	if err != nil {
		return 0, false, fmt.Errorf("find empty fixture surah: %w", err)
	}

	inserted, err = insertQuranSurahLiveFixture(
		ctx,
		pg,
		surahID,
		fmt.Sprintf("%s-%d", slugPrefix, surahID),
		nameLatin,
		ayahCount,
		metadataMarker,
	)
	if err != nil {
		return 0, false, err
	}

	if !inserted {
		return 0, false, fmt.Errorf("%w: %d", errQuranSurahLiveFixtureClaimed, surahID)
	}

	return surahID, true, nil
}

func insertQuranSurahLiveFixture(
	ctx context.Context,
	pg *postgres.Postgres,
	surahID int,
	slug string,
	nameLatin string,
	ayahCount int,
	metadataMarker string,
) (bool, error) {
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin fixture insert: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if markerErr := markQuranEditorialWriter(ctx, tx); markerErr != nil {
		return false, markerErr
	}

	var insertedSurahID int

	err = tx.QueryRow(ctx, `
INSERT INTO quran_surahs (surah_id, slug, name_latin, ayah_count, metadata)
VALUES ($1, $2, $3, $4, jsonb_build_object($5::TEXT, true))
ON CONFLICT (surah_id) DO NOTHING
RETURNING surah_id`, surahID, slug, nameLatin, ayahCount, metadataMarker).Scan(&insertedSurahID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}

	if err != nil {
		return false, fmt.Errorf("insert fixture surah: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit fixture insert: %w", err)
	}

	return insertedSurahID != 0, nil
}

// cleanupInsertedQuranSurah removes only a test-owned parent and its slug
// registry rows. Production aliases remain append-only; session replication is
// disabled here solely so live tests do not leave synthetic aliases in a real
// operator database.
func cleanupInsertedQuranSurah(
	ctx context.Context,
	pg *postgres.Postgres,
	surahID int,
	metadataMarker string,
) error {
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fixture cleanup: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`); err != nil {
		return fmt.Errorf("disable fixture triggers: %w", err)
	}

	if _, err = tx.Exec(ctx, `
DELETE FROM quran_surah_slug_registry registry
USING quran_surahs surah
WHERE registry.surah_id = surah.surah_id
  AND surah.surah_id = $1
  AND surah.metadata ->> $2 = 'true'`, surahID, metadataMarker); err != nil {
		return fmt.Errorf("delete fixture slug registry: %w", err)
	}

	if _, err = tx.Exec(ctx, `
DELETE FROM quran_surahs
WHERE surah_id = $1
  AND metadata ->> $2 = 'true'`, surahID, metadataMarker); err != nil {
		return fmt.Errorf("delete fixture surah: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit fixture cleanup: %w", err)
	}

	return nil
}
