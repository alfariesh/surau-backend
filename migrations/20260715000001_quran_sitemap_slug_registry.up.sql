-- Q-4: immutable Quran surah slug registry and public-page slug invariant.
--
-- Slugs are resolvable aliases, not Quran Anchors. quran_surahs.slug remains
-- the current slug while this append-only registry preserves every alias ever
-- used. Statement order is load-bearing because golang-migrate does not wrap
-- this file in one transaction.

-- Preflight existing routing data before adding a validated contract.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM quran_surahs
        WHERE slug IS NOT NULL
          AND slug !~ '^[a-z0-9]+(-[a-z0-9]+)*$'
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'Q-4 slug preflight failed: invalid Quran surah slug',
            CONSTRAINT = 'quran_surahs_slug_format_check';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM (
            SELECT surah_id FROM quran_surah_editorial_public
            UNION
            SELECT surah_id FROM quran_ayah_editorial_public
        ) public_editorial
        JOIN quran_surahs surah USING (surah_id)
        WHERE surah.slug IS NULL
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'Q-4 slug preflight failed: published permitted Quran editorial is missing a slug',
            CONSTRAINT = 'quran_editorial_public_slug_check';
    END IF;
END $$;

ALTER TABLE quran_surahs
    DROP CONSTRAINT IF EXISTS quran_surahs_slug_format_check,
    ADD CONSTRAINT quran_surahs_slug_format_check
        CHECK (slug IS NULL OR slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$') NOT VALID;

CREATE TABLE IF NOT EXISTS quran_surah_slug_registry (
    slug TEXT PRIMARY KEY,
    surah_id INTEGER NOT NULL REFERENCES quran_surahs(surah_id) ON DELETE RESTRICT,
    registered_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT quran_surah_slug_registry_format_check
        CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$')
);

CREATE INDEX IF NOT EXISTS idx_quran_surah_slug_registry_surah
    ON quran_surah_slug_registry(surah_id, registered_at);

INSERT INTO quran_surah_slug_registry (slug, surah_id, registered_at)
SELECT slug, surah_id, updated_at
FROM quran_surahs
WHERE slug IS NOT NULL
ON CONFLICT (slug) DO NOTHING;

CREATE OR REPLACE FUNCTION quran_surah_slug_registry_sync() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.slug IS NOT DISTINCT FROM OLD.slug THEN
        RETURN NEW;
    END IF;

    IF OLD.slug IS NOT NULL AND NEW.slug IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'Quran surah slug cannot be cleared after registration',
            CONSTRAINT = 'quran_surah_slug_current_required_check';
    END IF;

    IF NEW.slug IS NOT NULL AND EXISTS (
        SELECT 1 FROM quran_surah_slug_registry WHERE slug = NEW.slug
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23505',
            MESSAGE = 'Quran surah slug has already been registered and cannot be reused',
            CONSTRAINT = 'quran_surah_slug_registry_pkey';
    END IF;

    IF NEW.slug IS NOT NULL THEN
        INSERT INTO quran_surah_slug_registry (slug, surah_id)
        VALUES (NEW.slug, NEW.surah_id);
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- INSERT needs a separate function because OLD is not defined for INSERT.
CREATE OR REPLACE FUNCTION quran_surah_slug_registry_seed_insert() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.slug IS NOT NULL THEN
        INSERT INTO quran_surah_slug_registry (slug, surah_id)
        VALUES (NEW.slug, NEW.surah_id);
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_surah_slug_registry_update ON quran_surahs;
CREATE TRIGGER trg_quran_surah_slug_registry_update
    AFTER UPDATE OF slug ON quran_surahs
    FOR EACH ROW EXECUTE FUNCTION quran_surah_slug_registry_sync();

DROP TRIGGER IF EXISTS trg_quran_surah_slug_registry_insert ON quran_surahs;
CREATE TRIGGER trg_quran_surah_slug_registry_insert
    AFTER INSERT ON quran_surahs
    FOR EACH ROW EXECUTE FUNCTION quran_surah_slug_registry_seed_insert();

CREATE OR REPLACE FUNCTION quran_surah_slug_registry_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION USING
        ERRCODE = '23514',
        MESSAGE = 'Quran surah slug registry is append-only',
        CONSTRAINT = 'quran_surah_slug_registry_immutable_check';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_surah_slug_registry_immutable ON quran_surah_slug_registry;
CREATE TRIGGER trg_quran_surah_slug_registry_immutable
    BEFORE UPDATE OR DELETE ON quran_surah_slug_registry
    FOR EACH ROW EXECUTE FUNCTION quran_surah_slug_registry_immutable_guard();

DROP TRIGGER IF EXISTS trg_quran_surah_slug_registry_truncate_guard ON quran_surah_slug_registry;
CREATE TRIGGER trg_quran_surah_slug_registry_truncate_guard
    BEFORE TRUNCATE ON quran_surah_slug_registry
    FOR EACH STATEMENT EXECUTE FUNCTION quran_surah_slug_registry_immutable_guard();

CREATE OR REPLACE FUNCTION quran_editorial_public_slug_guard() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status = 'published' AND NEW.license_status = 'permitted'
       AND NOT EXISTS (
           SELECT 1
           FROM quran_surahs surah
           JOIN quran_surah_slug_registry registry
             ON registry.slug = surah.slug
            AND registry.surah_id = surah.surah_id
           WHERE surah.surah_id = NEW.surah_id
       ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'published permitted Quran editorial requires a registered surah slug',
            CONSTRAINT = 'quran_editorial_public_slug_check';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_quran_surah_editorial_public_slug_guard ON quran_surah_editorial;
CREATE TRIGGER trg_quran_surah_editorial_public_slug_guard
    BEFORE INSERT OR UPDATE OF status, license_status, surah_id ON quran_surah_editorial
    FOR EACH ROW EXECUTE FUNCTION quran_editorial_public_slug_guard();

DROP TRIGGER IF EXISTS trg_quran_ayah_editorial_public_slug_guard ON quran_ayah_editorial;
CREATE TRIGGER trg_quran_ayah_editorial_public_slug_guard
    BEFORE INSERT OR UPDATE OF status, license_status, surah_id ON quran_ayah_editorial
    FOR EACH ROW EXECUTE FUNCTION quran_editorial_public_slug_guard();

ALTER TABLE quran_surahs VALIDATE CONSTRAINT quran_surahs_slug_format_check;
