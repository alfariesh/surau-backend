-- Surah-level SEO/editorial enrichment.
-- golang-migrate runs this file with per-statement autocommit (no enclosing
-- transaction), so statement order is load-bearing: add columns -> backfill ->
-- unique index (the integrity gate, fails cleanly on a duplicate) -> checks.

-- 1) Language-independent surah-level columns.
ALTER TABLE quran_surahs
    ADD COLUMN IF NOT EXISTS slug TEXT,
    ADD COLUMN IF NOT EXISTS chronological_order INTEGER,
    ADD COLUMN IF NOT EXISTS ruku_count INTEGER;

-- 2) Backfill slug from name_latin BEFORE the unique index. Apostrophes and
-- backticks are stripped (not hyphenated) so "Al-An'am" -> "al-anam" to match
-- the established competitor URL convention. Editorial loader may override.
UPDATE quran_surahs
SET slug = trim(both '-' from regexp_replace(
        lower(translate(name_latin, chr(39) || chr(96), '')),
        '[^a-z0-9]+', '-', 'g'))
WHERE slug IS NULL AND name_latin IS NOT NULL;

-- 3) Unique index after backfill so a duplicate fails index creation cleanly.
CREATE UNIQUE INDEX IF NOT EXISTS idx_quran_surahs_slug
    ON quran_surahs(slug) WHERE slug IS NOT NULL;

-- 4) Range checks last. drop-then-add keeps the migration re-runnable after a
-- dirty state (ADD CONSTRAINT has no IF NOT EXISTS), matching repo convention.
ALTER TABLE quran_surahs
    DROP CONSTRAINT IF EXISTS quran_surahs_chronological_order_check,
    ADD  CONSTRAINT quran_surahs_chronological_order_check
        CHECK (chronological_order IS NULL OR chronological_order BETWEEN 1 AND 114),
    DROP CONSTRAINT IF EXISTS quran_surahs_ruku_count_check,
    ADD  CONSTRAINT quran_surahs_ruku_count_check
        CHECK (ruku_count IS NULL OR ruku_count >= 0);

-- 5) Per-language editorial + SEO copy. Kept separate from quran_surah_infos so
-- the NOT NULL import-provenance columns there are not coupled to editorial,
-- and so an "ar" row can exist without QUL background info.
CREATE TABLE IF NOT EXISTS quran_surah_editorial (
    surah_id INTEGER NOT NULL REFERENCES quran_surahs(surah_id) ON DELETE CASCADE,
    lang TEXT NOT NULL,
    meta_title TEXT,
    meta_description TEXT,
    arti_nama TEXT,
    keutamaan_html TEXT,
    asbabun_nuzul_html TEXT,
    pokok_kandungan_html TEXT,
    author_name TEXT,
    reviewed_by TEXT,
    reviewed_at TIMESTAMPTZ,
    license_status TEXT NOT NULL DEFAULT 'needs_review',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (surah_id, lang),
    CONSTRAINT quran_surah_editorial_lang_check CHECK (lang IN ('ar', 'id', 'en')),
    CONSTRAINT quran_surah_editorial_license_status_check
        CHECK (license_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain'))
);

CREATE INDEX IF NOT EXISTS idx_quran_surah_editorial_lang
    ON quran_surah_editorial(lang, surah_id);
