package persistent

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
)

const activityDateLayout = "2006-01-02"

const reminderCandidatesSQL = `
WITH profiles AS (
    SELECT
        u.id AS user_id,
        COALESCE(pref.preferred_ui_lang, 'id') AS lang,
        tz.name AS timezone
    FROM users u
    JOIN user_preferences pref ON pref.user_id = u.id
    JOIN user_profiles p ON p.user_id = u.id
    JOIN pg_timezone_names tz ON tz.name = btrim(p.timezone)
    WHERE u.deleted_at IS NULL
      AND COALESCE(pref.notify_streak_reminders, TRUE)
), eligible AS (
    SELECT
        user_id,
        lang,
        timezone,
        timezone(timezone, $1::timestamptz) AS local_ts
    FROM profiles
)
SELECT
    user_id,
    lang,
    timezone,
    to_char(local_ts::date, 'YYYY-MM-DD') AS local_date,
    timezone(
        timezone,
        local_ts::date + CASE
            WHEN $2::time > local_ts::time AND $2::time < TIME '21:00' THEN $2::time
            ELSE TIME '21:00'
        END
    ) AS delivery_deadline_at
FROM eligible
WHERE local_ts::time >= TIME '19:00'
  AND local_ts::time < TIME '21:00'
  AND NOT CASE
      WHEN $2::time < $3::time
          THEN local_ts::time >= $2::time AND local_ts::time < $3::time
      ELSE local_ts::time >= $2::time OR local_ts::time < $3::time
  END
  AND EXISTS (
      SELECT 1 FROM reading_activity ra
      WHERE ra.user_id = eligible.user_id AND ra.activity_date = local_ts::date - 1
  )
  AND NOT EXISTS (
      SELECT 1 FROM reading_activity ra
      WHERE ra.user_id = eligible.user_id AND ra.activity_date = local_ts::date
  )
  AND NOT EXISTS (
      SELECT 1
      FROM auth_notification_cooldowns cooldown
      WHERE cooldown.event = 'streak_reminder'
        AND cooldown.expires_at > $1
        AND cooldown.key_hash IN (
            encode(sha256(
                convert_to('streak_reminder', 'UTF8') || '\x00'::bytea ||
                convert_to(eligible.user_id::text, 'UTF8') || '\x00'::bytea
            ), 'hex'),
            encode(sha256(
                convert_to('streak_reminder', 'UTF8') || '\x00'::bytea ||
                convert_to(eligible.user_id::text, 'UTF8') || '\x00'::bytea ||
                convert_to(local_ts::date::text, 'UTF8') || '\x00'::bytea
            ), 'hex')
        )
  )
  AND NOT EXISTS (
      SELECT 1
      FROM notification_deliveries nd
      WHERE nd.user_id = eligible.user_id
        AND nd.notification_type = 'streak_reminder'
        AND nd.local_date = local_ts::date
  )
ORDER BY user_id
LIMIT 5000`

const reminderTimezoneSkipsSQL = `
SELECT
    count(*) FILTER (WHERE NULLIF(btrim(p.timezone), '') IS NULL),
    count(*) FILTER (
        WHERE NULLIF(btrim(p.timezone), '') IS NOT NULL AND tz.name IS NULL
    )
FROM users u
JOIN user_preferences pref ON pref.user_id = u.id
LEFT JOIN user_profiles p ON p.user_id = u.id
LEFT JOIN pg_timezone_names tz ON tz.name = btrim(p.timezone)
WHERE u.deleted_at IS NULL
  AND COALESCE(pref.notify_streak_reminders, TRUE)`

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
// by the streak-reminder preference. PostgreSQL owns IANA timezone and DST resolution; missing or
// invalid timezones fail closed per user instead of falling back to UTC or failing the whole sweep.
func (r *PersonalRepo) ReminderCandidates(
	ctx context.Context,
	asOf time.Time,
	quietStart,
	quietEnd string,
) (entity.ReminderCandidatesResult, error) {
	candidates, err := r.listReminderCandidates(ctx, asOf, quietStart, quietEnd)
	if err != nil {
		return entity.ReminderCandidatesResult{}, err
	}

	missing, invalid, err := r.reminderTimezoneSkipCounts(ctx)
	if err != nil {
		return entity.ReminderCandidatesResult{}, err
	}

	return entity.ReminderCandidatesResult{
		Candidates:             candidates,
		MissingTimezoneSkipped: missing,
		InvalidTimezoneSkipped: invalid,
	}, nil
}

func (r *PersonalRepo) listReminderCandidates(
	ctx context.Context,
	asOf time.Time,
	quietStart,
	quietEnd string,
) ([]entity.ReminderCandidate, error) {
	rows, err := r.Pool.Query(ctx, reminderCandidatesSQL, asOf.UTC(), quietStart, quietEnd)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - ReminderCandidates - Query: %w", err)
	}
	defer rows.Close()

	var candidates []entity.ReminderCandidate
	for rows.Next() {
		var candidate entity.ReminderCandidate
		if err := rows.Scan(
			&candidate.UserID,
			&candidate.Lang,
			&candidate.Timezone,
			&candidate.LocalDate,
			&candidate.DeliveryDeadlineAt,
		); err != nil {
			return nil, fmt.Errorf("PersonalRepo - ReminderCandidates - Scan: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - ReminderCandidates - rows: %w", err)
	}

	return candidates, nil
}

func (r *PersonalRepo) reminderTimezoneSkipCounts(ctx context.Context) (missing, invalid int64, err error) {
	if err := r.Pool.QueryRow(ctx, reminderTimezoneSkipsSQL).Scan(&missing, &invalid); err != nil {
		return 0, 0, fmt.Errorf("PersonalRepo - ReminderCandidates - skipped: %w", err)
	}

	return missing, invalid, nil
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
