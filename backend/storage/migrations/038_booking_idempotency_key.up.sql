-- PAC-82 (contracts/features/PAC-82.md section 5): the optional Idempotency-Key
-- of a public booking attempt, stored on the booking it saved. Every existing
-- row gets NULL, so the check and the partial unique index build on any data,
-- including existing duplicate or overlapping bookings (no exclusion
-- constraint, no repair of existing rows).
ALTER TABLE bookings ADD COLUMN idempotency_key TEXT;
ALTER TABLE bookings ADD CONSTRAINT bookings_idempotency_key_format
  CHECK (idempotency_key IS NULL OR idempotency_key ~ '^[A-Za-z0-9_-]{16,255}$');
CREATE UNIQUE INDEX bookings_link_idempotency_key
  ON bookings (link_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
