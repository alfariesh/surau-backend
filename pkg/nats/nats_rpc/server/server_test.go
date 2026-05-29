package server

import (
	"fmt"
	"testing"

	natsrpc "github.com/evrone/go-clean-template/pkg/nats/nats_rpc"
	"github.com/stretchr/testify/assert"
)

func TestStatusForError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "bad request",
			err:      fmt.Errorf("handler: %w", natsrpc.ErrBadRequest),
			expected: natsrpc.ErrBadRequest.Error(),
		},
		{
			name:     "unauthenticated",
			err:      fmt.Errorf("handler: %w", natsrpc.ErrUnauthenticated),
			expected: natsrpc.ErrUnauthenticated.Error(),
		},
		{
			name:     "failed precondition",
			err:      fmt.Errorf("handler: %w", natsrpc.ErrFailedPrecondition),
			expected: natsrpc.ErrFailedPrecondition.Error(),
		},
		{
			name:     "unavailable",
			err:      fmt.Errorf("handler: %w", natsrpc.ErrUnavailable),
			expected: natsrpc.ErrUnavailable.Error(),
		},
		{
			name:     "rate limited",
			err:      fmt.Errorf("handler: %w", natsrpc.ErrRateLimited),
			expected: natsrpc.ErrRateLimited.Error(),
		},
		{
			name:     "internal",
			err:      assert.AnError,
			expected: natsrpc.ErrInternalServer.Error(),
		},
	}

	for _, tc := range tests {
		localTc := tc
		t.Run(localTc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, localTc.expected, statusForError(localTc.err))
		})
	}
}
