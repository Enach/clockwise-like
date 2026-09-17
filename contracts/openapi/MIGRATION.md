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

- operations: **119**
- generated: **0**
- handwritten: **119**

## Register

| operationId                       | method | path                                            | tag              | status      | notes |
|-----------------------------------|--------|-------------------------------------------------|------------------|-------------|-------|
| listAnalyticsMeetings             | GET    | /api/analytics/meetings                         | analytics        | handwritten | |
| recomputeAnalytics                | POST   | /api/analytics/recompute                        | analytics        | handwritten | |
| listAnalyticsTrends               | GET    | /api/analytics/trends                           | analytics        | handwritten | |
| getAnalyticsWeek                  | GET    | /api/analytics/week                             | analytics        | handwritten | |
| listSsoProviders                  | GET    | /api/admin/sso                                  | auth             | handwritten | |
| createSsoProvider                 | POST   | /api/admin/sso                                  | auth             | handwritten | |
| deleteSsoProvider                 | DELETE | /api/admin/sso/{domain}                         | auth             | handwritten | |
| googleOAuthCallback               | GET    | /api/auth/callback                              | auth             | handwritten | |
| ssoOidcCallback                   | GET    | /api/auth/callback/oidc/{domain}                | auth             | handwritten | |
| detectAuthProvider                | POST   | /api/auth/detect                                | auth             | handwritten | |
| disconnectCalendar                | DELETE | /api/auth/disconnect                            | auth             | handwritten | |
| startGoogleOAuth                  | GET    | /api/auth/google                                | auth             | handwritten | |
| logout                            | POST   | /api/auth/logout                                | auth             | handwritten | |
| getCurrentUser                    | GET    | /api/auth/me                                    | auth             | handwritten | |
| startMicrosoftOAuth               | GET    | /api/auth/microsoft                             | auth             | handwritten | |
| microsoftOAuthCallback            | GET    | /api/auth/microsoft/callback                    | auth             | handwritten | |
| startSsoLogin                     | GET    | /api/auth/sso/{domain}                          | auth             | handwritten | |
| getAuthStatus                     | GET    | /api/auth/status                                | auth             | handwritten | |
| getHealth                         | GET    | /api/health                                     | auth             | handwritten | |
| getIntegrationAvailability        | GET    | /api/integrations/availability                  | auth             | handwritten | |
| getPublicLinkInfo                 | GET    | /api/book/{slug}                                | booking          | handwritten | |
| createPublicBooking               | POST   | /api/book/{slug}                                | booking          | handwritten | |
| getPublicBookingSlots             | GET    | /api/book/{slug}/slots                          | booking          | handwritten | |
| suggestAttendees                  | GET    | /api/attendees/suggest                          | calendar         | handwritten | |
| listAuditEntries                  | GET    | /api/audit                                      | calendar         | handwritten | |
| listCalendarEvents                | GET    | /api/calendar/events                            | calendar         | handwritten | |
| getCalendarFreeBusy               | GET    | /api/calendar/freebusy                          | calendar         | handwritten | |
| deleteEvent                       | DELETE | /api/events/{id}                                | calendar         | handwritten | |
| patchEvent                        | PATCH  | /api/events/{id}                                | calendar         | handwritten | |
| queryFreeBusy                     | POST   | /api/freebusy                                   | calendar         | handwritten | |
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
| createConferenceLink              | POST   | /api/conference/create                          | conferencing     | handwritten | |
| listConferenceProviders           | GET    | /api/conference/providers                       | conferencing     | handwritten | |
| disconnectZoom                    | POST   | /api/conference/zoom/disconnect                 | conferencing     | handwritten | |
| removeEventConference             | DELETE | /api/events/{id}/conference                     | conferencing     | handwritten | |
| addEventConference                | POST   | /api/events/{id}/conference                     | conferencing     | handwritten | |
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
| getManagerAnalytics               | GET    | /api/manager/analytics                          | manager          | handwritten | |
| previewManagerDetection           | POST   | /api/manager/detect                             | manager          | handwritten | |
| confirmManagerDetection           | POST   | /api/manager/detect/confirm                     | manager          | handwritten | |
| getManagerCadenceGaps             | GET    | /api/manager/gaps                               | manager          | handwritten | |
| getManagerProfile                 | GET    | /api/manager/profile                            | manager          | handwritten | |
| setManagerProfile                 | POST   | /api/manager/profile                            | manager          | handwritten | |
| getManagerRoster                  | GET    | /api/manager/team                               | manager          | handwritten | |
| addManagerTeamMember              | POST   | /api/manager/team/members                       | manager          | handwritten | |
| removeManagerTeamMember           | DELETE | /api/manager/team/members/{email}               | manager          | handwritten | |
| patchManagerTeamMember            | PATCH  | /api/manager/team/members/{email}               | manager          | handwritten | |
| scheduleManagerOneOnOne           | POST   | /api/manager/team/members/{email}/schedule      | manager          | handwritten | |
| getMeetingBrief                   | GET    | /api/meetings/{event_id}/brief                  | meeting-briefs   | handwritten | |
| refreshMeetingBrief               | POST   | /api/meetings/{event_id}/brief/refresh          | meeting-briefs   | handwritten | |
| confirmNaturalLanguage            | POST   | /api/nlp/confirm                                | nlp              | handwritten | |
| parseNaturalLanguage              | POST   | /api/nlp/parse                                  | nlp              | handwritten | |
| previewCompression                | POST   | /api/schedule/compress                          | schedule         | handwritten | |
| applyCompression                  | POST   | /api/schedule/compress/apply                    | schedule         | handwritten | |
| createScheduledMeeting            | POST   | /api/schedule/create                            | schedule         | handwritten | |
| suggestMeetingSlots               | POST   | /api/schedule/suggest                           | schedule         | handwritten | |
| listSchedulingLinks               | GET    | /api/scheduling-links                           | scheduling-links | handwritten | |
| createSchedulingLink              | POST   | /api/scheduling-links                           | scheduling-links | handwritten | |
| listSchedulingLinkHostInvites     | GET    | /api/scheduling-links/host-invites              | scheduling-links | handwritten | |
| acceptSchedulingLinkHostInvite    | POST   | /api/scheduling-links/host-invites/{id}/accept  | scheduling-links | handwritten | |
| declineSchedulingLinkHostInvite   | POST   | /api/scheduling-links/host-invites/{id}/decline | scheduling-links | handwritten | |
| listSchedulingLinkInvitesAlias    | GET    | /api/scheduling-links/invites                   | scheduling-links | handwritten | |
| deleteSchedulingLink              | DELETE | /api/scheduling-links/{id}                      | scheduling-links | handwritten | |
| getSchedulingLink                 | GET    | /api/scheduling-links/{id}                      | scheduling-links | handwritten | |
| updateSchedulingLink              | PATCH  | /api/scheduling-links/{id}                      | scheduling-links | handwritten | |
| acceptSchedulingLinkInviteByLink  | POST   | /api/scheduling-links/{id}/accept               | scheduling-links | handwritten | |
| listSchedulingLinkBookings        | GET    | /api/scheduling-links/{id}/bookings             | scheduling-links | handwritten | |
| declineSchedulingLinkInviteByLink | POST   | /api/scheduling-links/{id}/decline              | scheduling-links | handwritten | |
| inviteSchedulingLinkHost          | POST   | /api/scheduling-links/{id}/hosts                | scheduling-links | handwritten | |
| leaveSchedulingLink               | POST   | /api/scheduling-links/{id}/leave                | scheduling-links | handwritten | |
| getSettings                       | GET    | /api/settings                                   | settings         | handwritten | |
| patchSettings                     | PATCH  | /api/settings                                   | settings         | handwritten | |
| listTeams                         | GET    | /api/teams                                      | teams            | handwritten | |
| createTeam                        | POST   | /api/teams                                      | teams            | handwritten | |
| getTeamInvite                     | GET    | /api/teams/invites/{token}                      | teams            | handwritten | |
| acceptTeamInvite                  | POST   | /api/teams/invites/{token}/accept               | teams            | handwritten | |
| deleteTeam                        | DELETE | /api/teams/{id}                                 | teams            | handwritten | |
| getTeam                           | GET    | /api/teams/{id}                                 | teams            | handwritten | |
| renameTeam                        | PATCH  | /api/teams/{id}                                 | teams            | handwritten | |
| getFormalTeamAnalytics            | GET    | /api/teams/{id}/analytics                       | teams            | handwritten | |
| getTeamAvailability               | GET    | /api/teams/{id}/availability                    | teams            | handwritten | |
| inviteTeamMember                  | POST   | /api/teams/{id}/members/invite                  | teams            | handwritten | |
| removeTeamMember                  | DELETE | /api/teams/{id}/members/{userId}                | teams            | handwritten | |
| listNoMeetingZones                | GET    | /api/teams/{id}/no-meeting-zones                | teams            | handwritten | |
| createNoMeetingZone               | POST   | /api/teams/{id}/no-meeting-zones                | teams            | handwritten | |
| deleteNoMeetingZone               | DELETE | /api/teams/{id}/no-meeting-zones/{zoneId}       | teams            | handwritten | |
| updateNoMeetingZone               | PATCH  | /api/teams/{id}/no-meeting-zones/{zoneId}       | teams            | handwritten | |
