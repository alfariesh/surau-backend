from __future__ import annotations

from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from langextract_kg.run_mimo_eval import (  # noqa: E402
    FIFTY_GENRE_BOOKS,
    GOLDEN8_SPECS,
    expected_suite_size,
    is_model_error_code,
    normalize_page_row,
    parse_tasks,
    specs_values_sql,
)


class RunMimoEvalTest(unittest.TestCase):
    def test_suite_sizes_are_stable(self) -> None:
        self.assertEqual(expected_suite_size("golden8"), 8)
        self.assertEqual(len(GOLDEN8_SPECS), 8)
        self.assertEqual(expected_suite_size("fifty"), 50)
        self.assertEqual(set(FIFTY_GENRE_BOOKS), {"qiraat", "fiqh_usul", "hadith", "tafsir", "history_aqidah_adab"})

    def test_golden_values_sql_is_deterministic(self) -> None:
        values_sql = specs_values_sql(GOLDEN8_SPECS[:2])
        self.assertEqual(values_sql, "(1, 'qiraat_grammar', 3, 3),\n(2, 'qiraat_readings', 57, 3)")

    def test_normalize_page_row_fills_defaults(self) -> None:
        row = normalize_page_row({"book_id": "3", "page_id": "4", "content_text": "abc"}, 7)
        self.assertEqual(row["ord"], 7)
        self.assertEqual(row["genre"], "unknown")
        self.assertEqual(row["book_id"], 3)
        self.assertEqual(row["page_id"], 4)
        self.assertEqual(row["char_count"], 3)

    def test_parse_tasks_rejects_unknown_task(self) -> None:
        self.assertEqual(parse_tasks("mentions, terms"), ("mentions", "terms"))
        with self.assertRaises(SystemExit):
            parse_tasks("mentions,unknown")

    def test_model_api_error_counts_as_model_error(self) -> None:
        self.assertTrue(is_model_error_code("MODEL_API_ERROR"))
        self.assertTrue(is_model_error_code("MODEL_OUTPUT_SCHEMA_ERROR"))
        self.assertFalse(is_model_error_code("NON_EXACT_QUOTE"))


if __name__ == "__main__":
    unittest.main()
