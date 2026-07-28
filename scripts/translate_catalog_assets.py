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
    request_json,
)
from surau_inference import (
    InferenceClient,
    RESUMABLE_EXIT_CODE,
    attribution_metadata,
    budget_exceeded,
    generation_identity,
    parse_output,
)


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
    inference_client = None
    if args.dry_run:
        args.generation = new_generation_identity("dry-run", CATALOG_TRANSLATION_PROMPT_VERSION)
    else:
        inference_client = InferenceClient.from_env(args.inference_base_url, args.timeout_seconds)

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
    budget_blocked = False
    with out_path.open(mode, encoding="utf-8") as out_file:
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as executor:
            futures = {}
            for index, item in enumerate(items, start=1):
                future = executor.submit(translate_item, args, inference_client, item, index, len(items))
                futures[future] = (index, item)
                if args.sleep_seconds > 0 and index < len(items):
                    time.sleep(args.sleep_seconds)

            for future in concurrent.futures.as_completed(futures):
                index, item = futures[future]
                try:
                    asset = future.result()
                except Exception as err:
                    cap = budget_exceeded(err)
                    failures.append(
                        {
                            "item": item_key(item),
                            "error": str(err),
                            "status": "resumable" if cap else "failed",
                            "retry_after": cap.retry_after if cap else 0,
                        }
                    )
                    print(f"[{index}/{len(items)}] failed {item_key(item)}: {err}", file=sys.stderr)
                    if cap:
                        budget_blocked = True
                        for pending in futures:
                            pending.cancel()
                        break
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
        if budget_blocked:
            print("inference budget exceeded; batch is resumable with --resume", file=sys.stderr)
            return RESUMABLE_EXIT_CODE
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
    parser.add_argument("--inference-base-url", default=None, help="Surau U-0 gateway base URL")
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
        for category in request_json(
            "GET", f"{args.base_url.rstrip('/')}/v1/categories", surau_base_url=args.base_url
        ):
            items.append({"type": "category", "data": category})

    if args.kind in {"all", "authors"}:
        for author in fetch_paginated(args.base_url, "/v1/authors", "authors", args.page_size):
            items.append({"type": "author", "data": author})

    if args.kind in {"all", "books"}:
        if args.book_id:
            for book_id in args.book_id:
                book = request_json(
                    "GET", f"{args.base_url.rstrip('/')}/v1/books/{book_id}", surau_base_url=args.base_url
                )
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
        payload = request_json(
            "GET", f"{base_url.rstrip('/')}{path}?{query}", surau_base_url=base_url
        )
        rows = payload.get(key) or []
        total = int(payload.get("total") or 0)
        results.extend(rows)
        offset += len(rows)
        if not rows or offset >= total:
            break
    return results


def translate_item(
    args: argparse.Namespace,
    inference_client: InferenceClient | None,
    item: dict[str, Any],
    index: int,
    total: int,
) -> dict[str, Any]:
    print(f"[{index}/{total}] translating {item_key(item)}", file=sys.stderr)
    item_type = item["type"]
    data = item["data"]

    if args.dry_run:
        translated = dry_run_translation(item_type, data)
        generation = dict(args.generation)
        inference_metadata: dict[str, Any] = {"provider": "dry-run", "model": "dry-run"}
    else:
        if inference_client is None:
            raise RuntimeError("inference client is required")
        translated, inference_result = translate_catalog_item(
            args, inference_client, item_type, data
        )
        generation = generation_identity(inference_result)
        inference_metadata = attribution_metadata(inference_result)

    metadata = {
        **inference_metadata,
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
            "source": generation["model_id"],
            "translation_status": "generated",
            "provenance_class": MACHINE_PROVENANCE_CLASS,
            "generation": generation,
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
            "source": generation["model_id"],
            "translation_status": "generated",
            "provenance_class": MACHINE_PROVENANCE_CLASS,
            "generation": generation,
            "metadata": metadata,
        }
    if item_type == "category":
        return {
            "kind": "category_translation",
            "category_id": int(data["id"]),
            "lang": args.target_lang,
            "name": translated["name"],
            "source": generation["model_id"],
            "translation_status": "generated",
            "provenance_class": MACHINE_PROVENANCE_CLASS,
            "generation": generation,
            "metadata": metadata,
        }

    raise RuntimeError(f"unsupported item type {item_type}")


def translate_catalog_item(
    args: argparse.Namespace,
    client: InferenceClient,
    item_type: str,
    data: dict[str, Any],
) -> tuple[dict[str, str], dict[str, Any]]:
    target_name = TARGET_NAMES[args.target_lang]
    schema = schema_for(item_type)
    result = client.invoke(
        "catalog-translation",
        json.dumps(
            {
                "target_language": target_name,
                "target_style_guide": TARGET_STYLE_GUIDES[args.target_lang],
                "item_type": item_type,
                "source": data,
                "json_schema": schema,
            },
            ensure_ascii=False,
        ),
        cache_vary={"target_language": args.target_lang, "item_type": item_type},
    )
    translated = parse_output(result)
    return {key: str(translated.get(key, "")).strip() for key in schema}, result


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
