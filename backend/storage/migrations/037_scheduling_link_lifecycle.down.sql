-- PAC-40 down (contracts/features/PAC-40.md §4, §7; spec §7). Cannot fail on
-- an erased row (its name and email are valid NOT NULL '') and cannot restore
-- an erased value: it is gone. Deleted links reappear as PAUSED (delete also
-- set active = false) and their owner can resume them; a later re-upgrade
-- does NOT bring the deletion back (challenge K4-8): record
-- `SELECT id FROM scheduling_links WHERE deleted_at IS NOT NULL` first.
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_booker_erased_blank;
ALTER TABLE bookings DROP COLUMN IF EXISTS booker_erased_at;

DROP INDEX IF EXISTS idx_scheduling_links_deleted;
ALTER TABLE scheduling_links DROP CONSTRAINT IF EXISTS scheduling_links_deleted_inactive;
ALTER TABLE scheduling_links DROP COLUMN IF EXISTS deleted_at;
