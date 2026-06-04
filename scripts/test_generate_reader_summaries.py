#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
import tempfile
import types
import unittest
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

import generate_reader_summaries as gs  # noqa: E402


class GenerateReaderSummariesTest(unittest.TestCase):
    def test_parent_source_prefers_child_summaries(self) -> None:
        args = argparse.Namespace(base_url="http://127.0.0.1:8080", book_id=1, source_lang="ar")
        parent = {"heading_id": 1, "title": "Root"}
        child = {"heading_id": 2, "parent_id": 1, "title": "Child"}
        sibling = {"heading_id": 3, "parent_id": 1, "title": "Sibling"}

        source_text, source_kind = gs.source_text_for_node(
            args,
            parent,
            {1: [child, sibling]},
            {2: "Child summary", 3: "Sibling summary"},
        )

        self.assertEqual(source_kind, "child_summaries")
        self.assertIn("Child summary", source_text)
        self.assertIn("Sibling summary", source_text)

    def test_parent_source_falls_back_when_child_summary_is_missing(self) -> None:
        args = argparse.Namespace(base_url="http://127.0.0.1:8080", book_id=1, source_lang="ar")
        parent = {"heading_id": 1, "title": "Root"}
        child = {"heading_id": 2, "parent_id": 1, "title": "Child"}
        sibling = {"heading_id": 3, "parent_id": 1, "title": "Sibling"}
        original_fetch = gs.fetch_toc_section

        def fake_fetch_toc_section(base_url: str, book_id: int, heading_id: int, lang: str) -> dict[str, object]:
            self.assertEqual((base_url, book_id, heading_id, lang), ("http://127.0.0.1:8080", 1, 1, "ar"))
            return {"original_text": "Original section text"}

        gs.fetch_toc_section = fake_fetch_toc_section
        try:
            source_text, source_kind = gs.source_text_for_node(
                args,
                parent,
                {1: [child, sibling]},
                {2: "Child summary"},
            )
        finally:
            gs.fetch_toc_section = original_fetch

        self.assertEqual(source_kind, "section_text")
        self.assertEqual(source_text, "Original section text")

    def test_resume_reads_completed_summaries_for_cache(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "summaries.jsonl"
            rows = [
                {"kind": "heading_summary", "book_id": 1, "heading_id": 2, "lang": "ar", "summary": "Child summary"},
                {"kind": "heading_summary", "book_id": 1, "heading_id": 3, "lang": "id", "summary": "Ringkasan"},
                {"kind": "heading_summary", "book_id": 2, "heading_id": 4, "lang": "ar", "summary": "Other book"},
            ]
            path.write_text("\n".join(json.dumps(row, ensure_ascii=False) for row in rows) + "\n", encoding="utf-8")

            completed = gs.read_completed_summaries(path, 1, "ar")

        self.assertEqual(completed, {2: "Child summary"})

    def test_parse_args_defaults_to_untruncated_source(self) -> None:
        original_argv = sys.argv
        sys.argv = [
            "generate_reader_summaries.py",
            "--book-id",
            "1",
            "--heading-id",
            "2",
            "--out",
            "/tmp/summaries.jsonl",
        ]
        try:
            args = gs.parse_args()
        finally:
            sys.argv = original_argv

        self.assertEqual(args.max_source_chars, 0)

    def test_non_arabic_summary_language_is_rejected(self) -> None:
        with self.assertRaises(SystemExit) as ctx:
            gs.validate_summary_language(argparse.Namespace(summary_lang="id"))

        self.assertIn("--summary-only", str(ctx.exception))

    def test_write_eval_report_uses_qa_helper(self) -> None:
        calls: list[object] = []
        original_module = sys.modules.get("qa_reader_assets")

        def fake_run_qa(args: object) -> dict[str, object]:
            calls.append(args)
            payload = Path(args.file).read_text(encoding="utf-8")  # type: ignore[attr-defined]
            self.assertIn('"kind":"heading_summary"', payload)
            self.assertEqual(args.kind, "heading_summary")  # type: ignore[attr-defined]
            return {
                "summary": {"failures": 0, "warnings": 0},
                "issues": [],
            }

        sys.modules["qa_reader_assets"] = types.SimpleNamespace(run_qa=fake_run_qa)
        try:
            with tempfile.TemporaryDirectory() as tmp:
                report_path = Path(tmp) / "report.json"
                gs.write_eval_report(
                    report_path,
                    argparse.Namespace(base_url="http://127.0.0.1:8080", book_id=1, summary_lang="ar"),
                    [
                        {
                            "kind": "heading_summary",
                            "book_id": 1,
                            "heading_id": 2,
                            "lang": "ar",
                            "summary": "ملخص عربي صالح وطويل بما يكفي للعرض في واجهة القارئ.",
                        }
                    ],
                    [],
                )
                report = json.loads(report_path.read_text(encoding="utf-8"))
        finally:
            if original_module is None:
                sys.modules.pop("qa_reader_assets", None)
            else:
                sys.modules["qa_reader_assets"] = original_module

        self.assertEqual(len(calls), 1)
        self.assertEqual(report["qa_status"], "PASS")
        self.assertEqual(report["generated_count"], 1)

    def test_generate_summary_parses_json_response(self) -> None:
        calls: list[dict[str, object]] = []
        original_request_json = gs.request_json

        def fake_request_json(method: str, url: str, **kwargs: object) -> dict[str, object]:
            calls.append({"method": method, "url": url, "kwargs": kwargs})
            return {"choices": [{"message": {"content": json.dumps({"summary": "ملخص موجز"})}}]}

        gs.request_json = fake_request_json
        try:
            summary = gs.generate_summary(
                api_key="test-key",
                llm_base_url="https://example.test/v1",
                model="glm-5.1",
                summary_lang="ar",
                source_title="باب",
                source_kind="section_text",
                source_text="نص",
                max_tokens=200,
                timeout_seconds=1,
                retries=0,
            )
        finally:
            gs.request_json = original_request_json

        self.assertEqual(summary, "ملخص موجز")
        payload = calls[0]["kwargs"]["payload"]  # type: ignore[index]
        self.assertEqual(payload["model"], "glm-5.1")
        self.assertIn("response_format", payload)


if __name__ == "__main__":
    unittest.main()
