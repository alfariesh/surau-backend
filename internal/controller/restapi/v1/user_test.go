package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
		{
			name:       "request email change rate limited",
			method:     http.MethodPost,
			path:       "/auth/change-email/request",
			body:       `{"current_password":"oldpassword123","new_email":"new@example.com"}`,
			user:       &fakeAuthUser{requestEmailChangeErr: entity.ErrEmailChangeRateLimited},
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "request email change success",
			method:     http.MethodPost,
			path:       "/auth/change-email/request",
			body:       `{"current_password":"oldpassword123","new_email":"new@example.com"}`,
			user:       &fakeAuthUser{},
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "verify email change invalid token",
			method:     http.MethodPost,
			path:       "/auth/change-email/verify",
			body:       `{"token":"invalid"}`,
			user:       &fakeAuthUser{verifyEmailChangeErr: entity.ErrInvalidEmailChangeToken},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "verify email change success",
			method:     http.MethodPost,
			path:       "/auth/change-email/verify",
			body:       `{"token":"valid"}`,
			user:       &fakeAuthUser{},
			wantStatus: http.StatusOK,
		},
		{
			name:       "delete account wrong password",
			method:     http.MethodPost,
			path:       "/auth/delete-account",
			body:       `{"current_password":"oldpassword123"}`,
			user:       &fakeAuthUser{deleteAccountErr: entity.ErrInvalidCredentials},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "delete account success",
			method:     http.MethodPost,
			path:       "/auth/delete-account",
			body:       `{"current_password":"oldpassword123"}`,
			user:       &fakeAuthUser{},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newAuthTestApp(tt.user)
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

func TestAuthSessionRoutes(t *testing.T) {
	t.Parallel()

	t.Run("login returns access and refresh tokens with legacy alias", func(t *testing.T) {
		t.Parallel()

		app := newAuthTestApp(&fakeAuthUser{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login",
			bytes.NewBufferString(`{"email":"test@example.com","password":"password123"}`))
		req.Header.Set("Content-Type", "application/json")

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body := readTestBody(t, resp)
		assert.Contains(t, body, `"token":"token"`)
		assert.Contains(t, body, `"access_token":"token"`)
		assert.Contains(t, body, `"refresh_token":"refresh-token"`)
		assert.Contains(t, body, `"token_type":"Bearer"`)
		assert.Contains(t, body, `"expires_in"`)
	})

	t.Run("refresh returns new token pair", func(t *testing.T) {
		t.Parallel()

		app := newAuthTestApp(&fakeAuthUser{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/refresh",
			bytes.NewBufferString(`{"refresh_token":"some-refresh-token"}`))
		req.Header.Set("Content-Type", "application/json")

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body := readTestBody(t, resp)
		assert.Contains(t, body, `"access_token":"token"`)
		assert.Contains(t, body, `"refresh_token":"refresh-token"`)
	})

	t.Run("refresh invalid token returns 401", func(t *testing.T) {
		t.Parallel()

		app := newAuthTestApp(&fakeAuthUser{refreshErr: entity.ErrInvalidRefreshToken})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/refresh",
			bytes.NewBufferString(`{"refresh_token":"bad-token"}`))
		req.Header.Set("Content-Type", "application/json")

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("refresh rate limited sets Retry-After", func(t *testing.T) {
		t.Parallel()

		app := newAuthTestApp(&fakeAuthUser{
			refreshErr: &entity.AuthRateLimitedError{RetryAfter: 90 * time.Second},
		})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/refresh",
			bytes.NewBufferString(`{"refresh_token":"some-refresh-token"}`))
		req.Header.Set("Content-Type", "application/json")

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
		assert.Equal(t, "90", resp.Header.Get("Retry-After"))
		assert.Contains(t, readTestBody(t, resp), `"retry_after":90`)
	})

	t.Run("logout returns 200", func(t *testing.T) {
		t.Parallel()

		app := newAuthTestApp(&fakeAuthUser{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/logout",
			bytes.NewBufferString(`{"refresh_token":"some-refresh-token"}`))
		req.Header.Set("Content-Type", "application/json")

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, readTestBody(t, resp), `"logged_out":true`)
	})

	t.Run("logout-all returns 200", func(t *testing.T) {
		t.Parallel()

		app := newAuthTestApp(&fakeAuthUser{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/logout-all", http.NoBody)

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, readTestBody(t, resp), `"sessions_revoked":true`)
	})

	t.Run("change password returns fresh token pair", func(t *testing.T) {
		t.Parallel()

		app := newAuthTestApp(&fakeAuthUser{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/change-password",
			bytes.NewBufferString(`{"current_password":"oldpassword123","new_password":"newpassword123"}`))
		req.Header.Set("Content-Type", "application/json")

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body := readTestBody(t, resp)
		assert.Contains(t, body, `"password_changed":true`)
		assert.Contains(t, body, `"access_token":"token"`)
		assert.Contains(t, body, `"refresh_token":"refresh-token"`)
	})

	t.Run("verify email change returns fresh token pair", func(t *testing.T) {
		t.Parallel()

		app := newAuthTestApp(&fakeAuthUser{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/change-email/verify",
			bytes.NewBufferString(`{"token":"valid"}`))
		req.Header.Set("Content-Type", "application/json")

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body := readTestBody(t, resp)
		assert.Contains(t, body, `"email_changed":true`)
		assert.Contains(t, body, `"refresh_token":"refresh-token"`)
	})
}

func TestUserProfileAndPreferenceRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		user       *fakeAuthUser
		wantStatus int
		wantBody   string
	}{
		{
			name:       "profile includes default preferences",
			method:     http.MethodGet,
			path:       "/user/profile",
			user:       &fakeAuthUser{},
			wantStatus: http.StatusOK,
			wantBody:   `"preferred_content_lang":"id"`,
		},
		{
			name:       "onboarding unsupported language",
			method:     http.MethodPatch,
			path:       "/user/onboarding",
			body:       `{"preferred_content_lang":"fr"}`,
			user:       &fakeAuthUser{onboardingErr: entity.ErrUnsupportedLanguage},
			wantStatus: http.StatusBadRequest,
			wantBody:   `"unsupported language"`,
		},
		{
			name:       "onboarding success",
			method:     http.MethodPatch,
			path:       "/user/onboarding",
			body:       `{"preferred_content_lang":"id","arabic_level":"basic","reader_mode":"arabic_translation"}`,
			user:       &fakeAuthUser{},
			wantStatus: http.StatusOK,
			wantBody:   `"onboarding_required":false`,
		},
		{
			name:       "profile update invalid display name",
			method:     http.MethodPatch,
			path:       "/user/profile",
			body:       `{"display_name":" "}`,
			user:       &fakeAuthUser{profileErr: entity.ErrInvalidUserPreference},
			wantStatus: http.StatusBadRequest,
			wantBody:   `"invalid user preference"`,
		},
		{
			name:       "profile update success",
			method:     http.MethodPatch,
			path:       "/user/profile",
			body:       `{"display_name":"Ahmad","timezone":"Asia/Jakarta","country_code":"ID","personalization_enabled":true}`,
			user:       &fakeAuthUser{},
			wantStatus: http.StatusOK,
			wantBody:   `"profile"`,
		},
		{
			name:       "preferences success",
			method:     http.MethodPatch,
			path:       "/user/preferences",
			body:       `{"preferred_content_lang":"en"}`,
			user:       &fakeAuthUser{},
			wantStatus: http.StatusOK,
			wantBody:   `"preferences"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newAuthTestApp(tt.user)
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			if tt.wantBody != "" {
				body := readTestBody(t, resp)
				assert.Contains(t, body, tt.wantBody)
			}
		})
	}
}

func TestProfileIncludesCompleteDefaultPreferences(t *testing.T) {
	t.Parallel()

	app := newAuthTestApp(&fakeAuthUser{})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/user/profile", http.NoBody)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var account entity.UserAccount
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&account))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, entity.UserPreferredLangDefault, account.Preferences.PreferredUILang)
	assert.Equal(t, entity.UserPreferredLangDefault, account.Preferences.PreferredContentLang)
	assert.Equal(t, []string{entity.UserPreferredLangDefault}, account.Preferences.FallbackLangs)
	assert.Equal(t, entity.UserArabicLevelNone, account.Preferences.ArabicLevel)
	assert.Equal(t, entity.UserReaderModeArabicTranslation, account.Preferences.ReaderMode)
	assert.Empty(t, account.Preferences.Interests)
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
	app.Post("/auth/refresh", controller.refreshToken)
	app.Post("/auth/logout", controller.logout)
	app.Post("/auth/logout-all", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.logoutAll(ctx)
	})
	app.Post("/auth/verify-email", controller.verifyEmail)
	app.Post("/auth/resend-verification", controller.resendVerification)
	app.Post("/auth/forgot-password", controller.forgotPassword)
	app.Post("/auth/reset-password", controller.resetPassword)
	app.Post("/auth/change-password", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.changePassword(ctx)
	})
	app.Post("/auth/change-email/request", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.requestEmailChange(ctx)
	})
	app.Post("/auth/change-email/verify", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.verifyEmailChange(ctx)
	})
	app.Post("/auth/delete-account", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.deleteAccount(ctx)
	})
	app.Get("/user/profile", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.profile(ctx)
	})
	app.Patch("/user/profile", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.updateProfile(ctx)
	})
	app.Patch("/user/onboarding", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.updateOnboarding(ctx)
	})
	app.Patch("/user/preferences", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.updatePreferences(ctx)
	})
	app.Get("/auth/sessions", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")
		ctx.Locals("sessionID", "fam-current")

		return controller.listSessions(ctx)
	})
	app.Delete("/auth/sessions/:id", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "user-id-123")

		return controller.revokeSession(ctx)
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
	accountErr  error
	profileErr  error

	onboardingErr         error
	preferencesErr        error
	roleErr               error
	adminUsersErr         error
	adminActivityErr      error
	requestEmailChangeErr error
	verifyEmailChangeErr  error
	deleteAccountErr      error
	refreshErr            error
	logoutErr             error
	logoutAllErr          error
	listSessionsErr       error
	revokeSessionErr      error

	sessions          []entity.AuthSession
	revokeSessionUser string
	revokeSessionID   string
	roleActorID       string
	roleActorEmail    string
	roleEmail         string
	role              string
	roleUser          entity.User
}

func (f *fakeAuthUser) Register(context.Context, string, string, string) (entity.User, error) {
	return entity.User{ID: "user-id-123"}, f.registerErr
}

func (f *fakeAuthUser) Login(context.Context, string, string) (entity.LoginResult, error) {
	if f.loginErr != nil {
		return entity.LoginResult{}, f.loginErr
	}

	return testLoginResult(), nil
}

func (f *fakeAuthUser) RefreshSession(context.Context, string) (entity.LoginResult, error) {
	if f.refreshErr != nil {
		return entity.LoginResult{}, f.refreshErr
	}

	return testLoginResult(), nil
}

func (f *fakeAuthUser) Logout(context.Context, string) error {
	return f.logoutErr
}

func (f *fakeAuthUser) LogoutAll(context.Context, string) error {
	return f.logoutAllErr
}

func (f *fakeAuthUser) ListSessions(_ context.Context, _ string) ([]entity.AuthSession, error) {
	if f.listSessionsErr != nil {
		return nil, f.listSessionsErr
	}

	return f.sessions, nil
}

func (f *fakeAuthUser) RevokeSession(_ context.Context, userID, sessionID string) error {
	f.revokeSessionUser = userID
	f.revokeSessionID = sessionID

	return f.revokeSessionErr
}

func testLoginResult() entity.LoginResult {
	return entity.LoginResult{
		User:             entity.User{ID: "user-id-123"},
		SessionID:        "session-id-123",
		AccessToken:      "token",
		AccessExpiresAt:  time.Now().Add(15 * time.Minute),
		RefreshToken:     "refresh-token",
		RefreshExpiresAt: time.Now().Add(720 * time.Hour),
	}
}

func (f *fakeAuthUser) GetUser(context.Context, string) (entity.User, error) {
	return entity.User{}, nil
}

func (f *fakeAuthUser) GetUserAccount(context.Context, string) (entity.UserAccount, error) {
	if f.accountErr != nil {
		return entity.UserAccount{}, f.accountErr
	}

	return defaultTestAccount(), nil
}

func (f *fakeAuthUser) AdminUsers(
	context.Context,
	string,
	string,
	*bool,
	int,
	int,
) ([]entity.UserAccount, int, error) {
	if f.adminUsersErr != nil {
		return nil, 0, f.adminUsersErr
	}

	account := defaultTestAccount()
	account.ID = "editor-id"
	account.Username = "editor"
	account.Email = "editor@example.com"
	account.Role = entity.UserRoleEditor
	account.EmailVerified = true

	return []entity.UserAccount{account}, 1, nil
}

func (f *fakeAuthUser) AdminUserActivity(
	context.Context,
	string,
	int,
	int,
) ([]entity.UserActivity, int, error) {
	if f.adminActivityErr != nil {
		return nil, 0, f.adminActivityErr
	}

	oldRole := entity.UserRoleUser
	newRole := entity.UserRoleEditor
	actorID := "admin-id"
	actorEmail := "admin@example.com"

	return []entity.UserActivity{{
		ID:         "activity-id",
		UserID:     "editor-id",
		Email:      "editor@example.com",
		Event:      "role_change",
		Status:     "success",
		ActorID:    &actorID,
		ActorEmail: &actorEmail,
		OldRole:    &oldRole,
		NewRole:    &newRole,
		CreatedAt:  timeNowForTest(),
	}}, 1, nil
}

func (f *fakeAuthUser) CompleteOnboarding(
	context.Context,
	string,
	entity.UserOnboarding,
) (entity.UserAccount, error) {
	if f.onboardingErr != nil {
		return entity.UserAccount{}, f.onboardingErr
	}

	account := defaultTestAccount()
	account.OnboardingRequired = false
	completedAt := timeNowForTest()
	account.Profile.OnboardingCompletedAt = &completedAt

	return account, nil
}

func (f *fakeAuthUser) UpdateUserProfile(
	context.Context,
	string,
	entity.UserProfilePatch,
) (entity.UserAccount, error) {
	if f.profileErr != nil {
		return entity.UserAccount{}, f.profileErr
	}

	return defaultTestAccount(), nil
}

func (f *fakeAuthUser) UpdateUserPreferences(
	context.Context,
	string,
	entity.UserPreferencesPatch,
) (entity.UserAccount, error) {
	if f.preferencesErr != nil {
		return entity.UserAccount{}, f.preferencesErr
	}

	return defaultTestAccount(), nil
}

func (f *fakeAuthUser) SetRoleByEmail(_ context.Context, actorID, actorEmail, email, role string) (entity.User, error) {
	f.roleActorID = actorID
	f.roleActorEmail = actorEmail
	f.roleEmail = email
	f.role = role
	if f.roleErr != nil {
		return entity.User{}, f.roleErr
	}

	return f.roleUser, nil
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

	return testLoginResult(), nil
}

func (f *fakeAuthUser) RequestEmailChange(context.Context, string, string, string) error {
	return f.requestEmailChangeErr
}

func (f *fakeAuthUser) VerifyEmailChange(context.Context, string, string, string) (entity.LoginResult, error) {
	if f.verifyEmailChangeErr != nil {
		return entity.LoginResult{}, f.verifyEmailChangeErr
	}

	return testLoginResult(), nil
}

func (f *fakeAuthUser) DeleteAccount(context.Context, string, string) error {
	return f.deleteAccountErr
}

func defaultTestAccount() entity.UserAccount {
	now := timeNowForTest()

	return entity.UserAccount{
		User: entity.User{
			ID:       "user-id-123",
			Username: "testuser",
			Email:    "test@example.com",
		},
		Profile:            entity.DefaultUserProfile("user-id-123", now),
		Preferences:        entity.DefaultUserPreferences("user-id-123", now),
		OnboardingRequired: true,
	}
}

func timeNowForTest() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func readTestBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	body := new(bytes.Buffer)
	_, err := body.ReadFrom(resp.Body)
	require.NoError(t, err)

	return body.String()
}
