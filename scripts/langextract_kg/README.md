# LangExtract Knowledge Extraction

DB-backed extraction pipeline for Surau reader pages.

## Environment

Create `.env.local` at the repo root:

```env
PG_URL=postgres://user:password@localhost:5432/db?sslmode=disable
LANGEXTRACT_LLM_BASE_URL=https://ai.sumopod.com/v1
LANGEXTRACT_LLM_MODEL=glm-5.1
LANGEXTRACT_LLM_API_KEY=<your-api-key>
```

`LANGEXTRACT_LLM_API_KEY` falls back to `RAG_LLM_API_KEY`.

Install local Python dependencies:

```sh
python3 -m pip install -r scripts/langextract_kg/requirements.txt
```

## Dry Run

Dry run reads PostgreSQL source pages and writes review files, but does not
write knowledge tables:

```sh
python3 scripts/langextract_kg/extract_knowledge.py \
  --book-id 797 \
  --page-id 4 \
  --page-id 5 \
  --page-id 6 \
  --task mentions \
  --dry-run \
  --out-dir /tmp/surau-langextract-kg
```

## DB Write

Run one page first:

```sh
python3 scripts/langextract_kg/extract_knowledge.py \
  --book-id 797 \
  --page-id 4 \
  --task mentions \
  --write-db \
  --out-dir /tmp/surau-langextract-kg
```

Then QA stored rows:

```sh
python3 scripts/langextract_kg/qa_extractions.py --run-id <run_uuid>
```

## Notes

- Source text comes from `book_pages.content_text`; raw reader tables are not modified.
- `glm-5.1` uses the local `OpenAICompatibleJSONModel` adapter because the
  installed LangExtract package is 1.3.0 and does not expose the newer OpenAI
  schema provider available in `temp-langextract`.
- Common person names such as `أحمد`, `محمد`, `علي`, and `أبو بكر` are stored as ambiguous mentions and are not auto-merged.
- `book_title` is treated as a legacy class. New mention extraction uses
  `work_title`; Quran surah references belong in `citations` as
  `quran_reference`.
- DB writes store prompt versions, document/chunk audit rows, source spans, and
  rejected extractions alongside grounded mentions.
- Relations are disabled unless `--enable-relations` is passed.
