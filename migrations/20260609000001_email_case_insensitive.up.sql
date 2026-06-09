-- Make account email addresses case-insensitive.
-- Normalize existing rows to lowercase, then enforce uniqueness on lower(email)
-- so "John@x.com" and "john@x.com" can no longer be distinct accounts.
-- If this UPDATE fails on the existing UNIQUE(email) constraint, two accounts
-- already differ only by email case and must be reconciled before migrating.
UPDATE users SET email = lower(email) WHERE email <> lower(email);

CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_key ON users (lower(email));
