-- B-6 generation-run identity. Every newly persisted LLM output points at one
-- immutable descriptor, so model/prompt/run attribution cannot drift after a
-- human review. Existing translation assets deliberately remain
-- legacy_unknown; no provenance is invented for old rows.

CREATE TABLE IF NOT EXISTS generation_runs (
    id UUID PRIMARY KEY,
    task_name TEXT NOT NULL,
    model_id TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    provider TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT generation_runs_task_name_check CHECK (btrim(task_name) <> ''),
    CONSTRAINT generation_runs_model_id_check CHECK (btrim(model_id) <> ''),
    CONSTRAINT generation_runs_prompt_version_check CHECK (btrim(prompt_version) <> ''),
    CONSTRAINT generation_runs_provider_check CHECK (provider IS NULL OR btrim(provider) <> ''),
    CONSTRAINT generation_runs_metadata_object_check CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX IF NOT EXISTS idx_generation_runs_task_created
    ON generation_runs (task_name, created_at DESC, id);

-- PostgreSQL's UUID input accepts every valid UUID version (including v7) and
-- canonicalizes case. Legacy JSON must be parsed without letting one malformed
-- value abort the diagnostic query before we can report the preflight error.
CREATE OR REPLACE FUNCTION generation_run_safe_uuid(raw_value TEXT) RETURNS UUID AS $$
BEGIN
    RETURN raw_value::UUID;
EXCEPTION
    WHEN invalid_text_representation THEN
        RETURN NULL;
END;
$$ LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE;

CREATE OR REPLACE FUNCTION generation_runs_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'generation_runs rows are immutable; register a new run instead'
        USING ERRCODE = 'object_not_in_prerequisite_state';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_generation_runs_immutable ON generation_runs;
CREATE TRIGGER trg_generation_runs_immutable
    BEFORE UPDATE OR DELETE ON generation_runs
    FOR EACH ROW EXECUTE FUNCTION generation_runs_immutable_guard();

-- Generalize the existing extraction-run ledger without changing its UUIDs.
-- The original started_at is retained as the registry creation time and its
-- extraction-only settings remain available as structured metadata.
INSERT INTO generation_runs (
    id, task_name, model_id, prompt_version, provider, metadata, created_at
)
SELECT id,
       task_name,
       model_id,
       prompt_version,
       NULLIF(btrim(provider), ''),
       jsonb_build_object(
           'source', 'knowledge_extraction_runs',
           'parameters', parameters,
           'source_scope', source_scope
       ),
       started_at
FROM knowledge_extraction_runs
ON CONFLICT (id) DO NOTHING;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'knowledge_extraction_runs_generation_run_fk'
          AND conrelid = 'knowledge_extraction_runs'::regclass
    ) THEN
        ALTER TABLE knowledge_extraction_runs
            ADD CONSTRAINT knowledge_extraction_runs_generation_run_fk
            FOREIGN KEY (id) REFERENCES generation_runs (id) ON DELETE RESTRICT
            NOT VALID;
    END IF;
END;
$$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM knowledge_extraction_runs er
        LEFT JOIN generation_runs gr ON gr.id = er.id
        WHERE gr.id IS NULL
    ) THEN
        RAISE EXCEPTION 'knowledge extraction run is missing its generation registry row';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM knowledge_extraction_runs er
        JOIN generation_runs gr ON gr.id = er.id
        WHERE gr.task_name IS DISTINCT FROM er.task_name
           OR gr.model_id IS DISTINCT FROM er.model_id
           OR gr.prompt_version IS DISTINCT FROM er.prompt_version
           OR gr.provider IS DISTINCT FROM NULLIF(btrim(er.provider), '')
    ) THEN
        RAISE EXCEPTION 'knowledge extraction generation tuple conflicts with registry';
    END IF;
END;
$$;

ALTER TABLE knowledge_extraction_runs
    VALIDATE CONSTRAINT knowledge_extraction_runs_generation_run_fk;

CREATE OR REPLACE FUNCTION knowledge_extraction_generation_identity_guard() RETURNS TRIGGER AS $$
DECLARE
    registered_task TEXT;
    registered_model TEXT;
    registered_prompt TEXT;
    registered_provider TEXT;
BEGIN
    SELECT task_name, model_id, prompt_version, provider
    INTO registered_task, registered_model, registered_prompt, registered_provider
    FROM generation_runs
    WHERE id = NEW.id;

    IF NOT FOUND
       OR registered_task IS DISTINCT FROM NEW.task_name
       OR registered_model IS DISTINCT FROM NEW.model_id
       OR registered_prompt IS DISTINCT FROM NEW.prompt_version
       OR registered_provider IS DISTINCT FROM NULLIF(btrim(NEW.provider), '') THEN
        RAISE EXCEPTION 'knowledge extraction generation tuple conflicts with registry'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_knowledge_extraction_generation_identity ON knowledge_extraction_runs;
CREATE TRIGGER trg_knowledge_extraction_generation_identity
    BEFORE INSERT OR UPDATE ON knowledge_extraction_runs
    FOR EACH ROW EXECUTE FUNCTION knowledge_extraction_generation_identity_guard();

-- Cross-Reference machine rows already carried a complete tuple in
-- method_detail. Promote that real tuple into the registry rather than making
-- up attribution. Invalid/conflicting legacy tuples abort the migration.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM cross_references
        WHERE method = 'machine'
          AND generation_run_safe_uuid(method_detail ->> 'run_id') IS NULL
    ) THEN
        RAISE EXCEPTION 'machine cross-reference has an invalid generation run UUID';
    END IF;

    IF EXISTS (
        SELECT generation_run_safe_uuid(method_detail ->> 'run_id')
        FROM cross_references
        WHERE method = 'machine'
        GROUP BY generation_run_safe_uuid(method_detail ->> 'run_id')
        HAVING count(DISTINCT ROW(
            method_detail ->> 'model_id',
            method_detail ->> 'prompt_version'
        )) > 1
    ) THEN
        RAISE EXCEPTION 'machine cross-references disagree on one generation run descriptor';
    END IF;
END;
$$;

INSERT INTO generation_runs (
    id, task_name, model_id, prompt_version, metadata, created_at
)
SELECT DISTINCT ON (generation_run_safe_uuid(method_detail ->> 'run_id'))
       generation_run_safe_uuid(method_detail ->> 'run_id'),
       'cross-reference',
       method_detail ->> 'model_id',
       method_detail ->> 'prompt_version',
       jsonb_build_object('source', 'cross_references'),
       created_at
FROM cross_references
WHERE method = 'machine'
ORDER BY generation_run_safe_uuid(method_detail ->> 'run_id'), created_at, id
ON CONFLICT (id) DO NOTHING;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM cross_references cr
        JOIN generation_runs gr
          ON gr.id = generation_run_safe_uuid(cr.method_detail ->> 'run_id')
        WHERE cr.method = 'machine'
          AND (
              gr.task_name IS DISTINCT FROM 'cross-reference'
              OR gr.model_id IS DISTINCT FROM cr.method_detail ->> 'model_id'
              OR gr.prompt_version IS DISTINCT FROM cr.method_detail ->> 'prompt_version'
          )
    ) THEN
        RAISE EXCEPTION 'machine cross-reference conflicts with an existing generation run';
    END IF;
END;
$$;

ALTER TABLE cross_references
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;

DO $$
BEGIN
    PERFORM set_config('surau.cross_reference_writer', 'cross-reference-service', true);
    UPDATE cross_references
    SET generation_run_id = generation_run_safe_uuid(method_detail ->> 'run_id')
    WHERE method = 'machine' AND generation_run_id IS NULL;
END;
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'cross_references_generation_run_fk'
          AND conrelid = 'cross_references'::regclass
    ) THEN
        ALTER TABLE cross_references
            ADD CONSTRAINT cross_references_generation_run_fk
            FOREIGN KEY (generation_run_id) REFERENCES generation_runs (id) ON DELETE RESTRICT
            NOT VALID;
    END IF;
END;
$$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM cross_references cr
        LEFT JOIN generation_runs gr ON gr.id = cr.generation_run_id
        WHERE cr.generation_run_id IS NOT NULL AND gr.id IS NULL
    ) THEN
        RAISE EXCEPTION 'cross-reference generation run foreign-key preflight failed';
    END IF;
END;
$$;

ALTER TABLE cross_references
    VALIDATE CONSTRAINT cross_references_generation_run_fk;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'cross_references_generation_identity_check'
          AND conrelid = 'cross_references'::regclass
    ) THEN
        ALTER TABLE cross_references
            ADD CONSTRAINT cross_references_generation_identity_check CHECK (
                (method = 'machine') = (generation_run_id IS NOT NULL)
                AND (
                    method <> 'machine'
                    OR (
                        generation_run_safe_uuid(method_detail ->> 'run_id') IS NOT NULL
                        AND generation_run_safe_uuid(method_detail ->> 'run_id') = generation_run_id
                    )
                )
            ) NOT VALID;
    END IF;
END;
$$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM cross_references
        WHERE NOT (
            (method = 'machine') = (generation_run_id IS NOT NULL)
            AND (
                method <> 'machine'
                OR (
                    generation_run_safe_uuid(method_detail ->> 'run_id') IS NOT NULL
                    AND generation_run_safe_uuid(method_detail ->> 'run_id') = generation_run_id
                )
            )
        )
    ) THEN
        RAISE EXCEPTION 'cross-reference generation identity preflight failed';
    END IF;
END;
$$;

ALTER TABLE cross_references
    VALIDATE CONSTRAINT cross_references_generation_identity_check;

-- Citable Units can predate the generation registry. NOT VALID preserves an
-- untouched legacy machine row, while PostgreSQL still enforces the identity
-- rule for every new or updated row. Source/editorial units can never borrow a
-- machine run.
ALTER TABLE citable_units
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'citable_units_generation_run_fk'
          AND conrelid = 'citable_units'::regclass
    ) THEN
        ALTER TABLE citable_units
            ADD CONSTRAINT citable_units_generation_run_fk
            FOREIGN KEY (generation_run_id) REFERENCES generation_runs (id) ON DELETE RESTRICT
            NOT VALID;
    END IF;
END;
$$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM citable_units cu
        LEFT JOIN generation_runs gr ON gr.id = cu.generation_run_id
        WHERE cu.generation_run_id IS NOT NULL AND gr.id IS NULL
    ) THEN
        RAISE EXCEPTION 'citable unit generation run foreign-key preflight failed';
    END IF;
END;
$$;

ALTER TABLE citable_units
    VALIDATE CONSTRAINT citable_units_generation_run_fk;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'citable_units_generation_identity_check'
          AND conrelid = 'citable_units'::regclass
    ) THEN
        ALTER TABLE citable_units
            ADD CONSTRAINT citable_units_generation_identity_check CHECK (
                (provenance_class = 'machine') = (generation_run_id IS NOT NULL)
            ) NOT VALID;
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION citable_unit_generation_identity_guard() RETURNS TRIGGER AS $$
DECLARE
    registered_model TEXT;
    registered_prompt TEXT;
BEGIN
    IF TG_OP = 'UPDATE'
       AND NEW.provenance_class IS DISTINCT FROM OLD.provenance_class THEN
        RAISE EXCEPTION 'citable unit provenance_class is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'UPDATE'
       AND NEW.generation_run_id IS DISTINCT FROM OLD.generation_run_id THEN
        RAISE EXCEPTION 'citable unit generation_run_id is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.provenance_class = 'machine' AND NEW.generation_run_id IS NOT NULL THEN
        SELECT model_id, prompt_version
        INTO registered_model, registered_prompt
        FROM generation_runs
        WHERE id = NEW.generation_run_id;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'machine citable unit requires a registered generation run'
                USING ERRCODE = 'foreign_key_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_citable_units_generation_identity ON citable_units;
CREATE TRIGGER trg_citable_units_generation_identity
    BEFORE INSERT OR UPDATE ON citable_units
    FOR EACH ROW EXECUTE FUNCTION citable_unit_generation_identity_guard();

CREATE OR REPLACE FUNCTION cross_reference_generation_identity_guard() RETURNS TRIGGER AS $$
DECLARE
    registered_model TEXT;
    registered_prompt TEXT;
BEGIN
    IF TG_OP = 'UPDATE'
       AND NEW.generation_run_id IS DISTINCT FROM OLD.generation_run_id THEN
        RAISE EXCEPTION 'cross-reference generation_run_id is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.method = 'machine' THEN
        SELECT model_id, prompt_version
        INTO registered_model, registered_prompt
        FROM generation_runs
        WHERE id = NEW.generation_run_id;

        IF NOT FOUND
           OR registered_model IS DISTINCT FROM NEW.method_detail ->> 'model_id'
           OR registered_prompt IS DISTINCT FROM NEW.method_detail ->> 'prompt_version' THEN
            RAISE EXCEPTION 'machine cross-reference generation tuple conflicts with registry'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_cross_references_generation_identity ON cross_references;
CREATE TRIGGER trg_cross_references_generation_identity
    BEFORE INSERT OR UPDATE ON cross_references
    FOR EACH ROW EXECUTE FUNCTION cross_reference_generation_identity_guard();

-- Translation, summary, and catalog output tables. Existing rows are marked
-- legacy_unknown before NOT NULL is enabled. There is intentionally no
-- default: every new writer must choose source/editorial/machine explicitly.
ALTER TABLE book_metadata_translations
    ADD COLUMN IF NOT EXISTS provenance_class TEXT NOT NULL DEFAULT 'legacy_unknown',
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;
ALTER TABLE author_translations
    ADD COLUMN IF NOT EXISTS provenance_class TEXT NOT NULL DEFAULT 'legacy_unknown',
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;
ALTER TABLE category_translations
    ADD COLUMN IF NOT EXISTS provenance_class TEXT NOT NULL DEFAULT 'legacy_unknown',
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;
ALTER TABLE section_translations
    ADD COLUMN IF NOT EXISTS provenance_class TEXT NOT NULL DEFAULT 'legacy_unknown',
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;
ALTER TABLE book_heading_summaries
    ADD COLUMN IF NOT EXISTS provenance_class TEXT NOT NULL DEFAULT 'legacy_unknown',
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;

ALTER TABLE book_metadata_translation_edits
    ADD COLUMN IF NOT EXISTS provenance_class TEXT NOT NULL DEFAULT 'legacy_unknown',
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;
ALTER TABLE author_translation_edits
    ADD COLUMN IF NOT EXISTS provenance_class TEXT NOT NULL DEFAULT 'legacy_unknown',
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;
ALTER TABLE category_translation_edits
    ADD COLUMN IF NOT EXISTS provenance_class TEXT NOT NULL DEFAULT 'legacy_unknown',
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;
ALTER TABLE section_translation_edits
    ADD COLUMN IF NOT EXISTS provenance_class TEXT NOT NULL DEFAULT 'legacy_unknown',
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;
ALTER TABLE heading_summary_edits
    ADD COLUMN IF NOT EXISTS provenance_class TEXT NOT NULL DEFAULT 'legacy_unknown',
    ADD COLUMN IF NOT EXISTS generation_run_id UUID;

-- Install every child FK using the expand/preflight/validate sequence. The
-- pg_constraint guard makes this block safe after a partially applied deploy
-- and when the whole up migration is replayed for verification.
DO $$
DECLARE
    table_name TEXT;
    constraint_name TEXT;
    orphan_exists BOOLEAN;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'book_metadata_translations', 'author_translations', 'category_translations',
        'section_translations', 'book_heading_summaries',
        'book_metadata_translation_edits', 'author_translation_edits',
        'category_translation_edits', 'section_translation_edits', 'heading_summary_edits'
    ] LOOP
        constraint_name := table_name || '_generation_run_fk';

        IF NOT EXISTS (
            SELECT 1
            FROM pg_constraint
            WHERE conname = constraint_name
              AND conrelid = to_regclass(table_name)
        ) THEN
            EXECUTE format(
                'ALTER TABLE %I ADD CONSTRAINT %I '
                || 'FOREIGN KEY (generation_run_id) REFERENCES generation_runs (id) '
                || 'ON DELETE RESTRICT NOT VALID',
                table_name,
                constraint_name
            );
        END IF;

        EXECUTE format(
            'SELECT EXISTS ('
            || 'SELECT 1 FROM %I child '
            || 'LEFT JOIN generation_runs gr ON gr.id = child.generation_run_id '
            || 'WHERE child.generation_run_id IS NOT NULL AND gr.id IS NULL)',
            table_name
        ) INTO orphan_exists;

        IF orphan_exists THEN
            RAISE EXCEPTION '% generation run foreign-key preflight failed', table_name;
        END IF;

        EXECUTE format(
            'ALTER TABLE %I VALIDATE CONSTRAINT %I',
            table_name,
            constraint_name
        );
    END LOOP;
END;
$$;

DO $$
DECLARE
    table_name TEXT;
    constraint_name TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'book_metadata_translations', 'author_translations', 'category_translations',
        'section_translations', 'book_heading_summaries',
        'book_metadata_translation_edits', 'author_translation_edits',
        'category_translation_edits', 'section_translation_edits', 'heading_summary_edits'
    ] LOOP
        constraint_name := table_name || '_provenance_class_check';

        IF NOT EXISTS (
            SELECT 1
            FROM pg_constraint
            WHERE conname = constraint_name
              AND conrelid = to_regclass(table_name)
        ) THEN
            EXECUTE format(
                'ALTER TABLE %I ADD CONSTRAINT %I '
                || 'CHECK (provenance_class IN '
                || '(''legacy_unknown'', ''source'', ''editorial'', ''machine'')) NOT VALID',
                table_name,
                constraint_name
            );
        END IF;
    END LOOP;
END;
$$;

DO $$
DECLARE
    table_name TEXT;
    invalid_exists BOOLEAN;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'book_metadata_translations', 'author_translations', 'category_translations',
        'section_translations', 'book_heading_summaries',
        'book_metadata_translation_edits', 'author_translation_edits',
        'category_translation_edits', 'section_translation_edits', 'heading_summary_edits'
    ] LOOP
        EXECUTE format(
            'SELECT EXISTS (SELECT 1 FROM %I WHERE provenance_class NOT IN '
            || '(''legacy_unknown'', ''source'', ''editorial'', ''machine''))',
            table_name
        ) INTO invalid_exists;

        IF invalid_exists THEN
            RAISE EXCEPTION '% has an invalid provenance_class', table_name;
        END IF;
    END LOOP;
END;
$$;

ALTER TABLE book_metadata_translations VALIDATE CONSTRAINT book_metadata_translations_provenance_class_check;
ALTER TABLE author_translations VALIDATE CONSTRAINT author_translations_provenance_class_check;
ALTER TABLE category_translations VALIDATE CONSTRAINT category_translations_provenance_class_check;
ALTER TABLE section_translations VALIDATE CONSTRAINT section_translations_provenance_class_check;
ALTER TABLE book_heading_summaries VALIDATE CONSTRAINT book_heading_summaries_provenance_class_check;
ALTER TABLE book_metadata_translation_edits VALIDATE CONSTRAINT book_metadata_translation_edits_provenance_class_check;
ALTER TABLE author_translation_edits VALIDATE CONSTRAINT author_translation_edits_provenance_class_check;
ALTER TABLE category_translation_edits VALIDATE CONSTRAINT category_translation_edits_provenance_class_check;
ALTER TABLE section_translation_edits VALIDATE CONSTRAINT section_translation_edits_provenance_class_check;
ALTER TABLE heading_summary_edits VALIDATE CONSTRAINT heading_summary_edits_provenance_class_check;

-- PostgreSQL installs the constant legacy value without rewriting each row.
-- Drop the defaults immediately: after this migration every INSERT must name
-- provenance_class, while old rows retain their honest legacy marker.
ALTER TABLE book_metadata_translations ALTER COLUMN provenance_class DROP DEFAULT;
ALTER TABLE author_translations ALTER COLUMN provenance_class DROP DEFAULT;
ALTER TABLE category_translations ALTER COLUMN provenance_class DROP DEFAULT;
ALTER TABLE section_translations ALTER COLUMN provenance_class DROP DEFAULT;
ALTER TABLE book_heading_summaries ALTER COLUMN provenance_class DROP DEFAULT;
ALTER TABLE book_metadata_translation_edits ALTER COLUMN provenance_class DROP DEFAULT;
ALTER TABLE author_translation_edits ALTER COLUMN provenance_class DROP DEFAULT;
ALTER TABLE category_translation_edits ALTER COLUMN provenance_class DROP DEFAULT;
ALTER TABLE section_translation_edits ALTER COLUMN provenance_class DROP DEFAULT;
ALTER TABLE heading_summary_edits ALTER COLUMN provenance_class DROP DEFAULT;

CREATE OR REPLACE FUNCTION generated_asset_provenance_guard() RETURNS TRIGGER AS $$
DECLARE
    content_changed BOOLEAN := FALSE;
    column_name TEXT;
    restore_scoped BOOLEAN :=
        COALESCE(current_setting('surau.production_restore', true), '') = 'on';
    publish_scoped BOOLEAN :=
        COALESCE(current_setting('surau.production_publish', true), '') = 'on';
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.provenance_class = 'legacy_unknown' AND NOT restore_scoped THEN
            RAISE EXCEPTION 'new generated asset must declare provenance_class'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NOT restore_scoped THEN
        FOREACH column_name IN ARRAY TG_ARGV LOOP
            IF (to_jsonb(NEW) -> column_name) IS DISTINCT FROM (to_jsonb(OLD) -> column_name) THEN
                content_changed := TRUE;
                EXIT;
            END IF;
        END LOOP;

        IF publish_scoped THEN
            IF (NEW.provenance_class IS DISTINCT FROM OLD.provenance_class
                OR NEW.generation_run_id IS DISTINCT FROM OLD.generation_run_id)
               AND NOT content_changed THEN
                RAISE EXCEPTION 'publish cannot relabel generated asset without new text'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSE
            IF OLD.provenance_class = 'legacy_unknown' THEN
                IF content_changed AND NEW.provenance_class = 'legacy_unknown' THEN
                    RAISE EXCEPTION 'rewritten legacy asset must declare provenance_class'
                        USING ERRCODE = 'check_violation';
                END IF;
                IF NOT content_changed AND NEW.provenance_class IS DISTINCT FROM OLD.provenance_class THEN
                    RAISE EXCEPTION 'legacy provenance cannot be relabelled without rewriting the text'
                        USING ERRCODE = 'check_violation';
                END IF;
            ELSIF NEW.provenance_class IS DISTINCT FROM OLD.provenance_class THEN
                RAISE EXCEPTION 'generated asset provenance_class is immutable'
                    USING ERRCODE = 'check_violation';
            END IF;

            IF NEW.generation_run_id IS DISTINCT FROM OLD.generation_run_id AND NOT content_changed THEN
                RAISE EXCEPTION 'generation run can change only with generated asset content'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    END IF;

    IF NEW.provenance_class = 'machine' AND NEW.generation_run_id IS NULL THEN
        RAISE EXCEPTION 'machine generated asset requires generation_run_id'
            USING ERRCODE = 'not_null_violation';
    END IF;
    IF NEW.provenance_class <> 'machine' AND NEW.generation_run_id IS NOT NULL THEN
        RAISE EXCEPTION 'only machine generated assets may reference generation_run_id'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_book_metadata_translations_provenance ON book_metadata_translations;
CREATE TRIGGER trg_book_metadata_translations_provenance
    BEFORE INSERT OR UPDATE ON book_metadata_translations
    FOR EACH ROW EXECUTE FUNCTION generated_asset_provenance_guard(
        'display_title', 'bibliography', 'hint', 'description'
    );
DROP TRIGGER IF EXISTS trg_author_translations_provenance ON author_translations;
CREATE TRIGGER trg_author_translations_provenance
    BEFORE INSERT OR UPDATE ON author_translations
    FOR EACH ROW EXECUTE FUNCTION generated_asset_provenance_guard(
        'name', 'biography', 'death_text'
    );
DROP TRIGGER IF EXISTS trg_category_translations_provenance ON category_translations;
CREATE TRIGGER trg_category_translations_provenance
    BEFORE INSERT OR UPDATE ON category_translations
    FOR EACH ROW EXECUTE FUNCTION generated_asset_provenance_guard('name');
DROP TRIGGER IF EXISTS trg_section_translations_provenance ON section_translations;
CREATE TRIGGER trg_section_translations_provenance
    BEFORE INSERT OR UPDATE ON section_translations
    FOR EACH ROW EXECUTE FUNCTION generated_asset_provenance_guard('title', 'content');
DROP TRIGGER IF EXISTS trg_book_heading_summaries_provenance ON book_heading_summaries;
CREATE TRIGGER trg_book_heading_summaries_provenance
    BEFORE INSERT OR UPDATE ON book_heading_summaries
    FOR EACH ROW EXECUTE FUNCTION generated_asset_provenance_guard('summary');

DROP TRIGGER IF EXISTS trg_book_metadata_translation_edits_provenance ON book_metadata_translation_edits;
CREATE TRIGGER trg_book_metadata_translation_edits_provenance
    BEFORE INSERT OR UPDATE ON book_metadata_translation_edits
    FOR EACH ROW EXECUTE FUNCTION generated_asset_provenance_guard(
        'display_title', 'bibliography', 'hint', 'description'
    );
DROP TRIGGER IF EXISTS trg_author_translation_edits_provenance ON author_translation_edits;
CREATE TRIGGER trg_author_translation_edits_provenance
    BEFORE INSERT OR UPDATE ON author_translation_edits
    FOR EACH ROW EXECUTE FUNCTION generated_asset_provenance_guard(
        'name', 'biography', 'death_text'
    );
DROP TRIGGER IF EXISTS trg_category_translation_edits_provenance ON category_translation_edits;
CREATE TRIGGER trg_category_translation_edits_provenance
    BEFORE INSERT OR UPDATE ON category_translation_edits
    FOR EACH ROW EXECUTE FUNCTION generated_asset_provenance_guard('name');
DROP TRIGGER IF EXISTS trg_section_translation_edits_provenance ON section_translation_edits;
CREATE TRIGGER trg_section_translation_edits_provenance
    BEFORE INSERT OR UPDATE ON section_translation_edits
    FOR EACH ROW EXECUTE FUNCTION generated_asset_provenance_guard('title', 'content');
DROP TRIGGER IF EXISTS trg_heading_summary_edits_provenance ON heading_summary_edits;
CREATE TRIGGER trg_heading_summary_edits_provenance
    BEFORE INSERT OR UPDATE ON heading_summary_edits
    FOR EACH ROW EXECUTE FUNCTION generated_asset_provenance_guard('summary');
