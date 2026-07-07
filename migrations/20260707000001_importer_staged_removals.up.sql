-- E4 (K-0/D1): the book importer stops hard-deleting. Disappearing rows become
-- soft tombstones (is_deleted already exists on book_pages/book_headings and
-- every read path filters it); this adds the audit half of the house pattern
-- and the staging table that holds removals awaiting operator approval.

ALTER TABLE book_pages
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delete_reason TEXT;

ALTER TABLE book_headings
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delete_reason TEXT;

-- Removals recorded by a stage-mode run, keyed by (run, book). Applied (or
-- superseded) stages are kept for audit; they cascade away with their run.
CREATE TABLE IF NOT EXISTS book_import_removal_stages (
    run_id UUID NOT NULL REFERENCES import_runs(id) ON DELETE CASCADE,
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    page_ids INTEGER[] NOT NULL DEFAULT '{}',
    heading_ids INTEGER[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, book_id)
);

ALTER TABLE import_runs
    ADD COLUMN IF NOT EXISTS staged_removal_pages INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS staged_removal_headings INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tombstoned_pages INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tombstoned_headings INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS approved_stage_run UUID REFERENCES import_runs(id) ON DELETE SET NULL;
