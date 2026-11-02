-- PAC-84: encrypt stored secrets at rest (contracts/features/PAC-84.md
-- sections 4 and 7). The up step encrypts nothing (SQL has no key): the
-- backend's automatic conversion and `paceday-operator encryption convert`
-- do. No inventory column changes type, default or nullability (spec D-3).
--
-- RULE FOR EVERY LATER MIGRATION (contract 3.3): a stored secret is bound to
-- its table, column and row identity (AES-GCM associated data). A migration
-- can no longer move or copy a secret between rows or columns in SQL (as
-- 029_llm_bring_your_own did): the moved value would fail to decrypt. Such a
-- move needs a Go step that opens and re-seals each value.
--
-- Number: this pair is 034 because PAC-71 owns 033. The text works in either
-- merge order (contract 7.3): if PAC-71 has not merged, operator_audit is
-- created here with PAC-71's definition (033_identity_binding lines 61-74 on
-- impl/pac-71-identity-binding-backend); either way its action CHECK is
-- replaced by the eight-action one. ANY LATER MIGRATION THAT CHANGES THE
-- ACTION LIST RESTATES ALL EIGHT ACTIONS (contract 7.1, K-9).

CREATE TABLE IF NOT EXISTS operator_audit (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    action            TEXT        NOT NULL CHECK (action IN ('free_address', 'reassign', 'advance_all')),
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
ALTER TABLE operator_audit DROP CONSTRAINT IF EXISTS operator_audit_action_check;
ALTER TABLE operator_audit ADD CONSTRAINT operator_audit_action_check CHECK (action IN (
    'free_address', 'reassign', 'advance_all',
    'encryption_convert', 'encryption_rotate_data_key', 'encryption_decrypt_all',
    'encryption_reset_lost_key', 'encryption_downgrade_schema'));

-- Data keys (contract 4.1). At most one active; retired keys are kept. The
-- id sequence is never reset, so an id is never reused.
CREATE SEQUENCE encryption_data_keys_id_seq AS integer MINVALUE 1;
CREATE TABLE encryption_data_keys (
    id           INTEGER     PRIMARY KEY DEFAULT nextval('encryption_data_keys_id_seq'),
    state        TEXT        NOT NULL CHECK (state IN ('active', 'retired')),
    kek_id       TEXT        NOT NULL CHECK (kek_id ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$'),
    wrapped      BYTEA       NOT NULL CHECK (octet_length(wrapped) = 60),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    wrapped_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at   TIMESTAMPTZ NULL,
    sealed_count BIGINT      NOT NULL DEFAULT 0 CHECK (sealed_count >= 0),
    CHECK ((state = 'retired') = (retired_at IS NOT NULL))
);
ALTER SEQUENCE encryption_data_keys_id_seq OWNED BY encryption_data_keys.id;
CREATE UNIQUE INDEX encryption_data_keys_one_active
    ON encryption_data_keys ((true)) WHERE state = 'active';

-- Backend presence and seal lease (contract 4.3).
CREATE TABLE encryption_backend_heartbeats (
    instance_id UUID        PRIMARY KEY,
    hostname    TEXT        NOT NULL DEFAULT '',
    active_dek  INTEGER     NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    seen_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
