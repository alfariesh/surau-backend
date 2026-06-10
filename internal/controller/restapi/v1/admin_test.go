package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/evrone/go-clean-template/internal/controller/restapi/middleware"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminSetUserRole(t *testing.T) {
	t.Parallel()

	user := &fakeAuthUser{
		roleUser: entity.User{
			ID:       "user-id-123",
			Username: "editor",
			Email:    "editor@example.com",
			Role:     entity.UserRoleEditor,
		},
	}
	app := newAdminRoleTestApp(user, entity.User{
		ID:    "admin-id",
		Email: "admin@example.com",
		Role:  entity.UserRoleAdmin,
	})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/v1/admin/users/role",
		strings.NewReader(`{"email":"editor@example.com","role":"editor"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "admin-id", user.roleActorID)
	assert.Equal(t, "admin@example.com", user.roleActorEmail)
	assert.Equal(t, "editor@example.com", user.roleEmail)
	assert.Equal(t, entity.UserRoleEditor, user.role)
}

func TestAdminSetUserRoleRejectsEditorActor(t *testing.T) {
	t.Parallel()

	app := newAdminRoleTestApp(&fakeAuthUser{}, entity.User{ID: "editor-id", Role: entity.UserRoleEditor})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/v1/admin/users/role",
		strings.NewReader(`{"email":"user@example.com","role":"admin"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestAdminSetUserRoleMapsInvalidRole(t *testing.T) {
	t.Parallel()

	user := &fakeAuthUser{roleErr: entity.ErrInvalidRole}
	app := newAdminRoleTestApp(user, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/v1/admin/users/role",
		strings.NewReader(`{"email":"user@example.com","role":"owner"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdminSetUserRoleMapsSelfRoleChange(t *testing.T) {
	t.Parallel()

	user := &fakeAuthUser{roleErr: entity.ErrSelfRoleChange}
	app := newAdminRoleTestApp(user, entity.User{ID: "admin-id", Email: "admin@example.com", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/v1/admin/users/role",
		strings.NewReader(`{"email":"admin@example.com","role":"user"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, readTestBody(t, resp), "cannot change own role")
}

func TestAdminSetUserRoleMapsLastAdmin(t *testing.T) {
	t.Parallel()

	user := &fakeAuthUser{roleErr: entity.ErrLastAdmin}
	app := newAdminRoleTestApp(user, entity.User{ID: "admin-id", Email: "admin@example.com", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/v1/admin/users/role",
		strings.NewReader(`{"email":"other-admin@example.com","role":"user"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, readTestBody(t, resp), "cannot demote the last admin")
}

func TestAdminUsersListSupportsEditorLookup(t *testing.T) {
	t.Parallel()

	app := newAdminRoleTestApp(&fakeAuthUser{}, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/admin/users?role=editor&email_verified=true",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := readTestBody(t, resp)
	assert.Contains(t, body, `"users"`)
	assert.Contains(t, body, `"total":1`)
	assert.Contains(t, body, `"role":"editor"`)
}

func TestAdminUserDetail(t *testing.T) {
	t.Parallel()

	app := newAdminRoleTestApp(&fakeAuthUser{}, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/admin/users/user-id-123", nil)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := readTestBody(t, resp)
	assert.Contains(t, body, `"id":"user-id-123"`)
	assert.Contains(t, body, `"profile"`)
}

func TestAdminUserActivityReturnsRoleAudit(t *testing.T) {
	t.Parallel()

	app := newAdminRoleTestApp(&fakeAuthUser{}, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/admin/users/editor-id/activity", nil)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := readTestBody(t, resp)
	assert.Contains(t, body, `"activity"`)
	assert.Contains(t, body, `"event":"role_change"`)
	assert.Contains(t, body, `"actor_id":"admin-id"`)
	assert.Contains(t, body, `"old_role":"user"`)
	assert.Contains(t, body, `"new_role":"editor"`)
}

func newAdminRoleTestApp(user *fakeAuthUser, actor entity.User) *fiber.App {
	app := fiber.New()
	controller := &V1{
		u: user,
		l: logger.New("error"),
		v: validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Patch(
		"/v1/admin/users/role",
		func(ctx *fiber.Ctx) error {
			ctx.Locals("user", actor)

			return ctx.Next()
		},
		middleware.RequireRoles(user, entity.UserRoleAdmin),
		controller.adminSetUserRole,
	)
	adminUserHandler := func(handler fiber.Handler) fiber.Handler {
		return func(ctx *fiber.Ctx) error {
			ctx.Locals("user", actor)

			return handler(ctx)
		}
	}
	app.Get(
		"/v1/admin/users",
		adminUserHandler(middleware.RequireRoles(user, entity.UserRoleAdmin)),
		controller.adminUsers,
	)
	app.Get(
		"/v1/admin/users/:id/activity",
		adminUserHandler(middleware.RequireRoles(user, entity.UserRoleAdmin)),
		controller.adminUserActivity,
	)
	app.Get(
		"/v1/admin/users/:id",
		adminUserHandler(middleware.RequireRoles(user, entity.UserRoleAdmin)),
		controller.adminUserDetail,
	)

	return app
}
