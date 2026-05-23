package persistent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

// PersonalRepo stores authenticated reader data.
type PersonalRepo struct {
	*postgres.Postgres
}

// NewPersonalRepo creates a personal repository.
func NewPersonalRepo(pg *postgres.Postgres) *PersonalRepo {
	return &PersonalRepo{pg}
}

// GetProgress returns a user's progress for a book.
func (r *PersonalRepo) GetProgress(ctx context.Context, userID string, bookID int) (entity.ReadingProgress, error) {
	sqlText, args, err := r.progressSelectBuilder().
		Where(sq.Eq{"user_id": userID, "book_id": bookID}).
		ToSql()
	if err != nil {
		return entity.ReadingProgress{}, fmt.Errorf("PersonalRepo - GetProgress - r.Builder: %w", err)
	}

	progress, err := scanProgress(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.ReadingProgress{}, entity.ErrProgressNotFound
		}

		return entity.ReadingProgress{}, fmt.Errorf("PersonalRepo - GetProgress - scanProgress: %w", err)
	}

	return progress, nil
}

// SaveProgress upserts a user's progress for a book.
func (r *PersonalRepo) SaveProgress(ctx context.Context, progress entity.ReadingProgress) (entity.ReadingProgress, error) {
	if err := r.validateReaderLocation(ctx, progress.BookID, progress.PageID, progress.HeadingID, false); err != nil {
		return entity.ReadingProgress{}, err
	}

	sqlText := `
INSERT INTO reading_progress (user_id, book_id, page_id, heading_id, progress_percent, updated_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (user_id, book_id) DO UPDATE SET
    page_id = EXCLUDED.page_id,
    heading_id = EXCLUDED.heading_id,
    progress_percent = EXCLUDED.progress_percent,
    updated_at = now()
RETURNING user_id, book_id, page_id, heading_id, progress_percent, updated_at`

	saved, err := scanProgress(r.Pool.QueryRow(
		ctx,
		sqlText,
		progress.UserID,
		progress.BookID,
		progress.PageID,
		progress.HeadingID,
		progress.ProgressPercent,
	))
	if err != nil {
		return entity.ReadingProgress{}, fmt.Errorf("PersonalRepo - SaveProgress - scanProgress: %w", err)
	}

	return saved, nil
}

// ListBookmarks returns paginated bookmarks for a user.
func (r *PersonalRepo) ListBookmarks(ctx context.Context, userID string, filter repo.BookmarkFilter) ([]entity.Bookmark, int, error) {
	countBuilder := r.Builder.Select("COUNT(*)").From("bookmarks").Where(sq.Eq{"user_id": userID})
	dataBuilder := r.bookmarkSelectBuilder().
		Where(sq.Eq{"user_id": userID}).
		OrderBy("updated_at DESC", "created_at DESC").
		Limit(filter.Limit).
		Offset(filter.Offset)

	if filter.BookID != nil {
		countBuilder = countBuilder.Where(sq.Eq{"book_id": *filter.BookID})
		dataBuilder = dataBuilder.Where(sq.Eq{"book_id": *filter.BookID})
	}

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListBookmarks - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListBookmarks - r.Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListBookmarks - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	bookmarks := make([]entity.Bookmark, 0, filter.Limit)
	for rows.Next() {
		bookmark, err := scanBookmark(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("PersonalRepo - ListBookmarks - scanBookmark: %w", err)
		}

		bookmarks = append(bookmarks, bookmark)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListBookmarks - rows.Err: %w", err)
	}

	return bookmarks, total, nil
}

// CreateBookmark inserts a bookmark.
func (r *PersonalRepo) CreateBookmark(ctx context.Context, bookmark entity.Bookmark) (entity.Bookmark, error) {
	if err := r.validateReaderLocation(ctx, bookmark.BookID, bookmark.PageID, bookmark.HeadingID, true); err != nil {
		return entity.Bookmark{}, err
	}

	sqlText := `
INSERT INTO bookmarks (id, user_id, book_id, page_id, heading_id, label, note, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
RETURNING id, user_id, book_id, page_id, heading_id, label, note, created_at, updated_at`

	created, err := scanBookmark(r.Pool.QueryRow(
		ctx,
		sqlText,
		bookmark.ID,
		bookmark.UserID,
		bookmark.BookID,
		bookmark.PageID,
		bookmark.HeadingID,
		bookmark.Label,
		bookmark.Note,
	))
	if err != nil {
		return entity.Bookmark{}, fmt.Errorf("PersonalRepo - CreateBookmark - scanBookmark: %w", err)
	}

	return created, nil
}

// DeleteBookmark removes one bookmark belonging to a user.
func (r *PersonalRepo) DeleteBookmark(ctx context.Context, userID, bookmarkID string) error {
	sqlText, args, err := r.Builder.
		Delete("bookmarks").
		Where(sq.Eq{"id": bookmarkID, "user_id": userID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("PersonalRepo - DeleteBookmark - r.Builder: %w", err)
	}

	result, err := r.Pool.Exec(ctx, sqlText, args...)
	if err != nil {
		return fmt.Errorf("PersonalRepo - DeleteBookmark - r.Pool.Exec: %w", err)
	}

	if result.RowsAffected() == 0 {
		return entity.ErrBookmarkNotFound
	}

	return nil
}

func (r *PersonalRepo) validateReaderLocation(ctx context.Context, bookID int, pageID, headingID *int, requireLocation bool) error {
	if bookID <= 0 {
		return entity.ErrBookNotFound
	}
	if requireLocation && pageID == nil && headingID == nil {
		return entity.ErrInvalidReaderLocation
	}
	if pageID != nil && *pageID <= 0 {
		return entity.ErrInvalidReaderLocation
	}
	if headingID != nil && *headingID <= 0 {
		return entity.ErrInvalidReaderLocation
	}

	var exists bool
	if err := r.Pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM books b
    JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
    WHERE b.id = $1 AND b.is_deleted = false
)`,
		bookID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("PersonalRepo - validateReaderLocation - book: %w", err)
	}
	if !exists {
		return entity.ErrBookNotFound
	}

	if pageID != nil {
		if err := r.Pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM book_pages
    WHERE book_id = $1 AND page_id = $2 AND is_deleted = false
)`,
			bookID,
			*pageID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("PersonalRepo - validateReaderLocation - page: %w", err)
		}
		if !exists {
			return entity.ErrPageNotFound
		}
	}

	if headingID != nil {
		if err := r.Pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM book_headings
    WHERE book_id = $1 AND heading_id = $2 AND is_deleted = false
)`,
			bookID,
			*headingID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("PersonalRepo - validateReaderLocation - heading: %w", err)
		}
		if !exists {
			return entity.ErrHeadingNotFound
		}
	}

	return nil
}

func (r *PersonalRepo) progressSelectBuilder() sq.SelectBuilder {
	return r.Builder.Select(
		"user_id",
		"book_id",
		"page_id",
		"heading_id",
		"progress_percent",
		"updated_at",
	).From("reading_progress")
}

func (r *PersonalRepo) bookmarkSelectBuilder() sq.SelectBuilder {
	return r.Builder.Select(
		"id",
		"user_id",
		"book_id",
		"page_id",
		"heading_id",
		"label",
		"note",
		"created_at",
		"updated_at",
	).From("bookmarks")
}

func (r *PersonalRepo) count(ctx context.Context, builder sq.SelectBuilder) (int, error) {
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

func scanProgress(row rowScanner) (entity.ReadingProgress, error) {
	var progress entity.ReadingProgress
	var pageID sql.NullInt64
	var headingID sql.NullInt64
	var progressPercent sql.NullFloat64

	err := row.Scan(
		&progress.UserID,
		&progress.BookID,
		&pageID,
		&headingID,
		&progressPercent,
		&progress.UpdatedAt,
	)
	if err != nil {
		return entity.ReadingProgress{}, err
	}

	progress.PageID = nullableInt(pageID)
	progress.HeadingID = nullableInt(headingID)
	if progressPercent.Valid {
		progress.ProgressPercent = &progressPercent.Float64
	}

	return progress, nil
}

func scanBookmark(row rowScanner) (entity.Bookmark, error) {
	var bookmark entity.Bookmark
	var pageID sql.NullInt64
	var headingID sql.NullInt64
	var label sql.NullString
	var note sql.NullString

	err := row.Scan(
		&bookmark.ID,
		&bookmark.UserID,
		&bookmark.BookID,
		&pageID,
		&headingID,
		&label,
		&note,
		&bookmark.CreatedAt,
		&bookmark.UpdatedAt,
	)
	if err != nil {
		return entity.Bookmark{}, err
	}

	bookmark.PageID = nullableInt(pageID)
	bookmark.HeadingID = nullableInt(headingID)
	bookmark.Label = nullableString(label)
	bookmark.Note = nullableString(note)

	return bookmark, nil
}
