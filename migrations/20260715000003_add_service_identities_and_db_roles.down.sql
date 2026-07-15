DROP TRIGGER IF EXISTS trg_extraction_rejection_status ON knowledge_extraction_rejections;
DROP TRIGGER IF EXISTS trg_extraction_pending_entity_links ON knowledge_entity_links;
DROP TRIGGER IF EXISTS trg_extraction_pending_taxonomy_links ON knowledge_entity_taxonomy_links;
DROP TRIGGER IF EXISTS trg_extraction_pending_claims ON knowledge_claims;
DROP TRIGGER IF EXISTS trg_extraction_pending_relations ON knowledge_relations;
DROP TRIGGER IF EXISTS trg_extraction_pending_candidates ON knowledge_entity_candidates;
DROP TRIGGER IF EXISTS trg_extraction_pending_aliases ON knowledge_entity_aliases;
DROP TRIGGER IF EXISTS trg_extraction_pending_labels ON knowledge_entity_labels;
DROP TRIGGER IF EXISTS trg_extraction_pending_entities ON knowledge_entities;
DROP TRIGGER IF EXISTS trg_extraction_pending_mentions ON knowledge_mentions;
DROP FUNCTION IF EXISTS extraction_pending_only_guard();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'surau_collab_store') THEN
        DROP OWNED BY surau_collab_store;
        DROP ROLE surau_collab_store;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'surau_importer') THEN
        DROP OWNED BY surau_importer;
        DROP ROLE surau_importer;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'surau_extraction_writer') THEN
        DROP OWNED BY surau_extraction_writer;
        DROP ROLE surau_extraction_writer;
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS trg_service_token_revocation ON service_tokens;
DROP FUNCTION IF EXISTS service_token_revocation_guard();
DROP TRIGGER IF EXISTS trg_service_principal_immutable ON service_principals;
DROP FUNCTION IF EXISTS service_principal_immutable_guard();

DROP TABLE IF EXISTS service_identity_events;
DROP TABLE IF EXISTS service_request_audit_logs;
DROP TABLE IF EXISTS service_tokens;
DROP TABLE IF EXISTS service_principal_scopes;
DROP TABLE IF EXISTS service_principals;
