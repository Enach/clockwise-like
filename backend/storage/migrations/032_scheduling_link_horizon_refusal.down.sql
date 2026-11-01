-- DESTROYS DATA: every link's booking_horizon_days and refusal_message setting (PAC-72 revision 2).
--
-- After this down, every link behaves as PAC-72 revision 1 did: a fixed
-- 60-day horizon and the one generic refusal message. A link owner who chose
-- another horizon or specific reasons loses that choice; release notes must
-- say so. Leaves 031 applied: the database returns to 031.
ALTER TABLE scheduling_links
    DROP COLUMN IF EXISTS refusal_message,
    DROP COLUMN IF EXISTS booking_horizon_days;
