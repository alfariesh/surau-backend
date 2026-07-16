\set ON_ERROR_STOP on

-- This script mirrors the reader_minimal database stages with concrete,
-- production-safe SELECT parameters. The caller supplies page_number.
BEGIN READ ONLY;
SET LOCAL statement_timeout = '20s';
SET LOCAL lock_timeout = '250ms';
SET LOCAL idle_in_transaction_session_timeout = '30s';

SELECT COALESCE((
    SELECT s.id
    FROM public_quran_translation_sources s
    WHERE s.lang = 'id'
    ORDER BY CASE WHEN s.id = 'kemenag-id-translation' THEN 0 ELSE 1 END,
             s.coverage_count DESC,
             s.name ASC,
             s.id ASC
    LIMIT 1
), '') AS translation_source_id \gset

SELECT COALESCE((
    SELECT s.id
    FROM public_quran_transliteration_sources s
    WHERE s.lang = 'id'
    ORDER BY CASE WHEN s.id = 'kemenag-id-latin' THEN 0 ELSE 1 END,
             s.coverage_count DESC,
             s.name ASC,
             s.id ASC
    LIMIT 1
), '') AS transliteration_source_id \gset

SELECT quote_literal(COALESCE(
           array_agg(page_ayah.ayah_key ORDER BY page_ayah.surah_id, page_ayah.ayah_number),
           ARRAY[]::text[]
       ))
       AS page_ayah_keys
FROM quran_ayahs page_ayah
WHERE page_ayah.page_number = :page_number \gset

\echo main_reader_query_page_:page_number
EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON)
SELECT a.surah_id,
       a.ayah_number,
       a.ayah_key,
       a.text_qpc_hafs,
       a.text_imlaei_simple,
       a.search_text,
       a.script_type,
       a.font_family,
       a.page_number,
       a.juz_number,
       a.hizb_number,
       a.metadata,
       a.updated_at,
       t.source_id,
       t.lang,
       t.text,
       t.footnotes,
       t.chunks,
       t.metadata,
       t.updated_at,
       tn.source_id,
       tn.lang,
       tn.text,
       tn.metadata,
       tn.updated_at,
       COALESCE(ta.available_langs, ARRAY[]::text[]),
       a.updated_at
FROM quran_ayahs a
JOIN public_quran_script_sources script_source
  ON script_source.id = 'qpc-hafs'
LEFT JOIN quran_ayah_translations t
  ON t.surah_id = a.surah_id
 AND t.ayah_number = a.ayah_number
 AND t.lang = 'id'
 AND t.source_id = :'translation_source_id'
 AND EXISTS (
     SELECT 1
     FROM public_quran_translation_sources permitted
     WHERE permitted.id = t.source_id
       AND permitted.lang = t.lang
 )
LEFT JOIN quran_ayah_transliterations tn
  ON tn.surah_id = a.surah_id
 AND tn.ayah_number = a.ayah_number
 AND tn.lang = 'id'
 AND tn.source_id = :'transliteration_source_id'
 AND EXISTS (
     SELECT 1
     FROM public_quran_transliteration_sources permitted
     WHERE permitted.id = tn.source_id
       AND permitted.lang = tn.lang
 )
LEFT JOIN LATERAL (
    SELECT array_agg(DISTINCT available.lang ORDER BY available.lang) AS available_langs
    FROM quran_ayah_translations available
    JOIN public_quran_translation_sources permitted
      ON permitted.id = available.source_id
     AND permitted.lang = available.lang
    WHERE available.surah_id = a.surah_id
      AND available.ayah_number = a.ayah_number
) ta ON true
WHERE a.page_number = :page_number
ORDER BY a.surah_id, a.ayah_number;

\echo derived_state_query_page_:page_number
EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON)
SELECT a.ayah_key,
       s.units_derived_at IS NOT NULL AND s.units_stale_at IS NULL
FROM quran_ayahs a
JOIN quran_surahs s ON s.surah_id = a.surah_id
WHERE a.ayah_key = ANY(ARRAY(
    SELECT page_ayah.ayah_key
    FROM quran_ayahs page_ayah
    WHERE page_ayah.page_number = :page_number
    ORDER BY page_ayah.surah_id, page_ayah.ayah_number
)::text[]);

\echo citable_hydration_query_page_:page_number
EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON)
SELECT a.ayah_key,
       u.id::text,
       u.anchor,
       u.parent_unit_id::text,
       u.marker,
       u.text,
       b.role,
       b.translation_source_id,
       b.transliteration_source_id,
       b.footnote_key
FROM quran_ayahs a
JOIN quran_surahs s
  ON s.surah_id = a.surah_id
 AND s.units_stale_at IS NULL
JOIN quran_citable_unit_bindings b
  ON b.surah_id = a.surah_id
 AND b.ayah_number = a.ayah_number
JOIN citable_units u
  ON u.id = b.unit_id
 AND u.corpus = 'quran'
 AND u.lifecycle = 'active'
JOIN LATERAL (
    SELECT license.id
    FROM citable_units_with_effective_license license
    WHERE license.id = u.id
      AND license.corpus = 'quran'
      AND license.effective_license_status = 'permitted'
    LIMIT 1
) license ON true
LEFT JOIN quran_ayah_translations t
  ON t.source_id = b.translation_source_id
 AND t.surah_id = b.surah_id
 AND t.ayah_number = b.ayah_number
LEFT JOIN quran_ayah_transliterations x
  ON x.source_id = b.transliteration_source_id
 AND x.surah_id = b.surah_id
 AND x.ayah_number = b.ayah_number
WHERE a.ayah_key = ANY(ARRAY(
    SELECT page_ayah.ayah_key
    FROM quran_ayahs page_ayah
    WHERE page_ayah.page_number = :page_number
    ORDER BY page_ayah.surah_id, page_ayah.ayah_number
)::text[])
  AND (
      (b.role = 'primary_text' AND b.source_updated_at = a.updated_at AND u.text = a.text_qpc_hafs)
      OR (b.role = 'translation' AND b.source_updated_at = t.updated_at AND u.text = t.text)
      OR (b.role = 'footnote' AND b.source_updated_at = t.updated_at)
      OR (b.role = 'transliteration' AND b.source_updated_at = x.updated_at AND u.text = x.text)
  )
ORDER BY b.surah_id, b.ayah_number, b.ordinal;

-- pgx caches statements per connection. PostgreSQL may switch a parameterized
-- statement from a custom to a generic plan after its first five executions.
-- Execute both choices against the exact same bounded page array so a
-- connection-specific plan regression cannot hide behind an average.
PREPARE quran_citable_hydration(text[]) AS
SELECT a.ayah_key,
       u.id::text,
       u.anchor,
       u.parent_unit_id::text,
       u.marker,
       u.text,
       b.role,
       b.translation_source_id,
       b.transliteration_source_id,
       b.footnote_key
FROM quran_ayahs a
JOIN quran_surahs s
  ON s.surah_id = a.surah_id
 AND s.units_stale_at IS NULL
JOIN quran_citable_unit_bindings b
  ON b.surah_id = a.surah_id
 AND b.ayah_number = a.ayah_number
JOIN citable_units u
  ON u.id = b.unit_id
 AND u.corpus = 'quran'
 AND u.lifecycle = 'active'
JOIN LATERAL (
    SELECT license.id
    FROM citable_units_with_effective_license license
    WHERE license.id = u.id
      AND license.corpus = 'quran'
      AND license.effective_license_status = 'permitted'
    LIMIT 1
) license ON true
LEFT JOIN quran_ayah_translations t
  ON t.source_id = b.translation_source_id
 AND t.surah_id = b.surah_id
 AND t.ayah_number = b.ayah_number
LEFT JOIN quran_ayah_transliterations x
  ON x.source_id = b.transliteration_source_id
 AND x.surah_id = b.surah_id
 AND x.ayah_number = b.ayah_number
WHERE a.ayah_key = ANY($1::text[])
  AND (
      (b.role = 'primary_text' AND b.source_updated_at = a.updated_at AND u.text = a.text_qpc_hafs)
      OR (b.role = 'translation' AND b.source_updated_at = t.updated_at AND u.text = t.text)
      OR (b.role = 'footnote' AND b.source_updated_at = t.updated_at)
      OR (b.role = 'transliteration' AND b.source_updated_at = x.updated_at AND u.text = x.text)
  )
ORDER BY b.surah_id, b.ayah_number, b.ordinal;

SET LOCAL plan_cache_mode = 'force_custom_plan';
\echo citable_hydration_custom_plan_page_:page_number
EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON)
EXECUTE quran_citable_hydration(:page_ayah_keys);

SET LOCAL plan_cache_mode = 'force_generic_plan';
\echo citable_hydration_generic_plan_page_:page_number
EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON)
EXECUTE quran_citable_hydration(:page_ayah_keys);

DEALLOCATE quran_citable_hydration;

ROLLBACK;
