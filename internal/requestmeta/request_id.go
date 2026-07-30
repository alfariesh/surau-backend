package requestmeta

import (
	"context"
	"strings"
)

type requestIDKey struct{}

// WithRequestID carries the public HTTP request ID through usecase and trace
// boundaries without coupling domain code to Fiber.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ctx
	}

	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestID returns the request ID attached at the HTTP boundary.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	requestID, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(requestID)
}
