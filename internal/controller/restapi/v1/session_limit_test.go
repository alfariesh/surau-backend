package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSessionLimiterTestApp(userID string) *fiber.App {
	app := fiber.New()
	app.Use(func(ctx *fiber.Ctx) error {
		if userID != "" {
			ctx.Locals("userID", userID)
		}

		return ctx.Next()
	})
	app.Get("/auth/sessions", newSessionLimiter(), func(ctx *fiber.Ctx) error {
		return ctx.SendStatus(http.StatusOK)
	})

	return app
}

func TestSessionLimiterCapsPerUser(t *testing.T) {
	t.Parallel()

	app := newSessionLimiterTestApp("user-a")

	for i := range sessionRequestsPerMinute {
		resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/sessions", http.NoBody))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "request %d should pass", i+1)
		resp.Body.Close()
	}

	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/sessions", http.NoBody))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestSessionLimiterKeysAreIsolatedPerUser(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", ctx.Get("X-Test-User"))

		return ctx.Next()
	})
	app.Get("/auth/sessions", newSessionLimiter(), func(ctx *fiber.Ctx) error {
		return ctx.SendStatus(http.StatusOK)
	})

	for range sessionRequestsPerMinute {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/sessions", http.NoBody)
		req.Header.Set("X-Test-User", "user-a")
		resp, err := app.Test(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// user-a is exhausted, user-b still has a full budget.
	reqA := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/sessions", http.NoBody)
	reqA.Header.Set("X-Test-User", "user-a")
	respA, err := app.Test(reqA)
	require.NoError(t, err)

	defer respA.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, respA.StatusCode)

	reqB := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/sessions", http.NoBody)
	reqB.Header.Set("X-Test-User", "user-b")
	respB, err := app.Test(reqB)
	require.NoError(t, err)

	defer respB.Body.Close()

	assert.Equal(t, http.StatusOK, respB.StatusCode)
}
