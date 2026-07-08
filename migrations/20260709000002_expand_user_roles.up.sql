-- A-1: expand the user role set with curator + scholar_reviewer. Same
-- drop+re-add discipline as 20260530000002_add_editor_role (no NOT VALID; the
-- CHECK validates existing rows synchronously). Fixed constraint name + IN-list
-- order keeps the schema dump deterministic for the round-trip CI job.
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_role_check;

ALTER TABLE users
    ADD CONSTRAINT users_role_check
        CHECK (role IN ('user', 'editor', 'curator', 'scholar_reviewer', 'admin'));
