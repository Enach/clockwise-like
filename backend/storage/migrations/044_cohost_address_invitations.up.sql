-- PAC-149 (spec docs/specs/PAC-149.md §3.5, D-8, D-17, AC-10; contract
-- contracts/features/PAC-149.md §4): a pending co-host invitation is to an
-- address, never to an account. Pending rows on live and paused links become
-- address invitations in place (same row id, invited_at and roster position);
-- pending rows on deleted links, for freed placeholder addresses, and those
-- that clash with an entry, the owner or another pending row are dropped.
-- Accepted and declined rows are not touched.
ALTER TABLE scheduling_link_hosts ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE scheduling_link_hosts ADD COLUMN invitee_email TEXT NULL;
ALTER TABLE scheduling_link_hosts ADD COLUMN invite_position INTEGER NOT NULL DEFAULT 0;

DO $$
DECLARE
    n_deleted_link bigint;
    n_placeholder  bigint;
    n_owner_self   bigint;
    n_entry_clash  bigint;
    n_pending_dup  bigint;
    n_holder       bigint;
    n_no_holder    bigint;
BEGIN
    -- 1. On a deleted link (spec D-14): dropped.
    DELETE FROM scheduling_link_hosts h
     USING scheduling_links l
     WHERE h.status = 'pending' AND l.id = h.link_id AND l.deleted_at IS NOT NULL;
    GET DIAGNOSTICS n_deleted_link = ROW_COUNT;

    -- 2. The account carries an operator placeholder (storage.FreedAddress).
    DELETE FROM scheduling_link_hosts h
     USING users u
     WHERE h.status = 'pending' AND u.id = h.user_id
       AND u.email = 'freed+' || u.id::text || '@paceday.invalid';
    GET DIAGNOSTICS n_placeholder = ROW_COUNT;

    -- 3. The owner's own pending row (an owner who invited their own address
    --    on a link with no owner row, before PAC-112 created owner rows):
    --    dropped. The link is left with no owner row (contract md 2.1 D-c).
    DELETE FROM scheduling_link_hosts h
     USING scheduling_links l
     WHERE h.status = 'pending' AND l.id = h.link_id AND h.user_id = l.owner_user_id;
    GET DIAGNOSTICS n_owner_self = ROW_COUNT;

    -- 4. Equal, under lower(), to the owner's stored address or to the stored
    --    address of an accepted or declined entry of the same link: dropped;
    --    the entry or owner row is kept unchanged.
    DELETE FROM scheduling_link_hosts h
     USING users u, scheduling_links l, users o
     WHERE h.status = 'pending' AND u.id = h.user_id
       AND l.id = h.link_id AND o.id = l.owner_user_id
       AND (lower(u.email) = lower(o.email)
            OR EXISTS (SELECT 1
                         FROM scheduling_link_hosts e
                         JOIN users eu ON eu.id = e.user_id
                        WHERE e.link_id = h.link_id
                          AND e.status <> 'pending'
                          AND lower(eu.email) = lower(u.email)));
    GET DIAGNOSTICS n_entry_clash = ROW_COUNT;

    -- 5. Equal converted addresses on one link (under lower()): keep the
    --    earliest by invited_at, then stored address bytewise, then row id.
    DELETE FROM scheduling_link_hosts h
     USING (SELECT h2.id,
                   row_number() OVER (PARTITION BY h2.link_id, lower(u2.email)
                                      ORDER BY h2.invited_at, u2.email COLLATE "C", h2.id) AS rn
              FROM scheduling_link_hosts h2
              JOIN users u2 ON u2.id = h2.user_id
             WHERE h2.status = 'pending') d
     WHERE h.id = d.id AND d.rn > 1;
    GET DIAGNOSTICS n_pending_dup = ROW_COUNT;

    -- 6. Count, then convert every remaining pending row in place.
    SELECT count(*) FILTER (WHERE c.holder), count(*) FILTER (WHERE NOT c.holder)
      INTO n_holder, n_no_holder
      FROM (SELECT (u.address_standing = 'domain_confirmed'
                    AND NOT EXISTS (SELECT 1 FROM users v
                                     WHERE lower(v.email) = lower(u.email)
                                       AND v.id <> u.id)) AS holder
              FROM scheduling_link_hosts h
              JOIN users u ON u.id = h.user_id
             WHERE h.status = 'pending') c;

    UPDATE scheduling_link_hosts h
       SET invitee_email = u.email, user_id = NULL
      FROM users u
     WHERE h.status = 'pending' AND u.id = h.user_id;

    RAISE LOG 'PAC-149 migration 044: converted % with a holder, % without; dropped % on deleted links, % placeholder, % owner''s own pending row, % clashing with an entry or the owner, % clashing with another pending row',
        n_holder, n_no_holder, n_deleted_link, n_placeholder, n_owner_self, n_entry_clash, n_pending_dup;
    RAISE NOTICE 'PAC-149 migration 044: converted % with a holder, % without; dropped % on deleted links, % placeholder, % owner''s own pending row, % clashing with an entry or the owner, % clashing with another pending row',
        n_holder, n_no_holder, n_deleted_link, n_placeholder, n_owner_self, n_entry_clash, n_pending_dup;
END $$;

ALTER TABLE scheduling_link_hosts
    ADD CONSTRAINT scheduling_link_hosts_pending_is_invitation
        CHECK ((status = 'pending') = (user_id IS NULL)),
    ADD CONSTRAINT scheduling_link_hosts_invitation_has_address
        CHECK ((user_id IS NULL) = (invitee_email IS NOT NULL)),
    ADD CONSTRAINT scheduling_link_hosts_invitation_address_not_empty
        CHECK (invitee_email IS NULL OR invitee_email <> '');

CREATE UNIQUE INDEX scheduling_link_hosts_invitation_unique
    ON scheduling_link_hosts (link_id, lower(invitee_email))
    WHERE invitee_email IS NOT NULL;

-- The invitee-side match reads by address across links.
CREATE INDEX scheduling_link_hosts_invitee_email
    ON scheduling_link_hosts (lower(invitee_email))
    WHERE invitee_email IS NOT NULL;
