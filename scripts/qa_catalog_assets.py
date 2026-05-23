#!/usr/bin/env python3
"""QA generated Surau catalog translation JSONL files before import.

This script validates catalog rows produced by translate_catalog_assets.py:
book_metadata_translation, author_translation, and category_translation. It is
read-only and exits non-zero only for FAIL issues.
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
from datetime import datetime
from pathlib import Path
from typing import Any


FAIL = "FAIL"
WARN = "WARN"
PASS = "PASS"

CATALOG_KINDS = {
    "book_metadata_translation",
    "author_translation",
    "category_translation",
}
VALID_TRANSLATION_STATUSES = {"generated", "reviewed"}
DRY_RUN_RE = re.compile(r"\[DRY RUN\]", re.IGNORECASE)
PLACEHOLDER_RE = re.compile(r"\b(TODO|FIXME|lorem ipsum)\b", re.IGNORECASE)
ARABIC_RE = re.compile(r"[\u0600-\u06ff]")


@dataclass(frozen=True)
class Issue:
    severity: str
    code: str
    message: str
    line: int | None = None
    kind: str | None = None
    object_id: int | None = None
    lang: str | None = None

    def to_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "severity": self.severity,
            "code": self.code,
            "message": self.message,
        }
        if self.line is not None:
            payload["line"] = self.line
        if self.kind:
            payload["kind"] = self.kind
        if self.object_id is not None:
            payload["object_id"] = self.object_id
        if self.lang:
            payload["lang"] = self.lang
        return payload


@dataclass
class CatalogRow:
    line: int
    raw: dict[str, Any]

    @property
    def kind(self) -> str:
        return str(self.raw.get("kind", "")).strip()

    @property
    def lang(self) -> str:
        return str(self.raw.get("lang", "")).strip().lower()

    @property
    def object_id(self) -> int | None:
        if self.kind == "book_metadata_translation":
            return as_positive_int(self.raw.get("book_id"))
        if self.kind == "author_translation":
            return as_positive_int(self.raw.get("author_id"))
        if self.kind == "category_translation":
            return as_positive_int(self.raw.get("category_id"))
        return None

    @property
    def translation_status(self) -> str:
        return str(self.raw.get("translation_status") or "generated").strip().lower()

    @property
    def translation_reviewed_by(self) -> str:
        return str(self.raw.get("translation_reviewed_by") or "").strip()

    @property
    def translation_reviewed_at(self) -> str:
        return str(self.raw.get("translation_reviewed_at") or "").strip()


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
    parser.add_argument("--file", required=True, help="Catalog asset JSONL file")
    parser.add_argument("--lang", default="", help="Expected language code")
    parser.add_argument("--base-url", default="http://127.0.0.1:8080", help="Surau backend base URL")
    parser.add_argument("--check-public-ids", action="store_true", help="Check IDs against public catalog endpoints")
    parser.add_argument("--report", default="", help="Write machine-readable JSON report")
    parser.add_argument("--strict", action="store_true", help="Treat warnings as failures")
    return parser.parse_args()


def run_qa(args: argparse.Namespace) -> dict[str, Any]:
    file_path = Path(args.file)
    issues: list[Issue] = []
    rows, parse_issues = read_jsonl(file_path)
    issues.extend(parse_issues)

    expected_lang = args.lang.strip().lower() or None
    catalog_rows = [row for row in rows if row.kind in CATALOG_KINDS]
    ignored_rows = [row for row in rows if row.kind not in CATALOG_KINDS]

    if not catalog_rows and not parse_issues:
        issues.append(Issue(FAIL, "NO_CATALOG_ROWS", "file contains no catalog translation rows"))

    seen: dict[tuple[str, int, str], CatalogRow] = {}
    for row in catalog_rows:
        issues.extend(validate_catalog_row(row, expected_lang))
        key = (row.kind, row.object_id or 0, row.lang)
        if all(key):
            if key in seen:
                issues.append(
                    row_issue(
                        FAIL,
                        "DUPLICATE_CATALOG_TRANSLATION",
                        f"duplicate catalog key first seen at line {seen[key].line}",
                        row,
                    )
                )
            else:
                seen[key] = row

    public_id_summary = None
    if args.check_public_ids and catalog_rows:
        public_id_summary = check_public_ids(args.base_url, catalog_rows, issues)

    if args.strict:
        issues = [
            Issue(
                FAIL if issue.severity == WARN else issue.severity,
                issue.code,
                issue.message,
                issue.line,
                issue.kind,
                issue.object_id,
                issue.lang,
            )
            for issue in issues
        ]

    report: dict[str, Any] = {
        "file": str(file_path),
        "lang": expected_lang or infer_single_lang(catalog_rows, issues),
        "strict": bool(args.strict),
        "check_public_ids": bool(args.check_public_ids),
        "summary": summarize(rows, catalog_rows, ignored_rows, issues),
        "issues": [issue.to_dict() for issue in issues],
    }
    if public_id_summary is not None:
        report["public_ids"] = public_id_summary
    return report


def read_jsonl(path: Path) -> tuple[list[CatalogRow], list[Issue]]:
    rows: list[CatalogRow] = []
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
        rows.append(CatalogRow(line=line_number, raw=payload))

    return rows, issues


def validate_catalog_row(row: CatalogRow, expected_lang: str | None) -> list[Issue]:
    issues: list[Issue] = []
    if row.object_id is None:
        issues.append(row_issue(FAIL, "MISSING_OBJECT_ID", "catalog object ID must be a positive integer", row))
    if not row.lang:
        issues.append(row_issue(FAIL, "MISSING_LANG", "lang is required", row))
    if expected_lang is not None and row.lang and row.lang != expected_lang:
        issues.append(row_issue(FAIL, "LANG_MISMATCH", f"expected lang={expected_lang}", row))

    metadata = row.raw.get("metadata")
    if metadata is not None and not isinstance(metadata, dict):
        issues.append(row_issue(FAIL, "INVALID_METADATA", "metadata must be a JSON object", row))

    if row.translation_status not in VALID_TRANSLATION_STATUSES:
        issues.append(row_issue(FAIL, "INVALID_TRANSLATION_STATUS", "translation_status must be generated or reviewed", row))
    if row.translation_status == "reviewed" and not row.translation_reviewed_by:
        issues.append(row_issue(FAIL, "MISSING_REVIEWED_BY", "reviewed rows require translation_reviewed_by", row))
    if row.translation_reviewed_at and not is_iso_datetime(row.translation_reviewed_at):
        issues.append(row_issue(FAIL, "INVALID_REVIEWED_AT", "translation_reviewed_at must be an ISO datetime string", row))

    text_fields = translatable_text_fields(row)
    if not any(value for value in text_fields.values()):
        issues.append(row_issue(FAIL, "NO_TRANSLATED_TEXT", "at least one translated text field is required", row))

    required_field = required_text_field(row.kind)
    if required_field and not text_fields.get(required_field, ""):
        issues.append(row_issue(FAIL, "MISSING_REQUIRED_TEXT", f"{required_field} is required", row))

    for field, value in text_fields.items():
        if not value:
            continue
        if DRY_RUN_RE.search(value):
            issues.append(row_issue(FAIL, "DRY_RUN_PLACEHOLDER", f"{field} contains a dry-run placeholder", row))
        if PLACEHOLDER_RE.search(value):
            issues.append(row_issue(FAIL, "PLACEHOLDER_TEXT", f"{field} contains placeholder text", row))
        if arabic_ratio(value) > 0.35:
            issues.append(row_issue(WARN, "ARABIC_HEAVY_TEXT", f"{field} still looks mostly Arabic", row))

    return issues


def translatable_text_fields(row: CatalogRow) -> dict[str, str]:
    if row.kind == "book_metadata_translation":
        return {
            "display_title": clean_text(row.raw.get("display_title")),
            "bibliography": clean_text(row.raw.get("bibliography")),
            "hint": clean_text(row.raw.get("hint")),
            "description": clean_text(row.raw.get("description")),
        }
    if row.kind == "author_translation":
        return {
            "name": clean_text(row.raw.get("name")),
            "biography": clean_text(row.raw.get("biography")),
            "death_text": clean_text(row.raw.get("death_text")),
        }
    if row.kind == "category_translation":
        return {"name": clean_text(row.raw.get("name"))}
    return {}


def required_text_field(kind: str) -> str:
    if kind == "book_metadata_translation":
        return "display_title"
    if kind in {"author_translation", "category_translation"}:
        return "name"
    return ""


def check_public_ids(base_url: str, rows: list[CatalogRow], issues: list[Issue]) -> dict[str, Any]:
    summary = {
        "checked": 0,
        "missing": 0,
        "missing_keys": [],
    }
    cache: dict[str, set[int]] = {}
    for row in rows:
        if row.object_id is None:
            continue
        kind = row.kind
        try:
            known_ids = cache.setdefault(kind, fetch_public_ids(base_url, kind))
        except Exception as err:
            issues.append(row_issue(FAIL, "PUBLIC_ID_FETCH_FAILED", str(err), row))
            continue

        summary["checked"] += 1
        if row.object_id not in known_ids:
            summary["missing"] += 1
            key = f"{kind}:{row.object_id}"
            summary["missing_keys"].append(key)
            issues.append(row_issue(WARN, "PUBLIC_ID_NOT_FOUND", "ID was not found in public catalog endpoints", row))

    return summary


def fetch_public_ids(base_url: str, kind: str) -> set[int]:
    if kind == "category_translation":
        payload = request_json(f"{base_url.rstrip('/')}/v1/categories")
        if not isinstance(payload, list):
            raise RuntimeError("GET /v1/categories returned non-list payload")
        return {int(item["id"]) for item in payload if as_positive_int(item.get("id")) is not None}

    if kind == "author_translation":
        return fetch_paginated_ids(base_url, "/v1/authors", "authors")

    if kind == "book_metadata_translation":
        return fetch_paginated_ids(base_url, "/v1/books", "books")

    return set()


def fetch_paginated_ids(base_url: str, path: str, key: str) -> set[int]:
    ids: set[int] = set()
    offset = 0
    limit = 200
    while True:
        query = urllib.parse.urlencode({"limit": limit, "offset": offset})
        payload = request_json(f"{base_url.rstrip('/')}{path}?{query}")
        rows = payload.get(key) if isinstance(payload, dict) else None
        if not isinstance(rows, list):
            raise RuntimeError(f"GET {path} returned invalid payload")
        for row in rows:
            if isinstance(row, dict) and as_positive_int(row.get("id")) is not None:
                ids.add(int(row["id"]))
        total = int(payload.get("total") or 0)
        offset += len(rows)
        if not rows or offset >= total:
            break
    return ids


def request_json(url: str) -> Any:
    req = urllib.request.Request(url, headers={"Accept": "application/json"}, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as err:
        detail = err.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"GET {url} failed: HTTP {err.code}: {detail}") from err
    except (urllib.error.URLError, TimeoutError) as err:
        raise RuntimeError(f"GET {url} failed: {err}") from err


def infer_single_lang(rows: list[CatalogRow], issues: list[Issue]) -> str | None:
    langs = {row.lang for row in rows if row.lang}
    if len(langs) == 1:
        return next(iter(langs))
    if len(langs) > 1:
        issues.append(Issue(FAIL, "LANG_AMBIGUOUS", "multiple catalog languages found; pass --lang"))
    return None


def summarize(
    rows: list[CatalogRow],
    catalog_rows: list[CatalogRow],
    ignored_rows: list[CatalogRow],
    issues: list[Issue],
) -> dict[str, int]:
    row_state: dict[int, str] = {row.line: PASS for row in catalog_rows}
    for issue in issues:
        if issue.line is None:
            continue
        if issue.line not in row_state:
            row_state[issue.line] = PASS
        if issue.severity == FAIL:
            row_state[issue.line] = FAIL
        elif issue.severity == WARN and row_state[issue.line] != FAIL:
            row_state[issue.line] = WARN

    return {
        "total_rows": len(rows),
        "catalog_rows": len(catalog_rows),
        "ignored_rows": len(ignored_rows),
        "book_rows": sum(1 for row in catalog_rows if row.kind == "book_metadata_translation"),
        "author_rows": sum(1 for row in catalog_rows if row.kind == "author_translation"),
        "category_rows": sum(1 for row in catalog_rows if row.kind == "category_translation"),
        "generated_rows": sum(1 for row in catalog_rows if row.translation_status == "generated"),
        "reviewed_rows": sum(1 for row in catalog_rows if row.translation_status == "reviewed"),
        "pass_rows": sum(1 for state in row_state.values() if state == PASS),
        "warn_rows": sum(1 for state in row_state.values() if state == WARN),
        "fail_rows": sum(1 for state in row_state.values() if state == FAIL),
        "warnings": sum(1 for issue in issues if issue.severity == WARN),
        "failures": sum(1 for issue in issues if issue.severity == FAIL),
    }


def print_report(report: dict[str, Any]) -> None:
    summary = report["summary"]
    status = FAIL if summary["failures"] > 0 else WARN if summary["warnings"] > 0 else PASS
    print(f"{status} {report['file']}")
    print(
        "rows={total_rows} catalog={catalog_rows} ignored={ignored_rows} "
        "books={book_rows} authors={author_rows} categories={category_rows} "
        "generated={generated_rows} reviewed={reviewed_rows} "
        "pass_rows={pass_rows} warn_rows={warn_rows} fail_rows={fail_rows} "
        "warnings={warnings} failures={failures}".format(**summary)
    )

    public_ids = report.get("public_ids")
    if public_ids:
        print(
            "public_ids_checked={checked} public_ids_missing={missing}".format(
                checked=public_ids["checked"],
                missing=public_ids["missing"],
            )
        )

    for issue in report["issues"]:
        location = []
        if "line" in issue:
            location.append(f"line={issue['line']}")
        if "kind" in issue:
            location.append(f"kind={issue['kind']}")
        if "object_id" in issue:
            location.append(f"id={issue['object_id']}")
        if "lang" in issue:
            location.append(f"lang={issue['lang']}")
        suffix = f" {' '.join(location)}" if location else ""
        print(f"{issue['severity']} {issue['code']}{suffix}: {issue['message']}")


def row_issue(severity: str, code: str, message: str, row: CatalogRow) -> Issue:
    return Issue(
        severity=severity,
        code=code,
        message=message,
        line=row.line,
        kind=row.kind or None,
        object_id=row.object_id,
        lang=row.lang or None,
    )


def clean_text(value: Any) -> str:
    return str(value or "").strip()


def arabic_ratio(value: str) -> float:
    letters = [char for char in value if char.isalpha()]
    if not letters:
        return 0.0
    arabic = sum(1 for char in letters if ARABIC_RE.match(char))
    return arabic / len(letters)


def is_iso_datetime(value: str) -> bool:
    try:
        datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return False
    return True


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
