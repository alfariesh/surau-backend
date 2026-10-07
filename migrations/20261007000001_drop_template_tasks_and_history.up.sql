-- The task CRUD (tasks) and the Google-Translate history (history) tables came
-- from the original project template. No route, use case, or repository has
-- read or written either table since the Surau API replaced the template, so
-- they are dropped as orphan schema. The down migration restores the exact
-- shapes left by 20260403000002 and 20260403000003.
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS history;
