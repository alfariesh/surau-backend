package v1

import (
	"errors"
	"net/http"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

func (r *V1) editorialListBooks(ctx *fiber.Ctx) error {
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
		r.logEditorialError(err, "restapi - v1 - editorialListBooks")
		if errors.Is(err, entity.ErrInvalidStatus) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid status")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.BookList{Books: books, Total: total})
}

func (r *V1) editorialUpdatePublication(ctx *fiber.Ctx) error {
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
		r.logEditorialError(err, "restapi - v1 - editorialUpdatePublication")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(publication)
}

func (r *V1) editorialAddCollectionItem(ctx *fiber.Ctx) error {
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
		r.logEditorialError(err, "restapi - v1 - editorialAddCollectionItem")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(item)
}
