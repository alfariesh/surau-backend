ALTER TABLE quran_ayahs
    DROP CONSTRAINT IF EXISTS quran_ayahs_juz_number_check,
    DROP CONSTRAINT IF EXISTS quran_ayahs_hizb_number_check,
    DROP CONSTRAINT IF EXISTS quran_ayahs_page_number_check;
