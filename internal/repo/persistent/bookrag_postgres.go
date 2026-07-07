package persistent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

const arabicSearchMarks = "\u064b\u064c\u064d\u064e\u064f\u0650\u0651\u0652\u0653\u0654\u0655\u0670\u0640"

// BookRAGRepo provides retrieval queries for PageIndex-like book RAG.
type BookRAGRepo struct {
	*postgres.Postgres
}

// NewBookRAGRepo creates a book RAG repository.
func NewBookRAGRepo(pg *postgres.Postgres) *BookRAGRepo {
	return &BookRAGRepo{pg}
}

// GetRAGBookDocument returns published book metadata for QA.
func (r *BookRAGRepo) GetRAGBookDocument(ctx context.Context, bookID int, lang string) (entity.RAGBookDocument, error) {
	sqlText := `
SELECT b.id,
       COALESCE(bmt.display_title, me.display_title, b.name) AS title,
       COALESCE(bmt.description, me.description) AS description
FROM books b
JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
LEFT JOIN book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'
LEFT JOIN book_production_projects bpp ON bpp.book_id = b.id AND bpp.lang = $2 AND bpp.publication_status = 'published' AND bpp.workflow_status <> 'archived' AND $2 <> 'ar'
LEFT JOIN book_metadata_translations bmt ON bmt.book_id = b.id AND bmt.lang = $2 AND bmt.is_deleted = false AND bpp.id IS NOT NULL
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
JOIN book_publications p ON p.book_id = h.book_id AND p.status = 'published'
LEFT JOIN book_heading_edits he ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'
LEFT JOIN book_production_projects bpp ON bpp.book_id = h.book_id AND bpp.lang = $2 AND bpp.publication_status = 'published' AND bpp.workflow_status <> 'archived' AND $2 <> 'ar'
LEFT JOIN section_translations st ON st.book_id = h.book_id AND st.heading_id = h.heading_id AND st.lang = $2 AND st.is_deleted = false AND bpp.id IS NOT NULL
LEFT JOIN book_heading_summaries bhs_lang ON bhs_lang.book_id = h.book_id AND bhs_lang.heading_id = h.heading_id AND bhs_lang.lang = $2 AND bhs_lang.is_deleted = false AND ($2 = 'ar' OR bpp.id IS NOT NULL)
LEFT JOIN book_heading_summaries bhs_ar ON bhs_ar.book_id = h.book_id AND bhs_ar.heading_id = h.heading_id AND bhs_ar.lang = 'ar' AND bhs_ar.is_deleted = false AND ($2 = 'ar' OR bpp.id IS NOT NULL)
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
    LEFT JOIN section_translations st ON st.book_id = h.book_id AND st.heading_id = h.heading_id AND st.lang = $4 AND st.is_deleted = false AND bpp.id IS NOT NULL
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
    JOIN book_publications p ON p.book_id = bp.book_id AND p.status = 'published'
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
ORDER BY focus_rank ASC, page_id ASC
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

// SearchRAGPages returns lexical fallback hits across headings, pages, and translations.
func (r *BookRAGRepo) SearchRAGPages(
	ctx context.Context,
	bookID int,
	query string,
	lang string,
	limit int,
) ([]entity.RAGSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" || limit <= 0 {
		return []entity.RAGSearchResult{}, nil
	}
	query = stripArabicSearchMarks(query)

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
JOIN book_publications p ON p.book_id = h.book_id AND p.status = 'published'
LEFT JOIN book_heading_edits he ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'
LEFT JOIN book_page_edits pe ON pe.book_id = bp.book_id AND pe.page_id = bp.page_id AND pe.status = 'published'
LEFT JOIN book_production_projects bpp ON bpp.book_id = h.book_id AND bpp.lang = $3 AND bpp.publication_status = 'published' AND bpp.workflow_status <> 'archived' AND $3 <> 'ar'
LEFT JOIN section_translations st ON st.book_id = h.book_id AND st.heading_id = h.heading_id AND st.lang = $3 AND st.is_deleted = false AND bpp.id IS NOT NULL
LEFT JOIN book_heading_summaries bhs_lang ON bhs_lang.book_id = h.book_id AND bhs_lang.heading_id = h.heading_id AND bhs_lang.lang = $3 AND bhs_lang.is_deleted = false AND ($3 = 'ar' OR bpp.id IS NOT NULL)
LEFT JOIN book_heading_summaries bhs_ar ON bhs_ar.book_id = h.book_id AND bhs_ar.heading_id = h.heading_id AND bhs_ar.lang = 'ar' AND bhs_ar.is_deleted = false AND ($3 = 'ar' OR bpp.id IS NOT NULL)
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
