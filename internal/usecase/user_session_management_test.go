package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestListSessions(t *testing.T) {
	t.Parallel()

	t.Run("returns active sessions from repo", func(t *testing.T) {
		t.Parallel()

		uc, _, sessions, _ := newUserUseCaseWithSessions(t)
		want := []entity.AuthSession{
			{ID: "s1", FamilyID: "f1", UserID: "user-id-123", UserAgent: "iPhone", ClientIP: "203.0.113.1"},
			{ID: "s2", FamilyID: "f2", UserID: "user-id-123", UserAgent: "Chrome", ClientIP: "203.0.113.2"},
		}
		sessions.EXPECT().ListActiveAuthSessions(gomock.Any(), "user-id-123").Return(want, nil)

		got, err := uc.ListSessions(context.Background(), "user-id-123")

		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("blank user id rejected", func(t *testing.T) {
		t.Parallel()

		uc, _, _, _ := newUserUseCaseWithSessions(t)

		_, err := uc.ListSessions(context.Background(), "  ")

		require.ErrorIs(t, err, entity.ErrInvalidAuthInput)
	})
}

func TestRevokeSession(t *testing.T) {
	t.Parallel()

	const validSessionID = "11111111-1111-1111-1111-111111111111"

	t.Run("revokes a session by id scoped to user", func(t *testing.T) {
		t.Parallel()

		uc, _, sessions, _ := newUserUseCaseWithSessions(t)
		sessions.EXPECT().RevokeAuthSessionByID(gomock.Any(), "user-id-123", validSessionID).Return(nil)

		require.NoError(t, uc.RevokeSession(context.Background(), "user-id-123", validSessionID))
	})

	t.Run("missing session maps to not found", func(t *testing.T) {
		t.Parallel()

		uc, _, sessions, _ := newUserUseCaseWithSessions(t)
		sessions.EXPECT().
			RevokeAuthSessionByID(gomock.Any(), "user-id-123", validSessionID).
			Return(entity.ErrAuthSessionNotFound)

		err := uc.RevokeSession(context.Background(), "user-id-123", validSessionID)

		require.ErrorIs(t, err, entity.ErrAuthSessionNotFound)
	})

	t.Run("blank inputs rejected without touching repo", func(t *testing.T) {
		t.Parallel()

		uc, _, _, _ := newUserUseCaseWithSessions(t)

		require.ErrorIs(t, uc.RevokeSession(context.Background(), "user-id-123", " "), entity.ErrInvalidAuthInput)
		require.ErrorIs(t, uc.RevokeSession(context.Background(), "", "sess-1"), entity.ErrInvalidAuthInput)
	})

	t.Run("malformed (non-UUID) id maps to not found without touching repo", func(t *testing.T) {
		t.Parallel()

		uc, _, _, _ := newUserUseCaseWithSessions(t)

		// No RevokeAuthSessionByID expectation: a non-UUID id must short-circuit
		// before the repo call, so the DB never sees an invalid UUID.
		err := uc.RevokeSession(context.Background(), "user-id-123", "does-not-exist")

		require.ErrorIs(t, err, entity.ErrAuthSessionNotFound)
	})
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)

	return parsed
}
