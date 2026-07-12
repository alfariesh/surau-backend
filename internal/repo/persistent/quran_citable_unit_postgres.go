package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
)

const quranRegistryAdvisoryLockClass = 71002

var errInvalidQuranUnitSourceRelationship = errors.New("invalid Quran Citable Unit source relationship")

// LoadQuranSource loads all Citable Unit inputs for one surah from the existing
// Quran tables. It intentionally includes non-public sources: license is a
// dynamic read gate, not an identity/derivation filter.
//
//nolint:funlen,gocognit,gocyclo,cyclop,wsl_v5 // three ordered result sets hydrate one deterministic source aggregate with explicit guards
func (r *CitableUnitRepo) LoadQuranSource(
	ctx context.Context,
	surahID int,
) (entity.QuranUnitSource, error) {
	source := entity.QuranUnitSource{SurahID: surahID}
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if err := tx.QueryRow(ctx, `
SELECT now()
FROM quran_surahs
WHERE surah_id = $1`, surahID).Scan(&source.LoadedAt); errors.Is(err, pgx.ErrNoRows) {
		return source, entity.ErrQuranSurahNotFound
	} else if err != nil {
		return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource surah: %w", err)
	}

	rows, err := tx.Query(ctx, `
SELECT ayah_number, page_number, COALESCE(text_qpc_hafs, ''), updated_at
FROM quran_ayahs
WHERE surah_id = $1
ORDER BY ayah_number`, surahID)
	if err != nil {
		return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource ayahs: %w", err)
	}

	ayahIndexByNumber := make(map[int]int)
	for rows.Next() {
		var ayah entity.QuranUnitSourceAyah
		if err := rows.Scan(&ayah.AyahNumber, &ayah.PageNumber, &ayah.PrimaryText,
			&ayah.PrimaryUpdatedAt); err != nil {
			rows.Close()

			return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource scan ayah: %w", err)
		}
		source.Ayahs = append(source.Ayahs, ayah)
		ayahIndexByNumber[ayah.AyahNumber] = len(source.Ayahs) - 1
	}
	if err := rows.Err(); err != nil {
		rows.Close()

		return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource ayah rows: %w", err)
	}
	rows.Close()

	translationRows, err := tx.Query(ctx, `
SELECT t.ayah_number, t.source_id, t.lang, t.text, t.footnotes, t.metadata, t.updated_at
FROM quran_ayah_translations t
WHERE t.surah_id = $1
ORDER BY t.ayah_number, t.lang, t.source_id`, surahID)
	if err != nil {
		return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource translations: %w", err)
	}
	for translationRows.Next() {
		var (
			ayahNumber  int
			translation entity.QuranUnitSourceTranslation
		)
		if err := translationRows.Scan(&ayahNumber, &translation.SourceID, &translation.Language,
			&translation.Text, &translation.Footnotes, &translation.Metadata,
			&translation.UpdatedAt); err != nil {
			translationRows.Close()

			return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource scan translation: %w", err)
		}
		ayahIndex, exists := ayahIndexByNumber[ayahNumber]
		if !exists {
			translationRows.Close()

			return source, fmt.Errorf("%w: translation points to missing Quran ayah %d:%d",
				errInvalidQuranUnitSourceRelationship, surahID, ayahNumber)
		}
		source.Ayahs[ayahIndex].Translations = append(source.Ayahs[ayahIndex].Translations, translation)
	}
	if err := translationRows.Err(); err != nil {
		translationRows.Close()

		return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource translation rows: %w", err)
	}
	translationRows.Close()

	transliterationRows, err := tx.Query(ctx, `
SELECT t.ayah_number, t.source_id, t.lang, t.text, t.updated_at
FROM quran_ayah_transliterations t
WHERE t.surah_id = $1
ORDER BY t.ayah_number, t.lang, t.source_id`, surahID)
	if err != nil {
		return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource transliterations: %w", err)
	}
	defer transliterationRows.Close()

	for transliterationRows.Next() {
		var (
			ayahNumber      int
			transliteration entity.QuranUnitSourceTransliteration
		)
		if err := transliterationRows.Scan(&ayahNumber, &transliteration.SourceID,
			&transliteration.Language, &transliteration.Text, &transliteration.UpdatedAt); err != nil {
			return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource scan transliteration: %w", err)
		}
		ayahIndex, exists := ayahIndexByNumber[ayahNumber]
		if !exists {
			return source, fmt.Errorf("%w: transliteration points to missing Quran ayah %d:%d",
				errInvalidQuranUnitSourceRelationship, surahID, ayahNumber)
		}
		source.Ayahs[ayahIndex].Transliterations = append(
			source.Ayahs[ayahIndex].Transliterations, transliteration,
		)
	}
	if err := transliterationRows.Err(); err != nil {
		return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource transliteration rows: %w", err)
	}
	transliterationRows.Close()
	if err := tx.Commit(ctx); err != nil {
		return source, fmt.Errorf("CitableUnitRepo.LoadQuranSource commit: %w", err)
	}

	return source, nil
}

// QuranSnapshot returns one consistent registry snapshot for a surah.
//
//nolint:gocyclo,cyclop,funlen,wsl_v5 // repeatable-read snapshot uses two guarded scans plus one fingerprint
func (r *CitableUnitRepo) QuranSnapshot(
	ctx context.Context,
	surahID int,
) (entity.QuranUnitRegistrySnapshot, error) {
	snapshot := entity.QuranUnitRegistrySnapshot{
		MaxOrdinalByAyah: make(map[int]int),
		ExistingIDs:      make(map[string]struct{}),
	}

	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return snapshot, fmt.Errorf("CitableUnitRepo.QuranSnapshot begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	rows, err := tx.Query(ctx, `
SELECT u.id, u.page_id, u.kind, u.ordinal, u.position, u.parent_unit_id,
       u.marker, u.content_hash, u.occurrence, u.lifecycle,
       b.ayah_number, b.ordinal, b.role, b.translation_source_id,
       b.transliteration_source_id, b.footnote_key, b.source_updated_at
FROM citable_units u
JOIN quran_citable_unit_bindings b ON b.unit_id = u.id
WHERE b.surah_id = $1 AND u.lifecycle = 'active'
ORDER BY b.ayah_number, b.ordinal`, surahID)
	if err != nil {
		return snapshot, fmt.Errorf("CitableUnitRepo.QuranSnapshot active: %w", err)
	}
	for rows.Next() {
		record := entity.QuranCitableUnitRecord{
			Unit:    entity.CitableUnit{Corpus: entity.UnitCorpusQuran},
			Binding: entity.QuranCitableUnitBinding{SurahID: surahID},
		}
		if err := rows.Scan(
			&record.Unit.ID, &record.Unit.PageID, &record.Unit.Kind, &record.Unit.Ordinal,
			&record.Unit.Position, &record.Unit.ParentUnitID, &record.Unit.Marker,
			&record.Unit.ContentHash, &record.Unit.Occurrence, &record.Unit.Lifecycle,
			&record.Binding.AyahNumber, &record.Binding.Ordinal, &record.Binding.Role,
			&record.Binding.TranslationSourceID, &record.Binding.TransliterationSourceID,
			&record.Binding.FootnoteKey, &record.Binding.SourceUpdatedAt,
		); err != nil {
			rows.Close()

			return snapshot, fmt.Errorf("CitableUnitRepo.QuranSnapshot scan: %w", err)
		}
		record.Binding.UnitID = record.Unit.ID
		snapshot.Active = append(snapshot.Active, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()

		return snapshot, fmt.Errorf("CitableUnitRepo.QuranSnapshot active rows: %w", err)
	}
	rows.Close()

	ordinalRows, err := tx.Query(ctx, `
SELECT b.ayah_number, MAX(b.ordinal), array_agg(b.unit_id::text)
FROM quran_citable_unit_bindings b
WHERE b.surah_id = $1
GROUP BY b.ayah_number`, surahID)
	if err != nil {
		return snapshot, fmt.Errorf("CitableUnitRepo.QuranSnapshot ordinals: %w", err)
	}
	for ordinalRows.Next() {
		var (
			ayahNumber int
			maxOrdinal int
			ids        []string
		)
		if err := ordinalRows.Scan(&ayahNumber, &maxOrdinal, &ids); err != nil {
			ordinalRows.Close()

			return snapshot, fmt.Errorf("CitableUnitRepo.QuranSnapshot scan ordinals: %w", err)
		}
		snapshot.MaxOrdinalByAyah[ayahNumber] = maxOrdinal
		for _, id := range ids {
			snapshot.ExistingIDs[id] = struct{}{}
		}
	}
	if err := ordinalRows.Err(); err != nil {
		ordinalRows.Close()

		return snapshot, fmt.Errorf("CitableUnitRepo.QuranSnapshot ordinal rows: %w", err)
	}
	ordinalRows.Close()

	snapshot.Fingerprint, err = quranRegistryFingerprint(ctx, tx, surahID)
	if err != nil {
		return snapshot, err
	}

	return snapshot, nil
}

//nolint:wsl_v5 // aggregate declaration and its single guarded query are one compact fingerprint read
func quranRegistryFingerprint(
	ctx context.Context,
	queryer pgxQuerier,
	surahID int,
) (entity.QuranUnitRegistryFingerprint, error) {
	var fingerprint entity.QuranUnitRegistryFingerprint
	err := queryer.QueryRow(ctx, `
SELECT COUNT(*) FILTER (WHERE u.lifecycle = 'active'),
       COALESCE(MAX(b.ordinal), 0),
       COALESCE(MAX(GREATEST(u.updated_at, b.updated_at)), 'epoch'::timestamptz)
FROM quran_citable_unit_bindings b
JOIN citable_units u ON u.id = b.unit_id
WHERE b.surah_id = $1`, surahID).Scan(
		&fingerprint.ActiveCount, &fingerprint.MaxOrdinal, &fingerprint.MaxUpdatedAt,
	)
	if err != nil {
		return fingerprint, fmt.Errorf("CitableUnitRepo Quran fingerprint: %w", err)
	}

	return fingerprint, nil
}

// ApplyQuranReconcile atomically applies one surah plan through the B-1 writer
// guard and verifies the Quran-specific invariants before commit.
//
//nolint:gocognit,gocyclo,cyclop,funlen,wsl_v5 // one guarded transaction with ordered batches and invariant checks
func (r *CitableUnitRepo) ApplyQuranReconcile(
	ctx context.Context,
	plan *entity.QuranUnitReconcilePlan,
) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyQuranReconcile begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if _, err := tx.Exec(ctx, registryWriterGUC); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyQuranReconcile guard guc: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`,
		quranRegistryAdvisoryLockClass, plan.SurahID); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyQuranReconcile lock: %w", err)
	}

	var sourceCurrent bool
	err = tx.QueryRow(ctx, `
SELECT units_stale_at IS NULL OR units_stale_at <= $2
FROM quran_surahs
WHERE surah_id = $1
FOR UPDATE`, plan.SurahID, plan.LoadedAt).Scan(&sourceCurrent)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.ErrQuranSurahNotFound
	}
	if err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyQuranReconcile source lock: %w", err)
	}
	if !sourceCurrent {
		return entity.ErrUnitReconcileConflict
	}

	fingerprint, err := quranRegistryFingerprint(ctx, tx, plan.SurahID)
	if err != nil {
		return err
	}
	if fingerprint != plan.BasedOn {
		return entity.ErrUnitReconcileConflict
	}

	batch := &pgx.Batch{}
	expectOne := make([]bool, 0)
	queue := func(query string, args ...any) {
		batch.Queue(query, args...)
		expectOne = append(expectOne, true)
	}

	queueUnit := func(mint *entity.QuranUnitMint) error {
		detail, err := json.Marshal(mint.Unit.ProvenanceDetail)
		if err != nil {
			return fmt.Errorf("CitableUnitRepo.ApplyQuranReconcile provenance %s: %w", mint.Unit.ID, err)
		}
		unit := &mint.Unit
		queue(`
INSERT INTO citable_units (
    id, corpus, book_id, heading_id, page_id, kind, ordinal, position,
    parent_unit_id, anchor, marker, text, html, text_normalized,
    normalization_version, content_hash, occurrence, language,
    provenance_class, provenance_detail, generation_run_id, license_status, lifecycle
) VALUES ($1,$2,NULL,NULL,$3,$4,$5,$6,$7,$8,$9,$10,NULL,$11,$12,$13,$14,$15,$16,$17,NULL,NULL,'active')`,
			unit.ID, unit.Corpus, unit.PageID, unit.Kind, unit.Ordinal, unit.Position,
			unit.ParentUnitID, unit.Anchor, unit.Marker, unit.Text, unit.TextNormalized,
			unit.NormalizationVersion, unit.ContentHash, unit.Occurrence, unit.Language,
			unit.ProvenanceClass, detail)

		return nil
	}

	for i := range plan.Mints {
		if plan.Mints[i].Unit.ParentUnitID == nil {
			if err := queueUnit(&plan.Mints[i]); err != nil {
				return err
			}
		}
	}
	for i := range plan.Mints {
		if plan.Mints[i].Unit.ParentUnitID != nil {
			if err := queueUnit(&plan.Mints[i]); err != nil {
				return err
			}
		}
	}
	for i := range plan.Mints {
		binding := &plan.Mints[i].Binding
		queue(`
INSERT INTO quran_citable_unit_bindings (
    unit_id, surah_id, ayah_number, ordinal, role, translation_source_id,
    transliteration_source_id, footnote_key, source_updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			binding.UnitID, binding.SurahID, binding.AyahNumber, binding.Ordinal,
			binding.Role, binding.TranslationSourceID, binding.TransliterationSourceID,
			binding.FootnoteKey, binding.SourceUpdatedAt)
	}

	for i := range plan.Updates {
		update := &plan.Updates[i]
		queue(`
WITH changed AS (
    UPDATE citable_units
    SET page_id = $2, position = $3, parent_unit_id = $4, updated_at = now()
    WHERE id = $1 AND lifecycle = 'active'
    RETURNING id
)
UPDATE quran_citable_unit_bindings b
SET source_updated_at = $5, updated_at = now()
FROM changed
WHERE b.unit_id = changed.id`,
			update.ID, update.PageID, update.Position, update.ParentUnitID, update.SourceUpdatedAt)
	}
	for i := range plan.Retires {
		retire := &plan.Retires[i]
		queue(`
UPDATE citable_units
SET lifecycle = $2, retired_at = now(), updated_at = now()
WHERE id = $1 AND lifecycle = 'active'`, retire.ID, retire.Lifecycle)
	}
	for i := range plan.Edges {
		edge := &plan.Edges[i]
		queue(`
INSERT INTO citable_unit_lineage (predecessor_id, successor_id, reason)
VALUES ($1,$2,$3)`, edge.PredecessorID, edge.SuccessorID, edge.Reason)
	}
	queue(`
UPDATE quran_surahs
SET units_derived_at = $2, units_stale_at = NULL
WHERE surah_id = $1
  AND (units_stale_at IS NULL OR units_stale_at <= $2)`, plan.SurahID, plan.LoadedAt)

	results := tx.SendBatch(ctx, batch)
	for i := range expectOne {
		tag, err := results.Exec()
		if err != nil {
			_ = results.Close()

			return fmt.Errorf("CitableUnitRepo.ApplyQuranReconcile batch stmt %d: %w", i, err)
		}
		if expectOne[i] && tag.RowsAffected() != 1 {
			_ = results.Close()

			return fmt.Errorf("CitableUnitRepo.ApplyQuranReconcile stmt %d affected %d: %w",
				i, tag.RowsAffected(), entity.ErrUnitReconcileConflict)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyQuranReconcile batch close: %w", err)
	}

	if err := assertQuranRegistryInvariants(ctx, tx, plan); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyQuranReconcile commit: %w", err)
	}

	return nil
}

//nolint:funlen,wsl_v5 // four independent set-based invariants intentionally stay adjacent to the registry transaction
func assertQuranRegistryInvariants(
	ctx context.Context,
	tx pgx.Tx,
	plan *entity.QuranUnitReconcilePlan,
) error {
	var activeCount int64
	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM quran_citable_unit_bindings b
JOIN citable_units u ON u.id = b.unit_id
WHERE b.surah_id = $1 AND u.lifecycle = 'active'`, plan.SurahID).Scan(&activeCount); err != nil {
		return fmt.Errorf("CitableUnitRepo Quran invariant active count: %w", err)
	}
	if activeCount != plan.ExpectedActive {
		return fmt.Errorf("%w: Quran surah %d active=%d expected=%d",
			errRegistryInvariant, plan.SurahID, activeCount, plan.ExpectedActive)
	}

	var violations int64
	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM quran_citable_unit_bindings b
JOIN citable_units u ON u.id = b.unit_id
WHERE b.surah_id = $1
  AND (u.corpus <> 'quran' OR u.interpretive_corpus_eligible)`, plan.SurahID).Scan(&violations); err != nil {
		return fmt.Errorf("CitableUnitRepo Quran invariant binding/eligibility: %w", err)
	}
	if violations != 0 {
		return fmt.Errorf("%w: %d Quran units missing binding or interpretive-ineligible flag",
			errRegistryInvariant, violations)
	}

	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM quran_ayahs a
LEFT JOIN LATERAL (
    SELECT COUNT(*) AS n
    FROM quran_citable_unit_bindings b
    JOIN citable_units u ON u.id = b.unit_id AND u.lifecycle = 'active'
    WHERE b.surah_id = a.surah_id AND b.ayah_number = a.ayah_number
      AND b.role = 'primary_text'
) primary_units ON TRUE
WHERE a.surah_id = $1
  AND NULLIF(btrim(a.text_qpc_hafs), '') IS NOT NULL
  AND primary_units.n <> 1`, plan.SurahID).Scan(&violations); err != nil {
		return fmt.Errorf("CitableUnitRepo Quran invariant primary units: %w", err)
	}
	if violations != 0 {
		return fmt.Errorf("%w: %d Quran ayahs lack exactly one primary unit",
			errRegistryInvariant, violations)
	}

	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM quran_citable_unit_bindings f
JOIN citable_units fu ON fu.id = f.unit_id AND fu.lifecycle = 'active'
LEFT JOIN quran_citable_unit_bindings p ON p.unit_id = fu.parent_unit_id
LEFT JOIN citable_units pu ON pu.id = p.unit_id
WHERE f.surah_id = $1 AND f.role = 'footnote'
  AND (p.role IS DISTINCT FROM 'translation'
       OR p.surah_id IS DISTINCT FROM f.surah_id
       OR p.ayah_number IS DISTINCT FROM f.ayah_number
       OR p.translation_source_id IS DISTINCT FROM f.translation_source_id
       OR pu.lifecycle IS DISTINCT FROM 'active')`, plan.SurahID).Scan(&violations); err != nil {
		return fmt.Errorf("CitableUnitRepo Quran invariant footnote parents: %w", err)
	}
	if violations != 0 {
		return fmt.Errorf("%w: %d Quran footnotes have invalid parents", errRegistryInvariant, violations)
	}

	return nil
}

// ListQuranSurahsForReconcile returns deterministic chunks for initial
// backfill or stale-only re-derivation.
//
//nolint:wsl_v5 // one ordered scan builds the resumable surah cursor list
func (r *CitableUnitRepo) ListQuranSurahsForReconcile(
	ctx context.Context,
	staleOnly bool,
	limit int,
) ([]int, error) {
	if limit < 1 || limit > 114 {
		limit = 114
	}
	rows, err := r.Pool.Query(ctx, `
SELECT surah_id
FROM quran_surahs
WHERE NOT $1::boolean
   OR units_derived_at IS NULL
   OR units_stale_at IS NOT NULL
ORDER BY surah_id
LIMIT $2`, staleOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("CitableUnitRepo.ListQuranSurahsForReconcile: %w", err)
	}
	defer rows.Close()

	result := make([]int, 0, limit)

	for rows.Next() {
		var surahID int
		if err := rows.Scan(&surahID); err != nil {
			return nil, fmt.Errorf("CitableUnitRepo.ListQuranSurahsForReconcile scan: %w", err)
		}
		result = append(result, surahID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("CitableUnitRepo.ListQuranSurahsForReconcile rows: %w", err)
	}

	return result, nil
}
