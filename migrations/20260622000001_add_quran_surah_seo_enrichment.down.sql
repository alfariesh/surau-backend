DROP INDEX IF EXISTS idx_quran_surah_editorial_lang;
DROP TABLE IF EXISTS quran_surah_editorial;

ALTER TABLE quran_surahs
    DROP CONSTRAINT IF EXISTS quran_surahs_ruku_count_check,
    DROP CONSTRAINT IF EXISTS quran_surahs_chronological_order_check;

DROP INDEX IF EXISTS idx_quran_surahs_slug;

ALTER TABLE quran_surahs
    DROP COLUMN IF EXISTS ruku_count,
    DROP COLUMN IF EXISTS chronological_order,
    DROP COLUMN IF EXISTS slug;
