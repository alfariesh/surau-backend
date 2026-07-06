-- VALIDATE the CHECK constraints that 20260627000002 (juz/hizb/page) and
-- 20260628000001 (ruku_count/slug) added as NOT VALID. NOT VALID enforces new writes
-- but never scans existing rows, so those invariants are documented-but-unenforced on
-- legacy data. This migration closes that gap.
--
-- Each VALIDATE is SELF-GUARDING: it runs only when its preflight finds zero offending
-- rows, otherwise it RAISEs a NOTICE and leaves the constraint NOT VALID. That keeps the
-- deploy-time auto-migration safe — it can NEVER abort on dirty data — while validating
-- automatically the moment the data is clean (a later boot re-runs nothing, but a fresh
-- deploy after a data fix will pick it up if this migration is re-pointed; in practice
-- the canonical 6236-ayah corpus and editorial data are already clean, so all five
-- validate on first run).

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM quran_ayahs WHERE juz_number IS NOT NULL AND juz_number NOT BETWEEN 1 AND 30
    ) THEN
        ALTER TABLE quran_ayahs VALIDATE CONSTRAINT quran_ayahs_juz_number_check;
    ELSE
        RAISE NOTICE 'skip VALIDATE quran_ayahs_juz_number_check: out-of-range juz_number rows exist';
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM quran_ayahs WHERE hizb_number IS NOT NULL AND hizb_number NOT BETWEEN 1 AND 60
    ) THEN
        ALTER TABLE quran_ayahs VALIDATE CONSTRAINT quran_ayahs_hizb_number_check;
    ELSE
        RAISE NOTICE 'skip VALIDATE quran_ayahs_hizb_number_check: out-of-range hizb_number rows exist';
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM quran_ayahs WHERE page_number IS NOT NULL AND page_number < 1
    ) THEN
        ALTER TABLE quran_ayahs VALIDATE CONSTRAINT quran_ayahs_page_number_check;
    ELSE
        RAISE NOTICE 'skip VALIDATE quran_ayahs_page_number_check: non-positive page_number rows exist';
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM quran_surahs WHERE ruku_count IS NOT NULL AND ruku_count < 1
    ) THEN
        ALTER TABLE quran_surahs VALIDATE CONSTRAINT quran_surahs_ruku_count_check;
    ELSE
        RAISE NOTICE 'skip VALIDATE quran_surahs_ruku_count_check: ruku_count < 1 rows exist';
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM quran_surahs WHERE slug = ''
    ) THEN
        ALTER TABLE quran_surahs VALIDATE CONSTRAINT quran_surahs_slug_not_empty;
    ELSE
        RAISE NOTICE 'skip VALIDATE quran_surahs_slug_not_empty: empty-string slug rows exist';
    END IF;
END $$;
