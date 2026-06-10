package v1

import (
	"context"
	"testing"

	pb "github.com/evrone/go-clean-template/docs/proto/v1"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubAuthUser struct {
	registerErr error
	loginErr    error
	verifyErr   error
	resendErr   error
	forgotErr   error
	resetErr    error
	changeErr   error
}

func (s stubAuthUser) Register(context.Context, string, string, string) (entity.User, error) {
	return entity.User{}, s.registerErr
}

func (s stubAuthUser) Login(context.Context, string, string) (entity.LoginResult, error) {
	if s.loginErr != nil {
		return entity.LoginResult{}, s.loginErr
	}

	return entity.LoginResult{AccessToken: "token", RefreshToken: "refresh-token", SessionID: "session-id"}, nil
}

func (s stubAuthUser) RefreshSession(context.Context, string) (entity.LoginResult, error) {
	return entity.LoginResult{AccessToken: "token"}, nil
}

func (s stubAuthUser) Logout(context.Context, string) error {
	return nil
}

func (s stubAuthUser) LogoutAll(context.Context, string) error {
	return nil
}

func (s stubAuthUser) ListSessions(context.Context, string) ([]entity.AuthSession, error) {
	return nil, nil
}

func (s stubAuthUser) RevokeSession(context.Context, string, string) error {
	return nil
}

func (s stubAuthUser) GetUser(context.Context, string) (entity.User, error) {
	return entity.User{}, nil
}

func (s stubAuthUser) GetUserAccount(context.Context, string) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (s stubAuthUser) AdminUsers(context.Context, string, string, *bool, int, int) ([]entity.UserAccount, int, error) {
	return nil, 0, nil
}

func (s stubAuthUser) AdminUserActivity(context.Context, string, int, int) ([]entity.UserActivity, int, error) {
	return nil, 0, nil
}

func (s stubAuthUser) CompleteOnboarding(
	context.Context,
	string,
	entity.UserOnboarding,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (s stubAuthUser) UpdateUserProfile(
	context.Context,
	string,
	entity.UserProfilePatch,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (s stubAuthUser) UpdateUserPreferences(
	context.Context,
	string,
	entity.UserPreferencesPatch,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (s stubAuthUser) SetRoleByEmail(context.Context, string, string, string, string) (entity.User, error) {
	return entity.User{}, nil
}

func (s stubAuthUser) VerifyEmail(context.Context, string, string, string) error {
	return s.verifyErr
}

func (s stubAuthUser) ResendEmailVerification(context.Context, string) error {
	return s.resendErr
}

func (s stubAuthUser) ForgotPassword(context.Context, string) error {
	return s.forgotErr
}

func (s stubAuthUser) ResetPassword(context.Context, string, string) error {
	return s.resetErr
}

func (s stubAuthUser) ChangePassword(context.Context, string, string, string) (entity.LoginResult, error) {
	if s.changeErr != nil {
		return entity.LoginResult{}, s.changeErr
	}

	return entity.LoginResult{AccessToken: "token"}, nil
}

func (s stubAuthUser) RequestEmailChange(context.Context, string, string, string) error {
	return nil
}

func (s stubAuthUser) VerifyEmailChange(context.Context, string, string, string) (entity.LoginResult, error) {
	return entity.LoginResult{AccessToken: "token"}, nil
}

func (s stubAuthUser) DeleteAccount(context.Context, string, string) error {
	return nil
}

type noopLogger struct{}

func (noopLogger) Debug(any, ...any)   {}
func (noopLogger) Info(string, ...any) {}
func (noopLogger) Warn(string, ...any) {}
func (noopLogger) Error(any, ...any)   {}
func (noopLogger) Fatal(any, ...any)   {}

func TestAuthController_RegisterInvalidInput(t *testing.T) {
	t.Parallel()

	controller := &AuthController{u: stubAuthUser{registerErr: entity.ErrInvalidAuthInput}, l: noopLogger{}}

	resp, err := controller.Register(t.Context(), &pb.RegisterRequest{})

	assert.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAuthController_LoginInvalidInput(t *testing.T) {
	t.Parallel()

	controller := &AuthController{u: stubAuthUser{loginErr: entity.ErrInvalidAuthInput}, l: noopLogger{}}

	resp, err := controller.Login(t.Context(), &pb.LoginRequest{})

	assert.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAuthController_LoginEmailNotVerified(t *testing.T) {
	t.Parallel()

	controller := &AuthController{u: stubAuthUser{loginErr: entity.ErrEmailNotVerified}, l: noopLogger{}}

	resp, err := controller.Login(t.Context(), &pb.LoginRequest{})

	assert.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestAuthController_VerifyEmailInvalidToken(t *testing.T) {
	t.Parallel()

	controller := &AuthController{u: stubAuthUser{verifyErr: entity.ErrInvalidVerificationToken}, l: noopLogger{}}

	resp, err := controller.VerifyEmail(t.Context(), &pb.VerifyEmailRequest{})

	assert.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAuthController_ResendVerificationRateLimited(t *testing.T) {
	t.Parallel()

	controller := &AuthController{u: stubAuthUser{resendErr: entity.ErrVerificationRateLimited}, l: noopLogger{}}

	resp, err := controller.ResendEmailVerification(t.Context(), &pb.ResendEmailVerificationRequest{})

	assert.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
}

func TestAuthController_ForgotPasswordRateLimited(t *testing.T) {
	t.Parallel()

	controller := &AuthController{u: stubAuthUser{forgotErr: entity.ErrPasswordResetRateLimited}, l: noopLogger{}}

	resp, err := controller.ForgotPassword(t.Context(), &pb.ForgotPasswordRequest{})

	assert.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
}

func TestAuthController_ResetPasswordInvalidToken(t *testing.T) {
	t.Parallel()

	controller := &AuthController{u: stubAuthUser{resetErr: entity.ErrInvalidPasswordResetToken}, l: noopLogger{}}

	resp, err := controller.ResetPassword(t.Context(), &pb.ResetPasswordRequest{})

	assert.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}
