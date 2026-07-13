CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_citable_units_text_fts_interpretive
    ON citable_units USING gin (
        to_tsvector('simple'::regconfig, translate(text, 'ًٌٍَُِّْٰٕٓٔـ', ''))
    )
    WHERE lifecycle = 'active'
      AND corpus = 'kitab'
      AND interpretive_retrieval_eligible
      AND content_role = 'book_page'
      AND heading_id IS NOT NULL
      AND page_id IS NOT NULL;
