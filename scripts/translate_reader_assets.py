#!/usr/bin/env python3
"""Translate Surau TOC sections into import-reader-assets JSONL records.

The script fetches Arabic section content from the local Surau backend and sends it
to DeepSeek. Output rows can be imported with:

    go run ./cmd/import-reader-assets --file=translated.jsonl
"""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import Any

from generation_identity import (
    MACHINE_PROVENANCE_CLASS,
    READER_SUMMARY_TRANSLATION_PROMPT_VERSION,
    READER_TRANSLATION_PROMPT_VERSION,
    new_generation_identity,
)


SCRIPT_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = SCRIPT_DIR.parent
DEFAULT_ENV_FILE = PROJECT_ROOT / ".env.local"
DEFAULT_PROFILE_MAP = SCRIPT_DIR / "translation_profiles.json"
STYLE_VERSION = "reader-profile-v1"
TERM_STYLE = "balanced"
PROFILE_CHOICES = [
    "auto",
    "general",
    "arabic_language",
    "fiqh",
    "aqidah",
    "hadith",
    "tafsir",
    "history",
    "adab_tazkiyah",
]


SYSTEM_PROMPT = """You are a senior Islamic-studies book writer and editor.

Produce a faithful edited rendering of classical Arabic Islamic text in the
target language. Your role is not to sound like a translator. Your role is to
make the reader feel they are reading a carefully edited Islamic book that
preserves the author's meaning, scholarly register, argument flow, reverence,
and technical precision.

Editorial boundaries:
- Stay inside the source. Do not add arguments, soften claims, modernize the
  author's position, add rhetorical flourishes, invent metaphors, summarize
  away details, or expand beyond what is present or clearly implied.
- Remove translator-ish phrasing. Prefer natural book prose over word-for-word
  sentence order, while preserving all legal, theological, and evidentiary
  distinctions.
- Convert obvious source structure into natural book structure without changing
  meaning. Do not preserve raw editorial brackets around questions merely
  because they appear in the source. For example, source patterns like
  "[They said: ...]" should become a clean question label in the target
  language, followed by the question text.
- If the source is terse, keep it dignified and clear. Do not make it chatty.
- If the source is polemical, preserve the substance without making it harsher
  than the source.
- Follow the supplied translation_profile guide. It may ask you to preserve
  selected Arabic technical terms, or to translate more naturally for narrative
  genres. The profile refines style; it never permits adding meaning.

Style requirements:
- Output strict JSON only, with keys "title" and "content".
- The "content" value must be Markdown.
- Use clear paragraphs and natural sentence rhythm.
- Render Qur'an passages, hadith matn, and clearly quoted speech as standalone
  Markdown blockquotes (`> ...`). Do not keep them inline inside ordinary
  paragraphs, and do not use blockquotes for explanatory prose.
- Put references such as surah/ayah numbers or hadith attributions immediately
  before or after the relevant blockquote when the source gives them. Do not
  invent references.
- Use *italics* for transliterated technical terms such as *tawhid*, *shirk*,
  *iman*, *fiqh*, *sunnah*, *hadith*, *isnad*, *taqwa*, or similar terms.
- Use **bold** sparingly for important section labels or core propositions.
- Keep Qur'an/hadith references, names, honorifics, and Arabic book titles
  accurate. Do not invent references.
- If a technical term has no exact equivalent, keep a transliterated term and
  briefly clarify it in smooth prose on first use.
- Do not add translator commentary, footnotes, apologies, or external
  explanations.

Example JSON output:
{
  "title": "Introduction by the Editor",
  "content": "In this section, the author explains the foundation of *tawhid* with careful attention to the language of the early scholars."
}
"""


TARGET_NAMES = {
    "id": "Indonesian",
    "en": "English",
}


TARGET_STYLE_GUIDES = {
    "id": """Indonesian style guide:
- Write in formal, fluent Indonesian suitable for a printed Islamic book.
- Prefer "Allah Ta'ala" when the source uses exaltation language.
- For question-answer sections, use **Pertanyaan:** and **Jawaban:**. Avoid
  raw forms like "[Mereka berkata: ...]" unless the brackets are part of a
  bibliographic reference or manuscript note.
- Use consistent Indonesian scholarly terms: *tauhid*, *syirik*, *iman*,
  *fikih*, *sunah*, *hadis*, *isnad*, *takwa*.
- Avoid awkward calques such as "melakukan doa kepada"; use natural phrasing
  like "berdoa kepada" when faithful to the source.
- Keep honorifics readable and consistent; do not overdecorate every name.""",
    "en": """English style guide:
- Write in formal, fluent English suitable for a published Islamic studies
  book.
- Prefer "Allah, the Exalted" or "Allah Most High" only where the source
  indicates exaltation.
- For question-answer sections, use **Question:** and **Answer:**. Avoid raw
  forms like "[They said: ...]" unless the brackets are part of a bibliographic
  reference or manuscript note.
- Use consistent scholarly terms: *tawhid*, *shirk*, *iman*, *fiqh*, *sunnah*,
  *hadith*, *isnad*, *taqwa*.
- Avoid archaic English unless needed for a quoted formula.""",
}


def main() -> int:
    args = parse_args()
    load_env_file(Path(args.env_file).expanduser())
    if not args.model:
        args.model = os.environ.get("DEEPSEEK_MODEL") or os.environ.get("RAG_LLM_MODEL") or "deepseek-v4-flash"
    if not args.deepseek_base_url:
        args.deepseek_base_url = (
            os.environ.get("DEEPSEEK_BASE_URL")
            or os.environ.get("RAG_LLM_BASE_URL")
            or "https://api.deepseek.com"
        )
    if not args.provider_name:
        args.provider_name = resolve_provider_name(args.deepseek_base_url)
    initialize_generation_runs(args)

    api_key = os.environ.get(args.api_key_env) or os.environ.get("RAG_LLM_API_KEY")
    if not api_key and not args.dry_run:
        raise SystemExit(
            f"{args.api_key_env} or RAG_LLM_API_KEY is required. Put it in "
            f"{args.env_file} or export it first."
        )

    profile_map = load_translation_profiles(Path(args.profile_map).expanduser())
    book_metadata = fetch_book_metadata(args.base_url, args.book_id)
    profile_name, profile_source = resolve_translation_profile(args.profile, profile_map, book_metadata)
    selected_profile = profile_map["profiles"][profile_name]
    args.book_metadata = book_metadata
    args.profile_map_data = profile_map
    args.selected_profile = profile_name
    args.selected_profile_source = profile_source
    args.selected_profile_config = selected_profile
    print(
        "profile={profile} source={source} category={category}".format(
            profile=profile_name,
            source=profile_source,
            category=book_metadata.get("category_name") or "",
        ),
        file=sys.stderr,
    )

    heading_ids = args.heading_id or []
    if args.all_toc:
        heading_ids.extend(fetch_toc_heading_ids(args.base_url, args.book_id, args.source_lang))

    heading_ids = dedupe_preserve_order(heading_ids)
    if args.limit > 0:
        heading_ids = heading_ids[: args.limit]

    if not heading_ids:
        raise SystemExit("No headings selected. Use --heading-id or --all-toc.")
    if args.concurrency < 1:
        raise SystemExit("--concurrency must be at least 1.")

    out_path = Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)

    if args.resume and out_path.exists():
        required_kinds = ["translation"]
        if args.summary_only:
            required_kinds = ["heading_summary"]
        elif args.include_summary:
            required_kinds = ["translation", "heading_summary"]
        done_heading_ids = read_completed_heading_ids(out_path, args.book_id, args.target_lang, required_kinds)
        before_count = len(heading_ids)
        heading_ids = [heading_id for heading_id in heading_ids if heading_id not in done_heading_ids]
        skipped_count = before_count - len(heading_ids)
        if skipped_count:
            print(f"resume: skipped {skipped_count} already-written headings", file=sys.stderr)

    if not heading_ids:
        print(f"nothing to do; all selected headings already exist in {out_path}", file=sys.stderr)
        return 0

    failure_path = Path(args.failure_out) if args.failure_out else out_path.with_suffix(out_path.suffix + ".failures.jsonl")
    failure_path.parent.mkdir(parents=True, exist_ok=True)
    out_mode = "a" if args.resume and out_path.exists() else "w"
    failure_mode = "a" if args.resume and failure_path.exists() else "w"

    success_count = 0
    failures: list[dict[str, Any]] = []
    generated_assets: list[dict[str, Any]] = []

    with out_path.open(out_mode, encoding="utf-8") as out_file, failure_path.open(failure_mode, encoding="utf-8") as failure_file:
        if args.concurrency == 1:
            for index, heading_id in enumerate(heading_ids, start=1):
                try:
                    assets = translate_heading_assets(args, api_key or "", heading_id, index, len(heading_ids))
                except Exception as err:
                    record_failure(failure_file, args, heading_id, index, len(heading_ids), err)
                    failures.append({"heading_id": heading_id, "error": str(err)})
                    if args.fail_fast:
                        raise
                    continue

                for asset in assets:
                    write_jsonl(out_file, asset)
                    generated_assets.append(asset)
                    success_count += 1
                if args.sleep_seconds > 0 and index < len(heading_ids):
                    time.sleep(args.sleep_seconds)
        else:
            with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as executor:
                futures: dict[concurrent.futures.Future[list[dict[str, Any]]], tuple[int, int]] = {}
                for index, heading_id in enumerate(heading_ids, start=1):
                    future = executor.submit(
                        translate_heading_assets,
                        args,
                        api_key or "",
                        heading_id,
                        index,
                        len(heading_ids),
                    )
                    futures[future] = (index, heading_id)
                    if args.sleep_seconds > 0 and index < len(heading_ids):
                        time.sleep(args.sleep_seconds)

                for future in concurrent.futures.as_completed(futures):
                    index, heading_id = futures[future]
                    try:
                        assets = future.result()
                    except Exception as err:
                        record_failure(failure_file, args, heading_id, index, len(heading_ids), err)
                        failures.append({"heading_id": heading_id, "error": str(err)})
                        if args.fail_fast:
                            raise
                        continue

                    for asset in assets:
                        write_jsonl(out_file, asset)
                        generated_assets.append(asset)
                        success_count += 1
                    print(
                        f"[{index}/{len(heading_ids)}] done book={args.book_id} "
                        f"heading={heading_id} lang={args.target_lang}",
                        file=sys.stderr,
                    )

    print(f"wrote {success_count} JSONL records to {out_path}", file=sys.stderr)
    if args.eval_report:
        write_eval_report(Path(args.eval_report), args, generated_assets, failures)
    if failures:
        print(f"failed {len(failures)} headings; see {failure_path}", file=sys.stderr)
        return 1
    return 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8080", help="Surau backend base URL")
    parser.add_argument("--book-id", required=True, type=int, help="Book ID to translate")
    parser.add_argument("--heading-id", action="append", type=int, help="Heading ID; repeat for multiple sections")
    parser.add_argument("--all-toc", action="store_true", help="Translate all headings from the book TOC")
    parser.add_argument("--limit", type=int, default=0, help="Limit selected headings; 0 means no limit")
    parser.add_argument("--source-lang", default="ar", help="Source section language used for reader fetch")
    parser.add_argument("--target-lang", choices=sorted(TARGET_NAMES), default="id", help="Translation target language")
    parser.add_argument("--profile", choices=PROFILE_CHOICES, default="auto", help="Translation profile; auto uses book category metadata")
    parser.add_argument("--profile-map", default=str(DEFAULT_PROFILE_MAP), help="Translation profile JSON config")
    parser.add_argument("--out", required=True, help="Output JSONL file")
    parser.add_argument("--model", default=None, help="LLM model; defaults to DEEPSEEK_MODEL, RAG_LLM_MODEL, or deepseek-v4-flash")
    parser.add_argument("--deepseek-base-url", default=None, help="OpenAI-compatible API base URL")
    parser.add_argument("--provider-name", default=None, help="Provider label written to metadata; defaults from LLM_PROVIDER_NAME or base URL")
    parser.add_argument("--api-key-env", default="DEEPSEEK_API_KEY", help="Environment variable containing the DeepSeek API key")
    parser.add_argument(
        "--env-file",
        default=str(DEFAULT_ENV_FILE),
        help="Local dotenv file loaded before reading the API key",
    )
    parser.add_argument("--max-tokens", type=int, default=6000)
    parser.add_argument("--max-source-chars", type=int, default=0, help="Trim long source sections for test runs; 0 disables trimming")
    parser.add_argument("--timeout-seconds", type=int, default=120)
    parser.add_argument("--retries", type=int, default=2)
    parser.add_argument("--sleep-seconds", type=float, default=0.5)
    parser.add_argument("--concurrency", type=int, default=1, help="Number of heading translation workers")
    parser.add_argument("--resume", action="store_true", help="Append to --out and skip headings already present in that JSONL")
    parser.add_argument("--failure-out", default="", help="Failure JSONL path; defaults to OUT.failures.jsonl")
    parser.add_argument("--eval-report", default="", help="Write a compact JSON evaluation report for generated rows")
    parser.add_argument("--fail-fast", action="store_true", help="Stop on the first failed heading")
    parser.add_argument("--include-summary", action="store_true", help="Also translate existing source TOC summary rows")
    parser.add_argument("--summary-only", action="store_true", help="Only translate existing source TOC summaries")
    parser.add_argument("--dry-run", action="store_true", help="Fetch sections and write placeholder rows without calling DeepSeek")
    return parser.parse_args()


def initialize_generation_runs(args: argparse.Namespace) -> None:
    """Create one run per active prompt family for this invocation."""
    if not args.summary_only:
        args.translation_generation = new_generation_identity(
            args.model,
            READER_TRANSLATION_PROMPT_VERSION,
        )
    if args.include_summary or args.summary_only:
        args.summary_translation_generation = new_generation_identity(
            args.model,
            READER_SUMMARY_TRANSLATION_PROMPT_VERSION,
        )


def translate_heading_asset(
    args: argparse.Namespace,
    api_key: str,
    heading_id: int,
    index: int,
    total: int,
) -> dict[str, Any]:
    assets = translate_heading_assets(args, api_key, heading_id, index, total)
    for asset in assets:
        if asset.get("kind") == "translation":
            return asset
    return assets[0]


def translate_heading_assets(
    args: argparse.Namespace,
    api_key: str,
    heading_id: int,
    index: int,
    total: int,
) -> list[dict[str, Any]]:
    print(
        f"[{index}/{total}] translating book={args.book_id} "
        f"heading={heading_id} lang={args.target_lang}",
        file=sys.stderr,
    )

    section = fetch_toc_section(args.base_url, args.book_id, heading_id, args.source_lang)
    original_text = section.get("original_text", "")
    source_text = original_text
    if args.max_source_chars > 0 and len(source_text) > args.max_source_chars:
        source_text = source_text[: args.max_source_chars].rstrip()

    assets: list[dict[str, Any]] = []
    summary_only = bool(getattr(args, "summary_only", False))
    include_summary = bool(getattr(args, "include_summary", False))

    if not summary_only:
        if args.dry_run:
            translated = {
                "title": f"[DRY RUN] {section.get('title', '')}",
                "content": source_text[:500],
            }
        else:
            translated = translate_section(
                api_key=api_key,
                deepseek_base_url=args.deepseek_base_url,
                model=args.model,
                target_lang=args.target_lang,
                book_metadata=args.book_metadata,
                profile_name=args.selected_profile,
                profile_source=args.selected_profile_source,
                profile_config=args.selected_profile_config,
                source_title=section.get("title", ""),
                source_text=source_text,
                max_tokens=args.max_tokens,
                timeout_seconds=args.timeout_seconds,
                retries=args.retries,
            )
        assets.append(
            build_translation_asset(
                args,
                heading_id,
                index,
                total,
                section,
                translated,
                bool(args.max_source_chars > 0 and len(original_text) > args.max_source_chars),
            )
        )

    if include_summary or summary_only:
        summary_asset = translate_summary_asset(args, api_key, heading_id, index, total, section)
        if summary_asset is not None:
            assets.append(summary_asset)

    return assets


def build_translation_asset(
    args: argparse.Namespace,
    heading_id: int,
    index: int,
    total: int,
    section: dict[str, Any],
    translated: dict[str, str],
    truncated_source: bool,
) -> dict[str, Any]:
    provider_name = getattr(args, "provider_name", None) or resolve_provider_name(args.deepseek_base_url)
    return {
        "kind": "translation",
        "book_id": args.book_id,
        "heading_id": heading_id,
        "lang": args.target_lang,
        "title": translated["title"],
        "content": translated["content"],
        "source": args.model,
        "translation_status": "generated",
        "provenance_class": MACHINE_PROVENANCE_CLASS,
        "generation": dict(args.translation_generation),
        "metadata": {
            "provider": provider_name,
            "model": args.model,
            "format": "markdown",
            "source_lang": args.source_lang,
            "target_lang": args.target_lang,
            "unit": "toc_section",
            "style_version": STYLE_VERSION,
            "translation_profile": args.selected_profile,
            "profile_source": args.selected_profile_source,
            "term_style": TERM_STYLE,
            "category_id": args.book_metadata.get("category_id"),
            "category_name": args.book_metadata.get("category_name"),
            "selection_index": index,
            "selection_total": total,
            "generated_at": datetime.now(timezone.utc).isoformat(),
            "source_title": section.get("title", ""),
            "source_endpoint": f"/v1/books/{args.book_id}/toc/{heading_id}/read",
            "truncated_source": truncated_source,
        },
    }


def translate_summary_asset(
    args: argparse.Namespace,
    api_key: str,
    heading_id: int,
    index: int,
    total: int,
    section: dict[str, Any],
) -> dict[str, Any] | None:
    provider_name = getattr(args, "provider_name", None) or resolve_provider_name(args.deepseek_base_url)
    source_summary = str(section.get("summary") or "").strip()
    source_summary_lang = str(section.get("summary_lang") or args.source_lang).strip() or args.source_lang
    if not source_summary:
        if args.summary_only:
            raise RuntimeError(f"heading {heading_id} has no source summary in lang={args.source_lang}")
        print(f"[{index}/{total}] skip summary heading={heading_id}: no source summary", file=sys.stderr)
        return None

    if args.dry_run:
        translated_summary = f"[DRY RUN] {source_summary[:300]}"
    else:
        translated_summary = translate_summary(
            api_key=api_key,
            deepseek_base_url=args.deepseek_base_url,
            model=args.model,
            target_lang=args.target_lang,
            source_lang=source_summary_lang,
            source_title=section.get("title", ""),
            source_summary=source_summary,
            max_tokens=min(args.max_tokens, 1200),
            timeout_seconds=args.timeout_seconds,
            retries=args.retries,
        )

    return {
        "kind": "heading_summary",
        "book_id": args.book_id,
        "heading_id": heading_id,
        "lang": args.target_lang,
        "summary": translated_summary,
        "source": args.model,
        "summary_status": "generated",
        "provenance_class": MACHINE_PROVENANCE_CLASS,
        "generation": dict(args.summary_translation_generation),
        "metadata": {
            "provider": provider_name,
            "model": args.model,
            "unit": "toc_summary",
            "style_version": "reader-summary-v1",
            "source_lang": source_summary_lang,
            "target_lang": args.target_lang,
            "source_title": section.get("title", ""),
            "source_endpoint": f"/v1/books/{args.book_id}/toc/{heading_id}/read",
            "selection_index": index,
            "selection_total": total,
            "generated_at": datetime.now(timezone.utc).isoformat(),
        },
    }


def write_jsonl(out_file: Any, value: dict[str, Any]) -> None:
    out_file.write(json.dumps(value, ensure_ascii=False, separators=(",", ":")) + "\n")
    out_file.flush()


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
        "lang": args.target_lang,
        "selection_index": index,
        "selection_total": total,
        "error": str(err),
        "failed_at": datetime.now(timezone.utc).isoformat(),
    }
    write_jsonl(failure_file, record)
    print(
        f"[{index}/{total}] failed book={args.book_id} heading={heading_id}: {err}",
        file=sys.stderr,
    )


def read_completed_heading_ids(out_path: Path, book_id: int, lang: str, required_kinds: list[str] | None = None) -> set[int]:
    required = set(required_kinds or ["translation"])
    completed_by_heading: dict[int, set[str]] = {}
    with out_path.open("r", encoding="utf-8") as out_file:
        for line_number, line in enumerate(out_file, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                row = json.loads(line)
            except json.JSONDecodeError as err:
                raise RuntimeError(f"Invalid JSONL in {out_path}:{line_number}: {err}") from err
            kind = str(row.get("kind") or "")
            if kind not in required:
                continue
            if int(row.get("book_id", 0)) == book_id and row.get("lang") == lang:
                completed_by_heading.setdefault(int(row["heading_id"]), set()).add(kind)
    return {heading_id for heading_id, kinds in completed_by_heading.items() if required.issubset(kinds)}


def load_env_file(path: Path) -> None:
    if not path.exists():
        return

    for line_number, raw_line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[len("export ") :].strip()

        key, separator, value = line.partition("=")
        if not separator:
            raise RuntimeError(f"Invalid env line in {path}:{line_number}")

        key = key.strip()
        value = value.strip()
        if not key:
            raise RuntimeError(f"Invalid empty env key in {path}:{line_number}")
        if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
            value = value[1:-1]

        os.environ.setdefault(key, value)


def fetch_toc_heading_ids(base_url: str, book_id: int, lang: str) -> list[int]:
    url = f"{base_url.rstrip('/')}/v1/books/{book_id}/toc?{urllib.parse.urlencode({'lang': lang})}"
    toc = request_json("GET", url)

    heading_ids: list[int] = []

    def visit(nodes: list[dict[str, Any]]) -> None:
        for node in nodes:
            heading_ids.append(int(node["heading_id"]))
            visit(node.get("children") or [])

    visit(toc)
    return heading_ids


def fetch_toc_section(base_url: str, book_id: int, heading_id: int, lang: str) -> dict[str, Any]:
    path = f"/v1/books/{book_id}/toc/{heading_id}/read"
    url = f"{base_url.rstrip('/')}{path}?{urllib.parse.urlencode({'lang': lang})}"
    return request_json("GET", url)


def fetch_book_metadata(base_url: str, book_id: int) -> dict[str, Any]:
    url = f"{base_url.rstrip('/')}/v1/books/{book_id}?{urllib.parse.urlencode({'lang': 'ar'})}"
    payload = request_json("GET", url)
    if not isinstance(payload, dict):
        raise RuntimeError(f"GET {url} returned non-object book metadata")
    return payload


def load_translation_profiles(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except OSError as err:
        raise RuntimeError(f"failed to read profile map {path}: {err}") from err
    except json.JSONDecodeError as err:
        raise RuntimeError(f"invalid profile map JSON {path}: {err}") from err

    profiles = payload.get("profiles")
    if not isinstance(profiles, dict) or not profiles:
        raise RuntimeError(f"profile map {path} must contain a non-empty profiles object")
    if payload.get("style_version") != STYLE_VERSION:
        raise RuntimeError(f"profile map {path} must use style_version={STYLE_VERSION}")

    default_profile = str(payload.get("default_profile") or "general")
    if default_profile not in profiles:
        raise RuntimeError(f"profile map {path} default_profile={default_profile!r} is missing")

    for profile_name, profile in profiles.items():
        if not isinstance(profile, dict):
            raise RuntimeError(f"profile {profile_name!r} must be an object")
        if not str(profile.get("style_guide") or "").strip():
            raise RuntimeError(f"profile {profile_name!r} is missing style_guide")
        if not str(profile.get("term_policy") or "").strip():
            raise RuntimeError(f"profile {profile_name!r} is missing term_policy")

    return payload


def resolve_translation_profile(
    requested_profile: str,
    profile_map: dict[str, Any],
    book_metadata: dict[str, Any],
) -> tuple[str, str]:
    profiles = profile_map["profiles"]
    if requested_profile != "auto":
        if requested_profile not in profiles:
            raise RuntimeError(f"profile {requested_profile!r} is not present in the profile map")
        return requested_profile, "manual"

    default_profile = str(profile_map.get("default_profile") or "general")
    category_text = metadata_text(book_metadata, ["category_name"])
    book_text = metadata_text(book_metadata, ["name", "bibliography", "hint", "description"])

    best_profile = default_profile
    best_score = 0
    best_matches: list[str] = []
    for profile_name, profile in profiles.items():
        if profile_name == default_profile:
            continue

        score = 0
        matches: list[str] = []
        for keyword in profile.get("category_keywords") or []:
            if keyword_matches(keyword, category_text):
                score += 4
                matches.append(f"category:{keyword}")
        for keyword in profile.get("book_keywords") or []:
            if keyword_matches(keyword, book_text):
                score += 2
                matches.append(f"book:{keyword}")

        if score > best_score:
            best_profile = profile_name
            best_score = score
            best_matches = matches

    if best_score == 0:
        return default_profile, "auto:default"
    return best_profile, "auto:" + ",".join(best_matches[:3])


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


def metadata_text(metadata: dict[str, Any], keys: list[str]) -> str:
    values: list[str] = []
    for key in keys:
        value = metadata.get(key)
        if value is None:
            continue
        if isinstance(value, (dict, list)):
            values.append(json.dumps(value, ensure_ascii=False))
        else:
            values.append(str(value))
    return normalize_match_text(" ".join(values))


def normalize_match_text(value: str) -> str:
    return " ".join(value.casefold().replace("ـ", "").split())


def keyword_matches(keyword: Any, text: str) -> bool:
    keyword_text = normalize_match_text(str(keyword or ""))
    return bool(keyword_text and keyword_text in text)


def write_eval_report(
    report_path: Path,
    args: argparse.Namespace,
    generated_assets: list[dict[str, Any]],
    failures: list[dict[str, Any]],
) -> None:
    with TemporaryDirectory() as tmp_dir:
        tmp_path = Path(tmp_dir) / "generated.jsonl"
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
                "lang": args.target_lang,
                "all_toc": False,
                "report": "",
                "strict": False,
                "profile_map": args.profile_map,
            },
        )()
        qa_report = qa_reader_assets.run_qa(qa_args)

    issue_counts: dict[int, dict[str, int]] = {}
    for issue in qa_report["issues"]:
        line = issue.get("line")
        if not isinstance(line, int):
            continue
        counts = issue_counts.setdefault(line, {"warnings": 0, "failures": 0})
        if issue.get("severity") == "FAIL":
            counts["failures"] += 1
        elif issue.get("severity") == "WARN":
            counts["warnings"] += 1

    items: list[dict[str, Any]] = []
    for line, asset in enumerate(generated_assets, start=1):
        counts = issue_counts.get(line, {"warnings": 0, "failures": 0})
        status = "FAIL" if counts["failures"] else "WARN" if counts["warnings"] else "PASS"
        metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}
        items.append(
            {
                "book_id": asset.get("book_id"),
                "heading_id": asset.get("heading_id"),
                "lang": asset.get("lang"),
                "translation_profile": metadata.get("translation_profile"),
                "profile_source": metadata.get("profile_source"),
                "content_chars": len(str(asset.get("content") or "")),
                "qa_status": status,
                "warning_count": counts["warnings"],
                "failure_count": counts["failures"],
            }
        )

    summary = qa_report["summary"]
    report = {
        "book_id": args.book_id,
        "lang": args.target_lang,
        "profile": args.selected_profile,
        "profile_source": args.selected_profile_source,
        "style_version": STYLE_VERSION,
        "generated_count": len(generated_assets),
        "failure_count": len(failures),
        "qa_status": "FAIL" if summary["failures"] else "WARN" if summary["warnings"] else "PASS",
        "qa_summary": summary,
        "items": items,
        "failures": failures,
    }

    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"eval_report={report_path}", file=sys.stderr)


def translate_section(
    *,
    api_key: str,
    deepseek_base_url: str,
    model: str,
    target_lang: str,
    book_metadata: dict[str, Any],
    profile_name: str,
    profile_source: str,
    profile_config: dict[str, Any],
    source_title: str,
    source_text: str,
    max_tokens: int,
    timeout_seconds: int,
    retries: int,
) -> dict[str, str]:
    target_name = TARGET_NAMES[target_lang]
    user_payload = {
        "target_language": target_name,
        "target_style_guide": TARGET_STYLE_GUIDES[target_lang],
        "source_language": "Arabic",
        "book_title": book_metadata.get("name", ""),
        "category_id": book_metadata.get("category_id"),
        "category_name": book_metadata.get("category_name", ""),
        "translation_profile": profile_name,
        "profile_source": profile_source,
        "profile_style_guide": profile_config.get("style_guide", ""),
        "term_policy": profile_config.get("term_policy", ""),
        "term_style": TERM_STYLE,
        "source_title": source_title,
        "source_text": source_text,
        "json_schema": {"title": "string", "content": "markdown string"},
    }
    payload = {
        "model": model,
        "messages": [
            {"role": "system", "content": SYSTEM_PROMPT},
            {
                "role": "user",
                "content": (
                    "Translate the following Arabic Islamic text into "
                    f"{target_name}. Use the supplied translation_profile "
                    "and term_policy. Return json only.\n\n"
                    + json.dumps(user_payload, ensure_ascii=False)
                ),
            },
        ],
        "response_format": {"type": "json_object"},
        "thinking": {"type": "disabled"},
        "temperature": 0.35,
        "max_tokens": max_tokens,
    }
    headers = {"Authorization": f"Bearer {api_key}"}
    url = f"{deepseek_base_url.rstrip('/')}/chat/completions"

    response = request_json("POST", url, headers=headers, payload=payload, timeout_seconds=timeout_seconds, retries=retries)
    message = response["choices"][0]["message"]
    content = message.get("content") or message.get("reasoning_content", "")
    translated = load_json_object(content)

    title = str(translated.get("title", "")).strip()
    body = str(translated.get("content", "")).strip()
    if not title or not body:
        raise RuntimeError("DeepSeek returned JSON without non-empty title/content")

    return {"title": title, "content": body}


def translate_summary(
    *,
    api_key: str,
    deepseek_base_url: str,
    model: str,
    target_lang: str,
    source_lang: str,
    source_title: str,
    source_summary: str,
    max_tokens: int,
    timeout_seconds: int,
    retries: int,
) -> str:
    target_name = TARGET_NAMES[target_lang]
    payload = {
        "model": model,
        "messages": [
            {
                "role": "system",
                "content": (
                    "Translate reader-facing Islamic book section summaries faithfully. "
                    "Keep the summary concise and natural. Do not add details outside "
                    "the source summary. Return strict JSON only with key \"summary\"."
                ),
            },
            {
                "role": "user",
                "content": json.dumps(
                    {
                        "target_language": target_name,
                        "target_style_guide": TARGET_STYLE_GUIDES[target_lang],
                        "source_language": source_lang,
                        "source_title": source_title,
                        "source_summary": source_summary,
                        "json_schema": {"summary": "string"},
                    },
                    ensure_ascii=False,
                ),
            },
        ],
        "response_format": {"type": "json_object"},
        "thinking": {"type": "disabled"},
        "temperature": 0.25,
        "max_tokens": max_tokens,
    }
    response = request_json(
        "POST",
        f"{deepseek_base_url.rstrip('/')}/chat/completions",
        headers={"Authorization": f"Bearer {api_key}"},
        payload=payload,
        timeout_seconds=timeout_seconds,
        retries=retries,
    )
    message = response["choices"][0]["message"]
    content = message.get("content") or message.get("reasoning_content", "")
    parsed = load_json_object(content)
    summary = str(parsed.get("summary") or "").strip()
    if not summary:
        raise RuntimeError("DeepSeek returned JSON without non-empty summary")
    return summary


def request_json(
    method: str,
    url: str,
    *,
    headers: dict[str, str] | None = None,
    payload: dict[str, Any] | None = None,
    timeout_seconds: int = 60,
    retries: int = 0,
) -> Any:
    body = None
    request_headers = {"Accept": "application/json"}
    if headers:
        request_headers.update(headers)
    if payload is not None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        request_headers["Content-Type"] = "application/json"

    last_error: Exception | None = None
    for attempt in range(retries + 1):
        req = urllib.request.Request(url, data=body, headers=request_headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=timeout_seconds) as resp:
                return json.loads(resp.read().decode("utf-8"))
        except urllib.error.HTTPError as err:
            detail = err.read().decode("utf-8", errors="replace")
            last_error = RuntimeError(f"{method} {url} failed: HTTP {err.code}: {detail}")
            if err.code not in {429, 500, 502, 503, 504} or attempt >= retries:
                raise last_error
        except (urllib.error.URLError, TimeoutError) as err:
            last_error = err
            if attempt >= retries:
                raise RuntimeError(f"{method} {url} failed: {err}") from err

        time.sleep(1.5 * (attempt + 1))

    raise RuntimeError(f"{method} {url} failed: {last_error}")


def load_json_object(value: str) -> dict[str, Any]:
    cleaned = value.strip()
    if cleaned.startswith("```"):
        lines = cleaned.splitlines()
        if lines and lines[0].startswith("```"):
            lines = lines[1:]
        if lines and lines[-1].startswith("```"):
            lines = lines[:-1]
        cleaned = "\n".join(lines).strip()

    parsed = json.loads(cleaned)
    if not isinstance(parsed, dict):
        raise RuntimeError("Expected JSON object from translation model")
    return parsed


def dedupe_preserve_order(values: list[int]) -> list[int]:
    seen = set()
    result = []
    for value in values:
        if value in seen:
            continue
        seen.add(value)
        result.append(value)
    return result


if __name__ == "__main__":
    raise SystemExit(main())
