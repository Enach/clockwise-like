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
