-- NEW-llm-bring-your-own, Part 1 (docs/specs/NEW-llm-bring-your-own.md §7,
-- contracts/features/NEW-llm-bring-your-own.yaml repositories.backend.migration).
--
-- * New accounts follow their organisation by default (owner decision O-5);
--   before Part 2 that reads as "nothing configured" (spec row 6).
-- * One key per provider, each bound to the provider it was entered for
--   (spec D-5). An existing key is bound to the provider stored at upgrade
--   when that is openai or anthropic; otherwise it stays in llm_api_key,
--   which from now on holds only the key bound to no provider: never sent,
--   never deleted implicitly.
-- * No stored llm_provider value is rewritten, and the ollama_*, aws_profile,
--   gcp_project and gcp_location columns are left untouched (no longer read,
--   spec D-10), so the down migration restores the pre-upgrade state.
ALTER TABLE settings ALTER COLUMN llm_provider SET DEFAULT 'organization';

ALTER TABLE settings ADD COLUMN llm_api_key_openai TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN llm_api_key_anthropic TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN llm_api_key_azure_openai TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN llm_api_key_bedrock TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN llm_api_key_vertex TEXT NOT NULL DEFAULT '';

UPDATE settings SET llm_api_key_openai = llm_api_key, llm_api_key = ''
    WHERE llm_provider = 'openai' AND llm_api_key <> '';
UPDATE settings SET llm_api_key_anthropic = llm_api_key, llm_api_key = ''
    WHERE llm_provider = 'anthropic' AND llm_api_key <> '';
