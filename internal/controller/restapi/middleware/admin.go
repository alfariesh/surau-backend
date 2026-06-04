package middleware

import (
	"net/http"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/gofiber/fiber/v2"
)

// RequireRoles requires an authenticated user with one of the provided roles.
func RequireRoles(u usecase.User, roles ...string) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		user, ok, err := authenticatedUser(ctx, u)
		if err != nil || !ok {
			return middlewareError(ctx, http.StatusUnauthorized, "unauthorized")
		}

		if !hasAllowedRole(user.Role, roles) {
			return middlewareError(ctx, http.StatusForbidden, "forbidden")
		}

		ctx.Locals("user", user)
		ctx.Locals("userID", user.ID)

		return ctx.Next()
	}
}

// Admin requires an authenticated admin user.
func Admin(u usecase.User) fiber.Handler {
	return RequireRoles(u, entity.UserRoleAdmin)
}

func authenticatedUser(ctx *fiber.Ctx, u usecase.User) (entity.User, bool, error) {
	if user, ok := ctx.Locals("user").(entity.User); ok && user.ID != "" {
		return user, true, nil
	}

	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return entity.User{}, false, nil
	}

	user, err := u.GetUser(ctx.UserContext(), userID)
	if err != nil {
		return entity.User{}, false, err
	}

	return user, true, nil
}

func hasAllowedRole(role string, roles []string) bool {
	role, err := entity.NormalizeUserRole(role)
	if err != nil {
		return false
	}

	for _, allowed := range roles {
		allowed, err = entity.NormalizeUserRole(allowed)
		if err != nil {
			continue
		}

		if role == allowed {
			return true
		}
	}

	return false
}
