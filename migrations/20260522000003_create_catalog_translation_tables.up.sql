CREATE TABLE IF NOT EXISTS category_translations (
    category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    lang TEXT NOT NULL,
    name TEXT NOT NULL,
    source TEXT,
    metadata JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (category_id, lang)
);

CREATE TABLE IF NOT EXISTS author_translations (
    author_id INTEGER NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    lang TEXT NOT NULL,
    name TEXT NOT NULL,
    biography TEXT,
    death_text TEXT,
    source TEXT,
    metadata JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (author_id, lang)
);

CREATE TABLE IF NOT EXISTS book_metadata_translations (
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    lang TEXT NOT NULL,
    display_title TEXT NOT NULL,
    bibliography TEXT,
    hint TEXT,
    description TEXT,
    source TEXT,
    metadata JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, lang)
);

CREATE INDEX IF NOT EXISTS idx_category_translations_lang ON category_translations(lang);
CREATE INDEX IF NOT EXISTS idx_author_translations_lang ON author_translations(lang);
CREATE INDEX IF NOT EXISTS idx_author_translations_name_trgm ON author_translations USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_book_metadata_translations_lang ON book_metadata_translations(lang);
CREATE INDEX IF NOT EXISTS idx_book_metadata_translations_title_trgm ON book_metadata_translations USING gin (display_title gin_trgm_ops);
