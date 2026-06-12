-- Indexes for hot user-facing query paths that previously fell back to
-- composite indexes with the wrong leading column or to full scans.

-- Retention cleanup deletes by bare created_at range
-- (DELETE FROM auth_audit_logs WHERE created_at <= $1); the existing
-- (user_id, created_at) and (event, created_at) indexes cannot serve it.
CREATE INDEX IF NOT EXISTS idx_auth_audit_logs_created_at
    ON auth_audit_logs(created_at);

-- Continue-reading shelf orders by updated_at per user and /me/sync filters
-- updated_at >= cutoff per user; the PK (user_id, book_id) cannot serve either.
CREATE INDEX IF NOT EXISTS idx_reading_progress_user_updated
    ON reading_progress(user_id, updated_at DESC);

-- /me/sync filters khatam cycles by user_id + updated_at; existing indexes
-- are partial (active-only unique, completed-only history).
CREATE INDEX IF NOT EXISTS idx_quran_khatam_cycles_user_updated
    ON quran_khatam_cycles(user_id, updated_at DESC);

-- Intentionally skipped: saved_items already has (user_id, updated_at DESC)
-- from 20260530000003, and quran_reading_progress holds at most 114 rows per
-- user under its (user_id, surah_id) PK, so an updated_at twin buys nothing.
