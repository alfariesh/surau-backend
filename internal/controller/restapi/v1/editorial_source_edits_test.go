package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/policy"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSourceEditorial struct {
	fakeProductionEditorial

	pageEdit entity.EditorialPageEdit

	savePageDraftErr         error
	savePageDraftCalls       int
	savedPageDraftOrigin     string
	savedPageDraft           entity.BookPageEdit
	savedPageDraftExpected   *time.Time
	publishPageDraftErr      error
	publishPageDraftCalls    int
	publishPageDraftExpected *time.Time
}

func (f *fakeSourceEditorial) GetPageEdit(_ context.Context, _, _ int) (entity.EditorialPageEdit, error) {
	return f.pageEdit, nil
}

//nolint:gocritic // value param mirrors the usecase.Editorial interface
func (f *fakeSourceEditorial) SavePageDraft(
	_ context.Context,
	_ string,
	edit entity.BookPageEdit,
	expected *time.Time,
	origin string,
) (entity.BookPageEdit, error) {
	f.savedPageDraftOrigin = origin
	f.savePageDraftCalls++
	f.savedPageDraft = edit
	f.savedPageDraftExpected = expected

	if f.savePageDraftErr != nil {
		return entity.BookPageEdit{}, f.savePageDraftErr
	}

	edit.Status = entity.EditStatusDraft
	edit.UpdatedAt = time.Date(2026, 1, 2, 3, 4, 8, 0, time.UTC)

	return edit, nil
}

func (f *fakeSourceEditorial) PublishPageDraft(
	_ context.Context,
	_ string,
	bookID, pageID int,
	expected *time.Time,
) (entity.BookPageEdit, error) {
	f.publishPageDraftCalls++
	f.publishPageDraftExpected = expected

	if f.publishPageDraftErr != nil {
		return entity.BookPageEdit{}, f.publishPageDraftErr
	}

	return entity.BookPageEdit{
		BookID:    bookID,
		PageID:    pageID,
		Status:    entity.EditStatusPublished,
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 9, 0, time.UTC),
	}, nil
}

//nolint:gocritic // test helper mirrors production middleware locals shape
func newSourceEditTestApp(actor entity.User, editorial *fakeSourceEditorial) *fiber.App {
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

	group := app.Group(
		"/v1/editorial",
		injectActor,
		middleware.RequireCapability(user, policy.CapReviewEditorial),
	)
	group.Get("/books/:book_id/pages/:page_id", controller.editorialGetPageEdit)
	group.Put("/books/:book_id/pages/:page_id/draft", controller.editorialSavePageDraft)
	group.Post("/books/:book_id/pages/:page_id/publish", controller.editorialPublishPageDraft)
	group.Put("/books/:book_id/metadata-draft", controller.editorialSaveMetadataDraft)

	return app
}

func TestEditorialSavePageDraftRequiresIfMatch(t *testing.T) {
	t.Parallel()

	editorial := &fakeSourceEditorial{}
	app := newSourceEditTestApp(entity.User{ID: "editor-id", Role: entity.UserRoleEditor}, editorial)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/v1/editorial/books/797/pages/1/draft",
		strings.NewReader(`{"content_html":"<p>new</p>"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusPreconditionRequired, resp.StatusCode)
	assert.Zero(t, editorial.savePageDraftCalls)
}

func TestEditorialSavePageDraftRejectsUnparseableIfMatch(t *testing.T) {
	t.Parallel()

	editorial := &fakeSourceEditorial{}
	app := newSourceEditTestApp(entity.User{ID: "editor-id", Role: entity.UserRoleEditor}, editorial)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/v1/editorial/books/797/pages/1/draft",
		strings.NewReader(`{"content_html":"<p>new</p>"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", "garbage")

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)
	assert.Zero(t, editorial.savePageDraftCalls)
}

func TestEditorialSavePageDraftForwardsExpectedTimestamp(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 2, 3, 4, 5, 6, 123456000, time.UTC)
	editorial := &fakeSourceEditorial{}
	app := newSourceEditTestApp(entity.User{ID: "editor-id", Role: entity.UserRoleEditor}, editorial)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/v1/editorial/books/797/pages/1/draft",
		strings.NewReader(`{"content_html":"<p>new</p>"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", updatedAtETag(updatedAt))

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, editorial.savePageDraftCalls)
	require.NotNil(t, editorial.savedPageDraftExpected)
	assert.True(t, editorial.savedPageDraftExpected.Equal(updatedAt))
	assert.Equal(t, 797, editorial.savedPageDraft.BookID)
	assert.Equal(t, 1, editorial.savedPageDraft.PageID)
}

func TestEditorialSavePageDraftWildcardIfMatchIsUnconditional(t *testing.T) {
	t.Parallel()

	editorial := &fakeSourceEditorial{}
	app := newSourceEditTestApp(entity.User{ID: "editor-id", Role: entity.UserRoleEditor}, editorial)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/v1/editorial/books/797/pages/1/draft",
		strings.NewReader(`{"content_html":"<p>new</p>"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", "*")

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, editorial.savePageDraftCalls)
	assert.Nil(t, editorial.savedPageDraftExpected)
}

func TestEditorialSavePageDraftStaleIfMatchMapsTo412(t *testing.T) {
	t.Parallel()

	editorial := &fakeSourceEditorial{savePageDraftErr: entity.ErrPreconditionFailed}
	app := newSourceEditTestApp(entity.User{ID: "editor-id", Role: entity.UserRoleEditor}, editorial)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/v1/editorial/books/797/pages/1/draft",
		strings.NewReader(`{"content_html":"<p>new</p>"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", updatedAtETag(time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)))

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)
	assert.Equal(t, 1, editorial.savePageDraftCalls)
}

func TestEditorialPublishPageDraftRequiresIfMatch(t *testing.T) {
	t.Parallel()

	editorial := &fakeSourceEditorial{}
	app := newSourceEditTestApp(entity.User{ID: "editor-id", Role: entity.UserRoleEditor}, editorial)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/editorial/books/797/pages/1/publish",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusPreconditionRequired, resp.StatusCode)
	assert.Zero(t, editorial.publishPageDraftCalls)
}

func TestEditorialSaveMetadataDraftIfMatchIsOptional(t *testing.T) {
	t.Parallel()

	editorial := &fakeSourceEditorial{}
	app := newSourceEditTestApp(entity.User{ID: "editor-id", Role: entity.UserRoleEditor}, editorial)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/v1/editorial/books/797/metadata-draft",
		strings.NewReader(`{"display_title":"New title"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, editorial.saveMetadataDraftCalls)
	assert.Nil(t, editorial.savedMetadataDraftExpected)
}

func TestParseIfMatch(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 2, 3, 4, 5, 6, 123456000, time.UTC)
	stale := updatedAt.Add(-time.Second)

	tests := []struct {
		name        string
		header      string
		wantExpect  *time.Time
		wantPresent bool
		wantOK      bool
	}{
		{name: "absent header", header: "", wantExpect: nil, wantPresent: false, wantOK: true},
		{name: "wildcard", header: "*", wantExpect: nil, wantPresent: true, wantOK: true},
		{name: "valid etag", header: updatedAtETag(updatedAt), wantExpect: &updatedAt, wantPresent: true, wantOK: true},
		{name: "weak etag", header: "W/" + updatedAtETag(updatedAt), wantExpect: nil, wantPresent: true, wantOK: false},
		{name: "stale then current", header: updatedAtETag(stale) + ", " + updatedAtETag(updatedAt), wantExpect: &updatedAt, wantPresent: true, wantOK: true},
		{name: "current then stale", header: updatedAtETag(updatedAt) + ", " + updatedAtETag(stale), wantExpect: &updatedAt, wantPresent: true, wantOK: true},
		{name: "stale then wildcard", header: updatedAtETag(stale) + ", *", wantExpect: nil, wantPresent: true, wantOK: true},
		{name: "garbage", header: "garbage", wantExpect: nil, wantPresent: true, wantOK: false},
		{name: "quoted non-numeric", header: `"abc"`, wantExpect: nil, wantPresent: true, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := fiber.New()
			app.Get("/", func(ctx *fiber.Ctx) error {
				expected, present, ok := parseIfMatch(ctx)

				assert.Equal(t, tt.wantPresent, present)
				assert.Equal(t, tt.wantOK, ok)

				if tt.wantExpect == nil {
					assert.Nil(t, expected)
				} else {
					require.NotNil(t, expected)
					assert.True(t, expected.Equal(*tt.wantExpect))
				}

				return ctx.SendStatus(http.StatusOK)
			})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("If-Match", tt.header)
			}

			resp, err := app.Test(req)
			require.NoError(t, err)

			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}
