# Quran API Contract

Last updated: 2026-06-12

This document is the FE-facing contract for the Quran backend. It covers the public Quran read APIs, response shapes, audio behavior, errors, and the recommended fetch flow. The Quran domain is standalone: Quran rows live in dedicated Quran tables and are linked to kitab data only through Quran reference records.

For the shared kitab + Quran FE integration guide, see `docs/frontend-integration-contract.md`.

## Quick FE Rules

- Always render Arabic Quran text from `text_qpc_hafs`, with `text_imlaei_simple` as fallback.
- Always send `?lang=ar|id|en` from FE state.
- Treat `translation` and `info` as exact-language only. Do not render Indonesian text as English fallback.
- Use `availability.translation` and `available_translation_langs` for missing translation empty states and language offers.
- For audio playback, use `public_url ?? audio_url`. `public_url` is preferred, but source `audio_url` is a valid playable fallback.
- Segment timestamps are milliseconds.
- When audio is enabled, fetch `/v1/quran/recitations`, use `is_default=true` initially, then store the user's chosen `recitation_id`.
- Use `/v1/quran/juz` or `/v1/quran/hizbs` for mushaf navigation tabs; use their `/ayahs` endpoints for the actual reader payload.

## Base Contract

- Base path: `/v1`
- Auth: public Quran endpoints do not require bearer auth.
- Content type: JSON.
- List endpoints return a uniform envelope `{"items": [...], "total": <int>}` (breaking change from earlier bare arrays and bespoke keys like `results`/`references`). For paginated lists `total` is the unbounded match count; for full lists it equals `items.length`. Object endpoints (single surah, single ayah, audio manifest) are unchanged.
- Default language: `id`.
- Default translation source: language-specific. For Indonesian the preferred default is `qul-kfgqpc-id-simple` when imported; for other languages the backend chooses the highest-coverage source deterministically.
- Canonical ayah key: `{surah_id}:{ayah_number}`, for example `73:4`.
- Surah IDs are numeric `1` through `114`.
- Ayah numbers are numeric inside each surah.
- Juz numbers are `1` through `30`; hizb numbers are `1` through `60`.
- Quran text is imported from QUL files. The app does not call QUL at request time.

## Data Model Notes

### Text Fields

`QuranAyah` can contain multiple text forms:

| Field | Meaning | FE usage |
| --- | --- | --- |
| `text_qpc_hafs` | QPC Hafs display script | Main Arabic display text. |
| `text_imlaei_simple` | Imlaei/simple script | Search-friendly plain Arabic display/fallback. |
| `search_text` | Backend search normalization helper | Usually not needed for visual UI. |
| `translation.text` | Exact requested-language translation from selected/default source | Translation body. |
| `transliteration.text` | Exact requested-language transliteration, when imported for `id` or `en` | Latin/romanized reading aid. Hidden for `lang=ar`. |

Use `text_qpc_hafs` for Quran display when present. Use `text_imlaei_simple` as fallback if display text is missing.

### Surah Info

Surah info is language-specific background HTML. It can be large, so `/v1/quran/surahs` omits it by default.

- Use `/v1/quran/surahs?include_info=false` for index/list screens.
- Use `/v1/quran/surahs/{surah_id}` for a detail/header screen that needs `info`.
- Use `/v1/quran/surahs?include_info=true` only if the UI truly needs all surah info in one call.

Surah info is exact-language only. If `lang=en` info is not imported, `info` is omitted and `localization.available_langs` tells FE which languages can be offered.

`info.text_html` is sanitized by backend, but FE should still render it only in the Quran info area, not inside arbitrary user-controlled HTML containers.

### Surah Editorial (SEO/SGE)

Surah-level editorial enrichment powers the public `/surah/{slug}` SEO pages.

Surah-level fields (language-independent, on every surah object):

- `slug` — canonical, stable URL slug (e.g. `al-mulk`). Language-independent. Backfilled from `name_latin`; treat as stable (changing it breaks live URLs).
- `chronological_order` — order of revelation (`1`–`114`), nullable.
- `ruku_count` — number of ruku, nullable.
- `content_updated_at` — `GREATEST(surah, editorial)` for the requested lang. Use it as the sitemap `lastmod` so editorial edits move freshness; falls back to surah `updated_at` when no editorial.

`editorial` (per-language object, present **only when reviewed**):

- Returned **only when `license_status = 'permitted'`** — unreviewed/draft copy is never exposed by the API (the gate is in the backend, not just the FE).
- Heavy HTML (`keutamaan_html`, `asbabun_nuzul_html`, `pokok_kandungan_html`) is returned **only on the detail endpoint** `GET /v1/quran/surahs/{surah_id}`. The list endpoint (`?include_info`) carries only the **light** editorial metadata (`meta_title`, `meta_description`, `arti_nama`, `license_status`, `created_at`, `updated_at`) so the 114-row payload stays under the edge-cache limit.
- Fields: `meta_title`, `meta_description` (SEO), `arti_nama`, the three `*_html` bodies, `author_name`/`reviewed_by`/`reviewed_at` (E-E-A-T), `license_status`, `created_at`, `updated_at`.
- HTML is backend-sanitized on read; render only inside the editorial area.
- Populate via the CLI loader `cmd/import-quran-surah-editorial` (see README).

### Multilingual Availability

Quran uses the same `lang` contract as kitab reader:

- Supported: `ar`, `id`, `en`.
- Empty defaults to `id`.
- Region tags normalize to primary language, for example `en-US -> en`.
- Unsupported explicit languages return `400 {"error":"unsupported language"}`.
- No automatic `en -> id` translation fallback.
- Arabic Quran text is canonical source content and always remains available.

FE should use `localization` on surah responses and `availability.translation|audio` on ayah responses as the source of truth for tabs, empty states, labels, and language offers.

### Language Scenarios

`lang=id` with imported Indonesian translation:

```json
{
  "ayah_key": "1:1",
  "translation": {
    "source_id": "qul-kfgqpc-id-simple",
    "lang": "id",
    "text": "Dengan menyebut nama Allah Yang Maha Pemurah lagi Maha Penyayang."
  },
  "requested_lang": "id",
  "available_translation_langs": ["en", "id"],
  "translation_missing": false,
  "availability": {
    "translation": {
      "action": "show_requested",
      "reason": "exact_available",
      "requested_lang": "id",
      "display_lang": "id",
      "is_fallback": false,
      "missing": false,
      "available_langs": ["en", "id"]
    }
  }
}
```

`lang=en` with imported English translation:

```json
{
  "ayah_key": "1:1",
  "translation": {
    "source_id": "qul-en-haleem-simple",
    "lang": "en",
    "text": "In the name of God, the Lord of Mercy, the Giver of Mercy!"
  },
  "requested_lang": "en",
  "available_translation_langs": ["en", "id"],
  "translation_missing": false,
  "availability": {
    "translation": {
      "action": "show_requested",
      "reason": "exact_available",
      "requested_lang": "en",
      "display_lang": "en",
      "is_fallback": false,
      "missing": false,
      "available_langs": ["en", "id"]
    }
  }
}
```

`lang=en` when English is missing but Indonesian exists:

```json
{
  "ayah_key": "1:1",
  "translation": null,
  "requested_lang": "en",
  "available_translation_langs": ["id"],
  "translation_missing": true,
  "availability": {
    "translation": {
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

`lang=ar` Arabic-only mode:

```json
{
  "ayah_key": "1:1",
  "translation": null,
  "requested_lang": "ar",
  "available_translation_langs": ["en", "id"],
  "translation_missing": false,
  "availability": {
    "translation": {
      "action": "hide_translation_tab",
      "reason": "source_language",
      "requested_lang": "ar",
      "display_lang": "ar",
      "is_fallback": false,
      "missing": false,
      "available_langs": ["en", "id"]
    }
  }
}
```

### Audio Tracks

Audio is metadata only. Actual files are served by `public_url` when R2 ingestion has populated it, or by imported source `audio_url` as a playable fallback before R2 sync.

There are two supported track shapes:

| Track type | `track_key` example | Meaning |
| --- | --- | --- |
| `ayah` | `1:1` | One audio file for one ayah. |
| `surah` | `1` | One audio file for a full surah, with `segments` pointing to ayah timestamps. |

For FE playback:

- Use `public_url ?? audio_url` as the playable URL.
- Prefer `public_url` when present because it is the app-owned CDN URL.
- Do not require `public_url` before playing audio in local/dev environments.
- `audio_url` is the original/import source URL and remains a playable fallback until R2 sync has populated `public_url`.
- `r2_key` is storage metadata, not a browser URL.
- Segment timestamps are milliseconds.
- Surah tracks use `segments` so the player can seek to an ayah inside a full-surah file.
- Ayah tracks can also include `segments`; if absent, treat the whole file as the ayah.

### Default Recitation

`GET /v1/quran/recitations` marks one deterministic default with `is_default=true` when a full playable recitation exists. A track is playable when it has either `public_url` or source `audio_url`.

Default selection order:

1. Only recitations where `has_playable_audio=true` are eligible.
2. Prefer the lowest non-null `default_priority`; production pins Mishari Rashid Al-Afasy with priority `0`.
3. If no pinned playable recitation exists, prefer `mode="ayah"` over `mode="surah"`.
4. Then sort by `display_name` ascending.
5. Then sort by `id` ascending.

When an ayah endpoint receives `include_audio=true` without `recitation_id`, the backend uses the same default recitation. If no full playable recitation exists yet, the endpoint still returns the ayah data with `audio` omitted or empty.

FE helper:

```ts
export function playableQuranAudioURL(track: QuranAudioTrack): string | null {
  return track.public_url || track.audio_url || null;
}
```

Only visible recitations are returned by `/recitations` and accepted by audio endpoints. When `recitation_id` is provided explicitly and does not exist or is hidden, the backend returns `404`.

## Query Parameter Rules

### Boolean Query Params

Boolean params should be sent as `true` or `false`.

Invalid booleans return `400`:

```json
{
  "error": "invalid include_audio"
}
```

Affected params:

- `include_info`
- `include_translation`
- `include_audio`

### Integer Query Params

`from` and `to` must be positive integers when present. Invalid or non-positive values return `400`.

`limit` and `offset` are forgiving:

- Invalid `limit` falls back to `50`.
- `limit <= 0` falls back to `50`.
- `limit > 200` is clamped to `200`.
- Invalid `offset` falls back to `0`.
- `offset < 0` is clamped to `0`.

### Language

`lang` is trimmed, lowercased, and region-normalized. Empty value defaults to `id`.

If a language-specific translation or surah info is not imported, the main Quran object is still returned, but the optional language-specific object is omitted and availability metadata explains the missing asset.

## Endpoints

### 1. List Surahs

```http
GET /v1/quran/surahs?lang=id&include_info=false
```

Use this for a surah index, picker, sidebar, or Quran home screen.

### Query Params

| Param | Type | Default | Notes |
| --- | --- | --- | --- |
| `lang` | string | `id` | Used only when `include_info=true`. |
| `include_info` | boolean | `false` | Adds language-specific `info` to each surah. |

### Response

Status: `200`

```json
{
  "items": [
    {
      "surah_id": 1,
      "name_arabic": "الفاتحة",
      "name_latin": "Al-Fatihah",
      "name_translation": "Pembukaan",
      "revelation_type": "makkiyah",
      "ayah_count": 7,
      "slug": "al-fatihah",
      "chronological_order": 5,
      "ruku_count": 1,
      "localization": {
        "requested_lang": "id",
        "display_lang": "id",
        "is_fallback": false,
        "available_langs": ["en", "id"],
        "field_langs": {
          "name_arabic": "ar",
          "name_latin": "ar",
          "name_translation": "id",
          "info": "id"
        },
        "availability": {
          "action": "show_requested",
          "reason": "exact_available",
          "requested_lang": "id",
          "display_lang": "id",
          "is_fallback": false,
          "missing": false,
          "available_langs": ["en", "id"]
        }
      },
      "metadata": {},
      "content_updated_at": "2026-05-28T00:00:00Z",
      "updated_at": "2026-05-28T00:00:00Z"
    }
  ],
  "total": 114
}
```

When `include_info=true`, each item may include:

```json
{
  "surah_id": 1,
  "name_arabic": "الفاتحة",
  "name_latin": "Al-Fatihah",
  "name_translation": "Pembukaan",
  "revelation_type": "makkiyah",
  "ayah_count": 7,
  "info": {
    "lang": "id",
    "surah_name": "Al-Fatihah",
    "text_html": "<p>...</p>",
    "short_text": "...",
    "source_name": "QUL Surah information",
    "source_url": "https://qul.tarteel.ai/...",
    "qul_resource_id": "...",
    "format": "json",
    "license_status": "needs_review",
    "checksum": "...",
    "metadata": {},
    "imported_at": "2026-05-28T00:00:00Z",
    "updated_at": "2026-05-28T00:00:00Z"
  },
  "localization": {
    "requested_lang": "id",
    "display_lang": "id",
    "is_fallback": false,
    "available_langs": ["en", "id"],
    "field_langs": {
      "name_arabic": "ar",
      "name_latin": "ar",
      "name_translation": "id",
      "info": "id"
    },
    "availability": {
      "action": "show_requested",
      "reason": "exact_available",
      "requested_lang": "id",
      "display_lang": "id",
      "is_fallback": false,
      "missing": false,
      "available_langs": ["en", "id"]
    }
  },
  "metadata": {},
  "updated_at": "2026-05-28T00:00:00Z"
}
```

### FE Guidance

For list UI, keep `include_info=false`. Fetching all info HTML for 114 surahs is intentionally opt-in.

### 2. Get One Surah

```http
GET /v1/quran/surahs/{surah_id}?lang=id
```

Use this for a surah detail header, drawer, or info page.

### Path Params

| Param | Type | Notes |
| --- | --- | --- |
| `surah_id` | integer | `1` to `114`. |

### Query Params

| Param | Type | Default | Notes |
| --- | --- | --- | --- |
| `lang` | string | `id` | Selects `info` language. |

### Response

Status: `200`

```json
{
  "surah_id": 73,
  "slug": "al-muzzammil",
  "name_arabic": "المزمل",
  "name_latin": "Al-Muzzammil",
  "name_translation": "Orang yang Berselimut",
  "revelation_type": "makkiyah",
  "ayah_count": 20,
  "chronological_order": 3,
  "ruku_count": 2,
  "info": {
    "lang": "id",
    "surah_name": "Al-Muzzammil",
    "text_html": "<p>...</p>",
    "source_name": "QUL Surah information",
    "format": "json",
    "license_status": "needs_review",
    "updated_at": "2026-05-28T00:00:00Z"
  },
  "editorial": {
    "lang": "id",
    "meta_title": "Surah Al-Muzzammil: Keutamaan, Asbabun Nuzul & Pokok Kandungan",
    "meta_description": "Keutamaan Surah Al-Muzzammil, asbabun nuzul, dan ringkasan kandungannya.",
    "arti_nama": "Orang yang Berselimut",
    "keutamaan_html": "<p>...</p>",
    "asbabun_nuzul_html": "<p>...</p>",
    "pokok_kandungan_html": "<p>...</p>",
    "author_name": "Tim Surau",
    "reviewed_by": "Ustadz Fulan, Lc.",
    "reviewed_at": "2026-06-23T00:00:00Z",
    "license_status": "permitted",
    "created_at": "2026-06-23T00:00:00Z",
    "updated_at": "2026-06-23T00:00:00Z"
  },
  "localization": {
    "requested_lang": "id",
    "display_lang": "id",
    "is_fallback": false,
    "available_langs": ["en", "id"],
    "field_langs": {
      "name_arabic": "ar",
      "name_latin": "ar",
      "name_translation": "id",
      "info": "id"
    },
    "availability": {
      "action": "show_requested",
      "reason": "exact_available",
      "requested_lang": "id",
      "display_lang": "id",
      "is_fallback": false,
      "missing": false,
      "available_langs": ["en", "id"]
    }
  },
  "metadata": {},
  "content_updated_at": "2026-06-23T00:00:00Z",
  "updated_at": "2026-05-28T00:00:00Z"
}
```

### Errors

| Status | Body | Cause |
| --- | --- | --- |
| `400` | `{"error":"invalid surah_id"}` | Path value is not a positive integer. |
| `404` | `{"error":"quran surah not found"}` | Surah is outside `1..114` or not imported. |

### 3. List Recitations

```http
GET /v1/quran/recitations
```

Use this before showing audio reciter options or before storing a user's preferred recitation.

### Response

Status: `200`

```json
{
  "items": [
    {
      "id": "qul-ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953",
      "name": "QUL ayah recitation mishari rashid al afasy murattal hafs 953",
      "reciter_name": "Mishari Rashid Al-Afasy",
      "style": "murattal",
      "mode": "ayah",
      "source_url": "https://qul.tarteel.ai/...",
      "qul_resource_id": "953",
      "format": "json",
      "license_status": "needs_review",
      "checksum": "...",
      "track_count": 6236,
      "public_track_count": 0,
      "playable_track_count": 6236,
      "has_public_audio": false,
      "has_playable_audio": true,
      "is_default": true,
      "metadata": {},
      "imported_at": "2026-05-28T00:00:00Z",
      "updated_at": "2026-05-28T00:00:00Z"
    }
  ],
  "total": 1
}
```

### Field Notes

| Field | Meaning |
| --- | --- |
| `mode` | `ayah`, `surah`, or another imported mode. |
| `track_count` | Imported track metadata count. |
| `public_track_count` | Tracks that already have `public_url`. |
| `playable_track_count` | Tracks that have either `public_url` or source `audio_url`. |
| `has_public_audio` | `true` only when all tracks are public. |
| `has_playable_audio` | `true` when all imported tracks can be played from `public_url` or `audio_url`. |
| `is_default` | Backend-selected playable default for `include_audio=true` without `recitation_id`. |

### FE Guidance

Use the recitation with `is_default=true` as the initial selected recitation. If the user chooses another recitation, pass its `id` as `recitation_id` on ayah endpoints.

### 3b. Get Surah Audio Manifest

```http
GET /v1/quran/surahs/{surah_id}/audio?recitation_id=
```

Use this for player setup, preloading, download planning, or switching reciters without refetching the full ayah text payload. If `recitation_id` is omitted, the backend uses the same visible default recitation as `include_audio=true`.

Response shape:

```json
{
  "surah_id": 1,
  "recitation": {
    "id": "qul-ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953",
    "display_name": "Mishari Rashid Al-Afasy",
    "reciter_name": "Mishari Rashid Al-Afasy",
    "style": "murattal",
    "mode": "ayah",
    "is_default": true,
    "track_count": 6236,
    "public_track_count": 6236,
    "segment_count": 77796
  },
  "mode": "ayah",
  "tracks": [
    {
      "recitation_id": "qul-ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953",
      "track_type": "ayah",
      "track_key": "1:1",
      "surah_id": 1,
      "ayah_number": 1,
      "url": "https://cdn.surau.org/quran/audio/...",
      "duration_ms": 5000,
      "mime_type": "audio/mpeg",
      "segments": []
    }
  ],
  "missing_ayah_keys": []
}
```

For `mode="ayah"`, tracks normally contain one playable item per ayah. For `mode="surah"`, tracks normally contain one full-surah item and `segments` marks ayah ranges inside it. `missing_ayah_keys` is the FE-safe source for partial audio states.

### 4. List Translation Sources

```http
GET /v1/quran/translation-sources?lang=id
```

Use this before rendering translation source pickers or deciding whether a requested language has imported Quran translations.

### Response

Status: `200`

```json
{
  "items": [
    {
      "id": "qul-kfgqpc-id-simple",
      "lang": "id",
      "name": "King Fahad Quran Complex",
      "source_url": "https://qul.tarteel.ai/resources/translation/173",
      "qul_resource_id": "173",
      "format": "simple.json",
      "license_status": "needs_review",
      "coverage": {
        "translated_ayahs": 6236,
        "total_ayahs": 6236,
        "percent": 100
      },
      "is_default": true,
      "metadata": {},
      "updated_at": "2026-05-29T00:00:00Z"
    }
  ],
  "total": 1
}
```

For `lang=ar`, `items` is empty and `total` is `0` because Arabic is source text, not a translation source.

### 5. Get One Ayah

```http
GET /v1/quran/ayahs/{ayah_key}?lang=id&translation_source=qul-kfgqpc-id-simple&include_audio=false&recitation_id=
```

Use this for direct ayah pages, citation previews, or one-off lookup.

### Path Params

| Param | Type | Notes |
| --- | --- | --- |
| `ayah_key` | string | Canonical key like `1:1`, `73:4`, `114:6`. |

### Query Params

| Param | Type | Default | Notes |
| --- | --- | --- | --- |
| `lang` | string | `id` | Translation language. |
| `translation_source` | string | language default | Translation source ID. Explicit unknown or wrong-language source returns `404`. |
| `include_audio` | boolean | `false` | Adds audio tracks when true. |
| `recitation_id` | string | empty | If empty and `include_audio=true`, backend uses default recitation. |

### Response Without Audio

Status: `200`

```json
{
  "surah_id": 1,
  "ayah_number": 1,
  "ayah_key": "1:1",
  "text_qpc_hafs": "بِسْمِ اللَّهِ الرَّحْمَٰنِ الرَّحِيمِ",
  "text_imlaei_simple": "بسم الله الرحمن الرحيم",
  "search_text": "بسم الله الرحمن الرحيم",
  "script_type": "qpc-hafs",
  "font_family": "QPC Hafs",
  "page_number": 1,
  "juz_number": 1,
  "hizb_number": 1,
  "translation": {
    "source_id": "qul-kfgqpc-id-simple",
    "lang": "id",
    "text": "Dengan nama Allah Yang Maha Pengasih, Maha Penyayang.",
    "metadata": {},
    "updated_at": "2026-05-28T00:00:00Z"
  },
  "requested_lang": "id",
  "available_translation_langs": ["id"],
  "translation_missing": false,
  "availability": {
    "translation": {
      "action": "show_requested",
      "reason": "exact_available",
      "requested_lang": "id",
      "display_lang": "id",
      "is_fallback": false,
      "missing": false,
      "available_langs": ["id"]
    },
    "audio": {
      "action": "hide_audio",
      "reason": "unavailable",
      "requested_lang": "id",
      "display_lang": "ar",
      "is_fallback": false,
      "missing": true,
      "available_langs": []
    }
  },
  "metadata": {},
  "updated_at": "2026-05-28T00:00:00Z"
}
```

### Missing Requested Translation

When `lang=en` has no exact translation but Indonesian exists, Arabic still renders and the requested translation stays empty:

```json
{
  "ayah_key": "1:1",
  "text_qpc_hafs": "بِسْمِ اللَّهِ الرَّحْمَٰنِ الرَّحِيمِ",
  "translation": null,
  "requested_lang": "en",
  "available_translation_langs": ["id"],
  "translation_missing": true,
  "availability": {
    "translation": {
      "action": "offer_available_lang",
      "reason": "alternative_langs_available",
      "requested_lang": "en",
      "display_lang": "ar",
      "is_fallback": true,
      "missing": true,
      "available_langs": ["id"]
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
  }
}
```

For `lang=ar`, `translation` is omitted/null and `availability.translation.action` is `hide_translation_tab`.

### Response With Ayah Audio

```json
{
  "surah_id": 1,
  "ayah_number": 1,
  "ayah_key": "1:1",
  "text_qpc_hafs": "بِسْمِ اللَّهِ الرَّحْمَٰنِ الرَّحِيمِ",
  "translation": {
    "source_id": "qul-kfgqpc-id-simple",
    "lang": "id",
    "text": "Dengan nama Allah Yang Maha Pengasih, Maha Penyayang."
  },
  "audio": [
    {
      "recitation_id": "qul-ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953",
      "track_type": "ayah",
      "track_key": "1:1",
      "surah_id": 1,
      "ayah_number": 1,
      "audio_url": "https://...",
      "r2_key": "quran/audio/qul-ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953/ayah/1/1.mp3",
      "public_url": null,
      "duration_ms": 5000,
      "duration_seconds": 5,
      "mime_type": "audio/mpeg",
      "segments": [
        {
          "segment_index": 1,
          "ayah_key": "1:1",
          "timestamp_from_ms": 0,
          "timestamp_to_ms": 560,
          "duration_ms": 560,
          "metadata": {}
        }
      ],
      "metadata": {},
      "updated_at": "2026-05-28T00:00:00Z"
    }
  ],
  "availability": {
    "audio": {
      "action": "show_requested",
      "reason": "exact_available",
      "requested_lang": "id",
      "display_lang": "id",
      "is_fallback": false,
      "missing": false,
      "available_langs": []
    }
  },
  "updated_at": "2026-05-28T00:00:00Z"
}
```

In this example the track is still playable because `audio_url` is present even though `public_url` is `null`.

### Response With Surah Audio

If the selected/default recitation is `mode="surah"`, the audio item can be a full-surah track with an ayah segment:

```json
{
  "ayah_key": "1:1",
  "audio": [
    {
      "recitation_id": "qul-surah-recitation-yasser-al-dosari",
      "track_type": "surah",
      "track_key": "1",
      "surah_id": 1,
      "public_url": "https://cdn.surau.org/quran/audio/qul-surah-recitation-yasser-al-dosari/surah/1.mp3",
      "segments": [
        {
          "segment_index": 1,
          "ayah_key": "1:1",
          "timestamp_from_ms": 0,
          "timestamp_to_ms": 5000,
          "duration_ms": 5000,
          "metadata": {}
        }
      ],
      "updated_at": "2026-05-28T00:00:00Z"
    }
  ]
}
```

### Errors

| Status | Body | Cause |
| --- | --- | --- |
| `400` | `{"error":"invalid ayah key"}` | Path is not canonical `{surah}:{ayah}`. |
| `400` | `{"error":"invalid include_audio"}` | Boolean query value cannot be parsed. |
| `404` | `{"error":"quran ayah not found"}` | Ayah key is valid but not imported. |
| `404` | `{"error":"quran recitation not found"}` | Explicit `recitation_id` does not exist. |

### 6. List Ayahs In A Surah

```http
GET /v1/quran/surahs/{surah_id}/ayahs?from=&to=&lang=id&include_translation=true&include_audio=false&recitation_id=&view=
```

Use this for the main Quran reader screen. The response is `{"items": [...], "total": <int>}`; with the default view each item is a full `QuranAyah`. Pass `view=reader_minimal` for the compact reader payload.

### Query Params

| Param | Type | Default | Notes |
| --- | --- | --- | --- |
| `from` | integer | empty | Optional first ayah. Must be positive when present. |
| `to` | integer | empty | Optional last ayah. Must be positive when present. |
| `lang` | string | `id` | Translation language. |
| `translation_source` | string | language default | Explicit source override. Unknown or wrong-language source returns `404`. |
| `include_translation` | boolean | `true` | Set false for Arabic-only views. |
| `include_audio` | boolean | `false` | Adds audio tracks when true. |
| `recitation_id` | string | empty | If empty and audio is requested, backend uses default recitation. |
| `view` | string | `full` | `full` or empty returns full `QuranAyah` items; `reader_minimal` returns compact reader fields only. |

### Range Behavior

| Request | Meaning |
| --- | --- |
| no `from`, no `to` | Return the whole surah. |
| `from=5` | Return from ayah 5 through the end. |
| `to=10` | Return from ayah 1 through ayah 10. |
| `from=5&to=10` | Return ayahs 5 through 10. |
| `from=10&to=5` | `400 invalid quran range`. |

### Response

Status: `200`

```json
{
  "items": [
    {
      "surah_id": 73,
      "ayah_number": 1,
      "ayah_key": "73:1",
      "text_qpc_hafs": "يَٰٓأَيُّهَا ٱلْمُزَّمِّلُ",
      "text_imlaei_simple": "يا أيها المزمل",
      "translation": {
        "source_id": "qul-kfgqpc-id-simple",
        "lang": "id",
        "text": "Wahai orang yang berselimut!"
      },
      "updated_at": "2026-05-28T00:00:00Z"
    }
  ],
  "total": 20
}
```

### Reader Minimal Response

When `view=reader_minimal`, each item omits import/debug/localization/search fields and returns only reader-display data (the `{items,total}` envelope stays the same):

```json
{
  "items": [
    {
      "surah_id": 73,
      "ayah_number": 1,
      "ayah_key": "73:1",
      "text_qpc_hafs": "يَٰٓأَيُّهَا ٱلْمُزَّمِّلُ",
      "juz_number": 29,
      "page_number": 574,
      "translation": {
        "text": "Wahai orang yang berselimut!"
      },
      "audio": [
        {
          "recitation_id": "qul-ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953",
          "track_type": "ayah",
          "track_key": "73:1",
          "url": "https://cdn.example/quran/73-1.mp3",
          "segments": [
            {
              "segment_index": 1,
              "ayah_key": "73:1",
              "timestamp_from_ms": 1200,
              "timestamp_to_ms": 4200,
              "duration_ms": 3000
            }
          ]
        }
      ]
    }
  ],
  "total": 20
}
```

`translation` is omitted when translation is not requested or not available. `audio` is omitted when `include_audio=false` or no playable URL exists. Audio `url` is already resolved from `public_url` fallback to source `audio_url`.

### Errors

| Status | Body | Cause |
| --- | --- | --- |
| `400` | `{"error":"invalid surah_id"}` | Path value is not a positive integer. |
| `400` | `{"error":"invalid from"}` | `from` is not a positive integer. |
| `400` | `{"error":"invalid to"}` | `to` is not a positive integer. |
| `400` | `{"error":"invalid include_translation"}` | Boolean query value cannot be parsed. |
| `400` | `{"error":"invalid include_audio"}` | Boolean query value cannot be parsed. |
| `400` | `{"error":"invalid view"}` | `view` is not empty, `full`, or `reader_minimal`. |
| `400` | `{"error":"invalid quran range"}` | Range is logically invalid. |
| `404` | `{"error":"quran surah not found"}` | Surah is outside `1..114` or not imported. |
| `404` | `{"error":"quran recitation not found"}` | Explicit `recitation_id` does not exist. |

### 7. Quran Navigation: Juz and Hizb

```http
GET /v1/quran/juz?lang=id
GET /v1/quran/hizbs?lang=id
```

Use these for segmented navigation without downloading ayah text. The data comes from `juz_number` and `hizb_number` when present in imported ayah metadata; if the QUL script export only contains text, the importer fills the same columns from QUL's canonical Juz and Hizb metadata boundaries.

### Response

Status: `200`

```json
{
  "items": [
    {
      "kind": "juz",
      "number": 1,
      "ayah_count": 148,
      "start": {
        "surah_id": 1,
        "ayah_number": 1,
        "ayah_key": "1:1",
        "surah_name": "Al-Fatihah"
      },
      "end": {
        "surah_id": 2,
        "ayah_number": 141,
        "ayah_key": "2:141",
        "surah_name": "Al-Baqarah"
      }
    }
  ],
  "total": 30
}
```

`kind` is either `juz` or `hizb`. `surah_name` is lightweight and language-aware: Arabic mode prefers Arabic names, while `id/en` prefer imported surah info names when available.

### Navigation Ayahs

```http
GET /v1/quran/juz/{juz_number}/ayahs?lang=id&translation_source=&include_translation=true&include_audio=false&recitation_id=&view=
GET /v1/quran/hizbs/{hizb_number}/ayahs?lang=id&translation_source=&include_translation=true&include_audio=false&recitation_id=&view=
```

These endpoints return the same full or `reader_minimal` ayah shape and audio behavior as `/v1/quran/surahs/{surah_id}/ayahs`.

### Query Params

| Param | Type | Default | Notes |
| --- | --- | --- | --- |
| `lang` | string | `id` | Translation language. |
| `translation_source` | string | language default | Explicit source override. Unknown or wrong-language source returns `404`. |
| `include_translation` | boolean | `true` | Set false for Arabic-only views. |
| `include_audio` | boolean | `false` | Adds audio tracks when true. |
| `recitation_id` | string | empty | If empty and audio is requested, backend uses default recitation. |
| `view` | string | `full` | `full` or empty returns full `QuranAyah` items; `reader_minimal` returns compact reader fields only. |

### Errors

| Status | Body | Cause |
| --- | --- | --- |
| `400` | `{"error":"invalid juz_number"}` | Juz path value is not an integer. |
| `400` | `{"error":"invalid hizb_number"}` | Hizb path value is not an integer. |
| `400` | `{"error":"invalid include_translation"}` | Boolean query value cannot be parsed. |
| `400` | `{"error":"invalid include_audio"}` | Boolean query value cannot be parsed. |
| `400` | `{"error":"invalid view"}` | `view` is not empty, `full`, or `reader_minimal`. |
| `400` | `{"error":"invalid quran range"}` | Juz is outside `1..30` or hizb is outside `1..60`. |
| `404` | `{"error":"quran navigation not found"}` | Number is valid but imported ayah metadata has no rows for that segment. |
| `404` | `{"error":"quran recitation not found"}` | Explicit `recitation_id` does not exist. |

### 8. Search Quran

```http
GET /v1/quran/search?q=&lang=id&limit=50&offset=0
```

Searches Arabic Quran text, the requested translation, and other imported translations for discoverability. Result display still returns exact requested-language translation only.

Rate limit: per-IP limiter; exceeding it returns `429` with the standard error envelope (`code: too_many_requests`, `retry_after` mirrors the `Retry-After` header). Caching: unlike the other Quran GET endpoints (which send `Cache-Control: public, max-age=300, stale-while-revalidate=86400` + `ETag`), search is dynamic and answers `Cache-Control: no-store` (F1-D).

### Query Params

| Param | Type | Default | Notes |
| --- | --- | --- | --- |
| `q` | string | required by UI | Backend accepts empty but returns no useful results. |
| `lang` | string | `id` | Translation language included in search. |
| `limit` | integer | `50` | Clamped to max `200`. |
| `offset` | integer | `0` | Negative becomes `0`. |

### Response

Status: `200`

```json
{
  "items": [
    {
      "ayah": {
        "surah_id": 1,
        "ayah_number": 1,
        "ayah_key": "1:1",
        "text_qpc_hafs": "بِسْمِ اللَّهِ الرَّحْمَٰنِ الرَّحِيمِ",
        "translation": {
          "source_id": "qul-kfgqpc-id-simple",
          "lang": "id",
          "text": "Dengan nama Allah Yang Maha Pengasih, Maha Penyayang."
        },
        "updated_at": "2026-05-28T00:00:00Z"
      },
      "score": 0.82,
      "matched_lang": "id",
      "matched_source_id": "qul-kfgqpc-id-simple",
      "matched_field": "translation"
    }
  ],
  "total": 1
}
```

### FE Guidance

- Debounce search input.
- Keep using `limit` and `offset` for pagination.
- Display results by `ayah.ayah_key`, surah name from cached surah list, Arabic text, and translation.
- Use `matched_lang`, `matched_source_id`, and `matched_field` to label why a result matched when the requested translation is missing but another imported translation matched.
- Display still follows exact requested-language rules. A result may match Indonesian while `lang=en`, but `result.ayah.translation` remains `null` unless English exists.
- Search does not include audio.

### 9. Book Quran References

```http
GET /v1/books/{book_id}/quran-references?lang=id&heading_id=10&status=approved&limit=50&offset=0
```

Returns Quran references linked to a public kitab. Use `heading_id` for section-scoped reads; do not fetch a broad page and filter on the client.

`GET /v1/books/{book_id}/toc/{heading_id}/read?include_quran_references=true` can embed the same approved heading-scoped references under `quran_references` in the reader payload.

### Path Params

| Param | Type | Notes |
| --- | --- | --- |
| `book_id` | integer | Public kitab ID. |

### Query Params

| Param | Type | Default | Notes |
| --- | --- | --- | --- |
| `lang` | string | `id` | Translation language for attached ayahs. |
| `heading_id` | integer | none | Optional positive heading filter. |
| `status` | string | `approved` | One of `approved`, `pending`, `rejected`, `ambiguous`, `needs_review`, `all`. Invalid values become `approved`. |
| `limit` | integer | `50` | Clamped to max `200`. |
| `offset` | integer | `0` | Negative becomes `0`. |

### Response

Status: `200`

```json
{
  "items": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "book_id": 797,
      "page_id": 12,
      "heading_id": 10,
      "knowledge_mention_id": "mention-id",
      "source_text": "QS Al-Muzzammil: 4",
      "normalized_text": "al-muzzammil 4",
      "reference_kind": "surah_ayah",
      "surah_id": 73,
      "from_ayah_number": 4,
      "to_ayah_number": 4,
      "from_ayah_key": "73:4",
      "to_ayah_key": "73:4",
      "match_strategy": "explicit_surah_ayah",
      "confidence": 1,
      "review_status": "approved",
      "ayahs": [
        {
          "surah_id": 73,
          "ayah_number": 4,
          "ayah_key": "73:4",
          "text_qpc_hafs": "...",
          "translation": {
            "source_id": "qul-kfgqpc-id-simple",
            "lang": "id",
            "text": "..."
          },
          "updated_at": "2026-05-28T00:00:00Z"
        }
      ],
      "metadata": {},
      "created_at": "2026-05-28T00:00:00Z",
      "updated_at": "2026-05-28T00:00:00Z"
    }
  ],
  "total": 1
}
```

### Field Notes

| Field | Meaning |
| --- | --- |
| `source_text` | Text found in kitab/import pipeline. |
| `normalized_text` | Normalized resolver text. |
| `reference_kind` | Example: `surah`, `surah_ayah`, `ayah_range`, `quote`. |
| `match_strategy` | Example: `explicit_surah_ayah`, `exact_quote`, `surah_alias`. |
| `review_status` | Review workflow status. Public FE should default to `approved`. |
| `ayahs` | Present only when the reference has concrete ayah range fields. Surah-only references can have no ayahs. Each ayah uses the same `QuranAyah` multilingual metadata as Quran reader endpoints. |

### Errors

| Status | Body | Cause |
| --- | --- | --- |
| `400` | `{"error":"invalid book_id"}` | Path value is not a positive integer. |
| `404` | `{"error":"book not found"}` | Book does not exist or is not published. |

### 10. Editorial Missing Quran Assets

```http
GET /v1/editorial/quran/missing-assets?target_lang=en&asset_type=ayah_translation&surah_id=73
```

Editor/admin queue for Quran localization and media gaps. Empty `target_lang` returns both `id` and `en`; `target_lang=ar` returns `400` because Arabic is source content.

Supported `asset_type` values:

- `surah_info`
- `ayah_translation`
- `translation_source`
- `audio_public`

`audio_public` means an imported audio track is missing app-owned `public_url`. The same track may still be playable through source `audio_url`; this queue is for CDN/R2 publishing work, not for basic playback availability.

Response:

```json
{
  "items": [
    {
      "asset_type": "ayah_translation",
      "target_lang": "en",
      "surah_id": 73,
      "surah_name": "Al-Muzzammil",
      "ayah_number": 4,
      "ayah_key": "73:4",
      "available_langs": ["id"],
      "source_updated_at": "2026-05-29T00:00:00Z"
    }
  ],
  "total": 1,
  "counts": [
    {
      "asset_type": "ayah_translation",
      "target_lang": "en",
      "total": 1
    }
  ]
}
```

## Response Shape Reference

List endpoints wrap the item shapes below in the shared `{ "items": [...], "total": number }` envelope; the types describe one item.

### QuranSurah

```ts
type QuranSurah = {
  surah_id: number;
  name_arabic?: string;
  name_latin?: string;
  name_translation?: string;
  revelation_type?: string;
  ayah_count: number;
  info?: QuranSurahInfo;
  localization: LocalizationMeta;
  metadata?: Record<string, unknown>;
  updated_at: string;
};
```

### QuranSurahInfo

```ts
type QuranSurahInfo = {
  lang: string;
  surah_name?: string;
  text_html: string;
  short_text?: string;
  source_name: string;
  source_url?: string;
  qul_resource_id?: string;
  format: string;
  license_status: string;
  checksum?: string;
  metadata?: Record<string, unknown>;
  imported_at?: string;
  updated_at: string;
};
```

### QuranNavigationSegment

```ts
type QuranNavigationBoundary = {
  surah_id: number;
  ayah_number: number;
  ayah_key: string;
  surah_name?: string;
};

type QuranNavigationSegment = {
  kind: "juz" | "hizb";
  number: number;
  ayah_count: number;
  start: QuranNavigationBoundary;
  end: QuranNavigationBoundary;
};
```

### QuranAyah

```ts
type QuranAyah = {
  surah_id: number;
  ayah_number: number;
  ayah_key: string;
  text_qpc_hafs?: string;
  text_imlaei_simple?: string;
  search_text?: string;
  script_type?: string;
  font_family?: string;
  page_number?: number;
  juz_number?: number;
  hizb_number?: number;
  translation: QuranTranslation | null;
  transliteration: QuranTransliteration | null;
  audio?: QuranAudioTrack[];
  requested_lang: "ar" | "id" | "en";
  available_translation_langs: string[];
  translation_missing: boolean;
  availability: QuranAyahAvailability;
  metadata?: Record<string, unknown>;
  updated_at: string;
};
```

`audio` is omitted when `include_audio=false` or no playable track is available. FE should read it as `ayah.audio ?? []`.

### QuranReaderAyah

Returned only by ayah list endpoints when `view=reader_minimal`.

```ts
type QuranReaderAyah = {
  surah_id: number;
  ayah_number: number;
  ayah_key: string;
  text_qpc_hafs?: string;
  juz_number?: number;
  page_number?: number;
  translation?: {
    text: string;
  };
  transliteration?: {
    text: string;
  };
  audio?: QuranReaderAyahAudioTrack[];
};

type QuranReaderAyahAudioTrack = {
  recitation_id: string;
  track_type: "ayah" | "surah" | string;
  track_key: string;
  url: string;
  segments?: QuranReaderAyahAudioSegment[];
};

type QuranReaderAyahAudioSegment = {
  segment_index: number;
  ayah_key: string;
  timestamp_from_ms: number;
  timestamp_to_ms: number;
  duration_ms?: number;
};
```

### AvailabilityDecision

```ts
type AvailabilityDecision = {
  action:
    | "show_requested"
    | "show_arabic"
    | "offer_available_lang"
    | "hide_translation_tab"
    | "hide_audio";
  reason:
    | "source_language"
    | "exact_available"
    | "arabic_fallback"
    | "alternative_langs_available"
    | "unavailable";
  requested_lang: "ar" | "id" | "en";
  display_lang: "ar" | "id" | "en";
  is_fallback: boolean;
  missing: boolean;
  available_langs: string[];
};

type LocalizationMeta = {
  requested_lang: "ar" | "id" | "en";
  display_lang: "ar" | "id" | "en";
  is_fallback: boolean;
  available_langs: string[];
  field_langs: Record<string, string>;
  availability: AvailabilityDecision;
};

type QuranAyahAvailability = {
  translation: AvailabilityDecision;
  audio: AvailabilityDecision;
};
```

### QuranTranslation

```ts
type QuranTranslation = {
  source_id: string;
  lang: string;
  text: string;
  footnotes?: unknown;
  chunks?: unknown;
  metadata?: Record<string, unknown>;
  updated_at: string;
};
```

### QuranTransliteration

```ts
type QuranTransliteration = {
  source_id: string;
  lang: "id" | "en";
  text: string;
  metadata?: Record<string, unknown>;
  updated_at: string;
};
```

### QuranTranslationSource

```ts
type QuranTranslationSource = {
  id: string;
  lang: "id" | "en";
  name: string;
  translator?: string;
  source_url?: string;
  qul_resource_id?: string;
  format: string;
  license_status: string;
  checksum?: string;
  coverage: {
    translated_ayahs: number;
    total_ayahs: number;
    percent: number;
  };
  is_default: boolean;
  metadata?: Record<string, unknown>;
  imported_at?: string;
  updated_at: string;
};
```

### QuranRecitation

```ts
type QuranRecitation = {
  id: string;
  name: string;
  reciter_name?: string;
  style?: string;
  mode: string;
  source_url?: string;
  qul_resource_id?: string;
  format: string;
  license_status: string;
  checksum?: string;
  track_count: number;
  public_track_count: number;
  playable_track_count: number;
  has_public_audio: boolean;
  has_playable_audio: boolean;
  is_default: boolean;
  metadata?: Record<string, unknown>;
  imported_at?: string;
  updated_at: string;
};
```

### QuranAudioTrack

```ts
type QuranAudioTrack = {
  recitation_id: string;
  track_type: "ayah" | "surah" | string;
  track_key: string;
  surah_id: number;
  ayah_number?: number;
  audio_url?: string;
  r2_key?: string;
  public_url?: string;
  duration_ms?: number;
  duration_seconds?: number;
  mime_type?: string;
  segments?: QuranAudioSegment[];
  metadata?: Record<string, unknown>;
  updated_at: string;
};
```

### QuranAudioSegment

```ts
type QuranAudioSegment = {
  segment_index: number;
  ayah_key: string;
  timestamp_from_ms: number;
  timestamp_to_ms: number;
  duration_ms?: number;
  metadata?: Record<string, unknown>;
};
```

### BookQuranReference

```ts
type BookQuranReference = {
  id: string;
  book_id: number;
  page_id: number;
  heading_id?: number;
  knowledge_mention_id?: string;
  source_text: string;
  normalized_text: string;
  reference_kind: string;
  surah_id?: number;
  from_ayah_number?: number;
  to_ayah_number?: number;
  from_ayah_key?: string;
  to_ayah_key?: string;
  match_strategy: string;
  confidence?: number;
  review_status: string;
  ayahs?: QuranAyah[];
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
};
```

## Recommended FE Fetch Flows

### Quran Home / Surah Picker

1. Fetch `GET /v1/quran/surahs?lang=id`.
2. Cache by `surah_id`.
3. Display `name_latin`, `name_arabic`, `name_translation`, `revelation_type`, and `ayah_count`.
4. Do not fetch `include_info=true` for this screen unless the design explicitly shows every surah's background text.

### Surah Reader Page

1. Fetch `GET /v1/quran/surahs/{surah_id}?lang=id` for header/info.
2. Fetch `GET /v1/quran/translation-sources?lang=id` if the UI lets users choose translation sources.
3. Fetch `GET /v1/quran/surahs/{surah_id}/ayahs?lang=id&include_translation=true&view=reader_minimal`.
4. If audio is enabled, call the ayah list with `include_audio=true&recitation_id={selectedRecitationID}`.
5. Render Arabic from `text_qpc_hafs`, translation from `translation.text` only when `translation` is not null.
6. Use `availability.translation.action` for translation tabs, empty states, and language offers.
7. If the selected recitation is a surah track, use the current ayah's segment to seek inside the same full-surah audio file.

### Juz / Hizb Reader Page

1. Fetch `GET /v1/quran/juz?lang=id` or `GET /v1/quran/hizbs?lang=id` for the navigation picker.
2. Fetch `GET /v1/quran/juz/{juz_number}/ayahs?lang=id&include_translation=true&view=reader_minimal` or the hizb equivalent for the reader body.
3. Reuse the same translation and audio controls as the surah reader page.
4. If a valid segment returns `404 quran navigation not found`, show a data-missing state; it means the QPC Hafs import did not include that navigation metadata.

### Audio Setup

1. Fetch `GET /v1/quran/recitations`.
2. Pick the item where `is_default=true`.
3. Optionally fetch `GET /v1/quran/surahs/{surah_id}/audio?recitation_id={selectedRecitationID}` to initialize the player from a compact manifest.
4. Store user choice client-side if needed.
5. Always pass the selected `recitation_id` once the user has chosen one.
6. If a stored `recitation_id` returns `404 quran recitation not found`, clear it and fall back to the backend default.

Playback helper:

```ts
export function selectQuranTrackURL(track: QuranAudioTrack): string | null {
  return track.public_url || track.audio_url || null;
}

export function currentAyahSegment(
  track: QuranAudioTrack,
  ayahKey: string,
): QuranAudioSegment | null {
  return track.segments?.find((segment) => segment.ayah_key === ayahKey) ?? null;
}
```

For an ayah-mode recitation, each returned track normally maps to the requested ayah. Use the segment if present to drive highlight timing; otherwise use the whole track duration. For a surah-mode recitation, keep one full-surah audio element and use each ayah segment to seek/highlight.

### Search Page

1. Fetch `GET /v1/quran/search?q={query}&lang=id&limit=20&offset=0`.
2. Use cached surah list to display surah names beside `ayah_key`.
3. Link result clicks to the surah reader with the target `ayah_key`.
4. Do not request audio from search. Fetch the target ayah/surah only after navigation.

### Kitab Quran References

1. On a reader section screen, prefer `GET /v1/books/{book_id}/toc/{heading_id}/read?lang=id&include_quran_references=true`.
2. If references are lazy-loaded separately, call `GET /v1/books/{book_id}/quran-references?lang=id&heading_id={headingId}&status=approved`.
3. Use `page_id` and `heading_id` to group references near kitab content.
4. Use `ayahs` when available for preview.
5. For surah-only references without `ayahs`, link to `/quran/surahs/{surah_id}` or the FE equivalent.

## Error Contract

All Quran errors use:

```json
{
  "error": "message",
  "code": "message",
  "message": "message",
  "request_id": "..."
}
```

`error` remains for backward compatibility. New clients should log `request_id` and may use `code` for stable branching.

Common Quran errors:

| Status | Error |
| --- | --- |
| `400` | `invalid include_info` |
| `400` | `invalid include_audio` |
| `400` | `invalid include_translation` |
| `400` | `invalid from` |
| `400` | `invalid to` |
| `400` | `invalid ayah key` |
| `400` | `invalid quran range` |
| `400` | `unsupported language` |
| `404` | `quran surah not found` |
| `404` | `quran ayah not found` |
| `404` | `quran recitation not found` |
| `404` | `quran translation source not found` |
| `404` | `book not found` |
| `500` | `internal server error` |

## Smoke Test URLs

Use these when checking local backend behavior:

```text
http://localhost:8080/v1/quran/surahs?lang=id
http://localhost:8080/v1/quran/surahs?lang=id&include_info=true
http://localhost:8080/v1/quran/surahs/73?lang=id
http://localhost:8080/v1/quran/recitations
http://localhost:8080/v1/quran/translation-sources?lang=id
http://localhost:8080/v1/quran/ayahs/1:1?lang=id&include_audio=true
http://localhost:8080/v1/quran/ayahs/1:1?lang=en
http://localhost:8080/v1/quran/ayahs/1:1?lang=fr
http://localhost:8080/v1/quran/ayahs/1:1?lang=id&include_audio=true&recitation_id=bad-id
http://localhost:8080/v1/quran/surahs/73/ayahs?from=1&to=5&lang=id&include_translation=true
http://localhost:8080/v1/quran/search?q=rahman&lang=id&limit=10&offset=0
```

Expected key checks:

- Surah list default should not include `info`.
- Single surah detail should include `info` when imported for the requested language.
- Recitations should include at most one `is_default=true`.
- Translation sources should include `coverage` and at most one `is_default=true` per language.
- Unsupported language should return `400 unsupported language`.
- Missing `lang=en` translation should keep Arabic text and expose `translation_missing` plus availability metadata.
- Ayah audio without `recitation_id` should use the default playable recitation.
- The audio player should use `public_url ?? audio_url`; local imports may have `public_url=null`.
- Bad explicit `recitation_id` should return `404 quran recitation not found`.

## Integration Checklist

- Use `/v1/quran/surahs` as the cacheable surah index.
- Use `/v1/quran/surahs/{surah_id}` for surah background HTML.
- Use `ayah_key` as the FE route/share key.
- Use `text_qpc_hafs` for Arabic display.
- Use `translation.text` for Indonesian translation.
- Use `GET /v1/quran/recitations` before building audio controls.
- Use `public_url ?? audio_url` for audio playback.
- Handle both `ayah` and `surah` audio tracks.
- Treat `segments` timestamps as milliseconds.
- Handle optional fields defensively; imported QUL payloads can vary by source.
- For kitab integration, keep Quran references separate from core kitab page/toc calls.
