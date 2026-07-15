#!/usr/bin/env python3
"""PostgreSQL access for the LangExtract knowledge pipeline."""

from __future__ import annotations

from dataclasses import dataclass
import json
import os
from pathlib import Path
from typing import Any
from urllib.parse import quote
import uuid

from .arabic_normalize import PROFILE_VERSION, normalized_key


DEFAULT_ENV_FILE = Path(__file__).resolve().parents[2] / ".env.local"


@dataclass(frozen=True)
class PageSource:
    book_id: int
    page_id: int
    heading_id: int | None
    heading_title: str | None
    content_text: str


def load_env_file(path: Path = DEFAULT_ENV_FILE) -> None:
    """Load simple KEY=VALUE pairs without overriding exported values."""
    if not path.exists():
        return
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        if key and key not in os.environ:
            os.environ[key] = value


def normalize_postgres_url(postgres_url: str) -> str:
    """Quote unescaped @ signs in local Postgres passwords for psycopg."""
    if not postgres_url or "://" not in postgres_url:
        return postgres_url
    scheme, rest = postgres_url.split("://", 1)
    if scheme not in {"postgres", "postgresql"}:
        return postgres_url

    authority_end = len(rest)
    for marker in ("/", "?", "#"):
        marker_index = rest.find(marker)
        if marker_index != -1:
            authority_end = min(authority_end, marker_index)
    authority = rest[:authority_end]
    suffix = rest[authority_end:]
    if authority.count("@") <= 1:
        return postgres_url

    userinfo, hostinfo = authority.rsplit("@", 1)
    if ":" not in userinfo:
        return postgres_url
    user, password = userinfo.split(":", 1)
    quoted_userinfo = f"{quote(user, safe='')}:{quote(password, safe='')}"
    return f"{scheme}://{quoted_userinfo}@{hostinfo}{suffix}"


def postgres_url_from_env() -> str:
    dedicated = os.environ.get("LANGEXTRACT_PG_URL") or ""
    if dedicated:
        return normalize_postgres_url(dedicated)
    allow_legacy = (os.environ.get("ALLOW_LEGACY_DB_CREDENTIALS") or "").strip().lower()
    if allow_legacy in {"1", "true", "yes"}:
        return normalize_postgres_url(os.environ.get("PG_URL") or os.environ.get("POSTGRES_URL") or "")
    return ""


def json_dumps(value: Any) -> str:
    return json.dumps(value if value is not None else {}, ensure_ascii=False, sort_keys=True)


def new_uuid() -> str:
    return str(uuid.uuid4())


def machine_generation_tuple(record: dict[str, Any], line: int) -> tuple[str, str, str]:
    """Validate one knowledge output before any row from its batch is written."""
    if record.get("provenance_class") != "machine":
        raise ValueError(f"line {line}: provenance_class machine is required")

    generation = record.get("generation")
    if not isinstance(generation, dict):
        raise ValueError(f"line {line}: generation identity is required")

    try:
        run_id = str(uuid.UUID(str(generation.get("run_id") or "").strip()))
        record_run_id = str(uuid.UUID(str(record.get("run_id") or "").strip()))
    except ValueError as err:
        raise ValueError(f"line {line}: generation run_id must be a valid UUID") from err
    if record_run_id != run_id:
        raise ValueError(f"line {line}: run_id must equal generation.run_id")

    model_id = str(generation.get("model_id") or "").strip()
    prompt_version = str(generation.get("prompt_version") or "").strip()
    if not model_id:
        raise ValueError(f"line {line}: generation.model_id is required")
    if not prompt_version:
        raise ValueError(f"line {line}: generation.prompt_version is required")

    return run_id, model_id, prompt_version


def entity_type_for_class(extraction_class: str) -> str:
    mapping = {
        "person": "person",
        "person_reference": "person_reference",
        "place": "place",
        "work_title": "work_title",
        "book_title": "work_title",
        "book_reference": "work_title",
        "group": "group",
        "institution": "institution",
        "theonym": "theonym",
        "concept": "concept",
        "fiqh_term": "concept",
        "aqidah_term": "concept",
        "hadith_term": "concept",
        "qiraat_term": "concept",
        "arabic_language_term": "concept",
        "adab_term": "concept",
        "tasawwuf_term": "concept",
        "quran_reference": "citation",
        "hadith_reference": "citation",
        "athar": "citation",
        "isnad_chain": "citation",
        "quote": "quote",
        "poetry": "quote",
    }
    return mapping.get(extraction_class, "concept")


class MissingPostgresDriver(RuntimeError):
    pass


class DBClient:
    """Small DB wrapper that supports psycopg 3 or psycopg2."""

    def __init__(self, conn: Any, driver: str):
        self.conn = conn
        self.driver = driver
        self._defer_commit = False

    def _commit(self) -> None:
        if not self._defer_commit:
            self.conn.commit()

    @classmethod
    def connect(cls, postgres_url: str) -> "DBClient":
        if not postgres_url:
            raise ValueError("LANGEXTRACT_PG_URL or --pg-url is required for database access")
        postgres_url = normalize_postgres_url(postgres_url)
        try:
            import psycopg  # type: ignore

            conn = psycopg.connect(postgres_url)
            return cls(conn, "psycopg3")
        except ImportError:
            pass

        try:
            import psycopg2  # type: ignore

            conn = psycopg2.connect(postgres_url)
            return cls(conn, "psycopg2")
        except ImportError as err:
            raise MissingPostgresDriver(
                "PostgreSQL writes require psycopg or psycopg2. "
                "Install one locally, or run dry extraction to JSONL only."
            ) from err

    def close(self) -> None:
        self.conn.close()

    def _cursor(self, dict_rows: bool = False):
        if self.driver == "psycopg3":
            if dict_rows:
                from psycopg.rows import dict_row  # type: ignore

                return self.conn.cursor(row_factory=dict_row)
            return self.conn.cursor()

        if dict_rows:
            from psycopg2.extras import RealDictCursor  # type: ignore

            return self.conn.cursor(cursor_factory=RealDictCursor)
        return self.conn.cursor()

    def fetch_pages(
        self,
        *,
        book_id: int,
        page_ids: list[int],
        limit: int,
        skip_page_ids: set[int] | None = None,
    ) -> list[PageSource]:
        where = ["bp.book_id = %s", "bp.is_deleted = false"]
        params: list[Any] = [book_id]
        if page_ids:
            where.append("bp.page_id = ANY(%s)")
            params.append(page_ids)
        if skip_page_ids:
            where.append("NOT (bp.page_id = ANY(%s))")
            params.append(sorted(skip_page_ids))
        limit_sql = ""
        if limit > 0:
            limit_sql = "LIMIT %s"
            params.append(limit)

        sql = f"""
SELECT bp.book_id,
       bp.page_id,
       ctx.heading_id,
       ctx.heading_title,
       bp.content_text
FROM book_pages bp
LEFT JOIN LATERAL (
    SELECT h.heading_id,
           h.content AS heading_title
    FROM book_heading_ranges hr
    JOIN book_headings h ON h.book_id = hr.book_id AND h.heading_id = hr.heading_id
    WHERE hr.book_id = bp.book_id
      AND bp.page_id BETWEEN hr.start_page_id AND hr.end_page_id
      AND h.is_deleted = false
    ORDER BY h.depth DESC, h.ordinal DESC, h.heading_id ASC
    LIMIT 1
) ctx ON true
WHERE {' AND '.join(where)}
ORDER BY bp.page_id ASC
{limit_sql}
"""
        with self._cursor(dict_rows=True) as cur:
            cur.execute(sql, params)
            rows = cur.fetchall()
        return [
            PageSource(
                book_id=int(row["book_id"]),
                page_id=int(row["page_id"]),
                heading_id=int(row["heading_id"]) if row.get("heading_id") is not None else None,
                heading_title=row.get("heading_title"),
                content_text=str(row["content_text"] or ""),
            )
            for row in rows
        ]

    def already_processed_page_ids(
        self,
        *,
        book_id: int,
        task_name: str,
        prompt_version: str,
        policy_hash: str = "",
    ) -> set[int]:
        sql = """
SELECT DISTINCT d.page_id
FROM knowledge_extraction_documents d
JOIN knowledge_extraction_runs r ON r.id = d.run_id
WHERE d.book_id = %s
  AND r.task_name = %s
  AND r.prompt_version = %s
  AND r.status IN ('running', 'success', 'completed_with_errors')
  AND (%s = '' OR r.parameters->>'prompt_policy_hash' = %s)
"""
        with self._cursor() as cur:
            cur.execute(sql, (book_id, task_name, prompt_version, policy_hash, policy_hash))
            return {int(row[0]) for row in cur.fetchall()}

    def create_run(self, run: dict[str, Any]) -> None:
        provider = run.get("provider", "openai")
        generation_metadata = json_dumps(
            {
                "source": "knowledge_extraction_runs",
                "parameters": run.get("parameters") or {},
                "source_scope": run.get("source_scope") or {},
            }
        )
        generation_sql = """
WITH inserted AS (
    INSERT INTO generation_runs (
        id, task_name, model_id, prompt_version, provider, metadata
    )
    VALUES (%s, %s, %s, %s, %s, %s::jsonb)
    ON CONFLICT (id) DO NOTHING
    RETURNING TRUE AS descriptor_matches
)
SELECT descriptor_matches
FROM inserted
UNION ALL
SELECT task_name = %s
   AND model_id = %s
   AND prompt_version = %s
   AND provider IS NOT DISTINCT FROM %s
   AND metadata = %s::jsonb AS descriptor_matches
FROM generation_runs
WHERE id = %s
LIMIT 1
"""
        extraction_sql = """
INSERT INTO knowledge_extraction_runs (
    id, task_name, prompt_version, model_id, provider, provider_base_url,
    parameters, source_scope, status, total_documents
)
VALUES (%s, %s, %s, %s, %s, %s, %s::jsonb, %s::jsonb, %s, %s)
"""
        try:
            with self._cursor() as cur:
                cur.execute(
                    generation_sql,
                    (
                        run["id"],
                        run["task_name"],
                        run["model_id"],
                        run["prompt_version"],
                        provider,
                        generation_metadata,
                        run["task_name"],
                        run["model_id"],
                        run["prompt_version"],
                        provider,
                        generation_metadata,
                        run["id"],
                    ),
                )
                descriptor_row = cur.fetchone()
                if not descriptor_row or not bool(descriptor_row[0]):
                    raise ValueError(f"generation run {run['id']} conflicts with its registered descriptor")

                cur.execute(
                    extraction_sql,
                    (
                        run["id"],
                        run["task_name"],
                        run["prompt_version"],
                        run["model_id"],
                        provider,
                        run.get("provider_base_url"),
                        json_dumps(run.get("parameters")),
                        json_dumps(run.get("source_scope")),
                        run.get("status", "running"),
                        int(run.get("total_documents") or 0),
                    ),
                )
            self._commit()
        except Exception:
            self.conn.rollback()
            raise

    def register_prompt_version(self, prompt: Any) -> None:
        """Persist the exact prompt/examples used for reproducible runs."""
        sql = """
INSERT INTO knowledge_prompt_versions (
    id, prompt_version, task_name, description, examples_json, extraction_classes, policy_hash, updated_at
)
VALUES (%s, %s, %s, %s, %s::jsonb, %s, %s, now())
ON CONFLICT (prompt_version, policy_hash) DO UPDATE SET
    task_name = EXCLUDED.task_name,
    description = EXCLUDED.description,
    examples_json = EXCLUDED.examples_json,
    extraction_classes = EXCLUDED.extraction_classes,
    updated_at = now()
"""
        examples_json = json_dumps(prompt_examples_to_json(getattr(prompt, "examples", [])))
        policy_hash = str(getattr(prompt, "policy_hash", "") or "")
        with self._cursor() as cur:
            cur.execute(
                sql,
                (
                    new_uuid(),
                    prompt.version,
                    prompt.task,
                    prompt.description,
                    examples_json,
                    list(prompt.extraction_classes),
                    policy_hash,
                ),
            )
        self._commit()

    def insert_extraction_documents(self, documents: list[dict[str, Any]]) -> dict[str, str]:
        """Insert per-page document audit rows and return document_id -> audit id."""
        if not documents:
            return {}
        sql = """
INSERT INTO knowledge_extraction_documents (
    id, run_id, document_id, book_id, page_id, heading_id, source_hash,
    char_count, tokenizer, langextract_version, langextract_path, metadata
)
VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb)
ON CONFLICT (run_id, document_id) DO UPDATE SET
    book_id = EXCLUDED.book_id,
    page_id = EXCLUDED.page_id,
    heading_id = EXCLUDED.heading_id,
    source_hash = EXCLUDED.source_hash,
    char_count = EXCLUDED.char_count,
    tokenizer = EXCLUDED.tokenizer,
    langextract_version = EXCLUDED.langextract_version,
    langextract_path = EXCLUDED.langextract_path,
    metadata = EXCLUDED.metadata
RETURNING id, document_id
"""
        ids: dict[str, str] = {}
        with self._cursor() as cur:
            for document in documents:
                audit_id = document.get("id") or new_uuid()
                cur.execute(
                    sql,
                    (
                        audit_id,
                        document["run_id"],
                        document["document_id"],
                        document["book_id"],
                        document["page_id"],
                        document.get("heading_id"),
                        document["source_hash"],
                        int(document.get("char_count") or 0),
                        document.get("tokenizer", "RegexTokenizer"),
                        document.get("langextract_version"),
                        document.get("langextract_path"),
                        json_dumps(document.get("metadata")),
                    ),
                )
                row = cur.fetchone()
                ids[str(row[1])] = str(row[0])
        self._commit()
        return ids

    def insert_extraction_chunks(
        self,
        chunks: list[dict[str, Any]],
        document_audit_ids: dict[str, str] | None = None,
    ) -> dict[tuple[str, int, int], str]:
        """Insert chunk audit rows and return (document_id, pass, chunk) -> id."""
        if not chunks:
            return {}
        document_audit_ids = document_audit_ids or {}
        sql = """
INSERT INTO knowledge_extraction_chunks (
    id, run_id, extraction_document_id, document_id, chunk_index, pass_index,
    char_start, char_end, token_start, token_end, prompt_hash, output_hash,
    raw_output_path, parse_status, extracted_count, rejected_count, error_message, metadata
)
VALUES (
    %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb
)
ON CONFLICT (run_id, document_id, pass_index, chunk_index) DO UPDATE SET
    extraction_document_id = EXCLUDED.extraction_document_id,
    char_start = EXCLUDED.char_start,
    char_end = EXCLUDED.char_end,
    token_start = EXCLUDED.token_start,
    token_end = EXCLUDED.token_end,
    prompt_hash = EXCLUDED.prompt_hash,
    output_hash = EXCLUDED.output_hash,
    raw_output_path = EXCLUDED.raw_output_path,
    parse_status = EXCLUDED.parse_status,
    extracted_count = EXCLUDED.extracted_count,
    rejected_count = EXCLUDED.rejected_count,
    error_message = EXCLUDED.error_message,
    metadata = EXCLUDED.metadata
RETURNING id, document_id, pass_index, chunk_index
"""
        ids: dict[tuple[str, int, int], str] = {}
        with self._cursor() as cur:
            for chunk in chunks:
                audit_id = chunk.get("id") or new_uuid()
                document_id = str(chunk["document_id"])
                pass_index = int(chunk.get("pass_index") or 0)
                chunk_index = int(chunk.get("chunk_index") or 0)
                cur.execute(
                    sql,
                    (
                        audit_id,
                        chunk["run_id"],
                        chunk.get("extraction_document_id") or document_audit_ids.get(document_id),
                        document_id,
                        chunk_index,
                        pass_index,
                        int(chunk["char_start"]),
                        int(chunk["char_end"]),
                        chunk.get("token_start"),
                        chunk.get("token_end"),
                        chunk.get("prompt_hash"),
                        chunk.get("output_hash"),
                        chunk.get("raw_output_path"),
                        chunk.get("parse_status", "unknown"),
                        int(chunk.get("extracted_count") or 0),
                        int(chunk.get("rejected_count") or 0),
                        chunk.get("error_message"),
                        json_dumps(chunk.get("metadata")),
                    ),
                )
                row = cur.fetchone()
                ids[(str(row[1]), int(row[2]), int(row[3]))] = str(row[0])
        self._commit()
        return ids

    def finish_run(
        self,
        run_id: str,
        *,
        status: str,
        processed_documents: int,
        stored_mentions: int,
        errors: list[dict[str, Any]],
    ) -> None:
        sql = """
UPDATE knowledge_extraction_runs
SET status = %s,
    finished_at = now(),
    processed_documents = %s,
    stored_mentions = %s,
    errors = NULLIF(%s, '')::jsonb
WHERE id = %s
"""
        error_payload = json_dumps(errors) if errors else ""
        with self._cursor() as cur:
            cur.execute(sql, (status, processed_documents, stored_mentions, error_payload, run_id))
        self._commit()

    def insert_mention(self, record: dict[str, Any]) -> str:
        mention_id = record.get("id") or new_uuid()
        record["id"] = mention_id
        sql = """
INSERT INTO knowledge_mentions (
    id, run_id, book_id, page_id, heading_id, document_id, extraction_class,
    extraction_text, exact_quote, char_start, char_end, alignment_status,
    attributes, normalized_text, normalization_version, grounded, confidence, source_hash,
    source_span_id, token_start, token_end, extraction_index, group_index, pass_index
)
VALUES (
    %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s,
    %s::jsonb, %s, %s, %s, %s, %s,
    %s, %s, %s, %s, %s, %s
)
ON CONFLICT (run_id, book_id, page_id, extraction_class, char_start, char_end)
DO UPDATE SET
    extraction_text = EXCLUDED.extraction_text,
    exact_quote = EXCLUDED.exact_quote,
    alignment_status = EXCLUDED.alignment_status,
    attributes = EXCLUDED.attributes,
    normalized_text = EXCLUDED.normalized_text,
    normalization_version = EXCLUDED.normalization_version,
    grounded = EXCLUDED.grounded,
    confidence = EXCLUDED.confidence,
    source_hash = EXCLUDED.source_hash,
    token_start = EXCLUDED.token_start,
    token_end = EXCLUDED.token_end,
    extraction_index = EXCLUDED.extraction_index,
    group_index = EXCLUDED.group_index,
    pass_index = EXCLUDED.pass_index
WHERE knowledge_mentions.review_status = 'pending'
RETURNING id
"""
        with self._cursor() as cur:
            cur.execute(
                sql,
                (
                    mention_id,
                    record["run_id"],
                    record["book_id"],
                    record["page_id"],
                    record.get("heading_id"),
                    record["document_id"],
                    record["extraction_class"],
                    record["extraction_text"],
                    record["exact_quote"],
                    record["char_start"],
                    record["char_end"],
                    record["alignment_status"],
                    json_dumps(record.get("attributes")),
                    record["normalized_text"],
                    record["normalization_version"],
                    bool(record.get("grounded", True)),
                    record.get("confidence"),
                    record.get("source_hash"),
                    None,
                    record.get("token_start"),
                    record.get("token_end"),
                    record.get("extraction_index"),
                    record.get("group_index"),
                    record.get("pass_index"),
                ),
            )
            row = cur.fetchone()
            if row is None:
                cur.execute(
                    """
SELECT id
FROM knowledge_mentions
WHERE run_id = %s AND book_id = %s AND page_id = %s
  AND extraction_class = %s AND char_start = %s AND char_end = %s
""",
                    (
                        record["run_id"],
                        record["book_id"],
                        record["page_id"],
                        record["extraction_class"],
                        record["char_start"],
                        record["char_end"],
                    ),
                )
                existing = cur.fetchone()
                if existing is None:
                    raise RuntimeError("reviewed mention conflict disappeared")
                actual_mention_id = str(existing[0])
                record["id"] = actual_mention_id
                self._commit()
                return actual_mention_id
            actual_mention_id = str(row[0])
        record["id"] = actual_mention_id
        if not record.get("source_span_id"):
            record["source_span_id"] = new_uuid()
        source_span_id = self.insert_source_span(record, object_type="mention", object_id=actual_mention_id)
        record["source_span_id"] = source_span_id
        with self._cursor() as cur:
            cur.execute(
                """
UPDATE knowledge_mentions
SET source_span_id = %s
WHERE id = %s AND review_status = 'pending'
""",
                (source_span_id, actual_mention_id),
            )
        self._bind_mention_unit(actual_mention_id)
        self._commit()
        return actual_mention_id

    def _bind_mention_unit(self, mention_id: str) -> None:
        """Bind one page-relative span to exactly one Citable Unit.

        PostgreSQL character offsets match Python's Unicode code-point offsets.
        Ambiguous, stale, and cross-unit spans remain explicit fail-closed states;
        the extraction writer never guesses a unit with fuzzy matching.
        """
        sql = """
WITH source AS (
    SELECT mention.id,
           mention.book_id,
           mention.page_id,
           mention.char_start,
           mention.char_end,
           mention.exact_quote,
           mention.source_hash,
           mention.source_hash ~ '^[0-9A-Fa-f]{64}$' AS hash_well_formed,
           substring(
               page.content_text
               FROM mention.char_start + 1
               FOR mention.char_end - mention.char_start
           ) = mention.exact_quote AS quote_matches_page
    FROM knowledge_mentions mention
    JOIN book_pages page
      ON page.book_id = mention.book_id AND page.page_id = mention.page_id
    WHERE mention.id = %s
), candidates AS (
    SELECT unit.id AS unit_id,
           source.char_start - unit.source_char_start AS unit_char_start,
           source.char_end - unit.source_char_start AS unit_char_end,
           COUNT(*) OVER () AS candidate_count,
           ROW_NUMBER() OVER (
               ORDER BY CASE unit.lifecycle WHEN 'active' THEN 0 ELSE 1 END,
                        unit.source_char_end - unit.source_char_start,
                        unit.id
           ) AS candidate_rank
    FROM source
    JOIN citable_units unit
      ON unit.book_id = source.book_id
     AND unit.page_id = source.page_id
     AND unit.corpus = 'kitab'
     AND unit.content_role = 'book_page'
     AND unit.provenance_class = 'source'
     AND unit.lifecycle IN ('active', 'superseded')
     AND unit.source_document_hash IS NOT NULL
     AND encode(unit.source_document_hash, 'hex') = lower(source.source_hash)
     AND unit.source_char_start <= source.char_start
     AND unit.source_char_end >= source.char_end
    WHERE source.hash_well_formed AND source.quote_matches_page
), overlap AS (
    SELECT COUNT(unit.id) AS overlap_count
    FROM source
    LEFT JOIN citable_units unit
      ON unit.book_id = source.book_id
     AND unit.page_id = source.page_id
     AND unit.corpus = 'kitab'
     AND unit.content_role = 'book_page'
     AND unit.provenance_class = 'source'
     AND unit.lifecycle IN ('active', 'superseded')
     AND unit.source_document_hash IS NOT NULL
     AND encode(unit.source_document_hash, 'hex') = lower(source.source_hash)
     AND unit.source_char_start < source.char_end
     AND unit.source_char_end > source.char_start
), resolved AS (
    SELECT source.id,
           CASE WHEN candidate.candidate_count = 1 THEN candidate.unit_id END AS unit_id,
           CASE WHEN candidate.candidate_count = 1 THEN candidate.unit_char_start END AS unit_char_start,
           CASE WHEN candidate.candidate_count = 1 THEN candidate.unit_char_end END AS unit_char_end,
           CASE
               WHEN NOT source.hash_well_formed OR NOT source.quote_matches_page THEN 'stale'
               WHEN candidate.candidate_count = 1 THEN 'bound'
               WHEN candidate.candidate_count > 1 THEN 'ambiguous'
               WHEN overlap.overlap_count > 1 THEN 'cross_unit'
               ELSE 'missing'
           END AS binding_status
    FROM source
    LEFT JOIN candidates candidate ON candidate.candidate_rank = 1
    CROSS JOIN overlap
)
UPDATE knowledge_mentions mention
SET unit_id = resolved.unit_id,
    unit_char_start = resolved.unit_char_start,
    unit_char_end = resolved.unit_char_end,
    unit_binding_status = resolved.binding_status,
    unit_binding_version = 1,
    unit_source_hash = CASE WHEN resolved.binding_status = 'bound' THEN mention.source_hash ELSE NULL END
FROM resolved
WHERE mention.id = resolved.id AND mention.review_status = 'pending'
"""
        with self._cursor() as cur:
            cur.execute(sql, (mention_id,))

    def insert_source_span(self, record: dict[str, Any], *, object_type: str, object_id: str) -> str:
        source_span_id = str(record.get("source_span_id") or new_uuid())
        record["source_span_id"] = source_span_id
        sql = """
INSERT INTO knowledge_source_spans (
    id, run_id, source_object_type, source_object_id, book_id, page_id,
    heading_id, document_id, extraction_class, exact_quote, char_start, char_end,
    token_start, token_end, alignment_status, source_hash, attributes
)
VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb)
ON CONFLICT (run_id, source_object_type, source_object_id)
WHERE source_object_id IS NOT NULL
DO UPDATE SET
    book_id = EXCLUDED.book_id,
    page_id = EXCLUDED.page_id,
    heading_id = EXCLUDED.heading_id,
    document_id = EXCLUDED.document_id,
    extraction_class = EXCLUDED.extraction_class,
    exact_quote = EXCLUDED.exact_quote,
    char_start = EXCLUDED.char_start,
    char_end = EXCLUDED.char_end,
    token_start = EXCLUDED.token_start,
    token_end = EXCLUDED.token_end,
    alignment_status = EXCLUDED.alignment_status,
    source_hash = EXCLUDED.source_hash,
    attributes = EXCLUDED.attributes
RETURNING id
"""
        with self._cursor() as cur:
            cur.execute(
                sql,
                (
                    source_span_id,
                    record["run_id"],
                    object_type,
                    object_id,
                    record["book_id"],
                    record["page_id"],
                    record.get("heading_id"),
                    record["document_id"],
                    record.get("extraction_class"),
                    record["exact_quote"],
                    record["char_start"],
                    record["char_end"],
                    record.get("token_start"),
                    record.get("token_end"),
                    record.get("alignment_status", "unknown"),
                    record.get("source_hash"),
                    json_dumps(record.get("attributes")),
                ),
            )
            row = cur.fetchone()
        return str(row[0])

    def insert_extraction_rejections(
        self,
        failures: list[dict[str, Any]],
        chunk_ids: dict[tuple[str, int, int], str] | None = None,
    ) -> int:
        if not failures:
            return 0
        chunk_ids = chunk_ids or {}
        sql = """
INSERT INTO knowledge_extraction_rejections (
    id, run_id, chunk_id, book_id, page_id, heading_id, document_id,
    extraction_class, extraction_text, exact_quote, char_start, char_end,
    alignment_status, code, message, attributes, source_hash, raw_output_path
)
VALUES (
    %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb, %s, %s
)
"""
        inserted = 0
        with self._cursor() as cur:
            for failure in failures:
                document_id = failure.get("document_id")
                chunk_key = None
                if document_id is not None and failure.get("chunk_index") is not None:
                    chunk_key = (
                        str(document_id),
                        int(failure.get("pass_index") or 0),
                        int(failure.get("chunk_index") or 0),
                    )
                cur.execute(
                    sql,
                    (
                        failure.get("id") or new_uuid(),
                        failure.get("run_id"),
                        chunk_ids.get(chunk_key) if chunk_key else None,
                        failure["book_id"],
                        failure["page_id"],
                        failure.get("heading_id"),
                        failure.get("document_id"),
                        failure.get("extraction_class"),
                        failure.get("extraction_text"),
                        failure.get("exact_quote"),
                        failure.get("char_start"),
                        failure.get("char_end"),
                        failure.get("alignment_status"),
                        failure["code"],
                        failure["message"],
                        json_dumps(failure.get("attributes")),
                        failure.get("source_hash"),
                        failure.get("raw_output_path"),
                    ),
                )
                inserted += 1
        self._commit()
        return inserted

    def insert_mentions_with_candidates(self, records: list[dict[str, Any]]) -> int:
        self._verify_mention_generation_identities(records)

        stored = 0
        pages: dict[tuple[int, int], list[dict[str, Any]]] = {}
        for record in records:
            key = (int(record["book_id"]), int(record["page_id"]))
            pages.setdefault(key, []).append(record)

        for page_records in pages.values():
            self._defer_commit = True
            try:
                for record in page_records:
                    mention_id = self.insert_mention(record)
                    record["id"] = mention_id
                    stored += 1
                    if record["extraction_class"] in {"relation", "claim"}:
                        self.insert_relation_or_claim(record)
                    else:
                        self.upsert_candidates_for_mention(record)
                self.conn.commit()
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._defer_commit = False
        return stored

    def _verify_mention_generation_identities(self, records: list[dict[str, Any]]) -> None:
        identities: dict[str, tuple[str, str]] = {}
        for index, record in enumerate(records, start=1):
            run_id, model_id, prompt_version = machine_generation_tuple(record, index)
            previous = identities.get(run_id)
            if previous is not None and previous != (model_id, prompt_version):
                raise ValueError(f"line {index}: generation run {run_id} has a conflicting descriptor")
            identities[run_id] = (model_id, prompt_version)

        with self._cursor() as cur:
            for run_id, (model_id, prompt_version) in identities.items():
                cur.execute(
                    """
SELECT EXISTS (
    SELECT 1
    FROM generation_runs gr
    JOIN knowledge_extraction_runs er ON er.id = gr.id
    WHERE gr.id = %s
      AND gr.model_id = %s
      AND gr.prompt_version = %s
      AND er.model_id = gr.model_id
      AND er.prompt_version = gr.prompt_version
)
""",
                    (run_id, model_id, prompt_version),
                )
                row = cur.fetchone()
                if not row or not bool(row[0]):
                    raise ValueError(f"generation run {run_id} is not registered with this model/prompt tuple")

    def insert_relation_or_claim(self, mention: dict[str, Any]) -> None:
        attrs = mention.get("attributes") or {}
        if not isinstance(attrs, dict):
            attrs = {}
        extraction_class = str(mention["extraction_class"])
        if extraction_class == "relation":
            self.insert_relation_candidate(mention, attrs)
        elif extraction_class == "claim":
            self.insert_claim_candidate(mention, attrs)

    def insert_relation_candidate(self, mention: dict[str, Any], attrs: dict[str, Any]) -> None:
        predicate = str(attrs.get("predicate") or "mentions").strip() or "mentions"
        subject_text = str(attrs.get("subject_text") or attrs.get("subject") or "").strip()
        object_literal = str(attrs.get("object_text") or attrs.get("object") or "").strip()
        if not object_literal:
            object_literal = str(subject_text or mention["exact_quote"]).strip()
        certainty = str(attrs.get("certainty") or "explicit").strip()
        if certainty not in {"explicit", "probable", "ambiguous"}:
            certainty = "ambiguous"
        risk_level = risk_level_for_relation(predicate, attrs)
        sql = """
INSERT INTO knowledge_relations (
    id, run_id, predicate, object_literal, evidence_mention_id,
    evidence_quote, certainty, attributes, source_span_id,
    subject_text, object_text, risk_level, requires_scholar_review
)
VALUES (%s, %s, %s, %s, %s, %s, %s, %s::jsonb, %s, %s, %s, %s, %s)
"""
        with self._cursor() as cur:
            cur.execute(
                sql,
                (
                    new_uuid(),
                    mention["run_id"],
                    predicate,
                    object_literal,
                    mention["id"],
                    mention["exact_quote"],
                    certainty,
                    json_dumps(attrs),
                    mention.get("source_span_id"),
                    subject_text or None,
                    object_literal or None,
                    risk_level,
                    True,
                ),
            )
        self._commit()

    def insert_claim_candidate(self, mention: dict[str, Any], attrs: dict[str, Any]) -> None:
        claim_type = str(attrs.get("claim_type") or attrs.get("predicate") or "statement").strip()
        predicate = str(attrs.get("predicate") or claim_type).strip() or "statement"
        subject_text = str(attrs.get("subject_text") or attrs.get("subject") or "").strip()
        object_text = str(attrs.get("object_text") or attrs.get("object") or "").strip()
        certainty = str(attrs.get("certainty") or "explicit").strip()
        if certainty not in {"explicit", "probable", "ambiguous"}:
            certainty = "ambiguous"
        risk_level = risk_level_for_claim(claim_type, attrs)
        sql = """
INSERT INTO knowledge_claims (
    id, run_id, claim_type, claim_text_ar, claim_text_id,
    evidence_mention_id, evidence_quote, attributes, source_span_id,
    subject_text, object_text, predicate, risk_level, certainty, requires_scholar_review
)
VALUES (%s, %s, %s, %s, %s, %s, %s, %s::jsonb, %s, %s, %s, %s, %s, %s, %s)
"""
        with self._cursor() as cur:
            cur.execute(
                sql,
                (
                    new_uuid(),
                    mention["run_id"],
                    claim_type,
                    mention["exact_quote"],
                    attrs.get("claim_text_id"),
                    mention["id"],
                    mention["exact_quote"],
                    json_dumps(attrs),
                    mention.get("source_span_id"),
                    subject_text or None,
                    object_text or None,
                    predicate,
                    risk_level,
                    certainty,
                    True,
                ),
            )
        self._commit()

    def upsert_candidates_for_mention(self, mention: dict[str, Any]) -> None:
        normalized = normalized_key(str(mention.get("extraction_text") or ""))
        if not normalized:
            return
        entity_type = entity_type_for_class(str(mention["extraction_class"]))
        if entity_type in {"person_reference", "theonym"}:
            return
        existing = self.find_entities_by_alias(entity_type, normalized)
        mention_review = str(mention.get("review_status") or "pending")
        if existing:
            review = "ambiguous" if len(existing) > 1 or mention_review == "ambiguous" else "pending"
            score = 0.55 if review == "ambiguous" else 0.85
            for entity in existing:
                self.insert_entity_candidate(
                    mention_id=str(mention["id"]),
                    entity_id=str(entity["id"]),
                    score=score,
                    strategy="normalized_alias",
                    review_status=review,
                    reasons={"normalized_alias": normalized, "candidate_count": len(existing)},
                )
            return

        if mention_review == "ambiguous":
            return

        entity_id = self.create_entity_from_mention(mention, entity_type, normalized)
        self.insert_entity_candidate(
            mention_id=str(mention["id"]),
            entity_id=entity_id,
            score=0.75,
            strategy="normalized_alias_new_entity",
            review_status="pending",
            reasons={"normalized_alias": normalized, "created_from_mention": True},
        )

    def find_entities_by_alias(self, entity_type: str, normalized_alias: str) -> list[dict[str, Any]]:
        sql = """
SELECT e.id, e.entity_type, e.canonical_name_ar, a.normalized_alias
FROM knowledge_entities e
JOIN knowledge_entity_aliases a ON a.entity_id = e.id
WHERE e.entity_type = %s AND a.language = 'ar' AND a.normalized_alias = %s
ORDER BY e.created_at ASC
LIMIT 10
"""
        with self._cursor(dict_rows=True) as cur:
            cur.execute(sql, (entity_type, normalized_alias))
            return list(cur.fetchall())

    def create_entity_from_mention(
        self,
        mention: dict[str, Any],
        entity_type: str,
        normalized_alias: str,
    ) -> str:
        entity_id = new_uuid()
        text = str(mention["extraction_text"])
        sql = """
INSERT INTO knowledge_entities (
    id, entity_type, canonical_name_ar, normalized_name_ar,
    normalization_version, created_from_mention_id
)
VALUES (%s, %s, %s, %s, %s, %s)
"""
        with self._cursor() as cur:
            cur.execute(sql, (entity_id, entity_type, text, normalized_alias, PROFILE_VERSION, mention["id"]))
        self.upsert_entity_label(entity_id, "ar", text, "primary", "langextract")
        self.upsert_entity_alias(
            entity_id,
            alias_text=text,
            normalized_alias=normalized_alias,
            language="ar",
            alias_type="extracted",
            source_mention_id=str(mention["id"]),
            review_status="pending",
        )
        self._commit()
        return entity_id

    def upsert_entity_label(
        self,
        entity_id: str,
        lang: str,
        label: str,
        label_kind: str,
        source: str,
    ) -> None:
        sql = """
INSERT INTO knowledge_entity_labels (id, entity_id, lang, label, label_kind, source)
VALUES (%s, %s, %s, %s, %s, %s)
ON CONFLICT (entity_id, lang, label_kind, label) DO NOTHING
"""
        with self._cursor() as cur:
            cur.execute(sql, (new_uuid(), entity_id, lang, label, label_kind, source))

    def upsert_entity_alias(
        self,
        entity_id: str,
        *,
        alias_text: str,
        normalized_alias: str,
        language: str,
        alias_type: str,
        source_mention_id: str | None,
        review_status: str,
    ) -> None:
        sql = """
INSERT INTO knowledge_entity_aliases (
    id, entity_id, alias_text, normalized_alias, language,
    normalization_version, alias_type, source_mention_id
)
VALUES (%s, %s, %s, %s, %s, %s, %s, %s)
ON CONFLICT (entity_id, normalized_alias, language, alias_type) DO UPDATE SET
    alias_text = EXCLUDED.alias_text,
    normalization_version = EXCLUDED.normalization_version,
    source_mention_id = COALESCE(knowledge_entity_aliases.source_mention_id, EXCLUDED.source_mention_id)
WHERE knowledge_entity_aliases.review_status = 'pending'
"""
        with self._cursor() as cur:
            cur.execute(
                sql,
                (
                    new_uuid(),
                    entity_id,
                    alias_text,
                    normalized_alias,
                    language,
                    PROFILE_VERSION,
                    alias_type,
                    source_mention_id,
                ),
            )

    def insert_entity_candidate(
        self,
        *,
        mention_id: str,
        entity_id: str,
        score: float,
        strategy: str,
        review_status: str,
        reasons: dict[str, Any],
    ) -> None:
        sql = """
INSERT INTO knowledge_entity_candidates (
    mention_id, entity_id, score, strategy, reasons
)
VALUES (%s, %s, %s, %s, %s::jsonb)
ON CONFLICT (mention_id, entity_id, strategy) DO UPDATE SET
    score = EXCLUDED.score,
    reasons = EXCLUDED.reasons
WHERE knowledge_entity_candidates.review_status = 'pending'
"""
        with self._cursor() as cur:
            cur.execute(
                sql,
                (mention_id, entity_id, score, strategy, json_dumps(reasons)),
            )
        self._commit()

    def load_mentions_for_run(self, run_id: str) -> list[dict[str, Any]]:
        sql = """
SELECT 'knowledge_mention' AS kind,
       run_id,
       book_id,
       page_id,
       heading_id,
       document_id,
       extraction_class,
       extraction_text,
       exact_quote,
       char_start,
       char_end,
       alignment_status,
       attributes,
       normalized_text,
       normalization_version,
       grounded,
       confidence,
       review_status,
       source_hash,
       source_span_id,
       unit_id,
       unit_char_start,
       unit_char_end,
       unit_binding_status,
       unit_binding_version,
       unit_source_hash,
       token_start,
       token_end,
       extraction_index,
       group_index,
       pass_index
FROM knowledge_mentions
WHERE run_id = %s
ORDER BY book_id, page_id, char_start, id
"""
        with self._cursor(dict_rows=True) as cur:
            cur.execute(sql, (run_id,))
            return [dict(row) for row in cur.fetchall()]


def prompt_examples_to_json(examples: list[Any]) -> list[dict[str, Any]]:
    payload: list[dict[str, Any]] = []
    for example in examples:
        payload.append(
            {
                "text": getattr(example, "text", ""),
                "extractions": [
                    {
                        "extraction_class": getattr(extraction, "extraction_class", ""),
                        "extraction_text": getattr(extraction, "extraction_text", ""),
                        "attributes": getattr(extraction, "attributes", None) or {},
                    }
                    for extraction in getattr(example, "extractions", []) or []
                ],
            }
        )
    return payload


def risk_level_for_relation(predicate: str, attrs: dict[str, Any]) -> str:
    explicit = str(attrs.get("risk_level") or "").strip().lower()
    if explicit in {"low", "medium", "high"}:
        return explicit
    high_risk_predicates = {
        "permits",
        "prohibits",
        "requires",
        "invalidates",
        "authenticates",
        "weakens",
        "jarh",
        "tadil",
        "ijma",
    }
    return "high" if predicate in high_risk_predicates else "medium"


def risk_level_for_claim(claim_type: str, attrs: dict[str, Any]) -> str:
    explicit = str(attrs.get("risk_level") or "").strip().lower()
    if explicit in {"low", "medium", "high"}:
        return explicit
    high_risk_types = {
        "fiqh",
        "aqidah",
        "sanad",
        "jarh_tadil",
        "ijma",
        "halal_haram",
        "normative",
    }
    return "high" if claim_type in high_risk_types else "high"
