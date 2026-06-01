CREATE TABLE IF NOT EXISTS book_production_draft_revisions (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES book_production_projects(id) ON DELETE CASCADE,
    asset_type TEXT NOT NULL CHECK (
        asset_type IN (
            'book_metadata',
            'author_metadata',
            'category_metadata',
            'section_translation',
            'heading_summary',
            'section_audio'
        )
    ),
    heading_id INTEGER,
    version INTEGER NOT NULL CHECK (version > 0),
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    snapshot JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT book_production_draft_revisions_heading_scope_check CHECK (
        (
            asset_type IN ('section_translation', 'heading_summary', 'section_audio')
            AND heading_id IS NOT NULL
        )
        OR (
            asset_type IN ('book_metadata', 'author_metadata', 'category_metadata')
            AND heading_id IS NULL
        )
    )
);

CREATE INDEX IF NOT EXISTS idx_book_production_draft_revisions_project_asset
    ON book_production_draft_revisions(project_id, asset_type, heading_id, version DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_book_production_draft_revisions_unique_version
    ON book_production_draft_revisions(project_id, asset_type, COALESCE(heading_id, 0), version);
