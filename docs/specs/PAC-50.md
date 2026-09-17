# Spec — PAC-50: SSO configuration must not be writable by every employee, nor for domains that are not the caller's

- **Linear**: https://linear.app/paceday/issue/PAC-50/sso-configuration-is-writable-by-any-employee-and-for-any-subdomain
- **Status**: draft (v1)
- **Author**: spec-author
- **Inherited scope**:
  - **Resolved here**: API-001 (authorization half — see "Scope"), and PAC-46's
    subject, which has no register row of its own.
  - **Absorbed**: PAC-46 in full (see §5, "Reconciliation with PAC-46").
  - **Inherited, not resolved, position stated in §5**: API-005, API-010, API-091.
- **Depends on**: PAC-23, for the observability criteria only (§3.4, AC-12). Not
  for anything else.
- **Related**: PAC-22, which holds the projection half of the same surface and
  lands first (§5).

## Scope: this spec is the authorization half only

PAC-22 and this spec were one document until the stage-1 challenger recommended
splitting them. The line is drawn at *decisions*.

| Spec | Covers |
|---|---|
| **PAC-22** | Secrets stop crossing the API boundary; the response shape becomes a described one; a configuration that cannot work is refused when offered; SSO can be suspended and resumed; removing nothing is reported as removing nothing |
| **PAC-50** (this one) | Who may write an SSO configuration, and for which domain; what happens to configurations the new domain rule strands; making configuration changes observable |

PAC-22 needs no decision from anybody and no schema change. This spec needs both.
What it does **not** need is a product decision, which is the reason it is a spec
now rather than a placeholder: the two questions that blocked the previous
document's criteria are answered here, by decision, in §3.3 and §3.4. The
in-product administrator concept — an organisation administering itself, without
the deployment operator — remains deferred to its own issue (§5, OQ-1).

### This spec's precondition on PAC-45

**PAC-45 is not merged.** `origin/main` is at `6f7a856`; the fix is one commit
(`b1a07bb`) on `nihochart/pac-45-oidc-email-domain-binding` with a pull request
open, and `git branch -r --contains b1a07bb` lists only that branch. Every
severity claim below is therefore stated twice, and §2.2 describes the sign-in
path as it is on `main` rather than as it will be.

This is not an open question with an owner at the bottom of the document. It is a
precondition on the *contract* stage: the contract author must know which of the
two worlds they are writing the 403 vocabulary for, because the two differ in what
an unauthorized write can reach, not in whether it should be refused. If PAC-45
has not merged when stage 2 begins, the contract records the deployment-wide
reading; if it has, the bounded one. Nothing in §3 or §4 changes either way —
which is the point. **This spec is correct in both worlds; only its urgency
differs.**

## 1. Problem

An organisation's SSO configuration decides who Paceday believes its people are.
Whoever can replace it can decide who Paceday believes anybody is.

**Today any employee with a working Paceday account can replace it.** Nothing
distinguishes an employee entitled to configure SSO from one who merely has a
corporate email address, because organisation membership is derived from that
address and granted on signup with no invitation and no approval (§2.3). An
employee who repoints their organisation at an identity provider they run can then
sign in as any colleague, with a session that lasts a week and that suspending SSO
does not end. They can equally suspend or delete the configuration, locking
colleagues out of the product entirely.

**And the domain a caller may write is broader than the domain they belong to.**
The write path accepts any domain that ends in the caller's own, while every
lookup in the same subsystem matches the domain exactly (§2.4). On a deployment
where a company and one of its subsidiaries have both signed up, a member of the
parent can create, overwrite and delete the subsidiary's SSO configuration. The
row they plant appears in no organisation's listing — not the victim's, not their
own — and is nonetheless live for sign-in.

**Nothing anywhere records that any of this happened** (§2.5). The only trace of a
substituted identity provider is a modification timestamp on the row that was
substituted. An organisation that suspects it has been impersonated has nothing to
look at.

**Who is affected.** Every organisation whose domain anybody has ever signed up
under — including organisations that have never used SSO, because a configuration
can be created from nothing. For the domain rule specifically: every organisation
whose domain sits beneath another organisation's on the same deployment.

**What it costs them.** A silent, un-loggable impersonation of any colleague, and
the ability for any employee to take away the whole organisation's ability to log
in. The organisation cannot detect it, cannot attribute it afterwards, and has no
way to nominate who among its people should be able to do this, because the
product has no concept of such a person.

**How far it reaches depends on PAC-45.** If PAC-45 merges, the reach is the
attacker's own DNS subtree: any colleague at their own domain, plus any
organisation whose domain sits beneath it. If PAC-45 never merges, the same write
is a deployment-wide authentication bypass — the identity provider may assert any
address on the deployment, so the reach is every account, including accounts in
organisations that have never configured SSO. In both worlds the fix is the same.

## 2. Current behaviour

Nothing here was executed — the sandbox blocks the Go module proxy, so no build or
test was run. Every claim comes from reading source, and every `file:line` was read
personally for this spec.

**Citation basis.** Go and SQL citations are against **`origin/main`** at
`6f7a856`, which is the codebase as it actually is. The working tree
(`factory/spec-driven-pipeline`) contains no `backend/` change against
`origin/main`, so the two are identical for every Go and SQL citation here.
Citations into `contracts/` and `docs/factory/` are against the working tree,
because those directories do not exist on `origin/main` at all.

### 2.1 The only gate is organisation membership, and it doubles as an error path

All three admin handlers perform the identical first check: load the caller,
require that their organisation reference is non-nil, otherwise 403 "org
membership required" — create at `backend/api/handlers_sso.go:193-198`, list at
`:259-264`, delete at `:283-288`. There are exactly three routes and no others:
`POST /api/admin/sso`, `GET /api/admin/sso`, `DELETE /api/admin/sso/{domain}`
(`backend/api/routes.go:171-175`).

That same condition is also the database-error path — `err != nil || user == nil
|| user.OrgID == nil` are one branch — so a 403 from these handlers does not by
itself mean "refused for lack of membership". Create and delete then produce a
second, indistinguishable 403 when the organisation record cannot be loaded
(`handlers_sso.go:226-229`, `:291-294`). The contract fragment describes the
resulting 403 as three quoted strings in one description
(`contracts/openapi/paths/auth.yaml:514`, `:581`). Every criterion in §4 that
asserts a refusal therefore also asserts a stored-state outcome.

The write itself is an unconditional replace: the upsert overwrites every column
from `EXCLUDED`, including the issuer, the client id and the client secret
(`backend/storage/sso_providers.go:63-73`).

### 2.2 What a write yields at sign-in

On `origin/main`, `oidcCallback` passes the asserted address straight to
`storage.UpsertUser` with no comparison against the domain the provider row was
configured for (`handlers_sso.go:173-186`). `UpsertUser` is `ON CONFLICT (email) DO
UPDATE ... RETURNING` and does **not** compare `provider`
(`backend/storage/users.go:21-35`), so an account created by Google sign-in is
returned and impersonated by an SSO assertion for the same address. `issueJWT`
then sets a self-contained 7-day cookie with no server-side session state
(`backend/api/handlers_auth.go:139-155`). The callback's state check compares a
cookie the client set against a query parameter the client set
(`handlers_sso.go:137-147`), so it is not an obstacle to an attacker driving their
own callback.

**PAC-45's branch adds the missing binding**: the exchange is refused with 403
unless the asserted email's domain is exactly the provider row's domain, via a
case-insensitive, whitespace-trimmed, exact comparison that subdomains do not
match. If it merges, the reach of a write becomes the provider row's own domain
rather than the whole deployment. Everything else in this section is unchanged by
it — `UpsertUser`, `issueJWT` and the state check are all untouched on that branch,
which `git diff --stat main...origin/nihochart/pac-45-oidc-email-domain-binding`
confirms touches four Go files and nothing else.

### 2.3 Organisation membership is self-service

`AssociateUserWithOrg` (`backend/storage/orgs.go:42-52`) extracts the domain from
the user's email, skips generic consumer domains
(`backend/domain/domain.go:5-14`, `:24-26`), then **creates the organisation if it
does not exist** (`UpsertOrg`, `orgs.go:18-27`) and links the user to it
(`SetUserOrg`, `orgs.go:35-38`).

It has three call sites, and the load-bearing one is ordinary Google sign-up
(`backend/api/handlers_auth.go:76`); the others are the Microsoft callback
(`backend/api/handlers_microsoft_auth.go:74`) and the SSO callback
(`handlers_sso.go:183`). There is no invitation and no approval. The current gate
reduces to **"has a corporate email address"**, and an attacker with one becomes
an organisation member of a deployment that has never configured SSO.

### 2.4 The write path is a suffix match; every lookup is exact

Create and delete require that the target domain match the caller's organisation
domain (`handlers_sso.go:230-233`, `:295-298`) via `domain.DomainMatchesOrg`
(`backend/domain/domain.go:31-35`), which is
`d == orgDomain || strings.HasSuffix(d, "."+orgDomain)`.

Every read in the same subsystem is exact:

- the sign-in lookup — `WHERE domain = $1 AND enabled = true`
  (`backend/storage/sso_providers.go:47-54`)
- the listing — `JOIN organizations o ON o.domain = s.domain` (`:88-90`)
- the upsert key — `ON CONFLICT (domain)`, one row per exact domain (`:63`), the
  column being `NOT NULL UNIQUE`
  (`backend/storage/migrations/008_sso_providers.up.sql:3`)
- sign-in routing — on the user's exact email domain
  (`backend/auth/sso_detect.go:26-47`)
- organisation derivation — on the exact email domain (`orgs.go:42-52`)

Organisations are created per-domain from whatever address signs up, so nothing
stops a company and its subsidiary both existing as organisations on one
deployment. A member of the parent passes the check for the subsidiary's domain
and can create, overwrite **and delete** a *different organisation's* SSO
configuration. **The domain check is not a tenancy boundary.**

Because the listing joins on exact equality, such a row appears in no
organisation's listing — not the writer's, and the victim's only if the victim
organisation exists. It is nonetheless live: the sign-in lookup is a direct
exact-domain read with no organisation join, and routing looks up the email domain
exactly.

One consequence of the suffix form deserves recording so it is not re-derived:
were an account ever to exist whose email domain is a public suffix or a very
short domain, the suffix test would be true for most of the internet and this
would become deployment-wide write access to every provider row. Google OAuth
being the only account-creation path makes that impractical today
(`handlers_auth.go:41-79`); it is a reason to remove the suffix match rather than
to keep reasoning about it.

### 2.5 No SSO configuration change is recorded anywhere

None of the three handlers writes an audit entry. The seven `WriteAuditLog` call
sites are focus, scheduling and NLP only (`engine/focus_time_cleaner.go:39`,
`engine/focus_time.go:106`, `engine/smart_schedule.go:323`,
`engine/compression.go:178`, `api/handlers_nlp.go:67`,
`api/handlers_schedule.go:188`, `nlp/parser.go:221`). Substituting an
organisation's identity provider leaves no trace but an `updated_at` bump
(`sso_providers.go:73`).

The existing audit log is also not a usable destination as it stands: it has no
user or organisation predicate and is readable by any authenticated user — that is
API-005 (`docs/factory/api-audit.md:239`), critical and confirmed, and PAC-23 is
the issue that scopes it.

### 2.6 There is no administrator concept anywhere

Each candidate fails for a different reason:

- **The user record has no role.** `users` is id, email, name, avatar, provider,
  provider_id, timestamps (`migrations/006_auth.up.sql:3-12`), plus a nullable
  `org_id` added later (`007_organizations.up.sql:8`).
- **The organisation record has no owner.** `organizations` is id, name, domain,
  created_at (`007_organizations.up.sql:1-6`). It does not record who created it.
- **The only role column is scoped to a team, not an organisation.**
  `team_members.role` is `'owner' | 'member'` (`013_teams.up.sql:9-15`), read only
  by team and manager handlers (`backend/api/handlers_teams.go:538`,
  `handlers_manager.go:327`, `:575`). A team's `org_id` is nullable
  (`013_teams.up.sql:3`) and whoever creates a team is made its owner
  (`handlers_teams.go:55`), so team ownership is self-granted. Gating on it would
  be an escalation path.
- **`is_manager` is a detection, not a grant.** `user_profiles.is_manager` defaults
  false and is accompanied by `detected_at` (`016_manager.up.sql:1-7`) — it is
  inferred from calendar shape.
- **There is no operator-configured administrator list.** A case-insensitive grep
  for admin-shaped identifiers (`isAdmin`, `is_admin`, `admin_users`,
  `admin_emails`, `ADMIN_*`) across all of `backend/**/*.go` returns **nothing**.

### 2.7 Consumers and tests

No consumer of the admin surface exists: no match for the route, `ssoProvider`,
`OIDCClientSecret` or `SAMLCert` anywhere in
`/home/claude/smart-calendar-flow/src`; no match for `sso` in any case anywhere
under `/home/claude/clockwise-like/mcp/`; the only occurrence under `e2e/` is a
mention in `e2e/TESTIDS-REQUIRED.md`. This is the API-091 category
(`api-audit.md:325`).

`smart-calendar-flow/src/components/auth/AuthDialog.tsx:30,34,99,111-112` is a
consumer of the sign-in operations, and it is already broken by API-010
(`api-audit.md:244`): it gates the SSO redirect on `res.domain`, which
`auth.DetectResult` never declares (`backend/auth/sso_detect.go:18-22`).

**There is no handler test file for SSO on `origin/main` at all** — `backend/api/`
holds `handlers_auth_test.go` and nothing else for this surface. Nothing exercises
`createSSOProvider`, `listSSOProviders` or `deleteSSOProvider`, so nothing asserts
anything about the authorization gate. `backend/storage/sso_providers_test.go`
covers the storage layer (five tests, at `:9`, `:47`, `:59`, `:89`, `:112`), none
of them about authorization. This is API-092 (`api-audit.md:326`). Every criterion
in §4 is a new guard; none is a regression guard.

All three admin operations are `handwritten` in
`contracts/openapi/MIGRATION.md:55-57`.

## 3. Desired behaviour

### 3.1 Only someone the deployment operator has named may change SSO

Creating, changing, suspending, resuming or removing an SSO configuration is
possible only for a principal the deployment operator has designated. Everyone
else is refused, and told that they were refused for that reason rather than for
lack of organisation membership. A deployment whose operator has designated nobody
refuses every such attempt: it fails closed.

**Reading remains available to any organisation member.** Once PAC-22 has removed
the secrets, what is left is the fact that the organisation uses SSO and the name
of its provider, which any member learns by signing in. Restricting the read
further would mean restricting it to whoever an operator happened to name, which
is worse than leaving it open.

**Why an operator designation, and why default-deny is acceptable here.** The
argument is granularity and nothing else. A deployment-level capability that is
simply on or off cannot distinguish somebody from anybody: turning it on to let
one person configure SSO turns it on for every corporate-email holder at the same
moment. A designation distinguishes them at the same cost to the operator, and
that is the whole case for it.

It is worth being explicit that this does *not* come for free, because an earlier
version of this argument claimed it did. Default-deny means a deployment that
upgrades without naming anybody cannot configure SSO until it does — the same
day-one availability cost that makes an in-product administrator concept hard to
introduce (§5). It is accepted here for two reasons. The remedy is deployment
configuration rather than a release, so it is measured in minutes by the person
who is already performing the upgrade. And the cost is paid once and buys a
permanent distinction, where an on/off capability pays it every time it is turned
on and buys nothing while it is on.

**The designated set is not part of the product.** No operation the product
exposes may read it, add to it, remove from it or replace it. Changing it is an
act at the deployment level. This is not a detail: the gate this requirement
exists to close is "has a corporate email address", and a designated set the
product could edit would hand that gate a way to designate itself.

**The designation is per deployment, not per organisation.** A principal the
operator names is named for the deployment, and on a deployment hosting several
organisations that means one flat set rather than a mapping from principal to
organisation. This spec decides that rather than deferring it, because §3.2 makes
it safe: a designation says *who* may write, never *what* they may write, and each
designated principal remains confined to their own organisation's domain. A
mapping would be a recorded privilege model — the thing §5 defers — and it would
buy nothing that §3.2 does not already provide.

### 3.2 Only the caller's own organisation domain, exactly

A caller may configure or remove SSO only for their organisation's domain,
exactly. Not a subdomain of it.

This aligns the write path with what the listing and the sign-in path already do.
A member of one organisation can no longer create, overwrite or remove the SSO
configuration of another organisation whose domain happens to sit beneath theirs,
and a configuration can no longer be installed that its own organisation's listing
cannot see. Configuring SSO for a subdomain becomes what it already is for every
other operation on that subdomain: something a member of the subdomain's
organisation does.

Comparison of domains is case-insensitive and ignores surrounding whitespace, and
what is stored is the normalised form, so that two spellings of one domain cannot
become two configurations.

### 3.3 A configuration for a domain nobody owns must not keep minting sessions

Narrowing the rule in §3.2 has a consequence that must be handled rather than
noted. A configuration stored for a domain that is no organisation's exact domain
becomes, the moment the rule takes effect: unwritable, because every write is
checked against the caller's own organisation domain; unremovable, for the same
reason; invisible, because the listing joins on exact equality; and **still live**,
because sign-in routing and the sign-in lookup both key on the domain alone. It
would keep receiving that domain's users and minting sessions for them, with
nobody able to reach it through the product at all.

Such a configuration is not a hypothetical leftover. §2.4 describes exactly how one
is created, and describes it as invisible to every listing at the moment it is
created. It is a stealth persistence artifact, and this change would make an
already-planted one permanently unremovable.

So: **when the exact-domain rule takes effect, every stored configuration whose
domain is no organisation's exact domain stops being usable for sign-in, and the
stored record survives.** It stops being usable because it is unreachable and
therefore unauditable by the people it affects. The record survives because
deleting it destroys the only evidence that it was ever there, and because some of
these rows will be legitimate — a subdomain configured before anyone signed up
under it — and their owners need them back rather than re-entered from memory.

Restoring one is an operator act, outside the product, and it needs the
organisation to exist first. Naming the procedure and taking the inventory before
release are release requirements, not open questions (§7).

### 3.4 Changing SSO configuration leaves a trace

Creating, changing, suspending, resuming or removing an SSO configuration is
recorded, attributably, so that a substitution can be found after the fact. The
record identifies who made the change and which organisation's domain it
concerned. It never carries a secret value.

**The record is not readable by members of another organisation.** This is stated
as a requirement rather than left to whoever implements it, because the obvious
destination today fails it: the existing record of user actions has no user or
organisation predicate and is readable by any authenticated caller (§2.5). Writing
SSO-change records there unmodified would make one organisation's configuration
history readable by another's staff — a new disclosure on a surface that already
carries a critical finding, introduced by the work meant to close one. PAC-23 is
the issue that scopes that surface; this requirement is why this spec depends on
it (§5).

Recording a change does not stop it. It makes it detectable, which is the
difference between an incident that is investigated and one that is never noticed.

## 4. Acceptance criteria

None of these tests exist: nothing exercises the admin surface at all, and there is
no handler test for this surface (§2.7). AC-1 to AC-12 fail against current
behaviour. AC-13 already holds and would keep holding if nothing were built — it is
a guard against this spec's own gate refusing the wrong callers, not a defect
report, and it is stated as such rather than counted among the failing tests.
Because no test protects any of this, none of these is a regression guard either.
Every criterion asserting a refusal also asserts a stored-state outcome, because
the 403 on this surface is shared with two error paths (§2.1) and a status code
alone can pass for the wrong reason.

**Who may write**

AC-1. Given a deployment where the operator has designated nobody, when an
authenticated organisation member attempts to create, change, suspend, resume or
remove an SSO configuration, then each attempt is refused with a reason
distinguishable from the reason given to a caller with no organisation and from
the reason given when the organisation record cannot be loaded; and afterwards the
stored configuration is unchanged in every described value; and retrieving the
configuration still succeeds for that same caller.  `[contract]`

AC-2. Given a deployment where the operator has designated one organisation member
and not another, when the designated member creates a configuration for their
organisation's domain, then it succeeds; and when the undesignated member submits
the identical request, then it is refused and nothing is stored.  `[contract]`

AC-3. Given a designated principal, when they attempt to write or remove a
configuration for a domain that is not their own organisation's, then the attempt
is refused and the target's stored configuration is unchanged. Designation says
who may write, not what they may write.  `[contract]`

AC-4. Given the interface the product describes, when every operation in it is
examined, then none accepts, returns, or names in any request or response the set
of principals the operator has designated — the product can neither read it nor
change it.  `[contract]`

AC-5. Given a deployment started with one designated principal, when the same
deployment is restarted with a different designated principal, then the first can
no longer write and the second can, with no request having been made to the
product to effect the change.  `[e2e]`

**Which domain may be written**

AC-6. Given organisations for a parent domain and for a subdomain of it, and a
configuration stored for the subdomain, when a designated principal of the parent
organisation attempts to create or replace a configuration for the subdomain, then
the attempt is refused and the subdomain's stored configuration is unchanged — in
particular its issuer and its client credentials are unchanged.  `[contract]`

AC-7. Given the same two organisations, when a designated principal of the parent
organisation attempts to remove the subdomain's configuration, then the attempt is
refused, the configuration still exists, and a user at the subdomain can still
sign in through it.  `[contract]`

AC-8. Given an organisation for a parent domain and no organisation for a
subdomain of it, when a designated principal of the parent organisation attempts
to create a configuration for the subdomain, then the attempt is refused and
afterwards no configuration exists for that subdomain — it is not possible to
install a configuration that no organisation's listing can show.  `[contract]`

AC-9. Given a designated principal of an organisation, when they submit a
configuration for their own domain spelled with different capitalisation or with
surrounding whitespace, then it succeeds and afterwards exactly one configuration
exists for that organisation, not two.  `[contract]`

**Configurations the rule strands**

AC-10. Given a stored SSO configuration whose domain is no organisation's exact
domain, and which is usable for sign-in, when the exact-domain rule takes effect,
then the sign-in routing step no longer routes that domain's users to SSO, the SSO
sign-in route reports no configuration for that domain, and the stored record
still exists with every value it had other than whether it is usable.  `[unit]`

AC-11. Given the state produced by AC-10, when an organisation for that domain is
subsequently created and a principal of it is designated, then that principal can
retrieve, replace and remove the configuration through the product like any other
— the record is recoverable rather than merely retained.  `[e2e]`

**Observability**

AC-12. Given a designated principal who creates, replaces, suspends, resumes or
removes an SSO configuration, when the change has succeeded, then a record of it
exists identifying who made it and which organisation's domain it concerned; no
such record contains any value equal to a supplied client secret or signing
certificate; and a member of a different organisation cannot retrieve it through
any operation the product describes.  `[contract]`
*(The last clause is why this spec depends on PAC-23 — see §5. It is stated as
part of the criterion rather than as a caveat because a record that leaks across
organisations does not satisfy §3.4 and must not be allowed to pass as though it
did.)*

**Preserved behaviour**

AC-13. Given an organisation whose SSO configuration points at a working identity
provider, when a designated principal of that organisation replaces it with the
same values, then a user at that organisation's domain can still sign in and a
session is issued. The gate must refuse the right callers without refusing the
right ones.  `[e2e]`
*(It must assert that a session was issued positively rather than that the sign-in
did not error, because a sign-in can complete without issuing a session when the
deployment's token-signing secret is unset — API-011, §2.7.)*

**Note on how AC-5, AC-11 and AC-13 are proven.** There is no usable user
interface for any of this: the admin surface has no consumer, and the sign-in
surface's only consumer is broken by API-010, which this spec does not fix (§5).
These must be API-level scenarios against the running stack, and AC-13 additionally
requires the verification stack to be able to act as an identity provider, which
today it cannot — nothing it starts is one. PAC-22 carries the same requirement;
whichever lands first provides it.

## 5. Explicitly out of scope

- **Introducing an in-product organisation administrator concept.** The
  designation this spec requires is not one and does not claim to be: it is a
  deployment-level act, and it does not scale past deployments whose operator
  knows every administrator by name. Replacing it with something an organisation
  manages itself needs a product decision — what an administrator is permitted to
  do, where that is recorded, and how the first one of an existing organisation
  comes to be one. OQ-1, and its own issue.
- **Everything PAC-22 holds**: the secrets, the response shape, validation at
  entry, suspend and resume, and honest reporting of a removal that removed
  nothing. In particular API-067 stays in PAC-22 even though it sits three lines
  from the check this spec changes, because it needs no decision and is
  falsifiable today.
- **Scoping the existing record of user actions (API-005).** PAC-23 owns it. This
  spec depends on its outcome for AC-12 and specifies no replacement of its own.
  If PAC-23 has not landed when this reaches stage 2, the contract author states
  which destination satisfies §3.4 rather than assuming the existing one does.
- **Restricting who may read an SSO configuration** (§3.1).
- **Ending sessions when a configuration is changed, suspended or removed.**
  Sessions are self-contained week-long bearer credentials with no server-side
  state (§2.2), so nothing this spec does can revoke one. An attacker who obtained
  a session before this lands keeps it for up to a week afterwards. Revocation is
  its own work and the residual is not closed without it.
- **Comparing how an account was originally created against how it is being
  asserted** (§2.2). A question about identity linking, not about this surface.
- **Self-service organisation membership** (§2.3). A real weakness and the reason
  the current gate is weaker than it appears, but it is the membership model's
  problem and touches every organisation-scoped surface. §3.1 neutralises it for
  *this* surface without fixing it anywhere else.
- **Fixing the broken sign-in routing in the frontend (API-010)**, and **deciding
  whether this surface should exist at all (API-091)**. Positions unchanged from
  PAC-22.

### Reconciliation with PAC-46

**PAC-46 is absorbed into this spec rather than cited as a dependency, and should
be closed as absorbed rather than implemented.**

PAC-46 specifies §3.2 exactly, and its analysis is good — the bound it derives is
correct, and its public-suffix observation is carried into §2.4. The reason to
fold it in rather than keep two issues is that the exact-domain check and the
designation gate are the same lines of the same two handlers and both change which
requests receive a 403 on the same two operations. Kept apart, they would be two
contracts editing one operation's refusal vocabulary and two implementations
rebasing onto each other, to state a rule that neither can state completely: "who
may write" and "what they may write" are one answer at the boundary, and AC-3
exists precisely to pin the seam between them.

PAC-46's own text asserts that PAC-22 "does not fix this one". That was true of
PAC-22 v1, and it is true again of PAC-22 v3, which holds only the projection
work — but it should not be read as meaning the rule is unowned. It is owned here.

## 6. What must not change

- **Legitimate SSO configuration must keep working.** A designated principal
  configuring their own organisation's domain must succeed exactly as any member
  does today. **No test protects this**: there is no handler test for this surface
  at all (§2.7). AC-13 is the guard, and it is a new one.
- **SSO sign-in itself is untouched.** This spec changes who may write a
  configuration, not what happens at sign-in. Sign-in routing reads the same rows
  (`backend/auth/sso_detect.go:37`) and must be unaffected except where AC-10
  deliberately changes it for stranded configurations.
- **The write stays a replace, not a merge.** Protected by
  `backend/storage/sso_providers_test.go:59` (`TestUpsertSSOProvider_Update`).
- **Cross-organisation isolation is not a baseline this work preserves — it is a
  baseline this work establishes.** The current check is a suffix match and
  organisations are per-domain (§2.4). No test protects anything here. AC-6 to AC-8
  are new guards.
- **The reason a caller is refused must be distinguishable.** The 403 on this
  surface is shared with two error paths (§2.1). If the new refusals are not given
  reasons distinguishable from "org membership required" and "org not found", AC-1
  through AC-8 can all pass for the wrong reason, and the contract fragment's
  single lumped 403 description (`auth.yaml:514`, `:581`) must be split
  accordingly.
- **The state check on the sign-in callback is not a control and must not be
  recorded as one.** It compares a cookie the client set against a query parameter
  the client set (`handlers_sso.go:137-147`). Nothing in this spec should be read
  at stage 6 as relying on it.
- **A designated set the product can edit fails this spec even if every other
  criterion passes.** AC-4 is the guard; §3.1 is the reason.

## 7. Rollback

There are two changeable parts and they roll back differently.

**The gate and the domain rule revert cleanly.** Neither changes stored data.
Reverting restores a write path open to every corporate-email holder and to every
domain beneath theirs, so a revert reopens a critical finding rather than
returning to a neutral state; prefer fixing forward. One foreseeable support
incident is not a defect: a deployment that upgrades without naming anybody cannot
configure SSO until it does (§3.1). That must be in the release note, prominently,
with the remedy, because the first sign of it will be a customer unable to
configure SSO.

**The stranding change does not revert cleanly, and this is the part that needs
care.** Making stranded configurations unusable (§3.3, AC-10) is a change to stored
data. It must record which configurations it changed and what their previous state
was, so that reverting restores exactly those and does not resume configurations
that were already suspended for their own reasons. A reverse step that simply
makes everything usable again is wrong and would resume configurations somebody
had deliberately turned off.

Two release requirements follow, and they are requirements rather than open
questions:

1. **The inventory is taken before release, not after.** Each running deployment
   is queried for stored configurations whose domain is no organisation's exact
   domain, before this ships. Each one found is either matched to an organisation
   that should exist, or identified as unexplained. An unexplained one is an
   incident: §2.4 describes how it gets there, and the only way it gets there is
   somebody writing it.
2. **The recovery procedure is written before release.** AC-11 makes recovery
   possible through the product once the organisation exists and a principal is
   designated; the procedure says who does that, and what they check first. Without
   it AC-11 is a capability nobody knows how to use.

**Whatever records SSO changes accumulates rows that a revert leaves behind.**
Harmless, but a reverse step that drops them destroys the only evidence of any
substitution that happened in the meantime, so it should not.

## 8. Open questions

**OQ-1 — What makes someone an administrator of an organisation, as a product
concept, and who is the first one?** *Owner: human (product). Blocks the follow-up
issue, not this spec.* No candidate signal in the codebase is fit for purpose
(§2.6), so the answer is a decision, not a discovery. It has three parts: what an
administrator is permitted to do, where that is recorded, and how the first
administrator of an existing organisation comes to be one. The operator
designation in §3.1 answers the third for the interim, at deployment-configuration
cost, which is why this spec ships without waiting. The remaining question is what
the product needs in addition, and for whom — and my reading is that it is driven
entirely by whether Paceday intends deployments where the operator cannot know
every customer's administrators by name. That is a product question and it is not
this spec's to answer.

**OQ-2 — Will the audit register accept API-001 being split?** *Owner: whoever
maintains `docs/factory/api-audit.md`, before the contract stage, since the
contract gate enforces touch-it-fix-it.* API-001 is one row covering both the
disclosure and the missing gate. PAC-22 closes the disclosure and this spec closes
the authorization; as one row, neither can close it. PAC-46's subject has no row at
all and should get one, or be recorded against API-001's authorization half. The
audit's B1 batch (`api-audit.md:502-506`) bundles the DTO and the admin gate into
one PR and should be re-cut along the PAC-22/PAC-50 line.

**OQ-3 — Is PAC-45 merging, and when?** *Owner: the PAC-45 assignee. Not a blocker
on this spec's criteria; a precondition on its contract.* See "This spec's
precondition on PAC-45". The answer changes what an unauthorized write can reach
and therefore how the contract describes the risk it closes, but it changes no
criterion in §4.

**OQ-4 — Is there an organisation on any running deployment whose domain sits
beneath another's?** *Owner: whoever operates the existing deployments, before
release.* §3.2 is correct regardless, but the answer decides whether AC-6 to AC-8
describe a hypothetical or a live exposure, and therefore how the release is
sequenced against OQ-2's inventory. It cannot be answered from the repository.

---

## Provenance

This spec is the authorization half of what was PAC-22 v2, split on the
recommendation of that document's stage-1 challenge (2026-09-17), which is
preserved in full at the bottom of `docs/specs/PAC-22.md`. Findings 2, 3, 4, 5, 6,
8 and 9 of that challenge are answered here rather than in PAC-22: PAC-45's status
(the precondition section and OQ-3), the designation being reachable through the
API (§3.1, AC-4), the per-deployment/per-organisation question (§3.1, decided), the
self-refuting case for designation (§3.1, restated on granularity alone), the
stranded configurations (§3.3, AC-10, AC-11, §7), the criteria that could not be
written until a human answered a question (§3.1 and §3.4 answer them), and PAC-46
(§5).
