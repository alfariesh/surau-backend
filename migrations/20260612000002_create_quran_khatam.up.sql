CREATE TABLE IF NOT EXISTS quran_khatam_cycles (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT quran_khatam_cycles_notes_check
        CHECK (notes IS NULL OR char_length(notes) <= 2000),
    CONSTRAINT quran_khatam_cycles_completed_after_start_check
        CHECK (completed_at IS NULL OR completed_at >= started_at)
);

-- Exactly one active (uncompleted) cycle per user.
CREATE UNIQUE INDEX IF NOT EXISTS idx_quran_khatam_cycles_one_active
    ON quran_khatam_cycles(user_id) WHERE completed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_quran_khatam_cycles_user_completed
    ON quran_khatam_cycles(user_id, completed_at DESC) WHERE completed_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS quran_khatam_juz_marks (
    cycle_id UUID NOT NULL REFERENCES quran_khatam_cycles(id) ON DELETE CASCADE,
    juz_number INTEGER NOT NULL,
    marked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cycle_id, juz_number),
    CONSTRAINT quran_khatam_juz_marks_number_check
        CHECK (juz_number >= 1 AND juz_number <= 30)
);
