package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/evrone/go-clean-template/internal/controller/restapi/middleware"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/evrone/go-clean-template/pkg/logger"
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
		tt := tt
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
		ExistingProjectID string `json:"existing_project_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "production project already exists", body.Error)
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
		CanPublish     bool                                `json:"can_publish"`
		BlockingErrors []entity.BookProductionBlocking     `json:"blocking_errors"`
		Missing        []entity.BookProductionMissingAsset `json:"missing"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "production project is not ready", body.Error)
	assert.False(t, body.CanPublish)
	require.Len(t, body.BlockingErrors, 1)
	assert.Equal(t, entity.ProductionAssetBookMetadata, body.BlockingErrors[0].AssetType)
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
		middleware.RequireRoles(user, entity.UserRoleEditor, entity.UserRoleAdmin),
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
	editorialReview.Get("/books/:book_id/metadata-draft", controller.editorialGetMetadataDraft)
	editorialReview.Get("/books/:book_id/headings/:heading_id/draft", controller.editorialGetHeadingDraft)

	editorialAdmin := app.Group(
		"/v1/editorial",
		injectActor,
		middleware.RequireRoles(user, entity.UserRoleAdmin),
	)
	editorialAdmin.Post("/production-projects/:id/publish", controller.editorialPublishProductionProject)

	return app
}

type fakeProductionEditorial struct {
	usecase.Editorial
	createProductionProjectErr  error
	publishProductionProjectErr error
	publishCheck                entity.BookProductionPublishCheck
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
	return []entity.BookProductionProject{{ID: "project-id", BookID: 797, Lang: "id"}}, 1, nil
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

	return entity.BookProductionProject{ID: "project-id", BookID: 797, Lang: "id"}, nil
}

func (f *fakeProductionEditorial) ProductionWorkspace(
	context.Context,
	string,
) (entity.BookProductionWorkspace, error) {
	return entity.BookProductionWorkspace{
		Project: entity.BookProductionProject{ID: "project-id", BookID: 797, Lang: "id"},
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
		Project:    entity.BookProductionProject{ID: "project-id", BookID: 797, Lang: "id"},
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
	context.Context,
	string,
	string,
) (entity.BookProductionProject, error) {
	if f.publishProductionProjectErr != nil {
		return entity.BookProductionProject{}, f.publishProductionProjectErr
	}

	return entity.BookProductionProject{
		ID:                "project-id",
		WorkflowStatus:    entity.ProductionWorkflowPublished,
		PublicationStatus: entity.ProductionPublicationPublished,
	}, nil
}

func (f *fakeProductionEditorial) GetMetadataDraft(
	context.Context,
	int,
) (entity.BookMetadataEdit, error) {
	return entity.BookMetadataEdit{BookID: 797, Status: entity.EditStatusDraft}, nil
}

func (f *fakeProductionEditorial) GetHeadingDraft(
	context.Context,
	int,
	int,
) (entity.BookHeadingEdit, error) {
	return entity.BookHeadingEdit{BookID: 797, HeadingID: 10, Status: entity.EditStatusDraft}, nil
}
