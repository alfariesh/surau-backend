-- B-4: platform License Status for kitab (books are the Edition boundary until K-2).
--
-- Existing public catalog/projects are grandfathered at migration time. The
-- marker is one-way: unpublishing clears it, and publishing again requires the
-- literal status `permitted`. A `restricted` audit result is fail-closed at the
-- canonical public view and revokes both grandfather markers immediately.

-- EXPAND first: nullable columns make a partially applied migration replay-safe.
ALTER TABLE books
    ADD COLUMN IF NOT EXISTS license_status TEXT,
    ADD COLUMN IF NOT EXISTS license_reason TEXT,
    ADD COLUMN IF NOT EXISTS license_evidence_url TEXT,
    ADD COLUMN IF NOT EXISTS license_updated_by UUID,
    ADD COLUMN IF NOT EXISTS license_updated_at TIMESTAMPTZ;

UPDATE books
SET license_status = COALESCE(license_status, 'unknown'),
    license_updated_at = COALESCE(license_updated_at, updated_at, now())
WHERE license_status IS NULL OR license_updated_at IS NULL;

ALTER TABLE books
    ALTER COLUMN license_status SET DEFAULT 'unknown',
    ALTER COLUMN license_updated_at SET DEFAULT now();

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'books'::regclass
          AND conname = 'books_license_status_check'
    ) THEN
        ALTER TABLE books
            ADD CONSTRAINT books_license_status_check CHECK (
                license_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain')
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'books'::regclass
          AND conname = 'books_license_updated_by_fk'
    ) THEN
        ALTER TABLE books
            ADD CONSTRAINT books_license_updated_by_fk
            FOREIGN KEY (license_updated_by) REFERENCES users (id) ON DELETE RESTRICT NOT VALID;
    END IF;
END $$;

-- PREFLIGHT: B-4 requires 100% Work/Edition coverage, even when the truthful
-- value is still `unknown`.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM books
        WHERE license_status IS NULL
           OR license_status NOT IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain')
           OR license_updated_at IS NULL
    ) THEN
        RAISE EXCEPTION 'book license preflight failed: every book must have a valid license status and timestamp';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM books b
        LEFT JOIN users u ON u.id = b.license_updated_by
        WHERE b.license_updated_by IS NOT NULL AND u.id IS NULL
    ) THEN
        RAISE EXCEPTION 'book license preflight failed: license_updated_by contains an orphan user';
    END IF;
END $$;

ALTER TABLE books VALIDATE CONSTRAINT books_license_status_check;
ALTER TABLE books VALIDATE CONSTRAINT books_license_updated_by_fk;
ALTER TABLE books
    ALTER COLUMN license_status SET NOT NULL,
    ALTER COLUMN license_updated_at SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_books_license_status_id
    ON books (license_status, id);

CREATE TABLE IF NOT EXISTS book_license_audits (
    id BIGSERIAL PRIMARY KEY,
    book_id INTEGER NOT NULL,
    old_status TEXT NOT NULL,
    new_status TEXT NOT NULL,
    reason TEXT NOT NULL,
    evidence_url TEXT,
    actor_id UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT book_license_audits_old_status_check CHECK (
        old_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain')
    ),
    CONSTRAINT book_license_audits_new_status_check CHECK (
        new_status IN ('unknown', 'needs_review', 'permitted', 'restricted', 'public_domain')
    ),
    CONSTRAINT book_license_audits_reason_check CHECK (btrim(reason) <> ''),
    CONSTRAINT book_license_audits_book_fk
        FOREIGN KEY (book_id) REFERENCES books (id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_book_license_audits_book_created
    ON book_license_audits (book_id, created_at DESC, id DESC);

-- A safe B-4 down migration archives decisions in the pre-existing admin
-- audit log. If B-4 is installed again, restore both the append-only evidence
-- and the latest Edition decision before grandfather visibility is computed.
INSERT INTO book_license_audits (
    book_id, old_status, new_status, reason, evidence_url, actor_id, created_at
)
SELECT archived.book_id,
       archived.payload ->> 'old_status',
       archived.payload ->> 'new_status',
       archived.payload ->> 'reason',
       NULLIF(archived.payload ->> 'evidence_url', ''),
       archived.actor_id,
       (archived.payload ->> 'license_created_at')::TIMESTAMPTZ
FROM admin_audit_logs archived
JOIN books b ON b.id = archived.book_id
JOIN users actor ON actor.id = archived.actor_id
WHERE archived.action = 'license.decision.archive'
  AND NOT EXISTS (
      SELECT 1
      FROM book_license_audits existing
      WHERE existing.book_id = archived.book_id
        AND existing.old_status = archived.payload ->> 'old_status'
        AND existing.new_status = archived.payload ->> 'new_status'
        AND existing.reason = archived.payload ->> 'reason'
        AND existing.actor_id = archived.actor_id
        AND existing.created_at = (archived.payload ->> 'license_created_at')::TIMESTAMPTZ
  )
ORDER BY (archived.payload ->> 'license_created_at')::TIMESTAMPTZ, archived.id;

WITH latest_archived AS (
    SELECT DISTINCT ON (archived.book_id)
           archived.book_id,
           archived.actor_id,
           archived.payload
    FROM admin_audit_logs archived
    JOIN users actor ON actor.id = archived.actor_id
    WHERE archived.action = 'license.decision.archive'
    ORDER BY archived.book_id,
             (archived.payload ->> 'license_created_at')::TIMESTAMPTZ DESC,
             archived.id DESC
)
UPDATE books b
SET license_status = latest.payload ->> 'new_status',
    license_reason = latest.payload ->> 'reason',
    license_evidence_url = NULLIF(latest.payload ->> 'evidence_url', ''),
    license_updated_by = latest.actor_id,
    license_updated_at = (latest.payload ->> 'license_created_at')::TIMESTAMPTZ
FROM latest_archived latest
WHERE b.id = latest.book_id;

ALTER TABLE book_publications
    ADD COLUMN IF NOT EXISTS license_grandfathered_at TIMESTAMPTZ;
ALTER TABLE book_production_projects
    ADD COLUMN IF NOT EXISTS license_grandfathered_at TIMESTAMPTZ;

-- Normalize historical project rows to the visibility semantics the Reader
-- already used before B-4. Archived workflows were already hidden; any other
-- published publication was visible and therefore remains published.
UPDATE book_production_projects
SET publication_status = 'archived',
    updated_at = now()
WHERE publication_status = 'published'
  AND workflow_status = 'archived';

UPDATE book_production_projects
SET workflow_status = 'published',
    updated_at = now()
WHERE publication_status = 'published'
  AND workflow_status NOT IN ('published', 'archived');

-- This is the only grandfather backfill. Later unpublish operations clear the
-- marker; the triggers below never mint a new one.
CREATE TABLE IF NOT EXISTS book_license_policy_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    grandfather_backfilled_at TIMESTAMPTZ
);
INSERT INTO book_license_policy_state (singleton)
VALUES (TRUE)
ON CONFLICT (singleton) DO NOTHING;

DO $$
DECLARE
    already_backfilled TIMESTAMPTZ;
BEGIN
    SELECT grandfather_backfilled_at INTO already_backfilled
    FROM book_license_policy_state
    WHERE singleton = TRUE
    FOR UPDATE;

    IF already_backfilled IS NULL THEN
        UPDATE book_publications
        SET license_grandfathered_at = COALESCE(published_at, updated_at, now())
        WHERE status = 'published' AND license_grandfathered_at IS NULL;

        UPDATE book_production_projects
        SET license_grandfathered_at = COALESCE(published_at, updated_at, now())
        WHERE publication_status = 'published' AND license_grandfathered_at IS NULL;

        UPDATE book_license_policy_state
        SET grandfather_backfilled_at = clock_timestamp()
        WHERE singleton = TRUE;
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'book_publications'::regclass
          AND conname = 'book_publications_license_grandfather_check'
    ) THEN
        ALTER TABLE book_publications
            ADD CONSTRAINT book_publications_license_grandfather_check CHECK (
                license_grandfathered_at IS NULL OR status = 'published'
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'book_production_projects'::regclass
          AND conname = 'book_production_projects_license_grandfather_check'
    ) THEN
        ALTER TABLE book_production_projects
            ADD CONSTRAINT book_production_projects_license_grandfather_check CHECK (
                license_grandfathered_at IS NULL OR publication_status = 'published'
            ) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'book_production_projects'::regclass
          AND conname = 'book_production_projects_publication_workflow_check'
    ) THEN
        ALTER TABLE book_production_projects
            ADD CONSTRAINT book_production_projects_publication_workflow_check CHECK (
                publication_status <> 'published' OR workflow_status = 'published'
            ) NOT VALID;
    END IF;
END $$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM book_publications
        WHERE license_grandfathered_at IS NOT NULL AND status <> 'published'
    ) THEN
        RAISE EXCEPTION 'book publication grandfather preflight failed';
    END IF;

    IF EXISTS (
        SELECT 1 FROM book_production_projects
        WHERE license_grandfathered_at IS NOT NULL AND publication_status <> 'published'
    ) THEN
        RAISE EXCEPTION 'book production grandfather preflight failed';
    END IF;

    IF EXISTS (
        SELECT 1 FROM book_production_projects
        WHERE publication_status = 'published' AND workflow_status <> 'published'
    ) THEN
        RAISE EXCEPTION 'book production publication/workflow preflight failed';
    END IF;
END $$;

ALTER TABLE book_publications
    VALIDATE CONSTRAINT book_publications_license_grandfather_check;
ALTER TABLE book_production_projects
    VALIDATE CONSTRAINT book_production_projects_license_grandfather_check;
ALTER TABLE book_production_projects
    VALIDATE CONSTRAINT book_production_projects_publication_workflow_check;

-- Edition-level status. Kept as a function so guards and consumers do not
-- duplicate the current Work/Edition compatibility boundary.
CREATE OR REPLACE FUNCTION book_effective_license_status(target_book_id INTEGER)
RETURNS TEXT AS $$
    SELECT b.license_status FROM books b WHERE b.id = target_book_id
$$ LANGUAGE sql STABLE PARALLEL SAFE;

-- Public readers join this view instead of interpreting publication status.
-- Column order intentionally preserves all seven book_publications columns and
-- appends only the effective Edition status.
CREATE OR REPLACE VIEW public_book_publications AS
SELECT p.book_id,
       p.status,
       p.featured,
       p.sort_order,
       p.published_at,
       p.updated_by,
       p.updated_at,
       b.license_status
FROM book_publications p
JOIN books b ON b.id = p.book_id
WHERE p.status = 'published'
  AND b.is_deleted = FALSE
  AND (
      b.license_status = 'permitted'
      OR (
          p.license_grandfathered_at IS NOT NULL
          AND b.license_status <> 'restricted'
      )
  );

-- Citable Unit inheritance stays virtual: explicit unit overrides win, while
-- legacy/pilot units continue to inherit from their Edition without a rewrite.
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
LEFT JOIN books b ON b.id = u.book_id AND u.corpus = 'kitab';

-- Queue/report source: popularity uses only backend facts that exist today
-- (registered readers and saved items), never fabricated anonymous views.
CREATE OR REPLACE VIEW book_license_audit_queue AS
WITH reader_stats AS (
    SELECT rp.book_id,
           count(DISTINCT rp.user_id) AS registered_reader_count,
           max(rp.updated_at) AS last_progress_at
    FROM reading_progress rp
    GROUP BY rp.book_id
),
saved_stats AS (
    SELECT si.book_id,
           count(*) AS saved_item_count,
           max(si.updated_at) AS last_saved_at
    FROM saved_items si
    WHERE si.book_id IS NOT NULL
    GROUP BY si.book_id
),
language_stats AS (
    SELECT pp.book_id,
           count(*) FILTER (WHERE pp.publication_status = 'published') AS published_language_count,
           count(*) FILTER (
               WHERE pp.publication_status = 'published'
                 AND pp.license_grandfathered_at IS NOT NULL
                 AND b.license_status NOT IN ('permitted', 'restricted')
                 AND public_p.book_id IS NOT NULL
           ) AS grandfathered_language_count
    FROM book_production_projects pp
    JOIN books b ON b.id = pp.book_id
    LEFT JOIN public_book_publications public_p ON public_p.book_id = pp.book_id
    GROUP BY pp.book_id
)
SELECT b.id AS book_id,
       COALESCE(me.display_title, b.name) AS book_name,
       b.license_status,
       b.license_reason,
       b.license_evidence_url,
       b.license_updated_by,
       b.license_updated_at,
       b.is_deleted,
       p.status AS publication_status,
       p.published_at AS catalog_published_at,
       (
           public_p.book_id IS NOT NULL
           AND b.license_status NOT IN ('permitted', 'restricted')
       ) AS catalog_grandfathered,
       (public_p.book_id IS NOT NULL) AS currently_public,
       COALESCE(ls.published_language_count, 0::BIGINT) AS published_language_count,
       COALESCE(ls.grandfathered_language_count, 0::BIGINT) AS grandfathered_language_count,
       COALESCE(rs.registered_reader_count, 0::BIGINT) AS registered_reader_count,
       COALESCE(ss.saved_item_count, 0::BIGINT) AS saved_item_count,
       CASE
           WHEN rs.last_progress_at IS NULL THEN ss.last_saved_at
           WHEN ss.last_saved_at IS NULL THEN rs.last_progress_at
           ELSE GREATEST(rs.last_progress_at, ss.last_saved_at)
       END AS last_reader_activity_at
FROM books b
LEFT JOIN book_metadata_edits me
  ON me.book_id = b.id AND me.status = 'published'
LEFT JOIN book_publications p ON p.book_id = b.id
LEFT JOIN public_book_publications public_p ON public_p.book_id = b.id
LEFT JOIN reader_stats rs ON rs.book_id = b.id
LEFT JOIN saved_stats ss ON ss.book_id = b.id
LEFT JOIN language_stats ls ON ls.book_id = b.id;

-- Stable database identity for every publish gate. Go maps SQLSTATE 23514 plus
-- this constraint name to the `license_not_permitted` apierror.
CREATE OR REPLACE FUNCTION assert_license_status_publish_permitted(
    target_book_id INTEGER,
    candidate_status TEXT,
    attempted_action TEXT
) RETURNS VOID AS $$
BEGIN
    IF candidate_status IS DISTINCT FROM 'permitted' THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'license_not_permitted: book license_status must be permitted before ' || attempted_action,
            DETAIL = 'book_id=' || COALESCE(target_book_id::TEXT, 'null') || ', license_status=' || COALESCE(candidate_status, 'missing'),
            CONSTRAINT = 'book_license_publish_permitted_check';
    END IF;
END;
$$ LANGUAGE plpgsql STABLE;

CREATE OR REPLACE FUNCTION assert_book_license_publish_permitted(
    target_book_id INTEGER,
    attempted_action TEXT
) RETURNS VOID AS $$
DECLARE
    locked_status TEXT;
BEGIN
    -- Serialize every publish/content-write decision with license changes.
    -- Callers that touch multiple books invoke this in ascending book order.
    SELECT b.license_status INTO locked_status
    FROM books b
    WHERE b.id = target_book_id AND b.is_deleted = FALSE
    FOR SHARE;

    PERFORM assert_license_status_publish_permitted(
        target_book_id,
        locked_status,
        attempted_action
    );
END;
$$ LANGUAGE plpgsql;

-- New Edition rows always enter the registry as unknown. Any legal decision,
-- including permitted, must be a later actor+reason update so the audit trigger
-- can append evidence atomically.
CREATE OR REPLACE FUNCTION book_license_initial_status_guard() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.license_status IS DISTINCT FROM 'unknown'
       OR NEW.license_reason IS NOT NULL
       OR NEW.license_evidence_url IS NOT NULL
       OR NEW.license_updated_by IS NOT NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'new books must start with license_status unknown',
            CONSTRAINT = 'book_license_initial_unknown_check';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_books_license_initial_status ON books;
CREATE TRIGGER trg_books_license_initial_status
    BEFORE INSERT ON books
    FOR EACH ROW EXECUTE FUNCTION book_license_initial_status_guard();

CREATE OR REPLACE FUNCTION book_license_audit_guard() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.license_updated_by IS NULL THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'book license audit requires an actor',
            CONSTRAINT = 'book_license_audit_actor_check';
    END IF;
    IF btrim(COALESCE(NEW.license_reason, '')) = '' THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'book license audit requires a non-empty reason',
            CONSTRAINT = 'book_license_audit_reason_check';
    END IF;

    NEW.license_updated_at := GREATEST(
        clock_timestamp(),
        OLD.license_updated_at + INTERVAL '1 microsecond'
    );
    INSERT INTO book_license_audits (
        book_id, old_status, new_status, reason, evidence_url, actor_id, created_at
    ) VALUES (
        OLD.id,
        OLD.license_status,
        NEW.license_status,
        NEW.license_reason,
        NEW.license_evidence_url,
        NEW.license_updated_by,
        NEW.license_updated_at
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_books_license_audit ON books;
CREATE TRIGGER trg_books_license_audit
    BEFORE UPDATE OF license_status, license_reason, license_evidence_url,
        license_updated_by, license_updated_at
    ON books
    FOR EACH ROW EXECUTE FUNCTION book_license_audit_guard();

CREATE OR REPLACE FUNCTION book_license_audits_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION USING
        ERRCODE = '23514',
        MESSAGE = 'book license audit history is append-only',
        CONSTRAINT = 'book_license_audits_immutable_check';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_book_license_audits_immutable ON book_license_audits;
CREATE TRIGGER trg_book_license_audits_immutable
    BEFORE UPDATE OR DELETE ON book_license_audits
    FOR EACH ROW EXECUTE FUNCTION book_license_audits_immutable_guard();

CREATE OR REPLACE FUNCTION book_publication_license_guard() RETURNS TRIGGER AS $$
DECLARE
    has_active_grandfather BOOLEAN;
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.license_grandfathered_at IS NOT NULL
       AND NEW.book_id IS DISTINCT FROM OLD.book_id THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'book publication grandfather marker cannot move between books',
            CONSTRAINT = 'book_license_grandfather_immutable_check';
    END IF;

    IF (TG_OP = 'INSERT' AND NEW.license_grandfathered_at IS NOT NULL)
       OR (
           TG_OP = 'UPDATE'
           AND NEW.license_grandfathered_at IS NOT NULL
           AND NEW.license_grandfathered_at IS DISTINCT FROM OLD.license_grandfathered_at
       ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'book publication grandfather marker is migration-owned and immutable',
            CONSTRAINT = 'book_license_grandfather_immutable_check';
    END IF;

    IF NEW.status <> 'published' THEN
        NEW.license_grandfathered_at := NULL;
        RETURN NEW;
    END IF;

    IF book_effective_license_status(NEW.book_id) = 'permitted' THEN
        RETURN NEW;
    END IF;

    -- Editorial publication uses INSERT .. ON CONFLICT UPDATE. BEFORE INSERT
    -- therefore has to recognize a row that is already grandfathered.
    IF TG_OP = 'INSERT' THEN
        SELECT EXISTS (
            SELECT 1
            FROM book_publications p
            WHERE p.book_id = NEW.book_id
              AND p.status = 'published'
              AND p.license_grandfathered_at IS NOT NULL
        ) INTO has_active_grandfather;
        IF has_active_grandfather THEN
            RETURN NEW;
        END IF;
    ELSIF OLD.status = 'published'
       AND NEW.book_id IS NOT DISTINCT FROM OLD.book_id THEN
        -- Keeping an existing marker leaves legacy content as-is. Clearing it
        -- is also safe because it only removes visibility. Re-minting a marker
        -- after it was cleared is forbidden below.
        IF OLD.license_grandfathered_at IS NOT NULL
           OR NEW.license_grandfathered_at IS NULL THEN
            RETURN NEW;
        END IF;
    END IF;

    PERFORM assert_book_license_publish_permitted(NEW.book_id, 'catalog publication');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_book_publications_license_guard ON book_publications;
CREATE TRIGGER trg_book_publications_license_guard
    BEFORE INSERT OR UPDATE ON book_publications
    FOR EACH ROW EXECUTE FUNCTION book_publication_license_guard();

CREATE OR REPLACE FUNCTION book_production_project_license_guard() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.license_grandfathered_at IS NOT NULL
       AND (
           NEW.id IS DISTINCT FROM OLD.id
           OR NEW.book_id IS DISTINCT FROM OLD.book_id
           OR NEW.lang IS DISTINCT FROM OLD.lang
       ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'book production grandfather marker cannot move between project identities',
            CONSTRAINT = 'book_license_grandfather_immutable_check';
    END IF;

    IF (TG_OP = 'INSERT' AND NEW.license_grandfathered_at IS NOT NULL)
       OR (
           TG_OP = 'UPDATE'
           AND NEW.license_grandfathered_at IS NOT NULL
           AND NEW.license_grandfathered_at IS DISTINCT FROM OLD.license_grandfathered_at
       ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'book production grandfather marker is migration-owned and immutable',
            CONSTRAINT = 'book_license_grandfather_immutable_check';
    END IF;

    -- Leaving the published workflow ends grandfather permanently, even if a
    -- direct SQL caller forgot to update publication_status at the same time.
    IF TG_OP = 'UPDATE'
       AND OLD.workflow_status = 'published'
       AND NEW.workflow_status <> 'published'
       AND NEW.publication_status = 'published' THEN
        NEW.publication_status := CASE
            WHEN NEW.workflow_status = 'archived' THEN 'archived'
            ELSE 'hidden'
        END;
    END IF;

    IF NEW.publication_status <> 'published' THEN
        NEW.license_grandfathered_at := NULL;
        RETURN NEW;
    END IF;
    IF NEW.workflow_status <> 'published' THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'published production project requires published workflow',
            CONSTRAINT = 'book_production_projects_publication_workflow_check';
    END IF;
    IF book_effective_license_status(NEW.book_id) = 'permitted' THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'UPDATE'
       AND OLD.publication_status = 'published'
       AND OLD.workflow_status = 'published'
       AND NEW.id IS NOT DISTINCT FROM OLD.id
       AND NEW.book_id IS NOT DISTINCT FROM OLD.book_id
       AND NEW.lang IS NOT DISTINCT FROM OLD.lang
       AND (
           OLD.license_grandfathered_at IS NOT NULL
           OR NEW.license_grandfathered_at IS NULL
       ) THEN
        RETURN NEW;
    END IF;

    PERFORM assert_book_license_publish_permitted(NEW.book_id, 'production project publication');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_book_production_projects_license_guard ON book_production_projects;
CREATE TRIGGER trg_book_production_projects_license_guard
    BEFORE INSERT OR UPDATE ON book_production_projects
    FOR EACH ROW EXECUTE FUNCTION book_production_project_license_guard();

-- Restricted is the only audit result that forcibly takes down grandfathered
-- legacy work. Clearing the marker prevents it from reappearing if a later
-- audit moves the Work back to an unresolved status.
CREATE OR REPLACE FUNCTION revoke_restricted_book_grandfather() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.license_status = 'restricted'
       AND OLD.license_status IS DISTINCT FROM NEW.license_status THEN
        UPDATE book_publications
        SET license_grandfathered_at = NULL
        WHERE book_id = NEW.id AND license_grandfathered_at IS NOT NULL;

        UPDATE book_production_projects
        SET license_grandfathered_at = NULL
        WHERE book_id = NEW.id AND license_grandfathered_at IS NOT NULL;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_books_revoke_restricted_grandfather ON books;
CREATE TRIGGER trg_books_revoke_restricted_grandfather
    AFTER UPDATE OF license_status ON books
    FOR EACH ROW EXECUTE FUNCTION revoke_restricted_book_grandfather();

-- Publishing source overlays is a public content change even when the catalog
-- Work itself is grandfathered.
CREATE OR REPLACE FUNCTION book_published_overlay_license_guard() RETURNS TRIGGER AS $$
DECLARE
    target_book_id INTEGER;
    previous_book_id INTEGER;
    was_published BOOLEAN := FALSE;
    will_be_published BOOLEAN := FALSE;
BEGIN
    IF TG_OP = 'DELETE' THEN
        target_book_id := OLD.book_id;
        was_published := OLD.status = 'published';
    ELSE
        target_book_id := NEW.book_id;
        will_be_published := NEW.status = 'published';
        IF TG_OP = 'UPDATE' THEN
            previous_book_id := OLD.book_id;
            was_published := OLD.status = 'published';
        END IF;
    END IF;

    -- Moving a published overlay changes both Works. Guard the source Work as
    -- well as the destination so changing book_id cannot disguise a DELETE.
    IF TG_OP = 'UPDATE'
       AND was_published
       AND previous_book_id IS DISTINCT FROM target_book_id
       AND EXISTS (
           SELECT 1 FROM public_book_publications p WHERE p.book_id = previous_book_id
       ) THEN
        PERFORM assert_book_license_publish_permitted(
            previous_book_id,
            TG_TABLE_NAME || ' published overlay move'
        );
    END IF;

    IF (was_published OR will_be_published)
       AND EXISTS (
           SELECT 1 FROM public_book_publications p WHERE p.book_id = target_book_id
       ) THEN
        PERFORM assert_book_license_publish_permitted(target_book_id, TG_TABLE_NAME || ' published overlay change');
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_book_metadata_edits_license_guard ON book_metadata_edits;
CREATE TRIGGER trg_book_metadata_edits_license_guard
    BEFORE INSERT OR UPDATE OR DELETE ON book_metadata_edits
    FOR EACH ROW EXECUTE FUNCTION book_published_overlay_license_guard();
DROP TRIGGER IF EXISTS trg_book_page_edits_license_guard ON book_page_edits;
CREATE TRIGGER trg_book_page_edits_license_guard
    BEFORE INSERT OR UPDATE OR DELETE ON book_page_edits
    FOR EACH ROW EXECUTE FUNCTION book_published_overlay_license_guard();
DROP TRIGGER IF EXISTS trg_book_heading_edits_license_guard ON book_heading_edits;
CREATE TRIGGER trg_book_heading_edits_license_guard
    BEFORE INSERT OR UPDATE OR DELETE ON book_heading_edits
    FOR EACH ROW EXECUTE FUNCTION book_published_overlay_license_guard();

-- Raw Shamela source may be updated only while hidden/restricted or after the
-- Edition has been audited permitted. Deletes/tombstones are visibility-
-- reducing and remain allowed.
CREATE OR REPLACE FUNCTION book_raw_source_license_guard() RETURNS TRIGGER AS $$
DECLARE
    row_json JSONB;
    target_book_id INTEGER;
    target_deleted BOOLEAN;
    prospective_status TEXT;
BEGIN
    row_json := to_jsonb(NEW);
    target_book_id := CASE
        WHEN TG_TABLE_NAME = 'books' THEN (row_json ->> 'id')::INTEGER
        ELSE (row_json ->> 'book_id')::INTEGER
    END;
    target_deleted := COALESCE((row_json ->> 'is_deleted')::BOOLEAN, FALSE);
    prospective_status := CASE
        WHEN TG_TABLE_NAME = 'books' THEN row_json ->> 'license_status'
        ELSE book_effective_license_status(target_book_id)
    END;
    IF NOT target_deleted
       AND EXISTS (
           SELECT 1
           FROM book_publications p
           WHERE p.book_id = target_book_id
             AND p.status = 'published'
             AND (
                 prospective_status = 'permitted'
                 OR (
                     p.license_grandfathered_at IS NOT NULL
                     AND prospective_status <> 'restricted'
                 )
             )
       ) THEN
        IF TG_TABLE_NAME = 'books' THEN
            -- This row is already write-locked by the UPDATE; use NEW so a
            -- combined license+content statement cannot bypass the gate.
            PERFORM assert_license_status_publish_permitted(
                target_book_id,
                prospective_status,
                TG_TABLE_NAME || ' source update'
            );
        ELSE
            PERFORM assert_book_license_publish_permitted(
                target_book_id,
                TG_TABLE_NAME || ' source update'
            );
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_books_raw_source_license_guard ON books;
CREATE TRIGGER trg_books_raw_source_license_guard
    BEFORE UPDATE OF id, name, category_id, author_id, type, printed, minor_release, major_release,
        bibliography, hint, pdf_links, metadata, source_date, has_content, is_deleted
    ON books
    FOR EACH ROW EXECUTE FUNCTION book_raw_source_license_guard();
DROP TRIGGER IF EXISTS trg_book_pages_raw_source_license_guard ON book_pages;
CREATE TRIGGER trg_book_pages_raw_source_license_guard
    BEFORE INSERT OR UPDATE OF book_id, page_id, part, printed_page, number, content_html, content_text, services, is_deleted
    ON book_pages
    FOR EACH ROW EXECUTE FUNCTION book_raw_source_license_guard();
DROP TRIGGER IF EXISTS trg_book_headings_raw_source_license_guard ON book_headings;
CREATE TRIGGER trg_book_headings_raw_source_license_guard
    BEFORE INSERT OR UPDATE OF book_id, heading_id, parent_id, page_id, depth, ordinal, content, is_deleted
    ON book_headings
    FOR EACH ROW EXECUTE FUNCTION book_raw_source_license_guard();
DROP TRIGGER IF EXISTS trg_book_heading_ranges_raw_source_license_guard ON book_heading_ranges;
CREATE TRIGGER trg_book_heading_ranges_raw_source_license_guard
    BEFORE INSERT OR UPDATE OF book_id, heading_id, start_page_id, end_page_id, start_anchor, end_anchor
    ON book_heading_ranges
    FOR EACH ROW EXECUTE FUNCTION book_raw_source_license_guard();

-- Final production assets are visible only when their language project is
-- published. Writes to an already-visible grandfathered project are therefore
-- new publication and must fail atomically. Writes for a hidden project remain
-- possible; its later publish transition is guarded above.
CREATE OR REPLACE FUNCTION book_final_asset_license_guard() RETURNS TRIGGER AS $$
DECLARE
    row_json JSONB;
    previous_json JSONB;
    target_book_id INTEGER;
    target_lang TEXT;
    previous_book_id INTEGER;
    previous_lang TEXT;
    was_present BOOLEAN := FALSE;
    will_be_present BOOLEAN := FALSE;
    visible_without_project BOOLEAN := FALSE;
    previous_visible_without_project BOOLEAN := FALSE;
BEGIN
    IF TG_OP = 'DELETE' THEN
        row_json := to_jsonb(OLD);
        previous_json := row_json;
        was_present := NOT COALESCE((row_json ->> 'is_deleted')::BOOLEAN, FALSE);
    ELSE
        row_json := to_jsonb(NEW);
        will_be_present := NOT COALESCE((row_json ->> 'is_deleted')::BOOLEAN, FALSE);
        IF TG_OP = 'UPDATE' THEN
            previous_json := to_jsonb(OLD);
            was_present := NOT COALESCE((previous_json ->> 'is_deleted')::BOOLEAN, FALSE);
        ELSIF TG_TABLE_NAME = 'book_metadata_translations' THEN
            SELECT to_jsonb(existing) INTO previous_json
            FROM book_metadata_translations existing
            WHERE existing.book_id = NEW.book_id AND existing.lang = NEW.lang;
        ELSIF TG_TABLE_NAME = 'section_translations' THEN
            SELECT to_jsonb(existing) INTO previous_json
            FROM section_translations existing
            WHERE existing.book_id = NEW.book_id
              AND existing.heading_id = NEW.heading_id
              AND existing.lang = NEW.lang;
        ELSIF TG_TABLE_NAME = 'book_heading_summaries' THEN
            SELECT to_jsonb(existing) INTO previous_json
            FROM book_heading_summaries existing
            WHERE existing.book_id = NEW.book_id
              AND existing.heading_id = NEW.heading_id
              AND existing.lang = NEW.lang;
        ELSIF TG_TABLE_NAME = 'section_audio' THEN
            SELECT to_jsonb(existing) INTO previous_json
            FROM section_audio existing
            WHERE existing.book_id = NEW.book_id
              AND existing.heading_id = NEW.heading_id
              AND existing.lang = NEW.lang;
        END IF;
    END IF;

    -- INSERT .. ON CONFLICT and UPDATE no-ops remain operationally safe. The
    -- timestamp is not content and is intentionally excluded from equality.
    IF TG_OP <> 'DELETE'
       AND previous_json IS NOT NULL
       AND (previous_json - 'updated_at') = (row_json - 'updated_at') THEN
        RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    END IF;

    target_book_id := (row_json ->> 'book_id')::INTEGER;
    target_lang := row_json ->> 'lang';
    visible_without_project := target_lang = 'ar'
        AND TG_TABLE_NAME IN ('section_translations', 'book_heading_summaries', 'section_audio');

    -- A book/lang identity move removes the old public asset and creates a new
    -- one. Guard the old identity independently so a move to a hidden project
    -- cannot bypass the same rule enforced for DELETE.
    IF TG_OP = 'UPDATE' THEN
        previous_book_id := (previous_json ->> 'book_id')::INTEGER;
        previous_lang := previous_json ->> 'lang';
        previous_visible_without_project := previous_lang = 'ar'
            AND TG_TABLE_NAME IN ('section_translations', 'book_heading_summaries', 'section_audio');

        IF was_present
           AND (
               previous_book_id IS DISTINCT FROM target_book_id
               OR previous_lang IS DISTINCT FROM target_lang
           )
           AND EXISTS (
               SELECT 1 FROM public_book_publications p WHERE p.book_id = previous_book_id
           )
           AND (
               previous_visible_without_project
               OR EXISTS (
                   SELECT 1
                   FROM book_production_projects pp
                   WHERE pp.book_id = previous_book_id
                     AND pp.lang = previous_lang
                     AND pp.publication_status = 'published'
                     AND pp.workflow_status = 'published'
               )
           ) THEN
            PERFORM assert_book_license_publish_permitted(
                previous_book_id,
                TG_TABLE_NAME || ' final asset move'
            );
        END IF;
    END IF;

    IF (was_present OR will_be_present)
       AND EXISTS (SELECT 1 FROM public_book_publications p WHERE p.book_id = target_book_id)
       AND (
           visible_without_project
           OR EXISTS (
               SELECT 1
               FROM book_production_projects pp
               WHERE pp.book_id = target_book_id
                 AND pp.lang = target_lang
                 AND pp.publication_status = 'published'
                 AND pp.workflow_status = 'published'
           )
       ) THEN
        PERFORM assert_book_license_publish_permitted(target_book_id, TG_TABLE_NAME || ' final asset update');
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_book_metadata_translations_license_guard ON book_metadata_translations;
CREATE TRIGGER trg_book_metadata_translations_license_guard
    BEFORE INSERT OR UPDATE OR DELETE ON book_metadata_translations
    FOR EACH ROW EXECUTE FUNCTION book_final_asset_license_guard();
DROP TRIGGER IF EXISTS trg_section_translations_license_guard ON section_translations;
CREATE TRIGGER trg_section_translations_license_guard
    BEFORE INSERT OR UPDATE OR DELETE ON section_translations
    FOR EACH ROW EXECUTE FUNCTION book_final_asset_license_guard();
DROP TRIGGER IF EXISTS trg_book_heading_summaries_license_guard ON book_heading_summaries;
CREATE TRIGGER trg_book_heading_summaries_license_guard
    BEFORE INSERT OR UPDATE OR DELETE ON book_heading_summaries
    FOR EACH ROW EXECUTE FUNCTION book_final_asset_license_guard();
DROP TRIGGER IF EXISTS trg_section_audio_license_guard ON section_audio;
CREATE TRIGGER trg_section_audio_license_guard
    BEFORE INSERT OR UPDATE OR DELETE ON section_audio
    FOR EACH ROW EXECUTE FUNCTION book_final_asset_license_guard();

-- Author/category translations are directly visible on their public list
-- endpoints and fan out to every visible Work. No production-project join is
-- appropriate here: one unresolved affected Work is enough to block a change.
CREATE OR REPLACE FUNCTION shared_final_asset_license_guard() RETURNS TRIGGER AS $$
DECLARE
    row_json JSONB;
    previous_json JSONB;
    target_owner_id INTEGER;
    previous_owner_id INTEGER;
    affected_owner_ids INTEGER[];
    affected_book_id INTEGER;
BEGIN
    IF TG_OP = 'DELETE' THEN
        row_json := to_jsonb(OLD);
        previous_json := row_json;
    ELSE
        row_json := to_jsonb(NEW);
        IF TG_OP = 'UPDATE' THEN
            previous_json := to_jsonb(OLD);
        ELSIF TG_ARGV[0] = 'author' THEN
            SELECT to_jsonb(existing) INTO previous_json
            FROM author_translations existing
            WHERE existing.author_id = NEW.author_id AND existing.lang = NEW.lang;
        ELSE
            SELECT to_jsonb(existing) INTO previous_json
            FROM category_translations existing
            WHERE existing.category_id = NEW.category_id AND existing.lang = NEW.lang;
        END IF;
    END IF;

    IF TG_OP <> 'DELETE'
       AND previous_json IS NOT NULL
       AND (previous_json - 'updated_at') = (row_json - 'updated_at') THEN
        RETURN NEW;
    END IF;

    target_owner_id := CASE
        WHEN TG_ARGV[0] = 'author' THEN (row_json ->> 'author_id')::INTEGER
        ELSE (row_json ->> 'category_id')::INTEGER
    END;
    previous_owner_id := CASE
        WHEN previous_json IS NULL THEN NULL
        WHEN TG_ARGV[0] = 'author' THEN (previous_json ->> 'author_id')::INTEGER
        ELSE (previous_json ->> 'category_id')::INTEGER
    END;
    affected_owner_ids := ARRAY(
        SELECT DISTINCT owner_id
        FROM unnest(ARRAY[target_owner_id, previous_owner_id]) AS owners(owner_id)
        WHERE owner_id IS NOT NULL
        ORDER BY owner_id
    );

    FOR affected_book_id IN
        SELECT DISTINCT b.id
        FROM books b
        JOIN public_book_publications p ON p.book_id = b.id
        LEFT JOIN book_metadata_edits me
          ON me.book_id = b.id
         AND me.status = 'published'
        WHERE (
            (TG_ARGV[0] = 'author' AND b.author_id = ANY(affected_owner_ids))
            OR (
                TG_ARGV[0] = 'category'
                AND COALESCE(me.category_id, b.category_id) = ANY(affected_owner_ids)
            )
        )
        ORDER BY b.id
    LOOP
        PERFORM assert_book_license_publish_permitted(
            affected_book_id,
            TG_TABLE_NAME || ' shared final asset update'
        );
    END LOOP;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_author_translations_license_guard ON author_translations;
CREATE TRIGGER trg_author_translations_license_guard
    BEFORE INSERT OR UPDATE OR DELETE ON author_translations
    FOR EACH ROW EXECUTE FUNCTION shared_final_asset_license_guard('author');
DROP TRIGGER IF EXISTS trg_category_translations_license_guard ON category_translations;
CREATE TRIGGER trg_category_translations_license_guard
    BEFORE INSERT OR UPDATE OR DELETE ON category_translations
    FOR EACH ROW EXECUTE FUNCTION shared_final_asset_license_guard('category');

-- Raw author/category metadata is also public and shared. Tombstoning remains
-- allowed because it only reduces visibility; any other material change fans
-- out through every currently visible affected Work.
CREATE OR REPLACE FUNCTION shared_raw_metadata_license_guard() RETURNS TRIGGER AS $$
DECLARE
    new_json JSONB := to_jsonb(NEW);
    old_json JSONB := CASE WHEN TG_OP = 'UPDATE' THEN to_jsonb(OLD) ELSE NULL END;
    target_owner_id INTEGER;
    previous_owner_id INTEGER;
    affected_owner_ids INTEGER[];
    affected_book_id INTEGER;
BEGIN
    IF TG_OP = 'UPDATE'
       AND (old_json - 'updated_at') = (new_json - 'updated_at') THEN
        RETURN NEW;
    END IF;
    IF COALESCE((new_json ->> 'is_deleted')::BOOLEAN, FALSE) THEN
        RETURN NEW;
    END IF;

    target_owner_id := (new_json ->> 'id')::INTEGER;
    previous_owner_id := CASE
        WHEN old_json IS NULL THEN NULL
        ELSE (old_json ->> 'id')::INTEGER
    END;
    affected_owner_ids := ARRAY(
        SELECT DISTINCT owner_id
        FROM unnest(ARRAY[target_owner_id, previous_owner_id]) AS owners(owner_id)
        WHERE owner_id IS NOT NULL
        ORDER BY owner_id
    );

    FOR affected_book_id IN
        SELECT DISTINCT b.id
        FROM books b
        JOIN public_book_publications p ON p.book_id = b.id
        LEFT JOIN book_metadata_edits me
          ON me.book_id = b.id
         AND me.status = 'published'
        WHERE (
            (TG_ARGV[0] = 'author' AND b.author_id = ANY(affected_owner_ids))
            OR (
                TG_ARGV[0] = 'category'
                AND COALESCE(me.category_id, b.category_id) = ANY(affected_owner_ids)
            )
        )
        ORDER BY b.id
    LOOP
        PERFORM assert_book_license_publish_permitted(
            affected_book_id,
            TG_TABLE_NAME || ' shared raw metadata update'
        );
    END LOOP;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_authors_raw_metadata_license_guard ON authors;
CREATE TRIGGER trg_authors_raw_metadata_license_guard
    BEFORE INSERT OR UPDATE ON authors
    FOR EACH ROW EXECUTE FUNCTION shared_raw_metadata_license_guard('author');
DROP TRIGGER IF EXISTS trg_categories_raw_metadata_license_guard ON categories;
CREATE TRIGGER trg_categories_raw_metadata_license_guard
    BEFORE INSERT OR UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION shared_raw_metadata_license_guard('category');

-- Replace the B-3 function in place so every Cross-Reference surface consumes
-- the same license-aware publication projection. Unit overrides are fail-
-- closed: a non-null override must itself be `permitted`.
CREATE OR REPLACE FUNCTION cross_reference_anchor_point_visible(point_value TEXT)
RETURNS BOOLEAN AS $$
DECLARE
    captures TEXT[];
    book_key INTEGER;
    heading_key INTEGER;
    heading_deleted BOOLEAN;
    book_derived BOOLEAN;
    has_active BOOLEAN;
    crossed_work BOOLEAN;
BEGIN
    captures := regexp_match(point_value, '^quran/([1-9][0-9]*)$');
    IF captures IS NOT NULL THEN
        RETURN EXISTS (
            SELECT 1 FROM quran_surahs WHERE surah_id = captures[1]::INTEGER
        );
    END IF;

    captures := regexp_match(point_value, '^quran/([1-9][0-9]*):([1-9][0-9]*)$');
    IF captures IS NOT NULL THEN
        RETURN EXISTS (
            SELECT 1 FROM quran_ayahs
            WHERE surah_id = captures[1]::INTEGER
              AND ayah_number = captures[2]::INTEGER
        );
    END IF;

    captures := regexp_match(point_value, '^kitab/([1-9][0-9]*)$');
    IF captures IS NOT NULL THEN
        RETURN EXISTS (
            SELECT 1
            FROM public_book_publications p
            WHERE p.book_id = captures[1]::INTEGER
        );
    END IF;

    captures := regexp_match(
        point_value,
        '^kitab/([1-9][0-9]*)/h/([0-9]+)/u/([1-9][0-9]*)$'
    );
    IF captures IS NOT NULL THEN
        book_key := captures[1]::INTEGER;
        IF NOT EXISTS (
            SELECT 1 FROM public_book_publications p WHERE p.book_id = book_key
        ) THEN
            RETURN FALSE;
        END IF;

        WITH RECURSIVE walk(
            id, book_id, lifecycle, effective_license_status, license_source
        ) AS (
            SELECT id, book_id, lifecycle, effective_license_status, license_source
            FROM citable_units_with_effective_license
            WHERE anchor = point_value
            UNION
            SELECT u.id, u.book_id, u.lifecycle,
                   u.effective_license_status, u.license_source
            FROM walk w
            JOIN citable_unit_lineage l ON l.predecessor_id = w.id
            JOIN citable_units_with_effective_license u ON u.id = l.successor_id
        )
        SELECT
            COALESCE(bool_or(
                lifecycle = 'active'
                AND book_id = book_key
                AND (license_source = 'edition' OR effective_license_status = 'permitted')
            ), FALSE),
            COALESCE(bool_or(book_id <> book_key), FALSE)
        INTO has_active, crossed_work
        FROM walk;

        RETURN has_active AND NOT crossed_work;
    END IF;

    captures := regexp_match(point_value, '^kitab/([1-9][0-9]*)/h/([1-9][0-9]*)$');
    IF captures IS NOT NULL THEN
        book_key := captures[1]::INTEGER;
        heading_key := captures[2]::INTEGER;

        SELECT h.is_deleted, b.units_derived_at IS NOT NULL
        INTO heading_deleted, book_derived
        FROM book_headings h
        JOIN books b ON b.id = h.book_id
        JOIN public_book_publications p ON p.book_id = h.book_id
        WHERE h.book_id = book_key AND h.heading_id = heading_key;

        IF NOT FOUND THEN
            RETURN FALSE;
        END IF;
        IF NOT heading_deleted THEN
            RETURN NOT book_derived OR EXISTS (
                SELECT 1
                FROM citable_units u
                WHERE u.corpus = 'kitab'
                  AND u.book_id = book_key
                  AND u.heading_id = heading_key
                  AND u.lifecycle = 'active'
                  AND (u.license_status IS NULL OR u.license_status = 'permitted')
            );
        END IF;

        WITH RECURSIVE walk(
            id, book_id, lifecycle, effective_license_status, license_source
        ) AS (
            SELECT id, book_id, lifecycle, effective_license_status, license_source
            FROM citable_units_with_effective_license
            WHERE book_id = book_key AND heading_id = heading_key
            UNION
            SELECT u.id, u.book_id, u.lifecycle,
                   u.effective_license_status, u.license_source
            FROM walk w
            JOIN citable_unit_lineage l ON l.predecessor_id = w.id
            JOIN citable_units_with_effective_license u ON u.id = l.successor_id
        )
        SELECT
            COALESCE(bool_or(
                lifecycle = 'active'
                AND book_id = book_key
                AND (license_source = 'edition' OR effective_license_status = 'permitted')
            ), FALSE),
            COALESCE(bool_or(book_id <> book_key), FALSE)
        INTO has_active, crossed_work
        FROM walk;

        RETURN has_active AND NOT crossed_work;
    END IF;

    RETURN FALSE;
END;
$$ LANGUAGE plpgsql STABLE COST 10;
