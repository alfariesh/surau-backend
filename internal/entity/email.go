package entity

import "time"

const (
	EmailCategoryTransactional = "transactional"
	EmailCategoryMarketing     = "marketing"

	EmailTemplateKeyVerification            = "auth_verification"
	EmailTemplateKeyPasswordReset           = "auth_password_reset"
	EmailTemplateKeyEmailChangeVerification = "auth_email_change_verification"
	EmailTemplateKeyPasswordChanged         = "auth_password_changed"
	EmailTemplateKeyEmailVerified           = "auth_email_verified"
	EmailTemplateKeyNewLogin                = "auth_new_login"
	EmailTemplateKeyFailedLogin             = "auth_failed_login"
	EmailTemplateKeyRoleChanged             = "auth_role_changed"
	EmailTemplateKeyEmailChanged            = "auth_email_changed"
	EmailTemplateKeyAccountDeleted          = "auth_account_deleted"

	EmailMessageStatusQueued  = "queued"
	EmailMessageStatusSent    = "sent"
	EmailMessageStatusFailed  = "failed"
	EmailMessageStatusSkipped = "skipped"

	EmailCampaignStatusDraft     = "draft"
	EmailCampaignStatusScheduled = "scheduled"
	EmailCampaignStatusSending   = "sending"
	EmailCampaignStatusSent      = "sent"
	EmailCampaignStatusCancelled = "cancelled"

	EmailRecipientStatusPending = "pending"
	EmailRecipientStatusSent    = "sent"
	EmailRecipientStatusFailed  = "failed"
	EmailRecipientStatusSkipped = "skipped"

	EmailSuppressionScopeMarketing = "marketing"
	EmailSuppressionScopeAll       = "all"

	EmailProviderLog        = "log"
	EmailProviderCloudflare = "cloudflare"

	EmailDeliveryEventBounceHard = "bounce_hard"
	EmailDeliveryEventComplaint  = "complaint"

	EmailProviderPollCursorCloudflareSending = "cloudflare:email_sending"
)

// EmailMessage describes a rendered email ready for provider delivery.
type EmailMessage struct {
	To                string            `json:"to"`
	Subject           string            `json:"subject"`
	HTML              string            `json:"html"`
	Text              string            `json:"text"`
	Critical          bool              `json:"critical,omitempty"`
	Category          string            `json:"category,omitempty"`
	TemplateKey       string            `json:"template_key,omitempty"`
	TemplateVersionID string            `json:"template_version_id,omitempty"`
	Lang              string            `json:"lang,omitempty"`
	UserID            string            `json:"user_id,omitempty"`
	MessageID         string            `json:"message_id,omitempty"`
	CampaignID        string            `json:"campaign_id,omitempty"`
	CampaignRecipient string            `json:"campaign_recipient_id,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// EmailSendResult describes the provider response for one send attempt.
type EmailSendResult struct {
	Provider         string   `json:"provider"`
	Delivered        []string `json:"delivered,omitempty"`
	Queued           []string `json:"queued,omitempty"`
	PermanentBounces []string `json:"permanent_bounces,omitempty"`
	ProviderResponse string   `json:"provider_response,omitempty"`
}

// EmailTemplate is the admin-managed template family.
type EmailTemplate struct {
	ID         string     `json:"id"`
	Key        string     `json:"key"`
	Name       string     `json:"name"`
	Category   string     `json:"category"`
	Critical   bool       `json:"critical"`
	Enabled    bool       `json:"enabled"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// EmailTemplatePatch updates template metadata.
type EmailTemplatePatch struct {
	Name      *string
	Enabled   *bool
	Archived  *bool
	UpdatedBy string
}

// EmailTemplateVersion stores one localized template revision.
type EmailTemplateVersion struct {
	ID                  string     `json:"id"`
	TemplateID          string     `json:"template_id"`
	Lang                string     `json:"lang"`
	Version             int        `json:"version"`
	SubjectTemplate     string     `json:"subject_template"`
	PreviewTemplate     string     `json:"preview_template"`
	TitleTemplate       string     `json:"title_template"`
	BodyTemplate        string     `json:"body_template"`
	ButtonLabelTemplate string     `json:"button_label_template"`
	ButtonURLTemplate   string     `json:"button_url_template"`
	NoteTemplate        string     `json:"note_template"`
	FooterTemplate      string     `json:"footer_template"`
	TextTemplate        string     `json:"text_template"`
	RequiredVariables   []string   `json:"required_variables"`
	Published           bool       `json:"published"`
	CreatedBy           *string    `json:"created_by,omitempty"`
	PublishedBy         *string    `json:"published_by,omitempty"`
	PublishedAt         *time.Time `json:"published_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// EmailTemplateVersionPatch updates editable template version fields.
type EmailTemplateVersionPatch struct {
	SubjectTemplate     *string
	PreviewTemplate     *string
	TitleTemplate       *string
	BodyTemplate        *string
	ButtonLabelTemplate *string
	ButtonURLTemplate   *string
	NoteTemplate        *string
	FooterTemplate      *string
	TextTemplate        *string
	RequiredVariables   *[]string
	UpdatedBy           string
}

// EmailEventSetting controls one transactional event.
type EmailEventSetting struct {
	Key             string    `json:"key"`
	TemplateID      string    `json:"template_id"`
	Enabled         bool      `json:"enabled"`
	Critical        bool      `json:"critical"`
	CooldownSeconds *int64    `json:"cooldown_seconds,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// EmailEventSettingPatch updates a transactional event toggle/cooldown.
type EmailEventSettingPatch struct {
	Enabled         *bool
	CooldownSeconds *int64
	UpdatedBy       string
}

// EmailMessageLog stores one provider delivery attempt snapshot.
type EmailMessageLog struct {
	ID                string            `json:"id"`
	Category          string            `json:"category"`
	TemplateKey       string            `json:"template_key,omitempty"`
	TemplateVersionID string            `json:"template_version_id,omitempty"`
	CampaignID        string            `json:"campaign_id,omitempty"`
	CampaignRecipient string            `json:"campaign_recipient_id,omitempty"`
	UserID            string            `json:"user_id,omitempty"`
	RecipientEmail    string            `json:"recipient_email"`
	Lang              string            `json:"lang"`
	Subject           string            `json:"subject"`
	HTML              string            `json:"-" swaggerignore:"true"`
	Text              string            `json:"-" swaggerignore:"true"`
	Critical          bool              `json:"-" swaggerignore:"true"`
	Headers           map[string]string `json:"-" swaggerignore:"true"`
	Status            string            `json:"status"`
	Attempts          int               `json:"attempts"`
	ProviderResponse  string            `json:"provider_response,omitempty"`
	Error             string            `json:"error,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	ScheduledAt       *time.Time        `json:"scheduled_at,omitempty"`
	SentAt            *time.Time        `json:"sent_at,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

// EmailSubscription stores marketing consent for one user.
type EmailSubscription struct {
	UserID         string     `json:"user_id"`
	MarketingOptIn bool       `json:"marketing_opt_in"`
	OptedInAt      *time.Time `json:"opted_in_at,omitempty"`
	OptedOutAt     *time.Time `json:"opted_out_at,omitempty"`
	Source         string     `json:"source,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// EmailSuppression prevents delivery to an address.
type EmailSuppression struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Scope     string    `json:"scope"`
	Reason    string    `json:"reason"`
	CreatedBy *string   `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// EmailDeliveryEvent stores provider delivery audit events.
type EmailDeliveryEvent struct {
	ID                string    `json:"id"`
	DedupeKey         string    `json:"dedupe_key"`
	Provider          string    `json:"provider"`
	EventType         string    `json:"event_type"`
	RecipientEmail    string    `json:"recipient_email"`
	MessageID         string    `json:"message_id,omitempty"`
	CampaignID        string    `json:"campaign_id,omitempty"`
	CampaignRecipient string    `json:"campaign_recipient_id,omitempty"`
	Reason            string    `json:"reason,omitempty"`
	Diagnostic        string    `json:"diagnostic,omitempty"`
	RawPayload        RawJSON   `json:"raw_payload,omitempty" swaggertype:"object"`
	OccurredAt        time.Time `json:"occurred_at"`
	CreatedAt         time.Time `json:"created_at"`
}

// CloudflareEmailEventPollQuery identifies one Cloudflare Email Service analytics poll window.
type CloudflareEmailEventPollQuery struct {
	ZoneID string
	Start  time.Time
	End    time.Time
	Limit  int
}

// CloudflareEmailEvent is one outbound Email Service event from Cloudflare GraphQL analytics.
type CloudflareEmailEvent struct {
	Datetime      time.Time `json:"datetime"`
	From          string    `json:"from,omitempty"`
	To            string    `json:"to"`
	Subject       string    `json:"subject,omitempty"`
	Status        string    `json:"status"`
	EventType     string    `json:"event_type,omitempty"`
	SendingDomain string    `json:"sending_domain,omitempty"`
	MessageID     string    `json:"message_id,omitempty"`
	ErrorCause    string    `json:"error_cause,omitempty"`
	ErrorDetail   string    `json:"error_detail,omitempty"`
	RawPayload    RawJSON   `json:"raw_payload,omitempty" swaggertype:"object"`
}

// EmailProviderPollCursor stores the last successful provider polling checkpoint.
type EmailProviderPollCursor struct {
	Provider     string    `json:"provider"`
	CursorKey    string    `json:"cursor_key"`
	LastPolledAt time.Time `json:"last_polled_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// EmailCampaignDeliveryEventSummary summarizes delivery events for one campaign.
type EmailCampaignDeliveryEventSummary struct {
	CampaignID       string     `json:"campaign_id"`
	Total            int        `json:"total"`
	BounceHard       int        `json:"bounce_hard"`
	Complaint        int        `json:"complaint"`
	UniqueRecipients int        `json:"unique_recipients"`
	LastOccurredAt   *time.Time `json:"last_occurred_at,omitempty"`
}

// EmailWebhookIngestResult summarizes webhook processing.
type EmailWebhookIngestResult struct {
	Accepted   int `json:"accepted"`
	Processed  int `json:"processed"`
	Suppressed int `json:"suppressed"`
	Duplicates int `json:"duplicates,omitempty"`
}

// EmailCampaign stores a marketing campaign.
type EmailCampaign struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	TemplateID  string              `json:"template_id"`
	Status      string              `json:"status"`
	Audience    EmailAudienceFilter `json:"audience"`
	Metadata    map[string]string   `json:"metadata,omitempty"`
	ScheduledAt *time.Time          `json:"scheduled_at,omitempty"`
	SentAt      *time.Time          `json:"sent_at,omitempty"`
	CancelledAt *time.Time          `json:"cancelled_at,omitempty"`
	CreatedBy   *string             `json:"created_by,omitempty"`
	UpdatedBy   *string             `json:"updated_by,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

// EmailCampaignRecipient stores one target recipient for a campaign.
type EmailCampaignRecipient struct {
	ID             string     `json:"id"`
	CampaignID     string     `json:"campaign_id"`
	UserID         string     `json:"user_id"`
	Email          string     `json:"email"`
	Lang           string     `json:"lang"`
	UnsubscribeURL string     `json:"unsubscribe_url,omitempty"`
	Status         string     `json:"status"`
	MessageID      string     `json:"message_id,omitempty"`
	Error          string     `json:"error,omitempty"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// EmailAudienceFilter is the v1 marketing audience selector.
type EmailAudienceFilter struct {
	Role        string     `json:"role,omitempty"`
	Lang        string     `json:"lang,omitempty"`
	CreatedFrom *time.Time `json:"created_from,omitempty"`
	CreatedTo   *time.Time `json:"created_to,omitempty"`
	Limit       int        `json:"limit,omitempty"`
}

// EmailAudienceRecipient is a user selected by a marketing audience filter.
type EmailAudienceRecipient struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Lang   string `json:"lang"`
}

// EmailPreview contains rendered content for admin preview/test-send.
type EmailPreview struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
	Lang    string `json:"lang"`
}

// TransactionalEmailRequest asks the email service to send one transactional email.
type TransactionalEmailRequest struct {
	Key       string
	To        string
	UserID    string
	Lang      string
	Variables map[string]string
	Fallback  EmailMessage
	Critical  bool
}
