-- K-1 profile v3 retains heading-only published pages as exact HTML fallback
-- units and repairs historical footnote parent metadata before the catalog is
-- rederived. The public structural view stays fail-closed until each book has
-- completed the new profile.

UPDATE books
SET units_stale_at = COALESCE(units_stale_at, clock_timestamp())
WHERE units_derived_at IS NOT NULL
  AND units_derivation_profile_version IS DISTINCT FROM 3;

DO $$
BEGIN
    PERFORM set_config('surau.registry_writer', 'unit-service', true);

    -- Preserve an existing footnote relationship when lineage has exactly one
    -- active successor. Fan-out is deliberately not guessed.
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

    -- A missing or ambiguous parent is represented honestly as unlinked. This
    -- is fail-closed and preferable to retaining a dangling semantic edge.
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
    WHERE unit.lifecycle = 'active'
      AND NOT (
          unit.kind = 'footnote'
          OR (unit.kind = 'quran_quote' AND unit.marker IS NOT NULL)
      )
      AND unit.parent_unit_id IS NOT NULL;
END;
$$;

CREATE OR REPLACE VIEW public_book_interpretive_citable_units AS
SELECT unit.*
FROM citable_units unit
JOIN public_book_publications publication ON publication.book_id = unit.book_id
JOIN books book ON book.id = unit.book_id
WHERE unit.corpus = 'kitab'
  AND unit.lifecycle = 'active'
  AND unit.interpretive_retrieval_eligible
  AND (unit.license_status IS NULL OR unit.license_status = 'permitted')
  AND book.units_derived_at IS NOT NULL
  AND book.units_stale_at IS NULL
  AND book.units_derivation_profile_version = 3;
