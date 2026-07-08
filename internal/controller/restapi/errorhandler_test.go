package restapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type envelopeBody struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

var errLeakyInternal = errors.New("secret database dsn in panic value")

func newErrorHandlerApp(t *testing.T) *fiber.App {
	t.Helper()

	return fiber.New(fiber.Config{ErrorHandler: EnvelopeErrorHandler(logger.New("error"))})
}

func TestEnvelopeErrorHandlerNormalizesFiberErrors(t *testing.T) {
	t.Parallel()

	app := newErrorHandlerApp(t)
	app.Get("/teapot", func(*fiber.Ctx) error {
		return fiber.NewError(http.StatusNotFound, "Cannot GET /some/dynamic/path")
	})

	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/teapot", nil))
	require.NoError(t, err)

	defer resp.Body.Close()

	var body envelopeBody
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	// The frozen message, NOT fiber's per-path text (which would mint a new code).
	assert.Equal(t, "not found", body.Error)
	assert.Equal(t, "not_found", body.Code)
}

// TestEnvelopeErrorHandlerNeverEchoesInternalErrors pins the security fix:
// an unhandled error (e.g. a recovered panic propagated by the recovery
// middleware) must not leak its text into the 500 body.
func TestEnvelopeErrorHandlerNeverEchoesInternalErrors(t *testing.T) {
	t.Parallel()

	app := newErrorHandlerApp(t)
	app.Get("/boom", func(*fiber.Ctx) error {
		return errLeakyInternal
	})

	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", nil))
	require.NoError(t, err)

	defer resp.Body.Close()

	raw, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.NotContains(t, string(raw), "secret database dsn")
	assert.Contains(t, string(raw), `"code":"internal_server_error"`)
}

// TestEnvelopeErrorHandlerStatusMessages pins the frozen normalization per
// status (fiber converts fasthttp-level failures like body-limit into
// *fiber.Error before invoking the handler; request_id may be empty on
// those pre-routing paths).
func TestEnvelopeErrorHandlerStatusMessages(t *testing.T) {
	t.Parallel()

	cases := []struct {
		status   int
		wantMsg  string
		wantCode string
	}{
		{http.StatusRequestEntityTooLarge, "request entity too large", "request_entity_too_large"},
		{http.StatusMethodNotAllowed, "method not allowed", "method_not_allowed"},
		{http.StatusTooManyRequests, "too many requests", "too_many_requests"},
		{http.StatusBadRequest, "invalid request", "invalid_request"},
		{http.StatusBadGateway, "internal server error", "internal_server_error"},
	}

	for _, tc := range cases {
		app := newErrorHandlerApp(t)
		app.Get("/err", func(*fiber.Ctx) error {
			return fiber.NewError(tc.status, "Framework Specific Text")
		})

		resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/err", nil))
		require.NoError(t, err, "status %d", tc.status)

		var body envelopeBody
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		resp.Body.Close()

		assert.Equal(t, tc.status, resp.StatusCode)
		assert.Equal(t, tc.wantMsg, body.Error, "status %d", tc.status)
		assert.Equal(t, tc.wantCode, body.Code, "status %d", tc.status)
	}
}
