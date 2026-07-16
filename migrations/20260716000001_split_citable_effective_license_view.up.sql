-- Keep the effective-license contract virtual while separating its mutually
-- exclusive kitab/other and Quran branches. Public Quran reads can now perform
-- a targeted lookup by Citable Unit id without expanding the entire mixed
-- corpus view for every requested ayah. The output columns and license rules
-- are unchanged, so the old application remains compatible during rollout.
CREATE OR REPLACE VIEW citable_units_with_effective_license AS
SELECT u.id,
       u.corpus,
       u.book_id,
       u.heading_id,
       u.page_id,
       u.anchor,
       u.lifecycle,
       u.license_status,
       COALESCE(u.license_status, b.license_status) AS effective_license_status,
       CASE
           WHEN u.license_status IS NOT NULL THEN 'unit_override'::TEXT
           WHEN u.corpus = 'kitab' AND b.license_status IS NOT NULL THEN 'edition'::TEXT
           ELSE NULL::TEXT
       END AS license_source
FROM citable_units u
LEFT JOIN books b ON b.id = u.book_id AND u.corpus = 'kitab'
WHERE u.corpus <> 'quran'

UNION ALL

SELECT u.id,
       u.corpus,
       u.book_id,
       u.heading_id,
       u.page_id,
       u.anchor,
       u.lifecycle,
       u.license_status,
       COALESCE(
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
       ) AS effective_license_status,
       CASE
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
LEFT JOIN quran_citable_unit_bindings qb ON qb.unit_id = u.id
LEFT JOIN quran_translation_sources ts ON ts.id = qb.translation_source_id
LEFT JOIN quran_transliteration_sources xs ON xs.id = qb.transliteration_source_id
LEFT JOIN quran_script_sources ss ON ss.id = 'qpc-hafs' AND qb.role = 'primary_text'
WHERE u.corpus = 'quran';
