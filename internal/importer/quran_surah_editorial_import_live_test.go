package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveSurahEditorialImportWorkflow proves the Q-1 importer contract against
// a migrated throwaway Postgres. It deliberately uses a surah with no editorial
// state so a live corpus row is never overwritten.
//
//	SURAU_LIVE_PG=postgres://... go test -p 1 ./internal/importer -run TestLiveSurahEditorialImportWorkflow -v
//
//nolint:paralleltest // serial live-DB workflow proof; the linear assertions mirror the operator flow
func TestLiveSurahEditorialImportWorkflow(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var surahID int

	err = pool.QueryRow(ctx, `
SELECT candidate.surah_id
FROM generate_series(1, 114) AS candidate(surah_id)
WHERE NOT EXISTS (
    SELECT 1 FROM quran_surahs surah WHERE surah.surah_id = candidate.surah_id
)
ORDER BY candidate.surah_id DESC
LIMIT 1`).Scan(&surahID)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Skip("no isolated surah scope is available for the importer live fixture")
	}

	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO quran_surahs (surah_id, ayah_count, metadata)
		VALUES ($1, 0, '{"q1_surah_importer_fixture":true}'::jsonb)`, surahID)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx := context.Background()

		cleanupErr := withQuranEditorialFixtureWriter(cleanupCtx, pool, func(tx pgx.Tx) error {
			if _, txErr := tx.Exec(cleanupCtx, `
DELETE FROM quran_editorial_revisions
WHERE resource_type = 'surah' AND surah_id = $1`, surahID); txErr != nil {
				return txErr
			}

			if _, txErr := tx.Exec(cleanupCtx, `
DELETE FROM quran_surah_editorial WHERE surah_id = $1`, surahID); txErr != nil {
				return txErr
			}

			return nil
		})
		if cleanupErr != nil {
			t.Logf("cleanup Q-1 surah importer fixture: %v", cleanupErr)
		}

		if parentCleanupErr := cleanupInsertedQuranImporterSurah(
			cleanupCtx, pool, surahID, "q1_surah_importer_fixture",
		); parentCleanupErr != nil {
			t.Logf("cleanup Q-1 surah importer parent: %v", parentCleanupErr)
		}
	})

	// Start metadata from a known private baseline. This edit is fixture-only and
	// therefore uses the same transaction-local marker as all direct test DML.
	require.NoError(t, withQuranEditorialFixtureWriter(ctx, pool, func(tx pgx.Tx) error {
		_, txErr := tx.Exec(ctx, `
UPDATE quran_surahs
SET slug = NULL, chronological_order = NULL, ruku_count = NULL
WHERE surah_id = $1`, surahID)

		return txErr
	}))

	var desiredChronologicalOrder int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT candidate.value
FROM generate_series(1, 114) AS candidate(value)
WHERE NOT EXISTS (
    SELECT 1 FROM quran_surahs surah
    WHERE surah.chronological_order = candidate.value
)
ORDER BY candidate.value DESC
LIMIT 1`).Scan(&desiredChronologicalOrder))

	desiredSlug := fmt.Sprintf("q1-workflow-live-%d-%d", surahID, time.Now().UnixNano())
	desiredRukuCount := 9

	run := func(body string, publish bool) (QuranSurahEditorialStats, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "surah_editorial.json")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

		return RunQuranSurahEditorialImport(ctx, QuranSurahEditorialOptions{
			PostgresURL: url,
			Paths:       []string{path},
			Publish:     publish,
		})
	}

	full := fmt.Sprintf(`[{"surah_id":%d,"slug":%q,"chronological_order":%d,"ruku_count":%d,"lang":"id","meta_title":"Judul awal","meta_description":"Deskripsi tetap","arti_nama":"Arti tetap","keutamaan_html":"<p>Keutamaan tetap.</p>","asbabun_nuzul_html":"<p>Asbab tetap.</p>","pokok_kandungan_html":"<p>Pokok tetap.</p>","author_name":"Editor fixture","reviewed_by":"Reviewer fixture","license_status":"permitted"}]`,
		surahID, desiredSlug, desiredChronologicalOrder, desiredRukuCount)

	// Safe default: the importer creates only a draft. Public metadata and the
	// existing public read projection remain untouched.
	stats, err := run(full, false)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Changed)
	assert.Zero(t, stats.Published)

	var draftTitle, draftDescription, draftLicense string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT meta_title, meta_description, license_status
FROM quran_surah_editorial
WHERE surah_id = $1 AND lang = 'id' AND status = 'draft'`, surahID).Scan(
		&draftTitle, &draftDescription, &draftLicense,
	))
	assert.Equal(t, "Judul awal", draftTitle)
	assert.Equal(t, "Deskripsi tetap", draftDescription)
	assert.Equal(t, entity.LicenseStatusPermitted, draftLicense)
	assertSurahImportMetadata(ctx, t, pool, surahID, nil, nil, nil)
	assertQuranSurahPublicCount(ctx, t, pool, surahID, 0)
	assertSurahImportRevisions(ctx, t, pool, surahID, 1)

	// The exact same payload publishes only because Publish is explicit. Metadata
	// is applied in that same transaction, after the license preflight succeeds.
	stats, err = run(full, true)
	require.NoError(t, err)
	assert.Zero(t, stats.Changed, "the existing identical draft is a no-op")
	assert.Equal(t, 1, stats.Published)
	assertSurahImportMetadata(
		ctx, t, pool, surahID, &desiredSlug, &desiredChronologicalOrder, &desiredRukuCount,
	)
	assertQuranSurahPublicCount(ctx, t, pool, surahID, 1)
	assertSurahImportRevisions(ctx, t, pool, surahID, 2)

	// A partial import derives from the current draft. Omitted editorial fields
	// and an omitted license therefore survive instead of being cleared or reset
	// to needs_review. The published copy is still the old one until promotion.
	partial := fmt.Sprintf(`[{"surah_id":%d,"lang":"id","meta_title":"Judul draft baru"}]`, surahID)
	stats, err = run(partial, false)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Changed)

	var draftArtiNama, draftKeutamaan string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT meta_title, meta_description, arti_nama, keutamaan_html, license_status
FROM quran_surah_editorial
WHERE surah_id = $1 AND lang = 'id' AND status = 'draft'`, surahID).Scan(
		&draftTitle, &draftDescription, &draftArtiNama, &draftKeutamaan, &draftLicense,
	))
	assert.Equal(t, "Judul draft baru", draftTitle)
	assert.Equal(t, "Deskripsi tetap", draftDescription)
	assert.Equal(t, "Arti tetap", draftArtiNama)
	assert.Equal(t, "<p>Keutamaan tetap.</p>", draftKeutamaan)
	assert.Equal(t, entity.LicenseStatusPermitted, draftLicense)

	var publicTitle string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT meta_title FROM quran_surah_editorial_public
WHERE surah_id = $1 AND lang = 'id'`, surahID).Scan(&publicTitle))
	assert.Equal(t, "Judul awal", publicTitle, "draft changes must remain private")

	var (
		beforeNoop          time.Time
		beforeNoopRevisions int
	)

	require.NoError(t, pool.QueryRow(ctx, `
SELECT updated_at FROM quran_surah_editorial
WHERE surah_id = $1 AND lang = 'id' AND status = 'draft'`, surahID).Scan(&beforeNoop))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM quran_editorial_revisions
WHERE resource_type = 'surah' AND surah_id = $1 AND lang = 'id'`, surahID).Scan(&beforeNoopRevisions))

	stats, err = run(partial, false)
	require.NoError(t, err)
	assert.Zero(t, stats.Changed)

	var (
		afterNoop          time.Time
		afterNoopRevisions int
	)

	require.NoError(t, pool.QueryRow(ctx, `
SELECT updated_at FROM quran_surah_editorial
WHERE surah_id = $1 AND lang = 'id' AND status = 'draft'`, surahID).Scan(&afterNoop))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM quran_editorial_revisions
WHERE resource_type = 'surah' AND surah_id = $1 AND lang = 'id'`, surahID).Scan(&afterNoopRevisions))
	assert.True(t, beforeNoop.Equal(afterNoop), "no-op must preserve the optimistic-lock token")
	assert.Equal(t, beforeNoopRevisions, afterNoopRevisions, "no-op must not append a revision")

	stats, err = run(partial, true)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Published)
	require.NoError(t, pool.QueryRow(ctx, `
SELECT meta_title FROM quran_surah_editorial_public
WHERE surah_id = $1 AND lang = 'id'`, surahID).Scan(&publicTitle))
	assert.Equal(t, "Judul draft baru", publicTitle)
	assertSurahImportRevisions(ctx, t, pool, surahID, 4)

	// One non-permitted row aborts the whole publish batch: not even the permitted
	// sibling draft or its revision may survive, and public metadata stays put.
	rollbackBatch := fmt.Sprintf(`[
{"surah_id":%d,"slug":"must-not-apply","lang":"ar","meta_title":"Permitted sibling","license_status":"permitted"},
{"surah_id":%d,"lang":"en","meta_title":"Blocked sibling","license_status":"needs_review"}
]`, surahID, surahID)
	_, err = run(rollbackBatch, true)
	require.ErrorIs(t, err, entity.ErrLicenseNotPermitted)

	var rolledBackRows, rolledBackRevisions int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM quran_surah_editorial
WHERE surah_id = $1 AND lang IN ('ar', 'en')`, surahID).Scan(&rolledBackRows))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM quran_editorial_revisions
WHERE resource_type = 'surah' AND surah_id = $1 AND lang IN ('ar', 'en')`, surahID).Scan(&rolledBackRevisions))
	assert.Zero(t, rolledBackRows)
	assert.Zero(t, rolledBackRevisions)
	assertSurahImportMetadata(
		ctx, t, pool, surahID, &desiredSlug, &desiredChronologicalOrder, &desiredRukuCount,
	)
}

func assertSurahImportMetadata(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	surahID int,
	wantSlug *string,
	wantChronologicalOrder, wantRukuCount *int,
) {
	t.Helper()

	var (
		slug                          sql.NullString
		chronologicalOrder, rukuCount sql.NullInt64
	)

	require.NoError(t, pool.QueryRow(ctx, `
SELECT slug, chronological_order, ruku_count
FROM quran_surahs WHERE surah_id = $1`, surahID).Scan(
		&slug, &chronologicalOrder, &rukuCount,
	))
	assertNullableString(t, wantSlug, slug)
	assertNullableInt(t, wantChronologicalOrder, chronologicalOrder)
	assertNullableInt(t, wantRukuCount, rukuCount)
}

func assertQuranSurahPublicCount(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	surahID, want int,
) {
	t.Helper()

	var count int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM quran_surah_editorial_public
WHERE surah_id = $1 AND lang = 'id'`, surahID).Scan(&count))
	assert.Equal(t, want, count)
}

func assertSurahImportRevisions(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	surahID, want int,
) {
	t.Helper()

	var total, imported int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*), count(*) FILTER (WHERE origin = 'import')
FROM quran_editorial_revisions
WHERE resource_type = 'surah' AND surah_id = $1 AND lang = 'id'`, surahID).Scan(
		&total, &imported,
	))
	assert.Equal(t, want, total)
	assert.Equal(t, total, imported, "every importer revision must record origin=import")
}

func assertNullableString(t *testing.T, want *string, got sql.NullString) {
	t.Helper()

	if want == nil {
		assert.False(t, got.Valid)

		return
	}

	require.True(t, got.Valid)
	assert.Equal(t, *want, got.String)
}

func assertNullableInt(t *testing.T, want *int, got sql.NullInt64) {
	t.Helper()

	if want == nil {
		assert.False(t, got.Valid)

		return
	}

	require.True(t, got.Valid)
	assert.EqualValues(t, *want, got.Int64)
}

func nullableStringValue(value sql.NullString) any {
	if value.Valid {
		return value.String
	}

	return nil
}

func nullableInt64Value(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}

	return nil
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

	t.Run("empty slug rejected", func(t *testing.T) {
		t.Parallel()
		_, err := RunQuranSurahEditorialImport(context.Background(), QuranSurahEditorialOptions{
			DryRun: true,
			Paths:  []string{write(t, `[{"surah_id":113,"lang":"id","slug":""}]`)},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "slug")
	})

	t.Run("zero ruku_count rejected", func(t *testing.T) {
		t.Parallel()
		_, err := RunQuranSurahEditorialImport(context.Background(), QuranSurahEditorialOptions{
			DryRun: true,
			Paths:  []string{write(t, `[{"surah_id":113,"lang":"id","ruku_count":0}]`)},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ruku_count")
	})

	t.Run("duplicate chronological_order rejected", func(t *testing.T) {
		t.Parallel()
		_, err := RunQuranSurahEditorialImport(context.Background(), QuranSurahEditorialOptions{
			DryRun: true,
			Paths:  []string{write(t, `[{"surah_id":112,"lang":"id","chronological_order":5},{"surah_id":113,"lang":"id","chronological_order":5}]`)},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chronological_order")
	})
}
