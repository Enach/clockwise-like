-- 035_identity_binding.up.sql — PAC-71 (spec docs/specs/PAC-71.md §3, §7;
-- contract contracts/features/PAC-71.md §5–§9; plan docs/plans/PAC-71.md §2).
--
-- An account belongs to the identity bound to it, not to an address. This
-- migration adds the binding, the address standing, the per-account session
-- generation, case-insensitive uniqueness for accounts created from now on,
-- and the operator audit record. It writes NO binding: every existing account
-- stays unbound (unconfirmed, generation 0) until its first sign-in under
-- migration rule (a) or (b), or an operator step (AC-18). No row is deleted.
--
-- Numbered 035, the next free version after main's 034_encryption_at_rest (PAC-84;
-- written as 032, renumbered 033 after PAC-72's 032 and 035 after PAC-84's 034,
-- contract mergeConditions). golang-migrate never applies a version below the
-- database's current one, so 033 would be skipped on a database already at 034.
-- Rows the pre-PAC-71 API inserts between this migration and the API deploy
-- (contract §14 step 1) get the column defaults below, so the new API treats
-- them as unbound legacy accounts.

-- 1. Session generation (spec §3 "Sessions", D-20). Every account starts at 0,
--    so every session issued before PAC-71 (which carries no `gen`, read as 0)
--    stays valid until the account binds or the operator advances it.
ALTER TABLE users ADD COLUMN session_generation BIGINT NOT NULL DEFAULT 0
    CHECK (session_generation >= 0);

-- 2. Address standing (spec §3 "Evidence and address standing", D-5).
ALTER TABLE users ADD COLUMN address_standing TEXT NOT NULL DEFAULT 'unconfirmed'
    CHECK (address_standing IN ('domain_confirmed', 'unconfirmed'));

-- 3. Case-insensitive uniqueness (spec D-17). email_key is lower(email) for
--    every account whose lower-cased address no other account carries; it is
--    NULL for existing case collisions (spec §3 "Case collisions": neither
--    merged nor deleted) and for rows the old API inserts. NULLs never
--    conflict, so the index can be created on a deployment with collisions.
--    users.email UNIQUE (exact case) is kept.
ALTER TABLE users ADD COLUMN email_key TEXT NULL;
UPDATE users u SET email_key = lower(u.email)
WHERE (SELECT count(*) FROM users o WHERE lower(o.email) = lower(u.email)) = 1;
CREATE UNIQUE INDEX users_email_key_unique ON users (email_key);
CREATE INDEX users_email_lower_idx ON users (lower(email));

-- 4. The bound identity (spec §3 "Identities"). The primary key on user_id is
--    "an account has at most one bound identity"; the unique triple is "an
--    identity is bound to at most one account". Identifiers are compared only
--    together with their authority:
--      Google:          provider 'google',    tenant '',             subject = userinfo id
--      Microsoft work:  provider 'microsoft', tenant = lower(tid),   subject = lower(/me id)
--      Organisation SSO provider 'oidc',      tenant = verified iss, subject = sub
CREATE TABLE user_identities (
    user_id   UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    provider  TEXT        NOT NULL CHECK (provider IN ('google', 'microsoft', 'oidc')),
    tenant    TEXT        NOT NULL DEFAULT '',
    subject   TEXT        NOT NULL CHECK (subject <> ''),
    evidence  TEXT        NOT NULL CHECK (evidence IN ('authoritative', 'mailbox')),
    bound_by  TEXT        NOT NULL CHECK (bound_by IN ('created', 'rule_a', 'rule_b', 'operator')),
    bound_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, tenant, subject)
);

-- 5. Operator audit (spec §3 "Operator procedure"). No foreign key to users,
--    so a record survives every later change to the account, and the down
--    migration keeps this table (spec §7). IF NOT EXISTS makes up -> down -> up
--    work with the kept table.
--    034_encryption_at_rest (PAC-84) runs first and already creates this table
--    with this definition, so the CREATE below is a no-op on every database
--    migrated in order. Its action CHECK restates ALL EIGHT actions (PAC-71's
--    three and PAC-84's five; PAC-84 contract 7.1/7.3, K-9): PAC-71 never
--    re-adds a narrower CHECK, whichever migration creates the table. Any later
--    migration that changes the action list restates all eight.
CREATE TABLE IF NOT EXISTS operator_audit (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    action            TEXT        NOT NULL CHECK (action IN (
                          'free_address', 'reassign', 'advance_all',
                          'encryption_convert', 'encryption_rotate_data_key', 'encryption_decrypt_all',
                          'encryption_reset_lost_key', 'encryption_downgrade_schema')),
    operator          TEXT        NOT NULL CHECK (operator <> ''),
    reason            TEXT        NOT NULL CHECK (reason <> ''),
    account_id        UUID        NULL,
    account_email     TEXT        NULL,
    previous_identity TEXT        NULL,
    new_identity      TEXT        NULL,
    evidence          TEXT        NULL,
    removed           JSONB       NOT NULL DEFAULT '[]'::jsonb,
    details           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
