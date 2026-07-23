# Push notifications v1 — iOS

Status: backend contract for Surau iOS (`org.surau.app`). OneSignal App ID:
`7a650cae-1c1e-4b19-a7fe-393c14b894f0`.

## Identity route

`POST /v1/me/push/identity-token` requires a valid Bearer access token and an active refresh-session
family. It accepts no request body and no `external_id` parameter. The backend reads the exact
`UserAccount.id` and current `token_version` from authenticated server state.

Sanitized response:

```json
{
  "schema_version": "surau.push.identity.v1",
  "identity_token": "<ES256-JWT>",
  "external_id": "11111111-1111-4111-8111-111111111111",
  "owner_binding": "ob1.<opaque>",
  "expires_at": "2026-07-23T08:15:00Z",
  "expires_in": 900,
  "eligible_intents": ["notify_khatam_milestones"]
}
```

The JWT uses ES256. `iss` is the exact OneSignal App ID, `exp` is 15 minutes after issue time, and
`identity.external_id` is the authenticated backend UUID. The key is loaded from a read-only
KMS/secret-manager-mounted file configured by `ONESIGNAL_IDENTITY_PRIVATE_KEY_FILE`; it is not the
APNs `.p8` key and is never returned or logged.

iOS requests a fresh token on login and whenever the OneSignal SDK reports token invalidation or
expiry. There is no refresh token for this credential. Issuance fails with 401 when its session
family has been revoked, expired, logged out, deleted, or replaced by another account. Maximum
configured TTL is one hour.

## Targeting and eligibility

Backend delivery uses `POST /notifications`, server-only App API Key, `target_channel: "push"`,
and `include_aliases.external_id`. The App API Key is never exposed to iOS.

This OneSignal app has a fail-closed server allowlist:

- allowed: `khatam_milestone`, `khatam_completed`, both under product category
  `notify_khatam_milestones`;
- denied: `streak_reminder`, `new_login`, and every unknown category.

Denied categories are not submitted to OneSignal, including durable retries created before this
policy. iOS is not responsible for patching `notify_streak_reminders=false`.

## String-only notification data schema

Every value in OneSignal `data` is a string. Unknown schema versions or intents open Home.

Public deep link fixture:

```json
{
  "schema_version": "surau.push.v1",
  "scope": "public",
  "intent": "open_quran_ayah",
  "ayah_key": "2:255"
}
```

Personal same-owner fixture:

```json
{
  "schema_version": "surau.push.v1",
  "scope": "personal",
  "category": "notify_khatam_milestones",
  "intent": "open_khatam_progress",
  "owner_binding": "ob1.<opaque>"
}
```

`POST /v1/me/push/resolve` validates the data against the current authenticated account and active
session. Personal payloads with a missing/stale/mismatched `owner_binding`, unknown schema, or
unknown intent return:

```json
{"destination":"home"}
```

A matching payload returns:

```json
{"destination":"intent","intent":"open_khatam_progress"}
```

The binding is HMAC-SHA256 over the backend UUID and `token_version`, using a dedicated secret.
Thus A → switch to B → tap A resolves to Home without exposing either UUID in the payload.

## Controlled acceptance matrix

Use two non-production-content test accounts A and B; UUIDs and JWTs must stay out of screenshots,
test logs, and tickets.

1. Public fixture opens the public ayah while logged out or logged in.
2. Personal fixture generated for A opens A's khatam progress while A is active.
3. Deliver to A, switch the same device to B, then tap A's notification: Home.
4. Deliver with delay, then revoke consent/session before arrival: token renewal is rejected and
   tap resolution is Home.

## Retention, deletion, and audit

- OneSignal message/API data: provider default 30 days; do not place sensitive content in title,
  body, or `data`.
- OneSignal user/subscription data: retain only while the Surau account is active.
- Account deletion/DSAR: revoke Surau sessions and token generation immediately, enqueue provider
  deletion by `external_id`, call OneSignal Delete User, and verify asynchronously until View User
  returns 404. Retry with bounded backoff; alert after 24 hours and retain status evidence for 90
  days.
- Audit evidence contains timestamp, operation, HTTP status, attempt count, request ID, and
  `HMAC-SHA256(audit-key, external_id)` only. Never store raw UUID, JWT, private key, or App API Key
  in audit/log output.

Durable provider erasure sudah terintegrasi dengan delete account. Lifecycle, alarm, retensi, dan
query evidence tersanitasi ada di [OneSignal provider erasure](onesignal-erasure.md). Identity baru
boleh diaktifkan di production setelah acceptance staging pada runbook tersebut menghasilkan
DELETE `202|404`, View User `404`, dan evidence tanpa UUID/JWT mentah.

## Deployment secrets

```text
ONESIGNAL_ENABLED=true
ONESIGNAL_APP_ID=7a650cae-1c1e-4b19-a7fe-393c14b894f0
ONESIGNAL_IDENTITY_ENABLED=true
ONESIGNAL_IDENTITY_PRIVATE_KEY_FILE=/run/secrets/onesignal-identity-es256.pem
ONESIGNAL_IDENTITY_TOKEN_TTL=15m
ONESIGNAL_OWNER_BINDING_SECRET=<secret-manager-value-min-32-bytes>
ONESIGNAL_ERASURE_ENABLED=true
ONESIGNAL_ERASURE_SECRET=<dedicated-secret-manager-value-min-32-bytes>
```

`ONESIGNAL_REST_API_KEY` is also server-only. None of these secret values may be committed.
