package middleware

import (
	"net/http"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/gofiber/fiber/v2"
)

// Admin requires an authenticated admin user.
func Admin(u usecase.User) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		userID, ok := ctx.Locals("userID").(string)
		if !ok || userID == "" {
			return ctx.Status(http.StatusUnauthorized).JSON(errorResponse{Error: "unauthorized"})
		}

		user, err := u.GetUser(ctx.UserContext(), userID)
		if err != nil {
			return ctx.Status(http.StatusUnauthorized).JSON(errorResponse{Error: "unauthorized"})
		}

		if user.Role != entity.UserRoleAdmin {
			return ctx.Status(http.StatusForbidden).JSON(errorResponse{Error: "forbidden"})
		}

		return ctx.Next()
	}
}
