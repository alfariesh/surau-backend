package middleware

import (
	"net/http"
	"strings"

	"github.com/evrone/go-clean-template/internal/controller/authutil"
	"github.com/evrone/go-clean-template/internal/controller/restapi/apierror"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/gofiber/fiber/v2"
)

const _bearerParts = 2

type errorResponse struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// Auth returns a JWT authentication middleware for Fiber.
func Auth(jwtManager *jwt.Manager, users usecase.User) func(*fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		header := ctx.Get("Authorization")
		if header == "" {
			return middlewareError(ctx, http.StatusUnauthorized, "missing authorization header")
		}

		parts := strings.SplitN(header, " ", _bearerParts)
		if len(parts) != _bearerParts || !strings.EqualFold(parts[0], "Bearer") {
			return middlewareError(ctx, http.StatusUnauthorized, "invalid authorization header format")
		}

		token := strings.TrimSpace(parts[1])
		if token == "" {
			return middlewareError(ctx, http.StatusUnauthorized, "invalid authorization header format")
		}

		user, sessionID, err := authutil.AuthenticateUserSession(ctx.UserContext(), jwtManager, users, token)
		if err != nil {
			return middlewareError(ctx, http.StatusUnauthorized, "invalid or expired token")
		}

		ctx.Locals("user", user)
		ctx.Locals("userID", user.ID)
		ctx.Locals("sessionID", sessionID)

		return ctx.Next()
	}
}

func middlewareError(ctx *fiber.Ctx, status int, msg string) error {
	return ctx.Status(status).JSON(errorResponse{
		Error:     msg,
		Code:      apierror.Code(msg),
		Message:   msg,
		RequestID: middlewareRequestID(ctx),
	})
}

func middlewareRequestID(ctx *fiber.Ctx) string {
	requestID, ok := ctx.Locals("requestID").(string)
	if !ok {
		return ""
	}

	return requestID
}
