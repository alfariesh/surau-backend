package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/internal/usecase/authmeta"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	minUsernameRunes        = 3
	maxUsernameRunes        = 255
	minPasswordBytes        = 8
	maxPasswordBytes        = 72
	verificationTokenBytes  = 32
	passwordResetTokenBytes = 32
	maxResetTokenInputBytes = 512
	maxEmailUserAgentRunes  = 160
)

const (
	authEventRegister           = "register"
	authEventLogin              = "login"
	authEventVerifyEmail        = "verify_email"
	authEventResendVerification = "resend_verification"
	authEventForgotPassword     = "forgot_password"
	authEventResetPassword      = "reset_password"
	authEventChangePassword     = "change_password"
	authEventRoleChange         = "role_change"
	authEmailPasswordChanged    = "password_changed"
	authEmailEmailVerified      = "email_verified"
	authEmailNewLogin           = "new_login"
	authEmailFailedLogin        = "failed_login"
	authEmailRoleChanged        = "role_changed"
	authAuditStatusSuccess      = "success"
	authAuditStatusFailure      = "failure"
	rateLimitKeyTypeEmail       = "email"
	rateLimitKeyTypeIP          = "ip"
	rateLimitKeyTypeToken       = "token"
	rateLimitKeyTypeUser        = "user"
)

// UseCase -.
type UseCase struct {
	repo                     repo.UserRepo
	jwt                      *jwt.Manager
	emailSender              repo.EmailSender
	verifyFrontendURL        string
	verificationTTL          time.Duration
	resendCooldown           time.Duration
	passwordResetFrontendURL string
	passwordResetTTL         time.Duration
	passwordResetCooldown    time.Duration
	rateLimiter              repo.AuthRateLimitRepo
	auditLogger              repo.AuthAuditRepo
	rateLimit                RateLimitOptions
	emailNotifications       EmailNotificationOptions
}

// RateLimitRule configures one auth rate-limit dimension.
type RateLimitRule struct {
	Max    int
	Window time.Duration
}

// RateLimitOptions configures DB-backed auth rate limits.
type RateLimitOptions struct {
	LoginEmail              RateLimitRule
	LoginIP                 RateLimitRule
	RegisterEmail           RateLimitRule
	RegisterIP              RateLimitRule
	ForgotPasswordEmail     RateLimitRule
	ForgotPasswordIP        RateLimitRule
	ResendVerificationEmail RateLimitRule
	ResendVerificationIP    RateLimitRule
	ResetPasswordToken      RateLimitRule
	ResetPasswordIP         RateLimitRule
	ChangePasswordUser      RateLimitRule
	ChangePasswordIP        RateLimitRule
}

// EmailNotificationOptions configures best-effort auth security emails.
type EmailNotificationOptions struct {
	Enabled                bool
	NewLoginEnabled        bool
	FailedLoginEnabled     bool
	PasswordChangedEnabled bool
	EmailVerifiedEnabled   bool
	RoleChangedEnabled     bool
	FailedLoginCooldown    time.Duration
}

// Options configures user auth email verification.
type Options struct {
	VerifyFrontendURL        string
	VerificationTTL          time.Duration
	ResendCooldown           time.Duration
	PasswordResetFrontendURL string
	PasswordResetTTL         time.Duration
	PasswordResetCooldown    time.Duration
	RateLimiter              repo.AuthRateLimitRepo
	AuditLogger              repo.AuthAuditRepo
	RateLimit                RateLimitOptions
	EmailNotifications       EmailNotificationOptions
}

// New -.
func New(r repo.UserRepo, j *jwt.Manager, emailSender repo.EmailSender, opts Options) *UseCase {
	verificationTTL := opts.VerificationTTL
	if verificationTTL <= 0 {
		verificationTTL = 24 * time.Hour
	}
	resendCooldown := opts.ResendCooldown
	if resendCooldown <= 0 {
		resendCooldown = time.Minute
	}
	passwordResetTTL := opts.PasswordResetTTL
	if passwordResetTTL <= 0 {
		passwordResetTTL = time.Hour
	}
	passwordResetCooldown := opts.PasswordResetCooldown
	if passwordResetCooldown <= 0 {
		passwordResetCooldown = time.Minute
	}

	return &UseCase{
		repo:                     r,
		jwt:                      j,
		emailSender:              emailSender,
		verifyFrontendURL:        opts.VerifyFrontendURL,
		verificationTTL:          verificationTTL,
		resendCooldown:           resendCooldown,
		passwordResetFrontendURL: opts.PasswordResetFrontendURL,
		passwordResetTTL:         passwordResetTTL,
		passwordResetCooldown:    passwordResetCooldown,
		rateLimiter:              opts.RateLimiter,
		auditLogger:              opts.AuditLogger,
		rateLimit:                normalizeRateLimitOptions(opts.RateLimit),
		emailNotifications:       normalizeEmailNotificationOptions(opts.EmailNotifications),
	}
}

// Register -.
func (uc *UseCase) Register(ctx context.Context, username, email, password string) (created entity.User, err error) {
	auditEmail := strings.TrimSpace(email)
	auditUserID := ""
	defer func() {
		uc.auditAuth(ctx, authEventRegister, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), nil)
	}()

	username, email, err = validateRegisterInput(username, email, password)
	if err != nil {
		return entity.User{}, err
	}
	auditEmail = email

	if err = uc.enforceAuthRateLimit(ctx, authEventRegister, []rateLimitCheck{
		{keyType: rateLimitKeyTypeEmail, value: email, rule: uc.rateLimit.RegisterEmail},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.RegisterIP},
	}); err != nil {
		return entity.User{}, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return entity.User{}, fmt.Errorf("UserUseCase - Register - bcrypt.GenerateFromPassword: %w", err)
	}

	now := time.Now().UTC()

	user := entity.User{
		ID:            uuid.New().String(),
		Username:      username,
		Email:         email,
		Role:          entity.UserRoleUser,
		PasswordHash:  string(hash),
		EmailVerified: false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	auditUserID = user.ID

	rawToken, verificationToken, err := uc.newVerificationToken(user.ID, now)
	if err != nil {
		return entity.User{}, err
	}

	err = uc.repo.StoreWithVerificationToken(ctx, &user, &verificationToken)
	if err != nil {
		return entity.User{}, fmt.Errorf("UserUseCase - Register - uc.repo.StoreWithVerificationToken: %w", err)
	}

	if err = uc.sendVerificationEmail(ctx, user, rawToken); err != nil {
		_ = uc.repo.RevokeUnusedVerificationTokens(ctx, user.ID)

		return entity.User{}, err
	}

	return user, nil
}

// SetRoleByEmail updates a user role.
func (uc *UseCase) SetRoleByEmail(ctx context.Context, email, role string) (updated entity.User, err error) {
	auditEmail := strings.TrimSpace(email)
	auditUserID := ""
	defer func() {
		uc.auditAuth(ctx, authEventRoleChange, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), map[string]string{
			"role": role,
		})
	}()

	switch role {
	case entity.UserRoleUser, entity.UserRoleAdmin:
	default:
		return entity.User{}, entity.ErrInvalidRole
	}

	updated, err = uc.repo.SetRoleByEmail(ctx, email, role)
	if err == nil {
		auditUserID = updated.ID
		auditEmail = updated.Email
		uc.notifyRoleChanged(ctx, updated)
	}

	return updated, err
}

// Login -.
func (uc *UseCase) Login(ctx context.Context, email, password string) (token string, err error) {
	auditEmail := strings.TrimSpace(email)
	auditUserID := ""
	defer func() {
		uc.auditAuth(ctx, authEventLogin, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), nil)
	}()

	email, err = validateLoginInput(email, password)
	if err != nil {
		return "", err
	}
	auditEmail = email

	if err = uc.enforceLoginRateLimit(ctx, email); err != nil {
		return "", err
	}

	user, err := uc.repo.GetByEmail(ctx, email)
	if err != nil {
		return "", entity.ErrInvalidCredentials
	}
	auditUserID = user.ID

	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		return "", entity.ErrInvalidCredentials
	}
	if !user.EmailVerified {
		return "", entity.ErrEmailNotVerified
	}

	token, err = uc.jwt.GenerateToken(user.ID, user.TokenVersion)
	if err != nil {
		return "", fmt.Errorf("UserUseCase - Login - uc.jwt.GenerateToken: %w", err)
	}
	uc.notifyNewLogin(ctx, user)

	return token, nil
}

// GetUser -.
func (uc *UseCase) GetUser(ctx context.Context, userID string) (entity.User, error) {
	user, err := uc.repo.GetByID(ctx, userID)
	if err != nil {
		return entity.User{}, fmt.Errorf("UserUseCase - GetUser - uc.repo.GetByID: %w", err)
	}

	return user, nil
}

// VerifyEmail verifies a one-time email verification token.
func (uc *UseCase) VerifyEmail(ctx context.Context, token string) (err error) {
	auditUserID := ""
	auditEmail := ""
	defer func() {
		uc.auditAuth(ctx, authEventVerifyEmail, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), nil)
	}()

	tokenHash, err := hashVerificationToken(token)
	if err != nil {
		return err
	}

	storedToken, err := uc.repo.GetVerificationTokenByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, entity.ErrVerificationTokenNotFound) {
			return entity.ErrInvalidVerificationToken
		}

		return fmt.Errorf("UserUseCase - VerifyEmail - uc.repo.GetVerificationTokenByHash: %w", err)
	}
	if storedToken.UsedAt != nil || !time.Now().UTC().Before(storedToken.ExpiresAt) {
		return entity.ErrInvalidVerificationToken
	}

	verifiedUser, err := uc.repo.VerifyEmailWithToken(ctx, storedToken.ID, storedToken.UserID)
	if err != nil {
		if errors.Is(err, entity.ErrInvalidVerificationToken) {
			return err
		}

		return fmt.Errorf("UserUseCase - VerifyEmail - uc.repo.VerifyEmailWithToken: %w", err)
	}
	auditUserID = verifiedUser.ID
	auditEmail = verifiedUser.Email
	uc.notifyEmailVerified(ctx, verifiedUser)

	return nil
}

// ResendEmailVerification sends a new verification email for an existing unverified user.
func (uc *UseCase) ResendEmailVerification(ctx context.Context, email string) (err error) {
	auditEmail := strings.TrimSpace(email)
	auditUserID := ""
	defer func() {
		uc.auditAuth(ctx, authEventResendVerification, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), nil)
	}()

	email, err = validateEmailInput(email)
	if err != nil {
		return err
	}
	auditEmail = email

	if err = uc.enforceAuthRateLimit(ctx, authEventResendVerification, []rateLimitCheck{
		{keyType: rateLimitKeyTypeEmail, value: email, rule: uc.rateLimit.ResendVerificationEmail},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.ResendVerificationIP},
	}); err != nil {
		return err
	}

	user, err := uc.repo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return nil
		}

		return fmt.Errorf("UserUseCase - ResendEmailVerification - uc.repo.GetByEmail: %w", err)
	}
	auditUserID = user.ID
	if user.EmailVerified {
		return nil
	}

	now := time.Now().UTC()
	latestToken, err := uc.repo.GetLatestUnusedVerificationToken(ctx, user.ID)
	if err != nil && !errors.Is(err, entity.ErrVerificationTokenNotFound) {
		return fmt.Errorf("UserUseCase - ResendEmailVerification - uc.repo.GetLatestUnusedVerificationToken: %w", err)
	}
	if err == nil && now.Before(latestToken.SentAt.Add(uc.resendCooldown)) {
		return entity.ErrVerificationRateLimited
	}

	rawToken, verificationToken, err := uc.newVerificationToken(user.ID, now)
	if err != nil {
		return err
	}
	if err = uc.repo.ReplaceVerificationToken(ctx, &verificationToken); err != nil {
		return fmt.Errorf("UserUseCase - ResendEmailVerification - uc.repo.ReplaceVerificationToken: %w", err)
	}
	if err = uc.sendVerificationEmail(ctx, user, rawToken); err != nil {
		_ = uc.repo.RevokeUnusedVerificationTokens(ctx, user.ID)

		return err
	}

	return nil
}

// ForgotPassword sends a password reset email for an existing user.
func (uc *UseCase) ForgotPassword(ctx context.Context, email string) (err error) {
	auditEmail := strings.TrimSpace(email)
	auditUserID := ""
	defer func() {
		uc.auditAuth(ctx, authEventForgotPassword, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), nil)
	}()

	email, err = validateEmailInput(email)
	if err != nil {
		return err
	}
	auditEmail = email

	if err = uc.enforceAuthRateLimit(ctx, authEventForgotPassword, []rateLimitCheck{
		{keyType: rateLimitKeyTypeEmail, value: email, rule: uc.rateLimit.ForgotPasswordEmail},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.ForgotPasswordIP},
	}); err != nil {
		return err
	}

	user, err := uc.repo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return nil
		}

		return fmt.Errorf("UserUseCase - ForgotPassword - uc.repo.GetByEmail: %w", err)
	}
	auditUserID = user.ID

	now := time.Now().UTC()
	latestToken, err := uc.repo.GetLatestUnusedPasswordResetToken(ctx, user.ID)
	if err != nil && !errors.Is(err, entity.ErrPasswordResetTokenNotFound) {
		return fmt.Errorf("UserUseCase - ForgotPassword - uc.repo.GetLatestUnusedPasswordResetToken: %w", err)
	}
	if err == nil && now.Before(latestToken.SentAt.Add(uc.passwordResetCooldown)) {
		return entity.ErrPasswordResetRateLimited
	}

	rawToken, resetToken, err := uc.newPasswordResetToken(user.ID, now)
	if err != nil {
		return err
	}
	if err = uc.repo.ReplacePasswordResetToken(ctx, &resetToken); err != nil {
		return fmt.Errorf("UserUseCase - ForgotPassword - uc.repo.ReplacePasswordResetToken: %w", err)
	}
	if err = uc.sendPasswordResetEmail(ctx, user, rawToken); err != nil {
		_ = uc.repo.RevokeUnusedPasswordResetTokens(ctx, user.ID)

		return err
	}

	return nil
}

// ResetPassword updates the user's password using a one-time reset token.
func (uc *UseCase) ResetPassword(ctx context.Context, token, password string) (err error) {
	auditUserID := ""
	auditEmail := ""
	defer func() {
		uc.auditAuth(ctx, authEventResetPassword, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), nil)
	}()

	if !validPassword(password) {
		return entity.ErrInvalidAuthInput
	}

	token = strings.TrimSpace(token)
	if len(token) > maxResetTokenInputBytes {
		return entity.ErrInvalidPasswordResetToken
	}

	if err = uc.enforceAuthRateLimit(ctx, authEventResetPassword, []rateLimitCheck{
		{keyType: rateLimitKeyTypeToken, value: token, rule: uc.rateLimit.ResetPasswordToken},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.ResetPasswordIP},
	}); err != nil {
		return err
	}

	tokenHash, err := hashPasswordResetToken(token)
	if err != nil {
		return err
	}

	storedToken, err := uc.repo.GetPasswordResetTokenByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, entity.ErrPasswordResetTokenNotFound) {
			return entity.ErrInvalidPasswordResetToken
		}

		return fmt.Errorf("UserUseCase - ResetPassword - uc.repo.GetPasswordResetTokenByHash: %w", err)
	}
	if storedToken.UsedAt != nil || !time.Now().UTC().Before(storedToken.ExpiresAt) {
		return entity.ErrInvalidPasswordResetToken
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("UserUseCase - ResetPassword - bcrypt.GenerateFromPassword: %w", err)
	}

	resetUser, err := uc.repo.ResetPasswordWithToken(ctx, storedToken.ID, storedToken.UserID, string(hash))
	if err != nil {
		if errors.Is(err, entity.ErrInvalidPasswordResetToken) {
			return err
		}

		return fmt.Errorf("UserUseCase - ResetPassword - uc.repo.ResetPasswordWithToken: %w", err)
	}
	auditUserID = resetUser.ID
	auditEmail = resetUser.Email
	uc.notifyPasswordChanged(ctx, resetUser)

	return nil
}

// ChangePassword updates the current user's password after verifying the current password.
func (uc *UseCase) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (err error) {
	auditUserID := strings.TrimSpace(userID)
	auditEmail := ""
	defer func() {
		uc.auditAuth(ctx, authEventChangePassword, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), nil)
	}()

	if strings.TrimSpace(userID) == "" || !validPassword(currentPassword) || !validPassword(newPassword) {
		return entity.ErrInvalidAuthInput
	}

	if err = uc.enforceAuthRateLimit(ctx, authEventChangePassword, []rateLimitCheck{
		{keyType: rateLimitKeyTypeUser, value: userID, rule: uc.rateLimit.ChangePasswordUser},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.ChangePasswordIP},
	}); err != nil {
		return err
	}

	user, err := uc.repo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return entity.ErrInvalidCredentials
		}

		return fmt.Errorf("UserUseCase - ChangePassword - uc.repo.GetByID: %w", err)
	}
	auditUserID = user.ID
	auditEmail = user.Email

	if err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		return entity.ErrInvalidCredentials
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("UserUseCase - ChangePassword - bcrypt.GenerateFromPassword: %w", err)
	}

	changedUser, err := uc.repo.ChangePassword(ctx, user.ID, string(hash))
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return entity.ErrInvalidCredentials
		}

		return fmt.Errorf("UserUseCase - ChangePassword - uc.repo.ChangePassword: %w", err)
	}
	uc.notifyPasswordChanged(ctx, changedUser)

	return nil
}

func validateRegisterInput(username, email, password string) (string, string, error) {
	username = strings.TrimSpace(username)
	email = strings.TrimSpace(email)

	usernameLen := utf8.RuneCountInString(username)
	if usernameLen < minUsernameRunes || usernameLen > maxUsernameRunes {
		return "", "", entity.ErrInvalidAuthInput
	}
	if !validEmail(email) {
		return "", "", entity.ErrInvalidAuthInput
	}
	if !validPassword(password) {
		return "", "", entity.ErrInvalidAuthInput
	}

	return username, email, nil
}

func validateLoginInput(email, password string) (string, error) {
	email, err := validateEmailInput(email)
	if err != nil {
		return "", err
	}
	if !validPassword(password) {
		return "", entity.ErrInvalidAuthInput
	}

	return email, nil
}

func validateEmailInput(email string) (string, error) {
	email = strings.TrimSpace(email)
	if !validEmail(email) {
		return "", entity.ErrInvalidAuthInput
	}

	return email, nil
}

func validEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	if err != nil {
		return false
	}

	return address.Name == "" && address.Address == email
}

func validPassword(password string) bool {
	passwordLen := len(password)

	return passwordLen >= minPasswordBytes && passwordLen <= maxPasswordBytes
}

type rateLimitCheck struct {
	keyType string
	value   string
	rule    RateLimitRule
}

func normalizeRateLimitOptions(opts RateLimitOptions) RateLimitOptions {
	defaults := RateLimitOptions{
		LoginEmail:              RateLimitRule{Max: 5, Window: 5 * time.Minute},
		LoginIP:                 RateLimitRule{Max: 30, Window: 5 * time.Minute},
		RegisterEmail:           RateLimitRule{Max: 3, Window: time.Hour},
		RegisterIP:              RateLimitRule{Max: 10, Window: time.Hour},
		ForgotPasswordEmail:     RateLimitRule{Max: 3, Window: time.Hour},
		ForgotPasswordIP:        RateLimitRule{Max: 20, Window: time.Hour},
		ResendVerificationEmail: RateLimitRule{Max: 3, Window: time.Hour},
		ResendVerificationIP:    RateLimitRule{Max: 20, Window: time.Hour},
		ResetPasswordToken:      RateLimitRule{Max: 5, Window: 15 * time.Minute},
		ResetPasswordIP:         RateLimitRule{Max: 30, Window: 15 * time.Minute},
		ChangePasswordUser:      RateLimitRule{Max: 5, Window: 5 * time.Minute},
		ChangePasswordIP:        RateLimitRule{Max: 30, Window: 5 * time.Minute},
	}

	opts.LoginEmail = withDefaultRule(opts.LoginEmail, defaults.LoginEmail)
	opts.LoginIP = withDefaultRule(opts.LoginIP, defaults.LoginIP)
	opts.RegisterEmail = withDefaultRule(opts.RegisterEmail, defaults.RegisterEmail)
	opts.RegisterIP = withDefaultRule(opts.RegisterIP, defaults.RegisterIP)
	opts.ForgotPasswordEmail = withDefaultRule(opts.ForgotPasswordEmail, defaults.ForgotPasswordEmail)
	opts.ForgotPasswordIP = withDefaultRule(opts.ForgotPasswordIP, defaults.ForgotPasswordIP)
	opts.ResendVerificationEmail = withDefaultRule(opts.ResendVerificationEmail, defaults.ResendVerificationEmail)
	opts.ResendVerificationIP = withDefaultRule(opts.ResendVerificationIP, defaults.ResendVerificationIP)
	opts.ResetPasswordToken = withDefaultRule(opts.ResetPasswordToken, defaults.ResetPasswordToken)
	opts.ResetPasswordIP = withDefaultRule(opts.ResetPasswordIP, defaults.ResetPasswordIP)
	opts.ChangePasswordUser = withDefaultRule(opts.ChangePasswordUser, defaults.ChangePasswordUser)
	opts.ChangePasswordIP = withDefaultRule(opts.ChangePasswordIP, defaults.ChangePasswordIP)

	return opts
}

func normalizeEmailNotificationOptions(opts EmailNotificationOptions) EmailNotificationOptions {
	if opts.FailedLoginCooldown <= 0 {
		opts.FailedLoginCooldown = 24 * time.Hour
	}

	return opts
}

func withDefaultRule(rule, fallback RateLimitRule) RateLimitRule {
	if rule.Max <= 0 {
		rule.Max = fallback.Max
	}
	if rule.Window <= 0 {
		rule.Window = fallback.Window
	}

	return rule
}

func (uc *UseCase) enforceAuthRateLimit(ctx context.Context, action string, checks []rateLimitCheck) error {
	if uc.rateLimiter == nil {
		return nil
	}

	now := time.Now().UTC()
	for _, check := range checks {
		value := strings.TrimSpace(check.value)
		if value == "" || check.rule.Max <= 0 || check.rule.Window <= 0 {
			continue
		}

		windowStart := now.Truncate(check.rule.Window)
		result, err := uc.rateLimiter.IncrementAuthRateLimit(ctx, entity.AuthRateLimit{
			Action:        action,
			KeyHash:       authRateLimitKeyHash(action, check.keyType, value),
			WindowStart:   windowStart,
			WindowSeconds: int64(check.rule.Window / time.Second),
			MaxAttempts:   check.rule.Max,
			ExpiresAt:     windowStart.Add(check.rule.Window),
		})
		if err != nil {
			return fmt.Errorf("UserUseCase - enforceAuthRateLimit - IncrementAuthRateLimit: %w", err)
		}
		if !result.Allowed {
			return entity.ErrAuthRateLimited
		}
	}

	return nil
}

func (uc *UseCase) enforceLoginRateLimit(ctx context.Context, email string) error {
	if uc.rateLimiter == nil {
		return nil
	}

	meta := authmeta.From(ctx)
	checks := []rateLimitCheck{
		{keyType: rateLimitKeyTypeEmail, value: email, rule: uc.rateLimit.LoginEmail},
		{keyType: rateLimitKeyTypeIP, value: meta.ClientIP, rule: uc.rateLimit.LoginIP},
	}
	now := time.Now().UTC()
	for _, check := range checks {
		value := strings.TrimSpace(check.value)
		if value == "" || check.rule.Max <= 0 || check.rule.Window <= 0 {
			continue
		}

		windowStart := now.Truncate(check.rule.Window)
		result, err := uc.rateLimiter.IncrementAuthRateLimit(ctx, entity.AuthRateLimit{
			Action:        authEventLogin,
			KeyHash:       authRateLimitKeyHash(authEventLogin, check.keyType, value),
			WindowStart:   windowStart,
			WindowSeconds: int64(check.rule.Window / time.Second),
			MaxAttempts:   check.rule.Max,
			ExpiresAt:     windowStart.Add(check.rule.Window),
		})
		if err != nil {
			return fmt.Errorf("UserUseCase - enforceLoginRateLimit - IncrementAuthRateLimit: %w", err)
		}
		if !result.Allowed {
			if check.keyType == rateLimitKeyTypeEmail {
				uc.notifySuspiciousFailedLogin(ctx, email)
			}

			return entity.ErrAuthRateLimited
		}
	}

	return nil
}

func authRateLimitKeyHash(action, keyType, value string) string {
	normalizedValue := strings.ToLower(strings.TrimSpace(value))
	hash := sha256.Sum256([]byte(action + "\x00" + keyType + "\x00" + normalizedValue))

	return hex.EncodeToString(hash[:])
}

func (uc *UseCase) auditAuth(
	ctx context.Context,
	event string,
	status string,
	userID string,
	email string,
	errorCode string,
	metadata map[string]string,
) {
	if uc.auditLogger == nil {
		return
	}

	meta := authmeta.From(ctx)
	_ = uc.auditLogger.StoreAuthAuditLog(ctx, entity.AuthAuditLog{
		ID:        uuid.NewString(),
		Event:     event,
		Status:    status,
		UserID:    userID,
		Email:     strings.TrimSpace(email),
		ClientIP:  meta.ClientIP,
		UserAgent: meta.UserAgent,
		ErrorCode: errorCode,
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
	})
}

func auditStatus(err error) string {
	if err != nil {
		return authAuditStatusFailure
	}

	return authAuditStatusSuccess
}

func auditErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, entity.ErrInvalidAuthInput):
		return "invalid_auth_input"
	case errors.Is(err, entity.ErrInvalidCredentials):
		return "invalid_credentials"
	case errors.Is(err, entity.ErrEmailNotVerified):
		return "email_not_verified"
	case errors.Is(err, entity.ErrInvalidVerificationToken):
		return "invalid_verification_token"
	case errors.Is(err, entity.ErrEmailDeliveryFailed):
		return "email_delivery_failed"
	case errors.Is(err, entity.ErrVerificationRateLimited):
		return "verification_rate_limited"
	case errors.Is(err, entity.ErrInvalidPasswordResetToken):
		return "invalid_password_reset_token"
	case errors.Is(err, entity.ErrPasswordResetRateLimited):
		return "password_reset_rate_limited"
	case errors.Is(err, entity.ErrAuthRateLimited):
		return "auth_rate_limited"
	case errors.Is(err, entity.ErrUserAlreadyExists):
		return "user_already_exists"
	case errors.Is(err, entity.ErrInvalidRole):
		return "invalid_role"
	default:
		return "internal_error"
	}
}

func (uc *UseCase) newVerificationToken(userID string, now time.Time) (string, entity.EmailVerificationToken, error) {
	rawTokenBytes := make([]byte, verificationTokenBytes)
	if _, err := rand.Read(rawTokenBytes); err != nil {
		return "", entity.EmailVerificationToken{}, fmt.Errorf("UserUseCase - newVerificationToken - rand.Read: %w", err)
	}

	rawToken := base64.RawURLEncoding.EncodeToString(rawTokenBytes)
	token := entity.EmailVerificationToken{
		ID:        uuid.New().String(),
		UserID:    userID,
		TokenHash: hashVerificationTokenBytes(rawTokenBytes),
		ExpiresAt: now.Add(uc.verificationTTL),
		SentAt:    now,
		CreatedAt: now,
	}

	return rawToken, token, nil
}

func (uc *UseCase) newPasswordResetToken(userID string, now time.Time) (string, entity.PasswordResetToken, error) {
	rawTokenBytes := make([]byte, passwordResetTokenBytes)
	if _, err := rand.Read(rawTokenBytes); err != nil {
		return "", entity.PasswordResetToken{}, fmt.Errorf("UserUseCase - newPasswordResetToken - rand.Read: %w", err)
	}

	rawToken := base64.RawURLEncoding.EncodeToString(rawTokenBytes)
	token := entity.PasswordResetToken{
		ID:        uuid.New().String(),
		UserID:    userID,
		TokenHash: hashTokenBytes(rawTokenBytes),
		ExpiresAt: now.Add(uc.passwordResetTTL),
		SentAt:    now,
		CreatedAt: now,
	}

	return rawToken, token, nil
}

func (uc *UseCase) sendVerificationEmail(ctx context.Context, user entity.User, rawToken string) error {
	if uc.emailSender == nil {
		return entity.ErrEmailDeliveryFailed
	}

	link, err := uc.verificationLink(rawToken)
	if err != nil {
		return err
	}

	message := entity.EmailMessage{
		To:      user.Email,
		Subject: "Verify your Surau email",
		HTML: authEmailHTML(authEmailView{
			Preview:     "Verify your Surau email to finish setting up your account.",
			Title:       "Verify your email",
			Greeting:    "Assalamu'alaikum, " + user.Username,
			Body:        "Confirm this email address so your Surau account is ready to use.",
			ButtonLabel: "Verify email",
			ButtonURL:   link,
			Note:        "This verification link expires in " + humanDurationText(uc.verificationTTL) + ".",
			Footer:      "If you did not create a Surau account, you can ignore this email.",
		}),
		Text: fmt.Sprintf(
			"Assalamu'alaikum, %s\n\nPlease verify your email address for Surau:\n%s\n\nThis link expires in %s.\n\nIf you did not create a Surau account, you can ignore this email.",
			user.Username,
			link,
			humanDurationText(uc.verificationTTL),
		),
	}

	if err = uc.emailSender.Send(ctx, message); err != nil {
		return fmt.Errorf("%w: %w", entity.ErrEmailDeliveryFailed, err)
	}

	return nil
}

func (uc *UseCase) sendPasswordResetEmail(ctx context.Context, user entity.User, rawToken string) error {
	if uc.emailSender == nil {
		return entity.ErrEmailDeliveryFailed
	}

	link, err := uc.passwordResetLink(rawToken)
	if err != nil {
		return err
	}

	message := entity.EmailMessage{
		To:      user.Email,
		Subject: "Reset your Surau password",
		HTML: authEmailHTML(authEmailView{
			Preview:     "Use this secure link to reset your Surau password.",
			Title:       "Reset your password",
			Greeting:    "Assalamu'alaikum, " + user.Username,
			Body:        "We received a request to reset the password for your Surau account.",
			ButtonLabel: "Reset password",
			ButtonURL:   link,
			Note:        "This password reset link expires in " + humanDurationText(uc.passwordResetTTL) + ".",
			Footer:      "If you did not request this, you can safely ignore this email.",
		}),
		Text: fmt.Sprintf(
			"Assalamu'alaikum, %s\n\nWe received a request to reset your Surau password:\n%s\n\nThis link expires in %s.\n\nIf you did not request this, you can safely ignore this email.",
			user.Username,
			link,
			humanDurationText(uc.passwordResetTTL),
		),
	}

	if err = uc.emailSender.Send(ctx, message); err != nil {
		return fmt.Errorf("%w: %w", entity.ErrEmailDeliveryFailed, err)
	}

	return nil
}

func (uc *UseCase) verificationLink(rawToken string) (string, error) {
	parsedURL, err := url.Parse(uc.verifyFrontendURL)
	if err != nil || !parsedURL.IsAbs() {
		return "", fmt.Errorf("%w: invalid verification frontend URL", entity.ErrEmailDeliveryFailed)
	}

	query := parsedURL.Query()
	query.Set("token", rawToken)
	parsedURL.RawQuery = query.Encode()

	return parsedURL.String(), nil
}

func (uc *UseCase) passwordResetLink(rawToken string) (string, error) {
	parsedURL, err := url.Parse(uc.passwordResetFrontendURL)
	if err != nil || !parsedURL.IsAbs() {
		return "", fmt.Errorf("%w: invalid password reset frontend URL", entity.ErrEmailDeliveryFailed)
	}

	query := parsedURL.Query()
	query.Set("token", rawToken)
	parsedURL.RawQuery = query.Encode()

	return parsedURL.String(), nil
}

func (uc *UseCase) notifyPasswordChanged(ctx context.Context, user entity.User) {
	if !uc.notificationEnabled(uc.emailNotifications.PasswordChangedEnabled) || !validEmail(user.Email) {
		return
	}

	name := displayName(user)
	message := "Password akun Surau Anda baru saja diubah. Your Surau password was just changed."
	note := "Jika ini bukan Anda, segera gunakan fitur lupa password dan hubungi support. If this was not you, reset your password immediately and contact support."
	uc.sendSecurityNotification(ctx, user, "Password Surau berhasil diubah", authEmailView{
		Preview:  "Password akun Surau Anda baru saja diubah.",
		Title:    "Password berhasil diubah",
		Greeting: "Assalamu'alaikum, " + name,
		Body:     message,
		Note:     note,
		Footer:   "Email keamanan ini dikirim untuk membantu melindungi akun Anda. This security email helps protect your account.",
	}, fmt.Sprintf("Assalamu'alaikum, %s\n\n%s\n\n%s", name, message, note))
}

func (uc *UseCase) notifyEmailVerified(ctx context.Context, user entity.User) {
	if !uc.notificationEnabled(uc.emailNotifications.EmailVerifiedEnabled) || !validEmail(user.Email) {
		return
	}

	name := displayName(user)
	message := "Email akun Surau Anda sudah diverifikasi dan akun siap digunakan. Your Surau email is now verified."
	uc.sendSecurityNotification(ctx, user, "Email Surau berhasil diverifikasi", authEmailView{
		Preview:  "Email akun Surau Anda sudah diverifikasi.",
		Title:    "Email berhasil diverifikasi",
		Greeting: "Assalamu'alaikum, " + name,
		Body:     message,
		Note:     "Terima kasih sudah menjaga keamanan akun. Thank you for keeping your account secure.",
		Footer:   "Jika Anda tidak melakukan verifikasi ini, segera hubungi support. If this was not you, contact support.",
	}, fmt.Sprintf("Assalamu'alaikum, %s\n\n%s\n\nJika Anda tidak melakukan verifikasi ini, segera hubungi support.", name, message))
}

func (uc *UseCase) notifyRoleChanged(ctx context.Context, user entity.User) {
	if !uc.notificationEnabled(uc.emailNotifications.RoleChangedEnabled) || !validEmail(user.Email) {
		return
	}

	name := displayName(user)
	role := strings.TrimSpace(user.Role)
	if role == "" {
		role = "updated"
	}
	message := fmt.Sprintf("Role akun Surau Anda berubah menjadi %s. Your Surau account role was changed to %s.", role, role)
	uc.sendSecurityNotification(ctx, user, "Role akun Surau berubah", authEmailView{
		Preview:  "Role akun Surau Anda baru saja berubah.",
		Title:    "Role akun berubah",
		Greeting: "Assalamu'alaikum, " + name,
		Body:     message,
		Note:     "Jika perubahan ini tidak Anda kenali, segera hubungi support. If you do not recognize this change, contact support.",
		Footer:   "Email keamanan ini dikirim karena izin akun berubah. This security email was sent because account permissions changed.",
	}, fmt.Sprintf("Assalamu'alaikum, %s\n\n%s\n\nJika perubahan ini tidak Anda kenali, segera hubungi support.", name, message))
}

func (uc *UseCase) notifyNewLogin(ctx context.Context, user entity.User) {
	if !uc.notificationEnabled(uc.emailNotifications.NewLoginEnabled) || !validEmail(user.Email) {
		return
	}

	meta := authmeta.From(ctx)
	clientIP, userAgent, ok := loginFingerprintInputs(meta)
	if !ok {
		return
	}

	now := time.Now().UTC()
	isNew, err := uc.repo.RecordAuthLoginFingerprint(ctx, entity.AuthLoginFingerprint{
		UserID:          user.ID,
		FingerprintHash: loginFingerprintHash(clientIP, userAgent),
		ClientIP:        clientIP,
		UserAgent:       truncateRunes(userAgent, maxEmailUserAgentRunes),
		SeenAt:          now,
	})
	if err != nil || !isNew {
		return
	}

	name := displayName(user)
	device := truncateRunes(userAgent, maxEmailUserAgentRunes)
	if device == "" {
		device = "unknown"
	}
	details := fmt.Sprintf("Waktu: %s UTC. IP: %s. Perangkat: %s.", now.Format("2006-01-02 15:04:05"), clientIP, device)
	message := "Ada login baru ke akun Surau Anda. A new login to your Surau account was detected."
	uc.sendSecurityNotification(ctx, user, "Login baru ke akun Surau", authEmailView{
		Preview:  "Ada login baru ke akun Surau Anda.",
		Title:    "Login baru terdeteksi",
		Greeting: "Assalamu'alaikum, " + name,
		Body:     message,
		Note:     details + " Jika ini bukan Anda, segera ubah password. If this was not you, change your password immediately.",
		Footer:   "Email ini hanya dikirim untuk kombinasi IP/perangkat yang belum pernah terlihat sebelumnya.",
	}, fmt.Sprintf("Assalamu'alaikum, %s\n\n%s\n\n%s\n\nJika ini bukan Anda, segera ubah password.", name, message, details))
}

func (uc *UseCase) notifySuspiciousFailedLogin(ctx context.Context, email string) {
	if !uc.notificationEnabled(uc.emailNotifications.FailedLoginEnabled) || !validEmail(email) {
		return
	}

	user, err := uc.repo.GetByEmail(ctx, email)
	if err != nil || !validEmail(user.Email) {
		return
	}

	acquired, err := uc.repo.AcquireAuthNotificationCooldown(ctx, entity.AuthNotificationCooldown{
		Event:     authEmailFailedLogin,
		KeyHash:   authNotificationKeyHash(authEmailFailedLogin, rateLimitKeyTypeEmail, user.Email),
		ExpiresAt: time.Now().UTC().Add(uc.emailNotifications.FailedLoginCooldown),
	})
	if err != nil || !acquired {
		return
	}

	name := displayName(user)
	message := "Kami membatasi percobaan login ke akun Surau Anda karena terlalu banyak percobaan. We limited login attempts because there were too many tries."
	uc.sendSecurityNotification(ctx, user, "Percobaan login Surau dibatasi", authEmailView{
		Preview:  "Percobaan login ke akun Surau Anda sedang dibatasi.",
		Title:    "Percobaan login dibatasi",
		Greeting: "Assalamu'alaikum, " + name,
		Body:     message,
		Note:     "Jika ini bukan Anda, abaikan password lama dan reset password dari halaman login. If this was not you, reset your password from the login page.",
		Footer:   "Notifikasi ini dibatasi frekuensinya agar tidak mengganggu. This notification is rate-limited.",
	}, fmt.Sprintf("Assalamu'alaikum, %s\n\n%s\n\nJika ini bukan Anda, reset password dari halaman login.", name, message))
}

func (uc *UseCase) notificationEnabled(flag bool) bool {
	return uc.emailNotifications.Enabled && flag && uc.emailSender != nil
}

func (uc *UseCase) sendSecurityNotification(
	ctx context.Context,
	user entity.User,
	subject string,
	view authEmailView,
	text string,
) {
	if strings.TrimSpace(view.Greeting) == "" {
		view.Greeting = "Assalamu'alaikum, " + displayName(user)
	}
	_ = uc.emailSender.Send(ctx, entity.EmailMessage{
		To:      user.Email,
		Subject: subject,
		HTML:    authEmailHTML(view),
		Text:    text,
	})
}

func displayName(user entity.User) string {
	username := strings.TrimSpace(user.Username)
	if username != "" {
		return username
	}

	return "Sahabat Surau"
}

func loginFingerprintInputs(meta authmeta.Meta) (string, string, bool) {
	clientIP := strings.TrimSpace(meta.ClientIP)
	userAgent := strings.TrimSpace(meta.UserAgent)
	transport := strings.TrimSpace(meta.Transport)

	if clientIP == transport || clientIP == "unknown" || clientIP == "nats" || clientIP == "amqp" {
		clientIP = ""
	}
	if clientIP == "" && userAgent == "" {
		return "", "", false
	}

	return clientIP, userAgent, true
}

func loginFingerprintHash(clientIP, userAgent string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(clientIP) + "\x00" + strings.TrimSpace(userAgent)))

	return hex.EncodeToString(hash[:])
}

func authNotificationKeyHash(event, keyType, value string) string {
	return authRateLimitKeyHash("notification:"+event, keyType, value)
}

func truncateRunes(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}

	runes := []rune(value)
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}

	return string(runes[:maxRunes-3]) + "..."
}

type authEmailView struct {
	Preview     string
	Title       string
	Greeting    string
	Body        string
	ButtonLabel string
	ButtonURL   string
	Note        string
	Footer      string
}

func authEmailHTML(view authEmailView) string {
	preview := html.EscapeString(view.Preview)
	title := html.EscapeString(view.Title)
	greeting := html.EscapeString(view.Greeting)
	body := html.EscapeString(view.Body)
	note := html.EscapeString(view.Note)
	footer := html.EscapeString(view.Footer)
	actionHTML := ""
	buttonLabel := strings.TrimSpace(view.ButtonLabel)
	buttonURL := strings.TrimSpace(view.ButtonURL)
	if buttonLabel != "" && buttonURL != "" {
		escapedButtonLabel := html.EscapeString(buttonLabel)
		escapedButtonURL := html.EscapeString(buttonURL)
		actionHTML = fmt.Sprintf(`
                  <tr>
                    <td style="padding:26px 32px 0 32px;">
                      <a href="%s" style="display:inline-block;border-radius:14px;background:#6f9368;color:#ffffff;font-size:15px;font-weight:700;line-height:20px;text-decoration:none;padding:14px 18px;">%s</a>
                    </td>
                  </tr>
                  <tr>
                    <td style="padding:24px 32px 0 32px;">
                      <p style="margin:0;color:#706d64;font-size:13px;line-height:21px;">If the button does not work, copy and paste this link into your browser:</p>
                      <p style="margin:8px 0 0 0;word-break:break-all;color:#52794d;font-size:13px;line-height:21px;"><a href="%s" style="color:#52794d;text-decoration:underline;">%s</a></p>
                    </td>
                  </tr>`, escapedButtonURL, escapedButtonLabel, escapedButtonURL, escapedButtonURL)
	}
	noteHTML := ""
	if note != "" {
		noteHTML = fmt.Sprintf(`
                  <tr>
                    <td style="padding:24px 32px 0 32px;">
                      <p style="margin:0;padding:14px 16px;border-radius:16px;background:#f6f5ef;color:#5f5d55;font-size:14px;line-height:22px;">%s</p>
                    </td>
                  </tr>`, note)
	}

	return fmt.Sprintf(`<!doctype html>
<html lang="id">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="color-scheme" content="light">
    <meta name="supported-color-schemes" content="light">
    <title>%s</title>
  </head>
  <body style="margin:0;padding:0;background:#f6f5ef;color:#25241f;font-family:Inter, Arial, sans-serif;-webkit-font-smoothing:antialiased;">
    <div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">%s</div>
    <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;background:#f6f5ef;">
      <tr>
        <td align="center" style="padding:32px 16px;">
          <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;width:100%%;max-width:560px;">
            <tr>
              <td style="padding:0 0 16px 0;">
                <table role="presentation" cellpadding="0" cellspacing="0" style="border-collapse:collapse;">
                  <tr>
                    <td align="center" width="40" height="40" style="width:40px;height:40px;border-radius:14px;background:#6f9368;color:#ffffff;font-size:18px;font-weight:700;line-height:40px;">S</td>
                    <td style="padding-left:12px;color:#25241f;font-size:18px;font-weight:700;line-height:24px;">Surau</td>
                  </tr>
                </table>
              </td>
            </tr>
            <tr>
              <td style="background:#fffffb;border-radius:20px;overflow:hidden;">
                <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;">
                  <tr>
                    <td style="padding:32px 32px 12px 32px;">
                      <div style="display:inline-block;padding:6px 10px;border-radius:999px;background:#eef5ed;color:#52794d;font-size:12px;font-weight:600;line-height:16px;">Account Security</div>
                      <h1 style="margin:18px 0 0 0;color:#25241f;font-size:28px;font-weight:750;line-height:34px;letter-spacing:0;">%s</h1>
                    </td>
                  </tr>
                  <tr>
                    <td style="padding:4px 32px 0 32px;">
                      <p style="margin:0;color:#5f5d55;font-size:15px;line-height:24px;">%s</p>
                    </td>
                  </tr>
                  <tr>
                    <td style="padding:14px 32px 0 32px;">
                      <p style="margin:0;color:#25241f;font-size:16px;line-height:26px;">%s</p>
                    </td>
                  </tr>%s%s
                  <tr>
                    <td style="padding:24px 32px 32px 32px;"></td>
                  </tr>
                </table>
              </td>
            </tr>
            <tr>
              <td style="padding:16px 4px 0 4px;">
                <p style="margin:0;color:#706d64;font-size:12px;line-height:20px;">%s</p>
              </td>
            </tr>
          </table>
        </td>
      </tr>
    </table>
  </body>
</html>`, title, preview, title, greeting, body, actionHTML, noteHTML, footer)
}

func humanDurationText(duration time.Duration) string {
	if duration%time.Hour == 0 {
		hours := int(duration / time.Hour)
		if hours == 1 {
			return "1 hour"
		}

		return fmt.Sprintf("%d hours", hours)
	}
	if duration%time.Minute == 0 {
		minutes := int(duration / time.Minute)
		if minutes == 1 {
			return "1 minute"
		}

		return fmt.Sprintf("%d minutes", minutes)
	}

	return duration.String()
}

func hashVerificationToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	rawTokenBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(rawTokenBytes) != verificationTokenBytes {
		return "", entity.ErrInvalidVerificationToken
	}

	return hashTokenBytes(rawTokenBytes), nil
}

func hashPasswordResetToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	rawTokenBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(rawTokenBytes) != passwordResetTokenBytes {
		return "", entity.ErrInvalidPasswordResetToken
	}

	return hashTokenBytes(rawTokenBytes), nil
}

func hashVerificationTokenBytes(rawTokenBytes []byte) string {
	return hashTokenBytes(rawTokenBytes)
}

func hashTokenBytes(rawTokenBytes []byte) string {
	hash := sha256.Sum256(rawTokenBytes)

	return hex.EncodeToString(hash[:])
}
