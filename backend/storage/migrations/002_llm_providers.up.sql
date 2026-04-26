ALTER TABLE settings ADD COLUMN aws_region TEXT NOT NULL DEFAULT 'us-east-1';
ALTER TABLE settings ADD COLUMN aws_profile TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN bedrock_model TEXT NOT NULL DEFAULT 'anthropic.claude-sonnet-4-5-20251001-v1:0';
ALTER TABLE settings ADD COLUMN azure_endpoint TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN azure_deployment TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN azure_api_version TEXT NOT NULL DEFAULT '2024-02-01';
ALTER TABLE settings ADD COLUMN gcp_project TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN gcp_location TEXT NOT NULL DEFAULT 'us-central1';
ALTER TABLE settings ADD COLUMN vertex_model TEXT NOT NULL DEFAULT 'gemini-2.0-flash-001';
ALTER TABLE settings ADD COLUMN ollama_base_url TEXT NOT NULL DEFAULT 'http://localhost:11434';
ALTER TABLE settings ADD COLUMN ollama_model TEXT NOT NULL DEFAULT 'llama3';

UPDATE settings SET llm_provider = 'ollama' WHERE llm_provider = '';
ALTER TABLE settings ALTER COLUMN llm_provider SET DEFAULT 'ollama';
