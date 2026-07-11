-- Refuse destructive rollback once Q-1 has accepted real editorial work. The
-- old schema cannot represent drafts or history, so silently collapsing either
-- would lose data.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM quran_surah_editorial WHERE status = 'draft')
       OR EXISTS (SELECT 1 FROM quran_ayah_editorial WHERE status = 'draft') THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'cannot roll back Q-1: draft Quran editorial rows exist';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM quran_editorial_revisions
        WHERE NOT is_migration_baseline
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'cannot roll back Q-1: post-baseline Quran editorial revisions exist';
    END IF;
END $$;

DROP TRIGGER IF EXISTS trg_quran_ayahs_editorial_truncate_guard ON quran_ayahs;
DROP TRIGGER IF EXISTS trg_quran_surahs_editorial_truncate_guard ON quran_surahs;
DROP TRIGGER IF EXISTS trg_quran_surahs_editorial_fields_insert_guard ON quran_surahs;
DROP TRIGGER IF EXISTS trg_quran_surahs_editorial_fields_writer_guard ON quran_surahs;
DROP TRIGGER IF EXISTS trg_quran_editorial_revisions_truncate_guard ON quran_editorial_revisions;
DROP TRIGGER IF EXISTS trg_quran_editorial_revisions_writer_guard ON quran_editorial_revisions;
DROP TRIGGER IF EXISTS trg_quran_ayah_editorial_truncate_guard ON quran_ayah_editorial;
DROP TRIGGER IF EXISTS trg_quran_ayah_editorial_writer_guard ON quran_ayah_editorial;
DROP TRIGGER IF EXISTS trg_quran_surah_editorial_truncate_guard ON quran_surah_editorial;
DROP TRIGGER IF EXISTS trg_quran_surah_editorial_writer_guard ON quran_surah_editorial;

DROP VIEW IF EXISTS quran_ayah_editorial_public;
DROP VIEW IF EXISTS quran_surah_editorial_public;

DROP INDEX IF EXISTS idx_quran_ayah_editorial_published_permitted;
DROP INDEX IF EXISTS idx_quran_surah_editorial_published_permitted;

DROP TABLE IF EXISTS quran_editorial_revisions;

ALTER TABLE quran_ayah_editorial
    DROP CONSTRAINT IF EXISTS quran_ayah_editorial_pkey;
ALTER TABLE quran_ayah_editorial
    ADD CONSTRAINT quran_ayah_editorial_pkey PRIMARY KEY (surah_id, ayah_number, lang);

ALTER TABLE quran_surah_editorial
    DROP CONSTRAINT IF EXISTS quran_surah_editorial_pkey;
ALTER TABLE quran_surah_editorial
    ADD CONSTRAINT quran_surah_editorial_pkey PRIMARY KEY (surah_id, lang);

ALTER TABLE quran_ayah_editorial
    DROP CONSTRAINT IF EXISTS quran_ayah_editorial_updated_by_fk,
    DROP CONSTRAINT IF EXISTS quran_ayah_editorial_publish_timestamp_check,
    DROP CONSTRAINT IF EXISTS quran_ayah_editorial_status_check,
    DROP COLUMN IF EXISTS published_at,
    DROP COLUMN IF EXISTS updated_by,
    DROP COLUMN IF EXISTS status;

ALTER TABLE quran_surah_editorial
    DROP CONSTRAINT IF EXISTS quran_surah_editorial_updated_by_fk,
    DROP CONSTRAINT IF EXISTS quran_surah_editorial_publish_timestamp_check,
    DROP CONSTRAINT IF EXISTS quran_surah_editorial_status_check,
    DROP COLUMN IF EXISTS published_at,
    DROP COLUMN IF EXISTS updated_by,
    DROP COLUMN IF EXISTS status;

-- Restore the exact pre-Q-1 public-read indexes over the remaining published
-- rows. No content, checksum, or timestamp column is rewritten on rollback.
CREATE INDEX IF NOT EXISTS idx_quran_surah_editorial_permitted
    ON quran_surah_editorial (surah_id, lang)
    WHERE license_status = 'permitted';

CREATE INDEX IF NOT EXISTS idx_quran_ayah_editorial_permitted
    ON quran_ayah_editorial (surah_id, lang, ayah_number)
    WHERE license_status = 'permitted';

DROP FUNCTION IF EXISTS quran_editorial_writer_guard();
