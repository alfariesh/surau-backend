package v1

import (
	"fmt"

	natsrpc "github.com/evrone/go-clean-template/pkg/nats/nats_rpc"
)

func badRequestError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", natsrpc.ErrBadRequest, message)
	}

	return fmt.Errorf("%w: %s: %v", natsrpc.ErrBadRequest, message, cause)
}

func unauthenticatedError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", natsrpc.ErrUnauthenticated, message)
	}

	return fmt.Errorf("%w: %s: %v", natsrpc.ErrUnauthenticated, message, cause)
}

func failedPreconditionError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", natsrpc.ErrFailedPrecondition, message)
	}

	return fmt.Errorf("%w: %s: %v", natsrpc.ErrFailedPrecondition, message, cause)
}

func unavailableError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", natsrpc.ErrUnavailable, message)
	}

	return fmt.Errorf("%w: %s: %v", natsrpc.ErrUnavailable, message, cause)
}

func rateLimitedError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", natsrpc.ErrRateLimited, message)
	}

	return fmt.Errorf("%w: %s: %v", natsrpc.ErrRateLimited, message, cause)
}
