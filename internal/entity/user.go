package entity

import "time"

// User -.
type User struct {
	ID            string    `json:"id"         example:"550e8400-e29b-41d4-a716-446655440000"`
	Username      string    `json:"username"    example:"johndoe"`
	Email         string    `json:"email"       example:"john@example.com"`
	Role          string    `json:"role"        example:"user"`
	PasswordHash  string    `json:"-"`
	EmailVerified bool      `json:"email_verified" example:"true"`
	TokenVersion  int64     `json:"-"`
	CreatedAt     time.Time `json:"created_at"  example:"2026-01-01T00:00:00Z"`
	UpdatedAt     time.Time `json:"updated_at"  example:"2026-01-01T00:00:00Z"`
} // @name entity.User

// EmailVerificationToken stores a one-time email verification token hash.
type EmailVerificationToken struct {
	ID        string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	SentAt    time.Time
	CreatedAt time.Time
}

// PasswordResetToken stores a one-time password reset token hash.
type PasswordResetToken struct {
	ID        string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	SentAt    time.Time
	CreatedAt time.Time
}

// AuthRateLimit stores one rate-limit counter increment request.
type AuthRateLimit struct {
	Action        string
	KeyHash       string
	WindowStart   time.Time
	WindowSeconds int64
	MaxAttempts   int
	ExpiresAt     time.Time
}

// AuthRateLimitResult reports the counter state after an increment.
type AuthRateLimitResult struct {
	Allowed    bool
	Count      int
	RetryAfter time.Duration
}

// AuthAuditLog stores a sanitized authentication event.
type AuthAuditLog struct {
	ID        string
	Event     string
	Status    string
	UserID    string
	Email     string
	ClientIP  string
	UserAgent string
	ErrorCode string
	Metadata  map[string]string
	CreatedAt time.Time
}

// AuthLoginFingerprint stores a hashed client fingerprint for login alerts.
type AuthLoginFingerprint struct {
	UserID          string
	FingerprintHash string
	ClientIP        string
	UserAgent       string
	SeenAt          time.Time
}

// AuthNotificationCooldown stores one notification throttle key.
type AuthNotificationCooldown struct {
	Event     string
	KeyHash   string
	ExpiresAt time.Time
}
