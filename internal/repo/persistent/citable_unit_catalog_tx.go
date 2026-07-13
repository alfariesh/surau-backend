package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const knowledgeMentionBindingVersion = 1

const catalogChecksumSize = 32

// CitableCatalogChecksumVersion identifies the row-wise registry digest. The
// durable queue stores it so legacy evidence can be verified before an online
// algorithm-only upgrade.
const CitableCatalogChecksumVersion = 2

var errCatalogChecksumSize = errors.New("catalog checksum has invalid size")

type citableUnitCatalogTx struct {
	tx                         pgx.Tx
	legacyMentionBindingBypass bool
}

// WithCatalogTransaction owns K-1's complete one-book boundary. Registry
// writes, raw history, mention binding, checksums, and queue completion either
// commit together or all roll back.
//
//nolint:wsl_v5 // transaction safety gates intentionally remain in strict execution order
func (r *CitableUnitRepo) WithCatalogTransaction(
	ctx context.Context,
	bookID int,
	fn func(repo.CitableUnitCatalogTx) error,
) error {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return fmt.Errorf("CitableUnitRepo catalog begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if _, err := tx.Exec(ctx, registryWriterGUC); err != nil {
		return fmt.Errorf("CitableUnitRepo catalog registry guard: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, registryAdvisoryLockClass, bookID); err != nil {
		return fmt.Errorf("CitableUnitRepo catalog lock: %w", err)
	}

	// Only the first K-1 materialization may classify legacy approved mentions
	// as stale/ambiguous without aborting. Once a catalog profile has committed, every
	// delta reconcile is steady-state and the deferred database guard is enforced
	// before the queue item can be completed.
	var legacyMentionBindingBypass bool
	if err := tx.QueryRow(ctx, `
SELECT units_derivation_profile_version IS NULL
FROM books
WHERE id = $1`, bookID).Scan(&legacyMentionBindingBypass); err != nil {
		return fmt.Errorf("CitableUnitRepo catalog mention capability: %w", err)
	}
	if legacyMentionBindingBypass {
		if _, err := tx.Exec(ctx, `SET LOCAL surau.k1_mention_binding_backfill = 'on'`); err != nil {
			return fmt.Errorf("CitableUnitRepo catalog legacy mention guard: %w", err)
		}
	}

	if err := fn(&citableUnitCatalogTx{
		tx:                         tx,
		legacyMentionBindingBypass: legacyMentionBindingBypass,
	}); err != nil {
		return mapCatalogTransactionError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return mapCatalogTransactionError(fmt.Errorf("CitableUnitRepo catalog commit: %w", err))
	}

	return nil
}

// A source writer does not take the registry advisory lock. Under
// REPEATABLE READ, its post-snapshot update of the same books row is rejected
// by PostgreSQL with 40001. Expose that as the existing retryable CAS error so
// F1-H requeues the whole book without publishing a stale unit snapshot.
func mapCatalogTransactionError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "40001" {
		return fmt.Errorf("catalog source changed concurrently: %w",
			errors.Join(entity.ErrUnitReconcileConflict, err))
	}

	return err
}

func (t *citableUnitCatalogTx) LoadBookSource(ctx context.Context, bookID int) (entity.BookUnitSource, error) {
	return loadBookSource(ctx, t.tx, bookID)
}

func (t *citableUnitCatalogTx) Snapshot(ctx context.Context, bookID int) (entity.UnitRegistrySnapshot, error) {
	return snapshotFrom(ctx, t.tx, bookID)
}

func (t *citableUnitCatalogTx) ApplyReconcile(ctx context.Context, plan *entity.UnitReconcilePlan) error {
	return applyReconcileTx(ctx, t.tx, plan)
}

//nolint:funlen,wsl_v5 // exact-only binding SQL is kept intact with its review-state matrix
func (t *citableUnitCatalogTx) BindKnowledgeMentions(ctx context.Context, bookID int) error {
	_, err := t.tx.Exec(ctx, `
WITH mention_source AS (
	    SELECT mention.id,
           mention.page_id,
           mention.char_start,
           mention.char_end,
           mention.exact_quote,
           mention.source_hash,
           page.content_text,
	           mention.source_hash ~ '^[0-9A-Fa-f]{64}$' AS hash_well_formed,
	           COALESCE(
	               substring(page.content_text FROM mention.char_start + 1 FOR mention.char_end - mention.char_start)
	                   = mention.exact_quote,
	               FALSE
	           ) AS quote_matches_page
	    FROM knowledge_mentions mention
	    LEFT JOIN book_pages page
	      ON page.book_id = mention.book_id AND page.page_id = mention.page_id
    WHERE mention.book_id = $1
), candidates AS (
    SELECT source.id AS mention_id,
           unit.id AS unit_id,
           source.char_start - unit.source_char_start AS unit_char_start,
           source.char_end - unit.source_char_start AS unit_char_end,
           COUNT(*) OVER (PARTITION BY source.id) AS candidate_count,
           ROW_NUMBER() OVER (
               PARTITION BY source.id
               ORDER BY CASE unit.lifecycle WHEN 'active' THEN 0 ELSE 1 END,
                        unit.source_char_end - unit.source_char_start,
                        unit.id
           ) AS candidate_rank
    FROM mention_source source
    JOIN citable_units unit
      ON unit.book_id = $1
     AND unit.page_id = source.page_id
     AND unit.corpus = 'kitab'
     AND unit.content_role = 'book_page'
     AND unit.provenance_class = 'source'
     AND unit.lifecycle IN ('active', 'superseded')
     AND unit.source_document_hash IS NOT NULL
     AND encode(unit.source_document_hash, 'hex') = lower(source.source_hash)
     AND unit.source_char_start <= source.char_start
     AND unit.source_char_end >= source.char_end
    WHERE source.hash_well_formed AND source.quote_matches_page
), unit_overlaps AS (
    SELECT source.id AS mention_id, COUNT(unit.id) AS overlap_count
    FROM mention_source source
    JOIN knowledge_mentions mention ON mention.id = source.id
    LEFT JOIN citable_units unit
      ON unit.book_id = $1
     AND unit.page_id = mention.page_id
     AND unit.corpus = 'kitab'
     AND unit.content_role = 'book_page'
     AND unit.provenance_class = 'source'
     AND unit.lifecycle IN ('active', 'superseded')
     AND unit.source_document_hash IS NOT NULL
     AND encode(unit.source_document_hash, 'hex') = lower(source.source_hash)
     AND unit.source_char_start < source.char_end
     AND unit.source_char_end > source.char_start
    GROUP BY source.id
), resolved AS (
    SELECT source.id,
           CASE WHEN candidate.candidate_count = 1 THEN candidate.unit_id END AS unit_id,
           CASE WHEN candidate.candidate_count = 1 THEN candidate.unit_char_start END AS unit_char_start,
           CASE WHEN candidate.candidate_count = 1 THEN candidate.unit_char_end END AS unit_char_end,
           CASE
               WHEN NOT source.hash_well_formed OR NOT source.quote_matches_page THEN 'stale'
               WHEN candidate.candidate_count = 1 THEN 'bound'
               WHEN candidate.candidate_count > 1 THEN 'ambiguous'
               WHEN overlap.overlap_count > 1 THEN 'cross_unit'
               ELSE 'missing'
           END AS binding_status
    FROM mention_source source
    LEFT JOIN candidates candidate
      ON candidate.mention_id = source.id AND candidate.candidate_rank = 1
    LEFT JOIN unit_overlaps overlap ON overlap.mention_id = source.id
)
UPDATE knowledge_mentions mention
SET unit_id = resolved.unit_id,
    unit_char_start = resolved.unit_char_start,
    unit_char_end = resolved.unit_char_end,
    unit_binding_status = resolved.binding_status,
    unit_binding_version = $2,
    unit_source_hash = CASE WHEN resolved.binding_status = 'bound' THEN mention.source_hash ELSE NULL END
FROM resolved
WHERE mention.id = resolved.id`, bookID, knowledgeMentionBindingVersion)
	if err != nil {
		return fmt.Errorf("bind knowledge mentions for book %d: %w", bookID, err)
	}
	if !t.legacyMentionBindingBypass {
		// Force the deferred constraint now, before checksum and queue completion.
		// This also catches missing-page mentions because the LEFT JOIN above
		// classifies every mention row and therefore fires its constraint trigger.
		if _, err := t.tx.Exec(ctx,
			`SET CONSTRAINTS trg_knowledge_mentions_approved_unit_guard IMMEDIATE`); err != nil {
			return fmt.Errorf("validate approved knowledge mentions for book %d: %w", bookID, err)
		}
	}

	return nil
}

func (t *citableUnitCatalogTx) SourceFingerprint(ctx context.Context, bookID int) ([32]byte, error) {
	return catalogSourceFingerprint(ctx, t.tx, bookID)
}

func catalogSourceFingerprint(ctx context.Context, q pgxQuerier, bookID int) ([32]byte, error) {
	var digest []byte

	err := q.QueryRow(ctx, `
SELECT sha256(convert_to(jsonb_build_object(
    'book', jsonb_build_array(b.id, COALESCE(b.major_release, 0), COALESCE(b.minor_release, 0), b.is_deleted, b.has_content),
    'publication', jsonb_build_array(p.status, p.updated_at),
    'pages', COALESCE((
        SELECT jsonb_agg(jsonb_build_array(page_id, updated_at, is_deleted, content_text, content_html) ORDER BY page_id)
        FROM book_pages WHERE book_id = b.id
    ), '[]'::jsonb),
    'headings', COALESCE((
        SELECT jsonb_agg(jsonb_build_array(heading_id, page_id, updated_at, is_deleted, content) ORDER BY heading_id)
        FROM book_headings WHERE book_id = b.id
    ), '[]'::jsonb),
    'page_edits', COALESCE((
        SELECT jsonb_agg(jsonb_build_array(page_id, status, updated_at, content_text, content_html) ORDER BY page_id, status)
        FROM book_page_edits WHERE book_id = b.id AND status = 'published'
    ), '[]'::jsonb),
    'translations', COALESCE((
        SELECT jsonb_agg(jsonb_build_array(heading_id, lang, updated_at, is_deleted, content, provenance_class, generation_run_id) ORDER BY heading_id, lang)
        FROM section_translations translation
        WHERE translation.book_id = b.id AND translation.is_deleted = FALSE
          AND (translation.lang = 'ar' OR EXISTS (
              SELECT 1 FROM book_production_projects project
              WHERE project.book_id = translation.book_id AND project.lang = translation.lang
                AND project.publication_status = 'published' AND project.workflow_status <> 'archived'
          ))
    ), '[]'::jsonb),
    'summaries', COALESCE((
        SELECT jsonb_agg(jsonb_build_array(heading_id, lang, updated_at, is_deleted, summary, provenance_class, generation_run_id) ORDER BY heading_id, lang)
        FROM book_heading_summaries summary
        WHERE summary.book_id = b.id AND summary.is_deleted = FALSE
          AND (summary.lang = 'ar' OR EXISTS (
              SELECT 1 FROM book_production_projects project
              WHERE project.book_id = summary.book_id AND project.lang = summary.lang
                AND project.publication_status = 'published' AND project.workflow_status <> 'archived'
          ))
    ), '[]'::jsonb),
    'production_projects', COALESCE((
        SELECT jsonb_agg(jsonb_build_array(id, lang, workflow_status, publication_status, updated_at, published_at, archived_at) ORDER BY lang, id)
        FROM book_production_projects
        WHERE book_id = b.id AND publication_status = 'published' AND workflow_status <> 'archived'
    ), '[]'::jsonb)
)::text, 'UTF8'))
FROM books b
JOIN book_publications p ON p.book_id = b.id
WHERE b.id = $1 AND p.status = 'published'`, bookID).Scan(&digest)
	if err != nil {
		return [32]byte{}, fmt.Errorf("catalog source fingerprint book %d: %w", bookID, err)
	}

	if len(digest) != catalogChecksumSize {
		return [32]byte{}, fmt.Errorf("catalog source fingerprint book %d returned %d bytes: %w",
			bookID, len(digest), errCatalogChecksumSize)
	}

	var checksum [catalogChecksumSize]byte
	copy(checksum[:], digest)

	return checksum, nil
}

func (t *citableUnitCatalogTx) RegistryChecksum(ctx context.Context, bookID int) ([32]byte, error) {
	return catalogRegistryChecksum(ctx, t.tx, bookID)
}

func catalogRegistryChecksum(ctx context.Context, q pgxQuerier, bookID int) ([32]byte, error) {
	var digest []byte

	err := q.QueryRow(ctx, `
WITH unit_rows AS MATERIALIZED (
    SELECT id, sha256(convert_to(jsonb_build_array(
            id, corpus, book_id, heading_id, page_id, kind, ordinal, position,
            parent_unit_id, anchor, marker, text, html, text_normalized,
            normalization_version, encode(content_hash, 'hex'), occurrence, language,
            provenance_class, provenance_detail, generation_run_id, license_status, lifecycle,
            content_role, review_status, encode(source_document_hash, 'hex'),
            source_char_start, source_char_end
        )::text, 'UTF8')) AS row_digest
    FROM citable_units
    WHERE book_id = $1
),
lineage_rows AS MATERIALIZED (
    SELECT l.predecessor_id, l.successor_id, l.reason,
           sha256(convert_to(jsonb_build_array(
               l.predecessor_id, l.successor_id, l.reason
           )::text, 'UTF8')) AS row_digest
    FROM citable_unit_lineage l
    JOIN citable_units u ON u.id = l.predecessor_id
    WHERE u.book_id = $1
),
unit_digest AS (
    SELECT sha256(convert_to(COALESCE(
        string_agg(encode(row_digest, 'hex'), '' ORDER BY id), ''
    ), 'UTF8')) AS value
    FROM unit_rows
),
lineage_digest AS (
    SELECT sha256(convert_to(COALESCE(
        string_agg(encode(row_digest, 'hex'), ''
            ORDER BY predecessor_id, successor_id, reason), ''
    ), 'UTF8')) AS value
    FROM lineage_rows
)
SELECT sha256(unit_digest.value || lineage_digest.value)
FROM unit_digest CROSS JOIN lineage_digest`, bookID).Scan(&digest)
	if err != nil {
		return [32]byte{}, fmt.Errorf("catalog registry checksum book %d: %w", bookID, err)
	}

	if len(digest) != catalogChecksumSize {
		return [32]byte{}, fmt.Errorf("catalog registry checksum book %d returned %d bytes: %w",
			bookID, len(digest), errCatalogChecksumSize)
	}

	var checksum [catalogChecksumSize]byte
	copy(checksum[:], digest)

	return checksum, nil
}

// CatalogEvidenceChecksums recomputes the live source and registry digests
// used by both durable queue passes. The acceptance verifier compares these
// values with stored evidence so stale-but-mutually-equal queue rows cannot
// prove determinism after an editorial/import reconcile.
func (r *CitableUnitRepo) CatalogEvidenceChecksums(
	ctx context.Context,
	bookID int,
) (source, registry [32]byte, err error) {
	source, err = catalogSourceFingerprint(ctx, r.Pool, bookID)
	if err != nil {
		return source, registry, err
	}

	registry, err = catalogRegistryChecksum(ctx, r.Pool, bookID)

	return source, registry, err
}

// CatalogLegacyRegistryChecksum recomputes v1 only while upgrading durable
// evidence written before the row-wise checksum existed. New queue writes
// never use this JSONB aggregate.
func (r *CitableUnitRepo) CatalogLegacyRegistryChecksum(ctx context.Context, bookID int) ([32]byte, error) {
	return catalogRegistryChecksumLegacyV1(ctx, r.Pool, bookID)
}

func catalogRegistryChecksumLegacyV1(ctx context.Context, q pgxQuerier, bookID int) ([32]byte, error) {
	var digest []byte

	err := q.QueryRow(ctx, `
SELECT sha256(convert_to(jsonb_build_object(
    'units', COALESCE((
        SELECT jsonb_agg(jsonb_build_array(
            id, corpus, book_id, heading_id, page_id, kind, ordinal, position,
            parent_unit_id, anchor, marker, text, html, text_normalized,
            normalization_version, encode(content_hash, 'hex'), occurrence, language,
            provenance_class, provenance_detail, generation_run_id, license_status, lifecycle,
            content_role, review_status, encode(source_document_hash, 'hex'),
            source_char_start, source_char_end
        ) ORDER BY id)
        FROM citable_units WHERE book_id = $1
    ), '[]'::jsonb),
    'lineage', COALESCE((
        SELECT jsonb_agg(jsonb_build_array(
            l.predecessor_id, l.successor_id, l.reason
        ) ORDER BY l.predecessor_id, l.successor_id)
        FROM citable_unit_lineage l
        JOIN citable_units u ON u.id = l.predecessor_id
        WHERE u.book_id = $1
    ), '[]'::jsonb)
)::text, 'UTF8'))`, bookID).Scan(&digest)
	if err != nil {
		return [32]byte{}, fmt.Errorf("legacy catalog registry checksum book %d: %w", bookID, err)
	}

	if len(digest) != catalogChecksumSize {
		return [32]byte{}, fmt.Errorf("legacy catalog registry checksum book %d returned %d bytes: %w",
			bookID, len(digest), errCatalogChecksumSize)
	}

	var checksum [catalogChecksumSize]byte
	copy(checksum[:], digest)

	return checksum, nil
}

//nolint:wsl_v5 // queue CAS result is checked immediately after persistence
func (t *citableUnitCatalogTx) CompleteQueueItem(
	ctx context.Context,
	jobName string,
	bookID int,
	source, checksum [32]byte,
) error {
	tag, err := t.tx.Exec(ctx, `
UPDATE citable_unit_catalog_queue
SET status = 'completed',
    source_fingerprint = $3,
    result_checksum = $4,
    checksum_version = $5,
    error = NULL,
    finished_at = now(),
    updated_at = now()
WHERE job_name = $1 AND book_id = $2 AND status = 'running'`,
		jobName, bookID, source[:], checksum[:], CitableCatalogChecksumVersion)
	if err != nil {
		return fmt.Errorf("complete catalog queue book %d: %w", bookID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete catalog queue book %d affected %d rows: %w",
			bookID, tag.RowsAffected(), entity.ErrUnitReconcileConflict)
	}

	return nil
}
