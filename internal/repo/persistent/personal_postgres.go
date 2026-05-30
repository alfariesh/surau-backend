package persistent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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

// GetQuranProgress returns the most recently observed Quran resume position.
func (r *PersonalRepo) GetQuranProgress(ctx context.Context, userID string) (entity.QuranReadingProgress, error) {
	sqlText, args, err := r.quranProgressSelectBuilder().
		Where(sq.Eq{"user_id": userID}).
		OrderBy("observed_at DESC", "updated_at DESC", "surah_id ASC").
		Limit(1).
		ToSql()
	if err != nil {
		return entity.QuranReadingProgress{}, fmt.Errorf("PersonalRepo - GetQuranProgress - r.Builder: %w", err)
	}

	progress, err := scanQuranProgress(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranReadingProgress{}, entity.ErrProgressNotFound
		}

		return entity.QuranReadingProgress{}, fmt.Errorf("PersonalRepo - GetQuranProgress - scanQuranProgress: %w", err)
	}

	return progress, nil
}

// GetQuranSurahProgress returns one user's Quran resume position for a surah.
func (r *PersonalRepo) GetQuranSurahProgress(
	ctx context.Context,
	userID string,
	surahID int,
) (entity.QuranReadingProgress, error) {
	sqlText, args, err := r.quranProgressSelectBuilder().
		Where(sq.Eq{"user_id": userID, "surah_id": surahID}).
		ToSql()
	if err != nil {
		return entity.QuranReadingProgress{}, fmt.Errorf("PersonalRepo - GetQuranSurahProgress - r.Builder: %w", err)
	}

	progress, err := scanQuranProgress(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranReadingProgress{}, entity.ErrProgressNotFound
		}

		return entity.QuranReadingProgress{}, fmt.Errorf("PersonalRepo - GetQuranSurahProgress - scanQuranProgress: %w", err)
	}

	return progress, nil
}

// ListQuranSurahProgress returns all per-surah Quran resume positions for a user.
func (r *PersonalRepo) ListQuranSurahProgress(ctx context.Context, userID string) ([]entity.QuranReadingProgress, error) {
	sqlText, args, err := r.quranProgressSelectBuilder().
		Where(sq.Eq{"user_id": userID}).
		OrderBy("observed_at DESC", "updated_at DESC", "surah_id ASC").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - ListQuranSurahProgress - r.Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - ListQuranSurahProgress - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	items := make([]entity.QuranReadingProgress, 0)
	for rows.Next() {
		progress, err := scanQuranProgress(rows)
		if err != nil {
			return nil, fmt.Errorf("PersonalRepo - ListQuranSurahProgress - scanQuranProgress: %w", err)
		}

		items = append(items, progress)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - ListQuranSurahProgress - rows.Err: %w", err)
	}

	return items, nil
}

// SaveQuranProgress upserts one Quran resume position without rolling back stale observed events.
func (r *PersonalRepo) SaveQuranProgress(
	ctx context.Context,
	progress entity.QuranReadingProgress,
) (entity.QuranReadingProgress, error) {
	if progress.ObservedAt.IsZero() {
		progress.ObservedAt = time.Now().UTC()
	}

	sqlText := `
WITH target AS (
    SELECT
        a.surah_id,
        a.ayah_number,
        a.ayah_key,
        ROUND((a.ayah_number::numeric / GREATEST(s.ayah_count, 1)) * 100, 2) AS position_percent
    FROM quran_ayahs a
    JOIN quran_surahs s ON s.surah_id = a.surah_id
    WHERE a.ayah_key = $2
)
INSERT INTO quran_reading_progress (
    user_id, surah_id, ayah_number, ayah_key, position_percent, observed_at, updated_at
)
SELECT $1, surah_id, ayah_number, ayah_key, position_percent, $3, now()
FROM target
ON CONFLICT (user_id, surah_id) DO UPDATE SET
    ayah_number = CASE
        WHEN quran_reading_progress.observed_at <= EXCLUDED.observed_at THEN EXCLUDED.ayah_number
        ELSE quran_reading_progress.ayah_number
    END,
    ayah_key = CASE
        WHEN quran_reading_progress.observed_at <= EXCLUDED.observed_at THEN EXCLUDED.ayah_key
        ELSE quran_reading_progress.ayah_key
    END,
    position_percent = CASE
        WHEN quran_reading_progress.observed_at <= EXCLUDED.observed_at THEN EXCLUDED.position_percent
        ELSE quran_reading_progress.position_percent
    END,
    observed_at = GREATEST(quran_reading_progress.observed_at, EXCLUDED.observed_at),
    updated_at = CASE
        WHEN quran_reading_progress.observed_at <= EXCLUDED.observed_at THEN now()
        ELSE quran_reading_progress.updated_at
    END
RETURNING user_id, surah_id, ayah_number, ayah_key, position_percent, observed_at, updated_at`

	saved, err := scanQuranProgress(r.Pool.QueryRow(
		ctx,
		sqlText,
		progress.UserID,
		progress.AyahKey,
		progress.ObservedAt,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranReadingProgress{}, entity.ErrQuranAyahNotFound
		}

		return entity.QuranReadingProgress{}, fmt.Errorf("PersonalRepo - SaveQuranProgress - scanQuranProgress: %w", err)
	}

	return saved, nil
}

// ListSavedItems returns paginated saved items for a user.
func (r *PersonalRepo) ListSavedItems(ctx context.Context, userID string, filter repo.SavedItemFilter) ([]entity.SavedItem, int, error) {
	countBuilder := r.Builder.Select("COUNT(*)").From("saved_items").Where(sq.Eq{"user_id": userID})
	dataBuilder := r.savedItemSelectBuilder().
		Where(sq.Eq{"user_id": userID}).
		OrderBy("updated_at DESC", "created_at DESC").
		Limit(filter.Limit).
		Offset(filter.Offset)

	if filter.ItemType != "" {
		countBuilder = countBuilder.Where(sq.Eq{"item_type": filter.ItemType})
		dataBuilder = dataBuilder.Where(sq.Eq{"item_type": filter.ItemType})
	}
	if filter.BookID != nil {
		countBuilder = countBuilder.Where(sq.Eq{"book_id": *filter.BookID})
		dataBuilder = dataBuilder.Where(sq.Eq{"book_id": *filter.BookID})
	}
	if filter.SurahID != nil {
		countBuilder = countBuilder.Where(sq.Eq{"surah_id": *filter.SurahID})
		dataBuilder = dataBuilder.Where(sq.Eq{"surah_id": *filter.SurahID})
	}
	if filter.Tag != "" {
		tagCondition := sq.Expr("? = ANY(tags)", filter.Tag)
		countBuilder = countBuilder.Where(tagCondition)
		dataBuilder = dataBuilder.Where(tagCondition)
	}

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListSavedItems - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListSavedItems - r.Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListSavedItems - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	items := make([]entity.SavedItem, 0, filter.Limit)
	for rows.Next() {
		item, err := scanSavedItem(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("PersonalRepo - ListSavedItems - scanSavedItem: %w", err)
		}

		items = append(items, item)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListSavedItems - rows.Err: %w", err)
	}

	return items, total, nil
}

// UpsertSavedItem inserts or updates a saved item for the same target.
func (r *PersonalRepo) UpsertSavedItem(ctx context.Context, item entity.SavedItem) (entity.SavedItem, error) {
	if err := r.validateSavedItemTarget(ctx, item); err != nil {
		return entity.SavedItem{}, err
	}
	conflictTarget, err := savedItemConflictTarget(item.ItemType)
	if err != nil {
		return entity.SavedItem{}, err
	}

	sqlText := fmt.Sprintf(`
INSERT INTO saved_items (
    id, user_id, item_type, book_id, page_id, heading_id, surah_id, ayah_key,
    from_ayah_number, to_ayah_number, label, note, tags, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now(), now())
ON CONFLICT %s DO UPDATE SET
    label = EXCLUDED.label,
    note = EXCLUDED.note,
    tags = EXCLUDED.tags,
    updated_at = now()
RETURNING id, user_id, item_type, book_id, page_id, heading_id, surah_id, ayah_key,
          from_ayah_number, to_ayah_number, label, note, tags, created_at, updated_at`, conflictTarget)

	saved, err := scanSavedItem(r.Pool.QueryRow(
		ctx,
		sqlText,
		item.ID,
		item.UserID,
		item.ItemType,
		item.BookID,
		item.PageID,
		item.HeadingID,
		item.SurahID,
		item.AyahKey,
		item.FromAyahNumber,
		item.ToAyahNumber,
		item.Label,
		item.Note,
		item.Tags,
	))
	if err != nil {
		return entity.SavedItem{}, fmt.Errorf("PersonalRepo - UpsertSavedItem - scanSavedItem: %w", err)
	}

	return saved, nil
}

// UpdateSavedItem updates saved item metadata.
func (r *PersonalRepo) UpdateSavedItem(ctx context.Context, item entity.SavedItem) (entity.SavedItem, error) {
	sqlText := `
UPDATE saved_items
SET label = $3,
    note = $4,
    tags = $5,
    updated_at = now()
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, item_type, book_id, page_id, heading_id, surah_id, ayah_key,
          from_ayah_number, to_ayah_number, label, note, tags, created_at, updated_at`

	updated, err := scanSavedItem(r.Pool.QueryRow(ctx, sqlText, item.ID, item.UserID, item.Label, item.Note, item.Tags))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.SavedItem{}, entity.ErrSavedItemNotFound
		}

		return entity.SavedItem{}, fmt.Errorf("PersonalRepo - UpdateSavedItem - scanSavedItem: %w", err)
	}

	return updated, nil
}

// DeleteSavedItem removes one saved item belonging to a user.
func (r *PersonalRepo) DeleteSavedItem(ctx context.Context, userID, savedItemID string) error {
	sqlText, args, err := r.Builder.
		Delete("saved_items").
		Where(sq.Eq{"id": savedItemID, "user_id": userID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("PersonalRepo - DeleteSavedItem - r.Builder: %w", err)
	}

	result, err := r.Pool.Exec(ctx, sqlText, args...)
	if err != nil {
		return fmt.Errorf("PersonalRepo - DeleteSavedItem - r.Pool.Exec: %w", err)
	}

	if result.RowsAffected() == 0 {
		return entity.ErrSavedItemNotFound
	}

	return nil
}

// ListSavedItemTags returns distinct tags used by one user's saved items.
func (r *PersonalRepo) ListSavedItemTags(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT DISTINCT tag.value
FROM saved_items si
CROSS JOIN LATERAL unnest(si.tags) AS tag(value)
WHERE si.user_id = $1
ORDER BY tag.value ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - ListSavedItemTags - Query: %w", err)
	}
	defer rows.Close()

	tags := make([]string, 0)
	for rows.Next() {
		var tag string
		if err = rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("PersonalRepo - ListSavedItemTags - Scan: %w", err)
		}
		tags = append(tags, tag)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - ListSavedItemTags - rows.Err: %w", err)
	}

	return tags, nil
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

func (r *PersonalRepo) validateSavedItemTarget(ctx context.Context, item entity.SavedItem) error {
	switch item.ItemType {
	case entity.SavedItemTypeBookPage:
		if item.BookID == nil || item.PageID == nil {
			return entity.ErrInvalidSavedItem
		}
		return r.validateReaderLocation(ctx, *item.BookID, item.PageID, nil, true)
	case entity.SavedItemTypeBookHeading:
		if item.BookID == nil || item.HeadingID == nil {
			return entity.ErrInvalidSavedItem
		}
		return r.validateReaderLocation(ctx, *item.BookID, nil, item.HeadingID, true)
	case entity.SavedItemTypeQuranAyah:
		if item.AyahKey == nil || item.SurahID == nil {
			return entity.ErrInvalidSavedItem
		}

		var exists bool
		if err := r.Pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM quran_ayahs
    WHERE ayah_key = $1 AND surah_id = $2
)`, *item.AyahKey, *item.SurahID).Scan(&exists); err != nil {
			return fmt.Errorf("PersonalRepo - validateSavedItemTarget - quran_ayah: %w", err)
		}
		if !exists {
			return entity.ErrQuranAyahNotFound
		}
	case entity.SavedItemTypeQuranRange:
		if item.SurahID == nil || item.FromAyahNumber == nil || item.ToAyahNumber == nil {
			return entity.ErrInvalidSavedItem
		}

		var surahExists bool
		if err := r.Pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM quran_surahs
    WHERE surah_id = $1
)`, *item.SurahID).Scan(&surahExists); err != nil {
			return fmt.Errorf("PersonalRepo - validateSavedItemTarget - quran_surah: %w", err)
		}
		if !surahExists {
			return entity.ErrQuranSurahNotFound
		}

		var ayahCount int
		if err := r.Pool.QueryRow(ctx, `
SELECT COUNT(*)::int
FROM quran_ayahs
WHERE surah_id = $1 AND ayah_number BETWEEN $2 AND $3`,
			*item.SurahID,
			*item.FromAyahNumber,
			*item.ToAyahNumber,
		).Scan(&ayahCount); err != nil {
			return fmt.Errorf("PersonalRepo - validateSavedItemTarget - quran_range: %w", err)
		}
		if ayahCount != *item.ToAyahNumber-*item.FromAyahNumber+1 {
			return entity.ErrQuranAyahNotFound
		}
	default:
		return entity.ErrInvalidSavedItem
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

func (r *PersonalRepo) quranProgressSelectBuilder() sq.SelectBuilder {
	return r.Builder.Select(
		"user_id",
		"surah_id",
		"ayah_number",
		"ayah_key",
		"position_percent",
		"observed_at",
		"updated_at",
	).From("quran_reading_progress")
}

func (r *PersonalRepo) savedItemSelectBuilder() sq.SelectBuilder {
	return r.Builder.Select(
		"id",
		"user_id",
		"item_type",
		"book_id",
		"page_id",
		"heading_id",
		"surah_id",
		"ayah_key",
		"from_ayah_number",
		"to_ayah_number",
		"label",
		"note",
		"tags",
		"created_at",
		"updated_at",
	).From("saved_items")
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

func scanQuranProgress(row rowScanner) (entity.QuranReadingProgress, error) {
	var progress entity.QuranReadingProgress

	err := row.Scan(
		&progress.UserID,
		&progress.SurahID,
		&progress.AyahNumber,
		&progress.AyahKey,
		&progress.PositionPercent,
		&progress.ObservedAt,
		&progress.UpdatedAt,
	)
	if err != nil {
		return entity.QuranReadingProgress{}, err
	}

	return progress, nil
}

func scanSavedItem(row rowScanner) (entity.SavedItem, error) {
	var item entity.SavedItem
	var pageID sql.NullInt64
	var headingID sql.NullInt64
	var bookID sql.NullInt64
	var surahID sql.NullInt64
	var ayahKey sql.NullString
	var fromAyahNumber sql.NullInt64
	var toAyahNumber sql.NullInt64
	var label sql.NullString
	var note sql.NullString
	var tags []string

	err := row.Scan(
		&item.ID,
		&item.UserID,
		&item.ItemType,
		&bookID,
		&pageID,
		&headingID,
		&surahID,
		&ayahKey,
		&fromAyahNumber,
		&toAyahNumber,
		&label,
		&note,
		&tags,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return entity.SavedItem{}, err
	}

	item.BookID = nullableInt(bookID)
	item.PageID = nullableInt(pageID)
	item.HeadingID = nullableInt(headingID)
	item.SurahID = nullableInt(surahID)
	item.AyahKey = nullableString(ayahKey)
	item.FromAyahNumber = nullableInt(fromAyahNumber)
	item.ToAyahNumber = nullableInt(toAyahNumber)
	item.Label = nullableString(label)
	item.Note = nullableString(note)
	if tags == nil {
		tags = []string{}
	}
	item.Tags = tags

	return item, nil
}

func savedItemConflictTarget(itemType string) (string, error) {
	switch itemType {
	case entity.SavedItemTypeBookPage:
		return "(user_id, item_type, book_id, page_id) WHERE item_type = 'book_page'", nil
	case entity.SavedItemTypeBookHeading:
		return "(user_id, item_type, book_id, heading_id) WHERE item_type = 'book_heading'", nil
	case entity.SavedItemTypeQuranAyah:
		return "(user_id, item_type, ayah_key) WHERE item_type = 'quran_ayah'", nil
	case entity.SavedItemTypeQuranRange:
		return "(user_id, item_type, surah_id, from_ayah_number, to_ayah_number) WHERE item_type = 'quran_range'", nil
	default:
		return "", entity.ErrInvalidSavedItem
	}
}
