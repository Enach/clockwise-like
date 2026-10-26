# Spec — PAC-68: Org membership is granted by email domain alone

- **Linear**: https://linear.app/paceday/issue/PAC-68/org-membership-is-granted-by-email-domain-alone-use-google-hd
- **Status**: challenged (round 1 answered in "Revision 1"; awaiting re-challenge)
- **Author**: spec-author (local session, 2026-10-26)
- **Split from**: PAC-50 spec v2.1 §3 "Out of scope" (`docs/specs/PAC-50.md:64`), which names this work "verified org membership".
- **Depends on**: PAC-50 (not yet implemented on `main` at `2e1f392`). PAC-50 introduces the organisation's Microsoft tenant pin and the verified-domain evidence check (`docs/specs/PAC-50.md:126`, `:183`); AC-5 and AC-6 below assume they exist. PAC-45 (merged) for the SSO path, which this spec leaves alone.
- **Inherited scope**: none. The two sign-in callbacks this spec changes (`GET /api/auth/callback`, `GET /api/auth/microsoft/callback`; the OIDC callback is read but not changed) carry no `x-uncertain` marker (`contracts/openapi/paths/auth.yaml:64-200`; the only markers in that file are at `:713`, `:840`, `:901`, all on other operations). Their open audit rows API-008, API-011, API-012 and API-064 (`docs/factory/api-audit.md:242,245,246,298`) concern cookies, JWT issuance failure, Microsoft profile-fetch failure and `login_hint`; none is about org association. This spec does not modify those behaviours, so per factory §7 rule 1 they are listed for the contract author to confirm, not resolved here. If stage 2 changes the response of either callback (it should not need to), API-011/API-012 become in scope.

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
- Microsoft: `backend/api/handlers_microsoft_auth.go:76`, after the user upsert at `:67`.
- Generic OIDC SSO: `backend/api/handlers_sso.go:191`, after PAC-45's email-to-provider-domain check at `:181-184`.

**No organisation evidence from the IdP is read on either social path.**
- Google: the login requests only the Calendar scope and `userinfo.email` (`backend/auth/google_oauth.go:16-19`; no `openid` scope, so no ID token is requested). Identity comes from the v2 userinfo endpoint (`handlers_auth.go:171-193`), decoded into a struct with `id`, `email`, `name`, `picture` only (`handlers_auth.go:164-169`); the hosted-domain field and the email-verified flag are not read.
- Microsoft: the login requests `Calendars.ReadWrite`, `User.Read`, `offline_access` (`backend/auth/microsoft_oauth.go:30`; no `openid`, so no ID token and no tenant claim). Identity comes from Graph `/me?$select=id,mail,displayName` (`handlers_microsoft_auth.go:89-108`); the email is the `mail` profile attribute, which a tenant's own administrator sets. No tenant id is read and no organisation lookup is made.

**No tenant pin exists yet.** `organizations` has `id, name, domain, created_at` (`orgs.go:11-16`; `backend/storage/migrations/007_organizations.up.sql`). The `ms_tenant_id` column and the verified-domain check are PAC-50 design (`docs/specs/PAC-50.md:126`, `:183`), not code.

**A user's email cannot change after creation**, because the user row is upserted on `email` (`backend/storage/users.go:21-35`, `ON CONFLICT (email)`), so for any given user the derived org domain is fixed; the unconditional `SET org_id` on each sign-in therefore re-writes the same org, never a different one.

**Accounts are matched by email alone, across providers.** The conflict key is `email` only; on conflict the row's `name`, `avatar_url` and `provider_id` are overwritten but `provider` is not (`users.go:21-31`). Both callbacks pass the provider's email straight into that upsert (`handlers_auth.go:63`, `handlers_microsoft_auth.go:67`) and then attach the new provider token to the returned row (`handlers_auth.go:70`, `handlers_microsoft_auth.go:72`). So whoever signs in with *any* Google or Microsoft account carrying an address already in `users` receives that existing row, its session and its organisation, whichever provider the row was created through. See OQ-1.

**What membership grants today.**
- `GET /api/org/members` (`backend/api/routes.go:132`, `backend/api/handlers_org.go:16-40`): every member's id, email, name, avatar and provider (`orgs.go:55-77`). A user with no org gets `[]`.
- SSO admin writes and listing require an org and act on the caller's org (`handlers_sso.go:201-206`, `:269-274`, `:291-298`).
- Teams have an org id column, read at `backend/storage/teams.go:103,114`, but no code path writes it (`CreateTeam` inserts only `name, created_by`, `teams.go:51-57`). Teams are not a consumer of membership today.

**Tests protecting this behaviour: none.** No Go test calls `AssociateUserWithOrg`, `SetUserOrg` or `GetOrgMembers`; `UpsertOrg` appears only as fixture setup in `backend/storage/sso_providers_test.go:116`. `handlers_auth_test.go` covers status, disconnect, start-OAuth and the invalid-state callback only (`docs/factory/api-audit.md:326`, API-092). `domain_test.go` covers `IsGenericDomain` and `DeriveOrgName`. No e2e scenario touches organisations. This absence is itself a finding: §6 lists what the implementer must pin with new tests before changing anything.

## 3. Desired behaviour

Signing in with Google or Microsoft still creates or finds the user and signs them in exactly as today. What changes is only whether that sign-in **adds** the user to the organisation for their email's domain.

- **Google.** The user joins (or, if none exists yet, creates) the organisation for their email's domain only when Google states that the account is managed by a Workspace / Cloud Identity domain **and** that domain is exactly the email's domain. A consumer Google account, an account managed by a different domain, and an account whose managed domain differs from its email's domain (for example a secondary or alias domain of a Workspace) are signed in without an organisation. Google's statement that the email is verified is required as well.
- **Microsoft.** The user joins (or creates) the organisation only when the tenant the account signed in from is shown, by Microsoft, to have proven ownership of the email's domain. If the organisation is already pinned to a tenant (PAC-50), the account's tenant must be that tenant; a different tenant is not joined even if it also lists the domain. Personal Microsoft accounts are not joined.
- **Generic OIDC SSO.** Unchanged. PAC-45 already binds the asserted email to the domain whose provider was configured (`handlers_sso.go:177-184`).
- **When the evidence is missing, negative, or cannot be obtained** (the provider call fails or times out), the sign-in succeeds and the user is simply not added. Membership is never granted on a fallback to the email domain.
- **An unverified sign-in never creates an organisation.** The organisation for `acme.com` comes into existence only on the first verified sign-in for `acme.com`.
- **Existing memberships are kept.** A sign-in that does not prove membership leaves whatever organisation the user already has unchanged. This spec never removes a user from an organisation (see Decisions D-1).
- **Limit: membership through an address a member already holds is not closed by this spec.** Because accounts are matched by email alone (§2), an unverified sign-in carrying an address that already belongs to a member (a consumer Google account created on a forwarded or catch-all `henry@acme.com`; a foreign-tenant Microsoft user whose mail is set to `henry@acme.com`; either provider against a row created through the other) lands in that member's existing account, and with it that member's organisation, which D-1 keeps. That is an account-matching problem, not a membership-grant problem: what this spec guarantees is that no sign-in *adds* an organisation without verified evidence. The account-matching defect is OQ-1, whose owner decision is a precondition for stage 4.
- **Two tenants claiming one domain before any pin.** Microsoft does not allow two tenants to hold the same verified domain at the same time, so the "not yet pinned" branch of the Microsoft rule cannot admit two different tenants concurrently for one domain.
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

AC-9. Given an organisation-evidence lookup that is **separate from the identity answer** errors or times out (Microsoft: the tenant's verified-domain lookup), when an `@acme.com` account signs in, then the sign-in still completes with the same redirect and session as a successful sign-in today, and the user is not joined. Evidence that arrives on the same answer as the email (Google's hosted-domain value; Microsoft's tenant identifier if taken from the token response) has no separate failure: when absent it is AC-1/AC-8, and when the identity answer itself fails, sign-in fails exactly as it does today (`handlers_auth.go:56-60`, `handlers_microsoft_auth.go:62-66`).  [unit]

**Existing memberships (decision D-1)** — AC-11 is a regression guard (passes before and after) and is meaningful only if the plan adds a migration.

AC-10. Given a user `henry@acme.com` who already belongs to the `acme.com` organisation, when he signs in with Google and the answer carries no hosted-domain value (or with Microsoft and the tenant does not verify the domain), then he still belongs to the `acme.com` organisation afterwards and the org-member listing still includes him.  [unit]

AC-11. Given the change is deployed onto a database containing users with an organisation, when the deployment completes and before any of them signs in again, then every user's organisation is the same as before deployment and no organisation row has been removed.  [unit]

**Unchanged paths** — regression guards: AC-12 and AC-13 pass today and must still pass; they do not count toward proving the fix.

AC-12. Given a generic OIDC SSO provider configured for `acme.com`, when `ivan@acme.com` signs in through it, then he belongs to the `acme.com` organisation exactly as before this change.  [unit]

AC-13. Given an account on a consumer email domain (e.g. `@gmail.com`) on either social provider, when it signs in, then it belongs to no organisation and none is created, as today.  [unit]

**End to end**

AC-14. Given the full stack with a sign-in stand-in for Google, when a Google account with no hosted-domain value signs in as `x@acme.com` through the running API and then requests the API's organisation-member listing with the session it received, then the listing is empty; and when a second account `y@acme.com` whose hosted-domain value is `acme.com` signs in the same way and requests the listing, the listing contains exactly that second account and not the first.  [e2e] — observed at the API, not through a page: the web app has no page that shows organisation members and this spec adds none (§5). The e2e suite cannot drive a real sign-in today (OQ-4); if the plan does not add a sign-in stand-in, it drops this criterion with that reason and AC-1/AC-4 at unit level are the proof.

**Consent (decision D-5)**

AC-15. Given a Microsoft work account in a tenant that lets its users consent only to the permissions Paceday's Microsoft sign-in requests today, when it signs in, then the sign-in completes as today; the user is joined only if evidence obtainable under those permissions verifies the domain, and otherwise is not joined.  [unit]

## 5. Explicitly out of scope

- **Re-evaluating or removing existing memberships.** No sweep, no removal on a failed sign-in (D-1). A later issue may add a re-verification sweep; the issue body proposes the same.
- **Joining the hosted domain's organisation from a secondary domain** (AC-3's `acme.co.uk` user joining `acme.com`). That widens who is in an organisation and has no owner decision; it is a possible follow-up.
- **Admin roles, invitations, or manual add/remove of members.** No membership UI exists and none is added.
- **Account matching by email, on both social providers and across them.** See OQ-1 and the "Limit" bullet in §3: the user account is looked up by email alone, so an unverified sign-in carrying an address a member already holds lands in that member's account and keeps its organisation. This spec stops a sign-in from *adding* membership without evidence; it does not change how accounts are matched, and so does not close membership reached through an existing member's address.
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

- **OQ-1 — Account matching by email alone, on both social providers and across them (security finding, larger than this spec).** Users are upserted with `email` as the only conflict key and `provider` never overwritten (`users.go:21-31`); both callbacks feed the provider's email into it (`handlers_auth.go:63`, `handlers_microsoft_auth.go:67`) and attach the new token to the row returned (`:70`, `:72`). Three cases follow, each a sign-in *as an existing Paceday user*, with that user's session, calendar credential and organisation: (a) **Google mailbox holder**: a person who receives mail at `henry@acme.com` (catch-all, forwarding alias, former employee) creates a consumer Google account on it and signs in, getting henry's row although Google reports no hosted domain; (b) **Microsoft foreign tenant**: the administrator of any tenant sets a user's `mail` to `henry@acme.com` and signs in through the `/common` endpoint; (c) **cross-provider**: either of the above lands in henry's row even when henry's row was created through the other provider (henry joined via Google Workspace, the attacker arrives via Microsoft, or the reverse). This spec closes only the *grant* of membership by an unverified sign-in; it does not close membership reached through an address already held by a member (§3 "Limit", §5). Narrowing AC-10 is not a remedy, because D-1 forbids removing memberships. **Owner**: Nicolas Hochart (human) — decide whether to file a separate high-priority issue. **Resolve by**: filing it (covering cases a-c), or recording why not, before PAC-68 reaches stage 4.
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
- **D-5 — Sign-in does not come to depend on a new consent.** If the verified-domain evidence needs a Microsoft permission beyond what sign-in requests today, sign-in must not start requiring it; a user whose tenant does not grant it signs in and is not joined (AC-15). Added in Revision 1. Assumed by the factory, pending owner confirmation. (Preserves §6's first bullet; locking users out of sign-in is a larger change than this spec's problem justifies; "not joined" is the conservative outcome.)

## Challenge — 2026-10-26

*spec-challenger, against commit `4396c35` on `factory/pac-68-spec` (committed state only). Citations checked against `main` at `2e1f392`.*

**Verdict**: accept-with-changes

### Blocking

1. **Email-keyed sign-in means an unverified sign-in can still land in a verified member's account, with its membership. §3 says this never happens, and OQ-1 limits the problem to Microsoft.** The user row is upserted on `email` alone. `provider` is neither part of the key nor overwritten (`backend/storage/users.go:21-31`, `ON CONFLICT (email) DO UPDATE SET name, avatar_url, provider_id`). Take the spec's own §1 threat for Google: a person who receives mail at `henry@acme.com` (catch-all, a forwarding alias, an ex-employee) creates a *consumer* Google account on it. When they sign in, the Google callback returns henry's existing user row (`handlers_auth.go:63`). AC-10 then requires that row to keep the `acme.com` organisation. Cross-provider works the same way: a foreign-tenant Microsoft user whose `mail` is `henry@acme.com` lands in henry's row even if henry joined through Google Workspace. So for every address already held by a member, §3's "Membership is never granted on a fallback to the email domain" is contradicted by the code, and §1's "a stranger becomes a member" stays open. OQ-1 names only the Microsoft path ("Microsoft account matching by attacker-settable email"), so the owner it asks would decide on an understated finding. **Resolves when** (a) OQ-1 is restated to cover account matching by email on *both* social providers and across providers, with the Google mailbox-holder case written out; and (b) §3 or §5 says explicitly that this spec does not stop membership through an address that already belongs to a member, and points to OQ-1. Narrowing AC-10 is not an option: the most conservative fix must not remove memberships (D-1). The OQ-1 owner decision stays a precondition for stage 4, as already written.

2. **AC-14 asks for a web page that does not exist, and §5 rules out adding one.** AC-14 says the account "opens the page that shows organisation members". `smart-calendar-flow/src` has no such page and no caller of `GET /api/org/members`. The only references are the generated types (`src/api/generated/types.ts:1546`, `src/api/generated/schemas.types.d.ts:4052,5130`). §5 says "Any change to org-scoped features … none is added". This is true independently of OQ-4: a sign-in stand-in would not create the page. **Resolves when** AC-14 states what it observes at the API (the member listing each signed-in account gets back from the running stack) instead of through a page. Alternatively, the spec drops AC-14 with this reason added to OQ-4's.

### Non-blocking

1. **The Google half of AC-9 is either AC-1 or impossible.** §3 and OQ-2 locate Google's hosted-domain value in the ID token or in the userinfo response the callback already fetches. In both cases it arrives on the same call that yields the email. Today, if that call fails, sign-in fails before any user exists (`handlers_auth.go:56-60`, 500 "userinfo failed"). §6 keeps that behaviour unchanged. "Hosted-domain value unavailable" is therefore either "field absent" (AC-1) or a failure that already prevents sign-in, so there is no session to keep. The Microsoft half has the same problem if the tenant id comes from the token response. AC-9 should name which lookups are *separate* from the identity answer and may fail on their own (for Microsoft, the verified-domain lookup). It should also say that a failure of the identity answer itself keeps today's behaviour. Otherwise two test authors will write different tests.
2. **Sign-in must not come to depend on a new consent.** Under D-3, an unpinned organisation is checked against the tenant's verified domains on *every* Microsoft sign-in. If that needs a permission beyond `User.Read` (OQ-2; PAC-50 names `Organization.Read.All`), the sign-in request carries it, and users in tenants that restrict consent can no longer sign in at all. That breaks §6's first bullet. OQ-2 records the risk but no criterion pins it. Add a criterion: a Microsoft user whose tenant grants only today's permissions still signs in and is simply not joined. If the owner accepts that sign-in may break, record that decision instead.
3. **AC-11, AC-12 and AC-13 already pass today.** They guard against regressions and do not demonstrate the change. That is legitimate for "what must not change", but the spec should label them as guards so nobody counts them toward proving the fix. AC-11 is only meaningful if the plan adds a migration (§7 allows one).
4. **The preamble contradicts itself on the OIDC callback.** It says "The three sign-in callbacks this spec changes", then lists two and says the OIDC callback "is read but not changed". It should say two.
5. **Issue wording versus spec.** The issue's draft acceptance uses "signs up" and "existing users are not re-evaluated". The spec evaluates on every sign-in and keeps memberships (OQ-5, D-1). The result is consistent with the issue: re-evaluation can only add a verified membership, never remove one. Recording this so the stage-2 author does not read "at signup" as a constraint.
6. **Two tenants, no pin.** Nothing in AC-5–AC-8 covers two different Microsoft tenants each claiming the same domain before any pin exists. Microsoft does not let two tenants verify one domain at the same time, so the gap is acceptable. One sentence in §3 saying so would make AC-7's "not yet pinned" branch stand on its own.

### Criteria I could not falsify

- AC-9 (Google half): see non-blocking 1. No injected answer separates "hosted-domain value unavailable" from AC-1's "no hosted-domain value" without also removing the email that sign-in needs.
- AC-14: no test can observe "the page that shows organisation members" because no such page exists (blocking 2).
- AC-11, AC-12, AC-13: these pass before and after the change. They are regression guards, not proof of the change (non-blocking 3).

AC-1 to AC-8 and AC-10 are falsifiable as written. Each one fails today, because `AssociateUserWithOrg` joins or creates by email domain on every path (`orgs.go:42-52`), and passes once the evidence rule exists.

### Citations I checked and found wrong

- "Microsoft: `handlers_microsoft_auth.go:76`, after the user upsert at `:68`" — the upsert is at `:67` (`:68` is its error check). OQ-1's `:62-68` covers it.
- "Teams carry an org id (`backend/storage/teams.go:103,114`)" — those lines are row-scan assignments. No code path ever writes `teams.org_id`: `CreateTeam` inserts only `name, created_by` (`teams.go:51-57`). Teams are therefore not a consumer of membership today. The claim is not false, but it implies a dependency that does not exist.

I checked these and found them correct: `orgs.go:11-52` (upsert on domain, unconditional `SET org_id`, generic-domain early return); `domain.go:5-22` (21 generic domains, lower-casing); Google scopes `google_oauth.go:16-19` with no `openid`; the userinfo struct `handlers_auth.go:164-169` without `hd`/`verified_email`; Microsoft scopes `microsoft_oauth.go:30` with no `openid`, plus the `/common` endpoint (`:15-16`), so personal accounts reach this path and AC-8 is reachable; Graph `/me?$select=id,mail,displayName` (`handlers_microsoft_auth.go:91`); the SSO domain binding `handlers_sso.go:181-184` and association `:191`; the SSO admin org checks `:203`, `:269`, `:293`; `GET /api/org/members` `routes.go:132`, `handlers_org.go:26`; migration `007_organizations.up.sql` (no tenant column; `ON DELETE SET NULL`); the `x-uncertain` markers in `auth.yaml` only at `:713`, `:840`, `:901`; audit rows API-008/011/012/064/092 at `api-audit.md:242,245,246,298,326`; no Go test calls `AssociateUserWithOrg`, `SetUserOrg` or `GetOrgMembers`, and `UpsertOrg` appears only as a fixture at `sso_providers_test.go:116`; `e2e/auth.ts` mints the JWT and names the callback gap; PAC-50 `:26`, `:64`, `:126`, `:183`, `:273` say what the spec says they do.

### Consumers this breaks

- None found that would break. In `smart-calendar-flow/src` nothing outside the generated types calls `/api/org/members` or reads a user's `org`. `mcp/` has no org tool, and no e2e scenario touches organisations. The only behaviour that changes for existing callers is the one intended: `GET /api/org/members` (`handlers_org.go:26-28`) returns `[]` for an unverified user who used to be joined, and the SSO admin endpoints return 403 "org membership required" for that user (`handlers_sso.go:203-205`, `:269-271`, `:293-295`). Both are existing "no organisation" states (§3).

## Revision 1 — 2026-10-26

*spec-author, answering the round-1 challenge above (against `22d12b9`). Each finding, what changed, and where.*

### Blocking

1. **Email-keyed sign-in lands an unverified sign-in in a verified member's account.** Accepted. Re-checked: the conflict key is `email` only and `provider` is not in the update list (`users.go:21-31`); the new token is attached to the returned row (`handlers_auth.go:70`, `handlers_microsoft_auth.go:72`).
   - **OQ-1 rewritten** to cover account matching by email on both social providers and across providers, with three cases written out: (a) Google mailbox holder with a consumer account, (b) Microsoft foreign-tenant `mail`, (c) cross-provider. Owner and stage-4 precondition unchanged.
   - **§2** gains the paragraph "Accounts are matched by email alone, across providers", with citations.
   - **§3** gains the bullet "Limit: membership through an address a member already holds is not closed by this spec". It states that this spec guarantees only that no sign-in *adds* an organisation without verified evidence, and points to OQ-1. "Membership is never granted on a fallback to the email domain" is kept and is now true as scoped: it governs the grant, and the Limit bullet names the path it does not govern.
   - **§5**: "The Microsoft email-identity problem beyond organisations" is replaced by "Account matching by email, on both social providers and across them", stating the same limit.
   - AC-10 is **not** narrowed (D-1 forbids removing memberships), as the challenger required.

2. **AC-14 relied on a page that does not exist.** Accepted. Re-checked that the only references to the member listing in `smart-calendar-flow/src` are under `src/api/generated/` (`types.ts:1546`, `schemas.types.d.ts:4052,5130`). AC-14 now observes the API's organisation-member listing on the running stack, requested with the session each account received from sign-in, and says why it is not a page. The OQ-4 dependency (no sign-in stand-in today) and the drop-with-reason fallback are kept.

### Non-blocking

1. **AC-9, Google half.** AC-9 now covers only evidence lookups separate from the identity answer (the Microsoft verified-domain lookup). Evidence that arrives on the identity answer is AC-1/AC-8 when absent; a failure of the identity answer itself keeps today's failure behaviour (cited).
2. **Sign-in must not depend on a new consent.** Added AC-15 and D-5 (assumed by the factory, pending owner confirmation).
3. **AC-11/12/13 are guards.** Labelled as regression guards in §4; AC-11 is noted as meaningful only with a migration.
4. **Preamble "three callbacks".** Corrected to two.
5. **Issue wording "signs up" vs every sign-in.** No spec change; recorded for stage 2: evaluation on every sign-in can only add a verified membership, never remove one (OQ-5, D-1), consistent with the issue's intent.
6. **Two tenants, no pin.** One sentence added to §3.

### Citations corrected

- Microsoft user upsert: `:68` to `:67` (§2).
- Teams: §2 now says the org id column is read but never written, so teams are not a membership consumer today.

## Challenge — 2026-10-26 (round 2)

*spec-challenger, against commit `3e0ade8` on `factory/pac-68-spec` (committed state only). Citations checked against that commit, whose code is the same as `main` at `2e1f392`. The Linear issue body was re-read.*

**Verdict**: accept-with-changes

Revision 1 resolves both round-1 blocking findings. (1) OQ-1, §2, §3 "Limit" and §5 now describe email-keyed account matching on both providers and across them. AC-10 is not narrowed. (2) AC-14 now observes `GET /api/org/members` at the API and drops the page. I re-checked the citations Revision 1 added (`users.go:21-31`, `handlers_auth.go:63,70`, `handlers_microsoft_auth.go:67,72`, `handlers_auth.go:56-60`, `handlers_microsoft_auth.go:62-66`) and they say what the spec says. One new blocking finding comes from text that existed in the draft but now matters, because AC-9 and AC-15 depend on it.

### Blocking

1. **The Microsoft rule for a pinned organisation says two different things. They disagree on whether a sign-in from the pinned tenant joins when the verified-domain evidence is negative or unavailable.**
   - §3 says the user joins "only when the tenant the account signed in from is shown, by Microsoft, to have proven ownership of the email's domain". It then adds that, if the org is pinned, "the account's tenant must be that tenant". That reads as both conditions being required.
   - AC-7 says: "pinned to tenant T1 (or not yet pinned, with the tenant's verified-domain answer containing `acme.com`) … from T1 … then it belongs to the `acme.com` organisation". Here the verified-domain answer is a condition only in the unpinned branch.

   Take an org pinned to T1 and a `frank@acme.com` sign-in from T1 where the verified-domain lookup errors, or where the answer does not list `acme.com` (for example after the domain was moved out of T1 and the pin stayed, which PAC-50 NB5 allows). Under §3 he is not joined; AC-9 and AC-5 say so too. Under AC-7 he is joined. A test author writing AC-7's pinned branch with no verified-domain answer would write a test that §3 forbids. The choice also decides whether AC-15/D-5 apply to pinned orgs at all. **Resolves when** §3 and AC-7 state the same rule for the pinned case: either the tenant must equal the pin *and* the verified-domain answer must list the domain, or the pin alone suffices. AC-7's pinned branch should also say what the verified-domain answer is in its "Given". If the spec keeps "pin alone suffices", it should say why that does not widen membership after a domain leaves the pinned tenant, given that PAC-50 keeps the pin in that case (`docs/specs/PAC-50.md:126`, `tid_changed`).

### Non-blocking

1. **AC-15 asserts something Paceday cannot observe offline.** A tenant's consent policy is enforced by Microsoft, not by the callback. An injected unit test can model only two things: the evidence lookup being refused, which is already AC-9's outcome, and the permission set the sign-in start requests. As written, one author will test "lookup returns insufficient-privileges → not joined" and another will test the requested permission list. State the observable: the permissions requested at Microsoft sign-in start are today's set (or a named set), and a refused evidence lookup gives AC-9's outcome.
2. **D-5's "a Microsoft permission beyond what sign-in requests today" does not say whether adding the `openid` scope counts.** OQ-2 names an ID token (`openid`) as one source of the tenant id, and the Linear issue's resolution names the ID token's `tid`. A literal reading of D-5 forbids adding `openid`, even though it needs no admin consent. If that is intended, say so. If D-5 means "a permission a restrictive tenant can refuse", say that instead. Otherwise stage 2 inherits the ambiguity.
3. **§2 "What membership grants today" leaves out one place membership is visible.** `GET /api/auth/me` (`backend/api/routes.go:59`, `handlers_me.go:22-28`) encodes the user with an `org` object (`id, name, domain`) whenever `org_id` is set (`backend/storage/users.go:17,68-72`). §4's definition of "not joined" checks only the member listing. Adding "and the current-user response carries no organisation" would give AC-1–AC-9 a second observable, at no cost. No consumer breaks: no code in `smart-calendar-flow/src` reads `org` from that response. `AuthContext.tsx:50` and `schedulingLinks.ts:452,510,570` call it but do not read the field.
4. **AC-9 sets no bound on how long the separate lookup may take.** "Errors or times out" without a limit means the success path of every unpinned Microsoft sign-in can be delayed indefinitely by a slow Graph answer. Today's identity calls use 5 s client timeouts (`handlers_auth.go:178`, `handlers_microsoft_auth.go:90`). Stating a maximum added latency, or "no longer than the identity call's", would make "times out" testable.
5. **Sequential tenants before a pin.** The §3 sentence about two tenants holding one domain is correct for concurrent claims. Under D-3 and D-1, if a domain moves from tenant A to tenant B while unpinned, members joined from A are kept and B's users also join. Microsoft verified both tenants for the domain at the time each joined, so this is acceptable. One sentence would stop the plan from treating it as a gap.

### Criteria I could not falsify

- AC-15: see non-blocking 1. No offline test can distinguish "the tenant restricts consent" from "the evidence lookup failed", which AC-9 already covers.
- AC-7, pinned branch: falsifiable only once blocking 1 settles whether a verified-domain answer is part of the "Given".
- AC-11, AC-12, AC-13: regression guards, now labelled as such.

AC-1 to AC-6, AC-8 to AC-10 and AC-14 are falsifiable as written. Each one fails today, because `AssociateUserWithOrg` joins or creates by email domain on both social callbacks (`orgs.go:42-52`, `handlers_auth.go:76`, `handlers_microsoft_auth.go:76`). Each one passes once the evidence rule exists. AC-14 remains subject to OQ-4.

### Citations I checked and found wrong

- None. Re-checked and found correct: `users.go:21-35` (email-only conflict key; `provider` not updated); `orgs.go:18-52`; `handlers_auth.go:49-80,164-193` (no `hd`/`verified_email` field; 5 s timeout); `handlers_microsoft_auth.go:54-105` (Graph `/me?$select=id,mail,displayName`; 502 on missing mail); `microsoft_oauth.go:14-33` (`/common`, no `openid`); `google_oauth.go:16-19`; `handlers_sso.go:177-191,203,269,293`; `handlers_org.go:26-31`; PAC-50 `:118`, `:126`, `:183`, `:273`. The Linear issue's draft acceptance ("signs up", "not re-evaluated") is consistent with the spec, as recorded in round 1 non-blocking 5.

### Consumers this breaks

- None found. Membership is read only by `handlers_org.go:26-31`, the SSO admin handlers (`handlers_sso.go:203,233,269,274,293,298`) and the `org` field of `GET /api/auth/me` (`handlers_me.go:28`). Nothing in `mcp/` or `e2e/` reads it, and no file in `smart-calendar-flow/src` outside `src/api/generated/` reads it. The `client.ts:919` "current org domain" text describes a mock free/busy heuristic, not a server read. Every changed outcome is an existing "no organisation" state.
