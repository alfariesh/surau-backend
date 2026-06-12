package v1

import (
	"errors"
	"net/http"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/evrone/go-clean-template/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
)

// CollabInternal serves the service-to-service bridge used by the collab
// websocket server (Hocuspocus). It is intentionally tiny: read the current
// draft to seed a collaborative document, and write the merged document back
// through the exact same editorial pipeline the REST editor uses, so
// sanitization, content_text extraction, audit logs and revision history stay
// on one write path. Authentication is the X-Internal-Token service secret
// (see middleware.ServiceToken); these routes must never be exposed publicly.
type CollabInternal struct {
	editorial usecase.Editorial
	l         logger.Interface
	v         *validator.Validate
}

// NewInternalRoutes mounts the internal collab endpoints on the given group.
func NewInternalRoutes(group fiber.Router, editorial usecase.Editorial, l logger.Interface) {
	c := &CollabInternal{
		editorial: editorial,
		l:         l,
		v:         validator.New(validator.WithRequiredStructEnabled()),
	}

	group.Get("/collab/books/:book_id/pages/:page_id/draft", c.getPageDraft)
	group.Put("/collab/books/:book_id/pages/:page_id/draft", c.putPageDraft)
}

// getPageDraft returns the content the collab server should seed a fresh
// document from: the current draft when one exists, otherwise the raw page.
func (c *CollabInternal) getPageDraft(ctx *fiber.Ctx) error {
	bookID, pageID, err := pagePath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	edit, err := c.editorial.GetPageEdit(ctx.UserContext(), bookID, pageID)
	if err != nil {
		c.l.Error(err, "restapi - internal - collab getPageDraft")

		return c.collabError(ctx, err)
	}

	body := response.CollabPageDraft{
		BookID: bookID,
		PageID: pageID,
		Source: "raw",
	}
	if edit.Draft != nil {
		body.Source = "draft"
		body.ContentHTML = edit.Draft.ContentHTML
		body.UpdatedAt = edit.Draft.UpdatedAt
	} else {
		body.ContentHTML = edit.Raw.ContentHTML
		body.UpdatedAt = edit.Raw.UpdatedAt
	}

	return ctx.Status(http.StatusOK).JSON(body)
}

// putPageDraft persists the merged collaborative document as the page draft.
// The CRDT already resolved concurrent edits, so the save is unconditional
// (expected=nil); revision history records origin "collab".
func (c *CollabInternal) putPageDraft(ctx *fiber.Ctx) error {
	bookID, pageID, err := pagePath(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}

	var body request.CollabSavePageDraft
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err = c.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	edit, err := c.editorial.SavePageDraft(ctx.UserContext(), body.ActorID, entity.BookPageEdit{
		BookID:      bookID,
		PageID:      pageID,
		ContentHTML: body.ContentHTML,
	}, nil, entity.EditOriginCollab)
	if err != nil {
		c.l.Error(err, "restapi - internal - collab putPageDraft")

		return c.collabError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, edit, edit.UpdatedAt)
}

func (c *CollabInternal) collabError(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrBookNotFound),
		errors.Is(err, entity.ErrPageNotFound),
		errors.Is(err, entity.ErrDraftNotFound):
		return errorResponse(ctx, http.StatusNotFound, "page not found")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}
