package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// registryAdvisoryLockClass namespaces pg_advisory_xact_lock(class, book_id)
// for reconcile serialization; distinct from the backfill runner's lock space.
const registryAdvisoryLockClass = 71001

// registryWriterGUC must match citable_registry_guard() in the migration; the
// guard rejects any DML outside a transaction that sets it.
const registryWriterGUC = "SET LOCAL surau.registry_writer = 'unit-service'"

// CitableUnitRepo persists the shared Citable Unit registry (phase-1b B-1).
type CitableUnitRepo struct {
	*postgres.Postgres
}

// NewCitableUnitRepo -.
func NewCitableUnitRepo(pg *postgres.Postgres) *CitableUnitRepo {
	return &CitableUnitRepo{pg}
}

// LoadBookSource loads one book's effective derivation input: pages merged
// with published editorial edits (COALESCE), soft-deleted rows excluded.
// Deliberately WITHOUT the book_publications join — that is a public read
// gate, not a derivation gate (C2). LoadedAt is the DB clock at load time and
// becomes units_derived_at, so edits racing the reconcile are never masked.
//
//nolint:gocyclo,cyclop,funlen // linear: book row + two result-set scan loops, each with its own error guards
func (r *CitableUnitRepo) LoadBookSource(ctx context.Context, bookID int) (entity.BookUnitSource, error) {
	return loadBookSource(ctx, r.Pool, bookID)
}

type citableQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

//nolint:gocognit,gocyclo,cyclop,funlen,wsl_v5 // linear source projection shared by pool and catalog transaction
func loadBookSource(ctx context.Context, q citableQueryer, bookID int) (entity.BookUnitSource, error) {
	src := entity.BookUnitSource{BookID: bookID}

	var major, minor int

	err := q.QueryRow(ctx, `
		SELECT COALESCE(major_release, 0), COALESCE(minor_release, 0), now()
		FROM books
		WHERE id = $1 AND is_deleted = FALSE`, bookID).Scan(&major, &minor, &src.LoadedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return src, entity.ErrBookNotFound
	}

	if err != nil {
		return src, fmt.Errorf("CitableUnitRepo.LoadBookSource books: %w", err)
	}

	src.ReleaseKey = fmt.Sprintf("%d.%d", major, minor)

	rows, err := q.Query(ctx, `
		SELECT bp.page_id,
		       COALESCE(pe.content_html, bp.content_html),
		       COALESCE(pe.content_text, bp.content_text),
		       bp.content_html,
		       bp.content_text,
		       pe.book_id IS NOT NULL,
		       COALESCE(pe.updated_by::text, '')
		FROM book_pages bp
		LEFT JOIN book_page_edits pe
		  ON pe.book_id = bp.book_id AND pe.page_id = bp.page_id AND pe.status = 'published'
		WHERE bp.book_id = $1 AND bp.is_deleted = FALSE
		ORDER BY bp.page_id`, bookID)
	if err != nil {
		return src, fmt.Errorf("CitableUnitRepo.LoadBookSource pages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p entity.BookUnitSourcePage
		if err := rows.Scan(&p.PageID, &p.ContentHTML, &p.ContentText, &p.RawContentHTML,
			&p.RawContentText, &p.HasPublishedEdit, &p.EditActorID); err != nil {
			return src, fmt.Errorf("CitableUnitRepo.LoadBookSource scan page: %w", err)
		}

		src.Pages = append(src.Pages, p)
	}

	if err := rows.Err(); err != nil {
		return src, fmt.Errorf("CitableUnitRepo.LoadBookSource pages rows: %w", err)
	}

	hrows, err := q.Query(ctx, `
		SELECT heading_id, page_id
		FROM book_headings
		WHERE book_id = $1 AND is_deleted = FALSE
		ORDER BY heading_id`, bookID)
	if err != nil {
		return src, fmt.Errorf("CitableUnitRepo.LoadBookSource headings: %w", err)
	}
	defer hrows.Close()

	for hrows.Next() {
		var h entity.BookUnitSourceHeading
		if err := hrows.Scan(&h.HeadingID, &h.PageID); err != nil {
			return src, fmt.Errorf("CitableUnitRepo.LoadBookSource scan heading: %w", err)
		}

		src.Headings = append(src.Headings, h)
	}

	if err := hrows.Err(); err != nil {
		return src, fmt.Errorf("CitableUnitRepo.LoadBookSource headings rows: %w", err)
	}

	assetRows, err := q.Query(ctx, `
		SELECT st.heading_id, bh.page_id, 'section_translation', st.lang, st.content,
		       st.provenance_class, st.generation_run_id::text,
		       CASE WHEN st.translation_status = 'reviewed' THEN 'approved' ELSE 'pending' END
		FROM section_translations st
		JOIN book_headings bh ON bh.book_id = st.book_id AND bh.heading_id = st.heading_id
		WHERE st.book_id = $1 AND NOT st.is_deleted AND NOT bh.is_deleted
		  AND (
		      st.lang = 'ar'
		      OR EXISTS (
		          SELECT 1 FROM book_production_projects project
		          WHERE project.book_id = st.book_id
		            AND project.lang = st.lang
		            AND project.publication_status = 'published'
		            AND project.workflow_status <> 'archived'
		      )
		  )
		UNION ALL
		SELECT hs.heading_id, bh.page_id, 'heading_summary', hs.lang, hs.summary,
		       hs.provenance_class, hs.generation_run_id::text,
		       CASE WHEN hs.summary_status = 'reviewed' THEN 'approved' ELSE 'pending' END
		FROM book_heading_summaries hs
		JOIN book_headings bh ON bh.book_id = hs.book_id AND bh.heading_id = hs.heading_id
		WHERE hs.book_id = $1 AND NOT hs.is_deleted AND NOT bh.is_deleted
		  AND (
		      hs.lang = 'ar'
		      OR EXISTS (
		          SELECT 1 FROM book_production_projects project
		          WHERE project.book_id = hs.book_id
		            AND project.lang = hs.lang
		            AND project.publication_status = 'published'
		            AND project.workflow_status <> 'archived'
		      )
		  )
		ORDER BY 1, 3, 4`, bookID)
	if err != nil {
		return src, fmt.Errorf("CitableUnitRepo.LoadBookSource assets: %w", err)
	}
	defer assetRows.Close()

	for assetRows.Next() {
		var asset entity.BookUnitSourceAsset
		if err := assetRows.Scan(&asset.HeadingID, &asset.PageID, &asset.ContentRole, &asset.Language,
			&asset.Content, &asset.ProvenanceClass, &asset.GenerationRunID, &asset.ReviewStatus); err != nil {
			return src, fmt.Errorf("CitableUnitRepo.LoadBookSource scan asset: %w", err)
		}
		src.Assets = append(src.Assets, asset)
	}
	if err := assetRows.Err(); err != nil {
		return src, fmt.Errorf("CitableUnitRepo.LoadBookSource asset rows: %w", err)
	}

	return src, nil
}

// Snapshot loads the planner's registry read model for one book. All reads run
// inside ONE repeatable-read snapshot so the Active list, ordinal high-water
// marks, and the fingerprint are mutually consistent: plan.BasedOn then matches
// the data the plan was built from, and any concurrent reconcile that commits
// afterwards is caught at apply time as a retryable ErrUnitReconcileConflict
// (rather than defeating the check with a fingerprint read from a later state).
//
//nolint:gocyclo,cyclop,funlen,wsl_v5 // linear: repeatable-read tx + active-units scan loop + ordinal/id scan loop + fingerprint, each guarded
func (r *CitableUnitRepo) Snapshot(ctx context.Context, bookID int) (entity.UnitRegistrySnapshot, error) {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return entity.UnitRegistrySnapshot{}, fmt.Errorf("CitableUnitRepo.Snapshot begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	return snapshotFrom(ctx, tx, bookID)
}

//nolint:gocognit,gocyclo,cyclop,funlen,wsl_v5 // shared transaction snapshot projection
func snapshotFrom(ctx context.Context, q citableQueryer, bookID int) (entity.UnitRegistrySnapshot, error) {
	snap := entity.UnitRegistrySnapshot{
		MaxOrdinalByScope: map[int]int{},
		ExistingIDs:       map[string]struct{}{},
	}

	rows, err := q.Query(ctx, `
		SELECT id, heading_id, page_id, kind, ordinal, position, parent_unit_id,
		       marker, content_hash, occurrence, lifecycle, html, language, content_role,
		       review_status, source_document_hash, source_char_start, source_char_end,
		       provenance_class, provenance_detail, generation_run_id::text
		FROM citable_units
		WHERE book_id = $1 AND lifecycle = 'active'`, bookID)
	if err != nil {
		return snap, fmt.Errorf("CitableUnitRepo.Snapshot active: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		bookIDValue := bookID
		u := entity.CitableUnit{Corpus: entity.UnitCorpusKitab, BookID: &bookIDValue}
		var detail []byte
		if err := rows.Scan(&u.ID, &u.HeadingID, &u.PageID, &u.Kind, &u.Ordinal, &u.Position,
			&u.ParentUnitID, &u.Marker, &u.ContentHash, &u.Occurrence, &u.Lifecycle, &u.HTML,
			&u.Language, &u.ContentRole, &u.ReviewStatus, &u.SourceDocumentHash,
			&u.SourceCharStart, &u.SourceCharEnd, &u.ProvenanceClass, &detail, &u.GenerationRunID); err != nil {
			return snap, fmt.Errorf("CitableUnitRepo.Snapshot scan: %w", err)
		}
		if len(detail) > 0 {
			if err := json.Unmarshal(detail, &u.ProvenanceDetail); err != nil {
				return snap, fmt.Errorf("CitableUnitRepo.Snapshot provenance %s: %w", u.ID, err)
			}
		}

		snap.Active = append(snap.Active, u)
	}

	if err := rows.Err(); err != nil {
		return snap, fmt.Errorf("CitableUnitRepo.Snapshot active rows: %w", err)
	}

	rows.Close()

	orows, err := q.Query(ctx, `
		SELECT COALESCE(heading_id, 0), MAX(ordinal), array_agg(id)
		FROM citable_units
		WHERE book_id = $1
		GROUP BY COALESCE(heading_id, 0)`, bookID)
	if err != nil {
		return snap, fmt.Errorf("CitableUnitRepo.Snapshot ordinals: %w", err)
	}
	defer orows.Close()

	for orows.Next() {
		var (
			scope, maxOrdinal int
			ids               []string
		)
		if err := orows.Scan(&scope, &maxOrdinal, &ids); err != nil {
			return snap, fmt.Errorf("CitableUnitRepo.Snapshot scan ordinals: %w", err)
		}

		snap.MaxOrdinalByScope[scope] = maxOrdinal
		for _, id := range ids {
			snap.ExistingIDs[id] = struct{}{}
		}
	}

	if err := orows.Err(); err != nil {
		return snap, fmt.Errorf("CitableUnitRepo.Snapshot ordinal rows: %w", err)
	}

	orows.Close()

	snap.Fingerprint, err = registryFingerprint(ctx, q, bookID)
	if err != nil {
		return snap, err
	}

	return snap, nil
}

type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func registryFingerprint(ctx context.Context, q pgxQuerier, bookID int) (entity.UnitRegistryFingerprint, error) {
	var fp entity.UnitRegistryFingerprint

	err := q.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE lifecycle = 'active'),
		       COALESCE(MAX(ordinal), 0),
		       COALESCE(MAX(updated_at), 'epoch'::timestamptz)
		FROM citable_units
		WHERE book_id = $1`, bookID).Scan(&fp.ActiveCount, &fp.MaxOrdinal, &fp.MaxUpdatedAt)
	if err != nil {
		return fp, fmt.Errorf("CitableUnitRepo fingerprint: %w", err)
	}

	return fp, nil
}

//nolint:wsl_v5 // one source-drift query followed by its explicit guard
func bookSourceChangedSince(ctx context.Context, q pgxQuerier, bookID int, loadedAt time.Time) (bool, error) {
	var changed bool
	err := q.QueryRow(ctx, `
		SELECT GREATEST(
			COALESCE((SELECT MAX(updated_at) FROM book_pages WHERE book_id = $1), 'epoch'),
			COALESCE((SELECT MAX(updated_at) FROM book_headings WHERE book_id = $1), 'epoch'),
			COALESCE((SELECT MAX(updated_at) FROM book_page_edits WHERE book_id = $1 AND status = 'published'), 'epoch'),
			COALESCE((SELECT MAX(updated_at) FROM section_translations WHERE book_id = $1), 'epoch'),
			COALESCE((SELECT MAX(updated_at) FROM book_heading_summaries WHERE book_id = $1), 'epoch'),
			COALESCE((SELECT MAX(updated_at) FROM book_production_projects WHERE book_id = $1), 'epoch')
		) > $2
		OR COALESCE((SELECT units_stale_at > $2 FROM books WHERE id = $1), FALSE)`, bookID, loadedAt).Scan(&changed)
	if err != nil {
		return false, fmt.Errorf("CitableUnitRepo source fingerprint: %w", err)
	}

	return changed, nil
}

// ApplyReconcile applies one book plan atomically: guard GUC, per-book
// advisory lock, optimistic fingerprint check, batched mints (parents first) /
// locator updates / retires / lineage edges, units_derived_at stamp, then
// in-transaction invariant asserts. Any violated invariant rolls everything
// back — the registry never drifts partially.
//
//nolint:wsl_v5 // linear batch pipeline: guard -> lock -> fingerprint -> queue mints/updates/retires/edges -> assert; each stage is a distinct guard
func (r *CitableUnitRepo) ApplyReconcile(ctx context.Context, plan *entity.UnitReconcilePlan) error {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyReconcile begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if _, err := tx.Exec(ctx, registryWriterGUC); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyReconcile guard guc: %w", err)
	}

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, registryAdvisoryLockClass, plan.BookID); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyReconcile lock: %w", err)
	}
	if err := applyReconcileTx(ctx, tx, plan); err != nil {
		return err
	}
	// Editorial/import reconciles must enforce the same exact mention binding
	// boundary as the catalog runner. Otherwise retiring a cited unit could
	// commit while an approved mention still points at it.
	if err := (&citableUnitCatalogTx{tx: tx}).BindKnowledgeMentions(ctx, plan.BookID); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyReconcile bind mentions: %w", err)
	}
	// This wrapper is the editorial/import path; the catalog transaction calls
	// applyReconcileTx directly and owns queue completion itself. Any successful
	// out-of-band reconcile invalidates both raw and determinism evidence in the
	// same transaction, even though it has already cleared units_stale_at.
	if _, err := tx.Exec(ctx, `
		UPDATE citable_unit_catalog_queue
		SET status = 'pending',
		    source_fingerprint = NULL,
		    result_checksum = NULL,
		    error = 'registry changed outside catalog runner',
		    started_at = NULL,
		    finished_at = NULL,
		    updated_at = now()
		WHERE book_id = $1 AND status = 'completed'`, plan.BookID); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyReconcile invalidate catalog evidence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyReconcile commit: %w", err)
	}

	return nil
}

//nolint:gocognit,gocyclo,cyclop,funlen,wsl_v5 // one atomic guarded batch used by hook and catalog transaction
func applyReconcileTx(ctx context.Context, tx pgx.Tx, plan *entity.UnitReconcilePlan) error {
	fp, err := registryFingerprint(ctx, tx, plan.BookID)
	if err != nil {
		return err
	}

	if fp != plan.BasedOn {
		return entity.ErrUnitReconcileConflict
	}

	changed, err := bookSourceChangedSince(ctx, tx, plan.BookID, plan.LoadedAt)
	if err != nil {
		return err
	}
	if changed {
		return entity.ErrUnitReconcileConflict
	}

	batch := &pgx.Batch{}
	mintCount := 0
	queueMint := func(u *entity.CitableUnit) error {
		detail, err := json.Marshal(u.ProvenanceDetail)
		if err != nil {
			return fmt.Errorf("CitableUnitRepo.ApplyReconcile marshal provenance (unit %s): %w", u.ID, err)
		}

		batch.Queue(`
			INSERT INTO citable_units (
				id, corpus, book_id, heading_id, page_id, kind, ordinal, position,
				parent_unit_id, anchor, marker, text, html, text_normalized,
					normalization_version, content_hash, occurrence, language, content_role,
					review_status, source_document_hash, source_char_start, source_char_end,
					provenance_class, provenance_detail, generation_run_id, license_status, lifecycle, retired_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,
				    CASE WHEN $28::text = 'active' THEN NULL ELSE now() END)`,
			u.ID, u.Corpus, u.BookID, u.HeadingID, u.PageID, u.Kind, u.Ordinal, u.Position,
			u.ParentUnitID, u.Anchor, u.Marker, u.Text, u.HTML, u.TextNormalized,
			u.NormalizationVersion, u.ContentHash, u.Occurrence, u.Language, u.ContentRole,
			u.ReviewStatus, u.SourceDocumentHash, u.SourceCharStart, u.SourceCharEnd,
			u.ProvenanceClass, detail, u.GenerationRunID, u.LicenseStatus, u.Lifecycle)

		mintCount++

		return nil
	}
	// Parent FK: body units first, footnotes (the only parented kind) after,
	// across both current and already-superseded historical inserts.
	mintSets := [][]entity.CitableUnit{plan.Mints, plan.HistoricalMints}
	for _, mints := range mintSets {
		for i := range mints {
			if mints[i].ParentUnitID == nil {
				if err := queueMint(&mints[i]); err != nil {
					return err
				}
			}
		}
	}

	for _, mints := range mintSets {
		for i := range mints {
			if mints[i].ParentUnitID != nil {
				if err := queueMint(&mints[i]); err != nil {
					return err
				}
			}
		}
	}

	for _, up := range plan.Updates {
		detail, err := json.Marshal(up.ProvenanceDetail)
		if err != nil {
			return fmt.Errorf("CitableUnitRepo.ApplyReconcile marshal update provenance (unit %s): %w", up.ID, err)
		}
		batch.Queue(`
			UPDATE citable_units
			SET position = $2,
			    page_id = $3,
			    parent_unit_id = $4,
			    provenance_detail = $5,
			    html = $6,
			    review_status = $7,
			    source_document_hash = $8,
			    source_char_start = $9,
			    source_char_end = $10,
			    updated_at = now()
			WHERE id = $1 AND lifecycle = 'active'`,
			up.ID, up.Position, up.PageID, up.ParentUnitID, detail, up.HTML,
			up.ReviewStatus, up.SourceDocumentHash, up.SourceCharStart, up.SourceCharEnd)
	}

	for _, ret := range plan.Retires {
		batch.Queue(`
			UPDATE citable_units
			SET lifecycle = $2, retired_at = now(), updated_at = now()
			WHERE id = $1 AND lifecycle = 'active'`,
			ret.ID, ret.Lifecycle)
	}

	for _, e := range plan.Edges {
		batch.Queue(`
			INSERT INTO citable_unit_lineage (predecessor_id, successor_id, reason)
			VALUES ($1, $2, $3)`, e.PredecessorID, e.SuccessorID, e.Reason)
	}

	batch.Queue(`
		UPDATE books
		SET units_derived_at = $2,
		    units_derivation_profile_version = CASE
		        WHEN $4 THEN units_derivation_profile_version ELSE $3
		    END,
		    units_stale_at = CASE
		        WHEN $4 THEN COALESCE(units_stale_at, $2)
		        WHEN units_stale_at > $2 THEN units_stale_at
		        ELSE NULL
		    END
		WHERE id = $1`, plan.BookID, plan.LoadedAt, entity.KitabUnitDerivationProfileVersion,
		plan.Intermediate)

	results := tx.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		tag, err := results.Exec()
		if err != nil {
			_ = results.Close()

			return fmt.Errorf("CitableUnitRepo.ApplyReconcile batch stmt %d: %w", i, err)
		}
		// Updates and retires must each hit exactly one live row; a miss means
		// the snapshot lied despite the fingerprint — abort loudly.
		isMint := i < mintCount
		if !isMint && tag.RowsAffected() != 1 {
			_ = results.Close()

			return fmt.Errorf("CitableUnitRepo.ApplyReconcile stmt %d affected %d rows: %w",
				i, tag.RowsAffected(), entity.ErrUnitReconcileConflict)
		}
	}

	if err := results.Close(); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyReconcile batch close: %w", err)
	}

	if err := assertRegistryInvariants(ctx, tx, plan); err != nil {
		return err
	}

	return nil
}

// errRegistryInvariant marks a post-write invariant breach — the planner
// promised something the database does not reflect, so the reconcile aborts.
var errRegistryInvariant = errors.New("citable registry invariant violated")

// assertRegistryInvariants re-checks the planner's promises inside the write
// transaction; any violation rolls the whole reconcile back (C2 "pemeriksaan
// invarian sinkron").
func assertRegistryInvariants(ctx context.Context, tx pgx.Tx, plan *entity.UnitReconcilePlan) error {
	var activeCount int64
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM citable_units
		WHERE book_id = $1 AND lifecycle = 'active'`, plan.BookID).Scan(&activeCount); err != nil {
		return fmt.Errorf("CitableUnitRepo invariant active count: %w", err)
	}

	if activeCount != plan.ExpectedActive {
		return fmt.Errorf("%w: active=%d expected=%d (book %d)",
			errRegistryInvariant, activeCount, plan.ExpectedActive, plan.BookID)
	}

	var orphanSuperseded int64
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM citable_units u
		WHERE u.book_id = $1 AND u.lifecycle = 'superseded'
		  AND NOT EXISTS (SELECT 1 FROM citable_unit_lineage l WHERE l.predecessor_id = u.id)`,
		plan.BookID).Scan(&orphanSuperseded); err != nil {
		return fmt.Errorf("CitableUnitRepo invariant superseded: %w", err)
	}

	if orphanSuperseded != 0 {
		return fmt.Errorf("%w: %d superseded units without successor (book %d)",
			errRegistryInvariant, orphanSuperseded, plan.BookID)
	}

	var activePredecessors int64
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM citable_unit_lineage l
		JOIN citable_units u ON u.id = l.predecessor_id
		WHERE u.book_id = $1 AND u.lifecycle = 'active'`, plan.BookID).Scan(&activePredecessors); err != nil {
		return fmt.Errorf("CitableUnitRepo invariant predecessors: %w", err)
	}

	if activePredecessors != 0 {
		return fmt.Errorf("%w: %d lineage edges with active predecessor (book %d)",
			errRegistryInvariant, activePredecessors, plan.BookID)
	}

	return nil
}

// BookDerivedAt returns books.units_derived_at (nil until the initial
// backfill derived the book) — the editorial-publish hook gate.
func (r *CitableUnitRepo) BookDerivedAt(ctx context.Context, bookID int) (*time.Time, error) {
	var derivedAt *time.Time

	err := r.Pool.QueryRow(ctx,
		`SELECT units_derived_at FROM books WHERE id = $1`, bookID).Scan(&derivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, entity.ErrBookNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("CitableUnitRepo.BookDerivedAt: %w", err)
	}

	return derivedAt, nil
}

const citableUnitColumns = `
	u.id, u.corpus, u.book_id, u.heading_id, u.page_id, u.kind, u.ordinal, u.position,
	u.parent_unit_id, u.anchor, u.marker, u.text, u.html, u.text_normalized,
	u.normalization_version, u.content_hash, u.occurrence, u.language,
	COALESCE(u.content_role, ''), u.review_status, u.source_document_hash, u.source_char_start, u.source_char_end,
	u.provenance_class, u.provenance_detail, u.generation_run_id::text, u.license_status, u.lifecycle,
	u.retired_at, u.created_at, u.updated_at, license.effective_license_status, license.license_source`

const citableUnitLicenseFrom = `
	citable_units u
	JOIN citable_units_with_effective_license license ON license.id = u.id`

func scanCitableUnit(row pgx.Row) (entity.CitableUnit, error) {
	var (
		u      entity.CitableUnit
		detail []byte
	)

	err := row.Scan(&u.ID, &u.Corpus, &u.BookID, &u.HeadingID, &u.PageID, &u.Kind, &u.Ordinal,
		&u.Position, &u.ParentUnitID, &u.Anchor, &u.Marker, &u.Text, &u.HTML, &u.TextNormalized,
		&u.NormalizationVersion, &u.ContentHash, &u.Occurrence, &u.Language,
		&u.ContentRole, &u.ReviewStatus, &u.SourceDocumentHash, &u.SourceCharStart, &u.SourceCharEnd,
		&u.ProvenanceClass, &detail, &u.GenerationRunID, &u.LicenseStatus, &u.Lifecycle,
		&u.RetiredAt, &u.CreatedAt, &u.UpdatedAt, &u.EffectiveLicenseStatus, &u.LicenseSource)
	if err != nil {
		return u, err
	}

	if len(detail) > 0 {
		if err := json.Unmarshal(detail, &u.ProvenanceDetail); err != nil {
			return u, fmt.Errorf("citable unit %s provenance detail: %w", u.ID, err)
		}
	}

	return u, nil
}

// ResolveUnit loads one unit and, when it is retired, walks the lineage DAG to
// the active successor(s). Old anchors therefore never dead-end (AC-2); the
// full anchor-grammar resolution surface is B-2.
//
//nolint:cyclop,gocyclo // guarded root read, shared lineage walk, and ordered batch hydration
func (r *CitableUnitRepo) ResolveUnit(ctx context.Context, unitID string) (entity.UnitResolution, error) {
	var res entity.UnitResolution
	if _, err := uuid.Parse(unitID); err != nil {
		return res, entity.ErrUnitNotFound
	}

	unit, err := scanCitableUnit(r.Pool.QueryRow(ctx,
		`SELECT `+citableUnitColumns+` FROM `+citableUnitLicenseFrom+` WHERE u.id = $1`, unitID))
	if errors.Is(err, pgx.ErrNoRows) {
		return res, entity.ErrUnitNotFound
	}

	if err != nil {
		return res, fmt.Errorf("CitableUnitRepo.ResolveUnit: %w", err)
	}

	if err := r.hydrateUnitGeneration(ctx, &unit); err != nil {
		return res, err
	}

	res.Unit = unit

	walk, err := walkUnitLineage(ctx, r.Pool, &unit)
	if err != nil {
		return res, fmt.Errorf("CitableUnitRepo.ResolveUnit walk: %w", err)
	}

	if walk.CycleDetected {
		return res, entity.ErrAnchorLineageCycle
	}
	if unit.Lifecycle == entity.UnitLifecycleActive {
		return res, nil
	}

	if len(walk.ActiveUnits) == 0 {
		return res, nil
	}

	activeIDs := make([]string, 0, len(walk.ActiveUnits))
	for i := range walk.ActiveUnits {
		activeIDs = append(activeIDs, walk.ActiveUnits[i].ID)
	}

	rows, err := r.Pool.Query(ctx, `
		SELECT `+citableUnitColumns+`
		FROM `+citableUnitLicenseFrom+`
		WHERE u.id = ANY($1::uuid[])`, activeIDs)
	if err != nil {
		return res, fmt.Errorf("CitableUnitRepo.ResolveUnit active units: %w", err)
	}
	defer rows.Close()

	byID := make(map[string]entity.CitableUnit, len(activeIDs))
	for rows.Next() {
		successor, err := scanCitableUnit(rows)
		if err != nil {
			return res, fmt.Errorf("CitableUnitRepo.ResolveUnit scan successor: %w", err)
		}

		if err := r.hydrateUnitGeneration(ctx, &successor); err != nil {
			return res, err
		}

		byID[successor.ID] = successor
	}

	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("CitableUnitRepo.ResolveUnit walk rows: %w", err)
	}

	for _, id := range activeIDs {
		successor, ok := byID[id]
		if !ok {
			return res, fmt.Errorf("CitableUnitRepo.ResolveUnit active successor %s: %w", id, entity.ErrUnitNotFound)
		}

		res.Successors = append(res.Successors, successor)
	}

	return res, nil
}

func (r *CitableUnitRepo) hydrateUnitGeneration(ctx context.Context, unit *entity.CitableUnit) error {
	if unit.GenerationRunID == nil {
		unit.Generation = nil

		return nil
	}

	identity := entity.GenerationIdentity{RunID: *unit.GenerationRunID}
	if err := r.Pool.QueryRow(ctx, `
SELECT model_id, prompt_version
FROM generation_runs
WHERE id = $1`, *unit.GenerationRunID).Scan(&identity.ModelID, &identity.PromptVersion); err != nil {
		return fmt.Errorf("CitableUnitRepo generation run %s: %w", *unit.GenerationRunID, err)
	}

	unit.Generation = &identity

	return nil
}

// AuditCounts runs the SQL half of the scheduled integrity audit: registry
// invariant violations (alerting) and dashboard-only info counts. The Go-side
// hash recompute lives in the usecase (ListActiveUnitsForHashCheck).
//
//nolint:gocognit,gocyclo,cyclop,funlen // flat sequence of independent count(*) checks, each its own guard
func (r *CitableUnitRepo) AuditCounts(ctx context.Context) (entity.CitableAuditReport, error) {
	report := entity.CitableAuditReport{UnitsByLifecycle: map[string]int64{}}

	count := func(dst *int64, name, query string) error {
		if err := r.Pool.QueryRow(ctx, query).Scan(dst); err != nil {
			return fmt.Errorf("CitableUnitRepo.AuditCounts %s: %w", name, err)
		}

		return nil
	}

	if err := count(&report.Violations.BookGone, "book_gone", `
		SELECT COUNT(*) FROM citable_units u
		LEFT JOIN books b ON b.id = u.book_id
		WHERE u.corpus = 'kitab' AND u.lifecycle = 'active'
		  AND (b.id IS NULL OR b.is_deleted)`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.SupersededNoSuccessor, "superseded_no_successor", `
		SELECT COUNT(*) FROM citable_units u
		WHERE u.lifecycle = 'superseded'
		  AND NOT EXISTS (SELECT 1 FROM citable_unit_lineage l WHERE l.predecessor_id = u.id)`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.ActiveWithSuccessor, "active_with_successor", `
		SELECT COUNT(*) FROM citable_unit_lineage l
		JOIN citable_units u ON u.id = l.predecessor_id
		WHERE u.lifecycle = 'active'`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.LineageCycle, "lineage_cycle", `
		WITH RECURSIVE reach(start_id, current_id) AS (
			SELECT predecessor_id, successor_id
			FROM citable_unit_lineage
			UNION
			SELECT reach.start_id, lineage.successor_id
			FROM reach
			JOIN citable_unit_lineage lineage ON lineage.predecessor_id = reach.current_id
		)
		SELECT COUNT(*) FROM reach WHERE start_id = current_id`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.AnchorMalformed, "anchor_malformed", `
		SELECT COUNT(*) FROM citable_units u
		LEFT JOIN quran_citable_unit_bindings qb ON qb.unit_id = u.id
		WHERE (u.corpus = 'kitab'
		       AND u.anchor <> 'kitab/' || u.book_id || '/h/' || COALESCE(u.heading_id, 0) || '/u/' || u.ordinal)
		   OR (u.corpus = 'quran'
		       AND u.anchor <> 'quran/' || qb.surah_id || ':' || qb.ayah_number || '/u/' || qb.ordinal)`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.FootnoteParent, "footnote_parent", `
		SELECT COUNT(*) FROM citable_units f
		LEFT JOIN citable_units p ON p.id = f.parent_unit_id
		WHERE f.lifecycle = 'active'
		  AND ((((f.kind = 'footnote') OR (f.kind = 'quran_quote' AND f.marker IS NOT NULL))
		        AND f.parent_unit_id IS NULL
		        AND COALESCE(f.provenance_detail->>'footnote_link', '') <> 'unlinked')
		    OR (((f.kind = 'footnote') OR (f.kind = 'quran_quote' AND f.marker IS NOT NULL))
		        AND f.parent_unit_id IS NOT NULL AND p.lifecycle <> 'active')
		    OR (NOT ((f.kind = 'footnote') OR (f.kind = 'quran_quote' AND f.marker IS NOT NULL))
		        AND f.parent_unit_id IS NOT NULL))`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.QuranBinding, "quran_binding", `
		SELECT COUNT(*)
		FROM citable_units u
		FULL JOIN quran_citable_unit_bindings qb ON qb.unit_id = u.id
		LEFT JOIN quran_ayahs a
		  ON a.surah_id = qb.surah_id AND a.ayah_number = qb.ayah_number
		LEFT JOIN quran_ayah_translations t
		  ON t.source_id = qb.translation_source_id
		 AND t.surah_id = qb.surah_id AND t.ayah_number = qb.ayah_number
		LEFT JOIN quran_ayah_transliterations x
		  ON x.source_id = qb.transliteration_source_id
		 AND x.surah_id = qb.surah_id AND x.ayah_number = qb.ayah_number
		WHERE (u.corpus = 'quran' AND qb.unit_id IS NULL)
		   OR (qb.unit_id IS NOT NULL AND (u.id IS NULL OR u.corpus <> 'quran'))
		   OR (qb.unit_id IS NOT NULL AND (
		       u.ordinal <> qb.ordinal
		       OR a.surah_id IS NULL
		       OR (qb.role = 'primary_text' AND u.kind <> 'primary_text')
		       OR (qb.role = 'translation' AND (u.kind <> 'translation' OR t.source_id IS NULL))
		       OR (qb.role = 'footnote' AND (u.kind <> 'footnote' OR t.source_id IS NULL))
		       OR (qb.role = 'transliteration' AND (u.kind <> 'transliteration' OR x.source_id IS NULL))
		       OR (u.lifecycle = 'active' AND qb.role = 'primary_text' AND u.text <> a.text_qpc_hafs)
		       OR (u.lifecycle = 'active' AND qb.role = 'translation' AND u.text <> t.text)
		       OR (u.lifecycle = 'active' AND qb.role = 'transliteration' AND u.text <> x.text)
		       OR (u.lifecycle = 'active' AND qb.role = 'primary_text'
		           AND qb.source_updated_at IS DISTINCT FROM a.updated_at)
		       OR (u.lifecycle = 'active' AND qb.role IN ('translation', 'footnote')
		           AND qb.source_updated_at IS DISTINCT FROM t.updated_at)
		       OR (u.lifecycle = 'active' AND qb.role = 'transliteration'
		           AND qb.source_updated_at IS DISTINCT FROM x.updated_at)
		   ))`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.QuranInterpretive, "quran_interpretive", `
		SELECT COUNT(*) FROM citable_units
		WHERE corpus = 'quran' AND interpretive_corpus_eligible`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.InterpretiveSafety, "interpretive_safety", `
		SELECT COUNT(*) FROM citable_units
		WHERE interpretive_retrieval_eligible
		  AND (
		      corpus = 'quran'
		      OR kind = 'quran_quote'
		      OR (
		          provenance_class IN ('editorial', 'machine')
		          AND review_status <> 'approved'
		      )
		  )`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.RAGProjectionDangling, "rag_projection_dangling", fmt.Sprintf(`
		SELECT COUNT(*)
		FROM public_book_publications publication
		JOIN books b ON b.id = publication.book_id
		JOIN book_pages page ON page.book_id = b.id AND page.is_deleted = FALSE
		LEFT JOIN book_page_edits edit
		  ON edit.book_id = page.book_id AND edit.page_id = page.page_id AND edit.status = 'published'
		WHERE b.units_derived_at IS NOT NULL
		  AND b.units_stale_at IS NULL
		  AND b.units_derivation_profile_version = %d
		  AND (
		      btrim(COALESCE(edit.content_text, page.content_text)) <> ''
		      OR btrim(regexp_replace(
		          COALESCE(edit.content_html, page.content_html), '<[^>]+>', '', 'g'
		      )) <> ''
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM citable_units unit
		      WHERE unit.book_id = page.book_id
		        AND unit.page_id = page.page_id
		        AND unit.corpus = 'kitab'
		        AND unit.content_role = 'book_page'
		        AND unit.lifecycle = 'active'
		  )`, entity.KitabUnitDerivationProfileVersion)); err != nil {
		return report, err
	}

	if err := count(&report.Violations.ApprovedMentionAnchor, "approved_mention_anchor", `
		SELECT COUNT(*) FROM knowledge_mentions
		WHERE review_status = 'approved'
		  AND (unit_id IS NULL OR unit_binding_status <> 'bound')`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.MentionUnitDangling, "mention_unit_dangling", `
		WITH RECURSIVE walk(mention_id, unit_id) AS (
		    SELECT id, unit_id
		    FROM knowledge_mentions
		    WHERE unit_binding_status = 'bound' AND unit_id IS NOT NULL
		    UNION
		    SELECT walk.mention_id, lineage.successor_id
		    FROM walk
		    JOIN citable_unit_lineage lineage ON lineage.predecessor_id = walk.unit_id
		)
		SELECT COUNT(*)
		FROM knowledge_mentions mention
		WHERE mention.unit_binding_status = 'bound'
		  AND NOT EXISTS (
		      SELECT 1
		      FROM walk
		      JOIN citable_units unit ON unit.id = walk.unit_id
		      WHERE walk.mention_id = mention.id AND unit.lifecycle = 'active'
		  )`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.MentionBindingMismatch, "mention_binding_mismatch", `
		SELECT COUNT(*)
		FROM knowledge_mentions mention
		LEFT JOIN citable_units unit ON unit.id = mention.unit_id
		WHERE mention.unit_binding_status = 'bound'
		  AND (
		      unit.id IS NULL
		      OR mention.unit_char_start IS NULL
		      OR mention.unit_char_end IS NULL
		      OR mention.unit_char_start < 0
		      OR mention.unit_char_end > char_length(unit.text)
		      OR substring(
		          unit.text
		          FROM mention.unit_char_start + 1
		          FOR mention.unit_char_end - mention.unit_char_start
		      ) <> mention.exact_quote
		      OR unit.source_document_hash IS NULL
		      OR lower(COALESCE(mention.unit_source_hash, '')) <> encode(unit.source_document_hash, 'hex')
		  )`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.CrossReferenceAnchor, "cross_reference_anchor", `
			WITH RECURSIVE kitab_points AS (
			    SELECT reference.id, point
		    FROM cross_references reference
		    CROSS JOIN LATERAL unnest(string_to_array(reference.source_anchor, '..')) point
		    WHERE reference.review_status = 'approved' AND reference.source_corpus = 'kitab'
		    UNION ALL
		    SELECT reference.id, point
		    FROM cross_references reference
		    CROSS JOIN LATERAL unnest(string_to_array(reference.target_anchor, '..')) point
		    WHERE reference.review_status = 'approved' AND reference.target_corpus = 'kitab'
			), unit_points AS (
			    SELECT id, point, split_part(point, '/', 2)::integer AS expected_book_id
			    FROM kitab_points
			    WHERE point ~ '^kitab/[1-9][0-9]*/h/[0-9]+/u/[1-9][0-9]*$'
			), unit_roots AS (
			    SELECT point.id AS reference_id, point.point, point.expected_book_id,
			           root.id AS root_id
			    FROM unit_points point
			    LEFT JOIN citable_units root ON root.anchor = point.point
			), unit_walk(
			    reference_id, point, expected_book_id, unit_id, corpus, book_id,
			    lifecycle, license_status
			) AS (
			    SELECT root.reference_id, root.point, root.expected_book_id,
			           unit.id, unit.corpus, unit.book_id, unit.lifecycle, unit.license_status
			    FROM unit_roots root
			    JOIN citable_units unit ON unit.id = root.root_id
			    UNION
			    SELECT walk.reference_id, walk.point, walk.expected_book_id,
			           successor.id, successor.corpus, successor.book_id,
			           successor.lifecycle, successor.license_status
			    FROM unit_walk walk
			    JOIN citable_unit_lineage lineage ON lineage.predecessor_id = walk.unit_id
			    JOIN citable_units successor ON successor.id = lineage.successor_id
			), unit_resolution AS (
			    SELECT root.reference_id, root.point,
			           root.root_id IS NOT NULL AS root_exists,
			           COALESCE(bool_or(
			               walk.corpus IS DISTINCT FROM 'kitab'
			               OR walk.book_id IS DISTINCT FROM root.expected_book_id
			           ), FALSE) AS crossed_boundary,
			           COALESCE(bool_or(
			               walk.lifecycle = 'active'
			               AND walk.corpus = 'kitab'
			               AND walk.book_id = root.expected_book_id
			               AND (walk.license_status IS NULL OR walk.license_status = 'permitted')
			               AND publication.book_id IS NOT NULL
			           ), FALSE) AS has_active
			    FROM unit_roots root
			    LEFT JOIN unit_walk walk
			      ON walk.reference_id = root.reference_id AND walk.point = root.point
			    LEFT JOIN public_book_publications publication
			      ON publication.book_id = root.expected_book_id
			    GROUP BY root.reference_id, root.point, root.root_id
			), invalid AS (
			    SELECT DISTINCT id
			    FROM kitab_points
			    WHERE point !~ '^kitab/[1-9][0-9]*/h/[0-9]+/u/[1-9][0-9]*$'
			      AND NOT cross_reference_anchor_point_visible(point)
			    UNION
			    SELECT reference_id
			    FROM unit_resolution
			    -- The lineage_cycle counter earlier in this audit independently makes
			    -- every cycle fail, while this set-union walk stays bounded on cycles
			    -- and repeated diamonds.
			    WHERE NOT root_exists OR crossed_boundary OR NOT has_active
			)
			SELECT COUNT(*) FROM invalid`); err != nil {
		return report, err
	}

	if err := count(&report.Info.StaleBooks, "stale_books", `
		SELECT COUNT(*) FROM books b
		WHERE b.units_derived_at IS NOT NULL
		  AND b.units_derived_at < GREATEST(
			COALESCE((SELECT MAX(updated_at) FROM book_pages WHERE book_id = b.id), 'epoch'::timestamptz),
			COALESCE((SELECT MAX(updated_at) FROM book_headings WHERE book_id = b.id), 'epoch'::timestamptz),
			COALESCE((SELECT MAX(updated_at) FROM book_page_edits WHERE book_id = b.id AND status = 'published'), 'epoch'::timestamptz))`); err != nil {
		return report, err
	}

	if err := count(&report.Info.StaleQuranSurahs, "stale_quran_surahs", `
		SELECT COUNT(*) FROM quran_surahs
		WHERE units_derived_at IS NOT NULL AND units_stale_at IS NOT NULL`); err != nil {
		return report, err
	}

	legacy := []struct {
		dst   *int64
		name  string
		table string
	}{
		{&report.Info.LegacyQuranBookReferences, "legacy_quran_book_references", "quran_book_references"},
		{&report.Info.LegacyKnowledgeMentions, "legacy_knowledge_mentions", "knowledge_mentions"},
		{&report.Info.LegacyKnowledgeSourceSpans, "legacy_knowledge_source_spans", "knowledge_source_spans"},
		{&report.Info.LegacyKnowledgeRejections, "legacy_knowledge_rejections", "knowledge_extraction_rejections"},
	}
	for _, l := range legacy {
		// FK CASCADE only fires on hard deletes; soft-tombstoned pages and
		// headings (E4 importer) leave these rows dangling — B-3 owns the fix.
		query := fmt.Sprintf(`
			SELECT COUNT(*) FROM %s r
			LEFT JOIN book_pages bp ON bp.book_id = r.book_id AND bp.page_id = r.page_id
			LEFT JOIN book_headings bh ON r.heading_id IS NOT NULL
				AND bh.book_id = r.book_id AND bh.heading_id = r.heading_id
			WHERE COALESCE(bp.is_deleted, TRUE)
			   OR (r.heading_id IS NOT NULL AND COALESCE(bh.is_deleted, TRUE))`, l.table)
		if err := count(l.dst, l.name, query); err != nil {
			return report, err
		}
	}

	rows, err := r.Pool.Query(ctx,
		`SELECT lifecycle, COUNT(*) FROM citable_units GROUP BY lifecycle`)
	if err != nil {
		return report, fmt.Errorf("CitableUnitRepo.AuditCounts inventory: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			lifecycle string
			n         int64
		)
		if err := rows.Scan(&lifecycle, &n); err != nil {
			return report, fmt.Errorf("CitableUnitRepo.AuditCounts scan inventory: %w", err)
		}

		report.UnitsByLifecycle[lifecycle] = n
	}

	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("CitableUnitRepo.AuditCounts inventory rows: %w", err)
	}

	return report, nil
}

// ListActiveUnitsForHashCheck returns the lean projection the usecase needs to
// recompute content hashes and normalized text (foreign-write tripwire).
// Full-scan is fine at pilot scale; sample at K-1 scale before industrializing.
func (r *CitableUnitRepo) ListActiveUnitsForHashCheck(ctx context.Context) ([]entity.CitableUnit, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT id, kind, marker, text, text_normalized, normalization_version, content_hash
		FROM citable_units
		WHERE lifecycle = 'active'
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("CitableUnitRepo.ListActiveUnitsForHashCheck: %w", err)
	}
	defer rows.Close()

	units := make([]entity.CitableUnit, 0)

	for rows.Next() {
		var u entity.CitableUnit
		if err := rows.Scan(&u.ID, &u.Kind, &u.Marker, &u.Text, &u.TextNormalized,
			&u.NormalizationVersion, &u.ContentHash); err != nil {
			return nil, fmt.Errorf("CitableUnitRepo.ListActiveUnitsForHashCheck scan: %w", err)
		}

		units = append(units, u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("CitableUnitRepo.ListActiveUnitsForHashCheck rows: %w", err)
	}

	return units, nil
}

// ListActiveUnitsForHashCheckPage is the catalog-scale audit path. Keyset
// pagination keeps each hourly/weekly integrity pass at bounded memory even
// after every published kitab has been materialized.
//
//nolint:wsl_v5 // tight scan loop keeps the bounded audit projection readable
func (r *CitableUnitRepo) ListActiveUnitsForHashCheckPage(
	ctx context.Context,
	afterID string,
	limit int,
) ([]entity.CitableUnit, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT id, kind, marker, text, text_normalized, normalization_version, content_hash
		FROM citable_units
		WHERE lifecycle = 'active' AND ($1 = '' OR id > $1::uuid)
		ORDER BY id
		LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("CitableUnitRepo.ListActiveUnitsForHashCheckPage: %w", err)
	}
	defer rows.Close()

	units := make([]entity.CitableUnit, 0, limit)

	for rows.Next() {
		var u entity.CitableUnit
		if err := rows.Scan(&u.ID, &u.Kind, &u.Marker, &u.Text, &u.TextNormalized,
			&u.NormalizationVersion, &u.ContentHash); err != nil {
			return nil, fmt.Errorf("CitableUnitRepo.ListActiveUnitsForHashCheckPage scan: %w", err)
		}
		units = append(units, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("CitableUnitRepo.ListActiveUnitsForHashCheckPage rows: %w", err)
	}

	return units, nil
}
