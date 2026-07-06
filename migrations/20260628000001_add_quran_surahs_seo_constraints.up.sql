-- Tighten the surah SEO enrichment columns added in 20260622000001:
--   1. chronological_order is a 1-114 permutation, so it must be UNIQUE (the original
--      migration only range-checked it, letting a typo duplicate a position).
--   2. every surah has at least one ruku, so ruku_count >= 1 (original allowed 0).
--   3. slug is a routing key, so it must not be an empty string (the auto-backfill can
--      emit '' for an all-symbol name, and the existing partial unique index treats ''
--      as a real value that would then collide across surahs).
--
-- golang-migrate runs each statement in its own autocommit (no enclosing transaction),
-- so ordering is load-bearing.
--
-- PREFLIGHT — run these BEFORE deploying; each MUST return 0 rows. The unique index
-- below validates existing rows on creation and will abort the migration on a duplicate,
-- so confirm the data is clean first (editorial import is the only writer):
--   SELECT chronological_order, count(*) FROM quran_surahs
--     WHERE chronological_order IS NOT NULL
--     GROUP BY chronological_order HAVING count(*) > 1;          -- duplicate positions
--   SELECT count(*) FROM quran_surahs WHERE ruku_count = 0;      -- zero-ruku rows
--   SELECT count(*) FROM quran_surahs WHERE slug = '';           -- empty slugs
-- If the empty-slug check is non-zero, run `UPDATE quran_surahs SET slug = NULL WHERE
-- slug = ''` (or re-run the editorial import with corrected slugs) before deploying.

-- 1) Unique across non-null chronological_order values; NULL stays allowed for surahs
-- whose editorial data has not been imported yet.
CREATE UNIQUE INDEX IF NOT EXISTS idx_quran_surahs_chronological_order
    ON quran_surahs(chronological_order) WHERE chronological_order IS NOT NULL;

-- 2) ruku_count >= 1 and 3) slug non-empty. Added NOT VALID so the deploy-time
-- auto-migration never aborts on a legacy row — the constraints are still ENFORCED for
-- every new INSERT/UPDATE. After the preflight above returns 0 for both, VALIDATE them
-- in a separate maintenance step:
--   ALTER TABLE quran_surahs VALIDATE CONSTRAINT quran_surahs_ruku_count_check;
--   ALTER TABLE quran_surahs VALIDATE CONSTRAINT quran_surahs_slug_not_empty;
ALTER TABLE quran_surahs
    DROP CONSTRAINT IF EXISTS quran_surahs_ruku_count_check,
    ADD  CONSTRAINT quran_surahs_ruku_count_check
        CHECK (ruku_count IS NULL OR ruku_count >= 1) NOT VALID,
    ADD  CONSTRAINT quran_surahs_slug_not_empty
        CHECK (slug IS NULL OR slug <> '') NOT VALID;
