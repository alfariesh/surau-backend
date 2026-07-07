package v1

import (
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

func newActivityTestApp(personal *fakePersonal, authenticated bool) *fiber.App {
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

	app.Get("/v1/me/activity", controller.getReadingActivity)
	app.Get("/v1/me/activity/streak", controller.getReadingStreak)

	return app
}

func TestReadingActivityRoutes(t *testing.T) {
	t.Parallel()

	lastActive := "2026-06-12"
	app := newActivityTestApp(&fakePersonal{
		streak: entity.ReadingStreak{
			CurrentStreakDays: 5,
			LongestStreakDays: 12,
			TotalActiveDays:   40,
			LastActiveDate:    &lastActive,
			ActiveToday:       true,
		},
		activity: entity.ReadingActivitySummary{
			ActiveDays:     2,
			QuranAyahsRead: 30,
			KitabPagesRead: 4,
			Days: []entity.ReadingActivityDay{
				{Date: "2026-06-11", QuranAyahsRead: 10, QuranEvents: 2},
				{Date: "2026-06-12", QuranAyahsRead: 20, KitabPagesRead: 4, QuranEvents: 3, KitabEvents: 1},
			},
		},
	}, true)

	t.Run("activity summary", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/me/activity?from=2026-06-01&to=2026-06-12", http.NoBody)
		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"from":"2026-06-01"`)
		assert.Contains(t, string(body), `"quran_ayahs_read":30`)
		assert.Contains(t, string(body), `"days"`)
	})

	t.Run("streak", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/me/activity/streak?today=2026-06-12", http.NoBody)
		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"current_streak_days":5`)
		assert.Contains(t, string(body), `"today":"2026-06-12"`)
		assert.Contains(t, string(body), `"active_today":true`)
	})
}

func TestReadingActivityRouteErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		path string
	}{
		{name: "invalid date", err: entity.ErrInvalidActivityDate, path: "/v1/me/activity/streak?today=bukan-tanggal"},
		{name: "invalid range", err: entity.ErrInvalidActivityRange, path: "/v1/me/activity?from=2026-06-12&to=2026-06-01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newActivityTestApp(&fakePersonal{activityErr: tt.err}, true)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody)
			resp, err := app.Test(req)
			require.NoError(t, err)

			defer resp.Body.Close()

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestReadingActivityRoutesRequireAuth(t *testing.T) {
	t.Parallel()

	app := newActivityTestApp(&fakePersonal{}, false)

	for _, path := range []string{"/v1/me/activity", "/v1/me/activity/streak"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		resp, err := app.Test(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		resp.Body.Close()
	}
}
