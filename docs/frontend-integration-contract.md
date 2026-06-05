# Frontend Integration Contract

Last updated: 2026-05-31

This is the main FE integration entrypoint for kitab reader and Quran reader.
Use it together with:

- `docs/mobile-backend-integration-guide.md` for the mobile app implementation roadmap, screen flows, caching, and FE module guidance.
- `docs/user-onboarding-api.md` for profile, onboarding, and saved language preferences.
- `docs/admin-email-api.md` for admin email templates, campaigns, opt-in, unsubscribe, and delivery logs.
- `docs/kitab-multilingual-api.md` for kitab API details.
- `docs/kitab-frontend-contract.md` for kitab TypeScript helpers and UI branching.
- `docs/quran-api.md` for Quran endpoint details, response shapes, and smoke tests.
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
4. `GET /v1/books/{book_id}/quran-references?lang={lang}&status=approved` when the screen shows Quran references.

Kitab display rules:

- Always keep `original_html` or Arabic/source content renderable.
- Treat `original_html` as sanitized reader HTML. If the imported source was plain text, the backend wraps it into semantic HTML and sets `original_format="plain_text"`.
- Use `original_blocks` and `original_footnotes` when a screen needs paragraph-level rendering, footnote drawers, or Quran quote highlighting without reparsing HTML.
- Render `translation.content` only when `translation !== null`.
- For `lang=ar`, render Arabic/source as primary and hide feedback controls.
- Show translation feedback only when `translation !== null && translation.lang === selectedLang`.
- Use `availability.title`, `availability.translation`, `availability.summary`, and `availability.audio` for tabs, badges, empty states, and language offers.
- Use `localization.availability` on category, author, and book cards. Do not hide catalog rows only because requested metadata is missing.

## Quran Reader Flow

Use this order for a Quran reader screen:

1. `GET /v1/quran/surahs?lang={lang}` for the surah index.
2. `GET /v1/quran/surahs/{surah_id}?lang={lang}` when the header/info panel needs background HTML.
3. `GET /v1/quran/translation-sources?lang={lang}` if the UI exposes translation source selection.
4. `GET /v1/quran/recitations` if audio is enabled or the UI exposes reciter selection.
5. `GET /v1/quran/surahs/{surah_id}/ayahs?lang={lang}&include_translation=true&include_audio={boolean}&recitation_id={optional}&view=reader_minimal` for the main reader.

Quran display rules:

- Always render Arabic Quran text from `text_qpc_hafs`, with `text_imlaei_simple` as fallback.
- Render `translation.text` only when `translation !== null`.
- Prefer `view=reader_minimal` for ayah list reader bodies; it omits search/import/debug fields and exposes audio as one playable `audio[].url`.
- `lang=ar` returns Arabic-only mode: translation is `null` and `availability.translation.action` is `hide_translation_tab`.
- Missing `lang=en` translation returns `translation: null` and `available_translation_langs` tells FE whether to offer `id`.
- `BookQuranReference.ayahs[]` uses the same `QuranAyah` metadata contract.
- Search may match Arabic, requested translation, or another imported translation, but result display still follows exact requested-language rules.

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

All API errors use:

```json
{"error":"message"}
```

FE handling:

- `400 unsupported language`: reset to previous valid language or `id`.
- `400 invalid include_audio/include_translation/include_info`: fix caller query construction.
- `404 quran recitation not found`: clear saved recitation preference.
- `404 quran translation source not found`: clear saved source preference for that language.
- `404 translation not found` on kitab feedback: hide feedback because exact requested translation is missing.
- `500 internal server error`: show retry UI and keep previous content if cached.
- Onboarding/profile errors follow the same `{ "error": "message" }` shape.

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
