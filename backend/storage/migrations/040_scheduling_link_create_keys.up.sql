-- PAC-119 (contracts/features/PAC-119.md section 3): the Idempotency-Key of a
-- keyed link create, per account, with the link it created. The request digest
-- and the co-host outcomes are kept for the 24-hour replay window only and
-- cleared by the hourly cleanup; owner, key, link and time are kept for ever.
CREATE TABLE scheduling_link_create_keys (
    owner_user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idempotency_key TEXT        NOT NULL,
    link_id         UUID        NOT NULL REFERENCES scheduling_links(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    request_sha256  BYTEA       NULL,
    co_host_changes JSONB       NULL,
    CONSTRAINT scheduling_link_create_keys_owner_key PRIMARY KEY (owner_user_id, idempotency_key),
    CONSTRAINT scheduling_link_create_keys_link UNIQUE (link_id),
    CONSTRAINT scheduling_link_create_keys_key_format
        CHECK (idempotency_key ~ '^[A-Za-z0-9_-]{16,255}$'),
    CONSTRAINT scheduling_link_create_keys_digest_length
        CHECK (request_sha256 IS NULL OR octet_length(request_sha256) = 32),
    CONSTRAINT scheduling_link_create_keys_outcomes_need_digest
        CHECK (co_host_changes IS NULL OR request_sha256 IS NOT NULL)
);
CREATE INDEX scheduling_link_create_keys_retained
    ON scheduling_link_create_keys (created_at) WHERE request_sha256 IS NOT NULL;
