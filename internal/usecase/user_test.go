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
		RateLimiter:              limiter,
		AuditLogger:              auditor,
	})

	return useCase, repo, emailSender
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

				return nil
			},
		)
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) error {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Text, "https://frontend.example.com/verify-email?token=")

				return nil
			},
		)

		u, err := uc.Register(context.Background(), " testuser ", " test@example.com ", "password123")

		require.NoError(t, err)
		assert.NotEmpty(t, u.ID)
		assert.Equal(t, "testuser", u.Username)
		assert.Equal(t, "test@example.com", u.Email)
		assert.Equal(t, entity.UserRoleUser, u.Role)
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
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Return(entity.ErrEmailDeliveryFailed)
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
			Return(expectedUser, nil)

		u, err := uc.SetRoleByEmail(context.Background(), "admin@example.com", entity.UserRoleAdmin)

		require.NoError(t, err)
		assert.Equal(t, expectedUser, u)
	})

	t.Run("reject invalid role", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)

		_, err := uc.SetRoleByEmail(context.Background(), "admin@example.com", "owner")

		require.ErrorIs(t, err, entity.ErrInvalidRole)
	})
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

		claims, err := jwt.New(testJWTSecret, time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience).ParseTokenClaims(token)
		require.NoError(t, err)
		assert.Equal(t, int64(3), claims.TokenVersion)
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

		err := uc.VerifyEmail(context.Background(), rawToken)

		require.NoError(t, err)
	})

	t.Run("invalid token format", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)

		err := uc.VerifyEmail(context.Background(), "not-a-token")

		require.ErrorIs(t, err, entity.ErrInvalidVerificationToken)
	})

	t.Run("token not found", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testVerificationToken()
		repo.EXPECT().GetVerificationTokenByHash(context.Background(), tokenHash).
			Return(entity.EmailVerificationToken{}, entity.ErrVerificationTokenNotFound)

		err := uc.VerifyEmail(context.Background(), rawToken)

		require.ErrorIs(t, err, entity.ErrInvalidVerificationToken)
	})

	t.Run("expired token", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testVerificationToken()
		repo.EXPECT().GetVerificationTokenByHash(context.Background(), tokenHash).
			Return(entity.EmailVerificationToken{ExpiresAt: time.Now().Add(-time.Minute)}, nil)

		err := uc.VerifyEmail(context.Background(), rawToken)

		require.ErrorIs(t, err, entity.ErrInvalidVerificationToken)
	})

	t.Run("used token", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		rawToken, tokenHash := testVerificationToken()
		usedAt := time.Now()
		repo.EXPECT().GetVerificationTokenByHash(context.Background(), tokenHash).
			Return(entity.EmailVerificationToken{UsedAt: &usedAt, ExpiresAt: time.Now().Add(time.Hour)}, nil)

		err := uc.VerifyEmail(context.Background(), rawToken)

		require.ErrorIs(t, err, entity.ErrInvalidVerificationToken)
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

				return nil
			},
		)
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Return(nil)

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
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Return(errors.New("cloudflare down"))
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
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) error {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Text, "https://frontend.example.com/reset-password?token=")

				return nil
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
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Return(errors.New("cloudflare down"))
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
			DoAndReturn(func(_ context.Context, _, _ string, passwordHash string) (entity.User, error) {
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

		err = uc.ChangePassword(context.Background(), "user-id-123", "oldpassword123", "newpassword123")

		require.NoError(t, err)
	})

	t.Run("wrong current password", func(t *testing.T) {
		t.Parallel()

		uc, repo, _ := newUserUseCase(t)
		oldHash, err := bcrypt.GenerateFromPassword([]byte("oldpassword123"), bcrypt.DefaultCost)
		require.NoError(t, err)

		repo.EXPECT().GetByID(context.Background(), "user-id-123").
			Return(entity.User{ID: "user-id-123", PasswordHash: string(oldHash)}, nil)

		err = uc.ChangePassword(context.Background(), "user-id-123", "wrongpassword123", "newpassword123")

		require.ErrorIs(t, err, entity.ErrInvalidCredentials)
	})

	t.Run("invalid new password", func(t *testing.T) {
		t.Parallel()

		uc, _, _ := newUserUseCase(t)

		err := uc.ChangePassword(context.Background(), "user-id-123", "oldpassword123", "short")

		require.ErrorIs(t, err, entity.ErrInvalidAuthInput)
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
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) error {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Subject, "Password")
				assert.Contains(t, message.Text, "Password akun Surau")
				assert.NotContains(t, message.HTML, "href=\"\"")

				return nil
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
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).Return(errors.New("email down"))

		err = uc.ChangePassword(context.Background(), "user-id-123", "oldpassword123", "newpassword123")

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
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) error {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Subject, "diverifikasi")
				assert.Contains(t, message.Text, "Email akun Surau")

				return nil
			},
		)

		err := uc.VerifyEmail(context.Background(), rawToken)

		require.NoError(t, err)
	})

	t.Run("role change sends role changed email", func(t *testing.T) {
		t.Parallel()

		uc, repo, emailSender := newUserUseCaseWithNotifications(t, notifications, nil)
		repo.EXPECT().SetRoleByEmail(context.Background(), "admin@example.com", entity.UserRoleAdmin).
			Return(entity.User{ID: "user-id-123", Username: "admin", Email: "admin@example.com", Role: entity.UserRoleAdmin}, nil)
		emailSender.EXPECT().Send(context.Background(), gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) error {
				assert.Equal(t, "admin@example.com", message.To)
				assert.Contains(t, message.Text, entity.UserRoleAdmin)

				return nil
			},
		)

		_, err := uc.SetRoleByEmail(context.Background(), "admin@example.com", entity.UserRoleAdmin)

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
		emailSender.EXPECT().Send(ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) error {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Text, "Ada login baru")
				assert.Contains(t, message.Text, "203.0.113.10")

				return nil
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
		emailSender.EXPECT().Send(ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, message entity.EmailMessage) error {
				assert.Equal(t, "test@example.com", message.To)
				assert.Contains(t, message.Text, "membatasi percobaan login")

				return nil
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

func testVerificationToken() (string, string) {
	rawTokenBytes := []byte("0123456789abcdef0123456789abcdef")
	rawToken := base64.RawURLEncoding.EncodeToString(rawTokenBytes)
	hash := sha256.Sum256(rawTokenBytes)

	return rawToken, hex.EncodeToString(hash[:])
}

func testPasswordResetToken() (string, string) {
	return testVerificationToken()
}
