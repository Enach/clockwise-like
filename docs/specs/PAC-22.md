# Spec — PAC-22: SSO configuration must not disclose identity-provider secrets, and must not be writable by every employee

- **Linear**: https://linear.app/paceday/issue/PAC-22/add-an-ssoproviderdto-that-omits-oidcclientsecret-and-samlcert-and
- **Status**: draft (v2 — v1 was rejected; see the Challenge section at the bottom, which applies to v1)
- **Author**: spec-author
- **Inherited scope**:
  - **Resolved by this spec**: API-001 (disclosure half — see §0), API-060,
    API-066, API-067, API-092 (the `/api/admin/sso` share of it — see §2.10),
    `x-uncertain` U-21, `x-uncertain` U-22.
  - **Split out of this spec, must be filed as its own issue**: API-001
    (authorization half — the absence of an in-product administrator concept).
  - **Inherited, explicitly not resolved, position stated**: API-010 (§5),
    API-091 (§5), API-005 (§8, OQ-8).
  - **Depends on**: PAC-45, whose domain-binding fix is assumed landed
    throughout this spec (§2.2). If PAC-45 is reverted, this spec's severity
    argument collapses and §0 must be re-argued.

---

## What changed in v2, and why

v1 argued that deferring authorization was tolerable because an interim
operator kill-switch (v1 AC-10) reduced the write path. The challenger showed
that the write path was not "org takeover" but deployment-wide authentication
bypass, and rejected the spec. That bypass has since been filed as PAC-45 and
**partially fixed**: the OIDC exchange is now refused when the asserted email's
domain is not exactly the provider row's domain (§2.2).

So v2 re-argues from the corrected facts rather than patching v1:

| | v1 | v2 |
|---|---|---|
| Severity of the unguarded write path | deployment-wide session minting (per the challenge) | bounded to one email domain (§2.3) — still silent impersonation of any colleague |
| The split | non-disclosure now, **all** authorization later | non-disclosure **and the decision-free authorization** now, the in-product administrator concept later (§0) |
| Interim mitigation | operator on/off switch (AC-10) | **struck.** Replaced by operator-designated principals (§0.3, AC-9–AC-11) |
| "Login completes exactly as today" (AC-13) | froze the bypass | **struck.** Replaced by AC-19/AC-20, which pin the binding rather than the behaviour |
| AC-4 (round-trip keeps the secret) | presupposed an update operation | **struck** (§3.2). There is one write and it replaces |
| AC-12 (cross-org retrieve refused) | unfalsifiable | split into AC-12 (read: non-appearance) and AC-13/AC-14 (write, remove: refused) |
| Suffix-matched write authorization | deferred as a subdomain-visibility curiosity | in scope as cross-tenant write (AC-13, AC-14) |

### Where each of the challenge's blocking findings is answered

| Blocking finding (v1) | Answered in |
|---|---|
| 1 — deployment-wide bypass; AC-13 freezes it | §2.2 (PAC-45's binding, with its tests), §2.3 (severity restated as bounded), §3.9, AC-21/AC-22 replace AC-13 |
| 2 — three ACs need an update operation; replace-vs-merge; AC-4 ⊥ AC-5 | §2.7 records that no update operation exists and that the write replaces; §3.2 states replace semantics and strikes v1 AC-4; §3.3 defines "required" as required on every write; AC-18 carries what v1 AC-8 meant |
| 3 — AC-12's retrieve clause cannot pass | AC-12 (non-appearance, 200) split from AC-13/AC-14 (refusal) |
| 4 — cross-org isolation is not a baseline | §2.4 states the suffix match as cross-tenant write; §6 stops claiming the baseline and says this work *establishes* it; §3.7, AC-12–AC-15 |
| 5 — AC-10 buys less than claimed; OQ-6 forces it on at the worst time | §0.3 strikes the switch and argues both directions; AC-9–AC-11 require operator-designated principals; OQ-1 and OQ-7 rewritten accordingly |
| 6 — inherited scope understated (API-092, API-010) | Inherited-scope block above; §2.10 re-checks API-092 against the test file PAC-45 added and states exactly which share this spec closes; §5 states a position on API-010 and its consequence for AC-16/AC-21/AC-22 |
| Wrong citation — §2.4's `handlers_sso.go:183` | §2.6 now cites `backend/api/handlers_auth.go:76` as load-bearing, with the other two call sites named |

Non-blocking findings 1–8 are answered at §3.8/AC-20 (no trace of a change),
§3.4 (suspension does not end sessions), §3 preamble (the actor is named),
AC-3/AC-4 (the indicator must *mean* "a secret is on file"), AC-6 (the response
envelope), §2.4 and §4 preamble (the shared 403), the `[contract]`/`[e2e]`
labels, and the corrected AC numbering.

> **A note on this spec's own evidence.** The sandbox blocks the Go module
> proxy, so nothing here was established by compiling or running the backend.
> Every claim in §2 comes from reading source and carries a `file:line` that I
> verified personally.
>
> **Citation basis.** §2 describes the system *with PAC-45 applied*, because
> that is what "today" means by the time this spec is acted on. Line numbers
> are against `origin/nihochart/pac-45-oidc-email-domain-binding`. Two files
> differ from `main`: `backend/api/handlers_sso.go` (everything after line 176
> is shifted +8) and `backend/domain/domain.go` (everything after line 26 is
> shifted +14). Every other citation is identical on both.

---

## 0. Scope: still two problems, but the line between them has moved

v1 proposed splitting "stop the leak" from "gate the surface". The challenger
agreed with the split and asked for the mitigation to be replaced. PAC-45 did
not change the shape of that question — it changed the urgency, and it changed
it in the direction that makes the split *easier* to defend, not harder. But it
also exposed that v1 drew the line in the wrong place.

### 0.1 The split survives, for the reason v1 gave

Not disclosing a secret is a projection: decide what leaves the server. It
changes no stored data, needs no migration, and needs no decision from
anybody — there is no reading of the product in which the identity-provider
client secret should be readable over the API.

Introducing an in-product administrator concept is the introduction of a
privilege model that does not exist (§2.5). It needs a product decision about
who is an administrator, a schema change to record it, and a bootstrap rule,
because on the day it ships every existing deployment has zero administrators
and SSO becomes unconfigurable by anybody. That decision still has no owner
(OQ-1).

### 0.2 But v1 put too much on the far side of the line

v1's split was "non-disclosure now, **all** authorization later". That was wrong
even before PAC-45, and the challenger's blocking findings 4 and 5 said so from
two directions. Two pieces of the authorization problem need no product decision
at all:

1. **Which domain a caller may configure.** The write path authorizes with a
   *suffix* match (§2.4). PAC-45 has just settled, for the sign-in side of the
   same surface, that a provider row's domain is **exact** (§2.2). Making the
   write path exact too is a consistency fix between three places that currently
   disagree — write (suffix), listing (exact), sign-in (exact) — not a policy
   choice. It closes cross-tenant writes and the invisible-subdomain row in one
   move.
2. **Whether every employee may write at all.** An operator-designated set of
   principals costs the same deployment configuration as v1's on/off switch and,
   unlike the switch, distinguishes somebody from anybody. This is the
   challenger's blocking finding 5, accepted in full (§0.3).

So v2's line is: **everything that needs no product decision ships here; only
the in-product administrator concept is deferred.**

### 0.3 The interim kill-switch (v1 AC-10) is struck

Argued honestly, because the case moved:

**The case for keeping it is now weaker than v1's.** Its whole justification was
that the write path was an unbounded takeover primitive worth disabling the
feature for. PAC-45 removed the unbounded part (§2.3). A default-off switch
would now turn a working-if-dangerous capability into no capability at all, for
every deployment, in exchange for mitigating a risk that is confined to one
email domain.

**The case against it was already decisive and is unchanged.** The challenger's
timing objection stands: the rotation sequence (OQ-7) requires every affected
customer to reconfigure, which requires the switch **on** — so the mitigation is
off when there is nothing to protect and on during the one window when the whole
customer base is being reconfigured, with the primitive still live for every
corporate-email holder. A switch that is guaranteed to be open during the
attack window is not a mitigation.

**And it is dominated.** Designating principals is the same class of mechanism
at the same cost, and it survives the rotation window: during rotation the
operator designates themselves or the customer's security contact, rather than
opening writes to every employee.

So: the switch is struck as over-engineering in the wrong direction — it costs
availability and buys strictly less than the alternative. The *requirement*
underneath it is not struck, because the residual it guards is still serious
(§2.3). It is replaced by the challenger's formulation: **in the interim,
changing SSO configuration must be possible only for a principal the deployment
operator has designated.**

Adopting it narrows the follow-up issue rather than absorbing it. The follow-up
still exists, and still needs OQ-1: replacing operator-designated principals
with an administrator concept customers can manage themselves. That is the part
that does not scale past self-hosting, and it is the part that needs the product
decision.

### 0.4 Proposed split

| Spec | Covers | Blocked on |
|---|---|---|
| **PAC-22** (this one) | Secrets never leave the server; response shape stabilised; create-time validation; removal distinguishes missing from deleted; suspend/resume; writes restricted to the caller's exact organisation domain; writes restricted to operator-designated principals; SSO configuration changes become observable | nothing |
| **new issue — "in-product organisation administrator concept"** | Who may administer an org as a product concept; recording it; bootstrapping the first one; replacing PAC-22's operator-designated principals with it; whether reading is restricted too | OQ-1 (product decision) |

This split still requires the bookkeeping change v1 identified: API-001 is one
register row covering both halves, so under touch-it-fix-it PAC-22 cannot close
it. API-001 should be split into a disclosure row (closed here) and an
authorization row (closed by the follow-up). See OQ-6.

---

## 1. Problem

Paceday lets an organisation replace password login with its own identity
provider. To do that, somebody hands Paceday the credentials that let Paceday
impersonate the organisation to that identity provider: an OIDC client secret,
or a SAML signing certificate. These are the keys to the organisation's front
door.

**Today any employee with a working Paceday account can read them.** The
organisation's SSO configuration is served back in full, secrets included, to
any authenticated caller who belongs to the organisation. A curious colleague
with browser devtools reads the client secret. Anyone who phishes a single
low-privilege account — an intern, a contractor, a departed employee whose
account was not disabled — walks away with credentials to impersonate Paceday to
the organisation's identity provider.

**And any employee can silently replace them.** Nothing distinguishes an
employee who is entitled to configure SSO from one who merely has a corporate
email address, because organisation membership is derived from that address and
granted on signup with no invitation and no approval (§2.6). An employee who
repoints their organisation at an identity provider they run can then sign in as
any colleague at that domain, with a session that lasts a week and that
suspending SSO does not end. Nothing anywhere records that they did it (§2.9).

Until PAC-45 landed, that second problem was worse still: the identity provider
could assert *any* address on the deployment, so the primitive reached every
account regardless of domain. PAC-45 bounded it to one domain. It did not remove
it, and it did not touch who may write (§2.3).

**Who is affected.** Every organisation that has configured SSO, and every user
in it, for the disclosure. For the write path, every organisation whose domain
anybody has ever signed up under — including organisations that have never used
SSO, because the configuration can be created from nothing.

**What it costs them.** A secret that any employee can read is not a secret, and
must be treated as compromised the moment this is understood. The remedy is
rotation at the identity provider for every organisation that ever configured
SSO — an action they must take, coordinated with a Paceday reconfiguration, with
SSO login broken in between. The organisation's security team is affected worst:
they handed over a secret on the understanding that it was held, not published,
and no audit on their side would reveal that it is readable by their whole staff,
or that it can be swapped by any of them without trace.

Two smaller problems sit on the same surface and cost the person doing the
configuring rather than the organisation. An SSO configuration that cannot
possibly work is accepted without complaint, and the person who entered it finds
out only when a colleague cannot log in. And once SSO is configured there is no
way to turn it off again short of deleting it — so a misconfiguration that is
locking people out cannot be paused while it is diagnosed.

---

## 2. Current behaviour

All citations are from reading; nothing was executed. Line numbers are against
the PAC-45 branch (see the citation note above).

### 2.1 The response contains the secrets

`storage.SSOProvider` (`backend/storage/sso_providers.go:12-26`) declares
thirteen fields and **no struct tags**, among them `OIDCClientSecret`
(`sso_providers.go:20`) and `SAMLCert` (`sso_providers.go:23`).

The list handler encodes the slice of these structs directly
(`backend/api/handlers_sso.go:284`), and the create handler encodes the stored
struct directly (`handlers_sso.go:262`). There is no intermediate type in either
path. Both secret columns are selected into the struct on every read
(`sso_providers.go:28-31` column list, `:33-45` scan, `:84-87` the list query).

Because the struct has no tags, `encoding/json` emits the Go field names, so the
response keys are PascalCase. *This is an inference from reading the struct
definition, not an observed response* — the same inference the contract records
as unverified in U-22 (`contracts/openapi/paths/auth.yaml:871-874`). The
contract documents the full PascalCase shape including `OIDCClientSecret`, with
a comment that it is documented "because it is what the code does, not because
it is intended" (`auth.yaml:815-870`). The list response's top level is a bare
JSON array (`handlers_sso.go:284`, `auth.yaml:521-526`).

The column type is plain `TEXT` with no encryption at rest
(`backend/storage/migrations/008_sso_providers.up.sql:9,12`).

**The API boundary is the only channel that leaks these values.** I checked the
others rather than assuming: Sentry never receives a request body or cookies —
`scrubRequest` sets `req.Data = ""` and `req.Cookies = ""` unconditionally
(`backend/sentry.go:63-78`); the request logger emits method, path and duration
only (`backend/api/middleware.go:86-91`); none of the seven `WriteAuditLog` call
sites is on an SSO path (`engine/focus_time_cleaner.go:39`,
`engine/focus_time.go:106`, `engine/smart_schedule.go:323`,
`engine/compression.go:178`, `api/handlers_nlp.go:67`,
`api/handlers_schedule.go:188`, `nlp/parser.go:221`); and the raw error echoed
into a 500 on OIDC setup is built from the issuer, not the secret
(`handlers_sso.go:113`, `:164`).

### 2.2 The sign-in path now binds the asserted email to the provider's domain (PAC-45)

`oidcCallback` refuses the exchange with 403 unless the asserted email's domain
is exactly the `{domain}` whose provider row was used
(`handlers_sso.go:177-184`). The comparison is `domain.EmailBelongsToDomain`
(`backend/domain/domain.go:28-40`): case-insensitive, whitespace-trimmed,
**exact** — subdomains do not match — and an address containing more than one
`@` never matches.

This is the fix for PAC-45, and it is what stops the asserted identity from
being an arbitrary account on the deployment. It is covered by tests, which is
new on this surface: `backend/api/handlers_sso_test.go:138` (cross-domain
assertion refused, victim row unmodified, no session cookie),
`:162` (subdomain assertion refused), `:172` (same-domain assertion succeeds and
issues a session), driven through a fake IdP that signs whatever the test asks
for (`handlers_sso_test.go:30-93`); and
`backend/domain/domain_test.go:58` for the comparison itself.

Everything downstream of the check is unchanged. `storage.UpsertUser` is still
`ON CONFLICT (email) DO UPDATE ... RETURNING` and still does **not** compare
`provider` (`backend/storage/users.go:21-35`), so an account created by Google
sign-in is still returned and impersonated by an SSO assertion for the same
address. `issueJWT` still sets a self-contained 7-day cookie with no server-side
session state (`backend/api/handlers_auth.go:139-155`).

### 2.3 What the fix leaves: bounded, silent impersonation of any colleague

The write path is untouched by PAC-45. Putting §2.2 together with §2.4 and §2.6,
the residual primitive is precisely this:

- **Who.** Any authenticated user whose email domain `D` is non-generic — which
  is the same thing as "any member of the organisation for `D`", because
  membership is derived from the address (§2.6).
- **What they can write.** The provider row for `D`, and for any domain of which
  `D` is a suffix-parent (§2.4) — overwriting issuer, client id and client
  secret, because the write is an unconditional replace (`sso_providers.go:63-73`).
- **What that yields.** They point a domain's provider at an identity provider
  they run, sign in through the public route, and assert any address at exactly
  that domain. `EmailBelongsToDomain` permits it, `UpsertUser` returns the
  existing account, and they hold a 7-day session as that colleague.
- **Blast radius.** Every account whose email domain is exactly a domain they may
  write — i.e. their own organisation's domain, plus any organisation whose
  domain is a subdomain of it. Regardless of how those accounts were created
  (§2.2). No victim interaction is required.
- **Also available to them.** Suspending or deleting a domain's SSO, which locks
  colleagues out (§2.7, §2.8) — and neither ends sessions already issued (§2.2).
- **Detectability.** None. There is no record of an SSO configuration change
  anywhere (§2.9), and the substituted row does not appear in the organisation's
  own listing when the domain is a subdomain (§2.4).

So the correct statement of severity — which is what the challenger asked for,
and which PAC-45 has now made true rather than understated — is: **any employee
can silently become any colleague at their own domain, and can take SSO down for
them.** It is no longer a deployment-wide primitive. It is still critical inside
an organisation, and it is the reason §0.3 replaces the kill-switch rather than
dropping the requirement.

### 2.4 Write authorization is a suffix match, so it is not cross-tenant isolation

All three admin handlers perform the identical first check: load the caller,
require that their organisation reference is non-nil, otherwise 403 "org
membership required". Create at `handlers_sso.go:201-206`, list at `:267-272`,
delete at `:291-296`. That same condition is also the database-error path —
`err != nil || user == nil || user.OrgID == nil` are one branch — so a 403 from
these handlers does not by itself mean "refused for lack of membership".

Create and delete additionally require that the target domain match the caller's
own organisation domain (`handlers_sso.go:238-241`, `:303-306`), via
`domain.DomainMatchesOrg` (`backend/domain/domain.go:45-49`), which is
`d == orgDomain || strings.HasSuffix(d, "."+orgDomain)`.

Organisations are created per-domain from whatever address signs up
(`UpsertOrg`, `backend/storage/orgs.go:18-27`, called from
`AssociateUserWithOrg`, `orgs.go:42-52`). Nothing stops `acme.com` and
`eu.acme.com` both existing as organisations. A member of `acme.com` then passes
the check for `eu.acme.com` and can create, overwrite **and delete** a
*different organisation's* SSO configuration. **The domain check is not a
tenancy boundary.**

The listing query joins on **exact** domain equality —
`JOIN organizations o ON o.domain = s.domain` (`sso_providers.go:88-90`) — so
such a row never appears in `acme.com`'s listing, and appears in
`eu.acme.com`'s listing only if that organisation exists. It is nonetheless
live: the sign-in lookup is a direct exact-domain read with no org join
(`sso_providers.go:47-54`) and detection looks up the email domain exactly
(`backend/auth/sso_detect.go:26-47`).

So three places disagree about what a provider's domain means: writes use a
suffix match, listing uses exact equality, and sign-in — since PAC-45 — uses
exact equality twice over.

### 2.5 There is no administrator concept anywhere

I looked for one, on the assumption that the audit might have missed it. It is
genuinely absent, and each candidate fails for a different reason:

- **The user record has no role.** `users` is id, email, name, avatar, provider,
  provider_id, timestamps (`migrations/006_auth.up.sql:3-12`), plus a nullable
  `org_id` added later (`007_organizations.up.sql:8`). No role, no flag.
- **The organisation record has no owner.** `organizations` is id, name, domain,
  created_at (`007_organizations.up.sql:1-6`). It does not record who created it.
- **The only role column is scoped to a team, not an organisation.**
  `team_members.role` is `'owner' | 'member'` (`013_teams.up.sql:9-15`), read
  only by team and manager handlers (`backend/api/handlers_teams.go:536-540`,
  `handlers_manager.go:327`, `:575`). A team's `org_id` is nullable
  (`013_teams.up.sql:3`) and whoever creates a team is its owner, so team
  ownership is self-granted. Gating on it would be an escalation path.
- **`is_manager` is a detection, not a grant.** `user_profiles.is_manager`
  defaults false and is accompanied by `detected_at`
  (`016_manager.up.sql:1-7`) — it is inferred from calendar shape.
- **There is no operator-configured administrator list.** A case-insensitive
  grep for admin-shaped identifiers (`isAdmin`, `is_admin`, `admin_users`,
  `admin_emails`, `ADMIN_*`) across all of `backend/**/*.go` returns **nothing**.

### 2.6 Organisation membership is self-service

`AssociateUserWithOrg` (`backend/storage/orgs.go:42-52`) extracts the domain
from the user's email, skips generic consumer domains, and then **creates the
organisation if it does not exist** (`UpsertOrg`, `orgs.go:18-27`) and links the
user to it (`SetUserOrg`, `orgs.go:35-38`).

It has three call sites, and the load-bearing one is **ordinary Google
sign-up** (`backend/api/handlers_auth.go:76`); the others are the Microsoft
callback (`backend/api/handlers_microsoft_auth.go:74`) and the SSO callback
(`handlers_sso.go:191`). *(v1 cited only the SSO callback, which made
self-service membership look circular — you would already need SSO to obtain org
membership. The Google callback is the citation that establishes the claim, and
it is why an attacker with a corporate-domain address becomes an org member of a
deployment that has never configured SSO.)*

There is no invitation and no approval. The current gate reduces to **"has a
corporate email address"**.

### 2.7 An unusable configuration is accepted, and there is no update operation

There are exactly three admin routes: `POST /api/admin/sso`, `GET
/api/admin/sso`, `DELETE /api/admin/sso/{domain}`
(`backend/api/routes.go:171-175`). **There is no update operation.** The single
write is an upsert keyed on domain that overwrites every column from `EXCLUDED`
— provider name, type, enabled, issuer, client id, client secret and all three
SAML values (`sso_providers.go:63-73`). It is a replace, not a merge.

Create validates that domain, provider name and provider type are non-empty and
that the type is one of two values (`handlers_sso.go:223-230`). Nothing else.
The OIDC issuer, client id and client secret, and all three SAML fields, are
accepted empty and persisted empty (`handlers_sso.go:248-253`; the columns
default to `''`, `008_sso_providers.up.sql:7-12`).

The consequence is deferred and unhelpful. With an empty issuer, login
construction fails inside `auth.NewOIDCClient` and surfaces as a 500 with the
raw error (`handlers_sso.go:107-115`). A SAML provider is accepted and stored,
then the sign-in route rejects it with 501 "SAML not yet supported"
(`handlers_sso.go:99-102`) — so SAML is configurable but non-functional by
design. This is U-21 (`auth.yaml:810-813`) and API-060.

### 2.8 SSO cannot be disabled through the API

Create hardcodes `Enabled: true` (`handlers_sso.go:247`), and the replace-upsert
writes that value over the existing one on conflict (`sso_providers.go:66`). No
route sets it false. The column exists and works as a kill switch — the sign-in
lookup filters on `enabled = true` (`sso_providers.go:51`) — so the capability
is present in the schema and unreachable through the API. This is API-066.

### 2.9 Deleting a domain that has no configuration reports success, and no change is recorded

`DeleteSSOProvider` discards the result and returns only the error
(`sso_providers.go:108-112`), so the handler cannot distinguish "deleted one
row" from "matched nothing" and returns 204 either way
(`handlers_sso.go:308-312`). This is API-067.

Separately, and not in the register: **no SSO configuration change is recorded
anywhere.** None of the three handlers calls `WriteAuditLog`; the seven call
sites are focus, scheduling and NLP only (enumerated in §2.1). Substituting an
organisation's identity provider leaves no trace but an `updated_at` bump.

### 2.10 Consumers and tests

- **Frontend**: no match for the admin SSO route, `ssoProvider`,
  `OIDCClientSecret` or `SAMLCert` anywhere in
  `/home/claude/smart-calendar-flow/src`.
  But `smart-calendar-flow/src/components/auth/AuthDialog.tsx:32,99,111-113`
  **is** a consumer of the two sign-in operations this spec's suspend/resume
  criteria act on. It is already broken by API-010 (`api-audit.md:244`): it gates
  the SSO redirect on a property the backend never sends
  (`backend/auth/sso_detect.go:18-22`), so no user can reach the SSO flow
  through the UI at all.
- **MCP server**: no match for `sso` in any case anywhere under
  `/home/claude/clockwise-like/mcp/`.
- **e2e**: no scenario; the only occurrence of `sso` under `e2e/` is a mention in
  `e2e/TESTIDS-REQUIRED.md`.

This is the API-091 category (`api-audit.md:325`), whose own comment is that
API-001 "sat undetected in exactly this category".

**Test coverage, re-checked since PAC-45.** `backend/storage/sso_providers_test.go`
covers the storage layer (five tests at `:9`, `:47`, `:59`, `:89`, `:112`).
A handler test file now **exists** — `backend/api/handlers_sso_test.go`, added
by PAC-45 — but it covers the OIDC callback only (§2.2). **No test anywhere
exercises `createSSOProvider`, `listSSOProviders` or `deleteSSOProvider`**, so
nothing asserts anything about the authorization gate on the admin surface, the
response body, or its status codes. API-092 (`api-audit.md:326`) names six
operations; PAC-45 closed its `/api/auth/callback/oidc/{domain}` share, this
spec closes the three `/api/admin/sso` shares, and `/api/auth/detect`,
`/api/auth/sso/{domain}`, `/api/auth/me` and `/api/auth/logout` remain open
after it.

All three admin operations are `handwritten` in
`contracts/openapi/MIGRATION.md:55-57`.

---

## 3. Desired behaviour

The actor throughout is **a designated administrator**: an org member whom the
deployment operator has designated as able to administer SSO. That is not a
product role and is not claimed to be one — it is a deployment-level
designation, and replacing it with an in-product concept is the follow-up issue
(§0.4). Where behaviour is available to any org member, this section says "any
member".

### 3.1 The secrets stop leaving the server

A designated administrator can see that SSO is configured for their
organisation, which identity provider it points at, and whether it is currently
active. They cannot read back the client secret or the signing certificate, and
neither can anybody else, on any response from any operation. The secrets are
write-only: they can be supplied, and they can be replaced, and they can never
be retrieved.

A reader can still tell whether a secret has been supplied, because "configured
but no secret entered" and "configured with a secret" are different states that
the person configuring SSO needs to distinguish. Knowing that a secret exists is
not knowing the secret. That indicator must mean exactly that and nothing else —
it must not change when anything other than the secret changes.

Making the secrets write-only has a consequence this spec accepts deliberately:
an operator who forgets a client secret cannot retrieve it and must re-enter it.

### 3.2 Supplying a configuration means supplying all of it

There is one write today and it replaces the stored configuration wholesale
(§2.7). **This spec does not turn it into a merge.** Omitting a value on a write
means the value is absent, not "keep what is stored". So a change of any part of
the configuration requires supplying the whole configuration, secrets included.

This is stated plainly because v1 left it implicit and contradicted itself:
v1's AC-4 required that a retrieved representation submitted back unchanged
preserve the stored secret, which — once the secret is no longer in that
representation — is only possible if the write merges. **v1's AC-4 is struck.**
The cost is real and is accepted: renaming a provider means re-entering its
secret. The alternative is either a merge, which silently changes the stored-data
semantics of an existing operation, or a second write operation, which is
interface surface this spec does not need to buy its outcomes.

**One transition is exempt, and only one.** Suspending and resuming must be
possible without re-supplying secrets, because requiring a secret in order to
switch something off is the opposite of an emergency control. That is a named
state transition, not a partial write of arbitrary values; how it is expressed
is the contract's decision.

### 3.3 A configuration that cannot work is refused at the point of entry

Someone setting up SSO who omits something the chosen kind of identity provider
requires is told immediately, in terms naming what is missing, instead of finding
out when a colleague cannot log in. Because writes replace (§3.2), "required"
means required on every write, with no exception for values already stored.

The same applies to the kind of identity provider the product cannot actually
complete a login with: if it cannot work, it is refused when offered rather than
stored and rejected at sign-in.

### 3.4 SSO can be turned off without being thrown away

A designated administrator investigating a misconfiguration that is locking
colleagues out can suspend SSO, leaving the configuration intact to fix, and
turn it back on. Suspending it is a deliberate act: a write that says nothing
about whether SSO is active leaves it active, so nobody switches SSO off by
omission.

**Suspension stops new sign-ins; it does not end sessions.** Sessions already
established through the provider survive suspension, deletion and reconfiguration
alike, because the session is a self-contained bearer credential with a week's
life and no server-side state. An operator reaching for suspend after a
compromise must be told this, and it is out of scope to change it (§5).

### 3.5 Removing a configuration reports honestly

Removing something that is there and removing something that was never there are
distinguishable outcomes, so an administrator who mistypes a domain learns that
they did, instead of being told the removal succeeded.

### 3.6 The response shape is stable and conventional

The shape stops being an accident of how the server happens to store the data and
becomes a described, deliberate shape, consistent with the rest of the API. In
particular the retrieval response is a named container rather than a bare
sequence, so a later addition does not change the response's type for every
consumer at once.

### 3.7 Only the exact organisation's own domain, and only a designated administrator

Two restrictions on writing, neither of which needs a product decision (§0.2):

**A caller may configure or remove SSO only for their organisation's domain,
exactly.** Not a subdomain of it. This aligns the write path with what the
listing and the sign-in path already do, and it means a member of one
organisation can no longer create, overwrite or remove the SSO configuration of
another organisation whose domain happens to sit beneath theirs. It also means a
configuration can no longer be installed that its own organisation's listing
cannot see.

**Only a designated administrator may create, change, suspend, resume or remove
an SSO configuration.** A deployment where the operator has designated nobody
refuses every such attempt and says why — it fails closed. This is openly an
interim mechanism: it is a deployment-level designation, not authorization the
product can express, and the follow-up issue replaces it. It is chosen over the
switch v1 proposed because it distinguishes somebody from anybody at the same
cost, and because it survives the rotation window (§0.3).

**Reading remains available to any member for now.** With the secrets gone, what
is left is the fact that the organisation uses SSO and the name of its provider —
which any member learns by signing in. Restricting the read further is the
follow-up issue's job, and doing it here would mean restricting it to whoever an
operator happened to list.

### 3.8 Changing SSO configuration leaves a trace

Creating, changing, suspending, resuming or removing an SSO configuration is
recorded, attributably, so that a substitution can be found after the fact. The
record never carries a secret value. This does not stop the residual in §2.3 —
it makes it detectable, which is the difference between an incident that is
investigated and one that is not noticed.

### 3.9 The sign-in binding stays bound

The binding between the asserted identity and the provider's domain that PAC-45
introduced is load-bearing for every severity claim in this spec. A successful
sign-in for an address at exactly the provider's domain continues to work; an
assertion for any other domain, including a subdomain, continues to be refused
with no session issued. This spec must not relax either, and — unlike v1 — does
not describe the requirement as "as it behaves today", because that phrasing is
what froze the defect PAC-45 fixed.

---

## 4. Acceptance criteria

Each is falsifiable: none of these tests exist (§2.10 — nothing exercises the
admin surface at all), and each fails against current behaviour. Where a
criterion asserts a refusal, it also asserts a stored-state outcome, because the
403 on this surface is shared with the database-error path (§2.4) and a status
code alone can pass for the wrong reason.

**Secret non-disclosure**

AC-1. Given an SSO configuration exists for an organisation with a non-empty
client secret and a non-empty signing certificate, when a member of that
organisation retrieves the organisation's SSO configuration, then no value
anywhere in the response equals either stored value, and no property of the
response carries either one under any name.  `[contract]`

AC-2. Given a request that stores an SSO configuration and supplies a client
secret, when the request succeeds, then the response describes the stored
configuration and contains no value equal to the supplied secret.  `[contract]`

AC-3. Given two SSO configurations, one whose client secret is non-empty and one
whose client secret is empty, when each is retrieved, then exactly one described
property differs between them, its documented meaning is "a secret is on file",
and it is true for the first and false for the second.  `[contract]`

AC-4. Given a stored configuration with a non-empty client secret, when it is
replaced by a write that changes only its display name and supplies the same
secret, then the property of AC-3 is unchanged; and when it is replaced by a
write that supplies an empty secret, then that property becomes false. The
indicator tracks the secret and nothing else.  `[contract]`

AC-5. Given a configuration stored before this change with an empty client
secret and empty certificate, when it is retrieved, then the response is valid
against the described shape and reports that no secret is on file, rather than
failing to serialise or omitting the property.  `[contract]`

**Response shape**

AC-6. Given any number of SSO configurations for an organisation, when they are
retrieved, then the top level of the response is a named container and not a bare
sequence, and the response validates against the described shape when the
organisation has none, one, and more than one.  `[contract]`

**Validation at entry**

AC-7. Given no SSO configuration for a domain, when a designated administrator
submits one naming a kind of identity provider but omitting a value that kind
requires to complete a sign-in, then the request is rejected with a client error
naming the missing value, and afterwards no configuration exists for that
domain.  `[contract]`

AC-8. Given the current state, when a designated administrator submits a
configuration for the kind of identity provider the product cannot complete a
sign-in with, then the request is rejected at submission time with a client error
saying so, and nothing is stored.  `[contract]`
*(This changes existing behaviour — see OQ-2, which must settle it before the
contract is written. If OQ-2 settles the other way, AC-8 is struck and the
contract documents the deferred rejection instead.)*

**Who may write**

AC-9. Given a deployment where the operator has designated nobody to administer
SSO, when any authenticated org member attempts to create, change, suspend,
resume or remove an SSO configuration, then the attempt is refused with an error
naming the reason, distinguishable from the error returned to a caller with no
organisation, and the stored configuration is byte-for-byte unchanged; and
retrieving the configuration still succeeds for that same caller.  `[contract]`

AC-10. Given a deployment where the operator has designated one org member and
not another, when the designated member creates a configuration for their
organisation's domain it succeeds, and when the undesignated member attempts the
same write it is refused and nothing is stored.  `[contract]`

AC-11. Given a designated administrator, when they attempt to write a
configuration for a domain that is not their own organisation's, then the
designation does not help them — the attempt is refused. Designation says who
may write, not what they may write.  `[contract]`

**Which domain may be written**

AC-12. Given organisations for `acme.com` and for `eu.acme.com`, and a
configuration stored for `eu.acme.com`, when a designated administrator of
`acme.com` retrieves their organisation's configurations, then the
`eu.acme.com` configuration does not appear in the response and the response
succeeds.  `[contract]`

AC-13. Given the same two organisations, when a designated administrator of
`acme.com` attempts to create or replace a configuration for `eu.acme.com`, then
the attempt is refused and `eu.acme.com`'s stored configuration is unchanged —
in particular its issuer and client credentials are unchanged.  `[contract]`

AC-14. Given the same two organisations, when a designated administrator of
`acme.com` attempts to remove `eu.acme.com`'s configuration, then the attempt is
refused and the configuration still exists and is still usable for sign-in.
`[contract]`

AC-15. Given an organisation for `acme.com` and no organisation for
`eu.acme.com`, when a designated administrator of `acme.com` attempts to create
a configuration for `eu.acme.com`, then the attempt is refused and afterwards no
configuration exists for `eu.acme.com` — it is not possible to install a
configuration that no organisation's listing can show.  `[contract]`

**Active state**

AC-16. Given an active SSO configuration, when a designated administrator
suspends it, then the sign-in routing step for that organisation's domain no
longer routes users to SSO, the SSO sign-in route reports no configuration for
that domain, and the configuration is still retrievable and reports itself
suspended; and when it is resumed, both resume.  `[e2e]`

AC-17. Given an active SSO configuration with a client secret, when it is
suspended and then resumed without any secret being supplied at either step,
then a sign-in that succeeded before the suspension succeeds again afterwards,
and every described value of the configuration other than its active state is
unchanged.  `[e2e]`

AC-18. Given an existing SSO configuration, when a write replaces it and says
nothing about whether it is active, then afterwards it is active — omission never
suspends SSO.  `[contract]`

**Removal**

AC-19. Given no SSO configuration for a domain that is exactly the caller's
organisation's, when a designated administrator removes that domain's
configuration, then the response reports that there was nothing to remove,
distinguishably from the response given when there was.  `[contract]`

**Observability**

AC-20. Given a designated administrator who creates, replaces, suspends, resumes
or removes an SSO configuration, when the change has succeeded, then a record of
that change exists identifying who made it and which organisation's domain it
concerned; and no such record contains any value equal to a supplied client
secret or signing certificate.  `[contract]`

**Preserved behaviour**

AC-21. Given an organisation whose SSO configuration points at a working
identity provider, when a user whose email domain is exactly that
configuration's domain signs in through SSO, then the sign-in completes and a
session is issued.  `[e2e]`

AC-22. Given the same configuration, when the identity provider asserts an
address whose domain is not exactly that configuration's domain — including an
address at a subdomain of it, and including an address that already exists as an
account on the deployment — then the sign-in is refused, no session is issued,
and the existing account is unmodified.  `[e2e]`
*(AC-21 and AC-22 replace v1's AC-13. They state the binding rather than
preserving whatever the code does, because the latter is what froze the PAC-45
defect. Handler-level equivalents exist as of PAC-45 — §2.2 — so what is new
here is proving them against the full stack and proving this work does not
weaken them.)*

**Struck from v1, recorded so the change is auditable**

| v1 | Disposition |
|---|---|
| AC-4 (round-trip preserves the omitted secret) | **Struck.** Presupposes an update operation that does not exist and a merge this spec forbids (§3.2). |
| AC-10 (operator on/off switch) | **Struck.** §0.3. Replaced by AC-9–AC-11. |
| AC-11 (switch on ⇒ any member may write) | **Struck** with AC-10. Its replacement is AC-10 above, which requires designation, not a capability. |
| AC-12 retrieve clause (cross-org retrieval refused) | **Struck.** Unfalsifiable — retrieval lists by the caller's own organisation and cannot refuse (§2.4). Replaced by AC-12 above (non-appearance) plus AC-13/AC-14 (refusal for write and remove). |
| AC-13 ("login completes exactly as today") | **Struck.** §3.9. Replaced by AC-21/AC-22. |

**Note on how AC-16, AC-17, AC-21 and AC-22 are proven.** There is no usable
user interface for any of this: the admin surface has no consumer at all, and the
sign-in surface's only consumer is broken by API-010, which this spec does not
fix (§5). These must be API-level scenarios against the running stack. Stage 5
should not read the absence of a UI journey as permission to skip them — AC-21
and AC-22 are the only full-stack check that this work does not break or weaken
SSO sign-in.

---

## 5. Explicitly out of scope

- **Introducing an in-product administrator concept, and replacing the operator
  designation with it.** The substance of §0. Belongs to the follow-up issue,
  blocked on OQ-1. The designation this spec requires is not a substitute and
  does not claim to be: it does not scale past deployments whose operator knows
  every administrator by name.
- **Restricting who may *read* the configuration.** With the secrets removed,
  what remains is not sensitive (§3.7). Restricting it needs the administrator
  concept.
- **Encrypting the secrets at rest.** They are plain text in the database
  (§2.1). This spec stops them crossing the API boundary, which is the reported
  finding; encryption at rest is a separate, larger piece of work with its own
  key-management questions.
- **Ending sessions when SSO is suspended, removed or reconfigured.** Sessions
  are self-contained week-long bearer credentials with no server-side state
  (§2.2), so nothing this spec does can revoke one. §3.4 requires that this be
  said plainly rather than fixed. Revocation is its own piece of work and the
  residual in §2.3 is not closed without it.
- **Comparing how an account was originally created against how it is being
  asserted.** An account created by Google sign-in can still be assumed by an
  SSO assertion for the same address (§2.2). PAC-45 bounded this to one domain;
  removing it entirely is a question about identity linking, not about this
  surface.
- **Rotating secrets already exposed.** Every secret configured before this ships
  must be assumed compromised and rotated at the identity provider by the
  organisation that owns it. That is an operational and customer-communication
  task, not a code change, and it does not become unnecessary because the leak is
  closed. OQ-7.
- **Making SAML work.** If OQ-2 settles on refusing it, this spec refuses it;
  implementing it is a feature.
- **Fixing the broken sign-in routing in the frontend (API-010).** Stated as a
  position rather than an omission, per the challenge. It is a frontend defect on
  two operations this spec only reads, it is already tracked in the audit's D5
  batch, and the cross-repo ordering rule puts frontend work after the contract
  lands anyway. The consequence is recorded in §4: every criterion touching
  sign-in must be proven at the API level, because no user can reach the SSO flow
  through the UI until API-010 is fixed. It also means §1's "a misconfiguration
  locking people out cannot be paused" describes a journey that, today, nobody
  can enter through the UI — the problem is real at the API, not yet at the UI.
- **Deciding whether this surface should exist at all (API-091 / audit batch
  E3).** The audit's position is that surfaces with no consumer and no test
  should be either built for or deleted. If the answer is "deleted", most of this
  spec is moot — but the leak is live now and cannot wait for that decision.
- **Building a UI for SSO configuration.** Would resolve API-091 for these
  operations; it is a frontend feature with its own spec.
- **Self-service organisation membership (§2.6).** A real weakness, and the
  reason the current gate is weaker than it appears, but it is the membership
  model's problem and touches every org-scoped surface. Note that the designation
  requirement in §3.7 neutralises it for *this* surface without fixing it
  anywhere else.
- **Configurations already stored for a domain that is nobody's exact
  organisation domain.** §3.7 stops new ones being created; it does not remove
  existing ones, and after it lands such a row is unreachable through the API by
  anybody. This must be checked for before release. OQ-5.

---

## 6. What must not change

- **SSO sign-in must keep working for configurations stored before this change,
  and its domain binding must not be relaxed.** Protected, as of PAC-45, by
  `backend/api/handlers_sso_test.go:138,162,172` and
  `backend/domain/domain_test.go:58` — which is the first test protection this
  surface has ever had. Those must keep passing. They are handler-level; AC-21
  and AC-22 add the full-stack guard, which still does not exist.
- **No stored data changes as a result of the non-disclosure work.** The secrets
  stay in the database; only what crosses the boundary changes. There should be
  no migration for that part. The suspend/resume and designation work may need
  one; if a migration appears for the non-disclosure part, scope has grown.
- **The write stays a replace, not a merge.** §3.2. The one new partial
  transition is suspend/resume. Protected by
  `backend/storage/sso_providers_test.go:59` (`TestUpsertSSOProvider_Update`),
  which asserts replace semantics today.
- **Cross-organisation isolation is *not* a baseline this work preserves — it is
  a baseline this work establishes.** v1 claimed it was "currently enforced by
  the domain check". It is not: the check is a suffix match and organisations are
  per-domain (§2.4). No test protects anything here. AC-12 to AC-15 are new
  guards, not regression guards.
- **Sign-in routing reads the same rows** (`backend/auth/sso_detect.go:37`) and
  must be unaffected except where AC-16 deliberately changes it. No test protects
  it: `backend/auth/` has no test for `DetectAuthProvider`.
- **The secrets must not become recoverable by another route.** Making them
  write-only means an operator who forgets a client secret must re-enter it
  (§3.1). That is the intended trade and must not be softened later with a
  "reveal" affordance.
- **Anything reading the current PascalCase keys breaks.** I found no such
  reader: none in the frontend, none in the MCP server, none in e2e (§2.10),
  which is what U-22 predicted. But absence of a consumer in these two repos is
  not absence of a consumer — a script or saved request outside the tree would
  break silently. OQ-3.
- **The 403 on this surface is also its database-error path** (§2.4). Every new
  criterion asserting a refusal asserts a stored-state outcome too, and the
  contract should give the new refusals a reason distinguishable from "org
  membership required". If it does not, AC-9 to AC-15 can pass for the wrong
  reason.

---

## 7. Rollback

**The non-disclosure part has no migration and loses no data on the way back.**
Nothing in it changes stored data, and the secret columns keep their values
throughout.

Reverting it restores the leak, in full, for every organisation — so a revert is
not a neutral act and should be treated as reopening a critical finding rather
than as a rollback. If a defect is found after release, prefer fixing forward.

Four things do not roll back cleanly:

1. **Secrets entered after release are unrecoverable through the API.** They are
   in the database, so nothing is lost; but an operator who relied on reading
   them back must go to the database. By design (§3.1).
2. **The designation requirement fails closed.** A deployment that upgrades
   without designating anybody cannot configure SSO at all until it does. That is
   the intended behaviour and it is a foreseeable support incident; it is also
   the fastest thing on this list to remedy, because it is deployment
   configuration rather than a deploy.
3. **The exact-domain restriction can strand existing rows.** After it lands, a
   stored configuration whose domain is nobody's exact organisation domain cannot
   be listed, changed or removed through the API by anybody, while remaining live
   for sign-in. Reverting the code restores the ability to remove it. This is the
   one place where fixing forward may be worse than reverting, and it is why
   OQ-5 asks for the inventory *before* release rather than after.
4. **Whatever records SSO changes (§3.8) accumulates rows that a revert leaves
   behind.** Harmless, but a down migration that drops them loses the only
   evidence of any substitution that happened in the meantime — so it should not.

The behaviour changes a consumer could notice — the removed secret properties,
the key casing, the response envelope, the removal-of-nothing outcome, the
refusal of configurations previously accepted — all revert with the code, because
no state depends on them.

---

## 8. Open questions

**OQ-1 — What makes someone an administrator of an organisation, as a product
concept, and who is the first one?** *Owner: human (product). Blocks the
follow-up issue, not this spec.* No candidate signal in the codebase is fit for
purpose (§2.5), so the answer is a decision, not a discovery. It has three parts:
(a) what an administrator is permitted to do; (b) where that is recorded; (c) how
the first administrator of an existing organisation comes to be one. v1 called
(c) the hard part and the challenger disagreed, pointing out that an
operator-configured mechanism answers it at deployment-configuration cost. This
spec accepts that and uses it (§3.7), **which removes (c) as a blocker for
shipping and leaves it as a blocker only for the in-product model.** The
remaining question is therefore narrower than v1's: given that a deployment
operator can already designate principals, what does the product need in
addition, and for whom? My reading is that the answer is driven entirely by
whether Paceday intends multi-tenant SaaS deployments where the operator cannot
know every customer's administrators by name — and that is a product question.

**OQ-2 — Should the unimplementable kind of identity provider be refused at
submission instead of at sign-in?** *Owner: human (product), before the contract
is written.* Today it is accepted and stored, then rejected at sign-in (§2.7).
Refusing it at submission is the honest behaviour and is what AC-8 assumes, but
it removes a capability someone may be using to stage a configuration ahead of
the feature existing, and any already-stored row of that kind becomes
unreplaceable. Resolve by deciding; if the decision is to keep accepting it, AC-8
is struck and the contract documents the deferred rejection.

**OQ-3 — Is there a consumer of this surface outside the two repositories?**
*Owner: whoever operates the existing deployments.* The response shape change
breaks any reader of the current PascalCase keys. There is none in the frontend,
the MCP server or e2e (§2.10), but I cannot see operator scripts, saved requests
or anything that has ever called this by hand. The risk is low but not zero, and
I could only establish this by reading — no deployment logs were available.

**OQ-4 — How does the operator designate a principal, and is the designation
per-deployment or per-organisation?** *Owner: human (product/ops), before the
contract is written.* §3.7 requires designation but deliberately does not say
what a designation identifies or how many organisations one covers. On a
single-tenant deployment the distinction does not arise; on a deployment hosting
several organisations, a flat list of principals means a designated
administrator of one organisation is designated for all of them — AC-11 requires
that this does not let them write *another* organisation's domain, but they would
still be designated in principle. Decide whether that is acceptable for the
interim or whether the designation must name the organisation too.

**OQ-5 — Are there stored SSO configurations for a domain that is nobody's exact
organisation domain, and what happens to them?** *Owner: whoever operates the
existing deployments, before release.* §3.7 makes such rows unmanageable through
the API (§7 item 3) while leaving them live for sign-in. They must be inventoried
before this ships, and each one either removed or given an organisation. This
cannot be answered from the repository — it needs a query against each running
deployment. It subsumes v1's OQ-4, which asked what should happen to subdomain
configurations in general: §3.7 answers that for new writes, and this question is
what remains.

**OQ-6 — Will the audit register accept API-001 being split into a disclosure row
and an authorization row?** *Owner: whoever maintains
`docs/factory/api-audit.md`.* Required by §0.4: as one row, API-001 cannot be
closed by PAC-22, and touch-it-fix-it would then either block this spec or be
quietly broken. Unchanged from v1. Resolve before the contract stage, since the
contract gate enforces it. API-092 needs the same treatment in a milder form —
it names six operations and this spec closes three of them (§2.10), so either the
row is split or it is annotated as partially closed.

**OQ-7 — Who tells existing customers to rotate their identity-provider
secrets?** *Owner: human.* Out of scope for the code (§5) and cannot be dropped:
closing the leak does not un-expose a secret that has been readable by an
organisation's whole staff. Needs a named owner and a sequence. v1 noted that
rotation requires the capability its kill-switch disabled; with the switch struck
and designation in its place (§0.3), the sequence is now workable — the operator
designates themselves or the customer's security contact for the duration of the
rotation, rather than opening writes to every employee.

**OQ-8 — Where is an SSO configuration change recorded, given that the existing
audit log is readable by everyone?** *Owner: contract-author, with the API-005
owner.* §3.8 requires the change to be recorded but deliberately does not say
where. The obvious destination is the existing audit log, which has no user or
organisation predicate and is readable by any authenticated user (API-005,
`api-audit.md:239`) — so writing SSO-change records there would make one
organisation's configuration history readable by another's members. The records
carry no secret (AC-20), so this is not a leak of the kind this spec exists to
close, but it is a new disclosure on a surface that already has a critical
finding against it. Either the destination is scoped first, or AC-20 is satisfied
somewhere else, or the exposure is accepted explicitly. Decide before the
contract is written.

**OQ-9 — Is PAC-45 landing on `main`, and when?** *Owner: the PAC-45 assignee.*
Every severity claim in this spec assumes it. At the time of writing it is In
Progress with a pull request open, and §2's citations are against its branch. If
it does not land before this spec's contract is written, §0 must be re-argued
from the pre-PAC-45 facts — and in that case the urgency of §3.7's designation
requirement rises sharply, because the residual is deployment-wide rather than
domain-bounded.

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
