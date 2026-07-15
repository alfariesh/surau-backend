#!/usr/bin/env python3
"""Generate reader-grade TOC heading summaries as import-reader-assets JSONL.

The script uses Surau's existing TOC/range reader endpoints. It does not build a
new PageIndex tree; it attaches concise summaries to the authoritative
`book_headings` tree already present in PostgreSQL.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import os
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import Any


SCRIPT_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = SCRIPT_DIR.parent
sys.path.insert(0, str(SCRIPT_DIR))

from translate_reader_assets import (  # noqa: E402
    load_env_file,
    load_json_object,
    request_json,
    write_jsonl,
)
from generation_identity import (  # noqa: E402
    MACHINE_PROVENANCE_CLASS,
    READER_SUMMARY_PROMPT_VERSION,
    new_generation_identity,
)


DEFAULT_ENV_FILE = PROJECT_ROOT / ".env.local"
SUMMARY_STYLE_VERSION = "reader-summary-v1"
TARGET_NAMES = {
    "ar": "Arabic",
    "id": "Indonesian",
    "en": "English",
}

SYSTEM_PROMPT = """You are a senior Islamic-studies editor.

Write concise, faithful summaries for a classical Islamic book reader. Stay
strictly inside the supplied source. Do not add commentary, outside facts,
modern examples, criticism, citations, or claims not present in the source.

Output strict JSON only, with key "summary".
"""


def main() -> int:
    args = parse_args()
    validate_summary_language(args)
    load_env_file(Path(args.env_file).expanduser())
    resolve_llm_config(args)
    args.generation = new_generation_identity(args.model, READER_SUMMARY_PROMPT_VERSION)

    api_key = os.environ.get(args.api_key_env) or os.environ.get("RAG_LLM_API_KEY", "")
    if not api_key and not args.dry_run:
        raise SystemExit(
            f"{args.api_key_env} or RAG_LLM_API_KEY is required. Put it in "
            f"{args.env_file} or export it first."
        )

    toc = fetch_toc(args.base_url, args.book_id, args.source_lang)
    nodes = flatten_toc(toc)
    node_by_id = {int(node["heading_id"]): node for node in nodes}
    children_by_parent = build_children_by_parent(nodes)

    heading_ids = args.heading_id or []
    if args.all_toc:
        heading_ids.extend(int(node["heading_id"]) for node in nodes)
    heading_ids = dedupe_preserve_order(heading_ids)
    if args.limit > 0:
        heading_ids = heading_ids[: args.limit]
    if not heading_ids:
        raise SystemExit("No headings selected. Use --heading-id or --all-toc.")
    missing = [heading_id for heading_id in heading_ids if heading_id not in node_by_id]
    if missing:
        raise SystemExit(f"Heading IDs not found in TOC: {missing[:10]}")
    if args.concurrency < 1:
        raise SystemExit("--concurrency must be at least 1.")

    out_path = Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    generated_by_heading: dict[int, str] = {}
    if args.resume and out_path.exists():
        generated_by_heading = read_completed_summaries(out_path, args.book_id, args.summary_lang)
        completed = set(generated_by_heading)
        before = len(heading_ids)
        heading_ids = [heading_id for heading_id in heading_ids if heading_id not in completed]
        skipped = before - len(heading_ids)
        if skipped:
            print(f"resume: skipped {skipped} already-written summaries", file=sys.stderr)
    if not heading_ids:
        print(f"nothing to do; all selected summaries already exist in {out_path}", file=sys.stderr)
        return 0

    failure_path = Path(args.failure_out) if args.failure_out else out_path.with_suffix(out_path.suffix + ".failures.jsonl")
    failure_path.parent.mkdir(parents=True, exist_ok=True)
    selected_nodes = [node_by_id[heading_id] for heading_id in heading_ids]
    depth_groups = group_by_depth_desc(selected_nodes)
    failures: list[dict[str, Any]] = []
    generated_assets: list[dict[str, Any]] = []
    success_count = 0
    total = len(heading_ids)
    progress_index = 0
    out_mode = "a" if args.resume and out_path.exists() else "w"
    failure_mode = "a" if args.resume and failure_path.exists() else "w"

    with out_path.open(out_mode, encoding="utf-8") as out_file, failure_path.open(failure_mode, encoding="utf-8") as failure_file:
        for group in depth_groups:
            with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as executor:
                futures: dict[concurrent.futures.Future[dict[str, Any]], tuple[int, dict[str, Any]]] = {}
                for node in group:
                    progress_index += 1
                    future = executor.submit(
                        generate_summary_asset,
                        args,
                        api_key,
                        node,
                        children_by_parent,
                        generated_by_heading,
                        progress_index,
                        total,
                    )
                    futures[future] = (progress_index, node)
                    if args.sleep_seconds > 0 and progress_index < total:
                        time.sleep(args.sleep_seconds)

                for future in concurrent.futures.as_completed(futures):
                    index, node = futures[future]
                    heading_id = int(node["heading_id"])
                    try:
                        asset = future.result()
                    except Exception as err:
                        record_failure(failure_file, args, heading_id, index, total, err)
                        failures.append({"heading_id": heading_id, "error": str(err)})
                        if args.fail_fast:
                            raise
                        continue

                    write_jsonl(out_file, asset)
                    generated_by_heading[heading_id] = str(asset["summary"])
                    generated_assets.append(asset)
                    success_count += 1
                    print(
                        f"[{index}/{total}] done book={args.book_id} "
                        f"heading={heading_id} lang={args.summary_lang}",
                        file=sys.stderr,
                    )

    print(f"wrote {success_count} summary JSONL records to {out_path}", file=sys.stderr)
    if args.eval_report:
        write_eval_report(Path(args.eval_report), args, generated_assets, failures)
    if failures:
        print(f"failed {len(failures)} headings; see {failure_path}", file=sys.stderr)
        return 1
    return 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8080", help="Surau backend base URL")
    parser.add_argument("--book-id", required=True, type=int, help="Book ID to summarize")
    parser.add_argument("--heading-id", action="append", type=int, help="Heading ID; repeat for multiple sections")
    parser.add_argument("--all-toc", action="store_true", help="Summarize all headings from the book TOC")
    parser.add_argument("--limit", type=int, default=0, help="Limit selected headings; 0 means no limit")
    parser.add_argument("--source-lang", default="ar", help="Reader language used for source fetch")
    parser.add_argument("--summary-lang", choices=sorted(TARGET_NAMES), default="ar", help="Summary output language")
    parser.add_argument("--out", required=True, help="Output JSONL file")
    parser.add_argument("--model", default=None, help="LLM model; defaults to SUMMARY_LLM_MODEL, RAG_LLM_MODEL, or glm-5.1")
    parser.add_argument("--llm-base-url", default=None, help="OpenAI-compatible base URL")
    parser.add_argument("--provider-name", default=None, help="Provider label written to metadata; defaults from LLM_PROVIDER_NAME or base URL")
    parser.add_argument("--api-key-env", default="SUMMARY_LLM_API_KEY", help="Environment variable containing the LLM API key")
    parser.add_argument("--env-file", default=str(DEFAULT_ENV_FILE), help="Local dotenv file loaded before reading env")
    parser.add_argument("--max-tokens", type=int, default=900)
    parser.add_argument("--max-source-chars", type=int, default=0, help="Trim long source text for sampling; 0 disables trimming")
    parser.add_argument("--timeout-seconds", type=int, default=90)
    parser.add_argument("--retries", type=int, default=2)
    parser.add_argument("--sleep-seconds", type=float, default=0.3)
    parser.add_argument("--concurrency", type=int, default=1, help="Number of summary workers per depth level")
    parser.add_argument("--resume", action="store_true", help="Append to --out and skip summaries already present")
    parser.add_argument("--failure-out", default="", help="Failure JSONL path; defaults to OUT.failures.jsonl")
    parser.add_argument("--eval-report", default="", help="Write a compact JSON QA report for generated summary rows")
    parser.add_argument("--fail-fast", action="store_true", help="Stop on the first failed heading")
    parser.add_argument("--dry-run", action="store_true", help="Fetch sections and write placeholder rows without calling LLM")
    return parser.parse_args()


def validate_summary_language(args: argparse.Namespace) -> None:
    if args.summary_lang == "ar":
        return

    raise SystemExit(
        "generate_reader_summaries.py only generates canonical Arabic summaries. "
        "Use --summary-lang ar, import that JSONL, then run "
        "scripts/translate_reader_assets.py --summary-only for id/en summaries."
    )


def resolve_llm_config(args: argparse.Namespace) -> None:
    if not args.model:
        args.model = os.environ.get("SUMMARY_LLM_MODEL") or os.environ.get("RAG_LLM_MODEL") or "glm-5.1"
    if not args.llm_base_url:
        args.llm_base_url = (
            os.environ.get("SUMMARY_LLM_BASE_URL")
            or os.environ.get("RAG_LLM_BASE_URL")
            or "https://ai.sumopod.com/v1"
        )
    if not args.provider_name:
        args.provider_name = resolve_provider_name(args.llm_base_url)


def resolve_provider_name(base_url: str) -> str:
    env_name = os.environ.get("LLM_PROVIDER_NAME")
    if env_name and env_name.strip():
        return env_name.strip()
    normalized = base_url.casefold()
    if "deepseek" in normalized:
        return "deepseek"
    if "sumopod" in normalized:
        return "sumopod"
    return "openai-compatible"


def generate_summary_asset(
    args: argparse.Namespace,
    api_key: str,
    node: dict[str, Any],
    children_by_parent: dict[int, list[dict[str, Any]]],
    generated_by_heading: dict[int, str],
    index: int,
    total: int,
) -> dict[str, Any]:
    heading_id = int(node["heading_id"])
    title = str(node.get("title") or "")
    print(
        f"[{index}/{total}] summarizing book={args.book_id} "
        f"heading={heading_id} lang={args.summary_lang}",
        file=sys.stderr,
    )

    source_text, source_kind = source_text_for_node(args, node, children_by_parent, generated_by_heading)
    original_len = len(source_text)
    if args.max_source_chars > 0 and len(source_text) > args.max_source_chars:
        source_text = source_text[: args.max_source_chars].rstrip()

    if args.dry_run:
        summary = f"[DRY RUN] {source_text[:280].strip()}"
    else:
        summary = generate_summary(
            api_key=api_key,
            llm_base_url=args.llm_base_url,
            model=args.model,
            summary_lang=args.summary_lang,
            source_title=title,
            source_kind=source_kind,
            source_text=source_text,
            max_tokens=args.max_tokens,
            timeout_seconds=args.timeout_seconds,
            retries=args.retries,
        )

    return {
        "kind": "heading_summary",
        "book_id": args.book_id,
        "heading_id": heading_id,
        "lang": args.summary_lang,
        "summary": summary,
        "source": args.model,
        "summary_status": "generated",
        "provenance_class": MACHINE_PROVENANCE_CLASS,
        "generation": dict(args.generation),
        "metadata": {
            "provider": args.provider_name,
            "model": args.model,
            "unit": "toc_summary",
            "style_version": SUMMARY_STYLE_VERSION,
            "source_lang": args.source_lang,
            "summary_lang": args.summary_lang,
            "source_kind": source_kind,
            "source_title": title,
            "source_endpoint": f"/v1/books/{args.book_id}/toc/{heading_id}/read",
            "selection_index": index,
            "selection_total": total,
            "generated_at": datetime.now(timezone.utc).isoformat(),
            "truncated_source": bool(args.max_source_chars > 0 and original_len > args.max_source_chars),
        },
    }


def source_text_for_node(
    args: argparse.Namespace,
    node: dict[str, Any],
    children_by_parent: dict[int, list[dict[str, Any]]],
    generated_by_heading: dict[int, str],
) -> tuple[str, str]:
    heading_id = int(node["heading_id"])
    children = children_by_parent.get(heading_id, [])
    child_parts: list[str] = []
    for child in children:
        child_id = int(child["heading_id"])
        child_summary = generated_by_heading.get(child_id)
        if not child_summary:
            child_parts = []
            break
        child_parts.append(f"- {child.get('title') or child_id}: {child_summary}")
    if child_parts:
        return "\n".join(child_parts), "child_summaries"

    section = fetch_toc_section(args.base_url, args.book_id, heading_id, args.source_lang)
    source_text = str(section.get("original_text") or "").strip()
    if not source_text:
        raise RuntimeError(f"heading {heading_id} has empty original_text")
    return source_text, "section_text"


def generate_summary(
    *,
    api_key: str,
    llm_base_url: str,
    model: str,
    summary_lang: str,
    source_title: str,
    source_kind: str,
    source_text: str,
    max_tokens: int,
    timeout_seconds: int,
    retries: int,
) -> str:
    target_name = TARGET_NAMES[summary_lang]
    user_payload = {
        "summary_language": target_name,
        "source_language": "Arabic",
        "source_kind": source_kind,
        "source_title": source_title,
        "source_text": source_text,
        "requirements": [
            "Write 1-3 compact sentences.",
            "Make it useful as reader-facing section context.",
            "Do not mention that this is a summary unless needed by the language.",
            "Do not add facts outside the source.",
        ],
        "json_schema": {"summary": "string"},
    }
    payload = {
        "model": model,
        "messages": [
            {"role": "system", "content": SYSTEM_PROMPT},
            {
                "role": "user",
                "content": "Generate a faithful reader summary. Return json only.\n\n"
                + json.dumps(user_payload, ensure_ascii=False),
            },
        ],
        "response_format": {"type": "json_object"},
        "temperature": 0.2,
        "max_tokens": max_tokens,
    }
    response = request_json(
        "POST",
        f"{llm_base_url.rstrip('/')}/chat/completions",
        headers={"Authorization": f"Bearer {api_key}"},
        payload=payload,
        timeout_seconds=timeout_seconds,
        retries=retries,
    )
    content = response["choices"][0]["message"].get("content") or response["choices"][0]["message"].get("reasoning_content", "")
    parsed = load_json_object(content)
    summary = str(parsed.get("summary") or "").strip()
    if not summary:
        raise RuntimeError("LLM returned JSON without non-empty summary")
    return summary


def fetch_toc(base_url: str, book_id: int, lang: str) -> list[dict[str, Any]]:
    url = f"{base_url.rstrip('/')}/v1/books/{book_id}/toc?lang={lang}"
    payload = request_json("GET", url, surau_base_url=base_url)
    if not isinstance(payload, list):
        raise RuntimeError(f"GET {url} returned non-list TOC")
    return payload


def fetch_toc_section(base_url: str, book_id: int, heading_id: int, lang: str) -> dict[str, Any]:
    url = f"{base_url.rstrip('/')}/v1/books/{book_id}/toc/{heading_id}/read?lang={lang}"
    payload = request_json("GET", url, surau_base_url=base_url)
    if not isinstance(payload, dict):
        raise RuntimeError(f"GET {url} returned non-object section")
    return payload


def flatten_toc(nodes: list[dict[str, Any]]) -> list[dict[str, Any]]:
    result: list[dict[str, Any]] = []

    def visit(node_list: list[dict[str, Any]], depth: int) -> None:
        for node in node_list:
            copied = dict(node)
            copied["_summary_depth"] = depth
            result.append(copied)
            visit(node.get("children") or [], depth + 1)

    visit(nodes, 0)
    return result


def build_children_by_parent(nodes: list[dict[str, Any]]) -> dict[int, list[dict[str, Any]]]:
    children: dict[int, list[dict[str, Any]]] = {}
    for node in nodes:
        parent_id = node.get("parent_id")
        if parent_id is None:
            continue
        children.setdefault(int(parent_id), []).append(node)
    return children


def group_by_depth_desc(nodes: list[dict[str, Any]]) -> list[list[dict[str, Any]]]:
    by_depth: dict[int, list[dict[str, Any]]] = {}
    for node in nodes:
        by_depth.setdefault(int(node.get("_summary_depth") or 0), []).append(node)
    return [by_depth[depth] for depth in sorted(by_depth, reverse=True)]


def dedupe_preserve_order(values: list[int]) -> list[int]:
    result: list[int] = []
    seen: set[int] = set()
    for value in values:
        if value in seen:
            continue
        seen.add(value)
        result.append(value)
    return result


def read_completed_summaries(out_path: Path, book_id: int, lang: str) -> dict[int, str]:
    completed: dict[int, str] = {}
    with out_path.open("r", encoding="utf-8") as out_file:
        for line_number, line in enumerate(out_file, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                row = json.loads(line)
            except json.JSONDecodeError as err:
                raise RuntimeError(f"Invalid JSONL in {out_path}:{line_number}: {err}") from err
            if row.get("kind") != "heading_summary":
                continue
            if int(row.get("book_id", 0)) != book_id or row.get("lang") != lang:
                continue
            summary = str(row.get("summary") or "").strip()
            if summary:
                completed[int(row["heading_id"])] = summary
    return completed


def read_completed_summary_ids(out_path: Path, book_id: int, lang: str) -> set[int]:
    return set(read_completed_summaries(out_path, book_id, lang))


def write_eval_report(
    report_path: Path,
    args: argparse.Namespace,
    generated_assets: list[dict[str, Any]],
    failures: list[dict[str, Any]],
) -> None:
    with TemporaryDirectory() as tmp_dir:
        tmp_path = Path(tmp_dir) / "generated-summaries.jsonl"
        with tmp_path.open("w", encoding="utf-8") as tmp_file:
            for asset in generated_assets:
                write_jsonl(tmp_file, asset)

        import qa_reader_assets

        qa_args = type(
            "QAArgs",
            (),
            {
                "file": str(tmp_path),
                "base_url": args.base_url,
                "book_id": args.book_id,
                "lang": args.summary_lang,
                "all_toc": False,
                "kind": "heading_summary",
                "report": "",
                "strict": False,
                "profile_map": str(SCRIPT_DIR / "translation_profiles.json"),
            },
        )()
        qa_report = qa_reader_assets.run_qa(qa_args)

    report = {
        "book_id": args.book_id,
        "lang": args.summary_lang,
        "generated_count": len(generated_assets),
        "failure_count": len(failures),
        "qa_status": "FAIL"
        if qa_report["summary"]["failures"]
        else "WARN"
        if qa_report["summary"]["warnings"]
        else "PASS",
        "qa": qa_report,
        "failures": failures,
    }
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"eval_report={report_path}", file=sys.stderr)


def record_failure(
    failure_file: Any,
    args: argparse.Namespace,
    heading_id: int,
    index: int,
    total: int,
    err: Exception,
) -> None:
    record = {
        "book_id": args.book_id,
        "heading_id": heading_id,
        "lang": args.summary_lang,
        "selection_index": index,
        "selection_total": total,
        "error": str(err),
        "failed_at": datetime.now(timezone.utc).isoformat(),
    }
    write_jsonl(failure_file, record)
    print(f"[{index}/{total}] failed book={args.book_id} heading={heading_id}: {err}", file=sys.stderr)


if __name__ == "__main__":
    raise SystemExit(main())
