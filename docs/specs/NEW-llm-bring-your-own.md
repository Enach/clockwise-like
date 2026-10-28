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
- **Status**: revised after challenge (revision 1, 2026-10-28; awaiting
  `spec-challenger` re-check). The changes and the answer to each finding are
  in "Revision 1" at the end.
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
    2026-10-27 and recorded in Decisions (O-9, O-10). This spec closes both
    follow-ups.
  - **API-080** (`docs/factory/api-audit.md:338`, status Reported) is on
    `POST /api/nlp/parse`, an operation this spec changes: a missing
    `range_start` / `range_end` serialises as `0001-01-01T00:00:00Z` because the
    fields are `time.Time` with `omitempty` (`backend/nlp/parser.go:167-168`;
    also described in `contracts/openapi/paths/scheduling.notes.md:228`). The
    "AI not configured" parse answer carries no range, so it would carry the
    zero date. **Fixed here** (AC-35, D-20), not documented under rule 2.
  - **U-05** (`docs/factory/api-audit.md:475`) notes that its "unreachable 400"
    finding "applies to the refresh route too", but no `x-uncertain` marker sits
    on the brief-refresh operation (`contracts/openapi/paths/habits.yaml:487`).
    If the contract stage documents a 400 on refresh, U-05 is its scope too.
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
  path prefix. The parse response stops sending a zero date (API-080).
  ACs 1–21, 32–35, 37, 39 and 40 (the Part 1 rows of its table).
- **Part 2 — organisation-wide provider and key.** An organisation admin (D-6)
  sets one provider and key for the organisation. A member's own key takes
  precedence for that member only. A follower's requests use only the
  organisation's values. Members see the organisation's configuration, never
  the key. ACs 22–31, 36, 38, 41 and the Part 2 rows of AC-40.

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
  understand you". A result with no time range carries
  `range_start` / `range_end` as `0001-01-01T00:00:00Z` (API-080,
  `backend/nlp/parser.go:167-168`).
- **Meeting-brief refresh** (`POST /api/meetings/{event_id}/brief/refresh`):
  200 with a brief whose status is `failed` and whose text is
  "LLM not configured" or a refusal message
  (`backend/api/handlers_meeting_briefs.go:74-91`,
  `backend/engine/meeting_brief_service.go:76-83`). The service first stores a
  `pending` brief (`meeting_brief_service.go:60-62`), then fetches Slack and
  Notion context (:64-74), and only then checks for a client; the failed brief
  is then stored, replacing any earlier one.
- **Provider probe** (`POST /api/llm/test`): a factory error that is not a
  destination refusal is answered **500** with a generic body
  (`backend/api/handlers_llm.go:83-86`). A user who simply has no key gets a
  server error. The probe takes `llmProvider` from the body and uses the
  single stored key for it (`handlers_llm.go:51-63`); the web app always sends
  the provider of the unsaved draft (`smart-calendar-flow`
  `src/api/client.ts:836`). So picking another provider and pressing "Test
  connection" sends the stored key to that provider.
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
  PAC-50's proof is a grant bound to (user, organisation, identity provider),
  valid 10 minutes and consumed by one SSO write (`PAC-50.md:78`); its
  step-up is started for an SSO write specifically (`PAC-50.md:103`). The
  break-glass path has no grant: it authorises a write when the session was
  issued within the last 10 minutes (`PAC-50.md:154`, AC-14a). Nothing on a
  read identifies an administrator.

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
  fields, and a base URL field shown only for Ollama. The key field is shown
  only for OpenAI and Anthropic.
- The Ollama base URL field writes the **general** base URL (`llmBaseUrl`), not
  the Ollama-specific one (`Settings.tsx:742-747`; mapping
  `src/api/client.ts:717`). The server's Ollama branch falls back to the
  general base URL (`llm_factory.go:51-53`), and the same general base URL is
  read for OpenAI and Anthropic (:26, :35, :38, :47). A write of it is accepted
  only if it is approved for at least one of OpenAI, Anthropic or Ollama
  (`backend/api/handlers_settings.go:271-278`). No web field shows the general
  base URL for any other provider.
- The Bedrock model picker writes the **general** model (`Settings.tsx:618-622`);
  the client has no mapping for the Bedrock-specific model
  (`src/api/client.ts:713-728`), which is what the server reads
  (`llm_factory.go:17`) and which defaults to a fixed model
  (`backend/storage/migrations/002_llm_providers.up.sql:3`). So a web user's
  Bedrock model choice has never reached the server.

### 2.5 Existing tests that pin today's behaviour

26 backend test functions are named for Ollama, Bedrock, Vertex or Azure (for
example `TestBuildLLMClient_Ollama`, `TestOllamaClient_Complete_Mock`,
`TestNLPService_Parse_WithOllama` in `backend/nlp/parser_test.go`), plus Ollama
cases in `backend/api/pac24_scope_test.go`, `pac66b_base_url_test.go`,
`pac66_llm_destination_test.go`, `handlers_settings_test.go`,
`backend/config/llm_user_destinations_test.go` and
`backend/nlp/bedrock_credentials_test.go`. The key round trip is pinned in e2e
by `e2e/tests/settings.spec.ts:40-60`.

Outside the backend, these also pin Ollama:

- `e2e/tests/llm-base-url.spec.ts:48-56` (the Ollama probe answers 422 with
  "not enabled on this deployment");
- `e2e/docker-compose.e2e.yml:121-123` (comment naming the Ollama origin
  variable);
- `smart-calendar-flow` `src/api/settingsAdapter.test.ts:85,102,198`
  (`llmProvider: "ollama"`), `src/api/types.ts:13` (the hand-written provider
  union) and the mock probe in `src/api/client.ts:1431-1437` (branches on
  `ollama` and `aws_region`).

## 3. Desired behaviour

**Nobody but the organisation or the user pays.** Paceday makes an AI request
only with a credential the organisation or the user entered. It never uses an
identity, credential file, environment credential or metadata service of the
machine it runs on. An operator has no setting that supplies a key.

**New and existing accounts.** In this spec an *existing* account is one created
before Part 1 is deployed; a *new* account is one created after, including
between Part 1 and Part 2. "The upgrade" means Part 1's deployment unless a
sentence names Part 2.

**Which account is used: one state, one outcome, one reason.** For each user, at
the moment of each AI request (parse, brief refresh or probe), the user's stored
choice and the conditions below select exactly one row. Rows are evaluated top
to bottom and the first match wins. Only rows 2 and 4 send anything. Every
other row answers the documented "AI not configured" outcome with the row's
reason, and the three operations give the same reason for the same state.

| # | The user's stored choice | Condition | Outcome and reason | Who can fix it |
|---|---|---|---|---|
| 1 | Off | none | not configured: **off by the user's choice** | the user |
| 2 | A supported provider of their own | a key bound to that provider is on file | **configured**: the user's own provider, model, destination values and key | n/a |
| 3 | A supported provider of their own | no key bound to that provider | not configured: **that provider needs a key** (naming it) | the user |
| 4 | Follow the organisation (Part 2) | the user's organisation has a complete configuration and the user has confirmed it (D-15) | **configured**: the organisation's provider, model, destination values and key, and nothing else | n/a |
| 5 | Follow the organisation (Part 2) | the organisation has a complete configuration and the user has not confirmed it | not configured: **your organisation provides AI; confirm to use it** | the user, in one step |
| 6 | Follow the organisation | the user has no organisation, or it has no complete configuration | not configured: **nothing configured** | the user (own key) and, when the user has an organisation, its admin |
| 7 | A removed provider (Ollama) | the user's organisation has a complete configuration (Part 2) | not configured: **Ollama is no longer supported; your organisation provides AI, choose it in Settings** | the user |
| 8 | A removed provider (Ollama) | otherwise | not configured: **Ollama is no longer supported** | the user and, when the user has an organisation, its admin |

Consequences of the table, stated so no reader has to derive them:

- A stored Bedrock, Azure OpenAI or Vertex choice is a **supported** provider
  (rows 2 and 3). Such an account with no key bound to that provider answers
  "needs a key" naming the provider, whether the choice was made before or
  after the upgrade and whether or not its organisation is configured. The
  "no longer supported" reasons name only Ollama.
- Row 3 applies to an own-provider user even when their organisation is
  configured: a user who chose their own provider is not silently moved to the
  organisation's (AC-27).
- In Part 1 nobody can satisfy row 4 or 5; a follower is in row 6.
- "Complete configuration" means a supported provider, a model, every
  destination value that provider needs, and a key bound to that provider.

A user's own key applies to that user only. It never applies to another
member, and the organisation's key never becomes visible to a member.

**A follower uses only the organisation's values.** The organisation
configuration holds every value its provider needs to reach it: provider,
model, key and the destination values (for Azure OpenAI the endpoint,
deployment and API version; for Bedrock the region; for OpenAI and Anthropic
an optional approved gateway base URL). A request in row 4 uses only these.
No value stored on the member's own account (base URL, endpoint, deployment,
API version, region, model or key) is ever combined with the organisation's
key, and no organisation value is ever combined with the member's own key. The
organisation's destination values are subject to the same operator approval
as a user's (§6). D-18.

**Supported providers.** OpenAI, Anthropic, Azure OpenAI with an API key, AWS
Bedrock with a Bedrock API key, and Google Vertex AI with an express-mode API
key (D-2, D-3, D-4). Ollama is gone. A key is bound to the provider it was
entered for: changing provider does not carry the key to the new provider, and
changing back makes the earlier key usable again. The user, or the admin,
enters a key for the new provider.

**The probe sends a key only to the provider it was entered for** (D-19). A
probe that names provider X uses the key submitted with the probe or, if none
is submitted, the key bound to X on the user's own account, together with the
user's own destination values for X. If neither key exists it answers "needs a
key" for X and sends nothing. This holds whether or not X is the saved
provider, so "Test connection" on an unsaved provider choice never carries the
saved provider's key. A follower's probe uses the organisation configuration
only when it names no provider, model or key, or names exactly the
organisation's provider and no model or key; it then tests the organisation
configuration exactly as the follower's requests would use it (row 4; in
rows 5 and 6 it answers that row's reason and sends nothing). Any other probe
by a follower is a probe of the user's own configuration under the first rule.
No probe sends the organisation's key with a provider, model or destination
value taken from the request or from the member's account.

**"AI not configured" is an answer, not an error.** Every AI-backed operation
answers the same documented outcome in rows 1, 3 and 5–8: not a server error and
not a provider error. It carries the row's reason, from a fixed set, and says
who can fix it. A parse answer with no time range carries no range at all,
never a placeholder date (API-080).

**Brief refresh with AI not configured changes nothing stored.** It answers the
not-configured outcome, fetches no workspace context for a brief it will not
generate, and leaves whatever brief was stored for that event (none, pending,
ready or failed) exactly as it was (D-12).

**The web app** shows only the supported providers. When AI is not configured
for the user, the natural-language bar and Settings say that AI features are
off, why (the row's reason), and who can turn them on, instead of an error.
"Use my organisation's AI" is offered only to a user who belongs to an
organisation. For OpenAI and Anthropic, Settings shows the optional base URL
(gateway) field with its stored value and lets the user clear it, so a value
stored under an earlier provider is visible and removable. The Bedrock model
picker reads and writes the model the server uses for Bedrock (D-16).

Settings has an organisation AI section that is **the same for every member of
the organisation**, administrator or not: the provider, the model, the
destination, whether a key is set (never the key) and who can change it. No
read distinguishes an administrator, because none can (§2.3, D-7). Any member
may start editing; saving requires PAC-50's administrator proof (below), and a
member who cannot give it is refused and told who can change the configuration.

**Existing accounts.** No account is moved onto a provider it did not choose.

- An account whose stored choice is "off" stays off (row 1), including after
  Part 2.
- An account on OpenAI or Anthropic with a key keeps working as today; that key
  is bound to that provider (row 2).
- An account on OpenAI or Anthropic without a key is in row 3.
- An account on Bedrock, Azure OpenAI or Vertex stays on that provider and is in
  row 3 until the user adds a key. The Bedrock region and model, the Azure
  endpoint, deployment and API version, and the Vertex model that the server
  reads today are kept and used once a key is added. The model a web user
  picked for Bedrock was stored in the general model setting and never reached
  the server (§2.4); it is not moved (D-16).
- An account on Ollama (most accounts, by default) is in row 8, and row 7 once
  its organisation is configured. It is **not** enrolled in the organisation's
  configuration automatically, because that would send the user's text and
  meeting context to a destination they never chose (D-8). Settings offers
  "use my organisation's AI" (when the user has an organisation) and "use my
  own key" in one step.
- The settings that only meant something in the removed modes (Ollama base URL
  and model, AWS profile, GCP project and location) are no longer read or
  returned. They are left in storage untouched until a later release, so
  rolling back restores them (§7, D-10).
- **A base URL stored through the web's Ollama field** (§2.4) sits in the
  general base URL, which stays in use for OpenAI and Anthropic. It is not
  rewritten or deleted, and it is sent nowhere: a base URL is used only toward a
  destination the operator approved for the selected provider (§6), and an
  origin approved only for Ollama is approved for no remaining provider. So a
  request on OpenAI or Anthropic with such a value is refused before anything
  is sent, naming the base URL, as PAC-66-B does today. Settings shows the value
  in the OpenAI / Anthropic base URL field, where the user can clear it.
  Clearing it is always accepted, and writing it back unchanged is accepted
  (writes validate only what they change). D-14.
- A key stored on an account whose provider at upgrade is not OpenAI or
  Anthropic is not bound to any provider and is never sent. Settings reports
  that a key is on file that does not match the selected provider and offers
  to replace or remove it. It is not deleted.

**New accounts** follow their organisation by default (owner decision: org
members use the org's key). Before Part 2, and whenever the user has no
organisation or it has no complete configuration, that is row 6. Once the
organisation is configured, the user's first AI action shows, once, that their
text and meeting context (event titles, attendees, and the Slack and Notion
excerpts used for briefs) will go to the organisation's provider account,
which the people who run that account can see outside Paceday, and asks them to
confirm. Until they confirm nothing is sent (row 5); after they confirm, AI
works without opening Settings (row 4). Choosing "use my organisation's AI" in
Settings shows the same notice and counts as confirming. D-15.

**Gateways.** An operator may approve an OpenAI or Anthropic destination that
includes a path prefix, for an OpenAI-compatible gateway (owner decision OQ-B6).
Requests to it go to that prefix followed by the provider's one chat operation
and nowhere else. Everything else about approved destinations is unchanged
(§6).

**Organisation admin.** Only a person proven to be an administrator of the
organisation can set, replace or remove the organisation's provider, key and
destination values (D-6). Being a member, or owning a team, is not enough.
The proof is checked when the write is made, never on a read.

## 4. Acceptance criteria

"Row N" refers to the table in §3. AC numbers from the challenged draft are kept;
AC-35 to AC-41 are new in revision 1.

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
for that provider (row 3). Today a request is made with the ambient token.
[unit]

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
Ollama values) and whose organisation is not configured, when they read their
settings, then the response is valid against the contract (no removed provider
value, no removed property) and reports that the user has no usable provider
and the reason "Ollama is no longer supported" (row 8). Given instead a user
stored on Bedrock, Azure OpenAI or Vertex with no key, the same read is valid
against the contract, still shows that provider as selected, and reports the
reason "needs a key" for it (row 3). [contract] [unit]

AC-9. Given either user of AC-8, when they write back exactly the settings
document they read, then the write succeeds and their stored provider
choice, their stored provider-specific values and their stored key are
unchanged (in particular the user is not switched to "off" and not switched to
"follow organisation"). [unit] [e2e]

AC-10. Given the Ollama user of AC-8, when they parse a sentence, refresh a
brief or run the probe, then each answers the documented "AI not configured"
outcome with the reason "Ollama is no longer supported" (row 8), and no request
is made to any host. Today parse and refresh answer "ollama is not enabled on
this deployment", and the probe answers 422 (origin not approved) or 502
(approved origin unreachable), never a not-configured outcome. [contract]
[unit]

AC-11. Given a user with AI turned off by their own choice, when they parse,
refresh a brief or run the probe, then each answers "AI not configured" with
reason "off by user" (row 1), and nothing is sent. [unit]

AC-12. Given a user on OpenAI or Anthropic with no key, when they run the
probe, then the answer is the documented "AI not configured" outcome with
reason "needs a key" (row 3), not a 5xx. Today it is 500 (§2.2). [contract]
[unit]

AC-13. Given any user for whom AI is not configured, when they parse a sentence,
then the response lets a client tell "AI not configured" apart from "the model
did not understand you" without reading message text, and the existing
response fields a current client reads (`intent`, `error`) still let the
current web bar and the MCP tool show a sentence rather than fail. [contract]
[unit]

AC-14. Given any user for whom AI is not configured and, for one event, each
stored brief state in turn (no brief, a pending brief, a ready brief, a failed
brief), when they refresh that event's brief, then the answer is the documented
not-configured outcome; the brief read afterwards answers exactly what it
answered before the refresh (no brief stays "no brief", a ready brief is still
returned with its text and generation time); no pending or failed brief is
written at any point during the refresh; and no Slack or Notion request is made
for it. Today a pending brief is written first and a failed brief then replaces
whatever was stored (§2.2). [unit]

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

AC-20. Given `docker-compose.yml`, `docker-compose.prod.yml`,
`e2e/docker-compose.e2e.yml` and `.env.example`, when a test reads them, then
none mentions Ollama or an ambient cloud identity, and `.env.example` states
that Paceday supplies no LLM credential. Today all four mention Ollama.
[unit]

AC-21. Given a signed-in user for whom AI is not configured, when they open the
dashboard and use the natural-language bar, then the page says that AI
features are off, why (the row's reason), and who can turn them on (the user in
Settings, and their organisation's admin when they have an organisation),
with a link to Settings, instead of an error. When they open Settings, then the
provider list offers only "Off", OpenAI, Anthropic, Azure OpenAI, AWS Bedrock
and Google Vertex AI, plus "Use my organisation's AI" (Part 2) **only when the
user belongs to an organisation**; no Ollama, AWS profile, GCP project or GCP
location field is shown; and for OpenAI and Anthropic an optional base URL
field shows the stored value. A user with no organisation sees no
organisation option. [e2e]

AC-35. Given a parse whose result has no time range (an "AI not configured"
answer, or a configured parse whose intent is "unknown"), when the response is
read, then it has no range-start and no range-end property at all, and no
property anywhere in the parse response equals `0001-01-01T00:00:00Z`; a parse
that does find a range still carries both. The contract documents the range
properties as absent when there is no range, and API-080 is marked Fixed. Today
the zero date is sent. [contract] [unit]

AC-37. Given a user with a key bound to OpenAI, no key bound to Anthropic and
OpenAI saved as their provider, when the probe names Anthropic and carries no
key, then it answers "AI not configured" with "needs a key" for Anthropic and
no request reaches any host; when the same probe carries a key, that submitted
key is the only key sent, and it goes only to Anthropic; when the probe names
OpenAI and carries no key, the OpenAI key is sent to OpenAI. In the web app,
choosing Anthropic in Settings without saving and pressing "Test connection"
with no key typed shows the "needs a key" answer. Today the OpenAI key is sent
to Anthropic (§2.2). [unit] [e2e]

AC-39. Given a user stored on Ollama whose general base URL holds an origin the
operator approved for no remaining provider (on the e2e stack, any origin: no
extra OpenAI or Anthropic host is approved), when they open web Settings and
choose OpenAI, then the base URL field shows that stored value. When they clear
it, enter a key and save, then the settings read reports OpenAI, no base URL
and a key on file for OpenAI, and a probe of the saved settings is neither a
destination refusal nor "AI not configured" (it reaches the provider call; on
the e2e stack that is the provider-failure answer). When instead they save
OpenAI with a key without clearing the field, the save succeeds and the probe
is a destination refusal naming the base URL, with nothing sent. Today the web
shows no base URL field for OpenAI, so the value cannot be seen or cleared.
[e2e]

AC-40. Given one account per row of §3's table (rows 1, 3, 6 and 8 in Part 1;
all eight after Part 2), when each parses a sentence, refreshes a brief and
runs the probe naming nothing, then all three operations give exactly that
row's outcome and reason, and only rows 2 and 4 send a request. In particular,
an account stored on Bedrock with no key gives "needs a key" for Bedrock both
before and after its organisation is configured, and an account stored on
Ollama gives row 8's reason, then row 7's once its organisation is configured.
[unit]

### Part 2 — organisation-wide provider and key

AC-22. Given a member of organisation A who has not proven admin status, when
they set, replace or remove A's provider, key or destination values, then the
request is refused (not permitted, with a reason a client can act on, pointing
to the admin proof) and A's configuration is unchanged. A team owner who is
not an admin is refused the same way. [contract] [unit]

AC-23. Given a person who has completed PAC-50's administrator step-up for
organisation A **for an organisation-AI write**, when they set A's provider,
model, destination values and key, then the write succeeds, and a second write
without a new step-up is refused as in AC-22 (one grant authorises one write).
Given a grant obtained for an SSO write, when they write A's AI configuration,
then the write is refused and the SSO grant is not consumed; and the reverse.
Given an address on the operator's break-glass list whose session was issued
within the last 10 minutes, when they write, then the write succeeds as in
PAC-50 AC-14a (break-glass authorises by session freshness, not by a grant),
and with a session older than 10 minutes it is refused. [unit]

AC-24. Given A has a stored key, when any member of A, including the person who
set it, reads A's configuration or their own settings, then no response
contains the key; and every member's read of A's configuration is identical:
the provider, the model, the destination, whether a key is set, and who can
change it. Two members, one of whom has just written the configuration with a
proof, receive byte-identical organisation-configuration reads. [contract]
[unit] [e2e]

AC-25. Given a user in organisation B, when they read or write organisation A's
configuration by any means the contract offers, then they are refused and learn
nothing about A's configuration. [unit]

AC-26. Given A is configured, a member M1 who follows the organisation (and has
confirmed, D-15), and a member M2 with their own complete configuration, when
each parses a sentence, then M1's request carries A's key to A's provider and
M2's request carries M2's key to M2's provider, and no request carries A's key
for M2 or M2's key for anyone else. [unit]

AC-27. Given M2 of AC-26 removes their own key, when M2 parses a sentence, then
the answer is "needs a key" for M2's provider (row 3: M2 chose their own
provider) and nothing is sent, until M2 switches to "use my organisation's
AI". [unit]

AC-28. Given a new account (created after Part 1's deployment) in organisation
A, when A is not configured, then the account answers "nothing configured"
(row 6) and names the organisation's admin as someone who can enable AI. When A
becomes configured, then the account follows A without the user opening
Settings, subject to the one-time confirmation of AC-41. [unit] [e2e]

AC-29. Given an account that existed before Part 1's deployment and whose
stored provider is Ollama (AC-8), when A becomes configured, then the account
answers "not configured" with row 7's reason ("Ollama is no longer supported;
your organisation provides AI, choose it in Settings"), and nothing is sent
until the user chooses. An existing account stored on Bedrock, Azure OpenAI or
Vertex with no key keeps answering "needs a key" (row 3, AC-40). [unit]

AC-30. Given an organisation admin, when they remove A's key, then every member
following A answers "nothing configured" on their next AI request, and members
with their own keys are unaffected. [unit]

AC-31. Given the web Settings page and organisation A configured, when any
member of A opens it, then the organisation AI section shows the provider, the
model, the destination, whether a key is set (never the key) and who can change
it, identically for every member. When a member presses "Edit" and saves a
change, then the page sends them through PAC-50's step-up (or, for a
break-glass address with a fresh session, saves directly); a member who cannot
prove admin status sees the refusal of AC-22 and who can change the
configuration, and the section is unchanged. The key field is write-only and
shows only "a key is set". A user with no organisation sees no organisation AI
section. [e2e]

AC-36. Given A configured for Azure OpenAI with approved endpoint E_A,
deployment D_A and key K_A, and a confirmed follower M1 who has stored on their
own account a different approved Azure endpoint E_M1, another deployment, a
Bedrock region and an approved OpenAI gateway base URL, when M1 parses a
sentence and runs the probe naming nothing, then every request goes to E_A's
chat operation for D_A carrying K_A; E_M1, the gateway and any Bedrock host
receive nothing; and no request carries K_A together with any value from M1's
account. Given instead A configured for OpenAI with no gateway and M1 with a
stored approved gateway, M1's request goes to OpenAI's public host and the
gateway receives nothing. [unit]

AC-38. Given a confirmed follower M1 of A (A configured for OpenAI with key
K_A) who has no keys of their own, when M1's probe names nothing, then it tests
A's configuration and sends K_A only to A's destination; when it names
Anthropic with no key, or names OpenAI with a model, then it answers "needs a
key" for that provider and sends nothing; when it names Anthropic with a key,
only that submitted key is sent. In no case is K_A sent to a provider other
than A's or with a provider, model or destination value from the request. An
unconfirmed follower's probe naming nothing answers row 5's reason and sends
nothing. [unit]

AC-41. Given a new account in A, A configured, and the user has not confirmed,
when they first use the natural-language bar, then nothing is sent, the answer
is row 5, and the page shows a notice naming A's provider and saying that the
text and meeting context (event titles, attendees, and the Slack and Notion
excerpts used for briefs) go to the organisation's provider account, with a
confirm action. After they confirm, the next parse is sent with A's
configuration and the notice is not shown again. Choosing "Use my
organisation's AI" in Settings shows the same notice and counts as confirming.
[unit] [e2e]

### Both parts

AC-32. Given the assembled contract, when it is read, then the documented
"AI not configured" outcome, with its reason set, is present on every
AI-backed operation (parse, brief refresh, probe), and (Part 2) the
organisation configuration operations document their refusal for non-admins.
[contract]

AC-33. Given the web repo at the commit that consumes each part's contract,
when a test compares every provider value the web code accepts or offers
(generated types, any hand-written provider type, the Settings provider list
and the client's mock probe) with the provider value set in that contract,
then they are the same set: no value the contract lacks (for example `ollama`)
and none missing. Today the hand-written provider type and the mock probe
contain `ollama`. [unit]

AC-34. Given the e2e suite against the compose stack with no key anywhere, when
a fresh user parses a sentence, then the API answers "AI not configured" with
reason "nothing configured" (row 6) and not a 4xx/5xx error, and the dashboard
shows the "AI features are off" message of AC-21. [e2e]

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
- **Testing an unsaved organisation configuration.** The probe tests what a
  follower would use (AC-38); an admin who wants to test before saving does so
  with their own key. A "test before save" for the organisation is a later
  change if needed.
- **Clearing a stored base URL automatically.** A base URL stored for Ollama is
  shown and clearable (AC-39), never rewritten by the upgrade (D-14).
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
  replaced by AC-1, AC-2 and AC-6, not deleted silently (D-11). The same holds
  for the e2e and web files listed in §2.5: the Ollama probe case of
  `e2e/tests/llm-base-url.spec.ts` is replaced by AC-10's case, and the web
  adapter tests' `ollama` fixtures by a supported provider. The organisation's
  destination values are under the same rules.
- **Settings writes validate only what they change** (PAC-24 AC-17). AC-9
  extends it to removed providers, and AC-39 to a stored base URL approved for
  no remaining provider.
- **Response shapes** of parse and brief refresh for the configured case are
  unchanged, except that a parse with no range omits the range instead of
  sending the zero date (AC-35, API-080). Parse is read by the web bar and by
  `mcp/tools.go:195-203`; both already treat the range as optional
  (`smart-calendar-flow` `src/api/types.ts:207-208`).
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
as they did before the upgrade.

**Keys entered after the upgrade.** The old schema has one key slot per
account. After the down migration, each account's slot holds the key bound to
the provider selected at rollback when that provider is OpenAI or Anthropic,
and otherwise the key the slot held before the upgrade, if any. **Every other
key entered after the upgrade is lost by the down migration**: keys for Azure
OpenAI, Bedrock or Vertex (the old code does not use a key for them) and any
second key for a provider not selected at rollback. Keeping them in a store
the old code never reads would leave unreadable secrets behind; losing them
means the user re-enters them after re-upgrading. The release notes must say
so. Whether a confirmed follower stays confirmed (D-15) is likewise lost;
after re-upgrading they are asked again. Reverting the code restores Ollama and the
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
  Workspace nor Microsoft 365 depend on the operator's break-glass list. Using
  it here is **a change to PAC-50's design, not plain reuse**: PAC-50's grant
  is started for an SSO write only and carries no purpose, so Part 2 needs the
  grant to carry its purpose (SSO write or organisation-AI write) so that one
  cannot be spent on the other (D-6, AC-23). Owner: product owner (the gate),
  then the Part 2 contract stage (the PAC-50 amendment). Resolve: confirm, or
  name another admin notion. Non-blocking for Part 1: the assumed answer widens
  nothing. Surfaced to the owner again by the challenge (non-blocking 13).
- **OQ-2 — Should a member be told which provider the organisation uses?**
  Assumed yes (D-7): the member's text and meeting context go there, so they
  should know where. Revision 1 also shows every member whether a key is set
  and the destination, because no read can tell an admin from a member (D-7).
  Owner: product owner.
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
- **OQ-8 — Should a new account that follows the organisation by default be
  told, or asked, before its first AI request** that its text and meeting
  context (titles, attendee emails, Slack and Notion excerpts) go to the
  organisation's provider account, whose console the people who run that
  account can read outside Paceday? Assumed: told **and** asked once (D-15),
  the more conservative choice; nothing is sent before the user confirms.
  Owner: product owner (raised by the challenge, non-blocking 11). Non-blocking:
  the assumption widens nothing, and dropping the confirmation later only
  removes a step.
- **OQ-9 — D-8: should pre-existing default-Ollama accounts stay unenrolled
  while new accounts follow the organisation by default?** Assumed yes (D-8).
  Owner: product owner (challenge, non-blocking 12). Non-blocking: enrolling
  them later is possible; un-sending their data is not.
- **OQ-7 — Should the organisation admin be able to forbid personal keys** (so
  all members use the organisation's provider)? Not in the owner's decision;
  not built (the owner's decision makes the user's key take precedence).
  Owner: product owner. Surfaced again by the challenge (non-blocking 13),
  together with OQ-1; OQ-4 (warning about Bedrock short-term keys, D-13) and
  OQ-6 / D-4 (Vertex express mode, in Preview) were surfaced with them
  (non-blocking 14). All four stay assumed as recorded; none widens who sees
  data or deletes any.

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
  with a proof under PAC-50's rule: either a fresh, single-use step-up grant
  showing they administer the organisation's verified Google Workspace domain
  or Microsoft Entra tenant, purpose-bound to an organisation-AI write (one
  grant, one write), or an address on the operator's break-glass list with a
  session issued within the last 10 minutes (PAC-50 AC-14a; no grant, so no
  "one proof, one write" on that path). Purpose-binding is **a change to
  PAC-50's design**: PAC-50's grant has no purpose and its step-up is started
  for SSO writes only (`PAC-50.md:78`, `:103`). The Part 2 contract stage must
  amend PAC-50's grant (or its successor) so that an SSO grant cannot authorise
  an AI-configuration write and the reverse (AC-23); it may not reuse the SSO
  grant as is. The proof is checked only on a write; reads never depend on it.
  Rejected: "any organisation member" (membership is self-service by email
  domain, `orgs.go:42-52`) and "team owner" (anyone can create a team and
  becomes its owner, so it is equivalent to any member). Part 2 therefore
  cannot land before PAC-50's gate does. Assumed by the factory, pending owner
  confirmation (OQ-1).
- **D-7. Every member of the organisation gets the same read of its AI
  configuration: provider, model, destination, whether a key is set, and who
  can change it; never the key.** (Revised in revision 1; the draft showed
  "whether a key is set" only to the admin.) A member needs to know where their
  text goes; the key is a secret. No read can show the admin something
  different, because there is no stored admin role and PAC-50's proof exists
  only around a write (§2.3); inventing a stored role is out of scope (§5).
  Editing starts from the same read, and saving requires the D-6 proof.
  Whether a key is set and the destination are not secrets and are what a
  member needs to understand an "AI not configured" answer. Assumed by the
  factory, pending owner confirmation (OQ-2).
- **D-8. Existing accounts are not enrolled in the organisation configuration
  automatically.** "Existing" means created before Part 1's deployment (§3).
  New accounts follow the organisation by default (O-5), subject to D-15.
  Accounts that existed before the upgrade keep their choice: "off" stays off,
  a working own key stays, and accounts on a removed mode report "not
  configured" and are offered the organisation's AI in one step. Enrolling them
  silently would send their text and meeting context (including Slack and
  Notion excerpts used for briefs) to a destination they never chose, which
  widens who can see their data. Assumed by the factory, pending owner
  confirmation (OQ-9).
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
  backend tests named for Ollama or the ambient modes (§2.5), and the e2e and
  web files listed there (`e2e/tests/llm-base-url.spec.ts:48-56`,
  `e2e/docker-compose.e2e.yml:121-123`, `smart-calendar-flow`
  `src/api/settingsAdapter.test.ts:85,102,198`, `src/api/types.ts:13`, the mock
  probe in `src/api/client.ts:1431-1437`), pin behaviour the owner removed. Each is either rewritten to pin the new behaviour (AC-1,
  AC-2, AC-6, AC-8) or deleted in the same commit that deletes the code it
  covers, with the PR listing each deleted test and the AC that replaces it.
  Coverage floors are not lowered. Assumed by the factory, pending owner
  confirmation.
- **D-12. Brief refresh with AI not configured changes no stored brief**
  (AC-14). Whatever was stored for the event (nothing, pending, ready, failed)
  stays as it was, and no workspace context is fetched. Today a refresh with
  no AI first writes a pending brief and then replaces it with a failed one,
  so a good brief is lost; storing a "not configured" failure where there was
  no brief would also make the brief read report a failure the user never
  asked for. Assumed by the factory, pending owner
  confirmation.
- **D-14. A base URL stored through the web's former Ollama field is kept,
  shown and clearable, never sent.** It sits in the general base URL, which
  OpenAI and Anthropic still read (§2.4). It is not rewritten or deleted by the
  upgrade (that would delete a user value); it is sent nowhere unless the
  operator approves it for the selected provider; the web shows it for OpenAI
  and Anthropic so the user can clear it (AC-39). Rejected: ignoring it at send
  and using the provider's public host, which would send text somewhere other
  than the destination the user set. Assumed by the factory, pending owner
  confirmation.
- **D-15. A follower confirms once before the organisation's configuration is
  used.** A new account follows by default (O-5), but the first AI action
  tells the user where their text and meeting context will go and asks them to
  confirm (row 5, AC-41). This is the more conservative answer to the
  challenge's owner question (OQ-8). Assumed by the factory, pending owner
  confirmation.
- **D-16. The Bedrock model is the Bedrock-specific setting the server reads
  today.** The web's Bedrock picker is changed to read and write it. The model
  a web user picked earlier sits in the general model setting and never
  reached the server (§2.4); it is not copied over, because the server has
  never used it and the Bedrock-specific setting always holds a value. Assumed
  by the factory, pending owner confirmation.
- **D-17. One reason per stored state, by the table in §3.** Order: off; own
  provider (configured or needs a key); follow the organisation (configured,
  confirm, or nothing configured); removed provider (organisation available, or
  not). A stored Bedrock, Azure OpenAI or Vertex choice is a supported provider
  and gets "needs a key", never a removed-mode reason. Replaces the draft's
  overlapping reason list. Assumed by the factory, pending owner confirmation.
- **D-18. A follower's request uses only the organisation's values.** The
  organisation configuration holds every destination value its provider needs,
  and no member value is ever combined with the organisation key (§3, AC-36).
  The operator's approval lists are deployment-wide, so "approved" alone could
  mean another organisation's gateway; the organisation key goes only where the
  organisation's own configuration points. Assumed by the factory, pending
  owner confirmation.
- **D-19. A probe sends a key only to the provider it was entered for** (§3,
  AC-37, AC-38). A follower's probe uses the organisation configuration only as
  the follower's requests would use it. Assumed by the factory, pending owner
  confirmation.
- **D-20. API-080 is fixed here, not documented.** A parse with no range omits
  the range (AC-35). Both consumers already treat the range as optional
  (§6), and documenting a zero date on the new "not configured" answer would
  put a known-wrong value into a new contract. Assumed by the factory, pending
  owner confirmation.
- **D-13. A Bedrock key the provider rejects (expired, wrong region) is a
  provider failure, not "not configured".** The user did configure AI; the
  provider refused it. Assumed by the factory, pending owner confirmation
  (OQ-4).

## Challenge — 2026-10-28

Challenger: `spec-challenger`, on the committed spec at `8204c9c`
(branch `factory/new-llm-bring-your-own-spec`). No Linear issue exists for this
spec (header), so no Linear comment was posted; this section is the only copy.

**Verdict**: accept-with-changes

The direction is sound and most of §2 checks out against the code. Six findings
block the contract stage because they leave a request the spec permits that
leaks a key, two statements the spec itself contradicts, a web consumer that
strands users, and an open audit row on a touched operation.

### Blocking

1. **The organisation key can be sent to a destination the member chose.**
   §3 and AC-23 say the organisation configuration is "provider, model and key".
   Azure OpenAI needs an endpoint, deployment and API version, Bedrock needs a
   region, and an OpenAI/Anthropic gateway needs a base URL. Today every one of
   those values is read from the caller's own settings row
   (`backend/nlp/llm_factory.go:17-35`). The spec never says where a follower's
   destination comes from. Read literally, a member who follows the
   organisation and has a stored `llmBaseUrl` or `azureEndpoint` sends the
   organisation's key there. The operator's approval lists are
   deployment-wide (`backend/config/llm_hosts.go:16-18`,
   `backend/nlp/user_destination.go:99-104`), so "approved" can mean another
   organisation's gateway. That widens who can see a secret.
   **Resolve:** state that the organisation configuration holds every
   destination value its provider needs. State that a follower's request uses
   only the organisation's values, and that no value from a member's own row is
   ever combined with the organisation key. Add an AC: given A is configured
   for Azure and follower M1 has a different approved `azureEndpoint` stored,
   when M1 parses, the request goes to A's endpoint and M1's endpoint gets
   nothing.

2. **The probe still sends a stored key to a provider it was not entered for.**
   D-5 and AC-15 bind a key to its provider only when the user "changes
   provider". `POST /api/llm/test` accepts `llmProvider` in the body and then
   uses the single stored key for it (`backend/api/handlers_llm.go:51-63`).
   The web always sends the draft's provider (`smart-calendar-flow`
   `src/api/client.ts:836`). Under this spec, a user with an OpenAI key who
   picks "Anthropic" in the dropdown and presses "Test connection" before
   saving still sends the OpenAI key to Anthropic. The same question applies
   to a follower: can a probe that names a provider carry the organisation key
   to a different provider?
   **Resolve:** add an AC that a probe naming provider X uses only a key bound
   to X or a key submitted in the body. Without one, the probe answers "needs
   a key" and sends nothing. Also state what a follower's probe may use.

3. **One account gets three different reasons.** For an account stored on
   `bedrock`, `azure_openai` or `vertex` with no key, the spec says three
   things:
   - §3 "Existing accounts" and the reason list: the removed-mode reason
     ("previous provider no longer supported").
   - AC-2 and the second sentence of AC-8: "needs a key".
   - AC-10, which covers "the user of AC-8": the removed-provider reason.

   Once Part 2 lands and the organisation is configured, AC-29 gives the same
   account a fourth reason. The stored value `bedrock` is the same before and
   after the upgrade, so no test can tell which reason is right.
   **Resolve:** give each stored state exactly one reason, with an order of
   precedence (off, then removed mode or needs a key, then organisation
   available but not chosen, then nothing configured). Then make §3, AC-2,
   AC-8, AC-10 and AC-29 agree with it.

4. **Nothing on a read can recognise an "organisation admin", and §3
   contradicts D-7.** D-6 defines the admin as the holder of PAC-50's proof.
   That proof is a 10-minute, single-use grant consumed by a write
   (`docs/specs/PAC-50.md` §4.1, "Grant"). There is no stored admin role
   (§2.3). Two ACs still need a read to tell admin from member:
   - AC-24: "the admin sees whether a key is set", and a member does not.
   - AC-31: Settings is editable "when an organisation admin opens it" and
     read-only for a member.

   Separately, §3 says a member can read "whether a key is set", and D-7 and
   AC-24 say only the admin can.
   **Resolve:** either make the read the same for every member of the
   organisation, with editing starting the PAC-50 proof, or define how a read
   recognises an admin without a stored role. Then make §3, D-7, AC-24 and
   AC-31 agree.

5. **Former Ollama users on the web are left with a base URL they cannot see
   or clear.** The web's Ollama "base URL" field writes `llmBaseUrl`, not
   `ollamaBaseUrl` (`smart-calendar-flow` `src/pages/Settings.tsx:742-747`).
   The Ollama branch falls back to `llmBaseUrl` (`llm_factory.go:52-53`). So
   web users who set up Ollama have an Ollama origin in `llmBaseUrl`. §3 and
   D-10 retire only `ollamaBaseUrl`, and `llmBaseUrl` stays read for OpenAI and
   Anthropic. Such a user who picks OpenAI and pastes a key is refused at send,
   because the stored value is an `http://…:11434` origin. The web shows a base
   URL field only for Ollama (`Settings.tsx:742`), so after this change the
   user cannot see or clear the value causing the refusal. Write validation
   accepts `llmBaseUrl` only if it is approved for at least one of
   openai/anthropic/ollama (`backend/api/handlers_settings.go:271-278`). Once
   Ollama is removed, such a value is approved for no provider.
   **Resolve:** state what happens to a stored `llmBaseUrl` that was approved
   only as an Ollama origin. Add an AC that a former Ollama user who set a
   base URL in the web app can reach a working OpenAI or Anthropic
   configuration from the web Settings page alone.

6. **Inherited scope leaves out API-080 on a touched operation.** API-080
   (`docs/factory/api-audit.md:338`, status Reported) is on
   `POST /api/nlp/parse`: `range_start` and `range_end` are `time.Time` with
   `omitempty`, so a missing range serialises as `0001-01-01T00:00:00Z`
   (`backend/nlp/parser.go:167-168`, still true). This spec changes the parse
   response (AC-13). The "AI not configured" parse answer is exactly an
   `intent: "unknown"` response with no range, so the new documented outcome
   would carry the bogus zero date. README §7 rule 1 makes the row this spec's
   scope.
   **Resolve:** list API-080 under Inherited scope, and either fix it here or
   say how the contract documents it under rule 2.

### Non-blocking

1. Inherited scope cites the owner answers as "Decisions (D-1, D-9)". There is
   no D-1, and D-9 is "Off cannot be overridden". The owner answers are O-9
   (OQ-B2) and O-10 (OQ-B6).
2. §2.5 and D-11 list only backend tests. Other files also pin Ollama and need
   the same replace-not-skip treatment:
   - `e2e/tests/llm-base-url.spec.ts:48-56` (Ollama probe case);
   - `e2e/docker-compose.e2e.yml:121-123` (Ollama comment);
   - `smart-calendar-flow` `src/api/settingsAdapter.test.ts:85,102,198`
     (`llmProvider: "ollama"`).
3. AC-20 says "the compose files". It should name
   `e2e/docker-compose.e2e.yml`, which also mentions Ollama.
4. The web stores the Bedrock model in `llmModel` (`Settings.tsx:618-622`) and
   has no `bedrockModel` mapping (`src/api/client.ts:713-728`). The backend
   reads `bedrockModel` (`llm_factory.go:17`). §3's "the Bedrock … model a user
   entered [is] kept and reused" is therefore not true for web users. The spec
   should say which property holds the Bedrock model.
5. AC-23 says "one proof authorises one write, as in PAC-50". That does not
   hold for PAC-50's break-glass path, which authorises by session freshness
   with no grant (PAC-50 AC-14a). PAC-50's step-up is also started
   "specifically for an SSO write" (`/api/admin/sso/stepup`). D-6's
   "purpose-bound to this write" therefore needs PAC-50's grant to carry a
   purpose. Say that this is a change to PAC-50's design, not reuse.
6. §7 Part 1 rollback does not say what happens to keys entered after the
   upgrade for Azure, Bedrock or Vertex, or to a second key for another
   provider (AC-15 implies more than one key per user). Say whether the down
   migration loses them.
7. AC-21 offers "Use my organisation's AI" to every user. A user with no
   organisation (generic domain, §2.3) should not be offered it.
8. AC-28 and AC-29 say "the upgrade" without saying Part 1's or Part 2's.
   Accounts created between the two are new under one reading and existing
   under the other.
9. AC-14 covers only an existing *ready* brief. Say what a not-configured
   refresh stores when there is no earlier brief. Note too that the service
   writes a `pending` brief before it checks the LLM
   (`backend/engine/meeting_brief_service.go:60-62`), so the overwrite happens
   before the not-configured check.
10. AC-33 is a process requirement (gates in the same PR pair), not a
    behaviour a test can falsify. It belongs in the plan, or should be
    reworded as an observable property of the generated types.
11. OWNER: When the organisation's key is used, the organisation's provider
    account receives members' text and meeting context: titles, attendee
    emails, and Slack/Notion excerpts in briefs. Provider consoles and logging
    can show that content to whoever administers the account, outside
    Paceday. §6 only says nothing is visible "through Paceday". Should a new
    account that follows the organisation by default be told this, or asked,
    before its first AI request?
12. OWNER: D-8. Should pre-existing accounts that never chose a provider (the
    default Ollama accounts) stay unenrolled, while new accounts follow the
    organisation by default?
13. OWNER: OQ-1 (PAC-50's proof as the admin gate) and OQ-7 (whether an admin
    may forbid personal keys).
14. OWNER: D-4, Vertex in express mode only (Preview). Also D-13, whether to
    warn in Settings about Bedrock short-term keys.

### Criteria I could not falsify

- AC-24 and AC-31: no request distinguishes "an organisation admin reads"
  from "a member reads" (Blocking 4), so no test can tell pass from fail.
- AC-33: no observable behaviour (Non-blocking 10).
- AC-2, AC-8, AC-10 and AC-29, for a stored `bedrock`, `azure_openai` or
  `vertex` account with no key: a test cannot pass both readings (Blocking 3).

### Citations I checked and found wrong

- Inherited scope, "recorded in Decisions (D-1, D-9)": actually O-9 and O-10.
  D-1 does not exist, and D-9 is the "Off" decision.
- AC-10, "the probe answers 422 or 500" for an Ollama user today: wrong.
  `ollama` always builds a client (`llm_factory.go:48-65`). An unapproved
  origin is a 422 at `handlers_llm.go:92-94`, and an approved but unreachable
  one is a 502 at `:96-99` (`writeProviderFailure`, `:113-117`). It is never a
  500.
- §2.4, "an Ollama base URL field": the field exists but writes `llmBaseUrl`
  (`Settings.tsx:742-747`), not `ollamaBaseUrl`, which §3 and D-10 assume
  (Blocking 5).

Checked and found correct: the `llm_factory.go:14-69` branches; Azure
`DefaultAzureCredential` and the bearer header (`llm_azure_openai.go:39`,
`:69-70`); Bedrock `LoadDefaultConfig` and profile (`llm_bedrock.go:44-51`);
Vertex `FindDefaultCredentials` (`llm_vertex.go:44`); `OllamaClient` at
`parser.go:98`; parse at `:191-201`; the probe's 500 at
`handlers_llm.go:83-86`; refresh at `handlers_meeting_briefs.go:74-91`; the
migration 002 default; the provider allow-list at `handlers_settings.go:22-30`;
`Settings.tsx:58-66`; `Dashboard.tsx:184-185`; `handlers_sso.go:166-171`; the
compose line ranges; the 26 backend test functions named for the removed
modes; API-039 at `:297`, API-093 at `:351`; the `x-uncertain` locations; and
the ambient test seams (`backend/nlp/destination.go:55-64`).

### Consumers this breaks

- smart-calendar-flow/src/pages/Settings.tsx:742-747: the only UI that edits
  `llmBaseUrl` is removed with Ollama, which strands former Ollama users
  (Blocking 5).
- smart-calendar-flow/src/api/client.ts:836 (`llmTestBody`): always sends the
  draft provider, so it carries the stored key to an unsaved provider unless
  the probe binds keys (Blocking 2).
- smart-calendar-flow/src/api/client.ts:1431-1437: the mock fallback of
  `llmTest` still branches on `ollama` and `aws_region`. It must follow the new
  provider set.
- smart-calendar-flow/src/api/settingsAdapter.test.ts:85,102,198 and
  src/api/types.ts:13: pin `ollama` as a provider value.
- e2e/tests/llm-base-url.spec.ts:48-56: asserts the Ollama probe 422 and
  "not enabled on this deployment".
- mcp/tools.go:195-203 (`clockwise_parse_command`): passes the parse body
  through. It keeps working only if AC-13's "existing fields still let the MCP
  tool show a sentence" holds. Named here so the contract stage checks it.

## Revision 1 — 2026-10-28

Author: `spec-author`, answering the challenge above (verdict
accept-with-changes, on `8204c9c`). Every blocking and non-blocking change is
applied; each answer names where. Current-behaviour claims added in this
revision were re-read on the branch before writing (`llm_factory.go:14-69`,
`handlers_llm.go:40-120`, `parser.go:160-175`, `meeting_brief_service.go:50-95`,
`handlers_settings.go:260-285`, `002_llm_providers.up.sql`,
`api-audit.md:338`, `PAC-50.md:78,103,154`, and in `smart-calendar-flow`
`Settings.tsx:610-625,736-750`, `client.ts:705-730,830-840`, `types.ts:10-16,200-210`).
No Linear issue exists, so nothing was posted to Linear; this section is the
record.

### Blocking

1. **Organisation key to a member-chosen destination.** Applied. §3 "A
   follower uses only the organisation's values": the organisation
   configuration holds provider, model, key and every destination value its
   provider needs (Azure endpoint, deployment, API version; Bedrock region;
   optional OpenAI/Anthropic gateway), a row-4 request uses only those, and no
   member value is ever combined with the organisation key (or the reverse).
   New AC-36 is the requested test (follower with a different approved Azure
   endpoint, plus a stored gateway and Bedrock region, all receive nothing),
   with a second case for an OpenAI organisation and a member gateway. AC-22
   and AC-23 now cover destination values. D-18.
2. **Probe sends a stored key to another provider.** Applied. §3 "The probe
   sends a key only to the provider it was entered for": a probe naming X uses
   the submitted key or the key bound to X, else "needs a key" and nothing sent,
   whether or not X is saved. A follower's probe uses the organisation
   configuration only when it names nothing, or exactly the organisation's
   provider with no model or key, and never pairs the organisation key with a
   request or member value. AC-37 (Part 1, unit + web e2e for the unsaved
   "Test connection" case) and AC-38 (Part 2, follower). §2.2 now cites the
   probe and `client.ts:836`. D-19.
3. **Contradictory reasons for one account.** Applied. §3 replaces the reason
   list with one table: eight rows, evaluated top to bottom, each with exactly
   one outcome and reason. A stored Bedrock, Azure OpenAI or Vertex account with
   no key is a supported provider and is always "needs a key" (row 3), before
   and after Part 2; the removed-provider reasons name only Ollama (rows 7–8).
   §3 "Existing accounts", AC-2, AC-8 (rewritten in two halves), AC-10 (now
   only the Ollama user, row 8) and AC-29 (Ollama row 7; Bedrock stays row 3)
   agree with the table, and new AC-40 checks every row across all three
   operations. D-17. The order differs from the challenger's suggestion in one
   place: an Ollama account whose organisation is configured gets row 7 (which
   says both that Ollama is gone and that the organisation provides AI) rather
   than the bare removed reason, because that is the action that fixes it. It
   is still one reason for one state.
4. **No read can recognise an admin; §3 contradicted D-7.** Applied, taking the
   challenger's first option. Every member of the organisation gets the same
   read (provider, model, destination, whether a key is set, who can change
   it; never the key); editing is open to any member and saving requires the
   PAC-50 proof, which is checked only on writes. §3, D-7 (rewritten), AC-24
   (now asserts identical reads, including for the person who just wrote) and
   AC-31 (same section for all; save goes through step-up or break-glass;
   refusal shows who can change it) agree. §2.3 now states that nothing on a
   read identifies an administrator.
5. **Former Ollama users' base URL invisible and unclearable.** Applied. §2.4
   now states that the web's Ollama field wrote the general base URL and that
   the Bedrock picker writes the general model. §3 "Existing accounts" states
   what happens: the value is kept (not rewritten or deleted), sent nowhere
   unless approved for the selected provider (refused before sending, naming the
   base URL, as today), and shown in a base URL field the web now offers for
   OpenAI and Anthropic, where it can be cleared; clearing and unchanged
   write-back are always accepted. New AC-39 (e2e) is the requested journey:
   from web Settings alone, the user sees the value, clears it, adds a key and
   reaches a probe that is neither a refusal nor "not configured". AC-21 now
   requires the field. D-14.
6. **API-080 missing from inherited scope.** Applied. Listed under Inherited
   scope and **fixed here**: a parse with no range omits the range properties,
   never the zero date (AC-35, D-20). §2.2 and §6 note the current behaviour and
   that both consumers already treat the range as optional (`types.ts:207-208`;
   the MCP tool passes the body through). U-05's note about the refresh route
   is also listed, since refresh is touched.

### Non-blocking

1. **Wrong decision citation.** Fixed: "Decisions (O-9, O-10)".
2. **Non-backend tests pinning Ollama.** §2.5 lists
   `e2e/tests/llm-base-url.spec.ts:48-56`, `e2e/docker-compose.e2e.yml:121-123`,
   `settingsAdapter.test.ts:85,102,198`, `types.ts:13` and the mock probe at
   `client.ts:1431-1437`; D-11 and §6 extend replace-not-skip to them.
3. **AC-20 names the e2e compose.** AC-20 now names `docker-compose.yml`,
   `docker-compose.prod.yml`, `e2e/docker-compose.e2e.yml` and `.env.example`
   (all four mention Ollama today: 1, 1, 2 and 9 matching lines).
4. **Bedrock model property.** §2.4 states the web writes the general model and
   the server reads the Bedrock-specific one. §3 and D-16: the Bedrock model is
   the Bedrock-specific setting the server reads today; the web picker is
   changed to it; the earlier web pick is not copied (it never reached the
   server).
5. **AC-23 and PAC-50.** AC-23 rewritten: one grant authorises one write on the
   step-up path, purpose-bound (an SSO grant cannot authorise an AI write and
   the reverse), and break-glass authorises by a session issued within 10
   minutes as in PAC-50 AC-14a. D-6 and OQ-1 now say purpose-binding is a
   change to PAC-50's design that the Part 2 contract stage must make, not
   reuse.
6. **Rollback of keys entered after the upgrade.** §7 "Keys entered after the
   upgrade": the single old slot keeps the key bound to the selected provider
   when that is OpenAI or Anthropic, else its pre-upgrade key; every other key
   entered after the upgrade (Azure, Bedrock, Vertex, second keys) is lost by the
   down migration, and the release notes must say so. Confirmation state (D-15)
   is lost too.
7. **"Use my organisation's AI" for users with no organisation.** §3 and AC-21:
   offered only to a user who belongs to an organisation; AC-31 also hides the
   organisation section from them.
8. **"The upgrade" ambiguous.** §3 defines existing as created before Part 1's
   deployment and new as created after, including between Part 1 and Part 2.
   AC-28 and AC-29 say "Part 1's deployment".
9. **AC-14 with no earlier brief; pending write first.** AC-14 now covers each
   stored state (none, pending, ready, failed): a not-configured refresh writes
   nothing, at any point, and fetches no Slack or Notion context. §2.2 cites the
   pending write at `meeting_brief_service.go:60-62`. D-12 rewritten.
10. **AC-33 not falsifiable.** Rewritten as an observable property: every
    provider value the web code accepts or offers equals the contract's set;
    fails today on `types.ts:13` and the mock probe.
11. **Wrong probe status citation.** AC-10 now says 422 (origin not approved) or
    502 (approved origin unreachable), never 500.
12. **No Linear issue.** Unchanged: still none, nothing posted; the header says
    so and this section is the record.
13. **OWNER: follower disclosure.** Recorded as OQ-8 and answered
    conservatively by D-15: a follower is told where their text and meeting
    context go and confirms once before anything is sent (row 5, AC-41). Pending
    owner confirmation.
14. **OWNER: D-8.** Recorded as OQ-9; D-8 stands (existing default-Ollama
    accounts stay unenrolled, new accounts follow by default, subject to D-15).
    Pending owner confirmation.
15. **OWNER: OQ-1 and OQ-7.** Kept open with their assumptions (D-6 PAC-50
    proof, now explicitly a PAC-50 change; OQ-7 not built). Pending owner
    confirmation.
16. **OWNER: D-4 / OQ-6 and D-13 / OQ-4.** Kept with their assumptions (Vertex
    express mode only; a rejected Bedrock key is a provider failure; no Settings
    warning about short-term keys). Pending owner confirmation.

None of the owner questions blocks Part 1: each assumption either sends
nothing a user has not chosen or can be relaxed later without undoing data
exposure. Part 2 should not reach stage 4 without answers to OQ-1 (and the
PAC-50 amendment) and OQ-8.
