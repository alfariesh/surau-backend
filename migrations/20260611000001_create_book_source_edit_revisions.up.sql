-- Revision history for source kitab edits (pages, headings, metadata).
-- Mirrors book_production_draft_revisions: immutable snapshots, monotonically
-- increasing version per edited resource, pruned to a bounded tail so
-- autosave-heavy editing cannot grow the table without limit.
CREATE TABLE IF NOT EXISTS book_source_edit_revisions (
    id UUID PRIMARY KEY,
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    asset_type TEXT NOT NULL CHECK (asset_type IN ('page', 'heading', 'metadata')),
    page_id INTEGER,
    heading_id INTEGER,
    version INTEGER NOT NULL CHECK (version > 0),
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    origin TEXT NOT NULL DEFAULT 'rest' CHECK (origin IN ('rest', 'collab', 'restore')),
    snapshot JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT book_source_edit_revisions_scope_check CHECK (
        (asset_type = 'page' AND page_id IS NOT NULL AND heading_id IS NULL) OR
        (asset_type = 'heading' AND heading_id IS NOT NULL AND page_id IS NULL) OR
        (asset_type = 'metadata' AND page_id IS NULL AND heading_id IS NULL)
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_book_source_edit_revisions_version
    ON book_source_edit_revisions (book_id, asset_type, COALESCE(page_id, 0), COALESCE(heading_id, 0), version);

CREATE INDEX IF NOT EXISTS idx_book_source_edit_revisions_lookup
    ON book_source_edit_revisions (book_id, asset_type, page_id, heading_id, version DESC);
