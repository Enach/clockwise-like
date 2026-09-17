# SEAM-REQUIRED — what the backend needs before external calendars can be faked

**Status**: factory work item. Stage 0 intake, ready for a spec.
**Raised by**: `e2e-author`, while building `e2e/`.
**Problem statement (stage 0 form)**: *the majority of Paceday's user-visible
behaviour cannot be verified before release, because every calendar-dependent
journey can only be exercised against a live Google account.*

---

## 0. The finding, in one paragraph

**There is no seam. External calendar providers cannot be faked today, by any
means available outside the Go process.** Every calendar call in the backend —
all ten call sites — funnels through one constructor, `calendar.NewClient`,
which builds a `google.golang.org/api/calendar/v3` service with the library's
compiled-in base URL. It does not pass `option.WithEndpoint`, and there is no
environment variable, configuration struct or exported interface anywhere above
it that would let a test point it somewhere else. An interface that *looks*
like the seam (`calendar.Provider` / `calendar.NewProvider`) exists but is dead
code with zero production call sites. The consequence is that the e2e suite
covers the anonymous booking flow, sessions, scheduling-link management and the
manager roster in full, and cannot cover viewing the calendar, focus time,
compression, habits, analytics, meeting briefs, auto-decline, team availability
or manager detection at all.

---

## 1. Evidence

### 1.1 The single chokepoint

`backend/calendar/client.go`:

```go
func NewClient(ctx context.Context, tokenSource oauth2.TokenSource) (*CalendarClient, error) {
	svc, err := googlecalendar.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, err
	}
	return &CalendarClient{service: svc, CalendarID: "primary"}, nil
}
```

`googlecalendar.NewService` defaults to `https://www.googleapis.com/calendar/v3/`.
The Google API Go client honours no endpoint environment variable;
`option.WithEndpoint` is the only override, and it is not used here.

### 1.2 Every call site goes through it

Verified by `grep -rn "calendar.NewClient" backend --include=*.go`:

| File | Line | Reached by |
|---|---|---|
| `backend/engine/cal_iface.go` | 60 | `newCalOps` → focus time, compression, smart schedule, NLP confirm |
| `backend/engine/booking.go` | 206 | `calendarClientForUser` → collective slots, booking confirmation |
| `backend/engine/manager.go` | 196 | `DetectTeam` |
| `backend/engine/analytics.go` | 217 | weekly analytics recompute |
| `backend/engine/habits.go` | 258 | habit scheduling |
| `backend/engine/auto_decline.go` | 87 | auto-decline cron |
| `backend/engine/team_availability.go` | 85 | team availability |
| `backend/engine/freebusy_service.go` | 133 | `/api/freebusy` |
| `backend/api/handlers_calendar.go` | 144 | `GET /api/calendar/events`, `/freebusy` |
| `backend/api/handlers_events.go` | 31 | `PATCH`/`DELETE /api/events/{id}` |

Ten call sites, one constructor. That is good news: **one change fixes all ten.**

### 1.3 The interface that is not a seam

`backend/calendar/provider.go` defines `Provider`, and
`backend/calendar/client_factory.go` defines `NewProvider(ctx, settings, ts)`
which switches on `settings.CalendarProvider` over `"outlook"`, `"webcal"` and
a `"google"` default. The `"webcal"` branch calls
`NewWebcalClient(s.WebcalURL)` — a *database-configurable arbitrary HTTP URL*,
which would be a perfect injection point.

**`NewProvider` has no callers.** `grep -rn "calendar.NewProvider" backend`
returns nothing outside its own definition. Production code calls
`calendar.NewClient` directly and ignores `settings.calendar_provider`
entirely. So setting `calendar_provider = 'webcal'` on a user changes nothing,
and building a fake .ics server against it would be theatre — it would prove a
code path no user reaches. That is why this suite does not do it.

(The one genuinely live webcal path is
`backend/calendar/personal_reader.go`, used by the personal-calendar blocker.
It reads `personal_calendars.url`. That is a real, small seam and is noted as
option (C) below.)

### 1.4 The in-package seam that is unexported

`engine.FocusTimeEngine` has an unexported field `calOps calendarOps`
(`backend/engine/focus_time.go`, `backend/engine/cal_iface.go`) which its unit
tests set to a double. It is unexported and `main.go` never assigns it, so it
cannot be reached from outside the process. Similarly `engine.Clock` /
`engine.FixedClock` exist but `engine.SystemClock` is never replaced from
configuration — see §5.

### 1.5 OAuth and identity are hardcoded too

- `backend/auth/google_oauth.go` → `Endpoint: google.Endpoint` (auth at
  `accounts.google.com`, token exchange and refresh at `oauth2.googleapis.com`).
- `backend/api/handlers_auth.go:172` and `:195` → the literal
  `https://www.googleapis.com/oauth2/v2/userinfo`.
- `backend/calendar/outlook_client.go:15` → `const graphBaseURL = "https://graph.microsoft.com/v1.0"`.
- `backend/conference/teams.go:13` → `const graphBase = "https://graph.microsoft.com/v1.0"`.

So the sign-in callback cannot be exercised either. (The suite works around
this by minting session JWTs directly — which is legitimate, because
`api/middleware.go` treats a validly signed JWT *as* the session, with no
server-side session store. See `e2e/auth.ts`.)

---

## 2. The minimal change requested

Three items, independent, in priority order. Item **A** alone unblocks the
majority of the fixme'd journeys.

### Item A — `GOOGLE_CALENDAR_API_BASE_URL` (unblocks 8 journeys)

**Interface**: keep `calendar.Provider` out of it. Change one function.

*File*: `backend/calendar/client.go`

```go
// calendarAPIBaseURL overrides the Google Calendar API endpoint. Empty (the
// default, and the only value production ever sets) uses the library's own
// https://www.googleapis.com/calendar/v3/. A non-empty value is used verbatim
// and MUST include the trailing "/calendar/v3/" path, matching what
// option.WithEndpoint expects.
func NewClient(ctx context.Context, tokenSource oauth2.TokenSource) (*CalendarClient, error) {
	opts := []option.ClientOption{option.WithTokenSource(tokenSource)}
	if base := strings.TrimSpace(os.Getenv("GOOGLE_CALENDAR_API_BASE_URL")); base != "" {
		opts = append(opts, option.WithEndpoint(base))
	}
	svc, err := googlecalendar.NewService(ctx, opts...)
	...
}
```

**Env var**: `GOOGLE_CALENDAR_API_BASE_URL`, read once at construction.
Add it to `.env.example` with an explicit "leave empty outside tests" comment,
and to the `backend` service env block in `docker-compose.yml` so the key is
visible in one place.

**Call sites needing no change**: all ten in §1.2. That is the point of doing
it here.

**Why an env var rather than an injected interface**: `calendar.NewClient` is
called from eight engine functions that receive no configuration object, only a
`*sql.DB` and an `*oauth2.Config`. Threading a new parameter through all of
them is a large, untested refactor of exactly the kind
`docs/factory/README.md` §4 warns against. An env var read in one constructor
is a four-line diff. If a later refactor introduces a config struct, this moves
into it without changing any call site.

**Objection to answer in the spec**: reading an env var inside a constructor is
not testable in isolation and is a hidden global. The mitigation is that the
value is read exactly once, in exactly one function, and is documented as
test-only. A `contract-challenger` should push for
`calendar.NewClientWithEndpoint(ctx, ts, base)` plus a package-level default
set from `main.go`, which is equally small and more honest; either is
acceptable.

**Acceptance criterion (Given/When/Then, for the spec author)**:
> Given `GOOGLE_CALENDAR_API_BASE_URL` points at an HTTP server that implements
> `GET /calendars/{calendarId}/events` and `POST /freeBusy`, and given a user
> with a valid `oauth_tokens` row, when that user requests
> `GET /api/calendar/events`, then the response contains the events that server
> returned, and no request is made to `www.googleapis.com`.

### Item B — `GOOGLE_OAUTH_ENDPOINT_BASE` and `GOOGLE_USERINFO_URL` (unblocks sign-in)

*Files*: `backend/auth/google_oauth.go` (replace `google.Endpoint` with an
`oauth2.Endpoint` built from the env var when set), and
`backend/api/handlers_auth.go` lines 172 and 195 (replace the literal userinfo
URL with the env var when set).

Lower priority than A: `e2e/auth.ts` already covers everything downstream of
the callback by minting the JWT the callback would have issued. What stays
untested without B is the callback handler itself — the code exchange, the
userinfo fetch, the user upsert and the token persist.

### Item C — the cheap partial: make `calendar.NewProvider` live

Wire `calendar.NewProvider` in at the ten call sites so that
`settings.calendar_provider` is actually honoured. This costs more than item A
and buys less for testing (the webcal provider is read-only —
`CreateEvent` returns `ErrReadOnly` — so focus time, which must *write* events,
stays untestable). **But it is a real product bug independent of testing**: the
settings UI lets a user select Outlook or WebCal and the backend ignores the
choice. Worth its own Linear issue on those grounds alone. It is listed here so
the two are not conflated: fixing C does not remove the need for A.

---

## 3. What the fake provider would then look like

Not built, because it would have nothing to attach to. When item A lands, build
it as a **Go** service under `e2e/fake-google/`, not Node:

- the repo already builds two small Go binaries into scratch images
  (`backend/Dockerfile`, `docker/fileserver`), so the pattern, the base image
  and the reviewer familiarity are already there;
- it can import `google.golang.org/api/calendar/v3` and marshal the exact
  `Event` / `FreeBusyResponse` structs the client will unmarshal, which
  eliminates a whole class of "my hand-written JSON was subtly wrong" bugs;
- the e2e suite's only other Node dependency is Playwright itself, and adding a
  Node service would mean a second lockfile to pin and audit.

Endpoints the backend actually calls (from `backend/calendar/events.go`,
`freebusy.go`, `rooms_attendees.go` — enumerate exactly before building):

| Method | Path | Called by |
|---|---|---|
| GET | `/calendars/{calendarId}/events` | `ListEvents` |
| POST | `/calendars/{calendarId}/events` | `CreateEvent` |
| GET | `/calendars/{calendarId}/events/{eventId}` | `GetEvent` |
| PUT | `/calendars/{calendarId}/events/{eventId}` | `UpdateEvent` |
| DELETE | `/calendars/{calendarId}/events/{eventId}` | `DeleteEvent` |
| POST | `/freeBusy` | `GetFreeBusy` |
| PATCH/PUT | `/calendars/{calendarId}/events/{eventId}` | `DeclineEvent`, `AddGoogleMeet`, `ClearGoogleMeet` (all Update-shaped) |
| GET | `/users/me/calendarList` (verify) | `ListRooms` |

`ListRooms` and `SuggestAttendees` (`backend/calendar/rooms_attendees.go`) hit
additional Google surfaces; confirm their exact request shapes against that file
before implementing, rather than trusting this table.

It should hold state in memory keyed by calendar id, expose a
`POST /__fixture/reset` and a `POST /__fixture/events` control plane for the
tests to seed with, and reject any request without a bearer token so that a
broken token path fails rather than silently working.

---

## 4. Meanwhile: how this suite stays honest

`e2e/docker-compose.e2e.yml` maps `www.googleapis.com`, `oauth2.googleapis.com`,
`accounts.google.com`, `graph.microsoft.com` and the other provider hosts to
`127.0.0.1` inside the backend container. Any call that *does* escape fails
immediately with connection-refused instead of hanging for thirty seconds or —
far worse — reaching the real internet from a test run. The seeded users have
**no `oauth_tokens` rows**, which makes every calendar code path take its
"not connected" branch deterministically and instantly.

---

## 5. Related finding: the clock cannot be driven either

`backend/engine/clock.go` defines `Clock`, `realClock`, a package-level
`var SystemClock Clock = realClock{}` and a `FixedClock` test double. Engines
fall back to `SystemClock` when they have no clock configured, and
`backend/main.go` never sets one from configuration. **There is therefore no
way to control the backend's notion of "now" from outside the process**, and
this suite does not pretend otherwise: every time-dependent fixture is relative
(`e2e/lib/dates.ts`), and the compose file pins `TZ=UTC` in the backend
container and `timezoneId: 'UTC'` in the browser so the two agree.

If deterministic time becomes necessary — it would be, for testing the recap
cron's per-user send time, the 15-minute free/busy cache TTL, or the 4-hour
team-analytics cache — the change is the same shape as item A: read an
`E2E_FIXED_NOW` (RFC3339) in `main.go` and assign `engine.SystemClock =
engine.FixedClock{T: parsed}` when set. That is a separate work item and is
**not** requested here, because nothing in the currently-fixme'd journeys is
blocked on it.
