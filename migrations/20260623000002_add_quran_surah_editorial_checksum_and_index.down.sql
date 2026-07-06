DROP INDEX IF EXISTS idx_quran_surah_editorial_permitted;

ALTER TABLE quran_surah_editorial
    DROP COLUMN IF EXISTS checksum;
