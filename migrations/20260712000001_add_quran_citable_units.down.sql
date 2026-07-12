DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM quran_source_license_audits)
       OR EXISTS (
           SELECT 1 FROM quran_script_sources
           WHERE license_status <> 'needs_review'
              OR license_reason IS NOT NULL
              OR license_evidence_url IS NOT NULL
              OR license_updated_by IS NOT NULL
       ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'refusing Q-2 rollback: Quran license decisions must be preserved';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS trg_quran_primary_text_immutable ON quran_ayahs;
DROP FUNCTION IF EXISTS quran_primary_text_immutable_guard();

DROP TRIGGER IF EXISTS trg_quran_transliteration_units_stale ON quran_ayah_transliterations;
DROP TRIGGER IF EXISTS trg_quran_translation_units_stale ON quran_ayah_translations;
DROP TRIGGER IF EXISTS trg_quran_ayah_units_stale ON quran_ayahs;
DROP FUNCTION IF EXISTS quran_units_mark_stale();

CREATE OR REPLACE FUNCTION cross_reference_anchor_visible(anchor_value TEXT)
RETURNS BOOLEAN AS $$
DECLARE
    boundaries TEXT[];
BEGIN
    boundaries := string_to_array(anchor_value, '..');
    IF array_length(boundaries, 1) = 1 THEN
        RETURN cross_reference_anchor_point_visible_pre_q2(boundaries[1]);
    END IF;
    IF array_length(boundaries, 1) = 2 THEN
        RETURN cross_reference_anchor_point_visible_pre_q2(boundaries[1])
           AND cross_reference_anchor_point_visible_pre_q2(boundaries[2]);
    END IF;
    RETURN FALSE;
END;
$$ LANGUAGE plpgsql STABLE COST 10;

DROP FUNCTION IF EXISTS cross_reference_anchor_point_visible(TEXT);
ALTER FUNCTION cross_reference_anchor_point_visible_pre_q2(TEXT)
    RENAME TO cross_reference_anchor_point_visible;

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

CREATE OR REPLACE VIEW citable_units_with_effective_license AS
SELECT u.id,
       u.corpus,
       u.book_id,
       u.heading_id,
       u.page_id,
       u.anchor,
       u.lifecycle,
       u.license_status,
       COALESCE(u.license_status, b.license_status) AS effective_license_status,
       CASE
           WHEN u.license_status IS NOT NULL THEN 'unit_override'::TEXT
           WHEN u.corpus = 'kitab' AND b.license_status IS NOT NULL THEN 'edition'::TEXT
           ELSE NULL::TEXT
       END AS license_source
FROM citable_units u
LEFT JOIN books b ON b.id = u.book_id AND u.corpus = 'kitab';

DO $$
BEGIN
    PERFORM set_config('surau.registry_writer', 'unit-service', true);
    DELETE FROM citable_units WHERE corpus = 'quran';
END;
$$;

DROP TRIGGER IF EXISTS trg_quran_citable_unit_bindings_guard ON quran_citable_unit_bindings;
DROP TABLE IF EXISTS quran_citable_unit_bindings;

DROP INDEX IF EXISTS uq_citable_units_active_content;
CREATE UNIQUE INDEX uq_citable_units_active_content
    ON citable_units (corpus, book_id, heading_id, kind, content_hash, occurrence)
    NULLS NOT DISTINCT
    WHERE lifecycle = 'active';

DROP INDEX IF EXISTS uq_citable_units_scope_ordinal;
CREATE UNIQUE INDEX uq_citable_units_scope_ordinal
    ON citable_units (corpus, book_id, heading_id, ordinal) NULLS NOT DISTINCT;

DROP TRIGGER IF EXISTS trg_citable_unit_corpus_immutable ON citable_units;
DROP FUNCTION IF EXISTS citable_unit_corpus_immutable_guard();
ALTER TABLE citable_units DROP CONSTRAINT IF EXISTS citable_units_quran_license_inherited_check;
ALTER TABLE citable_units DROP COLUMN IF EXISTS interpretive_corpus_eligible;

ALTER TABLE citable_units DROP CONSTRAINT IF EXISTS citable_units_kind_check;
ALTER TABLE citable_units ADD CONSTRAINT citable_units_kind_check
    CHECK (kind IN ('paragraph', 'heading', 'quran_quote', 'footnote', 'html'));

DROP VIEW IF EXISTS public_quran_script_sources;
DROP VIEW IF EXISTS public_quran_transliteration_sources;
DROP VIEW IF EXISTS public_quran_translation_sources;
DROP VIEW IF EXISTS quran_source_license_inventory;

DROP TRIGGER IF EXISTS trg_quran_source_license_audits_immutable ON quran_source_license_audits;
DROP TRIGGER IF EXISTS trg_quran_transliteration_source_release_guard ON quran_transliteration_sources;
DROP TRIGGER IF EXISTS trg_quran_translation_source_release_guard ON quran_translation_sources;
DROP TRIGGER IF EXISTS trg_quran_script_source_release_guard ON quran_script_sources;
DROP TRIGGER IF EXISTS trg_quran_transliteration_source_identity_guard ON quran_transliteration_sources;
DROP TRIGGER IF EXISTS trg_quran_translation_source_identity_guard ON quran_translation_sources;
DROP TRIGGER IF EXISTS trg_quran_script_source_identity_guard ON quran_script_sources;
DROP TRIGGER IF EXISTS trg_quran_script_source_grandfather_guard ON quran_script_sources;
DROP TRIGGER IF EXISTS trg_quran_transliteration_source_initial_license ON quran_transliteration_sources;
DROP TRIGGER IF EXISTS trg_quran_translation_source_initial_license ON quran_translation_sources;
DROP TRIGGER IF EXISTS trg_quran_script_source_initial_license ON quran_script_sources;
DROP TRIGGER IF EXISTS trg_quran_transliteration_source_license_audit ON quran_transliteration_sources;
DROP TRIGGER IF EXISTS trg_quran_translation_source_license_audit ON quran_translation_sources;
DROP TRIGGER IF EXISTS trg_quran_script_source_license_audit ON quran_script_sources;
DROP FUNCTION IF EXISTS quran_source_license_audits_immutable_guard();
DROP FUNCTION IF EXISTS quran_source_release_review_guard();
DROP FUNCTION IF EXISTS quran_source_identity_immutable_guard();
DROP FUNCTION IF EXISTS quran_script_grandfather_immutable_guard();
DROP FUNCTION IF EXISTS quran_source_initial_license_guard();
DROP FUNCTION IF EXISTS quran_source_license_audit_guard();
DROP TABLE IF EXISTS quran_source_license_audits;
DROP TABLE IF EXISTS quran_script_sources;

ALTER TABLE quran_transliteration_sources
    DROP COLUMN IF EXISTS license_updated_at,
    DROP COLUMN IF EXISTS license_updated_by,
    DROP COLUMN IF EXISTS license_evidence_url,
    DROP COLUMN IF EXISTS license_reason,
    DROP COLUMN IF EXISTS responsible_role,
    DROP COLUMN IF EXISTS responsible_name;

ALTER TABLE quran_translation_sources
    DROP COLUMN IF EXISTS license_updated_at,
    DROP COLUMN IF EXISTS license_updated_by,
    DROP COLUMN IF EXISTS license_evidence_url,
    DROP COLUMN IF EXISTS license_reason,
    DROP COLUMN IF EXISTS responsible_role,
    DROP COLUMN IF EXISTS responsible_name;

ALTER TABLE quran_surahs
    DROP COLUMN IF EXISTS units_stale_at,
    DROP COLUMN IF EXISTS units_derived_at;
