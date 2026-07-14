-- Repair legacy parent metadata once more after the profile-v3 catalog pass,
-- then make the accepted shape structural. Profile-v3 exposed that a later
-- reconcile could otherwise reintroduce the same historical parent debt.
DO $$
BEGIN
    PERFORM set_config('surau.registry_writer', 'unit-service', true);

    WITH RECURSIVE invalid AS (
        SELECT footnote.id AS footnote_id,
               footnote.parent_unit_id AS root_parent_id
        FROM citable_units footnote
        JOIN citable_units parent ON parent.id = footnote.parent_unit_id
        WHERE footnote.lifecycle = 'active'
          AND (
              footnote.kind = 'footnote'
              OR (footnote.kind = 'quran_quote' AND footnote.marker IS NOT NULL)
          )
          AND parent.lifecycle <> 'active'
    ), walk AS (
        SELECT footnote_id, root_parent_id AS unit_id, ARRAY[root_parent_id] AS path
        FROM invalid
        UNION ALL
        SELECT walk.footnote_id, lineage.successor_id, walk.path || lineage.successor_id
        FROM walk
        JOIN citable_unit_lineage lineage ON lineage.predecessor_id = walk.unit_id
        WHERE NOT lineage.successor_id = ANY(walk.path)
    ), active_candidates AS (
        SELECT walk.footnote_id,
               (array_agg(walk.unit_id ORDER BY walk.unit_id))[1] AS successor_id
        FROM walk
        JOIN citable_units candidate ON candidate.id = walk.unit_id
        WHERE candidate.lifecycle = 'active'
        GROUP BY walk.footnote_id
        HAVING COUNT(DISTINCT walk.unit_id) = 1
    )
    UPDATE citable_units footnote
    SET parent_unit_id = candidate.successor_id
    FROM active_candidates candidate
    WHERE footnote.id = candidate.footnote_id;

    UPDATE citable_units footnote
    SET parent_unit_id = NULL,
        provenance_detail = jsonb_set(
            COALESCE(footnote.provenance_detail, '{}'::jsonb),
            '{footnote_link}',
            '"unlinked"'::jsonb,
            true
        )
    WHERE footnote.lifecycle = 'active'
      AND (
          footnote.kind = 'footnote'
          OR (footnote.kind = 'quran_quote' AND footnote.marker IS NOT NULL)
      )
      AND (
          footnote.parent_unit_id IS NULL
          OR NOT EXISTS (
              SELECT 1
              FROM citable_units parent
              WHERE parent.id = footnote.parent_unit_id
                AND parent.lifecycle = 'active'
          )
      );

    UPDATE citable_units unit
    SET parent_unit_id = NULL
    WHERE NOT (
        unit.kind = 'footnote'
        OR (unit.kind = 'quran_quote' AND unit.marker IS NOT NULL)
    )
      AND unit.parent_unit_id IS NOT NULL;
END;
$$;

ALTER TABLE citable_units
    ADD CONSTRAINT citable_units_parent_shape_check
    CHECK (
        lifecycle <> 'active'
        OR CASE
            WHEN kind = 'footnote' OR (kind = 'quran_quote' AND marker IS NOT NULL)
                THEN parent_unit_id IS NOT NULL
                     OR COALESCE(provenance_detail->>'footnote_link', '') = 'unlinked'
            ELSE parent_unit_id IS NULL
        END
    ) NOT VALID;

-- Parent lifecycle is a cross-row invariant, so it is checked at transaction
-- end: reconcile may supersede a body before re-pointing its footnote, but it
-- may never commit that intermediate state.
CREATE OR REPLACE FUNCTION citable_unit_parent_invariant_guard() RETURNS TRIGGER AS $$
DECLARE
    unit_id UUID := COALESCE(NEW.id, OLD.id);
    current_unit citable_units%ROWTYPE;
BEGIN
    SELECT * INTO current_unit
    FROM citable_units
    WHERE id = unit_id;

    IF FOUND AND current_unit.lifecycle = 'active' AND current_unit.parent_unit_id IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1
            FROM citable_units parent
            WHERE parent.id = current_unit.parent_unit_id
              AND parent.lifecycle = 'active'
        ) THEN
            RAISE EXCEPTION 'active Citable Unit % references a non-active parent', unit_id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM citable_units child
        WHERE child.lifecycle = 'active'
          AND child.parent_unit_id = unit_id
    ) AND (NOT FOUND OR current_unit.lifecycle <> 'active') THEN
        RAISE EXCEPTION 'Citable Unit % has an active child but is not active', unit_id
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_citable_unit_parent_invariant
    AFTER INSERT OR UPDATE OR DELETE ON citable_units
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION citable_unit_parent_invariant_guard();
