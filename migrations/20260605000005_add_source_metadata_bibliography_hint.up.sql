ALTER TABLE book_metadata_edits
    ADD COLUMN IF NOT EXISTS bibliography TEXT,
    ADD COLUMN IF NOT EXISTS hint TEXT;
