DROP TRIGGER IF EXISTS trg_knowledge_entity_aliases_normalization_version ON knowledge_entity_aliases;
DROP TRIGGER IF EXISTS trg_knowledge_entities_normalization_version ON knowledge_entities;
DROP TRIGGER IF EXISTS trg_knowledge_mentions_normalization_version ON knowledge_mentions;
DROP TRIGGER IF EXISTS trg_quran_cross_reference_bridge_normalization_version ON quran_cross_reference_bridge;
DROP TRIGGER IF EXISTS trg_quran_book_references_normalization_version ON quran_book_references;
DROP TRIGGER IF EXISTS trg_authors_name_search_normalization_version ON authors;

DROP FUNCTION IF EXISTS enforce_derived_text_normalization_version();

ALTER TABLE knowledge_entity_aliases
    DROP CONSTRAINT IF EXISTS knowledge_entity_aliases_normalization_version_check,
    DROP COLUMN IF EXISTS normalization_version;
ALTER TABLE knowledge_entities
    DROP CONSTRAINT IF EXISTS knowledge_entities_normalization_version_check,
    DROP COLUMN IF EXISTS normalization_version;
ALTER TABLE knowledge_mentions
    DROP CONSTRAINT IF EXISTS knowledge_mentions_normalization_version_check,
    DROP COLUMN IF EXISTS normalization_version;
ALTER TABLE quran_cross_reference_bridge
    DROP CONSTRAINT IF EXISTS quran_cross_reference_bridge_normalization_version_check,
    DROP COLUMN IF EXISTS normalization_version;
ALTER TABLE quran_book_references
    DROP CONSTRAINT IF EXISTS quran_book_references_normalization_version_check,
    DROP COLUMN IF EXISTS normalization_version;
ALTER TABLE authors
    DROP CONSTRAINT IF EXISTS authors_name_search_normalization_version_check,
    DROP COLUMN IF EXISTS name_search_normalization_version;
