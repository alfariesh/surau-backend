package middleware_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pins the F1-B correlation contract: every request-scoped log line — the
// access line AND anything a handler logs through the request logger —
// carries the request id, plus latency/status on the access line.
func TestAccessLogCarriesRequestIDAndLatency(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	l := logger.NewWithWriter("info", &buf)

	app := fiber.New()
	app.Use(middleware.RequestID())
	app.Use(middleware.Logger(l))
	app.Use(middleware.TraceContext(l))
	app.Get("/ping", func(c *fiber.Ctx) error {
		middleware.RequestLogger(c, l).Warn("handler-scoped line")

		return c.SendStatus(fiber.StatusTeapot)
	})

	req := httptest.NewRequestWithContext(t.Context(), fiber.MethodGet, "/ping", http.NoBody)
	req.Header.Set("X-Request-ID", "req-observability-test")
	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 2, "expected handler line + access line, got: %s", buf.String())

	var handlerLine map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &handlerLine))
	assert.Equal(t, "req-observability-test", handlerLine["request_id"], "handler logs must carry the request id")
	assert.Equal(t, "handler-scoped line", handlerLine["message"])

	var accessLine map[string]any
	require.NoError(t, json.Unmarshal(lines[1], &accessLine))
	assert.Equal(t, "req-observability-test", accessLine["request_id"])
	assert.Equal(t, "GET", accessLine["method"])
	assert.Equal(t, "/ping", accessLine["path"])
	assert.InDelta(t, fiber.StatusTeapot, accessLine["status"], 0)
	assert.Contains(t, accessLine, "latency_ms")
}

// Even when a middleware short-circuits before TraceContext runs, the access
// line must not lose the request id.
func TestAccessLogRequestIDOnShortCircuit(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	l := logger.NewWithWriter("info", &buf)

	app := fiber.New()
	app.Use(middleware.RequestID())
	app.Use(middleware.Logger(l))
	app.Use(func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusForbidden) }) // short-circuit

	req := httptest.NewRequestWithContext(t.Context(), fiber.MethodGet, "/blocked", http.NoBody)
	req.Header.Set("X-Request-ID", "req-shortcircuit")
	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	var accessLine map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &accessLine))
	assert.Equal(t, "req-shortcircuit", accessLine["request_id"])
}

func TestWithFieldChildDoesNotMutateParent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	l := logger.NewWithWriter("info", &buf)
	child := l.WithField("request_id", "abc")

	child.Info("with-field")
	l.Info("plain")

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 2)
	assert.Contains(t, string(lines[0]), `"request_id":"abc"`)
	assert.NotContains(t, string(lines[1]), "request_id", "parent logger must stay field-free")
}
