ALTER TABLE users
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user';

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_role_check;

ALTER TABLE users
    ADD CONSTRAINT users_role_check CHECK (role IN ('user', 'admin'));

CREATE TABLE IF NOT EXISTS book_publications (
    book_id INTEGER PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'hidden' CHECK (status IN ('hidden', 'draft', 'published', 'archived')),
    featured BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order INTEGER,
    published_at TIMESTAMPTZ,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS book_collections (
    slug TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS book_collection_items (
    collection_slug TEXT NOT NULL REFERENCES book_collections(slug) ON DELETE CASCADE,
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    sort_order INTEGER,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_slug, book_id)
);

CREATE TABLE IF NOT EXISTS book_metadata_edits (
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('draft', 'published')),
    display_title TEXT,
    description TEXT,
    cover_url TEXT,
    category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    notes TEXT,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    PRIMARY KEY (book_id, status)
);

CREATE TABLE IF NOT EXISTS book_page_edits (
    book_id INTEGER NOT NULL,
    page_id INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'published')),
    content_html TEXT NOT NULL,
    content_text TEXT NOT NULL,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    PRIMARY KEY (book_id, page_id, status),
    FOREIGN KEY (book_id, page_id) REFERENCES book_pages(book_id, page_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS book_heading_edits (
    book_id INTEGER NOT NULL,
    heading_id INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'published')),
    content TEXT NOT NULL,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    PRIMARY KEY (book_id, heading_id, status),
    FOREIGN KEY (book_id, heading_id) REFERENCES book_headings(book_id, heading_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS admin_audit_logs (
    id UUID PRIMARY KEY,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    book_id INTEGER REFERENCES books(id) ON DELETE SET NULL,
    page_id INTEGER,
    heading_id INTEGER,
    collection_slug TEXT,
    payload JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO book_collections (slug, title, description)
VALUES ('starter-50', 'Starter 50', 'First curated Surau reader collection')
ON CONFLICT (slug) DO NOTHING;

CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_book_publications_status ON book_publications(status);
CREATE INDEX IF NOT EXISTS idx_book_publications_featured ON book_publications(featured);
CREATE INDEX IF NOT EXISTS idx_book_collection_items_book ON book_collection_items(book_id);
CREATE INDEX IF NOT EXISTS idx_book_metadata_edits_status ON book_metadata_edits(status);
CREATE INDEX IF NOT EXISTS idx_book_page_edits_status ON book_page_edits(status);
CREATE INDEX IF NOT EXISTS idx_book_heading_edits_status ON book_heading_edits(status);
CREATE INDEX IF NOT EXISTS idx_admin_audit_logs_actor ON admin_audit_logs(actor_id);
CREATE INDEX IF NOT EXISTS idx_admin_audit_logs_book ON admin_audit_logs(book_id);
