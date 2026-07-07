package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase/user"
	"github.com/alfariesh/surau-backend/pkg/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newAlertUseCase(
	t *testing.T,
	opts user.AlertOptions,
) (*user.UseCase, *MockUserRepo, *MockAuthAuditRepo, *MockEmailSender) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockUserRepo(ctrl)
	audit := NewMockAuthAuditRepo(ctrl)
	emailSender := NewMockEmailSender(ctrl)
	jwtManager := jwt.New(testJWTSecret, time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)

	uc := user.New(repo, jwtManager, emailSender, user.Options{
		AuditLogger: audit,
		Alert:       opts,
	})

	return uc, repo, audit, emailSender
}

func TestAlertRefreshReuse(t *testing.T) {
	t.Parallel()

	t.Run("emails configured recipients when new events exist", func(t *testing.T) {
		t.Parallel()

		uc, _, audit, emailSender := newAlertUseCase(t, user.AlertOptions{
			Enabled:    true,
			Recipients: []string{"sec@example.com"},
		})

		events := []entity.AuthAuditLog{
			{
				ID:        "a1",
				Event:     "refresh_reuse_detected",
				UserID:    "victim-1",
				ClientIP:  "203.0.113.9",
				Metadata:  map[string]string{"family_id": "fam-1", "revoked_sessions": "2"},
				CreatedAt: mustParseTime(t, "2026-06-10T10:00:00Z"),
			},
		}
		audit.EXPECT().
			ListAuthAuditEventsSince(gomock.Any(), "refresh_reuse_detected", gomock.Any(), gomock.Any()).
			Return(events, nil)

		var sent entity.EmailMessage

		emailSender.EXPECT().
			Send(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg entity.EmailMessage) (entity.EmailSendResult, error) {
				sent = msg

				return entity.EmailSendResult{}, nil
			})

		count, err := uc.AlertRefreshReuse(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 1, count)
		assert.Equal(t, "sec@example.com", sent.To)
		assert.True(t, sent.Critical)
		assert.Contains(t, sent.Text, "victim-1")
		assert.Contains(t, sent.Text, "fam-1")
	})

	t.Run("no events means no email", func(t *testing.T) {
		t.Parallel()

		uc, _, audit, _ := newAlertUseCase(t, user.AlertOptions{
			Enabled:    true,
			Recipients: []string{"sec@example.com"},
		})

		audit.EXPECT().
			ListAuthAuditEventsSince(gomock.Any(), "refresh_reuse_detected", gomock.Any(), gomock.Any()).
			Return(nil, nil)

		count, err := uc.AlertRefreshReuse(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("disabled is a no-op", func(t *testing.T) {
		t.Parallel()

		uc, _, _, _ := newAlertUseCase(t, user.AlertOptions{Enabled: false})

		count, err := uc.AlertRefreshReuse(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("falls back to admin users when no recipients configured", func(t *testing.T) {
		t.Parallel()

		uc, repo, audit, emailSender := newAlertUseCase(t, user.AlertOptions{Enabled: true})

		audit.EXPECT().
			ListAuthAuditEventsSince(gomock.Any(), "refresh_reuse_detected", gomock.Any(), gomock.Any()).
			Return([]entity.AuthAuditLog{{ID: "a1", CreatedAt: mustParseTime(t, "2026-06-10T10:00:00Z")}}, nil)
		repo.EXPECT().
			ListAccounts(gomock.Any(), gomock.Any()).
			Return([]entity.UserAccount{
				{User: entity.User{Email: "admin@example.com", Role: entity.UserRoleAdmin}},
			}, 1, nil)

		recipients := make([]string, 0, 1)

		emailSender.EXPECT().
			Send(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg entity.EmailMessage) (entity.EmailSendResult, error) {
				recipients = append(recipients, msg.To)

				return entity.EmailSendResult{}, nil
			})

		count, err := uc.AlertRefreshReuse(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 1, count)
		assert.Equal(t, "admin@example.com", strings.Join(recipients, ","))
	})
}
