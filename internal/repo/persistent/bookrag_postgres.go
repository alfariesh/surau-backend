package persistent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

const (
	arabicSearchMarks          = "\u064b\u064c\u064d\u064e\u064f\u0650\u0651\u0652\u0653\u0654\u0655\u0670\u0640"
	maxCitationLocatorMatches  = 2
	ragUnitExactCandidateLimit = 1024
)

// BookRAGRepo provides retrieval queries for PageIndex-like book RAG.
type BookRAGRepo struct {
	*postgres.Postgres
}

// NewBookRAGRepo creates a book RAG repository.
func NewBookRAGRepo(pg *postgres.Postgres) *BookRAGRepo {
	return &BookRAGRepo{pg}
}

// CheckRAGUnitMaterialization fails before any LLM call when a published book
// cannot safely use the unit read path. This makes legacy fallback whole-request
// and limited to the two rollout states declared here.
//
//nolint:wsl_v5 // ordered fail-closed materialization gates are intentionally adjacent
func (r *BookRAGRepo) CheckRAGUnitMaterialization(ctx context.Context, bookID int) error {
	const sqlText = `
SELECT b.units_derived_at IS NOT NULL,
       b.units_stale_at IS NULL
       AND COALESCE(b.units_derivation_profile_version = $2, false),
       EXISTS (
           SELECT 1
           FROM citable_units materialized
           WHERE materialized.book_id = b.id
             AND materialized.corpus = 'kitab'
             AND materialized.lifecycle = 'active'
             AND materialized.content_role = 'book_page'
       ),
       EXISTS (
           SELECT 1
           FROM public_book_interpretive_citable_units cu
           WHERE cu.book_id = b.id
             AND cu.content_role = 'book_page'
       )
FROM books b
JOIN public_book_publications p ON p.book_id = b.id
WHERE b.id = $1 AND b.is_deleted = false`

	var derived, current, hasMaterializedUnits, hasEligibleUnits bool
	if err := r.Pool.QueryRow(
		ctx,
		sqlText,
		bookID,
		entity.KitabUnitDerivationProfileVersion,
	).Scan(&derived, &current, &hasMaterializedUnits, &hasEligibleUnits); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.ErrBookNotFound
		}

		return fmt.Errorf("BookRAGRepo - CheckRAGUnitMaterialization - QueryRow: %w", err)
	}

	if !derived {
		return entity.ErrRAGUnitMaterializationIncomplete
	}

	if !current {
		return entity.ErrRAGUnitMaterializationStale
	}
	if !hasMaterializedUnits {
		return entity.ErrRAGUnitMaterializationIncomplete
	}

	if !hasEligibleUnits {
		// This is provenance/kind denial, not a rollout gap. Never route it
		// through legacy pages, which could reintroduce Quran quotes or pending
		// machine text into interpretive retrieval.
		return entity.ErrRAGEvidenceNotFound
	}

	return nil
}

// GetRAGBookDocument returns published book metadata for QA.
func (r *BookRAGRepo) GetRAGBookDocument(ctx context.Context, bookID int, lang string) (entity.RAGBookDocument, error) {
	sqlText := `
SELECT b.id,
       COALESCE(bmt.display_title, me.display_title, b.name) AS title,
       COALESCE(bmt.description, me.description) AS description
FROM books b
JOIN public_book_publications p ON p.book_id = b.id
LEFT JOIN book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'
LEFT JOIN book_production_projects bpp ON bpp.book_id = b.id AND bpp.lang = $2 AND bpp.publication_status = 'published' AND bpp.workflow_status <> 'archived' AND $2 <> 'ar'
LEFT JOIN book_metadata_translations bmt
       ON bmt.book_id = b.id
      AND bmt.lang = $2
      AND bmt.is_deleted = false
      AND bpp.id IS NOT NULL
      AND (bmt.provenance_class = 'source' OR bmt.translation_status = 'reviewed')
WHERE b.id = $1 AND b.is_deleted = false`

	var doc entity.RAGBookDocument
	var description sql.NullString
	if err := r.Pool.QueryRow(ctx, sqlText, bookID, lang).Scan(&doc.BookID, &doc.Title, &description); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.RAGBookDocument{}, entity.ErrBookNotFound
		}

		return entity.RAGBookDocument{}, fmt.Errorf("BookRAGRepo - GetRAGBookDocument - QueryRow: %w", err)
	}

	doc.Description = nullableString(description)

	return doc, nil
}

// ListRAGStructure returns compact TOC rows with page ranges.
func (r *BookRAGRepo) ListRAGStructure(ctx context.Context, bookID int, lang string) ([]entity.RAGStructureNode, error) {
	sqlText := `
SELECT h.book_id,
       h.heading_id,
       h.parent_id,
       h.page_id,
       h.depth,
       h.ordinal,
       COALESCE(st.title, he.content, h.content) AS title,
       COALESCE(bhs_lang.summary, bhs_ar.summary) AS summary,
       CASE
           WHEN bhs_lang.summary IS NOT NULL THEN bhs_lang.lang
           WHEN bhs_ar.summary IS NOT NULL THEN bhs_ar.lang
           ELSE NULL
       END AS summary_lang,
       hr.start_page_id,
       hr.end_page_id
FROM book_headings h
JOIN book_heading_ranges hr ON hr.book_id = h.book_id AND hr.heading_id = h.heading_id
JOIN public_book_publications p ON p.book_id = h.book_id
LEFT JOIN book_heading_edits he ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'
LEFT JOIN book_production_projects bpp ON bpp.book_id = h.book_id AND bpp.lang = $2 AND bpp.publication_status = 'published' AND bpp.workflow_status <> 'archived' AND $2 <> 'ar'
LEFT JOIN section_translations st
       ON st.book_id = h.book_id
      AND st.heading_id = h.heading_id
      AND st.lang = $2
      AND st.is_deleted = false
      AND bpp.id IS NOT NULL
      AND (st.provenance_class = 'source' OR st.translation_status = 'reviewed')
LEFT JOIN book_heading_summaries bhs_lang
       ON bhs_lang.book_id = h.book_id
      AND bhs_lang.heading_id = h.heading_id
      AND bhs_lang.lang = $2
      AND bhs_lang.is_deleted = false
      AND ($2 = 'ar' OR bpp.id IS NOT NULL)
      AND (bhs_lang.provenance_class = 'source' OR bhs_lang.summary_status = 'reviewed')
LEFT JOIN book_heading_summaries bhs_ar
       ON bhs_ar.book_id = h.book_id
      AND bhs_ar.heading_id = h.heading_id
      AND bhs_ar.lang = 'ar'
      AND bhs_ar.is_deleted = false
      AND ($2 = 'ar' OR bpp.id IS NOT NULL)
      AND (bhs_ar.provenance_class = 'source' OR bhs_ar.summary_status = 'reviewed')
WHERE h.book_id = $1 AND h.is_deleted = false
ORDER BY h.ordinal ASC, h.heading_id ASC`

	rows, err := r.Pool.Query(ctx, sqlText, bookID, lang)
	if err != nil {
		return nil, fmt.Errorf("BookRAGRepo - ListRAGStructure - Query: %w", err)
	}
	defer rows.Close()

	nodes := make([]entity.RAGStructureNode, 0)
	for rows.Next() {
		node, err := scanRAGStructureNode(rows)
		if err != nil {
			return nil, fmt.Errorf("BookRAGRepo - ListRAGStructure - scanRAGStructureNode: %w", err)
		}

		nodes = append(nodes, node)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("BookRAGRepo - ListRAGStructure - rows.Err: %w", err)
	}

	return nodes, nil
}

// GetRAGPageSources returns page-level source blocks for selected heading ranges.
func (r *BookRAGRepo) GetRAGPageSources(
	ctx context.Context,
	bookID int,
	headingIDs []int,
	focusPageIDs []int,
	lang string,
	maxPages int,
) ([]entity.RAGPageSource, error) {
	if len(headingIDs) == 0 || maxPages <= 0 {
		return []entity.RAGPageSource{}, nil
	}

	sqlText := `
WITH selected AS (
    SELECT h.book_id,
           h.heading_id,
           h.depth,
           h.ordinal,
           COALESCE(st.title, he.content, h.content) AS heading_title,
           hr.start_page_id,
           hr.end_page_id,
           GREATEST(hr.start_page_id - 1, 1) AS context_start_page_id,
           st.content AS translation_text
    FROM book_headings h
    JOIN book_heading_ranges hr ON hr.book_id = h.book_id AND hr.heading_id = h.heading_id
    LEFT JOIN book_heading_edits he ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'
    LEFT JOIN book_production_projects bpp ON bpp.book_id = h.book_id AND bpp.lang = $4 AND bpp.publication_status = 'published' AND bpp.workflow_status <> 'archived' AND $4 <> 'ar'
    LEFT JOIN section_translations st
           ON st.book_id = h.book_id
          AND st.heading_id = h.heading_id
          AND st.lang = $4
          AND st.is_deleted = false
          AND bpp.id IS NOT NULL
          AND (st.provenance_class = 'source' OR st.translation_status = 'reviewed')
    WHERE h.book_id = $1 AND h.heading_id = ANY($2) AND h.is_deleted = false
),
ranked AS (
    SELECT s.book_id,
           s.heading_id,
           s.heading_title,
           s.start_page_id,
           s.end_page_id,
           bp.page_id,
           bp.part,
           bp.printed_page,
           bp.number,
           COALESCE(pe.content_text, bp.content_text) AS content_text,
           s.translation_text,
           CASE
               WHEN bp.page_id = ANY($3) THEN 0
               WHEN bp.page_id < s.start_page_id THEN 1
               ELSE 2
           END AS focus_rank,
           COALESCE(array_position($3::integer[], bp.page_id), 2147483647) AS focus_position,
           row_number() OVER (
               PARTITION BY bp.book_id, bp.page_id
               ORDER BY CASE WHEN bp.page_id = ANY($3) THEN 0 ELSE 1 END,
                        CASE WHEN bp.page_id BETWEEN s.start_page_id AND s.end_page_id THEN 0 ELSE 1 END,
                        s.depth DESC,
                        s.ordinal DESC,
                        s.heading_id ASC
           ) AS rn
    FROM selected s
    JOIN book_pages bp ON bp.book_id = s.book_id AND bp.page_id BETWEEN s.context_start_page_id AND s.end_page_id
    JOIN public_book_publications p ON p.book_id = bp.book_id
    LEFT JOIN book_page_edits pe ON pe.book_id = bp.book_id AND pe.page_id = bp.page_id AND pe.status = 'published'
    WHERE bp.is_deleted = false
)
SELECT book_id,
       heading_id,
       heading_title,
       start_page_id,
       end_page_id,
       page_id,
       part,
       printed_page,
       number,
       content_text,
       translation_text
FROM ranked
WHERE rn = 1
ORDER BY focus_rank ASC, focus_position ASC, page_id ASC
LIMIT $5`

	headingIDs32, err := int32Slice("heading IDs", headingIDs)
	if err != nil {
		return nil, fmt.Errorf("BookRAGRepo - GetRAGPageSources - heading IDs: %w", err)
	}
	focusPageIDs32, err := int32Slice("focus page IDs", focusPageIDs)
	if err != nil {
		return nil, fmt.Errorf("BookRAGRepo - GetRAGPageSources - focus page IDs: %w", err)
	}

	rows, err := r.Pool.Query(
		ctx,
		sqlText,
		bookID,
		headingIDs32,
		focusPageIDs32,
		lang,
		maxPages,
	)
	if err != nil {
		return nil, fmt.Errorf("BookRAGRepo - GetRAGPageSources - Query: %w", err)
	}
	defer rows.Close()

	sources := make([]entity.RAGPageSource, 0, maxPages)
	for rows.Next() {
		source, err := scanRAGPageSource(rows, lang)
		if err != nil {
			return nil, fmt.Errorf("BookRAGRepo - GetRAGPageSources - scanRAGPageSource: %w", err)
		}

		sources = append(sources, source)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("BookRAGRepo - GetRAGPageSources - rows.Err: %w", err)
	}

	return sources, nil
}

// GetRAGUnitSources returns one structurally eligible Citable Unit per source
// block. Public visibility and B-4's grandfather/restricted policy are owned by
// public_book_interpretive_citable_units, not reimplemented here.
//
//nolint:funlen,wsl_v5 // the auditable SQL projection and its row mapping intentionally stay adjacent
func (r *BookRAGRepo) GetRAGUnitSources(
	ctx context.Context,
	bookID int,
	headingIDs []int,
	focusPageIDs []int,
	lang string,
	maxPages int,
) ([]entity.RAGPageSource, error) {
	if len(headingIDs) == 0 || maxPages <= 0 {
		return []entity.RAGPageSource{}, nil
	}

	const sqlText = `
WITH selected_ranges AS (
    SELECT hr.start_page_id,
           hr.end_page_id,
           GREATEST(hr.start_page_id - 1, 1) AS context_start_page_id
    FROM book_heading_ranges hr
    WHERE hr.book_id = $1
      AND hr.heading_id = ANY($2)
),
candidate_pages AS (
    SELECT cu.page_id,
           min(CASE
               WHEN cu.page_id = ANY($3) THEN 0
               WHEN cu.page_id < selected.start_page_id THEN 1
               ELSE 2
           END) AS focus_rank,
           min(array_position($3::integer[], cu.page_id))
               FILTER (WHERE cu.page_id = ANY($3)) AS focus_position
    FROM selected_ranges selected
    JOIN public_book_interpretive_citable_units cu
      ON cu.book_id = $1
     AND cu.page_id BETWEEN selected.context_start_page_id AND selected.end_page_id
    WHERE cu.content_role = 'book_page'
      AND cu.page_id IS NOT NULL
    GROUP BY cu.page_id
),
chosen_pages AS (
    SELECT page_id, focus_rank, focus_position
    FROM candidate_pages
    WHERE page_id IS NOT NULL
    ORDER BY focus_rank ASC, focus_position ASC NULLS LAST, page_id ASC
    LIMIT $4
)
SELECT cu.id::text,
       cu.anchor,
       cu.book_id,
       cu.heading_id,
       COALESCE(he.content, h.content) AS heading_title,
       hr.start_page_id,
       hr.end_page_id,
       cu.page_id,
       bp.part,
       bp.printed_page,
       bp.number,
       cu.text
FROM public_book_interpretive_citable_units cu
JOIN chosen_pages chosen ON chosen.page_id = cu.page_id
JOIN book_headings h
  ON h.book_id = cu.book_id
 AND h.heading_id = cu.heading_id
 AND h.is_deleted = false
JOIN book_heading_ranges hr
  ON hr.book_id = h.book_id
 AND hr.heading_id = h.heading_id
JOIN book_pages bp
  ON bp.book_id = cu.book_id
 AND bp.page_id = cu.page_id
 AND bp.is_deleted = false
LEFT JOIN book_heading_edits he
  ON he.book_id = h.book_id
 AND he.heading_id = h.heading_id
 AND he.status = 'published'
WHERE cu.book_id = $1
  AND cu.content_role = 'book_page'
  AND cu.heading_id IS NOT NULL
ORDER BY chosen.focus_rank ASC,
         chosen.focus_position ASC NULLS LAST,
         cu.page_id ASC,
         cu.position ASC,
         cu.ordinal ASC`

	headingIDs32, err := int32Slice("heading IDs", headingIDs)
	if err != nil {
		return nil, fmt.Errorf("BookRAGRepo - GetRAGUnitSources - heading IDs: %w", err)
	}

	focusPageIDs32, err := int32Slice("focus page IDs", focusPageIDs)
	if err != nil {
		return nil, fmt.Errorf("BookRAGRepo - GetRAGUnitSources - focus page IDs: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, bookID, headingIDs32, focusPageIDs32, maxPages)
	if err != nil {
		return nil, fmt.Errorf("BookRAGRepo - GetRAGUnitSources - Query: %w", err)
	}
	defer rows.Close()

	sources := make([]entity.RAGPageSource, 0, maxPages)

	for rows.Next() {
		var (
			source                    entity.RAGPageSource
			unitID, unitAnchor        string
			part, printedPage, number sql.NullString
		)

		if err = rows.Scan(
			&unitID,
			&unitAnchor,
			&source.BookID,
			&source.HeadingID,
			&source.HeadingTitle,
			&source.StartPageID,
			&source.EndPageID,
			&source.PageID,
			&part,
			&printedPage,
			&number,
			&source.ContentText,
		); err != nil {
			return nil, fmt.Errorf("BookRAGRepo - GetRAGUnitSources - Scan: %w", err)
		}

		source.UnitID = &unitID
		source.UnitAnchor = &unitAnchor
		source.Part = nullableString(part)
		source.PrintedPage = nullableString(printedPage)
		source.Number = nullableString(number)
		source.Anchor = fmt.Sprintf("toc-%d", source.HeadingID)
		source.URL = fmt.Sprintf("/v1/books/%d/toc/%d/read?lang=%s", source.BookID, source.HeadingID, lang)
		sources = append(sources, source)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("BookRAGRepo - GetRAGUnitSources - rows.Err: %w", err)
	}

	return sources, nil
}

// SearchRAGPages returns lexical fallback hits across headings, pages, and translations.
func (r *BookRAGRepo) SearchRAGPages(
	ctx context.Context,
	bookID int,
	query string,
	lang string,
	limit int,
) ([]entity.RAGSearchResult, error) {
	rawQuery := strings.TrimSpace(query)
	if rawQuery == "" || limit <= 0 {
		return []entity.RAGSearchResult{}, nil
	}

	exact, err := r.searchRAGPagesExact(ctx, bookID, rawQuery, limit)
	if err != nil {
		return nil, err
	}

	if len(exact) > 0 {
		return exact, nil
	}

	query = stripArabicSearchMarks(rawQuery)

	sqlText := `
SELECT h.heading_id,
       bp.page_id,
       GREATEST(
           similarity(COALESCE(he.content, h.content), $2),
           similarity(COALESCE(pe.content_text, bp.content_text), $2),
           similarity(COALESCE(st.content, ''), $2),
           similarity(COALESCE(bhs_lang.summary, bhs_ar.summary, ''), $2),
           similarity(translate(COALESCE(he.content, h.content), $5, ''), $2),
           similarity(translate(COALESCE(pe.content_text, bp.content_text), $5, ''), $2),
           similarity(translate(COALESCE(st.content, ''), $5, ''), $2),
           similarity(translate(COALESCE(bhs_lang.summary, bhs_ar.summary, ''), $5, ''), $2)
       ) AS score
FROM book_headings h
JOIN book_heading_ranges hr ON hr.book_id = h.book_id AND hr.heading_id = h.heading_id
JOIN book_pages bp ON bp.book_id = h.book_id AND bp.page_id BETWEEN GREATEST(hr.start_page_id - 1, 1) AND hr.end_page_id
JOIN public_book_publications p ON p.book_id = h.book_id
LEFT JOIN book_heading_edits he ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'
LEFT JOIN book_page_edits pe ON pe.book_id = bp.book_id AND pe.page_id = bp.page_id AND pe.status = 'published'
LEFT JOIN book_production_projects bpp ON bpp.book_id = h.book_id AND bpp.lang = $3 AND bpp.publication_status = 'published' AND bpp.workflow_status <> 'archived' AND $3 <> 'ar'
LEFT JOIN section_translations st
       ON st.book_id = h.book_id
      AND st.heading_id = h.heading_id
      AND st.lang = $3
      AND st.is_deleted = false
      AND bpp.id IS NOT NULL
      AND (st.provenance_class = 'source' OR st.translation_status = 'reviewed')
LEFT JOIN book_heading_summaries bhs_lang
       ON bhs_lang.book_id = h.book_id
      AND bhs_lang.heading_id = h.heading_id
      AND bhs_lang.lang = $3
      AND bhs_lang.is_deleted = false
      AND ($3 = 'ar' OR bpp.id IS NOT NULL)
      AND (bhs_lang.provenance_class = 'source' OR bhs_lang.summary_status = 'reviewed')
LEFT JOIN book_heading_summaries bhs_ar
       ON bhs_ar.book_id = h.book_id
      AND bhs_ar.heading_id = h.heading_id
      AND bhs_ar.lang = 'ar'
      AND bhs_ar.is_deleted = false
      AND ($3 = 'ar' OR bpp.id IS NOT NULL)
      AND (bhs_ar.provenance_class = 'source' OR bhs_ar.summary_status = 'reviewed')
WHERE h.book_id = $1
  AND h.is_deleted = false
  AND bp.is_deleted = false
  AND (
      COALESCE(he.content, h.content) ILIKE '%' || $6 || '%'
      OR COALESCE(pe.content_text, bp.content_text) ILIKE '%' || $6 || '%'
      OR COALESCE(st.content, '') ILIKE '%' || $6 || '%'
      OR COALESCE(bhs_lang.summary, bhs_ar.summary, '') ILIKE '%' || $6 || '%'
      OR COALESCE(he.content, h.content) % $2
      OR COALESCE(pe.content_text, bp.content_text) % $2
      OR COALESCE(st.content, '') % $2
      OR COALESCE(bhs_lang.summary, bhs_ar.summary, '') % $2
      OR translate(COALESCE(he.content, h.content), $5, '') ILIKE '%' || $6 || '%'
      OR translate(COALESCE(pe.content_text, bp.content_text), $5, '') ILIKE '%' || $6 || '%'
      OR translate(COALESCE(st.content, ''), $5, '') ILIKE '%' || $6 || '%'
      OR translate(COALESCE(bhs_lang.summary, bhs_ar.summary, ''), $5, '') ILIKE '%' || $6 || '%'
      OR translate(COALESCE(he.content, h.content), $5, '') % $2
      OR translate(COALESCE(pe.content_text, bp.content_text), $5, '') % $2
      OR translate(COALESCE(st.content, ''), $5, '') % $2
      OR translate(COALESCE(bhs_lang.summary, bhs_ar.summary, ''), $5, '') % $2
  )
ORDER BY score DESC, h.depth DESC, bp.page_id ASC, h.heading_id ASC
LIMIT $4`

	// $2 stays raw for similarity()/trigram %; $6 is the LIKE-escaped copy for ILIKE.
	rows, err := r.Pool.Query(ctx, sqlText, bookID, query, lang, limit, arabicSearchMarks, escapeLike(query))
	if err != nil {
		return nil, fmt.Errorf("BookRAGRepo - SearchRAGPages - Query: %w", err)
	}
	defer rows.Close()

	results := make([]entity.RAGSearchResult, 0, limit)

	for rows.Next() {
		var result entity.RAGSearchResult
		if err = rows.Scan(&result.HeadingID, &result.PageID, &result.Score); err != nil {
			return nil, fmt.Errorf("BookRAGRepo - SearchRAGPages - rows.Scan: %w", err)
		}

		results = append(results, result)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("BookRAGRepo - SearchRAGPages - rows.Err: %w", err)
	}

	return results, nil
}

// searchRAGPagesExact keeps dual rollout probes and verbatim user questions on
// the indexed page path. Heading/translation-only questions still fall
// through to the established fuzzy query, preserving its broader behavior.
//
//nolint:funlen // Keeping the exact SQL and its row decoder adjacent makes the fallback boundary auditable.
func (r *BookRAGRepo) searchRAGPagesExact(
	ctx context.Context,
	bookID int,
	query string,
	limit int,
) ([]entity.RAGSearchResult, error) {
	const sqlText = `
WITH exact_pages AS MATERIALIZED (
    SELECT page.book_id,
           page.page_id
    FROM book_pages page
    JOIN public_book_publications publication ON publication.book_id = page.book_id
    WHERE page.book_id = $1
      AND page.is_deleted = FALSE
      AND NOT EXISTS (
          SELECT 1
          FROM book_page_edits edit
          WHERE edit.book_id = page.book_id
            AND edit.page_id = page.page_id
            AND edit.status = 'published'
      )
      AND page.content_text ILIKE '%' || $3 || '%'
    UNION ALL
    SELECT page.book_id,
           page.page_id
    FROM book_page_edits edit
    JOIN book_pages page
      ON page.book_id = edit.book_id
     AND page.page_id = edit.page_id
     AND page.is_deleted = FALSE
    JOIN public_book_publications publication ON publication.book_id = page.book_id
    WHERE edit.book_id = $1
      AND edit.status = 'published'
      AND edit.content_text ILIKE '%' || $3 || '%'
)
SELECT heading.heading_id,
       exact.page_id,
       1::float8 AS score
FROM exact_pages exact
JOIN book_headings heading
  ON heading.book_id = exact.book_id
 AND heading.is_deleted = FALSE
JOIN book_heading_ranges heading_range
  ON heading_range.book_id = heading.book_id
 AND heading_range.heading_id = heading.heading_id
 AND exact.page_id BETWEEN GREATEST(heading_range.start_page_id - 1, 1) AND heading_range.end_page_id
ORDER BY heading.depth DESC, heading.ordinal DESC, exact.page_id ASC, heading.heading_id ASC
LIMIT $2`

	// The book and quote values materially change selectivity. Bypass the
	// server-side generic-plan switch so a repeated full-catalog proof cannot
	// degrade from the per-book index plan into a corpus-wide scan after the
	// fifth execution.
	rows, err := r.Pool.Query(
		ctx, sqlText, pgx.QueryExecModeExec, bookID, limit, escapeLike(query),
	)
	if err != nil {
		return nil, fmt.Errorf("BookRAGRepo - SearchRAGPages - exact query: %w", err)
	}
	defer rows.Close()

	results := make([]entity.RAGSearchResult, 0, limit)

	for rows.Next() {
		var result entity.RAGSearchResult
		if err = rows.Scan(&result.HeadingID, &result.PageID, &result.Score); err != nil {
			return nil, fmt.Errorf("BookRAGRepo - SearchRAGPages - exact scan: %w", err)
		}

		results = append(results, result)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("BookRAGRepo - SearchRAGPages - exact rows: %w", err)
	}

	return results, nil
}

// SearchRAGUnits returns lexical hits only from the structural interpretive
// view; machine-unreviewed units and Quran quotes are impossible to select.
func (r *BookRAGRepo) SearchRAGUnits(
	ctx context.Context,
	bookID int,
	query string,
	limit int,
) ([]entity.RAGSearchResult, error) {
	query = stripArabicSearchMarks(strings.TrimSpace(query))
	if query == "" || limit <= 0 {
		return []entity.RAGSearchResult{}, nil
	}

	exact, err := r.searchRAGUnitsExact(ctx, bookID, query, limit)
	if err != nil {
		return nil, err
	}

	if len(exact) > 0 {
		return exact, nil
	}

	// Full-text search is the bounded common path. Preserve the previous
	// trigram behavior only as a second-stage typo/substring fallback when the
	// exact token index found no evidence at all. Filling a partially populated
	// exact window with trigram hits makes common multi-token citations scan the
	// whole book even though the strongest evidence is already known.
	fuzzy, err := r.searchRAGUnitsFuzzy(ctx, bookID, query, limit)
	if err != nil {
		return nil, err
	}

	type resultKey struct{ headingID, pageID int }

	seen := make(map[resultKey]struct{}, len(exact)+len(fuzzy))
	merged := make([]entity.RAGSearchResult, 0, min(limit, len(exact)+len(fuzzy)))
	appendUnique := func(results []entity.RAGSearchResult) {
		for i := range results {
			key := resultKey{headingID: results[i].HeadingID, pageID: results[i].PageID}
			if _, exists := seen[key]; exists {
				continue
			}

			seen[key] = struct{}{}

			merged = append(merged, results[i])
			if len(merged) == limit {
				return
			}
		}
	}
	appendUnique(exact)

	if len(merged) < limit {
		appendUnique(fuzzy)
	}

	return merged, nil
}

func (r *BookRAGRepo) searchRAGUnitsExact(
	ctx context.Context,
	bookID int,
	query string,
	limit int,
) ([]entity.RAGSearchResult, error) {
	const sqlText = `
WITH search_query AS (
    SELECT plainto_tsquery('simple'::regconfig, 'book' || ($1::integer)::text) &&
           plainto_tsquery('simple'::regconfig, $2) AS value
),
candidates AS MATERIALIZED (
    SELECT unit.id,
           unit.heading_id,
           unit.page_id
    FROM citable_units unit
    CROSS JOIN search_query
    WHERE unit.book_id = $1
      AND unit.lifecycle = 'active'
      AND unit.corpus = 'kitab'
      AND unit.interpretive_retrieval_eligible
      AND unit.content_role = 'book_page'
      AND unit.heading_id IS NOT NULL
      AND unit.page_id IS NOT NULL
      AND (unit.license_status IS NULL OR unit.license_status = 'permitted')
      AND to_tsvector('simple'::regconfig,
          'book' || unit.book_id::text || ' ' || translate(unit.text, 'ًٌٍَُِّْٰٕٓٔـ', '')) @@ search_query.value
    ORDER BY unit.page_id, unit.position, unit.ordinal, unit.id
    LIMIT $4
),
matches AS MATERIALIZED (
    SELECT candidate.id,
           candidate.heading_id,
           candidate.page_id
    FROM candidates candidate
),
scored_matches AS MATERIALIZED (
    SELECT eligible.id,
           eligible.heading_id,
           eligible.page_id,
           eligible.position,
           eligible.ordinal,
           CASE
               WHEN strpos(translate(eligible.text, 'ًٌٍَُِّْٰٕٓٔـ', ''), $2) > 0 THEN 2::float8
               ELSE 1::float8
           END AS score
    FROM matches
    JOIN public_book_interpretive_citable_units eligible ON eligible.id = matches.id
),
ranked_matches AS MATERIALIZED (
    SELECT scored.id::text AS unit_id,
           scored.heading_id,
           scored.page_id,
           scored.score,
           row_number() OVER (
               PARTITION BY scored.heading_id, scored.page_id
               ORDER BY scored.score DESC, scored.position, scored.ordinal, scored.id
           ) AS page_rank
    FROM scored_matches scored
)
SELECT unit_id,
       heading_id,
       page_id,
       score
FROM ranked_matches
WHERE page_rank = 1
ORDER BY score DESC, page_id ASC, heading_id ASC
LIMIT $3`

	return r.queryRAGUnitSearch(ctx, sqlText, bookID, query, limit, "exact", ragUnitExactCandidateLimit)
}

func (r *BookRAGRepo) searchRAGUnitsFuzzy(
	ctx context.Context,
	bookID int,
	query string,
	limit int,
) ([]entity.RAGSearchResult, error) {
	const sqlText = `
WITH candidates AS MATERIALIZED (
    SELECT cu.id::text AS unit_id,
           cu.heading_id,
           cu.page_id,
           cu.position,
           cu.ordinal,
           GREATEST(
           similarity(cu.text, $2),
           similarity(translate(cu.text, 'ًٌٍَُِّْٰٕٓٔـ', ''), $2)
           ) AS score
    FROM public_book_interpretive_citable_units cu
    WHERE cu.book_id = $1
      AND cu.content_role = 'book_page'
      AND cu.heading_id IS NOT NULL
      AND cu.page_id IS NOT NULL
      AND (
          cu.text ILIKE '%' || $4 || '%'
          OR cu.text % $2
          OR translate(cu.text, 'ًٌٍَُِّْٰٕٓٔـ', '') ILIKE '%' || $4 || '%'
          OR translate(cu.text, 'ًٌٍَُِّْٰٕٓٔـ', '') % $2
      )
),
ranked_matches AS (
    SELECT candidates.*,
           row_number() OVER (
               PARTITION BY heading_id, page_id
               ORDER BY score DESC, position, ordinal, unit_id
           ) AS page_rank
    FROM candidates
)
SELECT unit_id,
       heading_id,
       page_id,
       score
FROM ranked_matches
WHERE page_rank = 1
ORDER BY score DESC, page_id ASC, heading_id ASC
LIMIT $3`

	return r.queryRAGUnitSearch(ctx, sqlText, bookID, query, limit, "fuzzy", escapeLike(query))
}

func (r *BookRAGRepo) queryRAGUnitSearch(
	ctx context.Context,
	sqlText string,
	bookID int,
	query string,
	limit int,
	phase string,
	extra ...any,
) ([]entity.RAGSearchResult, error) {
	args := []any{bookID, query, limit}
	args = append(args, extra...)

	queryArgs := make([]any, 0, len(args)+1)
	queryArgs = append(queryArgs, pgx.QueryExecModeExec)
	queryArgs = append(queryArgs, args...)

	// Search selectivity varies sharply by book and Arabic term. PostgreSQL's
	// prepared-statement heuristic otherwise replaces the first custom plans
	// with one generic plan, which can scan the global FTS/trigram index for a
	// common word before applying book_id. Exec mode keeps every plan bound to
	// the actual book while retaining the extended protocol and typed values.
	rows, err := r.Pool.Query(ctx, sqlText, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("BookRAGRepo - SearchRAGUnits - %s query: %w", phase, err)
	}
	defer rows.Close()

	results := make([]entity.RAGSearchResult, 0, limit)

	for rows.Next() {
		var result entity.RAGSearchResult
		if err = rows.Scan(&result.UnitID, &result.HeadingID, &result.PageID, &result.Score); err != nil {
			return nil, fmt.Errorf("BookRAGRepo - SearchRAGUnits - %s scan: %w", phase, err)
		}

		results = append(results, result)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("BookRAGRepo - SearchRAGUnits - %s rows: %w", phase, err)
	}

	return results, nil
}

// ResolveRAGUnitCitation maps a legacy page quote only when it is contained
// verbatim in exactly one current eligible unit. Ambiguous and cross-unit
// quotes deliberately return Found=false rather than a guessed Anchor.
//
//nolint:wsl_v5 // scan, cardinality, and materialization checks form one exact resolver
func (r *BookRAGRepo) ResolveRAGUnitCitation(
	ctx context.Context,
	bookID int,
	headingID int,
	pageID int,
	quote string,
) (entity.RAGUnitLocator, error) {
	quote = strings.TrimSpace(quote)
	if quote == "" {
		return entity.RAGUnitLocator{}, nil
	}

	const sqlText = `
SELECT cu.id::text, cu.anchor
FROM public_book_interpretive_citable_units cu
WHERE cu.book_id = $1
  AND cu.heading_id = $2
  AND cu.page_id = $3
  AND cu.content_role = 'book_page'
  AND strpos(cu.text, $4) > 0
ORDER BY cu.position ASC, cu.ordinal ASC
LIMIT 2`

	rows, err := r.Pool.Query(
		ctx, sqlText, pgx.QueryExecModeExec, bookID, headingID, pageID, quote,
	)
	if err != nil {
		return entity.RAGUnitLocator{}, fmt.Errorf("BookRAGRepo - ResolveRAGUnitCitation - Query: %w", err)
	}
	defer rows.Close()

	locators := make([]entity.RAGUnitLocator, 0, maxCitationLocatorMatches)

	for rows.Next() {
		var locator entity.RAGUnitLocator
		if err = rows.Scan(&locator.UnitID, &locator.UnitAnchor); err != nil {
			return entity.RAGUnitLocator{}, fmt.Errorf("BookRAGRepo - ResolveRAGUnitCitation - Scan: %w", err)
		}

		locators = append(locators, locator)
	}

	if err = rows.Err(); err != nil {
		return entity.RAGUnitLocator{}, fmt.Errorf("BookRAGRepo - ResolveRAGUnitCitation - rows.Err: %w", err)
	}
	if len(locators) != 1 {
		if len(locators) == 0 {
			if materializationErr := r.CheckRAGUnitMaterialization(ctx, bookID); materializationErr != nil {
				return entity.RAGUnitLocator{}, materializationErr
			}
		}

		return entity.RAGUnitLocator{}, nil
	}

	locators[0].Found = true

	return locators[0], nil
}

func scanRAGStructureNode(row rowScanner) (entity.RAGStructureNode, error) {
	var node entity.RAGStructureNode
	var parentID sql.NullInt64
	var summary sql.NullString
	var summaryLang sql.NullString
	if err := row.Scan(
		&node.BookID,
		&node.HeadingID,
		&parentID,
		&node.PageID,
		&node.Depth,
		&node.Ordinal,
		&node.Title,
		&summary,
		&summaryLang,
		&node.StartPageID,
		&node.EndPageID,
	); err != nil {
		return entity.RAGStructureNode{}, err
	}

	node.ParentID = nullableInt(parentID)
	node.Summary = nullableString(summary)
	node.SummaryLang = nullableString(summaryLang)

	return node, nil
}

func scanRAGPageSource(row rowScanner, lang string) (entity.RAGPageSource, error) {
	var source entity.RAGPageSource
	var part sql.NullString
	var printedPage sql.NullString
	var number sql.NullString
	var translationText sql.NullString
	if err := row.Scan(
		&source.BookID,
		&source.HeadingID,
		&source.HeadingTitle,
		&source.StartPageID,
		&source.EndPageID,
		&source.PageID,
		&part,
		&printedPage,
		&number,
		&source.ContentText,
		&translationText,
	); err != nil {
		return entity.RAGPageSource{}, err
	}

	source.Part = nullableString(part)
	source.PrintedPage = nullableString(printedPage)
	source.Number = nullableString(number)
	source.TranslationText = nullableString(translationText)
	source.Anchor = fmt.Sprintf("toc-%d", source.HeadingID)
	source.URL = fmt.Sprintf("/v1/books/%d/toc/%d/read?lang=%s", source.BookID, source.HeadingID, lang)

	return source, nil
}

func int32Slice(name string, values []int) ([]int32, error) {
	result := make([]int32, 0, len(values))
	for _, value := range values {
		if value < math.MinInt32 || value > math.MaxInt32 {
			return nil, fmt.Errorf("%s contains value outside int32 range: %d", name, value)
		}
		result = append(result, int32(value))
	}

	return result, nil
}

func stripArabicSearchMarks(value string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(arabicSearchMarks, r) {
			return -1
		}

		return r
	}, value)
}
