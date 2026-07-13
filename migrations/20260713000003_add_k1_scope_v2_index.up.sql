CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_citable_units_scope_ordinal_v2_enrichment
    ON citable_units (corpus, book_id, heading_id, content_role, language, ordinal)
    NULLS NOT DISTINCT
    WHERE corpus = 'kitab'
      AND NOT (content_role = 'book_page' AND language = 'ar');
