package server

import (
	"fmt"
	"testing"

	rmqrpc "github.com/evrone/go-clean-template/pkg/rabbitmq/rmq_rpc"
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
			err:      fmt.Errorf("handler: %w", rmqrpc.ErrBadRequest),
			expected: rmqrpc.ErrBadRequest.Error(),
		},
		{
			name:     "unauthenticated",
			err:      fmt.Errorf("handler: %w", rmqrpc.ErrUnauthenticated),
			expected: rmqrpc.ErrUnauthenticated.Error(),
		},
		{
			name:     "failed precondition",
			err:      fmt.Errorf("handler: %w", rmqrpc.ErrFailedPrecondition),
			expected: rmqrpc.ErrFailedPrecondition.Error(),
		},
		{
			name:     "unavailable",
			err:      fmt.Errorf("handler: %w", rmqrpc.ErrUnavailable),
			expected: rmqrpc.ErrUnavailable.Error(),
		},
		{
			name:     "rate limited",
			err:      fmt.Errorf("handler: %w", rmqrpc.ErrRateLimited),
			expected: rmqrpc.ErrRateLimited.Error(),
		},
		{
			name:     "internal",
			err:      assert.AnError,
			expected: rmqrpc.ErrInternalServer.Error(),
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
