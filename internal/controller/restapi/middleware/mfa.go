package middleware

import (
	"net/http"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/gofiber/fiber/v2"
)

// RequireFreshMFA gates high-class destructive routes (A-3): enrolled users
// must have proven a second factor within the step-up window; MFA-mandated
// roles that never enrolled are locked out once their grace period ends.
// Attach AFTER Auth (and any RequireRoles) so user/sessionID locals exist.
func RequireFreshMFA(u usecase.User) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		user, ok, err := authenticatedUser(ctx, u)
		if err != nil || !ok {
			return middlewareError(ctx, http.StatusUnauthorized, "unauthorized")
		}

		familyID, _ := ctx.Locals("sessionID").(string)

		decision, err := u.MFAGate(ctx.UserContext(), &user, familyID)
		if err != nil {
			return middlewareError(ctx, http.StatusInternalServerError, "internal server error")
		}

		switch decision {
		case entity.MFAGateEnrollmentRequired:
			return middlewareError(ctx, http.StatusForbidden, "mfa enrollment required")
		case entity.MFAGateStepUpRequired:
			return middlewareError(ctx, http.StatusForbidden, "mfa step-up required")
		case entity.MFAGateAllowed:
		}

		return ctx.Next()
	}
}
