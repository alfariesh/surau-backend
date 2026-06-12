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

// SaveProgress upserts a user's progress for a book without rolling back stale
// observed events. Target validation and the monotonic upsert share one
// statement so the location cannot be unpublished between check and write.
func (r *PersonalRepo) SaveProgress(ctx context.Context, progress entity.ReadingProgress) (entity.ReadingProgress, error) {
	if progress.BookID <= 0 {
		return entity.ReadingProgress{}, entity.ErrBookNotFound
	}
	if progress.PageID != nil && *progress.PageID <= 0 {
		return entity.ReadingProgress{}, entity.ErrInvalidReaderLocation
	}
	if progress.HeadingID != nil && *progress.HeadingID <= 0 {
		return entity.ReadingProgress{}, entity.ErrInvalidReaderLocation
	}
	if progress.ObservedAt.IsZero() {
		progress.ObservedAt = time.Now().UTC()
	}

	sqlText := `
WITH checks AS (
    SELECT
        EXISTS (
            SELECT 1
            FROM books b
            JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
            WHERE b.id = $2 AND b.is_deleted = false
        ) AS book_ok,
        ($3::int IS NULL OR EXISTS (
            SELECT 1 FROM book_pages WHERE book_id = $2 AND page_id = $3 AND is_deleted = false
        )) AS page_ok,
        ($4::int IS NULL OR EXISTS (
            SELECT 1 FROM book_headings WHERE book_id = $2 AND heading_id = $4 AND is_deleted = false
        )) AS heading_ok
),
upserted AS (
    INSERT INTO reading_progress (user_id, book_id, page_id, heading_id, progress_percent, observed_at, updated_at)
    SELECT $1, $2, $3, $4, $5, $6, now()
    FROM checks
    WHERE book_ok AND page_ok AND heading_ok
    ON CONFLICT (user_id, book_id) DO UPDATE SET
        page_id = CASE
            WHEN reading_progress.observed_at <= EXCLUDED.observed_at THEN EXCLUDED.page_id
            ELSE reading_progress.page_id
        END,
        heading_id = CASE
            WHEN reading_progress.observed_at <= EXCLUDED.observed_at THEN EXCLUDED.heading_id
            ELSE reading_progress.heading_id
        END,
        progress_percent = CASE
            WHEN reading_progress.observed_at <= EXCLUDED.observed_at THEN EXCLUDED.progress_percent
            ELSE reading_progress.progress_percent
        END,
        observed_at = GREATEST(reading_progress.observed_at, EXCLUDED.observed_at),
        updated_at = CASE
            WHEN reading_progress.observed_at <= EXCLUDED.observed_at THEN now()
            ELSE reading_progress.updated_at
        END
    RETURNING user_id, book_id, page_id, heading_id, progress_percent, observed_at, updated_at
)
SELECT c.book_ok, c.page_ok, c.heading_ok,
       u.user_id, u.book_id, u.page_id, u.heading_id, u.progress_percent, u.observed_at, u.updated_at
FROM checks c
LEFT JOIN upserted u ON true`

	var (
		bookOK, pageOK, headingOK bool
		userID                    sql.NullString
		bookID                    sql.NullInt64
		pageID                    sql.NullInt64
		headingID                 sql.NullInt64
		progressPercent           sql.NullFloat64
		observedAt                sql.NullTime
		updatedAt                 sql.NullTime
	)
	if err := r.Pool.QueryRow(
		ctx,
		sqlText,
		progress.UserID,
		progress.BookID,
		progress.PageID,
		progress.HeadingID,
		progress.ProgressPercent,
		progress.ObservedAt,
	).Scan(
		&bookOK, &pageOK, &headingOK,
		&userID, &bookID, &pageID, &headingID, &progressPercent, &observedAt, &updatedAt,
	); err != nil {
		return entity.ReadingProgress{}, fmt.Errorf("PersonalRepo - SaveProgress - Scan: %w", err)
	}

	switch {
	case !bookOK:
		return entity.ReadingProgress{}, entity.ErrBookNotFound
	case !pageOK:
		return entity.ReadingProgress{}, entity.ErrPageNotFound
	case !headingOK:
		return entity.ReadingProgress{}, entity.ErrHeadingNotFound
	case !userID.Valid:
		return entity.ReadingProgress{}, fmt.Errorf("PersonalRepo - SaveProgress: %w", pgx.ErrNoRows)
	}

	saved := entity.ReadingProgress{
		UserID:     userID.String,
		BookID:     int(bookID.Int64),
		PageID:     nullableInt(pageID),
		HeadingID:  nullableInt(headingID),
		ObservedAt: observedAt.Time,
		UpdatedAt:  updatedAt.Time,
	}
	if progressPercent.Valid {
		saved.ProgressPercent = &progressPercent.Float64
	}

	return saved, nil
}

// ListProgress returns a user's in-progress books ordered by recent activity,
// enriched with light book metadata so clients can render a shelf directly.
// Unpublished and deleted books drop off the list.
func (r *PersonalRepo) ListProgress(
	ctx context.Context,
	userID, lang string,
	limit, offset uint64,
) ([]entity.ContinueReadingEntry, int, error) {
	const countSQL = `
SELECT COUNT(*)
FROM reading_progress rp
JOIN books b ON b.id = rp.book_id AND b.is_deleted = false
JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
WHERE rp.user_id = $1`

	var total int
	if err := r.Pool.QueryRow(ctx, countSQL, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListProgress - count: %w", err)
	}

	const dataSQL = `
SELECT rp.user_id,
       rp.book_id,
       rp.page_id,
       rp.heading_id,
       rp.progress_percent,
       rp.observed_at,
       rp.updated_at,
       CASE WHEN bmt.book_id IS NOT NULL THEN bmt.display_title ELSE COALESCE(me.display_title, b.name) END AS book_name,
       me.cover_url,
       CASE WHEN at.author_id IS NOT NULL THEN at.name ELSE a.name END AS author_name
FROM reading_progress rp
JOIN books b ON b.id = rp.book_id AND b.is_deleted = false
JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
LEFT JOIN book_metadata_edits me ON me.book_id = b.id AND me.status = 'published'
LEFT JOIN book_production_projects bpp
    ON bpp.book_id = b.id AND bpp.lang = $2 AND bpp.workflow_status <> 'archived' AND $2 <> 'ar'
LEFT JOIN book_metadata_translations bmt
    ON bmt.book_id = b.id AND bmt.lang = $2 AND bmt.is_deleted = false AND bpp.publication_status = 'published'
LEFT JOIN authors a ON a.id = b.author_id
LEFT JOIN author_translations at
    ON at.author_id = a.id AND at.lang = $2 AND at.is_deleted = false AND bpp.publication_status = 'published'
WHERE rp.user_id = $1
ORDER BY rp.updated_at DESC, rp.book_id ASC
LIMIT $3 OFFSET $4`

	rows, err := r.Pool.Query(ctx, dataSQL, userID, lang, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListProgress - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	entries := make([]entity.ContinueReadingEntry, 0, limit)
	for rows.Next() {
		var entry entity.ContinueReadingEntry
		var pageID, headingID sql.NullInt64
		var progressPercent sql.NullFloat64
		var coverURL, authorName sql.NullString

		if err := rows.Scan(
			&entry.UserID,
			&entry.BookID,
			&pageID,
			&headingID,
			&progressPercent,
			&entry.ObservedAt,
			&entry.UpdatedAt,
			&entry.Book.Name,
			&coverURL,
			&authorName,
		); err != nil {
			return nil, 0, fmt.Errorf("PersonalRepo - ListProgress - rows.Scan: %w", err)
		}

		entry.PageID = nullableInt(pageID)
		entry.HeadingID = nullableInt(headingID)
		if progressPercent.Valid {
			entry.ProgressPercent = &progressPercent.Float64
		}
		entry.Book.BookID = entry.BookID
		entry.Book.CoverURL = nullableString(coverURL)
		entry.Book.AuthorName = nullableString(authorName)

		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListProgress - rows.Err: %w", err)
	}

	return entries, total, nil
}

// GetQuranProgress returns the most recently observed Quran resume position.
func (r *PersonalRepo) GetQuranProgress(ctx context.Context, userID string) (entity.QuranReadingProgress, error) {
	sqlText, args, err := r.quranProgressSelectBuilder().
		Where(sq.Eq{"qrp.user_id": userID}).
		OrderBy("qrp.observed_at DESC", "qrp.updated_at DESC", "qrp.surah_id ASC").
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
		Where(sq.Eq{"qrp.user_id": userID, "qrp.surah_id": surahID}).
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
		Where(sq.Eq{"qrp.user_id": userID}).
		OrderBy("qrp.observed_at DESC", "qrp.updated_at DESC", "qrp.surah_id ASC").
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
),
upserted AS (
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
    RETURNING user_id, surah_id, ayah_number, ayah_key, position_percent, observed_at, updated_at
)
SELECT u.user_id, u.surah_id, u.ayah_number, u.ayah_key, u.position_percent, u.observed_at, u.updated_at,
       a.page_number, a.juz_number, a.hizb_number
FROM upserted u
JOIN quran_ayahs a ON a.ayah_key = u.ayah_key`

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
		OrderBy("updated_at DESC", "created_at DESC", "id DESC").
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
// Target validation and the upsert share one statement so the target cannot
// be unpublished between check and write. On conflict only provided
// (non-null) metadata overwrites the stored row; clearing is PATCH's job.
// The returned bool reports whether a new row was created.
func (r *PersonalRepo) UpsertSavedItem(ctx context.Context, item entity.SavedItem) (entity.SavedItem, bool, error) {
	conflictTarget, err := savedItemConflictTarget(item.ItemType)
	if err != nil {
		return entity.SavedItem{}, false, err
	}
	checks, err := savedItemChecks(item.ItemType)
	if err != nil {
		return entity.SavedItem{}, false, err
	}

	sqlText := fmt.Sprintf(`
WITH checks AS (
%s
),
upserted AS (
    INSERT INTO saved_items (
        id, user_id, item_type, book_id, page_id, heading_id, surah_id, ayah_key,
        from_ayah_number, to_ayah_number, label, note, tags, created_at, updated_at
    )
    SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, COALESCE($13::text[], ARRAY[]::TEXT[]), now(), now()
    FROM checks
    WHERE primary_ok AND target_ok
    ON CONFLICT %s DO UPDATE SET
        label = COALESCE(EXCLUDED.label, saved_items.label),
        note = COALESCE(EXCLUDED.note, saved_items.note),
        tags = CASE WHEN $13::text[] IS NULL THEN saved_items.tags ELSE $13::text[] END,
        updated_at = now()
    RETURNING id, user_id, item_type, book_id, page_id, heading_id, surah_id, ayah_key,
              from_ayah_number, to_ayah_number, label, note, tags, created_at, updated_at,
              (xmax = 0) AS created
)
SELECT c.primary_ok, c.target_ok,
       u.id, u.user_id, u.item_type, u.book_id, u.page_id, u.heading_id, u.surah_id, u.ayah_key,
       u.from_ayah_number, u.to_ayah_number, u.label, u.note, u.tags, u.created_at, u.updated_at, u.created
FROM checks c
LEFT JOIN upserted u ON true`, checks.sql, conflictTarget)

	var (
		primaryOK, targetOK bool
		id, userID          sql.NullString
		itemType            sql.NullString
		bookID              sql.NullInt64
		pageID              sql.NullInt64
		headingID           sql.NullInt64
		surahID             sql.NullInt64
		ayahKey             sql.NullString
		fromAyahNumber      sql.NullInt64
		toAyahNumber        sql.NullInt64
		label               sql.NullString
		note                sql.NullString
		tags                []string
		createdAt           sql.NullTime
		updatedAt           sql.NullTime
		created             sql.NullBool
	)
	if err := r.Pool.QueryRow(
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
	).Scan(
		&primaryOK, &targetOK,
		&id, &userID, &itemType, &bookID, &pageID, &headingID, &surahID, &ayahKey,
		&fromAyahNumber, &toAyahNumber, &label, &note, &tags, &createdAt, &updatedAt, &created,
	); err != nil {
		return entity.SavedItem{}, false, fmt.Errorf("PersonalRepo - UpsertSavedItem - Scan: %w", err)
	}

	switch {
	case !primaryOK:
		return entity.SavedItem{}, false, checks.primaryErr
	case !targetOK:
		return entity.SavedItem{}, false, checks.targetErr
	case !id.Valid:
		return entity.SavedItem{}, false, fmt.Errorf("PersonalRepo - UpsertSavedItem: %w", pgx.ErrNoRows)
	}

	if tags == nil {
		tags = []string{}
	}

	saved := entity.SavedItem{
		ID:             id.String,
		UserID:         userID.String,
		ItemType:       itemType.String,
		BookID:         nullableInt(bookID),
		PageID:         nullableInt(pageID),
		HeadingID:      nullableInt(headingID),
		SurahID:        nullableInt(surahID),
		AyahKey:        nullableString(ayahKey),
		FromAyahNumber: nullableInt(fromAyahNumber),
		ToAyahNumber:   nullableInt(toAyahNumber),
		Label:          nullableString(label),
		Note:           nullableString(note),
		Tags:           tags,
		CreatedAt:      createdAt.Time,
		UpdatedAt:      updatedAt.Time,
	}

	return saved, created.Bool, nil
}

// UpdateSavedItem applies a partial metadata update: only fields flagged as
// set are written, so absent PATCH fields stay unchanged.
func (r *PersonalRepo) UpdateSavedItem(
	ctx context.Context,
	userID, savedItemID string,
	patch entity.SavedItemPatch,
) (entity.SavedItem, error) {
	builder := r.Builder.
		Update("saved_items").
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": savedItemID, "user_id": userID}).
		Suffix(`RETURNING id, user_id, item_type, book_id, page_id, heading_id, surah_id, ayah_key,
          from_ayah_number, to_ayah_number, label, note, tags, created_at, updated_at`)

	if patch.LabelSet {
		builder = builder.Set("label", patch.Label)
	}
	if patch.NoteSet {
		builder = builder.Set("note", patch.Note)
	}
	if patch.TagsSet {
		tags := patch.Tags
		if tags == nil {
			tags = []string{}
		}
		builder = builder.Set("tags", tags)
	}

	sqlText, args, err := builder.ToSql()
	if err != nil {
		return entity.SavedItem{}, fmt.Errorf("PersonalRepo - UpdateSavedItem - r.Builder: %w", err)
	}

	updated, err := scanSavedItem(r.Pool.QueryRow(ctx, sqlText, args...))
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

// savedItemTargetChecks pairs the per-type validation CTE body with the
// errors reported when its flags come back false. The SQL references the
// shared upsert parameters: $4 book_id, $5 page_id, $6 heading_id,
// $7 surah_id, $8 ayah_key, $9 from_ayah_number, $10 to_ayah_number.
type savedItemTargetChecks struct {
	sql        string
	primaryErr error
	targetErr  error
}

func savedItemChecks(itemType string) (savedItemTargetChecks, error) {
	switch itemType {
	case entity.SavedItemTypeBookPage:
		return savedItemTargetChecks{
			sql: `    SELECT
        EXISTS (
            SELECT 1
            FROM books b
            JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
            WHERE b.id = $4 AND b.is_deleted = false
        ) AS primary_ok,
        EXISTS (
            SELECT 1 FROM book_pages WHERE book_id = $4 AND page_id = $5 AND is_deleted = false
        ) AS target_ok`,
			primaryErr: entity.ErrBookNotFound,
			targetErr:  entity.ErrPageNotFound,
		}, nil
	case entity.SavedItemTypeBookHeading:
		return savedItemTargetChecks{
			sql: `    SELECT
        EXISTS (
            SELECT 1
            FROM books b
            JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
            WHERE b.id = $4 AND b.is_deleted = false
        ) AS primary_ok,
        EXISTS (
            SELECT 1 FROM book_headings WHERE book_id = $4 AND heading_id = $6 AND is_deleted = false
        ) AS target_ok`,
			primaryErr: entity.ErrBookNotFound,
			targetErr:  entity.ErrHeadingNotFound,
		}, nil
	case entity.SavedItemTypeQuranAyah:
		return savedItemTargetChecks{
			sql: `    SELECT
        EXISTS (
            SELECT 1 FROM quran_ayahs WHERE ayah_key = $8 AND surah_id = $7
        ) AS primary_ok,
        true AS target_ok`,
			primaryErr: entity.ErrQuranAyahNotFound,
			targetErr:  entity.ErrQuranAyahNotFound,
		}, nil
	case entity.SavedItemTypeQuranRange:
		return savedItemTargetChecks{
			sql: `    SELECT
        EXISTS (
            SELECT 1 FROM quran_surahs WHERE surah_id = $7
        ) AS primary_ok,
        (
            (SELECT COUNT(*) FROM quran_ayahs WHERE surah_id = $7 AND ayah_number BETWEEN $9 AND $10)
            = $10::int - $9::int + 1
        ) AS target_ok`,
			primaryErr: entity.ErrQuranSurahNotFound,
			targetErr:  entity.ErrQuranAyahNotFound,
		}, nil
	default:
		return savedItemTargetChecks{}, entity.ErrInvalidSavedItem
	}
}

func (r *PersonalRepo) progressSelectBuilder() sq.SelectBuilder {
	return r.Builder.Select(
		"user_id",
		"book_id",
		"page_id",
		"heading_id",
		"progress_percent",
		"observed_at",
		"updated_at",
	).From("reading_progress")
}

func (r *PersonalRepo) quranProgressSelectBuilder() sq.SelectBuilder {
	return r.Builder.Select(
		"qrp.user_id",
		"qrp.surah_id",
		"qrp.ayah_number",
		"qrp.ayah_key",
		"qrp.position_percent",
		"qrp.observed_at",
		"qrp.updated_at",
		"a.page_number",
		"a.juz_number",
		"a.hizb_number",
	).
		From("quran_reading_progress qrp").
		LeftJoin("quran_ayahs a ON a.ayah_key = qrp.ayah_key")
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
		&progress.ObservedAt,
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
	var pageNumber, juzNumber, hizbNumber sql.NullInt64

	err := row.Scan(
		&progress.UserID,
		&progress.SurahID,
		&progress.AyahNumber,
		&progress.AyahKey,
		&progress.PositionPercent,
		&progress.ObservedAt,
		&progress.UpdatedAt,
		&pageNumber,
		&juzNumber,
		&hizbNumber,
	)
	if err != nil {
		return entity.QuranReadingProgress{}, err
	}

	progress.PageNumber = nullableInt(pageNumber)
	progress.JuzNumber = nullableInt(juzNumber)
	progress.HizbNumber = nullableInt(hizbNumber)

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
