package middleware

import (
	"time"

	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// requestLoggerKey is the Locals slot holding the request-scoped child logger
// (request_id + trace_id stamped). Handlers fetch it via RequestLogger().
const requestLoggerKey = "requestLogger"

// RequestLogger returns the request-scoped logger stored by TraceContext, or
// the fallback when the middleware did not run (tests, internal routes).
func RequestLogger(ctx *fiber.Ctx, fallback logger.Interface) logger.Interface {
	if l, ok := ctx.Locals(requestLoggerKey).(logger.Interface); ok && l != nil {
		return l
	}

	return fallback
}

// TraceContext builds the request-scoped logger (request_id, trace_id) and
// annotates the active span with the request id so a log line and a trace can
// be joined from either side (F1-B AC). Must run after RequestID and after
// the otelfiber middleware.
func TraceContext(l logger.Interface) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		reqLog := l

		requestID, hasRequestID := ctx.Locals("requestID").(string)
		if hasRequestID && requestID != "" {
			reqLog = reqLog.WithField("request_id", requestID)
		}

		if sc := trace.SpanFromContext(ctx.UserContext()).SpanContext(); sc.HasTraceID() {
			traceID := sc.TraceID().String()
			reqLog = reqLog.WithField("trace_id", traceID)
			ctx.Set("X-Trace-ID", traceID)

			if requestID != "" {
				trace.SpanFromContext(ctx.UserContext()).
					SetAttributes(attribute.String("surau.request_id", requestID))
			}
		}

		ctx.Locals(requestLoggerKey, reqLog)

		return ctx.Next()
	}
}

// Logger emits one structured access-log line per request, carrying the
// correlation fields (request_id, trace_id) plus method/path/status/latency.
func Logger(l logger.Interface) func(c *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		start := time.Now()
		err := ctx.Next()

		reqLog, hasScoped := ctx.Locals(requestLoggerKey).(logger.Interface)
		if !hasScoped || reqLog == nil {
			// Short-circuited before TraceContext (e.g. CORS preflight):
			// still stamp the request id so no access line loses correlation.
			reqLog = l
			if requestID, ok := ctx.Locals("requestID").(string); ok && requestID != "" {
				reqLog = reqLog.WithField("request_id", requestID)
			}
		}

		reqLog = reqLog.
			WithField("method", ctx.Method()).
			WithField("path", ctx.OriginalURL()).
			WithField("status", ctx.Response().StatusCode()).
			WithField("latency_ms", time.Since(start).Milliseconds()).
			WithField("ip", ctx.IP()).
			WithField("bytes", len(ctx.Response().Body()))

		reqLog.Info("request")

		return err
	}
}
