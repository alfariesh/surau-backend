package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/logger"
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

func TestAdminEmailResendMessage(t *testing.T) {
	t.Parallel()

	email := &fakeEmailAdmin{
		resendMessage: entity.EmailMessageLog{
			ID:       "message-id",
			Status:   entity.EmailMessageStatusQueued,
			Attempts: 0,
		},
	}
	app := newAdminEmailTestApp(email, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/admin/emails/messages/message-id/resend",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var message entity.EmailMessageLog
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&message))

	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	assert.Equal(t, "message-id", message.ID)
	assert.Equal(t, entity.EmailMessageStatusQueued, message.Status)
	assert.Equal(t, "message-id", email.resendID)
}

func TestAdminEmailResendMessageErrorMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		resendErr  error
		wantStatus int
	}{
		{name: "not found", resendErr: entity.ErrEmailMessageNotFound, wantStatus: http.StatusNotFound},
		{name: "not resendable", resendErr: entity.ErrEmailMessageNotResendable, wantStatus: http.StatusConflict},
		{name: "suppressed", resendErr: entity.ErrEmailRecipientSuppressed, wantStatus: http.StatusConflict},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			email := &fakeEmailAdmin{resendErr: tc.resendErr}
			app := newAdminEmailTestApp(email, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/v1/admin/emails/messages/message-id/resend",
				nil,
			)

			resp, err := app.Test(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.wantStatus, resp.StatusCode)
		})
	}
}

func TestAdminEmailResendMessageRejectsEditorActor(t *testing.T) {
	t.Parallel()

	email := &fakeEmailAdmin{}
	app := newAdminEmailTestApp(email, entity.User{ID: "editor-id", Role: entity.UserRoleEditor})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/admin/emails/messages/message-id/resend",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Empty(t, email.resendID)
}

func TestAdminEmailDeliveryEvents(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	email := &fakeEmailAdmin{
		deliveryEvents: []entity.EmailDeliveryEvent{
			{
				ID:             "event-id",
				DedupeKey:      "dedupe-key",
				Provider:       entity.EmailProviderCloudflare,
				EventType:      entity.EmailDeliveryEventBounceHard,
				RecipientEmail: "user@example.com",
				RawPayload:     entity.RawJSON(`{"provider":"cloudflare"}`),
				OccurredAt:     now,
				CreatedAt:      now,
			},
		},
		deliveryTotal: 3,
	}
	app := newAdminEmailTestApp(email, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/admin/emails/delivery-events?provider=cloudflare&event_type=bounce_hard&email=USER@example.com&message_id=message-id&campaign_id=campaign-id&campaign_recipient_id=recipient-id&limit=25&offset=5",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var body struct {
		Items []struct {
			ID         string         `json:"id"`
			RawPayload map[string]any `json:"raw_payload"`
		} `json:"items"`
		Total int `json:"total"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 3, body.Total)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "event-id", body.Items[0].ID)
	assert.Equal(t, "cloudflare", body.Items[0].RawPayload["provider"])
	assert.Equal(t, repo.EmailDeliveryEventFilter{
		Provider:            "cloudflare",
		EventType:           entity.EmailDeliveryEventBounceHard,
		Email:               "USER@example.com",
		MessageID:           "message-id",
		CampaignID:          "campaign-id",
		CampaignRecipientID: "recipient-id",
		Limit:               25,
		Offset:              5,
	}, email.deliveryFilter)
}

func TestAdminEmailMessageDeliveryEventsAppliesMessageID(t *testing.T) {
	t.Parallel()

	email := &fakeEmailAdmin{}
	app := newAdminEmailTestApp(email, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/admin/emails/messages/message-id/delivery-events?limit=10",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "message-id", email.deliveryFilter.MessageID)
	assert.Equal(t, uint64(10), email.deliveryFilter.Limit)
}

func TestAdminEmailCampaignRecipientDeliveryEventsAppliesRecipientID(t *testing.T) {
	t.Parallel()

	email := &fakeEmailAdmin{}
	app := newAdminEmailTestApp(email, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/admin/emails/campaign-recipients/recipient-id/delivery-events",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "recipient-id", email.deliveryFilter.CampaignRecipientID)
	assert.Equal(t, uint64(50), email.deliveryFilter.Limit)
}

func TestAdminEmailCampaignDeliveryEventSummary(t *testing.T) {
	t.Parallel()

	lastOccurredAt := time.Date(2026, 6, 5, 10, 30, 0, 0, time.UTC)
	email := &fakeEmailAdmin{
		deliverySummary: entity.EmailCampaignDeliveryEventSummary{
			CampaignID:       "campaign-id",
			Total:            3,
			BounceHard:       2,
			Complaint:        1,
			UniqueRecipients: 2,
			LastOccurredAt:   &lastOccurredAt,
		},
	}
	app := newAdminEmailTestApp(email, entity.User{ID: "admin-id", Role: entity.UserRoleAdmin})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/admin/emails/campaigns/campaign-id/delivery-event-summary",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var summary entity.EmailCampaignDeliveryEventSummary
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&summary))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "campaign-id", email.deliverySummaryID)
	assert.Equal(t, 3, summary.Total)
	assert.Equal(t, 2, summary.BounceHard)
	assert.Equal(t, 1, summary.Complaint)
	assert.Equal(t, 2, summary.UniqueRecipients)
	require.NotNil(t, summary.LastOccurredAt)
	assert.Equal(t, lastOccurredAt, *summary.LastOccurredAt)
}

func TestAdminEmailDeliveryEventsRejectsEditorActor(t *testing.T) {
	t.Parallel()

	email := &fakeEmailAdmin{}
	app := newAdminEmailTestApp(email, entity.User{ID: "editor-id", Role: entity.UserRoleEditor})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/admin/emails/delivery-events",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, repo.EmailDeliveryEventFilter{}, email.deliveryFilter)
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

func TestEmailUnsubscribePostAcceptsQueryToken(t *testing.T) {
	t.Parallel()

	email := &fakeEmailAdmin{
		unsubscribeResult: entity.EmailSubscription{
			UserID:         "user-id",
			MarketingOptIn: false,
			Source:         "unsubscribe_link",
		},
	}
	app := newEmailUnsubscribeTestApp(email)
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/email/unsubscribe?token=unsubscribe-token",
		nil,
	)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var subscription entity.EmailSubscription
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&subscription))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "unsubscribe-token", email.unsubscribeToken)
	assert.False(t, subscription.MarketingOptIn)
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
	app.Get(
		"/v1/admin/emails/delivery-events",
		injectActor,
		middleware.RequireRoles(user, entity.UserRoleAdmin),
		controller.adminEmailDeliveryEvents,
	)
	app.Get(
		"/v1/admin/emails/messages/:id/delivery-events",
		injectActor,
		middleware.RequireRoles(user, entity.UserRoleAdmin),
		controller.adminEmailMessageDeliveryEvents,
	)
	app.Post(
		"/v1/admin/emails/messages/:id/resend",
		injectActor,
		middleware.RequireRoles(user, entity.UserRoleAdmin),
		controller.adminEmailResendMessage,
	)
	app.Get(
		"/v1/admin/emails/campaign-recipients/:id/delivery-events",
		injectActor,
		middleware.RequireRoles(user, entity.UserRoleAdmin),
		controller.adminEmailCampaignRecipientDeliveryEvents,
	)
	app.Get(
		"/v1/admin/emails/campaigns/:id/delivery-event-summary",
		injectActor,
		middleware.RequireRoles(user, entity.UserRoleAdmin),
		controller.adminEmailCampaignDeliveryEventSummary,
	)

	return app
}

func newEmailUnsubscribeTestApp(email *fakeEmailAdmin) *fiber.App {
	app := fiber.New()
	controller := &V1{
		email: email,
		l:     logger.New("error"),
		v:     validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Post("/v1/email/unsubscribe", controller.emailUnsubscribe)

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

	resendID      string
	resendMessage entity.EmailMessageLog
	resendErr     error

	deliveryEvents    []entity.EmailDeliveryEvent
	deliveryTotal     int
	deliveryFilter    repo.EmailDeliveryEventFilter
	deliveryErr       error
	deliverySummary   entity.EmailCampaignDeliveryEventSummary
	deliverySummaryID string
	summaryErr        error

	unsubscribeToken  string
	unsubscribeResult entity.EmailSubscription
	unsubscribeErr    error

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

func (f *fakeEmailAdmin) ResendMessage(
	_ context.Context,
	id string,
) (entity.EmailMessageLog, error) {
	f.resendID = id
	if f.resendErr != nil {
		return entity.EmailMessageLog{}, f.resendErr
	}
	if f.resendMessage.ID == "" {
		f.resendMessage.ID = id
	}

	return f.resendMessage, nil
}

func (f *fakeEmailAdmin) DeliveryEvents(
	_ context.Context,
	filter repo.EmailDeliveryEventFilter,
) ([]entity.EmailDeliveryEvent, int, error) {
	f.deliveryFilter = filter
	if f.deliveryErr != nil {
		return nil, 0, f.deliveryErr
	}

	return append([]entity.EmailDeliveryEvent(nil), f.deliveryEvents...), f.deliveryTotal, nil
}

func (f *fakeEmailAdmin) CampaignDeliveryEventSummary(
	_ context.Context,
	campaignID string,
) (entity.EmailCampaignDeliveryEventSummary, error) {
	f.deliverySummaryID = campaignID
	if f.summaryErr != nil {
		return entity.EmailCampaignDeliveryEventSummary{}, f.summaryErr
	}
	summary := f.deliverySummary
	if summary.CampaignID == "" {
		summary.CampaignID = campaignID
	}

	return summary, nil
}

func (f *fakeEmailAdmin) Unsubscribe(
	_ context.Context,
	token string,
) (entity.EmailSubscription, error) {
	f.unsubscribeToken = token
	if f.unsubscribeErr != nil {
		return entity.EmailSubscription{}, f.unsubscribeErr
	}
	if f.unsubscribeResult.UserID == "" {
		f.unsubscribeResult.UserID = "user-id"
	}

	return f.unsubscribeResult, nil
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
