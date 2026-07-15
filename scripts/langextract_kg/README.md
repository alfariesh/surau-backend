# LangExtract Knowledge Extraction

DB-backed extraction pipeline for Surau reader pages.

## Environment

Create `.env.local` at the repo root:

```env
LANGEXTRACT_PG_URL=postgres://surau_extraction_YYYYMM_b:password@localhost:5432/db?sslmode=disable
LANGEXTRACT_LLM_BASE_URL=https://ai.sumopod.com/v1
LANGEXTRACT_LLM_MODEL=glm-5.1
LANGEXTRACT_LLM_API_KEY=<your-api-key>
```

`LANGEXTRACT_LLM_API_KEY` falls back to `RAG_LLM_API_KEY`.
`LANGEXTRACT_PG_URL` must be a login that belongs only to the
`surau_extraction_writer` group. The pipeline has no review-status column
grants, creates machine rows as `pending`, and only updates conflicts that are
still pending. Owner `PG_URL` fallback exists only during one A/B cutover when
`ALLOW_LEGACY_DB_CREDENTIALS=true`; remove both from the job afterward. See
[`docs/service-identity-rotation.md`](../../docs/service-identity-rotation.md).

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
- Every DB-backed extraction creates one UUID that is shared by
  `generation_runs` and `knowledge_extraction_runs`. Registration and
  extraction-run creation happen in one transaction; a reused UUID with a
  different task/model/prompt/provider descriptor is rejected.
- Mention, chunk-audit, and rejection JSONL rows carry
  `provenance_class=machine` plus the typed `generation` model/prompt/run tuple,
  including runs without `--write-db`. QA rejects missing, malformed, or
  conflicting identities before the files are accepted.
- Raw `*.langextract.jsonl` review documents carry the same typed identity on
  every line. Each `*.raw_chunks/chunk-*.json` wraps the original model output
  with that identity; the LangExtract visualizer remains compatible with the
  additive document fields.
- The run records the exact prompt version from `prompts.py` (`mentions_v2`,
  `terms_v2`, `citations_v3`, or `relations_v1`) and the configured model.
  New machine knowledge rows must never be written without that run identity.
- `glm-5.1` uses the local `OpenAICompatibleJSONModel` adapter because the
  installed LangExtract package is 1.3.0 and does not expose the newer OpenAI
  schema provider available in `temp-langextract`.
- Common person names such as `أحمد`, `محمد`, `علي`, and `أبو بكر` are stored as ambiguous mentions and are not auto-merged.
- `book_title` is treated as a legacy class. New mention extraction uses
  `work_title`; Quran surah references belong in `citations` as
  `quran_reference`.
- DB writes store prompt versions, document/chunk audit rows, source spans, and
  rejected extractions alongside grounded mentions.
- Persisted normalized mention/entity/alias fields use the shared
  `search-key` v1 contract and write `normalization_version=1` atomically.
  `normalized_grounding_key` remains separate because fallback grounding must
  preserve source-span mapping. The shared Go-Python corpus and legacy rules
  are documented in [`docs/arabic-normalization.md`](../../docs/arabic-normalization.md).
- Relations are disabled unless `--enable-relations` is passed.
