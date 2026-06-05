# Admin Email API Frontend Guide

This document is the implementation guide for frontend teams building the Surau email admin, user email preferences, and public unsubscribe screens. The generated OpenAPI reference is still available at `/swagger/index.html`, but this guide explains the expected flows, states, and edge cases.

## Quick Start

Use these base rules everywhere:

- Admin email endpoints require `Authorization: Bearer <admin JWT>`.
- User preference endpoints require any authenticated user JWT.
- Public unsubscribe and Cloudflare webhook endpoints do not use JWT.
- JSON requests use `Content-Type: application/json`.
- Paginated list responses use `{ "items": [...], "total": n }`.
- Pagination defaults to `limit=50&offset=0`; backend caps repo page size at `100` for delivery/message style lists.
- Timestamps are RFC3339 UTC strings.
- Supported template languages: `id`, `en`, `ar`.
- Common API errors look like `{ "error": "...", "code": "...", "request_id": "..." }`; branch primarily on HTTP status.
- Path placeholder `{id}` follows the route context: template ID under `/templates/{id}`, version ID under `/versions/{id}`, campaign ID under `/campaigns/{id}`, message ID under `/messages/{id}`, and campaign recipient ID under `/campaign-recipients/{id}`.

Recommended FE modules:

1. Template management: create templates, versions, preview, test send, publish.
2. Transactional settings: toggle security notification events and cooldowns.
3. Marketing campaigns: draft, preview audience, test send, schedule/send, retry failed, cancel.
4. Logs and observability: message logs, delivery events, campaign event summary.
5. Suppression management: manual suppression and bounce/complaint visibility.
6. User-facing pages: email preferences and public unsubscribe result page.

## Auth And URLs

Production API base URL:

```txt
https://api.surau.org/v1
```

Frontend routes configured in backend env:

```env
EMAIL_VERIFY_FRONTEND_URL=https://surau.org/verify-email
PASSWORD_RESET_FRONTEND_URL=https://surau.org/reset-password
EMAIL_CHANGE_FRONTEND_URL=https://surau.org/change-email
EMAIL_UNSUBSCRIBE_FRONTEND_URL=https://surau.org/unsubscribe
EMAIL_UNSUBSCRIBE_PUBLIC_URL=https://api.surau.org/v1/email/unsubscribe
```

Important distinction:

- `EMAIL_UNSUBSCRIBE_FRONTEND_URL` is the link in campaign body. It should open the FE unsubscribe page.
- `EMAIL_UNSUBSCRIBE_PUBLIC_URL` is the backend one-click endpoint used in `List-Unsubscribe`. FE does not render this URL directly.

## Core Types

Use these TypeScript shapes as the practical FE contract.

```ts
type EmailCategory = "transactional" | "marketing";
type EmailTemplateKey =
  | "auth_verification"
  | "auth_password_reset"
  | "auth_email_change_verification"
  | "auth_password_changed"
  | "auth_email_verified"
  | "auth_new_login"
  | "auth_failed_login"
  | "auth_role_changed"
  | "auth_email_changed"
  | "auth_account_deleted"
  | string;

type EmailTemplate = {
  id: string;
  key: EmailTemplateKey;
  name: string;
  category: EmailCategory;
  critical: boolean;
  enabled: boolean;
  archived_at?: string;
  deleted_at?: string;
  created_at: string;
  updated_at: string;
};

type EmailTemplateVersion = {
  id: string;
  template_id: string;
  lang: "id" | "en" | "ar" | string;
  version: number;
  subject_template: string;
  preview_template: string;
  title_template: string;
  body_template: string;
  button_label_template: string;
  button_url_template: string;
  note_template: string;
  footer_template: string;
  text_template: string;
  required_variables: string[];
  published: boolean;
  created_by?: string;
  published_by?: string;
  published_at?: string;
  created_at: string;
  updated_at: string;
};

type EmailPreview = {
  subject: string;
  html: string;
  text: string;
  lang: string;
};

type EmailEventSetting = {
  key: EmailTemplateKey;
  template_id: string;
  enabled: boolean;
  critical: boolean;
  cooldown_seconds?: number;
  created_at: string;
  updated_at: string;
};

type EmailMessageStatus = "queued" | "sent" | "failed" | "skipped";

type EmailMessageLog = {
  id: string;
  category: EmailCategory;
  template_key?: string;
  template_version_id?: string;
  campaign_id?: string;
  campaign_recipient_id?: string;
  user_id?: string;
  recipient_email: string;
  lang: string;
  subject: string;
  status: EmailMessageStatus;
  attempts: number;
  provider_response?: string;
  error?: string;
  metadata?: Record<string, string>;
  scheduled_at?: string;
  sent_at?: string;
  created_at: string;
  updated_at: string;
};

type EmailSuppressionScope = "marketing" | "all";

type EmailSuppression = {
  id: string;
  email: string;
  scope: EmailSuppressionScope;
  reason: string;
  created_by?: string;
  created_at: string;
};

type EmailDeliveryEventType = "bounce_hard" | "complaint";

type EmailDeliveryEvent = {
  id: string;
  dedupe_key: string;
  provider: "cloudflare" | "log" | string;
  event_type: EmailDeliveryEventType;
  recipient_email: string;
  message_id?: string;
  campaign_id?: string;
  campaign_recipient_id?: string;
  reason?: string;
  diagnostic?: string;
  raw_payload?: unknown;
  occurred_at: string;
  created_at: string;
};

type EmailCampaignStatus = "draft" | "scheduled" | "sending" | "sent" | "cancelled";
type EmailRecipientStatus = "pending" | "sent" | "failed" | "skipped";

type EmailAudienceFilter = {
  role?: string;
  lang?: string;
  created_from?: string;
  created_to?: string;
  limit?: number;
};

type EmailCampaign = {
  id: string;
  name: string;
  template_id: string;
  status: EmailCampaignStatus;
  audience: EmailAudienceFilter;
  metadata?: Record<string, string>;
  scheduled_at?: string;
  sent_at?: string;
  cancelled_at?: string;
  created_by?: string;
  updated_by?: string;
  created_at: string;
  updated_at: string;
};

type EmailAudienceRecipient = {
  user_id: string;
  email: string;
  lang: string;
};

type EmailCampaignDeliveryEventSummary = {
  campaign_id: string;
  total: number;
  bounce_hard: number;
  complaint: number;
  unique_recipients: number;
  last_occurred_at?: string;
};

type EmailSubscription = {
  user_id: string;
  marketing_opt_in: boolean;
  opted_in_at?: string;
  opted_out_at?: string;
  source?: string;
  created_at: string;
  updated_at: string;
};
```

Security note: `EmailMessageLog` intentionally never exposes rendered `html`, `text`, `headers`, OTPs, raw tokens, or retry payload bodies. Metadata is redacted for sensitive values.

## Template Management

### List Templates

```http
GET /v1/admin/emails/templates?q=&category=transactional&include_archived=false&limit=50&offset=0
Authorization: Bearer <admin-token>
```

Query params:

| Param | Type | Notes |
| --- | --- | --- |
| `q` | string | Search by key or name. |
| `category` | string | `transactional` or `marketing`. Omit for all. |
| `include_archived` | boolean | Default false. |
| `limit` | number | Default 50. |
| `offset` | number | Default 0. |

Response:

```json
{
  "items": [
    {
      "id": "template-id",
      "key": "weekly_digest",
      "name": "Weekly Digest",
      "category": "marketing",
      "critical": false,
      "enabled": true,
      "created_at": "2026-06-05T00:00:00Z",
      "updated_at": "2026-06-05T00:00:00Z"
    }
  ],
  "total": 1
}
```

### Create Template

```http
POST /v1/admin/emails/templates
```

```json
{
  "key": "weekly_digest",
  "name": "Weekly Digest",
  "category": "marketing",
  "critical": false,
  "enabled": true
}
```

Rules:

- `key` must be unique and max 128 chars.
- `category` must be `transactional` or `marketing`.
- `enabled` defaults to `true` if omitted.
- `critical=true` should only be used for auth/security templates that must bypass suppression.

### Get, Update, Archive/Delete Template

```http
GET /v1/admin/emails/templates/{id}
PATCH /v1/admin/emails/templates/{id}
DELETE /v1/admin/emails/templates/{id}
```

Patch body supports:

```json
{
  "name": "Weekly Digest",
  "enabled": true,
  "archived": false
}
```

`DELETE` soft-deletes/archives the template. FE should refresh list and avoid editing deleted templates.

## Template Versions

A template version is localized content. Versions can be draft or published. Publishing one version for a language makes it the active send version for that template/language.

### List Versions

```http
GET /v1/admin/emails/templates/{id}/versions
```

Response:

```json
{
  "items": [EmailTemplateVersion],
  "total": 3
}
```

### Create Version

```http
POST /v1/admin/emails/templates/{id}/versions
```

```json
{
  "lang": "id",
  "subject_template": "Update dari Surau",
  "preview_template": "Ada bacaan baru untuk Anda.",
  "title_template": "Update Surau",
  "body_template": "Assalamu'alaikum, {{.name}}.",
  "button_label_template": "Buka Surau",
  "button_url_template": "{{.link}}",
  "note_template": "",
  "footer_template": "Anda menerima email ini karena berlangganan update Surau.",
  "text_template": "Assalamu'alaikum, {{.name}}.\n{{.link}}",
  "required_variables": ["name", "link"]
}
```

Template syntax:

- Uses Go template variables: `{{.name}}`, `{{.link}}`, `{{.otp}}`.
- `required_variables` are validated during preview/test-send/send.
- `subject_template` and `text_template` are required.
- HTML email is composed from title/body/button/note/footer fields.

Common built-in transactional variables:

| Template key | Important variables |
| --- | --- |
| `auth_verification` | `name`, `link`, `otp`, `otp_duration` |
| `auth_password_reset` | `name`, `link` |
| `auth_email_change_verification` | `name`, `new_email`, `link`, `otp`, `otp_duration` |
| security notification keys | Depends on event, but normally `name`, event metadata, and support text. |

Marketing campaign variables:

- Campaign variables come from campaign metadata plus recipient-specific values.
- Every real campaign recipient gets `email`, `lang`, and a tokenized `unsubscribe_url`.
- Test-send campaign emails only get values the admin supplies plus campaign metadata; they do not automatically get a real unsubscribe token.

### Update Version

```http
PATCH /v1/admin/emails/versions/{id}
```

Body is a partial patch; send only fields being edited:

```json
{
  "subject_template": "Update Surau terbaru",
  "required_variables": ["name", "link"]
}
```

### Publish Version

```http
POST /v1/admin/emails/versions/{id}/publish
```

FE behavior:

- Show published badge from `published=true`.
- After publish, refresh versions list and template detail.
- Transactional templates should have published `id`, `en`, and `ar` versions.
- Marketing templates require `id`; `en` and `ar` are optional but recommended.

## Preview And Test Send

### Preview Template

```http
POST /v1/admin/emails/templates/{id}/preview
```

```json
{
  "lang": "id",
  "variables": {
    "name": "Admin",
    "link": "https://surau.org"
  }
}
```

Response:

```json
{
  "subject": "Update dari Surau",
  "html": "<html>...</html>",
  "text": "Assalamu'alaikum, Admin.\nhttps://surau.org",
  "lang": "id"
}
```

FE implementation:

- Render `html` in a sandboxed preview iframe/webview if possible.
- Show `text` fallback in a separate tab.
- If backend returns `400 invalid email template`, highlight missing `required_variables` or invalid template syntax.

### Test Send Template

```http
POST /v1/admin/emails/templates/{id}/test-send
```

```json
{
  "to": "admin@example.com",
  "lang": "id",
  "variables": {
    "name": "Admin",
    "link": "https://surau.org"
  }
}
```

Response status: `202 Accepted` with `EmailMessageLog`.

Notes:

- Test sends create normal message logs.
- If provider has a transient error for transactional category, the message can be returned as `queued` for retry.
- `503 email delivery failed` means the send was terminal for the request path.

## Transactional Settings

Transactional templates are seeded for:

- `auth_verification`
- `auth_password_reset`
- `auth_email_change_verification`
- `auth_password_changed`
- `auth_email_verified`
- `auth_new_login`
- `auth_failed_login`
- `auth_role_changed`
- `auth_email_changed`
- `auth_account_deleted`

Endpoints:

```http
GET /v1/admin/emails/events/{key}
PATCH /v1/admin/emails/events/{key}
```

Patch body:

```json
{
  "enabled": true,
  "cooldown_seconds": 86400
}
```

FE behavior:

- Critical auth link events cannot effectively be disabled. Show `critical=true` as locked or warning state.
- Security notification events are best-effort and may be disabled.
- `cooldown_seconds` is optional; when absent, show no cooldown override.

## Message Logs

```http
GET /v1/admin/emails/messages?category=marketing&status=failed&email=user@example.com&limit=50&offset=0
```

Filters:

| Param | Values | Notes |
| --- | --- | --- |
| `category` | `transactional`, `marketing` | Optional. |
| `status` | `queued`, `sent`, `failed`, `skipped` | Optional. |
| `email` | email address | Case-insensitive recipient search. |
| `limit` | number | Default 50. |
| `offset` | number | Default 0. |

Status meanings:

| Status | Meaning | FE display |
| --- | --- | --- |
| `queued` | Scheduled for retry or future dispatch. | Pending/retry badge, show `scheduled_at` if present. |
| `sent` | Provider accepted/delivered/queued successfully. | Success badge, show `sent_at`. |
| `failed` | Terminal failure or permanent bounce. | Error badge, show `error`. |
| `skipped` | Not sent due to disabled event or suppression. | Muted badge, show `error` reason. |

Sensitive metadata such as `link`, `otp`, `token`, `unsubscribe_url`, and URL values with a `token` query param are stored as `[redacted]`. FE should never expect raw OTP/token in logs.

Transactional retry/backoff behavior:

- First send is attempted immediately.
- Transient provider failure stores retry payload internally and returns success to caller flows; admin log status becomes `queued`.
- Retry delays after the initial attempt: `1m`, `5m`, `15m`, `1h`, `6h`.
- After final retry failure, status becomes `failed`.
- Permanent bounce is terminal and creates suppression; it is not retried.
- Non-critical queued messages are skipped if recipient becomes suppressed before retry.
- Critical queued messages bypass suppression.

## Delivery Events Observability

Delivery events are provider-side hard-bounce/complaint audit rows. Use these screens to explain why an email/campaign failed after provider feedback arrives.

### List Delivery Events

```http
GET /v1/admin/emails/delivery-events?provider=cloudflare&event_type=bounce_hard&email=user@example.com&message_id=&campaign_id=&campaign_recipient_id=&limit=50&offset=0
```

Filters:

| Param | Values | Notes |
| --- | --- | --- |
| `provider` | `cloudflare`, `log`, string | Usually `cloudflare`. |
| `event_type` | `bounce_hard`, `complaint` | Optional. |
| `email` | email address | Case-insensitive recipient filter. |
| `message_id` | UUID | Local `EmailMessageLog.id`. |
| `campaign_id` | UUID | Campaign ID. |
| `campaign_recipient_id` | UUID | Campaign recipient ID. |
| `limit` | number | Default 50. |
| `offset` | number | Default 0. |

Response:

```json
{
  "items": [
    {
      "id": "event-id",
      "dedupe_key": "sha256...",
      "provider": "cloudflare",
      "event_type": "bounce_hard",
      "recipient_email": "user@example.com",
      "message_id": "message-id",
      "campaign_id": "campaign-id",
      "campaign_recipient_id": "recipient-id",
      "reason": "permanent_bounce",
      "diagnostic": "550 5.1.1 user unknown",
      "raw_payload": { "provider": "cloudflare" },
      "occurred_at": "2026-06-05T01:02:03Z",
      "created_at": "2026-06-05T01:02:04Z"
    }
  ],
  "total": 1
}
```

Admin-only detail: `raw_payload` is intentionally returned for debugging provider payloads.

### Convenience Endpoints

```http
GET /v1/admin/emails/messages/{id}/delivery-events?limit=50&offset=0
GET /v1/admin/emails/campaign-recipients/{id}/delivery-events?limit=50&offset=0
```

Use these from a message detail drawer or campaign recipient row.

### Campaign Delivery Event Summary

```http
GET /v1/admin/emails/campaigns/{id}/delivery-event-summary
```

Response:

```json
{
  "campaign_id": "campaign-id",
  "total": 4,
  "bounce_hard": 3,
  "complaint": 1,
  "unique_recipients": 4,
  "last_occurred_at": "2026-06-05T01:02:03Z"
}
```

FE usage:

- Show bounce/complaint counts on campaign detail.
- If `total > 0`, provide a link to filtered delivery events for the campaign.
- `last_occurred_at` may be omitted for zero summary.

## Suppressions

Suppressions prevent delivery to an email address.

```http
GET /v1/admin/emails/suppressions?email=user@example.com&scope=marketing&limit=50&offset=0
POST /v1/admin/emails/suppressions
DELETE /v1/admin/emails/suppressions/{id}
```

Create body:

```json
{
  "email": "user@example.com",
  "scope": "marketing",
  "reason": "manual"
}
```

Scopes:

| Scope | Blocks |
| --- | --- |
| `marketing` | Campaign/marketing email only. |
| `all` | All non-critical email categories. Critical auth/security emails can still send. |

Suppression reasons you may see:

| Reason | Source |
| --- | --- |
| `manual` or custom text | Admin-created suppression. |
| `unsubscribe` | Public unsubscribe or user opt-out. |
| `permanent_bounce` | Cloudflare sync bounce, webhook, or polling. |
| `complaint` | Cloudflare complaint webhook. |

Automated suppression is idempotent. If an address already has a manual suppression, automated bounce handling preserves the manual reason instead of overwriting it.

## Campaigns

Campaigns use marketing templates and send one provider email per recipient, never BCC. Audience generation only includes users with explicit marketing opt-in and excludes suppressed emails.

### Create Or Update Draft

```http
POST /v1/admin/emails/campaigns
PATCH /v1/admin/emails/campaigns/{id}
```

```json
{
  "name": "Ramadan Digest",
  "template_id": "550e8400-e29b-41d4-a716-446655440000",
  "audience": {
    "role": "user",
    "lang": "id",
    "created_from": "2026-01-01T00:00:00Z",
    "created_to": "2026-05-01T00:00:00Z",
    "limit": 1000
  },
  "metadata": {
    "topic": "ramadan"
  }
}
```

Rules:

- Template must be marketing category, enabled, and not archived/deleted.
- Campaign is editable only while `draft`.
- `audience.limit` max is `10000`.
- `metadata` is available as template variables.

### List And Detail

```http
GET /v1/admin/emails/campaigns?status=draft&limit=50&offset=0
GET /v1/admin/emails/campaigns/{id}
```

Campaign statuses:

| Status | Meaning | Allowed FE actions |
| --- | --- | --- |
| `draft` | Editable, not queued. | edit, preview audience, test send, schedule, send now. |
| `scheduled` | Queued for scheduler. | cancel. |
| `sending` | Dispatch in progress. | read-only. |
| `sent` | Dispatch finished. | retry failed if failures exist. |
| `cancelled` | Cancelled before send. | read-only. |

After send, campaign `metadata` includes:

- `delivery_total`
- `delivery_sent`
- `delivery_failed`
- `delivery_skipped`
- `delivery_finished_at`

After retry failed, metadata also includes:

- `retry_failed_total`
- `retry_failed_sent`
- `retry_failed_failed`
- `retry_failed_skipped`
- `retry_failed_finished_at`

### Preview Audience

```http
POST /v1/admin/emails/campaigns/{id}/preview-audience
```

Response:

```json
{
  "items": [
    { "user_id": "user-id", "email": "user@example.com", "lang": "id" }
  ],
  "total": 1
}
```

FE usage:

- Call this before scheduling/sending.
- Show `total` as expected recipients.
- If `total=0`, block or warn before send.

### Test Send Campaign

```http
POST /v1/admin/emails/campaigns/{id}/test-send
```

```json
{
  "to": "admin@example.com",
  "lang": "id",
  "variables": {
    "name": "Admin"
  }
}
```

Response status: `202 Accepted` with `EmailMessageLog`.

Note: campaign test-send does not create a real campaign recipient and normally does not include one-click unsubscribe headers unless caller supplies a tokenized `unsubscribe_url` variable.

### Schedule Campaign

```http
POST /v1/admin/emails/campaigns/{id}/schedule
```

```json
{
  "scheduled_at": "2026-06-01T09:00:00Z"
}
```

FE behavior:

- Use UTC/RFC3339. Convert from local date-time carefully.
- Backend scheduler runs every minute, so actual send may be slightly later than `scheduled_at`.

### Send Now

```http
POST /v1/admin/emails/campaigns/{id}/send-now
```

Response status: `202 Accepted` with `EmailCampaign`.

Send behavior:

- Creates recipient snapshots from current audience.
- Sends in batches internally.
- Suppressed recipients are marked `skipped` before provider send.
- Hard bounce during send marks recipient/message failed and creates `all` suppression.
- Campaign is marked `sent` when dispatch loop completes, even if some recipients failed/skipped.

### Retry Failed

```http
POST /v1/admin/emails/campaigns/{id}/retry-failed
```

Response status: `202 Accepted` with `EmailCampaign`.

Rules:

- Valid only for `sent` campaigns.
- Retries only campaign recipients currently `failed`.
- Uses the original recipient snapshot, not a fresh audience query.
- Suppressed recipients can become `skipped` during retry.

### Cancel

```http
POST /v1/admin/emails/campaigns/{id}/cancel
```

Response status: `200 OK` with `EmailCampaign`.

Use for scheduled campaigns that should not send.

## User Preferences

Authenticated user endpoints:

```http
GET /v1/user/email-preferences
PATCH /v1/user/email-preferences
```

Patch body:

```json
{
  "marketing_opt_in": true
}
```

Behavior:

- `marketing_opt_in=true` records opt-in and removes the user's `marketing/unsubscribe` suppression entry if one exists.
- `marketing_opt_in=false` opts out of marketing.
- This endpoint is for the logged-in user's own settings page, not admin management.

## Public Unsubscribe FE Page

The campaign body unsubscribe link opens the FE route configured as `EMAIL_UNSUBSCRIBE_FRONTEND_URL`, usually:

```txt
https://surau.org/unsubscribe?token={token}
```

FE page behavior:

1. Read `token` from query string.
2. If token is missing, show an invalid-link state and do not call backend.
3. Call one of these public endpoints:

```http
GET /v1/email/unsubscribe?token={token}
POST /v1/email/unsubscribe
```

POST body:

```json
{ "token": "unsubscribe-token" }
```

4. On `200`, show unsubscribed success state.
5. On `400 invalid unsubscribe token`, show expired/invalid link state.
6. Do not require login.

Successful response:

```json
{
  "user_id": "user-id",
  "marketing_opt_in": false,
  "opted_out_at": "2026-06-05T01:02:03Z",
  "source": "unsubscribe_link",
  "created_at": "2026-06-01T00:00:00Z",
  "updated_at": "2026-06-05T01:02:03Z"
}
```

One-click unsubscribe headers:

- Real marketing campaign sends include `List-Unsubscribe: <https://api.surau.org/v1/email/unsubscribe?token=...>`.
- They also include `List-Unsubscribe-Post: One-Click`.
- These headers are set only when the recipient has a tokenized unsubscribe URL and `EMAIL_UNSUBSCRIBE_PUBLIC_URL` is configured.
- Transactional/auth/security email does not include unsubscribe headers.

## Cloudflare Bounce Webhook

This endpoint is for Cloudflare or backend operators, not FE UI.

```http
POST /v1/email/webhooks/cloudflare/bounces
cf-webhook-auth: {EMAIL_CLOUDFLARE_WEBHOOK_SECRET}
```

Behavior:

- No JWT.
- If `EMAIL_CLOUDFLARE_WEBHOOK_SECRET` is empty, endpoint returns `404`.
- Missing/wrong `cf-webhook-auth` returns `401`.
- Malformed JSON returns `400`.
- Valid JSON returns `202` even if all events are duplicates.
- Hard bounce and complaint events create delivery events, upsert `all` suppressions, and mark correlated messages/recipients failed when IDs are present.

Accepted payload shapes include:

```json
{ "permanent_bounces": ["user@example.com"] }
```

```json
{ "result": { "permanent_bounces": ["user@example.com"] } }
```

```json
{ "data": { "permanent_bounces": ["user@example.com"] } }
```

```json
{
  "events": [
    {
      "event_type": "bounce_hard",
      "recipient_email": "user@example.com",
      "message_id": "message-id",
      "campaign_id": "campaign-id",
      "campaign_recipient_id": "recipient-id",
      "reason": "permanent_bounce",
      "diagnostic": "550 5.1.1 user unknown",
      "occurred_at": "2026-06-05T01:02:03Z"
    }
  ]
}
```

Response:

```json
{
  "accepted": 1,
  "processed": 1,
  "suppressed": 1,
  "duplicates": 0
}
```

## Cloudflare Event Polling

This is backend-only async observability. FE only consumes the resulting delivery events and suppressions.

Config:

```env
EMAIL_CLOUDFLARE_EVENT_POLLING_ENABLED=true
EMAIL_CLOUDFLARE_ZONE_ID=...
EMAIL_CLOUDFLARE_ANALYTICS_API_TOKEN=...
EMAIL_CLOUDFLARE_EVENT_POLLING_INTERVAL=5m
EMAIL_CLOUDFLARE_EVENT_POLLING_LOOKBACK=30m
EMAIL_CLOUDFLARE_EVENT_POLLING_LIMIT=100
```

Behavior:

- Runs only when delivery mode is `cloudflare` and polling is enabled.
- Polls Cloudflare GraphQL Analytics `emailSendingAdaptive` for outbound `deliveryFailed` events.
- Stores cursor in `email_provider_poll_cursors`.
- Uses lookback overlap and delivery-event dedupe, so duplicate provider rows are safe.
- Records `bounce_hard`, creates `all` suppression with reason `permanent_bounce`, and marks local message failed when Cloudflare `messageId` is a local message UUID.

The analytics token must have GraphQL Analytics Read access for the Cloudflare zone.

## FE Screen Checklist

Template list/detail:

- Filter by category and archived state.
- Show languages with published/draft version states.
- Preview HTML/text before publish.
- Test send shows `202` and links to the message log row.

Transactional settings:

- List known seeded keys.
- Show `critical` keys as locked or warning.
- Support cooldown edit in seconds.

Campaign builder:

- Require marketing template.
- Validate audience and show preview count.
- Show UTC/local conversion for schedule time.
- Disable edit after leaving `draft`.
- Show send result counts from campaign metadata.
- Show retry failed only for `sent` campaigns with failed count > 0.

Observability:

- Message logs searchable by recipient email.
- Delivery events searchable by recipient/message/campaign.
- Campaign detail displays delivery-event summary.
- Delivery event detail can show raw provider payload in admin-only debug section.

Suppression:

- Show manual vs automated reasons.
- Warn that `all` blocks non-critical transactional and marketing email.
- Allow delete only with admin confirmation.

Public unsubscribe:

- No login required.
- Read `token` query param.
- Show success, invalid/expired, and network retry states.

## Common Error Handling

| HTTP | Typical error | FE action |
| --- | --- | --- |
| `400` | `invalid request body`, `invalid email template`, `invalid email campaign`, `invalid unsubscribe token` | Show validation/error state near form. |
| `401` | `unauthorized` | Ask user to login again, except webhook is backend-only. |
| `403` | role denied | Show admin access denied. |
| `404` | `not found` | Show missing/deleted item state. Webhook disabled also returns 404. |
| `429` | rate limited on auth/user email flows | Show wait/retry. |
| `503` | `email delivery failed` | Show provider delivery issue; allow retry/test later. |
| `500` | internal server error | Show generic error and capture `request_id`. |

## Deployment Notes For FE/QA

- Swagger is regenerated with `make swag-v1` when REST annotations/types change.
- These latest email batches add migrations that run automatically when the production container starts because Dockerfile builds with `-tags migrate`.
- Production deploy workflow runs on push to `main`, not merely PR creation.
- `docker-compose.prod.yml` must pass through all email env vars; verify server `.env.production` has values for unsubscribe and Cloudflare polling settings.
- Polling can stay disabled with `EMAIL_CLOUDFLARE_EVENT_POLLING_ENABLED=false`; webhook and sync hard-bounce handling still work.
