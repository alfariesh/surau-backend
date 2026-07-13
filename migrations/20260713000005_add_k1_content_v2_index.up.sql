CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_citable_units_active_content_v2_enrichment
    ON citable_units (
        corpus, book_id, heading_id, content_role, language, kind, content_hash, occurrence
    ) NULLS NOT DISTINCT
    WHERE lifecycle = 'active'
      AND corpus = 'kitab'
      AND NOT (content_role = 'book_page' AND language = 'ar');
