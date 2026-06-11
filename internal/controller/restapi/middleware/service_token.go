package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gofiber/fiber/v2"
)

// ServiceToken guards internal service-to-service endpoints with a static
// shared secret carried in X-Internal-Token. Comparison is constant-time. An
// empty configured token disables the whole group with 404 so the surface is
// invisible unless explicitly enabled.
func ServiceToken(token string) func(*fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		if token == "" {
			return ctx.SendStatus(http.StatusNotFound)
		}

		provided := ctx.Get("X-Internal-Token")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			return middlewareError(ctx, http.StatusUnauthorized, "invalid service token")
		}

		return ctx.Next()
	}
}
