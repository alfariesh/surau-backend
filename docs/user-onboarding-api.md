# User Onboarding and Preferences API

Last updated: 2026-05-29

This document defines the frontend contract for user enrichment after auth.
Register stays intentionally small: `username`, `email`, and `password`.
User product metadata lives in `user_profiles` and `user_preferences`, not in
the auth-focused `users` table.

## Core Rules

- Supported content languages are `ar`, `id`, and `en`.
- Empty or missing language preference defaults to `id`.
- Region tags normalize to primary language: `en-US -> en`, `id-ID -> id`, `ar-SA -> ar`.
- Unsupported explicit languages return `400 {"error":"unsupported language"}`.
- Authenticated reader clients should use `profile.preferences.preferred_content_lang`.
- Explicit per-screen language switches still win over saved preference for that screen.
- Guest clients should use saved local preference, then browser language, then `id`.

Language precedence:

```text
explicit screen language -> user preferred_content_lang -> local/browser language -> id
```

## Data Types

```ts
export type ContentLang = "ar" | "id" | "en";

export type ArabicLevel =
  | "none"
  | "basic"
  | "intermediate"
  | "advanced"
  | "native";

export type ReaderMode =
  | "arabic_translation"
  | "translation_only"
  | "arabic_only";

export type UserProfile = {
  user_id: string;
  display_name?: string | null;
  timezone?: string | null;
  country_code?: string | null;
  onboarding_version: number;
  onboarding_completed_at?: string | null;
  personalization_enabled: boolean;
  created_at: string;
  updated_at: string;
};

export type UserPreferences = {
  user_id: string;
  preferred_ui_lang: ContentLang;
  preferred_content_lang: ContentLang;
  fallback_langs: ContentLang[];
  arabic_level: ArabicLevel;
  reader_mode: ReaderMode;
  interests: string[];
  daily_goal_minutes?: number | null;
  quran_translation_source_id?: string | null;
  quran_recitation_id?: string | null;
  created_at: string;
  updated_at: string;
};

export type UserAccount = {
  id: string;
  username: string;
  email: string;
  role: "user" | "editor" | "admin" | string;
  email_verified: boolean;
  created_at: string;
  updated_at: string;
  profile: UserProfile;
  preferences: UserPreferences;
  onboarding_required: boolean;
};
```

Canonical interests accepted by the backend:

```ts
export type UserInterest =
  | "adab"
  | "aqidah"
  | "arabic_language"
  | "fiqh"
  | "hadith"
  | "learn_kitab"
  | "memorization"
  | "murottal"
  | "quran_daily"
  | "research"
  | "sirah"
  | "tafsir";
```

The backend also accepts Indonesian aliases for some interests, such as
`hadis -> hadith`, `fikih -> fiqh`, `bahasa_arab -> arabic_language`,
`quran_harian -> quran_daily`, and `riset -> research`.

## Profile Bootstrap

Call profile after login and on app startup when a token exists.

```http
GET /v1/user/profile
Authorization: Bearer <token>
```

Success `200`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "username": "ahmad",
  "email": "ahmad@example.com",
  "role": "user",
  "email_verified": true,
  "created_at": "2026-05-29T08:00:00Z",
  "updated_at": "2026-05-29T08:00:00Z",
  "profile": {
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "onboarding_version": 1,
    "personalization_enabled": true,
    "created_at": "2026-05-29T08:00:00Z",
    "updated_at": "2026-05-29T08:00:00Z"
  },
  "preferences": {
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "preferred_ui_lang": "id",
    "preferred_content_lang": "id",
    "fallback_langs": ["id"],
    "arabic_level": "none",
    "reader_mode": "arabic_translation",
    "interests": [],
    "created_at": "2026-05-29T08:00:00Z",
    "updated_at": "2026-05-29T08:00:00Z"
  },
  "onboarding_required": true
}
```

Frontend behavior:

- If `onboarding_required` is `true`, show onboarding after login/session bootstrap.
- Use `preferences.preferred_content_lang` for Quran/kitab requests.
- Keep existing top-level user fields compatible with older UI state.

## Complete Onboarding

```http
PATCH /v1/user/onboarding
Authorization: Bearer <token>
Content-Type: application/json
```

Request:

```json
{
  "display_name": "Ahmad",
  "timezone": "Asia/Jakarta",
  "country_code": "ID",
  "personalization_enabled": true,
  "preferred_ui_lang": "id",
  "preferred_content_lang": "id",
  "fallback_langs": ["id", "en"],
  "arabic_level": "basic",
  "reader_mode": "arabic_translation",
  "interests": ["quran_daily", "tafsir", "hadith"],
  "daily_goal_minutes": 15,
  "quran_translation_source_id": "qul-kfgqpc-id-simple",
  "quran_recitation_id": "mishari-rashid-alafasy"
}
```

Success `200` returns `UserAccount` with:

- `profile.onboarding_completed_at` set.
- `onboarding_required: false`.
- normalized preferences.

Minimal valid request:

```json
{
  "preferred_content_lang": "id"
}
```

If optional fields are omitted:

- `preferred_ui_lang` keeps current value, default `id`.
- `preferred_content_lang` keeps current value, default `id`.
- `fallback_langs` defaults to `[preferred_content_lang]`.
- `arabic_level` keeps current value, default `none`.
- `reader_mode` keeps current value, default `arabic_translation`.
- `interests` defaults to `[]`.

## Update Preferences

Use this after onboarding for settings screens.

```http
PATCH /v1/user/preferences
Authorization: Bearer <token>
Content-Type: application/json
```

Request fields are all optional:

```json
{
  "preferred_content_lang": "en",
  "fallback_langs": ["en", "id"],
  "arabic_level": "intermediate",
  "reader_mode": "translation_only",
  "interests": ["research", "fiqh"],
  "daily_goal_minutes": 20,
  "quran_translation_source_id": "quran-en-sahih",
  "quran_recitation_id": "rec-default"
}
```

Success `200` returns the updated `UserAccount`.

Notes:

- This endpoint does not mark onboarding complete.
- Use `PATCH /v1/user/onboarding` for the first-run flow.
- To clear optional string fields, send an empty string; backend stores it as null.

## Error Handling

All errors use:

```json
{"error":"message"}
```

Important errors:

| Status | Error | FE behavior |
| --- | --- | --- |
| `400` | `unsupported language` | Reset to previous valid language or `id`. |
| `400` | `invalid user preference` | Keep form open and highlight invalid selection. |
| `400` | `invalid request body` | Fix request shape/client validation. |
| `401` | `missing authorization header` | Clear token and redirect login. |
| `401` | `invalid or expired token` | Clear token and redirect login. |
| `404` | `user not found` | Clear token and redirect login. |
| `500` | `internal server error` | Show retry UI. |

## Frontend Flow

```ts
async function bootstrapSession(token: string | null): Promise<UserAccount | null> {
  if (!token) return null;

  try {
    const account = await getProfile(token);
    setContentLang(account.preferences.preferred_content_lang);

    if (account.onboarding_required) {
      showOnboarding();
    }

    return account;
  } catch (error) {
    clearToken();
    return null;
  }
}
```

After onboarding:

```ts
const account = await completeOnboarding({
  preferred_content_lang: selectedLang,
  arabic_level: selectedArabicLevel,
  reader_mode: selectedReaderMode,
  interests: selectedInterests,
});

setContentLang(account.preferences.preferred_content_lang);
hideOnboarding();
```

When loading reader data:

```ts
const lang =
  explicitScreenLang ??
  account?.preferences.preferred_content_lang ??
  localOrBrowserLang() ??
  "id";

await fetch(`/v1/books/${bookId}/toc?lang=${lang}`);
await fetch(`/v1/quran/surahs/${surahId}/ayahs?lang=${lang}&include_translation=true`);
```

## QA Checklist

- New registered user has `preferred_content_lang: "id"` and `onboarding_required: true`.
- `PATCH /v1/user/onboarding` with `preferred_content_lang: "id"`, `"en"`, or `"ar"` succeeds.
- `PATCH /v1/user/onboarding` with `preferred_content_lang: "fr"` returns `400 unsupported language`.
- `GET /v1/user/profile` still exposes top-level user fields for older UI code.
- Completing onboarding flips `onboarding_required` to `false`.
- Updating preferences changes future Quran/kitab default `lang`.
- Explicit screen `?lang=` still overrides saved preference.
