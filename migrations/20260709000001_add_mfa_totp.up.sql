-- A-3: MFA (TOTP) + one-time recovery codes + step-up freshness.
-- golang-migrate runs each statement in its own autocommit; ordering matters.

-- Per-user TOTP state. confirmed_at NULL = enrollment pending (re-enroll may
-- overwrite); the shared secret is AES-GCM encrypted at rest (pkg/cryptobox).
-- last_used_totp_step blocks replay of a just-used code (monotonic guard).
CREATE TABLE IF NOT EXISTS user_mfa (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    totp_secret_enc TEXT NOT NULL,
    last_used_totp_step BIGINT NOT NULL DEFAULT 0,
    confirmed_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

-- One-time recovery codes, SHA-256 hex at rest (same posture as the auth
-- token hashes). used_at flips exactly once via an atomic guarded UPDATE.
CREATE TABLE IF NOT EXISTS user_mfa_recovery_codes (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash CHAR(64) NOT NULL UNIQUE,
    used_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_mfa_recovery_codes_unused
    ON user_mfa_recovery_codes(user_id)
    WHERE used_at IS NULL;

-- Short-lived second-factor challenges: 'login' bridges password success to
-- code verification; 'reset' carries the emailed OTP for the lost-device
-- flow (email + recovery code combo). Consumed exactly once on success.
CREATE TABLE IF NOT EXISTS mfa_challenges (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL DEFAULT 'login' CHECK (purpose IN ('login', 'reset')),
    token_hash CHAR(64) NOT NULL UNIQUE,
    otp_hash TEXT NULL,
    otp_expires_at TIMESTAMP NULL,
    expires_at TIMESTAMP NOT NULL,
    consumed_at TIMESTAMP NULL,
    client_ip TEXT NULL,
    user_agent TEXT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mfa_challenges_expiry
    ON mfa_challenges(expires_at);

-- Step-up freshness anchor: stamped on MFA login/step-up success, copied to
-- the successor row on refresh rotation.
ALTER TABLE auth_sessions
    ADD COLUMN IF NOT EXISTS mfa_verified_at TIMESTAMP NULL;

-- Grace anchor for the enrollment mandate (AC-1): when an account first
-- became subject to "this role must have MFA". Backfilled for existing
-- admins so their grace clock starts at deploy time.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS mfa_enforced_from TIMESTAMP NULL;

UPDATE users
SET mfa_enforced_from = now()
WHERE role = 'admin'
  AND mfa_enforced_from IS NULL;
