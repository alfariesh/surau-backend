#!/usr/bin/env python3
"""QA generated Surau reader asset JSONL files before import.

The script is intentionally read-only. It validates translation JSONL rows,
optionally checks TOC completeness against the running backend, prints a compact
PASS/WARN/FAIL summary, and can write a machine-readable JSON report.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any


FAIL = "FAIL"
WARN = "WARN"
PASS = "PASS"

MIN_CONTENT_CHARS_FAIL = 120
MIN_CONTENT_CHARS_WARN = 300
MANY_FOOTNOTE_THRESHOLD = 2
VALID_TRANSLATION_STATUSES = {"generated", "reviewed"}
SCRIPT_DIR = Path(__file__).resolve().parent
DEFAULT_PROFILE_MAP = SCRIPT_DIR / "translation_profiles.json"
STYLE_VERSION = "reader-profile-v1"
TECHNICAL_ITALIC_CHECK_CHARS = 1200

RAW_BRACKET_RE = re.compile(
    r"\[\s*(?:Mereka\s+berkata|They\s+said|قالوا|قال)\s*[:：][^\]]{10,}\]",
    re.IGNORECASE,
)
FOOTNOTE_RE = re.compile(r"\[\d{1,3}\]")
EMPTY_HEADING_RE = re.compile(r"(?m)^#{1,6}\s*$")
BLOCKQUOTE_RE = re.compile(r"(?m)^>\s+\S")
ITALIC_TERM_RE = re.compile(r"(?<!\*)\*[^*\n]{2,60}\*(?!\*)|(?<!_)_[^_\n]{2,60}_(?!_)")
SCRIPTURE_OR_HADITH_RE = re.compile(
    r"(QS\.|Q\.S\.|Al-Qur['’`]?an|Allah(?:\s+Ta['’`]?ala)?\s+berfirman|"
    r"Rasulullah[^.\n]{0,120}(?:bersabda|berkata)|\bHR\.|Muttafaq\s+['’`]?alaih|"
    r"hadis|hadith)",
    re.IGNORECASE,
)


@dataclass(frozen=True)
class Issue:
    severity: str
    code: str
    message: str
    line: int | None = None
    book_id: int | None = None
    heading_id: int | None = None
    lang: str | None = None

    def to_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "severity": self.severity,
            "code": self.code,
            "message": self.message,
        }
        if self.line is not None:
            payload["line"] = self.line
        if self.book_id is not None:
            payload["book_id"] = self.book_id
        if self.heading_id is not None:
            payload["heading_id"] = self.heading_id
        if self.lang is not None:
            payload["lang"] = self.lang
        return payload


@dataclass
class AssetRow:
    line: int
    raw: dict[str, Any]

    @property
    def kind(self) -> str:
        return str(self.raw.get("kind", "")).strip()

    @property
    def book_id(self) -> int | None:
        return as_positive_int(self.raw.get("book_id"))

    @property
    def heading_id(self) -> int | None:
        return as_positive_int(self.raw.get("heading_id"))

    @property
    def lang(self) -> str:
        return str(self.raw.get("lang", "")).strip().lower()

    @property
    def title(self) -> str:
        return str(self.raw.get("title", "")).strip()

    @property
    def content(self) -> str:
        return str(self.raw.get("content", "")).strip()

    @property
    def metadata(self) -> Any:
        return self.raw.get("metadata")

    @property
    def translation_status(self) -> str:
        return str(self.raw.get("translation_status") or "generated").strip().lower()

    @property
    def translation_reviewed_by(self) -> str:
        return str(self.raw.get("translation_reviewed_by") or "").strip()


def main() -> int:
    args = parse_args()
    report = run_qa(args)
    print_report(report)

    if args.report:
        report_path = Path(args.report)
        report_path.parent.mkdir(parents=True, exist_ok=True)
        report_path.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n",
            encoding="utf-8",
        )
        print(f"report={report_path}", file=sys.stderr)

    return 1 if report["summary"]["failures"] > 0 else 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--file", required=True, help="Reader asset JSONL file")
    parser.add_argument("--base-url", default="http://127.0.0.1:8080", help="Surau backend base URL")
    parser.add_argument("--book-id", type=int, default=0, help="Expected book ID")
    parser.add_argument("--lang", default="", help="Expected language code")
    parser.add_argument("--all-toc", action="store_true", help="Require all TOC headings to have translation rows")
    parser.add_argument("--report", default="", help="Write machine-readable JSON report")
    parser.add_argument("--strict", action="store_true", help="Treat warnings as failures")
    parser.add_argument("--profile-map", default=str(DEFAULT_PROFILE_MAP), help="Translation profile JSON config")
    return parser.parse_args()


def run_qa(args: argparse.Namespace) -> dict[str, Any]:
    file_path = Path(args.file)
    issues: list[Issue] = []
    profile_map, profile_issues = load_profile_map(Path(args.profile_map).expanduser())
    issues.extend(profile_issues)
    rows, parse_issues = read_jsonl(file_path)
    issues.extend(parse_issues)

    expected_book_id = args.book_id if args.book_id > 0 else None
    expected_lang = args.lang.strip().lower() or None
    if args.all_toc and expected_book_id is None:
        issues.append(Issue(FAIL, "ALL_TOC_REQUIRES_BOOK_ID", "--all-toc requires --book-id"))

    translation_rows = [row for row in rows if row.kind == "translation"]
    audio_rows = [row for row in rows if row.kind == "audio"]
    ignored_rows = [row for row in rows if row.kind not in {"translation", "audio"}]

    for row in translation_rows:
        issues.extend(validate_common_shape(row, expected_book_id, expected_lang))

    seen: dict[tuple[int, int, str], AssetRow] = {}
    for row in translation_rows:
        issues.extend(validate_translation_row(row, profile_map))
        key = (row.book_id or 0, row.heading_id or 0, row.lang)
        if all(key):
            if key in seen:
                first = seen[key]
                issues.append(
                    row_issue(
                        FAIL,
                        "DUPLICATE_TRANSLATION",
                        f"duplicate translation key first seen at line {first.line}",
                        row,
                    )
                )
            else:
                seen[key] = row

    inferred_lang = expected_lang or infer_single_lang(translation_rows, issues)
    toc_summary: dict[str, Any] | None = None
    if args.all_toc and expected_book_id is not None and inferred_lang:
        toc_summary = check_toc_completeness(
            args.base_url,
            expected_book_id,
            inferred_lang,
            translation_rows,
            issues,
        )

    if args.strict:
        issues = [
            Issue(
                FAIL if issue.severity == WARN else issue.severity,
                issue.code,
                issue.message,
                issue.line,
                issue.book_id,
                issue.heading_id,
                issue.lang,
            )
            for issue in issues
        ]

    summary = summarize(rows, translation_rows, audio_rows, ignored_rows, issues)
    report: dict[str, Any] = {
        "file": str(file_path),
        "book_id": expected_book_id,
        "lang": inferred_lang,
        "all_toc": bool(args.all_toc),
        "strict": bool(args.strict),
        "summary": summary,
        "issues": [issue.to_dict() for issue in issues],
    }
    if toc_summary is not None:
        report["toc"] = toc_summary
    return report


def read_jsonl(path: Path) -> tuple[list[AssetRow], list[Issue]]:
    rows: list[AssetRow] = []
    issues: list[Issue] = []

    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as err:
        return [], [Issue(FAIL, "FILE_READ_FAILED", str(err))]

    for line_number, raw_line in enumerate(lines, start=1):
        line = raw_line.strip()
        if not line:
            continue
        try:
            payload = json.loads(line)
        except json.JSONDecodeError as err:
            issues.append(Issue(FAIL, "INVALID_JSON", f"invalid JSONL: {err}", line=line_number))
            continue
        if not isinstance(payload, dict):
            issues.append(Issue(FAIL, "JSON_OBJECT_REQUIRED", "JSONL row must be an object", line=line_number))
            continue
        rows.append(AssetRow(line=line_number, raw=payload))

    return rows, issues


def load_profile_map(path: Path) -> tuple[dict[str, Any], list[Issue]]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except OSError as err:
        return {"profiles": {}}, [Issue(FAIL, "PROFILE_MAP_READ_FAILED", str(err))]
    except json.JSONDecodeError as err:
        return {"profiles": {}}, [Issue(FAIL, "PROFILE_MAP_INVALID_JSON", f"invalid profile map JSON: {err}")]

    profiles = payload.get("profiles")
    if not isinstance(profiles, dict):
        return {"profiles": {}}, [Issue(FAIL, "PROFILE_MAP_INVALID", "profile map must contain profiles object")]
    if payload.get("style_version") != STYLE_VERSION:
        return payload, [
            Issue(
                FAIL,
                "PROFILE_MAP_STYLE_VERSION",
                f"profile map must use style_version={STYLE_VERSION}",
            )
        ]
    return payload, []


def validate_common_shape(row: AssetRow, expected_book_id: int | None, expected_lang: str | None) -> list[Issue]:
    issues: list[Issue] = []
    if not row.kind:
        issues.append(row_issue(FAIL, "MISSING_KIND", "kind is required", row))
    if row.book_id is None:
        issues.append(row_issue(FAIL, "MISSING_BOOK_ID", "book_id must be a positive integer", row))
    if row.heading_id is None:
        issues.append(row_issue(FAIL, "MISSING_HEADING_ID", "heading_id must be a positive integer", row))
    if not row.lang:
        issues.append(row_issue(FAIL, "MISSING_LANG", "lang is required", row))

    if expected_book_id is not None and row.book_id is not None and row.book_id != expected_book_id:
        issues.append(row_issue(FAIL, "BOOK_ID_MISMATCH", f"expected book_id={expected_book_id}", row))
    if expected_lang is not None and row.lang and row.lang != expected_lang:
        issues.append(row_issue(FAIL, "LANG_MISMATCH", f"expected lang={expected_lang}", row))

    metadata = row.metadata
    if metadata is not None and not isinstance(metadata, dict):
        issues.append(row_issue(FAIL, "INVALID_METADATA", "metadata must be a JSON object", row))

    return issues


def validate_translation_row(row: AssetRow, profile_map: dict[str, Any]) -> list[Issue]:
    issues: list[Issue] = []
    content = row.content
    title = row.title

    if not title:
        issues.append(row_issue(FAIL, "MISSING_TITLE", "translation title is required", row))
    if not content:
        issues.append(row_issue(FAIL, "MISSING_CONTENT", "translation content is required", row))
        return issues

    if row.translation_status not in VALID_TRANSLATION_STATUSES:
        issues.append(
            row_issue(
                FAIL,
                "INVALID_TRANSLATION_STATUS",
                "translation_status must be generated or reviewed",
                row,
            )
        )
    if row.translation_status == "reviewed" and not row.translation_reviewed_by:
        issues.append(row_issue(FAIL, "MISSING_REVIEWED_BY", "reviewed translations require translation_reviewed_by", row))

    metadata = row.metadata
    if metadata is None:
        issues.append(row_issue(WARN, "MISSING_METADATA", "metadata is missing", row))
    elif isinstance(metadata, dict):
        if metadata.get("truncated_source") is True:
            issues.append(row_issue(FAIL, "TRUNCATED_SOURCE", "metadata.truncated_source must not be true", row))
        issues.extend(validate_profile_metadata(row, metadata, profile_map))

    if "[DRY RUN]" in title or "[DRY RUN]" in content:
        issues.append(row_issue(FAIL, "DRY_RUN_PLACEHOLDER", "dry-run placeholder found", row))
    if RAW_BRACKET_RE.search(content):
        issues.append(row_issue(FAIL, "RAW_BRACKET_QUESTION", "raw source question bracket found", row))

    content_len = len(content)
    if content_len < MIN_CONTENT_CHARS_FAIL:
        issues.append(row_issue(FAIL, "CONTENT_TOO_SHORT", f"content has only {content_len} characters", row))
    elif content_len < MIN_CONTENT_CHARS_WARN:
        issues.append(row_issue(WARN, "CONTENT_SHORT", f"content has only {content_len} characters", row))

    if content.count("```") % 2 != 0:
        issues.append(row_issue(FAIL, "UNCLOSED_CODE_FENCE", "Markdown code fence is not closed", row))
    if EMPTY_HEADING_RE.search(content):
        issues.append(row_issue(WARN, "EMPTY_MARKDOWN_HEADING", "empty Markdown heading found", row))
    if "\n" not in content and len(content) > 1000:
        issues.append(row_issue(WARN, "SINGLE_LONG_PARAGRAPH", "content is one very long line", row))

    footnotes = FOOTNOTE_RE.findall(content)
    if len(footnotes) >= MANY_FOOTNOTE_THRESHOLD:
        issues.append(row_issue(WARN, "MANY_FOOTNOTES", f"found {len(footnotes)} numeric footnotes", row))

    if SCRIPTURE_OR_HADITH_RE.search(content) and not BLOCKQUOTE_RE.search(content):
        issues.append(row_issue(WARN, "MISSING_BLOCKQUOTE", "section appears to cite scripture/hadith but has no blockquote", row))

    return issues


def validate_profile_metadata(row: AssetRow, metadata: dict[str, Any], profile_map: dict[str, Any]) -> list[Issue]:
    issues: list[Issue] = []
    profiles = profile_map.get("profiles")
    if not isinstance(profiles, dict):
        profiles = {}

    profile_name = str(metadata.get("translation_profile") or "").strip()
    if not profile_name:
        issues.append(row_issue(WARN, "MISSING_TRANSLATION_PROFILE", "metadata.translation_profile is missing", row))
        return issues
    if profile_name not in profiles:
        issues.append(row_issue(FAIL, "INVALID_TRANSLATION_PROFILE", f"unknown translation_profile={profile_name}", row))
        return issues

    style_version = str(metadata.get("style_version") or "").strip()
    if style_version != STYLE_VERSION:
        issues.append(
            row_issue(
                WARN,
                "STYLE_VERSION_MISMATCH",
                f"metadata.style_version should be {STYLE_VERSION}",
                row,
            )
        )

    profile = profiles.get(profile_name)
    is_technical = isinstance(profile, dict) and bool(profile.get("technical"))
    if is_technical and len(row.content) >= TECHNICAL_ITALIC_CHECK_CHARS and not ITALIC_TERM_RE.search(row.content):
        issues.append(
            row_issue(
                WARN,
                "MISSING_TECHNICAL_ITALICS",
                "technical profile section has no italicized technical term",
                row,
            )
        )

    return issues


def infer_single_lang(rows: list[AssetRow], issues: list[Issue]) -> str | None:
    langs = {row.lang for row in rows if row.lang}
    if len(langs) == 1:
        return next(iter(langs))
    if len(langs) > 1:
        issues.append(Issue(FAIL, "LANG_AMBIGUOUS", "multiple translation languages found; pass --lang"))
    return None


def check_toc_completeness(
    base_url: str,
    book_id: int,
    lang: str,
    translation_rows: list[AssetRow],
    issues: list[Issue],
) -> dict[str, Any]:
    expected_ids: list[int] = []
    translated_ids = {row.heading_id for row in translation_rows if row.book_id == book_id and row.lang == lang and row.heading_id}

    try:
        toc = fetch_toc(base_url, book_id, lang)
    except Exception as err:
        issues.append(Issue(FAIL, "TOC_FETCH_FAILED", str(err), book_id=book_id, lang=lang))
        return {
            "expected_count": 0,
            "translated_count": len(translated_ids),
            "missing_heading_ids": [],
            "extra_heading_ids": sorted(translated_ids),
        }

    expected_ids = flatten_toc_heading_ids(toc)
    expected_set = set(expected_ids)
    missing = [heading_id for heading_id in expected_ids if heading_id not in translated_ids]
    extra = sorted(heading_id for heading_id in translated_ids if heading_id not in expected_set)

    for heading_id in missing:
        issues.append(
            Issue(
                FAIL,
                "MISSING_TOC_TRANSLATION",
                "TOC heading has no translation row",
                book_id=book_id,
                heading_id=heading_id,
                lang=lang,
            )
        )
    for heading_id in extra:
        issues.append(
            Issue(
                WARN,
                "EXTRA_TRANSLATION",
                "translation row is not present in current TOC",
                book_id=book_id,
                heading_id=heading_id,
                lang=lang,
            )
        )

    return {
        "expected_count": len(expected_ids),
        "translated_count": len(translated_ids & expected_set),
        "missing_heading_ids": missing,
        "extra_heading_ids": extra,
    }


def fetch_toc(base_url: str, book_id: int, lang: str) -> list[dict[str, Any]]:
    url = f"{base_url.rstrip('/')}/v1/books/{book_id}/toc?{urllib.parse.urlencode({'lang': lang})}"
    req = urllib.request.Request(url, headers={"Accept": "application/json"}, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as err:
        detail = err.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"GET {url} failed: HTTP {err.code}: {detail}") from err
    except (urllib.error.URLError, TimeoutError) as err:
        raise RuntimeError(f"GET {url} failed: {err}") from err

    if not isinstance(payload, list):
        raise RuntimeError(f"GET {url} returned non-list TOC")
    return payload


def flatten_toc_heading_ids(nodes: list[dict[str, Any]]) -> list[int]:
    heading_ids: list[int] = []

    def visit(items: list[dict[str, Any]]) -> None:
        for item in items:
            heading_id = as_positive_int(item.get("heading_id"))
            if heading_id is not None:
                heading_ids.append(heading_id)
            children = item.get("children") or []
            if isinstance(children, list):
                visit(children)

    visit(nodes)
    return heading_ids


def summarize(
    rows: list[AssetRow],
    translation_rows: list[AssetRow],
    audio_rows: list[AssetRow],
    ignored_rows: list[AssetRow],
    issues: list[Issue],
) -> dict[str, int]:
    row_state: dict[int, str] = {row.line: PASS for row in translation_rows}

    for issue in issues:
        if issue.line is None:
            continue
        if issue.line not in row_state:
            row_state[issue.line] = PASS
        if issue.severity == FAIL:
            row_state[issue.line] = FAIL
        elif issue.severity == WARN and row_state[issue.line] != FAIL:
            row_state[issue.line] = WARN

    pass_rows = sum(1 for state in row_state.values() if state == PASS)
    warn_rows = sum(1 for state in row_state.values() if state == WARN)
    fail_rows = sum(1 for state in row_state.values() if state == FAIL)
    failures = sum(1 for issue in issues if issue.severity == FAIL)
    warnings = sum(1 for issue in issues if issue.severity == WARN)

    return {
        "total_rows": len(rows),
        "translations": len(translation_rows),
        "audio_ignored": len(audio_rows),
        "other_ignored": len(ignored_rows),
        "generated_rows": sum(1 for row in translation_rows if row.translation_status == "generated"),
        "reviewed_rows": sum(1 for row in translation_rows if row.translation_status == "reviewed"),
        "pass_rows": pass_rows,
        "warn_rows": warn_rows,
        "fail_rows": fail_rows,
        "warnings": warnings,
        "failures": failures,
    }


def print_report(report: dict[str, Any]) -> None:
    summary = report["summary"]
    status = FAIL if summary["failures"] > 0 else WARN if summary["warnings"] > 0 else PASS
    print(f"{status} {report['file']}")
    print(
        "rows={total_rows} translations={translations} audio_ignored={audio_ignored} "
        "other_ignored={other_ignored} "
        "generated={generated_rows} reviewed={reviewed_rows} "
        "pass_rows={pass_rows} warn_rows={warn_rows} fail_rows={fail_rows} "
        "warnings={warnings} failures={failures}".format(**summary)
    )

    toc = report.get("toc")
    if toc:
        print(
            "toc_expected={expected_count} toc_translated={translated_count} "
            "toc_missing={missing}".format(
                expected_count=toc["expected_count"],
                translated_count=toc["translated_count"],
                missing=len(toc["missing_heading_ids"]),
            )
        )

    for issue in report["issues"]:
        location = []
        if "line" in issue:
            location.append(f"line={issue['line']}")
        if "book_id" in issue:
            location.append(f"book={issue['book_id']}")
        if "heading_id" in issue:
            location.append(f"heading={issue['heading_id']}")
        if "lang" in issue:
            location.append(f"lang={issue['lang']}")
        suffix = f" {' '.join(location)}" if location else ""
        print(f"{issue['severity']} {issue['code']}{suffix}: {issue['message']}")


def row_issue(severity: str, code: str, message: str, row: AssetRow) -> Issue:
    return Issue(
        severity=severity,
        code=code,
        message=message,
        line=row.line,
        book_id=row.book_id,
        heading_id=row.heading_id,
        lang=row.lang or None,
    )


def as_positive_int(value: Any) -> int | None:
    if isinstance(value, bool):
        return None
    try:
        number = int(value)
    except (TypeError, ValueError):
        return None
    if number <= 0:
        return None
    return number


if __name__ == "__main__":
    raise SystemExit(main())
