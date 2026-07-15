package entity

import "time"

const (
	NotificationTypeStreakReminder  = "streak_reminder"
	NotificationTypeKhatamMilestone = "khatam_milestone"
	NotificationTypeKhatamCompleted = "khatam_completed"
	NotificationTypeNewLogin        = "new_login"

	NotificationStatusPending  = "pending"
	NotificationStatusRetrying = "retrying"
	NotificationStatusAccepted = "accepted"
	NotificationStatusFailed   = "failed"

	PushDeliveryAccepted = "accepted"
	PushDeliveryFailed   = "failed"
)

// PushNotification is a single push delivery targeted at one or more users by their OneSignal
// external_id alias (the backend user UUID). Headings and Contents are keyed by language code
// (e.g. "en", "id", "ar"); "en" should always be present as the OneSignal default language. Data
// carries optional key/value extras such as a deep_link the app consumes when the push is tapped.
type PushNotification struct {
	ExternalIDs []string          `json:"external_ids"`
	Headings    map[string]string `json:"headings,omitempty"`
	Contents    map[string]string `json:"contents"`
	Data        map[string]string `json:"data,omitempty"`
}

// PushDeliveryResult is the normalized outcome of one provider request. A failed result may be
// retryable; Error is reserved for transport/encoding failures while this value carries the safe,
// bounded reason that is persisted and exported as a low-cardinality metric label.
type PushDeliveryResult struct {
	Outcome                string
	ProviderNotificationID string
	HTTPStatus             int
	ReasonCode             string
	ReasonDetail           string
	Retryable              bool
	Systemic               bool
	RetryAfter             time.Duration
}

// ReminderCandidate is one user eligible for a streak/daily reading reminder, resolved in the
// user's local timezone. LocalDate (YYYY-MM-DD) is the user's current local date, used to scope the
// per-day send cooldown so a restart can't double-send.
type ReminderCandidate struct {
	UserID             string
	Lang               string
	Timezone           string
	LocalDate          string
	DeliveryDeadlineAt time.Time
}

// ReminderCandidatesResult carries sendable users plus profiles skipped fail-closed because their
// timezone cannot be used safely. Counts are evaluation events, not distinct users.
type ReminderCandidatesResult struct {
	Candidates             []ReminderCandidate
	MissingTimezoneSkipped int64
	InvalidTimezoneSkipped int64
}

// NotificationDelivery is one durable logical OneSignal notification. Payload is persisted before
// the HTTP request so a process restart can replay the same content with the same idempotency key.
type NotificationDelivery struct {
	ID                     string
	UserID                 string
	NotificationType       string
	LocalDate              string
	Payload                PushNotification
	IdempotencyKey         string
	Status                 string
	ProviderNotificationID string
	LastReasonCode         string
	LastReasonDetail       string
	AttemptCount           int
	LeaseToken             string
	LeaseExpiresAt         time.Time
	NextAttemptAt          time.Time
	DeliveryDeadlineAt     time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// NotificationDeliveryCreate contains application-generated identifiers and the initial lease.
type NotificationDeliveryCreate struct {
	ID                 string
	UserID             string
	NotificationType   string
	LocalDate          string
	Payload            PushNotification
	IdempotencyKey     string
	LeaseToken         string
	LeaseExpiresAt     time.Time
	DeliveryDeadlineAt time.Time
}

// ReminderDeliveryClaim adds the independent 20-hour cooldown keys to a daily delivery claim.
// LegacyCooldownKeyHash preserves rolling-deploy compatibility with the pre-Q-6 date-scoped key.
type ReminderDeliveryClaim struct {
	Delivery              NotificationDeliveryCreate
	CooldownKeyHash       string
	LegacyCooldownKeyHash string
	CooldownExpiresAt     time.Time
}

// NotificationDeliveryAttempt is the append-only provider evidence written with the delivery
// state transition and persistent Prometheus totals in one transaction.
type NotificationDeliveryAttempt struct {
	ID                     string
	DeliveryID             string
	LeaseToken             string
	AttemptNumber          int
	Outcome                string
	Retryable              bool
	Systemic               bool
	Terminal               bool
	HTTPStatus             int
	RetryAfter             time.Duration
	ProviderNotificationID string
	ReasonCode             string
	ReasonDetail           string
	OccurredAt             time.Time
	NextAttemptAt          time.Time
}
