# Scripts

Operational scripts live here. Each new script should have a section in this
README that explains its purpose, required environment variables, safe test
command, production command, and output files.

Do not commit secrets. Local secrets belong in the project-level `.env.local`
file, which is ignored by git.

## `translate_reader_assets.py`

Generates `cmd/import-reader-assets` JSONL translation records from Surau TOC
sections. The script fetches Arabic content from the local backend, sends one
TOC section to DeepSeek, and writes one JSONL row per `(book_id, heading_id,
lang)`.

### Environment

Create `/Users/macmini/Downloads/surau-backend/.env.local`:

```env
DEEPSEEK_API_KEY=sk-...
```

Optional:

```env
DEEPSEEK_MODEL=deepseek-v4-flash
DEEPSEEK_BASE_URL=https://api.deepseek.com
```

### Smoke Test Without LLM

```sh
python3 scripts/translate_reader_assets.py \
  --base-url http://127.0.0.1:8080 \
  --book-id 1 \
  --heading-id 5 \
  --target-lang id \
  --out /tmp/surau-translation-dryrun.jsonl \
  --dry-run
```

### Live Test

```sh
python3 scripts/translate_reader_assets.py \
  --base-url http://127.0.0.1:8080 \
  --book-id 1 \
  --heading-id 5 \
  --target-lang id \
  --max-source-chars 2500 \
  --out /tmp/surau-book-1-heading-5-id.jsonl
```

### Full Book Queue

Translate every TOC section in one book as queued section-level jobs:

```sh
python3 scripts/translate_reader_assets.py \
  --base-url http://127.0.0.1:8080 \
  --book-id 1 \
  --all-toc \
  --target-lang id \
  --concurrency 10 \
  --resume \
  --sleep-seconds 0.2 \
  --max-source-chars 0 \
  --out /tmp/surau-book-1-id.jsonl
```

Notes:

- `--all-toc` fetches the TOC once, then queues every heading.
- `--concurrency 10` runs up to ten heading translations at the same time.
- `--resume` appends to the output file and skips headings already present.
- `--max-source-chars 0` means do not truncate source text. Use a positive
  value only for cheap sampling tests.
- Failures are written to `/tmp/surau-book-1-id.jsonl.failures.jsonl` by
  default, while successful rows remain importable.
- JSONL row order is not guaranteed when `--concurrency` is greater than `1`;
  import does not require ordered rows.

Import a generated JSONL file. For the full-book example above:

```sh
PG_URL='postgres://user:myAwEsOm3pa55%40w0rd@localhost:5432/db?sslmode=disable' \
go run ./cmd/import-reader-assets --file=/tmp/surau-book-1-id.jsonl
```

Check the reader:

```sh
curl 'http://127.0.0.1:8080/v1/books/1/toc/5/read?lang=id'
```

### Translation Strategy

Do not translate a full book and two languages in one LLM call, even when the
context window looks large. Use TOC sections as the unit of work.

Recommended pipeline:

1. Translate one `(book_id, heading_id, lang)` per call.
2. Run Indonesian and English as separate jobs.
3. For long sections, split by paragraph or page boundary and merge only after
   a QA pass.
4. Keep a per-book glossary/style guide and pass it into every section call.
5. Import generated rows as draft/editorial assets, review them, then publish.

In practical terms, "full book" means one command that queues all TOC headings,
not one LLM request containing the entire book.

Reasons:

- LLM output limits are the real bottleneck. A large context window helps with
  input, but one full-book translation can still exceed maximum output.
- Section-level jobs are retryable. One failed heading should not force a whole
  book to be regenerated.
- Editorial review is naturally per chapter or subchapter.
- Separate language jobs avoid Indonesian and English style bleeding into each
  other.
- Audio is also keyed by TOC heading, so translations should use the same unit.

For production batches, start with a small curated collection, translate only
published or review-target books, and store generated files under a dated run
directory outside git, for example `/tmp/surau-translation-runs/2026-05-23/`.
