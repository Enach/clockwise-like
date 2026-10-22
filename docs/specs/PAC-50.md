# Spec — PAC-50: SSO configuration must not be writable by every employee, nor for domains that are not the caller's

- **Linear**: https://linear.app/paceday/issue/PAC-50/sso-configuration-is-writable-by-any-employee-and-for-any-subdomain
- **Status**: v2.2 (2026-10-22) — v2.1 challenge answered; awaiting stage 2. Replaces the v2.1 body (2026-10-21) and the v1 body (2026-09-17, last at `d4ae0bf`); both Challenge sections are kept at the bottom, unaltered, and the `Revision v2.2` section before them maps every v2.1 finding to its answer.
- **Author**: spec-author (v2/v2.1 cloud session, 2026-10-21; v2.2 local session, 2026-10-22)
- **Absorbs**: PAC-46 (exact-domain rule).
- **Depends on**: PAC-45 / #172, merged (the OIDC-callback domain binding break-glass relies on, §4.5). **Migration ordering only** with PAC-23 and PAC-49 (§4.9); no dependency on PAC-23's activity-log changes remains (§4.6).
- **Inherited scope** (factory §7 rule 1; the stage-2 gate, item 3): the two operations this spec modifies carry `x-uncertain` U-21 and U-22 (`contracts/openapi/paths/auth.yaml:840-843` and `:901-904`; `docs/factory/api-audit.md:465-466`) and open audit rows API-060, API-066, API-067 and API-092 (`docs/factory/api-audit.md:294,300,301,326`). Disposition, one line each:
  - **U-21** (nothing beyond the three required fields is validated): determined at stage 2 by reading the handler and encoding what it does; this spec adds no field validation beyond §4.2 step 3 and does not change the request's field set.
  - **U-22** (response casing inferred, and it is the schema that leaks the secret): determined at stage 2 by reading the handler; where it is the secret in the response, the contract records the defect and links PAC-22 rather than changing the shape here (factory §7 rule 2). PAC-22's secret projection stays out of PAC-50; PAC-22 lands after PAC-50 and rebases onto it.
  - **API-060** (SAML accepted then unusable; empty OIDC fields stored): unchanged by this spec; the contract documents the behaviour and links the row (rule 2). Fixing it is a validation change outside this spec's problem.
  - **API-066** (`Enabled` write-once `true`): unchanged by this spec; documented and linked the same way.
  - **API-067** (DELETE of an absent domain answers 204): **fixed here** — AC-16c/AC-16d: deleting a domain with no row is 404, and the `{domain}` segment is compared and deleted lower-cased.
  - **API-092** (no Go test on the three admin operations): **closed by this spec's tests** — every AC on create, update and delete (AC-1, AC-2, AC-3, AC-4, AC-14, AC-16, AC-20) is a handler test that did not exist; the `oidcCallback` half of the row is already covered by `backend/api/handlers_sso_test.go:138-183`.

> **Landed from Linear on 2026-10-21** — document [PAC-50 spec v2.1 (2026-10-21) — SSO writes authorized by the org's own IdP admins](https://linear.app/paceday/document/pac-50-spec-v21-2026-10-21-sso-writes-authorized-by-the-orgs-own-idp-1861a1cadea6). This file is now the canonical copy. Linear's `<issue>`/`<pull-request>` markup was rewritten as markdown links. Edits while landing, per the document's own reality-check header and the §4.2 route note: the step-up routes are written as `POST /api/admin/sso/stepup` and `GET /api/admin/sso/stepup/callback` everywhere (the org is the caller's; there is no `{orgId}`), AC-16 says so, and the grant-table migration is **027** (025 is PAC-23's, 026 is PAC-49's). Nothing else was changed.

> ⚠️ **Reality check, 2026-10-21 16:10** — three facts learned after drafting: (1) [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email) **/ PR** [Enach/clockwise-like#172](https://linear.app/paceday/review/pac-45-fixauth-bind-the-oidc-asserted-email-to-the-providers-domain-ac3dd71bc31d) **is merged** (since 2026-09-17; erratum in [Enach/clockwise-like#176](https://linear.app/paceday/review/docspac-50pac-22-spec-challenge-and-an-erratum-pac-45-is-merged-f251b598b6a7)), so §1's "with [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email) unmerged" branch is history and AC-19 reduces to a one-line test that the binding exists. (2) [PAC-24](https://linear.app/paceday/issue/PAC-24/switch-apisettings-and-the-daily-recap-handlers-to-per-user-settings) **/** [Enach/clockwise-like#204](https://linear.app/paceday/review/featsettings-every-user-has-their-own-settings-pac-24-89e2813268be) **merged 2026-10-15**, so `PATCH /api/settings` replaced `PUT`, and migration 024 is taken — the grant table migration number in §4.1/§4.2/OQ-6/AC-20 is "next free", not necessarily 025 — **landed as 027** (025 is PAC-23's, 026 is PAC-49's). (3) No implementation PR for [PAC-50](https://linear.app/paceday/issue/PAC-50/sso-configuration-is-writable-by-any-employee-and-for-any-subdomain) exists in the repo; the issue's Done status dates from the docs-only PR [Enach/clockwise-like#176](https://linear.app/paceday/review/docspac-50pac-22-spec-challenge-and-an-erratum-pac-45-is-merged-f251b598b6a7). The design itself is unaffected.

> **Note on the two blocks above (v2.2).** They are kept as history. Two statements in them are superseded by this body: the step-up *start* is now `GET`, not `POST` (§4.3, decided 2026-10-22), and AC-19 is withdrawn rather than reduced (§4.5, §5).

## 1. Problem

Any organization member can create, edit or delete the organization's SSO provider. Membership is self-service: `AssociateUserWithOrg` (`backend/storage/orgs.go:42-52`, called from the Google callback at `backend/api/handlers_auth.go:76`, the Microsoft callback at `handlers_microsoft_auth.go:76` and the OIDC callback at `handlers_sso.go:191`) derives the org from the signup email's domain and creates the org if absent. There is no admin role anywhere (no role column on `users`, no owner on `organizations`, `team_members.role` is self-granted, `user_profiles.is_manager` is auto-detected).

So "may rewrite the IdP that logs everyone in" currently reduces to "signed up with a non-generic email domain". PAC-45 is merged (`backend/domain/domain.go:33-40`, applied in the OIDC callback at `backend/api/handlers_sso.go:177-184`), so a rewritten provider can no longer assert an email outside its own domain; what remains is a denial-of-login and configuration-tampering path for the whole organisation, open to every member. It must close before the first real user signs up (production has zero users as of 2026-10-21).

### 1.1 Current behaviour of the three write operations (verified 2026-10-22)

- Routes: `POST /api/admin/sso`, `GET /api/admin/sso`, `DELETE /api/admin/sso/{domain}`, all inside the session-protected group (`backend/api/routes.go:172-176`). There is no update operation: `POST` upserts on `domain` (`backend/storage/sso_providers.go:56-80`, `ON CONFLICT (domain)`), so "update" is a second `POST`.
- **The org is already the caller's, never the body's.** Both write handlers load the caller, answer 403 `org membership required` when the caller has no org (`backend/api/handlers_sso.go:201-206` and `:291-296`), and resolve the org from the caller's own org id (`:233`, `:298`). The body's `domain` is then checked against that org's domain (`:238`, `:303`) — this is the suffix match of §1.2. Nothing checks who the caller *is* beyond membership.
- The request body is decoded without rejecting unknown fields (`handlers_sso.go:219`), while the contract declares `additionalProperties: false` (`contracts/openapi/paths/auth.yaml:837`). An `org_id` in the body is silently dropped today.
- `DELETE` deletes by the raw path segment (`sso_providers.go:108-112`) with no rows-affected check, so a domain with no row — or a differently-cased spelling of an existing one — answers 204 having deleted nothing (API-067).
- Domain lookups at sign-in are keyed by `domain` alone (`sso_providers.go:47-54`); `sso_providers` has no org column (`backend/storage/migrations/008_sso_providers.up.sql:1-15`), and the only org linkage is the listing's join on `organizations.domain` (`sso_providers.go:89`). Both `sso_providers.domain` and `organizations.domain` are raw `UNIQUE` text (`008_sso_providers.up.sql:3`, `007_organizations.up.sql:4`); the latter is safe today only because org association lower-cases through `ExtractDomain` (`orgs.go:43`, `domain.go:21`); the former is written with whatever casing the caller sent (`handlers_sso.go:244`).
- Error bodies from these handlers are `{"error": "<text>"}` (`backend/api/handlers_settings.go:371-375`); no machine-readable code exists on any of them.

### 1.2 Secondary defect (ex-PAC-46)

`DomainMatchesOrg` is a suffix match (`backend/domain/domain.go:45-49`) while every other domain comparison is exact (`GetSSOProviderByDomain`, `ListSSOProvidersByOrg`, `UpsertSSOProvider`, `DetectAuthProvider`, `AssociateUserWithOrg`, `EmailBelongsToDomain`). `evil-acme.com` does not suffix-match `acme.com` (the check requires a leading dot), but `eu.acme.com` does: a member of `acme.com` can create a provider row for a subdomain nobody in that org administers, and sign-in detection for that subdomain will route to it.

## 2. Decision (human, 2026-10-21)

Authorization for SSO writes is **derived from the organization's own identity provider**, not from anything Paceday stores:

> An SSO write is allowed if a fresh re-authentication with Google or Microsoft, completed within the last few minutes, proves the caller is an administrator of the organization that owns that verified domain/tenant. A deployment-level admin list (environment variable) is the break-glass fallback.

Rationale: the org's real admins already decide who administers identity; Paceday should ask them rather than invent its own role system. The fallback exists for orgs on neither Google Workspace nor Microsoft 365, and for recovery.

## 3. Scope

In scope:

1. A write-authorization gate on the three SSO write operations (create, update — a second create for the same domain — and delete).
2. An "admin step-up" flow: a short-lived, purpose-bound proof that the caller is an IdP admin for the org's domain/tenant, started and finished by two new operations behind the session.
3. The env-var break-glass list.
4. Exact-domain matching for the provider domain (and any other suffix match found while in there).
5. A record of every SSO write and every step-up outcome, kept for operators (§4.6).
6. The two inherited items fixed here: delete of an absent domain is 404, and the delete segment is handled lower-cased (API-067); tests on the three admin operations (API-092).

**The admin UI is out of scope for PAC-50.** No SSO admin page exists in the web app today: `smart-calendar-flow/src/pages/` has no SSO page, and the only references to `/api/admin/sso` under `src/` are the generated types. No frontend code path can therefore produce the 403 this spec introduces. The one web-side requirement this spec carries is in §4.3 ("What the web client must do") and AC-22; it is testable at the client's error-mapping layer so that a future SSO page inherits it. **Follow-up issue to file** (not filed here): *SSO admin page in the web app* — where the save lives, what the step-up prompt looks like, what the 403 renders as. The brief's frontend-plan paragraph "SSO page step-up redirect on 403" reduces to that one requirement.

Out of scope (own issues):

* Using `hd` / `tid` to decide **org membership at signup** instead of the email domain. Larger change, touches org association; filed separately as "verified org membership". PAC-50 only uses the claims to authorize writes.
* Revoking live sessions when a provider changes (no server-side session store — separate problem).
* SSO **read** authorization and secret projection — that is PAC-22, which lands after PAC-50 and rebases.
* Re-pinning an organisation's Microsoft tenant after a tenant migration (§4.3, NB5): an operator task done directly in the database; no API does it.
* An operator-facing view of the SSO event table (§4.6): operators read the table directly for now.

## 4. Design

A note on level: factory §2 says a spec names no files, packages or signatures, and §4 below has been stripped of them (v2.1's named function signatures, SQL, package and interface names are gone; the behaviour each one carried is kept). §4 still names the routes, error codes, the break-glass variable and the schema objects, because the orchestrator fixed those on 2026-10-22 and the contract challenger needs them to check AC-18. That is a deliberate exception and it is said here so nobody mistakes it for the rule.

### 4.1 Vocabulary

* **Org domain**: the organisation's stored domain, exact string, lower-cased.
* **Step-up**: a fresh OAuth/OIDC round-trip with Google or Microsoft, started by Paceday specifically for an SSO write, whose result is checked for admin status and then stored as a short-lived grant.
* **Grant**: a server-side row (schema in §4.8) bound to `(user, org, provider)`, with an issue time, an expiry and a consumed-at mark. **Lifetime: 10 minutes.** Not a claim in the session JWT — the session JWT is a 7-day bearer with no revocation; a grant must not inherit that lifetime. **A write never names its grant** (decided 2026-10-22): the write request carries nothing new; the server finds and consumes the caller's grant itself (§4.2 step 5).
* **Break-glass admin**: an email listed in `PACEDAY_ADMIN_EMAILS` (comma-separated, compared lower-cased and exactly against the session's email claim).
* **Clock**: every grant-expiry comparison goes through the injectable clock that PAC-49 threads through the cron (same seam, same release), so tests move time without sleeping. The session JWT's issue time is stamped from the wall clock, not from that seam (`backend/auth/jwt.go:23`), so the break-glass freshness check (§4.5) is tested by minting a token with a chosen issue time (AC-14).
* **IdP endpoints**: issuer, JWKS/discovery URL, token URL, the Google Directory base and the Microsoft Graph base are **addresses the deployment can redirect to a stand-in** — but only under the test-mode declaration PAC-43 established for the calendar address (`PACEDAY_ALLOW_STANDINS`, `backend/config/endpoints.go:14-20`); without that declaration the real endpoints are used and any redirection is refused at startup, exactly as the calendar one is. This is new work, not a description: today Google uses the library's fixed endpoint (`backend/auth/google_oauth.go:20`), Microsoft a fixed pair (`backend/auth/microsoft_oauth.go:14-17`), Graph is a constant in three places (`backend/calendar/outlook_client.go:15`, `backend/conference/teams.go:13`, `backend/api/handlers_microsoft_auth.go:91`), and no Directory or Graph `/organization` client exists at all; the e2e stand-in (`e2e/fake-google/`) serves calendar only. Without the redirection nothing in §5 is testable offline, which is why it is in scope.

### 4.2 Gate

Every SSO write, in this order, before touching the provider store:

1. **Target org = the caller's org.** This is current behaviour (§1.1) and stays: nothing in the body or the path chooses the org. For `DELETE`, the `{domain}` segment is a target, not an authorization: it is lower-cased, must equal the org domain (step 4), and the row deleted is the lower-cased one.
2. **Membership.** The caller must belong to an org; 403 otherwise. Current behaviour (`handlers_sso.go:201-206`, `:291-296`), kept, and it runs before anything else — including before the break-glass check — so a listed email with no org is refused here like anyone else (v1 Finding 2, AC-1b). This 403 carries no `stepup_url`: there is no org to step up for.
3. **Body shape (create only).** The three required fields and the provider-type enum, as today; **and a body carrying any field the contract does not declare is rejected with 400** (new; matches the contract's `additionalProperties: false`). A body `org_id` is therefore a 400, never ignored (AC-16a).
4. **Provider domain = org domain**, exact and case-insensitive. Otherwise **400 `provider_domain_mismatch`**. Without this a real admin of `acme.com` could upsert a row whose domain is `other.com` and hijack that domain's login, because sign-in detection is keyed by domain alone (§1.1). This replaces today's suffix check and its 403. (v2.1's "additionally scoped by `org_id` at login" is withdrawn: the provider table has no org column and this spec does not add one — the exact-domain pin is the whole control.)
5. **Authorization.** The caller must satisfy one of:
   a. **A live grant, consumed atomically.** The server selects the caller's newest live grant for `(caller, caller's org)` and consumes it in **one statement that selects and updates**: it marks consumed-at, under the predicate "not yet consumed, not yet expired, user and org match", and returns the grant's id. Zero rows returned → no grant. Two concurrent writes by the same caller → exactly one consumes (AC-3b). The consume is the last step before the write: every 400 above happens before it and leaves the grant live (AC-3c); a storage failure after it leaves the grant consumed, so a failed write means a new step-up (AC-3d).
   b. **Break-glass** (§4.5), which additionally requires a fresh session; a listed caller with a stale session gets **403 `sso_write_requires_fresh_login`**.
   Otherwise **403 `sso_write_requires_admin_stepup`** with a `stepup_url` the client can navigate to (§4.3). Deliberately not a 401: the session is valid, the privilege is missing. Both 403 bodies carry the code in the body's `error` field, because the web client renders a 403 with no `error` as a generic "no permission" message (`smart-calendar-flow/src/api/client.ts:335`) and the prompt would be swallowed.
6. **Write, then record (§4.6).** The provider domain is stored lower-cased. A delete that removes zero rows answers **404** and records nothing (API-067).

One grant authorizes one write. A client that saves steps up once per save, which is the intended UX ("confirm with your Google/Microsoft admin account").

### 4.3 Step-up flow

Decided 2026-10-22. The step-up is two operations behind the session, shaped like the existing sign-in starts (`backend/api/handlers_auth.go:24-39`, `handlers_sso.go:117-129`) and like the Zoom connect that is already behind the session (`routes.go:38-39`):

**Start — `GET /api/admin/sso/stepup?provider=google|microsoft`** (behind the session; the org is the caller's, never a parameter). It sets a purpose-bound state cookie and answers **302 to the IdP**. A missing or unknown `provider` is **400**. No session → 401, like every operation in the group.

The **`stepup_url`** carried by the 403 of §4.2 is this start's URL for the caller, **without the provider**; the client appends `provider=google` or `provider=microsoft` after asking the user which account administers their organisation. Following the precedent of the sign-in detection response, whose `redirect_url` is a relative path (`contracts/openapi/paths/auth.yaml:773`), it is the relative path `/api/admin/sso/stepup`; the contract stage confirms or changes that.

The state cookie is `HttpOnly`, `SameSite=Lax`, expires in 10 minutes, and is bound to the caller, the org, the chosen provider and a nonce so that a callback cannot be replayed by, or for, anyone else. **`Secure` is a deployment property**: set in production (which is HTTPS), not in the plain-HTTP compose/e2e stack. How the server learns which it is in is the plan's business — note that the Go process never observes TLS itself in production, because nginx terminates it and forwards no `X-Forwarded-Proto` (`nginx/nginx.conf:5-10`, `docker-compose.prod.yml:113-125`), and that the existing session cookie sets no `Secure` at all (`handlers_auth.go:154-161`).

Which IdP: the caller picks; the **result** is what is checked against the org's domain, so a wrong pick simply fails. No org-level "provider type" needs to exist before the first write.

**Callback — `GET /api/admin/sso/stepup/callback`**, **behind the session** (the SPA holds the session in a cookie, so the top-level navigation back from the IdP carries it; if it ever does not, the callback answers 401 rather than proceeding as nobody — the context helper yields a nil id, not an error, when no session is present, `backend/auth/context.go:19-24`). Then:

0. State cookie present, valid, unexpired, and bound to **the session's user**; a cookie bound to a different user → denied, `state_mismatch`. Email equality (below) is not a substitute for this, because Entra's `email`/`preferred_username` are attacker-settable in the attacker's own tenant.

**Google**

1. Exchange code; verify the ID token (signature, audience, issuer, expiry).
2. Require `email_verified == true` and `hd` **claim == org domain** (exact). `hd` is only present for Google Workspace / Cloud Identity accounts and is the documented way to establish hosted-domain membership; the email's domain is explicitly insufficient ([developers.google.com/identity/openid-connect/reference](http://developers.google.com/identity/openid-connect/reference)). Absent → `no_hd`; mismatched → `hd_mismatch`.
3. Require the ID token's `email` == the caller's session email (the person stepping up is the person logged in) → else `email_mismatch`.
4. Ask the Google Directory API whether that user is an administrator of the domain (read-only directory-user scope, requested during this step-up only). Require `isAdmin == true` **or** `isDelegatedAdmin == true` (decided 2026-10-21, OQ-1: delegated admins qualify). A non-admin typically cannot call this endpoint at all; any error is "not admin" (`not_admin`), never "retry".
5. Issue grant.

**Microsoft**

1. Exchange code; verify the ID token (signature, audience, issuer, expiry).
2. **Bind the tenant to the org domain by evidence, never by first use.** Ask Graph for the organisation's id and verified domains with the step-up token (delegated `Organization.Read.All`; if `User.Read` turns out to return enough via `/organization` for some tenants, prefer it — OQ-2). Require the returned id == the token's `tid` **and** the verified-domain list to contain the org domain (exact, case-insensitive). This is Microsoft's equivalent of Google's `hd`: a tenant only lists a domain as verified after DNS proof. On success, **pin** the org's Microsoft tenant id (idempotent). A `tid` that does not verify the domain → denied, `tid_mismatch`. A different `tid` later, with the same verified domain, is a tenant migration or a takeover: **denied, `tid_changed`, and the pin stays** (decided 2026-10-22, NB5). Break-glass does not re-pin and is never part of a step-up; after a genuine tenant migration an operator re-pins the tenant id directly in the database, which is an operator task outside this spec. An attacker who is Global Admin of their *own* tenant cannot pass step 2, because their tenant does not have `acme.com` verified.
3. Require `preferred_username`/`email` == caller's session email, same as Google (defence in depth; step 0 is the real identity binding) → else `email_mismatch`.
4. Admin check, in order of preference:
   a. `wids` claim in the ID token (emitted when the app registration's group-claims setting includes "Directory roles"). Require it to contain at least one of the **qualifying role template IDs** (§4.4); otherwise `not_admin`.
   b. If `wids` is absent, ask Graph for the user's directory-role memberships (delegated `RoleManagement.Read.Directory`, least-privileged per Graph docs) and match `roleTemplateId` the same way.
5. Issue grant.

Both: the step-up tokens (access/refresh) are discarded after the check. Nothing from the step-up is written to the user's settings or to any token store (AC-17). The step-up uses its own OAuth client registration scopes; it must not widen the calendar consent.

**Terminal redirect.** The callback ends by redirecting the browser to the web app's settings page (`/settings` in the web app today, built from the configured frontend URL, not a literal — the two integration callbacks that hard-code `/app/settings` are the counter-example, API-084) with `?stepup=granted`, or `?stepup=denied&reason=<reason>` where `reason` is the §4.6 enum. The same outcome stands in the SSO event table (§4.6). A denied step-up issues no grant and, like a granted one, does not touch the session.

**What the web client must do** — the only web-side requirement of this spec: when the web client receives a 403 whose body carries `stepup_url`, it navigates the browser to that URL (with the provider appended once the user has chosen). This lives at the client's error-mapping layer, where today a 403 is turned into text (`smart-calendar-flow/src/api/client.ts:325-349`), so a future SSO page inherits it without knowing about step-up. Nothing else on the web side changes; the session logic treats 401/403 as "session gone" only on the `me` call (`src/contexts/AuthContext.tsx:58`), so this 403 does not log the user out.

### 4.4 Qualifying Entra roles (template IDs are immutable)

| Role | Template ID |
| -- | -- |
| Global Administrator | `62e90394-69f5-4237-9190-012177145e10` |
| Privileged Role Administrator | `e8611ab8-c189-46e8-94e1-60213ab1f814` |
| Application Administrator | `9b895d92-2cd3-44c7-9d02-a6ac2d5ea5c3` |
| Cloud Application Administrator | `158c047a-c907-4556-b7ef-446551a6b5f7` |

All four IDs verified 2026-10-21 against [learn.microsoft.com/entra/identity/role-based-access-control/permissions-reference](http://learn.microsoft.com/entra/identity/role-based-access-control/permissions-reference). `b79fbf4d-3ef9-4689-8143-76b194e85509` is the default "ordinary member" value present in every non-guest token and must **never** qualify. The list is fixed in the build, not configuration.

### 4.5 Break-glass

`PACEDAY_ADMIN_EMAILS` is read once at startup. Empty → feature off (this is fine: §4.3 lets a Google Workspace or Entra admin create an org's *first* provider without break-glass, since `hd` / verified domains bind the domain without any pre-existing provider row). Matching is exact on the lower-cased session email claim.

A break-glass write additionally requires a **fresh session**: the session's issue time must be within the last 10 minutes; otherwise 403 `sso_write_requires_fresh_login` (a client sends the user through login again). This keeps the §4.1 rule that no SSO-write privilege rides on a 7-day unrevocable bearer. The session JWT already carries an issue time (`backend/auth/jwt.go:23`; OQ-7 closed).

A break-glass write is recorded with `via: "breakglass"` so it is visible to operators that the IdP check was bypassed.

**Correctness dependency on PAC-45 (merged).** Break-glass trusts the session email, which is only trustworthy because the OIDC callback binds the asserted email to the provider's domain (`backend/api/handlers_sso.go:177-184`, PAC-45, merged 2026-09-17 as `5c5f34f`). That binding is already protected by `TestOIDCCallback_RejectsEmailOutsideProviderDomain` and `TestOIDCCallback_RejectsSubdomainEmail` (`backend/api/handlers_sso_test.go:138-170`); this spec adds no test for it (v2.1's AC-19 is withdrawn — a test that a merged function exists cannot fail) and must not weaken those two.

### 4.6 Recording (decided 2026-10-22)

The two SSO records do **not** go into the activity log. PAC-23's spec (§3.2, §3.5, OQ-7) and its contract revision 4 define that log as a per-user activity feed that holds no authentication or configuration events and is readable only by its subject; under it an SSO write would be visible only to the admin who made it and a denied step-up only to the attacker, and the PAC-23 contract challenger confirmed on 2026-10-22 that no revision of that contract can admit these two kinds. So:

* Migration 027 also creates a **dedicated SSO event table** (§4.8): per row an id, the acting user's id, the org's id, a `kind`, `details` as JSON, and a creation time. It is written with an explicit subject — never derived from a request context, because the context helper yields a nil id rather than an error when there is none (`backend/auth/context.go:19-24`).
* **It is not exposed by any API operation in this spec.** Operators read it directly; an operator view is a separate issue (§3). This answers "visible to whom": to operators, not to users.
* Two record kinds, details marshalled as JSON (never concatenated — the four concatenating activity-log writers are a known defect; do not add a fifth):
  * `sso_stepup` — details `{org_id, provider, outcome: "granted"|"denied", reason}`; `reason` is a short enum (`no_hd`, `hd_mismatch`, `not_admin`, `tid_mismatch`, `tid_changed`, `email_mismatch`, `state_mismatch`, `token_invalid`), never the raw token. The same enum is what the callback's `?reason=` carries (§4.3).
  * `sso_provider_written` — details `{org_id, provider_id, op: "create"|"update"|"delete", via: "stepup"|"breakglass"}`.

No dependency on PAC-23's activity-log changes remains. The only PAC-23 coupling is migration ordering (§4.9).

### 4.7 Exact-domain rule

The provider-domain comparison becomes an exact, case-insensitive comparison against the org domain; any other suffix comparison on a domain found while in there is aligned. No subdomain allowance — if a customer needs `eu.acme.com`, that is a second org or a later feature.

### 4.8 Schema (migration 027, the whole of it, listed once)

Named here because the plan's `.up.sql`/`.down.sql` pair is written from this list and the challenger asked for it in one place; everything is reversible (§7).

1. **Grant table** — `(id, user_id, org_id, provider, issued_at, expires_at, consumed_at NULL)`; `user_id` and `org_id` reference their tables; an index that serves "newest live grant for (user, org)".
2. **SSO event table** — `(id, user_id, org_id, kind, details JSON, created_at)` (§4.6).
3. **`sso_providers`**: existing `domain` values lower-cased (backfill), and the raw `UNIQUE (domain)` replaced by uniqueness on `lower(domain)`, so a second casing of the same domain can never be inserted (AC-20). `organizations.domain` is also raw-`UNIQUE` (`007_organizations.up.sql:4`) and is left as is: it is only ever written lower-cased (`orgs.go:43`, `domain.go:21`); if a later feature writes it from elsewhere, it needs the same treatment.
4. **`organizations.ms_tenant_id`**, nullable text: the Microsoft tenant pin of §4.3. NULL until the first successful Microsoft step-up.

### 4.9 Sequencing (decided 2026-10-22)

* Migrations are applied by golang-migrate's `Up()` (`backend/storage/db.go:40-46`), which walks upward from the database's current version only. A database that has applied 027 will never apply a 025 or 026 that lands afterwards. **025 (PAC-23), 026 (PAC-49) and 027 (PAC-50) are applied in that order on every environment**, so PAC-50's stage-4 PR merges **after** PAC-23's and PAC-49's. The highest migration on `main` today is 024 (`backend/storage/migrations/`).
* AC-12 is implemented in PAC-50 against PAC-50's own table (§4.6) and is never skipped; it has no PAC-23 dependency. The ordering above exists for the migration counter alone.
* PAC-22 lands after PAC-50 and rebases: its secret projection changes the response shape of the same operations, and AC-18 here is written against today's response.

## 5. Acceptance criteria

Each is Given/When/Then and names the layer it is proven at: `[unit]` is a handler or storage test against the test database, `[contract]` a check on the assembled OpenAPI bundle, `[e2e]` the compose stack with stubbed IdPs. "Write" means any of create (first `POST`), update (a second `POST` for the same domain) or delete; "no grant" means no live grant for the caller and the caller's org. Error codes are the body's `error` field.

**Gate**

* **AC-1a.** Given a signed-in org member with no grant and whose email is not on the break-glass list, when they create, update or delete the org's provider, then each request is answered 403 with error `sso_write_requires_admin_stepup` and a `stepup_url`, and the provider table is unchanged (row count, and the row's last-modified time where a row exists). `[unit]`
* **AC-1b.** Given a signed-in user who belongs to no organisation, whether or not their email is on the break-glass list, when they attempt any write, then the response is the same 403 for both (the membership refusal), it carries no `stepup_url`, and no grant, state or event row is created. `[unit]`
* **AC-2a.** Given a live grant recorded for user U and org A, when U — whose org association is now B — writes, then 403 `sso_write_requires_admin_stepup`, and the grant for (U, A) is still unconsumed. `[unit]`
* **AC-2b.** Given a live grant recorded for user V and org A, when U, also a member of A, writes, then 403 `sso_write_requires_admin_stepup`, and V's grant is still unconsumed. `[unit]`
* **AC-3a.** Given U holds exactly one live grant, when U writes twice in sequence, then the first is 2xx and the second is 403 `sso_write_requires_admin_stepup`. `[unit]`
* **AC-3b.** Given U holds exactly one live grant, when two writes by U are released simultaneously (a barrier in the test), then exactly one is 2xx and one is 403, and the grant's consumed-at is set exactly once. `[unit]`
* **AC-3c.** Given U holds one live grant, when U sends a create whose provider domain is not the org's (400 `provider_domain_mismatch`), then the grant is still live and the next valid write by U is 2xx. `[unit]`
* **AC-3d.** Given U holds one live grant, when U's write passes the gate and the test arranges for the provider store to fail the write, then the response is 5xx, the grant is consumed, and U's retry is 403 `sso_write_requires_admin_stepup`. `[unit]`
* **AC-4a.** Given a grant issued to U at time T, when U writes with the clock at T + 9 minutes, then 2xx. `[unit]`
* **AC-4b.** Given a grant issued to U at time T, when U writes with the clock at T + 11 minutes, then 403 `sso_write_requires_admin_stepup` and the grant is still unconsumed. `[unit]`

**Step-up, Google**

* **AC-5.** Given a Google step-up whose ID token carries no `hd` claim, when the callback completes, then no grant is issued, the browser is redirected to the settings page with `stepup=denied&reason=no_hd`, and one `sso_stepup` event with `outcome: denied, reason: no_hd` exists. `[unit]`
* **AC-6.** Given a Google step-up whose `hd` differs from the org domain, when the callback completes, then denied, `hd_mismatch` (redirect and event as in AC-5). `[unit]`
* **AC-7.** Given a Google step-up with `hd` equal to the org domain, when the directory answer is `isAdmin: false` and `isDelegatedAdmin: false`, or the directory call errors, then denied, `not_admin`. `[unit]`
* **AC-8a.** Given a Google step-up with `hd` equal to the org domain and `isAdmin: true`, when the callback completes, then a grant exists for (caller, org), the browser is redirected with `stepup=granted`, and the caller's next write is 2xx. `[unit]`
* **AC-8b.** Given the same with `isAdmin: false, isDelegatedAdmin: true`, when the callback completes, then the outcome is identical to AC-8a. `[unit]`

**Step-up, Microsoft**

* **AC-9a.** Given a Microsoft step-up whose organisation answer does not list the org domain among its verified domains, when the callback completes, then denied, `tid_mismatch`, and the org's Microsoft tenant pin is still NULL. `[unit]`
* **AC-9b.** Given the same answer *with* the org domain verified and an admin role present, when the callback completes, then the org's tenant pin equals the token's `tid` and a grant exists. `[unit]`
* **AC-9c.** Given an org already pinned to tenant T1, when a step-up from tenant T2 whose organisation answer also lists the org domain completes, then denied, `tid_changed`, the pin is still T1 and no grant exists. `[unit]`
* **AC-10.** Given a Microsoft step-up whose `wids` contains only `b79fbf4d-3ef9-4689-8143-76b194e85509`, when the callback completes, then denied, `not_admin`. `[unit]`
* **AC-11a.** Given a Microsoft step-up whose `wids` contains the Global Administrator template id, when the callback completes, then a grant exists. `[unit]`
* **AC-11b.** Given a Microsoft step-up with no `wids` claim and a directory-role answer containing the Global Administrator template id, when the callback completes, then a grant exists. `[unit]`

**Recording and identity binding**

* **AC-12.** Given the scenarios of AC-5 to AC-11 and the writes of AC-8 and AC-11, when each runs, then the SSO event table holds exactly one row per step-up outcome and one per write, each attributed to the caller's user id, each with `details` that parse as JSON into the §4.6 shape for its kind. `[unit]`
* **AC-13a.** Given a step-up whose ID token email differs from the session email while every other check passes, when the callback completes, then denied, `email_mismatch`, no grant. `[unit]`
* **AC-13b.** Given a callback whose state cookie is bound to a different user than the session, when it completes, then denied, `state_mismatch`, no grant. `[unit]`
* **AC-13c.** Given a callback request with no session, when it is handled, then 401, no event row and no grant. `[unit]`

**Break-glass**

* **AC-14a.** Given `PACEDAY_ADMIN_EMAILS=alice@acme.com` and alice, a member of her org, with a session issued 5 minutes ago (token minted with that issue time) and no grant, when alice writes, then 2xx and the write's event has `via: breakglass`. `[unit]`
* **AC-14b.** Given the same with a session issued 11 minutes ago, when alice writes, then 403 `sso_write_requires_fresh_login`, nothing written. `[unit]`
* **AC-14c.** Given `PACEDAY_ADMIN_EMAILS=ALICE@acme.com`, when alice (fresh session) writes, then 2xx — the list is matched lower-cased. `[unit]`
* **AC-14d.** Given `PACEDAY_ADMIN_EMAILS` unset, when alice (fresh session, no grant) writes, then 403 `sso_write_requires_admin_stepup`, as in AC-1a. `[unit]`

**Exact domain and request confinement**

* **AC-15.** Given an org domain `acme.com`, when the provider-domain comparison is asked about `evil-acme.com`, `eu.acme.com` and `ACME.com`, then it answers false, false and true. `[unit]`
* **AC-16a.** Given an authorised caller, when the create body carries a field the contract does not declare (for instance `org_id`), then 400 and no row is written. `[unit]`
* **AC-16b.** Given an authorised caller, when the create body's provider domain is not the org domain, then 400 `provider_domain_mismatch` and no row is written. `[unit]`
* **AC-16c.** Given an authorised caller and no provider row for the org domain, when the caller deletes it, then 404 and no `sso_provider_written` event is recorded. `[unit]`
* **AC-16d.** Given an authorised caller and a provider row `acme.com`, when the caller deletes `/api/admin/sso/ACME.com`, then 204 and the row is gone. `[unit]`
* **AC-17.** Given a user whose settings row (calendar provider, Microsoft token column) and OAuth-token row are captured before, when a fully successful Google step-up and a fully successful Microsoft step-up complete for that user, then those rows are identical to the capture. `[unit]`

**Contract**

* **AC-18.** Given the assembled OpenAPI bundle, when it is read, then it documents: on the create and delete operations, 403 with error `sso_write_requires_admin_stepup` and the `stepup_url` field, and 403 with error `sso_write_requires_fresh_login`; on create, 400 `provider_domain_mismatch` and 400 for an undeclared body field; on delete, 404 for an absent domain; the start operation `GET /api/admin/sso/stepup` with its `provider` parameter, its 302 and its 400 for a missing or unknown provider; and the callback operation `GET /api/admin/sso/stepup/callback` with its 302 to the settings page carrying `stepup=granted` or `stepup=denied&reason=<enum>`, and its 401. A test that reads the bundle for these fails today. `[contract]`
* **AC-19.** Withdrawn in v2.2 (PAC-45 is merged; see §4.5 for the tests that already protect the binding).

**Storage invariant**

* **AC-20a.** Given migration 027 applied to a database holding a provider row whose domain is `ACME.com`, when the migration has run, then that row's domain reads `acme.com`, and an attempt to insert a second row `Acme.com` is refused by the database. `[unit]`
* **AC-20b.** Given a provider row `acme.com`, when an authorised caller creates with domain `ACME.com`, then the row count is still 1 and that row's last-modified time has advanced. `[unit]`

**Start operation and the web client**

* **AC-21a.** Given a signed-in org member, when they request the step-up start with `provider=google` (and again with `provider=microsoft`), then 302 to the respective IdP and a state cookie bound to that caller. `[unit]`
* **AC-21b.** Given a signed-in org member, when they request the start with no `provider`, or with `provider=okta`, then 400 and no state cookie. `[unit]`
* **AC-22.** Given the web client's error mapping, when it receives a 403 whose body carries `stepup_url`, then it navigates the browser to that URL; a 403 without `stepup_url` renders as it does today. `[unit — web]`

**End to end**

* **AC-23.** Given the compose stack with the IdP stand-ins declared (§4.1) and a signed-in org member with no grant, when the member `POST`s a provider, then 403 with `stepup_url`; when the member follows the start for the stubbed provider and the stub's callback completes as an admin of the org domain, then the redirect carries `stepup=granted`; and when the member repeats the `POST`, then 2xx and the row exists. This is the API-level journey; there is no UI leg (§3). `[e2e]`

All unit ACs run against stand-ins for Google and Microsoft that sign ID tokens with a test key served from the stand-in's JWKS address, which is possible only because the IdP addresses can be redirected under the test-mode declaration (§4.1).

## 6. Open questions for the challenger / human

* **OQ-1** Closed 2026-10-21 (Nicolas): `isDelegatedAdmin` **qualifies**, same as `isAdmin`. AC-7/AC-8 updated.
* **OQ-2** Closed in v2.1: tenant is bound by verified-domain evidence (§4.3 step 2), never pinned on first use. Remaining sub-question: is `Organization.Read.All` really required to read verified domains, or does `User.Read` suffice? Graph docs list `Organization.Read.All` as least-privileged for the full resource; the implementer tests the narrower scope first.
* **OQ-3** Closed in v2.1: caller picks the provider; the result is checked against the org domain, so no pre-existing provider row and no break-glass are needed for the first create.
* **OQ-4** Closed 2026-10-21: all four §4.4 template IDs verified.
* **OQ-5** Closed 2026-10-21 (v1 findings recovered from the Linear comment of 2026-09-17):
  1. *"AC-9's uniqueness claim can be violated by pre-existing differently-cased* `sso_providers.domain` *rows."* With zero users there are no such rows, but the migration must still make it impossible: §4.8 item 3 lower-cases existing values and makes uniqueness case-insensitive; writes lower-case. AC-20.
  2. *"AC-1 doesn't state the outcome for a designated-but-orgless principal."* Membership (§4.2 step 2) is checked before the break-glass branch, so a listed email with no org gets the same refusal as anyone without an org; no third error path exists. AC-1b.
* **OQ-6** Closed in v2.1: grant is a table — needed for the atomic consume in §4.2. v2.2: the write carries no grant reference; the server selects the caller's newest live grant.
* **OQ-7** Closed 2026-10-22: the session JWT carries an issue time (`backend/auth/jwt.go:23`), stamped from the wall clock; AC-14's setup mints a token with a chosen issue time.
* **OQ-8** (new, for the plan) The `stepup_url` is written as a relative path by precedent (§4.3); if the contract stage prefers an absolute URL, AC-18 and AC-22 are unaffected in substance.

## 7. Not doing (recorded so nobody re-proposes it)

* "First user of an org becomes owner" — rejected: domain squatting.
* "DNS TXT verification" — rejected for now: real work, not needed while Workspace/Entra admins can vouch directly; revisit if an Okta-only customer appears.
* "No API writes, config-file only" — rejected: break-glass covers recovery without giving up the UI.
* "Carry the grant id in the write request" — rejected 2026-10-22 (B1): the server already knows the caller and the org; a client-supplied id adds a field to two contracts and buys nothing the atomic select-and-consume does not.
* "Put the SSO records in the activity log" — rejected 2026-10-22: that log is a per-user feed readable by its subject (PAC-23), which would show a denied step-up to the attacker and nobody else.

**Rollback.** Reverting the code removes the gate, the two step-up operations and the recording; the previous behaviour (any member writes) returns, which is the defect this spec closes, so a revert is a security regression and is release-noted as such. The down migration drops the grant table, the SSO event table and `organizations.ms_tenant_id` — losing their rows, which hold no user data beyond ids and short enums — and restores the raw uniqueness on `sso_providers.domain`; it does **not** restore the original casing of backfilled domains, which is harmless because every reader compares lower-cased after this spec and the only pre-existing writer of mixed case was the defect itself.

## 8. Revision log

* v2 (2026-10-21 am): IdP-derived authorization replaces the four designation options; absorbs PAC-46.
* v2.1 (2026-10-21 pm): after an internal challenge (12 findings across PAC-49/50) — provider domain pinned to org domain (§4.2); tenant bound by verified domains, not first use (§4.3); callback behind the session with state/user binding; atomic single-use grants with a stated 10-minute TTL; break-glass needs a fresh login; IdP endpoints injectable so ACs are really offline-testable; AC-9/13/14/16/17/19/20 rewritten or added; v1 Challenge findings closed (OQ-5).
* v2.2 (2026-10-22): response to the v2.1 challenge — see the `Revision v2.2` section below for the finding-by-finding map. Headlines: the write carries no grant reference (server-side atomic consume); the step-up start is a `GET` behind the session and the callback redirects to the settings page with the outcome; the admin UI is out of scope and the e2e journey is API-level; migration ordering 025 → 026 → 027 stated; U-21/U-22/API-060/066/067/092 dispositioned in the header; every AC rewritten as Given/When/Then; AC-19 withdrawn; recording moves from the activity log to a dedicated SSO event table (no PAC-23 dependency left).

## Revision v2.2 — 2026-10-22 (response to the v2.1 challenge)

Every finding of the `Challenge — 2026-10-22 (v2.1)` section, in its numbering, and what changed in the body. Decisions marked *(orchestrator, 2026-10-22)* were taken outside this revision and are written in as decisions.

**Blocking**

* **B1** — *(orchestrator)* The write request carries nothing; the server consumes the caller's newest live grant for (caller, caller's org) in one select-and-update statement returning the id; zero rows → 403 `sso_write_requires_admin_stepup`. §4.1 "Grant", §4.2 step 5a, AC-2a/b and AC-3a–d restated accordingly; §7 records the rejected alternative.
* **B2** — *(orchestrator)* Start is `GET /api/admin/sso/stepup?provider=google|microsoft` behind the session (state cookie + 302; missing/unknown provider 400); `stepup_url` is that URL without the provider; the callback ends with a redirect to the settings page carrying `?stepup=granted` or `?stepup=denied&reason=<enum>`. Admin UI out of scope, stated in §3 with the follow-up issue to file; §5's journey is now AC-23, API-level against the stubs; the only web requirement is the error-mapping navigation (§4.3, AC-22).
* **B3** — *(orchestrator)* §4.9 Sequencing: golang-migrate applies upward from the current version, so 025 → 026 → 027 on every environment and PAC-50's stage-4 PR merges after PAC-23's and PAC-49's. AC-12 is implemented, never skipped — and after the recording decision below it no longer depends on PAC-23 at all.
* **B4** — *(orchestrator)* Header "Inherited scope" lists U-21, U-22, API-060, API-066, API-067, API-092 with one-line dispositions: API-067 fixed here (AC-16c/d), API-092 closed by this spec's tests, U-21/U-22 determined at stage 2 by reading the handler with the secret recorded as a defect linking PAC-22 (rule 2); PAC-22 lands after PAC-50 and rebases (§4.9).
* **B5** — *(orchestrator)* All ACs rewritten as Given/When/Then with layer tags, numbers kept, a/b/c suffixes where one AC bundled behaviours; AC-19 withdrawn and replaced by the non-regression pointer in §4.5; AC-18 names the exact codes, fields and the two new operations. New AC-21 (start operation), AC-22 (web client), AC-23 (e2e journey) carry behaviours the decisions introduced.

**Non-blocking**

* **NB1** — Applied: §1.1 describes the membership check as present and caller-derived (`handlers_sso.go:201-206, 233, 291-298`); §4.2 steps 1–2 say it is kept, not verified.
* **NB2** — Applied: the "additionally scoped by `org_id`" sentence is gone; §4.2 step 4 says the provider table has no org column and 027 adds none.
* **NB3** — Applied: §4.8 lists the whole of migration 027 once (grant table, SSO event table, `lower(domain)` uniqueness with backfill, `ms_tenant_id`), with the `organizations.domain` note beside it.
* **NB4** — Applied: §4.2 steps 1 and 6 and AC-16c/d — delete compares and deletes lower-cased; zero rows → 404 (API-067).
* **NB5** — Decided: re-pinning after a tenant migration is an operator task done directly in the database; break-glass does not re-pin and is never part of a step-up; `tid_changed` stays a denial (§4.3 step 2, §3 out of scope, AC-9c no longer says "unless break-glass").
* **NB6** — Decided: `Secure` is a deployment property — set in production, not in the plain-HTTP e2e stack; how the server learns it is the plan's (§4.3, with the nginx/compose facts).
* **NB7** — Applied: unknown body fields are rejected with 400, matching `additionalProperties: false` (§4.2 step 3, AC-16a).
* **NB8** — Applied: AC-17 restated as the database observable (settings row and OAuth-token row identical before and after).
* **NB9** — Answered by the recording decision *(orchestrator, 2026-10-22)*: the two records do not go into the activity log at all; they go to a dedicated SSO event table, written with an explicit subject, not exposed by any API in this spec, read by operators directly (§4.6, AC-12). A future operator view is out of scope (§3).
* **NB10** — Applied: the "PAC-45 unmerged" / "until #172 merges" branches are gone from §1 and §4.5; the body states PAC-45 is merged and cites the binding and its tests. The reality-check header stays as history, with a note that two of its statements are superseded.
* **NB11** — Applied where the behaviour could be stated without the name: `engine.Clock`, `UserIDFromContext`, `encoding/json`, `WriteAuditLog`'s signature, the consume SQL, the cookie name, the HMAC composition, `requireAuth`, the storage function names are out of §4; each behaviour is kept. Routes, error codes, the env var and the §4.8 schema objects remain, by the orchestrator's decisions, and the exception is stated at the top of §4.
* **NB12** — Applied: OQ-7 closed; §4.1 "Clock" notes the issue time is wall-clock stamped and AC-14 mints tokens with a chosen issue time.
* **NB13** — Applied: §4.1 "IdP endpoints" states the redirection is honoured only under the PAC-43 test-mode declaration, exactly like the calendar address; production uses the real endpoints; and says plainly that none of it exists today (no Directory or Graph `/organization` client, calendar-only stand-in).
* **NB14** — Applied: §1.1 and §5's preamble say "update" is a second `POST` (upsert on domain).

**Criteria the challenger could not falsify**

* **AC-19** — Withdrawn; §4.5 names the two existing tests that protect the binding and forbids weakening them.
* **AC-18** — Rewritten to list every code, field and operation the contract must document; the `--check` sentence is gone.

**Citations found wrong** — all four corrected: §1.1 (membership present, caller-derived), §4.2 step 4 (no org-scoped lookup), §4.3 (`Secure` as a deployment property), §1/§4.5 (PAC-45 merged). Every citation in this body was re-opened on 2026-10-22 against the worktree at the v2.1 challenge's commit plus the uncommitted spec edits.

**Audit-related items, consolidated** *(orchestrator amendment, 2026-10-22)* — §4.6 is now "Recording": migration 027 creates a dedicated SSO event table `(id, user_id, org_id, kind, details JSON, created_at)`; the two kinds and their details shapes are unchanged; no API exposes the table; AC-12 asserts rows there; "Depends on PAC-23" is reduced to migration ordering (§4.9). Affects B3, NB9 and AC-12 above; everything else is as briefed.

---

## Challenge (2026-09-17, stage-1 challenger, cloud session)

Verdict: **accept-with-changes**. The authorization model (§3.1–§3.4) is sound and
the two decisions it makes (operator designation, exact-domain confinement) are
correctly justified. Two gaps below should be closed before stage 2; neither
changes §3's decisions.

### Finding 1 — AC-9's uniqueness guarantee can be violated by data that predates it

§3.2 states domain comparison is case-insensitive and whitespace-trimmed, and that
"what is stored is the normalised form." AC-9 concludes "exactly one configuration
exists for that organisation, not two" once this lands.

That conclusion depends on every *existing* row already being in normalised form.
`sso_providers.domain` is `NOT NULL UNIQUE` (`008_sso_providers.up.sql:3`) as a raw
Postgres text column — the uniqueness constraint is exact-byte, not
case/whitespace-folded. Nothing cited anywhere in §2 shows domains were normalised
before storage prior to this spec. If two rows already exist for the same logical
domain in different casing (e.g. planted by the suffix-match bug in §2.4, which by
construction writes to whatever string the caller supplied), normalising
comparisons at read/write time does not merge them — it makes both rows match the
same lookup, and which one sign-in and the listing return becomes an ordering
artifact of the query rather than a decided outcome. AC-9 tests the forward case
(one caller, one submission) and would pass even if this backlog exists.

Add to §7 (Rollback) or as a release requirement alongside the two already there: an
inventory of `sso_providers` rows whose domain differs from its own
lowercased/trimmed form, before release, with the same disposition options §3.3
already establishes for stranded rows (matched to an organisation, or flagged
unexplained). This is the same shape of gap PAC-49 was for PAC-24 — a migration
comment or an acceptance criterion assumed a data invariant nothing enforces.

### Finding 2 — AC-1 doesn't state the designated-but-orgless case

§3.1 designates principals; §3.2 confines each to their own organisation's domain.
Nothing in §4 states the outcome for a principal who is *designated* but has no
organisation (`OrgID == nil`) — e.g. named by email before ever signing up, or after
their org association is cleared. By §2.1's existing gate this should fail the same
"org membership required" branch AC-1 already covers for the undesignated case, but
AC-1 as written only exercises "no one designated," and AC-2/AC-3 assume the actor
has an organisation. Suggest folding this into AC-1 or adding AC-1b: a designated
principal with no organisation is refused for the same reason and by the same
mechanism as an undesignated one, not a new third error path that would violate the
distinguishability requirement in §6.

### Not a finding, confirmed correct

The PAC-45 hedge (both-worlds framing, OQ-3) matches ground truth as of this
challenge: `origin/main` is still `6f7a856`, PR #172 is still open, and the
Linear PAC-45 issue's "Done" status was a bookkeeping error independent of the
code — the underlying fix is genuinely unmerged. No change needed here; flagging
only so a future reader doesn't waste time re-verifying it.

> **Erratum, 2026-09-17.** This confirmation is wrong, for the same reason as
> the precondition section: #172 had already been squash-merged as `5c5f34f`, and
> Linear's "Done" was correct. See the erratum under "This spec's precondition
> on PAC-45".

---

## Challenge — 2026-10-22 (v2.1)

**Verdict**: accept-with-changes

Challenged by `spec-challenger` (local session, worktree `nihochart/pac-49-50-specs` at `a896faa`). Every claim below was checked against the code on that commit; `go build ./... && go vet ./...` pass. The Linear MCP was not authorised in this session, so the issue body was not re-read; the spec was compared against the issue's title ("SSO configuration is writable by any employee and for any subdomain") and v1's lineage. Both halves of that problem are addressed (§4.2 gate, §4.7 exact-domain rule). The design — authorisation derived from the org's own IdP, a short-lived single-use grant, env-list break-glass — is sound and the v2.1 rewrite closed the real holes (tenant bound by `verifiedDomains`, callback behind `requireAuth`, atomic consume). What follows is what the contract and plan stages cannot start without.

### Blocking

1. **A write never names its grant.** §4.1 gives the grant row an `id`; §4.2 step 4a consumes `WHERE id = $id AND user_id = $caller AND org_id = $org`. Nothing says where `$id` comes from. Neither `SsoProviderCreateRequest` (`contracts/openapi/paths/auth.yaml:811-843`) nor `DELETE /api/admin/sso/{domain}` carries one, and AC-18 lists only the 403/400 codes and `stepup_url`. AC-2 ("a grant for org A"), AC-3 ("the second write with the same grant", "two writes fired concurrently with the same grant") presuppose the request identifies a grant; if instead the server selects the caller's unconsumed grant for the org, the consume predicate loses `id = $id` and AC-3's concurrency test is over a different statement. *Resolves it*: state which — the write request carries a grant reference (then AC-18 must put it in the contract), or the server selects the caller's live grant for the org (then §4.2 step 4a is restated). Either works; the spec must pick, because the contract stage cannot.

2. **The client side of the step-up is undefined, and the frontend is neither in nor out of scope.** Three gaps, one finding:
   - §4.2 returns a 403 with "a `stepup_url` the client can open"; §4.3 defines the start as `POST /api/admin/sso/stepup?provider=…` returning JSON `{redirect_url}`. A URL a client *opens* is a GET navigation; a POST returning JSON is a call. The spec does not say which the 403 carries, nor whether the provider is chosen before or after the 403.
   - The callback `GET /api/admin/sso/stepup/callback` is a top-level navigation from the IdP. The spec never says where it sends the browser after issuing or denying the grant, nor how the SPA learns the outcome.
   - §3's five in-scope items are all server-side, yet §4.2 says "the frontend step-ups once per save" and §5 commits an e2e journey ("member saves SSO → is sent to step-up → … → save lands"). There is no SSO admin UI to do any of that: `smart-calendar-flow/src/pages/` has no SSO page and the only references to `/api/admin/sso` in `src/` are the generated types (`src/api/generated/types.ts:7-49`). Per README §6 a `[frontend]` sub-issue has nothing to build unless the spec says so.
   *Resolves it*: define `stepup_url` (what it is, which method), the callback's terminal redirect for both outcomes and how the outcome reaches the client; and one sentence in §3 putting the admin UI in scope (naming the user-visible behaviour: where the save is, what the step-up prompt and the 403 look like) or out of scope with §5's journey restated as the API-level journey against the stubbed IdP. AC-18 is written against whichever is chosen.

3. **Sequencing on PAC-23 and on migration order is asserted, not stated.** AC-12 and §4.6 need `WriteAuditLog` to take a `userID` and `audit_log` to have `user_id`. Today `WriteAuditLog(db, action, details)` has no user (`backend/storage/focus_blocks.go:74-75`) and `audit_log` has no user column (`backend/storage/migrations/001_initial.up.sql:44`; no later migration touches it). PAC-23 is `draft` (`docs/specs/PAC-23.md:4`) with an accept-with-changes challenge from 2026-09-17; its plan takes migration 025 (`docs/factory/PAC-23-plan.md:29-34`). The spec says "Depends on PAC-23" and "PAC-23 must provide this" but not what happens if PAC-50 reaches stage 4 first: is AC-12 a `t.Skip` naming PAC-23 (README §3 rule 4), does PAC-50 carry the column, or is PAC-50 blocked at stage 4 behind PAC-23's merge? There is also a hard ordering constraint nobody has written down: migrations run through golang-migrate `Up()` (`backend/storage/db.go:40-44`), whose `readUp` walks `Next(from)` upward from the current version only — a database that has applied 027 will never apply a 025 or 026 that lands afterwards. *Resolves it*: state the release ordering (025 and 026 applied before 027 on every environment), or make the number "next free at merge time" and leave it to the plan (README §2 keeps file-level names out of the spec anyway); and state AC-12's status if PAC-50 is implemented first.

4. **Inherited scope on the touched operations is routed to PAC-22 without an ordering.** PAC-50 modifies `createSsoProvider` and `deleteSsoProvider`. `createSsoProvider` carries `x-uncertain` U-21 on its request schema (`contracts/openapi/paths/auth.yaml:840-843`) and U-22 on its response schema (`:901-904`), plus open audit rows API-060, API-066, API-067, API-092 (`docs/factory/api-audit.md:294,300,301,326`). README §7 rule 1 and the stage-2 gate (item 3) require a feature touching these to resolve them. Sending secret projection to PAC-22 (§3) is a fine allocation, but PAC-22 is `draft (v3)` with two `reject` verdicts (`docs/specs/PAC-22.md:4,627,903`) and PAC-50 says nothing about which lands first. PAC-50's contract first → fails gate 3 on U-21/U-22; PAC-22 first → both edit the same fragment and PAC-22's DTO is what AC-18 must describe. *Resolves it*: state the ordering (PAC-22 before PAC-50, AC-18 written against PAC-22's response shape), or absorb U-21/U-22/API-060/066/067 here and list them in the header. API-092 is partly stale — `backend/api/handlers_sso_test.go:138-183` now covers `oidcCallback` — but the three admin operations still have no test, and this spec's ACs are what would close it; say so.

5. **Form: the ACs are not Given/When/Then.** README §2 Stage 1 gate: "every acceptance criterion is written as Given/When/Then". AC-1..AC-20 are prose; the sibling specs comply (`docs/specs/PAC-23.md:367`, `PAC-49.md:721`, `PAC-22.md:351`). Mechanical, and the content is mostly there, but it is the stated gate and the challenger is not entitled to waive it. *Resolves it*: rewrite the twenty.

### Non-blocking

1. §4.2 step 2 hedges "Today this check may be missing or body-derived — the implementer verifies." It is present and caller-derived: `backend/api/handlers_sso.go:201-206` (create) and `:291-296` (delete) 403 on `user.OrgID == nil`; the org comes from `*user.OrgID` at `:233` and `:298`. Describe it as current behaviour.
2. §4.2 step 3: "the login-time lookup is additionally scoped by `org_id`". `sso_providers` has no `org_id` column (`backend/storage/migrations/008_sso_providers.up.sql:1-15`); `GetSSOProviderByDomain` is keyed on domain alone (`backend/storage/sso_providers.go:47-54`); the only org linkage is `ListSSOProvidersByOrg`'s JOIN on `organizations.domain` (`:89`). Either 027 adds the column (then say so — next item) or drop the sentence; as written it is a requirement nothing can satisfy and no AC tests.
3. Migration 027 is three different things described in three places: the grant table (§4.1), `lower(domain)` uniqueness plus backfill on `sso_providers` (AC-20, OQ-5), and `organizations.ms_tenant_id` (§4.3 step 2; AC-9 asserts it "stays NULL", so the column must exist). List the schema change once. `organizations.domain` is raw-UNIQUE too (`007_organizations.up.sql:4`) and is only safe because `AssociateUserWithOrg` lower-cases via `ExtractDomain` (`orgs.go:43`, `domain.go:21`) — worth stating next to AC-20.
4. `DELETE /api/admin/sso/{domain}`: §4.2 step 1 says the segment "must equal" the org domain, but `DeleteSSOProvider` deletes by the raw string (`sso_providers.go:108-112`) while the comparison lower-cases (`domain.go:46-47`), so today `DELETE …/ACME.com` passes the gate, deletes nothing and returns 204 (API-067). AC-16 covers the body, AC-20 covers upsert; no AC says what a mixed-case `{domain}` does on delete after this spec.
5. `tid_changed` has no recovery path. §4.3 step 2 says a new `tid` with the same verified domain is "audited as `tid_changed` … require break-glass for that case", and AC-9 says "denied `tid_changed` unless break-glass". Break-glass (§4.5) is a *write* bypass for an env-listed email; it is never part of a step-up, and nothing in the spec can change `organizations.ms_tenant_id` once pinned. After a genuine tenant migration the org's Microsoft admins can never step up again and only the deployment operator can write. State what break-glass does here (re-pin, clear the pin) or state that re-pinning is out of scope and a tenant migration is an operator task.
6. The `Secure` cookie clause (§4.3) is written as a server-observable: "when the request arrived over TLS". In production TLS terminates at nginx (`docker-compose.prod.yml:113-125`), which forwards `Host`, `X-Real-IP`, `X-Forwarded-For` and no `X-Forwarded-Proto` (`nginx/nginx.conf:7-9`), so the Go process never sees TLS and the cookie would never be `Secure` where it matters. The existing `auth_token` cookie sets no `Secure` at all (`handlers_auth.go:155-161`). State the requirement as a deployment property (Secure in production, not in the plain-HTTP e2e stack) and leave how the server learns it to the plan.
7. AC-16: a body `org_id` is "ignored or rejected with 400" — two behaviours; a contract encodes one. Today the decoder ignores unknown fields (`handlers_sso.go:219`, no `DisallowUnknownFields`) while the contract says `additionalProperties: false` (`auth.yaml:837`). Pick one.
8. AC-17 prescribes a test mechanism ("the storage layer … is a recording mock … called zero times") the code cannot offer: the handlers hold a `*sql.DB` (`handlers_sso.go:19-21`; `handlers_sso_test.go:116`) and `SaveMicrosoftToken` is a package function on `*sql.DB` (`backend/auth/microsoft_oauth.go:52`). The observable is fine and falsifiable against the testcontainers database the existing tests use: `settings.microsoft_tokens`, `settings.calendar_provider` (`003_calendar_providers.up.sql:1-2`) and the user's `oauth_tokens` row unchanged after both step-ups. Restate as the observable.
9. Audience of the audit rows. §4.5 says break-glass is audited "so it is visible". PAC-23 makes `GET /api/audit` per-user (`WHERE user_id = $1`, `docs/factory/PAC-23-plan.md:38-40`), and its own OQ-7 notes the table is being asked to be two things. After both land, `sso_provider_written` and `sso_stepup` rows are visible only to the person who caused them. If "visible" means to someone other than the actor, say to whom; if not, say the rows are for operators reading the database.
10. Stale text the landing preserved. §1 "With PAC-45 unmerged … full account-takeover path" and §4.5 "until PR #172 merges" are contradicted by the reality-check header: PAC-45 is merged (`domain.EmailBelongsToDomain`, `backend/domain/domain.go:33-40`, applied at `handlers_sso.go:177-184`). README §8 says the artifact must stand alone; the body should say it, not the header.
11. Level. README §2 Stage 1: a spec "does not name files, packages or function signatures". §4 names `engine.Clock`, `UserIDFromContext`, `encoding/json`, `WriteAuditLog`'s signature, the exact consume SQL, the cookie name and the HMAC composition. The behaviour behind each is a legitimate requirement, so not blocking; flagged because the README is binding and says so, and because several of these are decisions the plan should get to argue.
12. OQ-7 can be closed: the session JWT carries `iat` (`backend/auth/jwt.go:23`). Note `GenerateToken` stamps it from `time.Now()`, not from the Clock, so AC-14's "issued 5 / 11 minutes ago" is set up by minting a token with a chosen `iat`, not by advancing the Clock — fine, but §4.1's "all expiry comparisons go through `engine.Clock`" is half true for this check.
13. §4.1 "IdP endpoints are config values" is a requirement, not a description, and the distance is larger than the sentence suggests. Nothing is configurable today: Google uses the library's `google.Endpoint` (`backend/auth/google_oauth.go:20`), Microsoft a package-level var (`microsoft_oauth.go:14-17`), Graph a `const` (`backend/calendar/outlook_client.go:15`, `backend/conference/teams.go:13`, `backend/api/handlers_microsoft_auth.go:91`), userinfo is hardcoded (`handlers_auth.go:179`), and the only seam is PAC-43's calendar-only `GOOGLE_CALENDAR_API_BASE_URL` gated by `PACEDAY_ALLOW_STANDINS` (`backend/config/endpoints.go:17-20`). No Admin SDK or Graph `/organization` client exists at all, and the e2e stand-in serves calendar only (`e2e/fake-google/`: calendarlist, events, freebusy). So §5's "all ACs are testable offline" is a consequence of new work this spec is asking for. The spec should also say the stand-in gate governs these new redirections — PAC-43 established that redirecting provider traffic is refused outside a declared test deployment, and an IdP redirection is the more dangerous one.
14. AC-1's "update" leg: there is no update operation — `POST /api/admin/sso` upserts (`sso_providers.go:63`). Falsifiable as two POSTs; say so, so the test author does not look for a PATCH.

### Criteria I could not falsify

- **AC-19**: PAC-45 is merged. A test "that asserts the function exists" cannot fail today — the build would not compile without it, and it exists (`domain.go:33`). The behavioural test already exists and passes (`handlers_sso_test.go:138-160`). No test distinguishes before from after. Drop it, or restate as a non-regression pointer to the existing test.
- **AC-18**, as written: "`scripts/openapi_assemble.py --check` passes" is a stage-2 gate property (`Makefile:102`), not a behaviour; it passes today and after. The falsifiable part is the set of codes and fields the three (now five) operations document — `sso_write_requires_admin_stepup` + `stepup_url`, `sso_write_requires_fresh_login`, `provider_domain_mismatch` — and the AC names none of them. The contract-challenger cannot check "the 403/400 codes" without the list.

### Citations I checked and found wrong

- §4.2 step 2 "this check may be missing or body-derived" — present and caller-derived at `backend/api/handlers_sso.go:201-206, 233` and `:291-298`.
- §4.2 step 3 "the login-time lookup is additionally scoped by `org_id`" — no such column or predicate; `backend/storage/migrations/008_sso_providers.up.sql:1-15`, `backend/storage/sso_providers.go:47-54`.
- §4.3 "`Secure` when the request arrived over TLS" — the server never observes TLS in production; `nginx/nginx.conf:7-9`, `docker-compose.prod.yml:113-125`.
- §1 and §4.5 "with PAC-45 unmerged" / "until PR #172 merges" — merged; `backend/api/handlers_sso.go:177-184`.
- AC-19 — rests on the above.

Correct and verified, so the next reader need not: `storage/orgs.go:42-52` and `api/handlers_auth.go:76` (self-service org association); `domain/domain.go:48` (suffix match); the seven existing audit actions and the four concatenating writers (`handlers_nlp.go:67`, `handlers_schedule.go:191`, `engine/smart_schedule.go:325`, `engine/compression.go:180`); `UserIDFromContext` returns `uuid.Nil`, not an error (`auth/context.go:19-24`); `engine.Clock` (`engine/clock.go:8-10`); no role column on `users` (`006_auth.up.sql:3-12`), `team_members.role` self-granted on team create (`handlers_teams.go:54`); highest migration on main is 024; OQ-5's two v1 closures hold — AC-20 covers v1 Finding 1 and is testable, and AC-1's last sentence covers v1 Finding 2 and matches the check order at `handlers_sso.go:201-206`.

### Consumers this breaks

- None today. No non-generated reference to `/api/admin/sso` in `smart-calendar-flow/src`; none in `mcp/`; none in `e2e/tests`. The generated types (`smart-calendar-flow/src/api/generated/types.ts:7-49`) regenerate from the bundle.
- For the frontend stage, not a break: `smart-calendar-flow/src/api/client.ts:334` renders a 403 whose body has no `error` as "You don't have permission to do this." — the step-up 403 must carry `error` or the generic message swallows the prompt. `src/contexts/AuthContext.tsx:58` treats 401/403 as "session gone" only for `/api/auth/me`, so a step-up 403 on `/api/admin/sso` will not log the user out.
