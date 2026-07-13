CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_citable_units_text_trgm_interpretive
    ON citable_units USING gin (text gin_trgm_ops)
    WHERE lifecycle = 'active'
      AND corpus = 'kitab'
      AND interpretive_retrieval_eligible;
