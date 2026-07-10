package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCrossReferenceActorID = "550e8400-e29b-41d4-a716-446655440000"

var errCrossReferenceDatabaseUnavailable = errors.New("database unavailable")

type crossReferenceUseCaseStub struct {
	createHumanFn   func(context.Context, entity.CrossReferenceCreateInput, string) (entity.CrossReference, error)
	getFn           func(context.Context, string) (entity.CrossReference, error)
	reviewFn        func(context.Context, string, string, string, *time.Time) (entity.CrossReference, error)
	listPublicFn    func(context.Context, string, string, string, uint64, uint64) (entity.CrossReferenceList, error)
	listEditorialFn func(context.Context, repo.CrossReferenceFilter) (entity.CrossReferenceList, error)
}

//nolint:gocritic // value input mirrors the production usecase interface.
func (s *crossReferenceUseCaseStub) CreateHuman(
	ctx context.Context,
	input entity.CrossReferenceCreateInput,
	actorID string,
) (entity.CrossReference, error) {
	return s.createHumanFn(ctx, input, actorID)
}

func (s *crossReferenceUseCaseStub) Get(ctx context.Context, id string) (entity.CrossReference, error) {
	return s.getFn(ctx, id)
}

func (s *crossReferenceUseCaseStub) Review(
	ctx context.Context,
	id, status, reviewerID string,
	expected *time.Time,
) (entity.CrossReference, error) {
	return s.reviewFn(ctx, id, status, reviewerID, expected)
}

func (s *crossReferenceUseCaseStub) ListPublic(
	ctx context.Context,
	anchor, direction, kind string,
	limit, offset uint64,
) (entity.CrossReferenceList, error) {
	return s.listPublicFn(ctx, anchor, direction, kind, limit, offset)
}

//nolint:gocritic // value input mirrors the production usecase interface.
func (s *crossReferenceUseCaseStub) ListEditorial(
	ctx context.Context,
	filter repo.CrossReferenceFilter,
) (entity.CrossReferenceList, error) {
	return s.listEditorialFn(ctx, filter)
}

func TestListCrossReferencesReturnsTwoWayEnvelopeAndCacheValidator(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 10, 10, 11, 12, 0, time.UTC)
	stub := &crossReferenceUseCaseStub{
		listPublicFn: func(
			_ context.Context,
			anchor, direction, kind string,
			limit, offset uint64,
		) (entity.CrossReferenceList, error) {
			assert.Equal(t, "quran/73:4", anchor)
			assert.Equal(t, entity.CrossReferenceDirectionIncoming, direction)
			assert.Equal(t, entity.CrossReferenceKindQuotes, kind)
			assert.Equal(t, uint64(25), limit)
			assert.Equal(t, uint64(5), offset)

			return entity.CrossReferenceList{
				Items: []entity.CrossReference{{
					ID:           "b5bcd9ef-d2e1-4053-9149-3f330754346c",
					SourceAnchor: "kitab/797",
					TargetAnchor: "quran/73:4",
					Kind:         entity.CrossReferenceKindQuotes,
					ReviewStatus: entity.CrossReferenceStatusApproved,
					UpdatedAt:    updatedAt,
				}},
				Total:     3,
				WorkTotal: 2,
			}, nil
		},
	}
	app := crossReferenceTestApp(stub)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/cross-references?anchor=quran%2F73%3A4&direction=incoming&kind=quotes&limit=25&offset=5",
		http.NoBody,
	)
	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "public, max-age=300, stale-while-revalidate=86400", resp.Header.Get("Cache-Control"))
	assert.NotEmpty(t, resp.Header.Get("ETag"))

	var body entity.CrossReferenceList
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, 3, body.Total)
	assert.Equal(t, 2, body.WorkTotal)
	require.Len(t, body.Items, 1)
}

func TestListCrossReferencesRejectsMalformedPagination(t *testing.T) {
	t.Parallel()

	stub := &crossReferenceUseCaseStub{
		listPublicFn: func(
			context.Context,
			string,
			string,
			string,
			uint64,
			uint64,
		) (entity.CrossReferenceList, error) {
			t.Fatal("use case must not run")

			return entity.CrossReferenceList{}, nil
		},
	}
	app := crossReferenceTestApp(stub)

	resp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/cross-references?anchor=kitab%2F797&direction=outgoing&offset=-1",
		http.NoBody,
	))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestEditorialCreateCrossReferenceUsesSessionActorAndReturnsPendingETag(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 10, 10, 11, 12, 123, time.UTC)
	stub := &crossReferenceUseCaseStub{
		createHumanFn: func(
			_ context.Context,
			input entity.CrossReferenceCreateInput,
			actorID string,
		) (entity.CrossReference, error) {
			assert.Equal(t, testCrossReferenceActorID, actorID)
			assert.Equal(t, "kitab/797", input.SourceAnchor)
			assert.Equal(t, "kitab/798", input.TargetAnchor)
			assert.Equal(t, 0.95, input.Confidence)
			assert.Equal(t, "quoted source", input.EvidenceText)

			return entity.CrossReference{
				ID:           "b5bcd9ef-d2e1-4053-9149-3f330754346c",
				SourceAnchor: input.SourceAnchor,
				TargetAnchor: input.TargetAnchor,
				Kind:         input.Kind,
				Method:       entity.CrossReferenceMethodHuman,
				ReviewStatus: entity.CrossReferenceStatusPending,
				UpdatedAt:    updatedAt,
			}, nil
		},
	}
	app := crossReferenceTestApp(stub)

	requestBody := `{
		"source_anchor":"kitab/797",
		"target_anchor":"kitab/798",
		"kind":"quotes",
		"confidence":0.95,
		"evidence_text":"quoted source"
	}`
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/editorial/cross-references",
		strings.NewReader(requestBody),
	)
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, updatedAtETag(updatedAt), resp.Header.Get(fiber.HeaderETag))

	var body entity.CrossReference
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, entity.CrossReferenceStatusPending, body.ReviewStatus)
	assert.Equal(t, entity.CrossReferenceMethodHuman, body.Method)
}

func TestEditorialReviewCrossReferenceRequiresAndForwardsIfMatch(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 10, 10, 11, 12, 0, time.UTC)
	id := "b5bcd9ef-d2e1-4053-9149-3f330754346c"
	stub := &crossReferenceUseCaseStub{
		reviewFn: func(
			_ context.Context,
			gotID, status, reviewerID string,
			expected *time.Time,
		) (entity.CrossReference, error) {
			assert.Equal(t, id, gotID)
			assert.Equal(t, entity.CrossReferenceStatusApproved, status)
			assert.Equal(t, testCrossReferenceActorID, reviewerID)
			require.NotNil(t, expected)
			assert.True(t, expected.Equal(updatedAt))

			return entity.CrossReference{ID: id, ReviewStatus: status, UpdatedAt: updatedAt.Add(time.Second)}, nil
		},
	}
	app := crossReferenceTestApp(stub)

	missing := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/v1/editorial/cross-references/"+id+"/review",
		strings.NewReader(`{"review_status":"approved"}`),
	)
	missing.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	missingResp, err := app.Test(missing)
	require.NoError(t, err)

	defer missingResp.Body.Close()

	assert.Equal(t, http.StatusPreconditionRequired, missingResp.StatusCode)

	match := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/v1/editorial/cross-references/"+id+"/review",
		strings.NewReader(`{"review_status":"approved"}`),
	)
	match.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	match.Header.Set(fiber.HeaderIfMatch, updatedAtETag(updatedAt))
	matchResp, err := app.Test(match)
	require.NoError(t, err)

	defer matchResp.Body.Close()

	assert.Equal(t, http.StatusOK, matchResp.StatusCode)
}

func TestEditorialReviewCrossReferenceMapsStaleWrite(t *testing.T) {
	t.Parallel()

	id := "b5bcd9ef-d2e1-4053-9149-3f330754346c"
	stub := &crossReferenceUseCaseStub{
		reviewFn: func(
			context.Context,
			string,
			string,
			string,
			*time.Time,
		) (entity.CrossReference, error) {
			return entity.CrossReference{}, entity.ErrPreconditionFailed
		},
	}
	app := crossReferenceTestApp(stub)
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/v1/editorial/cross-references/"+id+"/review",
		strings.NewReader(`{"review_status":"needs_review"}`),
	)
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderIfMatch, "*")

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)
}

func TestEditorialGetCrossReferenceReturnsETagAndNotFound(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 10, 10, 11, 12, 0, time.UTC)
	id := "b5bcd9ef-d2e1-4053-9149-3f330754346c"
	stub := &crossReferenceUseCaseStub{
		getFn: func(_ context.Context, gotID string) (entity.CrossReference, error) {
			if gotID != id {
				return entity.CrossReference{}, entity.ErrCrossReferenceNotFound
			}

			return entity.CrossReference{ID: id, UpdatedAt: updatedAt}, nil
		},
	}
	app := crossReferenceTestApp(stub)

	resp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/editorial/cross-references/"+id,
		http.NoBody,
	))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, updatedAtETag(updatedAt), resp.Header.Get(fiber.HeaderETag))

	notFoundResp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/editorial/cross-references/not-a-uuid",
		http.NoBody,
	))
	require.NoError(t, err)

	defer notFoundResp.Body.Close()

	assert.Equal(t, http.StatusNotFound, notFoundResp.StatusCode)
}

func TestCrossReferenceUnexpectedErrorUsesStandardEnvelope(t *testing.T) {
	t.Parallel()

	stub := &crossReferenceUseCaseStub{
		listPublicFn: func(
			context.Context,
			string,
			string,
			string,
			uint64,
			uint64,
		) (entity.CrossReferenceList, error) {
			return entity.CrossReferenceList{}, errCrossReferenceDatabaseUnavailable
		},
	}
	app := crossReferenceTestApp(stub)

	resp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/cross-references?anchor=kitab%2F797&direction=outgoing",
		http.NoBody,
	))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func crossReferenceTestApp(stub *crossReferenceUseCaseStub) *fiber.App {
	controller := &V1{
		crossReference: stub,
		l:              logger.New("error"),
		v:              validator.New(validator.WithRequiredStructEnabled()),
	}
	app := fiber.New()
	app.Get("/v1/cross-references", middleware.PublicCache(), controller.listCrossReferences)

	withActor := func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", testCrossReferenceActorID)

		return ctx.Next()
	}
	app.Get("/v1/editorial/cross-references/:id", withActor, controller.editorialGetCrossReference)
	app.Post("/v1/editorial/cross-references", withActor, controller.editorialCreateCrossReference)
	app.Patch("/v1/editorial/cross-references/:id/review", withActor, controller.editorialReviewCrossReference)

	return app
}
