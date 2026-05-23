CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS categories (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    display_order INTEGER,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS authors (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    biography TEXT,
    death_text TEXT,
    death_number INTEGER,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS books (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    category_id INTEGER REFERENCES categories(id),
    author_id INTEGER REFERENCES authors(id),
    type INTEGER,
    printed INTEGER,
    minor_release INTEGER,
    major_release INTEGER,
    bibliography TEXT,
    hint TEXT,
    pdf_links JSONB,
    metadata JSONB,
    source_date TEXT,
    has_content BOOLEAN NOT NULL DEFAULT FALSE,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS book_pages (
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    page_id INTEGER NOT NULL,
    part TEXT,
    printed_page TEXT,
    number TEXT,
    content_html TEXT NOT NULL,
    content_text TEXT NOT NULL,
    services JSONB,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, page_id)
);

CREATE TABLE IF NOT EXISTS book_headings (
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    heading_id INTEGER NOT NULL,
    parent_id INTEGER,
    page_id INTEGER NOT NULL,
    depth INTEGER NOT NULL DEFAULT 0,
    ordinal INTEGER NOT NULL,
    content TEXT NOT NULL,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, heading_id),
    FOREIGN KEY (book_id, page_id) REFERENCES book_pages(book_id, page_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS book_heading_ranges (
    book_id INTEGER NOT NULL,
    heading_id INTEGER NOT NULL,
    start_page_id INTEGER NOT NULL,
    end_page_id INTEGER NOT NULL,
    start_anchor TEXT,
    end_anchor TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, heading_id),
    FOREIGN KEY (book_id, heading_id) REFERENCES book_headings(book_id, heading_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS section_translations (
    book_id INTEGER NOT NULL,
    heading_id INTEGER NOT NULL,
    lang TEXT NOT NULL,
    title TEXT,
    content TEXT NOT NULL,
    source TEXT,
    metadata JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, heading_id, lang),
    FOREIGN KEY (book_id, heading_id) REFERENCES book_headings(book_id, heading_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS section_audio (
    book_id INTEGER NOT NULL,
    heading_id INTEGER NOT NULL,
    lang TEXT NOT NULL,
    url TEXT NOT NULL,
    narrator TEXT,
    duration_seconds INTEGER,
    mime_type TEXT,
    metadata JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, heading_id, lang),
    FOREIGN KEY (book_id, heading_id) REFERENCES book_headings(book_id, heading_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS reading_progress (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    page_id INTEGER,
    heading_id INTEGER,
    progress_percent NUMERIC(5,2),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, book_id)
);

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

CREATE TABLE IF NOT EXISTS source_releases (
    release_key TEXT PRIMARY KEY,
    source_dir TEXT NOT NULL,
    master_checksum TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS import_runs (
    id UUID PRIMARY KEY,
    release_key TEXT NOT NULL REFERENCES source_releases(release_key),
    mode TEXT NOT NULL,
    source_dir TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    master_checksum TEXT NOT NULL,
    total_books INTEGER NOT NULL DEFAULT 0,
    imported_books INTEGER NOT NULL DEFAULT 0,
    imported_pages INTEGER NOT NULL DEFAULT 0,
    imported_headings INTEGER NOT NULL DEFAULT 0,
    skipped_files INTEGER NOT NULL DEFAULT 0,
    errors JSONB
);

CREATE INDEX IF NOT EXISTS idx_books_category_id ON books(category_id);
CREATE INDEX IF NOT EXISTS idx_books_author_id ON books(author_id);
CREATE INDEX IF NOT EXISTS idx_books_has_content ON books(has_content);
CREATE INDEX IF NOT EXISTS idx_books_name_trgm ON books USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_authors_name_trgm ON authors USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_book_pages_book_page ON book_pages(book_id, page_id);
CREATE INDEX IF NOT EXISTS idx_book_headings_book_page ON book_headings(book_id, page_id);
CREATE INDEX IF NOT EXISTS idx_book_headings_content_trgm ON book_headings USING gin (content gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_bookmarks_user_book ON bookmarks(user_id, book_id);
