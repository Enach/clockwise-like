CREATE TABLE IF NOT EXISTS manager_team_member_assignments (
    manager_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id         UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    member_email    TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (manager_user_id, team_id, member_email),
    FOREIGN KEY (manager_user_id, member_email)
        REFERENCES manager_team_members(manager_user_id, member_email)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_manager_team_assignments_team
    ON manager_team_member_assignments(team_id, manager_user_id);

-- Preserve associations that can be inferred without guessing: a detected
-- manager record belongs to a formal team when the same email is an accepted
-- member and the manager is also a member of that team.
INSERT INTO manager_team_member_assignments (manager_user_id, team_id, member_email)
SELECT mtm.manager_user_id, target_tm.team_id, mtm.member_email
FROM manager_team_members mtm
JOIN users target_user
  ON lower(target_user.email) = lower(mtm.member_email)
JOIN team_members target_tm
  ON target_tm.user_id = target_user.id
JOIN team_members manager_tm
  ON manager_tm.team_id = target_tm.team_id
 AND manager_tm.user_id = mtm.manager_user_id
WHERE target_user.id <> mtm.manager_user_id
ON CONFLICT DO NOTHING;

-- When a manager belongs to exactly one formal team, assigning their remaining
-- legacy roster to that sole team is deterministic and preserves old behavior.
INSERT INTO manager_team_member_assignments (manager_user_id, team_id, member_email)
SELECT mtm.manager_user_id, only_team.team_id, mtm.member_email
FROM manager_team_members mtm
JOIN (
    SELECT user_id, min(team_id::text)::uuid AS team_id
    FROM team_members
    GROUP BY user_id
    HAVING count(*) = 1
) only_team ON only_team.user_id = mtm.manager_user_id
ON CONFLICT DO NOTHING;
