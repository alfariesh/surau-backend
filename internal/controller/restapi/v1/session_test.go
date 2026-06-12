package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListSessionsRoute(t *testing.T) {
	t.Parallel()

	t.Run("returns sessions and flags the current device", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
		app := newAuthTestApp(&fakeAuthUser{sessions: []entity.AuthSession{
			{ID: "s1", FamilyID: "fam-current", UserAgent: "iPhone", ClientIP: "203.0.113.1", LastUsedAt: now, ExpiresAt: now},
			{ID: "s2", FamilyID: "fam-other", UserAgent: "Chrome", ClientIP: "203.0.113.2", LastUsedAt: now, ExpiresAt: now},
		}})

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/sessions", http.NoBody)
		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body := readTestBody(t, resp)
		assert.Contains(t, body, `"id":"s1"`)
		assert.Contains(t, body, `"user_agent":"iPhone"`)
		// s1 matches the access token's session family, s2 does not.
		assert.Contains(t, body, `"id":"s1","user_agent":"iPhone","client_ip":"203.0.113.1"`)
		assert.Contains(t, body, `"is_current":true`)
		assert.Contains(t, body, `"is_current":false`)
		// Sensitive fields are never serialized.
		assert.NotContains(t, body, "refresh_token_hash")
		assert.NotContains(t, body, "family_id")
	})

	t.Run("internal error surfaces as 500", func(t *testing.T) {
		t.Parallel()

		app := newAuthTestApp(&fakeAuthUser{listSessionsErr: assertAnyError})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/sessions", http.NoBody)

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}

func TestRevokeSessionRoute(t *testing.T) {
	t.Parallel()

	t.Run("revokes a session and echoes the path id to the usecase", func(t *testing.T) {
		t.Parallel()

		user := &fakeAuthUser{}
		app := newAuthTestApp(user)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/auth/sessions/sess-42", http.NoBody)
		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, readTestBody(t, resp), `"session_revoked":true`)
		assert.Equal(t, "user-id-123", user.revokeSessionUser)
		assert.Equal(t, "sess-42", user.revokeSessionID)
	})

	t.Run("unknown session returns 404", func(t *testing.T) {
		t.Parallel()

		app := newAuthTestApp(&fakeAuthUser{revokeSessionErr: entity.ErrAuthSessionNotFound})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/auth/sessions/ghost", http.NoBody)

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

// assertAnyError is a sentinel non-domain error used to drive the 500 path;
// identity matters, so it must stay a single package-level value.
//
//nolint:gochecknoglobals // sentinel error compared by identity across tests
var assertAnyError = &genericError{}

type genericError struct{}

func (*genericError) Error() string { return "boom" }
