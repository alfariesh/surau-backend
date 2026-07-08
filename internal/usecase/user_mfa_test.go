package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase/user"
	"github.com/alfariesh/surau-backend/pkg/cryptobox"
	"github.com/alfariesh/surau-backend/pkg/jwt"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

//nolint:gosec // fixed RFC 6238 test vector, not a credential
const (
	testMFAUserID   = "mfa-user-1"
	testMFAFamilyID = "mfa-family-1"
	testTOTPSecret  = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
)

type mfaFixture struct {
	uc       *user.UseCase
	repo     *MockUserRepo
	mfa      *MockMFARepo
	sessions *MockAuthSessionRepo
	email    *MockEmailSender
	box      *cryptobox.Box
}

func newMFAFixture(t *testing.T) *mfaFixture {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockUserRepo(ctrl)
	mfaRepo := NewMockMFARepo(ctrl)
	sessions := NewMockAuthSessionRepo(ctrl)
	emailSender := NewMockEmailSender(ctrl)

	box, err := cryptobox.New(strings.Repeat("k", 32), "test-mfa")
	require.NoError(t, err)

	jwtManager := jwt.New(testJWTSecret, time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)
	uc := user.New(repo, jwtManager, emailSender, user.Options{
		Sessions:        sessions,
		MFA:             mfaRepo,
		MFABox:          box,
		RefreshTokenTTL: 24 * time.Hour,
		MFAOptions: user.MFAOptions{
			StepUpTTL:       10 * time.Minute,
			EnrollmentGrace: 168 * time.Hour,
			ChallengeTTL:    5 * time.Minute,
			ResetTTL:        15 * time.Minute,
			TOTPIssuer:      "SurauTest",
		},
	})

	return &mfaFixture{uc: uc, repo: repo, mfa: mfaRepo, sessions: sessions, email: emailSender, box: box}
}

func (f *mfaFixture) sealedSecret(t *testing.T) string {
	t.Helper()

	sealed, err := f.box.Seal([]byte(testTOTPSecret))
	require.NoError(t, err)

	return sealed
}

func totpCodeNow(t *testing.T) string {
	t.Helper()

	code, err := totp.GenerateCode(testTOTPSecret, time.Now().UTC())
	require.NoError(t, err)

	return code
}

func testMFAUser() entity.User {
	return entity.User{
		ID:            testMFAUserID,
		Username:      "mfauser",
		Email:         "mfa@example.com",
		Role:          entity.UserRoleAdmin,
		EmailVerified: true,
	}
}

func TestStartMFAEnrollment(t *testing.T) {
	t.Parallel()

	t.Run("provisions a pending secret and returns otpauth material", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		f.repo.EXPECT().GetByID(gomock.Any(), testMFAUserID).Return(testMFAUser(), nil)

		var sealed string

		f.mfa.EXPECT().UpsertPendingMFA(gomock.Any(), testMFAUserID, gomock.Any()).
			DoAndReturn(func(_ context.Context, _, secretEnc string) error {
				sealed = secretEnc

				return nil
			})

		enrollment, err := f.uc.StartMFAEnrollment(context.Background(), testMFAUserID)
		require.NoError(t, err)
		assert.NotEmpty(t, enrollment.Secret)
		assert.Contains(t, enrollment.OTPAuthURL, "otpauth://totp/")
		assert.Contains(t, enrollment.OTPAuthURL, "SurauTest")

		// The stored secret is sealed, not plaintext, and opens back to the
		// returned provisioning secret.
		assert.NotContains(t, sealed, enrollment.Secret)
		opened, err := f.box.Open(sealed)
		require.NoError(t, err)
		assert.Equal(t, enrollment.Secret, string(opened))
	})

	t.Run("already enabled bubbles up", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		f.repo.EXPECT().GetByID(gomock.Any(), testMFAUserID).Return(testMFAUser(), nil)
		f.mfa.EXPECT().UpsertPendingMFA(gomock.Any(), testMFAUserID, gomock.Any()).
			Return(entity.ErrMFAAlreadyEnabled)

		_, err := f.uc.StartMFAEnrollment(context.Background(), testMFAUserID)
		require.ErrorIs(t, err, entity.ErrMFAAlreadyEnabled)
	})
}

func TestConfirmMFAEnrollment(t *testing.T) {
	t.Parallel()

	t.Run("activates, returns ten recovery codes, stamps the session", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		pending := entity.UserMFA{UserID: testMFAUserID, TOTPSecretEnc: f.sealedSecret(t)}
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).Return(pending, nil)
		f.mfa.EXPECT().AdvanceMFATOTPStep(gomock.Any(), testMFAUserID, gomock.Any()).Return(nil)
		f.mfa.EXPECT().ConfirmMFA(gomock.Any(), testMFAUserID).Return(nil)

		var storedHashes []string

		f.mfa.EXPECT().ReplaceRecoveryCodes(gomock.Any(), testMFAUserID, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, hashes []string) error {
				storedHashes = hashes

				return nil
			})
		f.mfa.EXPECT().SetSessionMFAVerified(gomock.Any(), testMFAUserID, testMFAFamilyID, gomock.Any()).Return(nil)

		codes, err := f.uc.ConfirmMFAEnrollment(context.Background(), testMFAUserID, testMFAFamilyID, totpCodeNow(t))
		require.NoError(t, err)
		require.Len(t, codes, 10)
		require.Len(t, storedHashes, 10)

		for _, code := range codes {
			assert.Regexp(t, `^[A-Z2-7]{4}(-[A-Z2-7]{4}){3}$`, code, "grouped base32 display format")
		}
	})

	t.Run("wrong code is rejected without activating", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		pending := entity.UserMFA{UserID: testMFAUserID, TOTPSecretEnc: f.sealedSecret(t)}
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).Return(pending, nil)

		_, err := f.uc.ConfirmMFAEnrollment(context.Background(), testMFAUserID, testMFAFamilyID, "000000")
		require.ErrorIs(t, err, entity.ErrInvalidMFACode)
	})

	t.Run("nothing pending", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).Return(entity.UserMFA{}, entity.ErrMFANotEnabled)

		_, err := f.uc.ConfirmMFAEnrollment(context.Background(), testMFAUserID, testMFAFamilyID, "123456")
		require.ErrorIs(t, err, entity.ErrMFAEnrollmentNotStarted)
	})

	t.Run("already confirmed", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		confirmedAt := time.Now().UTC()
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).
			Return(entity.UserMFA{UserID: testMFAUserID, TOTPSecretEnc: f.sealedSecret(t), ConfirmedAt: &confirmedAt}, nil)

		_, err := f.uc.ConfirmMFAEnrollment(context.Background(), testMFAUserID, testMFAFamilyID, "123456")
		require.ErrorIs(t, err, entity.ErrMFAAlreadyEnabled)
	})
}

func TestLoginDivertsToMFAChallenge(t *testing.T) {
	t.Parallel()

	t.Run("confirmed MFA account gets a challenge instead of tokens", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		stored := testMFAUser()
		stored.PasswordHash = string(hash)
		confirmedAt := time.Now().UTC()

		f.repo.EXPECT().GetByEmail(gomock.Any(), "mfa@example.com").Return(stored, nil)
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).
			Return(entity.UserMFA{UserID: testMFAUserID, ConfirmedAt: &confirmedAt}, nil)

		var challenge entity.MFAChallenge

		f.mfa.EXPECT().CreateMFAChallenge(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, c entity.MFAChallenge) error {
				challenge = c

				return nil
			})

		result, err := f.uc.Login(context.Background(), "mfa@example.com", "password123")
		require.NoError(t, err)
		assert.True(t, result.MFARequired)
		assert.NotEmpty(t, result.MFAToken)
		assert.Empty(t, result.AccessToken, "no session before the second factor")
		assert.Empty(t, result.RefreshToken)
		assert.Equal(t, entity.MFAChallengePurposeLogin, challenge.Purpose)
		assert.Equal(t, testMFAUserID, challenge.UserID)
	})

	t.Run("account without MFA keeps the plain flow", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		stored := testMFAUser()
		stored.PasswordHash = string(hash)

		f.repo.EXPECT().GetByEmail(gomock.Any(), "mfa@example.com").Return(stored, nil)
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).Return(entity.UserMFA{}, entity.ErrMFANotEnabled)
		f.sessions.EXPECT().CreateAuthSession(gomock.Any(), gomock.Any()).Return(nil)

		result, err := f.uc.Login(context.Background(), "mfa@example.com", "password123")
		require.NoError(t, err)
		assert.False(t, result.MFARequired)
		assert.NotEmpty(t, result.AccessToken)
		assert.NotEmpty(t, result.RefreshToken)
	})
}

func TestVerifyMFALogin(t *testing.T) {
	t.Parallel()

	confirmedAt := time.Now().UTC().Add(-time.Hour)

	liveChallenge := func() entity.MFAChallenge {
		return entity.MFAChallenge{
			ID:        "challenge-1",
			UserID:    testMFAUserID,
			Purpose:   entity.MFAChallengePurposeLogin,
			ExpiresAt: time.Now().UTC().Add(4 * time.Minute),
		}
	}

	t.Run("TOTP completes login with a step-up-fresh session", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		f.mfa.EXPECT().GetMFAChallengeByTokenHash(gomock.Any(), gomock.Any(), entity.MFAChallengePurposeLogin).
			Return(liveChallenge(), nil)
		f.repo.EXPECT().GetByID(gomock.Any(), testMFAUserID).Return(testMFAUser(), nil)
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).
			Return(entity.UserMFA{UserID: testMFAUserID, TOTPSecretEnc: f.sealedSecret(t), ConfirmedAt: &confirmedAt}, nil)
		f.mfa.EXPECT().AdvanceMFATOTPStep(gomock.Any(), testMFAUserID, gomock.Any()).Return(nil)
		f.mfa.EXPECT().ConsumeMFAChallenge(gomock.Any(), "challenge-1").Return(nil)

		var created entity.AuthSession

		f.sessions.EXPECT().CreateAuthSession(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, s entity.AuthSession) error {
				created = s

				return nil
			})

		result, err := f.uc.VerifyMFALogin(context.Background(), "mfa-token", totpCodeNow(t))
		require.NoError(t, err)
		assert.NotEmpty(t, result.AccessToken)
		assert.NotEmpty(t, result.RefreshToken)
		require.NotNil(t, created.MFAVerifiedAt, "session born step-up fresh")
	})

	t.Run("recovery code path consumes exactly the stored hash", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)

		// Capture a real generated code set so the display format feeds back
		// into the consume path.
		pending := entity.UserMFA{UserID: testMFAUserID, TOTPSecretEnc: f.sealedSecret(t)}
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).Return(pending, nil)
		f.mfa.EXPECT().AdvanceMFATOTPStep(gomock.Any(), testMFAUserID, gomock.Any()).Return(nil)
		f.mfa.EXPECT().ConfirmMFA(gomock.Any(), testMFAUserID).Return(nil)

		var storedHashes []string

		f.mfa.EXPECT().ReplaceRecoveryCodes(gomock.Any(), testMFAUserID, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, hashes []string) error {
				storedHashes = hashes

				return nil
			})
		f.mfa.EXPECT().SetSessionMFAVerified(gomock.Any(), testMFAUserID, testMFAFamilyID, gomock.Any()).Return(nil)

		codes, err := f.uc.ConfirmMFAEnrollment(context.Background(), testMFAUserID, testMFAFamilyID, totpCodeNow(t))
		require.NoError(t, err)

		f.mfa.EXPECT().GetMFAChallengeByTokenHash(gomock.Any(), gomock.Any(), entity.MFAChallengePurposeLogin).
			Return(liveChallenge(), nil)
		f.repo.EXPECT().GetByID(gomock.Any(), testMFAUserID).Return(testMFAUser(), nil)
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).
			Return(entity.UserMFA{UserID: testMFAUserID, TOTPSecretEnc: f.sealedSecret(t), ConfirmedAt: &confirmedAt}, nil)
		f.mfa.EXPECT().ConsumeRecoveryCode(gomock.Any(), testMFAUserID, storedHashes[0]).Return(nil)
		f.mfa.EXPECT().ConsumeMFAChallenge(gomock.Any(), "challenge-1").Return(nil)
		f.sessions.EXPECT().CreateAuthSession(gomock.Any(), gomock.Any()).Return(nil)

		// Lowercase + spacing variants must normalize to the same hash.
		submitted := strings.ToLower(strings.ReplaceAll(codes[0], "-", " "))

		result, err := f.uc.VerifyMFALogin(context.Background(), "mfa-token", submitted)
		require.NoError(t, err)
		assert.NotEmpty(t, result.AccessToken)
	})

	t.Run("wrong code never consumes the challenge", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		f.mfa.EXPECT().GetMFAChallengeByTokenHash(gomock.Any(), gomock.Any(), entity.MFAChallengePurposeLogin).
			Return(liveChallenge(), nil)
		f.repo.EXPECT().GetByID(gomock.Any(), testMFAUserID).Return(testMFAUser(), nil)
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).
			Return(entity.UserMFA{UserID: testMFAUserID, TOTPSecretEnc: f.sealedSecret(t), ConfirmedAt: &confirmedAt}, nil)

		_, err := f.uc.VerifyMFALogin(context.Background(), "mfa-token", "000000")
		require.ErrorIs(t, err, entity.ErrInvalidMFACode)
	})

	t.Run("challenge for an account whose MFA was disabled is stale", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		f.mfa.EXPECT().GetMFAChallengeByTokenHash(gomock.Any(), gomock.Any(), entity.MFAChallengePurposeLogin).
			Return(liveChallenge(), nil)
		f.repo.EXPECT().GetByID(gomock.Any(), testMFAUserID).Return(testMFAUser(), nil)
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).Return(entity.UserMFA{}, entity.ErrMFANotEnabled)

		_, err := f.uc.VerifyMFALogin(context.Background(), "mfa-token", "123456")
		require.ErrorIs(t, err, entity.ErrInvalidMFAChallenge)
	})

	t.Run("unknown challenge token", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		f.mfa.EXPECT().GetMFAChallengeByTokenHash(gomock.Any(), gomock.Any(), entity.MFAChallengePurposeLogin).
			Return(entity.MFAChallenge{}, entity.ErrInvalidMFAChallenge)

		_, err := f.uc.VerifyMFALogin(context.Background(), "nope", "123456")
		require.ErrorIs(t, err, entity.ErrInvalidMFAChallenge)
	})
}

func TestStepUpMFA(t *testing.T) {
	t.Parallel()

	t.Run("stamps the session and returns the freshness deadline", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		confirmedAt := time.Now().UTC().Add(-time.Hour)
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).
			Return(entity.UserMFA{UserID: testMFAUserID, TOTPSecretEnc: f.sealedSecret(t), ConfirmedAt: &confirmedAt}, nil)
		f.mfa.EXPECT().AdvanceMFATOTPStep(gomock.Any(), testMFAUserID, gomock.Any()).Return(nil)
		f.mfa.EXPECT().SetSessionMFAVerified(gomock.Any(), testMFAUserID, testMFAFamilyID, gomock.Any()).Return(nil)

		expiresAt, err := f.uc.StepUpMFA(context.Background(), testMFAUserID, testMFAFamilyID, totpCodeNow(t))
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().UTC().Add(10*time.Minute), expiresAt, 5*time.Second)
	})

	t.Run("not enrolled", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		f.mfa.EXPECT().GetMFA(gomock.Any(), testMFAUserID).Return(entity.UserMFA{}, entity.ErrMFANotEnabled)

		_, err := f.uc.StepUpMFA(context.Background(), testMFAUserID, testMFAFamilyID, "123456")
		require.ErrorIs(t, err, entity.ErrMFANotEnabled)
	})
}

func TestMFAGate(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	fresh := now.Add(-time.Minute)
	stale := now.Add(-time.Hour)
	graceStart := now.Add(-24 * time.Hour)        // inside the 168h grace
	graceExpired := now.Add(-30 * 24 * time.Hour) // way past it

	admin := testMFAUser()
	regular := entity.User{ID: "user-2", Role: entity.UserRoleUser}

	cases := []struct {
		name string
		user *entity.User
		data entity.MFAGateData
		want entity.MFAGateDecision
	}{
		{"enrolled and fresh", &admin, entity.MFAGateData{Confirmed: true, MFAVerifiedAt: &fresh}, entity.MFAGateAllowed},
		{"enrolled and stale", &admin, entity.MFAGateData{Confirmed: true, MFAVerifiedAt: &stale}, entity.MFAGateStepUpRequired},
		{"enrolled never stepped up", &admin, entity.MFAGateData{Confirmed: true}, entity.MFAGateStepUpRequired},
		{"admin not enrolled within grace", &admin, entity.MFAGateData{EnforcedFrom: &graceStart}, entity.MFAGateAllowed},
		{"admin not enrolled past grace", &admin, entity.MFAGateData{EnforcedFrom: &graceExpired}, entity.MFAGateEnrollmentRequired},
		{"admin missing anchor fails open with audit", &admin, entity.MFAGateData{}, entity.MFAGateAllowed},
		{"regular role never gated", &regular, entity.MFAGateData{}, entity.MFAGateAllowed},
		{"pending enrollment still counts as not enrolled", &admin, entity.MFAGateData{Pending: true, EnforcedFrom: &graceExpired}, entity.MFAGateEnrollmentRequired},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newMFAFixture(t)
			f.mfa.EXPECT().GetMFAGateData(gomock.Any(), tc.user.ID, testMFAFamilyID).Return(tc.data, nil)

			decision, err := f.uc.MFAGate(context.Background(), tc.user, testMFAFamilyID)
			require.NoError(t, err)
			assert.Equal(t, tc.want, decision)
		})
	}
}

func TestDisableMFA(t *testing.T) {
	t.Parallel()

	t.Run("fresh step-up disables, revokes all sessions, reissues", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		fresh := time.Now().UTC().Add(-time.Minute)
		f.mfa.EXPECT().GetMFAGateData(gomock.Any(), testMFAUserID, testMFAFamilyID).
			Return(entity.MFAGateData{Confirmed: true, MFAVerifiedAt: &fresh}, nil)
		f.mfa.EXPECT().DeleteMFA(gomock.Any(), testMFAUserID).Return(nil)
		f.sessions.EXPECT().RevokeAllAuthSessions(gomock.Any(), testMFAUserID).Return(int64(2), nil)
		f.repo.EXPECT().GetByID(gomock.Any(), testMFAUserID).Return(testMFAUser(), nil)
		f.repo.EXPECT().GetAccount(gomock.Any(), testMFAUserID).
			Return(entity.UserAccount{}, entity.ErrUserNotFound).AnyTimes()
		f.sessions.EXPECT().CreateAuthSession(gomock.Any(), gomock.Any()).Return(nil)
		f.email.EXPECT().Send(gomock.Any(), gomock.Any()).Return(entity.EmailSendResult{}, nil).AnyTimes()

		result, err := f.uc.DisableMFA(context.Background(), testMFAUserID, testMFAFamilyID)
		require.NoError(t, err)
		assert.NotEmpty(t, result.AccessToken)
	})

	t.Run("stale step-up is refused", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		stale := time.Now().UTC().Add(-time.Hour)
		f.mfa.EXPECT().GetMFAGateData(gomock.Any(), testMFAUserID, testMFAFamilyID).
			Return(entity.MFAGateData{Confirmed: true, MFAVerifiedAt: &stale}, nil)

		_, err := f.uc.DisableMFA(context.Background(), testMFAUserID, testMFAFamilyID)
		require.ErrorIs(t, err, entity.ErrMFAStepUpRequired)
	})

	t.Run("not enabled", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		f.mfa.EXPECT().GetMFAGateData(gomock.Any(), testMFAUserID, testMFAFamilyID).
			Return(entity.MFAGateData{}, nil)

		_, err := f.uc.DisableMFA(context.Background(), testMFAUserID, testMFAFamilyID)
		require.ErrorIs(t, err, entity.ErrMFANotEnabled)
	})
}

func TestRegenerateMFARecoveryCodes(t *testing.T) {
	t.Parallel()

	f := newMFAFixture(t)
	fresh := time.Now().UTC().Add(-time.Minute)
	f.mfa.EXPECT().GetMFAGateData(gomock.Any(), testMFAUserID, testMFAFamilyID).
		Return(entity.MFAGateData{Confirmed: true, MFAVerifiedAt: &fresh}, nil)
	f.mfa.EXPECT().ReplaceRecoveryCodes(gomock.Any(), testMFAUserID, gomock.Any()).Return(nil)

	codes, err := f.uc.RegenerateMFARecoveryCodes(context.Background(), testMFAUserID, testMFAFamilyID)
	require.NoError(t, err)
	assert.Len(t, codes, 10)
}

func TestMFAStatus(t *testing.T) {
	t.Parallel()

	f := newMFAFixture(t)
	enforced := time.Now().UTC().Add(-24 * time.Hour)
	verified := time.Now().UTC().Add(-2 * time.Minute)

	f.repo.EXPECT().GetByID(gomock.Any(), testMFAUserID).Return(testMFAUser(), nil)
	f.mfa.EXPECT().GetMFAGateData(gomock.Any(), testMFAUserID, testMFAFamilyID).
		Return(entity.MFAGateData{Confirmed: true, EnforcedFrom: &enforced, MFAVerifiedAt: &verified}, nil)
	f.mfa.EXPECT().CountUnusedRecoveryCodes(gomock.Any(), testMFAUserID).Return(7, nil)

	status, err := f.uc.MFAStatus(context.Background(), testMFAUserID, testMFAFamilyID)
	require.NoError(t, err)
	assert.True(t, status.Enabled)
	assert.True(t, status.Required)
	assert.Equal(t, 7, status.RecoveryCodesRemaining)
	require.NotNil(t, status.GraceEndsAt)
	assert.WithinDuration(t, enforced.Add(168*time.Hour), *status.GraceEndsAt, time.Second)
	require.NotNil(t, status.StepUpExpiresAt)
	assert.WithinDuration(t, verified.Add(10*time.Minute), *status.StepUpExpiresAt, time.Second)
}

func TestMFAResetFlow(t *testing.T) {
	t.Parallel()

	t.Run("request mints a reset challenge and emails the otp", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		f.mfa.EXPECT().GetMFAChallengeByTokenHash(gomock.Any(), gomock.Any(), entity.MFAChallengePurposeLogin).
			Return(entity.MFAChallenge{ID: "login-1", UserID: testMFAUserID, Purpose: entity.MFAChallengePurposeLogin}, nil)
		f.repo.EXPECT().GetByID(gomock.Any(), testMFAUserID).Return(testMFAUser(), nil)
		f.repo.EXPECT().GetAccount(gomock.Any(), testMFAUserID).
			Return(entity.UserAccount{}, entity.ErrUserNotFound).AnyTimes()

		var created entity.MFAChallenge

		f.mfa.EXPECT().CreateMFAChallenge(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, c entity.MFAChallenge) error {
				created = c

				return nil
			})
		f.email.EXPECT().Send(gomock.Any(), gomock.Any()).Return(entity.EmailSendResult{}, nil)

		token, expiresAt, err := f.uc.RequestMFAReset(context.Background(), "login-token")
		require.NoError(t, err)
		assert.NotEmpty(t, token)
		assert.False(t, expiresAt.IsZero())
		assert.Equal(t, entity.MFAChallengePurposeReset, created.Purpose)
		require.NotNil(t, created.OTPHash)
		require.NotNil(t, created.OTPExpiresAt)
	})

	t.Run("confirm requires otp AND an unused recovery code, then nukes MFA", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		otpHash, err := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.DefaultCost)
		require.NoError(t, err)

		otpHashStr := string(otpHash)
		otpExpiry := time.Now().UTC().Add(10 * time.Minute)
		reset := entity.MFAChallenge{
			ID:           "reset-1",
			UserID:       testMFAUserID,
			Purpose:      entity.MFAChallengePurposeReset,
			OTPHash:      &otpHashStr,
			OTPExpiresAt: &otpExpiry,
			ExpiresAt:    otpExpiry,
		}

		f.mfa.EXPECT().GetMFAChallengeByTokenHash(gomock.Any(), gomock.Any(), entity.MFAChallengePurposeReset).
			Return(reset, nil)
		f.mfa.EXPECT().ConsumeRecoveryCode(gomock.Any(), testMFAUserID, gomock.Any()).Return(nil)
		f.mfa.EXPECT().ConsumeMFAChallenge(gomock.Any(), "reset-1").Return(nil)
		f.mfa.EXPECT().DeleteMFA(gomock.Any(), testMFAUserID).Return(nil)
		f.sessions.EXPECT().RevokeAllAuthSessions(gomock.Any(), testMFAUserID).Return(int64(3), nil)
		f.repo.EXPECT().GetByID(gomock.Any(), testMFAUserID).Return(testMFAUser(), nil)
		f.repo.EXPECT().GetAccount(gomock.Any(), testMFAUserID).
			Return(entity.UserAccount{}, entity.ErrUserNotFound).AnyTimes()
		f.email.EXPECT().Send(gomock.Any(), gomock.Any()).Return(entity.EmailSendResult{}, nil).AnyTimes()

		err = f.uc.ConfirmMFAReset(context.Background(), "reset-token", "123456", "AAAA-BBBB-CCCC-DDDD")
		require.NoError(t, err)
	})

	t.Run("wrong otp is rejected before anything is consumed", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		otpHash, err := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.DefaultCost)
		require.NoError(t, err)

		otpHashStr := string(otpHash)
		otpExpiry := time.Now().UTC().Add(10 * time.Minute)
		reset := entity.MFAChallenge{
			ID: "reset-1", UserID: testMFAUserID, Purpose: entity.MFAChallengePurposeReset,
			OTPHash: &otpHashStr, OTPExpiresAt: &otpExpiry, ExpiresAt: otpExpiry,
		}

		f.mfa.EXPECT().GetMFAChallengeByTokenHash(gomock.Any(), gomock.Any(), entity.MFAChallengePurposeReset).
			Return(reset, nil)

		err = f.uc.ConfirmMFAReset(context.Background(), "reset-token", "654321", "AAAA-BBBB-CCCC-DDDD")
		require.ErrorIs(t, err, entity.ErrInvalidMFAReset)
	})

	t.Run("spent recovery code fails the reset", func(t *testing.T) {
		t.Parallel()

		f := newMFAFixture(t)
		otpHash, err := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.DefaultCost)
		require.NoError(t, err)

		otpHashStr := string(otpHash)
		otpExpiry := time.Now().UTC().Add(10 * time.Minute)
		reset := entity.MFAChallenge{
			ID: "reset-1", UserID: testMFAUserID, Purpose: entity.MFAChallengePurposeReset,
			OTPHash: &otpHashStr, OTPExpiresAt: &otpExpiry, ExpiresAt: otpExpiry,
		}

		f.mfa.EXPECT().GetMFAChallengeByTokenHash(gomock.Any(), gomock.Any(), entity.MFAChallengePurposeReset).
			Return(reset, nil)
		f.mfa.EXPECT().ConsumeRecoveryCode(gomock.Any(), testMFAUserID, gomock.Any()).
			Return(entity.ErrInvalidMFACode)

		err = f.uc.ConfirmMFAReset(context.Background(), "reset-token", "123456", "AAAA-BBBB-CCCC-DDDD")
		require.ErrorIs(t, err, entity.ErrInvalidMFAReset)
	})
}
