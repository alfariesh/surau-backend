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
	app := newAdminRoleTestApp(user, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
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

	return app
}
