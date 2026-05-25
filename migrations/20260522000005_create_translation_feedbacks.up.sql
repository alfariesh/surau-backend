CREATE TABLE IF NOT EXISTS translation_feedbacks (
    id UUID PRIMARY KEY,
    book_id INTEGER NOT NULL,
    heading_id INTEGER NOT NULL,
    lang TEXT NOT NULL,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    client_id TEXT,
    vote TEXT NOT NULL,
    reason TEXT,
    note TEXT,
    user_agent TEXT,
    client_ip TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (book_id, heading_id, lang)
        REFERENCES section_translations(book_id, heading_id, lang)
        ON DELETE CASCADE,
    CONSTRAINT translation_feedbacks_vote_check
        CHECK (vote IN ('like', 'dislike')),
    CONSTRAINT translation_feedbacks_reason_check
        CHECK (reason IS NULL OR reason IN ('inaccurate', 'unclear', 'style', 'typo', 'formatting', 'other')),
    CONSTRAINT translation_feedbacks_client_id_check
        CHECK (client_id IS NULL OR NULLIF(BTRIM(client_id), '') IS NOT NULL),
    CONSTRAINT translation_feedbacks_note_check
        CHECK (note IS NULL OR char_length(note) <= 2000)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_translation_feedbacks_user_once
    ON translation_feedbacks (book_id, heading_id, lang, user_id)
    WHERE user_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_translation_feedbacks_client_once
    ON translation_feedbacks (book_id, heading_id, lang, client_id)
    WHERE user_id IS NULL AND client_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_translation_feedbacks_lookup
    ON translation_feedbacks (book_id, heading_id, lang, vote, updated_at DESC);
