ALTER TABLE section_translations
    ADD COLUMN IF NOT EXISTS translation_status TEXT NOT NULL DEFAULT 'generated',
    ADD COLUMN IF NOT EXISTS reviewed_by TEXT,
    ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;

ALTER TABLE section_translations
    DROP CONSTRAINT IF EXISTS section_translations_status_check;
ALTER TABLE section_translations
    DROP CONSTRAINT IF EXISTS section_translations_reviewed_by_check;

ALTER TABLE section_translations
    ADD CONSTRAINT section_translations_status_check
    CHECK (translation_status IN ('generated', 'reviewed'));
ALTER TABLE section_translations
    ADD CONSTRAINT section_translations_reviewed_by_check
    CHECK (translation_status <> 'reviewed' OR NULLIF(BTRIM(reviewed_by), '') IS NOT NULL);

ALTER TABLE book_metadata_translations
    ADD COLUMN IF NOT EXISTS translation_status TEXT NOT NULL DEFAULT 'generated',
    ADD COLUMN IF NOT EXISTS reviewed_by TEXT,
    ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;

ALTER TABLE book_metadata_translations
    DROP CONSTRAINT IF EXISTS book_metadata_translations_status_check;
ALTER TABLE book_metadata_translations
    DROP CONSTRAINT IF EXISTS book_metadata_translations_reviewed_by_check;

ALTER TABLE book_metadata_translations
    ADD CONSTRAINT book_metadata_translations_status_check
    CHECK (translation_status IN ('generated', 'reviewed'));
ALTER TABLE book_metadata_translations
    ADD CONSTRAINT book_metadata_translations_reviewed_by_check
    CHECK (translation_status <> 'reviewed' OR NULLIF(BTRIM(reviewed_by), '') IS NOT NULL);

ALTER TABLE author_translations
    ADD COLUMN IF NOT EXISTS translation_status TEXT NOT NULL DEFAULT 'generated',
    ADD COLUMN IF NOT EXISTS reviewed_by TEXT,
    ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;

ALTER TABLE author_translations
    DROP CONSTRAINT IF EXISTS author_translations_status_check;
ALTER TABLE author_translations
    DROP CONSTRAINT IF EXISTS author_translations_reviewed_by_check;

ALTER TABLE author_translations
    ADD CONSTRAINT author_translations_status_check
    CHECK (translation_status IN ('generated', 'reviewed'));
ALTER TABLE author_translations
    ADD CONSTRAINT author_translations_reviewed_by_check
    CHECK (translation_status <> 'reviewed' OR NULLIF(BTRIM(reviewed_by), '') IS NOT NULL);

ALTER TABLE category_translations
    ADD COLUMN IF NOT EXISTS translation_status TEXT NOT NULL DEFAULT 'generated',
    ADD COLUMN IF NOT EXISTS reviewed_by TEXT,
    ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;

ALTER TABLE category_translations
    DROP CONSTRAINT IF EXISTS category_translations_status_check;
ALTER TABLE category_translations
    DROP CONSTRAINT IF EXISTS category_translations_reviewed_by_check;

ALTER TABLE category_translations
    ADD CONSTRAINT category_translations_status_check
    CHECK (translation_status IN ('generated', 'reviewed'));
ALTER TABLE category_translations
    ADD CONSTRAINT category_translations_reviewed_by_check
    CHECK (translation_status <> 'reviewed' OR NULLIF(BTRIM(reviewed_by), '') IS NOT NULL);
