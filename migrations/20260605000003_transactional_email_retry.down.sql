DROP INDEX IF EXISTS idx_email_messages_transactional_retry_due;

ALTER TABLE email_messages
    DROP COLUMN IF EXISTS headers,
    DROP COLUMN IF EXISTS critical,
    DROP COLUMN IF EXISTS text,
    DROP COLUMN IF EXISTS html;
