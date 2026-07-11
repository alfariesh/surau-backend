-- Kept separate so PostgreSQL can build the traffic-priority index without
-- blocking saved-item writes on the live catalog.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_saved_items_book_updated
    ON saved_items (book_id, updated_at DESC)
    WHERE book_id IS NOT NULL;
