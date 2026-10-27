-- 028_scheduling_links_deleted_at.up.sql — PAC-40: "deleted" becomes a state of
-- its own, distinct from "paused" (active = false). A link with deleted_at set
-- does not exist for any read path that names it; its row, bookings and host
-- rows are kept (spec D-2) and its slug stays reserved (D-3). Every row is NULL
-- at migration time: links that were inactive before this change are treated as
-- paused, because the old schema cannot tell which were deleted (D-1).
ALTER TABLE scheduling_links ADD COLUMN deleted_at TIMESTAMPTZ;
