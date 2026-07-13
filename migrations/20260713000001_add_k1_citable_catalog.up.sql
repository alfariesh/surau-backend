-- K-1: expand the shared Citable Unit registry for full-catalog kitab
-- materialization. This migration is deliberately additive; the application
-- remains on the legacy Book-RAG path until the resumable backfill proves
-- complete and the rollout flag is moved through dual to unit mode.

ALTER TABLE books
    ADD COLUMN IF NOT EXISTS units_stale_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS units_derivation_profile_version INTEGER;

-- The B-1 pilot predates K-1's richer derivation profile. Mark it stale rather
-- than pretending the old snapshot already contains source spans/enrichment.
UPDATE books
SET units_stale_at = COALESCE(units_stale_at, clock_timestamp())
WHERE units_derived_at IS NOT NULL
  AND units_derivation_profile_version IS NULL;

ALTER TABLE citable_units
    -- Constant defaults give old rows their compatibility values without a
    -- table rewrite or an UPDATE that would disturb B-6 legacy exceptions.
    ADD COLUMN IF NOT EXISTS content_role TEXT DEFAULT 'book_page',
    ADD COLUMN IF NOT EXISTS review_status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS source_document_hash BYTEA,
    ADD COLUMN IF NOT EXISTS source_char_start INTEGER,
    ADD COLUMN IF NOT EXISTS source_char_end INTEGER;

-- B-6 deliberately left its generation identity CHECK NOT VALID so a
-- historical machine row with no trustworthy run can remain untouched. K-1
-- must not UPDATE that row or invent an identity. The fast defaults above set
-- it to book_page/pending, while this guarded backfill touches only rows whose
-- existing B-6 tuple is valid.
DO $$
BEGIN
    PERFORM set_config('surau.registry_writer', 'unit-service', true);

    -- Existing Quran units stay outside the kitab-only content-role vocabulary.
    UPDATE citable_units
    SET content_role = NULL
    WHERE corpus <> 'kitab' AND content_role IS NOT NULL;

    -- Published source/editorial material is already human-visible.
    -- Historical machine material is fail-closed until explicit review.
    UPDATE citable_units
    SET review_status = 'approved'
    WHERE provenance_class IN ('source', 'editorial')
      AND review_status <> 'approved';
END;
$$;

ALTER TABLE citable_units
    -- Compatibility sentinel for the one-release expand window. Both the
    -- pre-K-1 kitab writer and the Quran writer omit content_role, so a static
    -- book_page/NULL default cannot preserve both shapes. The BEFORE INSERT
    -- trigger below resolves this sentinel from corpus, while an explicitly
    -- supplied NULL on a kitab unit still fails the structural CHECK.
    ALTER COLUMN content_role SET DEFAULT 'legacy_auto',
    ALTER COLUMN review_status SET DEFAULT 'pending',
    ALTER COLUMN review_status SET NOT NULL;

CREATE OR REPLACE FUNCTION citable_unit_k1_content_role_compat() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.content_role = 'legacy_auto' THEN
        NEW.content_role := CASE WHEN NEW.corpus = 'kitab' THEN 'book_page' ELSE NULL END;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_citable_unit_k1_content_role_compat ON citable_units;
CREATE TRIGGER trg_citable_unit_k1_content_role_compat
    BEFORE INSERT ON citable_units
    FOR EACH ROW EXECUTE FUNCTION citable_unit_k1_content_role_compat();

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'citable_units'::regclass
          AND conname = 'citable_units_content_role_check'
    ) THEN
        ALTER TABLE citable_units
            ADD CONSTRAINT citable_units_content_role_check CHECK (
                (corpus = 'kitab' AND content_role IS NOT NULL AND content_role IN (
                    'book_page', 'section_translation', 'heading_summary'
                ))
                OR (corpus <> 'kitab' AND content_role IS NULL)
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'citable_units'::regclass
          AND conname = 'citable_units_review_status_check'
    ) THEN
        ALTER TABLE citable_units
            ADD CONSTRAINT citable_units_review_status_check CHECK (
                review_status IN (
                    'pending', 'approved', 'rejected', 'ambiguous', 'needs_review'
                )
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'citable_units'::regclass
          AND conname = 'citable_units_source_char_range_check'
    ) THEN
        ALTER TABLE citable_units
            ADD CONSTRAINT citable_units_source_char_range_check CHECK (
                (source_char_start IS NULL AND source_char_end IS NULL)
                OR (
                    source_char_start >= 0
                    AND source_char_end > source_char_start
                    AND source_document_hash IS NOT NULL
                )
            ) NOT VALID;
    END IF;
END;
$$;

-- Preflight before validation keeps failures explicit instead of leaving a
-- deploy-time schema in an ambiguous partial state.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM citable_units
        WHERE NOT (
            (corpus = 'kitab' AND content_role IS NOT NULL AND content_role IN (
                'book_page', 'section_translation', 'heading_summary'
            ))
            OR (corpus <> 'kitab' AND content_role IS NULL)
        )
    ) THEN
        RAISE EXCEPTION 'K-1 content-role preflight failed';
    END IF;

    IF EXISTS (
        SELECT 1 FROM citable_units
        WHERE review_status NOT IN (
            'pending', 'approved', 'rejected', 'ambiguous', 'needs_review'
        )
    ) THEN
        RAISE EXCEPTION 'K-1 review-status preflight failed';
    END IF;

    IF EXISTS (
        SELECT 1 FROM citable_units
        WHERE (source_char_start IS NULL) <> (source_char_end IS NULL)
           OR source_char_start < 0
           OR source_char_end <= source_char_start
           OR (source_char_start IS NOT NULL AND source_document_hash IS NULL)
    ) THEN
        RAISE EXCEPTION 'K-1 source-span preflight failed';
    END IF;
END;
$$;

ALTER TABLE citable_units
    VALIDATE CONSTRAINT citable_units_content_role_check;
ALTER TABLE citable_units
    VALIDATE CONSTRAINT citable_units_review_status_check;
ALTER TABLE citable_units
    VALIDATE CONSTRAINT citable_units_source_char_range_check;

ALTER TABLE citable_units DROP CONSTRAINT IF EXISTS citable_units_kind_check;
ALTER TABLE citable_units
    ADD CONSTRAINT citable_units_kind_check CHECK (
        kind IN (
            'paragraph', 'heading', 'quran_quote', 'footnote', 'html', 'summary',
            'primary_text', 'translation', 'transliteration'
        )
    ) NOT VALID;
ALTER TABLE citable_units VALIDATE CONSTRAINT citable_units_kind_check;

-- A generated column is non-overridable by every writer. It is the structural
-- safety boundary used by interpretive retrieval; prompts cannot bypass it.
ALTER TABLE citable_units
    ADD COLUMN IF NOT EXISTS interpretive_retrieval_eligible BOOLEAN
        GENERATED ALWAYS AS (
            corpus <> 'quran'
            AND kind <> 'quran_quote'
            AND (
                provenance_class = 'source'
                OR (
                    provenance_class IN ('editorial', 'machine')
                    AND review_status = 'approved'
                )
            )
        ) STORED;

CREATE OR REPLACE FUNCTION citable_unit_k1_identity_guard() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.corpus = 'kitab'
       AND (
           NEW.content_role IS DISTINCT FROM OLD.content_role
           OR NEW.language IS DISTINCT FROM OLD.language
       ) THEN
        RAISE EXCEPTION 'kitab Citable Unit content_role and language are immutable'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'citable_unit_k1_identity_immutable_check';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_citable_unit_k1_identity ON citable_units;
CREATE TRIGGER trg_citable_unit_k1_identity
    BEFORE UPDATE OF content_role, language ON citable_units
    FOR EACH ROW EXECUTE FUNCTION citable_unit_k1_identity_guard();

-- Only this view may feed public interpretive Book-RAG. Joining the canonical
-- B-4 public view preserves grandfather visibility, while an explicit unit
-- override remains fail-closed unless it is literally permitted.
CREATE OR REPLACE VIEW public_book_interpretive_citable_units AS
SELECT u.*
FROM citable_units u
JOIN public_book_publications publication ON publication.book_id = u.book_id
JOIN books b ON b.id = u.book_id
WHERE u.corpus = 'kitab'
  AND u.lifecycle = 'active'
  AND u.interpretive_retrieval_eligible
  AND (u.license_status IS NULL OR u.license_status = 'permitted')
  AND b.units_derived_at IS NOT NULL
  AND b.units_stale_at IS NULL
  AND b.units_derivation_profile_version = 2;

-- Durable per-book queue layered on F1-H's job checkpoint. A process crash can
-- safely resume pending/failed items without replaying completed books.
CREATE TABLE IF NOT EXISTS citable_unit_catalog_queue (
    job_name TEXT NOT NULL REFERENCES backfill_jobs(job_name) ON DELETE CASCADE,
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL CHECK (sequence >= 1),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    source_fingerprint BYTEA,
    result_checksum BYTEA,
    error TEXT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (job_name, book_id),
    UNIQUE (job_name, sequence),
    CONSTRAINT citable_unit_catalog_queue_finished_check CHECK (
        (status = 'completed' AND finished_at IS NOT NULL AND result_checksum IS NOT NULL)
        OR (status = 'cancelled' AND finished_at IS NOT NULL)
        OR status NOT IN ('completed', 'cancelled')
    )
);

ALTER TABLE knowledge_mentions
    ADD COLUMN IF NOT EXISTS unit_id UUID,
    ADD COLUMN IF NOT EXISTS unit_char_start INTEGER,
    ADD COLUMN IF NOT EXISTS unit_char_end INTEGER,
    ADD COLUMN IF NOT EXISTS unit_binding_status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS unit_binding_version INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS unit_source_hash TEXT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'knowledge_mentions'::regclass
          AND conname = 'knowledge_mentions_unit_fk'
    ) THEN
        ALTER TABLE knowledge_mentions
            ADD CONSTRAINT knowledge_mentions_unit_fk
            FOREIGN KEY (unit_id) REFERENCES citable_units(id) ON DELETE RESTRICT
            NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'knowledge_mentions'::regclass
          AND conname = 'knowledge_mentions_unit_binding_status_check'
    ) THEN
        ALTER TABLE knowledge_mentions
            ADD CONSTRAINT knowledge_mentions_unit_binding_status_check CHECK (
                unit_binding_status IN (
                    'pending', 'bound', 'stale', 'ambiguous', 'cross_unit', 'missing'
                )
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'knowledge_mentions'::regclass
          AND conname = 'knowledge_mentions_unit_binding_shape_check'
    ) THEN
        ALTER TABLE knowledge_mentions
            ADD CONSTRAINT knowledge_mentions_unit_binding_shape_check CHECK (
                unit_binding_version >= 1
                AND (
                    (unit_char_start IS NULL AND unit_char_end IS NULL)
                    OR (unit_char_start >= 0 AND unit_char_end > unit_char_start)
                )
                AND (
                    unit_binding_status <> 'bound'
                    OR (
                        unit_id IS NOT NULL
                        AND unit_char_start IS NOT NULL
                        AND unit_char_end IS NOT NULL
                        AND NULLIF(btrim(unit_source_hash), '') IS NOT NULL
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
        FROM knowledge_mentions mention
        LEFT JOIN citable_units unit ON unit.id = mention.unit_id
        WHERE mention.unit_id IS NOT NULL AND unit.id IS NULL
    ) THEN
        RAISE EXCEPTION 'K-1 knowledge mention unit FK preflight failed';
    END IF;

    IF EXISTS (
        SELECT 1 FROM knowledge_mentions
        WHERE unit_binding_status NOT IN (
                'pending', 'bound', 'stale', 'ambiguous', 'cross_unit', 'missing'
            )
           OR unit_binding_version < 1
           OR (unit_char_start IS NULL) <> (unit_char_end IS NULL)
           OR unit_char_start < 0
           OR unit_char_end <= unit_char_start
           OR (
               unit_binding_status = 'bound'
               AND (
                   unit_id IS NULL
                   OR unit_char_start IS NULL
                   OR unit_char_end IS NULL
                   OR NULLIF(btrim(unit_source_hash), '') IS NULL
               )
           )
    ) THEN
        RAISE EXCEPTION 'K-1 knowledge mention binding preflight failed';
    END IF;
END;
$$;

ALTER TABLE knowledge_mentions
    VALIDATE CONSTRAINT knowledge_mentions_unit_fk;
ALTER TABLE knowledge_mentions
    VALIDATE CONSTRAINT knowledge_mentions_unit_binding_status_check;
ALTER TABLE knowledge_mentions
    VALIDATE CONSTRAINT knowledge_mentions_unit_binding_shape_check;

-- Future approvals are fail-closed at commit time. The deferred check reads
-- the final row version, so the Python writer may insert the mention and bind
-- it later in the same per-page transaction. The one-time catalog backfill
-- uses a transaction-local GUC to record legacy ambiguous/stale outcomes; its
-- completion gate still requires approved_mention_unanchored=0.
CREATE OR REPLACE FUNCTION knowledge_mention_approved_unit_guard() RETURNS TRIGGER AS $$
DECLARE
    current_review_status TEXT;
    binding_valid BOOLEAN;
BEGIN
    SELECT mention.review_status
    INTO current_review_status
    FROM knowledge_mentions mention
    WHERE mention.id = NEW.id;

    IF current_review_status IS DISTINCT FROM 'approved'
       OR current_setting('surau.k1_mention_binding_backfill', TRUE) = 'on' THEN
        RETURN NEW;
    END IF;

    WITH RECURSIVE reachable(id, lifecycle, path) AS (
        SELECT root.id, root.lifecycle, ARRAY[root.id]
        FROM knowledge_mentions mention
        JOIN citable_units root ON root.id = mention.unit_id
        WHERE mention.id = NEW.id
        UNION ALL
        SELECT successor.id, successor.lifecycle, reachable.path || successor.id
        FROM reachable
        JOIN citable_unit_lineage lineage ON lineage.predecessor_id = reachable.id
        JOIN citable_units successor ON successor.id = lineage.successor_id
        WHERE NOT successor.id = ANY(reachable.path)
    )
    SELECT EXISTS (
        SELECT 1
        FROM knowledge_mentions mention
        JOIN citable_units root ON root.id = mention.unit_id
        WHERE mention.id = NEW.id
          AND mention.unit_binding_status = 'bound'
          AND mention.unit_binding_version IS NOT NULL
          AND mention.unit_char_start >= 0
          AND mention.unit_char_end > mention.unit_char_start
          AND root.book_id = mention.book_id
          AND root.page_id = mention.page_id
          AND root.corpus = 'kitab'
          AND root.content_role = 'book_page'
          AND root.provenance_class = 'source'
          AND root.lifecycle IN ('active', 'superseded')
          AND root.source_document_hash IS NOT NULL
          AND encode(root.source_document_hash, 'hex') = lower(mention.source_hash)
          AND mention.unit_source_hash = mention.source_hash
          AND mention.unit_char_end <= char_length(root.text)
          AND substring(
              root.text
              FROM mention.unit_char_start + 1
              FOR mention.unit_char_end - mention.unit_char_start
          ) = mention.exact_quote
          AND EXISTS (SELECT 1 FROM reachable WHERE lifecycle = 'active')
    ) INTO binding_valid;

    IF NOT binding_valid THEN
        RAISE EXCEPTION 'approved knowledge mention % requires an exact resolvable Citable Unit binding', NEW.id
            USING ERRCODE = '23514', CONSTRAINT = 'knowledge_mentions_approved_unit_guard';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_knowledge_mentions_approved_unit_guard ON knowledge_mentions;
CREATE CONSTRAINT TRIGGER trg_knowledge_mentions_approved_unit_guard
    AFTER INSERT OR UPDATE OF book_id, page_id, exact_quote, char_start, char_end,
        source_hash, review_status, unit_id, unit_char_start, unit_char_end,
        unit_binding_status, unit_binding_version, unit_source_hash
    ON knowledge_mentions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION knowledge_mention_approved_unit_guard();

-- Staleness is marked at the source boundary. statement_timestamp() avoids a
-- write per row in a batch while still defeating a concurrent stale-clear CAS.
CREATE OR REPLACE FUNCTION kitab_units_mark_book_stale(target_book_id INTEGER)
RETURNS VOID AS $$
BEGIN
    IF target_book_id IS NULL THEN
        RETURN;
    END IF;

    UPDATE books
    SET units_stale_at = statement_timestamp()
    WHERE id = target_book_id
      AND (
          units_stale_at IS NULL
          OR units_stale_at < statement_timestamp()
      );
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION kitab_units_source_stale_trigger() RETURNS TRIGGER AS $$
DECLARE
    old_row JSONB;
    new_row JSONB;
    old_book_id INTEGER;
    new_book_id INTEGER;
    status_column TEXT := COALESCE(TG_ARGV[0], '');
    visible_value TEXT := COALESCE(TG_ARGV[1], '');
    old_visible BOOLEAN := TRUE;
    new_visible BOOLEAN := TRUE;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_row := to_jsonb(OLD);
        old_book_id := NULLIF(old_row ->> 'book_id', '')::INTEGER;
        IF status_column <> '' THEN
            old_visible := old_row ->> status_column = visible_value;
        END IF;
    ELSE
        old_visible := FALSE;
    END IF;

    IF TG_OP <> 'DELETE' THEN
        new_row := to_jsonb(NEW);
        new_book_id := NULLIF(new_row ->> 'book_id', '')::INTEGER;
        IF status_column <> '' THEN
            new_visible := new_row ->> status_column = visible_value;
        END IF;
    ELSE
        new_visible := FALSE;
    END IF;

    IF old_visible THEN
        PERFORM kitab_units_mark_book_stale(old_book_id);
    END IF;
    IF new_visible AND new_book_id IS DISTINCT FROM old_book_id THEN
        PERFORM kitab_units_mark_book_stale(new_book_id);
    ELSIF new_visible AND NOT old_visible THEN
        PERFORM kitab_units_mark_book_stale(new_book_id);
    ELSIF new_visible AND old_visible THEN
        PERFORM kitab_units_mark_book_stale(new_book_id);
    END IF;

    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

-- Translation and summary drafts must not take the published unit catalog
-- offline. Arabic source assets are always derivation inputs; enrichment
-- assets only become inputs while their language project is published.
CREATE OR REPLACE FUNCTION kitab_units_asset_stale_trigger() RETURNS TRIGGER AS $$
DECLARE
    old_row JSONB;
    new_row JSONB;
    old_book_id INTEGER;
    new_book_id INTEGER;
    old_lang TEXT;
    new_lang TEXT;
    old_visible BOOLEAN := FALSE;
    new_visible BOOLEAN := FALSE;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_row := to_jsonb(OLD);
        old_book_id := NULLIF(old_row ->> 'book_id', '')::INTEGER;
        old_lang := old_row ->> 'lang';
        old_visible := old_lang = 'ar' OR EXISTS (
            SELECT 1
            FROM book_production_projects project
            WHERE project.book_id = old_book_id
              AND project.lang = old_lang
              AND project.publication_status = 'published'
              AND project.workflow_status <> 'archived'
        );
    END IF;

    IF TG_OP <> 'DELETE' THEN
        new_row := to_jsonb(NEW);
        new_book_id := NULLIF(new_row ->> 'book_id', '')::INTEGER;
        new_lang := new_row ->> 'lang';
        new_visible := new_lang = 'ar' OR EXISTS (
            SELECT 1
            FROM book_production_projects project
            WHERE project.book_id = new_book_id
              AND project.lang = new_lang
              AND project.publication_status = 'published'
              AND project.workflow_status <> 'archived'
        );
    END IF;

    IF old_visible THEN
        PERFORM kitab_units_mark_book_stale(old_book_id);
    END IF;
    IF new_visible AND (new_book_id IS DISTINCT FROM old_book_id OR NOT old_visible OR TG_OP = 'UPDATE') THEN
        PERFORM kitab_units_mark_book_stale(new_book_id);
    END IF;

    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_book_pages_units_stale ON book_pages;
CREATE TRIGGER trg_book_pages_units_stale
    AFTER INSERT OR UPDATE OR DELETE ON book_pages
    FOR EACH ROW EXECUTE FUNCTION kitab_units_source_stale_trigger();

CREATE OR REPLACE FUNCTION kitab_units_book_stale_trigger() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        PERFORM kitab_units_mark_book_stale(OLD.id);
    END IF;
    IF TG_OP <> 'DELETE' AND (TG_OP = 'INSERT' OR NEW.id IS DISTINCT FROM OLD.id OR
        NEW.major_release IS DISTINCT FROM OLD.major_release OR
        NEW.minor_release IS DISTINCT FROM OLD.minor_release OR
        NEW.is_deleted IS DISTINCT FROM OLD.is_deleted OR
        NEW.has_content IS DISTINCT FROM OLD.has_content) THEN
        PERFORM kitab_units_mark_book_stale(NEW.id);
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_books_units_stale ON books;
CREATE TRIGGER trg_books_units_stale
    AFTER INSERT OR UPDATE OF major_release, minor_release, is_deleted, has_content OR DELETE ON books
    FOR EACH ROW EXECUTE FUNCTION kitab_units_book_stale_trigger();

DROP TRIGGER IF EXISTS trg_book_headings_units_stale ON book_headings;
CREATE TRIGGER trg_book_headings_units_stale
    AFTER INSERT OR UPDATE OR DELETE ON book_headings
    FOR EACH ROW EXECUTE FUNCTION kitab_units_source_stale_trigger();

DROP TRIGGER IF EXISTS trg_book_page_edits_units_stale ON book_page_edits;
CREATE TRIGGER trg_book_page_edits_units_stale
    AFTER INSERT OR UPDATE OR DELETE ON book_page_edits
    FOR EACH ROW EXECUTE FUNCTION kitab_units_source_stale_trigger('status', 'published');

DROP TRIGGER IF EXISTS trg_book_heading_edits_units_stale ON book_heading_edits;
CREATE TRIGGER trg_book_heading_edits_units_stale
    AFTER INSERT OR UPDATE OR DELETE ON book_heading_edits
    FOR EACH ROW EXECUTE FUNCTION kitab_units_source_stale_trigger('status', 'published');

DROP TRIGGER IF EXISTS trg_section_translations_units_stale ON section_translations;
CREATE TRIGGER trg_section_translations_units_stale
    AFTER INSERT OR UPDATE OR DELETE ON section_translations
    FOR EACH ROW EXECUTE FUNCTION kitab_units_asset_stale_trigger();

DROP TRIGGER IF EXISTS trg_book_heading_summaries_units_stale ON book_heading_summaries;
CREATE TRIGGER trg_book_heading_summaries_units_stale
    AFTER INSERT OR UPDATE OR DELETE ON book_heading_summaries
    FOR EACH ROW EXECUTE FUNCTION kitab_units_asset_stale_trigger();

DROP TRIGGER IF EXISTS trg_book_publications_units_stale ON book_publications;
CREATE TRIGGER trg_book_publications_units_stale
    AFTER INSERT OR UPDATE OR DELETE ON book_publications
    FOR EACH ROW EXECUTE FUNCTION kitab_units_source_stale_trigger('status', 'published');

DROP TRIGGER IF EXISTS trg_book_production_projects_units_stale ON book_production_projects;
CREATE TRIGGER trg_book_production_projects_units_stale
    AFTER INSERT OR UPDATE OR DELETE ON book_production_projects
    FOR EACH ROW EXECUTE FUNCTION kitab_units_source_stale_trigger('publication_status', 'published');
