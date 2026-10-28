# Contract — PAC-71: bind accounts to the provider identity, not the email

- **Spec**: `docs/specs/PAC-71.md`, revision 4, accepted (challenge revision 4, verdict accept, 0 blocking, `e0c0ebd`).
- **Contract revision**: 2 (answers challenge revision 1; see "Revision 2" at the end). **Status**: draft, awaiting `contract-challenger`.
- **Manifest**: `contracts/features/PAC-71.yaml`.
- **Fragments edited**: `contracts/openapi/paths/auth.yaml` (most of it), `booking.yaml`, `teams.yaml`, `habits.yaml`, `calendar.yaml`, `scheduling.yaml`. The bundle, `backend/api/gen/paceday.gen.go` and `contracts/openapi/MIGRATION.md` are regenerated.
- **Verified against**: `clockwise-like` `origin/main` `ae5c2cf` (merged into this branch; no change), `smart-calendar-flow` `origin/main` `eea0cf6` (read with `git show` only). No provider endpoint was called (§3 says what that leaves open).

This document says **why** the interface has this shape. The fragments say **what** it is. Where the two disagree, the fragments win and this document is wrong.

## 1. What changes at the HTTP boundary

| Operation | Change |
|---|---|
| `googleOAuthCallback` `GET /api/auth/callback` | Two branches, chosen only by which state cookie matches `state`. **Sign-in**: account chosen by identity (`google` + userinfo `id`), evidence from `verified_email`/`hd`, migration rules (a)/(b), new fixed refusal redirect `<FRONTEND_URL>/?error=signin_refused`. **Connect**: credited `/auth/callback?provider=google&connect=credited`, credited to nobody `/?connect=not_credited`, including when the provider returns no `code` (the user cancelled). `code` becomes optional and the provider's `error` parameter is documented (revision 2). |
| `microsoftOAuthCallback` `GET /api/auth/microsoft/callback` | Same two branches. Sign-in adds Graph `/organization` (tenant, classification, evidence); tenant unobtainable or account not classifiable as work is the refusal. Connect reads neither `/me` nor `/organization` and never changes the work calendar. |
| `ssoOidcCallback` `GET /api/auth/callback/oidc/{domain}` | Identity = verified `iss` + `sub`. A carried address is always refused (SSO never reaches or binds an existing account). Same refusal redirect. No connect branch. |
| `startGoogleConnect` `POST /api/auth/google/connect` | **New.** Protected. 200 `ConnectStartResponse {authorize_url}` + `google_connect_state` cookie. Never redirects. |
| `startMicrosoftConnect` `POST /api/auth/microsoft/connect` | **New.** Same, `ms_connect_state`; 503 when Microsoft is not configured. |
| `startGoogleOAuth`, `startMicrosoftOAuth` | Description only: sign-in starts only; never a connect. |
| Every protected operation (114) | Session `gen` claim; stale generation is the existing 401; generation unreadable is a 500 `{"error":"internal error"}`. 31 operations that declared no 401 and 15 that declared no 500 now declare them. |
| `getAuthStatus` | Its "200 on internal failure" fallback no longer covers a stale generation (401) or an unreadable generation (500). |
| `getCurrentUser` | `CurrentUserResponse.addressStanding` (`domain_confirmed` / `unconfirmed`), required. |
| `ssoAdminStepupCallback` | New check 4b and reason `identity_mismatch`, last in the order. |
| `createSsoProvider` (and the shared break-glass rule of `deleteSsoProvider`, `setSsoProviderState`), `SsoWriteFreshLoginRequired` | Break-glass needs the session's **account** to hold the listed address domain-confirmed; the email claim is no longer compared. |
| `patchOrganizationAI` (added at the backend implementation) | Break-glass follows the same rule as `createSsoProvider`: the session's account holds the listed address domain-confirmed; the email claim is no longer compared. Description only. |
| `acceptTeamInvite`, `createSchedulingLink`, `inviteSchedulingLinkHost`, `updateSchedulingLink`, `addManagerTeamMember`, `removeManagerTeamMember`, `previewManagerDetection`, `patchManagerTeamMember`, `scheduleManagerOneOnOne`, `getManagerRoster` | Address-as-person sites: granting sites resolve only a domain-confirmed address, case-insensitively; revoking and keeping sites act on every carrier. No status or shape changes. |
| `getTeam` | 401 added; its 404 `x-uncertain` resolved (§10). |
| `createConferenceLink`, `addEventConference` | Revision 2: the two Teams 400 texts no longer send the user to the sign-in start. They become "teams: not connected — connect Outlook in Settings (POST /api/auth/microsoft/connect)" and "teams: connection lost — reconnect Outlook in Settings (POST /api/auth/microsoft/connect)". The status codes are unchanged; the Zoom texts are unchanged. |

No operation writes, replaces or removes a binding. The operator steps are a server-side command, not HTTP (§9).

## 2. The refusal is one fixed redirect to the web app

**Shape.** Every refusal on all three callbacks is `302 Location: <FRONTEND_URL>/?error=signin_refused`. No `auth_token` cookie is set or expired. The Location pattern is pinned in each callback.

**Why a redirect, and why this exact value.** The callbacks are top-level navigations back from the provider. The web app can only show the spec's message if the browser lands on a page of the web app. PAC-35 already established `/?error=auth_failed` as the landing for "sign-in did not complete" (`Index.tsx:568`, `AuthCallback.tsx:8`, `SignInFailedNotice.tsx`). The refusal uses the same landing with a second fixed value, so the web change is one more branch on a parameter it already reads.

**Rejected alternatives.**
- *A reason code per cause* (`?error=signin_refused&reason=bound_elsewhere`, and so on). Spec D-6 forbids it: a foreign-tenant administrator can set any address, so a distinct answer per cause lets anyone enumerate which addresses have accounts and how they are bound. The cause goes to the server log only.
- *Reusing `auth_failed`.* AC-19 requires the refusal to be distinct from the generic failure. The two messages also tell the user different things: "try again" for a failure, "sign in the way you did before, or ask your administrator" for a refusal.
- *A 403 JSON page on the API origin.* PAC-35 (API-011, API-012) moved exactly this kind of answer off the API origin, because the user is left on a raw JSON page.

## 3. Provider facts (spec OQ-3): what the contract relies on and how sure it is

No call was made at this stage. Each item says whether it is from documentation, from memory of documentation, or open.

| OQ-3 item | Contract choice | Basis | Open? |
|---|---|---|---|
| 1. Microsoft tenant | Graph `GET /v1.0/organization?$select=id,verifiedDomains` with the sign-in access token. Work account ⇔ 200 with exactly one entry, GUID `id`, and GUID `/me` `id`. Tenant = entry `id`, object id = `/me` `id`. | Graph documents `User.Read` as the least-privileged delegated permission for listing the organization (from documentation, not called). The PAC-50 step-up already reads the same resource (`stepup.go:290`). | The plan records one call under exactly today's scopes with a work account in a development tenant before stage 4 merges (merge condition). |
| 1. Alternatives | **Rejected** (i) the `tid` claim of the Graph access token: Microsoft documents access tokens as opaque to the client and their format may change or be encrypted, so a contract cannot pin behaviour to it. **Rejected** (ii) `client_info` (`uid`, `utid`) from the v2 token endpoint: it is what MSAL requests internally, and is not documented in the protocol reference for confidential web clients. | — | Not adopted, so PAC-68's verified-domain lookup and the tenant come from the same call. A failure of that call refuses Microsoft sign-in (spec D-23, owner question OQ-8, factory default: accept). |
| 2. Personal-account classification | **No positive personal signal is adopted.** Anything not classified as work by item 1 is refused. Graph refusing `/organization` for a personal account is a refusal, not a classification. | Fail closed (spec AC-5, AC-11b). | Follow-up **PAC-77** (https://linear.app/paceday/issue/PAC-77) is due. Under this contract a personal Microsoft account cannot sign in or sign up, and an **existing** account whose only sign-in is a personal Microsoft account is **locked out** until PAC-77 ships (Google cannot rescue it: rule (a) needs a Google-format identifier, rule (b) AUTHORITATIVE evidence). Recovery meanwhile is the operator's "unbind and reassign" (§9), or another sign-in method once F1 or PAC-77 provides one. A personal account can still be **connected** as a calendar (the connect reads neither `/me` nor `/organization`). Whether this may ship before PAC-77 is an **open owner question** (default: acceptable, release-noted; §12). |
| 3. Personal-account address evidence | Moot under item 2: none. | — | With PAC-77. |
| 4. Unmanaged ("viral") directories | Microsoft tenant evidence is treated as authoritative (spec default). | Not established. Microsoft has restricted creating unmanaged directories, but this contract does not rely on that. | **Open.** Residual release-noted (spec §5 "Provider evidence"). Merge condition 2 asks the plan to look (documentation or the recorded call). If it shows that such a directory can list a domain in `verifiedDomains` and cannot be told apart, it becomes the owner question the spec describes. |
| 5. Google | v2 userinfo under `userinfo.email` returns `id`, `email`, `verified_email` and, for Workspace accounts, `hd`; `id` is the stable subject and equals the OpenID `sub` the step-up sees. | From memory of Google's documentation, not called. | The plan records one call with a Workspace test account. If `hd` is absent under `userinfo.email` alone, Workspace users get MAILBOX evidence. That is the safe direction: new accounts are created unconfirmed, and rule (b) refuses. It is release-noted, not a takeover. **`id` = step-up `sub` is a hard merge condition** (§8): for the same Workspace account the plan records both values and asserts equality. |
| 6. Identifier formats for rule (a) | Google `^[0-9]{1,255}$`. Microsoft work object id: a GUID, compared case-insensitively. The two are disjoint, so a recorded value can never match both. An SSO subject never qualifies under (a). | Graph object ids are GUIDs. Google account ids are decimal strings (from documentation). | — |
| 7. Microsoft object id across flows (added in revision 2) | The bound object id is Graph `/me` `id` (sign-in step 2). Step-up check 4b compares it with the step-up ID token's `oid`. Both are documented as the directory object id of the user. | From documentation, not called. | **Hard merge condition** (§8): for the same work account the plan records `/me` `id` and the step-up `oid` and asserts equality. |

## 4. Connect: a POST that answers JSON, a state bound to (user, generation, browser), and the existing callback URLs

**Start.** `POST /api/auth/{google|microsoft}/connect`, behind `requireAuth`, no body. It answers 200 `{"authorize_url": ...}` and sets `google_connect_state` / `ms_connect_state` (`HttpOnly`, `SameSite=Lax`, 300 s). The web app navigates the window to the URL.

**Why this defeats every request in the spec's cross-site model** (§3 "Cross-site request model", AC-14 (d)). Take each request another site can make, each carrying the victim's cookie:
- *Top-level navigation, GET form, subresource*: these are GETs. They are not this operation, and no GET starts a connect.
- *POST form*: it reaches the operation. The browser receives the JSON and the state cookie. The attacking page cannot read the response, so it never learns `authorize_url` or the state. Nothing follows the URL, so no provider step ever completes for that state.
- *A replay of the attacker's own connect into the victim's browser* (login-CSRF in reverse): the attacker would need the victim's browser to hold the attacker's state cookie, and cannot set it.

So nothing a cross-site request can start can later store a credential. That holds without relying on `SameSite`, because the model assumes the cookie is always sent.

**The invariant this rests on** (challenge r1 Non-blocking 3). The POST-form case is safe only because the attacking page cannot read the response. That holds while the API never answers a cross-origin request with `Access-Control-Allow-Credentials: true` for an origin other than the configured web origin. Today `corsMiddleware` (`backend/api/middleware.go:79-99`) sends `*` without credentials, or exactly `FRONTEND_URL` with credentials. A change that reflects an arbitrary origin with credentials breaks the argument and must revise this contract. The fragment states the same invariant on `startGoogleConnect` (and `startMicrosoftConnect` by reference).

**Out of the model: sibling-subdomain cookie tossing** (challenge r1 Non-blocking 4, not applied). A host that can set cookies on a parent domain can plant its own `auth_token` and connect state, so the victim's consent credits the attacker's account. The spec's cross-site model does not include a hostile sibling host, so the contract does not claim to defeat it. Renaming only the two connect cookies to `__Host-` would not close it, because a tossed `auth_token` alone makes the victim start a connect as the attacker. Closing it needs `__Host-` on `auth_token` too, which changes every consumer of that cookie (web, e2e, `cookies.go`). It is recorded as a follow-up candidate (manifest `followUps.cookieHostPrefix`).

**The callback recognises a connect by the state alone** (spec D-24, PAC-47 D-18). The connect state cookie must equal `state`. The state must be one the server issued, for that provider, under 300 s old, naming user U and generation g. The session must be U's, at `gen` g, and U's current generation must be g. The store re-checks g in its own transaction (AC-47). Any failed check before the store means "credited to nobody". A session cookie never selects the connect branch.

**Rejected alternatives.**
- *Keep the GET start and redirect* (today). AC-14 (d) fails: a cross-site navigation starts the flow, and a provider that skips its consent screen completes it.
- *Separate connect callback URLs* (`/api/auth/google/connect/callback`). This would be cleaner to read and to generate. But every deployment would have to register a new redirect URI with Google and with Microsoft, and add two environment variables. A deployment that forgets would break connect, and nothing in this repo can check it. The existing callbacks already distinguish flows by state (PAC-50's step-up does the same with its own cookie), so the contract keeps one URL per provider and documents two branches.
- *A CSRF token header on the start.* It adds nothing once the start answers JSON instead of redirecting. It would also need the CORS allow-list widened.
- *Server-stored single-use state vs. a server-keyed MAC.* The contract pins only the observable properties: opaque, bound to (user, `gen`, provider, 300 s), checked by the server, tied to the browser by the cookie. The plan picks the mechanism. PAC-50's `sso_stepup_state` is a precedent for either.

**The user cancels at the provider** (challenge r1 Non-blocking 1). The provider then returns `?error=access_denied&state=S` with no `code`. On the connect branch this is NOT CREDITED: a 302 to `<FRONTEND_URL>/?connect=not_credited`, so a signed-in user lands back in the web app. It is never the 500 JSON page on the API origin (PAC-35, API-011/012). The check runs after the state and session checks (step 4), so it reveals nothing more than they do. `code` therefore becomes an optional query parameter, and the provider's `error` is documented and never echoed. In the generated Go, `GoogleOAuthCallbackParams.Code` and `MicrosoftOAuthCallbackParams.Code` become `*string` and gain `Error *string`. No handwritten code uses these structs: the callbacks stay handwritten. The sign-in branch keeps today's 500 for a missing code. Changing it is outside the spec, and the sign-in dialog is not a signed-in flow.

**The connect state is used at most once.** Every connect-branch response, the 500 included, expires `google_connect_state` / `ms_connect_state` (challenge r1 Non-blocking 7).

**What a connect stores.** Only the provider credential, on U. Storing ends a recorded loss (PAC-67), and a Microsoft response without a refresh token keeps the stored one. Nothing else changes: no binding, account, session, organisation, name, picture, address, standing, recorded identifier, work calendar or generation. In particular the Microsoft connect does **not** set `calendar_provider = 'outlook'`, unlike today's Microsoft sign-in (spec D-15, AC-12, AC-31).

## 5. Session generation, the 401/500 split, and exactly what the in-request re-check covers

**Claim.** `gen`, a non-negative integer. A token without it reads as 0. Every account starts at 0. It advances by one per binding write, per operator step, and per run of the operator's "advance every account" command (§9).

**Where it is documented.** In the `bearerAuth` / `cookieAuth` scheme descriptions, which govern every protected operation. In the shared `Unauthorized`, `BookingUnauthorized` and `MiddlewareUnauthorized` descriptions. In `SessionGenerationUnavailable`, a new shared 500 response. Every one of the 114 protected operations now declares a 401 and a 500. For the 31 and 15 that lacked them, the gap predates PAC-71, but the spec's AC-22 and AC-48 need them declared. A stale generation gives exactly today's 401 bytes. An unreadable generation gives a JSON 500 and never touches the cookie. Treating it as a 401 would make the web app sign the user out during a database outage (AC-48).

**ACCOUNT DATA, the closed list that the in-request re-check covers** (challenge r4 Non-blocking 1). This is everything spec §2 "What an account can carry today" lists, except the `users` row itself:

| # | Kind | Tables / columns |
|---|---|---|
| D1 | Google calendar credential | `oauth_tokens` (user's rows) |
| D2 | Microsoft and Zoom credentials | `settings.microsoft_tokens`, `settings.zoom_tokens` |
| D3 | AI keys and destination | `settings.llm_api_key`, `llm_api_key_{openai,anthropic,azure_openai,bedrock,vertex}`, `llm_base_url`, `llm_provider`, `llm_model`, and the provider columns of §6 |
| D4 | Settings pointing outside Paceday | `settings.webcal_url`, `calendar_email`, `recap_channel_id`, `recap_send_to` |
| D5 | Every other setting | every other `settings` column except `id`, `user_id`, `updated_at`, `auto_schedule_last_run` |
| D6 | Slack and Notion | `workspace_connections` |
| D7 | Personal calendars | `personal_calendars`, `personal_blockers` |
| D8 | Recorded credential loss | `credential_losses` |
| D9 | Teams | `teams` (created_by), `team_members`, `team_invites` (invited_by) |
| D10 | Scheduling links | `scheduling_links` (owner), `scheduling_link_hosts` |
| D11 | Bookings | `bookings` on owned links, `booking_events` |
| D12 | Manager | `user_profiles`, `manager_team_members` (either side), `one_on_one_occurrences`, `team_analytics_cache`, `manager_team_member_assignments` |
| D13 | Stored calendar-derived data | `focus_blocks`, `habits`, `habit_occurrences`, `analytics_weeks`, `meeting_briefs`, `recap_sends`, `audit_log` |
| D14 | Admin standing | `sso_stepup_grants`, `sso_events` |

**Rule.** A read or write of rows of D1–D14 that belong to the session's account, made while serving a request authenticated with a session, re-confirms in the same statement or transaction that the account's current generation equals the token's `gen`. On a mismatch, nothing is returned or stored, no provider call is made, and the answer is the 401. An account row that no longer exists has no current generation, so the re-check also answers the 401 (challenge r1 Non-blocking 6). requireAuth itself keeps today's behaviour for a missing row; only the re-check is new. A migration that adds an account-owned table extends this list. Until the plan classifies such a table, it counts as non-empty for rule (b) (spec §3).

**Credential load sites this covers, at `ae5c2cf`** (Microsoft and personal calendars included, as the challenge asked). Each primitive is listed with its callers on request paths.
- `auth.LoadUserToken` (`auth/token_store.go:61`), Google: `api/handlers_auth.go:143` (getAuthStatus); `api/handlers_conferencing.go:182` (addEventConference); `engine/cal_iface.go:33` → `LoadCalendarToken` (`:32`), reached from `api/handlers_calendar.go:139` (listEvents, freeBusy), `api/handlers_events.go:29` (events), `engine/cal_iface.go:86` `newCalOps` (SmartScheduler: `handlers_schedule.go:206`, `handlers_nlp.go:63`; focus and compression), `engine/analytics.go:212`, `engine/manager.go:191` (previewManagerDetection); `engine/habits.go:250`; `engine/freebusy_service.go:157` (`POST /api/freebusy`, requester's own); `engine/team_availability.go:82` **for the requester's own member row only**.
- `auth.LoadMicrosoftToken` (`auth/microsoft_oauth.go:112`), Microsoft: `api/handlers_auth.go:128` (getAuthStatus outlook); `api/handlers_conferencing.go:60` (createConferenceLink, teams); `:183` (addEventConference, teams); `engine/freebusy_service.go:146` (`POST /api/freebusy`, outlook); `auth/credential_lifecycle.go:222` (`microsoftStore.load`, renewal); `auth/microsoft_oauth.go:79` (read-for-merge inside the store, a write path).
- `storage.LoadZoomCredential` (`storage/credentials.go:108`) through `auth/credential_lifecycle.go:237`: Zoom meeting creation and `disconnectZoom`.
- `storage.GetWorkspaceConnection` (`storage/integrations.go:43`): `api/handlers_daily_recap.go:159`; `api/handlers_integrations.go:98`, `:230`; `engine/meeting_brief_service.go:66`, `:71` (meeting briefs).
- Personal calendars: `storage.ListPersonalCalendarsByUser`, `storage.GetPersonalCalendar` from `api/handlers_personal.go:53`, `:146`, `:199`, and `engine/personal_blocker.go:52`, `:68`, `:76` when the request triggers it.
- `storage.GetOrCreateSettingsByUser` (`storage/settings.go:266`), which carries D2–D5: every caller on a request path in `api/` (handlers_auth, handlers_conferencing, handlers_credentials, handlers_daily_recap, handlers_llm, handlers_meeting_briefs, handlers_settings), in `engine/` (analytics, compression, focus_time, freebusy_service, habits, personal_blocker, smart_schedule), and in `nlp/parser.go`.

**Not covered** (no session account to compare against): other accounts' credentials (team availability for other members, `handlers_booking.go:88` and `engine/booking.go:203` for public pages) and scheduled jobs (`engine/auto_decline.go:77`, `engine/daily_recap.go:77`, the auto-scheduler).

**AC-50 gains two cases at the contract stage** (manifest `acceptanceTests`):
- **(c)** The binding is Microsoft (T1, O3) under rule (b), storing Henry's Microsoft credential. S0, authenticated before and loading after (seam), creates a Teams conference link and asks `POST /api/freebusy` along the outlook path. The fake Graph receives no call with Henry's credential, and both requests get 401.
- **(d)** Henry adds a personal calendar after the binding. S0's personal-calendar list or sync, authenticated before and loading after, gets 401, and the calendar URL is never fetched.

## 6. Rule (b) "empty": every preference's default and normal form

Rule (b) compares each preference with **the value a newly created account holds today** (`settings` row created by `INSERT INTO settings (user_id) VALUES ($1)`, migrations 001–030). Schedules are compared **after `normalizeSchedules`** (`storage/settings.go:93-108`), as `GetOrCreateSettingsByUser` applies it on every read (`:304`) (challenge r4 Non-blocking 3). Values are compared after decoding: JSON as structures, maps without regard to key order. Credentials and keys (D1–D4, D6–D8) must be **absent**, meaning an empty string, NULL or no row.

| Column | Default | Note |
|---|---|---|
| work_start / work_end | `09:00` / `18:00` | |
| timezone | `UTC` | |
| focus_min_block_minutes / focus_max_block_minutes / focus_daily_target_minutes | 25 / 120 / 240 | |
| out_of_hours_meetings_per_week | 0 | |
| auto_decline_outside_working_hours | false | |
| focus_label / focus_color | `Focus Time` / `#4F46E5` | |
| lunch_start / lunch_end / protect_lunch | `12:00` / `13:00` / true | |
| buffer_before_minutes / buffer_after_minutes | 5 / 5 | |
| buffer_enabled / buffer_min_meeting_minutes / buffer_skip_back_to_back | false / 30 / true | |
| compression_enabled | false | |
| auto_schedule_enabled / auto_schedule_cron | false / `0 8 * * *` | |
| working_hours | normal form `{"mode":"all_days","default":{"enabled":true,"start":"09:00","end":"18:00"},"days":{}}` | `normalizeSchedules`: empty mode → `defaultWorkingHours(work_start, work_end)`; all_days with empty disabled default → that default; nil days → `{}` |
| lunch_breaks | normal form `{}` | nil → `{}` |
| time_format | `12h` | |
| calendar_provider | `google` | Outlook left behind by a removed Microsoft credential counts as changed (spec §3) |
| conferencing_provider | `meet` | |
| recap_enabled / recap_send_time | false / `08:00` (read as `HH24:MI`) | |
| recap_include_briefs / _focus / _habits | true / true / true | |
| recap_send_to / recap_channel_id | `dm` / `` | D4: point outside Paceday |
| webcal_url / calendar_email | `` / `` | D4 |
| llm_provider | `organization` | Migration 029's default. A row created before 029 holds `''` or `ollama` and counts as **changed** (fail closed; lock-out only, and production has no such rows) |
| llm_model / llm_base_url / llm_api_key / llm_api_key_* | `` | D3 |
| aws_region / bedrock_model | `us-east-1` / `anthropic.claude-sonnet-4-5-20251001-v1:0` | |
| azure_endpoint / azure_deployment / azure_api_version | `` / `` / `2024-02-01` | |
| gcp_project / gcp_location / vertex_model | `` / `us-central1` / `gemini-2.0-flash-001` | retired columns still compared |
| aws_profile / ollama_base_url / ollama_model | `` / `http://localhost:11434` / `llama3` | retired columns still compared |
| microsoft_tokens / zoom_tokens | NULL or `` | D2 |
| **Not compared** | `id`, `user_id`, `updated_at`, `auto_schedule_last_run` | bookkeeping (spec §3) |

A `settings` column that this table does not name counts as changed (fail closed). The plan's test asserts that the table and `settingsColumns` (`storage/settings.go`) name the same set, so a new column fails that test until it is classified. No settings row at all counts as all defaults.

## 7. Address standing at the boundary

`CurrentUserResponse.addressStanding` lets the web app's Settings say, as spec §3 requires, that an unconfirmed address cannot accept invitations yet. It is required, not optional: the web reads it on every load, and an absent value would mean "unknown". The Go constants are named through `x-enum-varnames`. Without it, oapi-codegen v2.4.1 resolves the `unconfirmed` collision with `ProviderDisconnectResult.revocation` by renaming every existing revocation constant, which breaks `handlers_credentials.go`.

The granting and keeping sites keep their statuses and shapes. Each operation's description now states which side of spec §3 it is on. `storage.AssignManagerTeamMembersMatchingFormalTeam` (`storage/manager.go:155`) has **no caller** at `ae5c2cf`, so no operation exposes it. The plan still brings it under the rule, or deletes it, so that a future caller cannot reintroduce address matching.

**Rejected:** a `GET /api/auth/identity` listing the bound identity. Listing sign-in methods belongs to follow-up F1, and `provider` keeps its "first sign-in" meaning (spec §6).

## 8. Step-up and break-glass

- **Check 4b depends on two provider facts, and their verification is a hard merge condition.** The bound Google subject is the v2 userinfo `id`. Check 4b compares it with the step-up ID token's `sub`. The bound Microsoft object id is Graph `/me` `id`, and 4b compares it with the step-up token's `oid`. Neither equality has been observed (§3 items 5 and 7). Until it is, check 4b fails closed: if the values differed, every Google (or Microsoft) step-up by a bound administrator would end in `302 <FRONTEND_URL>/settings?stepup=denied&reason=identity_mismatch`. No bound administrator could then change SSO configuration except through break-glass. So merge condition 2 makes the plan record, for the same Workspace account, the step-up `sub` and the userinfo `id`, and for the same work account the step-up `oid` and `/me` `id`, and assert each equality. A mismatch revises this contract before stage 4. It is not something the implementation may work around.
- Step-up check 4b (`identity_mismatch`) runs **after** every existing check, so every step-up that fails today keeps today's reason (spec §6, `pac50_sso_stepup_test.go`). The web already renders an unknown reason gracefully (`SsoAdminStepupSection.test.tsx:151`). The paired web PR gives `identity_mismatch` its wording.
- Break-glass compares the **account's** stored address and standing, not the token's `email` claim (AC-38). Its 403s are unchanged in shape.

## 9. Operator steps are a server-side command, not HTTP

Freeing an address, "unbind and reassign", and the one-time "advance every account's session generation" are run by the deployment's operator on the server (spec §3 "Operator procedure", D-25, AC-49). **No OpenAPI operation exists for any of them, and none may be added.** AC-49's contract half is falsifiable on the bundle: no operation writes or replaces a binding. The plan names the command, for example a `backend/cmd/...` binary with three subcommands, and where its audit records live so that a down migration keeps them (spec §7).

**OQ-6 needs no contract decision.** The "advance every account" command exists either way. Whether a release runs it is the owner's call at stage 7. AC-44 is conditional on that answer.

## 10. Inherited findings and uncertainties (factory §7.1)

- **Resolved:** the `getTeam` 404 `x-uncertain`. The operation is touched for its 401. From `013_teams.up.sql` (`team_members.team_id … ON DELETE CASCADE`) and `handlers_teams.go` `getTeam` (two statements, no transaction), the 404 is reachable exactly when the team is deleted between the membership read and the team read. The marker is replaced by that statement, and the plan owes a seam test.
- **Advanced:** API-092 (auth handlers without Go tests). Every new callback outcome gets a Go assertion (spec; manifest CONTRACT items).
- **Not resolved, with reason** (operations touched only for the session 401/500, or for address resolution unrelated to the finding; fixing them would change behaviour the spec does not ask for, which the contract-author rules forbid): API-049, API-053 (scheduling-link aliases), API-072 (inviteHost's two 404s stay distinguishable only by body; PAC-71 adds "unconfirmed" to the "user not found" case on purpose, so that the answer does not reveal standing), API-079, API-083, API-085, API-086, API-059/087/090 (team sub-resources), API-039 (settings), API-026/084 (Slack and Notion callbacks, not edited). API-001's write half is already closed by PAC-50, and PAC-71 only narrows break-glass. API-066 and API-091 concern parts of `/api/admin/sso` this contract does not edit.

## 11. Consumers

See the manifest's `consumersAffected`. In summary:
- **`clockwise-like` tests that must change**:
  - `pac35_auth_flows_test.go`: the fixtures gain a Google verified flag and a Microsoft `/organization` answer; the assertions are kept (AC-8).
  - `pac35_auth_flows_test.go:756-785` `TestPAC35_CurrentUser_EveryDocumentedStatus`: the strict decode (`DisallowUnknownFields`, `:779`) is kept, the decoded struct gains `AddressStanding`, and a 500 sub-test (generation unreadable) is added, because `getCurrentUser` now documents a 500.
  - `pac21_4xx_bodies_test.go:193` and `pac67_world_test.go:224` (used by `pac67_conferencing_test.go:107`, `:592`): the pinned Teams texts change to the revision-2 wording (§1).
  - `pac67_conferencing_test.go` `TestPAC67_MicrosoftCallbackKeepsRefreshAndFlipsOnlyTheCaller`: becomes a connect or a bound sign-in.
  - Every test that relies on an address resolving to a user created by `createTestUser`. Such users are unbound, so their standing is unconfirmed. This covers team-invite acceptance, co-host invites, manager add and import, and the break-glass tests (`handlers_teams_test.go`, `handlers_scheduling_links_test.go`, `handlers_manager_test.go`, `pac50_sso_gate_test.go`/`pac50_sso_helpers_test.go`, `pac26_wire_keys_test.go`, `pac21_error_bodies_test.go`). The plan adds a domain-confirmed fixture.
  - `pac50_signin_scopes_test.go` must pass **unchanged**.
- **e2e**:
  - `e2e/auth.ts`: its sessions without `gen` keep working at generation 0. A test after a generation advance must mint `gen`.
  - The seed users (`e2e/seed/seed.sql`) are unconfirmed, so the e2e journeys that accept invites or add co-hosts (`team-invite-analytics.spec.ts`, `scheduling-links.spec.ts`, `manager-team.spec.ts`, `sso-stepup.spec.ts` break-glass) need seeded domain-confirmed standing.
- **`smart-calendar-flow`**:
  - The four connect points move to the new POST: `WorkCalendarConnection.tsx:87`, `ConnectionStatus.tsx:18`, `Dashboard.tsx:475`, `Onboarding.tsx:80`. `api.authConnectUrl` (`client.ts:1181`) is replaced.
  - The sign-in dialog's starts are unchanged: `AuthDialog.tsx:79,98,102,108`.
  - `Index.tsx:568` and `AuthCallback.tsx` gain `signin_refused` and the two connect outcomes.
  - Settings shows `addressStanding`.
  - `SsoAdminStepupSection.tsx` gets `identity_mismatch` wording.
  - The generated types are regenerated.
- **Specs**:
  - PAC-47 AC-3i (a)–(c), AC-3j, AC-3k, AC-3l.
  - PAC-68 AC-9, AC-15 and D-4/D-5 (spec §6). The tenant source is now `/organization`, the same call as PAC-68's lookup.
- **`mcp/`**: unaffected. Its HTTP client (`mcp/client.go:68-88`) sends **no** credential to the API, so every protected operation it calls (about 16 in `mcp/tools.go`, not only `getAuthStatus`) is answered as it is today for a request without a session. `MCP_AUTH_TOKEN` (`mcp/main.go:29`) guards MCP's own SSE listener and is never forwarded. Nothing in it reads `gen`, `addressStanding` or the callbacks.

## 12. Decisions: owner vs factory

| Decision | Source |
|---|---|
| Identity = Google `id`; Microsoft (tid, oid) | **Owner** (PAC-68 OQ-1) |
| Sign-in keeps exactly today's scopes | **Owner** (PAC-68 OQ-2; PAC-50 AC-24) |
| No new work-calendar reset; connect never sets `outlook` | **Owner** (PAC-47 vs PAC-67 OQ-13) |
| OQ-1: a connect never adds a sign-in method | Factory guidance for this ticket; **owner open** for F1 (default: only through an explicit step) |
| OQ-2: SSO never reaches an existing member | **Owner open**, factory default "no", applied |
| OQ-8: a `/organization` failure refuses Microsoft sign-in | **Owner open**, factory default "accept", applied |
| OQ-6: sign everyone out at deploy | **Owner, no default**. The contract ships the operator command and leaves its use to the release |
| Personal Microsoft accounts cannot sign in or sign up, and existing ones are locked out, until PAC-77 | **Owner open** (owner-decisions.md, asked 2026-10-28: "acceptable, or must PAC-77 ship with PAC-71?"), default "acceptable, release-noted", applied |
| Tenant source `/organization`; no positive personal-account signal | **Factory-assumed** (§3) |
| Refusal `/?error=signin_refused`; connect outcomes `connect=credited` / `connect=not_credited` | **Factory-assumed** (§2, §4) |
| Connect start = POST with JSON `authorize_url`; same callback URLs, branch by state cookie | **Factory-assumed** (§4) |
| JWT claim `gen`, initial 0; absent = 0 | **Factory-assumed** (§5) |
| Generation unreadable = JSON 500 on every protected operation | Spec D-20 (factory decision), shape factory-assumed |
| ACCOUNT DATA = D1–D14; covered load sites as listed | **Factory-assumed** (§5, challenge r4 NB-1) |
| Preference defaults and normal form (§6), including `llm_provider` before 029 counting as changed | **Factory-assumed** (challenge r4 NB-3) |
| `addressStanding` on `getCurrentUser` | **Factory-assumed** (§7) |
| Step-up `identity_mismatch` checked last | **Factory-assumed** (§8) |
| Operator steps CLI-only | Spec D-25 (factory decision) |

## 13. Spec challenge revision 4, non-blocking items absorbed

1. **Load sites, the Microsoft case, and "account data".** §5 gives the closed list D1–D14, the load sites (Microsoft and personal calendars included), and AC-50 (c) and (d).
2. **Citation `credentials.go:192` → `:193`.** Corrected in `docs/specs/PAC-71.md` §2 (statement at `:191-194`).
3. **Normal form.** §6 uses the output of `normalizeSchedules` (`storage/settings.go:93-108`, applied on every read at `:304`) and names each preference's default.

## 14. Deploy order

1. The migration runs first (bindings, standings, generations; every account at generation 0, so every existing session stays valid).
2. Then the API. Between the API deploy and the web deploy, the shipped web's four connect controls still navigate to the sign-in start (`api.authConnectUrl`, `client.ts:1181`). Under PAC-71 that is a sign-in: it refuses, or reaches the account bound to the provider identity and replaces the session. Keep that window short, and release-note it.
3. Then the web app. Deploying the web first would break connect, because its POST starts would answer 404 until the API exists.
4. The operator runs "advance every account" only if the owner answers OQ-6 with yes, after both deploys.

Rollback reverses this order (manifest `rollback`): the web first, then the API.

## 15. Gates run at this stage

See the manifest `gatesRun` (each command with its exit code), recorded when the branch was pushed.

## Challenge, revision 1 — 2026-10-28

**Verdict**: accept-with-changes. 3 blocking, all in the manifest (`contracts/features/PAC-71.yaml`); no fragment or schema change is required. Reviewed at `9bda6bf`. The bundle hash matches the manifest.

### Blocking

1. **Consumer missing, and the merge conditions forbid fixing it.** `GET /api/auth/me` with a valid session will answer `{"id":"…","email":"pac35-me@example.com","name":"Me","avatarUrl":"https://img.test/me.png","provider":"google","addressStanding":"unconfirmed","createdAt":"…"}`. `addressStanding` is required in `CurrentUserResponse`. `TestPAC35_CurrentUser_EveryDocumentedStatus` (`backend/api/pac35_auth_flows_test.go:756-785`) decodes that body into a six-field struct with `dec.DisallowUnknownFields()` (`:779`), so it fails with `json: unknown field "addressStanding"`. The test is also named "every documented status", and `getCurrentUser` now documents a 500. `consumersAffected` names `pac35_auth_flows_test.go` only at `:142-246, :286`. The merge condition says "PAC-35 and PAC-67 tests amended only as consumersAffected states", so the implementer may not amend `:756-785` as the manifest stands. **Fix:** add a `consumersAffected` entry for `pac35_auth_flows_test.go:756-785`. The struct gains `AddressStanding`; the strict decode is kept; a 500 sub-test is added for an unreadable generation.
2. **The personal-Microsoft question is recorded as a factory assumption, but it is an open owner question. The release note also understates the effect.** Take an existing account created by a personal Microsoft sign-in (outlook.com). After deploy, `GET /api/auth/microsoft/callback?state=S&code=C`: Graph `/organization` refuses the personal account, so the answer is `302 Location: <FRONTEND_URL>/?error=signin_refused`. Google cannot rescue that account either. Rule (a) needs a Google-format `provider_id`, which it does not have. Rule (b) needs AUTHORITATIVE evidence and an empty account. So the account is **locked out**, not merely unable to sign up, until PAC-77 ships or the operator runs "unbind and reassign". `.claude/factory/owner-decisions.md` (2026-10-28, "Still open") lists this as asked of the owner: "acceptable, or must PAC-77 ship with PAC-71? (default: acceptable, release-noted)". The manifest has it only in `assumedByTheFactory`. `followUps.F4` says "orchestrator files it", but it is already filed as PAC-77. **Fix:** (a) add it to `ownerQuestionsOpen`, with its default. (b) Make the release merge condition say that existing accounts whose only sign-in is a personal Microsoft account lose access until PAC-77, that recovery is the operator's "unbind and reassign", and that they can still connect an Outlook calendar. (c) Rename F4 to PAC-77.
3. **A provider fact that check 4b depends on is not a merge condition.** Step-up check 4b compares the step-up ID token's `sub` with the bound Google subject. The bound subject is the v2 userinfo `id` (sign-in step 2). §3 item 5 says the two are equal "from memory of Google's documentation, not called". If they differ, every Google step-up by a bound Workspace admin gets `302 Location: <FRONTEND_URL>/settings?stepup=denied&reason=identity_mismatch`, and no Google admin can write SSO configuration (break-glass aside). Merge condition 2 records a v2 userinfo answer but never compares it with a step-up ID token. **Fix:** extend merge condition 2 so that, for the same Workspace test account, the plan records the step-up ID token's `sub` and asserts that it equals the v2 userinfo `id`. Do the same for Microsoft: the step-up `oid` equals Graph `/me` `id` for a work account. A mismatch revises the contract before stage 4.

### Requests wrongly permitted

None found at the schema level. Attacks tried:

- **Connect CSRF, attacker's state into the victim's browser.** The attacker runs `POST /api/auth/google/connect` with his own session and sends the victim the `authorize_url`. The victim consents with her account, and `GET /api/auth/callback?state=S_A&code=C_V` arrives without a `google_connect_state` cookie and without `oauth_state`. Result: 400 "invalid state", nothing written.
- **Victim's state completed with the attacker's code.** The attacker would need the victim's state value. The POST response cannot be read cross-site. `corsMiddleware` (`backend/api/middleware.go:79-99`) sends `*` without credentials, or one exact origin with credentials. A cross-site form POST does not even carry the `SameSite=Lax` `auth_token`, so it gets a 401 before any state is issued.
- **The victim's callback crediting the attacker, or the reverse.** Connect steps 1 and 2 bind the state to (U, g), and the session in the browser must be U's at g. A browser holding B's session, or none, gets `302 Location: <FRONTEND_URL>/?connect=not_credited`.
- **Cookie-name collision.** `oauth_state`, `ms_oauth_state`, `google_connect_state`, `ms_connect_state` and `sso_stepup_state` (`handlers_auth.go:31`, `handlers_microsoft_auth.go:29`, `handlers_sso_stepup.go:40`) are distinct names holding independent random values, so at most one matches. The step-up has its own callback path, behind requireAuth.
- **`SameSite=Lax` on the return from the provider.** Google and Microsoft return to the callback by a top-level GET (Microsoft uses the default `response_mode=query`, and the code sets none), and Lax cookies are sent on top-level GET navigations. Both the state cookie and `auth_token` arrive.
- **`gen` claim.** A token without `gen` reads as 0, every account starts at 0, and any advance makes it 1, so an old token gets a 401 after the first advance. `"gen":"0"`, `null` or `-1` are invalid tokens (401). A `gen` above the current value is a 401. Nothing survives an advance.

### Responses the client cannot handle

None blocking.

- `addressStanding`: the web's `AuthUser` (`smart-calendar-flow` `src/contexts/AuthContext.tsx`, `eea0cf6`) is a plain interface read by `apiFetch`, with no strict zod, so the extra field is ignored. Nothing in `src/contracts` parses `/me`.
- 401 vs 500: `refresh()` in the same file maps only 401/403 to signed-out. A 5xx keeps the user and sets `sessionStale`, so the generation-unreadable 500 does not sign anyone out (AC-48 holds for the shipped web).
- `identity_mismatch`: `SsoAdminStepupSection.test.tsx:148-152` pins the generic denial for an unknown reason.
- The CREDITED Location `/auth/callback?provider=google&connect=credited` lands on `AuthCallback.tsx`, which calls `refresh()` and navigates to `/app`, as today. Until the web PR, `/?connect=not_credited` and `/?error=signin_refused` are silent on the shipped web.

### Consumers affected

- `clockwise-like/backend/api/pac35_auth_flows_test.go:756-785`: see Blocking 1.
- `clockwise-like/backend/api/pac21_contract_5xx_test.go`: every new 500 is the `ErrorResponse` / `SessionGenerationUnavailable` JSON envelope. Checked by walking the bundle; the Go test itself needs the test DB and was not run here.
- 114 operations newly declaring 401/500: no shipped client or e2e asserts an operation's exact response set. `git grep` in both repos finds the bundle read only by `pac21_contract_5xx_test.go` and the scripts. `e2e/tests/team-invite-analytics.spec.ts:66` pins the keys of a 200 body, not responses.
- `mcp/`: unaffected, but for a different reason than §11 states. `mcp/client.go:68-88` sends **no** credential, and `mcp/tools.go` calls about 16 endpoints, not only `getAuthStatus`. `MCP_AUTH_TOKEN` (`mcp/main.go:29`) guards MCP's own SSE listener and is never forwarded. Non-blocking: correct §11.

### Contract/implementation divergences

None that block. The callbacks are described as future behaviour on purpose (stage 2), and the unchanged parts (state checks, cookie helper, `/common`, scopes) match `handlers_auth.go`, `handlers_microsoft_auth.go` and `cookies.go` at this head.

### Uncertainties not actually resolved

- `getTeam` 404: resolved by reasoning from the schema (`013_teams` cascade) and the two-statement handler. Acceptable because CONTRACT-9 owes the seam test that pins it.
- OQ-3 item 4 (unmanaged Microsoft directories): open and release-noted, but no merge condition asks the plan to look. Non-blocking: add it to merge condition 2 ("documentation or the recorded call").

### Non-blocking

1. **The user cancels at the provider on a connect.** `GET /api/auth/callback?error=access_denied&state=S` (no code) passes connect steps 1 to 3, then the exchange of an empty code is the 500 JSON page on the API origin. Sign-in does the same today, but connect is a new signed-in flow. Consider "provider returned no code" → `/?connect=not_credited`.
2. **Instructions that now point at a sign-in start.** The pinned 400 strings "teams: not connected — visit /api/auth/microsoft to authenticate" and "teams: connection lost — reconnect at /api/auth/microsoft" (`booking.yaml:1100-1110`, `:1349-1350`; `handlers_credentials.go:77`) now point at a sign-in start. A signed-in Google-bound user who follows them gets a Microsoft sign-in: a refusal, or a new account and a replaced session. It never reconnects Teams. File a follow-up, or change the text.
3. **The cross-site argument in §4 depends on CORS.** It relies on CORS never reflecting an arbitrary origin with credentials. State that as an invariant next to the argument.
4. **Sibling-subdomain cookie tossing.** It can plant an attacker's `auth_token` and `google_connect_state`, so the victim's consent credits the attacker's account. This is outside the spec's cross-site model. Consider the `__Host-` prefix when `Secure` is on.
5. **Deploy order.** Between the backend deploy and the web deploy, the shipped connect controls (`api.authConnectUrl`, `client.ts:1181`) start a sign-in. The rollback names a revert order, but nothing names a deploy order. Add one to the release merge condition.
6. **No account row for `sub`.** requireAuth says "no account row for `sub`: request proceeds", but the in-request re-check does not say what it does when the row is absent. Say 401.
7. **The connect-branch 500s** do not say whether `google_connect_state` / `ms_connect_state` is expired.

### Verified clean

- Spec ACs vs `acceptanceTests`: the AC numbers missing from the manifest (AC-24, -26, -27, -30, -33, -34, -36) are each replaced (AC-40 replaces AC-24) or moved to F1/F2 by the spec itself (`docs/specs/PAC-71.md:773`, `:782`). No silent narrowing.
- `MIGRATION.md`: main has 124 operations and this branch 126, with exactly `startGoogleConnect` and `startMicrosoftConnect` added. No row lost; the bundle `operationId` count matches (124 → 126).
- 401/500 coverage, by walking the bundle: main has 112 protected operations, missing 31 × 401 and 15 × 500 (the same 31 and 15 the doc names). HEAD has 114 protected operations, each with a 401 and a JSON 500.
- Generated Go: the diff adds only. No existing identifier is removed or renamed; `x-enum-varnames` avoids the `unconfirmed` collision with `ProviderDisconnectResult`.
- The refusal Location is one fixed value on all three callbacks, pinned by the Location patterns, with no reason or address in it. Whether a refusal happens depends on account existence only where the requester is AUTHORITATIVE or controls the mailbox for that address. Foreign-tenant probing yields NONE, so a refusal whether or not an account exists (D-6 holds).
- `identity_mismatch` is ordered after `not_admin`, so every reason pinned by `pac50_sso_stepup_test.go` is unchanged. The pattern and the "nine values" text agree.
- Break-glass by stored address: the session's `email` claim is no longer compared. An unconfirmed carrier, or a stale claim, is not break-glass; a case collision cannot bind, so it cannot become domain-confirmed.
- `getAuthStatus`: a stale generation (401) and an unreadable one (500) are carved out of the 200 fallback, so the web cannot read "signed in, not connected" from a dead session.

### Checks run (this worktree, `9bda6bf`)

- `py -3 scripts/openapi_assemble.py --check` → rc=0 (OK: bundle is in sync (105 paths))
- `py -3 scripts/openapi_migration_report.py --check` → rc=0 (OK: MIGRATION.md covers all 126 operations (6 generated))
- `bash scripts/openapi_gen_go.sh --check` → rc=0 (OK: backend/api/gen matches the contract)
- `cd backend && go build ./...` → rc=0; `go vet ./...` → rc=0
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` → `d14119d716aaf2d0be71979f81a8cd22c63d692f90b8d9373a01cfda7fde7d18`, equal to the manifest `contractHash`
- `go test ./api -run TestPAC21Every5xx` → could not run: the package's TestMain needs the test Postgres at 127.0.0.1:15432 (connection refused). I replaced it with a Python walk of the bundle (above).

## Revision 2 — 2026-10-28, answering challenge revision 1

Bundle hash at this revision: `5d54c76194180202d11049885b711f49f647a386baded03eae53e57481f4abb9` (manifest `contractHash`).

| Finding | Applied? | Where / why not |
|---|---|---|
| **B1** `TestPAC35_CurrentUser_EveryDocumentedStatus` missing from consumers | Applied | Manifest `consumersAffected` gains `pac35_auth_flows_test.go:756-785`: struct gains `AddressStanding`, strict decode kept, 500 sub-test added. The backend merge condition names it. §11. |
| **B2** personal-Microsoft lock-out is an owner question; release note understated; F4 is PAC-77 | Applied | Moved from `assumedByTheFactory` to `ownerQuestionsOpen`, worded as in owner-decisions.md (default: acceptable, release-noted). The release merge condition says that existing personal-Microsoft-only accounts lose sign-in at deploy and how they recover: the operator's "unbind and reassign", another method once F1/PAC-77 exists, or PAC-77 itself. They can still connect Outlook. F4 renamed PAC-77 (https://linear.app/paceday/issue/PAC-77) in the manifest, this document, `auth.yaml`, and annotated in the spec's follow-up list. §3, §12. |
| **B3** step-up `sub`/`oid` equality not a merge condition | Applied | Merge condition 2 is now HARD and records, for the same Workspace account, the step-up ID token `sub` and the v2 userinfo `id`, asserting equality; and for the same Microsoft work account, `oid` and Graph `/me` `id`. Until verified, check 4b fails closed (`identity_mismatch`), which would lock bound admins out of SSO changes. §3 items 5 and 7 (new), §8, and the 4b text in `auth.yaml`. |
| NB1 provider cancel during connect is a 500 JSON page | Applied | CONNECT step 4: no `code` → 302 `/?connect=not_credited`. `code` is optional and `error` is documented on both callbacks. Sign-in keeps today's 500 (not in the spec). CONTRACT-10. §4. |
| NB2 Teams texts point to the sign-in start | Applied | `booking.yaml`: "connect Outlook in Settings (POST /api/auth/microsoft/connect)" for both Teams texts. Consumers: `pac21_4xx_bodies_test.go:193`, `pac67_world_test.go:224`. CONTRACT-11. §1, §11. |
| NB3 CORS invariant | Applied | Stated in §4 and in the `startGoogleConnect` description (`startMicrosoftConnect` by reference). |
| NB4 sibling-subdomain cookie tossing / `__Host-` | Not applied | Outside the spec's cross-site model. Prefixing only the connect cookies does not close it, because a tossed `auth_token` suffices, and prefixing `auth_token` changes every consumer. Recorded as `followUps.cookieHostPrefix` for the orchestrator. §4. |
| NB5 deploy order | Applied | New §14 and a release merge condition: migration, then API, then web, then (if OQ-6 is yes) "advance every account". The API-to-web window is release-noted; web before API is forbidden. |
| NB6 re-check with no account row | Applied | The re-check answers 401 (`bearerAuth` description, §5). CONTRACT-12. |
| NB7 connect-branch 500 and the state cookie | Applied | Every connect-branch response, the 500 included, expires the connect state cookie (both callbacks, §4). |
| Uncertainty: OQ-3 item 4 has no merge condition | Applied | Merge condition 2 (v) asks for documentation or a recorded answer. §3. |
| Consumers: §11 MCP description wrong | Applied | §11 and the manifest: `mcp/client.go` sends no credential; about 16 endpoints; `MCP_AUTH_TOKEN` guards MCP's own listener. |

Generated-code note: making `code` optional changes `Code string` to `Code *string` in two generated parameter structs and adds `Error *string`. This is the first non-additive change in `backend/api/gen` on this branch. Nothing handwritten references them, and `go build`/`go vet` pass.

## Challenge, revision 2 — 2026-10-28

**Verdict**: accept. **Blocking**: 0. **Ready for implementation (stage 3, plan)**: yes. Merge condition 2 (HARD: the recorded provider answers, `sub` = userinfo `id`, `oid` = `/me` `id`) still applies before stage 4.

Scope: a confirming re-check of revision 2 only (`git diff 9d60fca 4774fbc`, head `4774fbc`). Read against the handlers at this head, `smart-calendar-flow` `origin/main`, `mcp/` and `e2e/`.

### Revision 2 table, checked row by row

| Finding | Status | Evidence |
|---|---|---|
| B1 `TestPAC35_CurrentUser_EveryDocumentedStatus` | **Closed** | Manifest `consumersAffected` names `pac35_auth_flows_test.go:756-785`; `:756` is the test, `:779` is `dec.DisallowUnknownFields()`. The backend merge condition names the amendment (struct gains `AddressStanding`, strict decode kept, 500 sub-test). |
| B2 personal-Microsoft lock-out | **Closed in the contract** | Moved to `ownerQuestionsOpen` with the lock-out of EXISTING accounts stated; the release merge condition names the recovery paths; F4 is PAC-77 everywhere (manifest, §3, `auth.yaml`, spec F4 line). See Non-blocking 1 for the owner-facing text. |
| B3 step-up `sub`/`oid` equality | **Closed** | Merge condition 2 is HARD, items (iii) and (iv) record both values for the same account and assert equality; §3 items 5 and 7, §8 and the 4b text in `auth.yaml` say what fails closed if they differ. |
| NB1 cancel during connect | Applied | Step 4 in both callbacks; checked below (attacks A1-A5). |
| NB2 Teams texts | Applied | Checked below. |
| NB3 CORS invariant | Applied | The description matches `corsMiddleware` (`middleware.go:79-99`): `*` without credentials, or `Access-Control-Allow-Credentials: true` with `Access-Control-Allow-Origin` set only when `Origin == FRONTEND_URL`. |
| NB4 `__Host-` | Not applied, reason accepted | The argument that prefixing only the connect cookies does not close it (a tossed `auth_token` suffices) is correct; `followUps.cookieHostPrefix` records it. |
| NB5 deploy order | Applied | §14 and the release merge condition. See Non-blocking 2 and 3. |
| NB6 re-check with no account row | Applied | `bearerAuth` text and CONTRACT-12. |
| NB7 connect 500 and the state cookie | Applied | Both callbacks' 500 descriptions and step text. |
| OQ-3 item 4 merge condition | Applied | Merge condition 2 (v). |
| §11 MCP description | Applied, verified | `mcp/client.go:68-88` `doJSON` sets only `Content-Type`; no credential is forwarded. |

### Attacks on the delta (optional `code`, documented `error`)

Today's handwritten callbacks are routed directly (`routes.go:34`, `:36`), not through the generated wrapper, so the generated `RequiredParamError` for `code` was never on the request path, and the `Code *string` / `Error *string` change has no runtime effect. `grep -rn OAuthCallbackParams backend` outside `api/gen` finds nothing; no request validator (`openapi3filter`) exists in `backend/` or `e2e/`.

- **A1. Cancel on the connect branch.** `GET /api/auth/callback?state=S&error=access_denied` with `Cookie: auth_token=<U, gen g>; google_connect_state=S`, S issued for (U, g, google) under 300 s. Contract: steps 1-3 pass, step 4 fails, `302 <FRONTEND_URL>/?connect=not_credited`, `google_connect_state` expired, no exchange, no `auth_token` Set-Cookie, `error` not echoed. CONTRACT-10 pins it for both providers. Correct.
- **A2. Cancel on the sign-in branch.** `GET /api/auth/callback?state=S&error=access_denied` with `Cookie: oauth_state=S`. Today: `oauth_state` expired (`handlers_auth.go:48`), then `auth.ExchangeCode(ctx, cfg, "")` (`google_oauth.go:31-33`, a plain `config.Exchange`) fails at the token endpoint, `500 {"error":"internal error"}` (`handlers_auth.go:50-53`). Microsoft: the same at `handlers_microsoft_auth.go:46-57`, after the existing 503 when not configured. The contract says the sign-in branch keeps today's 500 and lists "token exchange failed" under it. Describes the server; unchanged.
- **A3. Forced navigation to clear a victim's pending connect.** The victim has a pending `google_connect_state=S`; the attacker does not know S (HttpOnly cookie; the only other copy is inside `authorize_url` in a JSON body that a foreign origin cannot read under the CORS invariant). The attacker links `GET /api/auth/callback?state=X&error=access_denied` (or no `state`). Neither cookie equals X, so this is the existing 400 `invalid state` (`handlers_auth.go:44-46`), which sets no cookie: the victim's state survives. Clearing it needs S, and a holder of S could already finish or spoil the flow with any `code`. The no-code path adds no capability.
- **A4. Oracle.** Steps 1-3 and step 4 all answer the same `302 /?connect=not_credited`, and step 4 runs after the state and session checks. A request without a code learns nothing that a request with a garbage code (which reaches the exchange and 500s only after steps 1-3 pass) did not already reveal to someone holding a valid S and the victim's session, i.e. the victim.
- **A5. Edge values.** `code=` (present, empty) fails step 4 ("present and non-empty"): NOT CREDITED, no outbound call. `code=C&error=access_denied` passes step 4 and the exchange decides. Both are well defined.

### Teams 400 texts vs pinned tests

Every pin of the old strings is listed: `handlers_conferencing.go:38`, `handlers_credentials.go:77` (sources), `pac21_4xx_bodies_test.go:193`, `pac67_world_test.go:224` (`teamsLost`, used by `pac67_conferencing_test.go`). Whole-repo grep for `visit /api/auth/microsoft` and `reconnect at /api/auth/microsoft` finds nothing else outside `docs/` and earlier contracts (PAC-21, PAC-67 documents, which are history). `mcp/` and `e2e/`: none. `smart-calendar-flow` `origin/main` `src/`: no match on the text. The new instruction works: the connect requests the same Microsoft scopes as sign-in (`microsoft_oauth.go:40`), which is the credential Teams uses.

### Gates run (this head, `4774fbc`)

- `py -3 scripts/openapi_assemble.py --check` → rc=0 (OK: bundle is in sync (105 paths))
- `py -3 scripts/openapi_migration_report.py --check` → rc=0 (OK: MIGRATION.md covers all 126 operations (6 generated))
- `bash scripts/openapi_gen_go.sh --check` → rc=0 (OK: backend/api/gen matches the contract)
- `cd backend && go build ./...` → rc=0; `go vet ./...` → rc=0
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` → `5d54c76194180202d11049885b711f49f647a386baded03eae53e57481f4abb9`, equal to the manifest `contractHash`.

### Non-blocking

1. **The owner-facing question understates B2.** `.claude/factory/owner-decisions.md` (orchestrator's file, outside this repo) asks whether personal Microsoft accounts "cannot sign in or sign up" is acceptable. It does not say that EXISTING personal-Microsoft-only accounts lose access at deploy, nor how they recover. The manifest's `ownerQuestionsOpen` says both. The orchestrator should copy that wording before the owner answers; the release merge condition already depends on the answer.
2. **§14 window wording.** Between the API and the web deploy, the shipped web's connect control starts a sign-in, which can also **create a new account** (an unbound identity with authoritative or mailbox evidence on an address nobody carries) and switch the session to it, not only refuse or reach the bound account. Today's sign-in does the same, so it is not a regression. Add it to the release note.
3. **§14 step 1 assumes the migration is safe under the old API.** For the window between the migration and the API, rows the old `UpsertUser` inserts must get generation 0 and no binding by default, so the new API treats them as unbound legacy accounts. The plan should say so; it is a property of the migration, not of the contract.
4. **Sign-in cancel is still a JSON 500 on the API origin**, after an outbound token request with an empty code (A2). Unchanged by PAC-71 and outside its spec. A follow-up candidate alongside PAC-35 API-011/012, not this ticket. `error_description` / `error_uri`, which the providers also send, are undocumented; harmless with no request validator.

### Verified clean

The optional `code` / `error` delta (A1-A5): no new branch selection, no cookie-clearing by a third party, no oracle, sign-in behaviour unchanged and matching the handler; the generated-type change has no handwritten reference and no runtime path. The Teams text change: every pin listed, no web, MCP or e2e consumer. The CORS invariant text matches `corsMiddleware`. The MCP correction matches `mcp/client.go`. B1-B3 closed. Gates and `contractHash` reproduced.
