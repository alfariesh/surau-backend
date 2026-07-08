-- F1-G: statement-level performance visibility for the postgres-exporter
-- "slow statements" panels. CREATE EXTENSION succeeds even while the server
-- does not (yet) run with shared_preload_libraries=pg_stat_statements —
-- querying the view simply errors until the db container is recreated with
-- the preload flag (docker-compose db command). Rollout order documented in
-- docs/deploy-vps.md §Tuning Postgres.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
