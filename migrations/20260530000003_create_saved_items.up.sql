CREATE TABLE IF NOT EXISTS saved_items (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_type TEXT NOT NULL,
    book_id INTEGER,
    page_id INTEGER,
    heading_id INTEGER,
    surah_id INTEGER,
    ayah_key TEXT,
    from_ayah_number INTEGER,
    to_ayah_number INTEGER,
    label TEXT,
    note TEXT,
    tags TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT saved_items_type_check
        CHECK (item_type IN ('book_page', 'book_heading', 'quran_ayah', 'quran_range')),
    CONSTRAINT saved_items_label_check
        CHECK (label IS NULL OR char_length(label) <= 255),
    CONSTRAINT saved_items_note_check
        CHECK (note IS NULL OR char_length(note) <= 2000),
    CONSTRAINT saved_items_tags_count_check
        CHECK (cardinality(tags) <= 20),
    CONSTRAINT saved_items_tags_nonempty_check
        CHECK (array_position(tags, '') IS NULL),
    CONSTRAINT saved_items_target_check
        CHECK (
            (
                item_type = 'book_page'
                AND book_id IS NOT NULL
                AND page_id IS NOT NULL
                AND heading_id IS NULL
                AND surah_id IS NULL
                AND ayah_key IS NULL
                AND from_ayah_number IS NULL
                AND to_ayah_number IS NULL
            )
            OR
            (
                item_type = 'book_heading'
                AND book_id IS NOT NULL
                AND page_id IS NULL
                AND heading_id IS NOT NULL
                AND surah_id IS NULL
                AND ayah_key IS NULL
                AND from_ayah_number IS NULL
                AND to_ayah_number IS NULL
            )
            OR
            (
                item_type = 'quran_ayah'
                AND book_id IS NULL
                AND page_id IS NULL
                AND heading_id IS NULL
                AND surah_id IS NOT NULL
                AND ayah_key IS NOT NULL
                AND from_ayah_number IS NULL
                AND to_ayah_number IS NULL
            )
            OR
            (
                item_type = 'quran_range'
                AND book_id IS NULL
                AND page_id IS NULL
                AND heading_id IS NULL
                AND surah_id IS NOT NULL
                AND ayah_key IS NULL
                AND from_ayah_number IS NOT NULL
                AND to_ayah_number IS NOT NULL
                AND to_ayah_number >= from_ayah_number
            )
        )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_saved_items_user_book_page
    ON saved_items(user_id, item_type, book_id, page_id)
    WHERE item_type = 'book_page';

CREATE UNIQUE INDEX IF NOT EXISTS idx_saved_items_user_book_heading
    ON saved_items(user_id, item_type, book_id, heading_id)
    WHERE item_type = 'book_heading';

CREATE UNIQUE INDEX IF NOT EXISTS idx_saved_items_user_quran_ayah
    ON saved_items(user_id, item_type, ayah_key)
    WHERE item_type = 'quran_ayah';

CREATE UNIQUE INDEX IF NOT EXISTS idx_saved_items_user_quran_range
    ON saved_items(user_id, item_type, surah_id, from_ayah_number, to_ayah_number)
    WHERE item_type = 'quran_range';

CREATE INDEX IF NOT EXISTS idx_saved_items_user_updated ON saved_items(user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_saved_items_user_type ON saved_items(user_id, item_type);
CREATE INDEX IF NOT EXISTS idx_saved_items_user_book ON saved_items(user_id, book_id) WHERE book_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_saved_items_user_surah ON saved_items(user_id, surah_id) WHERE surah_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_saved_items_tags ON saved_items USING gin (tags);

DROP TABLE IF EXISTS bookmarks;
