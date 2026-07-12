package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAnchorResolver struct {
	result entity.AnchorResolution
	err    error

	calls  int
	anchor string
	bookID *int
	pageID *int
	input  *entity.AnchorResolveInput
}

//nolint:gocritic // value parameter mirrors the production usecase interface
func (f *fakeAnchorResolver) ResolveInput(
	_ context.Context,
	input entity.AnchorResolveInput,
) (entity.AnchorResolution, error) {
	f.calls++
	f.anchor = input.Anchor
	f.bookID = input.BookID
	f.pageID = input.PageID
	f.input = &input

	return f.result, f.err
}

func (f *fakeAnchorResolver) Resolve(
	_ context.Context,
	anchor string,
	bookID, pageID *int,
) (entity.AnchorResolution, error) {
	f.calls++
	f.anchor = anchor
	f.bookID = bookID
	f.pageID = pageID

	return f.result, f.err
}

func TestResolveAnchorControllerSuccessAndCacheValidators(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, time.July, 10, 13, 14, 15, 0, time.UTC)
	canonical := "kitab/797/h/11/u/42"
	unitID := "550e8400-e29b-41d4-a716-446655440000"
	bookID, headingID, pageID := 797, 11, 12
	resolver := &fakeAnchorResolver{result: entity.AnchorResolution{
		Requested:       entity.AnchorRequested{Form: entity.AnchorFormCanonical, Anchor: canonical},
		CanonicalAnchor: &canonical,
		Boundaries: []entity.AnchorBoundary{{
			Role:            entity.AnchorBoundaryPoint,
			CanonicalAnchor: &canonical,
			Status:          entity.UnitLifecycleActive,
			ActiveTargets: []entity.AnchorTarget{{
				TargetType:      entity.AnchorTargetCitableUnit,
				Corpus:          entity.UnitCorpusKitab,
				CanonicalAnchor: &canonical,
				UnitID:          &unitID,
				BookID:          &bookID,
				HeadingID:       &headingID,
				PageID:          &pageID,
				NavigationURL:   "/v1/books/797/toc/11/read",
				UpdatedAt:       updatedAt,
			}},
			RedirectChain: []entity.AnchorRedirect{},
		}},
	}}
	app := newAnchorControllerTestApp(resolver)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/anchors/resolve?anchor=kitab%2F797%2Fh%2F11%2Fu%2F42",
		http.NoBody,
	)
	req.Header.Set("X-Request-ID", "anchor-success")
	resp, err := app.Test(req)
	require.NoError(t, err)

	var body entity.AnchorResolution
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, canonical, resolver.anchor)
	assert.Nil(t, resolver.bookID)
	assert.Nil(t, resolver.pageID)
	assert.Equal(t, canonical, *body.CanonicalAnchor)
	assert.NotContains(t, body.Boundaries[0].ActiveTargets[0].NavigationURL, "text")
	assert.Equal(t, "public, max-age=300, stale-while-revalidate=86400", resp.Header.Get("Cache-Control"))
	assert.NotEmpty(t, resp.Header.Get("ETag"))
	assert.Equal(t, "Fri, 10 Jul 2026 13:14:15 GMT", resp.Header.Get("Last-Modified"))
	assert.Equal(t, "anchor-success", resp.Header.Get("X-Request-ID"))

	conditionalReq := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/anchors/resolve?anchor=kitab%2F797%2Fh%2F11%2Fu%2F42",
		http.NoBody,
	)
	conditionalReq.Header.Set("If-None-Match", resp.Header.Get("ETag"))
	conditionalResp, err := app.Test(conditionalReq)
	require.NoError(t, err)

	defer conditionalResp.Body.Close()

	conditionalBody, err := io.ReadAll(conditionalResp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotModified, conditionalResp.StatusCode)
	assert.Empty(t, conditionalBody)
	assert.Equal(t, 2, resolver.calls)
}

func TestResolveAnchorControllerPassesLegacyScopes(t *testing.T) {
	t.Parallel()

	resolver := &fakeAnchorResolver{result: anchorControllerFixtureResolution()}
	app := newAnchorControllerTestApp(resolver)

	resp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/anchors/resolve?anchor=toc-11&book_id=797",
		http.NoBody,
	))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "toc-11", resolver.anchor)
	require.NotNil(t, resolver.bookID)
	assert.Equal(t, 797, *resolver.bookID)
	assert.Nil(t, resolver.pageID)

	resolver = &fakeAnchorResolver{result: anchorControllerFixtureResolution()}
	app = newAnchorControllerTestApp(resolver)
	resp, err = app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/anchors/resolve?book_id=797&page_id=12",
		http.NoBody,
	))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resolver.anchor)
	require.NotNil(t, resolver.bookID)
	require.NotNil(t, resolver.pageID)
	assert.Equal(t, 797, *resolver.bookID)
	assert.Equal(t, 12, *resolver.pageID)
}

func TestResolveAnchorControllerPassesEveryLegacyQuranLocator(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		query  string
		assert func(*testing.T, *entity.AnchorResolveInput)
	}{
		{
			name:  "surah",
			query: "surah_id=73",
			assert: func(t *testing.T, input *entity.AnchorResolveInput) {
				t.Helper()

				require.NotNil(t, input.SurahID)
				assert.Equal(t, 73, *input.SurahID)
			},
		},
		{
			name:  "surah ayah range",
			query: "surah_id=73&from_ayah_number=1&to_ayah_number=4",
			assert: func(t *testing.T, input *entity.AnchorResolveInput) {
				t.Helper()

				require.NotNil(t, input.SurahID)
				require.NotNil(t, input.FromAyahNumber)
				require.NotNil(t, input.ToAyahNumber)
				assert.Equal(t, []int{73, 1, 4}, []int{*input.SurahID, *input.FromAyahNumber, *input.ToAyahNumber})
			},
		},
		{
			name:  "juz",
			query: "juz_number=29",
			assert: func(t *testing.T, input *entity.AnchorResolveInput) {
				t.Helper()

				require.NotNil(t, input.JuzNumber)
				assert.Equal(t, 29, *input.JuzNumber)
			},
		},
		{
			name:  "hizb",
			query: "hizb_number=57",
			assert: func(t *testing.T, input *entity.AnchorResolveInput) {
				t.Helper()

				require.NotNil(t, input.HizbNumber)
				assert.Equal(t, 57, *input.HizbNumber)
			},
		},
		{
			name:  "mushaf page",
			query: "page_number=574",
			assert: func(t *testing.T, input *entity.AnchorResolveInput) {
				t.Helper()

				require.NotNil(t, input.PageNumber)
				assert.Equal(t, 574, *input.PageNumber)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resolver := &fakeAnchorResolver{result: anchorControllerFixtureResolution()}
			resp, err := newAnchorControllerTestApp(resolver).Test(httptest.NewRequestWithContext(
				t.Context(), http.MethodGet, "/v1/anchors/resolve?"+test.query, http.NoBody,
			))
			require.NoError(t, err)

			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.NotNil(t, resolver.input)
			test.assert(t, resolver.input)
		})
	}
}

func TestResolveAnchorControllerRejectsMalformedQueryBeforeUseCase(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		path string
	}{
		{name: "present but empty anchor", path: "/v1/anchors/resolve?anchor="},
		{name: "book id is not an integer", path: "/v1/anchors/resolve?anchor=toc-11&book_id=nope"},
		{name: "page id is not an integer", path: "/v1/anchors/resolve?book_id=797&page_id=nope"},
		{name: "book id overflows integer", path: "/v1/anchors/resolve?book_id=999999999999999999999&page_id=1"},
		{name: "book id has leading zero", path: "/v1/anchors/resolve?anchor=toc-11&book_id=0797"},
		{name: "page id has leading zero", path: "/v1/anchors/resolve?book_id=797&page_id=012"},
		{name: "duplicate anchor", path: "/v1/anchors/resolve?anchor=quran%2F73%3A4&anchor=quran%2F73%3A5"},
		{name: "duplicate book id", path: "/v1/anchors/resolve?book_id=1&book_id=2&page_id=12"},
		{name: "duplicate page id", path: "/v1/anchors/resolve?book_id=797&page_id=1&page_id=2"},
		{name: "duplicate Quran page number", path: "/v1/anchors/resolve?page_number=574&page_number=575"},
		{name: "Quran locator has leading zero", path: "/v1/anchors/resolve?juz_number=029"},
		{name: "unknown query parameter", path: "/v1/anchors/resolve?anchor=quran%2F73%3A4&lang=id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resolver := &fakeAnchorResolver{}
			app := newAnchorControllerTestApp(resolver)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, http.NoBody)
			req.Header.Set("X-Request-ID", "anchor-invalid-query")
			resp, err := app.Test(req)
			require.NoError(t, err)

			body := decodeAnchorErrorEnvelope(t, resp)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Equal(t, "invalid anchor", body.Error)
			assert.Equal(t, "invalid_anchor", body.Code)
			assert.Equal(t, "invalid anchor", body.Message)
			assert.Equal(t, "anchor-invalid-query", body.RequestID)
			assert.Zero(t, resolver.calls)
		})
	}
}

func TestResolveAnchorControllerErrorEnvelopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		resolverErr error
		wantStatus  int
		wantError   string
		wantCode    string
	}{
		{name: "invalid or ambiguous shape", resolverErr: entity.ErrInvalidAnchor, wantStatus: http.StatusBadRequest, wantError: "invalid anchor", wantCode: "invalid_anchor"},
		{name: "unknown anchor", resolverErr: entity.ErrAnchorNotFound, wantStatus: http.StatusNotFound, wantError: "anchor not found", wantCode: "anchor_not_found"},
		{name: "legacy unit not found", resolverErr: entity.ErrUnitNotFound, wantStatus: http.StatusNotFound, wantError: "anchor not found", wantCode: "anchor_not_found"},
		{name: "lineage cycle", resolverErr: entity.ErrAnchorLineageCycle, wantStatus: http.StatusInternalServerError, wantError: "internal server error", wantCode: "internal_server_error"},
		{name: "repository failure", resolverErr: fmt.Errorf("database disconnected: %w", io.ErrUnexpectedEOF), wantStatus: http.StatusInternalServerError, wantError: "internal server error", wantCode: "internal_server_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resolver := &fakeAnchorResolver{err: test.resolverErr}
			app := newAnchorControllerTestApp(resolver)
			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				"/v1/anchors/resolve?anchor=quran%2F73%3A4",
				http.NoBody,
			)
			req.Header.Set("X-Request-ID", "anchor-error")
			resp, err := app.Test(req)
			require.NoError(t, err)

			body := decodeAnchorErrorEnvelope(t, resp)
			assert.Equal(t, test.wantStatus, resp.StatusCode)
			assert.Equal(t, test.wantError, body.Error)
			assert.Equal(t, test.wantCode, body.Code)
			assert.Equal(t, test.wantError, body.Message)
			assert.Equal(t, "anchor-error", body.RequestID)
			assert.Empty(t, resp.Header.Get("ETag"), "error responses must not be cached")
			assert.Equal(t, 1, resolver.calls)
		})
	}
}

func newAnchorControllerTestApp(resolver *fakeAnchorResolver) *fiber.App {
	app := fiber.New()
	app.Use(middleware.RequestID())

	controller := &V1{anchor: resolver, l: logger.New("error")}
	group := app.Group("/v1/anchors", middleware.PublicCache())
	group.Get("/resolve", controller.resolveAnchor)

	return app
}

func anchorControllerFixtureResolution() entity.AnchorResolution {
	canonical := "kitab/797/h/11"

	return entity.AnchorResolution{
		Requested:       entity.AnchorRequested{Form: entity.AnchorFormCanonical, Anchor: canonical},
		CanonicalAnchor: &canonical,
		Boundaries: []entity.AnchorBoundary{{
			Role:            entity.AnchorBoundaryPoint,
			CanonicalAnchor: &canonical,
			Status:          entity.UnitLifecycleTombstoned,
			ActiveTargets:   []entity.AnchorTarget{},
			RedirectChain:   []entity.AnchorRedirect{},
		}},
	}
}

type anchorErrorEnvelope struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func decodeAnchorErrorEnvelope(t *testing.T, resp *http.Response) anchorErrorEnvelope {
	t.Helper()

	defer resp.Body.Close()

	var body anchorErrorEnvelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	return body
}
