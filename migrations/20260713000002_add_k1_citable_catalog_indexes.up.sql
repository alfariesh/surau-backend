-- Online replacement for B-1's kitab-only uniqueness indexes. v1 keeps the
-- pilot Arabic page identity exactly; v2 gives enrichment its own role/lang
-- namespace so translated content can never collide with a source paragraph.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_citable_units_scope_ordinal_v1_book_page
    ON citable_units (corpus, book_id, heading_id, ordinal) NULLS NOT DISTINCT
    WHERE corpus = 'kitab' AND content_role = 'book_page' AND language = 'ar';
