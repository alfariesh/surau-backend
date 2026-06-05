CREATE TABLE IF NOT EXISTS email_delivery_events (
    id UUID PRIMARY KEY,
    dedupe_key VARCHAR(255) NOT NULL,
    provider VARCHAR(64) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    recipient_email VARCHAR(255) NOT NULL,
    message_id UUID NULL REFERENCES email_messages(id) ON DELETE SET NULL,
    campaign_id UUID NULL REFERENCES email_campaigns(id) ON DELETE SET NULL,
    campaign_recipient_id UUID NULL REFERENCES email_campaign_recipients(id) ON DELETE SET NULL,
    reason VARCHAR(128) NULL,
    diagnostic TEXT NULL,
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMP NOT NULL DEFAULT now(),
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    CONSTRAINT email_delivery_events_type_check CHECK (event_type IN ('bounce_hard', 'complaint'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_email_delivery_events_dedupe
    ON email_delivery_events(dedupe_key);

CREATE INDEX IF NOT EXISTS idx_email_delivery_events_recipient_created
    ON email_delivery_events(lower(recipient_email), created_at DESC);

CREATE INDEX IF NOT EXISTS idx_email_delivery_events_message
    ON email_delivery_events(message_id)
    WHERE message_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_email_delivery_events_campaign
    ON email_delivery_events(campaign_id, created_at DESC)
    WHERE campaign_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_email_delivery_events_campaign_recipient
    ON email_delivery_events(campaign_recipient_id)
    WHERE campaign_recipient_id IS NOT NULL;
