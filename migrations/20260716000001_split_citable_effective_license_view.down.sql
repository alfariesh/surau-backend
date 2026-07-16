-- Restore the single-view shape that preceded the performance rewrite. The
-- effective license semantics and output columns are identical in both forms.
CREATE OR REPLACE VIEW citable_units_with_effective_license AS
SELECT u.id,
       u.corpus,
       u.book_id,
       u.heading_id,
       u.page_id,
       u.anchor,
       u.lifecycle,
       u.license_status,
       CASE
           WHEN u.corpus = 'quran' THEN COALESCE(
               ts.license_status,
               xs.license_status,
               CASE
                   WHEN ss.license_status = 'permitted' THEN 'permitted'
                   WHEN ss.license_grandfathered_at IS NOT NULL
                        AND ss.license_status <> 'restricted'
                        AND ss.checksum IS NOT DISTINCT FROM ss.license_grandfathered_checksum
                       THEN 'permitted'
                   ELSE ss.license_status
               END
           )
           ELSE COALESCE(u.license_status, b.license_status)
       END AS effective_license_status,
       CASE
           WHEN u.corpus <> 'quran' AND u.license_status IS NOT NULL THEN 'unit_override'::TEXT
           WHEN u.corpus = 'kitab' AND b.license_status IS NOT NULL THEN 'edition'::TEXT
           WHEN qb.role IN ('translation', 'footnote') THEN 'quran_translation_source'::TEXT
           WHEN qb.role = 'transliteration' THEN 'quran_transliteration_source'::TEXT
           WHEN qb.role = 'primary_text' AND ss.license_grandfathered_at IS NOT NULL
               AND ss.license_status <> 'restricted'
               AND ss.checksum IS NOT DISTINCT FROM ss.license_grandfathered_checksum
               THEN 'quran_script_grandfather'::TEXT
           WHEN qb.role = 'primary_text' THEN 'quran_script_source'::TEXT
           ELSE NULL::TEXT
       END AS license_source
FROM citable_units u
LEFT JOIN books b ON b.id = u.book_id AND u.corpus = 'kitab'
LEFT JOIN quran_citable_unit_bindings qb ON qb.unit_id = u.id AND u.corpus = 'quran'
LEFT JOIN quran_translation_sources ts ON ts.id = qb.translation_source_id
LEFT JOIN quran_transliteration_sources xs ON xs.id = qb.transliteration_source_id
LEFT JOIN quran_script_sources ss ON ss.id = 'qpc-hafs' AND qb.role = 'primary_text';
