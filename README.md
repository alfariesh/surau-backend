# Surau Backend

REST + auth backend for an Islamic classical book reader. The service imports raw Shamela-like SQLite data from `/Users/macmini/Downloads/database` into PostgreSQL and serves catalog, page reader, heading/section reader, translation slots, audio slots, progress, and bookmarks.

## Runtime

- Go 1.26.3+ for builds. The Dockerfile pins `golang:1.26.3-alpine3.23`.
- Fiber REST API
- PostgreSQL via pgx
- JWT auth for profile, progress, and bookmarks
- SQLite importer via `modernc.org/sqlite`

The runtime app no longer starts RabbitMQ, NATS, or gRPC. Legacy packages may still compile in the tree, but `cmd/app` wires only REST + Postgres + JWT.

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
- `GET /v1/books/{book_id}/toc?lang=id&include_audio=true`
- `GET /v1/books/{book_id}/toc/{heading_id}/read?lang=id`
- `GET /v1/books/{book_id}/toc/{heading_id}/playlist?lang=id`
- `POST /v1/books/{book_id}/toc/{heading_id}/translation-feedback?lang=id`

Auth and personal reader:

- `POST /v1/auth/register`
- `POST /v1/auth/login`
- `GET /v1/user/profile`
- `GET /v1/me/progress/{book_id}`
- `PUT /v1/me/progress/{book_id}`
- `PUT /v1/me/progress/{book_id}/toc/{heading_id}`
- `GET /v1/me/bookmarks`
- `POST /v1/me/bookmarks`
- `POST /v1/me/bookmarks/toc/{book_id}/{heading_id}`
- `DELETE /v1/me/bookmarks/{id}`

Admin feedback review:

- `GET /v1/admin/translation-feedbacks?book_id=&heading_id=&lang=&vote=&status=&limit=&offset=`
- `GET /v1/admin/translation-feedbacks/summary?book_id=&heading_id=&lang=&vote=&status=&limit=`
- `POST /v1/admin/translation-feedbacks/{id}/resolve`
- `POST /v1/admin/translation-feedbacks/{id}/reopen`

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
JWT_SECRET=dev-secret \
JWT_TOKEN_EXPIRY=24h \
go run -tags migrate ./cmd/app
```

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

## Import Translation/Audio Assets

`cmd/import-reader-assets` accepts JSONL records.

Translation:

```json
{"kind":"translation","book_id":797,"heading_id":10,"lang":"id","title":"Mukadimah","content":"...","source":"manual","translation_status":"generated"}
```

Audio:

```json
{"kind":"audio","book_id":797,"heading_id":10,"lang":"id","url":"https://cdn.example/audio.mp3","mime_type":"audio/mpeg","duration_seconds":120}
```

Catalog metadata translation:

```json
{"kind":"book_metadata_translation","book_id":797,"lang":"id","display_title":"Judul Kitab","bibliography":"...","hint":"...","description":"...","source":"manual","translation_status":"generated"}
{"kind":"author_translation","author_id":177,"lang":"id","name":"Nama Penulis","biography":"...","death_text":"...","source":"manual","translation_status":"reviewed","translation_reviewed_by":"Editor A"}
{"kind":"category_translation","category_id":10,"lang":"id","name":"Ilmu Hadis","source":"manual","translation_status":"reviewed","translation_reviewed_by":"Editor B"}
```

Audio and section translations are keyed by TOC heading, not by page. Catalog translations are keyed by book, author, or category plus language. Translation status is informational only: `generated` means LLM/import generated content, while `reviewed` requires `translation_reviewed_by` and is shown publicly as a reader label. It does not decide whether a book is published. See `examples/reader-assets.sample.jsonl` for a ready-to-edit template with section, audio, and catalog rows.

Run:

```sh
PG_URL='postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db' \
go run ./cmd/import-reader-assets --file=examples/reader-assets.sample.jsonl
```

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

See [scripts/README.md](/Users/macmini/Downloads/surau-backend/scripts/README.md) for script-specific usage and the recommended translation batching strategy.

Catalog endpoints support an optional `lang` query parameter:

- `GET /v1/categories?lang=id`
- `GET /v1/authors?lang=id`
- `GET /v1/books?lang=id`
- `GET /v1/books/{book_id}?lang=id`

If a requested catalog translation does not exist, the API falls back to the raw Arabic metadata. When a translation exists, public responses include `translation_status`, `translation_reviewed_by`, and `translation_reviewed_at` where available. Section reader and TOC responses expose the same label fields for generated or reviewed translations.

Reader translation feedback is a lightweight public signal, not editorial approval. Send `vote=like` for good sections, or `vote=dislike` with optional `reason` and `note` when a translation needs attention:

```sh
curl -X POST 'http://127.0.0.1:8080/v1/books/1/toc/5/translation-feedback?lang=id' \
  -H 'Content-Type: application/json' \
  -d '{"vote":"dislike","reason":"style","note":"Terasa terlalu literal.","client_id":"local-browser-id"}'
```

Allowed reasons: `inaccurate`, `unclear`, `style`, `typo`, `formatting`, `other`. `client_id` is optional, but lets the backend update the same reader's feedback instead of inserting duplicates.

Admin feedback endpoints require an admin JWT. Use the list endpoint for raw notes and the summary endpoint to prioritize review queues by most disliked heading. Feedback defaults to `status=open`; resolved feedback is hidden from default list/summary, `status=resolved` shows handled items, and `status=all` includes both.

Resolve a handled feedback item:

```sh
curl -X POST 'http://127.0.0.1:8080/v1/admin/translation-feedbacks/{id}/resolve' \
  -H 'Authorization: Bearer <admin-token>' \
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
