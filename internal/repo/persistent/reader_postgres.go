package persistent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/readerutil"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

type rowScanner interface {
	Scan(dest ...any) error
}

// ReaderRepo provides catalog and reading queries.
type ReaderRepo struct {
	*postgres.Postgres
}

const (
	readerArabicSearchMarks = "\u064b\u064c\u064d\u064e\u064f\u0650\u0651\u0652\u0653\u0654\u0655\u0670\u0640"
	readerArabicVariantFrom = "أإآٱؤئءىة"
	readerArabicVariantTo   = "ااااويايه"
)

const bookCatalogStatsTotalsSQL = `
WITH published_books AS (
    SELECT b.id AS book_id,
           b.author_id,
           COALESCE(me.category_id, b.category_id) AS category_id,
           b.has_content
    FROM books b
    JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
    LEFT JOIN book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'
    WHERE b.is_deleted = false
),
covered_books AS (
    SELECT DISTINCT book_id
    FROM book_production_projects
    WHERE lang = $1
      AND publication_status = 'published'
      AND workflow_status <> 'archived'
      AND $1 <> 'ar'
)
SELECT COUNT(*)::INT AS total_books,
       COUNT(*)::INT AS published_count,
       COUNT(*)::INT AS catalog_published_count,
       COUNT(DISTINCT cb.book_id)::INT AS production_published_count,
       COUNT(DISTINCT author_id)::INT AS author_count,
       COUNT(DISTINCT category_id)::INT AS category_count,
       COUNT(*) FILTER (WHERE has_content)::INT AS with_content_count,
       COUNT(DISTINCT cb.book_id)::INT AS coverage_count
FROM published_books pb
LEFT JOIN covered_books cb ON cb.book_id = pb.book_id`

const bookCatalogStatsCategorySQL = `
WITH published_books AS (
    SELECT b.id AS book_id,
           COALESCE(me.category_id, b.category_id) AS category_id
    FROM books b
    JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
    LEFT JOIN book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'
    WHERE b.is_deleted = false
),
covered_books AS (
    SELECT DISTINCT book_id
    FROM book_production_projects
    WHERE lang = $1
      AND publication_status = 'published'
      AND workflow_status <> 'archived'
      AND $1 <> 'ar'
)
SELECT pb.category_id,
       CASE WHEN $1 <> 'ar' AND ct.category_id IS NOT NULL THEN ct.name ELSE c.name END AS category_name,
       COUNT(*)::INT AS total,
       COUNT(*)::INT AS published_count,
       COUNT(*)::INT AS catalog_published_count,
       COUNT(DISTINCT cb.book_id)::INT AS production_published_count,
       COUNT(DISTINCT cb.book_id)::INT AS coverage_count
FROM published_books pb
LEFT JOIN categories c ON c.id = pb.category_id
LEFT JOIN category_translations ct ON ct.category_id = c.id AND ct.lang = $1 AND ct.is_deleted = false AND $1 <> 'ar'
LEFT JOIN covered_books cb ON cb.book_id = pb.book_id
GROUP BY pb.category_id, category_name
ORDER BY total DESC, category_name ASC NULLS LAST`

const bookBaseSearchConditionSQL = `(COALESCE(bmt.display_title, me.display_title, b.name) ILIKE ?
	OR b.name ILIKE ?
	OR COALESCE(at.name, a.name) ILIKE ?
	OR a.name ILIKE ?
	OR COALESCE(ct.name, c.name) ILIKE ?
	OR c.name ILIKE ?
	OR COALESCE(bmt.bibliography, me.bibliography, b.bibliography) ILIKE ?
	OR COALESCE(bmt.hint, me.hint, b.hint) ILIKE ?
	OR EXISTS (
		SELECT 1
		FROM book_metadata_translations bmt_any
		WHERE bmt_any.book_id = b.id
		  AND bmt_any.is_deleted = false
		  AND EXISTS (
			SELECT 1
			FROM book_production_projects bpp_any
			WHERE bpp_any.book_id = b.id
			  AND bpp_any.lang = bmt_any.lang
			  AND bpp_any.publication_status = 'published'
			  AND bpp_any.workflow_status <> 'archived'
		  )
		  AND (
			bmt_any.display_title ILIKE ?
			OR COALESCE(bmt_any.bibliography, '') ILIKE ?
			OR COALESCE(bmt_any.hint, '') ILIKE ?
			OR COALESCE(bmt_any.description, '') ILIKE ?
		  )
	)
	OR EXISTS (
		SELECT 1
		FROM author_translations at_any
		WHERE at_any.author_id = a.id
		  AND at_any.is_deleted = false
		  AND EXISTS (
			SELECT 1
			FROM book_production_projects bpp_any
			WHERE bpp_any.book_id = b.id
			  AND bpp_any.lang = at_any.lang
			  AND bpp_any.publication_status = 'published'
			  AND bpp_any.workflow_status <> 'archived'
		  )
		  AND (at_any.name ILIKE ? OR COALESCE(at_any.biography, '') ILIKE ?)
	)
	OR EXISTS (
		SELECT 1
		FROM category_translations ct_any
		WHERE ct_any.category_id = c.id
		  AND ct_any.is_deleted = false
		  AND EXISTS (
			SELECT 1
			FROM book_production_projects bpp_any
			WHERE bpp_any.book_id = b.id
			  AND bpp_any.lang = ct_any.lang
			  AND bpp_any.publication_status = 'published'
			  AND bpp_any.workflow_status <> 'archived'
		  )
		  AND ct_any.name ILIKE ?
	))`

// NewReaderRepo creates a reader repository.
func NewReaderRepo(pg *postgres.Postgres) *ReaderRepo {
	return &ReaderRepo{pg}
}

// ListCategories returns non-deleted categories ordered for catalog display.
func (r *ReaderRepo) ListCategories(ctx context.Context, lang string) ([]entity.Category, error) {
	sqlText := `
SELECT c.id,
       CASE WHEN $1 <> 'ar' AND ct.category_id IS NOT NULL THEN ct.name ELSE c.name END AS name,
       c.display_order,
       ct.translation_status,
       ct.reviewed_by,
       ct.reviewed_at,
       c.is_deleted,
       c.updated_at,
       $1 AS requested_lang,
       CASE WHEN $1 <> 'ar' AND ct.category_id IS NOT NULL THEN $1 ELSE 'ar' END AS display_lang,
       ($1 <> 'ar' AND ct.category_id IS NULL) AS is_fallback,
       COALESCE(av.available_langs, ARRAY[]::TEXT[]) AS available_langs,
       CASE WHEN $1 <> 'ar' AND ct.category_id IS NOT NULL THEN $1 ELSE 'ar' END AS name_lang
FROM categories c
LEFT JOIN category_translations ct
    ON ct.category_id = c.id AND ct.lang = $1 AND ct.is_deleted = false AND $1 <> 'ar'
LEFT JOIN LATERAL (
    SELECT array_agg(lang ORDER BY lang) AS available_langs
    FROM category_translations
    WHERE category_id = c.id AND is_deleted = false
) av ON true
WHERE c.is_deleted = false
ORDER BY display_order ASC NULLS LAST, id ASC`

	rows, err := r.Pool.Query(ctx, sqlText, lang)
	if err != nil {
		return nil, fmt.Errorf("ReaderRepo - ListCategories - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	categories := make([]entity.Category, 0)
	for rows.Next() {
		var category entity.Category
		var displayOrder sql.NullInt64
		var translationStatus sql.NullString
		var reviewedBy sql.NullString
		var reviewedAt sql.NullTime
		var requestedLang string
		var displayLang string
		var isFallback bool
		var availableLangs []string
		var nameLang string

		if err = rows.Scan(
			&category.ID,
			&category.Name,
			&displayOrder,
			&translationStatus,
			&reviewedBy,
			&reviewedAt,
			&category.IsDeleted,
			&category.UpdatedAt,
			&requestedLang,
			&displayLang,
			&isFallback,
			&availableLangs,
			&nameLang,
		); err != nil {
			return nil, fmt.Errorf("ReaderRepo - ListCategories - rows.Scan: %w", err)
		}

		category.DisplayOrder = nullableInt(displayOrder)
		category.TranslationStatus = nullableString(translationStatus)
		category.TranslationReviewedBy = nullableString(reviewedBy)
		category.TranslationReviewedAt = nullableTime(reviewedAt)
		category.Localization = localizationMeta(
			requestedLang,
			displayLang,
			isFallback,
			availableLangs,
			map[string]string{"name": nameLang},
		)
		categories = append(categories, category)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ReaderRepo - ListCategories - rows.Err: %w", err)
	}

	return categories, nil
}

// ListAuthors returns paginated authors.
func (r *ReaderRepo) ListAuthors(ctx context.Context, filter repo.AuthorFilter) ([]entity.Author, int, error) {
	countBuilder := r.Builder.
		Select("COUNT(*)").
		From("authors a").
		Where(sq.Eq{"a.is_deleted": false})

	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		condition := `(a.name ILIKE ?
			OR COALESCE(a.biography, '') ILIKE ?
			OR EXISTS (
				SELECT 1
				FROM author_translations at_any
				WHERE at_any.author_id = a.id
				  AND at_any.is_deleted = false
				  AND (at_any.name ILIKE ? OR COALESCE(at_any.biography, '') ILIKE ?)
			))`
		countBuilder = countBuilder.Where(condition, like, like, like, like)
	}

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListAuthors - count: %w", err)
	}

	whereSQL := "WHERE a.is_deleted = false"
	args := []any{filter.Lang}
	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		whereSQL += fmt.Sprintf(` AND (a.name ILIKE $%d
			OR COALESCE(a.biography, '') ILIKE $%d
			OR EXISTS (
				SELECT 1
				FROM author_translations at_any
				WHERE at_any.author_id = a.id
				  AND at_any.is_deleted = false
				  AND (at_any.name ILIKE $%d OR COALESCE(at_any.biography, '') ILIKE $%d)
			))`, len(args)+1, len(args)+1, len(args)+1, len(args)+1)
		args = append(args, like)
	}
	limitIndex := len(args) + 1
	offsetIndex := len(args) + 2
	args = append(args, filter.Limit, filter.Offset)

	sqlText := fmt.Sprintf(`
SELECT a.id,
       CASE WHEN $1 <> 'ar' AND at.author_id IS NOT NULL THEN at.name ELSE a.name END AS name,
       CASE WHEN $1 <> 'ar' AND at.biography IS NOT NULL THEN at.biography ELSE a.biography END AS biography,
       CASE WHEN $1 <> 'ar' AND at.death_text IS NOT NULL THEN at.death_text ELSE a.death_text END AS death_text,
       a.death_number,
       at.translation_status,
       at.reviewed_by,
       at.reviewed_at,
       a.is_deleted,
       a.updated_at,
       $1 AS requested_lang,
       CASE WHEN $1 <> 'ar' AND at.author_id IS NOT NULL THEN $1 ELSE 'ar' END AS display_lang,
       ($1 <> 'ar' AND at.author_id IS NULL) AS is_fallback,
       COALESCE(av.available_langs, ARRAY[]::TEXT[]) AS available_langs,
       CASE WHEN $1 <> 'ar' AND at.author_id IS NOT NULL THEN $1 ELSE 'ar' END AS name_lang,
       CASE WHEN $1 <> 'ar' AND at.biography IS NOT NULL THEN $1 ELSE 'ar' END AS biography_lang,
       CASE WHEN $1 <> 'ar' AND at.death_text IS NOT NULL THEN $1 ELSE 'ar' END AS death_text_lang
FROM authors a
LEFT JOIN author_translations at
    ON at.author_id = a.id AND at.lang = $1 AND at.is_deleted = false AND $1 <> 'ar'
LEFT JOIN LATERAL (
    SELECT array_agg(lang ORDER BY lang) AS available_langs
    FROM author_translations
    WHERE author_id = a.id AND is_deleted = false
) av ON true
%s
ORDER BY name ASC
LIMIT $%d OFFSET $%d`, whereSQL, limitIndex, offsetIndex)

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListAuthors - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	authors := make([]entity.Author, 0, filter.Limit)
	for rows.Next() {
		author, err := scanAuthor(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("ReaderRepo - ListAuthors - scanAuthor: %w", err)
		}

		authors = append(authors, author)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListAuthors - rows.Err: %w", err)
	}

	return authors, total, nil
}

// ListBooks returns paginated catalog books.
func (r *ReaderRepo) ListBooks(ctx context.Context, filter repo.BookFilter) ([]entity.Book, int, error) {
	countBuilder := r.Builder.
		Select("COUNT(*)").
		From("books b").
		Join("book_publications p ON p.book_id = b.id AND p.status = 'published'").
		LeftJoin("book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'").
		LeftJoin("book_production_projects bpp ON bpp.book_id = b.id AND bpp.lang = ? AND bpp.workflow_status <> 'archived' AND ? <> 'ar'", filter.Lang, filter.Lang).
		LeftJoin("book_metadata_translations bmt ON bmt.book_id = b.id AND bmt.lang = ? AND bmt.is_deleted = false AND bpp.publication_status = 'published'", filter.Lang).
		LeftJoin("authors a ON a.id = b.author_id").
		LeftJoin("author_translations at ON at.author_id = a.id AND at.lang = ? AND at.is_deleted = false AND bpp.publication_status = 'published'", filter.Lang).
		LeftJoin("categories c ON c.id = COALESCE(me.category_id, b.category_id)").
		LeftJoin("category_translations ct ON ct.category_id = c.id AND ct.lang = ? AND ct.is_deleted = false AND bpp.publication_status = 'published'", filter.Lang).
		Where(sq.Eq{"b.is_deleted": false})

	dataBuilder := r.bookSelectBuilder(filter.Lang).
		Where(sq.Eq{"b.is_deleted": false}).
		OrderBy(
			"p.featured DESC",
			"p.sort_order ASC NULLS LAST",
			"b.has_content DESC",
			"COALESCE(bmt.display_title, me.display_title, b.name) ASC",
			"b.id ASC",
		).
		Limit(filter.Limit).
		Offset(filter.Offset)

	countBuilder, dataBuilder = applyBookFilter(countBuilder, dataBuilder, filter)

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListBooks - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListBooks - dataBuilder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListBooks - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	books := make([]entity.Book, 0, filter.Limit)
	for rows.Next() {
		book, err := scanBook(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("ReaderRepo - ListBooks - scanBook: %w", err)
		}

		books = append(books, book)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListBooks - rows.Err: %w", err)
	}

	return books, total, nil
}

// GetBookCatalogStats returns full published catalog aggregate counts.
func (r *ReaderRepo) GetBookCatalogStats(ctx context.Context, lang string) (entity.BookCatalogStats, error) {
	stats := entity.BookCatalogStats{
		Scope:      "catalog_global",
		ByCategory: []entity.BookCategoryStat{},
	}
	if err := r.scanBookCatalogTotals(ctx, lang, &stats); err != nil {
		return entity.BookCatalogStats{}, err
	}

	categories, err := r.bookCatalogCategoryStats(ctx, lang)
	if err != nil {
		return entity.BookCatalogStats{}, err
	}

	stats.ByCategory = categories

	return stats, nil
}

func (r *ReaderRepo) scanBookCatalogTotals(ctx context.Context, lang string, stats *entity.BookCatalogStats) error {
	err := r.Pool.QueryRow(ctx, bookCatalogStatsTotalsSQL, lang).Scan(
		&stats.TotalBooks,
		&stats.PublishedCount,
		&stats.CatalogPublishedCount,
		&stats.ProductionPublishedCount,
		&stats.AuthorCount,
		&stats.CategoryCount,
		&stats.WithContentCount,
		&stats.CoverageCount,
	)
	if err != nil {
		return fmt.Errorf("ReaderRepo - GetBookCatalogStats - totals: %w", err)
	}

	return nil
}

func (r *ReaderRepo) bookCatalogCategoryStats(ctx context.Context, lang string) ([]entity.BookCategoryStat, error) {
	rows, err := r.Pool.Query(ctx, bookCatalogStatsCategorySQL, lang)
	if err != nil {
		return nil, fmt.Errorf("ReaderRepo - GetBookCatalogStats - categories: %w", err)
	}

	defer rows.Close()

	categories := make([]entity.BookCategoryStat, 0)

	for rows.Next() {
		var (
			item         entity.BookCategoryStat
			categoryID   sql.NullInt64
			categoryName sql.NullString
		)
		if err = rows.Scan(
			&categoryID,
			&categoryName,
			&item.Total,
			&item.PublishedCount,
			&item.CatalogPublishedCount,
			&item.ProductionPublishedCount,
			&item.CoverageCount,
		); err != nil {
			return nil, fmt.Errorf("ReaderRepo - GetBookCatalogStats - scan category: %w", err)
		}

		item.CategoryID = nullableInt(categoryID)
		item.CategoryName = nullableString(categoryName)
		categories = append(categories, item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ReaderRepo - GetBookCatalogStats - rows.Err: %w", err)
	}

	return categories, nil
}

func (r *ReaderRepo) GetBook(ctx context.Context, bookID int, lang string) (entity.Book, error) {
	sqlText, args, err := r.bookSelectBuilder(lang).
		Where(sq.Eq{"b.id": bookID, "b.is_deleted": false}).
		ToSql()
	if err != nil {
		return entity.Book{}, fmt.Errorf("ReaderRepo - GetBook - r.Builder: %w", err)
	}

	book, err := scanBook(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.Book{}, entity.ErrBookNotFound
		}

		return entity.Book{}, fmt.Errorf("ReaderRepo - GetBook - scanBook: %w", err)
	}

	coverage, err := r.getBookLanguageCoverage(ctx, bookID)
	if err != nil {
		return entity.Book{}, err
	}
	book.LanguageCoverage = coverage

	return book, nil
}

// ListBookPages returns paginated pages for a book.
func (r *ReaderRepo) ListBookPages(ctx context.Context, bookID int, filter repo.PageFilter) ([]entity.BookPage, int, error) {
	countBuilder := r.Builder.
		Select("COUNT(*)").
		From("book_pages bp").
		Join("book_publications p ON p.book_id = bp.book_id AND p.status = 'published'").
		Where(sq.Eq{"bp.book_id": bookID, "bp.is_deleted": false})
	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListBookPages - count: %w", err)
	}

	sqlText, args, err := r.pageSelectBuilder().
		Where(sq.Eq{"bp.book_id": bookID, "bp.is_deleted": false}).
		OrderBy("bp.page_id ASC").
		Limit(filter.Limit).
		Offset(filter.Offset).
		ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListBookPages - r.Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListBookPages - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	pages := make([]entity.BookPage, 0, filter.Limit)
	for rows.Next() {
		page, err := scanPage(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("ReaderRepo - ListBookPages - scanPage: %w", err)
		}

		pages = append(pages, page)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListBookPages - rows.Err: %w", err)
	}

	return pages, total, nil
}

// GetBookPage returns one page.
func (r *ReaderRepo) GetBookPage(ctx context.Context, bookID, pageID int) (entity.BookPage, error) {
	sqlText, args, err := r.pageSelectBuilder().
		Where(sq.Eq{"bp.book_id": bookID, "bp.page_id": pageID, "bp.is_deleted": false}).
		ToSql()
	if err != nil {
		return entity.BookPage{}, fmt.Errorf("ReaderRepo - GetBookPage - r.Builder: %w", err)
	}

	page, err := scanPage(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookPage{}, entity.ErrPageNotFound
		}

		return entity.BookPage{}, fmt.Errorf("ReaderRepo - GetBookPage - scanPage: %w", err)
	}

	return page, nil
}

// ListBookHeadings returns a flat heading tree for a book.
func (r *ReaderRepo) ListBookHeadings(ctx context.Context, bookID int, query string) ([]entity.BookHeading, error) {
	builder := r.headingSelectBuilder().
		Where(sq.Eq{"h.book_id": bookID, "h.is_deleted": false}).
		OrderBy("h.ordinal ASC", "h.heading_id ASC")

	if query != "" {
		builder = builder.Where("COALESCE(he.content, h.content) ILIKE ?", "%"+query+"%")
	}

	sqlText, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("ReaderRepo - ListBookHeadings - r.Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("ReaderRepo - ListBookHeadings - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	headings := make([]entity.BookHeading, 0)
	for rows.Next() {
		heading, err := scanHeading(rows)
		if err != nil {
			return nil, fmt.Errorf("ReaderRepo - ListBookHeadings - scanHeading: %w", err)
		}

		headings = append(headings, heading)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ReaderRepo - ListBookHeadings - rows.Err: %w", err)
	}

	return headings, nil
}

// ListTOCEntries returns published effective TOC rows plus requested-language asset flags.
func (r *ReaderRepo) ListTOCEntries(
	ctx context.Context,
	bookID int,
	lang string,
	includeAudio bool,
) ([]entity.BookTOCEntry, error) {
	if err := r.ensurePublishedBook(ctx, bookID); err != nil {
		return nil, err
	}

	sqlText := `
SELECT h.book_id,
       h.heading_id,
       h.parent_id,
       h.page_id,
       h.depth,
       h.ordinal,
       COALESCE(NULLIF(st.title, ''), he.content, h.content) AS title,
       $2 AS requested_lang,
       CASE WHEN NULLIF(st.title, '') IS NOT NULL THEN $2 ELSE 'ar' END AS title_lang,
       ($2 <> 'ar' AND NULLIF(st.title, '') IS NULL) AS is_title_fallback,
       (COALESCE(bhs_lang.book_id, bhs_ar.book_id) IS NOT NULL) AS has_summary,
       CASE
           WHEN bhs_lang.book_id IS NOT NULL THEN bhs_lang.lang
           WHEN bhs_ar.book_id IS NOT NULL THEN bhs_ar.lang
           ELSE NULL
       END AS summary_lang,
       COALESCE(bhs_lang.summary, bhs_ar.summary) AS summary,
       COALESCE(bhs_lang.summary_status, bhs_ar.summary_status) AS summary_status,
       COALESCE(bhs_lang.reviewed_by, bhs_ar.reviewed_by) AS summary_reviewed_by,
       COALESCE(bhs_lang.reviewed_at, bhs_ar.reviewed_at) AS summary_reviewed_at,
       (st.book_id IS NOT NULL) AS has_translation,
       ($2 <> 'ar' AND st.book_id IS NULL) AS translation_missing,
       COALESCE(st_av.available_langs, ARRAY[]::TEXT[]) AS available_translation_langs,
       COALESCE(bhs_av.available_langs, ARRAY[]::TEXT[]) AS available_summary_langs,
       COALESCE(sa_av.available_langs, ARRAY[]::TEXT[]) AS available_audio_langs,
       st.translation_status,
       st.reviewed_by,
       st.reviewed_at,
       (sa.book_id IS NOT NULL) AS has_audio,
       sa.lang,
       sa.url,
       sa.narrator,
       sa.duration_seconds,
       sa.mime_type,
       sa.metadata,
       sa.updated_at
FROM book_headings h
LEFT JOIN book_heading_edits he
    ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'
LEFT JOIN book_production_projects bpp
    ON bpp.book_id = h.book_id
   AND bpp.lang = $2
   AND bpp.publication_status = 'published'
   AND bpp.workflow_status <> 'archived'
   AND $2 <> 'ar'
LEFT JOIN section_translations st
    ON st.book_id = h.book_id AND st.heading_id = h.heading_id AND st.lang = $2 AND st.is_deleted = false AND bpp.id IS NOT NULL
LEFT JOIN book_heading_summaries bhs_lang
    ON bhs_lang.book_id = h.book_id AND bhs_lang.heading_id = h.heading_id AND bhs_lang.lang = $2 AND bhs_lang.is_deleted = false AND ($2 = 'ar' OR bpp.id IS NOT NULL)
LEFT JOIN book_heading_summaries bhs_ar
    ON bhs_ar.book_id = h.book_id AND bhs_ar.heading_id = h.heading_id AND bhs_ar.lang = 'ar' AND bhs_ar.is_deleted = false
LEFT JOIN (
    SELECT st_lang.heading_id,
           array_agg(st_lang.lang ORDER BY st_lang.lang) AS available_langs
    FROM section_translations st_lang
    LEFT JOIN book_production_projects bpp_lang
      ON bpp_lang.book_id = st_lang.book_id
     AND bpp_lang.lang = st_lang.lang
     AND bpp_lang.publication_status = 'published'
     AND bpp_lang.workflow_status <> 'archived'
    WHERE st_lang.book_id = $1
      AND st_lang.is_deleted = false
      AND (st_lang.lang = 'ar' OR bpp_lang.id IS NOT NULL)
    GROUP BY st_lang.heading_id
) st_av ON st_av.heading_id = h.heading_id
LEFT JOIN (
    SELECT bhs_langs.heading_id,
           array_agg(bhs_langs.lang ORDER BY bhs_langs.lang) AS available_langs
    FROM book_heading_summaries bhs_langs
    LEFT JOIN book_production_projects bpp_lang
      ON bpp_lang.book_id = bhs_langs.book_id
     AND bpp_lang.lang = bhs_langs.lang
     AND bpp_lang.publication_status = 'published'
     AND bpp_lang.workflow_status <> 'archived'
    WHERE bhs_langs.book_id = $1
      AND bhs_langs.is_deleted = false
      AND (bhs_langs.lang = 'ar' OR bpp_lang.id IS NOT NULL)
    GROUP BY bhs_langs.heading_id
) bhs_av ON bhs_av.heading_id = h.heading_id
LEFT JOIN (
    SELECT sa_lang.heading_id,
           array_agg(sa_lang.lang ORDER BY sa_lang.lang) AS available_langs
    FROM section_audio sa_lang
    LEFT JOIN book_production_projects bpp_lang
      ON bpp_lang.book_id = sa_lang.book_id
     AND bpp_lang.lang = sa_lang.lang
     AND bpp_lang.publication_status = 'published'
     AND bpp_lang.workflow_status <> 'archived'
    WHERE sa_lang.book_id = $1
      AND sa_lang.is_deleted = false
      AND (sa_lang.lang = 'ar' OR bpp_lang.id IS NOT NULL)
    GROUP BY sa_lang.heading_id
) sa_av ON sa_av.heading_id = h.heading_id
LEFT JOIN section_audio sa
    ON sa.book_id = h.book_id AND sa.heading_id = h.heading_id AND sa.lang = $2 AND sa.is_deleted = false AND ($2 = 'ar' OR bpp.id IS NOT NULL)
WHERE h.book_id = $1 AND h.is_deleted = false
ORDER BY h.ordinal ASC, h.heading_id ASC`

	rows, err := r.Pool.Query(ctx, sqlText, bookID, lang)
	if err != nil {
		return nil, fmt.Errorf("ReaderRepo - ListTOCEntries - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	entries := make([]entity.BookTOCEntry, 0)
	for rows.Next() {
		entry, err := scanTOCEntry(rows, includeAudio)
		if err != nil {
			return nil, fmt.Errorf("ReaderRepo - ListTOCEntries - scanTOCEntry: %w", err)
		}

		entries = append(entries, entry)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ReaderRepo - ListTOCEntries - rows.Err: %w", err)
	}

	return entries, nil
}

// GetSection returns original section content plus optional translation/audio.
func (r *ReaderRepo) GetSection(ctx context.Context, bookID, headingID int, lang string) (entity.BookSection, error) {
	sqlText := `
SELECT h.book_id,
       h.heading_id,
       h.parent_id,
       h.page_id,
       h.depth,
       h.ordinal,
       COALESCE(NULLIF(st_title.title, ''), he.content, h.content) AS content,
       h.is_deleted,
       h.updated_at,
       hr.start_page_id,
       hr.end_page_id,
       hr.start_anchor,
       hr.end_anchor,
       CASE WHEN NULLIF(st_title.title, '') IS NOT NULL THEN $3 ELSE 'ar' END AS title_lang,
       (COALESCE(bhs_lang.book_id, bhs_ar.book_id) IS NOT NULL) AS has_summary,
       CASE
           WHEN bhs_lang.book_id IS NOT NULL THEN bhs_lang.lang
           WHEN bhs_ar.book_id IS NOT NULL THEN bhs_ar.lang
           ELSE NULL
       END AS summary_lang,
	       COALESCE(st_av.available_langs, ARRAY[]::TEXT[]) AS available_translation_langs,
	       COALESCE(bhs_av.available_langs, ARRAY[]::TEXT[]) AS available_summary_langs,
	       COALESCE(sa_av.available_langs, ARRAY[]::TEXT[]) AS available_audio_langs,
	       COALESCE(ehe.content, eh.content, '') AS end_heading_content
FROM book_headings h
JOIN book_heading_ranges hr ON hr.book_id = h.book_id AND hr.heading_id = h.heading_id
JOIN book_publications p ON p.book_id = h.book_id AND p.status = 'published'
LEFT JOIN book_heading_edits he ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'
LEFT JOIN book_headings eh
    ON eh.book_id = h.book_id
   AND eh.heading_id = CASE
       WHEN hr.end_anchor ~ '^toc-[0-9]+$' THEN substring(hr.end_anchor FROM 5)::int
       ELSE NULL
   END
LEFT JOIN book_heading_edits ehe ON ehe.book_id = eh.book_id AND ehe.heading_id = eh.heading_id AND ehe.status = 'published'
LEFT JOIN book_production_projects bpp
    ON bpp.book_id = h.book_id
   AND bpp.lang = $3
   AND bpp.publication_status = 'published'
   AND bpp.workflow_status <> 'archived'
   AND $3 <> 'ar'
LEFT JOIN section_translations st_title
    ON st_title.book_id = h.book_id AND st_title.heading_id = h.heading_id AND st_title.lang = $3 AND st_title.is_deleted = false AND bpp.id IS NOT NULL
LEFT JOIN book_heading_summaries bhs_lang
    ON bhs_lang.book_id = h.book_id AND bhs_lang.heading_id = h.heading_id AND bhs_lang.lang = $3 AND bhs_lang.is_deleted = false AND ($3 = 'ar' OR bpp.id IS NOT NULL)
LEFT JOIN book_heading_summaries bhs_ar
    ON bhs_ar.book_id = h.book_id AND bhs_ar.heading_id = h.heading_id AND bhs_ar.lang = 'ar' AND bhs_ar.is_deleted = false
LEFT JOIN LATERAL (
    SELECT array_agg(st_lang.lang ORDER BY st_lang.lang) AS available_langs
    FROM section_translations st_lang
    LEFT JOIN book_production_projects bpp_lang
      ON bpp_lang.book_id = st_lang.book_id
     AND bpp_lang.lang = st_lang.lang
     AND bpp_lang.publication_status = 'published'
     AND bpp_lang.workflow_status <> 'archived'
    WHERE st_lang.book_id = h.book_id
      AND st_lang.heading_id = h.heading_id
      AND st_lang.is_deleted = false
      AND (st_lang.lang = 'ar' OR bpp_lang.id IS NOT NULL)
) st_av ON true
LEFT JOIN LATERAL (
    SELECT array_agg(bhs_langs.lang ORDER BY bhs_langs.lang) AS available_langs
    FROM book_heading_summaries bhs_langs
    LEFT JOIN book_production_projects bpp_lang
      ON bpp_lang.book_id = bhs_langs.book_id
     AND bpp_lang.lang = bhs_langs.lang
     AND bpp_lang.publication_status = 'published'
     AND bpp_lang.workflow_status <> 'archived'
    WHERE bhs_langs.book_id = h.book_id
      AND bhs_langs.heading_id = h.heading_id
      AND bhs_langs.is_deleted = false
      AND (bhs_langs.lang = 'ar' OR bpp_lang.id IS NOT NULL)
) bhs_av ON true
LEFT JOIN LATERAL (
    SELECT array_agg(sa_lang.lang ORDER BY sa_lang.lang) AS available_langs
    FROM section_audio sa_lang
    LEFT JOIN book_production_projects bpp_lang
      ON bpp_lang.book_id = sa_lang.book_id
     AND bpp_lang.lang = sa_lang.lang
     AND bpp_lang.publication_status = 'published'
     AND bpp_lang.workflow_status <> 'archived'
    WHERE sa_lang.book_id = h.book_id
      AND sa_lang.heading_id = h.heading_id
      AND sa_lang.is_deleted = false
      AND (sa_lang.lang = 'ar' OR bpp_lang.id IS NOT NULL)
) sa_av ON true
WHERE h.book_id = $1 AND h.heading_id = $2 AND h.is_deleted = false`

	var heading entity.BookHeading
	var parentID sql.NullInt64
	var startAnchor sql.NullString
	var endAnchor sql.NullString
	var startPageID int
	var endPageID int
	var titleLang string
	var hasSummary bool
	var summaryLang sql.NullString
	var availableTranslationLangs []string
	var availableSummaryLangs []string
	var availableAudioLangs []string
	var endHeadingContent string

	err := r.Pool.QueryRow(ctx, sqlText, bookID, headingID, lang).Scan(
		&heading.BookID,
		&heading.HeadingID,
		&parentID,
		&heading.PageID,
		&heading.Depth,
		&heading.Ordinal,
		&heading.Content,
		&heading.IsDeleted,
		&heading.UpdatedAt,
		&startPageID,
		&endPageID,
		&startAnchor,
		&endAnchor,
		&titleLang,
		&hasSummary,
		&summaryLang,
		&availableTranslationLangs,
		&availableSummaryLangs,
		&availableAudioLangs,
		&endHeadingContent,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookSection{}, entity.ErrHeadingNotFound
		}

		return entity.BookSection{}, fmt.Errorf("ReaderRepo - GetSection - heading query: %w", err)
	}

	heading.ParentID = nullableInt(parentID)

	pages, err := r.pagesInRange(ctx, bookID, startPageID, endPageID)
	if err != nil {
		return entity.BookSection{}, err
	}

	var builder strings.Builder
	for i, page := range pages {
		if i > 0 {
			builder.WriteString("\n\n")
		}

		builder.WriteString(page.ContentHTML)
	}

	sourceContent := readerutil.SliceSectionContent(
		builder.String(),
		startAnchor.String,
		endAnchor.String,
		heading.Content,
		endHeadingContent,
	)
	structuredSource := readerutil.StructureSourceContent(sourceContent)

	section := entity.BookSection{
		BookID:                    bookID,
		HeadingID:                 headingID,
		Heading:                   heading,
		RequestedLang:             lang,
		TitleLang:                 titleLang,
		IsTitleFallback:           lang != "ar" && titleLang != lang,
		AvailableTranslationLangs: emptyStringSlice(availableTranslationLangs),
		AvailableSummaryLangs:     emptyStringSlice(availableSummaryLangs),
		StartPageID:               startPageID,
		EndPageID:                 endPageID,
		OriginalHTML:              structuredSource.HTML,
		OriginalText:              structuredSource.Text,
		OriginalFormat:            structuredSource.Format,
		OriginalBlocks:            sourceBlocks(structuredSource.Blocks),
		OriginalFootnotes:         sourceFootnotes(structuredSource.Footnotes),
	}

	translation, err := r.getSectionTranslation(ctx, bookID, headingID, lang)
	if err != nil {
		return entity.BookSection{}, err
	}

	audio, err := r.getSectionAudio(ctx, bookID, headingID, lang)
	if err != nil {
		return entity.BookSection{}, err
	}

	section.Translation = translation
	section.TranslationMissing = lang != "ar" && translation == nil
	section.Audio = audio
	section.Availability = entity.NewReaderAvailability(
		section.RequestedLang,
		section.TitleLang,
		section.IsTitleFallback,
		translation != nil,
		section.TranslationMissing,
		nullableString(summaryLang),
		hasSummary,
		audio != nil,
		section.AvailableTranslationLangs,
		section.AvailableSummaryLangs,
		emptyStringSlice(availableAudioLangs),
	)

	return section, nil
}

// CreateTranslationFeedback stores or updates a reader signal for one translated section.
func (r *ReaderRepo) CreateTranslationFeedback(
	ctx context.Context,
	feedback entity.TranslationFeedback,
) (entity.TranslationFeedback, error) {
	if err := r.ensurePublishedBook(ctx, feedback.BookID); err != nil {
		return entity.TranslationFeedback{}, err
	}
	if err := r.ensurePublishedHeading(ctx, feedback.BookID, feedback.HeadingID); err != nil {
		return entity.TranslationFeedback{}, err
	}
	if err := r.ensureSectionTranslation(ctx, feedback.BookID, feedback.HeadingID, feedback.Lang); err != nil {
		return entity.TranslationFeedback{}, err
	}

	sqlText := `
INSERT INTO translation_feedbacks (
    id, book_id, heading_id, lang, user_id, client_id, vote, reason, note, user_agent, client_ip, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now(), now())
RETURNING id, book_id, heading_id, lang, user_id, client_id, vote, reason, note, user_agent, client_ip, created_at, updated_at`
	if feedback.UserID != nil {
		sqlText = `
INSERT INTO translation_feedbacks (
    id, book_id, heading_id, lang, user_id, client_id, vote, reason, note, user_agent, client_ip, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now(), now())
ON CONFLICT (book_id, heading_id, lang, user_id) WHERE user_id IS NOT NULL
DO UPDATE SET
    vote = EXCLUDED.vote,
    reason = EXCLUDED.reason,
    note = EXCLUDED.note,
    user_agent = EXCLUDED.user_agent,
    client_ip = EXCLUDED.client_ip,
    status = 'open',
    resolved_by = NULL,
    resolved_at = NULL,
    resolution_note = NULL,
    updated_at = now()
RETURNING id, book_id, heading_id, lang, user_id, client_id, vote, reason, note, user_agent, client_ip, created_at, updated_at`
	} else if feedback.ClientID != nil {
		sqlText = `
INSERT INTO translation_feedbacks (
    id, book_id, heading_id, lang, user_id, client_id, vote, reason, note, user_agent, client_ip, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now(), now())
ON CONFLICT (book_id, heading_id, lang, client_id) WHERE user_id IS NULL AND client_id IS NOT NULL
DO UPDATE SET
    vote = EXCLUDED.vote,
    reason = EXCLUDED.reason,
    note = EXCLUDED.note,
    user_agent = EXCLUDED.user_agent,
    client_ip = EXCLUDED.client_ip,
    status = 'open',
    resolved_by = NULL,
    resolved_at = NULL,
    resolution_note = NULL,
    updated_at = now()
RETURNING id, book_id, heading_id, lang, user_id, client_id, vote, reason, note, user_agent, client_ip, created_at, updated_at`
	}

	created, err := scanTranslationFeedback(r.Pool.QueryRow(
		ctx,
		sqlText,
		feedback.ID,
		feedback.BookID,
		feedback.HeadingID,
		feedback.Lang,
		feedback.UserID,
		feedback.ClientID,
		feedback.Vote,
		feedback.Reason,
		feedback.Note,
		feedback.UserAgent,
		feedback.ClientIP,
	))
	if err != nil {
		return entity.TranslationFeedback{}, fmt.Errorf("ReaderRepo - CreateTranslationFeedback - scanTranslationFeedback: %w", err)
	}

	return created, nil
}

func (r *ReaderRepo) count(ctx context.Context, builder sq.SelectBuilder) (int, error) {
	sqlText, args, err := builder.ToSql()
	if err != nil {
		return 0, fmt.Errorf("building count query: %w", err)
	}

	var total int
	if err = r.Pool.QueryRow(ctx, sqlText, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("executing count query: %w", err)
	}

	return total, nil
}

func (r *ReaderRepo) ensurePublishedBook(ctx context.Context, bookID int) error {
	var exists int

	err := r.Pool.QueryRow(
		ctx, `
SELECT 1
FROM books b
JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
WHERE b.id = $1 AND b.is_deleted = false`,
		bookID,
	).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.ErrBookNotFound
		}

		return fmt.Errorf("ReaderRepo - ensurePublishedBook - QueryRow: %w", err)
	}

	return nil
}

func (r *ReaderRepo) ensurePublishedHeading(ctx context.Context, bookID, headingID int) error {
	var exists bool
	if err := r.Pool.QueryRow(
		ctx, `
SELECT EXISTS (
    SELECT 1
    FROM book_headings
    WHERE book_id = $1 AND heading_id = $2 AND is_deleted = false
)`,
		bookID,
		headingID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("ReaderRepo - ensurePublishedHeading - QueryRow: %w", err)
	}
	if !exists {
		return entity.ErrHeadingNotFound
	}

	return nil
}

func (r *ReaderRepo) ensureSectionTranslation(ctx context.Context, bookID, headingID int, lang string) error {
	var exists bool
	if err := r.Pool.QueryRow(
		ctx, `
SELECT EXISTS (
    SELECT 1
    FROM section_translations
    WHERE book_id = $1
      AND heading_id = $2
      AND lang = $3
      AND is_deleted = false
      AND EXISTS (
          SELECT 1
          FROM book_production_projects p
          WHERE p.book_id = section_translations.book_id
            AND p.lang = section_translations.lang
            AND p.publication_status = 'published'
            AND p.workflow_status <> 'archived'
      )
)`,
		bookID,
		headingID,
		lang,
	).Scan(&exists); err != nil {
		return fmt.Errorf("ReaderRepo - ensureSectionTranslation - QueryRow: %w", err)
	}
	if !exists {
		return entity.ErrTranslationNotFound
	}

	return nil
}

func (r *ReaderRepo) bookSelectBuilder(lang string) sq.SelectBuilder {
	return r.Builder.
		Select(
			"b.id",
			"CASE WHEN bmt.book_id IS NOT NULL THEN bmt.display_title ELSE COALESCE(me.display_title, b.name) END AS name",
			"COALESCE(me.category_id, b.category_id) AS category_id",
			"CASE WHEN ct.category_id IS NOT NULL THEN ct.name ELSE c.name END AS category_name",
			"b.author_id",
			"CASE WHEN at.author_id IS NOT NULL THEN at.name ELSE a.name END AS author_name",
			"b.type",
			"b.printed",
			"b.minor_release",
			"b.major_release",
			"COALESCE(bmt.bibliography, me.bibliography, b.bibliography) AS bibliography",
			"COALESCE(bmt.hint, me.hint, b.hint) AS hint",
			"b.pdf_links",
			"b.metadata",
			"b.source_date",
			"COALESCE(bmt.description, me.description) AS description",
			"me.cover_url",
			"me.notes AS editorial_notes",
			"bmt.translation_status",
			"bmt.reviewed_by",
			"bmt.reviewed_at",
			"p.status AS publication_status",
			"p.status AS catalog_publication_status",
			"bpp.workflow_status AS production_workflow_status",
			"bpp.publication_status AS production_publication_status",
			"p.featured",
			"p.sort_order",
			"b.has_content",
			"b.is_deleted",
			"b.updated_at",
		).
		Column(sq.Expr("? AS requested_lang", lang)).
		Column(sq.Expr(
			"CASE WHEN ? <> 'ar' AND bmt.book_id IS NOT NULL THEN ? ELSE 'ar' END AS display_lang",
			lang,
			lang,
		)).
		Column(sq.Expr("(? <> 'ar' AND bmt.book_id IS NULL) AS is_fallback", lang)).
		Column("COALESCE(bmt_av.available_langs, ARRAY[]::TEXT[]) AS available_langs").
		Column(sq.Expr("CASE WHEN ? <> 'ar' AND bmt.book_id IS NOT NULL THEN ? ELSE 'ar' END AS name_lang", lang, lang)).
		Column(sq.Expr(
			"CASE WHEN ? <> 'ar' AND ct.category_id IS NOT NULL THEN ? ELSE 'ar' END AS category_name_lang",
			lang,
			lang,
		)).
		Column(sq.Expr(
			"CASE WHEN ? <> 'ar' AND at.author_id IS NOT NULL THEN ? ELSE 'ar' END AS author_name_lang",
			lang,
			lang,
		)).
		Column(sq.Expr(
			"CASE WHEN ? <> 'ar' AND bmt.bibliography IS NOT NULL THEN ? ELSE 'ar' END AS bibliography_lang",
			lang,
			lang,
		)).
		Column(sq.Expr(
			"CASE WHEN ? <> 'ar' AND bmt.hint IS NOT NULL THEN ? ELSE 'ar' END AS hint_lang",
			lang,
			lang,
		)).
		Column(sq.Expr(
			"CASE WHEN ? <> 'ar' AND bmt.description IS NOT NULL THEN ? ELSE 'ar' END AS description_lang",
			lang,
			lang,
		)).
		Column(sq.Expr(
			`CASE
					WHEN ? = 'ar' THEN 'source'
					WHEN bpp.id IS NULL THEN 'candidate'
					WHEN bpp.publication_status = 'published' THEN 'published'
					ELSE bpp.workflow_status
				END AS production_status`,
			lang,
		)).
		From("books b").
		Join("book_publications p ON p.book_id = b.id AND p.status = 'published'").
		LeftJoin("book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'").
		LeftJoin("book_production_projects bpp ON bpp.book_id = b.id AND bpp.lang = ? AND bpp.workflow_status <> 'archived' AND ? <> 'ar'", lang, lang).
		LeftJoin("book_metadata_translations bmt ON bmt.book_id = b.id AND bmt.lang = ? AND bmt.is_deleted = false AND bpp.publication_status = 'published'", lang).
		LeftJoin(`LATERAL (
			SELECT array_agg(bmt_lang.lang ORDER BY bmt_lang.lang) AS available_langs
			FROM book_metadata_translations bmt_lang
			JOIN book_production_projects bpp_lang
			  ON bpp_lang.book_id = bmt_lang.book_id
			 AND bpp_lang.lang = bmt_lang.lang
			 AND bpp_lang.publication_status = 'published'
			 AND bpp_lang.workflow_status <> 'archived'
			WHERE bmt_lang.book_id = b.id
			  AND bmt_lang.is_deleted = false
			) bmt_av ON true`).
		LeftJoin("authors a ON a.id = b.author_id").
		LeftJoin("author_translations at ON at.author_id = a.id AND at.lang = ? AND at.is_deleted = false AND bpp.publication_status = 'published'", lang).
		LeftJoin("categories c ON c.id = COALESCE(me.category_id, b.category_id)").
		LeftJoin("category_translations ct ON ct.category_id = c.id AND ct.lang = ? AND ct.is_deleted = false AND bpp.publication_status = 'published'", lang)
}

func (r *ReaderRepo) pageSelectBuilder() sq.SelectBuilder {
	return r.Builder.Select(
		"bp.book_id",
		"bp.page_id",
		"bp.part",
		"bp.printed_page",
		"bp.number",
		"COALESCE(pe.content_html, bp.content_html) AS content_html",
		"COALESCE(pe.content_text, bp.content_text) AS content_text",
		"bp.services",
		"bp.is_deleted",
		"bp.updated_at",
	).From("book_pages bp").
		Join("book_publications p ON p.book_id = bp.book_id AND p.status = 'published'").
		LeftJoin("book_page_edits pe ON pe.book_id = bp.book_id AND pe.page_id = bp.page_id AND pe.status = 'published'")
}

func (r *ReaderRepo) headingSelectBuilder() sq.SelectBuilder {
	return r.Builder.Select(
		"h.book_id",
		"h.heading_id",
		"h.parent_id",
		"h.page_id",
		"h.depth",
		"h.ordinal",
		"COALESCE(he.content, h.content) AS content",
		"h.is_deleted",
		"h.updated_at",
	).From("book_headings h").
		Join("book_publications p ON p.book_id = h.book_id AND p.status = 'published'").
		LeftJoin("book_heading_edits he ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'")
}

func (r *ReaderRepo) pagesInRange(ctx context.Context, bookID, startPageID, endPageID int) ([]entity.BookPage, error) {
	sqlText, args, err := r.pageSelectBuilder().
		Where(sq.Eq{"bp.book_id": bookID, "bp.is_deleted": false}).
		Where("bp.page_id BETWEEN ? AND ?", startPageID, endPageID).
		OrderBy("bp.page_id ASC").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("ReaderRepo - pagesInRange - r.Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("ReaderRepo - pagesInRange - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	pages := make([]entity.BookPage, 0)
	for rows.Next() {
		page, err := scanPage(rows)
		if err != nil {
			return nil, fmt.Errorf("ReaderRepo - pagesInRange - scanPage: %w", err)
		}

		pages = append(pages, page)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ReaderRepo - pagesInRange - rows.Err: %w", err)
	}

	return pages, nil
}

func (r *ReaderRepo) getSectionTranslation(ctx context.Context, bookID, headingID int, lang string) (*entity.SectionTranslation, error) {
	if lang == "" || lang == "ar" {
		return nil, nil
	}

	sqlText := `
SELECT book_id, heading_id, lang, title, content, source, translation_status, reviewed_by, reviewed_at, metadata, updated_at
FROM section_translations
WHERE book_id = $1
  AND heading_id = $2
  AND lang = $3
  AND is_deleted = false
  AND EXISTS (
      SELECT 1
      FROM book_production_projects p
      WHERE p.book_id = section_translations.book_id
        AND p.lang = section_translations.lang
        AND p.publication_status = 'published'
        AND p.workflow_status <> 'archived'
  )`

	var translation entity.SectionTranslation
	var title sql.NullString
	var source sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime
	var metadata []byte

	err := r.Pool.QueryRow(ctx, sqlText, bookID, headingID, lang).Scan(
		&translation.BookID,
		&translation.HeadingID,
		&translation.Lang,
		&title,
		&translation.Content,
		&source,
		&translation.Status,
		&reviewedBy,
		&reviewedAt,
		&metadata,
		&translation.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, fmt.Errorf("ReaderRepo - getSectionTranslation - QueryRow: %w", err)
	}

	translation.Title = nullableString(title)
	translation.Source = nullableString(source)
	translation.ReviewedBy = nullableString(reviewedBy)
	translation.ReviewedAt = nullableTime(reviewedAt)
	translation.Metadata = entity.RawJSON(metadata)

	return &translation, nil
}

func (r *ReaderRepo) getSectionAudio(ctx context.Context, bookID, headingID int, lang string) (*entity.SectionAudio, error) {
	if lang == "" {
		return nil, nil
	}

	sqlText := `
SELECT book_id, heading_id, lang, url, narrator, duration_seconds, mime_type, metadata, updated_at
FROM section_audio
WHERE book_id = $1
  AND heading_id = $2
  AND lang = $3
  AND is_deleted = false
  AND (
      lang = 'ar'
      OR EXISTS (
          SELECT 1
          FROM book_production_projects p
          WHERE p.book_id = section_audio.book_id
            AND p.lang = section_audio.lang
            AND p.publication_status = 'published'
            AND p.workflow_status <> 'archived'
      )
  )`

	var audio entity.SectionAudio
	var narrator sql.NullString
	var duration sql.NullInt64
	var mimeType sql.NullString
	var metadata []byte

	err := r.Pool.QueryRow(ctx, sqlText, bookID, headingID, lang).Scan(
		&audio.BookID,
		&audio.HeadingID,
		&audio.Lang,
		&audio.URL,
		&narrator,
		&duration,
		&mimeType,
		&metadata,
		&audio.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, fmt.Errorf("ReaderRepo - getSectionAudio - QueryRow: %w", err)
	}

	audio.Narrator = nullableString(narrator)
	audio.DurationSeconds = nullableInt(duration)
	audio.MIMEType = nullableString(mimeType)
	audio.Metadata = entity.RawJSON(metadata)

	return &audio, nil
}

func (r *ReaderRepo) getBookLanguageCoverage(ctx context.Context, bookID int) ([]entity.LanguageCoverage, error) {
	sqlText := `
WITH langs AS (
    SELECT lang FROM section_translations WHERE book_id = $1 AND is_deleted = false
    UNION
    SELECT lang FROM book_heading_summaries WHERE book_id = $1 AND is_deleted = false
    UNION
    SELECT lang FROM section_audio WHERE book_id = $1 AND is_deleted = false
),
published_langs AS (
    SELECT lang
    FROM book_production_projects
    WHERE book_id = $1
      AND publication_status = 'published'
      AND workflow_status <> 'archived'
    UNION
    SELECT 'ar'
)
SELECT l.lang,
       COALESCE(st.translated_sections, 0) AS translated_sections,
       COALESCE(bhs.summarized_sections, 0) AS summarized_sections,
       COALESCE(sa.audio_sections, 0) AS audio_sections
FROM langs l
JOIN published_langs pl ON pl.lang = l.lang
LEFT JOIN (
    SELECT lang, COUNT(*)::INT AS translated_sections
    FROM section_translations
    WHERE book_id = $1 AND is_deleted = false
    GROUP BY lang
) st ON st.lang = l.lang
LEFT JOIN (
    SELECT lang, COUNT(*)::INT AS summarized_sections
    FROM book_heading_summaries
    WHERE book_id = $1 AND is_deleted = false
    GROUP BY lang
) bhs ON bhs.lang = l.lang
LEFT JOIN (
    SELECT lang, COUNT(*)::INT AS audio_sections
    FROM section_audio
    WHERE book_id = $1 AND is_deleted = false
    GROUP BY lang
) sa ON sa.lang = l.lang
ORDER BY l.lang`

	rows, err := r.Pool.Query(ctx, sqlText, bookID)
	if err != nil {
		return nil, fmt.Errorf("ReaderRepo - getBookLanguageCoverage - Query: %w", err)
	}
	defer rows.Close()

	coverage := make([]entity.LanguageCoverage, 0)
	for rows.Next() {
		var item entity.LanguageCoverage
		if err = rows.Scan(
			&item.Lang,
			&item.TranslatedSections,
			&item.SummarizedSections,
			&item.AudioSections,
		); err != nil {
			return nil, fmt.Errorf("ReaderRepo - getBookLanguageCoverage - rows.Scan: %w", err)
		}

		coverage = append(coverage, item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ReaderRepo - getBookLanguageCoverage - rows.Err: %w", err)
	}

	return coverage, nil
}

func applyBookFilter(countBuilder, dataBuilder sq.SelectBuilder, filter repo.BookFilter) (sq.SelectBuilder, sq.SelectBuilder) {
	if filter.Query != "" {
		condition, args := bookSearchCondition(filter.Query)
		countBuilder = countBuilder.Where(condition, args...)
		dataBuilder = dataBuilder.Where(condition, args...)
	}

	if filter.CategoryID != nil {
		countBuilder = countBuilder.Where("COALESCE(me.category_id, b.category_id) = ?", *filter.CategoryID)
		dataBuilder = dataBuilder.Where("COALESCE(me.category_id, b.category_id) = ?", *filter.CategoryID)
	}

	if filter.AuthorID != nil {
		countBuilder = countBuilder.Where(sq.Eq{"b.author_id": *filter.AuthorID})
		dataBuilder = dataBuilder.Where(sq.Eq{"b.author_id": *filter.AuthorID})
	}

	if filter.HasContent != nil {
		countBuilder = countBuilder.Where(sq.Eq{"b.has_content": *filter.HasContent})
		dataBuilder = dataBuilder.Where(sq.Eq{"b.has_content": *filter.HasContent})
	}

	return countBuilder, dataBuilder
}

func bookSearchCondition(query string) (condition string, args []any) {
	like := "%" + query + "%"
	condition = bookBaseSearchConditionSQL
	args = bookSearchArgs(like)

	if !containsArabic(query) {
		return condition, args
	}

	return bookArabicSearchCondition(condition, args, query)
}

func bookSearchArgs(like string) []any {
	return []any{
		like,
		like,
		like,
		like,
		like,
		like,
		like,
		like,
		like,
		like,
		like,
		like,
		like,
		like,
		like,
	}
}

func bookArabicSearchCondition(baseCondition string, args []any, query string) (
	condition string,
	searchArgs []any,
) {
	normalizedLike := "%" + normalizeReaderArabicSearchText(query) + "%"
	condition = baseCondition + ` OR (` + normalizedArabicSQL("COALESCE(bmt.display_title, me.display_title, b.name)") + ` ILIKE ?
		OR ` + normalizedArabicSQL("b.name") + ` ILIKE ?
		OR ` + normalizedArabicSQL("COALESCE(at.name, a.name)") + ` ILIKE ?
		OR ` + normalizedArabicSQL("a.name") + ` ILIKE ?
		OR ` + normalizedArabicSQL("COALESCE(ct.name, c.name)") + ` ILIKE ?
		OR ` + normalizedArabicSQL("c.name") + ` ILIKE ?)`
	searchArgs = args

	for range 6 {
		searchArgs = append(searchArgs, readerArabicSearchMarks, readerArabicVariantFrom, readerArabicVariantTo, normalizedLike)
	}

	return "(" + condition + ")", searchArgs
}

func normalizedArabicSQL(expr string) string {
	return "translate(translate(COALESCE(" + expr + ", ''), ?, ''), ?, ?)"
}

func containsArabic(value string) bool {
	for _, r := range value {
		if r >= '\u0600' && r <= '\u06ff' {
			return true
		}
	}

	return false
}

func normalizeReaderArabicSearchText(value string) string {
	var out strings.Builder

	replacer := strings.NewReplacer(
		"أ", "ا",
		"إ", "ا",
		"آ", "ا",
		"ٱ", "ا",
		"ؤ", "و",
		"ئ", "ي",
		"ء", "ا",
		"ى", "ي",
		"ة", "ه",
	)
	for _, r := range replacer.Replace(value) {
		if strings.ContainsRune(readerArabicSearchMarks, r) {
			continue
		}

		out.WriteRune(r)
	}

	return out.String()
}

func scanAuthor(row rowScanner) (entity.Author, error) {
	var author entity.Author
	var biography sql.NullString
	var deathText sql.NullString
	var deathNumber sql.NullInt64
	var translationStatus sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime
	var requestedLang string
	var displayLang string
	var isFallback bool
	var availableLangs []string
	var nameLang string
	var biographyLang string
	var deathTextLang string

	err := row.Scan(
		&author.ID,
		&author.Name,
		&biography,
		&deathText,
		&deathNumber,
		&translationStatus,
		&reviewedBy,
		&reviewedAt,
		&author.IsDeleted,
		&author.UpdatedAt,
		&requestedLang,
		&displayLang,
		&isFallback,
		&availableLangs,
		&nameLang,
		&biographyLang,
		&deathTextLang,
	)
	if err != nil {
		return entity.Author{}, err
	}

	author.Biography = nullableString(biography)
	author.DeathText = nullableString(deathText)
	author.DeathNumber = nullableInt(deathNumber)
	author.TranslationStatus = nullableString(translationStatus)
	author.TranslationReviewedBy = nullableString(reviewedBy)
	author.TranslationReviewedAt = nullableTime(reviewedAt)
	author.Localization = localizationMeta(
		requestedLang,
		displayLang,
		isFallback,
		availableLangs,
		map[string]string{
			"name":       nameLang,
			"biography":  biographyLang,
			"death_text": deathTextLang,
		},
	)

	return author, nil
}

func scanBook(row rowScanner) (entity.Book, error) {
	var (
		book                        entity.Book
		categoryID                  sql.NullInt64
		categoryName                sql.NullString
		authorID                    sql.NullInt64
		authorName                  sql.NullString
		bookType                    sql.NullInt64
		printed                     sql.NullInt64
		minorRelease                sql.NullInt64
		majorRelease                sql.NullInt64
		bibliography                sql.NullString
		hint                        sql.NullString
		pdfLinks                    []byte
		metadata                    []byte
		sourceDate                  sql.NullString
		description                 sql.NullString
		coverURL                    sql.NullString
		editorialNotes              sql.NullString
		translationStatus           sql.NullString
		reviewedBy                  sql.NullString
		reviewedAt                  sql.NullTime
		publicationStatus           sql.NullString
		catalogPublicationStatus    sql.NullString
		productionWorkflowStatus    sql.NullString
		productionPublicationStatus sql.NullString
		sortOrder                   sql.NullInt64
		requestedLang               string
		displayLang                 string
		isFallback                  bool
		availableLangs              []string
		nameLang                    string
		categoryNameLang            string
		authorNameLang              string
		bibliographyLang            string
		hintLang                    string
		descriptionLang             string
		productionStatus            sql.NullString
	)

	err := row.Scan(
		&book.ID,
		&book.Name,
		&categoryID,
		&categoryName,
		&authorID,
		&authorName,
		&bookType,
		&printed,
		&minorRelease,
		&majorRelease,
		&bibliography,
		&hint,
		&pdfLinks,
		&metadata,
		&sourceDate,
		&description,
		&coverURL,
		&editorialNotes,
		&translationStatus,
		&reviewedBy,
		&reviewedAt,
		&publicationStatus,
		&catalogPublicationStatus,
		&productionWorkflowStatus,
		&productionPublicationStatus,
		&book.Featured,
		&sortOrder,
		&book.HasContent,
		&book.IsDeleted,
		&book.UpdatedAt,
		&requestedLang,
		&displayLang,
		&isFallback,
		&availableLangs,
		&nameLang,
		&categoryNameLang,
		&authorNameLang,
		&bibliographyLang,
		&hintLang,
		&descriptionLang,
		&productionStatus,
	)
	if err != nil {
		return entity.Book{}, err
	}

	book.CategoryID = nullableInt(categoryID)
	book.CategoryName = nullableString(categoryName)
	book.AuthorID = nullableInt(authorID)
	book.AuthorName = nullableString(authorName)
	book.Type = nullableInt(bookType)
	book.Printed = nullableInt(printed)
	book.MinorRelease = nullableInt(minorRelease)
	book.MajorRelease = nullableInt(majorRelease)
	book.Bibliography = nullableString(bibliography)
	book.Hint = nullableString(hint)
	book.PDFLinks = entity.RawJSON(pdfLinks)
	book.Metadata = entity.RawJSON(metadata)
	book.SourceDate = nullableString(sourceDate)
	book.Description = nullableString(description)
	book.CoverURL = nullableString(coverURL)
	book.EditorialNotes = nullableString(editorialNotes)
	book.TranslationStatus = nullableString(translationStatus)
	book.TranslationReviewedBy = nullableString(reviewedBy)
	book.TranslationReviewedAt = nullableTime(reviewedAt)
	book.PublicationStatus = nullableString(publicationStatus)
	book.CatalogPublicationStatus = nullableString(catalogPublicationStatus)
	book.CatalogPublished = catalogPublicationStatus.Valid && catalogPublicationStatus.String == entity.PublicationStatusPublished
	book.ProductionWorkflowStatus = nullableString(productionWorkflowStatus)
	book.ProductionPublicationStatus = nullableString(productionPublicationStatus)
	book.ProductionPublished = productionPublicationStatus.Valid &&
		productionPublicationStatus.String == entity.ProductionPublicationPublished
	book.ProductionStatus = nullableString(productionStatus)
	book.SortOrder = nullableInt(sortOrder)
	book.Localization = localizationMeta(
		requestedLang,
		displayLang,
		isFallback,
		availableLangs,
		map[string]string{
			"name":          nameLang,
			"category_name": categoryNameLang,
			"author_name":   authorNameLang,
			"bibliography":  bibliographyLang,
			"hint":          hintLang,
			"description":   descriptionLang,
		},
	)

	return book, nil
}

func scanPage(row rowScanner) (entity.BookPage, error) {
	var page entity.BookPage
	var part sql.NullString
	var printedPage sql.NullString
	var number sql.NullString
	var services []byte

	err := row.Scan(
		&page.BookID,
		&page.PageID,
		&part,
		&printedPage,
		&number,
		&page.ContentHTML,
		&page.ContentText,
		&services,
		&page.IsDeleted,
		&page.UpdatedAt,
	)
	if err != nil {
		return entity.BookPage{}, err
	}

	page.Part = nullableString(part)
	page.PrintedPage = nullableString(printedPage)
	page.Number = nullableString(number)
	page.ContentHTML = readerutil.SanitizeHTML(page.ContentHTML)
	page.ContentText = readerutil.PlainText(page.ContentHTML)
	page.Services = entity.RawJSON(services)

	return page, nil
}

func scanHeading(row rowScanner) (entity.BookHeading, error) {
	var heading entity.BookHeading
	var parentID sql.NullInt64

	err := row.Scan(
		&heading.BookID,
		&heading.HeadingID,
		&parentID,
		&heading.PageID,
		&heading.Depth,
		&heading.Ordinal,
		&heading.Content,
		&heading.IsDeleted,
		&heading.UpdatedAt,
	)
	if err != nil {
		return entity.BookHeading{}, err
	}

	heading.ParentID = nullableInt(parentID)

	return heading, nil
}

func scanTOCEntry(row rowScanner, includeAudio bool) (entity.BookTOCEntry, error) {
	var entry entity.BookTOCEntry
	var parentID sql.NullInt64
	var titleLang string
	var summaryLang sql.NullString
	var summary sql.NullString
	var summaryStatus sql.NullString
	var summaryReviewedBy sql.NullString
	var summaryReviewedAt sql.NullTime
	var availableTranslationLangs []string
	var availableSummaryLangs []string
	var availableAudioLangs []string
	var translationStatus sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime
	var audioLang sql.NullString
	var audioURL sql.NullString
	var narrator sql.NullString
	var duration sql.NullInt64
	var mimeType sql.NullString
	var metadata []byte
	var audioUpdatedAt sql.NullTime

	err := row.Scan(
		&entry.BookID,
		&entry.HeadingID,
		&parentID,
		&entry.PageID,
		&entry.Depth,
		&entry.Ordinal,
		&entry.Title,
		&entry.RequestedLang,
		&titleLang,
		&entry.IsTitleFallback,
		&entry.HasSummary,
		&summaryLang,
		&summary,
		&summaryStatus,
		&summaryReviewedBy,
		&summaryReviewedAt,
		&entry.HasTranslation,
		&entry.TranslationMissing,
		&availableTranslationLangs,
		&availableSummaryLangs,
		&availableAudioLangs,
		&translationStatus,
		&reviewedBy,
		&reviewedAt,
		&entry.HasAudio,
		&audioLang,
		&audioURL,
		&narrator,
		&duration,
		&mimeType,
		&metadata,
		&audioUpdatedAt,
	)
	if err != nil {
		return entity.BookTOCEntry{}, err
	}

	entry.ParentID = nullableInt(parentID)
	entry.TitleLang = titleLang
	entry.SummaryLang = nullableString(summaryLang)
	entry.Summary = nullableString(summary)
	entry.SummaryStatus = nullableString(summaryStatus)
	entry.SummaryReviewedBy = nullableString(summaryReviewedBy)
	entry.SummaryReviewedAt = nullableTime(summaryReviewedAt)
	entry.AvailableTranslationLangs = emptyStringSlice(availableTranslationLangs)
	entry.AvailableSummaryLangs = emptyStringSlice(availableSummaryLangs)
	availableAudioLangs = emptyStringSlice(availableAudioLangs)
	entry.TranslationStatus = nullableString(translationStatus)
	entry.TranslationReviewedBy = nullableString(reviewedBy)
	entry.TranslationReviewedAt = nullableTime(reviewedAt)
	entry.Availability = entity.NewReaderAvailability(
		entry.RequestedLang,
		entry.TitleLang,
		entry.IsTitleFallback,
		entry.HasTranslation,
		entry.TranslationMissing,
		entry.SummaryLang,
		entry.HasSummary,
		entry.HasAudio,
		entry.AvailableTranslationLangs,
		entry.AvailableSummaryLangs,
		availableAudioLangs,
	)
	if includeAudio && entry.HasAudio {
		entry.Audio = &entity.SectionAudio{
			BookID:          entry.BookID,
			HeadingID:       entry.HeadingID,
			Lang:            audioLang.String,
			URL:             audioURL.String,
			Narrator:        nullableString(narrator),
			DurationSeconds: nullableInt(duration),
			MIMEType:        nullableString(mimeType),
			Metadata:        entity.RawJSON(metadata),
			UpdatedAt:       audioUpdatedAt.Time,
		}
	}

	return entry, nil
}

func scanTranslationFeedback(row rowScanner) (entity.TranslationFeedback, error) {
	var feedback entity.TranslationFeedback
	var userID sql.NullString
	var clientID sql.NullString
	var reason sql.NullString
	var note sql.NullString
	var userAgent sql.NullString
	var clientIP sql.NullString

	err := row.Scan(
		&feedback.ID,
		&feedback.BookID,
		&feedback.HeadingID,
		&feedback.Lang,
		&userID,
		&clientID,
		&feedback.Vote,
		&reason,
		&note,
		&userAgent,
		&clientIP,
		&feedback.CreatedAt,
		&feedback.UpdatedAt,
	)
	if err != nil {
		return entity.TranslationFeedback{}, err
	}

	feedback.UserID = nullableString(userID)
	feedback.ClientID = nullableString(clientID)
	feedback.Reason = nullableString(reason)
	feedback.Note = nullableString(note)
	feedback.UserAgent = nullableString(userAgent)
	feedback.ClientIP = nullableString(clientIP)

	return feedback, nil
}

func localizationMeta(
	requestedLang string,
	displayLang string,
	isFallback bool,
	availableLangs []string,
	fieldLangs map[string]string,
) entity.LocalizationMeta {
	if fieldLangs == nil {
		fieldLangs = map[string]string{}
	}

	availableLangs = emptyStringSlice(availableLangs)

	return entity.LocalizationMeta{
		RequestedLang:  requestedLang,
		DisplayLang:    displayLang,
		IsFallback:     isFallback,
		AvailableLangs: availableLangs,
		FieldLangs:     fieldLangs,
		Availability:   entity.CatalogAvailability(requestedLang, displayLang, isFallback, availableLangs),
	}
}

func sourceBlocks(values []readerutil.SourceBlock) []entity.SourceBlock {
	blocks := make([]entity.SourceBlock, 0, len(values))
	for _, value := range values {
		blocks = append(blocks, entity.SourceBlock{
			Type:           value.Type,
			Text:           value.Text,
			HTML:           value.HTML,
			QuranCitations: sourceQuranCitations(value.QuranCitations),
		})
	}

	return blocks
}

func sourceQuranCitations(values []readerutil.SourceQuranCitation) []entity.SourceQuranCitation {
	if len(values) == 0 {
		return nil
	}

	citations := make([]entity.SourceQuranCitation, 0, len(values))
	for _, value := range values {
		citations = append(citations, entity.SourceQuranCitation{
			Quote:     value.Quote,
			Reference: value.Reference,
		})
	}

	return citations
}

func sourceFootnotes(values []readerutil.SourceFootnote) []entity.SourceFootnote {
	footnotes := make([]entity.SourceFootnote, 0, len(values))
	for _, value := range values {
		footnotes = append(footnotes, entity.SourceFootnote{
			Marker: value.Marker,
			Text:   value.Text,
			HTML:   value.HTML,
		})
	}

	return footnotes
}

func emptyStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}

	return values
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}

	return &value.String
}

func nullableInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}

	i := int(value.Int64)

	return &i
}
