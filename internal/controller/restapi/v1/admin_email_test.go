package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/evrone/go-clean-template/internal/controller/restapi/middleware"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/evrone/go-clean-template/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminEmailCreateTemplateDefaultsEnabled(t *testing.T) {
	t.Parallel()

	email := &fakeEmailAdmin{}
	app := newAdminEmailTestApp(email, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/admin/emails/templates",
		strings.NewReader(`{"key":"weekly_digest","name":"Weekly Digest","category":"marketing"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var template entity.EmailTemplate
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&template))

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.True(t, email.created.Enabled)
	assert.True(t, template.Enabled)
}

func TestAdminEmailCreateTemplateRejectsEditorActor(t *testing.T) {
	t.Parallel()

	email := &fakeEmailAdmin{}
	app := newAdminEmailTestApp(email, entity.User{ID: "editor-id", Role: entity.UserRoleEditor})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/admin/emails/templates",
		strings.NewReader(`{"key":"weekly_digest","name":"Weekly Digest","category":"marketing","enabled":true}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Empty(t, email.created.Key)
}

func TestAdminEmailRetryFailedCampaign(t *testing.T) {
	t.Parallel()

	email := &fakeEmailAdmin{
		retryCampaign: entity.EmailCampaign{
			ID:     "campaign-id",
			Status: entity.EmailCampaignStatusSent,
		},
	}
	app := newAdminEmailTestApp(email, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/admin/emails/campaigns/campaign-id/retry-failed",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var campaign entity.EmailCampaign
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&campaign))

	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	assert.Equal(t, "campaign-id", campaign.ID)
	assert.Equal(t, "campaign-id", email.retryID)
	assert.Equal(t, "admin-id", email.retryActorID)
}

func newAdminEmailTestApp(email *fakeEmailAdmin, actor entity.User) *fiber.App {
	app := fiber.New()
	user := &fakeAuthUser{}
	controller := &V1{
		u:     user,
		email: email,
		l:     logger.New("error"),
		v:     validator.New(validator.WithRequiredStructEnabled()),
	}
	injectActor := func(ctx *fiber.Ctx) error {
		ctx.Locals("user", actor)
		ctx.Locals("userID", actor.ID)

		return ctx.Next()
	}

	app.Post(
		"/v1/admin/emails/templates",
		injectActor,
		middleware.RequireRoles(user, entity.UserRoleAdmin),
		controller.adminEmailCreateTemplate,
	)
	app.Post(
		"/v1/admin/emails/campaigns/:id/retry-failed",
		injectActor,
		middleware.RequireRoles(user, entity.UserRoleAdmin),
		controller.adminEmailRetryFailedCampaign,
	)

	return app
}

type fakeEmailAdmin struct {
	usecase.EmailAdmin

	created   entity.EmailTemplate
	createErr error

	retryID       string
	retryActorID  string
	retryCampaign entity.EmailCampaign
	retryErr      error
}

func (f *fakeEmailAdmin) CreateTemplate(
	_ context.Context,
	template entity.EmailTemplate,
) (entity.EmailTemplate, error) {
	if f.createErr != nil {
		return entity.EmailTemplate{}, f.createErr
	}

	if template.ID == "" {
		template.ID = "template-id"
	}
	f.created = template

	return template, nil
}

func (f *fakeEmailAdmin) RetryFailedCampaign(
	_ context.Context,
	id,
	actorID string,
) (entity.EmailCampaign, error) {
	f.retryID = id
	f.retryActorID = actorID
	if f.retryErr != nil {
		return entity.EmailCampaign{}, f.retryErr
	}
	if f.retryCampaign.ID == "" {
		f.retryCampaign.ID = id
	}

	return f.retryCampaign, nil
}
