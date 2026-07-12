package v1

import (
	"net/http"
	"strings"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	_ "github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response" // swaggo
	_ "github.com/alfariesh/surau-backend/internal/entity"                         // swaggo
	"github.com/gofiber/fiber/v2"
)

// @Summary     List Quran source license decisions
// @Description Protected inventory; defaults to unresolved sources. Requires CapReviewEditorial.
// @ID          editorial-quran-source-licenses
// @Tags        editorial,licenses,quran
// @Produce     json
// @Param       source_kind query string false "Source kind" Enums(script,translation,transliteration)
// @Param       status query string false "Status" Enums(unresolved,unknown,needs_review,permitted,restricted,public_domain,all) default(unresolved)
// @Param       limit query int false "Limit" default(50)
// @Param       offset query int false "Offset" default(0)
// @Success     200 {object} entity.QuranSourceLicenseList
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/source-licenses [get]
func (r *V1) editorialQuranSourceLicenses(ctx *fiber.Ctx) error {
	if r.quranLicenseAudit == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	result, err := r.quranLicenseAudit.QuranSourceLicenses(
		ctx.UserContext(), ctx.Query("source_kind"), ctx.Query("status"),
		queryInt(ctx, "limit", editorialLicenseAuditDefaultLimit), queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialQuranSourceLicenses")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(result)
}

// @Summary     Get one Quran source license decision
// @Description Return current attribution, ETag state, and append-only history.
// @ID          editorial-quran-source-license
// @Tags        editorial,licenses,quran
// @Produce     json
// @Param       source_kind path string true "Source kind" Enums(script,translation,transliteration)
// @Param       source_id path string true "Source ID"
// @Success     200 {object} entity.QuranSourceLicense
// @Failure     404 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/source-licenses/{source_kind}/{source_id} [get]
func (r *V1) editorialQuranSourceLicense(ctx *fiber.Ctx) error {
	if r.quranLicenseAudit == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	result, err := r.quranLicenseAudit.QuranSourceLicense(
		ctx.UserContext(), ctx.Params("source_kind"), ctx.Params("source_id"),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialQuranSourceLicense")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, result, result.UpdatedAt)
}

// @Summary     Record a Quran source license decision
// @Description Requires CapPublishProduction, fresh MFA, evidence, attribution, and If-Match.
// @ID          editorial-update-quran-source-license
// @Tags        editorial,licenses,quran
// @Accept      json
// @Produce     json
// @Param       source_kind path string true "Source kind" Enums(script,translation,transliteration)
// @Param       source_id path string true "Source ID"
// @Param       If-Match header string true "Current ETag or *"
// @Param       request body request.UpdateQuranSourceLicense true "Decision and attribution"
// @Success     200 {object} entity.QuranSourceLicense
// @Failure     400 {object} response.Error
// @Failure     412 {object} response.Error
// @Failure     428 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/source-licenses/{source_kind}/{source_id} [patch]
func (r *V1) editorialUpdateQuranSourceLicense(ctx *fiber.Ctx) error {
	if r.quranLicenseAudit == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	actorID, ok := ctx.Locals("userID").(string)
	if !ok || strings.TrimSpace(actorID) == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.UpdateQuranSourceLicense
	if err := ctx.BodyParser(&body); err != nil || r.v.Struct(body) != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	result, err := r.quranLicenseAudit.UpdateQuranSourceLicense(
		ctx.UserContext(), actorID, ctx.Params("source_kind"), ctx.Params("source_id"),
		body.LicenseStatus, body.Reason, body.EvidenceURL, body.Translator,
		body.ResponsibleName, body.ResponsibleRole, expected,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialUpdateQuranSourceLicense")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, result, result.UpdatedAt)
}
