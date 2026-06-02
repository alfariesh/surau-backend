package request

import "time"

// EmailTemplateCreate creates an admin-managed email template.
type EmailTemplateCreate struct {
	Key      string `json:"key"      validate:"required,max=128" example:"weekly_digest"`
	Name     string `json:"name"     validate:"required,max=255" example:"Weekly Digest"`
	Category string `json:"category" validate:"required,oneof=transactional marketing" example:"marketing"`
	Critical bool   `json:"critical" example:"false"`
	Enabled  *bool  `json:"enabled"  example:"true"`
} // @name v1.EmailTemplateCreate

// EmailTemplateUpdate updates template metadata.
type EmailTemplateUpdate struct {
	Name     *string `json:"name"     validate:"omitempty,max=255" example:"Weekly Digest"`
	Enabled  *bool   `json:"enabled"  example:"true"`
	Archived *bool   `json:"archived" example:"false"`
} // @name v1.EmailTemplateUpdate

// EmailTemplateVersionCreate creates one localized template version.
type EmailTemplateVersionCreate struct {
	Lang                string   `json:"lang"                  validate:"required,max=8" example:"id"`
	SubjectTemplate     string   `json:"subject_template"      validate:"required" example:"Update dari Surau"`
	PreviewTemplate     string   `json:"preview_template"      example:"Ada bacaan baru untuk Anda."`
	TitleTemplate       string   `json:"title_template"        example:"Update Surau"`
	BodyTemplate        string   `json:"body_template"         example:"Assalamu'alaikum, {{.name}}."`
	ButtonLabelTemplate string   `json:"button_label_template" example:"Buka Surau"`
	ButtonURLTemplate   string   `json:"button_url_template"   example:"https://surau.org"`
	NoteTemplate        string   `json:"note_template"`
	FooterTemplate      string   `json:"footer_template"`
	TextTemplate        string   `json:"text_template"         validate:"required" example:"Assalamu'alaikum, {{.name}}."`
	RequiredVariables   []string `json:"required_variables"    example:"name"`
} // @name v1.EmailTemplateVersionCreate

// EmailTemplateVersionUpdate updates a draft template version.
type EmailTemplateVersionUpdate struct {
	SubjectTemplate     *string   `json:"subject_template"`
	PreviewTemplate     *string   `json:"preview_template"`
	TitleTemplate       *string   `json:"title_template"`
	BodyTemplate        *string   `json:"body_template"`
	ButtonLabelTemplate *string   `json:"button_label_template"`
	ButtonURLTemplate   *string   `json:"button_url_template"`
	NoteTemplate        *string   `json:"note_template"`
	FooterTemplate      *string   `json:"footer_template"`
	TextTemplate        *string   `json:"text_template"`
	RequiredVariables   *[]string `json:"required_variables"`
} // @name v1.EmailTemplateVersionUpdate

// EmailPreviewRequest renders a template for preview.
type EmailPreviewRequest struct {
	Lang      string            `json:"lang"      validate:"required,max=8" example:"id"`
	Variables map[string]string `json:"variables"`
} // @name v1.EmailPreviewRequest

// EmailTestSendRequest sends a template preview to one recipient.
type EmailTestSendRequest struct {
	To        string            `json:"to"        validate:"required,email" example:"admin@example.com"`
	Lang      string            `json:"lang"      validate:"required,max=8" example:"id"`
	Variables map[string]string `json:"variables"`
} // @name v1.EmailTestSendRequest

// EmailEventSettingUpdate updates one transactional event setting.
type EmailEventSettingUpdate struct {
	Enabled         *bool  `json:"enabled" example:"true"`
	CooldownSeconds *int64 `json:"cooldown_seconds" validate:"omitempty,min=1" example:"86400"`
} // @name v1.EmailEventSettingUpdate

// EmailSubscriptionUpdate updates current user's marketing consent.
type EmailSubscriptionUpdate struct {
	MarketingOptIn bool `json:"marketing_opt_in" example:"true"`
} // @name v1.EmailSubscriptionUpdate

// EmailUnsubscribe unsubscribes from a public token.
type EmailUnsubscribe struct {
	Token string `json:"token" validate:"required"`
} // @name v1.EmailUnsubscribe

// EmailSuppressionCreate creates one suppression.
type EmailSuppressionCreate struct {
	Email  string `json:"email"  validate:"required,email" example:"user@example.com"`
	Scope  string `json:"scope"  validate:"required,oneof=marketing all" example:"marketing"`
	Reason string `json:"reason" validate:"required,max=128" example:"manual"`
} // @name v1.EmailSuppressionCreate

// EmailCampaignCreate creates a marketing campaign draft.
type EmailCampaignCreate struct {
	Name       string            `json:"name"        validate:"required,max=255" example:"Ramadan Digest"`
	TemplateID string            `json:"template_id" validate:"required" example:"550e8400-e29b-41d4-a716-446655440000"`
	Audience   EmailAudience     `json:"audience"`
	Metadata   map[string]string `json:"metadata"`
} // @name v1.EmailCampaignCreate

// EmailCampaignUpdate updates a marketing campaign draft.
type EmailCampaignUpdate struct {
	Name       string            `json:"name"        validate:"required,max=255" example:"Ramadan Digest"`
	TemplateID string            `json:"template_id" validate:"required" example:"550e8400-e29b-41d4-a716-446655440000"`
	Audience   EmailAudience     `json:"audience"`
	Metadata   map[string]string `json:"metadata"`
} // @name v1.EmailCampaignUpdate

// EmailAudience is the v1 marketing audience filter.
type EmailAudience struct {
	Role        string     `json:"role"         validate:"omitempty,max=32" example:"user"`
	Lang        string     `json:"lang"         validate:"omitempty,max=8" example:"id"`
	CreatedFrom *time.Time `json:"created_from" example:"2026-01-01T00:00:00Z"`
	CreatedTo   *time.Time `json:"created_to"   example:"2026-05-01T00:00:00Z"`
	Limit       int        `json:"limit"        validate:"omitempty,min=1,max=10000" example:"1000"`
} // @name v1.EmailAudience

// EmailCampaignSchedule schedules a draft campaign.
type EmailCampaignSchedule struct {
	ScheduledAt time.Time `json:"scheduled_at" validate:"required" example:"2026-06-01T09:00:00Z"`
} // @name v1.EmailCampaignSchedule

// EmailCampaignTestSend sends a campaign template to one recipient.
type EmailCampaignTestSend struct {
	To        string            `json:"to"        validate:"required,email" example:"admin@example.com"`
	Lang      string            `json:"lang"      validate:"required,max=8" example:"id"`
	Variables map[string]string `json:"variables"`
} // @name v1.EmailCampaignTestSend
