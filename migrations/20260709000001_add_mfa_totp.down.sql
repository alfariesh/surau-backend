ALTER TABLE users
    DROP COLUMN IF EXISTS mfa_enforced_from;

ALTER TABLE auth_sessions
    DROP COLUMN IF EXISTS mfa_verified_at;

DROP TABLE IF EXISTS mfa_challenges;

DROP TABLE IF EXISTS user_mfa_recovery_codes;

DROP TABLE IF EXISTS user_mfa;
