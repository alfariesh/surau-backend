# Admin Email API

This is the frontend contract for admin-managed transactional and marketing email.
All admin endpoints require a bearer token with role `admin`.

## Concepts

Supported template languages are `id`, `en`, and `ar`.

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

Transactional templates should have published `id`, `en`, and `ar` versions. Marketing templates require `id`; `en` and `ar` are optional but recommended.

Template variables use Go template syntax, for example `{{.name}}` and `{{.link}}`. Verification templates also receive `{{.otp}}` and `{{.otp_duration}}`. The backend validates `required_variables` before previewing or sending.

## Admin Templates

List templates:

```http
GET /v1/admin/emails/templates?q=&category=transactional&include_archived=false&limit=50&offset=0
```

Create template:

```http
POST /v1/admin/emails/templates
Content-Type: application/json

{
  "key": "weekly_digest",
  "name": "Weekly Digest",
  "category": "marketing",
  "critical": false,
  "enabled": true
}
```

`enabled` defaults to `true` when omitted.

Template metadata:

```http
GET /v1/admin/emails/templates/{id}
PATCH /v1/admin/emails/templates/{id}
DELETE /v1/admin/emails/templates/{id}
```

Create a localized version:

```http
POST /v1/admin/emails/templates/{id}/versions
Content-Type: application/json

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

Version actions:

```http
GET /v1/admin/emails/templates/{id}/versions
PATCH /v1/admin/emails/versions/{version_id}
POST /v1/admin/emails/versions/{version_id}/publish
```

Preview and test send:

```http
POST /v1/admin/emails/templates/{id}/preview
POST /v1/admin/emails/templates/{id}/test-send
```

Body:

```json
{
  "lang": "id",
  "to": "admin@example.com",
  "variables": {
    "name": "Admin",
    "link": "https://surau.org"
  }
}
```

The preview response is:

```ts
type EmailPreview = {
  subject: string;
  html: string;
  text: string;
  lang: "id" | "en" | "ar";
};
```

## Transactional Settings

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

Critical auth link events cannot be disabled by admin toggles. Security notification events are best effort and may be disabled.

## Logs And Suppression

Message log:

```http
GET /v1/admin/emails/messages?category=marketing&status=failed&email=user@example.com&limit=50&offset=0
```

Sensitive message metadata such as `link`, `otp`, `token`, `unsubscribe_url`, and URL values with a `token` query parameter are stored as `[redacted]`.

Suppression list:

```http
GET /v1/admin/emails/suppressions?email=user@example.com&scope=marketing&limit=50&offset=0
POST /v1/admin/emails/suppressions
DELETE /v1/admin/emails/suppressions/{id}
```

Create suppression body:

```json
{
  "email": "user@example.com",
  "scope": "marketing",
  "reason": "manual"
}
```

Use `scope=marketing` to block campaigns only, or `scope=all` to block all non-critical sends.

## Campaigns

Campaigns use marketing templates and one provider send per recipient, never BCC. Audience generation only includes users with explicit marketing opt-in and excludes suppressed emails.

Create or update draft:

```http
POST /v1/admin/emails/campaigns
PATCH /v1/admin/emails/campaigns/{id}
```

Body:

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

Campaign endpoints:

```http
GET /v1/admin/emails/campaigns?status=draft&limit=50&offset=0
GET /v1/admin/emails/campaigns/{id}
POST /v1/admin/emails/campaigns/{id}/preview-audience
POST /v1/admin/emails/campaigns/{id}/test-send
POST /v1/admin/emails/campaigns/{id}/schedule
POST /v1/admin/emails/campaigns/{id}/send-now
POST /v1/admin/emails/campaigns/{id}/retry-failed
POST /v1/admin/emails/campaigns/{id}/cancel
```

Schedule body:

```json
{
  "scheduled_at": "2026-06-01T09:00:00Z"
}
```

Campaign statuses are `draft`, `scheduled`, `sending`, `sent`, and `cancelled`.
Recipient statuses are `pending`, `sent`, `failed`, and `skipped`.
After send, campaign `metadata` includes `delivery_total`, `delivery_sent`, `delivery_failed`, `delivery_skipped`, and `delivery_finished_at`.
`retry-failed` is valid only for `sent` campaigns and retries only recipients whose campaign-recipient status is currently `failed`.

## User Preferences And Unsubscribe

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

Setting `marketing_opt_in=true` also removes the user's `marketing/unsubscribe` suppression entry when one exists.

Public unsubscribe endpoints for FE pages:

```http
GET /v1/email/unsubscribe?token={token}
POST /v1/email/unsubscribe
```

POST body:

```json
{
  "token": "unsubscribe-token"
}
```

Successful unsubscribe returns the updated `EmailSubscription`.

Marketing campaign sends include `List-Unsubscribe` and `List-Unsubscribe-Post: One-Click` only when the recipient has a tokenized unsubscribe URL and `EMAIL_UNSUBSCRIBE_PUBLIC_URL` points to the public backend endpoint, for example `https://api.surau.org/v1/email/unsubscribe`.

## Cloudflare Bounce Webhook

```http
POST /v1/email/webhooks/cloudflare/bounces
```

Header:

```http
cf-webhook-auth: {EMAIL_CLOUDFLARE_WEBHOOK_SECRET}
```

The endpoint is disabled with `404` when `EMAIL_CLOUDFLARE_WEBHOOK_SECRET` is empty. Valid hard bounce or complaint events create an audit row, upsert an `all` suppression, and return:

```json
{
  "accepted": 1,
  "processed": 1,
  "suppressed": 1
}
```

## Cloudflare Event Polling

The backend can also poll Cloudflare GraphQL Analytics for Email Service `deliveryFailed` events via `emailSendingAdaptive`. Enable it with:

```env
EMAIL_CLOUDFLARE_EVENT_POLLING_ENABLED=true
EMAIL_CLOUDFLARE_ZONE_ID=...
EMAIL_CLOUDFLARE_ANALYTICS_API_TOKEN=...
```

The analytics token must have GraphQL Analytics Read access for the zone. Polling uses a cursor with a lookback overlap, records `bounce_hard` delivery events, upserts `all` suppressions, and marks local messages failed when Cloudflare's `messageId` matches a local message UUID.
