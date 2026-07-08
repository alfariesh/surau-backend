package user

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/contentlang"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/policy"
	"github.com/alfariesh/surau-backend/internal/usecase/authmeta"
	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// A-3: MFA (TOTP) + one-time recovery codes + step-up. The whole feature is
// gated on Options.MFA/MFABox being wired; when absent every account keeps
// the exact pre-MFA behavior.

const (
	authEventMFAEnroll        = "mfa_enroll"
	authEventMFAEnrollConfirm = "mfa_enroll_confirm"
	authEventMFAVerify        = "mfa_verify"
	authEventMFAStepUp        = "mfa_step_up"
	authEventMFADisable       = "mfa_disable"
	authEventMFARecoveryRegen = "mfa_recovery_regenerate"
	authEventMFAResetRequest  = "mfa_reset_request"
	authEventMFAResetConfirm  = "mfa_reset_confirm"
)

const (
	mfaRecoveryCodeCount = 10
	// mfaRecoveryCodeBytes yields 16 base32 characters (80 bits) per code.
	mfaRecoveryCodeBytes = 10
	mfaRecoveryCodeGroup = 4
	// mfaChallengeTokenBytes matches the refresh-token entropy.
	mfaChallengeTokenBytes = 32
	// totpPeriodSeconds is the RFC 6238 default shared with authenticator apps.
	totpPeriodSeconds = 30
	// totpSkewSteps accepts one step of clock drift either side.
	totpSkewSteps = 1
)

const (
	defaultMFAStepUpTTL       = 10 * time.Minute
	defaultMFAEnrollmentGrace = 168 * time.Hour
	defaultMFAChallengeTTL    = 5 * time.Minute
	defaultMFAResetTTL        = 15 * time.Minute
)

// recoveryCodeEncoding is unpadded base32 (RFC 4648) — readable, no ambiguity
// after uppercasing.
func recoveryCodeEncoding() *base32.Encoding {
	return base32.StdEncoding.WithPadding(base32.NoPadding)
}

// MFAOptions tunes the A-3 feature; zero values take the documented defaults.
type MFAOptions struct {
	// StepUpTTL is how long a step-up (or MFA login) keeps a session "fresh"
	// for destructive routes.
	StepUpTTL time.Duration
	// EnrollmentGrace is how long an MFA-mandated role may operate without
	// enrolling, counted from users.mfa_enforced_from (AC-1).
	EnrollmentGrace time.Duration
	// ChallengeTTL bounds the login challenge (password success -> code).
	ChallengeTTL time.Duration
	// ResetTTL bounds the lost-device reset challenge and its emailed OTP.
	ResetTTL time.Duration
	// TOTPIssuer names the account in authenticator apps.
	TOTPIssuer string
}

func normalizeMFAOptions(opts MFAOptions) MFAOptions {
	if opts.StepUpTTL <= 0 {
		opts.StepUpTTL = defaultMFAStepUpTTL
	}

	if opts.EnrollmentGrace <= 0 {
		opts.EnrollmentGrace = defaultMFAEnrollmentGrace
	}

	if opts.ChallengeTTL <= 0 {
		opts.ChallengeTTL = defaultMFAChallengeTTL
	}

	if opts.ResetTTL <= 0 {
		opts.ResetTTL = defaultMFAResetTTL
	}

	if strings.TrimSpace(opts.TOTPIssuer) == "" {
		opts.TOTPIssuer = "Surau"
	}

	return opts
}

// mfaActive reports whether the MFA feature is wired at all.
func (uc *UseCase) mfaActive() bool {
	return uc.mfa != nil && uc.mfaBox != nil
}

// StartMFAEnrollment provisions a pending TOTP secret and returns the
// material the FE renders as a QR (otpauth URL) + manual-entry secret.
func (uc *UseCase) StartMFAEnrollment(ctx context.Context, userID string) (enrollment entity.MFAEnrollment, err error) {
	defer func() {
		uc.auditAuth(ctx, authEventMFAEnroll, auditStatus(err), userID, "", auditErrorCode(err), nil)
	}()

	if !uc.mfaActive() {
		return entity.MFAEnrollment{}, entity.ErrMFANotEnabled
	}

	user, err := uc.repo.GetByID(ctx, userID)
	if err != nil {
		return entity.MFAEnrollment{}, err
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      uc.mfaOptions.TOTPIssuer,
		AccountName: user.Email,
		Period:      totpPeriodSeconds,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return entity.MFAEnrollment{}, fmt.Errorf("UserUseCase - StartMFAEnrollment - totp.Generate: %w", err)
	}

	sealed, err := uc.mfaBox.Seal([]byte(key.Secret()))
	if err != nil {
		return entity.MFAEnrollment{}, fmt.Errorf("UserUseCase - StartMFAEnrollment - Seal: %w", err)
	}

	if err := uc.mfa.UpsertPendingMFA(ctx, userID, sealed); err != nil {
		return entity.MFAEnrollment{}, err
	}

	return entity.MFAEnrollment{Secret: key.Secret(), OTPAuthURL: key.URL()}, nil
}

// ConfirmMFAEnrollment activates a pending enrollment after the user proves
// the authenticator works, returns the one-time recovery codes (shown exactly
// once), and stamps the current session fresh so the admin can keep working.
func (uc *UseCase) ConfirmMFAEnrollment(
	ctx context.Context,
	userID, familyID, code string,
) (recoveryCodes []string, err error) {
	defer func() {
		uc.auditAuth(ctx, authEventMFAEnrollConfirm, auditStatus(err), userID, "", auditErrorCode(err), nil)
	}()

	if !uc.mfaActive() {
		return nil, entity.ErrMFANotEnabled
	}

	if err := uc.enforceAuthRateLimit(ctx, authEventMFAEnrollConfirm, []rateLimitCheck{
		{keyType: rateLimitKeyTypeUser, value: userID, rule: uc.rateLimit.MFAStepUpUser},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.MFAStepUpIP},
	}); err != nil {
		return nil, err
	}

	mfa, err := uc.pendingMFA(ctx, userID)
	if err != nil {
		return nil, err
	}

	if err := uc.verifyTOTP(ctx, &mfa, code); err != nil {
		return nil, err
	}

	if err := uc.mfa.ConfirmMFA(ctx, userID); err != nil {
		return nil, err
	}

	raw, hashes, err := generateRecoveryCodes()
	if err != nil {
		return nil, err
	}

	if err := uc.mfa.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}

	// Confirming just proved a second factor: stamp the session so step-up
	// routes open immediately. Best effort — a missing session only means
	// the next destructive action asks for a code.
	if familyID != "" {
		//nolint:errcheck // best-effort: a missed stamp only means the next destructive action asks for a code
		_ = uc.mfa.SetSessionMFAVerified(ctx, userID, familyID, time.Now().UTC())
	}

	return raw, nil
}

// pendingMFA fetches an enrollment that exists and is not yet confirmed.
func (uc *UseCase) pendingMFA(ctx context.Context, userID string) (entity.UserMFA, error) {
	mfa, err := uc.mfa.GetMFA(ctx, userID)
	if err != nil {
		if errors.Is(err, entity.ErrMFANotEnabled) {
			return entity.UserMFA{}, entity.ErrMFAEnrollmentNotStarted
		}

		return entity.UserMFA{}, err
	}

	if mfa.ConfirmedAt != nil {
		return entity.UserMFA{}, entity.ErrMFAAlreadyEnabled
	}

	return mfa, nil
}

// mfaLoginChallengeFor reports whether login must divert to the second-factor
// step, and creates the short-lived challenge when it must.
func (uc *UseCase) mfaLoginChallengeFor(ctx context.Context, user *entity.User) (entity.LoginResult, bool, error) {
	if !uc.mfaActive() {
		return entity.LoginResult{}, false, nil
	}

	mfa, err := uc.mfa.GetMFA(ctx, user.ID)
	if err != nil {
		if errors.Is(err, entity.ErrMFANotEnabled) {
			return entity.LoginResult{}, false, nil
		}

		return entity.LoginResult{}, false, fmt.Errorf("UserUseCase - mfaLoginChallengeFor - GetMFA: %w", err)
	}

	if mfa.ConfirmedAt == nil {
		return entity.LoginResult{}, false, nil
	}

	rawToken, tokenHash, err := newMFAChallengeToken()
	if err != nil {
		return entity.LoginResult{}, false, err
	}

	now := time.Now().UTC()
	expiresAt := now.Add(uc.mfaOptions.ChallengeTTL)
	meta := authmeta.From(ctx)

	challenge := entity.MFAChallenge{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		Purpose:   entity.MFAChallengePurposeLogin,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
		ClientIP:  strings.TrimSpace(meta.ClientIP),
		UserAgent: truncateRunes(meta.UserAgent, maxEmailUserAgentRunes),
	}
	if err := uc.mfa.CreateMFAChallenge(ctx, challenge); err != nil {
		return entity.LoginResult{}, false, fmt.Errorf("UserUseCase - mfaLoginChallengeFor - CreateMFAChallenge: %w", err)
	}

	return entity.LoginResult{
		User:              *user,
		MFARequired:       true,
		MFAToken:          rawToken,
		MFATokenExpiresAt: expiresAt,
	}, true, nil
}

// VerifyMFALogin completes an MFA login: challenge token + TOTP (or recovery
// code) buys the same token pair a plain login issues. The challenge is
// consumed only on success, atomically.
func (uc *UseCase) VerifyMFALogin(ctx context.Context, mfaToken, code string) (result entity.LoginResult, err error) {
	auditUserID := ""
	defer func() {
		uc.auditAuth(ctx, authEventMFAVerify, auditStatus(err), auditUserID, "", auditErrorCode(err), nil)
	}()

	if !uc.mfaActive() {
		return entity.LoginResult{}, entity.ErrInvalidMFAChallenge
	}

	mfaToken = strings.TrimSpace(mfaToken)
	if mfaToken == "" || len(mfaToken) > maxResetTokenInputBytes {
		return entity.LoginResult{}, entity.ErrInvalidMFAChallenge
	}

	if err := uc.enforceAuthRateLimit(ctx, authEventMFAVerify, []rateLimitCheck{
		{keyType: rateLimitKeyTypeToken, value: mfaToken, rule: uc.rateLimit.MFAVerifyToken},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.MFAVerifyIP},
	}); err != nil {
		return entity.LoginResult{}, err
	}

	challenge, user, mfa, err := uc.loadLoginChallenge(ctx, mfaToken)

	auditUserID = challenge.UserID

	if err != nil {
		return entity.LoginResult{}, err
	}

	if err := uc.verifySecondFactor(ctx, &mfa, code); err != nil {
		return entity.LoginResult{}, err
	}

	// Consume-on-success: a concurrent double-submit of the right code issues
	// exactly one session.
	if err := uc.mfa.ConsumeMFAChallenge(ctx, challenge.ID); err != nil {
		return entity.LoginResult{}, err
	}

	now := time.Now().UTC()
	sessionID := uuid.NewString()

	result, err = uc.issueSessionRow(ctx, &user, sessionID, sessionID, func(session *entity.AuthSession) error {
		if uc.sessions == nil {
			return nil
		}

		// The session is born step-up fresh: the user just proved a factor.
		session.MFAVerifiedAt = &now

		return uc.sessions.CreateAuthSession(ctx, *session)
	})
	if err != nil {
		return entity.LoginResult{}, err
	}

	// The new-device notification belongs to full authentication, so for MFA
	// accounts it fires here instead of at the password step.
	uc.notifyNewLogin(ctx, user)

	return result, nil
}

// loadLoginChallenge resolves a live login challenge to its user and their
// CONFIRMED enrollment; a user or enrollment that vanished since the password
// step reads as a stale challenge.
func (uc *UseCase) loadLoginChallenge(
	ctx context.Context,
	mfaToken string,
) (entity.MFAChallenge, entity.User, entity.UserMFA, error) {
	challenge, err := uc.mfa.GetMFAChallengeByTokenHash(ctx, hashMFAChallengeToken(mfaToken), entity.MFAChallengePurposeLogin)
	if err != nil {
		return entity.MFAChallenge{}, entity.User{}, entity.UserMFA{}, err
	}

	user, err := uc.repo.GetByID(ctx, challenge.UserID)
	if err != nil {
		return challenge, entity.User{}, entity.UserMFA{}, entity.ErrInvalidMFAChallenge
	}

	mfa, err := uc.mfa.GetMFA(ctx, user.ID)
	if err != nil || mfa.ConfirmedAt == nil {
		return challenge, user, entity.UserMFA{}, entity.ErrInvalidMFAChallenge
	}

	return challenge, user, mfa, nil
}

// StepUpMFA re-proves a factor on the CURRENT session so destructive routes
// open for another StepUpTTL window (AC-2).
func (uc *UseCase) StepUpMFA(ctx context.Context, userID, familyID, code string) (expiresAt time.Time, err error) {
	defer func() {
		uc.auditAuth(ctx, authEventMFAStepUp, auditStatus(err), userID, "", auditErrorCode(err), nil)
	}()

	if !uc.mfaActive() {
		return time.Time{}, entity.ErrMFANotEnabled
	}

	if err := uc.enforceAuthRateLimit(ctx, authEventMFAStepUp, []rateLimitCheck{
		{keyType: rateLimitKeyTypeUser, value: userID, rule: uc.rateLimit.MFAStepUpUser},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.MFAStepUpIP},
	}); err != nil {
		return time.Time{}, err
	}

	mfa, err := uc.mfa.GetMFA(ctx, userID)
	if err != nil {
		return time.Time{}, err
	}

	if mfa.ConfirmedAt == nil {
		return time.Time{}, entity.ErrMFANotEnabled
	}

	if err := uc.verifySecondFactor(ctx, &mfa, code); err != nil {
		return time.Time{}, err
	}

	now := time.Now().UTC()
	if err := uc.mfa.SetSessionMFAVerified(ctx, userID, familyID, now); err != nil {
		return time.Time{}, err
	}

	return now.Add(uc.mfaOptions.StepUpTTL), nil
}

// DisableMFA turns MFA off (fresh step-up required), revokes every session
// (stolen tokens die with the second factor), and issues a fresh pair —
// the ChangePassword shape.
func (uc *UseCase) DisableMFA(ctx context.Context, userID, familyID string) (result entity.LoginResult, err error) {
	defer func() {
		uc.auditAuth(ctx, authEventMFADisable, auditStatus(err), userID, "", auditErrorCode(err), nil)
	}()

	if !uc.mfaActive() {
		return entity.LoginResult{}, entity.ErrMFANotEnabled
	}

	if err := uc.requireFreshStepUp(ctx, userID, familyID); err != nil {
		return entity.LoginResult{}, err
	}

	if err := uc.mfa.DeleteMFA(ctx, userID); err != nil {
		return entity.LoginResult{}, err
	}

	if uc.sessions != nil {
		if _, err = uc.sessions.RevokeAllAuthSessions(ctx, userID); err != nil {
			return entity.LoginResult{}, fmt.Errorf("UserUseCase - DisableMFA - RevokeAllAuthSessions: %w", err)
		}
	}

	// Reload: token_version was bumped by the revoke.
	user, err := uc.repo.GetByID(ctx, userID)
	if err != nil {
		return entity.LoginResult{}, err
	}

	result, err = uc.issueSession(ctx, &user)
	if err != nil {
		return entity.LoginResult{}, err
	}

	uc.notifyMFADisabled(ctx, &user)

	return result, nil
}

// RegenerateMFARecoveryCodes replaces the whole recovery set (fresh step-up
// required); the previous codes die immediately.
func (uc *UseCase) RegenerateMFARecoveryCodes(ctx context.Context, userID, familyID string) (codes []string, err error) {
	defer func() {
		uc.auditAuth(ctx, authEventMFARecoveryRegen, auditStatus(err), userID, "", auditErrorCode(err), nil)
	}()

	if !uc.mfaActive() {
		return nil, entity.ErrMFANotEnabled
	}

	if err := uc.requireFreshStepUp(ctx, userID, familyID); err != nil {
		return nil, err
	}

	raw, hashes, err := generateRecoveryCodes()
	if err != nil {
		return nil, err
	}

	if err := uc.mfa.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}

	return raw, nil
}

// MFAStatus is the FE-facing state for the settings screen and login banners.
func (uc *UseCase) MFAStatus(ctx context.Context, userID, familyID string) (entity.MFAStatus, error) {
	if !uc.mfaActive() {
		return entity.MFAStatus{}, nil
	}

	user, err := uc.repo.GetByID(ctx, userID)
	if err != nil {
		return entity.MFAStatus{}, err
	}

	data, err := uc.mfa.GetMFAGateData(ctx, userID, familyID)
	if err != nil {
		return entity.MFAStatus{}, err
	}

	status := entity.MFAStatus{
		Enabled:      data.Confirmed,
		Pending:      data.Pending,
		Required:     policy.RoleRequiresMFA(user.Role),
		EnforcedFrom: data.EnforcedFrom,
	}

	if data.EnforcedFrom != nil {
		graceEnd := data.EnforcedFrom.Add(uc.mfaOptions.EnrollmentGrace)
		status.GraceEndsAt = &graceEnd
	}

	if data.Confirmed && data.MFAVerifiedAt != nil {
		stepUpEnd := data.MFAVerifiedAt.Add(uc.mfaOptions.StepUpTTL)
		status.StepUpVerifiedAt = data.MFAVerifiedAt
		status.StepUpExpiresAt = &stepUpEnd
	}

	if data.Confirmed {
		count, err := uc.mfa.CountUnusedRecoveryCodes(ctx, userID)
		if err != nil {
			return entity.MFAStatus{}, err
		}

		status.RecoveryCodesRemaining = count
	}

	return status, nil
}

// MFAGate is the step-up verdict for destructive routes (AC-1 + AC-2):
// enrolled users must be fresh; MFA-mandated roles must enroll once the grace
// window closes; everyone else passes.
func (uc *UseCase) MFAGate(ctx context.Context, user *entity.User, familyID string) (entity.MFAGateDecision, error) {
	if !uc.mfaActive() {
		return entity.MFAGateAllowed, nil
	}

	data, err := uc.mfa.GetMFAGateData(ctx, user.ID, familyID)
	if err != nil {
		return entity.MFAGateAllowed, err
	}

	now := time.Now().UTC()

	if data.Confirmed {
		if data.MFAVerifiedAt != nil && now.Sub(*data.MFAVerifiedAt) <= uc.mfaOptions.StepUpTTL {
			return entity.MFAGateAllowed, nil
		}

		return entity.MFAGateStepUpRequired, nil
	}

	if !policy.RoleRequiresMFA(user.Role) {
		return entity.MFAGateAllowed, nil
	}

	// A NULL anchor on a mandated role should never happen (migration
	// backfill + role-change stamping). Failing open would silently drop
	// AC-1, so treat it as grace-from-now and surface it in the audit log.
	if data.EnforcedFrom == nil {
		uc.auditAuth(ctx, authEventMFAStepUp, authAuditStatusFailure, user.ID, user.Email, "mfa_enforced_from_missing", nil)

		return entity.MFAGateAllowed, nil
	}

	if now.Sub(*data.EnforcedFrom) <= uc.mfaOptions.EnrollmentGrace {
		return entity.MFAGateAllowed, nil
	}

	return entity.MFAGateEnrollmentRequired, nil
}

// requireFreshStepUp guards user-scoped MFA mutations (disable, regenerate).
func (uc *UseCase) requireFreshStepUp(ctx context.Context, userID, familyID string) error {
	data, err := uc.mfa.GetMFAGateData(ctx, userID, familyID)
	if err != nil {
		return err
	}

	if !data.Confirmed {
		return entity.ErrMFANotEnabled
	}

	if data.MFAVerifiedAt == nil || time.Now().UTC().Sub(*data.MFAVerifiedAt) > uc.mfaOptions.StepUpTTL {
		return entity.ErrMFAStepUpRequired
	}

	return nil
}

// RequestMFAReset starts the lost-device flow from the login-challenge state
// (password already proven): a reset challenge is minted and its 6-digit OTP
// emailed, to be combined with a recovery code (roadmap: email + recovery).
func (uc *UseCase) RequestMFAReset(ctx context.Context, mfaToken string) (resetToken string, expiresAt time.Time, err error) {
	auditUserID := ""
	defer func() {
		uc.auditAuth(ctx, authEventMFAResetRequest, auditStatus(err), auditUserID, "", auditErrorCode(err), nil)
	}()

	if !uc.mfaActive() {
		return "", time.Time{}, entity.ErrInvalidMFAChallenge
	}

	mfaToken = strings.TrimSpace(mfaToken)
	if mfaToken == "" || len(mfaToken) > maxResetTokenInputBytes {
		return "", time.Time{}, entity.ErrInvalidMFAChallenge
	}

	if err := uc.enforceAuthRateLimit(ctx, authEventMFAResetRequest, []rateLimitCheck{
		{keyType: rateLimitKeyTypeToken, value: mfaToken, rule: uc.rateLimit.MFAResetEmail},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.MFAResetIP},
	}); err != nil {
		return "", time.Time{}, err
	}

	// The live login challenge is the proof of password; it stays unconsumed
	// so the user can still complete a normal code login if the phone shows up.
	loginChallenge, err := uc.mfa.GetMFAChallengeByTokenHash(ctx, hashMFAChallengeToken(mfaToken), entity.MFAChallengePurposeLogin)
	if err != nil {
		return "", time.Time{}, err
	}

	auditUserID = loginChallenge.UserID

	user, err := uc.repo.GetByID(ctx, loginChallenge.UserID)
	if err != nil {
		return "", time.Time{}, entity.ErrInvalidMFAChallenge
	}

	return uc.mintAndEmailResetChallenge(ctx, &user)
}

// mintAndEmailResetChallenge creates the purpose=reset challenge and emails
// its OTP (the "email" half of the email+recovery-code combo).
func (uc *UseCase) mintAndEmailResetChallenge(
	ctx context.Context,
	user *entity.User,
) (string, time.Time, error) {
	otpValue, otpHash, err := newOTPHash()
	if err != nil {
		return "", time.Time{}, err
	}

	rawToken, tokenHash, err := newMFAChallengeToken()
	if err != nil {
		return "", time.Time{}, err
	}

	resetExpiresAt := time.Now().UTC().Add(uc.mfaOptions.ResetTTL)
	meta := authmeta.From(ctx)

	challenge := entity.MFAChallenge{
		ID:           uuid.NewString(),
		UserID:       user.ID,
		Purpose:      entity.MFAChallengePurposeReset,
		TokenHash:    tokenHash,
		OTPHash:      &otpHash,
		OTPExpiresAt: &resetExpiresAt,
		ExpiresAt:    resetExpiresAt,
		ClientIP:     strings.TrimSpace(meta.ClientIP),
		UserAgent:    truncateRunes(meta.UserAgent, maxEmailUserAgentRunes),
	}
	if err := uc.mfa.CreateMFAChallenge(ctx, challenge); err != nil {
		return "", time.Time{}, fmt.Errorf("UserUseCase - RequestMFAReset - CreateMFAChallenge: %w", err)
	}

	emailCtx := uc.newAuthEmailContext(ctx, *user)
	if err := uc.sendAuthEmail(
		ctx,
		user.Email,
		entity.EmailTemplateKeyMFAResetOTP,
		user.ID,
		emailCtx.Lang,
		mfaResetEmailVariables(&emailCtx, otpValue, uc.mfaOptions.ResetTTL),
		mfaResetEmailContent(&emailCtx, otpValue, uc.mfaOptions.ResetTTL),
		true,
	); err != nil {
		return "", time.Time{}, entity.ErrEmailDeliveryFailed
	}

	return rawToken, resetExpiresAt, nil
}

// ConfirmMFAReset disables MFA with the email OTP + one recovery code combo
// and revokes every session; the account falls back to password-only login.
func (uc *UseCase) ConfirmMFAReset(ctx context.Context, resetToken, otpCode, recoveryCode string) (err error) {
	auditUserID := ""
	defer func() {
		uc.auditAuth(ctx, authEventMFAResetConfirm, auditStatus(err), auditUserID, "", auditErrorCode(err), nil)
	}()

	if !uc.mfaActive() {
		return entity.ErrInvalidMFAReset
	}

	resetToken = strings.TrimSpace(resetToken)
	if resetToken == "" || len(resetToken) > maxResetTokenInputBytes {
		return entity.ErrInvalidMFAReset
	}

	if err := uc.enforceAuthRateLimit(ctx, authEventMFAResetConfirm, []rateLimitCheck{
		{keyType: rateLimitKeyTypeToken, value: resetToken, rule: uc.rateLimit.MFAResetEmail},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.MFAResetIP},
	}); err != nil {
		return err
	}

	challenge, err := uc.mfa.GetMFAChallengeByTokenHash(ctx, hashMFAChallengeToken(resetToken), entity.MFAChallengePurposeReset)
	if err != nil {
		if errors.Is(err, entity.ErrInvalidMFAChallenge) {
			return entity.ErrInvalidMFAReset
		}

		return err
	}

	auditUserID = challenge.UserID

	// Factor order: OTP checked (non-consuming), then the recovery code is
	// spent atomically, then the challenge closes. A typo'd recovery code
	// costs nothing; a lost consume race after the code is spent can only be
	// the same user double-submitting.
	if !resetOTPMatches(&challenge, otpCode) {
		return entity.ErrInvalidMFAReset
	}

	if err := uc.spendResetFactors(ctx, &challenge, recoveryCode); err != nil {
		return err
	}

	return uc.finalizeMFAReset(ctx, challenge.UserID)
}

// resetOTPMatches checks the emailed OTP against the challenge, expiry-aware.
func resetOTPMatches(challenge *entity.MFAChallenge, otpCode string) bool {
	if challenge.OTPHash == nil || challenge.OTPExpiresAt == nil || time.Now().UTC().After(*challenge.OTPExpiresAt) {
		return false
	}

	normalized, err := validateOTPInput(otpCode)
	if err != nil {
		return false
	}

	return bcrypt.CompareHashAndPassword([]byte(*challenge.OTPHash), []byte(normalized)) == nil
}

// spendResetFactors burns the recovery code then the challenge, atomically
// each, collapsing both failure modes into the anti-oracle reset error.
func (uc *UseCase) spendResetFactors(ctx context.Context, challenge *entity.MFAChallenge, recoveryCode string) error {
	normalizedRecovery, err := normalizeRecoveryCode(recoveryCode)
	if err != nil {
		return entity.ErrInvalidMFAReset
	}

	if err := uc.mfa.ConsumeRecoveryCode(ctx, challenge.UserID, hashTokenBytes([]byte(normalizedRecovery))); err != nil {
		if errors.Is(err, entity.ErrInvalidMFACode) {
			return entity.ErrInvalidMFAReset
		}

		return err
	}

	if err := uc.mfa.ConsumeMFAChallenge(ctx, challenge.ID); err != nil {
		if errors.Is(err, entity.ErrInvalidMFAChallenge) {
			return entity.ErrInvalidMFAReset
		}

		return err
	}

	return nil
}

// finalizeMFAReset removes the enrollment, kills every session, and notifies.
func (uc *UseCase) finalizeMFAReset(ctx context.Context, userID string) error {
	if err := uc.mfa.DeleteMFA(ctx, userID); err != nil {
		return err
	}

	if uc.sessions != nil {
		if _, err := uc.sessions.RevokeAllAuthSessions(ctx, userID); err != nil {
			return fmt.Errorf("UserUseCase - ConfirmMFAReset - RevokeAllAuthSessions: %w", err)
		}
	}

	if user, userErr := uc.repo.GetByID(ctx, userID); userErr == nil {
		uc.notifyMFADisabled(ctx, &user)
	}

	return nil
}

// verifySecondFactor accepts a TOTP code or a recovery code; both failure
// modes collapse to ErrInvalidMFACode so responses cannot reveal which factor
// was wrong.
func (uc *UseCase) verifySecondFactor(ctx context.Context, mfa *entity.UserMFA, code string) error {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return entity.ErrInvalidMFACode
	}

	if normalizedOTP, otpErr := validateOTPInput(trimmed); otpErr == nil {
		return uc.verifyTOTP(ctx, mfa, normalizedOTP)
	}

	normalized, err := normalizeRecoveryCode(trimmed)
	if err != nil {
		return entity.ErrInvalidMFACode
	}

	return uc.mfa.ConsumeRecoveryCode(ctx, mfa.UserID, hashTokenBytes([]byte(normalized)))
}

// verifyTOTP checks a 6-digit code against the sealed secret over the ±skew
// window, then advances the monotonic step guard (replay of a just-used code
// loses to the guard even inside the validity window).
func (uc *UseCase) verifyTOTP(ctx context.Context, mfa *entity.UserMFA, code string) error {
	secretBytes, err := uc.mfaBox.Open(mfa.TOTPSecretEnc)
	if err != nil {
		return fmt.Errorf("UserUseCase - verifyTOTP - Open: %w", err)
	}

	secret := string(secretBytes)
	now := time.Now().UTC()
	step := now.Unix() / totpPeriodSeconds

	for offset := int64(-totpSkewSteps); offset <= totpSkewSteps; offset++ {
		candidateStep := step + offset

		expected, err := totp.GenerateCodeCustom(secret, time.Unix(candidateStep*totpPeriodSeconds, 0).UTC(), totp.ValidateOpts{
			Period:    totpPeriodSeconds,
			Skew:      0,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return fmt.Errorf("UserUseCase - verifyTOTP - GenerateCodeCustom: %w", err)
		}

		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return uc.mfa.AdvanceMFATOTPStep(ctx, mfa.UserID, candidateStep)
		}
	}

	return entity.ErrInvalidMFACode
}

// generateRecoveryCodes mints the raw display codes and their at-rest hashes.
func generateRecoveryCodes() (raw, hashes []string, err error) {
	raw = make([]string, 0, mfaRecoveryCodeCount)
	hashes = make([]string, 0, mfaRecoveryCodeCount)

	for range mfaRecoveryCodeCount {
		buf := make([]byte, mfaRecoveryCodeBytes)
		if _, err := rand.Read(buf); err != nil {
			return nil, nil, fmt.Errorf("UserUseCase - generateRecoveryCodes - rand.Read: %w", err)
		}

		normalized := recoveryCodeEncoding().EncodeToString(buf)
		raw = append(raw, formatRecoveryCode(normalized))
		hashes = append(hashes, hashTokenBytes([]byte(normalized)))
	}

	return raw, hashes, nil
}

// formatRecoveryCode groups the base32 body for readability (XXXX-XXXX-...).
func formatRecoveryCode(normalized string) string {
	groups := make([]string, 0, (len(normalized)+mfaRecoveryCodeGroup-1)/mfaRecoveryCodeGroup)
	for start := 0; start < len(normalized); start += mfaRecoveryCodeGroup {
		end := min(start+mfaRecoveryCodeGroup, len(normalized))
		groups = append(groups, normalized[start:end])
	}

	return strings.Join(groups, "-")
}

// normalizeRecoveryCode uppercases and strips separators; the result must be
// exactly the base32 body we generated.
func normalizeRecoveryCode(code string) (string, error) {
	var b strings.Builder

	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '2' && r <= '7':
			b.WriteRune(r)
		case r == '-' || r == ' ':
			// separators are cosmetic
		default:
			return "", entity.ErrInvalidMFACode
		}
	}

	expectedLen := recoveryCodeEncoding().EncodedLen(mfaRecoveryCodeBytes)
	if b.Len() != expectedLen {
		return "", entity.ErrInvalidMFACode
	}

	return b.String(), nil
}

// newMFAChallengeToken mints an opaque challenge token + its at-rest hash.
func newMFAChallengeToken() (raw, tokenHash string, err error) {
	buf := make([]byte, mfaChallengeTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("UserUseCase - newMFAChallengeToken - rand.Read: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), hashTokenBytes(buf), nil
}

// hashMFAChallengeToken hashes a presented challenge token for lookup.
func hashMFAChallengeToken(raw string) string {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		// Not one of ours; hash the raw bytes so the lookup simply misses.
		return hashTokenBytes([]byte(raw))
	}

	return hashTokenBytes(decoded)
}

// notifyMFADisabled tells the account owner their second factor is gone —
// the one email that must fire loudly if an attacker pulls it off.
func (uc *UseCase) notifyMFADisabled(ctx context.Context, user *entity.User) {
	if uc.emailSender == nil || !validEmail(user.Email) {
		return
	}

	emailCtx := uc.newAuthEmailContext(ctx, *user)
	uc.sendSecurityNotification(
		ctx,
		*user,
		entity.EmailTemplateKeyMFADisabled,
		authEmailVariables(emailCtx),
		mfaDisabledEmailContent(&emailCtx),
	)
}

func mfaResetEmailVariables(emailCtx *authEmailContext, otpValue string, ttl time.Duration) map[string]string {
	variables := authEmailVariables(*emailCtx)
	variables["otp"] = otpValue
	variables["ttl"] = humanDurationText(ttl, emailCtx.Lang)

	return variables
}

func mfaResetEmailContent(emailCtx *authEmailContext, otpValue string, ttl time.Duration) authEmailContent {
	duration := humanDurationText(ttl, emailCtx.Lang)

	if emailCtx.Lang == contentlang.English {
		text := fmt.Sprintf(
			"%s\n\nUse this code together with one of your recovery codes to remove two-factor authentication from your Surau account:\n%s\n\nThe code expires in %s.\n\nIf you did not request this, change your password immediately.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
			otpValue,
			duration,
		)

		return newAuthEmailContent(
			*emailCtx,
			"Your Surau MFA reset code",
			"MFA reset code inside",
			"Reset two-factor authentication",
			fmt.Sprintf(
				"Enter this code with one of your recovery codes to remove two-factor authentication: <strong>%s</strong>. It expires in %s.",
				otpValue, duration,
			),
			"",
			"",
			"If you did not request this, change your password immediately.",
			"",
			text,
		)
	}

	text := fmt.Sprintf(
		"%s\n\nGunakan kode ini bersama salah satu recovery code untuk melepas autentikasi dua langkah dari akun Surau Anda:\n%s\n\nKode kedaluwarsa dalam %s.\n\nJika Anda tidak meminta ini, segera ganti kata sandi Anda.",
		localizedGreeting(emailCtx.Lang, emailCtx.Name),
		otpValue,
		duration,
	)

	return newAuthEmailContent(
		*emailCtx,
		"Kode reset MFA Surau Anda",
		"Kode reset MFA di dalam",
		"Reset autentikasi dua langkah",
		fmt.Sprintf(
			"Masukkan kode ini bersama salah satu recovery code untuk melepas autentikasi dua langkah: <strong>%s</strong>. Berlaku %s.",
			otpValue, duration,
		),
		"",
		"",
		"Jika Anda tidak meminta ini, segera ganti kata sandi Anda.",
		"",
		text,
	)
}

func mfaDisabledEmailContent(emailCtx *authEmailContext) authEmailContent {
	if emailCtx.Lang == contentlang.English {
		text := fmt.Sprintf(
			"%s\n\nTwo-factor authentication was just REMOVED from your Surau account and every session was signed out.\n\nIf this was you, you can re-enroll from security settings. If not, reset your password immediately and contact support.",
			localizedGreeting(emailCtx.Lang, emailCtx.Name),
		)

		return newAuthEmailContent(
			*emailCtx,
			"Two-factor authentication removed",
			"MFA was removed from your account",
			"Two-factor authentication removed",
			"Two-factor authentication was just removed from your account and every session was signed out. If this was not you, reset your password immediately.",
			"",
			"",
			"",
			"",
			text,
		)
	}

	text := fmt.Sprintf(
		"%s\n\nAutentikasi dua langkah baru saja DILEPAS dari akun Surau Anda dan semua sesi dikeluarkan.\n\nJika ini Anda, silakan aktifkan kembali dari pengaturan keamanan. Jika bukan, segera reset kata sandi dan hubungi dukungan.",
		localizedGreeting(emailCtx.Lang, emailCtx.Name),
	)

	return newAuthEmailContent(
		*emailCtx,
		"Autentikasi dua langkah dilepas",
		"MFA dilepas dari akun Anda",
		"Autentikasi dua langkah dilepas",
		"Autentikasi dua langkah baru saja dilepas dari akun Anda dan semua sesi dikeluarkan. Jika ini bukan Anda, segera reset kata sandi.",
		"",
		"",
		"",
		"",
		text,
	)
}
