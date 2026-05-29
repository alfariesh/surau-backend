package v1

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthRoutesEmailVerificationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		user       *fakeAuthUser
		wantStatus int
	}{
		{
			name:       "register email delivery failure",
			method:     http.MethodPost,
			path:       "/auth/register",
			body:       `{"username":"testuser","email":"test@example.com","password":"password123"}`,
			user:       &fakeAuthUser{registerErr: entity.ErrEmailDeliveryFailed},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "login email not verified",
			method:     http.MethodPost,
			path:       "/auth/login",
			body:       `{"email":"test@example.com","password":"password123"}`,
			user:       &fakeAuthUser{loginErr: entity.ErrEmailNotVerified},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "verify invalid token",
			method:     http.MethodPost,
			path:       "/auth/verify-email",
			body:       `{"token":"invalid"}`,
			user:       &fakeAuthUser{verifyErr: entity.ErrInvalidVerificationToken},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "resend rate limited",
			method:     http.MethodPost,
			path:       "/auth/resend-verification",
			body:       `{"email":"test@example.com"}`,
			user:       &fakeAuthUser{resendErr: entity.ErrVerificationRateLimited},
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "resend delivery failure",
			method:     http.MethodPost,
			path:       "/auth/resend-verification",
			body:       `{"email":"test@example.com"}`,
			user:       &fakeAuthUser{resendErr: entity.ErrEmailDeliveryFailed},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "resend accepted",
			method:     http.MethodPost,
			path:       "/auth/resend-verification",
			body:       `{"email":"test@example.com"}`,
			user:       &fakeAuthUser{},
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "forgot password rate limited",
			method:     http.MethodPost,
			path:       "/auth/forgot-password",
			body:       `{"email":"test@example.com"}`,
			user:       &fakeAuthUser{forgotErr: entity.ErrPasswordResetRateLimited},
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "forgot password delivery failure",
			method:     http.MethodPost,
			path:       "/auth/forgot-password",
			body:       `{"email":"test@example.com"}`,
			user:       &fakeAuthUser{forgotErr: entity.ErrEmailDeliveryFailed},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "forgot password accepted",
			method:     http.MethodPost,
			path:       "/auth/forgot-password",
			body:       `{"email":"test@example.com"}`,
			user:       &fakeAuthUser{},
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "reset password invalid token",
			method:     http.MethodPost,
			path:       "/auth/reset-password",
			body:       `{"token":"invalid","password":"newpassword123"}`,
			user:       &fakeAuthUser{resetErr: entity.ErrInvalidPasswordResetToken},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "reset password success",
			method:     http.MethodPost,
			path:       "/auth/reset-password",
			body:       `{"token":"valid","password":"newpassword123"}`,
			user:       &fakeAuthUser{},
			wantStatus: http.StatusOK,
		},
		{
			name:       "change password wrong current password",
			method:     http.MethodPost,
			path:       "/auth/change-password",
			body:       `{"current_password":"oldpassword123","new_password":"newpassword123"}`,
			user:       &fakeAuthUser{changeErr: entity.ErrInvalidCredentials},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "change password success",
			method:     http.MethodPost,
			path:       "/auth/change-password",
			body:       `{"current_password":"oldpassword123","new_password":"newpassword123"}`,
			user:       &fakeAuthUser{},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newAuthTestApp(tt.user)
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

func newAuthTestApp(user *fakeAuthUser) *fiber.App {
	app := fiber.New()
	controller := &V1{
		u: user,
		l: logger.New("error"),
		v: validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Post("/auth/register", controller.register)
	app.Post("/auth/login", controller.login)
	app.Post("/auth/verify-email", controller.verifyEmail)
	app.Post("/auth/resend-verification", controller.resendVerification)
	app.Post("/auth/forgot-password", controller.forgotPassword)
	app.Post("/auth/reset-password", controller.resetPassword)
	app.Post("/auth/change-password", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.changePassword(ctx)
	})

	return app
}

type fakeAuthUser struct {
	registerErr error
	loginErr    error
	verifyErr   error
	resendErr   error
	forgotErr   error
	resetErr    error
	changeErr   error
}

func (f *fakeAuthUser) Register(context.Context, string, string, string) (entity.User, error) {
	return entity.User{ID: "user-id-123"}, f.registerErr
}

func (f *fakeAuthUser) Login(context.Context, string, string) (string, error) {
	if f.loginErr != nil {
		return "", f.loginErr
	}

	return "token", nil
}

func (f *fakeAuthUser) GetUser(context.Context, string) (entity.User, error) {
	return entity.User{}, nil
}

func (f *fakeAuthUser) SetRoleByEmail(context.Context, string, string) (entity.User, error) {
	return entity.User{}, nil
}

func (f *fakeAuthUser) VerifyEmail(context.Context, string) error {
	return f.verifyErr
}

func (f *fakeAuthUser) ResendEmailVerification(context.Context, string) error {
	return f.resendErr
}

func (f *fakeAuthUser) ForgotPassword(context.Context, string) error {
	return f.forgotErr
}

func (f *fakeAuthUser) ResetPassword(context.Context, string, string) error {
	return f.resetErr
}

func (f *fakeAuthUser) ChangePassword(context.Context, string, string, string) error {
	return f.changeErr
}
