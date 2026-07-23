package entity

import "time"

const (
	OneSignalErasureStatusPending   = "pending"
	OneSignalErasureStatusVerifying = "verifying"
	OneSignalErasureStatusVerified  = "verified"
)

// OneSignalErasureCreate is written atomically with local account deletion.
type OneSignalErasureCreate struct {
	ID                   string
	AppID                string
	ExternalIDCiphertext string
	ExternalIDHash       string
	NextAttemptAt        time.Time
}

// OneSignalErasure is one durable provider-deletion workflow.
type OneSignalErasure struct {
	ID                   string
	AppID                string
	ExternalIDCiphertext string
	ExternalIDHash       string
	Status               string
	AttemptCount         int
	NextAttemptAt        time.Time
	LeaseToken           string
	LeaseExpiresAt       time.Time
	AcceptedAt           *time.Time
	VerifiedAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// OneSignalErasureAttempt applies one provider outcome while holding a claim lease.
type OneSignalErasureAttempt struct {
	ID                  string
	ErasureID           string
	LeaseToken          string
	Operation           string
	Status              string
	HTTPStatus          int
	ReasonCode          string
	ReasonDetail        string
	NextAttemptAt       time.Time
	AcceptedAt          *time.Time
	VerifiedAt          *time.Time
	ClearExternalID     bool
	AttemptedAt         time.Time
	ProviderCallOutcome string
}

// OneSignalErasureProviderResult is a bounded, sanitized provider response.
type OneSignalErasureProviderResult struct {
	HTTPStatus   int
	ReasonCode   string
	ReasonDetail string
	Accepted     bool
	NotFound     bool
	Retryable    bool
	Systemic     bool
	RetryAfter   time.Duration
}
