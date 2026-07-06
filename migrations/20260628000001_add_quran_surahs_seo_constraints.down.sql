-- Revert to the looser constraints from 20260622000001.
ALTER TABLE quran_surahs
    DROP CONSTRAINT IF EXISTS quran_surahs_slug_not_empty,
    DROP CONSTRAINT IF EXISTS quran_surahs_ruku_count_check,
    ADD  CONSTRAINT quran_surahs_ruku_count_check
        CHECK (ruku_count IS NULL OR ruku_count >= 0);

DROP INDEX IF EXISTS idx_quran_surahs_chronological_order;
