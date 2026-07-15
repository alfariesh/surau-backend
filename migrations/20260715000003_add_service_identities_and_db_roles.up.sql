-- A-2: named machine identities, scoped credentials, durable request audit,
-- and least-privilege database roles. Login roles/passwords are deliberately
-- provisioned per environment by the rotation runbook; migrations create only
-- stable NOLOGIN group roles.

CREATE TABLE service_principals (
    id UUID PRIMARY KEY,
    principal_name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT service_principals_name_check CHECK (
        principal_name ~ '^[a-z][a-z0-9-]{2,62}$'
    )
);

CREATE TABLE service_principal_scopes (
    principal_id UUID NOT NULL REFERENCES service_principals(id) ON DELETE CASCADE,
    scope TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (principal_id, scope),
    CONSTRAINT service_principal_scopes_scope_check CHECK (scope IN (
        'collab:draft:write',
        'rag-eval:read',
        'enrichment:read',
        'prompt-registry:manage',
        'inference-budget:manage'
    ))
);

CREATE TABLE service_tokens (
    id UUID PRIMARY KEY,
    principal_id UUID NOT NULL REFERENCES service_principals(id) ON DELETE CASCADE,
    secret_hash BYTEA NOT NULL UNIQUE,
    token_kind TEXT NOT NULL DEFAULT 'structured',
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT service_tokens_kind_check CHECK (token_kind IN ('structured', 'legacy')),
    CONSTRAINT service_tokens_expiry_check CHECK (
        expires_at > created_at
        AND expires_at <= created_at + INTERVAL '90 days'
    )
);

CREATE INDEX idx_service_tokens_principal_active
    ON service_tokens (principal_id, expires_at DESC)
    WHERE revoked_at IS NULL;

CREATE TABLE service_request_audit_logs (
    id UUID PRIMARY KEY,
    principal_id UUID REFERENCES service_principals(id) ON DELETE SET NULL,
    principal_name TEXT NOT NULL,
    token_id UUID REFERENCES service_tokens(id) ON DELETE SET NULL,
    required_scope TEXT,
    method TEXT NOT NULL,
    route_template TEXT NOT NULL,
    request_id TEXT,
    trace_id TEXT,
    client_ip INET,
    auth_outcome TEXT NOT NULL,
    response_status INTEGER,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    CONSTRAINT service_request_audit_principal_check CHECK (btrim(principal_name) <> ''),
    CONSTRAINT service_request_audit_outcome_check CHECK (auth_outcome IN (
        'started', 'allowed', 'missing', 'malformed', 'invalid', 'expired',
        'token_revoked', 'principal_revoked', 'insufficient_scope', 'unavailable'
    )),
    CONSTRAINT service_request_audit_status_check CHECK (
        response_status IS NULL OR response_status BETWEEN 100 AND 599
    )
);

CREATE INDEX idx_service_request_audit_created
    ON service_request_audit_logs (started_at DESC, id);
CREATE INDEX idx_service_request_audit_principal
    ON service_request_audit_logs (principal_name, started_at DESC, id);

CREATE TABLE service_identity_events (
    id UUID PRIMARY KEY,
    principal_id UUID REFERENCES service_principals(id) ON DELETE SET NULL,
    principal_name TEXT NOT NULL,
    token_id UUID REFERENCES service_tokens(id) ON DELETE SET NULL,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT service_identity_events_action_check CHECK (action IN (
        'principal_created', 'principal_updated', 'principal_revoked',
        'token_issued', 'token_revoked', 'legacy_token_bootstrapped'
    )),
    CONSTRAINT service_identity_events_metadata_check CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX idx_service_identity_events_principal
    ON service_identity_events (principal_id, created_at DESC, id);

CREATE OR REPLACE FUNCTION service_principal_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.principal_name <> OLD.principal_name THEN
        RAISE EXCEPTION 'service principal name is immutable'
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
        RAISE EXCEPTION 'service principal revocation is permanent'
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_service_principal_immutable
    BEFORE UPDATE ON service_principals
    FOR EACH ROW EXECUTE FUNCTION service_principal_immutable_guard();

CREATE OR REPLACE FUNCTION service_token_revocation_guard() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
        RAISE EXCEPTION 'service token revocation is permanent'
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
    IF NEW.secret_hash IS DISTINCT FROM OLD.secret_hash
       OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
       OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
       OR NEW.token_kind IS DISTINCT FROM OLD.token_kind THEN
        RAISE EXCEPTION 'service token identity is immutable'
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_service_token_revocation
    BEFORE UPDATE ON service_tokens
    FOR EACH ROW EXECUTE FUNCTION service_token_revocation_guard();

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'surau_extraction_writer') THEN
        CREATE ROLE surau_extraction_writer
            NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'surau_importer') THEN
        CREATE ROLE surau_importer
            NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'surau_collab_store') THEN
        CREATE ROLE surau_collab_store
            NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    END IF;
END;
$$;

GRANT USAGE ON SCHEMA public TO surau_extraction_writer, surau_importer, surau_collab_store;

-- Collab owns only the CRDT state table; editorial draft writes still flow
-- through the scoped Go HTTP endpoint.
GRANT SELECT ON collab_documents TO surau_collab_store;
GRANT INSERT (name, state, updated_at) ON collab_documents TO surau_collab_store;
GRANT UPDATE (state, updated_at) ON collab_documents TO surau_collab_store;

-- Extraction source reads and machine-run ledgers.
GRANT SELECT ON
    book_pages, book_headings, book_heading_ranges, citable_units,
    generation_runs, knowledge_prompt_versions, knowledge_extraction_runs,
    knowledge_extraction_documents, knowledge_extraction_chunks,
    knowledge_source_spans, knowledge_extraction_rejections,
    knowledge_mentions, knowledge_entities, knowledge_entity_labels,
    knowledge_entity_aliases, knowledge_entity_candidates,
    knowledge_relations, knowledge_claims, knowledge_taxonomies,
    knowledge_entity_taxonomy_links, knowledge_entity_links
TO surau_extraction_writer;

GRANT INSERT ON generation_runs TO surau_extraction_writer;
GRANT INSERT, UPDATE ON
    knowledge_prompt_versions, knowledge_extraction_runs,
    knowledge_extraction_documents, knowledge_extraction_chunks,
    knowledge_source_spans
TO surau_extraction_writer;

GRANT INSERT (
    id, run_id, chunk_id, book_id, page_id, heading_id, document_id,
    extraction_class, extraction_text, exact_quote, char_start, char_end,
    alignment_status, code, message, attributes, source_hash, raw_output_path,
    created_at
) ON knowledge_extraction_rejections TO surau_extraction_writer;

GRANT INSERT (
    id, run_id, book_id, page_id, heading_id, document_id, extraction_class,
    extraction_text, exact_quote, char_start, char_end, alignment_status,
    attributes, normalized_text, normalization_version, grounded, confidence,
    source_hash, created_at, source_span_id, token_start, token_end,
    extraction_index, group_index, pass_index, unit_id, unit_char_start,
    unit_char_end, unit_binding_status, unit_binding_version, unit_source_hash
) ON knowledge_mentions TO surau_extraction_writer;
GRANT UPDATE (
    extraction_text, exact_quote, alignment_status, attributes, normalized_text,
    normalization_version, grounded, confidence, source_hash, source_span_id,
    token_start, token_end, extraction_index, group_index, pass_index, unit_id,
    unit_char_start, unit_char_end, unit_binding_status, unit_binding_version,
    unit_source_hash
) ON knowledge_mentions TO surau_extraction_writer;

GRANT INSERT (
    id, entity_type, canonical_name_ar, canonical_name_latin,
    normalized_name_ar, normalization_version, description_short,
    authority_refs, created_from_mention_id, created_at, updated_at
) ON knowledge_entities TO surau_extraction_writer;
GRANT UPDATE (
    canonical_name_ar, canonical_name_latin, normalized_name_ar,
    normalization_version, description_short, authority_refs,
    created_from_mention_id, updated_at
) ON knowledge_entities TO surau_extraction_writer;

GRANT INSERT (id, entity_id, lang, label, label_kind, source, created_at)
    ON knowledge_entity_labels TO surau_extraction_writer;
GRANT UPDATE (label, source)
    ON knowledge_entity_labels TO surau_extraction_writer;

GRANT INSERT (
    id, entity_id, alias_text, normalized_alias, normalization_version,
    language, alias_type, source_mention_id, created_at
) ON knowledge_entity_aliases TO surau_extraction_writer;
GRANT UPDATE (alias_text, normalized_alias, normalization_version, source_mention_id)
    ON knowledge_entity_aliases TO surau_extraction_writer;

GRANT INSERT (mention_id, entity_id, score, strategy, reasons, created_at)
    ON knowledge_entity_candidates TO surau_extraction_writer;
GRANT UPDATE (score, reasons)
    ON knowledge_entity_candidates TO surau_extraction_writer;

GRANT INSERT (
    id, run_id, subject_entity_id, predicate, object_entity_id, object_literal,
    evidence_mention_id, evidence_quote, certainty, attributes, created_at,
    source_span_id, subject_text, object_text, risk_level,
    requires_scholar_review
) ON knowledge_relations TO surau_extraction_writer;
GRANT UPDATE (
    subject_entity_id, predicate, object_entity_id, object_literal,
    evidence_mention_id, evidence_quote, certainty, attributes, source_span_id,
    subject_text, object_text, risk_level, requires_scholar_review
) ON knowledge_relations TO surau_extraction_writer;

GRANT INSERT (
    id, run_id, subject_entity_id, claim_type, claim_text_ar, claim_text_id,
    evidence_mention_id, evidence_quote, attributes, created_at, source_span_id,
    subject_text, object_text, predicate, risk_level, certainty,
    requires_scholar_review
) ON knowledge_claims TO surau_extraction_writer;
GRANT UPDATE (
    subject_entity_id, claim_type, claim_text_ar, claim_text_id,
    evidence_mention_id, evidence_quote, attributes, source_span_id,
    subject_text, object_text, predicate, risk_level, certainty,
    requires_scholar_review
) ON knowledge_claims TO surau_extraction_writer;

GRANT INSERT (entity_id, taxonomy_id, source_mention_id, created_at)
    ON knowledge_entity_taxonomy_links TO surau_extraction_writer;
GRANT UPDATE (source_mention_id)
    ON knowledge_entity_taxonomy_links TO surau_extraction_writer;

GRANT INSERT (
    id, source_entity_id, target_entity_id, link_type, score, source,
    reason, reviewer_notes, created_at
) ON knowledge_entity_links TO surau_extraction_writer;
GRANT UPDATE (score, source, reason)
    ON knowledge_entity_links TO surau_extraction_writer;

-- Defense in depth: even if a future migration accidentally widens a content
-- column grant, extraction may only create/update rows in its machine-pending
-- state and can never rewrite a human-reviewed row.
CREATE OR REPLACE FUNCTION extraction_pending_only_guard() RETURNS TRIGGER AS $$
DECLARE
    status_column TEXT := TG_ARGV[0];
    allowed_insert_status TEXT := TG_ARGV[1];
    old_status TEXT;
    new_status TEXT;
BEGIN
    -- Superusers report pg_has_role(..., 'MEMBER') for every role. Exclude
    -- them explicitly so operator recovery remains possible; a real pipeline
    -- LOGIN is non-superuser and reaches the guard through its one membership.
    IF NOT pg_has_role(current_user, 'surau_extraction_writer', 'MEMBER')
       OR EXISTS (
           SELECT 1 FROM pg_roles
           WHERE rolname = current_user AND rolsuper
       ) THEN
        RETURN NEW;
    END IF;

    new_status := to_jsonb(NEW) ->> status_column;
    IF TG_OP = 'INSERT' THEN
        IF new_status IS DISTINCT FROM allowed_insert_status THEN
            RAISE EXCEPTION 'extraction role may only insert % rows', allowed_insert_status
                USING ERRCODE = 'insufficient_privilege';
        END IF;
        RETURN NEW;
    END IF;

    old_status := to_jsonb(OLD) ->> status_column;
    IF old_status IS DISTINCT FROM allowed_insert_status
       OR new_status IS DISTINCT FROM old_status THEN
        RAISE EXCEPTION 'extraction role may not change reviewed rows or review status'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_extraction_pending_mentions
    BEFORE INSERT OR UPDATE ON knowledge_mentions
    FOR EACH ROW EXECUTE FUNCTION extraction_pending_only_guard('review_status', 'pending');
CREATE TRIGGER trg_extraction_pending_entities
    BEFORE INSERT OR UPDATE ON knowledge_entities
    FOR EACH ROW EXECUTE FUNCTION extraction_pending_only_guard('review_status', 'pending');
CREATE TRIGGER trg_extraction_pending_labels
    BEFORE INSERT OR UPDATE ON knowledge_entity_labels
    FOR EACH ROW EXECUTE FUNCTION extraction_pending_only_guard('review_status', 'pending');
CREATE TRIGGER trg_extraction_pending_aliases
    BEFORE INSERT OR UPDATE ON knowledge_entity_aliases
    FOR EACH ROW EXECUTE FUNCTION extraction_pending_only_guard('review_status', 'pending');
CREATE TRIGGER trg_extraction_pending_candidates
    BEFORE INSERT OR UPDATE ON knowledge_entity_candidates
    FOR EACH ROW EXECUTE FUNCTION extraction_pending_only_guard('review_status', 'pending');
CREATE TRIGGER trg_extraction_pending_relations
    BEFORE INSERT OR UPDATE ON knowledge_relations
    FOR EACH ROW EXECUTE FUNCTION extraction_pending_only_guard('review_status', 'pending');
CREATE TRIGGER trg_extraction_pending_claims
    BEFORE INSERT OR UPDATE ON knowledge_claims
    FOR EACH ROW EXECUTE FUNCTION extraction_pending_only_guard('status', 'pending');
CREATE TRIGGER trg_extraction_pending_taxonomy_links
    BEFORE INSERT OR UPDATE ON knowledge_entity_taxonomy_links
    FOR EACH ROW EXECUTE FUNCTION extraction_pending_only_guard('review_status', 'pending');
CREATE TRIGGER trg_extraction_pending_entity_links
    BEFORE INSERT OR UPDATE ON knowledge_entity_links
    FOR EACH ROW EXECUTE FUNCTION extraction_pending_only_guard('decision_status', 'pending');
CREATE TRIGGER trg_extraction_rejection_status
    BEFORE INSERT OR UPDATE ON knowledge_extraction_rejections
    FOR EACH ROW EXECUTE FUNCTION extraction_pending_only_guard('review_status', 'rejected');

-- Importer is broad across corpus ingestion, but has no authority over users,
-- auth, email, personal data, or destructive DELETE/TRUNCATE operations.
GRANT SELECT ON
    source_releases, import_runs, categories, authors, books, book_pages,
    book_headings, book_heading_ranges, book_import_removal_stages,
    book_license_audits, book_publications, book_production_projects,
    book_metadata_edits, book_page_edits, section_translations,
    book_heading_summaries, section_audio, book_metadata_translations,
    author_translations, category_translations, generation_runs,
    citable_units, citable_unit_lineage, citable_unit_catalog_queue,
    cross_references, quran_book_references, quran_cross_reference_bridge,
    quran_surahs, quran_ayahs, quran_script_sources, quran_surah_infos,
    quran_import_runs, quran_translation_sources, quran_ayah_translations,
    quran_transliteration_sources, quran_ayah_transliterations,
    quran_recitations, quran_audio_tracks, quran_audio_segments,
    quran_surah_editorial, quran_ayah_editorial, quran_editorial_revisions,
    quran_surah_slug_registry, knowledge_mentions, public_book_publications
TO surau_importer;

GRANT INSERT, UPDATE ON
    source_releases, import_runs, categories, authors, books, book_pages,
    book_headings, book_heading_ranges, book_import_removal_stages,
    section_translations, book_heading_summaries, section_audio,
    book_metadata_translations, author_translations, category_translations,
    citable_units, citable_unit_lineage, citable_unit_catalog_queue,
    cross_references, quran_book_references, quran_cross_reference_bridge,
    quran_surahs, quran_ayahs, quran_script_sources, quran_surah_infos,
    quran_import_runs, quran_translation_sources, quran_ayah_translations,
    quran_transliteration_sources, quran_ayah_transliterations,
    quran_recitations, quran_audio_tracks, quran_audio_segments,
    quran_surah_editorial, quran_ayah_editorial, quran_editorial_revisions,
    quran_surah_slug_registry
TO surau_importer;
GRANT INSERT ON generation_runs, book_license_audits TO surau_importer;
