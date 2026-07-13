CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_knowledge_mentions_unit_binding
    ON knowledge_mentions (unit_id, unit_binding_status)
    WHERE unit_id IS NOT NULL;
