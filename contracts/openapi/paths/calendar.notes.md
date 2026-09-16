# Domain: calendar — extraction notes

Scope: `GET /api/calendar/events`, `GET /api/calendar/freebusy`, `POST /api/freebusy`,
`PATCH|DELETE /api/events/{id}`, `GET /api/rooms`, `GET /api/attendees/suggest`,
the six `/api/personal-calendars` routes, `GET /api/org/members`, `GET /api/audit`.
15 operations, 24 schemas.

Backend sources read: `backend/api/routes.go`, `handlers_calendar.go`,
`handlers_events.go`, `handlers_freebusy.go`, `handlers_personal.go`,
`handlers_org.go`, `handlers_audit.go`, `middleware.go`, `interfaces.go`,
`handlers_settings.go` (for `writeError`), `handlers_conferencing.go` (for
`conferenceFromEvent`), `backend/calendar/{client,events,freebusy,provider,rooms_attendees}.go`,
`backend/engine/freebusy_service.go`, `backend/storage/{personal_calendars,users,orgs,audit_log}.go`.
Tests read: `handlers_calendar_test.go`, `handlers_freebusy_test.go`,
`handlers_personal_test.go`, `handlers_contract_extra_test.go`.

Frontend sources cross-checked: `smart-calendar-flow/src/api/{types.ts,contract.ts,client.ts}`.

---

## (a) Backend / frontend contract mismatches

These are real disagreements, recorded rather than smoothed over.

### 1. `Room` — backend returns 2 of the 7 fields the frontend declares
`calendar.Room` (`backend/calendar/rooms_attendees.go:11`) is `{id, name}` only.
`Room` in `src/api/types.ts:144` is `{id, name, email (required), building?, floor?, capacity?, available?}`.
`email` is **required** on the frontend type and is never sent. In practice the room's
email *is* the backend `id` (the Google resource calendar id), so the frontend is
almost certainly reading `id` where it means `email`, and `building`/`floor`/
`capacity`/`available` only ever exist in `MOCK_ROOMS`. Any UI that renders a live
(non-mock) room list is rendering `undefined` for those.

### 2. `GET /api/rooms` — `start`/`end` query params are sent and ignored
`client.ts:1198` calls `requestApi<Room[]>("GET", "/rooms", undefined, { q, start, end })`
and `contract.ts` declares `searchRooms(q, start?, end?)`. The handler
(`handlers_events.go:106`) reads only `q`. So the availability filtering the signature
implies is mock-only; against the real backend `available` is never computed.

### 3. `PATCH /api/events/{id}` — three body fields are silently dropped
Frontend sends `{...patch, send_updates}` where `patch` may contain
`room_resource_email` and `attendee_details` (`client.ts:1164`, `contract.ts:88`).
The backend body struct (`handlers_events.go:37`) accepts only
`title, description, location, start, end, attendees`. `send_updates`,
`room_resource_email` and `attendee_details` are discarded by `encoding/json`
with no error. Consequence: attendee RSVP/display-name edits and room changes
made through this endpoint are no-ops, and guests are never notified.

### 4. `PATCH /api/events/{id}` — response shape is wrong side of the DTO boundary
Frontend types the response as `CalendarEvent`. The handler encodes the **raw
`google.golang.org/api/calendar/v3.Event`** it got back from `UpdateEvent`
(`handlers_events.go:87`). That object uses `summary`, `start.dateTime`,
`hangoutLink`, etc. — not `title`/`start`/`end`. This is exactly what
`TestToCalendarEventDTO_JSONShape` asserts must *not* happen on the events list
endpoint, so the inconsistency is deliberate nowhere and accidental here.
Any frontend code reading `.title` off the PATCH result gets `undefined`.

### 5. `DELETE /api/events/{id}` — `send_updates` query param ignored
Same as (3): `client.ts:1187` sends `?send_updates=none|all`; the handler reads no
query parameters at all.

### 6. `POST /api/freebusy` — participant shape disagrees three ways
- Frontend `ParticipantCoverage` (`types.ts`) is `{email, status, provider?}`.
  Backend `freeBusyParticipantDTO` is `{email, status, busy}`. Backend never emits
  `provider`, so the "small provider logo on the coverage badge" the type comments
  describe can never render from live data.
- Frontend `CoverageStatus` is `"paceday_user" | "known" | "unknown"`. The backend
  only ever produces `"known"` or `"unknown"` (`engine/freebusy_service.go:143-146`
  and the personal-domain branch at line 93). `paceday_user` is unreachable.
- Frontend `FreeBusyResponse` does not declare the `results` key the backend always
  sends. Harmless for TS structural typing, but it means the legacy key is
  undocumented on the consumer side.

### 7. `GET /api/calendar/freebusy` has no consumer and a different shape
Nothing in `smart-calendar-flow/src` references `/api/calendar/freebusy` — the
frontend only uses `POST /api/freebusy`. The GET variant returns
`map[string][]calendar.TimeSlot` with **capitalized `Start`/`End`** keys (see (b)1),
which does not match `FreeBusyEntry` (`{start, end}`). Dead or broken; a human must
decide which.

### 8. `GET /api/org/members` has no consumer
No reference anywhere in `smart-calendar-flow/src`. There is no `OrgMember` type in
`types.ts` and no method on `ApiPort`.

### 9. `PersonalCalendar.email` is declared but never sent
`types.ts:98` has `email?: string` and the mock `addPersonalCalendar` fabricates
`you.google@example.com`. `personalCalendarDTO` has no such field. Only the mock path
produces it.

### 10. `PersonalCalendar.type` is an enum on the frontend, free-form on the backend
Frontend: `"google" | "outlook" | "webcal"`. Backend: any non-empty `provider` string
is accepted on create and echoed back as `type`. `normalizePersonalCalendar`
(`client.ts:390`) blind-casts `raw.type ?? raw.provider ?? "webcal"` to the union, so
a bad provider value flows into the UI mistyped rather than being rejected.

### 11. `GET /api/audit` default limit differs
Frontend `DEFAULT_AUDIT_LIMIT = 50` and always sends `?limit=`. Backend default when
the param is absent or unparseable is **100** (`storage.ListAuditLog` clamps `<=0` to
100). Not a bug today because the frontend always sends a value, but the two
"defaults" are not the same number.

### 12. `AuditEntry.details` — frontend defends against a shape the backend cannot emit
`normalizeAuditEntries` / `formatAuditDetails` (`client.ts:407-440`) handle `details`
being an object, number or boolean and JSON-stringify it. The backend scans the column
into a Go `string`, so `details` is always a JSON string. Defensive only; noted so
nobody "fixes" the backend to send structured details assuming the spec allows it.

---

## (b) Surprising or inconsistent things in the backend itself

1. **Two JSON key casings in one domain.** `calendar.TimeSlot` and
   `calendar.GenericEvent` have **no json struct tags**, so they serialize with Go
   field names. That means:
   - `GET /api/calendar/freebusy` → `{"alice@x.com":[{"Start":"…","End":"…"}]}`
   - `GET /api/personal-calendars/{id}/preview` → `[{"ID":…,"Title":…,"Start":…,"End":…}]`
   - `POST /api/freebusy` → the `results[].busy` windows are capitalized while the
     `participants[].busy` and `busy` windows on the *same response* are lowercase.
   This last one is the sharpest: one response body contains both casings for the
   same data.

2. **Two error body formats, mixed within single handlers.**
   `writeError` emits `{"error":"…"}` as `application/json`. `http.Error` emits plain
   text. `handlers_calendar.go` uses `http.Error` exclusively. `handlers_freebusy.go`
   uses `writeError` for 400/401 but `http.Error` for its 500 — so a client parsing
   that handler's errors as JSON succeeds on 4xx and throws on 5xx.

3. **JSON bodies served as `text/plain`.** `middleware.go:36-52` and
   `handlers_org.go:21,33` set `Content-Type: application/json`, then call
   `http.Error`, which **overwrites** the header with `text/plain; charset=utf-8` and
   adds `X-Content-Type-Options: nosniff`. The body is JSON, the header says it is not.

4. **`GET /api/audit` is not scoped to the caller.** `storage.ListAuditLog` has no
   user or org predicate — every authenticated user reads the whole global audit log.

5. **`PATCH /api/events/{id}` maps every `GetEvent` failure to 404**, including
   network and permission errors, with the raw Go error appended to the message
   (`"event not found: <err>"`). Conversely `DELETE` maps *every* failure to 500,
   so deleting a non-existent event is a 500, not a 404. Inconsistent between the two
   methods on the same path.

6. **`PATCH /api/events/{id}` silently converts all-day events to timed events.**
   Setting `start` or `end` always writes an `EventDateTime{DateTime: …}` and never a
   `Date:` value, so patching only the title of an all-day event is safe but patching
   its time destroys its all-day-ness.

7. **`PATCH /api/events/{id}` with `attendees` drops room resources.** The events
   *list* endpoint deliberately filters `*@resource.calendar.google.com` out of
   `attendees`, so a naive read-modify-write round trip through the frontend removes
   the room booking from the event.

8. **`POST /api/personal-calendars` creates a disabled calendar when `enabled` is
   omitted** — `Enabled bool` is not a pointer, so the zero value wins. The frontend
   always sends `true`, which masks this.

9. **`personalCalendarDTO.id` is a string; the path parameter is an integer.**
   `strconv.FormatInt` on the way out, `strconv.ParseInt` on the way in.

10. **`is_personal_block` on `CalendarEvent` is dead.** Declared on the DTO,
    never assigned by `toCalendarEventDTO`, so it never appears in a response.

11. **`/api/calendar/events` requires `start` and `end` implicitly.** There is no
    default window; an absent param fails `time.Parse` and yields 400. Documented as
    `required: true` for that reason.

12. **`POST /api/freebusy` does not validate `end_time > start_time`.**

13. **Free/busy failures degrade to `unknown` rather than erroring.** A 5s timeout,
    a missing OAuth token or a provider error all produce `coverage: "unknown"` with
    a 200. The only 500 path is a settings-read failure.

14. **`/api/attendees/suggest` does not filter room resources**, while
    `/api/calendar/events` does. The 30-day lookback and 20-result cap are hardcoded.

15. **`GET /api/org/members` returns 200 `[]` when the user lookup errors**, not 500 —
    an error and "no organization" are indistinguishable to the client.

16. **Error messages leak raw Go/provider error strings** into response bodies across
    this whole domain (`"not connected: "+err.Error()`, `"update failed: "+err.Error()`,
    `"audit: "+err.Error()`, and every `writeError(w, err.Error(), 500)`).

---

## (c) `x-uncertain` list — what a human must check

| Location in `calendar.yaml` | Question |
|---|---|
| `getCalendarFreeBusy` (operation) | `GET /api/calendar/freebusy` has no frontend consumer and returns capitalized `Start`/`End`. Keep it, align it with `POST /api/freebusy`, or delete it? |
| `patchEvent` (operation) | The 200 body is the raw Google event, not `CalendarEvent`. Which side is authoritative? No test covers the success body, so nothing pins it today. |
| `listAuditEntries` (operation) | The audit log is global, not per-user/per-org. Intentional, or a data-exposure bug to fix before publishing? |
| `CalendarEvent.is_personal_block` (property) | Dead field — is the tagging logic missing, or should the field be removed from the DTO? |
| `OrgMember.provider` (property) | The exact set of values written at signup was not traced. Confirm against `handlers_auth.go` / `handlers_microsoft_auth.go` before adding an enum. |

Additional items a human should resolve that are not marked `x-uncertain` because the
Go behaviour is unambiguous, only questionable:

- Whether `Room` should gain `email`/`capacity`/`available`, or the frontend type
  should shrink to `{id, name}` (mismatch 1 + 2).
- Whether `send_updates` should be implemented on `PATCH`/`DELETE /api/events/{id}`
  or removed from the frontend client (mismatches 3 + 5).
- Whether `CoverageStatus: "paceday_user"` and `ParticipantCoverage.provider` are
  planned backend work or frontend dead code (mismatch 6).
- Whether the domain should standardize on `writeError` everywhere, which would
  change the `Content-Type` and body of six documented error responses
  (surprises 2 + 3).
- Whether `results` in the `POST /api/freebusy` response can be dropped; it is
  explicitly labelled "for older API consumers" in the handler comment and is not in
  the frontend type.
