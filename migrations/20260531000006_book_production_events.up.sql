CREATE TABLE IF NOT EXISTS book_production_events (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES book_production_projects(id) ON DELETE CASCADE,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    asset_type TEXT,
    heading_id INTEGER,
    note TEXT,
    payload JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_book_production_events_project_created
    ON book_production_events(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_book_production_events_actor_created
    ON book_production_events(actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_book_production_events_type_created
    ON book_production_events(event_type, created_at DESC);
