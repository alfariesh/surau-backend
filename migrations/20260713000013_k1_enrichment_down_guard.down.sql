UPDATE knowledge_mentions mention
SET unit_id = NULL,
    unit_char_start = NULL,
    unit_char_end = NULL,
    unit_binding_status = 'pending',
    unit_source_hash = NULL
FROM citable_units unit
WHERE mention.unit_id = unit.id
  AND unit.corpus = 'kitab'
  AND NOT (unit.content_role = 'book_page' AND unit.language = 'ar');

DO $$
BEGIN
    PERFORM set_config('surau.registry_writer', 'unit-service', true);
    DELETE FROM citable_units
    WHERE corpus = 'kitab'
      AND NOT (content_role = 'book_page' AND language = 'ar');
END;
$$;
