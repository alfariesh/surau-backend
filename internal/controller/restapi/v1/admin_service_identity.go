package v1

import (
	"errors"
	"net/http"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

const serviceIdentityDefaultLimit = 50

// @Summary List service identities
// @Description List named machine principals and safe token metadata. Requires manage-service-tokens.
// @ID admin-list-service-identities
// @Tags admin,service-identities
// @Produce json
// @Param limit query int false "Limit (max 100)" default(50)
// @Param offset query int false "Offset (max 10000)" default(0)
// @Success 200 {object} response.ServiceIdentityList
// @Failure 401 {object} response.Error
// @Failure 403 {object} response.Error
// @Security BearerAuth
// @Router /admin/service-identities [get]
func (r *V1) adminServiceIdentities(ctx *fiber.Ctx) error {
	items, total, err := r.serviceIdentity.ListServicePrincipals(
		ctx.UserContext(),
		queryInt(ctx, "limit", serviceIdentityDefaultLimit),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.ServiceIdentityList{Items: items, Total: total})
}

// @Summary Create service identity
// @Description Register an immutable principal name and scopes. Requires fresh MFA.
// @ID admin-create-service-identity
// @Tags admin,service-identities
// @Accept json
// @Produce json
// @Param request body request.CreateServiceIdentity true "Principal"
// @Success 201 {object} entity.ServicePrincipal
// @Failure 400 {object} response.Error
// @Failure 401 {object} response.Error
// @Failure 403 {object} response.Error
// @Security BearerAuth
// @Router /admin/service-identities [post]
func (r *V1) adminCreateServiceIdentity(ctx *fiber.Ctx) error {
	actorID, ok := serviceIdentityActor(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.CreateServiceIdentity
	if err := ctx.BodyParser(&body); err != nil || r.v.Struct(body) != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	principal, err := r.serviceIdentity.CreateServicePrincipal(
		ctx.UserContext(), actorID, body.PrincipalName, body.Description, body.Scopes,
	)
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusCreated, principal, principal.UpdatedAt)
}

// @Summary Get service identity
// @ID admin-get-service-identity
// @Tags admin,service-identities
// @Produce json
// @Param id path string true "Principal UUID"
// @Success 200 {object} entity.ServicePrincipal
// @Failure 404 {object} response.Error
// @Security BearerAuth
// @Router /admin/service-identities/{id} [get]
func (r *V1) adminServiceIdentity(ctx *fiber.Ctx) error {
	principal, err := r.serviceIdentity.GetServicePrincipal(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, principal, principal.UpdatedAt)
}

// @Summary Update service identity scopes
// @Description Replace description/scopes. Principal name is immutable. Requires fresh MFA and If-Match.
// @ID admin-update-service-identity
// @Tags admin,service-identities
// @Accept json
// @Produce json
// @Param id path string true "Principal UUID"
// @Param If-Match header string true "Current ETag or *"
// @Param request body request.UpdateServiceIdentity true "Mutable fields"
// @Success 200 {object} entity.ServicePrincipal
// @Failure 412 {object} response.Error
// @Failure 428 {object} response.Error
// @Security BearerAuth
// @Router /admin/service-identities/{id} [patch]
func (r *V1) adminUpdateServiceIdentity(ctx *fiber.Ctx) error {
	actorID, ok := serviceIdentityActor(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.UpdateServiceIdentity
	if err := ctx.BodyParser(&body); err != nil || r.v.Struct(body) != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	expected, force, err := serviceIdentityIfMatch(ctx)
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	principal, err := r.serviceIdentity.UpdateServicePrincipal(
		ctx.UserContext(), actorID, ctx.Params("id"), body.Description, body.Scopes, expected, force,
	)
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, principal, principal.UpdatedAt)
}

// @Summary Issue service token
// @Description Return a new raw token exactly once. Default 30 days; maximum 90 days. Requires fresh MFA and If-Match.
// @ID admin-issue-service-token
// @Tags admin,service-identities
// @Accept json
// @Produce json
// @Param id path string true "Principal UUID"
// @Param If-Match header string true "Current ETag or *"
// @Param request body request.IssueServiceToken false "Optional expiry"
// @Success 201 {object} entity.ServiceTokenIssueResult
// @Failure 412 {object} response.Error
// @Failure 428 {object} response.Error
// @Security BearerAuth
// @Router /admin/service-identities/{id}/tokens [post]
func (r *V1) adminIssueServiceToken(ctx *fiber.Ctx) error {
	actorID, ok := serviceIdentityActor(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.IssueServiceToken
	if len(ctx.Body()) > 0 {
		if err := ctx.BodyParser(&body); err != nil {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
	}

	expected, force, err := serviceIdentityIfMatch(ctx)
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	result, err := r.serviceIdentity.IssueServiceToken(
		ctx.UserContext(), actorID, ctx.Params("id"), body.ExpiresAt, expected, force,
	)
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")
	setUpdatedAtETag(ctx, result.Principal.UpdatedAt)

	return ctx.Status(http.StatusCreated).JSON(result)
}

// @Summary Revoke one service token
// @Description Revoke one credential without affecting overlapping siblings. Requires fresh MFA and If-Match.
// @ID admin-revoke-service-token
// @Tags admin,service-identities
// @Param id path string true "Principal UUID"
// @Param token_id path string true "Token UUID"
// @Param If-Match header string true "Current ETag or *"
// @Success 200 {object} entity.ServicePrincipal
// @Failure 412 {object} response.Error
// @Failure 428 {object} response.Error
// @Security BearerAuth
// @Router /admin/service-identities/{id}/tokens/{token_id}/revoke [post]
func (r *V1) adminRevokeServiceToken(ctx *fiber.Ctx) error {
	actorID, ok := serviceIdentityActor(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	expected, force, err := serviceIdentityIfMatch(ctx)
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	principal, err := r.serviceIdentity.RevokeServiceToken(
		ctx.UserContext(), actorID, ctx.Params("id"), ctx.Params("token_id"), expected, force,
	)
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, principal, principal.UpdatedAt)
}

// @Summary Revoke service identity
// @Description Permanently revoke the principal and all current/future credentials. Requires fresh MFA and If-Match.
// @ID admin-revoke-service-identity
// @Tags admin,service-identities
// @Param id path string true "Principal UUID"
// @Param If-Match header string true "Current ETag or *"
// @Success 200 {object} entity.ServicePrincipal
// @Failure 412 {object} response.Error
// @Failure 428 {object} response.Error
// @Security BearerAuth
// @Router /admin/service-identities/{id}/revoke [post]
func (r *V1) adminRevokeServiceIdentity(ctx *fiber.Ctx) error {
	actorID, ok := serviceIdentityActor(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	expected, force, err := serviceIdentityIfMatch(ctx)
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	principal, err := r.serviceIdentity.RevokeServicePrincipal(
		ctx.UserContext(), actorID, ctx.Params("id"), expected, force,
	)
	if err != nil {
		return r.serviceIdentityError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, principal, principal.UpdatedAt)
}

func serviceIdentityActor(ctx *fiber.Ctx) (string, bool) {
	actorID, ok := ctx.Locals("userID").(string)

	return actorID, ok && actorID != ""
}

func serviceIdentityIfMatch(ctx *fiber.Ctx) (*time.Time, bool, error) {
	expected, present, ok := parseIfMatch(ctx)
	if !ok {
		return nil, false, entity.ErrPreconditionFailed
	}

	if !present {
		return nil, false, entity.ErrPreconditionRequired
	}

	return expected, expected == nil, nil
}

func (r *V1) serviceIdentityError(ctx *fiber.Ctx, err error) error {
	r.l.Error(err, "restapi - v1 - service identity")

	switch {
	case errors.Is(err, entity.ErrServicePrincipalNotFound):
		return errorResponse(ctx, http.StatusNotFound, "service identity not found")
	case errors.Is(err, entity.ErrServiceTokenNotFound):
		return errorResponse(ctx, http.StatusNotFound, "service token not found")
	case errors.Is(err, entity.ErrInvalidServicePrincipal):
		return errorResponse(ctx, http.StatusBadRequest, "invalid service identity")
	case errors.Is(err, entity.ErrInvalidServiceScope):
		return errorResponse(ctx, http.StatusBadRequest, "invalid service scope")
	case errors.Is(err, entity.ErrServicePrincipalRevoked):
		return errorResponse(ctx, http.StatusConflict, "service identity revoked")
	case errors.Is(err, entity.ErrPreconditionRequired):
		return errorResponse(ctx, http.StatusPreconditionRequired, "if-match header required")
	case errors.Is(err, entity.ErrPreconditionFailed):
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}
