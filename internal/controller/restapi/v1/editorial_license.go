package v1

import (
	"net/http"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	_ "github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response" // for swaggo
	_ "github.com/alfariesh/surau-backend/internal/entity"                         // for swaggo
	"github.com/gofiber/fiber/v2"
)

const editorialLicenseAuditDefaultLimit = 50

// @Summary     Report kitab license coverage
// @Description Return a prioritized audit queue plus complete status counts. Defaults to unresolved; requires CapReviewEditorial.
// @ID          editorial-license-audit
// @Tags        editorial,licenses
// @Produce     json
// @Param       status query string false "Queue status" Enums(unresolved,unknown,needs_review,permitted,restricted,public_domain,all) default(unresolved)
// @Param       limit  query int    false "Limit (max 200)" default(50)
// @Param       offset query int    false "Offset (max 10000)" default(0)
// @Success     200 {object} entity.BookLicenseAuditReport
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/license-audit [get]
func (r *V1) editorialLicenseAudit(ctx *fiber.Ctx) error {
	if r.licenseAudit == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	report, err := r.licenseAudit.LicenseAuditReport(
		ctx.UserContext(),
		ctx.Query("status"),
		queryInt(ctx, "limit", editorialLicenseAuditDefaultLimit),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialLicenseAudit")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(report)
}

// @Summary     Get one kitab license decision
// @Description Return the Edition-level decision and effective grandfather state. Requires CapReviewEditorial.
// @ID          editorial-get-book-license
// @Tags        editorial,licenses
// @Produce     json
// @Param       book_id path int true "Book ID"
// @Success     200 {object} entity.BookLicense
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/books/{book_id}/license [get]
func (r *V1) editorialGetBookLicense(ctx *fiber.Ctx) error {
	if r.licenseAudit == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	license, err := r.licenseAudit.BookLicense(ctx.UserContext(), bookID)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialGetBookLicense")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, license, license.UpdatedAt)
}

// @Summary     Record a kitab license decision
// @Description Atomically update the Edition-level license and audit history. Requires CapPublishProduction, fresh MFA, and If-Match; use * only for an explicit unconditional decision.
// @ID          editorial-update-book-license
// @Tags        editorial,licenses
// @Accept      json
// @Produce     json
// @Param       book_id  path   int true "Book ID"
// @Param       If-Match header string true "Current ETag or *"
// @Param       request  body   request.UpdateBookLicense true "License decision"
// @Success     200 {object} entity.BookLicense
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     412 {object} response.Error
// @Failure     428 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/books/{book_id}/license [patch]
func (r *V1) editorialUpdateBookLicense(ctx *fiber.Ctx) error {
	if r.licenseAudit == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	actorID, ok := ctx.Locals("userID").(string)
	if !ok || actorID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	var body request.UpdateBookLicense
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err = r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	license, err := r.licenseAudit.UpdateBookLicense(
		ctx.UserContext(),
		actorID,
		bookID,
		body.LicenseStatus,
		body.Reason,
		body.EvidenceURL,
		expected,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialUpdateBookLicense")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, license, license.UpdatedAt)
}
