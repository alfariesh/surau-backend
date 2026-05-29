ALTER TABLE book_heading_summaries
    DROP CONSTRAINT IF EXISTS book_heading_summaries_lang_check;

ALTER TABLE section_audio
    DROP CONSTRAINT IF EXISTS section_audio_lang_check;

ALTER TABLE section_translations
    DROP CONSTRAINT IF EXISTS section_translations_lang_check;

ALTER TABLE book_metadata_translations
    DROP CONSTRAINT IF EXISTS book_metadata_translations_lang_check;

ALTER TABLE author_translations
    DROP CONSTRAINT IF EXISTS author_translations_lang_check;

ALTER TABLE category_translations
    DROP CONSTRAINT IF EXISTS category_translations_lang_check;
