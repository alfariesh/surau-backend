package importer

import (
	"context"
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

// TestLiveAyahEditorialImportWorkflow proves draft-first importing, explicit
// publication, revision provenance, no-op behavior, and partial-field semantics
// against a migrated throwaway Postgres.
//
//	SURAU_LIVE_PG=postgres://... go test -p 1 ./internal/importer -run TestLiveAyahEditorialImportWorkflow -v
//
//nolint:paralleltest // serial live-DB workflow proof; assertions follow one editorial lifecycle
func TestLiveAyahEditorialImportWorkflow(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var (
		surahID        int
		ayahNumber     int
		insertedParent bool
	)

	// Use only a canonical slot proven absent. A fixed-number pre-clean could
	// destroy corpus data when this required live suite runs against dev.
	err = pool.QueryRow(ctx, `
SELECT surah.surah_id, candidate.ayah_number
FROM quran_surahs surah
CROSS JOIN generate_series(1, 286) AS candidate(ayah_number)
WHERE NOT EXISTS (
    SELECT 1 FROM quran_ayahs ayah
    WHERE ayah.surah_id = surah.surah_id
      AND ayah.ayah_number = candidate.ayah_number
)
ORDER BY surah.surah_id DESC, candidate.ayah_number DESC
LIMIT 1`).Scan(&surahID, &ayahNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		err = pool.QueryRow(ctx, `
SELECT candidate.surah_id
FROM generate_series(1, 114) AS candidate(surah_id)
WHERE NOT EXISTS (
    SELECT 1 FROM quran_surahs surah WHERE surah.surah_id = candidate.surah_id
)
ORDER BY candidate.surah_id DESC
LIMIT 1`).Scan(&surahID)
		if errors.Is(err, pgx.ErrNoRows) {
			t.Skip("no empty Quran ayah slot is available for the importer live fixture")
		}

		require.NoError(t, err)

		ayahNumber = 286
		_, err = pool.Exec(ctx, `
INSERT INTO quran_surahs (surah_id, ayah_count, metadata)
		VALUES ($1, 0, '{"q1_ayah_importer_fixture":true}'::jsonb)`, surahID)
		require.NoError(t, err)

		insertedParent = true
	} else {
		require.NoError(t, err)
	}

	ayahKey := fmt.Sprintf("%d:%d", surahID, ayahNumber)

	cleanup := func(cleanupCtx context.Context) error {
		if cleanupErr := withQuranEditorialFixtureWriter(cleanupCtx, pool, func(tx pgx.Tx) error {
			if _, txErr := tx.Exec(cleanupCtx, `
DELETE FROM quran_editorial_revisions
WHERE resource_type = 'ayah' AND surah_id = $1 AND ayah_number = $2`,
				surahID, ayahNumber); txErr != nil {
				return txErr
			}

			_, txErr := tx.Exec(cleanupCtx, `
DELETE FROM quran_ayah_editorial
WHERE surah_id = $1 AND ayah_number = $2`, surahID, ayahNumber)

			return txErr
		}); cleanupErr != nil {
			return cleanupErr
		}

		_, cleanupErr := pool.Exec(cleanupCtx, `
DELETE FROM quran_ayahs WHERE surah_id = $1 AND ayah_number = $2`, surahID, ayahNumber)

		return cleanupErr
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if cleanupErr := cleanup(cleanupCtx); cleanupErr != nil {
			t.Logf("cleanup Q-1 ayah importer fixture: %v", cleanupErr)
		}

		if insertedParent {
			if _, cleanupErr := pool.Exec(cleanupCtx,
				`DELETE FROM quran_surahs
                 WHERE surah_id = $1
                   AND metadata ->> 'q1_ayah_importer_fixture' = 'true'`, surahID); cleanupErr != nil {
				t.Logf("cleanup Q-1 ayah importer surah parent: %v", cleanupErr)
			}
		}
	})

	_, err = pool.Exec(ctx, `
INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key, metadata)
VALUES ($1, $2, $3, '{"q1_importer_fixture":true}'::jsonb)`, surahID, ayahNumber, ayahKey)
	require.NoError(t, err)

	run := func(body string, publish bool) (QuranAyahEditorialStats, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "ayah_editorial.json")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

		return RunQuranAyahEditorialImport(ctx, QuranAyahEditorialOptions{
			PostgresURL: url,
			Paths:       []string{path},
			Publish:     publish,
		})
	}

	full := fmt.Sprintf(`[{"surah_id":%d,"ayah_number":%d,"lang":"id","meta_title":"Judul awal","meta_description":"Deskripsi tetap","intisari_html":"<p>Intisari tetap.</p>","keutamaan_html":"<p>Keutamaan tetap.</p>","tafsir_range":"280","author_name":"Editor fixture","reviewed_by":"Reviewer fixture","license_status":"permitted","faq":[{"question":"Pertanyaan tetap?","answer_html":"<p>Jawaban tetap.</p>"}]}]`,
		surahID, ayahNumber)

	// Default import is private even when the supplied license is permitted.
	stats, err := run(full, false)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Upserted)
	assert.Zero(t, stats.Published)

	var (
		draftTitle,
		draftDescription,
		draftIntisari,
		draftLicense string
		draftFAQLen int
	)

	require.NoError(t, pool.QueryRow(ctx, `
SELECT meta_title, meta_description, intisari_html, license_status, jsonb_array_length(faq)
FROM quran_ayah_editorial
WHERE surah_id = $1 AND ayah_number = $2 AND lang = 'id' AND status = 'draft'`,
		surahID, ayahNumber).Scan(
		&draftTitle, &draftDescription, &draftIntisari, &draftLicense, &draftFAQLen,
	))
	assert.Equal(t, "Judul awal", draftTitle)
	assert.Equal(t, "Deskripsi tetap", draftDescription)
	assert.Equal(t, "<p>Intisari tetap.</p>", draftIntisari)
	assert.Equal(t, entity.LicenseStatusPermitted, draftLicense)
	assert.Equal(t, 1, draftFAQLen)
	assertQuranAyahPublicCount(ctx, t, pool, surahID, ayahNumber, 0)
	assertAyahImportRevisions(ctx, t, pool, surahID, ayahNumber, 1)

	// Publication is a separate explicit action; the identical draft itself stays
	// a no-op while its first published snapshot is appended.
	stats, err = run(full, true)
	require.NoError(t, err)
	assert.Zero(t, stats.Upserted)
	assert.Equal(t, 1, stats.Published)
	assertQuranAyahPublicCount(ctx, t, pool, surahID, ayahNumber, 1)
	assertAyahImportRevisions(ctx, t, pool, surahID, ayahNumber, 2)

	// Omitted content, FAQ, and license fields inherit from the current draft.
	// Only the new title is private until another explicit publication.
	partial := fmt.Sprintf(`[{"surah_id":%d,"ayah_number":%d,"lang":"id","meta_title":"Judul draft baru"}]`,
		surahID, ayahNumber)
	stats, err = run(partial, false)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Upserted)

	require.NoError(t, pool.QueryRow(ctx, `
SELECT meta_title, meta_description, intisari_html, license_status, jsonb_array_length(faq)
FROM quran_ayah_editorial
WHERE surah_id = $1 AND ayah_number = $2 AND lang = 'id' AND status = 'draft'`,
		surahID, ayahNumber).Scan(
		&draftTitle, &draftDescription, &draftIntisari, &draftLicense, &draftFAQLen,
	))
	assert.Equal(t, "Judul draft baru", draftTitle)
	assert.Equal(t, "Deskripsi tetap", draftDescription)
	assert.Equal(t, "<p>Intisari tetap.</p>", draftIntisari)
	assert.Equal(t, entity.LicenseStatusPermitted, draftLicense)
	assert.Equal(t, 1, draftFAQLen, "absent faq must preserve the stored FAQ")

	var publicTitle string
	require.NoError(t, pool.QueryRow(ctx, `
SELECT meta_title FROM quran_ayah_editorial_public
WHERE surah_id = $1 AND ayah_number = $2 AND lang = 'id'`,
		surahID, ayahNumber).Scan(&publicTitle))
	assert.Equal(t, "Judul awal", publicTitle, "draft changes must remain private")

	var (
		beforeNoop          time.Time
		beforeNoopRevisions int
	)

	require.NoError(t, pool.QueryRow(ctx, `
SELECT updated_at FROM quran_ayah_editorial
WHERE surah_id = $1 AND ayah_number = $2 AND lang = 'id' AND status = 'draft'`,
		surahID, ayahNumber).Scan(&beforeNoop))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM quran_editorial_revisions
WHERE resource_type = 'ayah' AND surah_id = $1 AND ayah_number = $2 AND lang = 'id'`,
		surahID, ayahNumber).Scan(&beforeNoopRevisions))

	stats, err = run(partial, false)
	require.NoError(t, err)
	assert.Zero(t, stats.Upserted)
	assert.Equal(t, 1, stats.Skipped)

	var (
		afterNoop          time.Time
		afterNoopRevisions int
	)

	require.NoError(t, pool.QueryRow(ctx, `
SELECT updated_at FROM quran_ayah_editorial
WHERE surah_id = $1 AND ayah_number = $2 AND lang = 'id' AND status = 'draft'`,
		surahID, ayahNumber).Scan(&afterNoop))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM quran_editorial_revisions
WHERE resource_type = 'ayah' AND surah_id = $1 AND ayah_number = $2 AND lang = 'id'`,
		surahID, ayahNumber).Scan(&afterNoopRevisions))
	assert.True(t, beforeNoop.Equal(afterNoop), "no-op must preserve the optimistic-lock token")
	assert.Equal(t, beforeNoopRevisions, afterNoopRevisions, "no-op must not append a revision")

	// An explicit empty FAQ still means clear. The same run publishes the cleared
	// draft only because Publish=true.
	clearFAQ := fmt.Sprintf(`[{"surah_id":%d,"ayah_number":%d,"lang":"id","faq":[]}]`,
		surahID, ayahNumber)
	stats, err = run(clearFAQ, true)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Upserted)
	assert.Equal(t, 1, stats.Published)

	var publicFAQLen int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT meta_title, jsonb_array_length(faq)
FROM quran_ayah_editorial_public
WHERE surah_id = $1 AND ayah_number = $2 AND lang = 'id'`,
		surahID, ayahNumber).Scan(&publicTitle, &publicFAQLen))
	assert.Equal(t, "Judul draft baru", publicTitle, "partial fields survive the FAQ-only import")
	assert.Zero(t, publicFAQLen)
	assertAyahImportRevisions(ctx, t, pool, surahID, ayahNumber, 5)

	// The publish phase preflights the complete batch. A needs_review sibling
	// rolls back the permitted sibling draft and both would-be revisions.
	rollbackBatch := fmt.Sprintf(`[
{"surah_id":%d,"ayah_number":%d,"lang":"ar","meta_title":"Permitted sibling","license_status":"permitted"},
{"surah_id":%d,"ayah_number":%d,"lang":"en","meta_title":"Blocked sibling","license_status":"needs_review"}
]`, surahID, ayahNumber, surahID, ayahNumber)
	_, err = run(rollbackBatch, true)
	require.ErrorIs(t, err, entity.ErrLicenseNotPermitted)

	var rolledBackRows, rolledBackRevisions int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM quran_ayah_editorial
WHERE surah_id = $1 AND ayah_number = $2 AND lang IN ('ar', 'en')`,
		surahID, ayahNumber).Scan(&rolledBackRows))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM quran_editorial_revisions
WHERE resource_type = 'ayah' AND surah_id = $1 AND ayah_number = $2 AND lang IN ('ar', 'en')`,
		surahID, ayahNumber).Scan(&rolledBackRevisions))
	assert.Zero(t, rolledBackRows)
	assert.Zero(t, rolledBackRevisions)
}

func assertQuranAyahPublicCount(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	surahID, ayahNumber, want int,
) {
	t.Helper()

	var count int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM quran_ayah_editorial_public
WHERE surah_id = $1 AND ayah_number = $2 AND lang = 'id'`,
		surahID, ayahNumber).Scan(&count))
	assert.Equal(t, want, count)
}

func assertAyahImportRevisions(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	surahID, ayahNumber, want int,
) {
	t.Helper()

	var total, imported int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*), count(*) FILTER (WHERE origin = 'import')
FROM quran_editorial_revisions
WHERE resource_type = 'ayah' AND surah_id = $1 AND ayah_number = $2 AND lang = 'id'`,
		surahID, ayahNumber).Scan(&total, &imported))
	assert.Equal(t, want, total)
	assert.Equal(t, total, imported, "every importer revision must record origin=import")
}
