package config

import (
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
)

type (
	// Config -.
	Config struct {
		App           app
		HTTP          http
		Log           log
		PG            pg
		JWT           jwt
		Email         email
		AuthRateLimit authRateLimit
		AuthEmail     authEmail
		RAG           rag
		Metrics       metrics
		Swagger       swagger
	}

	// App -.
	app struct {
		Name    string `env:"APP_NAME,required"`
		Version string `env:"APP_VERSION,required"`
	}

	// HTTP -.
	http struct {
		Port           string `env:"HTTP_PORT,required"`
		UsePreforkMode bool   `env:"HTTP_USE_PREFORK_MODE" envDefault:"false"`
	}

	// Log -.
	log struct {
		Level string `env:"LOG_LEVEL,required"`
	}

	// PG -.
	pg struct {
		PoolMax int    `env:"PG_POOL_MAX,required"`
		URL     string `env:"PG_URL,required"`
	}

	// JWT -.
	jwt struct {
		Secret      string        `env:"JWT_SECRET,required"`
		TokenExpiry time.Duration `env:"JWT_TOKEN_EXPIRY" envDefault:"24h"`
		Issuer      string        `env:"JWT_ISSUER" envDefault:"surau-backend"`
		Audience    string        `env:"JWT_AUDIENCE" envDefault:"surau-api"`
	}

	// Email -.
	email struct {
		DeliveryMode             string        `env:"EMAIL_DELIVERY_MODE" envDefault:"cloudflare"`
		CloudflareAccountID      string        `env:"CF_EMAIL_ACCOUNT_ID"`
		CloudflareAPIToken       string        `env:"CF_EMAIL_API_TOKEN"`
		FromAddress              string        `env:"EMAIL_FROM_ADDRESS"`
		FromName                 string        `env:"EMAIL_FROM_NAME" envDefault:"Surau"`
		ReplyTo                  string        `env:"EMAIL_REPLY_TO"`
		VerifyFrontendURL        string        `env:"EMAIL_VERIFY_FRONTEND_URL"`
		VerificationTTL          time.Duration `env:"EMAIL_VERIFICATION_TTL" envDefault:"24h"`
		ResendCooldown           time.Duration `env:"EMAIL_RESEND_COOLDOWN" envDefault:"1m"`
		PasswordResetFrontendURL string        `env:"PASSWORD_RESET_FRONTEND_URL"`
		PasswordResetTTL         time.Duration `env:"PASSWORD_RESET_TTL" envDefault:"1h"`
		PasswordResetCooldown    time.Duration `env:"PASSWORD_RESET_RESEND_COOLDOWN" envDefault:"1m"`
		EmailChangeFrontendURL   string        `env:"EMAIL_CHANGE_FRONTEND_URL"`
		EmailChangeTTL           time.Duration `env:"EMAIL_CHANGE_TTL" envDefault:"24h"`
		EmailChangeCooldown      time.Duration `env:"EMAIL_CHANGE_RESEND_COOLDOWN" envDefault:"1m"`
		UnsubscribeFrontendURL   string        `env:"EMAIL_UNSUBSCRIBE_FRONTEND_URL"`
		HTTPTimeout              time.Duration `env:"EMAIL_HTTP_TIMEOUT" envDefault:"10s"`
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

	// Swagger -.
	swagger struct {
		Enabled bool `env:"SWAGGER_ENABLED" envDefault:"false"`
	}
)

// NewConfig returns app config.
func NewConfig() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}
	if cfg.PG.PoolMax < 1 || cfg.PG.PoolMax > 100 {
		return nil, fmt.Errorf("config error: PG_POOL_MAX must be between 1 and 100")
	}
	if len(cfg.JWT.Secret) < 32 {
		return nil, fmt.Errorf("config error: JWT_SECRET must be at least 32 bytes")
	}
	if cfg.JWT.TokenExpiry <= 0 || cfg.JWT.TokenExpiry > 24*time.Hour {
		return nil, fmt.Errorf("config error: JWT_TOKEN_EXPIRY must be positive and no more than 24h")
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
	switch cfg.Email.DeliveryMode {
	case EmailDeliveryModeCloudflare:
		if strings.TrimSpace(cfg.Email.CloudflareAccountID) == "" {
			return nil, fmt.Errorf("config error: CF_EMAIL_ACCOUNT_ID is required")
		}
		if strings.TrimSpace(cfg.Email.CloudflareAPIToken) == "" {
			return nil, fmt.Errorf("config error: CF_EMAIL_API_TOKEN is required")
		}
		if !validEmailAddress(cfg.Email.FromAddress) {
			return nil, fmt.Errorf("config error: EMAIL_FROM_ADDRESS must be a valid email address")
		}
	case EmailDeliveryModeLog:
		if cfg.Email.FromAddress != "" && !validEmailAddress(cfg.Email.FromAddress) {
			return nil, fmt.Errorf("config error: EMAIL_FROM_ADDRESS must be a valid email address")
		}
	default:
		return nil, fmt.Errorf("config error: EMAIL_DELIVERY_MODE must be cloudflare or log")
	}
	if cfg.Email.ReplyTo != "" && !validEmailAddress(cfg.Email.ReplyTo) {
		return nil, fmt.Errorf("config error: EMAIL_REPLY_TO must be a valid email address")
	}
	if !validAbsoluteHTTPURL(cfg.Email.VerifyFrontendURL) {
		return nil, fmt.Errorf("config error: EMAIL_VERIFY_FRONTEND_URL must be an absolute http(s) URL")
	}
	if !validAbsoluteHTTPURL(cfg.Email.PasswordResetFrontendURL) {
		return nil, fmt.Errorf("config error: PASSWORD_RESET_FRONTEND_URL must be an absolute http(s) URL")
	}
	if !validAbsoluteHTTPURL(cfg.Email.EmailChangeFrontendURL) {
		return nil, fmt.Errorf("config error: EMAIL_CHANGE_FRONTEND_URL must be an absolute http(s) URL")
	}
	if cfg.Email.UnsubscribeFrontendURL != "" && !validAbsoluteHTTPURL(cfg.Email.UnsubscribeFrontendURL) {
		return nil, fmt.Errorf("config error: EMAIL_UNSUBSCRIBE_FRONTEND_URL must be an absolute http(s) URL")
	}
	if cfg.Email.VerificationTTL <= 0 {
		return nil, fmt.Errorf("config error: EMAIL_VERIFICATION_TTL must be positive")
	}
	if cfg.Email.ResendCooldown <= 0 {
		return nil, fmt.Errorf("config error: EMAIL_RESEND_COOLDOWN must be positive")
	}
	if cfg.Email.PasswordResetTTL <= 0 {
		return nil, fmt.Errorf("config error: PASSWORD_RESET_TTL must be positive")
	}
	if cfg.Email.PasswordResetCooldown <= 0 {
		return nil, fmt.Errorf("config error: PASSWORD_RESET_RESEND_COOLDOWN must be positive")
	}
	if cfg.Email.EmailChangeTTL <= 0 {
		return nil, fmt.Errorf("config error: EMAIL_CHANGE_TTL must be positive")
	}
	if cfg.Email.EmailChangeCooldown <= 0 {
		return nil, fmt.Errorf("config error: EMAIL_CHANGE_RESEND_COOLDOWN must be positive")
	}
	if cfg.Email.HTTPTimeout <= 0 {
		return nil, fmt.Errorf("config error: EMAIL_HTTP_TIMEOUT must be positive")
	}
	if cfg.AuthEmail.NotificationsEnabled &&
		cfg.AuthEmail.FailedLoginEnabled &&
		cfg.AuthEmail.FailedLoginCooldown <= 0 {
		return nil, fmt.Errorf("config error: AUTH_FAILED_LOGIN_EMAIL_COOLDOWN must be positive")
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
	}
	if cfg.RAG.LLMTimeout <= 0 {
		return nil, fmt.Errorf("config error: RAG_LLM_TIMEOUT must be positive")
	}
	if cfg.RAG.LLMMaxTokens < 1 {
		return nil, fmt.Errorf("config error: RAG_LLM_MAX_TOKENS must be positive")
	}
	if cfg.RAG.MaxContextPages < 1 {
		return nil, fmt.Errorf("config error: RAG_MAX_CONTEXT_PAGES must be positive")
	}
	if cfg.RAG.TreeFullMaxNodes < 1 {
		return nil, fmt.Errorf("config error: RAG_TREE_FULL_MAX_NODES must be positive")
	}
	if cfg.RAG.TreeBlockMaxNodes < 1 {
		return nil, fmt.Errorf("config error: RAG_TREE_BLOCK_MAX_NODES must be positive")
	}
	if cfg.RAG.TreeBeamSize < 1 {
		return nil, fmt.Errorf("config error: RAG_TREE_BEAM_SIZE must be positive")
	}
	if cfg.RAG.TreeMaxTurns < 1 {
		return nil, fmt.Errorf("config error: RAG_TREE_MAX_TURNS must be positive")
	}
	if cfg.RAG.TreeMaxBlocksPerTurn < 1 {
		return nil, fmt.Errorf("config error: RAG_TREE_MAX_BLOCKS_PER_TURN must be positive")
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

func validatePositiveInt(name string, value int) error {
	if value <= 0 {
		return fmt.Errorf("config error: %s must be positive", name)
	}

	return nil
}

func validatePositiveDuration(name string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("config error: %s must be positive", name)
	}

	return nil
}
