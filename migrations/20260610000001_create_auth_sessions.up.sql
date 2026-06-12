-- Refresh-token sessions. One row per refresh-token generation; rotations
-- chain rows via replaced_by_id and share family_id (the first row's id),
-- which is also the "sid" claim in access tokens. Presenting a revoked or
-- replaced token is treated as reuse and revokes the whole family.
CREATE TABLE IF NOT EXISTS auth_sessions (
    id UUID PRIMARY KEY,
    family_id UUID NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash CHAR(64) NOT NULL UNIQUE,
    token_version BIGINT NOT NULL DEFAULT 0,
    user_agent TEXT NULL,
    client_ip TEXT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    last_used_at TIMESTAMP NOT NULL DEFAULT now(),
    expires_at TIMESTAMP NOT NULL,
    revoked_at TIMESTAMP NULL,
    -- No FK self-reference: rows are bulk-deleted by the cleanup job.
    replaced_by_id UUID NULL
);

CREATE INDEX IF NOT EXISTS idx_auth_sessions_user_active
    ON auth_sessions (user_id) WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_auth_sessions_family_active
    ON auth_sessions (family_id) WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_auth_sessions_expires_at
    ON auth_sessions (expires_at);
