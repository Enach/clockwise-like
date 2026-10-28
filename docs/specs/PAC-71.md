# Spec — PAC-71: Sign-in matches accounts by email alone — bind accounts to the provider identity

- **Linear**: https://linear.app/paceday/issue/PAC-71/sign-in-matches-accounts-by-email-alone-anyone-who-controls-an-address
- **Status**: draft, revision 1 (answers "Challenge, revision 0"; see "Revision 1" at the end)
- **Author**: spec-author
- **Priority**: Urgent (security)
- **Inherited scope**: none blocking. `contracts/openapi/paths/auth.yaml` carries no `x-uncertain` marker on the three sign-in callbacks. Related audit rows: API-092 (auth handlers without Go tests; partly fixed, `docs/factory/api-audit.md:350`): every new callback outcome this spec adds gets a Go assertion. API-026 (`api-audit.md:284`): a connect flow that depends on the session cookie surviving the provider's redirect. This spec's linking step depends on the same thing, so the plan must say how it does (§8 OQ-5).
- **Origin**: PAC-68 spec OQ-1 (`origin/factory/pac-68-spec`, `docs/specs/PAC-68.md:144`), filed as PAC-71 at the owner's request (owner-decisions, PAC-68 OQ-1).
- **Verified against**: `clockwise-like` `origin/main` at `ae5c2cf` (2026-10-28); `smart-calendar-flow` `origin/main` at `eea0cf6`. Revision 1 re-read every new citation at those commits. No code was run and no provider endpoint was called.

## 1. Problem

A Paceday account is found by its email address and nothing else. Whoever completes a sign-in with Google, Microsoft or an organisation's single sign-on that asserts an address becomes the Paceday user who holds that address. That includes the user's session, their stored calendar credential and their organisation membership. It does not matter which provider created the account, or whether that provider had any authority over the address.

Concrete account-takeover scenarios against an existing user Henry (`henry@acme.com`):

1. **Google, mailbox holder.** Someone who receives mail at `henry@acme.com` creates a personal (consumer) Google account on that address. A catch-all, a forwarding alias or a former employee whose mail still arrives can do this. Google marks the address "verified" because a mail reached it, once, and keeps it marked after the mailbox is lost. They sign in with Google and get Henry's account.
2. **Microsoft, any tenant's administrator.** The administrator of *any* Microsoft tenant can set one of their users' mail attribute to `henry@acme.com`. Sign-in uses Microsoft's multi-tenant endpoint, so that user signs in and gets Henry's account. Microsoft does not vouch for the mail attribute.
3. **Across providers.** Either case lands in Henry's account even when Henry created it through the *other* provider. For example, Henry joined through Google Workspace and the attacker arrives through Microsoft.
4. **Microsoft personal account vs work account.** A personal Microsoft account and a work account can carry the same address. They are different people as far as Microsoft is concerned, but Paceday treats them as one.
5. **Organisation single sign-on (PAC-22, PAC-50).** An organisation's SSO configuration names an arbitrary OIDC issuer. Members can write that configuration, gated since PAC-50 by an admin step-up or the operator's break-glass list. Any identity that issuer asserts with an `@acme.com` address lands in the existing account for that address, even if the account was created through Google or Microsoft. Swapping the issuer for a different IdP therefore hands every member's account to whoever controls the new IdP.
6. **Email change or reuse at the provider.** If Henry's address changes at his provider, today's matching sends him to a different (new) account. If the provider later gives `henry@acme.com` to someone else, that person gets Henry's account.
7. **Squatting, the reverse direction.** A mailbox holder (case 1) or a foreign tenant (case 2) creates the account *before* Henry first uses Paceday. When Henry arrives, he is matched into the attacker's account, or, once accounts are bound to identities, locked out of his own address. Meanwhile the product treats the address as a person in several places: team invitations, scheduling-link co-hosts, a manager's team roster, and the operator's break-glass list (§2 "Address-as-person"). So a squatted account receives what is meant for the address's owner.

PAC-68 stops an unverified sign-in from being *granted* organisation membership. It does not stop anyone from reaching an existing member's account, and with it that membership (PAC-68 §3 "Limit"). The cost of the defect is full account takeover: calendar read/write through the stored credential, the user's organisation data, and admin standing where the user has it.

## 2. Current behaviour

**One conflict key: the email.** `users.email` is `NOT NULL UNIQUE` (`backend/storage/migrations/006_auth.up.sql:5`), plain text, so the uniqueness is case-sensitive. `provider` and `provider_id` are plain columns with defaults (`006_auth.up.sql:8-9`). `UpsertUser` inserts with `ON CONFLICT (email) DO UPDATE`. On conflict it overwrites `name`, `avatar_url` and **`provider_id`**, but not `provider` (`backend/storage/users.go:21-31`). So:
- `provider` records the provider of the row's *first* sign-in. The contract says so (`backend/api/gen/paceday.gen.go:1186-1197`).
- `provider_id` records the identifier of the *most recent* sign-in, from whichever provider it came (`users.go:28`). Nothing records which provider that identifier belongs to. For Microsoft it is Graph `/me`'s `id`; for SSO it is the issuer's `sub`, a string the configured IdP chooses.
- No code reads `provider_id`. Grep of `backend` outside tests and generated code finds it only in `users.go:23,28`.
- No callback lowercases the address before the upsert (`handlers_auth.go:64`, `handlers_microsoft_auth.go:69`, `handlers_sso.go:155`). `GetUserByEmail` compares exactly (`backend/storage/users.go:37-40`); its scheduling-link callers lowercase the input first (`handlers_scheduling_links.go:378`), so a stored mixed-case address is not found by them.

**Routes.** The `/api/auth` group is at `backend/api/routes.go:32-50`. Public: Google start `:33`, Google callback `GET /api/auth/callback` `:34`, Microsoft start `:35`, Microsoft callback `:36`, SSO start `:41`, OIDC callback `:42`. Behind `requireAuth`: status `:47`, Google disconnect `:48`, Microsoft disconnect `:49`.

**Google callback** (`routes.go:34`; `backend/api/handlers_auth.go:42-84`):
- It checks the state cookie (`:43-48`), exchanges the code (`:50`) and reads the v2 userinfo endpoint (`:57`, URL at `:232`).
- The decoded profile has only `id`, `email`, `name`, `picture` (`:235-240`). Google's email-verified flag and hosted domain are not read.
- It upserts by email (`:64`), stores the Google token on the returned row (`:71`), associates the org by the email's domain (`:77`, `backend/storage/orgs.go:42-52`) and issues a session for that row (`:79`).
- Scopes are exactly `calendar` and `userinfo.email`, without `openid` (`backend/auth/google_oauth.go:16-19`).

**Microsoft callback** (`routes.go:36`; `backend/api/handlers_microsoft_auth.go:40-84`):
- It uses Microsoft's `/common` endpoint, so any tenant and personal accounts can reach it (`backend/auth/microsoft_oauth.go:17-18`).
- Scopes are exactly `Calendars.ReadWrite`, `User.Read`, `offline_access`, without `openid` (`microsoft_oauth.go:40`). So no ID token is issued and the tenant id is never seen.
- The profile is Graph `/me?$select=id,mail,displayName` (`handlers_microsoft_auth.go:90`, struct `:93-97`). `mail` is the directory attribute that tenant administrators set. A sign-in without it fails (`:65`).
- It upserts by `mail` (`:69`) and saves the Microsoft token on the returned row (`:74`). `SaveMicrosoftToken` also sets that row's `calendar_provider = 'outlook'` (`microsoft_oauth.go:90`). This is the work-calendar reset that the owner ruled must go (PAC-47 vs PAC-67 OQ-13); PAC-47 removes it.
- It then associates the org (`handlers_microsoft_auth.go:78`), issues the session (`:79`) and redirects with `?provider=outlook` (`:83`).

**OIDC SSO callback** (`routes.go:42`; `backend/api/handlers_sso.go:103-167`):
- The issuer, client id and secret come from the SSO provider row for `{domain}` (`:118-131`). An org member writes that row behind PAC-50's step-up or the break-glass list (`handlers_sso.go:183-247`, `handlers_sso_stepup.go:228-252`).
- The ID token is verified against that issuer and client id (`backend/auth/oidc.go:79-83`). The claims kept are `sub`, `email`, `name` (`oidc.go:19-23`). `email_verified` is not read.
- The only identity check is that the email belongs to `{domain}` (`handlers_sso.go:150-153`, PAC-45). The callback then upserts by email (`:155`), with provider `"sso"` and `provider_id` = `sub`, associates the org (`:160`) and issues the session (`:162`).
- SAML is refused with 501 (`:62-65`).

**The calendar "connect" flow is the sign-in flow.** `api.authConnectUrl` returns the public sign-in start URL for the provider (`smart-calendar-flow` `src/api/client.ts:1181-1182`). Every signed-in connect control in the web app calls it:

| Surface (smart-calendar-flow `eea0cf6`) | Call | What it does |
|---|---|---|
| Settings work-calendar control | `src/components/WorkCalendarConnection.tsx:87` | After patching `calendar_provider` to the chosen provider (`:77`), navigates to the start for Google or Outlook. |
| Connection status control | `src/components/ConnectionStatus.tsx:18` | Connects Google (no provider argument). |
| Dashboard "Connect calendar" banner | `src/pages/Dashboard.tsx:475` | Connects Google (no provider argument). |
| Onboarding calendar step | `src/pages/Onboarding.tsx:80` | Navigates to the start for the provider chosen in the step. |

The public sign-in dialog uses the same start routes (`src/components/auth/AuthDialog.tsx:79` by button, `:98` Google with a login hint, `:102` Microsoft with a login hint). `git grep authConnectUrl` on `origin/main` finds no other caller outside tests and generated code. Both callbacks are public (`routes.go:34`, `:36`) and never look at the session the browser already holds. A signed-in user who connects Outlook therefore:
- lands in their own row only when the Microsoft `mail` equals their Paceday address;
- otherwise lands in, or creates, *another* account and is switched to it, because the callback issues a new session for whatever row the upsert returned (`handlers_microsoft_auth.go:69-79`).

The web app recognises only one failure state, `?error=auth_failed` (`src/pages/AuthCallback.tsx:8`, `src/pages/Index.tsx:568`, `src/components/auth/SignInFailedNotice.tsx:8`).

**A test pins today's email matching.** `TestPAC67_MicrosoftCallbackKeepsRefreshAndFlipsOnlyTheCaller` (`backend/api/pac67_conferencing_test.go:322-358`) creates `alice` with no provider identity (`createTestUser`, `backend/api/handlers_teams_test.go:53-60`). It then runs the Microsoft callback with **no session** and a Graph answer `id: ms-alice`, `mail: alice's address` (`pac67_conferencing_test.go:333-341`), and asserts that the token lands on alice (`:346-349`). Under this spec that callback is a sign-in against an account with no binding (§3 "Accounts created before this change"), so the test must be rewritten.

**A test pins today's sign-in scopes.** `TestPAC50_SignInAuthorizeURLsCarryNoNewScope` (`backend/auth/pac50_signin_scopes_test.go:19-69`, PAC-50 AC-24, owner decision 2026-10-27) asserts that each sign-in's scope parameter is **exactly** today's set: Google `{calendar, userinfo.email}`, Microsoft `{Calendars.ReadWrite, User.Read, offline_access}`, OIDC `{openid, profile, email}`; and that none carries a step-up or admin-consent scope.

**Sessions** are 7-day HS256 JWTs carrying the user id, the email and an issue time (`backend/auth/jwt.go:17-27`). There is no server-side session store (`e2e/auth.ts:13-15`). The issue time is already read for break-glass freshness (`backend/api/middleware.go:18-24`; `breakGlassFreshness = 10 * time.Minute`, `handlers_sso_stepup.go:43`). A session obtained through email matching before this change stays valid until it expires.

**PAC-50 step-up and break-glass compare email, not identity.**
- The step-up identity carries email, verified flag, `hd`, `tid`, `preferred_username` and roles, but no subject or object id (`backend/auth/stepup.go:172-182`).
- Google step-up requires `hd` equal to the org domain, a verified email equal to the session's email, and admin status (`backend/api/handlers_sso_stepup.go:494-508`).
- Microsoft step-up requires the tenant's verified domains to contain the org domain and the `/organization` tenant id to equal the token's `tid` (`:511-513`; `/organization?$select=id,verifiedDomains` at `stepup.go:290`, read with the step-up's own wider scopes), the tenant pin to hold, the username or email to equal the session's email, and a qualifying role (`:514-533`).
- Break-glass passes when the lowercased **session email claim** is on the operator's list and the session is under 10 minutes old (`handlers_sso_stepup.go:238-244`).

**Address-as-person.** These sites treat an address as a Paceday person:

| Site | Lookup |
|---|---|
| Team-invite acceptance | `backend/api/handlers_teams.go:300-305`: accepted when the session user's stored address equals the invitee address (case-insensitive). The invite is also mailed to the address with its token (`:214`, sender `:621-651`, only when `SMTP_HOST` and `SMTP_FROM` are set). |
| Scheduling-link co-host on create | `handlers_scheduling_links.go:377-382` |
| Scheduling-link co-host list patch | `handlers_scheduling_links.go:553` |
| Scheduling-link add co-host | `handlers_scheduling_links.go:656-660` |
| Manager adds a team member | `backend/api/handlers_manager.go:319` (exact match) |
| Manager removes a team member | `handlers_manager.go:356` (`lower(email)`) |
| Manager roster import | `backend/engine/manager.go:244` (exact match) |
| Break-glass | `handlers_sso_stepup.go:238` (session email claim) |
| Organisation association by domain | `storage/orgs.go:42-52`, owned by PAC-68 |

**Deployments.** Production had zero users on 2026-10-21; other deployments may have some (`docs/specs/PAC-22.md:80`, citing PAC-49 §2 item 1).

**Test coverage today:**
- Callback success and failure redirects: `backend/api/pac35_auth_flows_test.go:142-246` (Google and Microsoft) and `:286` (OIDC every documented status).
- The SSO email-domain binding: `backend/api/handlers_sso_test.go:161-195`.
- The sign-in scope sets: `pac50_signin_scopes_test.go:19-69`.
- No test asserts anything about *which* account a sign-in reaches when two identities share an address.
- e2e cannot reach any sign-in callback. It mints the session directly and names that gap (`e2e/auth.ts:17-25`).

## 3. Desired behaviour

### Identities

An account belongs to the identities bound to it, not to an address. An identity is the provider authority together with the subject that authority issues:

- **Google**: Google's stable account identifier.
- **Microsoft work or school account**: the pair of tenant and object id, both taken from Microsoft's answers for the same access token. The same object id from another tenant is a different identity.
- **Microsoft personal account**: its own authority, distinct from every work tenant, with Microsoft's identifier for the personal account. An account is classified personal only on a positive signal in Microsoft's answer; an answer that cannot be classified fails the sign-in (generic failure, nothing written).
- **Organisation SSO**: the issuer the ID token was verified against, together with that issuer's subject. Two different issuers are different identities even if one asserts the other's subjects.

Identifiers are only ever compared together with their authority. An identifier from one authority never matches an identifier from another, even when the strings are equal.

All identity evidence comes from the answers today's sign-in permissions already return. Sign-in requests exactly today's permissions (owner, PAC-68 OQ-2; PAC-50 AC-24). Where a part of the *identity* (for example the Microsoft tenant) cannot be obtained for a sign-in, the sign-in fails with the generic failure state and nothing is written.

### Address standing

An account's address has one of three standings. The standing, not the mere presence of the address, decides what the address may do.

- **Domain-confirmed**: a sign-in through one of the account's bound identities carried *authoritative* evidence for the address (below). At most one account holds an address domain-confirmed.
- **Email-confirmed**: the signed-in user proved current control of the mailbox with a Paceday confirmation message sent to the address (below). At most one account holds an address email-confirmed, and only while no account holds it domain-confirmed.
- **Unconfirmed**: everything else.

**Evidence a sign-in carries for its reported address:**

| Level | Google | Microsoft work account | Microsoft personal account | Organisation SSO |
|---|---|---|---|---|
| Authoritative | email verified **and** hosted domain equals the address's domain; or email verified on a Google consumer domain (gmail.com, googlemail.com) | the sign-in's tenant lists the address's domain among its verified domains (subject to OQ-3 on unmanaged directories) | an address on a Microsoft consumer mail domain, only if OQ-3 names an answer field that reports it as the account's own sign-in address; otherwise none | the issuer configured for the address's exact domain, and the ID token does not say the email is unverified |
| Mailbox | email verified, any other domain | — | only if OQ-3 names an answer that says Microsoft verified the address; otherwise none | — |
| None | email not verified, or flag absent | the tenant does not verify the domain, or the lookup fails | as above | — |

Mailbox evidence is weaker than a Paceday confirmation: Google's flag records that a mail reached the address at some past time and stays set after the mailbox is lost (§1 case 1). Authoritative evidence comes from the party that controls the address's domain.

**What standing allows:**
- Only a domain-confirmed or email-confirmed address is a person. Every address-as-person site in §2 (team-invite acceptance, scheduling-link co-hosts in all three places, the manager's member add, remove and roster import) resolves an address only to the account that holds it confirmed, preferring the domain-confirmed holder. An unconfirmed address resolves to nobody. Organisation membership remains PAC-68's rule.
- An account whose address is unconfirmed works fully for its own user (calendar, scheduling, settings). Settings shows the address as unconfirmed and offers to confirm it by email.
- **Paceday email confirmation.** A signed-in user whose address is unconfirmed may ask for a confirmation message. It is sent to the account's address, carries a single-use code that expires, and confirms the address only when opened in a browser holding a session for that same account. It is refused while another account holds the address confirmed. Where the deployment has no mail sender configured, confirmation is unavailable and Settings says so.
- **Authority wins.** When a sign-in with authoritative evidence arrives for an address that another account holds only email-confirmed or unconfirmed, the authoritative sign-in is not refused because of it (unless rule 3 applies to a bound account it would otherwise reach). It reaches or creates its own account, which holds the address domain-confirmed. The other account loses the address standing (it becomes unconfirmed), keeps its binding, data and sessions, and is not deleted. Address-based grants it has already accepted stay accepted (D-9); pending ones resolve to the new holder from then on.
- Addresses are compared case-insensitively. Two addresses that differ only in letter case are the same address everywhere this spec says "address".

### The sign-in rule

1. **A sign-in reaches an account only through a bound identity.** If the identity is bound, the sign-in reaches that account, whatever address the provider now reports.
2. **Bound identities are found only by authority and subject.** The address is never consulted to choose the account for a bound identity.
3. **The address never links a sign-in to an account that already has a bound identity.** This holds whatever the provider says about the address, including authoritative evidence.
4. **A new identity creates an account** as follows:
   - with authoritative evidence: created, bound, and its address domain-confirmed, unless an account already holds the address domain-confirmed (refused, D-12), or an unbound account created before this change carries the address (then migration rule (b) applies instead);
   - with mailbox evidence: created, bound, and its address unconfirmed, unless any account carries the address at any standing (refused);
   - with no evidence: refused.
5. **A second identity is added to an account only through the link step** (below). It is never added by a sign-in, by the presence of a session cookie, by the organisation's "extended access" step (owner, PAC-68 OQ-2), or by any other provider round trip.

### The link step (adding a sign-in method)

The link step is distinct from sign-in. It is started only from a signed-in surface, by the signed-in user, and is tied to that session and to a single-use state. The provider's answer is attached to that same account. Before the browser leaves for the provider, the app states that the provider account will also become a way to sign in to Paceday.

- **Fresh proof.** Adding a *new* identity requires that the session holding the link step was issued, by a sign-in made under this change's rules, no more than 10 minutes before the link callback completes (the same freshness PAC-50 uses for break-glass). A session issued by the code before this change never qualifies, however recent. Otherwise the link is refused with a distinct "confirm it's you" outcome: nothing is written, the session is kept, and the app asks the user to sign in again with a method they already have, then retry.
- **Reconnecting an identity already bound to this account** only replaces its stored credential. It adds no sign-in method, so it needs no fresh proof.
- **An identity bound to another account** is refused (D-3); both accounts are unchanged.
- **Address standing.** If the linked identity carries authoritative evidence for the account's own address, the account's address becomes domain-confirmed (subject to "authority wins"). A linked identity reporting a different address changes nothing about the account's address (D-7).
- **Work calendar.** The link step changes the account's work calendar only when the user chose that provider as the work calendar in the control that started it (Settings work-calendar control, onboarding's calendar step). Every other link step, and every sign-in through an identity added by a link step, leaves the work calendar unchanged. In particular, the success path of a Microsoft sign-in through a link-added identity never sets the work calendar to Outlook.

**Signed-in connect surfaces become link steps.** Each of these starts the link step, never the public sign-in start: the Settings work-calendar control, the connection status control, the Dashboard "Connect calendar" banner, and the onboarding calendar step (§2 table). The public sign-in dialog keeps the public sign-in start. With today's scopes, linking Google or Microsoft also stores that provider's calendar credential; that is unchanged.

**Sign-in methods in Settings.** Settings lists every identity bound to the account: the provider (and, for SSO, the organisation), the address the provider reported when it was bound, when and how it was added (created the account, migration, link step). The user can:
- add a method (the link step), including the organisation's SSO when the organisation has one configured;
- remove any method that is not the last one. Removal requires the same fresh session as adding. After removal, that identity no longer reaches the account, and every other session of the account ends. The calendar credential of that provider is not touched by the removal; it is disconnected separately.

**Disconnecting a calendar** (Google or Microsoft) does not remove the sign-in method. The disconnect result tells the user, in the same message, that the provider account is still a way to sign in and where to remove it. Removing it silently could remove the user's only sign-in method; keeping it silently would leave a sign-in path the user has not seen. Showing it does neither.

### Accounts created before this change (migration)

Existing accounts carry no binding (§2). Until one binds, its address keeps resolving as today (it is treated as email-confirmed), so co-host and team invitations to existing users keep working. An existing account is bound **lazily, on its first sign-in after this change** that meets one of these conditions:

- **(a) The identifier matches, same authority.** The incoming identity is a Google or Microsoft identity, the account's recorded identifier equals that identity's subject in the same field the code recorded (Google: the userinfo identifier; Microsoft: Graph's user `id`), and the recorded value has the format that provider issues (the contract names the formats; a value that fits more than one provider's format never matches). An SSO subject never qualifies under (a): the configured IdP chooses it. The provider must report the account's address with at least mailbox evidence.
- **(b) Authoritative evidence, Google and Microsoft only.** The sign-in carries authoritative evidence for the account's address (Google hosted domain or Google consumer domain; Microsoft work tenant that verifies the domain). An SSO issuer is **not** authoritative over an existing account, bound or not (D-10, OQ-2's default).

On binding, the account's address standing is set from the sign-in's evidence (domain-confirmed for authoritative, unconfirmed for mailbox). Any other sign-in that matches an unbound existing account only by address is refused. Binding happens once and atomically: when two qualifying identities race for the same unbound account (a domain verified in both Google Workspace and Microsoft Entra), exactly one binds and the other is refused. After binding, the account follows the rules above.

- A user who used both Google and Microsoft before this change gets one of them bound by (a) or (b). They add the other through the link step.
- **Existing members and SSO.** An existing member starts using SSO by adding it from Settings' sign-in methods through the link step (fresh session required). If the organisation's issuer changes, the old SSO identity no longer matches and each member adds the new one the same way.
- **Case collisions.** If two existing accounts carry addresses that differ only in letter case, the deployment neither merges nor deletes them. Every address match against that address (sign-in, migration, address-as-person) is refused or resolves to nobody until an operator resolves it, and the server log names the collision.
- Nothing is deleted. No account, membership, credential or setting is removed by the migration or by a refusal (D-9).

### Refusal

When a sign-in cannot reach an account under these rules, the user is told so plainly on the web app's sign-in page. Examples are an address that belongs to an account bound to another identity, an unbound account without (a) or (b), or a new identity without evidence. The message says that Paceday could not sign them in with this account. It also says that if they already use Paceday, they should sign in the way they did before and add this sign-in method from Settings.

The message is the same for every refusal cause, so it does not reveal whether an account exists for the address (D-6). It is distinct from today's generic "sign-in did not complete" state.

A refusal:
- creates no account and binds nothing;
- stores no provider credential;
- changes no organisation membership, no work calendar and no address standing;
- issues no session, and leaves any session the browser already holds untouched (as PAC-35's failure state does today).

The server log records the cause. Link-step refusals (identity bound elsewhere, "confirm it's you") return to the signed-in surface with their own messages; they are not sign-in refusals and keep the session.

### Address changes and reuse at the provider

- A bound identity whose provider now reports a different address still reaches its account.
- The account's stored address does **not** change on sign-in (D-7). If the new address belongs to another account, that account is not touched.
- A provider that gives an address to a new subject (a recycled mailbox) does not reach the old holder's account. With authoritative evidence, the new subject is refused while the old account holds the address domain-confirmed; an operator resolves it (D-12).

### Organisation membership (PAC-68)

Membership is still decided by PAC-68's rule on the provider's answer for the sign-in. This spec only makes sure that rule is evaluated for the account the identity is bound to, and never for an account reached through an address. A refused sign-in evaluates membership for nobody.

### PAC-50 admin step-up and break-glass

- **Step-up.** When the account has a bound identity at the authority the step-up uses (Google, or the same Microsoft tenant), the step-up's identity must be that bound identity, in addition to today's checks. A step-up by another Google account or another object id with the same address fails as an identity mismatch. When the account has no binding at that authority, today's checks apply unchanged. The step-up adds no new consent beyond its own (owner, PAC-68 OQ-2).
- **Break-glass.** The operator's list still names addresses. A listed address authorises the account that holds that address **domain-confirmed**, identified by the session's account, not by the email claim the session carries. A session whose account holds the address only email-confirmed or unconfirmed, or no longer holds it, is not break-glass. The 10-minute freshness stays.

### Constraints from owner decisions

- Sign-in keeps exactly today's permissions and never requires a new admin consent (owner, PAC-68 OQ-2; PAC-50 AC-24). The Microsoft tenant and its verified domains, Google's verified flag and hosted domain are read from answers today's permissions return (OQ-3 records what stage 2 must verify). Where authoritative or mailbox evidence cannot be obtained, the sign-in is treated as having less evidence; a bound identity still signs in.
- A Microsoft sign-in must not reset an existing user's work calendar (owner, PAC-47 vs PAC-67 OQ-13). Today's reset on the plain Microsoft sign-in path is PAC-47's to remove. This spec adds no path that resets it: a sign-in through a link-added identity, a refusal, a link step not started from the work-calendar choice, and a migration binding never change the work calendar.

## 4. Acceptance criteria

Conventions:
- **"Henry"** is an existing account for `henry@acme.com`.
- **"Bound to X"** means X is among the account's bound identities. Unless stated, Henry's address is domain-confirmed.
- **"Refused"** means all of the following:
  - the callback ends on the refusal state of §3 (one fixed outcome, the same for every cause, distinct from the generic failure state);
  - no session is issued and an existing session cookie is neither replaced nor expired;
  - no account is created; no binding is written; no provider credential is stored on any account;
  - no account's organisation, work calendar or address standing changes.
- **"Reaches Henry"** means the session issued identifies Henry's account and the provider credential is stored on Henry's account.
- **"Fresh session"** means a session issued under this change no more than 10 minutes before the callback.
- Provider answers (including Microsoft's organisation answer) are injected with the existing fake-provider seams (`pac35_auth_flows_test.go` pattern), so every [unit] criterion is offline. The tests for AC-1 to AC-7b drive the real callback handlers, not a storage helper, because AC-23 may be dropped and they are then the only proof.
- **Today's scope sets** are those of §2 ("A test pins today's sign-in scopes").

**Takeover scenarios (§1)**

AC-1. Given Henry bound to Google identity G1, when Google identity G2 signs in with `henry@acme.com`, email verified, no hosted domain, then the sign-in is refused, and Henry's stored Google credential is byte-for-byte unchanged.  [unit]

AC-2. Given Henry bound to Google identity G1, when a Microsoft work account (tenant T9, object id O9) whose mail is `henry@acme.com` signs in, then it is refused, whether or not tenant T9 verifies `acme.com`.  [unit]

AC-3. Given Henry bound to Microsoft (T1, O1), when Google identity G2 signs in with `henry@acme.com`, email verified and hosted domain `acme.com`, then it is refused.  [unit]

AC-4. Given Henry bound to Microsoft (T1, O1), when (a) (T1, O2) with mail `henry@acme.com`, or (b) (T2, O1) with mail `henry@acme.com` signs in, then both are refused.  [unit]

AC-5. Given Henry bound to Microsoft work account (T1, O1), when a personal Microsoft account with address `henry@acme.com` signs in, then it is refused. Given instead an unbound pre-existing account for `henry@acme.com`, a personal Microsoft account carrying that address is also refused.  [unit]

AC-6. Given Henry bound to Google G1 and an OIDC SSO configuration for `acme.com` with issuer I1, when I1 asserts subject S1 with `henry@acme.com`, then it is refused. Given instead Henry bound to (I1, S1), when the configuration's issuer is changed to I2 and I2 asserts subject S1 with `henry@acme.com`, then it is refused.  [unit]

AC-7. Given no account for `new@acme.com`, when two different identities with authoritative evidence for that address complete sign-in concurrently, then exactly one account exists afterwards, bound to exactly one of them, and the other sign-in is refused. Given an identity already bound to account A, no operation binds it to a second account B: a link attempt from B is refused and A is unchanged.  [unit]

AC-7b. Given an unbound pre-existing account for `henry@acme.com`, when Google G2 (hosted domain `acme.com`, verified) and Microsoft (T1, O3) (tenant verifies `acme.com`) sign in concurrently, then exactly one identity is bound to the account, the other sign-in is refused, and no second account is created.  [unit]

AC-24 (pre-emptive squat). Given no account for `henry@acme.com`, when consumer Google identity GA signs in first with `henry@acme.com`, email verified, no hosted domain, then an account A is created bound to GA with the address unconfirmed. When Henry then signs in with Google G1, hosted domain `acme.com`, verified, then a new account H is created bound to G1 with the address domain-confirmed, A is no longer a holder of the address (unconfirmed), and A's binding and data are unchanged. Before H exists, a team invite, a scheduling-link co-host invitation and a manager's member add for `henry@acme.com` resolve to nobody; after H exists, they resolve to H, and A cannot accept the team invite.  [unit]

AC-25. Given account A carries `alice@example.org` (any standing), when another identity with only mailbox evidence for that address signs in, then it is refused.  [unit]

**Ordinary sign-in keeps working**

AC-8. Given Henry bound to an identity at Google, at Microsoft, or at SSO issuer I1 (three cases), when that identity signs in, then it reaches Henry with the same redirect target as today's success path. Today's success tests (`pac35_auth_flows_test.go:167`, `:239`, `:286`) keep passing with the binding in place.  [unit]

AC-9. Given Henry bound to Google G1, when G1 signs in reporting `henry.new@acme.com`, then it reaches Henry, and Henry's stored address and its standing are unchanged. If another account holds `henry.new@acme.com`, that account's session, credential, address, standing and binding are unchanged.  [unit]

**New accounts and evidence**

AC-10. Given no account for `alice@example.org`:
- when Google identity G5 signs in with that address, email verified, no hosted domain, then an account is created and bound to G5 with the address unconfirmed, and a second sign-in of G5 reaches it;
- the same with the flag false or absent is refused, and no account exists for the address afterwards.  [unit]

AC-10b. Given no account for the address, when Google identity G6 signs in (a) with `bob@gmail.com`, verified, or (b) with `carol@acme.com`, verified, hosted domain `acme.com`, then an account is created bound to G6 with the address domain-confirmed. With (c) `carol@sub.acme.com` and hosted domain `acme.com`, the address is unconfirmed.  [unit]

AC-11. Given no account for `dave@acme.com`, when a Microsoft work account (T1, O7) signs in with that mail, then:
- if tenant T1's verified-domain answer contains `acme.com`, an account is created, bound to (T1, O7), with the address domain-confirmed;
- if the answer does not contain it, or the lookup errors or times out, the sign-in is refused;
- the sign-in's authorize request carries exactly today's Microsoft scope set, and `TestPAC50_SignInAuthorizeURLsCarryNoNewScope` passes unchanged.  [unit]

AC-11b. Given a Microsoft sign-in whose answers do not let the server determine the tenant (no tenant answer for a work account, or an answer that cannot be classified as work or personal), then the sign-in ends on the generic failure state, and nothing is created, bound or stored. The tenant identifier and object identifier used for the binding come from Microsoft's answers for the same access token; a fake whose two answers disagree on the tenant fails the sign-in.  [unit]

AC-11c. Given no account for `erin@acme.com`, when a personal Microsoft account signs in with that address, then it is refused, unless OQ-3 has recorded an answer that gives mailbox or authoritative evidence for personal accounts, in which case the outcome follows the §3 evidence table.  [unit]

**Address standing and address-as-person**

AC-26. Given account A (address `alice@example.org`, unconfirmed) and a configured mail sender:
- when A's user asks for confirmation, then one message with a single-use, expiring code is sent to `alice@example.org`;
- opening it with A's session makes A's address email-confirmed; opening it with no session or with another account's session, after expiry, or a second time, changes nothing;
- the request is refused while another account holds the address confirmed;
- with no mail sender configured, the request is refused and Settings says confirmation is unavailable.  [unit]

AC-27. Given account E holds `henry@acme.com` email-confirmed and has a pending co-host invitation for it, when Google G1 signs in with hosted domain `acme.com`, verified, then a new account H holds the address domain-confirmed, E's address becomes unconfirmed, E's accepted grants stay accepted, and the pending invitation resolves to H.  [unit]

AC-28. For each address-as-person site in §2 (team-invite acceptance; scheduling-link co-host on create, on list patch, on add; manager member add, remove, roster import): given account U carries the address unconfirmed, the site resolves the address to nobody (team-invite acceptance answers that the invite is for a different address); given U holds it email-confirmed or domain-confirmed, the site resolves it to U as today.  [unit]

AC-29 (letter case). Given Henry holds `henry@acme.com`:
- Google G2 signing in with `HENRY@Acme.com`, verified, no hosted domain, is refused (it is Henry's address);
- each address-as-person site resolves `Henry@ACME.com` to Henry;
- a new account created from `Zoe@Example.org` is found by `zoe@example.org` at every site.
Given two pre-existing accounts carrying `x@acme.com` and `X@acme.com`, the deployment changes neither; a sign-in matching that address only by address is refused, address-as-person sites resolve it to nobody, and the server log names the collision.  [unit]

**The link step (rule 5)**

AC-12. Given Henry signed in with a fresh session (bound to Google G1, work calendar Google), when Henry starts the link step from the Settings work-calendar control choosing Outlook, and Microsoft answers with (T1, O1) and mail `h.work@other.org`, then:
- Henry's session still identifies Henry;
- the Microsoft credential is stored on Henry's account;
- Henry's work calendar is Outlook (his explicit choice in that control);
- no account for `h.work@other.org` is created, and Henry's address and standing are unchanged;
- a later sign-in by (T1, O1) with no session reaches Henry.
Today the same flow creates or switches to another account (§2).  [unit]

AC-12b. Given Henry signed in with a fresh session, bound to Microsoft (T1, O1), work calendar Outlook, when Henry starts the link step from the connection status control (Google) and Google answers with G7 and `henry@acme.com`, then G7 is bound to Henry, the Google credential is stored on Henry, Henry's work calendar is still Outlook, and a later sign-in by G7 with no session reaches Henry.  [unit]

AC-13. Given (T1, O1) is already bound to account A, when signed-in user B starts the link step and Microsoft answers with (T1, O1), then the link is refused: B's session is kept, and A and B are unchanged.  [unit]

AC-14. Given user A starts the link step, when the callback arrives carrying (a) no session or (b) a session for user B, then no identity is linked to A or to B. Given a browser holding A's session that completes an ordinary sign-in (not started as a link step) with identity X whose address is not A's, then X is not linked to A. The presence of a session cookie alone never links.  [unit]

AC-30 (fresh proof). Given Henry bound to Google G1:
- with a session issued 11 minutes before the link callback, a link step answered by a new identity (T1, O1) ends on the "confirm it's you" outcome; nothing is bound or stored and the session is kept;
- with a session issued 2 minutes before the callback by the code before this change (a token minted the old way), the same outcome;
- after Henry signs in again with G1, the same link step binds (T1, O1);
- with an 11-minute-old session, a link step answered by G1 itself (already bound to Henry) stores the new Google credential and succeeds.  [unit]

AC-31 (work calendar, no new reset). Given Henry bound to Google G1 and to Microsoft (T1, O1) added by a link step, with work calendar Google, when (T1, O1) signs in with no session, then it reaches Henry, the Microsoft credential is stored, and Henry's work calendar is still Google. The same holds after a link step for Outlook started from the connection status control or the Dashboard banner: the work calendar is unchanged.  [unit]

AC-32 (connect surfaces). In the web app, the Settings work-calendar control (Google and Outlook), the connection status control, the Dashboard "Connect calendar" banner and the onboarding calendar step each start the link step, never the public sign-in start; the public sign-in dialog's three starts (button, Google with a hint, Microsoft with a hint) still use the public sign-in start. Each link-starting surface shows, before leaving, that the provider account will also become a way to sign in.  [unit] web app

AC-33 (sign-in methods). Given Henry bound to G1 and (T1, O1):
- Settings lists both, each with its provider, the address reported when bound, and how and when it was added;
- with a fresh session, removing (T1, O1) unbinds it; a later sign-in by (T1, O1) with mail `henry@acme.com` is refused; the Microsoft calendar credential is still stored; every other session of Henry's account ends and the current one is kept;
- with a non-fresh session, removal ends on "confirm it's you" and nothing changes;
- removing G1 when it is the only method is refused.  [unit] backend; [unit] web app

AC-34 (disconnect is visible). Given Henry bound to G1 and (T1, O1), when Henry disconnects the Outlook calendar, then (T1, O1) is still bound, and the disconnect result in the web app says that the Microsoft account is still a way to sign in and where to remove it.  [unit] backend; [unit] web app

AC-35 (extended access). Given the owner's optional "extended access" step (PAC-68 OQ-2) completed by an identity not bound to the signed-in account, then no identity is bound or removed and no address standing changes.  [unit]

**Organisation SSO**

AC-36. Given Henry bound to Google G1, a member of `acme.com`, whose SSO is configured with issuer I1:
- I1 asserting (S1, `henry@acme.com`) at sign-in is refused (AC-6);
- with a fresh session, Henry adds SSO from Settings' sign-in methods, I1 answers (S1, `henry@acme.com`), and (I1, S1) is bound to Henry; a later SSO sign-in by (I1, S1) reaches Henry;
- after the configured issuer becomes I2, I2 asserting (S1, `henry@acme.com`) is refused, and Henry can add (I2, S1) the same way.  [unit]

AC-37. Given an unbound pre-existing account for `henry@acme.com` whose recorded identifier is the string X:
- the configured `acme.com` issuer asserting a subject with `henry@acme.com` is refused, and the account stays unbound;
- the same issuer asserting subject X is refused;
- a Microsoft identity whose Graph user id is the string X is not matched under migration (a) unless X has the Microsoft format and the recorded value cannot be a Google identifier.  [unit]

AC-38 (break-glass). Given `henry@acme.com` on the operator's break-glass list and a session under 10 minutes old:
- for an account that holds `henry@acme.com` domain-confirmed, an SSO write is authorised as break-glass, as today;
- for an account that carries it unconfirmed or email-confirmed, it is not;
- for a session whose email claim is `henry@acme.com` but whose account no longer holds the address (AC-27), it is not.  [unit]

**Accounts created before this change**

AC-15. Given an unbound pre-existing account for `henry@acme.com` whose recorded last-sign-in identifier is G1:
- when Google G1 signs in with that address verified, then it reaches the account and binds G1;
- a later Google G2 with `henry@acme.com` verified and no hosted domain is refused.  [unit]

AC-16. Given an unbound pre-existing account for `henry@acme.com` whose recorded identifier is G1:
- (a) Google G2 with `henry@acme.com` verified and no hosted domain is refused, and the account stays unbound;
- (b) Google G2 with hosted domain `acme.com` and email verified reaches the account, binds G2, and the address is domain-confirmed;
- (c) a Microsoft work account (T1, O3) whose tenant verifies `acme.com` reaches the account and binds (T1, O3);
- (d) a Microsoft work account whose tenant does not verify `acme.com` is refused.
None of (b) and (c) changes the account's work calendar.  [unit]

AC-17. Given an unbound pre-existing account for `bob@example.org` (non-Google consumer domain) whose recorded identifier is X, when the Google answer for G1 has a verified email but G1 ≠ X, then it is refused.  [unit]

AC-39. Given an unbound pre-existing account for `bob@gmail.com` whose recorded identifier is X, when Google G1 ≠ X signs in with `bob@gmail.com` verified, then it reaches the account, binds G1, and the address is domain-confirmed.  [unit]

AC-18. Given the change is deployed, then no account, membership, credential or setting row is deleted, and the count of accounts is unchanged. Binding is written only by a sign-in, not by the deployment. Before an existing account binds, the address-as-person sites resolve its address as today.  [unit] (migration test)

**Refusal, membership, work calendar, step-up, contract**

AC-19. Given any refusal in AC-1 to AC-39, the callback's redirect is the same for every refusal cause and differs from the generic `auth_failed` state. The web app, on that state, shows the §3 refusal message (it names neither the address nor whether an account exists) and a control that opens the sign-in dialog.  [unit] backend; [unit] web app (`smart-calendar-flow`)

AC-20. Given Henry is an organisation member with work calendar set to Google, when any Microsoft sign-in matching Henry only by address is refused, then Henry's organisation and work calendar are unchanged.  [unit]

AC-21. Given Henry bound to Google G1 and an organisation `acme.com` where PAC-50 step-up is required, when Henry's session completes a Google step-up whose identity is G2 (also `henry@acme.com`, verified, hosted domain `acme.com`, admin), then the step-up fails as an identity mismatch and no grant is issued. With G1 it succeeds as today. The Microsoft analogue holds for Henry bound to (T1, O1) and a step-up identity (T1, O2).  [unit]

AC-22. Given the contract, then the three sign-in callbacks document the refusal outcome as a distinct, fixed redirect, alongside today's documented outcomes; the link step documents its success, "identity bound elsewhere" and "confirm it's you" outcomes; and the sign-in methods list, removal and email confirmation are documented operations.  [contract]

AC-23. Given the full stack with a sign-in stand-in, when a second identity carrying Henry's address signs in, then the API's current-user response for any session the browser holds is not Henry's. If the plan does not add a sign-in stand-in, it drops this criterion with that reason (OQ-4), and AC-1 to AC-7b at unit level are the proof.  [e2e]

## 5. Explicitly out of scope

- **Removing today's work-calendar reset on the plain Microsoft sign-in path.** PAC-47 owns it (owner, PAC-47 vs PAC-67 OQ-13). This spec adds no new reset path (AC-31).
- **Organisation membership rules.** PAC-68 owns them. This spec changes only *which account* the rule is applied to.
- **Revoking sessions issued before deployment.** Sessions are self-contained 7-day tokens. A session obtained through address matching before the change stays valid until it expires, but it cannot add a sign-in method (AC-30), so the exposure ends with its expiry. Rotating the signing secret at deploy is an operator choice (OQ-6).
- **Notifying the user by mail when a sign-in method is added or removed.** Useful, not required to close the findings: adding needs fresh proof and is listed in Settings. Candidate follow-up.
- **Merging two existing accounts**, or administrator tools to rebind an account. A recycled address and a letter-case collision are resolved by an operator outside the product.
- **SAML.** Not implemented today. When it is, it must follow the §3 rules; recorded here so its spec inherits it.
- **Residual mailbox risk on domains without an authority.** On a domain that has no Google Workspace, no Microsoft tenant verifying it and no configured SSO, nobody can bring authoritative evidence. There, whoever controls the mailbox can confirm the address by email and receive what is addressed to it. That is the same trust a mailed invitation already places in the mailbox, and authority still wins the moment the domain's owner signs in with it.

## 6. What must not change

- **An existing user's next ordinary sign-in through the provider they used last still works.** Migration rule (a) covers it. Today's success tests protect the redirect and session issuance (`pac35_auth_flows_test.go:167`, `:239`, `:286`). No existing test protects *which account* is reached. That is a finding, and AC-8 and AC-15 are the new protection.
- **Sign-in requests exactly today's scopes** (owner, PAC-68 OQ-2; PAC-50 AC-24). `TestPAC50_SignInAuthorizeURLsCarryNoNewScope` (`backend/auth/pac50_signin_scopes_test.go:19-69`) protects it and must pass **unchanged**; AC-11 restates it for Microsoft. No identity scope (`openid`, `profile`, `email`) is added to the Google or Microsoft sign-in.
- **The session cookie is untouched on failure** (PAC-35, API-011). Protected by `pac35_auth_flows_test.go:142-165`, `:193-237`. The refusal state and the link-step refusals keep this property.
- **The current-user response's `provider` still means "provider of the first sign-in"** (`paceday.gen.go:1186-1197`).
- **The SSO email-domain binding** (PAC-45) stays, protected by `handlers_sso_test.go:161-195`.
- **PAC-50 step-up's existing refusals** (`pac50_sso_stepup_test.go`) keep their reasons. AC-21 adds an identity check on top of them; it does not replace them. Break-glass freshness (10 minutes) is unchanged; AC-38 narrows who qualifies.
- **PAC-67 credential lifecycle**: reconnecting refreshes the credential and clears a recorded loss; disconnecting revokes it. AC-30's reconnect case and AC-34 keep both.
- **Must change.** `TestPAC67_MicrosoftCallbackKeepsRefreshAndFlipsOnlyTheCaller` (`pac67_conferencing_test.go:322-358`) asserts that a session-less Microsoft callback lands on an account matched by address. Under this spec it must become a signed-in link step (AC-12) or a bound-identity sign-in (AC-8), keeping its PAC-67 assertions: refresh token kept, loss cleared, the other user unaffected. The owner already expects this test to be amended for PAC-47.
- **Must change.** The web app's signed-in connect surfaces (§2 table) stop using the public sign-in start (AC-32); their tests follow.

## 7. Rollback

- **Reverting the code** restores matching by address on every callback. That re-opens every §1 scenario, so a revert is a security regression and is release-noted as one.
- **Down migration.** Dropping the binding and standing records loses the bindings, the sign-in method list, address standings and pending email confirmations. Accounts, credentials and memberships are untouched. On a later re-apply, every account is "pre-existing" again and re-binds lazily under §3 (a)/(b); identities added by link steps must be added again.
- **Identities linked by the link step** whose address differs from the account's would, after a revert, again sign in to (or create) an account matched by *their own* address. Accounts that lost an address to "authority wins" would again collide on that address. Release-note both.

## 8. Open questions

- **OQ-1: does connecting a work calendar also add that provider account as a sign-in method? (owner, product).** Narrowed in revision 1. The security parts are now decided by the factory and are not open: adding a method needs fresh proof, every method is listed in Settings and can be removed, and a calendar disconnect says the method remains (§3; Revision 1, Blocking 2). What remains is product:
  - **Factory default:** yes. Connecting Google or Microsoft from a signed-in surface is the link step and adds the sign-in method, with the disclosure before leaving. Settings also offers "add a sign-in method" (needed for SSO).
  - **Alternative:** a calendar connection never adds a sign-in method; only "add a sign-in method" does. Users whose two providers share an address then need both steps.
  - **Owner:** Nicolas Hochart. **Resolve by:** owner answer before stage 4. Not blocking stage 2: both options have the same contract shape.
- **OQ-2: when an organisation enables SSO or changes its IdP, may existing members' accounts be reached through SSO without each member adding it? (owner, product).**
  - **Factory default:** no (D-10). An existing member adds SSO from Settings' sign-in methods (AC-36). If the issuer changes, each member adds the new one.
  - **Alternative:** the org's configured issuer counts as authoritative for its own domain even on existing accounts. That reopens §1 case 5 for whoever can change the configuration (PAC-50 admin step-up or break-glass).
  - **Owner:** Nicolas Hochart. **Resolve by:** owner answer before stage 4. Not blocking stage 2: production has zero users (`docs/specs/PAC-22.md:80`).
- **OQ-3: what stage 2 must verify about provider answers within today's scopes (contract-author / plan-author, technical).** No call was made for this spec. Stage 2 records the answer for each item, with the documentation or a recorded call:
  1. **Microsoft tenant.** Graph `GET /organization` (fields `id`, `verifiedDomains`) is documented with `User.Read` as its least-privileged delegated permission (known from documentation, not called). Stage 2 confirms it answers under exactly today's Microsoft scopes for a work account. `id` is the tenant; Graph `/me`'s `id` is the object id for a work account. Both are read with the same access token. The step-up already reads the same resource with wider scopes (`stepup.go:290`).
  2. **Personal-account classification.** Which answer, under today's scopes, positively marks a personal Microsoft account (for example `/organization` refusing or answering empty for it), and which identifier is its stable subject. If no positive signal exists, a Microsoft sign-in that is not classified as work fails (AC-11b) and the limit is release-noted.
  3. **Personal-account address evidence.** Whether any answer under today's scopes says Microsoft verified the personal account's address, or reports it as the account's own sign-in name on a Microsoft consumer domain. Default if none: personal accounts carry no evidence (AC-11c) and can only be added by the link step.
  4. **Unmanaged ("viral") Microsoft directories.** Whether a self-service directory created by email verification can still list a domain in `verifiedDomains`, and whether today's answers can tell such a directory apart. If it can list the domain and cannot be told apart, Microsoft tenant evidence is no better than mailbox evidence. That would also weaken PAC-68 and PAC-50's step-up, which rely on the same evidence, and it becomes an owner question (product trade-off: Microsoft-only sign-up versus that residual). Until then the factory treats it as authoritative and release-notes the residual.
  5. **Google.** That v2 userinfo under `userinfo.email` returns `verified_email` and, for Workspace accounts, `hd`, and that its `id` is the stable subject (known from documentation, not called).
  6. **Identifier formats** for migration (a): the formats Google's userinfo `id` and Graph's user `id` (work and personal) take, so that a recorded value matching more than one never matches.
  - **Resolve by:** the contract naming fields and formats. A human decision is needed only under item 4, or if any item would require a new permission, which the owner has already ruled out.
- **OQ-4: can e2e reach the callbacks? (e2e-author / plan-author).** Not today (`e2e/auth.ts:17-25`), the same as PAC-68 OQ-4. **Resolve by:** the plan, which either adds a sign-in stand-in or drops AC-23 with that reason.
- **OQ-5: link-step mechanics and ordering (plan-author / orchestrator).**
  - The link step depends on the browser carrying its session across the provider's redirect, the same fragility as API-026.
  - PAC-68 and PAC-47 change the same three callbacks.
  - **Resolve by:** the plan stating the order. Suggested: PAC-71 first, because PAC-68's membership guarantee (§3 "Limit") depends on it. AC-31 holds whatever the order.
- **OQ-6: rotate the session signing secret at deploy? (orchestrator / operator).** No longer required for safety (AC-30 stops old sessions adding a method). It still closes sessions obtained through address matching before the change, at the cost of signing every user out once. **Resolve by:** release note. Production has zero users.
- **OQ-7: deliver email confirmation in this ticket or a follow-up? (orchestrator).** It is the only part of this spec that needs a mail sender. If split out, every rule here still holds with "email-confirmed" never reached: unconfirmed addresses receive no address-based grants until the follow-up lands (safe, with the product cost that consumer-Google users on custom domains cannot accept team invites meanwhile). **Resolve by:** the plan.

## 9. Decisions

| ID | Decision | Source |
|---|---|---|
| D-1 | Identity = Google account id; Microsoft (tenant, object id) from the same token's answers; Microsoft personal account as its own authority; SSO (verified issuer, subject). Identifiers compared only with their authority. | **Owner** for Google and Microsoft (PAC-68 OQ-1, "Google sub, Microsoft oid+tid"). **Factory-assumed** for SSO, personal accounts, and same-token sourcing. |
| D-2 | The address never links a sign-in to an account that has a bound identity, even with authoritative evidence. | Issue direction, filed at the owner's request ("A matching email address alone never links"). **Factory-assumed** that it holds even for authoritative evidence. |
| D-3 | Mismatch is refused; never a silent merge. | Issue direction; **factory-assumed** specifics (§3 Refusal). |
| D-4 | The link step is the only way to add an identity. Signed-in connect surfaces start it. Connecting a calendar adds the sign-in method, disclosed. | **Factory-assumed**; product aspect open as OQ-1. |
| D-5 | Three evidence levels (authoritative, mailbox, none) and three address standings (domain-confirmed, email-confirmed, unconfirmed). Only confirmed addresses act as a person. Authority wins over lower standing. | **Factory-assumed** (revision 1, Blocking 1). Closes the pre-emptive squat: Google's verified flag no longer makes an address a person. |
| D-6 | One refusal message for every cause, so it does not reveal whether an account exists. | **Factory-assumed**. A foreign-tenant admin can set any address, so a distinct message would let anyone enumerate accounts. |
| D-7 | The account's address does not change when the provider reports a new one. | **Factory-assumed**. |
| D-8 | Pre-existing accounts bind lazily on the first post-deployment sign-in meeting (a) (same-authority identifier, Google/Microsoft only) or (b) (Google/Microsoft authoritative evidence); everything else is refused. Binding is atomic. | **Factory-assumed**, following the issue ("records the current provider identity on the next verified sign-in"). Revision 1 removes SSO from (b) and from (a). |
| D-9 | Nothing is deleted: accounts, memberships, credentials, settings, accepted grants. | Factory rule; issue body. |
| D-10 | SSO is never authoritative over an existing account, bound or not; existing members add SSO by the link step; an issuer change requires adding the new one. | **Factory-assumed**; OQ-2. |
| D-11 | Disconnecting a calendar does not remove the sign-in method, and says so to the user. Removal is in Settings' sign-in methods. | **Factory-assumed** (revision 1, Blocking 2). Silent removal could remove the last method; silent retention hides a sign-in path. |
| D-12 | A recycled address at the provider (new subject) is refused while the old account holds it domain-confirmed; an operator resolves it. | **Factory-assumed**. |
| D-13 | PAC-50 step-up also requires the bound identity when the account has one at that authority. Break-glass authorises the account holding the listed address domain-confirmed, not a session email claim. | **Factory-assumed**. |
| D-14 | Sign-in requests exactly today's scopes; identity evidence comes from today's answers. | **Owner** (PAC-68 OQ-2, "offer both"; PAC-50 AC-24). |
| D-15 | A Microsoft sign-in must not reset an existing user's work calendar. This spec adds no reset path: link-added identities, link steps not started from the work-calendar choice, migration binding and refusals never change it. | **Owner** (PAC-47 vs PAC-67 OQ-13). |
| D-16 | Adding or removing a sign-in method requires a session issued under this change within 10 minutes; otherwise re-sign-in with an existing method. Reconnecting an already-bound identity does not. Removal ends the account's other sessions. | **Factory-assumed security decision** (revision 1, Blocking 2). Overrides revision 0's OQ-1 default ("nothing else added") for its security part; the product part stays with the owner (OQ-1). |
| D-17 | Addresses are compared case-insensitively everywhere; existing case collisions are left untouched and refused until an operator resolves them. | **Factory-assumed** (revision 1, Non-blocking 5). |
| D-18 | Paceday email confirmation proves current mailbox control; it makes an address act as a person only while no domain-confirmed holder exists. | **Factory-assumed** (revision 1, Blocking 1). Google's flag is a past, third-party check (§1 case 1). |
| D-19 | The organisation's "extended access" step never binds, links or removes an identity. | **Factory-assumed** (revision 1, Non-blocking 7). |

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

## Revision 1 — 2026-10-28

Answers every finding of "Challenge, revision 0". §1–§9 above are rewritten in place; the challenge text is kept unchanged above this section. AC numbers from revision 0 keep their meaning where they still exist; new criteria are AC-7b, AC-10b, AC-11b, AC-11c, AC-12b and AC-24 to AC-39.

### Blocking

| # | Finding | Resolution |
|---|---|---|
| B1 | Google's verified flag is not ownership; pre-emptive squat; §5 claim false; team invites and other address-as-person sites missing | **Applied.** §3 "Address standing": three evidence levels and three standings. Only Google `hd` equal to the domain, a Google consumer domain, a Microsoft tenant that verifies the domain, or the exact-domain SSO issuer is authoritative (domain-confirmed). A verified consumer Google account on any other domain creates an account whose address is **unconfirmed** and acts as nobody until a Paceday email confirmation (D-18), which proves current control rather than a past check. Authority wins over lower standing, so the squatter cannot lock out the domain's owner. Every address-as-person site is listed in §2 (team-invite acceptance `handlers_teams.go:300-305`, co-hosts `:377-382`/`:553`/`:656-660`, manager `handlers_manager.go:319`/`:356`, `engine/manager.go:244`, break-glass `handlers_sso_stepup.go:238`) and resolves only confirmed holders (AC-28). Pre-emptive squat: AC-24, AC-25, AC-27. §5's false claim is replaced by an explicit residual (domains with no authority). Google and personal Microsoft are now consistent: both follow the evidence table; personal Microsoft differs only because no answer is known to carry a verified flag (OQ-3 items 2–3, AC-11c). |
| B2 | Link step creates a permanent, invisible, irremovable sign-in method | **Applied.** §3 "The link step": adding a new identity needs a session issued under this change within 10 minutes, else "confirm it's you" and re-sign-in with an existing method; pre-change sessions never qualify (AC-30). Settings lists sign-in methods and removes a non-last one with the same freshness; removal ends the account's other sessions (AC-33). The link surface discloses that the account becomes a sign-in method (AC-32). Calendar disconnect keeps the method but tells the user and where to remove it (D-11, AC-34): silent removal could remove the last method, silent retention would hide a sign-in path. This **overrides revision 0's OQ-1 default as a factory-assumed security decision (D-16)**; OQ-1 stays open for the owner only on the product aspect (does a calendar connection add a sign-in method). |
| B3 | SSO inconsistent across migration (b), D-10/OQ-2 and OQ-1; (a) compares identifiers without provider; break-glass by email; no SSO link step | **Applied.** SSO removed from migration (b), consistent with OQ-2's default (D-8, D-10). Migration (a) is Google/Microsoft only, same field, same-format; an SSO subject never qualifies (AC-37). Identifiers are compared only with their authority (§3 "Identities", D-1). Break-glass authorises the account holding the listed address domain-confirmed, not the session's email claim (D-13, AC-38). Existing members start SSO by adding it from Settings' sign-in methods through the link step; issuer change requires adding the new one (AC-36). Migration ACs for SSO (AC-37) and the gmail branch (AC-39) added. |
| B4 | New work-calendar reset path through a linked Microsoft identity | **Applied.** §3 "The link step" → Work calendar, §3 constraints, D-15: a sign-in through a link-added identity never sets the work calendar (it must not call the reset at `microsoft_oauth.go:90`); a link step changes it only when started from the work-calendar choice (Settings control, onboarding step); migration binding never does. AC-31; AC-12 and AC-12b pin both sides; AC-16 adds "no work-calendar change" for migration. Holds independently of the PAC-47 order (OQ-5). |
| B5 | Link-step entry points incomplete | **Applied.** §2 table lists every `authConnectUrl` caller in `smart-calendar-flow` `eea0cf6` (`WorkCalendarConnection.tsx:87`, `ConnectionStatus.tsx:18`, `Dashboard.tsx:475`, `Onboarding.tsx:80`; `git grep` finds no other) and the sign-in dialog's three starts (`AuthDialog.tsx:79,98,102`). All four signed-in surfaces become link steps (AC-32, web). Same-address reconnect of an already-bound identity needs no fresh proof (AC-30, last bullet). Google analogue of AC-12: AC-12b. |
| B6 | §6 scope guard wrong; AC-11/OQ-3 allow added scopes | **Applied.** First option: identity evidence within exactly today's scope set. §2 and §6 cite `pac50_signin_scopes_test.go:19-69`, which must pass unchanged; AC-11's third bullet is now "exactly today's Microsoft scope set". No `openid` is added; the Microsoft tenant comes from Graph `/organization` under `User.Read` with the same token (OQ-3 item 1 names what stage 2 must verify). |

### Non-blocking

| # | Finding | Resolution |
|---|---|---|
| N1 | Unmanaged ("viral") Microsoft directories | **Applied** as OQ-3 item 4: stage 2 must establish it; if such a directory can list the domain and cannot be told apart, it becomes an owner question shared with PAC-68 and PAC-50; until then factory treats tenant evidence as authoritative and release-notes the residual. |
| N2 | Source of the tenant id | **Applied.** §3 "Identities", D-1, AC-11b: tenant and object id from Microsoft's answers for the same access token; disagreement fails the sign-in. |
| N3 | Identifier formats | **Applied.** Migration (a) names the field per authority and requires the provider's format; OQ-3 item 6 asks stage 2 to name the formats; AC-37. |
| N4 | Concurrent binding of a legacy account | **Applied.** AC-7b. |
| N5 | Case-sensitive address comparison | **Applied.** §2 records today's case-sensitivity; §3 and D-17 make it case-insensitive; existing collisions untouched and refused; AC-29. |
| N6 | Google `hd` and secondary domains | **Applied.** AC-10b (c) pins that a secondary or sub-domain is unconfirmed; such users rely on migration (a), the link step from a domain-confirmed method, or email confirmation. Release note. |
| N7 | "Extended access" step must not bind | **Applied.** §3 rule 5, D-19, AC-35. |
| N8 | Missing address-as-person consumers | **Applied.** §2 table (adds `engine/manager.go:244` and `handlers_manager.go:319`, found while checking); AC-28. |
| N9 | AC-11 third bullet names no scope set | **Applied** with B6. |
| N10 | AC-23 may be dropped; unit tests must drive real handlers | **Applied.** §4 conventions. |

### Unfalsifiable criteria and citations

- AC-11 third bullet: now names the scope set and the existing test.
- AC-19 web half: now "shows the refusal message, naming neither the address nor whether an account exists, and a control that opens the sign-in dialog".
- `routes.go:33` → `:34` for the Google callback; the Microsoft callback is `:36`; public routes are `:33-42`; protected `:47-49`.
- §6 "No test protects Microsoft's scope set" → replaced by `pac50_signin_scopes_test.go:19-69`.
- `AuthDialog.tsx:79,102` → `:79,98,102`.
- New citations in revision 1 were each read at `ae5c2cf` / `eea0cf6`: `jwt.go:17-27`, `middleware.go:18-24`, `handlers_sso_stepup.go:43,228-252,238,511-513`, `stepup.go:290`, `handlers_teams.go:214,300-305,621-651`, `handlers_manager.go:319,356`, `engine/manager.go:244`, `users.go:37-40`, `handlers_microsoft_auth.go:83`, `WorkCalendarConnection.tsx:77`, `AuthCallback.tsx:8`, `Index.tsx:568`, `SignInFailedNotice.tsx:8`.

### Questions left for the owner

- **OQ-1 (narrowed)**: does connecting a calendar also add the sign-in method? Factory default: yes, disclosed, listed, removable.
- **OQ-2 (unchanged)**: may SSO reach existing members without each adding it? Factory default: no.
- **OQ-3 item 4 (conditional)**: becomes an owner question only if stage 2 finds that unmanaged Microsoft directories defeat tenant evidence.

## Challenge, revision 1 — 2026-10-28

**Verdict**: accept-with-changes.

Revision 1 fixes all six revision-0 blocking findings as written. I re-checked each one:
- B1: the evidence table and the standings exist, and a Google verified flag no longer makes a squatted address a person.
- B2: adding a method needs fresh proof, methods are listed and removable, and a disconnect tells the user.
- B3: SSO is out of migration (a) and (b), break-glass is keyed on the account, and an SSO link path exists.
- B4: AC-31 exists.
- B5: all four `authConnectUrl` callers are listed. `git grep` on `eea0cf6` finds no fifth.
- B6: the scope set is pinned to `pac50_signin_scopes_test.go:19-69`.

N1 to N10 are applied.

The new model opens six problems, and they block stage 2:
- two takeover or lock-out paths that the new standings create;
- a revocation site that the standings silently disable;
- a last-method rule that can leave an account with no usable method;
- a migration step that hands a real user's credential to an account other people may hold;
- a direct contradiction with PAC-47 on the same four controls.

What I checked:
- `clockwise-like` `origin/main` `ae5c2cf` (still the tip), this spec at `ea39bbf`, `origin/factory/pac-47-spec` `24e0833` and `origin/factory/pac-68-spec` `ec76099`.
- `smart-calendar-flow` `origin/main` `eea0cf6`, read with `git show` only.

I ran no code and called no provider endpoint. Line numbers below are this file at `ea39bbf`.

### Blocking

1. **Migration (b) puts a real user's fresh credential into an account that other people may still hold sessions or integrations on. So OQ-6's "no longer required for safety" (line 436) is false.**
   - Lines 180-182 bind an authoritative identity to *any* unbound pre-existing account that carries the address. They do not ask who created that account or who holds sessions on it.
   - §5 (line 389) keeps sessions from before the change valid for up to 7 days.

   *Attack:*
   - Before deployment, an attacker who receives mail at `henry@acme.com` signs in with a consumer Google account on that address. Henry has never used Paceday, so today's email matching creates account A.
   - The attacker connects their own Slack and Notion to A and sets a personal LLM key on it.
   - After deployment, Henry signs in for the first time with acme's Workspace (`hd=acme.com`). Rule (b) binds Henry's G1 to A and stores Henry's Google calendar credential on A.
   - The attacker's old session on A is a 7-day JWT, and there is no server-side session store (`e2e/auth.ts:13-15`). It now reads and writes Henry's calendar through the API.
   - The attacker's integrations on A keep receiving Henry's meeting data with no time limit. This is the "pre-account-hijacking" class.

   Revision 0's B2 closed this route through the link step. The migration route is still open.

   *Resolve:* state requirements for the moment (b) binds an identity other than the one the account recorded:
   - every session issued before the binding ends;
   - before continuing, the user sees the third-party connections, keys and sign-in-relevant settings the account already carries, and can drop them.

   Alternatively, make OQ-6's secret rotation mandatory and add the second requirement. Add an AC for the scenario either way.

2. **A squatter with mailbox evidence permanently locks out the person who holds the mailbox now. This contradicts §5's residual (line 393).**
   - Rule 4's mailbox branch (line 153) and AC-25 (line 258) refuse a new identity with mailbox evidence whenever *any* account carries the address, at *any* standing, unconfirmed included.
   - §5 says that on a domain with no authority, "whoever controls the mailbox can confirm the address by email". That person cannot: they cannot create an account at all, so they never reach AC-26's confirmation.

   *Attack:*
   - The domain `smallco.io` has no Workspace, no Entra tenant and no SSO.
   - A former employee's consumer Google account on `pat@smallco.io` is still marked "verified". The former employee signs in first and gets account A, unconfirmed.
   - Pat now holds that mailbox. Pat signs in with their own consumer Google account on the same address, verified, and is refused for good:
     - there is no authority that could win;
     - the refusal tells Pat to "sign in the way you did before" (line 191), and Pat never has;
     - §5 (line 391) rules out operator tools.

   The unconfirmed squatter gains nothing except the denial, and the denial is permanent and costs nothing.

   *Resolve:* choose one, and add the AC either way:
   - require that an address held only *unconfirmed* never stops another identity from creating its own account and confirming the address by email, with an email-confirmed holder then outranking unconfirmed carriers; or
   - record the lock-out as a limit the owner accepts, and correct §5.

   If email confirmation is deferred (OQ-7), say which of the two applies meanwhile.

3. **Domain-confirmed standing never lapses. A departed employee keeps the address as a person, and the domain's owner cannot give it to the new holder.**
   - Standing is set once, from one sign-in's evidence (line 125).
   - "Authority wins" only over email-confirmed or unconfirmed holders (line 143).
   - A recycled address is refused while the old account holds it domain-confirmed (line 207, D-12 at line 454).

   *Attack:*
   - While employed, Henry links his personal Gmail through the link step, with a fresh session.
   - Henry leaves acme. acme suspends his Workspace account G1 and later gives `henry@acme.com` to a new hire.
   - Henry keeps signing in with Gmail indefinitely. His account still holds `henry@acme.com` domain-confirmed, so:
     - he receives every team invite, co-host invitation and manager-roster resolution addressed to it (AC-28);
     - he stays break-glass if that address is on the operator's list (line 216, AC-38);
     - the new hire is refused (D-12).

   This is §1 case 1's "former employee whose mail still arrives", made permanent by the product.

   *Resolve:* state when domain-confirmed standing ends. For example:
   - it is lost when an authoritative sign-in arrives from a *different* subject of the *same* authority (the domain's owner reassigned the address); or
   - it lapses when the confirming identity has not signed in with authoritative evidence for a stated period.

   State which address-as-person sites and break-glass check the standing at the time of use. Add ACs for both:
   - a reassigned address moves to the new holder;
   - the departed holder's link-added method no longer makes the address a person.

4. **AC-28 turns a revoking site into a silent no-op, and AC-27 creates exactly the members it can no longer remove.**
   - AC-28 (line 293) makes the manager's member *remove* resolve an unconfirmed address to nobody.
   - At `handlers_manager.go:356-366`, a lookup that finds nobody skips `RemoveTeamMember` and still answers 204.
   - These accounts keep their accepted grants but are no longer resolved:
     - AC-27 (line 291) and D-9 keep the accepted grants of an account that loses its standing;
     - AC-24's squatter A and an account migrated with mailbox evidence (line 181) are in the same position.

   *Scenario:*
   - E accepted a team invite while email-confirmed.
   - G1 arrives with `hd=acme.com`, and E becomes unconfirmed.
   - The manager removes `henry@acme.com` and gets 204. E is still a team member, and there is no path in the product to remove E.

   The co-host list patch (`handlers_scheduling_links.go:551-556`) has the reverse problem. An accepted unconfirmed co-host who is still on the list is no longer "kept", so they are silently removed, against D-9.

   *Resolve:*
   - Split the address-as-person sites into two kinds:
     - *granting* sites (invite acceptance, co-host add, member add, roster import) resolve only confirmed holders;
     - *revoking or retaining* sites (member remove, the co-host list's keep set) act on every account that carries the address, at any standing.
   - Add an AC for removing a member who has lost their standing.

5. **The last-method rule is not airtight. An account can end up with no method that can sign in, and nothing recovers it.**

   Line 171 forbids removing "the last one", counting bound identities. A bound identity can be dead:
   - (I1, S1) after the organisation's issuer changes. Line 185, and AC-36 at line 342, say it "no longer matches".
   - A Microsoft identity whose tenant answer can no longer be classified (AC-11b).

   *Scenario:*
   - Henry is bound to G1 and to (I1, S1).
   - acme moves to issuer I2.
   - Henry removes G1. The rule allows it because one method remains.
   - Henry is now locked out, and there is no support tool (line 391).

   Two more gaps:
   - The rule says nothing about two fresh sessions each removing a different one of two methods at the same time. Both see two methods, and the result is zero.
   - "Every other session of the account ends" (line 171, AC-33 at line 329) needs per-account session revocation. Today there is no session store, and logout only clears the cookie (`handlers_me.go:31-42`). Neither §2 nor §7 names this new state or how it rolls back.

   *Resolve:*
   - Define "last method" as the last method that can sign in now. An SSO identity counts only for the issuer currently configured for its domain.
   - Require removal to be atomic against a concurrent removal.
   - State the session-revocation requirement in §3, and in §7 with its down-migration consequence.
   - Add ACs for the dead-SSO case and the concurrent case.

6. **PAC-47 defines the same four connect controls differently, and PAC-71 does not mention it.**

   `docs/specs/PAC-47.md:377-411` (on `origin/factory/pac-47-spec` `24e0833`, revision 5) says a connect from those four actions:
   - "attaches the Microsoft credential to the account that started it, whatever email the Microsoft account has";
   - is credited while that account is still signed in, with no freshness condition;
   - adds no sign-in method. A later Microsoft sign-in "signs the browser in as the Paceday user for that Microsoft account (created if absent)".

   PAC-71 says the same controls:
   - start a link step that binds a sign-in method (line 167);
   - refuse a new identity on a session older than 10 minutes (line 161, AC-30);
   - make a later Microsoft sign-in with no session reach Henry (AC-12, last bullet).

   Both cannot be built as written, and whichever spec lands second breaks the other's ACs. OQ-5 (line 432) only orders the callbacks. OQ-1 (line 415) does not cite PAC-47, although PAC-47's model is OQ-1's "alternative".

   *Resolve:*
   - State which spec owns the semantics of the connect start and callback. Either:
     - record the fresh-proof rule as an amendment to PAC-47 §3 item 3; or
     - adopt PAC-47's connect without binding as the answer to OQ-1.
   - Name the PAC-47 ACs that change.

### Non-blocking

1. **The PAC-35 success tests will not "keep passing with the binding in place"** (AC-8 at line 262, §6 at line 397).
   - The Google fixture has no `verified_email` (`pac35_auth_flows_test.go:169`). Under rule 4 that is "no evidence, refused".
   - The Microsoft fixture serves only the `/me` profile and no organisation answer (`:241`). It ends on AC-11b's generic failure.
   - The failure-path tests at `:142`, `:160`, `:193` and `:208` change for the same reason: a refusal now comes before the session step they exercise.

   Say that their fixtures change and what each one must still assert.

2. **SSO configurations written before PAC-50's gate count as authoritative for new accounts** (evidence table, line 133).
   - Before `ae5c2cf`, any member of an organisation could write the SSO row.
   - Under "authority wins" (line 143), such an issuer can take the standing and pending invites of an email-confirmed holder at `acme.com`.
   - It can also create domain-confirmed accounts, and AC-38 makes those break-glass.

   Either require that an SSO configuration is authoritative only if it was written under PAC-50's gate, or release-note the residual.

3. **A stolen fresh session is a complete, silent takeover.**
   - With a session under 10 minutes old (malware, or an XSS on the API origin), an attacker can link their own identity, remove all of Henry's other methods, and end Henry's other sessions (AC-33).
   - Mail notification is out of scope (line 390), so nothing reaches Henry.

   Either state this residual in §5, or require that adding or removing a method is reported to the owner of the account through a channel the session does not control.

4. **Break-glass narrows more than §6 says.**
   - Line 177 treats unbound legacy accounts as email-confirmed, and line 216 requires domain-confirmed. So an operator's break-glass stops working until their account binds with authoritative evidence.
   - An operator whose address is on a domain with no authority loses break-glass for good.

   Release-note it, or ask the owner.

5. **Letter case (D-17, AC-29).**
   - Line 186: say whether migration (a) on a case-collided account counts as an "address match". The identifier matches, but the rule also requires the address.
   - Extend AC-7's race to `Zoe@example.org` and `zoe@example.org` arriving concurrently. As written it only covers one exact string.
   - AC-24 needs two accounts that carry the same address, but `users.email` is `UNIQUE` today (`006_auth.up.sql:5`). Say that uniqueness moves from "carries" to "holds confirmed", so that the contract and the plan know.

6. **OQ-7 (deferring email confirmation) is safe for security, but costs more than line 437 says.**
   - Legacy accounts that bind under (a) with mailbox evidence drop from "treated as email-confirmed" (line 177) to unconfirmed. They get no new invites and cannot recover until the follow-up.
   - Blocking 2 then has no remedy at all.

   Name the ACs that move with the follow-up: AC-26, the Settings offer at line 141, and the email-confirmed branches of AC-27 and AC-38.

7. **PAC-68 AC-15 allows `openid`; PAC-71 (line 398) forbids it.**
   - PAC-68 (accepted, `ec76099`) lets the plan add "sign-in identity scopes that need no tenant-admin consent" (D-5, AC-15).
   - PAC-71, `pac50_signin_scopes_test.go` and the owner's "keeps today's permissions" forbid any added scope.

   Record that PAC-71 supersedes PAC-68 AC-15's allowance, or have PAC-68 amended. Otherwise a PAC-68 plan that adds `openid` fails AC-11 here.

8. **Graph `/organization` under `User.Read` (OQ-3 item 1) is plausible but unverified.** From memory:
   - Microsoft documents `User.Read` as the least-privileged delegated permission for listing the organization.
   - With `User.Read`, the readable properties are limited to a few, including `id` and `verifiedDomains`.
   - Personal accounts are documented as not supported.

   If that holds, a personal account's answer is a refusal, which does not tell "personal" apart from a transient error. Under AC-11b every personal sign-in then fails. That is consistent with line 114, but release-note it.

9. **AC-27's "pending invitation resolves to H".** Co-host invitations are stored against a user id when they are created (`AddLinkHost(..., invitee.ID, "pending")`, `handlers_scheduling_links.go:382`). Team invites are stored against the address. Say whether E's existing pending co-host rows move to H, or stay and can no longer be accepted.

10. **The SSO link step (AC-36).** Say whether PAC-45's rule that the email must belong to `{domain}` still applies when SSO is *linked* rather than used to sign in.

### Criteria I could not falsify

- **AC-11b, second sentence.** Under today's Microsoft scopes, only one answer carries the tenant: `/organization`.
  - `/me?$select=id,mail,displayName` carries none (`handlers_microsoft_auth.go:90`).
  - No ID token is issued without `openid`.

  So "a fake whose two answers disagree on the tenant" has no second answer to disagree with. The step-up's mismatch check (`handlers_sso_stepup.go:512`) compares against an ID-token `tid` that sign-in does not have. Name the second source, or drop the clause.
- **AC-32, last sentence.** "Shows, before leaving, that the provider account will also become a way to sign in" names nothing observable: no text, role or confirmation control.
- **AC-11c.** It has two expected outcomes until OQ-3 is answered, so it can only be falsified after stage 2. That is acceptable if the contract records the answer.

Every other AC has a test that fails today and passes afterwards. For example:
- AC-24 fails today because the second Google sign-in upserts into A (`users.go:25`).
- AC-30 fails today because the Microsoft callback ignores the session and switches accounts (`handlers_microsoft_auth.go:69-79`).

### Citations I checked and found wrong

- **Revision 1 record (line 628), "public routes are `:33-42`".** `routes.go:38-39` (Zoom start and callback) are behind `requireAuth`. §2 at line 35 lists the public routes one by one and is correct.
- **AC-32 (line 325) and §2 (line 65), the dialog's "three starts".** There is a fourth: the SSO start at `AuthDialog.tsx:105-108`, which follows `redirect_url` from `/api/auth/detect`. It is a public start, so the requirement still holds, but the list is incomplete.
- **AC-8 (line 262) and §6 (line 397), "today's success tests keep passing".** They do not (Non-blocking 1).

Checked and correct at `ae5c2cf` / `eea0cf6`:
- `clockwise-like` backend:
  - `jwt.go:17-27`, `middleware.go:18-24`
  - `handlers_sso_stepup.go:43,228-252,238-244,494-533,511-513`, `stepup.go:172-182,290`
  - `handlers_teams.go:214,300-305,621-651`, `handlers_manager.go:319,356`, `engine/manager.go:244`
  - `users.go:21-31,37-40`, `006_auth.up.sql:5,8-9`, `paceday.gen.go:1186-1197`
  - `handlers_auth.go:43-84,232,235-240`, `handlers_microsoft_auth.go:64-97`, `handlers_sso.go:118-131,150-166`
  - `oidc.go:19-23,79-83`, `orgs.go:42-52`, `google_oauth.go:16-19`, `microsoft_oauth.go:17-18,40,90`
  - `handlers_scheduling_links.go:377-382,553,656-660`
- `clockwise-like` tests: `pac67_conferencing_test.go:322`, `pac50_signin_scopes_test.go:19`, `handlers_teams_test.go:53-60`, `e2e/auth.ts:13-25`.
- `smart-calendar-flow`: `client.ts:1181-1182`, `WorkCalendarConnection.tsx:77,87`, `ConnectionStatus.tsx:18`, `Dashboard.tsx:475`, `Onboarding.tsx:80`, `AuthDialog.tsx:79,98,102`, `AuthCallback.tsx:8`, `Index.tsx:568`, `SignInFailedNotice.tsx:8`.
- "No code reads `provider_id`" holds. The only other match is the JSON tag of an unrelated struct (`storage/sso_stepup.go:70`).

### Consumers this breaks

- `clockwise-like` `backend/api/pac35_auth_flows_test.go:142,160,167,193,208,239`: the fixtures carry no Google verified flag and no Microsoft organisation answer (Non-blocking 1).
- `clockwise-like` `backend/api/pac67_conferencing_test.go:322-358`: already named in §6.
- `clockwise-like` `backend/api/handlers_manager.go:356-366`: removal becomes a silent no-op for unconfirmed carriers (Blocking 4).
- `clockwise-like` `docs/specs/PAC-47.md:377-411` (`origin/factory/pac-47-spec`): the connect semantics conflict (Blocking 6).
- `clockwise-like` `docs/specs/PAC-68.md` AC-15 / D-5 (`origin/factory/pac-68-spec`): the `openid` allowance (Non-blocking 7).
- `smart-calendar-flow` `src/pages/Index.tsx:568` and `src/pages/AuthCallback.tsx:8`: they need a reader for the refusal state. AC-19 already covers this.

### Provider facts: known vs unverified

I called nothing. From memory, unverified:
- Graph `/organization` answers under `User.Read` with `id` and `verifiedDomains`, and does not support personal accounts (Non-blocking 8).
- An Entra domain can be verified in only one tenant at a time.
- Google Workspace `hd` is the primary domain only.

Whether unmanaged Entra directories still list a domain in `verifiedDomains` stays OQ-3 item 4. Google has had at least one reported flaw that let Workspace accounts exist on domains their creator did not verify. So `hd` evidence carries a residual of the same kind; release-note it next to item 4.
