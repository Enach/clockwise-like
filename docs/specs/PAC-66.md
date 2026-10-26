# Spec — PAC-66: A user can point the server's cloud-identity LLM requests at a host of their choice

- **Linear**: https://linear.app/paceday/issue/PAC-66
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**:
  - No `x-uncertain` marker sits on the provider-probe operation or on the
    settings read/write operations (`contracts/openapi/paths/scheduling.yaml`;
    the markers in that file are on schedule-suggest, NLP-confirm and two
    schemas).
  - The provider-probe operation's own description records this defect as
    "NOT resolved here … It needs its own issue"
    (`contracts/openapi/paths/scheduling.yaml:476-488`, citing
    `contracts/features/PAC-24.md` §2). This spec is that issue; the contract
    stage must replace that paragraph rather than leave it standing beside the fix.
  - API-039 (`docs/factory/api-audit.md:273`, settings document, provider enum
    mismatch) is open but **claimed by smart-calendar-flow#101**. It is a
    frontend enum problem and is not resolved here. The contract stage should
    confirm the claim is still live before it touches the provider enum.
  - T8 (`docs/factory/api-audit.md:185`, provider error text echoed to the
    client) is a theme, not a finding on these operations. The probe's 502 still
    echoes the provider's response body (`backend/nlp/parser.go:144-145`). See §5.

## 0. Scope and split

The issue names two channels with very different costs:

1. **Ambient-credential providers** (Azure OpenAI, Bedrock, Vertex). The server
   mints or loads **the deployment's own cloud identity** and sends it to a
   destination built from values the user stores in their own settings. A user
   who can steer that destination gets a bearer token for the deployment's cloud
   account. That is the high-severity part, and this spec covers it.
2. **User-key and keyless providers** (OpenAI, Anthropic, Ollama). The user's
   own stored key goes to a base URL the user chose, which is low value because
   it is their own key. Ollama sends no credential at all, but it lets a user make
   the server POST to any address, internal ones included, and read back
   success or failure plus the response text (server-side request forgery
   with an oracle).

Channel 2 cannot be closed the way the issue proposes (refuse `http://`,
loopback, private and link-local addresses) without a product decision. The
product's own Ollama path is local by design: the server's fallback is
`http://localhost:11434` (`backend/nlp/llm_factory.go:44-46`), the client's
fallback is `http://host.docker.internal:11434` (`backend/nlp/parser.go:102-104`),
and the settings page suggests `http://localhost:11434`
(`smart-calendar-flow/src/pages/Settings.tsx:749`). Refusing those addresses
removes the feature. So:

- **PAC-66 (this spec)**: channel 1, fully, on every code path that sends the
  ambient credential, not just the probe.
- **PAC-66-B (proposed, to be filed)**: channel 2. Its open question is whether a
  self-hosted Ollama on a private address is a supported product feature. That
  question also depends on PAC-56 OQ-1 ("Is 'bring your own AI key' a product
  feature?", `docs/specs/PAC-56.md:329-334`), which is still unanswered.

## 1. Problem

Paceday can use three cloud LLM services that authenticate with **the
deployment's own cloud identity** (a managed identity, an instance role, a
service account) rather than with a key the user types in. That identity
belongs to whoever runs the deployment. It can reach whatever the operator
granted it, which is often far more than one language model.

Each signed-in user can set where those requests go, because the endpoint,
region and location properties live in each user's own settings. Any user can
therefore save a host they control as their "Azure endpoint" or "Vertex
location" and then press "Test connection", type in the natural-language bar, or
regenerate a meeting brief. The server then sends a fresh bearer token for the
operator's cloud account to that host. The probe's 200 or 502 confirms that the
token was delivered.

Who is affected: every operator running Paceday with Azure OpenAI or Vertex (and,
as a defence-in-depth matter, Bedrock; see §2.3). The attacker only needs an
ordinary account. What it costs: theft of a cloud credential, which is worth more
than any LLM API key and may give the attacker access to everything the
deployment's identity can reach.

## 2. Current behaviour

### 2.1 Where the destination comes from

- The provider client is built from the caller's own settings row and nothing
  else: `NewLLMClientFromSettings` (`backend/nlp/llm_factory.go:13-58`).
  - `azure_openai` → `NewAzureOpenAIClient(s.AzureEndpoint, s.AzureDeployment, s.AzureAPIVersion)` (`:18`).
  - `vertex` → `NewVertexClient(s.GCPProject, s.GCPLocation, s.VertexModel)` (`:20`).
  - `bedrock` → `NewBedrockClient(s.AWSRegion, s.AWSProfile, s.BedrockModel)` (`:16`).
- Those properties are ordinary user-writable members of the settings document
  (`backend/storage/settings.go:176-193`), each `type: string` with no format,
  pattern or host constraint in the contract
  (`contracts/openapi/paths/scheduling.yaml:1777-1797`).
- No deployment-level configuration names an LLM endpoint. `.env.example:38-41`
  and `docker-compose.yml:46-48` say all LLM configuration is per-user in the
  database, and "no server-level env vars needed".

### 2.2 How each ambient branch builds its URL and sends the credential

- **Azure OpenAI** (`backend/nlp/llm_azure_openai.go:27-62`). It mints a token
  with `azidentity.NewDefaultAzureCredential` for scope
  `https://cognitiveservices.azure.com/.default` (`:28-34`). The URL is
  `strings.TrimRight(c.Endpoint, "/")` followed by a fixed path (`:39-40`), so the
  stored endpoint **is** the host, used verbatim with any scheme. The token goes
  out as `Authorization: Bearer` (`:50`).
- **Vertex** (`backend/nlp/llm_vertex.go:29-53`). It loads Application Default
  Credentials (`:30`). The URL is
  `fmt.Sprintf("https://%s-aiplatform.googleapis.com/…", c.Location, …)` (`:39-42`).
  The stored location is spliced unescaped into the authority. Verified with Go's
  `net/url.Parse` against that exact format string on 2026-10-26: location
  `attacker.example/x` gives host `attacker.example`, location `attacker.example#`
  gives `attacker.example`, and `attacker.example:443/` gives
  `attacker.example:443`. The token goes out as `Authorization: Bearer` (`:53`).
  **So Vertex is steerable today even though its URL "looks" fixed.**
- **Bedrock** (`backend/nlp/llm_bedrock.go:30-63`). The AWS SDK signs the request
  with the default credential chain, optionally narrowed to a named shared-config
  profile (`:32-38`). The host comes from the SDK's endpoint rules. Those rules
  reject a region that is not a valid host label
  (`aws-sdk-go-v2/service/bedrockruntime@v1.57.1/endpoints.go:222-230`,
  `IsValidHostLabel`), and every host they build sits under an AWS partition
  domain. As far as this read shows, Bedrock's host **cannot** be steered outside
  AWS from the user's settings today. Two things are still true: nothing in
  Paceday enforces that, and the user picks *which* of the deployment's AWS
  profiles signs the request (see §8 OQ-4).

### 2.3 Every path that sends the credential, not only the probe

- Provider probe: `testLLM` (`backend/api/handlers_llm.go:29-91`) loads the
  caller's settings (`:44`), lets the body override only provider, model and key
  (`:23-27`, `:50-62`), and calls `Complete` (`:79`).
- Natural-language parse: `NLPService.Parse` → `buildLLMClient` →
  `NewLLMClientFromSettings` (`backend/nlp/parser.go:173-184`, `:239-241`).
- Meeting-brief regeneration: `backend/api/handlers_meeting_briefs.go:73-77`.

### 2.4 What the settings write does with these properties

- `patchSettings` (`backend/api/handlers_settings.go:72-149`) merges the body
  onto the stored row and validates **only properties the request changes**
  (`validateChangedSettings`, `:191-203`; PAC-24 AC-17). `settingsProblems`
  (`:205-282`) checks the provider *name* (`:256-258`) but no endpoint, region,
  location or profile value. So any string is accepted and stored, and a value
  stored before a new rule exists is never re-checked on write.
- The write operation is `PATCH /api/settings` (`backend/api/routes.go:74-77`).
  The issue's draft acceptance says "PUT settings". `PUT` was removed by PAC-24
  (`docs/specs/PAC-24.md`, Decisions OQ-3), so the criteria below mean the
  settings write, whatever its method.

### 2.5 Transport details that matter for the fix

- All non-SDK providers send through `postJSON` → `http.DefaultClient.Do`
  (`backend/nlp/parser.go:125-148`, the call at `:138`). That client follows
  redirects. The Go standard library re-sends `Authorization` on a redirect to
  the same host or a subdomain of it, and drops it otherwise. So "the first host
  is allowed" does not on its own settle where the credential ends up.
- On failure the probe returns 502 with the provider's status and response body
  in the message (`parser.go:144-145`, `handlers_llm.go:80-82`).

### 2.6 Existing tests

- `TestLLMTest_BodyRulesAndNoKeyEcho` (`backend/api/pac24_scope_test.go:70-124`)
  proves the probe body cannot carry a destination and that no key is echoed. It
  uses the `openai` branch only.
- No test covers the `azure_openai`, `vertex` or `bedrock` branches. No test
  records where an outbound LLM request goes. No e2e test exercises the probe
  (`e2e/tests/settings.spec.ts` covers the key only).

## 3. Desired behaviour

The deployment's cloud identity leaves the server only toward destinations the
**operator** has approved. A user cannot add to that set.

- The operator names the approved inference destinations in deployment
  configuration. Approval is by exact host and covers secure transport only. No
  user, whatever their settings, can widen it.
- If the operator has approved nothing, requests that would carry the deployment
  identity are not sent at all. The user is told, in plain words, that this
  provider is not enabled on this deployment. Fail closed (see Decisions D-1).
- Saving settings that would point an ambient-credential provider at an
  unapproved destination is refused with a clear, per-property error, and nothing
  is stored.
- The rule is enforced again **at the moment of sending**, on every path (probe,
  natural-language parse, meeting briefs). A value stored before this change, or
  written by any other route, still cannot send the credential anywhere
  unapproved.
- If an approved destination redirects, the deployment identity never follows
  the redirect to an unapproved destination.
- A refusal at send time causes no outbound request at all. The user gets a
  client error that says what was refused and why. It is neither a 502 that looks
  like "the provider was unreachable" nor a 500.
- Users of the user-key providers (OpenAI, Anthropic, Ollama) see no change from
  this spec (see §0, PAC-66-B).

## 4. Acceptance criteria

Throughout, "ambient provider" means each of Azure OpenAI, Vertex and Bedrock,
and each criterion applies to each of them. "Approved" means named in the
operator's deployment configuration. "Recording transport" means a test double
for the outbound network that records every attempted request, including its
destination host and headers, and lets no request reach the real network.

AC-1. Given the operator has approved exactly one Azure inference host, and a
user's stored Azure endpoint names a different host, when that user probes their
stored configuration, then the recording transport records **zero** requests and
the response is a 4xx with a JSON error naming the endpoint as not approved.
[unit]
*Fails today: the request is sent to the stored host with a bearer token.*

AC-2. Given the operator has approved only the Vertex host for one region, and a
user's stored Vertex location is `attacker.example/x` (or any value that changes
the URL's authority), when that user probes, then zero requests are recorded and
the response is a 4xx JSON error. [unit]
*Fails today: §2.2 shows the request goes to `attacker.example`.*

AC-3. Given any ambient provider and a stored destination that resolves to an
unapproved host, when the natural-language parse runs for that user, and
separately when that user regenerates a meeting brief, then zero requests are
recorded for that provider on either path. [unit]
*Fails today for Azure and Vertex on both paths.*

AC-4. Given the operator has approved nothing for a given ambient provider, and a
user has that provider selected with any endpoint, region or location, when the
user probes, then zero requests are recorded and the response is a 4xx JSON error
saying the provider is not enabled on this deployment. [unit]
*Fails today: the request is sent.*

AC-5. Given a user who submits a settings write that sets an ambient provider's
destination property (Azure endpoint, Vertex location, Bedrock region) to a value
whose effective host is not approved, when the write is processed, then the
response is a 4xx with a JSON error naming that property, the stored settings are
unchanged (a following read returns the previous values), and the recording
transport records zero requests. [contract] [unit]
*Fails today: the write is accepted with 200 and stored.*

AC-6. Given a user who submits a settings write that sets the same properties to
a value whose effective host is approved, when the write is processed, then it
succeeds and a following probe sends exactly one request, to that host, over
secure transport. [unit]
*Guards against a fix that refuses everything.*

AC-7. Given an approved host for an ambient provider, and a user's stored value
that names that host but with an insecure scheme, when the user probes, then zero
requests are recorded and the response is a 4xx JSON error. A settings write of
that value is refused as in AC-5. [unit]
*Fails today for Azure: the endpoint is used verbatim with any scheme.*

AC-8. Given an approved host that answers the inference request with a redirect
to an unapproved host (including a subdomain of the approved host that is not
itself approved), when a user probes, then no request carrying the deployment
credential is recorded for the unapproved host, and the probe reports failure.
[unit]
*Fails today: `http.DefaultClient` follows the redirect and re-sends
`Authorization` to subdomains.*

AC-9. Given a settings row written **before** this change, holding an Azure
endpoint or Vertex location that points to an unapproved host, when the owner of
that row probes, parses, or regenerates a brief, then zero requests are recorded.
When the same user later saves an unrelated setting (for example their working
hours), that save succeeds. [unit]
*The second half preserves PAC-24 AC-17. The first half fails today.*

AC-10. Given the full compose stack started with an approved Azure host that
points to a local fake provider, and a signed-in user, when the user saves an
unapproved Azure endpoint through the settings write, then the response is a 4xx
JSON error. When the user then saves the approved endpoint and probes, the fake
provider receives exactly one request. When the user probes with the stored row
forced (by direct database write) to an unapproved host, the fake provider and a
second fake listening on that host both receive nothing. [e2e]

AC-11. Given a refusal under AC-1 to AC-9, when the response body is read, then
it contains no token, no key, and no part of the deployment's credential, and its
`error` member is a non-empty human-readable sentence. [unit]

AC-12. Given the operator's approved-destination configuration is present but
malformed (for example an entry that is not a host name), when the server starts,
then it either refuses to start or logs the bad entry and treats it as not
approved. It never treats a malformed entry as "approve everything". [unit]

## 5. Explicitly out of scope

- **OpenAI, Anthropic and Ollama base URLs** (channel 2 in §0). They carry the
  user's own key or no credential. Their request-forgery risk is real and goes to
  PAC-66-B, because the obvious fix removes the local Ollama feature, and that is
  a product decision.
- **Removing the per-user ambient-provider properties entirely** (issue option 2,
  and PAC-56-B's direction). This spec makes them safe to keep. Whether they
  should exist is PAC-56 OQ-1 / OQ-2, which is unanswered. Nothing here stops
  PAC-56-B from later moving them to deployment configuration.
- **The provider's error text echoed in the probe's 502** (T8). Once AC-1 to AC-9
  hold, that echo can only reflect an approved host's response. Narrowing it is a
  separate change to error handling.
- **Token acquisition traffic** (Entra ID, the instance metadata service, Google's
  token endpoint, AWS STS). No user setting influences it (§2.2). The approved
  set governs inference requests only.
- **Encrypting stored settings at rest** (`docs/security.md:87`).
- **Any change to the web app.** The existing settings page already shows a
  server error string. Better guidance in the form is a follow-up once the
  contract is settled.

## 6. What must not change

- The probe body still cannot carry a destination, and no response echoes a
  submitted or stored key. Protected by `TestLLMTest_BodyRulesAndNoKeyEcho`
  (`backend/api/pac24_scope_test.go:70-124`).
- Settings writes still validate only what the request changes (PAC-24 AC-17).
  Protected by the PAC-24 PATCH tests in `backend/api/pac24_scope_test.go`. AC-9
  adds the ambient-provider case.
- OpenAI, Anthropic and Ollama behave exactly as today, including a
  user-supplied OpenAI-compatible base URL and a local Ollama URL. The OpenAI
  branch is protected by `TestLLMTest_BodyRulesAndNoKeyEcho`. **No test protects
  the Ollama or Anthropic branches**, which is a finding. The plan should add one
  for each so that this change is shown not to touch them.
- The key-handling e2e journey in `e2e/tests/settings.spec.ts:40-60` still passes.
- A deployment whose operator approves its Azure, Vertex or Bedrock host keeps
  working with no change to any user's stored settings (AC-6).

## 7. Rollback

No migration and no stored value is rewritten or deleted. Rows that hold an
unapproved destination are left exactly as they are. They are refused at send
time, not cleaned up (Decisions D-3). Reverting the change restores today's
behaviour, along with today's vulnerability. The new operator configuration
becomes unused and can stay in place.

**Upgrade note (not rollback, but the operational risk):** with fail-closed (D-1),
a deployment that uses Azure, Vertex or Bedrock today stops making LLM calls on
upgrade until its operator approves a destination. The release notes and
`.env.example` must say so. Rolling back the change is the emergency workaround,
and it reopens the vulnerability.

## 8. Open questions

- **OQ-1 — Fail closed when nothing is approved?** Assumed yes (D-1), which breaks
  existing ambient-provider deployments until configured. The alternative is a
  built-in default set of vendor host suffixes. It is rejected for now: the
  suffixes anyone can register under (`*.openai.azure.com` is any Azure
  customer's resource; `*.amazonaws.com` includes S3 bucket and API Gateway
  hosts, which log request headers) would let an attacker receive the token on a
  vendor domain. Owner: product owner / operator. Resolve: confirm D-1 on PAC-66
  before stage 2.
- **OQ-2 — Exact hosts only, or operator-written wildcards?** Approval by exact
  host is assumed (D-2). An operator with many Azure resources may want a pattern.
  If wildcards are allowed, the contract must define their semantics. Owner:
  contract-author, with the operator. Resolve at stage 2.
- **OQ-3 — Should the approved set also constrain which ambient providers a user
  may select?** For example, if only Azure is approved, should choosing Vertex be
  refused at write time, or only at send time? AC-4 requires send time. Write
  time is a UX choice. Owner: contract-author.
- **OQ-4 — The Bedrock profile.** A user can name any profile in the deployment's
  AWS shared config (`backend/nlp/llm_bedrock.go:35-37`). That selects *which*
  deployment identity signs the request, possibly a more privileged one, even
  though the host stays inside AWS. Host steering is this spec's problem; identity
  selection is a sibling problem. Owner: product owner. Resolve: fold into
  PAC-66-B or file separately. Assumed not changed here.
- **OQ-5 — Should PAC-56-B supersede this?** If the owner answers PAC-56 OQ-1 "no,
  the deployment provides the model", the per-user ambient properties disappear,
  and AC-5/AC-7 become "the property does not exist". AC-1 to AC-4 and AC-8 stay
  valid either way. Owner: product owner.
- **OQ-6 — Is the Bedrock analysis in §2.2 complete?** It rests on the SDK's
  host-label check and on shared-config `endpoint_url` being operator-controlled.
  The plan stage should confirm this with a recording-transport test on the
  Bedrock branch (AC-1/AC-4/AC-5 cover it) rather than trust this reading. Owner:
  plan-author.

## Decisions

*None of these has a recorded owner answer. Each is the most conservative option
consistent with PRODUCT.md ("Privacy is not a setting to trade away") and the
PAC-24 decision that endpoints, regions and profiles "identify or bill the owner"
(`docs/specs/PAC-24.md`, Decisions OQ-1/OQ-4).*

- **D-1 — Fail closed.** With no approved destination, no request carrying the
  deployment identity is sent. *Assumed by the factory, pending owner
  confirmation.*
- **D-2 — Exact-host approval over secure transport only.** No built-in vendor
  suffixes. *Assumed by the factory, pending owner confirmation.*
- **D-3 — Refuse, never rewrite.** Stored rows with an unapproved destination are
  neither cleared nor migrated (no user data deleted). They are refused at send
  time, and the user can correct them. *Assumed by the factory, pending owner
  confirmation.*
- **D-4 — Split.** Channel 2 (OpenAI, Anthropic, Ollama base URLs) moves to
  PAC-66-B. *Assumed by the factory, pending owner confirmation.*

## Challenge — 2026-10-26

**Verdict**: accept-with-changes

Reviewed on `factory/pac-66-spec` at 5b7a7cc (committed state only), against
the Linear issue text, the code on that commit, both repos' consumers, the
contract fragments and `docs/factory/api-audit.md`. The core analysis holds: the
three ambient branches, the three send paths (probe, parse, brief refresh; a grep
for `NewLLMClientFromSettings` and `.Complete(` finds no fourth), the Vertex
authority injection (re-run with Go 1.25.5 against the exact format string:
`attacker.example/x`, `attacker.example#` and `attacker.example:443/` all yield
host `attacker.example[:443]`; `us-central1` yields
`us-central1-aiplatform.googleapis.com`), the Bedrock host-label check, and the
PATCH changed-only validation. The findings below are about what the criteria
leave undecided, not about the diagnosis.

### Blocking

1. **§3 promises a 4xx on every send path; the criteria and the contract say
   otherwise for parse and brief refresh, and no criterion decides it.** §3:
   "A refusal at send time ... The user gets a client error ... neither a 502 ...
   nor a 500." AC-1, AC-2, AC-4 and AC-7 pin a 4xx for the probe only. AC-3 and
   AC-9 pin "zero requests" for parse and brief refresh and say nothing about the
   response. Today those two operations report LLM-side failure very differently,
   and the contract documents it: `POST /api/nlp/parse` returns **200**
   `{"intent":"unknown","error":"..."}` when client construction fails
   (`backend/nlp/parser.go:179-182`, `contracts/openapi/paths/scheduling.yaml:332-337`)
   and a **500 text/plain** when `Complete` fails (`parser.go:197-200`,
   `backend/api/handlers_nlp.go:28-31`); `POST /api/meetings/{event_id}/brief/refresh`
   returns **200** with `status: "failed"` and `brief_text` "LLM not configured"
   or "LLM error: ..." (`backend/engine/meeting_brief_service.go:73-86`,
   `backend/api/handlers_meeting_briefs.go:73-77`,
   `contracts/openapi/paths/habits.yaml:481-483`). An implementer can satisfy every
   AC while the parse refusal is a 500, which §3 forbids, or can turn it into a
   4xx and break consumers §5 says are untouched (the web natural-language bar,
   the MCP tool `clockwise_parse_command` at `mcp/tools.go:195-203`, the brief
   panel). *Resolve*: add a criterion per path stating the observable outcome of a
   send-time refusal (status, body shape, and for briefs what is stored), and
   either narrow §3's sentence to the probe or accept that the parse/refresh
   responses change and list those operations under Inherited scope (see
   Non-blocking 5).

2. **Clearing a destination property is not guaranteed to succeed, although D-3
   relies on the user correcting their row.** AC-5 refuses a write whose value's
   "effective host is not approved". It does not say what the empty string's
   effective host is, and the empty string is not inert: an empty Vertex location
   is replaced by `us-central1` at build time (`backend/nlp/llm_vertex.go:20-22`),
   an empty Bedrock region falls back to the deployment's own AWS configuration,
   and an empty Azure endpoint yields a relative URL. Read literally, AC-5 permits
   refusing `azureEndpoint: ""` on a deployment that approved nothing for Azure,
   which locks a user out of the one correction D-3 promises them. The web app
   sends exactly this kind of write (`smart-calendar-flow/src/api/client.ts:699-710`,
   diff-only PATCH). *Resolve*: a criterion that setting each destination property
   (Azure endpoint, Vertex location, Bedrock region) to `""` is accepted on write
   regardless of the approved set, and a statement of what effective host an empty
   value has at send time (in particular whether Vertex's `us-central1` default
   must itself be approved).

3. **The zero-request criteria pass today in any environment without a deployment
   identity, and AC-10 cannot pass at all as written.** Every ambient branch
   acquires its credential *before* building the URL
   (`llm_azure_openai.go:28-37`, `llm_vertex.go:30-37`, `llm_bedrock.go:38-41`).
   In a unit test or the compose stack with no Azure, GCP or AWS identity, today's
   code fails at token acquisition and sends **no** inference request. So AC-1,
   AC-2, AC-3, AC-4, AC-7, AC-9 and AC-10's last sentence pass against the
   vulnerable code, and their "fails today" notes are only true when a credential
   is available. Conversely AC-10's "the fake provider receives exactly one
   request" and AC-6's "sends exactly one request" fail for every implementation
   unless the environment yields a token and the fake is reachable over transport
   the server trusts (AC-7 and D-2 forbid insecure). Separately, "the recording
   transport records **every** attempted request" contradicts §5's carve-out of
   token acquisition: if token traffic is recorded, an implementation that
   acquires a token before refusing fails AC-1 although §5 says that traffic is out
   of scope. *Resolve*: add to the shared Givens that a deployment credential is
   obtainable (a token source yielding a known sentinel token) so the ACs fail
   today; state whether "zero requests" counts token-acquisition traffic (or
   require, as behaviour, that refusal happens before acquisition); and state for
   AC-10 what the e2e stack must provide (a test credential and a fake provider
   over trusted secure transport), or move that half of AC-10 to the unit level.

### Non-blocking

1. **Switching provider is unspecified at write time.** Under PAC-24 AC-17 the
   write validates only properties whose values change
   (`backend/api/handlers_settings.go:128-136`, `:191-203`). A PATCH of only
   `{"llmProvider":"azure_openai"}` onto a row already holding an unapproved
   `azureEndpoint` changes no destination property. The spec does not say whether
   that write is refused or accepted (send-time refusal makes either safe). OQ-3
   is near this but asks about the approved set, not a stored unapproved value.
   State the outcome so the contract and the web form agree. Likewise a write of
   `azureEndpoint` while another provider is selected: AC-5 implies it is checked
   anyway; say so explicitly.
2. **AC-8 misses the scheme downgrade.** Go's redirect rule ignores the scheme:
   `shouldCopyHeaderOnRedirect` compares host names only
   (`net/http/client.go:992-1009`, Go 1.25.5), so an approved host answering an
   `https://approved/...` request with a redirect to `http://approved/...` gets
   the bearer token re-sent in cleartext. §3 says approval "covers secure transport
   only", but AC-8 only tests an unapproved *host*. Add the same-host
   insecure-scheme case.
3. **Bedrock's "effective host" is not defined by the region alone.** The SDK
   host also depends on the selected shared-config profile (user-chosen, OQ-4),
   that profile's `endpoint_url`, `AWS_ENDPOINT_URL*` in the environment, and
   FIPS/dual-stack variants. AC-5 and AC-6 for Bedrock need a definition of the
   value being approved (for example the host the request is actually sent to), or
   the write-time half for Bedrock should be stated as region validity only.
4. **PAC-66-B is not filed.** The issue's third draft acceptance
   (`ollamaBaseUrl`/`llmBaseUrl` reject `http://`, loopback, RFC1918, link-local)
   and half of its title leave with it. The split is sound (the Ollama defaults
   cited in §0 check out), but the spec should require PAC-66-B to exist and be
   linked before PAC-66 is closed, otherwise the issue's problem is half-solved
   silently.
5. **Inherited scope omits the other two touched operations.** If Blocking 1
   changes their responses, `POST /api/nlp/parse` carries open finding API-080
   (`docs/factory/api-audit.md:314`, zero-time `range_start`/`range_end`), which
   factory §7.1 "touch it, fix it" would pull in;
   `POST /api/meetings/{event_id}/brief/refresh` has no open marker (the
   `habits.yaml:463` `x-uncertain` is on `getMeetingBrief`). Name both operations
   and their status either way.
6. **AC-12 is weakly falsifiable** ("either refuses to start or logs ... and
   treats it as not approved"). Both are safe, but the contract stage should pick
   one so a single test can check it.

### Criteria I could not falsify

- AC-1, AC-2, AC-3, AC-4, AC-7, AC-9 (first half): as written they pass against
  today's code whenever no deployment credential is obtainable in the test
  environment (Blocking 3).
- AC-10: the "exactly one request" half fails for every implementation in a stack
  with no cloud identity; the "both fakes receive nothing" half passes for every
  implementation, today's included (Blocking 3).

### Citations I checked and found wrong

- §6 "**No test protects the Ollama or Anthropic branches**" — actually
  `backend/nlp/parser_test.go` has `TestBuildLLMClient_Ollama` (:90),
  `TestBuildLLMClient_Anthropic` (:118), `TestAnthropicClient_Complete` (:183),
  `TestOllamaClient_Complete_Mock` (:244) and `TestNLPService_Parse_WithOllama`
  (:282). What is true is narrower: no *probe-level* test covers those branches.
- §2.6 "No test records where an outbound LLM request goes" — overstated: the
  `httptest` servers in `parser_test.go:162-262` receive the user-key branches'
  requests. It is true for the three ambient branches (no test file mentions
  azure, vertex or bedrock).
- All other citations checked and correct: `llm_factory.go:13-58,16,18,20,44-46`,
  `llm_azure_openai.go:28-34,39-40,50`, `llm_vertex.go:30,39-42,53`,
  `llm_bedrock.go:32-38`, `bedrockruntime@v1.57.1/endpoints.go:222-230`,
  `parser.go:102-104,125-148,138,144-145,173-184,239-241`,
  `handlers_llm.go:23-27,44,50-62,79,80-82`, `handlers_meeting_briefs.go:73-77`,
  `handlers_settings.go:72-149,191-203,205-282,256-258`, `routes.go:74-77`,
  `storage/settings.go:176-193`, `scheduling.yaml:476-488,1777-1797`,
  `.env.example:38-41`, `docker-compose.yml:46-48`,
  `smart-calendar-flow/src/pages/Settings.tsx:749`, `api-audit.md:185,273`,
  `PAC-56.md:329-334`, PAC-24 Decisions (OQ-1, OQ-3, OQ-4),
  `pac24_scope_test.go:70-124`, `e2e/tests/settings.spec.ts:40-60`.

### Consumers this breaks

- None if the spec stays as scoped and parse/refresh keep their response shapes.
  If Blocking 1 is resolved by changing them: the `smart-calendar-flow`
  natural-language bar and brief panel (they read the 200 `error` and
  `status: "failed"` shapes) and `clockwise-like/mcp/tools.go:195-203`
  (`clockwise_parse_command` relays the parse response).
- The web settings save is diff-only (`smart-calendar-flow/src/api/client.ts:699-710`),
  so a stored unapproved value does not block unrelated saves; AC-9's second half
  holds for the web client.
