-- 039_sso_stepup_denial_windows.up.sql — PAC-75 (contract §3): denied admin
-- step-up outcomes are recorded individually up to a bound per user and
-- 10-minute window; the rest are counted here. The row is also the lock that
-- makes the bound hold under concurrency and across api processes.
CREATE TABLE sso_stepup_denial_windows (
    user_id          UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    window_start     TIMESTAMPTZ NOT NULL,
    recorded         INTEGER     NOT NULL CHECK (recorded >= 1),
    left_out         INTEGER     NOT NULL DEFAULT 0 CHECK (left_out >= 0),
    last_left_out_at TIMESTAMPTZ NULL,
    PRIMARY KEY (user_id, window_start),
    CHECK (extract(epoch FROM window_start) % 600 = 0),
    CHECK ((left_out = 0) = (last_left_out_at IS NULL))
);
