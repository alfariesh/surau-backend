package persistent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Percent values carry two decimal places: scale by percentScale before
// rounding, then divide by percentFull.
const (
	percentFull  = 100
	percentScale = 10000
)

// khatamCycleColumns is the shared select list for one cycle row plus its
// aggregated juz marks (completed_juz int[] and juz_count).
const khatamCycleColumns = `c.id, c.user_id, c.started_at, c.completed_at, c.notes, c.created_at, c.updated_at,
       COALESCE(m.juz, ARRAY[]::INTEGER[]) AS completed_juz,
       COALESCE(m.cnt, 0) AS juz_count`

// khatamMarksLateral aggregates marks per cycle; bounded at 30 rows per cycle.
const khatamMarksLateral = `
LEFT JOIN LATERAL (
    SELECT array_agg(j.juz_number ORDER BY j.juz_number) AS juz, COUNT(*)::int AS cnt
    FROM quran_khatam_juz_marks j
    WHERE j.cycle_id = c.id
) m ON true`

// StartKhatamCycle creates a new active cycle. The partial unique index on
// (user_id) WHERE completed_at IS NULL enforces one active cycle per user.
func (r *PersonalRepo) StartKhatamCycle(
	ctx context.Context,
	cycle entity.QuranKhatamCycle, //nolint:gocritic // value param fixed by the repo interface contract
) (entity.QuranKhatamCycle, error) {
	sqlText := `
INSERT INTO quran_khatam_cycles (id, user_id, started_at, completed_at, notes, created_at, updated_at)
VALUES ($1, $2, now(), NULL, $3, now(), now())
RETURNING id, user_id, started_at, completed_at, notes, created_at, updated_at`

	created := entity.QuranKhatamCycle{CompletedJuz: []int{}}

	var (
		completedAt sql.NullTime
		notes       sql.NullString
	)
	if err := r.Pool.QueryRow(ctx, sqlText, cycle.ID, cycle.UserID, cycle.Notes).Scan(
		&created.ID,
		&created.UserID,
		&created.StartedAt,
		&completedAt,
		&notes,
		&created.CreatedAt,
		&created.UpdatedAt,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return entity.QuranKhatamCycle{}, entity.ErrKhatamCycleActiveExists
		}

		return entity.QuranKhatamCycle{}, fmt.Errorf("PersonalRepo - StartKhatamCycle - Scan: %w", err)
	}

	created.Notes = nullableString(notes)
	if completedAt.Valid {
		created.CompletedAt = &completedAt.Time
	}

	return created, nil
}

// GetActiveKhatamCycle returns the user's uncompleted cycle with juz marks.
func (r *PersonalRepo) GetActiveKhatamCycle(ctx context.Context, userID string) (entity.QuranKhatamCycle, error) {
	sqlText := fmt.Sprintf(`
SELECT %s
FROM quran_khatam_cycles c
%s
WHERE c.user_id = $1 AND c.completed_at IS NULL`, khatamCycleColumns, khatamMarksLateral)

	cycle, err := scanKhatamCycle(r.Pool.QueryRow(ctx, sqlText, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranKhatamCycle{}, entity.ErrKhatamCycleNotFound
		}

		return entity.QuranKhatamCycle{}, fmt.Errorf("PersonalRepo - GetActiveKhatamCycle - scanKhatamCycle: %w", err)
	}

	return cycle, nil
}

// MarkKhatamJuz idempotently marks one juz as completed on the active cycle and
// returns the refreshed cycle state plus whether a NEW mark was actually inserted.
// Sibling CTEs cannot see each other's writes, so the aggregate unions the inserted
// mark explicitly. updated_at only advances when a new mark is inserted (so an
// idempotent re-mark does not churn sync cursors), and `changed` lets the usecase
// skip the milestone notifier on a re-mark.
func (r *PersonalRepo) MarkKhatamJuz(ctx context.Context, userID string, juzNumber int) (entity.QuranKhatamCycle, bool, error) {
	sqlText := `
WITH active AS (
    SELECT id FROM quran_khatam_cycles WHERE user_id = $1 AND completed_at IS NULL
),
mark AS (
    INSERT INTO quran_khatam_juz_marks (cycle_id, juz_number, marked_at)
    SELECT id, $2, now() FROM active
    ON CONFLICT (cycle_id, juz_number) DO NOTHING
    RETURNING juz_number
),
all_marks AS (
    SELECT j.juz_number
    FROM quran_khatam_juz_marks j
    WHERE j.cycle_id = (SELECT id FROM active)
    UNION
    SELECT mark.juz_number FROM mark
),
touched AS (
    UPDATE quran_khatam_cycles c
    SET updated_at = CASE WHEN EXISTS (SELECT 1 FROM mark) THEN now() ELSE c.updated_at END
    WHERE c.id = (SELECT id FROM active)
    RETURNING c.id, c.user_id, c.started_at, c.completed_at, c.notes, c.created_at, c.updated_at
)
SELECT t.id, t.user_id, t.started_at, t.completed_at, t.notes, t.created_at, t.updated_at,
       COALESCE((SELECT array_agg(am.juz_number ORDER BY am.juz_number) FROM all_marks am), ARRAY[]::INTEGER[]) AS completed_juz,
       (SELECT COUNT(*)::int FROM all_marks) AS juz_count,
       EXISTS (SELECT 1 FROM mark) AS changed
FROM touched t`

	cycle, changed, err := scanKhatamCycleRow(r.Pool.QueryRow(ctx, sqlText, userID, juzNumber), true)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranKhatamCycle{}, false, entity.ErrKhatamCycleNotFound
		}

		return entity.QuranKhatamCycle{}, false, fmt.Errorf("PersonalRepo - MarkKhatamJuz - scanKhatamCycle: %w", err)
	}

	return cycle, changed, nil
}

// UnmarkKhatamJuz idempotently removes one juz mark from the active cycle and
// returns the refreshed cycle state plus whether a mark was actually deleted.
// updated_at only advances when a mark is removed (no sync churn on a no-op unmark).
func (r *PersonalRepo) UnmarkKhatamJuz(ctx context.Context, userID string, juzNumber int) (entity.QuranKhatamCycle, bool, error) {
	sqlText := `
WITH active AS (
    SELECT id FROM quran_khatam_cycles WHERE user_id = $1 AND completed_at IS NULL
),
unmark AS (
    DELETE FROM quran_khatam_juz_marks
    WHERE cycle_id = (SELECT id FROM active) AND juz_number = $2
    RETURNING juz_number
),
all_marks AS (
    SELECT j.juz_number
    FROM quran_khatam_juz_marks j
    WHERE j.cycle_id = (SELECT id FROM active) AND j.juz_number <> $2
),
touched AS (
    UPDATE quran_khatam_cycles c
    SET updated_at = CASE WHEN EXISTS (SELECT 1 FROM unmark) THEN now() ELSE c.updated_at END
    WHERE c.id = (SELECT id FROM active)
    RETURNING c.id, c.user_id, c.started_at, c.completed_at, c.notes, c.created_at, c.updated_at
)
SELECT t.id, t.user_id, t.started_at, t.completed_at, t.notes, t.created_at, t.updated_at,
       COALESCE((SELECT array_agg(am.juz_number ORDER BY am.juz_number) FROM all_marks am), ARRAY[]::INTEGER[]) AS completed_juz,
       (SELECT COUNT(*)::int FROM all_marks) AS juz_count,
       EXISTS (SELECT 1 FROM unmark) AS changed
FROM touched t`

	cycle, changed, err := scanKhatamCycleRow(r.Pool.QueryRow(ctx, sqlText, userID, juzNumber), true)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranKhatamCycle{}, false, entity.ErrKhatamCycleNotFound
		}

		return entity.QuranKhatamCycle{}, false, fmt.Errorf("PersonalRepo - UnmarkKhatamJuz - scanKhatamCycle: %w", err)
	}

	return cycle, changed, nil
}

// CompleteKhatamCycle completes the active cycle only when all 30 juz are
// marked. Returns ErrKhatamCycleIncomplete when an active cycle exists but
// marks are missing, ErrKhatamCycleNotFound when there is no active cycle.
func (r *PersonalRepo) CompleteKhatamCycle(ctx context.Context, userID string) (entity.QuranKhatamCycle, error) {
	sqlText := fmt.Sprintf(`
WITH active AS (
    SELECT id FROM quran_khatam_cycles WHERE user_id = $1 AND completed_at IS NULL
),
completed AS (
    UPDATE quran_khatam_cycles c
    SET completed_at = now(), updated_at = now()
    WHERE c.id = (SELECT id FROM active)
      AND (SELECT COUNT(*) FROM quran_khatam_juz_marks j WHERE j.cycle_id = c.id) = %d
    RETURNING c.id, c.user_id, c.started_at, c.completed_at, c.notes, c.created_at, c.updated_at
)
SELECT EXISTS (SELECT 1 FROM active) AS has_active,
       c.id, c.user_id, c.started_at, c.completed_at, c.notes, c.created_at, c.updated_at
FROM (SELECT 1) one
LEFT JOIN completed c ON true`, entity.KhatamJuzTotal)

	var (
		hasActive   bool
		id, uID     sql.NullString
		startedAt   sql.NullTime
		completedAt sql.NullTime
		notes       sql.NullString
		createdAt   sql.NullTime
		updatedAt   sql.NullTime
	)
	if err := r.Pool.QueryRow(ctx, sqlText, userID).Scan(
		&hasActive, &id, &uID, &startedAt, &completedAt, &notes, &createdAt, &updatedAt,
	); err != nil {
		return entity.QuranKhatamCycle{}, fmt.Errorf("PersonalRepo - CompleteKhatamCycle - Scan: %w", err)
	}

	switch {
	case !hasActive:
		return entity.QuranKhatamCycle{}, entity.ErrKhatamCycleNotFound
	case !id.Valid:
		return entity.QuranKhatamCycle{}, entity.ErrKhatamCycleIncomplete
	}

	completedJuz := make([]int, 0, entity.KhatamJuzTotal)
	for juz := 1; juz <= entity.KhatamJuzTotal; juz++ {
		completedJuz = append(completedJuz, juz)
	}

	cycle := entity.QuranKhatamCycle{
		ID:           id.String,
		UserID:       uID.String,
		StartedAt:    startedAt.Time,
		Notes:        nullableString(notes),
		CompletedJuz: completedJuz,
		JuzCount:     entity.KhatamJuzTotal,
		Percent:      percentFull,
		CreatedAt:    createdAt.Time,
		UpdatedAt:    updatedAt.Time,
	}
	if completedAt.Valid {
		cycle.CompletedAt = &completedAt.Time
	}

	return cycle, nil
}

// ListKhatamHistory returns completed cycles ordered by completion recency.
func (r *PersonalRepo) ListKhatamHistory(
	ctx context.Context,
	userID string,
	limit, offset uint64,
) ([]entity.QuranKhatamCycle, int, error) {
	var total int
	if err := r.Pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM quran_khatam_cycles
WHERE user_id = $1 AND completed_at IS NOT NULL`, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListKhatamHistory - count: %w", err)
	}

	sqlText := fmt.Sprintf(`
SELECT %s
FROM quran_khatam_cycles c
%s
WHERE c.user_id = $1 AND c.completed_at IS NOT NULL
ORDER BY c.completed_at DESC, c.id DESC
LIMIT $2 OFFSET $3`, khatamCycleColumns, khatamMarksLateral)

	rows, err := r.Pool.Query(ctx, sqlText, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListKhatamHistory - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	cycles := make([]entity.QuranKhatamCycle, 0, limit)

	for rows.Next() {
		cycle, err := scanKhatamCycle(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("PersonalRepo - ListKhatamHistory - scanKhatamCycle: %w", err)
		}

		cycles = append(cycles, cycle)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("PersonalRepo - ListKhatamHistory - rows.Err: %w", err)
	}

	return cycles, total, nil
}

func scanKhatamCycle(row rowScanner) (entity.QuranKhatamCycle, error) {
	cycle, _, err := scanKhatamCycleRow(row, false)
	return cycle, err
}

// scanKhatamCycleRow scans a cycle row, optionally with a trailing `changed` bool
// that Mark/Unmark append to report whether the write actually altered the mark set
// (so the usecase can skip the milestone notifier on an idempotent re-mark).
func scanKhatamCycleRow(row rowScanner, withChanged bool) (entity.QuranKhatamCycle, bool, error) {
	var (
		cycle        entity.QuranKhatamCycle
		completedAt  sql.NullTime
		notes        sql.NullString
		completedJuz []int32
		changed      bool
	)

	dests := []any{
		&cycle.ID,
		&cycle.UserID,
		&cycle.StartedAt,
		&completedAt,
		&notes,
		&cycle.CreatedAt,
		&cycle.UpdatedAt,
		&completedJuz,
		&cycle.JuzCount,
	}
	if withChanged {
		dests = append(dests, &changed)
	}

	if err := row.Scan(dests...); err != nil {
		return entity.QuranKhatamCycle{}, false, err
	}

	if completedAt.Valid {
		cycle.CompletedAt = &completedAt.Time
	}

	cycle.Notes = nullableString(notes)

	cycle.CompletedJuz = make([]int, 0, len(completedJuz))
	for _, juz := range completedJuz {
		cycle.CompletedJuz = append(cycle.CompletedJuz, int(juz))
	}

	cycle.Percent = math.Round(float64(cycle.JuzCount)/float64(entity.KhatamJuzTotal)*percentScale) / percentFull

	return cycle, changed, nil
}
