# Spec — NEW-llm-bring-your-own: AI features run only on a provider account the organisation or the user brings

- **Linear**: none yet. Searches of the PAC team for "Ollama" and "LLM"
  (2026-10-28) found PAC-24 (Done), PAC-66 (In Progress, part A: approved hosts
  for the ambient-identity providers), PAC-70 (Done, PAC-66-B: approved user
  base URLs), PAC-31 (Done, enum alignment), PAC-56 (Backlog, "Settings shows
  operator configuration to every user") and PAC-22/PAC-23/PAC-27, none of which
  is this change. The factory ticket id is `NEW-llm-bring-your-own`; a PAC issue
  should be opened at intake (parent, with the two PR-pair sub-issues of §0)
  and this file linked from it. Because there is no issue, nothing was posted
  to Linear.
- **Status**: draft (awaiting `spec-challenger`)
- **Author**: spec-author
- **Inherited scope**:
  - No `x-uncertain` marker sits on the operations whose behaviour this spec
    changes: settings read and write, the provider probe, natural-language
    parse and meeting-brief refresh. The markers in `contracts/openapi/paths/`
    are in `auth.yaml` (1261, 1322), `calendar.yaml` (1309, 1771), `teams.yaml`
    (610, 1640, 1647) and `habits.yaml:475`. The last one is on the meeting-brief
    **read** (`/api/meetings/{event_id}/brief`, `habits.yaml:449`), which this
    spec does not change. If the contract stage finds it must touch that
    operation, README §7 rule 1 makes the marker its scope.
  - **API-039** (`docs/factory/api-audit.md:297`, provider enum mismatch on the
    settings document) is open and **claimed by smart-calendar-flow#101**. This
    spec changes that enum (it removes `ollama` and redefines the meaning of the
    three cloud providers). The contract stage must check whether the claim is
    still live. If it is not, this change resolves the `LLMProvider` half of
    API-039, because the web enum is regenerated from the contract here.
  - **PAC-66 part A** (approved hosts for Azure OpenAI, Vertex and Bedrock,
    landed: `backend/nlp/destination.go`, `backend/config/llm_hosts.go:16-18`)
    and **PAC-70 / PAC-66-B** (approved user base URLs, landed:
    `backend/nlp/user_destination.go`, API-093 marked Fixed at
    `docs/factory/api-audit.md:351`). This spec removes the ambient-credential
    half of what part A protects and the Ollama half of what part B protects.
    The approved-destination rules themselves stay (§6).
  - **PAC-70 manifest follow-ups** `gatewayPathPrefix` (OQ-B6, second half) and
    the enabled-providers advertisement (OQ-B2): both answered by the owner on
    2026-10-27 and recorded in Decisions (D-1, D-9). This spec closes both
    follow-ups.
  - **PAC-50** (SSO write gate, step-up proof of organisation-admin status) is
    not landed on `main` (spec `docs/specs/PAC-50.md`, Status v2.2; its
    implementation is on branch `impl/pac-50-sso-write-gate-backend`). Part 2 of
    this spec depends on it (D-6, §0).

## 0. Scope and split

The acceptance list is too large for one reviewable PR pair. It splits into two
PR pairs (api + web each) under this one spec. Part 1 can land alone; Part 2
needs Part 1 and PAC-50.

- **Part 1 — removal and key-only providers.** Ollama goes. The three cloud
  providers stop using any identity of the deployment and are kept only in
  their API-key mode (D-2, D-3, D-4). Keys are bound to the provider they were
  entered for. Every AI-backed operation answers one documented "AI not
  configured" outcome with a reason, and the web app says AI is off and who can
  turn it on. Stored accounts on a removed mode report "not configured" with
  the reason. An operator-approved OpenAI or Anthropic destination may carry a
  path prefix. ACs 1–21, 32–34.
- **Part 2 — organisation-wide provider and key.** An organisation admin (D-6)
  sets one provider and key for the organisation. A member's own key takes
  precedence for that member only. Members see that the organisation provides
  AI, never the key. ACs 22–31.

If the plan stage finds Part 1 still too large, it may split it further (for
example "remove Ollama and ambient modes" then "add the three key modes"), but
it may not land any slice that leaves a user's text going somewhere this spec
does not allow.

## 1. Problem

Paceday's AI features (the natural-language bar, meeting-prep briefs and the
"Test connection" probe in Settings) can today run on three kinds of account:

1. **The deployment's own cloud identity.** On AWS Bedrock, Azure OpenAI and
   Google Vertex AI the server authenticates with whatever credentials the
   machine it runs on carries: an instance role, a managed identity, a service
   account. Whoever runs Paceday pays for every user's AI use, and every
   signed-in user can spend it. The product owner has decided Paceday does not
   run or pay for LLM execution (Decisions, owner 2026-10-27).
2. **A local Ollama server.** Every account created since migration 002 starts
   on Ollama at `http://localhost:11434`. Inside the server's container that
   address is the server itself, and since PAC-66-B Ollama is off unless the
   operator approves an origin. So for almost every user today the AI features
   fail with "ollama is not enabled on this deployment", a message about server
   configuration the user can do nothing about. The owner has decided Ollama is
   removed from the product.
3. **A key the user brought** (OpenAI or Anthropic). This is the only mode that
   matches the owner's decision, and it works only for users who know to go to
   Settings and paste a key.

What it costs: an operator who deploys Paceday on a cloud VM silently pays for
everyone's AI. A user who never configured anything sees AI features fail with
errors that blame the server. An organisation that wants its staff to have AI
has no way to provide it except by every member pasting the same key, which
spreads the key across as many accounts as there are members.

## 2. Current behaviour

### 2.1 Which account a request runs on

- One function picks the client from the caller's own settings row:
  `backend/nlp/llm_factory.go:14-69`. Branches: `bedrock` (:16-17),
  `azure_openai` (:18-19), `vertex` (:20-21), `openai` (:22-35, needs a key),
  `anthropic` (:36-47, needs a key), `ollama` (:48-65, no key), anything else
  (:66-67) returns "LLM not configured — set API key in Settings".
- **Ambient credentials.** Azure mints an Entra ID token with
  `azidentity.NewDefaultAzureCredential` (`backend/nlp/llm_azure_openai.go:39`)
  and sends it as `Authorization: Bearer` (`:69-70`). Bedrock loads the AWS
  default chain with `config.LoadDefaultConfig`, optionally with a named shared
  profile from the user's settings (`backend/nlp/llm_bedrock.go:39-55`, profile
  at :44-46). Vertex loads Application Default Credentials with
  `google.FindDefaultCredentials` (`backend/nlp/llm_vertex.go:44`) and calls the
  regional `<location>-aiplatform.googleapis.com` host with the user's project
  (:31-35). None of these takes a credential from the user.
- **Ollama.** `OllamaClient` (`backend/nlp/parser.go:98-120`) posts to the
  stored `ollamaBaseUrl`, then `llmBaseUrl`, then `http://localhost:11434`
  (`llm_factory.go:51-57`), with no credential. It is refused unless the
  operator lists an origin in `PACEDAY_LLM_APPROVED_ORIGINS_OLLAMA`
  (`backend/nlp/user_destination.go:101-131`,
  `backend/config/llm_user_destinations.go:20`).
- **User keys.** One key slot per user (`backend/storage/settings.go:173-177`,
  write-only since PAC-24, with a presence flag). The key is not tied to a
  provider: switching from OpenAI to Anthropic keeps the stored key and sends
  it to the new provider.
- **Defaults.** Migration 002 added the Ollama, Bedrock, Azure and Vertex
  columns, moved every existing `''` provider to `ollama`, and made `ollama`
  the column default (`backend/storage/migrations/002_llm_providers.up.sql:1-14`).
  A settings row is created on first read with all defaults
  (`backend/storage/settings.go:246`), so every account that never chose a
  provider is on Ollama. `''` is accepted by the write validation
  (`backend/api/handlers_settings.go:22-30`) and the web app labels it
  "None (AI features off)" (`smart-calendar-flow/src/pages/Settings.tsx:59`),
  so a stored `''` today is an explicit user choice.

### 2.2 What each AI-backed operation does when no account is usable

- **Natural-language parse** (`POST /api/nlp/parse`): 200 with
  `intent: "unknown"` and `error` set to the factory's message
  (`backend/nlp/parser.go:191-194`), or to a destination refusal's message
  (:198-201). The web bar shows `error` verbatim
  (`smart-calendar-flow/src/pages/Dashboard.tsx:184-185`). The MCP tool
  `clockwise_parse_command` passes the body through (`mcp/tools.go:195-203`).
  There is no machine-readable way to tell "not configured" from "could not
  understand you".
- **Meeting-brief refresh** (`POST /api/meetings/{event_id}/brief/refresh`):
  200 with a brief whose status is `failed` and whose text is
  "LLM not configured" or a refusal message
  (`backend/api/handlers_meeting_briefs.go:74-91`,
  `backend/engine/meeting_brief_service.go:76-83`). The failed brief is
  stored, replacing any earlier one.
- **Provider probe** (`POST /api/llm/test`): a factory error that is not a
  destination refusal is answered **500** with a generic body
  (`backend/api/handlers_llm.go:83-86`). A user who simply has no key gets a
  server error.
- No operation tells the client which providers exist or whether AI is
  available before the user tries.

### 2.3 Organisations and admins

- An organisation is created from the email domain on first sign-in and every
  user with that domain joins it (`backend/storage/orgs.go:42-52`; schema
  `backend/storage/migrations/007_organizations.up.sql`). Generic domains
  (gmail.com, outlook.com…) get no organisation. Membership is self-service.
- There is no admin role: no role column on users, no owner on organisations.
  `team_members.role` (`013_teams.up.sql:12`) is `owner` for whoever created
  the team, and any member of an organisation can create a team. PAC-50's spec
  records the same finding (`docs/specs/PAC-50.md:24`).
- The SSO admin operations today require only organisation membership
  (`backend/api/handlers_sso.go:166-171`). PAC-50 replaces that with a
  short-lived, single-use proof that the caller is an administrator of the
  organisation's Google Workspace or Microsoft Entra tenant, plus an
  operator-maintained break-glass list (`docs/specs/PAC-50.md:41-47`, §4).

### 2.4 Configuration and web surface

- Compose passes six approval variables, including
  `PACEDAY_LLM_APPROVED_ORIGINS_OLLAMA` (`docker-compose.yml:50-63`,
  `docker-compose.prod.yml:93-99`). `.env.example:49-94` documents the ambient
  identities and the Ollama origins.
- The web Settings page lists seven choices, including "Ollama (local)",
  "AWS Bedrock", "Azure OpenAI", "Google Vertex AI"
  (`smart-calendar-flow/src/pages/Settings.tsx:58-66`). It shows an
  "AWS Profile" field whose hint is "leave empty to use the default credential
  chain", Azure endpoint and deployment fields, GCP project and location
  fields, and an Ollama base URL field. The key field is shown only for OpenAI
  and Anthropic.

### 2.5 Existing tests that pin today's behaviour

26 backend test functions are named for Ollama, Bedrock, Vertex or Azure (for
example `TestBuildLLMClient_Ollama`, `TestOllamaClient_Complete_Mock`,
`TestNLPService_Parse_WithOllama` in `backend/nlp/parser_test.go`), plus Ollama
cases in `backend/api/pac24_scope_test.go`, `pac66b_base_url_test.go`,
`pac66_llm_destination_test.go`, `handlers_settings_test.go`,
`backend/config/llm_user_destinations_test.go` and
`backend/nlp/bedrock_credentials_test.go`. The key round trip is pinned in e2e
by `e2e/tests/settings.spec.ts:40-60`.

## 3. Desired behaviour

**Nobody but the organisation or the user pays.** Paceday makes an AI request
only with a credential the organisation or the user entered. It never uses an
identity, credential file, environment credential or metadata service of the
machine it runs on. An operator has no setting that supplies a key.

**Which account is used.** For each user, at the moment of each AI request:

1. If the user has turned AI off for themselves, nothing is sent.
2. Otherwise, if the user has a complete configuration of their own (a
   supported provider and a key entered for that provider), it is used.
3. Otherwise, if the user is following their organisation and the organisation
   has a complete configuration, the organisation's is used.
4. Otherwise AI is not configured for that user, and every AI-backed operation
   says so (below).

A user's own key applies to that user only. It never applies to another
member, and the organisation's key never becomes visible to a member.

**Supported providers.** OpenAI, Anthropic, Azure OpenAI with an API key, AWS
Bedrock with a Bedrock API key, and Google Vertex AI with an express-mode API
key (D-2, D-3, D-4). Ollama is gone. A key is bound to the provider it was
entered for: changing provider does not carry the key to the new provider. The
user, or the admin, enters a key for the new provider.

**"AI not configured" is an answer, not an error.** Every AI-backed operation
answers the same documented outcome when rule 4 applies: not a server error
and not a provider error. It carries a reason from a fixed set and says who can
fix it. The reasons are at least:

- AI is off by the user's own choice;
- nothing is configured (no own key, and no organisation configuration, or the
  user has no organisation);
- the user's previous provider is no longer supported (naming it: Ollama, or
  Bedrock / Azure OpenAI / Vertex in their old identity-based mode);
- the user's provider needs a key and none is on file for it;
- the user's organisation provides AI but the user has not chosen to follow it
  (only for accounts that existed before this change; see "Existing accounts").

"Who can fix it" is the user (in Settings) and, when the user belongs to an
organisation, the organisation's admin.

**The web app** shows only the supported providers. When AI is not configured
for the user, the natural-language bar and Settings say that AI features are
off, why, and who can turn them on, instead of an error. Settings has a section
for the organisation's AI configuration that an organisation admin can edit and
that a member can only read (provider and model, and whether a key is set; never
the key).

**Existing accounts.** No account is moved onto a provider it did not choose.

- An account whose stored choice is "off" stays off.
- An account on OpenAI or Anthropic with a key keeps working as today; that key
  is bound to that provider.
- An account on OpenAI or Anthropic without a key reports "needs a key".
- An account on Ollama (most accounts, by default) or on Bedrock, Azure OpenAI
  or Vertex reports "not configured" with the removed-mode reason. It is **not**
  enrolled in the organisation's configuration automatically, because that
  would send the user's text and meeting context to a destination they never
  chose (D-8). Settings offers "use my organisation's AI" and "use my own key"
  in one step.
- The Bedrock region and model, Azure endpoint, deployment and API version and
  Vertex model a user entered are kept and reused if they add a key for that
  provider. The settings that only meant something in the removed modes (Ollama
  base URL and model, AWS profile, GCP project and location) are no longer read
  or returned. They are left in storage untouched until a later release, so
  rolling back restores them (§7, D-10).
- A key stored on an account whose provider at upgrade is not OpenAI or
  Anthropic is not bound to any provider and is never sent. Settings reports
  that a key is on file that does not match the selected provider and offers
  to replace or remove it. It is not deleted.
- **New accounts** follow their organisation by default (owner decision: org
  members use the org's key). A new account with no organisation, or whose
  organisation has no configuration, reports "nothing configured".

**Gateways.** An operator may approve an OpenAI or Anthropic destination that
includes a path prefix, for an OpenAI-compatible gateway (owner decision OQ-B6).
Requests to it go to that prefix followed by the provider's one chat operation
and nowhere else. Everything else about approved destinations is unchanged
(§6).

**Organisation admin.** Only a person proven to be an administrator of the
organisation can set, replace or remove the organisation's provider and key
(D-6). Being a member, or owning a team, is not enough.

## 4. Acceptance criteria

### Part 1 — removal and key-only providers

AC-1. Given the backend source tree, when a test scans the AI-provider code for
any use of the Azure default credential chain, the AWS default configuration
loader, or Google Application Default Credentials, then it finds none. The test
fails today (three uses). [unit]

AC-2. Given a deployment whose host has working AWS, Azure and Google ambient
credentials (simulated by the test seams that exist today), and a user on
Bedrock, Azure OpenAI or Vertex with no key, when the user parses a sentence,
refreshes a brief or runs the probe, then no request reaches any provider, no
token is minted, and each answers "AI not configured" with reason "needs a key"
for that provider. Today a request is made with the ambient token. [unit]

AC-3. Given a user on Azure OpenAI with an approved endpoint, a deployment and a
key, when they parse a sentence, then exactly one request per completion goes
to that endpoint's chat operation, carrying the key in the `api-key` header
and no `Authorization` header. [unit]

AC-4. Given a user on Bedrock with a region whose runtime host is approved, a
model and a Bedrock API key, when they parse a sentence, then the request goes
to that region's Bedrock runtime host, carrying the key as a bearer token, and
no AWS signature or other AWS credential is sent or loaded. [unit]

AC-5. Given a user on Vertex with a model and an express-mode API key, when they
parse a sentence, then the request goes to the Vertex express-mode endpoint
with the key in the `x-goog-api-key` header (never in the URL), and no project
or location from the user's settings is used. [unit]

AC-6. Given the settings write, when a client sets the provider to `ollama`,
then the write is refused with a validation error naming the provider property,
and nothing is stored. Today it is accepted. [contract] [unit]

AC-7. Given the assembled contract, when it is read, then the provider value
set does not contain `ollama`, the settings document has no Ollama base-URL or
Ollama-model property, no AWS-profile, GCP-project or GCP-location property,
the destination-refusal property list does not name any of them, and the probe
description no longer describes ambient credentials or an Ollama fallback
order. A test reading the bundle fails today. [contract]

AC-8. Given a user whose stored provider is `ollama` (with or without stored
Ollama values), when they read their settings, then the response is valid
against the contract (no removed provider value, no removed property), reports
that the user has no usable provider of their own and that the previous
provider "Ollama" is no longer supported. The same holds for a user stored on
Bedrock, Azure OpenAI or Vertex without a key, with reason "needs a key" for
that provider. [contract] [unit]

AC-9. Given the user of AC-8, when they write back exactly the settings
document they read, then the write succeeds and their stored provider
choice, their stored provider-specific values and their stored key are
unchanged (in particular the user is not switched to "off" and not switched to
"follow organisation"). [unit] [e2e]

AC-10. Given the user of AC-8, when they parse a sentence, refresh a brief or
run the probe, then each answers the documented "AI not configured" outcome
with the removed-provider reason, and no request is made to any host. Today
parse and refresh answer "ollama is not enabled on this deployment" and the
probe answers 422 or 500. [contract] [unit]

AC-11. Given a user with AI turned off by their own choice, when they parse,
refresh a brief or run the probe, then each answers "AI not configured" with
reason "off by user", and nothing is sent. [unit]

AC-12. Given a user on OpenAI or Anthropic with no key, when they run the
probe, then the answer is the documented "AI not configured" outcome with
reason "needs a key", not a 5xx. Today it is 500 (`handlers_llm.go:83-86`).
[contract] [unit]

AC-13. Given any user for whom AI is not configured, when they parse a sentence,
then the response lets a client tell "AI not configured" apart from "the model
did not understand you" without reading message text, and the existing
response fields a current client reads (`intent`, `error`) still let the
current web bar and the MCP tool show a sentence rather than fail. [contract]
[unit]

AC-14. Given any user for whom AI is not configured and an existing *ready*
brief for an event, when they refresh that brief, then the answer is the
documented not-configured outcome and the existing ready brief is still
returned by the brief read afterwards (it is not overwritten by a failed
brief). Today it is overwritten. [unit]

AC-15. Given a user on OpenAI with a stored key, when they change their
provider to Anthropic without sending a key, then their settings report no key
on file for the selected provider, and a parse sends nothing to Anthropic and
answers "needs a key". When they change back to OpenAI, then the OpenAI key is
usable again. Today the OpenAI key is sent to Anthropic. [unit]

AC-16. Given an account that at upgrade is on Ollama, Bedrock, Azure OpenAI or
Vertex and has a stored key, when its settings are read, then the key-presence
information says a key is on file that is not bound to the selected provider,
and no AI request ever sends that key. The key is still in storage. [unit]

AC-17. Given an account stored as "off" before the upgrade, when the upgrade
runs, then it is still off, and it does not follow its organisation even after
Part 2 lands and the organisation is configured. [unit]

AC-18. Given an operator approval for OpenAI that includes a path prefix
(`https://gw.example/openai`) and a user whose base URL is that destination,
when the user parses a sentence, then the request goes to the prefix followed
by the chat operation, and to no other path; and a user base URL with the same
host but a different or missing prefix is refused at write and at send.
Today any entry with a path is malformed and ignored. [unit]

AC-19. Given the server starting with `PACEDAY_LLM_APPROVED_ORIGINS_OLLAMA` set,
when it starts, then it starts, logs once that the variable is no longer used,
and approves nothing from it. [unit]

AC-20. Given the compose files and `.env.example`, when a test reads them, then
none mentions Ollama or an ambient cloud identity, and `.env.example` states
that Paceday supplies no LLM credential. [unit]

AC-21. Given a signed-in user for whom AI is not configured, when they open the
dashboard and use the natural-language bar, then the page says that AI
features are off, why (the reason), and who can turn them on (the user in
Settings, and their organisation's admin when they have an organisation),
with a link to Settings, instead of an error. When they open Settings, then the
provider list offers only "Off", "Use my organisation's AI" (Part 2), OpenAI,
Anthropic, Azure OpenAI, AWS Bedrock and Google Vertex AI, and no Ollama, AWS
profile, GCP project or GCP location field is shown. [e2e]

### Part 2 — organisation-wide provider and key

AC-22. Given a member of organisation A who has not proven admin status, when
they set, replace or remove A's provider and key, then the request is refused
(not permitted, with a reason a client can act on, pointing to the admin proof)
and A's configuration is unchanged. A team owner who is not an admin is refused
the same way. [contract] [unit]

AC-23. Given a person who has proven, under PAC-50's rule, that they administer
organisation A, when they set A's provider, model and key, then the write
succeeds; and a second write without a fresh proof is refused as in AC-22
(one proof authorises one write, as in PAC-50). [unit]

AC-24. Given A has a stored key, when any member of A, including the admin who
set it, reads A's configuration or their own settings, then no response
contains the key; the admin sees whether a key is set, and a member sees the
provider, the model and that the organisation provides AI. [contract] [unit]
[e2e]

AC-25. Given a user in organisation B, when they read or write organisation A's
configuration by any means the contract offers, then they are refused and learn
nothing about A's configuration. [unit]

AC-26. Given A is configured, a member M1 who follows the organisation, and a
member M2 with their own complete configuration, when each parses a sentence,
then M1's request carries A's key to A's provider and M2's request carries
M2's key to M2's provider, and no request carries A's key for M2 or M2's key
for anyone else. [unit]

AC-27. Given M2 of AC-26 removes their own key, when M2 parses a sentence, then
the answer is "needs a key" for M2's provider (M2 chose their own provider) and
nothing is sent, until M2 switches to "use my organisation's AI". [unit]

AC-28. Given a new account created after the upgrade in organisation A, when A
is configured, then the account follows A by default and its AI features work
without the user opening Settings. Given A is not configured, then the account
answers "nothing configured" and names the organisation's admin as someone who
can enable AI. [unit] [e2e]

AC-29. Given an account that existed before the upgrade and was on a removed
mode (AC-8), when A becomes configured, then the account still answers "not
configured" with the reason "your organisation provides AI; choose it in
Settings", and nothing is sent until the user chooses. [unit]

AC-30. Given an organisation admin, when they remove A's key, then every member
following A answers "nothing configured" on their next AI request, and members
with their own keys are unaffected. [unit]

AC-31. Given the web Settings page, when an organisation admin opens it, then
there is an organisation AI section where they can set provider, model and key
(key field write-only, showing only "a key is set"), and when a member who is
not an admin opens it, the same section is read-only and shows the provider,
the model and who can change it. [e2e]

### Both parts

AC-32. Given the assembled contract, when it is read, then the documented
"AI not configured" outcome, with its reason set, is present on every
AI-backed operation (parse, brief refresh, probe), and (Part 2) the
organisation configuration operations document their refusal for non-admins.
[contract]

AC-33. Given the web repo, when the generated types are regenerated from the
contract of each part, then the web gate passes on the regenerated types in
the same PR pair as the contract change. [contract]

AC-34. Given the e2e suite against the compose stack with no key anywhere, when
a fresh user parses a sentence, then the API answers "AI not configured" with
reason "nothing configured" and not a 4xx/5xx error, and the dashboard shows
the "AI features are off" message of AC-21. [e2e]

## 5. Explicitly out of scope

- **Service-account JSON keys or other uploaded cloud credentials** (a Google
  service-account key file, AWS access-key pairs, an Azure client secret).
  These are credentials the user supplies, so they are not excluded by the
  owner's decision, but each is a long-lived cloud credential with a far
  broader scope than one model API and a new secret-handling surface. Not
  offered; can be a later spec if a customer needs it.
- **Paceday-paid or metered AI, quotas, spending limits.** The owner decided
  Paceday pays for none.
- **Per-team keys.** Only organisation-wide and per-user.
- **An admin role beyond what PAC-50 proves.** No role table, no "make someone
  admin" action. If PAC-50's proof is not acceptable to the owner for this use,
  that is an open question (OQ-1), not something this spec invents.
- **Usage reporting to the organisation** (who used the org key, how much).
  The provider's own console shows usage for its key.
- **Dropping the now-unused columns.** They stay in storage for one release so
  that rollback restores them (D-10); dropping them is a follow-up.
- **Changing the approved-destination rules for Azure and Bedrock hosts**
  (OQ-3). They stay as PAC-66 part A left them.
- **PAC-56** (operator configuration shown to every user). This spec removes
  the ambient fields, which reduces that problem, but does not resolve PAC-56.

## 6. What must not change

- **Keys are write-only.** No response carries a user key or an organisation
  key, in any status, including the probe and the not-configured outcome.
  Protected today by `TestLLMTest_BodyRulesAndNoKeyEcho`
  (`backend/api/pac24_scope_test.go`) and `e2e/tests/settings.spec.ts:40-60`;
  Part 2 extends them to the organisation key.
- **The probe cannot carry a destination.** The probe body still may not carry
  an endpoint, base URL or region (PAC-24 invariant). Protected by the same
  test.
- **Approved destinations.** A user or organisation base URL, Azure endpoint
  or Bedrock region is still used only toward a destination the operator
  approved for that provider, with no redirect followed and only the chat
  operation called (PAC-66 parts A and B; API-093). Their existing tests in
  `pac66_llm_destination_test.go` and `pac66b_base_url_test.go` keep passing for
  OpenAI, Anthropic, Azure and Bedrock; the Ollama and ambient cases are
  replaced by AC-1, AC-2 and AC-6, not deleted silently (D-11).
- **Settings writes validate only what they change** (PAC-24 AC-17). AC-9
  extends it to removed providers.
- **Response shapes** of parse and brief refresh for the configured case are
  unchanged. Parse is read by the web bar and by `mcp/tools.go:195-203`.
- **OpenAI and Anthropic users with a key** keep working with no action.
- **"Paceday never reads email"** and the manager-visibility rules are not
  touched; the organisation admin gains no visibility into members' AI
  requests, prompts or briefs through Paceday.

## 7. Rollback

**Part 1.** The migration changes the default for new accounts and records,
for existing accounts, which provider each stored key is bound to and whether
the account follows its organisation. It does **not** rewrite any stored
provider value, does not delete any key, and leaves the Ollama, AWS-profile and
GCP columns and their values untouched (they are only no longer read). The down
migration restores the old default and drops only what the up migration
added; it loses nothing that existed before the upgrade. It does lose choices
made after the upgrade that the old schema cannot represent ("follow my
organisation", key-to-provider binding); after a rollback those accounts behave
as they did before the upgrade. Reverting the code restores Ollama and the
ambient identities, and with them the problem of §1.

**Part 2.** The down migration drops the organisation configuration, **including
organisation keys**. That is data loss for organisations that set one: they must
re-enter it after re-upgrading. The release notes must say so. Member accounts
that followed the organisation fall back to "nothing configured" after
rollback of Part 2 alone.

**Upgrade note (operational).** On upgrade, every deployment that used an
ambient identity stops making AI requests with it immediately, by design. Users
on Bedrock, Azure OpenAI or Vertex must add a key; users on Ollama must choose a
key-based provider or follow their organisation. The release notes and
`.env.example` must say so, and that `PACEDAY_LLM_APPROVED_ORIGINS_OLLAMA` and
the ambient-identity guidance are gone.

## 8. Open questions

- **OQ-1 — Is PAC-50's admin proof the right gate for the organisation AI key?**
  Assumed yes (D-6): it is the only notion of organisation admin the product
  has or plans, and it is narrower than "team owner". Cost: an admin repeats a
  Google/Microsoft step-up per change, and organisations on neither Google
  Workspace nor Microsoft 365 depend on the operator's break-glass list. Owner:
  product owner. Resolve: confirm, or name another admin notion. Non-blocking:
  the assumed answer widens nothing.
- **OQ-2 — Should a member be told which provider the organisation uses?**
  Assumed yes (D-7): the member's text and meeting context go there, so they
  should know where. Owner: product owner.
- **OQ-3 — Should Bedrock's regional runtime hosts and the Vertex express host
  be built-in approved destinations** (as OpenAI's and Anthropic's public hosts
  are), now that no deployment identity is sent? Assumed no for Bedrock and
  Azure (no change to PAC-66 part A); for Vertex express the single global host
  is approved by the same operator list (D-4). Owner: contract-author, then
  product owner.
- **OQ-4 — Bedrock short-term keys expire after at most 12 hours** (AWS
  documentation, below), and AWS recommends long-term keys "for exploration
  only". A user who stores a short-term key will see AI stop within a day.
  Assumed: accept either kind, and when Bedrock rejects an expired key, answer
  the provider failure as today (not "not configured"). Owner: contract-author
  for the failure shape, product owner for whether to warn in Settings.
- **OQ-5 — Exact reason set and status for "AI not configured".** The spec fixes
  the reasons' meaning and that the outcome is neither a 5xx nor a provider
  error; the contract stage chooses the status per operation (parse and brief
  refresh are 200-shaped today) and the codes. Owner: contract-author.
- **OQ-6 — Vertex express-mode API keys are documented as Preview** and tied to
  express-mode sign-up. If Google withdraws them, Vertex is removed rather than
  given another mode. Owner: product owner, at that time.
- **OQ-7 — Should the organisation admin be able to forbid personal keys** (so
  all members use the organisation's provider)? Not in the owner's decision;
  not built (the owner's decision makes the user's key take precedence).
  Owner: product owner.

## Decisions

### Owner, 2026-10-27

Recorded from `.claude/factory/owner-decisions.md` (2026-10-27). These override
any factory assumption.

- **O-1 (owner, 2026-10-27).** Paceday does not run or pay for LLM execution.
  The organisation and/or the user supply their own API key or provider
  account.
- **O-2 (owner, 2026-10-27).** No deployment-level LLM credential.
- **O-3 (owner, 2026-10-27).** No ambient cloud identity. Providers that only
  work through an ambient identity (AWS default chain for Bedrock,
  DefaultAzureCredential for Azure OpenAI, Google ADC for Vertex) are removed,
  unless the provider offers an API-key mode, in which case only the API-key
  mode is kept.
- **O-4 (owner, 2026-10-27).** Ollama is removed from the product (server,
  contract, web).
- **O-5 (owner, 2026-10-27).** An organisation admin can set an org-wide
  provider + key that members use.
- **O-6 (owner, 2026-10-27).** A user can set their own provider + key, which
  takes precedence for them.
- **O-7 (owner, 2026-10-27).** Keys stay write-only (PAC-24).
- **O-8 (owner, 2026-10-27).** When neither a user nor an organisation key is
  set, LLM features are off and say so.
- **O-9 (owner, 2026-10-27).** OQ-B2 (an endpoint advertising enabled
  providers) is not needed: the provider list is fixed and key-based.
- **O-10 (owner, 2026-10-27, PAC-70 OQ-B6).** An operator-approved LLM
  destination may carry a path prefix (for OpenAI-compatible gateways).

### Provider modes (factory, from cited provider documentation)

- **D-2. Azure OpenAI is kept, API-key mode only.** Microsoft documents two
  authentication methods: "API Keys or Microsoft Entra ID"; with an API key,
  "all API requests must include the API Key in the `api-key` HTTP header"
  (Microsoft Learn, *Azure OpenAI REST API reference*, section
  "Authentication",
  https://learn.microsoft.com/en-us/azure/ai-foundry/openai/reference, fetched
  2026-10-28). The Entra ID (bearer) mode is removed with the default
  credential chain. The endpoint, deployment and API version stay
  user-entered and the endpoint stays subject to operator approval. Follows
  O-3.
- **D-3. AWS Bedrock is kept, Bedrock API-key mode only.** AWS documents
  "Amazon Bedrock API keys" that "authenticate API requests using a bearer
  token instead of AWS credentials", passed "directly in the `Authorization`
  header" as `Bearer <key>` (or the `AWS_BEARER_TOKEN_BEDROCK` environment
  variable, which this server must not use since it would be a deployment-level
  credential, O-2), and used against the regional `bedrock-runtime` host.
  Short-term keys last "up to 12 hours"; long-term keys work "only in the
  Region you generated it in" and are recommended "for exploration only"
  (AWS, *Amazon Bedrock User Guide — API keys*,
  https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys.html, fetched
  2026-10-28). The SigV4 mode (default chain, shared profile) is removed. The
  region and model stay user-entered; the region's host stays subject to
  operator approval. Follows O-3; see OQ-4.
- **D-4. Google Vertex AI is kept, express-mode API key only.** Google
  documents that "express mode uses an API key to authenticate", sent in the
  `x-goog-api-key` header (a `key` query parameter only when headers cannot be
  set), against the global `aiplatform.googleapis.com` host, with URLs that
  "omit the project ID and location"; express mode is in Preview and supports
  a listed set of models (Google Cloud, *Vertex AI in express mode overview*,
  https://docs.cloud.google.com/vertex-ai/generative-ai/docs/start/express-mode/overview,
  fetched 2026-10-28). The same page says that after upgrading to a standard
  account one should "use your project ID and location instead of your API
  key", and no Google page found on 2026-10-28 documents an API key for
  standard (non-express) Vertex projects. So only express mode is kept; the
  regional ADC mode is removed, and the GCP project and location settings
  become unused. The key is always sent in the header, never in the URL (a key
  in a URL ends up in logs). The global host is approved through the same
  operator list as today (OQ-3). Assumed by the factory, pending owner
  confirmation; see OQ-6.

### Assumed by the factory, pending owner confirmation

- **D-5. Keys are bound to the provider they were entered for.** Today one key
  slot follows whatever provider is selected, so switching provider sends an
  OpenAI key to Anthropic (or, after this change, to Azure). Binding prevents
  a key from reaching a provider it was not issued by. Existing keys are bound
  to the provider stored at upgrade when that is OpenAI or Anthropic, and to
  nothing otherwise (never sent, never deleted). Assumed by the factory,
  pending owner confirmation.
- **D-6. "Organisation admin" is PAC-50's proven organisation administrator.**
  A person may set, replace or remove the organisation's AI configuration only
  with a fresh, single-use proof under PAC-50's rule (an administrator of the
  organisation's verified Google Workspace domain or Microsoft Entra tenant, or
  an address on the operator's break-glass list), purpose-bound to this write.
  Rejected: "any organisation member" (membership is self-service by email
  domain, `orgs.go:42-52`) and "team owner" (anyone can create a team and
  becomes its owner, so it is equivalent to any member). Part 2 therefore
  cannot land before PAC-50's gate does. Assumed by the factory, pending owner
  confirmation (OQ-1).
- **D-7. Members see the organisation's provider and model, never its key; only
  the admin sees whether a key is set.** A member needs to know where their
  text goes; the key is a secret. Assumed by the factory, pending owner
  confirmation (OQ-2).
- **D-8. Existing accounts are not enrolled in the organisation configuration
  automatically.** New accounts follow the organisation by default (O-5).
  Accounts that existed before the upgrade keep their choice: "off" stays off,
  a working own key stays, and accounts on a removed mode report "not
  configured" and are offered the organisation's AI in one step. Enrolling them
  silently would send their text and meeting context (including Slack and
  Notion excerpts used for briefs) to a destination they never chose, which
  widens who can see their data. Assumed by the factory, pending owner
  confirmation.
- **D-9. "Off" is a user choice the organisation cannot override.** A user who
  turns AI off is never sent through the organisation's key. Follows PRODUCT.md
  principle 3 ("the user stays in control"). Assumed by the factory, pending
  owner confirmation.
- **D-10. Removed-mode settings are retired, not deleted.** The Ollama base URL
  and model, AWS profile and GCP project and location are no longer read,
  validated or returned, and are left in storage for one release so that
  rollback restores them; dropping them is a follow-up. Stored provider values
  are not rewritten. The only user data that becomes unreachable is these
  now-meaningless settings. Assumed by the factory, pending owner
  confirmation.
- **D-11. Tests of removed behaviour are replaced, not skipped.** The 26
  backend tests named for Ollama or the ambient modes (§2.5) pin behaviour the
  owner removed. Each is either rewritten to pin the new behaviour (AC-1,
  AC-2, AC-6, AC-8) or deleted in the same commit that deletes the code it
  covers, with the PR listing each deleted test and the AC that replaces it.
  Coverage floors are not lowered. Assumed by the factory, pending owner
  confirmation.
- **D-12. Brief refresh does not overwrite a ready brief with a
  "not configured" failure** (AC-14). Today a refresh with no AI replaces a
  good brief with a failed one. Assumed by the factory, pending owner
  confirmation.
- **D-13. A Bedrock key the provider rejects (expired, wrong region) is a
  provider failure, not "not configured".** The user did configure AI; the
  provider refused it. Assumed by the factory, pending owner confirmation
  (OQ-4).
