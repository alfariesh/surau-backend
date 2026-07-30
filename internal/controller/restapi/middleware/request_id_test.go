package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/requestmeta"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestIDPropagatesIntoUserContext(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.RequestID())
	app.Get("/", func(ctx *fiber.Ctx) error {
		return ctx.SendString(requestmeta.RequestID(ctx.UserContext()))
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	request.Header.Set("X-Request-ID", "request-u0-http")

	response, err := app.Test(request)
	require.NoError(t, err)

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, "request-u0-http", string(body))
	assert.Equal(t, "request-u0-http", response.Header.Get("X-Request-ID"))
}
