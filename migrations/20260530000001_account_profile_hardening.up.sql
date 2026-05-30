ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_username_key,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP NULL;

CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON users (deleted_at);

UPDATE user_profiles p
SET
    display_name = NULLIF(BTRIM(u.username), ''),
    updated_at = now()
FROM users u
WHERE p.user_id = u.id
    AND p.display_name IS NULL
    AND NULLIF(BTRIM(u.username), '') IS NOT NULL;

CREATE TABLE IF NOT EXISTS email_change_tokens (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    new_email VARCHAR(255) NOT NULL,
    token_hash CHAR(64) NOT NULL UNIQUE,
    expires_at TIMESTAMP NOT NULL,
    used_at TIMESTAMP NULL,
    sent_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_email_change_tokens_user_id ON email_change_tokens (user_id);
CREATE INDEX IF NOT EXISTS idx_email_change_tokens_unused ON email_change_tokens (user_id, sent_at DESC)
    WHERE used_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_email_change_tokens_expires_at ON email_change_tokens (expires_at);
