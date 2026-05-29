package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/evrone/go-clean-template/internal/controller/restapi/middleware"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubUserUseCase struct {
	user entity.User
	err  error
}

func (s stubUserUseCase) Register(context.Context, string, string, string) (entity.User, error) {
	return entity.User{}, nil
}

func (s stubUserUseCase) Login(context.Context, string, string) (string, error) {
	return "", nil
}

func (s stubUserUseCase) GetUser(context.Context, string) (entity.User, error) {
	return s.user, s.err
}

func (s stubUserUseCase) SetRoleByEmail(context.Context, string, string) (entity.User, error) {
	return entity.User{}, nil
}

func (s stubUserUseCase) VerifyEmail(context.Context, string) error {
	return nil
}

func (s stubUserUseCase) ResendEmailVerification(context.Context, string) error {
	return nil
}

func (s stubUserUseCase) ForgotPassword(context.Context, string) error {
	return nil
}

func (s stubUserUseCase) ResetPassword(context.Context, string, string) error {
	return nil
}

func (s stubUserUseCase) ChangePassword(context.Context, string, string, string) error {
	return nil
}

func TestAdminMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		userID         string
		user           entity.User
		err            error
		expectedStatus int
	}{
		{
			name:           "admin allowed",
			userID:         "user-id-123",
			user:           entity.User{ID: "user-id-123", Role: entity.UserRoleAdmin},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "normal user forbidden",
			userID:         "user-id-123",
			user:           entity.User{ID: "user-id-123", Role: entity.UserRoleUser},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "missing user id unauthorized",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "lookup error unauthorized",
			userID:         "user-id-123",
			err:            entity.ErrUserNotFound,
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		localTc := tc

		t.Run(localTc.name, func(t *testing.T) {
			t.Parallel()

			app := fiber.New()
			app.Use(func(ctx *fiber.Ctx) error {
				if localTc.userID != "" {
					ctx.Locals("userID", localTc.userID)
				}

				return ctx.Next()
			})
			app.Use(middleware.Admin(stubUserUseCase{user: localTc.user, err: localTc.err}))
			app.Get("/admin", func(ctx *fiber.Ctx) error {
				return ctx.SendStatus(http.StatusOK)
			})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", http.NoBody)
			resp, err := app.Test(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, localTc.expectedStatus, resp.StatusCode)
		})
	}
}
