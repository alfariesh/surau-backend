# Surau Backend

REST + auth backend for an Islamic classical book reader. The service imports raw Shamela-like SQLite data from `/Users/macmini/Downloads/database` into PostgreSQL and serves catalog, page reader, heading/section reader, translation slots, audio slots, progress, and bookmarks.

## Runtime

- Go 1.26
- Fiber REST API
- PostgreSQL via pgx
- JWT auth for profile, progress, and bookmarks
- SQLite importer via `modernc.org/sqlite`

The runtime app no longer starts RabbitMQ, NATS, or gRPC. Legacy packages may still compile in the tree, but `cmd/app` wires only REST + Postgres + JWT.

## Main Endpoints

Public reader:

- `GET /healthz`
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
{"kind":"translation","book_id":797,"heading_id":10,"lang":"id","title":"Mukadimah","content":"...","source":"manual"}
```

Audio:

```json
{"kind":"audio","book_id":797,"heading_id":10,"lang":"id","url":"https://cdn.example/audio.mp3","mime_type":"audio/mpeg","duration_seconds":120}
```

Audio and translations are keyed by TOC heading, not by page. See `examples/reader-assets.sample.jsonl` for a ready-to-edit template with Indonesian translation and Arabic/Indonesian audio rows.

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

## Tests

```sh
go test ./...
```

Integration tests are opt-in:

```sh
RUN_INTEGRATION_TESTS=1 INTEGRATION_HTTP_URL=http://localhost:8080 go test ./integration-test/...
```
