-- 028_credential_losses.down.sql — drops only the derived loss markers. No
-- credential is stored here, so none is lost (spec PAC-67 section 7): after a
-- rollback every stored credential is reported connected again, as it was
-- before PAC-67.
DROP TABLE IF EXISTS credential_losses;
