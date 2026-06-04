from __future__ import annotations

import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from langextract_kg.prompts import get_prompt  # noqa: E402


class PromptsTest(unittest.TestCase):
    def test_prompt_examples_are_verbatim(self) -> None:
        for task in ["mentions", "terms", "citations", "relations"]:
            prompt = get_prompt(task)
            for example in prompt.examples:
                for extraction in example.extractions:
                    self.assertIn(extraction.extraction_text, example.text)

    def test_prompt_versions(self) -> None:
        self.assertEqual(get_prompt("mentions").version, "mentions_v2")
        self.assertEqual(get_prompt("terms").version, "terms_v2")
        self.assertEqual(get_prompt("citations").version, "citations_v3")
        self.assertEqual(get_prompt("relations").version, "relations_v1")

    def test_expected_classes_are_present(self) -> None:
        self.assertIn("person", get_prompt("mentions").extraction_classes)
        self.assertIn("person_reference", get_prompt("mentions").extraction_classes)
        self.assertIn("theonym", get_prompt("mentions").extraction_classes)
        self.assertIn("work_title", get_prompt("mentions").extraction_classes)
        self.assertNotIn("book_title", get_prompt("mentions").extraction_classes)
        self.assertIn("hadith_term", get_prompt("terms").extraction_classes)
        self.assertIn("qiraat_term", get_prompt("terms").extraction_classes)
        self.assertIn("quran_reference", get_prompt("citations").extraction_classes)
        self.assertIn("relation", get_prompt("relations").extraction_classes)

    def test_prompt_policy_hash_is_stable(self) -> None:
        self.assertEqual(get_prompt("mentions").policy_hash, get_prompt("mentions").policy_hash)
        self.assertEqual(len(get_prompt("mentions").policy_hash), 64)


if __name__ == "__main__":
    unittest.main()
