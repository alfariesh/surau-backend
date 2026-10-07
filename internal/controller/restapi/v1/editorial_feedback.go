package v1

import (
	"net/http"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	// The Swagger annotations reference entity types this file never names in
	// code; swag resolves them through this blank import.
	_ "github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     List translation feedback
// @Description List reader feedback on section translations, newest first. Requires editor or admin role.
// @ID          editorial-list-translation-feedbacks
// @Tags        editorial
// @Produce     json
// @Param       book_id    query    int    false "Book ID"
// @Param       heading_id query    int    false "Heading ID"
// @Param       lang       query    string false "Translation language"
// @Param       vote       query    string false "Reader vote" Enums(like, dislike)
// @Param       status     query    string false "Feedback status (default open)" Enums(open, resolved, all)
// @Param       limit      query    int    false "Page size (default 50, max 200)"
// @Param       offset     query    int    false "Offset"
// @Success     200        {object} response.TranslationFeedbackList
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/translation-feedbacks [get]
func (r *V1) editorialListTranslationFeedbacks(ctx *fiber.Ctx) error {
	bookID, err := optionalQueryInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	headingID, err := optionalQueryInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	feedbacks, total, err := r.editorial.TranslationFeedbacks(
		ctx.UserContext(),
		bookID,
		headingID,
		ctx.Query("lang"),
		ctx.Query("vote"),
		ctx.Query("status"),
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialListTranslationFeedbacks")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.TranslationFeedbackList{Feedbacks: feedbacks, Total: total})
}

// @Summary     Summarize translation feedback
// @Description Aggregate reader translation feedback counts and the most-disliked headings. Requires editor or admin role.
// @ID          editorial-translation-feedback-summary
// @Tags        editorial
// @Produce     json
// @Param       book_id    query    int    false "Book ID"
// @Param       heading_id query    int    false "Heading ID"
// @Param       lang       query    string false "Translation language"
// @Param       vote       query    string false "Reader vote" Enums(like, dislike)
// @Param       status     query    string false "Feedback status (default open)" Enums(open, resolved, all)
// @Param       limit      query    int    false "Top headings to include (default 20, max 200)"
// @Success     200        {object} entity.EditorialTranslationFeedbackSummary
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/translation-feedbacks/summary [get]
func (r *V1) editorialTranslationFeedbackSummary(ctx *fiber.Ctx) error {
	bookID, err := optionalQueryInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	headingID, err := optionalQueryInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	summary, err := r.editorial.TranslationFeedbackSummary(
		ctx.UserContext(),
		bookID,
		headingID,
		ctx.Query("lang"),
		ctx.Query("vote"),
		ctx.Query("status"),
		queryInt(ctx, "limit", 20),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialTranslationFeedbackSummary")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(summary)
}

// @Summary     Resolve translation feedback
// @Description Mark one reader translation feedback item as handled, with an optional note. Requires editor or admin role.
// @ID          editorial-resolve-translation-feedback
// @Tags        editorial
// @Accept      json
// @Produce     json
// @Param       id      path     string                             true  "Feedback ID"
// @Param       request body     request.ResolveTranslationFeedback false "Optional resolution note"
// @Success     200     {object} entity.EditorialTranslationFeedback
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/translation-feedbacks/{id}/resolve [post]
func (r *V1) editorialResolveTranslationFeedback(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	feedbackID := ctx.Params("id")
	if feedbackID == "" {
		return errorResponse(ctx, http.StatusBadRequest, "invalid feedback id")
	}

	var body request.ResolveTranslationFeedback
	if len(ctx.Body()) > 0 {
		if err := ctx.BodyParser(&body); err != nil {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	feedback, err := r.editorial.ResolveTranslationFeedback(ctx.UserContext(), actorID, feedbackID, body.Note)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialResolveTranslationFeedback")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(feedback)
}

// @Summary     Reopen translation feedback
// @Description Return a resolved reader translation feedback item to the open queue. Requires editor or admin role.
// @ID          editorial-reopen-translation-feedback
// @Tags        editorial
// @Produce     json
// @Param       id  path     string true "Feedback ID"
// @Success     200 {object} entity.EditorialTranslationFeedback
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/translation-feedbacks/{id}/reopen [post]
func (r *V1) editorialReopenTranslationFeedback(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	feedbackID := ctx.Params("id")
	if feedbackID == "" {
		return errorResponse(ctx, http.StatusBadRequest, "invalid feedback id")
	}

	feedback, err := r.editorial.ReopenTranslationFeedback(ctx.UserContext(), actorID, feedbackID)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialReopenTranslationFeedback")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(feedback)
}
