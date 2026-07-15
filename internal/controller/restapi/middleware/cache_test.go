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

// TestPublicCacheHeaderIsAContract pins the exact Cache-Control string:
// the max-age/stale-while-revalidate numbers MUST stay equal to the edge
// worker TTLs in workers/api-cache/wrangler.jsonc (FRESH 300 / STALE 86400).
// Changing one side without the other splits the cache policy.
func TestPublicCacheHeaderIsAContract(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.PublicCache())
	app.Get("/c", func(ctx *fiber.Ctx) error { return ctx.JSON(fiber.Map{"x": 1}) })

	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/c", nil))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, "public, max-age=300, stale-while-revalidate=86400", resp.Header.Get("Cache-Control"))
}

// TestPublicRevalidatePreventsStaleReuse pins the license-sensitive public
// cache policy. These responses may be retained for conditional requests, but
// a client must contact the origin before using them again.
func TestPublicRevalidatePreventsStaleReuse(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.PublicRevalidate())
	app.Get("/books/797", func(ctx *fiber.Ctx) error {
		return ctx.JSON(fiber.Map{
			"id":         797,
			"updated_at": "2026-07-11T08:09:10Z",
		})
	})

	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/books/797", nil))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "public, max-age=0, must-revalidate", resp.Header.Get("Cache-Control"))
	assert.NotEmpty(t, resp.Header.Get("ETag"))
	assert.Equal(t, "Sat, 11 Jul 2026 08:09:10 GMT", resp.Header.Get("Last-Modified"))

	revalidateReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/books/797", nil)
	revalidateReq.Header.Set("If-None-Match", resp.Header.Get("ETag"))
	notModifiedResp, err := app.Test(revalidateReq)
	require.NoError(t, err)

	defer notModifiedResp.Body.Close()

	assert.Equal(t, http.StatusNotModified, notModifiedResp.StatusCode)
	assert.Equal(t, "public, max-age=0, must-revalidate", notModifiedResp.Header.Get("Cache-Control"))
}

func TestPublicRevalidateUsesLatestSitemapLastmod(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.PublicRevalidate())
	app.Get("/quran/sitemap", func(ctx *fiber.Ctx) error {
		return ctx.JSON(fiber.Map{"items": []fiber.Map{
			{"lastmod": "2026-07-15T08:00:00Z"},
			{"lastmod": "2026-07-15T09:10:11.123456Z"},
		}})
	})

	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/quran/sitemap", nil))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Wed, 15 Jul 2026 09:10:11 GMT", resp.Header.Get("Last-Modified"))
}

// TestPublicCacheExcludePath: dynamic endpoints inside a cached group (e.g.
// /v1/quran/search) answer no-store while sibling routes stay cached (F1-D).
func TestPublicCacheExcludePath(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.PublicCache(middleware.ExcludePath("/quran/search")))
	app.Get("/quran/search", func(ctx *fiber.Ctx) error { return ctx.JSON(fiber.Map{"items": []string{}}) })
	app.Get("/quran/juz", func(ctx *fiber.Ctx) error { return ctx.JSON(fiber.Map{"items": []string{}}) })

	searchResp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/quran/search?q=x", nil))
	require.NoError(t, err)

	defer searchResp.Body.Close()

	assert.Equal(t, http.StatusOK, searchResp.StatusCode)
	assert.Equal(t, "no-store", searchResp.Header.Get("Cache-Control"))
	assert.Empty(t, searchResp.Header.Get("ETag"))

	juzResp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/quran/juz", nil))
	require.NoError(t, err)

	defer juzResp.Body.Close()

	assert.Equal(t, "public, max-age=300, stale-while-revalidate=86400", juzResp.Header.Get("Cache-Control"))
}
