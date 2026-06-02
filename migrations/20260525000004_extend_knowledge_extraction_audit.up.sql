CREATE TABLE IF NOT EXISTS knowledge_prompt_versions (
    id UUID PRIMARY KEY,
    prompt_version TEXT NOT NULL,
    task_name TEXT NOT NULL,
    description TEXT NOT NULL,
    examples_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    extraction_classes TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    policy_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_prompt_versions_task_check
        CHECK (task_name IN ('mentions', 'terms', 'citations', 'relations')),
    UNIQUE (prompt_version, policy_hash)
);

CREATE TABLE IF NOT EXISTS knowledge_extraction_documents (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES knowledge_extraction_runs(id) ON DELETE CASCADE,
    document_id TEXT NOT NULL,
    book_id INTEGER NOT NULL,
    page_id INTEGER NOT NULL,
    heading_id INTEGER,
    source_hash TEXT NOT NULL,
    char_count INTEGER NOT NULL DEFAULT 0,
    tokenizer TEXT NOT NULL DEFAULT 'RegexTokenizer',
    langextract_version TEXT,
    langextract_path TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (book_id, page_id) REFERENCES book_pages(book_id, page_id) ON DELETE CASCADE,
    FOREIGN KEY (book_id, heading_id) REFERENCES book_headings(book_id, heading_id) ON DELETE CASCADE,
    UNIQUE (run_id, document_id)
);

CREATE TABLE IF NOT EXISTS knowledge_extraction_chunks (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES knowledge_extraction_runs(id) ON DELETE CASCADE,
    extraction_document_id UUID REFERENCES knowledge_extraction_documents(id) ON DELETE CASCADE,
    document_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    pass_index INTEGER NOT NULL DEFAULT 0,
    char_start INTEGER NOT NULL,
    char_end INTEGER NOT NULL,
    token_start INTEGER,
    token_end INTEGER,
    prompt_hash TEXT,
    output_hash TEXT,
    raw_output_path TEXT,
    parse_status TEXT NOT NULL DEFAULT 'unknown',
    extracted_count INTEGER NOT NULL DEFAULT 0,
    rejected_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_extraction_chunks_range_check
        CHECK (char_start >= 0 AND char_end > char_start),
    CONSTRAINT knowledge_extraction_chunks_parse_status_check
        CHECK (parse_status IN ('success', 'empty', 'parse_error', 'schema_error', 'api_error', 'unknown')),
    UNIQUE (run_id, document_id, pass_index, chunk_index)
);

CREATE TABLE IF NOT EXISTS knowledge_source_spans (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES knowledge_extraction_runs(id) ON DELETE CASCADE,
    source_object_type TEXT NOT NULL,
    source_object_id UUID,
    book_id INTEGER NOT NULL,
    page_id INTEGER NOT NULL,
    heading_id INTEGER,
    document_id TEXT NOT NULL,
    extraction_class TEXT,
    exact_quote TEXT NOT NULL,
    char_start INTEGER NOT NULL,
    char_end INTEGER NOT NULL,
    token_start INTEGER,
    token_end INTEGER,
    alignment_status TEXT NOT NULL,
    source_hash TEXT,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_source_spans_object_type_check
        CHECK (source_object_type IN ('mention', 'relation', 'claim', 'citation')),
    CONSTRAINT knowledge_source_spans_char_range_check
        CHECK (char_start >= 0 AND char_end > char_start),
    FOREIGN KEY (book_id, page_id) REFERENCES book_pages(book_id, page_id) ON DELETE CASCADE,
    FOREIGN KEY (book_id, heading_id) REFERENCES book_headings(book_id, heading_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS knowledge_extraction_rejections (
    id UUID PRIMARY KEY,
    run_id UUID REFERENCES knowledge_extraction_runs(id) ON DELETE CASCADE,
    chunk_id UUID REFERENCES knowledge_extraction_chunks(id) ON DELETE SET NULL,
    book_id INTEGER NOT NULL,
    page_id INTEGER NOT NULL,
    heading_id INTEGER,
    document_id TEXT,
    extraction_class TEXT,
    extraction_text TEXT,
    exact_quote TEXT,
    char_start INTEGER,
    char_end INTEGER,
    alignment_status TEXT,
    code TEXT NOT NULL,
    message TEXT NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_hash TEXT,
    raw_output_path TEXT,
    review_status TEXT NOT NULL DEFAULT 'rejected',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_extraction_rejections_review_status_check
        CHECK (review_status IN ('rejected', 'needs_review')),
    FOREIGN KEY (book_id, page_id) REFERENCES book_pages(book_id, page_id) ON DELETE CASCADE,
    FOREIGN KEY (book_id, heading_id) REFERENCES book_headings(book_id, heading_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS knowledge_taxonomies (
    id TEXT PRIMARY KEY,
    parent_id TEXT REFERENCES knowledge_taxonomies(id) ON DELETE SET NULL,
    label_ar TEXT NOT NULL,
    label_id TEXT,
    label_en TEXT,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS knowledge_entity_taxonomy_links (
    entity_id UUID NOT NULL REFERENCES knowledge_entities(id) ON DELETE CASCADE,
    taxonomy_id TEXT NOT NULL REFERENCES knowledge_taxonomies(id) ON DELETE CASCADE,
    source_mention_id UUID REFERENCES knowledge_mentions(id) ON DELETE SET NULL,
    review_status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_id, taxonomy_id),
    CONSTRAINT knowledge_entity_taxonomy_links_review_status_check
        CHECK (review_status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review'))
);

CREATE TABLE IF NOT EXISTS knowledge_entity_links (
    id UUID PRIMARY KEY,
    source_entity_id UUID NOT NULL REFERENCES knowledge_entities(id) ON DELETE CASCADE,
    target_entity_id UUID NOT NULL REFERENCES knowledge_entities(id) ON DELETE CASCADE,
    link_type TEXT NOT NULL,
    score NUMERIC(5,4),
    source TEXT NOT NULL DEFAULT 'system',
    decision_status TEXT NOT NULL DEFAULT 'pending',
    reason JSONB NOT NULL DEFAULT '{}'::jsonb,
    reviewer_notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_entity_links_distinct_check CHECK (source_entity_id <> target_entity_id),
    CONSTRAINT knowledge_entity_links_type_check
        CHECK (link_type IN ('same_as', 'possibly_same_as', 'not_same_as', 'merge', 'split')),
    CONSTRAINT knowledge_entity_links_score_check CHECK (score IS NULL OR (score >= 0 AND score <= 1)),
    CONSTRAINT knowledge_entity_links_decision_status_check
        CHECK (decision_status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review')),
    UNIQUE (source_entity_id, target_entity_id, link_type)
);

ALTER TABLE knowledge_mentions
    ADD COLUMN IF NOT EXISTS source_span_id UUID,
    ADD COLUMN IF NOT EXISTS token_start INTEGER,
    ADD COLUMN IF NOT EXISTS token_end INTEGER,
    ADD COLUMN IF NOT EXISTS extraction_index INTEGER,
    ADD COLUMN IF NOT EXISTS group_index INTEGER,
    ADD COLUMN IF NOT EXISTS pass_index INTEGER;

ALTER TABLE knowledge_relations
    ADD COLUMN IF NOT EXISTS source_span_id UUID,
    ADD COLUMN IF NOT EXISTS subject_text TEXT,
    ADD COLUMN IF NOT EXISTS object_text TEXT,
    ADD COLUMN IF NOT EXISTS risk_level TEXT NOT NULL DEFAULT 'medium',
    ADD COLUMN IF NOT EXISTS requires_scholar_review BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE knowledge_claims
    ADD COLUMN IF NOT EXISTS source_span_id UUID,
    ADD COLUMN IF NOT EXISTS subject_text TEXT,
    ADD COLUMN IF NOT EXISTS object_text TEXT,
    ADD COLUMN IF NOT EXISTS predicate TEXT,
    ADD COLUMN IF NOT EXISTS risk_level TEXT NOT NULL DEFAULT 'high',
    ADD COLUMN IF NOT EXISTS certainty TEXT NOT NULL DEFAULT 'explicit',
    ADD COLUMN IF NOT EXISTS requires_scholar_review BOOLEAN NOT NULL DEFAULT TRUE;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'knowledge_mentions_source_span_fk'
    ) THEN
        ALTER TABLE knowledge_mentions
            ADD CONSTRAINT knowledge_mentions_source_span_fk
            FOREIGN KEY (source_span_id) REFERENCES knowledge_source_spans(id) ON DELETE SET NULL;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'knowledge_relations_source_span_fk'
    ) THEN
        ALTER TABLE knowledge_relations
            ADD CONSTRAINT knowledge_relations_source_span_fk
            FOREIGN KEY (source_span_id) REFERENCES knowledge_source_spans(id) ON DELETE SET NULL;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'knowledge_claims_source_span_fk'
    ) THEN
        ALTER TABLE knowledge_claims
            ADD CONSTRAINT knowledge_claims_source_span_fk
            FOREIGN KEY (source_span_id) REFERENCES knowledge_source_spans(id) ON DELETE SET NULL;
    END IF;
END $$;

ALTER TABLE knowledge_entities DROP CONSTRAINT IF EXISTS knowledge_entities_type_check;
ALTER TABLE knowledge_entities
    ADD CONSTRAINT knowledge_entities_type_check
    CHECK (entity_type IN (
        'person',
        'person_reference',
        'place',
        'work_title',
        'book_title',
        'group',
        'institution',
        'concept',
        'citation',
        'quote',
        'theonym'
    ));

ALTER TABLE knowledge_relations DROP CONSTRAINT IF EXISTS knowledge_relations_risk_level_check;
ALTER TABLE knowledge_relations
    ADD CONSTRAINT knowledge_relations_risk_level_check
    CHECK (risk_level IN ('low', 'medium', 'high'));

ALTER TABLE knowledge_claims DROP CONSTRAINT IF EXISTS knowledge_claims_risk_level_check;
ALTER TABLE knowledge_claims
    ADD CONSTRAINT knowledge_claims_risk_level_check
    CHECK (risk_level IN ('low', 'medium', 'high'));

ALTER TABLE knowledge_claims DROP CONSTRAINT IF EXISTS knowledge_claims_certainty_check;
ALTER TABLE knowledge_claims
    ADD CONSTRAINT knowledge_claims_certainty_check
    CHECK (certainty IN ('explicit', 'probable', 'ambiguous'));

INSERT INTO knowledge_taxonomies (id, parent_id, label_ar, label_id, label_en, description)
VALUES
    ('fiqh', NULL, 'الفقه', 'fikih', 'fiqh', 'Legal and ritual practice terminology'),
    ('hadith', NULL, 'الحديث', 'hadis', 'hadith', 'Hadith sciences and narrations'),
    ('aqidah', NULL, 'العقيدة', 'akidah', 'aqidah', 'Creed and theology terminology'),
    ('nahwu', NULL, 'النحو', 'nahwu', 'Arabic grammar', 'Arabic grammar and language sciences'),
    ('adab', NULL, 'الأدب', 'adab', 'adab', 'Manners, literature, and conduct'),
    ('tasawwuf', NULL, 'التصوف', 'tasawuf', 'tasawwuf', 'Tazkiyah and spiritual discipline'),
    ('quran', NULL, 'القرآن', 'quran', 'Quran', 'Quranic references and sciences'),
    ('sirah', NULL, 'السيرة', 'sirah', 'sirah', 'Prophetic biography and related history'),
    ('history', NULL, 'التاريخ', 'sejarah', 'history', 'Historical people, places, and events')
ON CONFLICT (id) DO NOTHING;

CREATE INDEX IF NOT EXISTS idx_knowledge_prompt_versions_task
    ON knowledge_prompt_versions (task_name, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_extraction_documents_run
    ON knowledge_extraction_documents (run_id, book_id, page_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_extraction_chunks_run_doc
    ON knowledge_extraction_chunks (run_id, document_id, pass_index, chunk_index);
CREATE INDEX IF NOT EXISTS idx_knowledge_source_spans_source
    ON knowledge_source_spans (book_id, page_id, char_start, char_end);
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_source_spans_object_unique
    ON knowledge_source_spans (run_id, source_object_type, source_object_id)
    WHERE source_object_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_knowledge_extraction_rejections_run
    ON knowledge_extraction_rejections (run_id, code, book_id, page_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_entity_links_source
    ON knowledge_entity_links (source_entity_id, link_type, decision_status);
CREATE INDEX IF NOT EXISTS idx_knowledge_entity_links_target
    ON knowledge_entity_links (target_entity_id, link_type, decision_status);
