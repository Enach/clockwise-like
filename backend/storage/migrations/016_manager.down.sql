-- DESTROYS DATA: drops every manager profile and sharing choice (user_profiles), every manager's team list and 1:1 cadence (manager_team_members), the recorded 1:1 history (one_on_one_occurrences) and the cached team analytics (team_analytics_cache). Never run in production: roll back by restoring a backup (docs/runbook.md).
-- Reverses 016_manager.up.sql. The indexes go with their tables.
DROP TABLE IF EXISTS team_analytics_cache;
DROP TABLE IF EXISTS one_on_one_occurrences;
DROP TABLE IF EXISTS manager_team_members;
DROP TABLE IF EXISTS user_profiles;
