package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

// registryAdvisoryLockClass namespaces pg_advisory_xact_lock(class, book_id)
// for reconcile serialization; distinct from the backfill runner's lock space.
const registryAdvisoryLockClass = 71001

// registryWriterGUC must match citable_registry_guard() in the migration; the
// guard rejects any DML outside a transaction that sets it.
const registryWriterGUC = "SET LOCAL surau.registry_writer = 'unit-service'"

// lineageWalkDepthCap bounds the resolve walk; lineage is a DAG, the cap only
// guards against pathological data.
const lineageWalkDepthCap = 32

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
	src := entity.BookUnitSource{BookID: bookID}

	var major, minor int

	err := r.Pool.QueryRow(ctx, `
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

	rows, err := r.Pool.Query(ctx, `
		SELECT bp.page_id,
		       COALESCE(pe.content_html, bp.content_html),
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
		if err := rows.Scan(&p.PageID, &p.ContentHTML, &p.HasPublishedEdit, &p.EditActorID); err != nil {
			return src, fmt.Errorf("CitableUnitRepo.LoadBookSource scan page: %w", err)
		}

		src.Pages = append(src.Pages, p)
	}

	if err := rows.Err(); err != nil {
		return src, fmt.Errorf("CitableUnitRepo.LoadBookSource pages rows: %w", err)
	}

	hrows, err := r.Pool.Query(ctx, `
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

	return src, nil
}

// Snapshot loads the planner's registry read model for one book. All reads run
// inside ONE repeatable-read snapshot so the Active list, ordinal high-water
// marks, and the fingerprint are mutually consistent: plan.BasedOn then matches
// the data the plan was built from, and any concurrent reconcile that commits
// afterwards is caught at apply time as a retryable ErrUnitReconcileConflict
// (rather than defeating the check with a fingerprint read from a later state).
//
//nolint:gocyclo,cyclop,funlen // linear: repeatable-read tx + active-units scan loop + ordinal/id scan loop + fingerprint, each guarded
func (r *CitableUnitRepo) Snapshot(ctx context.Context, bookID int) (entity.UnitRegistrySnapshot, error) {
	snap := entity.UnitRegistrySnapshot{
		MaxOrdinalByScope: map[int]int{},
		ExistingIDs:       map[string]struct{}{},
	}

	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return snap, fmt.Errorf("CitableUnitRepo.Snapshot begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	rows, err := tx.Query(ctx, `
		SELECT id, heading_id, page_id, kind, ordinal, position, parent_unit_id,
		       marker, content_hash, occurrence, lifecycle
		FROM citable_units
		WHERE book_id = $1 AND lifecycle = 'active'`, bookID)
	if err != nil {
		return snap, fmt.Errorf("CitableUnitRepo.Snapshot active: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		u := entity.CitableUnit{Corpus: entity.UnitCorpusKitab, BookID: bookID}
		if err := rows.Scan(&u.ID, &u.HeadingID, &u.PageID, &u.Kind, &u.Ordinal, &u.Position,
			&u.ParentUnitID, &u.Marker, &u.ContentHash, &u.Occurrence, &u.Lifecycle); err != nil {
			return snap, fmt.Errorf("CitableUnitRepo.Snapshot scan: %w", err)
		}

		snap.Active = append(snap.Active, u)
	}

	if err := rows.Err(); err != nil {
		return snap, fmt.Errorf("CitableUnitRepo.Snapshot active rows: %w", err)
	}

	rows.Close()

	orows, err := tx.Query(ctx, `
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

	snap.Fingerprint, err = registryFingerprint(ctx, tx, bookID)
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

// ApplyReconcile applies one book plan atomically: guard GUC, per-book
// advisory lock, optimistic fingerprint check, batched mints (parents first) /
// locator updates / retires / lineage edges, units_derived_at stamp, then
// in-transaction invariant asserts. Any violated invariant rolls everything
// back — the registry never drifts partially.
//
//nolint:gocognit,gocyclo,cyclop,funlen // linear batch pipeline: guard -> lock -> fingerprint -> queue mints/updates/retires/edges -> assert; each stage is a distinct guard
func (r *CitableUnitRepo) ApplyReconcile(ctx context.Context, plan *entity.UnitReconcilePlan) error {
	tx, err := r.Pool.Begin(ctx)
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

	fp, err := registryFingerprint(ctx, tx, plan.BookID)
	if err != nil {
		return err
	}

	if fp != plan.BasedOn {
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
				normalization_version, content_hash, occurrence, language,
				provenance_class, provenance_detail, license_status, lifecycle
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,'active')`,
			u.ID, u.Corpus, u.BookID, u.HeadingID, u.PageID, u.Kind, u.Ordinal, u.Position,
			u.ParentUnitID, u.Anchor, u.Marker, u.Text, u.HTML, u.TextNormalized,
			u.NormalizationVersion, u.ContentHash, u.Occurrence, u.Language,
			u.ProvenanceClass, detail, u.LicenseStatus)

		mintCount++

		return nil
	}
	// Parent FK: body units first, footnotes (the only parented kind) after.
	for i := range plan.Mints {
		if plan.Mints[i].ParentUnitID == nil {
			if err := queueMint(&plan.Mints[i]); err != nil {
				return err
			}
		}
	}

	for i := range plan.Mints {
		if plan.Mints[i].ParentUnitID != nil {
			if err := queueMint(&plan.Mints[i]); err != nil {
				return err
			}
		}
	}

	for _, up := range plan.Updates {
		// $5 (footnote_link) is NULL for non-footnotes and for footnotes whose
		// linkage did not change; when set it refreshes provenance_detail so a
		// now-unlinked footnote's label matches its NULL parent (audit fix).
		batch.Queue(`
			UPDATE citable_units
			SET position = $2,
			    page_id = $3,
			    parent_unit_id = $4,
			    provenance_detail = CASE
			        WHEN $5::text IS NULL THEN provenance_detail
			        ELSE jsonb_set(COALESCE(provenance_detail, '{}'::jsonb), '{footnote_link}', to_jsonb($5::text))
			    END,
			    updated_at = now()
			WHERE id = $1 AND lifecycle = 'active'`,
			up.ID, up.Position, up.PageID, up.ParentUnitID, up.FootnoteLink)
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

	batch.Queue(`UPDATE books SET units_derived_at = $2 WHERE id = $1`, plan.BookID, plan.LoadedAt)

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

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("CitableUnitRepo.ApplyReconcile commit: %w", err)
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
	id, corpus, book_id, heading_id, page_id, kind, ordinal, position,
	parent_unit_id, anchor, marker, text, html, text_normalized,
	normalization_version, content_hash, occurrence, language,
	provenance_class, provenance_detail, license_status, lifecycle,
	retired_at, created_at, updated_at`

func scanCitableUnit(row pgx.Row) (entity.CitableUnit, error) {
	var (
		u      entity.CitableUnit
		detail []byte
	)

	err := row.Scan(&u.ID, &u.Corpus, &u.BookID, &u.HeadingID, &u.PageID, &u.Kind, &u.Ordinal,
		&u.Position, &u.ParentUnitID, &u.Anchor, &u.Marker, &u.Text, &u.HTML, &u.TextNormalized,
		&u.NormalizationVersion, &u.ContentHash, &u.Occurrence, &u.Language,
		&u.ProvenanceClass, &detail, &u.LicenseStatus, &u.Lifecycle,
		&u.RetiredAt, &u.CreatedAt, &u.UpdatedAt)
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
func (r *CitableUnitRepo) ResolveUnit(ctx context.Context, unitID string) (entity.UnitResolution, error) {
	var res entity.UnitResolution

	unit, err := scanCitableUnit(r.Pool.QueryRow(ctx,
		`SELECT `+citableUnitColumns+` FROM citable_units WHERE id = $1`, unitID))
	if errors.Is(err, pgx.ErrNoRows) {
		return res, entity.ErrUnitNotFound
	}

	if err != nil {
		return res, fmt.Errorf("CitableUnitRepo.ResolveUnit: %w", err)
	}

	res.Unit = unit
	if unit.Lifecycle == entity.UnitLifecycleActive {
		return res, nil
	}

	rows, err := r.Pool.Query(ctx, `
		WITH RECURSIVE walk AS (
			SELECT l.successor_id, 1 AS depth
			FROM citable_unit_lineage l
			WHERE l.predecessor_id = $1
			UNION
			SELECT l.successor_id, w.depth + 1
			FROM walk w
			JOIN citable_unit_lineage l ON l.predecessor_id = w.successor_id
			WHERE w.depth < $2
		)
		SELECT `+citableUnitColumns+`
		FROM citable_units u
		WHERE u.id IN (SELECT successor_id FROM walk) AND u.lifecycle = 'active'
		ORDER BY u.anchor`, unitID, lineageWalkDepthCap)
	if err != nil {
		return res, fmt.Errorf("CitableUnitRepo.ResolveUnit walk: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		successor, err := scanCitableUnit(rows)
		if err != nil {
			return res, fmt.Errorf("CitableUnitRepo.ResolveUnit scan successor: %w", err)
		}

		res.Successors = append(res.Successors, successor)
	}

	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("CitableUnitRepo.ResolveUnit walk rows: %w", err)
	}

	return res, nil
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

	if err := count(&report.Violations.AnchorMalformed, "anchor_malformed", `
		SELECT COUNT(*) FROM citable_units
		WHERE corpus = 'kitab'
		  AND anchor <> 'kitab/' || book_id || '/h/' || COALESCE(heading_id, 0) || '/u/' || ordinal`); err != nil {
		return report, err
	}

	if err := count(&report.Violations.FootnoteParent, "footnote_parent", `
		SELECT COUNT(*) FROM citable_units f
		LEFT JOIN citable_units p ON p.id = f.parent_unit_id
		WHERE f.lifecycle = 'active'
		  AND ((f.kind = 'footnote' AND f.parent_unit_id IS NULL
		        AND COALESCE(f.provenance_detail->>'footnote_link', '') <> 'unlinked')
		    OR (f.kind = 'footnote' AND f.parent_unit_id IS NOT NULL AND p.lifecycle <> 'active')
		    OR (f.kind <> 'footnote' AND f.parent_unit_id IS NOT NULL))`); err != nil {
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
