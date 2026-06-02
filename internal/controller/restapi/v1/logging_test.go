package v1

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReaderExpectedErrorsLogWarn(t *testing.T) {
	t.Parallel()

	l := &spyLogger{}
	app := newReaderLanguageTestAppWithLogger(&fakeReader{err: entity.ErrBookNotFound}, l)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/books/1", http.NoBody)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, 1, l.warnCount)
	assert.Zero(t, l.errorCount)
}

func TestReaderUnexpectedErrorsLogError(t *testing.T) {
	t.Parallel()

	l := &spyLogger{}
	app := newReaderLanguageTestAppWithLogger(&fakeReader{err: errors.New("database offline")}, l)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/books", http.NoBody)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Zero(t, l.warnCount)
	assert.Equal(t, 1, l.errorCount)
}

func TestEditorialMissingReaderAssetsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantWarns  int
	}{
		{
			name:       "unsupported target language",
			err:        entity.ErrUnsupportedLanguage,
			wantStatus: http.StatusBadRequest,
			wantWarns:  1,
		},
		{
			name:       "invalid asset type",
			err:        entity.ErrInvalidAssetType,
			wantStatus: http.StatusBadRequest,
			wantWarns:  1,
		},
		{
			name:       "unexpected failure",
			err:        errors.New("query failed"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := &spyLogger{}
			app := newEditorialMissingReaderAssetsTestApp(&fakeMissingAssetsEditorial{err: tt.err}, l)
			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				"/v1/editorial/reader/missing-assets?target_lang=en&asset_type=section_translation",
				http.NoBody,
			)

			resp, err := app.Test(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			assert.Equal(t, tt.wantWarns, l.warnCount)
			if tt.wantWarns == 0 {
				assert.Equal(t, 1, l.errorCount)
			} else {
				assert.Zero(t, l.errorCount)
			}
		})
	}
}

func TestEditorialMissingReaderAssetsPassesFilters(t *testing.T) {
	t.Parallel()

	l := &spyLogger{}
	editorial := &fakeMissingAssetsEditorial{
		assets: entity.EditorialMissingReaderAssets{
			Items: []entity.EditorialMissingReaderAsset{
				{AssetType: entity.MissingAssetSectionTranslation, TargetLang: "en"},
			},
			Total: 1,
		},
	}
	app := newEditorialMissingReaderAssetsTestApp(editorial, l)
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/editorial/reader/missing-assets?target_lang=en-US&asset_type=section_translation&book_id=797&limit=25&offset=5",
		http.NoBody,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "en-US", editorial.targetLang)
	assert.Equal(t, entity.MissingAssetSectionTranslation, editorial.assetType)
	require.NotNil(t, editorial.bookID)
	assert.Equal(t, 797, *editorial.bookID)
	assert.Equal(t, 25, editorial.limit)
	assert.Equal(t, 5, editorial.offset)
	assert.Zero(t, l.warnCount)
	assert.Zero(t, l.errorCount)
}

func newEditorialMissingReaderAssetsTestApp(editorial usecase.Editorial, l *spyLogger) *fiber.App {
	app := fiber.New()
	controller := &V1{
		editorial: editorial,
		l:         l,
		v:         validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Get("/v1/editorial/reader/missing-assets", controller.editorialMissingReaderAssets)

	return app
}

type fakeMissingAssetsEditorial struct {
	usecase.Editorial

	assets     entity.EditorialMissingReaderAssets
	err        error
	targetLang string
	assetType  string
	bookID     *int
	limit      int
	offset     int
}

func (f *fakeMissingAssetsEditorial) MissingReaderAssets(
	_ context.Context,
	targetLang string,
	assetType string,
	bookID *int,
	limit int,
	offset int,
) (entity.EditorialMissingReaderAssets, error) {
	f.targetLang = targetLang
	f.assetType = assetType
	f.bookID = bookID
	f.limit = limit
	f.offset = offset

	return f.assets, f.err
}

type spyLogger struct {
	warnCount  int
	errorCount int
}

func (s *spyLogger) Debug(any, ...any)   {}
func (s *spyLogger) Info(string, ...any) {}
func (s *spyLogger) Warn(string, ...any) { s.warnCount++ }
func (s *spyLogger) Error(any, ...any)   { s.errorCount++ }
func (s *spyLogger) Fatal(any, ...any)   { s.errorCount++ }
