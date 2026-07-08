package v1

import (
	"net/http"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
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
		r.logEditorialError(ctx, err, "restapi - v1 - editorialGetMetadataDraft")

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

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, false)
	if !ok {
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
	}, expected, entity.EditOriginREST)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSaveMetadataDraft")

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

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, false)
	if !ok {
		return preconditionErr
	}

	edit, err := r.editorial.PublishMetadataDraft(ctx.UserContext(), actorID, bookID, expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialPublishMetadataDraft")

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
		r.logEditorialError(ctx, err, "restapi - v1 - editorialGetPageEdit")

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

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	edit, err := r.editorial.SavePageDraft(ctx.UserContext(), actorID, entity.BookPageEdit{
		BookID:      bookID,
		PageID:      pageID,
		ContentHTML: body.ContentHTML,
	}, expected, entity.EditOriginREST)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSavePageDraft")

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

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	edit, err := r.editorial.PublishPageDraft(ctx.UserContext(), actorID, bookID, pageID, expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialPublishPageDraft")

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
		r.logEditorialError(ctx, err, "restapi - v1 - editorialGetHeadingDraft")

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

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, false)
	if !ok {
		return preconditionErr
	}

	edit, err := r.editorial.SaveHeadingDraft(ctx.UserContext(), actorID, entity.BookHeadingEdit{
		BookID:    bookID,
		HeadingID: headingID,
		Content:   body.Content,
	}, expected, entity.EditOriginREST)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSaveHeadingDraft")

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

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, false)
	if !ok {
		return preconditionErr
	}

	edit, err := r.editorial.PublishHeadingDraft(ctx.UserContext(), actorID, bookID, headingID, expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialPublishHeadingDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

// defaultRevisionPageSize is the default page size for revision listings.
const defaultRevisionPageSize = 50

// @Summary     List page draft revisions
// @Description List the newest-first revision history for one page draft. Requires editor or admin role.
// @ID          editorial-list-page-draft-revisions
// @Tags        editorial
// @Produce     json
// @Param       book_id path     int true  "Book ID"
// @Param       page_id path     int true  "Page ID"
// @Param       limit   query    int false "Page size (default 50, max 200)"
// @Param       offset  query    int false "Offset"
// @Success     200     {object} response.SourceEditRevisionList
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/books/{book_id}/pages/{page_id}/draft-revisions [get]
func (r *V1) editorialListPageDraftRevisions(ctx *fiber.Ctx) error {
	bookID, pageID, err := pagePath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	revisions, total, err := r.editorial.PageDraftRevisions(
		ctx.UserContext(),
		bookID,
		pageID,
		queryInt(ctx, "limit", defaultRevisionPageSize),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialListPageDraftRevisions")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.SourceEditRevisionList{Revisions: revisions, Total: total})
}

// @Summary     Restore page draft revision
// @Description Replay a previous page draft snapshot as the current draft; the restore creates a new revision. Requires editor or admin role.
// @ID          editorial-restore-page-draft-revision
// @Tags        editorial
// @Produce     json
// @Param       book_id     path     int    true "Book ID"
// @Param       page_id     path     int    true "Page ID"
// @Param       revision_id path     string true "Revision ID"
// @Success     200         {object} entity.BookPageEdit
// @Failure     400         {object} response.Error
// @Failure     401         {object} response.Error
// @Failure     403         {object} response.Error
// @Failure     404         {object} response.Error
// @Failure     500         {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/books/{book_id}/pages/{page_id}/draft-revisions/{revision_id}/restore [post]
func (r *V1) editorialRestorePageDraftRevision(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, pageID, err := pagePath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	edit, err := r.editorial.RestorePageDraftRevision(ctx.UserContext(), actorID, bookID, pageID, ctx.Params("revision_id"))
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialRestorePageDraftRevision")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

// editorialIfMatch parses If-Match for source-edit writes. The expected
// timestamp travels down to the repo where it is enforced atomically inside
// the upsert (no read-then-write race). require demands the header (428) —
// used for page content, the high-value lost-work surface.
func (r *V1) editorialIfMatch(ctx *fiber.Ctx, require bool) (*time.Time, bool, error) {
	expected, present, ok := parseIfMatch(ctx)
	if !ok {
		return nil, false, errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	if require && !present {
		return nil, false, errorResponse(ctx, http.StatusPreconditionRequired, "if-match header required")
	}

	return expected, true, nil
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
