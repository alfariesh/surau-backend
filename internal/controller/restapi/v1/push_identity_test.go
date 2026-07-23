package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
)

type pushIdentityStub struct {
	issuedUserID   string
	issuedFamilyID string
}

func (s *pushIdentityStub) Issue(
	_ context.Context,
	userID,
	familyID string,
) (entity.PushIdentityToken, error) {
	s.issuedUserID = userID
	s.issuedFamilyID = familyID

	return entity.PushIdentityToken{ExternalID: userID}, nil
}

func (s *pushIdentityStub) Resolve(
	_ context.Context,
	_,
	_ string,
	_ entity.PushRouteInput,
) entity.PushRouteResolution {
	return entity.PushRouteResolution{Destination: "home"}
}

func TestIssuePushIdentityIgnoresClientIdentityAndUsesAuthLocals(t *testing.T) {
	t.Parallel()

	stub := &pushIdentityStub{}
	controller := &V1{
		pushIdentity: stub,
		l:            logger.New("error"),
		v:            validator.New(validator.WithRequiredStructEnabled()),
	}
	app := fiber.New()
	app.Post("/v1/me/push/identity-token", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "authenticated-account")
		ctx.Locals("sessionID", "active-family")

		return controller.issuePushIdentityToken(ctx)
	})

	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/me/push/identity-token?external_id=attacker",
		http.NoBody,
	)
	response, err := app.Test(request)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "authenticated-account", stub.issuedUserID)
	require.Equal(t, "active-family", stub.issuedFamilyID)
}
