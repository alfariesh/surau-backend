-- Progressive login lockout counters, keyed by a hash of the login email so
-- failures are tracked whether or not the account exists (no enumeration
-- leak). Rows reset on successful login and age out via the cleanup job.
CREATE TABLE IF NOT EXISTS auth_login_lockouts (
    key_hash CHAR(64) PRIMARY KEY,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    locked_until TIMESTAMP NULL,
    last_failure_at TIMESTAMP NOT NULL DEFAULT now(),
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_auth_login_lockouts_last_failure
    ON auth_login_lockouts (last_failure_at);
