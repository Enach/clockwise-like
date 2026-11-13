-- DESTROYS DATA: drops every Slack and Notion workspace connection, including their stored access and bot tokens (workspace_connections), and every generated meeting brief (meeting_briefs). Never run in production: roll back by restoring a backup (docs/runbook.md).
-- Reverses 014_workspace_connections.up.sql. The indexes go with their tables.
DROP TABLE IF EXISTS meeting_briefs;
DROP TABLE IF EXISTS workspace_connections;
