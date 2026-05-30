package middleware

import (
	"net/http"
	"strings"

	"github.com/evrone/go-clean-template/internal/controller/authutil"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/gofiber/fiber/v2"
)

const _bearerParts = 2

type errorResponse struct {
	Error string `json:"error"`
}

// Auth returns a JWT authentication middleware for Fiber.
func Auth(jwtManager *jwt.Manager, users usecase.User) func(*fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		header := ctx.Get("Authorization")
		if header == "" {
			return ctx.Status(http.StatusUnauthorized).JSON(errorResponse{Error: "missing authorization header"})
		}

		parts := strings.SplitN(header, " ", _bearerParts)
		if len(parts) != _bearerParts || !strings.EqualFold(parts[0], "Bearer") {
			return ctx.Status(http.StatusUnauthorized).JSON(errorResponse{Error: "invalid authorization header format"})
		}

		token := strings.TrimSpace(parts[1])
		if token == "" {
			return ctx.Status(http.StatusUnauthorized).JSON(errorResponse{Error: "invalid authorization header format"})
		}

		user, err := authutil.AuthenticateUser(ctx.UserContext(), jwtManager, users, token)
		if err != nil {
			return ctx.Status(http.StatusUnauthorized).JSON(errorResponse{Error: "invalid or expired token"})
		}

		ctx.Locals("user", user)
		ctx.Locals("userID", user.ID)

		return ctx.Next()
	}
}
