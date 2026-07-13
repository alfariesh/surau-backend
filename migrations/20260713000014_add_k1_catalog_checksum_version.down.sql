ALTER TABLE citable_unit_catalog_queue
    DROP CONSTRAINT IF EXISTS citable_unit_catalog_queue_checksum_version_check;

ALTER TABLE citable_unit_catalog_queue
    DROP COLUMN IF EXISTS checksum_version;
