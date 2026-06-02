package natsrpc

import "errors"

var (
	// ErrTimeout -.
	ErrTimeout = errors.New("timeout")
	// ErrInternalServer -.
	ErrInternalServer = errors.New("internal server error")
	// ErrBadRequest -.
	ErrBadRequest = errors.New("bad request")
	// ErrUnauthenticated -.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrFailedPrecondition -.
	ErrFailedPrecondition = errors.New("failed precondition")
	// ErrUnavailable -.
	ErrUnavailable = errors.New("unavailable")
	// ErrRateLimited -.
	ErrRateLimited = errors.New("rate limited")
	// ErrBadHandler -.
	ErrBadHandler = errors.New("unregistered handler")
)

// Success -.
const Success = "success"
