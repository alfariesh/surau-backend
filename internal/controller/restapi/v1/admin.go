package v1

import (
	"errors"
	"net/http"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

func (r *V1) adminListBooks(ctx *fiber.Ctx) error {
	categoryID, err := optionalQueryInt(ctx, "category_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid category_id")
	}

	hasContent, err := optionalQueryBool(ctx, "has_content")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid has_content")
	}

	status := optionalQueryString(ctx, "status")
	books, total, err := r.editorial.Books(
		ctx.UserContext(),
		ctx.Query("q"),
		status,
		categoryID,
		hasContent,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminListBooks")

		if errors.Is(err, entity.ErrInvalidStatus) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid status")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.BookList{Books: books, Total: total})
}

func (r *V1) adminListTranslationFeedbacks(ctx *fiber.Ctx) error {
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
		r.logEditorialError(err, "restapi - v1 - adminListTranslationFeedbacks")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.TranslationFeedbackList{Feedbacks: feedbacks, Total: total})
}

func (r *V1) adminTranslationFeedbackSummary(ctx *fiber.Ctx) error {
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
		r.logEditorialError(err, "restapi - v1 - adminTranslationFeedbackSummary")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(summary)
}

// @Summary     List missing reader assets
// @Description Admin-only queue of missing kitab metadata, section translations, summaries, and audio for target languages id/en.
// @ID          admin-list-missing-reader-assets
// @Tags        admin
// @Produce     json
// @Param       target_lang query    string false "Target language: id or en; empty returns both" Enums(id,en)
// @Param       asset_type  query    string false "Asset type filter" Enums(book_metadata,category_metadata,author_metadata,section_translation,heading_summary,section_audio)
// @Param       book_id     query    int    false "Book ID"
// @Param       limit       query    int    false "Page size" default(50)
// @Param       offset      query    int    false "Page offset" default(0)
// @Success     200         {object} entity.AdminMissingReaderAssets
// @Failure     400         {object} response.Error
// @Failure     401         {object} response.Error
// @Failure     403         {object} response.Error
// @Failure     500         {object} response.Error
// @Router      /admin/reader/missing-assets [get]
func (r *V1) adminMissingReaderAssets(ctx *fiber.Ctx) error {
	bookID, err := optionalQueryInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	assets, err := r.editorial.MissingReaderAssets(
		ctx.UserContext(),
		ctx.Query("target_lang"),
		ctx.Query("asset_type"),
		bookID,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminMissingReaderAssets")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(assets)
}

func (r *V1) adminUpdatePublication(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	var body request.UpdatePublication
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err = r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	publication, err := r.editorial.UpdatePublication(
		ctx.UserContext(),
		actorID,
		bookID,
		body.Status,
		body.Featured,
		body.SortOrder,
	)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminUpdatePublication")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(publication)
}

func (r *V1) adminSaveMetadataDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	var body request.SaveMetadataDraft
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err = r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	edit, err := r.editorial.SaveMetadataDraft(ctx.UserContext(), actorID, entity.BookMetadataEdit{
		BookID:       bookID,
		DisplayTitle: body.DisplayTitle,
		Description:  body.Description,
		CoverURL:     body.CoverURL,
		CategoryID:   body.CategoryID,
		Notes:        body.Notes,
	})
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminSaveMetadataDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
}

func (r *V1) adminPublishMetadataDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	edit, err := r.editorial.PublishMetadataDraft(ctx.UserContext(), actorID, bookID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminPublishMetadataDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
}

func (r *V1) adminGetPageEdit(ctx *fiber.Ctx) error {
	bookID, pageID, err := pagePath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	edit, err := r.editorial.GetPageEdit(ctx.UserContext(), bookID, pageID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminGetPageEdit")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
}

func (r *V1) adminSavePageDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, pageID, err := pagePath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	var body request.SavePageDraft
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err = r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	edit, err := r.editorial.SavePageDraft(ctx.UserContext(), actorID, entity.BookPageEdit{
		BookID:      bookID,
		PageID:      pageID,
		ContentHTML: body.ContentHTML,
	})
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminSavePageDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
}

func (r *V1) adminPublishPageDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, pageID, err := pagePath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	edit, err := r.editorial.PublishPageDraft(ctx.UserContext(), actorID, bookID, pageID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminPublishPageDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
}

func (r *V1) adminSaveHeadingDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, headingID, err := headingPath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	var body request.SaveHeadingDraft
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err = r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	edit, err := r.editorial.SaveHeadingDraft(ctx.UserContext(), actorID, entity.BookHeadingEdit{
		BookID:    bookID,
		HeadingID: headingID,
		Content:   body.Content,
	})
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminSaveHeadingDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
}

func (r *V1) adminPublishHeadingDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, headingID, err := headingPath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	edit, err := r.editorial.PublishHeadingDraft(ctx.UserContext(), actorID, bookID, headingID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminPublishHeadingDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
}

func (r *V1) adminAddCollectionItem(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	slug := ctx.Params("slug")
	if slug == "" {
		return errorResponse(ctx, http.StatusBadRequest, "invalid collection slug")
	}

	var body request.AddCollectionItem
	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	item, err := r.editorial.AddCollectionItem(ctx.UserContext(), actorID, slug, body.BookID, body.SortOrder)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - adminAddCollectionItem")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(item)
}

func (r *V1) adminResolveTranslationFeedback(ctx *fiber.Ctx) error {
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
		r.logEditorialError(err, "restapi - v1 - adminResolveTranslationFeedback")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(feedback)
}

func (r *V1) adminReopenTranslationFeedback(ctx *fiber.Ctx) error {
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
		r.logEditorialError(err, "restapi - v1 - adminReopenTranslationFeedback")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(feedback)
}

func (r *V1) editorialError(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrUnsupportedLanguage):
		return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
	case errors.Is(err, entity.ErrInvalidAssetType):
		return errorResponse(ctx, http.StatusBadRequest, "invalid asset_type")
	case errors.Is(err, entity.ErrInvalidStatus):
		return errorResponse(ctx, http.StatusBadRequest, "invalid status")
	case errors.Is(err, entity.ErrInvalidFeedback):
		return errorResponse(ctx, http.StatusBadRequest, "invalid feedback")
	case errors.Is(err, entity.ErrInvalidRole):
		return errorResponse(ctx, http.StatusBadRequest, "invalid role")
	case errors.Is(err, entity.ErrDraftNotFound):
		return errorResponse(ctx, http.StatusNotFound, "draft not found")
	case errors.Is(err, entity.ErrFeedbackNotFound):
		return errorResponse(ctx, http.StatusNotFound, "feedback not found")
	case errors.Is(err, entity.ErrBookNotFound):
		return errorResponse(ctx, http.StatusNotFound, "book not found")
	case errors.Is(err, entity.ErrPageNotFound):
		return errorResponse(ctx, http.StatusNotFound, "page not found")
	case errors.Is(err, entity.ErrHeadingNotFound):
		return errorResponse(ctx, http.StatusNotFound, "heading not found")
	case errors.Is(err, entity.ErrForbidden):
		return errorResponse(ctx, http.StatusForbidden, "forbidden")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}

func pagePath(ctx *fiber.Ctx) (int, int, error) {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return 0, 0, errors.New("invalid book_id")
	}

	pageID, err := pathInt(ctx, "page_id")
	if err != nil {
		return 0, 0, errors.New("invalid page_id")
	}

	return bookID, pageID, nil
}

func headingPath(ctx *fiber.Ctx) (int, int, error) {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return 0, 0, errors.New("invalid book_id")
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return 0, 0, errors.New("invalid heading_id")
	}

	return bookID, headingID, nil
}

func optionalQueryString(ctx *fiber.Ctx, key string) *string {
	value := ctx.Query(key)
	if value == "" {
		return nil
	}

	return &value
}
