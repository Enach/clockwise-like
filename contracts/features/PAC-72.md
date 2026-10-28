# Contract: PAC-72, public booking accepts only a start the slot listing would offer

- **Linear issue**: https://linear.app/paceday/issue/PAC-72/public-booking-never-checks-host-availability-the-links-hours-or-a
- **Spec**: `docs/specs/PAC-72.md`, challenged accept-with-changes at `d9be46b`,
  revision 1 at `5236cfb`. This branch fast-forwards `factory/pac-72-spec`, so it
  carries the spec. `.claude/factory/owner-decisions.md` holds no PAC-72 answer
  as of 2026-10-28, so every product choice below is the spec's own, "assumed by
  the factory, pending owner confirmation" (spec D-1 to D-11).
- **Stage**: contract-author, then contract-challenger.
- **Fragment edited**: `contracts/openapi/paths/booking.yaml`.
- **Generated**: `contracts/openapi/openapi.yaml` (`scripts/openapi_assemble.py`),
  `backend/api/gen/paceday.gen.go` (`scripts/openapi_gen_go.sh`, oapi-codegen
  v2.4.1). `MIGRATION.md` unchanged (no operation added). Web types are NOT
  regenerated here; that is the build stage's paired web PR (REGEN=1 web-gate).
- **Register**: `docs/factory/api-audit.md` API-094 row marked "fix contracted
  (PAC-72), server pending", with a PAC-72 note.

---

## 1. What changes at the HTTP boundary

| Operation | Change |
|---|---|
| `createPublicBooking` (`POST /api/book/{slug}`) | Description: the "Not checked (API-094, open)" text is replaced by the six-part offerable-start rule, the UTC zone, instant comparison, "stored interval equals the slot", one refusal body, check ordering, external-calendar tolerance, storage failure = 500, concurrency not guaranteed, API-051 still open. **409** lists every refusal cause and its body is now `BookingSlotUnavailableError`. **422 removed.** 400 messages for the length rule defined exactly. 500 names "booking conflict check failed" as the answer for unreadable busy time. The 429's list of otherwise-answered statuses loses 422. |
| `getPublicBookingSlots` (`GET /api/book/{slug}/slots`) | Description: same rule; 60-day horizon for both modes; UTC; link-day filter; explicit date outside the horizon is 200 with `slots: []`; hostless link offers nothing; unreadable busy time is 500 (with `date`) or an omitted day (without). `date` parameter and 500 descriptions follow. **No status added or removed, no shape change.** |
| Components | New schema `BookingSlotUnavailableError` (`{error: enum ['slot is no longer available']}`). Description-only changes to `PublicSlotsResponse.available_dates`, `CreatePublicBookingRequest.start` and `.end`. |

Not changed: `getPublicLinkInfo`, every authenticated operation, the 201 body,
the 404/410/429 responses.

## 2. Decisions this contract makes or encodes

### 2.1 One status for "not offerable": 409, and 422 goes

**Chosen**: every start the listing would not offer answers 409, including a
start inside the minimum notice (spec D-2, the Linear issue's own wording). The
422 response is deleted from the operation rather than kept "for
compatibility", because keeping it would document a status the server can no
longer send, and the contract describes what is.

**Rejected: keep 422 for the minimum notice and add 409 for the rest.** It is
the smallest change for the web app, which has a 422 branch with an
"advance notice" message. But it makes the notice the one rule a client can
tell apart from the others, it splits one decision ("would the listing offer
this?") across two statuses, and the issue asks for 409 for every non-offerable
start. The web 409 branch already does the right thing for a notice refusal
(clear the slot, reload the times), so nothing is lost for visitors.

### 2.2 One refusal body, pinned by enum (OQ-4)

**Chosen**: today's text, `{"error":"slot is no longer available"}`, for every
cause, pinned by a new schema `BookingSlotUnavailableError` the same way
NEW-booking-rate-limit pinned `rate limit exceeded`. This answers the spec's
OQ-4 (owner: contract-author). The web page shows the server text verbatim on
409; this sentence is already what visitors see on today's lost race, it does
not name a cause (spec D-5: a refusal must not reveal that a host has a meeting
then), and AC-13 becomes a schema property rather than only a test.

**Rejected: a new uniform text** such as "this time is not available". It reads
slightly better for a never-offered start (a Saturday) than "no longer", but it
changes the message every visitor sees on the common case (a slot taken while
the page was open), and it buys nothing a client can act on.

**Rejected: per-cause messages or a `reason` field.** Useful for debugging, but
it discloses host calendar facts to anonymous callers (D-5) and invites the web
app to branch on it, which the spec does not ask for.

### 2.3 Unoffered duration stays 400 (D-3)

The issue's draft lists "a duration that isn't offered" among the 409 cases.
The contract keeps 400 "invalid duration": it can never succeed at any time,
the listing answers the same 400 for the same duration, and the web app never
sends one. The length rule is made exact: with `end`, end - start must be
exactly a whole number of minutes the link offers; today's truncation of
30 min 30 s to 30 is gone. A mismatch with an explicitly sent `duration` keeps
its existing message "duration does not match end time"; otherwise "invalid
duration". No new message is introduced.

### 2.4 Horizon: 60 days, the same everywhere, empty rather than an error (D-1)

No per-link horizon exists. 60 days is the only bound the product has (the
date-less listing). The contract applies it to both listing modes and booking.
An explicit `date` outside it answers 200 with `slots: []`.

**Rejected: 400 for a date beyond the horizon.** It would add an error path to
an operation whose errors existing clients do not expect, and the web page,
whose date picker leaves every future date selectable, would then show its
generic error instead of the "No time available on this day" message. An empty
day is indistinguishable from a fully booked one, which is the honest answer.

**Rejected: a `horizon_days` field on `PublicLinkInfo`.** It would let the web
grey out dates, but it is a new interface for a value that is not configurable,
and the spec puts the picker change out of scope (OQ-5). It can be added
additively later.

### 2.5 Failures: 500 for storage, tolerance for external calendars (D-6, D-7)

The 500 message for unreadable busy time reuses "booking conflict check failed",
which the operation already documents for the old own-link overlap read. That
read is subsumed by rule 3 (bookings on any of the hosts' links, this one
included), so the message keeps its meaning and no new string appears. The
listing keeps the answers each mode already gives for an engine error (500 with
a date; omitted day without), so no new error shape exists anywhere.

### 2.6 Prefer the impossible to the rejected

Little of the rule can be expressed in the schema: the grid, days, window,
horizon and host freedom depend on server state. What can be is: the 409 body
(enum), and nothing else new. `start` keeps `format: date-time`; a
`pattern` forbidding seconds was considered and **rejected**, because an
off-grid start is a 409 by the spec (AC-21a), the same class as 09:05, and a
schema-level 400 would split one rule into two statuses.

## 3. Consumers

- `smart-calendar-flow/src/pages/PublicBooking.tsx`: the 409 branch handles
  every refusal; the 422 branch becomes unreachable and is left (spec §5: no
  web change). Visible changes are the spec's §3 list.
- `smart-calendar-flow/src/api/generated`: regenerated in the build stage. No
  hand-written web code names the generated booking operation types
  (`git grep` on `origin/main` `aa5c52e`).
- `backend/api/gen`: gains `BookingSlotUnavailableError` and the constant
  `SlotIsNoLongerAvailable`; `go build ./...` passes.
- Backend tests that pin 422 or book off-rule starts: amended by the backend PR
  (spec §6 list), never deleted or skipped.
- e2e: existing booking flows use link days within 60 days; AC-20 adds the
  offered/unoffered pair.
- `mcp`: does not call `/api/book`.

## 4. Findings and uncertainties

- **API-094**: resolved by this contract plus the PAC-72 backend PR. The
  description no longer states it open. Register row: "fix contracted
  (PAC-72), server pending"; the backend PR sets "Fixed (PAC-72)". Marking it
  Fixed here would make the register claim a server behaviour that does not
  exist yet.
- **API-051**: still open (PAC-40), still stated on the operation.
- `x-uncertain`: none on the three public booking operations, before or after.

## 5. Gate results (contract stage)

Run in the worktree of `factory/pac-72-contract` on 2026-10-28:

```
python scripts/openapi_assemble.py --check      OK: bundle is in sync (99 paths)
python scripts/openapi_migration_report.py --check  OK: MIGRATION.md covers all 120 operations (4 generated)
bash scripts/openapi_gen_go.sh --check          OK: backend/api/gen matches the contract
cd backend && go build ./...                    exit 0
```

Not run at this stage: the web half of `make openapi-check` (it will report drift
until the paired web PR regenerates types), `api-gate.sh`, `web-gate.sh` and
`e2e-gate.sh`; those belong to the build stage.

## 6. Rollback

No migration. Revert the backend PR first or last: the web handles 409 and 422.
Reverting this contract restores the 422 documentation and the "API-094 open"
text; the paired web type regeneration reverts with it.
