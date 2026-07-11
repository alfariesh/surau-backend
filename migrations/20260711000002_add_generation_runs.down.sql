-- Reverse B-6 without touching pre-existing translation/extraction data.

DROP TRIGGER IF EXISTS trg_book_metadata_translations_provenance ON book_metadata_translations;
DROP TRIGGER IF EXISTS trg_author_translations_provenance ON author_translations;
DROP TRIGGER IF EXISTS trg_category_translations_provenance ON category_translations;
DROP TRIGGER IF EXISTS trg_section_translations_provenance ON section_translations;
DROP TRIGGER IF EXISTS trg_book_heading_summaries_provenance ON book_heading_summaries;
DROP TRIGGER IF EXISTS trg_book_metadata_translation_edits_provenance ON book_metadata_translation_edits;
DROP TRIGGER IF EXISTS trg_author_translation_edits_provenance ON author_translation_edits;
DROP TRIGGER IF EXISTS trg_category_translation_edits_provenance ON category_translation_edits;
DROP TRIGGER IF EXISTS trg_section_translation_edits_provenance ON section_translation_edits;
DROP TRIGGER IF EXISTS trg_heading_summary_edits_provenance ON heading_summary_edits;
DROP FUNCTION IF EXISTS generated_asset_provenance_guard();

ALTER TABLE book_metadata_translations
    DROP COLUMN IF EXISTS generation_run_id,
    DROP COLUMN IF EXISTS provenance_class;
ALTER TABLE author_translations
    DROP COLUMN IF EXISTS generation_run_id,
    DROP COLUMN IF EXISTS provenance_class;
ALTER TABLE category_translations
    DROP COLUMN IF EXISTS generation_run_id,
    DROP COLUMN IF EXISTS provenance_class;
ALTER TABLE section_translations
    DROP COLUMN IF EXISTS generation_run_id,
    DROP COLUMN IF EXISTS provenance_class;
ALTER TABLE book_heading_summaries
    DROP COLUMN IF EXISTS generation_run_id,
    DROP COLUMN IF EXISTS provenance_class;

ALTER TABLE book_metadata_translation_edits
    DROP COLUMN IF EXISTS generation_run_id,
    DROP COLUMN IF EXISTS provenance_class;
ALTER TABLE author_translation_edits
    DROP COLUMN IF EXISTS generation_run_id,
    DROP COLUMN IF EXISTS provenance_class;
ALTER TABLE category_translation_edits
    DROP COLUMN IF EXISTS generation_run_id,
    DROP COLUMN IF EXISTS provenance_class;
ALTER TABLE section_translation_edits
    DROP COLUMN IF EXISTS generation_run_id,
    DROP COLUMN IF EXISTS provenance_class;
ALTER TABLE heading_summary_edits
    DROP COLUMN IF EXISTS generation_run_id,
    DROP COLUMN IF EXISTS provenance_class;

DROP TRIGGER IF EXISTS trg_cross_references_generation_identity ON cross_references;
DROP FUNCTION IF EXISTS cross_reference_generation_identity_guard();
ALTER TABLE cross_references
    DROP CONSTRAINT IF EXISTS cross_references_generation_identity_check,
    DROP COLUMN IF EXISTS generation_run_id;

DROP TRIGGER IF EXISTS trg_citable_units_generation_identity ON citable_units;
DROP FUNCTION IF EXISTS citable_unit_generation_identity_guard();
ALTER TABLE citable_units
    DROP CONSTRAINT IF EXISTS citable_units_generation_identity_check,
    DROP COLUMN IF EXISTS generation_run_id;

DROP TRIGGER IF EXISTS trg_knowledge_extraction_generation_identity ON knowledge_extraction_runs;
DROP FUNCTION IF EXISTS knowledge_extraction_generation_identity_guard();

ALTER TABLE knowledge_extraction_runs
    DROP CONSTRAINT IF EXISTS knowledge_extraction_runs_generation_run_fk;

DROP TRIGGER IF EXISTS trg_generation_runs_immutable ON generation_runs;
DROP TABLE IF EXISTS generation_runs;
DROP FUNCTION IF EXISTS generation_runs_immutable_guard();
DROP FUNCTION IF EXISTS generation_run_safe_uuid(TEXT);
