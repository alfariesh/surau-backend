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

// NewReaderRepo creates a reader repository.
func NewReaderRepo(pg *postgres.Postgres) *ReaderRepo {
	return &ReaderRepo{pg}
}

// ListCategories returns non-deleted categories ordered for catalog display.
func (r *ReaderRepo) ListCategories(ctx context.Context, lang string) ([]entity.Category, error) {
	sqlText, args, err := r.Builder.
		Select(
			"c.id",
			"COALESCE(ct.name, c.name) AS name",
			"c.display_order",
			"ct.translation_status",
			"ct.reviewed_by",
			"ct.reviewed_at",
			"c.is_deleted",
			"c.updated_at",
		).
		From("categories c").
		LeftJoin("category_translations ct ON ct.category_id = c.id AND ct.lang = ?", lang).
		Where(sq.Eq{"c.is_deleted": false}).
		OrderBy("display_order ASC NULLS LAST", "id ASC").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("ReaderRepo - ListCategories - r.Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
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

		if err = rows.Scan(
			&category.ID,
			&category.Name,
			&displayOrder,
			&translationStatus,
			&reviewedBy,
			&reviewedAt,
			&category.IsDeleted,
			&category.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("ReaderRepo - ListCategories - rows.Scan: %w", err)
		}

		category.DisplayOrder = nullableInt(displayOrder)
		category.TranslationStatus = nullableString(translationStatus)
		category.TranslationReviewedBy = nullableString(reviewedBy)
		category.TranslationReviewedAt = nullableTime(reviewedAt)
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
		LeftJoin("author_translations at ON at.author_id = a.id AND at.lang = ?", filter.Lang).
		Where(sq.Eq{"a.is_deleted": false})
	dataBuilder := r.Builder.
		Select(
			"a.id",
			"COALESCE(at.name, a.name) AS name",
			"COALESCE(at.biography, a.biography) AS biography",
			"COALESCE(at.death_text, a.death_text) AS death_text",
			"a.death_number",
			"at.translation_status",
			"at.reviewed_by",
			"at.reviewed_at",
			"a.is_deleted",
			"a.updated_at",
		).
		From("authors a").
		LeftJoin("author_translations at ON at.author_id = a.id AND at.lang = ?", filter.Lang).
		Where(sq.Eq{"a.is_deleted": false}).
		OrderBy("name ASC").
		Limit(filter.Limit).
		Offset(filter.Offset)

	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		condition := "(a.name ILIKE ? OR COALESCE(at.name, a.name) ILIKE ? OR COALESCE(at.biography, a.biography) ILIKE ?)"
		countBuilder = countBuilder.Where(condition, like, like, like)
		dataBuilder = dataBuilder.Where(condition, like, like, like)
	}

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListAuthors - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("ReaderRepo - ListAuthors - dataBuilder: %w", err)
	}

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
		LeftJoin("book_metadata_translations bmt ON bmt.book_id = b.id AND bmt.lang = ?", filter.Lang).
		LeftJoin("authors a ON a.id = b.author_id").
		LeftJoin("author_translations at ON at.author_id = a.id AND at.lang = ?", filter.Lang).
		LeftJoin("categories c ON c.id = COALESCE(me.category_id, b.category_id)").
		LeftJoin("category_translations ct ON ct.category_id = c.id AND ct.lang = ?", filter.Lang).
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
       COALESCE(he.content, h.content) AS title,
       (st.book_id IS NOT NULL) AS has_translation,
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
LEFT JOIN book_heading_edits he ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'
LEFT JOIN section_translations st ON st.book_id = h.book_id AND st.heading_id = h.heading_id AND st.lang = $2
LEFT JOIN section_audio sa ON sa.book_id = h.book_id AND sa.heading_id = h.heading_id AND sa.lang = $2
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
SELECT h.book_id, h.heading_id, h.parent_id, h.page_id, h.depth, h.ordinal, COALESCE(he.content, h.content) AS content, h.is_deleted, h.updated_at,
       hr.start_page_id, hr.end_page_id, hr.start_anchor, hr.end_anchor
FROM book_headings h
JOIN book_heading_ranges hr ON hr.book_id = h.book_id AND hr.heading_id = h.heading_id
JOIN book_publications p ON p.book_id = h.book_id AND p.status = 'published'
LEFT JOIN book_heading_edits he ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'
WHERE h.book_id = $1 AND h.heading_id = $2 AND h.is_deleted = false`

	var heading entity.BookHeading
	var parentID sql.NullInt64
	var startAnchor sql.NullString
	var endAnchor sql.NullString
	var startPageID int
	var endPageID int

	err := r.Pool.QueryRow(ctx, sqlText, bookID, headingID).Scan(
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

	originalHTML := readerutil.SliceAnchoredHTML(builder.String(), startAnchor.String, endAnchor.String)

	section := entity.BookSection{
		BookID:       bookID,
		HeadingID:    headingID,
		Heading:      heading,
		StartPageID:  startPageID,
		EndPageID:    endPageID,
		OriginalHTML: originalHTML,
		OriginalText: readerutil.PlainText(originalHTML),
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
	section.Audio = audio

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
	err := r.Pool.QueryRow(ctx, `
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
	if err := r.Pool.QueryRow(ctx, `
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
	if err := r.Pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM section_translations
    WHERE book_id = $1 AND heading_id = $2 AND lang = $3
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
			"COALESCE(bmt.display_title, me.display_title, b.name) AS name",
			"COALESCE(me.category_id, b.category_id) AS category_id",
			"COALESCE(ct.name, c.name) AS category_name",
			"b.author_id",
			"COALESCE(at.name, a.name) AS author_name",
			"b.type",
			"b.printed",
			"b.minor_release",
			"b.major_release",
			"COALESCE(bmt.bibliography, b.bibliography) AS bibliography",
			"COALESCE(bmt.hint, b.hint) AS hint",
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
			"p.featured",
			"p.sort_order",
			"b.has_content",
			"b.is_deleted",
			"b.updated_at",
		).
		From("books b").
		Join("book_publications p ON p.book_id = b.id AND p.status = 'published'").
		LeftJoin("book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'").
		LeftJoin("book_metadata_translations bmt ON bmt.book_id = b.id AND bmt.lang = ?", lang).
		LeftJoin("authors a ON a.id = b.author_id").
		LeftJoin("author_translations at ON at.author_id = a.id AND at.lang = ?", lang).
		LeftJoin("categories c ON c.id = COALESCE(me.category_id, b.category_id)").
		LeftJoin("category_translations ct ON ct.category_id = c.id AND ct.lang = ?", lang)
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
WHERE book_id = $1 AND heading_id = $2 AND lang = $3`

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
WHERE book_id = $1 AND heading_id = $2 AND lang = $3`

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

func applyBookFilter(countBuilder, dataBuilder sq.SelectBuilder, filter repo.BookFilter) (sq.SelectBuilder, sq.SelectBuilder) {
	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		condition := `(COALESCE(bmt.display_title, me.display_title, b.name) ILIKE ?
			OR b.name ILIKE ?
			OR COALESCE(at.name, a.name) ILIKE ?
			OR a.name ILIKE ?
			OR COALESCE(ct.name, c.name) ILIKE ?
			OR c.name ILIKE ?
			OR COALESCE(bmt.bibliography, b.bibliography) ILIKE ?
			OR COALESCE(bmt.hint, b.hint) ILIKE ?)`
		countBuilder = countBuilder.Where(condition, like, like, like, like, like, like, like, like)
		dataBuilder = dataBuilder.Where(condition, like, like, like, like, like, like, like, like)
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

func scanAuthor(row rowScanner) (entity.Author, error) {
	var author entity.Author
	var biography sql.NullString
	var deathText sql.NullString
	var deathNumber sql.NullInt64
	var translationStatus sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime

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

	return author, nil
}

func scanBook(row rowScanner) (entity.Book, error) {
	var book entity.Book
	var categoryID sql.NullInt64
	var categoryName sql.NullString
	var authorID sql.NullInt64
	var authorName sql.NullString
	var bookType sql.NullInt64
	var printed sql.NullInt64
	var minorRelease sql.NullInt64
	var majorRelease sql.NullInt64
	var bibliography sql.NullString
	var hint sql.NullString
	var pdfLinks []byte
	var metadata []byte
	var sourceDate sql.NullString
	var description sql.NullString
	var coverURL sql.NullString
	var editorialNotes sql.NullString
	var translationStatus sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime
	var publicationStatus sql.NullString
	var sortOrder sql.NullInt64

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
		&book.Featured,
		&sortOrder,
		&book.HasContent,
		&book.IsDeleted,
		&book.UpdatedAt,
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
	book.SortOrder = nullableInt(sortOrder)

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
		&entry.HasTranslation,
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
	entry.TranslationStatus = nullableString(translationStatus)
	entry.TranslationReviewedBy = nullableString(reviewedBy)
	entry.TranslationReviewedAt = nullableTime(reviewedAt)
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
