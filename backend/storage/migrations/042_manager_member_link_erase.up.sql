-- PAC-130 (spec docs/specs/PAC-130.md D-6, AC-12; contract
-- contracts/features/PAC-130.md §4): erase every Manager roster link that does
-- not satisfy the visibility rule (§2.1) for the roster's owner. Kept links serve
-- an image rollback only; nothing reads them after PAC-130. Irreversible.
--
-- A link is kept only when the linked account itself carries the row's address
-- (compared lowercased) and shares a team (team_members, either role) or the
-- non-null organisation with the roster's owner. Applied with no backend
-- serving (PAC-116 stop-migrate-start); updated_at is not touched.
DO $$
DECLARE n bigint;
BEGIN
    UPDATE manager_team_members mtm
       SET member_user_id = NULL
     WHERE mtm.member_user_id IS NOT NULL
       AND NOT EXISTS (
           SELECT 1 FROM users u
            WHERE u.id = mtm.member_user_id
              AND lower(u.email) = lower(mtm.member_email)
              AND (EXISTS (SELECT 1 FROM team_members v
                             JOIN team_members m ON m.team_id = v.team_id
                            WHERE v.user_id = mtm.manager_user_id
                              AND m.user_id = u.id)
                   OR EXISTS (SELECT 1 FROM users o
                               WHERE o.id = mtm.manager_user_id
                                 AND o.org_id IS NOT NULL
                                 AND o.org_id = u.org_id)));
    GET DIAGNOSTICS n = ROW_COUNT;
    RAISE LOG 'PAC-130 migration 042: erased % manager roster account links', n;
    RAISE NOTICE 'PAC-130 migration 042: erased % manager roster account links', n;
END $$;
