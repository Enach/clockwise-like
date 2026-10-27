# Spec — PAC-22: SSO configuration must not disclose identity-provider secrets

- **Linear**: https://linear.app/paceday/issue/PAC-22/stop-returning-oidcclientsecret-and-samlcert-from-apiadminsso
- **Status**: challenged (v3.2, 2026-10-27). v1 and v2 were rejected (see History). v3
  was never challenged. v3.1 is v3 checked again against `origin/main` at
  `6d751f9`, with the open questions answered as factory assumptions (see
  **Decisions**). The challenge of v3.1 (History) accepted it with changes. v3.2
  applies every one of those changes, and "Revision v3.2" answers each of them
  in turn. What changed between v3 and v3.1 is listed under "Revision v3.1".
- **Author**: spec-author
- **Inherited scope** (factory §7 rule 1):
  - **Resolved here**: API-001 (disclosure half; the write half is PAC-50's),
    API-060, API-066, and the `listSsoProviders` share of API-092 (its 200 and
    403; the three writes are covered by PAC-50, the five sign-in paths by
    PAC-35).
  - **`x-uncertain`**: on `main`, U-21 is on the create request
    (`contracts/openapi/paths/auth.yaml:1080`) and U-22 is on the response
    (`auth.yaml:1141`). PAC-50's branch removes **both** markers by observation
    (§2.8). Once PAC-50 lands (D-5), no marker is left on these operations for
    this spec to resolve. What remains is API-060. This spec also has to rewrite
    the create request's description, which PAC-50 words as "Nothing else is
    validated…" and which AC-7, AC-8 and AC-18 make false. If PAC-50 has not
    landed when this contract is written, U-21 and U-22 are resolved here
    instead: AC-7, AC-8 and AC-18 decide the validation U-21 asks about, and
    AC-1 to AC-6 replace the schema U-22 is about.
  - **Moved to PAC-50**: API-001's authorization half, API-067 (removal of
    nothing reported as success: v3's AC-13), and the exact-domain rule PAC-46
    reports.
  - **Inherited, not resolved, position stated**: API-091 (§5). API-010 and
    API-011 were listed by v3. Both are fixed now (§2.7), so neither shapes a
    criterion here any more.
- **Depends on**: **PAC-50 lands first.** That ordering was decided on 2026-10-22
  (`docs/specs/PAC-50.md`, §4.9) and this spec adopts it (D-5). It is an ordering,
  not a dependency on PAC-50's outcome: every criterion below means the same
  thing whatever PAC-50's gate turns out to be, because each one says "a caller
  permitted to change SSO configuration" and leaves PAC-50 to define who that is.

## Scope: this spec is the projection-and-validation half

| Spec | Covers | State on 2026-10-27 |
|---|---|---|
| **PAC-22** (this one) | Identity-provider secrets stop crossing the API boundary. The response becomes a described shape. A configuration that cannot work is refused when it is offered. SSO can be suspended and resumed. | spec draft v3.1 |
| **PAC-50** | Who may write an SSO configuration and for which domain. Removal of nothing is reported as such. Changes are recorded. | contract on `factory/pac-50-contract`; implementation PR #212 open, not merged |

The line is drawn at *decisions about privilege*. Everything in this spec is a
projection or a validation: it changes what leaves the server and what the server
refuses to accept. It adds no privilege model and needs no migration. PAC-50
supplies the privilege model.

## 1. Problem

Paceday lets an organisation replace password login with its own identity
provider. To set that up, somebody gives Paceday the credentials that let Paceday
identify itself to that identity provider on the organisation's behalf: an OIDC
client secret, or a SAML signing certificate. These are the keys to the
organisation's front door.

**Today any employee with a working Paceday account can read them.** The
organisation's SSO configuration is served back in full, secrets included, to any
authenticated caller who belongs to the organisation. A curious colleague with
browser devtools can read the client secret. So can anyone who phishes a single
low-privilege account, such as an intern, a contractor, or a former employee
whose account was not disabled. With it they hold the credential that identifies
Paceday to the organisation's identity provider.

**What it costs them.** A secret that any employee can read is not a secret. Once
anyone knows this, the secret must be treated as compromised. The fix is to
rotate it at the identity provider, which the customer has to do, coordinated
with a Paceday reconfiguration. The organisation's security team is hit hardest.
They handed over a secret expecting it to be held, not published, and no audit on
their side would show that their whole staff can read it.

**Who is affected.** Every organisation that has configured SSO, and every user
in it. Production had zero users on 2026-10-21 (human decision, recorded in
`docs/specs/PAC-49.md` §2 item 1). So no real customer secret has been exposed
yet. The defect must close before one can be.

Two smaller problems on the same surface cost the person who configures SSO, not
the organisation:

- A configuration that cannot possibly work is accepted without complaint. The
  person who entered it finds out only when a colleague cannot log in.
- Once SSO is configured there is no way to turn it off short of deleting it. A
  misconfiguration that is locking people out cannot be paused while someone
  diagnoses it.

## 2. Current behaviour

Every claim was read from source on `origin/main` at `6d751f9` for this revision.
None was carried over from v3. Some line numbers differ from v3 because PAC-21,
PAC-35 and PAC-45 have since landed. Nothing was executed for this spec; the
behaviour described is what the code says. Where a claim is an inference, it is
marked as one.

### 2.1 The response contains the secrets

`storage.SSOProvider` (`backend/storage/sso_providers.go:12-26`) declares thirteen
fields and **no struct tags**. Two of them are `OIDCClientSecret` (`:20`) and
`SAMLCert` (`:23`).

The create handler encodes the stored struct directly
(`backend/api/handlers_sso.go:228`). The list handler encodes the slice of these
structs directly (`:250`). Neither path has an intermediate type. Both secret
columns are selected into the struct on every read: the column list is at
`sso_providers.go:28-31`, the scan at `:33-45`, and the list query at `:84-87`.

The struct has no tags, so `encoding/json` emits the Go field names and the keys
are PascalCase. The contract documents the full PascalCase shape, including
`OIDCClientSecret` (`contracts/openapi/paths/auth.yaml:1122`) and `SAMLCert`
(`:1132`). It says it does so because that is what the code does, not because it
is intended (`:1126`). The list response is a bare array at the top level
(`handlers_sso.go:250`, `auth.yaml:700`).

The columns are plain `TEXT` with no encryption at rest
(`backend/storage/migrations/008_sso_providers.up.sql:9,12`).

**The API boundary is the only channel that leaks these values.** The other
channels were checked:

- **Sentry** never receives a request body or cookies. `scrubRequest` blanks
  both unconditionally (`backend/sentry.go:64-78`).
- **The request logger** prints only method, path and duration
  (`backend/api/middleware.go:91`).
- **The audit log** has seven `WriteAuditLog` call sites, and none is on an SSO
  path (`api/handlers_nlp.go:69`, `api/handlers_schedule.go:212`,
  `engine/compression.go:192`, `engine/focus_time.go:110`,
  `engine/focus_time_cleaner.go:47`, `engine/smart_schedule.go:331`,
  `nlp/parser.go:235`).
- **Error responses** no longer echo errors raw. Since PAC-21, an OIDC setup
  failure answers a generic message and sends the detail only to the log and
  Sentry (`handlers_sso.go:69-72`, `backend/api/server_errors.go:41-57`). Any
  such error is built from the issuer, not from the secret.

### 2.2 There is no update operation; the single write replaces

There are exactly three admin routes: create, list, and delete by domain
(`backend/api/routes.go:172-175`). **There is no update operation.** The single
write is an upsert keyed on domain. On conflict it overwrites every column from
`EXCLUDED`: provider name, type, enabled, issuer, client id, client secret and all
three SAML values (`sso_providers.go:63-73`). It is a replace, not a merge, and
`backend/storage/sso_providers_test.go:59` (`TestUpsertSSOProvider_Update`)
asserts that.

### 2.3 An unusable configuration is accepted

Create checks only that domain, provider name and provider type are non-empty,
and that the type is one of two values (`handlers_sso.go:189-196`). The OIDC
issuer, client id and client secret are accepted empty and stored empty, and so
are all three SAML values (`:209-220`). The columns default to `''`
(`008_sso_providers.up.sql:7-12`).

The failure comes later and does not help:

- **Empty issuer.** Building the login fails inside `auth.NewOIDCClient` at the
  sign-in start route, which answers a generic 500 (`handlers_sso.go:64-72`).
- **SAML.** A SAML provider is accepted and stored, then the start route refuses
  it with 501 "SAML not yet supported" (`:56-59`). SAML can be configured but does
  not work, by design.

This is API-060, and it is what U-21 (`auth.yaml:1080`) asks to decide. PAC-35
pinned the start route's 501 and 500 answers in Go
(`backend/api/pac35_auth_flows_test.go:396`) and left fixing them to this spec
(`docs/specs/PAC-35.md`, D-5).

### 2.4 SSO cannot be disabled through the API

Create hardcodes `Enabled: true` (`handlers_sso.go:213`), and the replace-upsert
writes that value over the stored one on conflict (`sso_providers.go:66`). No
route sets it to false.

The column already works as a kill switch. The sign-in lookup filters on
`enabled = true` (`sso_providers.go:51`), and both sign-in detection
(`backend/auth/sso_detect.go:37`) and the start and callback routes
(`handlers_sso.go:51`, `:112`) go through that lookup. The listing query does not
filter on it (`sso_providers.go:82-92`). So the schema has the capability, but
the API cannot reach it. This is API-066.

### 2.5 Who may call these operations today

All three admin handlers start with the same check: load the caller, require a
non-nil organisation reference, and otherwise answer 403 "org membership
required". The check is at `handlers_sso.go:167-172` for create, `:233-238` for
list and `:257-262` for delete. A database error takes the same branch
(`err != nil || user == nil || user.OrgID == nil`). So a 403 here does not by
itself mean the caller was refused for lack of membership, and any criterion
below that asserts a refusal also asserts what is stored afterwards.

Create and delete also require the target domain to match the caller's
organisation domain, and that match is a suffix match (`handlers_sso.go:204`,
`:269`; `backend/domain/domain.go`, `DomainMatchesOrg`). On `main` today,
therefore, **any member can create, replace and delete their organisation's SSO
configuration**. PAC-50 replaces both checks. Its implementation PR, #212, is
open and adds a step-up gate (IdP-admin re-authentication, or a break-glass list
held by the deployment) and an exact-domain rule. Its contract does not gate
the list operation beyond membership, and it leaves the secret in the response to
this spec (its create 201 description says so).

### 2.6 Sign-in is protected by tests today

v3 said nothing protected SSO sign-in. That is no longer true:

- `backend/api/handlers_sso_test.go:172` (`TestOIDCCallback_AcceptsEmailInProviderDomain`)
  drives the callback against an in-process fake identity provider
  (`:30-93`). It asserts a 302 to the frontend and a non-empty session cookie.
- `backend/api/pac35_auth_flows_test.go:286` and `:396` assert every documented
  status of the callback and start routes.
- `:803` asserts every documented status of detection.

All of these seed the provider row through the storage layer directly. **No
test, on any branch merged to `main`, exercises the three admin handlers.** PAC-50's open PR
adds tests for the writes. Nothing tests the list handler's 200 or 403 (API-092,
`docs/factory/api-audit.md:350`).

### 2.7 Consumers

- **Frontend.** The only references to the admin SSO operations in
  `smart-calendar-flow/src` are the generated types (`src/api/generated/types.ts`,
  `schemas.ts`, `schemas.types.d.ts`). No hand-written code calls them, and no
  page exists for them. The sign-in dialog consumes detection and the start route
  and now follows the `redirect_url` that detection returns
  (`src/components/auth/AuthDialog.tsx:105-108`). API-010 is therefore fixed in
  the web app by PAC-35. Its audit row (`api-audit.md:268`) still says
  "Confirmed", and that row is stale. API-011, the silent session-issue failure,
  is marked Fixed (PAC-35) (`api-audit.md:269`).
- **MCP server.** `sso` appears nowhere under `mcp/`, in any case.
- **e2e.** No scenario touches SSO on `main`.
- **Outside the repositories.** None can exist in production, which has zero
  users (D-4).

All three admin operations are `handwritten` (`contracts/openapi/MIGRATION.md:55-57`).

### 2.8 What PAC-50 changes on the same operations (not merged)

This section is read from `origin/impl/pac-50-sso-write-gate-backend` (PR #212).
It may change before PAC-50 merges, and the contract stage must re-read it.

- Create and delete are gated, as §2.5 describes. A refused write answers 403
  with a machine-readable code.
- Every 400 is decided before the write authorization is consumed. One
  authorization pays for one write.
- Removal of a domain with no configuration answers 404 (API-067 is marked
  "Fixed (PAC-50)").
- Domains are stored lower-cased.
- SSO writes are recorded in an operator-only SSO event table.
- **U-21 is removed as well.** `SsoProviderCreateRequest` says "Determined at
  stage 2 from the handler (resolves U-21)". It then states that nothing beyond
  domain, provider name and provider type is validated, and that an `oidc`
  provider can be stored with an empty issuer, client id or secret (API-060, "not
  changed by PAC-50") (`contracts/openapi/paths/auth.yaml:1379-1391` on that
  branch). After PAC-50, that description is what this spec makes false. It is
  not an open marker.
- **The domain rule becomes exact.** `DomainMatchesOrg` becomes a
  case-insensitive equality (`backend/domain/domain.go:49-51` on that branch),
  and delete compares its path segment with it before the authorization is
  consumed.
- **Writes are recorded under one kind.** The SSO event table allows exactly two
  kinds, `sso_stepup` and `sso_provider_written`
  (`028_sso_admin_stepup.up.sql:35` on that branch). A configuration write's
  details name the operation (`op`: `create` or `delete`), the provider, the
  organisation and how the write was authorized
  (`backend/storage/sso_stepup.go:64`, `:269`, `:298`). A delete that finds no
  row records no event: it returns at `:291-293`, before the record at
  `:297-298`.
- U-22 is marked resolved, and the 201 body is pinned to exactly the thirteen
  PascalCase keys (`TestSSOWrite_OneGrantOneWrite`). This **pins the leak in a
  test**, so this spec's implementation must change that test as well as the
  handler. That is deliberate (factory §7 rule 2), not a regression.
- The audit row for API-001 is annotated: the write half is closed, and the read
  half plus the secret in both responses are left to PAC-22.

## 3. Desired behaviour

The actors:

- **Any member**: any authenticated member of the organisation.
- **A permitted caller**: a caller allowed to change the organisation's SSO
  configuration. Who that is belongs to PAC-50, and this spec does not change it.
  Whatever rule PAC-50 sets for configuration writes also governs every new write
  this spec introduces (D-6).

### 3.1 The secrets stop leaving the server

Any member can see that SSO is configured for their organisation, which identity
provider it points at, and whether it is currently active. Nobody can read back
the client secret or the signing certificate, from any response of any operation.
The secrets are write-only: they can be supplied and replaced, never retrieved.

A reader can still tell whether a secret is on file, for each of the two
secrets: the client secret and the signing certificate. "Configured, no secret
entered" and "configured with a secret" are different states, and the person
configuring SSO needs to tell them apart. Knowing that a secret exists is not
knowing the secret. Each indicator means exactly that and nothing else. It does
not change when anything other than its own secret changes.

After this change no write through the API can store a signing certificate
(§3.4, D-8). The certificate indicator therefore describes only configurations
stored before the change. Those configurations stay readable (§3.4), and a
reader must still be able to see what they hold.

The trade-off is deliberate: someone who forgets a client secret cannot retrieve
it and must re-enter it.

### 3.2 Supplying a configuration means supplying all of it

The configuration write stays a replace (§2.2). **This spec does not turn it into
a merge.** If a configuration write leaves out one of the configuration's values,
that value is stored as absent. The stored value is not kept. So changing any
part of the configuration means supplying all of it, secrets included. Renaming
a provider means re-entering its secret. That cost is accepted. A merge would
quietly change the stored-data meaning of an operation that already exists, and
would bring back the question of what "required" means for a value that is
already stored.

### 3.3 Whether SSO is active is not part of the configuration

**This spec adds exactly one write operation: a named transition that changes
only whether SSO is active.** It carries no configuration values and no secrets.
The spec requires this outright rather than leaving it to the contract. The
alternative cannot work: §3.2 refuses a merge, and §3.4 makes every value a
provider kind needs required on every configuration write. Together those would
make it impossible to suspend SSO through the configuration write without
re-supplying the secret, and an emergency control must not need that.

The two writes divide cleanly:

- **The configuration write** replaces the configuration and never touches
  whether SSO is active (D-3). A new configuration starts active, as today. A
  replaced configuration keeps the active state it had. Nobody switches SSO off by
  accident, and nobody switches back on, by entering credentials, an SSO that a
  colleague deliberately suspended.
- **The transition** changes the active state and nothing else. Suspending and
  resuming never need a secret.

The trade-off is deliberate: fixing a suspended configuration and putting it back
into service takes two writes, a replace and then a resume.

**The transition names its target and states the outcome it wants.** It
identifies the configuration it acts on in the same way removal does, and it
asks for a state ("suspended" or "active"). It does not ask to flip the current
state. Its outcomes are fixed here, not left to the contract (D-9):

- **No configuration for the target.** The transition is refused with a "not
  found" outcome. Nothing is created, and nothing is recorded. An operator who
  is told SSO was suspended must be able to rely on SSO having existed.
- **The configuration is already in the requested state.** The transition
  succeeds, changes nothing, and reports the state the configuration is in. It
  is not refused. Someone reaching for the kill switch in an emergency wants SSO
  off, and "it is off" is the true answer whoever got there first. The answer is
  never success for a configuration that does not exist (the previous case).
- **A target outside the caller's organisation** is refused exactly as a
  configuration write to it is refused (§3.6, AC-21).
- **The write authorization.** PAC-50's rule decides it in the same way as for
  the configuration write: a refusal decided from the request alone (a malformed
  request, a target outside the caller's organisation) leaves the authorization
  unspent. Any outcome that needs the stored configuration (not found, already
  in the requested state, changed) spends it. This is PAC-50's D-5 for a removal
  that finds nothing, applied to the new operation.

### 3.4 A configuration that cannot work is refused at the point of entry

If someone setting up SSO leaves out something their kind of identity provider
needs to complete a sign-in, they are told at once, and told what is missing.
They do not find out later when a colleague cannot log in. Because the
configuration write replaces (§3.2), "required" means required on every
configuration write. Values already stored do not count.

A kind of identity provider that the product cannot complete a sign-in with is
refused when it is offered. It is not stored only to be rejected at sign-in (D-1).
Configurations already stored are not changed, deleted or rewritten by this rule.
They stay readable, removable and suspendable, and sign-in keeps refusing them
exactly as it does today.

A refused configuration costs the caller nothing beyond the refusal. Whatever
authorization PAC-50 requires for a write is still unspent afterwards, so the
caller can correct the submission and send it again.

### 3.5 SSO can be turned off without being thrown away

Someone investigating a misconfiguration that is locking colleagues out can
suspend SSO, leaving the configuration intact to fix, and later resume it. While
SSO is suspended, sign-in for that domain behaves as if no SSO were configured.

**Suspension stops new sign-ins. It does not end sessions.** Sessions already
established through the provider survive suspension, deletion and
reconfiguration alike. A session is a self-contained bearer credential that lasts
a week and keeps no state on the server. Anyone who reaches for suspend after a
compromise must be told this. Changing it is out of scope (§5).

### 3.6 Suspending and resuming are changes to SSO configuration

Suspending and resuming are authorized by exactly the rule that authorizes
configuration writes, never by membership alone (D-6). Otherwise any member could
switch off the organisation's SSO. They are also recorded wherever PAC-50 records
configuration writes, so that "who turned SSO off" has the same answer as "who
changed the configuration".

### 3.7 The response shape is stable and conventional

The response shape stops being an accident of how the server stores the data. It
becomes a described, deliberate shape. In particular, the retrieval response is a
named container rather than a bare sequence. The only reason is to allow for
later additions: a property added beside the sequence later does not change the
response's type for every consumer at once.

This is **not** done for consistency with the rest of the API. Most list
responses in the contract are bare sequences today (the teams, rooms, habits,
organisation members and audit listings among them). A later stage must not
"correct" this container back to a bare sequence in the name of consistency.

## 4. Acceptance criteria

None of these tests exist (§2.6). Each criterion says whether it fails today or
guards behaviour that already holds:

- **Fail against `main` today**: AC-1 to AC-10, AC-12, AC-15, AC-16 and AC-18 to
  AC-21. (AC-4 fails today only because the property it reads does not exist
  yet.)
- **AC-11**: one half guards existing behaviour, the other cannot be arranged
  today.
- **AC-14**: a regression guard that already exists and must stay green.
- **AC-13**: withdrawn (moved to PAC-50).
- **AC-17**: needs PAC-50's gate to exist, and is written to fail until this
  spec's validation runs before that gate's authorization is spent.

Whenever a criterion asserts a refusal, it also asserts what is stored
afterwards, because a status code alone can pass for the wrong reason (§2.5).

In these tests, "a permitted caller" means one arranged through PAC-50's
mechanism as it stands on `main` when the test is written: a step-up grant or
the break-glass list. If PAC-50 has not merged when these tests are written, the
plan says how the caller is arranged.

**Secret non-disclosure**

AC-1. Given an SSO configuration for an organisation with a non-empty client
secret and a non-empty signing certificate, and given a member of that
organisation who is not a permitted caller, when that member retrieves the
organisation's SSO configuration, then no value anywhere in the response equals
either stored value, and no property of the response carries either one under any
name. The same holds when the caller is a permitted caller.  `[contract]`

AC-2. Given a permitted caller, when they submit a complete configuration of the
supported kind with a non-empty client secret, and the request succeeds, then the
response describes the stored configuration and no value in it equals the
supplied secret. Given a permitted caller who submits the same configuration and
also supplies a signing certificate, then the request is refused (AC-18) and the
response, whatever its status, contains neither supplied secret. Given a member
who is not a permitted caller, when they make either submission, then the
response, whatever its status, contains neither supplied secret.  `[contract]`

AC-3. Given a stored configuration whose client secret is non-empty, when it is
retrieved, then a named property of the response, documented as meaning "a client
secret is on file", is true. Given a stored configuration whose client secret is
empty, when it is retrieved, then that same property is false. The same holds,
with a second named property documented as meaning "a signing certificate is on
file", for the signing certificate. Configurations holding a certificate are
arranged through storage, because no API write can store one (AC-18). Each
assertion is about the value of one property on one configuration, not about how
two configurations differ. Each property is checked once with its own secret
present and the other secret absent.  `[contract]`

AC-4. Given a stored configuration with a non-empty client secret, when a
configuration write replaces it, changing only its display name and supplying the
same secret, then the client-secret property of AC-3 is still true, and no
described property other than the display name and any timestamp changes. When a
write then tries to replace it supplying an empty secret, that write is refused
(AC-7), and the property is still true. The indicator tracks the secret and
nothing else. The "false" state after this change exists only for configurations
stored before it, and AC-3 and AC-5 cover those.  `[contract]`

AC-5. Given a configuration stored before this change with an empty client secret
and an empty certificate, when it is retrieved, then the response is valid against
the described shape and reports that no secret is on file. It does not fail to
serialise, and it does not omit the property.  `[contract]`

**Response shape**

AC-6. Given an organisation with no SSO configuration, then one, then more than
one, when its configurations are retrieved each time, then the top level of the
response is a named container and not a bare sequence, and the response validates
against the described shape in all three cases.  `[contract]`

**Validation at entry**

AC-7. Given no SSO configuration for a domain, when a permitted caller submits one
for the kind of identity provider the product can complete a sign-in with, and
leaves out any one of the values that kind needs to complete a sign-in (D-2), then
the request is rejected with a client error naming the missing value, and
afterwards no configuration exists for that domain. The test is run once for each
required value. Given a stored, working configuration for the domain, when the
same incomplete submission is made, then it is rejected the same way and the
stored configuration is unchanged.  `[contract]`

AC-8. Given any state, when a permitted caller submits a configuration for the
kind of identity provider the product cannot complete a sign-in with, then the
request is rejected at submission with a client error saying that kind is not
supported, and nothing is stored or changed (D-1). Given a configuration of that
kind already stored, written directly to storage as a pre-existing row would be,
when it is retrieved, suspended, resumed or removed, then each of those succeeds
exactly as for any other configuration.  `[contract]`

AC-18. Given no configuration for a domain, when a permitted caller submits a
configuration of the supported kind that is otherwise complete, and it also
carries any one of the values that belong only to the unsupported kind (its
sign-in address, its issuer or its signing certificate), then the request is
rejected with a client error naming that value, and afterwards no configuration
exists for that domain. The test is run once for each such value. Given a stored,
working configuration for the domain, when the same submission is made, then it
is rejected the same way and the stored configuration is unchanged (D-8).
`[contract]`

**Active state**

AC-9. Given an active SSO configuration, when a permitted caller suspends it, then:

- sign-in detection for an address in that domain does not answer SSO. The test
  asserts only that negative. It does not assert which other answer detection
  gives, because the fallback can depend on an outbound network call;
- the SSO sign-in start route reports no configuration for that domain;
- the configuration can still be retrieved, and it reports itself suspended.

When a permitted caller resumes it, detection routes to SSO again and the
configuration reports itself active.  `[e2e]`

AC-10. Given an active configuration with a client secret, pointing at a working
identity provider, when it is suspended and then resumed, with no secret supplied
at either step, then a sign-in through that provider that succeeded before the
suspension completes and issues a session afterwards. Every described value of
the configuration other than its active state is unchanged.  `[unit]`

AC-11. Given an existing configuration, when a configuration write replaces it,
then afterwards its active state is what it was before: active stays active and
suspended stays suspended (D-3). Given no existing configuration, when a
configuration write creates one, then it is active.  `[contract]`
*(The active-stays-active half and the creation half guard existing behaviour:
the active state is hardcoded true today (§2.4). The suspended-stays-suspended
half is new, and today it cannot even be arranged through the API.)*

AC-12. Given the described interface, when the operation that changes only the
active state is examined, then no request it accepts carries any configuration
value. The configuration values are exactly these eight: the provider's display
name, the kind of provider, the issuer, the client identifier, the client secret,
and the three values of the unsupported kind (its sign-in address, its issuer and
its signing certificate). The identifier of the configuration the operation acts
on is **not** a configuration value. The operation may, and must, name its
target the same way removal does. The requested active state is not a
configuration value either.  `[contract]`
*(This is the structural guarantee behind AC-10. It fails today because no such
operation exists, and it would fail again if suspension were later folded back
into the configuration write.)*

AC-15. Given an active configuration, when a member who is not a permitted caller
asks to suspend it, then the request is refused and the configuration is still
active. The same holds for resuming a suspended configuration: it stays
suspended.  `[contract]`

AC-19. Given no SSO configuration for the caller's own organisation domain, when a
permitted caller asks to suspend it, then the request is refused with a "not
found" outcome. Afterwards no configuration exists for that domain, no record of
a transition exists, and the caller's write authorization is spent (D-9). The
same holds for resuming.  `[contract]`

AC-20. Given a suspended configuration, when a permitted caller asks to suspend it
again, then the request succeeds and reports the configuration as suspended.
Every described value of the configuration, including its active state, is
unchanged, and the caller's write authorization is spent (D-9). The same holds
for resuming a configuration that is already active, which stays active.
`[contract]`

AC-21. Given a permitted caller of organisation A, when they ask to suspend an
active configuration whose domain is not A's own domain, then the request is
refused and that configuration is still active. The test is run for two targets:
the domain of another organisation B, and a subdomain of A's own domain, with the
subdomain configuration arranged through storage. The mirror case holds for
resume: a suspended configuration of either target stays suspended. In each case
no record of a transition exists afterwards.  `[contract]`

AC-16. Given an active configuration, when a permitted caller suspends it and then
resumes it, then for each of the two transitions a record exists, in the place
PAC-50 records configuration writes, naming the transition, the organisation and
the acting user. A record of the kind PAC-50 already uses for configuration
writes, with the transition named in its details, satisfies this criterion. A
new kind of record is not required (§6, D-10).  `[unit]`

**Removal**

AC-13. *Withdrawn in v3.1.* Removing a configuration that does not exist is
PAC-50's AC-16c, and PAC-50's implementation marks API-067 fixed. One change is
not specified twice.

**Validation and the write authorization**

AC-17. Given a permitted caller holding exactly one unspent write authorization
under PAC-50's rule, when they submit a configuration that AC-7 or AC-8 refuses,
and then submit a corrected configuration without obtaining a new authorization,
then the first submission is refused as AC-7 or AC-8 says, and the second
succeeds.  `[unit]`

**Preserved sign-in behaviour**

AC-14. Given an active configuration pointing at a working identity provider,
when a user whose email domain is the configuration's domain signs in through it,
then the sign-in completes and a session is issued.  `[unit]`
*(This guard already exists: `TestOIDCCallback_AcceptsEmailInProviderDomain`
(`backend/api/handlers_sso_test.go:172`), which asserts the session cookie
positively. The criterion is that it stays green unchanged. Stored configurations
must keep signing in after this change.)*

**How AC-9, AC-10, AC-14 and AC-16 are proven.**

- **AC-9** is an API-level e2e scenario. It needs no identity provider: detection
  and the start route's "not configured" answer never contact one. It does need a
  permitted caller in the e2e stack, arranged through whatever PAC-50 provides
  there.
- **AC-10, AC-14 and AC-16** are handler-level tests against a real database. The
  in-process fake identity provider that the sign-in tests already use
  (`handlers_sso_test.go:30-93`) stands in for the provider. A full e2e sign-in
  would need an identity provider that the e2e stack does not run on `main`, and
  this spec does not make adding one its job.

## 5. Explicitly out of scope

- **Who may write an SSO configuration, and for which domains.** This is PAC-50,
  covering both the gate and the exact-domain rule. This spec only requires that
  its new transition is governed by the same rule (§3.6).
- **Reporting a removal that removed nothing.** PAC-50 (AC-16c, API-067).
- **Restricting who may *read* the configuration.** Once the secrets are gone,
  what remains is that the organisation uses SSO, the provider's name, its issuer
  and client identifier, and whether it is active. Any member learns the first two
  by signing in. PAC-50's contract does not gate the list beyond membership.
  Narrowing that is a privilege decision, and PAC-50 is where such decisions live.
  It does not widen access, so the factory does not take it here.
- **Encrypting the secrets at rest.** They are plain text in the database (§2.1).
  This spec stops them crossing the API boundary, which is the reported finding.
  Encryption at rest is separate, larger work with its own key-management
  questions.
- **Ending sessions when SSO is suspended, removed or reconfigured.** Sessions are
  self-contained week-long bearer credentials with no state on the server, so
  nothing in this spec can revoke one. §3.5 requires that this be said plainly
  rather than fixed.
- **Rotating secrets already exposed.** See D-4. Production has no users, so there
  is nothing to rotate there. Any other deployment is its operator's to handle.
- **Making SAML work.** This spec refuses SAML at submission (D-1). Implementing
  SAML is a feature.
- **Deciding whether this surface should exist at all** (API-091, audit batch E3).
  The leak cannot wait for that decision.
- **Building a UI for SSO configuration.** PAC-50 §3 records that follow-up.
- **Updating API-010's stale audit row** (§2.7). It belongs to whoever closes
  PAC-35. It is noted here only so that nobody reads "Confirmed" as a blocker on
  AC-9.

## 6. What must not change

- **SSO sign-in keeps working for configurations stored before this change.** The
  guard is `handlers_sso_test.go:172` and the PAC-35 status tests (§2.6), which
  must stay green unchanged.
- **No stored data changes, and there is no migration.** The secrets stay in the
  database; only what crosses the boundary changes. Suspend and resume need no
  migration either: the column they act on already exists and already works as a
  kill switch (`008_sso_providers.up.sql:6`, read at `sso_providers.go:51`).
  Recording the transitions (AC-16) needs no migration: PAC-50's event table
  accepts only two kinds (§2.8), and a transition is recorded under
  `sso_provider_written`, with the transition named in the details' operation
  field (D-10). If a migration appears anywhere in this work, scope has grown and
  should be challenged rather than accepted. PAC-50's own migration is PAC-50's.
- **The configuration write stays a replace for every configuration value**
  (§3.2). `backend/storage/sso_providers_test.go:59` (`TestUpsertSSOProvider_Update`)
  protects that today. The one deliberate change is that the active state is no
  longer a value of the configuration write (§3.3). If the storage test asserts
  otherwise, it changes for that one property only, and the plan says so.
- **Sign-in routing reads the same rows** (`backend/auth/sso_detect.go:37`). It
  is unaffected except where AC-9 deliberately changes it. It is protected at the
  status level by `pac35_auth_flows_test.go:803`.
- **The secrets do not become recoverable by another route.** Making them
  write-only means someone who forgets a client secret must re-enter it (§3.1).
  That is the intended trade, and it must not be softened later with a "reveal"
  feature.
- **Configurations already stored are never deleted or rewritten** by the new
  validation (§3.4, D-1).
- **The write path's permissiveness is PAC-50's.** Nothing here should be read at
  stage 6 as this spec having reviewed it.

## 7. Rollback

**There is no migration and nothing to roll back in the database.** Nothing here
changes stored data, and the secret columns keep their values throughout.

A revert brings the leak back, in full, for every organisation. So a revert is not
a neutral act: treat it as reopening a critical finding, not as a rollback. If a
defect is found after release, prefer fixing forward.

Two things do not roll back cleanly:

1. **Secrets entered after release cannot be read back through the API.** They
   are in the database, so nothing is lost. Anyone who relied on reading them back
   must go to the database. This is by design (§3.1).
2. **A configuration suspended after release is a state the pre-release code
   cannot produce, though it can read it.** The sign-in lookup already filters on
   the active state, so a reverted deployment would treat a suspended
   configuration as unconfigured and offer no way to resume it. Worse, the
   pre-release configuration write hardcodes active, so the first replace after a
   revert would silently resume it. Before reverting, resume anything that should
   be active, and remove anything that must stay off.

The other changes a consumer could notice all revert with the code, because no
stored state depends on them: the removed secret properties, the key casing, the
response container, and the refusal of configurations that used to be accepted.

## 8. Open questions

v3's four open questions are answered under **Decisions**: OQ-1 as D-1, OQ-2 as
D-4, OQ-3 as D-7, OQ-4 as D-4. All are factory assumptions pending owner
confirmation. One new question remains, and it does not block the contract:

**OQ-5: Is two writes to fix-and-resume acceptable?** *Owner: human (product),
non-blocking.* D-3 means fixing a suspended configuration and resuming it takes
two writes. Under PAC-50, each write spends one step-up authorization, so the
person fixing it re-authenticates twice. The alternative, where a replace
resumes, is what v3 proposed. The factory did not choose it, because it lets
credential entry silently re-enable a login path that a colleague deliberately
turned off. If the owner prefers v3's behaviour, AC-11's suspended half is
inverted and §7 item 2 simplifies. Nothing else changes.

**OQ-6: May an OIDC public client (PKCE, no client secret) be configured?**
*Owner: human (product), non-blocking.* D-2 requires a non-empty client secret on
every configuration write. A provider registered as a public client therefore
cannot be configured at all. The factory chose this because sign-in today always
sends the secret in the server-side code exchange (`handlers_sso.go:64-68`,
`:121-125`), so a configuration without one cannot complete a sign-in. If the
owner wants public clients, that is a sign-in feature (PKCE) as well as a change
to D-2, and belongs in its own spec.

**OQ-7: Is it acceptable that the kill switch may need the operator?** *Owner:
human (product), non-blocking.* D-6 puts suspend and resume behind PAC-50's
step-up. PAC-50 offers that step-up only through Google or Microsoft (its grant
table's provider check on `origin/impl/pac-50-sso-write-gate-backend`) or through
the deployment's break-glass list. An organisation whose SSO administrators are
not Google or Microsoft workspace administrators can therefore suspend its SSO
only by asking the operator. The factory kept D-6, because the other option lets
any member switch off the organisation's login, which widens who can act. The
owner should know the limit exists.

## Decisions

These answer v3's open questions and the gaps found while re-checking it against
`main`. Each follows the factory rule for product decisions with no recorded
owner answer: take the most conservative option consistent with `PRODUCT.md` and
earlier owner decisions in `docs/specs/*.md`, and never choose an option that
deletes user data or widens who can see data. Each is **assumed by the factory,
pending owner confirmation** (2026-10-27).

- **D-1 (answers OQ-1): the kind of identity provider the product cannot complete
  a sign-in with is refused at submission.**
  - **Why.** It is the honest behaviour (§3.4), and it is what PAC-35 assumed this
    spec would do (`docs/specs/PAC-35.md`, D-5: "refusing SAML and incomplete OIDC
    rows at create time … PAC-22 owns").
  - **What v3 feared.** Its worry was that someone might be staging such a
    configuration ahead of the feature. No such person can exist: production has
    zero users (PAC-49 §2 item 1, human, 2026-10-21).
  - **Rows already stored.** None are deleted or rewritten. They stay readable,
    removable and suspendable, and sign-in keeps refusing them exactly as it does
    today (AC-8, §6).
  - **This narrows behaviour.** Something previously accepted is refused. It
    widens nothing.
  - Assumed by the factory, pending owner confirmation.
- **D-2: for the kind of provider that works, the values it needs are the issuer,
  the client identifier and the client secret.** All three are used to complete a
  sign-in today (`handlers_sso.go:64-68`, `:121-125`). Each is required, non-empty,
  on every configuration write (§3.2). The contract names them. Requiring the
  secret is the conservative reading: a configuration without one is the
  "configured but no secret" state AC-3 exposes, and it cannot complete the
  server-side code exchange. It also means a public client (PKCE, no secret)
  cannot be configured. That is OQ-6. Assumed by the factory, pending owner
  confirmation.
- **D-3: the configuration write preserves the active state; only the transition
  changes it.**
  - **Why.** This replaces v3 §3.3, which had a replace resume a suspended
    configuration. v3's choice was an author's choice, not an owner's.
  - **Why it is conservative.** Preserving the state is the more conservative of
    the two: entering credentials never re-enables a login path that someone
    deliberately turned off. A new configuration starts active, as today, so
    nobody turns SSO off by accident either.
  - **The cost** is OQ-5.
  - Assumed by the factory, pending owner confirmation.
- **D-4 (answers OQ-2 and OQ-4): no out-of-tree consumer is assumed, and no
  customer rotation notice is owed by this change.**
  - **Production has zero users** (PAC-49 §2 item 1, human, 2026-10-21). So no
    production organisation has configured SSO, no production secret has been
    exposed, and no production consumer of the current shape exists.
  - **The release.** The release-manager (stage 7) confirms, by a count of stored
    SSO configurations and never by reading values, that this still holds for
    production at release time.
  - **If the count is non-zero**, the release does not proceed until a human names
    who tells those organisations to rotate. The sequencing constraint from v3
    still applies: rotation means writing a new secret, which means passing
    PAC-50's gate.
  - **Other deployments** (development, staging, self-hosted) are their operators'
    to handle. The release note says that any secret configured before this
    release must be treated as exposed.
  - Assumed by the factory, pending owner confirmation.
- **D-5: PAC-50 lands before PAC-22, and PAC-22's contract is written on top of
  PAC-50's.**
  - **Why.** This adopts the ordering decided on 2026-10-22 (PAC-50 §4.9), and it
    is the order PAC-50's implementation PR (#212) already assumes. That PR pins
    today's thirteen-key response in a test and leaves the projection to this spec
    (§2.8).
  - **If PAC-22's contract is drafted while PAC-50 is unmerged**, it is drafted
    against PAC-50's contract. It is rebased onto `main` once PAC-50 merges, and
    its gate counts only on the rebased commit.
  - Assumed by the factory, pending owner confirmation.
- **D-6: suspending and resuming are SSO writes.** They are authorized by
  PAC-50's rule for configuration writes and recorded where PAC-50 records them
  (§3.6, AC-15, AC-16). The alternative, any member, would let every employee
  switch off the organisation's login. Assumed by the factory, pending owner
  confirmation.
- **D-7 (answers OQ-3): API-001 stays one audit row and is annotated, not split.**
  - **Precedent.** PAC-50's implementation already annotates the row with its
    write half closed (§2.8), and PAC-35's D-7 split API-092 the same way.
  - **When PAC-22 merges**, its implementer marks API-001 **Fixed**, citing
    PAC-50 for the write half and PAC-22 for the disclosure half, with the tests
    that prove AC-1 and AC-2. Reads by any member remain, but they no longer
    disclose anything secret. The row's consequence ("can read the org's OIDC
    client secret and SAML signing cert") then no longer holds, and the read
    restriction is recorded as out of scope (§5).
  - **The implementer also** marks API-060 and API-066 Fixed (PAC-22), marks
    API-092 Fixed once the list's 200 and 403 are asserted, and re-cuts audit
    batch B1 (`api-audit.md:526-530`) along the PAC-22/PAC-50 line.
  - Assumed by the factory, pending owner confirmation.
- **D-8: a submission of the supported kind that carries any value of the
  unsupported kind is refused, not stored and not discarded** (AC-18).
  - **The three options.** Such a submission could be refused, accepted with the
    values stored, or accepted with the values silently dropped.
  - **Why refused.** Storing would keep a secret, the certificate, that nothing
    uses and nobody can read back. Dropping would tell the caller that something
    they supplied was accepted when it was thrown away. Refusing tells them at
    once, which is §3.4's principle, and stores nothing.
  - **What it costs.** An accepted write of the supported kind stores the
    unsupported kind's values as absent. Today's replace does exactly that when
    they are left out (§3.2), so this deletes nothing that today's replace would
    keep. Configurations already stored are not rewritten by this rule (§3.4).
  - **Consequences.** AC-2's success case carries only a client secret. AC-3
    gains a certificate indicator, which describes configurations stored before
    the change (§3.1).
  - **The authorization.** The refusal is decided from the request alone, so,
    like every other validation refusal, it leaves PAC-50's authorization
    unspent (§3.4, AC-17).
  - Assumed by the factory, pending owner confirmation.
- **D-9: the transition's error paths** (§3.3, AC-19, AC-20, AC-21).
  - **No configuration.** The transition is refused as "not found", creates
    nothing and records nothing. Anything else would recreate API-067 on the new
    operation.
  - **Already in the requested state.** The transition succeeds without change
    and reports the actual state. A refusal was considered and rejected: in an
    emergency it would make two people suspending at once look like a failure,
    and it tells the caller nothing truer than "it is suspended". The success
    can never be for a configuration that does not exist, because that case is
    refused first.
  - **The authorization.** It follows PAC-50's rule unchanged. Refusals decided
    from the request alone leave it unspent. Outcomes that need the stored
    configuration spend it, as PAC-50's D-5 does for a removal that finds
    nothing. This is consistent with §3.4: "a refused configuration costs
    nothing" is about refusals decided before the authorization is consumed, and
    a missing configuration cannot be known without consulting storage.
  - **Recording.** A transition that found its configuration is recorded (AC-16),
    including one that changed nothing, because an authorization was spent on
    it. A transition that found nothing is not recorded, as PAC-50 does not
    record a removal that found nothing (§2.8).
  - Assumed by the factory, pending owner confirmation.
- **D-10: transitions are recorded under PAC-50's existing record kind** (AC-16,
  §6). They are recorded as `sso_provider_written` with the transition named in
  the operation field of its details, beside PAC-50's `create` and `delete`. A
  new kind would need a migration of PAC-50's table, and §6 treats a migration
  as scope growth. If the plan finds the existing kind cannot carry it, that is a
  finding for the spec, not a migration to add quietly. Assumed by the factory,
  pending owner confirmation.

## Revision v3.2 — 2026-10-27

This revision answers the challenge of v3.1 (History, "Challenge — 2026-10-27").
The challenge accepted v3.1 with changes. Every change it listed is applied
below, in the challenge's order. No owner answer was needed. Where a choice was
open, it is recorded as a factory assumption (D-8, D-9, D-10).

**Blocking**

1. **AC-4 contradicted D-2 and AC-7.** Its second half is restated as the
   challenge proposed: a replace supplying an empty secret is refused (AC-7) and
   the indicator stays true. D-2 is unchanged.
2. **What an accepted submission does with the unsupported kind's values.** It
   is refused (D-8, new AC-18). AC-2's success case now carries only a client
   secret, and its certificate case is a refusal that leaks neither value. AC-3
   gains a "signing certificate on file" indicator, so the certificates on
   configurations stored before the change stay visible as present or absent
   (§3.1).
3. **AC-12 forbade the transition from naming its target.** AC-12 now lists the
   eight configuration values and says that the target's identifier and the
   requested state are not among them.
4. **The transition had no error paths.** §3.3 now states them, and D-9 records
   the choice. Transitioning a missing configuration is refused, creates
   nothing, records nothing and spends the authorization (AC-19). A repeated
   transition succeeds without change, reports the real state and spends the
   authorization (AC-20). Both follow PAC-50's D-5. Refusals decided from the
   request alone leave the authorization unspent, as §3.4 says.
5. **No cross-organisation criterion.** AC-21 is added. A permitted caller of A
   cannot suspend or resume a configuration for another organisation's domain or
   for a subdomain of A's, and the target's state is unchanged. The subdomain
   case targets the suffix match still on `main` (`domain/domain.go:45-49`).

**Non-blocking**

1. **§2.8 and U-21.** §2.8 and the inherited-scope list now say that PAC-50's
   branch removes U-21 too. After PAC-50, what is left here is API-060 and
   rewriting the create request's description, which AC-7, AC-8 and AC-18 make
   false.
2. **AC-16 versus "no migration".** The existing kind, with the transition named
   in its details, satisfies AC-16. No new kind and no migration are needed
   (AC-16, §6, D-10).
3. **§3.7's rationale.** It is restated as allowing for later additions only. It
   now says that most list responses are bare sequences, and that the container
   must not be "corrected" towards them.
4. **AC-9's detection assertion** is negative only: detection does not answer
   SSO. Which fallback it gives is not asserted.
5. **The Linear issue body is stale.** The body was not rewritten: this stage
   only reads issues and comments on them. A comment on PAC-22 points at v3.2,
   says what moved to PAC-50 (the admin check, the RowsAffected fix, API-067),
   and asks the issue owner to update the body.
6. **OWNER: public OIDC clients.** Recorded as OQ-6 and noted under D-2. D-2 is
   unchanged, pending the owner.
7. **OWNER: two writes, two step-ups.** OQ-5 now says that each write needs its
   own step-up.
8. **OWNER: the kill switch may need the operator.** Recorded as OQ-7. D-6 is
   unchanged, pending the owner.

## Revision v3.1 — 2026-10-27

v3 was never challenged, so this revision answers no challenge. It brings v3 up to
date with what landed after 2026-09-17 and answers its open questions:

- **Citations.** Every citation in §2 was re-read on `main` at `6d751f9`. The v3
  citation basis (`6f7a856`, and "PAC-45 not merged" with its erratum) is gone.
- **What other specs settled.** PAC-35 fixed API-010 (in the web app) and API-011,
  and added sign-in tests, so AC-14 is now a guard that already exists. PAC-21
  stopped echoing raw errors.
- **Moved to PAC-50.** AC-13 (API-067), because PAC-50 specifies and implements
  it.
- **Decisions.** OQ-1 to OQ-4 are answered as D-1, D-4, D-7 and D-4. D-2, D-3, D-5
  and D-6 close gaps the new landscape opened. D-3 reverses v3's
  "a replace resumes".
- **New criteria.** AC-15, AC-16 and AC-17.
- **Proof levels.** AC-10 and AC-14 are now `[unit]` against the in-process fake
  identity provider. AC-9 stays `[e2e]` and needs none.

---

# History

The two `spec-challenger` rejections below are kept verbatim for the record. They
review **v1** and **v2** of this document, not the spec above. Their section
numbers, AC numbers and `file:line` citations refer to those versions and to the
codebase as each was written against; do not read them against v3. The v2
challenge's central recommendation — split the spec at the authorization boundary
— is what produced v3 and PAC-50. v3.1 refreshes v3. Its challenge, the last
entry below, accepted it with changes, and v3.2 (above) applies them. That
challenge's AC numbers and section references are v3.1's.

---

## Challenge — 2026-09-16 (applies to **v1** of this spec)

> **Historical.** This is the `spec-challenger`'s rejection of the v1 spec, kept
> verbatim for the record. It is **not** a review of the spec above. Sections,
> AC numbers and file:line citations in it refer to v1 and to the pre-PAC-45
> codebase; do not read them against v2. Where v2 resolves a finding it says so
> and cites the finding by number ("the challenge's blocking finding N").

---


**Verdict**: reject

Reject is not "start over". §2 is the most accurate current-behaviour section I have
checked in this register's specs — I verified all 31 `file:line` citations and found
one materially incomplete and none wrong. The reject is for §0/§1/§3 and AC-13: the
spec's central argument is that deferring authorization is tolerable because AC-10
reduces the write path, and that argument rests on a severity assessment I verified
to be **understated by a whole tenancy boundary**. AC-13 as written freezes the
defect that makes it understated. That argument has to be re-made against the
corrected facts, and three ACs have to be reconciled with an operation that does not
exist, before a contract author can proceed without guessing.

Nothing was executed here either; `proxy.golang.org` is blocked in my sandbox too.
Every claim below is from reading source and carries a `file:line`.

### Blocking

1. **The write path is not "org takeover" — it is deployment-wide authentication
   bypass, and AC-13 freezes it.** §0 and §1 say pointing an org at an
   attacker-controlled IdP is "a full account-takeover primitive for every user in
   that org". It is worse than that. `oidcCallback` never checks that the email the
   IdP asserts has anything to do with the domain the provider was configured for
   (`backend/api/handlers_sso.go:168-186`). It passes the asserted address straight
   to `UpsertUser`, which is `ON CONFLICT (email) DO UPDATE ... RETURNING id`
   (`backend/storage/users.go:21-35`), so for an address that already exists it
   returns **that** user, and `issueJWT` then sets a 7-day session cookie for them
   (`backend/api/handlers_auth.go:139-155`). `provider` is not even compared — a row
   created by Google login is happily impersonated.
   End to end, verified by reading: any org member creates a provider for *their own*
   domain (passes `DomainMatchesOrg`), pointing at an issuer they control; they hit
   the public `GET /api/auth/sso/{their-domain}`, their own IdP redirects back to the
   public callback, and it asserts `victim@any-other-domain.com`. They receive a
   valid Paceday session as that victim. No victim interaction, no SSO configured for
   the victim's org, works on a deployment that has never used SSO.
   So the read leak is not "arguably the lesser half" of API-001 — it is the lesser
   half by a wide margin, and the thing AC-10 gates is not "org takeover" but "mint a
   session for any account on the deployment". This is not in the register (I checked
   API-010/API-011/API-027/API-092 and every `callback/oidc` row —
   `docs/factory/api-audit.md:244-245,261,326`).
   **Resolves it**: §1 and §0 restate the severity from verified behaviour; the
   missing binding between asserted identity and provider domain is filed as its own
   finding; and AC-13 is rewritten so that "login completes exactly as it does today"
   does not contractually preserve it. As it stands AC-13 is a test that would fail if
   anyone fixed this.

2. **Three ACs require an operation the API does not have, and a semantic change the
   spec never states.** There are exactly three routes:
   `POST /`, `GET /`, `DELETE /{domain}` (`backend/api/routes.go:171-175`). There is
   no update. AC-4 ("submitted back unchanged as an update"), AC-8 ("a request updates
   some other part of it and says nothing about whether it is active") and §3
   ("a request that says nothing about whether SSO is active leaves that as it was")
   all presuppose one. §2 never records its absence.
   Whatever carries them has to become a **merge**, because today's single write is a
   replace-upsert: every column is overwritten from `EXCLUDED`, including
   `oidc_client_secret` and `enabled` (`backend/storage/sso_providers.go:63-73`), and
   `Enabled: true` is hardcoded at the call site
   (`backend/api/handlers_sso.go:239`). Turning a replace into a merge is a
   behavioural change to an existing operation, and §6 says the opposite — "No stored
   data changes ... only what crosses the boundary changes".
   It also puts AC-4 and AC-5 in direct contradiction. AC-5 requires an OIDC
   configuration submitted without a value that kind needs to be rejected. AC-4
   requires a representation that omits the secret (because AC-1 removed it) to be
   accepted and to leave the stored secret intact. Both cannot hold unless "required"
   means "required when nothing is stored", which the spec never says.
   **Resolves it**: §2 records that no update operation exists; §3 states plainly
   whether the write becomes a merge and what that does to omitted fields on a
   *create* versus a *change*; AC-4 and AC-5 are rewritten so one does not forbid the
   other.

3. **AC-12 asserts a refusal that does not exist and that §5 forbids creating.**
   "when a member of a different organisation attempts to **retrieve** ... then the
   attempt is refused". Retrieval is `GET /api/admin/sso`, which lists by the
   caller's own `org_id` (`handlers_sso.go:266`, `sso_providers.go:82-92`). Another
   org's member is not refused; they get 200 and their own list. Making retrieval
   refuse anything requires restricting the read path, which §5 explicitly places out
   of scope. As written AC-12 cannot pass without out-of-scope work, and §6 leans on
   it as "the new guard" for cross-org isolation.
   **Resolves it**: split AC-12 — for reads, the criterion is non-appearance in the
   other organisation's listing; for change and remove, refusal.

4. **Cross-organisation isolation is not actually enforced today, so §6's baseline is
   wrong and §2.8 is filed under the wrong heading.** §6 says isolation is "currently
   enforced by the domain check". `DomainMatchesOrg` is a suffix match
   (`backend/domain/domain.go:31-35`) and organisations are created per-domain by
   `UpsertOrg` from whatever address signs up (`backend/storage/orgs.go:18-27,47`).
   Nothing stops `acme.com` and `eu.acme.com` both existing as organisations. A
   member of `acme.com` then passes the check for `eu.acme.com` and can create,
   overwrite **and delete** a *different organisation's* SSO configuration
   (`handlers_sso.go:230-233`, `:295-298`). §2.8 reports the same suffix match only as
   an invisibility problem ("a config the org's own list cannot see") and §5 defers it
   as "a question about what a domain means to an organisation". Framed as
   cross-tenant write access it is not deferrable under §6's own "what must not
   change", and combined with finding 1 it means the provider row an attacker abuses
   need not even be in their own org.
   **Resolves it**: §2 states that the domain check does not establish cross-org
   isolation when one org's domain is a suffix of another's; §6 stops claiming a
   baseline that does not hold; AC-12 states which behaviour is required for a
   subdomain that is itself an organisation.

5. **AC-10 does not buy what §0 claims, and OQ-1 is less blocked than the spec
   assumes (this is my answer to OQ-7).** OQ-1 has three parts and the spec says (c),
   bootstrap, is the hard one. But AC-10 has *already chosen* an
   operator-configured mechanism, and §8 concedes "an operator-configured list is the
   right bootstrap regardless of which model wins". An operator-configured allow-list
   of principals is the same class of mechanism, at the same deploy-configuration
   cost, as an operator-configured on/off switch — and unlike the switch it
   distinguishes somebody from anybody, which §3 admits AC-10 does not.
   The switch is also worst exactly when it matters. OQ-6's rotation sequence
   requires every affected customer to reconfigure, which requires the switch **on**;
   while it is on, any holder of a corporate-domain address has the primitive in
   finding 1. So the interim mitigation is off when there is nothing to protect and on
   during the one window when the whole customer base is being reconfigured.
   I am not asking for a design. The requirement the spec is missing is: **in the
   interim, changing SSO configuration must be possible only for a principal the
   deployment operator has designated — not for any org member in a deployment where
   a capability has been switched on.** If that requirement is accepted, OQ-1(c) is
   answered in passing and the case for splitting off the gate weakens to what §8
   already predicted.
   On the split itself: I agree with it, for the reason §0 gives — non-disclosure is a
   projection, needs no decision, and the leak is live. "The DTO ships now, the gate
   later" is strictly safer than shipping nothing and does not degrade detection,
   because the issuer stays readable, so an org can still see a foreign IdP in its
   listing (unless it is the invisible subdomain row of §2.8). Keep the split. Replace
   the mitigation.

6. **Inherited scope is understated: two open findings on operations these ACs act
   on are not listed.**
   - **API-092** (`api-audit.md:326`) — "No Go test coverage ... all
     `/api/admin/sso`". Every operation this spec touches carries it. §2.9 describes
     the absence of tests in detail but never names the finding, and it is absent from
     the inherited-scope list. Under touch-it-fix-it it is squarely in scope, and it is
     the *one* finding this spec closes almost as a side effect.
   - **API-010** (`api-audit.md:244`, high, Confirmed) — the frontend gates the SSO
     redirect on `res.domain`, which the backend never sends, so an SSO-configured
     user can never reach the SSO flow at all. AC-7 and AC-13 both assert behaviour of
     `/api/auth/detect` and `/api/auth/sso/{domain}`, the two operations this finding
     is filed against. It also undercuts §1: "a misconfiguration that is locking
     people out cannot be paused while it is diagnosed" describes a journey no user
     can currently enter through the UI.
   **Resolves it**: add API-092 to the resolved list; state a position on API-010 —
   either in scope, or explicitly not, with the consequence for how AC-7 and AC-13 are
   proven, noted.

### Non-blocking

1. **No SSO configuration change is recorded anywhere.** There is no
   `WriteAuditLog` call on any of the three handlers — the seven call sites are focus,
   scheduling and NLP only (`engine/focus_time.go:106`, `engine/compression.go:178`,
   `api/handlers_schedule.go:188`, `nlp/parser.go:221`, and three others). Substituting
   an org's identity provider leaves no trace but `updated_at`. §3 and §4 say nothing
   about whether an SSO configuration change must be observable. Given findings 1 and
   5, the missing requirement is that a change to SSO configuration be recorded; where
   it is recorded is a later question (the audit log itself is unscoped — API-005).
2. **"Suspend" is not a kill switch and the spec's language invites the opposite
   reading.** §3 sells `enabled = false` to an administrator "investigating a
   misconfiguration that is locking colleagues out", and AC-7 covers detection and the
   login route. Nothing ends sessions already established through the provider: the
   JWT is a self-contained 7-day cookie with no server-side session state
   (`handlers_auth.go:139-155`). An operator who reaches for suspend after a
   compromise will find it stops new logins only. Worth stating in §3 so nobody
   expects otherwise.
3. **§3's actor is a role §5 says does not exist.** §3 is written throughout as "An
   administrator can see...", "An administrator investigating...", while §5 places the
   administrator concept out of scope and §3's last paragraph says reading stays open
   to org members. AC-7 inherits it ("when an administrator suspends it"). A reader
   cannot tell who AC-1's and AC-7's actor is, and under AC-10's default AC-7 is not
   reachable at all. Name the actor as an org member in a deployment where the write
   capability is available, or the tests are ambiguous about which configuration they
   run under.
4. **AC-3 can be satisfied by accident.** "the responses differ in a way that
   identifies which has a secret on file" is passed by two responses that differ only
   in `UpdatedAt`. The difference needs to be required to *mean* that, and to not vary
   for other reasons.
5. **§3 promises a stable, conventional response shape; no AC tests the envelope.**
   The list today encodes a bare top-level JSON array (`handlers_sso.go:276`). No
   criterion distinguishes an array from an object, so that part of §3 is
   unfalsifiable as written.
6. **The 403 these handlers return is also their database-error path.**
   `err != nil || user == nil || user.OrgID == nil` all produce 403 "org membership
   required" (`handlers_sso.go:195-198`, `:261-264`, `:285-288`). A test for AC-12 or
   AC-10 that asserts 403 can pass for the wrong reason. Worth a word so the criteria
   are written against a distinguishable outcome.
7. **AC-4 and AC-8 are labelled `[unit]` but describe HTTP round trips.** Cosmetic,
   but it will mislead stage 3 about where the test lives.
8. **§0 cites the interim mitigation as AC-8.** It is AC-10. AC-8 is the
   omission-does-not-suspend criterion. §3 and §5 get it right.

### Criteria I could not falsify

- **AC-4** — not falsifiable as written: there is no update operation to submit a
  representation back to (`routes.go:171-175`), and against the only write that
  exists the criterion cannot be distinguished from AC-5's requirement that a missing
  required value be rejected. Blocking 2.
- **AC-12 (the retrieve clause)** — no test can make a cross-org *retrieval* be
  "refused" without work §5 forbids. The change and remove clauses are falsifiable
  today. Blocking 3.
- **AC-6** — conditional on OQ-2 by the spec's own admission, so it is a candidate
  criterion, not one. Acceptable only because the spec flags it and dates the decision
  ahead of the contract; noted so the stage-1 gate is not recorded as passed while it
  stands.
- **AC-7** — falsifiable in substance, but unreachable under AC-10's default and its
  actor is undefined, so the scenario cannot be written without choosing which
  deployment configuration it runs under. Non-blocking 3.

Every other criterion I could write the failing test for. Worked examples, to show
the check was real: **AC-1** — insert a provider with a non-empty secret and cert,
`GET /api/admin/sso` as a member of that org, assert no value in the body equals
either; fails today because the slice of `storage.SSOProvider` is encoded directly
(`handlers_sso.go:276`) and the struct has no tags
(`storage/sso_providers.go:12-26`). **AC-9** — `DELETE /api/admin/sso/{domain}` for a
domain in the caller's org with no row; fails today because `DeleteSSOProvider`
discards the result (`storage/sso_providers.go:108-112`) and the handler returns 204
regardless (`handlers_sso.go:300-304`). **AC-10** — with no capability configured,
`POST /api/admin/sso` as an org member; fails today because the only gate is
`user.OrgID == nil` (`handlers_sso.go:195-198`). **AC-14** — a row with empty secret
and cert, retrieve, assert it validates and reports no secret on file; fails today
because there is no described shape that says so. None of these tests exist: §2.9's
claim is correct, and I verified it rather than taking it — the only `api/*_test.go`
hits for "sso" are substring matches inside `mockCompressor` and `contractCompressor`
(`api/handlers_schedule_test.go:18`, `api/interfaces_contract_test.go:47`).

### Citations I checked and found wrong

I verified every `file:line` in §2 and §6. One is materially incomplete; the rest are
accurate, including all five migration citations, both `x-uncertain` line ranges, the
`MIGRATION.md` rows, the five storage test line numbers, and all three `team_members.role`
readers (`api/handlers_teams.go:538`, `api/handlers_manager.go:327`, `:575`).

- **§2.4: "It is called on the SSO login path (`handlers_sso.go:183`)"** — incomplete,
  and in a way that weakens the spec's own argument. `AssociateUserWithOrg` has three
  call sites: the Google callback (`api/handlers_auth.go:76`), the Microsoft callback
  (`api/handlers_microsoft_auth.go:74`) and the SSO callback (`handlers_sso.go:183`).
  As cited, self-service membership looks circular — you would already need SSO to
  obtain org membership. The real path is ordinary Google sign-up, which is how an
  attacker with a corporate-domain address becomes an org member of a deployment that
  has never configured SSO. §2.4's conclusion ("the current gate reduces to 'has a
  corporate email address'") is correct, and the Google callback is the citation that
  establishes it.

Two claims the spec marks as inferences, which I could not resolve either and which
remain open: the PascalCase key casing (U-22) and, by extension, §2.1's response-shape
description. I read the same struct and reached the same inference. Neither of us has
observed a response.

Things I looked for and did **not** find, so they are not findings: secrets do not
reach Sentry — `scrubRequest` sets `req.Data = ""` and drops cookies wholesale, and
bodies are never attached (`backend/sentry.go:61-78`, `:100-107`); the request logger
emits method, path and duration only (`backend/api/middleware.go:86-91`); no
`WriteAuditLog` call carries a secret; and `NewOIDCClient`'s error, echoed raw into a
500 (`handlers_sso.go:113`), is built from the issuer, not the secret. §2's silence on
these channels turns out to be correct, and the spec could say so — "the boundary is
the only leak" is a stronger claim than "the boundary leaks".

### Consumers this breaks

- **`/api/admin/sso` — none, confirmed independently.** No hit for the route,
  `ssoProvider`, `OIDCClientSecret` or `SAMLCert` in
  `smart-calendar-flow/src` (172 TS files, so the tree is really there and really
  empty of it); no hit for `sso` in any case anywhere under `mcp/`; the only
  occurrence under `e2e/` is `e2e/TESTIDS-REQUIRED.md`. §2.9 is right and the
  PascalCase shape can change freely. OQ-3's residual out-of-tree risk stands and I
  could not reduce it either.
- **`smart-calendar-flow/src/components/auth/AuthDialog.tsx:32,99,111-113` — a
  consumer of the operations AC-7 and AC-13 act on, unnamed in the spec.** It calls
  `/api/auth/detect` and redirects to `/api/auth/sso/{domain}`. It is already broken
  by API-010 (it gates on `res.domain`, which `auth.DetectResult` never sends —
  `backend/auth/sso_detect.go:18-22`), so AC-7's user-visible effect is already the
  status quo in the UI and AC-13 cannot be driven through the frontend at all. The
  spec is right that these must be API-level scenarios; it is the conclusion "no
  consumer" that needs narrowing to the admin route, because this file is the reason
  AC-7 and AC-13 need an owner in §6's consumer analysis.

---

## Challenge v2 — 2026-09-17

**Verdict**: reject

This is a better document than v1 and most of the v1 findings are genuinely
resolved — §2.7/§3.2 settle the replace-vs-merge question that v1 could not, AC-12
is correctly split, §2.4 and §6 stop claiming a cross-org baseline that never
existed, and the one wrong v1 citation is fixed and correct. §2 remains excellent:
I re-verified every `file:line` against the PAC-45 branch and found three wrong,
all three in the contract fragment rather than in the Go source.

The reject is for four things. **PAC-45 is not merged**, so every severity claim
and the whole §0.3 argument rest on code that exists only on a branch. **§0.3's
case against the kill-switch refutes its own replacement** — designation
fails closed on every upgraded deployment, which is the availability cost §0.3
gives as the reason to strike AC-10. **The designation is underdefined in the two
ways that matter**: where it is recorded is OQ-4, and nothing forbids it being
writable through the API, which would hand it back to the corporate-email gate it
exists to close. And **five criteria, including the whole "Who may write" section,
cannot be turned into a test until a human answers an open question** — the
stage-1 gate is "falsifiable by a test that does not yet exist", not "falsifiable
once somebody decides".

Nothing was executed; the Go module proxy is blocked here too. Everything below is
from reading source at `origin/nihochart/pac-45-oidc-email-domain-binding` and from
`git log`/`git diff`, and carries a `file:line` or a command.

### Blocking

1. **PAC-45 is not merged, and the spec is written in the past tense as though it
   were.** `git log origin/main..origin/nihochart/pac-45-oidc-email-domain-binding`
   returns one commit (`b1a07bb`), and `git branch -r --contains` lists only that
   branch. PAC-45 is In Progress with PR #172 open. So `main` today is still the
   pre-PAC-45 codebase with the deployment-wide bypass live. The spec says "has
   since been filed as PAC-45 and **partially fixed**" (§ "What changed in v2"),
   "The sign-in path now binds..." (§2.2), and defends §2's citation basis with
   "that is what 'today' means by the time this spec is acted on" — which is an
   assumption about the future, not a statement about the system. OQ-9 asks the
   question but the other 1000 lines answer it. The error is already propagating:
   PAC-46's description, filed this morning, calls PAC-45 a "merged fix".
   **Resolves it**: state in §0 what is true if PAC-45 does not merge — that the
   residual in §2.3 is deployment-wide rather than domain-bounded, and that §0.3's
   stated reason for striking the kill-switch ("PAC-45 removed the unbounded part")
   no longer holds — and make PAC-45 a stated precondition on the contract stage
   rather than an open question at the bottom.

2. **§0.3's argument against the kill-switch applies verbatim to the mechanism
   that replaces it.** §0.3 strikes AC-10 partly because "a default-off switch
   would now turn a working-if-dangerous capability into no capability at all, for
   every deployment". §3.7, AC-9 and §7 item 2 require exactly that: a deployment
   that upgrades without designating anybody "refuses every such attempt... it
   fails closed", and §7 calls this "a foreseeable support incident". §0.1 gives
   the same property as the reason the admin concept must be deferred: "on the day
   it ships every existing deployment has zero administrators and SSO becomes
   unconfigurable by anybody". Nor does the rotation-window argument separate them
   — an operator who can designate themselves for the rotation window could equally
   switch a flag on for it. The one genuine difference is granularity, somebody vs
   anybody, and I accept it; it was my formulation. But two of the three legs of
   §0.3 are self-refuting, and a spec whose central argument does not survive being
   read against its own acceptance criteria cannot be handed to a contract author.
   **Resolves it**: drop the availability leg and the rotation leg, defend
   designation on granularity alone, and say why zero-designation default-deny is
   acceptable here where default-off was not.

3. **Nothing in the spec prevents a designation being granted through the API, so
   it can be self-granted.** OQ-4 leaves where a designation is recorded
   undecided, and §6 concedes "the suspend/resume and designation work may need"
   a migration — so a database-recorded designation is within the spec's own
   reading. AC-9 to AC-11 gate SSO writes; no criterion anywhere says anything
   about writes to the designated set itself. An implementation that records
   designations in a table and later grows a route to manage them satisfies every
   criterion in §4 and reproduces exactly the bootstrap problem §0.1 defers — and
   worse, hands the "has a corporate email address" gate (§2.6) a path to
   designate itself. The mechanism's whole value is that it is out of the
   product's reach, and the spec never requires that.
   **Resolves it**: a criterion that no API operation can create, alter or remove a
   designation, and that changing the designated set requires action at the
   deployment level.

4. **The "same cost as a switch" claim, which carries the split, depends on the
   half of OQ-4 the spec declines to answer.** §0.2 and §0.3 justify shipping
   designation now on the grounds that it "costs the same deployment configuration
   as v1's on/off switch". OQ-4 then leaves open whether designation is
   per-deployment or per-organisation. Per-organisation designation on a
   multi-org deployment is a mapping from principal to organisation — i.e. a
   recorded privilege model, which is the (b) half of OQ-1 that §0.1 defers as
   needing a schema change. So the cost claim holds only under the flat-list
   reading, which the spec refuses to commit to while relying on it.
   **Resolves it**: decide the per-deployment/per-organisation question in the
   spec. AC-11 already bounds the blast radius of a flat list, so this is a scope
   decision the spec can make, not a product decision it must wait for — or, if it
   is deferred, §0.2's cost argument has to be restated as conditional.

5. **Suspend and resume cannot be expressed by anything §3.2 permits. This is v1
   blocking finding 2 moved, not resolved.** The genuinely resolved half is real
   and I want to credit it: §2.7 records that the routes are POST/GET/DELETE only
   (`backend/api/routes.go:171-175`, verified), §3.2 states the write replaces and
   refuses to make it a merge, v1's AC-4 is struck, and the AC-4 ⊥ AC-5
   contradiction is gone. But §3.2 then dismisses the alternative — "a second
   write operation, which is interface surface this spec does not need to buy its
   outcomes" — and immediately exempts suspend/resume, which AC-17 requires to work
   "without any secret being supplied at either step". §3.3 makes "required" mean
   required on every write, so the single replace cannot carry it. That leaves a
   merge or a new operation, the two mechanisms the same paragraph rejects, and
   "how it is expressed is the contract's decision" hands the contract author that
   choice without a rule. v1 was rejected because a contract author could not
   proceed without guessing; on this point they still cannot.
   **Resolves it**: say plainly that this spec requires a named state transition
   beyond the three operations that exist, so §3.2's refusal of new surface is not
   read as forbidding the one thing §3.2 exempts.

6. **AC-3 cannot be satisfied by any implementation.** "Given two SSO
   configurations... then **exactly one** described property differs between
   them". Two stored configurations necessarily differ in id, in domain — the
   column is `NOT NULL UNIQUE` (`backend/storage/migrations/008_sso_providers.up.sql:3`)
   — and in `created_at`. No correct implementation passes this test, so the
   implementer will silently reinterpret it. v1's non-blocking 4 asked that the
   indicator be required to *mean* "a secret is on file"; AC-4 already does that
   correctly against a single configuration across a write. AC-3 overcorrected.
   **Resolves it**: restate AC-3 as an assertion about the value of a named
   property on a configuration with a secret and on one without, not as a count of
   differing properties between two rows.

7. **Five of twenty-two criteria cannot be written as tests until a human answers
   an open question, and they include the whole of v2's headline new content.**
   AC-9, AC-10 and AC-11 all turn on "the operator has designated" — no test can
   arrange that precondition without knowing what a designation is or where it
   lives, which is OQ-4, owner "human (product/ops)". AC-20 requires "a record of
   that change exists" without saying where, which is OQ-8, and OQ-8 itself says
   the obvious destination is unacceptable. AC-8 is conditional on OQ-2, which the
   spec flags. The stage-1 gate in `docs/factory/README.md` §2 is that every
   criterion be "falsifiable by a test that does not yet exist" — a test that
   cannot be written at all is a different thing from one that has not been
   written. v1's challenger allowed one flagged criterion of this kind; three
   groups covering the entire "Who may write" section and observability is not the
   same concession.
   **Resolves it**: answer OQ-4 and OQ-8 in the spec, or move AC-9 to AC-11 and
   AC-20 out of PAC-22 — see the size note, where that split also fixes finding 9.

8. **OQ-5 is real, is worse than the spec states, and needs an acceptance
   criterion rather than an open question.** I verified the chain rather than
   taking it. With writes restricted to the caller's exact organisation domain
   (§3.7), a stored row whose domain is nobody's exact organisation domain is:
   unwritable — `createSSOProvider`'s check is against `org.Domain`
   (`handlers_sso.go:238-241`); unremovable — the same check on delete
   (`:303-306`); invisible — `ListSSOProvidersByOrg` joins
   `organizations o ON o.domain = s.domain` (`storage/sso_providers.go:88-90`), so
   it appears in no organisation's listing; and **live** —
   `GetSSOProviderByDomain` filters only on `domain` and `enabled = true`
   (`:47-54`) and `DetectAuthProvider` routes on the exact email domain
   (`backend/auth/sso_detect.go:26-47`), so it keeps receiving that domain's users
   and minting sessions for them. The spec's claim is correct.
   What §5 and §8 do not say is what such a row *is*. §2.4 already identifies how
   one gets created: a member of `acme.com` installs a provider for
   `eu.acme.com`, which their own listing cannot show. That is a stealth
   persistence artifact, and this change makes an already-planted one permanently
   unremovable through the API by anybody while it continues to issue sessions.
   Handling that with an open question owned by "whoever operates the existing
   deployments" and a line in §5's out-of-scope list is not enough: the stage-7
   gate reads the PR and the plan, not §8.
   **Resolves it**: an acceptance criterion stating what must be true of a stored
   configuration whose domain is nobody's exact organisation domain at the moment
   the exact-domain rule takes effect. Whether that is "it must not remain live",
   "it must remain reachable by some caller", or something else is the spec's
   decision to make; leaving it unmade is not.

9. **PAC-46 specifies half of this spec, and this spec does not mention it.**
   PAC-46 — filed 2026-09-17 08:20, three minutes before the v2 spec comment —
   requires replacing both `DomainMatchesOrg` calls at `handlers_sso.go:238` and
   `:303` with an exact comparison, names four handler tests for it, and notes
   that API-067 "is in the same three lines". That is §3.7's first requirement,
   AC-13, AC-14, AC-15 and AC-19. `grep -n "PAC-46" docs/specs/PAC-22.md` returns
   nothing. Two issues now own the same change, and PAC-46's own text asserts that
   PAC-22 "does **not** fix this one", which is false of v2 — so whichever is
   implemented second will either conflict or be closed as already done without
   anyone checking which criteria were actually proven. Per the challenger's
   "is it one spec?" check, this is the reverse failure: it is one change in two
   specs.
   **Resolves it**: one of the two owns the exact-domain rule, the other cites it
   as a dependency, and PAC-46's claim about PAC-22's coverage is corrected.

### Non-blocking

1. **§4's preamble overclaims.** "Each is falsifiable: none of these tests exist
   (§2.10) ... and each fails against current behaviour." The first half is right
   and I verified it. The second is false for four criteria, all of which are
   legitimate regression guards rather than defects: **AC-12** already holds —
   `ListSSOProvidersByOrg` joins on exact domain equality, so `eu.acme.com`'s row
   already does not appear in `acme.com`'s listing and the response already
   succeeds; **AC-18** already holds — `Enabled: true` is hardcoded at
   `handlers_sso.go:247`, so a write that says nothing about the active state
   already leaves it active; **AC-21 and AC-22** already hold on the PAC-45 branch
   and are proven by `handlers_sso_test.go:138,162,172`, as the spec's own
   footnote under AC-22 concedes while the preamble denies it. Say which criteria
   are new guards and which are regression guards; §6 already makes that
   distinction well for cross-org isolation.

2. **API-092's arithmetic contradicts itself in one sentence.** §2.10 says the row
   "names six operations", then enumerates eight: one closed by PAC-45, three
   closed here, four remaining. `api-audit.md:326` names five paths plus "all
   `/api/admin/sso`".

3. **API-011 is on the operation AC-21 and AC-22 assert and is not in the
   inherited-scope position.** `api-audit.md:245` files it against
   `/api/auth/callback/oidc/{domain}`: `issueJWT` returns without setting a cookie
   when `jwtSecret` is empty (`handlers_auth.go:139-142`). AC-22's "no session is
   issued" can therefore pass for exactly the wrong reason — the same hazard the
   spec correctly identifies for the shared 403 and guards against everywhere
   else. AC-22's "the existing account is unmodified" clause partly covers it;
   AC-21 is the real guard and should be stated as pairing with it.

4. **§2.3's chain omits a step that would strengthen it.** The callback's state
   check compares a cookie the client set against a query parameter the client set
   (`handlers_sso.go:137-147`), so it is not an obstacle to an attacker driving
   their own callback — PAC-45's own harness does precisely this
   (`handlers_sso_test.go:120-121`). §2.3 presents the attack as if the attacker
   needs to complete a normal browser flow. §6 would also be the right place to
   record that the state check is not a control, so that nobody later reads it as
   one.

5. **AC-17, AC-21 and AC-22 are `[e2e]` and require an identity provider the
   verification stack does not have.** No compose file defines one:
   `docker-compose.yml`, `.dev.yml`, `.prod.yml` and `e2e/docker-compose.e2e.yml`
   between them run postgres, backend, frontend, docs, mcp-server and nginx. The
   only OIDC provider in the tree is the in-process fake at
   `handlers_sso_test.go:30-93`. §4's closing note instructs stage 5 not to read
   the absence of a UI as permission to skip these, which is right, but stage 5
   cannot comply. The spec should name the requirement that the verification stack
   be able to act as an identity provider for the SSO criteria.

6. **The citation note does not cover the contract and audit files.**
   `contracts/` and `docs/factory/` do not exist on
   `origin/nihochart/pac-45-oidc-email-domain-binding` at all — `git diff --stat
   main...` shows the branch touches four Go files and nothing else. So the
   `auth.yaml`, `MIGRATION.md` and `api-audit.md` citations are necessarily
   against the working tree, not the branch the note names. The line numbers are
   fine (where they are not, see below); the note is just not true of them.

### On the size of this spec

~1292 lines, of which the preserved v1 challenge is 283 and the body ~1010. About
150 of those — "What changed in v2" (lines 20-70), §0.3 (117-152) and the
struck-from-v1 table (759-767) — are about the previous version of the document
rather than about the system, and are near-verbatim duplicates of the Linear
comment. `docs/factory/README.md` §8 requires the artifact to stand alone for a
reader who starts cold; a stage-3 plan author here has to reconstruct which
criteria are live from a document that argues with a version they never read. The
argument for the rewrite belongs in the Linear comment, where it already is.

It should also be split, one criterion further left than §0.4 draws the line:

- **PAC-22 keeps the projection work** — AC-1 to AC-8, AC-16 to AC-19. Every one
  of these is writable as a failing test today, with no dependency on OQ-1, OQ-4
  or OQ-8. This is the half that is live, decision-free and urgent, and it is the
  half §0.1's own argument covers.
- **A second spec takes the write-authorization work** — §3.7, AC-9 to AC-11, and
  §3.8/AC-20 — which is blocked on OQ-4 and OQ-8, and which is where PAC-46's
  exact-domain rule belongs so that two issues stop owning one change.

That split resolves blocking findings 7 and 9 and removes the reason for most of
§0. It does not weaken the security argument: designation is still required, just
in the spec that can say what it is.

### Criteria I could not falsify

- **AC-3** — no implementation can pass it. Two configurations always differ in
  more than one described property, because `domain` is `NOT NULL UNIQUE`
  (`008_sso_providers.up.sql:3`) and id and `created_at` differ too. Blocking 6.
- **AC-9, AC-10, AC-11** — the precondition "the operator has designated
  nobody / one member and not another" cannot be arranged in a test, because OQ-4
  leaves what a designation is and where it lives undecided. The criteria are
  well-formed; they are not yet expressible. Blocking 7.
- **AC-20** — "a record of that change exists" names no destination, and OQ-8
  says the obvious one is unacceptable. A test cannot assert the existence of a
  record without knowing where to look. Blocking 7.
- **AC-8** — conditional on OQ-2 by the spec's own admission, as v1's AC-6 was.
  Acceptable only because it is flagged and dated ahead of the contract; noted so
  the stage-1 gate is not recorded as passed while it stands.
- **AC-17** and **AC-21** — falsifiable in substance, but not with the stack that
  exists; see non-blocking 5.

Every other criterion I could write the failing test for, and I checked rather
than assumed. Worked examples: **AC-2** — `POST /api/admin/sso` with a client
secret, assert no value in the 201 body equals it; fails today because the handler
encodes the stored struct directly (`handlers_sso.go:262`) and the struct has no
tags (`storage/sso_providers.go:12-26`). **AC-7** — `POST` an `oidc` provider with
an empty `oidc_issuer`; fails today because validation stops at three non-empty
fields and a type enum (`handlers_sso.go:223-230`) and the issuer is persisted
empty (`:248`, column default `''` at `008_sso_providers.up.sql:7`). **AC-13** —
as a member of `acme.com`, `POST` a provider for `eu.acme.com` and assert
`eu.acme.com`'s issuer and client id are unchanged; fails today because
`DomainMatchesOrg` is `d == orgDomain || strings.HasSuffix(d, "."+orgDomain)`
(`domain/domain.go:45-49`) and the upsert overwrites every column from `EXCLUDED`
(`sso_providers.go:63-73`). **AC-19** — `DELETE` a domain in the caller's own org
with no row; fails today because `DeleteSSOProvider` discards the result
(`sso_providers.go:108-112`) and the handler returns 204 regardless
(`handlers_sso.go:308-312`). **AC-16** — suspend, then assert detection and the
sign-in route both report nothing while the listing still shows the row; coherent,
because `GetSSOProviderByDomain` filters `enabled = true` (`:51`) and
`ListSSOProvidersByOrg` does not (`:82-92`).

### Citations I checked and found wrong

I re-verified every `file:line` in §2 and §6 against
`origin/nihochart/pac-45-oidc-email-domain-binding`. Three are wrong, all three in
the contract fragment; every citation into Go source, SQL and the audit register
is accurate.

- **§2.1: "the same inference the contract records as unverified in U-22
  (`contracts/openapi/paths/auth.yaml:871-874`)"** — U-22's `x-uncertain` on
  `SsoProviderResponse` is at `auth.yaml:885-888`. Lines 871-874 are the tail of
  the `OIDCClientSecret` description and the `SAMLEntryPoint`/`SAMLIssuer`
  properties. The claim about what U-22 says is correct; the line range points
  somewhere else.
- **§2.1: "The list response's top level is a bare JSON array
  (`handlers_sso.go:284`, `auth.yaml:521-526`)"** — the array is at
  `auth.yaml:535-537` (`type: array` / `items: $ref SsoProviderResponse`), under
  the 200 described at `:530-531`. Lines 521-526 are `createSsoProvider`'s 500
  response body and the `get:` key. The Go citation is right.
- **§2.7: "This is U-21 (`auth.yaml:810-813`) and API-060"** — U-21's
  `x-uncertain` on `SsoProviderCreateRequest` is at `auth.yaml:824-827`. Lines
  810-813 are the SAML-501 description and two property names. Both are relevant
  to §2.7's paragraph, but the sentence identifies 810-813 as U-21 and it is not.

Imprecise rather than wrong, noted because §2's value is its precision: **§2.1's
`auth.yaml:815-870`** for "the contract documents the full PascalCase shape" —
`SsoProviderResponse` runs `:829-888`; 815-828 belongs to the request schema, and
the quoted comment ("because it is what the code does, not because it is
intended") ends at 871, one line past the range.

Everything else in §2 and §6 I verified and found accurate, including: all five
migration citations (`006:3-12`, `007:1-6` and `:8`, `008:7-12` and `:9,12`,
`013:3` and `:9-15`, `016:1-7`); `routes.go:171-175` and the absence of any update
route; all nine `handlers_sso.go` ranges, including the PAC-45 check at `:177-184`
and the three identical membership gates at `:201-206`, `:267-272`, `:291-296`;
all eleven `storage/sso_providers.go` ranges; `domain.go:28-40` and `:45-49`;
`orgs.go:18-27`, `:35-38`, `:42-52`; `users.go:21-35` (and `provider` is not in
the `DO UPDATE` set either); `handlers_auth.go:76` and `:139-155`;
`sso_detect.go:18-22`, `:26-47`, `:37`; `sentry.go:63-78`; `middleware.go:86-91`;
all seven `WriteAuditLog` call sites, exactly as enumerated, with none on an SSO
path; all three `team_members.role` readers; the five storage-test line numbers;
`handlers_sso_test.go:138,162,172` and the fake IdP at `:30-93`;
`domain_test.go:58`; `MIGRATION.md:55-57`; and `api-audit.md:239,244,325,326`. The
admin-identifier grep in §2.5 returns nothing, as claimed. §2.10's claim that no
test exercises `createSSOProvider`, `listSSOProviders` or `deleteSSOProvider` is
correct — the PAC-45 harness seeds its provider row through
`storage.UpsertSSOProvider` directly and routes only the callback
(`handlers_sso_test.go:100-124`).

The citation note's shift claim is also correct, and I checked it rather than
taking it: `git diff --stat main...origin/nihochart/pac-45-oidc-email-domain-binding`
touches exactly four files, adding 8 lines to `handlers_sso.go` (the comment and
check at 177-184) and 14 to `domain.go`, so +8 after line 176 and +14 after line
26 are both right.

### Consumers this breaks

- **`/api/admin/sso` — none, confirmed again.** No hit for the route,
  `ssoProvider`, `OIDCClientSecret` or `SAMLCert` anywhere in
  `smart-calendar-flow/src`; no hit for `sso` in any case under
  `clockwise-like/mcp/`; the only occurrence under `e2e/` is
  `e2e/TESTIDS-REQUIRED.md`. §2.10 is right and OQ-3's out-of-tree residual stands
  — I could not reduce it either.
- **`smart-calendar-flow/src/components/auth/AuthDialog.tsx:32,99,111-113`** —
  verified, and it is the consumer of the two sign-in operations AC-16, AC-21 and
  AC-22 act on. Line 111 gates on `res.domain`; `auth.DetectResult` declares only
  `Type`, `ProviderName` and `RedirectURL` (`backend/auth/sso_detect.go:18-22`),
  so the branch is dead and API-010 is confirmed exactly as §2.10 states. The
  spec's position on it is the right one and the consequence it records in §4 is
  correct.

---

## Challenge — 2026-10-27 (applies to **v3.1**, commit `5b7d3be`)

**Verdict**: accept-with-changes

The spec solves what is left of the Linear issue after its 2026-09-17 split. That
is the projection, create-time validation, and a settable active state; the admin
check and the RowsAffected fix are PAC-50's. §2 is accurate: every `file:line`
in §2.1 to §2.7 was re-read on `origin/main` at `6d751f9`, which has not moved
since the spec was written, and every one holds. The five blocking findings are
local to §3.3 and §4:

- one AC contradicts a Decision;
- one leaves the accepted request body undefined;
- one forbids the obvious shape of the new operation;
- the new operation has no error paths and no cross-organisation criterion.

None of them needs a new decision from the owner. Nothing was executed for this
review; every claim is from reading source.

### Blocking

1. **AC-4's second half contradicts D-2 and AC-7.**
   - **The conflict.** AC-4 ends: "When a write then replaces it supplying an
     empty secret, that property becomes false." D-2 makes the client secret
     "required, non-empty, on every configuration write", and AC-7 requires
     exactly that write to be rejected with nothing changed. Both cannot pass.
   - **Why it matters.** After this change no API write can produce an empty
     secret. The "false" state exists only for rows stored before the change,
     and AC-3's second half and AC-5 already arrange those through storage.
   - **Resolves it.** Delete AC-4's second half, or restate it as "a write
     supplying an empty secret is refused (AC-7) and the property stays true".
     Keep D-2 as is.

2. **The spec does not define what an accepted submission does with the other
   kind's values, and AC-2 depends on it.**
   - **The gap.** After D-1 the only accepted kind is OIDC. The create body still
     carries `saml_entry_point`, `saml_issuer` and `saml_cert`
     (`handlers_sso.go:181-183`; still declared in `ssoProviderBodyProperties`
     on PAC-50's branch). The spec never says whether an OIDC submission
     carrying a signing certificate is refused, accepted and stored, or accepted
     and discarded.
   - **If refused.** AC-2's first sentence requires a *successful* submission
     "with a client secret and a signing certificate". If the contract refuses
     SAML values on an OIDC write, which is a natural reading of D-1, AC-2 cannot
     be arranged.
   - **If stored.** §3.1 says a reader can tell whether a secret was supplied,
     but AC-3 defines an indicator only for the client secret. A stored
     certificate would be invisible and could never be inspected.
   - **Resolves it.** State which of the three behaviours applies. Then make
     AC-2's success case use only values an accepted submission may carry, and
     either extend AC-3 to the certificate or say why no indicator is needed.

3. **AC-12 as worded forbids the transition from naming its target.**
   - **The wording.** AC-12 says "nothing it requires is something the
     configuration write also requires."
   - **The conflict.** The configuration write requires `domain`
     (`handlers_sso.go:189`). Every other SSO write names its target by domain
     (`DELETE /api/admin/sso/{domain}`, `routes.go:174`). Read literally, a
     transition addressed by domain fails AC-12. So the criterion either forbids
     the conventional contract or cannot tell a pass from a fail.
   - **Resolves it.** Restrict AC-12 to *configuration values* and list them:
     provider name, provider type, issuer, client id, client secret, and the
     three SAML values. Say that the identifier of the target configuration is
     not one of them.

4. **The new transition has no error paths.**
   - **What the spec leaves out.** It specifies suspend and resume only on an
     existing configuration in the opposite state. It says nothing about:
     - (a) a transition for a domain with no configuration;
     - (b) suspending a configuration already suspended, or resuming one already
       active;
     - (c) whether a transition that changes nothing spends PAC-50's one-use
       authorization.
   - **The precedent.** PAC-50's delete spends the authorization on a 404
     (`deleteSSOProvider` on `origin/impl/pac-50-sso-write-gate-backend`: "The
     grant (if one was used) is consumed").
   - **The risk.** As written, a contract may answer success to "suspend" for a
     domain that has nothing configured. That recreates API-067 ("removal of
     nothing reported as success", which this spec moved to PAC-50) on the new
     operation. An operator in an emergency would then believe SSO is off when
     it never existed.
   - **Resolves it.** Add a criterion that a transition on a domain with no
     configuration is refused and creates nothing. State the outcome of a
     repeated transition: success that changes nothing, or a refusal. Say whether
     either one spends the authorization, consistently with §3.4's "a refused
     configuration costs the caller nothing".

5. **No criterion keeps the transition inside the caller's organisation.**
   - **The gap.** AC-15 tests only a *non-permitted member of the same
     organisation*. §3 and §5 say the transition is governed by PAC-50's rule,
     including "for which domains", but no AC asserts it.
   - **Why it matters.** The transition is new code that PAC-50's tests do not
     cover, and on `main` the domain check is still a suffix match
     (`domain/domain.go:45-49`). A permitted caller of one organisation could
     suspend another organisation's SSO, or a subdomain's. That is exactly the
     kind of cross-tenant write this codebase has shipped before.
   - **Resolves it.** Add an AC: given a permitted caller of organisation A and
     an active configuration whose domain is not A's own (one case for another
     organisation's domain, one for a subdomain of A's), when they suspend it,
     the request is refused and that configuration is still active. Add the
     mirror case for resume.

### Non-blocking

1. **§2.8 understates what PAC-50 resolves.**
   - **The fact.** PAC-50's branch removes the U-21 marker as well as U-22.
     `SsoProviderCreateRequest` there says "Determined at stage 2 from the
     handler (resolves U-21)" (`contracts/openapi/paths/auth.yaml:1385` on
     `origin/impl/pac-50-sso-write-gate-backend`). The operation no longer
     carries any `x-uncertain`.
   - **The consequence.** Once PAC-50 lands (D-5), "U-21 … is resolved here" has
     no marker left to resolve. What remains is API-060, plus rewriting that
     description ("Nothing else is validated…"), which AC-7 and AC-8 will make
     false.
   - **Fix.** Say this in the inherited-scope list.
2. **AC-16 versus §6 "no migration".**
   - **The constraint.** PAC-50's migration restricts the record table:
     `sso_events.kind CHECK (kind IN ('sso_stepup', 'sso_provider_written'))`
     (`028_sso_admin_stepup.up.sql` on PAC-50's branch).
   - **The consequence.** A record "naming the transition" fits under the
     existing kind only if the transition is named in `details`. A distinct kind
     needs a migration, and §6 says a migration means scope has grown.
   - **Fix.** State which of the two satisfies AC-16, so the plan is not
     challenged for a migration the spec required.
3. **The bundle contradicts §3.7's rationale.**
   - **The claim.** §3.7 gives "consistent with the rest of the API" as the
     reason for a named container.
   - **The bundle.** 20 other 2xx JSON responses in
     `contracts/openapi/openapi.yaml` are bare arrays, e.g. `GET /api/teams`,
     `/api/rooms`, `/api/habits`, `/api/org/members` and `/api/audit`.
   - **Fix.** The container may still be right for the second reason given
     (future additions). Restate the rationale so the contract stage does not
     "fix" it back towards consistency.
4. **AC-9's detection assertion should be negative only.** A suspended
   configuration makes detection fall through to the Microsoft tenant heuristic,
   an outbound call with a 2 s timeout (`auth/sso_detect.go:49-55`), and then to
   `google`. "No longer routes to SSO" is the right assertion. A test that
   asserts a specific fallback type would depend on network reachability from the
   e2e stack.
5. **The Linear issue body is stale.** It still lists the admin check, the
   RowsAffected fix and "Spec … (v3)". The spec's scope is correct. The issue
   text should point at v3.1 and PAC-50, so that stage 6 does not review against
   the old text.
6. **OWNER:** D-2 makes the client secret mandatory. An OIDC client registered as
   a public client (PKCE, no secret) can therefore never be configured. The
   choice is conservative and matches what sign-in uses today
   (`handlers_sso.go:64-68`), but it is a product limit the owner should
   confirm.
7. **OWNER:** OQ-5 / D-3. Fixing a suspended configuration and resuming it takes
   two writes, and under PAC-50 two step-up authorizations.
8. **OWNER:** D-6 puts emergency suspension behind PAC-50's step-up.
   - **Who can use it.** That step-up is offered only through Google or Microsoft
     (`sso_stepup_grants.provider CHECK (provider IN ('google','microsoft'))` on
     PAC-50's branch), or through the deployment's break-glass list.
   - **The consequence.** An organisation whose SSO administrators are not
     Google or Microsoft workspace admins can suspend SSO only through the
     operator.
   - **The question.** That is acceptable if intended. The owner should know
     that the "kill switch" may need the operator.

### Criteria I could not falsify

- **AC-4 (second half).** Once AC-7 holds, no API write can produce the state it
  starts from (blocking 1).
- **AC-2 (first sentence).** It cannot be arranged if SAML values are refused on
  an OIDC write (blocking 2).
- **AC-12.** Whether a domain-addressed transition passes or fails depends on how
  "requires" is read (blocking 3).

For every other AC I could describe a test that fails on `main` today. AC-14 is
the exception: it is an existing guard (`handlers_sso_test.go:172`).

### Citations I checked and found wrong

- **None in §2.1 to §2.7.** Checked on `6d751f9`:
  - `sso_providers.go:12-26,20,23,28-31,33-45,51,63-73,66,82-92`
  - `handlers_sso.go:51,56-59,64-72,112,121-125,167-172,189-196,204,209-220,213,228,233-238,250,257-262,269`
  - `routes.go:172-175`, `008_sso_providers.up.sql:6-12`
  - `sentry.go:64-78`, `middleware.go:91`, `server_errors.go:41-57`, and the
    seven `WriteAuditLog` sites
  - `handlers_sso_test.go:30,93,172`, `pac35_auth_flows_test.go:286,396,803`,
    `sso_providers_test.go:59`
  - `sso_detect.go:37`, and `domain.go` (`DomainMatchesOrg` suffix match)
  - `MIGRATION.md:55-57`, `api-audit.md:268,269,350,526-530`
  - `auth.yaml:700,1080,1122,1126,1132,1141`
  - web `AuthDialog.tsx:105-108`
- **§2.8** is incomplete rather than wrong: see non-blocking 1 (U-21).
- **§3.7** "consistent with the rest of the API": see non-blocking 3.

### Consumers this breaks

- **smart-calendar-flow generated types** (`src/api/generated/types.ts`,
  `schemas.ts` and `schemas.types.d.ts`). They regenerate when the schema
  changes. They are generated files, and nothing hand-written calls them:
  `git grep` on web `origin/main` (`ad52160`) finds no reference outside
  `src/api/generated/`.
- **clockwise-like/backend/api/pac50_sso_gate_test.go:149-152** (PAC-50 branch,
  `TestSSOWrite_OneGrantOneWrite`). It pins the 201 body to the thirteen
  PascalCase keys, including `OIDCClientSecret` and `SAMLCert`. It must change
  with this spec, as §2.8 already says.
- **No other consumer.** There is no hand-written consumer in MCP, e2e or the
  web app: `mcp/` and `e2e/` contain no SSO reference.
