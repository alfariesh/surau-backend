DROP TRIGGER IF EXISTS trg_citable_unit_parent_invariant ON citable_units;
DROP FUNCTION IF EXISTS citable_unit_parent_invariant_guard();

ALTER TABLE citable_units
    DROP CONSTRAINT IF EXISTS citable_units_parent_shape_check;
