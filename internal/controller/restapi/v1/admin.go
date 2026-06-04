package v1

import (
	"errors"
	"net/http"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     List admin users
// @Description Admin-only paginated user management list. Supports q, role, and email_verified filters.
// @ID          admin-list-users
// @Tags        admin
// @Produce     json
// @Param       q              query string false "Search username, email, or display name"
// @Param       role           query string false "Role filter" Enums(user,editor,admin)
// @Param       email_verified query bool   false "Email verification status"
// @Param       limit          query int    false "Page size" default(50)
// @Param       offset         query int    false "Page offset" default(0)
// @Success     200            {object} response.AdminUserList
// @Failure     400            {object} response.Error
// @Failure     401            {object} response.Error
// @Failure     403            {object} response.Error
// @Failure     500            {object} response.Error
// @Security    BearerAuth
// @Router      /admin/users [get]
func (r *V1) adminUsers(ctx *fiber.Ctx) error {
	emailVerified, err := optionalQueryBool(ctx, "email_verified")
	if err != nil {
		return adminErrorResponse(ctx, http.StatusBadRequest, "invalid email_verified")
	}

	users, total, err := r.u.AdminUsers(
		ctx.UserContext(),
		ctx.Query("q"),
		ctx.Query("role"),
		emailVerified,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - adminUsers")

		return adminUsecaseError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.AdminUserList{Users: users, Total: total})
}

// @Summary     Get admin user detail
// @Description Admin-only user account detail by user ID.
// @ID          admin-get-user
// @Tags        admin
// @Produce     json
// @Param       id  path     string true "User ID"
// @Success     200 {object} entity.UserAccount
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /admin/users/{id} [get]
func (r *V1) adminUserDetail(ctx *fiber.Ctx) error {
	account, err := r.u.GetUserAccount(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - adminUserDetail")

		return adminUsecaseError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(account)
}

// @Summary     Get admin user activity
// @Description Admin-only role-change audit history for one user.
// @ID          admin-get-user-activity
// @Tags        admin
// @Produce     json
// @Param       id     path  string true  "User ID"
// @Param       limit  query int    false "Page size" default(50)
// @Param       offset query int    false "Page offset" default(0)
// @Success     200    {object} response.AdminUserActivityList
// @Failure     401    {object} response.Error
// @Failure     403    {object} response.Error
// @Failure     404    {object} response.Error
// @Failure     500    {object} response.Error
// @Security    BearerAuth
// @Router      /admin/users/{id}/activity [get]
func (r *V1) adminUserActivity(ctx *fiber.Ctx) error {
	activity, total, err := r.u.AdminUserActivity(
		ctx.UserContext(),
		ctx.Params("id"),
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - adminUserActivity")

		return adminUsecaseError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.AdminUserActivityList{Activity: activity, Total: total})
}

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
	actor, ok := ctx.Locals("user").(entity.User)
	if !ok || actor.ID == "" {
		return adminErrorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.SetUserRole
	if err := ctx.BodyParser(&body); err != nil {
		return adminErrorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return adminErrorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	user, err := r.u.SetRoleByEmail(restAuthContext(ctx), actor.ID, actor.Email, body.Email, body.Role)
	if err != nil {
		r.l.Error(err, "restapi - v1 - adminSetUserRole")

		return adminUsecaseError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(user)
}

func adminUsecaseError(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrInvalidRole):
		return adminErrorResponse(ctx, http.StatusBadRequest, "invalid role")
	case errors.Is(err, entity.ErrUserNotFound):
		return adminErrorResponse(ctx, http.StatusNotFound, "user not found")
	default:
		return adminErrorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}

func adminErrorResponse(ctx *fiber.Ctx, code int, msg string) error {
	return errorResponse(ctx, code, msg)
}
