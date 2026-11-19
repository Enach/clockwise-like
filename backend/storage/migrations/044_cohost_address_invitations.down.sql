-- PAC-149 (contract contracts/features/PAC-149.md §4.2; spec §7, AC-10 down):
-- each pending invitation whose address has a single domain-confirmed holder
-- becomes that holder's pending row again, unless the holder owns the link or
-- already has an entry on it (the existing entry survives); every other
-- invitation is dropped. The owner's own pending rows dropped by the up
-- step are not restored. Never fails on UNIQUE (link_id, user_id). Lossy.
DROP INDEX scheduling_link_hosts_invitee_email;
DROP INDEX scheduling_link_hosts_invitation_unique;
ALTER TABLE scheduling_link_hosts
    DROP CONSTRAINT scheduling_link_hosts_invitation_address_not_empty,
    DROP CONSTRAINT scheduling_link_hosts_invitation_has_address,
    DROP CONSTRAINT scheduling_link_hosts_pending_is_invitation;

UPDATE scheduling_link_hosts h
   SET user_id = hu.id, invitee_email = NULL
  FROM users hu, scheduling_links l
 WHERE h.user_id IS NULL
   AND l.id = h.link_id
   AND lower(hu.email) = lower(h.invitee_email)
   AND hu.address_standing = 'domain_confirmed'
   AND NOT EXISTS (SELECT 1 FROM users v
                    WHERE lower(v.email) = lower(h.invitee_email) AND v.id <> hu.id)
   AND hu.id <> l.owner_user_id
   AND NOT EXISTS (SELECT 1 FROM scheduling_link_hosts e
                    WHERE e.link_id = h.link_id AND e.user_id = hu.id);

DELETE FROM scheduling_link_hosts WHERE user_id IS NULL;

ALTER TABLE scheduling_link_hosts
    DROP COLUMN invite_position,
    DROP COLUMN invitee_email;
ALTER TABLE scheduling_link_hosts ALTER COLUMN user_id SET NOT NULL;
