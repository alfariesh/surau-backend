package v1

import (
	"errors"
	"net/http"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/logger"
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

// InternalRouteManifest is the audited route registry. Tests compare it with
// Fiber's live /internal route table so a future endpoint cannot bypass named
// service authentication by accident.
var InternalRouteManifest = []struct { //nolint:gochecknoglobals // immutable security contract
	Method string
	Path   string
	Scope  string
}{
	{Method: fiber.MethodGet, Path: "/collab/whoami", Scope: entity.ServiceScopeCollabDraftWrite},
	{Method: fiber.MethodGet, Path: "/collab/books/:book_id/pages/:page_id/draft", Scope: entity.ServiceScopeCollabDraftWrite},
	{Method: fiber.MethodPut, Path: "/collab/books/:book_id/pages/:page_id/draft", Scope: entity.ServiceScopeCollabDraftWrite},
}

// NewInternalRoutes mounts every internal endpoint through the scoped audit
// helper. There is intentionally no unguarded registration escape hatch.
func NewInternalRoutes(
	group fiber.Router,
	editorial usecase.Editorial,
	identities usecase.ServiceIdentity,
	l logger.Interface,
) {
	c := &CollabInternal{
		editorial: editorial,
		l:         l,
		v:         validator.New(validator.WithRequiredStructEnabled()),
	}

	register := func(method, path, scope string, handler fiber.Handler) {
		group.Add(method, path, middleware.RequireServicePrincipal(identities, scope, l), handler)
	}
	register(fiber.MethodGet, InternalRouteManifest[0].Path, InternalRouteManifest[0].Scope, c.whoami)
	register(fiber.MethodGet, InternalRouteManifest[1].Path, InternalRouteManifest[1].Scope, c.getPageDraft)
	register(fiber.MethodPut, InternalRouteManifest[2].Path, InternalRouteManifest[2].Scope, c.putPageDraft)
}

func (c *CollabInternal) whoami(ctx *fiber.Ctx) error {
	auth, ok := middleware.ServiceAuthentication(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusServiceUnavailable, "service identity unavailable")
	}

	return ctx.Status(http.StatusOK).JSON(fiber.Map{
		"principal_name": auth.PrincipalName,
		"scopes":         auth.Scopes,
		"token_id":       auth.TokenID,
		"expires_at":     auth.ExpiresAt,
	})
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
