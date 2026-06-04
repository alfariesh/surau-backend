package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const requestIDHeader = "X-Request-ID"

// RequestID attaches a request id to the response and Fiber context.
func RequestID() fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		requestID := strings.TrimSpace(ctx.Get(requestIDHeader))
		if requestID == "" {
			requestID = uuid.NewString()
		}

		ctx.Locals("requestID", requestID)
		ctx.Set(requestIDHeader, requestID)

		return ctx.Next()
	}
}
