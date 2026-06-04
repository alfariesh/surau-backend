package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const publicCacheControl = "public, max-age=300, stale-while-revalidate=86400"

// PublicCache sets cache validators for stable public GET JSON endpoints.
func PublicCache() fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		if ctx.Method() != http.MethodGet {
			return ctx.Next()
		}

		if err := ctx.Next(); err != nil {
			return err
		}

		if ctx.Response().StatusCode() != http.StatusOK {
			return nil
		}

		body := ctx.Response().Body()
		if len(body) == 0 {
			return nil
		}

		hash := sha256.Sum256(body)
		etag := `W/"` + hex.EncodeToString(hash[:]) + `"`

		ctx.Set("Cache-Control", publicCacheControl)
		ctx.Set("ETag", etag)

		if lastModified, ok := latestUpdatedAt(body); ok {
			ctx.Set("Last-Modified", lastModified.UTC().Format(http.TimeFormat))
		}

		if strings.TrimSpace(ctx.Get("If-None-Match")) == etag {
			ctx.Status(http.StatusNotModified)
			ctx.Response().SetBody(nil)
		}

		return nil
	}
}

func latestUpdatedAt(body []byte) (time.Time, bool) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return time.Time{}, false
	}

	var latest time.Time
	walkUpdatedAt(payload, &latest)

	if latest.IsZero() {
		return time.Time{}, false
	}

	return latest, true
}

func walkUpdatedAt(value any, latest *time.Time) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == "updated_at" {
				if parsed, ok := parseJSONTime(nested); ok && parsed.After(*latest) {
					*latest = parsed
				}

				continue
			}

			walkUpdatedAt(nested, latest)
		}
	case []any:
		for _, nested := range typed {
			walkUpdatedAt(nested, latest)
		}
	}
}

func parseJSONTime(value any) (time.Time, bool) {
	text, ok := value.(string)
	if !ok {
		return time.Time{}, false
	}

	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, false
	}

	return parsed, true
}
