ALTER TABLE book_metadata_edits
    DROP COLUMN IF EXISTS hint,
    DROP COLUMN IF EXISTS bibliography;
