-- F1-H: resumable-backfill machinery + first expand column.
--
-- golang-migrate runs every statement in its own autocommit transaction, so
-- statement order is load-bearing. Everything here is additive: no rewrite,
-- no lock beyond a brief ACCESS EXCLUSIVE for the ADD COLUMN (metadata-only),
-- safe for the deploy-time auto-migration. Pattern documented in
-- docs/data-change-playbook.md.

-- 1) Checkpoint table: one row per backfill job. The CLI runner
--    (cmd/backfill) upserts cursor/progress after every chunk, which is what
--    makes a stopped job resumable without losing progress; the app's
--    metrics collector reads it at scrape time (surau_backfill_*).
CREATE TABLE IF NOT EXISTS backfill_jobs (
    job_name TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'running',
    last_cursor BIGINT NOT NULL DEFAULT 0,
    rows_total BIGINT NOT NULL DEFAULT 0,
    rows_done BIGINT NOT NULL DEFAULT 0,
    profile_version INTEGER,
    error TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    CONSTRAINT backfill_jobs_status_check
        CHECK (status IN ('running', 'paused', 'completed', 'failed'))
);

-- 2) Expand (additive, nullable): canonical normalized author name for
--    hamza-insensitive public author search. NULL until the
--    authors-name-search backfill fills it; kept fresh going forward by the
--    importer upsert. Readers treat NULL as "not searchable via the
--    normalized arm" — never an error. Contract step (index strategy /
--    NOT NULL) intentionally deferred; see the playbook.
ALTER TABLE authors ADD COLUMN IF NOT EXISTS name_search TEXT;
