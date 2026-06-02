from __future__ import annotations

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from langextract_kg import db as kg_db  # noqa: E402
from langextract_kg.extract_knowledge import (  # noqa: E402
    canonical_extraction_class,
    dedupe_records,
    find_unique_exact_span,
    mention_record_from_extraction,
)


class _Interval:
    start_pos = 0
    end_pos = 4


class _Status:
    value = "match_exact"


class _Extraction:
    extraction_class = "person"
    extraction_text = "أحمد"
    char_interval = _Interval()
    alignment_status = _Status()
    attributes = {"certainty": "ambiguous"}


class _MissingIntervalExtraction:
    extraction_class = "concept"
    extraction_text = "المسافر"
    char_interval = None
    alignment_status = None
    attributes = {"certainty": "explicit"}


class _TheonymPersonExtraction:
    extraction_class = "person"
    extraction_text = "الله"
    char_interval = _Interval()
    alignment_status = _Status()
    attributes = {"certainty": "explicit"}


class _PersonReferenceExtraction:
    extraction_class = "person"
    extraction_text = "رسول الله"
    char_interval = _Interval()
    alignment_status = _Status()
    attributes = {"certainty": "explicit"}


class DBHelpersTest(unittest.TestCase):
    def test_entity_type_for_class(self) -> None:
        self.assertEqual(kg_db.entity_type_for_class("fiqh_term"), "concept")
        self.assertEqual(kg_db.entity_type_for_class("book_reference"), "work_title")
        self.assertEqual(kg_db.entity_type_for_class("work_title"), "work_title")
        self.assertEqual(kg_db.entity_type_for_class("person_reference"), "person_reference")
        self.assertEqual(kg_db.entity_type_for_class("quran_reference"), "citation")

    def test_json_dumps_is_object_default(self) -> None:
        self.assertEqual(json.loads(kg_db.json_dumps(None)), {})

    def test_mention_record_flags_ambiguous_person(self) -> None:
        page = kg_db.PageSource(
            book_id=797,
            page_id=4,
            heading_id=3,
            heading_title="اسمه ونسبه",
            content_text="أحمد قال",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_Extraction(),
            page=page,
            document_id="book:797:page:4",
            source_text=page.content_text,
            source_hash="hash",
        )
        self.assertIsNone(error)
        self.assertIsNotNone(record)
        assert record is not None
        self.assertEqual(record["review_status"], "ambiguous")
        self.assertEqual(record["normalized_text"], "احمد")

    def test_unique_exact_span_fallback_handles_arabic_clitic(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="حكم صيام المريض والمسافر",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_MissingIntervalExtraction(),
            page=page,
            document_id="book:21818:page:1",
            source_text=page.content_text,
            source_hash="hash",
        )
        self.assertIsNone(error)
        assert record is not None
        self.assertEqual(record["exact_quote"], "المسافر")
        self.assertEqual(record["alignment_status"], "match_exact_substring_fallback")
        self.assertEqual(record["review_status"], "needs_review")

    def test_unique_exact_span_rejects_repeated_text(self) -> None:
        self.assertIsNone(find_unique_exact_span("الصيام ثم الصيام", "الصيام"))

    def test_mention_record_rejects_theonym_as_person(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="الله",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_TheonymPersonExtraction(),
            page=page,
            document_id="book:21818:page:1",
            source_text=page.content_text,
            source_hash="hash",
        )
        self.assertIsNone(record)
        assert error is not None
        self.assertEqual(error["code"], "NON_PERSON_THEONYM")

    def test_person_reference_is_reclassified_and_reviewed(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="رسول الله",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_PersonReferenceExtraction(),
            page=page,
            document_id="book:21818:page:1",
            source_text=page.content_text,
            source_hash="hash",
        )
        self.assertIsNone(error)
        assert record is not None
        self.assertEqual(record["extraction_class"], "person_reference")
        self.assertEqual(record["review_status"], "needs_review")

    def test_legacy_book_title_class_is_normalized(self) -> None:
        self.assertEqual(canonical_extraction_class("book_title", "صحيح البخاري"), "work_title")
        self.assertEqual(canonical_extraction_class("book_title", "سورة البقرة"), "quran_reference")

    def test_dedupe_records_keeps_first_span(self) -> None:
        base = {
            "run_id": "run-id",
            "book_id": 21818,
            "page_id": 5,
            "extraction_class": "work_title",
            "char_start": 575,
            "char_end": 586,
            "extraction_text": "سورة البقرة",
        }
        rows = [base, {**base, "extraction_text": "duplicate"}, {**base, "char_start": 600, "char_end": 611}]
        self.assertEqual([row["extraction_text"] for row in dedupe_records(rows)], ["سورة البقرة", "سورة البقرة"])


if __name__ == "__main__":
    unittest.main()
