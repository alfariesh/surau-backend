package v1

import (
	"context"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/evrone/go-clean-template/pkg/logger"
	natsrpc "github.com/evrone/go-clean-template/pkg/nats/nats_rpc"
	"github.com/go-playground/validator/v10"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

func newNATSTestJWTManager() *jwt.Manager {
	return jwt.New("0123456789abcdef0123456789abcdef", time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)
}

func TestRegisterBadRequest(t *testing.T) {
	t.Parallel()

	handler := (&V1{v: validator.New(validator.WithRequiredStructEnabled())}).register()

	_, err := handler(&nats.Msg{Data: []byte(`{"username":"ab","email":"bad","password":"short"}`)})

	require.ErrorIs(t, err, natsrpc.ErrBadRequest)
}

func TestAuthenticatedHandlerUnauthenticated(t *testing.T) {
	t.Parallel()

	handler := (&V1{j: newNATSTestJWTManager()}).getHistory()

	_, err := handler(&nats.Msg{Data: []byte(`{"token":"invalid","data":{}}`)})

	require.ErrorIs(t, err, natsrpc.ErrUnauthenticated)
}

func TestAuthEmailVerificationErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler func(*V1) func(*nats.Msg) (any, error)
		body    string
		user    *fakeAuthUser
		wantErr error
	}{
		{
			name:    "register delivery unavailable",
			handler: func(v *V1) func(*nats.Msg) (any, error) { return v.register() },
			body:    `{"username":"testuser","email":"test@example.com","password":"password123"}`,
			user:    &fakeAuthUser{registerErr: entity.ErrEmailDeliveryFailed},
			wantErr: natsrpc.ErrUnavailable,
		},
		{
			name:    "login unverified failed precondition",
			handler: func(v *V1) func(*nats.Msg) (any, error) { return v.login() },
			body:    `{"email":"test@example.com","password":"password123"}`,
			user:    &fakeAuthUser{loginErr: entity.ErrEmailNotVerified},
			wantErr: natsrpc.ErrFailedPrecondition,
		},
		{
			name:    "verify invalid token bad request",
			handler: func(v *V1) func(*nats.Msg) (any, error) { return v.verifyEmail() },
			body:    `{"token":"invalid"}`,
			user:    &fakeAuthUser{verifyErr: entity.ErrInvalidVerificationToken},
			wantErr: natsrpc.ErrBadRequest,
		},
		{
			name:    "resend rate limited",
			handler: func(v *V1) func(*nats.Msg) (any, error) { return v.resendVerification() },
			body:    `{"email":"test@example.com"}`,
			user:    &fakeAuthUser{resendErr: entity.ErrVerificationRateLimited},
			wantErr: natsrpc.ErrRateLimited,
		},
		{
			name:    "resend delivery unavailable",
			handler: func(v *V1) func(*nats.Msg) (any, error) { return v.resendVerification() },
			body:    `{"email":"test@example.com"}`,
			user:    &fakeAuthUser{resendErr: entity.ErrEmailDeliveryFailed},
			wantErr: natsrpc.ErrUnavailable,
		},
		{
			name:    "forgot password rate limited",
			handler: func(v *V1) func(*nats.Msg) (any, error) { return v.forgotPassword() },
			body:    `{"email":"test@example.com"}`,
			user:    &fakeAuthUser{forgotErr: entity.ErrPasswordResetRateLimited},
			wantErr: natsrpc.ErrRateLimited,
		},
		{
			name:    "forgot password delivery unavailable",
			handler: func(v *V1) func(*nats.Msg) (any, error) { return v.forgotPassword() },
			body:    `{"email":"test@example.com"}`,
			user:    &fakeAuthUser{forgotErr: entity.ErrEmailDeliveryFailed},
			wantErr: natsrpc.ErrUnavailable,
		},
		{
			name:    "reset password invalid token",
			handler: func(v *V1) func(*nats.Msg) (any, error) { return v.resetPassword() },
			body:    `{"token":"invalid","password":"newpassword123"}`,
			user:    &fakeAuthUser{resetErr: entity.ErrInvalidPasswordResetToken},
			wantErr: natsrpc.ErrBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			controller := &V1{u: tt.user, l: logger.New("error"), v: validator.New(validator.WithRequiredStructEnabled())}
			handler := tt.handler(controller)

			_, err := handler(&nats.Msg{Data: []byte(tt.body)})

			require.ErrorIs(t, err, tt.wantErr)
		})
	}
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
	return entity.User{}, f.registerErr
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
