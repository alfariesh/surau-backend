package importer

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// cleanupInsertedQuranImporterSurah removes only a synthetic importer parent
// and its aliases. Production slug history is never touched; the replication
// override is scoped to this test-owned marker and transaction.
func cleanupInsertedQuranImporterSurah(
	ctx context.Context,
	pool *pgxpool.Pool,
	surahID int,
	metadataMarker string,
) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin importer fixture cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`); err != nil {
		return fmt.Errorf("disable importer fixture triggers: %w", err)
	}

	if _, err = tx.Exec(ctx, `
DELETE FROM quran_surah_slug_registry registry
USING quran_surahs surah
WHERE registry.surah_id = surah.surah_id
  AND surah.surah_id = $1
  AND surah.metadata ->> $2 = 'true'`, surahID, metadataMarker); err != nil {
		return fmt.Errorf("delete importer fixture slugs: %w", err)
	}

	if _, err = tx.Exec(ctx, `
DELETE FROM quran_surahs
WHERE surah_id = $1
  AND metadata ->> $2 = 'true'`, surahID, metadataMarker); err != nil {
		return fmt.Errorf("delete importer fixture surah: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit importer fixture cleanup: %w", err)
	}

	return nil
}
