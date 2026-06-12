-- Bring kitab reading_progress to parity with quran_reading_progress:
-- observed_at enables monotonic upserts (out-of-order autosaves cannot
-- regress progress), and the CHECK mirrors quran_reading_progress.
ALTER TABLE reading_progress
    ADD COLUMN IF NOT EXISTS observed_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Preserve relative ordering for pre-existing rows (dev data only).
UPDATE reading_progress SET observed_at = updated_at;

ALTER TABLE reading_progress
    ADD CONSTRAINT reading_progress_progress_percent_check
        CHECK (progress_percent IS NULL OR (progress_percent >= 0 AND progress_percent <= 100));
