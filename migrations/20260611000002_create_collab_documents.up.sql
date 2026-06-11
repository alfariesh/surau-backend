-- Binary Yjs document state for the realtime collaborative editor.
-- Written exclusively by the collab-server (Hocuspocus) via its database
-- extension; the Go app never touches this table. Draft HTML continues to live
-- in book_page_edits, synced from the collab document through the internal API
-- so sanitization, audit and revision history stay on the single Go write path.
CREATE TABLE IF NOT EXISTS collab_documents (
    name TEXT PRIMARY KEY,
    state BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
