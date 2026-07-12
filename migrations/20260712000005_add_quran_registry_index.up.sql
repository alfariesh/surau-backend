CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_citable_units_quran
    ON citable_units (id)
    WHERE corpus = 'quran';
