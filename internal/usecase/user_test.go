package usecase_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	repoContract "github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/internal/usecase/authmeta"
	"github.com/evrone/go-clean-template/internal/usecase/user"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

const testJWTSecret = "0123456789abcdef0123456789abcdef"

func newUserUseCase(t *testing.T) (*user.UseCase, *MockUserRepo, *MockEmailSender) {
	t.Helper()

	ctrl := gomock.NewController(t)

	repo := NewMockUserRepo(ctrl)
	emailSender := NewMockEmailSender(ctrl)
	jwtManager := jwt.New(testJWTSecret, time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)
	useCase := user.New(repo, jwtManager, emailSender, user.Options{
		VerifyFrontendURL:        "https://frontend.example.com/verify-email",
		VerificationTTL:          time.Hour,
		ResendCooldown:           time.Minute,
		PasswordResetFrontendURL: "https://frontend.example.com/reset-password",
		PasswordResetTTL:         time.Hour,
		PasswordResetCooldown:    time.Minute,
		EmailChangeFrontendURL:   "https://frontend.example.com/change-email",
		EmailChangeTTL:           time.Hour,
		EmailChangeCooldown:      time.Minute,
	})

	return useCase, repo, emailSender
}

func newUserUseCaseWithNotifications(
	t *testing.T,
	notifications user.EmailNotificationOptions,
	rateLimiter *MockAuthRateLimitRepo,
) (*user.UseCase, *MockUserRepo, *MockEmailSender) {
	t.Helper()

	ctrl := gomock.NewController(t)

	repo := NewMockUserRepo(ctrl)
	emailSender := NewMockEmailSender(ctrl)
	jwtManager := jwt.New(testJWTSecret, time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)
	var limiter repoContract.AuthRateLimitRepo
	if rateLimiter != nil {
		limiter = rateLimiter
	}
	useCase := user.New(repo, jwtManager, emailSender, user.Options{
		VerifyFrontendURL:        "https://frontend.example.com/verify-email",
		VerificationTTL:          time.Hour,
		ResendCooldown:           time.Minute,
		PasswordResetFrontendURL: "https://frontend.example.com/reset-password",
		PasswordResetTTL:         time.Hour,
		PasswordResetCooldown:    time.Minute,
		EmailChangeFrontendURL:   "https://frontend.example.com/change-email",
		EmailChangeTTL:           time.Hour,
		EmailChangeCooldown:      time.Minute,
		RateLimiter:              limiter,
		EmailNotifications:       notifications,
	})

	return useCase, repo, emailSender
}

func newUserUseCaseWithAuthDeps(
	t *testing.T,
	rateLimiter *MockAuthRateLimitRepo,
	auditLogger *MockAuthAuditRepo,
) (*user.UseCase, *MockUserRepo, *MockEmailSender) {
	t.Helper()

	ctrl := gomock.NewController(t)

	repo := NewMockUserRepo(ctrl)
	emailSender := NewMockEmailSender(ctrl)
	jwtManager := jwt.New(testJWTSecret, time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)
	var limiter repoContract.AuthRateLimitRepo
	if rateLimiter != nil {
		limiter = rateLimiter
	}
	var auditor repoContract.AuthAuditRepo
	if auditLogger != nil {
		auditor = auditLogger
	}
	useCase := user.New(repo, jwtManager, emailSender, user.Options{
		VerifyFrontendURL:        "https://frontend.example.com/verify-email",
		VerificationTTL:          time.Hour,
		ResendCooldown:           time.Minute,
		PasswordResetFrontendURL: "https://frontend.example.com/reset-password",
		PasswordResetTTL:         time.Hour,
		PasswordResetCooldown:    time.Minute,
		EmailChangeFrontendURL:   "https://frontend.example.com/change-email",
		EmailChangeTTL:           time.Hour,
		EmailChangeCooldown:      time.Minute,
		RateLimiter:              limiter,
		AuditLogger:              auditor,
	})

	return useCase, repo, emailSender
}

type accountOption func(*entity.UserAccount)

func testUserAccount(user entity.User, opts ...accountOption) entity.UserAccount {
	now := time.Now().UTC()
	account := entity.UserAccount{
		User:               user,
		Profile:            entity.DefaultUserProfile(user.ID, now),
		Preferences:        entity.DefaultUserPreferences(user.ID, now),
		OnboardingRequired: true,
	}
	for _, opt := range opts {
		opt(&account)
	}

	return account
}

func expectUserAccount(
	repo *MockUserRepo,
	ctx context.Context,
	user entity.User,
	opts ...accountOption,
) {
	repo.EXPECT().GetAccount(ctx, user.ID).Return(testUserAccount(user, opts...), nil)
}

func expectAnyUserAccount(
	repo *MockUserRepo,
	ctx context.Context,
	user entity.User,
	opts ...accountOption,
) {
	repo.EXPECT().GetAccount(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, userID string) (entity.UserAccount, error) {
			accountUser := user
			accountUser.ID = userID

			return testUserAccount(accountUser, opts...), nil
		},
	)
}

func withPreferredUILang(lang string) accountOption {
	return func(account *entity.UserAccount) {
		account.Preferences.PreferredUILang = lang
	}
}

func withDisplayName(name string) accountOption {
	return func(account *entity.UserAccount) {
		account.Profile.DisplayName = &name
	}
}

func withTimezone(timezone string) accountOption {
	return func(account *entity.UserAccount) {
		account.Profile.Timezone = &timezone
	}
}

func TestRegister(t *testing.T) {
	t.Parallel()

	t.Run("register success", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCase(t)
		repo.EXPECT().StoreWithVerificationToken(context.Background(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, storedUser *entity.User, token *entity.EmailVerificationToken) error {
				assert.Equal(t, "testuser", storedUser.Username)
				assert.Equal(t, "test@example.com", storedUser.Email)
				assert.False(t, storedUser.EmailVerified)
				assert.Equal(t, storedUser.ID, token.UserID)
				assert.Len(t, token.TokenHash, 64)
				assert.NotEmpty(t, token.OTPHash)
				assert.NotNil(t, token.OTPExpiresAt)

				return nil
			},
		)
		expectAnyUserAccount(repo, context.Background(), entity.User{
			Username: "testuser",
			Email:    "test@example.com",
			Role:     entity.UserRoleUser,
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Text, "https://frontend.example.com/verify-email?token=")
				assert.Contains(t, message.Text, "kode 6 digit")

				return entity.EmailSendResult{}, nil
			},
		)

		u, err := uc.Register(context.Background(), " testuser ", " test@example.com ", "password123")

		require.NoError(t, err)
		assert.NotEmpty(t, u.ID)
		assert.Equal(t, "testuser", u.Username)
		assert.Equal(t, "test@example.com", u.Email)
		assert.Equal(t, entity.UserRoleUser, u.Role)
	})

	t.Run("register normalizes email case", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCase(t)
		repo.EXPECT().StoreWithVerificationToken(context.Background(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, storedUser *entity.User, _ *entity.EmailVerificationToken) error {
				assert.Equal(t, "test@example.com", storedUser.Email)

				return nil
			},
		)
		expectAnyUserAccount(repo, context.Background(), entity.User{
			Username: "testuser",
			Email:    "test@example.com",
			Role:     entity.UserRoleUser,
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "test@example.com", message.To)

				return entity.EmailSendResult{}, nil
			},
		)

		u, err := uc.Register(context.Background(), "testuser", " Test@Example.COM ", "password123")

		require.NoError(t, err)
		assert.Equal(t, "test@example.com", u.Email)
	})

	t.Run("register duplicate", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().StoreWithVerificationToken(context.Background(), gomock.Any(), gomock.Any()).
			Return(entity.ErrUserAlreadyExists)

		_, err := uc.Register(context.Background(), "testuser", "test@example.com", "password123")

		require.ErrorIs(t, err, entity.ErrUserAlreadyExists)
	})

	t.Run("register email delivery failure", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCase(t)
		repo.EXPECT().StoreWithVerificationToken(context.Background(), gomock.Any(), gomock.Any()).Return(nil)
		expectAnyUserAccount(repo, context.Background(), entity.User{
			Username: "testuser",
			Email:    "test@example.com",
			Role:     entity.UserRoleUser,
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Return(entity.EmailSendResult{}, entity.ErrEmailDeliveryFailed)
		repo.EXPECT().RevokeUnusedVerificationTokens(context.Background(), gomock.Any()).Return(nil)

		_, err := uc.Register(context.Background(), "testuser", "test@example.com", "password123")

		require.ErrorIs(t, err, entity.ErrEmailDeliveryFailed)
	})

	t.Run("register invalid input", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			username string
			email    string
			password string
		}{
			{
				name:     "short username",
				username: "ab",
				email:    "test@example.com",
				password: "password123",
			},
			{
				name:     "long username",
				username: strings.Repeat("a", 256),
				email:    "test@example.com",
				password: "password123",
			},
			{
				name:     "invalid email",
				username: "testuser",
				email:    "not-an-email",
				password: "password123",
			},
			{
				name:     "short password",
				username: "testuser",
				email:    "test@example.com",
				password: "short",
			},
			{
				name:     "long password",
				username: "testuser",
				email:    "test@example.com",
				password: strings.Repeat("a", 73),
			},
		}

		for _, tc := range tests {
			localTc := tc
			t.Run(localTc.name, func(t *testing.T) {
				t.Parallel()

				uc, _, _ := newUserUseCase(t)

				_, err := uc.Register(context.Background(), localTc.username, localTc.email, localTc.password)

				require.ErrorIs(t, err, entity.ErrInvalidAuthInput)
			})
		}
	})
}

func TestSetRoleByEmail(t *testing.T) {
	t.Parallel()

	t.Run("set admin role", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		expectedUser := entity.User{
			ID:       "user-id-123",
			Username: "admin",
			Email:    "admin@example.com",
			Role:     entity.UserRoleAdmin,
		}

		repo.EXPECT().
			SetRoleByEmail(context.Background(), "admin@example.com", entity.UserRoleAdmin).
			Return(entity.UserRoleChange{
				User:         expectedUser,
				PreviousRole: entity.UserRoleUser,
				NewRole:      entity.UserRoleAdmin,
			}, nil)

		u, err := uc.SetRoleByEmail(context.Background(), "actor-id", "owner@example.com", "admin@example.com", entity.UserRoleAdmin)

		require.NoError(t, err)
		assert.Equal(t, expectedUser, u)
	})

	t.Run("set editor role normalizes input", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		expectedUser := entity.User{
			ID:       "user-id-123",
			Username: "editor",
			Email:    "editor@example.com",
			Role:     entity.UserRoleEditor,
		}

		repo.EXPECT().
			SetRoleByEmail(context.Background(), "editor@example.com", entity.UserRoleEditor).
			Return(entity.UserRoleChange{
				User:         expectedUser,
				PreviousRole: entity.UserRoleUser,
				NewRole:      entity.UserRoleEditor,
			}, nil)

		u, err := uc.SetRoleByEmail(context.Background(), "actor-id", "owner@example.com", " Editor@Example.com ", " EDITOR ")

		require.NoError(t, err)
		assert.Equal(t, expectedUser, u)
	})

	t.Run("reject invalid role", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)

		_, err := uc.SetRoleByEmail(context.Background(), "actor-id", "owner@example.com", "admin@example.com", "owner")

		require.ErrorIs(t, err, entity.ErrInvalidRole)
	})

	t.Run("audit includes actor and old/new role", func(t *testing.T) {
		t.Parallel()

		auditLogger := NewMockAuthAuditRepo(gomock.NewController(t))
		uc, repo, _ := newUserUseCaseWithAuthDeps(t, nil, auditLogger)
		updatedUser := entity.User{
			ID:       "user-id-123",
			Username: "editor",
			Email:    "editor@example.com",
			Role:     entity.UserRoleEditor,
		}
		repo.EXPECT().
			SetRoleByEmail(context.Background(), "editor@example.com", entity.UserRoleEditor).
			Return(entity.UserRoleChange{
				User:         updatedUser,
				PreviousRole: entity.UserRoleUser,
				NewRole:      entity.UserRoleEditor,
			}, nil)
		auditLogger.EXPECT().
			StoreAuthAuditLog(context.Background(), gomock.Cond(func(log entity.AuthAuditLog) bool {
				return log.Event == "role_change" &&
					log.Status == "success" &&
					log.UserID == "user-id-123" &&
					log.Email == "editor@example.com" &&
					log.Metadata["actor_id"] == "admin-id" &&
					log.Metadata["actor_email"] == "admin@example.com" &&
					log.Metadata["old_role"] == entity.UserRoleUser &&
					log.Metadata["new_role"] == entity.UserRoleEditor
			})).
			Return(nil)

		got, err := uc.SetRoleByEmail(
			context.Background(),
			"admin-id",
			"admin@example.com",
			"editor@example.com",
			entity.UserRoleEditor,
		)

		require.NoError(t, err)
		assert.Equal(t, updatedUser, got)
	})
}

func TestAdminUsers(t *testing.T) {
	t.Parallel()

	t.Run("normalizes role filter", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		expected := []entity.UserAccount{testUserAccount(entity.User{ID: "editor-id", Role: entity.UserRoleEditor})}
		verified := true
		repo.EXPECT().
			ListAccounts(context.Background(), repoContract.UserFilter{
				Query:         "editor",
				Role:          entity.UserRoleEditor,
				EmailVerified: &verified,
				Limit:         25,
				Offset:        5,
			}).
			Return(expected, 1, nil)

		got, total, err := uc.AdminUsers(context.Background(), " editor ", " EDITOR ", &verified, 25, 5)

		require.NoError(t, err)
		assert.Equal(t, expected, got)
		assert.Equal(t, 1, total)
	})

	t.Run("rejects invalid role filter", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)

		_, _, err := uc.AdminUsers(context.Background(), "", "owner", nil, 50, 0)

		require.ErrorIs(t, err, entity.ErrInvalidRole)
	})
}

func TestAdminUserActivity(t *testing.T) {
	t.Parallel()

	uc, repo, _ := newUserUseCase(t)
	expected := []entity.UserActivity{{ID: "activity-id", Event: "role_change"}}
	repo.EXPECT().GetByID(context.Background(), "user-id-123").Return(entity.User{ID: "user-id-123"}, nil)
	repo.EXPECT().
		ListUserActivity(context.Background(), repoContract.UserActivityFilter{
			UserID: "user-id-123",
			Limit:  50,
			Offset: 0,
		}).
		Return(expected, 1, nil)

	got, total, err := uc.AdminUserActivity(context.Background(), " user-id-123 ", 0, -10)

	require.NoError(t, err)
	assert.Equal(t, expected, got)
	assert.Equal(t, 1, total)
}

func TestLogin(t *testing.T) {
	t.Parallel()

	t.Run("login success", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		storedUser := entity.User{
			ID: "user-id-123", Username: "testuser",
			Email: "test@example.com", PasswordHash: string(hash), EmailVerified: true, TokenVersion: 3,
		}
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").Return(storedUser, nil)

		token, err := uc.Login(context.Background(), " test@example.com ", "password123")

		require.NoError(t, err)
		assert.NotEmpty(t, token)

		claims, err := jwt.New(testJWTSecret, time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience).ParseTokenClaims(token.AccessToken)
		require.NoError(t, err)
		assert.Equal(t, int64(3), claims.TokenVersion)
	})

	t.Run("login normalizes email case", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		storedUser := entity.User{
			ID: "user-id-123", Username: "testuser",
			Email: "test@example.com", PasswordHash: string(hash), EmailVerified: true,
		}
		// Mixed-case/whitespace input must resolve to the lowercased account.
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").Return(storedUser, nil)

		token, err := uc.Login(context.Background(), " Test@Example.COM ", "password123")

		require.NoError(t, err)
		assert.NotEmpty(t, token)
	})

	t.Run("login wrong password", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		storedUser := entity.User{
			ID: "user-id-123", Username: "testuser",
			Email: "test@example.com", PasswordHash: string(hash), EmailVerified: true,
		}
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").Return(storedUser, nil)

		token, err := uc.Login(context.Background(), "test@example.com", "wrongpassword")

		require.ErrorIs(t, err, entity.ErrInvalidCredentials)
		assert.Empty(t, token)
	})

	t.Run("login unverified email", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		storedUser := entity.User{
			ID: "user-id-123", Username: "testuser",
			Email: "test@example.com", PasswordHash: string(hash), EmailVerified: false,
		}
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").Return(storedUser, nil)

		token, err := uc.Login(context.Background(), "test@example.com", "password123")

		require.ErrorIs(t, err, entity.ErrEmailNotVerified)
		assert.Empty(t, token)
	})

	t.Run("login user not found", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetByEmail(context.Background(), "notfound@example.com").Return(entity.User{}, entity.ErrUserNotFound)

		token, err := uc.Login(context.Background(), "notfound@example.com", "password123")

		require.ErrorIs(t, err, entity.ErrInvalidCredentials)
		assert.Empty(t, token)
	})

	t.Run("login invalid input", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			email    string
			password string
		}{
			{
				name:     "invalid email",
				email:    "not-an-email",
				password: "password123",
			},
			{
				name:     "short password",
				email:    "test@example.com",
				password: "short",
			},
			{
				name:     "long password",
				email:    "test@example.com",
				password: strings.Repeat("a", 73),
			},
		}

		for _, tc := range tests {
			localTc := tc
			t.Run(localTc.name, func(t *testing.T) {
				t.Parallel()

				uc, _, _ := newUserUseCase(t)

				token, err := uc.Login(context.Background(), localTc.email, localTc.password)

				require.ErrorIs(t, err, entity.ErrInvalidAuthInput)
				assert.Empty(t, token)
			})
		}
	})
}

func TestVerifyEmail(t *testing.T) {
	t.Parallel()

	t.Run("verify success", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testVerificationToken()
		storedToken := entity.EmailVerificationToken{
			ID:        "token-id",
			UserID:    "user-id-123",
			TokenHash: tokenHash,
			ExpiresAt: time.Now().Add(time.Hour),
		}
		repo.EXPECT().GetVerificationTokenByHash(context.Background(), tokenHash).Return(storedToken, nil)
		repo.EXPECT().VerifyEmailWithToken(context.Background(), "token-id", "user-id-123").
			Return(entity.User{ID: "user-id-123", EmailVerified: true}, nil)

		err := uc.VerifyEmail(context.Background(), rawToken, "", "")

		require.NoError(t, err)
	})

	t.Run("invalid token format", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)

		err := uc.VerifyEmail(context.Background(), "not-a-token", "", "")

		require.ErrorIs(t, err, entity.ErrInvalidVerificationToken)
	})

	t.Run("token not found", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testVerificationToken()
		repo.EXPECT().GetVerificationTokenByHash(context.Background(), tokenHash).
			Return(entity.EmailVerificationToken{}, entity.ErrVerificationTokenNotFound)

		err := uc.VerifyEmail(context.Background(), rawToken, "", "")

		require.ErrorIs(t, err, entity.ErrInvalidVerificationToken)
	})

	t.Run("expired token", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testVerificationToken()
		repo.EXPECT().GetVerificationTokenByHash(context.Background(), tokenHash).
			Return(entity.EmailVerificationToken{ExpiresAt: time.Now().Add(-time.Minute)}, nil)

		err := uc.VerifyEmail(context.Background(), rawToken, "", "")

		require.ErrorIs(t, err, entity.ErrInvalidVerificationToken)
	})

	t.Run("used token", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testVerificationToken()
		usedAt := time.Now()
		repo.EXPECT().GetVerificationTokenByHash(context.Background(), tokenHash).
			Return(entity.EmailVerificationToken{UsedAt: &usedAt, ExpiresAt: time.Now().Add(time.Hour)}, nil)

		err := uc.VerifyEmail(context.Background(), rawToken, "", "")

		require.ErrorIs(t, err, entity.ErrInvalidVerificationToken)
	})

	t.Run("otp success", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		otpHash, otpExpiresAt := testOTPHash(t, "123456", time.Now().Add(10*time.Minute))
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com"}, nil)
		repo.EXPECT().GetLatestUnusedVerificationToken(context.Background(), "user-id-123").
			Return(entity.EmailVerificationToken{
				ID:           "token-id",
				UserID:       "user-id-123",
				OTPHash:      otpHash,
				OTPExpiresAt: otpExpiresAt,
				ExpiresAt:    time.Now().Add(time.Hour),
			}, nil)
		repo.EXPECT().VerifyEmailWithToken(context.Background(), "token-id", "user-id-123").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com", EmailVerified: true}, nil)

		err := uc.VerifyEmail(context.Background(), "", " test@example.com ", "123456")

		require.NoError(t, err)
	})

	t.Run("otp expired", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		otpHash, otpExpiresAt := testOTPHash(t, "123456", time.Now().Add(-time.Minute))
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com"}, nil)
		repo.EXPECT().GetLatestUnusedVerificationToken(context.Background(), "user-id-123").
			Return(entity.EmailVerificationToken{
				ID:           "token-id",
				UserID:       "user-id-123",
				OTPHash:      otpHash,
				OTPExpiresAt: otpExpiresAt,
				ExpiresAt:    time.Now().Add(time.Hour),
			}, nil)

		err := uc.VerifyEmail(context.Background(), "", "test@example.com", "123456")

		require.ErrorIs(t, err, entity.ErrInvalidVerificationToken)
	})

	t.Run("otp wrong", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		otpHash, otpExpiresAt := testOTPHash(t, "123456", time.Now().Add(10*time.Minute))
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com"}, nil)
		repo.EXPECT().GetLatestUnusedVerificationToken(context.Background(), "user-id-123").
			Return(entity.EmailVerificationToken{
				ID:           "token-id",
				UserID:       "user-id-123",
				OTPHash:      otpHash,
				OTPExpiresAt: otpExpiresAt,
				ExpiresAt:    time.Now().Add(time.Hour),
			}, nil)

		err := uc.VerifyEmail(context.Background(), "", "test@example.com", "654321")

		require.ErrorIs(t, err, entity.ErrInvalidVerificationToken)
	})

	t.Run("ambiguous token and otp input", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)
		rawToken, _ := testVerificationToken()

		err := uc.VerifyEmail(context.Background(), rawToken, "test@example.com", "123456")

		require.ErrorIs(t, err, entity.ErrInvalidAuthInput)
	})
}

func TestResendEmailVerification(t *testing.T) {
	t.Parallel()

	t.Run("not found accepted", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetByEmail(context.Background(), "missing@example.com").Return(entity.User{}, entity.ErrUserNotFound)

		err := uc.ResendEmailVerification(context.Background(), "missing@example.com")

		require.NoError(t, err)
	})

	t.Run("already verified accepted", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com", EmailVerified: true}, nil)

		err := uc.ResendEmailVerification(context.Background(), "test@example.com")

		require.NoError(t, err)
	})

	t.Run("rate limited", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com"}, nil)
		repo.EXPECT().GetLatestUnusedVerificationToken(context.Background(), "user-id-123").
			Return(entity.EmailVerificationToken{SentAt: time.Now()}, nil)

		err := uc.ResendEmailVerification(context.Background(), "test@example.com")

		require.ErrorIs(t, err, entity.ErrVerificationRateLimited)
	})

	t.Run("resend success", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCase(t)
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{ID: "user-id-123", Username: "testuser", Email: "test@example.com"}, nil)
		repo.EXPECT().GetLatestUnusedVerificationToken(context.Background(), "user-id-123").
			Return(entity.EmailVerificationToken{}, entity.ErrVerificationTokenNotFound)
		repo.EXPECT().ReplaceVerificationToken(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, token *entity.EmailVerificationToken) error {
				assert.Equal(t, "user-id-123", token.UserID)
				assert.Len(t, token.TokenHash, 64)
				assert.NotEmpty(t, token.OTPHash)
				assert.NotNil(t, token.OTPExpiresAt)

				return nil
			},
		)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Return(entity.EmailSendResult{}, nil)

		err := uc.ResendEmailVerification(context.Background(), "test@example.com")

		require.NoError(t, err)
	})

	t.Run("send failure", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCase(t)
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{ID: "user-id-123", Username: "testuser", Email: "test@example.com"}, nil)
		repo.EXPECT().GetLatestUnusedVerificationToken(context.Background(), "user-id-123").
			Return(entity.EmailVerificationToken{}, entity.ErrVerificationTokenNotFound)
		repo.EXPECT().ReplaceVerificationToken(context.Background(), gomock.Any()).Return(nil)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Return(entity.EmailSendResult{}, errors.New("cloudflare down"))
		repo.EXPECT().RevokeUnusedVerificationTokens(context.Background(), "user-id-123").Return(nil)

		err := uc.ResendEmailVerification(context.Background(), "test@example.com")

		require.ErrorIs(t, err, entity.ErrEmailDeliveryFailed)
	})
}

func TestForgotPassword(t *testing.T) {
	t.Parallel()

	t.Run("not found accepted", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetByEmail(context.Background(), "missing@example.com").Return(entity.User{}, entity.ErrUserNotFound)

		err := uc.ForgotPassword(context.Background(), "missing@example.com")

		require.NoError(t, err)
	})

	t.Run("rate limited", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com"}, nil)
		repo.EXPECT().GetLatestUnusedPasswordResetToken(context.Background(), "user-id-123").
			Return(entity.PasswordResetToken{SentAt: time.Now()}, nil)

		err := uc.ForgotPassword(context.Background(), "test@example.com")

		require.ErrorIs(t, err, entity.ErrPasswordResetRateLimited)
	})

	t.Run("send success", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCase(t)
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{ID: "user-id-123", Username: "testuser", Email: "test@example.com"}, nil)
		repo.EXPECT().GetLatestUnusedPasswordResetToken(context.Background(), "user-id-123").
			Return(entity.PasswordResetToken{}, entity.ErrPasswordResetTokenNotFound)
		repo.EXPECT().ReplacePasswordResetToken(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, token *entity.PasswordResetToken) error {
				assert.Equal(t, "user-id-123", token.UserID)
				assert.Len(t, token.TokenHash, 64)

				return nil
			},
		)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Text, "https://frontend.example.com/reset-password?token=")

				return entity.EmailSendResult{}, nil
			},
		)

		err := uc.ForgotPassword(context.Background(), "test@example.com")

		require.NoError(t, err)
	})

	t.Run("send failure", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCase(t)
		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{ID: "user-id-123", Username: "testuser", Email: "test@example.com"}, nil)
		repo.EXPECT().GetLatestUnusedPasswordResetToken(context.Background(), "user-id-123").
			Return(entity.PasswordResetToken{}, entity.ErrPasswordResetTokenNotFound)
		repo.EXPECT().ReplacePasswordResetToken(context.Background(), gomock.Any()).Return(nil)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Return(entity.EmailSendResult{}, errors.New("cloudflare down"))
		repo.EXPECT().RevokeUnusedPasswordResetTokens(context.Background(), "user-id-123").Return(nil)

		err := uc.ForgotPassword(context.Background(), "test@example.com")

		require.ErrorIs(t, err, entity.ErrEmailDeliveryFailed)
	})
}

func TestResetPassword(t *testing.T) {
	t.Parallel()

	t.Run("reset success", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testPasswordResetToken()
		storedToken := entity.PasswordResetToken{
			ID:        "token-id",
			UserID:    "user-id-123",
			TokenHash: tokenHash,
			ExpiresAt: time.Now().Add(time.Hour),
		}
		repo.EXPECT().GetPasswordResetTokenByHash(context.Background(), tokenHash).Return(storedToken, nil)
		repo.EXPECT().ResetPasswordWithToken(context.Background(), "token-id", "user-id-123", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _, passwordHash string) (entity.User, error) {
				assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("newpassword123")))

				return entity.User{ID: "user-id-123", EmailVerified: true, PasswordHash: passwordHash}, nil
			})

		err := uc.ResetPassword(context.Background(), rawToken, "newpassword123")

		require.NoError(t, err)
	})

	t.Run("invalid token format", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)

		err := uc.ResetPassword(context.Background(), "not-a-token", "newpassword123")

		require.ErrorIs(t, err, entity.ErrInvalidPasswordResetToken)
	})

	t.Run("token not found", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testPasswordResetToken()
		repo.EXPECT().GetPasswordResetTokenByHash(context.Background(), tokenHash).
			Return(entity.PasswordResetToken{}, entity.ErrPasswordResetTokenNotFound)

		err := uc.ResetPassword(context.Background(), rawToken, "newpassword123")

		require.ErrorIs(t, err, entity.ErrInvalidPasswordResetToken)
	})

	t.Run("expired token", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testPasswordResetToken()
		repo.EXPECT().GetPasswordResetTokenByHash(context.Background(), tokenHash).
			Return(entity.PasswordResetToken{ExpiresAt: time.Now().Add(-time.Minute)}, nil)

		err := uc.ResetPassword(context.Background(), rawToken, "newpassword123")

		require.ErrorIs(t, err, entity.ErrInvalidPasswordResetToken)
	})

	t.Run("used token", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testPasswordResetToken()
		usedAt := time.Now()
		repo.EXPECT().GetPasswordResetTokenByHash(context.Background(), tokenHash).
			Return(entity.PasswordResetToken{UsedAt: &usedAt, ExpiresAt: time.Now().Add(time.Hour)}, nil)

		err := uc.ResetPassword(context.Background(), rawToken, "newpassword123")

		require.ErrorIs(t, err, entity.ErrInvalidPasswordResetToken)
	})

	t.Run("invalid password", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			password string
		}{
			{name: "short password", password: "short"},
			{name: "long password", password: strings.Repeat("a", 73)},
		}

		for _, tc := range tests {
			localTc := tc
			t.Run(localTc.name, func(t *testing.T) {
				t.Parallel()

				uc, _, _ := newUserUseCase(t)
				rawToken, _ := testPasswordResetToken()

				err := uc.ResetPassword(context.Background(), rawToken, localTc.password)

				require.ErrorIs(t, err, entity.ErrInvalidAuthInput)
			})
		}
	})
}

func TestChangePassword(t *testing.T) {
	t.Parallel()

	t.Run("change password success", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		oldHash, err := bcrypt.GenerateFromPassword([]byte("oldpassword123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{ID: "user-id-123", PasswordHash: string(oldHash)}, nil)
		repo.EXPECT().ChangePassword(context.Background(), "user-id-123", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, passwordHash string) (entity.User, error) {
				assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("newpassword123")))

				return entity.User{ID: "user-id-123", PasswordHash: passwordHash, TokenVersion: 2}, nil
			})

		_, err = uc.ChangePassword(context.Background(), "user-id-123", "oldpassword123", "newpassword123")

		require.NoError(t, err)
	})

	t.Run("wrong current password", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		oldHash, err := bcrypt.GenerateFromPassword([]byte("oldpassword123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{ID: "user-id-123", PasswordHash: string(oldHash)}, nil)

		_, err = uc.ChangePassword(context.Background(), "user-id-123", "wrongpassword123", "newpassword123")

		require.ErrorIs(t, err, entity.ErrInvalidCredentials)
	})

	t.Run("invalid new password", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)

		_, err := uc.ChangePassword(context.Background(), "user-id-123", "oldpassword123", "short")

		require.ErrorIs(t, err, entity.ErrInvalidAuthInput)
	})
}

func TestUpdateUserProfile(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	displayName := "Old Name"
	existing := entity.UserAccount{
		User: entity.User{ID: "user-id-123"},
		Profile: entity.UserProfile{
			UserID:                 "user-id-123",
			DisplayName:            &displayName,
			OnboardingVersion:      entity.UserOnboardingVersion,
			PersonalizationEnabled: true,
			CreatedAt:              now,
			UpdatedAt:              now,
		},
		Preferences: entity.DefaultUserPreferences("user-id-123", now),
	}

	t.Run("updates normal profile fields", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetAccount(context.Background(), "user-id-123").Return(existing, nil)
		repo.EXPECT().UpsertProfile(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, profile entity.UserProfile) error {
				require.NotNil(t, profile.DisplayName)
				assert.Equal(t, "New Name", *profile.DisplayName)
				require.NotNil(t, profile.Timezone)
				assert.Equal(t, "Asia/Jakarta", *profile.Timezone)
				require.NotNil(t, profile.CountryCode)
				assert.Equal(t, "ID", *profile.CountryCode)
				assert.False(t, profile.PersonalizationEnabled)

				return nil
			},
		)
		repo.EXPECT().GetAccount(context.Background(), "user-id-123").Return(existing, nil)
		newName := " New Name "
		timezone := " Asia/Jakarta "
		countryCode := "id"
		personalization := false

		_, err := uc.UpdateUserProfile(context.Background(), "user-id-123", entity.UserProfilePatch{
			DisplayName:            &newName,
			Timezone:               &timezone,
			CountryCode:            &countryCode,
			PersonalizationEnabled: &personalization,
		})

		require.NoError(t, err)
	})

	t.Run("rejects empty display name", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetAccount(context.Background(), "user-id-123").Return(existing, nil)
		emptyName := "   "

		_, err := uc.UpdateUserProfile(context.Background(), "user-id-123", entity.UserProfilePatch{
			DisplayName: &emptyName,
		})

		require.ErrorIs(t, err, entity.ErrInvalidUserPreference)
	})
}

func TestEmailChange(t *testing.T) {
	t.Parallel()

	t.Run("request creates token without changing email or token version", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCase(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{
				ID:           "user-id-123",
				Username:     "testuser",
				Email:        "old@example.com",
				PasswordHash: string(hash),
			}, nil)
		repo.EXPECT().GetByEmail(context.Background(), "new@example.com").Return(entity.User{}, entity.ErrUserNotFound)
		repo.EXPECT().GetLatestUnusedEmailChangeToken(context.Background(), "user-id-123").
			Return(entity.EmailChangeToken{}, entity.ErrEmailChangeTokenNotFound)
		repo.EXPECT().ReplaceEmailChangeToken(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, token *entity.EmailChangeToken) error {
				assert.Equal(t, "user-id-123", token.UserID)
				assert.Equal(t, "new@example.com", token.NewEmail)
				assert.Len(t, token.TokenHash, 64)
				assert.NotEmpty(t, token.OTPHash)
				assert.NotNil(t, token.OTPExpiresAt)

				return nil
			},
		)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "old@example.com",
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "new@example.com", message.To)
				assert.Contains(t, message.Text, "https://frontend.example.com/change-email?token=")
				assert.Contains(t, message.Text, "kode 6 digit")

				return entity.EmailSendResult{}, nil
			},
		)

		err = uc.RequestEmailChange(context.Background(), "user-id-123", "password123", " new@example.com ")

		require.NoError(t, err)
	})

	t.Run("request rejects duplicate email", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{ID: "user-id-123", Email: "old@example.com", PasswordHash: string(hash)}, nil)
		repo.EXPECT().GetByEmail(context.Background(), "new@example.com").
			Return(entity.User{ID: "other-user", Email: "new@example.com"}, nil)

		err = uc.RequestEmailChange(context.Background(), "user-id-123", "password123", "new@example.com")

		require.ErrorIs(t, err, entity.ErrUserAlreadyExists)
	})

	t.Run("verify succeeds once and increments token version in repo path", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testEmailChangeToken()
		storedToken := entity.EmailChangeToken{
			ID:        "token-id",
			UserID:    "user-id-123",
			NewEmail:  "new@example.com",
			TokenHash: tokenHash,
			ExpiresAt: time.Now().Add(time.Hour),
		}
		repo.EXPECT().GetEmailChangeTokenByHash(context.Background(), tokenHash).Return(storedToken, nil)
		repo.EXPECT().ChangeEmailWithToken(context.Background(), "token-id", "user-id-123", "new@example.com").
			Return(entity.EmailChangeResult{
				User:     entity.User{ID: "user-id-123", Email: "new@example.com", TokenVersion: 2},
				OldEmail: "old@example.com",
				NewEmail: "new@example.com",
			}, nil)

		_, err := uc.VerifyEmailChange(context.Background(), "user-id-123", rawToken, "")

		require.NoError(t, err)
	})

	t.Run("verify rejects wrong user token", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testEmailChangeToken()
		repo.EXPECT().GetEmailChangeTokenByHash(context.Background(), tokenHash).
			Return(entity.EmailChangeToken{
				ID:        "token-id",
				UserID:    "another-user",
				NewEmail:  "new@example.com",
				TokenHash: tokenHash,
				ExpiresAt: time.Now().Add(time.Hour),
			}, nil)

		_, err := uc.VerifyEmailChange(context.Background(), "user-id-123", rawToken, "")

		require.ErrorIs(t, err, entity.ErrInvalidEmailChangeToken)
	})

	t.Run("verify rejects expired token", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testEmailChangeToken()
		repo.EXPECT().GetEmailChangeTokenByHash(context.Background(), tokenHash).
			Return(entity.EmailChangeToken{
				ID:        "token-id",
				UserID:    "user-id-123",
				NewEmail:  "new@example.com",
				TokenHash: tokenHash,
				ExpiresAt: time.Now().Add(-time.Minute),
			}, nil)

		_, err := uc.VerifyEmailChange(context.Background(), "user-id-123", rawToken, "")

		require.ErrorIs(t, err, entity.ErrInvalidEmailChangeToken)
	})

	t.Run("verify rejects used token", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testEmailChangeToken()
		usedAt := time.Now()
		repo.EXPECT().GetEmailChangeTokenByHash(context.Background(), tokenHash).
			Return(entity.EmailChangeToken{
				ID:        "token-id",
				UserID:    "user-id-123",
				NewEmail:  "new@example.com",
				TokenHash: tokenHash,
				ExpiresAt: time.Now().Add(time.Hour),
				UsedAt:    &usedAt,
			}, nil)

		_, err := uc.VerifyEmailChange(context.Background(), "user-id-123", rawToken, "")

		require.ErrorIs(t, err, entity.ErrInvalidEmailChangeToken)
	})

	t.Run("verify otp succeeds", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		otpHash, otpExpiresAt := testOTPHash(t, "123456", time.Now().Add(10*time.Minute))
		repo.EXPECT().GetLatestUnusedEmailChangeToken(context.Background(), "user-id-123").
			Return(entity.EmailChangeToken{
				ID:           "token-id",
				UserID:       "user-id-123",
				NewEmail:     "new@example.com",
				OTPHash:      otpHash,
				OTPExpiresAt: otpExpiresAt,
				ExpiresAt:    time.Now().Add(time.Hour),
			}, nil)
		repo.EXPECT().ChangeEmailWithToken(context.Background(), "token-id", "user-id-123", "new@example.com").
			Return(entity.EmailChangeResult{
				User:     entity.User{ID: "user-id-123", Email: "new@example.com", TokenVersion: 2},
				OldEmail: "old@example.com",
				NewEmail: "new@example.com",
			}, nil)

		_, err := uc.VerifyEmailChange(context.Background(), "user-id-123", "", "123456")

		require.NoError(t, err)
	})

	t.Run("verify otp rejects used token", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		usedAt := time.Now()
		otpHash, otpExpiresAt := testOTPHash(t, "123456", time.Now().Add(10*time.Minute))
		repo.EXPECT().GetLatestUnusedEmailChangeToken(context.Background(), "user-id-123").
			Return(entity.EmailChangeToken{
				ID:           "token-id",
				UserID:       "user-id-123",
				NewEmail:     "new@example.com",
				OTPHash:      otpHash,
				OTPExpiresAt: otpExpiresAt,
				ExpiresAt:    time.Now().Add(time.Hour),
				UsedAt:       &usedAt,
			}, nil)

		_, err := uc.VerifyEmailChange(context.Background(), "user-id-123", "", "123456")

		require.ErrorIs(t, err, entity.ErrInvalidEmailChangeToken)
	})

	t.Run("verify otp rejects expired otp", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		otpHash, otpExpiresAt := testOTPHash(t, "123456", time.Now().Add(-time.Minute))
		repo.EXPECT().GetLatestUnusedEmailChangeToken(context.Background(), "user-id-123").
			Return(entity.EmailChangeToken{
				ID:           "token-id",
				UserID:       "user-id-123",
				NewEmail:     "new@example.com",
				OTPHash:      otpHash,
				OTPExpiresAt: otpExpiresAt,
				ExpiresAt:    time.Now().Add(time.Hour),
			}, nil)

		_, err := uc.VerifyEmailChange(context.Background(), "user-id-123", "", "123456")

		require.ErrorIs(t, err, entity.ErrInvalidEmailChangeToken)
	})
}

func TestDeleteAccount(t *testing.T) {
	t.Parallel()

	t.Run("deletes after current password check", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com", PasswordHash: string(hash)}, nil)
		repo.EXPECT().DeleteAccount(context.Background(), "user-id-123").Return(nil)

		err = uc.DeleteAccount(context.Background(), "user-id-123", "password123")

		require.NoError(t, err)
	})

	t.Run("rejects wrong current password", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{ID: "user-id-123", PasswordHash: string(hash)}, nil)

		err = uc.DeleteAccount(context.Background(), "user-id-123", "wrongpassword123")

		require.ErrorIs(t, err, entity.ErrInvalidCredentials)
	})
}

func TestAuthEmailNotifications(t *testing.T) {
	t.Parallel()

	notifications := user.EmailNotificationOptions{
		Enabled:                true,
		NewLoginEnabled:        true,
		FailedLoginEnabled:     true,
		PasswordChangedEnabled: true,
		EmailVerifiedEnabled:   true,
		RoleChangedEnabled:     true,
		EmailChangedEnabled:    true,
		AccountDeletedEnabled:  true,
		FailedLoginCooldown:    24 * time.Hour,
	}

	t.Run("reset password sends password changed email", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, nil)
		rawToken, tokenHash := testPasswordResetToken()
		storedToken := entity.PasswordResetToken{
			ID:        "token-id",
			UserID:    "user-id-123",
			TokenHash: tokenHash,
			ExpiresAt: time.Now().Add(time.Hour),
		}
		repo.EXPECT().GetPasswordResetTokenByHash(context.Background(), tokenHash).Return(storedToken, nil)
		repo.EXPECT().ResetPasswordWithToken(context.Background(), "token-id", "user-id-123", gomock.Any()).
			Return(entity.User{ID: "user-id-123", Username: "testuser", Email: "test@example.com", EmailVerified: true}, nil)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:            "user-id-123",
			Username:      "testuser",
			Email:         "test@example.com",
			EmailVerified: true,
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Subject, "Password")
				assert.Contains(t, message.Text, "Password akun Surau")
				assert.NotContains(t, message.Text, "Your Surau password")
				assert.Contains(t, message.Text, "https://www.instagram.com/surauapp")
				assert.Contains(t, message.HTML, "https://cdn.surau.org/icons/duotone/instagram-duotone-rounded.svg")
				assert.Contains(t, message.HTML, "https://cdn.surau.org/icons/duotone/youtube.svg")
				assert.NotContains(t, message.HTML, "href=\"\"")

				return entity.EmailSendResult{}, nil
			},
		)

		err := uc.ResetPassword(context.Background(), rawToken, "newpassword123")

		require.NoError(t, err)
	})

	t.Run("change password sends password changed email and send failure is best effort", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, nil)
		oldHash, err := bcrypt.GenerateFromPassword([]byte("oldpassword123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com", PasswordHash: string(oldHash)}, nil)
		repo.EXPECT().ChangePassword(context.Background(), "user-id-123", gomock.Any()).
			Return(entity.User{ID: "user-id-123", Username: "testuser", Email: "test@example.com"}, nil)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Return(entity.EmailSendResult{}, errors.New("email down"))

		_, err = uc.ChangePassword(context.Background(), "user-id-123", "oldpassword123", "newpassword123")

		require.NoError(t, err)
	})

	t.Run("password changed email follows english ui preference", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, nil)
		oldHash, err := bcrypt.GenerateFromPassword([]byte("oldpassword123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com", PasswordHash: string(oldHash)}, nil)
		repo.EXPECT().ChangePassword(context.Background(), "user-id-123", gomock.Any()).
			Return(entity.User{ID: "user-id-123", Username: "testuser", Email: "test@example.com"}, nil)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		}, withPreferredUILang("en"))
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "Your Surau password was changed", message.Subject)
				assert.Contains(t, message.Text, "Your Surau account password was just changed.")
				assert.NotContains(t, message.Text, "Password akun Surau")
				assert.Contains(t, message.Text, "https://www.facebook.com/surauapp")

				return entity.EmailSendResult{}, nil
			},
		)

		_, err = uc.ChangePassword(context.Background(), "user-id-123", "oldpassword123", "newpassword123")

		require.NoError(t, err)
	})

	t.Run("password changed email supports arabic direction", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, nil)
		oldHash, err := bcrypt.GenerateFromPassword([]byte("oldpassword123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com", PasswordHash: string(oldHash)}, nil)
		repo.EXPECT().ChangePassword(context.Background(), "user-id-123", gomock.Any()).
			Return(entity.User{ID: "user-id-123", Username: "testuser", Email: "test@example.com"}, nil)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		}, withPreferredUILang("ar"))
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Contains(t, message.Subject, "كلمة مرور")
				assert.Contains(t, message.Text, "تم تغيير كلمة مرور حسابك في Surau")
				assert.Contains(t, message.HTML, `<html lang="ar" dir="rtl">`)
				assert.Contains(t, message.HTML, "direction:rtl")

				return entity.EmailSendResult{}, nil
			},
		)

		_, err = uc.ChangePassword(context.Background(), "user-id-123", "oldpassword123", "newpassword123")

		require.NoError(t, err)
	})

	t.Run("verify email sends success email", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, nil)
		rawToken, tokenHash := testVerificationToken()
		storedToken := entity.EmailVerificationToken{
			ID:        "token-id",
			UserID:    "user-id-123",
			TokenHash: tokenHash,
			ExpiresAt: time.Now().Add(time.Hour),
		}
		repo.EXPECT().GetVerificationTokenByHash(context.Background(), tokenHash).Return(storedToken, nil)
		repo.EXPECT().VerifyEmailWithToken(context.Background(), "token-id", "user-id-123").
			Return(entity.User{ID: "user-id-123", Username: "testuser", Email: "test@example.com", EmailVerified: true}, nil)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:            "user-id-123",
			Username:      "testuser",
			Email:         "test@example.com",
			EmailVerified: true,
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Subject, "diverifikasi")
				assert.Contains(t, message.Text, "Email akun Surau")

				return entity.EmailSendResult{}, nil
			},
		)

		err := uc.VerifyEmail(context.Background(), rawToken, "", "")

		require.NoError(t, err)
	})

	t.Run("role change sends role changed email", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, nil)
		repo.EXPECT().SetRoleByEmail(context.Background(), "admin@example.com", entity.UserRoleAdmin).
			Return(entity.UserRoleChange{
				User: entity.User{
					ID:       "user-id-123",
					Username: "admin",
					Email:    "admin@example.com",
					Role:     entity.UserRoleAdmin,
				},
				PreviousRole: entity.UserRoleUser,
				NewRole:      entity.UserRoleAdmin,
			}, nil)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "admin",
			Email:    "admin@example.com",
			Role:     entity.UserRoleAdmin,
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "admin@example.com", message.To)
				assert.Contains(t, message.Text, entity.UserRoleAdmin)

				return entity.EmailSendResult{}, nil
			},
		)

		_, err := uc.SetRoleByEmail(context.Background(), "actor-id", "owner@example.com", "admin@example.com", entity.UserRoleAdmin)

		require.NoError(t, err)
	})

	t.Run("email change sends notifications to old and new email", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, nil)
		rawToken, tokenHash := testEmailChangeToken()
		repo.EXPECT().GetEmailChangeTokenByHash(context.Background(), tokenHash).
			Return(entity.EmailChangeToken{
				ID:        "token-id",
				UserID:    "user-id-123",
				NewEmail:  "new@example.com",
				TokenHash: tokenHash,
				ExpiresAt: time.Now().Add(time.Hour),
			}, nil)
		repo.EXPECT().ChangeEmailWithToken(context.Background(), "token-id", "user-id-123", "new@example.com").
			Return(entity.EmailChangeResult{
				User:     entity.User{ID: "user-id-123", Username: "testuser", Email: "new@example.com"},
				OldEmail: "old@example.com",
				NewEmail: "new@example.com",
			}, nil)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "new@example.com",
		})
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Times(2).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Contains(t, []string{"old@example.com", "new@example.com"}, message.To)
				assert.Contains(t, message.Text, "Email akun Surau")

				return entity.EmailSendResult{}, errors.New("email down")
			},
		)

		_, err := uc.VerifyEmailChange(context.Background(), "user-id-123", rawToken, "")

		require.NoError(t, err)
	})

	t.Run("delete account sends best effort notification", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, nil)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{
				ID:           "user-id-123",
				Username:     "testuser",
				Email:        "test@example.com",
				PasswordHash: string(hash),
			}, nil)
		expectUserAccount(repo, context.Background(), entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		})
		repo.EXPECT().DeleteAccount(context.Background(), "user-id-123").Return(nil)
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Text, "Akun Surau Anda sudah dihapus")

				return entity.EmailSendResult{}, errors.New("email down")
			},
		)

		err = uc.DeleteAccount(context.Background(), "user-id-123", "password123")

		require.NoError(t, err)
	})

	t.Run("login with new fingerprint sends login alert", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, nil)
		ctx := authmeta.With(context.Background(), authmeta.Meta{
			ClientIP:  "203.0.113.10",
			UserAgent: "Mozilla/5.0 SurauTest",
			Transport: "rest",
		})
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByEmail(ctx, "test@example.com").
			Return(entity.User{
				ID:            "user-id-123",
				Username:      "testuser",
				Email:         "test@example.com",
				PasswordHash:  string(hash),
				EmailVerified: true,
			}, nil)
		repo.EXPECT().RecordAuthLoginFingerprint(ctx, gomock.Any()).
			DoAndReturn(func(_ context.Context, fingerprint entity.AuthLoginFingerprint) (bool, error) {
				assert.Equal(t, "user-id-123", fingerprint.UserID)
				assert.NotEmpty(t, fingerprint.FingerprintHash)
				assert.Equal(t, "203.0.113.10", fingerprint.ClientIP)
				assert.Equal(t, "Mozilla/5.0 SurauTest", fingerprint.UserAgent)

				return true, nil
			})
		expectUserAccount(repo, ctx, entity.User{
			ID:            "user-id-123",
			Username:      "testuser",
			Email:         "test@example.com",
			EmailVerified: true,
		}, withDisplayName("Ahmad"), withTimezone("Asia/Jakarta"))
		emailSender.EXPECT().Send(ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Text, "Ada login baru")
				assert.Contains(t, message.Text, "Assalamu'alaikum, Ahmad")
				assert.Contains(t, message.Text, "203.0.113.10")
				assert.Contains(t, message.Text, "Asia/Jakarta")

				return entity.EmailSendResult{}, nil
			},
		)

		token, err := uc.Login(ctx, "test@example.com", "password123")

		require.NoError(t, err)
		assert.NotEmpty(t, token)
	})

	t.Run("login with known fingerprint skips login alert", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCaseWithNotifications(t, notifications, nil)
		ctx := authmeta.With(context.Background(), authmeta.Meta{
			ClientIP:  "203.0.113.10",
			UserAgent: "Mozilla/5.0 SurauTest",
			Transport: "rest",
		})
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByEmail(ctx, "test@example.com").
			Return(entity.User{
				ID:            "user-id-123",
				Email:         "test@example.com",
				PasswordHash:  string(hash),
				EmailVerified: true,
			}, nil)
		repo.EXPECT().RecordAuthLoginFingerprint(ctx, gomock.Any()).Return(false, nil)

		token, err := uc.Login(ctx, "test@example.com", "password123")

		require.NoError(t, err)
		assert.NotEmpty(t, token)
	})

	t.Run("login rate limited for existing email sends failed login email once per cooldown", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		rateLimiter := NewMockAuthRateLimitRepo(ctrl)
		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, rateLimiter)
		ctx := authmeta.With(context.Background(), authmeta.Meta{ClientIP: "203.0.113.10", Transport: "rest"})

		rateLimiter.EXPECT().IncrementAuthRateLimit(ctx, gomock.Any()).
			Return(entity.AuthRateLimitResult{Allowed: false}, nil)
		repo.EXPECT().GetByEmail(ctx, "test@example.com").
			Return(entity.User{ID: "user-id-123", Username: "testuser", Email: "test@example.com"}, nil)
		repo.EXPECT().AcquireAuthNotificationCooldown(ctx, gomock.Any()).
			DoAndReturn(func(_ context.Context, cooldown entity.AuthNotificationCooldown) (bool, error) {
				assert.Equal(t, "failed_login", cooldown.Event)
				assert.NotEmpty(t, cooldown.KeyHash)
				assert.True(t, cooldown.ExpiresAt.After(time.Now()))

				return true, nil
			})
		expectUserAccount(repo, ctx, entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		})
		emailSender.EXPECT().Send(ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Text, "membatasi percobaan login")

				return entity.EmailSendResult{}, nil
			},
		)

		token, err := uc.Login(ctx, "test@example.com", "password123")

		require.ErrorIs(t, err, entity.ErrAuthRateLimited)
		assert.Empty(t, token)
	})

	t.Run("login rate limited during failed login cooldown skips email", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		rateLimiter := NewMockAuthRateLimitRepo(ctrl)
		uc, repo, _ := newUserUseCaseWithNotifications(t, notifications, rateLimiter)
		ctx := authmeta.With(context.Background(), authmeta.Meta{ClientIP: "203.0.113.10", Transport: "rest"})

		rateLimiter.EXPECT().IncrementAuthRateLimit(ctx, gomock.Any()).
			Return(entity.AuthRateLimitResult{Allowed: false}, nil)
		repo.EXPECT().GetByEmail(ctx, "test@example.com").
			Return(entity.User{ID: "user-id-123", Email: "test@example.com"}, nil)
		repo.EXPECT().AcquireAuthNotificationCooldown(ctx, gomock.Any()).Return(false, nil)

		token, err := uc.Login(ctx, "test@example.com", "password123")

		require.ErrorIs(t, err, entity.ErrAuthRateLimited)
		assert.Empty(t, token)
	})

	t.Run("login rate limited for missing email stays silent", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		rateLimiter := NewMockAuthRateLimitRepo(ctrl)
		uc, repo, _ := newUserUseCaseWithNotifications(t, notifications, rateLimiter)
		ctx := authmeta.With(context.Background(), authmeta.Meta{ClientIP: "203.0.113.10", Transport: "rest"})

		rateLimiter.EXPECT().IncrementAuthRateLimit(ctx, gomock.Any()).
			Return(entity.AuthRateLimitResult{Allowed: false}, nil)
		repo.EXPECT().GetByEmail(ctx, "missing@example.com").
			Return(entity.User{}, entity.ErrUserNotFound)

		token, err := uc.Login(ctx, "missing@example.com", "password123")

		require.ErrorIs(t, err, entity.ErrAuthRateLimited)
		assert.Empty(t, token)
	})
}

func TestAuthRateLimitAndAudit(t *testing.T) {
	t.Parallel()

	t.Run("login rate limited before password check", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		rateLimiter := NewMockAuthRateLimitRepo(ctrl)
		uc, _, _ := newUserUseCaseWithAuthDeps(t, rateLimiter, nil)

		rateLimiter.EXPECT().
			IncrementAuthRateLimit(context.Background(), gomock.Any()).
			DoAndReturn(func(_ context.Context, limit entity.AuthRateLimit) (entity.AuthRateLimitResult, error) {
				assert.Equal(t, "login", limit.Action)
				assert.Positive(t, limit.MaxAttempts)
				assert.NotEmpty(t, limit.KeyHash)

				return entity.AuthRateLimitResult{Allowed: false}, nil
			})

		token, err := uc.Login(context.Background(), "test@example.com", "password123")

		require.ErrorIs(t, err, entity.ErrAuthRateLimited)
		assert.Empty(t, token)
	})

	t.Run("audit failure does not block login", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		auditLogger := NewMockAuthAuditRepo(ctrl)
		uc, repo, _ := newUserUseCaseWithAuthDeps(t, nil, auditLogger)
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByEmail(context.Background(), "test@example.com").
			Return(entity.User{
				ID:            "user-id-123",
				Email:         "test@example.com",
				PasswordHash:  string(hash),
				EmailVerified: true,
				TokenVersion:  4,
			}, nil)
		auditLogger.EXPECT().
			StoreAuthAuditLog(context.Background(), gomock.Any()).
			DoAndReturn(func(_ context.Context, log entity.AuthAuditLog) error {
				assert.Equal(t, "login", log.Event)
				assert.Equal(t, "success", log.Status)
				assert.Equal(t, "user-id-123", log.UserID)

				return errors.New("audit down")
			})

		token, err := uc.Login(context.Background(), "test@example.com", "password123")

		require.NoError(t, err)
		assert.NotEmpty(t, token)
	})
}

func TestGetUser(t *testing.T) {
	t.Parallel()

	expectedUser := entity.User{
		ID:       "user-id-123",
		Username: "testuser",
		Email:    "test@example.com",
	}

	t.Run("get user success", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetByID(context.Background(), "user-id-123").Return(expectedUser, nil)

		u, err := uc.GetUser(context.Background(), "user-id-123")

		require.NoError(t, err)
		assert.Equal(t, expectedUser, u)
	})

	t.Run("get user not found", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetByID(context.Background(), "missing-id").Return(entity.User{}, entity.ErrUserNotFound)

		_, err := uc.GetUser(context.Background(), "missing-id")

		require.ErrorIs(t, err, entity.ErrUserNotFound)
	})
}

func TestGetUser_GenericError(t *testing.T) {
	t.Parallel()

	uc, repo, _ := newUserUseCase(t)

	repo.EXPECT().GetByID(context.Background(), "user-id-123").Return(entity.User{}, errInternalServErr)

	_, err := uc.GetUser(context.Background(), "user-id-123")

	require.Error(t, err)
	require.ErrorIs(t, err, errInternalServErr)
}

func TestGetUserAccount(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expected := entity.UserAccount{
		User: entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		},
		Profile:            entity.DefaultUserProfile("user-id-123", now),
		Preferences:        entity.DefaultUserPreferences("user-id-123", now),
		OnboardingRequired: true,
	}

	uc, repo, _ := newUserUseCase(t)
	repo.EXPECT().GetAccount(context.Background(), "user-id-123").Return(expected, nil)

	got, err := uc.GetUserAccount(context.Background(), "user-id-123")

	require.NoError(t, err)
	assert.Equal(t, expected, got)
	assert.Equal(t, "id", got.Preferences.PreferredContentLang)
	assert.True(t, got.OnboardingRequired)
}

func TestCompleteOnboarding(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	existing := entity.UserAccount{
		User: entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		},
		Profile:            entity.DefaultUserProfile("user-id-123", now),
		Preferences:        entity.DefaultUserPreferences("user-id-123", now),
		OnboardingRequired: true,
	}

	t.Run("stores normalized preferences", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetAccount(context.Background(), "user-id-123").Return(existing, nil)
		repo.EXPECT().UpsertProfile(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, profile entity.UserProfile) error {
				require.NotNil(t, profile.OnboardingCompletedAt)
				assert.Equal(t, entity.UserOnboardingVersion, profile.OnboardingVersion)
				assert.Equal(t, "ID", *profile.CountryCode)

				return nil
			},
		)
		repo.EXPECT().UpsertPreferences(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, preferences entity.UserPreferences) error {
				assert.Equal(t, "en", preferences.PreferredUILang)
				assert.Equal(t, "id", preferences.PreferredContentLang)
				assert.Equal(t, []string{"id", "en"}, preferences.FallbackLangs)
				assert.Equal(t, entity.UserArabicLevelBasic, preferences.ArabicLevel)
				assert.Equal(t, entity.UserReaderModeArabicTranslation, preferences.ReaderMode)
				assert.Equal(t, []string{"tafsir", "hadith", "arabic_language"}, preferences.Interests)

				return nil
			},
		)
		repo.EXPECT().GetAccount(context.Background(), "user-id-123").Return(existing, nil)
		countryCode := "id"
		dailyGoal := 15

		_, err := uc.CompleteOnboarding(context.Background(), "user-id-123", entity.UserOnboarding{
			CountryCode:          &countryCode,
			PreferredUILang:      "en-US",
			PreferredContentLang: "id",
			FallbackLangs:        []string{"id", "en", "id"},
			ArabicLevel:          "basic",
			ReaderMode:           "arabic_translation",
			Interests:            []string{"tafsir", "hadis", "bahasa_arab"},
			DailyGoalMinutes:     &dailyGoal,
		})

		require.NoError(t, err)
	})

	t.Run("rejects unsupported language", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		repo.EXPECT().GetAccount(context.Background(), "user-id-123").Return(existing, nil)

		_, err := uc.CompleteOnboarding(context.Background(), "user-id-123", entity.UserOnboarding{
			PreferredContentLang: "fr",
		})

		require.ErrorIs(t, err, entity.ErrUnsupportedLanguage)
	})
}

func TestUpdateUserPreferences(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	existing := entity.UserAccount{
		User: entity.User{ID: "user-id-123"},
		Profile: entity.UserProfile{
			UserID:                 "user-id-123",
			OnboardingVersion:      entity.UserOnboardingVersion,
			OnboardingCompletedAt:  &now,
			PersonalizationEnabled: true,
			CreatedAt:              now,
			UpdatedAt:              now,
		},
		Preferences: entity.DefaultUserPreferences("user-id-123", now),
	}

	uc, repo, _ := newUserUseCase(t)
	repo.EXPECT().GetAccount(context.Background(), "user-id-123").Return(existing, nil)
	repo.EXPECT().UpsertPreferences(context.Background(), gomock.Any()).DoAndReturn(
		func(_ context.Context, preferences entity.UserPreferences) error {
			assert.Equal(t, "ar", preferences.PreferredContentLang)
			assert.Equal(t, entity.UserReaderModeArabicOnly, preferences.ReaderMode)

			return nil
		},
	)
	repo.EXPECT().GetAccount(context.Background(), "user-id-123").Return(existing, nil)
	lang := "ar"
	readerMode := entity.UserReaderModeArabicOnly

	_, err := uc.UpdateUserPreferences(context.Background(), "user-id-123", entity.UserPreferencesPatch{
		PreferredContentLang: &lang,
		ReaderMode:           &readerMode,
	})

	require.NoError(t, err)
}

func testVerificationToken() (string, string) {
	rawTokenBytes := []byte("0123456789abcdef0123456789abcdef")
	rawToken := base64.RawURLEncoding.EncodeToString(rawTokenBytes)
	hash := sha256.Sum256(rawTokenBytes)

	return rawToken, hex.EncodeToString(hash[:])
}

func testPasswordResetToken() (string, string) {
	return testVerificationToken()
}

func testEmailChangeToken() (string, string) {
	return testVerificationToken()
}

func testOTPHash(t *testing.T, otp string, expiresAt time.Time) (string, *time.Time) {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(otp), bcrypt.DefaultCost)
	require.NoError(t, err)

	return string(hash), &expiresAt
}
