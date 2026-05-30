package config

import (
	"os"
	"strings"
	"testing"

	jwtpkg "github.com/evrone/go-clean-template/pkg/jwt"
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

func TestNewConfig_EmailDefaults(t *testing.T) {
	setRequiredEnv(t)
	unsetEnv(t, "EMAIL_FROM_NAME")
	unsetEnv(t, "EMAIL_VERIFICATION_TTL")
	unsetEnv(t, "EMAIL_RESEND_COOLDOWN")
	unsetEnv(t, "PASSWORD_RESET_TTL")
	unsetEnv(t, "PASSWORD_RESET_RESEND_COOLDOWN")
	unsetEnv(t, "EMAIL_CHANGE_TTL")
	unsetEnv(t, "EMAIL_CHANGE_RESEND_COOLDOWN")
	unsetEnv(t, "EMAIL_HTTP_TIMEOUT")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.Equal(t, EmailDeliveryModeCloudflare, cfg.Email.DeliveryMode)
	assert.Equal(t, "Surau", cfg.Email.FromName)
	assert.Equal(t, "24h0m0s", cfg.Email.VerificationTTL.String())
	assert.Equal(t, "1m0s", cfg.Email.ResendCooldown.String())
	assert.Equal(t, "1h0m0s", cfg.Email.PasswordResetTTL.String())
	assert.Equal(t, "1m0s", cfg.Email.PasswordResetCooldown.String())
	assert.Equal(t, "24h0m0s", cfg.Email.EmailChangeTTL.String())
	assert.Equal(t, "1m0s", cfg.Email.EmailChangeCooldown.String())
	assert.Equal(t, "10s", cfg.Email.HTTPTimeout.String())
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
			name:  "zero ttl",
			key:   "EMAIL_VERIFICATION_TTL",
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
	unsetEnv(t, "AUTH_RATE_LIMIT_RESET_PASSWORD_TOKEN_WINDOW")

	cfg, err := NewConfig()

	require.NoError(t, err)
	assert.True(t, cfg.AuthRateLimit.Enabled)
	assert.Equal(t, 5, cfg.AuthRateLimit.LoginEmailMax)
	assert.Equal(t, "5m0s", cfg.AuthRateLimit.LoginEmailWindow.String())
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
