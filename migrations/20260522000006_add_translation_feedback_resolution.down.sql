DROP INDEX IF EXISTS idx_translation_feedbacks_status_lookup;

ALTER TABLE translation_feedbacks
    DROP CONSTRAINT IF EXISTS translation_feedbacks_resolution_note_check;
ALTER TABLE translation_feedbacks
    DROP CONSTRAINT IF EXISTS translation_feedbacks_resolved_by_check;
ALTER TABLE translation_feedbacks
    DROP CONSTRAINT IF EXISTS translation_feedbacks_status_check;

ALTER TABLE translation_feedbacks
    DROP COLUMN IF EXISTS resolution_note,
    DROP COLUMN IF EXISTS resolved_at,
    DROP COLUMN IF EXISTS resolved_by,
    DROP COLUMN IF EXISTS status;
