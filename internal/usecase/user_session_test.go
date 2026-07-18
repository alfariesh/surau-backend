package usecase_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	repoContract "github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/internal/usecase/user"
	"github.com/alfariesh/surau-backend/pkg/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

func newUserUseCaseWithSessions(
	t *testing.T,
) (*user.UseCase, *MockUserRepo, *MockAuthSessionRepo, *MockAuthLockoutRepo) {
	t.Helper()

	return newUserUseCaseWithSessionClock(t, 24*time.Hour, nil)
}

func newUserUseCaseWithSessionClock(
	t *testing.T,
	refreshTTL time.Duration,
	now func() time.Time,
) (*user.UseCase, *MockUserRepo, *MockAuthSessionRepo, *MockAuthLockoutRepo) {
	t.Helper()

	ctrl := gomock.NewController(t)

	repo := NewMockUserRepo(ctrl)
	emailSender := NewMockEmailSender(ctrl)
	sessions := NewMockAuthSessionRepo(ctrl)
	lockout := NewMockAuthLockoutRepo(ctrl)
	jwtManager := jwt.New(testJWTSecret, time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)
	useCase := user.New(repo, jwtManager, emailSender, user.Options{
		VerificationTTL:       time.Hour,
		ResendCooldown:        time.Minute,
		PasswordResetTTL:      time.Hour,
		PasswordResetCooldown: time.Minute,
		EmailChangeTTL:        time.Hour,
		EmailChangeCooldown:   time.Minute,
		Sessions:              sessions,
		Lockout:               lockout,
		RefreshTokenTTL:       refreshTTL,
		LockoutOptions:        user.LockoutOptions{Enabled: true},
		Now:                   now,
	})

	return useCase, repo, sessions, lockout
}

func TestRefreshSessionSlidingInactivityWindow(t *testing.T) {
	t.Parallel()

	const refreshTTL = 336 * time.Hour

	fixedNow := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	activeLegacySession := func(tokenHash string, lastUsedAt time.Time) entity.AuthSession {
		return entity.AuthSession{
			ID:               "session-old",
			FamilyID:         "family-1",
			UserID:           "user-id-123",
			RefreshTokenHash: tokenHash,
			TokenVersion:     3,
			UserAgent:        "Mozilla/5.0 Chrome/126.0.0.0 Safari/537.36",
			ClientIP:         "203.0.113.10",
			CreatedAt:        fixedNow.Add(-30 * 24 * time.Hour),
			LastUsedAt:       lastUsedAt,
			// Legacy rows may still carry the previous 30-day absolute expiry.
			ExpiresAt: fixedNow.Add(30 * 24 * time.Hour),
		}
	}

	t.Run("legacy session active just before deadline rotates into fourteen-day successor", func(t *testing.T) {
		t.Parallel()

		uc, users, sessions, _ := newUserUseCaseWithSessionClock(t, refreshTTL, func() time.Time {
			return fixedNow
		})
		rawToken, tokenHash := testRefreshToken(t)
		legacy := activeLegacySession(tokenHash, fixedNow.Add(-refreshTTL).Add(time.Nanosecond))

		sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), tokenHash).Return(legacy, nil)
		users.EXPECT().GetByID(gomock.Any(), legacy.UserID).
			Return(entity.User{ID: legacy.UserID, TokenVersion: legacy.TokenVersion}, nil)

		var successor entity.AuthSession

		sessions.EXPECT().
			RotateAuthSession(gomock.Any(), legacy.ID, gomock.Any(), gomock.Any()).
			DoAndReturn(func(
				_ context.Context,
				_ string,
				next *entity.AuthSession,
				validity repoContract.AuthSessionValidity,
			) error {
				successor = *next

				assert.Equal(t, fixedNow, validity.Now)
				assert.Equal(t, fixedNow.Add(-refreshTTL), validity.IdleCutoff)

				return nil
			})

		result, err := uc.RefreshSession(context.Background(), rawToken)

		require.NoError(t, err)
		assert.Equal(t, fixedNow, successor.LastUsedAt)
		assert.Equal(t, fixedNow.Add(refreshTTL), successor.ExpiresAt)
		assert.Equal(t, successor.ExpiresAt, result.RefreshExpiresAt)
		assert.Equal(t, legacy.FamilyID, successor.FamilyID)
		assert.Equal(t, legacy.UserAgent, successor.UserAgent)
		assert.Equal(t, legacy.ClientIP, successor.ClientIP)
	})

	for _, tc := range []struct {
		name       string
		lastUsedAt time.Time
		expiresAt  time.Time
	}{
		{
			name:       "exactly fourteen days idle is rejected",
			lastUsedAt: fixedNow.Add(-refreshTTL),
			expiresAt:  fixedNow.Add(30 * 24 * time.Hour),
		},
		{
			name:       "more than fourteen days idle is rejected",
			lastUsedAt: fixedNow.Add(-refreshTTL).Add(-time.Nanosecond),
			expiresAt:  fixedNow.Add(30 * 24 * time.Hour),
		},
		{
			name:       "earlier absolute expiry still wins",
			lastUsedAt: fixedNow.Add(-time.Hour),
			expiresAt:  fixedNow,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc, _, sessions, _ := newUserUseCaseWithSessionClock(t, refreshTTL, func() time.Time {
				return fixedNow
			})
			rawToken, tokenHash := testRefreshToken(t)
			stored := activeLegacySession(tokenHash, tc.lastUsedAt)
			stored.ExpiresAt = tc.expiresAt
			sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), tokenHash).Return(stored, nil)

			_, err := uc.RefreshSession(context.Background(), rawToken)

			require.ErrorIs(t, err, entity.ErrInvalidRefreshToken)
		})
	}

	t.Run("expiry discovered inside transaction is not treated as reuse", func(t *testing.T) {
		t.Parallel()

		uc, users, sessions, _ := newUserUseCaseWithSessionClock(t, refreshTTL, func() time.Time {
			return fixedNow
		})
		rawToken, tokenHash := testRefreshToken(t)
		stored := activeLegacySession(tokenHash, fixedNow.Add(-time.Hour))

		sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), tokenHash).Return(stored, nil)
		users.EXPECT().GetByID(gomock.Any(), stored.UserID).
			Return(entity.User{ID: stored.UserID, TokenVersion: stored.TokenVersion}, nil)
		sessions.EXPECT().
			RotateAuthSession(gomock.Any(), stored.ID, gomock.Any(), gomock.Any()).
			Return(entity.ErrRefreshSessionExpired)

		_, err := uc.RefreshSession(context.Background(), rawToken)

		require.ErrorIs(t, err, entity.ErrInvalidRefreshToken)
		// No RevokeAuthSessionFamily expectation: ordinary expiry must not
		// trigger the reuse detector or revoke/alarm path.
	})
}

func TestRefreshSessionActiveFamilyCanOutliveSingleWindow(t *testing.T) {
	t.Parallel()

	const refreshTTL = 336 * time.Hour

	currentNow := time.Date(2026, time.January, 14, 0, 0, 0, 0, time.UTC)
	uc, users, sessions, _ := newUserUseCaseWithSessionClock(t, refreshTTL, func() time.Time {
		return currentNow
	})
	rawToken, tokenHash := testRefreshToken(t)
	state := entity.AuthSession{
		ID:               "session-first",
		FamilyID:         "family-long-lived",
		UserID:           "user-id-123",
		RefreshTokenHash: tokenHash,
		TokenVersion:     3,
		CreatedAt:        currentNow.Add(-13 * 24 * time.Hour),
		LastUsedAt:       currentNow.Add(-13 * 24 * time.Hour),
		ExpiresAt:        currentNow.Add(30 * 24 * time.Hour),
	}

	sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string) (entity.AuthSession, error) {
			return state, nil
		}).Times(6)
	users.EXPECT().GetByID(gomock.Any(), state.UserID).
		Return(entity.User{ID: state.UserID, TokenVersion: state.TokenVersion}, nil).
		Times(6)
	sessions.EXPECT().RotateAuthSession(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			oldID string,
			next *entity.AuthSession,
			validity repoContract.AuthSessionValidity,
		) error {
			assert.Equal(t, state.ID, oldID)
			assert.Equal(t, currentNow, validity.Now)
			assert.Equal(t, currentNow.Add(refreshTTL), next.ExpiresAt)
			assert.Equal(t, "family-long-lived", next.FamilyID)
			state = *next

			return nil
		}).Times(6)

	for range 6 {
		result, err := uc.RefreshSession(context.Background(), rawToken)
		require.NoError(t, err)

		rawToken = result.RefreshToken
		currentNow = currentNow.Add(13 * 24 * time.Hour)
	}

	assert.Equal(t, "family-long-lived", state.FamilyID)
	assert.True(t, currentNow.Sub(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)) > 60*24*time.Hour)
}

func testRefreshToken(t *testing.T) (token, tokenHash string) {
	t.Helper()

	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}

	hash := sha256.Sum256(raw)

	return base64.RawURLEncoding.EncodeToString(raw), hex.EncodeToString(hash[:])
}

func testLockoutKeyHash(email string) string {
	hash := sha256.Sum256([]byte("login_lockout\x00email\x00" + email))

	return hex.EncodeToString(hash[:])
}

func TestLoginIssuesSession(t *testing.T) {
	t.Parallel()

	uc, repo, sessions, lockout := newUserUseCaseWithSessions(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)

	storedUser := entity.User{
		ID: "user-id-123", Username: "testuser",
		Email: "test@example.com", PasswordHash: string(hash), EmailVerified: true, TokenVersion: 3,
	}

	lockout.EXPECT().
		GetAuthLoginLockout(context.Background(), testLockoutKeyHash("test@example.com")).
		Return(entity.AuthLoginLockout{}, nil)
	repo.EXPECT().GetByEmail(context.Background(), "test@example.com").Return(storedUser, nil)
	lockout.EXPECT().ResetAuthLoginLockout(context.Background(), testLockoutKeyHash("test@example.com")).Return(nil)

	var storedSession entity.AuthSession

	sessions.EXPECT().
		CreateAuthSession(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, session entity.AuthSession) error {
			storedSession = session

			return nil
		})

	result, err := uc.Login(context.Background(), "test@example.com", "password123")

	require.NoError(t, err)
	assert.NotEmpty(t, result.AccessToken)
	assert.NotEmpty(t, result.RefreshToken)
	assert.Equal(t, result.SessionID, storedSession.ID)
	assert.Equal(t, storedSession.ID, storedSession.FamilyID)
	assert.Equal(t, int64(3), storedSession.TokenVersion)

	// The stored hash must be the sha256 of the raw refresh token bytes.
	rawBytes, err := base64.RawURLEncoding.DecodeString(result.RefreshToken)
	require.NoError(t, err)

	expectedHash := sha256.Sum256(rawBytes)
	assert.Equal(t, hex.EncodeToString(expectedHash[:]), storedSession.RefreshTokenHash)

	claims, err := jwt.New(testJWTSecret, time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience).
		ParseTokenClaims(result.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, result.SessionID, claims.SessionID)
	assert.Equal(t, int64(3), claims.TokenVersion)
}

func TestRefreshSession(t *testing.T) {
	t.Parallel()

	activeSession := func(tokenHash string) entity.AuthSession {
		return entity.AuthSession{
			ID:               "session-old",
			FamilyID:         "family-1",
			UserID:           "user-id-123",
			RefreshTokenHash: tokenHash,
			TokenVersion:     3,
			CreatedAt:        time.Now().UTC().Add(-time.Hour),
			LastUsedAt:       time.Now().UTC().Add(-time.Hour),
			ExpiresAt:        time.Now().UTC().Add(time.Hour),
		}
	}

	t.Run("rotates session and issues new pair", func(t *testing.T) {
		t.Parallel()

		uc, repo, sessions, _ := newUserUseCaseWithSessions(t)
		rawToken, tokenHash := testRefreshToken(t)

		sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), tokenHash).Return(activeSession(tokenHash), nil)
		repo.EXPECT().GetByID(gomock.Any(), "user-id-123").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com", TokenVersion: 3}, nil)

		var next entity.AuthSession

		sessions.EXPECT().
			RotateAuthSession(gomock.Any(), "session-old", gomock.Any(), gomock.Any()).
			DoAndReturn(func(
				_ context.Context,
				_ string,
				session *entity.AuthSession,
				_ repoContract.AuthSessionValidity,
			) error {
				next = *session

				return nil
			})

		result, err := uc.RefreshSession(context.Background(), rawToken)

		require.NoError(t, err)
		assert.Equal(t, "family-1", result.SessionID)
		assert.Equal(t, "family-1", next.FamilyID)
		assert.NotEqual(t, "session-old", next.ID)
		assert.NotEqual(t, tokenHash, next.RefreshTokenHash)
		assert.NotEqual(t, rawToken, result.RefreshToken)
	})

	t.Run("reuse of rotated token revokes family", func(t *testing.T) {
		t.Parallel()

		uc, _, sessions, _ := newUserUseCaseWithSessions(t)
		rawToken, tokenHash := testRefreshToken(t)
		replacedBy := "session-new"
		spent := activeSession(tokenHash)
		spent.ReplacedByID = &replacedBy

		sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), tokenHash).Return(spent, nil)
		sessions.EXPECT().RevokeAuthSessionFamily(gomock.Any(), "family-1").Return(int64(2), nil)

		_, err := uc.RefreshSession(context.Background(), rawToken)

		require.ErrorIs(t, err, entity.ErrInvalidRefreshToken)
	})

	t.Run("expired refresh token rejected", func(t *testing.T) {
		t.Parallel()

		uc, _, sessions, _ := newUserUseCaseWithSessions(t)
		rawToken, tokenHash := testRefreshToken(t)
		expired := activeSession(tokenHash)
		expired.ExpiresAt = time.Now().UTC().Add(-time.Minute)

		sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), tokenHash).Return(expired, nil)

		_, err := uc.RefreshSession(context.Background(), rawToken)

		require.ErrorIs(t, err, entity.ErrInvalidRefreshToken)
	})

	t.Run("token version mismatch revokes family", func(t *testing.T) {
		t.Parallel()

		uc, repo, sessions, _ := newUserUseCaseWithSessions(t)
		rawToken, tokenHash := testRefreshToken(t)

		sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), tokenHash).Return(activeSession(tokenHash), nil)
		repo.EXPECT().GetByID(gomock.Any(), "user-id-123").
			Return(entity.User{ID: "user-id-123", TokenVersion: 4}, nil)
		sessions.EXPECT().RevokeAuthSessionFamily(gomock.Any(), "family-1").Return(int64(1), nil)

		_, err := uc.RefreshSession(context.Background(), rawToken)

		require.ErrorIs(t, err, entity.ErrInvalidRefreshToken)
	})

	t.Run("deleted user revokes family", func(t *testing.T) {
		t.Parallel()

		uc, repo, sessions, _ := newUserUseCaseWithSessions(t)
		rawToken, tokenHash := testRefreshToken(t)

		sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), tokenHash).Return(activeSession(tokenHash), nil)
		repo.EXPECT().GetByID(gomock.Any(), "user-id-123").Return(entity.User{}, entity.ErrUserNotFound)
		sessions.EXPECT().RevokeAuthSessionFamily(gomock.Any(), "family-1").Return(int64(1), nil)

		_, err := uc.RefreshSession(context.Background(), rawToken)

		require.ErrorIs(t, err, entity.ErrInvalidRefreshToken)
	})

	t.Run("losing rotation race revokes family", func(t *testing.T) {
		t.Parallel()

		uc, repo, sessions, _ := newUserUseCaseWithSessions(t)
		rawToken, tokenHash := testRefreshToken(t)

		sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), tokenHash).Return(activeSession(tokenHash), nil)
		repo.EXPECT().GetByID(gomock.Any(), "user-id-123").
			Return(entity.User{ID: "user-id-123", TokenVersion: 3}, nil)
		sessions.EXPECT().
			RotateAuthSession(gomock.Any(), "session-old", gomock.Any(), gomock.Any()).
			Return(entity.ErrInvalidRefreshToken)
		sessions.EXPECT().RevokeAuthSessionFamily(gomock.Any(), "family-1").Return(int64(1), nil)

		_, err := uc.RefreshSession(context.Background(), rawToken)

		require.ErrorIs(t, err, entity.ErrInvalidRefreshToken)
	})

	t.Run("malformed refresh token rejected without lookup", func(t *testing.T) {
		t.Parallel()

		uc, _, _, _ := newUserUseCaseWithSessions(t)

		_, err := uc.RefreshSession(context.Background(), "not-a-valid-token!!!")

		require.ErrorIs(t, err, entity.ErrInvalidRefreshToken)
	})
}

func TestLogout(t *testing.T) {
	t.Parallel()

	t.Run("revokes session family", func(t *testing.T) {
		t.Parallel()

		uc, _, sessions, _ := newUserUseCaseWithSessions(t)
		rawToken, tokenHash := testRefreshToken(t)

		sessions.EXPECT().GetAuthSessionByTokenHash(gomock.Any(), tokenHash).Return(entity.AuthSession{
			ID: "session-old", FamilyID: "family-1", UserID: "user-id-123",
		}, nil)
		sessions.EXPECT().RevokeAuthSessionFamily(gomock.Any(), "family-1").Return(int64(1), nil)

		require.NoError(t, uc.Logout(context.Background(), rawToken))
	})

	t.Run("unknown token is idempotent success", func(t *testing.T) {
		t.Parallel()

		uc, _, sessions, _ := newUserUseCaseWithSessions(t)
		rawToken, tokenHash := testRefreshToken(t)

		sessions.EXPECT().
			GetAuthSessionByTokenHash(gomock.Any(), tokenHash).
			Return(entity.AuthSession{}, entity.ErrInvalidRefreshToken)

		require.NoError(t, uc.Logout(context.Background(), rawToken))
	})

	t.Run("malformed token rejected", func(t *testing.T) {
		t.Parallel()

		uc, _, _, _ := newUserUseCaseWithSessions(t)

		err := uc.Logout(context.Background(), "???")

		require.ErrorIs(t, err, entity.ErrInvalidAuthInput)
	})
}

func TestLogoutAll(t *testing.T) {
	t.Parallel()

	t.Run("revokes all sessions", func(t *testing.T) {
		t.Parallel()

		uc, _, sessions, _ := newUserUseCaseWithSessions(t)

		sessions.EXPECT().RevokeAllAuthSessions(gomock.Any(), "user-id-123").Return(int64(3), nil)

		require.NoError(t, uc.LogoutAll(context.Background(), "user-id-123"))
	})

	t.Run("unknown user maps to invalid credentials", func(t *testing.T) {
		t.Parallel()

		uc, _, sessions, _ := newUserUseCaseWithSessions(t)

		sessions.EXPECT().RevokeAllAuthSessions(gomock.Any(), "user-id-404").Return(int64(0), entity.ErrUserNotFound)

		err := uc.LogoutAll(context.Background(), "user-id-404")

		require.ErrorIs(t, err, entity.ErrInvalidCredentials)
	})
}

func TestLoginLockout(t *testing.T) {
	t.Parallel()

	keyHash := testLockoutKeyHash("test@example.com")

	t.Run("locked account rejected before user lookup", func(t *testing.T) {
		t.Parallel()

		uc, _, _, lockout := newUserUseCaseWithSessions(t)
		lockedUntil := time.Now().UTC().Add(10 * time.Minute)

		// No GetByEmail expectation: lookup must not happen for locked keys.
		lockout.EXPECT().
			GetAuthLoginLockout(gomock.Any(), keyHash).
			Return(entity.AuthLoginLockout{ConsecutiveFailures: 5, LockedUntil: &lockedUntil}, nil)

		_, err := uc.Login(context.Background(), "test@example.com", "password123")

		require.ErrorIs(t, err, entity.ErrAccountLocked)
	})

	t.Run("failure below threshold increments without lock", func(t *testing.T) {
		t.Parallel()

		uc, repo, _, lockout := newUserUseCaseWithSessions(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		lockout.EXPECT().GetAuthLoginLockout(gomock.Any(), keyHash).Return(entity.AuthLoginLockout{}, nil)
		repo.EXPECT().GetByEmail(gomock.Any(), "test@example.com").Return(entity.User{
			ID: "user-id-123", Email: "test@example.com", PasswordHash: string(hash), EmailVerified: true,
		}, nil)
		lockout.EXPECT().
			GetAuthLoginLockout(gomock.Any(), keyHash).
			Return(entity.AuthLoginLockout{ConsecutiveFailures: 1}, nil)
		lockout.EXPECT().IncrementAuthLoginFailure(gomock.Any(), keyHash, nil).Return(2, nil)

		_, err = uc.Login(context.Background(), "test@example.com", "wrongpassword")

		require.ErrorIs(t, err, entity.ErrInvalidCredentials)
	})

	t.Run("failure at threshold applies lock", func(t *testing.T) {
		t.Parallel()

		uc, repo, _, lockout := newUserUseCaseWithSessions(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		lockout.EXPECT().GetAuthLoginLockout(gomock.Any(), keyHash).Return(entity.AuthLoginLockout{}, nil)
		repo.EXPECT().GetByEmail(gomock.Any(), "test@example.com").Return(entity.User{
			ID: "user-id-123", Email: "test@example.com", PasswordHash: string(hash), EmailVerified: true,
		}, nil)
		lockout.EXPECT().
			GetAuthLoginLockout(gomock.Any(), keyHash).
			Return(entity.AuthLoginLockout{ConsecutiveFailures: 4}, nil)
		lockout.EXPECT().
			IncrementAuthLoginFailure(gomock.Any(), keyHash, gomock.Cond(func(lockedUntil *time.Time) bool {
				return lockedUntil != nil && lockedUntil.After(time.Now().UTC())
			})).
			Return(5, nil)

		_, err = uc.Login(context.Background(), "test@example.com", "wrongpassword")

		require.ErrorIs(t, err, entity.ErrInvalidCredentials)
	})

	t.Run("unknown email still increments counter", func(t *testing.T) {
		t.Parallel()

		uc, repo, _, lockout := newUserUseCaseWithSessions(t)
		missingKey := testLockoutKeyHash("missing@example.com")

		lockout.EXPECT().GetAuthLoginLockout(gomock.Any(), missingKey).Return(entity.AuthLoginLockout{}, nil)
		repo.EXPECT().GetByEmail(gomock.Any(), "missing@example.com").Return(entity.User{}, entity.ErrUserNotFound)
		lockout.EXPECT().GetAuthLoginLockout(gomock.Any(), missingKey).Return(entity.AuthLoginLockout{}, nil)
		lockout.EXPECT().IncrementAuthLoginFailure(gomock.Any(), missingKey, nil).Return(1, nil)

		_, err := uc.Login(context.Background(), "missing@example.com", "password123")

		require.ErrorIs(t, err, entity.ErrInvalidCredentials)
	})

	t.Run("successful login resets counter", func(t *testing.T) {
		t.Parallel()

		uc, repo, sessions, lockout := newUserUseCaseWithSessions(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		lockout.EXPECT().GetAuthLoginLockout(gomock.Any(), keyHash).Return(entity.AuthLoginLockout{ConsecutiveFailures: 3}, nil)
		repo.EXPECT().GetByEmail(gomock.Any(), "test@example.com").Return(entity.User{
			ID: "user-id-123", Email: "test@example.com", PasswordHash: string(hash), EmailVerified: true,
		}, nil)
		lockout.EXPECT().ResetAuthLoginLockout(gomock.Any(), keyHash).Return(nil)
		sessions.EXPECT().CreateAuthSession(gomock.Any(), gomock.Any()).Return(nil)

		_, err = uc.Login(context.Background(), "test@example.com", "password123")

		require.NoError(t, err)
	})

	t.Run("disabled lockout skips all lockout calls", func(t *testing.T) {
		t.Parallel()

		// newUserUseCase has no lockout repo wired, so no lockout expectations
		// are possible; a failed login must still work.
		uc, repo, _ := newUserUseCase(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByEmail(gomock.Any(), "test@example.com").Return(entity.User{
			ID: "user-id-123", Email: "test@example.com", PasswordHash: string(hash), EmailVerified: true,
		}, nil)

		_, err = uc.Login(context.Background(), "test@example.com", "wrongpassword")

		require.ErrorIs(t, err, entity.ErrInvalidCredentials)
	})
}

func TestSetRoleByEmailGuards(t *testing.T) {
	t.Parallel()

	t.Run("self role change rejected", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)

		_, err := uc.SetRoleByEmail(
			context.Background(),
			"admin-id",
			"admin@example.com",
			" Admin@Example.com ",
			entity.UserRoleUser,
		)

		require.ErrorIs(t, err, entity.ErrSelfRoleChange)
	})

	t.Run("last admin error passes through", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)

		repo.EXPECT().
			SetRoleByEmail(context.Background(), "other@example.com", entity.UserRoleUser).
			Return(entity.UserRoleChange{}, entity.ErrLastAdmin)

		_, err := uc.SetRoleByEmail(
			context.Background(),
			"admin-id",
			"admin@example.com",
			"other@example.com",
			entity.UserRoleUser,
		)

		require.ErrorIs(t, err, entity.ErrLastAdmin)
	})
}

func TestCleanupAuthData(t *testing.T) {
	t.Parallel()

	t.Run("delegates with configured retentions", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		repo := NewMockUserRepo(ctrl)
		emailSender := NewMockEmailSender(ctrl)
		maintenance := NewMockAuthMaintenanceRepo(ctrl)
		jwtManager := jwt.New(testJWTSecret, time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)
		uc := user.New(repo, jwtManager, emailSender, user.Options{
			Maintenance: maintenance,
			Cleanup: user.CleanupOptions{
				TokenRetention:   48 * time.Hour,
				SessionRetention: 24 * time.Hour,
				AuditRetention:   0,
			},
		})

		expected := entity.AuthCleanupResult{RateLimits: 7, Sessions: 2}
		maintenance.EXPECT().
			CleanupAuthData(gomock.Any(), gomock.Cond(func(policy repoContract.AuthCleanupPolicy) bool {
				return policy.TokenRetention == 48*time.Hour &&
					policy.SessionRetention == 24*time.Hour &&
					policy.AuditRetention == 0 &&
					!policy.Now.IsZero()
			})).
			Return(expected, nil)

		result, err := uc.CleanupAuthData(context.Background())

		require.NoError(t, err)
		assert.Equal(t, expected, result)
	})

	t.Run("nil maintenance is a no-op", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)

		result, err := uc.CleanupAuthData(context.Background())

		require.NoError(t, err)
		assert.Equal(t, entity.AuthCleanupResult{}, result)
	})
}

func TestVerifyEmailTokenRateLimited(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	rateLimiter := NewMockAuthRateLimitRepo(ctrl)
	uc, _, _ := newUserUseCaseWithAuthDeps(t, rateLimiter, nil)

	rateLimiter.EXPECT().
		IncrementAuthRateLimit(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, limit entity.AuthRateLimit) (entity.AuthRateLimitResult, error) {
			assert.Equal(t, "verify_email", limit.Action)

			return entity.AuthRateLimitResult{Allowed: false, RetryAfter: 42 * time.Second}, nil
		})

	rawToken := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	err := uc.VerifyEmail(context.Background(), rawToken, "", "")

	require.ErrorIs(t, err, entity.ErrAuthRateLimited)

	var rateLimited *entity.AuthRateLimitedError
	require.ErrorAs(t, err, &rateLimited)
	assert.Equal(t, 42*time.Second, rateLimited.RetryAfter)
}

func TestChangePasswordIssuesSession(t *testing.T) {
	t.Parallel()

	uc, repo, sessions, _ := newUserUseCaseWithSessions(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("oldpassword123"), bcrypt.DefaultCost)
	require.NoError(t, err)

	storedUser := entity.User{
		ID: "user-id-123", Email: "test@example.com", PasswordHash: string(hash),
		EmailVerified: true, TokenVersion: 3,
	}
	changedUser := storedUser
	changedUser.TokenVersion = 4

	repo.EXPECT().GetByID(gomock.Any(), "user-id-123").Return(storedUser, nil)
	repo.EXPECT().ChangePassword(gomock.Any(), "user-id-123", gomock.Any()).Return(changedUser, nil)

	var storedSession entity.AuthSession

	sessions.EXPECT().
		CreateAuthSession(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, session entity.AuthSession) error {
			storedSession = session

			return nil
		})

	result, err := uc.ChangePassword(context.Background(), "user-id-123", "oldpassword123", "newpassword123")

	require.NoError(t, err)
	assert.NotEmpty(t, result.AccessToken)
	assert.NotEmpty(t, result.RefreshToken)
	// The new session must carry the bumped token version so the fresh access
	// token survives the revocation of older tokens.
	assert.Equal(t, int64(4), storedSession.TokenVersion)
}
