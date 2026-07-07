package v1

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSyncTestApp(personal *fakePersonal, authenticated bool) *fiber.App {
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

	app.Get("/v1/me/sync", controller.syncPersonalData)
	app.Post("/v1/me/progress/batch", controller.batchSaveProgress)

	return app
}

func TestSyncPersonalDataRoute(t *testing.T) {
	t.Parallel()

	serverTime := time.Date(2026, 6, 12, 3, 0, 0, 0, time.UTC)
	fake := &fakePersonal{
		syncSnapshot: entity.PersonalSyncSnapshot{
			ServerTime:      serverTime,
			ReadingProgress: []entity.ReadingProgress{{UserID: "user-id", BookID: 797}},
			QuranProgress:   []entity.QuranReadingProgress{{UserID: "user-id", SurahID: 73, AyahKey: "73:4"}},
			SavedItems:      []entity.SavedItem{{ID: "saved-id", Tags: []string{}}},
			SavedItemIDs:    []string{"saved-id"},
			KhatamCycles:    []entity.QuranKhatamCycle{{ID: "cycle-id", CompletedJuz: []int{1}}},
		},
	}
	app := newSyncTestApp(fake, true)

	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/me/sync?since=2026-06-11T00:00:00Z", http.NoBody))

	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"server_time":"2026-06-12T03:00:00Z"`)
	assert.Contains(t, string(body), `"reading_progress"`)
	assert.Contains(t, string(body), `"quran_progress"`)
	assert.Contains(t, string(body), `"saved_item_ids":["saved-id"]`)
	assert.Contains(t, string(body), `"khatam_cycles"`)

	require.NotNil(t, fake.lastSyncSince)
	assert.Equal(t, time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC), fake.lastSyncSince.UTC())
}

func TestSyncPersonalDataWithoutSince(t *testing.T) {
	t.Parallel()

	fake := &fakePersonal{}
	app := newSyncTestApp(fake, true)

	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/me/sync", http.NoBody))

	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Nil(t, fake.lastSyncSince)
}

func TestSyncPersonalDataInvalidSince(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fake *fakePersonal
		path string
	}{
		{name: "unparseable", fake: &fakePersonal{}, path: "/v1/me/sync?since=bukan-waktu"},
		{name: "rejected by usecase", fake: &fakePersonal{syncErr: entity.ErrInvalidSyncSince}, path: "/v1/me/sync?since=2030-01-01T00:00:00Z"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newSyncTestApp(tt.fake, true)
			resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody))

			require.NoError(t, err)

			defer resp.Body.Close()

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestBatchSaveProgressMixedResults(t *testing.T) {
	t.Parallel()

	fake := &fakePersonal{
		saveProgressErrs: map[int]error{999: entity.ErrBookNotFound},
		quranProgress:    entity.QuranReadingProgress{UserID: "user-id", SurahID: 73, AyahNumber: 4, AyahKey: "73:4"},
	}
	app := newSyncTestApp(fake, true)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/me/progress/batch",
		bytes.NewBufferString(`{
			"kitab": [
				{"book_id": 797, "page_id": 12, "client_observed_at": "2026-06-12T01:00:00Z"},
				{"book_id": 999, "page_id": 1}
			],
			"quran": [
				{"ayah_key": "73:4", "client_observed_at": "2026-06-12T01:00:00Z"}
			]
		}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)

	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"book_id":797`)
	assert.Contains(t, string(body), `"error":"book not found"`)
	assert.Contains(t, string(body), `"ayah_key":"73:4"`)
	// First kitab entry ok, second error, in request order.
	okIdx := bytes.Index(body, []byte(`"status":"ok"`))
	errIdx := bytes.Index(body, []byte(`"status":"error"`))

	require.GreaterOrEqual(t, okIdx, 0)
	require.GreaterOrEqual(t, errIdx, 0)
	assert.Less(t, okIdx, errIdx)
}

func TestBatchSaveProgressRejectsInvalidBodies(t *testing.T) {
	t.Parallel()

	oversized := fmt.Sprintf(`{"quran":[%s{"ayah_key":"73:4"}]}`,
		strings.Repeat(`{"ayah_key":"73:4"},`, 100))

	tests := []struct {
		name string
		body string
	}{
		{name: "empty batch", body: `{}`},
		{name: "oversized batch", body: oversized},
		{name: "invalid entry", body: `{"kitab":[{"book_id":0}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newSyncTestApp(&fakePersonal{}, true)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/me/progress/batch", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)

			require.NoError(t, err)

			defer resp.Body.Close()

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestSyncRoutesRequireAuth(t *testing.T) {
	t.Parallel()

	app := newSyncTestApp(&fakePersonal{}, false)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "sync", method: http.MethodGet, path: "/v1/me/sync"},
		{name: "batch", method: http.MethodPost, path: "/v1/me/progress/batch", body: `{"quran":[{"ayah_key":"73:4"}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, bytes.NewBufferString(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := app.Test(req)

			require.NoError(t, err)

			defer resp.Body.Close()

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})
	}
}
