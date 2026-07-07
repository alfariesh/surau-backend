package v1

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newKhatamTestApp(personal *fakePersonal, authenticated bool) *fiber.App {
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

	app.Post("/v1/me/quran/khatam", controller.startKhatamCycle)
	app.Get("/v1/me/quran/khatam", controller.getActiveKhatamCycle)
	app.Get("/v1/me/quran/khatam/history", controller.listKhatamHistory)
	app.Post("/v1/me/quran/khatam/complete", controller.completeKhatamCycle)
	app.Put("/v1/me/quran/khatam/juz/:juz_number", controller.markKhatamJuz)
	app.Delete("/v1/me/quran/khatam/juz/:juz_number", controller.unmarkKhatamJuz)

	return app
}

func TestKhatamRoutes(t *testing.T) {
	t.Parallel()

	cycle := entity.QuranKhatamCycle{
		ID:           "cycle-id",
		UserID:       "user-id",
		CompletedJuz: []int{1, 2},
		JuzCount:     2,
		Percent:      6.67,
	}
	app := newKhatamTestApp(&fakePersonal{
		khatamCycle:   cycle,
		khatamHistory: []entity.QuranKhatamCycle{cycle},
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
			name:       "start cycle",
			method:     http.MethodPost,
			path:       "/v1/me/quran/khatam",
			body:       `{"notes":"Khatam Ramadhan"}`,
			wantStatus: http.StatusCreated,
			wantBody:   `"id":"cycle-id"`,
		},
		{
			name:       "start cycle without body",
			method:     http.MethodPost,
			path:       "/v1/me/quran/khatam",
			wantStatus: http.StatusCreated,
		},
		{
			name:       "get active cycle",
			method:     http.MethodGet,
			path:       "/v1/me/quran/khatam",
			wantStatus: http.StatusOK,
			wantBody:   `"completed_juz":[1,2]`,
		},
		{
			name:       "mark juz",
			method:     http.MethodPut,
			path:       "/v1/me/quran/khatam/juz/3",
			wantStatus: http.StatusOK,
			wantBody:   `"juz_count":2`,
		},
		{
			name:       "unmark juz",
			method:     http.MethodDelete,
			path:       "/v1/me/quran/khatam/juz/3",
			wantStatus: http.StatusOK,
		},
		{
			name:       "invalid juz",
			method:     http.MethodPut,
			path:       "/v1/me/quran/khatam/juz/31",
			wantStatus: http.StatusBadRequest,
			wantBody:   `"invalid juz_number"`,
		},
		{
			name:       "complete cycle",
			method:     http.MethodPost,
			path:       "/v1/me/quran/khatam/complete",
			wantStatus: http.StatusOK,
		},
		{
			name:       "history",
			method:     http.MethodGet,
			path:       "/v1/me/quran/khatam/history",
			wantStatus: http.StatusOK,
			wantBody:   `"total":1`,
		},
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

			assert.Equal(t, tt.wantStatus, resp.StatusCode)

			if tt.wantBody != "" {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Contains(t, string(body), tt.wantBody)
			}
		})
	}
}

func TestKhatamRouteErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		method     string
		path       string
		wantStatus int
	}{
		{
			name:       "duplicate active cycle",
			err:        entity.ErrKhatamCycleActiveExists,
			method:     http.MethodPost,
			path:       "/v1/me/quran/khatam",
			wantStatus: http.StatusConflict,
		},
		{
			name:       "no active cycle",
			err:        entity.ErrKhatamCycleNotFound,
			method:     http.MethodGet,
			path:       "/v1/me/quran/khatam",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "incomplete cycle",
			err:        entity.ErrKhatamCycleIncomplete,
			method:     http.MethodPost,
			path:       "/v1/me/quran/khatam/complete",
			wantStatus: http.StatusConflict,
		},
		{
			name:       "mark without active cycle",
			err:        entity.ErrKhatamCycleNotFound,
			method:     http.MethodPut,
			path:       "/v1/me/quran/khatam/juz/3",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newKhatamTestApp(&fakePersonal{khatamErr: tt.err}, true)
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, http.NoBody)
			resp, err := app.Test(req)
			require.NoError(t, err)

			defer resp.Body.Close()

			assert.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

func TestKhatamRoutesRequireAuth(t *testing.T) {
	t.Parallel()

	app := newKhatamTestApp(&fakePersonal{}, false)
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "start", method: http.MethodPost, path: "/v1/me/quran/khatam"},
		{name: "active", method: http.MethodGet, path: "/v1/me/quran/khatam"},
		{name: "history", method: http.MethodGet, path: "/v1/me/quran/khatam/history"},
		{name: "complete", method: http.MethodPost, path: "/v1/me/quran/khatam/complete"},
		{name: "mark", method: http.MethodPut, path: "/v1/me/quran/khatam/juz/3"},
		{name: "unmark", method: http.MethodDelete, path: "/v1/me/quran/khatam/juz/3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, http.NoBody)
			resp, err := app.Test(req)
			require.NoError(t, err)

			defer resp.Body.Close()

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})
	}
}
