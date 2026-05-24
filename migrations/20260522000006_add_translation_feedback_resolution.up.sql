ALTER TABLE translation_feedbacks
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'open',
    ADD COLUMN IF NOT EXISTS resolved_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS resolution_note TEXT;

ALTER TABLE translation_feedbacks
    DROP CONSTRAINT IF EXISTS translation_feedbacks_status_check;
ALTER TABLE translation_feedbacks
    DROP CONSTRAINT IF EXISTS translation_feedbacks_resolution_note_check;

ALTER TABLE translation_feedbacks
    ADD CONSTRAINT translation_feedbacks_status_check
    CHECK (status IN ('open', 'resolved'));
ALTER TABLE translation_feedbacks
    ADD CONSTRAINT translation_feedbacks_resolution_note_check
    CHECK (resolution_note IS NULL OR char_length(resolution_note) <= 2000);

CREATE INDEX IF NOT EXISTS idx_translation_feedbacks_status_lookup
    ON translation_feedbacks (status, book_id, heading_id, lang, updated_at DESC);
