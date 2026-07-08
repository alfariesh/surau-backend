package middleware

import (
	"net/http"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/policy"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/gofiber/fiber/v2"
)

// RequireCapability gates a route on a policy capability (A-1). It is the only
// authorization entry point — role→capability resolution lives entirely in
// internal/policy, so no handler/usecase compares role strings. The checked
// capability is stashed in locals so restAuthContext can thread it into audit
// rows ("which capability authorized this action").
func RequireCapability(u usecase.User, capability policy.Capability) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		user, ok, err := authenticatedUser(ctx, u)
		if err != nil || !ok {
			return middlewareError(ctx, http.StatusUnauthorized, "unauthorized")
		}

		if !policy.Can(user.Role, capability) {
			return middlewareError(ctx, http.StatusForbidden, "forbidden")
		}

		ctx.Locals("user", user)
		ctx.Locals("userID", user.ID)
		ctx.Locals("capability", string(capability))

		return ctx.Next()
	}
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
