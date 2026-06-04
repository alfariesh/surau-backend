ALTER TABLE quran_recitations
    ADD COLUMN IF NOT EXISTS display_name TEXT,
    ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS default_priority INTEGER,
    ADD COLUMN IF NOT EXISTS is_visible BOOLEAN NOT NULL DEFAULT TRUE;

UPDATE quran_recitations
SET display_name = 'Mishari Rashid Al-Afasy',
    reciter_name = COALESCE(reciter_name, 'Mishari Rashid Al-Afasy'),
    style = COALESCE(style, 'murattal'),
    sort_order = 10,
    default_priority = 0,
    is_visible = TRUE
WHERE id = 'qul-ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953';

UPDATE quran_recitations
SET display_name = 'Yasser Al-Dosari',
    reciter_name = COALESCE(reciter_name, 'Yasser Al-Dosari'),
    style = COALESCE(style, 'murattal'),
    sort_order = 20,
    is_visible = TRUE
WHERE id = 'qul-surah-recitation-yasser-al-dosari';

CREATE INDEX IF NOT EXISTS idx_quran_recitations_visible_sort
    ON quran_recitations(is_visible, sort_order, display_name, id);

CREATE INDEX IF NOT EXISTS idx_quran_recitations_default_priority
    ON quran_recitations(default_priority, id)
    WHERE is_visible = TRUE AND default_priority IS NOT NULL;
