ALTER TABLE translation_feedbacks
    DROP CONSTRAINT IF EXISTS translation_feedbacks_book_id_heading_id_lang_fkey;

WITH ranked AS (
    SELECT ctid,
           ROW_NUMBER() OVER (
               PARTITION BY category_id, lower(split_part(replace(btrim(lang), '_', '-'), '-', 1))
               ORDER BY updated_at DESC, ctid DESC
           ) AS row_number
    FROM category_translations
)
DELETE FROM category_translations t
USING ranked r
WHERE t.ctid = r.ctid AND r.row_number > 1;

WITH ranked AS (
    SELECT ctid,
           ROW_NUMBER() OVER (
               PARTITION BY author_id, lower(split_part(replace(btrim(lang), '_', '-'), '-', 1))
               ORDER BY updated_at DESC, ctid DESC
           ) AS row_number
    FROM author_translations
)
DELETE FROM author_translations t
USING ranked r
WHERE t.ctid = r.ctid AND r.row_number > 1;

WITH ranked AS (
    SELECT ctid,
           ROW_NUMBER() OVER (
               PARTITION BY book_id, lower(split_part(replace(btrim(lang), '_', '-'), '-', 1))
               ORDER BY updated_at DESC, ctid DESC
           ) AS row_number
    FROM book_metadata_translations
)
DELETE FROM book_metadata_translations t
USING ranked r
WHERE t.ctid = r.ctid AND r.row_number > 1;

WITH ranked AS (
    SELECT ctid,
           ROW_NUMBER() OVER (
               PARTITION BY book_id, heading_id, lower(split_part(replace(btrim(lang), '_', '-'), '-', 1))
               ORDER BY updated_at DESC, ctid DESC
           ) AS row_number
    FROM section_translations
)
DELETE FROM section_translations t
USING ranked r
WHERE t.ctid = r.ctid AND r.row_number > 1;

WITH ranked AS (
    SELECT ctid,
           ROW_NUMBER() OVER (
               PARTITION BY book_id, heading_id, lower(split_part(replace(btrim(lang), '_', '-'), '-', 1))
               ORDER BY updated_at DESC, ctid DESC
           ) AS row_number
    FROM section_audio
)
DELETE FROM section_audio t
USING ranked r
WHERE t.ctid = r.ctid AND r.row_number > 1;

WITH ranked AS (
    SELECT ctid,
           ROW_NUMBER() OVER (
               PARTITION BY book_id, heading_id, lower(split_part(replace(btrim(lang), '_', '-'), '-', 1))
               ORDER BY updated_at DESC, ctid DESC
           ) AS row_number
    FROM book_heading_summaries
)
DELETE FROM book_heading_summaries t
USING ranked r
WHERE t.ctid = r.ctid AND r.row_number > 1;

WITH ranked AS (
    SELECT ctid,
           ROW_NUMBER() OVER (
               PARTITION BY book_id, heading_id, lower(split_part(replace(btrim(lang), '_', '-'), '-', 1)), user_id
               ORDER BY updated_at DESC, ctid DESC
           ) AS row_number
    FROM translation_feedbacks
    WHERE user_id IS NOT NULL
)
DELETE FROM translation_feedbacks t
USING ranked r
WHERE t.ctid = r.ctid AND r.row_number > 1;

WITH ranked AS (
    SELECT ctid,
           ROW_NUMBER() OVER (
               PARTITION BY book_id, heading_id, lower(split_part(replace(btrim(lang), '_', '-'), '-', 1)), client_id
               ORDER BY updated_at DESC, ctid DESC
           ) AS row_number
    FROM translation_feedbacks
    WHERE user_id IS NULL AND client_id IS NOT NULL
)
DELETE FROM translation_feedbacks t
USING ranked r
WHERE t.ctid = r.ctid AND r.row_number > 1;

UPDATE category_translations
SET lang = lower(split_part(replace(btrim(lang), '_', '-'), '-', 1));

UPDATE author_translations
SET lang = lower(split_part(replace(btrim(lang), '_', '-'), '-', 1));

UPDATE book_metadata_translations
SET lang = lower(split_part(replace(btrim(lang), '_', '-'), '-', 1));

UPDATE section_translations
SET lang = lower(split_part(replace(btrim(lang), '_', '-'), '-', 1));

UPDATE translation_feedbacks
SET lang = lower(split_part(replace(btrim(lang), '_', '-'), '-', 1));

UPDATE section_audio
SET lang = lower(split_part(replace(btrim(lang), '_', '-'), '-', 1));

UPDATE book_heading_summaries
SET lang = lower(split_part(replace(btrim(lang), '_', '-'), '-', 1));

ALTER TABLE category_translations
    DROP CONSTRAINT IF EXISTS category_translations_lang_check,
    ADD CONSTRAINT category_translations_lang_check CHECK (lang IN ('ar', 'id', 'en'));

ALTER TABLE author_translations
    DROP CONSTRAINT IF EXISTS author_translations_lang_check,
    ADD CONSTRAINT author_translations_lang_check CHECK (lang IN ('ar', 'id', 'en'));

ALTER TABLE book_metadata_translations
    DROP CONSTRAINT IF EXISTS book_metadata_translations_lang_check,
    ADD CONSTRAINT book_metadata_translations_lang_check CHECK (lang IN ('ar', 'id', 'en'));

ALTER TABLE section_translations
    DROP CONSTRAINT IF EXISTS section_translations_lang_check,
    ADD CONSTRAINT section_translations_lang_check CHECK (lang IN ('ar', 'id', 'en'));

ALTER TABLE section_audio
    DROP CONSTRAINT IF EXISTS section_audio_lang_check,
    ADD CONSTRAINT section_audio_lang_check CHECK (lang IN ('ar', 'id', 'en'));

ALTER TABLE book_heading_summaries
    DROP CONSTRAINT IF EXISTS book_heading_summaries_lang_check,
    ADD CONSTRAINT book_heading_summaries_lang_check CHECK (lang IN ('ar', 'id', 'en'));

ALTER TABLE translation_feedbacks
    ADD CONSTRAINT translation_feedbacks_book_id_heading_id_lang_fkey
    FOREIGN KEY (book_id, heading_id, lang)
    REFERENCES section_translations(book_id, heading_id, lang)
    ON DELETE CASCADE;
