#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

import translate_catalog_assets as tc  # noqa: E402


class TranslateCatalogAssetsTest(unittest.TestCase):
    def test_all_catalog_rows_share_invocation_generation(self) -> None:
        generation = tc.new_generation_identity(
            "test-model",
            tc.CATALOG_TRANSLATION_PROMPT_VERSION,
        )
        args = argparse.Namespace(
            dry_run=True,
            model="test-model",
            target_lang="id",
            generation=generation,
        )
        items = [
            {"type": "book", "data": {"id": 1, "name": "كتاب"}},
            {"type": "author", "data": {"id": 2, "name": "مؤلف"}},
            {"type": "category", "data": {"id": 3, "name": "قسم"}},
        ]

        rows = [tc.translate_item(args, "", item, index, len(items)) for index, item in enumerate(items, 1)]

        self.assertEqual({row["provenance_class"] for row in rows}, {"machine"})
        self.assertEqual({row["generation"]["run_id"] for row in rows}, {generation["run_id"]})
        self.assertEqual(
            {row["generation"]["prompt_version"] for row in rows},
            {"catalog-translation-v1"},
        )

    def test_resume_scan_does_not_change_existing_rows(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "catalog.jsonl"
            row = {
                "kind": "book_metadata_translation",
                "book_id": 1,
                "lang": "id",
                "display_title": "Judul lama",
                "provenance_class": "machine",
                "generation": {
                    "run_id": "66666666-6666-4666-8666-666666666666",
                    "model_id": "old-model",
                    "prompt_version": "catalog-translation-v1",
                },
            }
            path.write_text(json.dumps(row) + "\n", encoding="utf-8")
            before = path.read_bytes()

            completed = tc.read_completed_keys(path)

            self.assertEqual(completed, {"book:1"})
            self.assertEqual(path.read_bytes(), before)

    def test_resume_appends_new_rows_without_rewriting_old_identity(self) -> None:
        original_parse_args = tc.parse_args
        original_collect_items = tc.collect_items
        original_load_env = tc.load_env_file
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "catalog.jsonl"
            old_generation = {
                "run_id": "77777777-7777-4777-8777-777777777777",
                "model_id": "old-model",
                "prompt_version": "catalog-translation-v1",
            }
            old_row = {
                "kind": "book_metadata_translation",
                "book_id": 1,
                "lang": "id",
                "display_title": "Judul lama",
                "provenance_class": "machine",
                "generation": old_generation,
            }
            old_line = json.dumps(old_row, ensure_ascii=False, separators=(",", ":"))
            path.write_text(old_line + "\n", encoding="utf-8")

            args = argparse.Namespace(
                env_file="/does/not/exist",
                model="new-model",
                deepseek_base_url="https://example.test",
                api_key_env="UNUSED_API_KEY",
                dry_run=True,
                limit=0,
                out=str(path),
                resume=True,
                concurrency=1,
                sleep_seconds=0,
                fail_fast=True,
                target_lang="id",
            )
            items = [
                {"type": "book", "data": {"id": 1, "name": "قديم"}},
                {"type": "book", "data": {"id": 2, "name": "جديد"}},
            ]
            tc.parse_args = lambda: args
            tc.collect_items = lambda unused_args: items
            tc.load_env_file = lambda unused_path: None
            try:
                exit_code = tc.main()
            finally:
                tc.parse_args = original_parse_args
                tc.collect_items = original_collect_items
                tc.load_env_file = original_load_env

            lines = path.read_text(encoding="utf-8").splitlines()

        self.assertEqual(exit_code, 0)
        self.assertEqual(lines[0], old_line)
        self.assertEqual(len(lines), 2)
        appended = json.loads(lines[1])
        self.assertEqual(appended["book_id"], 2)
        self.assertNotEqual(appended["generation"]["run_id"], old_generation["run_id"])
        self.assertEqual(appended["generation"]["model_id"], "new-model")


if __name__ == "__main__":
    unittest.main()
