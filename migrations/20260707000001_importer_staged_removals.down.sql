ALTER TABLE import_runs
    DROP COLUMN IF EXISTS approved_stage_run,
    DROP COLUMN IF EXISTS tombstoned_headings,
    DROP COLUMN IF EXISTS tombstoned_pages,
    DROP COLUMN IF EXISTS staged_removal_headings,
    DROP COLUMN IF EXISTS staged_removal_pages;

DROP TABLE IF EXISTS book_import_removal_stages;

ALTER TABLE book_headings
    DROP COLUMN IF EXISTS delete_reason,
    DROP COLUMN IF EXISTS deleted_at;

ALTER TABLE book_pages
    DROP COLUMN IF EXISTS delete_reason,
    DROP COLUMN IF EXISTS deleted_at;
