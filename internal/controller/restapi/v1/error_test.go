package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorResponseIncludesStructuredFields(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.RequestID())
	app.Get("/error", func(ctx *fiber.Ctx) error {
		return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/error", nil)
	req.Header.Set("X-Request-ID", "req-test")
	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	var body struct {
		Error     string `json:"error"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "unsupported language", body.Error)
	assert.Equal(t, "unsupported_language", body.Code)
	assert.Equal(t, "unsupported language", body.Message)
	assert.Equal(t, "req-test", body.RequestID)
	assert.Equal(t, "req-test", resp.Header.Get("X-Request-ID"))
}

type errorEnvelopeBody struct {
	Error      string `json:"error"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Details    any    `json:"details"`
	RetryAfter int64  `json:"retry_after"`
	RequestID  string `json:"request_id"`
}

func decodeEnvelope(t *testing.T, resp *http.Response) errorEnvelopeBody {
	t.Helper()

	defer resp.Body.Close()

	var body errorEnvelopeBody
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	return body
}

// TestErrorResponseWithDetailsKeepsCodeStable pins the F1-D rule for
// variable error text: the message (and code) stay fixed, instance detail
// rides in details.
func TestErrorResponseWithDetailsKeepsCodeStable(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.RequestID())
	app.Get("/error", func(ctx *fiber.Ctx) error {
		return errorResponseWithDetails(ctx, http.StatusBadRequest, "invalid email template", "invalid email template: missing subject line 3")
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/error", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)

	body := decodeEnvelope(t, resp)

	assert.Equal(t, "invalid email template", body.Error)
	assert.Equal(t, "invalid_email_template", body.Code)
	assert.Equal(t, "invalid email template: missing subject line 3", body.Details)
	assert.NotEmpty(t, body.RequestID)
}

// TestLimiterLimitReachedUsesEnvelope: the in-process rate limiters answer
// 429 with the standard envelope and mirror Retry-After into the body.
func TestLimiterLimitReachedUsesEnvelope(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.RequestID())
	app.Get("/limited", func(ctx *fiber.Ctx) error {
		ctx.Set(fiber.HeaderRetryAfter, "42") // fiber's limiter sets this before LimitReached

		return limiterLimitReached(ctx)
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/limited", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)

	body := decodeEnvelope(t, resp)

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, "too many requests", body.Error)
	assert.Equal(t, "too_many_requests", body.Code)
	assert.Equal(t, int64(42), body.RetryAfter)
	assert.NotEmpty(t, body.RequestID)
}
