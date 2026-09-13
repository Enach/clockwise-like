ALTER TABLE manager_team_member_assignments
    ADD COLUMN display_name_override TEXT,
    ADD COLUMN cadence TEXT NOT NULL DEFAULT 'none'
        CHECK (cadence IN ('weekly', 'biweekly', 'monthly', 'custom', 'none')),
    ADD COLUMN cadence_custom_days INT
        CHECK (cadence_custom_days IS NULL OR cadence_custom_days BETWEEN 1 AND 365);

-- Migration 016 allowed legacy custom cadences without a valid custom-day
-- value. Copy only combinations accepted by the new team-scoped contract.
UPDATE manager_team_member_assignments a
SET cadence = CASE
        WHEN mtm.cadence = 'custom'
             AND mtm.cadence_custom_days BETWEEN 1 AND 365 THEN 'custom'
        WHEN mtm.cadence <> 'custom' THEN mtm.cadence
        ELSE 'none'
    END,
    cadence_custom_days = CASE
        WHEN mtm.cadence = 'custom'
             AND mtm.cadence_custom_days BETWEEN 1 AND 365
            THEN mtm.cadence_custom_days
        ELSE NULL
    END
FROM manager_team_members mtm
WHERE mtm.manager_user_id = a.manager_user_id
  AND mtm.member_email = a.member_email;

-- Migration 016 treated email identity as case- and whitespace-sensitive.
-- Migration 022 may therefore contain multiple FK-backed assignments that
-- collapse to the same canonical email. Temporarily remove that FK, retain
-- one deterministic assignment per team, and point it at the survivor below.
DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    FOR constraint_name IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'manager_team_member_assignments'::regclass
          AND confrelid = 'manager_team_members'::regclass
          AND contype = 'f'
    LOOP
        EXECUTE format(
            'ALTER TABLE manager_team_member_assignments DROP CONSTRAINT %I',
            constraint_name
        );
    END LOOP;
END $$;

WITH ranked AS (
    SELECT a.ctid,
           row_number() OVER (
               PARTITION BY a.manager_user_id, a.team_id,
                            lower(btrim(a.member_email))
               ORDER BY (a.cadence <> 'none') DESC,
                        (mtm.source = 'manual') DESC,
                        (mtm.member_user_id IS NOT NULL) DESC,
                        (btrim(mtm.display_name) <> '') DESC,
                        mtm.updated_at DESC,
                        mtm.created_at ASC,
                        mtm.id ASC
           ) AS position
    FROM manager_team_member_assignments a
    JOIN manager_team_members mtm
      ON mtm.manager_user_id = a.manager_user_id
     AND mtm.member_email = a.member_email
)
DELETE FROM manager_team_member_assignments a
USING ranked
WHERE a.ctid = ranked.ctid
  AND ranked.position > 1;

UPDATE manager_team_member_assignments
SET member_email = lower(btrim(member_email));

-- Select the canonical global identity deterministically. Manual names win;
-- otherwise prefer a linked Paceday user, a non-empty name, and recent data.
CREATE TEMP TABLE pace023_member_survivors ON COMMIT DROP AS
SELECT DISTINCT ON (manager_user_id, lower(btrim(member_email)))
       manager_user_id,
       lower(btrim(member_email)) AS canonical_email,
       id AS survivor_id
FROM manager_team_members
ORDER BY manager_user_id,
         lower(btrim(member_email)),
         (source = 'manual') DESC,
         (member_user_id IS NOT NULL) DESC,
         (btrim(display_name) <> '') DESC,
         updated_at DESC,
         created_at ASC,
         id ASC;

UPDATE manager_team_members survivor
SET member_user_id = COALESCE(
        survivor.member_user_id,
        (SELECT duplicate.member_user_id
         FROM manager_team_members duplicate
         WHERE duplicate.manager_user_id = survivor.manager_user_id
           AND lower(btrim(duplicate.member_email)) = s.canonical_email
           AND duplicate.member_user_id IS NOT NULL
         ORDER BY duplicate.updated_at DESC, duplicate.id ASC
         LIMIT 1)
    ),
    last_one_on_one_at = (
        SELECT max(duplicate.last_one_on_one_at)
        FROM manager_team_members duplicate
        WHERE duplicate.manager_user_id = survivor.manager_user_id
          AND lower(btrim(duplicate.member_email)) = s.canonical_email
    ),
    created_at = (
        SELECT min(duplicate.created_at)
        FROM manager_team_members duplicate
        WHERE duplicate.manager_user_id = survivor.manager_user_id
          AND lower(btrim(duplicate.member_email)) = s.canonical_email
    ),
    updated_at = (
        SELECT max(duplicate.updated_at)
        FROM manager_team_members duplicate
        WHERE duplicate.manager_user_id = survivor.manager_user_id
          AND lower(btrim(duplicate.member_email)) = s.canonical_email
    )
FROM pace023_member_survivors s
WHERE survivor.id = s.survivor_id;

DELETE FROM manager_team_members duplicate
USING pace023_member_survivors survivor
WHERE duplicate.manager_user_id = survivor.manager_user_id
  AND lower(btrim(duplicate.member_email)) = survivor.canonical_email
  AND duplicate.id <> survivor.survivor_id;

UPDATE manager_team_members
SET member_email = lower(btrim(member_email));
-- Keep the legacy global columns internally valid while old callers still
-- read them and while confirmation seeds a team's initial preference.
UPDATE manager_team_members
SET cadence = CASE
        WHEN cadence = 'custom' AND cadence_custom_days BETWEEN 1 AND 365
            THEN 'custom'
        WHEN cadence <> 'custom' THEN cadence
        ELSE 'none'
    END,
    cadence_custom_days = CASE
        WHEN cadence = 'custom' AND cadence_custom_days BETWEEN 1 AND 365
            THEN cadence_custom_days
        ELSE NULL
    END;


CREATE UNIQUE INDEX manager_team_members_email_ci
    ON manager_team_members(manager_user_id, lower(member_email));

CREATE UNIQUE INDEX manager_team_assignments_email_ci
    ON manager_team_member_assignments(manager_user_id, team_id, lower(member_email));

ALTER TABLE manager_team_members
    ADD CONSTRAINT manager_team_members_normalized_email
    CHECK (member_email = lower(btrim(member_email)));

ALTER TABLE manager_team_members
    ADD CONSTRAINT manager_team_members_custom_cadence CHECK (
        (cadence = 'custom' AND cadence_custom_days BETWEEN 1 AND 365)
        OR (cadence <> 'custom' AND cadence_custom_days IS NULL)
    );

ALTER TABLE manager_team_member_assignments
    ADD CONSTRAINT manager_team_assignments_normalized_email
    CHECK (member_email = lower(btrim(member_email)));

ALTER TABLE manager_team_member_assignments
    ADD CONSTRAINT manager_team_assignment_custom_cadence CHECK (
        (cadence = 'custom' AND cadence_custom_days IS NOT NULL)
        OR (cadence <> 'custom' AND cadence_custom_days IS NULL)
    ),
    ADD CONSTRAINT manager_team_assignments_member_fk
        FOREIGN KEY (manager_user_id, member_email)
        REFERENCES manager_team_members(manager_user_id, member_email)
        ON DELETE CASCADE;
