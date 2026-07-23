package v1

import (
	"errors"
	"net/http"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/apierror"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase/pushidentity"
	"github.com/gofiber/fiber/v2"
)

// issuePushIdentityToken godoc.
// @Summary      Issue a short-lived OneSignal identity token
// @Description  Identity is derived exclusively from the authenticated account and active session.
// @Tags         push
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} entity.PushIdentityToken
// @Failure      401 {object} response.Error
// @Failure      503 {object} response.Error
// @Router       /me/push/identity-token [post]
func (r *V1) issuePushIdentityToken(ctx *fiber.Ctx) error {
	userID, userOK := ctx.Locals("userID").(string)

	familyID, familyOK := ctx.Locals("sessionID").(string)
	if !userOK || !familyOK || r.pushIdentity == nil {
		return pushIdentityError(ctx, http.StatusServiceUnavailable, "push identity unavailable")
	}

	result, err := r.pushIdentity.Issue(ctx.UserContext(), userID, familyID)
	if err != nil {
		if errors.Is(err, pushidentity.ErrInactiveSession) {
			return pushIdentityError(ctx, http.StatusUnauthorized, "invalid or expired session")
		}

		r.l.Error("push identity issue: %v", err)

		return pushIdentityError(ctx, http.StatusServiceUnavailable, "push identity unavailable")
	}

	return ctx.Status(http.StatusOK).JSON(result)
}

// resolvePushRoute godoc.
// @Summary      Resolve a push route against the active account
// @Tags         push
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request body entity.PushRouteInput true "String-only notification data"
// @Success      200 {object} entity.PushRouteResolution
// @Failure      400 {object} response.Error
// @Router       /me/push/resolve [post]
func (r *V1) resolvePushRoute(ctx *fiber.Ctx) error {
	var input entity.PushRouteInput
	if err := ctx.BodyParser(&input); err != nil {
		return pushIdentityError(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(input); err != nil {
		return pushIdentityError(ctx, http.StatusBadRequest, "invalid push data")
	}

	userID, userOK := ctx.Locals("userID").(string)

	familyID, familyOK := ctx.Locals("sessionID").(string)
	if r.pushIdentity == nil {
		return ctx.Status(http.StatusOK).JSON(entity.PushRouteResolution{Destination: "home"})
	}

	if !userOK || !familyOK {
		return ctx.Status(http.StatusOK).JSON(entity.PushRouteResolution{Destination: "home"})
	}

	return ctx.Status(http.StatusOK).JSON(r.pushIdentity.Resolve(ctx.UserContext(), userID, familyID, input))
}

func pushIdentityError(ctx *fiber.Ctx, status int, message string) error {
	requestID, ok := ctx.Locals("requestID").(string)
	if !ok {
		requestID = ""
	}

	return ctx.Status(status).JSON(response.Error{
		Error: message, Code: apierror.Code(message), Message: message, RequestID: requestID,
	})
}
