package v1

import (
	"strings"
	"unicode"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/gofiber/fiber/v2"
)

func errorResponse(ctx *fiber.Ctx, code int, msg string) error {
	return ctx.Status(code).JSON(response.Error{
		Error:     msg,
		Code:      errorCode(msg),
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

func errorCode(msg string) string {
	msg = strings.ToLower(strings.TrimSpace(msg))
	if msg == "" {
		return "error"
	}

	var out strings.Builder

	lastUnderscore := false

	for _, r := range msg {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)

			lastUnderscore = false

			continue
		}

		if !lastUnderscore {
			out.WriteByte('_')

			lastUnderscore = true
		}
	}

	code := strings.Trim(out.String(), "_")
	if code == "" {
		return "error"
	}

	return code
}
