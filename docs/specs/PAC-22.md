# Spec — PAC-22: SSO configuration must not disclose identity-provider secrets

- **Linear**: https://linear.app/paceday/issue/PAC-22/add-an-ssoproviderdto-that-omits-oidcclientsecret-and-samlcert-and
- **Status**: draft (v3)
- **Author**: spec-author
- **Inherited scope**:
  - **Resolved here**: API-001 (disclosure half — see below), API-060, API-066,
    API-067, API-092 (the three `/api/admin/sso` operations), `x-uncertain` U-21,
    `x-uncertain` U-22.
  - **Moved to PAC-50**: API-001 (authorization half), and the exact-domain rule
    PAC-46 reports.
  - **Inherited, not resolved, position stated**: API-010 (§5), API-091 (§5),
    API-011 (§2.7 — it is on the operation AC-14 asserts and shapes how AC-14 is
    written).
- **Depends on**: nothing. In particular this spec does not depend on PAC-45 —
  see "Scope" below.

## Scope: this spec is the projection half only

Two different changes were previously written as one document. They are now two:

| Spec | Covers | Blocked on |
|---|---|---|
| **PAC-22** (this one) | Identity-provider secrets stop crossing the API boundary; the response shape becomes a described one; a configuration that cannot work is refused when it is offered; SSO can be suspended and resumed; removing nothing is reported as removing nothing | nothing |
| **PAC-50** | Who may write an SSO configuration and for which domain: operator-designated principals, the exact-domain rule PAC-46 reports, and making configuration changes observable | its own open questions, stated there |

The line is drawn at *decisions*. Everything in this spec is a projection or a
validation: it changes what leaves the server and what the server refuses to
accept, needs no product decision, needs no schema change, and every criterion
below is writable as a failing test against the code as it stands. Everything in
PAC-50 requires a privilege model that does not exist today.

**This spec's severity claim does not assume PAC-45.** The disclosure is live on
`origin/main` regardless of what happens to PAC-45: the secrets are selected,
scanned and encoded on every read, and no part of that path was touched by
PAC-45's branch. PAC-45 is **not merged** — `origin/main` is at `6f7a856`, the
fix is one commit (`b1a07bb`) on `nihochart/pac-45-oidc-email-domain-binding`
with a pull request open. If PAC-45 never merges, nothing in this spec changes.
What does change is PAC-50's severity, and PAC-50 states both cases.

## 1. Problem

Paceday lets an organisation replace password login with its own identity
provider. To do that, somebody hands Paceday the credentials that let Paceday
impersonate the organisation to that identity provider: an OIDC client secret,
or a SAML signing certificate. These are the keys to the organisation's front
door.

**Today any employee with a working Paceday account can read them.** The
organisation's SSO configuration is served back in full, secrets included, to any
authenticated caller who belongs to the organisation. A curious colleague with
browser devtools reads the client secret. Anyone who phishes a single
low-privilege account — an intern, a contractor, a departed employee whose
account was not disabled — walks away with credentials to impersonate Paceday to
the organisation's identity provider.

**What it costs them.** A secret that any employee can read is not a secret, and
must be treated as compromised the moment this is understood. The remedy is
rotation at the identity provider for every organisation that ever configured
SSO — an action the customer must take, coordinated with a Paceday
reconfiguration, with SSO login broken in between. The organisation's security
team is affected worst: they handed over a secret on the understanding that it
was held, not published, and no audit on their side would reveal that it is
readable by their whole staff.

**Who is affected.** Every organisation that has configured SSO, and every user
in it.

Two smaller problems sit on the same surface and cost the person doing the
configuring rather than the organisation. An SSO configuration that cannot
possibly work is accepted without complaint, and the person who entered it finds
out only when a colleague cannot log in. And once SSO is configured there is no
way to turn it off again short of deleting it — so a misconfiguration that is
locking people out cannot be paused while it is diagnosed.

Who may *write* an SSO configuration is a third problem on the same surface, and
it is serious. It is PAC-50's, and it is a reason to close this leak sooner
rather than to wait: rotating an exposed secret requires writing a new one, so
the leak and the write path have to be fixed in this order anyway.

## 2. Current behaviour

Nothing here was executed — the sandbox blocks the Go module proxy, so no build
or test was run. Every claim comes from reading source, and every `file:line` was
read personally for this revision rather than carried over.

**Citation basis.** Go and SQL citations are against **`origin/main`** at
`6f7a856`, which is the codebase as it actually is: PAC-45 is not merged. The
working tree (`factory/spec-driven-pipeline`) contains no `backend/` change
against `origin/main`, so the two are identical for every Go and SQL citation
here. Citations into `contracts/` and `docs/factory/` are against the working
tree, because those directories do not exist on `origin/main` at all. Line
numbers differ from v2 of this spec, which cited the PAC-45 branch; where they
do, the v2 numbers were the branch's and these are `main`'s.

### 2.1 The response contains the secrets

`storage.SSOProvider` (`backend/storage/sso_providers.go:12-26`) declares
thirteen fields and **no struct tags**, among them `OIDCClientSecret`
(`:20`) and `SAMLCert` (`:23`).

The list handler encodes the slice of these structs directly
(`backend/api/handlers_sso.go:276`), and the create handler encodes the stored
struct directly (`:254`). There is no intermediate type in either path. Both
secret columns are selected into the struct on every read (`sso_providers.go:28-31`
column list, `:33-45` scan, `:84-87` the list query).

Because the struct has no tags, `encoding/json` emits the Go field names, so the
response keys are PascalCase. *This is an inference from reading the struct
definition, not an observed response* — the same inference the contract records
as unverified in U-22 (`contracts/openapi/paths/auth.yaml:885-888`). The contract
documents the full PascalCase shape including `OIDCClientSecret`
(`auth.yaml:829-888`), with a comment that it is documented "because it is what
the code does, not because it is intended" (`:866-871`). The list response's top
level is a bare JSON array (`handlers_sso.go:276`, `auth.yaml:535-537`).

The column type is plain `TEXT` with no encryption at rest
(`backend/storage/migrations/008_sso_providers.up.sql:9,12`).

**The API boundary is the only channel that leaks these values.** The others were
checked rather than assumed: Sentry never receives a request body or cookies —
`scrubRequest` sets `req.Cookies = ""` and `req.Data = ""` unconditionally
(`backend/sentry.go:63-77`); the request logger emits method, path and duration
only (`backend/api/middleware.go:86-91`); none of the seven `WriteAuditLog` call
sites is on an SSO path (`engine/focus_time_cleaner.go:39`,
`engine/focus_time.go:106`, `engine/smart_schedule.go:323`,
`engine/compression.go:178`, `api/handlers_nlp.go:67`,
`api/handlers_schedule.go:188`, `nlp/parser.go:221`); and the raw error echoed
into a 500 on OIDC setup is built from the issuer, not the secret
(`handlers_sso.go:113`, `:164`).

### 2.2 There is no update operation; the single write replaces

There are exactly three admin routes: `POST /api/admin/sso`, `GET
/api/admin/sso`, `DELETE /api/admin/sso/{domain}` (`backend/api/routes.go:171-175`).
**There is no update operation.** The single write is an upsert keyed on domain
that overwrites every column from `EXCLUDED` — provider name, type, enabled,
issuer, client id, client secret and all three SAML values
(`sso_providers.go:63-73`). It is a replace, not a merge, and
`backend/storage/sso_providers_test.go:59` (`TestUpsertSSOProvider_Update`)
asserts that today.

### 2.3 An unusable configuration is accepted

Create validates that domain, provider name and provider type are non-empty and
that the type is one of two values (`handlers_sso.go:215-222`). Nothing else. The
OIDC issuer, client id and client secret, and all three SAML fields, are accepted
empty and persisted empty (`handlers_sso.go:240-245`; the columns default to `''`,
`008_sso_providers.up.sql:7-12`).

The consequence is deferred and unhelpful. With an empty issuer, login
construction fails inside `auth.NewOIDCClient` and surfaces as a 500 with the raw
error (`handlers_sso.go:107-115`). A SAML provider is accepted and stored, then
the sign-in route rejects it with 501 "SAML not yet supported" (`:99-102`) — so
SAML is configurable but non-functional by design. This is U-21
(`auth.yaml:824-827`) and API-060.

### 2.4 SSO cannot be disabled through the API

Create hardcodes `Enabled: true` (`handlers_sso.go:239`), and the replace-upsert
writes that value over the existing one on conflict (`sso_providers.go:66`). No
route sets it false. The column exists and works as a kill switch — the sign-in
lookup filters on `enabled = true` (`sso_providers.go:51`) and the listing query
does not (`:82-92`) — so the capability is present in the schema and unreachable
through the API. This is API-066.

### 2.5 Deleting a domain that has no configuration reports success

`DeleteSSOProvider` discards the result and returns only the error
(`sso_providers.go:108-112`), so the handler cannot distinguish "deleted one row"
from "matched nothing" and returns 204 either way (`handlers_sso.go:300-304`).
This is API-067.

PAC-46 observes correctly that this sits in the same three lines as the
domain-authorization check it wants changed (`handlers_sso.go:295-298`). The
reporting defect needs no decision and is falsifiable today, so it stays here;
the authorization rule in the adjacent lines is PAC-50's. §5 records the ordering
that follows.

### 2.6 Who may call these operations today

All three admin handlers perform the identical first check: load the caller,
require that their organisation reference is non-nil, otherwise 403 "org
membership required" — create at `handlers_sso.go:193-198`, list at `:259-264`,
delete at `:283-288`. That same condition is also the database-error path
(`err != nil || user == nil || user.OrgID == nil` are one branch), so a 403 from
these handlers does not by itself mean "refused for lack of membership". Any
criterion below that asserts a refusal therefore also asserts a stored-state
outcome.

Create and delete additionally require that the target domain match the caller's
organisation domain via `domain.DomainMatchesOrg`
(`handlers_sso.go:230-233`, `:295-298`; `backend/domain/domain.go:31-35`), which
is a **suffix** match. That is PAC-50's subject and is not restated here.

### 2.7 Consumers and tests

- **Frontend**: no match for the admin SSO route, `ssoProvider`,
  `OIDCClientSecret` or `SAMLCert` anywhere in `/home/claude/smart-calendar-flow/src`.
  But `smart-calendar-flow/src/components/auth/AuthDialog.tsx:30,34,99,111-112`
  **is** a consumer of the two sign-in operations the suspend/resume criteria act
  on. It is already broken by API-010 (`docs/factory/api-audit.md:244`): it gates
  the SSO redirect on `res.domain`, which `auth.DetectResult` never declares
  (`backend/auth/sso_detect.go:18-22`), so the branch is dead and no user can
  reach the SSO flow through the UI at all.
- **MCP server**: no match for `sso` in any case anywhere under
  `/home/claude/clockwise-like/mcp/`.
- **e2e**: no scenario; the only occurrence of `sso` under `e2e/` is a mention in
  `e2e/TESTIDS-REQUIRED.md`.

This is the API-091 category (`api-audit.md:325`), whose own comment is that
API-001 "sat undetected in exactly this category".

**Test coverage.** `backend/storage/sso_providers_test.go` covers the storage
layer (five tests, at `:9`, `:47`, `:59`, `:89`, `:112`). There is **no handler
test file for SSO at all** on `origin/main` — `backend/api/` contains
`handlers_auth_test.go` and nothing else for this surface — so nothing asserts
anything about the admin surface's authorization gate, response body or status
codes, and nothing protects SSO sign-in either. The first tests on this surface
arrive with PAC-45, which is not merged. API-092 (`api-audit.md:326`) names five
paths plus "all `/api/admin/sso`"; this spec closes the three `/api/admin/sso`
operations and leaves the five sign-in paths open.

All three admin operations are `handwritten` in
`contracts/openapi/MIGRATION.md:55-57`.

**One finding on the sign-in operation AC-14 asserts.** API-011
(`api-audit.md:245`) records that `issueJWT` returns without setting a cookie when
the signing secret is empty or token generation fails, while the callback still
redirects to the success page (`backend/api/handlers_auth.go:139-146`,
`:147-155`). A criterion that asserts "the sign-in did not fail" can therefore
pass with no session issued. This spec does not fix API-011; AC-14 is written to
assert a session positively so that it cannot pass for that reason.

## 3. Desired behaviour

The actor throughout is **a caller who is permitted to configure SSO for an
organisation**. Who that is does not change in this spec: it is whoever may
configure it today, and narrowing it is PAC-50's subject. Where behaviour is
available to any organisation member, this section says "any member".

### 3.1 The secrets stop leaving the server

Someone permitted to configure SSO can see that it is configured for their
organisation, which identity provider it points at, and whether it is currently
active. They cannot read back the client secret or the signing certificate, and
neither can anybody else, on any response from any operation. The secrets are
write-only: they can be supplied, they can be replaced, and they can never be
retrieved.

A reader can still tell whether a secret has been supplied, because "configured
but no secret entered" and "configured with a secret" are different states that
the person configuring SSO needs to distinguish. Knowing that a secret exists is
not knowing the secret. That indicator must mean exactly that and nothing else —
it must not change when anything other than the secret changes.

Making the secrets write-only has a consequence this spec accepts deliberately:
somebody who forgets a client secret cannot retrieve it and must re-enter it.

### 3.2 Supplying a configuration means supplying all of it

There is one write today and it replaces the stored configuration wholesale
(§2.2). **This spec does not turn it into a merge.** Omitting a value on that
write means the value is absent, not "keep what is stored". So changing any part
of the configuration means supplying the whole configuration, secrets included.
The cost is real and is accepted: renaming a provider means re-entering its
secret. A merge would silently change the stored-data semantics of an operation
that already exists, and would reintroduce the question of what "required" means
on a value that is already stored.

### 3.3 Turning SSO off is a separate act from configuring it

**This spec requires one write operation beyond the three that exist today: a
named transition that changes only whether SSO is active.** It carries no
configuration values and no secrets. This is stated as a requirement, not left to
the contract, because the alternative reading is incoherent: §3.2 refuses a
merge, §3.4 makes every value a kind of provider needs required on every write of
the configuration, and those two together make it impossible to suspend SSO
through the configuration write without re-supplying the secret — which is the
opposite of an emergency control.

The two writes therefore divide cleanly. The configuration write replaces the
configuration and never suspends by omission: a configuration write that says
nothing about the active state leaves SSO active, which is what happens today, so
nobody switches SSO off by accident and nobody has to think about the active
state while entering credentials. The state transition changes the active state
and nothing else, so suspending and resuming never require a secret.

One consequence is deliberate and is stated so nobody discovers it: replacing the
configuration of a suspended organisation resumes it. Anyone who wants it to stay
suspended suspends it again, which costs nothing because the transition needs no
secret.

### 3.4 A configuration that cannot work is refused at the point of entry

Someone setting up SSO who omits something the chosen kind of identity provider
requires is told immediately, in terms naming what is missing, instead of finding
out when a colleague cannot log in. Because the configuration write replaces
(§3.2), "required" means required on every configuration write, with no exception
for values already stored.

The same applies to the kind of identity provider the product cannot actually
complete a login with: if it cannot work, it is refused when offered rather than
stored and rejected at sign-in.

### 3.5 SSO can be turned off without being thrown away

Someone investigating a misconfiguration that is locking colleagues out can
suspend SSO, leaving the configuration intact to fix, and turn it back on.

**Suspension stops new sign-ins; it does not end sessions.** Sessions already
established through the provider survive suspension, deletion and reconfiguration
alike, because the session is a self-contained bearer credential with a week's
life and no server-side state. Anyone reaching for suspend after a compromise
must be told this, and changing it is out of scope (§5).

### 3.6 Removing a configuration reports honestly

Removing something that is there and removing something that was never there are
distinguishable outcomes, so somebody who mistypes a domain learns that they did,
instead of being told the removal succeeded.

### 3.7 The response shape is stable and conventional

The shape stops being an accident of how the server happens to store the data and
becomes a described, deliberate shape, consistent with the rest of the API. In
particular the retrieval response is a named container rather than a bare
sequence, so a later addition does not change the response's type for every
consumer at once.

## 4. Acceptance criteria

None of these tests exist — nothing exercises the admin surface at all (§2.7).
Which of them fail today is stated per criterion rather than claimed for all of
them: AC-1 to AC-8, AC-9, AC-10, AC-12 and AC-13 fail against current behaviour;
AC-11's first half already holds and its second half cannot yet be arranged; and
AC-14 already holds but is protected by nothing, so it is a new guard rather than a
failing test. Where a criterion asserts a refusal it also asserts a stored-state
outcome, because the 403 on this surface is shared with the database-error path
(§2.6) and a status code alone can pass for the wrong reason.

**Secret non-disclosure**

AC-1. Given an SSO configuration exists for an organisation with a non-empty
client secret and a non-empty signing certificate, when a member of that
organisation retrieves the organisation's SSO configuration, then no value
anywhere in the response equals either stored value, and no property of the
response carries either one under any name.  `[contract]`

AC-2. Given a request that stores an SSO configuration and supplies a client
secret, when the request succeeds, then the response describes the stored
configuration and contains no value equal to the supplied secret.  `[contract]`

AC-3. Given a stored SSO configuration whose client secret is non-empty, when it
is retrieved, then a named property of the response whose documented meaning is
"a client secret is on file" is true; and given a stored configuration whose
client secret is empty, when it is retrieved, then that same property is false.
The assertion is about the value of that one property on each configuration, not
about how two configurations differ from each other.  `[contract]`

AC-4. Given a stored configuration with a non-empty client secret, when it is
replaced by a configuration write that changes only its display name and supplies
the same secret, then the property of AC-3 is still true and no other described
property changes except the display name and any timestamp; and when it is
replaced by a write that supplies an empty secret, then that property becomes
false. The indicator tracks the secret and nothing else.  `[contract]`

AC-5. Given a configuration stored before this change with an empty client secret
and an empty certificate, when it is retrieved, then the response is valid against
the described shape and reports that no secret is on file, rather than failing to
serialise or omitting the property.  `[contract]`

**Response shape**

AC-6. Given any number of SSO configurations for an organisation, when they are
retrieved, then the top level of the response is a named container and not a bare
sequence, and the response validates against the described shape when the
organisation has none, one, and more than one.  `[contract]`

**Validation at entry**

AC-7. Given no SSO configuration for a domain, when a permitted caller submits one
naming a kind of identity provider but omitting a value that kind requires to
complete a sign-in, then the request is rejected with a client error naming the
missing value, and afterwards no configuration exists for that domain.
`[contract]`

AC-8. Given the current state, when a permitted caller submits a configuration for
the kind of identity provider the product cannot complete a sign-in with, then the
request is rejected at submission time with a client error saying so, and nothing
is stored.  `[contract]`
*(This changes existing behaviour — see OQ-1, which must settle before the
contract is written. If OQ-1 settles the other way, AC-8 is struck and the
contract documents the deferred rejection instead.)*

**Active state**

AC-9. Given an active SSO configuration, when a permitted caller suspends it, then
the sign-in routing step for that organisation's domain no longer routes users to
SSO, the SSO sign-in route reports no configuration for that domain, and the
configuration is still retrievable and reports itself suspended; and when it is
resumed, both resume.  `[e2e]`

AC-10. Given an active SSO configuration with a client secret, when it is
suspended and then resumed with no secret supplied at either step, then a sign-in
that succeeded before the suspension succeeds again afterwards, and every
described value of the configuration other than its active state is unchanged.
`[e2e]`

AC-11. Given an existing SSO configuration, when a configuration write replaces it
and says nothing about whether it is active, then afterwards it is active —
omission never suspends SSO. This holds whether the configuration was active or
suspended beforehand (§3.3).  `[contract]`
*(The active-beforehand half is a regression guard: it already holds, because the
active state is hardcoded true on the write today (§2.4). The suspended-beforehand
half is new and cannot even be arranged today, because nothing can suspend.)*

AC-12. Given the described interface, when the operation that changes only the
active state is examined, then no request it accepts carries a client secret, a
signing certificate, or any other value that belongs to the configuration itself,
and no value it requires is one that the configuration write also requires.
`[contract]`
*(The structural guarantee behind AC-10: it fails today because no such operation
exists, and it would fail again if suspension were later folded back into the
configuration write.)*

**Removal**

AC-13. Given a domain the caller is permitted to remove a configuration for, and
no configuration stored for it, when the caller removes that domain's
configuration, then the response reports that there was nothing to remove,
distinguishably from the response given when there was.  `[contract]`

**Preserved sign-in behaviour**

AC-14. Given an organisation whose SSO configuration points at a working identity
provider and is active, when a user whose email domain is that configuration's
domain signs in through SSO, then the sign-in completes and a session is issued.
`[e2e]`
*(New guard, not a regression guard: nothing protects SSO sign-in today (§2.7). It
must assert that a session was issued positively rather than that the sign-in did
not error, because a sign-in can complete without issuing a session when the
deployment's token-signing secret is unset — API-011, §2.7.)*

**Note on how AC-9, AC-10 and AC-14 are proven.** There is no usable user
interface for any of this: the admin surface has no consumer at all, and the
sign-in surface's only consumer is broken by API-010, which this spec does not fix
(§5). These must be API-level scenarios against the running stack. Stage 5 should
not read the absence of a UI journey as permission to skip them. They also require
the verification stack to be able to act as an identity provider, which today it
cannot — nothing it starts is one. Providing one is part of satisfying these
criteria, not a prerequisite someone else supplies.

## 5. Explicitly out of scope

- **Who may write an SSO configuration, and for which domains.** PAC-50. Both the
  operator-designated principals and the exact-domain rule. This spec changes
  neither, so the write path is exactly as permissive after it as before.
- **Making changes to SSO configuration observable.** PAC-50. It needs a
  destination that is not readable by another organisation's members, which today
  no destination is.
- **Restricting who may *read* the configuration.** With the secrets removed, what
  remains is the fact that the organisation uses SSO and the name of its provider,
  which any member learns by signing in. Restricting it further needs the privilege
  model PAC-50 defers.
- **Encrypting the secrets at rest.** They are plain text in the database (§2.1).
  This spec stops them crossing the API boundary, which is the reported finding;
  encryption at rest is a separate, larger piece of work with its own
  key-management questions.
- **Ending sessions when SSO is suspended, removed or reconfigured.** Sessions are
  self-contained week-long bearer credentials with no server-side state, so nothing
  this spec does can revoke one. §3.5 requires that this be said plainly rather
  than fixed.
- **Rotating secrets already exposed.** Every secret configured before this ships
  must be assumed compromised and rotated at the identity provider by the
  organisation that owns it. That is an operational and customer-communication
  task, not a code change, and it does not become unnecessary because the leak is
  closed. OQ-4.
- **Making SAML work.** If OQ-1 settles on refusing it, this spec refuses it;
  implementing it is a feature.
- **Fixing the broken sign-in routing in the frontend (API-010).** A frontend
  defect on two operations this spec only reads, already tracked in the audit's D5
  batch, and the cross-repo ordering rule puts frontend work after the contract
  lands anyway. The consequence is recorded in §4: every criterion touching sign-in
  must be proven at the API level, because no user can reach the SSO flow through
  the UI until API-010 is fixed. It also means §1's "a misconfiguration locking
  people out cannot be paused" describes a journey that, today, nobody can enter
  through the UI — the problem is real at the API, not yet at the UI.
- **Deciding whether this surface should exist at all (API-091 / audit batch E3).**
  The audit's position is that surfaces with no consumer and no test should be
  either built for or deleted. If the answer is "deleted", most of this spec is
  moot — but the leak is live now and cannot wait for that decision.
- **Building a UI for SSO configuration.** A frontend feature with its own spec.

Ordering against PAC-50: this spec and PAC-50 both change the removal path, three
lines apart. This spec changes what that path *reports*; PAC-50 changes who it
*permits*. Whichever lands second rebases onto the first. Neither depends on the
other's outcome, and this one is the one with no open decisions, so it should go
first.

## 6. What must not change

- **SSO sign-in must keep working for configurations stored before this change.**
  **No test protects this.** There is no handler test file for SSO on
  `origin/main` at all (§2.7). AC-14 adds the first guard, and it is a new guard,
  not a regression guard.
- **No stored data changes, and there is no migration.** The secrets stay in the
  database; only what crosses the boundary changes. The suspend/resume work needs
  no migration either — the column it acts on already exists and already works as a
  kill switch (`008_sso_providers.up.sql:6`, read at
  `backend/storage/sso_providers.go:51`). If a migration appears anywhere in this
  work, scope has grown and that should be challenged rather than accepted.
- **The configuration write stays a replace, not a merge** (§3.2). Protected by
  `backend/storage/sso_providers_test.go:59` (`TestUpsertSSOProvider_Update`),
  which asserts replace semantics today. The new state transition is not a partial
  write of the configuration; it writes one thing and that thing is not part of the
  configuration a caller supplies.
- **Sign-in routing reads the same rows** (`backend/auth/sso_detect.go:37`) and
  must be unaffected except where AC-9 deliberately changes it. No test protects
  it: `backend/auth/` has no test for `DetectAuthProvider`.
- **The secrets must not become recoverable by another route.** Making them
  write-only means somebody who forgets a client secret must re-enter it (§3.1).
  That is the intended trade and must not be softened later with a "reveal"
  affordance.
- **Anything reading the current PascalCase keys breaks.** No such reader was
  found: none in the frontend, none in the MCP server, none in e2e (§2.7), which is
  what U-22 predicted. Absence of a consumer in these two repos is not absence of a
  consumer — a script or saved request outside the tree would break silently. OQ-2.
- **The 403 on this surface is also its database-error path** (§2.6). Any new
  criterion asserting a refusal asserts a stored-state outcome too.
- **The write path's permissiveness is unchanged by this spec, and that is not an
  endorsement of it.** PAC-50 exists because it is wrong. Nothing here should be
  read at stage 6 as this spec having reviewed and accepted it.

## 7. Rollback

**There is no migration and nothing to roll back in the database.** Nothing here
changes stored data, and the secret columns keep their values throughout.

Reverting restores the leak, in full, for every organisation — so a revert is not
a neutral act and should be treated as reopening a critical finding rather than as
a rollback. If a defect is found after release, prefer fixing forward.

Two things do not roll back cleanly:

1. **Secrets entered after release are unrecoverable through the API.** They are
   in the database, so nothing is lost; but anyone who relied on reading them back
   must go to the database. By design (§3.1).
2. **A configuration suspended after release is a state the pre-release code
   cannot produce but can read.** The sign-in lookup already filters on the active
   state, so a reverted deployment would serve a suspended configuration as
   unconfigured and offer no way to resume it. A revert should be preceded by
   resuming anything suspended.

The behaviour changes a consumer could notice — the removed secret properties, the
key casing, the response envelope, the removal-of-nothing outcome, the refusal of
configurations previously accepted — all revert with the code, because no state
depends on them.

## 8. Open questions

**OQ-1 — Should the unimplementable kind of identity provider be refused at
submission instead of at sign-in?** *Owner: human (product), before the contract
is written.* Today it is accepted and stored, then rejected at sign-in (§2.3).
Refusing it at submission is the honest behaviour and is what AC-8 assumes, but it
removes a capability someone may be using to stage a configuration ahead of the
feature existing, and any already-stored row of that kind becomes unreplaceable.
Resolve by deciding; if the decision is to keep accepting it, AC-8 is struck and
the contract documents the deferred rejection.

**OQ-2 — Is there a consumer of this surface outside the two repositories?**
*Owner: whoever operates the existing deployments.* The response shape change
breaks any reader of the current PascalCase keys. There is none in the frontend,
the MCP server or e2e (§2.7), but operator scripts, saved requests and anything
that has ever called this by hand are not visible from here, and no deployment
logs were available. The risk is low but not zero.

**OQ-3 — Will the audit register accept API-001 being split into a disclosure row
and an authorization row?** *Owner: whoever maintains `docs/factory/api-audit.md`,
before the contract stage, since the contract gate enforces touch-it-fix-it.* As
one row, API-001 cannot be closed by this spec, which fixes only the disclosure.
API-092 needs the same treatment in a milder form: it names five paths plus all of
`/api/admin/sso` (`api-audit.md:326`), and this spec closes only the
`/api/admin/sso` share, so either the row is split or it is annotated as partially
closed. The audit's B1 batch (`api-audit.md:502-506`) also bundles the DTO and the
admin gate into one PR; it should be re-cut along the PAC-22/PAC-50 line.

**OQ-4 — Who tells existing customers to rotate their identity-provider secrets?**
*Owner: human.* Out of scope for the code (§5) and cannot be dropped: closing the
leak does not un-expose a secret that has been readable by an organisation's whole
staff. Needs a named owner and a sequence. Note the ordering constraint: rotation
requires writing a new secret, so it has to happen while the write path is still
open to the people who need it — which is a reason to sequence rotation against
PAC-50's designation work rather than against this spec.

---

# History

The two `spec-challenger` rejections below are kept verbatim for the record. They
review **v1** and **v2** of this document, not the spec above. Their section
numbers, AC numbers and `file:line` citations refer to those versions and to the
codebase as each was written against; do not read them against v3. The v2
challenge's central recommendation — split the spec at the authorization boundary
— is what produced v3 and PAC-50.

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
