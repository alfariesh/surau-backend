package v1

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeQuranSourceLicenseAudit struct {
	list              entity.QuranSourceLicenseList
	license           entity.QuranSourceLicense
	err               error
	sourceKind        string
	sourceID          string
	status            string
	limit             int
	offset            int
	actorID           string
	updateStatus      string
	reason            string
	evidenceURL       *string
	translator        *string
	responsibleName   *string
	responsibleRole   *string
	expectedUpdatedAt *time.Time
	updateCalls       int
}

func (f *fakeQuranSourceLicenseAudit) QuranSourceLicenses(
	_ context.Context,
	sourceKind, status string,
	limit, offset int,
) (entity.QuranSourceLicenseList, error) {
	f.sourceKind, f.status, f.limit, f.offset = sourceKind, status, limit, offset

	return f.list, f.err
}

func (f *fakeQuranSourceLicenseAudit) QuranSourceLicense(
	_ context.Context,
	sourceKind, sourceID string,
) (entity.QuranSourceLicense, error) {
	f.sourceKind, f.sourceID = sourceKind, sourceID

	return f.license, f.err
}

func (f *fakeQuranSourceLicenseAudit) UpdateQuranSourceLicense(
	_ context.Context,
	actorID, sourceKind, sourceID, status, reason string,
	evidenceURL, translator, responsibleName, responsibleRole *string,
	expectedUpdatedAt *time.Time,
) (entity.QuranSourceLicense, error) {
	f.updateCalls++
	f.actorID, f.sourceKind, f.sourceID = actorID, sourceKind, sourceID
	f.updateStatus, f.reason = status, reason
	f.evidenceURL, f.translator = evidenceURL, translator
	f.responsibleName, f.responsibleRole = responsibleName, responsibleRole
	f.expectedUpdatedAt = expectedUpdatedAt

	return f.license, f.err
}

func newEditorialQuranLicenseTestApp(fake *fakeQuranSourceLicenseAudit) *fiber.App {
	app := fiber.New()
	controller := &V1{
		quranLicenseAudit: fake,
		l:                 logger.New("error"),
		v:                 validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Get("/v1/editorial/quran/source-licenses", controller.editorialQuranSourceLicenses)
	app.Get("/v1/editorial/quran/source-licenses/:source_kind/:source_id", controller.editorialQuranSourceLicense)
	app.Patch("/v1/editorial/quran/source-licenses/:source_kind/:source_id", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "11111111-1111-1111-1111-111111111111")

		return controller.editorialUpdateQuranSourceLicense(ctx)
	})

	return app
}

func TestEditorialQuranSourceLicensesReturnsListEnvelope(t *testing.T) {
	t.Parallel()

	fake := &fakeQuranSourceLicenseAudit{list: entity.QuranSourceLicenseList{
		Items: []entity.QuranSourceLicense{}, Total: 2,
	}}
	resp, err := newEditorialQuranLicenseTestApp(fake).Test(httptest.NewRequestWithContext(
		t.Context(), http.MethodGet,
		"/v1/editorial/quran/source-licenses?source_kind=translation&status=needs_review&limit=25&offset=10",
		http.NoBody,
	))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "translation", fake.sourceKind)
	assert.Equal(t, "needs_review", fake.status)
	assert.Equal(t, 25, fake.limit)
	assert.Equal(t, 10, fake.offset)
}

func TestEditorialQuranSourceLicenseReturnsETag(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, time.July, 12, 1, 2, 3, 456000000, time.UTC)
	fake := &fakeQuranSourceLicenseAudit{license: entity.QuranSourceLicense{
		SourceKind: entity.QuranSourceKindTranslation,
		SourceID:   "kemenag-id",
		UpdatedAt:  updatedAt,
	}}
	resp, err := newEditorialQuranLicenseTestApp(fake).Test(httptest.NewRequestWithContext(
		t.Context(), http.MethodGet,
		"/v1/editorial/quran/source-licenses/translation/kemenag-id", http.NoBody,
	))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, updatedAtETag(updatedAt), resp.Header.Get(fiber.HeaderETag))
	assert.Equal(t, "translation", fake.sourceKind)
	assert.Equal(t, "kemenag-id", fake.sourceID)
}

func TestEditorialUpdateQuranSourceLicenseRequiresIfMatch(t *testing.T) {
	t.Parallel()

	fake := &fakeQuranSourceLicenseAudit{}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPatch,
		"/v1/editorial/quran/source-licenses/translation/kemenag-id",
		bytes.NewBufferString(`{"license_status":"permitted","reason":"Permission","translator":"Kemenag"}`),
	)
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	resp, err := newEditorialQuranLicenseTestApp(fake).Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusPreconditionRequired, resp.StatusCode)
	assert.Zero(t, fake.updateCalls)
	assertLicenseErrorCode(t, resp, "if_match_header_required")
}

func TestEditorialUpdateQuranSourceLicensePassesAttributionActorAndETag(t *testing.T) {
	t.Parallel()

	before := time.Date(2026, time.July, 12, 1, 2, 3, 0, time.UTC)
	after := before.Add(time.Second)
	fake := &fakeQuranSourceLicenseAudit{license: entity.QuranSourceLicense{
		SourceKind: entity.QuranSourceKindTranslation,
		SourceID:   "kemenag-id",
		UpdatedAt:  after,
	}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPatch,
		"/v1/editorial/quran/source-licenses/translation/kemenag-id",
		bytes.NewBufferString(`{"license_status":"permitted","reason":"Permission received","evidence_url":"https://example.org/evidence","translator":"Kemenag","responsible_name":"Ministry","responsible_role":"publisher"}`),
	)
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderIfMatch, updatedAtETag(before))
	resp, err := newEditorialQuranLicenseTestApp(fake).Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, updatedAtETag(after), resp.Header.Get(fiber.HeaderETag))
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", fake.actorID)
	assert.Equal(t, "translation", fake.sourceKind)
	assert.Equal(t, "kemenag-id", fake.sourceID)
	assert.Equal(t, entity.LicenseStatusPermitted, fake.updateStatus)
	assert.Equal(t, "Permission received", fake.reason)
	require.NotNil(t, fake.translator)
	assert.Equal(t, "Kemenag", *fake.translator)
	require.NotNil(t, fake.expectedUpdatedAt)
	assert.True(t, before.Equal(*fake.expectedUpdatedAt))
}

func TestEditorialUpdateQuranSourceLicenseMapsMissingAttribution(t *testing.T) {
	t.Parallel()

	fake := &fakeQuranSourceLicenseAudit{err: entity.ErrInvalidQuranSourceAttribution}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPatch,
		"/v1/editorial/quran/source-licenses/translation/kemenag-id",
		bytes.NewBufferString(`{"license_status":"permitted","reason":"Permission received"}`),
	)
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderIfMatch, "*")
	resp, err := newEditorialQuranLicenseTestApp(fake).Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assertLicenseErrorCode(t, resp, "quran_source_attribution_is_required")
}
