package persistent

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

const (
	quranSlugDrillAck        = "destroy-surau-q4-migration-drill"
	quranSlugPreVersion      = int64(20260714000003)
	quranSlugDrillSurahID    = 114
	quranSlugDrillInitial    = "q4-drill-initial"
	quranSlugDrillHistorical = "q4-drill-renamed"
)

// TestQuranSlugRegistryMigrationDrill proves Q-4 can replay up -> down -> up
// while the registry contains only its backfill, then refuses a destructive
// down as soon as a historical public slug exists. It runs only against CI's
// dedicated disposable migration database.
//
//nolint:paralleltest // destructive schema replay is serial and environment-gated
func TestQuranSlugRegistryMigrationDrill(t *testing.T) {
	databaseURL := os.Getenv("SURAU_Q4_MIGRATION_DRILL_PG")
	if databaseURL == "" {
		t.Skip("SURAU_Q4_MIGRATION_DRILL_PG not set")
	}

	require.Equal(t, quranSlugDrillAck, os.Getenv("SURAU_Q4_MIGRATION_DRILL_ACK"),
		"refusing destructive Q-4 migration drill without acknowledgement")

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	assertQuranSlugDrillTarget(ctx, t, pg)

	up, err := os.ReadFile("../../../migrations/20260715000001_quran_sitemap_slug_registry.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260715000001_quran_sitemap_slug_registry.down.sql")
	require.NoError(t, err)

	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	seedQuranSlugDrill(ctx, t, tx)

	_, err = tx.Exec(ctx, string(up))
	require.NoError(t, err)
	assertQuranSlugRegistryRow(ctx, t, tx, quranSlugDrillInitial, quranSlugDrillSurahID)

	_, err = tx.Exec(ctx, string(down))
	require.NoError(t, err)
	assertQuranSlugRegistryAbsent(ctx, t, tx)

	_, err = tx.Exec(ctx, string(up))
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE quran_surahs
SET slug = $1
WHERE surah_id = $2`, quranSlugDrillHistorical, quranSlugDrillSurahID)
	require.NoError(t, err)
	assertQuranSlugRegistryRow(ctx, t, tx, quranSlugDrillInitial, quranSlugDrillSurahID)
	assertQuranSlugRegistryRow(ctx, t, tx, quranSlugDrillHistorical, quranSlugDrillSurahID)

	_, err = tx.Exec(ctx, "SAVEPOINT q4_down_refusal")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(down))
	assertQuranSlugDownRefused(t, err)
	_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT q4_down_refusal")
	require.NoError(t, err)

	require.NoError(t, tx.Rollback(ctx))
}

func assertQuranSlugDrillTarget(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
) {
	t.Helper()

	var (
		databaseName string
		version      int64
		dirty        bool
		quranRows    int
		registry     *string
	)
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT current_database(), version, dirty
FROM schema_migrations`).Scan(&databaseName, &version, &dirty))
	require.Equal(t, quranEditorialDrillDatabase, databaseName,
		"refusing Q-4 migration drill outside its disposable database")
	require.Equal(t, quranSlugPreVersion, version,
		"Q-4 drill requires the schema immediately before Q-4")
	require.False(t, dirty)

	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM quran_surahs)
     + (SELECT count(*) FROM quran_ayahs)
     + (SELECT count(*) FROM quran_surah_editorial)
     + (SELECT count(*) FROM quran_ayah_editorial)`).Scan(&quranRows))
	require.Zero(t, quranRows, "Q-4 migration drill requires pristine Quran tables")
	require.NoError(t, pg.Pool.QueryRow(ctx,
		`SELECT to_regclass('public.quran_surah_slug_registry')::TEXT`).Scan(&registry))
	require.Nil(t, registry, "Q-4 registry already exists")
}

func seedQuranSlugDrill(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()

	_, err := tx.Exec(ctx, `SET LOCAL surau.quran_editorial_writer = 'quran-editorial-service'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO quran_surahs (
    surah_id, name_arabic, name_latin, ayah_count, slug
) VALUES ($1, 'سورة الاختبار', 'Q-4 Migration Drill', 1, $2)`,
		quranSlugDrillSurahID, quranSlugDrillInitial)
	require.NoError(t, err)
}

func assertQuranSlugRegistryRow(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	slug string,
	surahID int,
) {
	t.Helper()

	var got int
	require.NoError(t, tx.QueryRow(ctx, `
SELECT surah_id
FROM quran_surah_slug_registry
WHERE slug = $1`, slug).Scan(&got))
	require.Equal(t, surahID, got)
}

func assertQuranSlugRegistryAbsent(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()

	var registry *string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT to_regclass('public.quran_surah_slug_registry')::TEXT`).Scan(&registry))
	require.Nil(t, registry)
}

func assertQuranSlugDownRefused(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected PostgreSQL error, got %T", err)
	require.Equal(t, "55000", pgErr.Code)
	require.Contains(t, pgErr.Message, "historical Quran surah slugs exist")
}
