-- Per-ayah SEO/editorial enrichment (intisari, keutamaan, FAQ, tafsir range,
-- meta title/description). In-house single-author content like
-- quran_surah_editorial, so the key is (surah_id, ayah_number, lang) with NO
-- source_id (translations/transliterations are multi-source; this is one
-- canonical editorial copy per ayah per language).
-- golang-migrate runs this file with per-statement autocommit (no enclosing
-- transaction). Brand-new table, nothing to backfill, so the ordering is just
-- create table -> indexes.

CREATE TABLE IF NOT EXISTS quran_ayah_editorial (
    surah_id         INTEGER NOT NULL,
    ayah_number      INTEGER NOT NULL,
    ayah_key         TEXT    NOT NULL,
    lang             TEXT    NOT NULL,
    meta_title       TEXT,
    meta_description TEXT,
    intisari_html    TEXT,
    keutamaan_html   TEXT,
    faq              JSONB   NOT NULL DEFAULT '[]'::jsonb,
    tafsir_range     TEXT,
    author_name      TEXT,
    reviewed_by      TEXT,
    reviewed_at      TIMESTAMPTZ,
    license_status   TEXT    NOT NULL DEFAULT 'needs_review',
    checksum         TEXT,
    metadata         JSONB   NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (surah_id, ayah_number, lang),
    FOREIGN KEY (surah_id, ayah_number)
        REFERENCES quran_ayahs(surah_id, ayah_number) ON DELETE CASCADE,
    CONSTRAINT quran_ayah_editorial_key_check
        CHECK (ayah_key = surah_id::text || ':' || ayah_number::text),
    CONSTRAINT quran_ayah_editorial_lang_check
        CHECK (lang IN ('ar', 'id', 'en')),
    CONSTRAINT quran_ayah_editorial_license_status_check
        CHECK (license_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain')),
    CONSTRAINT quran_ayah_editorial_faq_is_array
        CHECK (jsonb_typeof(faq) = 'array'),
    CONSTRAINT quran_ayah_editorial_tafsir_range_format
        CHECK (tafsir_range IS NULL OR tafsir_range ~ '^[0-9]+(-[0-9]+)?$')
);

-- Hot public read path: "all permitted ayat of surah X in lang L, ordered by
-- ayah_number". Equality on (surah_id, lang), range/sort on ayah_number, partial
-- on the only license the public API joins so drafts never bloat the index.
CREATE INDEX IF NOT EXISTS idx_quran_ayah_editorial_permitted
    ON quran_ayah_editorial (surah_id, lang, ayah_number)
    WHERE license_status = 'permitted';

-- Secondary: language-wide scans for ops/coverage reports.
CREATE INDEX IF NOT EXISTS idx_quran_ayah_editorial_lang
    ON quran_ayah_editorial (lang, surah_id, ayah_number);
