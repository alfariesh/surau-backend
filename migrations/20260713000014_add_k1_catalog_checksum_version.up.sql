-- K-1 operational evidence: version the registry checksum algorithm so the
-- online row-wise v2 upgrade can preserve already-completed book commits
-- without confusing v1 and v2 digests.

ALTER TABLE citable_unit_catalog_queue
    ADD COLUMN IF NOT EXISTS checksum_version INTEGER NOT NULL DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'citable_unit_catalog_queue'::regclass
          AND conname = 'citable_unit_catalog_queue_checksum_version_check'
    ) THEN
        ALTER TABLE citable_unit_catalog_queue
            ADD CONSTRAINT citable_unit_catalog_queue_checksum_version_check
            CHECK (checksum_version >= 1) NOT VALID;
    END IF;
END;
$$;

ALTER TABLE citable_unit_catalog_queue
    VALIDATE CONSTRAINT citable_unit_catalog_queue_checksum_version_check;
