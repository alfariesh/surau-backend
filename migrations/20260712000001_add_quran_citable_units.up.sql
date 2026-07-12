-- Q-2: adopt the existing B-1 Citable Unit registry for Quran without
-- introducing a second registry, resolver, or Cross-Reference bridge.

ALTER TABLE quran_surahs
    ADD COLUMN IF NOT EXISTS units_derived_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS units_stale_at TIMESTAMPTZ;

UPDATE quran_surahs
SET units_stale_at = COALESCE(units_stale_at, clock_timestamp())
WHERE units_derived_at IS NULL;

-- Attribution and license decisions live on the source record. Citable Units
-- inherit them dynamically so a takedown never requires rewriting ~40k units.
ALTER TABLE quran_translation_sources
    ADD COLUMN IF NOT EXISTS responsible_name TEXT,
    ADD COLUMN IF NOT EXISTS responsible_role TEXT,
    ADD COLUMN IF NOT EXISTS license_reason TEXT,
    ADD COLUMN IF NOT EXISTS license_evidence_url TEXT,
    ADD COLUMN IF NOT EXISTS license_updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS license_updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE quran_transliteration_sources
    ADD COLUMN IF NOT EXISTS responsible_name TEXT,
    ADD COLUMN IF NOT EXISTS responsible_role TEXT,
    ADD COLUMN IF NOT EXISTS license_reason TEXT,
    ADD COLUMN IF NOT EXISTS license_evidence_url TEXT,
    ADD COLUMN IF NOT EXISTS license_updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS license_updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Existing source names are factual source/publisher attribution, not invented
-- translator identities. A human license audit can refine these fields later.
UPDATE quran_translation_sources
SET responsible_name = COALESCE(NULLIF(btrim(responsible_name), ''), NULLIF(btrim(name), '')),
    responsible_role = COALESCE(NULLIF(btrim(responsible_role), ''), 'source_organization')
WHERE responsible_name IS NULL OR responsible_role IS NULL;

UPDATE quran_transliteration_sources
SET responsible_name = COALESCE(NULLIF(btrim(responsible_name), ''), NULLIF(btrim(name), '')),
    responsible_role = COALESCE(NULLIF(btrim(responsible_role), ''), 'source_organization')
WHERE responsible_name IS NULL OR responsible_role IS NULL;

CREATE TABLE IF NOT EXISTS quran_script_sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    responsible_name TEXT,
    responsible_role TEXT,
    source_url TEXT,
    qul_resource_id TEXT,
    format TEXT NOT NULL,
    license_status TEXT NOT NULL DEFAULT 'needs_review'
        CHECK (license_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain')),
    license_reason TEXT,
    license_evidence_url TEXT,
    license_updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    license_updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    checksum TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    imported_at TIMESTAMPTZ,
    -- PK-1 permits only the exact script already public before the audit. This
    -- marker is never added by imports and restricted always revokes it.
    license_grandfathered_at TIMESTAMPTZ,
    license_grandfathered_checksum TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO quran_script_sources (
    id, name, responsible_name, responsible_role, source_url, qul_resource_id,
    format, license_status, checksum, imported_at, license_grandfathered_at,
    license_grandfathered_checksum
)
SELECT 'qpc-hafs',
       COALESCE(latest.source_name, 'QPC Hafs script - Ayah by Ayah'),
       COALESCE(latest.source_name, 'QPC Hafs script - Ayah by Ayah'),
       'source_organization',
       COALESCE(latest.source_url, 'https://qul.tarteel.ai/resources/quran-script/86'),
       COALESCE(latest.qul_resource_id, '86'),
       COALESCE(latest.format, 'json'),
       'needs_review',
       latest.checksum,
       latest.imported_at,
       CASE WHEN EXISTS (
           SELECT 1 FROM quran_ayahs WHERE NULLIF(btrim(text_qpc_hafs), '') IS NOT NULL
       ) THEN clock_timestamp() ELSE NULL END,
       CASE WHEN EXISTS (
           SELECT 1 FROM quran_ayahs WHERE NULLIF(btrim(text_qpc_hafs), '') IS NOT NULL
       ) THEN latest.checksum ELSE NULL END
FROM (VALUES (1)) seed(n)
LEFT JOIN LATERAL (
    SELECT r.source_name, r.source_url, r.qul_resource_id, r.format,
           r.checksum, r.imported_at
    FROM quran_import_runs r
    WHERE r.resource_type = 'script' AND r.qul_resource_id = '86'
    ORDER BY r.imported_at DESC, r.id DESC
    LIMIT 1
) latest ON TRUE
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS quran_source_license_audits (
    id BIGSERIAL PRIMARY KEY,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('script', 'translation', 'transliteration')),
    source_id TEXT NOT NULL,
    old_status TEXT NOT NULL CHECK (old_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain')),
    new_status TEXT NOT NULL CHECK (new_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain')),
    reason TEXT NOT NULL CHECK (btrim(reason) <> ''),
    evidence_url TEXT,
    old_attribution JSONB NOT NULL,
    new_attribution JSONB NOT NULL,
    actor_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_quran_source_license_audits_source_created
    ON quran_source_license_audits (source_kind, source_id, created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION quran_source_license_audit_guard() RETURNS TRIGGER AS $$
DECLARE
    source_kind_value TEXT := TG_ARGV[0];
    old_attribution_value JSONB;
    new_attribution_value JSONB;
BEGIN
    old_attribution_value := jsonb_build_object(
        'name', OLD.name,
        'translator', to_jsonb(OLD) ->> 'translator',
        'responsible_name', to_jsonb(OLD) ->> 'responsible_name',
        'responsible_role', to_jsonb(OLD) ->> 'responsible_role',
        'source_url', OLD.source_url
    );
    new_attribution_value := jsonb_build_object(
        'name', NEW.name,
        'translator', to_jsonb(NEW) ->> 'translator',
        'responsible_name', to_jsonb(NEW) ->> 'responsible_name',
        'responsible_role', to_jsonb(NEW) ->> 'responsible_role',
        'source_url', NEW.source_url
    );

    IF NEW.license_status IS NOT DISTINCT FROM OLD.license_status
       AND NEW.license_reason IS NOT DISTINCT FROM OLD.license_reason
       AND NEW.license_evidence_url IS NOT DISTINCT FROM OLD.license_evidence_url
       AND new_attribution_value IS NOT DISTINCT FROM old_attribution_value THEN
        RETURN NEW;
    END IF;

    IF NEW.license_updated_by IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'Quran source license audit requires an actor',
            CONSTRAINT = 'quran_source_license_audit_actor_check';
    END IF;
    IF btrim(COALESCE(NEW.license_reason, '')) = '' THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'Quran source license audit requires a non-empty reason',
            CONSTRAINT = 'quran_source_license_audit_reason_check';
    END IF;
    IF NEW.license_status = 'permitted'
       AND btrim(COALESCE(
           NULLIF(btrim(to_jsonb(NEW) ->> 'translator'), ''),
           NULLIF(btrim(to_jsonb(NEW) ->> 'responsible_name'), ''),
           ''
       )) = '' THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'permitted Quran sources require attribution',
            CONSTRAINT = 'quran_source_license_attribution_check';
    END IF;

    IF source_kind_value = 'script' AND NEW.license_status = 'restricted' THEN
        NEW.license_grandfathered_at := NULL;
        NEW.license_grandfathered_checksum := NULL;
    END IF;

    NEW.license_updated_at := GREATEST(
        clock_timestamp(),
        OLD.license_updated_at + INTERVAL '1 microsecond'
    );
    INSERT INTO quran_source_license_audits (
        source_kind, source_id, old_status, new_status, reason, evidence_url,
        old_attribution, new_attribution, actor_id, created_at
    ) VALUES (
        source_kind_value, OLD.id, OLD.license_status, NEW.license_status,
        NEW.license_reason, NEW.license_evidence_url, old_attribution_value,
        new_attribution_value, NEW.license_updated_by, NEW.license_updated_at
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_script_source_license_audit ON quran_script_sources;
CREATE TRIGGER trg_quran_script_source_license_audit
    BEFORE UPDATE OF license_status, license_reason, license_evidence_url,
        license_updated_by, license_updated_at, name, responsible_name,
        responsible_role, source_url
    ON quran_script_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_license_audit_guard('script');

DROP TRIGGER IF EXISTS trg_quran_translation_source_license_audit ON quran_translation_sources;
CREATE TRIGGER trg_quran_translation_source_license_audit
    BEFORE UPDATE OF license_status, license_reason, license_evidence_url,
        license_updated_by, license_updated_at, name, translator, responsible_name,
        responsible_role, source_url
    ON quran_translation_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_license_audit_guard('translation');

DROP TRIGGER IF EXISTS trg_quran_transliteration_source_license_audit ON quran_transliteration_sources;
CREATE TRIGGER trg_quran_transliteration_source_license_audit
    BEFORE UPDATE OF license_status, license_reason, license_evidence_url,
        license_updated_by, license_updated_at, name, responsible_name,
        responsible_role, source_url
    ON quran_transliteration_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_license_audit_guard('transliteration');

-- New source rows always enter the protected audit queue. A public state can
-- only be reached by the actor-attributed UPDATE path above.
CREATE OR REPLACE FUNCTION quran_source_initial_license_guard() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.license_status NOT IN ('unknown', 'needs_review') THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'new Quran sources must start unresolved',
            CONSTRAINT = 'quran_source_initial_license_check';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_script_source_initial_license ON quran_script_sources;
CREATE TRIGGER trg_quran_script_source_initial_license
    BEFORE INSERT ON quran_script_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_initial_license_guard();

DROP TRIGGER IF EXISTS trg_quran_translation_source_initial_license ON quran_translation_sources;
CREATE TRIGGER trg_quran_translation_source_initial_license
    BEFORE INSERT ON quran_translation_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_initial_license_guard();

DROP TRIGGER IF EXISTS trg_quran_transliteration_source_initial_license ON quran_transliteration_sources;
CREATE TRIGGER trg_quran_transliteration_source_initial_license
    BEFORE INSERT ON quran_transliteration_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_initial_license_guard();

-- The one-time grandfather marker belongs to this migration, not to runtime
-- writers. It may only move from populated to NULL as part of a restricted
-- decision; it can never be forged or revived later.
CREATE OR REPLACE FUNCTION quran_script_grandfather_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.license_grandfathered_at IS NOT NULL
           OR NEW.license_grandfathered_checksum IS NOT NULL THEN
            RAISE EXCEPTION USING
                ERRCODE = '23514',
                MESSAGE = 'Quran script grandfather marker is migration-owned',
                CONSTRAINT = 'quran_script_grandfather_immutable_check';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.license_grandfathered_at IS DISTINCT FROM OLD.license_grandfathered_at
       OR NEW.license_grandfathered_checksum IS DISTINCT FROM OLD.license_grandfathered_checksum THEN
        IF NOT (
            NEW.license_status = 'restricted'
            AND NEW.license_grandfathered_at IS NULL
            AND NEW.license_grandfathered_checksum IS NULL
        ) THEN
            RAISE EXCEPTION USING
                ERRCODE = '23514',
                MESSAGE = 'Quran script grandfather marker is immutable',
                CONSTRAINT = 'quran_script_grandfather_immutable_check';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_script_source_grandfather_guard ON quran_script_sources;
CREATE TRIGGER trg_quran_script_source_grandfather_guard
    BEFORE INSERT OR UPDATE OF license_grandfathered_at, license_grandfathered_checksum
    ON quran_script_sources
    FOR EACH ROW EXECUTE FUNCTION quran_script_grandfather_immutable_guard();

-- A source ID is a stable provenance identity. Importers may refresh content
-- only under that exact identity; changing language/resource/format requires a
-- new unresolved source ID.
CREATE OR REPLACE FUNCTION quran_source_identity_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR (to_jsonb(NEW) ->> 'lang') IS DISTINCT FROM (to_jsonb(OLD) ->> 'lang')
       OR (to_jsonb(NEW) ->> 'qul_resource_id') IS DISTINCT FROM (to_jsonb(OLD) ->> 'qul_resource_id')
       OR NEW.format IS DISTINCT FROM OLD.format THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'Quran source identity is immutable; use a new source id',
            CONSTRAINT = 'quran_source_identity_immutable_check';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_quran_script_source_identity_guard
    BEFORE UPDATE OF id, qul_resource_id, format ON quran_script_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_identity_immutable_guard();
CREATE TRIGGER trg_quran_translation_source_identity_guard
    BEFORE UPDATE OF id, lang, qul_resource_id, format ON quran_translation_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_identity_immutable_guard();
CREATE TRIGGER trg_quran_transliteration_source_identity_guard
    BEFORE UPDATE OF id, lang, format ON quran_transliteration_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_identity_immutable_guard();

-- A permitted/restricted decision is pinned to the reviewed release. Move the
-- source back to needs_review before importing a changed checksum/footnote set.
CREATE OR REPLACE FUNCTION quran_source_release_review_guard() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.license_status IN ('permitted', 'restricted')
       AND (
           NEW.checksum IS DISTINCT FROM OLD.checksum
           OR (
               TG_ARGV[0] = 'translation'
               AND (NEW.metadata ->> 'footnote_checksum')
                   IS DISTINCT FROM (OLD.metadata ->> 'footnote_checksum')
           )
       ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'reviewed Quran source release cannot change; move it to needs_review first',
            CONSTRAINT = 'quran_source_release_review_check';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_quran_script_source_release_guard
    BEFORE UPDATE OF checksum ON quran_script_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_release_review_guard('script');
CREATE TRIGGER trg_quran_translation_source_release_guard
    BEFORE UPDATE OF checksum, metadata ON quran_translation_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_release_review_guard('translation');
CREATE TRIGGER trg_quran_transliteration_source_release_guard
    BEFORE UPDATE OF checksum ON quran_transliteration_sources
    FOR EACH ROW EXECUTE FUNCTION quran_source_release_review_guard('transliteration');

CREATE OR REPLACE FUNCTION quran_source_license_audits_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION USING
        ERRCODE = '23514',
        MESSAGE = 'Quran source license audit history is append-only',
        CONSTRAINT = 'quran_source_license_audits_immutable_check';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_source_license_audits_immutable ON quran_source_license_audits;
CREATE TRIGGER trg_quran_source_license_audits_immutable
    BEFORE UPDATE OR DELETE ON quran_source_license_audits
    FOR EACH ROW EXECUTE FUNCTION quran_source_license_audits_immutable_guard();

CREATE OR REPLACE VIEW quran_source_license_inventory AS
SELECT 'script'::TEXT AS source_kind, s.id AS source_id, NULL::TEXT AS lang,
       s.name, NULL::TEXT AS translator, s.responsible_name, s.responsible_role,
       s.source_url, s.license_status, s.license_reason, s.license_evidence_url,
       s.license_updated_by, s.license_updated_at,
       (SELECT count(*)::INTEGER
          FROM quran_ayahs a
         WHERE NULLIF(btrim(a.text_qpc_hafs), '') IS NOT NULL) AS coverage_count,
       s.license_grandfathered_at
FROM quran_script_sources s
UNION ALL
SELECT 'translation', s.id, s.lang, s.name, s.translator, s.responsible_name,
       s.responsible_role, s.source_url, s.license_status, s.license_reason,
       s.license_evidence_url, s.license_updated_by, s.license_updated_at,
       s.coverage_count, NULL::TIMESTAMPTZ
FROM quran_translation_sources s
UNION ALL
SELECT 'transliteration', s.id, s.lang, s.name, NULL::TEXT, s.responsible_name,
       s.responsible_role, s.source_url, s.license_status, s.license_reason,
       s.license_evidence_url, s.license_updated_by, s.license_updated_at,
       s.coverage_count, NULL::TIMESTAMPTZ
FROM quran_transliteration_sources s;

-- Public source views are the only source tables the reader may use. Status is
-- deliberately literal: public_domain/unknown/needs_review are not permitted.
CREATE OR REPLACE VIEW public_quran_translation_sources AS
SELECT s.*
FROM quran_translation_sources s
WHERE s.license_status = 'permitted'
  AND btrim(COALESCE(NULLIF(btrim(s.translator), ''), NULLIF(btrim(s.responsible_name), ''), '')) <> '';

CREATE OR REPLACE VIEW public_quran_transliteration_sources AS
SELECT s.*
FROM quran_transliteration_sources s
WHERE s.license_status = 'permitted'
  AND btrim(COALESCE(s.responsible_name, '')) <> '';

CREATE OR REPLACE VIEW public_quran_script_sources AS
SELECT s.*
FROM quran_script_sources s
WHERE (s.license_status = 'permitted'
       OR (s.license_grandfathered_at IS NOT NULL
           AND s.license_status <> 'restricted'
           AND s.checksum IS NOT DISTINCT FROM s.license_grandfathered_checksum))
  AND btrim(COALESCE(s.responsible_name, '')) <> '';

ALTER TABLE citable_units
    DROP CONSTRAINT IF EXISTS citable_units_kind_check;
ALTER TABLE citable_units
    ADD CONSTRAINT citable_units_kind_check CHECK (
        kind IN ('paragraph', 'heading', 'quran_quote', 'footnote', 'html',
                 'primary_text', 'translation', 'transliteration')
    ) NOT VALID;
ALTER TABLE citable_units VALIDATE CONSTRAINT citable_units_kind_check;

-- This generated, non-overridable fact is the hard anti-tafsir boundary.
ALTER TABLE citable_units
    ADD COLUMN IF NOT EXISTS interpretive_corpus_eligible BOOLEAN
        GENERATED ALWAYS AS (corpus <> 'quran') STORED;

CREATE OR REPLACE FUNCTION citable_unit_corpus_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.corpus IS DISTINCT FROM OLD.corpus THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'Citable Unit corpus is immutable',
            CONSTRAINT = 'citable_unit_corpus_immutable_check';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_citable_unit_corpus_immutable ON citable_units;
CREATE TRIGGER trg_citable_unit_corpus_immutable
    BEFORE UPDATE OF corpus ON citable_units
    FOR EACH ROW EXECUTE FUNCTION citable_unit_corpus_immutable_guard();

ALTER TABLE citable_units
    ADD CONSTRAINT citable_units_quran_license_inherited_check
        CHECK (corpus <> 'quran' OR license_status IS NULL) NOT VALID;
ALTER TABLE citable_units VALIDATE CONSTRAINT citable_units_quran_license_inherited_check;

-- B-1's kitab indexes assumed a book scope. Keep them for kitab and add the
-- Quran natural key in the binding adapter below.
DROP INDEX uq_citable_units_scope_ordinal;
ALTER INDEX uq_citable_units_scope_ordinal_q2_kitab
    RENAME TO uq_citable_units_scope_ordinal;

DROP INDEX uq_citable_units_active_content;
ALTER INDEX uq_citable_units_active_content_q2_kitab
    RENAME TO uq_citable_units_active_content;

CREATE TABLE IF NOT EXISTS quran_citable_unit_bindings (
    unit_id UUID PRIMARY KEY REFERENCES citable_units(id) ON DELETE CASCADE,
    surah_id INTEGER NOT NULL,
    ayah_number INTEGER NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 1),
    role TEXT NOT NULL CHECK (role IN ('primary_text', 'translation', 'footnote', 'transliteration')),
    translation_source_id TEXT REFERENCES quran_translation_sources(id) ON DELETE RESTRICT,
    transliteration_source_id TEXT REFERENCES quran_transliteration_sources(id) ON DELETE RESTRICT,
    footnote_key TEXT,
    source_updated_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (surah_id, ayah_number)
        REFERENCES quran_ayahs(surah_id, ayah_number) ON DELETE RESTRICT,
    CONSTRAINT quran_citable_unit_bindings_role_shape_check CHECK (
        (role = 'primary_text' AND translation_source_id IS NULL
            AND transliteration_source_id IS NULL AND footnote_key IS NULL)
        OR (role = 'translation' AND translation_source_id IS NOT NULL
            AND transliteration_source_id IS NULL AND footnote_key IS NULL)
        OR (role = 'footnote' AND translation_source_id IS NOT NULL
            AND transliteration_source_id IS NULL AND btrim(COALESCE(footnote_key, '')) <> '')
        OR (role = 'transliteration' AND translation_source_id IS NULL
            AND transliteration_source_id IS NOT NULL AND footnote_key IS NULL)
    ),
    CONSTRAINT quran_citable_unit_bindings_ayah_ordinal_unique
        UNIQUE (surah_id, ayah_number, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_quran_citable_bindings_ayah_role
    ON quran_citable_unit_bindings (surah_id, ayah_number, role, ordinal);
CREATE INDEX IF NOT EXISTS idx_quran_citable_bindings_translation_source
    ON quran_citable_unit_bindings (translation_source_id, surah_id, ayah_number)
    WHERE translation_source_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_quran_citable_bindings_transliteration_source
    ON quran_citable_unit_bindings (transliteration_source_id, surah_id, ayah_number)
    WHERE transliteration_source_id IS NOT NULL;

DROP TRIGGER IF EXISTS trg_quran_citable_unit_bindings_guard ON quran_citable_unit_bindings;
CREATE TRIGGER trg_quran_citable_unit_bindings_guard
    BEFORE INSERT OR UPDATE OR DELETE ON quran_citable_unit_bindings
    FOR EACH ROW EXECUTE FUNCTION citable_registry_guard();

-- Extend B-4's virtual effective-license view; no license is copied to Quran
-- units. The already-public QPC script is represented explicitly as a
-- grandfathered source while a restricted decision revokes it immediately.
CREATE OR REPLACE VIEW citable_units_with_effective_license AS
SELECT u.id,
       u.corpus,
       u.book_id,
       u.heading_id,
       u.page_id,
       u.anchor,
       u.lifecycle,
       u.license_status,
       CASE
           WHEN u.corpus = 'quran' THEN COALESCE(
               ts.license_status,
               xs.license_status,
               CASE
                   WHEN ss.license_status = 'permitted' THEN 'permitted'
                   WHEN ss.license_grandfathered_at IS NOT NULL
                        AND ss.license_status <> 'restricted'
                        AND ss.checksum IS NOT DISTINCT FROM ss.license_grandfathered_checksum
                       THEN 'permitted'
                   ELSE ss.license_status
               END
           )
           ELSE COALESCE(u.license_status, b.license_status)
       END AS effective_license_status,
       CASE
           WHEN u.corpus <> 'quran' AND u.license_status IS NOT NULL THEN 'unit_override'::TEXT
           WHEN u.corpus = 'kitab' AND b.license_status IS NOT NULL THEN 'edition'::TEXT
           WHEN qb.role IN ('translation', 'footnote') THEN 'quran_translation_source'::TEXT
           WHEN qb.role = 'transliteration' THEN 'quran_transliteration_source'::TEXT
           WHEN qb.role = 'primary_text' AND ss.license_grandfathered_at IS NOT NULL
               AND ss.license_status <> 'restricted'
               AND ss.checksum IS NOT DISTINCT FROM ss.license_grandfathered_checksum
               THEN 'quran_script_grandfather'::TEXT
           WHEN qb.role = 'primary_text' THEN 'quran_script_source'::TEXT
           ELSE NULL::TEXT
       END AS license_source
FROM citable_units u
LEFT JOIN books b ON b.id = u.book_id AND u.corpus = 'kitab'
LEFT JOIN quran_citable_unit_bindings qb ON qb.unit_id = u.id AND u.corpus = 'quran'
LEFT JOIN quran_translation_sources ts ON ts.id = qb.translation_source_id
LEFT JOIN quran_transliteration_sources xs ON xs.id = qb.transliteration_source_id
LEFT JOIN quran_script_sources ss ON ss.id = 'qpc-hafs' AND qb.role = 'primary_text';

-- Preserve B-3/B-4's visibility implementation and wrap it additively for the
-- new Quran child Anchor profile. Logical Quran surah/ayah behavior remains
-- byte-for-byte delegated to the already-live function.
ALTER FUNCTION cross_reference_anchor_point_visible(TEXT)
    RENAME TO cross_reference_anchor_point_visible_pre_q2;

CREATE FUNCTION cross_reference_anchor_point_visible(point_value TEXT)
RETURNS BOOLEAN AS $$
DECLARE
    captures TEXT[];
    target_surah INTEGER;
    target_ayah INTEGER;
    has_active BOOLEAN;
    crossed_ayah BOOLEAN;
BEGIN
    captures := regexp_match(
        point_value,
        '^quran/([1-9][0-9]*):([1-9][0-9]*)/u/([1-9][0-9]*)$'
    );
    IF captures IS NULL THEN
        RETURN cross_reference_anchor_point_visible_pre_q2(point_value);
    END IF;

    target_surah := captures[1]::INTEGER;
    target_ayah := captures[2]::INTEGER;

    WITH RECURSIVE walk(id) AS (
        SELECT id FROM citable_units WHERE anchor = point_value
        UNION
        SELECT l.successor_id
        FROM walk w
        JOIN citable_unit_lineage l ON l.predecessor_id = w.id
    ), candidates AS (
        SELECT u.id, u.lifecycle, u.text,
               b.surah_id, b.ayah_number, b.role, b.source_updated_at,
               license.effective_license_status, s.units_stale_at,
               a.updated_at AS ayah_updated_at, a.text_qpc_hafs,
               t.updated_at AS translation_updated_at, t.text AS translation_text,
               x.updated_at AS transliteration_updated_at, x.text AS transliteration_text
        FROM walk w
        JOIN citable_units u ON u.id = w.id
        LEFT JOIN quran_citable_unit_bindings b ON b.unit_id = u.id
        LEFT JOIN citable_units_with_effective_license license ON license.id = u.id
        LEFT JOIN quran_ayahs a
          ON a.surah_id = b.surah_id AND a.ayah_number = b.ayah_number
        LEFT JOIN quran_surahs s ON s.surah_id = b.surah_id
        LEFT JOIN quran_ayah_translations t
          ON t.source_id = b.translation_source_id
         AND t.surah_id = b.surah_id AND t.ayah_number = b.ayah_number
        LEFT JOIN quran_ayah_transliterations x
          ON x.source_id = b.transliteration_source_id
         AND x.surah_id = b.surah_id AND x.ayah_number = b.ayah_number
    )
    SELECT COALESCE(bool_or(
               lifecycle = 'active'
               AND surah_id = target_surah
               AND ayah_number = target_ayah
               AND effective_license_status = 'permitted'
               AND units_stale_at IS NULL
               AND CASE role
                   WHEN 'primary_text' THEN source_updated_at = ayah_updated_at AND text = text_qpc_hafs
                   WHEN 'translation' THEN source_updated_at = translation_updated_at AND text = translation_text
                   WHEN 'footnote' THEN source_updated_at = translation_updated_at
                   WHEN 'transliteration' THEN source_updated_at = transliteration_updated_at AND text = transliteration_text
                   ELSE FALSE
               END
           ), FALSE),
           COALESCE(bool_or(
               surah_id IS NULL OR surah_id <> target_surah OR ayah_number <> target_ayah
           ), FALSE)
    INTO has_active, crossed_ayah
    FROM candidates;

    RETURN has_active AND NOT crossed_ayah;
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

CREATE OR REPLACE FUNCTION quran_units_mark_stale() RETURNS TRIGGER AS $$
DECLARE
    target_surah_id INTEGER;
BEGIN
    target_surah_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.surah_id ELSE NEW.surah_id END;
    UPDATE quran_surahs
    SET units_stale_at = GREATEST(COALESCE(units_stale_at, '-infinity'::TIMESTAMPTZ), clock_timestamp())
    WHERE surah_id = target_surah_id;
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION quran_primary_text_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    IF NULLIF(btrim(OLD.text_qpc_hafs), '') IS NOT NULL
       AND NEW.text_qpc_hafs IS DISTINCT FROM OLD.text_qpc_hafs THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'Quran primary text is immutable',
            CONSTRAINT = 'quran_primary_text_immutable_check';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_primary_text_immutable ON quran_ayahs;
CREATE TRIGGER trg_quran_primary_text_immutable
    BEFORE UPDATE OF text_qpc_hafs ON quran_ayahs
    FOR EACH ROW EXECUTE FUNCTION quran_primary_text_immutable_guard();

DROP TRIGGER IF EXISTS trg_quran_ayah_units_stale ON quran_ayahs;
CREATE TRIGGER trg_quran_ayah_units_stale
    AFTER INSERT OR UPDATE OF text_qpc_hafs, page_number OR DELETE ON quran_ayahs
    FOR EACH ROW EXECUTE FUNCTION quran_units_mark_stale();

DROP TRIGGER IF EXISTS trg_quran_translation_units_stale ON quran_ayah_translations;
CREATE TRIGGER trg_quran_translation_units_stale
    AFTER INSERT OR UPDATE OF text, footnotes OR DELETE ON quran_ayah_translations
    FOR EACH ROW EXECUTE FUNCTION quran_units_mark_stale();

DROP TRIGGER IF EXISTS trg_quran_transliteration_units_stale ON quran_ayah_transliterations;
CREATE TRIGGER trg_quran_transliteration_units_stale
    AFTER INSERT OR UPDATE OF text OR DELETE ON quran_ayah_transliterations
    FOR EACH ROW EXECUTE FUNCTION quran_units_mark_stale();
