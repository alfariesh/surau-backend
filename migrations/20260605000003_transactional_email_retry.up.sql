ALTER TABLE email_messages
    ADD COLUMN IF NOT EXISTS html TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS text TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS critical BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS headers JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_email_messages_transactional_retry_due
    ON email_messages(scheduled_at ASC, created_at ASC)
    WHERE category = 'transactional' AND status = 'queued';
