DROP INDEX IF EXISTS idx_section_audio_live;
DROP INDEX IF EXISTS idx_book_heading_summaries_live;
DROP INDEX IF EXISTS idx_section_translations_live;
DROP INDEX IF EXISTS idx_category_translations_live;
DROP INDEX IF EXISTS idx_author_translations_live;
DROP INDEX IF EXISTS idx_book_metadata_translations_live;

ALTER TABLE section_audio
    DROP COLUMN IF EXISTS delete_reason,
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS deleted_by,
    DROP COLUMN IF EXISTS is_deleted;

ALTER TABLE book_heading_summaries
    DROP COLUMN IF EXISTS delete_reason,
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS deleted_by,
    DROP COLUMN IF EXISTS is_deleted;

ALTER TABLE section_translations
    DROP COLUMN IF EXISTS delete_reason,
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS deleted_by,
    DROP COLUMN IF EXISTS is_deleted;

ALTER TABLE category_translations
    DROP COLUMN IF EXISTS delete_reason,
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS deleted_by,
    DROP COLUMN IF EXISTS is_deleted;

ALTER TABLE author_translations
    DROP COLUMN IF EXISTS delete_reason,
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS deleted_by,
    DROP COLUMN IF EXISTS is_deleted;

ALTER TABLE book_metadata_translations
    DROP COLUMN IF EXISTS delete_reason,
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS deleted_by,
    DROP COLUMN IF EXISTS is_deleted;

DROP TABLE IF EXISTS section_audio_edits;
DROP TABLE IF EXISTS heading_summary_edits;
DROP TABLE IF EXISTS section_translation_edits;
DROP TABLE IF EXISTS category_translation_edits;
DROP TABLE IF EXISTS author_translation_edits;
DROP TABLE IF EXISTS book_metadata_translation_edits;

DROP INDEX IF EXISTS idx_book_production_projects_owner;
DROP INDEX IF EXISTS idx_book_production_projects_workflow;
DROP INDEX IF EXISTS idx_book_production_projects_active_book_lang;
DROP TABLE IF EXISTS book_production_projects;
