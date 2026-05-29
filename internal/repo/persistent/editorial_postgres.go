package persistent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EditorialRepo manages admin-only publication and edit overlays.
type EditorialRepo struct {
	*postgres.Postgres
}

// NewEditorialRepo creates an editorial repository.
func NewEditorialRepo(pg *postgres.Postgres) *EditorialRepo {
	return &EditorialRepo{pg}
}

// ListBooks returns all books for admin review, including hidden books.
func (r *EditorialRepo) ListBooks(ctx context.Context, filter repo.EditorialBookFilter) ([]entity.Book, int, error) {
	countBuilder := r.Builder.
		Select("COUNT(*)").
		From("books b").
		LeftJoin("book_publications p ON p.book_id = b.id").
		LeftJoin("book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'").
		LeftJoin("authors a ON a.id = b.author_id").
		LeftJoin("categories c ON c.id = COALESCE(me.category_id, b.category_id)").
		Where(sq.Eq{"b.is_deleted": false})

	dataBuilder := r.adminBookSelectBuilder().
		Where(sq.Eq{"b.is_deleted": false}).
		OrderBy("COALESCE(p.sort_order, 2147483647) ASC", "b.id ASC").
		Limit(filter.Limit).
		Offset(filter.Offset)

	countBuilder, dataBuilder = applyEditorialBookFilter(countBuilder, dataBuilder, filter)

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListBooks - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListBooks - dataBuilder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListBooks - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	books := make([]entity.Book, 0, filter.Limit)
	for rows.Next() {
		book, err := scanBook(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("EditorialRepo - ListBooks - scanBook: %w", err)
		}

		books = append(books, book)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListBooks - rows.Err: %w", err)
	}

	return books, total, nil
}

// UpdatePublication upserts visibility settings.
func (r *EditorialRepo) UpdatePublication(
	ctx context.Context,
	actorID string,
	publication entity.BookPublication,
) (entity.BookPublication, error) {
	sqlText := `
INSERT INTO book_publications (book_id, status, featured, sort_order, published_at, updated_by, updated_at)
VALUES ($1, $2, $3, $4, CASE WHEN $2 = 'published' THEN now() ELSE NULL END, $5, now())
ON CONFLICT (book_id) DO UPDATE SET
    status = EXCLUDED.status,
    featured = EXCLUDED.featured,
    sort_order = EXCLUDED.sort_order,
    published_at = CASE
        WHEN EXCLUDED.status = 'published' AND book_publications.status <> 'published' THEN now()
        WHEN EXCLUDED.status = 'published' THEN COALESCE(book_publications.published_at, now())
        ELSE NULL
    END,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING book_id, status, featured, sort_order, published_at, updated_by, updated_at`

	saved, err := scanPublication(r.Pool.QueryRow(
		ctx,
		sqlText,
		publication.BookID,
		publication.Status,
		publication.Featured,
		publication.SortOrder,
		actorID,
	))
	if err != nil {
		return entity.BookPublication{}, fmt.Errorf("EditorialRepo - UpdatePublication - scanPublication: %w", err)
	}

	_ = r.audit(ctx, actorID, "publication.update", publication.BookID, nil, nil, "", saved)

	return saved, nil
}

// SaveMetadataDraft upserts metadata draft.
func (r *EditorialRepo) SaveMetadataDraft(
	ctx context.Context,
	actorID string,
	edit entity.BookMetadataEdit,
) (entity.BookMetadataEdit, error) {
	sqlText := `
INSERT INTO book_metadata_edits (
    book_id, status, display_title, description, cover_url, category_id, notes, updated_by, updated_at
)
VALUES ($1, 'draft', $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (book_id, status) DO UPDATE SET
    display_title = EXCLUDED.display_title,
    description = EXCLUDED.description,
    cover_url = EXCLUDED.cover_url,
    category_id = EXCLUDED.category_id,
    notes = EXCLUDED.notes,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING book_id, status, display_title, description, cover_url, category_id, notes, updated_by, updated_at, published_at`

	saved, err := scanMetadataEdit(r.Pool.QueryRow(
		ctx,
		sqlText,
		edit.BookID,
		edit.DisplayTitle,
		edit.Description,
		edit.CoverURL,
		edit.CategoryID,
		edit.Notes,
		actorID,
	))
	if err != nil {
		return entity.BookMetadataEdit{}, fmt.Errorf("EditorialRepo - SaveMetadataDraft - scanMetadataEdit: %w", err)
	}

	_ = r.audit(ctx, actorID, "metadata.draft.save", edit.BookID, nil, nil, "", saved)

	return saved, nil
}

// PublishMetadataDraft promotes metadata draft to published.
func (r *EditorialRepo) PublishMetadataDraft(ctx context.Context, actorID string, bookID int) (entity.BookMetadataEdit, error) {
	sqlText := `
INSERT INTO book_metadata_edits (
    book_id, status, display_title, description, cover_url, category_id, notes, updated_by, updated_at, published_at
)
SELECT book_id, 'published', display_title, description, cover_url, category_id, notes, $2, now(), now()
FROM book_metadata_edits
WHERE book_id = $1 AND status = 'draft'
ON CONFLICT (book_id, status) DO UPDATE SET
    display_title = EXCLUDED.display_title,
    description = EXCLUDED.description,
    cover_url = EXCLUDED.cover_url,
    category_id = EXCLUDED.category_id,
    notes = EXCLUDED.notes,
    updated_by = EXCLUDED.updated_by,
    updated_at = now(),
    published_at = now()
RETURNING book_id, status, display_title, description, cover_url, category_id, notes, updated_by, updated_at, published_at`

	saved, err := scanMetadataEdit(r.Pool.QueryRow(ctx, sqlText, bookID, actorID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookMetadataEdit{}, entity.ErrDraftNotFound
		}

		return entity.BookMetadataEdit{}, fmt.Errorf("EditorialRepo - PublishMetadataDraft - scanMetadataEdit: %w", err)
	}

	_ = r.audit(ctx, actorID, "metadata.draft.publish", bookID, nil, nil, "", saved)

	return saved, nil
}

// GetPageEdit returns raw page plus draft/published overrides.
func (r *EditorialRepo) GetPageEdit(ctx context.Context, bookID, pageID int) (entity.AdminPageEdit, error) {
	sqlText := `
SELECT book_id, page_id, part, printed_page, number, content_html, content_text, services, is_deleted, updated_at
FROM book_pages
WHERE book_id = $1 AND page_id = $2`

	raw, err := scanPage(r.Pool.QueryRow(ctx, sqlText, bookID, pageID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.AdminPageEdit{}, entity.ErrPageNotFound
		}

		return entity.AdminPageEdit{}, fmt.Errorf("EditorialRepo - GetPageEdit - scanPage: %w", err)
	}

	draft, err := r.getPageEditByStatus(ctx, bookID, pageID, entity.EditStatusDraft)
	if err != nil {
		return entity.AdminPageEdit{}, err
	}

	published, err := r.getPageEditByStatus(ctx, bookID, pageID, entity.EditStatusPublished)
	if err != nil {
		return entity.AdminPageEdit{}, err
	}

	return entity.AdminPageEdit{Raw: raw, Draft: draft, Published: published}, nil
}

// SavePageDraft upserts a page content draft.
func (r *EditorialRepo) SavePageDraft(ctx context.Context, actorID string, edit entity.BookPageEdit) (entity.BookPageEdit, error) {
	sqlText := `
INSERT INTO book_page_edits (book_id, page_id, status, content_html, content_text, updated_by, updated_at)
VALUES ($1, $2, 'draft', $3, $4, $5, now())
ON CONFLICT (book_id, page_id, status) DO UPDATE SET
    content_html = EXCLUDED.content_html,
    content_text = EXCLUDED.content_text,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING book_id, page_id, status, content_html, content_text, updated_by, updated_at, published_at`

	saved, err := scanPageEdit(r.Pool.QueryRow(ctx, sqlText, edit.BookID, edit.PageID, edit.ContentHTML, edit.ContentText, actorID))
	if err != nil {
		return entity.BookPageEdit{}, fmt.Errorf("EditorialRepo - SavePageDraft - scanPageEdit: %w", err)
	}

	_ = r.audit(ctx, actorID, "page.draft.save", edit.BookID, &edit.PageID, nil, "", nil)

	return saved, nil
}

// PublishPageDraft promotes a page draft to published.
func (r *EditorialRepo) PublishPageDraft(ctx context.Context, actorID string, bookID, pageID int) (entity.BookPageEdit, error) {
	sqlText := `
INSERT INTO book_page_edits (book_id, page_id, status, content_html, content_text, updated_by, updated_at, published_at)
SELECT book_id, page_id, 'published', content_html, content_text, $3, now(), now()
FROM book_page_edits
WHERE book_id = $1 AND page_id = $2 AND status = 'draft'
ON CONFLICT (book_id, page_id, status) DO UPDATE SET
    content_html = EXCLUDED.content_html,
    content_text = EXCLUDED.content_text,
    updated_by = EXCLUDED.updated_by,
    updated_at = now(),
    published_at = now()
RETURNING book_id, page_id, status, content_html, content_text, updated_by, updated_at, published_at`

	saved, err := scanPageEdit(r.Pool.QueryRow(ctx, sqlText, bookID, pageID, actorID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookPageEdit{}, entity.ErrDraftNotFound
		}

		return entity.BookPageEdit{}, fmt.Errorf("EditorialRepo - PublishPageDraft - scanPageEdit: %w", err)
	}

	_ = r.audit(ctx, actorID, "page.draft.publish", bookID, &pageID, nil, "", nil)

	return saved, nil
}

// SaveHeadingDraft upserts a heading draft.
func (r *EditorialRepo) SaveHeadingDraft(ctx context.Context, actorID string, edit entity.BookHeadingEdit) (entity.BookHeadingEdit, error) {
	sqlText := `
INSERT INTO book_heading_edits (book_id, heading_id, status, content, updated_by, updated_at)
VALUES ($1, $2, 'draft', $3, $4, now())
ON CONFLICT (book_id, heading_id, status) DO UPDATE SET
    content = EXCLUDED.content,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING book_id, heading_id, status, content, updated_by, updated_at, published_at`

	saved, err := scanHeadingEdit(r.Pool.QueryRow(ctx, sqlText, edit.BookID, edit.HeadingID, edit.Content, actorID))
	if err != nil {
		return entity.BookHeadingEdit{}, fmt.Errorf("EditorialRepo - SaveHeadingDraft - scanHeadingEdit: %w", err)
	}

	_ = r.audit(ctx, actorID, "heading.draft.save", edit.BookID, nil, &edit.HeadingID, "", saved)

	return saved, nil
}

// PublishHeadingDraft promotes a heading draft to published.
func (r *EditorialRepo) PublishHeadingDraft(ctx context.Context, actorID string, bookID, headingID int) (entity.BookHeadingEdit, error) {
	sqlText := `
INSERT INTO book_heading_edits (book_id, heading_id, status, content, updated_by, updated_at, published_at)
SELECT book_id, heading_id, 'published', content, $3, now(), now()
FROM book_heading_edits
WHERE book_id = $1 AND heading_id = $2 AND status = 'draft'
ON CONFLICT (book_id, heading_id, status) DO UPDATE SET
    content = EXCLUDED.content,
    updated_by = EXCLUDED.updated_by,
    updated_at = now(),
    published_at = now()
RETURNING book_id, heading_id, status, content, updated_by, updated_at, published_at`

	saved, err := scanHeadingEdit(r.Pool.QueryRow(ctx, sqlText, bookID, headingID, actorID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.BookHeadingEdit{}, entity.ErrDraftNotFound
		}

		return entity.BookHeadingEdit{}, fmt.Errorf("EditorialRepo - PublishHeadingDraft - scanHeadingEdit: %w", err)
	}

	_ = r.audit(ctx, actorID, "heading.draft.publish", bookID, nil, &headingID, "", saved)

	return saved, nil
}

// AddCollectionItem adds or updates a book inside a collection.
func (r *EditorialRepo) AddCollectionItem(
	ctx context.Context,
	actorID, slug string,
	bookID int,
	sortOrder *int,
) (entity.BookCollectionItem, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.BookCollectionItem{}, fmt.Errorf("EditorialRepo - AddCollectionItem - Begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	_, err = tx.Exec(ctx, `
INSERT INTO book_collections (slug, title, created_by, updated_at)
VALUES ($1, $1, $2, now())
ON CONFLICT (slug) DO UPDATE SET updated_at = now()`,
		slug,
		actorID,
	)
	if err != nil {
		return entity.BookCollectionItem{}, fmt.Errorf("EditorialRepo - AddCollectionItem - upsert collection: %w", err)
	}

	sqlText := `
INSERT INTO book_collection_items (collection_slug, book_id, sort_order, created_by, created_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (collection_slug, book_id) DO UPDATE SET
    sort_order = EXCLUDED.sort_order,
    created_by = EXCLUDED.created_by,
    created_at = now()
RETURNING collection_slug, book_id, sort_order, created_by, created_at`

	item, err := scanCollectionItem(tx.QueryRow(ctx, sqlText, slug, bookID, sortOrder, actorID))
	if err != nil {
		return entity.BookCollectionItem{}, fmt.Errorf("EditorialRepo - AddCollectionItem - scanCollectionItem: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.BookCollectionItem{}, fmt.Errorf("EditorialRepo - AddCollectionItem - Commit: %w", err)
	}

	_ = r.audit(ctx, actorID, "collection.item.add", bookID, nil, nil, slug, item)

	return item, nil
}

// ListTranslationFeedbacks returns paginated reader translation feedback for admin review.
func (r *EditorialRepo) ListTranslationFeedbacks(
	ctx context.Context,
	filter repo.TranslationFeedbackFilter,
) ([]entity.AdminTranslationFeedback, int, error) {
	countBuilder := r.feedbackBaseBuilder("COUNT(*)")
	dataBuilder := r.feedbackSelectBuilder().
		OrderBy("tf.updated_at DESC", "tf.created_at DESC").
		Limit(filter.Limit).
		Offset(filter.Offset)

	countBuilder, dataBuilder = applyTranslationFeedbackFilter(countBuilder, dataBuilder, filter)

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListTranslationFeedbacks - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListTranslationFeedbacks - dataBuilder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListTranslationFeedbacks - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	feedbacks := make([]entity.AdminTranslationFeedback, 0, filter.Limit)
	for rows.Next() {
		feedback, err := scanAdminTranslationFeedback(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("EditorialRepo - ListTranslationFeedbacks - scanAdminTranslationFeedback: %w", err)
		}

		feedbacks = append(feedbacks, feedback)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo - ListTranslationFeedbacks - rows.Err: %w", err)
	}

	return feedbacks, total, nil
}

// TranslationFeedbackSummary aggregates feedback for admin review queues.
func (r *EditorialRepo) TranslationFeedbackSummary(
	ctx context.Context,
	filter repo.TranslationFeedbackFilter,
) (entity.AdminTranslationFeedbackSummary, error) {
	summaryBuilder := r.feedbackBaseBuilder(
		"COUNT(*)",
		"COUNT(*) FILTER (WHERE tf.vote = 'like')",
		"COUNT(*) FILTER (WHERE tf.vote = 'dislike')",
	)
	summaryBuilder, _ = applyTranslationFeedbackFilter(summaryBuilder, r.Builder.Select("1"), filter)

	sqlText, args, err := summaryBuilder.ToSql()
	if err != nil {
		return entity.AdminTranslationFeedbackSummary{}, fmt.Errorf("EditorialRepo - TranslationFeedbackSummary - summaryBuilder: %w", err)
	}

	var summary entity.AdminTranslationFeedbackSummary
	if err = r.Pool.QueryRow(ctx, sqlText, args...).Scan(&summary.Total, &summary.Likes, &summary.Dislikes); err != nil {
		return entity.AdminTranslationFeedbackSummary{}, fmt.Errorf("EditorialRepo - TranslationFeedbackSummary - summary scan: %w", err)
	}

	topBuilder := r.feedbackBaseBuilder(
		"tf.book_id",
		"COALESCE(me.display_title, b.name) AS book_title",
		"tf.heading_id",
		"COALESCE(he.content, h.content) AS heading_title",
		"tf.lang",
		"COUNT(*) AS total",
		"COUNT(*) FILTER (WHERE tf.vote = 'like') AS likes",
		"COUNT(*) FILTER (WHERE tf.vote = 'dislike') AS dislikes",
	).
		GroupBy("tf.book_id", "COALESCE(me.display_title, b.name)", "tf.heading_id", "COALESCE(he.content, h.content)", "tf.lang").
		Having("COUNT(*) FILTER (WHERE tf.vote = 'dislike') > 0").
		OrderBy("dislikes DESC", "total DESC", "tf.book_id ASC", "tf.heading_id ASC", "tf.lang ASC").
		Limit(filter.Limit)
	topBuilder, _ = applyTranslationFeedbackFilter(topBuilder, r.Builder.Select("1"), filter)

	sqlText, args, err = topBuilder.ToSql()
	if err != nil {
		return entity.AdminTranslationFeedbackSummary{}, fmt.Errorf("EditorialRepo - TranslationFeedbackSummary - topBuilder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return entity.AdminTranslationFeedbackSummary{}, fmt.Errorf("EditorialRepo - TranslationFeedbackSummary - top query: %w", err)
	}
	defer rows.Close()

	topHeadings := make([]entity.TranslationFeedbackHeadingSummary, 0, filter.Limit)
	for rows.Next() {
		item, err := scanFeedbackHeadingSummary(rows)
		if err != nil {
			return entity.AdminTranslationFeedbackSummary{}, fmt.Errorf("EditorialRepo - TranslationFeedbackSummary - scanFeedbackHeadingSummary: %w", err)
		}

		topHeadings = append(topHeadings, item)
	}

	if err = rows.Err(); err != nil {
		return entity.AdminTranslationFeedbackSummary{}, fmt.Errorf("EditorialRepo - TranslationFeedbackSummary - rows.Err: %w", err)
	}

	if err = r.fillFeedbackReasons(ctx, topHeadings, filter); err != nil {
		return entity.AdminTranslationFeedbackSummary{}, err
	}

	summary.TopDislikedHeadings = topHeadings

	return summary, nil
}

// ResolveTranslationFeedback marks a reader feedback item as handled by an admin.
func (r *EditorialRepo) ResolveTranslationFeedback(
	ctx context.Context,
	actorID, feedbackID string,
	note *string,
) (entity.AdminTranslationFeedback, error) {
	result, err := r.Pool.Exec(ctx, `
UPDATE translation_feedbacks
SET status = 'resolved',
    resolved_by = $2,
    resolved_at = now(),
    resolution_note = $3,
    updated_at = now()
WHERE id = $1`, feedbackID, actorID, note)
	if err != nil {
		return entity.AdminTranslationFeedback{}, fmt.Errorf("EditorialRepo - ResolveTranslationFeedback - Exec: %w", err)
	}

	if result.RowsAffected() == 0 {
		return entity.AdminTranslationFeedback{}, entity.ErrFeedbackNotFound
	}

	feedback, err := r.getAdminTranslationFeedback(ctx, feedbackID)
	if err != nil {
		return entity.AdminTranslationFeedback{}, err
	}

	_ = r.audit(ctx, actorID, "translation_feedback.resolve", feedback.BookID, nil, &feedback.HeadingID, "", feedback)

	return feedback, nil
}

// ReopenTranslationFeedback moves a handled feedback item back into the active queue.
func (r *EditorialRepo) ReopenTranslationFeedback(
	ctx context.Context,
	actorID, feedbackID string,
) (entity.AdminTranslationFeedback, error) {
	result, err := r.Pool.Exec(ctx, `
UPDATE translation_feedbacks
SET status = 'open',
    resolved_by = NULL,
    resolved_at = NULL,
    resolution_note = NULL,
    updated_at = now()
WHERE id = $1`, feedbackID)
	if err != nil {
		return entity.AdminTranslationFeedback{}, fmt.Errorf("EditorialRepo - ReopenTranslationFeedback - Exec: %w", err)
	}

	if result.RowsAffected() == 0 {
		return entity.AdminTranslationFeedback{}, entity.ErrFeedbackNotFound
	}

	feedback, err := r.getAdminTranslationFeedback(ctx, feedbackID)
	if err != nil {
		return entity.AdminTranslationFeedback{}, err
	}

	_ = r.audit(ctx, actorID, "translation_feedback.reopen", feedback.BookID, nil, &feedback.HeadingID, "", feedback)

	return feedback, nil
}

func (r *EditorialRepo) adminBookSelectBuilder() sq.SelectBuilder {
	return r.Builder.
		Select(
			"b.id",
			"COALESCE(me.display_title, b.name) AS name",
			"COALESCE(me.category_id, b.category_id) AS category_id",
			"c.name AS category_name",
			"b.author_id",
			"a.name AS author_name",
			"b.type",
			"b.printed",
			"b.minor_release",
			"b.major_release",
			"b.bibliography",
			"b.hint",
			"b.pdf_links",
			"b.metadata",
			"b.source_date",
			"me.description",
			"me.cover_url",
			"me.notes AS editorial_notes",
			"NULL::TEXT AS translation_status",
			"NULL::TEXT AS reviewed_by",
			"NULL::TIMESTAMPTZ AS reviewed_at",
			"COALESCE(p.status, 'hidden') AS publication_status",
			"COALESCE(p.featured, false) AS featured",
			"p.sort_order",
			"b.has_content",
			"b.is_deleted",
			"b.updated_at",
			"'ar'::TEXT AS requested_lang",
			"'ar'::TEXT AS display_lang",
			"false AS is_fallback",
			"ARRAY[]::TEXT[] AS available_langs",
			"'ar'::TEXT AS name_lang",
			"'ar'::TEXT AS category_name_lang",
			"'ar'::TEXT AS author_name_lang",
			"'ar'::TEXT AS bibliography_lang",
			"'ar'::TEXT AS hint_lang",
			"'ar'::TEXT AS description_lang",
		).
		From("books b").
		LeftJoin("book_publications p ON p.book_id = b.id").
		LeftJoin("book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'").
		LeftJoin("authors a ON a.id = b.author_id").
		LeftJoin("categories c ON c.id = COALESCE(me.category_id, b.category_id)")
}

func (r *EditorialRepo) feedbackBaseBuilder(columns ...string) sq.SelectBuilder {
	return r.Builder.
		Select(columns...).
		From("translation_feedbacks tf").
		Join("books b ON b.id = tf.book_id").
		Join("book_headings h ON h.book_id = tf.book_id AND h.heading_id = tf.heading_id").
		Join("section_translations st ON st.book_id = tf.book_id AND st.heading_id = tf.heading_id AND st.lang = tf.lang").
		LeftJoin("book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'").
		LeftJoin("book_heading_edits he ON he.book_id = h.book_id AND he.heading_id = h.heading_id AND he.status = 'published'")
}

func (r *EditorialRepo) feedbackSelectBuilder() sq.SelectBuilder {
	return r.feedbackBaseBuilder(
		"tf.id",
		"tf.book_id",
		"COALESCE(me.display_title, b.name) AS book_title",
		"tf.heading_id",
		"COALESCE(he.content, h.content) AS heading_title",
		"tf.lang",
		"tf.user_id",
		"tf.client_id",
		"tf.vote",
		"tf.reason",
		"tf.note",
		"tf.status",
		"tf.resolved_by",
		"tf.resolved_at",
		"tf.resolution_note",
		"tf.user_agent",
		"tf.client_ip",
		"st.translation_status",
		"st.reviewed_by",
		"st.reviewed_at",
		"tf.created_at",
		"tf.updated_at",
	)
}

func (r *EditorialRepo) getAdminTranslationFeedback(
	ctx context.Context,
	feedbackID string,
) (entity.AdminTranslationFeedback, error) {
	sqlText, args, err := r.feedbackSelectBuilder().
		Where(sq.Eq{"tf.id": feedbackID}).
		ToSql()
	if err != nil {
		return entity.AdminTranslationFeedback{}, fmt.Errorf("EditorialRepo - getAdminTranslationFeedback - Builder: %w", err)
	}

	feedback, err := scanAdminTranslationFeedback(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.AdminTranslationFeedback{}, entity.ErrFeedbackNotFound
		}

		return entity.AdminTranslationFeedback{}, fmt.Errorf("EditorialRepo - getAdminTranslationFeedback - scanAdminTranslationFeedback: %w", err)
	}

	return feedback, nil
}

func (r *EditorialRepo) count(ctx context.Context, builder sq.SelectBuilder) (int, error) {
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

func (r *EditorialRepo) getPageEditByStatus(ctx context.Context, bookID, pageID int, status string) (*entity.BookPageEdit, error) {
	sqlText := `
SELECT book_id, page_id, status, content_html, content_text, updated_by, updated_at, published_at
FROM book_page_edits
WHERE book_id = $1 AND page_id = $2 AND status = $3`

	edit, err := scanPageEdit(r.Pool.QueryRow(ctx, sqlText, bookID, pageID, status))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, fmt.Errorf("EditorialRepo - getPageEditByStatus - scanPageEdit: %w", err)
	}

	return &edit, nil
}

func emptyStringNil(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func (r *EditorialRepo) audit(
	ctx context.Context,
	actorID string,
	action string,
	bookID int,
	pageID *int,
	headingID *int,
	collectionSlug string,
	payload any,
) error {
	var payloadJSON []byte
	var err error
	if payload != nil {
		payloadJSON, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal audit payload: %w", err)
		}
	}

	_, err = r.Pool.Exec(ctx, `
INSERT INTO admin_audit_logs (id, actor_id, action, book_id, page_id, heading_id, collection_slug, payload, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::jsonb, now())`,
		uuid.New().String(),
		actorID,
		action,
		bookID,
		pageID,
		headingID,
		emptyStringNil(collectionSlug),
		string(payloadJSON),
	)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}

	return nil
}

func applyEditorialBookFilter(countBuilder, dataBuilder sq.SelectBuilder, filter repo.EditorialBookFilter) (sq.SelectBuilder, sq.SelectBuilder) {
	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		condition := "(b.name ILIKE ? OR me.display_title ILIKE ? OR a.name ILIKE ? OR c.name ILIKE ?)"
		countBuilder = countBuilder.Where(condition, like, like, like, like)
		dataBuilder = dataBuilder.Where(condition, like, like, like, like)
	}

	if filter.Status != nil {
		countBuilder = countBuilder.Where("COALESCE(p.status, 'hidden') = ?", *filter.Status)
		dataBuilder = dataBuilder.Where("COALESCE(p.status, 'hidden') = ?", *filter.Status)
	}

	if filter.CategoryID != nil {
		countBuilder = countBuilder.Where("COALESCE(me.category_id, b.category_id) = ?", *filter.CategoryID)
		dataBuilder = dataBuilder.Where("COALESCE(me.category_id, b.category_id) = ?", *filter.CategoryID)
	}

	if filter.HasContent != nil {
		countBuilder = countBuilder.Where(sq.Eq{"b.has_content": *filter.HasContent})
		dataBuilder = dataBuilder.Where(sq.Eq{"b.has_content": *filter.HasContent})
	}

	return countBuilder, dataBuilder
}

func applyTranslationFeedbackFilter(
	countBuilder,
	dataBuilder sq.SelectBuilder,
	filter repo.TranslationFeedbackFilter,
) (sq.SelectBuilder, sq.SelectBuilder) {
	if filter.BookID != nil {
		countBuilder = countBuilder.Where(sq.Eq{"tf.book_id": *filter.BookID})
		dataBuilder = dataBuilder.Where(sq.Eq{"tf.book_id": *filter.BookID})
	}

	if filter.HeadingID != nil {
		countBuilder = countBuilder.Where(sq.Eq{"tf.heading_id": *filter.HeadingID})
		dataBuilder = dataBuilder.Where(sq.Eq{"tf.heading_id": *filter.HeadingID})
	}

	if filter.Lang != "" {
		countBuilder = countBuilder.Where(sq.Eq{"tf.lang": filter.Lang})
		dataBuilder = dataBuilder.Where(sq.Eq{"tf.lang": filter.Lang})
	}

	if filter.Vote != "" {
		countBuilder = countBuilder.Where(sq.Eq{"tf.vote": filter.Vote})
		dataBuilder = dataBuilder.Where(sq.Eq{"tf.vote": filter.Vote})
	}

	if filter.Status != "" {
		countBuilder = countBuilder.Where(sq.Eq{"tf.status": filter.Status})
		dataBuilder = dataBuilder.Where(sq.Eq{"tf.status": filter.Status})
	}

	return countBuilder, dataBuilder
}

func (r *EditorialRepo) fillFeedbackReasons(
	ctx context.Context,
	items []entity.TranslationFeedbackHeadingSummary,
	filter repo.TranslationFeedbackFilter,
) error {
	if len(items) == 0 {
		return nil
	}

	conditions := make([]sq.Sqlizer, 0, len(items))
	for _, item := range items {
		conditions = append(conditions, sq.And{
			sq.Eq{"book_id": item.BookID},
			sq.Eq{"heading_id": item.HeadingID},
			sq.Eq{"lang": item.Lang},
		})
	}

	builder := r.Builder.
		Select("book_id", "heading_id", "lang", "reason", "COUNT(*)").
		From("translation_feedbacks").
		Where(sq.Or(conditions)).
		Where(sq.Eq{"vote": "dislike"}).
		Where("reason IS NOT NULL").
		GroupBy("book_id", "heading_id", "lang", "reason").
		PlaceholderFormat(sq.Dollar)
	if filter.Status != "" {
		builder = builder.Where(sq.Eq{"status": filter.Status})
	}

	sqlText, args, err := builder.ToSql()
	if err != nil {
		return fmt.Errorf("EditorialRepo - fillFeedbackReasons - Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return fmt.Errorf("EditorialRepo - fillFeedbackReasons - Query: %w", err)
	}
	defer rows.Close()

	index := make(map[string]int, len(items))
	for i, item := range items {
		items[i].Reasons = map[string]int{}
		index[feedbackSummaryKey(item.BookID, item.HeadingID, item.Lang)] = i
	}

	for rows.Next() {
		var bookID int
		var headingID int
		var lang string
		var reason string
		var count int
		if err = rows.Scan(&bookID, &headingID, &lang, &reason, &count); err != nil {
			return fmt.Errorf("EditorialRepo - fillFeedbackReasons - Scan: %w", err)
		}

		if i, ok := index[feedbackSummaryKey(bookID, headingID, lang)]; ok {
			items[i].Reasons[reason] = count
		}
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("EditorialRepo - fillFeedbackReasons - rows.Err: %w", err)
	}

	return nil
}

func feedbackSummaryKey(bookID, headingID int, lang string) string {
	return fmt.Sprintf("%d:%d:%s", bookID, headingID, lang)
}

func scanPublication(row rowScanner) (entity.BookPublication, error) {
	var publication entity.BookPublication
	var sortOrder sql.NullInt64
	var publishedAt sql.NullTime
	var updatedBy sql.NullString

	err := row.Scan(
		&publication.BookID,
		&publication.Status,
		&publication.Featured,
		&sortOrder,
		&publishedAt,
		&updatedBy,
		&publication.UpdatedAt,
	)
	if err != nil {
		return entity.BookPublication{}, err
	}

	publication.SortOrder = nullableInt(sortOrder)
	publication.PublishedAt = nullableTime(publishedAt)
	publication.UpdatedBy = nullableString(updatedBy)

	return publication, nil
}

func scanMetadataEdit(row rowScanner) (entity.BookMetadataEdit, error) {
	var edit entity.BookMetadataEdit
	var displayTitle sql.NullString
	var description sql.NullString
	var coverURL sql.NullString
	var categoryID sql.NullInt64
	var notes sql.NullString
	var updatedBy sql.NullString
	var publishedAt sql.NullTime

	err := row.Scan(
		&edit.BookID,
		&edit.Status,
		&displayTitle,
		&description,
		&coverURL,
		&categoryID,
		&notes,
		&updatedBy,
		&edit.UpdatedAt,
		&publishedAt,
	)
	if err != nil {
		return entity.BookMetadataEdit{}, err
	}

	edit.DisplayTitle = nullableString(displayTitle)
	edit.Description = nullableString(description)
	edit.CoverURL = nullableString(coverURL)
	edit.CategoryID = nullableInt(categoryID)
	edit.Notes = nullableString(notes)
	edit.UpdatedBy = nullableString(updatedBy)
	edit.PublishedAt = nullableTime(publishedAt)

	return edit, nil
}

func scanPageEdit(row rowScanner) (entity.BookPageEdit, error) {
	var edit entity.BookPageEdit
	var updatedBy sql.NullString
	var publishedAt sql.NullTime

	err := row.Scan(
		&edit.BookID,
		&edit.PageID,
		&edit.Status,
		&edit.ContentHTML,
		&edit.ContentText,
		&updatedBy,
		&edit.UpdatedAt,
		&publishedAt,
	)
	if err != nil {
		return entity.BookPageEdit{}, err
	}

	edit.UpdatedBy = nullableString(updatedBy)
	edit.PublishedAt = nullableTime(publishedAt)

	return edit, nil
}

func scanHeadingEdit(row rowScanner) (entity.BookHeadingEdit, error) {
	var edit entity.BookHeadingEdit
	var updatedBy sql.NullString
	var publishedAt sql.NullTime

	err := row.Scan(
		&edit.BookID,
		&edit.HeadingID,
		&edit.Status,
		&edit.Content,
		&updatedBy,
		&edit.UpdatedAt,
		&publishedAt,
	)
	if err != nil {
		return entity.BookHeadingEdit{}, err
	}

	edit.UpdatedBy = nullableString(updatedBy)
	edit.PublishedAt = nullableTime(publishedAt)

	return edit, nil
}

func scanCollectionItem(row rowScanner) (entity.BookCollectionItem, error) {
	var item entity.BookCollectionItem
	var sortOrder sql.NullInt64
	var createdBy sql.NullString

	err := row.Scan(&item.CollectionSlug, &item.BookID, &sortOrder, &createdBy, &item.CreatedAt)
	if err != nil {
		return entity.BookCollectionItem{}, err
	}

	item.SortOrder = nullableInt(sortOrder)
	item.CreatedBy = nullableString(createdBy)

	return item, nil
}

func scanAdminTranslationFeedback(row rowScanner) (entity.AdminTranslationFeedback, error) {
	var feedback entity.AdminTranslationFeedback
	var userID sql.NullString
	var clientID sql.NullString
	var reason sql.NullString
	var note sql.NullString
	var resolvedBy sql.NullString
	var resolvedAt sql.NullTime
	var resolutionNote sql.NullString
	var userAgent sql.NullString
	var clientIP sql.NullString
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime

	err := row.Scan(
		&feedback.ID,
		&feedback.BookID,
		&feedback.BookTitle,
		&feedback.HeadingID,
		&feedback.HeadingTitle,
		&feedback.Lang,
		&userID,
		&clientID,
		&feedback.Vote,
		&reason,
		&note,
		&feedback.Status,
		&resolvedBy,
		&resolvedAt,
		&resolutionNote,
		&userAgent,
		&clientIP,
		&feedback.TranslationStatus,
		&reviewedBy,
		&reviewedAt,
		&feedback.CreatedAt,
		&feedback.UpdatedAt,
	)
	if err != nil {
		return entity.AdminTranslationFeedback{}, err
	}

	feedback.UserID = nullableString(userID)
	feedback.ClientID = nullableString(clientID)
	feedback.Reason = nullableString(reason)
	feedback.Note = nullableString(note)
	feedback.ResolvedBy = nullableString(resolvedBy)
	feedback.ResolvedAt = nullableTime(resolvedAt)
	feedback.ResolutionNote = nullableString(resolutionNote)
	feedback.UserAgent = nullableString(userAgent)
	feedback.ClientIP = nullableString(clientIP)
	feedback.TranslationReviewedBy = nullableString(reviewedBy)
	feedback.TranslationReviewedAt = nullableTime(reviewedAt)

	return feedback, nil
}

func scanFeedbackHeadingSummary(row rowScanner) (entity.TranslationFeedbackHeadingSummary, error) {
	var item entity.TranslationFeedbackHeadingSummary
	err := row.Scan(
		&item.BookID,
		&item.BookTitle,
		&item.HeadingID,
		&item.HeadingTitle,
		&item.Lang,
		&item.Total,
		&item.Likes,
		&item.Dislikes,
	)
	if err != nil {
		return entity.TranslationFeedbackHeadingSummary{}, err
	}

	item.Reasons = map[string]int{}

	return item, nil
}

func nullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}

	return &value.Time
}
