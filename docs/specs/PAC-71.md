# Spec — PAC-71: Sign-in matches accounts by email alone — bind accounts to the provider identity

- **Linear**: https://linear.app/paceday/issue/PAC-71/sign-in-matches-accounts-by-email-alone-anyone-who-controls-an-address
- **Status**: draft
- **Author**: spec-author
- **Priority**: Urgent (security)
- **Inherited scope**: none blocking. `contracts/openapi/paths/auth.yaml` carries no `x-uncertain` marker on the three sign-in callbacks. Related audit rows: API-092 (auth handlers without Go tests; partly fixed, `docs/factory/api-audit.md:350`): every new callback outcome this spec adds gets a Go assertion. API-026 (`api-audit.md:284`): a connect flow that depends on the session cookie surviving the provider's redirect. This spec's linking step depends on the same thing, so the plan must say how it does (§8 OQ-5).
- **Origin**: PAC-68 spec OQ-1 (`origin/factory/pac-68-spec`, `docs/specs/PAC-68.md:144`), filed as PAC-71 at the owner's request (owner-decisions, PAC-68 OQ-1).
- **Verified against**: `clockwise-like` `origin/main` at `ae5c2cf` (2026-10-28); `smart-calendar-flow` `origin/main` at `eea0cf6`.

## 1. Problem

A Paceday account is found by its email address and nothing else. Whoever completes a sign-in with Google, Microsoft or an organisation's single sign-on that asserts an address becomes the Paceday user who holds that address. That includes the user's session, their stored calendar credential and their organisation membership. It does not matter which provider created the account, or whether that provider had any authority over the address.

Concrete account-takeover scenarios against an existing user Henry (`henry@acme.com`):

1. **Google, mailbox holder.** Someone who receives mail at `henry@acme.com` creates a personal (consumer) Google account on that address. A catch-all, a forwarding alias or a former employee whose mail still arrives can do this. Google marks the address "verified" because a mail reached it. They sign in with Google and get Henry's account.
2. **Microsoft, any tenant's administrator.** The administrator of *any* Microsoft tenant can set one of their users' mail attribute to `henry@acme.com`. Sign-in uses Microsoft's multi-tenant endpoint, so that user signs in and gets Henry's account. Microsoft does not vouch for the mail attribute.
3. **Across providers.** Either case lands in Henry's account even when Henry created it through the *other* provider. For example, Henry joined through Google Workspace and the attacker arrives through Microsoft.
4. **Microsoft personal account vs work account.** A personal Microsoft account and a work account can carry the same address. They are different people as far as Microsoft is concerned, but Paceday treats them as one.
5. **Organisation single sign-on (PAC-22, PAC-50).** An organisation's SSO configuration names an arbitrary OIDC issuer. Members can write that configuration, gated since PAC-50 by an admin step-up. Any identity that issuer asserts with an `@acme.com` address lands in the existing account for that address, even if the account was created through Google or Microsoft. Swapping the issuer for a different IdP therefore hands every member's account to whoever controls the new IdP.
6. **Email change or reuse at the provider.** If Henry's address changes at his provider, today's matching sends him to a different (new) account. If the provider later gives `henry@acme.com` to someone else, that person gets Henry's account.
7. **Squatting, the reverse direction.** An unvouched address creates an account (cases 1, 2). The real owner is then matched *into the attacker's account* when they first sign in. Elsewhere in the product an address is trusted to mean a person: scheduling-link co-hosts and invitees are looked up by address. So a squatted account can receive invitations meant for the real owner.

PAC-68 stops an unverified sign-in from being *granted* organisation membership. It does not stop anyone from reaching an existing member's account, and with it that membership (PAC-68 §3 "Limit"). The cost of the defect is full account takeover: calendar read/write through the stored credential, the user's organisation data, and admin standing where the user has it.

## 2. Current behaviour

**One conflict key: the email.** `users.email` is `NOT NULL UNIQUE` (`backend/storage/migrations/006_auth.up.sql:5`). `provider` and `provider_id` are plain columns with defaults (`006_auth.up.sql:8-9`). `UpsertUser` inserts with `ON CONFLICT (email) DO UPDATE`. On conflict it overwrites `name`, `avatar_url` and **`provider_id`**, but not `provider` (`backend/storage/users.go:21-31`). So:
- `provider` records the provider of the row's *first* sign-in. The contract says so (`backend/api/gen/paceday.gen.go:1186-1197`).
- `provider_id` records the identifier of the *most recent* sign-in, from whichever provider it came (`users.go:28`). Nothing records which provider that identifier belongs to.
- No code reads `provider_id`. Grep of `backend` outside tests and generated code finds it only in `users.go:23,28`.

**Google callback** (`GET /api/auth/callback`, `backend/api/routes.go:33`; `backend/api/handlers_auth.go:42-84`):
- It checks the state cookie (`:43-48`), exchanges the code (`:50`) and reads the v2 userinfo endpoint (`:57`, URL at `:232`).
- The decoded profile has only `id`, `email`, `name`, `picture` (`:235-240`). Google's email-verified flag and hosted domain are not read.
- It upserts by email (`:64`), stores the Google token on the returned row (`:71`), associates the org by the email's domain (`:77`, `backend/storage/orgs.go:42-52`) and issues a session for that row (`:79`).
- Scopes are Calendar and `userinfo.email` only, without `openid` (`backend/auth/google_oauth.go:16-19`).

**Microsoft callback** (`GET /api/auth/microsoft/callback`, `routes.go:35-36`; `backend/api/handlers_microsoft_auth.go:40-84`):
- It uses Microsoft's `/common` endpoint, so any tenant and personal accounts can reach it (`backend/auth/microsoft_oauth.go:17-18`).
- Scopes are `Calendars.ReadWrite`, `User.Read`, `offline_access`, without `openid` (`microsoft_oauth.go:40`). So no ID token is requested and the tenant id is never seen.
- The profile is Graph `/me?$select=id,mail,displayName` (`handlers_microsoft_auth.go:90`, struct `:93-97`). `mail` is the directory attribute that tenant administrators set. A sign-in without it fails (`:65`).
- It upserts by `mail` (`:69`) and saves the Microsoft token on the returned row (`:74`). `SaveMicrosoftToken` also sets that row's `calendar_provider = 'outlook'` (`microsoft_oauth.go:90`). This is the work-calendar reset that the owner ruled must go (PAC-47 vs PAC-67 OQ-13).
- It then associates the org (`handlers_microsoft_auth.go:78`) and issues the session (`:79`).

**OIDC SSO callback** (`GET /api/auth/callback/oidc/{domain}`, `routes.go:42`; `backend/api/handlers_sso.go:103-167`):
- The issuer, client id and secret come from the SSO provider row for `{domain}` (`:118-131`). An org member writes that row behind PAC-50's step-up (`handlers_sso.go:183-247`).
- The ID token is verified against that issuer and client id (`backend/auth/oidc.go:79-83`). The claims kept are `sub`, `email`, `name` (`oidc.go:19-23`). `email_verified` is not read.
- The only identity check is that the email belongs to `{domain}` (`handlers_sso.go:150-153`, PAC-45). The callback then upserts by email (`:155`), with provider `"sso"` and `provider_id` = `sub`, associates the org (`:160`) and issues the session (`:162`).
- SAML is refused with 501 (`:62-65`).

**The calendar "connect" flow is the sign-in flow.** In the web app, "Connect work calendar" navigates to `/api/auth/google` or `/api/auth/microsoft` (`smart-calendar-flow` `src/api/client.ts:1181-1182`, `src/components/WorkCalendarConnection.tsx:87`). Sign-in uses the same start routes (`src/components/auth/AuthDialog.tsx:79,102`). Both callbacks are public (`routes.go:28-35`) and never look at the session the browser already holds. A signed-in user who connects Outlook therefore:
- lands in their own row only when the Microsoft `mail` equals their Paceday address;
- otherwise lands in, or creates, *another* account and is switched to it, because the callback issues a new session for whatever row the upsert returned (`handlers_microsoft_auth.go:69-79`).

**A test pins today's email matching.** `TestPAC67_MicrosoftCallbackKeepsRefreshAndFlipsOnlyTheCaller` (`backend/api/pac67_conferencing_test.go:322-358`) creates `alice` with no provider identity (`createTestUser`, `backend/api/handlers_teams_test.go:53-60`). It then runs the Microsoft callback with **no session** and a Graph answer `id: ms-alice`, `mail: alice's address` (`pac67_conferencing_test.go:333-341`), and asserts that the token lands on alice (`:346-349`). Under this spec that callback is a stranger's first sign-in against an account with no binding (§3 "Accounts created before this change"), so the test must be rewritten.

**Sessions** are 7-day HS256 JWTs carrying the user id and the email (`backend/auth/jwt.go:17-22`). There is no server-side session store (`e2e/auth.ts:13-15`). A session obtained through email matching before this change stays valid until it expires.

**PAC-50 step-up compares email, not identity.** The step-up identity carries email, verified flag, `hd`, `tid`, `preferred_username` and roles, but no subject or object id (`backend/auth/stepup.go:172-182`).
- Google step-up requires `hd` equal to the org domain, a verified email equal to the session's email, and admin status (`backend/api/handlers_sso_stepup.go:494-508`).
- Microsoft step-up requires the tenant's verified domains to contain the org domain, the tenant pin to hold, the username or email to equal the session's email, and a qualifying role (`:511-533`).

**Address-as-person elsewhere.** Scheduling-link co-hosts and invitees are resolved by address (`backend/api/handlers_scheduling_links.go:377-382`, `:553`, `:656-660`). An account squatted on an address therefore receives what is meant for its owner (§1 case 7).

**Deployments.** Production had zero users on 2026-10-21; other deployments may have some (`docs/specs/PAC-22.md:80`, citing PAC-49 §2 item 1).

**Test coverage today:**
- Callback success and failure redirects: `backend/api/pac35_auth_flows_test.go:142-246` (Google and Microsoft) and `:286` (OIDC every documented status).
- The SSO email-domain binding: `backend/api/handlers_sso_test.go:161-195`.
- No test asserts anything about *which* account a sign-in reaches when two identities share an address.
- e2e cannot reach any sign-in callback. It mints the session directly and names that gap (`e2e/auth.ts:17-25`).

## 3. Desired behaviour

### The rule

1. **An account belongs to the identities bound to it, not to an address.** An identity is the provider authority together with the subject that authority issues:
   - **Google**: Google's stable account identifier.
   - **Microsoft**: the pair of tenant and object id. The same object id from another tenant is a different identity. A personal Microsoft account is its own authority, distinct from every work tenant.
   - **Organisation SSO**: the configured issuer together with that issuer's subject. Two different issuers are different identities even if one asserts the other's subjects.
2. **A sign-in reaches an account only through a bound identity.** If the identity is bound, the sign-in reaches that account, whatever address the provider now reports.
3. **The address never links a sign-in to an account that already has a bound identity.** This holds whatever the provider says about the address, including the strongest evidence (a Workspace hosted domain or a tenant-verified domain).
4. **A new identity creates an account only when the provider vouches for the address and no account holds it.** That account is bound to the identity on creation. "Vouches" means:
   - **Google**: the email-verified flag is true.
   - **Microsoft work account**: the sign-in's tenant has proven ownership of the address's domain, the same evidence as PAC-68 §3 Microsoft item 1.
   - **Microsoft personal account**: only if the plan names a Microsoft answer that vouches for the address (OQ-3); otherwise refused.
   - **Organisation SSO**: the issuer is the one configured for the address's exact domain (as today) and the token does not say the email is unverified.
5. **A second identity is added to an account only by an explicit step that the signed-in user starts.** The step is tied to the session that started it, and the provider's answer is attached to that same account. Connecting a work calendar from Settings is such a step (D-4, OQ-1). Once that identity is linked, it signs the user in to the same account.

### Accounts created before this change (migration)

Existing accounts carry no binding; only the first-sign-in provider and the identifier of the last sign-in are recorded (§2). They are bound **lazily, on their first sign-in after this change** that meets one of these conditions:

- **(a) The identifier matches.** The incoming identifier equals the identifier recorded on the account, and the provider vouches for the address (rule 4). This is the user who signed in last, through the provider they used last.
- **(b) Authoritative evidence.** The provider is the authority for the address's domain:
  - Google: the hosted domain equals the address's domain and the email is verified, or the address is a Google consumer address (gmail.com / googlemail.com).
  - Microsoft: a work account whose tenant has verified the address's domain.
  - SSO: the issuer configured for that exact domain.

  A consumer Google account on a non-Google domain (§1 case 1), a foreign tenant (case 2), and a personal Microsoft account on a work address (case 4) are **not** authoritative.

Any other sign-in that matches an unbound existing account only by address is refused (see "Refusal"). Binding happens once: after it, the account follows rules 2–5 like any other.

- A user who used both Google and Microsoft before this change gets one of them bound by (a) or (b). They add the other through the explicit step in rule 5.
- Nothing is deleted. No account, membership, credential or setting is removed by the migration or by a refusal (D-9).

### Refusal

When a sign-in cannot reach an account under these rules, the user is told so plainly on the web app's sign-in page. Examples are an address that belongs to an account bound to another identity, an unbound account without (a) or (b), or a new identity whose address the provider does not vouch for. The message says that Paceday could not sign them in with this account. It also says that if they already use Paceday, they should sign in the way they did before and add this sign-in method from Settings.

The message is the same for every refusal cause, so it does not reveal whether an account exists for the address (D-6). It is distinct from today's generic "sign-in did not complete" state.

A refusal:
- creates no account and binds nothing;
- stores no provider credential;
- changes no organisation membership and no work calendar;
- issues no session, and leaves any session the browser already holds untouched (as PAC-35's failure state does today).

The server log records the cause.

### Address changes and reuse at the provider

- A bound identity whose provider now reports a different address still reaches its account.
- The account's stored address does **not** change on sign-in (D-7). If the new address belongs to another account, that account is not touched.
- A provider that gives an address to a new subject (a recycled mailbox) does not reach the old holder's account. The new subject cannot create an account on that address while the old account holds it. That is a refusal, resolved by an operator (D-12).

### Organisation membership (PAC-68)

Membership is still decided by PAC-68's rule on the provider's answer for the sign-in. This spec only makes sure that rule is evaluated for the account the identity is bound to, and never for an account reached through an address. A refused sign-in evaluates membership for nobody.

### PAC-50 admin step-up

When the account has a bound identity at the same authority the step-up uses (Google, or the same Microsoft tenant), the step-up's identity must be that bound identity, in addition to today's checks. A step-up by another Google account or another object id with the same address fails as an identity mismatch. When the account has no binding at that authority, today's address comparison applies unchanged. The step-up must not start requiring any new consent (owner, PAC-68 OQ-2 "offer both").

### Constraints from owner decisions

- Sign-in keeps today's permissions and never requires a new admin consent (owner, PAC-68 OQ-2). Any identity evidence this spec needs (tenant id, verified flag, hosted domain, tenant-verified domains) must be obtained within that rule. Where it cannot be obtained for a sign-in:
  - a **bound** identity still signs in (binding needs no address evidence);
  - a **new** identity or an unbound account that would need the evidence is refused (D-5).
- A Microsoft sign-in must not reset an existing user's work calendar (owner, PAC-47 vs PAC-67 OQ-13). This spec does not remove today's reset (PAC-47 owns that). It must not add a new path that resets it, and a refused sign-in never touches it. Connecting Outlook from Settings's work-calendar control is an explicit choice and may set it, as today.

## 4. Acceptance criteria

Conventions:
- **"Henry"** is an existing account for `henry@acme.com`.
- **"Bound to X"** means the account's identity binding is X.
- **"Refused"** means all of the following:
  - the callback ends on the refusal state of §3 (one fixed outcome, the same for every cause, distinct from the generic failure state);
  - no session is issued and an existing session cookie is neither replaced nor expired;
  - no account is created; no binding is written; no provider credential is stored on any account;
  - no account's organisation or work calendar changes.
- **"Reaches Henry"** means the session issued identifies Henry's account and the provider credential is stored on Henry's account.
- Provider answers are injected with the existing fake-provider seams (`pac35_auth_flows_test.go` pattern), so every [unit] criterion is offline.

**Takeover scenarios (§1)**

AC-1. Given Henry bound to Google identity G1, when Google identity G2 signs in with `henry@acme.com`, email verified, no hosted domain, then the sign-in is refused, and Henry's stored Google credential is byte-for-byte unchanged.  [unit]

AC-2. Given Henry bound to Google identity G1, when a Microsoft work account (tenant T9, object id O9) whose mail is `henry@acme.com` signs in, then it is refused. Also refused: tenant T9 verifies `acme.com` (authoritative evidence), because rule 3 forbids address linking to a bound account.  [unit]

AC-3. Given Henry bound to Microsoft (T1, O1), when Google identity G2 signs in with `henry@acme.com`, email verified and hosted domain `acme.com`, then it is refused.  [unit]

AC-4. Given Henry bound to Microsoft (T1, O1), when (a) (T1, O2) with mail `henry@acme.com`, or (b) (T2, O1) with mail `henry@acme.com` signs in, then both are refused. The object id alone does not identify Henry.  [unit]

AC-5. Given Henry bound to Microsoft work account (T1, O1), when a personal Microsoft account with address `henry@acme.com` signs in, then it is refused. Given instead an unbound pre-existing account for `henry@acme.com`, a personal Microsoft account carrying that address is also refused: it is never authoritative for a work domain.  [unit]

AC-6. Given Henry bound to Google G1 and an OIDC SSO configuration for `acme.com` with issuer I1, when I1 asserts subject S1 with `henry@acme.com`, then it is refused. Given instead Henry bound to (I1, S1), when the configuration's issuer is changed to I2 and I2 asserts subject S1 with `henry@acme.com`, then it is refused.  [unit]

AC-7. Given no account for `new@acme.com`, when two different identities with that address, each vouched for, complete sign-in concurrently, then exactly one account exists afterwards, bound to exactly one of them, and the other sign-in is refused. Given an identity already bound to account A, no operation binds it to a second account B: a link attempt from B is refused and A is unchanged.  [unit]

**Ordinary sign-in keeps working**

AC-8. Given Henry bound to an identity at Google, at Microsoft, or at SSO issuer I1 (three cases), when that identity signs in, then it reaches Henry with the same redirect target as today's success path. The criterion is a regression guard: today's success tests (`pac35_auth_flows_test.go:167`, `:239`, `:286`) keep passing with the binding in place.  [unit]

AC-9. Given Henry bound to Google G1, when G1 signs in reporting `henry.new@acme.com`, then it reaches Henry, and Henry's stored address is still `henry@acme.com`. If another account holds `henry.new@acme.com`, that account's session, credential, address and binding are unchanged.  [unit]

**New accounts**

AC-10. Given no account for `alice@example.org`:
- when Google identity G5 signs in with that address and the email-verified flag true, then an account is created and bound to G5, and a second sign-in of G5 reaches it;
- the same with the flag false or absent is refused, and no account exists for the address afterwards.  [unit]

AC-11. Given no account for `dave@acme.com`, when a Microsoft work account (T1, O7) signs in with that mail, then:
- if tenant T1's verified-domain answer contains `acme.com`, an account is created and bound to (T1, O7);
- if the answer does not contain it, or the lookup errors or times out, the sign-in is refused;
- in neither case does the Microsoft sign-in request any permission beyond today's set other than sign-in identity scopes that need no tenant-admin consent.  [unit]

**Explicit linking (rule 5)**

AC-12. Given Henry signed in (bound to Google G1), when Henry starts "connect work calendar → Outlook" from Settings and Microsoft answers with (T1, O1) and mail `h.work@other.org`, then:
- Henry's session still identifies Henry;
- the Microsoft credential is stored on Henry's account;
- no account for `h.work@other.org` is created;
- a later sign-in by (T1, O1) with no session reaches Henry.

Today the same flow creates or switches to another account (§2).  [unit]

AC-13. Given (T1, O1) is already bound to account A, when signed-in user B starts the same link step and Microsoft answers with (T1, O1), then the link is refused: B's session is kept, and A and B are unchanged.  [unit]

AC-14. Given user A starts the link step, when the callback arrives carrying (a) no session or (b) a session for user B, then no identity is linked to A or to B. Given a browser holding A's session that completes an ordinary sign-in (not started as a link step) with identity X whose address is not A's, then X is not linked to A. The presence of a session cookie alone never links.  [unit]

**Accounts created before this change**

AC-15. Given an unbound pre-existing account for `henry@acme.com` whose recorded last-sign-in identifier is G1:
- when Google G1 signs in with that address verified, then it reaches the account and binds G1;
- a later Google G2 with `henry@acme.com` verified and no hosted domain is refused.  [unit]

AC-16. Given an unbound pre-existing account for `henry@acme.com` whose recorded identifier is G1:
- (a) Google G2 with `henry@acme.com` verified and no hosted domain is refused, and the account stays unbound;
- (b) Google G2 with hosted domain `acme.com` and email verified reaches the account and binds G2;
- (c) a Microsoft work account (T1, O3) whose tenant verifies `acme.com` reaches the account and binds (T1, O3);
- (d) a Microsoft work account whose tenant does not verify `acme.com` is refused.  [unit]

AC-17. Given an unbound pre-existing account for `bob@example.org` (non-Google consumer domain) whose recorded identifier is X, when the Google answer for G1 has a verified email but G1 ≠ X, then it is refused. That is the mailbox-holder case for a legacy account.  [unit]

AC-18. Given the change is deployed, then no account, membership, credential or setting row is deleted, and the count of accounts is unchanged. Binding is written only by a sign-in, not by the deployment.  [unit] (migration test)

**Refusal, membership, work calendar, step-up**

AC-19. Given any refusal in AC-1 to AC-17, the user ends on a page that shows the §3 refusal message. The callback's redirect is the same for every refusal cause and differs from the generic `auth_failed` state. The web app shows the message for that state and offers sign-in again.  [unit] backend; [unit] web app (`smart-calendar-flow`)

AC-20. Given Henry is an organisation member with work calendar set to Google, when any Microsoft sign-in matching Henry only by address is refused, then Henry's organisation and work calendar are unchanged.  [unit]

AC-21. Given Henry bound to Google G1 and an organisation `acme.com` where PAC-50 step-up is required, when Henry's session completes a Google step-up whose identity is G2 (also `henry@acme.com`, verified, hosted domain `acme.com`, admin), then the step-up fails as an identity mismatch and no grant is issued. With G1 it succeeds as today. The Microsoft analogue holds for Henry bound to (T1, O1) and a step-up identity (T1, O2).  [unit]

AC-22. Given the contract, then the three sign-in callbacks document the refusal outcome as a distinct, fixed redirect, alongside today's documented outcomes.  [contract]

AC-23. Given the full stack with a sign-in stand-in, when a second identity carrying Henry's address signs in, then the API's current-user response for any session the browser holds is not Henry's. If the plan does not add a sign-in stand-in, it drops this criterion with that reason (OQ-4), and AC-1 to AC-6 at unit level are the proof.  [e2e]

## 5. Explicitly out of scope

- **Removing today's work-calendar reset on Microsoft sign-in.** PAC-47 owns it (owner, PAC-47 vs PAC-67 OQ-13). This spec only adds nothing that resets it.
- **Organisation membership rules.** PAC-68 owns them. This spec changes only *which account* the rule is applied to.
- **Revoking sessions issued before deployment.** Sessions are self-contained 7-day tokens with no server-side store. A session obtained through address matching before the change stays valid until it expires. Rotating the signing secret at deploy is an operator action: release-note it (OQ-6).
- **A Settings list of sign-in methods, or unlinking one.** Not added by this spec (OQ-1). Disconnecting a calendar credential (PAC-67) does not unlink the sign-in identity (D-11).
- **Merging two existing accounts**, or administrator tools to rebind an account. A recycled address (§3) is resolved by an operator outside the product.
- **SAML.** Not implemented today. When it is, it must follow the §3 rule; recorded here so its spec inherits it.
- **Address-as-person lookups outside sign-in** (scheduling-link co-host and invitee resolution). They rely on the account's address. This spec stops new accounts being created on unvouched addresses (rule 4), which removes the squatting route. The lookups themselves are unchanged.

## 6. What must not change

- **An existing user's next ordinary sign-in through the provider they used last still works.** Migration rule (a) covers it. Today's success tests protect the redirect and session issuance (`pac35_auth_flows_test.go:167`, `:239`, `:286`). No existing test protects *which account* is reached. That is a finding, and AC-8 and AC-15 are the new protection.
- **Sign-in asks for no permission that needs tenant-admin consent** (owner, PAC-68 OQ-2). No test protects Microsoft's scope set today; AC-11 adds one. PAC-50's sign-in scope test (`backend/auth/pac50_signin_scopes_test.go`) must keep passing.
- **The session cookie is untouched on failure** (PAC-35, API-011). Protected by `pac35_auth_flows_test.go:142-165`, `:193-237`. The refusal state keeps this property.
- **The current-user response's `provider` still means "provider of the first sign-in"** (`paceday.gen.go:1186-1197`).
- **The SSO email-domain binding** (PAC-45) stays, protected by `handlers_sso_test.go:161-195`.
- **PAC-50 step-up's existing refusals** (`pac50_sso_stepup_test.go`) keep their reasons. AC-21 adds an identity check on top of them; it does not replace them.
- **Must change.** `TestPAC67_MicrosoftCallbackKeepsRefreshAndFlipsOnlyTheCaller` (`pac67_conferencing_test.go:322-358`) asserts that a session-less Microsoft callback lands on an account matched by address. Under this spec it must become a signed-in link (AC-12) or a bound-identity sign-in (AC-8), keeping its PAC-67 assertions: refresh token kept, loss cleared, the other user unaffected. The owner already expects this test to be amended for PAC-47.

## 7. Rollback

- **Reverting the code** restores matching by address on every callback. That re-opens every §1 scenario, so a revert is a security regression and is release-noted as one.
- **Down migration.** Dropping the binding records loses only the bindings. Accounts, credentials and memberships are untouched. On a later re-apply, every account is "pre-existing" again and re-binds lazily under §3 (a)/(b).
- **Identities linked by the explicit step** (rule 5) whose address differs from the account's would, after a revert, again sign in to (or create) an account matched by *their own* address. Release-note that too.

## 8. Open questions

- **OQ-1: may users manage sign-in methods themselves? (owner, product).** This spec treats connecting a work calendar from Settings as the explicit step that adds that provider as a sign-in method (D-4). Should that hold? And should Settings also offer "add a sign-in method" without connecting a calendar, a list of linked methods, and unlinking?
  - **Factory default:** yes to the first; nothing else added.
  - **Owner:** Nicolas Hochart.
  - **Resolve by:** owner answer before stage 4. **Not blocking stage 2**: the default is safe, since linking always requires the signed-in user's own action.
- **OQ-2: when an organisation enables SSO or changes its IdP, may existing members' accounts be reached through SSO without each member linking? (owner, product).**
  - **Factory default:** no (D-10). After SSO is enabled, an existing member signs in once the way they did before, then links SSO from Settings. If the configured issuer changes, every member re-links.
  - **Alternative:** the org's configured issuer counts as authoritative for its own domain even on accounts already bound elsewhere. That reopens §1 case 5 for whoever can change the configuration (PAC-50 admin step-up).
  - **Owner:** Nicolas Hochart.
  - **Resolve by:** owner answer before stage 4. **Not blocking stage 2**: production has zero users (`docs/specs/PAC-22.md:80`).
- **OQ-3: which provider answers carry the identity evidence within today's consent? (contract-author / plan-author, technical).**
  - Microsoft's tenant id is not seen today (no `openid`, `microsoft_oauth.go:40`). The tenant's verified-domain list needs Graph's organisation resource. Whether `User.Read` suffices is the same sub-question as PAC-68 OQ-2 and PAC-50 OQ-2.
  - Google's verified flag and hosted domain are reported to be in the userinfo response already fetched. Unverified here: no call was made.
  - A personal Microsoft account may have no answer that vouches for its address. If none exists without new consent, personal accounts cannot create accounts (rule 4), and that limit is release-noted.
  - **Resolve by:** the contract naming the scopes and fields. A human decision is needed only if a new admin-consent permission would be required, which the owner has already ruled out for sign-in.
- **OQ-4: can e2e reach the callbacks? (e2e-author / plan-author).** Not today (`e2e/auth.ts:17-25`), the same as PAC-68 OQ-4. **Resolve by:** the plan, which either adds a sign-in stand-in or drops AC-23 with that reason.
- **OQ-5: link-step mechanics and ordering (plan-author / orchestrator).**
  - The link step depends on the browser carrying its session across the provider's redirect, the same fragility as API-026.
  - PAC-68 and PAC-47 change the same three callbacks.
  - **Resolve by:** the plan stating the order. Suggested: PAC-71 first, because PAC-68's membership guarantee (§3 "Limit") depends on it.
- **OQ-6: rotate the session signing secret at deploy? (orchestrator / operator).** It closes sessions obtained through address matching before the change, and signs every user out once. **Resolve by:** release note. Production has zero users, so this is for other deployments.

## 9. Decisions

| ID | Decision | Source |
|---|---|---|
| D-1 | Identity = Google account id; Microsoft (tenant, object id); SSO (configured issuer, subject). | **Owner** for Google and Microsoft (PAC-68 OQ-1, "Google sub, Microsoft oid+tid"). **Factory-assumed** for SSO and for personal Microsoft accounts being a distinct authority. |
| D-2 | The address never links a sign-in to an account that has a bound identity, even with authoritative evidence. | Issue direction, filed at the owner's request ("A matching email address alone never links"). **Factory-assumed** that it holds even for authoritative evidence. |
| D-3 | Mismatch is refused; never a silent merge, never a new account on a held address. | Issue direction; **factory-assumed** specifics (§3 Refusal). |
| D-4 | Connecting a work calendar from Settings while signed in is the explicit linking step. | **Factory-assumed**; OQ-1. Keeps today's experience for users whose two providers share an address, and fixes the account switch for those whose do not. |
| D-5 | A new identity needs the provider to vouch for the address. Missing evidence refuses *new* accounts and unbound-account binding, never a bound identity. | **Factory-assumed**. Stops squatting (§1 case 7). |
| D-6 | One refusal message for every cause, so it does not reveal whether an account exists. | **Factory-assumed**. A foreign-tenant admin can set any address, so a distinct message would let anyone enumerate accounts. |
| D-7 | The account's address does not change when the provider reports a new one. | **Factory-assumed**. The address feeds membership, invitee lookup and the session; changing it silently could collide or move org. |
| D-8 | Pre-existing accounts bind lazily on the first post-deployment sign-in meeting §3 (a) or (b); everything else is refused. | **Factory-assumed**, following the issue ("records the current provider identity on the next verified sign-in"). The recorded identifier is the last writer's (§2), so it is trusted only together with a vouched address. |
| D-9 | Nothing is deleted: accounts, memberships, credentials, settings. | Factory rule; issue body. |
| D-10 | SSO is not authoritative over accounts already bound elsewhere; an issuer change requires re-linking. | **Factory-assumed**; OQ-2. |
| D-11 | Disconnecting a calendar credential does not unlink the sign-in identity. | **Factory-assumed**; OQ-1. |
| D-12 | A recycled address at the provider (new subject) is refused while the old account holds it; an operator resolves it. | **Factory-assumed**. |
| D-13 | PAC-50 step-up also requires the bound identity when the account has one at that authority. | **Factory-assumed**. Step-up today compares only the address (§2). |
| D-14 | Sign-in requests no permission needing tenant-admin consent. | **Owner** (PAC-68 OQ-2, "offer both"). |
| D-15 | A Microsoft sign-in must not reset an existing user's work calendar. This spec adds no reset path, and a refusal touches no calendar. | **Owner** (PAC-47 vs PAC-67 OQ-13). |

## Challenge, revision 0 — 2026-10-28

**Verdict**: accept-with-changes. The core model is sound and answers the issue's three cases and its draft acceptance: an identity is an authority plus a subject, rule 3 says the address never links to a bound account, a refusal has no side effects, legacy accounts bind lazily, and nothing is deleted. Six findings below block stage 2. One contradicts an owner-pinned test. One adds a work-calendar reset path the owner forbade. Four leave a takeover or lock-out path that the rule still allows. Revision 1 must resolve all six before contract work starts.

Checked against `clockwise-like` `origin/main` `ae5c2cf` (worktree on `eddc3ce`, which differs from `ae5c2cf` only by this file) and `smart-calendar-flow` `origin/main` `eea0cf6` (read with `git show` only). No code was run. No provider endpoint was called. Every provider fact below is marked as known or unverified.

### Blocking

1. **Rule 4's Google "vouch" is the evidence §1 case 1 calls worthless, so squatting (§1 case 7) stays open.** Line 86 makes Google's email-verified flag enough to create an account. Line 17 says Google sets that flag "because a mail reached it". §5 (line 240) then claims rule 4 "removes the squatting route", which is false for Google.
   - *Attack:* before Henry first uses Paceday, a catch-all or ex-employee mailbox holder creates a consumer Google account on `henry@acme.com` (verified, no `hd`) and signs in. An account bound to the attacker is created. When Henry later arrives through acme's Workspace (`hd=acme.com`), rule 3 refuses him, so he is locked out of his own address.
   - Meanwhile the attacker's account receives every grant made to the address: scheduling-link co-host invites (`handlers_scheduling_links.go:377-382`, `:553`, `:656-660`) and **team invites**. Team-invite acceptance compares only the stored address (`backend/api/handlers_teams.go:302`), and §2 does not list it.
   - The same evidence is treated differently elsewhere: a personal Microsoft account is refused on it (line 88).
   - *Resolve:* the spec decides one of these, as an owner question:
     - (a) On a domain that is not a Google consumer domain, only `hd` equal to the address's domain vouches. Consumer Google accounts on custom domains then cannot create accounts, and that limit is release-noted.
     - (b) The residual risk is accepted explicitly.

     Either way, add an AC for the pre-emptive squat (attacker first, Workspace owner second) and correct line 240. Whatever is chosen, make the Google and personal-Microsoft rules consistent or justify the difference.

2. **The link step creates a permanent, invisible, irremovable sign-in method from any transient session.** Four parts of the spec combine:
   - Rule 5 (line 90) and D-4 (line 288) make every "Connect work calendar" add a sign-in identity.
   - OQ-1's default (lines 260-263) adds no list of sign-in methods and no unlink.
   - D-11 (line 295) says disconnecting the calendar does not unlink, and §5 (line 238) gives no operator tool.
   - OQ-6 (line 279) leaves pre-deployment sessions, obtained by email matching, valid for up to 7 days.

   *Attack:* anyone who holds Henry's session for a moment connects their **own** Microsoft account as Henry's work calendar. That session can be one from before deployment (the §1 scenarios), a stolen cookie, or an unlocked laptop. From then on they sign in as Henry indefinitely. Nothing in the product shows it or can undo it, and neither JWT expiry nor rotating the signing secret removes it. This turns the OQ-6 "transitional" exposure into a permanent takeover.

   *Resolve:* state a requirement that closes this, with ACs. Acceptable forms:
   - adding an identity requires fresh proof of an identity already bound to the account; or
   - the user can see and remove linked identities (this overrides OQ-1's default, so it is an owner question).

   In either case, also require that a session issued before deployment cannot add an identity, or make rotating the secret at deploy mandatory rather than a release note.

3. **Organisation SSO is inconsistent across the migration, D-10/OQ-2 and OQ-1, and no AC covers it.**
   - Migration (b) (line 100) makes "the issuer configured for that exact domain" authoritative over every **unbound** legacy account in the domain. That is OQ-2's rejected "alternative" (line 266), applied to every pre-existing account at once.
   - *Attack:* during the lazy-binding window, whoever configures or swaps the `acme.com` issuer binds it to every acme account that has not signed in since deployment. Doing so needs only a PAC-50 step-up or a **break-glass** address, and break-glass is an email-only check (`handlers_sso_stepup.go:238`).
   - Migration (a) compares "the identifier recorded on the account" with no provider attached (§2 line 31: nothing records which provider it belongs to). An SSO `sub` is a string the configured IdP chooses, so the issuer can assert a legacy user's Google `id` and also qualify under (a).
   - OQ-2's default says existing members "link SSO from Settings". OQ-1's default adds no such step, and no start route exists for an SSO link. Under the defaults, SSO is therefore permanently unusable by every existing member.

   *Resolve:*
   - Decide whether (b)-SSO applies to unbound accounts, and reconcile it with D-10.
   - State that (a) matches only an identifier from the same authority.
   - Either define the SSO link step with ACs, or record "SSO unusable for existing accounts" as a limit the owner accepts.
   - Add migration ACs for SSO and for the `gmail.com` branch of (b). AC-15 to AC-17 cover only Google `hd` and Microsoft.

4. **A new path that resets the work calendar, against §3 line 142 and the owner's PAC-47 vs PAC-67 OQ-13 ruling.** AC-12 makes a linked Microsoft identity with a different address (`h.work@other.org`) reach Henry on a later sign-in without a session. AC-8 requires that sign-in to follow "today's success path", and today's path calls `SaveMicrosoftToken`, which sets `calendar_provider = 'outlook'` (`backend/auth/microsoft_oauth.go:90`).
   - *Scenario:* Henry links Outlook. Later he switches his work calendar back to Google in Settings. He then signs in with Microsoft, and his work calendar is reset to Outlook.
   - Before this spec, that sign-in reached a different account. So this is a new reset path, not "today's existing behaviour".

   *Resolve:* add an AC that a Microsoft sign-in reaching an account through an identity bound by the link step leaves `calendar_provider` unchanged. Alternatively, make PAC-47's removal of the reset a stated prerequisite (OQ-5 ordering).

5. **The link step's entry points are incomplete, so ordinary reconnects regress.** §2 (line 53) names only `WorkCalendarConnection.tsx:87`. `api.authConnectUrl` is also called from these signed-in surfaces in `smart-calendar-flow`:
   - `src/components/ConnectionStatus.tsx:18`
   - `src/pages/Dashboard.tsx:475` (reconnect after credential loss, PAC-67)
   - `src/pages/Onboarding.tsx:80`

   AC-14 rules that a session cookie alone never links. The start routes are the same URLs as sign-in (`AuthDialog.tsx:79,98,102`). So every entry point the plan does not convert becomes an ordinary sign-in.

   *Scenario:* Henry is bound to Google and reconnects Outlook from the Dashboard banner. The Outlook account carries the **same** address. Today that works. Under rule 3 it is refused.

   *Resolve:* list every signed-in connect entry point in both repos, state whether each is a link step, and give each an AC. That includes a [unit] web AC that the link-start path is used. Also add the Google analogue of AC-12: a Microsoft-bound user connects Google.

6. **§6's scope guard is wrong, and it contradicts AC-11/OQ-3.** Line 245 says "No test protects Microsoft's scope set today". In fact `backend/auth/pac50_signin_scopes_test.go:19-69` (`TestPAC50_SignInAuthorizeURLsCarryNoNewScope`, PAC-50 AC-24, an owner decision) asserts that each sign-in's scope set is **exactly** today's:
   - Google: `{calendar, userinfo.email}`
   - Microsoft: `{Calendars.ReadWrite, User.Read, offline_access}`

   AC-11 (line 188) and OQ-3 (line 270, "no `openid`") allow adding "sign-in identity scopes", and doing so fails that test. The owner's words are "Sign-in keeps today's permissions".

   *Resolve:* choose one, and do not leave both statements in the spec:
   - require the identity evidence to be obtained within exactly today's scope set. AC-11's third bullet then becomes "the scope set is unchanged", which the existing test already guards; or
   - list that test under "Must change" with an explicit owner question, because this changes an owner-pinned decision.

### Non-blocking

1. **Microsoft "tenant has proven ownership" and unmanaged ("viral") directories. Unverified.** From my knowledge, Microsoft self-service sign-up has created *unmanaged* directories in which a domain counts as verified by email alone. If that still holds, a mailbox holder at `acme.com` could satisfy rule 4 and migration (b) for Microsoft, for example where acme is a Google Workspace shop with no Entra tenant. I could not verify whether such directories still appear in `verifiedDomains` or can still be created in 2026. Make this a question OQ-3 must answer before the contract. PAC-68 §3 relies on the same evidence.
2. **Source of the tenant id.** Require that `tid` and `oid` come from Microsoft's answer for the same token, and that the two sources agree when both are read (PAC-50 step-up does this at `handlers_sso_stepup.go:512`). AC-4 runs against fake providers, so it cannot tell a trusted source from an untrusted one.
3. **Identifier formats.** Today the recorded Microsoft identifier is the Graph `/me` `id`. For work accounts this equals the object id (known). For personal accounts it is a short hex id, not the token's `oid` (from memory, unverified). Name the exact field for each authority so that migration (a) compares like with like.
4. **Concurrent binding of a legacy account.** AC-7 covers new accounts only. Add an AC where two qualifying identities race to bind one unbound legacy account, for example on a hybrid domain verified in both Workspace and Entra: exactly one binds and the other is refused.
5. **Address comparison is case-sensitive today.** `users.email` is plain `TEXT UNIQUE` (`006_auth.up.sql:5`), and no callback lowercases the address. Define "an account holds the address" as case-insensitive. Otherwise `HENRY@acme.com` is a separate address an attacker can squat.
6. **Google `hd` and secondary or alias domains.** `hd` is the Workspace primary domain. Users on a secondary domain fail migration (b) and must rely on (a). Release-note it.
7. **The owner's "extended access" step** (PAC-68 OQ-2, "offer both") is another round trip to the provider. State that it never binds or links an identity.
8. **Address-as-person consumers missing from §2/§5:**
   - team-invite acceptance, `handlers_teams.go:302`;
   - break-glass by session address, `handlers_sso_stepup.go:238`;
   - the `lower(email)` lookup at `handlers_manager.go:356`.
9. **AC-11's third bullet names no scope set.** A test-writer cannot falsify "identity scopes that need no tenant-admin consent" without a list. Resolve it together with Blocking 6.
10. **AC-23 may be dropped by the plan.** That is acceptable as written. If it is dropped, though, AC-1 to AC-7 are the only proof, so their tests must drive the real callback handlers (as `pac35_auth_flows_test.go` does), not a storage-level helper.

### Criteria I could not falsify

- AC-11, third bullet: no scope set is named (Blocking 6, Non-blocking 9).
- AC-19, web half: "offers sign-in again" names nothing observable, such as a button text or a route.

Every other AC has a test that fails today and passes afterwards. Example: AC-1 fails today because `UpsertUser`'s `ON CONFLICT (email)` returns Henry's row and the callback stores G2's token on it.

### Citations I checked and found wrong

- `routes.go:33` for `GET /api/auth/callback`: the route is at `routes.go:34` (`:33` is `/google`).
- "Both callbacks are public (`routes.go:28-35`)": the Microsoft callback is at `:36`, outside that range.
- §6 line 245, "No test protects Microsoft's scope set today": wrong (Blocking 6).
- `AuthDialog.tsx:79,102`: correct as far as it goes, but it omits the Google start with a login hint at `:98`.

I checked every other citation in §2, §6 and the header and found it correct:

- `clockwise-like` backend: `006_auth.up.sql:5,8-9`; `users.go:21-31`; `paceday.gen.go:1186-1197`; `handlers_auth.go:42-84` and the lines inside it; `google_oauth.go:16-19`; `microsoft_oauth.go:17-18,40,90`; `handlers_microsoft_auth.go:40-97`; `handlers_sso.go:62-65,103-167,183-247`; `oidc.go:19-23,79-83`; `stepup.go:172-182`; `handlers_sso_stepup.go:494-533`; `handlers_scheduling_links.go:377-382,553,656-660`; `jwt.go:17-22`.
- `clockwise-like` tests: `pac67_conferencing_test.go:322-358`; `handlers_teams_test.go:53-60`; `e2e/auth.ts:13-25`; `pac35_auth_flows_test.go:142,167,193,239,286`; `handlers_sso_test.go:161-195`.
- `clockwise-like` docs: `api-audit.md:284,350`; `PAC-22.md:80`; `PAC-68.md:144`.
- `smart-calendar-flow`: `client.ts:1181-1182`; `WorkCalendarConnection.tsx:87`.

### Consumers this breaks

- `clockwise-like` `backend/api/pac67_conferencing_test.go:322-358`: already named by the spec.
- `clockwise-like` `backend/auth/pac50_signin_scopes_test.go:19-69`: fails if any sign-in scope is added (Blocking 6).
- `smart-calendar-flow` `src/components/ConnectionStatus.tsx:18`, `src/pages/Dashboard.tsx:475`, `src/pages/Onboarding.tsx:80` and `src/components/WorkCalendarConnection.tsx:87`: these signed-in connects become refused sign-ins unless they are converted to the link step (Blocking 5).
- `smart-calendar-flow` `src/pages/Index.tsx:568` and `src/pages/AuthCallback.tsx:8`: they only recognise `auth_failed`, so the new refusal state needs a reader (AC-19).

### Provider facts (OQ-3): known vs unverified

Known from my knowledge, not checked by any call:

- Google's v2 userinfo `id` equals the OIDC `sub`.
- With the `userinfo.email` scope, v2 userinfo returns `verified_email` (not `email_verified`), and returns `hd` for Workspace accounts.
- Graph `GET /organization` returns `id` (the tenant id) and `verifiedDomains` under delegated `User.Read`. The step-up already reads `/organization?$select=id,verifiedDomains` (`stepup.go:290`), but with its own, wider scopes.
- A Microsoft object id is the same across apps in one tenant. The `sub` claim is different for each app.
- A personal Microsoft account carries the consumer tenant id, and `/organization` does not answer for it.

Could not verify:

- whether `User.Read` alone is enough for `/organization` in 2026;
- whether unmanaged ("viral") directories list a domain in `verifiedDomains`, and whether they can still be created;
- whether Microsoft offers a claim that the email's domain owner verified it, usable without new consent, which would vouch for a personal account's address.
