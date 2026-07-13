CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_citable_units_scope_ordinal
    ON citable_units (corpus, book_id, heading_id, ordinal) NULLS NOT DISTINCT
    WHERE corpus = 'kitab';
