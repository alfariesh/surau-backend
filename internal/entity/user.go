package entity

import (
	"strings"
	"time"
)

const (
	UserOnboardingVersion = 1

	UserRoleUser   = "user"
	UserRoleEditor = "editor"
	UserRoleAdmin  = "admin"

	UserPreferredLangDefault = "id"

	UserArabicLevelNone         = "none"
	UserArabicLevelBasic        = "basic"
	UserArabicLevelIntermediate = "intermediate"
	UserArabicLevelAdvanced     = "advanced"
	UserArabicLevelNative       = "native"

	UserReaderModeArabicTranslation = "arabic_translation"
	UserReaderModeTranslationOnly   = "translation_only"
	UserReaderModeArabicOnly        = "arabic_only"
)

// NormalizeUserRole trims, lowercases, and validates a user role.
func NormalizeUserRole(role string) (string, error) {
	role = strings.ToLower(strings.TrimSpace(role))
	if !IsValidUserRole(role) {
		return "", ErrInvalidRole
	}

	return role, nil
}

// IsValidUserRole reports whether role is one of the supported account roles.
func IsValidUserRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case UserRoleUser, UserRoleEditor, UserRoleAdmin:
		return true
	default:
		return false
	}
}

// CanReviewEditorial reports whether role can access editorial review and draft tools.
func CanReviewEditorial(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case UserRoleEditor, UserRoleAdmin:
		return true
	default:
		return false
	}
}

// CanPublishEditorial reports whether role can publish or administer editorial state.
func CanPublishEditorial(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), UserRoleAdmin)
}

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

// UserProfile stores product-level user metadata outside auth identity.
type UserProfile struct {
	UserID                 string     `json:"user_id"                  example:"550e8400-e29b-41d4-a716-446655440000"`
	DisplayName            *string    `json:"display_name,omitempty"   example:"John"`
	Timezone               *string    `json:"timezone,omitempty"       example:"Asia/Jakarta"`
	CountryCode            *string    `json:"country_code,omitempty"   example:"ID"`
	OnboardingVersion      int        `json:"onboarding_version"       example:"1"`
	OnboardingCompletedAt  *time.Time `json:"onboarding_completed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	PersonalizationEnabled bool       `json:"personalization_enabled"  example:"true"`
	CreatedAt              time.Time  `json:"created_at"              example:"2026-01-01T00:00:00Z"`
	UpdatedAt              time.Time  `json:"updated_at"              example:"2026-01-01T00:00:00Z"`
} // @name entity.UserProfile

// UserPreferences stores reader and Quran personalization choices.
type UserPreferences struct {
	UserID                   string    `json:"user_id"                      example:"550e8400-e29b-41d4-a716-446655440000"`
	PreferredUILang          string    `json:"preferred_ui_lang"            example:"id"`
	PreferredContentLang     string    `json:"preferred_content_lang"       example:"id"`
	FallbackLangs            []string  `json:"fallback_langs"               example:"id,en"`
	ArabicLevel              string    `json:"arabic_level"                 example:"basic"`
	ReaderMode               string    `json:"reader_mode"                  example:"arabic_translation"`
	Interests                []string  `json:"interests"                    example:"tafsir,hadith"`
	DailyGoalMinutes         *int      `json:"daily_goal_minutes,omitempty" example:"15"`
	QuranTranslationSourceID *string   `json:"quran_translation_source_id,omitempty"`
	QuranRecitationID        *string   `json:"quran_recitation_id,omitempty"`
	CreatedAt                time.Time `json:"created_at"                   example:"2026-01-01T00:00:00Z"`
	UpdatedAt                time.Time `json:"updated_at"                   example:"2026-01-01T00:00:00Z"`
} // @name entity.UserPreferences

// UserAccount is the authenticated profile response with product preferences.
type UserAccount struct {
	User
	Profile            UserProfile     `json:"profile"`
	Preferences        UserPreferences `json:"preferences"`
	OnboardingRequired bool            `json:"onboarding_required" example:"true"`
} // @name entity.UserAccount

// UserRoleChange reports one role assignment mutation.
type UserRoleChange struct {
	User         User
	PreviousRole string
	NewRole      string
} // @name entity.UserRoleChange

// UserActivity shows admin-visible user account audit history.
type UserActivity struct {
	ID         string    `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	UserID     string    `json:"user_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Email      string    `json:"email" example:"editor@example.com"`
	Event      string    `json:"event" example:"role_change"`
	Status     string    `json:"status" example:"success"`
	ActorID    *string   `json:"actor_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
	ActorEmail *string   `json:"actor_email,omitempty" example:"admin@example.com"`
	OldRole    *string   `json:"old_role,omitempty" example:"user"`
	NewRole    *string   `json:"new_role,omitempty" example:"editor"`
	ErrorCode  *string   `json:"error_code,omitempty" example:"invalid_role"`
	ClientIP   *string   `json:"client_ip,omitempty"`
	UserAgent  *string   `json:"user_agent,omitempty"`
	CreatedAt  time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.UserActivity

// UserOnboarding stores the normalized onboarding form submitted by the client.
type UserOnboarding struct {
	DisplayName              *string
	Timezone                 *string
	CountryCode              *string
	PersonalizationEnabled   *bool
	PreferredUILang          string
	PreferredContentLang     string
	FallbackLangs            []string
	ArabicLevel              string
	ReaderMode               string
	Interests                []string
	DailyGoalMinutes         *int
	QuranTranslationSourceID *string
	QuranRecitationID        *string
}

// UserPreferencesPatch stores optional preference changes after onboarding.
type UserPreferencesPatch struct {
	PreferredUILang          *string
	PreferredContentLang     *string
	FallbackLangs            *[]string
	ArabicLevel              *string
	ReaderMode               *string
	Interests                *[]string
	DailyGoalMinutes         *int
	QuranTranslationSourceID *string
	QuranRecitationID        *string
}

// UserProfilePatch stores optional profile changes after onboarding.
type UserProfilePatch struct {
	DisplayName            *string
	Timezone               *string
	CountryCode            *string
	PersonalizationEnabled *bool
}

// DefaultUserProfile returns the initial profile row for a new account.
func DefaultUserProfile(userID string, now time.Time) UserProfile {
	return UserProfile{
		UserID:                 userID,
		OnboardingVersion:      UserOnboardingVersion,
		PersonalizationEnabled: true,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
}

// DefaultUserPreferences returns the initial reader preferences for a new account.
func DefaultUserPreferences(userID string, now time.Time) UserPreferences {
	return UserPreferences{
		UserID:               userID,
		PreferredUILang:      UserPreferredLangDefault,
		PreferredContentLang: UserPreferredLangDefault,
		FallbackLangs:        []string{UserPreferredLangDefault},
		ArabicLevel:          UserArabicLevelNone,
		ReaderMode:           UserReaderModeArabicTranslation,
		Interests:            []string{},
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

// EmailVerificationToken stores a one-time email verification token hash.
type EmailVerificationToken struct {
	ID           string
	UserID       string
	TokenHash    string
	OTPHash      string
	OTPExpiresAt *time.Time
	ExpiresAt    time.Time
	UsedAt       *time.Time
	SentAt       time.Time
	CreatedAt    time.Time
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

// EmailChangeToken stores a one-time email change verification token hash.
type EmailChangeToken struct {
	ID           string
	UserID       string
	NewEmail     string
	TokenHash    string
	OTPHash      string
	OTPExpiresAt *time.Time
	ExpiresAt    time.Time
	UsedAt       *time.Time
	SentAt       time.Time
	CreatedAt    time.Time
}

// EmailChangeResult reports the final state after an atomic email change.
type EmailChangeResult struct {
	User     User
	OldEmail string
	NewEmail string
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

// AuthSession stores one refresh-token generation. Rotations chain rows via
// ReplacedByID and share FamilyID (the first row's ID), which is also the
// "sid" claim embedded in access tokens.
type AuthSession struct {
	ID               string
	FamilyID         string
	UserID           string
	RefreshTokenHash string
	TokenVersion     int64
	UserAgent        string
	ClientIP         string
	CreatedAt        time.Time
	LastUsedAt       time.Time
	ExpiresAt        time.Time
	RevokedAt        *time.Time
	ReplacedByID     *string
}

// LoginResult carries the access/refresh token pair issued at login, refresh,
// password change, and email change.
type LoginResult struct {
	User             User
	SessionID        string
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// AuthLoginLockout stores the progressive lockout state for one login key.
type AuthLoginLockout struct {
	KeyHash             string
	ConsecutiveFailures int
	LockedUntil         *time.Time
	LastFailureAt       time.Time
}

// AuthCleanupResult reports per-table delete counts from one cleanup run.
type AuthCleanupResult struct {
	RateLimits            int64
	VerificationTokens    int64
	PasswordResetTokens   int64
	EmailChangeTokens     int64
	Sessions              int64
	Lockouts              int64
	NotificationCooldowns int64
	AuditLogs             int64
}
