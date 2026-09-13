WITH rollback_preference AS (
    SELECT DISTINCT ON (manager_user_id, member_email)
           manager_user_id, member_email, cadence, cadence_custom_days
    FROM manager_team_member_assignments
    ORDER BY manager_user_id, member_email,
             (cadence <> 'none') DESC, created_at ASC, team_id ASC
)
UPDATE manager_team_members mtm
SET cadence = preference.cadence,
    cadence_custom_days = preference.cadence_custom_days,
    updated_at = now()
FROM rollback_preference preference
WHERE preference.manager_user_id = mtm.manager_user_id
  AND preference.member_email = mtm.member_email;
ALTER TABLE manager_team_member_assignments
    DROP CONSTRAINT IF EXISTS manager_team_assignments_member_fk;


ALTER TABLE manager_team_members
    DROP CONSTRAINT IF EXISTS manager_team_members_custom_cadence;

ALTER TABLE manager_team_member_assignments
    DROP CONSTRAINT IF EXISTS manager_team_assignments_normalized_email;
ALTER TABLE manager_team_members
    DROP CONSTRAINT IF EXISTS manager_team_members_normalized_email;
DROP INDEX IF EXISTS manager_team_assignments_email_ci;
DROP INDEX IF EXISTS manager_team_members_email_ci;
ALTER TABLE manager_team_member_assignments
    DROP CONSTRAINT IF EXISTS manager_team_assignment_custom_cadence,
    DROP COLUMN IF EXISTS cadence_custom_days,
    DROP COLUMN IF EXISTS cadence,
    DROP COLUMN IF EXISTS display_name_override;

-- Restore the migration-022 relationship after removing the constraint name
-- introduced by migration 023.
ALTER TABLE manager_team_member_assignments
    ADD CONSTRAINT manager_team_assignments_member_fk
        FOREIGN KEY (manager_user_id, member_email)
        REFERENCES manager_team_members(manager_user_id, member_email) ON DELETE CASCADE;
