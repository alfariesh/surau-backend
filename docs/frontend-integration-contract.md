# Frontend Integration Contract

Last updated: 2026-07-13

This is the main FE integration entrypoint for kitab reader and Quran reader.
Use it together with:

- `docs/mobile-backend-integration-guide.md` for the mobile app implementation roadmap, screen flows, caching, and FE module guidance.
- `docs/user-onboarding-api.md` for profile, onboarding, and saved language preferences.
- `docs/admin-email-api.md` for admin email templates, campaigns, opt-in, unsubscribe, and delivery logs.
- `docs/kitab-multilingual-api.md` for kitab API details.
- `docs/kitab-frontend-contract.md` for kitab TypeScript helpers and UI branching.
- `docs/quran-api.md` for Quran endpoint details, response shapes, and smoke tests.
- `docs/anchors.md` for the normative cross-corpus Anchor grammar and resolver response.
- `docs/cross-references.md` for generic incoming/outgoing content links and Quran bridge behavior.
- `/swagger/index.html` on a running backend for the generated OpenAPI reference.

## Shared Language Contract

Supported languages are `ar`, `id`, and `en`.

Rules:

- Logged-in FE should read the default language from `GET /v1/user/profile` at `preferences.preferred_content_lang`.
- Guest FE should use local saved language, then browser `navigator.language`, then `id`.
- Explicit per-screen language selection overrides saved profile preference for that screen.
- FE should always send `?lang={selectedLang}`.
- Empty backend `lang` defaults to `id`, but FE should not rely on that for user-selected screens.
- Region tags normalize to the primary language: `en-US -> en`, `id-ID -> id`, `ar-SA -> ar`.
- Unsupported explicit languages return `400 {"error":"unsupported language"}`.
- There is no automatic `en -> id` fallback for translated content.
- Arabic/source content remains the canonical readable fallback.

Minimal FE normalizer:

```ts
export type ContentLang = "ar" | "id" | "en";

export function normalizeContentLang(input: string | null | undefined): ContentLang {
  const primary = (input || "id").trim().toLowerCase().replace("_", "-").split("-")[0];
  return primary === "ar" || primary === "id" || primary === "en" ? primary : "id";
}
```

Recommended language resolver:

```ts
export function selectedContentLang(input: {
  explicitLang?: string | null;
  profileLang?: string | null;
  localLang?: string | null;
  browserLang?: string | null;
}): ContentLang {
  return normalizeContentLang(
    input.explicitLang ||
      input.profileLang ||
      input.localLang ||
      input.browserLang ||
      "id",
  );
}
```

## Shared List Envelope

Every user-facing list endpoint returns the same envelope (breaking change from the earlier mix of bare arrays and bespoke keys like `books`, `results`, `references`, `authors`, `pages`):

```json
{
  "items": [],
  "total": 42
}
```

- Covers categories, authors, books, pages, headings, TOC, Quran surahs/recitations/translation-sources/juz/hizbs/ayah lists, Quran search, book Quran references, generic Cross-References, and the `/v1/me` lists (progress, saved items, saved-item tags, surah progress, khatam history).
- `GET /v1/books` keeps its `stats` sibling next to `items` and `total`.
- `GET /v1/cross-references` keeps an additive `work_total` sibling for distinct opposing Works.
- TOC items still nest `children` inside each item.
- For paginated lists, `total` is the unbounded match count; for full lists it equals `items.length`.
- Object endpoints (detail pages, audio manifests, `/v1/me/sync` snapshot, activity, profile) are unchanged.
- **Frozen legacy exceptions (F1-D):** eight pre-existing list envelopes keep
  their bespoke keys forever (contract-tested backend-side): admin users
  `{users,total}`, admin activity `{activity,total}`, editorial production
  `{projects,total}` / `{candidates,total}` / `{events,total}` /
  `{revisions,total}` (2×), translation feedback `{feedbacks,total}`. Every
  NEW list endpoint must use literal `items` + `total`.

## Shared Anchor Resolution

Use `GET /v1/anchors/resolve` when opening a persisted/shared content address. New cross-content
links should store a canonical Anchor; existing FE keys remain valid permanently:

```text
?anchor=kitab%2F797%2Fh%2F11%2Fu%2F42  canonical kitab unit
?anchor=quran%2F73%3A4                  canonical Quran ayah
?anchor=quran%2F73%3A4%2Fu%2F2         canonical Quran rendering/footnote unit
?anchor=73%3A4                          legacy ayah_key
?surah_id=73&from_ayah_number=1&to_ayah_number=4 legacy Quran range
?juz_number=29                          legacy Quran juz
?hizb_number=57                         legacy Quran hizb
?page_number=574                        legacy mushaf page
?anchor=toc-11&book_id=797              legacy TOC
?book_id=797&page_id=12                 legacy physical page
```

The object response contains `requested`, nullable `canonical_anchor`, and one boundary for a
point or two (`start`, `end`) for a range. Each boundary contains the requested point's `status`,
every `active_targets[]`, and the complete `redirect_chain[]`. Never assume one input has exactly
one target: an edited unit can split and a page normally maps to several units. A known
`tombstoned` Anchor still returns `200` and may have an empty target list.

Navigate using each target's `navigation_url`, but retain `canonical_anchor`/`unit_id` as the
precise identity because the existing reader URL can be heading- or page-level. Treat `400
invalid_anchor` as a bad client/deep-link shape and `404 anchor_not_found` as unavailable public
content. The endpoint supports `ETag`/`If-None-Match`, but is intentionally absent from the edge
worker cache allowlist. See `docs/anchors.md` for the exact grammar, TypeScript wire shape, range
rules, lifecycle semantics, and cache contract.

Untuk target ayah Q-2, simpan juga `primary_unit_id`/`primary_unit_anchor` bila tersedia. Nilai
absen berarti surah itu belum backfill atau sedang stale menunggu reconcile, bukan izin bagi FE
untuk membuat ID sendiri.
Reader translation/transliteration sekarang selalu membawa `source_name`, atribusi pihak
bertanggung jawab/penerjemah, `license_status`, dan identitas unit. Footnote terstruktur berada di
`footnote_units[]` dengan `parent_unit_id` ke translation; field `footnotes` mentah tetap ada untuk
kompatibilitas. Endpoint `GET /v1/quran/pages/{page_number}/ayahs` membuka locator halaman lama.
Seluruh `/v1/quran` bypass cache Worker dan memakai revalidation origin agar takedown lisensi
tidak tertahan salinan edge.

## User Bootstrap and Onboarding

After login or app startup with a stored token:

1. Call `GET /v1/user/profile`.
2. Store the returned `UserAccount`.
3. Use `account.preferences.preferred_content_lang` as the default Quran/kitab language.
4. If `account.onboarding_required === true`, show onboarding before sending the user deep into reader flows.
5. Save first-run answers with `PATCH /v1/user/onboarding`.
6. Save later settings changes with `PATCH /v1/user/preferences`.

Profile response includes top-level user fields plus:

```ts
type UserAccount = {
  id: string;
  username: string;
  email: string;
  role: string;
  email_verified: boolean;
  profile: UserProfile;
  preferences: UserPreferences;
  onboarding_required: boolean;
};
```

See `docs/user-onboarding-api.md` for full request/response shapes, accepted
interests, and QA scenarios.

## Authentication and Account Email

Email addresses are case-insensitive. The backend trims and lowercases the email on every auth endpoint that accepts one, and stores and returns it in lowercase.

- Trim and lowercase the email client-side before sending it to `POST /v1/auth/register`, `POST /v1/auth/login`, `POST /v1/auth/forgot-password`, `POST /v1/auth/resend-verification`, the email-OTP form of `POST /v1/auth/verify-email`, and `POST /v1/auth/change-email/request`. This keeps client state consistent with what the backend stores.
- Login succeeds regardless of the case the user typed: `John@Example.com` and `john@example.com` resolve to the same account.
- Expect every `email` field in responses (profile, register, account) to be lowercase. Compare against the API value, not the raw user input.
- Failed login and registering an already-used email return a generic error and never reveal whether an email exists. Do not branch UI on account existence.
- Auth endpoints are rate limited per client IP and per email/account. On `429 too many auth attempts`, show a retry-later state and back off; do not retry immediately.
- Verification and reset emails are queued and delivered asynchronously by a background dispatcher (typically within ~15-30 seconds), so tell the user the email may take up to ~30 seconds to arrive. `503 email delivery failed` now only means the backend could not durably queue the message; email-provider outages no longer fail signup or add latency.
- Browser clients: the API serves CORS itself for origins listed in the backend `CORS_ALLOWED_ORIGINS` env (dev defaults allow `http://localhost:3000` and `http://localhost:3005`); a web frontend on another origin must be added there. `AllowCredentials` is `false` — auth is pure Bearer token, never cookies — so do not send `credentials: "include"`. Allowed request headers are `Authorization`, `Content-Type`, `If-Match`, and `X-Request-ID`; exposed response headers are `ETag`, `Retry-After`, and `X-Request-ID`.

Request/response shapes and status codes are unchanged; see `/swagger/index.html` on a running backend for full auth schemas.

## Email Preferences

Use `GET /v1/user/email-preferences` and `PATCH /v1/user/email-preferences` for the user's marketing opt-in state. Marketing email is strict opt-in only.

Unsubscribe pages can call `GET /v1/email/unsubscribe?token={token}` or `POST /v1/email/unsubscribe` with `{ "token": "..." }`. Admin email CRUD, campaigns, suppressions, and message logs are documented in `docs/admin-email-api.md`.

## Availability Contract

Use backend `availability` objects for UI state. Do not infer UI behavior only from nullable fields.

```ts
export type AvailabilityAction =
  | "show_requested"
  | "show_arabic"
  | "offer_available_lang"
  | "hide_translation_tab"
  | "hide_audio";

export type AvailabilityDecision = {
  action: AvailabilityAction;
  reason:
    | "source_language"
    | "exact_available"
    | "arabic_fallback"
    | "alternative_langs_available"
    | "unavailable";
  requested_lang: ContentLang;
  display_lang: ContentLang;
  is_fallback: boolean;
  missing: boolean;
  available_langs: ContentLang[];
};
```

Recommended UI mapping:

| Action | FE behavior |
| --- | --- |
| `show_requested` | Render requested-language asset. |
| `show_arabic` | Render Arabic/source content with source label if useful. |
| `offer_available_lang` | Render source content and offer an explicit language switch from `available_langs`. |
| `hide_translation_tab` | Hide or disable translation UI. |
| `hide_audio` | Hide or disable audio UI. |

## Kitab Reader Flow

Use this order for a reader screen:

1. `GET /v1/books/{book_id}?lang={lang}` for title, metadata, and `language_coverage`.
2. `GET /v1/books/{book_id}/toc?lang={lang}` for navigation tree.
3. `GET /v1/books/{book_id}/toc/{heading_id}/read?lang={lang}` for current section body.
4. `GET /v1/books/{book_id}/quran-references?lang={lang}` when the screen shows Quran references. The server always returns approved-only; the legacy `status` query cannot expose editorial states.

Kitab display rules:

- Always keep `original_html` or Arabic/source content renderable.
- Treat `original_html` as sanitized reader HTML. If the imported source was plain text, the backend wraps it into semantic HTML and sets `original_format="plain_text"`.
- Use `original_blocks` and `original_footnotes` when a screen needs paragraph-level rendering, footnote drawers, or Quran quote highlighting without reparsing HTML.
- Render `translation.content` only when `translation !== null`.
- For `lang=ar`, render Arabic/source as primary and hide feedback controls.
- Show translation feedback only when `translation !== null && translation.lang === selectedLang`.
- Use `availability.title`, `availability.translation`, `availability.summary`, and `availability.audio` for tabs, badges, empty states, and language offers.
- Use `localization.availability` on category, author, and book cards. Do not hide catalog rows only because requested metadata is missing.

## Book-RAG Citation Contract (K-1)

`POST /v1/books/{book_id}/rag?lang={lang}` tetap mempertahankan seluruh locator sitasi lama.
K-1 hanya menambah identitas Citable Unit secara opsional:

```ts
export type BookRAGCitation = {
  ref: string;
  book_id: number;
  heading_id: number;
  heading_title: string;
  page_id: number;
  printed_page?: string | null;
  part?: string | null;
  anchor: string;       // tetap anchor legacy, mis. toc-11
  url: string;          // tetap URL reader legacy
  quote: string;
  unit_id?: string;
  unit_anchor?: string; // Anchor kanonik K-1 bila mapping exact berhasil
};
```

Aturan konsumsi:

- Jangan mengartikan `anchor` sebagai `unit_anchor`; semantik field lama tidak berubah.
- Bila `unit_anchor` hadir, simpan sebagai identitas presisi dan resolve lewat
  `GET /v1/anchors/resolve?anchor=...` saat perlu mengikuti edit/split/merge. Tetap gunakan `url`
  untuk navigasi reader saat ini.
- Field unit absen hanya pada mode `legacy` atau fallback satu-request penuh bertipe
  `incomplete|stale`; pada dua kondisi itu tetap render kartu sitasi legacy.
- Mapping quote nol/ganda/lintas unit pada mode `dual` yang sudah lengkap adalah pelanggaran
  parity: backend menggagalkan request (SSE mengirim event `error`) dan tidak mengirim kartu
  parsial atau locator tebakan. Tampilkan state gagal/coba lagi dan jangan menyusun locator di FE.
- JSON dan SSE memakai objek yang sama. Event `citations` mengirim `BookRAGCitation[]`; event
  `done` mengirim respons final lengkap.
- `include_trace=true` dapat menambah `citation_mode`, `legacy_fallback`, dan
  `fallback_reason="incomplete|stale"`. Ini diagnostik saja. Fallback selalu satu request penuh,
  jadi FE tidak perlu menyatukan dua set bukti.
- Field lama dipertahankan minimal 90 hari. Jangan membuat UUID/Anchor unit di FE.

Endpoint RAG adalah POST dinamis dan tetap bypass cache edge. Detail request/SSE ada di
[`docs/mobile-backend-integration-guide.md`](mobile-backend-integration-guide.md) §Kitab Reader.

## Quran Reader Flow

Use this order for a Quran reader screen:

1. `GET /v1/quran/surahs?lang={lang}` for the surah index.
2. `GET /v1/quran/surahs/{surah_id}?lang={lang}` when the header/info panel needs background HTML.
3. `GET /v1/quran/translation-sources?lang={lang}` if the UI exposes translation source selection.
4. `GET /v1/quran/recitations` if audio is enabled or the UI exposes reciter selection.
5. `GET /v1/quran/surahs/{surah_id}/ayahs?lang={lang}&include_translation=true&include_audio={boolean}&recitation_id={optional}&view=reader_minimal` for the main reader.

Jika state FE berasal dari halaman mushaf lama, langkah 5 dapat langsung memakai
`GET /v1/quran/pages/{page_number}/ayahs?...`; tidak perlu menebak surah/range di client.

Quran display rules:

- Always render Arabic Quran text from `text_qpc_hafs`, with `text_imlaei_simple` as fallback.
- Render `translation.text` only when `translation !== null`.
- Tampilkan nama sumber/penerjemah dari objek translation/transliteration dan pertahankan
  `unit_id`/`anchor` untuk sitasi. `footnote_units[]` sudah tertaut ke unit terjemahan lewat
  `parent_unit_id`; jangan parse ulang nomor footnote bila bentuk ini tersedia. Jika teks legacy
  tampil tanpa `unit_id`, jangan jadikan sitasi sampai reconcile selesai.
- Prefer `view=reader_minimal` for ayah list reader bodies; it omits search/import/debug fields and exposes audio as one playable `audio[].url`.
- `lang=ar` returns Arabic-only mode: translation is `null` and `availability.translation.action` is `hide_translation_tab`.
- Missing `lang=en` translation returns `translation: null` and `available_translation_langs` tells FE whether to offer `id`.
- `BookQuranReference.ayahs[]` uses the same `QuranAyah` metadata contract.
- Search may match Arabic, requested translation, or another imported translation, but result display still follows exact requested-language rules.
- Surah and ayah `editorial` is public only when the backend row is both
  `published` and `license_status=permitted`. Do not infer draft/review state
  from a missing public field; protected workflow state never appears here.

## Quran Audio Sync

Audio URLs:

- Use `public_url ?? audio_url` as the playable URL.
- Prefer `public_url` when present because it is the app-owned CDN URL.
- Do not require `public_url` for local/dev playback. Imported source `audio_url` is a valid playable fallback.
- `r2_key` is storage metadata, not a browser URL.

Default recitation:

- `GET /v1/quran/recitations` marks at most one `is_default=true`.
- Default eligibility uses `has_playable_audio=true`.
- A track is playable when it has `public_url` or source `audio_url`.
- If `include_audio=true` and `recitation_id` is omitted, ayah endpoints use the backend default.

Minimal audio helpers:

```ts
export type QuranAudioSegment = {
  segment_index: number;
  ayah_key: string;
  timestamp_from_ms: number;
  timestamp_to_ms: number;
  duration_ms?: number;
};

export type QuranAudioTrack = {
  recitation_id: string;
  track_type: "ayah" | "surah" | string;
  track_key: string;
  audio_url?: string | null;
  public_url?: string | null;
  segments?: QuranAudioSegment[];
};

export function playableQuranAudioURL(track: QuranAudioTrack): string | null {
  return track.public_url || track.audio_url || null;
}

export function segmentForAyah(track: QuranAudioTrack, ayahKey: string): QuranAudioSegment | null {
  return track.segments?.find((segment) => segment.ayah_key === ayahKey) ?? null;
}
```

Playback rules:

- For `track_type="ayah"`, play the track URL for that ayah. If segments are present, use them to drive highlight/progress.
- For `track_type="surah"`, play the full-surah URL and use `segments` to seek/highlight each ayah.
- Segment timestamps are milliseconds.
- If a stored `recitation_id` returns `404 quran recitation not found`, clear it and retry without `recitation_id`.

## Error Handling

All API errors use the standard envelope (F1-D — `error` is kept for old
clients; branch on `code` or, primarily, the HTTP status):

```json
{
  "error": "message",
  "code": "machine_code",
  "message": "message",
  "details": "optional instance-specific detail",
  "retry_after": 60,
  "request_id": "uuid"
}
```

Contract guarantees (F1-D, contract-tested backend-side):

- **`code` values are FROZEN.** Fixing an error sentence's wording can never
  change its `code` (`internal/controller/restapi/apierror/registry.go` is
  the frozen table; a contract test blocks unregistered message edits).
  Branch on `code`, never on the `error`/`message` text.
- Every error shape carries `code` + `request_id` — including the rich 409
  editorial envelopes (`existing_project_id`, publish-blocked), rate-limiter
  429s (`too many requests`, with `retry_after` mirroring the `Retry-After`
  header), unmatched-route 404s, and framework errors (413 etc. — there
  `request_id` may be empty).
- Include `request_id` in bug reports: it links directly to backend logs and
  traces.
- Variable human detail (e.g. template validation specifics) rides in
  `details`; `error`/`code` stay fixed.

FE handling:

- `400 unsupported language` (code `unsupported_language`): reset to previous valid language or `id`.
- `400 invalid include_audio/include_translation/include_info`: fix caller query construction.
- `404 quran recitation not found`: clear saved recitation preference.
- `404 quran translation source not found`: clear saved source preference for that language.
- `404 translation not found` on kitab feedback: hide feedback because exact requested translation is missing.
- `429 too many auth attempts` (code `AUTH_RATE_LIMITED`): auth rate limit hit (per client IP and per email/account). Back off; honor `retry_after`.
- `429 too many requests` (code `too_many_requests`): non-auth rate limiter (RAG/search/personal/editorial/session). Back off; honor `retry_after`.
- `409 license not permitted` (code `license_not_permitted`): keep the Quran
  draft private and disable publish until its license is `permitted`.
- `412 precondition failed` (code `precondition_failed`): another Quran
  editorial write won; refetch the workspace and show a conflict.
- `428 if-match header required` (code `if_match_header_required`): fix the
  mutation caller to send the workspace ETag (or an intentional `*`).
- `500 internal server error`: show retry UI and keep previous content if cached.

## Public Cache Contract (F1-D/B-4/Q-2)

Stable category and author GET endpoints answer with `Cache-Control: public, max-age=300,
stale-while-revalidate=86400`, a weak body-hash `ETag`, and `Last-Modified`. Those numbers equal
the edge Worker TTLs (`workers/api-cache/wrangler.jsonc` — FRESH 300 / STALE 86400);
`CACHE_VERSION` remains their mass-invalidation source of truth.

License-sensitive public GETs under `/v1/books`, `/v1/quran`, `/v1/anchors`, dan
`/v1/cross-references` instead
answer `Cache-Control: public, max-age=0, must-revalidate` and are always `BYPASS` at the edge
Worker. Browsers may retain their ETag/body, but must send a conditional request before reuse;
`If-None-Match` can still produce a `304`. This guarantees that a license takedown is checked by
the backend rather than hidden behind a fresh or stale edge copy.

`GET /v1/quran/search` remains dynamic and answers `Cache-Control: no-store`; prefix Quran lain
tetap revalidate, bukan edge-cache.

## Editorial Gap Queues

Reader gaps:

```text
GET /v1/editorial/reader/missing-assets?target_lang=en&asset_type=section_translation&book_id=797
```

Quran gaps:

```text
GET /v1/editorial/quran/missing-assets?target_lang=en&asset_type=ayah_translation&surah_id=73
```

Notes:

- Editorial endpoints require bearer auth with editor or admin role.
- Empty `target_lang` means `id,en`.
- `target_lang=ar` returns `400` because Arabic is source content.
- Quran `audio_public` means tracks missing app-owned `public_url`; they may still be playable from source `audio_url`.

## Editorial Quran Workflow

Use the protected workflow below for both surah and ayah copy. Editors with
editorial review capability can load/save/inspect/restore; publishing requires
production publish capability plus fresh MFA.

```text
GET  /v1/editorial/quran/surahs/{surah_id}?lang=id
PUT  /v1/editorial/quran/surahs/{surah_id}/draft?lang=id
POST /v1/editorial/quran/surahs/{surah_id}/publish?lang=id
GET  /v1/editorial/quran/surahs/{surah_id}/draft-revisions?lang=id&limit=50&offset=0
POST /v1/editorial/quran/surahs/{surah_id}/draft-revisions/{revision_id}/restore?lang=id

GET  /v1/editorial/quran/ayahs/{ayah_key}?lang=id
PUT  /v1/editorial/quran/ayahs/{ayah_key}/draft?lang=id
POST /v1/editorial/quran/ayahs/{ayah_key}/publish?lang=id
GET  /v1/editorial/quran/ayahs/{ayah_key}/draft-revisions?lang=id&limit=50&offset=0
POST /v1/editorial/quran/ayahs/{ayah_key}/draft-revisions/{revision_id}/restore?lang=id
```

The workspace response is `{ "draft": ..., "published": ... }`. Store its
`ETag`, then send that value as `If-Match` on every save, publish, or restore.
Use the new ETag returned after each successful mutation. Missing headers return
`428`; malformed/stale values return `412`. Refetch and present a conflict—do
not silently overwrite. `If-Match: *` is reserved for an explicit first or
force write.

Revision lists use `{ "items": [...], "total": number }`. `origin=rest`
identifies API edits, `origin=import` identifies importer/baseline writes, and
`origin=restore` identifies restored snapshots. An effective restore creates a
new draft revision and never publishes it; restoring a snapshot already
identical to the draft is a no-op. Publish is permitted only for
`license_status=permitted`; public Quran remains sourced exclusively from the
published + permitted slot.

Surah revision snapshots cover the per-language editorial row. Global routing
fields (`slug`, `chronological_order`, `ruku_count`) change only during an
explicit importer publish; Q-4 owns their permanent redirect/history contract.

## Editorial Book Production

Use `GET /v1/editorial/production-candidates?lang=id|en&unstarted=true` to let admin/editor pick a raw kitab. Candidate rows include heading/page counts and existing project status for the selected language.

Use `POST /v1/editorial/production-projects` to create a `book_id + lang` project from an existing raw kitab. Editors can manage metadata, author, category, per-TOC translation, per-TOC summary, and optional per-TOC audio drafts, then submit/approve/reject via `POST /v1/editorial/production-projects/{id}/review`.

Duplicate active `book_id + lang` creates return `409` with `existing_project_id` when available, so route the editor to that project instead of asking them to search manually.

Use `GET /v1/editorial/production-projects/{id}/workspace` to load the editor screen. It includes the source book, TOC headings, draft status, final asset flags, and completeness in one response.

Use `GET /v1/editorial/production-projects?ready_to_publish=true` or `?needs_work=true` for a lightweight production queue. The two flags are mutually exclusive.

Production project payloads keep `owner_id` and also include `owner` when the assigned owner still exists: `{ "id", "email", "display_name" }`. Use it for queue and workspace display labels instead of showing raw UUIDs.

Use `GET /v1/editorial/production-dashboard?lang=id|en` for the small-team operational summary: unstarted candidates, active projects, needs work, ready to publish, published count, and recent production events. Use `GET /v1/editorial/production-activity?lang=&limit=&offset=` when you need a global activity feed outside a single project.

Use `GET /v1/editorial/production-projects/{id}/draft-revisions?asset_type=...&heading_id=...` to show draft history. Use `POST /v1/editorial/production-projects/{id}/draft-revisions/{revision_id}/restore` to roll back; restore creates a new revision and resets the draft review status.

Use `GET /v1/editorial/production-projects/{id}/publish-check` before enabling publish UX. It mirrors backend publish readiness and includes structured blockers. If publish is still blocked, the `409` publish response includes the same `blocking_errors` payload. Use `GET /v1/editorial/production-projects/{id}/activity` for the project timeline.

Admin-only actions are publish, unpublish, and final asset soft-delete. Reader pages for `lang=id|en` only expose final assets after the matching project is published, so frontend can rely on public reader responses as the source of truth for what is visible.

For public kitab reader lists, do not treat legacy `publication_status` as target-language production status. It is the source catalog status. Prefer `catalog_published`, `production_published`, and `production_status`; stats likewise distinguish legacy `published_count`/`catalog_published_count` from `production_published_count`. Reader stats include `scope="catalog_global"` and are not scoped to the current list filter/page.

## Admin User Management

Use `GET /v1/admin/users?q=&role=&email_verified=&limit=&offset=` for the admin user list. The response is `{ "users": UserAccount[], "total": number }`. `GET /v1/admin/users?role=editor` doubles as the editor lookup for production project owner assignment.

Use `GET /v1/admin/users/{id}` for detail and `GET /v1/admin/users/{id}/activity` for role-change audit history. Activity rows include who changed the role (`actor_id`, `actor_email`), the previous role, the new role, and the timestamp.

## FE QA Checklist

- `lang=id` complete kitab section shows requested title/body/summary/audio and feedback.
- `lang=en` missing kitab section translation returns `translation: null`, offers `id` when available, and hides feedback.
- `lang=ar` kitab renders Arabic/source content as primary.
- Quran `lang=id` renders Arabic plus Indonesian translation.
- Quran `lang=en` renders English translation when imported; if missing, translation is `null` with availability metadata.
- Quran `lang=ar` renders Arabic-only mode and hides translation UI.
- Quran `include_audio=true` without `recitation_id` returns default audio when `has_playable_audio=true`.
- Quran audio player uses `public_url ?? audio_url` and segment timestamps in milliseconds.
- Unsupported `lang=fr` is handled as a recoverable client state error.
- New user profile returns `preferred_content_lang=id` and `onboarding_required=true`.
- Completing onboarding flips `onboarding_required=false` and updates subsequent default reader language.
- Registering `User@Example.com` then logging in with `user@example.com` succeeds (email is case-insensitive), and `email` in responses comes back lowercased.
