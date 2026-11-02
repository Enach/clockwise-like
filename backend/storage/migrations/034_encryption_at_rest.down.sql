-- PAC-84 down (contract 7.2). Run only by
-- `paceday-operator encryption downgrade-schema`, which performs the same
-- check in Go first (golang-migrate marks the version dirty before a down
-- step runs, so a refusal here would leave the database dirty). This DO
-- block is the backstop for any other runner.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM encryption_data_keys)
       OR EXISTS (SELECT 1 FROM oauth_tokens WHERE left(access_token, 6) = 'pdenc:' OR left(refresh_token, 6) = 'pdenc:')
       OR EXISTS (SELECT 1 FROM settings WHERE left(microsoft_tokens, 6) = 'pdenc:' OR left(zoom_tokens, 6) = 'pdenc:'
                  OR left(webcal_url, 6) = 'pdenc:' OR left(llm_api_key, 6) = 'pdenc:'
                  OR left(llm_api_key_openai, 6) = 'pdenc:' OR left(llm_api_key_anthropic, 6) = 'pdenc:'
                  OR left(llm_api_key_azure_openai, 6) = 'pdenc:' OR left(llm_api_key_bedrock, 6) = 'pdenc:'
                  OR left(llm_api_key_vertex, 6) = 'pdenc:')
       OR EXISTS (SELECT 1 FROM personal_calendars WHERE left(credentials_json, 6) = 'pdenc:' OR left(url, 6) = 'pdenc:')
       OR EXISTS (SELECT 1 FROM workspace_connections WHERE left(access_token, 6) = 'pdenc:' OR left(bot_token, 6) = 'pdenc:')
       OR EXISTS (SELECT 1 FROM org_ai_configurations WHERE left(api_key, 6) = 'pdenc:')
       OR EXISTS (SELECT 1 FROM sso_providers WHERE left(oidc_client_secret, 6) = 'pdenc:' OR left(saml_cert, 6) = 'pdenc:')
    THEN
        RAISE EXCEPTION 'PAC-84 down refused: encrypted values or data keys remain; run "paceday-operator encryption decrypt-all" first (docs/runbook.md, "Encryption key - downgrade")';
    END IF;
END $$;

DROP TABLE encryption_backend_heartbeats;
DROP TABLE encryption_data_keys;
DROP SEQUENCE IF EXISTS encryption_data_keys_id_seq;

-- operator_audit and its widened CHECK are kept (contract 7.2 step 3): the
-- downgrade's own audit row would violate a narrower CHECK.
