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
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/auth/sessions", nil))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "request %d should pass", i+1)
	}

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/auth/sessions", nil))
	require.NoError(t, err)
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
		req := httptest.NewRequest(http.MethodGet, "/auth/sessions", nil)
		req.Header.Set("X-Test-User", "user-a")
		resp, err := app.Test(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	// user-a is exhausted, user-b still has a full budget.
	reqA := httptest.NewRequest(http.MethodGet, "/auth/sessions", nil)
	reqA.Header.Set("X-Test-User", "user-a")
	respA, err := app.Test(reqA)
	require.NoError(t, err)
	assert.Equal(t, http.StatusTooManyRequests, respA.StatusCode)

	reqB := httptest.NewRequest(http.MethodGet, "/auth/sessions", nil)
	reqB.Header.Set("X-Test-User", "user-b")
	respB, err := app.Test(reqB)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respB.StatusCode)
}
