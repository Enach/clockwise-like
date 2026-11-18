-- PAC-129 (spec docs/specs/PAC-129.md; contract contracts/features/PAC-129.md
-- §2.2, revision 1.2): people added to a team without an invitation or Paceday
-- access, and one stored-pending invitation per team and address.

-- PAC-129: the address trim set of contract §2.2 (revision 1.2, P2-B1, P2-M1):
-- exactly the 26 code points listed there, built from chr() only. No escape
-- string: an escaped v is the letter v in PostgreSQL, not U+000B.
CREATE FUNCTION paceday_trim_ecma(t text) RETURNS text
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT btrim(t, chr(9) || chr(10) || chr(11) || chr(12) || chr(13) || chr(32)
        || chr(133) || chr(160) || chr(5760) || chr(8192) || chr(8193) || chr(8194)
        || chr(8195) || chr(8196) || chr(8197) || chr(8198) || chr(8199) || chr(8200)
        || chr(8201) || chr(8202) || chr(8232) || chr(8233) || chr(8239) || chr(8287)
        || chr(12288) || chr(65279))
$$;

-- PAC-129: people added to a team without Paceday access.
CREATE TABLE team_added_members (
    team_id      UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    email        TEXT        NOT NULL,
    display_name TEXT        NOT NULL DEFAULT '',
    added_by     UUID        NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT team_added_members_pkey PRIMARY KEY (team_id, email),
    CONSTRAINT team_added_members_email_normalised
        CHECK (email = lower(paceday_trim_ecma(email)) AND email <> '')
);
CREATE INDEX team_added_members_email ON team_added_members (email);
CREATE INDEX team_added_members_added_by ON team_added_members (added_by)
    WHERE added_by IS NOT NULL;

-- Legacy invitations whose address is empty once trimmed (P2-m1): deleted, and
-- counted in the server log and to the migrating client.
DO $$
DECLARE n bigint;
BEGIN
    DELETE FROM team_invites WHERE paceday_trim_ecma(invitee_email) = '';
    GET DIAGNOSTICS n = ROW_COUNT;
    RAISE LOG 'PAC-129 migration 041: deleted % team_invites rows whose address is empty after trimming', n;
    RAISE NOTICE 'PAC-129 migration 041: deleted % team_invites rows whose address is empty after trimming', n;
END $$;

-- Invitations: one address spelling, lapsed rows swept, live pending rows
-- de-duplicated (keep the newest), then one stored-pending row per team and
-- address (spec §3.7, D-12, AC-26; SF-012, API-090).
UPDATE team_invites SET invitee_email = lower(paceday_trim_ecma(invitee_email))
 WHERE invitee_email <> lower(paceday_trim_ecma(invitee_email));
UPDATE team_invites SET status = 'expired'
 WHERE status = 'pending' AND expires_at <= now();
DELETE FROM team_invites t
 USING (SELECT id, row_number() OVER (PARTITION BY team_id, invitee_email
                                      ORDER BY created_at DESC, id DESC) AS rn
          FROM team_invites WHERE status = 'pending') d
 WHERE t.id = d.id AND d.rn > 1;
ALTER TABLE team_invites ADD CONSTRAINT team_invites_email_normalised
    CHECK (invitee_email = lower(paceday_trim_ecma(invitee_email)) AND invitee_email <> '');
CREATE UNIQUE INDEX team_invites_one_pending
    ON team_invites (team_id, invitee_email) WHERE status = 'pending';
