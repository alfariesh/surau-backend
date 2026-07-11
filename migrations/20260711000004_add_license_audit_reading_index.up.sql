-- Kept separate so PostgreSQL can build the traffic-priority index without
-- blocking reading-progress writes on the live catalog.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_reading_progress_book_updated
    ON reading_progress (book_id, updated_at DESC);
