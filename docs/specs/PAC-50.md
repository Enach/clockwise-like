# Spec — PAC-50: SSO configuration must not be writable by every employee, nor for domains that are not the caller's

- **Linear**: https://linear.app/paceday/issue/PAC-50/sso-configuration-is-writable-by-any-employee-and-for-any-subdomain
- **Status**: v2.1 (2026-10-21), **awaiting challenge**. Replaces the v1 body (2026-09-17, last at `d4ae0bf`); v1's Challenge section is kept at the bottom, and both of its findings are closed in this version (OQ-5).
- **Author**: spec-author (cloud session, 2026-10-21)
- **Absorbs**: PAC-46 (exact-domain rule).
- **Depends on**: PAC-23 (audit `user_id`, AC-12); PAC-45 / #172, merged (break-glass, §4.5).

> **Landed from Linear on 2026-10-21** — document [PAC-50 spec v2.1 (2026-10-21) — SSO writes authorized by the org's own IdP admins](https://linear.app/paceday/document/pac-50-spec-v21-2026-10-21-sso-writes-authorized-by-the-orgs-own-idp-1861a1cadea6). This file is now the canonical copy. Linear's `<issue>`/`<pull-request>` markup was rewritten as markdown links. Edits while landing, per the document's own reality-check header and the §4.2 route note: the step-up routes are written as `POST /api/admin/sso/stepup` and `GET /api/admin/sso/stepup/callback` everywhere (the org is the caller's; there is no `{orgId}`), AC-16 says so, and the grant-table migration is **027** (025 is PAC-23's, 026 is PAC-49's). Nothing else was changed.

> ⚠️ **Reality check, 2026-10-21 16:10** — three facts learned after drafting: (1) [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email) **/ PR** [Enach/clockwise-like#172](https://linear.app/paceday/review/pac-45-fixauth-bind-the-oidc-asserted-email-to-the-providers-domain-ac3dd71bc31d) **is merged** (since 2026-09-17; erratum in [Enach/clockwise-like#176](https://linear.app/paceday/review/docspac-50pac-22-spec-challenge-and-an-erratum-pac-45-is-merged-f251b598b6a7)), so §1's "with [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email) unmerged" branch is history and AC-19 reduces to a one-line test that the binding exists. (2) [PAC-24](https://linear.app/paceday/issue/PAC-24/switch-apisettings-and-the-daily-recap-handlers-to-per-user-settings) **/** [Enach/clockwise-like#204](https://linear.app/paceday/review/featsettings-every-user-has-their-own-settings-pac-24-89e2813268be) **merged 2026-10-15**, so `PATCH /api/settings` replaced `PUT`, and migration 024 is taken — the grant table migration number in §4.1/§4.2/OQ-6/AC-20 is "next free", not necessarily 025 — **landed as 027** (025 is PAC-23's, 026 is PAC-49's). (3) No implementation PR for [PAC-50](https://linear.app/paceday/issue/PAC-50/sso-configuration-is-writable-by-any-employee-and-for-any-subdomain) exists in the repo; the issue's Done status dates from the docs-only PR [Enach/clockwise-like#176](https://linear.app/paceday/review/docspac-50pac-22-spec-challenge-and-an-erratum-pac-45-is-merged-f251b598b6a7). The design itself is unaffected.

* Stage: 1 (spec). Author: cloud session, 2026-10-21. Supersedes v1 (2026-09-17).
* Status: **awaiting challenge**. v1's `## Challenge` section (2 findings, accept-with-changes, 2026-09-17) lives in `docs/specs/PAC-50.md` on disk; whoever lands this file keeps it appended. Both v1 findings are closed in this version — see OQ-5.
* Absorbs [PAC-46](https://linear.app/paceday/issue/PAC-46/createssoprovider-and-deletessoprovider-authorize-with-a-suffix-match) (exact-domain rule) — same two handlers, same lines.
* Depends on: [PAC-23](https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the) (audit `user_id` column) for AC-12; [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email) (PR [Enach/clockwise-like#172](https://linear.app/paceday/review/pac-45-fixauth-bind-the-oidc-asserted-email-to-the-providers-domain-ac3dd71bc31d)) for severity framing and for the break-glass path (§4.5).

## 1. Problem

Any organization member can create, edit or delete the organization's SSO provider. Membership is self-service: `AssociateUserWithOrg` (`storage/orgs.go:42-52`, called at `api/handlers_auth.go:76`) derives the org from the signup email's domain and creates the org if absent. There is no admin role anywhere (no role column on `users`, no owner on `organizations`, `team_members.role` is self-granted, `user_profiles.is_manager` is auto-detected).

So "may rewrite the IdP that logs everyone in" currently reduces to "signed up with a non-generic email domain". With [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email) unmerged, that is a full account-takeover path; with [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email) merged it is still a denial-of-login and configuration-tampering path. Either way it must close before the first real user signs up (production has zero users as of 2026-10-21).

Secondary defect (ex-[PAC-46](https://linear.app/paceday/issue/PAC-46/createssoprovider-and-deletessoprovider-authorize-with-a-suffix-match)): `DomainMatchesOrg` is a suffix match while every other domain comparison is exact (`GetSSOProviderByDomain`, `ListSSOProvidersByOrg`, `UpsertSSOProvider`, `DetectAuthProvider`, `AssociateUserWithOrg`, `EmailBelongsToDomain`). `evil-acme.com` suffix-matches `acme.com`.

## 2. Decision (human, 2026-10-21)

Authorization for SSO writes is **derived from the organization's own identity provider**, not from anything Paceday stores:

> An SSO write is allowed if a fresh re-authentication with Google or Microsoft, completed within the last few minutes, proves the caller is an administrator of the organization that owns that verified domain/tenant. A deployment-level admin list (environment variable) is the break-glass fallback.

Rationale: the org's real admins already decide who administers identity; Paceday should ask them rather than invent its own role system. The fallback exists for orgs on neither Google Workspace nor Microsoft 365, and for recovery.

## 3. Scope

In scope:

1. A write-authorization gate on the three SSO write operations (create, update, delete — the handlers that call `UpsertSSOProvider` and `DeleteSSOProvider`).
2. An "admin step-up" flow: a short-lived, purpose-bound proof that the caller is an IdP admin for the org's domain/tenant.
3. The env-var break-glass list.
4. Exact-domain matching in `DomainMatchesOrg` (and any other suffix match found while in there).
5. Audit entries for every SSO write and every step-up outcome.

Out of scope (own issues):

* Using `hd` / `tid` to decide **org membership at signup** instead of the email domain. Larger change, touches `AssociateUserWithOrg`; filed separately as "verified org membership". [PAC-50](https://linear.app/paceday/issue/PAC-50/sso-configuration-is-writable-by-any-employee-and-for-any-subdomain) only uses the claims to authorize writes.
* Revoking live sessions when a provider changes (no server-side session store — separate problem).
* SSO **read** authorization and secret projection — that is [PAC-22](https://linear.app/paceday/issue/PAC-22/stop-returning-oidcclientsecret-and-samlcert-from-apiadminsso).

## 4. Design

### 4.1 Vocabulary

* **Org domain**: `organizations.domain`, exact string, lower-cased.
* **Step-up**: a fresh OAuth/OIDC round-trip with Google or Microsoft, started by Paceday specifically for an SSO write, whose result is checked for admin status and then stored as a short-lived grant.
* **Grant**: server-side row in `sso_stepup_grants` (migration 027): `(id, user_id, org_id, provider, issued_at, expires_at, consumed_at NULL)`. **Lifetime: 10 minutes.** Not a claim in the session JWT — the session JWT is a 7-day bearer with no revocation; a grant must not inherit that lifetime.
* **Break-glass admin**: an email listed in `PACEDAY_ADMIN_EMAILS` (comma-separated, lower-cased, exact match on the session's `email` claim).
* **Clock**: all expiry comparisons go through the `engine.Clock` interface that [PAC-49](https://linear.app/paceday/issue/PAC-49/listuserswithautoschedule-does-not-filter-user-id-so-the-focus-cron-is) threads through the cron (same seam, same release); tests inject a fake.
* **IdP endpoints**: issuer, JWKS/discovery URL, token URL, Admin SDK base and Graph base are **config values** with production defaults, so `httptest` servers can stand in for Google and Microsoft. Without this none of §5 is testable offline.

### 4.2 Gate

Every SSO write handler, before touching storage:

1. Resolve the target org from the **caller's** `users.org_id` — the routes are `POST /api/admin/sso` and `DELETE /api/admin/sso/{domain}`, and the step-up routes in §4.3 are `POST /api/admin/sso/stepup` and `GET /api/admin/sso/stepup/callback`. There is no `{orgId}` segment anywhere: the organisation is always the caller's. **Never** from the request body, and for `DELETE` the `{domain}` path segment must equal the caller's org domain (step 3) — it is a target, not an authorization.
2. Caller must be a member of that org (`users.org_id = org.id`). 403 otherwise. (Today this check may be missing or body-derived — the implementer verifies.)
3. **The provider's** `domain` **must equal** `organizations.domain` (exact, lower-cased). Otherwise 400 `provider_domain_mismatch`. Without this, a real admin of `acme.com` could upsert a provider row whose `domain` is `other.com` and hijack that org's login, because `GetSSOProviderByDomain` and `DetectAuthProvider` are keyed by domain alone. The login-time lookup is additionally scoped by `org_id` as belt-and-braces.
4. Caller must hold **one** of:
   a. a grant for `(caller, org)` consumed **atomically** right now: `UPDATE sso_stepup_grants SET consumed_at = $now WHERE id = $id AND user_id = $caller AND org_id = $org AND consumed_at IS NULL AND expires_at > $now RETURNING id`. Zero rows → no grant. The grant is burned whether or not the write then succeeds; a failed write means a new step-up. Two concurrent writes with the same grant: exactly one passes.
   b. break-glass (§4.5).
   Otherwise respond **403** with error code `sso_write_requires_admin_stepup` and a `stepup_url` the client can open. This is deliberately not a 401: the session is valid, the privilege is missing.
5. Write, then audit (§4.6).

One grant authorizes one write. The frontend step-ups once per save, which is the intended UX ("confirm with your Google/Microsoft admin account").

### 4.3 Step-up flow

`POST /api/admin/sso/stepup?provider=google|microsoft` (behind `requireAuth`; the org is the caller's, never a path or body parameter) → JSON `{redirect_url}`, with a purpose-bound state cookie (`sso_stepup_state`, `SameSite=Lax`, `HttpOnly`, `Secure` **when the request arrived over TLS** — the docker-compose/e2e stack is plain HTTP and would never return an unconditional `Secure` cookie; 10-minute expiry; value is an HMAC of `caller user_id + org_id + provider + nonce`).

Which IdP: the caller picks the button; the **result** is what is checked against the org's domain, so a wrong pick simply fails. No org-level "provider type" needs to exist before the first write.

Callback `GET /api/admin/sso/stepup/callback` — **behind** `requireAuth` (the SPA uses the `auth_token` cookie, so the top-level navigation from the IdP carries the session; if it ever does not, the callback 401s rather than proceeding with `UserIDFromContext`'s nil UUID). Then:

0. State cookie valid, unexpired, and its `user_id` **== session** `sub`. Nil `sub` → 401. Email equality (below) is not a substitute for this, because Entra's `email`/`preferred_username` are attacker-settable in the attacker's own tenant.

**Google**

1. Exchange code; verify ID token (signature, `aud`, `iss`, `exp`).
2. Require `email_verified == true` and `hd` **claim == org domain** (exact). The `hd` claim is only present for Google Workspace / Cloud Identity accounts and is the documented way to establish hosted-domain membership; the email's domain is explicitly insufficient ([developers.google.com/identity/openid-connect/reference](http://developers.google.com/identity/openid-connect/reference)). Absent or mismatched `hd` → fail.
3. Require the ID token's `email` == the caller's session `email` (the person stepping up is the person logged in).
4. Call Admin SDK Directory `users.get(userKey = sub-or-email)` with scope `https://www.googleapis.com/auth/admin.directory.user.readonly`, requested during this step-up only. Require `isAdmin == true` **or** `isDelegatedAdmin == true` (decided 2026-10-21, OQ-1: delegated admins qualify). Note: a non-admin typically cannot call this endpoint at all; treat any error as "not admin", never as "retry".
5. Issue grant.

**Microsoft**

1. Exchange code; verify ID token (signature, `aud`, `iss`, `exp`).
2. **Bind the tenant to the org domain by evidence, never by first use.** Call Graph `GET /organization?$select=id,verifiedDomains` with the step-up token (delegated `Organization.Read.All`; the challenger may find `User.Read` returns enough via `/organization` for some tenants — if so, prefer it). Require `id == tid` **and** `verifiedDomains[].name` contains the org domain (exact, case-insensitive). This is Microsoft's equivalent of Google's `hd`: a tenant only lists a domain as verified after DNS proof. On success, record `organizations.ms_tenant_id = tid` (idempotent; a different `tid` later with the same verified domain is a domain transfer and is audited as `tid_changed`, not silently accepted — require break-glass for that case). A `tid` that does not verify the domain → denied, `tid_mismatch`. An attacker who is Global Admin of their *own* tenant cannot pass this, because their tenant does not have `acme.com` verified.
3. Require `preferred_username`/`email` == caller's session email, same as Google (defence in depth; step 0 is the real identity binding).
4. Admin check, in order of preference:
   a. `wids` claim in the ID token (emitted when the app registration's group claims setting includes "Directory roles"). Require it to contain at least one of the **qualifying role template IDs** (§4.4).
   b. If `wids` is absent, call Graph `GET /me/memberOf/microsoft.graph.directoryRole` with delegated `RoleManagement.Read.Directory` (least-privileged per Graph docs) and match `roleTemplateId` the same way.
5. Issue grant.

Both: the step-up tokens (access/refresh) are discarded after the check. Nothing from the step-up is written to `settings` or any token table. The step-up uses its own OAuth client registration scopes; it must not widen the calendar consent.

### 4.4 Qualifying Entra roles (template IDs are immutable)

| Role | Template ID |
| -- | -- |
| Global Administrator | `62e90394-69f5-4237-9190-012177145e10` |
| Privileged Role Administrator | `e8611ab8-c189-46e8-94e1-60213ab1f814` |
| Application Administrator | `9b895d92-2cd3-44c7-9d02-a6ac2d5ea5c3` |
| Cloud Application Administrator | `158c047a-c907-4556-b7ef-446551a6b5f7` |

All four IDs verified 2026-10-21 against [learn.microsoft.com/entra/identity/role-based-access-control/permissions-reference](http://learn.microsoft.com/entra/identity/role-based-access-control/permissions-reference). `b79fbf4d-3ef9-4689-8143-76b194e85509` is the default "ordinary member" value present in every non-guest token and must **never** qualify. The list lives in code as a named constant table, not config.

### 4.5 Break-glass

`PACEDAY_ADMIN_EMAILS` read once at startup. Empty → feature off (this is fine: §4.3 lets a Google Workspace or Entra admin create an org's *first* provider without break-glass, since `hd` / `verifiedDomains` bind the domain without any pre-existing provider row). Matching is exact on the lower-cased session `email` claim.

A break-glass write additionally requires a **fresh session**: the session JWT's `iat` must be within the last 10 minutes (the frontend sends the user through login again if not). This keeps the §4.1 rule that no SSO-write privilege rides on a 7-day unrevocable bearer. The implementer verifies the JWT carries `iat`; if not, add it in the same change.

A break-glass write is audited with `via: "breakglass"` so it is visible that the IdP check was bypassed.

**Correctness dependency on** [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email): break-glass trusts the session `email`, which until PR [Enach/clockwise-like#172](https://linear.app/paceday/review/pac-45-fixauth-bind-the-oidc-asserted-email-to-the-providers-domain-ac3dd71bc31d) merges can be attacker-chosen via the OIDC callback. [PAC-50](https://linear.app/paceday/issue/PAC-50/sso-configuration-is-writable-by-any-employee-and-for-any-subdomain) must not ship with `PACEDAY_ADMIN_EMAILS` set on a build that lacks [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email). Release note it; AC-19 enforces it in code.

### 4.6 Audit (depends on [PAC-23](https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the))

Two new audit actions, string literals like the existing seven:

* `sso_stepup` — details `{org_id, provider, outcome: "granted"|"denied", reason}`; `reason` is a short enum (`no_hd`, `hd_mismatch`, `not_admin`, `tid_mismatch`, `tid_changed`, `email_mismatch`, `state_mismatch`, `token_invalid`), never the raw token.
* `sso_provider_written` — details `{org_id, provider_id, op: "create"|"update"|"delete", via: "stepup"|"breakglass"}`.

`WriteAuditLog` takes an explicit `userID` argument ([PAC-23](https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the) must provide this; the cron has no HTTP context and `UserIDFromContext` returns nil UUID rather than an error, so context-derived user ids are not acceptable here). Details are marshalled with `encoding/json`, not concatenated (the four concatenating writers are a known defect; do not add a fifth).

### 4.7 Exact-domain rule

`DomainMatchesOrg` becomes an exact, case-insensitive comparison of the email's domain part against `organizations.domain`. Grep for any other `strings.HasSuffix` on a domain and align it. No subdomain allowance — if a customer needs `eu.acme.com`, that is a second org or a later feature.

## 5. Acceptance criteria

* AC-1 An org member with a valid session but no grant and no break-glass entry gets 403 `sso_write_requires_admin_stepup` on create, update and delete. Storage is untouched (row count and `updated_at` unchanged). A break-glass email whose user has no `org_id` (or a different org) gets the same 403 — membership is checked before break-glass.
* AC-2 A grant for org A does not authorize a write on org B (403).
* AC-3 A grant is single-use: the second write with the same grant gets 403. Two writes fired concurrently with the same grant (goroutines, barrier): exactly one 2xx, one 403. A write that fails validation (400) after the gate also burns the grant: the retry gets 403.
* AC-4 A grant expires: with `engine.Clock` advanced 11 minutes past issue, the write gets 403; at 9 minutes it succeeds.
* AC-5 Google step-up with no `hd` claim → denied, audit `reason: no_hd`.
* AC-6 Google step-up with `hd` ≠ org domain → denied, `hd_mismatch`.
* AC-7 Google step-up where Directory `users.get` returns `isAdmin: false` **and** `isDelegatedAdmin: false`, or an error → denied, `not_admin`.
* AC-8 Google step-up with `hd` == domain and `isAdmin: true` → grant issued, subsequent write succeeds. Same with `isAdmin: false, isDelegatedAdmin: true`.
* AC-9 Microsoft step-up whose `/organization` fixture does not list the org domain in `verifiedDomains` → denied, `tid_mismatch`, and `organizations.ms_tenant_id` stays NULL. Same fixture *with* the domain verified → tenant pinned and grant issued. A later step-up from a different `tid` that also claims the domain → denied `tid_changed` unless break-glass.
* AC-10 Microsoft step-up whose `wids` contains only `b79fbf4d-3ef9-4689-8143-76b194e85509` → denied, `not_admin`.
* AC-11 Microsoft step-up whose `wids` contains Global Administrator → grant. Same with `wids` absent and Graph `memberOf` returning the GA role.
* AC-12 Every outcome in AC-5..AC-11 and every write in AC-8/AC-11 produces exactly one audit row with the caller's `user_id` ([PAC-23](https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the)) and the details shape in §4.6, parseable with `json.Unmarshal`.
* AC-13 Step-up ID token whose email ≠ session email → denied, `email_mismatch`, even when every other check passes. Callback with a state cookie whose `user_id` ≠ session `sub` → denied, `state_mismatch`. Callback with no session → 401, no audit row, no grant.
* AC-14 `PACEDAY_ADMIN_EMAILS=alice@acme.com`: alice with a session issued 5 minutes ago writes without a grant; audit says `via: breakglass`. Same with a session issued 11 minutes ago → 403 `sso_write_requires_fresh_login`. `ALICE@acme.com` in the env also matches (lower-cased). Unset env → alice is treated like any member (AC-1).
* AC-15 `DomainMatchesOrg("bob@evil-acme.com", "acme.com") == false`; `("bob@ACME.com", "acme.com") == true`.
* AC-16 Target org is the caller's `users.org_id`; a body carrying any `org_id` is ignored or rejected with 400, never honoured. A body whose provider `domain` ≠ the org's domain → 400 `provider_domain_mismatch`, no row written.
* AC-17 The storage layer used by the step-up handlers is a recording mock: after a full successful Google and a full successful Microsoft step-up, `SaveMicrosoftToken`, the Google token save, and any `settings` write have been called **zero** times; `calendar_provider` is unchanged.
* AC-18 The OpenAPI fragment for the three write operations documents the 403/400 codes and the `stepup_url` field; `scripts/openapi_assemble.py --check` passes.
* AC-19 Startup with `PACEDAY_ADMIN_EMAILS` non-empty on a build whose `oidcCallback` lacks the [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email) domain binding fails fast with a clear error. (Implement as a build-tag/const set by [[PAC-45] fix(auth): bind the OIDC-asserted email to the provider's domain](https://linear.app/paceday/review/pac-45-fixauth-bind-the-oidc-asserted-email-to-the-providers-domain-ac3dd71bc31d), or simply a test that asserts [[PAC-45] fix(auth): bind the OIDC-asserted email to the provider's domain](https://linear.app/paceday/review/pac-45-fixauth-bind-the-oidc-asserted-email-to-the-providers-domain-ac3dd71bc31d)'s function exists — the implementer picks the cheapest thing that cannot be forgotten.)
* AC-20 Migration 027 lower-cases existing `sso_providers.domain` and the unique constraint is on `lower(domain)`: upserting `ACME.com` when `acme.com` exists updates that row; `SELECT count(*)` stays 1.

All ACs are testable with `httptest` fixtures standing in for Google and Microsoft **because the IdP endpoints are config values (§4.1)**; the fixtures sign ID tokens with a test key served from the fixture's JWKS URL. The e2e suite gets one journey: "member saves SSO → is sent to step-up → stubbed IdP says admin → save lands".

## 6. Open questions for the challenger / human

* **OQ-1** Closed 2026-10-21 (Nicolas): `isDelegatedAdmin` **qualifies**, same as `isAdmin`. AC-7/AC-8 updated.
* **OQ-2** Closed in v2.1: tenant is bound by `verifiedDomains` evidence (§4.3 step 2), never pinned on first use. Remaining sub-question: is `Organization.Read.All` really required for `GET /organization?$select=verifiedDomains`, or does `User.Read` suffice? Graph docs list `Organization.Read.All` as least-privileged for the full resource; the implementer tests the narrower scope first.
* **OQ-3** Closed in v2.1: caller picks the provider button; the result is checked against the org domain, so no pre-existing provider row and no break-glass are needed for the first create.
* **OQ-4** Closed 2026-10-21: all four §4.4 template IDs verified.
* **OQ-5** Closed 2026-10-21 (v1 findings recovered from the Linear comment of 2026-09-17):
  1. *"AC-9's uniqueness claim can be violated by pre-existing differently-cased* `sso_providers.domain` *rows."* With zero users there are no such rows, but the migration must still make it impossible: migration 027 lower-cases existing `domain` values and replaces the raw `UNIQUE (domain)` with `UNIQUE (lower(domain))`; `UpsertSSOProvider` lower-cases on write. New **AC-20**.
  2. *"AC-1 doesn't state the outcome for a designated-but-orgless principal."* A break-glass email whose user has no `org_id` gets the same 403 as AC-1 — org membership (§4.2 step 2) is checked before the break-glass branch, so no third error reason exists. Folded into AC-1.
* **OQ-6** Closed in v2.1: grant is a table (`sso_stepup_grants`, migration 027) — needed for the atomic consume in §4.2.
* **OQ-7** Does the session JWT carry `iat`? §4.5 needs it.

## 7. Not doing (recorded so nobody re-proposes it)

* "First user of an org becomes owner" — rejected: domain squatting.
* "DNS TXT verification" — rejected for now: real work, not needed while Workspace/Entra admins can vouch directly; revisit if an Okta-only customer appears.
* "No API writes, config-file only" — rejected: break-glass covers recovery without giving up the UI.

## 8. Revision log

* v2 (2026-10-21 am): IdP-derived authorization replaces the four designation options; absorbs [PAC-46](https://linear.app/paceday/issue/PAC-46/createssoprovider-and-deletessoprovider-authorize-with-a-suffix-match).
* v2.1 (2026-10-21 pm): after an internal challenge (12 findings across [PAC-49](https://linear.app/paceday/issue/PAC-49/listuserswithautoschedule-does-not-filter-user-id-so-the-focus-cron-is)/50) — provider domain pinned to org domain (§4.2 step 3); tenant bound by `verifiedDomains`, not first use (§4.3); callback behind `requireAuth` with state/`sub` binding; atomic single-use grants with a stated 10-minute TTL; break-glass needs a fresh login and is gated on [PAC-45](https://linear.app/paceday/issue/PAC-45/authentication-bypass-the-oidc-callback-never-binds-the-asserted-email); IdP endpoints injectable so ACs are really offline-testable; AC-9/13/14/16/17/19/20 rewritten or added; v1 Challenge findings closed (OQ-5).

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
