package persistent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	emailTemplateColumns = `
id, key, name, category, critical, enabled, archived_at, deleted_at, created_at, updated_at`
	emailTemplateVersionColumns = `
id, template_id, lang, version, subject_template, preview_template, title_template, body_template,
button_label_template, button_url_template, note_template, footer_template, text_template,
required_variables, published, created_by, published_by, published_at, created_at, updated_at`
	emailTemplateVersionJoinColumns = `
v.id, v.template_id, v.lang, v.version, v.subject_template, v.preview_template, v.title_template, v.body_template,
v.button_label_template, v.button_url_template, v.note_template, v.footer_template, v.text_template,
v.required_variables, v.published, v.created_by, v.published_by, v.published_at, v.created_at, v.updated_at`
	emailMessageColumns = `
id, category, COALESCE(template_key, ''), COALESCE(template_version_id::text, ''),
COALESCE(campaign_id::text, ''), COALESCE(campaign_recipient_id::text, ''),
COALESCE(user_id::text, ''), recipient_email, lang, subject, status, attempts,
COALESCE(provider_response, ''), COALESCE(error, ''), metadata, html, text, critical, headers,
scheduled_at, sent_at, created_at, updated_at`
	emailMessageColumnsQualified = `
m.id, m.category, COALESCE(m.template_key, ''), COALESCE(m.template_version_id::text, ''),
COALESCE(m.campaign_id::text, ''), COALESCE(m.campaign_recipient_id::text, ''),
COALESCE(m.user_id::text, ''), m.recipient_email, m.lang, m.subject, m.status, m.attempts,
COALESCE(m.provider_response, ''), COALESCE(m.error, ''), m.metadata, m.html, m.text, m.critical, m.headers,
m.scheduled_at, m.sent_at, m.created_at, m.updated_at`
	emailCampaignColumns = `
id, name, template_id, status, audience, metadata, scheduled_at, sent_at, cancelled_at,
created_by, updated_by, created_at, updated_at`
	emailCampaignRecipientColumns = `
id, campaign_id, user_id, email, lang, COALESCE(unsubscribe_url, ''), status,
COALESCE(message_id::text, ''), COALESCE(error, ''), sent_at, created_at, updated_at`
	emailDeliveryEventColumns = `
id, dedupe_key, provider, event_type, recipient_email, COALESCE(message_id::text, ''),
COALESCE(campaign_id::text, ''), COALESCE(campaign_recipient_id::text, ''),
COALESCE(reason, ''), COALESCE(diagnostic, ''), raw_payload, occurred_at, created_at`
	emailProviderPollCursorColumns = `
provider, cursor_key, last_polled_at, created_at, updated_at`
)

// EmailRepo stores email templates, logs, consent, suppressions, and campaigns.
type EmailRepo struct {
	*postgres.Postgres
}

// NewEmailRepo creates an email repository.
func NewEmailRepo(pg *postgres.Postgres) *EmailRepo {
	return &EmailRepo{pg}
}

func (r *EmailRepo) CreateEmailTemplate(
	ctx context.Context,
	template entity.EmailTemplate,
) (entity.EmailTemplate, error) {
	sqlText, args, err := r.Builder.
		Insert("email_templates").
		Columns("id", "key", "name", "category", "critical", "enabled", "created_at", "updated_at").
		Values(
			template.ID,
			template.Key,
			template.Name,
			template.Category,
			template.Critical,
			template.Enabled,
			template.CreatedAt,
			template.UpdatedAt,
		).
		Suffix("RETURNING " + emailTemplateColumns).
		ToSql()
	if err != nil {
		return entity.EmailTemplate{}, fmt.Errorf("EmailRepo - CreateEmailTemplate - Builder: %w", err)
	}

	created, err := scanEmailTemplate(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if isUniqueViolation(err) {
			return entity.EmailTemplate{}, entity.ErrInvalidEmailTemplate
		}

		return entity.EmailTemplate{}, err
	}

	return created, nil
}

func (r *EmailRepo) ListEmailTemplates(
	ctx context.Context,
	filter repo.EmailTemplateFilter,
) ([]entity.EmailTemplate, int, error) {
	limit := filter.Limit
	if limit == 0 || limit > 100 {
		limit = 50
	}

	query := r.Builder.
		Select(emailTemplateColumns, "count(*) OVER()").
		From("email_templates").
		Where("deleted_at IS NULL").
		OrderBy("category ASC", "key ASC").
		Limit(limit).
		Offset(filter.Offset)
	if strings.TrimSpace(filter.Category) != "" {
		query = query.Where(sq.Eq{"category": strings.TrimSpace(filter.Category)})
	}
	if !filter.IncludeArchived {
		query = query.Where("archived_at IS NULL")
	}
	if strings.TrimSpace(filter.Query) != "" {
		query = query.Where(
			"(key ILIKE ? OR name ILIKE ?)",
			"%"+escapeLike(strings.TrimSpace(filter.Query))+"%",
			"%"+escapeLike(strings.TrimSpace(filter.Query))+"%",
		)
	}

	sqlText, args, err := query.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailTemplates - Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailTemplates - Query: %w", err)
	}
	defer rows.Close()

	templates := make([]entity.EmailTemplate, 0)
	total := 0
	for rows.Next() {
		template, rowTotal, err := scanEmailTemplateWithTotal(rows)
		if err != nil {
			return nil, 0, err
		}
		total = rowTotal
		templates = append(templates, template)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailTemplates - rows: %w", err)
	}

	return templates, total, nil
}

func (r *EmailRepo) GetEmailTemplateByID(ctx context.Context, id string) (entity.EmailTemplate, error) {
	return r.getEmailTemplate(ctx, sq.Eq{"id": id})
}

func (r *EmailRepo) GetEmailTemplateByKey(ctx context.Context, key string) (entity.EmailTemplate, error) {
	return r.getEmailTemplate(ctx, sq.Eq{"key": key})
}

func (r *EmailRepo) UpdateEmailTemplate(
	ctx context.Context,
	id string,
	patch entity.EmailTemplatePatch,
) (entity.EmailTemplate, error) {
	query := r.Builder.Update("email_templates").Set("updated_at", sq.Expr("now()"))
	if patch.Name != nil {
		query = query.Set("name", strings.TrimSpace(*patch.Name))
	}
	if patch.Enabled != nil {
		query = query.Set("enabled", *patch.Enabled)
	}
	if patch.Archived != nil {
		if *patch.Archived {
			query = query.Set("archived_at", sq.Expr("COALESCE(archived_at, now())"))
		} else {
			query = query.Set("archived_at", nil)
		}
	}

	sqlText, args, err := query.
		Where(sq.Eq{"id": id}).
		Where("deleted_at IS NULL").
		Suffix("RETURNING " + emailTemplateColumns).
		ToSql()
	if err != nil {
		return entity.EmailTemplate{}, fmt.Errorf("EmailRepo - UpdateEmailTemplate - Builder: %w", err)
	}

	template, err := scanEmailTemplate(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		return entity.EmailTemplate{}, err
	}

	return template, nil
}

func (r *EmailRepo) DeleteEmailTemplate(ctx context.Context, id string) error {
	sqlText, args, err := r.Builder.
		Update("email_templates").
		Set("deleted_at", sq.Expr("now()")).
		Set("enabled", false).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": id}).
		Where("deleted_at IS NULL").
		ToSql()
	if err != nil {
		return fmt.Errorf("EmailRepo - DeleteEmailTemplate - Builder: %w", err)
	}

	tag, err := r.Pool.Exec(ctx, sqlText, args...)
	if err != nil {
		return fmt.Errorf("EmailRepo - DeleteEmailTemplate - Exec: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return entity.ErrEmailTemplateNotFound
	}

	return nil
}

func (r *EmailRepo) CreateEmailTemplateVersion(
	ctx context.Context,
	version entity.EmailTemplateVersion,
) (entity.EmailTemplateVersion, error) {
	nextVersion, err := r.nextTemplateVersion(ctx, version.TemplateID, version.Lang)
	if err != nil {
		return entity.EmailTemplateVersion{}, err
	}
	version.Version = nextVersion

	sqlText, args, err := r.Builder.
		Insert("email_template_versions").
		Columns(
			"id",
			"template_id",
			"lang",
			"version",
			"subject_template",
			"preview_template",
			"title_template",
			"body_template",
			"button_label_template",
			"button_url_template",
			"note_template",
			"footer_template",
			"text_template",
			"required_variables",
			"published",
			"created_by",
			"created_at",
			"updated_at",
		).
		Values(
			version.ID,
			version.TemplateID,
			version.Lang,
			version.Version,
			version.SubjectTemplate,
			version.PreviewTemplate,
			version.TitleTemplate,
			version.BodyTemplate,
			version.ButtonLabelTemplate,
			version.ButtonURLTemplate,
			version.NoteTemplate,
			version.FooterTemplate,
			version.TextTemplate,
			version.RequiredVariables,
			false,
			nullableStringPtrArg(version.CreatedBy),
			version.CreatedAt,
			version.UpdatedAt,
		).
		Suffix("RETURNING " + emailTemplateVersionColumns).
		ToSql()
	if err != nil {
		return entity.EmailTemplateVersion{}, fmt.Errorf("EmailRepo - CreateEmailTemplateVersion - Builder: %w", err)
	}

	created, err := scanEmailTemplateVersion(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		return entity.EmailTemplateVersion{}, err
	}

	return created, nil
}

func (r *EmailRepo) ListEmailTemplateVersions(
	ctx context.Context,
	templateID string,
) ([]entity.EmailTemplateVersion, error) {
	sqlText, args, err := r.Builder.
		Select(emailTemplateVersionColumns).
		From("email_template_versions").
		Where(sq.Eq{"template_id": templateID}).
		OrderBy("lang ASC", "version DESC").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - ListEmailTemplateVersions - Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - ListEmailTemplateVersions - Query: %w", err)
	}
	defer rows.Close()

	versions := make([]entity.EmailTemplateVersion, 0)
	for rows.Next() {
		version, err := scanEmailTemplateVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("EmailRepo - ListEmailTemplateVersions - rows: %w", err)
	}

	return versions, nil
}

func (r *EmailRepo) GetEmailTemplateVersionByID(
	ctx context.Context,
	id string,
) (entity.EmailTemplateVersion, error) {
	sqlText, args, err := r.Builder.
		Select(emailTemplateVersionColumns).
		From("email_template_versions").
		Where(sq.Eq{"id": id}).
		Limit(1).
		ToSql()
	if err != nil {
		return entity.EmailTemplateVersion{}, fmt.Errorf("EmailRepo - GetEmailTemplateVersionByID - Builder: %w", err)
	}

	return scanEmailTemplateVersion(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) GetLatestEmailTemplateVersion(
	ctx context.Context,
	templateID,
	lang string,
) (entity.EmailTemplateVersion, error) {
	sqlText, args, err := r.Builder.
		Select(emailTemplateVersionColumns).
		From("email_template_versions").
		Where(sq.Eq{"template_id": templateID, "lang": lang}).
		OrderBy("version DESC").
		Limit(1).
		ToSql()
	if err != nil {
		return entity.EmailTemplateVersion{}, fmt.Errorf("EmailRepo - GetLatestEmailTemplateVersion - Builder: %w", err)
	}

	return scanEmailTemplateVersion(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) GetPublishedEmailTemplateVersion(
	ctx context.Context,
	templateKey,
	lang string,
) (entity.EmailTemplateVersion, entity.EmailTemplate, error) {
	const query = `
SELECT ` + emailTemplateVersionJoinColumns + `,
       t.id, t.key, t.name, t.category, t.critical, t.enabled, t.archived_at, t.deleted_at, t.created_at, t.updated_at
FROM email_templates t
JOIN email_template_versions v ON v.template_id = t.id
WHERE t.key = $1
    AND t.deleted_at IS NULL
    AND t.archived_at IS NULL
    AND t.enabled = true
    AND v.lang = $2
    AND v.published = true
LIMIT 1`

	row := r.Pool.QueryRow(ctx, query, templateKey, lang)
	version, template, err := scanEmailTemplateVersionAndTemplate(row)
	if err != nil {
		return entity.EmailTemplateVersion{}, entity.EmailTemplate{}, err
	}

	return version, template, nil
}

func (r *EmailRepo) UpdateEmailTemplateVersion(
	ctx context.Context,
	id string,
	patch entity.EmailTemplateVersionPatch,
) (entity.EmailTemplateVersion, error) {
	query := r.Builder.Update("email_template_versions").Set("updated_at", sq.Expr("now()"))
	if patch.SubjectTemplate != nil {
		query = query.Set("subject_template", *patch.SubjectTemplate)
	}
	if patch.PreviewTemplate != nil {
		query = query.Set("preview_template", *patch.PreviewTemplate)
	}
	if patch.TitleTemplate != nil {
		query = query.Set("title_template", *patch.TitleTemplate)
	}
	if patch.BodyTemplate != nil {
		query = query.Set("body_template", *patch.BodyTemplate)
	}
	if patch.ButtonLabelTemplate != nil {
		query = query.Set("button_label_template", *patch.ButtonLabelTemplate)
	}
	if patch.ButtonURLTemplate != nil {
		query = query.Set("button_url_template", *patch.ButtonURLTemplate)
	}
	if patch.NoteTemplate != nil {
		query = query.Set("note_template", *patch.NoteTemplate)
	}
	if patch.FooterTemplate != nil {
		query = query.Set("footer_template", *patch.FooterTemplate)
	}
	if patch.TextTemplate != nil {
		query = query.Set("text_template", *patch.TextTemplate)
	}
	if patch.RequiredVariables != nil {
		query = query.Set("required_variables", *patch.RequiredVariables)
	}

	sqlText, args, err := query.
		Where(sq.Eq{"id": id}).
		Where("published = false").
		Suffix("RETURNING " + emailTemplateVersionColumns).
		ToSql()
	if err != nil {
		return entity.EmailTemplateVersion{}, fmt.Errorf("EmailRepo - UpdateEmailTemplateVersion - Builder: %w", err)
	}

	return scanEmailTemplateVersion(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) PublishEmailTemplateVersion(
	ctx context.Context,
	id,
	actorID string,
) (entity.EmailTemplateVersion, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.EmailTemplateVersion{}, fmt.Errorf("EmailRepo - PublishEmailTemplateVersion - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		templateID string
		lang       string
	)
	err = tx.QueryRow(ctx, "SELECT template_id, lang FROM email_template_versions WHERE id = $1 FOR UPDATE", id).
		Scan(&templateID, &lang)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailTemplateVersion{}, entity.ErrEmailTemplateVersionNotFound
		}

		return entity.EmailTemplateVersion{}, fmt.Errorf("EmailRepo - PublishEmailTemplateVersion - lock: %w", err)
	}

	_, err = tx.Exec(
		ctx,
		"UPDATE email_template_versions SET published = false, updated_at = now() WHERE template_id = $1 AND lang = $2",
		templateID,
		lang,
	)
	if err != nil {
		return entity.EmailTemplateVersion{}, fmt.Errorf("EmailRepo - PublishEmailTemplateVersion - unpublish: %w", err)
	}

	row := tx.QueryRow(
		ctx,
		`UPDATE email_template_versions
SET published = true,
    published_by = $2,
    published_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING `+emailTemplateVersionColumns,
		id,
		nullableStringArg(actorID),
	)
	version, err := scanEmailTemplateVersion(row)
	if err != nil {
		return entity.EmailTemplateVersion{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.EmailTemplateVersion{}, fmt.Errorf("EmailRepo - PublishEmailTemplateVersion - Commit: %w", err)
	}

	return version, nil
}

func (r *EmailRepo) GetEmailEventSetting(ctx context.Context, key string) (entity.EmailEventSetting, error) {
	sqlText, args, err := r.Builder.
		Select("key", "template_id", "enabled", "critical", "cooldown_seconds", "created_at", "updated_at").
		From("email_event_settings").
		Where(sq.Eq{"key": key}).
		Limit(1).
		ToSql()
	if err != nil {
		return entity.EmailEventSetting{}, fmt.Errorf("EmailRepo - GetEmailEventSetting - Builder: %w", err)
	}

	return scanEmailEventSetting(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) UpdateEmailEventSetting(
	ctx context.Context,
	key string,
	patch entity.EmailEventSettingPatch,
) (entity.EmailEventSetting, error) {
	query := r.Builder.Update("email_event_settings").Set("updated_at", sq.Expr("now()"))
	if patch.Enabled != nil {
		query = query.Set("enabled", *patch.Enabled)
	}
	if patch.CooldownSeconds != nil {
		query = query.Set("cooldown_seconds", *patch.CooldownSeconds)
	}

	sqlText, args, err := query.
		Where(sq.Eq{"key": key}).
		Suffix("RETURNING key, template_id, enabled, critical, cooldown_seconds, created_at, updated_at").
		ToSql()
	if err != nil {
		return entity.EmailEventSetting{}, fmt.Errorf("EmailRepo - UpdateEmailEventSetting - Builder: %w", err)
	}

	return scanEmailEventSetting(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) CreateEmailMessage(
	ctx context.Context,
	message entity.EmailMessageLog,
) (entity.EmailMessageLog, error) {
	metadataJSON, err := marshalStringMap(message.Metadata)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}
	headersJSON, err := marshalStringMap(message.Headers)
	if err != nil {
		return entity.EmailMessageLog{}, err
	}

	sqlText, args, err := r.Builder.
		Insert("email_messages").
		Columns(
			"id",
			"category",
			"template_key",
			"template_version_id",
			"campaign_id",
			"campaign_recipient_id",
			"user_id",
			"recipient_email",
			"lang",
			"subject",
			"status",
			"attempts",
			"provider_response",
			"error",
			"metadata",
			"html",
			"text",
			"critical",
			"headers",
			"scheduled_at",
			"sent_at",
			"created_at",
			"updated_at",
		).
		Values(
			message.ID,
			message.Category,
			nullableStringArg(message.TemplateKey),
			nullableStringArg(message.TemplateVersionID),
			nullableStringArg(message.CampaignID),
			nullableStringArg(message.CampaignRecipient),
			nullableStringArg(message.UserID),
			message.RecipientEmail,
			message.Lang,
			message.Subject,
			message.Status,
			message.Attempts,
			nullableStringArg(message.ProviderResponse),
			nullableStringArg(message.Error),
			string(metadataJSON),
			message.HTML,
			message.Text,
			message.Critical,
			string(headersJSON),
			message.ScheduledAt,
			message.SentAt,
			message.CreatedAt,
			message.UpdatedAt,
		).
		Suffix("RETURNING " + emailMessageColumns).
		ToSql()
	if err != nil {
		return entity.EmailMessageLog{}, fmt.Errorf("EmailRepo - CreateEmailMessage - Builder: %w", err)
	}

	return scanEmailMessage(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) UpdateEmailMessageStatus(
	ctx context.Context,
	id string,
	status string,
	attempts int,
	providerResponse string,
	deliveryError string,
	sentAt *time.Time,
) (entity.EmailMessageLog, error) {
	sqlText, args, err := r.Builder.
		Update("email_messages").
		Set("status", status).
		Set("attempts", attempts).
		Set("provider_response", nullableStringArg(providerResponse)).
		Set("error", nullableStringArg(deliveryError)).
		Set("scheduled_at", nil).
		Set("sent_at", sentAt).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": id}).
		Suffix("RETURNING " + emailMessageColumns).
		ToSql()
	if err != nil {
		return entity.EmailMessageLog{}, fmt.Errorf("EmailRepo - UpdateEmailMessageStatus - Builder: %w", err)
	}

	return scanEmailMessage(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) ScheduleEmailMessageRetry(
	ctx context.Context,
	id string,
	attempts int,
	providerResponse string,
	deliveryError string,
	scheduledAt time.Time,
) (entity.EmailMessageLog, error) {
	sqlText, args, err := r.Builder.
		Update("email_messages").
		Set("status", entity.EmailMessageStatusQueued).
		Set("attempts", attempts).
		Set("provider_response", nullableStringArg(providerResponse)).
		Set("error", nullableStringArg(deliveryError)).
		Set("scheduled_at", scheduledAt).
		Set("sent_at", nil).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": id}).
		Suffix("RETURNING " + emailMessageColumns).
		ToSql()
	if err != nil {
		return entity.EmailMessageLog{}, fmt.Errorf("EmailRepo - ScheduleEmailMessageRetry - Builder: %w", err)
	}

	return scanEmailMessage(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) ClaimDueTransactionalEmailMessages(
	ctx context.Context,
	now time.Time,
	limit int,
	visibilityTimeout time.Duration,
) ([]entity.EmailMessageLog, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if visibilityTimeout <= 0 {
		visibilityTimeout = 5 * time.Minute
	}
	limit = emailMessageClaimLimit(limit)

	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - ClaimDueTransactionalEmailMessages - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(
		ctx,
		claimDueTransactionalEmailMessagesSQL(),
		entity.EmailCategoryTransactional,
		entity.EmailMessageStatusQueued,
		now.UTC(),
		limit,
		now.UTC().Add(visibilityTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - ClaimDueTransactionalEmailMessages - Query: %w", err)
	}
	defer rows.Close()

	messages := make([]entity.EmailMessageLog, 0, limit)
	for rows.Next() {
		message, err := scanEmailMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("EmailRepo - ClaimDueTransactionalEmailMessages - rows: %w", err)
	}
	rows.Close()

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("EmailRepo - ClaimDueTransactionalEmailMessages - Commit: %w", err)
	}

	return messages, nil
}

func emailMessageClaimLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}

	return limit
}

func claimDueTransactionalEmailMessagesSQL() string {
	return `
WITH due AS (
    SELECT id
    FROM email_messages
    WHERE category = $1
      AND status = $2
      AND scheduled_at IS NOT NULL
      AND scheduled_at <= $3
    ORDER BY scheduled_at ASC, created_at ASC
    LIMIT $4
    FOR UPDATE SKIP LOCKED
)
UPDATE email_messages AS m
SET scheduled_at = $5,
    updated_at = now()
FROM due
WHERE m.id = due.id
RETURNING ` + emailMessageColumnsQualified
}

func (r *EmailRepo) ListEmailMessages(
	ctx context.Context,
	filter repo.EmailMessageFilter,
) ([]entity.EmailMessageLog, int, error) {
	limit := filter.Limit
	if limit == 0 || limit > 100 {
		limit = 50
	}
	query := r.Builder.
		Select(emailMessageColumns, "count(*) OVER()").
		From("email_messages").
		OrderBy("created_at DESC").
		Limit(limit).
		Offset(filter.Offset)
	if strings.TrimSpace(filter.Category) != "" {
		query = query.Where(sq.Eq{"category": strings.TrimSpace(filter.Category)})
	}
	if strings.TrimSpace(filter.Status) != "" {
		query = query.Where(sq.Eq{"status": strings.TrimSpace(filter.Status)})
	}
	if strings.TrimSpace(filter.Email) != "" {
		query = query.Where("lower(recipient_email) = lower(?)", strings.TrimSpace(filter.Email))
	}

	sqlText, args, err := query.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailMessages - Builder: %w", err)
	}
	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailMessages - Query: %w", err)
	}
	defer rows.Close()

	messages := make([]entity.EmailMessageLog, 0)
	total := 0
	for rows.Next() {
		message, rowTotal, err := scanEmailMessageWithTotal(rows)
		if err != nil {
			return nil, 0, err
		}
		total = rowTotal
		messages = append(messages, message)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailMessages - rows: %w", err)
	}

	return messages, total, nil
}

func (r *EmailRepo) GetEmailSubscription(ctx context.Context, userID string) (entity.EmailSubscription, error) {
	const query = `
SELECT user_id, marketing_opt_in, opted_in_at, opted_out_at, COALESCE(source, ''), created_at, updated_at
FROM email_subscriptions
WHERE user_id = $1`

	return scanEmailSubscription(r.Pool.QueryRow(ctx, query, userID))
}

func (r *EmailRepo) UpsertEmailSubscription(
	ctx context.Context,
	subscription entity.EmailSubscription,
) (entity.EmailSubscription, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.EmailSubscription{}, fmt.Errorf("EmailRepo - UpsertEmailSubscription - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	optedInAt := subscription.OptedInAt
	optedOutAt := subscription.OptedOutAt
	if subscription.MarketingOptIn && optedInAt == nil {
		now := time.Now().UTC()
		optedInAt = &now
	}
	if !subscription.MarketingOptIn && optedOutAt == nil {
		now := time.Now().UTC()
		optedOutAt = &now
	}

	const query = `
INSERT INTO email_subscriptions (
    user_id, marketing_opt_in, opted_in_at, opted_out_at, source, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, now(), now())
ON CONFLICT (user_id) DO UPDATE SET
    marketing_opt_in = EXCLUDED.marketing_opt_in,
    opted_in_at = CASE WHEN EXCLUDED.marketing_opt_in THEN COALESCE(email_subscriptions.opted_in_at, now()) ELSE email_subscriptions.opted_in_at END,
    opted_out_at = CASE WHEN EXCLUDED.marketing_opt_in THEN NULL ELSE now() END,
    source = EXCLUDED.source,
    updated_at = now()
RETURNING user_id, marketing_opt_in, opted_in_at, opted_out_at, COALESCE(source, ''), created_at, updated_at`

	updated, err := scanEmailSubscription(tx.QueryRow(
		ctx,
		query,
		subscription.UserID,
		subscription.MarketingOptIn,
		optedInAt,
		optedOutAt,
		nullableStringArg(subscription.Source),
	))
	if err != nil {
		return entity.EmailSubscription{}, err
	}
	if updated.MarketingOptIn {
		_, err = tx.Exec(
			ctx,
			`DELETE FROM email_suppressions
WHERE scope = $1
  AND reason = 'unsubscribe'
  AND email_normalized = (
      SELECT lower(email)
      FROM users
      WHERE id = $2
        AND deleted_at IS NULL
  )`,
			entity.EmailSuppressionScopeMarketing,
			updated.UserID,
		)
		if err != nil {
			return entity.EmailSubscription{}, fmt.Errorf(
				"EmailRepo - UpsertEmailSubscription - DeleteSuppression: %w",
				err,
			)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return entity.EmailSubscription{}, fmt.Errorf("EmailRepo - UpsertEmailSubscription - Commit: %w", err)
	}

	return updated, nil
}

func (r *EmailRepo) UnsubscribeEmail(
	ctx context.Context,
	userID,
	email,
	source string,
) (entity.EmailSubscription, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.EmailSubscription{}, fmt.Errorf("EmailRepo - UnsubscribeEmail - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(
		ctx,
		`INSERT INTO email_subscriptions (
    user_id, marketing_opt_in, opted_out_at, source, created_at, updated_at
) VALUES ($1, false, now(), $2, now(), now())
ON CONFLICT (user_id) DO UPDATE SET
    marketing_opt_in = false,
    opted_out_at = now(),
    source = EXCLUDED.source,
    updated_at = now()
RETURNING user_id, marketing_opt_in, opted_in_at, opted_out_at, COALESCE(source, ''), created_at, updated_at`,
		userID,
		nullableStringArg(source),
	)
	subscription, err := scanEmailSubscription(row)
	if err != nil {
		return entity.EmailSubscription{}, err
	}

	suppression := entity.EmailSuppression{
		ID:        uuid.New().String(),
		Email:     email,
		Scope:     entity.EmailSuppressionScopeMarketing,
		Reason:    "unsubscribe",
		CreatedAt: time.Now().UTC(),
	}
	if _, err = createEmailSuppressionTx(ctx, tx, suppression); err != nil {
		return entity.EmailSubscription{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.EmailSubscription{}, fmt.Errorf("EmailRepo - UnsubscribeEmail - Commit: %w", err)
	}

	return subscription, nil
}

func (r *EmailRepo) ListEmailSuppressions(
	ctx context.Context,
	filter repo.EmailSuppressionFilter,
) ([]entity.EmailSuppression, int, error) {
	limit := filter.Limit
	if limit == 0 || limit > 100 {
		limit = 50
	}
	query := r.Builder.
		Select("id", "email", "scope", "reason", "created_by", "created_at", "count(*) OVER()").
		From("email_suppressions").
		OrderBy("created_at DESC").
		Limit(limit).
		Offset(filter.Offset)
	if strings.TrimSpace(filter.Email) != "" {
		query = query.Where("email_normalized = lower(?)", strings.TrimSpace(filter.Email))
	}
	if strings.TrimSpace(filter.Scope) != "" {
		query = query.Where(sq.Eq{"scope": strings.TrimSpace(filter.Scope)})
	}

	sqlText, args, err := query.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailSuppressions - Builder: %w", err)
	}
	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailSuppressions - Query: %w", err)
	}
	defer rows.Close()

	suppressions := make([]entity.EmailSuppression, 0)
	total := 0
	for rows.Next() {
		suppression, rowTotal, err := scanEmailSuppressionWithTotal(rows)
		if err != nil {
			return nil, 0, err
		}
		total = rowTotal
		suppressions = append(suppressions, suppression)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailSuppressions - rows: %w", err)
	}

	return suppressions, total, nil
}

func (r *EmailRepo) CreateEmailSuppression(
	ctx context.Context,
	suppression entity.EmailSuppression,
) (entity.EmailSuppression, error) {
	return createEmailSuppressionTx(ctx, r.Pool, suppression)
}

func (r *EmailRepo) UpsertAutomaticEmailSuppression(
	ctx context.Context,
	suppression entity.EmailSuppression,
) (entity.EmailSuppression, error) {
	return upsertAutomaticEmailSuppressionTx(ctx, r.Pool, suppression)
}

func (r *EmailRepo) DeleteEmailSuppression(ctx context.Context, id string) error {
	sqlText, args, err := r.Builder.
		Delete("email_suppressions").
		Where(sq.Eq{"id": id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("EmailRepo - DeleteEmailSuppression - Builder: %w", err)
	}
	tag, err := r.Pool.Exec(ctx, sqlText, args...)
	if err != nil {
		return fmt.Errorf("EmailRepo - DeleteEmailSuppression - Exec: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return entity.ErrEmailSuppressionNotFound
	}

	return nil
}

func (r *EmailRepo) IsEmailSuppressed(ctx context.Context, email, category string) (bool, error) {
	scopes := []string{entity.EmailSuppressionScopeAll}
	if category == entity.EmailCategoryMarketing {
		scopes = append(scopes, entity.EmailSuppressionScopeMarketing)
	}

	sqlText, args, err := r.Builder.
		Select("1").
		From("email_suppressions").
		Where("email_normalized = lower(?)", strings.TrimSpace(email)).
		Where(sq.Eq{"scope": scopes}).
		Limit(1).
		ToSql()
	if err != nil {
		return false, fmt.Errorf("EmailRepo - IsEmailSuppressed - Builder: %w", err)
	}

	var exists int
	err = r.Pool.QueryRow(ctx, sqlText, args...).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}

		return false, fmt.Errorf("EmailRepo - IsEmailSuppressed - QueryRow: %w", err)
	}

	return true, nil
}

func (r *EmailRepo) UpsertEmailDeliveryEvent(
	ctx context.Context,
	event entity.EmailDeliveryEvent,
) (entity.EmailDeliveryEvent, bool, error) {
	rawPayload := event.RawPayload
	if len(rawPayload) == 0 {
		rawPayload = entity.RawJSON(`{}`)
	}
	const query = `
INSERT INTO email_delivery_events (
    id, dedupe_key, provider, event_type, recipient_email, message_id, campaign_id,
    campaign_recipient_id, reason, diagnostic, raw_payload, occurred_at, created_at
) VALUES (
    $1::uuid, $2::varchar, $3::varchar, $4::varchar, lower($5::varchar), $6::uuid, $7::uuid,
    $8::uuid, $9::varchar, $10::text, $11::jsonb, $12::timestamp, $13::timestamp
)
ON CONFLICT (dedupe_key) DO UPDATE SET
    dedupe_key = email_delivery_events.dedupe_key
RETURNING ` + emailDeliveryEventColumns + `, (xmax = 0) AS inserted`

	var inserted bool
	created, err := scanEmailDeliveryEventWithInserted(r.Pool.QueryRow(
		ctx,
		query,
		event.ID,
		event.DedupeKey,
		event.Provider,
		event.EventType,
		event.RecipientEmail,
		nullableStringArg(event.MessageID),
		nullableStringArg(event.CampaignID),
		nullableStringArg(event.CampaignRecipient),
		nullableStringArg(event.Reason),
		nullableStringArg(event.Diagnostic),
		[]byte(rawPayload),
		event.OccurredAt,
		event.CreatedAt,
	))
	if err != nil {
		return entity.EmailDeliveryEvent{}, false, err
	}
	inserted = created.inserted

	return created.event, inserted, nil
}

func (r *EmailRepo) ListEmailDeliveryEvents(
	ctx context.Context,
	filter repo.EmailDeliveryEventFilter,
) ([]entity.EmailDeliveryEvent, int, error) {
	query := emailDeliveryEventListSelect(r.Builder, filter)

	sqlText, args, err := query.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailDeliveryEvents - Builder: %w", err)
	}
	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailDeliveryEvents - Query: %w", err)
	}
	defer rows.Close()

	events := make([]entity.EmailDeliveryEvent, 0)
	total := 0
	for rows.Next() {
		event, rowTotal, err := scanEmailDeliveryEventWithTotal(rows)
		if err != nil {
			return nil, 0, err
		}
		total = rowTotal
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailDeliveryEvents - rows: %w", err)
	}

	return events, total, nil
}

func emailDeliveryEventListSelect(
	builder sq.StatementBuilderType,
	filter repo.EmailDeliveryEventFilter,
) sq.SelectBuilder {
	limit := filter.Limit
	if limit == 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	query := builder.
		Select(emailDeliveryEventColumns, "count(*) OVER()").
		From("email_delivery_events").
		OrderBy("created_at DESC").
		Limit(limit).
		Offset(filter.Offset)
	if strings.TrimSpace(filter.Provider) != "" {
		query = query.Where(sq.Eq{"provider": strings.TrimSpace(filter.Provider)})
	}
	if strings.TrimSpace(filter.EventType) != "" {
		query = query.Where(sq.Eq{"event_type": strings.TrimSpace(filter.EventType)})
	}
	if strings.TrimSpace(filter.Email) != "" {
		query = query.Where("lower(recipient_email) = lower(?)", strings.TrimSpace(filter.Email))
	}
	if strings.TrimSpace(filter.MessageID) != "" {
		query = query.Where(sq.Eq{"message_id": strings.TrimSpace(filter.MessageID)})
	}
	if strings.TrimSpace(filter.CampaignID) != "" {
		query = query.Where(sq.Eq{"campaign_id": strings.TrimSpace(filter.CampaignID)})
	}
	if strings.TrimSpace(filter.CampaignRecipientID) != "" {
		query = query.Where(sq.Eq{"campaign_recipient_id": strings.TrimSpace(filter.CampaignRecipientID)})
	}

	return query
}

func (r *EmailRepo) GetEmailCampaignDeliveryEventSummary(
	ctx context.Context,
	campaignID string,
) (entity.EmailCampaignDeliveryEventSummary, error) {
	summary := entity.EmailCampaignDeliveryEventSummary{CampaignID: strings.TrimSpace(campaignID)}
	const query = `
SELECT
    count(*)::int,
    count(*) FILTER (WHERE event_type = $2)::int,
    count(*) FILTER (WHERE event_type = $3)::int,
    count(DISTINCT lower(recipient_email))::int,
    max(occurred_at)
FROM email_delivery_events
WHERE campaign_id = $1::uuid`

	var lastOccurredAt sql.NullTime
	err := r.Pool.QueryRow(
		ctx,
		query,
		summary.CampaignID,
		entity.EmailDeliveryEventBounceHard,
		entity.EmailDeliveryEventComplaint,
	).Scan(
		&summary.Total,
		&summary.BounceHard,
		&summary.Complaint,
		&summary.UniqueRecipients,
		&lastOccurredAt,
	)
	if err != nil {
		return entity.EmailCampaignDeliveryEventSummary{}, fmt.Errorf(
			"EmailRepo - GetEmailCampaignDeliveryEventSummary - QueryRow: %w",
			err,
		)
	}
	summary.LastOccurredAt = nullableTime(lastOccurredAt)

	return summary, nil
}

func (r *EmailRepo) GetEmailProviderPollCursor(
	ctx context.Context,
	provider string,
	cursorKey string,
) (entity.EmailProviderPollCursor, error) {
	return scanEmailProviderPollCursor(r.Pool.QueryRow(
		ctx,
		getEmailProviderPollCursorSQL(),
		strings.TrimSpace(provider),
		strings.TrimSpace(cursorKey),
	))
}

func (r *EmailRepo) UpsertEmailProviderPollCursor(
	ctx context.Context,
	cursor entity.EmailProviderPollCursor,
) (entity.EmailProviderPollCursor, error) {
	now := time.Now().UTC()
	if cursor.CreatedAt.IsZero() {
		cursor.CreatedAt = now
	}
	if cursor.UpdatedAt.IsZero() {
		cursor.UpdatedAt = now
	}

	return scanEmailProviderPollCursor(r.Pool.QueryRow(
		ctx,
		upsertEmailProviderPollCursorSQL(),
		strings.TrimSpace(cursor.Provider),
		strings.TrimSpace(cursor.CursorKey),
		cursor.LastPolledAt,
		cursor.CreatedAt,
		cursor.UpdatedAt,
	))
}

func getEmailProviderPollCursorSQL() string {
	return `
SELECT ` + emailProviderPollCursorColumns + `
FROM email_provider_poll_cursors
WHERE provider = $1 AND cursor_key = $2`
}

func upsertEmailProviderPollCursorSQL() string {
	return `
INSERT INTO email_provider_poll_cursors (
    provider, cursor_key, last_polled_at, created_at, updated_at
) VALUES (
    $1::varchar, $2::varchar, $3::timestamp, $4::timestamp, $5::timestamp
)
ON CONFLICT (provider, cursor_key) DO UPDATE SET
    last_polled_at = EXCLUDED.last_polled_at,
    updated_at = EXCLUDED.updated_at
RETURNING ` + emailProviderPollCursorColumns
}

func (r *EmailRepo) CreateEmailCampaign(
	ctx context.Context,
	campaign entity.EmailCampaign,
) (entity.EmailCampaign, error) {
	audienceJSON, metadataJSON, err := marshalCampaignJSON(campaign)
	if err != nil {
		return entity.EmailCampaign{}, err
	}

	sqlText, args, err := r.Builder.
		Insert("email_campaigns").
		Columns(
			"id",
			"name",
			"template_id",
			"status",
			"audience",
			"metadata",
			"scheduled_at",
			"sent_at",
			"cancelled_at",
			"created_by",
			"updated_by",
			"created_at",
			"updated_at",
		).
		Values(
			campaign.ID,
			campaign.Name,
			campaign.TemplateID,
			campaign.Status,
			string(audienceJSON),
			string(metadataJSON),
			campaign.ScheduledAt,
			campaign.SentAt,
			campaign.CancelledAt,
			nullableStringPtrArg(campaign.CreatedBy),
			nullableStringPtrArg(campaign.UpdatedBy),
			campaign.CreatedAt,
			campaign.UpdatedAt,
		).
		Suffix("RETURNING " + emailCampaignColumns).
		ToSql()
	if err != nil {
		return entity.EmailCampaign{}, fmt.Errorf("EmailRepo - CreateEmailCampaign - Builder: %w", err)
	}

	return scanEmailCampaign(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) ListEmailCampaigns(
	ctx context.Context,
	filter repo.EmailCampaignFilter,
) ([]entity.EmailCampaign, int, error) {
	limit := filter.Limit
	if limit == 0 || limit > 100 {
		limit = 50
	}
	query := r.Builder.
		Select(emailCampaignColumns, "count(*) OVER()").
		From("email_campaigns").
		OrderBy("created_at DESC").
		Limit(limit).
		Offset(filter.Offset)
	if strings.TrimSpace(filter.Status) != "" {
		query = query.Where(sq.Eq{"status": strings.TrimSpace(filter.Status)})
	}
	sqlText, args, err := query.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailCampaigns - Builder: %w", err)
	}
	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailCampaigns - Query: %w", err)
	}
	defer rows.Close()

	campaigns := make([]entity.EmailCampaign, 0)
	total := 0
	for rows.Next() {
		campaign, rowTotal, err := scanEmailCampaignWithTotal(rows)
		if err != nil {
			return nil, 0, err
		}
		total = rowTotal
		campaigns = append(campaigns, campaign)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListEmailCampaigns - rows: %w", err)
	}

	return campaigns, total, nil
}

func (r *EmailRepo) GetEmailCampaign(ctx context.Context, id string) (entity.EmailCampaign, error) {
	sqlText, args, err := r.Builder.
		Select(emailCampaignColumns).
		From("email_campaigns").
		Where(sq.Eq{"id": id}).
		Limit(1).
		ToSql()
	if err != nil {
		return entity.EmailCampaign{}, fmt.Errorf("EmailRepo - GetEmailCampaign - Builder: %w", err)
	}

	return scanEmailCampaign(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) ClaimEmailCampaignForSending(
	ctx context.Context,
	id,
	actorID string,
) (entity.EmailCampaign, error) {
	sqlText, args, err := r.Builder.
		Update("email_campaigns").
		Set("status", entity.EmailCampaignStatusSending).
		Set("updated_by", nullableStringArg(actorID)).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": id}).
		Where(sq.Eq{"status": []string{
			entity.EmailCampaignStatusDraft,
			entity.EmailCampaignStatusScheduled,
		}}).
		Suffix("RETURNING " + emailCampaignColumns).
		ToSql()
	if err != nil {
		return entity.EmailCampaign{}, fmt.Errorf("EmailRepo - ClaimEmailCampaignForSending - Builder: %w", err)
	}

	campaign, err := scanEmailCampaign(r.Pool.QueryRow(ctx, sqlText, args...))
	if errors.Is(err, entity.ErrEmailCampaignNotFound) {
		return entity.EmailCampaign{}, entity.ErrInvalidEmailCampaign
	}

	return campaign, err
}

func (r *EmailRepo) ClaimEmailCampaignForRetry(
	ctx context.Context,
	id,
	actorID string,
) (entity.EmailCampaign, error) {
	sqlText, args, err := r.Builder.
		Update("email_campaigns").
		Set("status", entity.EmailCampaignStatusSending).
		Set("updated_by", nullableStringArg(actorID)).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": id}).
		Where(sq.Eq{"status": entity.EmailCampaignStatusSent}).
		Suffix("RETURNING " + emailCampaignColumns).
		ToSql()
	if err != nil {
		return entity.EmailCampaign{}, fmt.Errorf("EmailRepo - ClaimEmailCampaignForRetry - Builder: %w", err)
	}

	campaign, err := scanEmailCampaign(r.Pool.QueryRow(ctx, sqlText, args...))
	if errors.Is(err, entity.ErrEmailCampaignNotFound) {
		return entity.EmailCampaign{}, entity.ErrInvalidEmailCampaign
	}

	return campaign, err
}

func (r *EmailRepo) UpdateEmailCampaign(
	ctx context.Context,
	campaign entity.EmailCampaign,
) (entity.EmailCampaign, error) {
	audienceJSON, metadataJSON, err := marshalCampaignJSON(campaign)
	if err != nil {
		return entity.EmailCampaign{}, err
	}
	sqlText, args, err := r.Builder.
		Update("email_campaigns").
		Set("name", campaign.Name).
		Set("template_id", campaign.TemplateID).
		Set("status", campaign.Status).
		Set("audience", string(audienceJSON)).
		Set("metadata", string(metadataJSON)).
		Set("scheduled_at", campaign.ScheduledAt).
		Set("sent_at", campaign.SentAt).
		Set("cancelled_at", campaign.CancelledAt).
		Set("updated_by", nullableStringPtrArg(campaign.UpdatedBy)).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": campaign.ID}).
		Suffix("RETURNING " + emailCampaignColumns).
		ToSql()
	if err != nil {
		return entity.EmailCampaign{}, fmt.Errorf("EmailRepo - UpdateEmailCampaign - Builder: %w", err)
	}

	return scanEmailCampaign(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) ListMarketingAudience(
	ctx context.Context,
	filter entity.EmailAudienceFilter,
) ([]entity.EmailAudienceRecipient, int, error) {
	limit := uint64(filter.Limit)
	if limit == 0 || limit > 10000 {
		limit = 1000
	}

	query := r.Builder.
		Select(
			"u.id",
			"u.email",
			"COALESCE(NULLIF(pref.preferred_ui_lang, ''), NULLIF(pref.preferred_content_lang, ''), 'id') AS lang",
			"count(*) OVER()",
		).
		From("users u").
		Join("email_subscriptions es ON es.user_id = u.id AND es.marketing_opt_in = true").
		LeftJoin("user_preferences pref ON pref.user_id = u.id").
		LeftJoin(
			"email_suppressions sup ON sup.email_normalized = lower(u.email) AND sup.scope IN ('marketing', 'all')",
		).
		Where("u.deleted_at IS NULL").
		Where("sup.id IS NULL").
		OrderBy("u.created_at ASC").
		Limit(limit)
	if strings.TrimSpace(filter.Role) != "" {
		query = query.Where(sq.Eq{"u.role": strings.TrimSpace(filter.Role)})
	}
	if strings.TrimSpace(filter.Lang) != "" {
		query = query.Where(
			"COALESCE(NULLIF(pref.preferred_ui_lang, ''), NULLIF(pref.preferred_content_lang, ''), 'id') = ?",
			strings.TrimSpace(filter.Lang),
		)
	}
	if filter.CreatedFrom != nil {
		query = query.Where("u.created_at >= ?", *filter.CreatedFrom)
	}
	if filter.CreatedTo != nil {
		query = query.Where("u.created_at <= ?", *filter.CreatedTo)
	}

	sqlText, args, err := query.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListMarketingAudience - Builder: %w", err)
	}
	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListMarketingAudience - Query: %w", err)
	}
	defer rows.Close()

	recipients := make([]entity.EmailAudienceRecipient, 0)
	total := 0
	for rows.Next() {
		var recipient entity.EmailAudienceRecipient
		if err = rows.Scan(&recipient.UserID, &recipient.Email, &recipient.Lang, &total); err != nil {
			return nil, 0, fmt.Errorf("EmailRepo - ListMarketingAudience - Scan: %w", err)
		}
		recipients = append(recipients, recipient)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EmailRepo - ListMarketingAudience - rows: %w", err)
	}

	return recipients, total, nil
}

func (r *EmailRepo) ReplaceEmailCampaignRecipients(
	ctx context.Context,
	campaignID string,
	recipients []entity.EmailCampaignRecipient,
) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("EmailRepo - ReplaceEmailCampaignRecipients - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, "DELETE FROM email_campaign_recipients WHERE campaign_id = $1", campaignID); err != nil {
		return fmt.Errorf("EmailRepo - ReplaceEmailCampaignRecipients - delete: %w", err)
	}
	for _, recipient := range recipients {
		_, err = tx.Exec(
			ctx,
			`INSERT INTO email_campaign_recipients (
    id, campaign_id, user_id, email, lang, unsubscribe_url, status, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			recipient.ID,
			recipient.CampaignID,
			recipient.UserID,
			recipient.Email,
			recipient.Lang,
			nullableStringArg(recipient.UnsubscribeURL),
			recipient.Status,
			recipient.CreatedAt,
			recipient.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("EmailRepo - ReplaceEmailCampaignRecipients - insert: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("EmailRepo - ReplaceEmailCampaignRecipients - Commit: %w", err)
	}

	return nil
}

func (r *EmailRepo) ListEmailCampaignRecipients(
	ctx context.Context,
	campaignID string,
	status string,
	limit int,
) ([]entity.EmailCampaignRecipient, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	query := r.Builder.
		Select(emailCampaignRecipientColumns).
		From("email_campaign_recipients").
		Where(sq.Eq{"campaign_id": campaignID}).
		OrderBy("created_at ASC").
		Limit(uint64(limit))
	if strings.TrimSpace(status) != "" {
		query = query.Where(sq.Eq{"status": strings.TrimSpace(status)})
	}
	sqlText, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - ListEmailCampaignRecipients - Builder: %w", err)
	}
	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - ListEmailCampaignRecipients - Query: %w", err)
	}
	defer rows.Close()

	recipients := make([]entity.EmailCampaignRecipient, 0)
	for rows.Next() {
		recipient, err := scanEmailCampaignRecipient(rows)
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, recipient)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("EmailRepo - ListEmailCampaignRecipients - rows: %w", err)
	}

	return recipients, nil
}

func (r *EmailRepo) ListEmailCampaignRecipientsForRetry(
	ctx context.Context,
	campaignID string,
	cutoff time.Time,
	limit int,
) ([]entity.EmailCampaignRecipient, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	sqlText, args, err := r.Builder.
		Select(emailCampaignRecipientColumns).
		From("email_campaign_recipients").
		Where(sq.Eq{"campaign_id": campaignID}).
		Where(sq.Eq{"status": entity.EmailRecipientStatusFailed}).
		Where("updated_at < ?", cutoff).
		OrderBy("updated_at ASC", "created_at ASC").
		Limit(uint64(limit)).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - ListEmailCampaignRecipientsForRetry - Builder: %w", err)
	}
	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - ListEmailCampaignRecipientsForRetry - Query: %w", err)
	}
	defer rows.Close()

	recipients := make([]entity.EmailCampaignRecipient, 0)
	for rows.Next() {
		recipient, err := scanEmailCampaignRecipient(rows)
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, recipient)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("EmailRepo - ListEmailCampaignRecipientsForRetry - rows: %w", err)
	}

	return recipients, nil
}

func (r *EmailRepo) CountEmailCampaignRecipientsByStatus(
	ctx context.Context,
	campaignID string,
) (map[string]int, error) {
	sqlText, args, err := r.Builder.
		Select("status", "count(*)").
		From("email_campaign_recipients").
		Where(sq.Eq{"campaign_id": campaignID}).
		GroupBy("status").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - CountEmailCampaignRecipientsByStatus - Builder: %w", err)
	}
	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - CountEmailCampaignRecipientsByStatus - Query: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("EmailRepo - CountEmailCampaignRecipientsByStatus - Scan: %w", err)
		}
		counts[status] = count
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("EmailRepo - CountEmailCampaignRecipientsByStatus - rows: %w", err)
	}

	return counts, nil
}

func (r *EmailRepo) UpdateEmailCampaignRecipientStatus(
	ctx context.Context,
	id,
	status,
	messageID,
	deliveryError string,
	sentAt *time.Time,
) (entity.EmailCampaignRecipient, error) {
	sqlText, args, err := r.Builder.
		Update("email_campaign_recipients").
		Set("status", status).
		Set("message_id", nullableStringArg(messageID)).
		Set("error", nullableStringArg(deliveryError)).
		Set("sent_at", sentAt).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": id}).
		Suffix("RETURNING " + emailCampaignRecipientColumns).
		ToSql()
	if err != nil {
		return entity.EmailCampaignRecipient{}, fmt.Errorf("EmailRepo - UpdateEmailCampaignRecipientStatus - Builder: %w", err)
	}

	return scanEmailCampaignRecipient(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) ListDueEmailCampaigns(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]entity.EmailCampaign, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	sqlText, args, err := r.Builder.
		Select(emailCampaignColumns).
		From("email_campaigns").
		Where(sq.Eq{"status": entity.EmailCampaignStatusScheduled}).
		Where("scheduled_at IS NOT NULL").
		Where("scheduled_at <= ?", now).
		OrderBy("scheduled_at ASC").
		Limit(uint64(limit)).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - ListDueEmailCampaigns - Builder: %w", err)
	}
	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - ListDueEmailCampaigns - Query: %w", err)
	}
	defer rows.Close()

	campaigns := make([]entity.EmailCampaign, 0)
	for rows.Next() {
		campaign, err := scanEmailCampaign(rows)
		if err != nil {
			return nil, err
		}
		campaigns = append(campaigns, campaign)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("EmailRepo - ListDueEmailCampaigns - rows: %w", err)
	}

	return campaigns, nil
}

func (r *EmailRepo) getEmailTemplate(ctx context.Context, predicate any) (entity.EmailTemplate, error) {
	sqlText, args, err := r.Builder.
		Select(emailTemplateColumns).
		From("email_templates").
		Where(predicate).
		Where("deleted_at IS NULL").
		Limit(1).
		ToSql()
	if err != nil {
		return entity.EmailTemplate{}, fmt.Errorf("EmailRepo - getEmailTemplate - Builder: %w", err)
	}

	return scanEmailTemplate(r.Pool.QueryRow(ctx, sqlText, args...))
}

func (r *EmailRepo) nextTemplateVersion(ctx context.Context, templateID, lang string) (int, error) {
	var version int
	err := r.Pool.QueryRow(
		ctx,
		"SELECT COALESCE(MAX(version), 0) + 1 FROM email_template_versions WHERE template_id = $1 AND lang = $2",
		templateID,
		lang,
	).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("EmailRepo - nextTemplateVersion - QueryRow: %w", err)
	}

	return version, nil
}

func createEmailSuppressionTx(
	ctx context.Context,
	execer interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	suppression entity.EmailSuppression,
) (entity.EmailSuppression, error) {
	const query = `
INSERT INTO email_suppressions (
    id, email, email_normalized, scope, reason, created_by, created_at
) VALUES ($1::uuid, $2::varchar, lower($2::varchar), $3::varchar, $4::varchar, $5::uuid, $6::timestamp)
ON CONFLICT (email_normalized, scope) DO UPDATE SET
    reason = EXCLUDED.reason
RETURNING id, email, scope, reason, created_by, created_at`

	return scanEmailSuppression(execer.QueryRow(
		ctx,
		query,
		suppression.ID,
		suppression.Email,
		suppression.Scope,
		suppression.Reason,
		nullableStringPtrArg(suppression.CreatedBy),
		suppression.CreatedAt,
	))
}

func upsertAutomaticEmailSuppressionTx(
	ctx context.Context,
	execer interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	suppression entity.EmailSuppression,
) (entity.EmailSuppression, error) {
	const query = `
INSERT INTO email_suppressions (
    id, email, email_normalized, scope, reason, created_by, created_at
) VALUES ($1::uuid, $2::varchar, lower($2::varchar), $3::varchar, $4::varchar, NULL, $5::timestamp)
ON CONFLICT (email_normalized, scope) DO UPDATE SET
    email = CASE
        WHEN email_suppressions.created_by IS NOT NULL THEN email_suppressions.email
        ELSE EXCLUDED.email
    END,
    reason = CASE
        WHEN email_suppressions.created_by IS NOT NULL THEN email_suppressions.reason
        ELSE EXCLUDED.reason
    END
RETURNING id, email, scope, reason, created_by, created_at`

	return scanEmailSuppression(execer.QueryRow(
		ctx,
		query,
		suppression.ID,
		suppression.Email,
		suppression.Scope,
		suppression.Reason,
		suppression.CreatedAt,
	))
}

func scanEmailTemplate(row rowScanner) (entity.EmailTemplate, error) {
	var template entity.EmailTemplate
	var archivedAt sql.NullTime
	var deletedAt sql.NullTime
	err := row.Scan(
		&template.ID,
		&template.Key,
		&template.Name,
		&template.Category,
		&template.Critical,
		&template.Enabled,
		&archivedAt,
		&deletedAt,
		&template.CreatedAt,
		&template.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailTemplate{}, entity.ErrEmailTemplateNotFound
		}

		return entity.EmailTemplate{}, fmt.Errorf("EmailRepo - scanEmailTemplate - Scan: %w", err)
	}
	template.ArchivedAt = nullableTime(archivedAt)
	template.DeletedAt = nullableTime(deletedAt)

	return template, nil
}

func scanEmailTemplateWithTotal(row rowScanner) (entity.EmailTemplate, int, error) {
	var template entity.EmailTemplate
	var archivedAt sql.NullTime
	var deletedAt sql.NullTime
	var total int
	err := row.Scan(
		&template.ID,
		&template.Key,
		&template.Name,
		&template.Category,
		&template.Critical,
		&template.Enabled,
		&archivedAt,
		&deletedAt,
		&template.CreatedAt,
		&template.UpdatedAt,
		&total,
	)
	if err != nil {
		return entity.EmailTemplate{}, 0, fmt.Errorf("EmailRepo - scanEmailTemplateWithTotal - Scan: %w", err)
	}
	template.ArchivedAt = nullableTime(archivedAt)
	template.DeletedAt = nullableTime(deletedAt)

	return template, total, nil
}

func scanEmailTemplateVersion(row rowScanner) (entity.EmailTemplateVersion, error) {
	var version entity.EmailTemplateVersion
	var createdBy sql.NullString
	var publishedBy sql.NullString
	var publishedAt sql.NullTime
	err := row.Scan(
		&version.ID,
		&version.TemplateID,
		&version.Lang,
		&version.Version,
		&version.SubjectTemplate,
		&version.PreviewTemplate,
		&version.TitleTemplate,
		&version.BodyTemplate,
		&version.ButtonLabelTemplate,
		&version.ButtonURLTemplate,
		&version.NoteTemplate,
		&version.FooterTemplate,
		&version.TextTemplate,
		&version.RequiredVariables,
		&version.Published,
		&createdBy,
		&publishedBy,
		&publishedAt,
		&version.CreatedAt,
		&version.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailTemplateVersion{}, entity.ErrEmailTemplateVersionNotFound
		}

		return entity.EmailTemplateVersion{}, fmt.Errorf("EmailRepo - scanEmailTemplateVersion - Scan: %w", err)
	}
	version.CreatedBy = nullableString(createdBy)
	version.PublishedBy = nullableString(publishedBy)
	version.PublishedAt = nullableTime(publishedAt)

	return version, nil
}

func scanEmailTemplateVersionAndTemplate(
	row rowScanner,
) (entity.EmailTemplateVersion, entity.EmailTemplate, error) {
	var version entity.EmailTemplateVersion
	var template entity.EmailTemplate
	var createdBy sql.NullString
	var publishedBy sql.NullString
	var publishedAt sql.NullTime
	var archivedAt sql.NullTime
	var deletedAt sql.NullTime
	err := row.Scan(
		&version.ID,
		&version.TemplateID,
		&version.Lang,
		&version.Version,
		&version.SubjectTemplate,
		&version.PreviewTemplate,
		&version.TitleTemplate,
		&version.BodyTemplate,
		&version.ButtonLabelTemplate,
		&version.ButtonURLTemplate,
		&version.NoteTemplate,
		&version.FooterTemplate,
		&version.TextTemplate,
		&version.RequiredVariables,
		&version.Published,
		&createdBy,
		&publishedBy,
		&publishedAt,
		&version.CreatedAt,
		&version.UpdatedAt,
		&template.ID,
		&template.Key,
		&template.Name,
		&template.Category,
		&template.Critical,
		&template.Enabled,
		&archivedAt,
		&deletedAt,
		&template.CreatedAt,
		&template.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailTemplateVersion{}, entity.EmailTemplate{}, entity.ErrEmailTemplateVersionNotFound
		}

		return entity.EmailTemplateVersion{}, entity.EmailTemplate{}, fmt.Errorf(
			"EmailRepo - scanEmailTemplateVersionAndTemplate - Scan: %w",
			err,
		)
	}
	version.CreatedBy = nullableString(createdBy)
	version.PublishedBy = nullableString(publishedBy)
	version.PublishedAt = nullableTime(publishedAt)
	template.ArchivedAt = nullableTime(archivedAt)
	template.DeletedAt = nullableTime(deletedAt)

	return version, template, nil
}

func scanEmailEventSetting(row rowScanner) (entity.EmailEventSetting, error) {
	var setting entity.EmailEventSetting
	var cooldown sql.NullInt64
	err := row.Scan(
		&setting.Key,
		&setting.TemplateID,
		&setting.Enabled,
		&setting.Critical,
		&cooldown,
		&setting.CreatedAt,
		&setting.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailEventSetting{}, entity.ErrEmailEventSettingNotFound
		}

		return entity.EmailEventSetting{}, fmt.Errorf("EmailRepo - scanEmailEventSetting - Scan: %w", err)
	}
	if cooldown.Valid {
		value := cooldown.Int64
		setting.CooldownSeconds = &value
	}

	return setting, nil
}

func scanEmailMessage(row rowScanner) (entity.EmailMessageLog, error) {
	message, _, err := scanEmailMessageInternal(row, false)

	return message, err
}

func scanEmailMessageWithTotal(row rowScanner) (entity.EmailMessageLog, int, error) {
	return scanEmailMessageInternal(row, true)
}

func scanEmailMessageInternal(row rowScanner, withTotal bool) (entity.EmailMessageLog, int, error) {
	var message entity.EmailMessageLog
	var metadataRaw []byte
	var headersRaw []byte
	var scheduledAt sql.NullTime
	var sentAt sql.NullTime
	total := 0
	dest := []any{
		&message.ID,
		&message.Category,
		&message.TemplateKey,
		&message.TemplateVersionID,
		&message.CampaignID,
		&message.CampaignRecipient,
		&message.UserID,
		&message.RecipientEmail,
		&message.Lang,
		&message.Subject,
		&message.Status,
		&message.Attempts,
		&message.ProviderResponse,
		&message.Error,
		&metadataRaw,
		&message.HTML,
		&message.Text,
		&message.Critical,
		&headersRaw,
		&scheduledAt,
		&sentAt,
		&message.CreatedAt,
		&message.UpdatedAt,
	}
	if withTotal {
		dest = append(dest, &total)
	}
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailMessageLog{}, 0, entity.ErrEmailMessageNotFound
		}

		return entity.EmailMessageLog{}, 0, fmt.Errorf("EmailRepo - scanEmailMessageInternal - Scan: %w", err)
	}
	message.ScheduledAt = nullableTime(scheduledAt)
	message.SentAt = nullableTime(sentAt)
	message.Metadata = unmarshalStringMap(metadataRaw)
	message.Headers = unmarshalStringMap(headersRaw)

	return message, total, nil
}

func scanEmailSubscription(row rowScanner) (entity.EmailSubscription, error) {
	var subscription entity.EmailSubscription
	var optedInAt sql.NullTime
	var optedOutAt sql.NullTime
	err := row.Scan(
		&subscription.UserID,
		&subscription.MarketingOptIn,
		&optedInAt,
		&optedOutAt,
		&subscription.Source,
		&subscription.CreatedAt,
		&subscription.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailSubscription{}, entity.ErrEmailSubscriptionNotFound
		}

		return entity.EmailSubscription{}, fmt.Errorf("EmailRepo - scanEmailSubscription - Scan: %w", err)
	}
	subscription.OptedInAt = nullableTime(optedInAt)
	subscription.OptedOutAt = nullableTime(optedOutAt)

	return subscription, nil
}

func scanEmailSuppression(row rowScanner) (entity.EmailSuppression, error) {
	var suppression entity.EmailSuppression
	var createdBy sql.NullString
	err := row.Scan(
		&suppression.ID,
		&suppression.Email,
		&suppression.Scope,
		&suppression.Reason,
		&createdBy,
		&suppression.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailSuppression{}, entity.ErrEmailSuppressionNotFound
		}

		return entity.EmailSuppression{}, fmt.Errorf("EmailRepo - scanEmailSuppression - Scan: %w", err)
	}
	suppression.CreatedBy = nullableString(createdBy)

	return suppression, nil
}

func scanEmailSuppressionWithTotal(row rowScanner) (entity.EmailSuppression, int, error) {
	var suppression entity.EmailSuppression
	var createdBy sql.NullString
	var total int
	err := row.Scan(
		&suppression.ID,
		&suppression.Email,
		&suppression.Scope,
		&suppression.Reason,
		&createdBy,
		&suppression.CreatedAt,
		&total,
	)
	if err != nil {
		return entity.EmailSuppression{}, 0, fmt.Errorf("EmailRepo - scanEmailSuppressionWithTotal - Scan: %w", err)
	}
	suppression.CreatedBy = nullableString(createdBy)

	return suppression, total, nil
}

type emailDeliveryEventInsertResult struct {
	event    entity.EmailDeliveryEvent
	inserted bool
}

func scanEmailDeliveryEventWithInserted(row rowScanner) (emailDeliveryEventInsertResult, error) {
	var event entity.EmailDeliveryEvent
	var rawPayload []byte
	var inserted bool
	err := row.Scan(
		&event.ID,
		&event.DedupeKey,
		&event.Provider,
		&event.EventType,
		&event.RecipientEmail,
		&event.MessageID,
		&event.CampaignID,
		&event.CampaignRecipient,
		&event.Reason,
		&event.Diagnostic,
		&rawPayload,
		&event.OccurredAt,
		&event.CreatedAt,
		&inserted,
	)
	if err != nil {
		return emailDeliveryEventInsertResult{}, fmt.Errorf(
			"EmailRepo - scanEmailDeliveryEventWithInserted - Scan: %w",
			err,
		)
	}
	event.RawPayload = entity.RawJSON(rawPayload)

	return emailDeliveryEventInsertResult{event: event, inserted: inserted}, nil
}

func scanEmailDeliveryEventWithTotal(row rowScanner) (entity.EmailDeliveryEvent, int, error) {
	var event entity.EmailDeliveryEvent
	var rawPayload []byte
	var total int
	err := row.Scan(
		&event.ID,
		&event.DedupeKey,
		&event.Provider,
		&event.EventType,
		&event.RecipientEmail,
		&event.MessageID,
		&event.CampaignID,
		&event.CampaignRecipient,
		&event.Reason,
		&event.Diagnostic,
		&rawPayload,
		&event.OccurredAt,
		&event.CreatedAt,
		&total,
	)
	if err != nil {
		return entity.EmailDeliveryEvent{}, 0, fmt.Errorf(
			"EmailRepo - scanEmailDeliveryEventWithTotal - Scan: %w",
			err,
		)
	}
	event.RawPayload = entity.RawJSON(rawPayload)

	return event, total, nil
}

func scanEmailProviderPollCursor(row rowScanner) (entity.EmailProviderPollCursor, error) {
	var cursor entity.EmailProviderPollCursor
	err := row.Scan(
		&cursor.Provider,
		&cursor.CursorKey,
		&cursor.LastPolledAt,
		&cursor.CreatedAt,
		&cursor.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailProviderPollCursor{}, entity.ErrEmailProviderPollCursorNotFound
		}

		return entity.EmailProviderPollCursor{}, fmt.Errorf("EmailRepo - scanEmailProviderPollCursor - Scan: %w", err)
	}

	return cursor, nil
}

func scanEmailCampaign(row rowScanner) (entity.EmailCampaign, error) {
	campaign, _, err := scanEmailCampaignInternal(row, false)

	return campaign, err
}

func scanEmailCampaignWithTotal(row rowScanner) (entity.EmailCampaign, int, error) {
	return scanEmailCampaignInternal(row, true)
}

func scanEmailCampaignInternal(row rowScanner, withTotal bool) (entity.EmailCampaign, int, error) {
	var campaign entity.EmailCampaign
	var audienceRaw []byte
	var metadataRaw []byte
	var scheduledAt sql.NullTime
	var sentAt sql.NullTime
	var cancelledAt sql.NullTime
	var createdBy sql.NullString
	var updatedBy sql.NullString
	total := 0
	dest := []any{
		&campaign.ID,
		&campaign.Name,
		&campaign.TemplateID,
		&campaign.Status,
		&audienceRaw,
		&metadataRaw,
		&scheduledAt,
		&sentAt,
		&cancelledAt,
		&createdBy,
		&updatedBy,
		&campaign.CreatedAt,
		&campaign.UpdatedAt,
	}
	if withTotal {
		dest = append(dest, &total)
	}
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailCampaign{}, 0, entity.ErrEmailCampaignNotFound
		}

		return entity.EmailCampaign{}, 0, fmt.Errorf("EmailRepo - scanEmailCampaignInternal - Scan: %w", err)
	}
	if len(audienceRaw) > 0 {
		_ = json.Unmarshal(audienceRaw, &campaign.Audience)
	}
	campaign.Metadata = unmarshalStringMap(metadataRaw)
	campaign.ScheduledAt = nullableTime(scheduledAt)
	campaign.SentAt = nullableTime(sentAt)
	campaign.CancelledAt = nullableTime(cancelledAt)
	campaign.CreatedBy = nullableString(createdBy)
	campaign.UpdatedBy = nullableString(updatedBy)

	return campaign, total, nil
}

func scanEmailCampaignRecipient(row rowScanner) (entity.EmailCampaignRecipient, error) {
	var recipient entity.EmailCampaignRecipient
	var sentAt sql.NullTime
	err := row.Scan(
		&recipient.ID,
		&recipient.CampaignID,
		&recipient.UserID,
		&recipient.Email,
		&recipient.Lang,
		&recipient.UnsubscribeURL,
		&recipient.Status,
		&recipient.MessageID,
		&recipient.Error,
		&sentAt,
		&recipient.CreatedAt,
		&recipient.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailCampaignRecipient{}, entity.ErrEmailCampaignNotFound
		}

		return entity.EmailCampaignRecipient{}, fmt.Errorf("EmailRepo - scanEmailCampaignRecipient - Scan: %w", err)
	}
	recipient.SentAt = nullableTime(sentAt)

	return recipient, nil
}

func marshalStringMap(values map[string]string) ([]byte, error) {
	if values == nil {
		values = map[string]string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - marshalStringMap: %w", err)
	}

	return encoded, nil
}

func unmarshalStringMap(raw []byte) map[string]string {
	values := map[string]string{}
	if len(raw) == 0 {
		return values
	}
	_ = json.Unmarshal(raw, &values)

	return values
}

func marshalCampaignJSON(campaign entity.EmailCampaign) ([]byte, []byte, error) {
	audienceJSON, err := json.Marshal(campaign.Audience)
	if err != nil {
		return nil, nil, fmt.Errorf("EmailRepo - marshalCampaignJSON audience: %w", err)
	}
	metadataJSON, err := marshalStringMap(campaign.Metadata)
	if err != nil {
		return nil, nil, err
	}

	return audienceJSON, metadataJSON, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
