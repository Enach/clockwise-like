# habits.yaml — extraction notes

Domain: habits, analytics, meeting briefs, Slack/Notion integrations.
22 operations across 20 path items, 19 schemas.

Primary sources:
`backend/api/routes.go` (lines 133–195),
`backend/api/handlers_habits.go`, `handlers_analytics.go`, `handlers_meeting_briefs.go`,
`handlers_integrations.go`, `backend/api/middleware.go`, `writeError` in
`backend/api/handlers_settings.go:206`,
`backend/engine/{habits,analytics,meeting_brief_service}.go`,
`backend/storage/{habits,analytics,meeting_briefs,integrations}.go`,
`backend/api/handlers_habits_validation_test.go`.

`GET /api/integrations/availability` is intentionally excluded (public route registered
outside the auth group at `routes.go:52`, owned by another agent).

---

## (a) Backend / frontend mismatches

The headline finding is **absence, not disagreement**.

1. **Zero frontend coverage for this entire domain.** Exhaustive grep of
   `/home/claude/smart-calendar-flow/src` finds no call to any of the 22 operations.
   `src/api/client.ts` contains no `/habits`, `/analytics/week`, `/analytics/trends`,
   `/analytics/meetings`, `/analytics/recompute`, `/meetings/.../brief`,
   `/integrations/slack*` or `/integrations/notion*` request. `src/api/types.ts` defines no
   `Habit`, `HabitOccurrence`, `AnalyticsWeek` or `MeetingBrief` type. There are no
   `src/hooks/useHabits.ts` / `useAnalytics.ts` equivalents.
   Consequence: **the schemas in habits.yaml could not be cross-validated against a
   consumer.** They are derived from Go struct json tags alone. Treat field-level
   confidence as "backend-verified, consumer-unverified".

2. **Slack/Notion appear in the frontend only as availability flags.**
   `src/api/types.ts:9` declares
   `IntegrationProvider = "google" | "microsoft" | "zoom" | "slack" | "notion" | "webcal"`,
   and `client.ts:782-792` calls `GET /integrations/availability` with a demo fallback
   that reports `slack` and `notion` as `{available: true, reason: "demo"}`.
   So the UI can learn Slack/Notion *could* be configured, but it has no code path to
   connect, disconnect, or read connection status. The four `*/connect`, `*/callback`,
   `DELETE /api/integrations/{provider}` and `*/status` routes are currently unreachable
   from the SPA.

3. **`authConnectUrl` pattern is not reused for Slack/Notion.**
   `client.ts:794` builds OAuth start URLs as a raw string
   (`${API_BASE}/auth/${provider}`) rather than fetching them, because those routes
   redirect. The Slack/Notion connect routes need exactly the same treatment — but with a
   difference the frontend has not had to handle yet: `/api/auth/google` is registered as
   a **public** route, whereas `/api/integrations/slack/connect` sits **inside the
   `requireAuth` group** (`routes.go:181-191`). A plain `window.location` navigation only
   works if the `auth_token` cookie is present and sent; a Bearer-token-only client
   cannot start these flows at all.

4. **Both OAuth *callbacks* are also behind `requireAuth`.** `slackCallback` /
   `notionCallback` call `userIDFromCtx` and need a real user id, so the browser must
   still be carrying the `auth_token` cookie when the provider redirects back. Any
   `SameSite=Lax`/`Strict` cookie behaviour on that cross-site redirect turns this into a
   401 instead of a connection. Flagged as a backend design risk, not a spec ambiguity.

5. **Callback success redirects to a hard-coded SPA path.** Both callbacks redirect to
   `/app/settings?tab=integrations&{slack|notion}=connected` — a literal, not derived from
   the `frontendURL` config that `RegisterRoutes` receives and that the auth handlers use.
   The frontend has no route reading those query params today.

### Internal backend inconsistencies (no frontend involved)

6. **`week_start` has two different wire formats.**
   `GET /api/analytics/week` builds a `map[string]any` and formats it as `"2006-01-02"`
   (`handlers_analytics.go:137`), while `GET /api/analytics/trends` returns the raw
   `storage.AnalyticsWeek`, whose `WeekStart time.Time` marshals to RFC3339
   (`"2026-03-02T00:00:00Z"`). Same logical field, two types. Modelled as two distinct
   schemas: `AnalyticsWeekWithBreakdown` vs `AnalyticsWeekRow`.

7. **`breakdown` exists only on `/analytics/week`.** `/analytics/trends` cannot be used to
   render the same chart without the client recomputing percentages.

8. **`HabitOccurrence.scheduled_date` is a DATE column typed as `time.Time` in Go**, so it
   serialises as a full timestamp, unlike the analytics week field. A client asking for
   `?from=2026-03-01` gets back `"2026-03-01T00:00:00Z"`.

9. **Two incompatible error shapes.** `writeError` emits
   `{"error": "..."}` with `Content-Type: application/json`. The meeting-brief and
   integrations handlers use `http.Error`, which emits a bare plain-text line with
   `Content-Type: text/plain; charset=utf-8`. Any generic client error handler that
   assumes JSON will fail on every brief and integration error.

10. **The 401 from `requireAuth` is mislabelled.** `middleware.go` sets
    `Content-Type: application/json` and then calls `http.Error`, which *overwrites* it
    with `text/plain; charset=utf-8`. The body really is `{"error":"unauthorized"}`, but
    the declared media type contradicts it. Documented on `components/responses/Unauthorized`.

11. **Two ack endpoints, two status codes.** `POST /api/habits/reoptimize` → **200**
    `{"status":"reoptimization started"}`. `POST /api/analytics/recompute` → **202**
    `{"status":"recompute accepted"}`. Both do the same thing (detached goroutine).

12. **Ownership failures are inconsistent.** `PATCH`/`DELETE /api/habits/{id}` and
    `GET .../occurrences` return **403 `forbidden`** for another user's habit (existence
    is leaked). `PATCH .../occurrences/{occurrenceId}` scopes by user inside the SQL and
    returns **404 `not found`** instead.

13. **`GET /api/analytics/meetings` returns 500 for a user with no connected calendar.**
    `calOpsForUser` fails with `no token for user <uuid>` and the handler wraps it as
    `"internal error: ..."`. That user-id-bearing string is also echoed to the client.

14. **`buffer_minutes` is always 0.** The column round-trips through storage but
    `AnalyticsEngine.compute` never assigns `BufferMinutes` (see `engine/analytics.go:185-207`),
    even though `ComputeBufferBlocks` exists and is used by the habits engine.

15. **`habit_completion_rate` is a 0..1 fraction**, while `AnalyticsBreakdownEntry.percentage`
    is 0..100. Easy client-side bug.

16. **`NotionPage` field/tag drift.** Go field `LastEditedAt`, json tag `last_edited_time`.
    The tag wins on the wire.

---

## (b) Habit validation rules, test-ready

All of these live in `validateHabitFields(title, duration, days, windowStart, windowEnd, priority)`
(`handlers_habits.go:31-66`) and are shared by `POST /api/habits` and `PATCH /api/habits/{id}`.
Every failure is `400` with body `{"error": "<exact message below>"}`.

### Normalisation applied before validation

| Step | POST (create) | PATCH (update) |
|---|---|---|
| `title` | `strings.TrimSpace` | merged value trimmed |
| `days_of_week` | absent/`null` → `[1,2,3,4,5]` | absent/`null` → keep stored |
| `window_start` | absent/blank after trim → `"09:00"` | absent → keep stored, then trimmed |
| `window_end` | absent/blank after trim → `"17:00"` | absent → keep stored, then trimmed |
| `priority` | `== 0` → `50` | pointer; absent → keep stored, explicit `0` honoured |
| `color` | `== ""` → `"#5B7FFF"` | pointer; absent → keep stored |
| `active` | not settable | pointer; absent → keep stored |

### Check order (first failure wins — matters for tests asserting the message)

1. `strings.TrimSpace(title) == ""` → `title is required`
2. `len([]rune(trimmed title)) > 200` → `title must be 200 characters or fewer`
   (rune count, so 200 multi-byte characters pass)
3. `duration <= 0 || duration > 1440` → `duration_minutes must be between 1 and 1440`
   (1440 = 24*60 passes; 0, negative and 1441 fail)
4. `len(days) == 0` → `days_of_week must contain at least one weekday`
   (fires for both `nil` and `[]` once defaulting has run — on POST only an explicit `[]` reaches it)
5. per element, in slice order: `day < 1 || day > 7` → `days_of_week must use Monday=1 through Sunday=7`
   (0 and 8 fail; Sunday is 7, not 0)
6. per element, in slice order: already seen → `days_of_week must not contain duplicates`
   (note 5 is checked before 6 for a given element, so `[0,0]` reports the range message)
7. `!validHabitClock(window_start) || !validHabitClock(window_end)` →
   `window_start and window_end must be in HH:MM format`
   `validHabitClock` requires **exactly** `len == 5`, `value[2] == ':'`, and
   `time.Parse("15:04", value)` to succeed. So `"8:00"`, `"08:00:00"`, `"0800"`, `"24:00"`,
   `"08:60"`, `""` all fail; `"00:00"` and `"23:59"` pass. One message covers both fields.
8. `!end.After(start)` → `window_end must be after window_start`
   Both parsed on the same reference date, so equal bounds fail and an overnight window
   (`"22:00"`–`"06:00"`) is impossible.
9. `priority < 0 || priority > 100` → `priority must be between 0 and 100`
   (0 and 100 pass; -1 and 101 fail)

### Cases `handlers_habits_validation_test.go` already pins

Happy path: `("Morning walk", 30, [1,3,7], "08:00", "09:30", 50)` must pass.
Failing: empty title (`" "`), duration 1441, nil days, day `0`, days `[1,1]`,
window `"8:00"`, window `"10:00"`–`"09:00"`, priority 101.
The test asserts only "an error occurred", not the message — the messages above come from
reading the function and are safe to assert on.

### Handler-level 400s not covered by `validateHabitFields`

| Route | Trigger | Message |
|---|---|---|
| POST /api/habits | body is not valid JSON | `invalid request body` |
| PATCH /api/habits/{id} | `{id}` not a UUID | `invalid id` |
| PATCH /api/habits/{id} | body is not valid JSON | `invalid request body` |
| DELETE /api/habits/{id} | `{id}` not a UUID | `invalid id` |
| GET /api/habits/{id}/occurrences | `{id}` not a UUID | `invalid id` |
| GET /api/habits/{id}/occurrences | `from` present and not `YYYY-MM-DD` | `from must use YYYY-MM-DD` |
| GET /api/habits/{id}/occurrences | `to` present and not `YYYY-MM-DD` | `to must use YYYY-MM-DD` |
| GET /api/habits/{id}/occurrences | `from > to` | `from must be on or before to` |
| PATCH .../occurrences/{occurrenceId} | habit `{id}` not a UUID | `invalid habit id` |
| PATCH .../occurrences/{occurrenceId} | `{occurrenceId}` not a UUID | `invalid occurrence id` |
| PATCH .../occurrences/{occurrenceId} | body is not valid JSON | `invalid request body` |
| PATCH .../occurrences/{occurrenceId} | status not in {completed, scheduled} after trim+lowercase | `status must be completed or scheduled` |

Ordering traps worth a test each:
- On `PATCH /api/habits/{id}`, ownership is resolved **before** the body is decoded, so a
  malformed body against another user's habit yields **403**, not 400.
- On `GET .../occurrences`, an empty query value (`?from=`) is treated as absent and uses
  the default bound — it is not a 400.
- `?from`/`?to` bounds are **inclusive** on both ends.
- `uuid.Parse` also accepts unhyphenated, brace-wrapped and `urn:uuid:` forms, so those are
  *not* `invalid id`.

### Non-validations (assert these are accepted)

- `color` is never validated. Any string, including `"not-a-color"` or `""` on PATCH, is stored.
- `days_of_week` order is not normalised; `[7,1]` is stored as given.
- Explicit `"priority": 0` on **POST** silently becomes 50.
- Unknown JSON keys are ignored (no `DisallowUnknownFields` anywhere in this domain).
- `POST /api/habits/reoptimize` and `POST /api/analytics/recompute` never read the body,
  so an invalid or absent body is still a success.
- `POST /api/meetings/{event_id}/brief/refresh` never reads the body either.

---

## (c) `x-uncertain` list

Marked inline in `habits.yaml`:

1. `/api/analytics/week` → `404` — believed unreachable. `compute()` ends in
   `UpsertAnalyticsWeek`, whose `INSERT ... RETURNING` always yields a row or an error, so
   the `result == nil && err == nil` branch should not fire. Documented because the code
   path exists.
2. `/api/meetings/{event_id}/brief` → `400 "event_id required"` — believed unreachable,
   because chi never matches an empty `{event_id}` path segment. Same for the refresh route.
3. `AnalyticsWeekWithBreakdown.buffer_minutes` — always `0`; the engine never sets it.

Uncertainties not expressible as an inline `x-uncertain` on a single node:

4. **Response nullability for `top_meeting_titles` on `/analytics/trends`.** It is populated
   by a failure-swallowing `json.Unmarshal` into a nil slice, so a NULL or malformed JSONB
   column marshals back as `null`, not `[]`. Marked `nullable: true` on `AnalyticsWeekRow`
   only; the `/analytics/week` map path passes the same nil slice through, so that response
   can emit `null` too — I did **not** mark it nullable there because the value always comes
   from a freshly computed `make([...], 0, 5)` on the compute path. Confidence: medium.
5. **`MeetingBriefResponse.generated_at` omission semantics.** `omitempty` on a
   pre-formatted string; the only path that omits it is GET's `sql.ErrNoRows` branch. The
   refresh path always emits it, potentially as `"0001-01-01T00:00:00Z"`. Not covered by
   any test.
6. **`HabitOccurrence.status` enum completeness.** `scheduled`/`completed`/`displaced`/
   `missed` are the four values I found written (`engine/habits.go:172-216`,
   `handlers_habits.go:360`). There is no DB CHECK constraint confirming the set is closed;
   I did not read the migrations.
7. **`event_id` format.** Used as an opaque string against `meeting_briefs.calendar_event_id`.
   No validation, no length bound, no documented charset. I asserted only `minLength: 1` and
   the structural fact that a chi segment cannot hold an unencoded `/`.
8. **Whether `GET /api/habits` is reachable without a trailing slash.** The route is
   registered as `r.Post("/")` / `r.Get("/")` inside `r.Route("/api/habits", ...)`. chi's
   `Mount` normally serves both `/api/habits` and `/api/habits/`; I documented the
   un-slashed form since that is what a client would write, but did not execute the server
   to confirm no 301 is involved.
9. **`SlackMessage.timestamp`** is Slack's `ts` (`"1712345678.123456"`), deliberately left as
   `type: string` with **no** `format: date-time`. I did not read the exact assignment in
   `meeting_brief_service.go`'s Slack parsing beyond the struct definition, so if it is
   reformatted before storage this is wrong. Confidence: medium.
10. **`Set-Cookie` attributes on the OAuth state cookies.** `http.Cookie{Path:"/", MaxAge:300,
    HttpOnly:true}` — `Secure` and `SameSite` are left at their zero values, so Go emits
    neither attribute. Documented as-is; this is very likely a bug given the cross-site
    callback (see mismatch #4), but I documented behaviour, not intent.
11. **`503` on connect is keyed on the client id only.** `SLACK_REDIRECT_URI` /
    `NOTION_REDIRECT_URI` being empty is *not* checked, so a half-configured server
    302-redirects to the provider with `redirect_uri=` empty rather than returning 503.

---

## (d) Assembly note — shared schema name

`ErrorResponse` is also defined in the sibling fragments `auth.yaml`, `calendar.yaml` and
`teams.yaml`. I checked all four: they are **structurally identical**
(`type: object`, `required: [error]`, `additionalProperties: false`, `error: string`) and
differ only in prose descriptions, because we all derived it from the same `writeError`.
Whoever merges the fragments should keep exactly one definition rather than renaming per
domain — a domain-suffixed `HabitsErrorResponse` would be wrong, since the shape genuinely
is global. No other schema name and no operationId in habits.yaml collides with any sibling
fragment (checked against auth, booking, calendar, scheduling and teams).

---
