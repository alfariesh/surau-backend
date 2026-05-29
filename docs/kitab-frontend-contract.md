# Kitab Frontend Integration Contract

Last updated: 2026-05-29

This guide is the frontend-facing companion to `docs/kitab-multilingual-api.md`.
Use the backend `availability` objects as the source of truth for UI behavior.
Frontend code should not recreate fallback rules from raw fields alone.

## Core Rules

- Send `?lang=ar|id|en` on kitab catalog, TOC, read, section, playlist, RAG, and feedback calls.
- Empty backend `lang` defaults to `id`, but frontend should still send the selected app language explicitly.
- Region tags are accepted by the backend (`en-US -> en`, `id-ID -> id`, `ar-SA -> ar`).
- Unsupported explicit language returns `400 {"error":"unsupported language"}`.
- Catalog display can fall back to Arabic/source metadata.
- Section translation content is exact-language only. Missing `lang=en` returns `translation: null` even if `id` exists.
- Translation feedback is exact-language only. Do not show feedback controls unless `translation` is non-null and `translation.lang === selectedLang`.

## Minimal TypeScript Types

```ts
export type KitabLang = "ar" | "id" | "en";

export type AvailabilityAction =
  | "show_requested"
  | "show_arabic"
  | "offer_available_lang"
  | "hide_translation_tab"
  | "hide_audio";

export type AvailabilityReason =
  | "source_language"
  | "exact_available"
  | "arabic_fallback"
  | "alternative_langs_available"
  | "unavailable";

export type AvailabilityDecision = {
  action: AvailabilityAction;
  reason: AvailabilityReason;
  requested_lang: KitabLang;
  display_lang: KitabLang;
  is_fallback: boolean;
  missing: boolean;
  available_langs: KitabLang[];
};

export type ReaderAvailability = {
  title: AvailabilityDecision;
  translation: AvailabilityDecision;
  summary: AvailabilityDecision;
  audio: AvailabilityDecision;
};

export type LocalizationMeta = {
  requested_lang: KitabLang;
  display_lang: KitabLang;
  is_fallback: boolean;
  available_langs: KitabLang[];
  field_langs: Record<string, KitabLang>;
  availability: AvailabilityDecision;
};

export type SectionTranslation = {
  book_id: number;
  heading_id: number;
  lang: KitabLang;
  title: string | null;
  content: string;
  translation_status: string;
  updated_at: string;
};

export type BookTOCRead = {
  book_id: number;
  heading_id: number;
  title: string;
  requested_lang: KitabLang;
  title_lang: KitabLang;
  is_title_fallback: boolean;
  summary?: string | null;
  summary_lang?: KitabLang | null;
  has_summary: boolean;
  translation_missing: boolean;
  available_translation_langs: KitabLang[];
  available_summary_langs: KitabLang[];
  original_html: string;
  original_text: string;
  translation: SectionTranslation | null;
  audio: {
    lang: KitabLang;
    url: string;
    narrator?: string | null;
    duration_seconds?: number | null;
    mime_type?: string | null;
  } | null;
  availability: ReaderAvailability;
};
```

## Fetch Helpers

```ts
const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

export function toKitabLang(input: string | undefined | null): KitabLang {
  const primary = (input || "id").trim().toLowerCase().replace("_", "-").split("-")[0];

  if (primary === "ar" || primary === "id" || primary === "en") {
    return primary;
  }

  return "id";
}

async function getJSON<T>(path: string, token?: string): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, {
    headers: {
      Accept: "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
  });

  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `request failed: ${res.status}`);
  }

  return res.json() as Promise<T>;
}

export function getBook(bookId: number, lang: KitabLang) {
  return getJSON(`/v1/books/${bookId}?lang=${lang}`);
}

export function getBookTOC(bookId: number, lang: KitabLang) {
  return getJSON(`/v1/books/${bookId}/toc?lang=${lang}`);
}

export function readTOCSection(bookId: number, headingId: number, lang: KitabLang) {
  return getJSON<BookTOCRead>(`/v1/books/${bookId}/toc/${headingId}/read?lang=${lang}`);
}
```

## UI Decision Helpers

```ts
export function textDirection(lang: KitabLang): "rtl" | "ltr" {
  return lang === "ar" ? "rtl" : "ltr";
}

export function shouldRenderTranslationTab(read: BookTOCRead): boolean {
  const action = read.availability.translation.action;
  return action === "show_requested" || action === "offer_available_lang";
}

export function shouldRenderFeedback(read: BookTOCRead, selectedLang: KitabLang): boolean {
  return read.translation !== null && read.translation.lang === selectedLang;
}

export function languageOffer(decision: AvailabilityDecision): KitabLang | null {
  if (decision.action !== "offer_available_lang") {
    return null;
  }

  return decision.available_langs[0] ?? null;
}

export function labelForAvailability(decision: AvailabilityDecision): string | null {
  switch (decision.action) {
    case "show_requested":
      return null;
    case "show_arabic":
      return "Arabic source";
    case "offer_available_lang":
      return `Translation unavailable in ${decision.requested_lang.toUpperCase()}`;
    case "hide_translation_tab":
      return "Translation unavailable";
    case "hide_audio":
      return "Audio unavailable";
  }
}
```

## Reader Screen Flow

1. Keep one selected kitab language in app state: `ar`, `id`, or `en`.
2. Fetch book detail with `GET /v1/books/{book_id}?lang={selectedLang}`.
3. Fetch TOC with `GET /v1/books/{book_id}/toc?lang={selectedLang}`.
4. Fetch article body with `GET /v1/books/{book_id}/toc/{heading_id}/read?lang={selectedLang}`.
5. Render `original_html` as the source panel for all languages.
6. Render translated body only when `translation !== null`.
7. Use `availability.translation.action` for translation tab state.
8. Use `availability.audio.action` for player state.
9. Use `availability.summary.action` for summary visibility and empty state.
10. Show feedback only when `translation` exists for the selected language.

## Scenario Examples

### `lang=id`, Translation Exists

```json
{
  "requested_lang": "id",
  "title_lang": "id",
  "is_title_fallback": false,
  "translation_missing": false,
  "translation": {
    "lang": "id",
    "content": "Konten terjemahan Indonesia"
  },
  "availability": {
    "translation": {
      "action": "show_requested",
      "requested_lang": "id",
      "display_lang": "id",
      "is_fallback": false,
      "missing": false,
      "available_langs": ["id"]
    }
  }
}
```

Frontend behavior:

- Show translated title/body normally.
- Show feedback controls.
- Direction for translation panel is `ltr`.

### `lang=en`, Translation Missing But `id` Exists

```json
{
  "requested_lang": "en",
  "title_lang": "ar",
  "is_title_fallback": true,
  "translation_missing": true,
  "available_translation_langs": ["id"],
  "translation": null,
  "availability": {
    "title": {
      "action": "offer_available_lang",
      "requested_lang": "en",
      "display_lang": "ar",
      "is_fallback": true,
      "missing": true,
      "available_langs": ["id"]
    },
    "translation": {
      "action": "offer_available_lang",
      "requested_lang": "en",
      "display_lang": "ar",
      "is_fallback": true,
      "missing": true,
      "available_langs": ["id"]
    }
  }
}
```

Frontend behavior:

- Show Arabic/source title with an "Arabic source" or "EN unavailable" badge.
- Keep translated body empty because `translation` is `null`.
- Offer a language switch to `id`.
- Do not show translation feedback controls for `en`.

### `lang=en`, Translation Missing And No Alternative

```json
{
  "requested_lang": "en",
  "translation_missing": true,
  "available_translation_langs": [],
  "translation": null,
  "availability": {
    "translation": {
      "action": "hide_translation_tab",
      "requested_lang": "en",
      "display_lang": "ar",
      "is_fallback": true,
      "missing": true,
      "available_langs": []
    }
  }
}
```

Frontend behavior:

- Hide or disable the translation tab.
- Show source/original content as the readable fallback.
- Do not show language switch or feedback controls.

### `lang=ar`

```json
{
  "requested_lang": "ar",
  "title_lang": "ar",
  "is_title_fallback": false,
  "translation_missing": false,
  "translation": null,
  "availability": {
    "translation": {
      "action": "hide_translation_tab",
      "reason": "source_language",
      "requested_lang": "ar",
      "display_lang": "ar",
      "is_fallback": false,
      "missing": false,
      "available_langs": ["id"]
    }
  }
}
```

Frontend behavior:

- Render Arabic source as the primary content.
- Use `dir="rtl"` for source/title containers.
- Hide translation feedback controls.
- Hide translation tab unless the UI has a dedicated "Translations" comparison mode.

## Catalog Cards

Use `localization.availability` on `Category`, `Author`, and `Book`.

```ts
export function catalogBadge(localization: LocalizationMeta): string | null {
  const action = localization.availability.action;

  if (action === "show_requested") {
    return null;
  }

  if (action === "show_arabic") {
    return "Arabic source";
  }

  if (action === "offer_available_lang") {
    return `${localization.requested_lang.toUpperCase()} unavailable`;
  }

  return "Translation unavailable";
}
```

Recommended catalog behavior:

- Keep the book/category/author card visible even when requested catalog metadata is missing.
- Show Arabic/source text and a subtle badge.
- If `available_langs` is non-empty, offer a language switch in detail pages, not necessarily in every dense list row.
- Do not hide books only because translated metadata is missing.

## Audio Controls

```ts
export function audioState(read: BookTOCRead):
  | { kind: "playable"; url: string }
  | { kind: "offer-language"; lang: KitabLang }
  | { kind: "hidden" } {
  const decision = read.availability.audio;

  if (decision.action === "show_requested" && read.audio?.url) {
    return { kind: "playable", url: read.audio.url };
  }

  const offer = languageOffer(decision);
  if (offer) {
    return { kind: "offer-language", lang: offer };
  }

  return { kind: "hidden" };
}
```

## Summary Controls

```ts
export function summaryState(read: BookTOCRead):
  | { kind: "visible"; lang: KitabLang; text: string }
  | { kind: "offer-language"; lang: KitabLang }
  | { kind: "hidden" } {
  if (read.summary && read.summary_lang) {
    return { kind: "visible", lang: read.summary_lang, text: read.summary };
  }

  const offer = languageOffer(read.availability.summary);
  if (offer) {
    return { kind: "offer-language", lang: offer };
  }

  return { kind: "hidden" };
}
```

## Translation Feedback Guard

```ts
export function canSubmitTranslationFeedback(read: BookTOCRead, selectedLang: KitabLang): boolean {
  return read.translation !== null && read.translation.lang === selectedLang;
}
```

If `canSubmitTranslationFeedback` is false, do not call:

```txt
POST /v1/books/{book_id}/toc/{heading_id}/translation-feedback?lang={selectedLang}
```

The backend will return `404 translation not found` for a missing exact-language translation.

## Admin Missing Assets Screen

Admin UI can use:

```txt
GET /v1/admin/reader/missing-assets?target_lang=en&asset_type=section_translation&book_id=797
```

Recommended admin columns:

- `asset_type`
- `target_lang`
- `book_id`
- `book_title`
- `heading_id`
- `heading_title`
- `category_name`
- `author_name`
- `available_langs`
- `source_updated_at`

Use `counts` for tabs or badges:

```ts
type MissingAssetCount = {
  asset_type:
    | "book_metadata"
    | "category_metadata"
    | "author_metadata"
    | "section_translation"
    | "heading_summary"
    | "section_audio";
  target_lang: "id" | "en";
  total: number;
};
```

## Quran Reader Parity

Quran now follows the same language resolver as kitab:

```txt
GET /v1/quran/surahs/73?lang=en
GET /v1/quran/translation-sources?lang=en
GET /v1/quran/surahs/73/ayahs?lang=en&include_translation=true
GET /v1/books/797/quran-references?lang=en
```

Frontend rules:

- Always render Arabic Quran text from `text_qpc_hafs` or `text_imlaei_simple`.
- Treat `translation` as exact-language only; if it is `null`, use `availability.translation`.
- Use `available_translation_langs` to offer another language.
- For `lang=ar`, hide the translation tab when `availability.translation.action === "hide_translation_tab"`.
- Use `GET /v1/quran/translation-sources?lang={lang}` to populate source pickers; `lang=ar` returns an empty list.

```ts
type QuranAyah = {
  ayah_key: string;
  text_qpc_hafs?: string;
  text_imlaei_simple?: string;
  translation: QuranTranslation | null;
  requested_lang: KitabLang;
  available_translation_langs: KitabLang[];
  translation_missing: boolean;
  availability: {
    translation: AvailabilityDecision;
    audio: AvailabilityDecision;
  };
};

export function quranTranslationState(ayah: QuranAyah) {
  const decision = ayah.availability.translation;
  if (ayah.translation) {
    return { kind: "show", text: ayah.translation.text };
  }
  const offer = languageOffer(decision);
  if (offer) {
    return { kind: "offer-language", lang: offer };
  }
  return { kind: "hidden" };
}
```

Admin Quran gaps use:

```txt
GET /v1/admin/quran/missing-assets?target_lang=en&asset_type=ayah_translation&surah_id=73
```

Supported `asset_type`: `surah_info`, `ayah_translation`, `translation_source`, `audio_public`.

## Recommended Component Split

- `LanguageSwitcher`: owns selected `KitabLang`.
- `CatalogFallbackBadge`: reads `localization.availability`.
- `ReaderTitle`: reads `title_lang`, `is_title_fallback`, and `availability.title`.
- `SourcePanel`: always renders `original_html`.
- `TranslationPanel`: renders only when `translation` exists or `availability.translation.action === "offer_available_lang"`.
- `SummaryPanel`: uses `summaryState`.
- `AudioPlayer`: uses `audioState`.
- `TranslationFeedback`: guarded by `canSubmitTranslationFeedback`.

## QA Checklist

- `lang=id` with complete assets shows requested text, summary, audio, and feedback.
- `lang=en` with missing translation shows `translation: null`, offers `id`, and hides feedback.
- `lang=en` with no alternatives hides translation/audio surfaces cleanly.
- `lang=ar` renders source content as primary and hides feedback.
- Unsupported language shows a recoverable error and resets frontend state to `id` or the previous valid language.
- Catalog cards remain visible even when requested metadata falls back to Arabic/source.
