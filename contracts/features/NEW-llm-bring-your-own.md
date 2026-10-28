# Contract — NEW-llm-bring-your-own: AI runs only on a key the organisation or the user brings

- **Linear issue**: none yet (spec header: searches of team PAC found no issue for
  this change). Nothing was posted to Linear; this file and the manifest are the
  record. A PAC parent issue with one sub-issue per PR pair should be opened at
  intake, and the manifest's `linearIssueUrl` filled in then.
- **Spec**: `docs/specs/NEW-llm-bring-your-own.md`, revision 1 (2026-10-28),
  copied unchanged onto this branch from `factory/new-llm-bring-your-own-spec`
  at `6aeb3d8`. Its status line says "awaiting spec-challenger re-check": the
  challenge verdict was accept-with-changes and revision 1 states that every
  blocking finding was applied, but no re-check is recorded. This contract is
  written against revision 1; if the re-check changes the spec, this contract
  is revised (§9).
- **Stage**: contract-author, then contract-challenger.
- **Scope of this revision**: **Part 1 only** (spec §0: removal and key-only
  providers; ACs 1-21, 32-35, 37, 39, 40 Part 1 rows). Part 2 (the
  organisation-wide provider and key) is deliberately not in this revision;
  §8 says why and what its revision will add.
- **Fragments edited**: `contracts/openapi/paths/scheduling.yaml`,
  `contracts/openapi/paths/habits.yaml`. Bundle and `backend/api/gen`
  regenerated (§10).

---

## 1. What this contract is for

Today Paceday's AI features can run on the deployment's own cloud identity
(Bedrock, Azure OpenAI, Vertex), on an Ollama server, or on a key the user typed
(spec §1-§2). The owner decided on 2026-10-27 that Paceday runs and pays for no
LLM execution: only a key the organisation or the user supplies, no
deployment-level credential, no ambient identity, Ollama removed, and AI off and
saying so when nothing is set (spec Decisions O-1 to O-10).

At the HTTP boundary Part 1 changes four operations and adds four schemas:

| Operation | Change |
|---|---|
| `parseNaturalLanguage` (`POST /api/nlp/parse`) | New optional `not_configured` on the 200 when the caller has no AI configuration (nothing sent). `range_start` / `range_end` are absent when there is no range (API-080 fixed). Description of the key-only rule. |
| `testLLMConnection` (`POST /api/llm/test`) | Rewritten description: key-only provider modes, which configuration a probe uses (D-19), order of decisions, gateway path prefixes, Vertex global host. **New 409** `LLMNotConfiguredResponse`. 500 narrowed to settings-load failure. `llmProvider` set loses `ollama`, gains `organization`. |
| `refreshMeetingBrief` (`POST /api/meetings/{event_id}/brief/refresh`) | **New 409** `LLMNotConfiguredResponse`: nothing written, nothing fetched, nothing sent. |
| `getSettings` / `patchSettings` (`/api/settings`) | `llmProvider` set: `""`, `organization`, `unsupported`, five key-based providers. Keys bound to providers (`llmApiKeysSet`, `llmUnboundApiKeySet`). Read-only `aiStatus`. Removed: `awsProfile`, `gcpProject`, `gcpLocation`, `ollamaBaseUrl`, `ollamaModel`. New and changed 400 messages. |

New schemas: `AINotConfigured`, `LLMNotConfiguredResponse`, `AIStatus`,
`LLMApiKeysSet`. Changed: `NLPParseResult`, `LLMTestRequest`, `LLMTestResult`,
`LLMDestinationRefusal` (`property` loses `gcpLocation`, `awsProfile`,
`ollamaBaseUrl`), `Settings` / `SettingsUpdate` (shared property map).

No operation is added or removed in Part 1.

---

## 2. Provider modes, with the provider documentation

The spec decided (D-2, D-3, D-4, citing provider documentation fetched
2026-10-28) that each cloud provider is kept only in an API-key mode:

| Provider | Kept mode | Header | Request | Source |
|---|---|---|---|---|
| Azure OpenAI | API key | `api-key: <key>`, no `Authorization` | `<endpoint>/openai/deployments/<deployment>/chat/completions?api-version=<v>` | Microsoft Learn, *Azure OpenAI REST API reference*, "Authentication": "API Keys or Microsoft Entra ID"; with a key, requests "must include the API Key in the `api-key` HTTP header" |
| AWS Bedrock | Amazon Bedrock API key | `Authorization: Bearer <key>` | `https://bedrock-runtime.<region>.amazonaws.com/model/<modelId>/converse` | AWS, *Amazon Bedrock User Guide — API keys*: keys "authenticate API requests using a bearer token instead of AWS credentials" |
| Vertex AI | express-mode API key | `x-goog-api-key: <key>` (never `?key=`) | `https://aiplatform.googleapis.com/v1/publishers/google/models/<model>:generateContent` | Google Cloud, *Vertex AI in express mode overview*: "express mode uses an API key to authenticate"; URLs "omit the project ID and location" |

The contract states these on `testLLMConnection` because they are what AC-3, AC-4
and AC-5 observe (the header carrying the key, the host, the absence of any other
credential). The Bedrock path is the Converse operation of the Bedrock Runtime
API; if the plan finds the current client uses InvokeModel instead, the
contract's path is the one to follow or to revise here, not to diverge from
silently. `AWS_BEARER_TOKEN_BEDROCK` is named as ignored because the AWS SDK
would otherwise pick it up from the server's environment, which would be a
deployment-level credential (O-2).

---

## 3. How "AI not configured" is answered, per operation

The spec fixes the meaning (one row of §3's table, one reason, who can fix it;
not a server error, not a provider error) and leaves the status to this stage
(OQ-5).

**Chosen:**

- **Parse: 200**, `intent: "unknown"`, `error` = the sentence, plus
  `not_configured` (an `AINotConfigured`). `not_configured` is present only in
  this case, so its presence separates "not configured" from "the model did not
  understand you" (AC-13).
- **Probe and brief refresh: 409** with `LLMNotConfiguredResponse`
  (`{error, notConfigured}`).

The reason set and the "who can fix it" set are one schema, `AINotConfigured`,
used by all three operations and by the settings read (`aiStatus`), so the same
state gives the same reason everywhere (AC-40) and a client has one renderer.

**Why not one status for all three:**

- *Uniform 200.* The probe's 200 means "the provider answered"
  (`LLMTestResult.ok` is always `true`), and the web before this change renders
  any 200 as "Reached <provider>: …" (`smart-calendar-flow`
  `src/api/client.ts:1420-1428`). A 200 with `ok: false` would be shown as a
  success by the deployed web during the cross-repo window. A 200 on refresh
  would have to return a brief, and the only honest brief is the stored one,
  which needs a second response schema (the shared `MeetingBriefResponse` is
  also the response of `getMeetingBrief`, whose 400 carries an `x-uncertain`
  this change would then pull into scope).
- *Uniform 409.* Parse is 200 for every LLM-level outcome today, and both of its
  consumers rely on that: the web bar shows `error` from the 200, and the MCP
  tool `clockwise_parse_command` turns any status >= 400 into a tool error
  (`mcp/client.go:37-38`). AC-13 requires both to keep showing a sentence; the
  200 does that with no consumer change.
- *Reusing the 422.* The 422 means "configured, but the destination is not
  approved" and its `reason` enum is about destinations. Adding
  "not configured" reasons to it would make one status mean two different
  things to fix (a key versus an operator approval).

**Why 409.** The request is well formed and is refused only because of the
current state of the caller's AI configuration (RFC 9110 §15.5.10: "a conflict
with the current state of the target resource"). The body is a superset of the
JSON error shape, so a client that reads only `error` (the web settings page
before this change) shows the sentence instead of failing. Brief refresh has no
consumer at all today (neither the web app nor the MCP server calls it), so its
new status breaks nothing.

---

## 4. A stored choice that is no longer supported reads as `unsupported`

AC-7 forbids `ollama` in the contract; AC-8 requires an Ollama user's settings
read to be valid against the contract; AC-9 requires that writing back the
document read changes nothing stored. The read therefore needs a value for
"your stored choice is a provider Paceday no longer supports".

**Chosen:** `llmProvider: "unsupported"`, with `aiStatus.notConfigured.reason`
`provider_removed`. A write of `unsupported` is accepted only when the row
already reads as `unsupported` (a no-op under the changed-only rule PAC-24 AC-17
already has), otherwise 400. The stored `ollama` is not rewritten, so a rollback
restores it (D-10).

**Rejected:**

- *Read it as `""`.* That reports a choice the user never made, and writing the
  document back would switch the user to "off" (AC-9 forbids exactly that).
- *Omit `llmProvider` or make it nullable for these rows.* Every `Settings`
  property is required and none is nullable; the PAC-24 contract made that so
  because all three generators mishandle optional-on-read / required-on-write
  splits (`Settings` description). A nullable provider would also make `null`
  a write value, which PAC-24 forbids for every property.
- *Rewrite stored `ollama` rows to `""` in a migration.* Moves users onto a
  choice they did not make, and loses the information a rollback needs.

`unsupported` is a single generic value rather than `ollama_removed` so that a
future removal needs no new enum value; the reason sentence names Ollama.

---

## 5. Keys are bound to providers

D-5 binds a key to the provider it was entered for; AC-15 requires that
switching back makes the earlier key usable again; AC-16 requires that a key
stored before the change on a non-OpenAI/Anthropic account is never sent and
not deleted.

**Chosen:** one write-only `llmApiKey` that applies to the provider the row has
after the write; `""` removes that provider's key. Reads gain `llmApiKeysSet`
(one boolean per provider) and `llmUnboundApiKeySet`. `llmApiKeySet` keeps its
name and now means "a key bound to the selected provider". Any write carrying
`llmApiKey` also removes an unbound key; that is how Settings offers to
"replace or remove" it (spec §3) with no extra property.

**Rejected:**

- *Five write-only key properties (`openaiApiKey`, …).* Five secrets in one
  body, each needing its own write-only handling in three generators, and a
  client must map provider to property. The single property plus "the provider
  after this write" says the same thing with one secret.
- *Clear the key when the provider changes.* Deletes a user value the user did
  not ask to delete, and breaks AC-15's "changing back makes the earlier key
  usable again".
- *A separate operation to remove the unbound key.* A new operation for a
  one-time migration artefact; any key write already expresses the user's
  intent (a new key replaces it, `""` removes it).

A non-empty `llmApiKey` while the provider after the write is `""`,
`organization` or `unsupported` is a 400: there is no provider to bind it to,
and storing it unbound would recreate the artefact this section retires.

---

## 6. Order of decisions on a probe: 409 before 422

PAC-70 (PAC-66-B) made the destination check come before the key-presence
check, so an OpenAI probe with no key and an unapproved base URL was a 422. The
spec's table says that only rows 2 and 4 (configured) send anything, and AC-12 /
AC-40 say a provider with no key bound answers "needs a key". This contract
therefore decides the configuration first (409) and checks the destination only
for a configured caller (422). Nothing is sent in either case; what changes is
which problem the user is told about first, and the key is the one they must fix
first anyway.

This supersedes one PAC-70 statement and one test:
`TestPAC66B_RefusalDoesNotDependOnTheKey`
(`backend/api/pac66b_base_url_test.go:119-127`) asserts the 422
`destination_not_approved` for an OpenAI / Anthropic account with no key and an
unapproved base URL, and the parse/brief refusal sentence for the same account.
Under this contract that account is row 3: the probe answers the 409
`needs_key`, parse answers 200 with `not_configured`, and brief refresh the 409;
the "no requests" assertion stays. The plan must rewrite that test to these
answers and add a sibling with a key on file that keeps the 422 (spec D-11:
replaced, not skipped).

---

## 7. Other choices

- **`organization` exists in Part 1.** New accounts follow their organisation by
  default (O-5, spec §3), and a new account created before Part 2 must store
  that choice. Before Part 2 nobody can satisfy rows 4-5, so it reads as
  `nothing_configured`. Accepting `organization` from a user with no
  organisation is harmless (row 6, `enablers: [user]`) and avoids a stored
  default that the same user cannot write back.
- **The reason enum includes the Part 2 values now** (`organization_unconfirmed`,
  `provider_removed_organization_available`). Adding values to a response enum
  later is a breaking change for any client with an exhaustive switch; listing
  them now, documented as not produced before Part 2, makes Part 2 additive.
- **`aiStatus` on the settings read, not a new endpoint.** The web needs, per
  caller, whether AI works and why, before trying (AC-21). The settings read is
  already per caller. An "enabled providers" endpoint (PAC-70 OQ-B2) is not
  needed (owner decision O-9), and the provider list is now fixed.
  `aiStatus.inOrganization` tells the client whether to offer "use my
  organisation's AI" and to name the organisation's admin (AC-21); it carries
  nothing else about the organisation.
- **`aiStatus` does not check destinations.** A configured caller with a
  refused destination reads `configured: true`; the refusal is reported when a
  request is attempted, as today. Checking destinations on every settings read
  would put operator configuration into a read that has none today (cf. PAC-56).
- **Gateway path prefix** (O-10, AC-18): an operator entry in
  `PACEDAY_LLM_APPROVED_HOSTS_OPENAI` / `_ANTHROPIC` may be
  `https://<host>/<prefix>`; a user `llmBaseUrl` is approved only when it is
  exactly that origin and prefix (one optional trailing `/`), and the request
  goes to `<prefix>/v1/chat/completions` (or `/v1/messages`) only. An entry
  without a prefix still approves only the bare origin. Prefix comparison is
  exact and case-sensitive; the manifest's `operatorConfiguration` gives the
  entry grammar.
- **Vertex's approved host is the global `aiplatform.googleapis.com`**, approved
  through the existing `PACEDAY_LLM_APPROVED_HOSTS_VERTEX` (spec OQ-3, assumed),
  not built in. Operators who listed regional hosts for the ambient mode must
  add the global host; the release note must say so.
- **The probe ignores `llmModel` / `llmApiKey` when it names `organization` or
  `""`.** They can never be combined with an organisation configuration (D-18);
  refusing them would make the web's existing probe body (which always sends
  `llmModel`) a 400 for every follower.

---

## 8. Part 2 is not in this revision, and why

Part 2 needs an organisation-AI read, an admin-only write and a follower's
one-time confirmation. Its write gate is PAC-50's administrator proof, amended so
that a grant carries its purpose (spec D-6, AC-23). On 2026-10-28 PAC-50's
contract is on `factory/pac-50-contract` (revision 7) and its implementation on
`impl/pac-50-sso-write-gate-backend` (PR #212, open); neither is on `main`.
Writing Part 2 here would mean either referencing a step-up operation and grant
that do not exist in the merged contract, or amending a contract that is still
under challenge on another branch. The spec also says Part 2 must not reach
stage 4 without answers to OQ-1 and OQ-8 (spec, end of Revision 1), and that
Part 1 can land alone (§0).

What the Part 2 revision is expected to add (not binding; recorded so the
challenger can check Part 1 does not block it):

- `GET /api/org/ai` (every member, identical bytes, never the key: AC-24, D-7);
  `PUT` / `DELETE` gated by a purpose-bound PAC-50 grant or break-glass with a
  fresh session (AC-22, AC-23, AC-25, AC-30), 403 for a non-admin.
- A follower's confirmation write (D-15, AC-41), and the row-5 reason already in
  the enum.
- The probe's follower rules (AC-38) on the `organization` / absent cases this
  revision already routes.
- `AIStatus.provider` / a `source` member saying whether a configured caller
  runs on their own or the organisation's configuration.

Part 1's choices that Part 2 relies on: the `organization` choice value, the
full reason enum, `enablers` naming `organization_admin`, and the probe's
`organization` case.

---

## 9. Points for the challenger

1. §3: 200 for parse, 409 for probe and refresh. Try a request whose status
   differs for the same state on two operations other than by this rule.
2. §4: can any write sequence store `unsupported` on a row that is not Ollama,
   or move an Ollama row anywhere without the user choosing?
3. §5: can a key reach a provider it was not entered for, through PATCH then
   probe, or through the probe's absent-provider case?
4. §6: the 409-before-422 order supersedes a PAC-70 statement.
5. The spec revision 1 re-check is not recorded (header).

---

## 10. What was run

On branch `factory/new-llm-bring-your-own-contract`, from `main` at `69ffa92`:

- `python3 scripts/openapi_assemble.py` then `--check`: `OK: bundle is in sync
  (99 paths)`; 120 operations, 150 schemas, 8 `x-uncertain` (unchanged; none on
  an operation this contract edits).
- `bash scripts/openapi_gen_go.sh`: regenerated `backend/api/gen/paceday.gen.go`.
  `go build ./...` and `go vet ./...` in `backend/`: no output (success); the
  existing handlers convert to the generated types without naming removed
  constants, so the backend still compiles against this contract.
- `python3 scripts/openapi_migration_report.py --check`: `OK: MIGRATION.md covers
  all 120 operations (4 generated)`.
- `bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh
  factory/new-llm-bring-your-own-contract` on commit `d068418` (the fragments,
  bundle and gen code; this document and the manifest were added after and
  touch no code): `make openapi rc=0`, `regenerated: none`,
  `openapi-check(api) rc=0`, `backend lint rc=0`, `mcp lint rc=0`,
  `backend test rc=0`, `mcp test rc=0`, backend coverage 63.2% (floor 57%)
  `rc=0`, mcp coverage 84.0% (floor 75%) `rc=0`. The existing tests pass
  because no test validates a live response against these schemas; the
  behaviour the contract describes is not implemented yet (stage 4). The
  web gate (REGEN=1) and e2e gate belong to the build stage, which regenerates
  the web types with the implementation (spec §0, the PR pair); they were not
  run here.

One defect found and fixed while generating: an unquoted `off` in the reason
enum was read by the YAML 1.1 loader as boolean `false`. It is quoted (`'off'`).
