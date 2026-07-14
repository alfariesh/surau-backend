-- Validation is deliberately isolated from the expand/repair migration. New
-- writes have already been enforced since the NOT VALID constraint was added.
ALTER TABLE citable_units
    VALIDATE CONSTRAINT citable_units_parent_shape_check;
