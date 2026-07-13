CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_citable_units_interpretive_retrieval
    ON citable_units (book_id, heading_id, page_id, position, id)
    WHERE lifecycle = 'active'
      AND corpus = 'kitab'
      AND interpretive_retrieval_eligible;
