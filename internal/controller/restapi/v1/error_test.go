package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/evrone/go-clean-template/internal/controller/restapi/middleware"
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
