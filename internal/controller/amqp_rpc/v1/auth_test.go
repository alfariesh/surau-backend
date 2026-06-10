package v1

import (
	"context"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/evrone/go-clean-template/pkg/logger"
	rmqrpc "github.com/evrone/go-clean-template/pkg/rabbitmq/rmq_rpc"
	"github.com/go-playground/validator/v10"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
)

func newAMQPTestJWTManager() *jwt.Manager {
	return jwt.New("0123456789abcdef0123456789abcdef", time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)
}

func TestRegisterBadRequest(t *testing.T) {
	t.Parallel()

	handler := (&V1{v: validator.New(validator.WithRequiredStructEnabled())}).register()

	_, err := handler(&amqp.Delivery{Body: []byte(`{"username":"ab","email":"bad","password":"short"}`)})

	require.ErrorIs(t, err, rmqrpc.ErrBadRequest)
}

func TestAuthenticatedHandlerUnauthenticated(t *testing.T) {
	t.Parallel()

	handler := (&V1{j: newAMQPTestJWTManager()}).getHistory()

	_, err := handler(&amqp.Delivery{Body: []byte(`{"token":"invalid","data":{}}`)})

	require.ErrorIs(t, err, rmqrpc.ErrUnauthenticated)
}

func TestAuthEmailVerificationErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler func(*V1) func(*amqp.Delivery) (any, error)
		body    string
		user    *fakeAuthUser
		wantErr error
	}{
		{
			name:    "register delivery unavailable",
			handler: func(v *V1) func(*amqp.Delivery) (any, error) { return v.register() },
			body:    `{"username":"testuser","email":"test@example.com","password":"password123"}`,
			user:    &fakeAuthUser{registerErr: entity.ErrEmailDeliveryFailed},
			wantErr: rmqrpc.ErrUnavailable,
		},
		{
			name:    "login unverified failed precondition",
			handler: func(v *V1) func(*amqp.Delivery) (any, error) { return v.login() },
			body:    `{"email":"test@example.com","password":"password123"}`,
			user:    &fakeAuthUser{loginErr: entity.ErrEmailNotVerified},
			wantErr: rmqrpc.ErrFailedPrecondition,
		},
		{
			name:    "verify invalid token bad request",
			handler: func(v *V1) func(*amqp.Delivery) (any, error) { return v.verifyEmail() },
			body:    `{"token":"invalid"}`,
			user:    &fakeAuthUser{verifyErr: entity.ErrInvalidVerificationToken},
			wantErr: rmqrpc.ErrBadRequest,
		},
		{
			name:    "resend rate limited",
			handler: func(v *V1) func(*amqp.Delivery) (any, error) { return v.resendVerification() },
			body:    `{"email":"test@example.com"}`,
			user:    &fakeAuthUser{resendErr: entity.ErrVerificationRateLimited},
			wantErr: rmqrpc.ErrRateLimited,
		},
		{
			name:    "resend delivery unavailable",
			handler: func(v *V1) func(*amqp.Delivery) (any, error) { return v.resendVerification() },
			body:    `{"email":"test@example.com"}`,
			user:    &fakeAuthUser{resendErr: entity.ErrEmailDeliveryFailed},
			wantErr: rmqrpc.ErrUnavailable,
		},
		{
			name:    "forgot password rate limited",
			handler: func(v *V1) func(*amqp.Delivery) (any, error) { return v.forgotPassword() },
			body:    `{"email":"test@example.com"}`,
			user:    &fakeAuthUser{forgotErr: entity.ErrPasswordResetRateLimited},
			wantErr: rmqrpc.ErrRateLimited,
		},
		{
			name:    "forgot password delivery unavailable",
			handler: func(v *V1) func(*amqp.Delivery) (any, error) { return v.forgotPassword() },
			body:    `{"email":"test@example.com"}`,
			user:    &fakeAuthUser{forgotErr: entity.ErrEmailDeliveryFailed},
			wantErr: rmqrpc.ErrUnavailable,
		},
		{
			name:    "reset password invalid token",
			handler: func(v *V1) func(*amqp.Delivery) (any, error) { return v.resetPassword() },
			body:    `{"token":"invalid","password":"newpassword123"}`,
			user:    &fakeAuthUser{resetErr: entity.ErrInvalidPasswordResetToken},
			wantErr: rmqrpc.ErrBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			controller := &V1{u: tt.user, l: logger.New("error"), v: validator.New(validator.WithRequiredStructEnabled())}
			handler := tt.handler(controller)

			_, err := handler(&amqp.Delivery{Body: []byte(tt.body)})

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

func (f *fakeAuthUser) Login(context.Context, string, string) (entity.LoginResult, error) {
	if f.loginErr != nil {
		return entity.LoginResult{}, f.loginErr
	}

	return entity.LoginResult{AccessToken: "token", RefreshToken: "refresh-token", SessionID: "session-id"}, nil
}

func (f *fakeAuthUser) RefreshSession(context.Context, string) (entity.LoginResult, error) {
	return entity.LoginResult{AccessToken: "token", RefreshToken: "refresh-token", SessionID: "session-id"}, nil
}

func (f *fakeAuthUser) Logout(context.Context, string) error {
	return nil
}

func (f *fakeAuthUser) LogoutAll(context.Context, string) error {
	return nil
}

func (f *fakeAuthUser) GetUser(context.Context, string) (entity.User, error) {
	return entity.User{}, nil
}

func (f *fakeAuthUser) GetUserAccount(context.Context, string) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (f *fakeAuthUser) AdminUsers(context.Context, string, string, *bool, int, int) ([]entity.UserAccount, int, error) {
	return nil, 0, nil
}

func (f *fakeAuthUser) AdminUserActivity(context.Context, string, int, int) ([]entity.UserActivity, int, error) {
	return nil, 0, nil
}

func (f *fakeAuthUser) CompleteOnboarding(
	context.Context,
	string,
	entity.UserOnboarding,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (f *fakeAuthUser) UpdateUserProfile(
	context.Context,
	string,
	entity.UserProfilePatch,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (f *fakeAuthUser) UpdateUserPreferences(
	context.Context,
	string,
	entity.UserPreferencesPatch,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (f *fakeAuthUser) SetRoleByEmail(context.Context, string, string, string, string) (entity.User, error) {
	return entity.User{}, nil
}

func (f *fakeAuthUser) VerifyEmail(context.Context, string, string, string) error {
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

func (f *fakeAuthUser) ChangePassword(context.Context, string, string, string) (entity.LoginResult, error) {
	if f.changeErr != nil {
		return entity.LoginResult{}, f.changeErr
	}

	return entity.LoginResult{AccessToken: "token", RefreshToken: "refresh-token", SessionID: "session-id"}, nil
}

func (f *fakeAuthUser) RequestEmailChange(context.Context, string, string, string) error {
	return nil
}

func (f *fakeAuthUser) VerifyEmailChange(context.Context, string, string, string) (entity.LoginResult, error) {
	return entity.LoginResult{AccessToken: "token", RefreshToken: "refresh-token", SessionID: "session-id"}, nil
}

func (f *fakeAuthUser) DeleteAccount(context.Context, string, string) error {
	return nil
}
