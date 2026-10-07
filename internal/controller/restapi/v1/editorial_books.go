package v1

import (
	"errors"
	"net/http"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     List books for editorial review
// @Description List source kitab with their publication state for the editorial queue. Requires editor or admin role.
// @ID          editorial-list-books
// @Tags        editorial
// @Produce     json
// @Param       q           query    string false "Search title, display title, author, or category"
// @Param       status      query    string false "Publication status" Enums(hidden, draft, published, archived)
// @Param       category_id query    int    false "Category ID"
// @Param       has_content query    bool   false "Only books with (true) or without (false) imported content"
// @Param       limit       query    int    false "Page size (default 50, max 200)"
// @Param       offset      query    int    false "Offset"
// @Success     200         {object} response.BookList
// @Failure     400         {object} response.Error
// @Failure     401         {object} response.Error
// @Failure     403         {object} response.Error
// @Failure     500         {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/books [get]
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
		r.logEditorialError(ctx, err, "restapi - v1 - editorialListBooks")
		if errors.Is(err, entity.ErrInvalidStatus) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid status")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.BookList{Items: books, Total: total})
}

// @Summary     Update book publication
// @Description Set a book's publication status, featured flag, and sort order. Publishing requires a permitted license. Requires CapPublishProduction and fresh MFA.
// @ID          editorial-update-publication
// @Tags        editorial
// @Accept      json
// @Produce     json
// @Param       book_id path     int                       true "Book ID"
// @Param       request body     request.UpdatePublication true "Publication state"
// @Success     200     {object} entity.BookPublication
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     409     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/books/{book_id}/publication [put]
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
		r.logEditorialError(ctx, err, "restapi - v1 - editorialUpdatePublication")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(publication)
}

// @Summary     Add book to collection
// @Description Add a book to a curated collection, or update its sort order when already present. Requires CapPublishProduction and fresh MFA.
// @ID          editorial-add-collection-item
// @Tags        editorial
// @Accept      json
// @Produce     json
// @Param       slug    path     string                    true "Collection slug"
// @Param       request body     request.AddCollectionItem true "Collection item"
// @Success     200     {object} entity.BookCollectionItem
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     409     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/collections/{slug}/items [post]
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
		r.logEditorialError(ctx, err, "restapi - v1 - editorialAddCollectionItem")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(item)
}
