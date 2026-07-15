-- Q-4 owns permanent slug aliases. Refuse rollback after the first real rename
-- because dropping the registry would make historical public URLs unresolvable.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM quran_surah_slug_registry registry
        JOIN quran_surahs surah ON surah.surah_id = registry.surah_id
        WHERE registry.slug IS DISTINCT FROM surah.slug
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'cannot roll back Q-4: historical Quran surah slugs exist';
    END IF;
END $$;

DROP TRIGGER IF EXISTS trg_quran_ayah_editorial_public_slug_guard ON quran_ayah_editorial;
DROP TRIGGER IF EXISTS trg_quran_surah_editorial_public_slug_guard ON quran_surah_editorial;
DROP FUNCTION IF EXISTS quran_editorial_public_slug_guard();

DROP TRIGGER IF EXISTS trg_quran_surah_slug_registry_insert ON quran_surahs;
DROP TRIGGER IF EXISTS trg_quran_surah_slug_registry_update ON quran_surahs;
DROP FUNCTION IF EXISTS quran_surah_slug_registry_seed_insert();
DROP FUNCTION IF EXISTS quran_surah_slug_registry_sync();

DROP TRIGGER IF EXISTS trg_quran_surah_slug_registry_truncate_guard ON quran_surah_slug_registry;
DROP TRIGGER IF EXISTS trg_quran_surah_slug_registry_immutable ON quran_surah_slug_registry;
DROP FUNCTION IF EXISTS quran_surah_slug_registry_immutable_guard();

DROP TABLE IF EXISTS quran_surah_slug_registry;

ALTER TABLE quran_surahs
    DROP CONSTRAINT IF EXISTS quran_surahs_slug_format_check;
