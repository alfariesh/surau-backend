# Kitab Multilingual API Contract

Last updated: 2026-05-29

This document defines the public multilingual contract for kitab/catalog reader APIs.

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
  "translation": null,
  "original_html": "<p>...</p>"
}
```

## Endpoint Behavior

- `GET /v1/categories?lang=...`, `/v1/authors`, `/v1/books`, and `/v1/books/{book_id}` display exact requested catalog translations when available; otherwise they display Arabic/source metadata with `localization.is_fallback=true`.
- `GET /v1/books/{book_id}` includes `language_coverage` for available section translations, summaries, and audio counts. Book list responses omit this detail.
- `GET /v1/books/{book_id}/toc?lang=...` uses translated section titles only when an exact requested-language section translation title exists.
- `GET /v1/books/{book_id}/toc/{heading_id}/read?lang=...` keeps `translation=null` when exact requested-language content is unavailable and exposes `available_translation_langs`.
- `POST /v1/books/{book_id}/toc/{heading_id}/translation-feedback?lang=...` remains exact-language only and returns `404 translation not found` if that section translation does not exist.
- `POST /v1/books/{book_id}/rag?lang=...` validates the same language contract and includes `requested_lang` in the response.

## Frontend Guidance

- If `localization.is_fallback=true`, show a small "Arabic source" or "translation unavailable" label instead of pretending the text is in the selected language.
- If `translation_missing=true` and `available_translation_langs` is non-empty, offer a switch to one of those languages.
- For `lang=ar`, render `original_html` as the primary body and hide the translation feedback UI.
