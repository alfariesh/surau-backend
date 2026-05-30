# Kitab Multilingual API Contract

Last updated: 2026-05-29

This document defines the public multilingual contract for kitab/catalog reader APIs.
For frontend implementation examples, TypeScript helpers, and UI branching rules, see `docs/kitab-frontend-contract.md`.
For a shared kitab + Quran integration entrypoint, see `docs/frontend-integration-contract.md`.

## Language Rules

- Supported query languages: `ar`, `id`, `en`.
- Empty `lang` defaults to `id`.
- Region tags normalize to the primary language, for example `en-US -> en`, `id-ID -> id`, and `ar-SA -> ar`.
- Unsupported explicit languages return:

```json
{"error":"unsupported language"}
```

## Fallback Policy

The backend does not automatically fall back from one translated language to another translated language. If a user requests `en` and only `id` exists, the response uses Arabic/source text for display fields and returns metadata so the frontend can offer the available language.

Catalog fallback example:

```json
{
  "id": 797,
  "name": "الزبد في مصطلح الحديث",
  "localization": {
    "requested_lang": "en",
    "display_lang": "ar",
    "is_fallback": true,
    "available_langs": ["id"],
    "field_langs": {
      "name": "ar",
      "description": "ar"
    },
    "availability": {
      "action": "offer_available_lang",
      "reason": "alternative_langs_available",
      "requested_lang": "en",
      "display_lang": "ar",
      "is_fallback": true,
      "missing": true,
      "available_langs": ["id"]
    }
  }
}
```

Section fallback example:

```json
{
  "book_id": 797,
  "heading_id": 10,
  "title": "النوع الأول: الصحيح",
  "requested_lang": "en",
  "title_lang": "ar",
  "is_title_fallback": true,
  "translation_missing": true,
  "available_translation_langs": ["id"],
  "availability": {
    "title": {
      "action": "offer_available_lang",
      "reason": "alternative_langs_available",
      "requested_lang": "en",
      "display_lang": "ar",
      "is_fallback": true,
      "missing": true,
      "available_langs": ["id"]
    },
    "translation": {
      "action": "offer_available_lang",
      "reason": "alternative_langs_available",
      "requested_lang": "en",
      "display_lang": "ar",
      "is_fallback": true,
      "missing": true,
      "available_langs": ["id"]
    },
    "summary": {
      "action": "show_requested",
      "reason": "exact_available",
      "requested_lang": "en",
      "display_lang": "en",
      "is_fallback": false,
      "missing": false,
      "available_langs": ["en", "id"]
    },
    "audio": {
      "action": "hide_audio",
      "reason": "unavailable",
      "requested_lang": "en",
      "display_lang": "ar",
      "is_fallback": false,
      "missing": true,
      "available_langs": []
    }
  },
  "translation": null,
  "original_html": "<p>...</p>"
}
```

Availability actions:

- `show_requested`: render the requested-language asset.
- `show_arabic`: render Arabic/source text.
- `offer_available_lang`: requested asset is missing, but `available_langs` can be offered as an explicit switch.
- `hide_translation_tab`: translation/summary UI has no useful translated asset for this request.
- `hide_audio`: audio UI has no playable exact-language asset.

## Endpoint Behavior

- `GET /v1/categories?lang=...`, `/v1/authors`, `/v1/books`, and `/v1/books/{book_id}` display exact requested catalog translations when available; otherwise they display Arabic/source metadata with `localization.is_fallback=true`.
- `GET /v1/books/{book_id}` includes `language_coverage` for available section translations, summaries, and audio counts. Book list responses omit this detail.
- `GET /v1/books/{book_id}/toc?lang=...` uses translated section titles only when an exact requested-language section translation title exists.
- `GET /v1/books/{book_id}/toc/{heading_id}/read?lang=...` keeps `translation=null` when exact requested-language content is unavailable and exposes `available_translation_langs`.
- TOC, section, and read responses include `availability.title`, `availability.translation`, `availability.summary`, and `availability.audio` so the frontend can choose tabs, empty states, language switch offers, and labels without duplicating backend fallback rules.
- `POST /v1/books/{book_id}/toc/{heading_id}/translation-feedback?lang=...` remains exact-language only and returns `404 translation not found` if that section translation does not exist.
- `POST /v1/books/{book_id}/rag?lang=...` validates the same language contract and includes `requested_lang` in the response.

## Editorial Missing Reader Assets Queue

`GET /v1/editorial/reader/missing-assets` requires editor or admin role and exposes reader localization gaps. It does not generate translations, summaries, or audio; it only reports missing assets for editorial tooling.

Query parameters:

- `target_lang`: optional `id` or `en`; empty means both `id,en`. `ar` returns `400 {"error":"unsupported language"}` because Arabic is source content.
- `asset_type`: optional one of `book_metadata`, `category_metadata`, `author_metadata`, `section_translation`, `heading_summary`, `section_audio`.
- `book_id`: optional published book filter.
- `limit`: optional, default `50`.
- `offset`: optional, default `0`.

Response:

```json
{
  "items": [
    {
      "asset_type": "section_translation",
      "target_lang": "en",
      "book_id": 797,
      "book_title": "الزبد في مصطلح الحديث",
      "heading_id": 10,
      "heading_title": "النوع الأول: الصحيح",
      "category_id": 1,
      "category_name": "مصطلح الحديث",
      "author_id": 2,
      "author_name": "مؤلف",
      "available_langs": ["id"],
      "source_updated_at": "2026-01-01T00:00:00Z"
    }
  ],
  "total": 42,
  "counts": [
    {"asset_type": "section_translation", "target_lang": "en", "total": 20}
  ]
}
```

Catalog gaps are computed only from published reader books. Category and author gaps include categories/authors referenced by published books. Section gaps are computed from non-deleted headings in published books.

## Frontend Guidance

- If `localization.is_fallback=true`, show a small "Arabic source" or "translation unavailable" label instead of pretending the text is in the selected language.
- If `translation_missing=true` and `available_translation_langs` is non-empty, offer a switch to one of those languages.
- Prefer the nested `availability` action for UI behavior; keep legacy fields such as `translation_missing`, `title_lang`, and `summary_lang` for display copy and analytics.
- For `lang=ar`, render `original_html` as the primary body and hide the translation feedback UI.
