package middleware

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel/trace"
)

const (
	// ServiceTokenHeader remains stable so consumers can rotate credentials
	// without changing their HTTP integration.
	ServiceTokenHeader = "X-Internal-Token"

	serviceAuthenticationKey = "serviceAuthentication"
)

// ServiceAuthentication returns the verified machine identity attached to the
// current request. Handlers must not parse or trust the token themselves.
func ServiceAuthentication(ctx *fiber.Ctx) (entity.ServiceAuthentication, bool) {
	auth, ok := ctx.Locals(serviceAuthenticationKey).(entity.ServiceAuthentication)

	return auth, ok
}

// RequireServicePrincipal authenticates and audits an internal request before
// its business handler runs. Authentication always reaches the DB, so token or
// principal revocation takes effect in an already-running process.
func RequireServicePrincipal(
	identities usecase.ServiceIdentity,
	requiredScope string,
	l logger.Interface,
) fiber.Handler {
	return servicePrincipalMiddleware(identities, requiredScope, false, l)
}

// OptionalServicePrincipal preserves a public endpoint when the header is
// absent. Once supplied, however, the credential is mandatory-valid, scoped,
// and audited exactly like an internal request.
func OptionalServicePrincipal(
	identities usecase.ServiceIdentity,
	requiredScope string,
	l logger.Interface,
) fiber.Handler {
	return servicePrincipalMiddleware(identities, requiredScope, true, l)
}

func servicePrincipalMiddleware(
	identities usecase.ServiceIdentity,
	requiredScope string,
	optional bool,
	l logger.Interface,
) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		rawToken := strings.TrimSpace(ctx.Get(ServiceTokenHeader))
		if optional && rawToken == "" {
			return ctx.Next()
		}

		if identities == nil {
			return middlewareError(ctx, http.StatusServiceUnavailable, "service identity unavailable")
		}

		auth, authErr := identities.AuthenticateServiceToken(ctx.UserContext(), rawToken, requiredScope)
		audit := newServiceRequestAudit(ctx, &auth, requiredScope)

		if authErr != nil {
			status, message := serviceAuthenticationError(authErr)
			audit.AuthOutcome = auth.Outcome
			audit.ResponseStatus = &status
			finishedAt := time.Now().UTC()

			audit.FinishedAt = &finishedAt
			if _, err := identities.CreateServiceRequestAudit(ctx.UserContext(), audit); err != nil {
				RequestLogger(ctx, l).Error(err, "service identity denied-request audit")

				return middlewareError(ctx, http.StatusServiceUnavailable, "service identity unavailable")
			}

			return middlewareError(ctx, status, message)
		}

		auditID, err := identities.CreateServiceRequestAudit(ctx.UserContext(), audit)
		if err != nil {
			RequestLogger(ctx, l).Error(err, "service identity pre-handler audit")

			return middlewareError(ctx, http.StatusServiceUnavailable, "service identity unavailable")
		}

		ctx.Locals(serviceAuthenticationKey, auth)
		ctx.Locals(requestLoggerKey, RequestLogger(ctx, l).WithField("service_principal", auth.PrincipalName))
		handlerErr := ctx.Next()

		status := serviceResponseStatus(ctx, handlerErr)
		if finishErr := identities.FinishServiceRequestAudit(ctx.UserContext(), auditID, status); finishErr != nil {
			RequestLogger(ctx, l).Error(finishErr, "service identity finish request audit")
		}

		return handlerErr
	}
}

func newServiceRequestAudit(
	ctx *fiber.Ctx,
	auth *entity.ServiceAuthentication,
	requiredScope string,
) entity.ServiceRequestAudit {
	principalName := auth.PrincipalName
	if principalName == "" {
		principalName = entity.ServicePrincipalUnattributed
	}

	audit := entity.ServiceRequestAudit{
		PrincipalName: principalName,
		RequiredScope: stringPointer(requiredScope),
		Method:        ctx.Method(),
		RouteTemplate: ctx.Route().Path,
		RequestID:     serviceRequestID(ctx),
		ClientIP:      stringPointer(ctx.IP()),
		AuthOutcome:   entity.ServiceAuthOutcomeStarted,
		StartedAt:     time.Now().UTC(),
	}

	if audit.RouteTemplate == "" {
		audit.RouteTemplate = ctx.Path()
	}

	if auth.PrincipalID != "" {
		audit.PrincipalID = &auth.PrincipalID
	}

	if auth.TokenID != "" {
		audit.TokenID = &auth.TokenID
	}

	if spanContext := trace.SpanFromContext(ctx.UserContext()).SpanContext(); spanContext.HasTraceID() {
		traceID := spanContext.TraceID().String()
		audit.TraceID = &traceID
	}

	return audit
}

func serviceAuthenticationError(err error) (status int, message string) {
	switch {
	case errors.Is(err, entity.ErrInsufficientServiceScope):
		return http.StatusForbidden, "insufficient service scope"
	case errors.Is(err, entity.ErrServiceIdentityUnavailable):
		return http.StatusServiceUnavailable, "service identity unavailable"
	default:
		return http.StatusUnauthorized, "invalid service token"
	}
}

func serviceResponseStatus(ctx *fiber.Ctx, err error) int {
	status := ctx.Response().StatusCode()
	if err == nil {
		return status
	}

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return fiberErr.Code
	}

	if status >= http.StatusBadRequest {
		return status
	}

	return http.StatusInternalServerError
}

func serviceRequestID(ctx *fiber.Ctx) *string {
	requestID, ok := ctx.Locals("requestID").(string)
	if !ok {
		return nil
	}

	return stringPointer(requestID)
}

func stringPointer(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	return &value
}

// ServiceToken is retained only for downstream source compatibility while
// A-2 consumers migrate. The application no longer mounts routes with it.
//
// Deprecated: use RequireServicePrincipal.
func ServiceToken(token string) func(*fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		if token == "" {
			return ctx.SendStatus(http.StatusNotFound)
		}

		provided := ctx.Get(ServiceTokenHeader)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			return middlewareError(ctx, http.StatusUnauthorized, "invalid service token")
		}

		return ctx.Next()
	}
}
