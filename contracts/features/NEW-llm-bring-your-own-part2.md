# Contract rationale — NEW-llm-bring-your-own, Part 2 (organisation-wide AI key)

- **Spec**: `docs/specs/NEW-llm-bring-your-own-part2.md` (revision 1, which
  answers spec challenge revision 0)
- **Contract revision**: 2 (2026-10-28), answering contract challenge
  revision 1 (recorded verbatim below) and the spec's revision 1. Sections 1
  to 5 are the live text; the "Revision 2" table at the end maps every
  finding to its resolution.
- **Manifest**: `contracts/features/NEW-llm-bring-your-own-part2.yaml`
- **Base**: `main` at `ae5c2cf` (Part 1 and PAC-50 merged)
- **Linear**: PAC-73

## 1. What the interface is

Three new operations, all behind the session, all on the caller's own
organisation (none takes an organisation id, so no request can name another
organisation, P2-AC-8):

| Operation | Who | What |
|---|---|---|
| `GET /api/organization/ai` (getOrganizationAI) | any member | the configuration without the key; identical bytes for every member |
| `PATCH /api/organization/ai` (patchOrganizationAI) | proven admin | merge change naming the version it was made from; PAC-50's gate with purpose `llm_org_key_write`; break-glass once per session |
| `POST /api/organization/ai/consent` (confirmOrganizationAI) | any member | "I was shown version N and I confirm" |

Changed: the three PAC-50 step-up operations gain an optional `purpose`; the
not-configured answer (`AINotConfigured`) gains rows 10 and 11 (row 9 is
reserved, revision 2), the reason
`personal_keys_forbidden` and the `organization_admin` enabler in practice;
`AIStatus` and `LLMTestResult` say whether the configuration is the caller's
own or the organisation's; the probe gets one new 400 and one new 409 case.

## 2. Why it is shaped this way

### 2.1 A purpose on the existing step-up, not a second step-up

PAC-50 already stores a purpose on each grant and consumes by purpose
(`030_sso_admin_stepup.up.sql:13-33`, `storage/sso_stepup.go:122-133`), and
its D-15(5) left "how a step-up for another purpose is started" to this
ticket. The only missing piece is choosing the purpose at the start. One
optional query parameter, bound into the signed state, does that.

**Rejected: a separate start/callback pair under `/api/organization/ai/stepup`.**
The callback URL is a redirect URI registered with Google and Microsoft by
each operator; a second one is a deployment change for every installation,
and two copies of the hd/tid/email/admin checks would drift. **Rejected:
spending the SSO grant.** PAC-50 AC-27 forbids it, and it would let anyone who
stepped up to fix SSO silently re-point every member's AI traffic.

The absent-purpose path is byte-identical to PAC-50 (including the callback's
Location), so PAC-50's web client and tests are untouched. The org-AI 403
carries `stepup_purpose` rather than putting the purpose inside `stepup_url`,
because `SsoStepupUrl` is a `const` path that clients resolve and append to;
changing it to carry a query would break that rule for PAC-50's clients. A
client that forgets the purpose gets an SSO grant, which the org-AI change
refuses without consuming: the failure is safe.

### 2.2 The 403s are PAC-50's shapes with their own `error` values

`OrganizationAIWriteForbiddenResponse` mirrors `SsoWriteForbiddenResponse`
(membership / step-up required / fresh login), reusing
`SsoWriteMembershipRequired` as is (one type for "no organisation", as PAC-50
N4 did for the step-up start) and `SsoStepupUrl`, `SsoStepupScopes`,
`SsoWithoutStepup` as components. The step-up and fresh-login variants get
their own `error` constants so a client rendering an AI-section refusal never
has to guess which write was refused. There is no domain-mismatch variant:
nothing in this body names a domain.

**Break-glass authorises one organisation-AI change per session** (spec
P2-D13, factory-assumed pending the owner). A further change with the same
session (same issue time) is the existing fresh-login 403, so no new error
value is needed and a client already knows to send the user through
sign-in. **Rejected: a new `organization_ai_write_breakglass_used` value**:
the remedy is the same as for a stale session, and a new value is a client
change. PAC-50's SSO gate is untouched.

**Rejected: one shared 403 schema for both writes** (`error` values from both
features in one enum). A generated SSO client would then accept AI refusals
as SSO refusals.

### 2.3 Every member reads the same bytes

Paceday stores no admin role, and PAC-50's proof exists only around a write,
so a read cannot honestly differ for an admin (parent D-7). The read therefore
depends only on the organisation's stored row, and per-caller state (have I
confirmed?) lives on the caller's settings read (`AIStatus`), not here. This
also makes P2-AC-7 ("byte-identical") a simple test. `changeableBy` is a
constant so the web can say who can change it without hard-coding text.

### 2.4 A configuration version, and confirmation naming it

O-13 requires that nothing is sent before the member confirms. A
confirmation that says only "yes" would race an admin change: the member
reads OpenAI, the admin switches to a self-hosted gateway, the member's "yes"
lands. So the read carries `configurationVersion`, the confirmation names the
version the member saw, and a stale one is a 409 carrying the current read
(nothing recorded). The version advances on provider, key or destination
changes (P2-D4): any of those may be a different provider account, which is
what the notice is about. It does not advance on a model change or on the
personal-keys switch, which change neither where data goes nor who can access
it.

**Rejected for now: re-asking on provider change only, or never.** The
owner said "tell and ask once" and did not say when to ask again; asking
again on a key or destination change widens that and is the factory's
default (spec P2-D4), pending the owner (spec OQ-P2-1). (Revision 1 of this
document called provider-only "the owner's example"; the owner gave no such
example, and the attribution is withdrawn.) A new key or gateway can be
another account with other administrators, which is exactly what the member
was told about. The cost is an extra confirmation after a key rotation.
Writing `apiKey` always advances the version, even when it equals the stored
key: the server never compares keys, and re-entering a key is a deliberate
act (challenge non-blocking 6).

**A confirmation covers exactly one version, and only the confirmation
operation records one** (spec P2-D14). Row 4 reads the configuration and the
confirmation as one consistent state and sends exactly the confirmed
version's values, else row 5. patchSettings never records a confirmation
(the web sends the whole settings document when its cache is empty, so
`llmProvider: organization` can arrive without the user having been shown
anything), and a patchSettings that stores `""` or a provider deletes the
confirmation, so a later forbid cannot route a member who left the
organisation's AI back to it without a notice.
**Rejected: a confirmation flag on `patchSettings`** (`llmProvider:
organization` + `confirmed: true`). It would tie confirmation to changing
one's choice, which revision 1's rows 9-10 could not use (members who never
changed their choice); and it would put a per-org-version fact into the
user's settings document. Under revision 2 a member does change their choice
to reach the organisation's AI, but the confirmation stays a separate
operation so that it names the version the member was shown (P2-AC-38).

### 2.5 Reasons: reuse `organization_unconfirmed`, add one

Part 1 reserved `organization_unconfirmed` and
`provider_removed_organization_available` precisely so a Part 1 client would
not break on them. "Consent required" is `organization_unconfirmed`; adding a
`consent_required` synonym would make one state carry two names. Only O-12's
case is new: `personal_keys_forbidden`, with `enablers` telling "confirm and
you are on" (`[user]`, row 10) from "wait for an admin" (`[organization_admin]`,
row 11 and the forbidden probe). **Rejected: two new reasons for rows 10 and
11**: `enablers` already separates them, and every extra enum value is a
client change.

### 2.6 The probe never picks the organisation's key by provider name

The parent spec let a follower's probe naming "exactly the organisation's
provider" test the organisation's configuration. That made the meaning of
`{"llmProvider":"openai"}` depend on what an admin last saved, and is the one
path by which a request-named value could select the organisation's key. Now:
naming a provider is always the caller's own configuration (D-19 with no
exception); `{}` or `organization` probes the organisation's, and a model or
key alongside it is a 400 rather than silently ignored (Part 1's rule that an
override must be refused, not dropped). While personal keys are forbidden,
naming a provider or sending a key is the 409 `personal_keys_forbidden`, so a
personal key cannot leave through "Test connection" either (O-12).

### 2.7 The change body makes the wrong change impossible to express

`OrganizationAIConfigurationUpdate` is `additionalProperties: false`,
`minProperties: 1`, and refuses (rather than ignores) a destination property
that does not apply to the provider. A provider change must carry a key, so a
key is never carried to another provider (D-5's rule, for the organisation);
`provider: ""` removes everything and must come alone. Body 400s happen before
the grant is consumed, so an admin who mistyped does not need a new step-up
(PAC-50's ordering). `model` and the destination properties with no provider
are refused (`<property> requires a provider`), and `model` with
`azure_openai` is refused, because the read never shows them there.

**The change names the version it was made from** (contract revision 2,
challenge B-1). Without it, an admin whose form was loaded at v5 (OpenAI)
could save `{"apiKey":"sk-proj-..."}` after another admin moved the
organisation to Anthropic (v6), and the OpenAI secret would be stored, and
sent, as Anthropic's key. `configurationVersion` is required on the body; a
mismatch is a 409 with the consent conflict's shape (`configuration_changed`
plus the current read), checked after the body 400s and before the proof, so
a stale form costs no step-up. **Rejected: an `If-Match` header with an
ETag.** It is the HTTP-native form, but the web client builds JSON bodies,
the generated strict wrapper surfaces headers less directly than body
fields, and confirmOrganizationAI already names the version in its body;
one mechanism for both is easier to get right. **Rejected: refusing only a
key-without-provider write after a provider change.** It closes the one
exploit the challenger named and leaves every other lost update (a model or
gateway typed for one provider landing on another).

**Rejected: per-provider key slots for the organisation**, as users have.
Each organisation change is proven and deliberate, and members are shown one
configuration; slots would add a hidden second key members cannot see.

### 2.7a Forbidding personal keys moves nobody (spec P2-D2, current default)

While personal keys are forbidden, a member on their own or a removed
provider answers row 10 (`personal_keys_forbidden`, `[user]`) or row 11
(`[organization_admin]`), and nothing is sent until they choose
`organization` and confirm. Row 9 ("configured: the organisation's" for such
a member) is kept in the table as reserved, so that if the owner answers
OQ-P2-2 the other way the row number, the reason set and the clients'
handling do not change; only which state reaches it does. Under this default
`AIStatus.configured` and `LLMTestResult.configuration: organization` occur
only in row 4. **Rejected for now: moving them after they confirm**
(revision 1's default): it moves a member who deliberately chose their own
provider, which is the owner's call.

### 2.7b No key leaves through provider error text (spec §3.1a)

A gateway may echo the key it was given in its error body, and that text
reaches the parse 200's `error`, a failed brief's stored `brief_text`, logs
and Sentry. The contract states, on parseNaturalLanguage, testLLMConnection
and refreshMeetingBrief, that every occurrence of the organisation's key and
the caller's keys is replaced by `[redacted]` before the text is stored,
returned, logged or reported. **Rejected: dropping provider error text
altogether** from the parse answer and brief text: it is the only diagnosis
a member gets for a misconfigured key or gateway, and Part 1 kept it.

### 2.7c A refused organisation destination says whose it is

`LLMDestinationRefusal` gains an optional `configuration` (`organization`
when the refused destination is the organisation's), mirroring
`LLMTestResult.configuration`, and its sentence then names the
organisation's administrator, never the member's own Settings. Optional, and
absent meaning "own", so Part 1's 422 is unchanged.

### 2.8 MCP gets nothing new

The confirmation is the person's (O-13). An MCP tool that confirms would let
an assistant send the user's data to the organisation's account on its own
initiative. MCP clients see the not-configured sentence ("confirm in
Paceday …") through the existing pass-through tools, and nothing is sent.

## 3. Generated code and the one code change on this branch

The three new operations are listed in `MIGRATION.md` as `handwritten` with a
note: they are not served yet, and stage 4 implements them on the generated
strict wrapper (factory README §4) and flips the rows. Marking them
`generated` now would claim an implementation that does not exist.

Adding `purpose` to `getSsoStepupOptions` (already on the generated wrapper)
changes the generated strict handler's signature. The branch passes empty
params in `backend/api/handlers_sso_stepup.go` `getStepupOptions`, so the
server keeps answering the `sso_write` options for every request, exactly as
before, until stage 4 reads the parameter. This is the only non-contract file
changed; without it `go build ./...` fails.

## 4. Consumers

See the manifest's `consumersAffected`. In short: the web regenerates types
and builds the organisation section, the confirmation notice, the
forbidden-keys display and the purpose-aware landing in the paired PR; the
deployed Part 1 web is safe against a Part 2 server until an admin configures
an organisation (which no Part 1 client can do), except that rows 6 and 8
for members now carry `organization_admin` and name the administrator, which
the Part 1 web renders as the message it is given; MCP is unchanged; PAC-50's
tests must pass untouched. Existing tests that pin Part 1's answer for an
organisation member are amended, not deleted, in the stage-4/5 PRs (revision
2, challenge B-2): `e2e/tests/llm-bring-your-own.spec.ts:89-120`, Part 1's
manifest AC-34, the web's `src/lib/aiProviders.test.ts:59-66`, and the web's
`SsoAdminStepupSection.tsx:40-57` behaviour; they are merge conditions.

## 5. Not decided here

Four product questions are open with the owner (spec §8); the contract
implements each factory default, which is factory-assumed, pending the owner:

- OQ-P2-1 (P2-D4): ask again on a key or destination change, not only a
  provider change? Default: yes, on provider, key or destination. This
  widens the owner's "ask once".
- OQ-P2-2 (P2-D2): move own-provider members onto the organisation's account
  when personal keys are forbidden? Default: no; row 10, nothing sent until
  they choose "Use my organisation's AI" and confirm.
- OQ-P2-3 (P2-D3): allow forbidding before the configuration is complete?
  Default: yes, with a warning in the web.
- OQ-P2-4 (P2-D13): one organisation-AI change per fresh break-glass
  sign-in? Default: yes.

The contract's shape and enums do not change with any answer: a different
answer to OQ-P2-2 makes row 9 reachable; to OQ-P2-1, changes when
`configurationVersion` advances; to OQ-P2-4, drops the once-per-session
rule. None blocks the contract.

## Challenge, revision 1 — 2026-10-28

**Verdict**: accept-with-changes

Challenged: contract revision 1 at a90ba5e on main ae5c2cf (contractHash
2dfed13770b83f8d0b744196a6bf41d5e125662b661bd4cb0efcbc1a1499f9c6, recomputed
with `git show HEAD:contracts/openapi/openapi.yaml | sha256sum`). Read: this
file, the manifest, the auth/scheduling/habits fragment diffs, MIGRATION.md,
the getStepupOptions change, PAC-50's grant store and state cookie
(storage/sso_stepup.go, api/handlers_sso_stepup.go), e2e/tests, mcp/, and
smart-calendar-flow origin/main (src/api/client.ts, src/api/types.ts,
src/api/generated/schemas.ts, src/lib/aiProviders.ts,
src/components/settings/SsoAdminStepupSection.tsx).

### Checks run (worktree at a90ba5e, no Docker)
- `py -3 scripts/openapi_assemble.py --check`: rc=0 (bundle in sync, 105 paths)
- `py -3 scripts/openapi_migration_report.py --check`: rc=0 (127 operations, 6 generated)
- `bash scripts/openapi_gen_go.sh --check`: rc=0
- `cd backend && go build ./...`: rc=0; `go vet ./...`: rc=0
- contractHash: matches.
- `go test ./api -run TestPAC21Every5xx…`: rc=1, could not run (the package
  needs a test database; no Docker). Not run: make verify, the backend suite,
  golangci-lint, the web gate, e2e.

### Requests wrongly permitted
1. **(blocking, B-1)** `PATCH /api/organization/ai` with `{"apiKey":"sk-proj-OPENAI…"}`
   from a form loaded at v5 (provider openai), after another proven admin saved
   `{"provider":"anthropic","apiKey":"sk-ant-…"}` (v6). The body is valid and
   the caller holds a live `llm_org_key_write` grant. The merge rules
   (scheduling.yaml:2596-2619: "a non-empty apiKey replaces the key for the
   provider after this write") store the OpenAI secret as Anthropic's key, and
   members' next requests (after re-confirming v7) send it to Anthropic. This
   breaks the contract's own rule "A key is never carried from one provider to
   another" (P2-D7, §2.7). Members are protected from staleness by
   `configurationVersion` on confirmOrganizationAI; admins are not. Fix: add
   a required `configurationVersion` (integer, minimum 0) to
   OrganizationAIConfigurationUpdate, the version the admin was shown. A
   mismatch is a 409 with the OrganizationAIConsentConflict shape
   (`reason: configuration_changed`, `organization`: the current read),
   checked after the body 400s and BEFORE the grant is used, so a stale form
   costs no step-up. Add the 409 to patchOrganizationAI and an acceptance test
   (stale version: 409, grant still live, configuration unchanged).
2. (non-blocking) `PATCH /api/organization/ai` with `{"model":"gpt-4o"}` or
   `{"baseUrl":"https://gw.example/v1"}` when no provider is stored. The
   message list has `apiKey requires a provider` only for apiKey, and
   `<property> does not apply to <provider>` has no provider to name. Also
   `{"model":"x"}` on an `azure_openai` configuration: the read says `model`
   is absent for azure_openai, but the update neither refuses nor documents
   it. Fix: state `<property> requires a provider` (400) for model and every
   destination property when no provider remains after the write, and
   `model does not apply to azure_openai`.
3. (non-blocking) `POST /api/llm/test` while the organisation forbids personal
   keys, with `{"llmProvider":"unsupported"}` (saved choice unsupported,
   complete, unconfirmed). Step 1a (scheduling.yaml:669) fires when the body
   "names a provider", but "provider" is not defined. If `unsupported` counts,
   the answer is 409 [organization_admin]; if not, it is row 10 [user].
   Fix: define "names a provider" as one of openai, anthropic, azure_openai,
   bedrock, vertex. Also state explicitly that `{"llmProvider":"organization","llmApiKey":"k"}`
   is the 409 personal_keys_forbidden when forbidden and the 400 (2a) when
   allowed (P2-AC-20 implies it; the description should say it).
4. (non-blocking) `POST /api/organization/ai/consent` `{"configurationVersion":5}`
   when v6 is current AND incomplete (an admin removed the key): both
   `configuration_changed` and `organization_not_configured` apply. Fix: state
   the precedence.
5. (non-blocking) `GET /api/admin/sso/stepup?provider=google&purpose=sso_write&purpose=llm_org_key_write`:
   a repeated `purpose` is undefined. Fix: say a repeated parameter is the 400
   (or that the first value wins).
6. (non-blocking) `PATCH {"apiKey":"<the same key>"}`: "advances when the
   change alters … the key" leaves open whether rewriting an identical key
   advances `configurationVersion` (and makes every member re-confirm). State it.

### Responses the client cannot handle
None found for the shipped web or mcp/:
- LLMTestResult.configuration (new, required): the shipped web reads the probe
  through an untyped `requestApi<{ok,response,provider}>` with no runtime
  parse (smart-calendar-flow src/api/client.ts:1531-1540). Its own
  `LLMTestResult` is the handwritten src/api/types.ts:139, not the generated
  one. The generated zod `LLMTestResult` is `z.strictObject` (schemas.ts:230)
  but no runtime code parses with it. Not breaking for the shipped web; the
  regenerated types in the paired web PR carry it.
- AIStatus.configuration (new, optional): the generated `AIStatus` is a
  `z.strictObject` (schemas.ts:11), but settings are read through
  `normalizeSettings` (client.ts:822) with no zod parse. Safe.
- personal_keys_forbidden: the notice renders `message` (aiProviders.ts:86),
  not a reason lookup, so an unknown value still renders. It only occurs after
  an admin uses patchOrganizationAI, which no Part 1 client calls.
- OrganizationAIWriteStepupRequired is not mistaken for an SSO refusal:
  `ssoStepupRequired` parses with the strict `SsoWriteStepupRequired`
  (client.ts:436, literal `sso_write_requires_admin_stepup`). The org-AI body
  fails that parse and falls back to the generic error.

### Consumers affected
- e2e/tests/llm-bring-your-own.spec.ts:89-120 **(blocking for the manifest,
  B-2)**: the test "AC-21 (Part 1, spec revision 2): an organisation member
  following the organisation is sent to no admin" asserts `enablers: ['user']`
  (lines 96, 100, 103), no "admin" in the message (98) or notice (107), no
  organisation option among the enabled choices (114-115), and no
  organisation/admin wording in #ai (117). Part 2 reverses each of these (row 6
  in an organisation is [user, organization_admin]; P2-AC-31). The manifest's
  consumersAffected (yaml:231) says "no existing spec". Fix: list this test
  as amended at stage 5 (not deleted), and add it to mergeConditions.
- e2e/tests/sso-stepup.spec.ts:71-108 (`toEqual` on the SSO 403, on
  `/stepup/options` without purpose, and Location `toBe(.../settings?stepup=granted)`):
  unaffected if the absent-purpose path is byte-identical as contracted.
  Listed so stage 4 runs it.
- backend `getStepupOptions` (api/handlers_sso_stepup.go:292-295) passes
  empty params, so on this branch `GET /api/admin/sso/stepup/options?purpose=bogus`
  is a 200 (contract: 400) and `?purpose=llm_org_key_write` answers
  `sso_configuration_writes`. `startSsoAdminStepup?purpose=llm_org_key_write`
  issues an `sso_write` grant (the state struct at :333-339 has no purpose).
  Acceptable for a contract-only branch, but mergeConditions name only the
  three new operations. Add: "the stage-4 api PR serves `purpose` on
  startSsoAdminStepup, getSsoStepupOptions and the callback (P2-AC-3)".
- State cookie (non-blocking): a step-up started before the deploy carries a
  state without a purpose field. Say in the contract that such a state is read
  as `sso_write`, and that "byte-identical" covers the 302/400/options bodies
  and the landing Location, not the signed cookie value.

### Contract/implementation divergences
1. getSsoStepupOptions/startSsoAdminStepup `purpose` (see above): documented
   on this branch, served nowhere until stage 4.
2. getOrganizationAI, patchOrganizationAI, confirmOrganizationAI: not served.
   MIGRATION.md marks them handwritten with a "flip at stage 4" note. The
   one-way rule (handwritten to generated) allows the flip; merge condition 3
   requires it.

### Uncertainties not actually resolved
None claimed (resolvesUncertain is empty; no x-uncertain on touched
operations).

### Verified clean
- Grant isolation: storage.ConsumeSSOStepupGrant selects `purpose = $4` only
  (storage/sso_stepup.go:117-140; 030_sso_admin_stepup.up.sql has no CHECK on
  purpose values). An sso_write grant cannot pay for `PATCH /api/organization/ai`,
  nor an llm_org_key_write grant for `POST /api/admin/sso`, and neither is
  consumed by the refused write (PAC-50 AC-27, P2-AC-2).
- `PATCH /api/organization/ai {"provider":"openai","apiKey":"sk-x"}` with no
  grant and not on the break-glass list: 403 OrganizationAIWriteStepupRequired
  with `stepup_purpose: llm_org_key_write`. Stale break-glass: 403
  fresh-login. No org: 403 SsoWriteMembershipRequired. The oneOf variants
  cannot overlap (`error` enum vs two consts, all `additionalProperties: false`).
- `?purpose=` (empty), `?purpose=x`, `?purpose=sso_write ` (trailing space):
  400 by enum on start (after provider) and on options (before membership).
  `?purpose=sso_write` explicit equals absent.
- A forgotten purpose fails safe: it yields an SSO grant, which the org-AI
  change refuses without consuming.
- A member cannot read the key: no readable schema has an org key
  (OrganizationAIConfiguration has only `keySet`; `apiKey` is writeOnly on
  OrganizationAIConfigurationUpdate only; the 409 consent conflict embeds the
  keyless read; the 400s say they never echo a key). `baseUrl` with userinfo
  (`https://u:p@gw/`) is refused on write by patchSettings' destination rules
  (nlp/destination.go:128, nlp/user_destination.go:158), so the
  every-member read cannot leak a credential embedded in the URL.
- Consent for a stale version (`{"configurationVersion":2}` when 3 is
  current): 409 configuration_changed with the current read, nothing
  recorded. A version from the future: also "not the current one". `0`,
  missing, extra property: 400. No operation takes an org id (P2-AC-8).
- O-13 wording: both example messages use "access" and contain none of the
  words read/reads/see. Row 5's message does not begin with the
  "AI features are off: " prefix that noticeReason strips, so the Part 1 web
  shows it whole.
- mcp/: clockwise_parse_command and clockwise_get_settings pass JSON through
  (mcp/tools.go:195-203, 233-238). Nothing in mcp/ references /api/llm/test,
  aiStatus or the step-up. mcp/nlp_not_configured_test.go stubs its own
  payload. No tool added (P2-D9).
- The shipped web's SsoAdminStepupSection (:40-57, :146) would show an org-AI
  landing as an SSO outcome, but no shipped client can start an org-AI
  step-up, as the manifest states.
- Location pattern `(&purpose=llm_org_key_write)?$` keeps every PAC-50
  Location valid. Grant TTL, scopes and IdP checks are independent of the
  purpose.
- MIGRATION.md: three rows, handwritten with notes; counts 127/6/121; gate rc=0.
- The PAC-21 5xx envelope rule holds by reading: the three new 500s are
  SchedulingErrorResponse with `{"error":"internal error"}`. Not confirmed
  by the test, which could not run here.

## Revision 2 — 2026-10-28 (answers contract challenge revision 1 and spec revision 1)

Author: contract-author. Every finding of the challenge above, with what was
done. Checks run for this revision are listed in the manifest's `gates`.

| Finding | Resolution | Where |
|---|---|---|
| B-1 stale admin form stores one provider's key as another's | Applied. `OrganizationAIConfigurationUpdate` gains required `configurationVersion` (integer, minimum 0; `minProperties` 2 so at least one property besides it). A mismatch is the new 409 on patchOrganizationAI with the `OrganizationAIConsentConflict` shape (`reason: configuration_changed`, `organization`: the current read), checked after every body 400 (step 2a) and before the grant is looked at, so the grant stays live. Spec P2-D15 and acceptance test P2-AC-40 (stale version: 409, grant still live, configuration unchanged; a change naming the current version then succeeds with the same grant). Rejected: `If-Match`/ETag (§2.7). | scheduling.yaml patchOrganizationAI (step 2a, 409, examples), OrganizationAIConfigurationUpdate, OrganizationAIConsentConflict; §2.7; manifest P2-AC-40 |
| B-2 `e2e/tests/llm-bring-your-own.spec.ts:89-120` reversed by Part 2, manifest said "no existing spec" | Applied. consumersAffected lists it as amended at stage 5 (not deleted), together with Part 1's manifest AC-34 (`contracts/features/NEW-llm-bring-your-own.yaml:149`), `smart-calendar-flow/src/lib/aiProviders.test.ts:59-66` and `SsoAdminStepupSection.tsx:40-57`; mergeConditions require each amended in the stage-4/5 PRs. mergeConditions also require the stage-4 api PR to serve `purpose` on startSsoAdminStepup, getSsoStepupOptions and the callback (P2-AC-3). The contract states that a state signed before the deploy (no purpose) is read as `sso_write`, and that "byte-identical" covers the 302/400/options bodies and the landing Location, not the signed cookie value. | manifest consumersAffected, mergeConditions; auth.yaml ssoAdminStepupCallback step 5 |
| NB-2 `model`/destination with no provider; `model` on azure_openai | Applied: `<property> requires a provider` (400) for `apiKey`, `model` and every destination property when no provider remains after the write; `model does not apply to azure_openai`. | scheduling.yaml patchOrganizationAI 400, OrganizationAIConfigurationUpdate |
| NB-3 "names a provider" undefined; `organization` + key | Applied: "names a provider" is one of the five provider values; `""`, `organization`, `unsupported` are not. While forbidden, any body carrying `llmApiKey` (including with `organization` or `unsupported`) is the 409 `personal_keys_forbidden` `[organization_admin]` (step 1a). When allowed, `organization` + key is the 400 in row 4 and that row's 409 in rows 5/6, nothing sent. `{"llmProvider":"unsupported"}` with a saved `unsupported` choice while forbidden is row 10 `[user]` or 11 `[organization_admin]`. | scheduling.yaml testLLMConnection, LLMTestRequest.llmProvider |
| NB-4 consent: stale AND incomplete | Applied: `organization_not_configured` wins (there is nothing to confirm). Matches spec §3.4. | scheduling.yaml confirmOrganizationAI description and 409 |
| NB-5 repeated `purpose` | Applied: a repeated `purpose` parameter is the 400, on the start and on the options read. | auth.yaml startSsoAdminStepup, getSsoStepupOptions parameters |
| NB-6 identical `apiKey` rewrite | Applied: any present `apiKey` (including `""` and a value equal to the stored key) advances `configurationVersion`; the server never compares keys. | scheduling.yaml patchOrganizationAI, OrganizationAIConfiguration.configurationVersion |
| Consumers: e2e `sso-stepup.spec.ts:71-108` | Listed in consumersAffected as "must pass unchanged; stage 4 runs it". | manifest |
| Consumers: getStepupOptions passes empty params; start issues sso_write for any purpose | Kept for this contract-only branch (stated in §3); added the merge condition that stage 4 serves `purpose` on all three step-up operations. | manifest mergeConditions |
| State cookie across the deploy | Applied (see B-2). | auth.yaml |
| Divergence 1 and 2 (purpose and new operations not served) | Unchanged: contract-only branch; merge conditions require stage 4 to serve them and flip the MIGRATION.md rows. | manifest mergeConditions; MIGRATION.md |
| Responses the client cannot handle: none found | Noted. Revision 2 adds an optional `configuration` to `LLMDestinationRefusal` (absent = own, so Part 1's 422 is unchanged) and a 409 to patchOrganizationAI, which no shipped client calls. | §2.7c |
| Verified clean items | No change needed. | - |

Changes from the spec's revision 1 (spec challenge revision 0), carried into
the contract in this revision:

| Spec change | Contract change |
|---|---|
| P2-D2 default now "not moved" (row 9 reserved, row 10 = choose "Use my organisation's AI") | AINotConfigured table (row 9 reserved; row 10 `forbids; complete` `[user]`), its consequences and message rule; AIStatus.configured rows 2 and 4; AIStatus/LLMTestResult `configuration: organization` row 4 only; OrganizationAIConfiguration.personalKeysAllowed; patchOrganizationAI "what a change does"; confirmOrganizationAI 200; parse example `personalKeysForbidden` (row 10). |
| B1: storing "off" or a provider ends the confirmation | patchSettings description; Settings.llmProvider; confirmOrganizationAI; AINotConfigured "Confirmed". |
| B2: one consistent read, exact confirmed version; only the confirmation operation records one | parseNaturalLanguage, testLLMConnection, refreshMeetingBrief, AINotConfigured "Confirmed", patchSettings, confirmOrganizationAI. |
| B3: no key in provider error text | parseNaturalLanguage, testLLMConnection (502), refreshMeetingBrief (failed `brief_text`, logs, Sentry). |
| B4: one break-glass change per sign-in | patchOrganizationAI step 3 and 403; OrganizationAIWriteFreshLoginRequired. |
| NB5: organisation destination refusal names the organisation's admin | LLMDestinationRefusal.configuration (optional), parse and brief descriptions. |
| NB1: wrong attribution | §2.4 of this document: "the owner's example" withdrawn. |

## Challenge, revision 2 — 2026-10-28

**Verdict**: accept-with-changes

Challenged: contract revision 2 at 0c81d42 (delta `git diff a90ba5e 0c81d42 -- contracts backend`). Bundle sha256 d2044556ed3398e7b791c7de56b28d6749f98ea7e81d9570247a73716ef68780, recomputed with `git show HEAD:contracts/openapi/openapi.yaml | sha256sum`; it matches the manifest. Read: this file's §1-§5 and the Revision 2 table, the manifest diff, the auth/habits/scheduling fragment diffs, the regenerated `api/gen` types, spec revision 1 (§3.1-§3.5, §4.6-§4.10), `backend/api/handlers_llm.go`, `backend/api/middleware.go`, `backend/auth/jwt.go`, `backend/api/pac66_llm_destination_test.go`, e2e/tests (422 assertions), and smart-calendar-flow origin/main (`src/api/client.ts`, `src/api/generated/schemas.ts`).

### Checks run (detached worktree at 0c81d42, no Docker)
- `py -3 scripts/openapi_assemble.py --check`: rc=0 (bundle in sync, 105 paths)
- `py -3 scripts/openapi_migration_report.py --check`: rc=0 (127 operations, 6 generated)
- `bash scripts/openapi_gen_go.sh --check`: rc=0
- `cd backend && go build ./...`: rc=0; `go vet ./...`: rc=0
- contractHash: matches.
- Not run: the backend suite (needs a test database), golangci-lint, the web gate, e2e, `make openapi-check`.

### Revision 1 findings, verified
- B-1: **partly fixed.** The sequential case is closed: the 409 comes after the body 400s and before the proof, the grant stays live, and P2-AC-40 pins it. The concurrent case is still open; see B-1′ below.
- B-2: fixed. consumersAffected lists `e2e/tests/llm-bring-your-own.spec.ts:89-120` (amended, not deleted), Part 1 AC-34, `aiProviders.test.ts:59-66` and `SsoAdminStepupSection.tsx:40-57`. mergeConditions require each of them, and also require stage 4 to serve `purpose` on all three step-up operations. The pre-deploy state is read as `sso_write`, and "byte-identical" is scoped to what clients can observe.
- NB-2 to NB-6: fixed as described in the Revision 2 table.

### Requests wrongly permitted
1. **(blocking, B-1′)** The stale-form race is still open when the two writes run concurrently. Admins X and Y each hold a live `llm_org_key_write` grant, and A is at v5 (openai). X sends `PATCH /api/organization/ai` `{"configurationVersion":5,"provider":"anthropic","apiKey":"sk-ant-…"}` and Y sends `{"configurationVersion":5,"apiKey":"sk-proj-OPENAI…"}`. Both pass step 2a (scheduling.yaml:1059-1066) before either reaches step 4 (:1090). X stores v6 (anthropic), then Y's write lands and stores the OpenAI secret as Anthropic's key (v7). This contradicts spec §3.2 ("If the configuration has changed since (another administrator saved first), the change is refused as a conflict"). P2-AC-40 (manifest:189) tests only the sequential order. The same race exists for an organisation with no row: two `{"configurationVersion":0,"provider":…}` writes.
   - Fix (contract): step 4 stores the change only if the stored version still equals `configurationVersion`, as one atomic conditional write that also covers inserting the first row, in the same transaction as the grant consumption. On mismatch: 409 `configuration_changed` with the current read, nothing stored, grant not consumed.
   - Fix (test): extend P2-AC-40 with an interleaved case using a test store as P2-AC-37 does: the other admin's change is applied between Y's version check and Y's write; Y gets the 409, A holds X's change, and Y's grant is still live.
2. (non-blocking) Two break-glass changes from the same fresh session at the same time can both find "no change recorded for this issue time" and both succeed (two changes for one sign-in, against P2-D13). Fix: at most one org-AI change is ever recorded per (user, session issue time), and the check and the record are one atomic step. Note: `iat` has one-second resolution and sessions have no `jti` (`backend/auth/jwt.go:17-24`); two sign-ins by the same address in the same second share an issue time, so the second is refused. Fails safe; say so in one sentence.
3. (non-blocking) The 400s that depend on stored state are decided before the version check, so a stale form gets a misleading 400 instead of the 409 (e.g. form at v5 openai, A now v6 azure_openai, `{"configurationVersion":5,"baseUrl":"https://gw.example/v1"}` → `baseUrl does not apply to azure_openai`). Fix: only shape 400s come before 2a; 400s that read stored state come after it.
4. (non-blocking) Precedence for a bad `configurationVersion` is undefined (`{"configurationVersion":"3","model":"x"}` fits two messages, scheduling.yaml:1154 and :1156-1157). `ConfigurationVersion` is a plain `int` in `api/gen/paceday.gen.go:2348`, so a missing value decodes as 0 and would match a never-configured organisation unless the handler decodes the raw body. Fix: name one message per case; pin "missing ≠ 0" in P2-AC-4 for a v0 organisation.

### Responses the client cannot handle
None for the shipped web, mcp/ or e2e:
- `LLMDestinationRefusal.configuration` (new, optional): the generated zod schema is strict (`smart-calendar-flow/src/api/generated/schemas.ts:221`) but no runtime code parses a 422 with it (`src/api/client.ts:475-480`, `:510-518`). The e2e 422 assertions use `toMatchObject`. mcp/ does not see the 422.
- The 409 on patchOrganizationAI reuses OrganizationAIConsentConflict; no shipped client calls the operation.
- Row 10's message renders from `message` and contains none of read/reads/see.

### Consumers affected
- `backend/api/pac66_llm_destination_test.go:150-154` (non-blocking, NB-1): fails on any 422 key other than error/reason/property; the schema allows `configuration: own`. Fix: the server never sends `own` (absent is the only form), or narrow the enum to `[organization]`; list this test as "must pass unchanged".
- `backend/api/handlers_llm.go:98-102` (non-blocking): the probe 200 lacks the now-required `LLMTestResult.configuration`. Add merge condition: "the stage-4 api PR emits `configuration` on every probe 200".

### Contract/implementation divergences
1. patchOrganizationAI step ordering vs spec §3.2's conflict rule under concurrency (B-1′).
2. Unchanged: purpose and the three new operations not served on this branch; merge conditions cover them.

### Uncertainties not actually resolved
None claimed.

### Contract vs spec revision 1
No divergence apart from B-1′.

### Verified clean
- Sequential stale version: 409 with the v6 read, grant live, nothing stored, break-glass not used.
- `{"configurationVersion":3}` alone refused; `{"configurationVersion":4,"provider":""}` allowed; `-1` and `null` → 400.
- Confirmation racing an admin change: exact-version match means row 5, nothing sent.
- Whole-document patchSettings records no confirmation (P2-AC-38); storing `openai` deletes it (P2-AC-36).
- Probe while forbidden: organisation/unsupported + key → 409 `[organization_admin]`; `openai` never uses the org key.
- Repeated `purpose` → 400; pre-deploy state reads as `sso_write`.
- OrganizationAIConsentConflict keyless; `apiKey` writeOnly on the update only.
- O-13 wording holds; MCP unchanged; `sso-stepup.spec.ts` unaffected.
