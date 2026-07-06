-- Denormalize ayah-coverage onto the small source tables so the public read path's
-- default-source resolution becomes a tiny lookup instead of a full GROUP BY aggregate
-- over quran_ayah_translations / quran_ayah_transliterations on EVERY reader request
-- that does not pin a translation_source (the common case).
ALTER TABLE quran_translation_sources
    ADD COLUMN IF NOT EXISTS coverage_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE quran_transliteration_sources
    ADD COLUMN IF NOT EXISTS coverage_count INTEGER NOT NULL DEFAULT 0;

-- One-time backfill from the current aggregate; the importer keeps it fresh afterwards.
UPDATE quran_translation_sources s
SET coverage_count = COALESCE(c.n, 0)
FROM (
    SELECT source_id, COUNT(*)::int AS n
    FROM quran_ayah_translations
    GROUP BY source_id
) c
WHERE c.source_id = s.id;

UPDATE quran_transliteration_sources s
SET coverage_count = COALESCE(c.n, 0)
FROM (
    SELECT source_id, COUNT(*)::int AS n
    FROM quran_ayah_transliterations
    GROUP BY source_id
) c
WHERE c.source_id = s.id;
