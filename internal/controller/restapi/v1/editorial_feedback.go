package v1

import (
	"net/http"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/gofiber/fiber/v2"
)

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
		r.logEditorialError(err, "restapi - v1 - editorialListTranslationFeedbacks")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.TranslationFeedbackList{Feedbacks: feedbacks, Total: total})
}

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
		r.logEditorialError(err, "restapi - v1 - editorialTranslationFeedbackSummary")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(summary)
}

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
		r.logEditorialError(err, "restapi - v1 - editorialResolveTranslationFeedback")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(feedback)
}

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
		r.logEditorialError(err, "restapi - v1 - editorialReopenTranslationFeedback")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(feedback)
}
