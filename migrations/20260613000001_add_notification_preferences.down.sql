ALTER TABLE user_preferences
    DROP COLUMN IF EXISTS notify_daily_reminders,
    DROP COLUMN IF EXISTS notify_streak_reminders,
    DROP COLUMN IF EXISTS notify_khatam_milestones;
