-- Composite indexes for the juz/hizb reader navigation queries. ListNavigationAyahs
-- filters on a.juz_number / a.hizb_number then orders by surah_id, ayah_number, so a
-- leading-column composite makes the plan stable as the corpus or call rate grows.
-- Pure additions (CREATE INDEX IF NOT EXISTS) — safe to apply with no preflight.
CREATE INDEX IF NOT EXISTS idx_quran_ayahs_juz
    ON quran_ayahs (juz_number, surah_id, ayah_number);

CREATE INDEX IF NOT EXISTS idx_quran_ayahs_hizb
    ON quran_ayahs (hizb_number, surah_id, ayah_number);
