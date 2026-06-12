ALTER TABLE reading_progress DROP CONSTRAINT IF EXISTS reading_progress_progress_percent_check;
ALTER TABLE reading_progress DROP COLUMN IF EXISTS observed_at;
