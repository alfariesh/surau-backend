package v1

import (
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/apierror"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

func errorResponse(ctx *fiber.Ctx, code int, msg string) error {
	return ctx.Status(code).JSON(response.Error{
		Error:     msg,
		Code:      apierror.Code(msg),
		Message:   msg,
		RequestID: requestID(ctx),
	})
}

// rateLimitedResponse renders a 429 and, when the usecase reported a
// retry-after hint, surfaces it via the Retry-After header and body field.
func rateLimitedResponse(ctx *fiber.Ctx, err error) error {
	const msg = "too many auth attempts"

	var rateLimited *entity.AuthRateLimitedError
	if errors.As(err, &rateLimited) && rateLimited.RetryAfter > 0 {
		seconds := int64(math.Ceil(rateLimited.RetryAfter.Seconds()))
		ctx.Set(fiber.HeaderRetryAfter, strconv.FormatInt(seconds, 10))

		return ctx.Status(http.StatusTooManyRequests).JSON(response.Error{
			Error:      msg,
			Code:       apierror.Code(msg),
			Message:    msg,
			RetryAfter: seconds,
			RequestID:  requestID(ctx),
		})
	}

	return errorResponse(ctx, http.StatusTooManyRequests, msg)
}

func requestID(ctx *fiber.Ctx) string {
	requestID, ok := ctx.Locals("requestID").(string)
	if !ok {
		return ""
	}

	return requestID
}
