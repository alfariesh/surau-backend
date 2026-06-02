DROP INDEX IF EXISTS idx_quran_ayah_translations_ayah_lang;

ALTER TABLE quran_ayah_translations
    DROP CONSTRAINT IF EXISTS quran_ayah_translations_source_lang_fkey,
    DROP CONSTRAINT IF EXISTS quran_ayah_translations_lang_check;

ALTER TABLE quran_translation_sources
    DROP CONSTRAINT IF EXISTS quran_translation_sources_id_lang_unique,
    DROP CONSTRAINT IF EXISTS quran_translation_sources_lang_check;

ALTER TABLE quran_surah_infos
    DROP CONSTRAINT IF EXISTS quran_surah_infos_lang_check;
