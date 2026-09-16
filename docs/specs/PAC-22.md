# Spec — PAC-22: SSO configuration must not disclose identity-provider secrets, and must be administered by an administrator

- **Linear**: https://linear.app/paceday/issue/PAC-22/add-an-ssoproviderdto-that-omits-oidcclientsecret-and-samlcert-and
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**:
  - **Resolved by this spec**: API-001 (disclosure half only — see §0), API-060,
    API-066, API-067, `x-uncertain` U-21, `x-uncertain` U-22.
  - **Split out of this spec, must be filed as its own issue**: API-001
    (authorization half — the absence of any administrator concept).
  - **Inherited, explicitly not resolved**: API-091 (these three operations have
    no consumer anywhere). API-091 is a product decision tracked as audit batch
    E3; it cannot be resolved by this work and is not made worse by it. See §5.

> **A note on this spec's own evidence.** The sandbox this spec was written in
> blocks `proxy.golang.org`, so nothing here was established by compiling or
> running the backend. Every claim in §2 comes from reading source, and each
> carries a `file:line`. Two claims in §2 are inferences from reading rather than
> observations of a live response, and are marked as such.

---

## 0. Scope: this issue is two problems and should be two specs

The Linear issue asks for two things in one breath: stop the secrets leaking, and
put the configuration surface behind "a real admin check". Per the spec-author
rules, I am saying plainly that these should not be one spec, and why.

**They have different shapes.** Not disclosing a secret is a projection: decide
what leaves the server. It changes no stored data, needs no migration, and needs
no decision from anybody — there is no reading of the product in which the
identity-provider client secret should be readable over the API.

Gating the surface behind an administrator is the introduction of a privilege
model that does not exist (§2.3). It needs a product decision about who is an
administrator, a schema change to record it, and — the part that has no obvious
answer — a bootstrap rule, because on the day it ships every existing deployment
has zero administrators and SSO becomes unconfigurable by anybody.

**They have different urgency.** The leak is live and rated critical. The
privilege model is blocked on a decision with no named owner (§8, OQ-1). Bundling
them means the critical leak ships when the product decision lands.

**But the split must be honest about what it leaves open.** The leak is only half
of API-001, and it is arguably the less severe half. Any org member can today
*write* the org's SSO configuration (§2.2). Pointing an org at an
attacker-controlled identity provider is a full account-takeover primitive for
every user in that org, and withholding secrets from the read path does nothing
about it. So this spec cannot simply defer the authorization problem and declare
the org safe. It carries an interim mitigation (AC-8) that reduces the write path
to something an operator has deliberately switched on, which is not a privilege
model and is not claimed to be one.

**Proposed split:**

| Spec | Covers | Blocked on |
|---|---|---|
| **PAC-22** (this one) | Secrets never leave the server; response shape stabilised; create-time validation; delete distinguishes missing from deleted; enabled/disabled state settable; interim mitigation on the write path | nothing |
| **new issue — "org administrator concept and SSO authorization gate"** | Who may administer an org; recording it; bootstrapping the first one; gating this surface on it; removing PAC-22's interim mitigation | OQ-1 (product decision) |

This split requires one bookkeeping change outside these specs: API-001 is a
single register row covering both halves, so under the factory's touch-it-fix-it
rule PAC-22 would be unable to close it. API-001 should be split in
`docs/factory/api-audit.md` into a disclosure row (closed by PAC-22) and an
authorization row (closed by the new issue), so the register stays true and the
contract gate has something resolvable. See OQ-5.

---

## 1. Problem

Paceday lets an organisation replace password login with its own identity
provider. To do that, somebody hands Paceday the credentials that let Paceday
impersonate the organisation to that identity provider: an OIDC client secret, or
a SAML signing certificate. These are the keys to the organisation's front door.

Today any employee with a working Paceday account can read them.

The organisation's SSO configuration is served back in full, secrets included, to
any authenticated caller who belongs to the organisation. A curious colleague
with browser devtools reads the client secret. Anyone who phishes a single
low-privilege account — an intern, a contractor, a departed employee whose
account was not disabled — walks away with credentials to impersonate Paceday to
the organisation's identity provider.

**Who is affected.** Every organisation that has configured SSO, and every user
in it. The organisation's security team is affected worst: they handed over a
secret on the understanding that it was held, not published, and no audit on
their side would reveal that it is readable by their whole staff.

**What it costs them.** A secret that any employee can read is not a secret, and
must be treated as compromised the moment this is understood. The remedy is
rotation at the identity provider for every organisation that ever configured
SSO — an action they must take, coordinated with a Paceday reconfiguration, with
SSO login broken in between.

Two smaller problems sit on the same surface and cost the person doing the
configuring rather than the organisation. An SSO configuration that cannot
possibly work is accepted without complaint, and the person who entered it finds
out only when a colleague cannot log in. And once SSO is configured there is no
way to turn it off again short of deleting it — so a misconfiguration that is
locking people out cannot be paused while it is diagnosed.

---

## 2. Current behaviour

All citations are from reading; nothing was executed (see the note above).

### 2.1 The response contains the secrets

`storage.SSOProvider` (`backend/storage/sso_providers.go:12-26`) declares thirteen
fields and **no struct tags**, among them `OIDCClientSecret`
(`sso_providers.go:20`) and `SAMLCert` (`sso_providers.go:23`).

The list handler encodes the slice of these structs directly
(`backend/api/handlers_sso.go:276`), and the create handler encodes the stored
struct directly (`handlers_sso.go:254`). There is no intermediate type in either
path. Both secret columns are selected into the struct on every read
(`sso_providers.go:28-31` column list, `:33-45` scan, `:84-87` the list query).

Because the struct has no tags, `encoding/json` emits the Go field names, so the
response keys are PascalCase (`ID`, `Domain`, `OIDCClientSecret`, …). *This is an
inference from reading the struct definition, not an observed response* — and it
is the same inference the contract records as unverified in U-22
(`contracts/openapi/paths/auth.yaml:871-874`). The contract documents the full
PascalCase shape including `OIDCClientSecret`, with a comment that it is
documented "because it is what the code does, not because it is intended"
(`auth.yaml:815-870`).

The column type is plain `TEXT` with no encryption at rest
(`backend/storage/migrations/008_sso_providers.up.sql:9,12`).

### 2.2 The write path has the same gate as the read path

All three handlers perform the identical check: load the caller, require that
their organisation reference is non-nil, otherwise 403 "org membership required".
Create at `handlers_sso.go:193-198`, list at `:259-264`, delete at `:283-288`.

Create and delete additionally require that the target domain match the caller's
own organisation domain (`handlers_sso.go:230-233`, `:295-298`), via
`domain.DomainMatchesOrg` (`backend/domain/domain.go:31-35`). That is a check
about *which* organisation you may configure. It is not a check about *whether you
are entitled to configure one*.

So: any org member can create or overwrite their organisation's SSO configuration,
including replacing the issuer with one of their choosing
(`handlers_sso.go:235-246`, upsert on conflict by domain at
`sso_providers.go:63-73`), and any org member can delete it (`:300-303`).

### 2.3 There is no administrator concept anywhere

I looked for one, on the assumption that the audit might have missed it. It is
genuinely absent, and each candidate fails for a different reason:

- **The user record has no role.** `users` is id, email, name, avatar, provider,
  provider_id, timestamps (`migrations/006_auth.up.sql:3-12`), plus a nullable
  `org_id` added later (`007_organizations.up.sql:8`). No role, no flag.
- **The organisation record has no owner.** `organizations` is id, name, domain,
  created_at (`007_organizations.up.sql:1-6`). It does not record who created it.
- **The only role column is scoped to a team, not an organisation.**
  `team_members.role` is `'owner' | 'member'` (`013_teams.up.sql:9-15`) and is
  read only by team and manager handlers (`backend/api/handlers_teams.go:538`,
  `handlers_manager.go:327,575`). It is not an org privilege: a team's `org_id` is
  nullable (`013_teams.up.sql:3`), and whoever creates a team is its owner, so
  team ownership is self-granted. Gating on it would be an escalation path, not a
  gate.
- **`is_manager` is a detection, not a grant.** `user_profiles.is_manager`
  defaults false and is accompanied by `detected_at`
  (`016_manager.up.sql:1-7`) — it is inferred from calendar shape. Gating on it
  would give the client secret to anyone whose calendar looks managerial.
- **There is no operator-configured administrator list.** A grep for admin-shaped
  identifiers across `backend/`, `backend/api/` and `backend/auth/` returns only
  a section comment (`handlers_sso.go:189`) and unrelated matches in test
  database setup.

### 2.4 Organisation membership is self-service

This is what makes the gate in §2.2 weaker than "any org member" suggests.
`AssociateUserWithOrg` (`backend/storage/orgs.go:42-52`) extracts the domain from
the user's email, skips generic consumer domains, and then **creates the
organisation if it does not exist** (`UpsertOrg`, `orgs.go:18-27`) and links the
user to it (`SetUserOrg`, `orgs.go:35-38`). It is called on the SSO login path
(`handlers_sso.go:183`).

There is no invitation and no approval. Anyone who can receive mail at a
non-generic domain becomes a member of that domain's organisation on signup, and
the first such person silently brings the organisation into existence. The
current gate therefore reduces to "has a corporate email address".

### 2.5 An unusable configuration is accepted

Create validates that domain, provider name and provider type are non-empty and
that the type is one of two values (`handlers_sso.go:215-222`). Nothing else. The
OIDC issuer, client id and client secret, and all three SAML fields, are accepted
empty and persisted empty (`handlers_sso.go:240-245`; the columns default to `''`,
`008_sso_providers.up.sql:7-12`).

The consequence is deferred and unhelpful. With an empty issuer, login
construction fails inside `auth.NewOIDCClient` and surfaces as a 500 with the raw
error (`handlers_sso.go:107-115`). A SAML provider is accepted and stored, then
the login route rejects it with 501 "SAML not yet supported"
(`handlers_sso.go:99-102`) — so SAML is configurable but non-functional by design.
This is U-21 (`auth.yaml:810-813`) and API-060.

### 2.6 SSO cannot be disabled through the API

Create hardcodes `Enabled: true` (`handlers_sso.go:239`), and the upsert writes
that value over the existing one on conflict (`sso_providers.go:66`). No route
sets it false. The column exists and works as a kill switch — the login lookup
filters on `enabled = true` (`sso_providers.go:51`) — so the capability is
present in the schema and unreachable through the API. This is API-066.

### 2.7 Deleting a domain that has no configuration reports success

`DeleteSSOProvider` discards the result and returns only the error
(`sso_providers.go:108-112`), so the handler cannot distinguish "deleted one row"
from "matched nothing" and returns 204 either way (`handlers_sso.go:300-304`).
This is API-067.

### 2.8 A configuration can be created that the organisation's own list cannot see

I did not find this in the audit; it follows from reading the two queries
together.

Create accepts any domain that is equal to *or a subdomain of* the caller's
organisation domain — `DomainMatchesOrg` is a suffix match
(`domain.go:31-35`). The list query joins on **exact** domain equality:
`JOIN organizations o ON o.domain = s.domain` (`sso_providers.go:88-90`).

So a member of `acme.com` may create a configuration for `eu.acme.com`, and it
will never appear in `acme.com`'s listing. It is nonetheless live: the login
lookup is a direct exact-domain read with no org join (`sso_providers.go:47-54`),
and provider detection looks up the email domain exactly
(`backend/auth/sso_detect.go:26-47`), so any user whose address is at that
subdomain is routed to it. Deleting it is possible but only by knowing the domain
to name.

An attacker-member can therefore install an identity provider the organisation's
administrators cannot see in their own configuration list. This compounds
whatever authorization is added later: a gate on the write path closes the way in,
but a gate does not make an already-installed invisible row visible.

### 2.9 Nothing consumes these operations, and nothing tests the handlers

I checked both consumers, including the one that gets forgotten:

- **Frontend**: no match for the admin SSO route anywhere in
  `/home/claude/smart-calendar-flow/src`.
- **MCP server**: no match for the route, and no match for `sso` in any case in
  `/home/claude/clockwise-like/mcp/` at all (`client.go`, `tools.go`, `main.go`
  and their tests).
- **e2e**: no scenario. The only occurrence of `sso` under `e2e/` is a mention in
  `e2e/TESTIDS-REQUIRED.md`, which is not a test.

This is the API-091 category (`api-audit.md:325`), and the audit's own comment on
it is that API-001 "sat undetected in exactly this category"
(`api-audit.md:325`).

Test coverage on this surface: `backend/storage/sso_providers_test.go` covers the
storage layer — upsert, get, update, delete, list by org (five tests at `:9`,
`:47`, `:59`, `:89`, `:112`). There is **no handler test file for SSO**; no file
in `backend/api/*_test.go` references the SSO handlers. So no test today asserts
anything about the authorization gate, the response body, or the status codes.

All three operations are `handwritten` in `contracts/openapi/MIGRATION.md:55-57`.

---

## 3. Desired behaviour

**The secrets stop leaving the server.** An administrator can see that SSO is
configured for their organisation, which identity provider it points at, and
whether it is currently active. They cannot read back the client secret or the
signing certificate, and neither can anybody else, on any response from any
operation. The secrets are write-only from the API's point of view: they can be
supplied, and they can be replaced, and they can never be retrieved.

A reader can still tell whether a secret has been supplied, because "configured
but no secret entered" and "configured with a secret" are different states that
the person configuring SSO needs to distinguish. Knowing that a secret exists is
not knowing the secret.

**A configuration that cannot work is refused at the point of entry.** Someone
setting up SSO who omits something the chosen kind of identity provider requires
is told immediately, in terms naming what is missing, instead of finding out when
a colleague cannot log in. The same applies to the kind of identity provider that
the product cannot actually complete a login with: if it cannot work, it is
refused when offered rather than stored and rejected at login.

**SSO can be turned off without being thrown away.** An administrator
investigating a misconfiguration that is locking colleagues out can suspend SSO,
leaving the configuration intact to fix, and turn it back on. Suspending it is a
deliberate act: an administrator who edits an unrelated part of the configuration
does not switch SSO off by omission, because a request that says nothing about
whether SSO is active leaves that as it was.

**Removing a configuration reports honestly.** Removing something that is there
and removing something that was never there are distinguishable outcomes, so an
administrator who mistypes a domain learns that they did, instead of being told
the deletion succeeded.

**The response shape is stable and conventional.** The shape stops being an
accident of how the server happens to store the data and becomes a described,
deliberate shape, consistent with the rest of the API.

**Until an administrator concept exists, changing SSO configuration is not
something any employee can do by default.** A deployment where nobody has
deliberately enabled SSO self-configuration refuses attempts to create, change or
remove an SSO configuration, and says why. This is a stopgap, openly a stopgap: it
is not authorization, it distinguishes nobody from anybody, and it exists only so
that the takeover path in §2.2 is not left open while the administrator concept is
designed. The follow-up spec removes it.

**Reading remains available to org members for now.** With the secrets gone, what
is left is the fact that the organisation uses SSO and the name of its provider —
which any member learns by logging in. Restricting the read further is the
follow-up spec's job, and doing it here without an administrator concept would
mean restricting it to nobody.

---

## 4. Acceptance criteria

Each is falsifiable today: none of these tests exist (§2.9 — there is no handler
test file for this surface at all), and each fails against current behaviour.

**Secret non-disclosure**

AC-1. Given an SSO configuration exists for an organisation with a non-empty
client secret and a non-empty signing certificate, when a member of that
organisation retrieves the organisation's SSO configuration, then no value
anywhere in the response equals either stored value, and no property of the
response carries either one under any name.  `[contract]`

AC-2. Given a request that creates or replaces an SSO configuration and supplies
a client secret, when the request succeeds, then the response describes the
stored configuration and does not contain the supplied secret.  `[contract]`

AC-3. Given an SSO configuration whose client secret is non-empty and another
whose client secret is empty, when each is retrieved, then the responses differ in
a way that identifies which has a secret on file, and neither reveals its value.
`[contract]`

AC-4. Given an SSO configuration created with a client secret, when that
configuration is retrieved and the returned representation is submitted back
unchanged as an update, then the stored secret is unchanged — round-tripping a
representation that omits the secret must not erase it.  `[unit]`

**Validation at entry**

AC-5. Given no SSO configuration for a domain, when a caller submits one naming a
kind of identity provider but omitting a value that kind requires to complete a
login, then the request is rejected with a client error naming the missing value,
and afterwards no configuration exists for that domain.  `[contract]`

AC-6. Given the current state, when a caller submits a configuration for the kind
of identity provider the product cannot complete a login with, then the request is
rejected at submission time with a client error saying so, rather than stored.
`[contract]`
*(This changes existing behaviour — see OQ-2, which must settle it before the
contract is written.)*

**Active state**

AC-7. Given an active SSO configuration, when an administrator suspends it, then
provider detection for that organisation's domain no longer routes users to SSO
and the SSO login route reports no configuration, while the configuration remains
retrievable; and when it is reactivated, both resume.  `[e2e]`

AC-8. Given an existing active SSO configuration, when a request updates some
other part of it and says nothing about whether it is active, then it remains
active — omission never suspends SSO.  `[unit]`

**Removal**

AC-9. Given no SSO configuration for a domain within the caller's organisation,
when the caller removes that domain's configuration, then the response reports
that there was nothing to remove, distinguishably from the success reported when
there was.  `[contract]`

**Interim mitigation on the write path**

AC-10. Given a deployment that has not enabled SSO self-configuration, when any
authenticated org member attempts to create, change or remove an SSO
configuration, then the attempt is refused with an error stating that SSO
configuration is not enabled for this deployment, and the stored configuration is
unchanged; and retrieving the configuration still succeeds.  `[contract]`

AC-11. Given a deployment that has enabled SSO self-configuration, when an
authenticated org member creates a configuration for their own organisation's
domain, then it succeeds — the mitigation is a switch, not a removal of the
capability.  `[e2e]`

**Preserved behaviour**

AC-12. Given a configuration belonging to one organisation, when a member of a
different organisation attempts to retrieve, change or remove it, then the attempt
is refused and the configuration is unchanged.  `[contract]`

AC-13. Given an organisation with a working SSO configuration, when a user at
that organisation's domain logs in through SSO, then the login completes exactly
as it does today.  `[e2e]`

AC-14. Given an SSO configuration stored before this change with an empty client
secret and empty certificate, when it is retrieved, then the response is valid
against the described shape and reports that no secret is on file, rather than
failing to serialise.  `[contract]`

**Note on how AC-7, AC-11 and AC-13 are proven.** There is no user interface for
this surface (§2.9), so these cannot be driven through the frontend. They must be
API-level scenarios against the running stack. Stage 5 should not interpret the
absence of a UI journey as permission to skip them — AC-13 in particular is the
only check that this work does not break SSO login, and no existing test covers
it.

---

## 5. Explicitly out of scope

- **Introducing an administrator concept, and gating this surface on it.** The
  substance of §0. Belongs to the follow-up issue, blocked on OQ-1. This spec's
  AC-10 mitigation is not a substitute and does not claim to be.
- **Restricting who may *read* the configuration.** With the secrets removed,
  what remains is not sensitive (§3). Restricting it requires the administrator
  concept.
- **Encrypting the secrets at rest.** They are plain text in the database
  (§2.1). This spec stops them crossing the API boundary, which is the reported
  finding; encryption at rest is a separate, larger piece of work with its own
  key-management questions.
- **Rotating secrets already exposed.** Every secret configured before this ships
  must be assumed compromised and rotated at the identity provider by the
  organisation that owns it. That is an operational and customer-communication
  task, not a code change, and it does not become unnecessary because the leak is
  closed. It needs an owner — OQ-6.
- **Making SAML work.** If OQ-2 settles on refusing it, this spec refuses it;
  implementing it is a feature.
- **The invisible-subdomain configuration (§2.8).** Discovered while writing this
  spec, not in the register, and not the reported problem. It should be filed
  separately; see OQ-4. It is deliberately not bundled here because it is a
  question about what a domain means to an organisation, which will have an answer
  once the administrator concept has one.
- **Deciding whether this surface should exist at all (API-091 / audit batch
  E3).** The audit's position is that surfaces with no consumer and no test should
  be either built for or deleted. If the answer turns out to be "deleted", most of
  this spec is moot — but the leak is live now and cannot wait for that decision.
- **Building a UI for SSO configuration.** Would resolve API-091 for these
  operations; it is a frontend feature with its own spec, and the cross-repo
  ordering rule puts it after the contract lands anyway.
- **Self-service organisation membership (§2.4).** A real weakness, and the reason
  the current gate is weaker than it appears, but it is the membership model's
  problem and touches every org-scoped surface, not this one.

---

## 6. What must not change

- **SSO login must keep working, for configurations stored before this change.**
  No test protects this today: there is no handler test for the SSO surface and no
  e2e scenario (§2.9). Storage-level tests exist
  (`backend/storage/sso_providers_test.go`) and cover round-tripping the struct
  including its secret fields, so they will need to keep passing — but they prove
  nothing about the HTTP boundary. AC-13 is the new guard.
- **No stored data changes.** The secrets stay in the database; only what crosses
  the boundary changes. There should be no migration for the non-disclosure part
  of this work. If the plan proposes one, that is a signal that scope has grown.
- **Cross-organisation isolation.** Currently enforced by the domain check
  (§2.2). Protected by no test. AC-12 is the new guard.
- **Provider detection at login.** Detection reads the same rows
  (`auth/sso_detect.go:37`) and must be unaffected except where AC-7
  deliberately changes it.
- **The secrets must not become recoverable by another route.** Making them
  write-only means an operator who forgets a client secret cannot retrieve it and
  must re-enter it. That is the intended trade and must not be softened later with
  a "reveal" affordance. It has a consequence that the plan must handle: an update
  that changes only the provider's display name must not require re-supplying the
  secret (AC-4).
- **Anything reading the current PascalCase keys breaks.** I found no such reader:
  none in the frontend, none in the MCP server, none in e2e (§2.9), which is what
  U-22 predicted would make this question dissolve. But absence of a consumer in
  these two repos is not absence of a consumer — a script or saved request outside
  the tree would break silently. OQ-3.

---

## 7. Rollback

**No migration, so no data loss on the way back.** Nothing in this spec changes
stored data, and the secret columns keep their values throughout.

Reverting the change restores the leak, in full, for every organisation — so a
revert is not a neutral act and should be treated as reopening a critical finding
rather than as a rollback. If a defect is found after release, prefer fixing
forward.

Two things do not roll back cleanly:

1. **Secrets entered after release are unrecoverable through the API.** They are
   in the database, so nothing is lost; but an operator who relied on reading them
   back must go to the database. This is by design and gets worse, not better, as
   the follow-up work lands.
2. **The interim mitigation (AC-10) is the one piece with a fast, safe partial
   rollback**: it is an operator switch, so a deployment that finds it blocks
   legitimate setup turns it on without a deploy. That is exactly why the
   mitigation was chosen in this shape.

The behaviour changes that a consumer could notice — the removed secret
properties, the key casing, the removal-of-nothing outcome, the refusal of
configurations previously accepted — all revert with the code, because no state
depends on them.

---

## 8. Open questions

**OQ-1 — What makes someone an administrator of an organisation, and who is the
first one?** *Owner: human (product). Blocks the follow-up issue, not this spec.*
No candidate signal in the codebase is fit for purpose (§2.3), so the answer is a
decision, not a discovery. It has three parts, and the third is the hard one:
(a) what an administrator is permitted to do; (b) where that is recorded; (c) how
the first administrator of an existing organisation comes to be one. On the day
the gate ships, every deployment has zero administrators and SSO becomes
unconfigurable unless (c) has an answer. Candidates I can see, each with a
problem: *earliest-created member of the org* — derivable from existing timestamps
but not recorded as a grant, and in a domain-derived org that person may be an
arbitrary early signup (§2.4); *operator-configured list* — works without a
schema change and bootstraps cleanly, but does not scale past self-hosting;
*explicit grant with a migration* — the real answer, and it still needs a rule for
who is seeded. Resolve by a product decision; my recommendation is that the
follow-up spec cannot be written until (c) is answered, and that an
operator-configured list is the right bootstrap regardless of which model wins.

**OQ-2 — Should the unimplementable kind of identity provider be refused at
submission instead of at login?** *Owner: human (product), before the contract is
written.* Today it is accepted and stored, and rejected at login
(§2.5). Refusing it at submission is the honest behaviour and is what AC-6
assumes, but it removes a capability someone may be using to stage a
configuration ahead of the feature existing, and any already-stored row of that
kind becomes unupdatable if validation applies to updates as well as creates.
Resolve by deciding; if the decision is to keep accepting it, AC-6 is struck and
the contract must document the deferred rejection instead.

**OQ-3 — Is there a consumer of this surface outside the two repositories?**
*Owner: whoever operates the existing deployments.* The response shape change
breaks any reader of the current PascalCase keys. I established there is none in
the frontend, the MCP server or e2e (§2.9), but I cannot see operator scripts,
saved requests or anything that has ever called this by hand. Resolve by asking;
if the answer is uncertain, note that this surface has no UI and was found to have
no consumer at all, which makes the risk low but not zero. I could only establish
this by reading — no deployment logs were available to me.

**OQ-4 — What should happen to a configuration created for a subdomain?**
*Owner: human, as a new issue.* §2.8: creation accepts subdomains, listing does
not show them, and login uses them, so a configuration can be installed that the
organisation cannot see. This is a new finding, not in the register. It should be
filed on its own. It is raised here because it interacts with the follow-up
issue — an authorization gate stops new invisible rows but does not surface
existing ones — and because whoever writes the contract for this surface will have
to decide whether the described shape admits subdomains.

**OQ-5 — Will the audit register accept API-001 being split into a disclosure row
and an authorization row?** *Owner: whoever maintains
`docs/factory/api-audit.md`.* Required by the §0 split: as one row, API-001 cannot
be closed by PAC-22, and the touch-it-fix-it rule would then either block this
spec or be quietly broken. Resolve before the contract stage, since the contract
gate is what enforces it.

**OQ-6 — Who tells existing customers to rotate their identity-provider
secrets?** *Owner: human.* Out of scope for the code (§5) and cannot be dropped:
closing the leak does not un-expose a secret that has been readable by an
organisation's whole staff. Needs a named owner and a sequence, because rotation
breaks SSO login until Paceday is reconfigured, and reconfiguration needs the
capability that AC-10 switches off by default.

**OQ-7 — Should the read path also be gated in this spec if OQ-1 resolves
quickly?** *Owner: spec-challenger, to push back on.* I have assumed the split is
right because OQ-1 has no owner today. If it is answered before this reaches the
contract stage, the argument for splitting weakens considerably and this spec and
its follow-up should probably be merged back. I would rather be told that than
guess.

---

## Challenge — 2026-09-16

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
