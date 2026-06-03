ALTER TABLE quran_import_runs
    DROP CONSTRAINT IF EXISTS quran_import_runs_resource_type_check,
    ADD CONSTRAINT quran_import_runs_resource_type_check
        CHECK (resource_type IN ('surah_metadata', 'surah_info', 'script', 'translation', 'transliteration', 'recitation', 'book_reference'));

CREATE TABLE IF NOT EXISTS quran_transliteration_sources (
    id TEXT PRIMARY KEY,
    lang TEXT NOT NULL,
    name TEXT NOT NULL,
    source_url TEXT,
    format TEXT NOT NULL,
    license_status TEXT NOT NULL DEFAULT 'needs_review',
    checksum TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    imported_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT quran_transliteration_sources_lang_check CHECK (lang IN ('id', 'en')),
    CONSTRAINT quran_transliteration_sources_license_status_check
        CHECK (license_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain')),
    CONSTRAINT quran_transliteration_sources_id_lang_unique UNIQUE (id, lang)
);

CREATE TABLE IF NOT EXISTS quran_ayah_transliterations (
    source_id TEXT NOT NULL REFERENCES quran_transliteration_sources(id) ON DELETE CASCADE,
    surah_id INTEGER NOT NULL,
    ayah_number INTEGER NOT NULL,
    ayah_key TEXT NOT NULL,
    lang TEXT NOT NULL,
    text TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, surah_id, ayah_number),
    FOREIGN KEY (surah_id, ayah_number) REFERENCES quran_ayahs(surah_id, ayah_number) ON DELETE CASCADE,
    CONSTRAINT quran_ayah_transliterations_key_check CHECK (ayah_key = surah_id::text || ':' || ayah_number::text),
    CONSTRAINT quran_ayah_transliterations_lang_check CHECK (lang IN ('id', 'en')),
    CONSTRAINT quran_ayah_transliterations_source_lang_fkey
        FOREIGN KEY (source_id, lang)
        REFERENCES quran_transliteration_sources(id, lang)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_quran_ayah_transliterations_lang
    ON quran_ayah_transliterations(lang, source_id);
CREATE INDEX IF NOT EXISTS idx_quran_ayah_transliterations_ayah_lang
    ON quran_ayah_transliterations(surah_id, ayah_number, lang);
CREATE INDEX IF NOT EXISTS idx_quran_ayah_transliterations_text_trgm
    ON quran_ayah_transliterations USING gin (text gin_trgm_ops);
