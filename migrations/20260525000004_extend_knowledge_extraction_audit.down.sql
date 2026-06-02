DROP INDEX IF EXISTS idx_knowledge_entity_links_target;
DROP INDEX IF EXISTS idx_knowledge_entity_links_source;
DROP INDEX IF EXISTS idx_knowledge_extraction_rejections_run;
DROP INDEX IF EXISTS idx_knowledge_source_spans_object_unique;
DROP INDEX IF EXISTS idx_knowledge_source_spans_source;
DROP INDEX IF EXISTS idx_knowledge_extraction_chunks_run_doc;
DROP INDEX IF EXISTS idx_knowledge_extraction_documents_run;
DROP INDEX IF EXISTS idx_knowledge_prompt_versions_task;

ALTER TABLE knowledge_claims DROP CONSTRAINT IF EXISTS knowledge_claims_source_span_fk;
ALTER TABLE knowledge_relations DROP CONSTRAINT IF EXISTS knowledge_relations_source_span_fk;
ALTER TABLE knowledge_mentions DROP CONSTRAINT IF EXISTS knowledge_mentions_source_span_fk;

ALTER TABLE knowledge_claims DROP CONSTRAINT IF EXISTS knowledge_claims_certainty_check;
ALTER TABLE knowledge_claims DROP CONSTRAINT IF EXISTS knowledge_claims_risk_level_check;
ALTER TABLE knowledge_relations DROP CONSTRAINT IF EXISTS knowledge_relations_risk_level_check;

ALTER TABLE knowledge_entities DROP CONSTRAINT IF EXISTS knowledge_entities_type_check;
ALTER TABLE knowledge_entities
    ADD CONSTRAINT knowledge_entities_type_check
    CHECK (entity_type IN (
        'person',
        'place',
        'book_title',
        'group',
        'institution',
        'concept',
        'citation',
        'quote'
    ));

ALTER TABLE knowledge_claims
    DROP COLUMN IF EXISTS requires_scholar_review,
    DROP COLUMN IF EXISTS certainty,
    DROP COLUMN IF EXISTS risk_level,
    DROP COLUMN IF EXISTS predicate,
    DROP COLUMN IF EXISTS object_text,
    DROP COLUMN IF EXISTS subject_text,
    DROP COLUMN IF EXISTS source_span_id;

ALTER TABLE knowledge_relations
    DROP COLUMN IF EXISTS requires_scholar_review,
    DROP COLUMN IF EXISTS risk_level,
    DROP COLUMN IF EXISTS object_text,
    DROP COLUMN IF EXISTS subject_text,
    DROP COLUMN IF EXISTS source_span_id;

ALTER TABLE knowledge_mentions
    DROP COLUMN IF EXISTS pass_index,
    DROP COLUMN IF EXISTS group_index,
    DROP COLUMN IF EXISTS extraction_index,
    DROP COLUMN IF EXISTS token_end,
    DROP COLUMN IF EXISTS token_start,
    DROP COLUMN IF EXISTS source_span_id;

DROP TABLE IF EXISTS knowledge_entity_links;
DROP TABLE IF EXISTS knowledge_entity_taxonomy_links;
DROP TABLE IF EXISTS knowledge_taxonomies;
DROP TABLE IF EXISTS knowledge_extraction_rejections;
DROP TABLE IF EXISTS knowledge_source_spans;
DROP TABLE IF EXISTS knowledge_extraction_chunks;
DROP TABLE IF EXISTS knowledge_extraction_documents;
DROP TABLE IF EXISTS knowledge_prompt_versions;
