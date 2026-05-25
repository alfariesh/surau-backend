CREATE INDEX IF NOT EXISTS idx_book_pages_content_text_trgm ON book_pages USING gin (content_text gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_section_translations_content_trgm ON section_translations USING gin (content gin_trgm_ops);
