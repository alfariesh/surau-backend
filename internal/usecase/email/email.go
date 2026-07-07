package email

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"maps"
	"net/mail"
	"net/url"
	"strings"
	"text/template"
	"time"

	"github.com/alfariesh/surau-backend/internal/contentlang"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/google/uuid"
)

const (
	defaultSupportEmail = "support@surau.org"
	defaultBatchSize    = 500
	redactedValue       = "[redacted]"
	defaultTokenKeyID   = "default"
	tokenVersionV2      = "v2"

	emailSuppressionReasonPermanentBounce = "permanent_bounce"
	emailSuppressionReasonComplaint       = "complaint"
	emailDeliverySourceSync               = "sync"
	emailDeliverySourceWebhook            = "webhook"
	emailDeliverySourcePoll               = "poll"
	emailHeaderListUnsubscribe            = "List-Unsubscribe"
	emailHeaderListUnsubscribePost        = "List-Unsubscribe-Post"
	emailHeaderListUnsubscribeOneClick    = "One-Click"
	transactionalRetryVisibilityTimeout   = 5 * time.Minute

	campaignMetadataDeliveryTotal      = "delivery_total"
	campaignMetadataDeliverySent       = "delivery_sent"
	campaignMetadataDeliveryFailed     = "delivery_failed"
	campaignMetadataDeliverySkipped    = "delivery_skipped"
	campaignMetadataDeliveryFinishedAt = "delivery_finished_at"
	campaignMetadataRetryTotal         = "retry_failed_total"
	campaignMetadataRetrySent          = "retry_failed_sent"
	campaignMetadataRetryFailed        = "retry_failed_failed"
	campaignMetadataRetrySkipped       = "retry_failed_skipped"
	campaignMetadataRetryFinishedAt    = "retry_failed_finished_at"
)

var transactionalRetryDelays = []time.Duration{
	time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	time.Hour,
	6 * time.Hour,
}

// Options configures the admin-managed email service.
type Options struct {
	SupportEmail              string
	UnsubscribeURL            string
	UnsubscribeHeaderURL      string
	UnsubscribeTokenKeyID     string
	UnsubscribeTokenSeed      string
	UnsubscribeTokenSecrets   map[string]string
	CloudflareEventPoller     repo.EmailEventPoller
	CloudflarePollingZoneID   string
	CloudflarePollingLookback time.Duration
	CloudflarePollingLimit    int
}

// UseCase coordinates templates, delivery, consent, suppressions, and campaigns.
type UseCase struct {
	repo            repo.EmailRepo
	sender          repo.EmailSender
	supportEmail    string
	unsubscribeURL  string
	headerURL       string
	tokenKeyID      string
	tokenSeed       string
	tokenSecrets    map[string]string
	eventPoller     repo.EmailEventPoller
	pollingZoneID   string
	pollingLookback time.Duration
	pollingLimit    int
}

type campaignClaimer interface {
	ClaimEmailCampaignForSending(ctx context.Context, id, actorID string) (entity.EmailCampaign, error)
}

type campaignDeliveryStats struct {
	total   int
	sent    int
	failed  int
	skipped int
}

type deliveryEventInput struct {
	Provider          string
	EventType         string
	RecipientEmail    string
	MessageID         string
	CampaignID        string
	CampaignRecipient string
	Reason            string
	Diagnostic        string
	RawPayload        entity.RawJSON
	OccurredAt        time.Time
	DedupeSeed        string
}

type cloudflareWebhookPayload struct {
	PermanentBounces []string                 `json:"permanent_bounces"`
	Complaints       []string                 `json:"complaints"`
	Result           *cloudflareWebhookResult `json:"result"`
	Data             json.RawMessage          `json:"data"`
	Events           []cloudflareWebhookEvent `json:"events"`
	Timestamp        any                      `json:"ts"`
	CorrelationID    string                   `json:"alert_correlation_id"`
}

type cloudflareWebhookResult struct {
	PermanentBounces []string `json:"permanent_bounces"`
	Complaints       []string `json:"complaints"`
}

type cloudflareWebhookEvent struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	Event             string `json:"event"`
	EventType         string `json:"event_type"`
	Email             string `json:"email"`
	Recipient         string `json:"recipient"`
	RecipientEmail    string `json:"recipient_email"`
	MessageID         string `json:"message_id"`
	CampaignID        string `json:"campaign_id"`
	CampaignRecipient string `json:"campaign_recipient_id"`
	Reason            string `json:"reason"`
	Diagnostic        string `json:"diagnostic"`
	DiagnosticMessage string `json:"diagnostic_message"`
	OccurredAt        string `json:"occurred_at"`
	Timestamp         string `json:"timestamp"`
	Time              string `json:"time"`
}

// New creates an email use case.
func New(r repo.EmailRepo, sender repo.EmailSender, opts Options) *UseCase {
	tokenKeyID, tokenSeed, tokenSecrets := normalizeUnsubscribeTokenOptions(opts)
	pollingLookback := opts.CloudflarePollingLookback
	if pollingLookback <= 0 {
		pollingLookback = 30 * time.Minute
	}
	pollingLimit := opts.CloudflarePollingLimit
	if pollingLimit <= 0 {
		pollingLimit = 100
	}
	if pollingLimit > 1000 {
		pollingLimit = 1000
	}

	return &UseCase{
		repo:            r,
		sender:          sender,
		supportEmail:    normalizeSupportEmail(opts.SupportEmail),
		unsubscribeURL:  strings.TrimSpace(opts.UnsubscribeURL),
		headerURL:       strings.TrimSpace(opts.UnsubscribeHeaderURL),
		tokenKeyID:      tokenKeyID,
		tokenSeed:       tokenSeed,
		tokenSecrets:    tokenSecrets,
		eventPoller:     opts.CloudflareEventPoller,
		pollingZoneID:   strings.TrimSpace(opts.CloudflarePollingZoneID),
		pollingLookback: pollingLookback,
		pollingLimit:    pollingLimit,
	}
}

// SendTransactional sends one admin-managed transactional email with a fallback message.
func (uc *UseCase) SendTransactional(ctx context.Context, req entity.TransactionalEmailRequest) error {
	if uc.sender == nil {
		return entity.ErrEmailDeliveryFailed
	}
	if uc.repo == nil {
		_, err := uc.sender.Send(ctx, req.Fallback)

		return err
	}

	lang := contentlang.MustNormalize(req.Lang)
	variables := cloneMap(req.Variables)
	message := req.Fallback
	message.Critical = message.Critical || req.Critical
	message.Category = entity.EmailCategoryTransactional
	message.TemplateKey = req.Key
	message.Lang = lang
	message.UserID = req.UserID
	message.Metadata = variables
	if message.To == "" {
		message.To = req.To
	}

	setting, err := uc.repo.GetEmailEventSetting(ctx, req.Key)
	if err != nil && !errors.Is(err, entity.ErrEmailEventSettingNotFound) {
		return fmt.Errorf("EmailUseCase - SendTransactional - GetEmailEventSetting: %w", err)
	}
	if err == nil && setting.Critical {
		message.Critical = true
	}
	if err == nil && !setting.Enabled && !setting.Critical && !message.Critical {
		templateCritical, templateErr := uc.templateIsCritical(ctx, setting.TemplateID)
		if templateErr != nil {
			return fmt.Errorf("EmailUseCase - SendTransactional - GetEmailTemplateByID: %w", templateErr)
		}
		if !templateCritical {
			return uc.logSkipped(ctx, message)
		}
		message.Critical = true
	}

	version, template, err := uc.publishedVersionForKey(ctx, req.Key, lang)
	if err == nil {
		if template.Critical {
			message.Critical = true
		}
		preview, renderErr := uc.render(version, variables)
		if renderErr != nil {
			if !message.Critical {
				return renderErr
			}
			variables["template_render_error"] = renderErr.Error()
		} else {
			message.Subject = preview.Subject
			message.HTML = preview.HTML
			message.Text = preview.Text
			message.TemplateVersionID = version.ID
		}
	} else if !errors.Is(err, entity.ErrEmailTemplateVersionNotFound) {
		return fmt.Errorf("EmailUseCase - SendTransactional - GetPublishedEmailTemplateVersion: %w", err)
	}

	if req.Async {
		return uc.enqueueTransactional(ctx, message)
	}

	_, err = uc.sendAndLog(ctx, message, "", "")

	return err
}

// enqueueTransactional durably queues a transactional message for the
// background dispatcher instead of calling the provider in-request.
// Suppression and permanent-bounce handling run at dispatch time
// (retryTransactionalMessage), matching the synchronous path's outcome.
//
//nolint:gocritic // message passed by value to match sendAndLog's signature
func (uc *UseCase) enqueueTransactional(ctx context.Context, message entity.EmailMessage) error {
	messageLog, err := uc.createMessageLog(
		ctx,
		message,
		entity.EmailCategoryTransactional,
		"",
		"",
		entity.EmailMessageStatusQueued,
		0,
		"",
		"",
		nil,
	)
	if err != nil {
		return fmt.Errorf("EmailUseCase - enqueueTransactional - createMessageLog: %w", err)
	}

	// scheduled_at must be set for the dispatcher to claim the row; attempts
	// stays 0 so a first dispatch failure starts the regular retry ladder.
	if _, err = uc.repo.ScheduleEmailMessageRetry(ctx, messageLog.ID, 0, "", "", time.Now().UTC()); err != nil {
		return fmt.Errorf("EmailUseCase - enqueueTransactional - ScheduleEmailMessageRetry: %w", err)
	}

	return nil
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

func (uc *UseCase) DeliveryEvents(
	ctx context.Context,
	filter repo.EmailDeliveryEventFilter,
) ([]entity.EmailDeliveryEvent, int, error) {
	filter = normalizeDeliveryEventFilter(filter)

	return uc.repo.ListEmailDeliveryEvents(ctx, filter)
}

func (uc *UseCase) CampaignDeliveryEventSummary(
	ctx context.Context,
	campaignID string,
) (entity.EmailCampaignDeliveryEventSummary, error) {
	campaignID = strings.TrimSpace(campaignID)
	if campaignID == "" {
		return entity.EmailCampaignDeliveryEventSummary{}, entity.ErrInvalidEmailCampaign
	}

	return uc.repo.GetEmailCampaignDeliveryEventSummary(ctx, campaignID)
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
	previousStatus := campaign.Status
	campaign, err = uc.claimCampaignForSending(ctx, campaign, actorID)
	if err != nil {
		return entity.EmailCampaign{}, err
	}
	if err = uc.prepareCampaignRecipients(ctx, campaign); err != nil {
		_ = uc.restoreCampaignStatus(ctx, campaign, previousStatus, actorID)

		return entity.EmailCampaign{}, err
	}
	stats, err := uc.deliverCampaign(ctx, campaign)
	if err != nil {
		return entity.EmailCampaign{}, err
	}

	now := time.Now().UTC()
	campaign.Status = entity.EmailCampaignStatusSent
	campaign.SentAt = &now
	campaign.UpdatedBy = nullableActor(actorID)
	campaign.Metadata = campaignMetadataWithDeliveryStats(campaign.Metadata, stats, now)

	return uc.repo.UpdateEmailCampaign(ctx, campaign)
}

func (uc *UseCase) RetryFailedCampaign(ctx context.Context, id, actorID string) (entity.EmailCampaign, error) {
	campaign, err := uc.repo.GetEmailCampaign(ctx, id)
	if err != nil {
		return entity.EmailCampaign{}, err
	}
	if campaign.Status != entity.EmailCampaignStatusSent {
		return entity.EmailCampaign{}, entity.ErrInvalidEmailCampaign
	}
	if err = uc.ensureMarketingTemplate(ctx, campaign.TemplateID); err != nil {
		return entity.EmailCampaign{}, err
	}
	campaign, err = uc.repo.ClaimEmailCampaignForRetry(ctx, campaign.ID, actorID)
	if err != nil {
		return entity.EmailCampaign{}, err
	}

	retryStartedAt := time.Now().UTC()
	stats, err := uc.deliverCampaignRetry(ctx, campaign, retryStartedAt)
	if err != nil {
		_ = uc.restoreCampaignStatus(ctx, campaign, entity.EmailCampaignStatusSent, actorID)

		return entity.EmailCampaign{}, err
	}
	counts, err := uc.repo.CountEmailCampaignRecipientsByStatus(ctx, campaign.ID)
	if err != nil {
		_ = uc.restoreCampaignStatus(ctx, campaign, entity.EmailCampaignStatusSent, actorID)

		return entity.EmailCampaign{}, err
	}

	now := time.Now().UTC()
	campaign.Status = entity.EmailCampaignStatusSent
	campaign.UpdatedBy = nullableActor(actorID)
	campaign.Metadata = campaignMetadataWithRetryStats(campaign.Metadata, counts, stats, now)

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

func (uc *UseCase) DispatchDueTransactionalEmails(ctx context.Context, limit int) error {
	if uc.sender == nil || uc.repo == nil {
		return nil
	}
	messages, err := uc.repo.ClaimDueTransactionalEmailMessages(
		ctx,
		time.Now().UTC(),
		limit,
		transactionalRetryVisibilityTimeout,
	)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if err = uc.retryTransactionalMessage(ctx, message); err != nil {
			return err
		}
	}

	return nil
}

func (uc *UseCase) PollCloudflareEmailEvents(ctx context.Context) (entity.EmailWebhookIngestResult, error) {
	var result entity.EmailWebhookIngestResult
	if uc.repo == nil || uc.eventPoller == nil || strings.TrimSpace(uc.pollingZoneID) == "" {
		return result, nil
	}

	now := time.Now().UTC()
	start := now.Add(-uc.pollingLookback)
	cursorKey := cloudflareEmailSendingCursorKey(uc.pollingZoneID)
	cursor, err := uc.repo.GetEmailProviderPollCursor(ctx, entity.EmailProviderCloudflare, cursorKey)
	if err != nil && !errors.Is(err, entity.ErrEmailProviderPollCursorNotFound) {
		return result, err
	}
	if err == nil && !cursor.LastPolledAt.IsZero() {
		start = cursor.LastPolledAt.Add(-uc.pollingLookback)
	}
	if !start.Before(now) {
		start = now.Add(-uc.pollingLookback)
	}

	events, err := uc.eventPoller.PollCloudflareEmailEvents(ctx, entity.CloudflareEmailEventPollQuery{
		ZoneID: uc.pollingZoneID,
		Start:  start,
		End:    now,
		Limit:  uc.pollingLimit,
	})
	if err != nil {
		return result, err
	}
	result.Accepted = len(events)
	for _, event := range events {
		input, ok := cloudflarePolledDeliveryEvent(event)
		if !ok {
			continue
		}
		inserted, err := uc.recordDeliveryEvent(ctx, input)
		if err != nil {
			return result, err
		}
		if inserted {
			result.Processed++
			if err = uc.markDeliveryTargetsFailed(ctx, input); err != nil {
				return result, err
			}
		} else {
			result.Duplicates++
		}
		if _, err = uc.upsertAutomatedSuppression(
			ctx,
			input.RecipientEmail,
			emailSuppressionReasonPermanentBounce,
		); err != nil {
			return result, err
		}
		result.Suppressed++
	}

	_, err = uc.repo.UpsertEmailProviderPollCursor(ctx, entity.EmailProviderPollCursor{
		Provider:     entity.EmailProviderCloudflare,
		CursorKey:    cursorKey,
		LastPolledAt: now,
	})
	if err != nil {
		return result, err
	}

	return result, nil
}

func (uc *UseCase) IngestCloudflareBounceWebhook(
	ctx context.Context,
	payload []byte,
) (entity.EmailWebhookIngestResult, error) {
	var result entity.EmailWebhookIngestResult
	events, err := parseCloudflareDeliveryEvents(payload, time.Now().UTC())
	if err != nil {
		return result, err
	}
	result.Accepted = len(events)

	for _, event := range events {
		inserted, err := uc.recordDeliveryEvent(ctx, event)
		if err != nil {
			return result, err
		}
		if inserted {
			result.Processed++
			if err = uc.markDeliveryTargetsFailed(ctx, event); err != nil {
				return result, err
			}
		} else {
			result.Duplicates++
		}
		if _, err = uc.upsertAutomatedSuppression(ctx, event.RecipientEmail, suppressionReasonForEvent(event.EventType)); err != nil {
			return result, err
		}
		result.Suppressed++
	}

	return result, nil
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

func (uc *UseCase) claimCampaignForSending(
	ctx context.Context,
	campaign entity.EmailCampaign,
	actorID string,
) (entity.EmailCampaign, error) {
	if claimer, ok := uc.repo.(campaignClaimer); ok {
		return claimer.ClaimEmailCampaignForSending(ctx, campaign.ID, actorID)
	}

	campaign.Status = entity.EmailCampaignStatusSending
	campaign.UpdatedBy = nullableActor(actorID)

	return uc.repo.UpdateEmailCampaign(ctx, campaign)
}

func (uc *UseCase) restoreCampaignStatus(
	ctx context.Context,
	campaign entity.EmailCampaign,
	status string,
	actorID string,
) error {
	campaign.Status = status
	campaign.UpdatedBy = nullableActor(actorID)

	_, err := uc.repo.UpdateEmailCampaign(ctx, campaign)

	return err
}

func (uc *UseCase) deliverCampaign(ctx context.Context, campaign entity.EmailCampaign) (campaignDeliveryStats, error) {
	var stats campaignDeliveryStats
	for {
		recipients, err := uc.repo.ListEmailCampaignRecipients(
			ctx,
			campaign.ID,
			entity.EmailRecipientStatusPending,
			defaultBatchSize,
		)
		if err != nil {
			return stats, err
		}
		if len(recipients) == 0 {
			return stats, nil
		}

		for _, recipient := range recipients {
			status, err := uc.deliverCampaignRecipient(ctx, campaign, recipient)
			if err != nil {
				return stats, err
			}
			stats.total++
			switch status {
			case entity.EmailRecipientStatusSent:
				stats.sent++
			case entity.EmailRecipientStatusSkipped:
				stats.skipped++
			case entity.EmailRecipientStatusFailed:
				stats.failed++
			}
		}
	}
}

func (uc *UseCase) deliverCampaignRetry(
	ctx context.Context,
	campaign entity.EmailCampaign,
	cutoff time.Time,
) (campaignDeliveryStats, error) {
	var stats campaignDeliveryStats
	for {
		recipients, err := uc.repo.ListEmailCampaignRecipientsForRetry(
			ctx,
			campaign.ID,
			cutoff,
			defaultBatchSize,
		)
		if err != nil {
			return stats, err
		}
		if len(recipients) == 0 {
			return stats, nil
		}

		for _, recipient := range recipients {
			status, err := uc.deliverCampaignRecipient(ctx, campaign, recipient)
			if err != nil {
				return stats, err
			}
			stats.total++
			switch status {
			case entity.EmailRecipientStatusSent:
				stats.sent++
			case entity.EmailRecipientStatusSkipped:
				stats.skipped++
			case entity.EmailRecipientStatusFailed:
				stats.failed++
			}
		}
	}
}

func (uc *UseCase) deliverCampaignRecipient(
	ctx context.Context,
	campaign entity.EmailCampaign,
	recipient entity.EmailCampaignRecipient,
) (string, error) {
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

		return entity.EmailRecipientStatusFailed, nil
	}

	variables := cloneMap(campaign.Metadata)
	variables["email"] = recipient.Email
	variables["lang"] = recipient.Lang
	variables["unsubscribe_url"] = recipient.UnsubscribeURL
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

		return entity.EmailRecipientStatusFailed, nil
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

		return entity.EmailRecipientStatusFailed, nil
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

		return entity.EmailRecipientStatusSkipped, err
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

	return entity.EmailRecipientStatusSent, err
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
	if !message.Critical {
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
		updated, _ := uc.repo.UpdateEmailMessageStatus(
			ctx,
			messageLog.ID,
			entity.EmailMessageStatusFailed,
			1,
			"",
			entity.ErrEmailDeliveryFailed.Error(),
			nil,
		)
		if updated.ID != "" {
			messageLog = updated
		}

		return messageLog, entity.ErrEmailDeliveryFailed
	}

	message.MessageID = messageLog.ID
	message.CampaignID = campaignID
	message.CampaignRecipient = campaignRecipientID
	message = uc.withDeliverabilityHeaders(message, category)
	sendResult, err := uc.sender.Send(ctx, message)
	providerResponse := sendResult.ProviderResponse
	if err != nil {
		if errors.Is(err, entity.ErrEmailPermanentBounce) {
			updated, _ := uc.repo.UpdateEmailMessageStatus(
				ctx,
				messageLog.ID,
				entity.EmailMessageStatusFailed,
				1,
				providerResponse,
				err.Error(),
				nil,
			)
			if updated.ID != "" {
				messageLog = updated
			}
			if recordErr := uc.handleSyncPermanentBounces(ctx, message, sendResult, err); recordErr != nil {
				return messageLog, recordErr
			}

			return messageLog, fmt.Errorf("%w: %w", entity.ErrEmailDeliveryFailed, err)
		}
		if category == entity.EmailCategoryTransactional {
			delay, _ := transactionalRetryDelayAfterAttempts(1)
			nextAttemptAt := time.Now().UTC().Add(delay)
			updated, retryErr := uc.repo.ScheduleEmailMessageRetry(
				ctx,
				messageLog.ID,
				1,
				providerResponse,
				err.Error(),
				nextAttemptAt,
			)
			if retryErr != nil {
				return messageLog, retryErr
			}

			return updated, nil
		}

		updated, _ := uc.repo.UpdateEmailMessageStatus(
			ctx,
			messageLog.ID,
			entity.EmailMessageStatusFailed,
			1,
			providerResponse,
			err.Error(),
			nil,
		)
		if updated.ID != "" {
			messageLog = updated
		}

		return messageLog, fmt.Errorf("%w: %w", entity.ErrEmailDeliveryFailed, err)
	}
	if providerResponse == "" {
		providerResponse = "sent"
	}

	now := time.Now().UTC()
	messageLog, err = uc.repo.UpdateEmailMessageStatus(
		ctx,
		messageLog.ID,
		entity.EmailMessageStatusSent,
		1,
		providerResponse,
		"",
		&now,
	)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}

	return messageLog, nil
}

func (uc *UseCase) retryTransactionalMessage(
	ctx context.Context,
	messageLog entity.EmailMessageLog,
) error {
	if messageLog.Category != entity.EmailCategoryTransactional {
		return nil
	}
	if !messageLog.Critical {
		suppressed, err := uc.repo.IsEmailSuppressed(ctx, messageLog.RecipientEmail, messageLog.Category)
		if err != nil {
			return err
		}
		if suppressed {
			_, err = uc.repo.UpdateEmailMessageStatus(
				ctx,
				messageLog.ID,
				entity.EmailMessageStatusSkipped,
				messageLog.Attempts,
				messageLog.ProviderResponse,
				"suppressed",
				nil,
			)

			return err
		}
	}

	message := emailMessageFromLog(messageLog)
	sendResult, err := uc.sender.Send(ctx, message)
	providerResponse := sendResult.ProviderResponse
	attempts := messageLog.Attempts + 1
	if attempts <= 0 {
		attempts = 1
	}
	if err != nil {
		if errors.Is(err, entity.ErrEmailPermanentBounce) {
			_, updateErr := uc.repo.UpdateEmailMessageStatus(
				ctx,
				messageLog.ID,
				entity.EmailMessageStatusFailed,
				attempts,
				providerResponse,
				err.Error(),
				nil,
			)
			if updateErr != nil {
				return updateErr
			}

			return uc.handleSyncPermanentBounces(ctx, message, sendResult, err)
		}
		if delay, ok := transactionalRetryDelayAfterAttempts(attempts); ok {
			_, scheduleErr := uc.repo.ScheduleEmailMessageRetry(
				ctx,
				messageLog.ID,
				attempts,
				providerResponse,
				err.Error(),
				time.Now().UTC().Add(delay),
			)

			return scheduleErr
		}

		_, updateErr := uc.repo.UpdateEmailMessageStatus(
			ctx,
			messageLog.ID,
			entity.EmailMessageStatusFailed,
			attempts,
			providerResponse,
			err.Error(),
			nil,
		)

		return updateErr
	}
	if providerResponse == "" {
		providerResponse = "sent"
	}

	now := time.Now().UTC()
	_, err = uc.repo.UpdateEmailMessageStatus(
		ctx,
		messageLog.ID,
		entity.EmailMessageStatusSent,
		attempts,
		providerResponse,
		"",
		&now,
	)

	return err
}

func emailMessageFromLog(messageLog entity.EmailMessageLog) entity.EmailMessage {
	return entity.EmailMessage{
		To:                messageLog.RecipientEmail,
		Subject:           messageLog.Subject,
		HTML:              messageLog.HTML,
		Text:              messageLog.Text,
		Critical:          messageLog.Critical,
		Category:          messageLog.Category,
		TemplateKey:       messageLog.TemplateKey,
		TemplateVersionID: messageLog.TemplateVersionID,
		Lang:              messageLog.Lang,
		UserID:            messageLog.UserID,
		MessageID:         messageLog.ID,
		CampaignID:        messageLog.CampaignID,
		CampaignRecipient: messageLog.CampaignRecipient,
		Headers:           cloneMap(messageLog.Headers),
		Metadata:          cloneMap(messageLog.Metadata),
	}
}

func transactionalRetryDelayAfterAttempts(attempts int) (time.Duration, bool) {
	if attempts <= 0 || attempts > len(transactionalRetryDelays) {
		return 0, false
	}

	return transactionalRetryDelays[attempts-1], true
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
		HTML:              message.HTML,
		Text:              message.Text,
		Critical:          message.Critical,
		Headers:           cloneMap(message.Headers),
		Status:            status,
		Attempts:          attempts,
		ProviderResponse:  providerResponse,
		Error:             deliveryError,
		Metadata:          redactEmailMetadata(message.Metadata),
		SentAt:            sentAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	return uc.repo.CreateEmailMessage(ctx, log)
}

func (uc *UseCase) handleSyncPermanentBounces(
	ctx context.Context,
	message entity.EmailMessage,
	result entity.EmailSendResult,
	deliveryErr error,
) error {
	bounces := result.PermanentBounces
	if len(bounces) == 0 {
		bounces = []string{message.To}
	}
	rawPayload := rawProviderPayload(result.ProviderResponse)
	for _, email := range bounces {
		if !validEmail(email) {
			continue
		}
		event := deliveryEventInput{
			Provider:          providerOrDefault(result.Provider),
			EventType:         entity.EmailDeliveryEventBounceHard,
			RecipientEmail:    email,
			MessageID:         message.MessageID,
			CampaignID:        message.CampaignID,
			CampaignRecipient: message.CampaignRecipient,
			Reason:            emailSuppressionReasonPermanentBounce,
			Diagnostic:        deliveryErr.Error(),
			RawPayload:        rawPayload,
			OccurredAt:        time.Now().UTC(),
			DedupeSeed:        emailDeliverySourceSync,
		}
		if _, err := uc.recordDeliveryEvent(ctx, event); err != nil {
			return err
		}
		if _, err := uc.upsertAutomatedSuppression(
			ctx,
			email,
			emailSuppressionReasonPermanentBounce,
		); err != nil {
			return err
		}
	}

	return nil
}

func (uc *UseCase) recordDeliveryEvent(ctx context.Context, input deliveryEventInput) (bool, error) {
	if !validEmail(input.RecipientEmail) || !validDeliveryEventType(input.EventType) {
		return false, entity.ErrInvalidAuthInput
	}
	now := time.Now().UTC()
	if input.OccurredAt.IsZero() {
		input.OccurredAt = now
	}
	rawPayload := input.RawPayload
	if len(rawPayload) == 0 || !json.Valid(rawPayload) {
		rawPayload = entity.RawJSON(`{}`)
	}
	event := entity.EmailDeliveryEvent{
		ID:                uuid.New().String(),
		DedupeKey:         deliveryEventDedupeKey(input),
		Provider:          providerOrDefault(input.Provider),
		EventType:         input.EventType,
		RecipientEmail:    strings.ToLower(strings.TrimSpace(input.RecipientEmail)),
		MessageID:         strings.TrimSpace(input.MessageID),
		CampaignID:        strings.TrimSpace(input.CampaignID),
		CampaignRecipient: strings.TrimSpace(input.CampaignRecipient),
		Reason:            strings.TrimSpace(input.Reason),
		Diagnostic:        strings.TrimSpace(input.Diagnostic),
		RawPayload:        rawPayload,
		OccurredAt:        input.OccurredAt,
		CreatedAt:         now,
	}
	_, inserted, err := uc.repo.UpsertEmailDeliveryEvent(ctx, event)

	return inserted, err
}

func cloudflarePolledDeliveryEvent(event entity.CloudflareEmailEvent) (deliveryEventInput, bool) {
	if !strings.EqualFold(strings.TrimSpace(event.Status), "deliveryFailed") || !validEmail(event.To) {
		return deliveryEventInput{}, false
	}
	rawPayload := event.RawPayload
	if len(rawPayload) == 0 || !json.Valid(rawPayload) {
		raw, _ := json.Marshal(event)
		rawPayload = entity.RawJSON(raw)
	}
	diagnostic := firstNonEmpty(event.ErrorDetail, event.ErrorCause, event.EventType, event.Status)

	return deliveryEventInput{
		Provider:       entity.EmailProviderCloudflare,
		EventType:      entity.EmailDeliveryEventBounceHard,
		RecipientEmail: event.To,
		MessageID:      localMessageIDFromProviderMessageID(event.MessageID),
		Reason:         firstNonEmpty(event.ErrorCause, emailSuppressionReasonPermanentBounce),
		Diagnostic:     diagnostic,
		RawPayload:     rawPayload,
		OccurredAt:     event.Datetime,
		DedupeSeed: strings.Join([]string{
			emailDeliverySourcePoll,
			strings.TrimSpace(event.MessageID),
			strings.TrimSpace(event.To),
			strings.TrimSpace(event.Status),
			event.Datetime.UTC().Format(time.RFC3339Nano),
			strings.TrimSpace(event.ErrorCause),
			strings.TrimSpace(event.ErrorDetail),
		}, ":"),
	}, true
}

func localMessageIDFromProviderMessageID(messageID string) string {
	messageID = strings.Trim(strings.TrimSpace(messageID), "<>")
	if _, err := uuid.Parse(messageID); err != nil {
		return ""
	}

	return messageID
}

func cloudflareEmailSendingCursorKey(zoneID string) string {
	return entity.EmailProviderPollCursorCloudflareSending + ":" + strings.TrimSpace(zoneID)
}

func (uc *UseCase) upsertAutomatedSuppression(
	ctx context.Context,
	email,
	reason string,
) (entity.EmailSuppression, error) {
	if !validEmail(email) {
		return entity.EmailSuppression{}, entity.ErrInvalidAuthInput
	}
	now := time.Now().UTC()

	return uc.repo.UpsertAutomaticEmailSuppression(ctx, entity.EmailSuppression{
		ID:        uuid.New().String(),
		Email:     strings.ToLower(strings.TrimSpace(email)),
		Scope:     entity.EmailSuppressionScopeAll,
		Reason:    strings.TrimSpace(reason),
		CreatedAt: now,
	})
}

func (uc *UseCase) markDeliveryTargetsFailed(ctx context.Context, event deliveryEventInput) error {
	messageID := localMessageIDFromProviderMessageID(event.MessageID)
	if messageID != "" {
		_, err := uc.repo.UpdateEmailMessageStatus(
			ctx,
			messageID,
			entity.EmailMessageStatusFailed,
			1,
			string(event.RawPayload),
			event.Diagnostic,
			nil,
		)
		if err != nil && !errors.Is(err, entity.ErrEmailMessageNotFound) {
			return err
		}
	}
	campaignRecipientID := localMessageIDFromProviderMessageID(event.CampaignRecipient)
	if campaignRecipientID != "" {
		_, err := uc.repo.UpdateEmailCampaignRecipientStatus(
			ctx,
			campaignRecipientID,
			entity.EmailRecipientStatusFailed,
			messageID,
			event.Diagnostic,
			nil,
		)
		if err != nil && !errors.Is(err, entity.ErrEmailCampaignNotFound) {
			return err
		}
	}

	return nil
}

func campaignMetadataWithDeliveryStats(
	metadata map[string]string,
	stats campaignDeliveryStats,
	finishedAt time.Time,
) map[string]string {
	metadata = cloneMap(metadata)
	metadata[campaignMetadataDeliveryTotal] = fmt.Sprintf("%d", stats.total)
	metadata[campaignMetadataDeliverySent] = fmt.Sprintf("%d", stats.sent)
	metadata[campaignMetadataDeliveryFailed] = fmt.Sprintf("%d", stats.failed)
	metadata[campaignMetadataDeliverySkipped] = fmt.Sprintf("%d", stats.skipped)
	metadata[campaignMetadataDeliveryFinishedAt] = finishedAt.Format(time.RFC3339)

	return metadata
}

func campaignMetadataWithRetryStats(
	metadata map[string]string,
	counts map[string]int,
	stats campaignDeliveryStats,
	finishedAt time.Time,
) map[string]string {
	metadata = cloneMap(metadata)
	deliveryTotal := 0
	for _, status := range []string{
		entity.EmailRecipientStatusPending,
		entity.EmailRecipientStatusSent,
		entity.EmailRecipientStatusFailed,
		entity.EmailRecipientStatusSkipped,
	} {
		deliveryTotal += counts[status]
	}
	metadata[campaignMetadataDeliveryTotal] = fmt.Sprintf("%d", deliveryTotal)
	metadata[campaignMetadataDeliverySent] = fmt.Sprintf("%d", counts[entity.EmailRecipientStatusSent])
	metadata[campaignMetadataDeliveryFailed] = fmt.Sprintf("%d", counts[entity.EmailRecipientStatusFailed])
	metadata[campaignMetadataDeliverySkipped] = fmt.Sprintf("%d", counts[entity.EmailRecipientStatusSkipped])
	metadata[campaignMetadataDeliveryFinishedAt] = finishedAt.Format(time.RFC3339)
	metadata[campaignMetadataRetryTotal] = fmt.Sprintf("%d", stats.total)
	metadata[campaignMetadataRetrySent] = fmt.Sprintf("%d", stats.sent)
	metadata[campaignMetadataRetryFailed] = fmt.Sprintf("%d", stats.failed)
	metadata[campaignMetadataRetrySkipped] = fmt.Sprintf("%d", stats.skipped)
	metadata[campaignMetadataRetryFinishedAt] = finishedAt.Format(time.RFC3339)

	return metadata
}

func redactEmailMetadata(metadata map[string]string) map[string]string {
	redacted := map[string]string{}
	for key, value := range metadata {
		if sensitiveEmailMetadataKey(key) || valueHasTokenQuery(value) {
			redacted[key] = redactedValue

			continue
		}
		redacted[key] = value
	}

	return redacted
}

func parseCloudflareDeliveryEvents(payload []byte, now time.Time) ([]deliveryEventInput, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || !json.Valid(payload) {
		return nil, entity.ErrInvalidAuthInput
	}
	var parsed cloudflareWebhookPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, entity.ErrInvalidAuthInput
	}
	rawPayload := entity.RawJSON(payload)
	seed := webhookDedupeSeed(payload, parsed.CorrelationID)
	occurredAt := webhookOccurredAt(parsed.Timestamp, now)
	events := make([]deliveryEventInput, 0)
	addAddresses := func(addresses []string, eventType, reason string) {
		for _, email := range addresses {
			if !validEmail(email) {
				continue
			}
			events = append(events, deliveryEventInput{
				Provider:       entity.EmailProviderCloudflare,
				EventType:      eventType,
				RecipientEmail: email,
				Reason:         reason,
				Diagnostic:     reason,
				RawPayload:     rawPayload,
				OccurredAt:     occurredAt,
				DedupeSeed:     seed,
			})
		}
	}
	addAddresses(parsed.PermanentBounces, entity.EmailDeliveryEventBounceHard, emailSuppressionReasonPermanentBounce)
	addAddresses(parsed.Complaints, entity.EmailDeliveryEventComplaint, emailSuppressionReasonComplaint)
	if parsed.Result != nil {
		addAddresses(
			parsed.Result.PermanentBounces,
			entity.EmailDeliveryEventBounceHard,
			emailSuppressionReasonPermanentBounce,
		)
		addAddresses(parsed.Result.Complaints, entity.EmailDeliveryEventComplaint, emailSuppressionReasonComplaint)
	}
	if len(parsed.Data) > 0 && json.Valid(parsed.Data) {
		dataEvents := parseCloudflareDataEvents(parsed.Data, now, seed)
		events = append(events, dataEvents...)
	}
	for _, event := range parsed.Events {
		if input, ok := normalizedCloudflareEvent(event, rawPayload, now, seed); ok {
			events = append(events, input)
		}
	}

	return events, nil
}

func parseCloudflareDataEvents(data json.RawMessage, now time.Time, seed string) []deliveryEventInput {
	var parsed struct {
		PermanentBounces []string                 `json:"permanent_bounces"`
		Complaints       []string                 `json:"complaints"`
		Events           []cloudflareWebhookEvent `json:"events"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	rawPayload := entity.RawJSON(data)
	events := make([]deliveryEventInput, 0)
	for _, email := range parsed.PermanentBounces {
		if !validEmail(email) {
			continue
		}
		events = append(events, deliveryEventInput{
			Provider:       entity.EmailProviderCloudflare,
			EventType:      entity.EmailDeliveryEventBounceHard,
			RecipientEmail: email,
			Reason:         emailSuppressionReasonPermanentBounce,
			Diagnostic:     emailSuppressionReasonPermanentBounce,
			RawPayload:     rawPayload,
			OccurredAt:     now,
			DedupeSeed:     seed + ":data",
		})
	}
	for _, email := range parsed.Complaints {
		if !validEmail(email) {
			continue
		}
		events = append(events, deliveryEventInput{
			Provider:       entity.EmailProviderCloudflare,
			EventType:      entity.EmailDeliveryEventComplaint,
			RecipientEmail: email,
			Reason:         emailSuppressionReasonComplaint,
			Diagnostic:     emailSuppressionReasonComplaint,
			RawPayload:     rawPayload,
			OccurredAt:     now,
			DedupeSeed:     seed + ":data",
		})
	}
	for _, event := range parsed.Events {
		if input, ok := normalizedCloudflareEvent(event, rawPayload, now, seed+":data"); ok {
			events = append(events, input)
		}
	}

	return events
}

func normalizedCloudflareEvent(
	event cloudflareWebhookEvent,
	rawPayload entity.RawJSON,
	now time.Time,
	fallbackSeed string,
) (deliveryEventInput, bool) {
	eventType, reason := normalizeDeliveryEventType(firstNonEmpty(event.EventType, event.Type, event.Event))
	if eventType == "" {
		return deliveryEventInput{}, false
	}
	email := firstNonEmpty(event.RecipientEmail, event.Email, event.Recipient)
	if !validEmail(email) {
		return deliveryEventInput{}, false
	}
	occurredAt := parseEventTime(firstNonEmpty(event.OccurredAt, event.Timestamp, event.Time), now)
	diagnostic := firstNonEmpty(event.Diagnostic, event.DiagnosticMessage, event.Reason, reason)
	dedupeSeed := firstNonEmpty(event.ID, fallbackSeed)

	return deliveryEventInput{
		Provider:          entity.EmailProviderCloudflare,
		EventType:         eventType,
		RecipientEmail:    email,
		MessageID:         event.MessageID,
		CampaignID:        event.CampaignID,
		CampaignRecipient: event.CampaignRecipient,
		Reason:            firstNonEmpty(event.Reason, reason),
		Diagnostic:        diagnostic,
		RawPayload:        rawPayload,
		OccurredAt:        occurredAt,
		DedupeSeed:        dedupeSeed,
	}, true
}

func normalizeDeliveryEventType(value string) (string, string) {
	switch normalizeKey(value) {
	case "bounce_hard", "hard_bounce", "permanent_bounce", "permanent_bounces", "bounce":
		return entity.EmailDeliveryEventBounceHard, emailSuppressionReasonPermanentBounce
	case "complaint", "spam_complaint", "abuse_complaint":
		return entity.EmailDeliveryEventComplaint, emailSuppressionReasonComplaint
	default:
		return "", ""
	}
}

func validDeliveryEventType(value string) bool {
	switch value {
	case entity.EmailDeliveryEventBounceHard, entity.EmailDeliveryEventComplaint:
		return true
	default:
		return false
	}
}

func suppressionReasonForEvent(eventType string) string {
	if eventType == entity.EmailDeliveryEventComplaint {
		return emailSuppressionReasonComplaint
	}

	return emailSuppressionReasonPermanentBounce
}

func deliveryEventDedupeKey(input deliveryEventInput) string {
	seed := strings.Join([]string{
		providerOrDefault(input.Provider),
		input.EventType,
		strings.ToLower(strings.TrimSpace(input.RecipientEmail)),
		strings.TrimSpace(input.MessageID),
		strings.TrimSpace(input.CampaignID),
		strings.TrimSpace(input.CampaignRecipient),
		strings.TrimSpace(input.DedupeSeed),
	}, ":")
	sum := sha256.Sum256([]byte(seed))

	return providerOrDefault(input.Provider) + ":" + input.EventType + ":" + hex.EncodeToString(sum[:])
}

func webhookDedupeSeed(payload []byte, correlationID string) string {
	if strings.TrimSpace(correlationID) != "" {
		return emailDeliverySourceWebhook + ":" + strings.TrimSpace(correlationID)
	}
	sum := sha256.Sum256(payload)

	return emailDeliverySourceWebhook + ":" + hex.EncodeToString(sum[:])
}

func rawProviderPayload(providerResponse string) entity.RawJSON {
	providerResponse = strings.TrimSpace(providerResponse)
	if providerResponse != "" && json.Valid([]byte(providerResponse)) {
		return entity.RawJSON(providerResponse)
	}
	raw, _ := json.Marshal(map[string]string{"provider_response": providerResponse})

	return entity.RawJSON(raw)
}

func providerOrDefault(provider string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return entity.EmailProviderCloudflare
	}

	return provider
}

func webhookOccurredAt(value any, fallback time.Time) time.Time {
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return time.Unix(int64(typed), 0).UTC()
		}
	case string:
		return parseEventTime(typed, fallback)
	}

	return fallback
}

func parseEventTime(value string, fallback time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC()
	}

	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func sensitiveEmailMetadataKey(key string) bool {
	normalized := normalizeKey(key)
	switch normalized {
	case "link", "otp", "token", "unsubscribe_url":
		return true
	default:
		return strings.HasSuffix(normalized, "_token")
	}
}

func valueHasTokenQuery(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}

	return parsed.Query().Get("token") != ""
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

	return fmt.Sprintf(
		`<!doctype html>
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

func normalizeUnsubscribeTokenOptions(opts Options) (string, string, map[string]string) {
	keyID := strings.TrimSpace(opts.UnsubscribeTokenKeyID)
	if keyID == "" {
		keyID = defaultTokenKeyID
	}
	seed := strings.TrimSpace(opts.UnsubscribeTokenSeed)
	secrets := map[string]string{}
	for key, secret := range opts.UnsubscribeTokenSecrets {
		key = strings.TrimSpace(key)
		secret = strings.TrimSpace(secret)
		if key == "" || secret == "" {
			continue
		}
		secrets[key] = secret
	}
	if seed == "" {
		seed = secrets[keyID]
	}
	if seed != "" && secrets[keyID] == "" {
		secrets[keyID] = seed
	}

	return keyID, seed, secrets
}

func (uc *UseCase) unsubscribeToken(userID, email string) string {
	payload := unsubscribeTokenPayload(userID, email)
	keyID := uc.tokenKeyID
	if keyID == "" {
		keyID = defaultTokenKeyID
	}
	signingInput := tokenVersionV2 + "." + keyID + "." + payload
	signature := unsubscribeTokenSignature(signingInput, uc.secretForTokenKey(keyID))

	return signingInput + "." + signature
}

func unsubscribeTokenPayload(userID, email string) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(strings.TrimSpace(userID) + "\n" + strings.ToLower(strings.TrimSpace(email))),
	)
}

func (uc *UseCase) parseUnsubscribeToken(token string) (string, string, error) {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	switch {
	case len(parts) == 4 && parts[0] == tokenVersionV2:
		return uc.parseV2UnsubscribeToken(parts)
	case len(parts) == 2:
		return uc.parseLegacyUnsubscribeToken(parts[0], parts[1])
	default:
		return "", "", entity.ErrInvalidUnsubscribeToken
	}
}

func (uc *UseCase) parseV2UnsubscribeToken(parts []string) (string, string, error) {
	keyID, payload, signature := parts[1], parts[2], parts[3]
	if keyID == "" || payload == "" || signature == "" {
		return "", "", entity.ErrInvalidUnsubscribeToken
	}
	secret := uc.tokenSecrets[keyID]
	if secret == "" {
		return "", "", entity.ErrInvalidUnsubscribeToken
	}
	signingInput := tokenVersionV2 + "." + keyID + "." + payload
	if !validUnsubscribeTokenSignature(signingInput, signature, secret) {
		return "", "", entity.ErrInvalidUnsubscribeToken
	}

	return decodeUnsubscribeTokenPayload(payload)
}

func (uc *UseCase) parseLegacyUnsubscribeToken(payload, signature string) (string, string, error) {
	if payload == "" || signature == "" {
		return "", "", entity.ErrInvalidUnsubscribeToken
	}
	for _, secret := range uc.legacyTokenSecrets() {
		if validUnsubscribeTokenSignature(payload, signature, secret) {
			return decodeUnsubscribeTokenPayload(payload)
		}
	}

	return "", "", entity.ErrInvalidUnsubscribeToken
}

func (uc *UseCase) secretForTokenKey(keyID string) string {
	if uc.tokenSecrets[keyID] != "" {
		return uc.tokenSecrets[keyID]
	}

	return uc.tokenSeed
}

func (uc *UseCase) legacyTokenSecrets() []string {
	secrets := make([]string, 0, len(uc.tokenSecrets)+1)
	seen := map[string]bool{}
	if uc.tokenSeed != "" {
		secrets = append(secrets, uc.tokenSeed)
		seen[uc.tokenSeed] = true
	}
	for _, secret := range uc.tokenSecrets {
		if secret == "" || seen[secret] {
			continue
		}
		secrets = append(secrets, secret)
		seen[secret] = true
	}

	return secrets
}

func unsubscribeTokenSignature(input, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(input))

	return hex.EncodeToString(mac.Sum(nil))
}

func validUnsubscribeTokenSignature(input, signature, secret string) bool {
	actual, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	expectedMAC := hmac.New(sha256.New, []byte(secret))
	_, _ = expectedMAC.Write([]byte(input))

	return hmac.Equal(actual, expectedMAC.Sum(nil))
}

func decodeUnsubscribeTokenPayload(payload string) (string, string, error) {
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

func (uc *UseCase) withDeliverabilityHeaders(
	message entity.EmailMessage,
	category string,
) entity.EmailMessage {
	if category != entity.EmailCategoryMarketing {
		return message
	}
	headerURL := uc.unsubscribeHeaderLink(message.Metadata["unsubscribe_url"])
	if headerURL == "" {
		return message
	}
	headers := cloneMap(message.Headers)
	headers[emailHeaderListUnsubscribe] = "<" + headerURL + ">"
	headers[emailHeaderListUnsubscribePost] = emailHeaderListUnsubscribeOneClick
	message.Headers = headers

	return message
}

func (uc *UseCase) unsubscribeHeaderLink(unsubscribeURL string) string {
	headerBaseURL := strings.TrimSpace(uc.headerURL)
	if headerBaseURL == "" {
		return ""
	}
	token := tokenFromUnsubscribeURL(unsubscribeURL)
	if token == "" {
		return ""
	}
	parsed, err := url.Parse(headerBaseURL)
	if err != nil || !validAbsoluteHTTPURL(parsed) {
		return ""
	}
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()

	return parsed.String()
}

func tokenFromUnsubscribeURL(unsubscribeURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(unsubscribeURL))
	if err != nil {
		return ""
	}

	return strings.TrimSpace(parsed.Query().Get("token"))
}

func validAbsoluteHTTPURL(parsed *url.URL) bool {
	return parsed != nil &&
		parsed.IsAbs() &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.Host != ""
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

func (uc *UseCase) templateIsCritical(ctx context.Context, templateID string) (bool, error) {
	if strings.TrimSpace(templateID) == "" {
		return false, nil
	}
	template, err := uc.repo.GetEmailTemplateByID(ctx, templateID)
	if errors.Is(err, entity.ErrEmailTemplateNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return template.Critical, nil
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

func normalizeDeliveryEventFilter(filter repo.EmailDeliveryEventFilter) repo.EmailDeliveryEventFilter {
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}

	return filter
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
	maps.Copy(cloned, values)

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
