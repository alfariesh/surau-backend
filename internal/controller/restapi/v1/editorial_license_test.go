package v1

import (
	"bytes"
	"context"
	"encoding/json"
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

type fakeLicenseAudit struct {
	report          entity.BookLicenseAuditReport
	license         entity.BookLicense
	err             error
	status          string
	limit           int
	offset          int
	actorID         string
	bookID          int
	updateStatus    string
	reason          string
	evidenceURL     *string
	expectedUpdated *time.Time
	updateCalls     int
}

func (f *fakeLicenseAudit) LicenseAuditReport(
	_ context.Context,
	status string,
	limit,
	offset int,
) (entity.BookLicenseAuditReport, error) {
	f.status = status
	f.limit = limit
	f.offset = offset

	return f.report, f.err
}

func (f *fakeLicenseAudit) BookLicense(_ context.Context, bookID int) (entity.BookLicense, error) {
	f.bookID = bookID

	return f.license, f.err
}

func (f *fakeLicenseAudit) UpdateBookLicense(
	_ context.Context,
	actorID string,
	bookID int,
	status,
	reason string,
	evidenceURL *string,
	expectedUpdatedAt *time.Time,
) (entity.BookLicense, error) {
	f.updateCalls++
	f.actorID = actorID
	f.bookID = bookID
	f.updateStatus = status
	f.reason = reason
	f.evidenceURL = evidenceURL
	f.expectedUpdated = expectedUpdatedAt

	return f.license, f.err
}

func newEditorialLicenseTestApp(fake *fakeLicenseAudit) *fiber.App {
	app := fiber.New()
	controller := &V1{
		licenseAudit: fake,
		l:            logger.New("error"),
		v:            validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Get("/v1/editorial/license-audit", controller.editorialLicenseAudit)
	app.Get("/v1/editorial/books/:book_id/license", controller.editorialGetBookLicense)
	app.Patch("/v1/editorial/books/:book_id/license", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "11111111-1111-1111-1111-111111111111")

		return controller.editorialUpdateBookLicense(ctx)
	})

	return app
}

func TestEditorialLicenseAuditReturnsCoverageEnvelope(t *testing.T) {
	t.Parallel()

	generatedAt := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	fake := &fakeLicenseAudit{report: entity.BookLicenseAuditReport{
		Items:       []entity.BookLicenseAuditItem{},
		Total:       12,
		Counts:      entity.BookLicenseAuditCounts{Total: 20, Unresolved: 12},
		GeneratedAt: generatedAt,
	}}
	res, err := newEditorialLicenseTestApp(fake).Test(httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/v1/editorial/license-audit?status=needs_review&limit=25&offset=10",
		http.NoBody,
	))
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "needs_review", fake.status)
	assert.Equal(t, 25, fake.limit)
	assert.Equal(t, 10, fake.offset)

	var body entity.BookLicenseAuditReport
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, 12, body.Total)
	assert.Equal(t, 20, body.Counts.Total)
	assert.NotNil(t, body.Items)
}

func TestEditorialLicenseAuditMapsInvalidFilter(t *testing.T) {
	t.Parallel()

	fake := &fakeLicenseAudit{err: entity.ErrInvalidLicenseStatus}
	res, err := newEditorialLicenseTestApp(fake).Test(httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/v1/editorial/license-audit?status=copyrighted",
		http.NoBody,
	))
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	assertLicenseErrorCode(t, res, "invalid_license_status")
}

func TestEditorialGetBookLicenseReturnsETag(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 11, 1, 2, 3, 456000000, time.UTC)
	fake := &fakeLicenseAudit{license: entity.BookLicense{
		BookID:        797,
		BookTitle:     "Kitab",
		LicenseStatus: entity.LicenseStatusNeedsReview,
		UpdatedAt:     updatedAt,
	}}
	res, err := newEditorialLicenseTestApp(fake).Test(httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/v1/editorial/books/797/license",
		http.NoBody,
	))
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, updatedAtETag(updatedAt), res.Header.Get(fiber.HeaderETag))
	assert.Equal(t, 797, fake.bookID)
}

func TestEditorialUpdateBookLicenseRequiresIfMatch(t *testing.T) {
	t.Parallel()

	fake := &fakeLicenseAudit{}
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPatch,
		"/v1/editorial/books/797/license",
		bytes.NewBufferString(`{"license_status":"permitted","reason":"Permission received"}`),
	)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	res, err := newEditorialLicenseTestApp(fake).Test(request)
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusPreconditionRequired, res.StatusCode)
	assert.Zero(t, fake.updateCalls)
	assertLicenseErrorCode(t, res, "if_match_header_required")
}

func TestEditorialUpdateBookLicensePassesActorAndETag(t *testing.T) {
	t.Parallel()

	before := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	after := before.Add(time.Second)
	fake := &fakeLicenseAudit{license: entity.BookLicense{
		BookID:        797,
		LicenseStatus: entity.LicenseStatusPermitted,
		UpdatedAt:     after,
	}}
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPatch,
		"/v1/editorial/books/797/license",
		bytes.NewBufferString(`{"license_status":"permitted","reason":"Permission received","evidence_url":"https://example.org/evidence"}`),
	)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	request.Header.Set(fiber.HeaderIfMatch, updatedAtETag(before))
	res, err := newEditorialLicenseTestApp(fake).Test(request)
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, updatedAtETag(after), res.Header.Get(fiber.HeaderETag))
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", fake.actorID)
	assert.Equal(t, 797, fake.bookID)
	assert.Equal(t, entity.LicenseStatusPermitted, fake.updateStatus)
	assert.Equal(t, "Permission received", fake.reason)
	require.NotNil(t, fake.evidenceURL)
	assert.Equal(t, "https://example.org/evidence", *fake.evidenceURL)
	require.NotNil(t, fake.expectedUpdated)
	assert.True(t, before.Equal(*fake.expectedUpdated))
}

func TestEditorialUpdateBookLicenseMapsStaleETag(t *testing.T) {
	t.Parallel()

	fake := &fakeLicenseAudit{err: entity.ErrPreconditionFailed}
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPatch,
		"/v1/editorial/books/797/license",
		bytes.NewBufferString(`{"license_status":"restricted","reason":"Rights holder requested takedown"}`),
	)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	request.Header.Set(fiber.HeaderIfMatch, "*")
	res, err := newEditorialLicenseTestApp(fake).Test(request)
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	assertLicenseErrorCode(t, res, "precondition_failed")
}

func TestEditorialLicenseErrorUsesFrozenPublishCode(t *testing.T) {
	t.Parallel()

	fake := &fakeLicenseAudit{err: entity.ErrLicenseNotPermitted}
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPatch,
		"/v1/editorial/books/797/license",
		bytes.NewBufferString(`{"license_status":"needs_review","reason":"Reopen audit"}`),
	)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	request.Header.Set(fiber.HeaderIfMatch, "*")
	res, err := newEditorialLicenseTestApp(fake).Test(request)
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusConflict, res.StatusCode)
	assertLicenseErrorCode(t, res, "license_not_permitted")
}

func assertLicenseErrorCode(t *testing.T, res *http.Response, expected string) {
	t.Helper()

	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, expected, body.Code)
}
