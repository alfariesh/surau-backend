CREATE TABLE IF NOT EXISTS email_provider_poll_cursors (
    provider VARCHAR(64) NOT NULL,
    cursor_key VARCHAR(255) NOT NULL,
    last_polled_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, cursor_key)
);
