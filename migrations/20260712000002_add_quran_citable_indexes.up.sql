-- F1-H online indexes for Q-2 read/reconcile paths.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_citable_units_interpretive_active
    ON citable_units (corpus, id)
    WHERE lifecycle = 'active' AND interpretive_corpus_eligible;
