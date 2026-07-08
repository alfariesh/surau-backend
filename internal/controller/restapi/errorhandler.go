package restapi

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/apierror"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/pkg/logger"
)

// EnvelopeErrorHandler returns the Fiber error handler that turns framework
// and unhandled errors into the standard API error envelope (F1-D):
// normalized FROZEN messages only — the original error is logged, never
// echoed to the client (an unhandled panic's value must not leak into the
// 500 body). request_id can be empty for failures that happen before the
// middleware chain runs (e.g. body-limit 413).
func EnvelopeErrorHandler(l logger.Interface) fiber.ErrorHandler {
	return func(ctx *fiber.Ctx, err error) error {
		status := http.StatusInternalServerError

		var fiberErr *fiber.Error
		if errors.As(err, &fiberErr) {
			status = fiberErr.Code
		}

		msg := normalizedErrorMessage(status)

		if status >= http.StatusInternalServerError {
			l.Error(err, "restapi - unhandled error")
		}

		requestID, _ := ctx.Locals("requestID").(string) //nolint:errcheck // absent locals just mean empty request_id

		return ctx.Status(status).JSON(response.Error{
			Error:     msg,
			Code:      apierror.Code(msg),
			Message:   msg,
			RequestID: requestID,
		})
	}
}

// normalizedErrorMessage maps a status to its FROZEN envelope message —
// never derived from err.Error(), so per-path/framework texts ("Cannot GET
// /x") cannot mint new machine codes.
func normalizedErrorMessage(status int) string {
	switch {
	case status == http.StatusNotFound:
		return "not found"
	case status == http.StatusMethodNotAllowed:
		return "method not allowed"
	case status == http.StatusRequestEntityTooLarge:
		return "request entity too large"
	case status == http.StatusTooManyRequests:
		return "too many requests"
	case status >= http.StatusInternalServerError:
		return "internal server error"
	default:
		return "invalid request"
	}
}
