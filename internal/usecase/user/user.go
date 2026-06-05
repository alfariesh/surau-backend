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

	"github.com/evrone/go-clean-template/internal/contentlang"
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
	defaultAdminLimit       = 50
	maxAdminLimit           = 200
	verificationTokenBytes  = 32
	passwordResetTokenBytes = 32
	emailChangeTokenBytes   = 32
	maxResetTokenInputBytes = 512
	maxEmailUserAgentRunes  = 160
	defaultSupportEmail     = "support@surau.org"
)

const (
	authEventRegister           = "register"
	authEventLogin              = "login"
	authEventVerifyEmail        = "verify_email"
	authEventResendVerification = "resend_verification"
	authEventForgotPassword     = "forgot_password"
	authEventResetPassword      = "reset_password"
	authEventChangePassword     = "change_password"
	authEventProfileUpdate      = "profile_update"
	authEventChangeEmailRequest = "change_email_request"
	authEventChangeEmailVerify  = "change_email_verify"
	authEventAccountDelete      = "account_delete"
	authEventRoleChange         = "role_change"
	authEmailPasswordChanged    = "password_changed"
	authEmailEmailVerified      = "email_verified"
	authEmailNewLogin           = "new_login"
	authEmailFailedLogin        = "failed_login"
	authEmailRoleChanged        = "role_changed"
	authEmailEmailChanged       = "email_changed"
	authEmailAccountDeleted     = "account_deleted"
	authAuditStatusSuccess      = "success"
	authAuditStatusFailure      = "failure"
	rateLimitKeyTypeEmail       = "email"
	rateLimitKeyTypeIP          = "ip"
	rateLimitKeyTypeToken       = "token"
	rateLimitKeyTypeUser        = "user"
)

const (
	surauFacebookURL  = "https://www.facebook.com/surauapp"
	surauInstagramURL = "https://www.instagram.com/surauapp"
	surauTikTokURL    = "https://www.tiktok.com/@surauapp"
	surauXURL         = "https://x.com/surauapp"
	surauYouTubeURL   = "https://www.youtube.com/@surauapp"

	surauFacebookIconURL  = "https://cdn.surau.org/icons/duotone/facebook.svg"
	surauInstagramIconURL = "https://cdn.surau.org/icons/duotone/instagram-duotone-rounded.svg"
	surauTikTokIconURL    = "https://cdn.surau.org/icons/duotone/tiktok.svg"
	surauXIconURL         = "https://cdn.surau.org/icons/duotone/x.svg"
	surauYouTubeIconURL   = "https://cdn.surau.org/icons/duotone/youtube.svg"
)

// UseCase -.
type UseCase struct {
	repo                     repo.UserRepo
	jwt                      *jwt.Manager
	emailSender              repo.EmailSender
	emailService             TransactionalEmailService
	verifyFrontendURL        string
	verificationTTL          time.Duration
	resendCooldown           time.Duration
	passwordResetFrontendURL string
	passwordResetTTL         time.Duration
	passwordResetCooldown    time.Duration
	emailChangeFrontendURL   string
	emailChangeTTL           time.Duration
	emailChangeCooldown      time.Duration
	supportEmail             string
	rateLimiter              repo.AuthRateLimitRepo
	auditLogger              repo.AuthAuditRepo
	rateLimit                RateLimitOptions
	emailNotifications       EmailNotificationOptions
}

// TransactionalEmailService sends admin-managed transactional emails.
type TransactionalEmailService interface {
	SendTransactional(ctx context.Context, req entity.TransactionalEmailRequest) error
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
	ChangeEmailUser         RateLimitRule
	ChangeEmailIP           RateLimitRule
	ChangeEmailToken        RateLimitRule
	DeleteAccountUser       RateLimitRule
	DeleteAccountIP         RateLimitRule
}

// EmailNotificationOptions configures best-effort auth security emails.
type EmailNotificationOptions struct {
	Enabled                bool
	NewLoginEnabled        bool
	FailedLoginEnabled     bool
	PasswordChangedEnabled bool
	EmailVerifiedEnabled   bool
	RoleChangedEnabled     bool
	EmailChangedEnabled    bool
	AccountDeletedEnabled  bool
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
	EmailChangeFrontendURL   string
	EmailChangeTTL           time.Duration
	EmailChangeCooldown      time.Duration
	SupportEmail             string
	EmailService             TransactionalEmailService
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
	emailChangeTTL := opts.EmailChangeTTL
	if emailChangeTTL <= 0 {
		emailChangeTTL = 24 * time.Hour
	}
	emailChangeCooldown := opts.EmailChangeCooldown
	if emailChangeCooldown <= 0 {
		emailChangeCooldown = time.Minute
	}

	return &UseCase{
		repo:                     r,
		jwt:                      j,
		emailSender:              emailSender,
		emailService:             opts.EmailService,
		verifyFrontendURL:        opts.VerifyFrontendURL,
		verificationTTL:          verificationTTL,
		resendCooldown:           resendCooldown,
		passwordResetFrontendURL: opts.PasswordResetFrontendURL,
		passwordResetTTL:         passwordResetTTL,
		passwordResetCooldown:    passwordResetCooldown,
		emailChangeFrontendURL:   opts.EmailChangeFrontendURL,
		emailChangeTTL:           emailChangeTTL,
		emailChangeCooldown:      emailChangeCooldown,
		supportEmail:             normalizeSupportEmail(opts.SupportEmail),
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

// AdminUsers returns admin-managed account rows with lightweight filters.
func (uc *UseCase) AdminUsers(
	ctx context.Context,
	query,
	role string,
	emailVerified *bool,
	limit,
	offset int,
) ([]entity.UserAccount, int, error) {
	var err error
	role = strings.TrimSpace(role)
	if role != "" {
		role, err = entity.NormalizeUserRole(role)
		if err != nil {
			return nil, 0, entity.ErrInvalidRole
		}
	}

	return uc.repo.ListAccounts(ctx, repo.UserFilter{
		Query:         strings.TrimSpace(query),
		Role:          role,
		EmailVerified: emailVerified,
		Limit:         clampAdminLimit(limit),
		Offset:        clampAdminOffset(offset),
	})
}

// AdminUserActivity returns admin-visible audit events for one user.
func (uc *UseCase) AdminUserActivity(
	ctx context.Context,
	userID string,
	limit,
	offset int,
) ([]entity.UserActivity, int, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, 0, entity.ErrUserNotFound
	}

	if _, err := uc.repo.GetByID(ctx, userID); err != nil {
		return nil, 0, err
	}

	return uc.repo.ListUserActivity(ctx, repo.UserActivityFilter{
		UserID: userID,
		Limit:  clampAdminLimit(limit),
		Offset: clampAdminOffset(offset),
	})
}

// SetRoleByEmail updates a user role.
func (uc *UseCase) SetRoleByEmail(
	ctx context.Context,
	actorID,
	actorEmail,
	email,
	role string,
) (updated entity.User, err error) {
	actorID = strings.TrimSpace(actorID)
	actorEmail = strings.ToLower(strings.TrimSpace(actorEmail))
	email = strings.ToLower(strings.TrimSpace(email))
	role = strings.ToLower(strings.TrimSpace(role))
	auditEmail := email
	auditUserID := ""
	oldRole := ""
	defer func() {
		uc.auditAuth(ctx, authEventRoleChange, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), map[string]string{
			"actor_id":    actorID,
			"actor_email": actorEmail,
			"old_role":    oldRole,
			"new_role":    role,
			"role":        role,
		})
	}()

	role, err = entity.NormalizeUserRole(role)
	if err != nil {
		return entity.User{}, entity.ErrInvalidRole
	}

	change, err := uc.repo.SetRoleByEmail(ctx, email, role)
	if err == nil {
		updated = change.User
		oldRole = change.PreviousRole
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

// GetUserAccount returns the current user plus onboarding/profile preferences.
func (uc *UseCase) GetUserAccount(ctx context.Context, userID string) (entity.UserAccount, error) {
	account, err := uc.repo.GetAccount(ctx, userID)
	if err != nil {
		return entity.UserAccount{}, fmt.Errorf("UserUseCase - GetUserAccount - uc.repo.GetAccount: %w", err)
	}

	return account, nil
}

// CompleteOnboarding stores the onboarding answers and marks onboarding complete.
func (uc *UseCase) CompleteOnboarding(
	ctx context.Context,
	userID string,
	onboarding entity.UserOnboarding,
) (entity.UserAccount, error) {
	account, err := uc.repo.GetAccount(ctx, userID)
	if err != nil {
		return entity.UserAccount{}, fmt.Errorf("UserUseCase - CompleteOnboarding - uc.repo.GetAccount: %w", err)
	}

	now := time.Now().UTC()
	profile := account.Profile
	profile.DisplayName, err = normalizeDisplayNamePtr(onboarding.DisplayName, true)
	if err != nil {
		return entity.UserAccount{}, err
	}
	profile.Timezone = cleanOptionalString(onboarding.Timezone)
	countryCode := cleanOptionalString(onboarding.CountryCode)
	profile.CountryCode = normalizeCountryCode(onboarding.CountryCode)
	if countryCode != nil && profile.CountryCode == nil {
		return entity.UserAccount{}, entity.ErrInvalidUserPreference
	}
	if onboarding.PersonalizationEnabled != nil {
		profile.PersonalizationEnabled = *onboarding.PersonalizationEnabled
	}
	profile.OnboardingVersion = entity.UserOnboardingVersion
	profile.OnboardingCompletedAt = &now

	preferences := account.Preferences
	if err = applyOnboardingPreferences(&preferences, onboarding); err != nil {
		return entity.UserAccount{}, err
	}

	if err = uc.repo.UpsertProfile(ctx, profile); err != nil {
		return entity.UserAccount{}, fmt.Errorf("UserUseCase - CompleteOnboarding - uc.repo.UpsertProfile: %w", err)
	}
	if err = uc.repo.UpsertPreferences(ctx, preferences); err != nil {
		return entity.UserAccount{}, fmt.Errorf("UserUseCase - CompleteOnboarding - uc.repo.UpsertPreferences: %w", err)
	}

	return uc.GetUserAccount(ctx, userID)
}

// UpdateUserProfile stores partial profile changes after onboarding.
func (uc *UseCase) UpdateUserProfile(
	ctx context.Context,
	userID string,
	patch entity.UserProfilePatch,
) (updated entity.UserAccount, err error) {
	auditUserID := strings.TrimSpace(userID)
	defer func() {
		uc.auditAuth(ctx, authEventProfileUpdate, auditStatus(err), auditUserID, "", auditErrorCode(err), nil)
	}()

	account, err := uc.repo.GetAccount(ctx, userID)
	if err != nil {
		return entity.UserAccount{}, fmt.Errorf("UserUseCase - UpdateUserProfile - uc.repo.GetAccount: %w", err)
	}
	auditUserID = account.ID

	profile := account.Profile
	if patch.DisplayName != nil {
		profile.DisplayName, err = normalizeDisplayNamePtr(patch.DisplayName, false)
		if err != nil {
			return entity.UserAccount{}, err
		}
	}
	if patch.Timezone != nil {
		profile.Timezone = cleanOptionalString(patch.Timezone)
	}
	if patch.CountryCode != nil {
		countryCode := cleanOptionalString(patch.CountryCode)
		profile.CountryCode = normalizeCountryCode(patch.CountryCode)
		if countryCode != nil && profile.CountryCode == nil {
			return entity.UserAccount{}, entity.ErrInvalidUserPreference
		}
	}
	if patch.PersonalizationEnabled != nil {
		profile.PersonalizationEnabled = *patch.PersonalizationEnabled
	}

	if err = uc.repo.UpsertProfile(ctx, profile); err != nil {
		return entity.UserAccount{}, fmt.Errorf("UserUseCase - UpdateUserProfile - uc.repo.UpsertProfile: %w", err)
	}

	return uc.GetUserAccount(ctx, userID)
}

// UpdateUserPreferences stores partial preference changes after onboarding.
func (uc *UseCase) UpdateUserPreferences(
	ctx context.Context,
	userID string,
	patch entity.UserPreferencesPatch,
) (entity.UserAccount, error) {
	account, err := uc.repo.GetAccount(ctx, userID)
	if err != nil {
		return entity.UserAccount{}, fmt.Errorf("UserUseCase - UpdateUserPreferences - uc.repo.GetAccount: %w", err)
	}

	preferences := account.Preferences
	if err = applyPreferencePatch(&preferences, patch); err != nil {
		return entity.UserAccount{}, err
	}

	if err = uc.repo.UpsertPreferences(ctx, preferences); err != nil {
		return entity.UserAccount{}, fmt.Errorf("UserUseCase - UpdateUserPreferences - uc.repo.UpsertPreferences: %w", err)
	}

	return uc.GetUserAccount(ctx, userID)
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

// RequestEmailChange sends a verification link to a new email after password confirmation.
func (uc *UseCase) RequestEmailChange(ctx context.Context, userID, currentPassword, newEmail string) (err error) {
	auditUserID := strings.TrimSpace(userID)
	auditEmail := strings.TrimSpace(newEmail)
	defer func() {
		uc.auditAuth(ctx, authEventChangeEmailRequest, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), nil)
	}()

	if strings.TrimSpace(userID) == "" || !validPassword(currentPassword) {
		return entity.ErrInvalidAuthInput
	}
	newEmail, err = validateEmailInput(newEmail)
	if err != nil {
		return err
	}
	auditEmail = newEmail

	if err = uc.enforceAuthRateLimit(ctx, authEventChangeEmailRequest, []rateLimitCheck{
		{keyType: rateLimitKeyTypeUser, value: userID, rule: uc.rateLimit.ChangeEmailUser},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.ChangeEmailIP},
	}); err != nil {
		return err
	}

	user, err := uc.repo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return entity.ErrInvalidCredentials
		}

		return fmt.Errorf("UserUseCase - RequestEmailChange - uc.repo.GetByID: %w", err)
	}
	auditUserID = user.ID

	if err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		return entity.ErrInvalidCredentials
	}
	if strings.EqualFold(user.Email, newEmail) {
		return entity.ErrInvalidAuthInput
	}

	existingUser, err := uc.repo.GetByEmail(ctx, newEmail)
	if err == nil && existingUser.ID != user.ID {
		return entity.ErrUserAlreadyExists
	}
	if err != nil && !errors.Is(err, entity.ErrUserNotFound) {
		return fmt.Errorf("UserUseCase - RequestEmailChange - uc.repo.GetByEmail: %w", err)
	}

	now := time.Now().UTC()
	latestToken, err := uc.repo.GetLatestUnusedEmailChangeToken(ctx, user.ID)
	if err != nil && !errors.Is(err, entity.ErrEmailChangeTokenNotFound) {
		return fmt.Errorf("UserUseCase - RequestEmailChange - uc.repo.GetLatestUnusedEmailChangeToken: %w", err)
	}
	if err == nil && now.Before(latestToken.SentAt.Add(uc.emailChangeCooldown)) {
		return entity.ErrEmailChangeRateLimited
	}

	rawToken, emailChangeToken, err := uc.newEmailChangeToken(user.ID, newEmail, now)
	if err != nil {
		return err
	}
	if err = uc.repo.ReplaceEmailChangeToken(ctx, &emailChangeToken); err != nil {
		return fmt.Errorf("UserUseCase - RequestEmailChange - uc.repo.ReplaceEmailChangeToken: %w", err)
	}
	if err = uc.sendEmailChangeVerificationEmail(ctx, user, newEmail, rawToken); err != nil {
		_ = uc.repo.RevokeUnusedEmailChangeTokens(ctx, user.ID)

		return err
	}

	return nil
}

// VerifyEmailChange updates a user's email using an authenticated one-time token.
func (uc *UseCase) VerifyEmailChange(ctx context.Context, userID, token string) (err error) {
	auditUserID := strings.TrimSpace(userID)
	auditEmail := ""
	defer func() {
		uc.auditAuth(ctx, authEventChangeEmailVerify, auditStatus(err), auditUserID, auditEmail, auditErrorCode(err), nil)
	}()

	if strings.TrimSpace(userID) == "" {
		return entity.ErrInvalidAuthInput
	}

	token = strings.TrimSpace(token)
	if len(token) > maxResetTokenInputBytes {
		return entity.ErrInvalidEmailChangeToken
	}
	if err = uc.enforceAuthRateLimit(ctx, authEventChangeEmailVerify, []rateLimitCheck{
		{keyType: rateLimitKeyTypeToken, value: token, rule: uc.rateLimit.ChangeEmailToken},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.ChangeEmailIP},
	}); err != nil {
		return err
	}

	tokenHash, err := hashEmailChangeToken(token)
	if err != nil {
		return err
	}

	storedToken, err := uc.repo.GetEmailChangeTokenByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, entity.ErrEmailChangeTokenNotFound) {
			return entity.ErrInvalidEmailChangeToken
		}

		return fmt.Errorf("UserUseCase - VerifyEmailChange - uc.repo.GetEmailChangeTokenByHash: %w", err)
	}
	if storedToken.UserID != userID ||
		storedToken.UsedAt != nil ||
		!time.Now().UTC().Before(storedToken.ExpiresAt) {
		return entity.ErrInvalidEmailChangeToken
	}

	result, err := uc.repo.ChangeEmailWithToken(ctx, storedToken.ID, userID, storedToken.NewEmail)
	if err != nil {
		if errors.Is(err, entity.ErrInvalidEmailChangeToken) || errors.Is(err, entity.ErrUserAlreadyExists) {
			return err
		}

		return fmt.Errorf("UserUseCase - VerifyEmailChange - uc.repo.ChangeEmailWithToken: %w", err)
	}
	auditUserID = result.User.ID
	auditEmail = result.NewEmail
	uc.notifyEmailChanged(ctx, result.User, result.OldEmail)

	return nil
}

// DeleteAccount soft-deletes the current account after password confirmation.
func (uc *UseCase) DeleteAccount(ctx context.Context, userID, currentPassword string) (err error) {
	auditUserID := strings.TrimSpace(userID)
	defer func() {
		uc.auditAuth(ctx, authEventAccountDelete, auditStatus(err), auditUserID, "", auditErrorCode(err), nil)
	}()

	if strings.TrimSpace(userID) == "" || !validPassword(currentPassword) {
		return entity.ErrInvalidAuthInput
	}

	if err = uc.enforceAuthRateLimit(ctx, authEventAccountDelete, []rateLimitCheck{
		{keyType: rateLimitKeyTypeUser, value: userID, rule: uc.rateLimit.DeleteAccountUser},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.DeleteAccountIP},
	}); err != nil {
		return err
	}

	user, err := uc.repo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return entity.ErrInvalidCredentials
		}

		return fmt.Errorf("UserUseCase - DeleteAccount - uc.repo.GetByID: %w", err)
	}
	auditUserID = user.ID

	if err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		return entity.ErrInvalidCredentials
	}

	emailCtx := authEmailContext{}
	if uc.notificationEnabled(uc.emailNotifications.AccountDeletedEnabled) && validEmail(user.Email) {
		emailCtx = uc.newAuthEmailContext(ctx, user)
	}
	if err = uc.repo.DeleteAccount(ctx, user.ID); err != nil {
		return fmt.Errorf("UserUseCase - DeleteAccount - uc.repo.DeleteAccount: %w", err)
	}
	uc.notifyAccountDeleted(ctx, user, emailCtx)

	return nil
}

func applyOnboardingPreferences(
	preferences *entity.UserPreferences,
	onboarding entity.UserOnboarding,
) error {
	var err error
	preferences.PreferredUILang, err = normalizeContentLangOrCurrent(
		onboarding.PreferredUILang,
		preferences.PreferredUILang,
	)
	if err != nil {
		return err
	}
	preferences.PreferredContentLang, err = normalizeContentLangOrCurrent(
		onboarding.PreferredContentLang,
		preferences.PreferredContentLang,
	)
	if err != nil {
		return err
	}

	preferences.FallbackLangs, err = normalizeFallbackLangs(onboarding.FallbackLangs, preferences.PreferredContentLang)
	if err != nil {
		return err
	}
	preferences.ArabicLevel, err = normalizeArabicLevel(onboarding.ArabicLevel, preferences.ArabicLevel)
	if err != nil {
		return err
	}
	preferences.ReaderMode, err = normalizeReaderMode(onboarding.ReaderMode, preferences.ReaderMode)
	if err != nil {
		return err
	}
	preferences.Interests, err = normalizeInterests(onboarding.Interests)
	if err != nil {
		return err
	}
	if err = validateDailyGoalMinutes(onboarding.DailyGoalMinutes); err != nil {
		return err
	}
	preferences.DailyGoalMinutes = onboarding.DailyGoalMinutes
	preferences.QuranTranslationSourceID = cleanOptionalString(onboarding.QuranTranslationSourceID)
	preferences.QuranRecitationID = cleanOptionalString(onboarding.QuranRecitationID)

	return nil
}

func applyPreferencePatch(preferences *entity.UserPreferences, patch entity.UserPreferencesPatch) error {
	var err error
	if patch.PreferredUILang != nil {
		preferences.PreferredUILang, err = normalizeContentLangOrCurrent(
			*patch.PreferredUILang,
			preferences.PreferredUILang,
		)
		if err != nil {
			return err
		}
	}
	if patch.PreferredContentLang != nil {
		preferences.PreferredContentLang, err = normalizeContentLangOrCurrent(
			*patch.PreferredContentLang,
			preferences.PreferredContentLang,
		)
		if err != nil {
			return err
		}
	}
	if patch.FallbackLangs != nil {
		preferences.FallbackLangs, err = normalizeFallbackLangs(*patch.FallbackLangs, preferences.PreferredContentLang)
		if err != nil {
			return err
		}
	}
	if patch.ArabicLevel != nil {
		preferences.ArabicLevel, err = normalizeArabicLevel(*patch.ArabicLevel, preferences.ArabicLevel)
		if err != nil {
			return err
		}
	}
	if patch.ReaderMode != nil {
		preferences.ReaderMode, err = normalizeReaderMode(*patch.ReaderMode, preferences.ReaderMode)
		if err != nil {
			return err
		}
	}
	if patch.Interests != nil {
		preferences.Interests, err = normalizeInterests(*patch.Interests)
		if err != nil {
			return err
		}
	}
	if patch.DailyGoalMinutes != nil {
		if err = validateDailyGoalMinutes(patch.DailyGoalMinutes); err != nil {
			return err
		}
		preferences.DailyGoalMinutes = patch.DailyGoalMinutes
	}
	if patch.QuranTranslationSourceID != nil {
		preferences.QuranTranslationSourceID = cleanOptionalString(patch.QuranTranslationSourceID)
	}
	if patch.QuranRecitationID != nil {
		preferences.QuranRecitationID = cleanOptionalString(patch.QuranRecitationID)
	}

	return nil
}

func normalizeContentLangOrCurrent(value, current string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = current
	}

	return contentlang.Normalize(value)
}

func normalizeFallbackLangs(values []string, preferred string) ([]string, error) {
	if len(values) == 0 {
		return []string{preferred}, nil
	}

	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		lang, err := contentlang.Normalize(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[lang]; ok {
			continue
		}
		seen[lang] = struct{}{}
		normalized = append(normalized, lang)
	}
	if len(normalized) == 0 {
		return []string{preferred}, nil
	}

	return normalized, nil
}

func normalizeArabicLevel(value, current string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = current
	}

	switch value {
	case entity.UserArabicLevelNone,
		entity.UserArabicLevelBasic,
		entity.UserArabicLevelIntermediate,
		entity.UserArabicLevelAdvanced,
		entity.UserArabicLevelNative:
		return value, nil
	default:
		return "", entity.ErrInvalidUserPreference
	}
}

func normalizeReaderMode(value, current string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = current
	}

	switch value {
	case entity.UserReaderModeArabicTranslation,
		entity.UserReaderModeTranslationOnly,
		entity.UserReaderModeArabicOnly:
		return value, nil
	default:
		return "", entity.ErrInvalidUserPreference
	}
}

func normalizeInterests(values []string) ([]string, error) {
	allowed := map[string]string{
		"adab":            "adab",
		"aqidah":          "aqidah",
		"arabic_language": "arabic_language",
		"bahasa_arab":     "arabic_language",
		"fiqh":            "fiqh",
		"fikih":           "fiqh",
		"hadis":           "hadith",
		"hadith":          "hadith",
		"hafalan":         "memorization",
		"kitab":           "learn_kitab",
		"learn_kitab":     "learn_kitab",
		"memorization":    "memorization",
		"murottal":        "murottal",
		"quran_daily":     "quran_daily",
		"quran_harian":    "quran_daily",
		"research":        "research",
		"riset":           "research",
		"sirah":           "sirah",
		"tafsir":          "tafsir",
	}

	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		key = strings.ReplaceAll(key, "-", "_")
		key = strings.ReplaceAll(key, " ", "_")
		if key == "" {
			continue
		}

		canonical, ok := allowed[key]
		if !ok {
			return nil, entity.ErrInvalidUserPreference
		}
		if _, ok = seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}

	return normalized, nil
}

func validateDailyGoalMinutes(value *int) error {
	if value == nil {
		return nil
	}
	if *value <= 0 || *value > 1440 {
		return entity.ErrInvalidUserPreference
	}

	return nil
}

func cleanOptionalString(value *string) *string {
	if value == nil {
		return nil
	}

	cleaned := strings.TrimSpace(*value)
	if cleaned == "" {
		return nil
	}

	return &cleaned
}

func normalizeDisplayNamePtr(value *string, allowEmpty bool) (*string, error) {
	cleaned := cleanOptionalString(value)
	if cleaned == nil {
		if allowEmpty {
			return nil, nil
		}

		return nil, entity.ErrInvalidUserPreference
	}

	nameLen := utf8.RuneCountInString(*cleaned)
	if nameLen < minUsernameRunes || nameLen > maxUsernameRunes {
		return nil, entity.ErrInvalidUserPreference
	}

	return cleaned, nil
}

func normalizeCountryCode(value *string) *string {
	cleaned := cleanOptionalString(value)
	if cleaned == nil {
		return nil
	}

	countryCode := strings.ToUpper(*cleaned)
	if len(countryCode) != 2 {
		return nil
	}
	for _, char := range countryCode {
		if char < 'A' || char > 'Z' {
			return nil
		}
	}

	return &countryCode
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
		ChangeEmailUser:         RateLimitRule{Max: 3, Window: time.Hour},
		ChangeEmailIP:           RateLimitRule{Max: 10, Window: time.Hour},
		ChangeEmailToken:        RateLimitRule{Max: 5, Window: 15 * time.Minute},
		DeleteAccountUser:       RateLimitRule{Max: 3, Window: time.Hour},
		DeleteAccountIP:         RateLimitRule{Max: 10, Window: time.Hour},
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
	opts.ChangeEmailUser = withDefaultRule(opts.ChangeEmailUser, defaults.ChangeEmailUser)
	opts.ChangeEmailIP = withDefaultRule(opts.ChangeEmailIP, defaults.ChangeEmailIP)
	opts.ChangeEmailToken = withDefaultRule(opts.ChangeEmailToken, defaults.ChangeEmailToken)
	opts.DeleteAccountUser = withDefaultRule(opts.DeleteAccountUser, defaults.DeleteAccountUser)
	opts.DeleteAccountIP = withDefaultRule(opts.DeleteAccountIP, defaults.DeleteAccountIP)

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
	if event == authEventAccountDelete {
		meta.ClientIP = ""
		meta.UserAgent = ""
	}
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
	case errors.Is(err, entity.ErrInvalidEmailChangeToken):
		return "invalid_email_change_token"
	case errors.Is(err, entity.ErrEmailChangeRateLimited):
		return "email_change_rate_limited"
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

func clampAdminLimit(limit int) uint64 {
	if limit <= 0 {
		return defaultAdminLimit
	}
	if limit > maxAdminLimit {
		return maxAdminLimit
	}

	return uint64(limit)
}

func clampAdminOffset(offset int) uint64 {
	if offset < 0 {
		return 0
	}

	return uint64(offset)
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

func (uc *UseCase) newEmailChangeToken(userID, newEmail string, now time.Time) (string, entity.EmailChangeToken, error) {
	rawTokenBytes := make([]byte, emailChangeTokenBytes)
	if _, err := rand.Read(rawTokenBytes); err != nil {
		return "", entity.EmailChangeToken{}, fmt.Errorf("UserUseCase - newEmailChangeToken - rand.Read: %w", err)
	}

	rawToken := base64.RawURLEncoding.EncodeToString(rawTokenBytes)
	token := entity.EmailChangeToken{
		ID:        uuid.New().String(),
		UserID:    userID,
		NewEmail:  newEmail,
		TokenHash: hashTokenBytes(rawTokenBytes),
		ExpiresAt: now.Add(uc.emailChangeTTL),
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

	emailCtx := uc.newAuthEmailContext(ctx, user)
	content := verificationEmailContent(emailCtx, link, uc.verificationTTL)
	variables := authEmailVariables(emailCtx)
	variables["link"] = link
	variables["duration"] = humanDurationText(uc.verificationTTL, emailCtx.Lang)
	if err = uc.sendAuthEmail(
		ctx,
		user.Email,
		entity.EmailTemplateKeyVerification,
		user.ID,
		emailCtx.Lang,
		variables,
		content,
		true,
	); err != nil {
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

	emailCtx := uc.newAuthEmailContext(ctx, user)
	content := passwordResetEmailContent(emailCtx, link, uc.passwordResetTTL)
	variables := authEmailVariables(emailCtx)
	variables["link"] = link
	variables["duration"] = humanDurationText(uc.passwordResetTTL, emailCtx.Lang)
	if err = uc.sendAuthEmail(
		ctx,
		user.Email,
		entity.EmailTemplateKeyPasswordReset,
		user.ID,
		emailCtx.Lang,
		variables,
		content,
		true,
	); err != nil {
		return fmt.Errorf("%w: %w", entity.ErrEmailDeliveryFailed, err)
	}

	return nil
}

func (uc *UseCase) sendEmailChangeVerificationEmail(
	ctx context.Context,
	user entity.User,
	newEmail string,
	rawToken string,
) error {
	if uc.emailSender == nil {
		return entity.ErrEmailDeliveryFailed
	}

	link, err := uc.emailChangeLink(rawToken)
	if err != nil {
		return err
	}

	emailCtx := uc.newAuthEmailContext(ctx, user)
	content := emailChangeVerificationEmailContent(emailCtx, link, uc.emailChangeTTL)
	variables := authEmailVariables(emailCtx)
	variables["link"] = link
	variables["duration"] = humanDurationText(uc.emailChangeTTL, emailCtx.Lang)
	if err = uc.sendAuthEmail(
		ctx,
		newEmail,
		entity.EmailTemplateKeyEmailChangeVerification,
		user.ID,
		emailCtx.Lang,
		variables,
		content,
		true,
	); err != nil {
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

func (uc *UseCase) emailChangeLink(rawToken string) (string, error) {
	parsedURL, err := url.Parse(uc.emailChangeFrontendURL)
	if err != nil || !parsedURL.IsAbs() {
		return "", fmt.Errorf("%w: invalid email change frontend URL", entity.ErrEmailDeliveryFailed)
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

	emailCtx := uc.newAuthEmailContext(ctx, user)
	uc.sendSecurityNotification(
		ctx,
		user,
		entity.EmailTemplateKeyPasswordChanged,
		authEmailVariables(emailCtx),
		passwordChangedEmailContent(emailCtx),
	)
}

func (uc *UseCase) notifyEmailVerified(ctx context.Context, user entity.User) {
	if !uc.notificationEnabled(uc.emailNotifications.EmailVerifiedEnabled) || !validEmail(user.Email) {
		return
	}

	emailCtx := uc.newAuthEmailContext(ctx, user)
	uc.sendSecurityNotification(
		ctx,
		user,
		entity.EmailTemplateKeyEmailVerified,
		authEmailVariables(emailCtx),
		emailVerifiedEmailContent(emailCtx),
	)
}

func (uc *UseCase) notifyRoleChanged(ctx context.Context, user entity.User) {
	if !uc.notificationEnabled(uc.emailNotifications.RoleChangedEnabled) || !validEmail(user.Email) {
		return
	}

	emailCtx := uc.newAuthEmailContext(ctx, user)
	variables := authEmailVariables(emailCtx)
	variables["role"] = strings.TrimSpace(user.Role)
	uc.sendSecurityNotification(
		ctx,
		user,
		entity.EmailTemplateKeyRoleChanged,
		variables,
		roleChangedEmailContent(emailCtx),
	)
}

func (uc *UseCase) notifyEmailChanged(ctx context.Context, user entity.User, oldEmail string) {
	if !uc.notificationEnabled(uc.emailNotifications.EmailChangedEnabled) {
		return
	}

	emailCtx := uc.newAuthEmailContext(ctx, user)
	content := emailChangedEmailContent(emailCtx, oldEmail, user.Email)
	variables := authEmailVariables(emailCtx)
	variables["old_email"] = strings.TrimSpace(oldEmail)
	variables["new_email"] = strings.TrimSpace(user.Email)
	if validEmail(oldEmail) {
		uc.sendSecurityNotificationTo(ctx, oldEmail, entity.EmailTemplateKeyEmailChanged, user.ID, emailCtx.Lang, variables, content)
	}
	if validEmail(user.Email) && !strings.EqualFold(oldEmail, user.Email) {
		uc.sendSecurityNotificationTo(ctx, user.Email, entity.EmailTemplateKeyEmailChanged, user.ID, emailCtx.Lang, variables, content)
	}
}

func (uc *UseCase) notifyAccountDeleted(ctx context.Context, user entity.User, emailCtx authEmailContext) {
	if !uc.notificationEnabled(uc.emailNotifications.AccountDeletedEnabled) || !validEmail(user.Email) {
		return
	}

	uc.sendSecurityNotificationTo(
		ctx,
		user.Email,
		entity.EmailTemplateKeyAccountDeleted,
		user.ID,
		emailCtx.Lang,
		authEmailVariables(emailCtx),
		accountDeletedEmailContent(emailCtx),
	)
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

	device := truncateRunes(userAgent, maxEmailUserAgentRunes)
	if device == "" {
		device = "unknown"
	}
	emailCtx := uc.newAuthEmailContext(ctx, user)
	details := localizedLoginDetails(emailCtx, now, clientIP, device)
	variables := authEmailVariables(emailCtx)
	variables["details"] = details
	uc.sendSecurityNotification(
		ctx,
		user,
		entity.EmailTemplateKeyNewLogin,
		variables,
		newLoginEmailContent(emailCtx, details),
	)
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

	emailCtx := uc.newAuthEmailContext(ctx, user)
	uc.sendSecurityNotification(
		ctx,
		user,
		entity.EmailTemplateKeyFailedLogin,
		authEmailVariables(emailCtx),
		failedLoginEmailContent(emailCtx),
	)
}

func (uc *UseCase) notificationEnabled(flag bool) bool {
	return uc.emailNotifications.Enabled && flag && uc.emailSender != nil
}

func (uc *UseCase) sendSecurityNotification(
	ctx context.Context,
	user entity.User,
	key string,
	variables map[string]string,
	content authEmailContent,
) {
	uc.sendSecurityNotificationTo(ctx, user.Email, key, user.ID, content.View.Lang, variables, content)
}

func (uc *UseCase) sendSecurityNotificationTo(
	ctx context.Context,
	email string,
	key string,
	userID string,
	lang string,
	variables map[string]string,
	content authEmailContent,
) {
	_ = uc.sendAuthEmail(ctx, email, key, userID, lang, variables, content, false)
}

func (uc *UseCase) sendAuthEmail(
	ctx context.Context,
	email string,
	key string,
	userID string,
	lang string,
	variables map[string]string,
	content authEmailContent,
	critical bool,
) error {
	message := entity.EmailMessage{
		To:      email,
		Subject: content.Subject,
		HTML:    authEmailHTML(content.View),
		Text:    content.Text,
	}
	if uc.emailService != nil {
		return uc.emailService.SendTransactional(ctx, entity.TransactionalEmailRequest{
			Key:       key,
			To:        email,
			UserID:    userID,
			Lang:      lang,
			Variables: variables,
			Fallback:  message,
			Critical:  critical,
		})
	}

	_, err := uc.emailSender.Send(ctx, message)

	return err
}

func authEmailVariables(emailCtx authEmailContext) map[string]string {
	return map[string]string{
		"name":          emailCtx.Name,
		"support_email": emailCtx.SupportEmail,
	}
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

type authEmailContext struct {
	User         entity.User
	Account      *entity.UserAccount
	Lang         string
	Name         string
	SupportEmail string
}

type authEmailContent struct {
	Subject string
	View    authEmailView
	Text    string
}

type authEmailView struct {
	Preview      string
	Title        string
	Lang         string
	Badge        string
	Greeting     string
	Body         string
	ButtonLabel  string
	ButtonURL    string
	Note         string
	Footer       string
	SupportEmail string
}

type authEmailChrome struct {
	Badge               string
	CopyLinkInstruction string
	FooterFollow        string
	FooterSupport       string
	TextFooterIntro     string
}

type authEmailSocialLink struct {
	Label   string
	URL     string
	IconURL string
}

var surauSocialLinks = []authEmailSocialLink{
	{Label: "Facebook", URL: surauFacebookURL, IconURL: surauFacebookIconURL},
	{Label: "Instagram", URL: surauInstagramURL, IconURL: surauInstagramIconURL},
	{Label: "TikTok", URL: surauTikTokURL, IconURL: surauTikTokIconURL},
	{Label: "X", URL: surauXURL, IconURL: surauXIconURL},
	{Label: "YouTube", URL: surauYouTubeURL, IconURL: surauYouTubeIconURL},
}

func (uc *UseCase) newAuthEmailContext(ctx context.Context, user entity.User) authEmailContext {
	emailCtx := authEmailContext{
		User:         user,
		Lang:         contentlang.Default,
		SupportEmail: uc.supportEmail,
	}
	if strings.TrimSpace(emailCtx.SupportEmail) == "" {
		emailCtx.SupportEmail = defaultSupportEmail
	}

	if strings.TrimSpace(user.ID) != "" {
		account, err := uc.repo.GetAccount(ctx, user.ID)
		if err == nil {
			emailCtx.Account = &account
			if strings.TrimSpace(emailCtx.User.Username) == "" {
				emailCtx.User.Username = account.Username
			}
			if strings.TrimSpace(emailCtx.User.Email) == "" {
				emailCtx.User.Email = account.Email
			}
			if strings.TrimSpace(emailCtx.User.Role) == "" {
				emailCtx.User.Role = account.Role
			}
		}
	}

	emailCtx.Lang = preferredEmailLang(emailCtx.Account)
	emailCtx.Name = localizedDisplayName(emailCtx.User, emailCtx.Account, emailCtx.Lang)

	return emailCtx
}

func preferredEmailLang(account *entity.UserAccount) string {
	if account == nil {
		return contentlang.Default
	}
	if lang, ok := normalizeExplicitEmailLang(account.Preferences.PreferredUILang); ok {
		return lang
	}
	if lang, ok := normalizeExplicitEmailLang(account.Preferences.PreferredContentLang); ok {
		return lang
	}

	return contentlang.Default
}

func normalizeExplicitEmailLang(value string) (string, bool) {
	if strings.TrimSpace(value) == "" {
		return "", false
	}
	lang, err := contentlang.Normalize(value)
	if err != nil {
		return "", false
	}

	return lang, true
}

func localizedDisplayName(user entity.User, account *entity.UserAccount, lang string) string {
	if account != nil && account.Profile.DisplayName != nil {
		displayName := strings.TrimSpace(*account.Profile.DisplayName)
		if displayName != "" {
			return displayName
		}
	}

	username := strings.TrimSpace(user.Username)
	if username == "" && account != nil {
		username = strings.TrimSpace(account.Username)
	}
	if username != "" {
		return username
	}

	switch lang {
	case contentlang.English:
		return "Surau friend"
	case contentlang.Arabic:
		return "صديق Surau"
	default:
		return "Sahabat Surau"
	}
}

func localizedGreeting(lang, name string) string {
	if lang == contentlang.Arabic {
		return "السلام عليكم، " + name
	}

	return "Assalamu'alaikum, " + name
}

func newAuthEmailContent(
	emailCtx authEmailContext,
	subject string,
	preview string,
	title string,
	body string,
	buttonLabel string,
	buttonURL string,
	note string,
	footer string,
	text string,
) authEmailContent {
	chrome := authEmailChromeFor(emailCtx.Lang)
	view := authEmailView{
		Lang:         emailCtx.Lang,
		Badge:        chrome.Badge,
		Preview:      preview,
		Title:        title,
		Greeting:     localizedGreeting(emailCtx.Lang, emailCtx.Name),
		Body:         body,
		ButtonLabel:  buttonLabel,
		ButtonURL:    buttonURL,
		Note:         note,
		Footer:       footer,
		SupportEmail: emailCtx.SupportEmail,
	}

	return authEmailContent{
		Subject: subject,
		View:    view,
		Text:    authEmailText(text, emailCtx),
	}
}

func verificationEmailContent(emailCtx authEmailContext, link string, ttl time.Duration) authEmailContent {
	duration := humanDurationText(ttl, emailCtx.Lang)
	switch emailCtx.Lang {
	case contentlang.English:
		text := fmt.Sprintf(
			"%s\n\nConfirm this email address so your Surau account is ready to use:\n%s\n\nThis verification link expires in %s.\n\nIf you did not create a Surau account, you can ignore this email.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			link,
			duration,
		)

		return newAuthEmailContent(
			emailCtx,
			"Verify your Surau email",
			"Verify your Surau email to finish setting up your account.",
			"Verify your email",
			"Confirm this email address so your Surau account is ready to use.",
			"Verify email",
			link,
			"This verification link expires in "+duration+".",
			"If you did not create a Surau account, you can ignore this email.",
			text,
		)
	case contentlang.Arabic:
		text := fmt.Sprintf(
			"%s\n\nأكد هذا البريد الإلكتروني ليصبح حسابك في Surau جاهزا:\n%s\n\nتنتهي صلاحية رابط التأكيد خلال %s.\n\nإذا لم تنشئ حسابا في Surau، يمكنك تجاهل هذه الرسالة.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			link,
			duration,
		)

		return newAuthEmailContent(
			emailCtx,
			"تأكيد بريدك في Surau",
			"أكمل تأكيد البريد ليصبح حسابك في Surau جاهزا.",
			"تأكيد البريد الإلكتروني",
			"أكد هذا البريد الإلكتروني ليصبح حسابك في Surau جاهزا.",
			"تأكيد البريد",
			link,
			"تنتهي صلاحية رابط التأكيد خلال "+duration+".",
			"إذا لم تنشئ حسابا في Surau، يمكنك تجاهل هذه الرسالة.",
			text,
		)
	default:
		text := fmt.Sprintf(
			"%s\n\nKonfirmasi alamat email ini agar akun Surau Anda siap digunakan:\n%s\n\nLink verifikasi ini berlaku selama %s.\n\nJika Anda tidak membuat akun Surau, abaikan email ini.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			link,
			duration,
		)

		return newAuthEmailContent(
			emailCtx,
			"Verifikasi email Surau",
			"Selesaikan verifikasi email agar akun Surau Anda siap digunakan.",
			"Verifikasi email",
			"Konfirmasi alamat email ini agar akun Surau Anda siap digunakan.",
			"Verifikasi email",
			link,
			"Link verifikasi ini berlaku selama "+duration+".",
			"Jika Anda tidak membuat akun Surau, abaikan email ini.",
			text,
		)
	}
}

func passwordResetEmailContent(emailCtx authEmailContext, link string, ttl time.Duration) authEmailContent {
	duration := humanDurationText(ttl, emailCtx.Lang)
	switch emailCtx.Lang {
	case contentlang.English:
		text := fmt.Sprintf(
			"%s\n\nWe received a request to reset the password for your Surau account:\n%s\n\nThis password reset link expires in %s.\n\nIf you did not request this, you can safely ignore this email.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			link,
			duration,
		)

		return newAuthEmailContent(
			emailCtx,
			"Reset your Surau password",
			"Use this secure link to reset your Surau password.",
			"Reset your password",
			"We received a request to reset the password for your Surau account.",
			"Reset password",
			link,
			"This password reset link expires in "+duration+".",
			"If you did not request this, you can safely ignore this email.",
			text,
		)
	case contentlang.Arabic:
		text := fmt.Sprintf(
			"%s\n\nوصلنا طلب لإعادة تعيين كلمة مرور حسابك في Surau:\n%s\n\nتنتهي صلاحية رابط إعادة التعيين خلال %s.\n\nإذا لم تطلب ذلك، يمكنك تجاهل هذه الرسالة بأمان.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			link,
			duration,
		)

		return newAuthEmailContent(
			emailCtx,
			"إعادة تعيين كلمة مرور Surau",
			"استخدم هذا الرابط الآمن لإعادة تعيين كلمة مرور Surau.",
			"إعادة تعيين كلمة المرور",
			"وصلنا طلب لإعادة تعيين كلمة مرور حسابك في Surau.",
			"إعادة تعيين كلمة المرور",
			link,
			"تنتهي صلاحية رابط إعادة التعيين خلال "+duration+".",
			"إذا لم تطلب ذلك، يمكنك تجاهل هذه الرسالة بأمان.",
			text,
		)
	default:
		text := fmt.Sprintf(
			"%s\n\nKami menerima permintaan untuk reset password akun Surau Anda:\n%s\n\nLink reset password ini berlaku selama %s.\n\nJika Anda tidak meminta ini, abaikan email ini.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			link,
			duration,
		)

		return newAuthEmailContent(
			emailCtx,
			"Reset password Surau",
			"Gunakan link aman ini untuk reset password Surau Anda.",
			"Reset password",
			"Kami menerima permintaan untuk reset password akun Surau Anda.",
			"Reset password",
			link,
			"Link reset password ini berlaku selama "+duration+".",
			"Jika Anda tidak meminta ini, abaikan email ini.",
			text,
		)
	}
}

func emailChangeVerificationEmailContent(emailCtx authEmailContext, link string, ttl time.Duration) authEmailContent {
	duration := humanDurationText(ttl, emailCtx.Lang)
	switch emailCtx.Lang {
	case contentlang.English:
		text := fmt.Sprintf(
			"%s\n\nConfirm this new email address for your Surau account:\n%s\n\nThis link expires in %s.\n\nIf you did not request this, ignore this email and keep your current email.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			link,
			duration,
		)

		return newAuthEmailContent(
			emailCtx,
			"Confirm your new Surau email",
			"Confirm this email address before it becomes your Surau login email.",
			"Confirm new email",
			"Confirm this email address before it becomes your Surau login email.",
			"Confirm email",
			link,
			"This link expires in "+duration+".",
			"If you did not request this, ignore this email and keep your current email.",
			text,
		)
	case contentlang.Arabic:
		text := fmt.Sprintf(
			"%s\n\nأكد هذا البريد الإلكتروني الجديد لحسابك في Surau:\n%s\n\nتنتهي صلاحية الرابط خلال %s.\n\nإذا لم تطلب ذلك، فتجاهل هذه الرسالة وسيبقى بريدك الحالي كما هو.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			link,
			duration,
		)

		return newAuthEmailContent(
			emailCtx,
			"تأكيد بريد Surau الجديد",
			"أكد هذا البريد قبل أن يصبح بريد الدخول إلى Surau.",
			"تأكيد البريد الجديد",
			"أكد هذا البريد قبل أن يصبح بريد الدخول إلى Surau.",
			"تأكيد البريد",
			link,
			"تنتهي صلاحية الرابط خلال "+duration+".",
			"إذا لم تطلب ذلك، فتجاهل هذه الرسالة وسيبقى بريدك الحالي كما هو.",
			text,
		)
	default:
		text := fmt.Sprintf(
			"%s\n\nKonfirmasi email baru untuk akun Surau Anda:\n%s\n\nLink ini berlaku selama %s.\n\nJika Anda tidak meminta ini, abaikan email ini dan email saat ini tetap digunakan.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			link,
			duration,
		)

		return newAuthEmailContent(
			emailCtx,
			"Konfirmasi email baru Surau",
			"Konfirmasi alamat email ini sebelum menjadi email login Surau Anda.",
			"Konfirmasi email baru",
			"Konfirmasi alamat email ini sebelum menjadi email login Surau Anda.",
			"Konfirmasi email",
			link,
			"Link ini berlaku selama "+duration+".",
			"Jika Anda tidak meminta ini, abaikan email ini dan email saat ini tetap digunakan.",
			text,
		)
	}
}

func passwordChangedEmailContent(emailCtx authEmailContext) authEmailContent {
	switch emailCtx.Lang {
	case contentlang.English:
		body := "Your Surau account password was just changed."
		note := "If this was not you, reset your password from the login page and contact support."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Your Surau password was changed",
			body,
			"Password changed",
			body,
			"",
			"",
			note,
			"This security email was sent to help protect your account.",
			text,
		)
	case contentlang.Arabic:
		body := "تم تغيير كلمة مرور حسابك في Surau للتو."
		note := "إذا لم تكن أنت من قام بذلك، فأعد تعيين كلمة المرور من صفحة تسجيل الدخول وتواصل مع الدعم."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"تم تغيير كلمة مرور Surau",
			body,
			"تم تغيير كلمة المرور",
			body,
			"",
			"",
			note,
			"أرسلنا هذه الرسالة الأمنية للمساعدة في حماية حسابك.",
			text,
		)
	default:
		body := "Password akun Surau Anda baru saja diubah."
		note := "Jika ini bukan Anda, segera reset password dari halaman login dan hubungi support."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Password Surau berhasil diubah",
			body,
			"Password berhasil diubah",
			body,
			"",
			"",
			note,
			"Email keamanan ini dikirim untuk membantu melindungi akun Anda.",
			text,
		)
	}
}

func emailVerifiedEmailContent(emailCtx authEmailContext) authEmailContent {
	summary := emailVerifiedPreferenceSummary(emailCtx)
	switch emailCtx.Lang {
	case contentlang.English:
		body := "Your Surau email is verified and your account is ready to use."
		note := "Thank you for keeping your account secure."
		if summary != "" {
			note += " " + summary
		}
		text := fmt.Sprintf(
			"%s\n\n%s\n\n%s\n\nIf you did not verify this email, contact support.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			body,
			note,
		)

		return newAuthEmailContent(
			emailCtx,
			"Your Surau email was verified",
			body,
			"Email verified",
			body,
			"",
			"",
			note,
			"If you did not verify this email, contact support.",
			text,
		)
	case contentlang.Arabic:
		body := "تم تأكيد بريد حسابك في Surau وأصبح الحساب جاهزا للاستخدام."
		note := "شكرا لك على الحفاظ على أمان حسابك."
		if summary != "" {
			note += " " + summary
		}
		text := fmt.Sprintf(
			"%s\n\n%s\n\n%s\n\nإذا لم تقم بتأكيد هذا البريد، فتواصل مع الدعم.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			body,
			note,
		)

		return newAuthEmailContent(
			emailCtx,
			"تم تأكيد بريد Surau",
			body,
			"تم تأكيد البريد",
			body,
			"",
			"",
			note,
			"إذا لم تقم بتأكيد هذا البريد، فتواصل مع الدعم.",
			text,
		)
	default:
		body := "Email akun Surau Anda sudah diverifikasi dan akun siap digunakan."
		note := "Terima kasih sudah menjaga keamanan akun."
		if summary != "" {
			note += " " + summary
		}
		text := fmt.Sprintf(
			"%s\n\n%s\n\n%s\n\nJika Anda tidak melakukan verifikasi ini, segera hubungi support.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			body,
			note,
		)

		return newAuthEmailContent(
			emailCtx,
			"Email Surau berhasil diverifikasi",
			body,
			"Email berhasil diverifikasi",
			body,
			"",
			"",
			note,
			"Jika Anda tidak melakukan verifikasi ini, segera hubungi support.",
			text,
		)
	}
}

func roleChangedEmailContent(emailCtx authEmailContext) authEmailContent {
	role := strings.TrimSpace(emailCtx.User.Role)
	if role == "" {
		role = localizedRoleUpdated(emailCtx.Lang)
	}

	switch emailCtx.Lang {
	case contentlang.English:
		body := fmt.Sprintf("Your Surau account role was changed to %s.", role)
		note := "If you do not recognize this change, contact support."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Your Surau account role changed",
			"Your Surau account role was just changed.",
			"Account role changed",
			body,
			"",
			"",
			note,
			"This security email was sent because account permissions changed.",
			text,
		)
	case contentlang.Arabic:
		body := fmt.Sprintf("تم تغيير دور حسابك في Surau إلى %s.", role)
		note := "إذا لم تتعرف على هذا التغيير، فتواصل مع الدعم."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"تغير دور حساب Surau",
			"تم تغيير دور حسابك في Surau للتو.",
			"تغير دور الحساب",
			body,
			"",
			"",
			note,
			"أرسلنا هذه الرسالة الأمنية لأن صلاحيات الحساب تغيرت.",
			text,
		)
	default:
		body := fmt.Sprintf("Role akun Surau Anda berubah menjadi %s.", role)
		note := "Jika perubahan ini tidak Anda kenali, segera hubungi support."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Role akun Surau berubah",
			"Role akun Surau Anda baru saja berubah.",
			"Role akun berubah",
			body,
			"",
			"",
			note,
			"Email keamanan ini dikirim karena izin akun berubah.",
			text,
		)
	}
}

func emailChangedEmailContent(emailCtx authEmailContext, oldEmail, newEmail string) authEmailContent {
	oldEmail = strings.TrimSpace(oldEmail)
	newEmail = strings.TrimSpace(newEmail)
	switch emailCtx.Lang {
	case contentlang.English:
		body := "The email address for your Surau account was changed."
		note := fmt.Sprintf("Old email: %s. New email: %s. If this was not you, contact support immediately.", oldEmail, newEmail)
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Your Surau email was changed",
			body,
			"Email changed",
			body,
			"",
			"",
			note,
			"This security email was sent because your login email changed.",
			text,
		)
	case contentlang.Arabic:
		body := "تم تغيير البريد الإلكتروني لحسابك في Surau."
		note := fmt.Sprintf("البريد القديم: %s. البريد الجديد: %s. إذا لم تكن أنت من قام بذلك، فتواصل مع الدعم فورا.", oldEmail, newEmail)
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"تم تغيير بريد Surau",
			body,
			"تم تغيير البريد",
			body,
			"",
			"",
			note,
			"أرسلنا هذه الرسالة الأمنية لأن بريد تسجيل الدخول تغير.",
			text,
		)
	default:
		body := "Email akun Surau Anda sudah berubah."
		note := fmt.Sprintf("Email lama: %s. Email baru: %s. Jika ini bukan Anda, segera hubungi support.", oldEmail, newEmail)
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Email Surau berhasil diubah",
			body,
			"Email berhasil diubah",
			body,
			"",
			"",
			note,
			"Email keamanan ini dikirim karena email login akun berubah.",
			text,
		)
	}
}

func accountDeletedEmailContent(emailCtx authEmailContext) authEmailContent {
	switch emailCtx.Lang {
	case contentlang.English:
		body := "Your Surau account was deleted."
		note := "If this was not you, contact support immediately."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Your Surau account was deleted",
			body,
			"Account deleted",
			body,
			"",
			"",
			note,
			"This is a final security notification for the deleted account.",
			text,
		)
	case contentlang.Arabic:
		body := "تم حذف حسابك في Surau."
		note := "إذا لم تكن أنت من قام بذلك، فتواصل مع الدعم فورا."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"تم حذف حساب Surau",
			body,
			"تم حذف الحساب",
			body,
			"",
			"",
			note,
			"هذه رسالة أمان أخيرة للحساب المحذوف.",
			text,
		)
	default:
		body := "Akun Surau Anda sudah dihapus."
		note := "Jika ini bukan Anda, segera hubungi support."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Akun Surau berhasil dihapus",
			body,
			"Akun berhasil dihapus",
			body,
			"",
			"",
			note,
			"Ini adalah notifikasi keamanan terakhir untuk akun yang dihapus.",
			text,
		)
	}
}

func newLoginEmailContent(emailCtx authEmailContext, details string) authEmailContent {
	switch emailCtx.Lang {
	case contentlang.English:
		body := "A new login to your Surau account was detected."
		note := details + " If this was not you, change your password immediately."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"New login to your Surau account",
			body,
			"New login detected",
			body,
			"",
			"",
			note,
			"This email is only sent for an IP/device combination we have not seen before.",
			text,
		)
	case contentlang.Arabic:
		body := "رصدنا تسجيل دخول جديدا إلى حسابك في Surau."
		note := details + " إذا لم تكن أنت من قام بذلك، فغير كلمة المرور فورا."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"تسجيل دخول جديد إلى Surau",
			body,
			"تم رصد تسجيل دخول جديد",
			body,
			"",
			"",
			note,
			"نرسل هذه الرسالة فقط عند ظهور تركيبة IP/جهاز لم نرها من قبل.",
			text,
		)
	default:
		body := "Ada login baru ke akun Surau Anda."
		note := details + " Jika ini bukan Anda, segera ubah password."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Login baru ke akun Surau",
			body,
			"Login baru terdeteksi",
			body,
			"",
			"",
			note,
			"Email ini hanya dikirim untuk kombinasi IP/perangkat yang belum pernah terlihat sebelumnya.",
			text,
		)
	}
}

func failedLoginEmailContent(emailCtx authEmailContext) authEmailContent {
	switch emailCtx.Lang {
	case contentlang.English:
		body := "We limited login attempts to your Surau account because there were too many tries."
		note := "If this was not you, reset your password from the login page."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Surau login attempts were limited",
			"Login attempts to your Surau account are currently limited.",
			"Login attempts limited",
			body,
			"",
			"",
			note,
			"This notification is rate-limited so it does not interrupt you too often.",
			text,
		)
	case contentlang.Arabic:
		body := "قمنا بتقييد محاولات تسجيل الدخول إلى حسابك في Surau بسبب كثرة المحاولات."
		note := "إذا لم تكن أنت من قام بذلك، فأعد تعيين كلمة المرور من صفحة تسجيل الدخول."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"تم تقييد محاولات دخول Surau",
			"محاولات تسجيل الدخول إلى حسابك في Surau مقيدة حاليا.",
			"تم تقييد محاولات الدخول",
			body,
			"",
			"",
			note,
			"يتم تحديد تكرار هذا التنبيه حتى لا يزعجك كثيرا.",
			text,
		)
	default:
		body := "Kami membatasi percobaan login ke akun Surau Anda karena terlalu banyak percobaan."
		note := "Jika ini bukan Anda, reset password dari halaman login."
		text := fmt.Sprintf("%s\n\n%s\n\n%s", localizedGreeting(emailCtx.Lang, emailCtx.Name), body, note)

		return newAuthEmailContent(
			emailCtx,
			"Percobaan login Surau dibatasi",
			"Percobaan login ke akun Surau Anda sedang dibatasi.",
			"Percobaan login dibatasi",
			body,
			"",
			"",
			note,
			"Notifikasi ini dibatasi frekuensinya agar tidak mengganggu.",
			text,
		)
	}
}

func authEmailHTML(view authEmailView) string {
	lang := contentlang.MustNormalize(view.Lang)
	dir := emailDirection(lang)
	align := emailTextAlign(lang)
	chrome := authEmailChromeFor(lang)
	preview := html.EscapeString(view.Preview)
	title := html.EscapeString(view.Title)
	badge := html.EscapeString(view.Badge)
	if badge == "" {
		badge = html.EscapeString(chrome.Badge)
	}
	greeting := html.EscapeString(view.Greeting)
	body := html.EscapeString(view.Body)
	note := html.EscapeString(view.Note)
	footer := html.EscapeString(view.Footer)
	supportEmail := normalizeSupportEmail(view.SupportEmail)
	escapedSupportEmail := html.EscapeString(supportEmail)
	supportURL := html.EscapeString("mailto:" + supportEmail)
	actionHTML := ""
	buttonLabel := strings.TrimSpace(view.ButtonLabel)
	buttonURL := strings.TrimSpace(view.ButtonURL)
	if buttonLabel != "" && buttonURL != "" {
		escapedButtonLabel := html.EscapeString(buttonLabel)
		escapedButtonURL := html.EscapeString(buttonURL)
		actionHTML = fmt.Sprintf(`
	                  <tr>
	                    <td style="padding:26px 32px 0 32px;text-align:%s;">
	                      <a href="%s" style="display:inline-block;border-radius:14px;background:#6f9368;color:#ffffff;font-size:15px;font-weight:700;line-height:20px;text-decoration:none;padding:14px 18px;">%s</a>
	                    </td>
	                  </tr>
	                  <tr>
	                    <td style="padding:24px 32px 0 32px;text-align:%s;">
	                      <p style="margin:0;color:#706d64;font-size:13px;line-height:21px;">%s</p>
	                      <p style="margin:8px 0 0 0;word-break:break-all;color:#52794d;font-size:13px;line-height:21px;"><a href="%s" style="color:#52794d;text-decoration:underline;">%s</a></p>
	                    </td>
	                  </tr>`,
			align,
			escapedButtonURL,
			escapedButtonLabel,
			align,
			html.EscapeString(chrome.CopyLinkInstruction),
			escapedButtonURL,
			escapedButtonURL,
		)
	}
	noteHTML := ""
	if note != "" {
		noteHTML = fmt.Sprintf(`
	                  <tr>
	                    <td style="padding:24px 32px 0 32px;text-align:%s;">
	                      <p style="margin:0;padding:14px 16px;border-radius:16px;background:#f6f5ef;color:#5f5d55;font-size:14px;line-height:22px;">%s</p>
	                    </td>
	                  </tr>`, align, note)
	}
	footerNoteHTML := ""
	if footer != "" {
		footerNoteHTML = fmt.Sprintf(
			`<p style="margin:0;color:#706d64;font-size:12px;line-height:20px;">%s</p>`,
			footer,
		)
	}
	socialHTML := authEmailSocialLinksHTML()

	return fmt.Sprintf(`<!doctype html>
<html lang="%s" dir="%s">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="color-scheme" content="light">
    <meta name="supported-color-schemes" content="light">
    <title>%s</title>
  </head>
  <body style="margin:0;padding:0;background:#f6f5ef;color:#25241f;font-family:Inter, Arial, sans-serif;-webkit-font-smoothing:antialiased;direction:%s;">
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
	                    <td style="padding:32px 32px 12px 32px;text-align:%s;">
	                      <div style="display:inline-block;padding:6px 10px;border-radius:999px;background:#eef5ed;color:#52794d;font-size:12px;font-weight:600;line-height:16px;">%s</div>
	                      <h1 style="margin:18px 0 0 0;color:#25241f;font-size:28px;font-weight:750;line-height:34px;letter-spacing:0;">%s</h1>
	                    </td>
	                  </tr>
	                  <tr>
	                    <td style="padding:4px 32px 0 32px;text-align:%s;">
	                      <p style="margin:0;color:#5f5d55;font-size:15px;line-height:24px;">%s</p>
	                    </td>
	                  </tr>
	                  <tr>
	                    <td style="padding:14px 32px 0 32px;text-align:%s;">
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
	              <td style="padding:16px 4px 0 4px;text-align:center;">
	                %s
	                <p style="margin:12px 0 8px 0;color:#706d64;font-size:12px;line-height:20px;">%s</p>
	                <table role="presentation" cellpadding="0" cellspacing="0" align="center" style="border-collapse:collapse;margin:0 auto;">
	                  <tr>%s</tr>
	                </table>
	                <p style="margin:10px 0 0 0;color:#706d64;font-size:12px;line-height:20px;">%s <a href="%s" style="color:#52794d;text-decoration:underline;">%s</a></p>
	              </td>
	            </tr>
	          </table>
        </td>
      </tr>
    </table>
  </body>
</html>`,
		lang,
		dir,
		title,
		dir,
		preview,
		align,
		badge,
		title,
		align,
		greeting,
		align,
		body,
		actionHTML,
		noteHTML,
		footerNoteHTML,
		html.EscapeString(chrome.FooterFollow),
		socialHTML,
		html.EscapeString(chrome.FooterSupport),
		supportURL,
		escapedSupportEmail,
	)
}

func authEmailSocialLinksHTML() string {
	var builder strings.Builder
	for _, link := range surauSocialLinks {
		builder.WriteString(fmt.Sprintf(
			`<td style="padding:0 5px;"><a href="%s" style="display:inline-block;text-decoration:none;"><img src="%s" width="24" height="24" alt="%s" style="display:block;border:0;width:24px;height:24px;"></a></td>`,
			html.EscapeString(link.URL),
			html.EscapeString(link.IconURL),
			html.EscapeString(link.Label),
		))
	}

	return builder.String()
}

func authEmailText(text string, emailCtx authEmailContext) string {
	text = strings.TrimSpace(text)
	footer := authEmailTextFooter(emailCtx.Lang, emailCtx.SupportEmail)
	if text == "" {
		return footer
	}

	return text + "\n\n" + footer
}

func authEmailTextFooter(lang, supportEmail string) string {
	chrome := authEmailChromeFor(lang)
	var builder strings.Builder
	builder.WriteString(chrome.TextFooterIntro)
	for _, link := range surauSocialLinks {
		builder.WriteString("\n")
		builder.WriteString(link.Label)
		builder.WriteString(": ")
		builder.WriteString(link.URL)
	}
	builder.WriteString("\n")
	builder.WriteString(chrome.FooterSupport)
	builder.WriteString(" ")
	builder.WriteString(normalizeSupportEmail(supportEmail))

	return builder.String()
}

func authEmailChromeFor(lang string) authEmailChrome {
	switch lang {
	case contentlang.English:
		return authEmailChrome{
			Badge:               "Account Security",
			CopyLinkInstruction: "If the button does not work, copy and paste this link into your browser:",
			FooterFollow:        "Follow Surau",
			FooterSupport:       "Need help?",
			TextFooterIntro:     "Follow Surau:",
		}
	case contentlang.Arabic:
		return authEmailChrome{
			Badge:               "أمان الحساب",
			CopyLinkInstruction: "إذا لم يعمل الزر، انسخ هذا الرابط والصقه في المتصفح:",
			FooterFollow:        "تابع Surau",
			FooterSupport:       "تحتاج مساعدة؟",
			TextFooterIntro:     "تابع Surau:",
		}
	default:
		return authEmailChrome{
			Badge:               "Keamanan Akun",
			CopyLinkInstruction: "Jika tombol tidak berfungsi, salin link ini ke browser:",
			FooterFollow:        "Ikuti Surau",
			FooterSupport:       "Butuh bantuan?",
			TextFooterIntro:     "Ikuti Surau:",
		}
	}
}

func emailDirection(lang string) string {
	if lang == contentlang.Arabic {
		return "rtl"
	}

	return "ltr"
}

func emailTextAlign(lang string) string {
	if lang == contentlang.Arabic {
		return "right"
	}

	return "left"
}

func emailVerifiedPreferenceSummary(emailCtx authEmailContext) string {
	if emailCtx.Account == nil ||
		emailCtx.Account.Profile.OnboardingCompletedAt == nil ||
		!emailCtx.Account.Profile.PersonalizationEnabled {
		return ""
	}

	preferences := emailCtx.Account.Preferences
	contentLang := localizedContentLangLabel(preferences.PreferredContentLang, emailCtx.Lang)
	readerMode := localizedReaderModeLabel(preferences.ReaderMode, emailCtx.Lang)
	switch emailCtx.Lang {
	case contentlang.English:
		summary := fmt.Sprintf(
			"Your reading preferences are active: content language %s and reader mode %s.",
			contentLang,
			readerMode,
		)
		if preferences.DailyGoalMinutes != nil {
			summary += fmt.Sprintf(" Daily goal: %d minutes.", *preferences.DailyGoalMinutes)
		}

		return summary
	case contentlang.Arabic:
		summary := fmt.Sprintf(
			"تفضيلات القراءة مفعلة: لغة المحتوى %s ووضع القراءة %s.",
			contentLang,
			readerMode,
		)
		if preferences.DailyGoalMinutes != nil {
			summary += fmt.Sprintf(" الهدف اليومي: %d دقيقة.", *preferences.DailyGoalMinutes)
		}

		return summary
	default:
		summary := fmt.Sprintf(
			"Preferensi baca Anda sudah aktif: bahasa konten %s dan mode baca %s.",
			contentLang,
			readerMode,
		)
		if preferences.DailyGoalMinutes != nil {
			summary += fmt.Sprintf(" Target harian: %d menit.", *preferences.DailyGoalMinutes)
		}

		return summary
	}
}

func localizedContentLangLabel(lang, displayLang string) string {
	normalized := contentlang.MustNormalize(lang)
	switch displayLang {
	case contentlang.English:
		switch normalized {
		case contentlang.Arabic:
			return "Arabic"
		case contentlang.English:
			return "English"
		default:
			return "Indonesian"
		}
	case contentlang.Arabic:
		switch normalized {
		case contentlang.Arabic:
			return "العربية"
		case contentlang.English:
			return "الإنجليزية"
		default:
			return "الإندونيسية"
		}
	default:
		switch normalized {
		case contentlang.Arabic:
			return "Arab"
		case contentlang.English:
			return "Inggris"
		default:
			return "Indonesia"
		}
	}
}

func localizedReaderModeLabel(mode, displayLang string) string {
	switch displayLang {
	case contentlang.English:
		switch mode {
		case entity.UserReaderModeArabicOnly:
			return "Arabic only"
		case entity.UserReaderModeTranslationOnly:
			return "translation only"
		default:
			return "Arabic + translation"
		}
	case contentlang.Arabic:
		switch mode {
		case entity.UserReaderModeArabicOnly:
			return "العربية فقط"
		case entity.UserReaderModeTranslationOnly:
			return "الترجمة فقط"
		default:
			return "العربية مع الترجمة"
		}
	default:
		switch mode {
		case entity.UserReaderModeArabicOnly:
			return "Arab saja"
		case entity.UserReaderModeTranslationOnly:
			return "terjemahan saja"
		default:
			return "Arab + terjemahan"
		}
	}
}

func localizedRoleUpdated(lang string) string {
	switch lang {
	case contentlang.English:
		return "updated"
	case contentlang.Arabic:
		return "محدث"
	default:
		return "diperbarui"
	}
}

func localizedLoginDetails(emailCtx authEmailContext, eventTime time.Time, clientIP, device string) string {
	timeText, timezoneName := localizedEmailTime(emailCtx, eventTime)
	if strings.TrimSpace(clientIP) == "" {
		clientIP = localizedUnknown(emailCtx.Lang)
	}
	if strings.TrimSpace(device) == "" || device == "unknown" {
		device = localizedUnknown(emailCtx.Lang)
	}

	switch emailCtx.Lang {
	case contentlang.English:
		return fmt.Sprintf("Time: %s %s. IP: %s. Device: %s.", timeText, timezoneName, clientIP, device)
	case contentlang.Arabic:
		return fmt.Sprintf("الوقت: %s %s. عنوان IP: %s. الجهاز: %s.", timeText, timezoneName, clientIP, device)
	default:
		return fmt.Sprintf("Waktu: %s %s. IP: %s. Perangkat: %s.", timeText, timezoneName, clientIP, device)
	}
}

func localizedEmailTime(emailCtx authEmailContext, eventTime time.Time) (string, string) {
	location := time.UTC
	timezoneName := "UTC"
	if emailCtx.Account != nil && emailCtx.Account.Profile.Timezone != nil {
		candidate := strings.TrimSpace(*emailCtx.Account.Profile.Timezone)
		if candidate != "" {
			if loadedLocation, err := time.LoadLocation(candidate); err == nil {
				location = loadedLocation
				timezoneName = candidate
			}
		}
	}

	return eventTime.In(location).Format("2006-01-02 15:04:05"), timezoneName
}

func localizedUnknown(lang string) string {
	switch lang {
	case contentlang.English:
		return "unknown"
	case contentlang.Arabic:
		return "غير معروف"
	default:
		return "tidak diketahui"
	}
}

func normalizeSupportEmail(value string) string {
	value = strings.TrimSpace(value)
	if validEmail(value) {
		return value
	}

	return defaultSupportEmail
}

func humanDurationText(duration time.Duration, lang string) string {
	if duration%time.Hour == 0 {
		hours := int(duration / time.Hour)
		switch lang {
		case contentlang.English:
			if hours == 1 {
				return "1 hour"
			}

			return fmt.Sprintf("%d hours", hours)
		case contentlang.Arabic:
			if hours == 1 {
				return "ساعة واحدة"
			}

			return fmt.Sprintf("%d ساعات", hours)
		default:
			if hours == 1 {
				return "1 jam"
			}

			return fmt.Sprintf("%d jam", hours)
		}
	}
	if duration%time.Minute == 0 {
		minutes := int(duration / time.Minute)
		switch lang {
		case contentlang.English:
			if minutes == 1 {
				return "1 minute"
			}

			return fmt.Sprintf("%d minutes", minutes)
		case contentlang.Arabic:
			if minutes == 1 {
				return "دقيقة واحدة"
			}

			return fmt.Sprintf("%d دقيقة", minutes)
		default:
			if minutes == 1 {
				return "1 menit"
			}

			return fmt.Sprintf("%d menit", minutes)
		}
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

func hashEmailChangeToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	rawTokenBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(rawTokenBytes) != emailChangeTokenBytes {
		return "", entity.ErrInvalidEmailChangeToken
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
