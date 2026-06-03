DROP TABLE IF EXISTS quran_ayah_transliterations;
DROP TABLE IF EXISTS quran_transliteration_sources;

DELETE FROM quran_import_runs
WHERE resource_type = 'transliteration';

ALTER TABLE quran_import_runs
    DROP CONSTRAINT IF EXISTS quran_import_runs_resource_type_check,
    ADD CONSTRAINT quran_import_runs_resource_type_check
        CHECK (resource_type IN ('surah_metadata', 'surah_info', 'script', 'translation', 'recitation', 'book_reference'));
