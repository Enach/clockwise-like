# Spec — PAC-66: A user can point the server's cloud-identity LLM requests at a host of their choice

- **Linear**: https://linear.app/paceday/issue/PAC-66
- **Status**: challenged (round 1 answered in "Revision 1", round 2 in "Revision 2")
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
  - *(Revision 1)* The two other operations whose behaviour this spec changes:
    - Natural-language parse (`POST /api/nlp/parse`). Its response **shape** does
      not change (AC-13 reuses the documented 200 `intent: unknown` + `error`
      shape, `contracts/openapi/paths/scheduling.yaml:332-337`). It carries open
      finding **API-080** (`docs/factory/api-audit.md:314`, zero-time
      `range_start`/`range_end`). If the contract stage edits this operation at
      all (for example to correct its description, which says LLM failures never
      give a 5xx while `backend/nlp/parser.go:197-200` gives a 500), factory §7.1
      pulls API-080 into scope. No `x-uncertain` marker sits on it.
    - Meeting-brief refresh (`POST /api/meetings/{event_id}/brief/refresh`). Its
      response shape does not change (AC-14 reuses the documented 200
      `status: "failed"`, `contracts/openapi/paths/habits.yaml:487-488`). No open
      audit finding and no `x-uncertain` marker sits on it (the `habits.yaml:463`
      marker is on `getMeetingBrief`).

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
  AWS from the user's settings today. *(Revision 2)* An empty stored region is
  replaced by a hard-coded `us-east-1` (`llm_bedrock.go:21-23`) and passed as an
  explicit `config.WithRegion` (`:31-33`). The SDK reads load options before the
  environment and shared config (`aws-sdk-go-v2/config@v1.32.35/provider.go:113-123`,
  `config.go:212`), so `AWS_REGION` and the shared-config region are never used
  for an inference request. Two things are still true: nothing in
  Paceday enforces that, and the user picks *which* of the deployment's AWS
  profiles signs the request (see §8 OQ-4).

### 2.3 Every path that sends the credential, not only the probe

- Provider probe: `testLLM` (`backend/api/handlers_llm.go:29-91`) loads the
  caller's settings (`:44`), lets the body override only provider, model and key
  (`:23-27`, `:50-62`), and calls `Complete` (`:79`). *(Revision 2)* The provider
  override is how the web app always probes: "Test connection" sends
  `llmProvider` from the unsaved form (`smart-calendar-flow/src/pages/Settings.tsx:173`,
  `src/api/client.ts:712-716`). So the provider a probe uses can differ from the
  saved one, while the destination properties always come from the saved row.
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
- No test covers the `azure_openai`, `vertex` or `bedrock` branches, and no test
  records where an outbound request from those three branches goes. (The
  user-key branches' requests do reach `httptest` servers in
  `backend/nlp/parser_test.go:162-262`.) No e2e test exercises the probe
  (`e2e/tests/settings.spec.ts` covers the key only).

## 3. Desired behaviour

The deployment's cloud identity leaves the server only toward destinations the
**operator** has approved. A user cannot add to that set.

- The operator names the approved inference destinations in deployment
  configuration, **separately for each ambient provider**. Each provider's
  credential goes only to hosts approved for that provider: a host approved for
  Vertex is not approved for Azure. Approval is by exact host and covers secure
  transport only. No user, whatever their settings, can widen it. *(Revision 2)*
- The rule follows the provider a request will actually use, not the provider
  the user has saved. A probe that names an ambient provider for one try only is
  checked exactly as if that provider were saved. *(Revision 2)*
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
- A refusal at send time causes no outbound request at all, on every path.
- How the refusal is reported depends on the path, so that no existing consumer
  breaks (Decisions D-5):
  - **The provider probe** answers with a client error that says what was refused
    and why. It is neither a 502 that looks like "the provider was unreachable"
    nor a 500.
  - **Natural-language parse** answers the way it already answers when the LLM
    cannot be used: a success response whose intent is "unknown" and whose error
    text says the provider's destination is not approved on this deployment. It
    never answers with a server error.
  - **Meeting-brief refresh** answers the way it already answers when the LLM
    cannot be used: a success response with a brief marked failed, whose text
    says why. That failed brief is what is stored.
- Clearing a destination (saving it empty) is always accepted, whatever the
  operator has approved, so a user can always undo a bad value (D-3). An empty
  value is not a free pass at send time: it stands for whatever default the
  provider would use, and that default must itself be approved (see "Effective
  host" in §4).
- Users of the user-key providers (OpenAI, Anthropic, Ollama) see no change from
  this spec (see §0, PAC-66-B).

## 4. Acceptance criteria

Throughout, "ambient provider" means each of Azure OpenAI, Vertex and Bedrock,
and each criterion applies to each of them. "Approved" means named in the
operator's deployment configuration.

*Shared Givens and definitions (Revision 1):*

- **Sentinel credential.** Unless a criterion says otherwise, every `[unit]`
  criterion has the Given "a deployment credential is obtainable". For Azure and
  Vertex this is a token source that yields a known sentinel bearer token; for
  Bedrock, sentinel signing keys. Without it, today's code fails at credential
  acquisition and sends nothing (`llm_azure_openai.go:28-37`,
  `llm_vertex.go:30-37`, `llm_bedrock.go:38-41`), which would make the
  zero-request criteria pass against the vulnerable code. With it, each
  "fails today" note below is true.
- **Recording transport.** A test double for the outbound network that records
  every attempted request **other than credential acquisition**, including its
  destination host, scheme and headers, and lets no request reach the real
  network. Credential acquisition (Entra ID, instance metadata, Google's token
  endpoint, AWS STS or shared config) is served by the sentinel source and is
  **not counted**: §5 keeps that traffic out of scope, and an implementation may
  acquire the credential before or after it decides to refuse.
- **Refusal does not depend on the credential.** *(Revision 2)* A destination
  refusal is reported with the outcome its criterion states even when acquiring
  the credential would fail. The compose stack in AC-10 has no cloud identity, so
  an implementation that acquires first and reports the acquisition failure fails
  AC-10. Whether acquisition is attempted before the refusal is still free; only
  the reported outcome is pinned.
- **Successful answer.** *(Revision 2)* In every criterion that counts "exactly
  one request" (AC-6, AC-16, AC-19), the recording transport answers the
  inference request with a well-formed success, so SDK retries (the AWS SDK
  retries failed requests by default) cannot inflate the count.
- **Zero requests** means the recording transport records no request at all. It
  also implies that the sentinel credential appears in no recorded request to an
  unapproved host and in no response body.
- **Effective host** of a destination value is the host (and scheme) of the
  inference request that would actually be sent, after every default is applied:
  - Azure: the host of the stored endpoint. An empty endpoint has **no** effective
    host. At send time it is refused (no request), and the error says the Azure
    endpoint is not set, not that it is "not approved" (AC-20). *(Revision 2:
    one message chosen.)*
  - Vertex: the host built from the location. An empty location means the
    built-in default region (`us-central1` today, `llm_vertex.go:20-22`), so its
    effective host is that region's host, which is sent to only if approved.
  - Bedrock: the host the request would actually be sent to once the AWS
    configuration is resolved (region, the stored profile, and any
    operator-controlled endpoint override). An empty region means the built-in
    default region (`us-east-1` today, `llm_bedrock.go:21-23`). It is **not** the
    deployment's configured region: the default is applied as an explicit region
    that overrides `AWS_REGION` and the shared-config region (§2.2). Its effective
    host is that region's resolved host, which is sent to only if approved
    (AC-19). This spec does not change which region an empty value means (D-7).
    At **write** time the Bedrock region is checked only for validity as a region
    name (a single host label); the host itself is enforced at send time (AC-1,
    AC-4). *(Revision 2: corrected; Revision 1 said "the deployment's own
    configured region", which the code does not do.)*

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

AC-3. Given any ambient provider and a stored destination whose effective host
is not approved, when the natural-language parse runs for that user, and
separately when that user regenerates a meeting brief (the user has no workspace
connections, so no other outbound traffic is expected), then zero requests are
recorded on either path. The response on each path is as AC-13 and AC-14 state.
[unit]
*Fails today for Azure and Vertex on both paths: with the sentinel credential the
request is sent.*

AC-4. Given the operator has approved nothing for a given ambient provider, and a
user has that provider selected with any endpoint, region or location, when the
user probes, then zero requests are recorded and the response is a 4xx JSON error
saying the provider is not enabled on this deployment. [unit]
*Fails today: the request is sent.*

AC-5. Given a user who submits a settings write that sets the Azure endpoint or
the Vertex location to a **non-empty** value whose effective host is not
approved, or sets the Bedrock region to a non-empty value that is not a valid
region name, when the write is processed, then the
response is a 4xx with a JSON error naming that property, the stored settings are
unchanged (a following read returns the previous values), and the recording
transport records zero requests. The check applies whichever provider is
currently selected. [contract] [unit]
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
itself approved), or to the same approved host over an insecure scheme, when a
user probes, then no request carrying the deployment credential is recorded for
the redirect target, and the probe reports failure. [unit]
*Fails today: `http.DefaultClient` follows the redirect and re-sends
`Authorization` to subdomains and to the same host over `http://`.*

AC-9. Given a settings row written **before** this change, holding an Azure
endpoint or Vertex location that points to an unapproved host, when the owner of
that row probes, parses, or regenerates a brief, then zero requests are recorded
and each path responds as AC-1, AC-13 and AC-14 state. When the same user later
saves an unrelated setting (for example their working hours), that save
succeeds. [unit]
*The second half preserves PAC-24 AC-17. The first half fails today.*

AC-10. Given the full compose stack started with one approved Azure host in the
operator configuration (nothing needs to listen on it; the stack provides **no**
cloud identity and no TLS fake), and a signed-in user with Azure selected, when
the user saves an unapproved Azure endpoint through the settings write, then the
response is a 4xx JSON error naming the endpoint and a following settings read
shows the previous value. When the stored endpoint is then forced (by direct
database write) to an unapproved host and the user probes, the response is a 4xx
JSON error saying the endpoint is not approved. [e2e]
*Fails today: the write is accepted with 200, and the probe answers 502 with the
credential-acquisition error, because today's code tries to send.* That exactly
one request reaches an approved host is proven at unit level by AC-6, where the
sentinel credential and the recording transport stand in for a cloud identity
and a trusted TLS fake. The compose stack cannot provide either without a real
cloud account. *(Revision 2)* The probe half relies on the shared Given
"Refusal does not depend on the credential".

AC-11. Given a refusal under AC-1 to AC-9, AC-13, AC-14 or AC-16 to AC-20, when the response
body (and, for AC-14, the stored brief) is read, then
it contains no token, no key, and no part of the deployment's credential, and its
error text (the `error` member, or the brief text for AC-14) is a non-empty
human-readable sentence. [unit]

AC-12. Given the operator's approved-destination configuration is present but
malformed (for example an entry that is not a host name), when the server starts,
then it starts, logs the bad entry and treats it as not approved, and well-formed
entries beside it stay approved. It never treats a malformed entry as "approve
everything". [unit] *(Revision 1: one outcome chosen, D-6.)*

AC-13. Given any ambient provider and a stored destination whose effective host
is not approved (or nothing approved for that provider), when the user's
natural-language parse runs, then zero requests are recorded and the response is
HTTP 200 with a JSON body whose intent is "unknown" and whose error text is a
non-empty sentence saying the provider's destination is not approved (or the
provider is not enabled) on this deployment. It is never a 4xx or a 5xx, and
never `text/plain`. [contract] [unit]
*Fails today: with the sentinel credential the request is sent, and when sending
fails the response is a 500 `text/plain` (`backend/nlp/parser.go:197-200`,
`backend/api/handlers_nlp.go:28-31`).*

AC-14. Given the same state as AC-13, when the user regenerates a meeting brief,
then zero requests are recorded, the response is HTTP 200 with the brief's status
"failed" and a brief text that is a non-empty sentence saying why, and a
following read of that brief returns the same status and text. [contract] [unit]
*Fails today: with the sentinel credential the request is sent.*

AC-15. Given any approved set (including nothing approved), and a user whose
stored Azure endpoint, Vertex location or Bedrock region holds a value that is
not approved, when the user submits a settings write that sets that property to
the empty string, then the write succeeds and a following read shows the empty
value. [contract] [unit]
*Guards D-3: a user can always clear their own bad value. Fails for any fix that
treats "" as an unapproved host at write time.*

AC-16. Given Vertex is approved only for a region other than the default region,
and a user's stored Vertex location is empty, when the user probes, then zero
requests are recorded and the response is a 4xx JSON error as in AC-1. Given
instead that the default region's host is approved, the same probe sends exactly
one request, to that host, over secure transport. [unit]
*Pins the effective host of an empty location. The first half fails today.*

AC-17. Given a user's row already holds an unapproved Azure endpoint while
another provider is selected, when the user submits a settings write that
changes only the provider to Azure, then the write succeeds (it changes no
destination property; PAC-24 AC-17) and the next probe is refused as in AC-1.
[unit] *(Revision 1, challenge non-blocking 1: this case is refused at send time
only.)*

AC-18. *(Revision 2)* Given, for each ambient provider P, a user whose saved
provider is not P (a user-key provider such as OpenAI, and as a separate case a
different ambient provider), when the user probes with a request that names P as
the provider for that probe only (as the settings page's "Test connection" does),
then:
(a) if the user's stored destination for P has an effective host that is not
approved for P, zero requests are recorded and the response is a 4xx JSON error
naming the destination as not approved, as in AC-1;
(b) if the operator has approved nothing for P, zero requests are recorded and the
response is a 4xx JSON error saying P is not enabled on this deployment, as in
AC-4;
(c) if the stored destination's effective host is approved only for a different
ambient provider, the outcome is as in (a).
In every case the saved provider is unchanged afterwards. [unit]
*Fails today: (a) and (b) send the request with the sentinel credential to the
stored host. Catches an implementation that keys its check on the saved provider.*

AC-19. *(Revision 2)* Given Bedrock is approved only for the resolved host of a
region other than the built-in default region, the deployment's own AWS region
configuration names that approved region, and a user's stored Bedrock region is
empty, when the user probes, then zero requests are recorded and the response is a
4xx JSON error as in AC-1. Given instead that the default region's resolved host is
approved, the same probe sends exactly one request, to that host, over secure
transport. [unit]
*Pins the effective host of an empty Bedrock region. The first half fails today:
the request goes to the default region's host.*

AC-20. *(Revision 2)* Given at least one approved Azure host, and a user with Azure
selected (saved, or named in the probe request) whose stored Azure endpoint is
empty, when the user probes, then zero requests are recorded and the response is a
4xx JSON error saying the Azure endpoint is not set. [unit]
*Fails today: the probe answers 502, because the request cannot be completed
against a URL with no host.*

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
  set governs inference requests only, and the criteria do not count this
  traffic (see "Recording transport" in §4).
- **New response shapes for parse and brief refresh.** A send-time refusal reuses
  each operation's existing "LLM unusable" shape (AC-13, AC-14), so the web
  natural-language bar and the MCP tool `clockwise_parse_command` (the two parse
  consumers) need no change. No web code calls brief refresh today.
  *(Revision 2: "the brief panel" removed; it does not exist.)*
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
  branch is protected by `TestLLMTest_BodyRulesAndNoKeyEcho`. The Ollama and
  Anthropic client branches are protected by `backend/nlp/parser_test.go`
  (`TestBuildLLMClient_Ollama` :90, `TestBuildLLMClient_Anthropic` :118,
  `TestAnthropicClient_Complete` :183, `TestOllamaClient_Complete_Mock` :244,
  `TestNLPService_Parse_WithOllama` :282). *(Revision 1: the earlier claim that no
  test protects them was wrong.)* No **probe-level** test covers the Ollama or
  Anthropic branches. The plan should add one for each.
- The parse and brief-refresh response shapes. Parse is read by the web
  natural-language bar (`smart-calendar-flow/src/api/client.ts:1155`) and by
  `mcp/tools.go:195-203` (`clockwise_parse_command`). Brief refresh has no
  consumer in the web app today *(Revision 2)*. AC-13 and AC-14 pin both shapes
  for the refusal case.
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
- **OQ-7 — Should an empty Bedrock region mean the deployment's region?**
  *(Revision 2)* Today it means `us-east-1`, whatever the deployment configures
  (§2.2). This spec keeps that (D-7). An operator who approves only their own
  region's host will see users with an empty region refused until they set a
  region. Changing the meaning is a behaviour change. Owner: product owner.

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
  PAC-66-B. PAC-66 is not closed until PAC-66-B exists in Linear and is linked
  from this spec. *Assumed by the factory, pending owner confirmation.*
- **D-5 — Each path reports a refusal in its existing "LLM unusable" shape.** The
  probe gives a 4xx JSON error; parse gives 200 with intent "unknown" and an
  error sentence; brief refresh gives 200 with a failed brief. This option
  changes no consumer (§5 promises no web change; the MCP tool relays parse
  verbatim). *Assumed by the factory, pending owner confirmation.*
- **D-6 — A malformed approved-destination entry is logged and ignored, not
  fatal.** The server still starts, so the rest of the product stays available;
  the entry approves nothing (fail closed, as D-1). *Assumed by the factory,
  pending owner confirmation.*
- **D-7 — An empty Bedrock region keeps meaning the built-in default region.**
  *(Revision 2)* No change to which region is used; the default region's host is
  sent to only if approved (AC-19). Refusing is the conservative outcome when it is
  not approved. *Assumed by the factory, pending owner confirmation.*
- **D-8 — Approval is per provider.** *(Revision 2)* Each ambient provider has its
  own approved set, and a provider's credential is never sent to a host approved
  only for another provider. *Assumed by the factory, pending owner confirmation.*

*Decisions taken at stage 2 (contract revision 1, `contracts/features/PAC-66.md`).
They settle questions this spec left to the contract stage (OQ-2, OQ-3, and the
round-3 challenge's non-blocking items). None changes a criterion above.*

- **D-9 — Exact-host matching rule (OQ-2).** A destination matches only with
  scheme `https`, port absent or 443, no userinfo, and a host equal (ASCII
  case-insensitive) to an entry for that provider. A trailing dot is not
  stripped. No wildcards, suffixes or subdomains. *Assumed by the factory,
  pending owner confirmation.*
- **D-10 — Operator entries (AC-12, round-3 NB-5).** Three variables,
  `PACEDAY_LLM_APPROVED_HOSTS_{AZURE_OPENAI,VERTEX,BEDROCK}`, comma-separated. An
  entry is a bare host name, or `https://<host>` with an optional trailing `/`
  (reduced to its host). Anything else is malformed and is logged and ignored
  (D-6). *Assumed by the factory, pending owner confirmation.*
- **D-11 — No write-time check on provider selection (OQ-3).** Selecting an
  ambient provider with nothing approved is accepted on write and refused at
  send time (AC-4, AC-17). *Assumed by the factory, pending owner confirmation.*
- **D-12 — AC-5 applies only when the value changes (round-3 NB-3).** A write
  that sends back an unapproved value already stored is accepted under PAC-24
  AC-17, and the value is refused at send time (AC-9). *Assumed by the factory,
  pending owner confirmation.*
- **D-13 — Host approval does not constrain the path (round-3 NB-1).** As AC-6
  says, a value whose effective host is approved is accepted whatever its path.
  Path and project steering on an approved host is recorded as a follow-up
  (contract §7) with the product owner as owner. *Assumed by the factory,
  pending owner confirmation.*

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

## Revision 1 — 2026-10-26

Answers to the round-1 challenge. The spec sections above are changed in place;
changes that are not obvious from the text are marked *(Revision 1)*.

### Blocking

1. **Refusal response on parse and brief refresh.** Accepted. §3 no longer
   promises a client error on every path; it now gives the outcome per path. The
   probe keeps the 4xx (AC-1, AC-2, AC-4, AC-7). New AC-13 pins parse: HTTP 200,
   intent "unknown", a non-empty error sentence, never a 4xx, a 5xx or
   `text/plain`. New AC-14 pins brief refresh: HTTP 200, status "failed", a
   non-empty brief text, and the same failed brief stored. AC-3 and AC-9 now
   point at them, and AC-11 covers them. This is the option that keeps every
   consumer working (web natural-language bar, brief panel, MCP
   `clockwise_parse_command`), recorded as D-5. Because both operations'
   behaviour changes, Inherited scope now lists them: parse carries API-080
   (pulled in if the contract stage edits that operation); brief refresh has no
   open finding or marker. §5 and §6 state that the shapes do not change.
2. **Clearing a destination.** Accepted. New AC-15: writing `""` to the Azure
   endpoint, the Vertex location or the Bedrock region is accepted whatever the
   approved set. AC-5 now applies only to non-empty values. §4 defines the
   effective host of an empty value at send time: Azure has none and is refused;
   Vertex means the default region's host, which must itself be approved (new
   AC-16); Bedrock means the deployment's own configured region, enforced on the
   resolved host at send time.
3. **Falsifiability without a deployment identity.** Accepted. §4 now has shared
   Givens. Every unit criterion runs with a sentinel credential (a sentinel
   bearer token for Azure and Vertex, sentinel signing keys for Bedrock), so
   today's code sends the request and every "fails today" note holds. The
   recording transport records every request **except credential acquisition**,
   which the sentinel source serves and which is not counted, matching §5. An
   implementation may acquire the credential before refusing. AC-10 is narrowed
   to what the compose stack can prove with no cloud identity and no TLS fake:
   the write refusal, and a probe of a forced unapproved row answering 4xx
   instead of today's 502. Both fail today. "Exactly one request reaches the
   approved host" stays at unit level in AC-6 (and AC-16), and AC-10 says so.

### Non-blocking, also taken

- NB-1: new AC-17 (switching the provider onto a stored unapproved endpoint is
  accepted at write and refused at send). AC-5 says the write check applies
  whichever provider is selected.
- NB-2: AC-8 now includes a redirect to the same approved host over an insecure
  scheme.
- NB-3: Bedrock's effective host is defined as the resolved send host. The write
  check for Bedrock is region-name validity only (AC-5).
- NB-4: D-4 now requires PAC-66-B to exist and be linked before PAC-66 closes.
- NB-5: Inherited scope names both operations and their status.
- NB-6: AC-12 now has one outcome (start, log, ignore the entry), recorded as D-6.
- Citations found wrong: §2.6 and §6 corrected.

## Challenge — 2026-10-26 (round 2)

**Verdict**: accept-with-changes

Reviewed on `factory/pac-66-spec` at a23e836 (committed state only), against the
code on that commit, `smart-calendar-flow` at 8bf231f, the contract fragments and
`docs/factory/api-audit.md`. Revision 1 fixes all three round-1 blocking findings.
AC-13 and AC-14 pin the parse and brief-refresh outcomes, and they match today's
"LLM unusable" shapes (`backend/nlp/parser.go:179-182`, and
`backend/engine/meeting_brief_service.go:73-89`, which stores the failed brief so
`getBrief` returns it). AC-15 guarantees that a user can clear a value. The
sentinel-credential Given makes the zero-request criteria fail today. A fresh grep
for `NewLLMClientFromSettings` and `.Complete(` outside tests still finds only the
three send paths. Parse makes two `Complete` calls (`parser.go:185` via
`extractAttendees` at `:227`, and `:197`), and both use the same client. The
brief-refresh Given "no workspace connections, so no other outbound traffic"
holds: Slack and Notion are called only when a connection row exists
(`meeting_brief_service.go:63-71`), and the handler uses a stub event instead of
fetching from the calendar (`handlers_meeting_briefs.go:64-67`).

### Blocking

1. **No criterion covers a probe that overrides the provider, and that is the only
   way the web app probes.** The probe body may set `llmProvider`
   (`backend/api/handlers_llm.go:50-56`). The web client always sends it, taken
   from the form rather than from the saved row
   (`smart-calendar-flow/src/api/client.ts:712-716`, used at `:1297-1301`; `src/pages/Settings.tsx:173` passes the unsaved `draft`). Every
   probe criterion assumes the provider comes from the *stored* settings: AC-1
   "probes their stored configuration", AC-4 "has that provider selected", and
   AC-17 reaches the ambient provider through a settings write. An implementation
   could key its check on the stored `llmProvider` (for example, refusing at write
   time and whenever the saved provider is ambient) and pass AC-1 to AC-17. Then
   take a user whose saved provider is `openai` and whose row holds
   `azureEndpoint: "https://attacker.example"`. That user sends
   `POST /api/llm/test {"llmProvider":"azure_openai"}`, which is exactly what the
   Settings page's "Test connection" button does, and the deployment's token goes
   out. §3 forbids this in prose ("enforced again at the moment of sending, on
   every path"), but no test written from the criteria would catch it.
   *Resolve*: add a criterion for each ambient provider with this setup: the stored
   provider is a user-key provider (or a different ambient provider), the stored
   destination is unapproved (and, as a separate case, nothing is approved for the
   provider), and the probe body names the ambient provider. Expected: zero
   requests, and a 4xx JSON error as in AC-1 and AC-4.

2. **The spec gets the effective host of an empty Bedrock region wrong.** §4 says
   "An empty region means the deployment's own configured region", and Revision 1
   says it again. The code substitutes a hard-coded `us-east-1`
   (`backend/nlp/llm_bedrock.go:21-23`) and passes it as `config.WithRegion`
   (`:31-33`). That setting overrides `AWS_REGION` and the shared-config region,
   so the deployment's configured region is never used. (The round-1 challenge
   made the same mistake, "falls back to the deployment's own AWS configuration",
   and the author adopted it.) This changes what the criteria expect. Take an
   operator who approved only the Bedrock host of their configured region, say
   `eu-west-1`. For every user with an empty region, the result is either a
   refusal or a request to `us-east-1`, and the spec tells the test author to
   expect the opposite. Read literally, it also asks the implementer to change
   which region is used, which is a behaviour change §3 does not list.
   *Resolve*: state that an empty Bedrock region means the built-in default region
   (`us-east-1` today), and that the request goes to its resolved host only if that
   host is approved. Add a Bedrock version of AC-16: empty region with the default
   region not approved gives a refusal; with it approved, exactly one request. If
   the owner wants empty to mean "the deployment's region" instead, record that as
   a behaviour change and a decision.

### Non-blocking

1. **AC-10 constrains the order that the shared Givens leave free.** The Givens
   say "an implementation may acquire the credential before or after it decides to
   refuse". The compose stack has no cloud identity. An implementation that
   acquires first and returns on failure gives today's 502
   ("azure: get token: ...") instead of AC-10's 4xx. So AC-10 requires that a
   destination refusal is reported even when acquiring the credential would fail.
   That requirement is reasonable. State it in the Givens so the plan does not
   discover it in e2e.
2. **The spec does not say whether approval is per provider.** AC-1 ("one Azure
   inference host") and AC-4 ("approved nothing for a given ambient provider")
   assume a separate set per provider, but §3 says only "the approved inference
   destinations". With a shared set, a user can point the Azure endpoint at the
   approved Vertex or Bedrock host, and the Azure token goes to a host it was
   never meant for. State that each provider's credential goes only to hosts
   approved for that provider.
3. **"Exactly one request" needs the test double's answer pinned.** AC-6, AC-16
   and the Bedrock criterion requested in Blocking 2 count requests on the probe.
   The AWS SDK retries failed requests by default. If the recording transport
   answers with an error, a correct implementation records several Bedrock
   requests. State that in these criteria the double answers the inference request
   successfully.
4. **An empty Azure endpoint gets two different messages.** §4 says an empty
   endpoint is refused with "the provider is reported as not configured". AC-1's
   error must name "the endpoint as not approved". Say which message the probe
   gives for `""`, and add a criterion for the empty case. Today it appears only in
   the definition.
5. **The web repo has no "brief panel" consumer.** §5, §6 and D-5 say the web brief
   panel reads the refresh response. A case-insensitive grep for "brief" under
   `smart-calendar-flow/src` (generated types excluded) finds nothing, and the
   client never calls `/meetings/{id}/brief`. Parse has two consumers: the web
   client (`client.ts:1155`) and the MCP tool (`mcp/tools.go:195-203`). Nothing
   breaks, but the consumer list is wrong.

### Criteria I could not falsify

- None in the revised set. With the sentinel credential, AC-1 to AC-9, AC-13,
  AC-14, AC-16 and AC-17 fail against a23e836. AC-10 fails on the write (200
  today) and on the probe (502 today). AC-6, AC-11, AC-12 and AC-15 are guards, and
  each would catch a specific wrong implementation. Blocking 1 is a missing
  criterion, not an unfalsifiable one.

### Citations I checked and found wrong

- §4 "Effective host", Bedrock: "An empty region means the deployment's own
  configured region". The code uses `us-east-1`, hard-coded at
  `backend/nlp/llm_bedrock.go:21-23` and applied with `config.WithRegion` at `:31-33`
  (Blocking 2).
- Inherited scope, brief refresh: "documented 200 `status: "failed"`,
  `contracts/openapi/paths/habits.yaml:481-483`". That sentence is at
  `habits.yaml:487-488`. Lines 481-483 hold the operation's `operationId` and
  `summary`.
- §5, §6, D-5: "the brief panel" in the web app. No such consumer exists
  (Non-blocking 5).
- Checked and correct: `parser.go:179-182,185,197-200,227`, `handlers_nlp.go:28-31`,
  `meeting_brief_service.go:73-86`, `handlers_meeting_briefs.go:73-77`,
  `scheduling.yaml:332-337`, `habits.yaml:463` (`x-uncertain` on `getMeetingBrief`),
  `api-audit.md:314` (API-080, open, on `POST /api/nlp/parse`),
  `llm_vertex.go:20-22`, `llm_azure_openai.go:28-37`, `llm_vertex.go:30-37`,
  `llm_bedrock.go:38-41`, `bedrockruntime@v1.57.1/endpoints.go:226`
  (`IsValidHostLabel`), `mcp/tools.go:195-203`, `e2e/db.ts` (the e2e suite can
  write to the database directly, as AC-10 needs).

### Consumers this breaks

- None, given D-5. The web probe (`smart-calendar-flow/src/api/client.ts:1294-1302`)
  already throws on a non-2xx and shows the server's message, so a 4xx in place of
  today's 502 renders. Parse and brief refresh keep their response shapes for the
  web client (`client.ts:1155`) and `clockwise-like/mcp/tools.go:195-203`.

## Revision 2 — 2026-10-26

Answers to the round-2 challenge. The spec sections above are changed in place;
changes are marked *(Revision 2)*.

### Blocking

1. **A probe that overrides the provider.** Accepted. §2.3 now records that the
   probe may name a provider for one try and that the web settings page always
   does so from the unsaved form (`smart-calendar-flow/src/pages/Settings.tsx:173`,
   `src/api/client.ts:712-716`). §3 states that the rule follows the provider the
   request will actually use, not the saved one. New AC-18 covers each ambient
   provider with the saved provider set to a user-key provider and, as a separate
   case, to a different ambient provider: (a) stored destination unapproved gives
   zero requests and the AC-1 error; (b) nothing approved for the named provider
   gives zero requests and the AC-4 error; (c) a stored host approved only for a
   different provider is treated as unapproved. The saved provider must be
   unchanged afterwards. An implementation that keys its check on the saved
   provider fails (a) and (b), which is the case the challenge described (saved
   `openai`, unapproved Azure endpoint, probe naming `azure_openai`).
2. **The effective host of an empty Bedrock region.** Accepted; the challenge is
   right and Revision 1 was wrong. Checked on the code: `NewBedrockClient`
   substitutes `us-east-1` for an empty region (`backend/nlp/llm_bedrock.go:21-23`)
   and `Complete` passes it with `config.WithRegion` (`:31-33`). The SDK consults
   load options before the environment and shared config
   (`aws-sdk-go-v2/config@v1.32.35/config.go:212`, `provider.go:113-123`), so the
   deployment's configured region is never used. §2.2 and the §4 definition now
   say an empty region means the built-in default region, sent to only if its
   resolved host is approved. New AC-19 is the Bedrock version of AC-16: with the
   deployment's own region configured to the only approved region, an empty
   stored region is refused; with the default region's host approved, exactly one
   request goes to it. The spec does not change which region an empty value
   means; that is recorded as D-7, and the alternative is OQ-7 for the owner.

### Non-blocking, also taken

- NB-1: new shared Given "Refusal does not depend on the credential". The
  reported outcome of a refusal is pinned even when acquisition would fail; the
  order of acquisition and refusal stays free. AC-10 points at it.
- NB-2: §3 and new D-8: each ambient provider has its own approved set. AC-18 (c)
  tests a host approved only for another provider.
- NB-3: new shared Given "Successful answer": in AC-6, AC-16 and AC-19 the
  recording transport answers the inference request with a success, so SDK
  retries cannot change the count.
- NB-4: one message chosen for an empty Azure endpoint ("the Azure endpoint is
  not set"), pinned by new AC-20. AC-1's "not approved" message applies to a
  non-empty endpoint.
- NB-5: "the brief panel" removed from §5 and §6. Parse has two consumers (the
  web client, `client.ts:1155`, and `mcp/tools.go:195-203`); no web code calls
  brief refresh. D-5's wording did not name the panel and is unchanged.
- Citation found wrong: the Inherited-scope reference to the brief-refresh
  "failed" sentence is now `habits.yaml:487-488`.
- AC-11 now also covers the refusals in AC-16 to AC-20.

## Challenge — 2026-10-26 (round 3)

**Verdict**: accept

Reviewed on `factory/pac-66-spec` at 8eb4f20 (committed state only; the branch
sits on `origin/main` 037656c, so the code read is current main), against the
Linear issue text, `smart-calendar-flow` at 8bf231f, the contract fragments and
`docs/factory/api-audit.md`. Revision 2 resolves both round-2 blocking findings.
AC-18 covers a probe that names the provider for one try, and the citations
behind it hold: the probe overwrites `s.LLMProvider` from the body after loading
the saved row (`backend/api/handlers_llm.go:44-56`), and the web client always
sends `llmProvider` from the unsaved draft (`smart-calendar-flow/src/pages/Settings.tsx:173`,
`src/api/client.ts:712-716`, sent at `:1297-1301`). AC-19 and the corrected §4
definition match the code: `us-east-1` is substituted at `llm_bedrock.go:21-23`
and passed as `config.WithRegion` at `:31-33`, and the SDK puts load options
first in the config list (`aws-sdk-go-v2/config@v1.32.35/config.go:212`) and
takes the first region found (`provider.go:113-123`). A fresh grep of non-test
code for `NewLLMClientFromSettings`, the three ambient constructors and
`.Complete(` still finds only the three send paths (probe, parse twice, brief
refresh via `meeting_brief_service.go:81`). `MeetingBriefService` is built only
in the refresh handler, so no background job sends the credential. Inherited
scope is complete: the only open audit rows on the touched operations are
API-039 (claimed) and API-080, both named. API-054 in the probe's contract text
(`scheduling.yaml:473-474`) is an analytics finding, not one on these
operations, so "T8 is a theme here" is right.

I found nothing in Revision 2 that wrongly permits or forbids a request, breaks
a consumer, or contradicts the code. The items below are non-blocking.

### Blocking

None.

### Non-blocking

1. **Path and project steering on an approved host is neither covered nor
   recorded.** Approval is by host, and the user still controls the rest of the
   URL. Checked with Go's `net/url` against the exact format strings: Vertex
   location `us-central1-aiplatform.googleapis.com/v1/projects/OTHER/locations/us-central1/endpoints?#`
   gives host `us-central1-aiplatform.googleapis.com` (approved if that region
   is) and path `/v1/projects/OTHER/locations/us-central1/endpoints`. Azure
   endpoint `https://approved.openai.azure.com/openai/other/path?x=` gives the
   approved host with path `/openai/other/path`. `gcpProject`, `vertexModel`,
   `azureDeployment` and `azureApiVersion` are also spliced in unescaped
   (`llm_vertex.go:39-42`, `llm_azure_openai.go:39-40`). So a user can make the
   server POST, with the deployment's token, to any operation on an approved host,
   against any GCP project the identity can reach. The response comes back in
   the 502 text (`parser.go:144-145`) or in `vertex: unexpected response: <body>`
   (`llm_vertex.go:66`). The token itself does not leave, so this is not the
   issue's core problem. But the issue names "region / project" as the steering
   values, and the spec says nothing about `gcpProject` or paths. Record it the
   way OQ-4 records profile selection, as a sibling problem with an owner. Also
   say explicitly that AC-5 and AC-6 accept a value whose effective host is
   approved whatever its path, so the contract stage does not have to guess.
2. **AC-18 (c) and (b) can apply to the same state and require different
   messages.** If nothing is approved for P and the stored host is approved only
   for another provider, (b) requires "P is not enabled" and (c) requires "not
   approved". Both give zero requests and a 4xx, so the security outcome is the
   same. Add "at least one host is approved for P" to (c), as AC-20 already does,
   or state which message wins.
3. **AC-5 and PAC-24 AC-17 disagree when the same value is sent again.** AC-5
   refuses a write that "sets" an unapproved value. Under changed-only validation
   (`handlers_settings.go:129-136`), a write that sends back an unapproved value
   already stored, as `e2e/tests/settings.spec.ts:54-55` does with a whole GET
   document, changes nothing and is accepted. Say that AC-5 applies when the
   value differs from the stored one. Send-time refusal still covers the stored
   value (AC-9).
4. **"Exact host" does not say how port, letter case and a trailing dot are
   handled.** `https://approved.openai.azure.com:8443` parses to
   `Host = approved.openai.azure.com:8443`. Whether that matches an entry
   `approved.openai.azure.com` is open. Each choice is safe, because only the
   operator's machine answers on that name, but a test needs one answer. Leave it
   to the contract stage under OQ-2, and name it there.
5. **AC-12 may fail closed on the most likely operator input.** Operators copy
   the Azure endpoint from the portal as a URL (`https://name.openai.azure.com/`).
   Under AC-12 an entry that is "not a host name" approves nothing and is only
   logged, so an upgraded deployment would stay broken with no user-visible cause
   beyond AC-4's message. This is safe, so it does not block, but the contract
   stage should say whether a URL-form entry is accepted (reduced to its host) or
   refused, and `.env.example` must show the expected form (§7 already requires
   a note there).

### Criteria I could not falsify

- None. With the sentinel credential, AC-18 (a) and (b) fail today (the probe
  sends to the stored host after taking the provider from the body), AC-19's
  first half fails today (the request goes to the `us-east-1` host), and AC-20
  fails today (an empty endpoint builds a relative URL, `http.DefaultClient.Do`
  errors, and the probe answers 502 at `handlers_llm.go:80-82`). AC-18's "saved
  provider unchanged" check and AC-11 are guards, and each would catch a specific
  wrong implementation.

### Citations I checked and found wrong

- None in Revision 2. Checked and correct: `handlers_llm.go:23-27,44,50-62,73,79,80-82`,
  `llm_bedrock.go:21-23,31-33,35-37,38-41`, `aws-sdk-go-v2/config@v1.32.35/config.go:212`,
  `provider.go:113-123`, `llm_vertex.go:20-22,30-37,39-42`,
  `llm_azure_openai.go:28-37,39-40,50`, `parser.go:125-148,179-182,197-200,227,239-241`,
  `meeting_brief_service.go:73-91`, `handlers_settings.go:129-136` and
  `settingsProblems` (no endpoint, region, location or profile check),
  `scheduling.yaml:332-337,476-488` and its `x-uncertain` markers
  (schedule-suggest :246, NLP-confirm :376, schemas :928 and :1209),
  `habits.yaml:463,487-488`, `api-audit.md:185,273,314`,
  `pac24_scope_test.go:70-124`, the `parser_test.go` test line numbers in §6,
  and the absence of any test that mentions azure, vertex or bedrock.
  Web: `Settings.tsx:173,749`, `client.ts:699-716,1155,1294-1302`.

### Consumers this breaks

- None. The web probe throws on a non-2xx and shows the server message, so the
  4xx renders. Parse and brief refresh keep their shapes for
  `smart-calendar-flow/src/api/client.ts:1155` and `clockwise-like/mcp/tools.go:195-203`.
  No MCP tool calls the probe, the settings write or brief refresh. The e2e
  whole-document PATCH (`e2e/tests/settings.spec.ts:54-55`) keeps passing,
  because it changes no destination property and an empty value is always
  accepted (AC-15).
