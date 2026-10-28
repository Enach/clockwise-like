# Plan — NEW-llm-bring-your-own, Part 1 (removal and key-only providers)

- **Spec**: `docs/specs/NEW-llm-bring-your-own.md` (revision 1; owner decisions
  of 2026-10-27 recorded as O-1..O-10, "owner, 2026-10-27").
- **Contract**: revision 2, `contracts/features/NEW-llm-bring-your-own.{yaml,md}`,
  `contractHash` `c014ab82c2823e38b108ee0aab068437763f42dd1dc345f5fab7c08207d5610a`.
- **Written by**: the build stage, because no plan existed when the build
  started. It records what the PR pair implements and why it stops at Part 1.

## Split

The acceptance list does not fit one reviewable PR pair, so it is split as the
spec's §0 says:

| Pair | Scope | Status |
|---|---|---|
| Part 1 (this pair) | Ollama and every ambient identity removed; Azure OpenAI, Bedrock and Vertex only in API-key mode; keys bound to providers; "AI not configured" answer on parse, probe and brief refresh and in the settings read; gateway path prefixes; API-080; web Settings and the AI-off notice. ACs 1-21, 32-35, 37, 39, Part 1 rows of 40; contract C-1..C-3. | implemented |
| Part 2 | Organisation-wide provider and key, settable only by a proven organisation admin (spec D-6: PAC-50's administrator proof, purpose-bound); members' read; follower confirmation (D-15). ACs 22-31, 36, 38, 41. | **blocked**: needs a Part 2 contract revision, which needs PAC-50's grant on `main` (PAC-50 PR #212 unmerged on 2026-10-28) and owner answers to OQ-1 and OQ-8 (spec, end of Revision 1). |

## Backend (`Enach/clockwise-like`)

Tests written first (`50ac999`, red against the unchanged server, then the
implementation `c982817`):

| Test | ACs |
|---|---|
| `backend/api/byok_test.go` | AC-2..AC-18, AC-35, AC-37, AC-40, C-1..C-3 (HTTP level, both recording transports, ambient AWS/Azure credentials in the environment) |
| `backend/nlp/no_ambient_identity_test.go` | AC-1 (grep of `backend/nlp` for the three default-credential loaders and their SDKs) |
| `backend/nlp/destination_internal_test.go` (`TestDecideSaved_*`, `TestDecideProbe`) | spec §3 table, D-19 |
| `backend/nlp/destination_test.go`, `user_destination_test.go` | AC-3..AC-5, AC-18; PAC-66 / PAC-66-B rules kept with keys bound |
| `backend/config/llm_user_destinations_test.go` | gateway entry grammar, AC-19 |
| `backend/config/deployment_files_test.go` | AC-20 |
| `backend/storage/migrations_029_test.go` | migration 029 up, down, up; AC-16, AC-17, spec §7 |
| `mcp/nlp_not_configured_test.go` | AC-13 (MCP tool relays the 200) |

Files: `backend/nlp/{providers,llm_factory,llm_azure_openai,llm_bedrock,llm_vertex,destination,user_destination,parser}.go`,
`backend/config/llm_user_destinations.go`, `backend/storage/{settings,settings_ai}.go`,
`backend/api/{handlers_llm,handlers_settings,handlers_meeting_briefs}.go`,
`backend/main.go`, `backend/go.{mod,sum}` (AWS and Azure SDKs dropped),
compose files, `.env.example`, `docs/factory/api-audit.md`.

**Migration 029** (`backend/storage/migrations/029_llm_bring_your_own.{up,down}.sql`):
default `llm_provider` becomes `organization`; five per-provider key columns;
an existing key is bound to the provider stored at upgrade when that is openai
or anthropic, otherwise left in `llm_api_key`, which now holds only the unbound
key. Nothing else is rewritten; the Ollama, AWS-profile and GCP columns stay
(D-10). Down: the old single slot gets the key bound to the provider selected
at rollback (openai/anthropic), else the unbound key, else the key bound at
upgrade; `organization` rows return to the old default `ollama`; added columns
dropped; default restored. Keys entered after the upgrade for Azure, Bedrock or
Vertex, and any second key, are lost by the down migration (spec §7; release
notes). Verified up, down, up by `TestMigration029_BindsKeysAndRollsBack`.

**Tests replaced, not skipped** (spec D-11): `nlp/bedrock_credentials_test.go`
(SigV4 via STS/IMDS) is deleted with the SDK and replaced by AC-1, AC-2, AC-4;
every Ollama test (`TestOllama_*`, `TestBuildLLMClient_Ollama`,
`TestOllamaClient_Complete_Mock`, `TestNLPService_Parse_WithOllama`,
`TestPAC66B_OllamaNotEnabledOnEveryPath`, `TestPAC66B_SettingsWriteWithOllamaNotEnabled`'s
Ollama half, `TestParseApprovedOrigins_*`) is replaced by AC-6, AC-10, AC-19 and
the row-8 case of `TestBYOK_EveryNotConfiguredRowGivesOneReason`; the regional
Vertex tests are replaced by `TestVertex_ExpressModeGlobalHostOnly` and C-1's
Vertex case; the keyless PAC-66 / PAC-66-B probe tests bind a key so the 422
stays pinned, and their keyless siblings assert the 409 (contract C-1, §6).

## Frontend (`Enach/smart-calendar-flow`)

Commits: regenerated types (`chore(contract)`), failing tests, implementation.

| Test | ACs |
|---|---|
| `src/lib/aiProviders.test.ts` | AC-33 (provider sets equal the generated enums; mock probe), AC-21 (who can enable) |
| `src/components/AINotConfiguredNotice.test.tsx` | AC-21 (notice, link, NLPBar shows it instead of an error) |
| `src/api/settingsAdapter.test.ts` | key flags, aiStatus, bedrockModel (D-16), llmUnboundApiKey, removed fields never sent |

Files: `src/api/{types,client}.ts`, `src/lib/aiProviders.ts`,
`src/components/{AINotConfiguredNotice,NLPBar}.tsx`, `src/pages/{Settings,Dashboard}.tsx`.

## e2e

`e2e/tests/llm-bring-your-own.spec.ts` (AC-9, AC-21, AC-34, AC-37, AC-39);
`llm-base-url.spec.ts` Ollama case replaced by AC-10; `llm-destination.spec.ts`
binds an Azure key (C-1); `TESTIDS-REQUIRED.md` adds `ai-off-notice`.

## Rollback

Revert both PRs and run `029_llm_bring_your_own.down.sql` (see Migration 029).
Reverting restores Ollama and the ambient identities, and with them spec §1's
problem. No `x-uncertain` is resolved or touched (contract `uncertainCheck`).
