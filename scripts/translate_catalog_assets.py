#!/usr/bin/env python3
"""Translate Surau catalog metadata into import-reader-assets JSONL records.

This complements translate_reader_assets.py. It translates catalog metadata
such as book titles, bibliographies, hints, author biographies, and categories.
The raw database remains unchanged; generated rows are imported as language
overlays.
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
from typing import Any

from generation_identity import (
    CATALOG_TRANSLATION_PROMPT_VERSION,
    MACHINE_PROVENANCE_CLASS,
    new_generation_identity,
)

from translate_reader_assets import (
    DEFAULT_ENV_FILE,
    TARGET_NAMES,
    load_env_file,
    load_json_object,
    request_json,
)


SYSTEM_PROMPT = """You are a senior Islamic-studies catalog editor.

Translate Arabic Islamic catalog metadata into the target language as polished,
native library/catalog prose. Stay strictly inside the supplied metadata. Do
not invent historical claims, book summaries, author details, edition data, or
virtues that are not present or clearly implied by the source.

Output strict JSON only.

Rules:
- Preserve proper names accurately while making common names readable.
- Translate descriptive book titles semantically into the target language.
  Transliterate only proper names, author names, place names, or titles that
  function as fixed proper nouns.
- Translate category names naturally.
- Translate bibliographic notes faithfully; keep numbers, dates, edition notes,
  and source markers.
- If a field is empty or has no useful source content, return an empty string
  for that field rather than inventing text.
- Do not add footnotes, apologies, translator comments, or marketing language.
"""

TARGET_STYLE_GUIDES = {
    "id": """Indonesian catalog style:
- Use formal, clear Indonesian.
- Keep author names recognizable, e.g. ابن عثيمين -> Ibnu Utsaimin.
- Translate descriptive titles, e.g. فصول في الصيام والتراويح والزكاة ->
  Pasal-Pasal tentang Puasa, Tarawih, dan Zakat.
- Prefer concise book-list prose, not promotional copy.""",
    "en": """English catalog style:
- Use formal, clear English.
- Keep author names recognizable.
- Translate descriptive titles, e.g. فصول في الصيام والتراويح والزكاة ->
  Chapters on Fasting, Tarawih, and Zakat.
- Prefer concise catalog prose, not promotional copy.""",
}


def main() -> int:
    args = parse_args()
    load_env_file(Path(args.env_file).expanduser())
    if not args.model:
        args.model = os.environ.get("DEEPSEEK_MODEL", "deepseek-v4-flash")
    if not args.deepseek_base_url:
        args.deepseek_base_url = os.environ.get("DEEPSEEK_BASE_URL", "https://api.deepseek.com")
    args.generation = new_generation_identity(args.model, CATALOG_TRANSLATION_PROMPT_VERSION)

    api_key = os.environ.get(args.api_key_env)
    if not api_key and not args.dry_run:
        raise SystemExit(f"{args.api_key_env} is required. Put it in {args.env_file}.")

    items = collect_items(args)
    if args.limit > 0:
        items = items[: args.limit]
    if not items:
        raise SystemExit("No catalog items selected.")

    out_path = Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    completed = read_completed_keys(out_path) if args.resume and out_path.exists() else set()
    items = [item for item in items if item_key(item) not in completed]
    if not items:
        print(f"nothing to do; all selected catalog items already exist in {out_path}", file=sys.stderr)
        return 0

    mode = "a" if args.resume and out_path.exists() else "w"
    successes = 0
    failures: list[dict[str, Any]] = []
    with out_path.open(mode, encoding="utf-8") as out_file:
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as executor:
            futures = {}
            for index, item in enumerate(items, start=1):
                future = executor.submit(translate_item, args, api_key or "", item, index, len(items))
                futures[future] = (index, item)
                if args.sleep_seconds > 0 and index < len(items):
                    time.sleep(args.sleep_seconds)

            for future in concurrent.futures.as_completed(futures):
                index, item = futures[future]
                try:
                    asset = future.result()
                except Exception as err:
                    failures.append({"item": item_key(item), "error": str(err)})
                    print(f"[{index}/{len(items)}] failed {item_key(item)}: {err}", file=sys.stderr)
                    if args.fail_fast:
                        raise
                    continue

                out_file.write(json.dumps(asset, ensure_ascii=False, separators=(",", ":")) + "\n")
                out_file.flush()
                successes += 1
                print(f"[{index}/{len(items)}] done {item_key(item)}", file=sys.stderr)

    print(f"wrote {successes} JSONL records to {out_path}", file=sys.stderr)
    if failures:
        failure_path = out_path.with_suffix(out_path.suffix + ".failures.json")
        failure_path.write_text(json.dumps(failures, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(f"failed {len(failures)} items; see {failure_path}", file=sys.stderr)
        return 1

    return 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8080", help="Surau backend base URL")
    parser.add_argument("--kind", choices=["all", "books", "authors", "categories"], default="all")
    parser.add_argument("--book-id", action="append", type=int, help="Specific published book ID; repeatable")
    parser.add_argument("--target-lang", choices=sorted(TARGET_NAMES), default="id")
    parser.add_argument("--out", required=True, help="Output JSONL file")
    parser.add_argument("--limit", type=int, default=0, help="Limit total selected catalog items")
    parser.add_argument("--page-size", type=int, default=100)
    parser.add_argument("--model", default=None, help="DeepSeek model; defaults to DEEPSEEK_MODEL or deepseek-v4-flash")
    parser.add_argument("--deepseek-base-url", default=None, help="DeepSeek API base URL")
    parser.add_argument("--api-key-env", default="DEEPSEEK_API_KEY")
    parser.add_argument("--env-file", default=str(DEFAULT_ENV_FILE))
    parser.add_argument("--max-tokens", type=int, default=3000)
    parser.add_argument("--timeout-seconds", type=int, default=120)
    parser.add_argument("--retries", type=int, default=2)
    parser.add_argument("--concurrency", type=int, default=4)
    parser.add_argument("--sleep-seconds", type=float, default=0.2)
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--fail-fast", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    return parser.parse_args()


def collect_items(args: argparse.Namespace) -> list[dict[str, Any]]:
    items: list[dict[str, Any]] = []
    if args.kind in {"all", "categories"}:
        for category in request_json("GET", f"{args.base_url.rstrip('/')}/v1/categories"):
            items.append({"type": "category", "data": category})

    if args.kind in {"all", "authors"}:
        for author in fetch_paginated(args.base_url, "/v1/authors", "authors", args.page_size):
            items.append({"type": "author", "data": author})

    if args.kind in {"all", "books"}:
        if args.book_id:
            for book_id in args.book_id:
                book = request_json("GET", f"{args.base_url.rstrip('/')}/v1/books/{book_id}")
                items.append({"type": "book", "data": book})
        else:
            for book in fetch_paginated(args.base_url, "/v1/books", "books", args.page_size):
                items.append({"type": "book", "data": book})

    return items


def fetch_paginated(base_url: str, path: str, key: str, page_size: int) -> list[dict[str, Any]]:
    offset = 0
    results: list[dict[str, Any]] = []
    while True:
        query = f"limit={page_size}&offset={offset}"
        payload = request_json("GET", f"{base_url.rstrip('/')}{path}?{query}")
        rows = payload.get(key) or []
        total = int(payload.get("total") or 0)
        results.extend(rows)
        offset += len(rows)
        if not rows or offset >= total:
            break
    return results


def translate_item(
    args: argparse.Namespace,
    api_key: str,
    item: dict[str, Any],
    index: int,
    total: int,
) -> dict[str, Any]:
    print(f"[{index}/{total}] translating {item_key(item)}", file=sys.stderr)
    item_type = item["type"]
    data = item["data"]

    if args.dry_run:
        translated = dry_run_translation(item_type, data)
    else:
        translated = call_deepseek(args, api_key, item_type, data)

    metadata = {
        "provider": "deepseek",
        "model": args.model,
        "unit": f"catalog_{item_type}",
        "source_lang": "ar",
        "target_lang": args.target_lang,
        "generated_at": datetime.now(timezone.utc).isoformat(),
    }

    if item_type == "book":
        return {
            "kind": "book_metadata_translation",
            "book_id": int(data["id"]),
            "lang": args.target_lang,
            "display_title": translated["display_title"],
            "bibliography": translated.get("bibliography", ""),
            "hint": translated.get("hint", ""),
            "description": translated.get("description", ""),
            "source": args.model,
            "translation_status": "generated",
            "provenance_class": MACHINE_PROVENANCE_CLASS,
            "generation": dict(args.generation),
            "metadata": metadata,
        }
    if item_type == "author":
        return {
            "kind": "author_translation",
            "author_id": int(data["id"]),
            "lang": args.target_lang,
            "name": translated["name"],
            "biography": translated.get("biography", ""),
            "death_text": translated.get("death_text", ""),
            "source": args.model,
            "translation_status": "generated",
            "provenance_class": MACHINE_PROVENANCE_CLASS,
            "generation": dict(args.generation),
            "metadata": metadata,
        }
    if item_type == "category":
        return {
            "kind": "category_translation",
            "category_id": int(data["id"]),
            "lang": args.target_lang,
            "name": translated["name"],
            "source": args.model,
            "translation_status": "generated",
            "provenance_class": MACHINE_PROVENANCE_CLASS,
            "generation": dict(args.generation),
            "metadata": metadata,
        }

    raise RuntimeError(f"unsupported item type {item_type}")


def call_deepseek(args: argparse.Namespace, api_key: str, item_type: str, data: dict[str, Any]) -> dict[str, str]:
    target_name = TARGET_NAMES[args.target_lang]
    schema = schema_for(item_type)
    payload = {
        "model": args.model,
        "messages": [
            {"role": "system", "content": SYSTEM_PROMPT},
            {
                "role": "user",
                "content": json.dumps(
                    {
                        "target_language": target_name,
                        "target_style_guide": TARGET_STYLE_GUIDES[args.target_lang],
                        "item_type": item_type,
                        "source": data,
                        "json_schema": schema,
                    },
                    ensure_ascii=False,
                ),
            },
        ],
        "response_format": {"type": "json_object"},
        "thinking": {"type": "disabled"},
        "temperature": 0.2,
        "max_tokens": args.max_tokens,
    }
    headers = {"Authorization": f"Bearer {api_key}"}
    response = request_json(
        "POST",
        f"{args.deepseek_base_url.rstrip('/')}/chat/completions",
        headers=headers,
        payload=payload,
        timeout_seconds=args.timeout_seconds,
        retries=args.retries,
    )
    content = response["choices"][0]["message"]["content"]
    translated = load_json_object(content)
    return {key: str(translated.get(key, "")).strip() for key in schema}


def schema_for(item_type: str) -> dict[str, str]:
    if item_type == "book":
        return {"display_title": "string", "bibliography": "string", "hint": "string", "description": "string"}
    if item_type == "author":
        return {"name": "string", "biography": "string", "death_text": "string"}
    if item_type == "category":
        return {"name": "string"}
    raise RuntimeError(f"unsupported item type {item_type}")


def dry_run_translation(item_type: str, data: dict[str, Any]) -> dict[str, str]:
    if item_type == "book":
        return {
            "display_title": f"[DRY RUN] {data.get('name', '')}",
            "bibliography": str(data.get("bibliography") or ""),
            "hint": str(data.get("hint") or ""),
            "description": str(data.get("description") or ""),
        }
    if item_type == "author":
        return {
            "name": f"[DRY RUN] {data.get('name', '')}",
            "biography": str(data.get("biography") or ""),
            "death_text": str(data.get("death_text") or ""),
        }
    if item_type == "category":
        return {"name": f"[DRY RUN] {data.get('name', '')}"}
    raise RuntimeError(f"unsupported item type {item_type}")


def item_key(item: dict[str, Any]) -> str:
    return f"{item['type']}:{item['data'].get('id')}"


def read_completed_keys(out_path: Path) -> set[str]:
    completed: set[str] = set()
    for line in out_path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        row = json.loads(line)
        kind = row.get("kind")
        if kind == "book_metadata_translation":
            completed.add(f"book:{row.get('book_id')}")
        elif kind == "author_translation":
            completed.add(f"author:{row.get('author_id')}")
        elif kind == "category_translation":
            completed.add(f"category:{row.get('category_id')}")
    return completed


if __name__ == "__main__":
    raise SystemExit(main())
