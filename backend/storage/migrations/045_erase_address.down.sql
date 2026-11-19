-- PAC-134. Drops paceday_go_lower (nothing else uses it: no index, CHECK or
-- generated column may, see the up step). operator_audit and its widened
-- ten-action CHECK are KEPT, as 034_encryption_at_rest.down and
-- 035_identity_binding.down keep theirs (PAC-84 contract 7.2, PAC-71): the
-- erase_address and erase_address_dry_run records are the accountability
-- record of erasures already done, which no down migration removes or
-- rewrites, and a narrower CHECK would refuse them. The 044 code never writes
-- those actions, so the wider list is inert under it.
DROP FUNCTION IF EXISTS paceday_go_lower(text);
