# Contract rationale — NEW-llm-bring-your-own, Part 2 (organisation-wide AI key)

- **Spec**: `docs/specs/NEW-llm-bring-your-own-part2.md` (revision 1, not yet
  challenged)
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
| `PATCH /api/organization/ai` (patchOrganizationAI) | proven admin | merge change; PAC-50's gate with purpose `llm_org_key_write` |
| `POST /api/organization/ai/consent` (confirmOrganizationAI) | any member | "I was shown version N and I confirm" |

Changed: the three PAC-50 step-up operations gain an optional `purpose`; the
not-configured answer (`AINotConfigured`) gains rows 9-11, the reason
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

**Rejected: re-asking on provider change only** (the owner's example). A new
key or gateway can be another account with other administrators, which is
exactly what the member was told about. Recorded as spec OQ-P2-1; the cost is
an extra confirmation after a key rotation.
**Rejected: a confirmation flag on `patchSettings`** (`llmProvider:
organization` + `confirmed: true`). It would tie confirmation to changing
one's choice, but rows 9-10 need confirmation from members who never change
their choice (personal keys forbidden), and it would put a per-org-version
fact into the user's settings document.

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
(PAC-50's ordering).

**Rejected: per-provider key slots for the organisation**, as users have.
Each organisation change is proven and deliberate, and members are shown one
configuration; slots would add a hidden second key members cannot see.

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
an organisation (which no Part 1 client can do); MCP is unchanged; PAC-50's
tests must pass untouched.

## 5. Not decided here

OQ-P2-1 (re-ask scope) and OQ-P2-2 (route forbidden own-key members to the
organisation after confirmation) are recorded factory assumptions, both the
conservative choice. Neither blocks the contract.

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
