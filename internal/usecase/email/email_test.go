package email

import (
	"context"
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
		UnsubscribeURL:       "https://frontend.example.com/unsubscribe",
		UnsubscribeTokenSeed: "secret",
	})
	token := uc.unsubscribeToken("user-id", "USER@example.com")
	userID, email, err := uc.parseUnsubscribeToken(token)

	require.NoError(t, err)
	assert.Equal(t, "user-id", userID)
	assert.Equal(t, "user@example.com", email)
	assert.Contains(t, uc.unsubscribeLink(token), "https://frontend.example.com/unsubscribe?token=")

	_, _, err = uc.parseUnsubscribeToken(token + "tampered")
	require.ErrorIs(t, err, entity.ErrInvalidUnsubscribeToken)
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
	err := uc.deliverCampaignRecipient(
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
	assert.Equal(t, entity.EmailRecipientStatusSkipped, stub.recipientStatus)
	assert.Equal(t, "suppressed", stub.recipientError)
}

type emailRepoStub struct {
	repo.EmailRepo

	template         entity.EmailTemplate
	createdVersion   entity.EmailTemplateVersion
	publishedVersion entity.EmailTemplateVersion
	suppressed       bool
	recipientStatus  string
	recipientError   string
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
		message.ID = "message-id"
	}

	return message, nil
}

func (s *emailRepoStub) UpdateEmailCampaignRecipientStatus(
	_ context.Context,
	_ string,
	status string,
	_ string,
	deliveryError string,
	_ *time.Time,
) (entity.EmailCampaignRecipient, error) {
	s.recipientStatus = status
	s.recipientError = deliveryError

	return entity.EmailCampaignRecipient{Status: status, Error: deliveryError}, nil
}
