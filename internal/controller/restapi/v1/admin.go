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
		r.l.Error(err, "restapi - v1 - adminListBooks")

		if errors.Is(err, entity.ErrInvalidStatus) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid status")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.BookList{Books: books, Total: total})
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
		r.l.Error(err, "restapi - v1 - adminUpdatePublication")

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
		r.l.Error(err, "restapi - v1 - adminSaveMetadataDraft")

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
		r.l.Error(err, "restapi - v1 - adminPublishMetadataDraft")

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
		r.l.Error(err, "restapi - v1 - adminGetPageEdit")

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
		r.l.Error(err, "restapi - v1 - adminSavePageDraft")

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
		r.l.Error(err, "restapi - v1 - adminPublishPageDraft")

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
		r.l.Error(err, "restapi - v1 - adminSaveHeadingDraft")

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
		r.l.Error(err, "restapi - v1 - adminPublishHeadingDraft")

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
		r.l.Error(err, "restapi - v1 - adminAddCollectionItem")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(item)
}

func (r *V1) editorialError(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrInvalidStatus):
		return errorResponse(ctx, http.StatusBadRequest, "invalid status")
	case errors.Is(err, entity.ErrInvalidRole):
		return errorResponse(ctx, http.StatusBadRequest, "invalid role")
	case errors.Is(err, entity.ErrDraftNotFound):
		return errorResponse(ctx, http.StatusNotFound, "draft not found")
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
