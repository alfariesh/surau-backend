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

func TestEmailCloudflareBounceWebhook(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		secret     string
		header     string
		body       string
		ingestErr  error
		wantStatus int
		wantCalled bool
	}{
		{
			name:       "disabled",
			secret:     "",
			header:     "secret",
			body:       `{}`,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "missing secret header",
			secret:     "secret",
			body:       `{}`,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong secret header",
			secret:     "secret",
			header:     "wrong",
			body:       `{}`,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed json",
			secret:     "secret",
			header:     "secret",
			body:       `{`,
			ingestErr:  entity.ErrInvalidAuthInput,
			wantStatus: http.StatusBadRequest,
			wantCalled: true,
		},
		{
			name:       "accepted",
			secret:     "secret",
			header:     "secret",
			body:       `{"permanent_bounces":["user@example.com"]}`,
			wantStatus: http.StatusAccepted,
			wantCalled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			email := &fakeEmailAdmin{
				webhookResult: entity.EmailWebhookIngestResult{
					Accepted:   1,
					Processed:  1,
					Suppressed: 1,
				},
				webhookErr: tc.ingestErr,
			}
			app := newEmailWebhookTestApp(email, tc.secret)
			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/v1/email/webhooks/cloudflare/bounces",
				strings.NewReader(tc.body),
			)
			req.Header.Set("Content-Type", "application/json")
			if tc.header != "" {
				req.Header.Set("cf-webhook-auth", tc.header)
			}

			resp, err := app.Test(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.wantStatus, resp.StatusCode)
			if tc.wantCalled {
				assert.Equal(t, []byte(tc.body), email.webhookPayload)
			} else {
				assert.Empty(t, email.webhookPayload)
			}
			if tc.wantStatus == http.StatusAccepted {
				var result entity.EmailWebhookIngestResult
				require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
				assert.Equal(t, email.webhookResult, result)
			}
		})
	}
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

func newEmailWebhookTestApp(email *fakeEmailAdmin, secret string) *fiber.App {
	app := fiber.New()
	controller := &V1{
		email:              email,
		emailWebhookSecret: secret,
		l:                  logger.New("error"),
		v:                  validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Post("/v1/email/webhooks/cloudflare/bounces", controller.emailCloudflareBounceWebhook)

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

	webhookPayload []byte
	webhookResult  entity.EmailWebhookIngestResult
	webhookErr     error
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

func (f *fakeEmailAdmin) IngestCloudflareBounceWebhook(
	_ context.Context,
	payload []byte,
) (entity.EmailWebhookIngestResult, error) {
	f.webhookPayload = append([]byte(nil), payload...)
	if f.webhookErr != nil {
		return entity.EmailWebhookIngestResult{}, f.webhookErr
	}

	return f.webhookResult, nil
}
