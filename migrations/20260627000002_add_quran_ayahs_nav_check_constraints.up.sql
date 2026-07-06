-- Defensive range CHECKs for the navigation columns on quran_ayahs. These nullable
-- columns are currently unconstrained, so a bad import could store out-of-domain juz/
-- hizb/page values that only surface as wrong UI later (the same juz 1-30 guard already
-- exists on quran_khatam_progress).
--
-- Added NOT VALID on purpose: the constraints are ENFORCED for every new INSERT/UPDATE
-- (so future imports are guarded), but existing rows are NOT scanned at migration time.
-- This keeps the deploy-time auto-migration safe even if legacy data has an out-of-range
-- value — the migration cannot abort and block the release.
--
-- To also enforce the invariant on existing rows, run the preflight below and, once it
-- returns 0 rows for all three, VALIDATE the constraints in a separate maintenance step:
--   SELECT count(*) FROM quran_ayahs WHERE juz_number  IS NOT NULL AND juz_number  NOT BETWEEN 1 AND 30;
--   SELECT count(*) FROM quran_ayahs WHERE hizb_number IS NOT NULL AND hizb_number NOT BETWEEN 1 AND 60;
--   SELECT count(*) FROM quran_ayahs WHERE page_number IS NOT NULL AND page_number < 1;
--   ALTER TABLE quran_ayahs VALIDATE CONSTRAINT quran_ayahs_juz_number_check;
--   ALTER TABLE quran_ayahs VALIDATE CONSTRAINT quran_ayahs_hizb_number_check;
--   ALTER TABLE quran_ayahs VALIDATE CONSTRAINT quran_ayahs_page_number_check;
ALTER TABLE quran_ayahs
    ADD CONSTRAINT quran_ayahs_juz_number_check
        CHECK (juz_number IS NULL OR juz_number BETWEEN 1 AND 30) NOT VALID,
    ADD CONSTRAINT quran_ayahs_hizb_number_check
        CHECK (hizb_number IS NULL OR hizb_number BETWEEN 1 AND 60) NOT VALID,
    ADD CONSTRAINT quran_ayahs_page_number_check
        CHECK (page_number IS NULL OR page_number > 0) NOT VALID;
