package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveSurahEditorialImport verifies the surah editorial importer against a live
// Postgres: the migration-added checksum is backfilled without a one-time lastmod
// churn (G12), re-imports are idempotent, and provenance-only changes persist without
// bumping updated_at (G11). Gated on SURAU_LIVE_PG.
func TestLiveSurahEditorialImport(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	t.Parallel()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close) // registered first → runs LAST, after the row cleanup below

	_, err = pool.Exec(ctx, `INSERT INTO quran_surahs (surah_id, ayah_count) VALUES (113, 5) ON CONFLICT (surah_id) DO NOTHING`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM quran_surah_editorial WHERE surah_id = 113`)
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM quran_surah_editorial WHERE surah_id = 113`); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	run := func(t *testing.T, body string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "surah_editorial.json")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		_, err := RunQuranSurahEditorialImport(ctx, QuranSurahEditorialOptions{PostgresURL: url, Paths: []string{path}})
		require.NoError(t, err)
	}
	read := func(t *testing.T) (checksum, reviewedBy *string, updatedAt time.Time) {
		t.Helper()
		require.NoError(t, pool.QueryRow(
			ctx,
			`SELECT checksum, reviewed_by, updated_at FROM quran_surah_editorial WHERE surah_id=113 AND lang='id'`,
		).Scan(&checksum, &reviewedBy, &updatedAt))
		return checksum, reviewedBy, updatedAt
	}

	// G12: simulate a row that existed BEFORE migration 20260623000002 — checksum NULL,
	// an old updated_at. Its content matches the payload we are about to import.
	_, err = pool.Exec(ctx, `
INSERT INTO quran_surah_editorial (surah_id, lang, meta_title, checksum, updated_at)
VALUES (113, 'id', 'Seed Title', NULL, now() - interval '10 days')`)
	require.NoError(t, err)
	_, _, u0 := read(t)

	// First post-migration re-import with identical content must backfill checksum but
	// NOT bump updated_at (no false "everything changed" sitemap signal).
	run(t, `[{"surah_id":113,"lang":"id","meta_title":"Seed Title"}]`)
	checksum, _, u1 := read(t)
	require.NotNil(t, checksum, "checksum is backfilled")
	assert.True(t, u0.Equal(u1), "NULL-checksum backfill must NOT bump updated_at (G12)")

	// Idempotent re-import.
	run(t, `[{"surah_id":113,"lang":"id","meta_title":"Seed Title"}]`)
	_, _, u2 := read(t)
	assert.True(t, u1.Equal(u2), "identical re-import must not bump updated_at")

	// Provenance-only change persists without bumping updated_at (G11).
	run(t, `[{"surah_id":113,"lang":"id","meta_title":"Seed Title","reviewed_by":"rev2"}]`)
	_, reviewedBy, u3 := read(t)
	require.NotNil(t, reviewedBy)
	assert.Equal(t, "rev2", *reviewedBy, "provenance-only change persisted")
	assert.True(t, u2.Equal(u3), "provenance-only change must NOT bump updated_at")

	// Content change DOES bump updated_at.
	run(t, `[{"surah_id":113,"lang":"id","meta_title":"New Title"}]`)
	_, _, u4 := read(t)
	assert.True(t, u4.After(u3), "content change bumps updated_at")
}

// TestRunQuranSurahEditorialImportValidation covers the up-front, DB-free guards
// (strict decode + duplicate-key detection) — they fire before any DB connection.
func TestRunQuranSurahEditorialImportValidation(t *testing.T) {
	t.Parallel()

	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "se.json")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		return path
	}

	t.Run("unknown field rejected", func(t *testing.T) {
		t.Parallel()
		_, err := RunQuranSurahEditorialImport(context.Background(), QuranSurahEditorialOptions{
			DryRun: true,
			Paths:  []string{write(t, `[{"surah_id":113,"lang":"id","keutamaan":"typo key"}]`)},
		})
		require.Error(t, err)
	})

	t.Run("duplicate surah+lang rejected", func(t *testing.T) {
		t.Parallel()
		_, err := RunQuranSurahEditorialImport(context.Background(), QuranSurahEditorialOptions{
			DryRun: true,
			Paths:  []string{write(t, `[{"surah_id":113,"lang":"id","meta_title":"a"},{"surah_id":113,"lang":"id","meta_title":"b"}]`)},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})
}
