package v1

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/gofiber/fiber/v2"
)

// @Summary  List email templates
// @ID       admin-email-list-templates
// @Tags     admin-emails
// @Produce  json
// @Param    q                query string false "Search key or name"
// @Param    category         query string false "Template category" Enums(transactional,marketing)
// @Param    include_archived query bool   false "Include archived templates"
// @Param    limit            query int    false "Page size" default(50)
// @Param    offset           query int    false "Page offset" default(0)
// @Success  200 {object} response.EmailTemplateList
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/templates [get]
func (r *V1) adminEmailTemplates(ctx *fiber.Ctx) error {
	templates, total, err := r.email.Templates(ctx.UserContext(), repo.EmailTemplateFilter{
		Query:           ctx.Query("q"),
		Category:        ctx.Query("category"),
		IncludeArchived: ctx.QueryBool("include_archived"),
		Limit:           uint64(queryInt(ctx, "limit", 50)),
		Offset:          uint64(queryInt(ctx, "offset", 0)),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.EmailTemplateList{Items: templates, Total: total})
}

// @Summary  Create email template
// @ID       admin-email-create-template
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    request body request.EmailTemplateCreate true "Template metadata"
// @Success  201 {object} entity.EmailTemplate
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/templates [post]
func (r *V1) adminEmailCreateTemplate(ctx *fiber.Ctx) error {
	var body request.EmailTemplateCreate
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	template, err := r.email.CreateTemplate(ctx.UserContext(), entity.EmailTemplate{
		Key:      body.Key,
		Name:     body.Name,
		Category: body.Category,
		Critical: body.Critical,
		Enabled:  enabled,
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusCreated).JSON(template)
}

// @Summary  Get email template
// @ID       admin-email-get-template
// @Tags     admin-emails
// @Produce  json
// @Param    id path string true "Template ID"
// @Success  200 {object} entity.EmailTemplate
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/templates/{id} [get]
func (r *V1) adminEmailTemplate(ctx *fiber.Ctx) error {
	template, err := r.email.Template(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(template)
}

// @Summary  Update email template
// @ID       admin-email-update-template
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    id      path string                      true "Template ID"
// @Param    request body request.EmailTemplateUpdate true "Template patch"
// @Success  200 {object} entity.EmailTemplate
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/templates/{id} [patch]
func (r *V1) adminEmailUpdateTemplate(ctx *fiber.Ctx) error {
	var body request.EmailTemplateUpdate
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}

	template, err := r.email.UpdateTemplate(ctx.UserContext(), ctx.Params("id"), entity.EmailTemplatePatch{
		Name:      body.Name,
		Enabled:   body.Enabled,
		Archived:  body.Archived,
		UpdatedBy: emailActorID(ctx),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(template)
}

// @Summary  Delete email template
// @ID       admin-email-delete-template
// @Tags     admin-emails
// @Param    id path string true "Template ID"
// @Success  204
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/templates/{id} [delete]
func (r *V1) adminEmailDeleteTemplate(ctx *fiber.Ctx) error {
	if err := r.email.DeleteTemplate(ctx.UserContext(), ctx.Params("id")); err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// @Summary  List email template versions
// @ID       admin-email-list-template-versions
// @Tags     admin-emails
// @Produce  json
// @Param    id path string true "Template ID"
// @Success  200 {object} response.EmailTemplateVersionList
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/templates/{id}/versions [get]
func (r *V1) adminEmailTemplateVersions(ctx *fiber.Ctx) error {
	versions, err := r.email.Versions(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.EmailTemplateVersionList{Items: versions, Total: len(versions)})
}

// @Summary  Create email template version
// @ID       admin-email-create-template-version
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    id      path string                             true "Template ID"
// @Param    request body request.EmailTemplateVersionCreate true "Localized version"
// @Success  201 {object} entity.EmailTemplateVersion
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/templates/{id}/versions [post]
func (r *V1) adminEmailCreateTemplateVersion(ctx *fiber.Ctx) error {
	var body request.EmailTemplateVersionCreate
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	actorID := emailActorID(ctx)
	version, err := r.email.CreateVersion(ctx.UserContext(), entity.EmailTemplateVersion{
		TemplateID:          ctx.Params("id"),
		Lang:                body.Lang,
		SubjectTemplate:     body.SubjectTemplate,
		PreviewTemplate:     body.PreviewTemplate,
		TitleTemplate:       body.TitleTemplate,
		BodyTemplate:        body.BodyTemplate,
		ButtonLabelTemplate: body.ButtonLabelTemplate,
		ButtonURLTemplate:   body.ButtonURLTemplate,
		NoteTemplate:        body.NoteTemplate,
		FooterTemplate:      body.FooterTemplate,
		TextTemplate:        body.TextTemplate,
		RequiredVariables:   body.RequiredVariables,
		CreatedBy:           nullableString(actorID),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusCreated).JSON(version)
}

// @Summary  Update email template version
// @ID       admin-email-update-template-version
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    id      path string                             true "Version ID"
// @Param    request body request.EmailTemplateVersionUpdate true "Version patch"
// @Success  200 {object} entity.EmailTemplateVersion
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/versions/{id} [patch]
func (r *V1) adminEmailUpdateTemplateVersion(ctx *fiber.Ctx) error {
	var body request.EmailTemplateVersionUpdate
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	version, err := r.email.UpdateVersion(ctx.UserContext(), ctx.Params("id"), entity.EmailTemplateVersionPatch{
		SubjectTemplate:     body.SubjectTemplate,
		PreviewTemplate:     body.PreviewTemplate,
		TitleTemplate:       body.TitleTemplate,
		BodyTemplate:        body.BodyTemplate,
		ButtonLabelTemplate: body.ButtonLabelTemplate,
		ButtonURLTemplate:   body.ButtonURLTemplate,
		NoteTemplate:        body.NoteTemplate,
		FooterTemplate:      body.FooterTemplate,
		TextTemplate:        body.TextTemplate,
		RequiredVariables:   body.RequiredVariables,
		UpdatedBy:           emailActorID(ctx),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(version)
}

// @Summary  Publish email template version
// @ID       admin-email-publish-template-version
// @Tags     admin-emails
// @Produce  json
// @Param    id path string true "Version ID"
// @Success  200 {object} entity.EmailTemplateVersion
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/versions/{id}/publish [post]
func (r *V1) adminEmailPublishTemplateVersion(ctx *fiber.Ctx) error {
	version, err := r.email.PublishVersion(ctx.UserContext(), ctx.Params("id"), emailActorID(ctx))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(version)
}

// @Summary  Preview email template
// @ID       admin-email-preview-template
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    id      path string                true "Template ID"
// @Param    request body request.EmailPreviewRequest true "Preview variables"
// @Success  200 {object} entity.EmailPreview
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/templates/{id}/preview [post]
func (r *V1) adminEmailPreviewTemplate(ctx *fiber.Ctx) error {
	var body request.EmailPreviewRequest
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	preview, err := r.email.PreviewTemplate(ctx.UserContext(), ctx.Params("id"), body.Lang, body.Variables)
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(preview)
}

// @Summary  Test send email template
// @ID       admin-email-test-send-template
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    id      path string                       true "Template ID"
// @Param    request body request.EmailTestSendRequest true "Recipient and variables"
// @Success  202 {object} entity.EmailMessageLog
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  503 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/templates/{id}/test-send [post]
func (r *V1) adminEmailTestSendTemplate(ctx *fiber.Ctx) error {
	var body request.EmailTestSendRequest
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	message, err := r.email.TestSendTemplate(
		ctx.UserContext(),
		ctx.Params("id"),
		body.Lang,
		body.To,
		body.Variables,
	)
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusAccepted).JSON(message)
}

// @Summary  Get transactional email event setting
// @ID       admin-email-get-event-setting
// @Tags     admin-emails
// @Produce  json
// @Param    key path string true "Transactional event key"
// @Success  200 {object} entity.EmailEventSetting
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/events/{key} [get]
func (r *V1) adminEmailEventSetting(ctx *fiber.Ctx) error {
	setting, err := r.email.EventSetting(ctx.UserContext(), ctx.Params("key"))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(setting)
}

// @Summary  Update transactional email event setting
// @ID       admin-email-update-event-setting
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    key     path string                          true "Transactional event key"
// @Param    request body request.EmailEventSettingUpdate true "Event setting patch"
// @Success  200 {object} entity.EmailEventSetting
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/events/{key} [patch]
func (r *V1) adminEmailUpdateEventSetting(ctx *fiber.Ctx) error {
	var body request.EmailEventSettingUpdate
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	setting, err := r.email.UpdateEventSetting(ctx.UserContext(), ctx.Params("key"), entity.EmailEventSettingPatch{
		Enabled:         body.Enabled,
		CooldownSeconds: body.CooldownSeconds,
		UpdatedBy:       emailActorID(ctx),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(setting)
}

// @Summary  List email delivery messages
// @ID       admin-email-list-messages
// @Tags     admin-emails
// @Produce  json
// @Param    category query string false "Email category" Enums(transactional,marketing)
// @Param    status   query string false "Message status" Enums(queued,sent,failed,skipped)
// @Param    email    query string false "Recipient email"
// @Param    limit    query int    false "Page size" default(50)
// @Param    offset   query int    false "Page offset" default(0)
// @Success  200 {object} response.EmailMessageList
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/messages [get]
func (r *V1) adminEmailMessages(ctx *fiber.Ctx) error {
	messages, total, err := r.email.Messages(ctx.UserContext(), repo.EmailMessageFilter{
		Category: ctx.Query("category"),
		Status:   ctx.Query("status"),
		Email:    ctx.Query("email"),
		Limit:    uint64(queryInt(ctx, "limit", 50)),
		Offset:   uint64(queryInt(ctx, "offset", 0)),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.EmailMessageList{Items: messages, Total: total})
}

// @Summary  List email delivery events
// @ID       admin-email-list-delivery-events
// @Tags     admin-emails
// @Produce  json
// @Param    provider              query string false "Email provider"
// @Param    event_type            query string false "Delivery event type" Enums(bounce_hard,complaint)
// @Param    email                 query string false "Recipient email"
// @Param    message_id            query string false "Email message ID"
// @Param    campaign_id           query string false "Email campaign ID"
// @Param    campaign_recipient_id query string false "Email campaign recipient ID"
// @Param    limit                 query int    false "Page size" default(50)
// @Param    offset                query int    false "Page offset" default(0)
// @Success  200 {object} response.EmailDeliveryEventList
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/delivery-events [get]
func (r *V1) adminEmailDeliveryEvents(ctx *fiber.Ctx) error {
	events, total, err := r.email.DeliveryEvents(ctx.UserContext(), adminEmailDeliveryEventFilter(ctx))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.EmailDeliveryEventList{Items: events, Total: total})
}

// @Summary  List delivery events for an email message
// @ID       admin-email-list-message-delivery-events
// @Tags     admin-emails
// @Produce  json
// @Param    id     path  string true  "Email message ID"
// @Param    limit  query int    false "Page size" default(50)
// @Param    offset query int    false "Page offset" default(0)
// @Success  200 {object} response.EmailDeliveryEventList
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/messages/{id}/delivery-events [get]
func (r *V1) adminEmailMessageDeliveryEvents(ctx *fiber.Ctx) error {
	filter := adminEmailDeliveryEventFilter(ctx)
	filter.MessageID = ctx.Params("id")
	events, total, err := r.email.DeliveryEvents(ctx.UserContext(), filter)
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.EmailDeliveryEventList{Items: events, Total: total})
}

// @Summary  Resend a dead-lettered transactional email
// @ID       admin-email-resend-message
// @Tags     admin-emails
// @Produce  json
// @Param    id path string true "Email message ID"
// @Success  202 {object} entity.EmailMessageLog
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  409 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/messages/{id}/resend [post]
func (r *V1) adminEmailResendMessage(ctx *fiber.Ctx) error {
	message, err := r.email.ResendMessage(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	// Audit trail: the requeue keeps the previous failure on the row; stamp
	// who pushed the button alongside it (request-scoped log carries
	// request_id/trace_id).
	r.reqLog(ctx).Info(
		"admin email resend: message=%s recipient=%s actor=%s previous_error=%q",
		message.ID, message.RecipientEmail, emailActorID(ctx), message.Error,
	)

	return ctx.Status(http.StatusAccepted).JSON(message)
}

// @Summary  List delivery events for a campaign recipient
// @ID       admin-email-list-campaign-recipient-delivery-events
// @Tags     admin-emails
// @Produce  json
// @Param    id     path  string true  "Email campaign recipient ID"
// @Param    limit  query int    false "Page size" default(50)
// @Param    offset query int    false "Page offset" default(0)
// @Success  200 {object} response.EmailDeliveryEventList
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaign-recipients/{id}/delivery-events [get]
func (r *V1) adminEmailCampaignRecipientDeliveryEvents(ctx *fiber.Ctx) error {
	filter := adminEmailDeliveryEventFilter(ctx)
	filter.CampaignRecipientID = ctx.Params("id")
	events, total, err := r.email.DeliveryEvents(ctx.UserContext(), filter)
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.EmailDeliveryEventList{Items: events, Total: total})
}

func adminEmailDeliveryEventFilter(ctx *fiber.Ctx) repo.EmailDeliveryEventFilter {
	return repo.EmailDeliveryEventFilter{
		Provider:            ctx.Query("provider"),
		EventType:           ctx.Query("event_type"),
		Email:               ctx.Query("email"),
		MessageID:           ctx.Query("message_id"),
		CampaignID:          ctx.Query("campaign_id"),
		CampaignRecipientID: ctx.Query("campaign_recipient_id"),
		Limit:               uint64(queryInt(ctx, "limit", 50)),
		Offset:              uint64(queryInt(ctx, "offset", 0)),
	}
}

// @Summary  List email suppressions
// @ID       admin-email-list-suppressions
// @Tags     admin-emails
// @Produce  json
// @Param    email  query string false "Suppressed email"
// @Param    scope  query string false "Suppression scope" Enums(marketing,all)
// @Param    limit  query int    false "Page size" default(50)
// @Param    offset query int    false "Page offset" default(0)
// @Success  200 {object} response.EmailSuppressionList
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/suppressions [get]
func (r *V1) adminEmailSuppressions(ctx *fiber.Ctx) error {
	suppressions, total, err := r.email.Suppressions(ctx.UserContext(), repo.EmailSuppressionFilter{
		Email:  ctx.Query("email"),
		Scope:  ctx.Query("scope"),
		Limit:  uint64(queryInt(ctx, "limit", 50)),
		Offset: uint64(queryInt(ctx, "offset", 0)),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.EmailSuppressionList{Items: suppressions, Total: total})
}

// @Summary  Create email suppression
// @ID       admin-email-create-suppression
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    request body request.EmailSuppressionCreate true "Suppression"
// @Success  201 {object} entity.EmailSuppression
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/suppressions [post]
func (r *V1) adminEmailCreateSuppression(ctx *fiber.Ctx) error {
	var body request.EmailSuppressionCreate
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	suppression, err := r.email.CreateSuppression(ctx.UserContext(), entity.EmailSuppression{
		Email:     body.Email,
		Scope:     body.Scope,
		Reason:    body.Reason,
		CreatedBy: nullableString(emailActorID(ctx)),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusCreated).JSON(suppression)
}

// @Summary  Delete email suppression
// @ID       admin-email-delete-suppression
// @Tags     admin-emails
// @Param    id path string true "Suppression ID"
// @Success  204
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/suppressions/{id} [delete]
func (r *V1) adminEmailDeleteSuppression(ctx *fiber.Ctx) error {
	if err := r.email.DeleteSuppression(ctx.UserContext(), ctx.Params("id")); err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// @Summary  List email campaigns
// @ID       admin-email-list-campaigns
// @Tags     admin-emails
// @Produce  json
// @Param    status query string false "Campaign status" Enums(draft,scheduled,sending,sent,cancelled)
// @Param    limit  query int    false "Page size" default(50)
// @Param    offset query int    false "Page offset" default(0)
// @Success  200 {object} response.EmailCampaignList
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns [get]
func (r *V1) adminEmailCampaigns(ctx *fiber.Ctx) error {
	campaigns, total, err := r.email.Campaigns(ctx.UserContext(), repo.EmailCampaignFilter{
		Status: ctx.Query("status"),
		Limit:  uint64(queryInt(ctx, "limit", 50)),
		Offset: uint64(queryInt(ctx, "offset", 0)),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.EmailCampaignList{Items: campaigns, Total: total})
}

// @Summary  Summarize campaign delivery events
// @ID       admin-email-campaign-delivery-event-summary
// @Tags     admin-emails
// @Produce  json
// @Param    id path string true "Campaign ID"
// @Success  200 {object} entity.EmailCampaignDeliveryEventSummary
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns/{id}/delivery-event-summary [get]
func (r *V1) adminEmailCampaignDeliveryEventSummary(ctx *fiber.Ctx) error {
	summary, err := r.email.CampaignDeliveryEventSummary(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(summary)
}

// @Summary  Create email campaign
// @ID       admin-email-create-campaign
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    request body request.EmailCampaignCreate true "Campaign draft"
// @Success  201 {object} entity.EmailCampaign
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns [post]
func (r *V1) adminEmailCreateCampaign(ctx *fiber.Ctx) error {
	var body request.EmailCampaignCreate
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	campaign, err := r.email.CreateCampaign(ctx.UserContext(), entity.EmailCampaign{
		Name:       body.Name,
		TemplateID: body.TemplateID,
		Audience:   toEmailAudience(body.Audience),
		Metadata:   body.Metadata,
		CreatedBy:  nullableString(emailActorID(ctx)),
		UpdatedBy:  nullableString(emailActorID(ctx)),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusCreated).JSON(campaign)
}

// @Summary  Get email campaign
// @ID       admin-email-get-campaign
// @Tags     admin-emails
// @Produce  json
// @Param    id path string true "Campaign ID"
// @Success  200 {object} entity.EmailCampaign
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns/{id} [get]
func (r *V1) adminEmailCampaign(ctx *fiber.Ctx) error {
	campaign, err := r.email.Campaign(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(campaign)
}

// @Summary  Update email campaign draft
// @ID       admin-email-update-campaign
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    id      path string                      true "Campaign ID"
// @Param    request body request.EmailCampaignUpdate true "Campaign draft"
// @Success  200 {object} entity.EmailCampaign
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns/{id} [patch]
func (r *V1) adminEmailUpdateCampaign(ctx *fiber.Ctx) error {
	var body request.EmailCampaignUpdate
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	campaign, err := r.email.UpdateCampaign(ctx.UserContext(), entity.EmailCampaign{
		ID:         ctx.Params("id"),
		Name:       body.Name,
		TemplateID: body.TemplateID,
		Audience:   toEmailAudience(body.Audience),
		Metadata:   body.Metadata,
		UpdatedBy:  nullableString(emailActorID(ctx)),
	})
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(campaign)
}

// @Summary  Preview email campaign audience
// @ID       admin-email-preview-campaign-audience
// @Tags     admin-emails
// @Produce  json
// @Param    id path string true "Campaign ID"
// @Success  200 {object} response.EmailAudienceRecipientList
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns/{id}/preview-audience [post]
func (r *V1) adminEmailPreviewAudience(ctx *fiber.Ctx) error {
	campaign, err := r.email.Campaign(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return adminEmailError(ctx, err)
	}
	recipients, total, err := r.email.PreviewAudience(ctx.UserContext(), campaign.Audience)
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.EmailAudienceRecipientList{Items: recipients, Total: total})
}

// @Summary  Test send email campaign
// @ID       admin-email-test-send-campaign
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    id      path string                         true "Campaign ID"
// @Param    request body request.EmailCampaignTestSend true "Recipient and variables"
// @Success  202 {object} entity.EmailMessageLog
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  503 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns/{id}/test-send [post]
func (r *V1) adminEmailTestSendCampaign(ctx *fiber.Ctx) error {
	var body request.EmailCampaignTestSend
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	message, err := r.email.TestSendCampaign(
		ctx.UserContext(),
		ctx.Params("id"),
		body.To,
		body.Lang,
		body.Variables,
	)
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusAccepted).JSON(message)
}

// @Summary  Schedule email campaign
// @ID       admin-email-schedule-campaign
// @Tags     admin-emails
// @Accept   json
// @Produce  json
// @Param    id      path string                        true "Campaign ID"
// @Param    request body request.EmailCampaignSchedule true "Schedule time"
// @Success  200 {object} entity.EmailCampaign
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns/{id}/schedule [post]
func (r *V1) adminEmailScheduleCampaign(ctx *fiber.Ctx) error {
	var body request.EmailCampaignSchedule
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	campaign, err := r.email.ScheduleCampaign(
		ctx.UserContext(),
		ctx.Params("id"),
		emailActorID(ctx),
		body.ScheduledAt,
	)
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(campaign)
}

// @Summary  Send email campaign now
// @ID       admin-email-send-campaign-now
// @Tags     admin-emails
// @Produce  json
// @Param    id path string true "Campaign ID"
// @Success  202 {object} entity.EmailCampaign
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns/{id}/send-now [post]
func (r *V1) adminEmailSendCampaignNow(ctx *fiber.Ctx) error {
	campaign, err := r.email.SendCampaignNow(ctx.UserContext(), ctx.Params("id"), emailActorID(ctx))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusAccepted).JSON(campaign)
}

// @Summary  Retry failed email campaign recipients
// @ID       admin-email-retry-failed-campaign
// @Tags     admin-emails
// @Produce  json
// @Param    id path string true "Campaign ID"
// @Success  202 {object} entity.EmailCampaign
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns/{id}/retry-failed [post]
func (r *V1) adminEmailRetryFailedCampaign(ctx *fiber.Ctx) error {
	campaign, err := r.email.RetryFailedCampaign(ctx.UserContext(), ctx.Params("id"), emailActorID(ctx))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusAccepted).JSON(campaign)
}

// @Summary  Cancel email campaign
// @ID       admin-email-cancel-campaign
// @Tags     admin-emails
// @Produce  json
// @Param    id path string true "Campaign ID"
// @Success  200 {object} entity.EmailCampaign
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  403 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /admin/emails/campaigns/{id}/cancel [post]
func (r *V1) adminEmailCancelCampaign(ctx *fiber.Ctx) error {
	campaign, err := r.email.CancelCampaign(ctx.UserContext(), ctx.Params("id"), emailActorID(ctx))
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(campaign)
}

// @Summary  Get user email preferences
// @ID       user-email-preferences
// @Tags     user
// @Produce  json
// @Success  200 {object} entity.EmailSubscription
// @Failure  401 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /user/email-preferences [get]
func (r *V1) emailPreferences(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}
	subscription, err := r.email.Subscription(ctx.UserContext(), userID)
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(subscription)
}

// @Summary  Update user email preferences
// @ID       update-user-email-preferences
// @Tags     user
// @Accept   json
// @Produce  json
// @Param    request body request.EmailSubscriptionUpdate true "Marketing opt-in"
// @Success  200 {object} entity.EmailSubscription
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  500 {object} response.Error
// @Security BearerAuth
// @Router   /user/email-preferences [patch]
func (r *V1) updateEmailPreferences(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}
	var body request.EmailSubscriptionUpdate
	if err := parseAndValidate(ctx, r, &body); err != nil {
		return err
	}
	subscription, err := r.email.UpdateSubscription(
		ctx.UserContext(),
		userID,
		body.MarketingOptIn,
		"user_preferences",
	)
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(subscription)
}

// @Summary  Ingest Cloudflare email bounce webhook
// @ID       email-cloudflare-bounce-webhook
// @Tags     email
// @Accept   json
// @Produce  json
// @Param    cf-webhook-auth header string true "Cloudflare webhook secret"
// @Success  202 {object} entity.EmailWebhookIngestResult
// @Failure  400 {object} response.Error
// @Failure  401 {object} response.Error
// @Failure  404 {object} response.Error
// @Failure  500 {object} response.Error
// @Router   /email/webhooks/cloudflare/bounces [post]
func (r *V1) emailCloudflareBounceWebhook(ctx *fiber.Ctx) error {
	secret := strings.TrimSpace(r.emailWebhookSecret)
	if secret == "" {
		return errorResponse(ctx, http.StatusNotFound, "not found")
	}
	if !constantTimeStringEqual(ctx.Get("cf-webhook-auth"), secret) {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}
	result, err := r.email.IngestCloudflareBounceWebhook(ctx.UserContext(), ctx.Body())
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusAccepted).JSON(result)
}

// @Summary Unsubscribe from marketing email
// @ID      email-unsubscribe
// @Tags    email
// @Accept  json
// @Produce json
// @Param   token   query string                   false "Unsubscribe token"
// @Param   request body  request.EmailUnsubscribe false "Unsubscribe token"
// @Success 200 {object} entity.EmailSubscription
// @Failure 400 {object} response.Error
// @Failure 500 {object} response.Error
// @Router  /email/unsubscribe [get]
// @Router  /email/unsubscribe [post]
func (r *V1) emailUnsubscribe(ctx *fiber.Ctx) error {
	token := strings.TrimSpace(ctx.Query("token"))
	if token == "" && len(ctx.Body()) > 0 {
		var body request.EmailUnsubscribe
		if err := ctx.BodyParser(&body); err != nil {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		token = body.Token
	}
	if token == "" {
		return errorResponse(ctx, http.StatusBadRequest, "invalid unsubscribe token")
	}
	subscription, err := r.email.Unsubscribe(ctx.UserContext(), token)
	if err != nil {
		return adminEmailError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(subscription)
}

func parseAndValidate(ctx *fiber.Ctx, r *V1, body any) error {
	if err := ctx.BodyParser(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}
	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	return nil
}

func adminEmailError(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrEmailTemplateNotFound),
		errors.Is(err, entity.ErrEmailTemplateVersionNotFound),
		errors.Is(err, entity.ErrEmailEventSettingNotFound),
		errors.Is(err, entity.ErrEmailCampaignNotFound),
		errors.Is(err, entity.ErrEmailMessageNotFound),
		errors.Is(err, entity.ErrEmailSuppressionNotFound):
		return errorResponse(ctx, http.StatusNotFound, "not found")
	case errors.Is(err, entity.ErrEmailMessageNotResendable),
		errors.Is(err, entity.ErrEmailRecipientSuppressed):
		return errorResponse(ctx, http.StatusConflict, err.Error())
	case errors.Is(err, entity.ErrInvalidEmailTemplate):
		// Template errors carry dynamic parser detail ("missing subject",
		// template line numbers). Keep the message — and thus the machine
		// code — FIXED (F1-D) and surface the specifics via details.
		return errorResponseWithDetails(ctx, http.StatusBadRequest, entity.ErrInvalidEmailTemplate.Error(), err.Error())
	case errors.Is(err, entity.ErrInvalidEmailCampaign),
		errors.Is(err, entity.ErrInvalidAuthInput),
		errors.Is(err, entity.ErrUnsupportedLanguage),
		errors.Is(err, entity.ErrInvalidUnsubscribeToken):
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	case errors.Is(err, entity.ErrEmailDeliveryFailed):
		return errorResponse(ctx, http.StatusServiceUnavailable, "email delivery failed")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}

func emailActorID(ctx *fiber.Ctx) string {
	userID, _ := ctx.Locals("userID").(string)

	return userID
}

func constantTimeStringEqual(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if len(left) != len(right) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func toEmailAudience(audience request.EmailAudience) entity.EmailAudienceFilter {
	return entity.EmailAudienceFilter{
		Role:        audience.Role,
		Lang:        audience.Lang,
		CreatedFrom: audience.CreatedFrom,
		CreatedTo:   audience.CreatedTo,
		Limit:       audience.Limit,
	}
}

func nullableString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	return &value
}
