# Paceday API audit — consolidated defect register

## 1. What this is

Six agents each reverse-engineered one domain of the Paceday HTTP API from the Go
implementation in `backend/`, wrote an OpenAPI fragment under
`contracts/openapi/paths/`, and cross-checked the fragment against the frontend
consumer in `smart-calendar-flow/src`. Their raw findings live in
`auth.notes.md`, `calendar.notes.md`, `scheduling.notes.md`, `booking.notes.md`,
`teams.notes.md` and `habits.notes.md`. This document consolidates those six
reports into one prioritised register, plus the `x-uncertain` markers carried in
the assembled bundle `contracts/openapi/openapi.yaml`.

**Caveat on method.** Everything here comes from reading source and existing Go
tests. **No request was ever issued against a running server, and no Postgres
instance was consulted.** Wire shapes are therefore derived from struct tags,
handler code and a handful of assertions in `*_test.go` — not from observed
traffic. Several findings turn on how a driver renders a `TIME` column or how
`encoding/json` treats a nil slice; those are marked accordingly.

Every row carries a confidence level:

| Level | Meaning |
|---|---|
| **Confirmed** | Re-read in the Go source during this consolidation pass; the file and behaviour are quoted below or in the finding. |
| **Reported** | Traced by the domain agent to a named source file and consistent with the surrounding reports, but not independently re-read here. |
| **Inferred** | The reporting agent explicitly flagged it as unverified — a DB rendering, a dead branch, or a shape with no consumer and no test. Treat as a hypothesis to test, not a fact. |

Where two reports disagree, or where this pass disagrees with a report, the row
says so in full rather than silently picking a side. Section 7 lists those
corrections.

---

## 2. Systemic themes

Six themes were proposed in the task; all six are supported by the notes and were
re-verified here. Five further themes emerged that were not in the original list.
Nothing proposed had to be dropped.

### T1. Go structs with no `json` tags leak PascalCase field names onto the wire

**Confirmed.** A sweep of `backend/` found 44 exported structs with zero json
tags; most are service objects that are never serialised. Seven are encoded
directly into a response body:

| Struct | Source | Endpoint(s) that emit it |
|---|---|---|
| `storage.FocusBlock` | `storage/models.go:14` | `GET /api/focus/blocks` (`handlers_focus.go:66`) |
| `engine.FocusBlock` | `engine/focus_time.go:17` | `POST /api/focus/run` → `FocusRunResult.createdBlocks` |
| `calendar.TimeSlot` | `calendar/freebusy.go:10` | `GET /api/calendar/freebusy`; `POST /api/freebusy` → `results[].busy[]` |
| `calendar.GenericEvent` | `calendar/provider.go:13` | `GET /api/personal-calendars/{id}/preview` (`handlers_personal.go:177`) |
| `storage.SSOProvider` | `storage/sso_providers.go:12` | `POST /api/admin/sso`, `GET /api/admin/sso` |
| `storage.UserProfile` | `storage/manager.go` | `POST /api/manager/profile` (`handlers_manager.go:87`) |
| `storage.ManagerTeamMember` | `storage/manager.go` | `POST /api/manager/team/members` (`:311`), `PATCH /api/manager/team/members/{email}` (`:401`) |

The sharpest instance: `POST /api/freebusy` emits **both casings in one response
body**. `participants[].busy[]` and `busy{}` are built from the locally-declared
`freeBusyWindowDTO` (`{"start","end"}`), while `results[].busy[]` passes
`engine.ParticipantBusy.Busy []calendar.TimeSlot` straight through
(`{"Start","End"}`). Confirmed at `api/handlers_freebusy.go:90-99` and
`engine/freebusy_service.go:32-36`.

Structs with no tags that are *not* on the wire, and so are **not** defects:
`storage.MeetingBrief` and `storage.WorkspaceConnection` are both wrapped in
tagged DTOs before encoding (`handlers_meeting_briefs.go:23`,
`handlers_integrations.go:102`). The habits report did not claim otherwise; noted
so this theme is not over-applied.

### T2. Two coexisting error encodings, mixed within single files

**Confirmed by count.** `writeError` (`handlers_settings.go:206`) emits
`application/json` `{"error": "..."}`. Go's `http.Error` emits
`text/plain; charset=utf-8`. Across `backend/api` there are 274 `writeError` call
sites and 111 `http.Error` call sites, and **eleven files use both**:

- `writeError` only: `handlers_analytics.go`, `handlers_audit.go`, `handlers_booking.go`, `handlers_events.go`, `handlers_habits.go`, `handlers_personal.go`, `handlers_scheduling_links.go`
- `http.Error` only: `handlers_auth.go`, `handlers_calendar.go`, `handlers_daily_recap.go`, `handlers_integrations.go`, `handlers_me.go`, `handlers_meeting_briefs.go`, `handlers_org.go`, `middleware.go`
- **Both:** `handlers_conferencing.go` (28/3), `handlers_focus.go` (3/3), `handlers_freebusy.go` (6/1), `handlers_llm.go` (2/1), `handlers_manager.go` (17/15), `handlers_microsoft_auth.go` (2/4), `handlers_nlp.go` (3/2), `handlers_schedule.go` (7/4), `handlers_settings.go` (3/3), `handlers_sso.go` (20/6), `handlers_teams.go` (46/17)

The frontend has two fetch wrappers with different tolerance:
`src/api/client.ts::requestApi` attempts `res.json()` on a non-2xx and passes
`undefined` to `ApiHttpError` when that fails — so a `text/plain` 500 reaches the
UI as an error with **no message**. `src/lib/api.ts::apiFetch` is more lenient.
The pattern is worst where it splits inside one handler: `handlers_freebusy.go`
is JSON for 400/401 and text for 500; `handlers_llm.go` is text for a
settings-load 500 and JSON for the far more common "LLM not configured" 500.

**Sub-pattern, worth its own fix:** four sites set `Content-Type: application/json`
and *then* call `http.Error`, which overwrites the header and appends
`X-Content-Type-Options: nosniff`. The body really is JSON; the header says it is
not. `middleware.go:36-52` (the `requireAuth` 401 — reported independently by
five of the six agents), `handlers_me.go` (its 401 and 404), `handlers_org.go:21,33`.

### T3. camelCase vs snake_case drift, worst on Settings

**Confirmed.** The API has no global casing convention. Backend `Settings`
(`storage/settings.go:172` and neighbours) is **camelCase** (`workStart`,
`focusMinBlockMinutes`, `llmApiKey`). Frontend `Settings` in `src/api/types.ts`
is **snake_case** (`work_start`, `focus_min_block_minutes`). `normalizeSettings()`
bridges exactly four keys (`workingHours`, `lunchBreaks`,
`outOfHoursMeetingsPerWeek`, `autoDeclineOutsideWorkingHours`); everything else
is cast through untouched. `settingsRequestBody()` has the mirror problem — it
spreads the snake_case object into the PUT body, which the camelCase decoder
ignores, writing zero values instead. The scheduling agent called this "the
single highest-risk drift in the domain" and that assessment holds.

The same split recurs on `FocusRunResult` (camelCase response vs snake_case
frontend type), `MoveProposal` (camelCase *response*, snake_case *request*, on
adjacent endpoints of the same feature), and `CompressionResult`
(`totalFocusGainMinutes` vs `estimated_focus_gain_minutes`). By contrast the
booking, habits, teams and manager domains are consistently snake_case, and
`POST /api/conference/create` is the lone camelCase response in booking.

### T4. Handlers returning the raw Google `calendar/v3.Event`

**Confirmed.** Three endpoints encode the vendored Google struct instead of the
`CalendarEvent` DTO the frontend types them as:

- `PATCH /api/events/{id}` — `handlers_events.go:87` encodes `updated`, the
  `*googlecalendar.Event` returned by `client.UpdateEvent`.
- `POST /api/schedule/create` — reported in `scheduling.notes.md` §a10.
- `POST /api/nlp/confirm` — same.

The wire keys are `summary`, `start.dateTime`, `hangoutLink`, `attendees[].email`;
the frontend reads `title`, `start`, `end`, `attendees: string[]`. Only `id`
lines up. `TestToCalendarEventDTO_JSONShape` pins the flattened DTO for
`GET /api/calendar/events`, so the list and the mutations disagree about the
same object. No test covers any of the three success bodies.

### T5. Nil slices serialise as `null` instead of `[]`

**Reported, consistent across two domains.** No `omitempty`, no
pre-initialisation. Affected: `GET /api/focus/blocks`,
`POST /api/schedule/compress` (whole body, in week mode), `ScheduleSuggestions.slots`,
`FocusRunResult.createdBlocks`/`skippedDays`/`errors`, `CompressionResult.proposals`,
`CompressionApplyResult.applied`/`failed`, `DailyRecapPreview.blocks`,
`GET /api/teams/{id}` → `members`, `GET /api/teams/{id}/analytics` →
`member_breakdown`, and `AnalyticsWeekRow.top_meeting_titles`.

The inconsistency is the real problem: in the same teams domain, `members`
(manager roster), `gaps`, `candidates`, `slots`, the zones array and the teams
array *are* explicitly normalised to `[]`. Two adjacent endpoints, two
conventions. The spec currently papers over it with `nullable: true`.

### T6. Silent-success writes that never check `RowsAffected`

**Confirmed.** Three storage functions run a bare `UPDATE`/`DELETE ... WHERE` and
return only the driver error:

- `storage.RespondToHostInvite` (`storage/scheduling_links.go:291`) — backs
  `POST /api/scheduling-links/{id}/accept` and `/decline`, and their two
  `/host-invites/{id}/...` aliases. Accepting an invite you never received
  returns `200 {"status":"accepted"}` having changed nothing.
- `storage.RemoveLinkHost` (`:285`) — backs `POST /{id}/leave`; a non-host gets
  `204`.
- `storage.DeleteSSOProvider` (`storage/sso_providers.go:108`) — `DELETE
  /api/admin/sso/{domain}` returns `204` for a domain that has no provider row.

Neither accept/decline endpoint has a `403` or `404` branch at all
(`handlers_scheduling_links.go:669-690`).

### T7. Global singletons behind per-user authentication *(added)*

Not in the original theme list, but it is the root cause of the four most severe
findings and deserves naming. Four pieces of state are global where the auth
model is per-user:

- **`settings`** — `storage.GetSettings` and `SaveSettings` are hard-coded to
  `WHERE id = 1` / `VALUES (1, …)` (`storage/settings.go:227, :269-290`), even
  though the table has a `user_id` column, migration
  `018_per_user_settings_and_calendars` exists, and `GetSettingsByUser`
  (`:420`) is implemented and used elsewhere. Migration 018's own comment says
  the singleton path "doesn't yet supply a user_id", so this is a half-finished
  migration rather than an oversight — which does not make it less severe.
- **`audit_log`** — the table has **no user or org column at all**
  (`INSERT INTO audit_log (action, details, created_at)`,
  `storage/focus_blocks.go:62`), so `GET /api/audit` is not merely unscoped, it
  is *unscopable* without a schema change.
- **the Microsoft OAuth token** — `auth.LoadMicrosoftToken(h.db)` takes no user
  id, and `microsoftCallback` saves the token onto the global settings row.
- **`GET /api/auth/status`** — branches on the global settings row and returns
  the global `CalendarEmail` (`handlers_auth.go:82-107`).

### T8. Raw Go and provider error strings echoed to clients *(added)*

Reported by the calendar and habits agents and visible throughout: `"not
connected: "+err.Error()`, `"event not found: "+err.Error()`,
`"update failed: "+err.Error()`, `"audit: "+err.Error()`, and every
`writeError(w, err.Error(), 500)`. `GET /api/analytics/meetings` is the worst
case — a user with no connected calendar gets `"internal error: no token for
user <uuid>"`, echoing an internal user id.

### T9. Responses built from pre-write in-memory state *(added)*

`POST /api/manager/team/members` encodes the same `*ManagerTeamMember` it handed
to `Upsert…`, which never writes back (`handlers_manager.go:311`, confirmed). The
`201` body therefore carries `"ID":"00000000-0000-0000-0000-000000000000"` and
`"CreatedAt":"0001-01-01T00:00:00Z"`. `POST /api/manager/profile` likewise
returns the profile as read *before* the upsert. And two handlers discard a
re-read error and encode the result anyway — `patchMember` and `patchTeam` can
both return `200` with the JSON literal `null` after a *successful* write.

### T10. Whole surfaces with no frontend consumer *(added)*

This is why most of the above went unnoticed. Endpoints with zero references in
`smart-calendar-flow/src`: **the entire habits/analytics/meeting-briefs/Slack/
Notion domain (22 operations)**, all four daily-recap endpoints, all three
`/api/admin/sso` methods, `GET /api/calendar/freebusy`, `GET /api/org/members`,
and `POST /api/conference/create`. For those, schema confidence is
"backend-verified, consumer-unverified" — there is no second opinion on any of
their shapes.

### T11. Duplicate and aliased routes *(added)*

`routes.go:157-158` binds `slh.listHostInvites` to both
`/api/scheduling-links/host-invites` and `/api/scheduling-links/invites` — the
same function, same payload, neither marked deprecated. `routes.go:159-160` and
`:166-167` bind accept/decline to both `/{id}/accept|decline` and
`/host-invites/{id}/accept|decline`. Four spec operations, two behaviours. The
booking agent's analysis that this is not a *routing* hazard (chi matches static
segments before wildcards) is correct; the hazard is semantic — `{id}` in
`/host-invites/{id}/accept` is the **scheduling-link** id, not the invite id, and
passing the wrong one lands squarely in T6's silent no-op.

---

## 3. Findings

Domain key: `auth`, `cal` (calendar), `sched` (scheduling), `book` (booking),
`team` (teams/manager), `hab` (habits/analytics/integrations).

| ID | Sev | Conf | Domain | Endpoint(s) | What is wrong | User-visible consequence | Source |
|---|---|---|---|---|---|---|---|
| API-001 | critical | Confirmed | auth | `GET /api/admin/sso`, `POST /api/admin/sso` | `storage.SSOProvider` is encoded whole; it carries `OIDCClientSecret` and `SAMLCert`, and the only gate is `user.OrgID != nil` (`handlers_sso.go:258-276`). No admin role is checked anywhere. | Any authenticated member of the org can read the org's OIDC client secret and SAML signing cert in plaintext. | auth (b)1; re-read here |
| API-002 | critical | Confirmed | sched | `GET /api/settings`, `PUT /api/settings`, `PATCH /api/settings/daily-recap`, `POST /api/llm/test`, all 4 daily-recap routes | `GetSettings`/`SaveSettings` are pinned to `WHERE id = 1` (`storage/settings.go:227,269`) while every route sits behind `requireAuth`. `GetSettingsByUser` exists and is unused here. | Every user reads and overwrites the same settings row. User B saving their working hours destroys user A's. | sched (b)1; re-read here |
| API-003 | critical | Confirmed | sched | `GET /api/settings` | `LLMAPIKey` has tag `json:"llmApiKey"` and no redaction (`storage/settings.go:172`); the handler encodes the whole struct. Combined with API-002 the row is shared. | Any authenticated user reads the deployment's LLM API key in cleartext. | sched (b)16; re-read here |
| API-004 | critical | Reported | sched | `PUT /api/settings` | Body decodes into a zero-valued `storage.Settings`; the INSERT lists every column, so omitted fields persist as `""`/`0`/`false`. Only `microsoft_tokens`/`zoom_tokens` (`json:"-"`) survive. | A partial PUT silently wipes the stored LLM API key, timezone, focus config and recap config. Data loss, not just drift. | sched (b)2 |
| API-005 | critical | Confirmed | cal | `GET /api/audit` | `storage.ListAuditLog(db, limit)` has no user or org predicate, and `audit_log` has no user column to filter on (`storage/focus_blocks.go:62`). Details include meeting titles (`handlers_schedule.go:188`) and raw NLP prompt text (`nlp/parser.go:221`). | Any authenticated user reads every other user's meeting titles and natural-language scheduling prompts. Fixing it needs a migration, not a WHERE clause. | cal (b)4; re-read and extended here |
| API-006 | high | Confirmed | book | `GET /api/scheduling-links/{id}` | No ownership check (`handlers_scheduling_links.go:414-431`), unlike PATCH/DELETE/`/bookings`/`/hosts` which all enforce `OwnerUserID != userID → 403`. Returns `includeHosts=true`, i.e. every host's email, name and avatar URL. | Any authenticated user holding a link UUID reads the full record and host roster. **Not rated critical:** the public booking surface keys on `slug` and never emits the UUID (`publicLinkDTO`, `handlers_booking.go:37-45`), so the id is not broadly distributed. | book (c)1; re-read here |
| API-007 | high | Confirmed | auth | every authenticated route | No cookie anywhere in `backend/` sets `Secure` — grep for `Secure` in non-test Go returns nothing. That includes the 7-day session cookie `auth_token` (`handlers_auth.go:147-153`). | The session cookie is transmitted over plaintext HTTP if the app is ever reachable without TLS, and is stealable by a network attacker on any such request. | new in this pass (auth notes list cookie attrs but not this) |
| API-008 | high | Confirmed | auth | `POST /api/auth/logout`, `GET /api/auth/callback*`, all `requireAuth` routes | `requireAuth` reads the `auth_token` cookie **first** and only then `Authorization: Bearer` (`middleware.go`). A stale cookie shadows a valid bearer token. | A user with an expired cookie and a fresh bearer token is rejected. Also makes a bearer-only client unable to start the Slack/Notion OAuth flows, which sit inside the auth group. | auth (b)4 + habits (a)3 |
| API-009 | high | Confirmed | auth | `GET /api/auth/status` | Branches on the global settings row and returns the global `CalendarEmail`; `auth.LoadMicrosoftToken(h.db)` takes no user id (`handlers_auth.go:82-107`). Only the google branch consults the caller. | In a multi-user deployment one user's Outlook address and connection state are reported to everybody. Same root cause as API-002. | auth (a)6; re-read here |
| API-010 | high | Confirmed | auth | `GET /api/auth/detect` → `/api/auth/sso/{domain}` | Backend `DetectResult` returns `{type, provider_name?, redirect_url?}`. The frontend declares `{type, domain?}` and gates the redirect on `res.domain` (`AuthDialog.tsx:32,111`), which is never sent. | An SSO-configured user can never reach the SSO flow; they fall through to the password step and cannot log in. | auth (a)1 |
| API-011 | high | Reported | auth | `GET /api/auth/callback`, `/api/auth/microsoft/callback`, `/api/auth/callback/oidc/{domain}` | `issueJWT` returns without setting a cookie when `jwtSecret == ""` or token generation fails, and every callback still issues its `302` to `/auth/callback`. | The user lands on the success page logged out, with no error shown. | auth (b)5 |
| API-012 | high | Reported | auth | `GET /api/auth/microsoft/callback` | If the Graph profile fetch or user upsert fails the handler skips `issueJWT` but still redirects with `?provider=outlook`. | Failure is indistinguishable from success. Same class as API-011 but a distinct code path. | auth (b)6 |
| API-013 | high | Reported | sched | `GET /api/settings`, `PUT /api/settings` | `normalizeSettings()` bridges four keys; `settingsRequestBody()` spreads snake_case into a camelCase decoder. | Every settings field except four reads as `undefined` in the UI, and every edit the user makes is discarded server-side. Compounds API-004. | sched (a)1 |
| API-014 | high | Confirmed | sched | `GET /api/focus/blocks`, `POST /api/focus/run` | Three shapes for one concept: `storage.FocusBlock` (untagged → `ID`/`StartTime`/`EndTime`), `engine.FocusBlock` (untagged, *different fields* → `Start`/`End`, no `ID`), frontend `{id, google_event_id, start_time, end_time, date}`. | Nothing matches. Every field the focus-time UI reads off a block is `undefined`. | sched (a)5; re-read here |
| API-015 | high | Reported | sched | `POST /api/focus/run` | `FocusRunResult` is camelCase (`weekStart`, `createdBlocks`, `skippedDays`, `totalMinutes`); the frontend type is snake_case. Only `errors` aligns. | The focus-run summary renders blank. | sched (a)6 |
| API-016 | high | Reported | sched | `POST /api/schedule/compress` | Response `engine.MoveProposal` is camelCase (`eventId`, `proposedStart`, `focusGainMinutes`); the frontend type is snake_case. The *apply* request is snake_case and accidentally works. | The compression preview shows nothing; pressing apply then posts fields the user never saw. | sched (a)7 |
| API-017 | high | Confirmed | cal, sched | `PATCH /api/events/{id}`, `POST /api/schedule/create`, `POST /api/nlp/confirm` | Raw `calendar/v3.Event` encoded instead of the `CalendarEvent` DTO (`handlers_events.go:87`). | Reading `.title`/`.start`/`.end` off the result yields `undefined` on all three. Contradicts `TestToCalendarEventDTO_JSONShape` on the list endpoint. | cal (a)4, sched (a)10; re-read here |
| API-018 | high | Confirmed | cal | `PATCH /api/events/{id}` | The body struct accepts only `title, description, location, start, end, attendees` (`handlers_events.go:36-43`). The frontend sends `send_updates`, `room_resource_email` and `attendee_details`; `encoding/json` discards them without error. | Room changes and attendee edits made through this endpoint are silent no-ops, and guests are never notified of a reschedule. | cal (a)3; re-read here |
| API-019 | high | Reported | book | `GET /api/scheduling-links/{id}/bookings` | Returns raw `storage.Booking` (`start_time`/`end_time`); `schedulingLinks.ts::listBookings` reads `b.start`, `b.end`, `b.link_slug`, `b.title`, `b.duration_minutes`, `b.hosts`. The frontend type was written against the *public confirmation* DTO and reused. | Every row in the owner's bookings list shows `start: undefined`, `title: "Booking"` and `duration_minutes: NaN`. | book (a)1 |
| API-020 | high | Confirmed | book | `POST /api/scheduling-links/{id}/accept`, `/decline`, `/host-invites/{id}/accept`, `/decline`, `POST /{id}/leave` | No `RowsAffected` check (T6) and **no 403/404 branch at all** in the handler. | Accepting a co-host invite with the wrong id — e.g. the host-row id returned by `POST /{id}/hosts`, which reads like the right one — returns `200 {"status":"accepted"}` and does nothing. The user believes they joined. **Not a security finding:** the UPDATE is keyed on `user_id = $2`, so no cross-user write is possible. | book (b); re-read here |
| API-021 | high | Reported | team | `GET /api/teams/invites/{token}` | Backend returns only `{teamName, inviterName, email}`; `teams.ts::normalizeInvite` reads `team_id`, `token`, `expires_at`, `inviter_email`. `teamInvites.test.ts` asserts a hand-written fixture the backend never produces, so the test passes while the integration is broken. | The invite preview cannot show who invited you or when the link expires, despite the invite email promising "expires in 7 days". | team A2 |
| API-022 | high | Reported | team | `GET /api/manager/analytics` | The handler's `memberAnalytics` emits `{email, display_name, weeks}`. `manager.ts::normalizeAnalytics` defaults the missing `data_available` to **`true`**, and its `weeks` mapper drops the per-week `data_available` the backend *does* send. | The degraded-analytics signal is lost in the unsafe direction: the manager chart renders zeroes as measured data. Rated high rather than medium because the backend computes the signal deliberately (invariant I7) and the frontend inverts it. | team A7 |
| API-023 | high | Reported | team | `DELETE /api/teams/{id}/members/{userId}` | Self-removal skips `requireOwner` entirely; there is no last-owner guard. The `422 "the team owner cannot be removed"` rule exists only on the manager-domain delete. | A sole owner can remove themselves and strand the team with no administrator. Recoverable only by direct DB access. | team A14 |
| API-024 | high | Reported | sched | `POST /api/nlp/confirm` | Bounds check is `len(...) == 0 \|\| SelectedSlotIndex >= len(...)` (`handlers_nlp.go:49`). A negative index passes, then indexes out of range and panics; `sentryMiddleware` repanics, so it surfaces as a bare 500 from `net/http`. | A malformed client request kills the request with an unhandled panic instead of a 400. Noisy in Sentry, opaque to the caller. | sched (b)7 / `x-uncertain` |
| API-025 | high | Reported | sched | `GET /api/settings`, and every engine that reads `workingHours` | The validator lowercases the `workingHours.days` key before checking it, so `"Monday"` validates; `WorkWindow()` looks up `strings.ToLower(weekday.String())`, so a stored `"Monday"` never matches. | A day saved with a capitalised key is silently treated as a non-working day by focus, compression and scheduling. | sched (b)15 |
| API-026 | high | Reported | hab | `GET /api/integrations/slack/callback`, `/notion/callback` | Both callbacks are registered inside the `requireAuth` group (`routes.go:181-191`) and call `userIDFromCtx`, so the browser must carry `auth_token` across the provider's redirect back. | Any tightening of cookie `SameSite` turns the connect flow into a 401. Today it works only because the cookie is `Lax` and the callback is a top-level GET — a fragile accident. | hab (a)4 |
| API-027 | medium | Confirmed | hab | `GET /api/integrations/slack/connect`, `/notion/connect` | `slack_oauth_state` and `notion_oauth_state` are set with `Path`, `MaxAge`, `HttpOnly` only — **no `SameSite` and no `Secure`** (`handlers_integrations.go:41,174`). Every other state cookie in the codebase (`oauth_state`, `ms_oauth_state`, `sso_state`, `zoom_oauth_state`) does set `SameSiteLaxMode`. | Weakened OAuth CSRF protection on two flows. Rated medium, not high: modern browsers default an attribute-less cookie to `Lax`, so the practical gap is narrow — but it is an inconsistency with the rest of the codebase, so it is a bug either way. | hab (c)10; corrected here |
| API-028 | medium | Confirmed | cal | `POST /api/freebusy` | One response body carries both key casings for the same data — `results[].busy[]` is `{"Start","End"}` (`calendar.TimeSlot`), `participants[].busy[]` and `busy{}` are `{"start","end"}`. | A client that iterates `results` (the documented legacy key) reads `undefined`. | cal (b)1; re-read here |
| API-029 | medium | Reported | cal | `GET /api/calendar/freebusy`, `GET /api/personal-calendars/{id}/preview` | Untagged `calendar.TimeSlot` / `calendar.GenericEvent` (T1). | Capitalized keys on both. The preview endpoint *is* consumed by the frontend, so this one is live. | cal (b)1 |
| API-030 | medium | Confirmed | team | `POST /api/manager/profile`, `POST /api/manager/team/members`, `PATCH /api/manager/team/members/{email}` | Untagged `storage.UserProfile` / `storage.ManagerTeamMember` (T1), in a domain that is otherwise uniformly snake_case, on paths whose GET *is* snake_case. | Latent only — the frontend discards these bodies and re-fetches. Any other client gets nothing usable. | team A3; re-read here |
| API-031 | medium | Confirmed | team | `POST /api/manager/team/members` | The `201` body is the pre-insert struct (`handlers_manager.go:311`); `Upsert…` never writes back. | `ID` is the zero UUID and `CreatedAt`/`UpdatedAt` are `0001-01-01T00:00:00Z`. A client that stores the returned id stores garbage. | team A3; re-read here |
| API-032 | medium | Reported | team | `PATCH /api/manager/team/members/{email}`, `PATCH /api/teams/{id}` | Both discard the re-read error (`updated, _ := …`) and encode the result. | A `200` whose body is the JSON literal `null` after a *successful* write. | team A4 |
| API-033 | medium | Reported | sched, team, hab, cal | ~15 endpoints (see T5) | Nil slices encode as `null` rather than `[]`, inconsistently with sibling endpoints in the same domain that do normalise. | Clients must defend with `?? []` per endpoint; several already do, some do not. | sched (b)6, team A5, hab (c)4 |
| API-034 | medium | Reported | sched | `POST /api/schedule/compress` | Week mode `continue`s past every engine error, returning `200` with body `null`; single-day mode with the same error returns `500`. | A completely failed compression run looks like "nothing to compress". | sched (b)5 |
| API-035 | medium | Reported | sched | `POST /api/focus/run`, `POST /api/schedule/compress` | Malformed JSON is swallowed — `week` is reset to `""` / the decode error is discarded (`_ = json.NewDecoder(...)`). Both return 200. Every sibling endpoint returns 400. | Garbage input succeeds silently against the wrong week. | sched (b)4 |
| API-036 | medium | Reported | sched | `POST /api/schedule/create` | `out-of-hours meeting allowance reached` is returned as a plain error and becomes a `500 text/plain`. | A policy rejection is indistinguishable from a calendar outage; the UI cannot explain why the meeting was refused. 409/422 is the right code. | sched (b)9 |
| API-037 | medium | Reported | sched | `POST /api/llm/test` | The handler never reads the posted body; it always tests the *stored* settings. The frontend `llmTest(settings)` posts the edited object. | "Test connection" in the settings UI tests the saved config, not what the user just typed. | sched (a)9 |
| API-038 | medium | Reported | sched | `POST /api/llm/test` | Returns `{ok, response, provider}`; the frontend type is `{ok, message, latency_ms?}`. | The test result panel is blank whichever way the test went. | sched (a)9 |
| API-039 | medium | Reported | sched | `GET/PUT /api/settings` | `conferencingProvider` backend values are `meet\|zoom\|teams`; the frontend enum is `google_meet\|zoom\|teams\|custom`. `LLMProvider` backend accepts `vertex` and `""`; the frontend enum has neither, though `""` is the DB default. | Google Meet never matches on the frontend; a Vertex-configured or unconfigured deployment falls off the end of the provider switch. | sched (a)3, (a)4 |
| API-040 | medium | Reported | cal | `GET /api/rooms` | The frontend sends `start` and `end`; the handler reads only `q` (`handlers_events.go:106`). | Room availability filtering does not exist against the real backend; `available` is only ever populated by `MOCK_ROOMS`. | cal (a)2 |
| API-041 | medium | Reported | cal | `GET /api/rooms` | `calendar.Room` is `{id, name}`; the frontend `Room` type declares `email` as **required** plus `building`, `floor`, `capacity`, `available`. | Any live (non-mock) room list renders `undefined` for five of seven fields. | cal (a)1 |
| API-042 | medium | Reported | cal | `DELETE /api/events/{id}` | The `send_updates` query param is sent by `client.ts:1187` and read by nobody. | Guests are never notified of a cancellation. | cal (a)5 |
| API-043 | medium | Reported | cal | `PATCH /api/events/{id}` | Setting `start`/`end` always writes `EventDateTime{DateTime: …}`, never `Date:`. | Editing the time of an all-day event silently converts it to a timed event. | cal (b)6 |
| API-044 | medium | Reported | cal | `PATCH /api/events/{id}` | The events *list* endpoint filters `*@resource.calendar.google.com` out of `attendees`; PATCH writes `attendees` wholesale. | A read-modify-write round trip through the UI removes the room booking from the event. | cal (b)7 |
| API-045 | medium | Reported | cal | `PATCH` vs `DELETE /api/events/{id}` | PATCH maps *every* `GetEvent` failure to `404` (including network and permission errors); DELETE maps *every* failure to `500`, so deleting a missing event is a 500. | Two methods on one path disagree about what "not found" means. | cal (b)5 |
| API-046 | medium | Reported | cal | `POST /api/freebusy` | Frontend `ParticipantCoverage` declares `provider`, which the backend never emits, and `CoverageStatus: "paceday_user"`, which `engine/freebusy_service.go:143-146` can never produce. | The provider badge on the coverage chip can never render; one of three declared coverage states is unreachable. | cal (a)6 |
| API-047 | medium | Reported | cal | `GET /api/org/members` | Returns `200 []` when the user lookup errors, rather than 500. | "Your org has no members" and "we could not read your org" are indistinguishable. | cal (b)15 |
| API-048 | medium | Reported | book | `POST /api/scheduling-links/{id}/hosts` | `ON CONFLICT … DO UPDATE SET status = EXCLUDED.status` with status `'pending'`. | Re-inviting someone who already accepted demotes them to pending, and returns `201` as if new. Same effect from `co_host_emails` on PATCH. | book (b) |
| API-049 | medium | Reported | book | `POST /api/scheduling-links`, `PATCH /api/scheduling-links/{id}` | Alias precedence is inverted between the verbs: create resolves `duration_options` over `durations` and `days_of_week` over `days`; PATCH applies them in sequence so the opposite wins. | Sending both spellings gives different results depending on the verb. | book (b) |
| API-050 | medium | Reported | book | `DELETE /api/scheduling-links/{id}` | Soft delete sets `active = false`. `GetSchedulingLinkBySlug` filters `active = true`; `GetSchedulingLinkByID` and `ListSchedulingLinksByUser` do not. | The public page 404s but the "deleted" link stays in the owner's list forever. | book (b) |
| API-051 | medium | Reported | book | `POST /api/book/{slug}` | `uses_count` is a `SELECT COUNT(*) … WHERE status <> 'cancelled'` subquery, not a stored counter. | Cancelling a booking re-opens an exhausted `single_use` link. | book (b) |
| API-052 | medium | Reported | book | `POST /api/conference/create` | No validation: missing `title`/`start`/`end` decode to Go zero values and are sent to the provider. | A `{}` request reaches Zoom/Meet with a blank title and the year 1. No frontend consumer today, so latent. | book (b) |
| API-053 | medium | Reported | book | `POST /api/scheduling-links` (`days` alias) | `dayNumbers` drops unrecognised tokens, so `{"days":["Montag"]}` becomes `[]` and fails as "at least one day is required". | The error does not explain that the day names were not understood. | book (b) |
| API-054 | medium | Reported | hab | `GET /api/analytics/meetings` | `calOpsForUser` failure is wrapped as `"internal error: no token for user <uuid>"` and returned to the client. | A user with no connected calendar gets a 500 containing an internal user id instead of a 4xx telling them to connect a calendar. T8's worst case. | hab (a)13 |
| API-055 | medium | Reported | hab | `GET /api/analytics/week` vs `GET /api/analytics/trends` | `week_start` is `"2006-01-02"` on one and RFC3339 on the other (`handlers_analytics.go:137` vs raw `storage.AnalyticsWeek`). `breakdown` exists only on `/week`. | The same logical field has two types; the trends endpoint cannot render the week chart without client-side recomputation. | hab (a)6, (a)7 |
| API-056 | medium | Reported | hab | `PATCH/DELETE /api/habits/{id}`, `GET /api/habits/{id}/occurrences` vs `PATCH …/occurrences/{occurrenceId}` | The first three return `403 forbidden` for another user's habit (leaking existence); the last scopes inside the SQL and returns `404 not found`. | Inconsistent, and the 403 path confirms that a habit id exists. | hab (a)12 |
| API-057 | medium | Reported | hab | `GET /api/analytics/week`, `/trends` | `AnalyticsEngine.compute` never assigns `BufferMinutes` (`engine/analytics.go:185-207`) despite `ComputeBufferBlocks` existing and being used by the habits engine. | `buffer_minutes` is always `0`. Any buffer chart shows a flat zero line. | hab (a)14 / `x-uncertain` |
| API-058 | medium | Reported | hab | `GET /api/analytics/week` | `habit_completion_rate` is a 0..1 fraction while `AnalyticsBreakdownEntry.percentage` is 0..100, in the same response. | A client that formats both the same way is off by 100×. | hab (a)15 |
| API-059 | medium | Reported | team | `PATCH /api/teams/{id}/no-meeting-zones/{zoneId}` | PATCH is a full replace with no merge; an omitted `dayOfWeek` decodes to `0` and 422s. | A naive partial PATCH always fails. The frontend compensates by GETting and re-sending everything. | team A10 |
| API-060 | medium | Reported | auth | `POST /api/admin/sso` → `GET /api/auth/sso/{domain}` | `provider_type: "saml"` is accepted and persisted, then the start route returns `501 "SAML not yet supported"`. `SsoProviderCreateRequest` validates nothing beyond the three required fields, so an `oidc` row can be stored with an empty issuer and only fails later as a 500. | An admin can configure SSO that cannot possibly work and gets no feedback until a user tries to log in. | auth (b)9, (c)2 |
| API-061 | medium | Reported | auth | `DELETE /api/auth/disconnect` | Also clears the `auth_token` cookie, not just the calendar token. | "Disconnect calendar" logs the user out. Neither the name nor the frontend's `authDisconnect(): Promise<void>` suggests a session change. | auth (b)7 |
| API-062 | medium | Reported | auth | `POST /api/auth/detect` | `detectLimiter` is a per-process, never-swept `map[string]*ipBucket` keyed on client-supplied `X-Real-IP`/`X-Forwarded-For`. | The documented 20/min limit is 20/min × replicas, is spoofable by header, and the map grows without bound. | auth (b)12 |
| API-063 | low | Reported | auth | `POST /api/auth/logout` | Returns `200` with an empty body and no `Content-Type`. `apiFetch` tolerates it; `requestApi` would throw `ApiUnreachableError("Non-JSON response")` for the same shape. | Works today only because the lenient wrapper happens to call it. `204` is correct for both. | auth (a)4 |
| API-064 | low | Reported | auth | `GET /api/auth/google`, `/api/auth/microsoft` | `login_hint` is sent by `AuthDialog.tsx` and read by neither `startOAuth` nor `startMicrosoftOAuth`. | The user still sees an account chooser after typing their email. | auth (a)2 |
| API-065 | low | Reported | auth | `GET /api/auth/me` | Backend serialises `avatarUrl`; the frontend `AuthUser` declares `avatar?`. `schedulingLinks.ts` types the same object as `{id, email, name?}`. | `user.avatar` is always `undefined`; the avatar never renders. Three types for one payload. | auth (a)3 |
| API-066 | low | Reported | auth | `POST /api/admin/sso` | `Enabled` is hardcoded `true` on upsert and no route can set it false. | The column is write-once via this API; SSO cannot be disabled without deleting the row. | auth (b)10 |
| API-067 | low | Confirmed | auth | `DELETE /api/admin/sso/{domain}` | No `RowsAffected` check (T6) — `204` for a domain with no provider row. | Delete is not observably idempotent-vs-missing. | auth (b)11; re-read here |
| API-068 | low | Reported | auth | `GET /api/health` | `version` is the literal `"0.1.0"` in the handler, not injected at build time. | The version never changes between releases, so the endpoint cannot be used to confirm a deploy. | auth (b)14 |
| API-069 | low | Confirmed | book | `GET /api/scheduling-links/invites`, `POST /api/scheduling-links/{id}/accept\|decline` | Duplicate routes (T11) — `routes.go:157-158` and `:159-160`/`:166-167` bind the same handlers twice, neither marked deprecated. | Four spec operations for two behaviours; the `/host-invites/{id}/...` spelling actively misleads about what `{id}` is (see API-020). | book (b) |
| API-070 | low | Reported | book | `GET /api/conference/providers` | The array is 1–4 entries depending on which OAuth env vars are set; `custom` is always present and always `connected: true`. `handlers_conferencing_test.go:117` indexes `[0]` as google_meet. | A test that only passes on a fully-configured server. | book (b) |
| API-071 | low | Reported | book | `POST /api/conference/events/{id}` | If the event already has a conference, the handler returns `200` with the **existing** provider even when `custom` was requested. | The `provider` field does not echo the request. | book (b) |
| API-072 | low | Reported | book | `POST /api/scheduling-links/{id}/hosts` | `inviteHost` returns `404` for both "link not found" and "no account with that email". | A caller cannot distinguish a bad link id from inviting someone who has not signed up. | book (b) |
| API-073 | low | Reported | book | `POST /api/book/{slug}` | Dead `if err != nil` re-check at `handlers_booking.go:281-284` (unreachable); `linkExhausted` runs twice for the same 410. | Dead code; the "invalid end time" message appears twice in one handler. | book (b) |
| API-074 | low | Reported | cal | `GET /api/audit` | Frontend `DEFAULT_AUDIT_LIMIT = 50`; backend default when the param is absent is `100`. | Not a live bug — the frontend always sends a value — but the two "defaults" differ. | cal (a)11 |
| API-075 | low | Reported | cal | `POST /api/personal-calendars` | `Enabled bool` is not a pointer, so omitting `enabled` creates a **disabled** calendar. | Masked today because the frontend always sends `true`. | cal (b)8 |
| API-076 | low | Reported | cal | `GET /api/calendar/events` | `is_personal_block` is declared on the DTO and never assigned by `toCalendarEventDTO`. | Dead field; never appears in a response. | cal (b)10 / `x-uncertain` |
| API-077 | low | Reported | cal | `POST /api/freebusy` | No validation that `end_time > start_time`. | An inverted window returns 200 with empty results rather than a 400. | cal (b)12 |
| API-078 | low | Reported | cal | `GET /api/attendees/suggest` | Does not filter room resources, while `/api/calendar/events` does. 30-day lookback and 20-result cap are hardcoded. | Conference rooms appear in the people-suggestion list. | cal (b)14 |
| API-079 | low | Reported | sched | `GET/DELETE /api/focus/blocks` vs `POST /api/schedule/compress` | Compress snaps `week` to Monday via `startOfWeek()`; the focus routes use the supplied date verbatim as the range anchor. | Passing a Wednesday to `/api/focus/blocks` returns Wed–Tue, not Mon–Sun. | sched (b)10 |
| API-080 | low | Reported | sched | `POST /api/nlp/parse` | `nlp.ParseResult.range_start`/`range_end` are `time.Time` with `omitempty`, which does not omit the zero time. | A missing range serialises as `0001-01-01T00:00:00Z` rather than being absent. | sched (b)18 |
| API-081 | low | Reported | sched | `PUT /api/settings` | `recapSendTo` is validated (`dm\|channel`) only on the daily-recap PATCH, not on PUT. | A bad value on PUT hits the DB CHECK and returns a 500 instead of a 400. | sched (b)13 |
| API-082 | low | Reported | sched | `PUT /api/settings` | `validateSettings` gaps: no `workEnd > workStart` for the legacy global fields, no `focusMin <= focusMax`, no `>= 0` on `bufferMinMeetingMinutes` (though `bufferBefore/After` have it), `timezone` never validated (engines fall back to UTC). | Invalid configurations are accepted and misbehave later, silently. | sched (b)14 |
| API-083 | low | Reported | hab | `POST /api/habits/reoptimize` vs `POST /api/analytics/recompute` | Identical behaviour (detached goroutine), different status codes: `200` vs `202`. | Cosmetic inconsistency in an ack contract. | hab (a)11 |
| API-084 | low | Reported | hab | `GET /api/integrations/slack/callback`, `/notion/callback` | Success redirects to the literal `/app/settings?tab=integrations&{slack\|notion}=connected`, not derived from the `frontendURL` config that `RegisterRoutes` receives. | Breaks on any non-default frontend deployment. No SPA route reads those params today. | hab (a)5 |
| API-085 | low | Reported | hab | `GET /api/integrations/slack/connect`, `/notion/connect` | The `503` guard keys on the client id only; an empty `SLACK_REDIRECT_URI`/`NOTION_REDIRECT_URI` is not checked. | A half-configured server 302s to the provider with `redirect_uri=` empty instead of returning 503. | hab (c)11 |
| API-086 | low | Reported | team | `GET /api/manager/profile` | Sends `team_member_count`, which nothing reads — and it is a *global* roster count, not scoped to the team the UI is showing. | Dead field that would be wrong if anyone used it. | team A8 |
| API-087 | low | Reported | team | `GET /api/teams/{id}/no-meeting-zones` | `listZones` orders by the **stored** `day_of_week` (Sunday=0) while emitting the wire convention (Sunday=7). | Sunday sorts first in a list labelled 1..7. | team A9 |
| API-088 | low | Confirmed | team | manager routes without `team_id` | `team_id` is *required* on `detect`, `detect/confirm` and `GET /api/manager/team`, but *optional* on the three `team/members` mutations, `gaps`, `schedule` and `analytics`. | Reading a roster demands a team; mutating one does not. **Correcting the teams report:** it describes the `team_id`-less calls as having "no authorization beyond JWT". They are in fact self-scoped — `ManagerUserID: userID` (`handlers_manager.go:291`) — so this is an API-shape inconsistency, not a vulnerability. | team A13; corrected here |
| API-089 | low | Reported | team | `GET /api/teams/invites/{token}` | The handler comment says `// (public)` but the route is inside the `requireAuth` group (`routes.go:224`). | Any doc generated from the comment will be wrong. The code is right; the comment is not. | team A12 |
| API-090 | low | Reported | team | `POST /api/teams/{id}/invites` | No check for an existing pending invite and no uniqueness constraint on `(team_id, invitee_email)`. | Duplicate invites accumulate for the same address. | team I9.4 |
| API-091 | low | Reported | sched, hab, cal, book, team | 22 habits operations, 4 daily-recap, 3 `/api/admin/sso`, `GET /api/calendar/freebusy`, `GET /api/org/members`, `POST /api/conference/create` | No frontend consumer anywhere in `smart-calendar-flow/src` (T10). | Not defects themselves, but these surfaces have no second opinion on their shapes — and API-001 (an org-wide secret leak) sat undetected in exactly this category. | hab (a)1, auth (a)7, cal (a)7, (a)8, book (a)11 |
| API-092 | low | Reported | auth | `/api/auth/detect`, `/api/auth/sso/{domain}`, `/api/auth/callback/oidc/{domain}`, `/api/auth/me`, `/api/auth/logout`, all `/api/admin/sso` | No Go test coverage. `handlers_auth_test.go` covers only status 200, disconnect 204, startOAuth 302 and callback-invalid-state 400. | Every other status code documented in `auth.yaml` is read off handler source, not off a passing assertion. Relevant to the `CLAUDE.md` 75–80% coverage rule. | auth (b)13 |

**Total: 92 findings — 5 critical, 21 high, 36 medium, 30 low.**

### Where reports disagree

- **`GET /api/calendar/freebusy`.** The calendar agent marks it "dead or broken; a
  human must decide which" and flags the operation `x-uncertain`. No other report
  touches it. There is no contradiction, only a single unresolved vote — recorded
  as API-029/API-091 rather than as a deletion recommendation.
- **The `requireAuth` 401 shape.** Five of six agents report it independently
  (auth (b)3, calendar (b)3, scheduling (b)17, booking (b), habits (a)10) and all
  five describe it identically. Booking adds the observation that
  `requestApi` survives it "because it only enforces `application/json` on 2xx",
  while habits documents it on `components/responses/Unauthorized` as though it
  were JSON. These are consistent facts stated with different emphasis, not a
  disagreement — but the *spec* currently documents the media type wrongly, which
  is worth fixing at assembly time.
- **`ErrorResponse` duplication.** The habits agent checked all four fragment
  definitions (auth, calendar, habits, teams) and found them structurally
  identical; the scheduling agent pre-emptively prefixed its own as
  `SchedulingErrorResponse` to avoid a collision. The habits agent's
  recommendation — keep exactly one global definition — is the right one, and the
  scheduling prefix should be removed at assembly.

---

## 4. Security findings

Restating the critical and high rows that are security issues, in terms of what
someone could actually do.

### Critical

**API-001 — SSO secrets readable by any org member.**
`GET /api/admin/sso` gates on `user.OrgID != nil` and nothing else; there is no
admin role anywhere in the codebase. It encodes `storage.SSOProvider` whole,
including `OIDCClientSecret` and `SAMLCert`. *A merely-curious colleague* with a
normal account and a browser devtools console reads the org's OIDC client secret.
*An attacker* who compromises any single low-privilege account obtains the
credentials to impersonate the application to the identity provider. Verified by
reading `storage/sso_providers.go:12-25` and `api/handlers_sso.go:258-276`.

**API-003 + API-002 — LLM API key readable by every authenticated user.**
`GET /api/settings` returns `llmApiKey` in cleartext off a settings row shared by
every user (`WHERE id = 1`). Any account reads the deployment's OpenAI/Anthropic
key and can spend against it. The two findings compound: even a future per-user
settings fix leaves the key readable by its owner's own session unless the field
becomes write-only.

**API-005 — the audit log is everyone's audit log.**
`GET /api/audit` runs `SELECT … FROM audit_log … LIMIT $1` with no predicate, and
the table has no user column to add one to. The `details` column carries meeting
titles (`{"event_id":"…","title":"<meeting title>"}`) and the raw text of
natural-language scheduling requests (`{"text":"<user's prompt>","intent":"…"}`).
*A curious colleague* reads the titles of everyone's meetings and the prompts
people typed — "1:1 re: PIP", "call with recruiter at X" — with one request to a
documented endpoint. This is the finding with the widest blast radius and the
most expensive fix, because it needs a migration before it needs a `WHERE`.

**API-002 + API-004 — cross-user destruction of settings.**
Every authenticated user writes the same row, and `PUT /api/settings` is a full
replace that persists omitted fields as zero values. One user saving their
working hours resets everyone's timezone, focus configuration, recap schedule and
LLM key. This is data loss, reachable by normal use of the product, not by an
attacker.

### High

**API-006 — scheduling link records readable without ownership.**
`GET /api/scheduling-links/{id}` is the only route in its family with no owner
check and returns the full host roster (email, name, avatar URL). Exploitation
requires a link UUID, which the public booking surface never emits (it keys on
`slug`), so this is a broken access control with a narrow acquisition path rather
than an open door — which is exactly why it is rated high and not critical.

**API-007 — the session cookie is not `Secure`.**
`auth_token` is `HttpOnly` and `SameSite=Lax` but carries no `Secure` attribute,
and the string `Secure` does not appear in any non-test Go file in `backend/`. On
any deployment reachable over plaintext HTTP — including a stray redirect, a
misconfigured health check, or a local dev proxy — the 7-day session cookie
crosses the wire in the clear and is replayable.

**API-008 — a stale cookie beats a valid bearer token.**
`requireAuth` checks the cookie first. Beyond the usability failure, this means a
user cannot recover from a poisoned cookie by presenting a good token; they must
clear site data.

**API-009 — one user's calendar identity reported to all.**
`GET /api/auth/status` returns the global settings row's `CalendarEmail` and the
globally-stored Microsoft token's connection state to whoever asks.

**API-023 — a team can be stranded with no owner.**
`DELETE /api/teams/{id}/members/{userId}` skips `requireOwner` for self-removal
and has no last-owner guard. A sole owner leaving makes the team permanently
unadministrable through the API.

### Medium, listed here because they are security-shaped

**API-027** — the Slack and Notion OAuth state cookies omit `SameSite`
(and `Secure`), unlike every other state cookie in the codebase. Browser defaults
narrow the practical gap, which is why it is medium.
**API-054 / T8** — internal user UUIDs and raw provider error strings are echoed
in 5xx bodies across every domain.
**API-062** — the `/api/auth/detect` rate limiter keys on a client-supplied
`X-Forwarded-For` and is therefore trivially bypassed, while also leaking memory.
**API-056** — habit ownership failures return `403` rather than `404`, confirming
the existence of other users' habit ids.

---

## 5. The `x-uncertain` register

23 `x-uncertain` keys in `contracts/openapi/openapi.yaml` (line numbers from the
assembled bundle). The document preamble at line 13 and the prose
cross-references at lines 2989, 5999 and 7077 are not separate markers.

| # | Line | Location | What is unclear | Action that resolves it |
|---|---|---|---|---|
| U-01 | 324 | `/api/analytics/week` → `get` → `404` | Believed unreachable: `compute()` ends in `UpsertAnalyticsWeek`, whose `INSERT … RETURNING` yields a row or an error, so `result == nil && err == nil` should not fire. | **Write a test.** Stub the storage layer to return `(nil, nil)` and assert the branch; if it cannot be reached, delete the branch and the documented 404. |
| U-02 | 379 | `/api/audit` → `get` | Whether the global audit log is intentional. | **Decide, then migrate.** Resolved as API-005 (critical). The action is a migration adding `user_id` to `audit_log` plus a predicate — not a spec annotation. |
| U-03 | 1230 | `/api/calendar/freebusy` → `get` | Dead endpoint: no consumer, capitalized `Start`/`End`. Keep, align with `POST /api/freebusy`, or delete? | **Product decision.** Grep any non-SPA consumer (MCP server, scripts); if none, delete the route and the spec operation in one PR. |
| U-04 | 1386 | `/api/events/{id}` → `patch` | The 200 body is the raw Google event, not `CalendarEvent`. Which side is authoritative? No test pins it. | **Observe a response**, then **write a test.** Capture one real PATCH body, then add the `PATCH` analogue of `TestToCalendarEventDTO_JSONShape`. See API-017. |
| U-05 | 2889 | `/api/meetings/{event_id}/brief` → `get` → `400` | `400 "event_id required"` believed unreachable — chi never matches an empty segment. | **Write a test** asserting the router's behaviour for `/api/meetings//brief`; delete the branch if confirmed. Applies to the refresh route too. |
| U-06 | 2958 | `/api/nlp/confirm` → `post` | A negative `selected_slot_index` panics rather than returning 400. Should the spec document 400 (and the code be fixed) or the 500? | **Fix the code**, then document 400. Resolved as API-024; the panic is not a contract to preserve. |
| U-07 | 3506 | `/api/schedule/suggest` → `post` | The task brief asserts a 15-minute increment rule; the smart scheduler steps 30 minutes, and the only 15-minute stepping is in `engine/booking.go`, `engine/habits.go`, `engine/team_availability.go`. | **Ask the brief's author.** Either the rule belongs to a different domain (likely) or it is a missing feature in scheduling. Do not encode it in the spec until answered. |
| U-08 | 3738 | `/api/scheduling-links/{id}` → `get` | Missing ownership check — bug or deliberate public-by-UUID? | **Decide (it is a bug).** Resolved as API-006. Add the owner check and a `403` to the spec. |
| U-09 | 4429 | `/api/teams/{id}` → `get` → `404` | Believed unreachable: `requireMember` runs first and a `team_members` row cannot outlive its team (FK + `ON DELETE CASCADE`). | **Read the migration** to confirm the FK cascade, then delete the branch and the documented 404. |
| U-10 | 5055 | `AnalyticsWeekWithBreakdown.buffer_minutes` | Always `0`; `AnalyticsEngine.compute` never sets `BufferMinutes`. | **Decide:** wire up `ComputeBufferBlocks` (it already exists and the habits engine uses it) or drop the field. See API-057. |
| U-11 | 5301 | `CalendarEvent.is_personal_block` | Dead field — missing tagging logic, or should it be removed? | **Decide.** Grep `PersonalBlocker` for the intended tagging call site; if absent, remove the field from the DTO. See API-076. |
| U-12 | 5477 | `ConferenceMeetingDetails` | The exact `provider` string set written by `meet.go` / `zoom.go` / `teams.go` was not verified. | **Read three files.** Cheapest item on this list: grep the three provider implementations for the literal they assign, then add an `enum`. |
| U-13 | 5690 | `CurrentUserResponse.provider` | Observed writers pass `google`/`microsoft`/`sso`, but the column is free text with no CHECK constraint found. | **Read the migrations** for a constraint on `users.provider`, and confirm no fourth writer (seed script, Zoom flow). If closed, add both an enum and a CHECK. |
| U-14 | 6154 | `GoogleCalendarEvent` | Deliberately open and partial; the member set is fixed by the vendored Google client version, not this repo. | **Decide** whether the contract pins a normalised event instead. Resolving API-017 removes this schema entirely — the two should be one work item. |
| U-15 | 7180 | `NoMeetingZone.startTime` | Requests are strictly `HH:MM`; the response is the driver's text form of a `TIME` column, expected `"HH:MM:SS"`. No test asserts it. | **Observe a response** (or write an integration test against testcontainers, which this repo already uses) and pin the format. |
| U-16 | 7188 | `NoMeetingZone.endTime` | Same question as U-15. | Same test resolves both. |
| U-17 | 7296 | `OrgMember.provider` | The exact set written at signup was not traced. | **Read two files** — `handlers_auth.go` and `handlers_microsoft_auth.go` — and confirm against U-13, which is the same question about the same column. Resolve as one item. |
| U-18 | 8332 | `SettingsFields.calendarProvider` | No server-side validation; `google`/`outlook`/`webcal` is inferred from the frontend type and the DB default. | **Decide the authoritative list, then enforce it** in `validateSettings`. Today an unknown value is silently accepted and falls through the provider switch. |
| U-19 | 8356 | `SettingsFields.recapSendTime` | `recap_send_time::TEXT` on a Postgres `TIME` is believed to yield `08:00:00` (possibly with fractional seconds), contradicting the struct comment's `"HH:MM"`. | **Observe a response** against a real Postgres. Same class as U-15 and the booking `window_start`/`window_end` note — one integration test can pin all of them. |
| U-20 | 8402 | `SlackBlock` | `engine.slackBlock` is `map[string]interface{}`; only `header` and `context` blocks were confirmed in `BuildMessage`. | **Read `engine/daily_recap.go` `BuildMessage` end to end**, or capture a live `/preview` response, before tightening the schema. |
| U-21 | 8506 | `SsoProviderCreateRequest` | Nothing beyond the three required fields is validated, so an `oidc` provider can be created with an empty issuer or client id; it fails later as a 500. | **Decide and encode.** Make the OIDC trio required when `provider_type == "oidc"` (and the SAML trio for `"saml"`) via `dependentRequired`, and add the matching server-side validation. See API-060. |
| U-22 | 8567 | `SsoProviderResponse` | No consumer, so the PascalCase casing is inferred from the absence of json tags rather than observed. | **Observe a response.** But note the casing is the lesser problem — this is the schema that leaks `OIDCClientSecret` (API-001). Fix the leak first; the casing question dissolves when the DTO is written. |
| U-23 | 5999 | `FocusRunResult.skippedDays` (inline note, not a formal key) | Whether entries are `YYYY-MM-DD` or a weekday name; the `append` sites in `engine/focus_time.go` were not all traced. | **Read one file.** Grep every `append` into `SkippedDays` in `engine/focus_time.go`. |

Three further uncertainties are recorded in the notes but have no marker in the
bundle and should get one, or be resolved: the booking
`SchedulingLink.window_start`/`window_end` `"HH:MM:SS"` question (pinned only by
`handlers_scheduling_links_test.go:91`), `MeetingBriefResponse.generated_at`
omission semantics, and `HabitOccurrence.status` enum completeness (four values
found, no DB CHECK read).

---

## 6. Recommended sequencing

Twenty-three work items, all of them in machine-readable form in
`api-audit-issues.yaml` alongside this file. Every one of the 92 findings is
assigned to exactly one item. Batched by root cause, not by domain — several
items span four or five domains precisely because the cause is shared.
"Contract change" means the published wire shape moves, so `openapi.yaml` must
be regenerated and any consumer re-checked; 19 of the 23 items do, 4 do not.

### Batch A — ship first, independently, no contract change

**A1. Cookie hardening.** Add `Secure` to every `http.SetCookie` in `backend/`
(there are ten sites across six files) behind a config flag for local HTTP dev;
add `SameSite: http.SameSiteLaxMode` to the two integrations cookies. Covers
API-007 and API-027. Touches backend only, changes no response body, and is
independent of everything else here. **Do this one first** — it is the cheapest
security win in the register.

**A2. Stop echoing raw errors.** Replace `err.Error()` in response bodies with a
fixed message plus a server-side log. Covers T8, API-054. Backend only. Response
*bodies* change but their documented shape does not.

### Batch B — the security batch, in this order

**B1. SSO provider DTO + admin gate** (API-001, U-21, U-22, API-060, API-066,
API-067). One PR: introduce an `SsoProviderDTO` that omits `OIDCClientSecret` and
`SAMLCert`, add a real admin check, add `dependentRequired` validation on create,
add a `RowsAffected` check to the delete, and make `enabled` settable. Contract
change.

**B2. Audit log scoping** (API-005, U-02). Migration adding `user_id` to
`audit_log`, backfill decision (the safest is to hard-delete pre-migration rows
rather than attribute them), a `WriteAuditLog` signature change at all seven call
sites, and a predicate on `ListAuditLog`. Contract change. This is the largest
single item and should not be bundled with anything else.

**B3. Per-user settings + secret handling** (API-002, API-003, API-004, API-009,
API-081, API-082, U-18). Switch `getSettings`/`putSettings` and the four
daily-recap handlers to `GetSettingsByUser`/`SaveSettingsByUser`, finish
migration 018's `NOT NULL` tightening, make `llmApiKey` write-only (accept on
PUT, never return; return a `llmApiKeySet: bool` instead), make PUT a merge
rather than a replace, and fill the `validateSettings` gaps. Contract change.
Depends on nothing, but B3 and B2 both touch migrations and should not land in
the same release.

**B4. Scheduling-link ownership + last-owner guard** (API-006, U-08, API-023).
Two small independent authorization fixes; grouped because they are the same
review conversation. Contract change (new 403/422 responses).

### Batch C — one root cause each, land in any order

**C1. json tags on the seven wire-exposed structs** (API-014, API-028, API-029,
API-030, API-031, and the casing half of U-22). One PR: add tags to
`storage.FocusBlock`, `engine.FocusBlock`, `calendar.TimeSlot`,
`calendar.GenericEvent`, `storage.UserProfile`, `storage.ManagerTeamMember`, and
— if B1 has not already replaced it — `storage.SSOProvider`. Also read the
manager member row back after upsert so the `201` stops returning a zero UUID.
Contract change on seven endpoints. **This is the "one PR" the brief anticipated,
and it is genuinely one PR**, but note it cannot include the SSO struct if B1
lands first, because B1 deletes that encoding path entirely.

**C2. Single error envelope** (T2, API-034's 500 half, API-036, API-038's shape,
and the `Content-Type` overwrite at four sites). Replace every `http.Error` in
`backend/api` with `writeError` — 111 call sites across 19 files — and fix
`middleware.go`, `handlers_me.go` and `handlers_org.go` to write the JSON body
themselves rather than through `http.Error`. Then collapse
`SchedulingErrorResponse` into the single global `ErrorResponse`. Contract change
on roughly 60 documented error responses. Mechanical, large diff, low risk, and
it should land *after* A2 so the messages being moved are already sanitised.

**C3. Nil slices → empty arrays** (API-033, U-10's sibling). Initialise every
response slice with `make([]T, 0)` at the ~15 sites in T5 and drop `nullable:
true` from the spec. Contract change, mechanical, independent.

**C4. Replace the raw Google event with the CalendarEvent DTO** (API-017, U-04,
U-14). Route `PATCH /api/events/{id}`, `POST /api/schedule/create` and
`POST /api/nlp/confirm` through `toCalendarEventDTO`, add the `PATCH` analogue of
`TestToCalendarEventDTO_JSONShape`, and delete the `GoogleCalendarEvent` schema.
Contract change. Independent of C1–C3.

**C5. Silent-success writes** (API-020, T6). Add `RowsAffected` checks to
`RespondToHostInvite` and `RemoveLinkHost`, return `404` when nothing matched,
and — separately in the same PR — deprecate the duplicate
`/host-invites/{id}/accept|decline` spellings whose `{id}` semantics cause the
mistake (API-069). Contract change (new 404).

### Batch D — frontend-side alignment, after the backend shape is settled

**D1. Settings adapter** (API-013, API-039). Either extend `normalizeSettings`/
`settingsRequestBody` to cover every field, or — better — delete the adapter and
change the frontend `Settings` type to camelCase to match the backend. Must land
*after* B3, or it will be rewritten twice. Frontend, plus a backend enum
reconciliation for `conferencingProvider` and `LLMProvider`.

**D2. Focus and compression field alignment** (API-015, API-016). Frontend types
follow whatever C1 settles. Frontend only, blocked on C1.

**D3. Bookings list DTO** (API-019). Map `storage.Booking` through a DTO with
`start`/`end`/`title`/`duration_minutes`/`hosts` — the shape the frontend already
expects and the public confirmation endpoint already emits. Backend change is
cleaner than changing the frontend, because it makes the two booking payloads
consistent. Contract change.

**D4. Teams and manager payload gaps** (API-021, API-022). Add `team_id`,
`expires_at` and `inviter_email` to the invite preview response; add
`is_paceday_user` and `data_available` to `memberAnalytics`, and stop the
frontend normalizer defaulting `data_available` to `true`. Fix
`teamInvites.test.ts`, which currently asserts a fixture the backend never
produces. Backend and frontend. Contract change.

**D5. Auth flow repairs** (API-010, API-011, API-012, API-061, API-063,
API-064, API-065). One coherent item: make the frontend read `redirect_url`
instead of the never-sent `domain`, honour `login_hint`, make `issueJWT` failure
redirect to an error page rather than a success page, stop `disconnect` from
clearing the session cookie, return `204` from logout, and settle on one spelling
of the avatar field. Backend and frontend. Partly a contract change.

### Independent, not batched

**E1. Validation and status-code corrections** (API-024, API-035, API-036,
API-034, API-053, API-077). Each is a few lines in a different handler; they
share no code, only a theme. Best handled as a single chore PR or left to
opportunistic cleanup. Contract change (documented status codes move).

**E2. Resolve the `x-uncertain` register** (section 5). Nine of the 23 are
answered by resolving a finding above; six more need only reading one file
(U-12, U-13, U-17, U-23, U-20, U-11); five need an observed response or an
integration test against testcontainers (U-15, U-16, U-19, U-04, U-22), which the
repo is already set up for; and three need a product decision (U-03, U-07, U-14).
The five DB-formatting questions — `NoMeetingZone` times, `recapSendTime`, and
the booking `window_start`/`window_end` — are all the same question about
Postgres `TIME` rendering and **one integration test answers all five**.

**E3. Decide the fate of the unconsumed surfaces** (API-091, U-03). 22 habits
operations, 4 daily-recap endpoints, `GET /api/calendar/freebusy`,
`GET /api/org/members`, `POST /api/conference/create`. Either build the frontend
for them or delete them; publishing a contract for surfaces nobody calls and
nobody tests is how API-001 stayed hidden. A product decision, not an engineering
one, but it gates how much of C1–C3 is worth doing.

**E4. Auth middleware and rate limiter** (API-008, API-062, API-068). Prefer the
`Authorization` header over the cookie in `requireAuth`; key the detect limiter on
the trusted proxy hop instead of a client-supplied header and give it eviction;
inject the health version at build time. Backend only, no contract change.

**E5. Scheduling-link lifecycle** (API-049, API-050, API-051, API-073). Make the
three read paths agree about `active = false`, make alias precedence identical
between create and PATCH, decide whether `uses_count` is stored or computed, and
delete the dead code in `createBooking`. Contract change.

**E6. Room contract and unreachable freebusy states** (API-040, API-041, API-046,
API-047, API-074, API-075, API-078). Decide whether `Room` grows `email`/
`capacity`/`available` or the frontend type shrinks to `{id, name}`; delete
`ParticipantCoverage.provider` and `CoverageStatus: "paceday_user"` or implement
them. Backend and frontend, contract change.

**E7. Analytics field consistency** (API-055, API-056, API-058, API-083). One
`week_start` format across `/analytics/week` and `/trends`, one unit convention
for `habit_completion_rate` vs `percentage`, one ownership-failure status code,
one ack status code. Backend only, contract change.

### What is safe without a contract change

A1, A2, E4, D2, and the server-side halves of E1 (returning 400 instead of 200 for
malformed JSON arguably *is* a contract change, but only to a status code nothing
currently relies on). Everything else moves a documented wire shape. Since the
contract has not been published yet, "requires a contract change" here means
"regenerate `openapi.yaml` and re-check the frontend", not "version the API" —
which is a strong argument for landing C1, C2, C3 and C4 **before** the spec is
published rather than after.
