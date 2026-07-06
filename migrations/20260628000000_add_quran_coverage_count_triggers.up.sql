-- Keep coverage_count on quran_translation_sources / quran_transliteration_sources
-- consistent with the real row count via triggers, instead of the one-shot recompute
-- the importer used to run. That recompute drifted whenever rows were deleted outside
-- an import (ON DELETE CASCADE from quran_ayahs, admin edits, integration-test cleanup),
-- and it ran outside the import transaction. The read path
-- (defaultTranslationSourceID / defaultTransliterationSourceID) orders by
-- coverage_count DESC on every request, so a stale count silently picks a source that
-- is NOT the most complete.
--
-- Triggers fire only on genuine INSERT / DELETE. The importer upserts with
-- ON CONFLICT DO UPDATE, which takes the UPDATE path (no INSERT trigger) on re-import,
-- so an unchanged re-import does not double-count and a brand-new row increments once.
-- Deletes decrement (floored at 0), so cascade/admin deletes stay accurate too.

CREATE OR REPLACE FUNCTION quran_translation_coverage_sync() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE quran_translation_sources
        SET coverage_count = coverage_count + 1
        WHERE id = NEW.source_id;

        RETURN NEW;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE quran_translation_sources
        SET coverage_count = GREATEST(0, coverage_count - 1)
        WHERE id = OLD.source_id;

        RETURN OLD;
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_translation_coverage_sync ON quran_ayah_translations;
CREATE TRIGGER trg_quran_translation_coverage_sync
    AFTER INSERT OR DELETE ON quran_ayah_translations
    FOR EACH ROW
    EXECUTE FUNCTION quran_translation_coverage_sync();

CREATE OR REPLACE FUNCTION quran_transliteration_coverage_sync() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE quran_transliteration_sources
        SET coverage_count = coverage_count + 1
        WHERE id = NEW.source_id;

        RETURN NEW;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE quran_transliteration_sources
        SET coverage_count = GREATEST(0, coverage_count - 1)
        WHERE id = OLD.source_id;

        RETURN OLD;
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_transliteration_coverage_sync ON quran_ayah_transliterations;
CREATE TRIGGER trg_quran_transliteration_coverage_sync
    AFTER INSERT OR DELETE ON quran_ayah_transliterations
    FOR EACH ROW
    EXECUTE FUNCTION quran_transliteration_coverage_sync();

-- One-time reconciliation so any drift accumulated before the triggers existed is
-- corrected. Safe to run more than once (it is an absolute recompute, not a delta).
UPDATE quran_translation_sources s
SET coverage_count = (
    SELECT COUNT(*) FROM quran_ayah_translations t WHERE t.source_id = s.id
);

UPDATE quran_transliteration_sources s
SET coverage_count = (
    SELECT COUNT(*) FROM quran_ayah_transliterations t WHERE t.source_id = s.id
);
