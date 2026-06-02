CREATE TABLE IF NOT EXISTS user_profiles (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    display_name TEXT,
    timezone TEXT,
    country_code CHAR(2),
    onboarding_version INTEGER NOT NULL DEFAULT 1,
    onboarding_completed_at TIMESTAMPTZ,
    personalization_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_profiles_onboarding_version_check CHECK (onboarding_version > 0),
    CONSTRAINT user_profiles_country_code_check CHECK (
        country_code IS NULL OR country_code ~ '^[A-Z]{2}$'
    )
);

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    preferred_ui_lang TEXT NOT NULL DEFAULT 'id',
    preferred_content_lang TEXT NOT NULL DEFAULT 'id',
    fallback_langs TEXT[] NOT NULL DEFAULT ARRAY['id']::TEXT[],
    arabic_level TEXT NOT NULL DEFAULT 'none',
    reader_mode TEXT NOT NULL DEFAULT 'arabic_translation',
    interests TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    daily_goal_minutes INTEGER,
    quran_translation_source_id TEXT,
    quran_recitation_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_preferences_preferred_ui_lang_check CHECK (preferred_ui_lang IN ('ar', 'id', 'en')),
    CONSTRAINT user_preferences_preferred_content_lang_check CHECK (preferred_content_lang IN ('ar', 'id', 'en')),
    CONSTRAINT user_preferences_fallback_langs_check CHECK (
        fallback_langs <@ ARRAY['ar', 'id', 'en']::TEXT[]
    ),
    CONSTRAINT user_preferences_arabic_level_check CHECK (
        arabic_level IN ('none', 'basic', 'intermediate', 'advanced', 'native')
    ),
    CONSTRAINT user_preferences_reader_mode_check CHECK (
        reader_mode IN ('arabic_translation', 'translation_only', 'arabic_only')
    ),
    CONSTRAINT user_preferences_daily_goal_minutes_check CHECK (
        daily_goal_minutes IS NULL OR (daily_goal_minutes > 0 AND daily_goal_minutes <= 1440)
    )
);

CREATE INDEX IF NOT EXISTS idx_user_preferences_preferred_content_lang
    ON user_preferences(preferred_content_lang);

CREATE INDEX IF NOT EXISTS idx_user_preferences_interests
    ON user_preferences USING gin(interests);

INSERT INTO user_profiles (user_id, created_at, updated_at)
SELECT id, now(), now()
FROM users
ON CONFLICT (user_id) DO NOTHING;

INSERT INTO user_preferences (user_id, created_at, updated_at)
SELECT id, now(), now()
FROM users
ON CONFLICT (user_id) DO NOTHING;
