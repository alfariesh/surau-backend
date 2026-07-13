DROP TRIGGER IF EXISTS trg_books_units_stale ON books;
DROP TRIGGER IF EXISTS trg_book_production_projects_units_stale ON book_production_projects;
DROP TRIGGER IF EXISTS trg_book_publications_units_stale ON book_publications;
DROP TRIGGER IF EXISTS trg_book_heading_summaries_units_stale ON book_heading_summaries;
DROP TRIGGER IF EXISTS trg_section_translations_units_stale ON section_translations;
DROP TRIGGER IF EXISTS trg_book_heading_edits_units_stale ON book_heading_edits;
DROP TRIGGER IF EXISTS trg_book_page_edits_units_stale ON book_page_edits;
DROP TRIGGER IF EXISTS trg_book_headings_units_stale ON book_headings;
DROP TRIGGER IF EXISTS trg_book_pages_units_stale ON book_pages;
DROP FUNCTION IF EXISTS kitab_units_asset_stale_trigger();
DROP FUNCTION IF EXISTS kitab_units_book_stale_trigger();
DROP FUNCTION IF EXISTS kitab_units_source_stale_trigger();
DROP FUNCTION IF EXISTS kitab_units_mark_book_stale(INTEGER);

DROP TRIGGER IF EXISTS trg_knowledge_mentions_approved_unit_guard ON knowledge_mentions;
DROP FUNCTION IF EXISTS knowledge_mention_approved_unit_guard();

ALTER TABLE knowledge_mentions
    DROP CONSTRAINT IF EXISTS knowledge_mentions_unit_binding_shape_check,
    DROP CONSTRAINT IF EXISTS knowledge_mentions_unit_binding_status_check,
    DROP CONSTRAINT IF EXISTS knowledge_mentions_unit_fk;

ALTER TABLE knowledge_mentions
    DROP COLUMN IF EXISTS unit_source_hash,
    DROP COLUMN IF EXISTS unit_binding_version,
    DROP COLUMN IF EXISTS unit_binding_status,
    DROP COLUMN IF EXISTS unit_char_end,
    DROP COLUMN IF EXISTS unit_char_start,
    DROP COLUMN IF EXISTS unit_id;

DROP TABLE IF EXISTS citable_unit_catalog_queue;
DROP VIEW IF EXISTS public_book_interpretive_citable_units;

DROP TRIGGER IF EXISTS trg_citable_unit_k1_content_role_compat ON citable_units;
DROP FUNCTION IF EXISTS citable_unit_k1_content_role_compat();

DROP TRIGGER IF EXISTS trg_citable_unit_k1_identity ON citable_units;
DROP FUNCTION IF EXISTS citable_unit_k1_identity_guard();

ALTER TABLE citable_units
    DROP COLUMN IF EXISTS interpretive_retrieval_eligible;

ALTER TABLE citable_units DROP CONSTRAINT IF EXISTS citable_units_kind_check;
ALTER TABLE citable_units
    ADD CONSTRAINT citable_units_kind_check CHECK (
        kind IN (
            'paragraph', 'heading', 'quran_quote', 'footnote', 'html',
            'primary_text', 'translation', 'transliteration'
        )
    ) NOT VALID;
ALTER TABLE citable_units VALIDATE CONSTRAINT citable_units_kind_check;

ALTER TABLE citable_units
    DROP CONSTRAINT IF EXISTS citable_units_source_char_range_check,
    DROP CONSTRAINT IF EXISTS citable_units_review_status_check,
    DROP CONSTRAINT IF EXISTS citable_units_content_role_check;

ALTER TABLE citable_units
    DROP COLUMN IF EXISTS source_char_end,
    DROP COLUMN IF EXISTS source_char_start,
    DROP COLUMN IF EXISTS source_document_hash,
    DROP COLUMN IF EXISTS review_status,
    DROP COLUMN IF EXISTS content_role;

ALTER TABLE books
    DROP COLUMN IF EXISTS units_derivation_profile_version,
    DROP COLUMN IF EXISTS units_stale_at;
