CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_citable_units_active_content_v1_book_page
    ON citable_units (corpus, book_id, heading_id, kind, content_hash, occurrence)
    NULLS NOT DISTINCT
    WHERE lifecycle = 'active'
      AND corpus = 'kitab'
      AND content_role = 'book_page'
      AND language = 'ar';
