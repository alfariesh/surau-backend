package observability_test

import (
	"context"
	"testing"

	"github.com/alfariesh/surau-backend/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Enabled init must construct the provider without error — the resource
// schema conflict that crash-looped dev would have failed right here.
func TestInitTracingEnabled(t *testing.T) {
	t.Parallel()

	shutdown, err := observability.InitTracing(context.Background(), &observability.TracingConfig{
		Enabled:     true,
		Endpoint:    "http://127.0.0.1:4318", // never dialed during init
		SampleRatio: 1.0,
		ServiceName: "surau-backend-test",
		Environment: "test",
		Version:     "test",
	})
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	// Shutdown may fail on flush (nothing listening) — only assert it returns.
	if err := shutdown(ctx); err != nil {
		t.Logf("shutdown flush error (expected, no collector): %v", err)
	}
}

func TestInitTracingDisabledIsNoop(t *testing.T) {
	t.Parallel()

	shutdown, err := observability.InitTracing(context.Background(), &observability.TracingConfig{Enabled: false})
	require.NoError(t, err)
	assert.NoError(t, shutdown(context.Background()))
}
