package v1

import (
	"fmt"

	rmqrpc "github.com/evrone/go-clean-template/pkg/rabbitmq/rmq_rpc"
)

func badRequestError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", rmqrpc.ErrBadRequest, message)
	}

	return fmt.Errorf("%w: %s: %v", rmqrpc.ErrBadRequest, message, cause)
}

func unauthenticatedError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", rmqrpc.ErrUnauthenticated, message)
	}

	return fmt.Errorf("%w: %s: %v", rmqrpc.ErrUnauthenticated, message, cause)
}

func failedPreconditionError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", rmqrpc.ErrFailedPrecondition, message)
	}

	return fmt.Errorf("%w: %s: %v", rmqrpc.ErrFailedPrecondition, message, cause)
}

func unavailableError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", rmqrpc.ErrUnavailable, message)
	}

	return fmt.Errorf("%w: %s: %v", rmqrpc.ErrUnavailable, message, cause)
}

func rateLimitedError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", rmqrpc.ErrRateLimited, message)
	}

	return fmt.Errorf("%w: %s: %v", rmqrpc.ErrRateLimited, message, cause)
}
