package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/policy"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEditorialProductionPermissionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		actor          entity.User
		expectedStatus int
	}{
		{
			name:           "user forbidden from production candidates",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-candidates?lang=id",
			actor:          entity.User{ID: "user-id", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "user forbidden from production dashboard",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-dashboard?lang=id",
			actor:          entity.User{ID: "user-id", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "user forbidden from global activity",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-activity",
			actor:          entity.User{ID: "user-id", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "user forbidden from editorial project list",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-projects",
			actor:          entity.User{ID: "user-id", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "user forbidden from project activity",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-projects/project-id/activity",
			actor:          entity.User{ID: "user-id", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "user forbidden from publish check",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-projects/project-id/publish-check",
			actor:          entity.User{ID: "user-id", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "user forbidden from draft revisions",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-projects/project-id/draft-revisions?asset_type=book_metadata",
			actor:          entity.User{ID: "user-id", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "user forbidden from draft revision restore",
			method:         http.MethodPost,
			path:           "/v1/editorial/production-projects/project-id/draft-revisions/revision-id/restore",
			actor:          entity.User{ID: "user-id", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "user forbidden from source metadata draft",
			method:         http.MethodGet,
			path:           "/v1/editorial/books/797/metadata-draft",
			actor:          entity.User{ID: "user-id", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "user forbidden from source heading draft",
			method:         http.MethodGet,
			path:           "/v1/editorial/books/797/headings/10/draft",
			actor:          entity.User{ID: "user-id", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "editor allowed project list",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-projects",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor allowed production candidates",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-candidates?lang=id",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor allowed production dashboard",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-dashboard?lang=id",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor allowed global activity",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-activity",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor allowed project workspace",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-projects/project-id/workspace",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor allowed project activity",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-projects/project-id/activity",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor allowed publish check",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-projects/project-id/publish-check",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor allowed draft revisions",
			method:         http.MethodGet,
			path:           "/v1/editorial/production-projects/project-id/draft-revisions?asset_type=book_metadata",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor allowed draft revision restore",
			method:         http.MethodPost,
			path:           "/v1/editorial/production-projects/project-id/draft-revisions/revision-id/restore",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor allowed source metadata draft",
			method:         http.MethodGet,
			path:           "/v1/editorial/books/797/metadata-draft",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor allowed source heading draft",
			method:         http.MethodGet,
			path:           "/v1/editorial/books/797/headings/10/draft",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "editor forbidden publish",
			method:         http.MethodPost,
			path:           "/v1/editorial/production-projects/project-id/publish",
			actor:          entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "admin allowed publish",
			method:         http.MethodPost,
			path:           "/v1/editorial/production-projects/project-id/publish",
			actor:          entity.User{ID: "admin-id", Role: entity.UserRoleAdmin},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newProductionPermissionTestApp(tt.actor)
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
		})
	}
}

func TestEditorialCreateProductionProjectRejectsBadLang(t *testing.T) {
	t.Parallel()

	app := newProductionPermissionTestApp(entity.User{ID: "editor-id", Role: entity.UserRoleEditor})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/editorial/production-projects",
		strings.NewReader(`{"book_id":797,"lang":"ar"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestEditorialCreateProductionProjectConflictIncludesExistingProjectID(t *testing.T) {
	t.Parallel()

	existingProjectID := "existing-project-id"
	app := newProductionTestApp(
		entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
		&fakeProductionEditorial{createProductionProjectErr: entity.NewProductionProjectExistsError(existingProjectID)},
	)
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/editorial/production-projects",
		strings.NewReader(`{"book_id":797,"lang":"id"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var body struct {
		Error             string `json:"error"`
		Code              string `json:"code"`
		RequestID         string `json:"request_id"`
		ExistingProjectID string `json:"existing_project_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "production project already exists", body.Error)
	// F1-D: the rich envelope now carries the machine code too.
	assert.Equal(t, "production_project_already_exists", body.Code)
	assert.Equal(t, existingProjectID, body.ExistingProjectID)
}

func TestEditorialPublishProductionProjectBlockedIncludesBlockers(t *testing.T) {
	t.Parallel()

	app := newProductionTestApp(
		entity.User{ID: "admin-id", Role: entity.UserRoleAdmin},
		&fakeProductionEditorial{
			publishProductionProjectErr: entity.ErrProductionNotReady,
			publishCheck: entity.BookProductionPublishCheck{
				Project:    entity.BookProductionProject{ID: "project-id", BookID: 797, Lang: "id"},
				Ready:      false,
				CanPublish: false,
				BlockingErrors: []entity.BookProductionBlocking{{
					Code:      "missing_required_asset",
					AssetType: entity.ProductionAssetBookMetadata,
					Message:   "metadata translation draft is missing",
				}},
			},
		},
	)
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/editorial/production-projects/project-id/publish",
		nil,
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var body struct {
		Error          string                              `json:"error"`
		Code           string                              `json:"code"`
		CanPublish     bool                                `json:"can_publish"`
		BlockingErrors []entity.BookProductionBlocking     `json:"blocking_errors"`
		Missing        []entity.BookProductionMissingAsset `json:"missing"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "production project is not ready", body.Error)
	// F1-D: the rich envelope now carries the machine code too.
	assert.Equal(t, "production_project_is_not_ready", body.Code)
	assert.False(t, body.CanPublish)
	require.Len(t, body.BlockingErrors, 1)
	assert.Equal(t, entity.ProductionAssetBookMetadata, body.BlockingErrors[0].AssetType)
}

func TestEditorialProductionProjectResponsesIncludeOwner(t *testing.T) {
	t.Parallel()

	ownerID := "owner-id"
	displayName := "Editor Name"
	project := entity.BookProductionProject{
		ID:      "project-id",
		BookID:  797,
		Lang:    "id",
		OwnerID: &ownerID,
		Owner: &entity.ProductionProjectOwner{
			ID:          ownerID,
			Email:       "editor@example.com",
			DisplayName: &displayName,
		},
	}
	app := newProductionTestApp(
		entity.User{ID: "editor-id", Role: entity.UserRoleEditor},
		&fakeProductionEditorial{productionProject: project},
	)

	listResp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/editorial/production-projects",
		nil,
	))
	require.NoError(t, err)
	defer listResp.Body.Close()

	var listBody struct {
		Projects []entity.BookProductionProject `json:"projects"`
	}
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&listBody))
	require.Len(t, listBody.Projects, 1)
	assertProductionOwner(t, listBody.Projects[0], ownerID, displayName)

	detailResp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/editorial/production-projects/project-id",
		nil,
	))
	require.NoError(t, err)
	defer detailResp.Body.Close()

	var detailBody entity.BookProductionProject
	require.NoError(t, json.NewDecoder(detailResp.Body).Decode(&detailBody))
	assertProductionOwner(t, detailBody, ownerID, displayName)

	workspaceResp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/editorial/production-projects/project-id/workspace",
		nil,
	))
	require.NoError(t, err)
	defer workspaceResp.Body.Close()

	var workspaceBody entity.BookProductionWorkspace
	require.NoError(t, json.NewDecoder(workspaceResp.Body).Decode(&workspaceBody))
	assertProductionOwner(t, workspaceBody.Project, ownerID, displayName)
}

func TestEditorialMetadataTranslationDraftETagPrecondition(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	editorial := &fakeProductionEditorial{
		metadataTranslationDraft: entity.BookMetadataTranslationEdit{
			ProjectID:    "project-id",
			DisplayTitle: "Old title",
			UpdatedAt:    updatedAt,
		},
	}
	app := newProductionTestApp(entity.User{ID: "editor-id", Role: entity.UserRoleEditor}, editorial)

	getResp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/editorial/production-projects/project-id/metadata-draft",
		nil,
	))
	require.NoError(t, err)

	defer getResp.Body.Close()

	require.Equal(t, http.StatusOK, getResp.StatusCode)
	assert.Equal(t, updatedAtETag(updatedAt), getResp.Header.Get("ETag"))

	staleReq := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/v1/editorial/production-projects/project-id/metadata-draft",
		strings.NewReader(`{"display_title":"New title"}`),
	)
	staleReq.Header.Set("Content-Type", "application/json")
	staleReq.Header.Set("If-Match", updatedAtETag(updatedAt.Add(-time.Second)))

	staleResp, err := app.Test(staleReq)
	require.NoError(t, err)

	defer staleResp.Body.Close()

	assert.Equal(t, http.StatusPreconditionFailed, staleResp.StatusCode)
	assert.Zero(t, editorial.saveMetadataTranslationDraftCalls)

	matchReq := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/v1/editorial/production-projects/project-id/metadata-draft",
		strings.NewReader(`{"display_title":"New title"}`),
	)
	matchReq.Header.Set("Content-Type", "application/json")
	matchReq.Header.Set("If-Match", updatedAtETag(updatedAt))

	matchResp, err := app.Test(matchReq)
	require.NoError(t, err)

	defer matchResp.Body.Close()

	assert.Equal(t, http.StatusOK, matchResp.StatusCode)
	assert.Equal(t, 1, editorial.saveMetadataTranslationDraftCalls)
}

func TestEditorialSaveSourceMetadataDraftIncludesBibliographyAndHint(t *testing.T) {
	t.Parallel()

	editorial := &fakeProductionEditorial{}
	app := newProductionTestApp(entity.User{ID: "editor-id", Role: entity.UserRoleEditor}, editorial)
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/v1/editorial/books/797/metadata-draft",
		strings.NewReader(`{
			"display_title":"New title",
			"bibliography":"New bibliography",
			"hint":"New hint",
			"description":"New description"
		}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, editorial.saveMetadataDraftCalls)
	assert.Equal(t, 797, editorial.savedMetadataDraft.BookID)
	assert.Equal(t, new("New title"), editorial.savedMetadataDraft.DisplayTitle)
	assert.Equal(t, new("New bibliography"), editorial.savedMetadataDraft.Bibliography)
	assert.Equal(t, new("New hint"), editorial.savedMetadataDraft.Hint)
	assert.Equal(t, new("New description"), editorial.savedMetadataDraft.Description)
}

func TestEditorialPublishProductionProjectRejectsStaleIfMatch(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	editorial := &fakeProductionEditorial{
		productionProject: entity.BookProductionProject{
			ID:        "project-id",
			BookID:    797,
			Lang:      "id",
			UpdatedAt: updatedAt,
		},
	}
	app := newProductionTestApp(entity.User{ID: "admin-id", Role: entity.UserRoleAdmin}, editorial)
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/editorial/production-projects/project-id/publish",
		nil,
	)
	req.Header.Set("If-Match", updatedAtETag(updatedAt.Add(-time.Second)))

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)
	assert.Zero(t, editorial.publishProductionProjectCalls)
}

func assertProductionOwner(
	t *testing.T,
	project entity.BookProductionProject,
	ownerID,
	displayName string,
) {
	t.Helper()

	require.NotNil(t, project.OwnerID)
	assert.Equal(t, ownerID, *project.OwnerID)
	require.NotNil(t, project.Owner)
	assert.Equal(t, ownerID, project.Owner.ID)
	assert.Equal(t, "editor@example.com", project.Owner.Email)
	require.NotNil(t, project.Owner.DisplayName)
	assert.Equal(t, displayName, *project.Owner.DisplayName)
}

func newProductionPermissionTestApp(actor entity.User) *fiber.App {
	return newProductionTestApp(actor, &fakeProductionEditorial{})
}

func newProductionTestApp(actor entity.User, editorial *fakeProductionEditorial) *fiber.App {
	app := fiber.New()
	user := &fakeAuthUser{}
	controller := &V1{
		u:         user,
		editorial: editorial,
		l:         logger.New("error"),
		v:         validator.New(validator.WithRequiredStructEnabled()),
	}
	injectActor := func(ctx *fiber.Ctx) error {
		ctx.Locals("user", actor)
		ctx.Locals("userID", actor.ID)

		return ctx.Next()
	}

	editorialReview := app.Group(
		"/v1/editorial",
		injectActor,
		middleware.RequireCapability(user, policy.CapReviewEditorial),
	)
	editorialReview.Get("/production-candidates", controller.editorialListProductionCandidates)
	editorialReview.Get("/production-dashboard", controller.editorialProductionDashboard)
	editorialReview.Get("/production-activity", controller.editorialGlobalProductionActivity)
	editorialReview.Get("/production-projects", controller.editorialListProductionProjects)
	editorialReview.Post("/production-projects", controller.editorialCreateProductionProject)
	editorialReview.Get("/production-projects/:id/workspace", controller.editorialProductionWorkspace)
	editorialReview.Get("/production-projects/:id/activity", controller.editorialProductionActivity)
	editorialReview.Get("/production-projects/:id/publish-check", controller.editorialProductionPublishCheck)
	editorialReview.Get("/production-projects/:id/draft-revisions", controller.editorialListProductionDraftRevisions)
	editorialReview.Post("/production-projects/:id/draft-revisions/:revision_id/restore", controller.editorialRestoreProductionDraftRevision)
	editorialReview.Get("/production-projects/:id", controller.editorialGetProductionProject)
	editorialReview.Patch("/production-projects/:id", controller.editorialUpdateProductionProject)
	editorialReview.Get("/production-projects/:id/metadata-draft", controller.editorialGetMetadataTranslationDraft)
	editorialReview.Put("/production-projects/:id/metadata-draft", controller.editorialSaveMetadataTranslationDraft)
	editorialReview.Get("/books/:book_id/metadata-draft", controller.editorialGetMetadataDraft)
	editorialReview.Put("/books/:book_id/metadata-draft", controller.editorialSaveMetadataDraft)
	editorialReview.Get("/books/:book_id/headings/:heading_id/draft", controller.editorialGetHeadingDraft)

	editorialAdmin := app.Group(
		"/v1/editorial",
		injectActor,
		middleware.RequireCapability(user, policy.CapPublishProduction),
	)
	editorialAdmin.Post("/production-projects/:id/publish", controller.editorialPublishProductionProject)

	return app
}

type fakeProductionEditorial struct {
	usecase.Editorial
	createProductionProjectErr  error
	publishProductionProjectErr error
	publishCheck                entity.BookProductionPublishCheck
	productionProject           entity.BookProductionProject
	metadataTranslationDraft    entity.BookMetadataTranslationEdit
	sourceMetadataDraft         entity.BookMetadataEdit
	savedMetadataDraft          entity.BookMetadataEdit
	savedMetadataDraftExpected  *time.Time

	publishProductionProjectCalls     int
	saveMetadataTranslationDraftCalls int
	saveMetadataDraftCalls            int
}

func (f *fakeProductionEditorial) projectResponse() entity.BookProductionProject {
	if f.productionProject.ID != "" {
		return f.productionProject
	}

	return entity.BookProductionProject{ID: "project-id", BookID: 797, Lang: "id"}
}

func (f *fakeProductionEditorial) ProductionProjects(
	context.Context,
	*int,
	string,
	string,
	string,
	bool,
	bool,
	int,
	int,
) ([]entity.BookProductionProject, int, error) {
	return []entity.BookProductionProject{f.projectResponse()}, 1, nil
}

func (f *fakeProductionEditorial) ProductionCandidates(
	context.Context,
	string,
	string,
	*int,
	*int,
	*bool,
	bool,
	int,
	int,
) ([]entity.BookProductionCandidate, int, error) {
	return []entity.BookProductionCandidate{{BookID: 797, Name: "book", HasContent: true}}, 1, nil
}

func (f *fakeProductionEditorial) ProductionDashboard(
	context.Context,
	string,
	int,
) (entity.BookProductionDashboard, error) {
	return entity.BookProductionDashboard{
		Lang:                "id",
		CandidateCount:      5,
		ActiveProjectCount:  2,
		NeedsWorkCount:      1,
		ReadyToPublishCount: 1,
		PublishedCount:      3,
	}, nil
}

func (f *fakeProductionEditorial) GlobalProductionActivity(
	context.Context,
	string,
	int,
	int,
) ([]entity.BookProductionEvent, int, error) {
	return []entity.BookProductionEvent{{
		ID:        "event-id",
		ProjectID: "project-id",
		EventType: entity.ProductionEventDraftSave,
	}}, 1, nil
}

func (f *fakeProductionEditorial) CreateProductionProject(
	context.Context,
	string,
	entity.BookProductionProject,
) (entity.BookProductionProject, error) {
	if f.createProductionProjectErr != nil {
		return entity.BookProductionProject{}, f.createProductionProjectErr
	}

	return f.projectResponse(), nil
}

func (f *fakeProductionEditorial) ProductionProject(
	context.Context,
	string,
) (entity.BookProductionProject, error) {
	return f.projectResponse(), nil
}

func (f *fakeProductionEditorial) ProductionWorkspace(
	context.Context,
	string,
) (entity.BookProductionWorkspace, error) {
	return entity.BookProductionWorkspace{
		Project: f.projectResponse(),
		Book:    entity.BookProductionWorkspaceBook{ID: 797, Name: "book", HasContent: true},
	}, nil
}

func (f *fakeProductionEditorial) ProductionActivity(
	context.Context,
	string,
	int,
	int,
) ([]entity.BookProductionEvent, int, error) {
	return []entity.BookProductionEvent{{
		ID:        "event-id",
		ProjectID: "project-id",
		EventType: entity.ProductionEventDraftSave,
	}}, 1, nil
}

func (f *fakeProductionEditorial) ProductionPublishCheck(
	context.Context,
	string,
) (entity.BookProductionPublishCheck, error) {
	if f.publishCheck.Project.ID != "" || len(f.publishCheck.BlockingErrors) > 0 {
		return f.publishCheck, nil
	}

	return entity.BookProductionPublishCheck{
		Project:    f.projectResponse(),
		Ready:      true,
		CanPublish: true,
	}, nil
}

func (f *fakeProductionEditorial) ProductionDraftRevisions(
	context.Context,
	string,
	string,
	*int,
	int,
	int,
) ([]entity.BookProductionDraftRevision, int, error) {
	return []entity.BookProductionDraftRevision{{
		ID:        "revision-id",
		ProjectID: "project-id",
		AssetType: entity.ProductionAssetBookMetadata,
		Version:   1,
	}}, 1, nil
}

func (f *fakeProductionEditorial) RestoreProductionDraftRevision(
	context.Context,
	string,
	string,
	string,
) (entity.BookProductionDraftRevision, error) {
	return entity.BookProductionDraftRevision{
		ID:        "revision-id-2",
		ProjectID: "project-id",
		AssetType: entity.ProductionAssetBookMetadata,
		Version:   2,
	}, nil
}

func (f *fakeProductionEditorial) PublishProductionProject(
	_ context.Context,
	_ string,
	_ string,
	expected *time.Time,
) (entity.BookProductionProject, error) {
	// Mirror the repo behavior: a non-nil expected must match the project row.
	if expected != nil && !expected.Equal(f.projectResponse().UpdatedAt) {
		return entity.BookProductionProject{}, entity.ErrPreconditionFailed
	}

	f.publishProductionProjectCalls++
	if f.publishProductionProjectErr != nil {
		return entity.BookProductionProject{}, f.publishProductionProjectErr
	}

	return entity.BookProductionProject{
		ID:                "project-id",
		WorkflowStatus:    entity.ProductionWorkflowPublished,
		PublicationStatus: entity.ProductionPublicationPublished,
	}, nil
}

func (f *fakeProductionEditorial) GetMetadataTranslationDraft(
	context.Context,
	string,
) (entity.BookMetadataTranslationEdit, error) {
	if f.metadataTranslationDraft.ProjectID != "" {
		return f.metadataTranslationDraft, nil
	}

	return entity.BookMetadataTranslationEdit{ProjectID: "project-id", DisplayTitle: "Title"}, nil
}

//nolint:gocritic // value param mirrors the usecase.Editorial interface
func (f *fakeProductionEditorial) SaveMetadataTranslationDraft(
	_ context.Context,
	_ string,
	_ string,
	_ entity.BookMetadataTranslationEdit,
	expected *time.Time,
) (entity.BookMetadataTranslationEdit, error) {
	// Mirror the repo behavior: a non-nil expected must match the draft row.
	if expected != nil && !expected.Equal(f.metadataTranslationDraft.UpdatedAt) {
		return entity.BookMetadataTranslationEdit{}, entity.ErrPreconditionFailed
	}

	f.saveMetadataTranslationDraftCalls++

	return entity.BookMetadataTranslationEdit{
		ProjectID:    "project-id",
		DisplayTitle: "New title",
		UpdatedAt:    time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC),
	}, nil
}

func (f *fakeProductionEditorial) GetMetadataDraft(
	context.Context,
	int,
) (entity.BookMetadataEdit, error) {
	if f.sourceMetadataDraft.BookID != 0 {
		return f.sourceMetadataDraft, nil
	}

	return entity.BookMetadataEdit{BookID: 797, Status: entity.EditStatusDraft}, nil
}

func (f *fakeProductionEditorial) SaveMetadataDraft(
	_ context.Context,
	_ string,
	edit entity.BookMetadataEdit,
	expected *time.Time,
	_ string,
) (entity.BookMetadataEdit, error) {
	f.saveMetadataDraftCalls++
	f.savedMetadataDraft = edit
	f.savedMetadataDraftExpected = expected
	edit.Status = entity.EditStatusDraft
	edit.UpdatedAt = time.Date(2026, 1, 2, 3, 4, 7, 0, time.UTC)

	return edit, nil
}

func (f *fakeProductionEditorial) GetHeadingDraft(
	context.Context,
	int,
	int,
) (entity.BookHeadingEdit, error) {
	return entity.BookHeadingEdit{BookID: 797, HeadingID: 10, Status: entity.EditStatusDraft}, nil
}

//go:fix inline
func ptrString(value string) *string {
	return new(value)
}
