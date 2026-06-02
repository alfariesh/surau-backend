package v1

import (
	"errors"
	"net/http"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     Set user role
// @Description Admin-only user role assignment for user, editor, or admin.
// @ID          admin-set-user-role
// @Tags        admin
// @Accept      json
// @Produce     json
// @Param       request body request.SetUserRole true "User role update"
// @Success     200 {object} entity.User
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /admin/users/role [patch]
func (r *V1) adminSetUserRole(ctx *fiber.Ctx) error {
	var body request.SetUserRole
	if err := ctx.BodyParser(&body); err != nil {
		return adminErrorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return adminErrorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	user, err := r.u.SetRoleByEmail(restAuthContext(ctx), body.Email, body.Role)
	if err != nil {
		r.l.Error(err, "restapi - v1 - adminSetUserRole")

		switch {
		case errors.Is(err, entity.ErrInvalidRole):
			return adminErrorResponse(ctx, http.StatusBadRequest, "invalid role")
		case errors.Is(err, entity.ErrUserNotFound):
			return adminErrorResponse(ctx, http.StatusNotFound, "user not found")
		default:
			return adminErrorResponse(ctx, http.StatusInternalServerError, "internal server error")
		}
	}

	return ctx.Status(http.StatusOK).JSON(user)
}

func adminErrorResponse(ctx *fiber.Ctx, code int, msg string) error {
	return ctx.Status(code).JSON(response.Error{Error: msg})
}
