package v1

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     List production candidates
// @Description List raw source books with counts and current production state for one target language. Requires editor or admin role.
// @ID          editorial-list-production-candidates
// @Tags        editorial-production
// @Produce     json
// @Param       lang        query    string true  "Target language" Enums(id,en)
// @Param       q           query    string false "Search query"
// @Param       category_id query    int    false "Category ID"
// @Param       author_id   query    int    false "Author ID"
// @Param       has_content query    bool   false "Filter source books that have imported content"
// @Param       unstarted   query    bool   false "Only books without an active production project for lang"
// @Param       limit       query    int    false "Page size" default(50)
// @Param       offset      query    int    false "Page offset" default(0)
// @Success     200         {object} response.ProductionCandidateList
// @Failure     400         {object} response.Error
// @Failure     401         {object} response.Error
// @Failure     403         {object} response.Error
// @Failure     500         {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-candidates [get]
func (r *V1) editorialListProductionCandidates(ctx *fiber.Ctx) error {
	categoryID, err := optionalQueryInt(ctx, "category_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid category_id")
	}
	authorID, err := optionalQueryInt(ctx, "author_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid author_id")
	}
	hasContent, err := optionalQueryBool(ctx, "has_content")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid has_content")
	}
	unstartedValue, err := optionalQueryBool(ctx, "unstarted")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid unstarted")
	}

	unstarted := unstartedValue != nil && *unstartedValue
	candidates, total, err := r.editorial.ProductionCandidates(
		ctx.UserContext(),
		ctx.Query("lang"),
		ctx.Query("q"),
		categoryID,
		authorID,
		hasContent,
		unstarted,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialListProductionCandidates")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.ProductionCandidateList{Candidates: candidates, Total: total})
}

// @Summary     Get production dashboard
// @Description Get compact production counts and recent activity for one target language. Requires editor or admin role.
// @ID          editorial-get-production-dashboard
// @Tags        editorial-production
// @Produce     json
// @Param       lang           query    string true  "Target language" Enums(id,en)
// @Param       activity_limit query    int    false "Recent event count" default(20)
// @Success     200            {object} entity.BookProductionDashboard
// @Failure     400            {object} response.Error
// @Failure     401            {object} response.Error
// @Failure     403            {object} response.Error
// @Failure     500            {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-dashboard [get]
func (r *V1) editorialProductionDashboard(ctx *fiber.Ctx) error {
	dashboard, err := r.editorial.ProductionDashboard(
		ctx.UserContext(),
		ctx.Query("lang"),
		queryInt(ctx, "activity_limit", 20),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialProductionDashboard")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(dashboard)
}

// @Summary     List global production activity
// @Description List recent operational timeline events across production projects. Requires editor or admin role.
// @ID          editorial-list-global-production-activity
// @Tags        editorial-production
// @Produce     json
// @Param       lang   query string false "Target language" Enums(id,en)
// @Param       limit  query int    false "Page size" default(50)
// @Param       offset query int    false "Page offset" default(0)
// @Success     200    {object} response.ProductionEventList
// @Failure     400    {object} response.Error
// @Failure     401    {object} response.Error
// @Failure     403    {object} response.Error
// @Failure     500    {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-activity [get]
func (r *V1) editorialGlobalProductionActivity(ctx *fiber.Ctx) error {
	events, total, err := r.editorial.GlobalProductionActivity(
		ctx.UserContext(),
		ctx.Query("lang"),
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialGlobalProductionActivity")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.ProductionEventList{Events: events, Total: total})
}

// @Summary     Create production project
// @Description Start one book+language translation production workflow. Requires editor or admin role.
// @ID          editorial-create-production-project
// @Tags        editorial-production
// @Accept      json
// @Produce     json
// @Param       request body request.CreateProductionProject true "Production project"
// @Success     201 {object} entity.BookProductionProject
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     409 {object} response.ProductionProjectConflict
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects [post]
func (r *V1) editorialCreateProductionProject(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.CreateProductionProject
	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}
	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	requiresReview := true
	if body.RequiresReview != nil {
		requiresReview = *body.RequiresReview
	}

	project, err := r.editorial.CreateProductionProject(ctx.UserContext(), actorID, entity.BookProductionProject{
		BookID:         body.BookID,
		Lang:           body.Lang,
		WorkflowStatus: entity.ProductionWorkflowCandidate,
		RequiresReview: requiresReview,
		RequiresAudio:  body.RequiresAudio,
		Priority:       body.Priority,
		OwnerID:        body.OwnerID,
		Notes:          body.Notes,
	})
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialCreateProductionProject")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusCreated, project, project.UpdatedAt)
}

// @Summary     List production projects
// @Description List translation production workflows. Requires editor or admin role.
// @ID          editorial-list-production-projects
// @Tags        editorial-production
// @Produce     json
// @Param       book_id            query    int    false "Book ID"
// @Param       lang               query    string false "Target language" Enums(id,en)
// @Param       workflow_status    query    string false "Workflow status" Enums(candidate,drafting,in_review,ready,published,archived)
// @Param       publication_status query    string false "Publication status" Enums(hidden,published,archived)
// @Param       ready_to_publish   query    bool   false "Only active hidden projects ready to publish"
// @Param       needs_work         query    bool   false "Only active hidden projects that still need work"
// @Param       limit              query    int    false "Page size" default(50)
// @Param       offset             query    int    false "Page offset" default(0)
// @Success     200                {object} response.ProductionProjectList
// @Failure     400                {object} response.Error
// @Failure     401                {object} response.Error
// @Failure     403                {object} response.Error
// @Failure     500                {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects [get]
func (r *V1) editorialListProductionProjects(ctx *fiber.Ctx) error {
	bookID, err := optionalQueryInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}
	readyToPublish, err := optionalQueryBool(ctx, "ready_to_publish")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid ready_to_publish")
	}
	needsWork, err := optionalQueryBool(ctx, "needs_work")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid needs_work")
	}

	projects, total, err := r.editorial.ProductionProjects(
		ctx.UserContext(),
		bookID,
		ctx.Query("lang"),
		ctx.Query("workflow_status"),
		ctx.Query("publication_status"),
		readyToPublish != nil && *readyToPublish,
		needsWork != nil && *needsWork,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialListProductionProjects")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.ProductionProjectList{Projects: projects, Total: total})
}

// @Summary     Get production project
// @Description Get one translation production workflow. Requires editor or admin role.
// @ID          editorial-get-production-project
// @Tags        editorial-production
// @Produce     json
// @Param       id  path     string true "Production project ID"
// @Success     200 {object} entity.BookProductionProject
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id} [get]
func (r *V1) editorialGetProductionProject(ctx *fiber.Ctx) error {
	project, err := r.editorial.ProductionProject(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialGetProductionProject")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, project, project.UpdatedAt)
}

// @Summary     Get production workspace
// @Description Get project, source book, TOC draft status, final asset flags, and completeness in one editor payload. Requires editor or admin role.
// @ID          editorial-get-production-workspace
// @Tags        editorial-production
// @Produce     json
// @Param       id  path     string true "Production project ID"
// @Success     200 {object} entity.BookProductionWorkspace
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/workspace [get]
func (r *V1) editorialProductionWorkspace(ctx *fiber.Ctx) error {
	workspace, err := r.editorial.ProductionWorkspace(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialProductionWorkspace")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(workspace)
}

// @Summary     Get production activity
// @Description List operational timeline events for one production project. Requires editor or admin role.
// @ID          editorial-get-production-activity
// @Tags        editorial-production
// @Produce     json
// @Param       id     path  string true  "Production project ID"
// @Param       limit  query int    false "Page size" default(50)
// @Param       offset query int    false "Page offset" default(0)
// @Success     200 {object} response.ProductionEventList
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/activity [get]
func (r *V1) editorialProductionActivity(ctx *fiber.Ctx) error {
	events, total, err := r.editorial.ProductionActivity(
		ctx.UserContext(),
		ctx.Params("id"),
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialProductionActivity")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.ProductionEventList{Events: events, Total: total})
}

// @Summary     List production draft revisions
// @Description List immutable snapshots for one production draft asset. Requires editor or admin role.
// @ID          editorial-list-production-draft-revisions
// @Tags        editorial-production
// @Produce     json
// @Param       id         path  string true  "Production project ID"
// @Param       asset_type query string true  "Asset type" Enums(book_metadata,author_metadata,category_metadata,section_translation,heading_summary,section_audio)
// @Param       heading_id query int    false "Heading ID for TOC-scoped assets"
// @Param       limit      query int    false "Page size" default(50)
// @Param       offset     query int    false "Page offset" default(0)
// @Success     200 {object} response.ProductionDraftRevisionList
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/draft-revisions [get]
func (r *V1) editorialListProductionDraftRevisions(ctx *fiber.Ctx) error {
	headingID, err := optionalQueryInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	revisions, total, err := r.editorial.ProductionDraftRevisions(
		ctx.UserContext(),
		ctx.Params("id"),
		ctx.Query("asset_type"),
		headingID,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialListProductionDraftRevisions")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.ProductionDraftRevisionList{Revisions: revisions, Total: total})
}

// @Summary     Restore production draft revision
// @Description Restore a previous draft snapshot into the current draft and create a new revision. Requires editor or admin role.
// @ID          editorial-restore-production-draft-revision
// @Tags        editorial-production
// @Produce     json
// @Param       id          path string true "Production project ID"
// @Param       revision_id path string true "Draft revision ID"
// @Success     200 {object} entity.BookProductionDraftRevision
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/draft-revisions/{revision_id}/restore [post]
func (r *V1) editorialRestoreProductionDraftRevision(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	revision, err := r.editorial.RestoreProductionDraftRevision(
		ctx.UserContext(),
		actorID,
		ctx.Params("id"),
		ctx.Params("revision_id"),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialRestoreProductionDraftRevision")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(revision)
}

// @Summary     Get production publish check
// @Description Explain whether a production project can be published and which required assets still block it. Requires editor or admin role.
// @ID          editorial-get-production-publish-check
// @Tags        editorial-production
// @Produce     json
// @Param       id  path     string true "Production project ID"
// @Success     200 {object} entity.BookProductionPublishCheck
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/publish-check [get]
func (r *V1) editorialProductionPublishCheck(ctx *fiber.Ctx) error {
	check, err := r.editorial.ProductionPublishCheck(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialProductionPublishCheck")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(check)
}

// @Summary     Update production project
// @Description Update production workflow settings. Requires editor or admin role.
// @ID          editorial-update-production-project
// @Tags        editorial-production
// @Accept      json
// @Produce     json
// @Param       id      path string                          true "Production project ID"
// @Param       request body request.UpdateProductionProject true "Production project patch"
// @Success     200     {object} entity.BookProductionProject
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id} [patch]
func (r *V1) editorialUpdateProductionProject(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.UpdateProductionProject
	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}
	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	project, err := r.editorial.UpdateProductionProject(ctx.UserContext(), actorID, ctx.Params("id"), entity.BookProductionProjectPatch{
		WorkflowStatus: body.WorkflowStatus,
		RequiresReview: body.RequiresReview,
		RequiresAudio:  body.RequiresAudio,
		Priority:       body.Priority,
		OwnerID:        body.OwnerID,
		Notes:          body.Notes,
	}, expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialUpdateProductionProject")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, project, project.UpdatedAt)
}

// @Summary     Get production completeness
// @Description Check whether all required translation, summary, metadata, and optional audio drafts are complete enough to publish. Requires editor or admin role.
// @ID          editorial-production-completeness
// @Tags        editorial-production
// @Produce     json
// @Param       id  path     string true "Production project ID"
// @Success     200 {object} entity.BookProductionCompleteness
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/completeness [get]
func (r *V1) editorialProductionCompleteness(ctx *fiber.Ctx) error {
	completeness, err := r.editorial.ProductionCompleteness(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialProductionCompleteness")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(completeness)
}

// @Summary     Get metadata translation draft
// @Description Get the book metadata translation draft for a production project. Requires editor or admin role.
// @ID          editorial-get-metadata-translation-draft
// @Tags        editorial-production
// @Produce     json
// @Param       id  path     string true "Production project ID"
// @Success     200 {object} entity.BookMetadataTranslationEdit
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/metadata-draft [get]
func (r *V1) editorialGetMetadataTranslationDraft(ctx *fiber.Ctx) error {
	draft, err := r.editorial.GetMetadataTranslationDraft(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Save metadata translation draft
// @Description Create or replace the book metadata translation draft. Requires editor or admin role.
// @ID          editorial-save-metadata-translation-draft
// @Tags        editorial-production
// @Accept      json
// @Produce     json
// @Param       id      path string                               true "Production project ID"
// @Param       request body request.SaveMetadataTranslationDraft true "Metadata translation draft"
// @Success     200     {object} entity.BookMetadataTranslationEdit
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/metadata-draft [put]
func (r *V1) editorialSaveMetadataTranslationDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.SaveMetadataTranslationDraft
	if err := parseAndValidateBody(ctx, r, &body); err != nil {
		return err
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	draft, err := r.editorial.SaveMetadataTranslationDraft(ctx.UserContext(), actorID, ctx.Params("id"), entity.BookMetadataTranslationEdit{
		DisplayTitle: body.DisplayTitle,
		Bibliography: body.Bibliography,
		Hint:         body.Hint,
		Description:  body.Description,
		Source:       body.Source,
		Metadata:     entity.RawJSON(body.Metadata),
	}, expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSaveMetadataTranslationDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Delete metadata translation draft
// @Description Delete the book metadata translation draft. Requires editor or admin role.
// @ID          editorial-delete-metadata-translation-draft
// @Tags        editorial-production
// @Param       id path string true "Production project ID"
// @Success     204
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/metadata-draft [delete]
func (r *V1) editorialDeleteMetadataTranslationDraft(ctx *fiber.Ctx) error {
	return r.deleteProjectDraft(
		ctx,
		r.editorial.DeleteMetadataTranslationDraft,
	)
}

// @Summary     Get author translation draft
// @Description Get the author translation draft for a production project. Requires editor or admin role.
// @ID          editorial-get-author-translation-draft
// @Tags        editorial-production
// @Produce     json
// @Param       id  path     string true "Production project ID"
// @Success     200 {object} entity.AuthorTranslationEdit
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/author-draft [get]
func (r *V1) editorialGetAuthorTranslationDraft(ctx *fiber.Ctx) error {
	draft, err := r.editorial.GetAuthorTranslationDraft(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Save author translation draft
// @Description Create or replace the author translation draft. Requires editor or admin role.
// @ID          editorial-save-author-translation-draft
// @Tags        editorial-production
// @Accept      json
// @Produce     json
// @Param       id      path string                             true "Production project ID"
// @Param       request body request.SaveAuthorTranslationDraft true "Author translation draft"
// @Success     200     {object} entity.AuthorTranslationEdit
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/author-draft [put]
func (r *V1) editorialSaveAuthorTranslationDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.SaveAuthorTranslationDraft
	if err := parseAndValidateBody(ctx, r, &body); err != nil {
		return err
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	draft, err := r.editorial.SaveAuthorTranslationDraft(ctx.UserContext(), actorID, ctx.Params("id"), entity.AuthorTranslationEdit{
		Name:      body.Name,
		Biography: body.Biography,
		DeathText: body.DeathText,
		Source:    body.Source,
		Metadata:  entity.RawJSON(body.Metadata),
	}, expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSaveAuthorTranslationDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Delete author translation draft
// @Description Delete the author translation draft. Requires editor or admin role.
// @ID          editorial-delete-author-translation-draft
// @Tags        editorial-production
// @Param       id path string true "Production project ID"
// @Success     204
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/author-draft [delete]
func (r *V1) editorialDeleteAuthorTranslationDraft(ctx *fiber.Ctx) error {
	return r.deleteProjectDraft(
		ctx,
		r.editorial.DeleteAuthorTranslationDraft,
	)
}

// @Summary     Get category translation draft
// @Description Get the category translation draft for a production project. Requires editor or admin role.
// @ID          editorial-get-category-translation-draft
// @Tags        editorial-production
// @Produce     json
// @Param       id  path     string true "Production project ID"
// @Success     200 {object} entity.CategoryTranslationEdit
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/category-draft [get]
func (r *V1) editorialGetCategoryTranslationDraft(ctx *fiber.Ctx) error {
	draft, err := r.editorial.GetCategoryTranslationDraft(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Save category translation draft
// @Description Create or replace the category translation draft. Requires editor or admin role.
// @ID          editorial-save-category-translation-draft
// @Tags        editorial-production
// @Accept      json
// @Produce     json
// @Param       id      path string                               true "Production project ID"
// @Param       request body request.SaveCategoryTranslationDraft true "Category translation draft"
// @Success     200     {object} entity.CategoryTranslationEdit
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/category-draft [put]
func (r *V1) editorialSaveCategoryTranslationDraft(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.SaveCategoryTranslationDraft
	if err := parseAndValidateBody(ctx, r, &body); err != nil {
		return err
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	draft, err := r.editorial.SaveCategoryTranslationDraft(ctx.UserContext(), actorID, ctx.Params("id"), entity.CategoryTranslationEdit{
		Name:     body.Name,
		Source:   body.Source,
		Metadata: entity.RawJSON(body.Metadata),
	}, expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSaveCategoryTranslationDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Delete category translation draft
// @Description Delete the category translation draft. Requires editor or admin role.
// @ID          editorial-delete-category-translation-draft
// @Tags        editorial-production
// @Param       id path string true "Production project ID"
// @Success     204
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/category-draft [delete]
func (r *V1) editorialDeleteCategoryTranslationDraft(ctx *fiber.Ctx) error {
	return r.deleteProjectDraft(
		ctx,
		r.editorial.DeleteCategoryTranslationDraft,
	)
}

// @Summary     Get section translation draft
// @Description Get a TOC-scoped section translation draft. Requires editor or admin role.
// @ID          editorial-get-section-translation-draft
// @Tags        editorial-production
// @Produce     json
// @Param       id         path     string true "Production project ID"
// @Param       heading_id path     int    true "Heading ID"
// @Success     200        {object} entity.SectionTranslationEdit
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/toc/{heading_id}/translation-draft [get]
func (r *V1) editorialGetSectionTranslationDraft(ctx *fiber.Ctx) error {
	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	draft, err := r.editorial.GetSectionTranslationDraft(ctx.UserContext(), ctx.Params("id"), headingID)
	if err != nil {
		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Save section translation draft
// @Description Create or replace a TOC-scoped section translation draft. Requires editor or admin role.
// @ID          editorial-save-section-translation-draft
// @Tags        editorial-production
// @Accept      json
// @Produce     json
// @Param       id         path string                              true "Production project ID"
// @Param       heading_id path int                                 true "Heading ID"
// @Param       request    body request.SaveSectionTranslationDraft true "Section translation draft"
// @Success     200        {object} entity.SectionTranslationEdit
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/toc/{heading_id}/translation-draft [put]
func (r *V1) editorialSaveSectionTranslationDraft(ctx *fiber.Ctx) error {
	actorID, headingID, ok := r.productionDraftContext(ctx)
	if !ok {
		return nil
	}

	var body request.SaveSectionTranslationDraft
	if err := parseAndValidateBody(ctx, r, &body); err != nil {
		return err
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	draft, err := r.editorial.SaveSectionTranslationDraft(ctx.UserContext(), actorID, ctx.Params("id"), entity.SectionTranslationEdit{
		HeadingID: headingID,
		Title:     body.Title,
		Content:   body.Content,
		Source:    body.Source,
		Metadata:  entity.RawJSON(body.Metadata),
	}, expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSaveSectionTranslationDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Delete section translation draft
// @Description Delete a TOC-scoped section translation draft. Requires editor or admin role.
// @ID          editorial-delete-section-translation-draft
// @Tags        editorial-production
// @Param       id         path string true "Production project ID"
// @Param       heading_id path int    true "Heading ID"
// @Success     204
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/toc/{heading_id}/translation-draft [delete]
func (r *V1) editorialDeleteSectionTranslationDraft(ctx *fiber.Ctx) error {
	return r.deleteHeadingDraft(
		ctx,
		r.editorial.DeleteSectionTranslationDraft,
	)
}

// @Summary     Get heading summary draft
// @Description Get a TOC-scoped summary draft. Requires editor or admin role.
// @ID          editorial-get-heading-summary-draft
// @Tags        editorial-production
// @Produce     json
// @Param       id         path     string true "Production project ID"
// @Param       heading_id path     int    true "Heading ID"
// @Success     200        {object} entity.HeadingSummaryEdit
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/toc/{heading_id}/summary-draft [get]
func (r *V1) editorialGetHeadingSummaryDraft(ctx *fiber.Ctx) error {
	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	draft, err := r.editorial.GetHeadingSummaryDraft(ctx.UserContext(), ctx.Params("id"), headingID)
	if err != nil {
		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Save heading summary draft
// @Description Create or replace a TOC-scoped summary draft. Requires editor or admin role.
// @ID          editorial-save-heading-summary-draft
// @Tags        editorial-production
// @Accept      json
// @Produce     json
// @Param       id         path string                          true "Production project ID"
// @Param       heading_id path int                             true "Heading ID"
// @Param       request    body request.SaveHeadingSummaryDraft true "Heading summary draft"
// @Success     200        {object} entity.HeadingSummaryEdit
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/toc/{heading_id}/summary-draft [put]
func (r *V1) editorialSaveHeadingSummaryDraft(ctx *fiber.Ctx) error {
	actorID, headingID, ok := r.productionDraftContext(ctx)
	if !ok {
		return nil
	}

	var body request.SaveHeadingSummaryDraft
	if err := parseAndValidateBody(ctx, r, &body); err != nil {
		return err
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	draft, err := r.editorial.SaveHeadingSummaryDraft(ctx.UserContext(), actorID, ctx.Params("id"), entity.HeadingSummaryEdit{
		HeadingID: headingID,
		Summary:   body.Summary,
		Source:    body.Source,
		Metadata:  entity.RawJSON(body.Metadata),
	}, expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSaveHeadingSummaryDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Delete heading summary draft
// @Description Delete a TOC-scoped summary draft. Requires editor or admin role.
// @ID          editorial-delete-heading-summary-draft
// @Tags        editorial-production
// @Param       id         path string true "Production project ID"
// @Param       heading_id path int    true "Heading ID"
// @Success     204
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/toc/{heading_id}/summary-draft [delete]
func (r *V1) editorialDeleteHeadingSummaryDraft(ctx *fiber.Ctx) error {
	return r.deleteHeadingDraft(
		ctx,
		r.editorial.DeleteHeadingSummaryDraft,
	)
}

// @Summary     Get section audio draft
// @Description Get a TOC-scoped audio draft. Requires editor or admin role.
// @ID          editorial-get-section-audio-draft
// @Tags        editorial-production
// @Produce     json
// @Param       id         path     string true "Production project ID"
// @Param       heading_id path     int    true "Heading ID"
// @Success     200        {object} entity.SectionAudioEdit
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/toc/{heading_id}/audio-draft [get]
func (r *V1) editorialGetSectionAudioDraft(ctx *fiber.Ctx) error {
	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	draft, err := r.editorial.GetSectionAudioDraft(ctx.UserContext(), ctx.Params("id"), headingID)
	if err != nil {
		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Save section audio draft
// @Description Create or replace a TOC-scoped audio draft. Requires editor or admin role.
// @ID          editorial-save-section-audio-draft
// @Tags        editorial-production
// @Accept      json
// @Produce     json
// @Param       id         path string                        true "Production project ID"
// @Param       heading_id path int                           true "Heading ID"
// @Param       request    body request.SaveSectionAudioDraft true "Section audio draft"
// @Success     200        {object} entity.SectionAudioEdit
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/toc/{heading_id}/audio-draft [put]
func (r *V1) editorialSaveSectionAudioDraft(ctx *fiber.Ctx) error {
	actorID, headingID, ok := r.productionDraftContext(ctx)
	if !ok {
		return nil
	}

	var body request.SaveSectionAudioDraft
	if err := parseAndValidateBody(ctx, r, &body); err != nil {
		return err
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	draft, err := r.editorial.SaveSectionAudioDraft(ctx.UserContext(), actorID, ctx.Params("id"), entity.SectionAudioEdit{
		HeadingID:       headingID,
		URL:             body.URL,
		Narrator:        body.Narrator,
		DurationSeconds: body.DurationSeconds,
		MIMEType:        body.MIMEType,
		Metadata:        entity.RawJSON(body.Metadata),
	}, expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSaveSectionAudioDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, draft, draft.UpdatedAt)
}

// @Summary     Delete section audio draft
// @Description Delete a TOC-scoped audio draft. Requires editor or admin role.
// @ID          editorial-delete-section-audio-draft
// @Tags        editorial-production
// @Param       id         path string true "Production project ID"
// @Param       heading_id path int    true "Heading ID"
// @Success     204
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/toc/{heading_id}/audio-draft [delete]
func (r *V1) editorialDeleteSectionAudioDraft(ctx *fiber.Ctx) error {
	return r.deleteHeadingDraft(
		ctx,
		r.editorial.DeleteSectionAudioDraft,
	)
}

// @Summary     Review production asset
// @Description Submit, approve, or reject one production draft asset. Requires editor or admin role.
// @ID          editorial-review-production-asset
// @Tags        editorial-production
// @Accept      json
// @Produce     json
// @Param       id      path string                        true "Production project ID"
// @Param       request body request.ReviewProductionAsset true "Review decision"
// @Success     204
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/review [post]
func (r *V1) editorialReviewProductionAsset(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.ReviewProductionAsset
	if err := parseAndValidateBody(ctx, r, &body); err != nil {
		return err
	}

	err := r.editorial.ReviewProductionAsset(
		ctx.UserContext(),
		actorID,
		ctx.Params("id"),
		body.AssetType,
		body.HeadingID,
		body.Decision,
		body.Note,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialReviewProductionAsset")

		return r.editorialError(ctx, err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// @Summary     Publish production project
// @Description Publish a complete book+language production project into final reader assets. Admin role required. A blocked publish, including a non-permitted book license, returns the rich ProductionPublishBlocked contract.
// @ID          editorial-publish-production-project
// @Tags        editorial-production
// @Produce     json
// @Param       id  path     string true "Production project ID"
// @Success     200 {object} entity.BookProductionProject
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     409 {object} response.ProductionPublishBlocked
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/publish [post]
func (r *V1) editorialPublishProductionProject(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	project, err := r.editorial.PublishProductionProject(ctx.UserContext(), actorID, ctx.Params("id"), expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialPublishProductionProject")

		if errors.Is(err, entity.ErrProductionNotReady) || errors.Is(err, entity.ErrLicenseNotPermitted) {
			check, checkErr := r.editorial.ProductionPublishCheck(ctx.UserContext(), ctx.Params("id"))
			if checkErr == nil {
				message := "production project is not ready"
				if errors.Is(err, entity.ErrLicenseNotPermitted) {
					message = "license not permitted"
				}

				blocked := response.ProductionPublishBlockedFromCheck(message, check)
				blocked.RequestID = requestID(ctx)

				return ctx.Status(http.StatusConflict).JSON(blocked)
			}

			r.logEditorialError(ctx, checkErr, "restapi - v1 - editorialPublishProductionProject - publishCheck")
		}

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, project, project.UpdatedAt)
}

// @Summary     Unpublish production project
// @Description Hide a book+language publication without deleting final assets. Admin role required.
// @ID          editorial-unpublish-production-project
// @Tags        editorial-production
// @Produce     json
// @Param       id  path     string true "Production project ID"
// @Success     200 {object} entity.BookProductionProject
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/unpublish [post]
func (r *V1) editorialUnpublishProductionProject(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	project, err := r.editorial.UnpublishProductionProject(ctx.UserContext(), actorID, ctx.Params("id"), expected)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialUnpublishProductionProject")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, project, project.UpdatedAt)
}

// @Summary     Delete final project asset
// @Description Soft-delete a project-scoped final asset and hide the publication. Admin role required.
// @ID          editorial-delete-final-production-asset
// @Tags        editorial-production
// @Accept      json
// @Param       id         path string                             true  "Production project ID"
// @Param       asset_type path string                             true  "Asset type" Enums(book_metadata,author_metadata,category_metadata)
// @Param       request    body request.DeleteFinalProductionAsset false "Delete reason"
// @Success     204
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/final-assets/{asset_type} [delete]
func (r *V1) editorialDeleteFinalProductionAsset(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.DeleteFinalProductionAsset
	if len(ctx.Body()) > 0 {
		if err := parseAndValidateBody(ctx, r, &body); err != nil {
			return err
		}
	}

	err := r.editorial.DeleteFinalProductionAsset(
		ctx.UserContext(),
		actorID,
		ctx.Params("id"),
		ctx.Params("asset_type"),
		nil,
		body.Reason,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialDeleteFinalProductionAsset")

		return r.editorialError(ctx, err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// @Summary     Delete final heading asset
// @Description Soft-delete a TOC-scoped final asset and hide the publication. Admin role required.
// @ID          editorial-delete-final-heading-production-asset
// @Tags        editorial-production
// @Accept      json
// @Param       id         path string                             true  "Production project ID"
// @Param       heading_id path int                                true  "Heading ID"
// @Param       asset_type path string                             true  "Asset type" Enums(section_translation,heading_summary,section_audio)
// @Param       request    body request.DeleteFinalProductionAsset false "Delete reason"
// @Success     204
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     403        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/production-projects/{id}/toc/{heading_id}/final-assets/{asset_type} [delete]
func (r *V1) editorialDeleteFinalHeadingProductionAsset(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	var body request.DeleteFinalProductionAsset
	if len(ctx.Body()) > 0 {
		if err := parseAndValidateBody(ctx, r, &body); err != nil {
			return err
		}
	}

	err = r.editorial.DeleteFinalProductionAsset(
		ctx.UserContext(),
		actorID,
		ctx.Params("id"),
		ctx.Params("asset_type"),
		&headingID,
		body.Reason,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialDeleteFinalHeadingProductionAsset")

		return r.editorialError(ctx, err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

func parseAndValidateBody(ctx *fiber.Ctx, r *V1, body any) error {
	if err := ctx.BodyParser(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}
	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	return nil
}

func (r *V1) productionDraftContext(ctx *fiber.Ctx) (string, int, bool) {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		_ = errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
		return "", 0, false
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		_ = errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
		return "", 0, false
	}

	return actorID, headingID, true
}

func (r *V1) deleteProjectDraft(
	ctx *fiber.Ctx,
	deleteDraft func(context.Context, string, string, *time.Time) error,
) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	if err := deleteDraft(ctx.UserContext(), actorID, ctx.Params("id"), expected); err != nil {
		return r.editorialError(ctx, err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

func (r *V1) deleteHeadingDraft(
	ctx *fiber.Ctx,
	deleteDraft func(context.Context, string, string, int, *time.Time) error,
) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	expected, _, okMatch := parseIfMatch(ctx)
	if !okMatch {
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	}

	if err = deleteDraft(ctx.UserContext(), actorID, ctx.Params("id"), headingID, expected); err != nil {
		return r.editorialError(ctx, err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}
