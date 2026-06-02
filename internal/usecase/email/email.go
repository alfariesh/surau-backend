package email

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"net/url"
	"strings"
	"text/template"
	"time"

	"github.com/evrone/go-clean-template/internal/contentlang"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/google/uuid"
)

const (
	defaultSupportEmail = "support@surau.org"
	defaultBatchSize    = 500
)

// Options configures the admin-managed email service.
type Options struct {
	SupportEmail         string
	UnsubscribeURL       string
	UnsubscribeTokenSeed string
}

// UseCase coordinates templates, delivery, consent, suppressions, and campaigns.
type UseCase struct {
	repo           repo.EmailRepo
	sender         repo.EmailSender
	supportEmail   string
	unsubscribeURL string
	tokenSeed      string
}

// New creates an email use case.
func New(r repo.EmailRepo, sender repo.EmailSender, opts Options) *UseCase {
	return &UseCase{
		repo:           r,
		sender:         sender,
		supportEmail:   normalizeSupportEmail(opts.SupportEmail),
		unsubscribeURL: strings.TrimSpace(opts.UnsubscribeURL),
		tokenSeed:      opts.UnsubscribeTokenSeed,
	}
}

// SendTransactional sends one admin-managed transactional email with a fallback message.
func (uc *UseCase) SendTransactional(ctx context.Context, req entity.TransactionalEmailRequest) error {
	if uc.sender == nil {
		return entity.ErrEmailDeliveryFailed
	}
	if uc.repo == nil {
		return uc.sender.Send(ctx, req.Fallback)
	}

	lang := contentlang.MustNormalize(req.Lang)
	message := req.Fallback
	message.Category = entity.EmailCategoryTransactional
	message.TemplateKey = req.Key
	message.Lang = lang
	message.UserID = req.UserID
	message.Metadata = req.Variables
	if message.To == "" {
		message.To = req.To
	}

	setting, err := uc.repo.GetEmailEventSetting(ctx, req.Key)
	if err != nil && !errors.Is(err, entity.ErrEmailEventSettingNotFound) {
		return fmt.Errorf("EmailUseCase - SendTransactional - GetEmailEventSetting: %w", err)
	}
	if err == nil && !setting.Enabled && !setting.Critical && !req.Critical {
		return uc.logSkipped(ctx, message)
	}

	version, _, err := uc.publishedVersionForKey(ctx, req.Key, lang)
	if err == nil {
		preview, renderErr := uc.render(version, req.Variables)
		if renderErr != nil {
			return renderErr
		}
		message.Subject = preview.Subject
		message.HTML = preview.HTML
		message.Text = preview.Text
		message.TemplateVersionID = version.ID
	} else if !errors.Is(err, entity.ErrEmailTemplateVersionNotFound) {
		return fmt.Errorf("EmailUseCase - SendTransactional - GetPublishedEmailTemplateVersion: %w", err)
	}

	_, err = uc.sendAndLog(ctx, message, "", "")

	return err
}

func (uc *UseCase) Templates(
	ctx context.Context,
	filter repo.EmailTemplateFilter,
) ([]entity.EmailTemplate, int, error) {
	return uc.repo.ListEmailTemplates(ctx, filter)
}

func (uc *UseCase) CreateTemplate(
	ctx context.Context,
	template entity.EmailTemplate,
) (entity.EmailTemplate, error) {
	now := time.Now().UTC()
	template.ID = uuid.New().String()
	template.Key = normalizeKey(template.Key)
	template.Name = strings.TrimSpace(template.Name)
	template.Category = strings.TrimSpace(template.Category)
	template.CreatedAt = now
	template.UpdatedAt = now
	if template.Category == "" {
		template.Category = entity.EmailCategoryMarketing
	}
	if template.Key == "" || template.Name == "" || !validCategory(template.Category) {
		return entity.EmailTemplate{}, entity.ErrInvalidEmailTemplate
	}
	if template.Category == entity.EmailCategoryMarketing {
		template.Critical = false
	}

	return uc.repo.CreateEmailTemplate(ctx, template)
}

func (uc *UseCase) Template(ctx context.Context, id string) (entity.EmailTemplate, error) {
	return uc.repo.GetEmailTemplateByID(ctx, id)
}

func (uc *UseCase) UpdateTemplate(
	ctx context.Context,
	id string,
	patch entity.EmailTemplatePatch,
) (entity.EmailTemplate, error) {
	if patch.Name != nil && strings.TrimSpace(*patch.Name) == "" {
		return entity.EmailTemplate{}, entity.ErrInvalidEmailTemplate
	}

	return uc.repo.UpdateEmailTemplate(ctx, id, patch)
}

func (uc *UseCase) DeleteTemplate(ctx context.Context, id string) error {
	return uc.repo.DeleteEmailTemplate(ctx, id)
}

func (uc *UseCase) CreateVersion(
	ctx context.Context,
	version entity.EmailTemplateVersion,
) (entity.EmailTemplateVersion, error) {
	if err := validateTemplateVersion(version); err != nil {
		return entity.EmailTemplateVersion{}, err
	}
	now := time.Now().UTC()
	version.ID = uuid.New().String()
	version.Lang = contentlang.MustNormalize(version.Lang)
	version.RequiredVariables = normalizeVariables(version.RequiredVariables)
	version.Published = false
	version.CreatedAt = now
	version.UpdatedAt = now

	return uc.repo.CreateEmailTemplateVersion(ctx, version)
}

func (uc *UseCase) Versions(ctx context.Context, templateID string) ([]entity.EmailTemplateVersion, error) {
	return uc.repo.ListEmailTemplateVersions(ctx, templateID)
}

func (uc *UseCase) UpdateVersion(
	ctx context.Context,
	id string,
	patch entity.EmailTemplateVersionPatch,
) (entity.EmailTemplateVersion, error) {
	if patch.RequiredVariables != nil {
		normalized := normalizeVariables(*patch.RequiredVariables)
		patch.RequiredVariables = &normalized
	}

	return uc.repo.UpdateEmailTemplateVersion(ctx, id, patch)
}

func (uc *UseCase) PublishVersion(ctx context.Context, id, actorID string) (entity.EmailTemplateVersion, error) {
	version, err := uc.repo.GetEmailTemplateVersionByID(ctx, id)
	if err != nil {
		return entity.EmailTemplateVersion{}, err
	}
	template, err := uc.repo.GetEmailTemplateByID(ctx, version.TemplateID)
	if err != nil {
		return entity.EmailTemplateVersion{}, err
	}
	if template.Category == entity.EmailCategoryTransactional {
		if err = uc.ensureTransactionalCoverage(ctx, template.ID, version.Lang); err != nil {
			return entity.EmailTemplateVersion{}, err
		}
	} else if version.Lang != contentlang.Default {
		if _, err = uc.repo.GetLatestEmailTemplateVersion(ctx, template.ID, contentlang.Default); err != nil {
			return entity.EmailTemplateVersion{}, err
		}
	}

	return uc.repo.PublishEmailTemplateVersion(ctx, id, actorID)
}

func (uc *UseCase) PreviewTemplate(
	ctx context.Context,
	templateID,
	lang string,
	variables map[string]string,
) (entity.EmailPreview, error) {
	lang = contentlang.MustNormalize(lang)
	version, err := uc.repo.GetLatestEmailTemplateVersion(ctx, templateID, lang)
	if err != nil {
		return entity.EmailPreview{}, err
	}

	return uc.render(version, variables)
}

func (uc *UseCase) TestSendTemplate(
	ctx context.Context,
	templateID,
	lang,
	to string,
	variables map[string]string,
) (entity.EmailMessageLog, error) {
	if !validEmail(to) {
		return entity.EmailMessageLog{}, entity.ErrInvalidAuthInput
	}
	lang = contentlang.MustNormalize(lang)
	version, err := uc.repo.GetLatestEmailTemplateVersion(ctx, templateID, lang)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}
	template, err := uc.repo.GetEmailTemplateByID(ctx, templateID)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}
	preview, err := uc.render(version, variables)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}

	message := entity.EmailMessage{
		To:                to,
		Subject:           preview.Subject,
		HTML:              preview.HTML,
		Text:              preview.Text,
		Category:          template.Category,
		TemplateVersionID: version.ID,
		Lang:              lang,
		Metadata:          variables,
	}

	return uc.sendAndLog(ctx, message, "", "")
}

func (uc *UseCase) EventSetting(ctx context.Context, key string) (entity.EmailEventSetting, error) {
	return uc.repo.GetEmailEventSetting(ctx, normalizeKey(key))
}

func (uc *UseCase) UpdateEventSetting(
	ctx context.Context,
	key string,
	patch entity.EmailEventSettingPatch,
) (entity.EmailEventSetting, error) {
	setting, err := uc.repo.GetEmailEventSetting(ctx, normalizeKey(key))
	if err != nil {
		return entity.EmailEventSetting{}, err
	}
	if setting.Critical && patch.Enabled != nil && !*patch.Enabled {
		return entity.EmailEventSetting{}, entity.ErrInvalidEmailTemplate
	}

	return uc.repo.UpdateEmailEventSetting(ctx, normalizeKey(key), patch)
}

func (uc *UseCase) Messages(
	ctx context.Context,
	filter repo.EmailMessageFilter,
) ([]entity.EmailMessageLog, int, error) {
	return uc.repo.ListEmailMessages(ctx, filter)
}

func (uc *UseCase) Subscription(ctx context.Context, userID string) (entity.EmailSubscription, error) {
	subscription, err := uc.repo.GetEmailSubscription(ctx, userID)
	if errors.Is(err, entity.ErrEmailSubscriptionNotFound) {
		return entity.EmailSubscription{UserID: userID, MarketingOptIn: false}, nil
	}

	return subscription, err
}

func (uc *UseCase) UpdateSubscription(
	ctx context.Context,
	userID string,
	marketingOptIn bool,
	source string,
) (entity.EmailSubscription, error) {
	return uc.repo.UpsertEmailSubscription(ctx, entity.EmailSubscription{
		UserID:         userID,
		MarketingOptIn: marketingOptIn,
		Source:         source,
	})
}

func (uc *UseCase) Suppressions(
	ctx context.Context,
	filter repo.EmailSuppressionFilter,
) ([]entity.EmailSuppression, int, error) {
	return uc.repo.ListEmailSuppressions(ctx, filter)
}

func (uc *UseCase) CreateSuppression(
	ctx context.Context,
	suppression entity.EmailSuppression,
) (entity.EmailSuppression, error) {
	if !validEmail(suppression.Email) {
		return entity.EmailSuppression{}, entity.ErrInvalidAuthInput
	}
	if !validSuppressionScope(suppression.Scope) {
		return entity.EmailSuppression{}, entity.ErrInvalidAuthInput
	}
	now := time.Now().UTC()
	suppression.ID = uuid.New().String()
	suppression.Email = strings.ToLower(strings.TrimSpace(suppression.Email))
	suppression.CreatedAt = now

	return uc.repo.CreateEmailSuppression(ctx, suppression)
}

func (uc *UseCase) DeleteSuppression(ctx context.Context, id string) error {
	return uc.repo.DeleteEmailSuppression(ctx, id)
}

func (uc *UseCase) Campaigns(
	ctx context.Context,
	filter repo.EmailCampaignFilter,
) ([]entity.EmailCampaign, int, error) {
	return uc.repo.ListEmailCampaigns(ctx, filter)
}

func (uc *UseCase) CreateCampaign(
	ctx context.Context,
	campaign entity.EmailCampaign,
) (entity.EmailCampaign, error) {
	if err := validateCampaign(campaign); err != nil {
		return entity.EmailCampaign{}, err
	}
	if err := uc.ensureMarketingTemplate(ctx, campaign.TemplateID); err != nil {
		return entity.EmailCampaign{}, err
	}
	now := time.Now().UTC()
	campaign.ID = uuid.New().String()
	campaign.Status = entity.EmailCampaignStatusDraft
	campaign.CreatedAt = now
	campaign.UpdatedAt = now

	return uc.repo.CreateEmailCampaign(ctx, campaign)
}

func (uc *UseCase) Campaign(ctx context.Context, id string) (entity.EmailCampaign, error) {
	return uc.repo.GetEmailCampaign(ctx, id)
}

func (uc *UseCase) UpdateCampaign(
	ctx context.Context,
	campaign entity.EmailCampaign,
) (entity.EmailCampaign, error) {
	stored, err := uc.repo.GetEmailCampaign(ctx, campaign.ID)
	if err != nil {
		return entity.EmailCampaign{}, err
	}
	if stored.Status != entity.EmailCampaignStatusDraft {
		return entity.EmailCampaign{}, entity.ErrInvalidEmailCampaign
	}
	if err = validateCampaign(campaign); err != nil {
		return entity.EmailCampaign{}, err
	}
	if err = uc.ensureMarketingTemplate(ctx, campaign.TemplateID); err != nil {
		return entity.EmailCampaign{}, err
	}
	campaign.Status = stored.Status
	campaign.CreatedBy = stored.CreatedBy
	campaign.CreatedAt = stored.CreatedAt
	campaign.UpdatedAt = time.Now().UTC()

	return uc.repo.UpdateEmailCampaign(ctx, campaign)
}

func (uc *UseCase) PreviewAudience(
	ctx context.Context,
	filter entity.EmailAudienceFilter,
) ([]entity.EmailAudienceRecipient, int, error) {
	return uc.repo.ListMarketingAudience(ctx, filter)
}

func (uc *UseCase) ScheduleCampaign(
	ctx context.Context,
	id,
	actorID string,
	scheduledAt time.Time,
) (entity.EmailCampaign, error) {
	if !scheduledAt.After(time.Now().UTC()) {
		return entity.EmailCampaign{}, entity.ErrInvalidEmailCampaign
	}
	campaign, err := uc.repo.GetEmailCampaign(ctx, id)
	if err != nil {
		return entity.EmailCampaign{}, err
	}
	if campaign.Status != entity.EmailCampaignStatusDraft {
		return entity.EmailCampaign{}, entity.ErrInvalidEmailCampaign
	}
	if err = uc.ensureMarketingTemplate(ctx, campaign.TemplateID); err != nil {
		return entity.EmailCampaign{}, err
	}
	if err = uc.prepareCampaignRecipients(ctx, campaign); err != nil {
		return entity.EmailCampaign{}, err
	}
	campaign.Status = entity.EmailCampaignStatusScheduled
	campaign.ScheduledAt = &scheduledAt
	campaign.UpdatedBy = nullableActor(actorID)

	return uc.repo.UpdateEmailCampaign(ctx, campaign)
}

func (uc *UseCase) SendCampaignNow(ctx context.Context, id, actorID string) (entity.EmailCampaign, error) {
	campaign, err := uc.repo.GetEmailCampaign(ctx, id)
	if err != nil {
		return entity.EmailCampaign{}, err
	}
	switch campaign.Status {
	case entity.EmailCampaignStatusDraft, entity.EmailCampaignStatusScheduled:
	default:
		return entity.EmailCampaign{}, entity.ErrInvalidEmailCampaign
	}

	if err = uc.ensureMarketingTemplate(ctx, campaign.TemplateID); err != nil {
		return entity.EmailCampaign{}, err
	}
	if err = uc.prepareCampaignRecipients(ctx, campaign); err != nil {
		return entity.EmailCampaign{}, err
	}
	campaign.Status = entity.EmailCampaignStatusSending
	campaign.UpdatedBy = nullableActor(actorID)
	campaign, err = uc.repo.UpdateEmailCampaign(ctx, campaign)
	if err != nil {
		return entity.EmailCampaign{}, err
	}
	if err = uc.deliverCampaign(ctx, campaign); err != nil {
		return entity.EmailCampaign{}, err
	}

	now := time.Now().UTC()
	campaign.Status = entity.EmailCampaignStatusSent
	campaign.SentAt = &now
	campaign.UpdatedBy = nullableActor(actorID)

	return uc.repo.UpdateEmailCampaign(ctx, campaign)
}

func (uc *UseCase) CancelCampaign(ctx context.Context, id, actorID string) (entity.EmailCampaign, error) {
	campaign, err := uc.repo.GetEmailCampaign(ctx, id)
	if err != nil {
		return entity.EmailCampaign{}, err
	}
	if campaign.Status == entity.EmailCampaignStatusSent ||
		campaign.Status == entity.EmailCampaignStatusCancelled {
		return entity.EmailCampaign{}, entity.ErrInvalidEmailCampaign
	}
	now := time.Now().UTC()
	campaign.Status = entity.EmailCampaignStatusCancelled
	campaign.CancelledAt = &now
	campaign.UpdatedBy = nullableActor(actorID)

	return uc.repo.UpdateEmailCampaign(ctx, campaign)
}

func (uc *UseCase) TestSendCampaign(
	ctx context.Context,
	id,
	to,
	lang string,
	variables map[string]string,
) (entity.EmailMessageLog, error) {
	if !validEmail(to) {
		return entity.EmailMessageLog{}, entity.ErrInvalidAuthInput
	}
	campaign, err := uc.repo.GetEmailCampaign(ctx, id)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}
	if err = uc.ensureMarketingTemplate(ctx, campaign.TemplateID); err != nil {
		return entity.EmailMessageLog{}, err
	}
	lang = contentlang.MustNormalize(lang)
	version, err := uc.repo.GetLatestEmailTemplateVersion(ctx, campaign.TemplateID, lang)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}
	variables = cloneMap(variables)
	variables["email"] = to
	if variables["unsubscribe_url"] == "" {
		variables["unsubscribe_url"] = uc.unsubscribeURL
	}
	preview, err := uc.render(version, variables)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}

	return uc.sendAndLog(ctx, entity.EmailMessage{
		To:                to,
		Subject:           preview.Subject,
		HTML:              preview.HTML,
		Text:              preview.Text,
		Category:          entity.EmailCategoryMarketing,
		TemplateVersionID: version.ID,
		Lang:              lang,
		Metadata:          variables,
	}, campaign.ID, "")
}

func (uc *UseCase) DispatchDueCampaigns(ctx context.Context, limit int) error {
	campaigns, err := uc.repo.ListDueEmailCampaigns(ctx, time.Now().UTC(), limit)
	if err != nil {
		return err
	}
	for _, campaign := range campaigns {
		if _, err = uc.SendCampaignNow(ctx, campaign.ID, ""); err != nil {
			return err
		}
	}

	return nil
}

func (uc *UseCase) Unsubscribe(ctx context.Context, token string) (entity.EmailSubscription, error) {
	userID, email, err := uc.parseUnsubscribeToken(token)
	if err != nil {
		return entity.EmailSubscription{}, err
	}

	return uc.repo.UnsubscribeEmail(ctx, userID, email, "unsubscribe_link")
}

func (uc *UseCase) prepareCampaignRecipients(ctx context.Context, campaign entity.EmailCampaign) error {
	audience, _, err := uc.repo.ListMarketingAudience(ctx, campaign.Audience)
	if err != nil {
		return err
	}
	recipients := make([]entity.EmailCampaignRecipient, 0, len(audience))
	now := time.Now().UTC()
	for _, item := range audience {
		token := uc.unsubscribeToken(item.UserID, item.Email)
		recipients = append(recipients, entity.EmailCampaignRecipient{
			ID:             uuid.New().String(),
			CampaignID:     campaign.ID,
			UserID:         item.UserID,
			Email:          item.Email,
			Lang:           contentlang.MustNormalize(item.Lang),
			UnsubscribeURL: uc.unsubscribeLink(token),
			Status:         entity.EmailRecipientStatusPending,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}

	return uc.repo.ReplaceEmailCampaignRecipients(ctx, campaign.ID, recipients)
}

func (uc *UseCase) deliverCampaign(ctx context.Context, campaign entity.EmailCampaign) error {
	recipients, err := uc.repo.ListEmailCampaignRecipients(
		ctx,
		campaign.ID,
		entity.EmailRecipientStatusPending,
		defaultBatchSize,
	)
	if err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err = uc.deliverCampaignRecipient(ctx, campaign, recipient); err != nil {
			return err
		}
	}

	return nil
}

func (uc *UseCase) deliverCampaignRecipient(
	ctx context.Context,
	campaign entity.EmailCampaign,
	recipient entity.EmailCampaignRecipient,
) error {
	version, err := uc.publishedVersionForTemplate(ctx, campaign.TemplateID, recipient.Lang)
	if err != nil {
		_, _ = uc.repo.UpdateEmailCampaignRecipientStatus(
			ctx,
			recipient.ID,
			entity.EmailRecipientStatusFailed,
			"",
			err.Error(),
			nil,
		)

		return nil
	}

	variables := map[string]string{
		"email":           recipient.Email,
		"lang":            recipient.Lang,
		"unsubscribe_url": recipient.UnsubscribeURL,
	}
	preview, err := uc.render(version, variables)
	if err != nil {
		_, _ = uc.repo.UpdateEmailCampaignRecipientStatus(
			ctx,
			recipient.ID,
			entity.EmailRecipientStatusFailed,
			"",
			err.Error(),
			nil,
		)

		return nil
	}

	log, err := uc.sendAndLog(ctx, entity.EmailMessage{
		To:                recipient.Email,
		Subject:           preview.Subject,
		HTML:              preview.HTML,
		Text:              preview.Text,
		Category:          entity.EmailCategoryMarketing,
		TemplateVersionID: version.ID,
		Lang:              recipient.Lang,
		UserID:            recipient.UserID,
		Metadata:          variables,
	}, campaign.ID, recipient.ID)
	if err != nil {
		_, _ = uc.repo.UpdateEmailCampaignRecipientStatus(
			ctx,
			recipient.ID,
			entity.EmailRecipientStatusFailed,
			log.ID,
			err.Error(),
			nil,
		)

		return nil
	}
	if log.Status == entity.EmailMessageStatusSkipped {
		_, err = uc.repo.UpdateEmailCampaignRecipientStatus(
			ctx,
			recipient.ID,
			entity.EmailRecipientStatusSkipped,
			log.ID,
			log.Error,
			nil,
		)

		return err
	}

	now := time.Now().UTC()
	_, err = uc.repo.UpdateEmailCampaignRecipientStatus(
		ctx,
		recipient.ID,
		entity.EmailRecipientStatusSent,
		log.ID,
		"",
		&now,
	)

	return err
}

func (uc *UseCase) sendAndLog(
	ctx context.Context,
	message entity.EmailMessage,
	campaignID,
	campaignRecipientID string,
) (entity.EmailMessageLog, error) {
	category := message.Category
	if category == "" {
		category = entity.EmailCategoryTransactional
	}
	if suppressed, err := uc.repo.IsEmailSuppressed(ctx, message.To, category); err != nil {
		return entity.EmailMessageLog{}, err
	} else if suppressed {
		messageLog, logErr := uc.createMessageLog(
			ctx,
			message,
			category,
			campaignID,
			campaignRecipientID,
			entity.EmailMessageStatusSkipped,
			0,
			"",
			"suppressed",
			nil,
		)

		return messageLog, logErr
	}

	messageLog, err := uc.createMessageLog(
		ctx,
		message,
		category,
		campaignID,
		campaignRecipientID,
		entity.EmailMessageStatusQueued,
		0,
		"",
		"",
		nil,
	)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}
	if uc.sender == nil {
		_, _ = uc.repo.UpdateEmailMessageStatus(
			ctx,
			messageLog.ID,
			entity.EmailMessageStatusFailed,
			1,
			"",
			entity.ErrEmailDeliveryFailed.Error(),
			nil,
		)

		return messageLog, entity.ErrEmailDeliveryFailed
	}

	if err = uc.sender.Send(ctx, message); err != nil {
		_, _ = uc.repo.UpdateEmailMessageStatus(
			ctx,
			messageLog.ID,
			entity.EmailMessageStatusFailed,
			1,
			"",
			err.Error(),
			nil,
		)
		if errors.Is(err, entity.ErrEmailPermanentBounce) {
			_, _ = uc.CreateSuppression(ctx, entity.EmailSuppression{
				Email:  message.To,
				Scope:  entity.EmailSuppressionScopeAll,
				Reason: "permanent_bounce",
			})
		}

		return messageLog, fmt.Errorf("%w: %w", entity.ErrEmailDeliveryFailed, err)
	}

	now := time.Now().UTC()
	messageLog, err = uc.repo.UpdateEmailMessageStatus(
		ctx,
		messageLog.ID,
		entity.EmailMessageStatusSent,
		1,
		"sent",
		"",
		&now,
	)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}

	return messageLog, nil
}

func (uc *UseCase) logSkipped(ctx context.Context, message entity.EmailMessage) error {
	_, err := uc.createMessageLog(
		ctx,
		message,
		entity.EmailCategoryTransactional,
		"",
		"",
		entity.EmailMessageStatusSkipped,
		0,
		"",
		"event disabled",
		nil,
	)

	return err
}

func (uc *UseCase) createMessageLog(
	ctx context.Context,
	message entity.EmailMessage,
	category,
	campaignID,
	campaignRecipientID,
	status string,
	attempts int,
	providerResponse,
	deliveryError string,
	sentAt *time.Time,
) (entity.EmailMessageLog, error) {
	now := time.Now().UTC()
	log := entity.EmailMessageLog{
		ID:                uuid.New().String(),
		Category:          category,
		TemplateKey:       message.TemplateKey,
		TemplateVersionID: message.TemplateVersionID,
		CampaignID:        campaignID,
		CampaignRecipient: campaignRecipientID,
		UserID:            message.UserID,
		RecipientEmail:    message.To,
		Lang:              contentlang.MustNormalize(message.Lang),
		Subject:           message.Subject,
		Status:            status,
		Attempts:          attempts,
		ProviderResponse:  providerResponse,
		Error:             deliveryError,
		Metadata:          message.Metadata,
		SentAt:            sentAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	return uc.repo.CreateEmailMessage(ctx, log)
}

func (uc *UseCase) render(
	version entity.EmailTemplateVersion,
	variables map[string]string,
) (entity.EmailPreview, error) {
	variables = cloneMap(variables)
	variables["support_email"] = uc.supportEmail
	for _, required := range version.RequiredVariables {
		if strings.TrimSpace(variables[required]) == "" {
			return entity.EmailPreview{}, fmt.Errorf("%w: missing %s", entity.ErrInvalidEmailTemplate, required)
		}
	}

	subject, err := renderTemplateString("subject", version.SubjectTemplate, variables)
	if err != nil {
		return entity.EmailPreview{}, err
	}
	preview, err := renderTemplateString("preview", version.PreviewTemplate, variables)
	if err != nil {
		return entity.EmailPreview{}, err
	}
	title, err := renderTemplateString("title", version.TitleTemplate, variables)
	if err != nil {
		return entity.EmailPreview{}, err
	}
	body, err := renderTemplateString("body", version.BodyTemplate, variables)
	if err != nil {
		return entity.EmailPreview{}, err
	}
	buttonLabel, err := renderTemplateString("button_label", version.ButtonLabelTemplate, variables)
	if err != nil {
		return entity.EmailPreview{}, err
	}
	buttonURL, err := renderTemplateString("button_url", version.ButtonURLTemplate, variables)
	if err != nil {
		return entity.EmailPreview{}, err
	}
	note, err := renderTemplateString("note", version.NoteTemplate, variables)
	if err != nil {
		return entity.EmailPreview{}, err
	}
	footer, err := renderTemplateString("footer", version.FooterTemplate, variables)
	if err != nil {
		return entity.EmailPreview{}, err
	}
	text, err := renderTemplateString("text", version.TextTemplate, variables)
	if err != nil {
		return entity.EmailPreview{}, err
	}

	return entity.EmailPreview{
		Subject: strings.TrimSpace(subject),
		HTML: emailHTML(emailView{
			Lang:         version.Lang,
			Preview:      preview,
			Title:        title,
			Body:         body,
			ButtonLabel:  buttonLabel,
			ButtonURL:    buttonURL,
			Note:         note,
			Footer:       footer,
			SupportEmail: uc.supportEmail,
		}),
		Text: textWithFooter(text, version.Lang, uc.supportEmail),
		Lang: version.Lang,
	}, nil
}

func renderTemplateString(name, source string, variables map[string]string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", nil
	}
	tmpl, err := template.New(name).Option("missingkey=error").Parse(source)
	if err != nil {
		return "", fmt.Errorf("%w: %s template parse: %w", entity.ErrInvalidEmailTemplate, name, err)
	}
	var out bytes.Buffer
	if err = tmpl.Execute(&out, variables); err != nil {
		return "", fmt.Errorf("%w: %s template render: %w", entity.ErrInvalidEmailTemplate, name, err)
	}

	return strings.TrimSpace(out.String()), nil
}

type emailView struct {
	Lang         string
	Preview      string
	Title        string
	Body         string
	ButtonLabel  string
	ButtonURL    string
	Note         string
	Footer       string
	SupportEmail string
}

func emailHTML(view emailView) string {
	lang := contentlang.MustNormalize(view.Lang)
	dir := "ltr"
	align := "left"
	if lang == contentlang.Arabic {
		dir = "rtl"
		align = "right"
	}
	actionHTML := ""
	if strings.TrimSpace(view.ButtonLabel) != "" && strings.TrimSpace(view.ButtonURL) != "" {
		buttonURL := html.EscapeString(view.ButtonURL)
		actionHTML = fmt.Sprintf(
			`<tr><td style="padding:24px 32px 0;text-align:%s;"><a href="%s" style="display:inline-block;border-radius:12px;background:#52794d;color:#fff;font-size:15px;font-weight:700;text-decoration:none;padding:13px 18px;">%s</a></td></tr><tr><td style="padding:16px 32px 0;text-align:%s;"><p style="margin:0;word-break:break-all;color:#52794d;font-size:13px;line-height:21px;"><a href="%s" style="color:#52794d;">%s</a></p></td></tr>`,
			align,
			buttonURL,
			html.EscapeString(view.ButtonLabel),
			align,
			buttonURL,
			buttonURL,
		)
	}
	noteHTML := ""
	if strings.TrimSpace(view.Note) != "" {
		noteHTML = fmt.Sprintf(
			`<tr><td style="padding:22px 32px 0;text-align:%s;"><p style="margin:0;padding:14px 16px;border-radius:12px;background:#f6f5ef;color:#5f5d55;font-size:14px;line-height:22px;">%s</p></td></tr>`,
			align,
			html.EscapeString(view.Note),
		)
	}
	footerHTML := ""
	if strings.TrimSpace(view.Footer) != "" {
		footerHTML = fmt.Sprintf(
			`<p style="margin:0 0 10px;color:#706d64;font-size:12px;line-height:20px;">%s</p>`,
			html.EscapeString(view.Footer),
		)
	}
	supportEmail := normalizeSupportEmail(view.SupportEmail)

	return fmt.Sprintf(`<!doctype html>
<html lang="%s" dir="%s">
  <head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title></head>
  <body style="margin:0;padding:0;background:#f6f5ef;color:#25241f;font-family:Arial,sans-serif;direction:%s;">
    <div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">%s</div>
    <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;background:#f6f5ef;">
      <tr><td align="center" style="padding:32px 16px;">
        <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;width:100%%;max-width:560px;">
          <tr><td style="padding:0 0 16px;color:#25241f;font-size:18px;font-weight:700;">Surau</td></tr>
          <tr><td style="background:#fffffb;border-radius:16px;overflow:hidden;">
            <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;">
              <tr><td style="padding:32px 32px 12px;text-align:%s;"><h1 style="margin:0;color:#25241f;font-size:28px;line-height:34px;letter-spacing:0;">%s</h1></td></tr>
              <tr><td style="padding:4px 32px 0;text-align:%s;"><p style="margin:0;color:#5f5d55;font-size:15px;line-height:24px;">%s</p></td></tr>
              %s%s
              <tr><td style="padding:26px 32px 32px;"></td></tr>
            </table>
          </td></tr>
          <tr><td style="padding:16px 4px 0;text-align:center;">%s<p style="margin:0;color:#706d64;font-size:12px;line-height:20px;">Support: <a href="mailto:%s" style="color:#52794d;">%s</a></p></td></tr>
        </table>
      </td></tr>
    </table>
  </body>
</html>`,
		lang,
		dir,
		html.EscapeString(view.Title),
		dir,
		html.EscapeString(view.Preview),
		align,
		html.EscapeString(view.Title),
		align,
		html.EscapeString(view.Body),
		actionHTML,
		noteHTML,
		footerHTML,
		html.EscapeString(supportEmail),
		html.EscapeString(supportEmail),
	)
}

func textWithFooter(text, lang, supportEmail string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, `\n`, "\n"))
	footer := "Support: " + normalizeSupportEmail(supportEmail)
	if lang == contentlang.Arabic {
		footer = "الدعم: " + normalizeSupportEmail(supportEmail)
	}
	if text == "" {
		return footer
	}

	return text + "\n\n" + footer
}

func (uc *UseCase) unsubscribeToken(userID, email string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(userID) + "\n" + strings.ToLower(strings.TrimSpace(email))))
	mac := hmac.New(sha256.New, []byte(uc.tokenSeed))
	_, _ = mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))

	return payload + "." + signature
}

func (uc *UseCase) parseUnsubscribeToken(token string) (string, string, error) {
	payload, signature, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || payload == "" || signature == "" {
		return "", "", entity.ErrInvalidUnsubscribeToken
	}
	mac := hmac.New(sha256.New, []byte(uc.tokenSeed))
	_, _ = mac.Write([]byte(payload))
	if !hmac.Equal([]byte(signature), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return "", "", entity.ErrInvalidUnsubscribeToken
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", "", entity.ErrInvalidUnsubscribeToken
	}
	userID, email, ok := strings.Cut(string(decoded), "\n")
	if !ok || strings.TrimSpace(userID) == "" || !validEmail(email) {
		return "", "", entity.ErrInvalidUnsubscribeToken
	}

	return userID, strings.ToLower(strings.TrimSpace(email)), nil
}

func (uc *UseCase) unsubscribeLink(token string) string {
	baseURL := strings.TrimSpace(uc.unsubscribeURL)
	if baseURL == "" {
		return token
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()

	return parsed.String()
}

func (uc *UseCase) ensureTransactionalCoverage(ctx context.Context, templateID, publishingLang string) error {
	for _, lang := range []string{contentlang.Default, contentlang.English, contentlang.Arabic} {
		if lang == publishingLang {
			continue
		}
		if _, err := uc.repo.GetLatestEmailTemplateVersion(ctx, templateID, lang); err != nil {
			return err
		}
	}

	return nil
}

func (uc *UseCase) ensureMarketingTemplate(ctx context.Context, templateID string) error {
	template, err := uc.repo.GetEmailTemplateByID(ctx, templateID)
	if err != nil {
		return err
	}
	if template.Category != entity.EmailCategoryMarketing ||
		!template.Enabled ||
		template.ArchivedAt != nil ||
		template.DeletedAt != nil {
		return entity.ErrInvalidEmailCampaign
	}

	return nil
}

func (uc *UseCase) publishedVersionForKey(
	ctx context.Context,
	templateKey,
	lang string,
) (entity.EmailTemplateVersion, entity.EmailTemplate, error) {
	version, template, err := uc.repo.GetPublishedEmailTemplateVersion(ctx, templateKey, lang)
	if err == nil || lang == contentlang.Default ||
		!errors.Is(err, entity.ErrEmailTemplateVersionNotFound) {
		return version, template, err
	}

	return uc.repo.GetPublishedEmailTemplateVersion(ctx, templateKey, contentlang.Default)
}

func (uc *UseCase) publishedVersionForTemplate(
	ctx context.Context,
	templateID,
	lang string,
) (entity.EmailTemplateVersion, error) {
	template, err := uc.repo.GetEmailTemplateByID(ctx, templateID)
	if err != nil {
		return entity.EmailTemplateVersion{}, err
	}
	version, _, err := uc.publishedVersionForKey(ctx, template.Key, lang)

	return version, err
}

func validateTemplateVersion(version entity.EmailTemplateVersion) error {
	if strings.TrimSpace(version.TemplateID) == "" {
		return entity.ErrInvalidEmailTemplate
	}
	if _, err := contentlang.Normalize(version.Lang); err != nil {
		return err
	}
	if strings.TrimSpace(version.SubjectTemplate) == "" ||
		strings.TrimSpace(version.TextTemplate) == "" {
		return entity.ErrInvalidEmailTemplate
	}

	return nil
}

func validateCampaign(campaign entity.EmailCampaign) error {
	if strings.TrimSpace(campaign.Name) == "" || strings.TrimSpace(campaign.TemplateID) == "" {
		return entity.ErrInvalidEmailCampaign
	}

	return nil
}

func validCategory(category string) bool {
	switch category {
	case entity.EmailCategoryTransactional, entity.EmailCategoryMarketing:
		return true
	default:
		return false
	}
}

func validSuppressionScope(scope string) bool {
	switch scope {
	case entity.EmailSuppressionScopeAll, entity.EmailSuppressionScopeMarketing:
		return true
	default:
		return false
	}
}

func normalizeKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")

	return value
}

func normalizeVariables(values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeKey(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}

	return normalized
}

func cloneMap(values map[string]string) map[string]string {
	cloned := map[string]string{}
	for key, value := range values {
		cloned[key] = value
	}

	return cloned
}

func nullableActor(actorID string) *string {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return nil
	}

	return &actorID
}

func normalizeSupportEmail(value string) string {
	value = strings.TrimSpace(value)
	if validEmail(value) {
		return value
	}

	return defaultSupportEmail
}

func validEmail(email string) bool {
	email = strings.TrimSpace(email)
	address, err := mail.ParseAddress(email)
	if err != nil {
		return false
	}

	return address.Name == "" && address.Address == email
}
