# Spec — PAC-66: A user can point the server's cloud-identity LLM requests at a host of their choice

- **Linear**: https://linear.app/paceday/issue/PAC-66
- **Status**: challenged (round 1 answered in "Revision 1", round 2 in "Revision 2")
- **Part B (PAC-66-B)**: draft, appended at the end of this file (channel 2, D-4)
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
  *(2026-10-26)* PAC-66-B's spec is "Part B" at the end of this file; its
  Ollama decision is D-B1 there.
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

---

# Part B — PAC-66-B: a user-set OpenAI, Anthropic or Ollama base URL can still steer server-side LLM requests to any host

- **Linear**: not yet filed. This part is the spec D-4 asks for; the factory
  ticket is `NEW-pac66b-llm-base-url`. D-4 still holds: PAC-66 is not closed
  until PAC-66-B exists in Linear and is linked to PAC-66 (AC-B13).
- **Status**: challenged (Revision B2 answers the Part B round-2 challenge)
- **Author**: spec-author
- **Base**: `main` at c954521 (PAC-66 part A implemented by #214, PAC-21 by #217).
- **Inherited scope**:
  - The provider-probe operation's description still says the user-key and
    keyless providers "are unchanged by PAC-66" and that their request-forgery
    channel "is PAC-66-B" (`contracts/openapi/paths/scheduling.yaml:522-528`).
    The contract stage must replace that paragraph.
  - The settings document's two base-URL properties carry no host rule in the
    contract (`contracts/openapi/paths/scheduling.yaml:1943-1945`, `:1997-1999`).
    The second says it falls back to the first, then to a local default.
  - `contracts/features/PAC-66.yaml:135-136` records two follow-ups "to be filed
    beside PAC-66-B": path steering on an approved host
    (`approvedHostPathSteering`) and Bedrock profile selection
    (`bedrockProfileSelection`, part A OQ-4). Neither is resolved here (§B5); they
    are listed so the filing of PAC-66-B carries them.
  - No `x-uncertain` marker sits on the probe, settings read/write, parse or
    brief-refresh operations (the markers in `scheduling.yaml` are at :246, :376,
    :1046, :1327, on other operations and schemas).
  - Open audit rows on the touched operations: API-039 (`docs/factory/api-audit.md:283`,
    still "Claimed (smart-calendar-flow#101)", a frontend enum problem, not
    resolved here) and API-080 (`:324`, parse zero-time range, pulled in only if
    the contract stage edits the parse operation, exactly as in part A).

## B1. Problem

Paceday can talk to three LLM services that are not tied to the deployment's
cloud identity: OpenAI and Anthropic, with a key the user types in, and Ollama,
with no key at all. For each of them the **user** decides where the server sends
the request, by saving a base URL in their own settings. Part A of this spec
stopped the deployment's cloud identity from going to a user-chosen host. It
deliberately left these three alone (§0, D-4).

What a user can still do with an ordinary account:

1. **Make the Paceday server send requests to any address it can reach.** Saving
   a base URL that points at an internal address (the cloud metadata service, a
   database admin port, a service on the deployment's private network, the
   loopback interface) and pressing "Test connection", typing in the
   natural-language bar or regenerating a meeting brief makes the server POST
   to that address. The probe's answer (success, or a failure) tells the user
   whether something answered there, and a success returns text the server
   extracted from the answer. This is server-side request forgery with an
   oracle. On a hosted product (PRODUCT.md: "Delivered as hosted SaaS") the
   reachable addresses are the operator's own infrastructure.
2. **Send their own key somewhere they did not intend, through a redirect.** An
   approved-looking host that answers with a redirect gets the request re-sent
   by the server. For Anthropic the key travels in a header that the Go HTTP
   client re-sends to **any** redirect target (§B2.4), so a third party can
   receive it.
3. **Have their key sent to their Ollama server.** The web app stores the Ollama
   address in the same setting that OpenAI and Anthropic use as their base URL
   (§B2.2). A user who once set up Ollama and then switches to OpenAI sends their
   OpenAI key to the Ollama address. Low value today (it is their own server),
   but any fix that approves Ollama addresses must not turn this into "the key
   goes to an address the operator approved for something else".

Who is affected: every operator. The attacker needs only an account. The cost is
exposure of the operator's internal network to probing and to blind POST
requests, and, for item 2, a user's own key reaching a third party.

Every user is also affected by default, not only by choice *(Revision B2)*. An
account that never opened the AI settings is stored as an Ollama account with the
local address (§B2.1), so today each natural-language parse and each brief refresh
by such an account makes the server POST to its own loopback interface. After this
part ships, on the hosted product and on any deployment that approves no Ollama
destination, every such account is told that Ollama is not enabled on this
deployment, by the natural-language bar and by every refreshed brief, until the
user picks another provider. That is the intended fail-closed outcome (D-B1), and
it is why OQ-B2 matters more than it first reads.

## B2. Current behaviour

### B2.1 Where the destination comes from

- `NewLLMClientFromSettings` (`backend/nlp/llm_factory.go:13-58`) builds the
  client from the caller's own settings row:
  - `openai` → `OpenAIClient{APIKey: s.LLMAPIKey, …, BaseURL: s.LLMBaseURL}` (`:29`).
  - `anthropic` → `AnthropicClient{…, BaseURL: s.LLMBaseURL}` (`:38`).
  - `ollama` → base URL is `s.OllamaBaseURL`, else `s.LLMBaseURL` (`:40-42`), else
    `http://localhost:11434` (`:44-46`).
- Each client uses the base URL verbatim, with any scheme, port or host:
  OpenAI falls back to `https://api.openai.com` only when it is empty
  (`backend/nlp/parser.go:33-36`), Anthropic to `https://api.anthropic.com`
  (`:66-69`), Ollama to `http://host.docker.internal:11434` (`:102-105`, unreachable
  through the factory, which always fills it).
- The key goes out as `Authorization: Bearer` for OpenAI (`parser.go:45`) and as
  `x-api-key` for Anthropic (`:80`). Ollama sends no credential.
- **Stored defaults of a settings row** *(Revision B2)*. Migration
  `backend/storage/migrations/002_llm_providers.up.sql:10` gives the Ollama base
  URL the column default `http://localhost:11434`, and `:13-14` make the provider
  default to `ollama` (and rewrote existing empty providers to it). The shared
  base URL has no such default (empty). Every settings row is created by an
  `INSERT INTO settings (user_id)` that names no other column
  (`backend/storage/settings.go:246`, `backend/storage/personal_calendars.go:165`,
  `backend/auth/microsoft_oauth.go:65`), so it takes these defaults. An untouched
  account therefore reads back provider `ollama`, shared base URL empty, Ollama
  base URL `http://localhost:11434` (confirmed by the round-2 challenger against
  the migrations on `postgres:16-alpine`). Because the factory prefers the Ollama
  base URL over the shared one (`llm_factory.go:40-42`), **on such a row the
  shared base URL is never Ollama's destination**; it becomes one only after the
  Ollama base URL is set to the empty string through the API.

### B2.2 What the settings write does with these values

- `settingsProblems` (`backend/api/handlers_settings.go:206-282`) checks the
  provider name (`:257`) and, since part A, the Azure endpoint, Vertex location
  and Bedrock region (`:263-271`). It checks neither base URL. Any string is
  stored.
- The web app's only base-URL field is shown for Ollama and writes the **shared**
  base URL (`smart-calendar-flow/src/pages/Settings.tsx:741-752`, placeholder
  `http://localhost:11434`; mapped to the API's shared property at
  `src/api/client.ts:594`). The provider list labels it "Ollama (local)"
  (`Settings.tsx:62`). There is no web field for an OpenAI or Anthropic base URL;
  those can be set through the API only.
- The web app never reads or writes the Ollama-specific base URL (its field
  mapping at `src/api/client.ts:594` covers the shared one only). Combined with the
  stored default (§B2.1), a value typed in the web app's Ollama field has **no
  effect** on where Ollama requests go today: they go to the stored
  `http://localhost:11434` *(Revision B2)*.

### B2.3 Every path that sends

The same three paths as part A (§2.3), confirmed on c954521 by a grep of
non-test code for `NewLLMClientFromSettings`, `OpenAIClient{`,
`AnthropicClient{` and `OllamaClient{`: the probe
(`backend/api/handlers_llm.go:75`, send at `:89`), natural-language parse
(`backend/nlp/parser.go:253`), meeting-brief refresh
(`backend/api/handlers_meeting_briefs.go:76`). All three call `CheckDestination`
first (`handlers_llm.go:85`, `parser.go:193`, `handlers_meeting_briefs.go:80`),
which returns nil for these three providers (`backend/nlp/destination.go:166-176`,
"not checked (spec §0, PAC-66-B)").

### B2.4 Transport

- All three send through `postJSON` → `http.DefaultClient`
  (`backend/nlp/parser.go:126-128`, `Do` at `:144`). It follows redirects.
- Verified on 2026-10-26 with Go 1.25.5 (a two-server `httptest` program, A on
  `127.0.0.1` redirecting with 307 to B on `localhost`): B received
  `x-api-key: SENTINEL` and an empty `Authorization`. The standard library strips
  only `Authorization`, `WWW-Authenticate` and `Cookie` on a cross-host redirect,
  so **an Anthropic key follows a redirect to any host**; an OpenAI key follows
  one to the same host or a subdomain, including over `http`.
- Since PAC-21 the probe's 502 body is generic (`backend/api/server_errors.go:53-56`,
  used by `handlers_llm.go:106-110`). The provider's response text no longer
  reaches the client on failure. The oracle that remains is success versus
  failure, timing, and the extracted text on success.

### B2.5 Configuration

- No deployment setting names an OpenAI, Anthropic or Ollama host.
  `.env.example:50` says they "are configured per-user in the Settings page".
  Part A's operator configuration covers the three ambient providers only
  (`backend/config` `PACEDAY_LLM_APPROVED_HOSTS_{AZURE_OPENAI,VERTEX,BEDROCK}`;
  `docker-compose.yml:56-58`).

### B2.6 Existing tests

- `backend/nlp/parser_test.go` sends real requests from these three clients to
  `httptest` servers, which listen on `http://127.0.0.1:<port>`:
  `TestOpenAIClient_Complete` (:162), `TestAnthropicClient_Complete` (:183),
  `TestPostJSON_Success` (:204), `TestPostJSON_HTTPError` (:230),
  `TestOllamaClient_Complete_Mock` (:244), `TestNLPService_Parse_WithOllama` (:282).
  Every one of them points a client at a host that this part refuses by default
  (§B6).
- `TestLLMTest_BodyRulesAndNoKeyEcho` (`backend/api/pac24_scope_test.go:70-124`)
  covers the `openai` probe body rules.
- Further tests point these clients at `httptest` servers on
  `http://127.0.0.1:<port>`, or assert today's unchecked behaviour *(Revision B1)*:
  `TestNLPService_Parse_ScheduleMeeting` (`parser_test.go:317`),
  `TestNLPService_Parse_InvalidJSON` (:351) and
  `TestParse_AuditEntrySubjectIsCallerFromContext` (:395), all three storing the
  test server as an Ollama `LLMBaseURL`; `TestUserKeyProviders_AreNotChecked`
  (`backend/nlp/destination_test.go:251-262`), which asserts that the destination
  check returns nil for `openai` at `http://10.0.0.1`, `anthropic` at
  `http://attacker.example` and `ollama` with nothing stored;
  `TestPAC66_UserKeyAndKeylessProbesUnchanged`
  (`backend/api/pac66_llm_destination_test.go:448-475`), which probes Anthropic
  and Ollama at the test server and expects 200; and
  `TestLLMTest_BodyRulesAndNoKeyEcho`, which also stores an `http://127.0.0.1`
  OpenAI base URL (`pac24_scope_test.go:86`) and asserts a 502 after the provider
  is contacted (:112-118).
- These tests assert on what arrives at their test server. None of them runs these
  clients through part A's recording transport, and none covers a redirect. No
  e2e test exercises a base URL (`e2e/tests/llm-destination.spec.ts` covers Azure
  only).
- Each client builds its request address by appending the provider's operation
  path to the base URL as a string (`parser.go:44`, `:78`, `:114`). A path, query
  or fragment in the base URL therefore changes which operation is called on the
  destination; for example the base `http://ollama:11434/api/pull?x=` sends a POST
  to Ollama's model-download operation with the user's chosen model in the body
  (verified by the challenger with Go `net/http` on 2026-10-26).

## B3. Desired behaviour

The server sends an LLM request only to a destination that someone accountable
for the server approved: the operator, or (for the two vendors' own public
endpoints) the product itself. A user chooses among approved destinations; a
user never adds one.

- **The vendors' own endpoints keep working with no configuration.** A user who
  leaves the base URL empty reaches OpenAI's or Anthropic's public API exactly as
  today. Those two exact hosts, over secure transport, are approved for their own
  vendor and for nothing else (D-B2).
- **Any other OpenAI or Anthropic destination needs the operator.** An
  OpenAI-compatible gateway or an Anthropic proxy is used only if the operator
  approved that exact host for that provider, over secure transport (D-B3).
- **Ollama is kept for self-hosted deployments only** (D-B1). It works only
  toward a host the operator approved for Ollama. Because a self-hosted Ollama is
  normally a plain-HTTP service on a local or private address, the operator (and
  only the operator) may approve a host with insecure transport and a
  non-default port for Ollama. If the operator approved nothing for Ollama,
  Ollama is not enabled on that deployment, and the user is told so in plain
  words. On the hosted product, where the operator approves no Ollama host,
  Ollama is not available.
- **Approval is per provider and never transfers** (as part A, D-8). A host
  approved for Ollama is never a destination for an OpenAI or Anthropic key, and
  the reverse. Because the Ollama address and the OpenAI/Anthropic base URL share
  one stored value today, this is what keeps a user's key off an Ollama host.
- **An approved destination is reached only for the provider's chat inference
  operation** *(Revision B1, D-B8)*. Approval names a scheme, host and port, not a
  service: the user does not get to pick which operation on that destination the
  server calls. A base URL that carries anything beyond scheme, host and port
  (a path other than an empty one or a single slash, a query, a fragment, or user
  information) is refused on save and at send time, for all three providers. The
  request the server sends has exactly the path of the provider's chat operation
  and no query. The user's choice of model stays a body value of that one
  operation and does not change which operation is called.
- **The rule is applied on save and again at the moment of sending**, on every
  path (probe, natural-language parse, meeting-brief refresh), and follows the
  provider the request actually uses, including a provider named for one probe
  only. Values stored before this change are refused at send time, not rewritten.
- **A redirect is never followed** for these three providers *(Revision B2,
  D-B6)*: not to another host, not to another operation or query on the same
  destination, not with a changed method, and not to another destination approved
  for the same provider. The request that drew the redirect counts as failed. So a
  redirect never takes a request to a destination not approved for that provider,
  and never takes a user's key to any other host.
- **A refusal sends nothing.** It is reported the way part A reports one on each
  path: the probe answers with the documented client error and its documented
  refusal reason; parse answers "intent unknown" with an explanatory error;
  brief refresh stores and returns a failed brief that says why. A settings write
  is refused with a per-property error and stores nothing.
- **Clearing a base URL is always accepted**, so a user can undo a bad value.
  Clearing means "the provider's default", and that default is itself checked
  (for Ollama the local default is sent to only if approved).

## B4. Acceptance criteria

Throughout, "user-key provider" means OpenAI and Anthropic, and each criterion
written for them applies to each. "Approved for P" means either named for P in
the operator's deployment configuration, or, for OpenAI and Anthropic only, the
vendor's own public host (D-B2). The **recording transport** and **zero
requests** are as defined in part A §4, with a **sentinel key** (a known user key
on file or submitted in the probe) in place of the sentinel credential.

**Effective destination** of a stored configuration *(Revision B1, as part A's
"effective host")*: the scheme, host, port and path of the inference request that
would actually be sent, after every default is applied. The defaults are:

- OpenAI / Anthropic: when the base URL is the empty string, the vendor's public
  host over secure transport. A non-empty base URL whose host is empty (for
  example `http:/`) is **not** empty: it has no approvable destination and is
  refused, never replaced by the public host.
- Ollama: the Ollama base URL; when it is the empty string, the shared base URL;
  when both are empty, the built-in local default (`http://localhost:11434`
  today). This spec does not change that fallback order. *(Revision B2)* An
  account's starting state is not "both empty": its Ollama base URL is stored as
  `http://localhost:11434` (§B2.1), so its effective destination is that value and
  the shared base URL plays no part until the Ollama base URL is cleared.

**Refusal does not depend on the key** *(Revision B1)*: when a destination is
refused and no key is on file or submitted, the outcome is the destination
refusal, as part A §4 states for the credential.

AC-B1. Given the operator approved no extra host for OpenAI, and a user with
OpenAI selected whose stored base URL names any other host (for example an
internal address), when the user probes their stored configuration, then the
recording transport records zero requests and the response is the probe's
documented refusal (a 4xx JSON error with the documented refusal reason, naming
the base URL as not approved). [unit]
*Fails today: the request is sent to the stored host with the sentinel key.*

AC-B2. Given the same state for Anthropic, when the user probes, then the outcome
is as AC-B1. [unit] *Fails today.*

AC-B3. Given a user who submits a settings write that changes the shared base URL
to a non-empty value whose effective destination is approved for **none** of
OpenAI, Anthropic and Ollama, when the write is processed, then the response is a
4xx JSON error naming that property, a following read returns the previous
value, and zero requests are recorded. The check applies whichever provider is
selected. [contract] [unit]
*Fails today: the write is accepted with 200 and stored.*

AC-B4. Given a user who submits a settings write that changes the Ollama-specific
base URL to a non-empty value not approved for Ollama, when the write is
processed, then the outcome is as AC-B3. [contract] [unit] *Fails today.*

AC-B5. Given the operator approved exactly one extra host for OpenAI, when a user
saves a base URL on that host over secure transport and probes with OpenAI, then
the write succeeds and exactly one request is recorded, to that host, over secure
transport, carrying the sentinel key. Given instead an empty base URL, the same
probe sends exactly one request, to OpenAI's public host. The same holds for
Anthropic and its public host. [unit]
*Guards against a fix that refuses everything, and against a fix that breaks the
default configuration. The recording transport answers with a well-formed
success, as in part A.*

AC-B6. Given an approved OpenAI or Anthropic host, and a stored base URL that
names that host with an insecure scheme or a non-default port, when the user
probes, then zero requests are recorded and the response is the documented
refusal. A settings write of that value is refused as in AC-B3 unless the value
is approved for Ollama (AC-B11 covers what happens then). [unit]
*Fails today: the base URL is used verbatim.*

AC-B7. Given an approved destination for a provider P (each of OpenAI, Anthropic,
Ollama, and OpenAI's and Anthropic's public hosts) that answers with a redirect
of any status (301, 302, 303, 307, 308) to any target, including another host, a
subdomain of the approved host, the same host over insecure transport, an
internal address, the same approved destination at a path other than the
provider's chat operation *(Revision B1)*, the same chat operation path with a
query added, or another destination that is also approved for P *(Revision B2)*,
when a user probes, then exactly one request is recorded (the first, to the
approved destination, with method POST), no request is recorded for the redirect
target with any method, the sentinel key appears in no recorded request other
than the first, and the probe reports failure. [unit]
*Fails today: `http.DefaultClient` follows the redirect (turning the POST into a
GET on 301/302/303), and for Anthropic re-sends the key to any host (§B2.4).*

AC-B8. Given the operator approved nothing for Ollama, and a user with Ollama
selected (saved, or named for one probe) with any stored base URL, including
none, when the user probes, then zero requests are recorded and the response is
the documented refusal with the reason saying Ollama is not enabled on this
deployment. [unit]
*Fails today: the request is sent, to `localhost:11434` when nothing is stored.*

AC-B9. Given the operator approved one Ollama destination, including one with
insecure transport and a non-default port (for example a plain-HTTP host on port
11434), when a user saves exactly that destination as the **Ollama-specific**
base URL (through the API; the web app cannot write that property today, §B2.2)
and probes with Ollama, then exactly one request is recorded, to that
destination, carrying no key. A stored Ollama destination that differs from the
approved one in host, scheme or port is refused as in AC-B1. [unit]
*Revision B2*: given instead that the user saves that destination as the
**shared** base URL (what the web app's field writes) while the Ollama-specific
base URL holds the stored default `http://localhost:11434`, the save succeeds
(the value is approved for Ollama, AC-B3), and the probe is decided on the
effective destination, the stored default: zero requests are recorded and the
response is the documented refusal naming the Ollama-specific base URL, unless
the operator also approved the local default, in which case exactly one request
is recorded, to the local default. The shared value is never the destination in
this state (fallback order unchanged, D-B9).
*Guards D-B1: Ollama is kept, not dropped, for an operator who approves it, and
pins that the web field does not silently redirect Ollama.*

AC-B10. Given the operator approved Ollama only for the built-in local default
destination, and a user with Ollama selected whose settings are in an account's
starting state (Ollama-specific base URL stored as `http://localhost:11434`,
shared base URL empty, §B2.1) *(Revision B2)*, when the user probes, then exactly
one request is recorded, to that default. The same holds when both base URLs are
empty (the built-in fallback). Given instead that the default is not approved
(and something else is), the same probes record zero requests and return the
documented refusal. [unit]
*Pins the effective destination of an untouched account and of an empty Ollama
configuration.*

AC-B11. Given a destination approved **only for Ollama**, and a user whose shared
base URL is that destination, when the user probes with OpenAI or with Anthropic
(saved, or named for that probe only, with a key on file or submitted), then zero
requests are recorded and the response is the documented refusal naming the base
URL as not approved for that provider. The sentinel key appears in no recorded
request. The converse also holds: a host approved only for OpenAI or Anthropic is
refused for Ollama. [unit]
*Fails today: the key is sent to the Ollama host. This is the criterion that
proves no option widens who receives the user's key.*

AC-B12. Given any user-key provider or Ollama, and a stored effective destination
not approved for it (or nothing approved for Ollama), when the natural-language
parse runs for that user, and separately when that user regenerates a meeting
brief, then zero requests are recorded on either path; parse answers as part A
AC-13 (200, intent "unknown", a non-empty error sentence saying the destination
is not approved or the provider is not enabled), and brief refresh answers and
stores as part A AC-14 (200, a failed brief whose text says why). [contract] [unit]
*Fails today: the request is sent on both paths.*

AC-B13. Given this spec is accepted, when Linear is read, then an issue for
PAC-66-B exists in team PAC, it is linked to PAC-66 as related, its description
links this part of the spec, and PAC-66 links back to it. [manual: checked by the
stage that files the issue, before PAC-66 is moved to Done]

AC-B14. Given a settings row written **before** this change holding an unapproved
OpenAI, Anthropic or Ollama destination, when its owner probes, parses or
regenerates a brief, then zero requests are recorded and each path answers as
AC-B1, AC-B12 state; when the same user then saves an unrelated setting (for
example their working hours), that save succeeds; and when the user saves either
base URL as empty, that save succeeds and a following read shows the empty
value. [unit]
*Preserves PAC-24 AC-17 and part A D-3/D-12 for these properties.*

AC-B15. Given the operator's approved-destination configuration for OpenAI,
Anthropic or Ollama is present but malformed, when the server starts, then it
starts, logs the bad entry, treats it as approving nothing, and keeps the
well-formed entries beside it. A malformed entry never approves everything, and
never removes OpenAI's or Anthropic's public host from the approved set. [unit]

AC-B16. Given a refusal under any criterion above, when the response body (and,
for a brief, the stored brief) is read, then it contains neither the stored nor
a submitted key, and its error text is a non-empty human-readable sentence.
[unit]

AC-B17. Given the full compose stack with **no** Ollama destination and no extra
OpenAI destination approved, and a signed-in user with an OpenAI key on file
*(Revision B1)*, when the user saves an OpenAI
base URL pointing at another service on the stack's network (for example the
database's host and port), then the response is a 4xx JSON error naming the base
URL and a following read shows the previous value. When **both** stored base URLs
(the shared one and the Ollama-specific one) are then forced, by direct database
write, to that address *(Revision B2: so that the forced value is the effective
destination of both providers, §B2.1)*, and the user probes with Ollama, the
response is the documented refusal saying Ollama is not enabled on this
deployment; when the user probes with OpenAI, the response is the documented
refusal naming the shared base URL. [e2e]
*Fails today: the save returns 200, and each probe is sent to the forced address
(the Ollama one because the forced Ollama-specific base URL is its destination,
the OpenAI one through the shared base URL) and returns 502 when that service does
not answer as an LLM would.* On the stack the e2e test observes the response, not
the request; that the refusal sends zero requests, and that exactly one request
reaches an approved destination, is proven at unit level (AC-B1, AC-B8, AC-B5,
AC-B9), not on the stack.

AC-B18. *(Revision B1)* Given the operator approved one Ollama destination, and a
user who submits a settings write setting the Ollama base URL or the shared base
URL to that destination followed by a path, a query or a fragment (for example
the approved destination followed by `/api/pull?x=`, by `#`, or by
`/x/../api/pull`), when the write is processed, then the outcome is as AC-B3
(4xx naming the property, previous value kept, zero requests). Given instead that
such a value is already stored (written directly, as before this change), when
the user probes with Ollama, parses, or regenerates a brief, then zero requests
are recorded and each path answers as AC-B1 and AC-B12. Given the approved
destination itself, with nothing after the port or a single trailing slash, the
probe records exactly one request, whose path is exactly Ollama's chat operation
path and which has no query. [unit]
*Fails today: the write is accepted, and the probe sends a POST to Ollama's
model-download operation with the user's model in the body.*

AC-B19. *(Revision B1)* Given OpenAI's public host, or an operator-approved
OpenAI destination, and a stored or saved base URL on that destination followed
by a path, a query or a fragment, when the user saves it or probes, then the
outcome is as AC-B18 (write refused; zero requests at send time; the reason
names the base URL). With an empty base URL or the approved destination alone,
exactly one request is recorded and its path is exactly the chat-completions
path, with no query. The same holds for Anthropic and its messages path. [unit]
*Fails today: the stored value is concatenated and sent.*

## B5. Explicitly out of scope

- **Removing per-user keys altogether.** Whether "bring your own AI key" is a
  product feature is PAC-56 OQ-1, still unanswered. This part makes the feature
  safe to keep; it does not decide whether it stays.
- **Re-checking the approved host's resolved addresses.** An approved host name
  that resolves to a private address (by DNS change or rebinding) is the
  operator's configuration to keep correct. The criteria approve by host name, as
  part A does. Recorded as OQ-B3.
- **Path steering on an approved host for the ambient providers, and Bedrock
  profile selection.** Both are part-A follow-ups (inherited scope above). For
  OpenAI, Anthropic and Ollama, path steering is **in** scope *(Revision B1)*:
  §B3, AC-B18, AC-B19 and D-B8 close it on this channel. What stays out is the
  ambient case (Azure endpoint path, deployment, API version, Vertex project and
  model) and Bedrock profile selection. Whoever files PAC-66-B files these two
  beside it.
- **Any change to the web app.** The settings page already shows the server's
  error sentence. Hiding Ollama, or the base-URL field, when the deployment has
  not enabled it needs the server to say which providers are enabled, which is a
  contract addition; it is a follow-up (OQ-B2). So is making the web app's
  Ollama field write the property Ollama actually uses, or show and clear it
  (OQ-B7, D-B9) *(Revision B2)*.
- **Splitting the shared base URL into one value per provider.** It would remove
  the cause of item 3 in §B1 but rewrites stored data. AC-B11 closes the risk
  without touching stored values (D-B4).
- **Request bodies, models and the probe's fixed prompt.** Unchanged.

## B6. What must not change

- **OpenAI and Anthropic with no base URL** keep working on every deployment with
  no configuration (AC-B5 second half). Protected today by
  `TestBuildLLMClient_OpenAI` (`backend/nlp/parser_test.go:104`) and
  `TestBuildLLMClient_Anthropic` (:118), which build the clients but send nothing
  (`TestBuildLLMClient_DefaultModels` at :143, cited here before Revision B2,
  builds an Ollama client only and protects nothing for OpenAI or Anthropic).
  AC-B5 is the first test that sends a request to the public host (through the
  recording transport).
- **Existing tests that point these clients at a test server** *(Revision B1;
  replaces the earlier bullet, which prescribed a Given D-B3 forbids)*. No test is
  deleted or skipped. Each one below falls in exactly one of four groups.
  - **Keep their assertions; Ollama Given.** `TestOllamaClient_Complete_Mock`
    (`parser_test.go:244`), `TestNLPService_Parse_WithOllama` (:282),
    `TestNLPService_Parse_ScheduleMeeting` (:317),
    `TestNLPService_Parse_InvalidJSON` (:351),
    `TestParse_AuditEntrySubjectIsCallerFromContext` (:395). Given: the operator's
    configuration for the test approves the test server's destination
    (`http://127.0.0.1:<port>`) for Ollama. D-B1 permits that, because Ollama
    carries no key; no exception is needed.
  - **Keep their assertions; public-host Given.** `TestOpenAIClient_Complete`
    (:162), `TestAnthropicClient_Complete` (:183). Given: the client's base URL is
    empty or the vendor's public host (approved by D-B2: secure transport, default
    port), and the test's outbound transport delivers the request to the
    in-process test server instead of the network, the same technique as part A's
    recording transport (§4). The destination the rule sees is the approved public
    host, so D-B3 is respected and **there is no test-only exception**: nothing in
    the product approves `http://127.0.0.1` for OpenAI or Anthropic, in a test or
    elsewhere. The assertions on method, headers, body and parsed answer are
    unchanged.
  - **Keep their assertions; whichever Given fits.** `TestPostJSON_Success` (:204),
    `TestPostJSON_HTTPError` (:230) exercise the raw sending step with no
    provider and no key. If the plan puts the rule above that step they are
    untouched; if it puts the rule in that step, they take the Ollama Given above
    (keyless, approved test-server destination). Either way the assertions stay.
  - **Superseded by Part B criteria.** Changing these assertions is authorised by
    the criteria named and is not a skipped or weakened test:
    - `TestUserKeyProviders_AreNotChecked` (`backend/nlp/destination_test.go:251-262`):
      superseded by AC-B1 (openai at `http://10.0.0.1` is refused), AC-B2 and AC-B6
      (anthropic at `http://attacker.example` is refused), and AC-B8 / AC-B10
      (ollama with nothing stored is refused unless the local default is approved).
    - `TestPAC66_UserKeyAndKeylessProbesUnchanged`
      (`backend/api/pac66_llm_destination_test.go:448-475`): the Anthropic case is
      superseded by AC-B2 and AC-B6 (refused); the Ollama case keeps its 200 under
      AC-B9's Given (test server approved for Ollama). The test's last assertion,
      that the ambient transport records nothing, is unchanged.
    - `TestLLMTest_BodyRulesAndNoKeyEcho` (`backend/api/pac24_scope_test.go:70-124`):
      the body-rule half (400s) and the no-key-echo half are unchanged. The
      expectation of a 502 after contacting an `http://127.0.0.1` OpenAI base URL
      (:86, :112-118) is superseded by AC-B1 (the 4xx refusal, still with no key
      in the body, AC-B16). The property that half protected, a provider failure
      gives a 502 whose body echoes no key, stays covered by giving it the
      public-host Given above with a test server that answers 401.
    - `TestDecideLLMApprovedHosts_PerProvider`
      (`backend/config/llm_hosts_test.go:87-89`) *(Revision B2)*: its last
      assertion, that `openai` is not enabled and has no approved host in part
      A's ambient-provider set, is **kept** if the plan holds the OpenAI,
      Anthropic and Ollama approved sets separately from part A's set, and is
      **superseded by D-B2 and D-B3** if the plan merges them into it. Either way
      the property behind it holds: an entry approved for one provider approves
      nothing for another (AC-B11, part A D-8).
- **Part A sentences Part B supersedes** *(Revision B1)*. Part A §3's last bullet
  ("Users of the user-key providers (OpenAI, Anthropic, Ollama) see no change from
  this spec") and part A §6's bullet saying OpenAI, Anthropic and Ollama behave
  exactly as today describe part A alone. Once Part B ships they no longer hold;
  §B3 and §B4 govern those providers. Part A's criteria are unaffected.
- **Part A, unchanged**: every AC-1 to AC-20, `e2e/tests/llm-destination.spec.ts`,
  and the probe's documented 422 shape. Part B adds reasons or properties to that
  shape only by extension; it does not change the meaning of an existing reason.
- **The probe body still cannot carry a destination** and no response echoes a
  key: `TestLLMTest_BodyRulesAndNoKeyEcho` (`backend/api/pac24_scope_test.go:70-124`).
- **Settings writes still validate only what the request changes** (PAC-24 AC-17;
  AC-B14).
- **Parse and brief-refresh response shapes**, read by the web natural-language bar
  and by the MCP tool `clockwise_parse_command` (`mcp/tools.go:195-203`).
- **The key-handling e2e journey** `e2e/tests/settings.spec.ts:40-60`.

## B7. Rollback

No migration; no stored value is rewritten or deleted (D-B4). Reverting restores
today's behaviour and today's request-forgery channel. The new operator
configuration becomes unused.

**Upgrade note:** on upgrade, a self-hosted deployment whose users use Ollama, or
an OpenAI-compatible gateway or Anthropic proxy, stops sending to it until the
operator approves that destination. Users with an empty OpenAI or Anthropic base
URL are unaffected. A stored base URL that carries a path, query or fragment
(for example a gateway prefix) is refused until it is cleared or, if OQ-B6 allows
it, the operator approves the prefix *(Revision B1)*. Release notes and
`.env.example` must say so; `.env.example`
must also stop saying these providers need no server configuration, and so must
the comments in `docker-compose.yml:50-52` ("configured per-user in the Settings
page") and `docker-compose.prod.yml:89-91` ("no server-level env vars needed")
*(Revision B2)*.

**Default accounts** *(Revision B2)*: every account still on the stored defaults
(provider Ollama, Ollama base URL `http://localhost:11434`, §B2.1) changes, on a
deployment that approves no Ollama destination, from a connection failure to
"Ollama is not enabled on this deployment" on parse and brief refresh. No stored
value changes; picking another provider in settings removes the message. The
release note must say this.

## B8. Open questions

- **OQ-B1 — Ollama: keep for self-hosted, or drop?** Assumed: keep, behind
  operator approval, fail closed (D-B1). Dropping it would remove a provider value
  users may have stored and a labelled option in the web app, for no extra safety
  over "not enabled unless approved". Owner: product owner. Resolve: confirm D-B1
  on PAC-66-B before stage 2.
- **OQ-B2 — Should the client be told which providers are enabled?** With D-B1 the
  hosted product's settings page still offers "Ollama (local)", which can only
  fail. Owner: product owner, then contract-author. Resolve: a follow-up issue.
- **OQ-B3 — Should an approved host also be required to resolve to a public
  address?** Not required here (§B5). For OpenAI's and Anthropic's built-in hosts
  this would be a cheap extra guard; for Ollama it would forbid the feature.
  Owner: contract-author with the operator.
- **OQ-B4 — May an operator remove OpenAI's or Anthropic's public host from the
  approved set** (for example to forbid sending calendar text to a vendor)?
  Assumed no, as today (D-B2): it is a new product capability, not a fix. Owner:
  product owner.
- **OQ-B5 — Write-time rule for the shared base URL.** AC-B3 refuses a value
  approved for no provider that reads it. A stricter rule (approved for the
  selected provider) would refuse a user who saves the Ollama address while
  OpenAI is selected, which the web app's form allows. Assumed the looser
  write rule with the per-provider send-time rule (D-B5). Owner: contract-author.
- **OQ-B6 — Operator entry syntax for these providers, and whether it may carry a
  path prefix.** *(Revision B1; challenge NB-5.)* AC-B9 needs an Ollama entry with
  scheme and port (`http://host:11434`), which part A's D-10 syntax (bare host or
  `https://<host>`) does not express, and AC-B15's "malformed" depends on the
  syntax. An OpenAI-compatible gateway that serves the chat operation under a
  path prefix cannot be used under D-B8 unless the operator's entry may name that
  prefix. Assumed: no prefix (D-B8), entry syntax decided by the contract stage
  and listed in the feature manifest, not silently copied from D-10. Owner:
  contract-author, then product owner for the prefix.
  *(Revision B2, challenge round 2 NB-5)* The answer must also say which
  spellings of an approved destination, including OpenAI's and Anthropic's public
  hosts, count as that destination: an explicit default port (`:443`, `:80`), an
  upper-case host, and a trailing-dot host. AC-B5, AC-B6 and AC-B15 depend on it.
  Until it is answered the conservative reading applies: a spelling that is not
  the canonical one is refused, never approved.
- **OQ-B7 — The web app's Ollama field writes a property Ollama does not use.**
  *(Revision B2.)* On every account still holding the stored Ollama default
  (§B2.1), the web field is ignored for Ollama, and an Ollama refusal names the
  Ollama-specific base URL, which the web app can neither show nor clear. This is
  today's behaviour, unchanged by this part (D-B9). Fixing it means either the web
  app showing and writing the Ollama-specific property (a web change; the
  contract already exposes it) or changing the fallback order or the stored
  default (a stored-data or behaviour change). Owner: product owner, then
  contract-author. Resolve: a follow-up issue filed beside PAC-66-B and OQ-B2.

## B. Decisions

*None of these has a recorded owner answer. Each is the most conservative option
consistent with PRODUCT.md ("Delivered as hosted SaaS"; "Privacy is not a setting
to trade away") and with part A's D-1 to D-13.*

- **D-B1 — Ollama is kept for self-hosted deployments only.** It is sent to only
  if the operator approved that exact destination for Ollama; nothing approved
  means Ollama is not enabled (fail closed, as D-1). The operator may approve an
  insecure-transport destination and a non-default port for Ollama only, because
  Ollama carries no key; no user can. No stored Ollama value is deleted. *Assumed
  by the factory, pending owner confirmation.*
- **D-B2 — OpenAI's and Anthropic's public hosts are approved by the product**,
  each for its own vendor only, over secure transport on the default port. These
  are exact hosts the vendor operates, not registrable suffixes, so D-2's
  objection to built-in vendor suffixes does not apply. This keeps every
  configuration that works today with an empty base URL working, and sends a key
  to no recipient it does not reach today. *Assumed by the factory, pending owner
  confirmation.*
- **D-B3 — Any other OpenAI or Anthropic destination is approved by the operator,
  per provider, by exact host over secure transport**, as D-2, D-8 and D-9.
  *Assumed by the factory, pending owner confirmation.*
- **D-B4 — Refuse, never rewrite.** Stored base URLs are neither cleared, split nor
  migrated; they are refused at send time and the user can correct or clear them
  (as D-3). *Assumed by the factory, pending owner confirmation.*
- **D-B5 — The shared base URL is checked on write against every provider that
  reads it, and on send against the provider in use** (OQ-B5). Approval never
  transfers between providers, so no option widens who receives the user's key
  (AC-B11). *Assumed by the factory, pending owner confirmation.*
- **D-B6 — Redirects are never followed for OpenAI, Anthropic and Ollama**
  *(Revision B2, narrowed from "not followed to a destination not approved for the
  provider")*, whatever the status, target, method change or query; the request
  counts as failed, and a user's key is never re-sent (AC-B7). Chosen over
  following redirects between approved destinations because the vendors' chat
  operations do not redirect, a followed POST may become a GET on another
  operation, and "follow, but strip the key" is a rule the plan would have to get
  right per header. *Assumed by the factory, pending owner confirmation.*
- **D-B7 — Refusals reuse part A's per-path shapes (D-5)**, with a reason that
  says which case applies. The contract stage decides whether that is a new reason
  value or an existing one; an existing reason's meaning does not change.
  *Assumed by the factory, pending owner confirmation.*
- **D-B8 — Approval covers one operation, not the whole destination.** *(Revision
  B1.)* A request to an approved OpenAI, Anthropic or Ollama destination goes only
  to that provider's chat inference operation, with no query. A base URL carrying
  a path (other than empty or a single slash), a query, a fragment or user
  information is refused on write and at send time; a redirect to another
  operation on the same destination is not followed (AC-B7, AC-B18, AC-B19).
  Chosen over keeping path steering out of scope because §B1 item 1 names blind
  POSTs to the operator's infrastructure as the harm, and on a self-hosted Ollama
  that harm is exactly a request to a non-chat operation. Cost: a gateway that
  needs a path prefix stops working until OQ-B6 is answered; stated in §B7.
  *Assumed by the factory, pending owner confirmation.*
- **D-B9 — The Ollama fallback order and the stored Ollama default are kept, and
  the web field's lack of effect on Ollama is accepted as friction.** *(Revision
  B2.)* The Ollama-specific base URL still wins over the shared one, and no stored
  `http://localhost:11434` is cleared or rewritten (as D-B4). Consequence, stated
  plainly: on a deployment that approves an Ollama destination other than the
  local default, a user who enters that destination in the web app's field gets a
  successful save but a refused probe that names a property the web app cannot
  show or clear; D-B1's journey then works through the API, or for an operator
  who approves the local default, until OQ-B7 is resolved. Chosen over changing
  the fallback order because that would make the shared base URL, which OpenAI
  and Anthropic also read, the Ollama destination on every account, and over
  clearing the stored default because that rewrites user data. Neither
  alternative is safer, and both change behaviour this part does not need to
  change. *Assumed by the factory, pending owner confirmation.*

## Challenge (Part B) — 2026-10-26

**Verdict**: accept-with-changes

Reviewed Part B on `factory/new-pac66b-llm-base-url-spec` at 45bfc7b (committed
state only), against the PAC-66 Linear issue text, the code on that commit,
`smart-calendar-flow` at d9e0786 (origin/main), the contract fragments and
`docs/factory/api-audit.md`. The diagnosis holds. The following all check out:
the three user-key and keyless branches (`llm_factory.go:29,38,40-46`), their
verbatim use of the base URL (`parser.go:33-36,66-69,102-105`), the credential
headers (`parser.go:45,80`), `postJSON` on `http.DefaultClient`
(`parser.go:126-128`, `Do` at `:144`), the three send paths and their
`CheckDestination` calls, which return nil for these providers
(`handlers_llm.go:75,85,89`, `parser.go:193,253`,
`handlers_meeting_briefs.go:76,80`, `destination.go:163-176`), and the settings
write, which checks neither base URL (`handlers_settings.go:206-282`). I re-ran the
redirect claim with Go 1.25.5 (A on 127.0.0.1 answering 307 to B on localhost).
B received `x-api-key: SENTINEL` and an empty `Authorization`. The split also holds
against the issue's third draft acceptance, which asks to refuse `http://`,
loopback and private ranges for `ollamaBaseUrl`/`llmBaseUrl`. Per-provider
operator approval is stricter than that for OpenAI and Anthropic. For Ollama it is
the only option that keeps the feature, and D-B1 records that.

### Blocking

1. **An approved destination still lets the user choose the path, and for
   Ollama that path choice belongs to this channel, not only to the ambient
   credential as §B5 says.** §B4 approves by scheme, host and port only. §B5 hands
   path steering to part A's follow-up because it concerns "the ambient
   credential, not this channel". The code contradicts that. Each client builds its
   URL by string concatenation: `base+"/api/chat"` (`OllamaClient.Complete`,
   `parser.go`), and `base+"/v1/chat/completions"` or `base+"/v1/messages"` for the
   other two. The user writes both the base URL and the model
   (`ollamaModel`/`llmModel`). I checked this with Go's `net/http` on 2026-10-26.
   The base `http://ollama:11434/api/pull?x=` produces a POST to host
   `ollama:11434`, path `/api/pull`, with a body carrying `"model": <the user's
   model>` and `"stream": false`. That is Ollama's model-download operation. D-B1
   keeps Ollama only on self-hosted deployments whose operator approved an Ollama
   host. On exactly those deployments, any user can make the operator's Ollama
   download any model they name. That fills the operator's disk, and the
   downloaded model then answers that user's later chats. Every criterion still
   passes, because host, scheme and port are the approved ones. B1 item 1 names
   this kind of request ("blind POST requests" to the operator's
   infrastructure), so the spec wrongly permits a request that its own problem
   statement describes. *Resolve*: one of the following.
   (a) Add the requirement, with a criterion per provider (at least Ollama), that
   a request to an approved destination reaches only that provider's inference
   operation. A stored base URL whose path or query would change the operation is
   then refused on write and at send time.
   (b) Keep path steering out of scope, and correct §B5 to say it applies to this
   channel. Record that as a Part B decision with an owner, and file it as a
   follow-up beside `approvedHostPathSteering` that names the Ollama case.

2. **§B6 tells the plan to keep test assertions in a way D-B3 forbids, and it
   misses tests whose assertion Part B reverses.** §B6 says the six
   `parser_test.go` tests "keep their assertions ... the plan must make them
   approve their test server's destination for the provider under test, not
   delete or skip them". Those servers are `httptest.NewServer`, which listens on
   `http://127.0.0.1:<random port>`. For Ollama that can be approved (D-B1). For
   OpenAI and Anthropic it cannot: D-B3 and AC-B6 approve only an exact host over
   secure transport on the default port. So `TestOpenAIClient_Complete` (:162) and
   `TestAnthropicClient_Complete` (:183) cannot keep their assertions the way §B6
   prescribes. A bypass is not available either, because §B3 requires the check
   "at the moment of sending". §B6 also omits several tests that point a user-key
   or keyless client at an `httptest` server, or that assert today's behaviour:
   - `backend/nlp/destination_test.go:251-262`
     `TestUserKeyProviders_AreNotChecked` asserts that `CheckDestination` returns
     nil for `openai` at `http://10.0.0.1`, `anthropic` at
     `http://attacker.example`, and `ollama` with nothing stored. AC-B1, AC-B2 and
     AC-B8 require the opposite.
   - `backend/api/pac66_llm_destination_test.go`
     `TestPAC66_UserKeyAndKeylessProbesUnchanged` probes Anthropic and Ollama at
     `http://127.0.0.1:<port>` and expects 200.
   - `backend/api/pac24_scope_test.go:70-124` `TestLLMTest_BodyRulesAndNoKeyEcho`
     is listed in §B6 only for the probe-body rules. It also stores an
     `http://127.0.0.1` OpenAI base URL (:86) and asserts a **502** after the
     provider is contacted (:112-118). Under AC-B1 that response becomes the 422
     refusal.
   - `parser_test.go` `TestNLPService_Parse_ScheduleMeeting` (:317),
     `TestNLPService_Parse_InvalidJSON` (:351) and
     `TestParse_AuditEntrySubjectIsCallerFromContext` (:395). Each stores an
     `httptest` URL as `LLMBaseURL`.

   The factory forbids deleting or skipping a test to go green, so an implementer
   who follows §B6 has no allowed path. *Resolve* in §B6:
   (i) List every test above.
   (ii) For the tests whose assertion Part B reverses
   (`TestUserKeyProviders_AreNotChecked`, the 502 half of
   `TestLLMTest_BodyRulesAndNoKeyEcho`, the "unchanged" probes), state that the
   named Part B criteria supersede them. Changing them is then authorised and is
   not a skipped test.
   (iii) For the tests that keep their assertion, state a Given under which their
   destination counts as approved without contradicting D-B3. Otherwise state that
   D-B3 has a test-only exception and what that exception is.

### Non-blocking

1. **"Effective destination" is defined from the base URL, not from the request
   actually sent.** Part A defined the effective host as the host of "the
   inference request that would actually be sent". §B4 defines it as "the scheme,
   host and port of the base URL". The two differ for a base URL with an empty
   authority. I checked this with Go on 2026-10-26. The base `http:/` produces a
   request to host `api` for Ollama (`/api/chat`) or `v1` for OpenAI
   (`/v1/chat/completions`), while the base URL's own host is empty. Exact
   matching refuses it anyway. A reader could still take "when it is empty" to
   cover an empty host, apply the public-host default, and send to `v1` or `api`.
   Use part A's definition.
2. **AC-B17's OpenAI half has no key in its Given.** Without a stored or
   submitted key, `NewLLMClientFromSettings` fails before any check
   (`llm_factory.go:22-24`, `handlers_llm.go:75-79`). Today that gives a 500, not
   the "502 after the server contacted the address" that the note claims. Add
   "with an OpenAI key on file" to the Given. Also state which outcome wins when
   both "no key" and "destination not approved" apply. The spec currently orders
   neither.
3. **The web app's obvious path cannot clear a stale shared base URL.** Take B1
   item 3's user, who once used Ollama and now uses OpenAI. AC-B11 refuses that
   user with an error naming the base URL. The web app shows that field only while
   Ollama is selected (`smart-calendar-flow/src/pages/Settings.tsx:741-752`). To
   recover, the user has to switch to Ollama, clear the field, save, and switch
   back. §B5 rules out web changes, so the refusal sentence is the only guidance.
   Either require that the sentence says how to clear the field, or accept the
   friction explicitly.
4. **Part B overrides statements in part A that are still in the file.** Part A
   §3 (last bullet) and §6 ("OpenAI, Anthropic and Ollama behave exactly as today,
   including a user-supplied OpenAI-compatible base URL and a local Ollama URL")
   become false once Part B ships. §B6 says only that part A's ACs are unchanged.
   Say which part A sentences Part B supersedes.
5. **The operator entry syntax for Ollama is undefined.** D-10 allows a bare host
   or `https://<host>`. AC-B9 needs `http://host:port`, and whether an entry is
   "malformed" under AC-B15 depends on the syntax. Settling it belongs to the
   contract stage, but list it as an open question so the contract does not
   silently reuse D-10.
6. **§B2.6 overstates.** "No test records where a request from these three
   branches goes" is too strong. The tests in Blocking 2 point these clients at
   `httptest` servers and assert on what arrives there. The narrower claim is
   true: no test runs them through part A's recording transport, and none covers
   a redirect.
7. **Citation location.** The Decisions preamble quotes PRODUCT.md. That file is
   `smart-calendar-flow/PRODUCT.md` (:34, :90), not this repo. The quotes are
   correct.

### Criteria I could not falsify

- AC-B13: a process check in Linear, not a test. It is acceptable as the manual
  gate D-4 asks for, but no test distinguishes pass from fail.
- AC-B15 for Ollama, until the entry syntax is defined (Non-blocking 5). Without
  the syntax, no test can say whether `http://host:11434` is malformed.

### Citations I checked and found wrong

- §B5: path steering "concern[s] the ambient credential, not this channel".
  The code says otherwise. User-key and keyless URLs are built by concatenation
  from a base URL and a model that the user writes (the OpenAI, Anthropic and
  Ollama `Complete` methods in `parser.go`), so path steering applies to this
  channel (Blocking 1).
- §B6: `TestLLMTest_BodyRulesAndNoKeyEcho` is cited only for the probe-body
  rules. It also asserts a 502 after contacting an `http://127.0.0.1` OpenAI base
  URL (`pac24_scope_test.go:86,112-118`), which Part B changes (Blocking 2).
- §B2.6: "No test records where a request from these three branches goes" is
  overstated (Non-blocking 6).
- All other citations I checked are correct: `llm_factory.go:13-58,29,38,40-46`,
  `parser.go:33-36,45,66-69,80,102-105,126-128,144,193,253`,
  `handlers_llm.go:75,85,89,106-110`, `handlers_meeting_briefs.go:76,80`,
  `destination.go:166-176`, `handlers_settings.go:206-282,257,263-271`,
  `server_errors.go:53-56`, `scheduling.yaml:522-528,1943-1945,1997-1999` and the
  `x-uncertain` lines :246, :376, :1046 and :1327, `api-audit.md:283,324`,
  `contracts/features/PAC-66.yaml:135-136`, `.env.example:50`,
  `docker-compose.yml:56-58`, `Settings.tsx:62,741-752`, `client.ts:594`,
  `parser_test.go:104,118,143,162,183,204,230,244,282` and
  `mcp/tools.go:195-203`. `e2e/` has no base-URL test.

### Consumers this breaks

- In `clockwise-like`: `backend/nlp/destination_test.go:251-262`,
  `backend/api/pac66_llm_destination_test.go`
  (`TestPAC66_UserKeyAndKeylessProbesUnchanged`),
  `backend/api/pac24_scope_test.go:86,112-118`, and the `parser_test.go` tests at
  :162, :183, :204, :230, :244, :282, :317, :351 and :395. All of them point
  user-key or keyless clients at `http://127.0.0.1` (Blocking 2).
- `smart-calendar-flow`: nothing breaks at runtime. The probe's 422 body is not
  parsed at runtime: `src/api/client.ts:1294-1302` reads only the success body,
  and `withFallback` rethrows non-network errors (`:491-504`). The generated
  `LLMDestinationRefusal` zod schema is a `strictObject` with a closed `property`
  enum (`src/api/generated/schemas.ts:215`). If Part B adds `llmBaseUrl` or
  `ollamaBaseUrl` to that enum, the web types must be regenerated in the usual
  order (factory §1).
- Self-hosted operators who use Ollama, an OpenAI-compatible gateway or an
  Anthropic proxy lose it on upgrade until they approve the destination. This is
  by design and stated in §B7. On the hosted product, every user with Ollama
  stored loses it (D-B1). No stored value is deleted.

## Revision B1 — 2026-10-26

Answers to the Part B challenge above. Part B's sections are changed in place;
changed text is marked *(Revision B1)*.

### Blocking

1. **Path steering on an approved destination is this channel's problem too.**
   Accepted, resolved by option (a). The challenger is right that §B5 was wrong:
   each client appends its operation path to the user's base URL as a string
   (`parser.go:44`, `:78`, `:114`, now cited in §B2.6), so a user who may reach an
   approved Ollama destination could also reach its model-download operation with
   a model of their choosing. Changes: §B3 has a new bullet (an approved
   destination is reached only for the provider's chat inference operation; a
   base URL with a path other than empty or `/`, a query, a fragment or user
   information is refused on save and at send time). New AC-B18 (Ollama: write
   refused, stored value refused on every path, the approved destination alone
   sends exactly one request whose path is the chat operation's and has no query)
   and AC-B19 (the same for OpenAI and Anthropic, including their public hosts).
   AC-B7 now also covers a redirect to another operation on the same destination.
   Recorded as D-B8, with its cost (a gateway needing a path prefix stops working)
   in §B7 and the prefix question as OQ-B6. §B5 is corrected: path steering is in
   scope for these three providers; only the ambient case and Bedrock profile
   selection stay as part-A follow-ups. Option (b) was rejected because it would
   leave open the harm §B1 item 1 names, on exactly the deployments D-B1 keeps
   Ollama for.
2. **§B6 prescribed a Given that D-B3 forbids, and missed tests Part B reverses.**
   Accepted. §B6's test bullet is replaced. Every test the challenger named is
   listed, with `parser_test.go:317`, `:351` and `:395` confirmed as Ollama tests
   (each stores the test server as an Ollama `LLMBaseURL`). Four groups:
   (i) the five Ollama tests keep their assertions by approving the test server's
   destination for Ollama, which D-B1 allows; (ii) `TestOpenAIClient_Complete` and
   `TestAnthropicClient_Complete` keep their assertions with the base URL empty or
   the vendor's public host and a test transport that delivers to the in-process
   server, the technique part A's recording transport already uses, so the rule
   sees an approved destination and **no test-only exception exists**;
   (iii) the two `postJSON` tests keep their assertions under whichever Given fits
   where the plan places the rule; (iv) `TestUserKeyProviders_AreNotChecked`, the
   Anthropic case of `TestPAC66_UserKeyAndKeylessProbesUnchanged` and the 502
   expectation in `TestLLMTest_BodyRulesAndNoKeyEcho` are superseded by the named
   criteria (AC-B1, AC-B2, AC-B6, AC-B8, AC-B10), which authorises changing those
   assertions; the 502-with-no-key-echo property is kept under the public-host
   Given. §B2.6 lists the same tests.

### Non-blocking, also taken

- NB-1: "Effective destination" now uses part A's definition (the request that
  would actually be sent), and says a non-empty base URL with an empty host is
  refused, never defaulted to the public host.
- NB-2: AC-B17 now has an OpenAI key on file. §B4 states that a destination
  refusal wins over a missing key, as part A §4 does for the credential.
- NB-5: operator entry syntax is OQ-B6, owned by the contract stage.
- NB-4: §B6 now names the part A sentences (§3 last bullet, §6 user-key bullet)
  that Part B supersedes.
- NB-6: §B2.6's overstatement corrected.

### Non-blocking, not taken in this revision

- NB-3 (recovering a stale shared base URL from the web app): accepted as
  friction for now; the refusal sentence must still name the property (AC-B1,
  AC-B11), and the web change waits for OQ-B2.
- NB-7 (PRODUCT.md location): the file is `smart-calendar-flow/PRODUCT.md`
  (:34, :90); the quotes stand.

## Challenge (Part B, round 2) — 2026-10-26

**Verdict**: accept-with-changes

Reviewed Part B on `factory/new-pac66b-llm-base-url-spec` at 0d67c86 (committed
state only), against the code at c954521 (= `origin/main`), `smart-calendar-flow`
at d9e0786 (`origin/main`), the contract fragments and the earlier Part B
challenge. Revision B1 resolves both round-1 blockers. Path steering is now a
requirement with falsifiable criteria (AC-B18, AC-B19, D-B8). §B6 now puts every
test that round 1 named into one of four groups, and the public-host Given for
`TestOpenAIClient_Complete`/`TestAnthropicClient_Complete` gives them a test-only
route that D-B3 does not forbid. The new citations check out: `parser.go:44,78,114`
(string concatenation), `parser_test.go:317,351,395` (all three store the test
server as an Ollama `LLMBaseURL`), `destination_test.go:252-262`,
`pac66_llm_destination_test.go:448-475` and `pac24_scope_test.go:86,112-118`.
One new blocking finding comes from something both earlier rounds missed: the
stored defaults of a settings row.

### Blocking

1. **The spec assumes an untouched account has empty Ollama settings. On every
   real account the Ollama address is stored as `http://localhost:11434` and the
   provider is stored as `ollama`. Because of that, AC-B17's Ollama half does not
   test what it says, and its "fails today" note is wrong.**
   `002_llm_providers.up.sql:10` adds `ollama_base_url ... DEFAULT
   'http://localhost:11434'`, and `:13-14` makes `llm_provider` default to
   `'ollama'` (and rewrote existing empty values to it). Settings rows are created
   by `INSERT INTO settings (user_id)` (`storage/settings.go:246`,
   `storage/personal_calendars.go:165`, `auth/microsoft_oauth.go:65`), so they take
   these defaults. I checked this on 2026-10-26 by applying the migrations to a
   scratch `postgres:16-alpine` and inserting a row that way. It read back
   `ollama | [] | http://localhost:11434 | llama3`. The factory prefers the Ollama
   base URL over the shared one (`llm_factory.go:40-46`), so on such a row the
   shared base URL is never Ollama's destination.
   - AC-B17 creates its user the normal way (e2e tests insert into `users` only,
     e.g. `e2e/tests/settings.spec.ts:19`, and the settings row comes from
     `GetOrCreateSettingsByUser`). It then forces the **shared** base URL to the
     database's address and probes with Ollama. That probe's effective
     destination is `http://localhost:11434`, not the forced address. Today the
     server contacts its own loopback, not "the address" as the note claims.
     After the change it refuses because nothing is approved for Ollama, whatever
     address is stored. So the Ollama half proves nothing about a shared base URL
     that points at an internal service.
   - AC-B9 says "a user saves exactly that destination" without naming the
     property. Through the web app's only field (the shared base URL,
     `Settings.tsx:741-752`, `client.ts:594`), the save is accepted under AC-B3,
     but the probe still goes to the stored `http://localhost:11434` and is
     refused. The criterion passes only if the test writes `ollamaBaseUrl`
     through the API. D-B1's journey ("the operator approves an Ollama host,
     users use it") then holds only for API users, or for an operator who approves
     `http://localhost:11434`. The behaviour is not new: today the web value is
     ignored in the same way. But the spec gives D-B1's web journey as the reason
     for keeping Ollama and does not say it does not work.
   - AC-B10's Given ("both base URLs empty") is not the state an account starts
     in. The effective destination is the same, so the criterion stays valid, but
     the default case is a stored `http://localhost:11434`.

   *Resolve*:
   (i) State the stored defaults in §B2, citing the migration lines.
   (ii) Rewrite AC-B17's Ollama half so its Given makes the forced value Ollama's
   effective destination. For example, force the Ollama base URL, or force both
   properties. Then correct its "fails today" note.
   (iii) Name the property AC-B9 saves.
   (iv) Say plainly that on a deployment that approves an Ollama host, a value
   entered in the web app's field is not used for Ollama while the stored Ollama
   base URL is non-empty, and that the refusal then names a property the web app
   cannot show or clear. Either accept that friction in Decisions, or record it as
   a follow-up beside OQ-B2.

### Non-blocking

1. **The refusal becomes the default experience, and §B1/§B7 do not say so.**
   Every account that never opened the LLM settings is an Ollama account with a
   localhost address (Blocking 1). Today each natural-language parse and each
   brief refresh by such an account makes the server POST to its own
   `localhost:11434`, which is part of §B1 item 1 by default, not by the user's
   choice. After the change, on the hosted product and on any deployment that
   approves no Ollama host, every such account is told "Ollama is not enabled on
   this deployment" by the natural-language bar and by every refreshed brief.
   Neither §B1's "Who is affected" nor §B7's upgrade note says this, and it makes
   OQ-B2 more urgent than it reads.
2. **A test that asserts "a user-key provider has no approved set" is not in
   §B6.** `backend/config/llm_hosts_test.go:87-89`
   (`TestDecideLLMApprovedHosts_PerProvider`) fails with exactly that message if
   `openai` is enabled or has an approved host. D-B2 and D-B3 give OpenAI and
   Anthropic approved sets. Whether this assertion still holds depends on where
   the plan keeps those sets. List it in §B6 as superseded (by D-B2/D-B3) or as
   kept, so the plan has authority either way.
3. **The redirect rule leaves three cases open.** D-B8 allows only the chat
   operation with no query. AC-B7 lists a different path but not (a) the same
   chat path with a query added, or (b) a redirect that changes the method (Go
   turns a POST into a GET on 301/302/303). §B3 says a redirect "never takes a
   user's key to any other host" but does not say (c) whether a redirect between
   two destinations that are both approved for the same provider is followed
   without the key, or not followed at all. Each case is untestable until the
   spec decides it.
4. **The upgrade note names only `.env.example`.** `docker-compose.yml:50-52` and
   `docker-compose.prod.yml:89-91` say the same thing ("configured per-user in
   the Settings page ... no server-level env vars needed"), and §B7 should name
   them too.
5. **Which spellings of a public host count as approved is not stated.** AC-B6
   refuses a "non-default port". It does not say whether an explicit `:443`, an
   upper-case host or a trailing-dot host counts as the approved public host.
   OQ-B6 can settle this, but the question should name these spellings, because
   AC-B5 and AC-B15 depend on the answer.

### Criteria I could not falsify

- AC-B13: a manual Linear check, as round 1 said.
- AC-B15 for Ollama, until OQ-B6 fixes the entry syntax.
- AC-B17's Ollama half as written: it gives the same result whether or not a
  shared base URL that points at an internal service is checked (Blocking 1).

### Citations I checked and found wrong

- AC-B17 note: "both probes return 502 after the server contacted the
  address". For the Ollama probe the server contacts the stored
  `http://localhost:11434` (`002_llm_providers.up.sql:10`, `llm_factory.go:40-46`),
  not the forced shared address.
- §B6: `TestBuildLLMClient_DefaultModels` (`parser_test.go:143`) is cited as
  protecting OpenAI and Anthropic with no base URL. It builds an Ollama client
  only and asserts its default model (`llama3.2`). The protection comes from
  AC-B5, as §B6 itself goes on to say.
- Everything else I re-checked is correct: `llm_factory.go:13-58`,
  `parser.go:33-36,44,66-69,78,102-105,114,126-128,193,253`,
  `handlers_llm.go:75,85,89`, `handlers_meeting_briefs.go:76,80`,
  `destination.go:163-176`, `handlers_settings.go:206-282` (changed-value
  validation at `:163-202`), `server_errors.go:50-56`,
  `scheduling.yaml:522-528,1943-1945,1997-1999` and the `x-uncertain` lines
  :246, :376, :1046 and :1327, `api-audit.md:283,324`,
  `contracts/features/PAC-66.yaml:135-136`, `.env.example:50`,
  `docker-compose.yml:56-58`. No other send path exists: `Complete` is called
  only at `handlers_llm.go:89`, `parser.go:210,240` (parse sends twice, attendee
  extraction then the main prompt, both after the check at `:193`) and
  `meeting_brief_service.go:87`. `mcp/` builds no LLM client.

### Consumers this breaks

- `clockwise-like/backend/config/llm_hosts_test.go:87-89`: possibly, depending on
  where the plan keeps the approved sets for OpenAI and Anthropic
  (Non-blocking 2).
- Every account still on the stored defaults: parse and brief refresh change from
  a connection failure to "Ollama is not enabled on this deployment" on any
  deployment that approves no Ollama host (Non-blocking 1). No stored value is
  changed.
- `smart-calendar-flow`: nothing breaks at runtime. `llmTest` reads only the
  success body (`src/api/client.ts:1293-1302`). The web app never sends
  `ollamaBaseUrl`, which is why its Ollama field has no effect (Blocking 1).
  If the refusal's `property` enum gains a value, the generated
  `LLMDestinationRefusal` schema must be regenerated in the usual order.
- The tests §B6 lists, as §B6 says.

## Revision B2 — 2026-10-26

Answers to the Part B round-2 challenge above. Part B's sections are changed in
place; changed text is marked *(Revision B2)*. Every citation below was re-read
on c954521 before writing it.

### Blocking

1. **An untouched account does not have empty Ollama settings.** Accepted;
   confirmed in the code: `002_llm_providers.up.sql:10` defaults the Ollama base
   URL to `http://localhost:11434`, `:13-14` default the provider to `ollama`, and
   the three `INSERT INTO settings (user_id)` sites (`storage/settings.go:246`,
   `storage/personal_calendars.go:165`, `auth/microsoft_oauth.go:65`) name no
   other column. The factory's preference for the Ollama base URL is at
   `llm_factory.go:40-42`. (The scratch-postgres read-back is the challenger's; I
   did not repeat it.)
   - (i) §B2.1 now states the stored defaults with those citations, and that on
     such a row the shared base URL is never Ollama's destination. §B2.2 adds that
     the web app never writes the Ollama-specific property, so its Ollama field has
     no effect today. §B4's effective-destination definition says an account
     starts from the stored default, not from "both empty".
   - (ii) AC-B17 now forces **both** stored base URLs to the internal address, so
     the forced value is the effective destination of the Ollama probe as well as
     the OpenAI one. Its "fails today" note is corrected: each probe is sent to the
     forced address (Ollama through its own property, OpenAI through the shared
     one). It also says what the e2e test can and cannot observe; zero requests is
     proven at unit level.
   - (iii) AC-B9 names the property: the Ollama-specific base URL, written through
     the API. A second Given pins the web-app case: saving the approved
     destination as the shared base URL is accepted, but with the stored default
     in place the probe is decided on `http://localhost:11434` and is refused
     (naming the Ollama-specific base URL) unless the operator also approved the
     local default.
   - (iv) Stated plainly and accepted as friction in new D-B9, with the reason
     (changing the fallback order would make a value OpenAI and Anthropic also
     read into the Ollama destination on every account; clearing the stored
     default rewrites user data). Filed as follow-up OQ-B7 beside OQ-B2, and §B5's
     web bullet points at it. AC-B10's Given now is the account's starting state,
     with "both empty" kept as the second, equivalent case.

### Non-blocking, also taken

- NB-1: §B1 now says every untouched account is affected by default, and that
  after the change such accounts see "Ollama is not enabled" on parse and brief
  refresh where no Ollama destination is approved. §B7 adds the same as an upgrade
  note for the release notes.
- NB-2: `TestDecideLLMApprovedHosts_PerProvider` (`config/llm_hosts_test.go:87-89`)
  is listed in §B6 group (iv): kept if the plan stores the new approved sets
  apart from part A's, superseded by D-B2/D-B3 if it merges them; the
  per-provider property behind it holds either way.
- NB-3: the three open redirect cases are decided by narrowing D-B6: no redirect
  is followed for these providers, whatever the status, target, query or method
  change, including between two destinations approved for the same provider.
  AC-B7 lists all three cases and requires exactly one recorded request, a POST.
  §B3's redirect bullet says the same.
- NB-4: §B7 now names `docker-compose.yml:50-52` and `docker-compose.prod.yml:89-91`.
  (Checked: the dev file says "configured per-user in the Settings page"; the
  prod file adds "no server-level env vars needed".)
- NB-5: OQ-B6 now names the spellings (explicit default port, upper-case host,
  trailing-dot host) and states the conservative reading until it is answered:
  a non-canonical spelling is refused.
- Citation fix: §B6 no longer cites `TestBuildLLMClient_DefaultModels` as
  protecting OpenAI and Anthropic; it builds an Ollama client only.
