package persistent

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	quranCitableDrillDatabase   = "surau_q1_migration_drill"
	quranCitableDrillAck        = "destroy-surau-q2-migration-drill"
	quranCitablePreVersion      = int64(20260711000006)
	quranCitableDrillBookID     = 999201
	quranCitableDrillCategoryID = 999201
	quranCitableDrillAuthorID   = 999201
	quranCitableDrillActorID    = "00000000-0000-4000-8000-00000000a201"
)

// TestQuranCitableUnitMigrationDrill proves the populated Q-2 core migration
// preserves already-live B-1 units, B-3 references, and legacy Quran source
// rows across up -> down -> up. Concurrent indexes are covered by the shared
// migration-roundtrip job outside this transaction.
//
//nolint:paralleltest,wsl_v5 // destructive schema replay is serial and restricted to a disposable CI database
func TestQuranCitableUnitMigrationDrill(t *testing.T) {
	databaseURL := os.Getenv("SURAU_Q2_MIGRATION_DRILL_PG")
	if databaseURL == "" {
		t.Skip("SURAU_Q2_MIGRATION_DRILL_PG not set")
	}
	require.Equal(t, quranCitableDrillAck, os.Getenv("SURAU_Q2_MIGRATION_DRILL_ACK"),
		"refusing destructive Q-2 migration drill without acknowledgement")

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	assertQuranCitableDrillTarget(ctx, t, pg)

	up, err := os.ReadFile("../../../migrations/20260712000001_add_quran_citable_units.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260712000001_add_quran_citable_units.down.sql")
	require.NoError(t, err)

	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	seedLegacyQuranCitableDrill(ctx, t, tx)
	legacy := quranCitableDrillSnapshot(ctx, t, tx)

	prepareQuranCitableDrillIndexes(ctx, t, tx)
	_, err = tx.Exec(ctx, string(up))
	require.NoError(t, err)
	assertQuranCitableMigrationUp(ctx, t, tx, legacy)
	assertQuranLicenseMigrationGuards(ctx, t, tx, down)
	seedQuranUnitInsideDrill(ctx, t, tx)

	_, err = tx.Exec(ctx, string(down))
	require.NoError(t, err)
	assertQuranCitableMigrationDown(ctx, t, tx, legacy)

	prepareQuranCitableDrillIndexes(ctx, t, tx)
	_, err = tx.Exec(ctx, string(up))
	require.NoError(t, err)
	assertQuranCitableMigrationUp(ctx, t, tx, legacy)

	require.NoError(t, tx.Rollback(ctx))
}

func assertQuranCitableDrillTarget(
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
		q2Columns    int
	)
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT current_database(), version, dirty FROM schema_migrations`).Scan(
		&databaseName, &version, &dirty,
	))
	require.Equal(t, quranCitableDrillDatabase, databaseName,
		"refusing Q-2 migration drill outside its disposable database")
	require.Equal(t, quranCitablePreVersion, version,
		"Q-2 drill requires the schema immediately before Q-2")
	require.False(t, dirty)

	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM quran_surahs)
     + (SELECT count(*) FROM quran_ayahs)
     + (SELECT count(*) FROM citable_units)
     + (SELECT count(*) FROM cross_references)`).Scan(&quranRows))
	require.Zero(t, quranRows, "Q-2 drill requires pristine source/registry tables")
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema = 'public'
  AND ((table_name = 'citable_units' AND column_name = 'interpretive_corpus_eligible')
    OR (table_name = 'quran_surahs' AND column_name = 'units_derived_at'))`).Scan(&q2Columns))
	require.Zero(t, q2Columns, "Q-2 drill target is already upgraded")
}

func seedLegacyQuranCitableDrill(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()

	_, err := tx.Exec(ctx, `
INSERT INTO users (id, username, email, password_hash)
VALUES ($1, 'q2-drill-actor', 'q2-drill-actor@example.test', 'x')`, quranCitableDrillActorID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO quran_import_runs (
	    id, source_name, source_url, qul_resource_id, resource_type,
	    format, checksum, license_status, imported_at
	) VALUES
	    ('00000000-0000-4000-8000-000000000086', 'QPC exact drill',
	     'https://example.test/qpc', '86', 'script', 'json', 'qpc-exact-checksum',
	     'needs_review', '2026-07-12T00:00:00Z'),
	    ('ffffffff-ffff-4fff-8fff-ffffffffffff', 'Imlaei must not win',
	     'https://example.test/imlaei', '', 'script', 'json', 'imlaei-checksum',
	     'needs_review', '2026-07-12T00:00:00Z');

	INSERT INTO quran_surahs (
    surah_id, name_arabic, name_latin, name_translation,
    revelation_type, ayah_count, metadata
) VALUES (114, 'الناس', 'An-Nas Q-2 Drill', 'Manusia', 'makkiyah', 1, '{}'::jsonb);

INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, text_qpc_hafs, text_imlaei_simple,
    search_text, page_number, juz_number, hizb_number, metadata
) VALUES (
    114, 1, '114:1', 'قُلْ أَعُوذُ بِرَبِّ النَّاسِ', 'قل أعوذ برب الناس',
    'قل اعوذ برب الناس', 604, 30, 60, '{}'::jsonb
);

INSERT INTO quran_translation_sources (
    id, lang, name, translator, source_url, format, license_status, metadata
) VALUES (
    'q2-drill-translation', 'id', 'Q-2 Drill Translation', 'Drill Translator',
    'https://example.test/q2-translation', 'json', 'permitted', '{}'::jsonb
);
INSERT INTO quran_ayah_translations (
    source_id, surah_id, ayah_number, ayah_key, lang, text, footnotes, metadata
) VALUES (
    'q2-drill-translation', 114, 1, '114:1', 'id', 'Katakanlah aku berlindung',
    '[{"n":1,"t":"Catatan drill"}]'::jsonb, '{}'::jsonb
);

INSERT INTO quran_transliteration_sources (
    id, lang, name, source_url, format, license_status, metadata
) VALUES (
    'q2-drill-transliteration', 'id', 'Q-2 Drill Transliteration',
    'https://example.test/q2-transliteration', 'json', 'permitted', '{}'::jsonb
);
INSERT INTO quran_ayah_transliterations (
    source_id, surah_id, ayah_number, ayah_key, lang, text, metadata
) VALUES (
    'q2-drill-transliteration', 114, 1, '114:1', 'id',
    'Qul aūdzu birabbin-nās', '{}'::jsonb
);
		`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO categories (id, name, display_order)
VALUES ($1, 'Q-2 Drill Category', $1)`, quranCitableDrillCategoryID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO authors (id, name) VALUES ($1, 'Q-2 Drill Author')`, quranCitableDrillAuthorID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO books (
    id, name, category_id, author_id, has_content, is_deleted, license_status
) VALUES ($1, 'Q-2 Drill Book', $2, $3, TRUE, FALSE, 'unknown')`,
		quranCitableDrillBookID, quranCitableDrillCategoryID, quranCitableDrillAuthorID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO citable_units (
    id, corpus, book_id, kind, ordinal, position, anchor, text,
    text_normalized, normalization_version, content_hash, occurrence,
    language, provenance_class, lifecycle
) VALUES (
    '00000000-0000-4000-8000-00000000b201', 'kitab', $1, 'paragraph', 1, 0,
    'kitab/999201/h/0/u/1', 'B-1 legacy unit', 'b-1 legacy unit', 1,
    decode(md5('q2-drill-b1'), 'hex'), 1, 'ar', 'source', 'active'
)`, quranCitableDrillBookID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus,
    target_quran_surah_id, target_quran_from_ayah, target_quran_to_ayah,
    kind, method, method_detail, confidence, review_status,
    evidence_text, evidence_normalized, normalization_version,
    origin, origin_key, metadata
) VALUES (
    '00000000-0000-4000-8000-00000000b301',
    'quran/114', 'quran/114:1', 'quran', 'quran', 114, 1, 1,
    'cites', 'resolver', '{"strategy":"q2_migration_drill"}'::jsonb,
    1, 'approved', 'Q-2 parity evidence', 'q-2 parity evidence', 1,
    'resolver', 'q2-migration-drill', '{}'::jsonb
)
`)
	require.NoError(t, err)
}

func prepareQuranCitableDrillIndexes(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()

	_, err := tx.Exec(ctx, `
CREATE UNIQUE INDEX uq_citable_units_scope_ordinal_q2_kitab
    ON citable_units (corpus, book_id, heading_id, ordinal) NULLS NOT DISTINCT
    WHERE corpus = 'kitab';
CREATE UNIQUE INDEX uq_citable_units_active_content_q2_kitab
    ON citable_units (corpus, book_id, heading_id, kind, content_hash, occurrence)
    NULLS NOT DISTINCT
    WHERE lifecycle = 'active' AND corpus = 'kitab'`)
	require.NoError(t, err)
}

func assertQuranLicenseMigrationGuards(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	down []byte,
) {
	t.Helper()

	_, err := tx.Exec(ctx, `SAVEPOINT q2_license_guards`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO quran_translation_sources (id, lang, name, format, license_status)
VALUES ('q2-illegal-public-source', 'id', 'Illegal public source', 'json', 'permitted')`)
	require.Error(t, err, "new permitted source must be rejected before publication")

	_, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT q2_license_guards`)
	require.NoError(t, rollbackErr)

	_, err = tx.Exec(ctx, `SAVEPOINT q2_grandfather_guard`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE quran_script_sources
SET license_grandfathered_at = license_grandfathered_at + interval '1 second'
WHERE id = 'qpc-hafs'`)
	require.Error(t, err, "runtime must not forge or move the grandfather marker")

	_, rollbackErr = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT q2_grandfather_guard`)
	require.NoError(t, rollbackErr)

	_, err = tx.Exec(ctx, `SAVEPOINT q2_source_identity_guard`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE quran_translation_sources
SET qul_resource_id = 'replacement-resource'
WHERE id = 'q2-drill-translation'`)
	require.Error(t, err, "a reviewed source id must not be rebound to another resource")

	_, rollbackErr = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT q2_source_identity_guard`)
	require.NoError(t, rollbackErr)

	_, err = tx.Exec(ctx, `SAVEPOINT q2_source_release_guard`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE quran_translation_sources
SET checksum = 'replacement-release'
WHERE id = 'q2-drill-translation'`)
	require.Error(t, err, "a permitted source release must move to needs_review before replacement")

	_, rollbackErr = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT q2_source_release_guard`)
	require.NoError(t, rollbackErr)

	_, err = tx.Exec(ctx, `SAVEPOINT q2_down_guard`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE quran_script_sources
SET license_status = 'restricted', license_reason = 'drill takedown',
    license_updated_by = $1::uuid
WHERE id = 'qpc-hafs'`, quranCitableDrillActorID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(down))
	require.Error(t, err, "rollback must refuse to erase an audited Quran license decision")

	_, rollbackErr = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT q2_down_guard`)
	require.NoError(t, rollbackErr)
}

func seedQuranUnitInsideDrill(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()

	_, err := tx.Exec(ctx, `
INSERT INTO citable_units (
    id, corpus, kind, ordinal, position, anchor, text, text_normalized,
    normalization_version, content_hash, occurrence, language,
    provenance_class, lifecycle
) VALUES (
    '00000000-0000-4000-8000-00000000c201', 'quran', 'primary_text', 1, 0,
    'quran/114:1/u/1', 'قُلْ أَعُوذُ بِرَبِّ النَّاسِ', 'قل اعوذ برب الناس',
    1, decode(md5('q2-drill-quran'), 'hex'), 1, 'ar', 'source', 'active'
);
INSERT INTO quran_citable_unit_bindings (
    unit_id, surah_id, ayah_number, ordinal, role, source_updated_at
) SELECT '00000000-0000-4000-8000-00000000c201', 114, 1, 1,
         'primary_text', updated_at
    FROM quran_ayahs WHERE surah_id = 114 AND ayah_number = 1
`)
	require.NoError(t, err)

	var eligible bool
	require.NoError(t, tx.QueryRow(ctx, `
SELECT interpretive_corpus_eligible
FROM citable_units
WHERE id = '00000000-0000-4000-8000-00000000c201'`).Scan(&eligible))
	assert.False(t, eligible)
}

func assertQuranCitableMigrationUp(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	legacy json.RawMessage,
) {
	t.Helper()

	assert.JSONEq(t, string(legacy), string(quranCitableDrillSnapshot(ctx, t, tx)),
		"Q-2 up must not alter B-1/B-3 identities or legacy Quran source values")

	var (
		kitabEligible       bool
		qpcGrandfathered    bool
		qpcSourceName       string
		qpcChecksum         string
		qpcGrandfatherHash  string
		publicRenderSources int
		logicalVisible      bool
	)
	require.NoError(t, tx.QueryRow(ctx, `
SELECT interpretive_corpus_eligible
FROM citable_units
WHERE id = '00000000-0000-4000-8000-00000000b201'`).Scan(&kitabEligible))
	require.NoError(t, tx.QueryRow(ctx, `
SELECT license_grandfathered_at IS NOT NULL, name, checksum, license_grandfathered_checksum
FROM quran_script_sources WHERE id = 'qpc-hafs'`).Scan(
		&qpcGrandfathered, &qpcSourceName, &qpcChecksum, &qpcGrandfatherHash,
	))
	require.NoError(t, tx.QueryRow(ctx, `
SELECT (SELECT count(*) FROM public_quran_translation_sources WHERE id = 'q2-drill-translation')
     + (SELECT count(*) FROM public_quran_transliteration_sources WHERE id = 'q2-drill-transliteration')`).Scan(&publicRenderSources))
	require.NoError(t, tx.QueryRow(ctx, `
SELECT cross_reference_anchor_visible('quran/114:1')`).Scan(&logicalVisible))
	assert.True(t, kitabEligible)
	assert.True(t, qpcGrandfathered)
	assert.Equal(t, "QPC exact drill", qpcSourceName)
	assert.Equal(t, "qpc-exact-checksum", qpcChecksum)
	assert.Equal(t, qpcChecksum, qpcGrandfatherHash)
	assert.Equal(t, 2, publicRenderSources)
	assert.True(t, logicalVisible)
}

func assertQuranCitableMigrationDown(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	legacy json.RawMessage,
) {
	t.Helper()

	assert.JSONEq(t, string(legacy), string(quranCitableDrillSnapshot(ctx, t, tx)),
		"Q-2 down must restore populated B-1/B-3 and legacy Quran source rows")

	var (
		quranUnits     int
		bindingTable   *string
		eligibilityCol int
		logicalVisible bool
	)
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM citable_units WHERE corpus = 'quran'`).Scan(&quranUnits))
	require.NoError(t, tx.QueryRow(ctx, `SELECT to_regclass('public.quran_citable_unit_bindings')::text`).Scan(&bindingTable))
	require.NoError(t, tx.QueryRow(ctx, `
SELECT count(*) FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = 'citable_units'
  AND column_name = 'interpretive_corpus_eligible'`).Scan(&eligibilityCol))
	require.NoError(t, tx.QueryRow(ctx, `SELECT cross_reference_anchor_visible('quran/114:1')`).Scan(&logicalVisible))
	assert.Zero(t, quranUnits)
	assert.Nil(t, bindingTable)
	assert.Zero(t, eligibilityCol)
	assert.True(t, logicalVisible)
}

func quranCitableDrillSnapshot(ctx context.Context, t *testing.T, tx pgx.Tx) json.RawMessage {
	t.Helper()

	var value json.RawMessage
	require.NoError(t, tx.QueryRow(ctx, `
SELECT jsonb_build_object(
    'b1', (
        SELECT jsonb_build_object(
            'id', id, 'corpus', corpus, 'book_id', book_id, 'kind', kind,
            'ordinal', ordinal, 'anchor', anchor, 'text', text,
            'content_hash', encode(content_hash, 'hex'), 'lifecycle', lifecycle
        ) FROM citable_units WHERE id = '00000000-0000-4000-8000-00000000b201'
    ),
    'b3', (
        SELECT jsonb_build_object(
            'id', id, 'source_anchor', source_anchor, 'target_anchor', target_anchor,
            'target_quran_surah_id', target_quran_surah_id,
            'target_quran_from_ayah', target_quran_from_ayah,
            'target_quran_to_ayah', target_quran_to_ayah,
            'review_status', review_status, 'origin', origin, 'origin_key', origin_key
        ) FROM cross_references WHERE id = '00000000-0000-4000-8000-00000000b301'
    ),
    'translation', (
        SELECT jsonb_build_object(
            'id', id, 'lang', lang, 'name', name, 'translator', translator,
            'source_url', source_url, 'format', format, 'license_status', license_status,
            'metadata', metadata
        ) FROM quran_translation_sources WHERE id = 'q2-drill-translation'
    ),
    'transliteration', (
        SELECT jsonb_build_object(
            'id', id, 'lang', lang, 'name', name, 'source_url', source_url,
            'format', format, 'license_status', license_status, 'metadata', metadata
        ) FROM quran_transliteration_sources WHERE id = 'q2-drill-transliteration'
    )
)`).Scan(&value))

	return value
}
