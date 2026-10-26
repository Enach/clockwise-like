# Spec — PAC-68: Org membership is granted by email domain alone

- **Linear**: https://linear.app/paceday/issue/PAC-68/org-membership-is-granted-by-email-domain-alone-use-google-hd
- **Status**: draft
- **Author**: spec-author (local session, 2026-10-26)
- **Split from**: PAC-50 spec v2.1 §3 "Out of scope" (`docs/specs/PAC-50.md:64`), which names this work "verified org membership".
- **Depends on**: PAC-50 (not yet implemented on `main` at `2e1f392`). PAC-50 introduces the organisation's Microsoft tenant pin and the verified-domain evidence check (`docs/specs/PAC-50.md:126`, `:183`); AC-5 and AC-6 below assume they exist. PAC-45 (merged) for the SSO path, which this spec leaves alone.
- **Inherited scope**: none. The three sign-in callbacks this spec changes (`GET /api/auth/callback`, `GET /api/auth/microsoft/callback`; the OIDC callback is read but not changed) carry no `x-uncertain` marker (`contracts/openapi/paths/auth.yaml:64-200`; the only markers in that file are at `:713`, `:840`, `:901`, all on other operations). Their open audit rows API-008, API-011, API-012 and API-064 (`docs/factory/api-audit.md:242,245,246,298`) concern cookies, JWT issuance failure, Microsoft profile-fetch failure and `login_hint`; none is about org association. This spec does not modify those behaviours, so per factory §7 rule 1 they are listed for the contract author to confirm, not resolved here. If stage 2 changes the response of either callback (it should not need to), API-011/API-012 become in scope.

## 1. Problem

Paceday groups users into an **organisation** by the domain of their email address. Being in an organisation is not cosmetic: a member can list every other member's name and email address, and every member can create, change or delete the organisation's SSO configuration (today; PAC-50 narrows the latter to proven IdP admins but still requires membership first).

Today the only proof of membership is "your email address ends in `@acme.com`". For Google sign-in that is weaker than it looks: anyone can create a *consumer* Google account on an address they receive mail at, and organisations that let people mint addresses on their domain (universities, ISPs, hosting providers, any company with a shared or catch-all mailbox, a former employee whose alias still forwards) hand out membership to whoever holds such an address. For Microsoft sign-in, the email comes from a profile field that the administrator of *any* Microsoft tenant can set on their own users; an attacker who owns a tenant can give one of their users the address `someone@acme.com` and be joined to `acme.com`'s organisation.

The cost: a stranger becomes a member of someone else's organisation, sees its member directory, and — until PAC-50 lands — can rewrite the identity provider configuration everyone in that organisation logs in through. Production has zero users (`docs/specs/PAC-50.md:26`), so the fix is cheap now and expensive later.

Both identity providers already publish evidence that is stronger than the email: Google tells a relying party which Workspace / Cloud Identity domain an account is managed by (the hosted-domain claim, absent for consumer accounts; Google's documentation states the email domain alone is insufficient), and Microsoft identifies the tenant an account belongs to, and the tenant can be asked which domains it has proven ownership of by DNS. PAC-50 already relies on both for admin step-up.

## 2. Current behaviour

Verified against `main` at `2e1f392` (2026-10-26).

**Association is by email domain and creates the org on demand.** `AssociateUserWithOrg` (`backend/storage/orgs.go:40-52`) extracts the email's domain (lower-cased, `backend/domain/domain.go:16-22`), returns without doing anything for an empty domain or one of 21 hard-coded consumer domains (`domain.go:5-14`, `orgs.go:44-46`), otherwise upserts an `organizations` row keyed by that domain (`orgs.go:19-28`, `ON CONFLICT (domain) DO UPDATE SET name`) and sets the user's org to it (`orgs.go:35-38`, an unconditional `UPDATE users SET org_id`).

**It runs on every sign-in, not only at sign-up**, on all three paths, and its error is discarded each time:
- Google: `backend/api/handlers_auth.go:76`, after the user upsert at `:63`.
- Microsoft: `backend/api/handlers_microsoft_auth.go:76`, after the user upsert at `:68`.
- Generic OIDC SSO: `backend/api/handlers_sso.go:191`, after PAC-45's email-to-provider-domain check at `:181-184`.

**No organisation evidence from the IdP is read on either social path.**
- Google: the login requests only the Calendar scope and `userinfo.email` (`backend/auth/google_oauth.go:16-19`; no `openid` scope, so no ID token is requested). Identity comes from the v2 userinfo endpoint (`handlers_auth.go:171-193`), decoded into a struct with `id`, `email`, `name`, `picture` only (`handlers_auth.go:164-169`); the hosted-domain field and the email-verified flag are not read.
- Microsoft: the login requests `Calendars.ReadWrite`, `User.Read`, `offline_access` (`backend/auth/microsoft_oauth.go:30`; no `openid`, so no ID token and no tenant claim). Identity comes from Graph `/me?$select=id,mail,displayName` (`handlers_microsoft_auth.go:89-108`); the email is the `mail` profile attribute, which a tenant's own administrator sets. No tenant id is read and no organisation lookup is made.

**No tenant pin exists yet.** `organizations` has `id, name, domain, created_at` (`orgs.go:11-16`; `backend/storage/migrations/007_organizations.up.sql`). The `ms_tenant_id` column and the verified-domain check are PAC-50 design (`docs/specs/PAC-50.md:126`, `:183`), not code.

**A user's email cannot change after creation**, because the user row is upserted on `email` (`backend/storage/users.go:21-35`, `ON CONFLICT (email)`), so for any given user the derived org domain is fixed; the unconditional `SET org_id` on each sign-in therefore re-writes the same org, never a different one.

**What membership grants today.**
- `GET /api/org/members` (`backend/api/routes.go:132`, `backend/api/handlers_org.go:16-40`): every member's id, email, name, avatar and provider (`orgs.go:55-77`). A user with no org gets `[]`.
- SSO admin writes and listing require an org and act on the caller's org (`handlers_sso.go:201-206`, `:269-274`, `:291-298`).
- Teams carry an org id (`backend/storage/teams.go:103,114`).

**Tests protecting this behaviour: none.** No Go test calls `AssociateUserWithOrg`, `SetUserOrg` or `GetOrgMembers`; `UpsertOrg` appears only as fixture setup in `backend/storage/sso_providers_test.go:116`. `handlers_auth_test.go` covers status, disconnect, start-OAuth and the invalid-state callback only (`docs/factory/api-audit.md:326`, API-092). `domain_test.go` covers `IsGenericDomain` and `DeriveOrgName`. No e2e scenario touches organisations. This absence is itself a finding: §6 lists what the implementer must pin with new tests before changing anything.

## 3. Desired behaviour

Signing in with Google or Microsoft still creates or finds the user and signs them in exactly as today. What changes is only whether that sign-in **adds** the user to the organisation for their email's domain.

- **Google.** The user joins (or, if none exists yet, creates) the organisation for their email's domain only when Google states that the account is managed by a Workspace / Cloud Identity domain **and** that domain is exactly the email's domain. A consumer Google account, an account managed by a different domain, and an account whose managed domain differs from its email's domain (for example a secondary or alias domain of a Workspace) are signed in without an organisation. Google's statement that the email is verified is required as well.
- **Microsoft.** The user joins (or creates) the organisation only when the tenant the account signed in from is shown, by Microsoft, to have proven ownership of the email's domain. If the organisation is already pinned to a tenant (PAC-50), the account's tenant must be that tenant; a different tenant is not joined even if it also lists the domain. Personal Microsoft accounts are not joined.
- **Generic OIDC SSO.** Unchanged. PAC-45 already binds the asserted email to the domain whose provider was configured (`handlers_sso.go:177-184`).
- **When the evidence is missing, negative, or cannot be obtained** (the provider call fails or times out), the sign-in succeeds and the user is simply not added. Membership is never granted on a fallback to the email domain.
- **An unverified sign-in never creates an organisation.** The organisation for `acme.com` comes into existence only on the first verified sign-in for `acme.com`.
- **Existing memberships are kept.** A sign-in that does not prove membership leaves whatever organisation the user already has unchanged. This spec never removes a user from an organisation (see Decisions D-1).
- The user is not told anything new on sign-in. Not being in an organisation is a normal state (consumer-domain users are in it today), and the existing "no organisation" behaviour of every org-scoped feature applies.

## 4. Acceptance criteria

Each criterion is provable offline: the identity provider's answers are injected, not fetched. "Sign-in" means the provider callback completes successfully. "Not joined" means: the user exists, can use Paceday, belongs to no organisation, and the org-member listing for them returns an empty list.

**Google**

AC-1. Given no organisation exists for `acme.com`, and a Google account `alice@acme.com` whose provider answer has a verified email and **no** hosted-domain value, when it signs in for the first time, then the user is signed in, belongs to no organisation, and no organisation for `acme.com` exists afterwards.  [unit]

AC-2. Given an organisation for `acme.com` with one existing member, and a Google account `mallory@acme.com` whose hosted-domain value is `other.com`, when it signs in, then it is not joined, and the existing member's org-member listing still contains exactly one entry.  [unit]

AC-3. Given a Google account `bob@acme.co.uk` whose hosted-domain value is `acme.com` (a secondary domain of the `acme.com` Workspace), when it signs in, then it is joined to no organisation — neither `acme.co.uk` nor `acme.com` — and no organisation is created.  [unit]

AC-4. Given a Google account `carol@acme.com` whose hosted-domain value is `acme.com` (any letter case) and whose email is verified, when it signs in, then it belongs to the organisation for `acme.com`, creating it if it did not exist. The same account with the email-verified flag false or absent is not joined.  [unit]

**Microsoft**

AC-5. Given an organisation for `acme.com` with no tenant pin, and a Microsoft account `dave@acme.com` whose tenant's verified-domain answer does **not** contain `acme.com`, when it signs in, then it is not joined, and the organisation still has no tenant pin.  [unit]

AC-6. Given an organisation for `acme.com` pinned to tenant T1, and a Microsoft account `erin@acme.com` from tenant T2 whose verified-domain answer **does** contain `acme.com`, when it signs in, then it is not joined and the pin is still T1.  [unit]

AC-7. Given an organisation for `acme.com` pinned to tenant T1 (or not yet pinned, with the tenant's verified-domain answer containing `acme.com`), and a Microsoft work account `frank@acme.com` from T1, when it signs in, then it belongs to the `acme.com` organisation; an absent organisation is created. This criterion does not change the pin (see D-3).  [unit]

AC-8. Given a personal Microsoft account (consumer tenant) with mail `gina@acme.com`, when it signs in, then it is not joined and no organisation is created.  [unit]

**Failure of the evidence lookup**

AC-9. Given the provider's organisation-evidence lookup errors or times out (Google: hosted-domain value unavailable; Microsoft: tenant or verified-domain lookup fails), when an `@acme.com` account signs in, then the sign-in still completes with the same redirect and session as a successful sign-in today, and the user is not joined.  [unit]

**Existing memberships (decision D-1)**

AC-10. Given a user `henry@acme.com` who already belongs to the `acme.com` organisation, when he signs in with Google and the answer carries no hosted-domain value (or with Microsoft and the tenant does not verify the domain), then he still belongs to the `acme.com` organisation afterwards and the org-member listing still includes him.  [unit]

AC-11. Given the change is deployed onto a database containing users with an organisation, when the deployment completes and before any of them signs in again, then every user's organisation is the same as before deployment and no organisation row has been removed.  [unit]

**Unchanged paths**

AC-12. Given a generic OIDC SSO provider configured for `acme.com`, when `ivan@acme.com` signs in through it, then he belongs to the `acme.com` organisation exactly as before this change.  [unit]

AC-13. Given an account on a consumer email domain (e.g. `@gmail.com`) on either social provider, when it signs in, then it belongs to no organisation and none is created, as today.  [unit]

**End to end**

AC-14. Given the full stack with a sign-in stand-in for Google, when a Google account with no hosted-domain value signs in and then opens the page that shows organisation members, then the page shows no other member, and when a second account with a matching hosted-domain value for the same domain signs in, it sees only itself.  [e2e] — the e2e suite cannot drive a real sign-in today (OQ-4); if the plan does not add one, it drops this criterion with that reason and AC-1/AC-4 at unit level are the proof.

## 5. Explicitly out of scope

- **Re-evaluating or removing existing memberships.** No sweep, no removal on a failed sign-in (D-1). A later issue may add a re-verification sweep; the issue body proposes the same.
- **Joining the hosted domain's organisation from a secondary domain** (AC-3's `acme.co.uk` user joining `acme.com`). That widens who is in an organisation and has no owner decision; it is a possible follow-up.
- **Admin roles, invitations, or manual add/remove of members.** No membership UI exists and none is added.
- **The Microsoft email-identity problem beyond organisations.** See OQ-1: the user account itself is looked up by an email a foreign tenant's admin can set. This spec stops that from granting org membership; it does not change how accounts are matched at sign-in.
- **Generic OIDC SSO membership rules** — unchanged, covered by PAC-45.
- **Setting or changing the Microsoft tenant pin.** Owned by PAC-50's admin step-up (D-3).
- **Any change to org-scoped features** (member listing, SSO admin, teams). Their "no organisation" behaviour is reused as-is.

## 6. What must not change

- Sign-in itself on both social providers: same user creation/upsert, same stored calendar credential, same session cookie, same redirect target (`handlers_auth.go:62-79`, `handlers_microsoft_auth.go:62-79`). **No test protects the success path of either callback today** (only invalid-state, `handlers_auth_test.go`); the plan must add a characterisation test for each before changing them.
- Consumer-domain accounts never join or create an organisation (`orgs.go:44-46`). Protected only indirectly by `TestIsGenericDomain` (`backend/domain/domain_test.go:24`); no test covers the association path.
- The generic OIDC SSO path's membership behaviour (`handlers_sso.go:191`). No test covers it (API-092).
- Org name derivation for a newly created org (`DeriveOrgName`, `domain.go`; `TestDeriveOrgName`, `domain_test.go:87`).
- No user loses an organisation, and no organisation row is deleted, by deployment or by sign-in (AC-10, AC-11). No test covers this today.
- The org-member listing's response for a user with no organisation (`[]`, `handlers_org.go:26-28`). No test covers it.

## 7. Rollback

Reverting the code restores today's behaviour: the next sign-in of any non-consumer-domain user joins (or creates) the org for their email domain again. That re-opens the defect, so a revert is a security regression and is release-noted as such. Memberships granted while this change was live are verified ones and remain valid after a revert; users who were left out will be joined on their next sign-in after the revert.

This spec needs no schema change of its own (the tenant pin is PAC-50's). If the plan nonetheless adds one, its down migration must not delete users, organisations or memberships.

## 8. Open questions

- **OQ-1 — Microsoft account matching by attacker-settable email (security finding, larger than this spec).** The Microsoft callback upserts the user by Graph's `mail` (`handlers_microsoft_auth.go:62-68`, `users.go:25`), which any tenant's administrator can set on their own users. An attacker who owns any tenant can therefore sign in *as an existing Paceday user* whose email they copy, regardless of organisations. This spec only closes the membership consequence. **Owner**: Nicolas Hochart (human) — decide whether to file a separate high-priority issue. **Resolve by**: filing it, or recording why not, before PAC-68 reaches stage 4.
- **OQ-2 — Which provider evidence, and does obtaining it change the consent screen?** Google's hosted-domain value is available from the ID token (requires requesting `openid`) and, per Google's userinfo documentation, from the userinfo response already fetched; Microsoft's tenant id needs an ID token (`openid`) or Graph, and the verified-domain list needs Graph's organisation resource, possibly with a broader permission than `User.Read` (same sub-question as PAC-50 OQ-2, `docs/specs/PAC-50.md:273`). A broader Microsoft permission may require tenant-admin consent and could block sign-in for users in tenants that restrict consent. **Owner**: contract-author / plan-author, with a human decision if a new admin-consent permission is needed. **Resolve by**: the plan naming the scopes, and the PAC-50 implementer's answer to its OQ-2.
- **OQ-3 — PAC-50 ordering.** AC-6 and AC-7 depend on PAC-50's tenant pin existing. If PAC-68 is scheduled first, either AC-6 is deferred or PAC-68 introduces the pin column itself (and PAC-50 then reuses it). **Owner**: orchestrator / human. **Resolve by**: Linear ordering before stage 2; the issue body already states PAC-50 lands first.
- **OQ-4 — Can e2e reach the sign-in callback at all?** Today it cannot: the e2e suite logs in by minting the session JWT directly and setting the cookie (`e2e/auth.ts:17-25`, which names the untested OAuth callback as a known gap), and the Google stand-in (`e2e/fake.ts:1-7`, `e2e/fake-google/`) serves the calendar API, not sign-in. AC-14 therefore needs either a sign-in stand-in that can vary the hosted-domain and tenant evidence, or to be dropped with this reason, leaving AC-1–AC-13 at unit level as the proof. **Owner**: e2e-author / plan-author. **Resolve by**: the plan.
- **OQ-5 — Should a verified sign-in move an un-joined user into the organisation later?** Under this spec, yes: a user left out on one sign-in (AC-1) joins on a later verified one (AC-4 pattern), because evaluation happens on every sign-in as it does today. Stated so the challenger can contest it; no owner decision exists, and it does not widen visibility beyond verified members.

## Decisions — 2026-10-26

*Made at stage 1 (spec-author). None had a recorded owner answer; each is the most conservative option consistent with PRODUCT.md (`smart-calendar-flow/PRODUCT.md:60-64`, "Privacy behaviours … must not weaken") and the earlier owner decisions in `docs/specs/PAC-50.md` §2, and each is **assumed by the factory, pending owner confirmation**.*

- **D-1 — Existing memberships are not deleted.** A sign-in that fails or cannot obtain verification leaves the user's current organisation unchanged; deployment re-evaluates nobody; nothing in this spec removes a user from an organisation or deletes an organisation. Assumed by the factory, pending owner confirmation. (The factory rules forbid choosing an option that deletes user data; the issue body proposes the same; production has zero users, so the population of unverified legacy members is empty in practice.)
- **D-2 — Only an exact hosted-domain / verified-domain match on the email's own domain joins.** Secondary-domain Workspace users join no organisation rather than the primary domain's (AC-3). Assumed by the factory, pending owner confirmation. (Narrowest membership; joining another domain's org would widen who sees its member list.)
- **D-3 — Sign-in never writes the Microsoft tenant pin.** Only PAC-50's admin step-up sets it. When no pin exists, a sign-in is checked against the tenant's verified-domain answer each time; when a pin exists, the tenant must equal it, matching PAC-50's `tid_changed` stance (`docs/specs/PAC-50.md:126`, NB5). Assumed by the factory, pending owner confirmation. (Keeps a single, admin-proven writer of the pin.)
- **D-4 — Missing or failed evidence fails closed for membership, open for sign-in.** The user is signed in without an organisation rather than refused (AC-9). Assumed by the factory, pending owner confirmation. (Refusing sign-in would lock out users for a provider outage; granting membership would reopen the defect; "no organisation" is an existing supported state.)
