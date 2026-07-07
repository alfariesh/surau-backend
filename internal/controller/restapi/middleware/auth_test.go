package middleware_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/jwt"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestApp(t *testing.T) (*fiber.App, *jwt.Manager) {
	t.Helper()

	return newTestAppWithUser(t, entity.User{ID: "user-id-123", Role: entity.UserRoleUser})
}

func newTestAppWithUser(t *testing.T, user entity.User) (*fiber.App, *jwt.Manager) {
	t.Helper()

	jwtManager := jwt.New("0123456789abcdef0123456789abcdef", time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)

	app := fiber.New()
	app.Use(middleware.Auth(jwtManager, &stubUserUseCase{user: user}))
	app.Get("/test", func(c *fiber.Ctx) error {
		userID, ok := c.Locals("userID").(string)
		if !ok {
			return c.SendStatus(http.StatusUnauthorized)
		}
		user, ok := c.Locals("user").(entity.User)
		if !ok || user.ID != userID {
			return c.SendStatus(http.StatusInternalServerError)
		}

		return c.SendString(userID)
	})

	return app, jwtManager
}

func TestAuthMiddleware(t *testing.T) {
	t.Parallel()

	app, jwtManager := newTestApp(t)

	validToken, err := jwtManager.GenerateToken("user-id-123")
	require.NoError(t, err)

	tests := []struct {
		name           string
		authHeader     string
		expectedStatus int
		expectedBody   string
		expectedCode   string
	}{
		{
			name:           "missing header",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
			expectedCode:   "AUTH_HEADER_MISSING",
		},
		{
			name:           "invalid format",
			authHeader:     "Basic xxx",
			expectedStatus: http.StatusUnauthorized,
			expectedCode:   "AUTH_HEADER_INVALID",
		},
		{
			name:           "invalid token",
			authHeader:     "Bearer invalid",
			expectedStatus: http.StatusUnauthorized,
			expectedCode:   "AUTH_TOKEN_INVALID",
		},
		{
			name:           "valid token",
			authHeader:     "Bearer " + validToken,
			expectedStatus: http.StatusOK,
			expectedBody:   "user-id-123",
		},
		{
			name:           "valid lowercase bearer with extra spaces",
			authHeader:     "bearer   " + validToken + " ",
			expectedStatus: http.StatusOK,
			expectedBody:   "user-id-123",
		},
		{
			name:           "empty bearer token",
			authHeader:     "Bearer ",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		localTc := tc

		t.Run(localTc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test", http.NoBody)
			if localTc.authHeader != "" {
				req.Header.Set("Authorization", localTc.authHeader)
			}

			resp, err := app.Test(req)
			require.NoError(t, err)

			defer resp.Body.Close()

			assert.Equal(t, localTc.expectedStatus, resp.StatusCode)

			if localTc.expectedBody != "" {
				body, readErr := io.ReadAll(resp.Body)
				require.NoError(t, readErr)
				assert.Equal(t, localTc.expectedBody, string(body))
			}
			if localTc.expectedCode != "" {
				var body struct {
					Code string `json:"code"`
				}
				require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
				assert.Equal(t, localTc.expectedCode, body.Code)
			}
		})
	}
}

func TestAuthMiddlewareRejectsRevokedTokenVersion(t *testing.T) {
	t.Parallel()

	app, jwtManager := newTestAppWithUser(t, entity.User{
		ID:           "user-id-123",
		Role:         entity.UserRoleUser,
		TokenVersion: 2,
	})

	oldToken, err := jwtManager.GenerateToken("user-id-123", 1)
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+oldToken)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "invalid or expired token", body.Error)
	assert.Equal(t, "AUTH_TOKEN_INVALID", body.Code)
}
