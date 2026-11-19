# Endpoint migration register

<!-- The rows are generated from contracts/openapi/openapi.yaml by
     scripts/openapi_migration_report.py. The `status` and `notes` columns are
     yours: they are read back and preserved on every regeneration. Run
     `make openapi` after changing the contract, `make openapi-check` to gate. -->

Every operation in the contract is either **handwritten** (a hand-written
handler in `backend/api/`, verified against the contract but not generated from
it) or **generated** (the handler implements the generated
`StrictServerInterface` method from `backend/api/gen/`).

## The one-way rule

`docs/factory/README.md` §4: *new and modified endpoints use the generated
server interface; untouched endpoints are verified against the contract but not
regenerated.* A big-bang migration of every operation would be a rewrite with no
test coverage to catch what it broke.

So a row moves `handwritten` → `generated` and never back. `make openapi-check`
compares this file against its committed version and fails if the number of
`generated` rows went down. If a migration genuinely has to be reverted, say so
in the PR body — the gate is there to make that a decision, not an accident.

## Moving a row

1. Change the handler so it implements the `StrictServerInterface` method for
   that operationId and is mounted through the generated wrapper.
2. Keep the existing handler tests passing — they are the only proof the
   migration preserved behaviour.
3. Flip the row's `status` to `generated` here, and put the issue key in
   `notes`.
4. `make openapi` — it re-renders this table, keeping the status and notes you
   just wrote, and fixes the column alignment your edit changed. Editing the
   cell by hand and skipping this step is reported as drift.
5. `make verify`.

Do not edit the operationId, method, path or tag columns: they are regenerated
from the contract and any edit is reported as drift.

## Progress

- operations: **134**
- generated: **14**
- handwritten: **120**

## Register

| operationId                       | method | path                                            | tag              | status      | notes |
|-----------------------------------|--------|-------------------------------------------------|------------------|-------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| listAnalyticsMeetings             | GET    | /api/analytics/meetings                         | analytics        | handwritten | |
| recomputeAnalytics                | POST   | /api/analytics/recompute                        | analytics        | handwritten | |
| listAnalyticsTrends               | GET    | /api/analytics/trends                           | analytics        | handwritten | PAC-42: response modified, kept handwritten. Moving it is a contract change: the generated binder answers a malformed `weeks` with 400, which this operation does not declare, where the handler falls back (to 8 outside 1..52, spec PAC-42 section 6). |
| getAnalyticsWeek                  | GET    | /api/analytics/week                             | analytics        | handwritten | PAC-42: response modified, kept handwritten. Moving it is a contract change: the generated binder answers a malformed `date` with 400, which this operation does not declare, where the handler falls back (to today). |
| listSsoProviders                  | GET    | /api/admin/sso                                  | auth             | generated   | PAC-22: response modified; implemented as generatedServer.ListSsoProviders (backend/api/handlers_sso.go), mounted through the generated strict wrapper. |
| createSsoProvider                 | POST   | /api/admin/sso                                  | auth             | handwritten | PAC-22: request and response modified, kept handwritten. Moving it is a contract change: the generated binder cannot tell an absent `oidc_client_secret` from a `null` one (both decode to nil; the contract keeps the first and refuses the second), never sees the SAML properties the contract refuses by name, and answers an undecodable body with its own message instead of `invalid JSON`. |
| startSsoAdminStepup               | GET    | /api/admin/sso/stepup                           | auth             | handwritten | |
| ssoAdminStepupCallback            | GET    | /api/admin/sso/stepup/callback                  | auth             | handwritten | |
| getSsoStepupOptions               | GET    | /api/admin/sso/stepup/options                   | auth             | generated   | PAC-50: new (contract revision 8); implemented as generatedServer.GetSsoStepupOptions (backend/api/handlers_sso_stepup.go), mounted through the generated strict wrapper. |
| deleteSsoProvider                 | DELETE | /api/admin/sso/{domain}                         | auth             | handwritten | |
| setSsoProviderState               | PUT    | /api/admin/sso/{domain}/state                   | auth             | handwritten | PAC-22: a new operation, so its implementation uses the generated server interface (factory §4) and flips this row to `generated` in the same PR. `handwritten` here only means no handler exists yet. |
| googleOAuthCallback               | GET    | /api/auth/callback                              | auth             | handwritten | PAC-71: modified (refusal redirect, connect branch), kept handwritten. Moving it is a contract change: it branches on which state cookie matches and answers text/plain 400, JSON 500 and three cookie-dependent 302s, like the other two sign-in callbacks (PAC-35 precedent). |
| ssoOidcCallback                   | GET    | /api/auth/callback/oidc/{domain}                | auth             | handwritten | PAC-71: modified (identity binding, refusal redirect), kept handwritten for the same reason as googleOAuthCallback. |
| detectAuthProvider                | POST   | /api/auth/detect                                | auth             | handwritten | |
| disconnectCalendar                | DELETE | /api/auth/disconnect                            | auth             | handwritten | |
| startGoogleOAuth                  | GET    | /api/auth/google                                | auth             | handwritten | |
| startGoogleConnect                | POST   | /api/auth/google/connect                        | auth             | generated   | PAC-71: new; implemented as generatedServer.StartGoogleConnect (backend/api/handlers_pac71_auth.go), mounted through the generated strict wrapper. |
| logout                            | POST   | /api/auth/logout                                | auth             | handwritten | |
| getCurrentUser                    | GET    | /api/auth/me                                    | auth             | handwritten | |
| startMicrosoftOAuth               | GET    | /api/auth/microsoft                             | auth             | handwritten | |
| microsoftOAuthCallback            | GET    | /api/auth/microsoft/callback                    | auth             | handwritten | PAC-71: modified (tenant check, refusal redirect, connect branch), kept handwritten for the same reason as googleOAuthCallback. |
| startMicrosoftConnect             | POST   | /api/auth/microsoft/connect                     | auth             | generated   | PAC-71: new; implemented as generatedServer.StartMicrosoftConnect (backend/api/handlers_pac71_auth.go), mounted through the generated strict wrapper. |
| disconnectMicrosoft               | POST   | /api/auth/microsoft/disconnect                  | auth             | generated   | PAC-67: new; implemented as generatedServer.DisconnectMicrosoft (backend/api/handlers_credentials.go), mounted through the generated strict wrapper. |
| startSsoLogin                     | GET    | /api/auth/sso/{domain}                          | auth             | handwritten | |
| getAuthStatus                     | GET    | /api/auth/status                                | auth             | handwritten | |
| getHealth                         | GET    | /api/health                                     | auth             | handwritten | |
| getIntegrationAvailability        | GET    | /api/integrations/availability                  | auth             | handwritten | |
| getPublicLinkInfo                 | GET    | /api/book/{slug}                                | booking          | handwritten | |
| createPublicBooking               | POST   | /api/book/{slug}                                | booking          | handwritten | |
| getPublicBookingSlots             | GET    | /api/book/{slug}/slots                          | booking          | handwritten | PAC-88: query and response modified, kept handwritten. Moving it is a contract change: the generated binder validates `date`, `from`, `to` and `display_time_zone` (format, pattern) before the handler runs, so a malformed query would answer the binder's 400 ahead of the per-client 429, the 404/410 and the link-zone 500, and with the binder's own message, where the contract pins the rate limit first and one message per check. |
| suggestAttendees                  | GET    | /api/attendees/suggest                          | calendar         | handwritten | |
| listAuditEntries                  | GET    | /api/audit                                      | calendar         | handwritten | PAC-23: moving this row to `generated` is a CONTRACT change. The generated binder answers an empty, non-integer, int64-overflowing or repeated `limit` with 400, which this operation does not declare. Declare the 400 in the same PR — see `listAuditEntries.parameters.limit` in contracts/openapi/paths/calendar.yaml. The binder does not enforce minimum/maximum. |
| listCalendarEvents                | GET    | /api/calendar/events                            | calendar         | handwritten | |
| getCalendarFreeBusy               | GET    | /api/calendar/freebusy                          | calendar         | handwritten | PAC-133: 200 reshaped (RawFreeBusyAnswer), 400 `end must be after start` added, kept handwritten. Moving it is a contract change: the generated wrapper binds `start`/`end` as date-times and answers its own 400 text before the handler, and that text and its JSON-vs-plain-text shape differ from `invalid start param` / `invalid end param` (contract PAC-133 §2.2). |
| deleteEvent                       | DELETE | /api/events/{id}                                | calendar         | handwritten | |
| patchEvent                        | PATCH  | /api/events/{id}                                | calendar         | handwritten | |
| queryFreeBusy                     | POST   | /api/freebusy                                   | calendar         | handwritten | PAC-133: 400 `end_time must be after start_time` added, coverage and cache rules restated, kept handwritten. Moving it is a contract change: the generated wrapper decodes the body before the handler, so `invalid JSON` and the order of the 400s would change (contract PAC-133 §2.1). |
| listOrgMembers                    | GET    | /api/org/members                                | calendar         | handwritten | |
| listPersonalCalendars             | GET    | /api/personal-calendars                         | calendar         | handwritten | |
| createPersonalCalendar            | POST   | /api/personal-calendars                         | calendar         | handwritten | |
| deletePersonalCalendar            | DELETE | /api/personal-calendars/{id}                    | calendar         | handwritten | |
| updatePersonalCalendar            | PATCH  | /api/personal-calendars/{id}                    | calendar         | handwritten | |
| previewPersonalCalendar           | GET    | /api/personal-calendars/{id}/preview            | calendar         | handwritten | |
| syncPersonalCalendar              | POST   | /api/personal-calendars/{id}/sync               | calendar         | handwritten | |
| listRooms                         | GET    | /api/rooms                                      | calendar         | handwritten | |
| startZoomOAuth                    | GET    | /api/auth/zoom                                  | conferencing     | handwritten | |
| handleZoomOAuthCallback           | GET    | /api/auth/zoom/callback                         | conferencing     | handwritten | |
| createConferenceLink              | POST   | /api/conference/create                          | conferencing     | generated   | PAC-67: generatedServer.CreateConferenceLink (backend/api/handlers_credentials.go); body validation (API-052) and credential renewal. |
| listConferenceProviders           | GET    | /api/conference/providers                       | conferencing     | handwritten | |
| disconnectZoom                    | POST   | /api/conference/zoom/disconnect                 | conferencing     | generated   | PAC-67: generatedServer.DisconnectZoom (backend/api/handlers_credentials.go); 200 ProviderDisconnectResult with revocation at Zoom. |
| removeEventConference             | DELETE | /api/events/{id}/conference                     | conferencing     | handwritten | |
| addEventConference                | POST   | /api/events/{id}/conference                     | conferencing     | generated   | PAC-67: generatedServer.AddEventConference (backend/api/handlers_credentials.go); credential renewal and the connection-lost 400s. |
| getDailyRecapSettings             | GET    | /api/settings/daily-recap                       | daily-recap      | handwritten | |
| patchDailyRecapSettings           | PATCH  | /api/settings/daily-recap                       | daily-recap      | handwritten | |
| previewDailyRecap                 | POST   | /api/settings/daily-recap/preview               | daily-recap      | handwritten | |
| sendDailyRecapTest                | POST   | /api/settings/daily-recap/test                  | daily-recap      | handwritten | |
| clearFocusBlocks                  | DELETE | /api/focus/blocks                               | focus            | handwritten | |
| listFocusBlocks                   | GET    | /api/focus/blocks                               | focus            | handwritten | |
| runFocus                          | POST   | /api/focus/run                                  | focus            | handwritten | |
| listHabits                        | GET    | /api/habits                                     | habits           | handwritten | |
| createHabit                       | POST   | /api/habits                                     | habits           | handwritten | |
| reoptimizeHabits                  | POST   | /api/habits/reoptimize                          | habits           | handwritten | |
| listHabitTemplates                | GET    | /api/habits/templates                           | habits           | handwritten | |
| deactivateHabit                   | DELETE | /api/habits/{id}                                | habits           | handwritten | |
| updateHabit                       | PATCH  | /api/habits/{id}                                | habits           | handwritten | |
| listHabitOccurrences              | GET    | /api/habits/{id}/occurrences                    | habits           | handwritten | |
| updateHabitOccurrenceStatus       | PATCH  | /api/habits/{id}/occurrences/{occurrenceId}     | habits           | handwritten | |
| disconnectNotion                  | DELETE | /api/integrations/notion                        | integrations     | handwritten | |
| notionOauthCallback               | GET    | /api/integrations/notion/callback               | integrations     | handwritten | |
| connectNotion                     | GET    | /api/integrations/notion/connect                | integrations     | handwritten | |
| getNotionStatus                   | GET    | /api/integrations/notion/status                 | integrations     | handwritten | |
| disconnectSlack                   | DELETE | /api/integrations/slack                         | integrations     | handwritten | |
| slackOauthCallback                | GET    | /api/integrations/slack/callback                | integrations     | handwritten | |
| connectSlack                      | GET    | /api/integrations/slack/connect                 | integrations     | handwritten | |
| getSlackStatus                    | GET    | /api/integrations/slack/status                  | integrations     | handwritten | |
| testLLMConnection                 | POST   | /api/llm/test                                   | llm              | handwritten | |
| getManagerAnalytics               | GET    | /api/manager/analytics                          | manager          | handwritten | PAC-129: query and response modified (`include_added`), kept handwritten. Moving it is a contract change: the generated binder answers a malformed `team_id` with its own 400 text instead of "team_id must be a valid UUID". PAC-130: behaviour modified (visibility rule and three branches for `is_paceday_user` and figures; 500 on a failed visibility read), kept handwritten. |
| previewManagerDetection           | POST   | /api/manager/detect                             | manager          | handwritten | PAC-130: behaviour modified (detection looks up no account and stores no link), kept handwritten. |
| confirmManagerDetection           | POST   | /api/manager/detect/confirm                     | manager          | handwritten | |
| getManagerCadenceGaps             | GET    | /api/manager/gaps                               | manager          | handwritten | |
| getManagerProfile                 | GET    | /api/manager/profile                            | manager          | handwritten | |
| setManagerProfile                 | POST   | /api/manager/profile                            | manager          | handwritten | |
| getManagerRoster                  | GET    | /api/manager/team                               | manager          | handwritten | PAC-129: query and response modified (`include_added`, source "added"), kept handwritten. Moving it is a contract change: the generated binder answers a malformed `team_id` with its own 400 text instead of "team_id must be a valid UUID", and a missing `team_id` with its own text instead of "team_id is required". PAC-130: behaviour modified (visibility rule and three branches; 500 on a failed visibility read), kept handwritten. |
| addManagerTeamMember              | POST   | /api/manager/team/members                       | manager          | handwritten | PAC-130: behaviour modified (no account lookup; `member_user_id` always null, deprecated), kept handwritten. Moving it is a contract change: the generated binder answers a malformed `team_id` with its own 400 text instead of "team_id must be a valid UUID". |
| removeManagerTeamMember           | DELETE | /api/manager/team/members/{email}               | manager          | handwritten | PAC-129: behaviour modified (one transaction under the team lock; added entry and every invitation deleted), kept handwritten. Moving it is a contract change: the generated binder answers a malformed `team_id` with its own 400 text instead of "team_id must be a valid UUID". |
| patchManagerTeamMember            | PATCH  | /api/manager/team/members/{email}               | manager          | handwritten | PAC-129: response modified (422 for an added person), kept handwritten. Moving it is a contract change: the generated binder answers a malformed `team_id` with its own 400 text instead of "team_id must be a valid UUID", and an undecodable body with "invalid body" instead of "invalid JSON". PAC-130: response modified (`member_user_id` always null, deprecated; no link written), kept handwritten. |
| scheduleManagerOneOnOne           | POST   | /api/manager/team/members/{email}/schedule      | manager          | handwritten | PAC-129: response modified (422 for an added person), kept handwritten. Moving it is a contract change: the generated binder answers a malformed `team_id` with its own 400 text instead of "team_id must be a valid UUID"; it also tolerates an empty body (io.EOF), which the wrapper would refuse. PAC-130: behaviour modified (the formal-member materialisation writes no account link; answer unchanged), kept handwritten. |
| getMeetingBrief                   | GET    | /api/meetings/{event_id}/brief                  | meeting-briefs   | handwritten | |
| refreshMeetingBrief               | POST   | /api/meetings/{event_id}/brief/refresh          | meeting-briefs   | handwritten | |
| confirmNaturalLanguage            | POST   | /api/nlp/confirm                                | nlp              | handwritten | |
| parseNaturalLanguage              | POST   | /api/nlp/parse                                  | nlp              | handwritten | PAC-133: `error` semantics on a failed suggestion step, description only, kept handwritten. |
| getReadiness                      | GET    | /api/ready                                      | ops              | generated   | PAC-81 Part A: new; implemented as generatedServer.GetReadiness (backend/api/readiness.go), mounted through the generated strict wrapper. |
| getOrganizationAI                 | GET    | /api/organization/ai                            | organization-ai  | generated   | NEW-llm-bring-your-own Part 2 (PAC-73): served through the generated strict wrapper (backend/api/handlers_org_ai.go). |
| patchOrganizationAI               | PATCH  | /api/organization/ai                            | organization-ai  | generated   | NEW-llm-bring-your-own Part 2 (PAC-73): served through the generated strict wrapper (backend/api/handlers_org_ai.go). |
| confirmOrganizationAI             | POST   | /api/organization/ai/consent                    | organization-ai  | generated   | NEW-llm-bring-your-own Part 2 (PAC-73): served through the generated strict wrapper (backend/api/handlers_org_ai.go). |
| previewCompression                | POST   | /api/schedule/compress                          | schedule         | handwritten | PAC-133: 503 listed, attendee rule restated, kept handwritten. Moving it is a contract change: the optional/`null` body handling (PAC-36) is the handler's own and the generated wrapper would refuse it differently. |
| applyCompression                  | POST   | /api/schedule/compress/apply                    | schedule         | handwritten | PAC-133: new failed reason `attendee availability unverified`, description only, kept handwritten (only the engine changes). |
| createScheduledMeeting            | POST   | /api/schedule/create                            | schedule         | handwritten | |
| suggestMeetingSlots               | POST   | /api/schedule/suggest                           | schedule         | handwritten | PAC-133: `unknown_attendees` added (required), 500 restated, kept handwritten. Moving it is a contract change: the generated wrapper would refuse unknown members and a missing range before the handler, with its own 400 text. |
| listSchedulingLinks               | GET    | /api/scheduling-links                           | scheduling-links | handwritten | PAC-149: response meaning modified (roster entries by status, contract PAC-149 §2.5); kept handwritten, the shared roster builder serves five operations and moves with them. |
| createSchedulingLink              | POST   | /api/scheduling-links                           | scheduling-links | handwritten | PAC-112: request and response modified, kept handwritten. Moving it is a contract change: the generated decoder cannot express the per-entry ECMA-262 trim, the blank-entry 422 or the not_added outcome for a non-address entry (contract PAC-112 §3.4). PAC-119: Idempotency-Key header and 409 oneOf added, kept handwritten. Moving it is a contract change: the generated binder cannot express the key 400 before the body decode, an empty header treated as absent, or the 400 for the header on two field lines (contract PAC-119 §1.1). PAC-149: co-host outcomes by address, roster and report shapes modified; kept handwritten for the PAC-112 and PAC-119 reasons above. |
| listArchivedSchedulingLinks       | GET    | /api/scheduling-links/archive                   | scheduling-links | handwritten | |
| getArchivedSchedulingLink         | GET    | /api/scheduling-links/archive/{id}              | scheduling-links | handwritten | |
| listSchedulingLinkHostInvites     | GET    | /api/scheduling-links/host-invites              | scheduling-links | handwritten | PAC-149: modified (holder match on the caller's own address, 500 on a failed match); kept handwritten with its deprecated alias, which must stay byte-identical (spec D-12). |
| acceptSchedulingLinkHostInvite    | POST   | /api/scheduling-links/host-invites/{id}/accept  | scheduling-links | handwritten | PAC-149: modified (address invitations, holder match); kept handwritten: the generated wrapper parses `{id}` and the body before the handler runs, so a non-UUID id would get the wrapper's 400 text instead of "invalid id" (contract PAC-120 §1.1 reasoning); moving it is a contract change. |
| declineSchedulingLinkHostInvite   | POST   | /api/scheduling-links/host-invites/{id}/decline | scheduling-links | handwritten | PAC-149: modified (as accept); kept handwritten: the generated wrapper parses `{id}` and the body before the handler runs, so a non-UUID id would get the wrapper's 400 text instead of "invalid id" (contract PAC-120 §1.1 reasoning); moving it is a contract change. |
| listSchedulingLinkInvitesAlias    | GET    | /api/scheduling-links/invites                   | scheduling-links | handwritten | PAC-149: deprecated (API-069); identical to listSchedulingLinkHostInvites until removed. |
| deleteSchedulingLink              | DELETE | /api/scheduling-links/{id}                      | scheduling-links | handwritten | PAC-149: modified (pending invitations erased in the delete's write); kept handwritten: the generated wrapper parses `{id}` and the body before the handler runs, so a non-UUID id would get the wrapper's 400 text instead of "invalid id" (contract PAC-120 §1.1 reasoning); moving it is a contract change. |
| getSchedulingLink                 | GET    | /api/scheduling-links/{id}                      | scheduling-links | handwritten | PAC-149: response meaning modified (roster entries by status); kept handwritten: the generated wrapper parses `{id}` and the body before the handler runs, so a non-UUID id would get the wrapper's 400 text instead of "invalid id" (contract PAC-120 §1.1 reasoning); moving it is a contract change. |
| updateSchedulingLink              | PATCH  | /api/scheduling-links/{id}                      | scheduling-links | handwritten | PAC-112: request and response modified, kept handwritten. Moving it is a contract change: the generated decoder cannot express the per-entry ECMA-262 trim, the blank-entry 422 or the not_added outcome for a non-address entry (contract PAC-112 §3.4). PAC-149: co-host outcomes by address, roster and report shapes modified; kept handwritten for the PAC-112 reason above. |
| acceptSchedulingLinkInviteByLink  | POST   | /api/scheduling-links/{id}/accept               | scheduling-links | handwritten | PAC-149: deprecated (API-069); same handler as acceptSchedulingLinkHostInvite. |
| listSchedulingLinkBookings        | GET    | /api/scheduling-links/{id}/bookings             | scheduling-links | handwritten | |
| declineSchedulingLinkInviteByLink | POST   | /api/scheduling-links/{id}/decline              | scheduling-links | handwritten | PAC-149: deprecated (API-069); same handler as declineSchedulingLinkHostInvite. |
| inviteSchedulingLinkHost          | POST   | /api/scheduling-links/{id}/hosts                | scheduling-links | handwritten | PAC-120: responses modified (422 co-host limit added, 500 cases restated), kept handwritten. Moving it is a contract change: the generated wrapper decodes the body before the handler runs, so a malformed body would be refused before the link read, the 404 and the 403, and with the wrapper's own 400 text instead of "email is required" (contract PAC-120 §1.1). PAC-149: responses modified (200/201 are SchedulingLinkHost, 404 "user not found" removed, 400 for a malformed address); kept handwritten for the PAC-120 reason above. |
| leaveSchedulingLink               | POST   | /api/scheduling-links/{id}/leave                | scheduling-links | handwritten | PAC-149: modified (leave a held pending invitation); kept handwritten: the generated wrapper parses `{id}` and the body before the handler runs, so a non-UUID id would get the wrapper's 400 text instead of "invalid id" (contract PAC-120 §1.1 reasoning); moving it is a contract change. |
| getSettings                       | GET    | /api/settings                                   | settings         | handwritten | |
| patchSettings                     | PATCH  | /api/settings                                   | settings         | handwritten | |
| listTeams                         | GET    | /api/teams                                      | teams            | handwritten | |
| createTeam                        | POST   | /api/teams                                      | teams            | handwritten | |
| getTeamInvite                     | GET    | /api/teams/invites/{token}                      | teams            | handwritten | |
| acceptTeamInvite                  | POST   | /api/teams/invites/{token}/accept               | teams            | handwritten | PAC-129: responses modified (422 team full, 404 after a removal), kept handwritten. Its statuses and their order are unchanged apart from the 422; the PAC-71 interleave tests pin the hand-written handler (`api/pac71_interleave_test.go`), and the move is left to a change with no behaviour in it. |
| deleteTeam                        | DELETE | /api/teams/{id}                                 | teams            | handwritten | |
| getTeam                           | GET    | /api/teams/{id}                                 | teams            | handwritten | PAC-129: response modified (`people`, the 500 on a failed member read), kept handwritten. The PAC-71 CONTRACT-9 seam between its two reads (`getTeamAfterMembershipHook`) lives in the hand-written handler; the move is left to a change with no behaviour in it. |
| renameTeam                        | PATCH  | /api/teams/{id}                                 | teams            | handwritten | |
| getFormalTeamAnalytics            | GET    | /api/teams/{id}/analytics                       | teams            | handwritten | PAC-129: response modified (`week_start`, `people`, averages, 503), kept handwritten. Moving it is a contract change: the generated binder validates `date` before the handler runs and answers with its own 400 text, ahead of the 403. |
| getTeamAvailability               | GET    | /api/teams/{id}/availability                    | teams            | handwritten | PAC-129: response modified (slot counts), kept handwritten. Moving it is a contract change: the generated binder validates `date` and `duration` before the handler runs and answers with its own 400 text, where the contract pins "date must be YYYY-MM-DD" and "duration must be a positive integer (minutes)" after the 403. |
| inviteTeamMember                  | POST   | /api/teams/{id}/members/invite                  | teams            | handwritten | PAC-129: request and responses modified (name, outcome body, 200, cap 422), kept handwritten. Moving it is a contract change: the generated wrapper decodes the body before the handler runs and answers an undecodable body with "invalid body", where this operation answers "email is required" (contract PAC-129 §3.3). |
| removeTeamMember                  | DELETE | /api/teams/{id}/members/{userId}                | teams            | handwritten | |
| listNoMeetingZones                | GET    | /api/teams/{id}/no-meeting-zones                | teams            | handwritten | |
| createNoMeetingZone               | POST   | /api/teams/{id}/no-meeting-zones                | teams            | handwritten | |
| deleteNoMeetingZone               | DELETE | /api/teams/{id}/no-meeting-zones/{zoneId}       | teams            | handwritten | |
| updateNoMeetingZone               | PATCH  | /api/teams/{id}/no-meeting-zones/{zoneId}       | teams            | handwritten | |
| addTeamPerson                     | POST   | /api/teams/{id}/people                          | teams            | generated   | PAC-129: generatedServer.AddTeamPerson (backend/api/handlers_team_people.go), mounted by teamHandlers.addTeamPerson, which answers 400 "invalid team id" first. |
| removeTeamPerson                  | DELETE | /api/teams/{id}/people/{email}                  | teams            | generated   | PAC-129: generatedServer.RemoveTeamPerson (backend/api/handlers_team_people.go), mounted by teamHandlers.removeTeamPerson, which answers 400 "invalid team id" first and path-unescapes {email}; the handler normalises it (400 "invalid email"). |
