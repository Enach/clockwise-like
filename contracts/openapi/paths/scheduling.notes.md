# scheduling.yaml — extraction notes

Domain: focus time, compression, smart scheduling, NLP, LLM config, settings, daily recap.
16 operations across 13 paths, 35 schemas.

Backend read: `backend/api/{routes,handlers_focus,handlers_schedule,handlers_nlp,handlers_llm,handlers_settings,handlers_daily_recap,middleware,interfaces}.go`,
`backend/engine/{focus_time,compression,smart_schedule,daily_recap}.go`,
`backend/nlp/{parser,llm_factory}.go`, `backend/storage/{settings,models,focus_blocks}.go`,
`backend/calendar/participants.go`, `backend/conference/factory.go`,
migrations `001_initial`, `005_conferencing`, `015_daily_recap`.
Tests read: `handlers_focus_test.go`, `handlers_schedule_test.go`, `handlers_nlp_test.go`,
`handlers_settings_test.go`, `handlers_settings_schedule_test.go`.

Frontend read: `/home/claude/smart-calendar-flow/src/api/{types.ts,contract.ts,client.ts}`.

---

## (a) Backend ↔ frontend mismatches

These are real disagreements, not naming preferences. The frontend has an adapter layer
(`normalizeSettings` / `settingsRequestBody` in `client.ts`) for settings only; everything
else is consumed as-is and will silently produce `undefined` fields.

### 1. Settings: whole-object case mismatch, only partly bridged
- Backend emits **camelCase** (`workStart`, `focusMinBlockMinutes`, `llmApiKey`, …).
- Frontend `Settings` (types.ts) is **snake_case** (`work_start`, `focus_min_block_minutes`, …).
- `normalizeSettings()` only bridges four keys: `workingHours`, `lunchBreaks`,
  `outOfHoursMeetingsPerWeek`, `autoDeclineOutsideWorkingHours`. Everything else is
  passed through as the raw backend object cast to `Settings`, so **every other
  snake_case field on the frontend type is `undefined` against the real backend**.
  `settingsRequestBody()` has the mirror-image problem: it spreads the snake_case object
  into the PUT body, so the backend decoder ignores all of it and writes zero values.
  This looks like the single highest-risk drift in the domain.

### 2. Settings: fields that exist on one side only
Backend-only (no frontend field at all): `bufferEnabled`, `bufferMinMeetingMinutes`,
`bufferSkipBackToBack`, `gcpProject`, `gcpLocation`, `vertexModel`, `ollamaBaseUrl`,
`ollamaModel`, `calendarEmail`, `conferencingProvider`, `focusLabel`/`focusColor`
(frontend has `focus_label`/`focus_color` but see #1), all seven `recap*` fields,
`updatedAt`.
Frontend-only (backend ignores them entirely on PUT): `calendar_id`, `teams_enabled`,
`default_conference_provider`.

### 3. `conferencingProvider` enum disagreement
Backend values are `meet` | `zoom` | `teams` (`conference.NewProvider` switch, DB default
`'meet'`). Frontend `ConferenceProvider` is `google_meet` | `zoom` | `teams` | `custom`.
`meet` vs `google_meet` and the extra `custom` are unreconciled.

### 4. `LLMProvider` enum disagreement
Backend `validLLMProviders` accepts `openai, anthropic, ollama, bedrock, azure_openai,
vertex, ""`. Frontend `LLMProvider` omits **`vertex`** and has no empty-string member,
even though `""` is the DB default and is explicitly valid.

### 5. `FocusBlock` — three different shapes for one concept
- `GET /api/focus/blocks` returns `storage.FocusBlock`, which has **no json tags**, so the
  wire keys are Go field names: `ID`, `GoogleEventID`, `StartTime`, `EndTime`, `Date`,
  `CreatedAt`.
- `FocusRunResult.createdBlocks` returns `engine.FocusBlock`, also untagged, with a
  *different* field set: `GoogleEventID`, `Start`, `End`, `Date` (no `ID`, no `CreatedAt`,
  and `Start`/`End` instead of `StartTime`/`EndTime`).
- Frontend `FocusBlock` is `{id, google_event_id, start_time, end_time, date}`.
  **Nothing matches.** Every field the frontend reads off a focus block is `undefined`.

### 6. `FocusRunResult` key case
Backend `weekStart`/`createdBlocks`/`skippedDays`/`totalMinutes`/`errors`;
frontend `week_start`/`created_blocks`/`skipped_days`/`total_minutes`/`errors`.
Only `errors` lines up.

### 7. `MoveProposal` — response camelCase vs request snake_case
Backend response (`engine.MoveProposal`) is camelCase: `eventId`, `eventTitle`,
`currentStart`, `proposedStart`, `focusGainMinutes`, …
Backend *request* for `/api/schedule/compress/apply` is snake_case: `event_id`,
`proposed_start`, `proposed_end`.
Frontend `MoveProposal` is snake_case throughout, and `compressionApply` posts
`{proposals: MoveProposal[]}` straight through — which **accidentally works** for apply
(the three fields it reads line up) but means the frontend never sees any field of the
compression *preview* response.

### 8. `CompressionResult` gain field renamed
Backend `totalFocusGainMinutes`; frontend `estimated_focus_gain_minutes`. Different name,
same meaning.

### 9. `LLMTestResult` is a different object
Backend returns `{ok, response, provider}`. Frontend type is `{ok, message, latency_ms?}`.
`message` and `latency_ms` do not exist on the backend; `response` and `provider` are not
read by the frontend. Additionally the frontend `llmTest(settings)` **posts the settings
object**, which the backend handler never reads — it always tests the *stored* settings.
A user editing provider config in the UI and hitting "Test" tests the saved config, not
the edited one.

### 10. `/api/schedule/create` and `/api/nlp/confirm` return a Google Calendar event
Backend returns the raw `google.golang.org/api/calendar/v3.Event`
(`id`, `summary`, `start.dateTime`, `attendees[].email`, …). Frontend expects its own
`CalendarEvent` (`id`, `title`, `start`, `end`, `attendees: string[]`). Only `id` matches.

### 11. `ParseResult` — closest match in the domain, but still drifts
Backend `nlp.ParseResult` is snake_case and aligns on `intent`, `title`,
`duration_minutes`, `attendees`, `range_start`, `range_end`, `constraints`, `error`,
`suggested_slots`. Backend-only: `preferred_times`, `avoid_times`, `timezone_notes`,
`participant_infos`. Frontend-only: `coverage` (a `CoverageSummary`), which the backend
never sets here. Same for `SuggestedSlot.coverage` — the backend `engine.SuggestedSlot`
has only `start`, `end`, `score`, `reasons`.

### 12. `SuggestedSlot.score` type
Backend `score` is a Go `int` (raw additive score from `scoreCandidate`). The frontend
mock produces fractional values (`0.95 - i * 0.15`), implying a 0..1 float. The real
backend never returns a fraction. Any UI treating score as a 0..1 confidence is wrong.

### 13. Daily recap is entirely absent from the frontend
No reference to `daily-recap` or `recap` anywhere in `smart-calendar-flow/src`. The four
recap endpoints have no client consumer; nothing cross-checks them.

### 14. Error shape: frontend assumes JSON, backend often sends text/plain
`requestApi` parses the error body with `res.json()` (in a try/catch) and builds
`ApiHttpError(status, data)`. But every 500 in this domain, and all four daily-recap
4xx/5xx responses, use Go's `http.Error` → `text/plain; charset=utf-8` with a bare message.
`data` is then `undefined` and any UI reading `err.data.error` shows nothing.
Only `writeError()` responses (most 400s, plus the LLM 502) are JSON.

---

## (b) Surprises and internal inconsistencies

1. **`GET/PUT /api/settings` is not user-scoped.** `storage.GetSettings` /
   `SaveSettings` operate on `WHERE id = 1` — a global singleton — even though the
   `settings` table has a `user_id` column, migration `018_per_user_settings_and_calendars`
   exists, and `GetSettingsByUser` is implemented and used elsewhere. Behind
   `requireAuth`, every authenticated user reads and writes the *same* row. `/api/llm/test`
   and all four daily-recap handlers inherit this.

2. **`PUT /api/settings` is a destructive full replace.** The body decodes into a
   zero-valued struct; omitted fields are persisted as `""` / `0` / `false`. Omitting
   `llmApiKey` wipes the stored API key. Only `microsoft_tokens` and `zoom_tokens`
   (both `json:"-"`, absent from the INSERT column list) survive.

3. **`PATCH /api/settings/daily-recap` rewrites the whole settings row.** It calls the same
   full-row `SaveSettings`, so it also re-writes every non-recap column (from the values it
   just read) and re-runs `saveScheduleFields` normalisation.

4. **Malformed JSON is silently tolerated on two POSTs.** `POST /api/focus/run` resets
   `week` to `""` on a decode error; `POST /api/schedule/compress` discards the decode
   error entirely (`_ = json.NewDecoder(...)`). Both return 200 for garbage input, while
   every sibling endpoint returns 400.

5. **Week-mode compression cannot fail.** The Mon–Fri loop `continue`s past any engine
   error, so a fully broken calendar returns HTTP 200 with body `null` (nil slice).
   Single-day mode with the same error returns 500. Same input class, two outcomes.

6. **Nil slices encode as `null`, not `[]`.** Affects `GET /api/focus/blocks`,
   `POST /api/schedule/compress`, `ScheduleSuggestions.slots`,
   `FocusRunResult.createdBlocks/skippedDays/errors`, `CompressionResult.proposals`,
   `CompressionApplyResult.applied/failed`, `DailyRecapPreview.blocks`. Marked
   `nullable: true` throughout the spec.

7. **`selected_slot_index` has no lower bound.** `handlers_nlp.go:49` checks
   `len(SuggestedSlots) == 0 || SelectedSlotIndex >= len(...)`. A negative index passes and
   then panics on `pr.SuggestedSlots[-1]`. `sentryMiddleware` repanics, so this becomes a
   bare 500 from net/http, not the documented 400.

8. **`/api/llm/test` returns 500 in two different encodings.** Settings-load failure is
   text/plain (`http.Error`); client-construction failure (the common case —
   "LLM not configured") is JSON (`writeError`). A client cannot branch on status alone.
   Also: a misconfigured provider is arguably a 4xx, not a 500.

9. **Out-of-hours refusal is a 500.** `SmartScheduler.CreateMeeting` returns
   `out-of-hours meeting allowance reached` as a plain error, which
   `POST /api/schedule/create` turns into a 500 text/plain. It is a policy rejection
   (409/422 territory), and it is indistinguishable from a real calendar outage.

10. **Week identifiers are inconsistently snapped.** `POST /api/schedule/compress` snaps
    `week` to Monday via `startOfWeek()`. `POST /api/focus/run`, `GET /api/focus/blocks`
    and `DELETE /api/focus/blocks` use the supplied date verbatim as the range anchor.
    Passing a Wednesday to `/api/focus/blocks` returns Wed–Tue, not Mon–Sun.

11. **No 15-minute granularity on this surface.** The brief expected one. The smart
    scheduler steps **30 minutes** (`engine/smart_schedule.go`, three `t.Add(30*time.Minute)`
    sites) and caps at 3 slots ≥1h apart. The only 15-minute stepping in the repo is
    `engine/booking.go:104`, `engine/habits.go:268` and `engine/team_availability.go:141` —
    all other domains. No handler in this domain validates minute alignment of any input.

12. **`recapSendTime` is almost certainly `HH:MM:SS`, not `HH:MM`.** The column is Postgres
    `TIME`, read as `COALESCE(recap_send_time::TEXT,'')`. The Go struct comment says
    `"HH:MM"`. `::TEXT` on TIME renders `08:00:00`.

13. **`recapSendTo` is validated in only one of two write paths.** The daily-recap PATCH
    checks `dm|channel`; `PUT /api/settings` does not, so a bad value there hits the DB
    CHECK constraint and returns a 500.

14. **Validation asymmetries in `validateSettings`.** `workEnd > workStart` is not checked
    for the legacy global fields (only for `DaySchedule` entries). `focusMin <= focusMax`
    is not checked. `bufferMinMeetingMinutes` has no `>= 0` check while
    `bufferBefore/AfterMinutes` do. `timezone` is never validated (engines silently fall
    back to UTC).

15. **`workingHours.days` key casing is a trap.** The validator lowercases the key before
    checking it, so `"Monday"` validates; but `WorkWindow()` looks up
    `strings.ToLower(day.Weekday().String())`, so a stored `"Monday"` key never matches and
    that day reads as "not working".

16. **`llmApiKey` is returned in cleartext** by `GET /api/settings`, on a globally-shared
    settings row.

17. **The 401 from `requireAuth` is a hybrid.** The handler sets
    `Content-Type: application/json` and then calls `http.Error`, which overwrites it to
    `text/plain; charset=utf-8`. The body text is still `{"error":"unauthorized"}`.

18. **`nlp.ParseResult.range_start`/`range_end` are `time.Time` with `omitempty`**, which
    does not omit the zero time. A "missing" range serialises as `0001-01-01T00:00:00Z`
    rather than being absent.

19. **LLM failures inside `/api/nlp/parse` are 200s.** The parser converts them into
    `{"intent":"unknown","error":"..."}`. Only a non-nil error from `Parse()` is a 500.

---

## (c) `x-uncertain` markers — what a human must check

| # | Location in scheduling.yaml | Question |
|---|---|---|
| 1 | `paths./api/schedule/suggest.post.x-uncertain` | The brief asserts a 15-minute increment rule somewhere in this domain. I found only a 30-minute step in the smart scheduler, and 15-minute stepping exclusively in booking/habits/team-availability. Confirm whether a 15-minute constraint was intended here (and is missing), or whether the brief meant a different domain. |
| 2 | `paths./api/nlp/confirm.post.x-uncertain` | Negative `selected_slot_index` panics instead of returning 400. Is that a bug to fix (then the spec should document 400) or should the spec document the 500? |
| 3 | `schemas.GoogleCalendarEvent.x-uncertain` | `/api/schedule/create` and `/api/nlp/confirm` leak the vendored `calendar/v3.Event`. Its exact member set is fixed by the Google client library version, not this repo. I documented only the members the repo sets or the frontend reads, with `additionalProperties: true`. Decide whether the contract should pin a normalised event instead. |
| 4 | `schemas.SlackBlock.x-uncertain` | `engine.slackBlock` is `map[string]interface{}`. I confirmed `header` and `context` blocks in `BuildMessage`; the rest of the function (meetings / focus / habits / briefs sections) was not enumerated block by block. Someone should capture a live `/preview` response before tightening this. |
| 5 | `schemas.SettingsFields.recapSendTime.x-uncertain` | Verify against a live Postgres whether `recap_send_time::TEXT` yields `08:00:00` (my reading) or `08:00`. This changes the documented format and affects any `HH:MM` client. |
| 6 | `schemas.SettingsFields.calendarProvider.x-uncertain` | No server-side validation of the accepted value set. `google` / `outlook` / `webcal` is inferred from the frontend type and the DB default, not from backend code. Confirm the authoritative list. |
| 7 | `schemas.FocusRunResult.skippedDays` (inline note, not a formal `x-uncertain`) | The element format is not pinned by a struct tag. I did not trace every `append` site in `engine/focus_time.go` to confirm whether entries are `YYYY-MM-DD` or a weekday name. |

### Also worth a human decision (documented in the spec, not flagged `x-uncertain`)

- **`SchedulingErrorResponse` name.** Another agent may be defining a shared error schema.
  I prefixed mine to guarantee global uniqueness; merge into a single `ErrorResponse` at
  assembly time if one exists. Note that it only covers `writeError()` responses — the
  text/plain `http.Error` responses are documented inline per operation.
- **Trailing slash on `/api/settings`.** Registered as `r.Route("/api/settings")` +
  `r.Get("/")`. chi serves both spellings; I documented the un-slashed form only.
- **No `security` key is written on any operation**, per instruction — all 16 sit inside
  the `requireAuth(jwtSecret)` group in `routes.go` and inherit the global default.
