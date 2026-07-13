-- Ordering marker: its down migration removes derived enrichment rows before
-- the two legacy broad uniqueness indexes are recreated by 00012/00011 down.
SELECT 1;
