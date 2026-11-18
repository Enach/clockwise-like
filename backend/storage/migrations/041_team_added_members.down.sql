-- PAC-129. Destroys every added person. Invitations stay lowercased; the older
-- duplicate pending invitations deleted by the up step cannot be restored
-- (their tokens stopped working at the up step). Paceday memberships,
-- including upgraded ones, are untouched. Development and tests only (runbook
-- "Rollback").
DROP INDEX IF EXISTS team_invites_one_pending;
ALTER TABLE team_invites DROP CONSTRAINT IF EXISTS team_invites_email_normalised;
DROP TABLE IF EXISTS team_added_members;
DROP FUNCTION IF EXISTS paceday_trim_ecma(text);
