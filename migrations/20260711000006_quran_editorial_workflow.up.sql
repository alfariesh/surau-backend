-- Q-1: bring Quran surah/ayah editorial content onto the same draft/publish
-- workflow used by kitab. Existing rows are grandfathered as published without
-- rewriting any legacy content, checksum, or timestamps.

-- EXPAND: keep the new columns nullable while existing rows are grandfathered.
ALTER TABLE quran_surah_editorial
    ADD COLUMN IF NOT EXISTS status TEXT,
    ADD COLUMN IF NOT EXISTS updated_by UUID,
    ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ;

ALTER TABLE quran_ayah_editorial
    ADD COLUMN IF NOT EXISTS status TEXT,
    ADD COLUMN IF NOT EXISTS updated_by UUID,
    ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ;

-- Grandfather only workflow metadata. In particular, updated_at and checksum
-- are deliberately absent from SET so public lastmod/content remain identical.
UPDATE quran_surah_editorial
SET status = 'published',
    published_at = updated_at
WHERE status IS NULL;

UPDATE quran_ayah_editorial
SET status = 'published',
    published_at = updated_at
WHERE status IS NULL;

ALTER TABLE quran_surah_editorial
    ALTER COLUMN status SET DEFAULT 'draft',
    ALTER COLUMN status SET NOT NULL;

ALTER TABLE quran_ayah_editorial
    ALTER COLUMN status SET DEFAULT 'draft',
    ALTER COLUMN status SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'quran_surah_editorial'::regclass
          AND conname = 'quran_surah_editorial_status_check'
    ) THEN
        ALTER TABLE quran_surah_editorial
            ADD CONSTRAINT quran_surah_editorial_status_check
            CHECK (status IN ('draft', 'published')) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'quran_surah_editorial'::regclass
          AND conname = 'quran_surah_editorial_publish_timestamp_check'
    ) THEN
        ALTER TABLE quran_surah_editorial
            ADD CONSTRAINT quran_surah_editorial_publish_timestamp_check
            CHECK (
                (status = 'draft' AND published_at IS NULL)
                OR (status = 'published' AND published_at IS NOT NULL)
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'quran_surah_editorial'::regclass
          AND conname = 'quran_surah_editorial_updated_by_fk'
    ) THEN
        ALTER TABLE quran_surah_editorial
            ADD CONSTRAINT quran_surah_editorial_updated_by_fk
            FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE SET NULL NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'quran_ayah_editorial'::regclass
          AND conname = 'quran_ayah_editorial_status_check'
    ) THEN
        ALTER TABLE quran_ayah_editorial
            ADD CONSTRAINT quran_ayah_editorial_status_check
            CHECK (status IN ('draft', 'published')) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'quran_ayah_editorial'::regclass
          AND conname = 'quran_ayah_editorial_publish_timestamp_check'
    ) THEN
        ALTER TABLE quran_ayah_editorial
            ADD CONSTRAINT quran_ayah_editorial_publish_timestamp_check
            CHECK (
                (status = 'draft' AND published_at IS NULL)
                OR (status = 'published' AND published_at IS NOT NULL)
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'quran_ayah_editorial'::regclass
          AND conname = 'quran_ayah_editorial_updated_by_fk'
    ) THEN
        ALTER TABLE quran_ayah_editorial
            ADD CONSTRAINT quran_ayah_editorial_updated_by_fk
            FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE SET NULL NOT VALID;
    END IF;
END $$;

-- PREFLIGHT before replacing the keys. This also catches a partially applied
-- migration instead of allowing invalid workflow rows to become canonical.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM quran_surah_editorial
        WHERE status NOT IN ('draft', 'published')
           OR (status = 'draft' AND published_at IS NOT NULL)
           OR (status = 'published' AND published_at IS NULL)
    ) THEN
        RAISE EXCEPTION 'Q-1 surah editorial workflow preflight failed';
    END IF;

    IF EXISTS (
        SELECT 1 FROM quran_ayah_editorial
        WHERE status NOT IN ('draft', 'published')
           OR (status = 'draft' AND published_at IS NOT NULL)
           OR (status = 'published' AND published_at IS NULL)
    ) THEN
        RAISE EXCEPTION 'Q-1 ayah editorial workflow preflight failed';
    END IF;
END $$;

ALTER TABLE quran_surah_editorial
    VALIDATE CONSTRAINT quran_surah_editorial_status_check;
ALTER TABLE quran_surah_editorial
    VALIDATE CONSTRAINT quran_surah_editorial_publish_timestamp_check;
ALTER TABLE quran_surah_editorial
    VALIDATE CONSTRAINT quran_surah_editorial_updated_by_fk;

ALTER TABLE quran_ayah_editorial
    VALIDATE CONSTRAINT quran_ayah_editorial_status_check;
ALTER TABLE quran_ayah_editorial
    VALIDATE CONSTRAINT quran_ayah_editorial_publish_timestamp_check;
ALTER TABLE quran_ayah_editorial
    VALIDATE CONSTRAINT quran_ayah_editorial_updated_by_fk;

ALTER TABLE quran_surah_editorial
    DROP CONSTRAINT IF EXISTS quran_surah_editorial_pkey;
ALTER TABLE quran_surah_editorial
    ADD CONSTRAINT quran_surah_editorial_pkey PRIMARY KEY (surah_id, lang, status);

ALTER TABLE quran_ayah_editorial
    DROP CONSTRAINT IF EXISTS quran_ayah_editorial_pkey;
ALTER TABLE quran_ayah_editorial
    ADD CONSTRAINT quran_ayah_editorial_pkey
    PRIMARY KEY (surah_id, ayah_number, lang, status);

-- Immutable snapshots. Version is monotonic across the logical resource, not
-- per status, so save -> publish -> restore is one unambiguous timeline.
CREATE TABLE IF NOT EXISTS quran_editorial_revisions (
    id UUID PRIMARY KEY,
    resource_type TEXT NOT NULL,
    surah_id INTEGER NOT NULL,
    ayah_number INTEGER,
    lang TEXT NOT NULL,
    status TEXT NOT NULL,
    version BIGINT NOT NULL,
    actor_id UUID REFERENCES users (id) ON DELETE SET NULL,
    origin TEXT NOT NULL DEFAULT 'rest',
    snapshot JSONB NOT NULL,
    is_migration_baseline BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT quran_editorial_revisions_surah_fk
        FOREIGN KEY (surah_id) REFERENCES quran_surahs (surah_id) ON DELETE CASCADE,
    CONSTRAINT quran_editorial_revisions_ayah_fk
        FOREIGN KEY (surah_id, ayah_number)
        REFERENCES quran_ayahs (surah_id, ayah_number) ON DELETE CASCADE,
    CONSTRAINT quran_editorial_revisions_resource_type_check
        CHECK (resource_type IN ('surah', 'ayah')),
    CONSTRAINT quran_editorial_revisions_scope_check CHECK (
        (resource_type = 'surah' AND ayah_number IS NULL)
        OR (resource_type = 'ayah' AND ayah_number IS NOT NULL)
    ),
    CONSTRAINT quran_editorial_revisions_lang_check CHECK (lang IN ('ar', 'id', 'en')),
    CONSTRAINT quran_editorial_revisions_status_check
        CHECK (status IN ('draft', 'published')),
    CONSTRAINT quran_editorial_revisions_version_check CHECK (version > 0),
    CONSTRAINT quran_editorial_revisions_origin_check
        CHECK (origin IN ('rest', 'import', 'restore')),
    CONSTRAINT quran_editorial_revisions_snapshot_check
        CHECK (jsonb_typeof(snapshot) = 'object'),
    CONSTRAINT quran_editorial_revisions_baseline_check CHECK (
        NOT is_migration_baseline
        OR (
            version = 1
            AND status = 'published'
            AND origin = 'import'
            AND actor_id IS NULL
        )
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_quran_editorial_revisions_scope_version
    ON quran_editorial_revisions (
        resource_type,
        surah_id,
        COALESCE(ayah_number, 0),
        lang,
        version
    );

CREATE INDEX IF NOT EXISTS idx_quran_editorial_revisions_scope_history
    ON quran_editorial_revisions (
        resource_type,
        surah_id,
        COALESCE(ayah_number, 0),
        lang,
        version DESC
    );

-- Every grandfathered row gets a restorable v1 baseline. Deterministic IDs and
-- NOT EXISTS keep recovery/replay safe without requiring an extension UUID
-- generator. to_jsonb(row) records every column, including NULL-valued fields.
INSERT INTO quran_editorial_revisions (
    id,
    resource_type,
    surah_id,
    ayah_number,
    lang,
    status,
    version,
    actor_id,
    origin,
    snapshot,
    is_migration_baseline,
    created_at
)
SELECT md5('quran-editorial-baseline:surah:' || editorial.surah_id::TEXT || ':' || editorial.lang)::UUID,
       'surah',
       editorial.surah_id,
       NULL,
       editorial.lang,
       'published',
       1,
       NULL,
       'import',
       to_jsonb(editorial),
       TRUE,
       editorial.updated_at
FROM quran_surah_editorial editorial
WHERE editorial.status = 'published'
  AND NOT EXISTS (
      SELECT 1
      FROM quran_editorial_revisions revision
      WHERE revision.resource_type = 'surah'
        AND revision.surah_id = editorial.surah_id
        AND revision.ayah_number IS NULL
        AND revision.lang = editorial.lang
  );

INSERT INTO quran_editorial_revisions (
    id,
    resource_type,
    surah_id,
    ayah_number,
    lang,
    status,
    version,
    actor_id,
    origin,
    snapshot,
    is_migration_baseline,
    created_at
)
SELECT md5(
           'quran-editorial-baseline:ayah:'
           || editorial.surah_id::TEXT || ':'
           || editorial.ayah_number::TEXT || ':'
           || editorial.lang
       )::UUID,
       'ayah',
       editorial.surah_id,
       editorial.ayah_number,
       editorial.lang,
       'published',
       1,
       NULL,
       'import',
       to_jsonb(editorial),
       TRUE,
       editorial.updated_at
FROM quran_ayah_editorial editorial
WHERE editorial.status = 'published'
  AND NOT EXISTS (
      SELECT 1
      FROM quran_editorial_revisions revision
      WHERE revision.resource_type = 'ayah'
        AND revision.surah_id = editorial.surah_id
        AND revision.ayah_number = editorial.ayah_number
        AND revision.lang = editorial.lang
  );

-- Canonical public projections. Workflow fields are intentionally omitted so
-- the existing public API shape cannot leak draft/publish internals.
CREATE OR REPLACE VIEW quran_surah_editorial_public AS
SELECT surah_id,
       lang,
       meta_title,
       meta_description,
       arti_nama,
       keutamaan_html,
       asbabun_nuzul_html,
       pokok_kandungan_html,
       author_name,
       reviewed_by,
       reviewed_at,
       license_status,
       metadata,
       created_at,
       updated_at,
       checksum
FROM quran_surah_editorial
WHERE status = 'published' AND license_status = 'permitted';

CREATE OR REPLACE VIEW quran_ayah_editorial_public AS
SELECT surah_id,
       ayah_number,
       ayah_key,
       lang,
       meta_title,
       meta_description,
       intisari_html,
       keutamaan_html,
       faq,
       tafsir_range,
       author_name,
       reviewed_by,
       reviewed_at,
       license_status,
       checksum,
       metadata,
       created_at,
       updated_at
FROM quran_ayah_editorial
WHERE status = 'published' AND license_status = 'permitted';

DROP INDEX IF EXISTS idx_quran_surah_editorial_permitted;
CREATE INDEX IF NOT EXISTS idx_quran_surah_editorial_published_permitted
    ON quran_surah_editorial (surah_id, lang)
    WHERE status = 'published' AND license_status = 'permitted';

DROP INDEX IF EXISTS idx_quran_ayah_editorial_permitted;
CREATE INDEX IF NOT EXISTS idx_quran_ayah_editorial_published_permitted
    ON quran_ayah_editorial (surah_id, lang, ayah_number)
    WHERE status = 'published' AND license_status = 'permitted';

-- One write-path guard for both editorial tables, their history, and the
-- language-independent fields owned by the surah editorial importer. Nested
-- trigger depth permits FK cascades from quran_surahs/quran_ayahs.
CREATE OR REPLACE FUNCTION quran_editorial_writer_guard() RETURNS TRIGGER AS $$
BEGIN
    IF pg_trigger_depth() > 1 THEN
        IF TG_OP = 'TRUNCATE' THEN
            RETURN NULL;
        END IF;
        RETURN COALESCE(NEW, OLD);
    END IF;

    -- Ordinary canonical-surah inserts remain valid. A caller supplying any
    -- language-independent editorial field must use the workflow marker, just
    -- like an UPDATE of those fields.
    IF TG_TABLE_NAME = 'quran_surahs'
       AND TG_OP = 'INSERT'
       AND to_jsonb(NEW) ->> 'slug' IS NULL
       AND to_jsonb(NEW) ->> 'chronological_order' IS NULL
       AND to_jsonb(NEW) ->> 'ruku_count' IS NULL THEN
        RETURN NEW;
    END IF;

    IF current_setting('surau.quran_editorial_writer', true)
        IS DISTINCT FROM 'quran-editorial-service' THEN
        RAISE EXCEPTION USING
            ERRCODE = '42501',
            MESSAGE = 'Quran editorial accepts writes only via the editorial workflow service';
    END IF;

    IF TG_TABLE_NAME = 'quran_editorial_revisions'
       AND TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'Quran editorial revision snapshots are immutable',
            CONSTRAINT = 'quran_editorial_revisions_immutable_check';
    END IF;

    IF TG_TABLE_NAME = 'quran_editorial_revisions'
       AND TG_OP = 'INSERT'
       AND to_jsonb(NEW) ->> 'is_migration_baseline' = 'true' THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'Quran editorial baseline revisions are migration-owned',
            CONSTRAINT = 'quran_editorial_revisions_baseline_owned_check';
    END IF;

    IF TG_TABLE_NAME IN ('quran_surah_editorial', 'quran_ayah_editorial')
       AND TG_OP <> 'DELETE'
       AND to_jsonb(NEW) ->> 'status' = 'published'
       AND (to_jsonb(NEW) ->> 'license_status') IS DISTINCT FROM 'permitted' THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'license_not_permitted: Quran editorial must be permitted before publish',
            CONSTRAINT = 'quran_editorial_published_license_check';
    END IF;

    IF TG_OP = 'TRUNCATE' THEN
        RETURN NULL;
    END IF;

    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

-- Install guards only after grandfathering and baseline seeding are complete.
DROP TRIGGER IF EXISTS trg_quran_surah_editorial_writer_guard ON quran_surah_editorial;
CREATE TRIGGER trg_quran_surah_editorial_writer_guard
    BEFORE INSERT OR UPDATE OR DELETE ON quran_surah_editorial
    FOR EACH ROW EXECUTE FUNCTION quran_editorial_writer_guard();

DROP TRIGGER IF EXISTS trg_quran_surah_editorial_truncate_guard ON quran_surah_editorial;
CREATE TRIGGER trg_quran_surah_editorial_truncate_guard
    BEFORE TRUNCATE ON quran_surah_editorial
    FOR EACH STATEMENT EXECUTE FUNCTION quran_editorial_writer_guard();

DROP TRIGGER IF EXISTS trg_quran_ayah_editorial_writer_guard ON quran_ayah_editorial;
CREATE TRIGGER trg_quran_ayah_editorial_writer_guard
    BEFORE INSERT OR UPDATE OR DELETE ON quran_ayah_editorial
    FOR EACH ROW EXECUTE FUNCTION quran_editorial_writer_guard();

DROP TRIGGER IF EXISTS trg_quran_ayah_editorial_truncate_guard ON quran_ayah_editorial;
CREATE TRIGGER trg_quran_ayah_editorial_truncate_guard
    BEFORE TRUNCATE ON quran_ayah_editorial
    FOR EACH STATEMENT EXECUTE FUNCTION quran_editorial_writer_guard();

DROP TRIGGER IF EXISTS trg_quran_editorial_revisions_writer_guard ON quran_editorial_revisions;
CREATE TRIGGER trg_quran_editorial_revisions_writer_guard
    BEFORE INSERT OR UPDATE OR DELETE ON quran_editorial_revisions
    FOR EACH ROW EXECUTE FUNCTION quran_editorial_writer_guard();

DROP TRIGGER IF EXISTS trg_quran_editorial_revisions_truncate_guard ON quran_editorial_revisions;
CREATE TRIGGER trg_quran_editorial_revisions_truncate_guard
    BEFORE TRUNCATE ON quran_editorial_revisions
    FOR EACH STATEMENT EXECUTE FUNCTION quran_editorial_writer_guard();

DROP TRIGGER IF EXISTS trg_quran_surahs_editorial_fields_writer_guard ON quran_surahs;
CREATE TRIGGER trg_quran_surahs_editorial_fields_writer_guard
    BEFORE UPDATE OF slug, chronological_order, ruku_count ON quran_surahs
    FOR EACH ROW EXECUTE FUNCTION quran_editorial_writer_guard();

DROP TRIGGER IF EXISTS trg_quran_surahs_editorial_fields_insert_guard ON quran_surahs;
CREATE TRIGGER trg_quran_surahs_editorial_fields_insert_guard
    BEFORE INSERT ON quran_surahs
    FOR EACH ROW EXECUTE FUNCTION quran_editorial_writer_guard();

-- TRUNCATE does not fire row triggers. Guard both FK parents as well, otherwise
-- TRUNCATE ... CASCADE could erase workflow rows without visiting a row guard.
DROP TRIGGER IF EXISTS trg_quran_surahs_editorial_truncate_guard ON quran_surahs;
CREATE TRIGGER trg_quran_surahs_editorial_truncate_guard
    BEFORE TRUNCATE ON quran_surahs
    FOR EACH STATEMENT EXECUTE FUNCTION quran_editorial_writer_guard();

DROP TRIGGER IF EXISTS trg_quran_ayahs_editorial_truncate_guard ON quran_ayahs;
CREATE TRIGGER trg_quran_ayahs_editorial_truncate_guard
    BEFORE TRUNCATE ON quran_ayahs
    FOR EACH STATEMENT EXECUTE FUNCTION quran_editorial_writer_guard();
