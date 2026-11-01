-- Reverses 029 (spec §7). The single old key slot gets the key bound to the
-- provider selected at rollback when that is openai or anthropic; otherwise
-- the unbound key (the slot's pre-upgrade key), or failing that the openai or
-- anthropic key bound at upgrade, so no key that existed before the upgrade
-- is lost. Keys entered after the upgrade for azure_openai, bedrock or vertex,
-- and any second key, are LOST: the old code cannot use them and they must be
-- re-entered after re-upgrading (release notes).
-- 'organization' ("follow my organisation") cannot be represented by the old
-- code; such rows return to the old default, 'ollama', as a new account of the
-- old schema would have.
UPDATE settings SET llm_api_key = CASE
        WHEN llm_provider = 'openai' AND llm_api_key_openai <> '' THEN llm_api_key_openai
        WHEN llm_provider = 'anthropic' AND llm_api_key_anthropic <> '' THEN llm_api_key_anthropic
        WHEN llm_api_key <> '' THEN llm_api_key
        WHEN llm_api_key_openai <> '' THEN llm_api_key_openai
        ELSE llm_api_key_anthropic
    END;
UPDATE settings SET llm_provider = 'ollama' WHERE llm_provider = 'organization';

ALTER TABLE settings DROP COLUMN IF EXISTS llm_api_key_openai;
ALTER TABLE settings DROP COLUMN IF EXISTS llm_api_key_anthropic;
ALTER TABLE settings DROP COLUMN IF EXISTS llm_api_key_azure_openai;
ALTER TABLE settings DROP COLUMN IF EXISTS llm_api_key_bedrock;
ALTER TABLE settings DROP COLUMN IF EXISTS llm_api_key_vertex;

ALTER TABLE settings ALTER COLUMN llm_provider SET DEFAULT 'ollama';
