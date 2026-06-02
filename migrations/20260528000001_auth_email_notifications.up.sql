CREATE TABLE IF NOT EXISTS auth_login_fingerprints (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    fingerprint_hash CHAR(64) NOT NULL,
    client_ip TEXT NULL,
    user_agent TEXT NULL,
    first_seen_at TIMESTAMP NOT NULL,
    last_seen_at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, fingerprint_hash)
);

CREATE INDEX IF NOT EXISTS idx_auth_login_fingerprints_user_last_seen
    ON auth_login_fingerprints(user_id, last_seen_at DESC);

CREATE TABLE IF NOT EXISTS auth_notification_cooldowns (
    event VARCHAR(64) NOT NULL,
    key_hash CHAR(64) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    PRIMARY KEY (event, key_hash)
);

CREATE INDEX IF NOT EXISTS idx_auth_notification_cooldowns_expires_at
    ON auth_notification_cooldowns(expires_at);
