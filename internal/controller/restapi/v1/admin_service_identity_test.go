package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

type adminServiceIdentitySpy struct {
	usecase.ServiceIdentity
	issueResult  entity.ServiceTokenIssueResult
	issueCalls   int
	issueActorID string
	issueForce   bool
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
