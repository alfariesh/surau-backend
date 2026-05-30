CREATE TABLE IF NOT EXISTS bookmarks (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    page_id INTEGER,
    heading_id INTEGER,
    label TEXT,
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_bookmarks_user_book ON bookmarks(user_id, book_id);

DROP TABLE IF EXISTS saved_items;
