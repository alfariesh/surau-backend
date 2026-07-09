-- Rollback of the B-1 Citable Unit registry EXPAND step.
-- Drop order: lineage first (FK), then units (dropping tables drops their
-- triggers), then the guard function explicitly (CI round-trip asserts no
-- app-owned functions remain), then the books marker column.

ALTER TABLE books DROP COLUMN IF EXISTS units_derived_at;

DROP TABLE IF EXISTS citable_unit_lineage;
DROP TABLE IF EXISTS citable_units;

DROP FUNCTION IF EXISTS citable_registry_guard();
