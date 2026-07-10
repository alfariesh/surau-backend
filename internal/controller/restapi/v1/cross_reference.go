package v1

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	_ "github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response" // for swaggo
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/gofiber/fiber/v2"
)

const crossReferenceDefaultLimit uint64 = 50

// @Summary     List Cross-References in either direction
// @Description Return approved, publicly visible content-to-content claims for one canonical Anchor. Incoming Quran ayah queries include target ranges containing that ayah; surah-only targets do not imply every ayah.
// @ID          list-cross-references
// @Tags        cross-references
// @Produce     json
// @Param       anchor    query    string true  "Canonical Anchor"
// @Param       direction query    string true  "Direction" Enums(incoming,outgoing)
// @Param       kind      query    string false "Claim kind" Enums(cites,quotes,explains,parallel)
// @Param       limit     query    int    false "Limit (max 200)" default(50)
// @Param       offset    query    int    false "Offset (max 10000)" default(0)
// @Success     200       {object} entity.CrossReferenceList
// @Failure     400       {object} response.Error
// @Failure     500       {object} response.Error
// @Router      /cross-references [get]
func (r *V1) listCrossReferences(ctx *fiber.Ctx) error {
	limit, offset, err := crossReferencePagination(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid cross-reference")
	}

	result, err := r.crossReference.ListPublic(
		ctx.UserContext(),
		ctx.Query("anchor"),
		ctx.Query("direction"),
		ctx.Query("kind"),
		limit,
		offset,
	)
	if err != nil {
		r.logCrossReferenceError(ctx, err, "restapi - v1 - listCrossReferences")

		return r.crossReferenceError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(result)
}

// @Summary     List Cross-References for editorial review
// @Description List the protected review queue. Requires CapReviewEditorial; unlike the public endpoint this surface can inspect all five review states.
// @ID          editorial-list-cross-references
// @Tags        editorial,cross-references
// @Produce     json
// @Param       anchor        query    string false "Canonical Anchor"
// @Param       direction     query    string false "Direction (required with anchor)" Enums(incoming,outgoing)
// @Param       kind          query    string false "Claim kind" Enums(cites,quotes,explains,parallel)
// @Param       method        query    string false "Attribution method" Enums(resolver,machine,human)
// @Param       review_status query    string false "Review state" Enums(pending,approved,rejected,ambiguous,needs_review)
// @Param       limit         query    int    false "Limit (max 200)" default(50)
// @Param       offset        query    int    false "Offset (max 10000)" default(0)
// @Success     200           {object} entity.CrossReferenceList
// @Failure     400           {object} response.Error
// @Failure     401           {object} response.Error
// @Failure     403           {object} response.Error
// @Failure     500           {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/cross-references [get]
func (r *V1) editorialListCrossReferences(ctx *fiber.Ctx) error {
	limit, offset, err := crossReferencePagination(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid cross-reference")
	}

	result, err := r.crossReference.ListEditorial(restAuthContext(ctx), repo.CrossReferenceFilter{
		Anchor:       ctx.Query("anchor"),
		Direction:    ctx.Query("direction"),
		Kind:         ctx.Query("kind"),
		Method:       ctx.Query("method"),
		ReviewStatus: ctx.Query("review_status"),
		Limit:        limit,
		Offset:       offset,
	})
	if err != nil {
		r.logCrossReferenceError(ctx, err, "restapi - v1 - editorialListCrossReferences")

		return r.crossReferenceError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(result)
}

// @Summary     Create a human Cross-Reference
// @Description Create a pending, session-attributed claim. Method, actor, review state, origin, and normalized evidence are assigned by the server.
// @ID          editorial-create-cross-reference
// @Tags        editorial,cross-references
// @Accept      json
// @Produce     json
// @Param       request body     request.CreateCrossReference true "Human Cross-Reference"
// @Success     201     {object} entity.CrossReference
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     409     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/cross-references [post]
func (r *V1) editorialCreateCrossReference(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok || actorID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.CreateCrossReference
	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	created, err := r.crossReference.CreateHuman(restAuthContext(ctx), entity.CrossReferenceCreateInput{
		SourceAnchor: body.SourceAnchor,
		TargetAnchor: body.TargetAnchor,
		Kind:         body.Kind,
		Confidence:   *body.Confidence,
		EvidenceText: body.EvidenceText,
		Metadata:     entity.RawJSON(body.Metadata),
	}, actorID)
	if err != nil {
		r.logCrossReferenceError(ctx, err, "restapi - v1 - editorialCreateCrossReference")

		return r.crossReferenceError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusCreated, created, created.UpdatedAt)
}

// @Summary     Get one Cross-Reference
// @Description Get one claim, including evidence and attribution, for editorial review.
// @ID          editorial-get-cross-reference
// @Tags        editorial,cross-references
// @Produce     json
// @Param       id  path     string true "Cross-Reference UUID"
// @Success     200 {object} entity.CrossReference
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/cross-references/{id} [get]
func (r *V1) editorialGetCrossReference(ctx *fiber.Ctx) error {
	ref, err := r.crossReference.Get(restAuthContext(ctx), ctx.Params("id"))
	if err != nil {
		r.logCrossReferenceError(ctx, err, "restapi - v1 - editorialGetCrossReference")

		return r.crossReferenceError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, ref, ref.UpdatedAt)
}

// @Summary     Review a Cross-Reference
// @Description Set any of the existing five review states. If-Match is required; use the ETag from GET, or * for an explicit unconditional transition.
// @ID          editorial-review-cross-reference
// @Tags        editorial,cross-references
// @Accept      json
// @Produce     json
// @Param       id       path   string true "Cross-Reference UUID"
// @Param       If-Match header string true "Current ETag or *"
// @Param       request  body   request.ReviewCrossReference true "Review decision"
// @Success     200      {object} entity.CrossReference
// @Failure     400      {object} response.Error
// @Failure     401      {object} response.Error
// @Failure     403      {object} response.Error
// @Failure     404      {object} response.Error
// @Failure     412      {object} response.Error
// @Failure     428      {object} response.Error
// @Failure     500      {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/cross-references/{id}/review [patch]
func (r *V1) editorialReviewCrossReference(ctx *fiber.Ctx) error {
	reviewerID, ok := ctx.Locals("userID").(string)
	if !ok || reviewerID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.ReviewCrossReference
	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	ref, err := r.crossReference.Review(
		restAuthContext(ctx),
		ctx.Params("id"),
		body.ReviewStatus,
		reviewerID,
		expected,
	)
	if err != nil {
		r.logCrossReferenceError(ctx, err, "restapi - v1 - editorialReviewCrossReference")

		return r.crossReferenceError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, ref, ref.UpdatedAt)
}

func (r *V1) crossReferenceError(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrInvalidCrossReference), errors.Is(err, entity.ErrInvalidAnchor):
		return errorResponse(ctx, http.StatusBadRequest, "invalid cross-reference")
	case errors.Is(err, entity.ErrCrossReferenceNotFound), errors.Is(err, entity.ErrAnchorNotFound):
		return errorResponse(ctx, http.StatusNotFound, "cross-reference not found")
	case errors.Is(err, entity.ErrCrossReferenceConflict):
		return errorResponse(ctx, http.StatusConflict, "cross-reference already exists")
	case errors.Is(err, entity.ErrPreconditionFailed):
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}

func crossReferencePagination(ctx *fiber.Ctx) (limit, offset uint64, err error) {
	limit, err = strictCrossReferenceQueryUint(ctx, "limit", crossReferenceDefaultLimit)
	if err != nil {
		return 0, 0, err
	}

	offset, err = strictCrossReferenceQueryUint(ctx, "offset", 0)
	if err != nil {
		return 0, 0, err
	}

	return limit, offset, nil
}

func strictCrossReferenceQueryUint(ctx *fiber.Ctx, key string, defaultValue uint64) (uint64, error) {
	raw := ctx.Query(key)
	if raw == "" {
		return defaultValue, nil
	}

	for i := range len(raw) {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, entity.ErrInvalidCrossReference
		}
	}

	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, entity.ErrInvalidCrossReference
	}

	return parsed, nil
}
