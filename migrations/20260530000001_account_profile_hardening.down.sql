DROP TABLE IF EXISTS email_change_tokens;

DROP INDEX IF EXISTS idx_users_deleted_at;

ALTER TABLE users
    DROP COLUMN IF EXISTS deleted_at;

ALTER TABLE users
    ADD CONSTRAINT users_username_key UNIQUE (username);
