-- Bring surah editorial in line with the per-ayah table: a content checksum so
-- no-op re-imports don't bump updated_at (the sitemap lastmod), and a partial
-- index matching the public read predicate (surah_id, lang) WHERE permitted.
-- golang-migrate per-statement autocommit; no backfill needed.

ALTER TABLE quran_surah_editorial
    ADD COLUMN IF NOT EXISTS checksum TEXT;

-- The public read joins ed.surah_id = s.surah_id AND ed.lang = $1 AND
-- license_status = 'permitted'. The PK (surah_id, lang) already serves equality;
-- this partial index narrows to published rows for the hot path. The existing
-- idx_quran_surah_editorial_lang (lang, surah_id) is kept for language-wide scans.
CREATE INDEX IF NOT EXISTS idx_quran_surah_editorial_permitted
    ON quran_surah_editorial (surah_id, lang)
    WHERE license_status = 'permitted';
