-- PAC-40 (contracts/features/PAC-40.md §4; spec docs/specs/PAC-40.md §3.1,
-- §3.5, §7): a deleted scheduling link is a state of its own, distinct from
-- paused, and a booking can record that its booker's name and email were
-- erased. Part A ships both columns; only part B's job ever sets
-- booker_erased_at.

-- (a) The deletion instant. Null for every existing row, so a link inactive
--     before this migration stays PAUSED (D-1, AC-12). Delete sets both
--     deleted_at and active = false; the CHECK makes a deleted link never
--     publicly bookable whatever writer touches the row (binding, K4-1).
ALTER TABLE scheduling_links ADD COLUMN deleted_at TIMESTAMPTZ NULL;
ALTER TABLE scheduling_links
    ADD CONSTRAINT scheduling_links_deleted_inactive
    CHECK (deleted_at IS NULL OR active = false);

-- The owner's archive (newest deletion first) and the erasure scan.
CREATE INDEX idx_scheduling_links_deleted
    ON scheduling_links (owner_user_id, deleted_at DESC)
    WHERE deleted_at IS NOT NULL;

-- (b) The erased state of a booking: name and email stay NOT NULL and hold ''
--     once erased (the reverted code reads them as blank, spec §7); on the
--     wire the archive answers null plus booker_erased_at (contract §2.2).
--     Every existing row passes (booker_erased_at is null); the check scans
--     bookings once, inside this migration's transaction.
ALTER TABLE bookings ADD COLUMN booker_erased_at TIMESTAMPTZ NULL;
ALTER TABLE bookings
    ADD CONSTRAINT bookings_booker_erased_blank
    CHECK (booker_erased_at IS NULL OR (booker_name = '' AND booker_email = ''));
