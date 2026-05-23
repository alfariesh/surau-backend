ALTER TABLE category_translations
    DROP CONSTRAINT IF EXISTS category_translations_status_check,
    DROP CONSTRAINT IF EXISTS category_translations_reviewed_by_check,
    DROP COLUMN IF EXISTS reviewed_at,
    DROP COLUMN IF EXISTS reviewed_by,
    DROP COLUMN IF EXISTS translation_status;

ALTER TABLE author_translations
    DROP CONSTRAINT IF EXISTS author_translations_status_check,
    DROP CONSTRAINT IF EXISTS author_translations_reviewed_by_check,
    DROP COLUMN IF EXISTS reviewed_at,
    DROP COLUMN IF EXISTS reviewed_by,
    DROP COLUMN IF EXISTS translation_status;

ALTER TABLE book_metadata_translations
    DROP CONSTRAINT IF EXISTS book_metadata_translations_status_check,
    DROP CONSTRAINT IF EXISTS book_metadata_translations_reviewed_by_check,
    DROP COLUMN IF EXISTS reviewed_at,
    DROP COLUMN IF EXISTS reviewed_by,
    DROP COLUMN IF EXISTS translation_status;

ALTER TABLE section_translations
    DROP CONSTRAINT IF EXISTS section_translations_status_check,
    DROP CONSTRAINT IF EXISTS section_translations_reviewed_by_check,
    DROP COLUMN IF EXISTS reviewed_at,
    DROP COLUMN IF EXISTS reviewed_by,
    DROP COLUMN IF EXISTS translation_status;
