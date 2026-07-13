CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_citable_unit_catalog_queue_claim
    ON citable_unit_catalog_queue (job_name, status, sequence)
    WHERE status IN ('pending', 'failed');
