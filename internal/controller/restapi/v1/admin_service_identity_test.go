package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errAdminServiceIdentityDatabase = errors.New("database failed")

func TestAdminIssueServiceTokenRequiresIfMatch(t *testing.T) {
	t.Parallel()

	identity := &adminServiceIdentitySpy{}
	app := newAdminServiceIdentityTestApp(identity)
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/v1/admin/service-identities/principal/tokens", nil,
	)
	response, err := app.Test(request)
	require.NoError(t, err)

	defer response.Body.Close()

	assert.Equal(t, http.StatusPreconditionRequired, response.StatusCode)
	assert.Zero(t, identity.issueCalls)
}

func TestAdminIssueServiceTokenReturnsRawOnceWithoutCaching(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 15, 10, 11, 12, 0, time.UTC)
	identity := &adminServiceIdentitySpy{issueResult: entity.ServiceTokenIssueResult{ // #nosec G101 -- one-time fake token fixture
		Principal: entity.ServicePrincipal{ID: "principal", UpdatedAt: updatedAt},
		Token: entity.IssuedServiceToken{ // #nosec G101 -- one-time fake token fixture
			ServiceToken: entity.ServiceToken{ID: "token", PrincipalID: "principal"},
			Token:        "surau_st_token.one-time-secret",
		},
	}}
	app := newAdminServiceIdentityTestApp(identity)
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/v1/admin/service-identities/principal/tokens", nil,
	)
	request.Header.Set(fiber.HeaderIfMatch, "*")
	response, err := app.Test(request)
	require.NoError(t, err)

	defer response.Body.Close()

	assert.Equal(t, http.StatusCreated, response.StatusCode)
	assert.Equal(t, "no-store", response.Header.Get(fiber.HeaderCacheControl))
	assert.Equal(t, updatedAtETag(updatedAt), response.Header.Get(fiber.HeaderETag))
	assert.Equal(t, 1, identity.issueCalls)
	assert.True(t, identity.issueForce)
	assert.Equal(t, "admin-id", identity.issueActorID)

	var body entity.ServiceTokenIssueResult
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	assert.Equal(t, "surau_st_token.one-time-secret", body.Token.Token)
}

//nolint:paralleltest,tparallel // subtests intentionally share one call-counting spy
func TestAdminServiceIdentityCRUDHandlers(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 15, 10, 11, 12, 0, time.UTC)
	principal := entity.ServicePrincipal{
		ID: "principal-id", PrincipalName: "collab-server", Description: "draft bridge",
		Scopes: []string{entity.ServiceScopeCollabDraftWrite}, UpdatedAt: updatedAt,
	}
	identity := &adminServiceIdentitySpy{
		principal: principal,
		issueResult: entity.ServiceTokenIssueResult{
			Principal: principal,
			Token: entity.IssuedServiceToken{ // #nosec G101 -- fake one-time controller fixture
				ServiceToken: entity.ServiceToken{ID: "token-id", PrincipalID: principal.ID},
				Token:        "surau_st_token.one-time-secret",
			},
		},
	}
	app := newAdminServiceIdentityCRUDTestApp(identity, true)

	tests := []struct {
		name, method, path, body, ifMatch string
		wantStatus                        int
	}{
		{name: "list", method: http.MethodGet, path: "/v1/admin/service-identities?limit=2&offset=3", wantStatus: http.StatusOK},
		{name: "create", method: http.MethodPost, path: "/v1/admin/service-identities", body: `{"principal_name":"collab-server","description":"draft bridge","scopes":["collab:draft:write"]}`, wantStatus: http.StatusCreated},
		{name: "get", method: http.MethodGet, path: "/v1/admin/service-identities/principal-id", wantStatus: http.StatusOK},
		{name: "update", method: http.MethodPatch, path: "/v1/admin/service-identities/principal-id", body: `{"description":"rotated","scopes":["collab:draft:write"]}`, ifMatch: "*", wantStatus: http.StatusOK},
		{name: "issue", method: http.MethodPost, path: "/v1/admin/service-identities/principal-id/tokens", body: `{"expires_at":"2026-08-01T00:00:00Z"}`, ifMatch: "*", wantStatus: http.StatusCreated},
		{name: "revoke token", method: http.MethodPost, path: "/v1/admin/service-identities/principal-id/tokens/token-id/revoke", ifMatch: "*", wantStatus: http.StatusOK},
		{name: "revoke principal", method: http.MethodPost, path: "/v1/admin/service-identities/principal-id/revoke", ifMatch: "*", wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(
				t.Context(), test.method, test.path, strings.NewReader(test.body),
			)
			request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

			if test.ifMatch != "" {
				request.Header.Set(fiber.HeaderIfMatch, test.ifMatch)
			}

			response, err := app.Test(request)
			require.NoError(t, err)

			defer response.Body.Close()

			assert.Equal(t, test.wantStatus, response.StatusCode)
		})
	}

	assert.Equal(t, 2, identity.listLimit)
	assert.Equal(t, 3, identity.listOffset)
	assert.Equal(t, 1, identity.createCalls)
	assert.Equal(t, 1, identity.getCalls)
	assert.Equal(t, 1, identity.updateCalls)
	assert.Equal(t, 1, identity.issueCalls)
	assert.Equal(t, 1, identity.revokeTokenCalls)
	assert.Equal(t, 1, identity.revokePrincipalCalls)
}

//nolint:paralleltest,tparallel // boundary subtests intentionally share one spy
func TestAdminServiceIdentityRequestAndErrorBoundaries(t *testing.T) {
	t.Parallel()

	identity := &adminServiceIdentitySpy{principal: entity.ServicePrincipal{UpdatedAt: time.Now().UTC()}}
	unauthenticated := newAdminServiceIdentityCRUDTestApp(identity, false)
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/v1/admin/service-identities",
		strings.NewReader(`{"principal_name":"collab-server","scopes":["collab:draft:write"]}`),
	)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := unauthenticated.Test(request)
	require.NoError(t, err)
	response.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)

	app := newAdminServiceIdentityCRUDTestApp(identity, true)

	for _, test := range []struct {
		name, method, path, body, ifMatch string
		wantStatus                        int
	}{
		{name: "invalid create body", method: http.MethodPost, path: "/v1/admin/service-identities", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "invalid update body", method: http.MethodPatch, path: "/v1/admin/service-identities/id", body: `{`, ifMatch: "*", wantStatus: http.StatusBadRequest},
		{name: "invalid issue body", method: http.MethodPost, path: "/v1/admin/service-identities/id/tokens", body: `{`, ifMatch: "*", wantStatus: http.StatusBadRequest},
		{name: "invalid etag", method: http.MethodPatch, path: "/v1/admin/service-identities/id", body: `{"description":"ok","scopes":["collab:draft:write"]}`, ifMatch: "invalid", wantStatus: http.StatusPreconditionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), test.method, test.path, strings.NewReader(test.body))
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

			if test.ifMatch != "" {
				req.Header.Set(fiber.HeaderIfMatch, test.ifMatch)
			}

			resp, requestErr := app.Test(req)
			require.NoError(t, requestErr)

			resp.Body.Close()

			assert.Equal(t, test.wantStatus, resp.StatusCode)
		})
	}

	errorCases := []struct {
		err        error
		wantStatus int
	}{
		{entity.ErrServicePrincipalNotFound, http.StatusNotFound},
		{entity.ErrServiceTokenNotFound, http.StatusNotFound},
		{entity.ErrInvalidServicePrincipal, http.StatusBadRequest},
		{entity.ErrInvalidServiceScope, http.StatusBadRequest},
		{entity.ErrServicePrincipalRevoked, http.StatusConflict},
		{entity.ErrPreconditionRequired, http.StatusPreconditionRequired},
		{entity.ErrPreconditionFailed, http.StatusPreconditionFailed},
		{errAdminServiceIdentityDatabase, http.StatusInternalServerError},
	}

	for _, test := range errorCases {
		controller := &V1{l: logger.New("error")}
		errorApp := fiber.New()
		errorApp.Get("/error", func(ctx *fiber.Ctx) error { return controller.serviceIdentityError(ctx, test.err) })
		resp, requestErr := errorApp.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/error", nil))
		require.NoError(t, requestErr)
		resp.Body.Close()
		assert.Equal(t, test.wantStatus, resp.StatusCode)
	}
}

func newAdminServiceIdentityTestApp(identity usecase.ServiceIdentity) *fiber.App {
	app := fiber.New()
	controller := &V1{
		serviceIdentity: identity,
		l:               logger.New("error"),
		v:               validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Post("/v1/admin/service-identities/:id/tokens", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "admin-id")

		return ctx.Next()
	}, controller.adminIssueServiceToken)

	return app
}

func newAdminServiceIdentityCRUDTestApp(identity usecase.ServiceIdentity, actor bool) *fiber.App {
	app := fiber.New()
	controller := &V1{
		serviceIdentity: identity,
		l:               logger.New("error"),
		v:               validator.New(validator.WithRequiredStructEnabled()),
	}

	if actor {
		app.Use(func(ctx *fiber.Ctx) error {
			ctx.Locals("userID", "admin-id")

			return ctx.Next()
		})
	}

	app.Get("/v1/admin/service-identities", controller.adminServiceIdentities)
	app.Post("/v1/admin/service-identities", controller.adminCreateServiceIdentity)
	app.Get("/v1/admin/service-identities/:id", controller.adminServiceIdentity)
	app.Patch("/v1/admin/service-identities/:id", controller.adminUpdateServiceIdentity)
	app.Post("/v1/admin/service-identities/:id/tokens", controller.adminIssueServiceToken)
	app.Post("/v1/admin/service-identities/:id/tokens/:token_id/revoke", controller.adminRevokeServiceToken)
	app.Post("/v1/admin/service-identities/:id/revoke", controller.adminRevokeServiceIdentity)

	return app
}

type adminServiceIdentitySpy struct {
	usecase.ServiceIdentity
	principal            entity.ServicePrincipal
	issueResult          entity.ServiceTokenIssueResult
	listLimit            int
	listOffset           int
	createCalls          int
	getCalls             int
	updateCalls          int
	issueCalls           int
	revokeTokenCalls     int
	revokePrincipalCalls int
	issueActorID         string
	issueForce           bool
}

func (spy *adminServiceIdentitySpy) CreateServicePrincipal(
	_ context.Context, _, _, _ string, _ []string,
) (entity.ServicePrincipal, error) {
	spy.createCalls++

	return spy.principal, nil
}

func (spy *adminServiceIdentitySpy) ListServicePrincipals(
	_ context.Context, limit, offset int,
) ([]entity.ServicePrincipal, int, error) {
	spy.listLimit = limit
	spy.listOffset = offset

	return []entity.ServicePrincipal{spy.principal}, 1, nil
}

func (spy *adminServiceIdentitySpy) GetServicePrincipal(
	_ context.Context, _ string,
) (entity.ServicePrincipal, error) {
	spy.getCalls++

	return spy.principal, nil
}

func (spy *adminServiceIdentitySpy) UpdateServicePrincipal(
	_ context.Context, _, _, _ string, _ []string, _ *time.Time, _ bool,
) (entity.ServicePrincipal, error) {
	spy.updateCalls++

	return spy.principal, nil
}

func (spy *adminServiceIdentitySpy) IssueServiceToken(
	_ context.Context,
	actorID, _ string,
	_ *time.Time,
	_ *time.Time,
	force bool,
) (entity.ServiceTokenIssueResult, error) {
	spy.issueCalls++
	spy.issueActorID = actorID
	spy.issueForce = force

	return spy.issueResult, nil
}

func (spy *adminServiceIdentitySpy) RevokeServiceToken(
	_ context.Context, _, _, _ string, _ *time.Time, _ bool,
) (entity.ServicePrincipal, error) {
	spy.revokeTokenCalls++

	return spy.principal, nil
}

func (spy *adminServiceIdentitySpy) RevokeServicePrincipal(
	_ context.Context, _, _ string, _ *time.Time, _ bool,
) (entity.ServicePrincipal, error) {
	spy.revokePrincipalCalls++

	return spy.principal, nil
}
