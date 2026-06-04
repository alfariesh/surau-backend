DROP INDEX IF EXISTS idx_quran_recitations_default_priority;
DROP INDEX IF EXISTS idx_quran_recitations_visible_sort;

ALTER TABLE quran_recitations
    DROP COLUMN IF EXISTS is_visible,
    DROP COLUMN IF EXISTS default_priority,
    DROP COLUMN IF EXISTS sort_order,
    DROP COLUMN IF EXISTS display_name;
