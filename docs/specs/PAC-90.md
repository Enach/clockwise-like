# Spec — PAC-90: PAC-81 Part A follow-ups (booking connection release, access-log implicit 200, step-up reservation test, flush, completion-unit cancellation)

- **Linear**: https://linear.app/paceday/issue/PAC-90
- **Status**: draft
- **Author**: spec-author
- **Base**: origin/main 4f798cb (after PAC-81 Part A 48f6c60, PAC-83, PAC-84, PAC-81 F-C1)
- **Parent spec**: `docs/specs/NEW-deploy-hardening.md` (PAC-81) — AC-A7, AC-A8, AC-A9/A10, AC-A13, AC-A19 are the criteria these items tighten.
- **Inherited scope**: none. No audit finding in `docs/factory/api-audit.md` and no
  `x-uncertain` marker covers these code paths; nothing here changes the HTTP contract.

## 1. Problem

The confirming review of PAC-81 Part A (#247) left five non-blocking findings.
Each is a place where the hardening does not quite do what its own criterion says:

1. A booking whose database insert panics leaks one database connection for the
   life of the process. With the production pool cap, a handful of such panics
   makes every request wait for a connection until the budget answers 503.
2. The access log records "-" (nothing sent) for a request whose handler wrote
   nothing, although the server then sends a 200. Operators reading the log
   during an incident see phantom "client went away" lines for healthy requests.
3. The SSO step-up path reserves its record's connection before committing,
   exactly like booking, but only the booking variant has a test that saturates
   the pool at that moment. A regression on the step-up path (recording a grant
   after the budget already told the admin "denied") would go unnoticed.
4. A handler's flush never reaches the client in production: one wrapper in the
   chain does not pass it on, and the test recorder hides this. Nothing streams
   today, so the cost is latent: the first streaming feature silently buffers.
5. After a booking is saved, the background unit that creates host events and
   sends the confirmation email has two steps the shutdown's hard cancel cannot
   stop. They keep running until the process exits (the email up to its own
   15 s limit, longer than the 5 s cancel grace), and a stuck event-record write
   can hold up closing the database pool, which the shutdown waits on.

## 2. Current behaviour (origin/main 4f798cb)

### Item 1 — booking connection released by explicit calls only — STILL VALID

- `backend/engine/booking.go:389` takes a dedicated connection; `:396-398`
  closes it when the budget has already answered; `:400-407` runs the insert on
  it; `:408` closes it explicitly ("released before the completion unit needs the
  pool"); `:413` then starts the completion unit and waits for it.
- No `defer` covers `:400-407`. A panic in the insert path propagates (the
  budget middleware re-panics on the request goroutine, `internal/budget/budget.go:180-184`;
  net/http recovers it) and the `sql.Conn` is never returned to the pool.
- **The issue's proposed fix ("`defer conn.Close()`") is wrong as written.** A
  function-scoped defer would hold the connection until `ConfirmBooking` returns,
  i.e. while it waits on the completion unit (`:413-417`), which itself needs a
  connection. With the pool capped at 1 that is a deadlock; it would break
  `TestPAC81_PoolCapOnePaths` (`backend/api/pac81_paths_test.go:605`, AC-A10).
  The release must cover the panic path *and* still happen before the unit starts.
  Fix direction (non-binding): scope the reservation + insert in its own function
  (or closure) with a deferred close, or add a deferred close alongside the
  existing early close (`sql.Conn.Close` is safe to call twice; the second
  returns `sql.ErrConnDone`, which is ignored).
- Contrast: the step-up path already uses a deferred close
  (`backend/api/handlers_sso_stepup.go:660`) and is not affected.
- Not fixed by PAC-83/PAC-84 or any test. Other `db.Conn` call sites
  (`storage/db.go:49`, `storage/encryption_keys.go:171`, `storage/encryption_ops.go:781`)
  are operator/migration paths, not request paths, and are out of scope.

### Item 2 — access log shows "-" for an implicit 200 — STILL VALID

- `backend/api/middleware.go:118-138`: the status is "-" unless the wrapped
  writer saw a `WriteHeader`/`Write` (`:123-126`). A handler that returns without
  writing gets an implicit 200 from net/http when the connection is alive, yet
  the line says "-".
- The comment at `:110-117` and AC-A13 both say "the status actually sent".
- The only test, `TestPAC81_AccessLogRoutePatternAndNothingWritten`
  (`backend/api/pac81_paths_test.go:741`), covers the client-gone case (context
  cancelled before anything is written → "-"); no test covers a live request
  whose handler writes nothing. That test must keep passing.
- The budget's "client gone" branch writes nothing on purpose
  (`internal/budget/budget.go:190-191`, `:209-211`); in that case "-" is right.

### Item 3 — step-up reservation path untested — STILL VALID

- `backend/api/handlers_sso_stepup.go:648-674`: the record's connection is
  reserved on the request context (`:651`), a reservation failure with an ended
  context returns and lets the budget answer (`:653-655`), then `budget.Commit`
  (`:662`) gates the write, and the record runs detached on the reserved
  connection (`:665-669`).
- Existing coverage: `TestPAC81_StepupCallbackAtBudgetBeforeCheck0`
  (`pac81_paths_test.go:400`) cuts the budget *before* the checks (users table
  locked), not at the reservation. `TestPAC81_HandlerGoroutineReturnsAtBudgetWithPoolHeld`
  (`:637`) iterates `pac81NamedPaths`, which contains no step-up callback. The
  booking twin exists (`engine/pac81_review_test.go:97`,
  `TestPAC81_BookingPoolSaturatedBeforeInsertIs503`); the step-up one does not.
- What the budget answers at that moment: the callback registers a denied
  redirect as the budget's answer, updated as checks pass
  (`handlers_sso_stepup.go:555-560`, `:570-616`). By the record step the last
  registration is `reason=not_admin` (`:592` Google, `:616` Microsoft). So an
  admin whose checks all passed but whose record could not get a connection is
  redirected to `/settings?stepup=denied&reason=not_admin` — a misleading reason.
  This is recorded as an observation (§8 OQ-2), not changed here: the reason set
  is a contract enum (`contracts/openapi/paths/auth.yaml:1695`).

### Item 4 — `budget.writer.Flush` is a no-op in production — STILL VALID

- Production chain, outer to inner (`backend/api/routes.go:32-40`): Sentry →
  `loggingMiddleware` (wraps the writer in `statusWriter`) → CORS → body cap →
  budget (wraps it in `budget.writer`) → handler.
- `internal/budget/budget.go:269-278`: `Flush` forwards only if
  `w.dst.(http.Flusher)` holds. `w.dst` is the `*statusWriter`, which defines
  `WriteHeader`, `Write` and `Unwrap` only (`api/middleware.go:166-185`). A type
  assertion does not follow `Unwrap`, so the flush is dropped.
- Unit tests pass an `httptest.ResponseRecorder` (which implements `Flush`)
  directly as `dst`, hiding this.
- No production handler flushes today (`grep Flush` over non-test backend code
  finds only `budget.go:269` and unrelated `FlushSealCounts`/`sentry.Flush`).
- PAC-71 (#242) adds another wrapper, `guardedResponseWriter`, inside
  `requireAuth` with `Unwrap` but no `Flush` (see §9) — the same gap one layer in.

### Item 5 — completion-unit steps the hard cancel cannot stop — STILL VALID

- The completion unit runs on the lifecycle group's context
  (`internal/lifecycle/lifecycle.go:119-130`), cancelled at the end of the drain
  (`:284`), then given the 5 s cancel grace (`:285-287`, `DefaultPhases` `:234`)
  before being logged as abandoned (`:288-296`).
- Host event record: `engine/booking.go:526` writes on
  `context.WithoutCancel(callCtx)`, so neither the per-call limit nor the hard
  cancel stops it (deliberately: "An answer that arrived is always recorded").
  `main.go:315` closes the pool after shutdown; `database/sql` `Close` waits for
  queries already running, so a blocked record write can delay exit past AC-A7's
  bound (notice + drain + cancel grace + 3 s).
- Confirmation email: `engine/booking.go:538-539` calls `sendConfirmationEmail`,
  which takes no context; it ends in `sendMail` (`:598`, `outbound.SendMail`,
  `internal/outbound/outbound.go:121-133`), bounded only by its own 15 s
  deadline (`outbound.go:43`). The hard cancel cannot cut it; after the 5 s grace
  the unit is logged abandoned while the send may still complete or fail unseen.
- Not addressed by PAC-83/PAC-84. Owner Q-O4 (2026-11-01) — booking emails need
  not survive a restart; an outbox is PAC-85 — still holds and is not reopened.

## 3. Desired behaviour

1. A booking request that fails in any way after reserving its database
   connection — including a crash in the save step — gives that connection
   back. The connection is still given back before the post-save work starts,
   so a pool of one never deadlocks.
2. The access log's status is what the client actually received: a request
   whose handler wrote nothing and whose client is still connected logs 200;
   "-" is reserved for requests whose context ended (client gone, or the
   server cancelling at the end of the drain) before anything was written.
3. The step-up path's "pool saturated at the record" behaviour is pinned by a
   test: the admin gets the budget's answer and no grant or outcome is stored.
4. A flush issued by a handler reaches the client through every wrapper the
   production server installs, unless the budget has already answered (then it
   stays a no-op). A flush before any write counts as sending a 200 for the
   access log.
5. The shutdown's hard cancel stops every step of the booking completion unit
   within the cancel grace: an in-progress event-record write and an
   in-progress confirmation email are cut, logged with the booking id (AC-A8),
   and not retried. During the drain (before the hard cancel) nothing changes:
   a created event's id is still recorded, the email still has its own limit.

## 4. Acceptance criteria

AC-1 (item 1). Given the booking save step panics after the connection was
reserved and the budget committed, when the request ends, then the pool reports
zero connections in use within 1 s, and with the pool capped at 1 a following
request can acquire a connection within 1 s. Fails today: the connection stays
in use forever. [unit — needs a fault seam in the save step]

AC-2 (item 1). Given the pool capped at 1, when each PAC-81 named path runs,
then every path completes within 10 s and the background units finish (the
existing AC-A10 test, unchanged, must still pass after the item-1 change). [integration — existing `TestPAC81_PoolCapOnePaths`]

AC-3 (item 2). Given the production middleware chain and a live request whose
handler returns without writing, when the request completes, then exactly one
access-log line records status 200 (not "-"). Fails today. [unit]

AC-4 (item 2). Given a request whose context is cancelled before anything is
written, then the line records "-"; given a budget expiry, 503; given a
replaced status, the replaced status (existing AC-A13 behaviour, unchanged). [unit — existing test plus the replaced-status test at `pac81_middleware_test.go:117`]

AC-5 (item 3). Given a step-up callback whose checks all pass and whose
database pool (capped at 1) is held elsewhere just before the outcome record
reserves its connection, with a short budget, when the callback runs, then the
response is the budget's denied redirect within budget + 1 s, the handler
goroutine returns within budget + 1 s, and after the held connection is
released no step-up outcome and no grant exists for that user. [integration — DB-backed api test]

AC-6 (item 3). Same setup but the pool is free: the outcome is recorded and the
response is the granted redirect (control case, so AC-5 cannot pass vacuously). [integration]

AC-7 (item 4). Given a real HTTP server with the production middleware chain
and a handler that writes a first chunk, flushes, and then blocks until the
client confirms it received that chunk, when the client reads, then the first
chunk arrives before the handler returns (bounded wait 2 s). Fails today
(the chunk only arrives when the handler returns). [integration — `httptest.Server`]

AC-8 (item 4). Given a handler that flushes before writing anything, then the
client receives status 200 and the access log records 200. Given the budget
has already answered, a later flush sends nothing and does not panic. [unit]

AC-9 (item 5). Given a booking whose completion unit is sending the
confirmation email to an SMTP server that accepts the connection and never
replies, when the shutdown's hard cancel fires, then the unit returns within
the cancel grace (5 s, not the 15 s SMTP limit), the email failure is logged at
error level with the booking id and no email address, and it is not retried. Fails today. [integration — shutdown sequence with short phases]

AC-10 (item 5). Given a completion unit whose host-event record write is blocked
(the table locked by another transaction) when the hard cancel fires, then the
unit returns within the cancel grace, the abandoned/failed line names that host
as not done, and closing the pool after shutdown does not wait for the blocked
write. Fails today. [integration]

AC-11 (item 5). Given a booking saved during the drain (before the hard
cancel), when a host's event is created after the per-call limit has elapsed
for the request but before the hard cancel, then its id is still recorded
(existing "an answer that arrived is always recorded" behaviour; covered today
by `TestPAC81_BookingCommittedDuringDrainCreatesHostEvents`,
`engine/pac81_review_test.go:27`, which must still pass). [integration]

## 5. Explicitly out of scope

- An email outbox or retry of booking emails (owner Q-O4, 2026-11-01; PAC-85).
- Changing the step-up denial reason shown when the budget answers at the
  record step (contract enum change; see OQ-2).
- The team-invite email's cancellation by the hard cancel. It shares the same
  transport, and the item-5 change may make it trivial, but its handler is
  rewritten by PAC-71 (#242); a follow-up after PAC-71 merges.
- Other `db.Conn` call sites (operator and migration paths).
- Any new streaming endpoint; item 4 only makes flush work.

## 6. What must not change

- Pool cap 1 never deadlocks (AC-A10): `TestPAC81_PoolCapOnePaths`,
  `TestPAC81_HandlerGoroutineReturnsAtBudgetWithPoolHeld`.
- Booking budget semantics (AC-A19): `TestPAC81_BookingBudgetBeforeInsertIs503NoRow`,
  `TestPAC81_BookingInsertHeldPastBudgetIs201`, `TestPAC81_BookingInsertFailsAfterBudgetIs500`
  (`api/pac81_paths_test.go:178-260`), `TestPAC81_BookingPoolSaturatedBeforeInsertIs503`
  (`engine/pac81_review_test.go:97`).
- Access log: route pattern, no token, "-" for client gone, 503 at expiry,
  replaced status (`TestPAC81_AccessLogRoutePatternAndNothingWritten`,
  `api/pac81_middleware_test.go:117`).
- The budget never exposes the raw writer: no `Unwrap` on `budget.writer`, and
  Hijack stays refused (`internal/budget/budget.go:266-292`). Item 4 must not
  add a path to write around the budget.
- Drain behaviour of the completion unit (`TestPAC81_BookingCommittedDuringDrainCreatesHostEvents`,
  `TestPAC81_BookingCompletionAbandonedAtDrainIsLogged` `engine/pac81_test.go:559`,
  `TestPAC81_BookingEmailFailureLogged` `:619`).
- Step-up records on a reserved connection, detached from the budget once committed
  (PAC-50 tests in `api/pac50_sso_stepup_test.go`).

## 7. Rollback

No migration, no contract change, no configuration change. Reverting the PR(s)
restores today's behaviour exactly. Items are independent; any one can be
reverted alone.

## 8. Decisions and open questions

| ID | Decision | Kind |
|----|----------|------|
| D-1 | Item 1: release on every exit path including a panic, *and* before the completion unit starts; a plain function-scoped `defer` (the issue's wording) is rejected because it deadlocks a pool of 1. | factory-assumed |
| D-2 | Item 2: "-" only when the request context ended before anything was written; otherwise an unwritten response logs 200. A request cancelled by the server at the end of the drain also logs "-" (the server closes the connection right after, so nothing reliable was sent). | factory-assumed |
| D-3 | Item 3: test-only. The test pins today's budget answer (a denied redirect) and "nothing stored"; it asserts the reason only as "a denied redirect", so OQ-2 can change the reason without rewriting it. | factory-assumed |
| D-4 | Item 4: make flush work (forward it), rather than documenting a no-op. Forwarding must go through the standard unwrap mechanism so wrappers added later (PAC-71's guarded writer) are traversed too. | factory-assumed |
| D-5 | Item 5: cut both steps at the hard cancel only, not at the request's per-call limit; "an answer that arrived is recorded" keeps holding during the drain. A cut email is a logged failure (AC-A8), never retried (Q-O4). | factory-assumed |
| D-6 | Split: items 1–4 ship as one small PR now (no overlap with PAC-88's changed regions). Item 5 ships as a separate PR **after PAC-88 merges**, because PAC-88 rewrites the confirmation-email path it must change (§9). | factory-assumed |

Open questions:

- **OQ-1 (factory, implementer)**: item 5 needs a context-aware SMTP send. Either
  a context parameter on the shared sender (also used by team invites) or a
  booking-only variant. Resolve at the contract/plan stage; default: one shared
  context-aware sender, team-invite caller passes its existing context unchanged.
- **OQ-2 (factory, follow-up ticket)**: at the step-up record step the budget's
  answer says `reason=not_admin` for an admin whose checks passed (§2 item 3).
  A distinct "try again" reason would need a contract enum change and a web
  message. Not product-level enough to ask the owner now; propose a follow-up
  ticket and let the owner prioritise it.

No owner (product-level) questions: all five items are internal correctness of
already-decided behaviour.

## 9. Overlap with open PRs (checked with `git diff origin/main...<branch>`)

| PR / branch | Touches | Overlap with PAC-90 |
|---|---|---|
| PAC-71 #242 `impl/pac-71-identity-binding-backend` | `api/middleware.go` (+155: `guardedResponseWriter`, `guarded`), `api/handlers_sso_stepup.go` (+46: identity re-check in `checkStepup`), `api/handlers_teams.go`, `api/routes.go` | **Item 2/4**: same file as `statusWriter`/`loggingMiddleware`, different hunks; its new writer has `Unwrap` but no `Flush`, so AC-7 should run with a route behind `requireAuth` once both land. **Item 3**: `finishStepup` itself unchanged (shifts ~+20 lines), but the step-up fixture must satisfy PAC-71's identity re-check if PAC-71 lands first. Textual conflict risk: low; semantic: rebase the item-3 test. |
| PAC-87 `impl/pac-87-api` (pushed, f0b3168) | settings, org AI, storage | none |
| PAC-88 `impl/pac-88-api` | `engine/booking.go` (+244/−92): slot listing by day, `sendBookingEmail`/`sendConfirmationEmail` gain a time zone, call site at the email step | **Item 5**: direct overlap (the email call and signature). **Item 1**: `ConfirmBooking`'s reservation block (main `:386-408`) is outside PAC-88's hunks — no conflict expected. Hence D-6. |
