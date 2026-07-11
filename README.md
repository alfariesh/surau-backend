# Surau Backend

REST + auth backend for an Islamic classical book reader. The service imports raw Shamela-like SQLite data from `/Users/macmini/Downloads/database` into PostgreSQL and serves catalog, page reader, heading/section reader, translation slots, audio slots, progress, and saved items.

## Runtime

- Go 1.26.3+ for builds. The Dockerfile pins `golang:1.26.3-alpine3.23`.
- Fiber REST API
- PostgreSQL via pgx
- JWT auth for profile, progress, and saved items
- Cloudflare Email Service for transactional email verification and password reset
- SQLite importer via `modernc.org/sqlite`
- Optional realtime collaborative page editing via a Yjs/Hocuspocus sidecar (`collab-server/`, Node 22) — see [docs/collab.md](docs/collab.md)

The runtime app wires only REST + Postgres + JWT; the template's RabbitMQ/NATS/gRPC transports were removed entirely (code and dependencies).

### Editorial source edits: concurrency & history

- Draft saves use atomic optimistic locking enforced in SQL. Page draft writes
  (`PUT .../pages/{page_id}/draft` and `POST .../publish`) **require**
  `If-Match` with the ETag from the last GET — stale ETags get `412`, a missing
  header gets `428`, `If-Match: *` is the explicit last-write-wins escape
  hatch. Metadata/heading saves accept If-Match optionally but enforce it
  atomically when present. The same applies to every production endpoint
  (translation/summary/audio drafts, project PATCH/publish/unpublish/delete):
  If-Match stays optional there — enrichment scripts save unconditionally —
  but a provided ETag is enforced atomically (412 on staleness).
- Every effective save snapshots into `book_source_edit_revisions`
  (page/heading/metadata; deduplicated, last 50 kept per resource).
  `GET /v1/editorial/books/{book_id}/pages/{page_id}/draft-revisions` lists
  history; `POST .../draft-revisions/{revision_id}/restore` replays a snapshot
  as a new draft.

## Main Endpoints

Public reader:

- `GET /healthz`
- `GET /readyz`
- `GET /v1/categories`
- `GET /v1/authors?q=&limit=&offset=`
- `GET /v1/books?q=&category_id=&author_id=&has_content=&limit=&offset=`
- `GET /v1/books/{book_id}`
- `GET /v1/books/{book_id}/pages`
- `GET /v1/books/{book_id}/pages/{page_id}`
- `GET /v1/books/{book_id}/headings?q=`
- `GET /v1/books/{book_id}/sections/{heading_id}?lang=id`
- `POST /v1/books/{book_id}/rag?lang=id`
- `GET /v1/books/{book_id}/toc?lang=id&include_audio=true`
- `GET /v1/books/{book_id}/toc/{heading_id}/read?lang=id`
- `GET /v1/books/{book_id}/toc/{heading_id}/playlist?lang=id`
- `GET /v1/books/{book_id}/quran-references?lang=id&status=approved`
- `POST /v1/books/{book_id}/toc/{heading_id}/translation-feedback?lang=id`

Public Quran:

- `GET /v1/quran/surahs?lang=id&include_info=false`
- `GET /v1/quran/surahs/{surah_id}?lang=id`
- `GET /v1/quran/recitations`
- `GET /v1/quran/translation-sources?lang=id`
- `GET /v1/quran/juz?lang=id`
- `GET /v1/quran/juz/{juz_number}/ayahs?lang=id&translation_source=&include_translation=true&include_audio=false&recitation_id=&view=reader_minimal`
- `GET /v1/quran/hizbs?lang=id`
- `GET /v1/quran/hizbs/{hizb_number}/ayahs?lang=id&translation_source=&include_translation=true&include_audio=false&recitation_id=&view=reader_minimal`
- `GET /v1/quran/ayahs/{ayah_key}?lang=id&translation_source=qul-kfgqpc-id-simple&include_audio=false&recitation_id=`
- `GET /v1/quran/surahs/{surah_id}/ayahs?from=&to=&lang=id&include_translation=true&include_audio=false&recitation_id=&view=reader_minimal`
- `GET /v1/quran/search?q=&lang=id&limit=&offset=`

Quran `lang` follows the same contract as kitab: supported `ar`, `id`, and `en`; empty defaults to `id`; region tags normalize to the primary language; unsupported explicit languages return `400 {"error":"unsupported language"}`. Quran Arabic text is always canonical source content. Translation and surah info are exact-language only; if `lang=en` is missing but `id` exists, the response keeps `translation`/`info` empty and exposes availability metadata for FE language offers.

`/v1/quran/surahs` is lightweight by default and omits `info`; use `include_info=true` or the single-surah endpoint when the UI needs surah background HTML. `/v1/quran/juz` and `/v1/quran/hizbs` expose lightweight navigation boundaries from imported QPC Hafs metadata. `/v1/quran/translation-sources` lists per-language sources with coverage and default markers. `/v1/quran/recitations` marks the deterministic playable default with `is_default=true`; playable means a track has `public_url` or source `audio_url`. If `include_audio=true` is requested without `recitation_id`, ayah endpoints use that default recitation; an unknown explicit `recitation_id` returns `404 quran recitation not found`.

Start mobile integration from [docs/mobile-backend-integration-guide.md](docs/mobile-backend-integration-guide.md). Start general FE integration from [docs/frontend-integration-contract.md](docs/frontend-integration-contract.md). For the editorial production module, use [docs/editorial-production-frontend-implementation.md](docs/editorial-production-frontend-implementation.md) as the detailed screen-by-screen implementation guide. See [docs/user-onboarding-api.md](docs/user-onboarding-api.md) for user profile, onboarding, and language preferences. See [docs/quran-api.md](docs/quran-api.md) for the full Quran API contract, response shapes, audio behavior, and integration checklist.

Auth and personal reader:

- `POST /v1/auth/register`
- `POST /v1/auth/login`
- `POST /v1/auth/verify-email`
- `POST /v1/auth/resend-verification`
- `POST /v1/auth/forgot-password`
- `POST /v1/auth/reset-password`
- `POST /v1/auth/change-password` (Bearer auth)
- `GET /v1/user/profile`
- `PATCH /v1/user/onboarding`
- `PATCH /v1/user/preferences`
- `GET /v1/me/progress/{book_id}`
- `PUT /v1/me/progress/{book_id}`
- `PUT /v1/me/progress/{book_id}/toc/{heading_id}`
- `GET /v1/me/quran/progress`
- `PUT /v1/me/quran/progress`
- `GET /v1/me/quran/progress/surahs`
- `GET /v1/me/quran/progress/surahs/{surah_id}`
- `GET /v1/me/saved-items?item_type=&book_id=&surah_id=&tag=&limit=&offset=`
- `POST /v1/me/saved-items`
- `PATCH /v1/me/saved-items/{id}`
- `DELETE /v1/me/saved-items/{id}`
- `GET /v1/me/saved-items/tags`

Editorial feedback review:

- `GET /v1/editorial/translation-feedbacks?book_id=&heading_id=&lang=&vote=&status=&limit=&offset=`
- `GET /v1/editorial/translation-feedbacks/summary?book_id=&heading_id=&lang=&vote=&status=&limit=`
- `POST /v1/editorial/translation-feedbacks/{id}/resolve`
- `POST /v1/editorial/translation-feedbacks/{id}/reopen`

Editorial book production:

- `GET /v1/editorial/production-dashboard?lang=&activity_limit=`
- `GET /v1/editorial/production-activity?lang=&limit=&offset=`
- `GET /v1/editorial/production-candidates?lang=&q=&category_id=&author_id=&has_content=&unstarted=&limit=&offset=`
- `POST /v1/editorial/production-projects`
- `GET /v1/editorial/production-projects?book_id=&lang=&workflow_status=&publication_status=&ready_to_publish=&needs_work=&limit=&offset=`
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
- Admin only: `POST /v1/editorial/production-projects/{id}/publish`, `POST /v1/editorial/production-projects/{id}/unpublish`, and `DELETE /v1/editorial/production-projects/{id}/.../final-assets/...`

Admin user management:

- `PATCH /v1/admin/users/role`

## Local Setup

```sh
make compose-up

go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate \
  -path migrations \
  -database 'postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db?sslmode=disable' up

APP_NAME=surau-backend \
APP_VERSION=1.0.0 \
HTTP_PORT=8080 \
HTTP_USE_PREFORK_MODE=false \
LOG_LEVEL=debug \
PG_POOL_MAX=4 \
PG_URL='postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db' \
METRICS_ENABLED=false \
SWAGGER_ENABLED=false \
JWT_SECRET=dev-secret-change-me-32-bytes-minimum \
JWT_TOKEN_EXPIRY=24h \
JWT_ISSUER=surau-backend \
JWT_AUDIENCE=surau-api \
AUTH_RATE_LIMIT_ENABLED=true \
AUTH_EMAIL_NOTIFICATIONS_ENABLED=true \
AUTH_FAILED_LOGIN_EMAIL_COOLDOWN=24h \
AUTH_NEW_LOGIN_EMAIL_ENABLED=true \
AUTH_FAILED_LOGIN_EMAIL_ENABLED=true \
AUTH_PASSWORD_CHANGED_EMAIL_ENABLED=true \
AUTH_EMAIL_VERIFIED_EMAIL_ENABLED=true \
AUTH_ROLE_CHANGED_EMAIL_ENABLED=true \
AUTH_EMAIL_CHANGED_EMAIL_ENABLED=true \
AUTH_ACCOUNT_DELETED_EMAIL_ENABLED=true \
EMAIL_DELIVERY_MODE=log \
CF_EMAIL_ACCOUNT_ID='your-cloudflare-account-id' \
CF_EMAIL_API_TOKEN='your-cloudflare-email-token' \
EMAIL_FROM_ADDRESS='noreply@yourdomain.com' \
EMAIL_FROM_NAME='Surau' \
EMAIL_VERIFY_FRONTEND_URL='http://localhost:3005/verify-email' \
EMAIL_VERIFICATION_TTL=24h \
EMAIL_VERIFICATION_OTP_TTL=10m \
EMAIL_RESEND_COOLDOWN=1m \
PASSWORD_RESET_FRONTEND_URL='http://localhost:3005/reset-password' \
PASSWORD_RESET_TTL=1h \
PASSWORD_RESET_RESEND_COOLDOWN=1m \
EMAIL_CHANGE_FRONTEND_URL='http://localhost:3005/change-email' \
EMAIL_CHANGE_TTL=24h \
EMAIL_CHANGE_OTP_TTL=10m \
EMAIL_CHANGE_RESEND_COOLDOWN=1m \
EMAIL_UNSUBSCRIBE_FRONTEND_URL='http://localhost:3005/unsubscribe' \
EMAIL_HTTP_TIMEOUT=10s \
RAG_LLM_API_KEY='your-openai-compatible-key' \
RAG_LLM_BASE_URL='https://ai.sumopod.com/v1' \
RAG_LLM_MODEL='glm-5.1' \
go run -tags migrate ./cmd/app
```

Set `EMAIL_DELIVERY_MODE=log` for local development to print verification, password reset, email-change, and admin test-send links in the backend logs instead of calling an external email provider. Email verification and email-change messages include both a link and a 6-digit OTP; the OTP defaults to a 10 minute TTL while links keep their longer token TTL. In production, use `EMAIL_DELIVERY_MODE=cloudflare`; email verification, password reset, email change, best-effort auth security notifications, and marketing campaigns then use Cloudflare Email Service REST API. The sending domain must be onboarded in Cloudflare Email Service with SPF, DKIM, DMARC, and bounce records configured before real email can be delivered. Admin email APIs are documented in [docs/admin-email-api.md](docs/admin-email-api.md).

Auth uses DB-backed rate limits for login, register, email verification resend, forgot/reset password, change password, change email, and delete account so limits work across multiple app instances. Password reset, password change, email change, and account delete increment `users.token_version`, which invalidates older JWTs on the next protected request. Sanitized auth events are written to `auth_audit_logs` for investigation; passwords, raw JWTs, and raw verification/reset/email-change tokens are never stored there. Optional security notifications cover password changed, email verified, email changed, account deleted, role changed, new login fingerprint, and suspicious failed login rate-limit events.

Frontend auth integration details are documented in [docs/auth-frontend.md](docs/auth-frontend.md), including endpoint contracts, error handling, token storage guidance, and verification/reset password flows.

Security scan baseline notes are documented in [docs/security-scan-baseline.md](docs/security-scan-baseline.md).

## Book RAG

The book RAG endpoint uses a PageIndex-like vectorless retrieval flow over the existing TOC and page range tables. It only serves published books and cites page-level source blocks.

```sh
curl -X POST 'http://127.0.0.1:8080/v1/books/797/rag?lang=id' \
  -H 'Content-Type: application/json' \
  -d '{"question":"Apa definisi hadis sahih?","max_citations":5}'
```

Set `RAG_LLM_API_KEY` for your OpenAI-compatible provider. Optional defaults are `RAG_LLM_BASE_URL=https://ai.sumopod.com/v1`, `RAG_LLM_MODEL=glm-5.1`, `RAG_LLM_TIMEOUT=45s`, `RAG_LLM_MAX_TOKENS=1400`, `RAG_LLM_TEMPERATURE=0.1`, `RAG_MAX_CONTEXT_PAGES=8`, `RAG_TREE_FULL_MAX_NODES=450`, `RAG_TREE_BLOCK_MAX_NODES=120`, `RAG_TREE_BEAM_SIZE=3`, `RAG_TREE_MAX_TURNS=6`, and `RAG_TREE_MAX_BLOCKS_PER_TURN=6`.

Reader TOC summaries can be generated separately with `scripts/generate_reader_summaries.py`. Generate canonical Arabic summaries first with `--summary-lang ar` and `--max-source-chars 0`, import them, then translate those summaries to `id` or `en` with `scripts/translate_reader_assets.py --summary-only`. The summary generator defaults to `SUMMARY_LLM_BASE_URL=https://ai.sumopod.com/v1`, `SUMMARY_LLM_MODEL=glm-5.1`, and falls back to the `RAG_LLM_*` environment if `SUMMARY_LLM_*` is not set. Summaries are stored per `(book_id, heading_id, lang)` for reader display and RAG tree ranking; citations still come from original page text.

Run the black-box golden eval against a local or deployed API:

```sh
go run ./cmd/rag-eval \
  -base-url http://127.0.0.1:8080 \
  -cases eval/bookrag_smoke.jsonl
```

The eval posts to `/v1/books/{book_id}/rag`, requests `include_trace=true`, and checks citation heading/page IDs, retrieval mode, tree LLM call budget, not-found behavior, and optional answer/quote substrings. Use `-output json` for CI-friendly output.
It retries failed cases once by default (`-retries 1`) to reduce one-off LLM sampling noise while still reporting the attempt count.
`answer_must_contain` is a warning by default because answer wording can vary; pass `-strict-answer` to make it a failure.
Use `-verbose` when debugging slow or invisible failures; it prints per-case start/finish lines to stderr while the eval is still running.
Use `eval/bookrag_golden.jsonl` for the fuller, costlier suite that includes medium-book and not-found cases.

## Import Raw Books

Sample import for one book:

```sh
PG_URL='postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db' \
go run ./cmd/import-books --book-ids=797 --release-key=sample-797
```

Full import:

```sh
PG_URL='postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db' \
go run ./cmd/import-books --release-key=full-YYYYMMDD
```

Full import has a disk preflight and defaults to requiring 30GiB free. Use `--limit` or `--book-ids` for sample imports.

### Re-import safety (staged diff + approval)

Re-imports are **non-destructive by default**: rows that disappeared from the
new source release are only *staged* (recorded in `book_import_removal_stages`
with the run id printed at the end) — nothing is deleted or hidden. To apply
them as **soft tombstones** (`is_deleted`, reversible, editorial/user data
untouched), review the staged list and re-run with the same source:

```sh
go run ./cmd/import-books --release-key=full-YYYYMMDD -approve-removals=<staged-run-id>
```

If the source changed between staging and approval the run aborts (drift
guard) — re-stage and review again. A row that reappears in a later release
automatically clears its tombstone.

## Import Translation/Audio Assets

`cmd/import-reader-assets` accepts JSONL records.

Translation:

```json
{"kind":"translation","book_id":797,"heading_id":10,"lang":"id","title":"Mukadimah","content":"...","source":"glm-5.1","translation_status":"generated","provenance_class":"machine","generation":{"run_id":"11111111-1111-4111-8111-111111111111","model_id":"glm-5.1","prompt_version":"reader-translation-v1"}}
```

Audio:

```json
{"kind":"audio","book_id":797,"heading_id":10,"lang":"id","url":"https://cdn.example/audio.mp3","mime_type":"audio/mpeg","duration_seconds":120}
```

Catalog metadata translation:

```json
{"kind":"book_metadata_translation","book_id":797,"lang":"id","display_title":"Judul Kitab","bibliography":"...","hint":"...","description":"...","source":"glm-5.1","translation_status":"generated","provenance_class":"machine","generation":{"run_id":"22222222-2222-4222-8222-222222222222","model_id":"glm-5.1","prompt_version":"catalog-translation-v1"}}
{"kind":"author_translation","author_id":177,"lang":"id","name":"Nama Penulis","biography":"...","death_text":"...","source":"glm-5.1","translation_status":"reviewed","translation_reviewed_by":"Editor A","provenance_class":"machine","generation":{"run_id":"22222222-2222-4222-8222-222222222222","model_id":"glm-5.1","prompt_version":"catalog-translation-v1"}}
{"kind":"category_translation","category_id":10,"lang":"id","name":"Ilmu Hadis","source":"glm-5.1","translation_status":"reviewed","translation_reviewed_by":"Editor B","provenance_class":"machine","generation":{"run_id":"22222222-2222-4222-8222-222222222222","model_id":"glm-5.1","prompt_version":"catalog-translation-v1"}}
```

Audio and section translations are keyed by TOC heading, not by page. Catalog translations are keyed by book, author, or category plus language. Every imported text row is machine enrichment and therefore requires `provenance_class=machine` plus `generation.run_id`, `model_id`, and the exact prompt version for its kind. The importer validates the full file first and commits run registration plus every row atomically; one invalid row aborts the file with its line number. Human review never removes the machine identity. Translation status is informational only: `generated` means LLM/import generated content, while `reviewed` requires `translation_reviewed_by` and is shown publicly as a reader label. It does not decide whether a book is published. Audio rows do not carry text provenance. See `examples/reader-assets.sample.jsonl` and `docs/generation-runs.md` for the complete contract.

Run:

```sh
PG_URL='postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db' \
go run ./cmd/import-reader-assets --file=examples/reader-assets.sample.jsonl
```

## Import Quran Assets

Quran data is a standalone domain sourced from local QUL exports. The app does not call QUL at runtime. Download the QUL files first, then import them:

```sh
PG_URL='postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db' \
go run ./cmd/import-quran-assets \
  --surah-names-json=/path/to/surahs.json \
  --surah-info-json=/path/to/surah-info-id.json \
  --surah-info-json=/path/to/surah-info-en.json \
  --script-qpc-hafs-json=/path/to/qpc-hafs.json \
  --script-imlaei-simple-json=/path/to/imlaei-simple.json \
  --translation-simple-json=/path/to/kfgqpc-id-simple.json \
  --translation-lang=id \
  --translation-source-id=qul-kfgqpc-id-simple \
  --translation-source-url=https://qul.tarteel.ai/resources/translation/173 \
  --translation-resource-id=173 \
  --translation-footnote-tags-json=/path/to/kfgqpc-id-footnotes.json \
  --recitation-json=/path/to/surah-recitation-yasser-al-dosari.zip \
  --recitation-json=/path/to/ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953.json.zip \
  --resolve-references
```

Use `--dry-run` to parse and count rows without writing. JSON files may be passed directly or as single-resource QUL `.zip` downloads. `--surah-info-json` is repeatable for multiple languages, and `--surah-info-lang` can override filename inference for a batch. `--translation-lang` defaults to `id` and supports `id` or `en` translation imports with a matching `--translation-source-id`; non-Indonesian translation imports must provide a source ID. `--translation-source-url`, `--translation-resource-id`, `--translation-format`, and `--translation-footnote-format` keep source metadata accurate for each QUL translation resource. V1 imports QPC Hafs display text, Imlaei/simple search text, language-specific surah information, translation source metadata, optional footnote/chunk payloads, and recitation timestamp metadata. If a QUL script export does not include ayah navigation fields, the importer fills `juz_number` and `hizb_number` from QUL's canonical Juz/Hizb metadata boundaries. Audio files themselves stay outside Postgres; `audio_url` remains a playable source fallback, while `r2_key` and `public_url` are prepared for later Cloudflare R2 ingestion.

After audio files are uploaded to Cloudflare R2, sync the manifest back into Postgres:

```sh
PG_URL='postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db' \
go run ./cmd/sync-quran-audio-r2 \
  --manifest-jsonl=tmp/quran-audio-r2-manifest.jsonl \
  --recitation-metadata-json=config/quran_recitation_metadata.json \
  --public-base-url=https://your-public-r2-base-url
```

Use `--dry-run` to validate manifest and recitation counts without writing. The sync is idempotent: it upserts missing recitations/tracks from the manifest, applies clean display metadata when `--recitation-metadata-json` is provided, and updates `r2_key/public_url`. If `--public-base-url` is omitted, the command updates `r2_key` only and leaves existing `public_url` values unchanged.

## Import Surah Editorial (SEO/SGE)

Surah-level editorial enrichment (keutamaan, asbabun nuzul, pokok kandungan, SEO meta) for the public `/surah/{slug}` pages is self-authored and loaded from JSON — independent of the QUL import above:

```sh
PG_URL='postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db' \
go run ./cmd/import-quran-surah-editorial \
  --editorial-json=/path/to/al-mulk.id.json \
  --editorial-json=/path/to/al-fatihah.id.json
```

Each file is an array of records keyed by `surah_id` + `lang`:

```json
[
  {
    "surah_id": 67,
    "lang": "id",
    "slug": "al-mulk",
    "chronological_order": 77,
    "ruku_count": 2,
    "meta_title": "Surah Al-Mulk: Keutamaan, Asbabun Nuzul & Pokok Kandungan",
    "meta_description": "Keutamaan Surah Al-Mulk, asbabun nuzul, dan ringkasan kandungannya.",
    "arti_nama": "Kerajaan",
    "keutamaan_html": "<p>...</p>",
    "asbabun_nuzul_html": "<p>...</p>",
    "pokok_kandungan_html": "<p>...</p>",
    "author_name": "Tim Surau",
    "reviewed_by": "Ustadz Fulan, Lc.",
    "reviewed_at": "2026-06-23T00:00:00Z",
    "license_status": "permitted"
  }
]
```

Only `surah_id` and `lang` are required; every other field is optional and uses a COALESCE upsert (an absent field keeps the existing value on re-import). `slug`/`chronological_order`/`ruku_count` update the `quran_surahs` row; the rest update `quran_surah_editorial` for that language. `license_status` defaults to `needs_review` — **set it to `permitted` to publish**, since the API and the public `/surah/{slug}` page only expose and index editorial that is `permitted`. `--editorial-json` is repeatable; use `--dry-run` to parse and count without writing.

## Generate Test Translations with DeepSeek

For quick multilingual reader testing, `scripts/translate_reader_assets.py` fetches Arabic TOC sections from the local backend and writes importer-compatible translation JSONL.

```sh
printf 'DEEPSEEK_API_KEY=your-key\n' > .env.local

python3 scripts/translate_reader_assets.py \
  --base-url http://127.0.0.1:8080 \
  --book-id 1 \
  --heading-id 1 \
  --target-lang id \
  --out tmp/book-1-id.jsonl

PG_URL='postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db' \
go run ./cmd/import-reader-assets --file=tmp/book-1-id.jsonl
```

Use `--target-lang en` for English, repeat `--heading-id` for multiple sections, or use `--all-toc --limit=5` for a small batch. The generated translation content is Markdown with a professional scholarly style, including blockquotes for Qur'an, hadith, or clearly quoted source speech.

Reader translation prompts are category-aware. `--profile auto` detects a
translation profile from book/category metadata, while `--profile fiqh`,
`--profile history`, and similar overrides are available for manual curation.
Generated JSONL metadata stores the profile and `style_version`.

See [`scripts/README.md`](scripts/README.md) for script-specific usage and the recommended translation batching strategy.

Catalog endpoints support an optional `lang` query parameter:

- `GET /v1/categories?lang=id`
- `GET /v1/authors?lang=id`
- `GET /v1/books?lang=id`
- `GET /v1/books/{book_id}?lang=id`

Supported kitab languages are `ar`, `id`, and `en`; empty `lang` defaults to `id`, and region tags such as `en-US` normalize to `en`. Unsupported languages return `400 {"error":"unsupported language"}`.

If a requested catalog translation does not exist, the API falls back to the raw Arabic metadata and includes `localization` metadata with `requested_lang`, `display_lang`, `is_fallback`, `available_langs`, per-field language hints, and nested `availability`. Section reader and TOC responses expose `requested_lang`, `title_lang`, `is_title_fallback`, `available_translation_langs`, `available_summary_langs`, `translation_missing`, and `availability.title|translation|summary|audio` action hints. Section translation content stays exact-language only: if `lang=en` is missing but `lang=id` exists, `translation` remains `null` and the frontend can offer `id` from `available_translation_langs`. Reader book list items distinguish source catalog visibility (`catalog_published`) from target-language production visibility (`production_published`, `production_status`).

See [docs/frontend-integration-contract.md](docs/frontend-integration-contract.md), [docs/user-onboarding-api.md](docs/user-onboarding-api.md), [docs/kitab-multilingual-api.md](docs/kitab-multilingual-api.md), and [docs/kitab-frontend-contract.md](docs/kitab-frontend-contract.md) before wiring FE fallback states.

See [docs/kitab-multilingual-api.md](docs/kitab-multilingual-api.md) for the multilingual kitab API contract and [docs/kitab-frontend-contract.md](docs/kitab-frontend-contract.md) for frontend consumption examples.

Reader translation feedback is a lightweight public signal, not editorial approval. Send `vote=like` for good sections, or `vote=dislike` with optional `reason` and `note` when a translation needs attention:

```sh
curl -X POST 'http://127.0.0.1:8080/v1/books/1/toc/5/translation-feedback?lang=id' \
  -H 'Content-Type: application/json' \
  -d '{"vote":"dislike","reason":"style","note":"Terasa terlalu literal.","client_id":"local-browser-id"}'
```

Allowed reasons: `inaccurate`, `unclear`, `style`, `typo`, `formatting`, `other`. `client_id` is optional, but lets the backend update the same reader's feedback instead of inserting duplicates.

Editorial feedback endpoints require an editor or admin JWT. Use the list endpoint for raw notes and the summary endpoint to prioritize review queues by most disliked heading. Feedback defaults to `status=open`; resolved feedback is hidden from default list/summary, `status=resolved` shows handled items, and `status=all` includes both.

Editorial reader localization gaps are available at `GET /v1/editorial/reader/missing-assets`. Filter with `target_lang=id|en`, `asset_type=book_metadata|category_metadata|author_metadata|section_translation|heading_summary|section_audio`, `book_id`, `limit`, and `offset`. Empty `target_lang` means both `id,en`; `target_lang=ar` is rejected because Arabic is source content.

Editorial Quran gaps are available at `GET /v1/editorial/quran/missing-assets`. Filter with `target_lang=id|en`, `asset_type=surah_info|ayah_translation|translation_source|audio_public`, `surah_id`, `limit`, and `offset`. Empty `target_lang` means both `id,en`; `target_lang=ar` is rejected because Arabic is source content.

Book translation production is managed through `book_id + lang` projects. Editors create a project from an existing raw Postgres kitab, fill drafts per metadata/author/category and per TOC heading for translation, summary, and optional audio, then submit/approve drafts when review is required. Admins publish only when completeness passes; publish upserts approved drafts into final reader tables and marks the project published. Unpublish hides the non-Arabic reader assets without deleting final rows.

Resolve a handled feedback item:

```sh
curl -X POST 'http://127.0.0.1:8080/v1/editorial/translation-feedbacks/{id}/resolve' \
  -H 'Authorization: Bearer <editor-or-admin-token>' \
  -H 'Content-Type: application/json' \
  -d '{"note":"Reworked wording and re-imported the section."}'
```

Run QA before import:

```sh
python3 scripts/qa_reader_assets.py --file tmp/book-1-id.jsonl --book-id 1 --lang id
python3 scripts/qa_catalog_assets.py --file tmp/catalog-id.jsonl --lang id
```

## Tests

```sh
go test ./...
```

Integration tests are opt-in:

```sh
RUN_INTEGRATION_TESTS=1 INTEGRATION_HTTP_URL=http://localhost:8080 go test ./integration-test/...
```
