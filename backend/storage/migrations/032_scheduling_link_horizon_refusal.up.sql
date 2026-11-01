-- 032_scheduling_link_horizon_refusal.up.sql — PAC-72 revision 2
-- (docs/specs/PAC-72.md "Revision 2 — owner answers 2026-11-01",
-- contracts/features/PAC-72.md, owner decisions D-1 and D-2/D-5).
--
-- * booking_horizon_days: how many days ahead a link can be booked, counting
--   today in the link owner's profile time zone (D-1: a per-link setting,
--   editable by the link owner). Every existing link gets 60, the fixed
--   horizon PAC-72 revision 1 applied to all links, so nothing that can be
--   booked today changes at upgrade.
-- * refusal_message: the link owner's choice of 409 body for a refused public
--   booking (D-2/D-5): 'generic' (one message, the default and every existing
--   link's value) or 'specific' (the reason).
--
-- The link's zone (D-9) is NOT stored here: it is the owner's
-- settings.timezone, read when the rule runs.
ALTER TABLE scheduling_links
    ADD COLUMN booking_horizon_days INTEGER NOT NULL DEFAULT 60
        CONSTRAINT scheduling_links_booking_horizon_days_check
        CHECK (booking_horizon_days BETWEEN 1 AND 365),
    ADD COLUMN refusal_message TEXT NOT NULL DEFAULT 'generic'
        CONSTRAINT scheduling_links_refusal_message_check
        CHECK (refusal_message IN ('generic', 'specific'));
