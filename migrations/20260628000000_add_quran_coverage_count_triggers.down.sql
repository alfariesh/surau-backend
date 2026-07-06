DROP TRIGGER IF EXISTS trg_quran_translation_coverage_sync ON quran_ayah_translations;
DROP TRIGGER IF EXISTS trg_quran_transliteration_coverage_sync ON quran_ayah_transliterations;
DROP FUNCTION IF EXISTS quran_translation_coverage_sync();
DROP FUNCTION IF EXISTS quran_transliteration_coverage_sync();
