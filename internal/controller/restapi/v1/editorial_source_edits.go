package v1

import (
	"net/http"
	"time"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	_ "github.com/evrone/go-clean-template/internal/controller/restapi/v1/response" // for swaggo
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     Get source metadata draft
// @Description Get the current source kitab metadata draft. Requires editor or admin role.
// @ID          editorial-get-source-metadata-draft
// @Tags        editorial
// @Produce     json
// @Param       book_id path     int true "Book ID"
// @Success     200     {object} entity.BookMetadataEdit
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/books/{book_id}/metadata-draft [get]
func (r *V1) editorialGetMetadataDraft(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	edit, err := r.editorial.GetMetadataDraft(ctx.UserContext(), bookID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialGetMetadataDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

func (r *V1) editorialSaveMetadataDraft(ctx *fiber.Ctx) error {
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

	if ok, preconditionErr := r.checkEditorialDraftIfMatch(ctx, "restapi - v1 - editorialSaveMetadataDraft - precondition", func() (time.Time, error) {
		current, currentErr := r.editorial.GetMetadataDraft(ctx.UserContext(), bookID)

		return current.UpdatedAt, currentErr
	}); !ok || preconditionErr != nil {
		return preconditionErr
	}

	edit, err := r.editorial.SaveMetadataDraft(ctx.UserContext(), actorID, entity.BookMetadataEdit{
		BookID:       bookID,
		DisplayTitle: body.DisplayTitle,
		Bibliography: body.Bibliography,
		Hint:         body.Hint,
		Description:  body.Description,
		CoverURL:     body.CoverURL,
		CategoryID:   body.CategoryID,
		Notes:        body.Notes,
	})
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialSaveMetadataDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

func (r *V1) editorialPublishMetadataDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	if ok, preconditionErr := r.checkEditorialDraftIfMatch(ctx, "restapi - v1 - editorialPublishMetadataDraft - precondition", func() (time.Time, error) {
		current, currentErr := r.editorial.GetMetadataDraft(ctx.UserContext(), bookID)

		return current.UpdatedAt, currentErr
	}); !ok || preconditionErr != nil {
		return preconditionErr
	}

	edit, err := r.editorial.PublishMetadataDraft(ctx.UserContext(), actorID, bookID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialPublishMetadataDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

func (r *V1) editorialGetPageEdit(ctx *fiber.Ctx) error {
	bookID, pageID, err := pagePath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	edit, err := r.editorial.GetPageEdit(ctx.UserContext(), bookID, pageID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialGetPageEdit")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, pageEditCurrentUpdatedAt(&edit))
}

func (r *V1) editorialSavePageDraft(ctx *fiber.Ctx) error {
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

	if ok, preconditionErr := r.checkEditorialResourceIfMatch(ctx, "restapi - v1 - editorialSavePageDraft - precondition", func() (time.Time, error) {
		current, currentErr := r.editorial.GetPageEdit(ctx.UserContext(), bookID, pageID)
		if currentErr != nil {
			return time.Time{}, currentErr
		}

		return pageEditCurrentUpdatedAt(&current), nil
	}); !ok || preconditionErr != nil {
		return preconditionErr
	}

	edit, err := r.editorial.SavePageDraft(ctx.UserContext(), actorID, entity.BookPageEdit{
		BookID:      bookID,
		PageID:      pageID,
		ContentHTML: body.ContentHTML,
	})
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialSavePageDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

func (r *V1) editorialPublishPageDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, pageID, err := pagePath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	if ok, preconditionErr := r.checkEditorialResourceIfMatch(ctx, "restapi - v1 - editorialPublishPageDraft - precondition", func() (time.Time, error) {
		current, currentErr := r.editorial.GetPageEdit(ctx.UserContext(), bookID, pageID)
		if currentErr != nil {
			return time.Time{}, currentErr
		}

		return pageDraftUpdatedAt(&current), nil
	}); !ok || preconditionErr != nil {
		return preconditionErr
	}

	edit, err := r.editorial.PublishPageDraft(ctx.UserContext(), actorID, bookID, pageID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialPublishPageDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

// @Summary     Get source heading draft
// @Description Get the current source kitab heading title draft. Requires editor or admin role.
// @ID          editorial-get-source-heading-draft
// @Tags        editorial
// @Produce     json
// @Param       book_id    path     int true "Book ID"
// @Param       heading_id path     int true "Heading ID"
// @Success     200        {object} entity.BookHeadingEdit
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/books/{book_id}/headings/{heading_id}/draft [get]
func (r *V1) editorialGetHeadingDraft(ctx *fiber.Ctx) error {
	bookID, headingID, err := headingPath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	edit, err := r.editorial.GetHeadingDraft(ctx.UserContext(), bookID, headingID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialGetHeadingDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

func (r *V1) editorialSaveHeadingDraft(ctx *fiber.Ctx) error {
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

	if ok, preconditionErr := r.checkEditorialDraftIfMatch(ctx, "restapi - v1 - editorialSaveHeadingDraft - precondition", func() (time.Time, error) {
		current, currentErr := r.editorial.GetHeadingDraft(ctx.UserContext(), bookID, headingID)

		return current.UpdatedAt, currentErr
	}); !ok || preconditionErr != nil {
		return preconditionErr
	}

	edit, err := r.editorial.SaveHeadingDraft(ctx.UserContext(), actorID, entity.BookHeadingEdit{
		BookID:    bookID,
		HeadingID: headingID,
		Content:   body.Content,
	})
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialSaveHeadingDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

func (r *V1) editorialPublishHeadingDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, headingID, err := headingPath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	if ok, preconditionErr := r.checkEditorialDraftIfMatch(ctx, "restapi - v1 - editorialPublishHeadingDraft - precondition", func() (time.Time, error) {
		current, currentErr := r.editorial.GetHeadingDraft(ctx.UserContext(), bookID, headingID)

		return current.UpdatedAt, currentErr
	}); !ok || preconditionErr != nil {
		return preconditionErr
	}

	edit, err := r.editorial.PublishHeadingDraft(ctx.UserContext(), actorID, bookID, headingID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialPublishHeadingDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

func pageEditCurrentUpdatedAt(edit *entity.EditorialPageEdit) time.Time {
	if edit == nil {
		return time.Time{}
	}

	if edit.Draft != nil {
		return edit.Draft.UpdatedAt
	}

	return edit.Raw.UpdatedAt
}

func pageDraftUpdatedAt(edit *entity.EditorialPageEdit) time.Time {
	if edit == nil || edit.Draft == nil {
		return time.Time{}
	}

	return edit.Draft.UpdatedAt
}
