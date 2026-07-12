package persistent

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveQuranCitableUnitReconcileAndReader proves deterministic minting,
// linked footnotes, attributed public rendering, child Anchor resolution, and
// fail-closed primary drift against real PostgreSQL. It owns surah 1 only when
// that surah is absent, so a populated operator corpus is skipped unchanged.
//
//nolint:paralleltest,wsl_v5 // serial linear live-DB end-to-end fixture
func TestLiveQuranCitableUnitReconcileAndReader(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	var existing int
	require.NoError(t, pg.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM quran_surahs WHERE surah_id = 1`).Scan(&existing))
	if existing != 0 {
		t.Skip("Q-2 reconcile fixture requires unowned surah 1")
	}

	const (
		surahID                 = 1
		translationSourceID     = "q2-live-translation"
		transliterationSourceID = "q2-live-transliteration"
		actorID                 = "c2000000-0000-4000-8000-000000000001"
	)
	seedLiveUser(t, pg, actorID, "q2-license")
	var (
		originalScriptStatus          string
		originalScriptReason          sql.NullString
		originalScriptEvidence        sql.NullString
		originalScriptUpdatedBy       sql.NullString
		originalScriptUpdatedAt       time.Time
		originalScriptGrandfatheredAt sql.NullTime
		originalScriptGrandfatherHash sql.NullString
	)
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT license_status, license_reason, license_evidence_url,
       license_updated_by::text, license_updated_at, license_grandfathered_at,
       license_grandfathered_checksum
FROM quran_script_sources
WHERE id = 'qpc-hafs'`).Scan(
		&originalScriptStatus, &originalScriptReason, &originalScriptEvidence,
		&originalScriptUpdatedBy, &originalScriptUpdatedAt, &originalScriptGrandfatheredAt,
		&originalScriptGrandfatherHash,
	))
	t.Cleanup(func() {
		tx, txErr := pg.Pool.Begin(ctx)
		if txErr != nil {
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		cleanupExec := func(query string, args ...any) bool {
			_, cleanupErr := tx.Exec(ctx, query, args...)

			return assert.NoError(t, cleanupErr, "Q-2 live fixture cleanup")
		}
		if !cleanupExec(registryWriterGUC) ||
			!cleanupExec(`SET LOCAL session_replication_role = 'replica'`) ||
			!cleanupExec(`
DELETE FROM quran_source_license_audits
WHERE (source_kind = 'translation' AND source_id = $1)
	   OR (source_kind = 'transliteration' AND source_id = $3)
	   OR (source_kind = 'script' AND source_id = 'qpc-hafs' AND actor_id = $2::uuid)`,
				translationSourceID, actorID, transliterationSourceID) ||
			!cleanupExec(`
UPDATE quran_script_sources
SET license_status = $1,
    license_reason = $2,
    license_evidence_url = $3,
	    license_updated_by = $4::uuid,
	    license_updated_at = $5,
	    license_grandfathered_at = $6,
	    license_grandfathered_checksum = $7
WHERE id = 'qpc-hafs'`, originalScriptStatus, nullableSQLString(originalScriptReason),
				nullableSQLString(originalScriptEvidence), nullableSQLString(originalScriptUpdatedBy),
				originalScriptUpdatedAt, nullableSQLTime(originalScriptGrandfatheredAt),
				nullableSQLString(originalScriptGrandfatherHash)) ||
			!cleanupExec(`SET LOCAL session_replication_role = 'origin'`) ||
			!cleanupExec(`
DELETE FROM citable_units u
USING quran_citable_unit_bindings b
WHERE b.unit_id = u.id AND b.surah_id = $1`, surahID) ||
			!cleanupExec(`DELETE FROM quran_surahs WHERE surah_id = $1`, surahID) ||
			!cleanupExec(`DELETE FROM quran_translation_sources WHERE id = $1`, translationSourceID) ||
			!cleanupExec(`DELETE FROM quran_transliteration_sources WHERE id = $1`, transliterationSourceID) {
			return
		}
		assert.NoError(t, tx.Commit(ctx), "commit Q-2 live fixture cleanup")
	})

	_, err = pg.Pool.Exec(ctx,
		`INSERT INTO quran_surahs (surah_id, name_latin, ayah_count) VALUES (1, 'Al-Fatihah', 1)`)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, text_qpc_hafs, text_imlaei_simple,
    search_text, page_number, juz_number, hizb_number
) VALUES (1, 1, '1:1', 'بِسْمِ اللَّهِ', 'بسم الله', 'بسم الله', 1, 1, 1)`)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, metadata
) VALUES (1, 999, '1:999', '{}'::jsonb)`)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_translation_sources (id, lang, name, format, license_status)
VALUES ('q2-illegal-permitted-insert', 'id', 'Illegal permitted insert', 'json', 'permitted')`)
	require.Error(t, err, "new Quran source cannot bypass the audited license transition")
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_translation_sources (
    id, lang, name, responsible_name, responsible_role, format, license_status
) VALUES ($1, 'id', 'Q-2 live translation', 'Fixture publisher', 'publisher', 'json', 'needs_review')`,
		translationSourceID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayah_translations (
    source_id, surah_id, ayah_number, ayah_key, lang, text, footnotes, metadata
) VALUES ($1, 1, 1, '1:1', 'id',
	  'Dengan nama Allah',
	  '{"77123":"Catatan sumber"}',
	  jsonb_build_object('t', 'Dengan nama Allah<sup foot_note="77123">1</sup>'))`,
		translationSourceID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayah_translations (
    source_id, surah_id, ayah_number, ayah_key, lang, text
) VALUES ($1, 1, 999, '1:999', 'id', 'Legacy dependent without primary text')`,
		translationSourceID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_transliteration_sources (
    id, lang, name, responsible_name, responsible_role, format, license_status
) VALUES ($1, 'id', 'Q-2 live transliteration', 'Fixture publisher', 'publisher', 'json', 'needs_review')`,
		transliterationSourceID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayah_transliterations (
    source_id, surah_id, ayah_number, ayah_key, lang, text
	) VALUES ($1, 1, 1, '1:1', 'id', 'Bismillāh')`, transliterationSourceID)
	require.NoError(t, err)

	licenseRepo := NewEditorialRepo(pg)
	permitSource := func(kind, id string) entity.QuranSourceLicense {
		current, currentErr := licenseRepo.GetQuranSourceLicense(ctx, kind, id)
		require.NoError(t, currentErr)
		permitted, permitErr := licenseRepo.UpdateQuranSourceLicense(
			ctx, actorID, entity.QuranSourceLicenseUpdate{
				SourceKind: kind, SourceID: id, LicenseStatus: entity.LicenseStatusPermitted,
				Reason: "Q-2 live fixture permission",
			}, &current.UpdatedAt,
		)
		require.NoError(t, permitErr)

		return permitted
	}
	permitSource(entity.QuranSourceKindTranslation, translationSourceID)
	permitSource(entity.QuranSourceKindTransliteration, transliterationSourceID)
	permitSource(entity.QuranSourceKindScript, "qpc-hafs")

	registryRepo := NewCitableUnitRepo(pg)
	registry := unitregistry.New(registryRepo)
	first, err := registry.ReconcileQuranSurah(ctx, surahID)
	require.NoError(t, err)
	assert.Equal(t, 4, first.Minted)
	second, err := registry.ReconcileQuranSurah(ctx, surahID)
	require.NoError(t, err)
	assert.Equal(t, 4, second.Matched)
	assert.Zero(t, second.Minted)
	assert.Zero(t, second.Updated)
	audit, err := registryRepo.AuditCounts(ctx)
	require.NoError(t, err)
	assert.Zero(t, audit.Violations.QuranBinding)
	assert.Zero(t, audit.Violations.QuranInterpretive)
	var ownedStale bool
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT units_stale_at IS NOT NULL FROM quran_surahs WHERE surah_id = 1`).Scan(&ownedStale))
	assert.False(t, ownedStale)
	var incompleteUnits int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*) FROM quran_citable_unit_bindings
WHERE surah_id = 1 AND ayah_number = 999`).Scan(&incompleteUnits))
	assert.Zero(t, incompleteUnits, "an ayah without primary text cannot mint dependent units")

	quranRepo := NewQuranRepo(pg)
	ayah, err := quranRepo.GetAyah(ctx, "1:1", "id", translationSourceID, false, "")
	require.NoError(t, err)
	require.NotNil(t, ayah.PrimaryUnitID)
	require.NotNil(t, ayah.Translation)
	assert.Equal(t, "Fixture publisher", *ayah.Translation.ResponsibleName)
	assert.Equal(t, entity.LicenseStatusPermitted, ayah.Translation.LicenseStatus)
	require.NotNil(t, ayah.Translation.UnitID)
	require.Len(t, ayah.Translation.FootnoteUnits, 1)
	assert.Equal(t, *ayah.Translation.UnitID, ayah.Translation.FootnoteUnits[0].ParentUnitID)
	assert.Equal(t, "77123", ayah.Translation.FootnoteUnits[0].FootnoteKey)
	require.NotNil(t, ayah.Translation.FootnoteUnits[0].Marker)
	assert.Equal(t, "1", *ayah.Translation.FootnoteUnits[0].Marker)
	require.NotNil(t, ayah.Transliteration)
	require.NotNil(t, ayah.Transliteration.UnitID)

	anchorRepo := NewAnchorRepo(pg)
	logical, err := anchorRepo.ResolveQuran(ctx, "1:1")
	require.NoError(t, err)
	require.Len(t, logical.ActiveRecords, 1)
	assert.Equal(t, ayah.PrimaryUnitID, logical.ActiveRecords[0].PrimaryUnitID)

	translationAnchor := *ayah.Translation.Anchor
	resolved, err := anchorRepo.ResolveCanonicalUnit(ctx, translationAnchor)
	require.NoError(t, err)
	require.Len(t, resolved.ActiveRecords, 1)
	assert.Equal(t, *ayah.Translation.UnitID, *resolved.ActiveRecords[0].UnitID)

	_, err = pg.Pool.Exec(ctx, `
UPDATE quran_ayahs SET page_number = 2
WHERE surah_id = 1 AND ayah_number = 1`)
	require.NoError(t, err)
	pageChanged, err := registry.ReconcileQuranSurah(ctx, surahID)
	require.NoError(t, err)
	assert.Zero(t, pageChanged.Minted, "a page correction must keep deterministic IDs")
	assert.Equal(t, 4, pageChanged.Updated)
	var wrongPageUnits int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM quran_citable_unit_bindings b
JOIN citable_units u ON u.id = b.unit_id AND u.lifecycle = 'active'
WHERE b.surah_id = 1 AND b.ayah_number = 1 AND u.page_id IS DISTINCT FROM 2`).Scan(&wrongPageUnits))
	assert.Zero(t, wrongPageUnits)
	pageTwoStart, pageTwoEnd, err := anchorRepo.ResolveQuranLocator(ctx, "page", 2)
	require.NoError(t, err)
	require.Len(t, pageTwoStart.ActiveRecords, 1)
	require.Len(t, pageTwoEnd.ActiveRecords, 1)
	_, _, err = anchorRepo.ResolveQuranLocator(ctx, "page", 1)
	assert.ErrorIs(t, err, entity.ErrAnchorNotFound)

	loadedBeforeFootnote, err := registryRepo.LoadQuranSource(ctx, surahID)
	require.NoError(t, err)
	snapshotBeforeFootnote, err := registryRepo.QuranSnapshot(ctx, surahID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
UPDATE quran_ayah_translations
SET footnotes = '[{"n":77123,"marker":"1","t":"Catatan sumber yang diperbarui"}]'::jsonb
WHERE source_id = $1 AND surah_id = 1 AND ayah_number = 1`, translationSourceID)
	require.NoError(t, err)
	err = registryRepo.ApplyQuranReconcile(ctx, &entity.QuranUnitReconcilePlan{
		SurahID: surahID, LoadedAt: loadedBeforeFootnote.LoadedAt,
		BasedOn: snapshotBeforeFootnote.Fingerprint, ExpectedActive: int64(len(snapshotBeforeFootnote.Active)),
	})
	assert.ErrorIs(t, err, entity.ErrUnitReconcileConflict,
		"an older source snapshot must not erase a newer stale marker")
	var remainsStale bool
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT units_stale_at IS NOT NULL FROM quran_surahs WHERE surah_id = 1`).Scan(&remainsStale))
	assert.True(t, remainsStale)

	staleFootnote, err := quranRepo.GetAyah(ctx, "1:1", "id", translationSourceID, false, "")
	require.NoError(t, err)
	require.NotNil(t, staleFootnote.Translation)
	assert.Nil(t, staleFootnote.Translation.UnitID)
	assert.Empty(t, staleFootnote.Translation.FootnoteUnits,
		"stale linked footnotes must disappear even when source updated_at was not touched")
	_, err = anchorRepo.ResolveCanonicalUnit(ctx, translationAnchor)
	assert.ErrorIs(t, err, entity.ErrAnchorNotFound)

	footnoteChanged, err := registry.ReconcileQuranSurah(ctx, surahID)
	require.NoError(t, err)
	assert.Equal(t, 1, footnoteChanged.Superseded)
	currentFootnote, err := quranRepo.GetAyah(ctx, "1:1", "id", translationSourceID, false, "")
	require.NoError(t, err)
	require.NotNil(t, currentFootnote.Translation)
	assert.Equal(t, ayah.Translation.UnitID, currentFootnote.Translation.UnitID,
		"footnote editing must not remint its parent translation")
	require.Len(t, currentFootnote.Translation.FootnoteUnits, 1)
	assert.NotEqual(t, ayah.Translation.FootnoteUnits[0].UnitID,
		currentFootnote.Translation.FootnoteUnits[0].UnitID)

	_, err = pg.Pool.Exec(ctx, `
UPDATE quran_ayah_translations
SET text = 'Dengan menyebut nama Allah', updated_at = clock_timestamp()
WHERE source_id = $1 AND surah_id = 1 AND ayah_number = 1`, translationSourceID)
	require.NoError(t, err)
	stale, err := quranRepo.GetAyah(ctx, "1:1", "id", translationSourceID, false, "")
	require.NoError(t, err)
	require.NotNil(t, stale.Translation)
	assert.Nil(t, stale.Translation.UnitID,
		"stale rendering may use the licensed legacy fallback but must not claim a current Citable Unit")

	changed, err := registry.ReconcileQuranSurah(ctx, surahID)
	require.NoError(t, err)
	assert.Equal(t, 1, changed.Superseded)
	current, err := quranRepo.GetAyah(ctx, "1:1", "id", translationSourceID, false, "")
	require.NoError(t, err)
	require.NotNil(t, current.Translation)
	assert.NotEqual(t, *ayah.Translation.UnitID, *current.Translation.UnitID)
	old, err := anchorRepo.ResolveCanonicalUnit(ctx, translationAnchor)
	require.NoError(t, err)
	require.Len(t, old.ActiveRecords, 1)
	assert.Equal(t, *current.Translation.UnitID, *old.ActiveRecords[0].UnitID)

	license, err := licenseRepo.GetQuranSourceLicense(
		ctx, entity.QuranSourceKindTranslation, translationSourceID,
	)
	require.NoError(t, err)
	restricted, err := licenseRepo.UpdateQuranSourceLicense(
		ctx,
		actorID,
		entity.QuranSourceLicenseUpdate{
			SourceKind:    entity.QuranSourceKindTranslation,
			SourceID:      translationSourceID,
			LicenseStatus: entity.LicenseStatusRestricted,
			Reason:        "Q-2 live takedown",
		},
		&license.UpdatedAt,
	)
	require.NoError(t, err)
	assert.Equal(t, entity.LicenseStatusRestricted, restricted.LicenseStatus)
	require.Len(t, restricted.History, 2)
	_, err = quranRepo.GetAyah(ctx, "1:1", "id", translationSourceID, false, "")
	assert.ErrorIs(t, err, entity.ErrQuranTranslationSourceNotFound)
	_, err = anchorRepo.ResolveCanonicalUnit(ctx, *current.Translation.Anchor)
	assert.ErrorIs(t, err, entity.ErrAnchorNotFound)

	scriptLicense, err := licenseRepo.GetQuranSourceLicense(
		ctx, entity.QuranSourceKindScript, "qpc-hafs",
	)
	require.NoError(t, err)
	scriptRestricted, err := licenseRepo.UpdateQuranSourceLicense(
		ctx,
		actorID,
		entity.QuranSourceLicenseUpdate{
			SourceKind:    entity.QuranSourceKindScript,
			SourceID:      "qpc-hafs",
			LicenseStatus: entity.LicenseStatusRestricted,
			Reason:        "Q-2 live script takedown",
		},
		&scriptLicense.UpdatedAt,
	)
	require.NoError(t, err)
	assert.Nil(t, scriptRestricted.GrandfatheredAt, "restricted permanently revokes grandfather")
	_, err = quranRepo.GetAyah(ctx, "1:1", "ar", "", false, "")
	assert.ErrorIs(t, err, entity.ErrQuranAyahNotFound)
	require.NotNil(t, ayah.PrimaryUnitAnchor)
	_, err = anchorRepo.ResolveCanonicalUnit(ctx, *ayah.PrimaryUnitAnchor)
	assert.ErrorIs(t, err, entity.ErrAnchorNotFound)

	needsReview, err := licenseRepo.UpdateQuranSourceLicense(
		ctx,
		actorID,
		entity.QuranSourceLicenseUpdate{
			SourceKind:    entity.QuranSourceKindScript,
			SourceID:      "qpc-hafs",
			LicenseStatus: entity.LicenseStatusNeedsReview,
			Reason:        "Q-2 live post-takedown review",
		},
		&scriptRestricted.UpdatedAt,
	)
	require.NoError(t, err)
	assert.Nil(t, needsReview.GrandfatheredAt, "grandfather must never revive after takedown")
	_, err = quranRepo.GetAyah(ctx, "1:1", "ar", "", false, "")
	assert.ErrorIs(t, err, entity.ErrQuranAyahNotFound)

	overrideTx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	_, err = overrideTx.Exec(ctx, registryWriterGUC)
	require.NoError(t, err)
	_, err = overrideTx.Exec(ctx, `
UPDATE citable_units SET license_status = 'permitted' WHERE id = $1`, *ayah.PrimaryUnitID)
	require.Error(t, err, "a Quran unit override must never defeat its source takedown")
	require.NoError(t, overrideTx.Rollback(ctx))

	permitSource(entity.QuranSourceKindScript, "qpc-hafs")
	_, err = pg.Pool.Exec(ctx, `
UPDATE quran_ayahs SET text_qpc_hafs = 'تغيير ممنوع', updated_at = clock_timestamp()
WHERE surah_id = 1 AND ayah_number = 1`)
	require.Error(t, err, "PostgreSQL must reject primary Quran text drift before it is published")
	var primaryText string
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT text_qpc_hafs FROM quran_ayahs WHERE surah_id = 1 AND ayah_number = 1`).Scan(&primaryText))
	assert.Equal(t, "بِسْمِ اللَّهِ", primaryText)
	unchanged, err := quranRepo.GetAyah(ctx, "1:1", "ar", "", false, "")
	require.NoError(t, err)
	require.NotNil(t, unchanged.TextQPCHafs)
	assert.Equal(t, primaryText, *unchanged.TextQPCHafs)
}

func nullableSQLString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}

	return value.String
}

func nullableSQLTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}

	return value.Time
}

// TestLiveQuranCitableUnitsNeverInterpretiveEligible is the exact Q-2 safety
// gate referenced by U-6. It proves eligibility is generated by PostgreSQL,
// cannot be overridden, and excludes Quran even from the indexed candidate
// predicate used by future interpretive retrieval.
//
//nolint:paralleltest // serial live-DB invariant test gated on SURAU_LIVE_PG
func TestLiveQuranCitableUnitsNeverInterpretiveEligible(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	require.NoError(t, func() error {
		_, execErr := tx.Exec(ctx, registryWriterGUC)

		return execErr
	}())

	unitID := uuid.NewString()
	text := "نص قرآني لا يفسر آليا"
	hash := unitregistry.ContentHash("primary_text", "", text)
	_, err = tx.Exec(ctx, `
INSERT INTO citable_units (
    id, corpus, kind, ordinal, position, anchor, text, text_normalized,
    normalization_version, content_hash, occurrence, language,
    provenance_class, lifecycle
) VALUES ($1, 'quran', 'primary_text', 1, 0, $2, $3, $3, 1, $4, 1, 'ar', 'source', 'active')`,
		unitID, "quran/114:999/u/1-"+unitID, text, hash)
	require.NoError(t, err)

	var eligible bool
	require.NoError(t, tx.QueryRow(ctx, `
SELECT interpretive_corpus_eligible FROM citable_units WHERE id = $1`, unitID).Scan(&eligible))
	assert.False(t, eligible)

	var generatedExpression string
	require.NoError(t, tx.QueryRow(ctx, `
SELECT pg_get_expr(d.adbin, d.adrelid)
FROM pg_attrdef d
JOIN pg_attribute a ON a.attrelid = d.adrelid AND a.attnum = d.adnum
WHERE d.adrelid = 'citable_units'::regclass
  AND a.attname = 'interpretive_corpus_eligible'`).Scan(&generatedExpression))
	assert.Contains(t, generatedExpression, "corpus")
	assert.Contains(t, generatedExpression, "quran")

	var (
		indexPredicate string
		indexValid     bool
	)
	require.NoError(t, tx.QueryRow(ctx, `
SELECT pg_get_expr(i.indpred, i.indrelid), i.indisvalid
FROM pg_index i
JOIN pg_class c ON c.oid = i.indexrelid
WHERE c.relname = 'idx_citable_units_interpretive_active'`).Scan(&indexPredicate, &indexValid))
	assert.True(t, indexValid)
	assert.Contains(t, indexPredicate, "interpretive_corpus_eligible")
	assert.Contains(t, indexPredicate, "lifecycle")

	_, err = tx.Exec(ctx, `SAVEPOINT generated_override`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE citable_units SET interpretive_corpus_eligible = TRUE WHERE id = $1`, unitID)
	assert.Error(t, err, "generated eligibility must reject application overrides")
	_, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT generated_override`)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `SAVEPOINT corpus_override`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE citable_units SET corpus = 'wiki' WHERE id = $1`, unitID)
	assert.Error(t, err, "Quran corpus identity must be immutable")
	_, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT corpus_override`)
	require.NoError(t, err)

	controlID := uuid.NewString()
	controlText := "non-Quran interpretive control"
	controlHash := unitregistry.ContentHash("paragraph", "", controlText)
	_, err = tx.Exec(ctx, `
INSERT INTO citable_units (
    id, corpus, kind, ordinal, position, anchor, text, text_normalized,
    normalization_version, content_hash, occurrence, language,
    provenance_class, lifecycle
) VALUES ($1, 'wiki', 'paragraph', 1, 0, $2, $3, $3, 1, $4, 1, 'id', 'source', 'active')`,
		controlID, "wiki/q2-control/"+controlID, controlText, controlHash)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(ctx, `
SELECT interpretive_corpus_eligible FROM citable_units WHERE id = $1`, controlID).Scan(&eligible))
	assert.True(t, eligible, "the construction must exclude Quran specifically, not disable retrieval globally")

	require.NoError(t, tx.Rollback(ctx))

	var leaked int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM citable_units
WHERE lifecycle = 'active'
  AND interpretive_corpus_eligible
  AND corpus = 'quran'`).Scan(&leaked))
	assert.Zero(t, leaked)
}
