-- Q-6: durable OneSignal delivery evidence, restart-safe idempotency, and
-- persistent metric totals. UUIDs are supplied by the application, matching
-- the convention used by the rest of the repository.

CREATE TABLE IF NOT EXISTS notification_deliveries (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_type VARCHAR(64) NOT NULL,
    local_date DATE NULL,
    payload JSONB NOT NULL,
    provider VARCHAR(32) NOT NULL DEFAULT 'onesignal',
    idempotency_key UUID NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    provider_notification_id VARCHAR(128) NULL,
    last_reason_code VARCHAR(64) NULL,
    last_reason_detail TEXT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    lease_token UUID NULL,
    lease_expires_at TIMESTAMPTZ NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    delivery_deadline_at TIMESTAMPTZ NULL,
    accepted_at TIMESTAMPTZ NULL,
    failed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT notification_deliveries_type_check CHECK (
        notification_type IN (
            'streak_reminder',
            'khatam_milestone',
            'khatam_completed',
            'new_login'
        )
    ),
    CONSTRAINT notification_deliveries_local_date_check CHECK (
        (notification_type = 'streak_reminder' AND local_date IS NOT NULL)
        OR (notification_type <> 'streak_reminder' AND local_date IS NULL)
    ),
    CONSTRAINT notification_deliveries_payload_check CHECK (
        jsonb_typeof(payload) = 'object'
    ),
    CONSTRAINT notification_deliveries_provider_check CHECK (provider = 'onesignal'),
    CONSTRAINT notification_deliveries_status_check CHECK (
        status IN ('pending', 'retrying', 'accepted', 'failed')
    ),
    CONSTRAINT notification_deliveries_attempt_count_check CHECK (attempt_count >= 0),
    CONSTRAINT notification_deliveries_lease_check CHECK (
        (lease_token IS NULL AND lease_expires_at IS NULL)
        OR (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
    ),
    CONSTRAINT notification_deliveries_reason_code_check CHECK (
        last_reason_code IS NULL
        OR last_reason_code ~ '^[a-z0-9][a-z0-9_]{0,63}$'
    ),
    CONSTRAINT notification_deliveries_reason_detail_check CHECK (
        last_reason_detail IS NULL OR char_length(last_reason_detail) <= 2000
    ),
    CONSTRAINT notification_deliveries_provider_id_check CHECK (
        provider_notification_id IS NULL
        OR (char_length(provider_notification_id) BETWEEN 1 AND 128)
    ),
    CONSTRAINT notification_deliveries_terminal_state_check CHECK (
        (
            status IN ('pending', 'retrying')
            AND accepted_at IS NULL
            AND failed_at IS NULL
        )
        OR (
            status = 'accepted'
            AND accepted_at IS NOT NULL
            AND failed_at IS NULL
            AND provider_notification_id IS NOT NULL
        )
        OR (
            status = 'failed'
            AND accepted_at IS NULL
            AND failed_at IS NOT NULL
            AND last_reason_code IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_notification_deliveries_idempotency
    ON notification_deliveries(idempotency_key);

CREATE UNIQUE INDEX IF NOT EXISTS idx_notification_deliveries_reminder_day
    ON notification_deliveries(user_id, notification_type, local_date)
    WHERE local_date IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_notification_deliveries_claim
    ON notification_deliveries(next_attempt_at, created_at, id)
    WHERE status IN ('pending', 'retrying');

CREATE INDEX IF NOT EXISTS idx_notification_deliveries_expired_lease
    ON notification_deliveries(lease_expires_at, next_attempt_at, id)
    WHERE status IN ('pending', 'retrying') AND lease_expires_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_notification_deliveries_deadline
    ON notification_deliveries(delivery_deadline_at, id)
    WHERE status IN ('pending', 'retrying') AND delivery_deadline_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_notification_deliveries_user_created
    ON notification_deliveries(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS notification_delivery_attempts (
    id UUID PRIMARY KEY,
    delivery_id UUID NOT NULL REFERENCES notification_deliveries(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL,
    outcome VARCHAR(16) NOT NULL,
    retryable BOOLEAN NOT NULL DEFAULT false,
    systemic BOOLEAN NOT NULL DEFAULT false,
    http_status SMALLINT NULL,
    retry_after_seconds INTEGER NULL,
    provider_notification_id VARCHAR(128) NULL,
    reason_code VARCHAR(64) NOT NULL,
    reason_detail TEXT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT notification_delivery_attempts_number_check CHECK (attempt_number > 0),
    CONSTRAINT notification_delivery_attempts_outcome_check CHECK (
        outcome IN ('accepted', 'failed')
    ),
    CONSTRAINT notification_delivery_attempts_http_status_check CHECK (
        http_status IS NULL OR http_status BETWEEN 100 AND 599
    ),
    CONSTRAINT notification_delivery_attempts_retry_after_check CHECK (
        retry_after_seconds IS NULL
        OR (retryable AND retry_after_seconds BETWEEN 0 AND 86400)
    ),
    CONSTRAINT notification_delivery_attempts_reason_code_check CHECK (
        reason_code ~ '^[a-z0-9][a-z0-9_]{0,63}$'
    ),
    CONSTRAINT notification_delivery_attempts_reason_detail_check CHECK (
        reason_detail IS NULL OR char_length(reason_detail) <= 2000
    ),
    CONSTRAINT notification_delivery_attempts_provider_id_check CHECK (
        provider_notification_id IS NULL
        OR (char_length(provider_notification_id) BETWEEN 1 AND 128)
    ),
    CONSTRAINT notification_delivery_attempts_accepted_check CHECK (
        outcome <> 'accepted'
        OR (
            retryable = false
            AND systemic = false
            AND provider_notification_id IS NOT NULL
        )
    ),
    CONSTRAINT notification_delivery_attempts_delivery_number_key
        UNIQUE (delivery_id, attempt_number)
);

CREATE INDEX IF NOT EXISTS idx_notification_delivery_attempts_occurred
    ON notification_delivery_attempts(occurred_at DESC, delivery_id);

CREATE INDEX IF NOT EXISTS idx_notification_delivery_attempts_failed
    ON notification_delivery_attempts(occurred_at DESC, reason_code)
    WHERE outcome = 'failed';

CREATE TABLE IF NOT EXISTS notification_delivery_metric_totals (
    metric_kind VARCHAR(32) NOT NULL,
    notification_type VARCHAR(64) NOT NULL,
    result VARCHAR(16) NOT NULL,
    reason_code VARCHAR(64) NOT NULL DEFAULT '',
    total BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (metric_kind, notification_type, result, reason_code),
    CONSTRAINT notification_delivery_metric_totals_kind_check CHECK (
        metric_kind IN ('delivery_attempt', 'delivery', 'reminder_skip')
    ),
    CONSTRAINT notification_delivery_metric_totals_type_check CHECK (
        notification_type IN (
            'streak_reminder',
            'khatam_milestone',
            'khatam_completed',
            'new_login'
        )
    ),
    CONSTRAINT notification_delivery_metric_totals_result_check CHECK (
        (metric_kind IN ('delivery_attempt', 'delivery') AND result IN ('accepted', 'failed'))
        OR (
            metric_kind = 'reminder_skip'
            AND notification_type = 'streak_reminder'
            AND result = 'skipped'
        )
    ),
    CONSTRAINT notification_delivery_metric_totals_reason_check CHECK (
        reason_code = '' OR reason_code ~ '^[a-z0-9][a-z0-9_]{0,63}$'
    ),
    CONSTRAINT notification_delivery_metric_totals_value_check CHECK (total >= 0)
);

CREATE INDEX IF NOT EXISTS idx_notification_delivery_metric_totals_updated
    ON notification_delivery_metric_totals(updated_at DESC);

-- Seed every bounded terminal series at zero so dashboards have a baseline before the first
-- delivery. Failure reasons are a closed application taxonomy; provider text is never a label.
INSERT INTO notification_delivery_metric_totals (
    metric_kind, notification_type, result, reason_code, total
)
SELECT 'delivery', notification_type, status, '', 0
FROM unnest(ARRAY[
    'streak_reminder', 'khatam_milestone', 'khatam_completed', 'new_login'
]) AS notification_types(notification_type)
CROSS JOIN unnest(ARRAY['accepted', 'failed']) AS statuses(status)
ON CONFLICT DO NOTHING;

INSERT INTO notification_delivery_metric_totals (
    metric_kind, notification_type, result, reason_code, total
)
SELECT 'delivery_attempt', notification_type, 'accepted', 'accepted', 0
FROM unnest(ARRAY[
    'streak_reminder', 'khatam_milestone', 'khatam_completed', 'new_login'
]) AS notification_types(notification_type)
ON CONFLICT DO NOTHING;

INSERT INTO notification_delivery_metric_totals (
    metric_kind, notification_type, result, reason_code, total
)
SELECT 'delivery_attempt', notification_type, 'failed', reason_code, 0
FROM unnest(ARRAY[
    'streak_reminder', 'khatam_milestone', 'khatam_completed', 'new_login'
]) AS notification_types(notification_type)
CROSS JOIN unnest(ARRAY[
    'no_subscribers',
    'provider_rejected',
    'invalid_request',
    'unauthorized',
    'rate_limited',
    'provider_unavailable',
    'timeout',
    'network_error',
    'provider_invalid_response',
    'invalid_configuration',
    'invalid_idempotency_key',
    'provider_error'
]) AS failure_reasons(reason_code)
ON CONFLICT DO NOTHING;
