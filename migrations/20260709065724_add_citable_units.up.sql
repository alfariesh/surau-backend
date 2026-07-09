-- B-1 Citable Unit registry (roadmap/phase-1b-content-backbone.md C1/C2, B-D1..D11).
-- Shared registry for paragraph-level units across corpora (pilot corpus: kitab).
-- Corpus tables (book_pages, quran_ayahs, ...) remain the source of truth for
-- display text; this registry holds identity, lifecycle, provenance, license,
-- and normalized text for indexing.
--
-- EXPAND step (docs/data-change-playbook.md §3.1): new tables + one nullable
-- column; no read path changes. Tables are new and empty, so full constraints
-- and plain indexes are created upfront (playbook §1).
--
-- SINGLE WRITE PATH (C2): all DML on citable_units / citable_unit_lineage must
-- run inside a transaction that begins with
--     SET LOCAL surau.registry_writer = 'unit-service';
-- enforced by citable_registry_guard(). Data-fixing migrations must wrap the
-- SET LOCAL + DML in one BEGIN;...COMMIT; or DO $$ block because migration
-- statements otherwise autocommit one by one (playbook §2). Incident escape
-- hatch is documented in docs/data-change-playbook.md §6.

CREATE TABLE IF NOT EXISTS citable_units (
    -- UUIDv5(namespace, natural key); PK therefore enforces natural-key
    -- uniqueness across ALL lifecycles (retired ids are never re-mintable).
    id UUID PRIMARY KEY,
    corpus TEXT NOT NULL CHECK (corpus IN ('kitab', 'quran', 'hadith', 'wiki')),
    -- kitab scope; heading_id/page_id are soft references (audited, no FK) so
    -- importer maintenance on book_headings/book_pages is never blocked by the
    -- registry. heading_id NULL = front-matter before the first heading anchor.
    book_id INTEGER REFERENCES books (id) ON DELETE CASCADE,
    heading_id INTEGER,
    -- physical locator, secondary metadata per B-D2 (identity is logical).
    page_id INTEGER,
    kind TEXT NOT NULL CHECK (kind IN ('paragraph', 'heading', 'quran_quote', 'footnote', 'html')),
    -- minted once per scope in document order, never recycled (C1); part of the
    -- canonical anchor, so it must survive edits and re-imports unchanged.
    ordinal INTEGER NOT NULL CHECK (ordinal >= 1),
    -- current display index within the scope; mutable locator attribute.
    position INTEGER NOT NULL CHECK (position >= 0),
    -- footnote -> owning body unit; mutable metadata (re-pointed when the
    -- parent is superseded), never part of identity.
    parent_unit_id UUID REFERENCES citable_units (id) ON DELETE CASCADE,
    -- provisional canonical anchor kitab/{book_id}/h/{heading_id|0}/u/{ordinal};
    -- B-2 ratifies the cross-corpus grammar before any public exposure.
    anchor TEXT NOT NULL,
    -- footnote marker as printed, e.g. (¬٢); hash input for footnote units.
    marker TEXT,
    text TEXT NOT NULL CHECK (text <> ''),
    html TEXT,
    -- produced exclusively by internal/searchtext (versioned profile).
    text_normalized TEXT NOT NULL,
    normalization_version INTEGER NOT NULL,
    -- sha256(kind || 0x00 || coalesce(marker,'') || 0x00 || text): formatting-only
    -- HTML changes do not change identity.
    content_hash BYTEA NOT NULL,
    -- disambiguates identical content within one scope (1-based dup rank, bumped
    -- past retired twins so ids are never recycled).
    occurrence INTEGER NOT NULL CHECK (occurrence >= 1),
    language TEXT NOT NULL DEFAULT 'ar',
    -- who AUTHORED the text; immutable (B-D11). Review state is a separate
    -- dimension and never mutates the class.
    provenance_class TEXT NOT NULL CHECK (provenance_class IN ('source', 'editorial', 'machine')),
    provenance_detail JSONB,
    -- per-unit override; NULL = inherit from the Work (books license column
    -- arrives with B-4). Gate stays query-time per C4.
    license_status TEXT CHECK (license_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain')),
    lifecycle TEXT NOT NULL DEFAULT 'active' CHECK (lifecycle IN ('active', 'superseded', 'tombstoned')),
    retired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((lifecycle = 'active') = (retired_at IS NULL)),
    -- pilot corpus shape: kitab units always carry a book scope.
    CHECK ((corpus = 'kitab') = (book_id IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_citable_units_anchor ON citable_units (anchor);
-- ordinals are identity-bearing and never recycled: unique across ALL lifecycles.
CREATE UNIQUE INDEX IF NOT EXISTS uq_citable_units_scope_ordinal
    ON citable_units (corpus, book_id, heading_id, ordinal) NULLS NOT DISTINCT;
-- foreign-write tripwire: at most one ACTIVE unit per natural key.
CREATE UNIQUE INDEX IF NOT EXISTS uq_citable_units_active_content
    ON citable_units (corpus, book_id, heading_id, kind, content_hash, occurrence)
    NULLS NOT DISTINCT
    WHERE lifecycle = 'active';
CREATE INDEX IF NOT EXISTS idx_citable_units_scope_position
    ON citable_units (book_id, heading_id, lifecycle, position);
CREATE INDEX IF NOT EXISTS idx_citable_units_parent
    ON citable_units (parent_unit_id)
    WHERE parent_unit_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_citable_units_book_page ON citable_units (book_id, page_id);

-- Edit-resilience lineage (B-D3): superseded units point at their successors;
-- old anchors resolve by walking these edges, never 404.
CREATE TABLE IF NOT EXISTS citable_unit_lineage (
    predecessor_id UUID NOT NULL REFERENCES citable_units (id) ON DELETE CASCADE,
    successor_id UUID NOT NULL REFERENCES citable_units (id) ON DELETE CASCADE,
    -- 'edit' = same-scope gap alignment; 'content_move' = book-level rescue pass
    -- (identical content re-appearing in another scope or surviving twin).
    reason TEXT NOT NULL DEFAULT 'edit' CHECK (reason IN ('edit', 'content_move')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (predecessor_id, successor_id),
    CHECK (predecessor_id <> successor_id)
);
CREATE INDEX IF NOT EXISTS idx_citable_unit_lineage_successor
    ON citable_unit_lineage (successor_id);

-- C2 single-write-path guard: reject any DML that does not come from the unit
-- service transaction (SET LOCAL surau.registry_writer = 'unit-service').
-- pg_trigger_depth() > 1 allows referential actions (books CASCADE, lineage
-- CASCADE) which execute as nested triggers; direct DML always sees depth 1.
CREATE OR REPLACE FUNCTION citable_registry_guard() RETURNS TRIGGER AS $$
BEGIN
    IF pg_trigger_depth() > 1
        OR current_setting('surau.registry_writer', true) IS NOT DISTINCT FROM 'unit-service' THEN
        RETURN COALESCE(NEW, OLD);
    END IF;
    RAISE EXCEPTION 'citable registry accepts writes only via the unit service (phase-1b C2 single write path)'
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_citable_units_guard ON citable_units;
CREATE TRIGGER trg_citable_units_guard
    BEFORE INSERT OR UPDATE OR DELETE ON citable_units
    FOR EACH ROW EXECUTE FUNCTION citable_registry_guard();

DROP TRIGGER IF EXISTS trg_citable_unit_lineage_guard ON citable_unit_lineage;
CREATE TRIGGER trg_citable_unit_lineage_guard
    BEFORE INSERT OR UPDATE OR DELETE ON citable_unit_lineage
    FOR EACH ROW EXECUTE FUNCTION citable_registry_guard();

-- Derivation marker: NULL = never derived; otherwise the LOAD timestamp of the
-- last successful reconcile (backfill staleness predicate + publish-hook gate).
ALTER TABLE books ADD COLUMN IF NOT EXISTS units_derived_at TIMESTAMPTZ;
