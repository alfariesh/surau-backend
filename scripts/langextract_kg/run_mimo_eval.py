#!/usr/bin/env python3
"""Run dry-run Mimo LangExtract eval suites."""

from __future__ import annotations

import argparse
from collections import Counter, defaultdict
from dataclasses import dataclass
from datetime import datetime
import json
import os
from pathlib import Path
import sys
import time
from types import SimpleNamespace
from typing import Any

if __package__ in (None, ""):
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    from langextract_kg import db as kg_db  # type: ignore
    from langextract_kg.extract_knowledge import (  # type: ignore
        attach_chunk_counts,
        assign_failure_chunks,
        dedupe_records_with_rejections,
        run_extraction,
        run_status_for_failures,
        write_jsonl,
    )
    from langextract_kg.prompts import get_prompt  # type: ignore
    from langextract_kg.qa_extractions import validate_rows  # type: ignore
    from langextract_kg.visualize_run import write_visualization  # type: ignore
else:
    from . import db as kg_db
    from .extract_knowledge import (
        attach_chunk_counts,
        assign_failure_chunks,
        dedupe_records_with_rejections,
        run_extraction,
        run_status_for_failures,
        write_jsonl,
    )
    from .prompts import get_prompt
    from .qa_extractions import validate_rows
    from .visualize_run import write_visualization


DEFAULT_MODEL = "xmtp/mimo-v2.5-pro"
DEFAULT_BASE_URL = "http://localhost:20128/v1"
DEFAULT_TASKS = ("mentions", "terms", "citations")
GROUNDING_REJECTION_CODES = {"UNGROUNDED", "NON_EXACT_QUOTE"}
MODEL_ERROR_CODES = {
    "MODEL_OUTPUT_EMPTY",
    "MODEL_OUTPUT_PARSE_ERROR",
    "MODEL_OUTPUT_SCHEMA_ERROR",
    "MODEL_API_ERROR",
}


@dataclass(frozen=True)
class PageSpec:
    ord: int
    genre: str
    book_id: int
    page_id: int


GOLDEN8_SPECS = (
    PageSpec(1, "qiraat_grammar", 3, 3),
    PageSpec(2, "qiraat_readings", 57, 3),
    PageSpec(3, "fiqh", 9, 3),
    PageSpec(4, "usul", 10, 8),
    PageSpec(5, "hadith_commentary", 11, 11),
    PageSpec(6, "tafsir", 41, 4),
    PageSpec(7, "history_places", 27, 4),
    PageSpec(8, "aqidah", 56, 3),
)

FIFTY_GENRE_BOOKS = {
    "qiraat": (3, 31, 57),
    "fiqh_usul": (9, 10, 19, 20, 30, 32),
    "hadith": (11, 21, 23, 47, 52),
    "tafsir": (14, 29, 41, 42, 43),
    "history_aqidah_adab": (27, 28, 44, 50, 54, 56, 59),
}


def main() -> int:
    args = parse_args()
    kg_db.load_env_file(Path(args.env_file).expanduser())

    rows = load_page_rows(args)
    pages = rows_to_page_sources(rows)
    if not pages:
        raise SystemExit("No pages selected.")

    out_root = Path(args.out_dir).expanduser() if args.out_dir else default_out_dir(args.suite)
    out_root.mkdir(parents=True, exist_ok=True)
    pages_path = out_root / "pages.json"
    pages_path.write_text(json.dumps(rows, ensure_ascii=False, indent=2), encoding="utf-8")

    report = run_eval(args, rows, pages, out_root, pages_path)
    summary_path = out_root / "summary.json"
    summary_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(
        json.dumps(
            {
                "out_root": str(out_root),
                "summary_json": str(summary_path),
                "acceptance": report["acceptance"],
                "task_summary": [
                    {
                        "task": task["task"],
                        "prompt_version": task["prompt_version"],
                        "records": task["records"],
                        "rejections": task["rejections"],
                        "parse_statuses": task["parse_statuses"],
                        "class_counts": task["class_counts"],
                        "qa": task["qa"],
                        "grounding": task["grounding"],
                    }
                    for task in report["tasks"]
                ],
            },
            ensure_ascii=False,
            indent=2,
        )
    )
    return 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--suite", choices=["golden8", "fifty"], default="golden8")
    parser.add_argument("--page-source-json", default="", help="Use pre-fetched page rows instead of querying PG.")
    parser.add_argument("--pg-url", default="", help="PostgreSQL URL; defaults to PG_URL/POSTGRES_URL")
    parser.add_argument("--env-file", default=str(kg_db.DEFAULT_ENV_FILE))
    parser.add_argument("--out-dir", default="")
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument("--llm-base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--api-key-env", default="LANGEXTRACT_LLM_API_KEY")
    parser.add_argument("--tasks", default=",".join(DEFAULT_TASKS))
    parser.add_argument("--max-char-buffer", type=int, default=1800)
    parser.add_argument("--context-window-chars", type=int, default=250)
    parser.add_argument("--extraction-passes", type=int, default=1)
    parser.add_argument("--batch-length", type=int, default=1)
    parser.add_argument("--max-workers", type=int, default=1)
    parser.add_argument("--max-output-tokens", type=int, default=4500)
    parser.add_argument("--request-timeout-seconds", type=float, default=180.0)
    parser.add_argument("--temperature", type=float, default=0.0)
    parser.add_argument("--grounding-rate-target", type=float, default=0.20)
    return parser.parse_args()


def default_out_dir(suite: str) -> Path:
    return Path("/tmp") / f"surau-langextract-mimo-{suite}-{datetime.now().strftime('%Y%m%d-%H%M%S')}"


def load_page_rows(args: argparse.Namespace) -> list[dict[str, Any]]:
    if args.page_source_json:
        path = Path(args.page_source_json).expanduser()
        rows = json.loads(path.read_text(encoding="utf-8"))
        if not isinstance(rows, list):
            raise SystemExit("--page-source-json must contain a JSON array")
        return [normalize_page_row(row, index + 1) for index, row in enumerate(rows)]

    pg_url = args.pg_url or kg_db.postgres_url_from_env()
    if not pg_url:
        raise SystemExit("--pg-url or --page-source-json is required")
    client = kg_db.DBClient.connect(pg_url)
    try:
        if args.suite == "golden8":
            return fetch_page_specs(client, GOLDEN8_SPECS)
        return fetch_fifty_pages(client)
    finally:
        client.close()


def normalize_page_row(row: dict[str, Any], ordinal: int) -> dict[str, Any]:
    return {
        "ord": int(row.get("ord") or ordinal),
        "genre": str(row.get("genre") or "unknown"),
        "book_id": int(row["book_id"]),
        "page_id": int(row["page_id"]),
        "book_title": row.get("book_title") or row.get("name") or "",
        "heading_id": row.get("heading_id"),
        "heading_title": row.get("heading_title"),
        "content_text": str(row.get("content_text") or ""),
        "char_count": int(row.get("char_count") or len(str(row.get("content_text") or ""))),
    }


def rows_to_page_sources(rows: list[dict[str, Any]]) -> list[kg_db.PageSource]:
    return [
        kg_db.PageSource(
            book_id=int(row["book_id"]),
            page_id=int(row["page_id"]),
            heading_id=int(row["heading_id"]) if row.get("heading_id") is not None else None,
            heading_title=row.get("heading_title") or row.get("book_title"),
            content_text=str(row.get("content_text") or ""),
        )
        for row in sorted(rows, key=lambda item: int(item["ord"]))
    ]


def specs_values_sql(specs: tuple[PageSpec, ...] | list[PageSpec]) -> str:
    return ",\n".join(f"({spec.ord}, '{spec.genre}', {spec.book_id}, {spec.page_id})" for spec in specs)


def fetch_page_specs(client: kg_db.DBClient, specs: tuple[PageSpec, ...] | list[PageSpec]) -> list[dict[str, Any]]:
    sql = f"""
WITH target(ord, genre, book_id, page_id) AS (
  VALUES
    {specs_values_sql(specs)}
)
{selected_pages_sql("target")}
"""
    with client._cursor(dict_rows=True) as cur:  # noqa: SLF001 - script-level DB read helper.
        cur.execute(sql, ())
        rows = cur.fetchall()
    return [normalize_page_row(dict(row), index + 1) for index, row in enumerate(rows)]


def fetch_fifty_pages(client: kg_db.DBClient) -> list[dict[str, Any]]:
    values = ",\n".join(
        f"('{genre}', {book_id})" for genre, book_ids in FIFTY_GENRE_BOOKS.items() for book_id in book_ids
    )
    sql = f"""
WITH target_books(genre, book_id) AS (
  VALUES
    {values}
),
candidates AS (
  SELECT tb.genre,
         bp.book_id,
         bp.page_id,
         row_number() OVER (PARTITION BY tb.genre, bp.book_id ORDER BY bp.page_id) AS book_rn
  FROM target_books tb
  JOIN book_pages bp ON bp.book_id = tb.book_id AND bp.is_deleted = false
  WHERE bp.page_id >= 3
    AND length(bp.content_text) BETWEEN 900 AND 1600
),
target AS (
  SELECT row_number() OVER (ORDER BY genre, book_rn, book_id, page_id) AS ord,
         genre,
         book_id,
         page_id
  FROM (
    SELECT genre,
           book_id,
           page_id,
           book_rn,
           row_number() OVER (PARTITION BY genre ORDER BY book_rn, book_id, page_id) AS genre_rn
    FROM candidates
  ) ranked
  WHERE genre_rn <= 10
)
{selected_pages_sql("target")}
"""
    with client._cursor(dict_rows=True) as cur:  # noqa: SLF001 - script-level DB read helper.
        cur.execute(sql, ())
        rows = cur.fetchall()
    normalized = [normalize_page_row(dict(row), index + 1) for index, row in enumerate(rows)]
    if len(normalized) != 50:
        raise SystemExit(f"Expected 50 pages for fifty suite, got {len(normalized)}")
    return normalized


def selected_pages_sql(target_name: str) -> str:
    return f"""
SELECT t.ord,
       t.genre,
       bp.book_id,
       bp.page_id,
       b.name AS book_title,
       ctx.heading_id,
       ctx.heading_title,
       bp.content_text,
       length(bp.content_text) AS char_count
FROM {target_name} t
JOIN books b ON b.id = t.book_id
JOIN book_pages bp ON bp.book_id = t.book_id AND bp.page_id = t.page_id AND bp.is_deleted = false
LEFT JOIN LATERAL (
  SELECT h.heading_id,
         h.content AS heading_title
  FROM book_heading_ranges hr
  JOIN book_headings h ON h.book_id = hr.book_id AND h.heading_id = hr.heading_id
  WHERE hr.book_id = bp.book_id
    AND bp.page_id BETWEEN hr.start_page_id AND hr.end_page_id
    AND h.is_deleted = false
  ORDER BY h.depth DESC, h.ordinal DESC, h.heading_id ASC
  LIMIT 1
) ctx ON true
ORDER BY t.ord
"""


def run_eval(
    args: argparse.Namespace,
    rows: list[dict[str, Any]],
    pages: list[kg_db.PageSource],
    out_root: Path,
    pages_path: Path,
) -> dict[str, Any]:
    api_key = os.environ.get(args.api_key_env) or os.environ.get("RAG_LLM_API_KEY") or "local-mimo-eval"
    page_meta = {(int(row["book_id"]), int(row["page_id"])): row for row in rows}
    task_names = parse_tasks(args.tasks)

    task_summaries = []
    all_records_by_task: dict[str, list[dict[str, Any]]] = {}
    all_failures_by_task: dict[str, list[dict[str, Any]]] = {}
    all_chunks_by_task: dict[str, list[dict[str, Any]]] = {}

    for task in task_names:
        prompt = get_prompt(task)
        run_id = kg_db.new_uuid()
        out_dir = out_root / task
        out_dir.mkdir(parents=True, exist_ok=True)
        run_args = SimpleNamespace(
            run_id=run_id,
            task=task,
            model=args.model,
            llm_base_url=args.llm_base_url,
            temperature=args.temperature,
            max_output_tokens=args.max_output_tokens,
            max_workers=args.max_workers,
            request_timeout_seconds=args.request_timeout_seconds,
            extraction_passes=args.extraction_passes,
            max_char_buffer=args.max_char_buffer,
            context_window_chars=args.context_window_chars,
            batch_length=args.batch_length,
            out_dir=str(out_dir),
        )
        started = time.perf_counter()
        result = run_extraction(run_args, prompt, pages, api_key)
        elapsed = round(time.perf_counter() - started, 2)

        records, duplicate_failures = dedupe_records_with_rejections(result["records"])
        failures = [*result["failures"], *duplicate_failures]
        assign_failure_chunks(failures, result["chunks_audit"])
        chunks = attach_chunk_counts(result["chunks_audit"], records, failures)

        records_path = out_dir / f"{run_id}.{task}.mentions.jsonl"
        chunks_path = out_dir / f"{run_id}.{task}.chunks.jsonl"
        rejections_path = out_dir / f"{run_id}.{task}.rejections.jsonl"
        write_jsonl(records_path, records)
        write_jsonl(chunks_path, chunks)
        write_jsonl(rejections_path, failures)
        raw_jsonl_path, html_path = write_visualization(
            result["annotated_docs"],
            out_dir=out_dir,
            output_stem=f"{run_id}.{task}.langextract",
            show_progress=False,
        )

        qa_issues = validate_rows([{**record, "_line": index + 1} for index, record in enumerate(records)])
        class_counts_by_genre = defaultdict(Counter)
        for record in records:
            meta = page_meta.get((int(record["book_id"]), int(record["page_id"])), {})
            class_counts_by_genre[str(meta.get("genre") or "unknown")][str(record.get("extraction_class"))] += 1

        grounding = grounding_summary(records, failures)
        task_summaries.append(
            {
                "task": task,
                "run_id": run_id,
                "prompt_version": prompt.version,
                "status": run_status_for_failures(failures),
                "elapsed_sec": elapsed,
                "records": len(records),
                "rejections": len(failures),
                "class_counts": dict(Counter(str(record.get("extraction_class")) for record in records)),
                "class_counts_by_genre": {
                    genre: dict(counter) for genre, counter in sorted(class_counts_by_genre.items())
                },
                "rejection_codes": dict(Counter(str(failure.get("code")) for failure in failures)),
                "parse_statuses": dict(Counter(str(chunk.get("parse_status")) for chunk in chunks)),
                "grounding": grounding,
                "qa": {
                    "status": "PASS" if not any(issue.severity == "FAIL" for issue in qa_issues) else "FAIL",
                    "failures": sum(1 for issue in qa_issues if issue.severity == "FAIL"),
                    "warnings": sum(1 for issue in qa_issues if issue.severity == "WARN"),
                    "issue_codes": dict(Counter(issue.code for issue in qa_issues)),
                },
                "html": str(html_path),
                "records_jsonl": str(records_path),
                "chunks_jsonl": str(chunks_path),
                "rejections_jsonl": str(rejections_path),
                "raw_langextract_jsonl": str(raw_jsonl_path),
            }
        )
        all_records_by_task[task] = records
        all_failures_by_task[task] = failures
        all_chunks_by_task[task] = chunks

    acceptance = acceptance_summary(args, rows, all_records_by_task, all_failures_by_task, all_chunks_by_task, task_summaries)
    return {
        "source": {
            "suite": args.suite,
            "model": args.model,
            "base_url": args.llm_base_url,
            "write_db": False,
            "pages_file": str(pages_path),
            "page_count": len(rows),
            "pages": [
                {
                    "genre": row["genre"],
                    "book_id": row["book_id"],
                    "page_id": row["page_id"],
                    "char_count": row["char_count"],
                    "book_title": row["book_title"],
                }
                for row in rows
            ],
        },
        "acceptance": acceptance,
        "tasks": task_summaries,
    }


def parse_tasks(raw_tasks: str) -> tuple[str, ...]:
    tasks = tuple(task.strip() for task in raw_tasks.split(",") if task.strip())
    invalid = [task for task in tasks if task not in {"mentions", "terms", "citations", "relations"}]
    if invalid:
        raise SystemExit(f"Invalid task(s): {', '.join(invalid)}")
    return tasks or DEFAULT_TASKS


def grounding_summary(records: list[dict[str, Any]], failures: list[dict[str, Any]]) -> dict[str, Any]:
    grounding_failures = [failure for failure in failures if failure.get("code") in GROUNDING_REJECTION_CODES]
    extraction_failures = [failure for failure in failures if failure.get("extraction_class") or failure.get("extraction_text")]
    attempted = len(records) + len(extraction_failures)
    return {
        "attempted": attempted,
        "grounding_rejections": len(grounding_failures),
        "grounding_rejection_rate": round(len(grounding_failures) / attempted, 4) if attempted else 0.0,
        "fallback_records": dict(
            Counter(
                str(record.get("alignment_status"))
                for record in records
                if str(record.get("alignment_status") or "").endswith("_fallback")
            )
        ),
    }


def acceptance_summary(
    args: argparse.Namespace,
    rows: list[dict[str, Any]],
    records_by_task: dict[str, list[dict[str, Any]]],
    failures_by_task: dict[str, list[dict[str, Any]]],
    chunks_by_task: dict[str, list[dict[str, Any]]],
    task_summaries: list[dict[str, Any]],
) -> dict[str, Any]:
    parse_failures = [
        {"task": task, "document_id": chunk.get("document_id"), "parse_status": chunk.get("parse_status")}
        for task, chunks in chunks_by_task.items()
        for chunk in chunks
        if chunk.get("parse_status") != "success"
    ]
    model_output_rejections = [
        failure
        for failures in failures_by_task.values()
        for failure in failures
        if is_model_error_code(str(failure.get("code") or ""))
    ]
    citation_records = records_by_task.get("citations", [])
    author_book_refs = [
        record
        for record in citation_records
        if record.get("extraction_class") == "book_reference"
        and str((record.get("attributes") or {}).get("reference_type") or "").strip().lower() in {"author", "person"}
    ]
    qiraat_page_keys = {
        (int(row["book_id"]), int(row["page_id"]))
        for row in rows
        if str(row.get("genre") or "").startswith("qiraat")
    }
    qiraat_term_page_keys = {
        (int(record["book_id"]), int(record["page_id"]))
        for record in records_by_task.get("terms", [])
        if record.get("extraction_class") == "qiraat_term"
    }
    missing_qiraat_term_pages = sorted([list(key) for key in qiraat_page_keys - qiraat_term_page_keys])
    qa_failures = [summary for summary in task_summaries if summary["qa"]["status"] != "PASS"]
    overall_grounding = grounding_summary(
        [record for records in records_by_task.values() for record in records],
        [failure for failures in failures_by_task.values() for failure in failures],
    )
    return {
        "all_chunks_parse_success": not parse_failures,
        "no_model_output_rejections": not model_output_rejections,
        "qa_pass_all_tasks": not qa_failures,
        "no_author_only_book_reference_records": not author_book_refs,
        "qiraat_pages_with_qiraat_term": sorted([list(key) for key in qiraat_term_page_keys & qiraat_page_keys]),
        "qiraat_pages_missing_qiraat_term": missing_qiraat_term_pages,
        "grounding": overall_grounding,
        "grounding_rate_target": args.grounding_rate_target,
        "grounding_rate_at_or_below_target": overall_grounding["grounding_rejection_rate"] <= args.grounding_rate_target,
        "details": {
            "parse_failures": parse_failures[:25],
            "model_output_rejection_count": len(model_output_rejections),
            "author_book_reference_records": author_book_refs[:25],
            "qa_failures": qa_failures,
        },
    }


def expected_suite_size(suite: str) -> int:
    return 8 if suite == "golden8" else 50


def is_model_error_code(code: str) -> bool:
    return code in MODEL_ERROR_CODES or code.startswith("MODEL_OUTPUT_")


if __name__ == "__main__":
    raise SystemExit(main())
