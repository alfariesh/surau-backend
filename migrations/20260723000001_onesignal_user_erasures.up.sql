CREATE TABLE onesignal_user_erasures (
    id UUID PRIMARY KEY,
    app_id UUID NOT NULL,
    external_id_ciphertext TEXT,
    external_id_hash CHAR(64) NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID,
    lease_expires_at TIMESTAMPTZ,
    last_http_status INTEGER,
    last_reason_code TEXT NOT NULL DEFAULT '',
    last_reason_detail TEXT NOT NULL DEFAULT '',
    accepted_at TIMESTAMPTZ,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT onesignal_user_erasures_status_check
        CHECK (status IN ('pending', 'verifying', 'verified')),
    CONSTRAINT onesignal_user_erasures_attempt_count_check
        CHECK (attempt_count >= 0),
    CONSTRAINT onesignal_user_erasures_http_status_check
        CHECK (last_http_status IS NULL OR last_http_status BETWEEN 0 AND 599),
    CONSTRAINT onesignal_user_erasures_hash_check
        CHECK (external_id_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT onesignal_user_erasures_lease_check
        CHECK (
            (lease_token IS NULL AND lease_expires_at IS NULL)
            OR (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        ),
    CONSTRAINT onesignal_user_erasures_verified_check
        CHECK (
            (status = 'verified' AND verified_at IS NOT NULL AND external_id_ciphertext IS NULL)
            OR (status <> 'verified' AND verified_at IS NULL AND external_id_ciphertext IS NOT NULL)
        ),
    UNIQUE (app_id, external_id_hash)
);

CREATE INDEX onesignal_user_erasures_due_idx
    ON onesignal_user_erasures (next_attempt_at, created_at)
    WHERE status <> 'verified';

CREATE INDEX onesignal_user_erasures_verified_retention_idx
    ON onesignal_user_erasures (verified_at)
    WHERE status = 'verified';

CREATE TABLE onesignal_user_erasure_attempts (
    id UUID PRIMARY KEY,
    erasure_id UUID NOT NULL REFERENCES onesignal_user_erasures(id) ON DELETE CASCADE,
    operation TEXT NOT NULL,
    outcome TEXT NOT NULL,
    http_status INTEGER,
    reason_code TEXT NOT NULL DEFAULT '',
    reason_detail TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT onesignal_user_erasure_attempts_operation_check
        CHECK (operation IN ('delete', 'verify')),
    CONSTRAINT onesignal_user_erasure_attempts_outcome_check
        CHECK (outcome IN ('accepted', 'not_found', 'exists', 'failed')),
    CONSTRAINT onesignal_user_erasure_attempts_http_status_check
        CHECK (http_status IS NULL OR http_status BETWEEN 0 AND 599)
);

CREATE INDEX onesignal_user_erasure_attempts_erasure_idx
    ON onesignal_user_erasure_attempts (erasure_id, occurred_at);
