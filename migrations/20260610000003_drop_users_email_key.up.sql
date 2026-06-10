-- users_email_lower_key (20260609000001) enforces case-insensitive
-- uniqueness on lower(email), which implies case-sensitive uniqueness.
-- The original UNIQUE(email) constraint is redundant and only adds write
-- overhead, so drop it.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_key;
