ALTER TABLE citable_units
    DROP CONSTRAINT IF EXISTS citable_units_parent_shape_check;

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
