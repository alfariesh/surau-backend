package persistent

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
)

const activityDateLayout = "2006-01-02"

// GetReadingStreak computes consecutive-day streaks from the daily activity
// buckets. The current streak counts the run ending today or yesterday
// (relative to the client-supplied local date), so a streak is not broken
// before the user had a chance to read today.
func (r *PersonalRepo) GetReadingStreak(ctx context.Context, userID, today string) (entity.ReadingStreak, error) {
	sqlText := `
WITH user_days AS (
    SELECT activity_date FROM reading_activity WHERE user_id = $1
),
runs AS (
    SELECT activity_date,
           activity_date - (ROW_NUMBER() OVER (ORDER BY activity_date))::int AS grp
    FROM user_days
),
islands AS (
    SELECT MAX(activity_date) AS end_date, COUNT(*)::int AS len
    FROM runs
    GROUP BY grp
),
anchor AS (
    SELECT MAX(activity_date) AS d FROM user_days WHERE activity_date <= $2::date
)
SELECT
    COALESCE((
        SELECT i.len
        FROM islands i, anchor a
        WHERE a.d IS NOT NULL AND a.d >= $2::date - 1 AND i.end_date = a.d
    ), 0) AS current_streak,
    COALESCE((SELECT MAX(len) FROM islands), 0) AS longest_streak,
    (SELECT COUNT(*)::int FROM user_days) AS total_active_days,
    (SELECT MAX(activity_date) FROM user_days) AS last_active_date,
    EXISTS (SELECT 1 FROM user_days WHERE activity_date = $2::date) AS active_today`

	streak := entity.ReadingStreak{Today: today}

	var lastActive sql.NullTime
	if err := r.Pool.QueryRow(ctx, sqlText, userID, today).Scan(
		&streak.CurrentStreakDays,
		&streak.LongestStreakDays,
		&streak.TotalActiveDays,
		&lastActive,
		&streak.ActiveToday,
	); err != nil {
		return entity.ReadingStreak{}, fmt.Errorf("PersonalRepo - GetReadingStreak - Scan: %w", err)
	}

	if lastActive.Valid {
		formatted := lastActive.Time.Format(activityDateLayout)
		streak.LastActiveDate = &formatted
	}

	return streak, nil
}

// ReminderCandidates lists users to nudge with a streak reminder: those who read yesterday but not
// yet today (so their streak is at risk) and whose local time is in the evening send window, gated
// by the streak-reminder preference. All date/time logic is resolved in each user's IANA timezone
// (NULL/empty falls back to UTC); the local date is returned so the caller can scope a per-day
// send cooldown. Bounded to avoid an unbounded sweep.
func (r *PersonalRepo) ReminderCandidates(ctx context.Context) ([]entity.ReminderCandidate, error) {
	const sqlText = `
WITH eligible AS (
    SELECT
        u.id AS user_id,
        COALESCE(pref.preferred_ui_lang, 'id') AS lang,
        COALESCE(pref.notify_streak_reminders, TRUE) AS streak_enabled,
        (now() AT TIME ZONE COALESCE(NULLIF(p.timezone, ''), 'UTC')) AS local_ts
    FROM users u
    JOIN user_preferences pref ON pref.user_id = u.id
    LEFT JOIN user_profiles p ON p.user_id = u.id
    WHERE u.deleted_at IS NULL
)
SELECT user_id, lang, to_char(local_ts::date, 'YYYY-MM-DD') AS local_date
FROM eligible
WHERE streak_enabled
  AND EXTRACT(HOUR FROM local_ts) BETWEEN 19 AND 20
  AND EXISTS (
      SELECT 1 FROM reading_activity ra
      WHERE ra.user_id = eligible.user_id AND ra.activity_date = local_ts::date - 1
  )
  AND NOT EXISTS (
      SELECT 1 FROM reading_activity ra
      WHERE ra.user_id = eligible.user_id AND ra.activity_date = local_ts::date
  )
LIMIT 5000`

	rows, err := r.Pool.Query(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - ReminderCandidates - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	var candidates []entity.ReminderCandidate
	for rows.Next() {
		var candidate entity.ReminderCandidate
		if err := rows.Scan(&candidate.UserID, &candidate.Lang, &candidate.LocalDate); err != nil {
			return nil, fmt.Errorf("PersonalRepo - ReminderCandidates - rows.Scan: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - ReminderCandidates - rows.Err: %w", err)
	}

	return candidates, nil
}

// GetReadingActivity returns daily activity buckets in [from, to] plus the
// aggregated summary computed from the same rows.
func (r *PersonalRepo) GetReadingActivity(
	ctx context.Context,
	userID, from, to string,
) (entity.ReadingActivitySummary, error) {
	sqlText := `
SELECT activity_date, quran_ayahs_read, kitab_pages_read, quran_events, kitab_events
FROM reading_activity
WHERE user_id = $1 AND activity_date BETWEEN $2::date AND $3::date
ORDER BY activity_date ASC`

	rows, err := r.Pool.Query(ctx, sqlText, userID, from, to)
	if err != nil {
		return entity.ReadingActivitySummary{}, fmt.Errorf("PersonalRepo - GetReadingActivity - r.Pool.Query: %w", err)
	}
	defer rows.Close()

	summary := entity.ReadingActivitySummary{
		From: from,
		To:   to,
		Days: []entity.ReadingActivityDay{},
	}

	for rows.Next() {
		var (
			day  entity.ReadingActivityDay
			date time.Time
		)
		if err := rows.Scan(
			&date,
			&day.QuranAyahsRead,
			&day.KitabPagesRead,
			&day.QuranEvents,
			&day.KitabEvents,
		); err != nil {
			return entity.ReadingActivitySummary{}, fmt.Errorf("PersonalRepo - GetReadingActivity - rows.Scan: %w", err)
		}

		day.Date = date.Format(activityDateLayout)
		summary.Days = append(summary.Days, day)
		summary.ActiveDays++
		summary.QuranAyahsRead += day.QuranAyahsRead

		summary.KitabPagesRead += day.KitabPagesRead
		if day.QuranEvents > 0 {
			summary.QuranActiveDays++
		}

		if day.KitabEvents > 0 {
			summary.KitabActiveDays++
		}
	}

	if err := rows.Err(); err != nil {
		return entity.ReadingActivitySummary{}, fmt.Errorf("PersonalRepo - GetReadingActivity - rows.Err: %w", err)
	}

	return summary, nil
}
