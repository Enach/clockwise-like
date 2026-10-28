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
- **Stage**: contract-author, then contract-challenger. **Revision 2**
  (2026-10-28) answers the contract challenge of the same day (verdict
  accept-with-changes, four blocking findings); the challenge and the answer
  to each finding are at the end of this file ("Challenge", "Revision 2").
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
| `testLLMConnection` (`POST /api/llm/test`) | Rewritten description: key-only provider modes, which configuration a probe uses (D-19), order of decisions, gateway path prefixes, Vertex global host, model-ID path encoding (rev. 2). **New 409** `LLMNotConfiguredResponse`. 500 narrowed to settings-load failure. `llmProvider` set loses `ollama`, gains `organization` and (rev. 2) `unsupported`, the latter accepted only when it is the saved choice. |
| `refreshMeetingBrief` (`POST /api/meetings/{event_id}/brief/refresh`) | **New 409** `LLMNotConfiguredResponse`: nothing written, nothing fetched, nothing sent. 500 (rev. 2) also covers a settings-load failure, with nothing written. |
| `getSettings` / `patchSettings` (`/api/settings`) | `llmProvider` set: `""`, `organization`, `unsupported`, five key-based providers. Keys bound to providers (`llmApiKeysSet`, `llmUnboundApiKeySet`); (rev. 2) the unbound key is removed only by the write-only `llmUnboundApiKey: ""`. Read-only `aiStatus` (rev. 2: `notConfigured` present exactly when `configured` is false). Removed: `awsProfile`, `gcpProject`, `gcpLocation`, `ollamaBaseUrl`, `ollamaModel`. New and changed 400 messages. |

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

**The probe accepts the value the read returns (revision 2).** The web builds
its probe body from the settings document (`llmTestBody`,
`smart-calendar-flow` `src/api/client.ts:835-838` at `aa5c52e`, always sends
`llmProvider`), so an Ollama account's "Test connection" sends
`{"llmProvider":"unsupported", ...}`. `LLMTestRequest.llmProvider` therefore
accepts `unsupported` under the same rule as the settings write: only when the
caller's saved choice reads as `unsupported`, and it is then decided as the
saved choice (the row 7/8 409, a submitted key sent nowhere). Any other
`unsupported` probe is a 400. Rejected: keeping the enum and requiring clients
to omit the property when the document reads `unsupported`. That is a client
rule a generated type cannot carry, it breaks the deployed web for every
Ollama account during the cross-repo window, and it would give one state a 400
on the probe and a 409 / 200 `not_configured` on refresh and parse.

---

## 5. Keys are bound to providers

D-5 binds a key to the provider it was entered for; AC-15 requires that
switching back makes the earlier key usable again; AC-16 requires that a key
stored before the change on a non-OpenAI/Anthropic account is never sent and
not deleted.

**Chosen:** one write-only `llmApiKey` that applies to the provider the row has
after the write, and to nothing else; `""` removes that provider's key. Reads
gain `llmApiKeysSet` (one boolean per provider) and `llmUnboundApiKeySet`.
`llmApiKeySet` keeps its name and now means "a key bound to the selected
provider". The unbound legacy key is removed only by an explicit, write-only
`llmUnboundApiKey` whose only accepted value is `""` (revision 2); any other
value is a 400. Settings "offers to replace or remove it" (spec §3): remove is
`llmUnboundApiKey: ""`; replace is a key entered for the selected provider,
sent with `llmUnboundApiKey: ""` in the same write when the user asks for both.

**Rejected:**

- *Five write-only key properties (`openaiApiKey`, …).* Five secrets in one
  body, each needing its own write-only handling in three generators, and a
  client must map provider to property. The single property plus "the provider
  after this write" says the same thing with one secret.
- *Clear the key when the provider changes.* Deletes a user value the user did
  not ask to delete, and breaks AC-15's "changing back makes the earlier key
  usable again".
- *A separate operation to remove the unbound key.* A new operation for a
  one-time migration artefact; one write-only property of the existing write
  says the same thing.
- *Any write carrying `llmApiKey` removes the unbound key* (revision 1). It
  deletes a user value the user did not ask to delete (an account on Bedrock
  at upgrade that adds an OpenAI key loses its Bedrock-era key), contradicting
  spec D-5 ("never sent, never deleted"), §3 ("It is not deleted") and AC-16
  ("The key is still in storage"). The same argument that rejects clearing a
  key on provider change applies. Withdrawn by the challenge (blocking 2).

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

The same flip reaches further than revision 1 said (challenge, consumers 1).
Every probe case that sets a provider with no key and an unapproved, empty or
not-enabled destination now meets the 409 first:
`e2e/tests/llm-destination.spec.ts:36-56` (azure_openai, no key, endpoint
forced unapproved, expects the 422 `destination_not_approved azureEndpoint`);
in `backend/api/pac66_llm_destination_test.go`
`TestPAC66_ProbeRefusesAnUnapprovedStoredEndpoint` (:158),
`TestPAC66_ProbeRefusesAProviderWithNothingApproved` (:176),
`TestPAC66_ProbeEmptyAzureEndpointIsNotSet` (:428) and the other keyless probe,
parse and brief cases of that file (which run on the ambient identity this
change removes); and `pac66b_base_url_test.go`
`TestPAC66B_ProbeChecksTheProviderItUses` (:208). Rewriting them to expect the
409 would leave the PAC-66 destination check on `azure_openai`, `bedrock` and
`vertex` pinned by nothing, and that check is now what keeps a user's key off
an unapproved host. So the manifest lists each of them as **replaced, not
skipped: a key is bound to the provider so the 422 stays pinned**, and adds
acceptance test C-1: for each of `azure_openai`, `bedrock`, `vertex` with a
key bound and a refused or not-enabled destination, the probe is the 422 with
the PAC-66 `reason` / `property`, parse gives the refusal sentence with no
`not_configured`, brief refresh is 200 `failed`, zero requests; the same
account without a key is the 409 `needs_key`.

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
- **The probe does not send `llmModel` / `llmApiKey` when it names
  `organization`, `""` or `unsupported`.** In Part 1 each of these is always
  that row's 409, so the submitted values are sent nowhere; refusing them
  would make the web's existing probe body (which always sends `llmModel`) a
  400 for every follower. Revision 1 said they are "ignored" for
  `organization`, which contradicts spec D-19: naming a model or key makes a
  follower's probe NOT an organisation probe ("any other probe by a follower
  is a probe of the user's own configuration"). Part 1 does not need the
  difference (no organisation configuration exists); the Part 2 revision must
  define `{"llmProvider":"organization","llmApiKey":...}` by D-19 (most
  likely the user's own configuration, i.e. a 409 `needs_key` or a 400), not
  as "ignored" (§8).

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
  revision already routes, reconciled with spec D-19 for an `organization`
  probe that carries a model or key (§7).
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
6. Revision 2: `unsupported` on the probe (§4), `llmUnboundApiKey` (§5), the
   `aiStatus.notConfigured` presence rule, and the probe sentence describing
   the probed choice. Try a state in which the probe and the settings read
   still disagree.

---

## 10. What was run

This section records revision 1's runs; revision 2's are at the end of
"Revision 2".


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

---

## Challenge — 2026-10-28

Reviewer: contract-challenger, on `factory/new-llm-bring-your-own-contract` at
`b3565749`, committed state only. Read: the spec (revision 1), this file, the
manifest, the fragment diff against `origin/main` (`scheduling.yaml`,
`habits.yaml`), the handlers and tests named below, `mcp/`, `e2e/`, and
`Enach/smart-calendar-flow` at `origin/main` `aa5c52e`.

**Verdict**: accept-with-changes (four blocking findings; each fix is a contract
or manifest edit, none reopens the spec's shape).

### Requests wrongly permitted or forbidden

1. **The probe forbids the provider value the settings read returns.**
   An account stored on `ollama` reads `{"llmProvider":"unsupported", ...}`.
   The web builds the probe from that document:
   `llmTestBody` sends `llmProvider: String(s.llm_provider ?? "")` on every
   "Test connection" (`smart-calendar-flow` `src/api/client.ts:835-838`). So
   the request the deployed web makes for every Ollama account is
   `POST /api/llm/test {"llmProvider":"unsupported","llmModel":""}`, and
   `LLMTestRequest.llmProvider` (enum `['', organization, openai, anthropic,
   azure_openai, bedrock, vertex]`) makes it the 400
   `llmProvider must be one of: ...`, not the 409 `provider_removed` that the
   same account gets from parse (200 with `not_configured.reason:
   provider_removed`) and from `{}`. That is §9 point 1 failing: the same state
   answers 400 on one path and 409 on another, and the Ollama user (most
   accounts, spec §3) sees a validation error instead of "Ollama is no longer
   supported" (spec AC-10: "when they ... run the probe"). It also falsifies
   `LLMTestRequest`'s own statement that its property names match
   `SettingsUpdate` "so a client can build this from the document it already
   holds". **Fix**: add `unsupported` to `LLMTestRequest.llmProvider` with the
   same rule as the settings write: accepted only when the caller's saved row
   reads `unsupported`, then decided as the saved choice (the row 7/8 409, a
   submitted key sent nowhere); otherwise 400. Document it in the probe's
   "Which configuration is probed" list and in the 400 description (which
   today says `unsupported` is a 400 unconditionally). If the author prefers to
   keep the enum, the alternative is to state on `LLMTestRequest.llmProvider`
   that a client MUST omit the property when the document reads
   `unsupported`, and to list `llmTestBody` in `consumersAffected` as
   breaking; the first fix needs no client rule.

2. **Any key write deletes the legacy unbound key; the spec says it is never
   deleted.** Account on `bedrock` at upgrade with a stored key
   (`llmUnboundApiKeySet: true`). The user picks OpenAI and types an OpenAI
   key: `PATCH /api/settings {"llmProvider":"openai","llmApiKey":"sk-new"}`.
   The contract (`patchSettings`: a key "bound to no provider ... is removed by
   any write that carries `llmApiKey`, with any value") deletes the
   Bedrock-era key, though the user asked only to add an OpenAI key. Spec D-5:
   existing keys are bound "to nothing otherwise (never sent, never deleted)";
   spec §3: Settings "offers to replace or remove it. It is not deleted."; spec
   AC-16: "The key is still in storage." The manifest's AC-16 adds "until a
   write carrying llmApiKey", a silent narrowing of the spec's criterion. §5 of
   this file rejects clearing a key on provider change because it "deletes a
   user value the user did not ask to delete"; the same argument applies here,
   and the factory rule is never to choose an option that deletes user data
   without an owner answer. **Fix**: an `llmApiKey` write touches only the key
   of the provider after the write; the unbound key is removed only by an
   explicit request, e.g. a write-only `SettingsUpdate.llmUnboundApiKey` whose
   only accepted value is `""` (anything else 400). Restore the manifest's
   AC-16 to the spec's wording and add the payload above as a unit case (after
   it, `llmUnboundApiKeySet` is still `true`). If the author believes the
   implicit removal is what the spec meant by "replace", that is a spec change
   for spec-author and the owner, not a contract decision (OWNER item in
   Non-blocking).

### Responses the client cannot handle

1. **`aiStatus` may say "not configured" without saying why.** `AIStatus`
   requires only `configured` and `inOrganization`, and `notConfigured` has no
   presence rule anywhere (the property is a bare `$ref`; the description
   defines `provider` as "present exactly when `configured` is true" but says
   nothing about `notConfigured`). So `GET /api/settings` may return
   `"aiStatus": {"configured": false, "inOrganization": true}`, valid against
   the contract, and the Settings and dashboard notice of AC-21 has no reason
   and no enablers to render; equally `{"configured": true, "provider":
   "openai", "notConfigured": {...}}` is valid. AC-8 is tagged `[contract]` and
   requires the read to report the reason. **Fix**: on
   `AIStatus.notConfigured` add a description (as on
   `NLPParseResult.not_configured`; OpenAPI 3.1 allows it beside `$ref`)
   "present exactly when `configured` is false", say the same in the
   `AIStatus` description, and add a `[contract]` case to AC-8 asserting it for
   rows 1, 3, 6 and 8.

### Consumers affected

1. **The 409-before-422 order breaks the PAC-66 e2e journey and the keyless
   PAC-66 unit tests, and the manifest lists none of them.**
   `e2e/tests/llm-destination.spec.ts:36-56`: `PATCH {llmProvider:
   'azure_openai', azureEndpoint: APPROVED}` (no key), the endpoint is then
   forced to `https://attacker.example`, and `POST /api/llm/test {}` expects
   `422 {reason: destination_not_approved, property: azureEndpoint}`. Under
   this contract that caller is row 3 and the answer is
   `409 {notConfigured: {reason: needs_key, provider: azure_openai}}`, so the
   e2e gate fails. The same holds for every probe test that sets an
   ambient-era provider with no key: `backend/api/pac66_llm_destination_test.go`
   `TestPAC66_ProbeRefusesAnUnapprovedStoredEndpoint` (:158),
   `TestPAC66_ProbeRefusesAProviderWithNothingApproved` (:176, all three
   providers), `TestPAC66_ProbeEmptyAzureEndpointIsNotSet` (:428) and the other
   keyless probe cases in that file; and `pac66b_base_url_test.go`
   `TestPAC66B_ProbeChecksTheProviderItUses` (:208: an Ollama row whose stored
   key becomes unbound, so `{"llmProvider":"openai"}` is now the 409, not the
   422). §6 names only `TestPAC66B_RefusalDoesNotDependOnTheKey`;
   `consumersAffected` names only `llm-base-url.spec.ts` and `settings.spec.ts`.
   The risk is not only a red gate: rewritten to expect the 409, these tests
   would leave the PAC-66 destination check on `azure_openai`, `bedrock` and
   `vertex` pinned by nothing, and that check is now what keeps a user's key
   off an unapproved host. **Fix** (manifest): add each of these to
   `consumersAffected` as "replaced, not skipped: a key is bound to the
   provider so the 422 stays pinned", and add an acceptance test: for each of
   `azure_openai`, `bedrock`, `vertex` with a key bound and a refused or
   not-enabled destination, the probe is the 422 with the PAC-66 `reason` /
   `property`, parse gives the refusal sentence without `not_configured`, brief
   refresh is 200 `failed`, and zero requests are made; the same account
   without a key is the 409 `needs_key`.

Not breaking (checked):
- `mcp/`: no tool reads settings or calls the probe or brief refresh (grep of
  `mcp/*.go` for ollama, llmProvider, awsProfile, gcp, `llm/test`, brief: no
  match outside tests); `clockwise_parse_command` posts to `/api/nlp/parse` and
  only a status >= 400 is a tool error (`mcp/client.go:37-38`), so the 200 with
  `not_configured` reaches the tool as a body.
- `refreshMeetingBrief` has no caller in the web outside generated types
  (`src/api/generated/types.ts:1743`), none in `mcp/` or `e2e/`: the new 409
  breaks nothing.
- Web `withFallback` (`src/api/client.ts:614-627`) falls back to the mock only
  when the API is unreachable, so a 409 is thrown, not rendered as the mock's
  "Reached ... mock response".
- Web parse: `range_start` / `range_end` are optional in
  `src/api/types.ts:207-208` and the parse answer is not zod-validated
  (`client.ts:1281`), so the absent range and the extra `not_configured` member
  are safe for the deployed web.
- Web PATCH: `SETTINGS_PATCH_KEYS` is derived from the regenerated
  `SettingsUpdate.shape` (`client.ts:812-814`), so removed properties drop out
  of the PATCH after REGEN; the new read-only members are sent only when they
  differ from the last read, and are then ignored.
- `e2e/tests/settings.spec.ts:40-60`: the user chooses `openai` with the key,
  and the full-document write-back at :55 carries the new read-only members,
  which are accepted and ignored. Unaffected.

### Contract/implementation divergences

The contract is ahead of the code by design (stage 4 not started); the
manifest says so. Divergences that matter for the plan:

1. `refreshMeetingBrief`, settings-load failure: the handler ignores a
   `GetOrCreateSettingsByUser` error and generates with no LLM
   (`backend/api/handlers_meeting_briefs.go:75-90`), which writes a `pending`
   and then a `failed` brief, the overwrite AC-14 forbids. "Before anything
   else the server decides which AI configuration" does not say what happens
   when that cannot be decided, and the 500 says only "Brief generation
   returned an error". Non-blocking; the 500 description should add "or the
   caller's settings could not be loaded; nothing was written".
2. Today's probe echoes `provider: ""` for an unset provider and answers 500
   for no key (`backend/api/handlers_llm.go:53,75`); the 409 and the narrowed
   `LLMTestResult.provider` replace both. Covered by AC-11 and AC-12.

### Uncertainties not actually resolved

None claimed. `resolvesUncertain` is empty; the one `x-uncertain` near the
edited operations (`habits.yaml:475`) belongs to `getMeetingBrief`'s 400,
which is not edited, and `refreshMeetingBrief`'s 400 carries none. API-080 is
claimed only as "fixed in the contract, marked Fixed by the implementation
PR", which is accurate.

### Verified clean

Attacked and could not break:
- §9 point 3, a key reaching the wrong provider. OpenAI key bound, then
  `PATCH {"llmProvider":"anthropic"}`: the contract gives `llmApiKeySet: false`
  and `llmApiKeysSet.openai: true`; `POST /api/llm/test {}` and
  `{"llmProvider":"anthropic"}` are the 409 `needs_key` anthropic; no
  documented path pairs the OpenAI key with Anthropic. A follower
  (`organization`) probing `{"llmApiKey":"sk-x"}` or
  `{"llmProvider":"organization","llmApiKey":"sk-x"}` is the 409
  `nothing_configured` and the key is sent nowhere.
- §9 point 2, storing `unsupported`. `PATCH {"llmProvider":"unsupported"}` on an
  `openai` row is the 400 `llmProvider unsupported cannot be chosen: ...`; on
  an Ollama row it is a no-op. `PATCH {"llmApiKey":"sk"}` on an Ollama row is
  the 400 `llmApiKey cannot be set: ...`. An Ollama row changes provider only
  through an explicit `llmProvider` in the body.
- The PAC-24 closed probe body survives:
  `{"llmProvider":"openai","llmBaseUrl":"https://attacker.example"}` and
  `{"awsRegion":"x"}` are still 400 (`additionalProperties: false`), and no
  remaining `LLMTestRequest` property reaches a URL.
- Gateway prefixes: an entry `https://gw.example/openai` approves
  `https://gw.example/openai` and `.../openai/` only; `https://gw.example`,
  `https://gw.example/other`, `https://gw.example/openai/x` and
  `https://gw.example/openai?x=1` are refused on write (400) and on send (422);
  the request path is exactly `<prefix>/v1/chat/completions`. Matches spec
  AC-18 and the owner's answer to PAC-70 OQ-B6.
- Owner decisions honoured: no `ollama` in any provider enum of the bundle
  (checked by loading `contracts/openapi/openapi.yaml`: `Settings` /
  `SettingsUpdate`, `LLMTestRequest`, `LLMTestResult`, `AINotConfigured`,
  `AIStatus`, `LLMApiKeysSet`); no ambient identity in any provider mode; Azure,
  Bedrock and Vertex only in API-key modes; no enabled-providers endpoint;
  keys write-only (PAC-24).
- `AINotConfigured.reason` loads as the string `off`, not boolean `false`
  (the quoting fix holds in the bundle).
- New accounts reach row 6: `GetOrCreateSettingsByUser` inserts only `user_id`
  (`backend/storage/settings.go:246`), so the migration's new column default
  applies and AC-34 is reachable on the e2e stack.
- Status per operation (§3): the parse 200 keeps both consumers working (web bar
  shows `error`; MCP treats >= 400 as a tool error); 409 on probe and refresh.
  Within each operation one state gives one status, apart from blocking
  finding 1.
- `contractHash` reproduced: `sha256sum contracts/openapi/openapi.yaml` =
  `dae3dce9...77c1f9`, as in the manifest.
- Gate run by this review on `b3565749`: (filled in by contract-author from the gate log, because this review returned before the run ended) `make openapi rc=0`, `regenerated: none`, `openapi-check(api) rc=0` (bundle in sync, 99 paths; MIGRATION.md covers 120 operations; backend/api/gen matches), `backend lint rc=0`, `mcp lint rc=0`, `backend test rc=0`, **`mcp test rc=1`** (the gate's filter captured no FAIL or test line, so the failing step is not identified; see Revision 2, Non-blocking 2), backend coverage 63.2% (floor 57%) `rc=0`, mcp coverage 84.0% (floor 75%) `rc=0`. Web generated types SKIPPED by the api gate (`WEB=/nonexistent`). Log: `C:/Users/jouzu/paceday/.claude/factory/logs/api-gate-factory_new-llm-bring-your-own-contract.log`.

### Non-blocking

1. AC-33 says the web's provider values equal "the contract's set", but the
   contract now has three: `Settings.llmProvider` (8 values),
   `LLMTestRequest.llmProvider` (7), and the provider sets of `LLMTestResult`,
   `AINotConfigured`, `AIStatus` and `LLMApiKeysSet` (5). Name which set each
   web use must equal.
2. The 409 sentence is specified as "AI features are off ...". On a probe of an
   unsaved provider (AC-37) or `{"llmApiKey":""}` while the saved
   configuration works, AI is not off, and Settings would tell a working
   account "AI features are off". Let the probe's message describe the probed
   choice ("Anthropic needs an API key") rather than the caller's AI state.
3. §7 says the probe ignores `llmModel` / `llmApiKey` when it names
   `organization`. Spec D-19 says a follower's probe uses the organisation
   configuration only when it names no model or key. Harmless in Part 1
   (always the 409), but the Part 2 revision must reconcile this with D-19, or
   the spec must change.
4. `bedrockModel` is a URL path segment and Bedrock model IDs contain `:` (the
   DB default `anthropic.claude-sonnet-4-5-20251001-v1:0`). Say whether the
   segment is percent-encoded, so AC-4's ".../model/<model>/converse" is one
   exact path.
5. The spec's revision-1 re-check by spec-challenger is still not recorded
   (header of this file); this challenge does not substitute for it.
6. OWNER: (blocking finding 2) should a key stored before the upgrade on a
   non-OpenAI/Anthropic account be removed implicitly by the user's next key
   entry, or only when the user explicitly removes it? The spec (D-5, assumed
   by the factory) says never deleted; the contract should follow the spec
   until the owner answers.

---

## Revision 2 — 2026-10-28

Author: contract-author, answering the Challenge above (on `b356574`). Every
finding, blocking and non-blocking, is answered here; nothing was skipped.
Fragments edited: `contracts/openapi/paths/scheduling.yaml`,
`contracts/openapi/paths/habits.yaml`; bundle and `backend/api/gen`
regenerated. The spec is not edited by this stage.

### Blocking

1. **The probe rejected the value the settings read returns.** Applied the
   first fix. `LLMTestRequest.llmProvider` is now the same eight values as
   `Settings.llmProvider`, including `unsupported`. `unsupported` is accepted
   only when the caller's saved choice reads as `unsupported` and is then
   decided as the saved choice: the row 7/8 409 (`provider_removed`; after
   Part 2 possibly `provider_removed_organization_available`), with a
   submitted `llmModel` / `llmApiKey` sent nowhere. Any other `unsupported`
   probe is the 400 `llmProvider unsupported can be probed only when it is
   your saved choice`. Documented in the probe's "Which configuration is
   probed" list, in the 400 description (which no longer calls `unsupported`
   a 400 unconditionally) and in `LLMTestRequest`'s description, which now
   says every read value is accepted. Manifest: AC-10 adds the web's exact
   body `{"llmProvider":"unsupported","llmModel":""}` and the key-carrying
   variant; new AC-10b pins the 400 for a non-Ollama row; `llmTestBody` is
   listed as a consumer that does not break. §4 records the rejected
   alternative (a client rule to omit the property).

2. **Any key write deleted the legacy unbound key.** Applied. An `llmApiKey`
   write touches only the key of the provider after the write. The unbound
   key is removed only by a new write-only `SettingsUpdate.llmUnboundApiKey`
   whose schema admits only `""` (`enum: ['']`); any other value is the 400
   `llmUnboundApiKey must be "" (it can only remove the key bound to no
   provider)` and nothing is stored. `patchSettings`, `Settings.llmUnboundApiKeySet`,
   `SettingsUpdate` and `SettingsUpdate.llmApiKey` are rewritten to match, and
   the message is in `SettingsValidationError`. Manifest AC-16 is restored to
   the spec's wording ("The key is still in storage."), and new AC-16b is the
   challenger's payload as a unit case (bedrock at upgrade with a key;
   `{"llmProvider":"openai","llmApiKey":"sk-new"}` leaves
   `llmUnboundApiKeySet: true`), plus `llmApiKey: ""`, the explicit removal,
   a refused non-empty value and the no-op. §5 moves "any key write removes
   it" to the rejected alternatives with the reason. The OWNER question
   (Non-blocking 6) is carried in the manifest's `ownerAssumptionsCarried`;
   the contract follows spec D-5 ("never deleted") until the owner answers.

3. **`aiStatus.notConfigured` had no presence rule.** Applied.
   `AIStatus.notConfigured` carries the description "Present exactly when
   `configured` is false; absent exactly when it is true" beside its `$ref`,
   and the `AIStatus` description says exactly one of `provider` and
   `notConfigured` is present. The bundle loads with the description beside
   the `$ref` and oapi-codegen accepts it (gen unchanged for that field).
   Manifest: new AC-8b, `[contract]` for the bundle text and `[unit]` for one
   account in each of rows 1, 3, 6 and 8 (present, with that row's reason and
   enablers, no `provider`) and a row-2 account (absent).

4. **409-before-422 broke unlisted consumers.** Applied as a manifest fix; the
   order is unchanged (§6 gives the reason). `consumersAffected` now lists
   `e2e/tests/llm-destination.spec.ts:36-56`, the keyless cases of
   `backend/api/pac66_llm_destination_test.go` (:158, :176, :428 by name, and
   :169, :259, :283, :348, :362, :372, :415) and
   `pac66b_base_url_test.go` `TestPAC66B_ProbeChecksTheProviderItUses` (:208),
   each as "replaced, not skipped: a key is bound to the provider so the 422
   stays pinned". New acceptance test C-1: for `azure_openai` (unapproved and
   empty endpoint), `bedrock` (region host not approved, nothing approved) and
   `vertex` (global host not approved), with a key bound, the probe gives the
   422 with the PAC-66 `reason` / `property`, parse gives the refusal
   sentence with no `not_configured`, brief refresh gives 200 `failed`, zero
   requests; the same account without a key gives the 409 `needs_key`. §6
   explains why rewriting them to the 409 alone would unpin the check.

### Non-blocking

1. **Challenge section not on the branch.** Appended above, as drafted by the
   contract-challenger, with its gate placeholder filled from the gate log
   (the challenger returned before the run ended). No Linear comment: no PAC
   issue exists for this feature (manifest `linearIssueNote`).
2. **The challenger's api-gate run on `b356574`.** Its final results are in the
   Challenge's "Verified clean" list. One step failed: `mcp test rc=1`, with
   no failing test captured by the gate's filter. Rerun by contract-author
   (`go test -count=1 ./...` in `mcp/`, same host, same branch) passed; the
   cause of the earlier rc=1 is not identified. The revision 2 gate run is
   below.
3. **`refreshMeetingBrief` and settings-load failure.** Applied: the 500 now
   says "or the caller's settings could not be loaded ...; nothing was
   written" (no `pending` or `failed` brief, stored brief unchanged, nothing
   fetched or sent), citing today's `handlers_meeting_briefs.go:75-90`.
   Manifest C-2 pins it. This follows from the operation's existing statement
   that the configuration is decided "before anything else"; it is not new
   behaviour beyond the spec's AC-14.
4. **AC-33 and three provider sets.** Applied: manifest AC-33 now names the
   set per web use (settings document and hand-written type =
   `Settings.llmProvider`; probe body = `LLMTestRequest.llmProvider`, now the
   same eight; rendered provider names and mock probe = the five-value
   provider enum; the Settings list offers `""` and the five, never
   `unsupported`).
5. **The 409 sentence.** Applied: `AINotConfigured.message` and the probe's
   409 description say the sentence describes the configuration decided on;
   "AI features are off" only when that is the caller's saved choice. A probe
   of another provider or with `llmApiKey` describes only the probed choice.
   The probe's `needsKey` example is now the AC-37 case ("Anthropic needs an
   API key. Enter one to test it."), and a `savedNeedsKey` example shows the
   saved-choice wording. Manifest C-3 pins both.
6. **§7 versus spec D-19 for an `organization` probe with a model or key.**
   §7 is corrected: in Part 1 these are sent nowhere because the answer is
   always the 409, and the earlier word "ignored" is withdrawn as
   contradicting D-19. §8 now obliges the Part 2 revision to define that case
   by D-19.
7. **Model IDs as path segments.** Applied: the probe description states that
   `bedrockModel` and `vertexModel` are each sent as one path segment encoded
   with Go `url.PathEscape` (RFC 3986 §3.3): `:` literal, `/` as `%2F`. AC-4
   now pins the exact path
   `/model/anthropic.claude-sonnet-4-5-20251001-v1:0/converse`.
8. **Spec revision 1 re-check not recorded.** Still true; this stage cannot
   perform it (author is not challenger). Recorded in the manifest's
   `contractApproval`; the spec-challenger re-check remains a precondition of
   approval.
9. **OWNER question on the unbound key.** Not a blocking question: the
   contract takes the conservative option (never deleted implicitly; the
   factory rule forbids choosing deletion without an owner answer) and both
   answers are implementable by changing only whether `llmApiKey` also clears
   the unbound key. Carried in `ownerAssumptionsCarried`.

### Side effect of regeneration

`backend/api/gen/paceday.gen.go` gains `LLMTestRequestLlmProviderUnsupported`
and the `SettingsUpdateLlmUnboundApiKey` type and field. Because the new
`SettingsUpdateLlmUnboundApiKeyEmpty` constant makes `Empty` ambiguous,
oapi-codegen renamed the `WorkingHoursScheduleMode` constants from `AllDays`,
`ByDay`, `Empty` to `WorkingHoursScheduleModeAllDays`, `...ByDay`,
`...Empty`. No Go code names the old constants (`grep` of the repo); `go build
./...` and `go vet ./api/` in `backend/` succeeded.

### What was run for revision 2

- Local, before committing: `python scripts/openapi_assemble.py` and
  `--check`: `OK: bundle is in sync (99 paths)`, 120 operations, 150
  schemas, 8 `x-uncertain` (unchanged); `bash scripts/openapi_gen_go.sh`
  regenerated `backend/api/gen/paceday.gen.go`; `go build ./...` and
  `go vet ./api/` in `backend/`: success; `python
  scripts/openapi_migration_report.py --check`: `OK: MIGRATION.md covers all
  120 operations (4 generated)`; `sha256sum contracts/openapi/openapi.yaml`
  = `c014ab82c2823e38b108ee0aab068437763f42dd1dc345f5fab7c08207d5610a`
  (manifest `contractHash`).
- `bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh
  factory/new-llm-bring-your-own-contract` on `b6fd578` (the revision 2
  contract, bundle and gen code; the commit recording this touches only this
  file and the manifest): `make openapi rc=0`, `regenerated: none`,
  `openapi-check(api) rc=0` (bundle in sync, 99 paths; MIGRATION.md 120
  operations; backend/api/gen matches the contract), `backend lint rc=0`,
  `mcp lint rc=0`, `backend test rc=0`, `mcp test rc=0`, backend coverage
  63.2% (floor 57%) `rc=0`, mcp coverage 84.0% (floor 75%) `rc=0`. Web
  generated types were not checked by this gate (`WEB=/nonexistent`).
- Not run: the web gate (REGEN=1) and the e2e gate. As for revision 1, they
  belong to the build stage, which regenerates the web types with the
  implementation (manifest `gates.webGate`, `gates.e2eGate`). The behaviour
  this contract describes is not implemented yet; the existing tests pass
  because none validates a live response against these schemas.

---

## Challenge, revision 2 — 2026-10-28

Reviewer: contract-challenger, doing the confirming re-check that the manifest's
`gates.contractApproval` requires. Branch `impl/new-llm-bring-your-own-backend`
at `7a4e589`, in a detached worktree. That commit holds the revision 2 contract
(`b6fd578` + `2193af9`), the Part 1 implementation, and `origin/main` merged in.
Read: this file, the manifest, the spec (including `a4a91a5`, the owner answers
of 2026-10-28), the fragments `scheduling.yaml` / `habits.yaml`, the handlers
and decision code named below, `mcp/`, `e2e/`, and `Enach/smart-calendar-flow`
at `a5beb27` (PR #131). The web repo was read with `git show`; nothing was
checked out there.

**Verdict**: accept. No blocking finding. Each of revision 1's four blocking
findings is applied in the fragments, the manifest and the code, and none of
the attacks below broke revision 2. The non-blocking items are bookkeeping or
things Part 2 must do. None of them changes a Part 1 request or response.

### Revision 1 findings: applied?

1. **The probe rejected the value the settings read returns.** Applied.
   `LLMTestRequest.llmProvider` now has the same eight values as
   `Settings.llmProvider` (`scheduling.yaml:1844`). `unsupported` is accepted
   only when the saved choice reads as `unsupported` (`backend/nlp/llm_factory.go`
   `DecideProbe`). Otherwise the answer is the 400
   `llmProvider unsupported can be probed only when it is your saved choice`,
   the contract's wording. On an Ollama row, the web's exact body
   `{"llmProvider":"unsupported","llmModel":""}` (`src/api/client.ts:864-868`
   at `a5beb27`) gets the 409 `provider_removed` with the saved-choice wording.
   That is the same reason `{}`, parse and `aiStatus` give. Pinned by
   `TestBYOK_OllamaUserProbeVariants` (AC-10, AC-10b).
2. **Any key write deleted the unbound key.** Applied.
   `SettingsUpdate.llmUnboundApiKey` is `enum: ['']` and write-only. The handler
   removes the unbound key only when that property is sent
   (`handlers_settings.go:161-193`). `llmApiKey` only calls
   `LLMAPIKeys.Set(provider-after-write, …)`. Test case: a Bedrock row with a
   key at upgrade, then `PATCH {"llmProvider":"openai","llmApiKey":"sk-new"}`.
   The unbound column is untouched, because the save writes
   `s.LLMUnboundAPIKey` back unchanged (`storage/settings.go:422`), so
   `llmUnboundApiKeySet` stays `true`. `{"llmUnboundApiKey":"x"}` is the 400
   and nothing is stored, because the check runs before the save. Manifest
   AC-16 is back to the spec's wording, and AC-16b is
   `TestBYOK_UnboundKeyIsRemovedOnlyExplicitly`. The web makes the removal an
   explicit "Remove old key" button (`Settings.tsx` at `a5beb27`) and sends
   `llmUnboundApiKey: ""` only after that click.
3. **`aiStatus.notConfigured` had no presence rule.** Applied, in both the
   property description and the `AIStatus` description
   (`scheduling.yaml:2034-2063`). The code cannot break the rule. `nlp.Status`
   sets exactly one of `Provider` and `NotConfigured` from the same `Decision`
   (`llm_factory.go:51-57`), and both fields are `omitempty`. The web shows the
   notice only when `!configured && notConfigured` (`Settings.tsx:597`).
4. **409-before-422 broke unlisted consumers.** Applied.
   `e2e/tests/llm-destination.spec.ts` now asserts the keyless 409
   `needs_key azure_openai`. It then binds a key and keeps the 422
   `destination_not_approved azureEndpoint`. `pac66b_base_url_test.go:211-221`
   keeps the 422 with a submitted key. C-1 is
   `TestBYOK_KeyedCloudProviderStillRefusesAnUnapprovedDestination`. The PAC-66
   destination check is still pinned for every provider with a key.

Revision 1's non-blocking items 3, 5 and 7 are in the fragments and match the
code:

- **Item 3, brief refresh 500 writes nothing.** `refreshBrief` returns the 500
  when the settings or organisation lookup fails, before `MeetingBriefService`
  is created (`handlers_meeting_briefs.go:78-91`).
- **Item 5, the probe sentence describes the probed choice.**
  `notConfiguredMessage` uses `AI features are off: …` only when `saved` is
  true. The examples `needsKey`, `savedNeedsKey` and `providerRemoved` are the
  exact strings the code produces.
- **Item 7, model IDs as path segments.** The `url.PathEscape` rule is stated
  on the probe.

### Requests wrongly permitted or forbidden

None blocking. Attacked:
- `POST /api/llm/test {"llmProvider":"unsupported"}` on an `openai` row: the
  400. Contract and code agree. On an Ollama row with `"llmApiKey":"sk"`: the
  409 `provider_removed` with the probed-choice wording, and the key goes
  nowhere, because `decide`'s default branch builds no client.
- `{"llmProvider":"anthropic"}` while the saved choice is `openai` with a stored
  model: `DecideProbe` uses the stored model only when the probed choice equals
  the saved one (`llm_factory.go:93-97`). So the server never sends a stored
  OpenAI model to Anthropic.
- `{"llmApiKey":""}` on a configured `openai` row: the 409 `needs_key openai`,
  "OpenAI needs an API key. Enter one to test it." It does not say "AI features
  are off", as revision 2 requires.
- `PATCH {"llmProvider":"unsupported"}` after an Ollama row has moved to
  `openai`: the 400, because the row no longer reads as `unsupported`. An
  Ollama row cannot be re-entered.
- `PATCH {"llmApiKey":"sk"}` on an Ollama, `organization` or `""` row: the 400
  `llmApiKey cannot be set: …`. `{"llmApiKey":""}` on such a row is an accepted
  no-op. Both as the contract states.
- Probe body `{"llmProvider":null}` or `{"llmApiKey":null}`: the schema makes
  these a 400, because no property is nullable. The handler decodes into
  `*string` and treats null as absent (`handlers_llm.go:25-29`). This has been
  so since PAC-24 (the same struct is on `origin/main`) and is harmless: a null
  key means the stored key, the same as an absent one. See non-blocking 4.

### Responses the client cannot handle

None blocking.
- **Generated types are in sync.** I regenerated the web's three generated
  files from this branch's bundle with the pinned `openapi-typescript` 7.4.4
  and `typed-openapi` 4.1.0 from the web checkout's `node_modules`, into a
  scratch directory. `types.ts`, `schemas.ts` and `schemas.types.d.ts` at
  `a5beb27` are byte-identical to the output.
- **The 409 sentence reaches the user.** `apiErrorMessage` shows the body's
  `error` before its generic 409 text (`client.ts:388-393`), so "Test
  connection" shows the server's sentence.
- **Parse "not configured" is not shown as an error.** `not_configured` is
  rendered by `AINotConfiguredNotice` in place of the red error (`NLPBar.tsx`,
  `Dashboard.tsx`).
- **The select can display `organization`.** A new account outside an
  organisation reads `organization`. `providerOptions` adds it as a disabled
  "No provider chosen" option (AC-21).

### Consumers affected

- `mcp/`: unchanged. `clockwise_parse_command` still gets the parse 200.
  `mcp/nlp_not_configured_test.go` pins that `not_configured` reaches the tool
  as a body (ran `go test -run NotConfigured ./...` in `mcp/`: ok). No MCP tool
  reads settings, runs the probe or refreshes briefs.
- `e2e/`: `llm-destination.spec.ts`, `llm-base-url.spec.ts`,
  `llm-bring-your-own.spec.ts` and `settings.spec.ts` expect the contract's
  answers (read, not run).
- Backend: every LLM call site goes through `DecideSaved` or `DecideProbe`.
  Grepping for `Complete(` and `NewLLMClient` finds only parse, brief refresh
  and the probe. Only `refreshBrief` creates a `MeetingBriefService`.

### Contract/implementation divergences

None that a client can observe, apart from non-blocking 4 and 5.

### Uncertainties not actually resolved

None claimed. `resolvesUncertain` is empty. The bundle has 8 `x-uncertain`
markers, none on an operation this contract edits.

### Non-blocking

1. **Owner answer O-12 makes two forward-looking statements false.** O-12
   (OQ-7, 2026-10-28) is settled and is recorded on this branch at `a4a91a5`,
   after revision 2. The two statements:
   - `AINotConfigured.enablers` says `user` "is always present".
   - §7 says listing the Part 2 reasons now "makes Part 2 additive".

   Under O-12, when an organisation forbids personal keys and its own AI setup
   is incomplete, the member cannot turn AI on themselves. Part 2 then needs
   either `enablers: [organization_admin]` without `user`, or a reason value
   that today's enum lacks. Either one changes a guarantee this revision
   publishes. Both statements are true of Part 1, which cannot produce that
   state, so this does not block Part 1. The Part 2 revision must handle it,
   and the cheapest time is now:
   - replace "is always present" with "is present in every answer before
     Part 2";
   - add to §8 that O-12 adds a reason or an enablers case.

   The web's `whoCanEnable` (`src/lib/aiProviders.ts`) would then also have to
   stop always saying "You can turn them on in Settings".
2. **Stale header.** This file says "Linear issue: none yet", and the
   manifest's `linearIssueUrl` is empty, but the ticket is PAC-73. This
   re-check did not post a Linear comment either.
3. **Stale `contractHash`.** The manifest's `c014ab82…610a` is the hash of the
   bundle at `2193af9` (reproduced). Since `origin/main` (PAC-22) was merged,
   the bundle hash is
   `b14aa7e8dfca3ad08fa5b0f5503dc3bd929d9ba5d9430c9dca2f839bf76b7ff7`, with 100
   paths and 121 operations. The merge changed no LLM, NLP, brief or settings
   schema (checked by grepping
   `git diff 2193af9 HEAD -- contracts/openapi/openapi.yaml`). Update the hash
   and the counts.
4. **null in the probe body** (see above). Either say on `LLMTestRequest` that
   `null` is treated as absent, or make the handler return the 400 the schema
   implies. The behaviour predates this change.
5. **500 wording.** The 500 of the probe and of brief refresh says "settings
   load failure". The handlers also return it when the organisation-membership
   lookup (`storage.UserInOrganization`) fails. Add "or the caller's
   organisation membership could not be read".
6. **Web (#131), not the contract.** The contract permits each of these:
   - "Remove saved key" sets the draft's `llm_api_key` to `""`, but
     `llmTestBody` leaves out an empty key. So "Test connection" before saving
     probes with the stored key, and can report "Reached OpenAI" right after
     the user removed it. Fix: send `llmApiKey: ""` when the draft holds `""`.
   - Switching the draft provider keeps `llm_model`. An unsaved switch from
     Anthropic to OpenAI therefore probes, and then saves, a `claude-…` model
     on OpenAI, which gives a 502.
   - The notice says "AI features are off" twice: once in its heading and again
     in the server's sentence.
7. **Spec re-check.** The spec-challenger re-check of spec revision 1 is still
   not recorded (spec status line). This re-check does not replace it.

### Verified clean

- Revision 1's four blocking findings, checked against the fragments, the code
  and the web (see above).
- §9 points 1-6, attacked again on the implementation:
  - One state gives one reason on parse, brief refresh, the `{}` probe and
    `aiStatus`. All four call `decide` with the saved choice.
  - `unsupported` cannot be stored on a non-Ollama row. The save writes
    `StoredLLMProvider` only while the row reads as `unsupported`
    (`settings_ai.go:40-45`).
  - No key reaches a provider it was not bound to or submitted for. The code
    looks keys up only with `LLMAPIKeys.For(choice)`.
  - The unbound key is used on no path (`For` never reads it), and every probe
    response redacts it.
- Owner decisions:
  - no `ollama` and no ambient identity in any enum or provider mode;
  - Azure, Bedrock and Vertex only with API keys;
  - gateway path prefixes allowed (O-10);
  - no enabled-providers endpoint;
  - keys write-only.
- Checks this re-check ran, in the worktree at `7a4e589`:
  - `py -3 scripts/openapi_assemble.py --check`: `OK: bundle is in sync (100 paths)`, rc=0.
  - `py -3 scripts/openapi_migration_report.py --check`: `OK: MIGRATION.md covers all 121 operations (5 generated)`, rc=0.
  - `bash scripts/openapi_gen_go.sh --check`: `OK: backend/api/gen matches the contract`.
  - `go build ./...` in `backend/`: ok.
  - Web generated files at `a5beb27`, regenerated from this bundle: identical (3 files).
  - `go test -count=1 -run NotConfigured ./...` in `mcp/`: ok.
- Not run:
  - The backend `api`, `nlp` and `storage` tests, including every `TestBYOK_*`.
    They need the test Postgres on 127.0.0.1:15432, which was not running, and
    this review was told not to start containers on that port.
  - `make verify`, the web gate and the e2e gate.
