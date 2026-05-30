CREATE TABLE IF NOT EXISTS quran_reading_progress (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    surah_id INTEGER NOT NULL REFERENCES quran_surahs(surah_id) ON DELETE CASCADE,
    ayah_number INTEGER NOT NULL,
    ayah_key TEXT NOT NULL,
    position_percent NUMERIC(5,2) NOT NULL DEFAULT 0,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, surah_id),
    FOREIGN KEY (surah_id, ayah_number) REFERENCES quran_ayahs(surah_id, ayah_number) ON DELETE CASCADE,
    CONSTRAINT quran_reading_progress_ayah_key_check
        CHECK (ayah_key = surah_id::text || ':' || ayah_number::text),
    CONSTRAINT quran_reading_progress_position_percent_check
        CHECK (position_percent >= 0 AND position_percent <= 100)
);

CREATE INDEX IF NOT EXISTS idx_quran_reading_progress_user_observed
    ON quran_reading_progress(user_id, observed_at DESC);
