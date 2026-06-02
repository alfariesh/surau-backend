CREATE TABLE IF NOT EXISTS book_production_projects (
    id UUID PRIMARY KEY,
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    lang TEXT NOT NULL CHECK (lang IN ('id', 'en')),
    workflow_status TEXT NOT NULL DEFAULT 'candidate'
        CHECK (workflow_status IN ('candidate', 'drafting', 'in_review', 'ready', 'published', 'archived')),
    publication_status TEXT NOT NULL DEFAULT 'hidden'
        CHECK (publication_status IN ('hidden', 'published', 'archived')),
    requires_review BOOLEAN NOT NULL DEFAULT TRUE,
    requires_audio BOOLEAN NOT NULL DEFAULT FALSE,
    priority INTEGER NOT NULL DEFAULT 0,
    owner_id UUID REFERENCES users(id) ON DELETE SET NULL,
    notes TEXT,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    published_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    archived_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_book_production_projects_active_book_lang
    ON book_production_projects(book_id, lang)
    WHERE workflow_status <> 'archived';
CREATE INDEX IF NOT EXISTS idx_book_production_projects_workflow
    ON book_production_projects(workflow_status, publication_status, priority DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_book_production_projects_owner
    ON book_production_projects(owner_id);

CREATE TABLE IF NOT EXISTS book_metadata_translation_edits (
    project_id UUID PRIMARY KEY REFERENCES book_production_projects(id) ON DELETE CASCADE,
    display_title TEXT NOT NULL,
    bibliography TEXT,
    hint TEXT,
    description TEXT,
    source TEXT,
    metadata JSONB,
    review_status TEXT NOT NULL DEFAULT 'draft'
        CHECK (review_status IN ('draft', 'pending_review', 'approved', 'rejected')),
    review_note TEXT,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS author_translation_edits (
    project_id UUID PRIMARY KEY REFERENCES book_production_projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    biography TEXT,
    death_text TEXT,
    source TEXT,
    metadata JSONB,
    review_status TEXT NOT NULL DEFAULT 'draft'
        CHECK (review_status IN ('draft', 'pending_review', 'approved', 'rejected')),
    review_note TEXT,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS category_translation_edits (
    project_id UUID PRIMARY KEY REFERENCES book_production_projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    source TEXT,
    metadata JSONB,
    review_status TEXT NOT NULL DEFAULT 'draft'
        CHECK (review_status IN ('draft', 'pending_review', 'approved', 'rejected')),
    review_note TEXT,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS section_translation_edits (
    project_id UUID NOT NULL REFERENCES book_production_projects(id) ON DELETE CASCADE,
    heading_id INTEGER NOT NULL,
    title TEXT,
    content TEXT NOT NULL,
    source TEXT,
    metadata JSONB,
    review_status TEXT NOT NULL DEFAULT 'draft'
        CHECK (review_status IN ('draft', 'pending_review', 'approved', 'rejected')),
    review_note TEXT,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ,
    PRIMARY KEY (project_id, heading_id)
);

CREATE TABLE IF NOT EXISTS heading_summary_edits (
    project_id UUID NOT NULL REFERENCES book_production_projects(id) ON DELETE CASCADE,
    heading_id INTEGER NOT NULL,
    summary TEXT NOT NULL,
    source TEXT,
    metadata JSONB,
    review_status TEXT NOT NULL DEFAULT 'draft'
        CHECK (review_status IN ('draft', 'pending_review', 'approved', 'rejected')),
    review_note TEXT,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ,
    PRIMARY KEY (project_id, heading_id)
);

CREATE TABLE IF NOT EXISTS section_audio_edits (
    project_id UUID NOT NULL REFERENCES book_production_projects(id) ON DELETE CASCADE,
    heading_id INTEGER NOT NULL,
    url TEXT NOT NULL,
    narrator TEXT,
    duration_seconds INTEGER,
    mime_type TEXT,
    metadata JSONB,
    review_status TEXT NOT NULL DEFAULT 'draft'
        CHECK (review_status IN ('draft', 'pending_review', 'approved', 'rejected')),
    review_note TEXT,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ,
    PRIMARY KEY (project_id, heading_id),
    CONSTRAINT section_audio_edits_duration_check
        CHECK (duration_seconds IS NULL OR duration_seconds >= 0)
);

CREATE INDEX IF NOT EXISTS idx_section_translation_edits_project_status
    ON section_translation_edits(project_id, review_status);
CREATE INDEX IF NOT EXISTS idx_heading_summary_edits_project_status
    ON heading_summary_edits(project_id, review_status);
CREATE INDEX IF NOT EXISTS idx_section_audio_edits_project_status
    ON section_audio_edits(project_id, review_status);

ALTER TABLE book_metadata_translations
    ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS deleted_by TEXT,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delete_reason TEXT;

ALTER TABLE author_translations
    ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS deleted_by TEXT,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delete_reason TEXT;

ALTER TABLE category_translations
    ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS deleted_by TEXT,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delete_reason TEXT;

ALTER TABLE section_translations
    ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS deleted_by TEXT,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delete_reason TEXT;

ALTER TABLE book_heading_summaries
    ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS deleted_by TEXT,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delete_reason TEXT;

ALTER TABLE section_audio
    ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS deleted_by TEXT,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delete_reason TEXT;

CREATE INDEX IF NOT EXISTS idx_book_metadata_translations_live
    ON book_metadata_translations(book_id, lang)
    WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_author_translations_live
    ON author_translations(author_id, lang)
    WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_category_translations_live
    ON category_translations(category_id, lang)
    WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_section_translations_live
    ON section_translations(book_id, heading_id, lang)
    WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_book_heading_summaries_live
    ON book_heading_summaries(book_id, heading_id, lang)
    WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_section_audio_live
    ON section_audio(book_id, heading_id, lang)
    WHERE is_deleted = false;

WITH existing_book_lang AS (
    SELECT book_id, lang FROM book_metadata_translations WHERE lang IN ('id', 'en') AND is_deleted = false
    UNION
    SELECT book_id, lang FROM section_translations WHERE lang IN ('id', 'en') AND is_deleted = false
    UNION
    SELECT book_id, lang FROM book_heading_summaries WHERE lang IN ('id', 'en') AND is_deleted = false
    UNION
    SELECT book_id, lang FROM section_audio WHERE lang IN ('id', 'en') AND is_deleted = false
),
deduped AS (
    SELECT DISTINCT e.book_id, e.lang
    FROM existing_book_lang e
    JOIN books b ON b.id = e.book_id AND b.is_deleted = false
)
INSERT INTO book_production_projects (
    id,
    book_id,
    lang,
    workflow_status,
    publication_status,
    requires_review,
    requires_audio,
    notes,
    created_at,
    updated_at,
    published_at
)
SELECT (
           substr(md5('book-production:' || book_id::TEXT || ':' || lang), 1, 8) || '-' ||
           substr(md5('book-production:' || book_id::TEXT || ':' || lang), 9, 4) || '-' ||
           substr(md5('book-production:' || book_id::TEXT || ':' || lang), 13, 4) || '-' ||
           substr(md5('book-production:' || book_id::TEXT || ':' || lang), 17, 4) || '-' ||
           substr(md5('book-production:' || book_id::TEXT || ':' || lang), 21, 12)
       )::UUID,
       book_id,
       lang,
       'published',
       'published',
       false,
       false,
       'Backfilled from existing published translation assets',
       now(),
       now(),
       now()
FROM deduped
ON CONFLICT DO NOTHING;
