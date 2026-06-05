package email

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/contentlang"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderTemplate(t *testing.T) {
	t.Parallel()

	t.Run("requires declared variables", func(t *testing.T) {
		t.Parallel()

		uc := New(nil, nil, Options{SupportEmail: "support@example.com"})
		_, err := uc.render(entity.EmailTemplateVersion{
			Lang:              contentlang.Default,
			SubjectTemplate:   "Halo {{.name}}",
			BodyTemplate:      "Body {{.name}}",
			TextTemplate:      "Text {{.name}}",
			RequiredVariables: []string{"name"},
		}, map[string]string{})

		require.ErrorIs(t, err, entity.ErrInvalidEmailTemplate)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("renders arabic rtl html and converts literal newlines in text", func(t *testing.T) {
		t.Parallel()

		uc := New(nil, nil, Options{SupportEmail: "support@example.com"})
		preview, err := uc.render(entity.EmailTemplateVersion{
			Lang:                contentlang.Arabic,
			SubjectTemplate:     "مرحبا {{.name}}",
			PreviewTemplate:     "معاينة {{.name}}",
			TitleTemplate:       "عنوان",
			BodyTemplate:        "السلام عليكم، {{.name}}",
			ButtonLabelTemplate: "افتح",
			ButtonURLTemplate:   "https://example.com/{{.slug}}",
			TextTemplate:        "السطر الأول\\nالسطر الثاني {{.name}}",
			RequiredVariables:   []string{"name", "slug"},
		}, map[string]string{"name": "أحمد", "slug": "reader"})

		require.NoError(t, err)
		assert.Equal(t, "مرحبا أحمد", preview.Subject)
		assert.Contains(t, preview.HTML, `<html lang="ar" dir="rtl">`)
		assert.Contains(t, preview.HTML, "direction:rtl")
		assert.Contains(t, preview.HTML, "https://example.com/reader")
		assert.Contains(t, preview.Text, "السطر الأول\nالسطر الثاني أحمد")
		assert.Contains(t, preview.Text, "support@example.com")
	})
}

func TestUnsubscribeToken(t *testing.T) {
	t.Parallel()

	uc := New(nil, nil, Options{
		UnsubscribeURL:        "https://frontend.example.com/unsubscribe",
		UnsubscribeTokenKeyID: "2026-06",
		UnsubscribeTokenSeed:  "secret",
		UnsubscribeTokenSecrets: map[string]string{
			"2026-06": "secret",
			"2026-05": "previous-secret",
		},
	})
	token := uc.unsubscribeToken("user-id", "USER@example.com")
	userID, email, err := uc.parseUnsubscribeToken(token)

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(token, "v2.2026-06."))
	assert.Equal(t, "user-id", userID)
	assert.Equal(t, "user@example.com", email)
	assert.Contains(t, uc.unsubscribeLink(token), "https://frontend.example.com/unsubscribe?token=")

	_, _, err = uc.parseUnsubscribeToken(token + "tampered")
	require.ErrorIs(t, err, entity.ErrInvalidUnsubscribeToken)
}

func TestParseUnsubscribeTokenRotation(t *testing.T) {
	t.Parallel()

	uc := New(nil, nil, Options{
		UnsubscribeTokenKeyID: "2026-06",
		UnsubscribeTokenSeed:  "new-secret",
		UnsubscribeTokenSecrets: map[string]string{
			"2026-06": "new-secret",
			"2026-05": "old-secret",
		},
	})
	payload := unsubscribeTokenPayload("user-id", "USER@example.com")

	t.Run("v2 previous key", func(t *testing.T) {
		t.Parallel()

		signingInput := "v2.2026-05." + payload
		token := signingInput + "." + unsubscribeTokenSignature(signingInput, "old-secret")

		userID, email, err := uc.parseUnsubscribeToken(token)

		require.NoError(t, err)
		assert.Equal(t, "user-id", userID)
		assert.Equal(t, "user@example.com", email)
	})

	t.Run("legacy current secret", func(t *testing.T) {
		t.Parallel()

		token := payload + "." + unsubscribeTokenSignature(payload, "new-secret")

		userID, email, err := uc.parseUnsubscribeToken(token)

		require.NoError(t, err)
		assert.Equal(t, "user-id", userID)
		assert.Equal(t, "user@example.com", email)
	})

	t.Run("legacy previous secret", func(t *testing.T) {
		t.Parallel()

		token := payload + "." + unsubscribeTokenSignature(payload, "old-secret")

		userID, email, err := uc.parseUnsubscribeToken(token)

		require.NoError(t, err)
		assert.Equal(t, "user-id", userID)
		assert.Equal(t, "user@example.com", email)
	})

	t.Run("unknown v2 key", func(t *testing.T) {
		t.Parallel()

		signingInput := "v2.unknown." + payload
		token := signingInput + "." + unsubscribeTokenSignature(signingInput, "old-secret")

		_, _, err := uc.parseUnsubscribeToken(token)

		require.ErrorIs(t, err, entity.ErrInvalidUnsubscribeToken)
	})
}

func TestCreateVersionNormalizesRequiredVariables(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{}
	uc := New(stub, nil, Options{})
	_, err := uc.CreateVersion(t.Context(), entity.EmailTemplateVersion{
		TemplateID:        "template-id",
		Lang:              "id-ID",
		SubjectTemplate:   "Halo",
		TextTemplate:      "Halo",
		RequiredVariables: []string{" Name ", "name", "UNSUBSCRIBE-URL"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"name", "unsubscribe_url"}, stub.createdVersion.RequiredVariables)
}

func TestCreateCampaignRequiresActiveMarketingTemplate(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{
		template: entity.EmailTemplate{
			ID:       "template-id",
			Key:      "auth_password_reset",
			Category: entity.EmailCategoryTransactional,
			Enabled:  true,
		},
	}
	uc := New(stub, nil, Options{})
	_, err := uc.CreateCampaign(t.Context(), entity.EmailCampaign{
		Name:       "Wrong template",
		TemplateID: "template-id",
	})

	require.ErrorIs(t, err, entity.ErrInvalidEmailCampaign)
}

func TestDeliverCampaignRecipientMarksSuppressedAsSkipped(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{
		template: entity.EmailTemplate{
			ID:       "template-id",
			Key:      "weekly_digest",
			Category: entity.EmailCategoryMarketing,
			Enabled:  true,
		},
		publishedVersion: entity.EmailTemplateVersion{
			ID:              "version-id",
			TemplateID:      "template-id",
			Lang:            contentlang.Default,
			SubjectTemplate: "Halo {{.email}}",
			TextTemplate:    "Halo {{.unsubscribe_url}}",
			Published:       true,
		},
		suppressed: true,
	}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{})
	status, err := uc.deliverCampaignRecipient(
		t.Context(),
		entity.EmailCampaign{ID: "campaign-id", TemplateID: "template-id"},
		entity.EmailCampaignRecipient{
			ID:             "recipient-id",
			UserID:         "user-id",
			Email:          "user@example.com",
			Lang:           contentlang.Default,
			UnsubscribeURL: "https://frontend.example.com/unsubscribe?token=abc",
		},
	)

	require.NoError(t, err)
	assert.Equal(t, entity.EmailRecipientStatusSkipped, status)
	assert.Equal(t, entity.EmailRecipientStatusSkipped, stub.recipientStatus)
	assert.Equal(t, "suppressed", stub.recipientError)
	assert.Empty(t, sender.sent)
}

func TestDeliverCampaignRecipientAddsListUnsubscribeHeaders(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{
		template: entity.EmailTemplate{
			ID:       "template-id",
			Key:      "weekly_digest",
			Category: entity.EmailCategoryMarketing,
			Enabled:  true,
		},
		publishedVersion: entity.EmailTemplateVersion{
			ID:              "version-id",
			TemplateID:      "template-id",
			Lang:            contentlang.Default,
			SubjectTemplate: "Halo {{.email}}",
			TextTemplate:    "Halo {{.unsubscribe_url}}",
			Published:       true,
		},
	}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{
		UnsubscribeHeaderURL: "https://api.surau.org/v1/email/unsubscribe",
	})

	status, err := uc.deliverCampaignRecipient(
		t.Context(),
		entity.EmailCampaign{ID: "campaign-id", TemplateID: "template-id"},
		entity.EmailCampaignRecipient{
			ID:             "recipient-id",
			UserID:         "user-id",
			Email:          "user@example.com",
			Lang:           contentlang.Default,
			UnsubscribeURL: "https://frontend.example.com/unsubscribe?token=abc",
		},
	)

	require.NoError(t, err)
	assert.Equal(t, entity.EmailRecipientStatusSent, status)
	require.Len(t, sender.sent, 1)
	assert.Equal(
		t,
		"<https://api.surau.org/v1/email/unsubscribe?token=abc>",
		sender.sent[0].Headers["List-Unsubscribe"],
	)
	assert.Equal(t, "One-Click", sender.sent[0].Headers["List-Unsubscribe-Post"])
}

func TestDeliverCampaignRecipientHardBounceCreatesEventAndSuppression(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{
		template: entity.EmailTemplate{
			ID:       "template-id",
			Key:      "weekly_digest",
			Category: entity.EmailCategoryMarketing,
			Enabled:  true,
		},
		publishedVersion: entity.EmailTemplateVersion{
			ID:              "version-id",
			TemplateID:      "template-id",
			Lang:            contentlang.Default,
			SubjectTemplate: "Halo {{.email}}",
			TextTemplate:    "Halo {{.unsubscribe_url}}",
			Published:       true,
		},
	}
	sender := &emailSenderStub{
		result: entity.EmailSendResult{
			Provider:         entity.EmailProviderCloudflare,
			PermanentBounces: []string{"user@example.com"},
			ProviderResponse: `{"success":true,"result":{"permanent_bounces":["user@example.com"]}}`,
		},
		err: fmt.Errorf("%w: %w for user@example.com", entity.ErrEmailDeliveryFailed, entity.ErrEmailPermanentBounce),
	}
	uc := New(stub, sender, Options{})

	status, err := uc.deliverCampaignRecipient(
		t.Context(),
		entity.EmailCampaign{ID: "campaign-id", TemplateID: "template-id"},
		entity.EmailCampaignRecipient{
			ID:             "recipient-id",
			UserID:         "user-id",
			Email:          "user@example.com",
			Lang:           contentlang.Default,
			UnsubscribeURL: "https://frontend.example.com/unsubscribe?token=abc",
		},
	)

	require.NoError(t, err)
	assert.Equal(t, entity.EmailRecipientStatusFailed, status)
	require.Len(t, sender.sent, 1)
	messageID := stub.createdMessages[0].ID
	assert.Equal(t, messageID, sender.sent[0].MessageID)
	assert.Equal(t, "campaign-id", sender.sent[0].CampaignID)
	assert.Equal(t, "recipient-id", sender.sent[0].CampaignRecipient)
	require.Len(t, stub.deliveryEvents, 1)
	assert.Equal(t, entity.EmailDeliveryEventBounceHard, stub.deliveryEvents[0].EventType)
	assert.Equal(t, "user@example.com", stub.deliveryEvents[0].RecipientEmail)
	assert.Equal(t, messageID, stub.deliveryEvents[0].MessageID)
	require.Len(t, stub.suppressions, 1)
	assert.Equal(t, entity.EmailSuppressionScopeAll, stub.suppressions[0].Scope)
	assert.Equal(t, "permanent_bounce", stub.suppressions[0].Reason)
	require.Len(t, stub.updatedMessages, 1)
	assert.Equal(t, entity.EmailMessageStatusFailed, stub.updatedMessages[0].Status)
	assert.Contains(t, stub.updatedMessages[0].ProviderResponse, "permanent_bounces")
}

func TestIngestCloudflareBounceWebhookDedupesAndSuppresses(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{}
	uc := New(stub, nil, Options{})
	payload := []byte(`{
		"alert_correlation_id":"event-1",
		"data":{
			"events":[{
				"event_type":"bounce_hard",
				"recipient_email":"USER@example.com",
				"message_id":"message-id-1",
				"campaign_id":"campaign-id",
				"campaign_recipient_id":"recipient-id",
				"diagnostic":"550 5.1.1 user unknown",
				"occurred_at":"2026-06-05T01:02:03Z"
			}]
		}
	}`)

	first, err := uc.IngestCloudflareBounceWebhook(t.Context(), payload)
	require.NoError(t, err)
	second, err := uc.IngestCloudflareBounceWebhook(t.Context(), payload)
	require.NoError(t, err)

	assert.Equal(t, entity.EmailWebhookIngestResult{Accepted: 1, Processed: 1, Suppressed: 1}, first)
	assert.Equal(t, entity.EmailWebhookIngestResult{Accepted: 1, Processed: 0, Suppressed: 1, Duplicates: 1}, second)
	require.Len(t, stub.deliveryEvents, 1)
	assert.Equal(t, "user@example.com", stub.deliveryEvents[0].RecipientEmail)
	assert.Equal(t, "message-id-1", stub.deliveryEvents[0].MessageID)
	require.Len(t, stub.suppressions, 1)
	assert.Equal(t, "permanent_bounce", stub.suppressions[0].Reason)
}

func TestIngestCloudflareBounceWebhookPreservesManualSuppression(t *testing.T) {
	t.Parallel()

	actor := "admin-id"
	stub := &emailRepoStub{
		suppressionByKey: map[string]entity.EmailSuppression{
			"user@example.com:all": {
				ID:        "suppression-id",
				Email:     "user@example.com",
				Scope:     entity.EmailSuppressionScopeAll,
				Reason:    "manual",
				CreatedBy: &actor,
				CreatedAt: time.Now().UTC(),
			},
		},
	}
	uc := New(stub, nil, Options{})

	result, err := uc.IngestCloudflareBounceWebhook(
		t.Context(),
		[]byte(`{"permanent_bounces":["user@example.com"]}`),
	)

	require.NoError(t, err)
	assert.Equal(t, entity.EmailWebhookIngestResult{Accepted: 1, Processed: 1, Suppressed: 1}, result)
	assert.Equal(t, "manual", stub.suppressionByKey["user@example.com:all"].Reason)
	assert.Empty(t, stub.suppressions)
}

func TestSendTransactionalRedactsSensitiveMetadata(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{
		UnsubscribeHeaderURL: "https://api.surau.org/v1/email/unsubscribe",
	})

	err := uc.SendTransactional(t.Context(), entity.TransactionalEmailRequest{
		Key:  entity.EmailTemplateKeyVerification,
		To:   "user@example.com",
		Lang: contentlang.Default,
		Variables: map[string]string{
			"link":            "https://frontend.example.com/verify-email?token=secret",
			"otp":             "123456",
			"unsubscribe_url": "https://frontend.example.com/unsubscribe?token=secret",
			"relative_link":   "/verify-email?token=secret",
			"duration":        "10 menit",
		},
		Fallback: entity.EmailMessage{
			To:      "user@example.com",
			Subject: "Verify",
			Text:    "Verify",
		},
	})

	require.NoError(t, err)
	require.Len(t, sender.sent, 1)
	require.Len(t, stub.createdMessages, 1)
	assert.Equal(t, redactedValue, stub.createdMessages[0].Metadata["link"])
	assert.Equal(t, redactedValue, stub.createdMessages[0].Metadata["otp"])
	assert.Equal(t, redactedValue, stub.createdMessages[0].Metadata["unsubscribe_url"])
	assert.Equal(t, redactedValue, stub.createdMessages[0].Metadata["relative_link"])
	assert.Equal(t, "10 menit", stub.createdMessages[0].Metadata["duration"])
	assert.Empty(t, sender.sent[0].Headers)
}

func TestSendTransactionalCriticalBypassesSuppression(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{suppressed: true}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{})

	err := uc.SendTransactional(t.Context(), entity.TransactionalEmailRequest{
		Key:      entity.EmailTemplateKeyVerification,
		To:       "user@example.com",
		Lang:     contentlang.Default,
		Critical: true,
		Fallback: entity.EmailMessage{
			To:      "user@example.com",
			Subject: "Verify",
			Text:    "Verify",
		},
	})

	require.NoError(t, err)
	require.Len(t, sender.sent, 1)
	require.Len(t, stub.updatedMessages, 1)
	assert.Equal(t, entity.EmailMessageStatusSent, stub.updatedMessages[0].Status)
	assert.Equal(t, "user@example.com", sender.sent[0].To)
}

func TestSendTransactionalCriticalTemplateRenderFallback(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{
		template: entity.EmailTemplate{
			ID:       "template-id",
			Key:      entity.EmailTemplateKeyVerification,
			Category: entity.EmailCategoryTransactional,
			Critical: true,
			Enabled:  true,
		},
		eventSetting: entity.EmailEventSetting{
			Key:        entity.EmailTemplateKeyVerification,
			TemplateID: "template-id",
			Enabled:    false,
			Critical:   false,
		},
		publishedVersion: entity.EmailTemplateVersion{
			ID:              "version-id",
			TemplateID:      "template-id",
			Lang:            contentlang.Default,
			SubjectTemplate: "Halo {{.missing}}",
			TextTemplate:    "Text {{.missing}}",
			Published:       true,
		},
	}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{})

	err := uc.SendTransactional(t.Context(), entity.TransactionalEmailRequest{
		Key:  entity.EmailTemplateKeyVerification,
		To:   "user@example.com",
		Lang: contentlang.Default,
		Variables: map[string]string{
			"link": "https://frontend.example.com/verify-email?token=secret",
		},
		Fallback: entity.EmailMessage{
			To:      "user@example.com",
			Subject: "Fallback",
			Text:    "Fallback text",
		},
	})

	require.NoError(t, err)
	require.Len(t, sender.sent, 1)
	require.Len(t, stub.createdMessages, 1)
	assert.Equal(t, "Fallback", sender.sent[0].Subject)
	assert.Equal(t, redactedValue, stub.createdMessages[0].Metadata["link"])
	assert.NotEmpty(t, stub.createdMessages[0].Metadata["template_render_error"])
}

func TestSendTransactionalTransientFailureQueuesRetryAndReturnsSuccess(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{}
	sender := &emailSenderStub{
		result: entity.EmailSendResult{
			Provider:         entity.EmailProviderCloudflare,
			ProviderResponse: `{"success":false}`,
		},
		err: assert.AnError,
	}
	uc := New(stub, sender, Options{})
	start := time.Now().UTC()

	err := uc.SendTransactional(t.Context(), entity.TransactionalEmailRequest{
		Key:  entity.EmailTemplateKeyVerification,
		To:   "user@example.com",
		Lang: contentlang.Default,
		Fallback: entity.EmailMessage{
			To:      "user@example.com",
			Subject: "Verify",
			HTML:    "<p>Verify</p>",
			Text:    "Verify",
			Headers: map[string]string{"X-Test": "value"},
		},
	})

	require.NoError(t, err)
	require.Len(t, sender.sent, 1)
	require.Len(t, stub.scheduledRetries, 1)
	retry := stub.scheduledRetries[0]
	assert.Equal(t, entity.EmailMessageStatusQueued, retry.Status)
	assert.Equal(t, 1, retry.Attempts)
	assert.Equal(t, `{"success":false}`, retry.ProviderResponse)
	assert.Contains(t, retry.Error, assert.AnError.Error())
	require.NotNil(t, retry.ScheduledAt)
	assert.WithinDuration(t, start.Add(time.Minute), *retry.ScheduledAt, 2*time.Second)
	require.Len(t, stub.createdMessages, 1)
	assert.Equal(t, "<p>Verify</p>", stub.createdMessages[0].HTML)
	assert.Equal(t, "Verify", stub.createdMessages[0].Text)
	assert.Equal(t, "value", stub.createdMessages[0].Headers["X-Test"])
}

func TestSendTransactionalPermanentBounceFailsWithoutRetry(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{}
	sender := &emailSenderStub{
		result: entity.EmailSendResult{
			Provider:         entity.EmailProviderCloudflare,
			PermanentBounces: []string{"user@example.com"},
			ProviderResponse: `{"success":true,"result":{"permanent_bounces":["user@example.com"]}}`,
		},
		err: fmt.Errorf("%w: %w for user@example.com", entity.ErrEmailDeliveryFailed, entity.ErrEmailPermanentBounce),
	}
	uc := New(stub, sender, Options{})

	err := uc.SendTransactional(t.Context(), entity.TransactionalEmailRequest{
		Key:  entity.EmailTemplateKeyVerification,
		To:   "user@example.com",
		Lang: contentlang.Default,
		Fallback: entity.EmailMessage{
			To:      "user@example.com",
			Subject: "Verify",
			Text:    "Verify",
		},
	})

	require.ErrorIs(t, err, entity.ErrEmailDeliveryFailed)
	require.ErrorIs(t, err, entity.ErrEmailPermanentBounce)
	assert.Empty(t, stub.scheduledRetries)
	require.Len(t, stub.updatedMessages, 1)
	assert.Equal(t, entity.EmailMessageStatusFailed, stub.updatedMessages[0].Status)
	require.Len(t, stub.deliveryEvents, 1)
	assert.Equal(t, entity.EmailDeliveryEventBounceHard, stub.deliveryEvents[0].EventType)
	require.Len(t, stub.suppressions, 1)
	assert.Equal(t, entity.EmailSuppressionScopeAll, stub.suppressions[0].Scope)
}

func TestDispatchDueTransactionalEmailsSendsQueuedMessage(t *testing.T) {
	t.Parallel()

	queued := transactionalMessageLogForTest("message-id", "user@example.com", 1, false)
	stub := &emailRepoStub{claimedTransactionalMessages: []entity.EmailMessageLog{queued}}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{})

	err := uc.DispatchDueTransactionalEmails(t.Context(), 20)

	require.NoError(t, err)
	assert.Equal(t, 20, stub.claimLimit)
	assert.Equal(t, transactionalRetryVisibilityTimeout, stub.claimVisibilityTimeout)
	require.Len(t, sender.sent, 1)
	assert.Equal(t, "message-id", sender.sent[0].MessageID)
	assert.Equal(t, "user@example.com", sender.sent[0].To)
	assert.Equal(t, queued.HTML, sender.sent[0].HTML)
	require.Len(t, stub.updatedMessages, 1)
	assert.Equal(t, entity.EmailMessageStatusSent, stub.updatedMessages[0].Status)
	assert.Equal(t, 2, stub.updatedMessages[0].Attempts)
	require.NotNil(t, stub.updatedMessages[0].SentAt)
}

func TestDispatchDueTransactionalEmailsReschedulesAndEventuallyFails(t *testing.T) {
	t.Parallel()

	reschedule := transactionalMessageLogForTest("message-id-reschedule", "retry@example.com", 1, false)
	final := transactionalMessageLogForTest("message-id-final", "final@example.com", 5, false)
	stub := &emailRepoStub{claimedTransactionalMessages: []entity.EmailMessageLog{reschedule, final}}
	sender := &emailSenderStub{
		errByEmail: map[string]error{
			"retry@example.com": assert.AnError,
			"final@example.com": assert.AnError,
		},
	}
	uc := New(stub, sender, Options{})
	start := time.Now().UTC()

	err := uc.DispatchDueTransactionalEmails(t.Context(), 20)

	require.NoError(t, err)
	require.Len(t, sender.sent, 2)
	require.Len(t, stub.scheduledRetries, 1)
	assert.Equal(t, "message-id-reschedule", stub.scheduledRetries[0].ID)
	assert.Equal(t, 2, stub.scheduledRetries[0].Attempts)
	require.NotNil(t, stub.scheduledRetries[0].ScheduledAt)
	assert.WithinDuration(t, start.Add(5*time.Minute), *stub.scheduledRetries[0].ScheduledAt, 2*time.Second)
	require.Len(t, stub.updatedMessages, 2)
	assert.Equal(t, entity.EmailMessageStatusFailed, stub.updatedMessages[1].Status)
	assert.Equal(t, 6, stub.updatedMessages[1].Attempts)
	assert.Equal(t, "message-id-final", stub.updatedMessages[1].ID)
}

func TestDispatchDueTransactionalEmailsSkipsSuppressedNonCritical(t *testing.T) {
	t.Parallel()

	queued := transactionalMessageLogForTest("message-id", "user@example.com", 1, false)
	stub := &emailRepoStub{
		suppressed:                   true,
		claimedTransactionalMessages: []entity.EmailMessageLog{queued},
	}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{})

	err := uc.DispatchDueTransactionalEmails(t.Context(), 20)

	require.NoError(t, err)
	assert.Empty(t, sender.sent)
	require.Len(t, stub.updatedMessages, 1)
	assert.Equal(t, entity.EmailMessageStatusSkipped, stub.updatedMessages[0].Status)
	assert.Equal(t, "suppressed", stub.updatedMessages[0].Error)
}

func TestDispatchDueTransactionalEmailsCriticalBypassesSuppression(t *testing.T) {
	t.Parallel()

	queued := transactionalMessageLogForTest("message-id", "user@example.com", 1, true)
	stub := &emailRepoStub{
		suppressed:                   true,
		claimedTransactionalMessages: []entity.EmailMessageLog{queued},
	}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{})

	err := uc.DispatchDueTransactionalEmails(t.Context(), 20)

	require.NoError(t, err)
	require.Len(t, sender.sent, 1)
	require.Len(t, stub.updatedMessages, 1)
	assert.Equal(t, entity.EmailMessageStatusSent, stub.updatedMessages[0].Status)
}

func TestTransactionalRetryDelayAfterAttempts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		attempts int
		want     time.Duration
		wantOK   bool
	}{
		{name: "first failure", attempts: 1, want: time.Minute, wantOK: true},
		{name: "second failure", attempts: 2, want: 5 * time.Minute, wantOK: true},
		{name: "third failure", attempts: 3, want: 15 * time.Minute, wantOK: true},
		{name: "fourth failure", attempts: 4, want: time.Hour, wantOK: true},
		{name: "fifth failure", attempts: 5, want: 6 * time.Hour, wantOK: true},
		{name: "after final retry", attempts: 6, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := transactionalRetryDelayAfterAttempts(tt.attempts)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSendCampaignNowProcessesAllBatchesAndUsesMetadata(t *testing.T) {
	t.Parallel()

	const recipientCount = defaultBatchSize + 1
	audience := make([]entity.EmailAudienceRecipient, 0, recipientCount)
	for i := 0; i < recipientCount; i++ {
		audience = append(audience, entity.EmailAudienceRecipient{
			UserID: fmt.Sprintf("user-id-%d", i+1),
			Email:  fmt.Sprintf("user%d@example.com", i+1),
			Lang:   contentlang.Default,
		})
	}
	stub := &emailRepoStub{
		template: entity.EmailTemplate{
			ID:       "template-id",
			Key:      "weekly_digest",
			Category: entity.EmailCategoryMarketing,
			Enabled:  true,
		},
		publishedVersion: entity.EmailTemplateVersion{
			ID:              "version-id",
			TemplateID:      "template-id",
			Lang:            contentlang.Default,
			SubjectTemplate: "Update {{.topic}}",
			TextTemplate:    "{{.topic}} {{.email}} {{.unsubscribe_url}}",
			Published:       true,
		},
		campaign: entity.EmailCampaign{
			ID:         "campaign-id",
			Name:       "Ramadan Digest",
			TemplateID: "template-id",
			Status:     entity.EmailCampaignStatusDraft,
			Metadata: map[string]string{
				"topic":           "ramadan",
				"unsubscribe_url": "https://bad.example.com/unsubscribe",
			},
		},
		audience: audience,
	}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{
		UnsubscribeURL:       "https://frontend.example.com/unsubscribe",
		UnsubscribeTokenSeed: "secret",
	})

	campaign, err := uc.SendCampaignNow(t.Context(), "campaign-id", "admin-id")

	require.NoError(t, err)
	require.Len(t, sender.sent, recipientCount)
	assert.Equal(t, entity.EmailCampaignStatusSent, campaign.Status)
	assert.Equal(t, fmt.Sprintf("%d", recipientCount), campaign.Metadata[campaignMetadataDeliveryTotal])
	assert.Equal(t, fmt.Sprintf("%d", recipientCount), campaign.Metadata[campaignMetadataDeliverySent])
	assert.Equal(t, "0", campaign.Metadata[campaignMetadataDeliveryFailed])
	assert.Equal(t, "0", campaign.Metadata[campaignMetadataDeliverySkipped])
	assert.Contains(t, sender.sent[0].Text, "ramadan")
	assert.Contains(t, sender.sent[0].Text, "https://frontend.example.com/unsubscribe?token=")
	assert.NotContains(t, sender.sent[0].Text, "https://bad.example.com/unsubscribe")
	assert.Empty(t, sender.sent[0].Headers)
}

func TestTestSendCampaignOmitsListUnsubscribeHeadersWithoutToken(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{
		template: entity.EmailTemplate{
			ID:       "template-id",
			Key:      "weekly_digest",
			Category: entity.EmailCategoryMarketing,
			Enabled:  true,
		},
		publishedVersion: entity.EmailTemplateVersion{
			ID:              "version-id",
			TemplateID:      "template-id",
			Lang:            contentlang.Default,
			SubjectTemplate: "Update",
			TextTemplate:    "{{.unsubscribe_url}}",
			Published:       true,
		},
		campaign: entity.EmailCampaign{
			ID:         "campaign-id",
			TemplateID: "template-id",
			Status:     entity.EmailCampaignStatusDraft,
		},
	}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{
		UnsubscribeURL:       "https://frontend.example.com/unsubscribe",
		UnsubscribeHeaderURL: "https://api.surau.org/v1/email/unsubscribe",
	})

	_, err := uc.TestSendCampaign(t.Context(), "campaign-id", "user@example.com", contentlang.Default, nil)

	require.NoError(t, err)
	require.Len(t, sender.sent, 1)
	assert.Empty(t, sender.sent[0].Headers)
}

func TestRetryFailedCampaignRetriesOnlyFailedSnapshot(t *testing.T) {
	t.Parallel()

	oldUpdatedAt := time.Now().UTC().Add(-time.Hour)
	stub := &emailRepoStub{
		template: entity.EmailTemplate{
			ID:       "template-id",
			Key:      "weekly_digest",
			Category: entity.EmailCategoryMarketing,
			Enabled:  true,
		},
		publishedVersion: entity.EmailTemplateVersion{
			ID:              "version-id",
			TemplateID:      "template-id",
			Lang:            contentlang.Default,
			SubjectTemplate: "Update {{.topic}}",
			TextTemplate:    "{{.topic}} {{.email}} {{.unsubscribe_url}}",
			Published:       true,
		},
		campaign: entity.EmailCampaign{
			ID:         "campaign-id",
			Name:       "Ramadan Digest",
			TemplateID: "template-id",
			Status:     entity.EmailCampaignStatusSent,
			Metadata:   map[string]string{"topic": "ramadan"},
		},
		recipients: []entity.EmailCampaignRecipient{
			{
				ID:             "sent-recipient",
				CampaignID:     "campaign-id",
				Email:          "sent@example.com",
				Lang:           contentlang.Default,
				UnsubscribeURL: "https://frontend.example.com/unsubscribe?token=sent",
				Status:         entity.EmailRecipientStatusSent,
				UpdatedAt:      oldUpdatedAt,
			},
			{
				ID:             "failed-success-recipient",
				CampaignID:     "campaign-id",
				Email:          "failed-success@example.com",
				Lang:           contentlang.Default,
				UnsubscribeURL: "https://frontend.example.com/unsubscribe?token=failed-success",
				Status:         entity.EmailRecipientStatusFailed,
				UpdatedAt:      oldUpdatedAt,
			},
			{
				ID:             "failed-again-recipient",
				CampaignID:     "campaign-id",
				Email:          "failed-again@example.com",
				Lang:           contentlang.Default,
				UnsubscribeURL: "https://frontend.example.com/unsubscribe?token=failed-again",
				Status:         entity.EmailRecipientStatusFailed,
				UpdatedAt:      oldUpdatedAt,
			},
			{
				ID:             "skipped-recipient",
				CampaignID:     "campaign-id",
				Email:          "skipped@example.com",
				Lang:           contentlang.Default,
				UnsubscribeURL: "https://frontend.example.com/unsubscribe?token=skipped",
				Status:         entity.EmailRecipientStatusSkipped,
				UpdatedAt:      oldUpdatedAt,
			},
		},
	}
	stub.resetRecipientStatusMaps()
	sender := &emailSenderStub{
		errByEmail: map[string]error{"failed-again@example.com": assert.AnError},
	}
	uc := New(stub, sender, Options{})

	campaign, err := uc.RetryFailedCampaign(t.Context(), "campaign-id", "admin-id")

	require.NoError(t, err)
	require.Len(t, sender.sent, 2)
	assert.Equal(t, entity.EmailCampaignStatusSent, campaign.Status)
	assert.Equal(t, entity.EmailRecipientStatusSent, stub.recipientStatuses["failed-success-recipient"])
	assert.Equal(t, entity.EmailRecipientStatusFailed, stub.recipientStatuses["failed-again-recipient"])
	assert.Equal(t, entity.EmailRecipientStatusSent, stub.recipientStatuses["sent-recipient"])
	assert.Equal(t, entity.EmailRecipientStatusSkipped, stub.recipientStatuses["skipped-recipient"])
	assert.Equal(t, "4", campaign.Metadata[campaignMetadataDeliveryTotal])
	assert.Equal(t, "2", campaign.Metadata[campaignMetadataDeliverySent])
	assert.Equal(t, "1", campaign.Metadata[campaignMetadataDeliveryFailed])
	assert.Equal(t, "1", campaign.Metadata[campaignMetadataDeliverySkipped])
	assert.Equal(t, "2", campaign.Metadata[campaignMetadataRetryTotal])
	assert.Equal(t, "1", campaign.Metadata[campaignMetadataRetrySent])
	assert.Equal(t, "1", campaign.Metadata[campaignMetadataRetryFailed])
	assert.Equal(t, "0", campaign.Metadata[campaignMetadataRetrySkipped])
	assert.Contains(t, sender.sent[0].Text, "https://frontend.example.com/unsubscribe?token=failed")
}

func TestDeliveryEventsPassesFilterToRepo(t *testing.T) {
	t.Parallel()

	occurredAt := time.Now().UTC()
	store := &emailRepoStub{
		deliveryEvents: []entity.EmailDeliveryEvent{
			{
				ID:                "event-id",
				DedupeKey:         "dedupe-key",
				Provider:          entity.EmailProviderCloudflare,
				EventType:         entity.EmailDeliveryEventBounceHard,
				RecipientEmail:    "user@example.com",
				MessageID:         "message-id",
				CampaignID:        "campaign-id",
				CampaignRecipient: "recipient-id",
				RawPayload:        entity.RawJSON(`{"provider":"cloudflare"}`),
				OccurredAt:        occurredAt,
				CreatedAt:         occurredAt,
			},
		},
	}
	uc := New(store, nil, Options{})
	filter := repo.EmailDeliveryEventFilter{
		Provider:            entity.EmailProviderCloudflare,
		EventType:           entity.EmailDeliveryEventBounceHard,
		Email:               "USER@example.com",
		MessageID:           "message-id",
		CampaignID:          "campaign-id",
		CampaignRecipientID: "recipient-id",
		Limit:               25,
		Offset:              5,
	}

	events, total, err := uc.DeliveryEvents(t.Context(), filter)

	require.NoError(t, err)
	assert.Equal(t, filter, store.deliveryEventFilter)
	assert.Equal(t, 1, total)
	require.Len(t, events, 1)
	assert.Equal(t, "event-id", events[0].ID)
}

func TestDeliveryEventsNormalizesPagination(t *testing.T) {
	t.Parallel()

	store := &emailRepoStub{}
	uc := New(store, nil, Options{})

	_, _, err := uc.DeliveryEvents(t.Context(), repo.EmailDeliveryEventFilter{})
	require.NoError(t, err)
	assert.Equal(t, uint64(50), store.deliveryEventFilter.Limit)
	assert.Equal(t, uint64(0), store.deliveryEventFilter.Offset)

	_, _, err = uc.DeliveryEvents(t.Context(), repo.EmailDeliveryEventFilter{Limit: 101, Offset: 7})
	require.NoError(t, err)
	assert.Equal(t, uint64(100), store.deliveryEventFilter.Limit)
	assert.Equal(t, uint64(7), store.deliveryEventFilter.Offset)
}

func TestCampaignDeliveryEventSummaryEmpty(t *testing.T) {
	t.Parallel()

	store := &emailRepoStub{}
	uc := New(store, nil, Options{})

	summary, err := uc.CampaignDeliveryEventSummary(t.Context(), "campaign-id")

	require.NoError(t, err)
	assert.Equal(t, "campaign-id", store.deliverySummaryCampaignID)
	assert.Equal(t, "campaign-id", summary.CampaignID)
	assert.Zero(t, summary.Total)
	assert.Zero(t, summary.BounceHard)
	assert.Zero(t, summary.Complaint)
	assert.Zero(t, summary.UniqueRecipients)
	assert.Nil(t, summary.LastOccurredAt)
}

func transactionalMessageLogForTest(
	id string,
	email string,
	attempts int,
	critical bool,
) entity.EmailMessageLog {
	scheduledAt := time.Now().UTC().Add(-time.Minute)

	return entity.EmailMessageLog{
		ID:             id,
		Category:       entity.EmailCategoryTransactional,
		TemplateKey:    entity.EmailTemplateKeyVerification,
		RecipientEmail: email,
		Lang:           contentlang.Default,
		Subject:        "Verify",
		HTML:           "<p>Verify</p>",
		Text:           "Verify",
		Critical:       critical,
		Headers:        map[string]string{"X-Test": "value"},
		Status:         entity.EmailMessageStatusQueued,
		Attempts:       attempts,
		ScheduledAt:    &scheduledAt,
		CreatedAt:      scheduledAt.Add(-time.Minute),
		UpdatedAt:      scheduledAt,
	}
}

type emailRepoStub struct {
	repo.EmailRepo

	template                     entity.EmailTemplate
	eventSetting                 entity.EmailEventSetting
	createdVersion               entity.EmailTemplateVersion
	publishedVersion             entity.EmailTemplateVersion
	suppressed                   bool
	recipientStatus              string
	recipientError               string
	createdMessages              []entity.EmailMessageLog
	updatedMessages              []entity.EmailMessageLog
	scheduledRetries             []entity.EmailMessageLog
	claimedTransactionalMessages []entity.EmailMessageLog
	claimNow                     time.Time
	claimLimit                   int
	claimVisibilityTimeout       time.Duration
	deliveryEvents               []entity.EmailDeliveryEvent
	deliveryEventFilter          repo.EmailDeliveryEventFilter
	deliveryEventTotal           int
	deliverySummaryCampaignID    string
	deliverySummary              entity.EmailCampaignDeliveryEventSummary
	deliveryEventKeys            map[string]bool
	suppressions                 []entity.EmailSuppression
	suppressionByKey             map[string]entity.EmailSuppression
	campaign                     entity.EmailCampaign
	updatedCampaign              entity.EmailCampaign
	audience                     []entity.EmailAudienceRecipient
	recipients                   []entity.EmailCampaignRecipient
	recipientStatuses            map[string]string
	recipientErrors              map[string]string
}

type emailSenderStub struct {
	sent          []entity.EmailMessage
	result        entity.EmailSendResult
	resultByEmail map[string]entity.EmailSendResult
	err           error
	errByEmail    map[string]error
}

func (s *emailSenderStub) Send(ctx context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
	if err := ctx.Err(); err != nil {
		return entity.EmailSendResult{}, err
	}
	s.sent = append(s.sent, message)
	result := s.result
	if result.Provider == "" {
		result.Provider = entity.EmailProviderLog
	}
	if result.ProviderResponse == "" {
		result.ProviderResponse = "sent"
	}
	if len(result.Delivered) == 0 && len(result.Queued) == 0 && len(result.PermanentBounces) == 0 {
		result.Delivered = []string{message.To}
	}
	if emailResult, ok := s.resultByEmail[message.To]; ok {
		result = emailResult
	}
	if err := s.errByEmail[message.To]; err != nil {
		return result, err
	}

	return result, s.err
}

func (s *emailRepoStub) CreateEmailTemplateVersion(
	_ context.Context,
	version entity.EmailTemplateVersion,
) (entity.EmailTemplateVersion, error) {
	if version.ID == "" {
		version.ID = "version-id"
	}
	s.createdVersion = version

	return version, nil
}

func (s *emailRepoStub) GetEmailTemplateByID(
	_ context.Context,
	id string,
) (entity.EmailTemplate, error) {
	if s.template.ID == id {
		return s.template, nil
	}

	return entity.EmailTemplate{}, entity.ErrEmailTemplateNotFound
}

func (s *emailRepoStub) GetEmailEventSetting(
	_ context.Context,
	key string,
) (entity.EmailEventSetting, error) {
	if s.eventSetting.Key == key {
		return s.eventSetting, nil
	}

	return entity.EmailEventSetting{}, entity.ErrEmailEventSettingNotFound
}

func (s *emailRepoStub) GetPublishedEmailTemplateVersion(
	_ context.Context,
	templateKey,
	lang string,
) (entity.EmailTemplateVersion, entity.EmailTemplate, error) {
	if s.template.Key == templateKey && s.publishedVersion.Lang == lang {
		return s.publishedVersion, s.template, nil
	}

	return entity.EmailTemplateVersion{}, entity.EmailTemplate{}, entity.ErrEmailTemplateVersionNotFound
}

func (s *emailRepoStub) GetLatestEmailTemplateVersion(
	_ context.Context,
	templateID,
	lang string,
) (entity.EmailTemplateVersion, error) {
	if s.publishedVersion.TemplateID == templateID && s.publishedVersion.Lang == lang {
		return s.publishedVersion, nil
	}

	return entity.EmailTemplateVersion{}, entity.ErrEmailTemplateVersionNotFound
}

func (s *emailRepoStub) IsEmailSuppressed(
	context.Context,
	string,
	string,
) (bool, error) {
	return s.suppressed, nil
}

func (s *emailRepoStub) UpsertAutomaticEmailSuppression(
	_ context.Context,
	suppression entity.EmailSuppression,
) (entity.EmailSuppression, error) {
	if s.suppressionByKey == nil {
		s.suppressionByKey = map[string]entity.EmailSuppression{}
	}
	key := strings.ToLower(suppression.Email) + ":" + suppression.Scope
	if existing, ok := s.suppressionByKey[key]; ok {
		if existing.CreatedBy != nil {
			return existing, nil
		}
		existing.Email = suppression.Email
		existing.Reason = suppression.Reason
		s.suppressionByKey[key] = existing

		return existing, nil
	}
	s.suppressionByKey[key] = suppression
	s.suppressions = append(s.suppressions, suppression)

	return suppression, nil
}

func (s *emailRepoStub) UpsertEmailDeliveryEvent(
	_ context.Context,
	event entity.EmailDeliveryEvent,
) (entity.EmailDeliveryEvent, bool, error) {
	if s.deliveryEventKeys == nil {
		s.deliveryEventKeys = map[string]bool{}
	}
	if s.deliveryEventKeys[event.DedupeKey] {
		return event, false, nil
	}
	s.deliveryEventKeys[event.DedupeKey] = true
	s.deliveryEvents = append(s.deliveryEvents, event)

	return event, true, nil
}

func (s *emailRepoStub) ListEmailDeliveryEvents(
	_ context.Context,
	filter repo.EmailDeliveryEventFilter,
) ([]entity.EmailDeliveryEvent, int, error) {
	s.deliveryEventFilter = filter
	total := s.deliveryEventTotal
	if total == 0 {
		total = len(s.deliveryEvents)
	}

	return append([]entity.EmailDeliveryEvent(nil), s.deliveryEvents...), total, nil
}

func (s *emailRepoStub) GetEmailCampaignDeliveryEventSummary(
	_ context.Context,
	campaignID string,
) (entity.EmailCampaignDeliveryEventSummary, error) {
	s.deliverySummaryCampaignID = campaignID
	summary := s.deliverySummary
	if summary.CampaignID == "" {
		summary.CampaignID = campaignID
	}

	return summary, nil
}

func (s *emailRepoStub) CreateEmailMessage(
	_ context.Context,
	message entity.EmailMessageLog,
) (entity.EmailMessageLog, error) {
	if message.ID == "" {
		message.ID = fmt.Sprintf("message-id-%d", len(s.createdMessages)+1)
	}
	s.createdMessages = append(s.createdMessages, message)

	return message, nil
}

func (s *emailRepoStub) UpdateEmailMessageStatus(
	_ context.Context,
	id string,
	status string,
	attempts int,
	providerResponse string,
	deliveryError string,
	sentAt *time.Time,
) (entity.EmailMessageLog, error) {
	for idx := range s.createdMessages {
		if s.createdMessages[idx].ID != id {
			continue
		}
		updated := s.createdMessages[idx]
		updated.Status = status
		updated.Attempts = attempts
		updated.ProviderResponse = providerResponse
		updated.Error = deliveryError
		updated.SentAt = sentAt
		updated.UpdatedAt = time.Now().UTC()
		s.createdMessages[idx] = updated
		s.updatedMessages = append(s.updatedMessages, updated)

		return updated, nil
	}
	for idx := range s.claimedTransactionalMessages {
		if s.claimedTransactionalMessages[idx].ID != id {
			continue
		}
		updated := s.claimedTransactionalMessages[idx]
		updated.Status = status
		updated.Attempts = attempts
		updated.ProviderResponse = providerResponse
		updated.Error = deliveryError
		updated.ScheduledAt = nil
		updated.SentAt = sentAt
		updated.UpdatedAt = time.Now().UTC()
		s.claimedTransactionalMessages[idx] = updated
		s.updatedMessages = append(s.updatedMessages, updated)

		return updated, nil
	}

	return entity.EmailMessageLog{}, entity.ErrEmailMessageNotFound
}

func (s *emailRepoStub) ScheduleEmailMessageRetry(
	_ context.Context,
	id string,
	attempts int,
	providerResponse string,
	deliveryError string,
	scheduledAt time.Time,
) (entity.EmailMessageLog, error) {
	updateMessage := func(message entity.EmailMessageLog) entity.EmailMessageLog {
		message.Status = entity.EmailMessageStatusQueued
		message.Attempts = attempts
		message.ProviderResponse = providerResponse
		message.Error = deliveryError
		message.ScheduledAt = &scheduledAt
		message.SentAt = nil
		message.UpdatedAt = time.Now().UTC()

		return message
	}
	for idx := range s.createdMessages {
		if s.createdMessages[idx].ID != id {
			continue
		}
		updated := updateMessage(s.createdMessages[idx])
		s.createdMessages[idx] = updated
		s.scheduledRetries = append(s.scheduledRetries, updated)
		s.updatedMessages = append(s.updatedMessages, updated)

		return updated, nil
	}
	for idx := range s.claimedTransactionalMessages {
		if s.claimedTransactionalMessages[idx].ID != id {
			continue
		}
		updated := updateMessage(s.claimedTransactionalMessages[idx])
		s.claimedTransactionalMessages[idx] = updated
		s.scheduledRetries = append(s.scheduledRetries, updated)
		s.updatedMessages = append(s.updatedMessages, updated)

		return updated, nil
	}

	return entity.EmailMessageLog{}, entity.ErrEmailMessageNotFound
}

func (s *emailRepoStub) ClaimDueTransactionalEmailMessages(
	_ context.Context,
	now time.Time,
	limit int,
	visibilityTimeout time.Duration,
) ([]entity.EmailMessageLog, error) {
	s.claimNow = now
	s.claimLimit = limit
	s.claimVisibilityTimeout = visibilityTimeout

	return append([]entity.EmailMessageLog(nil), s.claimedTransactionalMessages...), nil
}

func (s *emailRepoStub) GetEmailCampaign(
	_ context.Context,
	id string,
) (entity.EmailCampaign, error) {
	if s.campaign.ID == id {
		return s.campaign, nil
	}

	return entity.EmailCampaign{}, entity.ErrEmailCampaignNotFound
}

func (s *emailRepoStub) ClaimEmailCampaignForSending(
	_ context.Context,
	id,
	actorID string,
) (entity.EmailCampaign, error) {
	if s.campaign.ID != id {
		return entity.EmailCampaign{}, entity.ErrEmailCampaignNotFound
	}
	if s.campaign.Status != entity.EmailCampaignStatusDraft &&
		s.campaign.Status != entity.EmailCampaignStatusScheduled {
		return entity.EmailCampaign{}, entity.ErrInvalidEmailCampaign
	}
	s.campaign.Status = entity.EmailCampaignStatusSending
	s.campaign.UpdatedBy = nullableActor(actorID)

	return s.campaign, nil
}

func (s *emailRepoStub) ClaimEmailCampaignForRetry(
	_ context.Context,
	id,
	actorID string,
) (entity.EmailCampaign, error) {
	if s.campaign.ID != id {
		return entity.EmailCampaign{}, entity.ErrEmailCampaignNotFound
	}
	if s.campaign.Status != entity.EmailCampaignStatusSent {
		return entity.EmailCampaign{}, entity.ErrInvalidEmailCampaign
	}
	s.campaign.Status = entity.EmailCampaignStatusSending
	s.campaign.UpdatedBy = nullableActor(actorID)

	return s.campaign, nil
}

func (s *emailRepoStub) UpdateEmailCampaign(
	_ context.Context,
	campaign entity.EmailCampaign,
) (entity.EmailCampaign, error) {
	if s.campaign.ID != "" && s.campaign.ID != campaign.ID {
		return entity.EmailCampaign{}, entity.ErrEmailCampaignNotFound
	}
	s.campaign = campaign
	s.updatedCampaign = campaign

	return campaign, nil
}

func (s *emailRepoStub) ListMarketingAudience(
	_ context.Context,
	filter entity.EmailAudienceFilter,
) ([]entity.EmailAudienceRecipient, int, error) {
	total := len(s.audience)
	if filter.Limit > 0 && filter.Limit < len(s.audience) {
		return append([]entity.EmailAudienceRecipient(nil), s.audience[:filter.Limit]...), total, nil
	}

	return append([]entity.EmailAudienceRecipient(nil), s.audience...), total, nil
}

func (s *emailRepoStub) ReplaceEmailCampaignRecipients(
	_ context.Context,
	campaignID string,
	recipients []entity.EmailCampaignRecipient,
) error {
	s.recipients = append([]entity.EmailCampaignRecipient(nil), recipients...)
	s.recipientStatuses = map[string]string{}
	s.recipientErrors = map[string]string{}
	for _, recipient := range recipients {
		if recipient.CampaignID != campaignID {
			continue
		}
		s.recipientStatuses[recipient.ID] = recipient.Status
	}

	return nil
}

func (s *emailRepoStub) resetRecipientStatusMaps() {
	s.recipientStatuses = map[string]string{}
	s.recipientErrors = map[string]string{}
	for _, recipient := range s.recipients {
		s.recipientStatuses[recipient.ID] = recipient.Status
		s.recipientErrors[recipient.ID] = recipient.Error
	}
}

func (s *emailRepoStub) ListEmailCampaignRecipients(
	_ context.Context,
	campaignID string,
	status string,
	limit int,
) ([]entity.EmailCampaignRecipient, error) {
	if limit <= 0 {
		limit = len(s.recipients)
	}
	recipients := make([]entity.EmailCampaignRecipient, 0, min(limit, len(s.recipients)))
	for _, recipient := range s.recipients {
		if recipient.CampaignID != campaignID {
			continue
		}
		currentStatus := s.recipientStatuses[recipient.ID]
		if currentStatus == "" {
			currentStatus = recipient.Status
		}
		if status != "" && currentStatus != status {
			continue
		}
		recipient.Status = currentStatus
		recipient.Error = s.recipientErrors[recipient.ID]
		recipients = append(recipients, recipient)
		if len(recipients) == limit {
			break
		}
	}

	return recipients, nil
}

func (s *emailRepoStub) ListEmailCampaignRecipientsForRetry(
	_ context.Context,
	campaignID string,
	cutoff time.Time,
	limit int,
) ([]entity.EmailCampaignRecipient, error) {
	if limit <= 0 {
		limit = len(s.recipients)
	}
	recipients := make([]entity.EmailCampaignRecipient, 0, min(limit, len(s.recipients)))
	for _, recipient := range s.recipients {
		if recipient.CampaignID != campaignID {
			continue
		}
		currentStatus := s.recipientStatuses[recipient.ID]
		if currentStatus == "" {
			currentStatus = recipient.Status
		}
		if currentStatus != entity.EmailRecipientStatusFailed || !recipient.UpdatedAt.Before(cutoff) {
			continue
		}
		recipient.Status = currentStatus
		recipient.Error = s.recipientErrors[recipient.ID]
		recipients = append(recipients, recipient)
		if len(recipients) == limit {
			break
		}
	}

	return recipients, nil
}

func (s *emailRepoStub) CountEmailCampaignRecipientsByStatus(
	_ context.Context,
	campaignID string,
) (map[string]int, error) {
	counts := map[string]int{}
	for _, recipient := range s.recipients {
		if recipient.CampaignID != campaignID {
			continue
		}
		currentStatus := s.recipientStatuses[recipient.ID]
		if currentStatus == "" {
			currentStatus = recipient.Status
		}
		counts[currentStatus]++
	}

	return counts, nil
}

func (s *emailRepoStub) UpdateEmailCampaignRecipientStatus(
	_ context.Context,
	id string,
	status string,
	messageID string,
	deliveryError string,
	sentAt *time.Time,
) (entity.EmailCampaignRecipient, error) {
	if s.recipientStatuses == nil {
		s.recipientStatuses = map[string]string{}
	}
	if s.recipientErrors == nil {
		s.recipientErrors = map[string]string{}
	}
	s.recipientStatus = status
	s.recipientError = deliveryError
	s.recipientStatuses[id] = status
	s.recipientErrors[id] = deliveryError
	for idx := range s.recipients {
		if s.recipients[idx].ID != id {
			continue
		}
		s.recipients[idx].Status = status
		s.recipients[idx].MessageID = messageID
		s.recipients[idx].Error = deliveryError
		s.recipients[idx].SentAt = sentAt
		s.recipients[idx].UpdatedAt = time.Now().UTC()

		return s.recipients[idx], nil
	}

	return entity.EmailCampaignRecipient{ID: id, Status: status, MessageID: messageID, Error: deliveryError}, nil
}
