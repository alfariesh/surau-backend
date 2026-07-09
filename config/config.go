package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

const (
	EmailDeliveryModeCloudflare = "cloudflare"
	EmailDeliveryModeLog        = "log"

	minCollabServiceTokenBytes = 32
)

// errConfig is the static root of every configuration validation error.
var errConfig = errors.New("config error")

func configError(msg string) error {
	return fmt.Errorf("%w: %s", errConfig, msg)
}

func configErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errConfig, fmt.Sprintf(format, args...))
}

func validateMFA(m mfa) error {
	if m.StepUpTTL <= 0 {
		return configError("MFA_STEP_UP_TTL must be positive")
	}

	if m.EnrollmentGrace <= 0 {
		return configError("MFA_ENROLLMENT_GRACE must be positive")
	}

	if m.ChallengeTTL <= 0 {
		return configError("MFA_CHALLENGE_TTL must be positive")
	}

	if m.ResetTTL <= 0 {
		return configError("MFA_RESET_TTL must be positive")
	}

	if key := strings.TrimSpace(m.EncryptionKey); key != "" && len(key) < 32 {
		return configError("MFA_ENCRYPTION_KEY must be at least 32 bytes when set")
	}

	return nil
}

func validateAuthLockout(lockout authLockout) error {
	if !lockout.Enabled {
		return nil
	}

	if lockout.Threshold <= 0 {
		return configError("AUTH_LOCKOUT_THRESHOLD must be positive")
	}

	if lockout.Factor <= 0 {
		return configError("AUTH_LOCKOUT_FACTOR must be positive")
	}

	if lockout.BaseDuration <= 0 {
		return configError("AUTH_LOCKOUT_BASE_DURATION must be positive")
	}

	if lockout.MaxDuration < lockout.BaseDuration {
		return configError("AUTH_LOCKOUT_MAX_DURATION must be at least AUTH_LOCKOUT_BASE_DURATION")
	}

	return nil
}

func validateAuthCleanup(cleanup authCleanup) error {
	if !cleanup.Enabled {
		return nil
	}

	if cleanup.Interval <= 0 {
		return configError("AUTH_CLEANUP_INTERVAL must be positive")
	}

	if cleanup.TokenRetention <= 0 {
		return configError("AUTH_CLEANUP_TOKEN_RETENTION must be positive")
	}

	if cleanup.SessionRetention <= 0 {
		return configError("AUTH_CLEANUP_SESSION_RETENTION must be positive")
	}

	if cleanup.AuditRetention < 0 {
		return configError("AUTH_CLEANUP_AUDIT_RETENTION must not be negative")
	}

	return nil
}

type (
	// Config -.
	Config struct {
		App           app
		HTTP          http
		CORS          cors
		Security      security
		Log           log
		PG            pg
		JWT           jwt
		Email         email
		AuthRateLimit authRateLimit
		AuthLockout   authLockout
		AuthCleanup   authCleanup
		AuthEmail     authEmail
		AuthAlert     authAlert
		CitableAudit  citableAudit
		MFA           mfa
		RAG           rag
		Collab        collab
		Metrics       metrics
		Otel          otel
		Swagger       swagger
		OneSignal     oneSignal
	}

	// App -.
	app struct {
		Name    string `env:"APP_NAME,required"`
		Version string `env:"APP_VERSION,required"`
		// Env is the deployment environment (dev/prod), surfaced by GET /version so a
		// deploy can be verified and clients can tell which backend they hit.
		Env string `env:"APP_ENV" envDefault:"dev"`
	}

	// HTTP -.
	http struct {
		Port               string   `env:"HTTP_PORT,required"`
		UsePreforkMode     bool     `env:"HTTP_USE_PREFORK_MODE" envDefault:"false"`
		ProxyHeader        string   `env:"HTTP_PROXY_HEADER" envDefault:"X-Real-IP"`
		TrustedProxies     []string `env:"HTTP_TRUSTED_PROXIES" envSeparator:","`
		BodyLimitBytes     int      `env:"HTTP_BODY_LIMIT_BYTES" envDefault:"4194304"`
		CompressionEnabled bool     `env:"HTTP_COMPRESSION_ENABLED" envDefault:"true"`
	}

	// CORS configures cross-origin resource sharing for browser clients.
	// An empty AllowedOrigins list leaves the CORS middleware unregistered
	// (same-origin clients only).
	cors struct {
		AllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" envSeparator:","`
	}

	// Security configures HTTP security response headers. HSTSSeconds 0
	// disables Strict-Transport-Security (only enable behind TLS).
	security struct {
		HeadersEnabled bool `env:"SECURITY_HEADERS_ENABLED" envDefault:"true"`
		HSTSSeconds    int  `env:"SECURITY_HSTS_SECONDS" envDefault:"0"`
	}

	// Collab configures the realtime collaborative editing bridge. When
	// enabled, the app exposes /internal/collab endpoints (service-token
	// guarded) that the collab-server uses to seed and persist page drafts.
	collab struct {
		Enabled      bool   `env:"COLLAB_ENABLED" envDefault:"false"`
		ServiceToken string `env:"COLLAB_SERVICE_TOKEN"`
	}

	// Log -.
	log struct {
		Level string `env:"LOG_LEVEL,required"`
	}

	// PG -.
	pg struct {
		PoolMax int    `env:"PG_POOL_MAX" envDefault:"10"`
		URL     string `env:"PG_URL,required"`
		// Zero keeps the pgxpool default for either setting.
		MaxConnLifetime time.Duration `env:"PG_MAX_CONN_LIFETIME" envDefault:"1h"`
		MaxConnIdleTime time.Duration `env:"PG_MAX_CONN_IDLE_TIME" envDefault:"30m"`
	}

	// JWT -.
	jwt struct {
		Secret string `env:"JWT_SECRET,required"`
		// TokenExpiry is the legacy single-token TTL; signing now uses
		// AccessTokenExpiry. Kept validated for env back-compat.
		TokenExpiry        time.Duration `env:"JWT_TOKEN_EXPIRY" envDefault:"24h"`
		AccessTokenExpiry  time.Duration `env:"JWT_ACCESS_TOKEN_EXPIRY" envDefault:"15m"`
		RefreshTokenExpiry time.Duration `env:"JWT_REFRESH_TOKEN_EXPIRY" envDefault:"720h"`
		Issuer             string        `env:"JWT_ISSUER" envDefault:"surau-backend"`
		Audience           string        `env:"JWT_AUDIENCE" envDefault:"surau-api"`
	}

	// Email -.
	email struct {
		DeliveryMode                   string        `env:"EMAIL_DELIVERY_MODE" envDefault:"cloudflare"`
		CloudflareAccountID            string        `env:"CF_EMAIL_ACCOUNT_ID"`
		CloudflareAPIToken             string        `env:"CF_EMAIL_API_TOKEN"`
		FromAddress                    string        `env:"EMAIL_FROM_ADDRESS"`
		FromName                       string        `env:"EMAIL_FROM_NAME" envDefault:"Surau"`
		ReplyTo                        string        `env:"EMAIL_REPLY_TO"`
		VerifyFrontendURL              string        `env:"EMAIL_VERIFY_FRONTEND_URL"`
		VerificationTTL                time.Duration `env:"EMAIL_VERIFICATION_TTL" envDefault:"24h"`
		VerificationOTPTTL             time.Duration `env:"EMAIL_VERIFICATION_OTP_TTL" envDefault:"10m"`
		ResendCooldown                 time.Duration `env:"EMAIL_RESEND_COOLDOWN" envDefault:"1m"`
		PasswordResetFrontendURL       string        `env:"PASSWORD_RESET_FRONTEND_URL"`
		PasswordResetTTL               time.Duration `env:"PASSWORD_RESET_TTL" envDefault:"1h"`
		PasswordResetCooldown          time.Duration `env:"PASSWORD_RESET_RESEND_COOLDOWN" envDefault:"1m"`
		EmailChangeFrontendURL         string        `env:"EMAIL_CHANGE_FRONTEND_URL"`
		EmailChangeTTL                 time.Duration `env:"EMAIL_CHANGE_TTL" envDefault:"24h"`
		EmailChangeOTPTTL              time.Duration `env:"EMAIL_CHANGE_OTP_TTL" envDefault:"10m"`
		EmailChangeCooldown            time.Duration `env:"EMAIL_CHANGE_RESEND_COOLDOWN" envDefault:"1m"`
		UnsubscribeFrontendURL         string        `env:"EMAIL_UNSUBSCRIBE_FRONTEND_URL"`
		UnsubscribePublicURL           string        `env:"EMAIL_UNSUBSCRIBE_PUBLIC_URL"`
		UnsubscribeTokenKeyID          string        `env:"EMAIL_UNSUBSCRIBE_TOKEN_KEY_ID" envDefault:"default"`
		UnsubscribeTokenSecret         string        `env:"EMAIL_UNSUBSCRIBE_TOKEN_SECRET"`
		UnsubscribeTokenSecrets        string        `env:"EMAIL_UNSUBSCRIBE_TOKEN_SECRETS"`
		CloudflareWebhookSecret        string        `env:"EMAIL_CLOUDFLARE_WEBHOOK_SECRET"`
		CloudflareEventPollingEnabled  bool          `env:"EMAIL_CLOUDFLARE_EVENT_POLLING_ENABLED" envDefault:"false"`
		CloudflareZoneID               string        `env:"EMAIL_CLOUDFLARE_ZONE_ID"`
		CloudflareAnalyticsAPIToken    string        `env:"EMAIL_CLOUDFLARE_ANALYTICS_API_TOKEN"`
		CloudflareEventPollingInterval time.Duration `env:"EMAIL_CLOUDFLARE_EVENT_POLLING_INTERVAL" envDefault:"5m"`
		CloudflareEventPollingLookback time.Duration `env:"EMAIL_CLOUDFLARE_EVENT_POLLING_LOOKBACK" envDefault:"30m"`
		CloudflareEventPollingLimit    int           `env:"EMAIL_CLOUDFLARE_EVENT_POLLING_LIMIT" envDefault:"100"`
		HTTPTimeout                    time.Duration `env:"EMAIL_HTTP_TIMEOUT" envDefault:"10s"`
		// DispatchInterval paces the background dispatcher that delivers
		// queued transactional and campaign emails; auth emails are enqueued,
		// so this bounds their worst-case delivery latency.
		DispatchInterval time.Duration `env:"EMAIL_DISPATCH_INTERVAL" envDefault:"15s"`
		DispatchBatch    int           `env:"EMAIL_DISPATCH_BATCH" envDefault:"20"`
	}

	// OneSignal -. Push-notification delivery via the OneSignal REST API. Disabled by default so the
	// app builds and runs without credentials; the REST API key is a secret and must never be committed.
	oneSignal struct {
		Enabled          bool          `env:"ONESIGNAL_ENABLED" envDefault:"false"`
		AppID            string        `env:"ONESIGNAL_APP_ID"`
		RESTAPIKey       string        `env:"ONESIGNAL_REST_API_KEY"`
		HTTPTimeout      time.Duration `env:"ONESIGNAL_HTTP_TIMEOUT" envDefault:"10s"`
		ReminderInterval time.Duration `env:"ONESIGNAL_REMINDER_INTERVAL" envDefault:"1h"`
	}

	// AuthRateLimit -.
	authRateLimit struct {
		Enabled                       bool          `env:"AUTH_RATE_LIMIT_ENABLED" envDefault:"true"`
		LoginEmailMax                 int           `env:"AUTH_RATE_LIMIT_LOGIN_EMAIL_MAX" envDefault:"5"`
		LoginEmailWindow              time.Duration `env:"AUTH_RATE_LIMIT_LOGIN_EMAIL_WINDOW" envDefault:"5m"`
		LoginIPMax                    int           `env:"AUTH_RATE_LIMIT_LOGIN_IP_MAX" envDefault:"30"`
		LoginIPWindow                 time.Duration `env:"AUTH_RATE_LIMIT_LOGIN_IP_WINDOW" envDefault:"5m"`
		RegisterEmailMax              int           `env:"AUTH_RATE_LIMIT_REGISTER_EMAIL_MAX" envDefault:"3"`
		RegisterEmailWindow           time.Duration `env:"AUTH_RATE_LIMIT_REGISTER_EMAIL_WINDOW" envDefault:"1h"`
		RegisterIPMax                 int           `env:"AUTH_RATE_LIMIT_REGISTER_IP_MAX" envDefault:"10"`
		RegisterIPWindow              time.Duration `env:"AUTH_RATE_LIMIT_REGISTER_IP_WINDOW" envDefault:"1h"`
		ForgotPasswordEmailMax        int           `env:"AUTH_RATE_LIMIT_FORGOT_PASSWORD_EMAIL_MAX" envDefault:"3"`
		ForgotPasswordEmailWindow     time.Duration `env:"AUTH_RATE_LIMIT_FORGOT_PASSWORD_EMAIL_WINDOW" envDefault:"1h"`
		ForgotPasswordIPMax           int           `env:"AUTH_RATE_LIMIT_FORGOT_PASSWORD_IP_MAX" envDefault:"20"`
		ForgotPasswordIPWindow        time.Duration `env:"AUTH_RATE_LIMIT_FORGOT_PASSWORD_IP_WINDOW" envDefault:"1h"`
		VerifyEmailOTPEmailMax        int           `env:"AUTH_RATE_LIMIT_VERIFY_EMAIL_OTP_EMAIL_MAX" envDefault:"5"`
		VerifyEmailOTPEmailWindow     time.Duration `env:"AUTH_RATE_LIMIT_VERIFY_EMAIL_OTP_EMAIL_WINDOW" envDefault:"15m"`
		VerifyEmailOTPIPMax           int           `env:"AUTH_RATE_LIMIT_VERIFY_EMAIL_OTP_IP_MAX" envDefault:"30"`
		VerifyEmailOTPIPWindow        time.Duration `env:"AUTH_RATE_LIMIT_VERIFY_EMAIL_OTP_IP_WINDOW" envDefault:"15m"`
		ResendVerificationEmailMax    int           `env:"AUTH_RATE_LIMIT_RESEND_VERIFICATION_EMAIL_MAX" envDefault:"3"`
		ResendVerificationEmailWindow time.Duration `env:"AUTH_RATE_LIMIT_RESEND_VERIFICATION_EMAIL_WINDOW" envDefault:"1h"`
		ResendVerificationIPMax       int           `env:"AUTH_RATE_LIMIT_RESEND_VERIFICATION_IP_MAX" envDefault:"20"`
		ResendVerificationIPWindow    time.Duration `env:"AUTH_RATE_LIMIT_RESEND_VERIFICATION_IP_WINDOW" envDefault:"1h"`
		ResetPasswordTokenMax         int           `env:"AUTH_RATE_LIMIT_RESET_PASSWORD_TOKEN_MAX" envDefault:"5"`
		ResetPasswordTokenWindow      time.Duration `env:"AUTH_RATE_LIMIT_RESET_PASSWORD_TOKEN_WINDOW" envDefault:"15m"`
		ResetPasswordIPMax            int           `env:"AUTH_RATE_LIMIT_RESET_PASSWORD_IP_MAX" envDefault:"30"`
		ResetPasswordIPWindow         time.Duration `env:"AUTH_RATE_LIMIT_RESET_PASSWORD_IP_WINDOW" envDefault:"15m"`
		ChangePasswordUserMax         int           `env:"AUTH_RATE_LIMIT_CHANGE_PASSWORD_USER_MAX" envDefault:"5"`
		ChangePasswordUserWindow      time.Duration `env:"AUTH_RATE_LIMIT_CHANGE_PASSWORD_USER_WINDOW" envDefault:"5m"`
		ChangePasswordIPMax           int           `env:"AUTH_RATE_LIMIT_CHANGE_PASSWORD_IP_MAX" envDefault:"30"`
		ChangePasswordIPWindow        time.Duration `env:"AUTH_RATE_LIMIT_CHANGE_PASSWORD_IP_WINDOW" envDefault:"5m"`
		ChangeEmailUserMax            int           `env:"AUTH_RATE_LIMIT_CHANGE_EMAIL_USER_MAX" envDefault:"3"`
		ChangeEmailUserWindow         time.Duration `env:"AUTH_RATE_LIMIT_CHANGE_EMAIL_USER_WINDOW" envDefault:"1h"`
		ChangeEmailIPMax              int           `env:"AUTH_RATE_LIMIT_CHANGE_EMAIL_IP_MAX" envDefault:"10"`
		ChangeEmailIPWindow           time.Duration `env:"AUTH_RATE_LIMIT_CHANGE_EMAIL_IP_WINDOW" envDefault:"1h"`
		ChangeEmailTokenMax           int           `env:"AUTH_RATE_LIMIT_CHANGE_EMAIL_TOKEN_MAX" envDefault:"5"`
		ChangeEmailTokenWindow        time.Duration `env:"AUTH_RATE_LIMIT_CHANGE_EMAIL_TOKEN_WINDOW" envDefault:"15m"`
		DeleteAccountUserMax          int           `env:"AUTH_RATE_LIMIT_DELETE_ACCOUNT_USER_MAX" envDefault:"3"`
		DeleteAccountUserWindow       time.Duration `env:"AUTH_RATE_LIMIT_DELETE_ACCOUNT_USER_WINDOW" envDefault:"1h"`
		DeleteAccountIPMax            int           `env:"AUTH_RATE_LIMIT_DELETE_ACCOUNT_IP_MAX" envDefault:"10"`
		DeleteAccountIPWindow         time.Duration `env:"AUTH_RATE_LIMIT_DELETE_ACCOUNT_IP_WINDOW" envDefault:"1h"`
		RefreshTokenMax               int           `env:"AUTH_RATE_LIMIT_REFRESH_TOKEN_MAX" envDefault:"5"`
		RefreshTokenWindow            time.Duration `env:"AUTH_RATE_LIMIT_REFRESH_TOKEN_WINDOW" envDefault:"15m"`
		RefreshIPMax                  int           `env:"AUTH_RATE_LIMIT_REFRESH_IP_MAX" envDefault:"60"`
		RefreshIPWindow               time.Duration `env:"AUTH_RATE_LIMIT_REFRESH_IP_WINDOW" envDefault:"15m"`
		VerifyEmailTokenMax           int           `env:"AUTH_RATE_LIMIT_VERIFY_EMAIL_TOKEN_MAX" envDefault:"5"`
		VerifyEmailTokenWindow        time.Duration `env:"AUTH_RATE_LIMIT_VERIFY_EMAIL_TOKEN_WINDOW" envDefault:"15m"`
		MFAVerifyTokenMax             int           `env:"AUTH_RATE_LIMIT_MFA_VERIFY_TOKEN_MAX" envDefault:"5"`
		MFAVerifyTokenWindow          time.Duration `env:"AUTH_RATE_LIMIT_MFA_VERIFY_TOKEN_WINDOW" envDefault:"5m"`
		MFAVerifyIPMax                int           `env:"AUTH_RATE_LIMIT_MFA_VERIFY_IP_MAX" envDefault:"30"`
		MFAVerifyIPWindow             time.Duration `env:"AUTH_RATE_LIMIT_MFA_VERIFY_IP_WINDOW" envDefault:"15m"`
		MFAStepUpUserMax              int           `env:"AUTH_RATE_LIMIT_MFA_STEP_UP_USER_MAX" envDefault:"10"`
		MFAStepUpUserWindow           time.Duration `env:"AUTH_RATE_LIMIT_MFA_STEP_UP_USER_WINDOW" envDefault:"5m"`
		MFAStepUpIPMax                int           `env:"AUTH_RATE_LIMIT_MFA_STEP_UP_IP_MAX" envDefault:"60"`
		MFAStepUpIPWindow             time.Duration `env:"AUTH_RATE_LIMIT_MFA_STEP_UP_IP_WINDOW" envDefault:"15m"`
		MFAResetEmailMax              int           `env:"AUTH_RATE_LIMIT_MFA_RESET_EMAIL_MAX" envDefault:"3"`
		MFAResetEmailWindow           time.Duration `env:"AUTH_RATE_LIMIT_MFA_RESET_EMAIL_WINDOW" envDefault:"1h"`
		MFAResetIPMax                 int           `env:"AUTH_RATE_LIMIT_MFA_RESET_IP_MAX" envDefault:"10"`
		MFAResetIPWindow              time.Duration `env:"AUTH_RATE_LIMIT_MFA_RESET_IP_WINDOW" envDefault:"1h"`
	}

	// MFA (A-3) -.
	mfa struct {
		// StepUpTTL is how long a proven second factor keeps a session
		// "fresh" for destructive admin routes.
		StepUpTTL time.Duration `env:"MFA_STEP_UP_TTL" envDefault:"10m"`
		// EnrollmentGrace is how long an MFA-mandated role may operate
		// without enrolling, counted per-account from mfa_enforced_from.
		EnrollmentGrace time.Duration `env:"MFA_ENROLLMENT_GRACE" envDefault:"168h"`
		ChallengeTTL    time.Duration `env:"MFA_CHALLENGE_TTL" envDefault:"5m"`
		ResetTTL        time.Duration `env:"MFA_RESET_TTL" envDefault:"15m"`
		TOTPIssuer      string        `env:"MFA_TOTP_ISSUER" envDefault:"Surau"`
		// EncryptionKey seals TOTP secrets at rest. Empty = derive from
		// JWT_SECRET (set a dedicated key in prod so JWT rotation cannot
		// orphan MFA secrets).
		EncryptionKey string `env:"MFA_ENCRYPTION_KEY"`
	}

	// AuthLockout -.
	authLockout struct {
		Enabled      bool          `env:"AUTH_LOCKOUT_ENABLED" envDefault:"true"`
		Threshold    int           `env:"AUTH_LOCKOUT_THRESHOLD" envDefault:"5"`
		BaseDuration time.Duration `env:"AUTH_LOCKOUT_BASE_DURATION" envDefault:"1m"`
		Factor       int           `env:"AUTH_LOCKOUT_FACTOR" envDefault:"15"`
		MaxDuration  time.Duration `env:"AUTH_LOCKOUT_MAX_DURATION" envDefault:"1h"`
	}

	// AuthCleanup -.
	authCleanup struct {
		Enabled          bool          `env:"AUTH_CLEANUP_ENABLED" envDefault:"true"`
		Interval         time.Duration `env:"AUTH_CLEANUP_INTERVAL" envDefault:"6h"`
		TokenRetention   time.Duration `env:"AUTH_CLEANUP_TOKEN_RETENTION" envDefault:"720h"`
		SessionRetention time.Duration `env:"AUTH_CLEANUP_SESSION_RETENTION" envDefault:"720h"`
		// AuditRetention 0 keeps audit logs forever.
		AuditRetention time.Duration `env:"AUTH_CLEANUP_AUDIT_RETENTION" envDefault:"0"`
	}

	// AuthEmail -.
	authEmail struct {
		NotificationsEnabled   bool          `env:"AUTH_EMAIL_NOTIFICATIONS_ENABLED" envDefault:"true"`
		FailedLoginCooldown    time.Duration `env:"AUTH_FAILED_LOGIN_EMAIL_COOLDOWN" envDefault:"24h"`
		NewLoginEnabled        bool          `env:"AUTH_NEW_LOGIN_EMAIL_ENABLED" envDefault:"true"`
		FailedLoginEnabled     bool          `env:"AUTH_FAILED_LOGIN_EMAIL_ENABLED" envDefault:"true"`
		PasswordChangedEnabled bool          `env:"AUTH_PASSWORD_CHANGED_EMAIL_ENABLED" envDefault:"true"`
		EmailVerifiedEnabled   bool          `env:"AUTH_EMAIL_VERIFIED_EMAIL_ENABLED" envDefault:"true"`
		RoleChangedEnabled     bool          `env:"AUTH_ROLE_CHANGED_EMAIL_ENABLED" envDefault:"true"`
		EmailChangedEnabled    bool          `env:"AUTH_EMAIL_CHANGED_EMAIL_ENABLED" envDefault:"true"`
		AccountDeletedEnabled  bool          `env:"AUTH_ACCOUNT_DELETED_EMAIL_ENABLED" envDefault:"true"`
	}

	// AuthAlert configures admin alerting for high-signal security events
	// (currently refresh-token reuse).
	authAlert struct {
		Enabled    bool          `env:"AUTH_ALERT_ENABLED" envDefault:"false"`
		Interval   time.Duration `env:"AUTH_ALERT_INTERVAL" envDefault:"5m"`
		Recipients []string      `env:"AUTH_ALERT_RECIPIENTS" envSeparator:","`
	}

	// CitableAudit schedules the citable-unit registry integrity audit
	// (phase-1b B-1 "nol sitasi menggantung"). Enabled by default: it has no
	// external dependency and an integrity audit should not be silently off —
	// its violation gauges feed the Grafana → Telegram alert.
	citableAudit struct {
		Enabled  bool          `env:"CITABLE_AUDIT_ENABLED" envDefault:"true"`
		Interval time.Duration `env:"CITABLE_AUDIT_INTERVAL" envDefault:"1h"`
	}

	// RAG -.
	rag struct {
		LLMBaseURL           string        `env:"RAG_LLM_BASE_URL" envDefault:"https://ai.sumopod.com/v1"`
		LLMAPIKey            string        `env:"RAG_LLM_API_KEY"`
		LLMModel             string        `env:"RAG_LLM_MODEL" envDefault:"glm-5.1"`
		LLMTimeout           time.Duration `env:"RAG_LLM_TIMEOUT" envDefault:"45s"`
		LLMMaxTokens         int           `env:"RAG_LLM_MAX_TOKENS" envDefault:"1400"`
		LLMTemperature       float64       `env:"RAG_LLM_TEMPERATURE" envDefault:"0.1"`
		MaxContextPages      int           `env:"RAG_MAX_CONTEXT_PAGES" envDefault:"8"`
		TreeFullMaxNodes     int           `env:"RAG_TREE_FULL_MAX_NODES" envDefault:"450"`
		TreeBlockMaxNodes    int           `env:"RAG_TREE_BLOCK_MAX_NODES" envDefault:"120"`
		TreeBeamSize         int           `env:"RAG_TREE_BEAM_SIZE" envDefault:"3"`
		TreeMaxTurns         int           `env:"RAG_TREE_MAX_TURNS" envDefault:"6"`
		TreeMaxBlocksPerTurn int           `env:"RAG_TREE_MAX_BLOCKS_PER_TURN" envDefault:"6"`
	}

	// Metrics -.
	metrics struct {
		Enabled bool `env:"METRICS_ENABLED" envDefault:"true"`
	}

	// Otel — OpenTelemetry tracing (F1-B). Disabled by default; when enabled
	// spans flow HTTP → pgx → outbound webapi to the OTLP endpoint (Tempo).
	otel struct {
		Enabled     bool    `env:"OTEL_ENABLED" envDefault:"false"`
		Endpoint    string  `env:"OTEL_EXPORTER_OTLP_ENDPOINT" envDefault:"http://tempo:4318"`
		SampleRatio float64 `env:"OTEL_TRACE_SAMPLE_RATIO" envDefault:"1.0"`
	}

	// Swagger -.
	swagger struct {
		Enabled bool `env:"SWAGGER_ENABLED" envDefault:"false"`
	}
)

// NewConfig returns app config.
func NewConfig() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("%w: %w", errConfig, err)
	}
	if cfg.PG.PoolMax < 1 || cfg.PG.PoolMax > 100 {
		return nil, configError("PG_POOL_MAX must be between 1 and 100")
	}

	if cfg.PG.MaxConnLifetime < 0 {
		return nil, configError("PG_MAX_CONN_LIFETIME must not be negative")
	}

	if cfg.PG.MaxConnIdleTime < 0 {
		return nil, configError("PG_MAX_CONN_IDLE_TIME must not be negative")
	}

	if cfg.HTTP.BodyLimitBytes <= 0 {
		return nil, configError("HTTP_BODY_LIMIT_BYTES must be positive")
	}

	for i, origin := range cfg.CORS.AllowedOrigins {
		cfg.CORS.AllowedOrigins[i] = strings.TrimSpace(origin)
		if !validCORSOrigin(cfg.CORS.AllowedOrigins[i]) {
			return nil, configError("CORS_ALLOWED_ORIGINS entries must be * or absolute http(s) origins without a path")
		}
	}

	if cfg.Security.HSTSSeconds < 0 {
		return nil, configError("SECURITY_HSTS_SECONDS must not be negative")
	}

	if cfg.Collab.Enabled && len(cfg.Collab.ServiceToken) < minCollabServiceTokenBytes {
		return nil, configError("COLLAB_SERVICE_TOKEN must be at least 32 bytes when COLLAB_ENABLED is true")
	}
	if len(cfg.JWT.Secret) < 32 {
		return nil, configError("JWT_SECRET must be at least 32 bytes")
	}
	if cfg.JWT.TokenExpiry <= 0 || cfg.JWT.TokenExpiry > 24*time.Hour {
		return nil, configError("JWT_TOKEN_EXPIRY must be positive and no more than 24h")
	}

	if cfg.JWT.AccessTokenExpiry <= 0 || cfg.JWT.AccessTokenExpiry > 24*time.Hour {
		return nil, configError("JWT_ACCESS_TOKEN_EXPIRY must be positive and no more than 24h")
	}

	if cfg.JWT.RefreshTokenExpiry < cfg.JWT.AccessTokenExpiry || cfg.JWT.RefreshTokenExpiry > 8760*time.Hour {
		return nil, configError("JWT_REFRESH_TOKEN_EXPIRY must be at least JWT_ACCESS_TOKEN_EXPIRY and no more than 8760h")
	}

	if err := validateAuthLockout(cfg.AuthLockout); err != nil {
		return nil, err
	}

	if err := validateAuthCleanup(cfg.AuthCleanup); err != nil {
		return nil, err
	}

	if cfg.AuthAlert.Enabled && cfg.AuthAlert.Interval <= 0 {
		return nil, configError("AUTH_ALERT_INTERVAL must be positive")
	}

	if cfg.CitableAudit.Enabled && cfg.CitableAudit.Interval <= 0 {
		return nil, configError("CITABLE_AUDIT_INTERVAL must be positive")
	}

	if err := validateMFA(cfg.MFA); err != nil {
		return nil, err
	}
	cfg.Email.CloudflareAccountID = strings.TrimSpace(cfg.Email.CloudflareAccountID)
	cfg.Email.CloudflareAPIToken = strings.TrimSpace(cfg.Email.CloudflareAPIToken)
	cfg.Email.DeliveryMode = strings.ToLower(strings.TrimSpace(cfg.Email.DeliveryMode))
	cfg.Email.FromAddress = strings.TrimSpace(cfg.Email.FromAddress)
	cfg.Email.ReplyTo = strings.TrimSpace(cfg.Email.ReplyTo)
	cfg.Email.VerifyFrontendURL = strings.TrimSpace(cfg.Email.VerifyFrontendURL)
	cfg.Email.PasswordResetFrontendURL = strings.TrimSpace(cfg.Email.PasswordResetFrontendURL)
	cfg.Email.EmailChangeFrontendURL = strings.TrimSpace(cfg.Email.EmailChangeFrontendURL)
	cfg.Email.UnsubscribeFrontendURL = strings.TrimSpace(cfg.Email.UnsubscribeFrontendURL)
	cfg.Email.UnsubscribePublicURL = strings.TrimSpace(cfg.Email.UnsubscribePublicURL)
	cfg.Email.UnsubscribeTokenKeyID = strings.TrimSpace(cfg.Email.UnsubscribeTokenKeyID)
	cfg.Email.UnsubscribeTokenSecret = strings.TrimSpace(cfg.Email.UnsubscribeTokenSecret)
	cfg.Email.UnsubscribeTokenSecrets = strings.TrimSpace(cfg.Email.UnsubscribeTokenSecrets)
	cfg.Email.CloudflareWebhookSecret = strings.TrimSpace(cfg.Email.CloudflareWebhookSecret)
	cfg.Email.CloudflareZoneID = strings.TrimSpace(cfg.Email.CloudflareZoneID)
	cfg.Email.CloudflareAnalyticsAPIToken = strings.TrimSpace(cfg.Email.CloudflareAnalyticsAPIToken)
	if cfg.Email.UnsubscribeTokenKeyID == "" {
		return nil, configError("EMAIL_UNSUBSCRIBE_TOKEN_KEY_ID must not be empty")
	}
	if !validUnsubscribeTokenKeyID(cfg.Email.UnsubscribeTokenKeyID) {
		return nil, configError("EMAIL_UNSUBSCRIBE_TOKEN_KEY_ID contains unsupported characters")
	}
	if err := validateUnsubscribeTokenSecrets(
		cfg.Email.UnsubscribeTokenKeyID,
		cfg.Email.UnsubscribeTokenSecret,
		cfg.Email.UnsubscribeTokenSecrets,
	); err != nil {
		return nil, err
	}
	switch cfg.Email.DeliveryMode {
	case EmailDeliveryModeCloudflare:
		if strings.TrimSpace(cfg.Email.CloudflareAccountID) == "" {
			return nil, configError("CF_EMAIL_ACCOUNT_ID is required")
		}
		if strings.TrimSpace(cfg.Email.CloudflareAPIToken) == "" {
			return nil, configError("CF_EMAIL_API_TOKEN is required")
		}
		if !validEmailAddress(cfg.Email.FromAddress) {
			return nil, configError("EMAIL_FROM_ADDRESS must be a valid email address")
		}
	case EmailDeliveryModeLog:
		if cfg.Email.FromAddress != "" && !validEmailAddress(cfg.Email.FromAddress) {
			return nil, configError("EMAIL_FROM_ADDRESS must be a valid email address")
		}
	default:
		return nil, configError("EMAIL_DELIVERY_MODE must be cloudflare or log")
	}
	if cfg.Email.ReplyTo != "" && !validEmailAddress(cfg.Email.ReplyTo) {
		return nil, configError("EMAIL_REPLY_TO must be a valid email address")
	}
	if !validAbsoluteHTTPURL(cfg.Email.VerifyFrontendURL) {
		return nil, configError("EMAIL_VERIFY_FRONTEND_URL must be an absolute http(s) URL")
	}
	if !validAbsoluteHTTPURL(cfg.Email.PasswordResetFrontendURL) {
		return nil, configError("PASSWORD_RESET_FRONTEND_URL must be an absolute http(s) URL")
	}
	if !validAbsoluteHTTPURL(cfg.Email.EmailChangeFrontendURL) {
		return nil, configError("EMAIL_CHANGE_FRONTEND_URL must be an absolute http(s) URL")
	}
	if cfg.Email.UnsubscribeFrontendURL != "" && !validAbsoluteHTTPURL(cfg.Email.UnsubscribeFrontendURL) {
		return nil, configError("EMAIL_UNSUBSCRIBE_FRONTEND_URL must be an absolute http(s) URL")
	}
	if cfg.Email.UnsubscribePublicURL != "" && !validAbsoluteHTTPURL(cfg.Email.UnsubscribePublicURL) {
		return nil, configError("EMAIL_UNSUBSCRIBE_PUBLIC_URL must be an absolute http(s) URL")
	}
	if cfg.Email.VerificationTTL <= 0 {
		return nil, configError("EMAIL_VERIFICATION_TTL must be positive")
	}
	if cfg.Email.VerificationOTPTTL <= 0 {
		return nil, configError("EMAIL_VERIFICATION_OTP_TTL must be positive")
	}
	if cfg.Email.ResendCooldown <= 0 {
		return nil, configError("EMAIL_RESEND_COOLDOWN must be positive")
	}
	if cfg.Email.PasswordResetTTL <= 0 {
		return nil, configError("PASSWORD_RESET_TTL must be positive")
	}
	if cfg.Email.PasswordResetCooldown <= 0 {
		return nil, configError("PASSWORD_RESET_RESEND_COOLDOWN must be positive")
	}
	if cfg.Email.EmailChangeTTL <= 0 {
		return nil, configError("EMAIL_CHANGE_TTL must be positive")
	}
	if cfg.Email.EmailChangeOTPTTL <= 0 {
		return nil, configError("EMAIL_CHANGE_OTP_TTL must be positive")
	}
	if cfg.Email.EmailChangeCooldown <= 0 {
		return nil, configError("EMAIL_CHANGE_RESEND_COOLDOWN must be positive")
	}
	if cfg.Email.HTTPTimeout <= 0 {
		return nil, configError("EMAIL_HTTP_TIMEOUT must be positive")
	}

	if cfg.Email.DispatchInterval <= 0 {
		return nil, configError("EMAIL_DISPATCH_INTERVAL must be positive")
	}

	if cfg.Email.DispatchBatch <= 0 {
		return nil, configError("EMAIL_DISPATCH_BATCH must be positive")
	}
	if cfg.Email.CloudflareEventPollingInterval <= 0 {
		return nil, configError("EMAIL_CLOUDFLARE_EVENT_POLLING_INTERVAL must be positive")
	}
	if cfg.Email.CloudflareEventPollingLookback <= 0 {
		return nil, configError("EMAIL_CLOUDFLARE_EVENT_POLLING_LOOKBACK must be positive")
	}
	if cfg.Email.CloudflareEventPollingLimit <= 0 || cfg.Email.CloudflareEventPollingLimit > 1000 {
		return nil, configError("EMAIL_CLOUDFLARE_EVENT_POLLING_LIMIT must be between 1 and 1000")
	}
	if cfg.Email.CloudflareEventPollingEnabled && cfg.Email.DeliveryMode == EmailDeliveryModeCloudflare {
		if cfg.Email.CloudflareZoneID == "" {
			return nil, configError("EMAIL_CLOUDFLARE_ZONE_ID is required when event polling is enabled")
		}
		if cfg.Email.CloudflareAnalyticsAPIToken == "" {
			return nil, fmt.Errorf(
				"config error: EMAIL_CLOUDFLARE_ANALYTICS_API_TOKEN is required when event polling is enabled",
			)
		}
	}
	if cfg.AuthEmail.NotificationsEnabled &&
		cfg.AuthEmail.FailedLoginEnabled &&
		cfg.AuthEmail.FailedLoginCooldown <= 0 {
		return nil, configError("AUTH_FAILED_LOGIN_EMAIL_COOLDOWN must be positive")
	}
	if cfg.AuthRateLimit.Enabled {
		if err := validatePositiveInt("AUTH_RATE_LIMIT_LOGIN_EMAIL_MAX", cfg.AuthRateLimit.LoginEmailMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_LOGIN_EMAIL_WINDOW", cfg.AuthRateLimit.LoginEmailWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_LOGIN_IP_MAX", cfg.AuthRateLimit.LoginIPMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_LOGIN_IP_WINDOW", cfg.AuthRateLimit.LoginIPWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_REGISTER_EMAIL_MAX", cfg.AuthRateLimit.RegisterEmailMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_REGISTER_EMAIL_WINDOW", cfg.AuthRateLimit.RegisterEmailWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_REGISTER_IP_MAX", cfg.AuthRateLimit.RegisterIPMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_REGISTER_IP_WINDOW", cfg.AuthRateLimit.RegisterIPWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_FORGOT_PASSWORD_EMAIL_MAX", cfg.AuthRateLimit.ForgotPasswordEmailMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_FORGOT_PASSWORD_EMAIL_WINDOW", cfg.AuthRateLimit.ForgotPasswordEmailWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_FORGOT_PASSWORD_IP_MAX", cfg.AuthRateLimit.ForgotPasswordIPMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_FORGOT_PASSWORD_IP_WINDOW", cfg.AuthRateLimit.ForgotPasswordIPWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_VERIFY_EMAIL_OTP_EMAIL_MAX", cfg.AuthRateLimit.VerifyEmailOTPEmailMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_VERIFY_EMAIL_OTP_EMAIL_WINDOW", cfg.AuthRateLimit.VerifyEmailOTPEmailWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_VERIFY_EMAIL_OTP_IP_MAX", cfg.AuthRateLimit.VerifyEmailOTPIPMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_VERIFY_EMAIL_OTP_IP_WINDOW", cfg.AuthRateLimit.VerifyEmailOTPIPWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_RESEND_VERIFICATION_EMAIL_MAX", cfg.AuthRateLimit.ResendVerificationEmailMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_RESEND_VERIFICATION_EMAIL_WINDOW", cfg.AuthRateLimit.ResendVerificationEmailWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_RESEND_VERIFICATION_IP_MAX", cfg.AuthRateLimit.ResendVerificationIPMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_RESEND_VERIFICATION_IP_WINDOW", cfg.AuthRateLimit.ResendVerificationIPWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_RESET_PASSWORD_TOKEN_MAX", cfg.AuthRateLimit.ResetPasswordTokenMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_RESET_PASSWORD_TOKEN_WINDOW", cfg.AuthRateLimit.ResetPasswordTokenWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_RESET_PASSWORD_IP_MAX", cfg.AuthRateLimit.ResetPasswordIPMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_RESET_PASSWORD_IP_WINDOW", cfg.AuthRateLimit.ResetPasswordIPWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_CHANGE_PASSWORD_USER_MAX", cfg.AuthRateLimit.ChangePasswordUserMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_CHANGE_PASSWORD_USER_WINDOW", cfg.AuthRateLimit.ChangePasswordUserWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_CHANGE_PASSWORD_IP_MAX", cfg.AuthRateLimit.ChangePasswordIPMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_CHANGE_PASSWORD_IP_WINDOW", cfg.AuthRateLimit.ChangePasswordIPWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_CHANGE_EMAIL_USER_MAX", cfg.AuthRateLimit.ChangeEmailUserMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_CHANGE_EMAIL_USER_WINDOW", cfg.AuthRateLimit.ChangeEmailUserWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_CHANGE_EMAIL_IP_MAX", cfg.AuthRateLimit.ChangeEmailIPMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_CHANGE_EMAIL_IP_WINDOW", cfg.AuthRateLimit.ChangeEmailIPWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_CHANGE_EMAIL_TOKEN_MAX", cfg.AuthRateLimit.ChangeEmailTokenMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_CHANGE_EMAIL_TOKEN_WINDOW", cfg.AuthRateLimit.ChangeEmailTokenWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_DELETE_ACCOUNT_USER_MAX", cfg.AuthRateLimit.DeleteAccountUserMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_DELETE_ACCOUNT_USER_WINDOW", cfg.AuthRateLimit.DeleteAccountUserWindow); err != nil {
			return nil, err
		}
		if err := validatePositiveInt("AUTH_RATE_LIMIT_DELETE_ACCOUNT_IP_MAX", cfg.AuthRateLimit.DeleteAccountIPMax); err != nil {
			return nil, err
		}
		if err := validatePositiveDuration("AUTH_RATE_LIMIT_DELETE_ACCOUNT_IP_WINDOW", cfg.AuthRateLimit.DeleteAccountIPWindow); err != nil {
			return nil, err
		}

		if err := validatePositiveInt("AUTH_RATE_LIMIT_REFRESH_TOKEN_MAX", cfg.AuthRateLimit.RefreshTokenMax); err != nil {
			return nil, err
		}

		if err := validatePositiveDuration("AUTH_RATE_LIMIT_REFRESH_TOKEN_WINDOW", cfg.AuthRateLimit.RefreshTokenWindow); err != nil {
			return nil, err
		}

		if err := validatePositiveInt("AUTH_RATE_LIMIT_REFRESH_IP_MAX", cfg.AuthRateLimit.RefreshIPMax); err != nil {
			return nil, err
		}

		if err := validatePositiveDuration("AUTH_RATE_LIMIT_REFRESH_IP_WINDOW", cfg.AuthRateLimit.RefreshIPWindow); err != nil {
			return nil, err
		}

		if err := validatePositiveInt("AUTH_RATE_LIMIT_VERIFY_EMAIL_TOKEN_MAX", cfg.AuthRateLimit.VerifyEmailTokenMax); err != nil {
			return nil, err
		}

		if err := validatePositiveDuration("AUTH_RATE_LIMIT_VERIFY_EMAIL_TOKEN_WINDOW", cfg.AuthRateLimit.VerifyEmailTokenWindow); err != nil {
			return nil, err
		}
	}
	if cfg.RAG.LLMTimeout <= 0 {
		return nil, configError("RAG_LLM_TIMEOUT must be positive")
	}
	if cfg.RAG.LLMMaxTokens < 1 {
		return nil, configError("RAG_LLM_MAX_TOKENS must be positive")
	}
	if cfg.RAG.MaxContextPages < 1 {
		return nil, configError("RAG_MAX_CONTEXT_PAGES must be positive")
	}
	if cfg.RAG.TreeFullMaxNodes < 1 {
		return nil, configError("RAG_TREE_FULL_MAX_NODES must be positive")
	}
	if cfg.RAG.TreeBlockMaxNodes < 1 {
		return nil, configError("RAG_TREE_BLOCK_MAX_NODES must be positive")
	}
	if cfg.RAG.TreeBeamSize < 1 {
		return nil, configError("RAG_TREE_BEAM_SIZE must be positive")
	}
	if cfg.RAG.TreeMaxTurns < 1 {
		return nil, configError("RAG_TREE_MAX_TURNS must be positive")
	}
	if cfg.RAG.TreeMaxBlocksPerTurn < 1 {
		return nil, configError("RAG_TREE_MAX_BLOCKS_PER_TURN must be positive")
	}
	if cfg.OneSignal.Enabled {
		if strings.TrimSpace(cfg.OneSignal.AppID) == "" {
			return nil, configError("ONESIGNAL_APP_ID is required when ONESIGNAL_ENABLED is true")
		}
		if strings.TrimSpace(cfg.OneSignal.RESTAPIKey) == "" {
			return nil, configError("ONESIGNAL_REST_API_KEY is required when ONESIGNAL_ENABLED is true")
		}
		if cfg.OneSignal.HTTPTimeout <= 0 {
			return nil, configError("ONESIGNAL_HTTP_TIMEOUT must be positive")
		}
		if cfg.OneSignal.ReminderInterval <= 0 {
			return nil, configError("ONESIGNAL_REMINDER_INTERVAL must be positive")
		}
	}

	return cfg, nil
}

func validEmailAddress(value string) bool {
	value = strings.TrimSpace(value)
	address, err := mail.ParseAddress(value)
	if err != nil {
		return false
	}

	return address.Name == "" && address.Address == value
}

func validAbsoluteHTTPURL(value string) bool {
	parsedURL, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}

	return parsedURL.IsAbs() && (parsedURL.Scheme == "http" || parsedURL.Scheme == "https") && parsedURL.Host != ""
}

// validCORSOrigin accepts the wildcard or a bare scheme://host[:port] origin
// (no path, query, fragment, or userinfo).
func validCORSOrigin(value string) bool {
	if value == "*" {
		return true
	}

	parsedURL, err := url.Parse(value)
	if err != nil {
		return false
	}

	return parsedURL.IsAbs() &&
		(parsedURL.Scheme == "http" || parsedURL.Scheme == "https") &&
		parsedURL.Host != "" &&
		parsedURL.Path == "" &&
		parsedURL.RawQuery == "" &&
		parsedURL.Fragment == "" &&
		parsedURL.User == nil
}

func validUnsubscribeTokenKeyID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' ||
			r == '_' {
			continue
		}

		return false
	}

	return true
}

func validateUnsubscribeTokenSecrets(keyID, currentSecret, rawSecrets string) error {
	currentSecret = strings.TrimSpace(currentSecret)
	rawSecrets = strings.TrimSpace(rawSecrets)
	if rawSecrets == "" {
		return nil
	}

	secrets := map[string]string{}
	if err := json.Unmarshal([]byte(rawSecrets), &secrets); err != nil {
		return configError("EMAIL_UNSUBSCRIBE_TOKEN_SECRETS must be a JSON object")
	}
	if len(secrets) == 0 {
		return configError("EMAIL_UNSUBSCRIBE_TOKEN_SECRETS must not be empty")
	}
	for key, secret := range secrets {
		if !validUnsubscribeTokenKeyID(key) {
			return configError("EMAIL_UNSUBSCRIBE_TOKEN_SECRETS contains invalid key id")
		}
		if strings.TrimSpace(secret) == "" {
			return configError("EMAIL_UNSUBSCRIBE_TOKEN_SECRETS contains empty secret")
		}
	}
	if strings.TrimSpace(secrets[keyID]) == "" && currentSecret == "" {
		return configError("EMAIL_UNSUBSCRIBE_TOKEN_SECRETS must include EMAIL_UNSUBSCRIBE_TOKEN_KEY_ID or EMAIL_UNSUBSCRIBE_TOKEN_SECRET must be set")
	}

	return nil
}

func validatePositiveInt(name string, value int) error {
	if value <= 0 {
		return configErrorf("%s must be positive", name)
	}

	return nil
}

func validatePositiveDuration(name string, value time.Duration) error {
	if value <= 0 {
		return configErrorf("%s must be positive", name)
	}

	return nil
}
