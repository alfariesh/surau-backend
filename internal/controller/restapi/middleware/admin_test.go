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

func (s stubUserUseCase) Login(context.Context, string, string) (entity.LoginResult, error) {
	return entity.LoginResult{}, nil
}

func (s stubUserUseCase) RefreshSession(context.Context, string) (entity.LoginResult, error) {
	return entity.LoginResult{}, nil
}

func (s stubUserUseCase) Logout(context.Context, string) error {
	return nil
}

func (s stubUserUseCase) LogoutAll(context.Context, string) error {
	return nil
}

func (s stubUserUseCase) GetUser(context.Context, string) (entity.User, error) {
	return s.user, s.err
}

func (s stubUserUseCase) GetUserAccount(context.Context, string) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (s stubUserUseCase) AdminUsers(context.Context, string, string, *bool, int, int) ([]entity.UserAccount, int, error) {
	return nil, 0, nil
}

func (s stubUserUseCase) AdminUserActivity(context.Context, string, int, int) ([]entity.UserActivity, int, error) {
	return nil, 0, nil
}

func (s stubUserUseCase) CompleteOnboarding(
	context.Context,
	string,
	entity.UserOnboarding,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (s stubUserUseCase) UpdateUserProfile(
	context.Context,
	string,
	entity.UserProfilePatch,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (s stubUserUseCase) UpdateUserPreferences(
	context.Context,
	string,
	entity.UserPreferencesPatch,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (s stubUserUseCase) SetRoleByEmail(context.Context, string, string, string, string) (entity.User, error) {
	return entity.User{}, nil
}

func (s stubUserUseCase) VerifyEmail(context.Context, string, string, string) error {
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

func (s stubUserUseCase) ChangePassword(context.Context, string, string, string) (entity.LoginResult, error) {
	return entity.LoginResult{}, nil
}

func (s stubUserUseCase) RequestEmailChange(context.Context, string, string, string) error {
	return nil
}

func (s stubUserUseCase) VerifyEmailChange(context.Context, string, string, string) (entity.LoginResult, error) {
	return entity.LoginResult{}, nil
}

func (s stubUserUseCase) DeleteAccount(context.Context, string, string) error {
	return nil
}

func TestRequireRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		userID         string
		localUser      entity.User
		user           entity.User
		err            error
		roles          []string
		expectedStatus int
	}{
		{
			name:           "editor allowed for editorial review",
			userID:         "user-id-123",
			user:           entity.User{ID: "user-id-123", Role: entity.UserRoleEditor},
			roles:          []string{entity.UserRoleEditor, entity.UserRoleAdmin},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "cached admin user allowed",
			localUser:      entity.User{ID: "user-id-123", Role: entity.UserRoleAdmin},
			roles:          []string{entity.UserRoleEditor, entity.UserRoleAdmin},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "normal user forbidden for editorial review",
			userID:         "user-id-123",
			user:           entity.User{ID: "user-id-123", Role: entity.UserRoleUser},
			roles:          []string{entity.UserRoleEditor, entity.UserRoleAdmin},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "editor forbidden for publish",
			userID:         "user-id-123",
			user:           entity.User{ID: "user-id-123", Role: entity.UserRoleEditor},
			roles:          []string{entity.UserRoleAdmin},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "admin allowed for publish",
			userID:         "user-id-123",
			user:           entity.User{ID: "user-id-123", Role: entity.UserRoleAdmin},
			roles:          []string{entity.UserRoleAdmin},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "missing user id unauthorized",
			roles:          []string{entity.UserRoleAdmin},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "lookup error unauthorized",
			userID:         "user-id-123",
			err:            entity.ErrUserNotFound,
			roles:          []string{entity.UserRoleAdmin},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		localTc := tc

		t.Run(localTc.name, func(t *testing.T) {
			t.Parallel()

			app := fiber.New()
			app.Use(func(ctx *fiber.Ctx) error {
				if localTc.localUser.ID != "" {
					ctx.Locals("user", localTc.localUser)
				}
				if localTc.userID != "" {
					ctx.Locals("userID", localTc.userID)
				}

				return ctx.Next()
			})
			app.Use(middleware.RequireRoles(stubUserUseCase{user: localTc.user, err: localTc.err}, localTc.roles...))
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
