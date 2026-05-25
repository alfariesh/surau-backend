#!/usr/bin/env python3
from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

import qa_catalog_assets as qa  # noqa: E402


def make_args(path: Path, **kwargs: object):
    defaults = {
        "file": str(path),
        "lang": "",
        "base_url": "http://127.0.0.1:1",
        "check_public_ids": False,
        "report": "",
        "strict": False,
    }
    defaults.update(kwargs)
    return type("Args", (), defaults)()


def write_jsonl(path: Path, rows: list[object]) -> None:
    with path.open("w", encoding="utf-8") as file:
        for row in rows:
            if isinstance(row, str):
                file.write(row + "\n")
            else:
                file.write(json.dumps(row, ensure_ascii=False) + "\n")


def book_row(**overrides: object) -> dict[str, object]:
    row: dict[str, object] = {
        "kind": "book_metadata_translation",
        "book_id": 21818,
        "lang": "id",
        "display_title": "Pasal-Pasal tentang Puasa, Tarawih, dan Zakat",
        "bibliography": "Terjemahan metadata bibliografi yang ringkas.",
        "hint": "Ringkasan katalog.",
        "description": "Deskripsi katalog untuk halaman detail kitab.",
        "translation_status": "generated",
        "metadata": {"unit": "catalog_book"},
    }
    row.update(overrides)
    return row


def author_row(**overrides: object) -> dict[str, object]:
    row: dict[str, object] = {
        "kind": "author_translation",
        "author_id": 123,
        "lang": "id",
        "name": "Ibnu Utsaimin",
        "biography": "Biografi singkat penulis.",
        "translation_status": "generated",
        "metadata": {"unit": "catalog_author"},
    }
    row.update(overrides)
    return row


class CatalogQATest(unittest.TestCase):
    def run_qa_for_rows(self, rows: list[object], **kwargs: object) -> dict[str, object]:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "catalog.jsonl"
            write_jsonl(path, rows)
            return qa.run_qa(make_args(path, **kwargs))

    def issue_codes(self, report: dict[str, object]) -> set[str]:
        return {issue["code"] for issue in report["issues"]}  # type: ignore[index]

    def test_valid_catalog_passes(self) -> None:
        report = self.run_qa_for_rows([book_row(), author_row()])

        self.assertEqual(report["summary"]["failures"], 0)  # type: ignore[index]
        self.assertEqual(report["summary"]["warnings"], 0)  # type: ignore[index]

    def test_invalid_json_fails(self) -> None:
        report = self.run_qa_for_rows(["{not-json"])

        self.assertIn("INVALID_JSON", self.issue_codes(report))

    def test_duplicate_catalog_row_fails(self) -> None:
        report = self.run_qa_for_rows([book_row(), book_row()])

        self.assertIn("DUPLICATE_CATALOG_TRANSLATION", self.issue_codes(report))

    def test_reviewed_without_reviewer_fails(self) -> None:
        report = self.run_qa_for_rows([book_row(translation_status="reviewed")])

        self.assertIn("MISSING_REVIEWED_BY", self.issue_codes(report))

    def test_reviewed_with_reviewer_passes(self) -> None:
        report = self.run_qa_for_rows(
            [book_row(translation_status="reviewed", translation_reviewed_by="Editor A")]
        )

        self.assertEqual(report["summary"]["failures"], 0)  # type: ignore[index]

    def test_dry_run_fails(self) -> None:
        report = self.run_qa_for_rows([book_row(display_title="[DRY RUN] فصول")])

        self.assertIn("DRY_RUN_PLACEHOLDER", self.issue_codes(report))

    def test_lang_mismatch_fails(self) -> None:
        report = self.run_qa_for_rows([book_row(lang="en")], lang="id")

        self.assertIn("LANG_MISMATCH", self.issue_codes(report))

    def test_arabic_heavy_warns_not_fails(self) -> None:
        report = self.run_qa_for_rows([book_row(display_title="كتاب كتاب كتاب كتاب")])

        self.assertIn("ARABIC_HEAVY_TEXT", self.issue_codes(report))
        self.assertEqual(report["summary"]["failures"], 0)  # type: ignore[index]

    def test_no_catalog_rows_fails(self) -> None:
        report = self.run_qa_for_rows(
            [{"kind": "audio", "book_id": 1, "heading_id": 1, "lang": "id", "url": "https://example.test/a.mp3"}]
        )

        self.assertIn("NO_CATALOG_ROWS", self.issue_codes(report))


if __name__ == "__main__":
    unittest.main()
