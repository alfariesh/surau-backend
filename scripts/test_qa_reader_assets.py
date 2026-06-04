#!/usr/bin/env python3
from __future__ import annotations

import json
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

import qa_reader_assets as qa  # noqa: E402

LONG_CONTENT = (
    "Ini adalah terjemahan yang cukup panjang untuk melewati batas minimum "
    "dan terlihat seperti paragraf buku yang wajar. Ia memuat kalimat yang "
    "natural, tertata, dan tidak sekadar placeholder. Paragraf ini menjaga "
    "aliran bacaan agar serupa dengan naskah buku yang sudah disunting.\n\n"
    "Paragraf kedua menjaga format Markdown tetap normal dan memberi ruang "
    "cukup agar QA tidak menganggap konten sebagai hasil yang terlalu pendek."
)


def make_args(path: Path, **kwargs: object):
    defaults = {
        "file": str(path),
        "base_url": "http://127.0.0.1:1",
        "book_id": 0,
        "lang": "",
        "all_toc": False,
        "kind": "auto",
        "report": "",
        "strict": False,
        "profile_map": str(SCRIPT_DIR / "translation_profiles.json"),
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


def translation(
    *,
    book_id: int = 10,
    heading_id: int = 1,
    lang: str = "id",
    title: str = "Judul",
    content: str | None = None,
    metadata: dict[str, object] | None = None,
    translation_status: str = "generated",
    reviewed_by: str = "",
) -> dict[str, object]:
    row: dict[str, object] = {
        "kind": "translation",
        "book_id": book_id,
        "heading_id": heading_id,
        "lang": lang,
        "title": title,
        "content": content or LONG_CONTENT,
        "translation_status": translation_status,
        "metadata": metadata
        if metadata is not None
        else {
            "truncated_source": False,
            "style_version": "reader-profile-v1",
            "translation_profile": "general",
        },
    }
    if reviewed_by:
        row["translation_reviewed_by"] = reviewed_by
    return row


def heading_summary(
    *,
    book_id: int = 10,
    heading_id: int = 1,
    lang: str = "ar",
    summary: str = "يتناول هذا القسم معنى الحمد لله وبيان الثناء على الله تعالى بأنه مالك الحمد ومستحقه.",
    metadata: dict[str, object] | None = None,
    summary_status: str = "generated",
    reviewed_by: str = "",
) -> dict[str, object]:
    row: dict[str, object] = {
        "kind": "heading_summary",
        "book_id": book_id,
        "heading_id": heading_id,
        "lang": lang,
        "summary": summary,
        "summary_status": summary_status,
        "metadata": metadata
        if metadata is not None
        else {
            "unit": "toc_summary",
            "style_version": "reader-summary-v1",
            "source_lang": "ar",
            "summary_lang": lang,
            "truncated_source": False,
        },
    }
    if reviewed_by:
        row["summary_reviewed_by"] = reviewed_by
    return row


class QATest(unittest.TestCase):
    def run_qa_for_rows(self, rows: list[object], **kwargs: object) -> dict[str, object]:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "assets.jsonl"
            write_jsonl(path, rows)
            return qa.run_qa(make_args(path, **kwargs))

    def issue_codes(self, report: dict[str, object]) -> set[str]:
        return {issue["code"] for issue in report["issues"]}  # type: ignore[index]

    def test_valid_translation_passes(self) -> None:
        report = self.run_qa_for_rows([translation()])

        self.assertEqual(report["summary"]["failures"], 0)  # type: ignore[index]
        self.assertEqual(report["summary"]["warnings"], 0)  # type: ignore[index]

    def test_valid_summary_passes(self) -> None:
        report = self.run_qa_for_rows([heading_summary()])

        self.assertEqual(report["kind"], "heading_summary")
        self.assertEqual(report["summary"]["summaries"], 1)  # type: ignore[index]
        self.assertEqual(report["summary"]["failures"], 0)  # type: ignore[index]
        self.assertEqual(report["summary"]["warnings"], 0)  # type: ignore[index]

    def test_invalid_json_fails(self) -> None:
        report = self.run_qa_for_rows(["{not-json"])

        self.assertIn("INVALID_JSON", self.issue_codes(report))
        self.assertGreater(report["summary"]["failures"], 0)  # type: ignore[index]

    def test_duplicate_translation_fails(self) -> None:
        report = self.run_qa_for_rows([translation(), translation()])

        self.assertIn("DUPLICATE_TRANSLATION", self.issue_codes(report))

    def test_duplicate_summary_fails(self) -> None:
        report = self.run_qa_for_rows([heading_summary(), heading_summary()])

        self.assertIn("DUPLICATE_SUMMARY", self.issue_codes(report))

    def test_truncated_source_fails(self) -> None:
        report = self.run_qa_for_rows(
            [
                translation(
                    metadata={
                        "truncated_source": True,
                        "style_version": "reader-profile-v1",
                        "translation_profile": "general",
                    }
                )
            ]
        )

        self.assertIn("TRUNCATED_SOURCE", self.issue_codes(report))

    def test_truncated_summary_source_fails(self) -> None:
        report = self.run_qa_for_rows(
            [
                heading_summary(
                    metadata={
                        "unit": "toc_summary",
                        "style_version": "reader-summary-v1",
                        "truncated_source": True,
                    }
                )
            ]
        )

        self.assertIn("TRUNCATED_SUMMARY_SOURCE", self.issue_codes(report))

    def test_missing_translation_profile_warns(self) -> None:
        report = self.run_qa_for_rows([translation(metadata={"truncated_source": False})])

        self.assertIn("MISSING_TRANSLATION_PROFILE", self.issue_codes(report))
        self.assertEqual(report["summary"]["failures"], 0)  # type: ignore[index]

    def test_invalid_translation_profile_fails(self) -> None:
        report = self.run_qa_for_rows(
            [
                translation(
                    metadata={
                        "truncated_source": False,
                        "style_version": "reader-profile-v1",
                        "translation_profile": "unknown",
                    }
                )
            ]
        )

        self.assertIn("INVALID_TRANSLATION_PROFILE", self.issue_codes(report))

    def test_style_version_mismatch_warns(self) -> None:
        report = self.run_qa_for_rows(
            [
                translation(
                    metadata={
                        "truncated_source": False,
                        "style_version": "old",
                        "translation_profile": "general",
                    }
                )
            ]
        )

        self.assertIn("STYLE_VERSION_MISMATCH", self.issue_codes(report))
        self.assertEqual(report["summary"]["failures"], 0)  # type: ignore[index]

    def test_technical_profile_without_italics_warns(self) -> None:
        content = "\n\n".join([LONG_CONTENT, LONG_CONTENT, LONG_CONTENT, LONG_CONTENT, LONG_CONTENT])
        report = self.run_qa_for_rows(
            [
                translation(
                    content=content,
                    metadata={
                        "truncated_source": False,
                        "style_version": "reader-profile-v1",
                        "translation_profile": "fiqh",
                    },
                )
            ]
        )

        self.assertIn("MISSING_TECHNICAL_ITALICS", self.issue_codes(report))
        self.assertEqual(report["summary"]["failures"], 0)  # type: ignore[index]

    def test_dry_run_fails(self) -> None:
        report = self.run_qa_for_rows([translation(title="[DRY RUN] Judul")])

        self.assertIn("DRY_RUN_PLACEHOLDER", self.issue_codes(report))

    def test_dry_run_summary_fails(self) -> None:
        report = self.run_qa_for_rows([heading_summary(summary="[DRY RUN] ringkasan placeholder yang cukup panjang")])

        self.assertIn("DRY_RUN_SUMMARY_PLACEHOLDER", self.issue_codes(report))

    def test_reviewed_without_reviewer_fails(self) -> None:
        report = self.run_qa_for_rows([translation(translation_status="reviewed")])

        self.assertIn("MISSING_REVIEWED_BY", self.issue_codes(report))

    def test_reviewed_summary_without_reviewer_fails(self) -> None:
        report = self.run_qa_for_rows([heading_summary(summary_status="reviewed")])

        self.assertIn("MISSING_SUMMARY_REVIEWED_BY", self.issue_codes(report))

    def test_raw_bracket_question_fails(self) -> None:
        content = (
            "[Mereka berkata: Apa pendapat Anda tentang masalah ini yang "
            "disebutkan oleh penulis?]\n\nJawaban yang cukup panjang."
        )
        report = self.run_qa_for_rows([translation(content=content)])

        self.assertIn("RAW_BRACKET_QUESTION", self.issue_codes(report))

    def test_footnote_warns_not_fails(self) -> None:
        content = (
            "Paragraf terjemahan yang cukup panjang dengan catatan [1] dan "
            "catatan lain [2] tanpa masalah fatal. Bagian ini tetap memuat "
            "uraian yang cukup panjang agar tidak dianggap sebagai konten "
            "pendek oleh QA gate. Catatan kaki di sini sengaja dibuat sebagai "
            "peringatan, bukan sebagai kegagalan.\n\nParagraf kedua menjaga "
            "format Markdown tetap wajar dan siap dibaca editor."
        )
        report = self.run_qa_for_rows([translation(content=content)])

        self.assertIn("MANY_FOOTNOTES", self.issue_codes(report))
        self.assertEqual(report["summary"]["failures"], 0)  # type: ignore[index]
        self.assertGreater(report["summary"]["warnings"], 0)  # type: ignore[index]

    def test_all_toc_missing_heading_fails(self) -> None:
        with toc_server([1, 2]) as base_url:
            report = self.run_qa_for_rows(
                [translation(book_id=10, heading_id=1, lang="id")],
                base_url=base_url,
                book_id=10,
                lang="id",
                all_toc=True,
            )

        self.assertIn("MISSING_TOC_TRANSLATION", self.issue_codes(report))

    def test_all_toc_missing_summary_fails(self) -> None:
        with toc_server([1, 2]) as base_url:
            report = self.run_qa_for_rows(
                [heading_summary(book_id=10, heading_id=1, lang="ar")],
                base_url=base_url,
                book_id=10,
                lang="ar",
                all_toc=True,
            )

        self.assertIn("MISSING_TOC_SUMMARY", self.issue_codes(report))


class toc_server:
    def __init__(self, heading_ids: list[int]) -> None:
        self.heading_ids = heading_ids
        self.server: ThreadingHTTPServer | None = None
        self.thread: threading.Thread | None = None

    def __enter__(self) -> str:
        heading_ids = self.heading_ids

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802
                payload = [
                    {
                        "book_id": 10,
                        "heading_id": heading_id,
                        "title": f"Heading {heading_id}",
                        "children": [],
                    }
                    for heading_id in heading_ids
                ]
                body = json.dumps(payload).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, format: str, *args: object) -> None:
                return

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        host, port = self.server.server_address
        return f"http://{host}:{port}"

    def __exit__(self, exc_type: object, exc: object, tb: object) -> None:
        if self.server is not None:
            self.server.shutdown()
            self.server.server_close()
        if self.thread is not None:
            self.thread.join(timeout=5)


if __name__ == "__main__":
    unittest.main()
