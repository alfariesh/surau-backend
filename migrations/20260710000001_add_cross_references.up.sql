-- B-3 shared Cross-Reference registry and Quran compatibility bridge.
--
-- EXPAND: both tables start empty. quran_book_references remains available to
-- the old reader while the resumable bridge backfill runs. New resolver writes
-- use one transaction for all three tables; this migration does not freeze the
-- legacy table until parity has been established and soaked.
--
-- SINGLE WRITE PATH: all DML on the two new tables must run inside a
-- transaction that starts with
--     SET LOCAL surau.cross_reference_writer = 'cross-reference-service';

CREATE TABLE IF NOT EXISTS cross_references (
    id UUID PRIMARY KEY,
    source_anchor TEXT NOT NULL,
    target_anchor TEXT NOT NULL,

    -- Query projections. Anchors remain the sole identity; these columns only
    -- make visibility, distinct-Work counts, and Quran containment indexable.
    source_corpus TEXT NOT NULL,
    target_corpus TEXT NOT NULL,
    source_work_id INTEGER REFERENCES books (id) ON DELETE RESTRICT,
    target_work_id INTEGER REFERENCES books (id) ON DELETE RESTRICT,
    target_quran_surah_id INTEGER REFERENCES quran_surahs (surah_id) ON DELETE RESTRICT,
    target_quran_from_ayah INTEGER,
    target_quran_to_ayah INTEGER,

    kind TEXT NOT NULL,
    method TEXT NOT NULL,
    method_detail JSONB NOT NULL DEFAULT '{}'::jsonb,
    confidence NUMERIC(5,4),
    review_status TEXT NOT NULL DEFAULT 'pending',

    evidence_text TEXT NOT NULL,
    evidence_normalized TEXT NOT NULL,
    normalization_version INTEGER NOT NULL,

    origin TEXT NOT NULL,
    origin_key TEXT NOT NULL,
    -- Actor identity is evidence. Hard deletion is restricted rather than
    -- nulling attribution (account deletion is anonymization/soft deletion).
    created_by UUID REFERENCES users (id) ON DELETE RESTRICT,
    reviewed_by UUID REFERENCES users (id) ON DELETE RESTRICT,
    reviewed_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT cross_references_anchor_length_check CHECK (
        octet_length(source_anchor) BETWEEN 1 AND 512
        AND octet_length(target_anchor) BETWEEN 1 AND 512
    ),
    CONSTRAINT cross_references_distinct_anchor_check CHECK (source_anchor <> target_anchor),
    CONSTRAINT cross_references_corpus_check CHECK (
        source_corpus IN ('kitab', 'quran', 'hadith', 'wiki', 'entity')
        AND target_corpus IN ('kitab', 'quran', 'hadith', 'wiki', 'entity')
        AND split_part(source_anchor, '/', 1) = source_corpus
        AND split_part(target_anchor, '/', 1) = target_corpus
    ),
    CONSTRAINT cross_references_work_projection_check CHECK (
        (source_corpus = 'kitab') = (source_work_id IS NOT NULL)
        AND (target_corpus = 'kitab') = (target_work_id IS NOT NULL)
    ),
    CONSTRAINT cross_references_quran_projection_check CHECK (
        (
            target_corpus = 'quran'
            AND target_quran_surah_id IS NOT NULL
            AND (
                (target_quran_from_ayah IS NULL AND target_quran_to_ayah IS NULL)
                OR (
                    target_quran_from_ayah IS NOT NULL
                    AND target_quran_to_ayah IS NOT NULL
                    AND target_quran_from_ayah >= 1
                    AND target_quran_to_ayah >= target_quran_from_ayah
                )
            )
        )
        OR (
            target_corpus <> 'quran'
            AND target_quran_surah_id IS NULL
            AND target_quran_from_ayah IS NULL
            AND target_quran_to_ayah IS NULL
        )
    ),
    CONSTRAINT cross_references_quran_from_fk FOREIGN KEY (target_quran_surah_id, target_quran_from_ayah)
        REFERENCES quran_ayahs (surah_id, ayah_number) ON DELETE RESTRICT,
    CONSTRAINT cross_references_quran_to_fk FOREIGN KEY (target_quran_surah_id, target_quran_to_ayah)
        REFERENCES quran_ayahs (surah_id, ayah_number) ON DELETE RESTRICT,
    CONSTRAINT cross_references_kind_check CHECK (kind IN ('cites', 'quotes', 'explains', 'parallel')),
    CONSTRAINT cross_references_method_check CHECK (method IN ('resolver', 'machine', 'human')),
    CONSTRAINT cross_references_method_detail_object_check CHECK (jsonb_typeof(method_detail) = 'object'),
    CONSTRAINT cross_references_method_detail_check CHECK (
        (
            method = 'resolver'
            AND btrim(COALESCE(method_detail ->> 'strategy', '')) <> ''
        )
        OR (
            method = 'machine'
            AND btrim(COALESCE(method_detail ->> 'model_id', '')) <> ''
            AND btrim(COALESCE(method_detail ->> 'prompt_version', '')) <> ''
            AND btrim(COALESCE(method_detail ->> 'run_id', '')) <> ''
        )
        OR (
            method = 'human'
            AND created_by IS NOT NULL
            AND method_detail ->> 'actor_id' = created_by::text
        )
    ),
    CONSTRAINT cross_references_confidence_check CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    CONSTRAINT cross_references_confidence_required_check CHECK (
        confidence IS NOT NULL OR origin = 'legacy_quran_reference'
    ),
    CONSTRAINT cross_references_review_status_check CHECK (
        review_status IN ('pending', 'approved', 'rejected', 'ambiguous', 'needs_review')
    ),
    CONSTRAINT cross_references_pending_reviewer_check CHECK (
        review_status <> 'pending' OR (reviewed_by IS NULL AND reviewed_at IS NULL)
    ),
    CONSTRAINT cross_references_reviewer_pair_check CHECK (
        (reviewed_by IS NULL) = (reviewed_at IS NULL)
    ),
    CONSTRAINT cross_references_normalization_check CHECK (normalization_version >= 1),
    CONSTRAINT cross_references_origin_check CHECK (btrim(origin) <> '' AND btrim(origin_key) <> ''),
    CONSTRAINT cross_references_metadata_object_check CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_cross_references_origin
    ON cross_references (origin, origin_key);
CREATE INDEX IF NOT EXISTS idx_cross_references_source_lookup
    ON cross_references (source_anchor, review_status, kind, created_at, id);
CREATE INDEX IF NOT EXISTS idx_cross_references_target_lookup
    ON cross_references (target_anchor, review_status, kind, created_at, id);
CREATE INDEX IF NOT EXISTS idx_cross_references_source_work
    ON cross_references (source_work_id, review_status, kind, created_at, id)
    WHERE source_work_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cross_references_target_work
    ON cross_references (target_work_id, review_status, kind, created_at, id)
    WHERE target_work_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cross_references_target_quran
    ON cross_references (target_quran_surah_id, target_quran_from_ayah, target_quran_to_ayah, review_status)
    WHERE target_quran_surah_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cross_references_target_quran_containment
    ON cross_references USING gist (
        int4range(target_quran_from_ayah, target_quran_to_ayah, '[]')
    )
    WHERE target_quran_from_ayah IS NOT NULL AND target_quran_to_ayah IS NOT NULL;

-- Public visibility follows Anchor semantics rather than trusting the derived
-- Work columns alone. It checks both range boundaries, publication/deletion,
-- and active unit successors. The registry list calls this only after its
-- indexed Anchor/status narrowing predicates.
CREATE OR REPLACE FUNCTION cross_reference_anchor_point_visible(point_value TEXT)
RETURNS BOOLEAN AS $$
DECLARE
    captures TEXT[];
    book_key INTEGER;
    heading_key INTEGER;
    heading_deleted BOOLEAN;
    has_active BOOLEAN;
    crossed_work BOOLEAN;
BEGIN
    captures := regexp_match(point_value, '^quran/([1-9][0-9]*)$');
    IF captures IS NOT NULL THEN
        RETURN EXISTS (
            SELECT 1 FROM quran_surahs WHERE surah_id = captures[1]::INTEGER
        );
    END IF;

    captures := regexp_match(point_value, '^quran/([1-9][0-9]*):([1-9][0-9]*)$');
    IF captures IS NOT NULL THEN
        RETURN EXISTS (
            SELECT 1 FROM quran_ayahs
            WHERE surah_id = captures[1]::INTEGER
              AND ayah_number = captures[2]::INTEGER
        );
    END IF;

    captures := regexp_match(point_value, '^kitab/([1-9][0-9]*)$');
    IF captures IS NOT NULL THEN
        RETURN EXISTS (
            SELECT 1
            FROM books b
            JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
            WHERE b.id = captures[1]::INTEGER AND b.is_deleted = FALSE
        );
    END IF;

    captures := regexp_match(
        point_value,
        '^kitab/([1-9][0-9]*)/h/([0-9]+)/u/([1-9][0-9]*)$'
    );
    IF captures IS NOT NULL THEN
        book_key := captures[1]::INTEGER;
        IF NOT EXISTS (
            SELECT 1
            FROM books b
            JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
            WHERE b.id = book_key AND b.is_deleted = FALSE
        ) THEN
            RETURN FALSE;
        END IF;

        WITH RECURSIVE walk(id, book_id, lifecycle) AS (
            SELECT id, book_id, lifecycle
            FROM citable_units
            WHERE anchor = point_value
            UNION
            SELECT u.id, u.book_id, u.lifecycle
            FROM walk w
            JOIN citable_unit_lineage l ON l.predecessor_id = w.id
            JOIN citable_units u ON u.id = l.successor_id
        )
        SELECT
            COALESCE(bool_or(lifecycle = 'active' AND book_id = book_key), FALSE),
            COALESCE(bool_or(book_id <> book_key), FALSE)
        INTO has_active, crossed_work
        FROM walk;

        RETURN has_active AND NOT crossed_work;
    END IF;

    captures := regexp_match(point_value, '^kitab/([1-9][0-9]*)/h/([1-9][0-9]*)$');
    IF captures IS NOT NULL THEN
        book_key := captures[1]::INTEGER;
        heading_key := captures[2]::INTEGER;

        SELECT h.is_deleted INTO heading_deleted
        FROM book_headings h
        JOIN books b ON b.id = h.book_id AND b.is_deleted = FALSE
        JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
        WHERE h.book_id = book_key AND h.heading_id = heading_key;

        IF NOT FOUND THEN
            RETURN FALSE;
        END IF;
        IF NOT heading_deleted THEN
            RETURN TRUE;
        END IF;

        WITH RECURSIVE walk(id, book_id, lifecycle) AS (
            SELECT id, book_id, lifecycle
            FROM citable_units
            WHERE book_id = book_key AND heading_id = heading_key
            UNION
            SELECT u.id, u.book_id, u.lifecycle
            FROM walk w
            JOIN citable_unit_lineage l ON l.predecessor_id = w.id
            JOIN citable_units u ON u.id = l.successor_id
        )
        SELECT
            COALESCE(bool_or(lifecycle = 'active' AND book_id = book_key), FALSE),
            COALESCE(bool_or(book_id <> book_key), FALSE)
        INTO has_active, crossed_work
        FROM walk;

        RETURN has_active AND NOT crossed_work;
    END IF;

    RETURN FALSE;
END;
$$ LANGUAGE plpgsql STABLE COST 10;

CREATE OR REPLACE FUNCTION cross_reference_anchor_visible(anchor_value TEXT)
RETURNS BOOLEAN AS $$
DECLARE
    boundaries TEXT[];
BEGIN
    boundaries := string_to_array(anchor_value, '..');
    IF array_length(boundaries, 1) = 1 THEN
        RETURN cross_reference_anchor_point_visible(boundaries[1]);
    END IF;
    IF array_length(boundaries, 1) = 2 THEN
        RETURN cross_reference_anchor_point_visible(boundaries[1])
           AND cross_reference_anchor_point_visible(boundaries[2]);
    END IF;
    RETURN FALSE;
END;
$$ LANGUAGE plpgsql STABLE COST 10;

CREATE TABLE IF NOT EXISTS quran_cross_reference_bridge (
    cross_reference_id UUID PRIMARY KEY
        REFERENCES cross_references (id) ON DELETE CASCADE,
    book_id INTEGER NOT NULL,
    page_id INTEGER NOT NULL,
    heading_id INTEGER,
    knowledge_mention_id UUID REFERENCES knowledge_mentions (id) ON DELETE SET NULL,
    source_text TEXT NOT NULL,
    normalized_text TEXT NOT NULL,
    reference_kind TEXT NOT NULL,
    surah_id INTEGER REFERENCES quran_surahs (surah_id) ON DELETE SET NULL,
    from_ayah_number INTEGER,
    to_ayah_number INTEGER,
    from_ayah_key TEXT,
    to_ayah_key TEXT,
    match_strategy TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT quran_cross_reference_bridge_legacy_fk FOREIGN KEY (cross_reference_id)
        REFERENCES quran_book_references (id) ON DELETE CASCADE,
    CONSTRAINT quran_cross_reference_bridge_page_fk FOREIGN KEY (book_id, page_id)
        REFERENCES book_pages (book_id, page_id) ON DELETE CASCADE,
    CONSTRAINT quran_cross_reference_bridge_heading_fk FOREIGN KEY (book_id, heading_id)
        REFERENCES book_headings (book_id, heading_id) ON DELETE CASCADE,
    CONSTRAINT quran_cross_reference_bridge_from_fk FOREIGN KEY (surah_id, from_ayah_number)
        REFERENCES quran_ayahs (surah_id, ayah_number) ON DELETE SET NULL,
    CONSTRAINT quran_cross_reference_bridge_to_fk FOREIGN KEY (surah_id, to_ayah_number)
        REFERENCES quran_ayahs (surah_id, ayah_number) ON DELETE SET NULL,
    CONSTRAINT quran_cross_reference_bridge_kind_check CHECK (
        reference_kind IN ('surah_ayah', 'surah', 'quote', 'ambiguous')
    ),
    CONSTRAINT quran_cross_reference_bridge_range_check CHECK (
        (from_ayah_number IS NULL AND to_ayah_number IS NULL)
        OR (
            surah_id IS NOT NULL
            AND from_ayah_number IS NOT NULL
            AND to_ayah_number IS NOT NULL
            AND to_ayah_number >= from_ayah_number
        )
    ),
    CONSTRAINT quran_cross_reference_bridge_key_check CHECK (
        (from_ayah_number IS NULL AND from_ayah_key IS NULL AND to_ayah_number IS NULL AND to_ayah_key IS NULL)
        OR (
            from_ayah_key = surah_id::text || ':' || from_ayah_number::text
            AND to_ayah_key = surah_id::text || ':' || to_ayah_number::text
        )
    ),
    CONSTRAINT quran_cross_reference_bridge_metadata_object_check CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX IF NOT EXISTS idx_quran_cross_reference_bridge_book
    ON quran_cross_reference_bridge (book_id, heading_id, page_id, cross_reference_id);
CREATE INDEX IF NOT EXISTS idx_quran_cross_reference_bridge_surah
    ON quran_cross_reference_bridge (surah_id, from_ayah_number, to_ayah_number);
CREATE UNIQUE INDEX IF NOT EXISTS uq_quran_cross_reference_bridge_mention
    ON quran_cross_reference_bridge (knowledge_mention_id)
    WHERE knowledge_mention_id IS NOT NULL;

-- Freeze switch for the expand/dual-write/contract transition. It starts open
-- so a migration-first deploy cannot break the old binary. A separate,
-- operator-invoked freeze job flips it only after bridge and parity drills pass.
CREATE TABLE IF NOT EXISTS cross_reference_registry_state (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    quran_legacy_frozen BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO cross_reference_registry_state (id, quran_legacy_frozen)
VALUES (TRUE, FALSE)
ON CONFLICT (id) DO NOTHING;

CREATE OR REPLACE FUNCTION cross_reference_registry_guard() RETURNS TRIGGER AS $$
BEGIN
    IF pg_trigger_depth() > 1
        OR current_setting('surau.cross_reference_writer', true)
            IS NOT DISTINCT FROM 'cross-reference-service' THEN
        RETURN COALESCE(NEW, OLD);
    END IF;
    RAISE EXCEPTION 'cross-reference registry accepts writes only via the cross-reference service (phase-1b B-3)'
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_cross_references_guard ON cross_references;
CREATE TRIGGER trg_cross_references_guard
    BEFORE INSERT OR UPDATE OR DELETE ON cross_references
    FOR EACH ROW EXECUTE FUNCTION cross_reference_registry_guard();

DROP TRIGGER IF EXISTS trg_quran_cross_reference_bridge_guard ON quran_cross_reference_bridge;
CREATE TRIGGER trg_quran_cross_reference_bridge_guard
    BEFORE INSERT OR UPDATE OR DELETE ON quran_cross_reference_bridge
    FOR EACH ROW EXECUTE FUNCTION cross_reference_registry_guard();

DROP TRIGGER IF EXISTS trg_cross_reference_registry_state_guard ON cross_reference_registry_state;
CREATE TRIGGER trg_cross_reference_registry_state_guard
    BEFORE INSERT OR UPDATE OR DELETE ON cross_reference_registry_state
    FOR EACH ROW EXECUTE FUNCTION cross_reference_registry_guard();

CREATE OR REPLACE FUNCTION legacy_quran_reference_write_guard() RETURNS TRIGGER AS $$
DECLARE
    is_frozen BOOLEAN;
BEGIN
    IF pg_trigger_depth() > 1
        OR current_setting('surau.cross_reference_writer', true)
            IS NOT DISTINCT FROM 'cross-reference-service' THEN
        RETURN COALESCE(NEW, OLD);
    END IF;

    SELECT quran_legacy_frozen INTO is_frozen
    FROM cross_reference_registry_state
    WHERE id = TRUE
    FOR KEY SHARE;

    IF NOT COALESCE(is_frozen, TRUE) THEN
        RETURN COALESCE(NEW, OLD);
    END IF;

    RAISE EXCEPTION 'legacy Quran reference writes are frozen; use the cross-reference service'
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_book_references_cross_reference_guard ON quran_book_references;
CREATE TRIGGER trg_quran_book_references_cross_reference_guard
    BEFORE INSERT OR UPDATE OR DELETE ON quran_book_references
    FOR EACH ROW EXECUTE FUNCTION legacy_quran_reference_write_guard();
