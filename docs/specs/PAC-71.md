# Spec — PAC-71: Sign-in matches accounts by email alone — bind accounts to the provider identity

- **Linear**: https://linear.app/paceday/issue/PAC-71/sign-in-matches-accounts-by-email-alone-anyone-who-controls-an-address
- **Status**: draft, revision 4. Revision 2 narrowed the ticket to a secure core (§10 Split). Revision 3 answers "Challenge, revision 2" inside that same scope, without growing it. Revision 4 answers "Challenge, revision 3" only and changes nothing else. See "Revision 3" and "Revision 4" at the end.
- **Author**: spec-author
- **Priority**: Urgent (security)
- **Inherited scope**: none blocking. `contracts/openapi/paths/auth.yaml` carries no `x-uncertain` marker on the three sign-in callbacks. Related audit rows:
  - API-092 (auth handlers without Go tests; partly fixed, `docs/factory/api-audit.md:350`): every new callback outcome this spec adds gets a Go assertion.
  - API-026 (`api-audit.md:284`): a connect flow depends on the session cookie surviving the provider's redirect. The connect step here depends on the same thing (§8 OQ-5).
- **Origin**: PAC-68 spec OQ-1 (`origin/factory/pac-68-spec`, `docs/specs/PAC-68.md:144`), filed as PAC-71 at the owner's request (owner-decisions, PAC-68 OQ-1).
- **Verified against**: `clockwise-like` `origin/main` at `ae5c2cf` (still the tip on 2026-10-28); `smart-calendar-flow` `origin/main` at `eea0cf6`; `origin/factory/pac-47-spec` at `24e0833`; `origin/factory/pac-68-spec` at `ec76099`. Revisions 2 and 3 re-read every citation they add or change at those commits. Revision 3's inventory of what an account can carry was read from every migration in `backend/storage/migrations/*.up.sql` (001 to 030) at `ae5c2cf`. No code was run and no provider endpoint was called.
- **Orchestrator guidance (factory-level, 2026-10-28)**: revision 2 follows a scope instruction from the factory orchestrator, after two challenge rounds each found six blocking problems because the scope kept growing. The instruction is recorded here as factory guidance, not as an owner decision:
  1. narrow the ticket to the identity core;
  2. add per-account session revocation;
  3. make migration refuse rather than bind where another party may hold the account;
  4. drop the address-confirmation state machine;
  5. drop user-facing linking and removal of sign-in methods;
  6. align the signed-in connect controls with PAC-47.

  Each item is applied below and mapped in "Revision 2".
- **Orchestrator decisions for revision 3 (factory-level, fail closed, 2026-10-28)**. Recorded as factory decisions, not owner decisions:
  1. Migration rule (b) binds only an account that is **empty** of data and relationships (§2 "What an account can carry today", §3). Anything else is refused with the single message, logged, and left to the operator procedure. The "recorded value is an SSO subject" clause is removed.
  2. Every connect is bound at its start to the account and its session generation; its completion is refused if the generation moved. A binding is one atomic step that locks the account, re-checks its conditions, binds and advances the generation. Writes from sessions of an older generation fail after it.
  3. PAC-71 fails closed on the Microsoft tenant: without it, a Microsoft sign-in is refused with the single message, whether it would create, bind or reach a bound account. PAC-68 AC-9, AC-15's second clause and D-4 must amend.
  4. The operator procedure gains an operator-only, audited "unbind and reassign" step that advances the generation. Rule (a) binds the identity on record. Cheap non-blocking findings are applied; AC-7/AC-7b get an interleaving seam; AC-35 is dropped; AC-5's personal-account half fails closed.

  Each item is applied below and mapped in "Revision 3".

## 1. Problem

A Paceday account is found by its email address and nothing else. Whoever completes a sign-in with Google, Microsoft or an organisation's single sign-on that asserts an address becomes the Paceday user who holds that address. That includes the user's session, their stored calendar credential and their organisation membership. It does not matter which provider created the account, or whether that provider had any authority over the address.

Concrete account-takeover scenarios against an existing user Henry (`henry@acme.com`):

1. **Google, mailbox holder.** Someone who receives mail at `henry@acme.com` creates a personal (consumer) Google account on that address. A catch-all, a forwarding alias or a former employee whose mail still arrives can do this. Google marks the address "verified" because a mail reached it, once, and keeps it marked after the mailbox is lost. They sign in with Google and get Henry's account.
2. **Microsoft, any tenant's administrator.** The administrator of *any* Microsoft tenant can set one of their users' mail attribute to `henry@acme.com`. Sign-in uses Microsoft's multi-tenant endpoint, so that user signs in and gets Henry's account. Microsoft does not vouch for the mail attribute.
3. **Across providers.** Either case lands in Henry's account even when Henry created it through the *other* provider. For example, Henry joined through Google Workspace and the attacker arrives through Microsoft.
4. **Microsoft personal account vs work account.** A personal Microsoft account and a work account can carry the same address. They are different people as far as Microsoft is concerned, but Paceday treats them as one.
5. **Organisation single sign-on (PAC-22, PAC-50).** An organisation's SSO configuration names an arbitrary OIDC issuer. Members can write that configuration, gated since PAC-50 by an admin step-up or the operator's break-glass list. Any identity that issuer asserts with an `@acme.com` address lands in the existing account for that address, even if the account was created through Google or Microsoft. Swapping the issuer for a different IdP therefore hands every member's account to whoever controls the new IdP.
6. **Email change or reuse at the provider.** If Henry's address changes at his provider, today's matching sends him to a different (new) account. If the provider later gives `henry@acme.com` to someone else, that person gets Henry's account.
7. **Squatting, the reverse direction.** A mailbox holder (case 1) or a foreign tenant (case 2) creates the account *before* Henry first uses Paceday. When Henry arrives, he is matched into the attacker's account. Meanwhile the product treats the address as a person in several places: team invitations, scheduling-link co-hosts, a manager's team roster, and the operator's break-glass list (§2 "Address-as-person"). So a squatted account receives what is meant for the address's owner.

PAC-68 stops an unverified sign-in from being *granted* organisation membership. It does not stop anyone from reaching an existing member's account, and with it that membership (PAC-68 §3 "Limit"). The cost of the defect is full account takeover: calendar read/write through the stored credential, the user's organisation data, and admin standing where the user has it.

**What this ticket closes, and what it leaves to follow-ups (revision 2).** This ticket closes takeover: cases 1 to 6, and case 7's "matched into the attacker's account" together with the grants a squatter would receive. It does **not** give the address's rightful owner a product path to evict a squatter or to reclaim an address that a departed holder still carries. Those cases are refused safely, and an operator resolves them by a documented procedure (§3 "Operator procedure") until follow-up F2 (§10) ships. Adding, listing and removing sign-in methods moves to follow-up F1.

## 2. Current behaviour

**One conflict key: the email.** `users.email` is `NOT NULL UNIQUE` (`backend/storage/migrations/006_auth.up.sql:5`), plain text, so the uniqueness is case-sensitive. `provider` and `provider_id` are plain columns with defaults (`006_auth.up.sql:8-9`). `UpsertUser` inserts with `ON CONFLICT (email) DO UPDATE`. On conflict it overwrites `name`, `avatar_url` and **`provider_id`**, but not `provider` (`backend/storage/users.go:21-31`). So:
- `provider` records the provider of the row's *first* sign-in. The contract says so (`backend/api/gen/paceday.gen.go:1186-1197`).
- `provider_id` records the identifier of the *most recent* sign-in, from whichever provider it came (`users.go:28`). Nothing records which provider that identifier belongs to. For Microsoft it is Graph `/me`'s `id`; for SSO it is the issuer's `sub`, a string the configured IdP chooses.
- No code reads `provider_id`. Grep of `backend` outside tests and generated code finds it only in `users.go:23,28` (and the JSON tag of an unrelated struct, `storage/sso_stepup.go:70`).
- No callback lowercases the address before the upsert (`handlers_auth.go:64`, `handlers_microsoft_auth.go:69`, `handlers_sso.go:155`). `GetUserByEmail` compares exactly (`backend/storage/users.go:37-40`). Its scheduling-link callers lowercase the input first (`handlers_scheduling_links.go:378`, `:553`), so they do not find a stored mixed-case address.

**Routes.** The `/api/auth` group is at `backend/api/routes.go:32-50`.
- Public: Google start `:33`, Google callback `GET /api/auth/callback` `:34`, Microsoft start `:35`, Microsoft callback `:36`, detect `:40`, SSO start `:41`, OIDC callback `:42`.
- Behind `requireAuth`: Zoom start and callback `:38-39` (per-route, PAC-24), status `:47`, Google disconnect `:48`, Microsoft disconnect `:49`.

**Google callback** (`routes.go:34`; `backend/api/handlers_auth.go:42-84`):
- It checks the state cookie (`:43-48`), exchanges the code (`:50`) and reads the v2 userinfo endpoint (`:57`, URL at `:232`).
- The decoded profile has only `id`, `email`, `name`, `picture` (`:235-240`). Google's email-verified flag and hosted domain are not read.
- It upserts by email (`:64`), stores the Google token on the returned row (`:71`), associates the org by the email's domain (`:77`, `backend/storage/orgs.go:42-52`) and issues a session for that row (`:79`).
- Scopes are exactly `calendar` and `userinfo.email`, without `openid` (`backend/auth/google_oauth.go:16-19`).

**Microsoft callback** (`routes.go:36`; `backend/api/handlers_microsoft_auth.go:40-84`):
- It uses Microsoft's `/common` endpoint, so any tenant and personal accounts can reach it (`backend/auth/microsoft_oauth.go:17-18`).
- Scopes are exactly `Calendars.ReadWrite`, `User.Read`, `offline_access`, without `openid` (`microsoft_oauth.go:40`). So no ID token is issued and the tenant id is never seen.
- The profile is Graph `/me?$select=id,mail,displayName` (`handlers_microsoft_auth.go:90`, struct `:93-97`). It carries no tenant. `mail` is the directory attribute that tenant administrators set. A sign-in without it fails (`:65`).
- It upserts by `mail` (`:69`) and saves the Microsoft token on the returned row (`:74`). `SaveMicrosoftToken` also sets that row's `calendar_provider = 'outlook'` (`microsoft_oauth.go:90`). This is the work-calendar reset that the owner ruled must go (PAC-47 vs PAC-67 OQ-13); PAC-47 removes it (PAC-47 D-17).
- It then associates the org (`handlers_microsoft_auth.go:78`), issues the session (`:79`) and redirects with `?provider=outlook` (`:83`).
- **Where the tenant could come from under today's scopes.** No answer the callback reads today carries the tenant. The only documented source within today's scopes is Graph `/organization` (`id`), a second call with the same access token (OQ-3 item 1). Two other candidates exist and are unverified: the access token itself, which Microsoft issues as a JWT that usually carries a `tid` claim but documents as opaque to the client; and the `client_info` value that Microsoft's v2 token endpoint returns when the token request asks for it (`uid` and `utid`), which needs no scope. Neither is read today (OQ-3 item 1).

**Sign-in and connect starts are plain GETs with a 5-minute state cookie.** The Google and Microsoft starts are `GET` routes (`routes.go:33`, `:35`). Each sets an `oauth_state` cookie, `SameSite=Lax`, `MaxAge` 300 seconds (`handlers_auth.go:30-36`, `handlers_microsoft_auth.go:30-34`). The state is a random value, bound to nothing: not to a user, not to a session. A stored Google or Microsoft credential is one row per user and calendar (`017_oauth_tokens_per_user.up.sql:12`), so a later connect replaces it.

**OIDC SSO callback** (`routes.go:42`; `backend/api/handlers_sso.go:103-167`):
- The issuer, client id and secret come from the SSO provider row for `{domain}` (`:118-131`). An org member writes that row behind PAC-50's step-up or the break-glass list (`handlers_sso.go:183-247`, `handlers_sso_stepup.go:228-252`). Before PAC-50 any member could write it.
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

`git grep authConnectUrl` on `eea0cf6` finds no other caller outside tests and generated code. The public sign-in dialog has four starts (`src/components/auth/AuthDialog.tsx`):
- `:79`, by button (Google or Microsoft);
- `:98`, Google with a login hint;
- `:102`, Microsoft with a login hint;
- `:108`, the SSO start, following the `redirect_url` that `/api/auth/detect` returns.

Both OAuth callbacks are public (`routes.go:34`, `:36`) and never look at the session the browser already holds. A signed-in user who connects Outlook therefore:
- lands in their own row only when the Microsoft `mail` equals their Paceday address;
- otherwise lands in, or creates, *another* account and is switched to it, because the callback issues a new session for whatever row the upsert returned (`handlers_microsoft_auth.go:69-79`).

The web app recognises only one failure state, `?error=auth_failed` (`src/pages/AuthCallback.tsx:8`, `src/pages/Index.tsx:568`, `src/components/auth/SignInFailedNotice.tsx:8`).

**PAC-47 already defines the connect for Outlook** (`docs/specs/PAC-47.md:377-411` on `origin/factory/pac-47-spec` `24e0833`, revision 5; not yet on `main`):
- An Outlook connection started by the signed-in user from one of the same four controls attaches the Microsoft credential to the account that started it, whatever email the Microsoft account has. It does not sign the browser in as anyone else, creates no account, and changes no other account.
- It is credited only while that account is still the one signed in when the Microsoft step completes; otherwise it is credited to nobody and changes no session (PAC-47 AC-3e, AC-3f).
- A Microsoft flow that no connect action started is a sign-in, wherever it was started, and a session being present does not make it a connect (PAC-47 D-18, AC-3h, AC-3i).
- A sign-in sets a work calendar only for the account it creates (PAC-47 D-17, AC-3j to AC-3l).

**Tests pin today's email matching.**
- `TestPAC67_MicrosoftCallbackKeepsRefreshAndFlipsOnlyTheCaller` (`backend/api/pac67_conferencing_test.go:322-358`) creates `alice` with no provider identity (`createTestUser`, `backend/api/handlers_teams_test.go:53-60`). It runs the Microsoft callback with **no session** and a Graph answer `id: ms-alice`, `mail: alice's address` (`pac67_conferencing_test.go:333-341`), and asserts that the token lands on alice (`:346-349`).
- PAC-47's AC-3i (a) and (b), AC-3k and AC-3l (`PAC-47.md:650-695` at `24e0833`) expect a Microsoft sign-in to reach an existing user whose address equals the Microsoft account's, including a user who signed up with Google.

**A test pins today's sign-in scopes.** `TestPAC50_SignInAuthorizeURLsCarryNoNewScope` (`backend/auth/pac50_signin_scopes_test.go:19-69`, PAC-50 AC-24, owner decision 2026-10-27) asserts that each sign-in's scope parameter is **exactly** today's set:
- Google `{calendar, userinfo.email}`;
- Microsoft `{Calendars.ReadWrite, User.Read, offline_access}`;
- OIDC `{openid, profile, email}`.

It also asserts that none carries a step-up or admin-consent scope. PAC-68 (accepted, `ec76099`) allows the plan to add "sign-in identity scopes that need no tenant-admin consent", including `openid` (`PAC-68.md:115` AC-15, `:158` D-5).

**Sessions.**
- Sessions are 7-day HS256 JWTs carrying the user id, the email and an issue time (`backend/auth/jwt.go:11-27`).
- There is no server-side session store (`e2e/auth.ts:13-15`). The only validation site is `requireAuth` (`backend/api/middleware.go:39`, `ValidateToken` at `:57`), which reads no server state.
- Logout only expires the browser's cookie (`backend/api/handlers_me.go:31-42`). Nothing can end a session held by another browser before it expires.
- The issue time is already read for break-glass freshness (`middleware.go:18-24`, `:71-73`; `breakGlassFreshness = 10 * time.Minute`, `handlers_sso_stepup.go:43`).
- `requireAuth` is the only caller of `ValidateToken`. Every failure in it answers 401 (`middleware.go:52-66`). There are no per-user API or MCP tokens: the MCP server uses one shared `MCP_AUTH_TOKEN` (`mcp/main.go:29`). So a check in `requireAuth` covers every authenticated way in that exists today.
- The provider callbacks are public routes (`routes.go:34`, `:36`), so `requireAuth` never sees them.

**What an account can carry today.** Every table that refers to an account, read from the migrations at `ae5c2cf`. Revision 3's migration rule (b) (§3) uses this inventory.

| Kind | Where it is stored (`backend/storage/migrations/`) |
|---|---|
| Account row itself | `users` (`006_auth.up.sql:3-12`; `org_id` from `007_organizations.up.sql:8`) |
| Google calendar credential | `oauth_tokens.user_id` (`006_auth.up.sql:14`, one per user and calendar `017_oauth_tokens_per_user.up.sql:12`) |
| Microsoft and Zoom credentials | `settings.microsoft_tokens` (`003_calendar_providers.up.sql:2`), `settings.zoom_tokens` (`005_conferencing.up.sql:2`) |
| Personal AI keys and destination | `settings.llm_api_key`, `llm_base_url`, `llm_provider`, `llm_model` (`001_initial.up.sql:19-22`); `settings.llm_api_key_openai`, `_anthropic`, `_azure_openai`, `_bedrock`, `_vertex` (`029_llm_bring_your_own.up.sql:16-20`) |
| Other settings that point outside Paceday | `settings.webcal_url`, `calendar_email` (`003_calendar_providers.up.sql:3-4`); `settings.recap_channel_id`, `recap_send_to` (`015_daily_recap.up.sql:4-6`) |
| All other settings (preferences) | `settings` (`001_initial.up.sql:1-24`, extended by migrations 002, 003, 005, 012, 015, 020, 021, 025, 027; one row per user, `018_per_user_settings_and_calendars.up.sql:22`) |
| Slack and Notion connections | `workspace_connections.user_id` (`014_workspace_connections.up.sql:1-13`) |
| Personal calendars (with credentials) and their blockers | `personal_calendars.user_id` (`004_personal_calendars.up.sql:1-9`, `006_auth.up.sql:17`), `personal_blockers` (`004_personal_calendars.up.sql:11-18`) |
| Record of a lost credential | `credential_losses.user_id` (`028_credential_losses.up.sql:8-13`) |
| Team created, team membership, invites sent | `teams.created_by`, `team_members.user_id`, `team_invites.invited_by` (`013_teams.up.sql:1-26`) |
| Scheduling link owned; host or co-host on any link (any status) | `scheduling_links.owner_user_id`, `scheduling_link_hosts.user_id` (`009_scheduling_links.up.sql:1-24`) |
| Bookings and the events they created | `bookings` (by `link_id`, `009_scheduling_links.up.sql:26-36`), `booking_events.user_id` (`:38-44`) |
| Manager and roster | `user_profiles` (`016_manager.up.sql:1-7`); `manager_team_members.manager_user_id` and `.member_user_id` (`:9-23`); `one_on_one_occurrences`, `team_analytics_cache` by `manager_user_id` (`:25-46`); `manager_team_member_assignments.manager_user_id` (`022_manager_team_assignments.up.sql:1-9`) |
| Stored calendar-derived data | `focus_blocks.user_id` (`001_initial.up.sql:35-42`, `006_auth.up.sql:16`); `habits.user_id`, `habit_occurrences` (`010_habits.up.sql:1-26`); `analytics_weeks.user_id` (`011_analytics.up.sql:1-21`); `meeting_briefs.user_id` (`014_workspace_connections.up.sql:15-25`); `recap_sends.user_id` (`015_daily_recap.up.sql:11-19`); `audit_log.user_id` (`026_audit_log_user_id.up.sql:8`) |
| Organisation admin standing | `sso_stepup_grants.user_id` (`030_sso_admin_stepup.up.sql:20-30`); `sso_events.user_id`, kinds `sso_stepup` and `sso_provider_written` (`:39-46`) |

Notes on the inventory:
- No table stores tasks or calendar events as such. Events live in the provider's calendar, reached through the credentials above. "Tasks" in the product are calendar events.
- There is no persistent organisation-admin role column. Admin standing exists only as step-up grants and as the record of SSO writes (PAC-50).
- `user_profiles` is created on read with defaults (`storage/manager.go:21-29`), so a row at its defaults says nothing about use.
- `settings` rows are created on demand with defaults (`storage/settings.go:271`, `storage/credentials.go:132`, `auth/microsoft_oauth.go:87`).
- *Preference values the code writes by itself (revision 4).* Nothing records whether a user or the code wrote a setting: there is no per-field origin, and `updated_at` is not a use signal. The code writes settings without a settings request in these places:
  - row creation at the column defaults (`storage/settings.go:271`, `storage/credentials.go:132`, `auth/microsoft_oauth.go:87`);
  - storing a Microsoft credential also sets `calendar_provider = 'outlook'` (`auth/microsoft_oauth.go:90`), and removing that credential leaves `calendar_provider` alone (`storage/credentials.go:184-194`, PAC-67 D-3);
  - storing a Zoom credential sets `conferencing_provider = 'zoom'` (`storage/credentials.go:148`); removing it sets `'meet'` (`:179-182`), which is the column default (`005_conferencing.up.sql:1`); removing a Microsoft credential turns `'teams'` into `'meet'` (`:193`, in the statement at `:191-194`; citation corrected at the contract stage, challenge r4 Non-blocking 2);
  - every settings save normalises the working-hours and lunch schedules before storing them (`storage/settings.go:93-108`, called at `:327`), so a stored schedule can differ in form from the column default (`020_schedule_preferences.up.sql:3-4`) while meaning the same thing;
  - the scheduler advances the auto-schedule watermark, only while auto-schedule is on (`storage/settings.go:543-556`).

  So "the user changed it through an endpoint" cannot be decided from today's data. "The stored value differs from the default" can, once each preference's default and normal form are named (§3 rule (b)).
- *Credentials are loaded after authentication (revision 4).* A handler authenticates the request first, loads the account's calendar credential afterwards, then calls the provider. Two examples:
  - listing events: `api/handlers_calendar.go:162` loads through `getCalendarClient` (`:137-144`), which reads the stored token through `engine.LoadCalendarToken` (`engine/cal_iface.go:32`); the provider call is at `handlers_calendar.go:168`;
  - creating a meeting: `api/handlers_schedule.go:206` calls `SmartScheduler.CreateMeeting`, which opens the calendar client first (`engine/smart_schedule.go:288-289`).

  The same pattern is in `handlers_events.go` (`calClient`, `:27-29`), `handlers_nlp.go:63`, and the focus and personal-calendar handlers.
- Organisation membership is a column of the account row (`users.org_id`). It exposes the member list of that organisation (`storage/orgs.go:55-58`, `handlers_org.go:26-31`), not any calendar data.
- Other accounts read a member's calendar through relationships, not through anything on their own account. Team availability reads every member's stored credential (`handlers_teams.go:492-533`, `engine/team_availability.go:78-84`). A public scheduling link reads each accepted host's credential (`handlers_booking.go:80-88`).

**PAC-50 step-up and break-glass compare email, not identity.**
- The step-up identity carries email, verified flag, `hd`, `tid`, `preferred_username` and roles, but no subject or object id (`backend/auth/stepup.go:172-182`).
- Google step-up requires `hd` equal to the org domain, a verified email equal to the session's email, and admin status (`backend/api/handlers_sso_stepup.go:494-508`).
- Microsoft step-up requires:
  - the tenant's verified domains to contain the org domain, and the `/organization` tenant id to equal the token's `tid` (`:511-513`; `/organization?$select=id,verifiedDomains` at `stepup.go:290`, read with the step-up's own wider scopes);
  - the tenant pin to hold;
  - the username or email to equal the session's email;
  - a qualifying role (`:514-533`).
- Break-glass passes when the lowercased **session email claim** is on the operator's list and the session is under 10 minutes old (`handlers_sso_stepup.go:238-244`).

**Address-as-person.** These sites treat an address as a Paceday person. Revision 2 sorts them into two kinds. A **granting** site gives the address's holder something new. A **revoking or keeping** site takes something away, or decides what is kept.

| Site | Kind | Lookup today |
|---|---|---|
| Team-invite acceptance | granting | `backend/api/handlers_teams.go:300-305`: accepted when the session user's stored address equals the invitee address (case-insensitive). The invite is also mailed to the address with its token (`:214`, sender `:621-651`, only when `SMTP_HOST` and `SMTP_FROM` are set). |
| Scheduling-link co-host on create | granting | `handlers_scheduling_links.go:377-382` (input lowercased, exact match; invite stored against the user id, `:382`) |
| Scheduling-link add co-host | granting | `handlers_scheduling_links.go:656-660` |
| Scheduling-link co-host list patch, new entries | granting | `handlers_scheduling_links.go:547-562`: every listed address found is invited (`:558-561`) |
| Scheduling-link co-host list patch, keep set | keeping | `handlers_scheduling_links.go:550-557`: hosts whose address is not found are removed (`:557`). A stored mixed-case address is never found (lowercased input, exact match), so an accepted co-host stored that way is removed silently. |
| Manager adds a team member | granting | `backend/api/handlers_manager.go:319` (exact match) |
| Manager roster import | granting | `backend/engine/manager.go:244` (exact match) |
| Manager removes a team member | revoking | `handlers_manager.go:345-369`: `lower(email)` lookup with a single-row read (`:356`). If no account is found, the team membership is not touched, but the call still removes the manager assignment and pending invites (`:365-366`) and answers 204 (`:368`). If two accounts differ only in letter case, only one is read. |
| Manager roster joined to a formal team's members | granting | `storage/manager.go:155-168` (`AssignManagerTeamMembersMatchingFormalTeam`): roster addresses are joined to accounts with `lower(u.email) = lower(mtm.member_email)` and the account's team membership; an assignment row is created per match. |
| Manager creates a roster entry for a formal team member | granting | `handlers_manager.go:669-697` (`getOrCreateScopedMember`): a formal member whose stored address equals the requested address (case-insensitive) gets a new roster entry pointing at that account. |
| Manager lists members of a formal team | keeping | `handlers_manager.go:621-660` (`listManagerMembers`): formal members, already members by account, are merged with roster entries by their stored address (lowercased). Nothing is granted; it decides which roster entry represents an existing member. |
| Break-glass | granting | `handlers_sso_stepup.go:238` (session email claim) |
| Organisation association by domain | — | `storage/orgs.go:42-52`, owned by PAC-68 |

**Deployments.** Production had zero users on 2026-10-21; other deployments may have some (`docs/specs/PAC-22.md:80`, citing PAC-49 §2 item 1).

**Test coverage today:**
- Callback success and failure redirects: `backend/api/pac35_auth_flows_test.go:142-246` (Google and Microsoft) and `:286` (OIDC, every documented status). Their fixtures carry no Google verified flag (`:145`, `:163`, `:169`). The Microsoft runner fakes only the `/me` answer (`:183`), with no organisation answer (`:196`, `:211`, `:241`).
- The SSO email-domain binding: `backend/api/handlers_sso_test.go:161-195`.
- The sign-in scope sets: `pac50_signin_scopes_test.go:19-69`.
- No test asserts anything about *which* account a sign-in reaches when two identities share an address.
- No test covers the manager's removal of an address no account carries, or the co-host keep set with a mixed-case stored address.
- e2e cannot reach any sign-in callback. It mints the session directly and names that gap (`e2e/auth.ts:17-25`).

## 3. Desired behaviour

### Identities

An account belongs to the identity bound to it, not to an address. An identity is the provider authority together with the subject that authority issues:

- **Google**: Google's stable account identifier.
- **Microsoft work or school account**: the pair of tenant and object id, both taken from Microsoft's answers for the same access token. The same object id from another tenant is a different identity.
- **Microsoft personal account**: its own authority, distinct from every work tenant, with Microsoft's identifier for the personal account. An account is classified personal only on a positive signal in Microsoft's answer. An answer that cannot be classified is refused (§3 "Refusal"; nothing written).
- **Organisation SSO**: the issuer the ID token was verified against, together with that issuer's subject. Two different issuers are different identities even if one asserts the other's subjects.

Identifiers are only ever compared together with their authority. An identifier from one authority never matches an identifier from another, even when the strings are equal.

All identity evidence comes from the answers that today's sign-in permissions already return. Sign-in requests exactly today's permissions (owner, PAC-68 OQ-2; PAC-50 AC-24).

**The Microsoft tenant fails closed (revision 3).** The tenant is part of a work account's identity, not only evidence for membership. When it cannot be obtained for a Microsoft sign-in (the source errors, times out, is refused, or is absent), the sign-in is **refused** with the single refusal message (§3 "Refusal") and nothing is written. That holds in every case:
- the sign-in would create an account;
- it would bind a pre-existing account under migration (a) or (b);
- the identity is already bound to an account. A bound Microsoft identity cannot be recognised without its tenant, because the same object id in another tenant is a different identity. So that sign-in is refused too.

Which source supplies the tenant is stage 2's to establish (OQ-3 item 1): Graph's organisation answer is documented under today's scopes; the tenant claim inside the access token and the client information that Microsoft's token endpoint can return are candidates that need no organisation call (§2). Whatever source stage 2 picks, the rule above does not change. This overrides PAC-68's "open for sign-in" stance for Microsoft (§3 constraints, D-23).

In this ticket an account has **at most one** bound identity. Binding a second identity is follow-up F1.

### Evidence and address standing

**Evidence a sign-in carries for its reported address:**

| Level | Google | Microsoft work account | Microsoft personal account | Organisation SSO |
|---|---|---|---|---|
| Authoritative | email verified **and** hosted domain equals the address's domain; or email verified on a Google consumer domain (gmail.com, googlemail.com) | the sign-in's tenant lists the address's domain among its verified domains (subject to OQ-3 on unmanaged directories) | an address on a Microsoft consumer mail domain, only if OQ-3 names an answer field that reports it as the account's own sign-in address; otherwise none | the issuer configured for the address's exact domain, and the ID token does not say the email is unverified |
| Mailbox | email verified, any other domain | — | only if OQ-3 names an answer that says Microsoft verified the address; otherwise none | — |
| None | email not verified, or flag absent | the tenant does not verify the domain, or the lookup fails | as above | — |

Mailbox evidence is weaker than authoritative evidence. Google's flag records that a mail reached the address at some past time, and it stays set after the mailbox is lost (§1 case 1). Authoritative evidence comes from the party that controls the address's domain.

**An account's address has one of two standings:**
- **Domain-confirmed**: the sign-in that bound the account's identity carried authoritative evidence for the account's address.
- **Unconfirmed**: everything else. That covers an account bound with mailbox evidence, and an account created before this change that has not bound yet.

**What standing allows:**
- **Granting sites** (§2 table: team-invite acceptance; scheduling-link co-host on create, on add, and the new entries of a list patch; the manager's member add, roster import, roster join to a formal team and roster entry for a formal member; break-glass) resolve an address only to an account that holds it **domain-confirmed**. An unconfirmed address resolves to nobody there. Team-invite acceptance answers that the invite is for a different address, as it does today for a mismatch.
- **Revoking and keeping sites** (the manager's member removal; the keep set of a co-host list patch; the manager's member list for a formal team) act on **every** account that carries the address, at any standing, compared case-insensitively. A removal of an address that some account carries is never a silent no-op. A keep never drops an accepted co-host whose address is listed.
- An account whose address is unconfirmed works fully for its own user (calendar, scheduling, settings). Settings shows that the address is not confirmed and that invitations addressed to it cannot be accepted yet.
- Organisation membership remains PAC-68's rule.
- Addresses are compared case-insensitively. Two addresses that differ only in letter case are the same address everywhere this spec says "address". No new account may carry an address that an existing account carries in any letter case.

There is no Paceday email confirmation in this ticket, and no way for an authoritative sign-in to take an address from an account that carries it. Both are follow-up F2.

### The sign-in rule

1. **A sign-in reaches an account only through its bound identity.** If the identity is bound, the sign-in reaches that account, whatever address the provider now reports.
2. **Bound identities are found only by authority and subject.** The address is never consulted to choose the account for a bound identity.
3. **The address never links a sign-in to an account that has a bound identity.** This holds whatever the provider says about the address, including authoritative evidence.
4. **An unknown identity whose address an existing account carries** (any standing, any letter case) is refused with the single refusal message (§3 "Refusal"). The one exception is an account created before this change, which the migration rules below decide.
5. **An unknown identity whose address no account carries creates an account** as follows:
   - with authoritative evidence: created, bound, address domain-confirmed;
   - with mailbox evidence: created, bound, address unconfirmed;
   - with no evidence: refused.
6. **No sign-in, connect, step-up or "extended access" round trip adds an identity to an account that already has one** (owner, PAC-68 OQ-2 for "extended access"). Adding a sign-in method is follow-up F1.

### Connecting a calendar from inside the product (the connect step)

This follows PAC-47 §3 item 3 (`PAC-47.md:377-411`) and extends it to Google.

- The four signed-in controls (§2 table: Settings work-calendar control, connection status control, Dashboard "Connect calendar" banner, onboarding calendar step) start a **connect**, never the public sign-in start. The public sign-in dialog's four starts (§2) keep the public sign-in start.
- A connect attaches the provider's calendar credential to the account that started it, whatever identity and address the provider account has. It **does not bind that identity and adds no sign-in method.** It creates no account, issues no new session, and changes no other account.
- It is credited only while the account that started it is still the one signed in when the provider step completes. Otherwise it is credited to nobody and changes no session (PAC-47 AC-3f).
- **A connect is bound to the account and its session generation when it starts (revision 3).** The completion is credited only if, when it is handled, the account's current session generation is still the one recorded at the start, and the browser's session is a current-generation session of that same account. If the generation moved in between (a binding, an operator step, the deploy-time step), the completion is refused: no credential is written, nothing is bound, no session is issued or changed, and it ends on the "credited to nobody" outcome.
- A connect is recognised only by something that a connect action of the signed-in user produces and another site cannot. A flow that no connect action started is a sign-in, wherever it was started. A session being present does not make it a connect (PAC-47 D-18).
- **A connect cannot be started by another site.** A cross-site navigation or form that reaches the connect start, carrying the victim's session, starts nothing that can later store a credential. (Today the starts are plain GETs, §2.)

  *Cross-site request model (revision 4).* "Another site" is a page on any other site, open in the victim's browser while the victim is signed in. It can make the browser:
  - navigate the top-level window to any Paceday address, with any query;
  - submit a form to any Paceday address, by GET or by POST, with any fields it chooses;
  - load any Paceday address as an image, script, frame or other subresource.

  For the criterion, every such request is assumed to carry the victim's session cookie, whatever the browser's same-site rules would do with it. This is fail closed: the property must not rest on how a browser applies those rules. Such a request carries nothing else the victim's session holds. Another site cannot read Paceday's responses or cookies, cannot set custom request headers on a cross-site request, cannot read anything the web app obtained from an authenticated response, and runs no script on Paceday's origin. A connect start that can be made with only what this model allows starts nothing that can later store a credential. Today's starts fail the model: a top-level GET to them begins a flow (§2).
- A connect never changes the work calendar. The Settings work-calendar control and the onboarding step change the work calendar by the user's explicit choice in that control before the connect starts, as today (§2).
- The provider identity used by a connect stays unknown for sign-in. If that provider account later signs in, rules 1 to 5 apply to it as to any other identity. For example, a Google account connected to Henry, Microsoft-bound, that reports `henry@acme.com` is refused at sign-in (rule 4).

This answers OQ-1 **"no" for this ticket** (factory guidance, §8): connecting a calendar never adds a sign-in method. Whether the product should later offer to add one, and how, belongs to follow-up F1.

### Accounts created before this change (migration)

Existing accounts carry no binding (§2). Until one binds, its address is unconfirmed: granting sites resolve it to nobody and revoking and keeping sites act on it. An existing account is bound **lazily, on its first sign-in after this change**, under one of two rules. Anything else that matches an unbound existing account only by address is refused (rule 4).

- **(a) Same identity as recorded.** The incoming identity is a Google or Microsoft identity, and the account's recorded last-sign-in identifier equals that identity's subject:
  - in the same field the code recorded (Google: the userinfo identifier; Microsoft: Graph's user `id`);
  - in the format that provider issues. The contract names the formats; a value that fits more than one provider's format never matches.

  The provider must report the account's address with at least mailbox evidence. An SSO subject never qualifies under (a), because the configured IdP chooses it. The standing is set from the evidence: domain-confirmed for authoritative, unconfirmed for mailbox.

  *What (a) trusts (revision 3).* Rule (a) binds **the identity on record**, and the spec treats it as the account's creator of record. The record cannot tell a creator from someone who reached the account by email matching before this change: such a sign-in overwrote the recorded identifier (§2). If that someone signs in first after deployment, (a) binds them, and the rightful holder is refused afterwards (rule 3). This is not detectable from the data. The remedy is the operator's "unbind and reassign" step (§3 "Operator procedure"). To make such cases findable, every (a) binding made on mailbox evidence for an address on a domain that is not a provider consumer domain is written to the server log for operator review.
- **(b) Authoritative evidence on an empty account (revision 3).** A Google or Microsoft sign-in carrying authoritative evidence for the account's address binds the account only when the account is **empty**: it carries nothing that any party put there and no relationship to any other account or public page. Concretely, against the inventory in §2 "What an account can carry today", the account has none of the following:
  - any stored calendar, conferencing or personal-calendar credential, any Slack or Notion connection, any personal AI key or AI destination, and no record that a credential was lost;
  - any other setting that points outside Paceday (a subscribed calendar address, a calendar address, a recap channel);
  - any preference changed from its default (fail closed: a changed preference shows that someone used the account, and some preferences act on the calendar). *Revision 4:* "changed from its default" is decided **per preference**:
    - the stored value is compared with that preference's default, which is the value a newly created account holds;
    - the comparison is on meaning, in the normal form the code itself stores, so a schedule the code reformatted on save without changing it counts as unchanged (§2);
    - the contract names the default and the normal form of every preference, and a preference it does not name counts as changed (fail closed);
    - values the code writes by itself (§2) are judged the same way: equal to the default is unchanged, different counts as changed whoever wrote it. For example, a work calendar left on Outlook after a Microsoft credential was stored and then removed counts as changed. That costs lock-out only, and only on an account that once held a credential;
    - bookkeeping that is not a preference (the time of the last save, the auto-schedule watermark) is not compared;
  - membership of any team, any team it created, or any team invitation it sent;
  - any scheduling link it owns, and any host or co-host entry on any link, in any status;
  - any booking on a link it owns, and any event a booking created for it;
  - any manager standing or roster: a manager profile, a roster it keeps, one-to-one or team-analytics records it keeps, or an entry in someone else's roster that points at it;
  - any stored focus block, habit or habit occurrence, analytics week, meeting brief, recap record, or activity-log entry;
  - any organisation-admin standing: a step-up grant, or a record of a step-up or SSO configuration write by it.

  Not counted: the account row itself (name, picture, first provider, recorded identifier) and its organisation membership, which PAC-68 decides afresh for the binding sign-in and which exposes no calendar data (§2). A kind that a later migration adds and the plan has not classified counts as **non-empty** (fail closed).

  The recorded last-sign-in identifier plays no part in (b). Revision 2's condition "no recorded identifier (empty, or an SSO subject)" is removed: it could not be decided from today's data (§2: nothing records which provider an identifier belongs to). An account created through SSO, or one whose recorded identifier differs from the incoming identity, binds under (b) only if it is empty; otherwise it is refused like any other.

  Otherwise the sign-in is **refused**: the account stays unbound, nothing is stored on it, the single refusal message is shown, and the server log names the account and the first non-empty kind found, for the operator (§3 "Operator procedure"). An SSO issuer is never authoritative over an existing account, bound or not (D-10, OQ-2's default).

  *Why (b) is this narrow.* Binding stores the rightful holder's calendar credential on the account. Anything already on it was put there by someone, who may not be the person now presenting, and other accounts and public pages read a member's calendar through relationships, not through the member's own credentials (§2). Two examples:
  - Revision 1 Blocking 1: a mailbox holder created account A before deployment with a consumer Google account, connected Slack and set an AI key. Henry, arriving with acme's Workspace, is refused because A is not empty. His credential never reaches A.
  - Revision 2 Challenge Blocking 1: before PAC-50's gate, an attacker pointed acme's SSO at their own IdP, created A as `henry@acme.com`, and from A joined their own team and accepted a co-host invitation on their own public link, connecting nothing. Under revision 2, Henry's binding would have exposed his calendar to the attacker's team and link. Under revision 3, A's team membership and co-host entry make it non-empty, so Henry is refused and an operator resolves it.

  A genuine user who used both providers before this change and last signed in with the other one, on a non-empty account, gets the same message. It tells them to sign in the way they did before, which binds under (a). That costs one extra sign-in.
- **Every binding ends every other session of the account** (§3 "Sessions"). The session issued by the binding sign-in is the only valid one afterwards. Any session from before the binding, including one obtained by email matching before the change, stops working at its next request.
- **Binding is one atomic step (revision 3).** Binding under (a) or (b):
  1. locks the account against every concurrent change to it;
  2. re-checks, under that lock, that the account is still unbound and that (a)'s or (b)'s conditions still hold, including (b)'s emptiness;
  3. writes the binding and the standing;
  4. advances the session generation;

  and then ends, all or nothing. A write to the account from a session of the old generation either takes effect before step 2, and is then seen by the re-check (so (b) refuses), or is refused, and stores nothing. It never takes effect after the binding on the newly bound account. Every request is checked against the current generation when it is authenticated (§3 "Sessions"). For a write of any kind that (b) counts, that is not enough on its own: a request authenticated just before the binding could store its write just after. So such a write is also checked against the account's current generation as part of storing it, serialised with the binding's lock.
- **An older-generation request never reaches what a binding stored (revision 4, factory decision, independent of OQ-6).** A request authenticated with a session whose generation is older than the account's current one never obtains, uses or returns any credential, connection, key or account data stored after the account's generation advanced. This covers provider calls made with such a credential: reading the calendar or free/busy, creating or changing an event or a meeting, and every other call a handler makes through it.
  - Every load of a credential or of account data for a request re-confirms the request's generation against the account's current generation **in the same read**, so no binding can commit between the comparison and the load.
  - On a mismatch the load returns nothing, no provider call is made with anything it would have returned, and the request ends as unauthenticated (401), like any other old-generation request (§3 "Sessions").
  - This holds whether or not the deploy-time step runs. Authentication alone leaves a window between a request's authentication and its credential load (§2), and a binding can commit inside it.
- When two qualifying identities race for the same unbound account (for example a domain verified in both Google Workspace and Microsoft Entra, on an empty account), exactly one binds and the other is refused.
- A user who used both Google and Microsoft before this change gets one of them bound. Signing in with the other one is refused (rule 3). They can still connect the other provider's calendar from inside the product (the connect step). Adding it as a sign-in method is follow-up F1.
- **Existing members and SSO.** An existing member's account is never reached through SSO in this ticket (D-10). Until follow-up F1 lets a member add SSO as a sign-in method, existing members keep signing in with Google or Microsoft. New members whose address no account carries can sign up through SSO (rule 5).
- **Case collisions.** If two existing accounts carry addresses that differ only in letter case, the deployment neither merges nor deletes them:
  - every sign-in or migration match against that address is refused, even when rule (a)'s identifier matches;
  - granting sites resolve the address to nobody;
  - revoking and keeping sites act on both accounts;
  - the server log names the collision.
- Nothing is deleted. No account, membership, credential or setting is removed by the migration or by a refusal (D-9).

### Sessions

- Every account has a **session generation**. Every session carries the generation of its account at the moment the session was issued. Every authenticated request is refused as unauthenticated (today's 401) when the session's generation is not the account's current one.
- **Reading the generation fails closed, and is never cached (revision 3).** If the account's current generation cannot be read, the request fails with a server error, not 401: the web app treats 401 as "signed out", and an outage must not look like one. No cache of the generation is allowed, so a session is closed from the first request after the advance commits.
- A session issued before this change carries no generation. It is valid only while its account's generation is still the initial value.
- Every account created after this change starts at the initial generation. A test helper that mints sessions directly (today's e2e helper, §2) must mint the account's current generation for an account whose generation has moved (for example after the deploy-time step).
- The generation advances whenever a binding is written or changed for the account. In this ticket that means a migration binding (a) or (b), or an operator's step under the operator procedure. The session that the same sign-in issues carries the new generation.
- A connect in flight is bound to the generation at its start (§3 "Connecting a calendar"), so an advance also ends every connect started before it.
- **Deploy-time step (OQ-6, the owner decides).** The factory recommends advancing every account's generation once at deployment, which signs every user out once. It closes every session obtained through email matching before the change, including on accounts that never sign in again. Without it, such a session stays valid until it expires (at most 7 days), on any account that does not bind in that time.
- Logout is unchanged: it expires the browser's cookie. A user-facing "sign out everywhere" is follow-up F3.

### Refusal

When a sign-in cannot reach an account under these rules, the user is told so plainly on the web app's sign-in page. Examples are:
- an address that belongs to an account bound to another identity;
- an unbound pre-existing account where neither (a) nor (b) applies;
- an address that an account carries while the incoming identity is unknown;
- a new identity without evidence;
- a Microsoft sign-in whose tenant cannot be obtained, or whose account cannot be classified as work or personal (§3 "Identities").

The message has two parts. It says that Paceday could not sign them in with this account. It says that if they already use Paceday, they should sign in the way they did before, and otherwise contact their workspace administrator or the Paceday operator.

The message is the same for every refusal cause, so it does not reveal whether an account exists for the address (D-6). It is distinct from today's generic "sign-in did not complete" state.

A refusal:
- creates no account and binds nothing;
- stores no provider credential;
- changes no organisation membership, no work calendar, no address standing and no session generation;
- issues no session, and leaves any session the browser already holds untouched (as PAC-35's failure state does today).

The server log records the cause, and for causes involving an existing account it names that account, so the operator procedure can act on it.

### Address changes and reuse at the provider

- A bound identity whose provider now reports a different address still reaches its account.
- The account's stored address does **not** change on sign-in (D-7). If the new address belongs to another account, that account is not touched.
- A provider that gives an address to a new subject (a recycled mailbox) does not reach the old holder's account. The new subject is refused while any account carries the address (rule 4). An operator resolves it (D-12) until follow-up F2.

### Operator procedure (interim, until follow-up F2)

The release ships a written operator procedure (in the operator documentation, not in the product) for the refusals that lock a rightful owner out of an address:
- an address carried by a mailbox-only account (a squatter);
- an address held by a departed holder (a recycled mailbox);
- an unbound legacy account refused under (b);
- a case collision.

For each, the procedure gives four steps:
1. Find the account named in the server log.
2. Verify the person presenting through a channel outside Paceday, for example the domain's administrator.
3. Free the address on the account that carries it. The account is never deleted. Its address is replaced by a non-deliverable placeholder, its stored credentials and third-party connections are removed when it is a squatter's, and its session generation is advanced.
4. Ask the person to sign in again. The sign-in then follows rule 5 or migration (b).

The procedure states that freeing an address removes that account from every address-based grant it has not yet accepted.

**Unbind and reassign (revision 3).** For an account whose data belongs to the person presenting but whose binding is someone else's: typically a pre-deploy takeover that migration (a) then bound (§3 "Accounts created before this change"), or a non-empty legacy account refused under (b) that the operator has verified belongs to the person presenting. Freeing the address would leave that person's data with the wrong party, so the procedure offers a second remedy instead of step 3:
1. The person signs in with the identity they want bound, and is refused. The server log records the account and the incoming identity (authority and subject) with its evidence level.
2. The operator verifies the person through a channel outside Paceday, as in step 2 above.
3. The operator replaces the account's binding with that identity (or binds it, if the account is unbound). The standing is set from the evidence level the log recorded for that sign-in. **By default, every stored calendar credential (Google, Microsoft, personal calendars) is removed (revision 4)**, unless the operator has confirmed that it was issued to the identity now being bound; the verified person then reconnects their calendar. Otherwise a calendar credential issued to the previous identity would keep receiving the person's meetings. Other credentials, connections and keys that the verified person says are not theirs are removed. The session generation is advanced, which ends every session of the account and every connect in flight.
4. The person signs in again and reaches the account (rule 1). The previous identity, if any, is now unknown and is refused at sign-in while the account carries the address (rule 4).

The step is operator-only: it is run by the deployment's operator with direct access to the server, and no API route, web page or MCP tool can perform it. Every run is **audited**: the record names the operator, the time, the account, the previous identity, the new identity, the evidence level, what was removed, and the reason, and it survives later changes to the account. Freeing an address (step 3 above) is audited the same way.

### Organisation membership (PAC-68)

Membership is still decided by PAC-68's rule on the provider's answer for the sign-in. This spec only makes sure that rule is evaluated for the account the identity is bound to, and never for an account reached through an address. A refused sign-in, and a connect, evaluate membership for nobody.

### PAC-50 admin step-up and break-glass

- **Step-up.** When the account's bound identity is at the authority the step-up uses (Google, or the same Microsoft tenant), the step-up's identity must be that bound identity, in addition to today's checks. A step-up by another Google account or another object id with the same address fails as an identity mismatch. When the account's binding is at another authority, or it has none, today's checks apply unchanged. The step-up adds no new consent beyond its own (owner, PAC-68 OQ-2).
- **Break-glass.** The operator's list still names addresses. A listed address authorises the account that holds that address **domain-confirmed**, identified by the session's account, not by the email claim the session carries. A session whose account carries the address unconfirmed, or no longer carries it, is not break-glass. The 10-minute freshness stays.

### Constraints from owner decisions

- Sign-in keeps exactly today's permissions and never requires a new admin consent (owner, PAC-68 OQ-2; PAC-50 AC-24). No identity scope (`openid`, `profile`, `email`) is added to the Google or Microsoft sign-in. Where authoritative or mailbox evidence cannot be obtained under today's permissions, the sign-in is treated as having less evidence; a bound identity still signs in. The exception is the Microsoft tenant, which is identity, not evidence: without it every Microsoft sign-in is refused (§3 "Identities"). **This overrides PAC-68's allowance of `openid` (PAC-68 AC-15, D-5) for sign-in scopes; PAC-68 must amend AC-15 and D-5 to "exactly today's set"** (owner decision on sign-in scopes; PAC-50 AC-24's test).
- **PAC-68's "open for sign-in" stance does not hold for the Microsoft tenant (revision 3, factory decision, fail closed).** PAC-68 lets a Microsoft sign-in complete, not joined, when its verified-domain lookup errors, times out or is refused (PAC-68 AC-9, AC-15's second clause, D-4; `PAC-68.md:95`, `:115`, `:157` at `ec76099`). PAC-68 assumed the tenant could come from the token response (AC-9, "if taken from the token response"), which needs a scope this spec forbids. Under today's scopes the tenant may come from that same lookup (§2), and the tenant is identity. So PAC-71 refuses such a sign-in, and PAC-68 must amend those three items (§6). If stage 2 finds a tenant source that does not depend on the organisation lookup (OQ-3 item 1), PAC-68's lookup failing would again affect only membership, but a sign-in whose tenant itself cannot be obtained is still refused. Release note: Microsoft sign-in may depend on a second Graph call, and an outage of it refuses Microsoft sign-in with the refusal message.
- A Microsoft sign-in must not reset an existing user's work calendar (owner, PAC-47 vs PAC-67 OQ-13). Today's reset on the plain Microsoft sign-in path is PAC-47's to remove (PAC-47 D-17). This spec adds no path that resets it: a connect, a refusal and a migration binding never change the work calendar.

## 4. Acceptance criteria

Conventions:
- **"Henry"** is an existing account for `henry@acme.com`.
- **"Bound to X"** means X is the account's bound identity. Unless stated, Henry's address is domain-confirmed.
- **"Refused"** means all of the following:
  - the callback ends on the refusal state of §3 (one fixed outcome, the same for every cause, distinct from the generic failure state);
  - no session is issued and an existing session cookie is neither replaced nor expired;
  - no account is created; no binding is written; no provider credential is stored on any account;
  - no account's organisation, work calendar, address standing or session generation changes.
- **"Reaches Henry"** means the session issued identifies Henry's account and the provider credential is stored on Henry's account.
- **"Connect"** means the flow started by one of the four signed-in controls (§3 "Connecting a calendar").
- Provider answers (including Microsoft's organisation answer) are injected with the existing fake-provider seams (`pac35_auth_flows_test.go` pattern), so every [unit] criterion is offline. The tests for AC-1 to AC-7b drive the real callback handlers, not a storage helper, because AC-23 may be dropped and they are then the only proof.
- **Today's scope sets** are those of §2 ("A test pins today's sign-in scopes").
- A criterion marked **(revised r2)** or **(revised r3)** keeps its number with a changed meaning; "Revision 2" or "Revision 3" says how. Numbers moved to a follow-up or dropped are listed in §10 or in "Revision 3" and are not reused.
- **"Empty"** means empty in the sense of §3 rule (b), checked against §2 "What an account can carry today".
- **Interleaving seam.** Criteria about atomicity (AC-7, AC-7b, AC-46, AC-47, AC-50) are proven with a deterministic test seam, as PAC-22 AC-27 and AC-28 do (`docs/specs/PAC-22.md:682-720` on `main`): the test injects the competing write at a named point between one operation's check and its store, and asserts on what is stored. Running the two operations one after the other is not a proof, because a non-atomic check-then-store passes that. Each such criterion also states the outcome of both sequential orders.

**Takeover scenarios (§1)**

AC-1. Given Henry bound to Google identity G1, when Google identity G2 signs in with `henry@acme.com`, email verified, no hosted domain, then the sign-in is refused, and Henry's stored Google credential is byte-for-byte unchanged.  [unit]

AC-2. Given Henry bound to Google identity G1, when a Microsoft work account (tenant T9, object id O9) whose mail is `henry@acme.com` signs in, then it is refused, whether or not tenant T9 verifies `acme.com`.  [unit]

AC-3. Given Henry bound to Microsoft (T1, O1), when Google identity G2 signs in with `henry@acme.com`, email verified and hosted domain `acme.com`, then it is refused.  [unit]

AC-4. Given Henry bound to Microsoft (T1, O1), when (a) (T1, O2) with mail `henry@acme.com`, or (b) (T2, O1) with mail `henry@acme.com` signs in, then both are refused.  [unit]

AC-5 (revised r3). Given Henry bound to Microsoft work account (T1, O1), when a personal Microsoft account with address `henry@acme.com` signs in, then it is refused. Given instead an unbound pre-existing account for `henry@acme.com`, a personal Microsoft account carrying that address is also refused. "Refused" holds whatever stage 2 finds for OQ-3 item 2: with a positive personal-account signal the cause is rule 4; without one the answer cannot be classified and the cause is §3 "Identities". Either way the outcome is the single refusal state and nothing is written. Today both cases reach Henry's account.  [unit]

AC-6. Given Henry bound to Google G1 and an OIDC SSO configuration for `acme.com` with issuer I1, when I1 asserts subject S1 with `henry@acme.com`, then it is refused. Given instead Henry bound to (I1, S1), when the configuration's issuer is changed to I2 and I2 asserts subject S1 with `henry@acme.com`, then it is refused.  [unit]

AC-7 (revised r3). Given no account for `new@acme.com`, when two different identities with authoritative evidence for that address complete sign-in, and the second one's account creation is stored **between the first one's check that no account carries the address and the first one's store** (interleaving seam), then exactly one account exists afterwards, bound to the second identity, and the first sign-in is refused with nothing stored. The same holds when one reports `Zoe@acme.com` and the other `zoe@acme.com`: exactly one account exists for that address in any letter case. Run one after the other, in either order, the first creates and the second is refused. Given an identity X already bound to account A, when signed-in user B connects a calendar and the provider answers with X, then B holds the calendar credential, X is still bound only to A, A is unchanged, and a later sign-in by X reaches A.  [unit]

AC-7b (revised r3). Given an empty unbound pre-existing account for `henry@acme.com`, when Google G2 (hosted domain `acme.com`, verified) and Microsoft (T1, O3) (tenant verifies `acme.com`) both sign in, and the Microsoft binding is stored **between the Google sign-in's check of rule (b) and its store** (interleaving seam), then exactly one identity, (T1, O3), is bound to the account, the Google sign-in is refused with no credential stored, the generation advanced exactly once, and no second account is created. The mirror interleaving binds G2 and refuses (T1, O3). Run one after the other, the first binds and the second is refused.  [unit]

AC-25. Given account A carries `alice@example.org` (any standing, any letter case), when another identity with only mailbox evidence for that address signs in, then it is refused.  [unit]

AC-40 (pre-emptive squat, replaces AC-24). Given no account for `henry@acme.com`, when consumer Google identity GA signs in first with `henry@acme.com`, email verified, no hosted domain, then an account A is created bound to GA with the address unconfirmed. Afterwards:
- a team invite, a scheduling-link co-host invitation on create and on add, a co-host list patch adding the address, a manager's member add and a roster import for `henry@acme.com` each resolve to nobody, and A cannot accept the team invite;
- break-glass with `henry@acme.com` on the operator's list does not authorise A;
- when Henry then signs in with Google G1, hosted domain `acme.com`, verified, he is refused; A's binding, data and sessions are unchanged; the server log names A and the cause.  [unit]

**Ordinary sign-in keeps working**

AC-8 (revised r2). Given Henry bound to an identity at Google, at Microsoft, or at SSO issuer I1 (three cases), when that identity signs in, then it reaches Henry with the same redirect target as today's success path. Today's PAC-35 callback tests (`pac35_auth_flows_test.go:142-246`, `:286`) keep every assertion about redirects, the session cookie and what is kept on the failure path. Only their fixtures change, so that the sign-in carries evidence:
- the Google fixtures add a verified flag;
- the Microsoft runner adds an organisation answer whose verified domains list the fixture's domain, and a tenant from whichever source stage 2 picks (OQ-3 item 1).  [unit]

AC-9. Given Henry bound to Google G1, when G1 signs in reporting `henry.new@acme.com`, then it reaches Henry, and Henry's stored address and its standing are unchanged. If another account holds `henry.new@acme.com`, that account's session, credential, address, standing and binding are unchanged.  [unit]

**New accounts and evidence**

AC-10. Given no account for `alice@example.org`:
- when Google identity G5 signs in with that address, email verified, no hosted domain, then an account is created and bound to G5 with the address unconfirmed, and a second sign-in of G5 reaches it;
- the same with the flag false or absent is refused, and no account exists for the address afterwards.  [unit]

AC-10b. Given no account for the address, when Google identity G6 signs in with either of these, then an account is created bound to G6 with the address domain-confirmed:
- (a) `bob@gmail.com`, verified;
- (b) `carol@acme.com`, verified, hosted domain `acme.com`.

With (c) `carol@sub.acme.com` and hosted domain `acme.com`, the address is unconfirmed.  [unit]

AC-11. Given no account for `dave@acme.com`, when a Microsoft work account (T1, O7) signs in with that mail, then:
- if tenant T1's verified-domain answer contains `acme.com`, an account is created, bound to (T1, O7), with the address domain-confirmed;
- if the answer does not contain it, or the lookup errors or times out, the sign-in is refused;
- the sign-in's authorize request carries exactly today's Microsoft scope set, and `TestPAC50_SignInAuthorizeURLsCarryNoNewScope` passes unchanged.  [unit]

AC-11b (revised r3). Given a Microsoft sign-in whose answers do not let the server determine the tenant, then the sign-in is **refused** (the single refusal state, not the generic failure state), and nothing is created, bound or stored. Each case below is run for (i) no account carrying the address, (ii) an empty unbound pre-existing account for the address, and (iii) Henry bound to (T1, O1), with the answered object id O1:
- the tenant source errors, times out, or is refused for lack of permission;
- no tenant answer at all for what would be a work account;
- an answer that cannot be classified as work or personal.

Case (iii) reaches Henry today, and would complete as a sign-in under PAC-68 AC-9 as written; PAC-68 must amend (§6).

(Revision 1's second sentence, about two answers disagreeing on the tenant, is dropped: under today's Microsoft scopes only the organisation answer carries the tenant, so there is no second source to disagree with.)  [unit]

AC-11c. Given no account for `erin@acme.com`, when a personal Microsoft account signs in with that address, then it is refused, unless OQ-3 has recorded an answer that gives mailbox or authoritative evidence for personal accounts, in which case the outcome follows the §3 evidence table.  [unit]

**Address standing and address-as-person**

AC-28 (revised r2). For each site in the §2 address-as-person table, given account U carries the address:
- **Granting sites** (team-invite acceptance; co-host on create, on add, and new entries of a list patch; manager member add; roster import; roster join to a formal team; roster entry for a formal member): if U's address is unconfirmed, the site resolves the address to nobody, and team-invite acceptance answers that the invite is for a different address. If it is domain-confirmed, the site resolves it to U as today.
- **Revoking and keeping sites** (manager member removal; the keep set of a co-host list patch; the manager's member list for a formal team): the site acts on U at either standing.  [unit]

AC-43 (removal is never silent). Given account E is a member of the manager's team and carries `henry@acme.com` unconfirmed (an unbound legacy account, or bound with mailbox evidence), when the manager removes `henry@acme.com`, then E is no longer a team member. Given instead two pre-existing accounts carrying `x@acme.com` and `X@acme.com`, both members, the removal of `x@acme.com` removes both. Given a scheduling link with an accepted co-host whose stored address is `Pat@Acme.com` (or unconfirmed), when the owner patches the co-host list to a list containing `pat@acme.com`, then that co-host is kept with status accepted. Today the mixed-case case removes the co-host (§2).  [unit]

AC-29 (letter case, revised r2). Given Henry holds `henry@acme.com`:
- Google G2 signing in with `HENRY@Acme.com`, verified, no hosted domain, is refused (it is Henry's address);
- each granting site resolves `Henry@ACME.com` to Henry;
- a new account created from `Zoe@Example.org` is found by `zoe@example.org` at every site.

Given two pre-existing accounts carrying `x@acme.com` and `X@acme.com`, the deployment changes neither. A sign-in matching that address is refused, including one whose identifier matches one account's recorded identifier under migration (a). Granting sites resolve the address to nobody, revoking sites act on both (AC-43), and the server log names the collision.  [unit]

**Connecting a calendar (aligned with PAC-47)**

AC-12 (revised r2). Given Henry signed in, bound to Google G1, work calendar Google, when Henry chooses Outlook in the Settings work-calendar control and the connect completes with Microsoft (T1, O1), mail `h.work@other.org`, then:
- Henry's session still identifies Henry;
- the Microsoft credential is stored on Henry's account;
- Henry's work calendar is Outlook, set by his choice in the control before the connect;
- no account for `h.work@other.org` is created; (T1, O1) is not bound to anyone; Henry's address, standing and session generation are unchanged;
- a later sign-in by (T1, O1) with no session does not reach Henry.

Today the same flow creates or switches to another account (§2).  [unit]

AC-12b (revised r2). Given Henry signed in, bound to Microsoft (T1, O1), work calendar Outlook, when Henry connects Google from the connection status control and Google answers with G7 and `henry@acme.com`, then:
- the Google credential is stored on Henry;
- G7 is not bound;
- Henry's work calendar is still Outlook;
- a later sign-in by G7 with no session is refused (rule 4).  [unit]

AC-13 (revised r2). Given (T1, O1) bound to account A, when signed-in user B connects Outlook from any of the four controls and Microsoft answers with (T1, O1), then B holds the Microsoft credential, B's session is kept, A's binding, credential and sessions are unchanged, and no account is created.  [unit]

AC-14 (revised r3). Given user A starts a connect, when the callback arrives carrying (a) no session or (b) a session for user B, then no credential is stored on A or B, no identity is bound, no session is issued and none changes (PAC-47 AC-3f). (c) Given A starts a connect with session S0 and A's session generation then advances (a binding, or the operator step), when the provider step completes in the browser still holding S0, then no credential is stored on A, A's stored credential is byte-for-byte unchanged, no session is issued or changed, and the outcome is "credited to nobody". The same holds if the browser by then holds a new, current session of A: the connect was bound to the old generation. (d) Given A signed in, when another site, within the cross-site request model of §3 "Connecting a calendar" (revision 4), makes A's browser navigate to, submit a GET or POST form to, or load as a subresource, the connect start (each a separate case, each carrying A's session cookie), and the provider completes without a consent screen, then nothing is stored on A and A's stored credential is unchanged. Given a browser holding A's session that completes an ordinary sign-in (not started as a connect) with identity X whose address is not A's, then X is not bound to A and no credential is stored on A. The presence of a session cookie alone never makes a flow a connect.  [unit]

AC-31 (work calendar, no new reset; revised r2). Given Henry bound to Google G1, work calendar Google:
- when Henry connects Outlook from the connection status control, the Dashboard banner or the onboarding step without choosing Outlook as the work calendar, then the work calendar is still Google;
- a refusal and a migration binding (AC-15, AC-41) never change the work calendar.  [unit]

AC-32 (connect surfaces, revised r2). In the web app, each of these starts a connect and never navigates to a public sign-in start: the Settings work-calendar control (Google and Outlook), the connection status control, the Dashboard "Connect calendar" banner, and the onboarding calendar step. The public sign-in dialog's four starts (button, Google with a hint, Microsoft with a hint, SSO via detect) still navigate to the public sign-in start. Revision 1's last sentence, about a disclosure before leaving, is dropped: a connect no longer adds a sign-in method.  [unit] web app

**Sessions**

AC-42 (session generation). Given Henry with two sessions S1 and S2:
- when the operator procedure advances Henry's session generation, then the next authenticated request with S1 or S2 gets today's 401, and a new sign-in by Henry's bound identity issues a session that works;
- given an unbound pre-existing account with a session S0 issued by the code before this change, when the account binds under (a), then S0 gets 401 on its next request and the session issued by the binding sign-in works;
- a refusal, a connect and an ordinary bound sign-in do not change the generation, so S1 and S2 keep working;
- an account created after this change starts at the initial generation, so a session minted for it with the initial generation (today's e2e helper) works until its generation first advances.  [unit]

AC-44 (deploy-time step, conditional on OQ-6). If the owner answers OQ-6 "yes", then given sessions issued before the deployment for any accounts, after the deployment's migration every one of them gets 401 on its next request, and no account, credential or setting changes. If the owner answers "no", this criterion is dropped and §5 records the residual.  [unit] (migration test)

**Organisation SSO**

AC-37. Given an unbound pre-existing account for `henry@acme.com` whose recorded identifier is the string X:
- the configured `acme.com` issuer asserting a subject with `henry@acme.com` is refused, and the account stays unbound;
- the same issuer asserting subject X is refused;
- a Microsoft identity whose Graph user id is the string X is not matched under migration (a) unless X has the Microsoft format and the recorded value cannot be a Google identifier.  [unit]

AC-38 (break-glass, revised r2). Given `henry@acme.com` on the operator's break-glass list and a session under 10 minutes old:
- for an account that holds `henry@acme.com` domain-confirmed, an SSO write is authorised as break-glass, as today;
- for an account that carries it unconfirmed, including an unbound pre-existing account, it is not;
- for a session whose email claim is `henry@acme.com` but whose account no longer carries that address (freed by the operator procedure), it is not.  [unit]

**Accounts created before this change**

AC-15. Given an unbound pre-existing account for `henry@acme.com` whose recorded last-sign-in identifier is G1:
- when Google G1 signs in with that address verified, then it reaches the account and binds G1;
- a later Google G2 with `henry@acme.com` verified and no hosted domain is refused.  [unit]

AC-16 (revised r3). Given an unbound pre-existing account for `henry@acme.com` whose recorded identifier is G1 and which is not empty (it stores one habit), each of these is refused and the account stays unbound with no credential stored on it:
- (a) Google G2 with `henry@acme.com` verified and no hosted domain;
- (b) Google G2 with hosted domain `acme.com` and email verified;
- (c) a Microsoft work account (T1, O3) whose tenant verifies `acme.com`;
- (d) a Microsoft work account whose tenant does not verify `acme.com`.

For (b) and (c), the server log names the account and the first non-empty kind found. The empty variant of (b) binds (AC-41).  [unit]

AC-41 (migration rule (b) and pre-account hijack, revised r3). Given an unbound pre-existing account A for `henry@acme.com`:
- empty, with each of these recorded identifiers in turn: none; an SSO subject on an account first created through SSO; a value in Google's format on an account first created through SSO; a consumer Google account GA. With two sessions from before the change: when Google G1 (hosted domain `acme.com`, verified) signs in, then it reaches A, binds G1 with the address domain-confirmed, the two old sessions get 401 on their next request, and A's work calendar is unchanged;
- not empty because of exactly one of these, each a separate case: a Slack connection; a Notion connection; a personal AI key; a Zoom connection; a Google calendar credential; a Microsoft credential; a personal calendar; a recorded credential loss; one preference changed from its default. The same sign-in is refused, A stays unbound, Henry's Google credential is not stored on A, and the server log names A and that kind;
- with recorded identifier GA and a Slack connection: the same sign-in is refused (AC-16 (b)).

Today the first case fails on the 401s, and the second fails because the credential is stored on A.  [unit]

AC-45 (rule (b) refuses every relationship, revision 3). Given an unbound pre-existing account A for `henry@acme.com` with no credential, connection, key or changed preference, whose only content is exactly one of the following (each a separate case), when Google G1 (hosted domain `acme.com`, verified) signs in, then it is refused, A stays unbound, no credential is stored on A, A's generation is unchanged, and the server log names A and the kind:
- (a) team: A is a member of a team created by another account B (and, separately, A created a team with no other member; and A sent a team invitation that is still pending);
- (b) co-hosted link: A is an accepted co-host on a scheduling link owned by B (and, separately, a pending co-host; and a declined one);
- (c) owned link: A owns a scheduling link with no other host and no booking;
- (d) booking: a booking exists on a link A owns, or an event was created for A by a booking;
- (e) manager or roster: A has a manager profile marked as manager; A keeps a roster entry; B's roster has an entry pointing at A;
- (f) stored data: one focus block; one habit; one analytics week; one meeting brief; one recap record; one activity-log entry;
- (g) organisation admin: A holds a step-up grant (consumed or not), or a record of an SSO configuration write by A exists.

Cases (a) and (b) fail today: Henry's credential is stored on A and B's team availability, or the public link, reads it.  [unit]

AC-46 (binding re-checks under the lock, revision 3). Given an empty unbound pre-existing account A for `henry@acme.com` and a session S0 of A from before the change, when Google G1 (hosted domain `acme.com`, verified) signs in, and S0's write is stored **between G1's first check of rule (b) and the binding's store** (interleaving seam), each write a separate case: S0 creates a team (revised r4: replaces "accepts a team invitation", which an unconfirmed address cannot do, AC-28); S0 accepts a co-host invitation on B's link; S0 sets a personal AI key; S0 completes a Slack connection; then the binding is refused, A stays unbound, A's generation is unchanged, no Google credential is stored on A, and S0's write is kept. Run one after the other: write first, binding refused; binding first, write refused (AC-47).  [unit]

AC-47 (old-generation writes after the binding, revision 3). Given an empty unbound pre-existing account A and a session S0 of A from before the change, when a request of S0 is authenticated **before** G1's binding is stored and its write is attempted **after** it (interleaving seam), each write a separate case as in AC-46, then the write is refused, nothing it carried is stored on A, and A stays bound to G1 with Henry's credential unchanged. When S0's request is authenticated after the binding, it gets 401. *Connect completion (revision 4):* given a connect that S0 started before the binding, at Google and at Microsoft (each a separate case), whose completion passes its generation check **before** G1's binding is stored and attempts to store its credential **after** it (interleaving seam), then the connect stores nothing on A, A's Google credential is Henry's G1 credential byte-for-byte, A holds no Microsoft credential, no session is issued or changed, and the outcome is "credited to nobody".  [unit]

AC-50 (old-generation reads and provider calls after the binding, revision 4). Given an empty unbound pre-existing account A for `henry@acme.com`, a session S0 of A from before the change, and Henry's Workspace G1 (hosted domain `acme.com`, verified) signing in and binding A under rule (b) with Henry's Google credential. A request of S0 is authenticated **before** G1's binding is stored and loads A's calendar credential **after** it (interleaving seam: the binding commits between the request's authentication and its credential load). The fake provider records every call and the credential it carries. Each a separate case:
- (a) *calendar read:* S0 lists A's calendar events for a range in which Henry's calendar has events. The response contains none of Henry's events and no other data stored on A after the binding, the fake provider receives no call made with Henry's credential, and the request gets 401.
- (b) *meeting create:* S0 creates a meeting on A's calendar. No event or meeting is created at the provider, the fake provider receives no call made with Henry's credential, nothing is stored on A, and the request gets 401.

Sequential orders: S0's request authenticated after the binding gets 401 and makes no provider call (AC-47); S0's request completed before the binding finds no credential (A is empty) and makes no provider call with Henry's credential. Today the credential is loaded after authentication with no generation check (§2), so both interleaved cases fail.  [unit]

AC-48 (generation read failure, revision 3). Given a valid current session of Henry, when reading Henry's current session generation fails, then the request gets a server error, not 401, and the session cookie is neither replaced nor expired. Given a generation advance committed for Henry, the very next request with the old session gets 401 (no cached generation).  [unit]

AC-49 (operator unbind and reassign, revision 3). Given account A for `henry@acme.com`, bound under migration (a) to consumer Google GA with mailbox evidence, holding Henry's habits and a team membership, and given that Henry's Workspace G1 (hosted domain `acme.com`, verified) was refused and logged with its identity and evidence: when the operator runs the reassign step for A with G1, then
- A is bound to G1, with the address domain-confirmed, and A's habits and team membership are unchanged;
- A's generation advanced: every session of A from before gets 401, and a connect started before is credited to nobody;
- a later sign-in by G1 reaches A; a later sign-in by GA is refused;
- an audit record names the operator, the time, A, GA, G1, the evidence level, what was removed and the reason;
- no API route, web page or MCP tool can perform the step: the assembled contract contains no operation that writes or replaces a binding, and the MCP server's tool list contains none.

The same step on an unbound, non-empty account binds it. Today no binding exists, so every bullet fails.  [unit]

AC-17. Given an unbound pre-existing account for `bob@example.org` (non-Google consumer domain) whose recorded identifier is X, when the Google answer for G1 has a verified email but G1 ≠ X, then it is refused.  [unit]

AC-39 (revised r3). Given an unbound pre-existing account for `bob@gmail.com`:
- empty, when Google G1 signs in with `bob@gmail.com` verified, then it reaches the account, binds G1, and the address is domain-confirmed;
- with recorded identifier X ≠ G1 and one stored habit, the same sign-in is refused.  [unit]

AC-18 (revised r2). Given the change is deployed:
- no account, membership, credential or setting row is deleted, and the count of accounts is unchanged;
- a binding is written only by a sign-in, never by the deployment;
- before an existing account binds, granting sites resolve its address to nobody, and revoking and keeping sites act on it.  [unit] (migration test)

**Refusal, membership, work calendar, step-up, contract**

AC-19. Given any refusal in AC-1 to AC-49, the callback's redirect is the same for every refusal cause and differs from the generic `auth_failed` state. The web app, on that state, shows the §3 refusal message and a control that opens the sign-in dialog. The message names neither the address nor whether an account exists.  [unit] backend; [unit] web app (`smart-calendar-flow`)

AC-20. Given Henry is an organisation member with work calendar set to Google, when any Microsoft sign-in matching Henry only by address is refused, then Henry's organisation and work calendar are unchanged.  [unit]

AC-21. Given Henry bound to Google G1 and an organisation `acme.com` where PAC-50 step-up is required, when Henry's session completes a Google step-up whose identity is G2 (also `henry@acme.com`, verified, hosted domain `acme.com`, admin), then the step-up fails as an identity mismatch and no grant is issued. With G1 it succeeds as today. The Microsoft analogue holds for Henry bound to (T1, O1) and a step-up identity (T1, O2).  [unit]

AC-22 (revised r2). Given the contract, then:
- the three sign-in callbacks document the refusal outcome as a distinct, fixed redirect, alongside today's documented outcomes;
- the connect start and its callback are documented as distinct from the sign-in start, with their outcomes (credited, credited to nobody);
- every authenticated operation's 401 covers a session whose generation is no longer current, and a failure to read the generation is a documented server error, not 401;
- the connect callback's "credited to nobody" outcome covers a connect started under an older generation.  [contract]

AC-23. Given the full stack with a sign-in stand-in, when a second identity carrying Henry's address signs in, then the API's current-user response for any session the browser holds is not Henry's. If the plan does not add a sign-in stand-in, it drops this criterion with that reason (OQ-4), and AC-1 to AC-7b at unit level are the proof.  [e2e]

## 5. Explicitly out of scope

- **Everything listed in §10 Split.** In short:
  - adding, listing and removing sign-in methods, including SSO for existing members (F1);
  - Paceday email confirmation, "authority wins", and evicting a squatter or reclaiming a recycled address in the product (F2);
  - "sign out everywhere" and security notifications for the user (F3).
- **Removing today's work-calendar reset on the plain Microsoft sign-in path.** PAC-47 owns it (owner, PAC-47 vs PAC-67 OQ-13; PAC-47 D-17). This spec adds no new reset path (AC-31).
- **Organisation membership rules.** PAC-68 owns them. This spec changes only *which account* the rule is applied to.
- **Merging two existing accounts**, or administrator tools in the product to rebind an account. The operator procedure (§3) is documentation plus one operator-run step ("unbind and reassign") that only the deployment's operator can run on the server; it is not a product feature and has no API, web or MCP surface.
- **SAML.** Not implemented today. When it is, it must follow the §3 rules; recorded here so its spec inherits it.
- **Residuals this ticket accepts, release-noted:**
  - *Lock-out by a squatter.* An address carried by a mailbox-only account, by a departed holder, or by a legacy account refused under (b) cannot be taken by its rightful owner until an operator frees it (§3 "Operator procedure"; F2). The squatter gains nothing from it: the address resolves to nobody at granting sites.
  - *Unconfirmed addresses.* A user whose only evidence is mailbox evidence (a consumer Google account on a custom domain, or a Google Workspace user on a secondary domain) cannot accept address-based invitations until F2. That includes legacy users who bind under (a) with mailbox evidence.
  - *Break-glass.* An operator's break-glass needs an account holding the listed address domain-confirmed. Until the operator's account binds with authoritative evidence, and for good on a domain with no authority, break-glass does not work for them. The operator should list an address on a domain with an authority.
  - *Existing members and SSO.* Existing members cannot sign in through SSO until F1 (D-10, OQ-2).
  - *Legacy users of two providers* can sign in only with the bound one until F1.
  - *Personal Microsoft accounts* may be unable to sign in at all if OQ-3 item 2 finds no positive signal.
  - *Pre-gate SSO configurations.* An SSO configuration written before PAC-50's gate, when any member could write it, still gives authoritative evidence for new accounts at its domain. It cannot reach an existing account. The release notes ask operators to review every configured issuer at deployment.
  - *A stolen session* can connect the attacker's own calendar to the victim's account (PAC-47's model), but cannot add a sign-in method, so it ends with the session (expiry, or the operator advancing the generation).
  - *Pre-deploy sessions.* If the owner answers OQ-6 "no", a session obtained by email matching before the change stays valid up to 7 days on an account that does not bind in that time. On an account that does bind, such a session reaches nothing the binding stored, even in a request already authenticated when the binding commits (§3, AC-50; revision 4).
  - *Provider evidence.* The OQ-3 item 4 residual on unmanaged Microsoft directories, and reported past flaws that let Workspace accounts exist on unverified domains (`hd`). This residual also applies to migration rule (b): until stage 2 answers OQ-3 item 4, (b) binds an empty legacy account on Microsoft tenant evidence that an unmanaged directory might fake. The account is empty, so the binder gains no one else's data, but the rightful holder is then refused.
  - *Microsoft availability (revision 3).* The Microsoft tenant fails closed. If its source is a second Graph call, an outage of that call refuses every Microsoft sign-in, including bound users, with the refusal message, which then tells them to "sign in the way you did before". PAC-68 had chosen the opposite (open for sign-in); PAC-71 overrides it for the tenant (§3 constraints).
  - *Rule (a) trusts the record (revision 3).* A pre-deploy takeover by email matching that overwrote the recorded identifier is bound under (a) if the taker signs in first. It is undetectable from the data; the server log flags (a) bindings on mailbox evidence for non-consumer domains, and the operator's "unbind and reassign" step repairs it.
  - *Rule (b) is narrow (revision 3).* Most legacy accounts that were used at all are not empty, so a user whose recorded identifier differs from their incoming identity is refused and needs the operator unless they sign in the way they did before (rule (a)). Production has zero users (`docs/specs/PAC-22.md:80`).

## 6. What must not change

- **An existing user's next ordinary sign-in through the provider they used last still works.** Migration rule (a) covers it. Today's success tests protect the redirect and session issuance (`pac35_auth_flows_test.go:167`, `:239`, `:286`); their fixtures change as AC-8 states. No existing test protects *which account* is reached. That is a finding, and AC-8 and AC-15 are the new protection.
- **Sign-in requests exactly today's scopes** (owner, PAC-68 OQ-2; PAC-50 AC-24). `TestPAC50_SignInAuthorizeURLsCarryNoNewScope` (`backend/auth/pac50_signin_scopes_test.go:19-69`) protects it and must pass **unchanged**; AC-11 restates it for Microsoft. No identity scope (`openid`, `profile`, `email`) is added to the Google or Microsoft sign-in.
- **The session cookie is untouched on failure** (PAC-35, API-011). Protected by `pac35_auth_flows_test.go:142-165` and `:193-237`, whose failure assertions are kept. The refusal state and a connect credited to nobody keep this property.
- **The current-user response's `provider` still means "provider of the first sign-in"** (`paceday.gen.go:1186-1197`).
- **The SSO email-domain binding** (PAC-45) stays, protected by `handlers_sso_test.go:161-195`.
- **PAC-50 step-up's existing refusals** (`pac50_sso_stepup_test.go`) keep their reasons. AC-21 adds an identity check on top of them; it does not replace them. Break-glass freshness (10 minutes) is unchanged; AC-38 narrows who qualifies (§5 residual).
- **PAC-67 credential lifecycle**: reconnecting refreshes the credential and clears a recorded loss; disconnecting revokes it. A connect from inside the product keeps both.
- **The manager's removal of an address no account carries** still removes the manager assignment and pending invites and answers 204 (`handlers_manager.go:364-368`).
- **Must change.** `TestPAC67_MicrosoftCallbackKeepsRefreshAndFlipsOnlyTheCaller` (`pac67_conferencing_test.go:322-358`) asserts that a session-less Microsoft callback lands on an account matched by address. Under this spec it must become a connect (AC-12) or a bound-identity sign-in (AC-8), keeping its PAC-67 assertions: refresh token kept, loss cleared, the other user unaffected. The owner already expects this test to be amended for PAC-47.
- **Must change.** The web app's signed-in connect surfaces (§2 table) stop using the public sign-in start (AC-32); their tests follow.
- **Must change, in PAC-47** (`origin/factory/pac-47-spec`, not yet built). These ACs presuppose email matching: AC-3i (a) and (b), AC-3k and AC-3l (`PAC-47.md:650-695`). Their Given must say that the Microsoft identity is the user's bound identity, or their recorded identifier under migration (a). Otherwise PAC-71 refuses them. Their work-calendar assertions (D-17) are unaffected. AC-3j's new account needs a tenant that verifies `z` (rule 5). AC-3e and AC-3f already match the connect step and do not change.
- **Must change, in PAC-68** (`origin/factory/pac-68-spec` `ec76099`):
  - AC-15's first clause and D-5 (`PAC-68.md:115`, `:158`) must say "exactly today's set", without the `openid` allowance (§3 constraints).
  - AC-9, its Microsoft case (`PAC-68.md:95`): when the lookup that supplies the tenant errors or times out, the Microsoft sign-in is **refused** with PAC-71's refusal state and nothing is written, instead of "the sign-in still completes". AC-9's premise that the tenant may be "taken from the token response" needs a scope that is forbidden; it must name the source stage 2 picks (OQ-3 item 1).
  - AC-15's second clause (`PAC-68.md:115`): a Microsoft work account whose tenant lookup is refused for lack of permission is refused at sign-in, not "signed in as today, not joined". If stage 2 finds a tenant source independent of the verified-domain lookup, both clauses apply only when the tenant itself cannot be obtained; a failure of the verified-domain lookup alone keeps PAC-68's outcome (signed in, not joined).
  - D-4 (`PAC-68.md:157`): "fails closed for membership, open for sign-in" becomes "fails closed for membership; for sign-in, fails closed whenever the identity (the Microsoft tenant) cannot be established" (PAC-71 D-23).
- **Must change, in PAC-47** (addition, revision 3). AC-3i (c) (`PAC-47.md:665-675` at `24e0833`) allows creating `m@z` through a Microsoft sign-in. Under rule 5 that needs a tenant that verifies `z`, and under §3 "Identities" a tenant that can be obtained; the AC's Given must say so, as for AC-3j.
- **Must change, the e2e helper** (`e2e/auth.ts:17-25`). It mints sessions with no generation. They keep working for accounts at the initial generation (§3 "Sessions"); a test that advances an account's generation (or runs after the deploy-time step, AC-44) must mint the current generation. Every e2e test that inserts users and mints sessions is affected only in that case.

## 7. Rollback

- **Reverting the code** restores matching by address on every callback. That re-opens every §1 scenario, so a revert is a security regression and is release-noted as one.
- **Down migration.** Dropping the binding, standing and session-generation records loses the bindings, the address standings and the generations. Accounts, credentials and memberships are untouched. Sessions issued after the change keep working under the reverted code, which ignores the generation they carry. A session ended by a generation advance becomes valid again until it expires: the down migration re-opens every session that the deploy-time step (OQ-6) or a binding closed. Release-note it; rotating the signing secret on revert closes them.
- **Re-applying.** Every account is "pre-existing" again and re-binds lazily under (a)/(b). An account bound by (b) has no recorded identifier of the bound identity unless the plan records it on binding. The plan must record the bound identity's subject in today's recorded-identifier field as well, so that a re-apply re-binds it under (a).
- **Operator procedure.** An address freed by an operator stays freed after a revert. The placeholder address is not restored. A binding replaced by the operator's "unbind and reassign" step is lost with every other binding on the down migration; the audit records of operator steps are kept (the plan says where, so that a down migration does not drop them).
- **Connect state.** A connect in flight when the code is reverted completes under the reverted code's rules (no generation check). That window is the 5-minute state lifetime (§2).

## 8. Open questions

- **OQ-1: does connecting a work calendar also add that provider account as a sign-in method? (owner, product).**
  - **For this ticket: no**, on factory guidance, aligned with PAC-47 (`PAC-47.md:377-411`). A connect attaches the calendar credential only (§3). This removes revision 1's Blocking 5 and 6 and the fresh-proof machinery.
  - **Still open for follow-up F1**: whether the product should offer, after a connect, to also add that account as a sign-in method, or only through an explicit "add a sign-in method" step. Factory default for F1: only through the explicit step.
  - **Owner:** Nicolas Hochart. **Resolve by:** owner answer before F1's spec. Not blocking this ticket.
- **OQ-2: when an organisation enables SSO or changes its IdP, may existing members' accounts be reached through SSO without each adding it? (owner, product).**
  - **Factory default:** no (D-10). In this ticket existing members cannot use SSO at all. In F1 they add it as a sign-in method.
  - **Alternative:** the org's configured issuer counts as authoritative for its own domain even on existing accounts. That reopens §1 case 5 for whoever can change the configuration (PAC-50 admin step-up or break-glass).
  - **Owner:** Nicolas Hochart. **Resolve by:** owner answer before F1's stage 4. Not blocking this ticket: production has zero users (`docs/specs/PAC-22.md:80`).
- **OQ-3: what stage 2 must verify about provider answers within today's scopes (contract-author / plan-author, technical).** No call was made for this spec. Stage 2 records the answer for each item, with the documentation or a recorded call:
  1. **Microsoft tenant.** Graph `GET /organization` (fields `id`, `verifiedDomains`) is documented with `User.Read` as its least-privileged delegated permission (known from documentation, not called). Stage 2 confirms that it answers under exactly today's Microsoft scopes for a work account. `id` is the tenant; Graph `/me`'s `id` is the object id for a work account. Both are read with the same access token. The step-up already reads the same resource with wider scopes (`stepup.go:290`).
     - **Alternatives stage 2 must check (revision 3), none needing `/organization`:** (i) the `tid` claim of the Graph access token returned by today's code exchange. Microsoft documents access tokens as opaque to the client and Graph tokens cannot be signature-checked by a third party, but this one arrives directly from Microsoft's token endpoint over TLS in the code exchange. Stage 2 states whether reading it is supported and stable. (ii) The `client_info` value (`uid`, `utid`) that the v2 token endpoint returns when the token request includes `client_info=1`; it needs no scope. Stage 2 states whether it is documented for confidential web clients. If either is adopted, stage 2 also states how it agrees with `/me`'s object id.
     - **Whatever stage 2 finds, the rule stays fail-closed** (§3 "Identities"): a sign-in whose tenant cannot be obtained from the chosen source is refused. A source independent of `/organization` only narrows which outages refuse sign-in, and lets PAC-68 keep "signed in, not joined" for a failed verified-domain lookup alone (§6).
  2. **Personal-account classification.** Which answer, under today's scopes, positively marks a personal Microsoft account, and which identifier is its stable subject. `/organization` refusing personal accounts would not, on its own, tell "personal" apart from a transient error. If no positive signal exists, a Microsoft sign-in that is not classified as work is refused (AC-11b, AC-5) and the limit is release-noted (§5).
  3. **Personal-account address evidence.** Whether any answer under today's scopes says Microsoft verified the personal account's address, or reports it as the account's own sign-in name on a Microsoft consumer domain. Default if none: personal accounts carry no evidence (AC-11c).
  4. **Unmanaged ("viral") Microsoft directories.** Whether a self-service directory created by email verification can still list a domain in `verifiedDomains`, and whether today's answers can tell such a directory apart. If it can list the domain and cannot be told apart, Microsoft tenant evidence is no better than mailbox evidence. That would also weaken PAC-68 and PAC-50's step-up, which rely on the same evidence, and it becomes an owner question (product trade-off: Microsoft-only sign-up versus that residual). Until then the factory treats it as authoritative and release-notes the residual.
  5. **Google.** That v2 userinfo under `userinfo.email` returns `verified_email` and, for Workspace accounts, `hd`, and that its `id` is the stable subject (known from documentation, not called).
  6. **Identifier formats** for migration (a): the formats that Google's userinfo `id` and Graph's user `id` (work and personal) take, so that a recorded value matching more than one never matches.
  - **Resolve by:** the contract naming fields and formats. A human decision is needed only under item 4, or if any item would require a new permission, which the owner has already ruled out.
- **OQ-4: can e2e reach the callbacks? (e2e-author / plan-author).** Not today (`e2e/auth.ts:17-25`), the same as PAC-68 OQ-4. **Resolve by:** the plan, which either adds a sign-in stand-in or drops AC-23 with that reason.
- **OQ-5: connect mechanics and ordering (plan-author / orchestrator).**
  - The connect depends on the browser carrying its session across the provider's redirect, the same fragility as API-026.
  - PAC-47 defines the Outlook connect (§2). This spec extends the same semantics to Google. Whichever of PAC-47 and PAC-71 is built first implements the connect start and callback for both providers. The second reuses it. Neither redefines it.
  - PAC-68 and PAC-47 change the same three callbacks. Suggested order: PAC-71 first, because PAC-68's membership guarantee (§3 "Limit") depends on it. AC-31 holds whatever the order.
  - **Resolve by:** the plan stating the order.
- **OQ-6: end every existing session when PAC-71 deploys? (owner; no default).**
  - **Factory recommendation: yes**, by advancing every account's session generation once at deployment (§3 "Sessions", AC-44). Every user signs in once more. The step closes every session obtained through email matching before the change, including on accounts that never sign in again. Production has zero users (`docs/specs/PAC-22.md:80`), so the cost there is nil.
  - **Alternative:** no deploy-time step. Pre-deploy sessions end when their account binds (AC-42) or when they expire (at most 7 days).
  - **Owner:** Nicolas Hochart. **Resolve by:** owner answer before release (stage 7). Not blocking stages 2 to 6: AC-44 is conditional.
- **OQ-7: deliver email confirmation in this ticket or a follow-up?** Resolved in revision 2: follow-up F2 (§10).
- **OQ-8: is a refused Microsoft sign-in during a tenant-lookup outage acceptable? (owner, product; factory default recorded, not blocking).**
  - **Factory decision (revision 3, fail closed):** yes; the tenant is identity, and a bound Microsoft identity cannot be recognised without it (D-23). PAC-68's "open for sign-in" is overridden for the tenant.
  - **Alternative:** none safe that the factory can see within today's scopes, other than a tenant source independent of the lookup (OQ-3 item 1), which stage 2 checks anyway.
  - **Owner:** Nicolas Hochart. **Resolve by:** before release; the factory default applies meanwhile.

## 9. Decisions

| ID | Decision | Source |
|---|---|---|
| D-1 | Identity = Google account id; Microsoft (tenant, object id) from the same token's answers; Microsoft personal account as its own authority; SSO (verified issuer, subject). Identifiers compared only with their authority. | **Owner** for Google and Microsoft (PAC-68 OQ-1, "Google sub, Microsoft oid+tid"). **Factory-assumed** for SSO, personal accounts, and same-token sourcing. |
| D-2 | The address never links a sign-in to an account that has a bound identity, even with authoritative evidence. | Issue direction, filed at the owner's request ("A matching email address alone never links"). **Factory-assumed** that it holds even for authoritative evidence. |
| D-3 | Mismatch is refused; never a silent merge. | Issue direction; **factory-assumed** specifics (§3 Refusal). |
| D-4 | (revised r2) A connect from inside the product attaches a calendar credential to the signed-in account and never binds an identity or adds a sign-in method. In this ticket an account has one bound identity; adding one is F1. | **Factory guidance** (orchestrator, 2026-10-28), aligned with PAC-47 §3 item 3 (`PAC-47.md:377-411`). Answers OQ-1 "no" for this ticket. |
| D-5 | (revised r2) Three evidence levels (authoritative, mailbox, none) and two address standings (domain-confirmed, unconfirmed). Only domain-confirmed addresses act as a person at granting sites; revoking and keeping sites act on every carrier. | **Factory-assumed** (revision 1 Blocking 1; narrowed in revision 2 by factory guidance). |
| D-6 | One refusal message for every cause, so it does not reveal whether an account exists. | **Factory-assumed**. A foreign-tenant admin can set any address, so a distinct message would let anyone enumerate accounts. |
| D-7 | The account's address does not change when the provider reports a new one. | **Factory-assumed**. |
| D-8 | (revised r3) Pre-existing accounts bind lazily on the first post-deployment sign-in meeting (a) (same recorded identifier, Google/Microsoft only; binds the identity on record) or (b) (Google/Microsoft authoritative evidence on an **empty** account: no credential, connection, key, changed preference, relationship or stored data, per §2's inventory; the recorded identifier plays no part). Everything else is refused, logged, and left to the operator procedure. Binding locks the account, re-checks, binds and advances the generation as one atomic step. | **Factory decision** (orchestrator, 2026-10-28, revision 3 items 1 and 2, fail closed). |
| D-9 | Nothing is deleted: accounts, memberships, credentials, settings, accepted grants. | Factory rule; issue body. |
| D-10 | SSO is never authoritative over an existing account, bound or not. Existing members cannot use SSO until F1. | **Factory-assumed**; OQ-2. |
| D-11 | (withdrawn r2) Disconnect and sign-in methods. | Moved to F1 with sign-in-method management. |
| D-12 | (revised r2) A recycled address at the provider (new subject) is refused while any account carries it; an operator resolves it until F2. | **Factory-assumed**. |
| D-13 | PAC-50 step-up also requires the bound identity when the account's binding is at that authority. Break-glass authorises the account holding the listed address domain-confirmed, not a session email claim. | **Factory-assumed**. |
| D-14 | Sign-in requests exactly today's scopes; identity evidence comes from today's answers. PAC-68's `openid` allowance (AC-15, D-5) is overridden for sign-in scopes and PAC-68 must amend. | **Owner** (PAC-68 OQ-2, "offer both"; PAC-50 AC-24). |
| D-15 | A Microsoft sign-in must not reset an existing user's work calendar. This spec adds no reset path: a connect, a migration binding and a refusal never change it. | **Owner** (PAC-47 vs PAC-67 OQ-13). |
| D-16 | (withdrawn r2) Fresh proof for adding or removing a sign-in method. | Moved to F1. |
| D-17 | Addresses are compared case-insensitively everywhere; no new account may carry an address another carries in any case; existing case collisions are left untouched, refused at sign-in and granting sites, and acted on in full at revoking sites. | **Factory-assumed** (revision 1, Non-blocking 5; revision 2 for revoking sites). |
| D-18 | (withdrawn r2) Paceday email confirmation. | Moved to F2. |
| D-19 | The organisation's "extended access" step never binds or changes an identity. Revision 3: it does not exist on `main` (`contracts/features/PAC-50.yaml:215` says the step-up is not that step), so AC-35 is dropped; the ticket that builds the step inherits this decision and must carry the criterion. | **Factory-assumed** (revision 1, Non-blocking 7). |
| D-20 | (revised r3) Every account has a session generation carried by its sessions and checked on every authenticated request; it advances on every binding write and on the operator procedure. A failed read of it is a server error, not 401; no cache. Writes of any kind rule (b) counts are also checked against it when stored. (revised r4) A request of an older generation never obtains, uses or returns a credential, connection, key or account data stored after the advance, provider calls included. Every load of a credential or account data for a request re-confirms the request's generation against the account's current one in the same read; on a mismatch it returns nothing, no provider call is made, and the request ends as 401. | **Factory guidance** (orchestrator, 2026-10-28, item 2); read-failure and in-flight-write rules **factory decision** (revision 3, Challenge r2 Non-blocking 2 and Blocking 2); read and provider-call rule **factory decision** (orchestrator, revision 4, Challenge r3 Blocking 1 option (i), independent of OQ-6). |
| D-21 | A one-time advance of every account's generation at deployment is the factory's recommendation; the owner decides (OQ-6). | **Factory recommendation**, owner pending. |
| D-22 | Lock-out of a rightful owner by an account that carries the address is refused safely and resolved by a written operator procedure until F2. | **Factory guidance** (orchestrator, 2026-10-28, item 4). |
| D-23 | The Microsoft tenant is identity. A Microsoft sign-in whose tenant cannot be obtained is refused with the single message, whether it would create, bind or reach a bound account. Overrides PAC-68 D-4 "open for sign-in" for the tenant; PAC-68 AC-9, AC-15 second clause and D-4 amend. | **Factory decision** (orchestrator, 2026-10-28, revision 3 item 3, fail closed); OQ-8 for the owner. |
| D-24 | A connect is bound at its start to the account and its session generation; a completion under a different current generation is credited to nobody. A connect cannot be started by another site. | **Factory decision** (orchestrator, 2026-10-28, revision 3 item 2; Challenge r2 Non-blocking 5). |
| D-25 | Operator "unbind and reassign": operator-only (no API, web or MCP path), audited, advances the generation. Rule (a) binds the identity on record; (a) bindings on mailbox evidence for non-consumer domains are logged for review. | **Factory decision** (orchestrator, 2026-10-28, revision 3 item 4; Challenge r2 Non-blocking 1). |

## 10. Split

Revision 2 narrows PAC-71 to the identity core. The rest goes to the follow-up tickets below, which the orchestrator files. No Linear issue is created by this spec. Each follow-up inherits §3's identity model, the evidence table, the refusal rule and the session generation, and must not weaken them. Criteria and decisions retired here are listed with the follow-up that takes them over, and their numbers are not reused.

- **F1 — Sign-in methods: add, list and remove (including SSO for existing members).** Lets a signed-in user add a second identity to their account through an explicit link step, tied to the session and a single-use state. Adding needs fresh proof of the existing identity (revision 1's 10-minute rule, sessions issued before PAC-71 never qualifying). Settings lists every bound identity, and the user can remove one. The removal rule counts only methods that can sign in now: an SSO identity counts only for the issuer currently configured for its domain, and a Microsoft identity only while it can still be classified. Removal is atomic against a concurrent removal, so an account never ends with zero usable methods. Adding or removing a method advances the session generation, ending the account's other sessions. The account holder is told through a channel the session does not control (revision 1 Non-blocking 3). F1 answers OQ-1's remaining product part (whether a connect may offer to add the method) and OQ-2's mechanism. Through F1, existing members add their organisation's SSO, including after an issuer change, under PAC-45's rule that the SSO email belongs to `{domain}`. F1 states what a calendar disconnect says about a remaining sign-in method. Takes over: revision 1 §3 "The link step" and "Sign-in methods in Settings", AC-30, AC-33, AC-34, AC-36, D-11, D-16, revision 1 Blocking 5, Non-blocking 3 and 10.
- **F2 — Address re-verification and recovery.** Gives the person who controls an address now a product path to it, replacing the interim operator procedure:
  - Paceday email confirmation (single-use, expiring, opened in a browser holding the same account's session) and an "email-confirmed" standing;
  - "authority wins": an authoritative sign-in takes the address standing from an account that holds it only by mailbox evidence or email confirmation;
  - the rule under which an address held only unconfirmed never stops another identity from creating its own account and confirming the address;
  - when domain-confirmed standing lapses or moves (an authoritative sign-in from a different subject of the same authority, i.e. the domain owner reassigned the address), so a departed holder stops acting as the address;
  - what happens to pending address-based grants when standing moves, for team invites (stored by address) and co-host invitations (stored by user id);
  - resolution of letter-case collisions.

  Granting and revoking sites keep the split defined here, and break-glass checks the standing at the time of use. Takes over: AC-24, AC-26, AC-27, D-18, revision 1 Blocking 2 and 3, Non-blocking 6 and 9, and the lock-out residual of §5.
- **F3 — Session management and security notices for users.** Builds on the session generation added here. It offers a user-facing "sign out of all other sessions" in Settings, and states whether logout should also advance the generation. It notifies the account holder by mail, when a sender is configured, of security events: a migration binding, an operator freeing an address, and, once F1 exists, a method added or removed. Takes over: revision 1 §5 "notifying the user", the user-facing part of revision 1 Blocking 5's session-revocation requirement.
- **F4 (conditional; filed as PAC-77, https://linear.app/paceday/issue/PAC-77, after stage 2 confirmed the condition) — Sign-in with a personal Microsoft account.** Filed only if stage 2 finds (OQ-3 items 2 and 3) that today's Microsoft answers give no positive personal-account signal or no address evidence, so that personal accounts cannot sign in or create accounts under PAC-71. It decides, as an owner question, whether to obtain that signal through a permission that needs no admin consent, or to accept the limit. Takes over: the personal-Microsoft residual of §5.

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

## Revision 2 — 2026-10-28

Answers every finding of "Challenge, revision 1" by **narrowing** the ticket, on the orchestrator's factory-level guidance (header). §1–§9 are rewritten in place and §10 Split is new. The challenge texts and the Revision 1 record above are kept unchanged. Their line numbers refer to revision 1 (`ea39bbf`), not to this file.

**The core that ships:**
- An identity is (authority, subject), and an account has at most one bound identity.
- An address never links to a bound account.
- An unknown identity whose address an account carries is refused with the single message.
- A new account needs authoritative evidence, or mailbox evidence when no account carries the address.
- A connect from inside the product attaches a calendar credential and never adds a sign-in method (PAC-47).
- A per-account session generation lets a binding or an operator end other sessions.
- Migration binds only where no other party can be on the account.
- Granting sites resolve only domain-confirmed holders, and revoking and keeping sites act on every carrier.

### Blocking (Challenge, revision 1)

| # | Finding | Resolution |
|---|---|---|
| B1 | Migration (b) hands a real user's credential to an account that others may hold sessions or integrations on; OQ-6 "no longer required" false | **Resolved.** Rule (b) now binds only when the account has no recorded Google/Microsoft identifier **and** carries no stored credential, connection or key. Otherwise it refuses, leaves the account unbound, shows the single message and logs the account for the operator (§3 "Accounts created before this change", D-8). Justified by the challenge's own scenario: Henry's credential never reaches A. Every binding advances the session generation, ending sessions from before the binding (§3 "Sessions", D-20). OQ-6 is no longer described as unnecessary. It is an owner question with the factory recommending a one-time global advance at deploy (D-21, AC-44). ACs: AC-16 (revised), AC-41, AC-42, AC-44. |
| B2 | Mailbox squatter permanently locks out the current mailbox holder; contradicts §5 residual | **Moved to follow-up F2** (address re-verification and recovery). In this ticket the lock-out is refused safely: the squatter gains nothing, because the address resolves to nobody at granting sites (AC-40). The rightful owner is unlocked by the interim operator procedure (§3, D-22). §5's false claim ("whoever controls the mailbox can confirm the address by email") is removed and replaced by the stated lock-out residual. |
| B3 | Domain-confirmed standing never lapses; departed employee keeps the address; new hire refused | **Partly resolved, rest moved to F2.** The attack used a link-added personal Gmail to keep signing in after the Workspace account was suspended. With linking gone (F1), a departed holder has only their suspended identity and sessions that expire, or end when the operator advances the generation. The remaining part is still open: the address stays carried by the old account, so the new hire is refused. The operator procedure resolves it until F2 defines lapse and reassignment. Break-glass is keyed on the session's account (AC-38), so a freed address stops authorising the old account. |
| B4 | AC-28 turns member removal into a silent no-op; co-host keep set drops accepted unconfirmed co-hosts | **Resolved.** The §2 table classifies each site as granting or revoking/keeping. Granting sites resolve only domain-confirmed holders. Revoking and keeping sites act on every account carrying the address, case-insensitively, at any standing (§3, D-5, D-17). AC-28 is revised, and AC-43 is new: the manager removes an unconfirmed member, a case-collided pair are both removed, and a mixed-case accepted co-host is kept. This also pins today's mixed-case bug at `handlers_scheduling_links.go:553`. `handlers_manager.go:356-366`'s single-row read must cover every carrier. |
| B5 | Last-method rule not airtight; concurrent removal; "end other sessions" needs revocation | **Moved to follow-up F1** with sign-in-method management. This ticket has no user-facing linking or removal, so no account can lose its last method and no race exists. F1's scope carries the "usable now" counting rule and atomic removal. The revocation primitive is delivered **here** (session generation, D-20, AC-42), with its down-migration consequence in §7. |
| B6 | PAC-47 defines the four connect controls differently | **Resolved.** The connect step now follows PAC-47 §3 item 3 (`PAC-47.md:377-411`, `24e0833`) and extends it to Google. It attaches the credential to the starting account, credited only while that account is signed in, never binds and adds no sign-in method (§3, D-4). This answers OQ-1 "no" for this ticket (factory guidance). The "add a sign-in method" product question moves to F1. AC-12, AC-12b, AC-13, AC-14, AC-31 and AC-32 are revised to match PAC-47 AC-3e, AC-3f and D-18. The PAC-47 ACs that presuppose email matching (AC-3i (a)/(b), AC-3k, AC-3l, and AC-3j's evidence) are named in §6 "Must change, in PAC-47". OQ-5 says whichever spec is built first implements the connect for both providers. |

### Non-blocking (Challenge, revision 1)

| # | Finding | Resolution |
|---|---|---|
| N1 | PAC-35 success tests will not keep passing; fixtures lack verified flag and organisation answer | **Resolved.** §2 cites the fixture lines (`:145`, `:163`, `:169`, `:183`, `:196`, `:211`, `:241`). AC-8 (revised) and §6 say that only the fixtures change (Google verified flag; a Microsoft organisation answer listing the fixture domain) and that every redirect, cookie and kept-on-failure assertion stays. |
| N2 | SSO configurations written before PAC-50's gate count as authoritative | **Not applied as a rule; release-noted** (§5 residuals). An SSO issuer can no longer reach or take any existing account: rule 4 refuses, D-10 applies, and "authority wins" is gone. The residual is limited to new accounts at that domain. The release notes ask operators to review configured issuers at deployment. Rejected alternative: tracking whether each configuration was written under the gate needs provenance that today's rows may not carry. Proving it is more work than the reduced residual justifies. |
| N3 | A stolen fresh session is a complete, silent takeover | **Resolved by removal of the cause.** A session can no longer add or remove a sign-in method, so a stolen session ends with the session. The remaining residual, connecting the attacker's own calendar, follows PAC-47 and is stated in §5. Notifications move to F3, and F1 must notify on method changes. |
| N4 | Break-glass narrows more than §6 says | **Not changed; release-noted** (§5 residuals, §6). Break-glass still requires domain-confirmed standing, so an unbound or mailbox-evidence operator account is not break-glass. The reason: keying break-glass on an unconfirmed address would make break-glass squattable. The operator lists an address on a domain with an authority. |
| N5 | Letter case: migration (a) on collided accounts; AC-7 case race; `UNIQUE` vs two carriers | **Resolved.** A collision refuses every sign-in or migration match, including (a) (§3, AC-29). AC-7 now covers `Zoe@`/`zoe@` racing. Uniqueness: under revision 2 no two accounts can carry one address (rule 4 refuses), except grandfathered case collisions, so uniqueness stays on "carries", case-insensitively, for new accounts (§3 "Evidence and address standing", D-17). AC-24, which needed two carriers, is retired (moved to F2) and replaced by AC-40. |
| N6 | Deferring email confirmation costs more than stated | **Resolved by deciding the deferral.** Email confirmation moves to F2 (OQ-7 resolved). The costs are stated in §5 residuals: legacy users bound under (a) with mailbox evidence lose address-based invitations until F2, and the lock-out has only the operator procedure meanwhile. AC-26, AC-27 and D-18 move to F2. |
| N7 | PAC-68 AC-15 allows `openid`; PAC-71 forbids | **Resolved.** PAC-71 wins for sign-in scopes, per the owner's "keeps today's permissions" and PAC-50 AC-24 (§3 constraints, D-14). §6 records that PAC-68 must amend AC-15 and D-5. |
| N8 | Graph `/organization` under `User.Read` unverified; personal accounts may all fail | **Resolved as release note and conditional follow-up.** OQ-3 item 2 notes that a refusal alone does not classify an account as personal. §5 states the residual. F4 is filed only if stage 2 confirms it. |
| N9 | AC-27: pending co-host rows stored by user id | **Moved to F2.** AC-27 ("authority wins") leaves this ticket. In the core no standing moves between accounts, and granting sites never create a pending co-host row for an unconfirmed carrier. F2 must decide how pending rows move when standing moves. |
| N10 | Does PAC-45's domain rule apply to a linked SSO identity? | **Moved to F1**, whose scope states that it does. |

### Criteria the challenger could not falsify

| Criterion | Resolution |
|---|---|
| AC-11b, second sentence (two tenant sources) | **Dropped.** Under today's Microsoft scopes only the organisation answer carries the tenant: `/me?$select=id,mail,displayName` carries none and no ID token is issued without `openid`. So no second source exists. AC-11b says so. |
| AC-32, last sentence (disclosure before leaving) | **Dropped.** A connect no longer adds a sign-in method, so there is nothing to disclose. The rest of AC-32 is observable: which start each control navigates to. |
| AC-11c (two outcomes until OQ-3) | **Kept**, as the challenger accepted, with the contract recording the answer. |

### Citations the challenger found wrong

| Citation | Resolution |
|---|---|
| Revision 1 record, "public routes are `:33-42`" | **Fixed in §2.** `routes.go:38-39` (Zoom) are behind `requireAuth`. §2 now lists public and authenticated routes one by one, including detect `:40`. The revision 1 record is left as written, because it is history. |
| AuthDialog "three starts" | **Fixed.** §2 and AC-32 list four starts, including the SSO start at `AuthDialog.tsx:108` (which follows `/api/auth/detect`'s `redirect_url`; the challenger's `:105-108` is the block, `:108` the navigation). |
| AC-8 / §6 "today's success tests keep passing" | **Fixed** (N1). |

### Consumers the challenger listed

| Consumer | Resolution |
|---|---|
| `pac35_auth_flows_test.go:142,160,167,193,208,239` | Fixtures change, assertions kept (AC-8, §6). |
| `pac67_conferencing_test.go:322-358` | Unchanged requirement (§6 "Must change"); now becomes a connect or a bound sign-in. |
| `handlers_manager.go:356-366` | Must act on every carrier (B4, AC-43). |
| `PAC-47.md:377-411` | Aligned (B6); PAC-47 ACs to amend named in §6. |
| PAC-68 AC-15 / D-5 | Must amend (N7, §6). |
| `Index.tsx:568`, `AuthCallback.tsx:8` | Unchanged: AC-19 covers the refusal reader. |

### Acceptance-criterion ledger

- **Kept unchanged:** AC-1 to AC-6, AC-9, AC-10, AC-10b, AC-11, AC-11c, AC-14 (wording aligned to "connect"), AC-15, AC-17, AC-19, AC-20, AC-21, AC-23, AC-25, AC-35, AC-37.
- **Revised (same number, changed meaning, marked "revised r2"):**
  - AC-7 (connect instead of link; case race);
  - AC-7b (account with no recorded identifier);
  - AC-8 (fixtures);
  - AC-11b (second sentence dropped);
  - AC-12, AC-12b, AC-13 (connect, no binding);
  - AC-16 (b) and (c) now refused;
  - AC-18 (unbound legacy addresses resolve to nobody at granting sites);
  - AC-22 (contract outcomes);
  - AC-28 (granting vs revoking);
  - AC-29 (collisions at revoking sites);
  - AC-31 (no link-added identity);
  - AC-32 (four starts, disclosure dropped);
  - AC-38 (no email-confirmed);
  - AC-39 (requires no recorded identifier).
- **New:** AC-40 (squat in the core, replaces AC-24), AC-41 (migration (b) and pre-account hijack), AC-42 (session generation), AC-43 (removal never silent), AC-44 (deploy-time step, conditional on OQ-6).
- **Moved to follow-ups, numbers retired:** AC-24, AC-26 and AC-27 (F2); AC-30, AC-33, AC-34 and AC-36 (F1).

### Questions left for the owner

- **OQ-6 (needs an answer before release):** end every existing session at deploy? **Factory recommendation: yes**, by a one-time advance of every account's session generation (AC-44).
- **OQ-1 (for F1 only):** this ticket answers "no" on factory guidance, aligned with PAC-47. Open for F1: may a connect offer to add the sign-in method? Factory default for F1: only through an explicit "add a sign-in method" step.
- **OQ-2 (unchanged, matters from F1):** may SSO reach existing members without each adding it? Factory default: no.
- **OQ-3 item 4 (conditional):** becomes an owner question only if stage 2 finds that unmanaged Microsoft directories defeat tenant evidence. Items 2 and 3 may trigger F4.

## Challenge, revision 2 — 2026-10-28

**Verdict**: accept-with-changes.

I judged revision 2 against its own narrowed scope: is the core safe and shippable with F1 to F4 absent? Mostly yes. Linking is gone, so revision 1's B5 has no cause left, and the session generation is a sound primitive. Three problems still block stage 2:
- migration rule (b) still lets data from the rightful owner's new calendar credential reach other parties, through relationships and a race that its "no other party" test does not see;
- a connect can still be completed by a session that the binding was meant to end;
- PAC-71 contradicts PAC-68's accepted AC-9 on the same Microsoft callback.

What I checked:
- this spec at `05be889`;
- `clockwise-like` `origin/main` `ae5c2cf` (still the tip), `origin/factory/pac-47-spec` `24e0833`, `origin/factory/pac-68-spec` `ec76099`;
- `smart-calendar-flow` `origin/main` `eea0cf6`, read with `git show` only.

I ran no code and called no provider endpoint. Line numbers below are this file at `05be889`.

**Revision 1's blocking findings:**

| # | Status in revision 2 | Judgement |
|---|---|---|
| B1 | Resolved in part | Sessions from before the binding now end (lines 239, 254), and an account with a stored credential, connection or key is refused (lines 226-230). Two channels to other parties remain, and the "no recorded identifier" test cannot be decided from today's data. These are Blocking 1 and 2 below. |
| B2 | Legitimately moved to F2 | The squatter gains nothing: granting sites resolve the address to nobody (line 184, AC-40), and PAC-68 decides organisation membership on evidence. The rightful owner is locked out, and an operator procedure covers it (lines 284-298). That is lock-out, not takeover. |
| B3 | Legitimately moved to F2 | Without linking, a departed holder keeps only a suspended identity and sessions of at most 7 days, the same as today. Break-glass needs a session under 10 minutes old, so a departed holder who cannot sign in cannot use it. New invites to the address go to an account nobody can newly sign in to. That is lock-out, not exposure. |
| B4 | Resolved | §2's table classifies each site as granting or revoking/keeping (lines 125-138), and AC-43 pins removal and keeping. |
| B5 | Legitimately moved to F1 | No user-facing method management exists in the core, so there is no last-method race. The revocation primitive ships here (D-20, AC-42). |
| B6 | Resolved | The connect follows `PAC-47.md:377-411` at `24e0833` word for word on the points that matter: it attaches the credential to the starting account, is credited only while that account is signed in, and is recognised by a connect marker, not by a session being present. A new conflict with PAC-68 appears instead (Blocking 3). |

### Blocking

1. **Migration (b) checks only what is stored on the account. It does not check the relationships through which other accounts and public pages read the account's calendar. And it cannot tell an SSO subject from a Google or Microsoft identifier.**

   Lines 226-228 bind an authoritative identity to an unbound account that has "no recorded last-sign-in identifier from Google or Microsoft (the recorded value is empty, or is an SSO subject)" and "no stored provider credential and no third-party connection or key". Binding then stores the new holder's calendar credential on that account (`handlers_auth.go:71`, `handlers_microsoft_auth.go:74`). Two facts make this unsafe:
   - **Other accounts read a member's calendar through Paceday relationships, not through credentials stored on the member's own account.**
     - Team availability and team analytics: any member of a team reads slots and analytics computed from every member's stored credential (`handlers_teams.go:492-533`, `engine/team_availability.go:78-84`).
     - Public scheduling links: the link's public slot listing reads each accepted host's stored credential (`handlers_booking.go:80-88`). The slug is public to whoever knows it, and the link's owner can be another account.
   - **"Is an SSO subject" cannot be decided.** §2 line 44 says it itself: "Nothing records which provider that identifier belongs to." `provider` records the *first* sign-in's provider and `provider_id` the *last* one's (`users.go:21-31`). An SSO issuer chooses its subjects freely, so a subject can look like a Google or Microsoft identifier, and the reverse. If the plan reads "SSO subject" as "`provider = 'sso'`", then an account first created through SSO and later signed in with a consumer Google account GA counts as having no recorded identifier, although GA is recorded. That account carries no credential once GA disconnects (`handlers_auth.go:194` deletes the Google token).

   *Attack (works with F1 and F2 absent):*
   - Before PAC-50's gate, an `acme.com` member points the SSO configuration at their own IdP. They sign in through it as `henry@acme.com`, and email matching creates account A with `provider = 'sso'`. Henry has never used Paceday.
   - From their own account B, the attacker creates a team and a scheduling link with a known slug. From A, they accept B's team invite and the co-host invitation. They connect nothing on A.
   - After deployment, Henry signs in for the first time with acme's Workspace G1 (`hd=acme.com`). A has an "SSO subject" and no credential, connection or key, so rule (b) binds G1 to A and stores Henry's Google credential on A. The attacker's sessions on A end (line 239), as intended.
   - B still reads Henry's busy times and meeting analytics through the team, and the public link exposes Henry's free slots. Bookings land on Henry's calendar.
   - Nothing tells Henry: notifications are F3, and Settings shows the team and the link as his own.

   This is revision 1's B1 ("integrations on A keep receiving Henry's meeting data") through a channel that line 228's list does not name. The variant where the IdP really was acme's and the account was a predecessor's (a recycled address) gives the new holder the predecessor's bookings, booker names and addresses.

   *Resolve:*
   - Define "no recorded identifier" by a test that today's data can decide, in the safe direction. Either only an empty value qualifies, or any value that is not provably an SSO subject counts as recorded.
   - Extend (b)'s second condition, or the contract's enumeration that line 228 defers to the plan, to every relationship through which another account or a public page reads the account's calendar or data. At minimum: membership of a team that has other members; accepted or pending co-host rows on a link owned by another account; scheduling links the account owns that have other hosts or are publicly listable. With any of them present, (b) refuses (operator procedure), the same as for a connection.
   - Add an AC: an unbound pre-existing account with an SSO-recorded identifier, no credential, and membership of another account's team (separately: an accepted co-host row on another account's link) is refused under (b), and no credential is stored on it. Add AC-41 cases for an empty identifier versus `provider = 'sso'` with a Google-format value.

2. **The binding ends old sessions only for requests that start after it. In-flight requests from those sessions, and a connect that was started before it, still write to the newly bound account.**
   - The generation is checked once, when a request is authenticated (line 252). Rule (b)'s "no credential, connection or key" is a check made at binding time (line 228). Nothing says the two are atomic against writes by sessions of the old generation.
   - The connect is credited "only while the account that started it is still the one signed in when the provider step completes" (line 210). Line 252 refuses stale sessions only on "every authenticated request", and today's provider callbacks are public routes (`routes.go:34`, `:36`). AC-14 (line 422) tests no session and another user's session, but not the same account's session with a stale generation.
   - The state cookie lives 5 minutes (`handlers_auth.go:34`, `handlers_microsoft_auth.go:32`). Credentials are one row per user and calendar (`017_oauth_tokens_per_user.up.sql:12`), so a connect replaces the stored credential.

   *Attack A (connect across the binding):*
   - A squatter holds session S0 on account A, which qualifies for (b). Every few minutes, S0 starts a Google connect and leaves Google's consent page open.
   - Henry binds A under (b). His Google credential is stored on A, and S0's next authenticated request gets 401. That 401 tells the squatter the binding has happened.
   - Within the 5-minute state window, the squatter completes the pending consent with their own Google account. The callback sees S0, which still names A.
   - If the connect callback does not check the generation, it credits A. The squatter's calendar credential replaces Henry's, and from then on Paceday writes Henry's meetings, attendees and focus blocks into the squatter's calendar.

   *Attack B (write during the check):* during the same window, S0 sends a stream of requests that set a personal AI key or start a Slack connection. A request that passed authentication before the binding commits writes its key or connection after (b)'s check, onto the account now bound to Henry.

   *Resolve:*
   - Require that a connect completion counts the account as signed in only if the session's generation is current when the callback is handled. Otherwise it is credited to nobody, as in PAC-47 AC-3f. Add AC-14 (c): A's session issued before A's generation advanced completes a connect; nothing is stored and no session changes.
   - Require that (b)'s conditions hold at the moment the binding and the generation advance take effect. A write by a session of the old generation either lands before that moment, and is then seen by the check, or is refused.
   - Add an AC that interleaves the binding with a write from a pre-binding session, using a deterministic test seam, not timing.

3. **PAC-71 contradicts PAC-68's accepted AC-9 and AC-15 on the Microsoft callback, and §6 names only the scope half.**
   - PAC-68 AC-9 (`PAC-68.md:95` at `ec76099`) says a separate verified-domain lookup that "errors or times out" still lets the sign-in complete, "with the same redirect and session as a successful sign-in today", not joined. PAC-68 AC-15's second clause (`:115`) says the same for a lookup refused for lack of permission. D-4 (`:157`) states the principle: "fails closed for membership, open for sign-in".
   - PAC-71 takes the Microsoft tenant, which is part of the identity, from that same lookup. Line 163 says so, and line 384 says it is the only source under today's scopes. When it cannot be obtained, the sign-in fails (line 163, AC-11b at line 380), and for a new account it is refused (AC-11 at line 377).
   - So a bound Microsoft user whose `/organization` call errors is signed in under PAC-68 and gets the generic failure under PAC-71. Whichever spec is built second fails the other's test.
   - §6 line 533 asks PAC-68 to amend only AC-15's scope allowance and D-5. AC-9 relies on a tenant "taken from the token response", which needs `openid`, and that is forbidden here.

   *Resolve:*
   - Add PAC-68 AC-9 (its Microsoft case), AC-15's second clause and D-4 to §6 "Must change, in PAC-68", stating PAC-71's outcome: generic failure, nothing written.
   - Add a decision row recording that PAC-71 overrides "open for sign-in" for Microsoft, because the tenant is identity, not membership evidence. Release-note that Microsoft sign-in now depends on a second Graph call.
   - If the factory prefers PAC-68's availability stance, say instead how a bound Microsoft identity is recognised without the tenant. Line 157 suggests it cannot be.

### Non-blocking

1. **Rule (a) with mailbox evidence makes a takeover from before deployment permanent, and the operator procedure cannot undo it.**
   - Before deployment, a mailbox holder's consumer Google account GA signs in to Henry's account by email matching. That overwrites `provider_id` with GA (`users.go:28`).
   - After deployment, whichever of the two signs in first, GA binds under (a) (lines 221-225). Henry's G1 is refused under (a) because the identifier differs, and under (b) because an identifier is recorded.
   - The procedure's step 3 (line 295) only frees the address. That leaves Henry's data with GA and gives Henry a new, empty account.

   Either state this residual in §5, and add to the procedure how an operator unbinds an account bound to the wrong identity, or have the server log flag every (a) binding made on mailbox evidence on a non-consumer domain, for the operator to review.
2. **Failure mode of the per-request generation read.** Line 252 adds a database read to every authenticated request. `requireAuth` reads no server state today (`middleware.go:39-75`). State that a failed read is a server error, not 401: the web app treats 401 as signed out. State also whether any cache is allowed, and if so the maximum time a closed session can keep working. Nothing else validates sessions: `requireAuth` is the only caller of `ValidateToken`. There are no per-user API or MCP tokens: the MCP server uses one shared `MCP_AUTH_TOKEN` (`mcp/main.go:29`) and calls the backend without user credentials (`mcp/client.go:22-41`). So the generation covers every way in that exists today.
3. **§2's address-as-person table misses one join.** `AssignManagerTeamMembersMatchingFormalTeam` (`storage/manager.go:155-168`) resolves roster addresses to accounts with `lower(u.email) = lower(mtm.member_email)`. `handlers_manager.go:621-660` (`listManagerMembers`) and `:669-697` (`getOrCreateScopedMember`) map formal members to roster entries by their stored address. Classify these sites as granting or keeping.
4. **AC-5 and AC-11c depend on OQ-3 item 2.** If no positive signal for a personal account exists, the personal-account sign-in in AC-5 ends on AC-11b's generic failure, not on "refused". State AC-5's personal-account outcome as conditional, as AC-11c already is.
5. **A connect start triggered from another site.** Line 211 requires the connect to be recognised by "something that a connect action of the signed-in user produces and another site cannot". If the start itself is a GET that issues that marker, a cross-site navigation can start a genuine connect in the victim's browser. A provider that skips the consent screen then replaces the victim's stored credential with whichever provider account that browser is signed in to. Add an AC-14 case: a cross-site navigation to the connect start stores nothing.
6. **PAC-47 AC-3i (c)** (`PAC-47.md:665-675`) allows, as one outcome, creating `m@z` through a Microsoft sign-in. Under rule 5 that needs a tenant that verifies `z`. §6 line 532 names AC-3j for this but not AC-3i (c).
7. **e2e mints sessions** (`e2e/auth.ts:17-25`), and those sessions will carry no generation. They stay valid only while the account is at the initial generation (line 253). State that accounts created after the deploy-time step (AC-44) start at the initial generation, or that the e2e helper must mint the generation.
8. **Rule (b) and Microsoft evidence.** Until stage 2 answers OQ-3 item 4, (b) binds legacy accounts on tenant evidence that an unmanaged directory might fake. Consider listing (b) by Microsoft evidence in §5's provider-evidence residual explicitly.

### Criteria I could not falsify

- **AC-7 and AC-7b, the atomicity part.** Today both fail deterministically: the second sign-in upserts into the first account (`users.go:25`), and `Zoe@`/`zoe@` create two rows. After the change, a test that runs the two sign-ins one after the other passes even with a non-atomic check-then-insert. Require the test to force the interleaving through a seam, or to assert a storage-level constraint.
- **AC-35.** The owner's optional "extended access" step does not exist on `ae5c2cf`. Only PAC-50's step-up exists, and its contract says it is not that step (`contracts/features/PAC-50.yaml:215`). No test can run this criterion in this ticket. Make it conditional on the step existing, or move it to the ticket that builds the step.
- **AC-5, personal-account half.** See Non-blocking 4.

Every other AC has an offline test that fails today and passes afterwards. For example:
- AC-41's first case fails today because no session is ever refused (`middleware.go:57`);
- AC-43's co-host case fails today at `handlers_scheduling_links.go:553`.

### Citations I checked and found wrong

None in revision 2's new or changed citations. Checked and correct at `ae5c2cf` / `eea0cf6` / `24e0833` / `ec76099`:
- `routes.go:32-50` (public `:33-36,40-42`, Zoom `:38-39` behind `requireAuth`, protected `:47-49`); `middleware.go:18-24,39,57,71-73`; `jwt.go:11-27`; `handlers_me.go:31-42`.
- `handlers_auth.go:42-84,64,71,77,79`; `handlers_microsoft_auth.go:40-84,65,69,74,78,79,83,90`; `handlers_sso.go:62-65,103-167,155,160,162,183-247`; `handlers_sso_stepup.go:43,228-252,238`; `microsoft_oauth.go:90`; `users.go:21-31,23,28,37-40`; `006_auth.up.sql:5,8-9`; `storage/sso_stepup.go:70`.
- `handlers_scheduling_links.go:377-382,547-562,550-557,553,557,558-561,656-660`; `handlers_manager.go:319,345-369,356,364-368`; `engine/manager.go:244`; `handlers_teams.go:300-305`.
- `pac35_auth_flows_test.go:142,145,160,163,165,167,169,183,193,196,208,211,237,239,241,246,286`; `pac67_conferencing_test.go:322-358,333-341,346-349`; `handlers_teams_test.go:53-60`; `pac50_signin_scopes_test.go:19-69`; `e2e/auth.ts:13-25`.
- `PAC-47.md:377-411,650-695`; `PAC-68.md:115` (AC-15), `:158` (D-5); `PAC-22.md:80`; `api-audit.md:284` (API-026), `:350` (API-092).
- `smart-calendar-flow`: `client.ts:1181-1182`, `WorkCalendarConnection.tsx:77,87`, `ConnectionStatus.tsx:18`, `Dashboard.tsx:475`, `Onboarding.tsx:80`, `AuthDialog.tsx:79,98,102,108`, `AuthCallback.tsx:8`, `Index.tsx:568`, `SignInFailedNotice.tsx:8`.

One imprecision: line 136 says the manager's removal "answers 204 (`:364-368`)". The removals are at `:365-366` and the 204 at `:368`. The range is right.

### Consumers this breaks

The consumers already listed by revision 2 stand. Add:
- `clockwise-like` `docs/specs/PAC-68.md:95` (AC-9), `:115` (AC-15, second clause) and `:157` (D-4) on `origin/factory/pac-68-spec` (Blocking 3);
- `clockwise-like` `docs/specs/PAC-47.md:665-675` (AC-3i (c)) (Non-blocking 6);
- `clockwise-like` `e2e/auth.ts:17-25` and every e2e test that inserts users and mints sessions (Non-blocking 7).

## Revision 3 — 2026-10-28

Answers every finding of "Challenge, revision 2" inside revision 2's narrowed scope. No follow-up ticket is added and none is widened; §10 is unchanged. §1–§9 are edited in place. The challenge texts and the Revision 1 and 2 records above are kept unchanged, and their line numbers refer to their own revisions. The four orchestrator decisions (header) are factory decisions, fail closed, not owner decisions.

**Placement note.** The orchestrator asked for rule (b)'s tables to be enumerated and cited. The spec-author rules keep table names out of §3–§5, so the inventory with citations is in §2 ("What an account can carry today"), and §3 rule (b) lists the same kinds in prose against it. Likewise the locking mechanism (`SELECT … FOR UPDATE` on the account row, the generation check in `requireAuth`) is stated in §3 as behaviour (lock, re-check, bind, advance, all or nothing), and the plan names it.

### Blocking (Challenge, revision 2)

| # | Finding | Resolution |
|---|---|---|
| B1 | Rule (b) misses relationships through which other accounts and public pages read the calendar; "is an SSO subject" is undecidable | **Resolved** (orchestrator decision 1). (b) binds only an **empty** account: no credential, connection, key, AI destination, changed preference or record of a lost credential; no team membership, created team or sent invite; no owned link and no host or co-host entry in any status; no booking or booking event; no manager profile, roster, or roster entry pointing at it; no focus block, habit, analytics, brief, recap or activity entry; no step-up grant or SSO-write record. The inventory cites every migration at `ae5c2cf` (§2). A kind added later and not classified counts as non-empty. The "SSO subject" clause is removed: the recorded identifier plays no part in (b), so an SSO-created account is either empty or goes to the operator. ACs: AC-45 (one case per relationship kind: team, co-hosted link in each status, owned link, booking, manager/roster, stored data, organisation admin); AC-41 (an empty account binds whatever its recorded identifier; each credential kind refuses); AC-16 and AC-39 now refuse on a non-empty account. |
| B2 | A connect started before the binding, and in-flight writes from old sessions, still land on the newly bound account | **Resolved** (orchestrator decision 2). A connect is bound at its start to (account, generation); its completion is credited to nobody if the generation moved (§3 "Connecting a calendar", D-24, AC-14 (c)). Binding is one atomic step: lock the account, re-check, bind, advance (§3, D-8). Old-generation requests get 401. A write of any kind (b) counts is also checked against the current generation when it is stored, so a request authenticated just before the binding cannot store just after it. That last point tightens decision 2: the per-request check alone leaves that window open. ACs with an interleaving seam: AC-46 (a write between check and bind refuses the binding), AC-47 (a write authenticated before the binding and stored after it is refused). |
| B3 | PAC-71 contradicts PAC-68 AC-9 / AC-15 / D-4 on the Microsoft callback | **Resolved** (orchestrator decision 3). The tenant is identity and fails closed: without it a Microsoft sign-in is refused with the single message, whether it would create, bind or reach a bound account (§3 "Identities", D-23; AC-11b revised for all three cases). §6 names PAC-68 AC-9 (`PAC-68.md:95`), AC-15's second clause (`:115`) and D-4 (`:157`) at `ec76099`, with replacement text. The outcome is the refusal state, not the generic failure the challenger suggested: orchestrator decision 3 asks for the single message. OQ-3 item 1 names two tenant sources that do not need `/organization` (the access token's `tid` claim; the token endpoint's `client_info`) for stage 2 to verify; the rule stays fail-closed either way. Release note added (§5) and owner question OQ-8 recorded. |

### Non-blocking (Challenge, revision 2)

| # | Finding | Resolution |
|---|---|---|
| N1 | Rule (a) with mailbox evidence makes a pre-deploy takeover permanent | **Resolved** (orchestrator decision 4). §3 says (a) binds the identity on record, treated as the creator of record, and that the record cannot tell a creator from a pre-deploy taker. The operator procedure gains "unbind and reassign": operator-only, audited, sets the binding to the verified identity, advances the generation (§3, D-25, AC-49). (a) bindings on mailbox evidence for non-consumer domains are logged for review. §5 residual added. |
| N2 | Failure mode and caching of the per-request generation read | **Resolved.** A failed read is a server error, not 401, and no cache is allowed (§3 "Sessions", D-20, AC-48, AC-22). |
| N3 | The address-as-person table misses the roster join and two manager mappings | **Resolved.** §2 adds `storage/manager.go:155-168` (granting), `handlers_manager.go:669-697` (granting) and `:621-660` (keeping); §3 and AC-28 list them. The challenger's imprecision on `:365-366` and `:368` is fixed too. |
| N4 | AC-5's personal-account half depends on OQ-3 item 2 | **Resolved, fail closed.** An unclassifiable Microsoft answer is now a refusal, not the generic failure (§3 "Identities"). So AC-5's outcome is "refused" whatever stage 2 finds; only the logged cause differs. F4 stays conditional, for the product question of letting personal accounts sign in at all. |
| N5 | A connect start triggered from another site | **Resolved.** §3 requires that another site cannot start a connect that can store a credential; AC-14 (d). §2 records that today's starts are plain GETs with an unbound 5-minute state. |
| N6 | PAC-47 AC-3i (c) not named | **Resolved.** §6 "Must change, in PAC-47" adds AC-3i (c) (`PAC-47.md:665-675`). |
| N7 | e2e mints sessions with no generation | **Resolved.** New accounts start at the initial generation; the e2e helper must mint the current generation only for accounts whose generation moved (§3 "Sessions", §6, AC-42 last bullet). |
| N8 | Rule (b) on Microsoft evidence and unmanaged directories | **Resolved as a residual.** §5's provider-evidence residual names (b) explicitly. With (b) limited to empty accounts, the exposure is lock-out of the rightful holder, not data. |

### Criteria the challenger could not falsify

| Criterion | Resolution |
|---|---|
| AC-7, AC-7b atomicity | **Rewritten** with an interleaving seam (§4 conventions, following PAC-22 AC-27 and AC-28 on `main`): the competing creation or binding is stored between the other's check and its store, and the stored state is asserted. The sequential orders are stated separately. |
| AC-35 (extended access) | **Dropped.** The step does not exist on `main` (`contracts/features/PAC-50.yaml:215`). D-19 stays, as a decision that the ticket building the step inherits together with this criterion. The number is retired. |
| AC-5, personal-account half | **Rewritten** (N4): one observable outcome, refused, nothing written. |

### Consumers the challenger listed

| Consumer | Resolution |
|---|---|
| `PAC-68.md:95` (AC-9), `:115` (AC-15 second clause), `:157` (D-4) | §6 "Must change, in PAC-68", with replacement text (B3). |
| `PAC-47.md:665-675` (AC-3i (c)) | §6 "Must change, in PAC-47" (N6). |
| `e2e/auth.ts:17-25` and the e2e tests that mint sessions | §6 "Must change, the e2e helper" (N7). |

### Acceptance-criterion ledger (revision 3)

- **Revised (marked "revised r3"):** AC-5, AC-7, AC-7b, AC-11b, AC-14 ((c) and (d) added), AC-16 (non-empty Given), AC-39, AC-41. Extended without a new mark: AC-8 (tenant fixture), AC-19 (range now AC-1 to AC-49), AC-22 (generation read failure; connect under an older generation), AC-28 (new manager sites), AC-42 (new accounts' initial generation).
- **New:** AC-45 (every relationship kind refuses (b)), AC-46 (competing write between check and bind), AC-47 (old-generation write after the bind), AC-48 (generation read failure), AC-49 (operator unbind and reassign).
- **Dropped, number retired:** AC-35.

### Questions left for the owner

- **OQ-6 (unchanged, needed before release):** end every existing session at deploy? Factory recommendation: yes (AC-44).
- **OQ-8 (new, not blocking):** a Microsoft tenant-lookup outage refuses Microsoft sign-in (fail closed, D-23). Factory default: accept.
- **OQ-1 and OQ-2 (unchanged, matter from F1)** and **OQ-3 item 4 (conditional)**, as in revision 2.

## Challenge, revision 3 — 2026-10-28

**Verdict**: accept-with-changes. One blocking finding, and it is narrow. **Ready for the contract stage: not yet.** It is ready once Blocking 1 is resolved: one requirement sentence in §3, matching text in D-20, and one interleaved AC. That fix extends a rule revision 3 already states, so it needs no new scope or follow-up. After it, I see no remaining path to takeover, cross-account exposure or a misattached credential inside the narrowed scope.

What I checked:
- this spec at `38d5350`;
- `clockwise-like` `origin/main` `ae5c2cf`, `origin/factory/pac-68-spec` `ec76099`, `origin/factory/pac-47-spec` `24e0833`;
- every migration `001` to `030` at `ae5c2cf`, for tables that refer to `users`.

I ran no code and called no provider endpoint. Line numbers are this file at `38d5350`.

Per the factory's instruction for this round, I looked only for blocking-grade problems beyond verifying revision 3's fixes. That means takeover, another person's data exposed, a credential on the wrong account, a contradiction a reviewer could not resolve, or an unfalsifiable security AC. Lock-out cases that the operator procedure covers are non-blocking by definition.

**Challenge revision 2's blocking findings:**

| # | Status in revision 3 | Judgement |
|---|---|---|
| B1 (rule (b) ignores relationships; "SSO subject" undecidable) | Resolved | §2's inventory (lines 127-153) matches every table that refers to `users` in migrations 001-030. My grep of `REFERENCES users` and `user_id` finds nothing it omits. The one join table not named, `no_meeting_zones`, hangs off `teams`, which is counted. Rule (b) (lines 280-295) counts every kind, fails closed on unclassified future kinds, and no longer uses the recorded identifier. AC-45 has one case per relationship kind. The two channels I named in round 2 are both counted: team availability (`engine/team_availability.go:78-84`) and the public link's host credential read (`handlers_booking.go:80-88`). The manager's per-member reads go through `member_user_id` plus `OwnsTeamWithMember` (`engine/manager.go:437-472`), which are also counted. |
| B2 (in-flight connects and old-session writes) | Resolved for writes and connects; **open for reads** | The connect is bound to (account, generation) and refused after an advance (line 261, AC-14 (c)). Binding locks the account, re-checks, binds and advances (lines 303-309). Writes of every counted kind are also checked against the generation when stored (line 309, D-20). AC-46 and AC-47 use the interleaving seam. The same window still exists for a request that *reads or uses* the account's credential, and only writes are guarded. See Blocking 1. |
| B3 (PAC-68 AC-9 / AC-15 / D-4 contradiction) | Resolved | The tenant fails closed in all three cases (lines 208-213, AC-11b (i)-(iii)). §6 "Must change, in PAC-68" names AC-9 (`PAC-68.md:95`), AC-15's second clause (`:115`) and D-4 (`:157`), with replacement text. I checked `:95` and `:157` at `ec76099`: they say what §6 says they do. D-23 and OQ-8 record it. A reviewer building either spec second now has one consistent outcome. |

**The author's stated interpretations:**
- *An empty account binds whatever its recorded identifier (AC-41).* Agreed. An empty account holds nothing that the binder could gain and has no relationship through which the new holder's data could leave. Whoever recorded the identifier loses only a sign-in method on an empty account. That is lock-out, and the operator procedure covers it.
- *`users.org_id` is not counted.* Agreed. Organisations are unique by domain (`007_organizations.up.sql`, `domain ... UNIQUE`), and association is by the address's domain (`storage/orgs.go:42-52`). So a legacy account's `org_id` can only be the organisation the binding sign-in's own domain maps to. Membership is read only for the member list (`storage/orgs.go:55-58`) and for an "is in an organisation" flag (`storage/settings_ai.go:144`). Neither exposes the member's calendar.
- *Any changed preference makes the account non-empty.* Agreed as fail-closed. The cost is lock-out only (Non-blocking 3).
- *AC-11b ends on the refusal state, not the generic failure.* Agreed. This is consistent with §3 "Refusal" (line 338) and orchestrator decision 3, and the "Refused" convention makes it testable.
- *The extra write-time generation check.* It is sound and needed: the per-request check alone leaves a window between authentication and storage. It is also exactly the reasoning that Blocking 1 extends to reads.

### Blocking

1. **A request from an old-generation session that was authenticated just before a rule (b) binding can still read, or act through, the credential the binding stores. The rightful holder's calendar then reaches the squatter.**
   - Line 309 guards only "a write of any kind that (b) counts". Every other request is checked only "when it is authenticated" (line 309; §3 "Sessions", line 322).
   - The handlers authenticate first and load the credential afterwards. For example, `GET /api/calendar/events` passes `requireAuth`, then calls `getCalendarClient(r)` (`handlers_calendar.go:162`), then lists the provider's events (`:168`).
   - Under (b) the account is empty, so it has no credential before the binding. Any credential such a request loads after the binding commits is the new holder's.

   *Attack (needs only what revision 3 already concedes):*
   - A squatter keeps a pre-deploy session S0 on an empty legacy account A for `henry@acme.com`. Revision 2 Blocking 1's SSO squat, or §1 case 7 before the change, are examples.
   - The squatter keeps sending requests that read the calendar or act on it: list events, free/busy, compress, suggest, create a meeting, NLP confirm.
   - Henry signs in with acme's Workspace. (b) binds A and stores his Google credential.
   - A request of S0 authenticated before the binding commits, and loads the credential after it. The squatter receives Henry's events, or creates a meeting on Henry's calendar in his name.
   - Creating a provider-side event is not "a write of any kind that (b) counts", because nothing is stored in Paceday. The write-time check therefore does not cover it either.
   - The window per request is short. But the squatter chooses how many requests to keep in flight, and Henry's first sign-in is the trigger.
   - The attack needs a pre-deploy session to survive deployment, which is the OQ-6 "no" branch. OQ-6 has no default and the spec must be safe in both branches. §5's "Pre-deploy sessions" residual describes the risk as "stays valid ... on an account that does not bind". This case is an account that *does* bind.

   *Resolve:* either of these.
   - (i) State in §3 and D-20 that a request of a session whose generation is older than the account's current one never obtains, uses or returns a credential or any data stored on the account after its generation advanced. The rule covers provider calls made with that credential. Add an interleaved AC on the AC-47 pattern: S0's calendar-read request authenticated before G1's binding, with its credential load after it, returns none of Henry's events and makes no provider call with Henry's credential. A second case does the same for a meeting-create request.
   - (ii) Or make rule (b) bind only when no session of an older generation can still be valid for the account, for example only after the deploy-time step. Then state that (b) depends on the OQ-6 answer.

   Either way the stated property must cover reads and provider-side actions, not only Paceday-stored writes.

### Non-blocking

1. **AC-46 and AC-47's first case cannot occur.** "S0 accepts a team invitation from B" needs team-invite acceptance, which is a granting site (lines 169, 234). An unbound legacy account's address is unconfirmed, so the acceptance answers "invite is for a different address" (AC-28) and nothing is written. AC-46 then expects "the binding is refused ... S0's write is kept", which cannot happen. Replace it with a team write that an unconfirmed account can make: create a team, or send an invite. Co-host acceptance is fine, because co-host invitations are stored by user id (`handlers_scheduling_links.go:382`).
2. **Round 2's Attack A has only a sequential AC.** AC-14 (c) runs the connect completion after the advance. Line 309's write-time check covers the interleaved order: the completion's generation check passes, the binding commits, then the connect stores. No AC exercises that order. Add the Google and Microsoft connect completion to AC-47's interleaved cases.
3. **"Changed from its default" needs a reference set.** `settings` rows are created on demand from column defaults (`storage/settings.go:271`, `storage/credentials.go:132`), but some code writes non-default values on its own. For example, `SaveMicrosoftToken` sets `calendar_provider = 'outlook'` (`auth/microsoft_oauth.go:90`), although that row also has a credential. The contract should name the default for each preference. Otherwise an automatic write can make every legacy account non-empty. That is lock-out, not exposure.
4. **AC-14 (d) needs the request model stated.** The test can only show that "another site" starts nothing if the contract says what a cross-site request may carry: a top-level GET, or a form POST with the `SameSite=Lax` session cookie, and nothing the app itself issued. Today's GET start (`routes.go:33`, `:35`) fails that model, so the AC can be falsified once the model is written down.
5. **Operator reassign and the old calendar credential.** Step 3 (line 377) removes credentials only when the verified person disowns them. If GA's own calendar credential stays on A and Henry's next sign-in is at another provider, Paceday keeps writing Henry's meetings into GA's calendar. Consider making "remove every calendar credential not issued to the new identity" the default in the procedure. Either way it is operator documentation, not product behaviour.

### Criteria I could not falsify

None for a security property. AC-46/AC-47's team-invite case is unsatisfiable rather than unfalsifiable (Non-blocking 1). AC-23 stays conditional on OQ-4, as before.

### Citations I checked and found wrong

None. Checked and correct at `ae5c2cf` / `ec76099`:
- migrations `003:2-4`, `005:2`, `006:3-17`, `007:8`, `009:1-44`, `010`, `011`, `013:1-26`, `014:1-25`, `015:4-19`, `016:1-46`, `022:1-9`, `026:8`, `028:8-13`, `029:16-20`, `030:20-46`;
- `storage/manager.go:21-29`, `storage/settings.go:271`, `storage/credentials.go:132`, `auth/microsoft_oauth.go:87,90`, `storage/orgs.go:42-58`;
- `engine/team_availability.go:78-84`, `handlers_booking.go:80-88`, `handlers_teams.go:300-305`;
- `PAC-22.md:682` (AC-27), `PAC-68.md:95` (AC-9), `:157` (D-4).

Routes: the Slack, Notion and Zoom callbacks sit behind `requireAuth` (`routes.go:38-39`, `:183-190`). So the generation check reaches them, as AC-46 and AC-47 assume.

### Consumers this breaks

No new consumers beyond those revision 3 lists. If Blocking 1 is resolved by option (i), the guard reaches every handler that loads a credential after authentication: `handlers_calendar.go`, `handlers_schedule.go`, `handlers_nlp.go`, `handlers_events.go`, `handlers_focus.go` and `handlers_personal.go`. The plan should name them.

## Revision 4 — 2026-10-28

Answers "Challenge, revision 3" only. Nothing the challenge did not raise is changed; §10 is unchanged and no follow-up is added or widened. The challenge text and the earlier revision records above are kept unchanged. As in revision 3, mechanisms are stated in §3 as behaviour, and the citations are in §2.

| # | Finding | Resolution |
|---|---|---|
| B1 | An old-generation request authenticated just before a rule (b) binding can still read or act through the credential the binding stores | **Resolved, option (i)** (orchestrator decision, factory-level, independent of OQ-6). §3 "Accounts created before this change" adds the rule: a request of an older-generation session never obtains, uses or returns any credential, connection, key or account data stored after the generation advanced, provider calls included (calendar reads, event and meeting creation). Behaviour: every load of a credential or account data for a request re-confirms the request's generation against the account's current one in the same read; on a mismatch it returns nothing, makes no provider call and ends as 401. D-20 revised to match. §2 cites the authenticate-then-load pattern (`handlers_calendar.go:137-144,162,168`; `handlers_schedule.go:206` → `engine/smart_schedule.go:288-289`). New AC-50 (interleaving seam: the binding commits between authentication and credential load), with (a) a calendar-read case and (b) a meeting-create case. Both assert that no provider call is made with Henry's credential and no data is returned. §5 "Pre-deploy sessions" residual updated for accounts that do bind. The plan should name the handlers the challenger listed (`handlers_calendar.go`, `handlers_schedule.go`, `handlers_nlp.go`, `handlers_events.go`, `handlers_focus.go`, `handlers_personal.go`). |
| N1 | AC-46/AC-47's first case (team-invitation acceptance) cannot happen for an unconfirmed address | **Resolved.** The case is now "S0 creates a team", a write that an unbound, unconfirmed account can make and that rule (b) counts. AC-47 reuses AC-46's cases, so both are fixed. |
| N2 | Connect-across-binding has only a sequential AC | **Resolved.** AC-47 gains interleaved connect-completion cases at Google and at Microsoft: the completion's generation check passes before the binding is stored, and its credential store is attempted after it. Nothing is stored, Henry's credential is unchanged, and the outcome is "credited to nobody". |
| N3 | "Changed from its default" needs a reference set | **Resolved, per preference.** The code writes some settings by itself, and nothing records who wrote a value (§2 cites `auth/microsoft_oauth.go:87,90`; `storage/credentials.go:132,148,179-182,184-194`; `storage/settings.go:93-108,271,327,543-556`; `005_conferencing.up.sql:1`; `020_schedule_preferences.up.sql:3-4`). So "the user changed it through an endpoint" is not decidable. §3 rule (b) instead compares each preference with its default (the new-account value), on meaning, in the code's normal form. The contract names each default and normal form, and an unnamed preference counts as changed. Values the code wrote that differ from the default (for example a work calendar left on Outlook) count as changed: lock-out only. Bookkeeping fields are not compared. |
| N4 | AC-14 (d) needs the cross-site request model written down | **Resolved.** §3 "Connecting a calendar" states the model. Another site can cause top-level navigation, GET or POST form submission and subresource loads, with any address and fields. Each request is assumed to carry the session cookie whatever the browser's same-site rules do (fail closed). It carries nothing else the app issued, reads no responses or cookies, sets no custom headers and runs no script on Paceday's origin. AC-14 (d) now cites the model, with one case per request kind. Today's GET starts fail it. |
| N5 | Operator "unbind and reassign" leaves the previous identity's calendar credential | **Resolved.** Step 3 now removes every stored calendar credential (Google, Microsoft, personal calendars) by default, unless the operator has confirmed it was issued to the identity being bound; the person reconnects. Other credentials, connections and keys are still removed when the person disowns them. This is operator documentation; AC-49 is unchanged. |

**Acceptance-criterion ledger (revision 4).**
- **Revised:** AC-14 ((d) cites the cross-site model, one case per request kind), AC-46 (first case is now "creates a team"), AC-47 (same case fix; interleaved Google and Microsoft connect-completion cases added).
- **New:** AC-50 (old-generation reads and provider calls after the binding, interleaved: calendar read and meeting create).
- **Conventions:** the interleaving-seam list now includes AC-50.
- **Unchanged:** every other criterion.

**Questions left for the owner.** Unchanged from revision 3 (OQ-6, OQ-8, OQ-1, OQ-2, OQ-3 item 4). Blocking 1 was resolved without depending on OQ-6.

## Challenge, revision 4 — 2026-10-28

Confirming re-check of revision 4 only (`git diff 86835b8 2abdc49 -- docs/specs/PAC-71.md`). Citations checked against `origin/main` `ae5c2cf`.

**Verdict**: accept. The spec is ready for the contract stage.

### Blocking

None. Revision 3's Blocking 1 is closed:
- §3 "An older-generation request never reaches what a binding stored" covers obtaining, using and returning a credential, and provider calls made with it. The generation is re-confirmed **in the same read** as the load, so no binding can commit between the comparison and the load. That closes the window between `requireAuth` and the credential load that revision 3 found.
- A load made before the binding finds an empty account, because rule (b) binds only empty accounts. A load made after it is refused. No ordering is left in which an old-generation request reads Henry's credential or calls the provider with it.
- There are no per-user API or MCP tokens (§2 line 124, `mcp/main.go:29`), so the session generation still covers every way in.
- The revision opens no new path to takeover, data exposure, or a credential on the wrong account. Each change is a refusal (in-read check; per-preference "changed" with an unnamed preference counting as changed) or a removal (operator step 3 now removes calendar credentials by default).

### Revision 4 resolutions checked

| # | Status |
|---|---|
| B1 | Closed (see above). D-20, §5 "Pre-deploy sessions" and the seam list agree with the new rule. |
| N1 | Closed. Creating a team is not a granting site, so an unconfirmed account can do it (§3 line 247 lists the granting sites). Rule (b) counts it (line 309). |
| N2 | Closed. AC-47 has interleaved connect-completion cases at Google and at Microsoft, with byte-for-byte and "credited to nobody" outcomes. |
| N3 | Closed. Each preference is compared with its own default in the normal form, and an unnamed preference counts as changed. This is decidable from stored data, which "changed through an endpoint" was not. |
| N4 | Closed. The cross-site model is explicit and fails closed: the cookie is always assumed present. AC-14 (d) has one case per request kind (navigation, GET form, POST form, subresource). Each fails today, because the starts are plain GETs. |
| N5 | Closed. Calendar credentials are removed by default unless the operator confirms them. Operator documentation only, as stated. |

### AC-50 is falsifiable offline

AC-50 is a unit test with a fake provider that records each call and the credential it carries. The seam commits the binding between the request's authentication and its credential load.
- **Today** the test fails. No generation exists, so `getCalendarClient` (`handlers_calendar.go:137-144`) loads Henry's token and `ListEvents` (`:168`) calls the fake with it. Case (b) behaves the same way through `CreateMeeting` → `calClient` (`smart_schedule.go:288-289`).
- **A check at authentication only** passes the two sequential orders but fails the interleaved cases. So the criterion separates the required in-read check from a weaker one.

### Non-blocking

1. **AC-50 tests only the Google calendar credential.** Rule (b) also binds on Microsoft tenant evidence. That binding stores a Microsoft credential, which old-generation requests could use through the Outlook paths. The §3 rule covers it, but no criterion would catch a plan that guards only `LoadCalendarToken`. The contract should list every load site the rule covers: the six handlers from the revision 3 challenge, plus the Microsoft and personal-calendar loads. It should also give AC-50 a case bound by Microsoft. "Account data" is unbounded as written, so the contract must name exactly which loads carry the in-read check. At minimum: every credential, connection and key column, and every field rule (b) counts.
2. **Citation nit.** `storage/credentials.go:192` is cited for "teams becomes meet". The `CASE` expression is at `:193`, in the statement at `:191-194`. The substance is correct.
3. **§2 normal form is incomplete.** `normalizeSchedules` runs on every read (`storage/settings.go:304`) and on copies (`:116`, `:138`), not only on save (`:327`). This supports the "compare in normal form" rule and needs no spec change. The contract's normal form should be the one `normalizeSchedules` produces.

### Criteria I could not falsify

None among the revised and new criteria (AC-14, AC-46, AC-47, AC-50).

### Citations I checked and found wrong

- `storage/credentials.go:192`: one line off (see Non-blocking 2).

All other revision 4 citations match `ae5c2cf`:
- `auth/microsoft_oauth.go:87,90`
- `storage/credentials.go:132,148,179-182,184-194`
- `storage/settings.go:93-108,271,327,543-556`
- `005_conferencing.up.sql:1`, `020_schedule_preferences.up.sql:3-4`
- `api/handlers_calendar.go:137-144,162,168`, `engine/cal_iface.go:32`
- `api/handlers_schedule.go:206`, `engine/smart_schedule.go:288-289`
- `api/handlers_events.go:27-29`, `api/handlers_nlp.go:63`

### Consumers this breaks

None beyond those revision 3 lists. The in-read generation check reaches every handler that loads a credential after authentication. The plan must name them (Non-blocking 1).
