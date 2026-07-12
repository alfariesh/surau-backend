CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_quran_ayahs_page_surah_ayah
    ON quran_ayahs (page_number, surah_id, ayah_number);
