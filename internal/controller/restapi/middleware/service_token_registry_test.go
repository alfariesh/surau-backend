package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase"
	pkglogger "github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errServiceIdentityDatabaseUnavailable = errors.New("database unavailable")

func TestScopedServiceAuthenticationAuditsEveryOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		token         string
		wantStatus    int
		wantPrincipal string
		wantOutcome   string
	}{
		{name: "allowed", token: "valid", wantStatus: 200, wantPrincipal: "collab-server", wantOutcome: "allowed"},
		{name: "wrong scope", token: "wrong-scope", wantStatus: 403, wantPrincipal: "rag-eval", wantOutcome: "insufficient_scope"},
		{name: "expired", token: "expired", wantStatus: 401, wantPrincipal: "collab-server", wantOutcome: "expired"},
		{name: "revoked", token: "revoked", wantStatus: 401, wantPrincipal: "collab-server", wantOutcome: "token_revoked"},
		{name: "fake", token: "fake", wantStatus: 401, wantPrincipal: "unattributed", wantOutcome: "invalid"},
		{name: "missing", token: "", wantStatus: 401, wantPrincipal: "unattributed", wantOutcome: "missing"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spy := &serviceIdentityMiddlewareSpy{}
			app := fiber.New()
			app.Use(RequestID())
			app.Get("/internal/test/:id", RequireServicePrincipal(
				spy, entity.ServiceScopeCollabDraftWrite, serviceMiddlewareTestLogger{},
			), func(ctx *fiber.Ctx) error {
				return ctx.SendStatus(http.StatusOK)
			})

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/internal/test/42", nil)
			if test.token != "" {
				request.Header.Set(ServiceTokenHeader, test.token)
			}

			response, err := app.Test(request)
			require.NoError(t, err)

			defer response.Body.Close()

			assert.Equal(t, test.wantStatus, response.StatusCode)
			require.Len(t, spy.audits, 1)
			audit := spy.audits[0]
			assert.Equal(t, test.wantPrincipal, audit.PrincipalName)
			assert.Equal(t, entity.ServiceScopeCollabDraftWrite, *audit.RequiredScope)
			assert.Equal(t, "/internal/test/:id", audit.RouteTemplate)
			assert.NotNil(t, audit.RequestID)

			if test.wantOutcome == entity.ServiceAuthOutcomeAllowed {
				require.Len(t, spy.finished, 1)
				assert.Equal(t, http.StatusOK, spy.finished[0])
			} else {
				assert.Equal(t, test.wantOutcome, audit.AuthOutcome)
				require.NotNil(t, audit.ResponseStatus)
				assert.Equal(t, test.wantStatus, *audit.ResponseStatus)
			}
		})
	}
}

func TestScopedServiceAuthenticationFailsClosedBeforeHandlerWhenAuditFails(t *testing.T) {
	t.Parallel()

	spy := &serviceIdentityMiddlewareSpy{auditErr: errServiceIdentityDatabaseUnavailable}
	handlerCalled := false
	app := fiber.New()
	app.Get("/internal/write", RequireServicePrincipal(
		spy, entity.ServiceScopeCollabDraftWrite, serviceMiddlewareTestLogger{},
	), func(ctx *fiber.Ctx) error {
		handlerCalled = true

		return ctx.SendStatus(http.StatusNoContent)
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/internal/write", nil)
	request.Header.Set(ServiceTokenHeader, "valid")
	response, err := app.Test(request)
	require.NoError(t, err)

	defer response.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	assert.False(t, handlerCalled)
}

func TestScopedServiceAuthenticationAuditsHandlerErrorStatus(t *testing.T) {
	t.Parallel()

	spy := &serviceIdentityMiddlewareSpy{}
	app := fiber.New()
	app.Get("/internal/failure", RequireServicePrincipal(
		spy, entity.ServiceScopeCollabDraftWrite, serviceMiddlewareTestLogger{},
	), func(*fiber.Ctx) error {
		return fiber.ErrBadGateway
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/internal/failure", nil)
	request.Header.Set(ServiceTokenHeader, "valid")
	response, err := app.Test(request)
	require.NoError(t, err)

	defer response.Body.Close()

	assert.Equal(t, http.StatusBadGateway, response.StatusCode)
	require.Equal(t, []int{http.StatusBadGateway}, spy.finished)
}

type serviceMiddlewareTestLogger struct{}

func (serviceMiddlewareTestLogger) Debug(any, ...any)   {}
func (serviceMiddlewareTestLogger) Info(string, ...any) {}
func (serviceMiddlewareTestLogger) Warn(string, ...any) {}
func (serviceMiddlewareTestLogger) Error(any, ...any)   {}
func (serviceMiddlewareTestLogger) Fatal(any, ...any)   {}
func (testLogger serviceMiddlewareTestLogger) WithField(string, any) pkglogger.Interface {
	return testLogger
}

type serviceIdentityMiddlewareSpy struct {
	usecase.ServiceIdentity
	audits   []entity.ServiceRequestAudit
	finished []int
	auditErr error
}

func (spy *serviceIdentityMiddlewareSpy) AuthenticateServiceToken(
	_ context.Context, rawToken, _ string,
) (entity.ServiceAuthentication, error) {
	expires := time.Now().Add(time.Hour)

	switch rawToken {
	case "valid":
		return entity.ServiceAuthentication{
			PrincipalID: "principal", PrincipalName: "collab-server", TokenID: "token",
			Scopes: []string{entity.ServiceScopeCollabDraftWrite}, ExpiresAt: expires,
			Outcome: entity.ServiceAuthOutcomeAllowed,
		}, nil
	case "wrong-scope":
		return entity.ServiceAuthentication{
			PrincipalID: "principal", PrincipalName: "rag-eval", TokenID: "token",
			Outcome: entity.ServiceAuthOutcomeInsufficientScope,
		}, entity.ErrInsufficientServiceScope
	case "expired":
		return entity.ServiceAuthentication{
			PrincipalID: "principal", PrincipalName: "collab-server", TokenID: "token",
			Outcome: entity.ServiceAuthOutcomeExpired,
		}, entity.ErrInvalidServiceToken
	case "revoked":
		return entity.ServiceAuthentication{
			PrincipalID: "principal", PrincipalName: "collab-server", TokenID: "token",
			Outcome: entity.ServiceAuthOutcomeTokenRevoked,
		}, entity.ErrInvalidServiceToken
	case "":
		return entity.ServiceAuthentication{Outcome: entity.ServiceAuthOutcomeMissing}, entity.ErrInvalidServiceToken
	default:
		return entity.ServiceAuthentication{Outcome: entity.ServiceAuthOutcomeInvalid}, entity.ErrInvalidServiceToken
	}
}

//nolint:gocritic // spy stores a copy to assert exactly what crossed the usecase boundary
func (spy *serviceIdentityMiddlewareSpy) CreateServiceRequestAudit(
	_ context.Context, audit entity.ServiceRequestAudit,
) (string, error) {
	if spy.auditErr != nil {
		return "", spy.auditErr
	}

	spy.audits = append(spy.audits, audit)

	return "audit-id", nil
}

func (spy *serviceIdentityMiddlewareSpy) FinishServiceRequestAudit(
	_ context.Context, _ string, status int,
) error {
	spy.finished = append(spy.finished, status)
	if len(spy.audits) > 0 {
		spy.audits[len(spy.audits)-1].AuthOutcome = entity.ServiceAuthOutcomeAllowed
	}

	return nil
}
