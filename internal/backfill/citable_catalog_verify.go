package backfill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"math/bits"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Masterminds/squirrel"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/readerutil"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	verificationSearchBookLimit        = 10
	verificationSearchResultLimit      = 20
	searchP95TargetMilliseconds        = 400
	microsecondsPerMillisecond         = 1000
	verificationSearchPercentile       = 95
	verificationSearchPercentileScale  = 100
	bitmapWordBits                     = 64
	canonicalVerificationPageBatchSize = 128
)

// CitableCatalogVerification is the machine-readable K-1 acceptance evidence.
// Counts use the raw published denominator; public visibility remains a
// separate B-4 concern.
type CitableCatalogVerification struct {
	VerifiedAt                 time.Time                     `json:"verified_at"`
	ProfileVersion             int                           `json:"profile_version"`
	TargetBooks                int64                         `json:"target_books"`
	MaterializedBooks          int64                         `json:"materialized_books"`
	MissingBooks               int64                         `json:"missing_books"`
	StaleBooks                 int64                         `json:"stale_books"`
	UncoveredPages             int64                         `json:"uncovered_pages"`
	TargetAssets               int64                         `json:"target_assets"`
	MaterializedAssets         int64                         `json:"materialized_assets"`
	UncoveredAssets            int64                         `json:"uncovered_assets"`
	CanonicalDocuments         int64                         `json:"canonical_documents"`
	CanonicalCoveredRunes      int64                         `json:"canonical_covered_runes"`
	UncoveredCanonicalRunes    int64                         `json:"uncovered_canonical_runes"`
	UnexpectedCanonicalSpans   int64                         `json:"unexpected_canonical_spans"`
	ParityTargetBooks          int64                         `json:"parity_target_books"`
	ParityVerifiedBooks        int64                         `json:"parity_verified_books"`
	ParityMismatches           int64                         `json:"parity_mismatches"`
	ParitySamplesMissing       int64                         `json:"parity_samples_missing"`
	ParityDenialMismatches     int64                         `json:"parity_denial_mismatches"`
	ParityRequestMismatches    int64                         `json:"parity_request_mismatches"`
	ParityLocatorMismatches    int64                         `json:"parity_locator_mismatches"`
	ParityDiagnostics          []string                      `json:"parity_diagnostics,omitempty"`
	UnitAnchorsUnresolved      int64                         `json:"unit_anchors_unresolved"`
	DeterminismVerifiedBooks   int64                         `json:"determinism_verified_books"`
	QueuePending               int64                         `json:"queue_pending"`
	LegacyUnknownAssetsSkipped int64                         `json:"legacy_unknown_assets_skipped"`
	MentionBindings            map[string]int64              `json:"mention_bindings"`
	SearchSamples              int                           `json:"search_samples"`
	SearchP95Milliseconds      float64                       `json:"search_p95_ms"`
	SearchWithinTarget         bool                          `json:"search_within_target"`
	Audit                      entity.CitableAuditViolations `json:"audit"`
	Passed                     bool                          `json:"passed"`
}

// VerifyCitableCatalog recomputes every K-1 gate directly from PostgreSQL. It
// returns a report even when acceptance fails; only query/integrity execution
// errors are returned as Go errors.
func VerifyCitableCatalog(ctx context.Context, pool *pgxpool.Pool) (CitableCatalogVerification, error) {
	return verifyCitableCatalog(ctx, pool, nil)
}

// VerifyCitableCatalogWithProgress is the operational CLI variant. Progress
// is deliberately low-cardinality: it reveals which bounded acceptance phase
// is running without logging a book ID or any corpus content.
func VerifyCitableCatalogWithProgress(
	ctx context.Context,
	pool *pgxpool.Pool,
	progress func(string),
) (CitableCatalogVerification, error) {
	return verifyCitableCatalog(ctx, pool, progress)
}

//nolint:cyclop,funlen,gocognit,gocyclo // The acceptance report intentionally assembles all independent K-1 gates in one transaction-free read pass.
func verifyCitableCatalog(
	ctx context.Context,
	pool *pgxpool.Pool,
	progress func(string),
) (CitableCatalogVerification, error) {
	report := CitableCatalogVerification{
		VerifiedAt:      time.Now().UTC(),
		ProfileVersion:  entity.KitabUnitDerivationProfileVersion,
		MentionBindings: map[string]int64{},
	}

	verificationProgress(progress, "coverage")

	err := pool.QueryRow(ctx, `
SELECT COUNT(*)::bigint,
       COUNT(*) FILTER (
           WHERE b.units_derived_at IS NOT NULL
             AND b.units_stale_at IS NULL
             AND b.units_derivation_profile_version = $1
             AND EXISTS (
                 SELECT 1 FROM citable_units unit
                 WHERE unit.book_id = b.id
                   AND unit.corpus = 'kitab'
                   AND unit.content_role = 'book_page'
                   AND unit.lifecycle = 'active'
             )
       )::bigint,
       COUNT(*) FILTER (WHERE b.units_derived_at IS NULL)::bigint,
       COUNT(*) FILTER (
           WHERE b.units_stale_at IS NOT NULL
              OR b.units_derivation_profile_version IS DISTINCT FROM $1
       )::bigint
FROM book_publications publication
JOIN books b ON b.id = publication.book_id
WHERE publication.status = 'published' AND b.is_deleted = FALSE
  AND ($2::integer[] IS NULL OR b.id = ANY($2))`, report.ProfileVersion, CitableCatalogBookIDs).
		Scan(&report.TargetBooks, &report.MaterializedBooks, &report.MissingBooks, &report.StaleBooks)
	if err != nil {
		return report, fmt.Errorf("verify citable catalog coverage: %w", err)
	}

	if err := pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM book_publications publication
JOIN books b ON b.id = publication.book_id
JOIN book_pages page ON page.book_id = b.id AND page.is_deleted = FALSE
LEFT JOIN book_page_edits edit
  ON edit.book_id = page.book_id AND edit.page_id = page.page_id AND edit.status = 'published'
WHERE publication.status = 'published'
  AND b.is_deleted = FALSE
  AND ($1::integer[] IS NULL OR b.id = ANY($1))
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
  )`, CitableCatalogBookIDs).Scan(&report.UncoveredPages); err != nil {
		return report, fmt.Errorf("verify citable catalog page coverage: %w", err)
	}

	if err := pool.QueryRow(ctx, `
WITH assets AS (
    SELECT translation.book_id,
           translation.heading_id,
           translation.lang,
           'section_translation'::text AS content_role
    FROM section_translations translation
    JOIN book_publications publication ON publication.book_id = translation.book_id
    JOIN books b ON b.id = translation.book_id
    WHERE publication.status = 'published'
      AND b.is_deleted = FALSE
      AND ($1::integer[] IS NULL OR b.id = ANY($1))
      AND translation.is_deleted = FALSE
      AND translation.provenance_class <> 'legacy_unknown'
	  AND (
	      translation.lang = 'ar'
	      OR EXISTS (
	          SELECT 1 FROM book_production_projects project
	          WHERE project.book_id = translation.book_id
	            AND project.lang = translation.lang
	            AND project.publication_status = 'published'
	            AND project.workflow_status <> 'archived'
	      )
	  )
    UNION ALL
    SELECT summary.book_id,
           summary.heading_id,
           summary.lang,
           'heading_summary'::text AS content_role
    FROM book_heading_summaries summary
    JOIN book_publications publication ON publication.book_id = summary.book_id
    JOIN books b ON b.id = summary.book_id
    WHERE publication.status = 'published'
      AND b.is_deleted = FALSE
      AND ($1::integer[] IS NULL OR b.id = ANY($1))
      AND summary.is_deleted = FALSE
      AND summary.provenance_class <> 'legacy_unknown'
	  AND (
	      summary.lang = 'ar'
	      OR EXISTS (
	          SELECT 1 FROM book_production_projects project
	          WHERE project.book_id = summary.book_id
	            AND project.lang = summary.lang
	            AND project.publication_status = 'published'
	            AND project.workflow_status <> 'archived'
	      )
	  )
)
SELECT COUNT(*)::bigint,
       COUNT(*) FILTER (
           WHERE EXISTS (
               SELECT 1 FROM citable_units unit
               WHERE unit.book_id = assets.book_id
                 AND unit.heading_id = assets.heading_id
                 AND unit.language = assets.lang
                 AND unit.content_role = assets.content_role
                 AND unit.lifecycle = 'active'
           )
       )::bigint
FROM assets`, CitableCatalogBookIDs).Scan(&report.TargetAssets, &report.MaterializedAssets); err != nil {
		return report, fmt.Errorf("verify citable catalog asset coverage: %w", err)
	}

	report.UncoveredAssets = report.TargetAssets - report.MaterializedAssets

	pg := &postgres.Postgres{
		Pool:    pool,
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}
	unitRepo := persistent.NewCitableUnitRepo(pg)

	verificationProgress(progress, "canonical_rune_coverage")
	report.CanonicalDocuments, report.CanonicalCoveredRunes,
		report.UncoveredCanonicalRunes, report.UnexpectedCanonicalSpans, err = verifyCanonicalUnitCoverage(ctx, pool, unitRepo)
	if err != nil {
		return report, err
	}

	verificationProgress(progress, "determinism_evidence")
	report.DeterminismVerifiedBooks, err = verifyCatalogDeterminismEvidence(ctx, pool, unitRepo)
	if err != nil {
		return report, err
	}

	if err := pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM citable_unit_catalog_queue queued
JOIN book_publications publication ON publication.book_id = queued.book_id
JOIN books b ON b.id = queued.book_id
WHERE publication.status = 'published'
  AND b.is_deleted = FALSE
  AND ($1::integer[] IS NULL OR b.id = ANY($1))
  AND queued.status <> 'completed'`, CitableCatalogBookIDs).Scan(&report.QueuePending); err != nil {
		return report, fmt.Errorf("verify citable catalog queue: %w", err)
	}

	if err := pool.QueryRow(ctx, `
SELECT (
    SELECT COUNT(*)
    FROM section_translations asset
    JOIN book_publications publication ON publication.book_id = asset.book_id
    JOIN books b ON b.id = asset.book_id
    WHERE publication.status = 'published'
      AND b.is_deleted = FALSE
      AND ($1::integer[] IS NULL OR b.id = ANY($1))
      AND asset.is_deleted = FALSE
      AND asset.provenance_class = 'legacy_unknown'
	  AND (
	      asset.lang = 'ar'
	      OR EXISTS (
	          SELECT 1 FROM book_production_projects project
	          WHERE project.book_id = asset.book_id
	            AND project.lang = asset.lang
	            AND project.publication_status = 'published'
	            AND project.workflow_status <> 'archived'
	      )
	  )
) + (
    SELECT COUNT(*)
    FROM book_heading_summaries asset
    JOIN book_publications publication ON publication.book_id = asset.book_id
    JOIN books b ON b.id = asset.book_id
    WHERE publication.status = 'published'
      AND b.is_deleted = FALSE
      AND ($1::integer[] IS NULL OR b.id = ANY($1))
      AND asset.is_deleted = FALSE
      AND asset.provenance_class = 'legacy_unknown'
	  AND (
	      asset.lang = 'ar'
	      OR EXISTS (
	          SELECT 1 FROM book_production_projects project
	          WHERE project.book_id = asset.book_id
	            AND project.lang = asset.lang
	            AND project.publication_status = 'published'
	            AND project.workflow_status <> 'archived'
	      )
	  )
)`, CitableCatalogBookIDs).Scan(&report.LegacyUnknownAssetsSkipped); err != nil {
		return report, fmt.Errorf("verify citable catalog legacy assets: %w", err)
	}

	rows, err := pool.Query(ctx, `
SELECT unit_binding_status, COUNT(*)
FROM knowledge_mentions
WHERE ($1::integer[] IS NULL OR book_id = ANY($1))
GROUP BY unit_binding_status
ORDER BY unit_binding_status`, CitableCatalogBookIDs)
	if err != nil {
		return report, fmt.Errorf("verify citable catalog mention bindings: %w", err)
	}

	for rows.Next() {
		var (
			status string
			count  int64
		)

		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()

			return report, fmt.Errorf("verify citable catalog mention bindings scan: %w", err)
		}

		report.MentionBindings[status] = count
	}

	if err := rows.Err(); err != nil {
		rows.Close()

		return report, fmt.Errorf("verify citable catalog mention bindings rows: %w", err)
	}

	rows.Close()

	registry := unitregistry.New(unitRepo)

	verificationProgress(progress, "integrity_audit")
	audit, err := registry.AuditPass(ctx)
	if err != nil {
		return report, fmt.Errorf("verify citable catalog audit: %w", err)
	}

	report.Audit = audit.Violations

	verificationProgress(progress, "retrieval_performance")
	bookIDs, err := verificationSearchBookIDs(ctx, pool)
	if err != nil {
		return report, err
	}

	report.SearchSamples, report.SearchP95Milliseconds, err = verifyUnitSearchP95(
		ctx,
		persistent.NewBookRAGRepo(pg),
		bookIDs,
	)
	if err != nil {
		return report, err
	}

	report.SearchWithinTarget = report.SearchSamples > 0 &&
		report.SearchP95Milliseconds < searchP95TargetMilliseconds

	verificationProgress(progress, "book_rag_parity")

	parity, parityErr := verifyFullCatalogBookRAGParity(
		ctx,
		pool,
		persistent.NewBookRAGRepo(pg),
		persistent.NewAnchorRepo(pg),
	)
	if parityErr != nil {
		return report, parityErr
	}

	report.ParityTargetBooks = parity.target
	report.ParityVerifiedBooks = parity.verified
	report.ParityMismatches = parity.mismatches
	report.ParitySamplesMissing = parity.samplesMissing
	report.ParityDenialMismatches = parity.denialMismatches
	report.ParityRequestMismatches = parity.requestMismatches
	report.ParityLocatorMismatches = parity.locatorMismatches
	report.ParityDiagnostics = parity.diagnostics
	report.UnitAnchorsUnresolved = parity.unresolved

	report.Passed = report.TargetBooks > 0 &&
		report.MaterializedBooks == report.TargetBooks &&
		report.MissingBooks == 0 && report.StaleBooks == 0 && report.UncoveredPages == 0 &&
		report.MaterializedAssets == report.TargetAssets && report.UncoveredAssets == 0 &&
		report.UncoveredCanonicalRunes == 0 && report.UnexpectedCanonicalSpans == 0 &&
		report.DeterminismVerifiedBooks == report.TargetBooks && report.QueuePending == 0 &&
		report.ParityTargetBooks == report.TargetBooks && report.ParityVerifiedBooks == report.ParityTargetBooks &&
		report.ParityMismatches == 0 &&
		report.UnitAnchorsUnresolved == 0 &&
		report.SearchWithinTarget &&
		citableAuditViolationTotal(report.Audit) == 0

	verificationProgress(progress, "complete")

	return report, nil
}

func verificationProgress(progress func(string), phase string) {
	if progress != nil {
		progress(phase)
	}
}

//nolint:gocyclo,cyclop,funlen // every evidence field is an independent fail-closed gate
func verifyCatalogDeterminismEvidence(
	ctx context.Context,
	pool *pgxpool.Pool,
	unitRepo *persistent.CitableUnitRepo,
) (int64, error) {
	type evidenceRow struct {
		bookID                              int
		catalogCompleted, rederiveCompleted bool
		catalogVersion, rederiveVersion     int
		catalogSource, catalogRegistry      []byte
		rederiveSource, rederiveRegistry    []byte
	}

	rows, err := pool.Query(ctx, `
SELECT b.id,
       COALESCE(catalog.status = 'completed', FALSE),
       catalog.source_fingerprint,
       catalog.result_checksum,
       COALESCE(catalog.checksum_version, 0),
       COALESCE(rederive.status = 'completed', FALSE),
       rederive.source_fingerprint,
       rederive.result_checksum,
       COALESCE(rederive.checksum_version, 0)
FROM book_publications publication
JOIN books b ON b.id = publication.book_id
LEFT JOIN citable_unit_catalog_queue catalog
  ON catalog.job_name = $1 AND catalog.book_id = b.id
LEFT JOIN citable_unit_catalog_queue rederive
  ON rederive.job_name = $2 AND rederive.book_id = b.id
WHERE publication.status = 'published'
  AND b.is_deleted = FALSE
  AND ($3::integer[] IS NULL OR b.id = ANY($3))
ORDER BY b.id`, citableCatalogJobName, citableCatalogRederiveJobName, CitableCatalogBookIDs)
	if err != nil {
		return 0, fmt.Errorf("verify citable catalog determinism evidence: %w", err)
	}

	evidence := make([]evidenceRow, 0)

	for rows.Next() {
		var item evidenceRow
		if err := rows.Scan(
			&item.bookID,
			&item.catalogCompleted,
			&item.catalogSource,
			&item.catalogRegistry,
			&item.catalogVersion,
			&item.rederiveCompleted,
			&item.rederiveSource,
			&item.rederiveRegistry,
			&item.rederiveVersion,
		); err != nil {
			rows.Close()

			return 0, fmt.Errorf("verify citable catalog determinism evidence scan: %w", err)
		}

		evidence = append(evidence, item)
	}

	if err := rows.Err(); err != nil {
		rows.Close()

		return 0, fmt.Errorf("verify citable catalog determinism evidence rows: %w", err)
	}

	rows.Close()

	var verified int64

	for i := range evidence {
		item := &evidence[i]
		if !item.catalogCompleted || !item.rederiveCompleted ||
			item.catalogVersion != persistent.CitableCatalogChecksumVersion ||
			item.rederiveVersion != persistent.CitableCatalogChecksumVersion {
			continue
		}

		liveSource, liveRegistry, err := unitRepo.CatalogEvidenceChecksums(ctx, item.bookID)
		if err != nil {
			return verified, fmt.Errorf("verify citable catalog live checksum book %d: %w", item.bookID, err)
		}

		if bytes.Equal(item.catalogSource, liveSource[:]) && bytes.Equal(item.rederiveSource, liveSource[:]) &&
			bytes.Equal(item.catalogRegistry, liveRegistry[:]) &&
			bytes.Equal(item.rederiveRegistry, liveRegistry[:]) {
			verified++
		}
	}

	return verified, nil
}

type canonicalSourceLoader interface {
	LoadBookSource(ctx context.Context, bookID int) (entity.BookUnitSource, error)
}

type canonicalSpan struct {
	document string
	span     string
	start    int
	end      int
}

// canonicalDocumentCoverage keeps one bit per source rune instead of one map
// entry per rune. The catalog contains multi-million-rune books; a
// map[int]bool needs tens of bytes per rune and made the acceptance verifier
// compete with the public API for memory/CPU. Two dense bitmaps preserve the
// exact same independent source-vs-registry proof at roughly 1/4 byte per
// rune.
type canonicalDocumentCoverage struct {
	runeCount int
	required  []uint64
	actual    []uint64
}

type canonicalCoverage map[string]*canonicalDocumentCoverage

// verifyCanonicalUnitCoverage re-derives the current source and compares the
// exact rune-span multiset to active registry rows. This proves more than
// "one unit exists per page": a missing paragraph, lost HTML fallback,
// overlap with the wrong text, stale document hash, or extra active span all
// fail the acceptance report.
//
//nolint:cyclop,funlen,gocognit,gocyclo // Exact source-to-registry multiset comparison is intentionally kept as one auditable verifier.
func verifyCanonicalUnitCoverage(
	ctx context.Context,
	pool *pgxpool.Pool,
	loader canonicalSourceLoader,
) (documents, coveredRunes, uncoveredRunes, unexpectedSpans int64, err error) {
	rows, err := pool.Query(ctx, `
SELECT b.id
FROM book_publications publication
JOIN books b ON b.id = publication.book_id
WHERE publication.status = 'published' AND b.is_deleted = FALSE
  AND ($1::integer[] IS NULL OR b.id = ANY($1))
ORDER BY b.id`, CitableCatalogBookIDs)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("verify canonical coverage books: %w", err)
	}

	bookIDs := make([]int, 0)

	for rows.Next() {
		var bookID int
		if scanErr := rows.Scan(&bookID); scanErr != nil {
			rows.Close()

			return 0, 0, 0, 0, fmt.Errorf("verify canonical coverage books scan: %w", scanErr)
		}

		bookIDs = append(bookIDs, bookID)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()

		return 0, 0, 0, 0, fmt.Errorf("verify canonical coverage books rows: %w", rowsErr)
	}

	rows.Close()

	for _, bookID := range bookIDs {
		source, loadErr := loader.LoadBookSource(ctx, bookID)
		if loadErr != nil {
			return documents, coveredRunes, uncoveredRunes, unexpectedSpans,
				fmt.Errorf("verify canonical coverage load book %d: %w", bookID, loadErr)
		}

		for start := 0; start < len(source.Pages); start += canonicalVerificationPageBatchSize {
			end := min(start+canonicalVerificationPageBatchSize, len(source.Pages))
			batch := source
			batch.Pages = source.Pages[start:end]
			batch.Assets = nil

			batchDocuments, batchCovered, batchUncovered, batchUnexpected, batchErr := verifyCanonicalSourceBatch(ctx, pool, bookID, &batch, true)
			if batchErr != nil {
				return documents, coveredRunes, uncoveredRunes, unexpectedSpans, batchErr
			}

			documents += batchDocuments
			coveredRunes += batchCovered
			uncoveredRunes += batchUncovered
			unexpectedSpans += batchUnexpected
		}

		pageIDs := make([]int, 0, len(source.Pages))
		for i := range source.Pages {
			pageIDs = append(pageIDs, source.Pages[i].PageID)
		}

		var strayPageUnits int64
		if err := pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM citable_units
WHERE book_id = $1 AND corpus = 'kitab' AND lifecycle = 'active'
  AND content_role = 'book_page'
  AND (page_id IS NULL OR NOT (page_id = ANY($2)))`, bookID, pageIDs).Scan(&strayPageUnits); err != nil {
			return documents, coveredRunes, uncoveredRunes, unexpectedSpans,
				fmt.Errorf("verify canonical coverage stray pages book %d: %w", bookID, err)
		}

		unexpectedSpans += strayPageUnits

		batch := source
		batch.Pages = nil

		batchDocuments, batchCovered, batchUncovered, batchUnexpected, batchErr := verifyCanonicalSourceBatch(ctx, pool, bookID, &batch, false)
		if batchErr != nil {
			return documents, coveredRunes, uncoveredRunes, unexpectedSpans, batchErr
		}

		documents += batchDocuments
		coveredRunes += batchCovered
		uncoveredRunes += batchUncovered
		unexpectedSpans += batchUnexpected
	}

	return documents, coveredRunes, uncoveredRunes, unexpectedSpans, nil
}

// verifyCanonicalSourceBatch preserves the exact source-to-registry proof while
// bounding the expensive parser/deriver state. In particular, a large Shamela
// book no longer keeps every DerivedUnit and every span key live at once.
//
//nolint:cyclop,funlen,gocognit,gocyclo // exact row validation keeps every fail-closed check in one bounded scan
func verifyCanonicalSourceBatch(
	ctx context.Context,
	pool *pgxpool.Pool,
	bookID int,
	source *entity.BookUnitSource,
	pages bool,
) (documents, coveredRunes, uncoveredRunes, unexpectedSpans int64, err error) {
	derived, _, err := unitregistry.DeriveBook(source)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("verify canonical coverage derive book %d: %w", bookID, err)
	}

	expectedCounts := make(map[string]int, len(derived))

	requiredCoverage := canonicalRequiredCoverage(bookID, source)
	for i := range derived {
		span := canonicalSpanForDerived(bookID, &derived[i])
		expectedCounts[span.span]++
	}

	documents = int64(len(requiredCoverage))

	var actualRows pgx.Rows

	if pages {
		pageIDs := make([]int, 0, len(source.Pages))
		for i := range source.Pages {
			pageIDs = append(pageIDs, source.Pages[i].PageID)
		}

		actualRows, err = pool.Query(ctx, `
SELECT COALESCE(heading_id, 0), page_id, kind, text, language, content_role,
       source_document_hash, source_char_start, source_char_end
FROM citable_units
WHERE book_id = $1 AND corpus = 'kitab' AND lifecycle = 'active'
  AND content_role = 'book_page' AND page_id = ANY($2)
ORDER BY id`, bookID, pageIDs)
	} else {
		actualRows, err = pool.Query(ctx, `
SELECT COALESCE(heading_id, 0), page_id, kind, text, language, content_role,
       source_document_hash, source_char_start, source_char_end
FROM citable_units
WHERE book_id = $1 AND corpus = 'kitab' AND lifecycle = 'active'
  AND content_role <> 'book_page'
ORDER BY id`, bookID)
	}

	if err != nil {
		return documents, coveredRunes, uncoveredRunes, unexpectedSpans,
			fmt.Errorf("verify canonical coverage units book %d: %w", bookID, err)
	}

	for actualRows.Next() {
		var (
			headingID, pageID          int
			kind, text, language, role string
			documentHash               []byte
			startPointer, endPointer   *int
		)
		if scanErr := actualRows.Scan(&headingID, &pageID, &kind, &text, &language, &role,
			&documentHash, &startPointer, &endPointer); scanErr != nil {
			actualRows.Close()

			return documents, coveredRunes, uncoveredRunes, unexpectedSpans,
				fmt.Errorf("verify canonical coverage units scan book %d: %w", bookID, scanErr)
		}

		if startPointer == nil || endPointer == nil || documentHash == nil {
			unexpectedSpans++

			continue
		}

		span := canonicalSpanKey(bookID, headingID, pageID, kind, text, language, role,
			documentHash, *startPointer, *endPointer)
		if expectedCounts[span.span] == 0 {
			unexpectedSpans++

			continue
		}

		expectedCounts[span.span]--
		markCanonicalRunes(requiredCoverage, span)
	}

	if rowsErr := actualRows.Err(); rowsErr != nil {
		actualRows.Close()

		return documents, coveredRunes, uncoveredRunes, unexpectedSpans,
			fmt.Errorf("verify canonical coverage units rows book %d: %w", bookID, rowsErr)
	}

	actualRows.Close()

	coveredRunes, uncoveredRunes = countUncoveredCanonicalRunes(requiredCoverage)

	return documents, coveredRunes, uncoveredRunes, unexpectedSpans, nil
}

func canonicalSpanForDerived(bookID int, unit *unitregistry.DerivedUnit) canonicalSpan {
	headingID := 0
	if unit.HeadingID != nil {
		headingID = *unit.HeadingID
	}

	return canonicalSpanKey(bookID, headingID, unit.PageID, unit.Kind, unit.Text, unit.Language,
		unit.ContentRole, unit.SourceDocumentHash, unit.SourceCharStart, unit.SourceCharEnd)
}

func canonicalSpanKey(
	bookID, headingID, pageID int,
	kind, text, language, role string,
	documentHash []byte,
	start, end int,
) canonicalSpan {
	textHash := sha256.Sum256([]byte(text))
	document := canonicalDocumentKey(bookID, headingID, pageID, role, language, documentHash)

	return canonicalSpan{
		document: document,
		span:     fmt.Sprintf("%s|%s|%d|%d|%x", document, kind, start, end, textHash),
		start:    start,
		end:      end,
	}
}

func canonicalDocumentKey(bookID, headingID, pageID int, role, language string, documentHash []byte) string {
	if role == entity.UnitContentRoleBookPage {
		headingID = 0 // one page document may cross multiple heading scopes
	}

	return fmt.Sprintf("%d|%d|%d|%s|%s|%x", bookID, headingID, pageID, role, language, documentHash)
}

// canonicalRequiredCoverage starts from the independent effective source
// document, not from derived units. Every non-whitespace source rune must be
// covered unless it is an explicit structural token (TOC label, footnote
// marker, or Shamela underscore separator). Thus a parser and registry that
// both drop an Arabic tail still fail this verifier.
func canonicalRequiredCoverage(bookID int, source *entity.BookUnitSource) canonicalCoverage {
	required := make(canonicalCoverage)

	for i := range source.Pages {
		page := &source.Pages[i]

		content := page.ContentHTML
		if strings.TrimSpace(content) == "" {
			content = page.ContentText
		}

		structured := readerutil.StructureMixedContent(content)
		if strings.TrimSpace(page.ContentText) != "" && !readerutil.AlignMixedSourceSpans(&structured, page.ContentText) {
			structured = readerutil.StructureMixedContent(page.ContentText)
			_ = readerutil.AlignMixedSourceSpans(&structured, page.ContentText)
		}

		hash := sha256.Sum256([]byte(structured.Text))
		document := canonicalDocumentKey(bookID, 0, page.PageID,
			entity.UnitContentRoleBookPage, "ar", hash[:])
		markRequiredDocumentRunes(required, document, &structured)
	}

	for i := range source.Assets {
		asset := &source.Assets[i]
		if asset.ProvenanceClass == entity.ProvenanceClassLegacyUnknown {
			continue
		}

		structured := readerutil.StructureMixedContent(asset.Content)
		if asset.ContentRole == entity.UnitContentRoleHeadingSummary {
			text := strings.TrimSpace(readerutil.PlainText(readerutil.SanitizeHTML(asset.Content)))
			structured = readerutil.StructureMixedContent(text)
		}

		hash := sha256.Sum256([]byte(structured.Text))
		document := canonicalDocumentKey(bookID, asset.HeadingID, asset.PageID,
			asset.ContentRole, asset.Language, hash[:])
		markRequiredDocumentRunes(required, document, &structured)
	}

	return required
}

//nolint:cyclop,gocognit,gocyclo // Marker removal mirrors the parser's exact rune offsets and must remain explicit for auditability.
func markRequiredDocumentRunes(
	required canonicalCoverage,
	document string,
	structured *readerutil.StructuredContent,
) {
	runes := []rune(structured.Text)

	coverage := &canonicalDocumentCoverage{
		runeCount: len(runes),
		required:  make([]uint64, bitmapWordCount(len(runes))),
		actual:    make([]uint64, bitmapWordCount(len(runes))),
	}
	for offset, value := range runes {
		if !unicode.IsSpace(value) && value != '_' {
			setBitmapBit(coverage.required, offset)
		}
	}

	for i := range structured.Blocks {
		block := &structured.Blocks[i]
		if block.AnchorID > 0 {
			clearBitmapRange(coverage.required, block.SourceCharStart, block.SourceCharEnd, len(runes))
		}
	}

	for i := range structured.Footnotes {
		note := &structured.Footnotes[i]

		marker := []rune(note.Marker)
		if len(marker) == 0 || note.SourceCharStart < len(marker) {
			continue
		}

		for start := note.SourceCharStart - len(marker); start >= 0; start-- {
			if string(runes[start:start+len(marker)]) != note.Marker {
				continue
			}

			clearBitmapRange(coverage.required, start, start+len(marker), len(runes))

			break
		}
	}

	if bitmapCount(coverage.required) > 0 {
		required[document] = coverage
	}
}

func countUncoveredCanonicalRunes(required canonicalCoverage) (covered, uncovered int64) {
	for _, document := range required {
		for index := range document.required {
			requiredCount := int64(bits.OnesCount64(document.required[index]))
			actualCount := int64(bits.OnesCount64(document.required[index] & document.actual[index]))
			covered += requiredCount
			uncovered += requiredCount - actualCount
		}
	}

	return covered, uncovered
}

func markCanonicalRunes(target canonicalCoverage, span canonicalSpan) {
	document := target[span.document]
	if document == nil {
		return
	}

	setBitmapRange(document.actual, span.start, span.end, document.runeCount)
}

func bitmapWordCount(runeCount int) int {
	return (runeCount + bitmapWordBits - 1) / bitmapWordBits
}

func setBitmapBit(bitmap []uint64, offset int) {
	bitmap[offset/bitmapWordBits] |= uint64(1) << (offset % bitmapWordBits)
}

func bitmapCount(bitmap []uint64) int64 {
	var total int64
	for _, word := range bitmap {
		total += int64(bits.OnesCount64(word))
	}

	return total
}

func setBitmapRange(bitmap []uint64, start, end, limit int) {
	mutateBitmapRange(bitmap, start, end, limit, true)
}

func clearBitmapRange(bitmap []uint64, start, end, limit int) {
	mutateBitmapRange(bitmap, start, end, limit, false)
}

func mutateBitmapRange(bitmap []uint64, start, end, limit int, set bool) {
	start = max(start, 0)

	end = min(end, limit)
	if start >= end {
		return
	}

	firstWord := start / bitmapWordBits

	lastWord := (end - 1) / bitmapWordBits
	for word := firstWord; word <= lastWord; word++ {
		wordStart := word * bitmapWordBits
		from := max(start-wordStart, 0)
		to := min(end-wordStart, bitmapWordBits)

		mask := ^uint64(0) << from
		if to < bitmapWordBits {
			mask &= (uint64(1) << to) - 1
		}

		if set {
			bitmap[word] |= mask
		} else {
			bitmap[word] &^= mask
		}
	}
}

func verificationSearchBookIDs(ctx context.Context, pool *pgxpool.Pool) ([]int, error) {
	rows, err := pool.Query(ctx, `
SELECT unit.book_id
FROM public_book_interpretive_citable_units unit
WHERE unit.content_role = 'book_page'
  AND ($1::integer[] IS NULL OR unit.book_id = ANY($1))
GROUP BY unit.book_id
ORDER BY COUNT(*) DESC, unit.book_id
LIMIT 10`, CitableCatalogBookIDs)
	if err != nil {
		return nil, fmt.Errorf("verify citable catalog search books: %w", err)
	}
	defer rows.Close()

	ids := make([]int, 0, verificationSearchBookLimit)

	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("verify citable catalog search books scan: %w", err)
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("verify citable catalog search books rows: %w", err)
	}

	return ids, nil
}

type unitSearchVerifier interface {
	SearchRAGUnits(ctx context.Context, bookID int, query string, limit int) ([]entity.RAGSearchResult, error)
}

func verifyUnitSearchP95(
	ctx context.Context,
	repo unitSearchVerifier,
	bookIDs []int,
) (samples int, p95Milliseconds float64, err error) {
	queries := []string{"الله", "قال", "الحديث", "القرآن", "العلم"}

	durations := make([]float64, 0, len(bookIDs)*len(queries))
	for _, bookID := range bookIDs {
		// One warm-up per book keeps connection/setup noise out of the p95 gate.
		if _, err := repo.SearchRAGUnits(ctx, bookID, queries[0], verificationSearchResultLimit); err != nil {
			return 0, 0, fmt.Errorf("verify citable catalog search warm-up book %d: %w", bookID, err)
		}

		for _, query := range queries {
			started := time.Now()

			if _, err := repo.SearchRAGUnits(ctx, bookID, query, verificationSearchResultLimit); err != nil {
				return 0, 0, fmt.Errorf("verify citable catalog search book %d: %w", bookID, err)
			}

			durations = append(durations,
				float64(time.Since(started).Microseconds())/microsecondsPerMillisecond)
		}
	}

	if len(durations) == 0 {
		return 0, 0, nil
	}

	sort.Float64s(durations)

	index := (len(durations)*verificationSearchPercentile + verificationSearchPercentileScale - 1) /
		verificationSearchPercentileScale
	if index > 0 {
		index--
	}

	return len(durations), durations[index], nil
}

//nolint:gocritic // The immutable audit aggregate is passed by value to preserve the existing verifier/test contract.
func citableAuditViolationTotal(v entity.CitableAuditViolations) int64 {
	return v.BookGone + v.SupersededNoSuccessor + v.ActiveWithSuccessor + v.LineageCycle +
		v.HashMismatch + v.AnchorMalformed + v.FootnoteParent + v.QuranBinding + v.QuranInterpretive +
		v.InterpretiveSafety + v.RAGProjectionDangling + v.ApprovedMentionAnchor +
		v.MentionUnitDangling + v.MentionBindingMismatch + v.CrossReferenceAnchor
}
