package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicCacheSetsValidatorsAndSupportsNotModified(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.PublicCache())
	app.Get("/books", func(ctx *fiber.Ctx) error {
		return ctx.JSON(fiber.Map{
			"title":      "Book",
			"updated_at": "2026-01-02T03:04:05Z",
		})
	})

	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/books", nil))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "public, max-age=300, stale-while-revalidate=86400", resp.Header.Get("Cache-Control"))
	assert.NotEmpty(t, resp.Header.Get("ETag"))
	assert.Equal(t, "Fri, 02 Jan 2026 03:04:05 GMT", resp.Header.Get("Last-Modified"))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/books", nil)
	req.Header.Set("If-None-Match", resp.Header.Get("ETag"))
	notModifiedResp, err := app.Test(req)
	require.NoError(t, err)

	defer notModifiedResp.Body.Close()

	body, err := io.ReadAll(notModifiedResp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusNotModified, notModifiedResp.StatusCode)
	assert.Empty(t, body)
}
