UPDATE quran_surah_infos
SET lang = lower(split_part(replace(btrim(lang), '_', '-'), '-', 1));

UPDATE quran_translation_sources
SET lang = lower(split_part(replace(btrim(lang), '_', '-'), '-', 1));

UPDATE quran_ayah_translations qat
SET lang = qts.lang
FROM quran_translation_sources qts
WHERE qat.source_id = qts.id;

UPDATE quran_ayah_translations
SET lang = lower(split_part(replace(btrim(lang), '_', '-'), '-', 1));

ALTER TABLE quran_surah_infos
    DROP CONSTRAINT IF EXISTS quran_surah_infos_lang_check,
    ADD CONSTRAINT quran_surah_infos_lang_check CHECK (lang IN ('ar', 'id', 'en'));

ALTER TABLE quran_translation_sources
    DROP CONSTRAINT IF EXISTS quran_translation_sources_lang_check,
    ADD CONSTRAINT quran_translation_sources_lang_check CHECK (lang IN ('ar', 'id', 'en'));

ALTER TABLE quran_ayah_translations
    DROP CONSTRAINT IF EXISTS quran_ayah_translations_lang_check,
    ADD CONSTRAINT quran_ayah_translations_lang_check CHECK (lang IN ('ar', 'id', 'en'));

ALTER TABLE quran_translation_sources
    DROP CONSTRAINT IF EXISTS quran_translation_sources_id_lang_unique,
    ADD CONSTRAINT quran_translation_sources_id_lang_unique UNIQUE (id, lang);

ALTER TABLE quran_ayah_translations
    DROP CONSTRAINT IF EXISTS quran_ayah_translations_source_lang_fkey,
    ADD CONSTRAINT quran_ayah_translations_source_lang_fkey
    FOREIGN KEY (source_id, lang)
    REFERENCES quran_translation_sources(id, lang)
    ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_quran_ayah_translations_ayah_lang
    ON quran_ayah_translations(surah_id, ayah_number, lang);
