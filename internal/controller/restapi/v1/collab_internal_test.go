package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/controller/restapi/middleware"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeJSONBody(t *testing.T, resp *http.Response, target any) {
	t.Helper()

	defer resp.Body.Close()

	require.NoError(t, json.NewDecoder(resp.Body).Decode(target))
}

const testServiceToken = "test-service-token-32-bytes-minimum!"

func newCollabInternalTestApp(editorial *fakeSourceEditorial, token string) *fiber.App {
	app := fiber.New()
	group := app.Group("/internal", middleware.ServiceToken(token))
	NewInternalRoutes(group, editorial, logger.New("error"))

	return app
}

func TestCollabInternalRequiresServiceToken(t *testing.T) {
	t.Parallel()

	editorial := &fakeSourceEditorial{}
	app := newCollabInternalTestApp(editorial, testServiceToken)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/internal/collab/books/797/pages/1/draft",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	req = httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/internal/collab/books/797/pages/1/draft",
		nil,
	)
	req.Header.Set("X-Internal-Token", "wrong-token")

	resp, err = app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestCollabInternalDisabledWithEmptyToken(t *testing.T) {
	t.Parallel()

	editorial := &fakeSourceEditorial{}
	app := newCollabInternalTestApp(editorial, "")

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/internal/collab/books/797/pages/1/draft",
		nil,
	)
	req.Header.Set("X-Internal-Token", "anything")

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestCollabInternalGetPageDraftSeedsFromDraftThenRaw(t *testing.T) {
	t.Parallel()

	draftUpdatedAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	editorial := &fakeSourceEditorial{
		pageEdit: entity.EditorialPageEdit{
			Raw: entity.BookPage{BookID: 797, PageID: 1, ContentHTML: "<p>raw</p>"},
			Draft: &entity.BookPageEdit{
				BookID:      797,
				PageID:      1,
				ContentHTML: "<p>draft</p>",
				UpdatedAt:   draftUpdatedAt,
			},
		},
	}
	app := newCollabInternalTestApp(editorial, testServiceToken)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/internal/collab/books/797/pages/1/draft",
		nil,
	)
	req.Header.Set("X-Internal-Token", testServiceToken)

	resp, err := app.Test(req)
	require.NoError(t, err)

	var body struct {
		Source      string `json:"source"`
		ContentHTML string `json:"content_html"`
	}
	decodeJSONBody(t, resp, &body)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "draft", body.Source)
	assert.Equal(t, "<p>draft</p>", body.ContentHTML)

	// Without a draft row, seeding falls back to the raw page.
	editorial.pageEdit.Draft = nil

	req = httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/internal/collab/books/797/pages/1/draft",
		nil,
	)
	req.Header.Set("X-Internal-Token", testServiceToken)

	resp, err = app.Test(req)
	require.NoError(t, err)
	decodeJSONBody(t, resp, &body)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "raw", body.Source)
	assert.Equal(t, "<p>raw</p>", body.ContentHTML)
}

func TestCollabInternalPutPageDraftUsesCollabOrigin(t *testing.T) {
	t.Parallel()

	editorial := &fakeSourceEditorial{}
	app := newCollabInternalTestApp(editorial, testServiceToken)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/internal/collab/books/797/pages/1/draft",
		strings.NewReader(`{
			"content_html": "<p>merged</p>",
			"actor_id": "550e8400-e29b-41d4-a716-446655440000",
			"contributors": ["550e8400-e29b-41d4-a716-446655440000"]
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", testServiceToken)

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, editorial.savePageDraftCalls)
	assert.Equal(t, entity.EditOriginCollab, editorial.savedPageDraftOrigin)
	assert.Nil(t, editorial.savedPageDraftExpected)
	assert.Equal(t, "<p>merged</p>", editorial.savedPageDraft.ContentHTML)
	assert.NotEmpty(t, resp.Header.Get("ETag"))
}

func TestCollabInternalPutPageDraftValidatesActor(t *testing.T) {
	t.Parallel()

	editorial := &fakeSourceEditorial{}
	app := newCollabInternalTestApp(editorial, testServiceToken)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/internal/collab/books/797/pages/1/draft",
		strings.NewReader(`{"content_html": "<p>merged</p>", "actor_id": "not-a-uuid"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", testServiceToken)

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Zero(t, editorial.savePageDraftCalls)
}
