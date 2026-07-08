-- Rollback of the F1-H expand step: drop the derived column and the
-- checkpoint table. Both are additive and re-derivable (the backfill job
-- rebuilds name_search from authors.name), so this down migration loses no
-- source-of-truth data.
ALTER TABLE authors DROP COLUMN IF EXISTS name_search;

DROP TABLE IF EXISTS backfill_jobs;
