package middleware_test

import (
	"context"
	"testing"
	"time"

	grpcmw "github.com/evrone/go-clean-template/internal/controller/grpc/middleware"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const respOK = "ok"

type ctxCapture struct {
	ctx context.Context
}

type stubAuthUserUseCase struct{}

func (stubAuthUserUseCase) Register(context.Context, string, string, string) (entity.User, error) {
	return entity.User{}, nil
}

func (stubAuthUserUseCase) Login(context.Context, string, string) (entity.LoginResult, error) {
	return entity.LoginResult{}, nil
}

func (stubAuthUserUseCase) RefreshSession(context.Context, string) (entity.LoginResult, error) {
	return entity.LoginResult{}, nil
}

func (stubAuthUserUseCase) Logout(context.Context, string) error {
	return nil
}

func (stubAuthUserUseCase) LogoutAll(context.Context, string) error {
	return nil
}

func (stubAuthUserUseCase) GetUser(_ context.Context, userID string) (entity.User, error) {
	return entity.User{ID: userID}, nil
}

func (stubAuthUserUseCase) GetUserAccount(context.Context, string) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (stubAuthUserUseCase) AdminUsers(context.Context, string, string, *bool, int, int) ([]entity.UserAccount, int, error) {
	return nil, 0, nil
}

func (stubAuthUserUseCase) AdminUserActivity(context.Context, string, int, int) ([]entity.UserActivity, int, error) {
	return nil, 0, nil
}

func (stubAuthUserUseCase) CompleteOnboarding(
	context.Context,
	string,
	entity.UserOnboarding,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (stubAuthUserUseCase) UpdateUserProfile(
	context.Context,
	string,
	entity.UserProfilePatch,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (stubAuthUserUseCase) UpdateUserPreferences(
	context.Context,
	string,
	entity.UserPreferencesPatch,
) (entity.UserAccount, error) {
	return entity.UserAccount{}, nil
}

func (stubAuthUserUseCase) SetRoleByEmail(context.Context, string, string, string, string) (entity.User, error) {
	return entity.User{}, nil
}

func (stubAuthUserUseCase) VerifyEmail(context.Context, string, string, string) error {
	return nil
}

func (stubAuthUserUseCase) ResendEmailVerification(context.Context, string) error {
	return nil
}

func (stubAuthUserUseCase) ForgotPassword(context.Context, string) error {
	return nil
}

func (stubAuthUserUseCase) ResetPassword(context.Context, string, string) error {
	return nil
}

func (stubAuthUserUseCase) ChangePassword(context.Context, string, string, string) (entity.LoginResult, error) {
	return entity.LoginResult{}, nil
}

func (stubAuthUserUseCase) RequestEmailChange(context.Context, string, string, string) error {
	return nil
}

func (stubAuthUserUseCase) VerifyEmailChange(context.Context, string, string, string) (entity.LoginResult, error) {
	return entity.LoginResult{}, nil
}

func (stubAuthUserUseCase) DeleteAccount(context.Context, string, string) error {
	return nil
}

func (c *ctxCapture) handler(_ context.Context, _ any) (any, error) {
	return respOK, nil
}

func (c *ctxCapture) capturingHandler(ctx context.Context, _ any) (any, error) {
	c.ctx = ctx

	return respOK, nil
}

func newJWTManager(t *testing.T) *jwt.Manager {
	t.Helper()

	return jwt.New("0123456789abcdef0123456789abcdef", time.Hour, jwt.DefaultIssuer, jwt.DefaultAudience)
}

func runSkipAuthTest(t *testing.T, method string) {
	t.Helper()

	jwtMgr := newJWTManager(t)
	interceptor := grpcmw.AuthInterceptor(jwtMgr, stubAuthUserUseCase{})
	info := &grpc.UnaryServerInfo{FullMethod: method}

	called := false
	handler := func(_ context.Context, _ any) (any, error) {
		called = true

		return respOK, nil
	}

	resp, err := interceptor(t.Context(), nil, info, handler)

	require.NoError(t, err)
	assert.Equal(t, respOK, resp)
	assert.True(t, called)
}

func TestAuthInterceptor_SkipRegister(t *testing.T) {
	t.Parallel()
	runSkipAuthTest(t, "/grpc.v1.AuthService/Register")
}

func TestAuthInterceptor_SkipLogin(t *testing.T) {
	t.Parallel()
	runSkipAuthTest(t, "/grpc.v1.AuthService/Login")
}

func TestAuthInterceptor_MissingMetadata(t *testing.T) {
	t.Parallel()

	jwtMgr := newJWTManager(t)
	interceptor := grpcmw.AuthInterceptor(jwtMgr, stubAuthUserUseCase{})
	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.v1.TaskService/GetTask"}

	capture := &ctxCapture{}

	resp, err := interceptor(t.Context(), nil, info, capture.handler)

	assert.Nil(t, resp)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
	assert.Contains(t, st.Message(), "missing metadata")
}

func TestAuthInterceptor_MissingAuthorizationToken(t *testing.T) {
	t.Parallel()

	jwtMgr := newJWTManager(t)
	interceptor := grpcmw.AuthInterceptor(jwtMgr, stubAuthUserUseCase{})
	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.v1.TaskService/GetTask"}

	md := metadata.New(map[string]string{"other-key": "value"})
	ctx := metadata.NewIncomingContext(t.Context(), md)

	capture := &ctxCapture{}

	resp, err := interceptor(ctx, nil, info, capture.handler)

	assert.Nil(t, resp)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
	assert.Contains(t, st.Message(), "missing authorization token")
}

func TestAuthInterceptor_InvalidToken(t *testing.T) {
	t.Parallel()

	jwtMgr := newJWTManager(t)
	interceptor := grpcmw.AuthInterceptor(jwtMgr, stubAuthUserUseCase{})
	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.v1.TaskService/GetTask"}

	md := metadata.Pairs("authorization", "invalid-token")
	ctx := metadata.NewIncomingContext(t.Context(), md)

	capture := &ctxCapture{}

	resp, err := interceptor(ctx, nil, info, capture.handler)

	assert.Nil(t, resp)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
	assert.Contains(t, st.Message(), "invalid or expired token")
}

func TestAuthInterceptor_ValidToken(t *testing.T) {
	t.Parallel()

	jwtMgr := newJWTManager(t)
	interceptor := grpcmw.AuthInterceptor(jwtMgr, stubAuthUserUseCase{})
	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.v1.TaskService/GetTask"}

	token, err := jwtMgr.GenerateToken("user-id-123")
	require.NoError(t, err)

	md := metadata.Pairs("authorization", token)
	ctx := metadata.NewIncomingContext(t.Context(), md)

	capture := &ctxCapture{}

	resp, err := interceptor(ctx, nil, info, capture.capturingHandler)

	require.NoError(t, err)
	assert.Equal(t, respOK, resp)

	userID, ok := grpcmw.UserIDFromContext(capture.ctx)
	assert.True(t, ok)
	assert.Equal(t, "user-id-123", userID)
}

func TestAuthInterceptor_ValidBearerToken(t *testing.T) {
	t.Parallel()

	jwtMgr := newJWTManager(t)
	interceptor := grpcmw.AuthInterceptor(jwtMgr, stubAuthUserUseCase{})
	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.v1.TaskService/GetTask"}

	token, err := jwtMgr.GenerateToken("user-id-123")
	require.NoError(t, err)

	md := metadata.Pairs("authorization", "Bearer "+token)
	ctx := metadata.NewIncomingContext(t.Context(), md)

	capture := &ctxCapture{}

	resp, err := interceptor(ctx, nil, info, capture.capturingHandler)

	require.NoError(t, err)
	assert.Equal(t, respOK, resp)

	userID, ok := grpcmw.UserIDFromContext(capture.ctx)
	assert.True(t, ok)
	assert.Equal(t, "user-id-123", userID)
}

func TestUserIDFromContext_WithValue(t *testing.T) {
	t.Parallel()

	jwtMgr := newJWTManager(t)
	interceptor := grpcmw.AuthInterceptor(jwtMgr, stubAuthUserUseCase{})
	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.v1.TaskService/GetTask"}

	token, err := jwtMgr.GenerateToken("user-42")
	require.NoError(t, err)

	md := metadata.Pairs("authorization", token)
	ctx := metadata.NewIncomingContext(t.Context(), md)

	capture := &ctxCapture{}

	_, err = interceptor(ctx, nil, info, capture.capturingHandler)
	require.NoError(t, err)

	userID, ok := grpcmw.UserIDFromContext(capture.ctx)
	assert.True(t, ok)
	assert.Equal(t, "user-42", userID)
}

func TestUserIDFromContext_WithoutValue(t *testing.T) {
	t.Parallel()

	userID, ok := grpcmw.UserIDFromContext(t.Context())
	assert.False(t, ok)
	assert.Empty(t, userID)
}
