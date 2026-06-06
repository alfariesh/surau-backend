package v1

import (
	"github.com/evrone/go-clean-template/internal/controller/restapi/apierror"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
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

func requestID(ctx *fiber.Ctx) string {
	requestID, ok := ctx.Locals("requestID").(string)
	if !ok {
		return ""
	}

	return requestID
}
