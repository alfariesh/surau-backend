package v1

import (
	"net/http"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

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

	edit, err := r.editorial.SaveMetadataDraft(ctx.UserContext(), actorID, entity.BookMetadataEdit{
		BookID:       bookID,
		DisplayTitle: body.DisplayTitle,
		Description:  body.Description,
		CoverURL:     body.CoverURL,
		CategoryID:   body.CategoryID,
		Notes:        body.Notes,
	})
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialSaveMetadataDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
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

	edit, err := r.editorial.PublishMetadataDraft(ctx.UserContext(), actorID, bookID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialPublishMetadataDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
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

	return ctx.Status(http.StatusOK).JSON(edit)
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

	edit, err := r.editorial.SavePageDraft(ctx.UserContext(), actorID, entity.BookPageEdit{
		BookID:      bookID,
		PageID:      pageID,
		ContentHTML: body.ContentHTML,
	})
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialSavePageDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
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

	edit, err := r.editorial.PublishPageDraft(ctx.UserContext(), actorID, bookID, pageID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialPublishPageDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
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

	edit, err := r.editorial.SaveHeadingDraft(ctx.UserContext(), actorID, entity.BookHeadingEdit{
		BookID:    bookID,
		HeadingID: headingID,
		Content:   body.Content,
	})
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialSaveHeadingDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
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

	edit, err := r.editorial.PublishHeadingDraft(ctx.UserContext(), actorID, bookID, headingID)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialPublishHeadingDraft")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(edit)
}
