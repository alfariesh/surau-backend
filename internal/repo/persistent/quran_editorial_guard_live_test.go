package persistent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveQuranEditorialWriterGuard is the database-level half of Q-1's
// single-write-path proof. It is intentionally serial because it owns one
// throwaway ayah in the shared live-test database.
//
//	SURAU_LIVE_PG=postgres://... go test -p 1 ./internal/repo/persistent -run TestLiveQuranEditorialWriterGuard -v
//
//nolint:paralleltest // serial live-DB invariant check over a fixed throwaway ayah
func TestLiveQuranEditorialWriterGuard(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()

	const (
		surahID   = 114
		ayahNo    = 990006
		ayahKey   = "114:990006"
		writerGUC = "quran-editorial-service"
	)

	// quran_surahs is a bounded canonical table, so remember whether this test
	// supplied its FK parent and remove it only in that case.
	var insertedSurahID int

	err = pg.Pool.QueryRow(ctx, `
INSERT INTO quran_surahs (surah_id, name_latin, ayah_count)
VALUES ($1, 'Q-1 Guard Fixture', 0)
ON CONFLICT (surah_id) DO NOTHING
RETURNING surah_id`, surahID).Scan(&insertedSurahID)
	if !errors.Is(err, pgx.ErrNoRows) {
		require.NoError(t, err)
	}

	// A prior interrupted run may have left this test-owned ayah. Deleting the
	// parent exercises the guard's nested-trigger cascade escape hatch.
	_, err = pg.Pool.Exec(ctx, `DELETE FROM quran_ayahs WHERE ayah_key = $1`, ayahKey)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key, metadata)
VALUES ($1, $2, $3, '{"q1_writer_guard_fixture":true}'::jsonb)`, surahID, ayahNo, ayahKey)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		// The ayah delete cascades through editorial + revision guards at nested
		// trigger depth, so no workflow marker is needed for these children.
		if _, cleanupErr := pg.Pool.Exec(cleanupCtx,
			`DELETE FROM quran_ayahs WHERE ayah_key = $1`, ayahKey); cleanupErr != nil {
			t.Logf("cleanup Q-1 ayah fixture: %v", cleanupErr)
		}

		if insertedSurahID != 0 {
			if _, cleanupErr := pg.Pool.Exec(cleanupCtx,
				`DELETE FROM quran_surahs WHERE surah_id = $1`, insertedSurahID); cleanupErr != nil {
				t.Logf("cleanup Q-1 surah fixture: %v", cleanupErr)
			}
		}
	})

	var surahLang string

	createSurahEditorial := true

	err = pg.Pool.QueryRow(ctx, `
SELECT candidate.lang
FROM (VALUES ('ar'), ('id'), ('en')) AS candidate(lang)
WHERE NOT EXISTS (
    SELECT 1
    FROM quran_surah_editorial editorial
    WHERE editorial.surah_id = $1
      AND editorial.lang = candidate.lang
      AND editorial.status = 'draft'
)
LIMIT 1`, surahID).Scan(&surahLang)
	if errors.Is(err, pgx.ErrNoRows) {
		createSurahEditorial = false
		err = pg.Pool.QueryRow(ctx, `
SELECT lang
FROM quran_surah_editorial
WHERE surah_id = $1 AND status = 'draft'
		ORDER BY lang
		LIMIT 1`, surahID).Scan(&surahLang)
	}

	require.NoError(t, err)

	revisionID := uuid.New()
	seedTx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)

	defer rollbackTx(ctx, seedTx)

	_, err = seedTx.Exec(ctx, `SET LOCAL surau.quran_editorial_writer = 'quran-editorial-service'`)
	require.NoError(t, err)

	if createSurahEditorial {
		_, err = seedTx.Exec(ctx, `
INSERT INTO quran_surah_editorial (
    surah_id, lang, status, meta_title, license_status, metadata
) VALUES ($1, $2, 'draft', 'Q-1 guard fixture', 'needs_review', '{"q1_writer_guard_fixture":true}'::jsonb)`,
			surahID, surahLang)
		require.NoError(t, err)
	}

	_, err = seedTx.Exec(ctx, `
INSERT INTO quran_ayah_editorial (
    surah_id, ayah_number, ayah_key, lang, status, meta_title, license_status, metadata
) VALUES (
    $1, $2, $3, 'en', 'draft', 'Q-1 guard fixture', 'needs_review',
    '{"q1_writer_guard_fixture":true}'::jsonb
)`, surahID, ayahNo, ayahKey)
	require.NoError(t, err)
	_, err = seedTx.Exec(ctx, `
INSERT INTO quran_editorial_revisions (
    id, resource_type, surah_id, ayah_number, lang, status, version,
    actor_id, origin, snapshot
) VALUES ($1, 'ayah', $2, $3, 'en', 'draft', 1, NULL, 'rest', '{}'::jsonb)`,
		revisionID, surahID, ayahNo)
	require.NoError(t, err)
	require.NoError(t, seedTx.Commit(ctx))

	t.Cleanup(func() {
		cleanupCtx := context.Background()

		cleanupTx, cleanupErr := pg.Pool.Begin(cleanupCtx)
		if cleanupErr == nil {
			_, cleanupErr = cleanupTx.Exec(cleanupCtx,
				`SET LOCAL surau.quran_editorial_writer = 'quran-editorial-service'`)
		}

		if cleanupErr == nil {
			_, cleanupErr = cleanupTx.Exec(cleanupCtx, `
DELETE FROM quran_surah_editorial
WHERE surah_id = $1 AND lang = $2 AND status = 'draft'
	  AND metadata ->> 'q1_writer_guard_fixture' = 'true'`, surahID, surahLang)
		}

		if cleanupErr == nil {
			cleanupErr = cleanupTx.Commit(cleanupCtx)
		} else if cleanupTx != nil {
			_ = cleanupTx.Rollback(cleanupCtx)
		}

		if cleanupErr != nil {
			t.Logf("cleanup Q-1 surah editorial fixture: %v", cleanupErr)
		}
	})

	assertDenied := func(name, query string, args ...any) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			_, execErr := pg.Pool.Exec(ctx, query, args...)
			assertQuranEditorialGuardDenied(t, execErr)
		})
	}

	// Every DML verb on every workflow-owned table is denied without the local
	// marker. INSERT cases use valid FK/scope data so 42501 is unambiguously the
	// workflow guard, not a later relational constraint.
	assertDenied("surah insert", `
INSERT INTO quran_surah_editorial (surah_id, lang, status, license_status)
VALUES ($1, $2, 'draft', 'needs_review')`, surahID, surahLang)
	assertDenied("surah update", `
UPDATE quran_surah_editorial SET meta_title = meta_title
WHERE surah_id = $1 AND lang = $2 AND status = 'draft'`, surahID, surahLang)
	assertDenied("surah delete", `
DELETE FROM quran_surah_editorial
WHERE surah_id = $1 AND lang = $2 AND status = 'draft'`, surahID, surahLang)

	assertDenied("ayah insert", `
INSERT INTO quran_ayah_editorial (
    surah_id, ayah_number, ayah_key, lang, status, license_status
) VALUES ($1, $2, $3, 'en', 'draft', 'needs_review')`, surahID, ayahNo, ayahKey)
	assertDenied("ayah update", `
UPDATE quran_ayah_editorial SET meta_title = meta_title
WHERE surah_id = $1 AND ayah_number = $2 AND lang = 'en' AND status = 'draft'`, surahID, ayahNo)
	assertDenied("ayah delete", `
DELETE FROM quran_ayah_editorial
WHERE surah_id = $1 AND ayah_number = $2 AND lang = 'en' AND status = 'draft'`, surahID, ayahNo)

	assertDenied("revision insert", `
INSERT INTO quran_editorial_revisions (
    id, resource_type, surah_id, ayah_number, lang, status, version, origin, snapshot
) VALUES ($1, 'ayah', $2, $3, 'en', 'draft', 2, 'rest', '{}'::jsonb)`,
		uuid.New(), surahID, ayahNo)
	assertDenied("revision update", `
UPDATE quran_editorial_revisions SET snapshot = snapshot WHERE id = $1`, revisionID)
	assertDenied("revision delete", `DELETE FROM quran_editorial_revisions WHERE id = $1`, revisionID)
	assertDenied("surah editorial truncate", `TRUNCATE TABLE quran_surah_editorial`)
	assertDenied("ayah editorial truncate", `TRUNCATE TABLE quran_ayah_editorial`)
	assertDenied("revision truncate", `TRUNCATE TABLE quran_editorial_revisions`)
	assertDenied("surah parent truncate cascade", `TRUNCATE TABLE quran_surahs CASCADE`)
	assertDenied("ayah parent truncate cascade", `TRUNCATE TABLE quran_ayahs CASCADE`)

	// Only the three editorial metadata fields on quran_surahs are guarded.
	assertDenied("quran_surahs editorial upsert", `
INSERT INTO quran_surahs (surah_id, ayah_count, slug)
VALUES ($1, 0, 'q1-guard-must-not-write')
ON CONFLICT (surah_id) DO UPDATE SET slug = EXCLUDED.slug`, surahID)

	for _, field := range []string{"slug", "chronological_order", "ruku_count"} {
		assertDenied("quran_surahs "+field, fmt.Sprintf(
			"UPDATE quran_surahs SET %s = %s WHERE surah_id = $1", field, field,
		), surahID)
	}

	tag, err := pg.Pool.Exec(ctx,
		`UPDATE quran_surahs SET name_latin = name_latin WHERE surah_id = $1`, surahID)
	require.NoError(t, err, "ordinary Quran fields remain outside the editorial workflow guard")
	assert.EqualValues(t, 1, tag.RowsAffected())

	// Hold one physical pooled connection while committing/rolling back SET
	// LOCAL. This proves the transaction marker cannot authorize a later request
	// that reuses exactly the same connection.
	conn, err := pg.Pool.Acquire(ctx)
	require.NoError(t, err)

	defer conn.Release()

	for _, scenario := range []struct {
		name   string
		finish func(pgx.Tx) error
	}{
		{name: "commit", finish: func(tx pgx.Tx) error { return tx.Commit(ctx) }},
		{name: "rollback", finish: func(tx pgx.Tx) error { return tx.Rollback(ctx) }},
	} {
		t.Run("SET LOCAL cleared after "+scenario.name, func(t *testing.T) {
			tx, beginErr := conn.Begin(ctx)
			require.NoError(t, beginErr)

			_, setErr := tx.Exec(ctx, `SET LOCAL surau.quran_editorial_writer = 'quran-editorial-service'`)
			require.NoError(t, setErr)

			var inside string
			require.NoError(t, tx.QueryRow(ctx,
				`SELECT current_setting('surau.quran_editorial_writer', true)`).Scan(&inside))
			assert.Equal(t, writerGUC, inside)
			require.NoError(t, scenario.finish(tx))

			var after string
			require.NoError(t, conn.QueryRow(ctx, `
SELECT COALESCE(current_setting('surau.quran_editorial_writer', true), '')`).Scan(&after))
			assert.NotEqual(t, writerGUC, after)

			_, execErr := conn.Exec(ctx, `
UPDATE quran_ayah_editorial SET meta_title = meta_title
WHERE surah_id = $1 AND ayah_number = $2 AND lang = 'en' AND status = 'draft'`, surahID, ayahNo)
			assertQuranEditorialGuardDenied(t, execErr)
		})
	}
}

func assertQuranEditorialGuardDenied(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected PostgreSQL error, got %T: %v", err, err)
	assert.Equal(t, "42501", pgErr.Code)
	assert.Contains(t, pgErr.Message, "only via the editorial workflow service")
}
