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
	uc := New(stub, nil, Options{})
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
}

func TestSendTransactionalRedactsSensitiveMetadata(t *testing.T) {
	t.Parallel()

	stub := &emailRepoStub{}
	sender := &emailSenderStub{}
	uc := New(stub, sender, Options{})

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

type emailRepoStub struct {
	repo.EmailRepo

	template          entity.EmailTemplate
	eventSetting      entity.EmailEventSetting
	createdVersion    entity.EmailTemplateVersion
	publishedVersion  entity.EmailTemplateVersion
	suppressed        bool
	recipientStatus   string
	recipientError    string
	createdMessages   []entity.EmailMessageLog
	updatedMessages   []entity.EmailMessageLog
	campaign          entity.EmailCampaign
	updatedCampaign   entity.EmailCampaign
	audience          []entity.EmailAudienceRecipient
	recipients        []entity.EmailCampaignRecipient
	recipientStatuses map[string]string
	recipientErrors   map[string]string
}

type emailSenderStub struct {
	sent       []entity.EmailMessage
	err        error
	errByEmail map[string]error
}

func (s *emailSenderStub) Send(ctx context.Context, message entity.EmailMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.sent = append(s.sent, message)
	if err := s.errByEmail[message.To]; err != nil {
		return err
	}

	return s.err
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

func (s *emailRepoStub) IsEmailSuppressed(
	context.Context,
	string,
	string,
) (bool, error) {
	return s.suppressed, nil
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

	return entity.EmailMessageLog{}, entity.ErrEmailMessageNotFound
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
