package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	anchorgrammar "github.com/alfariesh/surau-backend/internal/anchor"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const crossReferenceWriterGUC = "SET LOCAL surau.cross_reference_writer = 'cross-reference-service'"

const crossReferenceReturning = `
id::text, source_anchor, target_anchor, source_corpus, target_corpus,
source_work_id, target_work_id, target_quran_surah_id,
	target_quran_from_ayah, target_quran_to_ayah, kind, method, method_detail,
	generation_run_id::text, confidence, review_status, evidence_text, evidence_normalized,
normalization_version, origin, origin_key, created_by::text,
reviewed_by::text, reviewed_at, metadata, created_at, updated_at`

const crossReferenceSelect = `
cr.id::text, cr.source_anchor, cr.target_anchor, cr.source_corpus, cr.target_corpus,
cr.source_work_id, cr.target_work_id, cr.target_quran_surah_id,
	cr.target_quran_from_ayah, cr.target_quran_to_ayah, cr.kind, cr.method, cr.method_detail,
	cr.generation_run_id::text, cr.confidence, cr.review_status, cr.evidence_text, cr.evidence_normalized,
cr.normalization_version, cr.origin, cr.origin_key, cr.created_by::text,
cr.reviewed_by::text, cr.reviewed_at, cr.metadata, cr.created_at, cr.updated_at`

const requestedAnchorCTE = `
WITH RECURSIVE requested(anchor) AS (VALUES (?::text)),
requested_unit_walk(id) AS (
    SELECT u.id
    FROM citable_units u
    JOIN requested r ON r.anchor = u.anchor
    UNION
    SELECT l.successor_id
    FROM requested_unit_walk w
    JOIN citable_unit_lineage l ON l.predecessor_id = w.id
),
requested_unit_ancestors(id) AS (
    SELECT u.id
    FROM citable_units u
    JOIN requested r ON r.anchor = u.anchor
    UNION
    SELECT l.predecessor_id
    FROM requested_unit_ancestors a
    JOIN citable_unit_lineage l ON l.successor_id = a.id
),
requested_anchors(anchor) AS (
    SELECT anchor FROM requested
    UNION
    SELECT u.anchor
    FROM requested_unit_walk w
    JOIN citable_units u ON u.id = w.id
    UNION
    SELECT u.anchor
    FROM requested_unit_ancestors a
    JOIN citable_units u ON u.id = a.id
)`

// CrossReferenceRepo persists the guarded B-3 registry and its legacy Quran
// compatibility projection.
type CrossReferenceRepo struct {
	*postgres.Postgres
}

// NewCrossReferenceRepo constructs the registry persistence adapter.
func NewCrossReferenceRepo(pg *postgres.Postgres) *CrossReferenceRepo {
	return &CrossReferenceRepo{Postgres: pg}
}

// Create inserts one human-authored pending edge after validating both Anchor
// endpoints inside the same transaction as the guarded write.
//
//nolint:gocritic // interface owns a value copy so persistence cannot mutate caller state
func (r *CrossReferenceRepo) Create(
	ctx context.Context,
	ref entity.CrossReference,
) (entity.CrossReference, error) {
	normalizeCrossReferenceTimes(&ref)

	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.Create begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if err := beginCrossReferenceWrite(ctx, tx); err != nil {
		return entity.CrossReference{}, err
	}

	if err := validateCrossReferenceAnchors(ctx, tx, &ref); err != nil {
		return entity.CrossReference{}, err
	}

	saved, err := insertCrossReference(ctx, tx, &ref, false)
	if err != nil {
		return entity.CrossReference{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.Create commit: %w", err)
	}

	return saved, nil
}

// UpsertDerived atomically writes the generic edge and, when bridge is nonnil,
// quran_book_references plus the typed bridge. Conflicts are idempotent by
// origin and never overwrite a review decision.
//
//nolint:cyclop,gocognit,gocritic,gocyclo,nestif,wsl_v5 // guarded triple-write stays visibly linear
func (r *CrossReferenceRepo) UpsertDerived(
	ctx context.Context,
	ref entity.CrossReference,
	bridge *entity.QuranCrossReferenceBridge,
) (entity.CrossReference, error) {
	normalizeCrossReferenceTimes(&ref)

	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.UpsertDerived begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if err := beginCrossReferenceWrite(ctx, tx); err != nil {
		return entity.CrossReference{}, err
	}

	if err := validateCrossReferenceAnchors(ctx, tx, &ref); err != nil {
		return entity.CrossReference{}, err
	}

	if bridge != nil {
		canonicalID, insertErr := insertLegacyQuranReference(ctx, tx, &ref, bridge)
		if insertErr != nil {
			return entity.CrossReference{}, insertErr
		}
		if canonicalID != ref.ID {
			ref.ID = canonicalID
			bridge.ID = canonicalID
			if ref.Origin == entity.CrossReferenceOriginLegacyQuran {
				ref.OriginKey = canonicalID
			}
		}
		if err := loadLegacyReviewState(ctx, tx, &ref, bridge); err != nil {
			return entity.CrossReference{}, err
		}
	}

	saved, err := insertCrossReference(ctx, tx, &ref, true)
	if err != nil {
		return entity.CrossReference{}, err
	}

	// A derived caller may retry the same stable (origin, origin_key) with a
	// freshly generated candidate UUID. Returning the canonical stored row is
	// the idempotent contract. A bridge must still keep the UUID shared with
	// quran_book_references, so a conflicting registry identity is rejected.
	if bridge != nil && saved.ID != ref.ID {
		return entity.CrossReference{}, entity.ErrCrossReferenceConflict
	}

	if bridge != nil {
		if err := insertQuranBridge(ctx, tx, bridge, ref.NormalizationVersion); err != nil {
			return entity.CrossReference{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.UpsertDerived commit: %w", err)
	}

	return saved, nil
}

// Get returns an editorial row without public visibility filtering.
func (r *CrossReferenceRepo) Get(ctx context.Context, id string) (entity.CrossReference, error) {
	row := r.Pool.QueryRow(ctx, "SELECT "+crossReferenceSelect+" FROM cross_references cr WHERE cr.id = $1", id)

	ref, err := scanCrossReference(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.CrossReference{}, entity.ErrCrossReferenceNotFound
	}

	if err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.Get: %w", err)
	}

	return ref, nil
}

// Review applies optimistic locking and mirrors the status into the legacy
// projection in the same transaction so the old endpoint remains byte-stable.
//
//nolint:cyclop,funlen,gocognit,gocyclo,wsl_v5 // branches preserve stale/not-found/ambiguous API errors
func (r *CrossReferenceRepo) Review(
	ctx context.Context,
	id, status, reviewerID string,
	expectedUpdatedAt *time.Time,
) (entity.CrossReference, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.Review begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if err := beginCrossReferenceWrite(ctx, tx); err != nil {
		return entity.CrossReference{}, err
	}

	query := `
UPDATE cross_references cr
SET review_status = $2,
    reviewed_by = CASE WHEN $2 = 'pending' THEN NULL ELSE $3::uuid END,
    reviewed_at = CASE WHEN $2 = 'pending' THEN NULL ELSE now() END,
    updated_at = now()
WHERE cr.id = $1`
	query += `
  AND NOT (
      $2 = 'approved'
      AND EXISTS (
          SELECT 1
          FROM quran_cross_reference_bridge b
          WHERE b.cross_reference_id = cr.id
            AND b.reference_kind = 'ambiguous'
      )
  )`
	args := []any{id, status, reviewerID}
	if expectedUpdatedAt != nil {
		query += " AND cr.updated_at = $4"
		args = append(args, *expectedUpdatedAt)
	}
	query += " RETURNING " + crossReferenceReturning

	saved, err := scanCrossReference(tx.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		var exists, ambiguousBridge bool
		if lookupErr := tx.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM cross_references WHERE id = $1),
       EXISTS (
           SELECT 1 FROM quran_cross_reference_bridge
           WHERE cross_reference_id = $1 AND reference_kind = 'ambiguous'
       )`, id).Scan(&exists, &ambiguousBridge); lookupErr != nil {
			return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.Review stale lookup: %w", lookupErr)
		}
		if exists && ambiguousBridge && status == entity.CrossReferenceStatusApproved {
			return entity.CrossReference{}, fmt.Errorf(
				"%w: ambiguous legacy reference cannot be approved in place",
				entity.ErrInvalidCrossReference,
			)
		}

		if exists && expectedUpdatedAt != nil {
			return entity.CrossReference{}, entity.ErrPreconditionFailed
		}

		return entity.CrossReference{}, entity.ErrCrossReferenceNotFound
	}

	if err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.Review update: %w", mapCrossReferenceWriteError(err))
	}

	if _, err := tx.Exec(ctx, `
UPDATE quran_book_references qbr
SET review_status = $2, updated_at = $3
WHERE qbr.id = $1
  AND EXISTS (
      SELECT 1 FROM quran_cross_reference_bridge b
      WHERE b.cross_reference_id = qbr.id
  )`, id, status, saved.UpdatedAt); err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.Review legacy mirror: %w", err)
	}

	if _, err := tx.Exec(ctx, `
UPDATE quran_cross_reference_bridge
SET updated_at = $2
WHERE cross_reference_id = $1`, id, saved.UpdatedAt); err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.Review bridge timestamp: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo.Review commit: %w", err)
	}

	return saved, nil
}

// List executes count/work-count and page reads in one repeatable-read
// snapshot. Counts are therefore correct even when Offset yields an empty page.
//
//nolint:cyclop,funlen,gocritic,gocyclo,wsl_v5 // snapshot count+page stages are intentionally explicit
func (r *CrossReferenceRepo) List(
	ctx context.Context,
	filter repo.CrossReferenceFilter,
) (entity.CrossReferenceList, error) {
	result := entity.CrossReferenceList{
		Items: make([]entity.CrossReference, 0, filter.Limit),
	}

	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, fmt.Errorf("CrossReferenceRepo.List begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	// PostgreSQL otherwise JIT-compiles the recursive lineage alternative based
	// on its worst-case estimate, even for a tiny OLTP page. Compilation alone
	// measured hundreds of milliseconds on PG18; these bounded reads are faster
	// and much more predictable without per-query JIT.
	if _, err := tx.Exec(ctx, `SET LOCAL jit = off`); err != nil {
		return result, fmt.Errorf("CrossReferenceRepo.List disable JIT: %w", err)
	}

	var parsed *anchorgrammar.Value
	if filter.Anchor != "" {
		value, parseErr := anchorgrammar.Parse(filter.Anchor)
		if parseErr != nil {
			return result, entity.ErrInvalidCrossReference
		}
		parsed = &value
	}

	workExpr := "NULL::text"
	switch filter.Direction {
	case entity.CrossReferenceDirectionIncoming:
		workExpr = crossReferenceWorkKey("source")
	case entity.CrossReferenceDirectionOutgoing:
		workExpr = crossReferenceWorkKey("target")
	}

	countBuilder := r.Builder.Select(
		"COUNT(*)",
		"COUNT(DISTINCT "+workExpr+")",
	).From("cross_references cr")
	countBuilder = applyCrossReferenceListFilter(countBuilder, filter, parsed)

	countSQL, countArgs, err := countBuilder.ToSql()
	if err != nil {
		return result, fmt.Errorf("CrossReferenceRepo.List count SQL: %w", err)
	}

	var total, workTotal int64
	if err := tx.QueryRow(ctx, countSQL, countArgs...).Scan(&total, &workTotal); err != nil {
		return result, fmt.Errorf("CrossReferenceRepo.List count: %w", err)
	}
	result.Total = int(total)
	result.WorkTotal = int(workTotal)

	dataBuilder := r.Builder.Select(crossReferenceSelect).
		From("cross_references cr").
		OrderBy("cr.created_at ASC", "cr.id ASC").
		Limit(filter.Limit).
		Offset(filter.Offset)
	dataBuilder = applyCrossReferenceListFilter(dataBuilder, filter, parsed)

	dataSQL, dataArgs, err := dataBuilder.ToSql()
	if err != nil {
		return result, fmt.Errorf("CrossReferenceRepo.List data SQL: %w", err)
	}

	rows, err := tx.Query(ctx, dataSQL, dataArgs...)
	if err != nil {
		return result, fmt.Errorf("CrossReferenceRepo.List data: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		item, scanErr := scanCrossReference(rows)
		if scanErr != nil {
			return result, fmt.Errorf("CrossReferenceRepo.List scan: %w", scanErr)
		}
		result.Items = append(result.Items, item)
	}

	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("CrossReferenceRepo.List rows: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("CrossReferenceRepo.List commit: %w", err)
	}

	return result, nil
}

// FreezeLegacyQuranWrites takes the singleton row FOR UPDATE before checking
// parity. The direct-write trigger takes FOR KEY SHARE, closing the race where
// a legacy insert could otherwise commit immediately after the count.
//
//nolint:cyclop,funlen,gocyclo // lock, parity gates, and state update form one auditable transaction
func (r *CrossReferenceRepo) FreezeLegacyQuranWrites(ctx context.Context) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("CrossReferenceRepo.FreezeLegacyQuranWrites begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if err := beginCrossReferenceWrite(ctx, tx); err != nil {
		return err
	}

	var alreadyFrozen bool
	if err := tx.QueryRow(ctx, `
SELECT quran_legacy_frozen
FROM cross_reference_registry_state
WHERE id = TRUE
FOR UPDATE`).Scan(&alreadyFrozen); err != nil {
		return fmt.Errorf("CrossReferenceRepo.FreezeLegacyQuranWrites lock: %w", err)
	}

	var missing int64
	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM quran_book_references qbr
LEFT JOIN quran_cross_reference_bridge b ON b.cross_reference_id = qbr.id
LEFT JOIN cross_references cr ON cr.id = b.cross_reference_id
WHERE qbr.review_status = 'approved'
  AND (b.cross_reference_id IS NULL OR cr.id IS NULL OR cr.review_status <> 'approved')`).Scan(&missing); err != nil {
		return fmt.Errorf("CrossReferenceRepo.FreezeLegacyQuranWrites parity: %w", err)
	}

	if missing != 0 {
		return fmt.Errorf("%w: %d approved legacy Quran references are not bridged", entity.ErrPreconditionFailed, missing)
	}

	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM quran_book_references qbr
LEFT JOIN quran_cross_reference_bridge b ON b.cross_reference_id = qbr.id
LEFT JOIN cross_references cr ON cr.id = b.cross_reference_id
WHERE qbr.surah_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM books b WHERE b.id = qbr.book_id AND b.is_deleted = FALSE)
  AND (
      qbr.reference_kind = 'surah'
      OR (qbr.from_ayah_number IS NOT NULL AND qbr.to_ayah_number IS NOT NULL)
  )
  AND (b.cross_reference_id IS NULL OR cr.id IS NULL)`).Scan(&missing); err != nil {
		return fmt.Errorf("CrossReferenceRepo.FreezeLegacyQuranWrites mappable parity: %w", err)
	}

	if missing != 0 {
		return fmt.Errorf("%w: %d mappable legacy Quran references are not bridged", entity.ErrPreconditionFailed, missing)
	}

	if !alreadyFrozen {
		if _, err := tx.Exec(ctx, `
UPDATE cross_reference_registry_state
SET quran_legacy_frozen = TRUE, updated_at = now()
WHERE id = TRUE`); err != nil {
			return fmt.Errorf("CrossReferenceRepo.FreezeLegacyQuranWrites update: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("CrossReferenceRepo.FreezeLegacyQuranWrites commit: %w", err)
	}

	return nil
}

// UnfreezeLegacyQuranWrites is the explicit reversible rollback control. It
// uses the same singleton lock as freeze so no concurrent state transition can
// interleave; the legacy trigger observes the committed false value.
func (r *CrossReferenceRepo) UnfreezeLegacyQuranWrites(ctx context.Context) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("CrossReferenceRepo.UnfreezeLegacyQuranWrites begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if err := beginCrossReferenceWrite(ctx, tx); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
SELECT quran_legacy_frozen
FROM cross_reference_registry_state
WHERE id = TRUE
FOR UPDATE`); err != nil {
		return fmt.Errorf("CrossReferenceRepo.UnfreezeLegacyQuranWrites lock: %w", err)
	}

	if _, err := tx.Exec(ctx, `
UPDATE cross_reference_registry_state
SET quran_legacy_frozen = FALSE, updated_at = now()
WHERE id = TRUE AND quran_legacy_frozen = TRUE`); err != nil {
		return fmt.Errorf("CrossReferenceRepo.UnfreezeLegacyQuranWrites update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("CrossReferenceRepo.UnfreezeLegacyQuranWrites commit: %w", err)
	}

	return nil
}

func beginCrossReferenceWrite(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, crossReferenceWriterGUC); err != nil {
		return fmt.Errorf("CrossReferenceRepo writer guard: %w", err)
	}

	return nil
}

//nolint:wsl_v5 // SQL construction and its positional arguments are intentionally adjacent
func insertCrossReference(
	ctx context.Context,
	tx pgx.Tx,
	ref *entity.CrossReference,
	idempotent bool,
) (entity.CrossReference, error) {
	detail, err := json.Marshal(ref.MethodDetail)
	if err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo method detail: %w", err)
	}

	query := `
INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus,
	    source_work_id, target_work_id, target_quran_surah_id,
	    target_quran_from_ayah, target_quran_to_ayah, kind, method, method_detail,
	    generation_run_id, confidence, review_status, evidence_text, evidence_normalized,
	    normalization_version, origin, origin_key, created_by, reviewed_by,
	    reviewed_at, metadata, created_at, updated_at
) VALUES (
	    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
	    $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27
)`
	if idempotent {
		// A no-op assignment gives RETURNING access to the canonical existing
		// row while deliberately preserving every review/audit field.
		query += ` ON CONFLICT (origin, origin_key) DO UPDATE
SET origin_key = cross_references.origin_key`
	}
	query += " RETURNING " + crossReferenceReturning

	row := tx.QueryRow(
		ctx, query,
		ref.ID, ref.SourceAnchor, ref.TargetAnchor, ref.SourceCorpus, ref.TargetCorpus,
		ref.SourceWorkID, ref.TargetWorkID, ref.TargetQuranSurahID,
		ref.TargetQuranFromAyah, ref.TargetQuranToAyah, ref.Kind, ref.Method, detail,
		ref.GenerationRunID, ref.Confidence, ref.ReviewStatus, ref.EvidenceText, ref.EvidenceNormalized,
		ref.NormalizationVersion, ref.Origin, ref.OriginKey, ref.CreatedBy, ref.ReviewedBy,
		ref.ReviewedAt, []byte(ref.Metadata), ref.CreatedAt, ref.UpdatedAt,
	)

	saved, err := scanCrossReference(row)
	if err != nil {
		return entity.CrossReference{}, fmt.Errorf("CrossReferenceRepo insert: %w", mapCrossReferenceWriteError(err))
	}

	return saved, nil
}

//nolint:wsl_v5 // legacy timestamps and canonical-ID arbitration are intentionally staged together
func insertLegacyQuranReference(
	ctx context.Context,
	tx pgx.Tx,
	ref *entity.CrossReference,
	bridge *entity.QuranCrossReferenceBridge,
) (string, error) {
	createdAt := bridge.CreatedAt
	if createdAt.IsZero() {
		createdAt = ref.CreatedAt
	}
	updatedAt := bridge.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = ref.UpdatedAt
	}

	var canonicalID string
	err := tx.QueryRow(
		ctx, `
INSERT INTO quran_book_references (
    id, book_id, page_id, heading_id, knowledge_mention_id, source_text,
    normalized_text, normalization_version, reference_kind, surah_id, from_ayah_number,
    to_ayah_number, from_ayah_key, to_ayah_key, match_strategy, confidence,
    review_status, metadata, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
    $15, $16, $17, $18, $19, $20
)
ON CONFLICT DO NOTHING
RETURNING id::text`,
		bridge.ID, bridge.BookID, bridge.PageID, bridge.HeadingID, bridge.KnowledgeMentionID,
		bridge.SourceText, bridge.NormalizedText, ref.NormalizationVersion, bridge.ReferenceKind, bridge.SurahID,
		bridge.FromAyahNumber, bridge.ToAyahNumber, bridge.FromAyahKey, bridge.ToAyahKey,
		bridge.MatchStrategy, ref.Confidence, ref.ReviewStatus, []byte(bridge.Metadata),
		createdAt, updatedAt,
	).Scan(&canonicalID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
SELECT id::text
FROM quran_book_references
WHERE id = $1
   OR ($2::uuid IS NOT NULL AND knowledge_mention_id = $2::uuid)
ORDER BY (id = $1) DESC
LIMIT 1
FOR UPDATE`, bridge.ID, bridge.KnowledgeMentionID).Scan(&canonicalID)
	}
	if err != nil {
		return "", fmt.Errorf("CrossReferenceRepo legacy Quran insert: %w", mapCrossReferenceWriteError(err))
	}

	return canonicalID, nil
}

//nolint:nestif // legacy NULL verification and atomic v1 upgrade stay next to the locked row load
func loadLegacyReviewState(
	ctx context.Context,
	tx pgx.Tx,
	ref *entity.CrossReference,
	bridge *entity.QuranCrossReferenceBridge,
) error {
	var (
		metadata             []byte
		knowledgeMentionID   *string
		normalizationVersion *int
	)

	err := tx.QueryRow(ctx, `
	SELECT book_id, page_id, heading_id, knowledge_mention_id::text,
	       source_text, normalized_text, normalization_version, reference_kind, surah_id,
       from_ayah_number, to_ayah_number, from_ayah_key, to_ayah_key,
       match_strategy, confidence, review_status, metadata, created_at, updated_at
FROM quran_book_references
WHERE id = $1`, bridge.ID).Scan(
		&bridge.BookID, &bridge.PageID, &bridge.HeadingID, &knowledgeMentionID,
		&bridge.SourceText, &bridge.NormalizedText, &normalizationVersion, &bridge.ReferenceKind, &bridge.SurahID,
		&bridge.FromAyahNumber, &bridge.ToAyahNumber, &bridge.FromAyahKey, &bridge.ToAyahKey,
		&bridge.MatchStrategy, &ref.Confidence, &ref.ReviewStatus, &metadata,
		&bridge.CreatedAt, &bridge.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("CrossReferenceRepo load legacy review state: %w", err)
	}

	if normalizationVersion == nil {
		expected := quranutil.NormalizeKeyV1(bridge.SourceText)
		if bridge.NormalizedText != expected {
			return fmt.Errorf(
				"%w: legacy Quran reference %s has unverified normalized text %q, expected search-key/v1 %q",
				entity.ErrInvalidCrossReference,
				bridge.ID,
				bridge.NormalizedText,
				expected,
			)
		}

		tag, updateErr := tx.Exec(ctx, `
UPDATE quran_book_references
SET normalization_version = $2
WHERE id = $1 AND normalization_version IS NULL`, bridge.ID, quranutil.SearchKeyV1ProfileVersion)
		if updateErr != nil {
			return fmt.Errorf("CrossReferenceRepo stamp verified legacy normalization: %w", updateErr)
		}

		if tag.RowsAffected() != 1 {
			return fmt.Errorf("CrossReferenceRepo stamp verified legacy normalization: %w", entity.ErrCrossReferenceConflict)
		}

		ref.NormalizationVersion = quranutil.SearchKeyV1ProfileVersion
	} else {
		ref.NormalizationVersion = *normalizationVersion
	}

	if err := normalizeLegacyAmbiguousReview(ctx, tx, ref, bridge); err != nil {
		return err
	}

	// The legacy row is authoritative until freeze. Reloading every compatibility
	// field also closes the old/new-writer race: the winning UUID and payload,
	// not the losing resolver candidate, become the bridge projection.
	bridge.KnowledgeMentionID = knowledgeMentionID
	bridge.Metadata = entity.RawJSON(metadata)
	ref.CreatedAt = bridge.CreatedAt
	ref.UpdatedAt = bridge.UpdatedAt
	ref.EvidenceText = bridge.SourceText
	ref.EvidenceNormalized = bridge.NormalizedText
	ref.MethodDetail.Strategy = bridge.MatchStrategy
	ref.Metadata = entity.RawJSON(metadata)

	return nil
}

func normalizeLegacyAmbiguousReview(
	ctx context.Context,
	tx pgx.Tx,
	ref *entity.CrossReference,
	bridge *entity.QuranCrossReferenceBridge,
) error {
	if bridge.ReferenceKind != "ambiguous" {
		return nil
	}

	if ref.ReviewStatus == entity.CrossReferenceStatusApproved {
		return fmt.Errorf(
			"%w: ambiguous legacy reference cannot remain approved",
			entity.ErrInvalidCrossReference,
		)
	}

	if ref.ReviewStatus == entity.CrossReferenceStatusAmbiguous {
		return nil
	}

	err := tx.QueryRow(ctx, `
UPDATE quran_book_references
SET review_status = 'ambiguous', updated_at = now()
WHERE id = $1
RETURNING review_status, updated_at`, bridge.ID).Scan(&ref.ReviewStatus, &bridge.UpdatedAt)
	if err != nil {
		return fmt.Errorf("CrossReferenceRepo normalize legacy ambiguous review: %w", err)
	}

	return nil
}

func insertQuranBridge(
	ctx context.Context,
	tx pgx.Tx,
	bridge *entity.QuranCrossReferenceBridge,
	normalizationVersion int,
) error {
	_, err := tx.Exec(
		ctx, `
INSERT INTO quran_cross_reference_bridge (
    cross_reference_id, book_id, page_id, heading_id, knowledge_mention_id,
    source_text, normalized_text, normalization_version, reference_kind, surah_id, from_ayah_number,
    to_ayah_number, from_ayah_key, to_ayah_key, match_strategy, metadata,
    created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
    $15, $16, $17, $18
)
ON CONFLICT (cross_reference_id) DO NOTHING`,
		bridge.ID, bridge.BookID, bridge.PageID, bridge.HeadingID, bridge.KnowledgeMentionID,
		bridge.SourceText, bridge.NormalizedText, normalizationVersion, bridge.ReferenceKind, bridge.SurahID,
		bridge.FromAyahNumber, bridge.ToAyahNumber, bridge.FromAyahKey, bridge.ToAyahKey,
		bridge.MatchStrategy, []byte(bridge.Metadata), bridge.CreatedAt, bridge.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("CrossReferenceRepo bridge insert: %w", mapCrossReferenceWriteError(err))
	}

	return nil
}

func scanCrossReference(row rowScanner) (entity.CrossReference, error) {
	var (
		ref          entity.CrossReference
		detailJSON   []byte
		metadataJSON []byte
		createdBy    *string
		reviewedBy   *string
	)

	err := row.Scan(
		&ref.ID, &ref.SourceAnchor, &ref.TargetAnchor, &ref.SourceCorpus, &ref.TargetCorpus,
		&ref.SourceWorkID, &ref.TargetWorkID, &ref.TargetQuranSurahID,
		&ref.TargetQuranFromAyah, &ref.TargetQuranToAyah, &ref.Kind, &ref.Method, &detailJSON,
		&ref.GenerationRunID, &ref.Confidence, &ref.ReviewStatus, &ref.EvidenceText, &ref.EvidenceNormalized,
		&ref.NormalizationVersion, &ref.Origin, &ref.OriginKey, &createdBy, &reviewedBy,
		&ref.ReviewedAt, &metadataJSON, &ref.CreatedAt, &ref.UpdatedAt,
	)
	if err != nil {
		return entity.CrossReference{}, err
	}

	if err := json.Unmarshal(detailJSON, &ref.MethodDetail); err != nil {
		return entity.CrossReference{}, fmt.Errorf("decode method_detail: %w", err)
	}

	if ref.Method == entity.CrossReferenceMethodMachine {
		ref.Generation = &entity.GenerationIdentity{
			RunID:         ref.MethodDetail.RunID,
			ModelID:       ref.MethodDetail.ModelID,
			PromptVersion: ref.MethodDetail.PromptVersion,
		}
	}

	ref.CreatedBy = createdBy
	ref.ReviewedBy = reviewedBy
	ref.Metadata = entity.RawJSON(metadataJSON)

	return ref, nil
}

func normalizeCrossReferenceTimes(ref *entity.CrossReference) {
	now := time.Now().UTC()

	if ref.CreatedAt.IsZero() {
		ref.CreatedAt = now
	}

	if ref.UpdatedAt.IsZero() {
		ref.UpdatedAt = ref.CreatedAt
	}
}

//nolint:cyclop,gocognit,gocritic,gocyclo,nestif,wsl_v5 // closed matrix remains auditable in one builder
func applyCrossReferenceListFilter(
	builder sq.SelectBuilder,
	filter repo.CrossReferenceFilter,
	parsed *anchorgrammar.Value,
) sq.SelectBuilder {
	if filter.Anchor != "" {
		lineageLookup := crossReferenceLineageLookup(parsed)
		if lineageLookup {
			builder = builder.Prefix(requestedAnchorCTE, filter.Anchor)
		}

		switch filter.Direction {
		case entity.CrossReferenceDirectionOutgoing:
			if lineageLookup {
				builder = builder.Where("cr.source_anchor IN (SELECT anchor FROM requested_anchors)")
			} else {
				builder = builder.Where(sq.Eq{"cr.source_anchor": filter.Anchor})
			}
		case entity.CrossReferenceDirectionIncoming:
			match := sq.Or{}
			if lineageLookup {
				match = append(match, sq.Expr("cr.target_anchor IN (SELECT anchor FROM requested_anchors)"))
			} else {
				match = append(match, sq.Eq{"cr.target_anchor": filter.Anchor})
			}

			if parsed != nil && !parsed.IsRange() &&
				parsed.Start().Kind() == anchorgrammar.PointKindQuranAyah {
				point := parsed.Start()
				match = append(match, sq.And{
					sq.Eq{"cr.target_quran_surah_id": point.Surah()},
					sq.Expr("cr.target_quran_from_ayah IS NOT NULL"),
					sq.Expr("cr.target_quran_to_ayah IS NOT NULL"),
					sq.Expr("int4range(cr.target_quran_from_ayah, cr.target_quran_to_ayah, '[]') @> ?::integer", point.Ayah()),
				})
			}
			builder = builder.Where(match)
		}
	}

	if filter.Kind != "" {
		builder = builder.Where(sq.Eq{"cr.kind": filter.Kind})
	}
	if filter.Method != "" {
		builder = builder.Where(sq.Eq{"cr.method": filter.Method})
	}

	if filter.PublicOnly {
		builder = applyPublicCrossReferenceVisibility(builder)
		builder = builder.Where(sq.Eq{"cr.review_status": entity.CrossReferenceStatusApproved})
		builder = builder.Where(publicCrossReferenceVisibilitySQL)
	} else if filter.ReviewStatus != "" {
		builder = builder.Where(sq.Eq{"cr.review_status": filter.ReviewStatus})
	}

	return builder
}

func applyPublicCrossReferenceVisibility(builder sq.SelectBuilder) sq.SelectBuilder {
	return builder.
		LeftJoin(`books cr_source_book
            ON cr.source_corpus = 'kitab'
           AND cr_source_book.id = cr.source_work_id
           AND cr_source_book.is_deleted = FALSE`).
		LeftJoin(`public_book_publications cr_source_publication
            ON cr_source_publication.book_id = cr_source_book.id
        `).
		LeftJoin(`books cr_target_book
            ON cr.target_corpus = 'kitab'
           AND cr_target_book.id = cr.target_work_id
           AND cr_target_book.is_deleted = FALSE`).
		LeftJoin(`public_book_publications cr_target_publication
            ON cr_target_publication.book_id = cr_target_book.id
        `).
		JoinClause(`JOIN LATERAL (
            SELECT CASE
                WHEN cr.source_corpus = 'kitab'
                 AND cr.source_anchor = 'kitab/' || cr.source_work_id::text THEN TRUE
                ELSE cross_reference_anchor_visible(cr.source_anchor)
            END AS visible
            OFFSET 0
        ) cr_source_anchor_visibility ON TRUE`).
		JoinClause(`JOIN LATERAL (
            SELECT CASE
                WHEN cr.target_corpus = 'quran' THEN TRUE
                WHEN cr.target_corpus = 'kitab'
                 AND cr.target_anchor = 'kitab/' || cr.target_work_id::text THEN TRUE
                ELSE cross_reference_anchor_visible(cr.target_anchor)
            END AS visible
            OFFSET 0
        ) cr_target_anchor_visibility ON TRUE`)
}

const publicCrossReferenceVisibilitySQL = `
(
    cr.source_corpus <> 'kitab'
    OR cr_source_publication.book_id IS NOT NULL
)
AND (
    cr.target_corpus <> 'kitab'
    OR cr_target_publication.book_id IS NOT NULL
)
AND cr_source_anchor_visibility.visible
AND cr_target_anchor_visibility.visible`

func crossReferenceLineageLookup(value *anchorgrammar.Value) bool {
	return value != nil && !value.IsRange() && value.Start().Kind() == anchorgrammar.PointKindKitabUnit
}

func crossReferenceWorkKey(side string) string {
	return fmt.Sprintf(`CASE
    WHEN cr.%[1]s_corpus = 'kitab' THEN 'kitab/' || cr.%[1]s_work_id::text
    WHEN cr.%[1]s_corpus = 'quran' THEN 'quran'
    ELSE cr.%[1]s_corpus || '/' || cr.%[1]s_anchor
END`, side)
}

func validateCrossReferenceAnchors(ctx context.Context, tx pgx.Tx, ref *entity.CrossReference) error {
	for _, raw := range []string{ref.SourceAnchor, ref.TargetAnchor} {
		value, err := anchorgrammar.Parse(raw)
		if err != nil {
			return entity.ErrInvalidCrossReference
		}

		if err := validateAnchorValueExists(ctx, tx, &value); err != nil {
			return err
		}
	}

	return nil
}

func validateAnchorValueExists(ctx context.Context, tx pgx.Tx, value *anchorgrammar.Value) error {
	if err := validateAnchorPointExists(ctx, tx, value.Start()); err != nil {
		return err
	}

	if end, ok := value.End(); ok {
		return validateAnchorPointExists(ctx, tx, end)
	}

	return nil
}

//nolint:cyclop,gocyclo,wsl_v5 // every active Anchor profile has a distinct indexed existence check
func validateAnchorPointExists(ctx context.Context, tx pgx.Tx, point anchorgrammar.Point) error {
	var exists bool

	switch point.Kind() {
	case anchorgrammar.PointKindQuranSurah:
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM quran_surahs WHERE surah_id = $1)", point.Surah()).Scan(&exists); err != nil {
			return fmt.Errorf("CrossReferenceRepo validate Quran surah: %w", err)
		}
	case anchorgrammar.PointKindQuranAyah:
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM quran_ayahs WHERE surah_id = $1 AND ayah_number = $2
)`, point.Surah(), point.Ayah()).Scan(&exists); err != nil {
			return fmt.Errorf("CrossReferenceRepo validate Quran ayah: %w", err)
		}
	case anchorgrammar.PointKindKitabWork:
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM books WHERE id = $1 AND is_deleted = FALSE)", point.BookID()).Scan(&exists); err != nil {
			return fmt.Errorf("CrossReferenceRepo validate Work: %w", err)
		}
	case anchorgrammar.PointKindKitabHeading:
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM book_headings h
    JOIN books b ON b.id = h.book_id AND b.is_deleted = FALSE
    WHERE h.book_id = $1 AND h.heading_id = $2 AND h.is_deleted = FALSE
)`, point.BookID(), point.HeadingID()).Scan(&exists); err != nil {
			return fmt.Errorf("CrossReferenceRepo validate heading: %w", err)
		}
	case anchorgrammar.PointKindKitabUnit:
		var cycle bool
		if err := tx.QueryRow(ctx, `
WITH RECURSIVE walk(id, lifecycle, path, cycle, depth) AS (
    SELECT id, lifecycle, ARRAY[id], FALSE, 0
    FROM citable_units
    WHERE anchor = $1
    UNION ALL
    SELECT u.id, u.lifecycle, w.path || u.id, u.id = ANY(w.path), w.depth + 1
    FROM walk w
    JOIN citable_unit_lineage l ON l.predecessor_id = w.id
    JOIN citable_units u ON u.id = l.successor_id
    WHERE NOT w.cycle AND w.depth < 4096
)
SELECT COALESCE(bool_or(lifecycle = 'active'), FALSE), COALESCE(bool_or(cycle), FALSE)
FROM walk`, point.String()).Scan(&exists, &cycle); err != nil {
			return fmt.Errorf("CrossReferenceRepo validate unit: %w", err)
		}
		if cycle {
			return entity.ErrAnchorLineageCycle
		}
	default:
		return entity.ErrInvalidCrossReference
	}

	if !exists {
		return fmt.Errorf("%w: Anchor does not resolve to active content", entity.ErrInvalidCrossReference)
	}

	return nil
}

func mapCrossReferenceWriteError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.Code {
	case "23505":
		return entity.ErrCrossReferenceConflict
	case "23503", "23514", "22P02":
		return entity.ErrInvalidCrossReference
	default:
		return err
	}
}
