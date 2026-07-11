from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

from scripts.langextract_kg.extract_knowledge import hydrate_chunk_audits
from scripts.langextract_kg.visualize_run import attach_machine_generation, write_visualization


class RawLangExtractGenerationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.generation = {
            "run_id": "018f47a2-4d50-7cc1-8b3f-1c319541f717",
            "model_id": "test-model",
            "prompt_version": "mentions-v1",
        }

    def test_attaches_typed_identity_to_every_document(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            path = Path(temp_dir) / "raw.jsonl"
            path.write_text(
                '{"document_id":"one","text":"A","extractions":[]}\n'
                '{"document_id":"two","text":"B","extractions":[]}\n',
                encoding="utf-8",
            )

            attach_machine_generation(path, self.generation)

            rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines()]
            self.assertEqual(2, len(rows))
            for row in rows:
                self.assertEqual("machine", row["provenance_class"])
                self.assertEqual(self.generation, row["generation"])

    def test_conflict_leaves_original_file_unchanged(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            path = Path(temp_dir) / "raw.jsonl"
            original = '{"document_id":"one","generation":{"run_id":"other"}}\n'
            path.write_text(original, encoding="utf-8")

            with self.assertRaisesRegex(ValueError, "line 1 conflicts"):
                attach_machine_generation(path, self.generation)

            self.assertEqual(original, path.read_text(encoding="utf-8"))

    def test_visualizer_reads_the_augmented_jsonl(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            out_dir = Path(temp_dir)

            def save_documents(_documents, *, output_name, output_dir, show_progress):
                del show_progress
                (Path(output_dir) / output_name).write_text(
                    '{"document_id":"one","text":"A","extractions":[]}\n',
                    encoding="utf-8",
                )

            def visualize(path):
                row = json.loads(Path(path).read_text(encoding="utf-8"))
                self.assertEqual(self.generation, row["generation"])
                return "<html></html>"

            with (
                mock.patch(
                    "scripts.langextract_kg.visualize_run.lx.io.save_annotated_documents",
                    side_effect=save_documents,
                ),
                mock.patch(
                    "scripts.langextract_kg.visualize_run.lx.visualize", side_effect=visualize
                ),
            ):
                jsonl_path, html_path = write_visualization(
                    [object()],
                    out_dir=out_dir,
                    output_stem="run.langextract",
                    generation=self.generation,
                )

            self.assertTrue(jsonl_path.exists())
            self.assertEqual("<html></html>", html_path.read_text(encoding="utf-8"))

    def test_raw_chunk_output_is_wrapped_with_generation_identity(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            chunks = [{"id": "chunk-one", "metadata": {}}]
            audits = [
                {
                    "request_index": 0,
                    "raw_output": '{"extractions":[]}',
                    "parse_status": "ok",
                }
            ]

            hydrate_chunk_audits(
                chunks,
                audits,
                Path(temp_dir),
                self.generation["run_id"],
                "mentions",
                self.generation,
            )

            raw_path = Path(chunks[0]["raw_output_path"])
            payload = json.loads(raw_path.read_text(encoding="utf-8"))
            self.assertEqual("machine", payload["provenance_class"])
            self.assertEqual(self.generation, payload["generation"])
            self.assertEqual('{"extractions":[]}', payload["raw_output"])


if __name__ == "__main__":
    unittest.main()
