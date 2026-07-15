package config

import (
	"os"
	"strings"
	"testing"

	jwtpkg "github.com/alfariesh/surau-backend/pkg/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()

	t.Setenv("APP_NAME", "surau-backend")
	t.Setenv("APP_VERSION", "test")
	t.Setenv("HTTP_PORT", "8080")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("PG_POOL_MAX", "2")
	t.Setenv("PG_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("JWT_TOKEN_EXPIRY", "24h")
	t.Setenv("CF_EMAIL_ACCOUNT_ID", "account-id")
	t.Setenv("CF_EMAIL_API_TOKEN", "api-token")
	t.Setenv("EMAIL_FROM_ADDRESS", "noreply@example.com")
	t.Setenv("EMAIL_VERIFY_FRONTEND_URL", "https://frontend.example.com/verify-email")
	t.Setenv("PASSWORD_RESET_FRONTEND_URL", "https://frontend.example.com/reset-password")
	t.Setenv("EMAIL_CHANGE_FRONTEND_URL", "https://frontend.example.com/change-email")
}

//nolint:paralleltest // t.Setenv mutates process-global environment variables
func TestNewConfig_OneSignalQuietHours(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		setRequiredEnv(t)

		cfg, err := NewConfig()

		require.NoError(t, err)
		assert.Equal(t, "21:00", cfg.OneSignal.QuietHoursStartLocal)
		assert.Equal(t, "07:00", cfg.OneSignal.QuietHoursEndLocal)
	})

	t.Run("invalid format", func(t *testing.T) {
		setRequiredEnv(t)
		t.Setenv("ONESIGNAL_QUIET_HOURS_START_LOCAL", "9:00")

		_, err := NewConfig()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "ONESIGNAL_QUIET_HOURS_START_LOCAL")
	})

	t.Run("equal boundaries", func(t *testing.T) {
		setRequiredEnv(t)
		t.Setenv("ONESIGNAL_QUIET_HOURS_START_LOCAL", "07:00")
		t.Setenv("ONESIGNAL_QUIET_HOURS_END_LOCAL", "07:00")

		_, err := NewConfig()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "start and end must differ")
	})
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()

	oldValue, hadValue := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if hadValue {
			require.NoError(t, os.Setenv(key, oldValue))

			return
		}

		require.NoError(t, os.Unsetenv(key))
	})
}

//nolint:paralleltest // environment mutation is process-global and must remain serial
func TestNewConfig_BookRAGCitationDefaults(t *testing.T) {
	setRequiredEnv(t)
	unsetEnv(t, "RAG_BOOK_CITATION_MODE")
	unsetEnv(t, "RAG_BOOK_LEGACY_FALLBACK_ENABLED")
	unsetEnv(t, "RAG_LLM_DRIVER")
	unsetEnv(t, "BACKGROUND_LOOPS_ENABLED")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, "unit", cfg.RAG.BookCitationMode)
	assert.True(t, cfg.RAG.BookLegacyFallback)
	assert.Equal(t, RAGLLMDriverOpenAI, cfg.RAG.LLMDriver)
	assert.True(t, cfg.App.BackgroundLoopsEnabled)
}

func TestNewConfig_DeterministicRolloutLLMRequiresIsolatedDevProcess(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("BACKGROUND_LOOPS_ENABLED", "false")
	t.Setenv("RAG_LLM_DRIVER", " DETERMINISTIC-ROLLOUT ")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, RAGLLMDriverRollout, cfg.RAG.LLMDriver)
	assert.False(t, cfg.App.BackgroundLoopsEnabled)
}

func TestNewConfig_DeterministicRolloutLLMRejectsProduction(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("APP_ENV", "prod")
	t.Setenv("BACKGROUND_LOOPS_ENABLED", "false")
	t.Setenv("RAG_LLM_DRIVER", RAGLLMDriverRollout)

	_, err := NewConfig()

	require.EqualError(
		t,
		err,
		"config error: RAG_LLM_DRIVER deterministic-rollout requires APP_ENV=dev and BACKGROUND_LOOPS_ENABLED=false",
	)
}

func TestNewConfig_DeterministicRolloutLLMRejectsBackgroundWorkers(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("BACKGROUND_LOOPS_ENABLED", "true")
	t.Setenv("RAG_LLM_DRIVER", RAGLLMDriverRollout)

	_, err := NewConfig()

	require.EqualError(
		t,
		err,
		"config error: RAG_LLM_DRIVER deterministic-rollout requires APP_ENV=dev and BACKGROUND_LOOPS_ENABLED=false",
	)
}

func TestNewConfig_RejectsUnknownRAGLLMDriver(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("RAG_LLM_DRIVER", "unknown")

	_, err := NewConfig()

	require.EqualError(t, err, "config error: RAG_LLM_DRIVER must be openai-compatible or deterministic-rollout")
}

func TestNewConfig_BookRAGCitationModeValidation(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("RAG_BOOK_CITATION_MODE", " mixed ")

	_, err := NewConfig()

	require.EqualError(t, err, "config error: RAG_BOOK_CITATION_MODE must be legacy, dual, or unit")
}

func TestNewConfig_BookRAGCitationModeNormalizes(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("RAG_BOOK_CITATION_MODE", " DUAL ")
	t.Setenv("RAG_BOOK_LEGACY_FALLBACK_ENABLED", "false")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, "dual", cfg.RAG.BookCitationMode)
	assert.False(t, cfg.RAG.BookLegacyFallback)
}

func TestNewConfig_EmailDefaults(t *testing.T) {
	setRequiredEnv(t)
	unsetEnv(t, "EMAIL_FROM_NAME")
	unsetEnv(t, "EMAIL_VERIFICATION_TTL")
	unsetEnv(t, "EMAIL_VERIFICATION_OTP_TTL")
	unsetEnv(t, "EMAIL_RESEND_COOLDOWN")
	unsetEnv(t, "PASSWORD_RESET_TTL")
	unsetEnv(t, "PASSWORD_RESET_RESEND_COOLDOWN")
	unsetEnv(t, "EMAIL_CHANGE_TTL")
	unsetEnv(t, "EMAIL_CHANGE_OTP_TTL")
	unsetEnv(t, "EMAIL_CHANGE_RESEND_COOLDOWN")
	unsetEnv(t, "EMAIL_UNSUBSCRIBE_TOKEN_KEY_ID")
	unsetEnv(t, "EMAIL_UNSUBSCRIBE_PUBLIC_URL")
	unsetEnv(t, "EMAIL_UNSUBSCRIBE_TOKEN_SECRET")
	unsetEnv(t, "EMAIL_UNSUBSCRIBE_TOKEN_SECRETS")
	unsetEnv(t, "EMAIL_CLOUDFLARE_WEBHOOK_SECRET")
	unsetEnv(t, "EMAIL_CLOUDFLARE_EVENT_POLLING_ENABLED")
	unsetEnv(t, "EMAIL_CLOUDFLARE_ZONE_ID")
	unsetEnv(t, "EMAIL_CLOUDFLARE_ANALYTICS_API_TOKEN")
	unsetEnv(t, "EMAIL_CLOUDFLARE_EVENT_POLLING_INTERVAL")
	unsetEnv(t, "EMAIL_CLOUDFLARE_EVENT_POLLING_LOOKBACK")
	unsetEnv(t, "EMAIL_CLOUDFLARE_EVENT_POLLING_LIMIT")
	unsetEnv(t, "EMAIL_HTTP_TIMEOUT")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, EmailDeliveryModeCloudflare, cfg.Email.DeliveryMode)
	assert.Equal(t, "Surau", cfg.Email.FromName)
	assert.Equal(t, "24h0m0s", cfg.Email.VerificationTTL.String())
	assert.Equal(t, "10m0s", cfg.Email.VerificationOTPTTL.String())
	assert.Equal(t, "1m0s", cfg.Email.ResendCooldown.String())
	assert.Equal(t, "1h0m0s", cfg.Email.PasswordResetTTL.String())
	assert.Equal(t, "1m0s", cfg.Email.PasswordResetCooldown.String())
	assert.Equal(t, "24h0m0s", cfg.Email.EmailChangeTTL.String())
	assert.Equal(t, "10m0s", cfg.Email.EmailChangeOTPTTL.String())
	assert.Equal(t, "1m0s", cfg.Email.EmailChangeCooldown.String())
	assert.Equal(t, "default", cfg.Email.UnsubscribeTokenKeyID)
	assert.Empty(t, cfg.Email.UnsubscribePublicURL)
	assert.Empty(t, cfg.Email.UnsubscribeTokenSecret)
	assert.Empty(t, cfg.Email.UnsubscribeTokenSecrets)
	assert.Empty(t, cfg.Email.CloudflareWebhookSecret)
	assert.False(t, cfg.Email.CloudflareEventPollingEnabled)
	assert.Empty(t, cfg.Email.CloudflareZoneID)
	assert.Empty(t, cfg.Email.CloudflareAnalyticsAPIToken)
	assert.Equal(t, "5m0s", cfg.Email.CloudflareEventPollingInterval.String())
	assert.Equal(t, "30m0s", cfg.Email.CloudflareEventPollingLookback.String())
	assert.Equal(t, 100, cfg.Email.CloudflareEventPollingLimit)
	assert.Equal(t, "10s", cfg.Email.HTTPTimeout.String())
}

func TestNewConfig_UnsubscribePublicURLTrims(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EMAIL_UNSUBSCRIBE_PUBLIC_URL", "  https://api.surau.org/v1/email/unsubscribe  ")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, "https://api.surau.org/v1/email/unsubscribe", cfg.Email.UnsubscribePublicURL)
}

func TestNewConfig_UnsubscribeTokenSecretTrims(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EMAIL_UNSUBSCRIBE_TOKEN_SECRET", "  unsubscribe-secret  ")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, "unsubscribe-secret", cfg.Email.UnsubscribeTokenSecret)
}

func TestNewConfig_UnsubscribeTokenSecretMap(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EMAIL_UNSUBSCRIBE_TOKEN_KEY_ID", "2026-06")
	t.Setenv("EMAIL_UNSUBSCRIBE_TOKEN_SECRETS", `{"2026-06":"new-secret","2026-05":"old-secret"}`)

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, "2026-06", cfg.Email.UnsubscribeTokenKeyID)
	assert.Equal(t, `{"2026-06":"new-secret","2026-05":"old-secret"}`, cfg.Email.UnsubscribeTokenSecrets)
}

func TestNewConfig_CloudflareEventPollingTrimsAndValidates(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EMAIL_CLOUDFLARE_EVENT_POLLING_ENABLED", "true")
	t.Setenv("EMAIL_CLOUDFLARE_ZONE_ID", "  zone-id  ")
	t.Setenv("EMAIL_CLOUDFLARE_ANALYTICS_API_TOKEN", "  analytics-token  ")
	t.Setenv("EMAIL_CLOUDFLARE_EVENT_POLLING_INTERVAL", "10m")
	t.Setenv("EMAIL_CLOUDFLARE_EVENT_POLLING_LOOKBACK", "45m")
	t.Setenv("EMAIL_CLOUDFLARE_EVENT_POLLING_LIMIT", "250")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.True(t, cfg.Email.CloudflareEventPollingEnabled)
	assert.Equal(t, "zone-id", cfg.Email.CloudflareZoneID)
	assert.Equal(t, "analytics-token", cfg.Email.CloudflareAnalyticsAPIToken)
	assert.Equal(t, "10m0s", cfg.Email.CloudflareEventPollingInterval.String())
	assert.Equal(t, "45m0s", cfg.Email.CloudflareEventPollingLookback.String())
	assert.Equal(t, 250, cfg.Email.CloudflareEventPollingLimit)
}

func TestNewConfig_CloudflareEventPollingRequiresZoneAndToken(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "missing zone",
			env: map[string]string{
				"EMAIL_CLOUDFLARE_ANALYTICS_API_TOKEN": "analytics-token",
			},
			want: "EMAIL_CLOUDFLARE_ZONE_ID is required",
		},
		{
			name: "missing token",
			env: map[string]string{
				"EMAIL_CLOUDFLARE_ZONE_ID": "zone-id",
			},
			want: "EMAIL_CLOUDFLARE_ANALYTICS_API_TOKEN is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("EMAIL_CLOUDFLARE_EVENT_POLLING_ENABLED", "true")
			t.Setenv("EMAIL_CLOUDFLARE_ZONE_ID", "")
			t.Setenv("EMAIL_CLOUDFLARE_ANALYTICS_API_TOKEN", "")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			_, err := NewConfig()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestNewConfig_InvalidEmail(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{
			name:  "missing account id",
			key:   "CF_EMAIL_ACCOUNT_ID",
			value: "",
		},
		{
			name:  "missing token",
			key:   "CF_EMAIL_API_TOKEN",
			value: "",
		},
		{
			name:  "invalid from",
			key:   "EMAIL_FROM_ADDRESS",
			value: "not-an-email",
		},
		{
			name:  "invalid verify url",
			key:   "EMAIL_VERIFY_FRONTEND_URL",
			value: "/verify-email",
		},
		{
			name:  "invalid password reset url",
			key:   "PASSWORD_RESET_FRONTEND_URL",
			value: "/reset-password",
		},
		{
			name:  "invalid email change url",
			key:   "EMAIL_CHANGE_FRONTEND_URL",
			value: "/change-email",
		},
		{
			name:  "invalid unsubscribe public url",
			key:   "EMAIL_UNSUBSCRIBE_PUBLIC_URL",
			value: "/v1/email/unsubscribe",
		},
		{
			name:  "invalid unsubscribe token key id",
			key:   "EMAIL_UNSUBSCRIBE_TOKEN_KEY_ID",
			value: "bad.key",
		},
		{
			name:  "invalid unsubscribe token secrets json",
			key:   "EMAIL_UNSUBSCRIBE_TOKEN_SECRETS",
			value: "{",
		},
		{
			name:  "missing current unsubscribe token secret",
			key:   "EMAIL_UNSUBSCRIBE_TOKEN_SECRETS",
			value: `{"previous":"old-secret"}`,
		},
		{
			name:  "zero ttl",
			key:   "EMAIL_VERIFICATION_TTL",
			value: "0s",
		},
		{
			name:  "zero verification otp ttl",
			key:   "EMAIL_VERIFICATION_OTP_TTL",
			value: "0s",
		},
		{
			name:  "zero cooldown",
			key:   "EMAIL_RESEND_COOLDOWN",
			value: "0s",
		},
		{
			name:  "zero password reset ttl",
			key:   "PASSWORD_RESET_TTL",
			value: "0s",
		},
		{
			name:  "zero password reset cooldown",
			key:   "PASSWORD_RESET_RESEND_COOLDOWN",
			value: "0s",
		},
		{
			name:  "zero email change ttl",
			key:   "EMAIL_CHANGE_TTL",
			value: "0s",
		},
		{
			name:  "zero email change otp ttl",
			key:   "EMAIL_CHANGE_OTP_TTL",
			value: "0s",
		},
		{
			name:  "zero email change cooldown",
			key:   "EMAIL_CHANGE_RESEND_COOLDOWN",
			value: "0s",
		},
		{
			name:  "zero timeout",
			key:   "EMAIL_HTTP_TIMEOUT",
			value: "0s",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(tc.key, tc.value)

			_, err := NewConfig()

			require.Error(t, err)
		})
	}
}

func TestNewConfig_LogEmailDeliveryMode(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EMAIL_DELIVERY_MODE", "log")
	t.Setenv("CF_EMAIL_ACCOUNT_ID", "")
	t.Setenv("CF_EMAIL_API_TOKEN", "")
	t.Setenv("EMAIL_FROM_ADDRESS", "")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, EmailDeliveryModeLog, cfg.Email.DeliveryMode)
}

func TestNewConfig_InvalidEmailDeliveryMode(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EMAIL_DELIVERY_MODE", "smtp")

	_, err := NewConfig()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "EMAIL_DELIVERY_MODE")
}

func TestNewConfig_JWTDefaults(t *testing.T) {
	setRequiredEnv(t)
	unsetEnv(t, "JWT_ISSUER")
	unsetEnv(t, "JWT_AUDIENCE")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, jwtpkg.DefaultIssuer, cfg.JWT.Issuer)
	assert.Equal(t, jwtpkg.DefaultAudience, cfg.JWT.Audience)
}

func TestNewConfig_AuthRateLimitDefaults(t *testing.T) {
	setRequiredEnv(t)
	unsetEnv(t, "AUTH_RATE_LIMIT_ENABLED")
	unsetEnv(t, "AUTH_RATE_LIMIT_LOGIN_EMAIL_MAX")
	unsetEnv(t, "AUTH_RATE_LIMIT_LOGIN_EMAIL_WINDOW")
	unsetEnv(t, "AUTH_RATE_LIMIT_VERIFY_EMAIL_OTP_EMAIL_WINDOW")
	unsetEnv(t, "AUTH_RATE_LIMIT_RESET_PASSWORD_TOKEN_WINDOW")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.True(t, cfg.AuthRateLimit.Enabled)
	assert.Equal(t, 5, cfg.AuthRateLimit.LoginEmailMax)
	assert.Equal(t, "5m0s", cfg.AuthRateLimit.LoginEmailWindow.String())
	assert.Equal(t, "15m0s", cfg.AuthRateLimit.VerifyEmailOTPEmailWindow.String())
	assert.Equal(t, "15m0s", cfg.AuthRateLimit.ResetPasswordTokenWindow.String())
}

func TestNewConfig_AuthEmailNotificationDefaults(t *testing.T) {
	setRequiredEnv(t)
	unsetEnv(t, "AUTH_EMAIL_NOTIFICATIONS_ENABLED")
	unsetEnv(t, "AUTH_FAILED_LOGIN_EMAIL_COOLDOWN")
	unsetEnv(t, "AUTH_NEW_LOGIN_EMAIL_ENABLED")
	unsetEnv(t, "AUTH_FAILED_LOGIN_EMAIL_ENABLED")
	unsetEnv(t, "AUTH_PASSWORD_CHANGED_EMAIL_ENABLED")
	unsetEnv(t, "AUTH_EMAIL_VERIFIED_EMAIL_ENABLED")
	unsetEnv(t, "AUTH_ROLE_CHANGED_EMAIL_ENABLED")
	unsetEnv(t, "AUTH_EMAIL_CHANGED_EMAIL_ENABLED")
	unsetEnv(t, "AUTH_ACCOUNT_DELETED_EMAIL_ENABLED")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.True(t, cfg.AuthEmail.NotificationsEnabled)
	assert.Equal(t, "24h0m0s", cfg.AuthEmail.FailedLoginCooldown.String())
	assert.True(t, cfg.AuthEmail.NewLoginEnabled)
	assert.True(t, cfg.AuthEmail.FailedLoginEnabled)
	assert.True(t, cfg.AuthEmail.PasswordChangedEnabled)
	assert.True(t, cfg.AuthEmail.EmailVerifiedEnabled)
	assert.True(t, cfg.AuthEmail.RoleChangedEnabled)
	assert.True(t, cfg.AuthEmail.EmailChangedEnabled)
	assert.True(t, cfg.AuthEmail.AccountDeletedEnabled)
}

func TestNewConfig_InvalidAuthEmailNotification(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("AUTH_FAILED_LOGIN_EMAIL_COOLDOWN", "0s")

	_, err := NewConfig()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_FAILED_LOGIN_EMAIL_COOLDOWN")
}

func TestNewConfig_InvalidAuthRateLimit(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{
			name:  "zero max",
			key:   "AUTH_RATE_LIMIT_LOGIN_EMAIL_MAX",
			value: "0",
		},
		{
			name:  "zero window",
			key:   "AUTH_RATE_LIMIT_LOGIN_EMAIL_WINDOW",
			value: "0s",
		},
		{
			name:  "zero reset token max",
			key:   "AUTH_RATE_LIMIT_RESET_PASSWORD_TOKEN_MAX",
			value: "0",
		},
		{
			name:  "zero verify otp email window",
			key:   "AUTH_RATE_LIMIT_VERIFY_EMAIL_OTP_EMAIL_WINDOW",
			value: "0s",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(tc.key, tc.value)

			_, err := NewConfig()

			require.Error(t, err)
			assert.Contains(t, err.Error(), "AUTH_RATE_LIMIT_")
		})
	}
}

func TestNewConfig_InvalidJWT(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{
			name:  "weak secret",
			key:   "JWT_SECRET",
			value: "short",
		},
		{
			name:  "zero expiry",
			key:   "JWT_TOKEN_EXPIRY",
			value: "0s",
		},
		{
			name:  "expiry greater than 24h",
			key:   "JWT_TOKEN_EXPIRY",
			value: "25h",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(tc.key, tc.value)

			_, err := NewConfig()

			require.Error(t, err)
			assert.Contains(t, err.Error(), "JWT_")
		})
	}
}

//nolint:paralleltest // t.Setenv mutates process-global environment variables
func TestNewConfig_JWTKeysetFileReplacesLegacySecretRequirement(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("JWT_SECRET", "")
	t.Setenv("JWT_KEYSET_FILE", "  /run/secrets/surau-jwt/keyset.json  ")
	t.Setenv("MFA_ENCRYPTION_KEY", "dedicated-mfa-encryption-key-32-bytes")
	t.Setenv("EMAIL_UNSUBSCRIBE_TOKEN_SECRET", "dedicated-unsubscribe-secret")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Empty(t, cfg.JWT.Secret)
	assert.Equal(t, "/run/secrets/surau-jwt/keyset.json", cfg.JWT.KeysetFile)
}

//nolint:paralleltest // t.Setenv mutates process-global environment variables
func TestNewConfig_JWTKeysetOnlyRequiresIndependentDerivedSecrets(t *testing.T) {
	setRequiredEnv(t)
	// The invariant applies from the first overlap deploy, while the legacy
	// signer secret is intentionally still present.
	t.Setenv("JWT_SECRET", "legacy-secret-remains-during-overlap")
	t.Setenv("JWT_KEYSET_FILE", "/run/secrets/surau-jwt/keyset.json")
	t.Setenv("MFA_ENCRYPTION_KEY", "")

	_, err := NewConfig()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "MFA_ENCRYPTION_KEY is required when JWT_KEYSET_FILE is set")

	t.Setenv("MFA_ENCRYPTION_KEY", "dedicated-mfa-encryption-key-32-bytes")
	t.Setenv("EMAIL_UNSUBSCRIBE_TOKEN_SECRET", "")
	t.Setenv("EMAIL_UNSUBSCRIBE_TOKEN_SECRETS", "")

	_, err = NewConfig()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "EMAIL_UNSUBSCRIBE_TOKEN_SECRET")
}

//nolint:paralleltest // t.Setenv mutates process-global environment variables
func TestNewConfig_JWTRequiresSecretOrKeysetFile(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("JWT_SECRET", "")
	t.Setenv("JWT_KEYSET_FILE", "")

	_, err := NewConfig()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET or JWT_KEYSET_FILE")
}

//nolint:paralleltest // t.Setenv (via setRequiredEnv) is incompatible with t.Parallel
func TestNewConfig_AuthSessionDefaults(t *testing.T) {
	setRequiredEnv(t)
	unsetEnv(t, "JWT_ACCESS_TOKEN_EXPIRY")
	unsetEnv(t, "JWT_REFRESH_TOKEN_EXPIRY")
	unsetEnv(t, "AUTH_LOCKOUT_ENABLED")
	unsetEnv(t, "AUTH_CLEANUP_ENABLED")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, "15m0s", cfg.JWT.AccessTokenExpiry.String())
	assert.Equal(t, "720h0m0s", cfg.JWT.RefreshTokenExpiry.String())
	assert.True(t, cfg.AuthLockout.Enabled)
	assert.Equal(t, 5, cfg.AuthLockout.Threshold)
	assert.Equal(t, "1m0s", cfg.AuthLockout.BaseDuration.String())
	assert.Equal(t, 15, cfg.AuthLockout.Factor)
	assert.Equal(t, "1h0m0s", cfg.AuthLockout.MaxDuration.String())
	assert.True(t, cfg.AuthCleanup.Enabled)
	assert.Equal(t, "6h0m0s", cfg.AuthCleanup.Interval.String())
	assert.Equal(t, "720h0m0s", cfg.AuthCleanup.TokenRetention.String())
	assert.Equal(t, "720h0m0s", cfg.AuthCleanup.SessionRetention.String())
	assert.Equal(t, "0s", cfg.AuthCleanup.AuditRetention.String())
}

func TestNewConfig_AuthSessionValidation(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{
			name:    "access expiry over 24h",
			key:     "JWT_ACCESS_TOKEN_EXPIRY",
			value:   "25h",
			wantErr: "JWT_ACCESS_TOKEN_EXPIRY",
		},
		{
			name:    "refresh expiry below access expiry",
			key:     "JWT_REFRESH_TOKEN_EXPIRY",
			value:   "5m",
			wantErr: "JWT_REFRESH_TOKEN_EXPIRY",
		},
		{
			name:    "refresh expiry over one year",
			key:     "JWT_REFRESH_TOKEN_EXPIRY",
			value:   "9000h",
			wantErr: "JWT_REFRESH_TOKEN_EXPIRY",
		},
		{
			name:    "lockout threshold zero",
			key:     "AUTH_LOCKOUT_THRESHOLD",
			value:   "0",
			wantErr: "AUTH_LOCKOUT_THRESHOLD",
		},
		{
			name:    "lockout max below base",
			key:     "AUTH_LOCKOUT_MAX_DURATION",
			value:   "1s",
			wantErr: "AUTH_LOCKOUT_MAX_DURATION",
		},
		{
			name:    "cleanup interval zero",
			key:     "AUTH_CLEANUP_INTERVAL",
			value:   "0",
			wantErr: "AUTH_CLEANUP_INTERVAL",
		},
		{
			name:    "refresh rate limit max zero",
			key:     "AUTH_RATE_LIMIT_REFRESH_TOKEN_MAX",
			value:   "0",
			wantErr: "AUTH_RATE_LIMIT_REFRESH_TOKEN_MAX",
		},
		{
			name:    "verify email token window zero",
			key:     "AUTH_RATE_LIMIT_VERIFY_EMAIL_TOKEN_WINDOW",
			value:   "0",
			wantErr: "AUTH_RATE_LIMIT_VERIFY_EMAIL_TOKEN_WINDOW",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(tc.key, tc.value)

			_, err := NewConfig()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
