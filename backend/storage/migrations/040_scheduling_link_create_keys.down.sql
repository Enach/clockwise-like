-- Destroys every stored link-create key, digest and co-host outcome. Links and
-- host rows are untouched. Development and tests only (runbook "Rollback").
DROP TABLE IF EXISTS scheduling_link_create_keys;
