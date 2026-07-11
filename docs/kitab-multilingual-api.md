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
  "original_html": "<p dir=\"rtl\" lang=\"ar\">...</p>",
  "original_text": "...",
  "original_format": "plain_text",
  "original_blocks": [
    {
      "type": "paragraph",
      "text": "...",
      "html": "<p dir=\"rtl\" lang=\"ar\">...</p>"
    }
  ],
  "original_footnotes": [
    {
      "marker": "(¬١)",
      "text": "...",
      "html": "<li data-marker=\"(¬١)\">...</li>"
    }
  ]
}
```

`original_html` is always sanitized reader HTML. If the imported kitab source is plain text, the backend derives semantic paragraphs, best-effort Quran quote citations, and separate footnotes while preserving `original_text` for plain-text fallback/search previews. The section page range remains explicit via `start_page_id` and `end_page_id`; pass `include_quran_references=true` to embed approved structured Quran references for the heading.

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

### Reader Publication Status Fields

`publication_status` is the legacy catalog/source publication status from `book_publications`. New clients should read the explicit fields:

- `catalog_publication_status` / `catalog_published`: whether the Arabic/source catalog book is visible in the public reader.
- `production_workflow_status`, `production_publication_status`, `production_published`: status of the matching `book_id + lang` production project for `lang=id|en`.
- `production_status`: compact frontend state. `candidate` means no active production project exists for the requested target language; `published` means the target-language production project is public.

Reader stats have `scope="catalog_global"` because they describe the published source catalog, not the current list filter/page. They keep `published_count` as the legacy catalog count and add:

- `catalog_published_count`: source catalog books published via `book_publications`.
- `production_published_count`: target-language production projects published via `book_production_projects`.
- `coverage_count`: alias for target-language production coverage in the published source catalog.

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

## Editorial Book Production Workflow

Translation production is scoped to `book_id + lang` and target languages are `id` and `en` for v1. Runtime editorial flow reads raw kitab data from Postgres tables (`books`, `book_pages`, `book_headings`); SQLite import and CLI tools are not part of the editor/admin user flow.

Core endpoints require editor or admin role:

- `GET /v1/editorial/production-candidates?lang=&q=&category_id=&author_id=&has_content=&unstarted=&limit=&offset=`
- `GET /v1/editorial/production-dashboard?lang=&activity_limit=`
- `GET /v1/editorial/production-activity?lang=&limit=&offset=`
- `POST /v1/editorial/production-projects`
- `GET /v1/editorial/production-projects?book_id=&lang=&workflow_status=&publication_status=&ready_to_publish=&needs_work=&limit=&offset=`
- `GET /v1/editorial/production-projects/{id}`
- `PATCH /v1/editorial/production-projects/{id}`
- `GET /v1/editorial/production-projects/{id}/workspace`
- `GET /v1/editorial/production-projects/{id}/completeness`
- `GET /v1/editorial/production-projects/{id}/publish-check`
- `GET /v1/editorial/production-projects/{id}/activity?limit=&offset=`
- `GET /v1/editorial/production-projects/{id}/draft-revisions?asset_type=&heading_id=&limit=&offset=`
- `POST /v1/editorial/production-projects/{id}/draft-revisions/{revision_id}/restore`
- `GET|PUT|DELETE /v1/editorial/production-projects/{id}/metadata-draft`
- `GET|PUT|DELETE /v1/editorial/production-projects/{id}/author-draft`
- `GET|PUT|DELETE /v1/editorial/production-projects/{id}/category-draft`
- `GET|PUT|DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/translation-draft`
- `GET|PUT|DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/summary-draft`
- `GET|PUT|DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/audio-draft`
- `POST /v1/editorial/production-projects/{id}/review`

Admin-only endpoints:

- `POST /v1/editorial/production-projects/{id}/publish`
- `POST /v1/editorial/production-projects/{id}/unpublish`
- `DELETE /v1/editorial/production-projects/{id}/final-assets/{asset_type}`
- `DELETE /v1/editorial/production-projects/{id}/toc/{heading_id}/final-assets/{asset_type}`

Completeness is book-level. The backend requires metadata, author/category drafts where the raw book has them, section translation for every active heading, heading summary for every active heading, and audio for every active heading only when `requires_audio=true`. If `requires_review=true`, required draft assets must be approved before publish.

`GET /workspace` is the recommended editor screen bootstrap call. It returns the project, source book, completeness, scalar asset statuses, and per-TOC draft/final flags in one payload.

Use `GET /production-candidates` to choose raw kitab for a target language; `unstarted=true` hides books that already have an active project for that `book_id + lang`. Use `ready_to_publish=true` or `needs_work=true` on the project list for a small production queue.

Production project responses expose both `owner_id` and optional `owner` (`id`, `email`, `display_name`) so queues and workspace settings can show human-readable assignees without an admin user lookup.

Every successful production draft save creates an immutable draft revision. Use `GET /draft-revisions` to inspect history for one asset and `POST /restore` to roll back a snapshot into the active draft.

Text draft responses for metadata, author, category, section translation, and heading summary
also expose `provenance_class` plus optional typed
`generation: {run_id,model_id,prompt_version}`. On the first save, the backend inherits the run
when a machine final asset already exists for the same project target; otherwise the new draft is
`editorial` with no generation. A machine-derived draft remains `machine` through editing and
human review; approval does not erase its run. Revision snapshots and restore preserve this attribution, and publish
copies it unchanged to final reader tables. Existing rows whose origin cannot be proven are
`legacy_unknown`, never retroactively assigned a model or prompt. See
[`docs/generation-runs.md`](generation-runs.md).

Use `GET /publish-check` for a read-only publish validator that mirrors publish readiness and returns structured blocking errors. Use `GET /activity` to render the project timeline for create/update, draft save/delete/restore, review, publish, unpublish, and final asset soft-delete events.

Public reader behavior for `lang=ar` is unchanged. For `lang=id|en`, final translation/audio/summary data is exposed only when the matching production project has `publication_status=published`; otherwise reader responses safely fall back to Arabic/source content or omit the unpublished asset.

Catalog gaps are computed only from published reader books. Category and author gaps include categories/authors referenced by published books. Section gaps are computed from non-deleted headings in published books.

## Frontend Guidance

- If `localization.is_fallback=true`, show a small "Arabic source" or "translation unavailable" label instead of pretending the text is in the selected language.
- If `translation_missing=true` and `available_translation_langs` is non-empty, offer a switch to one of those languages.
- Prefer the nested `availability` action for UI behavior; keep legacy fields such as `translation_missing`, `title_lang`, and `summary_lang` for display copy and analytics.
- For `lang=ar`, render `original_html` as the primary body and hide the translation feedback UI.
