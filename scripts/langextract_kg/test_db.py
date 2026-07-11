from __future__ import annotations

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from langextract_kg import db as kg_db  # noqa: E402
from langextract_kg.extract_knowledge import (  # noqa: E402
    attach_machine_generation,
    canonical_extraction_class,
    chunk_rejections_from_audits,
    dedupe_records,
    find_unique_exact_span,
    machine_generation_identity,
    mention_record_from_extraction,
    run_status_for_failures,
)


class _Interval:
    start_pos = 0
    end_pos = 4


class _Status:
    value = "match_exact"


class _DynamicExtraction:
    def __init__(
        self,
        extraction_class: str,
        extraction_text: str,
        *,
        char_start: int | None = None,
        char_end: int | None = None,
        attributes: dict[str, object] | None = None,
    ) -> None:
        self.extraction_class = extraction_class
        self.extraction_text = extraction_text
        if char_start is None or char_end is None:
            self.char_interval = None
        else:
            self.char_interval = type("_DynamicInterval", (), {"start_pos": char_start, "end_pos": char_end})()
        self.alignment_status = _Status()
        self.attributes = attributes or {"certainty": "explicit"}


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


class _TheonymPersonReferenceExtraction:
    extraction_class = "person_reference"
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


class _UnknownClassExtraction:
    extraction_class = "author"
    extraction_text = "أحمد"
    char_interval = _Interval()
    alignment_status = _Status()
    attributes = {"certainty": "explicit"}


class _BookTitleExtraction:
    extraction_class = "book_title"
    extraction_text = "صحيح البخاري"
    char_interval = _Interval()
    alignment_status = _Status()
    attributes = {"certainty": "explicit"}


class _SurahAsWorkTitleExtraction:
    extraction_class = "work_title"
    extraction_text = "سورة البقرة"
    char_interval = _Interval()
    alignment_status = _Status()
    attributes = {"certainty": "explicit"}


class _FormulaQuoteExtraction:
    extraction_class = "quote"
    extraction_text = "بسم الله الرحمن الرحيم"
    char_interval = _Interval()
    alignment_status = _Status()
    attributes = {"reference_type": "quote", "locator_text": "unknown", "citation_certainty": "explicit"}


class _ExplicitFormulaQuoteExtraction:
    extraction_class = "quote"
    extraction_text = "بسم الله الرحمن الرحيم"
    char_interval = _Interval()
    alignment_status = _Status()
    attributes = {"reference_type": "quran_quote", "locator_text": "الفاتحة: 1", "citation_certainty": "explicit"}


class _AuthorOnlyBookReferenceExtraction:
    extraction_class = "book_reference"
    extraction_text = "ابن الجزري"
    char_interval = _Interval()
    alignment_status = _Status()
    attributes = {"reference_type": "author", "locator_text": "unknown", "citation_certainty": "explicit"}


class _FakeCursor:
    def __init__(self, rows: list[tuple[object, ...]]) -> None:
        self.rows = rows
        self.sql = ""
        self.params: tuple[object, ...] = ()
        self.executions: list[tuple[str, tuple[object, ...]]] = []

    def __enter__(self) -> "_FakeCursor":
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def execute(self, sql: str, params: tuple[object, ...]) -> None:
        self.sql = sql
        self.params = params
        self.executions.append((sql, params))

    def fetchall(self) -> list[tuple[object, ...]]:
        return self.rows

    def fetchone(self) -> tuple[object, ...]:
        return self.rows[0]


class _FakeConn:
    def __init__(self, cursor: _FakeCursor) -> None:
        self._cursor = cursor
        self.commits = 0
        self.rollbacks = 0

    def cursor(self) -> _FakeCursor:
        return self._cursor

    def commit(self) -> None:
        self.commits += 1

    def rollback(self) -> None:
        self.rollbacks += 1


class _SourceSpanDBClient(kg_db.DBClient):
    def __init__(self, conn: _FakeConn) -> None:
        super().__init__(conn, "fake")
        self.source_span_calls: list[tuple[dict[str, object], str, str]] = []

    def insert_source_span(self, record: dict[str, object], *, object_type: str, object_id: str) -> str:
        self.source_span_calls.append((dict(record), object_type, object_id))
        return "existing-source-span-id"


class _CandidateSpyDBClient(kg_db.DBClient):
    def __init__(self) -> None:
        super().__init__(_FakeConn(_FakeCursor([])), "fake")
        self.calls: list[str] = []

    def find_entities_by_alias(self, entity_type: str, normalized_alias: str) -> list[dict[str, object]]:
        self.calls.append(f"find:{entity_type}:{normalized_alias}")
        return []

    def create_entity_from_mention(self, mention: dict[str, object], entity_type: str, normalized_alias: str) -> str:
        self.calls.append(f"create:{entity_type}:{normalized_alias}")
        return "entity-id"

    def insert_entity_candidate(
        self,
        *,
        mention_id: str,
        entity_id: str,
        score: float,
        strategy: str,
        reasons: dict[str, object],
        review_status: str,
    ) -> None:
        self.calls.append(f"candidate:{mention_id}:{entity_id}:{strategy}")


class DBHelpersTest(unittest.TestCase):
    def test_machine_generation_identity_is_typed_on_every_jsonl_row(self) -> None:
        generation = machine_generation_identity(
            "00000000-0000-4000-8000-000000000012",
            "model-v1",
            "mentions_v1",
        )
        rows = [{"kind": "knowledge_mention"}, {"code": "MODEL_OUTPUT_PARSE_ERROR"}]

        attach_machine_generation(rows, generation)

        for row in rows:
            self.assertEqual(row["provenance_class"], "machine")
            self.assertEqual(row["run_id"], generation["run_id"])
            self.assertEqual(row["generation"], generation)

        with self.assertRaisesRegex(ValueError, "valid UUID"):
            machine_generation_identity("bad", "model-v1", "mentions_v1")

    def test_entity_type_for_class(self) -> None:
        self.assertEqual(kg_db.entity_type_for_class("fiqh_term"), "concept")
        self.assertEqual(kg_db.entity_type_for_class("qiraat_term"), "concept")
        self.assertEqual(kg_db.entity_type_for_class("book_reference"), "work_title")
        self.assertEqual(kg_db.entity_type_for_class("work_title"), "work_title")
        self.assertEqual(kg_db.entity_type_for_class("person_reference"), "person_reference")
        self.assertEqual(kg_db.entity_type_for_class("theonym"), "theonym")
        self.assertEqual(kg_db.entity_type_for_class("quran_reference"), "citation")

    def test_json_dumps_is_object_default(self) -> None:
        self.assertEqual(json.loads(kg_db.json_dumps(None)), {})

    def test_create_run_registers_generation_before_extraction_atomically(self) -> None:
        cursor = _FakeCursor([(True,)])
        conn = _FakeConn(cursor)
        client = kg_db.DBClient(conn, "fake")
        run = {
            "id": "00000000-0000-4000-8000-000000000001",
            "task_name": "mentions",
            "prompt_version": "mentions_v1",
            "model_id": "model-v1",
            "provider": "openai",
            "provider_base_url": "https://example.test/v1",
            "parameters": {"temperature": 0},
            "source_scope": {"book_id": 797},
            "status": "running",
            "total_documents": 4,
        }

        client.create_run(run)

        self.assertEqual(len(cursor.executions), 2)
        generation_sql, generation_params = cursor.executions[0]
        extraction_sql, extraction_params = cursor.executions[1]
        self.assertIn("INSERT INTO generation_runs", generation_sql)
        self.assertIn("INSERT INTO knowledge_extraction_runs", extraction_sql)
        self.assertEqual(generation_params[1:5], ("mentions", "model-v1", "mentions_v1", "openai"))
        self.assertEqual(json.loads(str(generation_params[5]))["source_scope"], {"book_id": 797})
        identity_keys = ("id", "task_name", "prompt_version", "model_id", "provider")
        self.assertEqual(extraction_params[:5], tuple(run[key] for key in identity_keys))
        self.assertEqual(conn.commits, 1)
        self.assertEqual(conn.rollbacks, 0)

    def test_create_run_rejects_conflicting_generation_descriptor(self) -> None:
        cursor = _FakeCursor([(False,)])
        conn = _FakeConn(cursor)
        client = kg_db.DBClient(conn, "fake")
        run = {
            "id": "00000000-0000-4000-8000-000000000002",
            "task_name": "mentions",
            "prompt_version": "mentions_v1",
            "model_id": "conflicting-model",
            "parameters": {},
            "source_scope": {},
        }

        with self.assertRaisesRegex(ValueError, "conflicts with its registered descriptor"):
            client.create_run(run)

        self.assertEqual(len(cursor.executions), 1)
        self.assertEqual(conn.commits, 0)
        self.assertEqual(conn.rollbacks, 1)

    def test_knowledge_writer_validates_generation_before_any_insert(self) -> None:
        cursor = _FakeCursor([(True,)])
        client = kg_db.DBClient(_FakeConn(cursor), "fake")
        record = {
            "run_id": "00000000-0000-4000-8000-000000000010",
            "provenance_class": "machine",
            "generation": {
                "run_id": "00000000-0000-4000-8000-000000000010",
                "model_id": "model-v1",
                "prompt_version": "mentions_v1",
            },
        }

        client._verify_mention_generation_identities([record])

        self.assertEqual(len(cursor.executions), 1)
        self.assertIn("JOIN knowledge_extraction_runs", cursor.sql)
        self.assertEqual(
            cursor.params,
            ("00000000-0000-4000-8000-000000000010", "model-v1", "mentions_v1"),
        )

        cursor.executions.clear()
        with self.assertRaisesRegex(ValueError, "line 1: generation identity is required"):
            client.insert_mentions_with_candidates([{"run_id": record["run_id"], "provenance_class": "machine"}])
        self.assertEqual(cursor.executions, [])

    def test_knowledge_writer_rejects_unregistered_generation_tuple(self) -> None:
        cursor = _FakeCursor([(False,)])
        client = kg_db.DBClient(_FakeConn(cursor), "fake")
        record = {
            "run_id": "00000000-0000-4000-8000-000000000011",
            "provenance_class": "machine",
            "generation": {
                "run_id": "00000000-0000-4000-8000-000000000011",
                "model_id": "wrong-model",
                "prompt_version": "mentions_v1",
            },
        }

        with self.assertRaisesRegex(ValueError, "not registered with this model/prompt tuple"):
            client._verify_mention_generation_identities([record])

    def test_normalize_postgres_url_quotes_unescaped_password_at_sign(self) -> None:
        normalized = kg_db.normalize_postgres_url(
            "postgres://user:myAwEsOm3pa55@w0rd@localhost:5432/db?sslmode=disable"
        )
        self.assertEqual(
            normalized,
            "postgres://user:myAwEsOm3pa55%40w0rd@localhost:5432/db?sslmode=disable",
        )

    def test_normalize_postgres_url_leaves_encoded_url_unchanged(self) -> None:
        postgres_url = "postgres://user:myAwEsOm3pa55%40w0rd@localhost:5432/db?sslmode=disable"
        self.assertEqual(kg_db.normalize_postgres_url(postgres_url), postgres_url)

    def test_theonym_mentions_do_not_create_entity_candidates(self) -> None:
        client = _CandidateSpyDBClient()
        client.upsert_candidates_for_mention(
            {
                "id": "mention-id",
                "extraction_class": "theonym",
                "extraction_text": "الله",
                "review_status": "pending",
            }
        )
        self.assertEqual(client.calls, [])

    def test_resume_uses_document_audit_and_policy_hash(self) -> None:
        cursor = _FakeCursor([(4,), ("5",)])
        client = kg_db.DBClient(_FakeConn(cursor), "fake")
        processed = client.already_processed_page_ids(
            book_id=797,
            task_name="mentions",
            prompt_version="mentions_v1",
            policy_hash="hash-1",
        )
        self.assertEqual(processed, {4, 5})
        self.assertIn("knowledge_extraction_documents", cursor.sql)
        self.assertNotIn("knowledge_mentions", cursor.sql)
        self.assertEqual(cursor.params, (797, "mentions", "mentions_v1", "hash-1", "hash-1"))

    def test_insert_mention_upserts_source_span_after_actual_mention_id(self) -> None:
        cursor = _FakeCursor([("existing-mention-id",)])
        conn = _FakeConn(cursor)
        client = _SourceSpanDBClient(conn)
        record: dict[str, object] = {
            "id": "new-mention-id",
            "source_span_id": "new-source-span-id",
            "run_id": "run-id",
            "book_id": 21818,
            "page_id": 1,
            "heading_id": 2,
            "document_id": "book:21818:page:1",
            "extraction_class": "work_title",
            "extraction_text": "صحيح البخاري",
            "exact_quote": "صحيح البخاري",
            "char_start": 0,
            "char_end": 12,
            "alignment_status": "match_exact",
            "attributes": {"certainty": "explicit"},
            "normalized_text": "صحيح البخاري",
            "normalization_version": 1,
            "grounded": True,
            "confidence": 0.8,
            "review_status": "pending",
            "source_hash": "hash",
        }

        actual_id = client.insert_mention(record)

        self.assertEqual(actual_id, "existing-mention-id")
        self.assertEqual(record["id"], "existing-mention-id")
        self.assertEqual(record["source_span_id"], "existing-source-span-id")
        self.assertEqual(client.source_span_calls[0][1:], ("mention", "existing-mention-id"))
        self.assertEqual(client.source_span_calls[0][0]["source_span_id"], "new-source-span-id")
        insert_sql, insert_params = cursor.executions[0]
        self.assertIn("INSERT INTO knowledge_mentions", insert_sql)
        self.assertEqual(insert_params[14], 1)
        self.assertIsNone(insert_params[19])
        update_sql, update_params = cursor.executions[1]
        self.assertIn("UPDATE knowledge_mentions", update_sql)
        self.assertEqual(update_params, ("existing-source-span-id", "existing-mention-id"))
        self.assertEqual(conn.commits, 1)

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
        self.assertEqual(record["normalization_version"], 1)

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

    def test_normalized_missing_interval_fallback_uses_source_slice(self) -> None:
        page = kg_db.PageSource(
            book_id=9,
            page_id=3,
            heading_id=None,
            heading_title="مقدمة",
            content_text="وهان على سراة بني لؤي حريق",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_DynamicExtraction("group", "بني لُؤيٍّ"),
            page=page,
            document_id="book:9:page:3",
            source_text=page.content_text,
            source_hash="hash",
            task_name="mentions",
            allowed_classes=("group",),
        )
        self.assertIsNone(error)
        assert record is not None
        self.assertEqual(record["extraction_text"], "بني لؤي")
        self.assertEqual(record["exact_quote"], "بني لؤي")
        self.assertEqual(record["alignment_status"], "match_normalized_substring_fallback")
        self.assertEqual(record["attributes"]["model_extraction_text"], "بني لُؤيٍّ")
        self.assertEqual(record["review_status"], "needs_review")

    def test_normalized_interval_fallback_uses_source_slice(self) -> None:
        source_text = "قال رسول الله صلى الله عليه وسلم"
        page = kg_db.PageSource(
            book_id=11,
            page_id=11,
            heading_id=None,
            heading_title="شرح",
            content_text=source_text,
        )
        start = source_text.index("رسول")
        end = len(source_text)
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_DynamicExtraction(
                "person_reference",
                "رسولِ اللَّهِ صلى الله عليه وسلم",
                char_start=start,
                char_end=end,
            ),
            page=page,
            document_id="book:11:page:11",
            source_text=page.content_text,
            source_hash="hash",
            task_name="mentions",
            allowed_classes=("person_reference",),
        )
        self.assertIsNone(error)
        assert record is not None
        self.assertEqual(record["extraction_text"], "رسول الله صلى الله عليه وسلم")
        self.assertEqual(record["alignment_status"], "match_normalized_substring_fallback")
        self.assertEqual(record["attributes"]["model_extraction_text"], "رسولِ اللَّهِ صلى الله عليه وسلم")

    def test_normalized_fallback_rejects_repeated_phrase(self) -> None:
        page = kg_db.PageSource(
            book_id=9,
            page_id=3,
            heading_id=None,
            heading_title="فقه",
            content_text="الصيام ثم الصيام",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_DynamicExtraction("fiqh_term", "الصِّيام"),
            page=page,
            document_id="book:9:page:3",
            source_text=page.content_text,
            source_hash="hash",
            task_name="terms",
            allowed_classes=("fiqh_term",),
        )
        self.assertIsNone(record)
        assert error is not None
        self.assertEqual(error["code"], "UNGROUNDED")

    def test_segmented_quote_fallback_is_quote_only(self) -> None:
        source_text = "صدر البيت وسط البيت عجز البيت"
        page = kg_db.PageSource(
            book_id=9,
            page_id=3,
            heading_id=None,
            heading_title="شعر",
            content_text=source_text,
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_DynamicExtraction(
                "poetry",
                "صدر البيت … عجز البيت",
                attributes={"reference_type": "poetry", "citation_certainty": "explicit"},
            ),
            page=page,
            document_id="book:9:page:3",
            source_text=page.content_text,
            source_hash="hash",
            task_name="citations",
            allowed_classes=("poetry",),
        )
        self.assertIsNone(error)
        assert record is not None
        self.assertEqual(record["extraction_text"], source_text)
        self.assertEqual(record["alignment_status"], "match_segmented_quote_fallback")
        self.assertEqual(record["attributes"]["model_extraction_text"], "صدر البيت … عجز البيت")

        term_record, term_error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_DynamicExtraction("fiqh_term", "صدر البيت … عجز البيت"),
            page=page,
            document_id="book:9:page:3",
            source_text=page.content_text,
            source_hash="hash",
            task_name="terms",
            allowed_classes=("fiqh_term",),
        )
        self.assertIsNone(term_record)
        assert term_error is not None
        self.assertEqual(term_error["code"], "UNGROUNDED")

    def test_mention_record_canonicalizes_theonym_person(self) -> None:
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
            task_name="mentions",
            allowed_classes=("person", "person_reference", "theonym"),
        )
        self.assertIsNone(error)
        assert record is not None
        self.assertEqual(record["extraction_class"], "theonym")
        self.assertEqual(record["normalized_text"], "الله")

    def test_mention_record_canonicalizes_theonym_person_reference(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="الله",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_TheonymPersonReferenceExtraction(),
            page=page,
            document_id="book:21818:page:1",
            source_text=page.content_text,
            source_hash="hash",
            task_name="mentions",
            allowed_classes=("person", "person_reference", "theonym"),
        )
        self.assertIsNone(error)
        assert record is not None
        self.assertEqual(record["extraction_class"], "theonym")

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
        self.assertEqual(canonical_extraction_class("person_reference", "الله"), "theonym")

    def test_mention_record_rejects_class_not_allowed_for_task(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="أحمد",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_UnknownClassExtraction(),
            page=page,
            document_id="book:21818:page:1",
            source_text=page.content_text,
            source_hash="hash",
            task_name="mentions",
            allowed_classes=("person", "work_title"),
        )
        self.assertIsNone(record)
        assert error is not None
        self.assertEqual(error["code"], "CLASS_NOT_ALLOWED")
        self.assertEqual(error["attributes"]["original_extraction_class"], "author")
        self.assertEqual(error["attributes"]["canonical_extraction_class"], "author")
        self.assertEqual(error["attributes"]["task"], "mentions")
        self.assertEqual(error["attributes"]["allowed_classes"], ["person", "work_title"])

    def test_canonical_class_is_validated_after_normalization(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="صحيح البخاري",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_BookTitleExtraction(),
            page=page,
            document_id="book:21818:page:1",
            source_text=page.content_text,
            source_hash="hash",
            task_name="mentions",
            allowed_classes=("work_title",),
        )
        self.assertIsNone(error)
        assert record is not None
        self.assertEqual(record["extraction_class"], "work_title")

    def test_surah_canonicalized_out_of_mentions_is_rejected(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="سورة البقرة",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_SurahAsWorkTitleExtraction(),
            page=page,
            document_id="book:21818:page:1",
            source_text=page.content_text,
            source_hash="hash",
            task_name="mentions",
            allowed_classes=("person", "person_reference", "place", "work_title", "group", "institution"),
        )
        self.assertIsNone(record)
        assert error is not None
        self.assertEqual(error["code"], "CLASS_NOT_ALLOWED")
        self.assertEqual(error["attributes"]["original_extraction_class"], "work_title")
        self.assertEqual(error["attributes"]["canonical_extraction_class"], "quran_reference")

    def test_formula_only_citation_is_rejected(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="بسم الله الرحمن الرحيم",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_FormulaQuoteExtraction(),
            page=page,
            document_id="book:21818:page:1",
            source_text=page.content_text,
            source_hash="hash",
            task_name="citations",
            allowed_classes=("quran_reference", "hadith_reference", "athar", "quote", "poetry", "book_reference", "isnad_chain"),
        )
        self.assertIsNone(record)
        assert error is not None
        self.assertEqual(error["code"], "FORMULA_NOT_CITATION")
        self.assertEqual(error["attributes"]["canonical_extraction_class"], "quote")

    def test_formula_citation_with_explicit_locator_is_allowed(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="بسم الله الرحمن الرحيم",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_ExplicitFormulaQuoteExtraction(),
            page=page,
            document_id="book:21818:page:1",
            source_text=page.content_text,
            source_hash="hash",
            task_name="citations",
            allowed_classes=("quran_reference", "hadith_reference", "athar", "quote", "poetry", "book_reference", "isnad_chain"),
        )
        self.assertIsNone(error)
        assert record is not None
        self.assertEqual(record["extraction_class"], "quote")

    def test_author_only_book_reference_is_rejected(self) -> None:
        page = kg_db.PageSource(
            book_id=3,
            page_id=3,
            heading_id=None,
            heading_title="مقدمة",
            content_text="ابن الجزري",
        )
        record, error = mention_record_from_extraction(
            run_id="run-id",
            extraction=_AuthorOnlyBookReferenceExtraction(),
            page=page,
            document_id="book:3:page:3",
            source_text=page.content_text,
            source_hash="hash",
            task_name="citations",
            allowed_classes=("quran_reference", "hadith_reference", "athar", "quote", "poetry", "book_reference", "isnad_chain"),
        )
        self.assertIsNone(record)
        assert error is not None
        self.assertEqual(error["code"], "AUTHOR_ONLY_BOOK_REFERENCE")
        self.assertEqual(error["attributes"]["reference_type"], "author")
        self.assertEqual(error["attributes"]["canonical_extraction_class"], "book_reference")

    def test_chunk_schema_error_creates_rejection(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="سورة البقرة",
        )
        chunks = [
            {
                "document_id": "book:21818:page:1",
                "chunk_index": 0,
                "pass_index": 0,
                "char_start": 0,
                "char_end": len(page.content_text),
                "parse_status": "schema_error",
                "raw_output_path": "/tmp/raw.json",
                "error_message": "bad shape",
                "metadata": {"request_index": 7},
            }
        ]
        rejections = chunk_rejections_from_audits("run-id", chunks, {"book:21818:page:1": page})
        self.assertEqual(len(rejections), 1)
        self.assertEqual(rejections[0]["code"], "MODEL_OUTPUT_SCHEMA_ERROR")
        self.assertEqual(rejections[0]["raw_output_path"], "/tmp/raw.json")
        self.assertEqual(rejections[0]["chunk_index"], 0)
        self.assertEqual(rejections[0]["attributes"]["request_index"], 7)

    def test_successful_empty_extractions_do_not_create_chunk_rejection(self) -> None:
        page = kg_db.PageSource(
            book_id=21818,
            page_id=1,
            heading_id=2,
            heading_title="مقدمة",
            content_text="",
        )
        chunks = [{"document_id": "book:21818:page:1", "parse_status": "success"}]
        self.assertEqual(chunk_rejections_from_audits("run-id", chunks, {"book:21818:page:1": page}), [])

    def test_run_status_reflects_chunk_rejection_failures(self) -> None:
        self.assertEqual(run_status_for_failures([]), "success")
        self.assertEqual(run_status_for_failures([{"code": "MODEL_OUTPUT_SCHEMA_ERROR"}]), "completed_with_errors")

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
