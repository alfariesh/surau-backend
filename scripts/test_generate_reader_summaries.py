#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
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

        source_text, source_kind = gs.source_text_for_node(
            args,
            parent,
            {1: [child]},
            {2: "Child summary"},
        )

        self.assertEqual(source_kind, "child_summaries")
        self.assertIn("Child summary", source_text)

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
