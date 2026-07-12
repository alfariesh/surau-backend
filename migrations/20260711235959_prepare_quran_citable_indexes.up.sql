-- Q-2 expand step: build the replacement kitab-only uniqueness indexes
-- without blocking registry writers. The core migration swaps these names in
-- one transactional metadata operation before any Quran unit can be minted.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_citable_units_scope_ordinal_q2_kitab
    ON citable_units (corpus, book_id, heading_id, ordinal) NULLS NOT DISTINCT
    WHERE corpus = 'kitab';
