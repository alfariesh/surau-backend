ALTER TABLE users
    ADD COLUMN IF NOT EXISTS token_version BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS auth_rate_limits (
    action VARCHAR(64) NOT NULL,
    key_hash CHAR(64) NOT NULL,
    window_start TIMESTAMP NOT NULL,
    window_seconds BIGINT NOT NULL,
    count INTEGER NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    PRIMARY KEY (action, key_hash, window_start)
);

CREATE INDEX IF NOT EXISTS idx_auth_rate_limits_expires_at
    ON auth_rate_limits(expires_at);

CREATE TABLE IF NOT EXISTS auth_audit_logs (
    id UUID PRIMARY KEY,
    event VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    user_id UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    email VARCHAR(255) NULL,
    client_ip TEXT NULL,
    user_agent TEXT NULL,
    error_code VARCHAR(128) NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_auth_audit_logs_user_created
    ON auth_audit_logs(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_auth_audit_logs_event_created
    ON auth_audit_logs(event, created_at DESC);
