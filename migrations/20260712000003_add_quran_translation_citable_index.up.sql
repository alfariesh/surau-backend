CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_quran_ayah_translations_source_ayah
    ON quran_ayah_translations (surah_id, ayah_number, lang, source_id);
