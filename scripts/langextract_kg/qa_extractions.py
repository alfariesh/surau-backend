#!/usr/bin/env python3
"""QA grounded LangExtract knowledge extraction rows."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import json
from pathlib import Path
import sys
from typing import Any

if __package__ in (None, ""):
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    from langextract_kg import db as kg_db  # type: ignore
    from langextract_kg.arabic_normalize import (  # type: ignore
        is_ambiguous_person_name,
        is_generic_extraction,
        is_person_reference,
        is_surah_reference,
        is_theonym,
    )
else:
    from . import db as kg_db
    from .arabic_normalize import (
        is_ambiguous_person_name,
        is_generic_extraction,
        is_person_reference,
        is_surah_reference,
        is_theonym,
    )


FAIL = "FAIL"
WARN = "WARN"
VALID_REVIEW_STATUSES = {"pending", "approved", "rejected", "ambiguous", "needs_review"}


@dataclass(frozen=True)
class Issue:
    severity: str
    code: str
    message: str
    line: int | None = None
    book_id: int | None = None
    page_id: int | None = None
    extraction_text: str | None = None

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
        if self.page_id is not None:
            payload["page_id"] = self.page_id
        if self.extraction_text is not None:
            payload["extraction_text"] = self.extraction_text
        return payload


def main() -> int:
    args = parse_args()
    kg_db.load_env_file(Path(args.env_file).expanduser())
    rows, read_issues = load_rows(args)
    issues = [*read_issues, *validate_rows(rows)]
    if args.strict:
        issues = [
            Issue(
                FAIL if issue.severity == WARN else issue.severity,
                issue.code,
                issue.message,
                issue.line,
                issue.book_id,
                issue.page_id,
                issue.extraction_text,
            )
            for issue in issues
        ]
    report = build_report(rows, issues)
    print_report(report)
    if args.report:
        output = Path(args.report).expanduser()
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(f"report={output}", file=sys.stderr)
    return 1 if report["summary"]["failures"] else 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--file", default="", help="Knowledge extraction JSONL file")
    parser.add_argument("--run-id", default="", help="Validate rows already stored for this extraction run")
    parser.add_argument("--pg-url", default="", help="PostgreSQL URL; defaults to PG_URL")
    parser.add_argument("--env-file", default=str(kg_db.DEFAULT_ENV_FILE), help="Local dotenv file")
    parser.add_argument("--strict", action="store_true", help="Treat warnings as failures")
    parser.add_argument("--report", default="", help="Write JSON report")
    return parser.parse_args()


def load_rows(args: argparse.Namespace) -> tuple[list[dict[str, Any]], list[Issue]]:
    if args.run_id:
        pg_url = args.pg_url or kg_db.postgres_url_from_env()
        client = kg_db.DBClient.connect(pg_url)
        try:
            return client.load_mentions_for_run(args.run_id), []
        finally:
            client.close()
    if not args.file:
        return [], [Issue(FAIL, "INPUT_REQUIRED", "pass --file or --run-id")]
    return read_jsonl(Path(args.file).expanduser())


def read_jsonl(path: Path) -> tuple[list[dict[str, Any]], list[Issue]]:
    rows: list[dict[str, Any]] = []
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
            row = json.loads(line)
        except json.JSONDecodeError as err:
            issues.append(Issue(FAIL, "INVALID_JSON", str(err), line=line_number))
            continue
        if not isinstance(row, dict):
            issues.append(Issue(FAIL, "JSON_OBJECT_REQUIRED", "row must be a JSON object", line=line_number))
            continue
        row["_line"] = line_number
        rows.append(row)
    return rows, issues


def validate_rows(rows: list[dict[str, Any]]) -> list[Issue]:
    issues: list[Issue] = []
    seen: set[tuple[Any, ...]] = set()
    for row in rows:
        issues.extend(validate_row(row))
        key = (
            row.get("run_id"),
            row.get("book_id"),
            row.get("page_id"),
            row.get("extraction_class"),
            row.get("char_start"),
            row.get("char_end"),
            row.get("extraction_text"),
        )
        if all(value is not None and value != "" for value in key):
            if key in seen:
                issues.append(row_issue(FAIL, "DUPLICATE_SPAN", "duplicate mention span", row))
            else:
                seen.add(key)
    return issues


def validate_row(row: dict[str, Any]) -> list[Issue]:
    issues: list[Issue] = []
    if row.get("kind") not in {None, "", "knowledge_mention"}:
        issues.append(row_issue(FAIL, "INVALID_KIND", "kind must be knowledge_mention", row))
    for field in [
        "run_id",
        "book_id",
        "page_id",
        "document_id",
        "extraction_class",
        "extraction_text",
        "exact_quote",
        "char_start",
        "char_end",
        "alignment_status",
        "normalized_text",
        "review_status",
    ]:
        if row.get(field) in {None, ""}:
            issues.append(row_issue(FAIL, "MISSING_FIELD", f"{field} is required", row))

    start = as_int(row.get("char_start"))
    end = as_int(row.get("char_end"))
    if start is None or end is None or start < 0 or end <= start:
        issues.append(row_issue(FAIL, "BAD_CHAR_RANGE", "char_start/char_end must form a positive range", row))

    extraction_text = str(row.get("extraction_text") or "")
    exact_quote = str(row.get("exact_quote") or "")
    if extraction_text and exact_quote and extraction_text != exact_quote:
        issues.append(row_issue(FAIL, "NON_EXACT_QUOTE", "exact_quote must equal extraction_text", row))
    if is_generic_extraction(extraction_text):
        issues.append(row_issue(FAIL, "GENERIC_EXTRACTION", "generic pronouns/titles/verbs are not valid mentions", row))

    review_status = str(row.get("review_status") or "")
    if review_status and review_status not in VALID_REVIEW_STATUSES:
        issues.append(row_issue(FAIL, "INVALID_REVIEW_STATUS", "invalid review_status", row))
    if row.get("extraction_class") == "person" and is_ambiguous_person_name(extraction_text):
        if review_status != "ambiguous":
            issues.append(row_issue(FAIL, "AMBIGUOUS_PERSON_NOT_FLAGGED", "common person name must be ambiguous", row))
    if row.get("extraction_class") == "person" and is_theonym(extraction_text):
        issues.append(row_issue(FAIL, "THEONYM_AS_PERSON", "divine names must not be stored as person", row))
    if row.get("extraction_class") == "person_reference" and is_theonym(extraction_text):
        issues.append(
            row_issue(FAIL, "THEONYM_AS_PERSON_REFERENCE", "standalone divine names must use theonym", row)
        )
    if row.get("extraction_class") == "person" and is_person_reference(extraction_text):
        issues.append(row_issue(FAIL, "PERSON_REFERENCE_AS_PERSON", "title-like references must use person_reference", row))
    if row.get("extraction_class") == "person_reference" and review_status != "needs_review":
        issues.append(row_issue(FAIL, "PERSON_REFERENCE_AUTO_REVIEW", "person_reference must stay needs_review", row))
    if row.get("extraction_class") == "book_title":
        issues.append(row_issue(FAIL, "LEGACY_BOOK_TITLE_CLASS", "use work_title for authored works", row))
    if row.get("extraction_class") in {"book_title", "work_title"} and is_surah_reference(extraction_text):
        issues.append(row_issue(FAIL, "SURAH_AS_WORK_TITLE", "Quran surah references must use quran_reference", row))
    if row.get("alignment_status") == "match_exact_substring_fallback" and review_status == "pending":
        issues.append(row_issue(WARN, "FALLBACK_ALIGNMENT_PENDING", "substring fallback should be reviewed", row))

    source_text = row.get("source_text")
    if isinstance(source_text, str) and start is not None and end is not None and 0 <= start < end <= len(source_text):
        if source_text[start:end] != exact_quote:
            issues.append(row_issue(FAIL, "SOURCE_SLICE_MISMATCH", "char range does not match exact_quote", row))
    return issues


def row_issue(severity: str, code: str, message: str, row: dict[str, Any]) -> Issue:
    return Issue(
        severity,
        code,
        message,
        line=as_int(row.get("_line")),
        book_id=as_int(row.get("book_id")),
        page_id=as_int(row.get("page_id")),
        extraction_text=str(row.get("extraction_text") or "") or None,
    )


def as_int(value: Any) -> int | None:
    if isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, str) and value.strip().lstrip("-").isdigit():
        return int(value)
    return None


def build_report(rows: list[dict[str, Any]], issues: list[Issue]) -> dict[str, Any]:
    failures = sum(1 for issue in issues if issue.severity == FAIL)
    warnings = sum(1 for issue in issues if issue.severity == WARN)
    return {
        "summary": {
            "rows": len(rows),
            "failures": failures,
            "warnings": warnings,
            "status": "FAIL" if failures else "PASS",
        },
        "issues": [issue.to_dict() for issue in issues],
    }


def print_report(report: dict[str, Any]) -> None:
    summary = report["summary"]
    print(
        "status={status} rows={rows} failures={failures} warnings={warnings}".format(
            **summary
        )
    )
    for issue in report["issues"][:50]:
        location = []
        if "line" in issue:
            location.append(f"line={issue['line']}")
        if "book_id" in issue:
            location.append(f"book={issue['book_id']}")
        if "page_id" in issue:
            location.append(f"page={issue['page_id']}")
        prefix = " ".join(location)
        if prefix:
            prefix += " "
        print(f"{issue['severity']} {issue['code']} {prefix}{issue['message']}")
    if len(report["issues"]) > 50:
        print(f"... {len(report['issues']) - 50} more issues")


if __name__ == "__main__":
    raise SystemExit(main())
