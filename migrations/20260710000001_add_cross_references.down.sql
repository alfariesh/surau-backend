-- Roll back only the B-3 EXPAND objects. The legacy Quran reference table is
-- intentionally untouched, so the pre-B-3 reader remains usable.

DROP TRIGGER IF EXISTS trg_quran_book_references_cross_reference_guard ON quran_book_references;
DROP FUNCTION IF EXISTS legacy_quran_reference_write_guard();

DROP TABLE IF EXISTS cross_reference_registry_state;
DROP TABLE IF EXISTS quran_cross_reference_bridge;
DROP TABLE IF EXISTS cross_references;
DROP FUNCTION IF EXISTS cross_reference_anchor_visible(TEXT);
DROP FUNCTION IF EXISTS cross_reference_anchor_point_visible(TEXT);
DROP FUNCTION IF EXISTS cross_reference_registry_guard();
