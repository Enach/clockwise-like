-- PAC-84 down (contract 7.2). Run only by
-- `paceday-operator encryption downgrade-schema`, which performs the same
-- check in Go first (golang-migrate marks the version dirty before a down
-- step runs, so a refusal here would leave the database dirty). This DO
-- block is the backstop for any other runner. A value counts when it starts
-- with 'pdenc:' or is a damaged envelope (first six characters one off
-- 'pdenc:', rest '<digits>:<digits>:<base64url>'), exactly as the Go
-- classifier and storage.DamagedSQL decide (review #5, contract 3.1 note).
DO $$
DECLARE
    c record;
    found boolean;
BEGIN
    IF EXISTS (SELECT 1 FROM encryption_data_keys) THEN
        RAISE EXCEPTION 'PAC-84 down refused: encrypted values or data keys remain; run "paceday-operator encryption decrypt-all" first (docs/runbook.md, "Encryption key - downgrade")';
    END IF;
    FOR c IN SELECT * FROM (VALUES
        ('oauth_tokens', 'access_token'), ('oauth_tokens', 'refresh_token'),
        ('settings', 'microsoft_tokens'), ('settings', 'zoom_tokens'), ('settings', 'webcal_url'),
        ('settings', 'llm_api_key'), ('settings', 'llm_api_key_openai'), ('settings', 'llm_api_key_anthropic'),
        ('settings', 'llm_api_key_azure_openai'), ('settings', 'llm_api_key_bedrock'), ('settings', 'llm_api_key_vertex'),
        ('personal_calendars', 'credentials_json'), ('personal_calendars', 'url'),
        ('workspace_connections', 'access_token'), ('workspace_connections', 'bot_token'),
        ('org_ai_configurations', 'api_key'),
        ('sso_providers', 'oidc_client_secret'), ('sso_providers', 'saml_cert')) AS v(t, col)
    LOOP
        EXECUTE format(
            'SELECT EXISTS (SELECT 1 FROM %1$I WHERE left(%2$I, 6) = ''pdenc:'' OR ('
            || 'octet_length(%2$I) >= 11 AND substr(%2$I, 7) ~ ''^[0-9]+:[0-9]+:[A-Za-z0-9_-]+$'' AND '
            || '((substr(%2$I, 1, 1) <> ''p'')::int + (substr(%2$I, 2, 1) <> ''d'')::int + (substr(%2$I, 3, 1) <> ''e'')::int'
            || ' + (substr(%2$I, 4, 1) <> ''n'')::int + (substr(%2$I, 5, 1) <> ''c'')::int + (substr(%2$I, 6, 1) <> '':'')::int) = 1))',
            c.t, c.col) INTO found;
        IF found THEN
            RAISE EXCEPTION 'PAC-84 down refused: encrypted values or data keys remain; run "paceday-operator encryption decrypt-all" first (docs/runbook.md, "Encryption key - downgrade")';
        END IF;
    END LOOP;
END $$;

DROP TABLE encryption_backend_heartbeats;
DROP TABLE encryption_data_keys;
DROP SEQUENCE IF EXISTS encryption_data_keys_id_seq;

-- operator_audit and its widened CHECK are kept (contract 7.2 step 3): the
-- downgrade's own audit row would violate a narrower CHECK.
