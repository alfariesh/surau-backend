-- Restore the original case-sensitive unique constraint. This can fail if
-- rows differing only by email case were inserted while the constraint was
-- absent; users_email_lower_key prevents that, so in practice it is safe.
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
