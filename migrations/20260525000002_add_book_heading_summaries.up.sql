CREATE TABLE IF NOT EXISTS book_heading_summaries (
    book_id INTEGER NOT NULL,
    heading_id INTEGER NOT NULL,
    lang TEXT NOT NULL,
    summary TEXT NOT NULL,
    source TEXT,
    summary_status TEXT NOT NULL DEFAULT 'generated',
    reviewed_by TEXT,
    reviewed_at TIMESTAMPTZ,
    metadata JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, heading_id, lang),
    FOREIGN KEY (book_id, heading_id) REFERENCES book_headings(book_id, heading_id) ON DELETE CASCADE,
    CONSTRAINT book_heading_summaries_status_check
        CHECK (summary_status IN ('generated', 'reviewed')),
    CONSTRAINT book_heading_summaries_reviewed_by_check
        CHECK (summary_status <> 'reviewed' OR NULLIF(BTRIM(reviewed_by), '') IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_book_heading_summaries_lang ON book_heading_summaries(lang);
CREATE INDEX IF NOT EXISTS idx_book_heading_summaries_summary_trgm
    ON book_heading_summaries USING gin (summary gin_trgm_ops);
