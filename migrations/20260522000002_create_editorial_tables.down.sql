DROP TABLE IF EXISTS admin_audit_logs;
DROP TABLE IF EXISTS book_heading_edits;
DROP TABLE IF EXISTS book_page_edits;
DROP TABLE IF EXISTS book_metadata_edits;
DROP TABLE IF EXISTS book_collection_items;
DROP TABLE IF EXISTS book_collections;
DROP TABLE IF EXISTS book_publications;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_role_check;

ALTER TABLE users
    DROP COLUMN IF EXISTS role;
