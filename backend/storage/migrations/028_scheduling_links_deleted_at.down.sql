-- 028_scheduling_links_deleted_at.down.sql — PAC-40. Loses only the deleted
-- marker: a deleted link was also set active = false, so after this it is
-- indistinguishable from a paused link and reappears in its owner's list, as
-- before PAC-40 (spec section 7). No link, booking or host row is removed.
ALTER TABLE scheduling_links DROP COLUMN IF EXISTS deleted_at;
