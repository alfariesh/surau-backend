package v1

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSavedItemRoutes(t *testing.T) {
	t.Parallel()

	ayahKey := "73:4"
	surahID := 73
	app := newSavedItemsTestApp(&fakePersonal{
		item: entity.SavedItem{
			ID:       "saved-id",
			UserID:   "user-id",
			ItemType: entity.SavedItemTypeQuranAyah,
			SurahID:  &surahID,
			AyahKey:  &ayahKey,
			Tags:     []string{"tafsir"},
		},
		tags: []string{"fiqh", "tafsir"},
	}, true)

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "upsert quran ayah",
			method:     http.MethodPost,
			path:       "/v1/me/saved-items",
			body:       `{"item_type":"quran_ayah","ayah_key":"73:4","tags":["Tafsir"]}`,
			wantStatus: http.StatusOK,
			wantBody:   `"item_type":"quran_ayah"`,
		},
		{
			name:       "list saved items",
			method:     http.MethodGet,
			path:       "/v1/me/saved-items?item_type=quran_ayah&surah_id=73&tag=tafsir",
			wantStatus: http.StatusOK,
			wantBody:   `"items"`,
		},
		{
			name:       "list saved item tags",
			method:     http.MethodGet,
			path:       "/v1/me/saved-items/tags",
			wantStatus: http.StatusOK,
			wantBody:   `"tags":["fiqh","tafsir"]`,
		},
		{
			name:       "update saved item",
			method:     http.MethodPatch,
			path:       "/v1/me/saved-items/saved-id",
			body:       `{"label":"Kajian","tags":["fiqh"]}`,
			wantStatus: http.StatusOK,
			wantBody:   `"id":"saved-id"`,
		},
		{
			name:       "delete saved item",
			method:     http.MethodDelete,
			path:       "/v1/me/saved-items/saved-id",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "invalid request body",
			method:     http.MethodPost,
			path:       "/v1/me/saved-items",
			body:       `{"ayah_key":"73:4"}`,
			wantStatus: http.StatusBadRequest,
			wantBody:   `"invalid request body"`,
		},
		{
			name:       "legacy bookmarks route removed",
			method:     http.MethodGet,
			path:       "/v1/me/bookmarks",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := app.Test(req)

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			if tt.wantBody != "" {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Contains(t, string(body), tt.wantBody)
			}
		})
	}
}

func TestSavedItemRoutesRequireAuth(t *testing.T) {
	t.Parallel()

	app := newSavedItemsTestApp(&fakePersonal{}, false)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/v1/me/saved-items", nil))

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestSavedItemRoutesNotFound(t *testing.T) {
	t.Parallel()

	app := newSavedItemsTestApp(&fakePersonal{updateErr: entity.ErrSavedItemNotFound, deleteErr: entity.ErrSavedItemNotFound}, true)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "update missing", method: http.MethodPatch, path: "/v1/me/saved-items/missing", body: `{}`},
		{name: "delete missing", method: http.MethodDelete, path: "/v1/me/saved-items/missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := app.Test(req)

			require.NoError(t, err)
			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		})
	}
}

func TestQuranProgressRoutes(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	progress := entity.QuranReadingProgress{
		UserID:          "user-id",
		SurahID:         73,
		AyahNumber:      4,
		AyahKey:         "73:4",
		PositionPercent: 5.0,
		ObservedAt:      observedAt,
		UpdatedAt:       observedAt,
	}
	app := newPersonalTestApp(&fakePersonal{
		quranProgress:   progress,
		quranProgresses: []entity.QuranReadingProgress{progress},
	}, true)

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "save quran progress",
			method:     http.MethodPut,
			path:       "/v1/me/quran/progress",
			body:       `{"ayah_key":"73:4","client_observed_at":"2026-01-01T00:00:00Z"}`,
			wantStatus: http.StatusOK,
			wantBody:   `"ayah_key":"73:4"`,
		},
		{
			name:       "get latest quran progress",
			method:     http.MethodGet,
			path:       "/v1/me/quran/progress",
			wantStatus: http.StatusOK,
			wantBody:   `"surah_id":73`,
		},
		{
			name:       "list quran surah progress",
			method:     http.MethodGet,
			path:       "/v1/me/quran/progress/surahs",
			wantStatus: http.StatusOK,
			wantBody:   `"surahs"`,
		},
		{
			name:       "get quran surah progress",
			method:     http.MethodGet,
			path:       "/v1/me/quran/progress/surahs/73",
			wantStatus: http.StatusOK,
			wantBody:   `"position_percent":5`,
		},
		{
			name:       "invalid body",
			method:     http.MethodPut,
			path:       "/v1/me/quran/progress",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantBody:   `"invalid request body"`,
		},
		{
			name:       "invalid surah path",
			method:     http.MethodGet,
			path:       "/v1/me/quran/progress/surahs/0",
			wantStatus: http.StatusBadRequest,
			wantBody:   `"invalid surah_id"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := app.Test(req)

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			if tt.wantBody != "" {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Contains(t, string(body), tt.wantBody)
			}
		})
	}
}

func TestQuranProgressRoutesRequireAuth(t *testing.T) {
	t.Parallel()

	app := newPersonalTestApp(&fakePersonal{}, false)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "get latest", method: http.MethodGet, path: "/v1/me/quran/progress"},
		{name: "save", method: http.MethodPut, path: "/v1/me/quran/progress", body: `{"ayah_key":"73:4"}`},
		{name: "list surahs", method: http.MethodGet, path: "/v1/me/quran/progress/surahs"},
		{name: "get surah", method: http.MethodGet, path: "/v1/me/quran/progress/surahs/73"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := app.Test(req)

			require.NoError(t, err)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})
	}
}

func TestQuranProgressRoutesNotFound(t *testing.T) {
	t.Parallel()

	app := newPersonalTestApp(&fakePersonal{quranErr: entity.ErrProgressNotFound}, true)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/v1/me/quran/progress", nil))

	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestQuranProgressRoutesAyahNotFound(t *testing.T) {
	t.Parallel()

	app := newPersonalTestApp(&fakePersonal{quranErr: entity.ErrQuranAyahNotFound}, true)
	req := httptest.NewRequest(http.MethodPut, "/v1/me/quran/progress", bytes.NewBufferString(`{"ayah_key":"99:999"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"quran ayah not found"`)
}

func newSavedItemsTestApp(personal *fakePersonal, authenticated bool) *fiber.App {
	return newPersonalTestApp(personal, authenticated)
}

func newPersonalTestApp(personal *fakePersonal, authenticated bool) *fiber.App {
	app := fiber.New()
	controller := &V1{
		personal: personal,
		l:        logger.New("error"),
		v:        validator.New(validator.WithRequiredStructEnabled()),
	}
	if authenticated {
		app.Use(func(ctx *fiber.Ctx) error {
			ctx.Locals("userID", "user-id")
			return ctx.Next()
		})
	}
	app.Get("/v1/me/saved-items", controller.listSavedItems)
	app.Post("/v1/me/saved-items", controller.upsertSavedItem)
	app.Get("/v1/me/saved-items/tags", controller.listSavedItemTags)
	app.Patch("/v1/me/saved-items/:id", controller.updateSavedItem)
	app.Delete("/v1/me/saved-items/:id", controller.deleteSavedItem)
	app.Get("/v1/me/quran/progress", controller.getQuranProgress)
	app.Put("/v1/me/quran/progress", controller.saveQuranProgress)
	app.Get("/v1/me/quran/progress/surahs", controller.listQuranSurahProgress)
	app.Get("/v1/me/quran/progress/surahs/:surah_id", controller.getQuranSurahProgress)

	return app
}

type fakePersonal struct {
	item            entity.SavedItem
	tags            []string
	updateErr       error
	deleteErr       error
	quranProgress   entity.QuranReadingProgress
	quranProgresses []entity.QuranReadingProgress
	quranErr        error
}

func (f *fakePersonal) GetProgress(context.Context, string, int) (entity.ReadingProgress, error) {
	return entity.ReadingProgress{}, nil
}

func (f *fakePersonal) SaveProgress(context.Context, string, int, *int, *int, *float64) (entity.ReadingProgress, error) {
	return entity.ReadingProgress{}, nil
}

func (f *fakePersonal) GetQuranProgress(context.Context, string) (entity.QuranReadingProgress, error) {
	if f.quranErr != nil {
		return entity.QuranReadingProgress{}, f.quranErr
	}

	return f.quranProgress, nil
}

func (f *fakePersonal) GetQuranSurahProgress(context.Context, string, int) (entity.QuranReadingProgress, error) {
	if f.quranErr != nil {
		return entity.QuranReadingProgress{}, f.quranErr
	}

	return f.quranProgress, nil
}

func (f *fakePersonal) ListQuranSurahProgress(context.Context, string) ([]entity.QuranReadingProgress, error) {
	if f.quranErr != nil {
		return nil, f.quranErr
	}

	return f.quranProgresses, nil
}

func (f *fakePersonal) SaveQuranProgress(_ context.Context, userID, ayahKey string, clientObservedAt *time.Time) (entity.QuranReadingProgress, error) {
	if f.quranErr != nil {
		return entity.QuranReadingProgress{}, f.quranErr
	}
	if f.quranProgress.AyahKey != "" {
		return f.quranProgress, nil
	}

	observedAt := time.Now().UTC()
	if clientObservedAt != nil {
		observedAt = clientObservedAt.UTC()
	}

	return entity.QuranReadingProgress{
		UserID:     userID,
		SurahID:    73,
		AyahNumber: 4,
		AyahKey:    ayahKey,
		ObservedAt: observedAt,
		UpdatedAt:  observedAt,
	}, nil
}

func (f *fakePersonal) ListSavedItems(context.Context, string, string, *int, *int, string, int, int) ([]entity.SavedItem, int, error) {
	return []entity.SavedItem{f.item}, 1, nil
}

func (f *fakePersonal) UpsertSavedItem(_ context.Context, userID string, item entity.SavedItem) (entity.SavedItem, error) {
	if f.item.ID == "" {
		item.ID = "saved-id"
		item.UserID = userID
		return item, nil
	}

	return f.item, nil
}

func (f *fakePersonal) UpdateSavedItem(context.Context, string, string, *string, *string, []string) (entity.SavedItem, error) {
	if f.updateErr != nil {
		return entity.SavedItem{}, f.updateErr
	}

	return f.item, nil
}

func (f *fakePersonal) DeleteSavedItem(context.Context, string, string) error {
	return f.deleteErr
}

func (f *fakePersonal) ListSavedItemTags(context.Context, string) ([]string, error) {
	return f.tags, nil
}
