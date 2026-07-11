-- License evidence is external governance state, not disposable schema data.
-- Archive it in the pre-existing administrative audit log before removing the
-- typed B-4 table. A later B-4 up restores the exact decisions and timestamps.
INSERT INTO admin_audit_logs (
    id, actor_id, action, book_id, payload, created_at
)
SELECT md5(
           'b4-license-audit:'
           || audit.book_id::TEXT || ':'
           || audit.created_at::TEXT || ':'
           || audit.new_status || ':'
           || audit.actor_id::TEXT
       )::UUID,
       audit.actor_id,
       'license.decision.archive',
       audit.book_id,
       jsonb_build_object(
           'old_status', audit.old_status,
           'new_status', audit.new_status,
           'reason', audit.reason,
           'evidence_url', audit.evidence_url,
           'license_created_at', audit.created_at
       ),
       audit.created_at
FROM book_license_audits audit
ON CONFLICT (id) DO NOTHING;

-- The pre-B-4 reader only understands publication status. Materialize every
-- restricted takedown there before restoring old queries, so rollback cannot
-- resurrect a Work even for one request.
UPDATE book_publications publication
SET status = 'hidden',
    published_at = NULL,
    updated_at = now()
FROM books b
WHERE b.id = publication.book_id
  AND b.license_status = 'restricted'
  AND publication.status = 'published';

UPDATE book_production_projects project
SET publication_status = 'hidden',
    workflow_status = CASE
        WHEN project.workflow_status = 'published' THEN 'ready'
        ELSE project.workflow_status
    END,
    updated_at = now()
FROM books b
WHERE b.id = project.book_id
  AND b.license_status = 'restricted'
  AND project.publication_status = 'published';

-- Restore B-3 visibility before removing the B-4 views it depends on.
CREATE OR REPLACE FUNCTION cross_reference_anchor_point_visible(point_value TEXT)
RETURNS BOOLEAN AS $$
DECLARE
    captures TEXT[];
    book_key INTEGER;
    heading_key INTEGER;
    heading_deleted BOOLEAN;
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
            FROM books b
            JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
            WHERE b.id = captures[1]::INTEGER AND b.is_deleted = FALSE
        );
    END IF;

    captures := regexp_match(
        point_value,
        '^kitab/([1-9][0-9]*)/h/([0-9]+)/u/([1-9][0-9]*)$'
    );
    IF captures IS NOT NULL THEN
        book_key := captures[1]::INTEGER;
        IF NOT EXISTS (
            SELECT 1
            FROM books b
            JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
            WHERE b.id = book_key AND b.is_deleted = FALSE
        ) THEN
            RETURN FALSE;
        END IF;

        WITH RECURSIVE walk(id, book_id, lifecycle) AS (
            SELECT id, book_id, lifecycle
            FROM citable_units
            WHERE anchor = point_value
            UNION
            SELECT u.id, u.book_id, u.lifecycle
            FROM walk w
            JOIN citable_unit_lineage l ON l.predecessor_id = w.id
            JOIN citable_units u ON u.id = l.successor_id
        )
        SELECT
            COALESCE(bool_or(lifecycle = 'active' AND book_id = book_key), FALSE),
            COALESCE(bool_or(book_id <> book_key), FALSE)
        INTO has_active, crossed_work
        FROM walk;

        RETURN has_active AND NOT crossed_work;
    END IF;

    captures := regexp_match(point_value, '^kitab/([1-9][0-9]*)/h/([1-9][0-9]*)$');
    IF captures IS NOT NULL THEN
        book_key := captures[1]::INTEGER;
        heading_key := captures[2]::INTEGER;

        SELECT h.is_deleted INTO heading_deleted
        FROM book_headings h
        JOIN books b ON b.id = h.book_id AND b.is_deleted = FALSE
        JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
        WHERE h.book_id = book_key AND h.heading_id = heading_key;

        IF NOT FOUND THEN
            RETURN FALSE;
        END IF;
        IF NOT heading_deleted THEN
            RETURN TRUE;
        END IF;

        WITH RECURSIVE walk(id, book_id, lifecycle) AS (
            SELECT id, book_id, lifecycle
            FROM citable_units
            WHERE book_id = book_key AND heading_id = heading_key
            UNION
            SELECT u.id, u.book_id, u.lifecycle
            FROM walk w
            JOIN citable_unit_lineage l ON l.predecessor_id = w.id
            JOIN citable_units u ON u.id = l.successor_id
        )
        SELECT
            COALESCE(bool_or(lifecycle = 'active' AND book_id = book_key), FALSE),
            COALESCE(bool_or(book_id <> book_key), FALSE)
        INTO has_active, crossed_work
        FROM walk;

        RETURN has_active AND NOT crossed_work;
    END IF;

    RETURN FALSE;
END;
$$ LANGUAGE plpgsql STABLE COST 10;

DROP TRIGGER IF EXISTS trg_author_translations_license_guard ON author_translations;
DROP TRIGGER IF EXISTS trg_category_translations_license_guard ON category_translations;
DROP FUNCTION IF EXISTS shared_final_asset_license_guard();

DROP TRIGGER IF EXISTS trg_authors_raw_metadata_license_guard ON authors;
DROP TRIGGER IF EXISTS trg_categories_raw_metadata_license_guard ON categories;
DROP FUNCTION IF EXISTS shared_raw_metadata_license_guard();

DROP TRIGGER IF EXISTS trg_book_metadata_translations_license_guard ON book_metadata_translations;
DROP TRIGGER IF EXISTS trg_section_translations_license_guard ON section_translations;
DROP TRIGGER IF EXISTS trg_book_heading_summaries_license_guard ON book_heading_summaries;
DROP TRIGGER IF EXISTS trg_section_audio_license_guard ON section_audio;
DROP FUNCTION IF EXISTS book_final_asset_license_guard();

DROP TRIGGER IF EXISTS trg_books_raw_source_license_guard ON books;
DROP TRIGGER IF EXISTS trg_book_pages_raw_source_license_guard ON book_pages;
DROP TRIGGER IF EXISTS trg_book_headings_raw_source_license_guard ON book_headings;
DROP TRIGGER IF EXISTS trg_book_heading_ranges_raw_source_license_guard ON book_heading_ranges;
DROP FUNCTION IF EXISTS book_raw_source_license_guard();

DROP TRIGGER IF EXISTS trg_book_metadata_edits_license_guard ON book_metadata_edits;
DROP TRIGGER IF EXISTS trg_book_page_edits_license_guard ON book_page_edits;
DROP TRIGGER IF EXISTS trg_book_heading_edits_license_guard ON book_heading_edits;
DROP FUNCTION IF EXISTS book_published_overlay_license_guard();

DROP TRIGGER IF EXISTS trg_books_revoke_restricted_grandfather ON books;
DROP FUNCTION IF EXISTS revoke_restricted_book_grandfather();

DROP TRIGGER IF EXISTS trg_book_production_projects_license_guard ON book_production_projects;
DROP FUNCTION IF EXISTS book_production_project_license_guard();
DROP TRIGGER IF EXISTS trg_book_publications_license_guard ON book_publications;
DROP FUNCTION IF EXISTS book_publication_license_guard();

DROP TRIGGER IF EXISTS trg_books_license_audit ON books;
DROP FUNCTION IF EXISTS book_license_audit_guard();
DROP TRIGGER IF EXISTS trg_books_license_initial_status ON books;
DROP FUNCTION IF EXISTS book_license_initial_status_guard();
DROP TRIGGER IF EXISTS trg_book_license_audits_immutable ON book_license_audits;
DROP FUNCTION IF EXISTS book_license_audits_immutable_guard();
DROP FUNCTION IF EXISTS assert_book_license_publish_permitted(INTEGER, TEXT);
DROP FUNCTION IF EXISTS assert_license_status_publish_permitted(INTEGER, TEXT, TEXT);

DROP VIEW IF EXISTS book_license_audit_queue;
DROP VIEW IF EXISTS citable_units_with_effective_license;
DROP VIEW IF EXISTS public_book_publications;
DROP FUNCTION IF EXISTS book_effective_license_status(INTEGER);

DROP INDEX IF EXISTS idx_book_license_audits_book_created;
DROP TABLE IF EXISTS book_license_audits;
DROP TABLE IF EXISTS book_license_policy_state;
DROP INDEX IF EXISTS idx_books_license_status_id;

ALTER TABLE book_production_projects
    DROP CONSTRAINT IF EXISTS book_production_projects_license_grandfather_check,
    DROP CONSTRAINT IF EXISTS book_production_projects_publication_workflow_check,
    DROP COLUMN IF EXISTS license_grandfathered_at;
ALTER TABLE book_publications
    DROP CONSTRAINT IF EXISTS book_publications_license_grandfather_check,
    DROP COLUMN IF EXISTS license_grandfathered_at;

ALTER TABLE books
    DROP CONSTRAINT IF EXISTS books_license_updated_by_fk,
    DROP CONSTRAINT IF EXISTS books_license_status_check,
    DROP COLUMN IF EXISTS license_updated_at,
    DROP COLUMN IF EXISTS license_updated_by,
    DROP COLUMN IF EXISTS license_evidence_url,
    DROP COLUMN IF EXISTS license_reason,
    DROP COLUMN IF EXISTS license_status;
