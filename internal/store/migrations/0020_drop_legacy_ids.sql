DROP INDEX IF EXISTS idx_projects_legacy_id;
DROP INDEX IF EXISTS idx_goals_legacy_id;
DROP INDEX IF EXISTS idx_tasks_legacy_id;
DROP INDEX IF EXISTS idx_decisions_legacy_id;

ALTER TABLE projects DROP COLUMN legacy_id;
ALTER TABLE goals DROP COLUMN legacy_id;
ALTER TABLE tasks DROP COLUMN legacy_id;
ALTER TABLE decisions DROP COLUMN legacy_id;
