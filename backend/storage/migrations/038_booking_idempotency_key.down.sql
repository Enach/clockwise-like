-- PAC-82: loses the booking attempts' idempotency keys only; every booking row
-- survives (spec AC-22).
DROP INDEX IF EXISTS bookings_link_idempotency_key;
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_idempotency_key_format;
ALTER TABLE bookings DROP COLUMN IF EXISTS idempotency_key;
