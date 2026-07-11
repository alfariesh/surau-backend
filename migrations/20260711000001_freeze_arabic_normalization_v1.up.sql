-- B-5: version every persisted search-key derivative without guessing the
-- provenance of legacy rows. Columns remain nullable during the expand phase;
-- writers and the trigger require text+version atomically for new/changed
-- derivatives, while untouched legacy rows remain readable with NULL version.

ALTER TABLE authors
    ADD COLUMN IF NOT EXISTS name_search_normalization_version INTEGER;
ALTER TABLE quran_book_references
    ADD COLUMN IF NOT EXISTS normalization_version INTEGER;
ALTER TABLE quran_cross_reference_bridge
    ADD COLUMN IF NOT EXISTS normalization_version INTEGER;
ALTER TABLE knowledge_mentions
    ADD COLUMN IF NOT EXISTS normalization_version INTEGER;
ALTER TABLE knowledge_entities
    ADD COLUMN IF NOT EXISTS normalization_version INTEGER;
ALTER TABLE knowledge_entity_aliases
    ADD COLUMN IF NOT EXISTS normalization_version INTEGER;

-- Recovery-safe constraint creation: a deploy interrupted after any one
-- statement can be repaired and replayed without colliding with the objects
-- that already exist. A non-NULL version always requires a real derivative;
-- the inverse is intentionally not constrained so untouched legacy text may
-- remain unversioned until a proven backfill processes it.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'authors'::regclass
          AND conname = 'authors_name_search_normalization_version_check'
    ) THEN
        ALTER TABLE authors
            ADD CONSTRAINT authors_name_search_normalization_version_check
            CHECK (
                name_search_normalization_version IS NULL
                OR (name_search IS NOT NULL AND name_search_normalization_version >= 1)
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'quran_book_references'::regclass
          AND conname = 'quran_book_references_normalization_version_check'
    ) THEN
        ALTER TABLE quran_book_references
            ADD CONSTRAINT quran_book_references_normalization_version_check
            CHECK (
                normalization_version IS NULL
                OR (normalized_text IS NOT NULL AND normalization_version >= 1)
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'quran_cross_reference_bridge'::regclass
          AND conname = 'quran_cross_reference_bridge_normalization_version_check'
    ) THEN
        ALTER TABLE quran_cross_reference_bridge
            ADD CONSTRAINT quran_cross_reference_bridge_normalization_version_check
            CHECK (
                normalization_version IS NULL
                OR (normalized_text IS NOT NULL AND normalization_version >= 1)
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'knowledge_mentions'::regclass
          AND conname = 'knowledge_mentions_normalization_version_check'
    ) THEN
        ALTER TABLE knowledge_mentions
            ADD CONSTRAINT knowledge_mentions_normalization_version_check
            CHECK (
                normalization_version IS NULL
                OR (normalized_text IS NOT NULL AND normalization_version >= 1)
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'knowledge_entities'::regclass
          AND conname = 'knowledge_entities_normalization_version_check'
    ) THEN
        ALTER TABLE knowledge_entities
            ADD CONSTRAINT knowledge_entities_normalization_version_check
            CHECK (
                normalization_version IS NULL
                OR (normalized_name_ar IS NOT NULL AND normalization_version >= 1)
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'knowledge_entity_aliases'::regclass
          AND conname = 'knowledge_entity_aliases_normalization_version_check'
    ) THEN
        ALTER TABLE knowledge_entity_aliases
            ADD CONSTRAINT knowledge_entity_aliases_normalization_version_check
            CHECK (
                normalization_version IS NULL
                OR (normalized_alias IS NOT NULL AND normalization_version >= 1)
            ) NOT VALID;
    END IF;
END;
$$;

-- Explicit preflight keeps deploy failures actionable and preserves the
-- F1-H order: expand -> NOT VALID -> inspect legacy rows -> VALIDATE.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM authors
        WHERE name_search_normalization_version IS NOT NULL
          AND (name_search IS NULL OR name_search_normalization_version < 1)
    ) THEN
        RAISE EXCEPTION 'authors contains an invalid search-key normalization version pair';
    END IF;

    IF EXISTS (
        SELECT 1 FROM quran_book_references
        WHERE normalization_version IS NOT NULL
          AND (normalized_text IS NULL OR normalization_version < 1)
    ) THEN
        RAISE EXCEPTION 'quran_book_references contains an invalid normalization version pair';
    END IF;

    IF EXISTS (
        SELECT 1 FROM quran_cross_reference_bridge
        WHERE normalization_version IS NOT NULL
          AND (normalized_text IS NULL OR normalization_version < 1)
    ) THEN
        RAISE EXCEPTION 'quran_cross_reference_bridge contains an invalid normalization version pair';
    END IF;

    IF EXISTS (
        SELECT 1 FROM knowledge_mentions
        WHERE normalization_version IS NOT NULL
          AND (normalized_text IS NULL OR normalization_version < 1)
    ) THEN
        RAISE EXCEPTION 'knowledge_mentions contains an invalid normalization version pair';
    END IF;

    IF EXISTS (
        SELECT 1 FROM knowledge_entities
        WHERE normalization_version IS NOT NULL
          AND (normalized_name_ar IS NULL OR normalization_version < 1)
    ) THEN
        RAISE EXCEPTION 'knowledge_entities contains an invalid normalization version pair';
    END IF;

    IF EXISTS (
        SELECT 1 FROM knowledge_entity_aliases
        WHERE normalization_version IS NOT NULL
          AND (normalized_alias IS NULL OR normalization_version < 1)
    ) THEN
        RAISE EXCEPTION 'knowledge_entity_aliases contains an invalid normalization version pair';
    END IF;
END;
$$;

ALTER TABLE authors VALIDATE CONSTRAINT authors_name_search_normalization_version_check;
ALTER TABLE quran_book_references VALIDATE CONSTRAINT quran_book_references_normalization_version_check;
ALTER TABLE quran_cross_reference_bridge VALIDATE CONSTRAINT quran_cross_reference_bridge_normalization_version_check;
ALTER TABLE knowledge_mentions VALIDATE CONSTRAINT knowledge_mentions_normalization_version_check;
ALTER TABLE knowledge_entities VALIDATE CONSTRAINT knowledge_entities_normalization_version_check;
ALTER TABLE knowledge_entity_aliases VALIDATE CONSTRAINT knowledge_entity_aliases_normalization_version_check;

CREATE OR REPLACE FUNCTION enforce_derived_text_normalization_version()
RETURNS TRIGGER AS $$
DECLARE
    old_text TEXT;
    new_text TEXT;
    old_version TEXT;
    new_version TEXT;
BEGIN
    new_text := to_jsonb(NEW) ->> TG_ARGV[0];
    new_version := to_jsonb(NEW) ->> TG_ARGV[1];

    IF TG_OP = 'UPDATE' THEN
        old_text := to_jsonb(OLD) ->> TG_ARGV[0];
        old_version := to_jsonb(OLD) ->> TG_ARGV[1];

        IF old_text IS NOT DISTINCT FROM new_text
           AND old_version IS NOT DISTINCT FROM new_version THEN
            RETURN NEW;
        END IF;
    END IF;

    IF new_text IS NULL AND new_version IS NOT NULL THEN
        RAISE EXCEPTION '% requires % to be NULL when % is NULL',
            TG_TABLE_NAME, TG_ARGV[1], TG_ARGV[0]
            USING ERRCODE = 'check_violation';
    END IF;

    IF new_text IS NOT NULL AND new_version IS NULL THEN
        RAISE EXCEPTION '% requires % when writing %',
            TG_TABLE_NAME, TG_ARGV[1], TG_ARGV[0]
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_authors_name_search_normalization_version ON authors;
CREATE TRIGGER trg_authors_name_search_normalization_version
    BEFORE INSERT OR UPDATE OF name_search, name_search_normalization_version ON authors
    FOR EACH ROW EXECUTE FUNCTION enforce_derived_text_normalization_version(
        'name_search', 'name_search_normalization_version'
    );
DROP TRIGGER IF EXISTS trg_quran_book_references_normalization_version ON quran_book_references;
CREATE TRIGGER trg_quran_book_references_normalization_version
    BEFORE INSERT OR UPDATE OF normalized_text, normalization_version ON quran_book_references
    FOR EACH ROW EXECUTE FUNCTION enforce_derived_text_normalization_version(
        'normalized_text', 'normalization_version'
    );
DROP TRIGGER IF EXISTS trg_quran_cross_reference_bridge_normalization_version ON quran_cross_reference_bridge;
CREATE TRIGGER trg_quran_cross_reference_bridge_normalization_version
    BEFORE INSERT OR UPDATE OF normalized_text, normalization_version ON quran_cross_reference_bridge
    FOR EACH ROW EXECUTE FUNCTION enforce_derived_text_normalization_version(
        'normalized_text', 'normalization_version'
    );
DROP TRIGGER IF EXISTS trg_knowledge_mentions_normalization_version ON knowledge_mentions;
CREATE TRIGGER trg_knowledge_mentions_normalization_version
    BEFORE INSERT OR UPDATE OF normalized_text, normalization_version ON knowledge_mentions
    FOR EACH ROW EXECUTE FUNCTION enforce_derived_text_normalization_version(
        'normalized_text', 'normalization_version'
    );
DROP TRIGGER IF EXISTS trg_knowledge_entities_normalization_version ON knowledge_entities;
CREATE TRIGGER trg_knowledge_entities_normalization_version
    BEFORE INSERT OR UPDATE OF normalized_name_ar, normalization_version ON knowledge_entities
    FOR EACH ROW EXECUTE FUNCTION enforce_derived_text_normalization_version(
        'normalized_name_ar', 'normalization_version'
    );
DROP TRIGGER IF EXISTS trg_knowledge_entity_aliases_normalization_version ON knowledge_entity_aliases;
CREATE TRIGGER trg_knowledge_entity_aliases_normalization_version
    BEFORE INSERT OR UPDATE OF normalized_alias, normalization_version ON knowledge_entity_aliases
    FOR EACH ROW EXECUTE FUNCTION enforce_derived_text_normalization_version(
        'normalized_alias', 'normalization_version'
    );
