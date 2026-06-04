from __future__ import annotations

import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from langextract_kg.qa_extractions import validate_rows  # noqa: E402


def valid_row(**overrides: object) -> dict[str, object]:
    row: dict[str, object] = {
        "kind": "knowledge_mention",
        "run_id": "00000000-0000-0000-0000-000000000001",
        "book_id": 797,
        "page_id": 4,
        "heading_id": 3,
        "document_id": "book:797:page:4",
        "extraction_class": "person",
        "extraction_text": "أبو حامد الغزالي",
        "exact_quote": "أبو حامد الغزالي",
        "char_start": 10,
        "char_end": 26,
        "alignment_status": "match_exact",
        "attributes": {"certainty": "explicit"},
        "normalized_text": "ابو حامد الغزالي",
        "grounded": True,
        "confidence": 0.8,
        "review_status": "pending",
    }
    row.update(overrides)
    return row


class QAExtractionsTest(unittest.TestCase):
    def issue_codes(self, rows: list[dict[str, object]]) -> set[str]:
        return {issue.code for issue in validate_rows(rows)}

    def test_valid_row_passes(self) -> None:
        self.assertEqual(validate_rows([valid_row()]), [])

    def test_non_exact_quote_fails(self) -> None:
        codes = self.issue_codes([valid_row(exact_quote="الغزالي")])
        self.assertIn("NON_EXACT_QUOTE", codes)

    def test_generic_extraction_fails(self) -> None:
        codes = self.issue_codes(
            [
                valid_row(
                    extraction_text="قال",
                    exact_quote="قال",
                    normalized_text="قال",
                )
            ]
        )
        self.assertIn("GENERIC_EXTRACTION", codes)

    def test_ambiguous_person_must_be_flagged(self) -> None:
        codes = self.issue_codes(
            [
                valid_row(
                    extraction_text="أحمد",
                    exact_quote="أحمد",
                    normalized_text="احمد",
                    review_status="pending",
                )
            ]
        )
        self.assertIn("AMBIGUOUS_PERSON_NOT_FLAGGED", codes)

    def test_theonym_as_person_fails(self) -> None:
        codes = self.issue_codes(
            [
                valid_row(
                    extraction_text="الله",
                    exact_quote="الله",
                    normalized_text="الله",
                )
            ]
        )
        self.assertIn("THEONYM_AS_PERSON", codes)

    def test_theonym_as_person_reference_fails(self) -> None:
        codes = self.issue_codes(
            [
                valid_row(
                    extraction_class="person_reference",
                    extraction_text="الله",
                    exact_quote="الله",
                    normalized_text="الله",
                    review_status="needs_review",
                )
            ]
        )
        self.assertIn("THEONYM_AS_PERSON_REFERENCE", codes)

    def test_person_reference_as_person_fails(self) -> None:
        codes = self.issue_codes(
            [
                valid_row(
                    extraction_text="رسول الله",
                    exact_quote="رسول الله",
                    normalized_text="رسول الله",
                )
            ]
        )
        self.assertIn("PERSON_REFERENCE_AS_PERSON", codes)

    def test_person_reference_needs_review(self) -> None:
        codes = self.issue_codes(
            [
                valid_row(
                    extraction_class="person_reference",
                    extraction_text="رسول الله",
                    exact_quote="رسول الله",
                    normalized_text="رسول الله",
                    review_status="pending",
                )
            ]
        )
        self.assertIn("PERSON_REFERENCE_AUTO_REVIEW", codes)

    def test_legacy_book_title_fails(self) -> None:
        codes = self.issue_codes(
            [
                valid_row(
                    extraction_class="book_title",
                    extraction_text="صحيح البخاري",
                    exact_quote="صحيح البخاري",
                    normalized_text="صحيح البخاري",
                )
            ]
        )
        self.assertIn("LEGACY_BOOK_TITLE_CLASS", codes)

    def test_surah_as_work_title_fails(self) -> None:
        codes = self.issue_codes(
            [
                valid_row(
                    extraction_class="work_title",
                    extraction_text="سورة البقرة",
                    exact_quote="سورة البقرة",
                    normalized_text="سورة البقرة",
                )
            ]
        )
        self.assertIn("SURAH_AS_WORK_TITLE", codes)

    def test_fallback_pending_warns(self) -> None:
        codes = self.issue_codes(
            [
                valid_row(
                    alignment_status="match_exact_substring_fallback",
                    review_status="pending",
                )
            ]
        )
        self.assertIn("FALLBACK_ALIGNMENT_PENDING", codes)

    def test_duplicate_span_fails(self) -> None:
        codes = self.issue_codes([valid_row(), valid_row()])
        self.assertIn("DUPLICATE_SPAN", codes)


if __name__ == "__main__":
    unittest.main()
