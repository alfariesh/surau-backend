CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS quran_import_runs (
    id UUID PRIMARY KEY,
    source_name TEXT NOT NULL,
    source_url TEXT,
    qul_resource_id TEXT,
    resource_type TEXT NOT NULL,
    format TEXT NOT NULL,
    checksum TEXT NOT NULL,
    license_status TEXT NOT NULL DEFAULT 'needs_review',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    dry_run BOOLEAN NOT NULL DEFAULT FALSE,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT quran_import_runs_resource_type_check
        CHECK (resource_type IN ('surah_metadata', 'surah_info', 'script', 'translation', 'recitation', 'book_reference')),
    CONSTRAINT quran_import_runs_license_status_check
        CHECK (license_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain'))
);

CREATE TABLE IF NOT EXISTS quran_surahs (
    surah_id INTEGER PRIMARY KEY,
    name_arabic TEXT,
    name_latin TEXT,
    name_translation TEXT,
    revelation_type TEXT,
    ayah_count INTEGER NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT quran_surahs_id_check CHECK (surah_id BETWEEN 1 AND 114),
    CONSTRAINT quran_surahs_ayah_count_check CHECK (ayah_count >= 0)
);

CREATE TABLE IF NOT EXISTS quran_surah_infos (
    surah_id INTEGER NOT NULL REFERENCES quran_surahs(surah_id) ON DELETE CASCADE,
    lang TEXT NOT NULL,
    surah_name TEXT,
    text_html TEXT NOT NULL,
    short_text TEXT,
    source_name TEXT NOT NULL,
    source_url TEXT,
    qul_resource_id TEXT,
    format TEXT NOT NULL,
    license_status TEXT NOT NULL DEFAULT 'needs_review',
    checksum TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    imported_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (surah_id, lang),
    CONSTRAINT quran_surah_infos_license_status_check
        CHECK (license_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain'))
);

CREATE TABLE IF NOT EXISTS quran_ayahs (
    surah_id INTEGER NOT NULL REFERENCES quran_surahs(surah_id) ON DELETE CASCADE,
    ayah_number INTEGER NOT NULL,
    ayah_key TEXT NOT NULL UNIQUE,
    text_qpc_hafs TEXT,
    text_imlaei_simple TEXT,
    search_text TEXT,
    script_type TEXT,
    font_family TEXT,
    page_number INTEGER,
    juz_number INTEGER,
    hizb_number INTEGER,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (surah_id, ayah_number),
    CONSTRAINT quran_ayahs_number_check CHECK (ayah_number > 0),
    CONSTRAINT quran_ayahs_key_check CHECK (ayah_key = surah_id::text || ':' || ayah_number::text)
);

CREATE TABLE IF NOT EXISTS quran_translation_sources (
    id TEXT PRIMARY KEY,
    lang TEXT NOT NULL,
    name TEXT NOT NULL,
    translator TEXT,
    source_url TEXT,
    qul_resource_id TEXT,
    format TEXT NOT NULL,
    license_status TEXT NOT NULL DEFAULT 'needs_review',
    checksum TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    imported_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT quran_translation_sources_license_status_check
        CHECK (license_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain'))
);

CREATE TABLE IF NOT EXISTS quran_ayah_translations (
    source_id TEXT NOT NULL REFERENCES quran_translation_sources(id) ON DELETE CASCADE,
    surah_id INTEGER NOT NULL,
    ayah_number INTEGER NOT NULL,
    ayah_key TEXT NOT NULL,
    lang TEXT NOT NULL,
    text TEXT NOT NULL,
    footnotes JSONB,
    chunks JSONB,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, surah_id, ayah_number),
    FOREIGN KEY (surah_id, ayah_number) REFERENCES quran_ayahs(surah_id, ayah_number) ON DELETE CASCADE,
    CONSTRAINT quran_ayah_translations_key_check CHECK (ayah_key = surah_id::text || ':' || ayah_number::text)
);

CREATE TABLE IF NOT EXISTS quran_recitations (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    reciter_name TEXT,
    style TEXT,
    mode TEXT NOT NULL DEFAULT 'unknown',
    source_url TEXT,
    qul_resource_id TEXT,
    format TEXT NOT NULL,
    license_status TEXT NOT NULL DEFAULT 'needs_review',
    checksum TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    imported_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT quran_recitations_mode_check CHECK (mode IN ('surah', 'ayah', 'unknown')),
    CONSTRAINT quran_recitations_license_status_check
        CHECK (license_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain'))
);

CREATE TABLE IF NOT EXISTS quran_audio_tracks (
    recitation_id TEXT NOT NULL REFERENCES quran_recitations(id) ON DELETE CASCADE,
    track_type TEXT NOT NULL,
    track_key TEXT NOT NULL,
    surah_id INTEGER NOT NULL REFERENCES quran_surahs(surah_id) ON DELETE CASCADE,
    ayah_number INTEGER,
    audio_url TEXT,
    r2_key TEXT,
    public_url TEXT,
    duration_ms INTEGER,
    duration_seconds INTEGER,
    mime_type TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (recitation_id, track_type, track_key),
    FOREIGN KEY (surah_id, ayah_number) REFERENCES quran_ayahs(surah_id, ayah_number) ON DELETE CASCADE,
    CONSTRAINT quran_audio_tracks_type_check CHECK (track_type IN ('surah', 'ayah')),
    CONSTRAINT quran_audio_tracks_ayah_check
        CHECK (
            (track_type = 'surah' AND ayah_number IS NULL AND track_key = surah_id::text)
            OR
            (track_type = 'ayah' AND ayah_number IS NOT NULL AND track_key = surah_id::text || ':' || ayah_number::text)
        ),
    CONSTRAINT quran_audio_tracks_duration_check CHECK (duration_ms IS NULL OR duration_ms >= 0)
);

CREATE TABLE IF NOT EXISTS quran_audio_segments (
    recitation_id TEXT NOT NULL,
    track_type TEXT NOT NULL,
    track_key TEXT NOT NULL,
    surah_id INTEGER NOT NULL,
    ayah_number INTEGER NOT NULL,
    segment_index INTEGER NOT NULL,
    timestamp_from_ms INTEGER NOT NULL,
    timestamp_to_ms INTEGER NOT NULL,
    duration_ms INTEGER,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (recitation_id, track_type, track_key, ayah_number, segment_index),
    FOREIGN KEY (recitation_id, track_type, track_key)
        REFERENCES quran_audio_tracks(recitation_id, track_type, track_key) ON DELETE CASCADE,
    FOREIGN KEY (surah_id, ayah_number) REFERENCES quran_ayahs(surah_id, ayah_number) ON DELETE CASCADE,
    CONSTRAINT quran_audio_segments_type_check CHECK (track_type IN ('surah', 'ayah')),
    CONSTRAINT quran_audio_segments_index_check CHECK (segment_index > 0),
    CONSTRAINT quran_audio_segments_time_check CHECK (timestamp_from_ms >= 0 AND timestamp_to_ms >= timestamp_from_ms),
    CONSTRAINT quran_audio_segments_duration_check CHECK (duration_ms IS NULL OR duration_ms >= 0)
);

CREATE TABLE IF NOT EXISTS quran_book_references (
    id UUID PRIMARY KEY,
    book_id INTEGER NOT NULL,
    page_id INTEGER NOT NULL,
    heading_id INTEGER,
    knowledge_mention_id UUID REFERENCES knowledge_mentions(id) ON DELETE SET NULL,
    source_text TEXT NOT NULL,
    normalized_text TEXT NOT NULL,
    reference_kind TEXT NOT NULL,
    surah_id INTEGER REFERENCES quran_surahs(surah_id) ON DELETE SET NULL,
    from_ayah_number INTEGER,
    to_ayah_number INTEGER,
    from_ayah_key TEXT,
    to_ayah_key TEXT,
    match_strategy TEXT NOT NULL,
    confidence NUMERIC(5,4),
    review_status TEXT NOT NULL DEFAULT 'approved',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (book_id, page_id) REFERENCES book_pages(book_id, page_id) ON DELETE CASCADE,
    FOREIGN KEY (book_id, heading_id) REFERENCES book_headings(book_id, heading_id) ON DELETE CASCADE,
    FOREIGN KEY (surah_id, from_ayah_number) REFERENCES quran_ayahs(surah_id, ayah_number) ON DELETE SET NULL,
    FOREIGN KEY (surah_id, to_ayah_number) REFERENCES quran_ayahs(surah_id, ayah_number) ON DELETE SET NULL,
    CONSTRAINT quran_book_references_kind_check CHECK (reference_kind IN ('surah_ayah', 'surah', 'quote', 'ambiguous')),
    CONSTRAINT quran_book_references_review_status_check
        CHECK (review_status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review')),
    CONSTRAINT quran_book_references_confidence_check CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    CONSTRAINT quran_book_references_range_check
        CHECK (
            (from_ayah_number IS NULL AND to_ayah_number IS NULL)
            OR
            (surah_id IS NOT NULL AND from_ayah_number IS NOT NULL AND to_ayah_number IS NOT NULL AND to_ayah_number >= from_ayah_number)
        ),
    CONSTRAINT quran_book_references_key_check
        CHECK (
            (from_ayah_number IS NULL AND from_ayah_key IS NULL AND to_ayah_number IS NULL AND to_ayah_key IS NULL)
            OR
            (
                from_ayah_key = surah_id::text || ':' || from_ayah_number::text
                AND to_ayah_key = surah_id::text || ':' || to_ayah_number::text
            )
        )
);

CREATE INDEX IF NOT EXISTS idx_quran_import_runs_resource ON quran_import_runs(resource_type, imported_at DESC);
CREATE INDEX IF NOT EXISTS idx_quran_surahs_name_arabic_trgm ON quran_surahs USING gin (name_arabic gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_quran_surah_infos_lang ON quran_surah_infos(lang, surah_id);
CREATE INDEX IF NOT EXISTS idx_quran_surah_infos_text_trgm ON quran_surah_infos USING gin (text_html gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_quran_ayahs_surah ON quran_ayahs(surah_id, ayah_number);
CREATE INDEX IF NOT EXISTS idx_quran_ayahs_page ON quran_ayahs(page_number);
CREATE INDEX IF NOT EXISTS idx_quran_ayahs_text_qpc_trgm ON quran_ayahs USING gin (text_qpc_hafs gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_quran_ayahs_search_text_trgm ON quran_ayahs USING gin (search_text gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_quran_ayah_translations_lang ON quran_ayah_translations(lang, source_id);
CREATE INDEX IF NOT EXISTS idx_quran_ayah_translations_text_trgm ON quran_ayah_translations USING gin (text gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_quran_audio_tracks_ayah ON quran_audio_tracks(surah_id, ayah_number);
CREATE INDEX IF NOT EXISTS idx_quran_audio_segments_ayah ON quran_audio_segments(surah_id, ayah_number);
CREATE INDEX IF NOT EXISTS idx_quran_book_references_book_status ON quran_book_references(book_id, review_status, page_id);
CREATE INDEX IF NOT EXISTS idx_quran_book_references_surah ON quran_book_references(surah_id, from_ayah_number, to_ayah_number);
CREATE UNIQUE INDEX IF NOT EXISTS idx_quran_book_references_mention_unique
    ON quran_book_references(knowledge_mention_id)
    WHERE knowledge_mention_id IS NOT NULL;
