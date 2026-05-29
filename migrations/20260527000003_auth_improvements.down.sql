DROP TABLE IF EXISTS auth_audit_logs;

DROP TABLE IF EXISTS auth_rate_limits;

ALTER TABLE users
    DROP COLUMN IF EXISTS token_version;
