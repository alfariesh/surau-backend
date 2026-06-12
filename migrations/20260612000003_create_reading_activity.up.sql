-- Daily reading-activity buckets powering streaks, statistics, and heatmaps.
-- activity_date is the calendar date of the client_observed_at timestamp in
-- its own UTC offset, so a 23:50 local read counts on the local day and
-- offline replays backfill the day the reading actually happened.
CREATE TABLE IF NOT EXISTS reading_activity (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    activity_date DATE NOT NULL,
    quran_ayahs_read INTEGER NOT NULL DEFAULT 0,
    kitab_pages_read INTEGER NOT NULL DEFAULT 0,
    quran_events INTEGER NOT NULL DEFAULT 0,
    kitab_events INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, activity_date),
    CONSTRAINT reading_activity_counters_check
        CHECK (
            quran_ayahs_read >= 0
            AND kitab_pages_read >= 0
            AND quran_events >= 0
            AND kitab_events >= 0
        )
);
