#!/usr/bin/env python3
"""Run grounded LangExtract extraction over Surau reader pages."""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
from importlib import metadata
import json
import os
from pathlib import Path
import sys
from typing import Any

import langextract as lx
from langextract import chunking as lx_chunking
from langextract.core import tokenizer as lx_tokenizer
from langextract.prompt_validation import PromptValidationLevel

if __package__ in (None, ""):
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    from langextract_kg import db as kg_db  # type: ignore
    from langextract_kg.arabic_normalize import (  # type: ignore
        is_ambiguous_person_name,
        is_generic_extraction,
        is_person_reference,
        is_surah_reference,
        is_theonym,
        normalized_key,
    )
    from langextract_kg.openai_compatible_model import OpenAICompatibleJSONModel  # type: ignore
    from langextract_kg.prompts import get_prompt  # type: ignore
    from langextract_kg.visualize_run import write_visualization  # type: ignore
else:
    from . import db as kg_db
    from .arabic_normalize import (
        is_ambiguous_person_name,
        is_generic_extraction,
        is_person_reference,
        is_surah_reference,
        is_theonym,
        normalized_key,
    )
    from .openai_compatible_model import OpenAICompatibleJSONModel
    from .prompts import get_prompt
    from .visualize_run import write_visualization


DEFAULT_OUT_DIR = Path("/tmp/surau-langextract-kg")


def main() -> int:
    args = parse_args()
    kg_db.load_env_file(Path(args.env_file).expanduser())
    resolve_llm_config(args)

    if args.task == "relations" and not args.enable_relations:
        raise SystemExit("--task relations is disabled by default; pass --enable-relations to run it")

    api_key = os.environ.get(args.api_key_env) or os.environ.get("RAG_LLM_API_KEY", "")
    if not api_key:
        raise SystemExit(
            f"{args.api_key_env} or RAG_LLM_API_KEY is required. Put it in {args.env_file} "
            "or export it first."
        )

    prompt = get_prompt(args.task)
    run_id = args.run_id or kg_db.new_uuid()
    args.run_id = run_id
    out_dir = Path(args.out_dir).expanduser()
    out_dir.mkdir(parents=True, exist_ok=True)

    client = kg_db.DBClient.connect(args.pg_url)
    try:
        skip_pages: set[int] = set()
        if args.resume:
            skip_pages = client.already_processed_page_ids(
                book_id=args.book_id,
                task_name=args.task,
                prompt_version=prompt.version,
            )

        pages = client.fetch_pages(
            book_id=args.book_id,
            page_ids=args.page_id or [],
            limit=args.limit,
            skip_page_ids=skip_pages,
        )
        if not pages:
            raise SystemExit("No source pages selected.")

        run_record = build_run_record(args, prompt.version, run_id, len(pages))
        if args.write_db:
            client.register_prompt_version(prompt)
            client.create_run(run_record)

        result = run_extraction(args, prompt, pages, api_key)
        records, duplicate_failures = dedupe_records_with_rejections(result["records"])
        annotated_docs = result["annotated_docs"]
        failures = [*result["failures"], *duplicate_failures]
        documents_audit = result["documents_audit"]
        assign_failure_chunks(failures, result["chunks_audit"])
        chunks_audit = attach_chunk_counts(result["chunks_audit"], records, failures)

        jsonl_path = out_dir / f"{run_id}.{args.task}.mentions.jsonl"
        chunks_jsonl_path = out_dir / f"{run_id}.{args.task}.chunks.jsonl"
        rejections_jsonl_path = out_dir / f"{run_id}.{args.task}.rejections.jsonl"
        write_jsonl(jsonl_path, records)
        write_jsonl(chunks_jsonl_path, chunks_audit)
        write_jsonl(rejections_jsonl_path, failures)
        raw_jsonl_path, html_path = write_visualization(
            annotated_docs,
            out_dir=out_dir,
            output_stem=f"{run_id}.{args.task}.langextract",
            show_progress=False,
        )

        stored = 0
        status = "success"
        if failures:
            status = "completed_with_errors"
        if args.write_db:
            document_audit_ids = client.insert_extraction_documents(documents_audit)
            chunk_ids = client.insert_extraction_chunks(chunks_audit, document_audit_ids)
            stored = client.insert_mentions_with_candidates(records)
            client.insert_extraction_rejections(failures, chunk_ids)
            client.finish_run(
                run_id,
                status=status,
                processed_documents=len(annotated_docs),
                stored_mentions=stored,
                errors=failures,
            )

        print(
            json.dumps(
                {
                    "run_id": run_id,
                    "task": args.task,
                    "prompt_version": prompt.version,
                    "pages": len(pages),
                    "records": len(records),
                    "rejections": len(failures),
                    "stored_mentions": stored,
                    "jsonl": str(jsonl_path),
                    "chunks_jsonl": str(chunks_jsonl_path),
                    "rejections_jsonl": str(rejections_jsonl_path),
                    "langextract_jsonl": str(raw_jsonl_path),
                    "html": str(html_path),
                    "write_db": bool(args.write_db),
                    "status": status,
                },
                ensure_ascii=False,
            )
        )
        return 0 if not failures else 1
    except Exception:
        if args.write_db:
            try:
                client.finish_run(
                    run_id,
                    status="failed",
                    processed_documents=0,
                    stored_mentions=0,
                    errors=[{"error": "pipeline failed; see stderr"}],
                )
            except Exception:
                pass
        raise
    finally:
        client.close()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pg-url", default="", help="PostgreSQL URL; defaults to PG_URL")
    parser.add_argument("--book-id", required=True, type=int, help="Source book ID")
    parser.add_argument("--page-id", action="append", type=int, help="Source page ID; repeat for many")
    parser.add_argument("--limit", type=int, default=0, help="Limit selected pages; 0 means no limit")
    parser.add_argument(
        "--task",
        choices=["mentions", "terms", "citations", "relations"],
        default="mentions",
        help="Extraction task",
    )
    parser.add_argument("--dry-run", action="store_true", help="Alias for not writing DB rows")
    parser.add_argument("--write-db", action="store_true", help="Persist run, mentions, and candidates")
    parser.add_argument("--resume", action="store_true", help="Skip pages already processed for this task/prompt")
    parser.add_argument("--out-dir", default=str(DEFAULT_OUT_DIR), help="Output directory for JSONL/HTML review files")
    parser.add_argument("--model", default=None, help="LLM model; defaults to LANGEXTRACT_LLM_MODEL or glm-5.1")
    parser.add_argument("--llm-base-url", default=None, help="OpenAI-compatible base URL")
    parser.add_argument(
        "--api-key-env",
        default="LANGEXTRACT_LLM_API_KEY",
        help="Environment variable containing the LLM API key",
    )
    parser.add_argument("--env-file", default=str(kg_db.DEFAULT_ENV_FILE), help="Local dotenv file")
    parser.add_argument("--run-id", default="", help="Optional UUID for deterministic reruns")
    parser.add_argument("--max-char-buffer", type=int, default=1400)
    parser.add_argument("--context-window-chars", type=int, default=250)
    parser.add_argument("--extraction-passes", type=int, default=2)
    parser.add_argument("--batch-length", type=int, default=8)
    parser.add_argument("--max-workers", type=int, default=4)
    parser.add_argument("--max-output-tokens", type=int, default=1800)
    parser.add_argument("--temperature", type=float, default=0.0)
    parser.add_argument("--enable-relations", action="store_true", help="Allow high-risk relation extraction")
    return parser.parse_args()


def resolve_llm_config(args: argparse.Namespace) -> None:
    if not args.pg_url:
        args.pg_url = kg_db.postgres_url_from_env()
    if not args.model:
        args.model = os.environ.get("LANGEXTRACT_LLM_MODEL") or os.environ.get("RAG_LLM_MODEL") or "glm-5.1"
    if not args.llm_base_url:
        args.llm_base_url = (
            os.environ.get("LANGEXTRACT_LLM_BASE_URL")
            or os.environ.get("RAG_LLM_BASE_URL")
            or "https://ai.sumopod.com/v1"
        )
    if args.dry_run:
        args.write_db = False


def build_run_record(args: argparse.Namespace, prompt_version: str, run_id: str, total_docs: int) -> dict[str, Any]:
    return {
        "id": run_id,
        "task_name": args.task,
        "prompt_version": prompt_version,
        "model_id": args.model,
        "provider": "openai",
        "provider_base_url": args.llm_base_url,
        "parameters": {
            "max_char_buffer": args.max_char_buffer,
            "context_window_chars": args.context_window_chars,
            "extraction_passes": args.extraction_passes,
            "batch_length": args.batch_length,
            "max_workers": args.max_workers,
            "max_output_tokens": args.max_output_tokens,
            "temperature": args.temperature,
            "created_at": datetime.now(timezone.utc).isoformat(),
            "langextract": langextract_runtime_info(),
            "prompt_policy_hash": getattr(get_prompt(args.task), "policy_hash", ""),
        },
        "source_scope": {
            "book_id": args.book_id,
            "page_ids": args.page_id or [],
            "limit": args.limit,
            "unit": "page",
        },
        "status": "running",
        "total_documents": total_docs,
    }


def run_extraction(
    args: argparse.Namespace,
    prompt: Any,
    pages: list[kg_db.PageSource],
    api_key: str,
) -> dict[str, Any]:
    raw_audits: list[dict[str, Any]] = []
    model = OpenAICompatibleJSONModel(
        model_id=args.model,
        api_key=api_key,
        base_url=args.llm_base_url,
        temperature=args.temperature,
        max_output_tokens=args.max_output_tokens,
        max_workers=args.max_workers,
        audit_sink=raw_audits,
    )
    tokenizer = lx_tokenizer.RegexTokenizer()
    documents = [
        lx.data.Document(
            text=page.content_text,
            document_id=document_id_for_page(page.book_id, page.page_id),
        )
        for page in pages
    ]
    page_by_doc = {doc.document_id: page for doc, page in zip(documents, pages)}
    documents_audit = build_document_audits(args.run_id, pages)
    chunks_audit = build_chunk_audits(
        args=args,
        documents=documents,
        raw_audits=raw_audits,
        tokenizer=tokenizer,
    )

    extracted = lx.extract(
        text_or_documents=documents,
        prompt_description=prompt.description,
        examples=prompt.examples,
        model=model,
        temperature=args.temperature,
        extraction_passes=args.extraction_passes,
        max_char_buffer=args.max_char_buffer,
        context_window_chars=args.context_window_chars,
        batch_length=args.batch_length,
        max_workers=args.max_workers,
        resolver_params={
            "suppress_parse_errors": True,
            "enable_fuzzy_alignment": True,
            "fuzzy_alignment_threshold": 0.75,
            "fuzzy_alignment_min_density": 0.45,
            "accept_match_lesser": False,
        },
        prompt_validation_level=PromptValidationLevel.ERROR,
        prompt_validation_strict=True,
        use_schema_constraints=False,
        show_progress=True,
        tokenizer=tokenizer,
    )
    hydrate_chunk_audits(chunks_audit, raw_audits, Path(args.out_dir).expanduser(), args.run_id, args.task)
    annotated_docs = extracted if isinstance(extracted, list) else [extracted]
    records: list[dict[str, Any]] = []
    failures: list[dict[str, Any]] = []
    for adoc in annotated_docs:
        page = page_by_doc.get(adoc.document_id)
        if page is None:
            failures.append({"document_id": adoc.document_id, "error": "missing source page mapping"})
            continue
        page_records, page_failures = records_from_annotated_doc(args.run_id, adoc, page)
        records.extend(page_records)
        failures.extend(page_failures)
    assign_failure_chunks(failures, chunks_audit)
    return {
        "annotated_docs": annotated_docs,
        "records": records,
        "failures": failures,
        "documents_audit": documents_audit,
        "chunks_audit": chunks_audit,
    }


def document_id_for_page(book_id: int, page_id: int) -> str:
    return f"book:{book_id}:page:{page_id}"


def langextract_runtime_info() -> dict[str, str]:
    try:
        version = metadata.version("langextract")
    except metadata.PackageNotFoundError:
        version = "unknown"
    return {
        "version": version,
        "path": str(getattr(lx, "__file__", "") or ""),
    }


def build_document_audits(run_id: str, pages: list[kg_db.PageSource]) -> list[dict[str, Any]]:
    runtime = langextract_runtime_info()
    return [
        {
            "id": kg_db.new_uuid(),
            "run_id": run_id,
            "document_id": document_id_for_page(page.book_id, page.page_id),
            "book_id": page.book_id,
            "page_id": page.page_id,
            "heading_id": page.heading_id,
            "source_hash": hashlib.sha256(page.content_text.encode("utf-8")).hexdigest(),
            "char_count": len(page.content_text),
            "tokenizer": "RegexTokenizer",
            "langextract_version": runtime["version"],
            "langextract_path": runtime["path"],
            "metadata": {"heading_title": page.heading_title},
        }
        for page in pages
    ]


def build_chunk_audits(
    *,
    args: argparse.Namespace,
    documents: list[Any],
    raw_audits: list[dict[str, Any]],
    tokenizer: lx_tokenizer.Tokenizer,
) -> list[dict[str, Any]]:
    del raw_audits
    chunks: list[dict[str, Any]] = []
    for pass_index in range(max(1, int(args.extraction_passes or 1))):
        for document in documents:
            chunk_iter = lx_chunking.ChunkIterator(
                text=document.text or "",
                max_char_buffer=args.max_char_buffer,
                tokenizer_impl=tokenizer,
                document=document,
            )
            for chunk_index, chunk in enumerate(chunk_iter):
                chunks.append(
                    {
                        "id": kg_db.new_uuid(),
                        "run_id": args.run_id,
                        "document_id": document.document_id,
                        "chunk_index": chunk_index,
                        "pass_index": pass_index,
                        "char_start": chunk.char_interval.start_pos,
                        "char_end": chunk.char_interval.end_pos,
                        "token_start": chunk.token_interval.start_index,
                        "token_end": chunk.token_interval.end_index,
                        "parse_status": "unknown",
                        "metadata": {
                            "max_char_buffer": args.max_char_buffer,
                            "context_window_chars": args.context_window_chars,
                        },
                    }
                )
    return chunks


def hydrate_chunk_audits(
    chunks: list[dict[str, Any]],
    raw_audits: list[dict[str, Any]],
    out_dir: Path,
    run_id: str,
    task: str,
) -> None:
    raw_dir = out_dir / f"{run_id}.{task}.raw_chunks"
    raw_dir.mkdir(parents=True, exist_ok=True)
    audits_by_index = {int(audit["request_index"]): audit for audit in raw_audits}
    for index, chunk in enumerate(chunks):
        audit = audits_by_index.get(index)
        if not audit:
            continue
        raw_path = raw_dir / f"chunk-{index:05d}.json"
        raw_path.write_text(str(audit.get("raw_output") or ""), encoding="utf-8")
        chunk.update(
            {
                "prompt_hash": audit.get("prompt_hash"),
                "output_hash": audit.get("output_hash"),
                "raw_output_path": str(raw_path),
                "parse_status": audit.get("parse_status") or "unknown",
                "error_message": audit.get("error_message") or None,
                "metadata": {
                    **(chunk.get("metadata") or {}),
                    "request_index": index,
                    "retry_count": audit.get("retry_count", 0),
                    "normalized_output_hash": audit.get("normalized_output_hash"),
                },
            }
        )


def attach_chunk_counts(
    chunks: list[dict[str, Any]],
    records: list[dict[str, Any]],
    failures: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    for chunk in chunks:
        if int(chunk.get("pass_index") or 0) != 0:
            continue
        document_id = chunk.get("document_id")
        start = int(chunk["char_start"])
        end = int(chunk["char_end"])
        chunk["extracted_count"] = sum(
            1
            for record in records
            if record.get("document_id") == document_id
            and start <= int(record.get("char_start") or -1)
            and int(record.get("char_end") or -1) <= end
        )
        chunk["rejected_count"] = sum(
            1
            for failure in failures
            if failure.get("document_id") == document_id
            and failure.get("char_start") is not None
            and start <= int(failure.get("char_start") or -1)
            and int(failure.get("char_end") or -1) <= end
        )
    return chunks


def assign_failure_chunks(failures: list[dict[str, Any]], chunks: list[dict[str, Any]]) -> None:
    for failure in failures:
        if failure.get("char_start") is None or failure.get("document_id") is None:
            continue
        char_start = int(failure["char_start"])
        char_end = int(failure["char_end"])
        for chunk in chunks:
            if int(chunk.get("pass_index") or 0) != 0:
                continue
            if chunk.get("document_id") != failure.get("document_id"):
                continue
            if int(chunk["char_start"]) <= char_start and char_end <= int(chunk["char_end"]):
                failure["chunk_index"] = chunk["chunk_index"]
                failure["pass_index"] = chunk["pass_index"]
                break


def records_from_annotated_doc(
    run_id: str,
    adoc: Any,
    page: kg_db.PageSource,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    records: list[dict[str, Any]] = []
    failures: list[dict[str, Any]] = []
    source_text = adoc.text or ""
    source_hash = hashlib.sha256(source_text.encode("utf-8")).hexdigest()
    for extraction in adoc.extractions or []:
        record, error = mention_record_from_extraction(
            run_id=run_id,
            extraction=extraction,
            page=page,
            document_id=adoc.document_id,
            source_text=source_text,
            source_hash=source_hash,
        )
        if error:
            failures.append(error)
            continue
        records.append(record)
    return records, failures


def dedupe_records(records: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Keep the first grounded mention for each run/book/page/class/span."""
    return dedupe_records_with_rejections(records)[0]


def dedupe_records_with_rejections(records: list[dict[str, Any]]) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    """Keep the first grounded mention and report duplicate spans."""
    deduped: list[dict[str, Any]] = []
    rejections: list[dict[str, Any]] = []
    seen: set[tuple[Any, ...]] = set()
    for record in records:
        key = (
            record.get("run_id"),
            record.get("book_id"),
            record.get("page_id"),
            record.get("extraction_class"),
            record.get("char_start"),
            record.get("char_end"),
        )
        if key in seen:
            rejections.append(
                {
                    "id": kg_db.new_uuid(),
                    "run_id": record.get("run_id"),
                    "book_id": record.get("book_id"),
                    "page_id": record.get("page_id"),
                    "heading_id": record.get("heading_id"),
                    "document_id": record.get("document_id"),
                    "extraction_class": record.get("extraction_class"),
                    "extraction_text": record.get("extraction_text"),
                    "exact_quote": record.get("exact_quote"),
                    "char_start": record.get("char_start"),
                    "char_end": record.get("char_end"),
                    "alignment_status": record.get("alignment_status"),
                    "code": "DUPLICATE_SPAN",
                    "message": "duplicate grounded mention span",
                    "attributes": record.get("attributes") or {},
                    "source_hash": record.get("source_hash"),
                    "review_status": "rejected",
                }
            )
            continue
        seen.add(key)
        deduped.append(record)
    return deduped, rejections


def mention_record_from_extraction(
    *,
    run_id: str,
    extraction: Any,
    page: kg_db.PageSource,
    document_id: str,
    source_text: str,
    source_hash: str,
) -> tuple[dict[str, Any] | None, dict[str, Any] | None]:
    extraction_text = str(getattr(extraction, "extraction_text", "") or "").strip()
    if not extraction_text:
        return None, failure(run_id, page, extraction, "EMPTY_TEXT", "extraction_text is empty", document_id, source_hash)
    if is_generic_extraction(extraction_text):
        return None, failure(run_id, page, extraction, "GENERIC_TEXT", "generic extraction text", document_id, source_hash)

    extraction_class = canonical_extraction_class(
        str(getattr(extraction, "extraction_class", "") or "").strip(),
        extraction_text,
    )
    if extraction_class == "person" and is_theonym(extraction_text):
        return None, failure(
            run_id,
            page,
            extraction,
            "NON_PERSON_THEONYM",
            "divine names are not person mentions",
            document_id,
            source_hash,
        )

    interval = getattr(extraction, "char_interval", None)
    alignment_status = alignment_status_value(extraction)
    if interval is None or interval.start_pos is None or interval.end_pos is None:
        fallback_span = find_unique_exact_span(source_text, extraction_text)
        if fallback_span is None:
            return None, failure(run_id, page, extraction, "UNGROUNDED", "char_interval is missing", document_id, source_hash)
        start, end = fallback_span
        alignment_status = "match_exact_substring_fallback"
    else:
        start = int(interval.start_pos)
        end = int(interval.end_pos)
        if start < 0 or end <= start or end > len(source_text):
            return None, failure(
                run_id,
                page,
                extraction,
                "BAD_SPAN",
                f"invalid char span {start}:{end}",
                document_id,
                source_hash,
            )
        if source_text[start:end] != extraction_text:
            fallback_span = find_unique_exact_span(source_text, extraction_text)
            if fallback_span is None:
                return None, failure(
                    run_id,
                    page,
                    extraction,
                    "NON_EXACT_QUOTE",
                    "source slice differs from extraction_text",
                    document_id,
                    source_hash,
                )
            start, end = fallback_span
            alignment_status = "match_exact_substring_fallback"

    exact_quote = source_text[start:end]
    token_interval = getattr(extraction, "token_interval", None)

    attributes = getattr(extraction, "attributes", None) or {}
    if not isinstance(attributes, dict):
        attributes = {"raw_attributes": attributes}
    review_status = review_status_for(extraction_class, extraction_text, attributes, alignment_status)
    confidence = confidence_for(attributes, review_status)
    return (
        {
            "kind": "knowledge_mention",
            "id": kg_db.new_uuid(),
            "source_span_id": kg_db.new_uuid(),
            "run_id": run_id,
            "book_id": page.book_id,
            "page_id": page.page_id,
            "heading_id": page.heading_id,
            "document_id": document_id,
            "extraction_class": extraction_class,
            "extraction_text": extraction_text,
            "exact_quote": exact_quote,
            "char_start": start,
            "char_end": end,
            "alignment_status": alignment_status,
            "attributes": attributes,
            "normalized_text": normalized_key(extraction_text),
            "grounded": True,
            "confidence": confidence,
            "review_status": review_status,
            "source_hash": source_hash,
            "token_start": getattr(token_interval, "start_index", None) if token_interval else None,
            "token_end": getattr(token_interval, "end_index", None) if token_interval else None,
            "extraction_index": getattr(extraction, "extraction_index", None),
            "group_index": getattr(extraction, "group_index", None),
            "pass_index": None,
        },
        None,
    )


def find_unique_exact_span(source_text: str, extraction_text: str) -> tuple[int, int] | None:
    """Find a unique exact substring span, useful for Arabic clitic boundaries."""
    if not source_text or not extraction_text:
        return None
    matches: list[int] = []
    start = source_text.find(extraction_text)
    while start != -1:
        matches.append(start)
        if len(matches) > 1:
            return None
        start = source_text.find(extraction_text, start + 1)
    if not matches:
        return None
    return matches[0], matches[0] + len(extraction_text)


def canonical_extraction_class(extraction_class: str, extraction_text: str) -> str:
    """Normalize legacy/misplaced ontology classes without changing evidence text."""
    if extraction_class == "book_title":
        return "quran_reference" if is_surah_reference(extraction_text) else "work_title"
    if extraction_class == "work_title" and is_surah_reference(extraction_text):
        return "quran_reference"
    if extraction_class == "person" and is_person_reference(extraction_text):
        return "person_reference"
    return extraction_class


def review_status_for(
    extraction_class: str,
    extraction_text: str,
    attributes: dict[str, Any],
    alignment_status: str = "match_exact",
) -> str:
    certainty = str(attributes.get("certainty") or attributes.get("citation_certainty") or "").lower()
    needs_review = str(attributes.get("needs_review") or attributes.get("requires_scholar_review") or "").lower()
    if extraction_class in {"relation", "claim"}:
        return "needs_review"
    if extraction_class == "person_reference":
        return "needs_review"
    if extraction_class == "person" and is_ambiguous_person_name(extraction_text):
        return "ambiguous"
    if certainty == "ambiguous":
        return "ambiguous"
    if needs_review in {"yes", "true", "1"}:
        return "needs_review"
    if alignment_status == "match_exact_substring_fallback":
        return "needs_review"
    return "pending"


def confidence_for(attributes: dict[str, Any], review_status: str) -> float:
    if review_status == "ambiguous":
        return 0.45
    if review_status == "needs_review":
        return 0.60
    certainty = str(attributes.get("certainty") or attributes.get("citation_certainty") or "").lower()
    if certainty == "explicit":
        return 0.80
    return 0.70


def alignment_status_value(extraction: Any) -> str:
    status = getattr(extraction, "alignment_status", None)
    if status is None:
        return "unknown"
    return getattr(status, "value", str(status))


def failure(
    run_id: str,
    page: kg_db.PageSource,
    extraction: Any,
    code: str,
    message: str,
    document_id: str,
    source_hash: str,
) -> dict[str, Any]:
    interval = getattr(extraction, "char_interval", None)
    attributes = getattr(extraction, "attributes", None) or {}
    if not isinstance(attributes, dict):
        attributes = {"raw_attributes": attributes}
    char_start = getattr(interval, "start_pos", None) if interval else None
    char_end = getattr(interval, "end_pos", None) if interval else None
    return {
        "id": kg_db.new_uuid(),
        "run_id": run_id,
        "book_id": page.book_id,
        "page_id": page.page_id,
        "heading_id": page.heading_id,
        "document_id": document_id,
        "code": code,
        "message": message,
        "extraction_class": str(getattr(extraction, "extraction_class", "") or ""),
        "extraction_text": str(getattr(extraction, "extraction_text", "") or ""),
        "char_start": char_start,
        "char_end": char_end,
        "alignment_status": alignment_status_value(extraction),
        "attributes": attributes,
        "source_hash": source_hash,
        "review_status": "rejected",
    }


def write_jsonl(path: Path, rows: list[dict[str, Any]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as out_file:
        for row in rows:
            out_file.write(json.dumps(row, ensure_ascii=False) + "\n")


if __name__ == "__main__":
    raise SystemExit(main())
