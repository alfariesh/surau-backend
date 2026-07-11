package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	quranEditorialDrillDatabase = "surau_q1_migration_drill"
	quranEditorialDrillAck      = "destroy-surau-q1-migration-drill"
	quranEditorialPreVersion    = int64(20260711000005)
)

// TestQuranEditorialGrandfatherMigrationDrill executes Q-1's exact up/down SQL
// over populated pre-Q-1 tables. It is intentionally excluded from the shared
// SURAU_LIVE_PG suite: this destructive schema replay may run only in the
// dedicated, disposable migration-roundtrip database provisioned by CI.
//
// Safety is defense in depth: a separate env var, an explicit acknowledgement,
// an exact database name, the exact pre-Q-1 schema version, empty Quran tables,
// and a transaction that is always rolled back must all agree before fixtures
// or DDL are executed.
//
//nolint:maintidx,paralleltest // serial destructive migration drill on its own disposable database
func TestQuranEditorialGrandfatherMigrationDrill(t *testing.T) {
	databaseURL := os.Getenv("SURAU_Q1_MIGRATION_DRILL_PG")
	if databaseURL == "" {
		t.Skip("SURAU_Q1_MIGRATION_DRILL_PG not set")
	}

	require.Equal(t, quranEditorialDrillAck, os.Getenv("SURAU_Q1_MIGRATION_DRILL_ACK"),
		"refusing destructive migration drill without the dedicated acknowledgement")

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	assertQuranEditorialDrillTarget(ctx, t, pg)

	up, err := os.ReadFile("../../../migrations/20260711000006_quran_editorial_workflow.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260711000006_quran_editorial_workflow.down.sql")
	require.NoError(t, err)

	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	seedLegacyQuranEditorial(ctx, t, tx)

	legacySurah := quranEditorialJSON(ctx, t, tx, legacySurahEditorialSnapshotSQL)
	legacyAyah := quranEditorialJSON(ctx, t, tx, legacyAyahEditorialSnapshotSQL)
	legacyPublicSurah := quranEditorialJSON(ctx, t, tx, legacyPublicSurahSnapshotSQL)
	legacyPublicAyah := quranEditorialJSON(ctx, t, tx, legacyPublicAyahSnapshotSQL)

	_, err = tx.Exec(ctx, string(up))
	require.NoError(t, err)
	assertGrandfatheredQuranEditorial(
		ctx, t, tx,
		legacySurah, legacyAyah, legacyPublicSurah, legacyPublicAyah,
	)

	// A real draft cannot be represented by the old schema, so down must fail
	// closed instead of silently collapsing it. The savepoint keeps the drill's
	// outer transaction usable after PostgreSQL reports the expected error.
	_, err = tx.Exec(ctx, `SET LOCAL surau.quran_editorial_writer = 'quran-editorial-service'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO quran_surah_editorial (
    surah_id, lang, status, meta_title, license_status, metadata
) VALUES (114, 'ar', 'draft', 'Q-1 rollback refusal fixture', 'needs_review', '{}'::jsonb)`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "SAVEPOINT q1_down_refusal")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(down))
	assertQuranEditorialDownRefused(t, err)
	_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT q1_down_refusal")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
DELETE FROM quran_surah_editorial
WHERE surah_id = 114 AND lang = 'ar' AND status = 'draft'`)
	require.NoError(t, err)

	// Baseline-only populated data is reversible. Down restores the exact legacy
	// rows, then a second up proves the populated upgrade can be replayed.
	_, err = tx.Exec(ctx, string(down))
	require.NoError(t, err)
	assertQuranEditorialLegacyRestored(ctx, t, tx, legacySurah, legacyAyah)

	_, err = tx.Exec(ctx, string(up))
	require.NoError(t, err)
	assertGrandfatheredQuranEditorial(
		ctx, t, tx,
		legacySurah, legacyAyah, legacyPublicSurah, legacyPublicAyah,
	)

	require.NoError(t, tx.Rollback(ctx))
}

func assertQuranEditorialDrillTarget(ctx context.Context, t *testing.T, pg *postgres.Postgres) {
	t.Helper()

	var (
		databaseName string
		version      int64
		dirty        bool
		quranRows    int
		statusCols   int
	)

	err := pg.Pool.QueryRow(ctx, `
SELECT current_database(), version, dirty
FROM schema_migrations`).Scan(&databaseName, &version, &dirty)
	require.NoError(t, err)
	require.Equal(t, quranEditorialDrillDatabase, databaseName,
		"refusing migration drill outside its disposable database")
	require.Equal(t, quranEditorialPreVersion, version,
		"migration drill requires the schema immediately before Q-1")
	require.False(t, dirty, "refusing migration drill on a dirty schema")

	err = pg.Pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM quran_surahs)
  + (SELECT count(*) FROM quran_ayahs)
  + (SELECT count(*) FROM quran_surah_editorial)
  + (SELECT count(*) FROM quran_ayah_editorial)`).Scan(&quranRows)
	require.NoError(t, err)
	require.Zero(t, quranRows, "migration drill requires pristine Quran tables")

	err = pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema = 'public'
  AND table_name IN ('quran_surah_editorial', 'quran_ayah_editorial')
  AND column_name IN ('status', 'updated_by', 'published_at')`).Scan(&statusCols)
	require.NoError(t, err)
	require.Zero(t, statusCols, "migration drill target is already at or beyond Q-1")
}

func seedLegacyQuranEditorial(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()

	_, err := tx.Exec(ctx, `
INSERT INTO quran_surahs (
    surah_id, name_arabic, name_latin, name_translation,
    revelation_type, ayah_count, metadata
) VALUES (
    114, 'الناس', 'An-Nas Q-1 Drill', 'Manusia', 'makkiyah', 2,
    '{"fixture":"q1-grandfather"}'::jsonb
);

INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, text_qpc_hafs,
    text_imlaei_simple, search_text, metadata
) VALUES
    (114, 1, '114:1', 'قُلْ أَعُوذُ', 'قل أعوذ', 'قل اعوذ', '{"fixture":1}'::jsonb),
    (114, 2, '114:2', 'بِرَبِّ النَّاسِ', 'برب الناس', 'برب الناس', '{"fixture":2}'::jsonb);

INSERT INTO quran_surah_editorial (
    surah_id, lang, meta_title, meta_description, arti_nama,
    keutamaan_html, asbabun_nuzul_html, pokok_kandungan_html,
    author_name, reviewed_by, reviewed_at, license_status, metadata,
    created_at, updated_at, checksum
) VALUES
    (
        114, 'id', 'Judul API lama', 'Deskripsi API lama', 'Manusia',
        '<p>Keutamaan lama</p>', '<p>Asbab lama</p>', '<p>Pokok lama</p>',
        'Penulis Lama', 'Penyunting Lama', '2024-02-03 04:05:06.123456+00',
        'permitted', '{"nested":{"keep":true},"source":"legacy"}'::jsonb,
        '2024-01-01 01:02:03.123456+00', '2024-03-04 05:06:07.654321+00',
        'legacy-surah-checksum'
    ),
    (
        114, 'en', 'Legacy public-domain title', 'Must remain non-public', NULL,
        NULL, NULL, NULL, 'Legacy Author', NULL, NULL,
        'public_domain', '{"visibility":"not-permitted"}'::jsonb,
        '2024-01-02 01:02:03.123456+00', '2024-03-05 05:06:07.654321+00',
        'legacy-surah-public-domain-checksum'
    );

INSERT INTO quran_ayah_editorial (
    surah_id, ayah_number, ayah_key, lang, meta_title, meta_description,
    intisari_html, keutamaan_html, faq, tafsir_range, author_name,
    reviewed_by, reviewed_at, license_status, checksum, metadata,
    created_at, updated_at
) VALUES
    (
        114, 1, '114:1', 'id', 'Judul ayat API lama', 'Deskripsi ayat API lama',
        '<p>Intisari lama</p>', '<p>Keutamaan ayat lama</p>',
        '[{"question":"Pertanyaan lama?","answer_html":"<p>Jawaban lama</p>"}]'::jsonb,
        '1-2', 'Penulis Ayat Lama', 'Penyunting Ayat Lama',
        '2024-02-04 04:05:06.123456+00', 'permitted', 'legacy-ayah-checksum',
        '{"nested":{"keep":true},"source":"legacy"}'::jsonb,
        '2024-01-03 01:02:03.123456+00', '2024-03-06 05:06:07.654321+00'
    ),
    (
        114, 2, '114:2', 'en', 'Restricted legacy title', 'Must remain non-public',
        NULL, NULL, '[]'::jsonb, '2', 'Legacy Author', NULL, NULL,
        'restricted', 'legacy-ayah-restricted-checksum',
        '{"visibility":"restricted"}'::jsonb,
        '2024-01-04 01:02:03.123456+00', '2024-03-07 05:06:07.654321+00'
    )`)
	require.NoError(t, err)
}

func assertGrandfatheredQuranEditorial(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	legacySurah json.RawMessage,
	legacyAyah json.RawMessage,
	legacyPublicSurah json.RawMessage,
	legacyPublicAyah json.RawMessage,
) {
	t.Helper()

	assert.JSONEq(t, string(legacySurah), string(quranEditorialJSON(ctx, t, tx, grandfatheredSurahSnapshotSQL)),
		"surah content, checksum, created_at, and updated_at must be byte-for-byte equivalent")
	assert.JSONEq(t, string(legacyAyah), string(quranEditorialJSON(ctx, t, tx, grandfatheredAyahSnapshotSQL)),
		"ayah content, checksum, created_at, and updated_at must be byte-for-byte equivalent")

	assert.JSONEq(t, string(legacyPublicSurah), string(quranEditorialJSON(ctx, t, tx, publicSurahViewSnapshotSQL)),
		"surah public-read projection must retain its pre-Q-1 shape and values")
	assert.JSONEq(t, string(legacyPublicAyah), string(quranEditorialJSON(ctx, t, tx, publicAyahViewSnapshotSQL)),
		"ayah public-read projection must retain its pre-Q-1 shape and values")

	var (
		publishedRows     int
		matchingPublishAt int
		baselineRows      int
		matchingSnapshots int
		publicSurahRows   int
		publicAyahRows    int
	)

	err := tx.QueryRow(ctx, `
SELECT
    (SELECT count(*)
       FROM quran_surah_editorial
      WHERE status = 'published' AND published_at = updated_at)
  + (SELECT count(*)
       FROM quran_ayah_editorial
      WHERE status = 'published' AND published_at = updated_at),
    (SELECT count(*) FROM quran_surah_editorial WHERE status = 'published')
  + (SELECT count(*) FROM quran_ayah_editorial WHERE status = 'published'),
    (SELECT count(*)
       FROM quran_editorial_revisions
      WHERE version = 1
        AND status = 'published'
        AND origin = 'import'
        AND actor_id IS NULL
        AND is_migration_baseline),
    (SELECT count(*)
       FROM quran_editorial_revisions revision
       LEFT JOIN quran_surah_editorial surah
         ON revision.resource_type = 'surah'
        AND revision.surah_id = surah.surah_id
        AND revision.lang = surah.lang
        AND revision.ayah_number IS NULL
       LEFT JOIN quran_ayah_editorial ayah
         ON revision.resource_type = 'ayah'
        AND revision.surah_id = ayah.surah_id
        AND revision.ayah_number = ayah.ayah_number
        AND revision.lang = ayah.lang
      WHERE revision.is_migration_baseline
        AND revision.snapshot = COALESCE(to_jsonb(surah), to_jsonb(ayah))),
    (SELECT count(*) FROM quran_surah_editorial_public),
    (SELECT count(*) FROM quran_ayah_editorial_public)
`).Scan(
		&matchingPublishAt,
		&publishedRows,
		&baselineRows,
		&matchingSnapshots,
		&publicSurahRows,
		&publicAyahRows,
	)
	require.NoError(t, err)
	assert.Equal(t, 4, publishedRows, "every legacy row is grandfathered as published")
	assert.Equal(t, publishedRows, matchingPublishAt, "published_at must copy updated_at exactly")
	assert.Equal(t, publishedRows, baselineRows, "every legacy row gets one v1 import baseline")
	assert.Equal(t, baselineRows, matchingSnapshots, "baseline snapshots must be exactly restorable")
	assert.Equal(t, 1, publicSurahRows, "public_domain is not the exact permitted state")
	assert.Equal(t, 1, publicAyahRows, "restricted content must remain outside public reads")

	assertPublicQuranEditorialColumns(ctx, t, tx)
}

func assertPublicQuranEditorialColumns(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()

	var surahColumns, ayahColumns []string

	err := tx.QueryRow(ctx, `
SELECT array_agg(attname ORDER BY attnum)
FROM pg_attribute
WHERE attrelid = 'quran_surah_editorial_public'::regclass
  AND attnum > 0
  AND NOT attisdropped`).Scan(&surahColumns)
	require.NoError(t, err)
	err = tx.QueryRow(ctx, `
SELECT array_agg(attname ORDER BY attnum)
FROM pg_attribute
WHERE attrelid = 'quran_ayah_editorial_public'::regclass
  AND attnum > 0
  AND NOT attisdropped`).Scan(&ayahColumns)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"surah_id", "lang", "meta_title", "meta_description", "arti_nama",
		"keutamaan_html", "asbabun_nuzul_html", "pokok_kandungan_html",
		"author_name", "reviewed_by", "reviewed_at", "license_status",
		"metadata", "created_at", "updated_at", "checksum",
	}, surahColumns)
	assert.Equal(t, []string{
		"surah_id", "ayah_number", "ayah_key", "lang", "meta_title",
		"meta_description", "intisari_html", "keutamaan_html", "faq",
		"tafsir_range", "author_name", "reviewed_by", "reviewed_at",
		"license_status", "checksum", "metadata", "created_at", "updated_at",
	}, ayahColumns)

	for _, workflowColumn := range []string{"status", "updated_by", "published_at"} {
		assert.NotContains(t, surahColumns, workflowColumn)
		assert.NotContains(t, ayahColumns, workflowColumn)
	}
}

func assertQuranEditorialLegacyRestored(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	legacySurah json.RawMessage,
	legacyAyah json.RawMessage,
) {
	t.Helper()

	assert.JSONEq(t, string(legacySurah), string(quranEditorialJSON(ctx, t, tx, legacySurahEditorialSnapshotSQL)))
	assert.JSONEq(t, string(legacyAyah), string(quranEditorialJSON(ctx, t, tx, legacyAyahEditorialSnapshotSQL)))

	var workflowColumns int

	err := tx.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema = 'public'
  AND table_name IN ('quran_surah_editorial', 'quran_ayah_editorial')
  AND column_name IN ('status', 'updated_by', 'published_at')`).Scan(&workflowColumns)
	require.NoError(t, err)
	assert.Zero(t, workflowColumns)

	var revisionTable, publicViews *string

	err = tx.QueryRow(ctx, `
SELECT to_regclass('public.quran_editorial_revisions')::text,
       to_regclass('public.quran_surah_editorial_public')::text`).Scan(
		&revisionTable,
		&publicViews,
	)
	require.NoError(t, err)
	assert.Nil(t, revisionTable)
	assert.Nil(t, publicViews)
}

func assertQuranEditorialDownRefused(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr))
	assert.Equal(t, "55000", pgErr.Code)
	assert.Contains(t, pgErr.Message, "draft Quran editorial rows exist")
}

func quranEditorialJSON(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	query string,
) json.RawMessage {
	t.Helper()

	var raw json.RawMessage

	err := tx.QueryRow(ctx, query).Scan(&raw)
	require.NoError(t, err)

	return raw
}

const legacySurahEditorialSnapshotSQL = `
SELECT COALESCE(jsonb_agg(to_jsonb(editorial) ORDER BY surah_id, lang), '[]'::jsonb)
FROM quran_surah_editorial editorial`

const grandfatheredSurahSnapshotSQL = `
SELECT COALESCE(jsonb_agg(
    to_jsonb(editorial) - ARRAY['status', 'updated_by', 'published_at']::text[]
    ORDER BY surah_id, lang
), '[]'::jsonb)
FROM quran_surah_editorial editorial`

const legacyAyahEditorialSnapshotSQL = `
SELECT COALESCE(jsonb_agg(to_jsonb(editorial) ORDER BY surah_id, ayah_number, lang), '[]'::jsonb)
FROM quran_ayah_editorial editorial`

const grandfatheredAyahSnapshotSQL = `
SELECT COALESCE(jsonb_agg(
    to_jsonb(editorial) - ARRAY['status', 'updated_by', 'published_at']::text[]
    ORDER BY surah_id, ayah_number, lang
), '[]'::jsonb)
FROM quran_ayah_editorial editorial`

const legacyPublicSurahSnapshotSQL = `
SELECT COALESCE(jsonb_agg(to_jsonb(public_row) ORDER BY surah_id, lang), '[]'::jsonb)
FROM (
    SELECT surah_id, lang, meta_title, meta_description, arti_nama,
           keutamaan_html, asbabun_nuzul_html, pokok_kandungan_html,
           author_name, reviewed_by, reviewed_at, license_status, metadata,
           created_at, updated_at, checksum
    FROM quran_surah_editorial
    WHERE license_status = 'permitted'
) public_row`

const publicSurahViewSnapshotSQL = `
SELECT COALESCE(jsonb_agg(to_jsonb(public_row) ORDER BY surah_id, lang), '[]'::jsonb)
FROM quran_surah_editorial_public public_row`

const legacyPublicAyahSnapshotSQL = `
SELECT COALESCE(jsonb_agg(to_jsonb(public_row) ORDER BY surah_id, ayah_number, lang), '[]'::jsonb)
FROM (
    SELECT surah_id, ayah_number, ayah_key, lang, meta_title,
           meta_description, intisari_html, keutamaan_html, faq, tafsir_range,
           author_name, reviewed_by, reviewed_at, license_status, checksum,
           metadata, created_at, updated_at
    FROM quran_ayah_editorial
    WHERE license_status = 'permitted'
) public_row`

const publicAyahViewSnapshotSQL = `
SELECT COALESCE(jsonb_agg(to_jsonb(public_row) ORDER BY surah_id, ayah_number, lang), '[]'::jsonb)
FROM quran_ayah_editorial_public public_row`
