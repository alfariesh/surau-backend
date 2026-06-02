CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS knowledge_extraction_runs (
    id UUID PRIMARY KEY,
    task_name TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    model_id TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'openai',
    provider_base_url TEXT,
    parameters JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_scope JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'running',
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    total_documents INTEGER NOT NULL DEFAULT 0,
    processed_documents INTEGER NOT NULL DEFAULT 0,
    stored_mentions INTEGER NOT NULL DEFAULT 0,
    errors JSONB,
    CONSTRAINT knowledge_extraction_runs_task_check
        CHECK (task_name IN ('mentions', 'terms', 'citations', 'relations')),
    CONSTRAINT knowledge_extraction_runs_status_check
        CHECK (status IN ('running', 'success', 'completed_with_errors', 'failed'))
);

CREATE TABLE IF NOT EXISTS knowledge_mentions (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES knowledge_extraction_runs(id) ON DELETE CASCADE,
    book_id INTEGER NOT NULL,
    page_id INTEGER NOT NULL,
    heading_id INTEGER,
    document_id TEXT NOT NULL,
    extraction_class TEXT NOT NULL,
    extraction_text TEXT NOT NULL,
    exact_quote TEXT NOT NULL,
    char_start INTEGER NOT NULL,
    char_end INTEGER NOT NULL,
    alignment_status TEXT NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    normalized_text TEXT NOT NULL,
    grounded BOOLEAN NOT NULL DEFAULT TRUE,
    confidence NUMERIC(4,3),
    review_status TEXT NOT NULL DEFAULT 'pending',
    source_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_mentions_char_range_check CHECK (char_start >= 0 AND char_end > char_start),
    CONSTRAINT knowledge_mentions_review_status_check
        CHECK (review_status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review')),
    FOREIGN KEY (book_id, page_id) REFERENCES book_pages(book_id, page_id) ON DELETE CASCADE,
    FOREIGN KEY (book_id, heading_id) REFERENCES book_headings(book_id, heading_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS knowledge_entities (
    id UUID PRIMARY KEY,
    entity_type TEXT NOT NULL,
    canonical_name_ar TEXT,
    canonical_name_latin TEXT,
    normalized_name_ar TEXT,
    description_short TEXT,
    authority_refs JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_from_mention_id UUID REFERENCES knowledge_mentions(id) ON DELETE SET NULL,
    review_status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_entities_type_check
        CHECK (entity_type IN (
            'person',
            'place',
            'book_title',
            'group',
            'institution',
            'concept',
            'citation',
            'quote'
        )),
    CONSTRAINT knowledge_entities_review_status_check
        CHECK (review_status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review'))
);

CREATE TABLE IF NOT EXISTS knowledge_entity_labels (
    id UUID PRIMARY KEY,
    entity_id UUID NOT NULL REFERENCES knowledge_entities(id) ON DELETE CASCADE,
    lang TEXT NOT NULL,
    label TEXT NOT NULL,
    label_kind TEXT NOT NULL DEFAULT 'primary',
    source TEXT,
    review_status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_entity_labels_kind_check
        CHECK (label_kind IN ('primary', 'display', 'short', 'description')),
    CONSTRAINT knowledge_entity_labels_review_status_check
        CHECK (review_status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review')),
    UNIQUE (entity_id, lang, label_kind, label)
);

CREATE TABLE IF NOT EXISTS knowledge_entity_aliases (
    id UUID PRIMARY KEY,
    entity_id UUID NOT NULL REFERENCES knowledge_entities(id) ON DELETE CASCADE,
    alias_text TEXT NOT NULL,
    normalized_alias TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'ar',
    alias_type TEXT NOT NULL DEFAULT 'extracted',
    source_mention_id UUID REFERENCES knowledge_mentions(id) ON DELETE SET NULL,
    review_status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_entity_aliases_type_check
        CHECK (alias_type IN ('canonical', 'extracted', 'manual', 'transliteration')),
    CONSTRAINT knowledge_entity_aliases_review_status_check
        CHECK (review_status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review')),
    UNIQUE (entity_id, normalized_alias, language, alias_type)
);

CREATE TABLE IF NOT EXISTS knowledge_entity_candidates (
    mention_id UUID NOT NULL REFERENCES knowledge_mentions(id) ON DELETE CASCADE,
    entity_id UUID NOT NULL REFERENCES knowledge_entities(id) ON DELETE CASCADE,
    score NUMERIC(5,4) NOT NULL DEFAULT 0,
    strategy TEXT NOT NULL,
    reasons JSONB NOT NULL DEFAULT '{}'::jsonb,
    review_status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (mention_id, entity_id, strategy),
    CONSTRAINT knowledge_entity_candidates_score_check CHECK (score >= 0 AND score <= 1),
    CONSTRAINT knowledge_entity_candidates_review_status_check
        CHECK (review_status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review'))
);

CREATE TABLE IF NOT EXISTS knowledge_relations (
    id UUID PRIMARY KEY,
    run_id UUID REFERENCES knowledge_extraction_runs(id) ON DELETE SET NULL,
    subject_entity_id UUID REFERENCES knowledge_entities(id) ON DELETE SET NULL,
    predicate TEXT NOT NULL,
    object_entity_id UUID REFERENCES knowledge_entities(id) ON DELETE SET NULL,
    object_literal TEXT,
    evidence_mention_id UUID REFERENCES knowledge_mentions(id) ON DELETE SET NULL,
    evidence_quote TEXT NOT NULL,
    certainty TEXT NOT NULL DEFAULT 'explicit',
    review_status TEXT NOT NULL DEFAULT 'pending',
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_relations_object_check
        CHECK (object_entity_id IS NOT NULL OR NULLIF(BTRIM(object_literal), '') IS NOT NULL),
    CONSTRAINT knowledge_relations_certainty_check
        CHECK (certainty IN ('explicit', 'probable', 'ambiguous')),
    CONSTRAINT knowledge_relations_review_status_check
        CHECK (review_status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review'))
);

CREATE TABLE IF NOT EXISTS knowledge_claims (
    id UUID PRIMARY KEY,
    run_id UUID REFERENCES knowledge_extraction_runs(id) ON DELETE SET NULL,
    subject_entity_id UUID REFERENCES knowledge_entities(id) ON DELETE SET NULL,
    claim_type TEXT NOT NULL,
    claim_text_ar TEXT,
    claim_text_id TEXT,
    evidence_mention_id UUID REFERENCES knowledge_mentions(id) ON DELETE SET NULL,
    evidence_quote TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_claims_status_check
        CHECK (status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review'))
);

CREATE INDEX IF NOT EXISTS idx_knowledge_extraction_runs_task
    ON knowledge_extraction_runs (task_name, prompt_version, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_mentions_run
    ON knowledge_mentions (run_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_mentions_source
    ON knowledge_mentions (book_id, page_id, heading_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_mentions_class
    ON knowledge_mentions (extraction_class);
CREATE INDEX IF NOT EXISTS idx_knowledge_mentions_review
    ON knowledge_mentions (review_status, extraction_class);
CREATE INDEX IF NOT EXISTS idx_knowledge_mentions_normalized_trgm
    ON knowledge_mentions USING gin (normalized_text gin_trgm_ops);
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_mentions_run_span_unique
    ON knowledge_mentions (run_id, book_id, page_id, extraction_class, char_start, char_end);
CREATE INDEX IF NOT EXISTS idx_knowledge_entities_type_review
    ON knowledge_entities (entity_type, review_status);
CREATE INDEX IF NOT EXISTS idx_knowledge_entities_normalized_trgm
    ON knowledge_entities USING gin (normalized_name_ar gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_knowledge_entity_labels_lang_trgm
    ON knowledge_entity_labels USING gin (label gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_knowledge_entity_aliases_normalized
    ON knowledge_entity_aliases (language, normalized_alias);
CREATE INDEX IF NOT EXISTS idx_knowledge_entity_aliases_normalized_trgm
    ON knowledge_entity_aliases USING gin (normalized_alias gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_knowledge_entity_candidates_entity
    ON knowledge_entity_candidates (entity_id, score DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_relations_predicate
    ON knowledge_relations (predicate, review_status);
CREATE INDEX IF NOT EXISTS idx_knowledge_claims_type_status
    ON knowledge_claims (claim_type, status);
