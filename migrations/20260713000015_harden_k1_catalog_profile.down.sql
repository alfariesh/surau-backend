CREATE OR REPLACE VIEW public_book_interpretive_citable_units AS
SELECT unit.*
FROM citable_units unit
JOIN public_book_publications publication ON publication.book_id = unit.book_id
JOIN books book ON book.id = unit.book_id
WHERE unit.corpus = 'kitab'
  AND unit.lifecycle = 'active'
  AND unit.interpretive_retrieval_eligible
  AND (unit.license_status IS NULL OR unit.license_status = 'permitted')
  AND book.units_derived_at IS NOT NULL
  AND book.units_stale_at IS NULL
  AND book.units_derivation_profile_version = 2;

-- The repaired parent metadata is intentionally not corrupted on rollback.
-- Mark profile-v3 books stale so the prior binary must prove its own profile
-- before the public view can expose them again.
UPDATE books
SET units_derivation_profile_version = 2,
    units_stale_at = COALESCE(units_stale_at, clock_timestamp())
WHERE units_derivation_profile_version = 3;
