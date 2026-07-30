package requestmeta_test

import (
	"context"
	"testing"

	"github.com/alfariesh/surau-backend/internal/requestmeta"
	"github.com/stretchr/testify/assert"
)

func TestRequestIDContextRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := requestmeta.WithRequestID(context.Background(), "  request-u0-1  ")

	assert.Equal(t, "request-u0-1", requestmeta.RequestID(ctx))
	assert.Empty(t, requestmeta.RequestID(context.Background()))
}
