# Spec — PAC-82: booking completion and idempotency (concurrent double booking, safe retry, single-use / max-uses race)

- **Linear**: https://linear.app/paceday/issue/PAC-82
- **Status**: accepted (self-challenged 2026-11-14, accept-with-changes; changes applied in place, see the Challenge log at the end). Contract-stage amendments acknowledged and AC-27 amended by the orchestrator at stage 4, 2026-11-14 (see "Stage 4 — orchestrator decisions" at the end).
- **Author**: spec-author
- **Base**: origin/main `d520775` (after PAC-101 `b98b6a8`, PAC-40 part B `d520775`, PAC-72, PAC-81 Part A, PAC-90). Web: smart-calendar-flow origin/main `d935bcc`.
- **Scope sources**: the issue body (items 1-3); the issue comment of 2026-11-14 folding in PAC-101 OQ-2 (the single-use / max-uses race, PAC-40 spec OQ-4 case 1) with the constraint that PAC-101 AC-16 holds unless this spec amends it explicitly; PAC-83 §3.5 (`docs/specs/PAC-83.md:436-440`), which hands this spec the revisit of the "may have been saved" wording.
- **Split**: issue item 3 (provider-side idempotency for host calendar events and personal-sync blockers) is **not** in this spec. See §5 and OQ-1.
- **Inherited scope**:
  - No open `docs/factory/api-audit.md` finding on `POST /api/book/{slug}` (every row for it is Fixed: API-051, API-073, API-094, ...). No `x-uncertain` marker in `contracts/openapi/paths/booking.yaml`.
  - **Stale contract text (touch-it-fix-it)**: the `createPublicBooking` description still says "Still open (API-051, owned by PAC-40): `uses_count` is recomputed from live bookings, so cancelling a booking re-opens an exhausted single-use link" (`contracts/openapi/paths/booking.yaml:157-159`). API-051 is Fixed and the count includes cancelled bookings (`storage/scheduling_links.go:29-31,130`; the 410 description at `booking.yaml:258-261` already says so). The sentence must go. Same description, `booking.yaml:154-157`: "Not guaranteed: two concurrent requests ... can both pass the check" is what this spec fixes, and the 201's "A retry behaves as before PAC-81 ... no request returns another request's booking" (`booking.yaml:207-210`) changes meaning (AC-9).

## 1. Problem

Three failures share one cause: a public booking is decided by checks that run
before the save, outside anything that orders it against other bookings.

1. **Double booking.** Two visitors who pick the same time on the same link (or
   two links that share a host) can both be told "You are confirmed!". Both
   bookings are stored, every host gets two calendar events at the same time,
   and two people turn up to one meeting. It needs no attacker: two people
   clicking within the few seconds the server spends reading host calendars is
   enough.
2. **Single-use and max-uses links overrun.** A single-use link exists so that
   exactly one person can book it (an interview slot, a one-off meeting). Two
   concurrent bookings both pass "is the link used up?" and both are stored. A
   link capped at N bookings ends with more than N.
3. **A booker cannot safely retry.** When the answer to a booking is lost (the
   network drops, a proxy times out, the server answers a 500 after the insert
   was sent), the booking may or may not exist. Today the web says "It may have
   been saved — check your email before booking again" and offers no retry
   (PAC-83). If the visitor books again anyway, either the retry is refused as
   "slot is already booked" (their own booking took it) — telling them the
   opposite of the truth — or, if the first request was not saved, nothing tells
   them so except the absence of an email. The visitor is left guessing at the
   one moment they most need a clear answer.

## 2. Current behaviour (origin/main `d520775`)

### 2.1 The booking request, in order

`POST /api/book/{slug}` is `bookingHandlers.createBooking`
(`backend/api/handlers_booking.go:324`):

| Step | Where | Notes |
|---|---|---|
| rate limit (429) | `handlers_booking.go:327` | counted first |
| link read by slug (404) | `:333`, `storage/scheduling_links.go:125-135` | live links only (`active AND deleted_at IS NULL`); `uses_count` is a `COUNT(*)` of every booking of the link, cancelled included (`:130`) |
| exhausted (410) | `:342`, `linkExhausted` at `:80-88` | single-use: `UsesCount >= 1`; recurring with `max_uses`: `UsesCount >= MaxUses`. Evaluated on the **link read above**, once |
| body and validation (400) | `:349-411` | name/email trimmed; `notes` stored as sent |
| link zone (500) | `:425-430` | |
| cheap refusals (409: horizon, hours, notice) | `:431-435` | |
| same-link overlap (409 `taken`) | `:436-443`, `storage.HasOverlappingBooking` at `scheduling_links.go:651-658` | plain `SELECT EXISTS` on the pool, no lock |
| offered-slot check (409 `host_busy`) | `:444-468` | reads every accepted host's calendar, focus blocks and bookings on every link the host owns or co-hosts (`engine/booking.go:292-369`, `storage.GetBusyBookingsForUser` at `scheduling_links.go:689`) — seconds of provider calls |
| `ConfirmBooking` | `:470`, `engine/booking.go:436-513` | re-reads accepted hosts (`:444`), re-reads each host calendar for readability only (`:455-470`), takes a connection under the budget (`:478`), `budget.Commit` (`:485`), then the save |
| the save | `insertBookingOnConn` → `storage.SaveBookingOnLiveLink` (`scheduling_links.go:529-574`) | one transaction: `SET LOCAL lock_timeout` (10 s), then `INSERT … SELECT … FROM scheduling_links WHERE id=$1 AND active AND deleted_at IS NULL FOR SHARE OF l` (`:551-560`). **No overlap check, no exhaustion check, no other lock** |
| completion unit | `engine/booking.go:507`, `startCompletion` `:627-639` (`lifecycle.Go`, kind `booking-completion`) | per host `CreateEvent` (`:683`), then the confirmation email (`:701`); the handler waits for it until the budget ends (`:508-511`) |
| 201 | `handlers_booking.go:491-497` | body built from the stored row plus the link's current title and hosts |

### 2.2 How concurrent bookings of one slot are (not) prevented

- The only same-slot guards are the **pre-save reads**: `HasOverlappingBooking`
  (same link) and the offered-slot computation (any link of any accepted host).
  Both run on the pool, outside any transaction, before `ConfirmBooking`.
- The save holds the link `FOR SHARE`, which by design does **not** conflict
  with another booking's `FOR SHARE` (PAC-101 spec §9.2,
  `docs/specs/PAC-101.md:440-458`; AC-16 at `:341-343`: "neither waits for the
  other").
- No database constraint: `bookings` has a primary key and the `link_id` FK
  only (`storage/migrations/009_scheduling_links.up.sql:26-36`, index
  `idx_bookings_link` `:50`). No uniqueness or exclusion on time.
- **Race window**: from request A's overlap/offered-slot reads
  (`handlers_booking.go:436-468`) to A's commit (`scheduling_links.go:570`).
  That window contains every host calendar read twice (listing rule, then
  `ConfirmBooking`'s readability read), each up to 15 s (`HostReadLimit`), so
  it is seconds wide in normal operation. Any request B whose reads fall in A's
  window and whose save commits after A's reads saw nothing double-books. Cross-
  link: the same holds for two links sharing a host (owner or accepted co-host),
  because busy time includes bookings on every link the host hosts
  (`scheduling_links.go:682-700`), and the save checks none of it.
- The contract states it: "Not guaranteed: two concurrent requests for the same
  or overlapping starts can both pass the check before either is written"
  (`contracts/openapi/paths/booking.yaml:154-157`).

### 2.3 Single-use / max-uses enforcement

- `uses_count` is not stored; it is a `COUNT(*)` over the link's bookings at the
  link read (`scheduling_links.go:112,130,147,160,194`). Cancelled bookings
  count (PAC-40 D-6, `:29-31`). No product path cancels a public booking today
  (no `UPDATE bookings SET status` outside tests).
- The check is `linkExhausted` on that one read (`handlers_booking.go:342`).
  Nothing re-checks at the save. Two requests on a single-use link with 0
  bookings both read `uses_count = 0`, both pass, both save. Same for
  `max_uses = N` with N-1 stored.
- PAC-101 left this open on purpose (spec §5, `docs/specs/PAC-101.md:357-360`;
  OQ-2 folded into this ticket).

### 2.4 Idempotency today

- None. No request header, body field or column identifies a booking attempt
  (no `idempoten*` in `backend/api`, `backend/storage`, `contracts/openapi/paths/booking.yaml`,
  or smart-calendar-flow `src/` outside generated text).
- CORS allows only `Content-Type, Authorization` request headers
  (`backend/api/middleware.go:250`); a cross-origin browser could not send any
  other header today.

### 2.5 What a client retry does today

Given request A was saved and its answer lost, a retry B with the same body:
- passes 429/404; if A used up a single-use / max-uses link, B is **410**
  "link is no longer accepting bookings" (`handlers_booking.go:342`);
- otherwise, B's same-link overlap read sees A: **409** (generic body, or
  `taken` with specific refusals) (`:436-443`);
- if B arrives while A is still between its reads and its commit (A's answer
  can be lost while A is still running: the web's bound is 135 s,
  `smart-calendar-flow src/api/client.ts:84`, the server's budget 90 s), B can
  pass every check and **save a second booking** at the same time (§2.2).
- So a retry is either told the opposite of the truth (409/410 for a booking
  that exists) or double-books. The contract says so ("A retry behaves as
  before PAC-81: 409 ... 410 ...; no request returns another request's
  booking", `booking.yaml:207-210`).

### 2.6 The PAC-90 completion unit

After the save commits and its connection is released
(`engine/booking.go:531-555`, PAC-90 item 1), `startCompletion`
(`:627-639`) runs `completeBooking` (`:642-713`) as one tracked
`lifecycle` unit: one `CreateEvent` per accepted host on the detached per-item
pool (`:665-690`, recorded in `booking_events` when an answer arrives), then one
confirmation email (`:701`), never retried (`:698-706`). The handler waits for
the unit until the budget ends, then answers 201 anyway (`:507-512`). A unit is
started once per **saved** booking; a `ConfirmBooking` that returns an error
starts none.

### 2.7 Callers

- **Web** (smart-calendar-flow origin/main `d935bcc`):
  `schedulingLinksApi.bookSlot` (`src/api/schedulingLinks.ts:539-556`) posts
  `{name, email, start, end, duration, notes}` through `requestApi`
  (`src/api/client.ts:588-606`), which sends only `Content-Type` and has no way
  to add a header. `PublicBooking.tsx` `bookMutation` (`:309-357`): `retry:
  false`; 409 → refetch the day, clear the slot; 410/400/429 → message; budget
  503 → "not made", the booker may press Book again; lost answer or any other
  5xx → `API_MESSAGES.lostBooking` ("We couldn't confirm your booking. It may
  have been saved — check your email for a confirmation before booking again.",
  `src/api/client.ts:418`), keep details, re-read the day, **no control
  re-sends** (PAC-83 AC-21, AC-25; tests
  `src/pages/PublicBooking.pac83.test.tsx:184-268`).
- **MCP**: no booking tool (`mcp/tools.go` has no `/api/book` call; its only
  "book" text is the rooms search description at `:208`).
- **e2e**: `e2e/tests/edge.spec.ts:86` and the PAC-72/PAC-40 journeys post
  bookings without any idempotency information. These must keep working.

## 3. Desired behaviour

1. **One slot, one booking.** However many requests arrive at once, a host is
   never booked twice for overlapping time through public booking links,
   whether the requests use the same link or two links that share the host.
   Exactly one request wins; the others get the same refusal they would have
   got had they arrived after it (409 `taken` on the same link, `host_busy` on
   another link), and write nothing.
2. **A use limit is a limit.** A single-use link stores at most one booking and
   a link capped at N stores at most N, whatever the concurrency. A request that
   loses gets the 410 it would have got had it arrived after the winner.
3. **Unrelated bookings still do not wait for each other.** Bookings of
   different, non-overlapping times on an unlimited link proceed in parallel
   (PAC-101 AC-16 keeps its meaning for those). Only bookings that could
   actually conflict — overlapping time for a common host, or any two bookings
   of a use-limited link — are ordered against each other, and only for the
   length of the save itself (no provider call is ever made while ordered).
4. **A booking attempt can be retried safely.** A client may label each booking
   attempt with a key of its choosing. Sending the same attempt again (same key,
   same booking details) never creates a second booking: if the first was
   saved, the answer is that booking, as a 201, even when the link has since
   been used up or the slot is now too soon; if it was not saved, the retry is
   handled as a new request. The same key with different details is refused and
   writes nothing, and never reveals the other request's details. A key is
   honoured for 24 hours. Requests without a key behave as today, plus items 1-3.
5. **A retry causes nothing twice.** A replayed answer creates no booking, no
   host calendar event and no email.
6. **The web turns "may have been saved" into a safe "Try again".** When the
   outcome of a booking is unknown, the visitor sees that it may have been saved
   and is offered one button that re-sends exactly the same attempt. Pressing it
   either shows the confirmation (whether the first or the retry saved it), or
   a definite refusal. Nothing is ever re-sent without the visitor pressing the
   button. Changing the time or any detail starts a new attempt.

## 4. Acceptance criteria

Unless stated, "a booking" is a public booking request that passes every check
other than the one under test. Concurrency criteria are proven by tests that
force the interleaving with the existing seams (or new ones) and a real
PostgreSQL, never by sleeps alone.

**Concurrent same-slot prevention**

AC-1. Given a live, unlimited link and two requests for the same slot from
different bookers, when both have passed every pre-save check and are then
released to save at the same time, then exactly one answers 201, the other
answers 409 (`taken` when the link's refusals are specific, the generic body
otherwise), exactly one booking row exists, exactly one completion unit ran
(one event per host, one email). [unit]

AC-2. Given two live links L1 and L2 that share a host H (owner of one,
accepted co-host of the other, or owner of both), and two requests for
overlapping (not necessarily equal) slots, one on each link, held past their
pre-save checks and released together, then exactly one answers 201, the other
answers 409 (`host_busy` when specific), and exactly one row exists. [unit]

AC-3. Given AC-1's setup with 20 concurrent requests released together, then
exactly one 201, nineteen 409s, one row, and no request answers 500 or fails
with a deadlock (SQLSTATE 40P01) or lock timeout (55P03). [unit]

AC-4. Given a request R whose save is in progress (held inside its save after it
has taken whatever ordering it takes) and a second request S for an
overlapping slot sharing a host, then S's save waits for R and, when R commits,
S answers 409 and writes nothing; when R rolls back instead (injected failure),
S answers 201. [unit]

**Unrelated bookings do not wait (PAC-101 AC-16, amended)**

AC-5. Given a live link with no use limit and two requests whose slots are at
least 15 minutes apart (end of one to start of the other), when both saves are
held open at the same time, then neither waits for the other (no session in a
lock wait, observed in the database). This replaces PAC-101 AC-16 ("different
free slots"), which held for any two different slots; two different slots less
than 15 minutes apart may now wait for each other for the duration of a save.
[unit]

AC-6. Given two links with no host in common, and two requests for the same
time, one on each, when both saves are held open, then neither waits. [unit]

**Use limits under concurrency**

AC-7. Given a single-use link with no booking and two requests for different,
non-overlapping slots released together past their pre-save checks, then
exactly one answers 201 and the other 410 "link is no longer accepting
bookings", and one row exists. Same with `max_uses = N` and N-1 bookings
stored. With `max_uses = N` and N-2 stored, both answer 201. [unit]

AC-8. Given a link with a use limit, two bookings of it (any times) may wait for
each other during their saves; this is the explicit amendment of PAC-101 AC-16
for use-limited links. A test pins that the second save waits while the first
is held, and that the wait ends when the first commits. [unit]

**Idempotent retry**

AC-9. Given a request with key K saved booking B and answered 201, when the same
request (same key, same link, same name, email, start, end and notes after the
server's normalisation) is sent again within 24 hours, then the answer is 201
with the same booking id, start, end, duration, booker name, booker email and
notes as B; no new row, no host calendar event, no email is created; the
row count is unchanged. [unit]

AC-10. Given AC-9's B on a single-use link (so the link is now used up), or B's
start now inside the link's minimum notice, or B's slot now busy for a host,
then the same retry still answers AC-9's 201 (the replay is decided before the
410, the notice check, the availability check and the 409 overlap check). [unit]

AC-11. Given B saved with key K, when a request with key K on the same link
differs in any one of name, email, start, end (or the resolved duration), or
notes, then the answer is 409 with a body distinct from every slot-refusal body
(and the same whatever the link's refusal setting), nothing is written, and the
body contains none of B's details. One test per differing field. [unit]

AC-12. Given B saved with key K more than 24 hours ago, when a request with key
K on the same link is sent, with the same or different details, then the answer
is AC-11's 409 and nothing is written. [unit]

AC-13. Given key K used on link L1, when a request with key K is sent to a
different link L2, then it is handled as a new request on L2 (a key is scoped to
its link). [unit]

AC-14. Given request A with key K held after its pre-save checks and before its
save, when a retry A' with key K and the same details runs to completion, then
A' answers 201 and creates the booking; when A is released it answers 201 with
the **same** booking id, writes nothing and starts no completion unit; exactly
one row, one completion unit. [unit]

AC-15. Given request A with key K held inside its save (after its ordering,
before commit), when the retry A' with key K and the same details reaches its
save, then A' waits; when A commits, A' answers 201 with A's booking id and
writes nothing; when A rolls back instead, A' saves and answers 201 with a new
id. Exactly one row either way. [unit]

AC-16. Given two requests with key K on the same link, different details,
released together, then exactly one answers 201 and the other AC-11's 409; one
row. [unit]

AC-17. Given a key that is empty, then the request is handled as one without a
key. Given a key outside the accepted format (contract-defined, D-6), then 400
and nothing is written, after the 404 and before any other check. A body that
fails validation answers its 400 whether or not a key is sent. [unit]

AC-18. Given a request without a key, then every answer is as on main except
where AC-1..AC-8 change it (the order of 429, 404, 410, 400, 409 is unchanged
for keyless requests). [unit]

AC-19. Given a link deleted or paused after B was saved, when B's retry with
key K arrives, then the answer is the 404 of a deleted link (PAC-101), nothing
is written. [unit]

AC-20. Given a browser on the allowed origin, when it sends a booking with a key
cross-origin, then the preflight allows the key header and the booking
succeeds. [e2e]

**Migration**

AC-21. Given a database at main's schema containing two non-cancelled bookings
of one link with identical times, and two overlapping bookings on two links
sharing a host, when the server starts and applies the new migration, then the
migration succeeds, every existing row is unchanged, and a new booking
overlapping those rows is refused with the 409. [unit]

AC-22. The down migration runs on a database containing keyed bookings and
loses only the keys (every booking row survives). [unit]

**Lock waits and deadlocks**

AC-23. Given a save waiting on another booking's save longer than the link lock
bound (shortened in the test), then it answers the 500 `internal error`,
writes nothing, and the waiting is bounded by the same 10 s bound as PAC-101.
[unit]

AC-24. Given a mix of concurrent bookings (shared hosts, overlapping and
non-overlapping slots, a use-limited link), link deletes, pauses, PATCHes with
co-host changes, co-host accept/leave and a PAC-40 part B erasure run, then no
request fails with SQLSTATE 40P01 or 55P03, every 201 has its row, every
refusal wrote nothing, and no host has two overlapping non-cancelled bookings
created during the run. (Extends PAC-101 AC-15.) [unit]

**Web (smart-calendar-flow)**

AC-25. Given a booker presses Book, then the request carries a fresh key; given
the booker then changes the time, the duration, the name, the email or the
notes and presses Book again, the new request carries a different key. [unit]

AC-26. Given a booking whose answer is lost (bound reached, network failure,
non-JSON answer, any 5xx other than the budget 503), then the page says the
booking may have been saved and shows one "Try again" control; exactly one
request was sent until it is pressed; pressing it sends exactly one request
with the same key and the same body as the first. Nothing re-sends without the
press (reconnect, focus, timer: PAC-83 AC-25 holds). [unit]

AC-27 (amended at stage 4 by the orchestrator's C-8 decision, 2026-11-14).
Given the "Try again" request answers 201, then the confirmation is shown
(whether the first or the retry saved the booking). Given it answers a slot
409 or a 410, the page says exactly "This time can't be booked now. Your
earlier attempt may still have been saved — check your email before booking
another time." (it never claims the earlier attempt was not saved) and offers
no further retry of that attempt. Given it answers AC-11's 409 or a 404, the
page says the booking may have been saved and to check email, and offers no
further retry of that attempt. Given it is lost again, any 5xx (the budget 503
included, because the first send is still unresolved), a 429 or a 400, the
"may have been saved" message and "Try again" stay. [unit]

AC-28. Given the e2e stack, when the first booking's answer is dropped by
request interception after the server saved it, and the booker presses "Try
again", then the confirmation is shown and exactly one booking exists for that
slot. [e2e]

## 5. Explicitly out of scope

- **Issue item 3: provider-side idempotency** for host calendar events and
  personal-sync blockers (an event created at the provider whose answer timed
  out). Split out (OQ-1). After this spec, a replay never starts a second
  completion unit (AC-9, AC-14), so host events are not duplicated by booking
  retries. The remaining duplicates come from the personal-blocker sync
  re-creating a blocker whose create answer was lost
  (`engine/personal_blocker.go:145-158` records the blocker only after the
  provider answers), and an orphan host event when `CreateEvent` times out after
  the provider accepted it (`engine/booking.go:683-689`). Both need provider
  client-supplied ids (Google event `id`, Microsoft `transactionId`): a
  different subsystem and its own tests.
- **Conflicts with calendar events or focus blocks written concurrently** by
  something other than a public booking (a host adding a meeting in Google
  Calendar during the booking). The provider is outside the database; the
  existing readability re-read stays the only guard.
- **Owner-side booking paths.** None exists that writes bookings (no owner
  create, no cancel).
- **A retry after the page was reloaded or the tab closed.** The key lives for
  the attempt in the page only (D-8). A visitor who reloads starts a new
  attempt; if the first was saved, the new one is refused as taken and the
  visitor relies on the email, as today.
- **Storing the visitor's time zone or details across visits** (PAC-88 owner
  OQ-2: not remembered).
- **Rate limits.** A retry counts against the 20 / 10 min abuse limit like any
  request (owner, booking rate limits are abuse protection).

## 6. What must not change

- **PAC-101 commit point** (`docs/specs/PAC-101.md` §9.2-9.5): the liveness
  check and the save stay one transaction under the link `FOR SHARE`; a link
  deleted or paused first is the 404 with nothing written; the lock wait bound
  (10 s, `storage.LinkLockTimeout`, `scheduling_links.go:479-484`) applies to
  every new wait. Tests: the PAC-101 suite (`backend/storage/*pac101*`,
  `backend/api/*pac101*`) including AC-15's soak. AC-16 is amended by AC-5/AC-8
  and its test must be updated, not deleted.
- **PAC-81 AC-A19 / PAC-90**: 503 only before the insert is sent; once sent,
  201 or 500; the save's connection is released before the completion unit
  starts; a panic in the save does not block (`engine/booking.go:515-555`).
  Tests: `backend/engine/*pac90*`, `*pac81*`.
- **PAC-72 rule and 409 bodies**: the listing/booking equivalence, the reason
  precedence (`beyond_horizon`, `outside_link_hours`, `too_soon`, `taken`,
  `host_busy`), generic vs specific bodies (`booking_rule.go:127-148`). Tests:
  `backend/api/pac72_*_test.go`.
- **PAC-72 D-6 (owner)**: an unreadable host calendar is busy.
- **PAC-40 D-6 / part B**: `uses_count` counts cancelled bookings; erasure's
  lock order (link `FOR UPDATE` → bookings → owner user) and result.
- **PAC-83** budget-503 meaning ("not made") for a first send; the client
  bound; no automatic resend (AC-25 of PAC-83). PAC-83 AC-21's "no control
  re-sends the booking in one click" is **amended** by AC-26 (this spec owns it,
  `docs/specs/PAC-83.md:436-440`).
- **Keyless callers** (e2e journeys, any external client): AC-18.

## 7. Rollback

- Server: revert the commit; the down migration drops the key storage only
  (AC-22). Bookings are never deleted. After rollback a retried key is a new
  request (today's behaviour) and the concurrency guarantees are gone.
- Web: revert independently; a web without keys works against the new server
  (keys optional). A keyed web against an old server: the old CORS rejects the
  header on cross-origin deployments (same-origin deployments ignore it), so
  the web must ship after the server (paired merge, server first).

## 8. Open questions

- **OQ-1 — triage (orchestrator), not owner**: file issue item 3 as its own
  ticket ("provider-side idempotency for host events and personal-sync
  blockers"). Resolution: a new Linear issue linked to PAC-82. Factory default:
  split, as above.
- **OQ-2 — contract stage**: verify on PostgreSQL 16 that `lock_timeout` bounds
  a wait on the ordering in D-2 (expected: yes, it is a lock wait like any
  other) and record the observation as PAC-101 did. If it does not, the
  contract must bound the wait another way; AC-23 is the test.
- **OQ-3 — contract stage**: the exact 409 body for AC-11/AC-12 and how it sits
  next to `BookingSlotUnavailableError` in the 409 response (a second message
  value or a separate schema), so generated web types can tell it apart.

No owner question: every decision below is technical or follows an existing
owner decision; the visible change (a "Try again" control) is the one PAC-83
already assigned to this ticket.

## 9. Mechanism and lock analysis (binding constraints for the contract and plan)

### 9.1 Why not an exclusion constraint

An `EXCLUDE USING gist (link_id WITH =, tstzrange(start_time, end_time) WITH &&) WHERE (status <> 'cancelled')`
was considered and rejected:
1. It cannot be added to a table that already holds overlapping rows, and
   PostgreSQL has no `NOT VALID` for exclusion constraints; migrations run at
   startup and must succeed on existing duplicates (issue constraint). A
   migration that first "repairs" duplicates would cancel real bookings.
2. It is per link. Busy time is per **host** across every link the host owns or
   co-hosts (PAC-72 D-11, `scheduling_links.go:682-700`); bookings carry no host
   column, and the host set of a link changes. AC-2 cannot be met by it.
3. It needs `btree_gist`, an extension the migrations do not create today.

### 9.2 Chosen: ordered transaction-scoped locks plus in-transaction re-checks

The save transaction becomes (each step on the save's connection, after
`budget.Commit`, detached as today):

1. `SET LOCAL lock_timeout` (unchanged, PAC-101).
2. Lock the link `FOR SHARE` where `active AND deleted_at IS NULL`, reading its
   `usage_type`, `max_uses`, `owner_user_id`; zero rows → link gone (404).
   (PAC-101 §9.2 allows this two-statement form.)
3. Read the link's host set: owner plus accepted co-hosts (plain read).
4. Take **transaction-scoped advisory locks**, in ascending key order, on:
   - for each host in the set and each 15-minute UTC bucket that the slot
     `[start, end)` touches: one key derived from (host id, bucket index);
   - when the link has a use limit (single-use, or recurring with `max_uses`):
     one key derived from the link id.
   Keys live in a key space of their own (distinct from the encryption
   maintenance locks, `storage/encryption_keys.go:21,176,200`). A hash
   collision only adds a wait, never a wrong answer, because the order is by the
   key value itself.
5. When the request carries a key: look up the link's booking with that key.
   Found → decide the replay (match → replay; differs or older than 24 h → 409),
   roll back, done. No completion unit.
6. When the link has a use limit: count the link's bookings (all statuses);
   at the limit → 410, roll back.
7. Overlap re-check: any non-cancelled booking overlapping `[start, end)` on any
   link that any host in the set owns or co-hosts (accepted) → 409, `taken` if
   on this link else `host_busy`; roll back.
8. Insert, carrying the key when present. A unique violation on (link, key)
   (a same-key request with different details that took different locks) →
   roll back and resolve as in step 5 against the stored row.
9. Commit (releases every lock).

Why it is correct:
- Two overlapping intervals share at least one instant, hence one bucket; two
  conflicting bookings share a host; so they share a lock key and serialize.
  The second's step 7 runs after the first committed (xact locks are released at
  commit, and READ COMMITTED takes a new snapshot per statement), so it sees
  the first booking. Same reasoning for the use-limit key and step 6.
- Slots at least 15 minutes apart touch disjoint buckets (AC-5); unlimited links
  with no common host share no key (AC-6).
- The pre-save checks stay (they give the cheap, early refusal and read host
  calendars, which must never happen under a lock). Steps 5-7 make the
  decision authoritative.
- The conflict rule in step 7 must be exactly the bookings half of the PAC-72
  rule (non-cancelled, any link of any accepted host, raw overlap — buffers
  apply to calendar events only). If that rule ever widens bookings (buffers),
  the buckets must cover the widened interval.

### 9.3 Deadlock analysis

Lock order of the booking save: link `FOR SHARE` → advisory keys (ascending) →
the new booking row (and its unique-index entry). Every advisory key is
acquired before any statement that can wait on a row or index entry; the reads
in steps 3, 5, 6, 7 are plain MVCC reads and never wait.

| Pair | Can they wait on each other? | Cycle? |
|---|---|---|
| booking / booking, same link | advisory keys (ascending, total order); the unique index on (link, key) only after both hold all their keys | No: a waiter on keys holds a prefix of the order; a waiter on the index holds all its keys, and the holder it waits for is past its key phase |
| booking / booking, links sharing a host | same as above; each holds only its own link's `FOR SHARE`, never the other's | No |
| booking / delete or pause (`NO KEY UPDATE`) | booking waits on the link at step 2 before any key; delete waits on a booking holding `FOR SHARE` | No: the delete takes nothing a booking waits for after step 2 |
| booking / PATCH (link `NO KEY UPDATE`/`UPDATE` → host rows) | as delete; the booking never locks a host row | No |
| booking / invite, accept, leave (`FOR SHARE` → host row) | compatible on the link; the booking never locks host rows | No |
| booking / PAC-40 erasure (link `FOR UPDATE` → bookings → owner `KEY SHARE`) | the booking waits at step 2 or holds `FOR SHARE` (erasure waits); erasure takes no advisory key; it updates only rows of its deleted link, which a booking cannot insert into (link gone → 404) | No |
| booking / users-row writers (PAC-71) | the booking locks no user row (FK only to links) | No |

PostgreSQL's detector would turn a missed cycle into a 500; AC-24 asserts none.
Waits are bounded by the 10 s link lock bound (OQ-2, AC-23). No network call is
made inside the transaction (PAC-101 §9.4 holds).

### 9.4 Idempotency key

- Sent as a request header (the IETF `Idempotency-Key` header,
  draft-ietf-httpapi-idempotency-key-header), optional. The CORS allow-list
  must include it (AC-20).
- Generated by the **web, once per attempt** (a cryptographically random UUID):
  a new key whenever the slot or any detail changes; the same key only for the
  "Try again" of that exact attempt. Kept in page memory only.
- Stored on the booking row itself (nullable), unique per (link, key) where
  present. The migration adds a nullable column and a partial unique index:
  every existing row has no key, so it succeeds on any existing data (AC-21).
- Replay match: link, key, and the server-normalised name, email, start, end
  (instants) and notes all equal the stored row's. The response is built from
  the stored row (it can only echo what the caller sent), with the link's
  current title and hosts, exactly as a 201 is built today.
- TTL: 24 hours from the stored booking's creation; afterwards the key is
  refused (AC-12). Nothing is deleted at expiry; the key stays on the row
  (it is random and carries no personal data). PAC-40 erasure need not touch it.
- Order for keyed requests: 429 → 404 → key format 400 → body 400 → replay
  decision → 410 → zone 500 → refusals 409 → host reads → save (whose step 5
  decides the replay authoritatively). Keyless requests keep today's order.

## Decisions

| ID | Decision | Status |
|---|---|---|
| D-1 | Concurrent-booking prevention by ordered transaction-scoped advisory locks per (host, 15-minute bucket) plus in-transaction re-checks (§9.2), not an exclusion constraint (§9.1). | factory-assumed |
| D-2 | Locks are taken after the PAC-101 link `FOR SHARE` and before any write; order: link → advisory keys ascending → booking row. Waits bounded by `LinkLockTimeout` (10 s). | factory-assumed (fits PAC-101 §9.4) |
| D-3 | Use-limited links (single-use, or recurring with `max_uses`) serialize all their bookings on one per-link key; the count is re-checked in the transaction. Explicitly amends PAC-101 AC-16 for those links (as the PAC-82 issue comment requires); unlimited links keep AC-16 for slots ≥ 15 min apart. | factory-assumed |
| D-4 | The in-transaction overlap rule is the bookings half of PAC-72's rule (D-11: pending and confirmed bookings block; any link of any accepted host). | follows owner PAC-72 D-11 |
| D-5 | Idempotency key optional, per link, sent by the client in a request header; stored on the booking; partial unique index. | factory-assumed |
| D-6 | Key format: 16-255 characters from `A-Z a-z 0-9 - _` (a UUID fits); outside → 400; empty → absent. Exact bounds fixed by the contract. | factory-assumed |
| D-7 | Replay requires all stored booker fields to match after normalisation; any difference, or a key older than 24 h → one 409 body distinct from slot refusals, independent of the link's refusal setting, revealing nothing of the stored booking. | factory-assumed (issue constraint: partial match is a 409) |
| D-8 | The web generates a random key per attempt, keeps it in page memory only, re-uses it only for "Try again" of the identical attempt. | factory-assumed (consistent with PAC-88 owner OQ-2: nothing remembered across visits) |
| D-9 | The web's "may have been saved" message gains one user-pressed "Try again" that re-sends the same attempt; still no automatic resend. Amends PAC-83 AC-21 as PAC-83 §3.5 anticipated. | factory-assumed |
| D-10 | A replay is answered without waiting for, or starting, a completion unit, and sends nothing. | factory-assumed |
| D-11 | A retry on a link deleted or paused since the original save answers the PAC-101 404, not a replay. | factory-assumed (PAC-101: a gone link answers 404 everywhere) |
| D-12 | Issue item 3 (provider-side idempotency) split to its own ticket. | factory-assumed (OQ-1) |

## Challenge log — 2026-11-14 (spec-challenger, self-review)

**Verdict**: accept-with-changes; the changes below are applied in the body
above. Nothing blocking remains.

### Blocking (resolved in place)

1. **C-1 Same-key retry while the original is still before its save.** The first
   draft decided the replay only in the handler, before the checks. If A (key K)
   is still reading host calendars when A' (key K) arrives, A' finds nothing,
   saves, and then A saves a second booking (it already passed its pre-checks;
   only an overlap re-check would stop it, and A's answer would then be a 409
   for a booking that exists). Resolved: the replay is decided again inside the
   save after the locks (§9.2 step 5), the unique (link, key) index backs it
   (step 8), and AC-14 / AC-15 pin both orders.
2. **C-2 Same key, different details, no common lock.** Two same-key requests
   with different times share no advisory key, so step 5 can miss the other.
   Resolved: step 8 resolves the unique violation into the AC-11 409; AC-16
   tests it.
3. **C-3 Cross-link double booking was outside the first framing ("same
   slot").** The issue says "same slot", but busy time is per host across links
   (PAC-72 D-11), so a per-link fix leaves AC-2's case open. Resolved: the lock
   and re-check are per host; §9.1 records why an exclusion constraint cannot
   cover it.
4. **C-4 Web "not made" after a retry.** PAC-83 maps the budget 503 to "not
   made". On a "Try again", a 503 proves only that the retry saved nothing; the
   first send may still be in flight. Resolved: AC-27 keeps "may have been
   saved" for any 5xx on a retry.
5. **C-5 CORS.** A new request header is refused cross-origin by
   `middleware.go:250`; the web would fail on dev / preview deployments only.
   Resolved: AC-20, §9.4, rollback ordering in §7.

### Non-blocking

1. **N-1 Adjacent slots now wait briefly.** AC-16's "different slots" becomes
   "≥ 15 minutes apart"; adjacent slots touching a common bucket serialize for
   one short transaction. Accepted: no provider call is held, the wait is
   milliseconds, and AC-5 states the new boundary exactly.
2. **N-2 Owner removed as a host row.** The lock set is owner ∪ accepted
   co-hosts, a superset of the hosts whose busy time the listing uses; a
   superset only adds waits.
3. **N-3 Co-host accepting during a booking.** The host set is read in the
   transaction after the link lock; an acceptance committing after that read is
   not ordered against the booking. An acceptance never validated existing
   bookings before either; out of scope.
4. **N-4 Replay after erasure.** Erased booker fields never match, so a retry of
   an erased booking is the 409. Unreachable within the 24 h window (erasure
   runs ≥ 90 days after the last meeting).
5. **N-5 Stale contract sentences** (API-051 "still open", "not guaranteed",
   retry paragraph of the 201) listed under Inherited scope so the contract
   stage removes them.
6. **N-6 Keyed request order differs from keyless** (400 before 410 only with a
   key). Accepted: a keyless caller sees no change (AC-18); a keyed caller needs
   the body to decide the replay before the 410, which the issue requires.

### Criteria I could not falsify

- None. AC-5/AC-6 need a database-observed lock wait (pg_stat_activity
  `wait_event_type = 'Lock'`), as PAC-101's tests already do; AC-28 needs
  request interception after the server saved, which Playwright's route
  `fulfill`/`abort` after `fetch` provides.

### Citations I checked and found wrong

- None in this spec. Found in the contract: `booking.yaml:157-159` (API-051
  "still open") contradicts the code (`scheduling_links.go:130`, no status
  filter) and the 410 description; listed as inherited scope.

### Consumers this breaks

- smart-calendar-flow `src/pages/PublicBooking.pac83.test.tsx:184-211` (AC-21
  "no control re-sends") — amended by AC-26; the test changes with the web PR.
- clockwise-like PAC-101 AC-16 test (bookings never wait) — amended by AC-5 /
  AC-8.
- e2e keyless bookings (`e2e/tests/edge.spec.ts:86`, PAC-72/PAC-40 journeys) —
  unaffected (AC-18).
- MCP — no booking caller.

## Stage 4 — orchestrator decisions (2026-11-14)

Recorded by the stage-4 implementer on the orchestrator's instruction. Not
owner decisions: the owner raised no question on PAC-82.

1. **Contract-stage changes to accepted criteria, acknowledged.** The contract
   (`contracts/features/PAC-82.{md,yaml}` revision 1.1, contractHash
   `85187c94…`) amended this spec, and the orchestrator accepts each change as
   part of the criteria implemented at stage 4:
   - **AC-5 narrowed** to fine saves (contract section 4.3, challenge C-5): a
     save whose lock hosts times 15-minute UTC buckets exceeds 64
     (`BookingKeyCap`) is coarse and waits for every save of its hosts.
   - **AC-10 extended** (challenge C-4): the replay also wins when the link's
     duration list no longer contains the booked duration.
   - **The keyed 400 order** (contract section 1.1, challenge C-4): with a
     key, the link-independent body 400s come before the replay decision, and
     "invalid duration" comes after the replay and the 410. The order in
     section 9.4 above is superseded by the contract's for keyed requests;
     keyless requests are unchanged (AC-18).
   - **AC-29..AC-32 added**: the re-lookup of a keyed request's key before any
     409 or 410 (C-3), the pinned advisory-key derivation (C-5), the re-checks
     as separate READ COMMITTED statements (C-2), and the contract text itself.
2. **C-8 decided: what a refused "Try again" says.** A slot 409 or a 410 on
   "Try again" must NOT claim the earlier attempt was not saved (the server
   only proves no booking with that key existed when the refusal was decided).
   The web shows: "This time can't be booked now. Your earlier attempt may
   still have been saved — check your email before booking another time."
   This text is the factory default. AC-27 is amended accordingly (above). A
   429 or a 400 on "Try again" keeps "may have been saved" and "Try again"
   (this supersedes the manifest's reading of a 400 as a slot refusal).
