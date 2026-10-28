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
  offered/unoffered pair. Rule 3 now applies a host's bookings on all of their
  links to the POST, not only to the listing; the manifest's e2e entry records
  the static check that no two journeys book the same host at the same instant
  (revision 2), which the build stage re-confirms with the e2e gate.
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

## Challenge — 2026-10-28

Reviewed the committed state of `factory/pac-72-contract` at `d212afa`
(fragment `contracts/openapi/paths/booking.yaml`, bundle, `backend/api/gen`,
`contracts/features/PAC-72.yaml`/`.md`, `docs/factory/api-audit.md`) against the
spec on the same branch (`docs/specs/PAC-72.md`, revision 1), the handlers,
engine and storage at the same commit, `smart-calendar-flow` `origin/main`
`aa5c52e`, `mcp/` and `e2e/`. `.claude/factory/owner-decisions.md` has no
PAC-72 answer.

**Verdict**: accept-with-changes (1 blocking)

### Checks run (this review, worktree of `d212afa`)

```
python scripts/openapi_assemble.py --check          OK: bundle is in sync (99 paths)       rc=0
python scripts/openapi_migration_report.py --check  OK: MIGRATION.md covers all 120 operations (4 generated)  rc=0
bash scripts/openapi_gen_go.sh --check              OK: backend/api/gen matches the contract  rc=0
cd backend && go build ./...                        rc=0
git show HEAD:contracts/openapi/openapi.yaml | sha256sum
                                                    7c528c7e...969629 = manifest contractHash
CONTRACT=<this bundle> smart-calendar-flow/scripts/openapi-gen.sh --check
                                                    rc=1: drift in src/api/generated (expected:
                                                    the paired web regeneration is a build-stage PR;
                                                    the diff is the new schema, the 409 type, the
                                                    removed 422 and description text)
go test ./api -run 'TestPAC21Every5xx|TestPAC21ContractCheck'
                                                    NOT RUN: needs the test Postgres
                                                    ("dial tcp 127.0.0.1:15432 ... refused");
                                                    api-gate.sh not run (the host checkout is on
                                                    another stage's branch and was left there)
```

### Blocking

1. **Unrelated description of `SchedulingLink` corrupted.** The diff changes
   `booking.yaml:1853` (and with it `openapi.yaml` and the comment in
   `backend/api/gen/paceday.gen.go`) from
   "`window_start` vs `window_start_time`" to
   "`window_start_time` vs `window_start_time`". The sentence lists the DTO name
   against the storage json tag. The DTO field is `window_start`
   (`backend/api/handlers_scheduling_links.go:39`, and the schema's own
   `required`/`properties` at `booking.yaml:1855`, `:1881`). The storage tag is
   `window_start_time` (`backend/storage/scheduling_links.go:21`). The new text
   now names a DTO field that does not exist. It is also outside PAC-72's
   operations (`contract-author` must not change what the spec does not cover).
   Fix: restore "`window_start` vs `window_start_time`" at `booking.yaml:1853`,
   regenerate (`make openapi`), and update `contractHash` in
   `contracts/features/PAC-72.yaml`.

### Requests wrongly permitted

None that block. Requests I constructed against the rule and the schema:

- `POST /api/book/{slug}` with
  `{"name":"A","email":"a@x","start":"2026-11-02T10:00:00Z","end":"2026-11-02T10:30:00Z","duration":0}`.
  The new `end` text says the length must be exactly `duration` minutes "if
  either was also sent (else 400 'duration does not match end time')". `0` is
  sent. But the handler treats `0` (and `null`) as not sent
  (`handlers_booking.go:259-277`, `requestedDuration != 0` at `:273`), so today this
  answers 201. This existed before (the old text said the same). Non-blocking 1
  proposes a description fix.
- `{"...","start":"2026-11-02T10:00:00Z","duration":0}` with no `end`: the
  handler books `durations[0]` (`:279-285`). The `duration_minutes` text says
  the fallback applies "when neither is sent". This existed before too
  (non-blocking 1).
- `start` `"2026-11-02T10:00:00.500Z"` / `"…T10:00:30Z"`: the schema permits it
  (`format: date-time`), and the contract answers 409 on purpose
  (§2.6, AC-21a). Consistent.
- `start` `"2026-11-02T11:00:00+01:00"`, `end` `"…T11:30:00+01:00"`: permitted,
  and the contract says it is compared as an instant and stored as the UTC slot
  (AC-21c). Consistent.
- `end` = start + 30 min 30 s, with `duration:30` → 400 "duration does not match
  end time"; without a duration → 400 "invalid duration". This matches AC-21b
  and the manifest. Today's truncation (`:272`) is explicitly gone.

### Responses the client cannot handle

None found.
- `createPublicBooking` 409 `{"error":"slot is no longer available"}` for every
  cause, including the former 422. `smart-calendar-flow/src/pages/PublicBooking.tsx:186-189`
  shows the server text, clears the slot and refetches. The visitor sees "slot
  is no longer available" for a notice refusal or a never-offered start. That
  is accepted wording (§2.2, D-5).
- `getPublicBookingSlots` 200 `{"slots":[],"available_dates":[]}` for a date
  past the horizon or a non-link day: the page renders "No time available on
  this day" (`PublicBooking.tsx:451-457`). The new 500 for unreadable focus
  blocks or bookings goes through `slotsErrorMessage` with a Retry button
  (`:152-158`, `:432-443`).
- The web's real request (`src/api/schedulingLinks.ts:718-726`) sends `start`
  taken from the listing, `end = start + duration` via `toISOString()` (with a
  `.000Z` fraction), and `duration`. That is a zero fraction and an exact
  length, so it passes the exact-interval rule.

### Consumers affected

- `smart-calendar-flow/src/api/generated/*`: drift against this bundle confirmed
  (above). No hand-written web code names `createPublicBooking`'s generated
  response types (`git grep` on `aa5c52e`, outside `src/api/generated`), so
  removing the 422 type breaks no import.
- `smart-calendar-flow/src/api/schedulingLinks.ts:743-750` (demo-mode
  `bookSlot`) still models 422 for the notice and accepts any start. This is
  demo only and the spec rules out web changes (§5). Noted so nobody reads it
  as the contract.
- `backend/api/gen`: `CreatePublicBooking422JSONResponse` is removed. Nothing
  outside `gen` references it (`go build ./...` rc=0).
- `mcp/`: no reference to `/api/book` or bookings (grep). Not affected.
- `e2e/`: `public-booking.spec.ts:177` asserts only the 409 status, not the
  body, so it is unaffected. `e2e/lib/dates.ts:34-37`'s comment goes stale
  (spec §6). The journeys post fixed starts (`atUtc(bookingDay(n), …)`) on
  links of the same seeded owner. Under rule 3 a start now collides with that
  owner's bookings on *other* links too. This was already true of the listing
  since PAC-64, but it is new for the POST. The build stage should confirm
  that no two specs book the same owner at the same instant (non-blocking 4).
- Backend tests that pin 422 or off-rule starts: the spec's §6 list. The
  manifest copies it correctly.

### Contract/implementation divergences

The contract describes the specified change, which the server does not do yet
(stage 2 before stage 4). These lines will be false until the backend PR lands:
`handlers_booking.go:295-298` (422 "booking is too soon"), `:299-305` (only
this link's overlap), `engine/booking.go:168` (busy-time failures swallowed,
so no 500), and `handlers_booking.go:174-190` (process zone, no days filter).
See non-blocking 2 on landing order. Apart from the blocking item, I found no
divergence in the parts the contract says are unchanged (400 messages and their
order, 404/410/429, the 201 shape, the listing shape).

### Uncertainties not actually resolved

None claimed. No `x-uncertain` on `getPublicLinkInfo`, `createPublicBooking` or
`getPublicBookingSlots` before or after (checked in the bundle). API-094 is
"fix contracted, server pending" rather than Fixed, which is right while the
server is unchanged. API-051 is still stated as open on the operation
(`booking.yaml:111-113`).

### Non-blocking

1. Document `0`/`null` as "not sent" for `duration` and `duration_minutes` in
   the `end`, `duration` and `duration_minutes` descriptions (for example "if
   either was sent with a non-zero value"). The handler treats them that way
   (`handlers_booking.go:259-285`), and this contract has just reworded those
   sentences. If the spec intends `0` to be a 400, that is a behaviour change
   and needs a spec amendment, not a description.
2. **Landing order.** `mergeAfter` says "no other ordering constraint". If this
   contract lands on `main` before the backend PR, `main` documents 409 for the
   notice, a 500 for unreadable busy time and a link-day filter that the server
   on `main` does not have. The regenerated web types also lose a 422 that the
   server still sends. That is the gap factory §7 rule 2 warns about. Prefer
   stating in `mergeAfter` that the contract lands with, or immediately before,
   the PAC-72 backend PR, and that the audit row's "server pending" wording
   covers only that window.
3. AC-19 asks that 422 be documented "nowhere" on the booking operation. The
   409 description still says "(formerly 422 "booking is too soon")"
   (`booking.yaml:175`). It is history, not a documented status, but a literal
   AC-19 contract test that greps the operation for `422` would fail on it.
   Drop the parenthesis or make sure the AC-19 test checks response keys and
   status lists rather than text.
4. e2e: list in the manifest's e2e consumer entry that rule 3 now applies to
   the POST across the seeded owner's links. AC-20 will add an offered and an
   unoffered booking, and the existing journeys must not share an instant.
5. The API-094 row in `api-audit.md` still says "No PAC issue yet; to be opened
   at intake". It is PAC-72 now.
6. The rule names link fields by their storage/create-request spelling
   (`days_of_week`, `window_start_time`, `window_end_time`), while the owner's
   `SchedulingLink` DTO calls them `days`, `window_start`, `window_end`. Name
   both once, so a reader of the read model can find them.
7. AC-19 in the spec says "API-094 is marked fixed in the audit register". The
   manifest moves that to the backend PR, with a reason. That is fine, but the
   backend PR must do it. The narrowing is recorded in the manifest's AC-19
   line, not silently.
8. OWNER: D-1, a fixed 60-day horizon for booking and both listing modes,
   with an explicit date past it answering 200 with no slots (the issue says
   "the link's booking horizon"; no such setting exists).
9. OWNER: D-2, a start inside the minimum notice answers 409 instead of 422.
10. OWNER: D-3, an unoffered duration stays 400, though the issue's draft lists
    it among the 409 cases.
11. OWNER: D-4, the link's days of the week now hide days the listing shows
    today.
12. OWNER: D-6 / OQ-2, a host whose external calendar cannot be read stays
    "free" for both listing and booking.
13. OWNER: D-7 to D-11 (spec OQ-6): fail closed on storage failures (500, or
    the day omitted), exact interval, UTC as the one zone, a hostless link offers
    nothing, and pending bookings count as busy.

### Verified clean

- **Schema/handler order**: 429 → 404/410 (`:219-234`) → 400 body, start, end,
  duration (`:244-294`) → then the rule. The contract's "409 after 429, 404,
  410 and 400" and "a 400 is never turned into a 409" are consistent with the
  handler's existing order and with AC-18.
- **One refusal body**: `BookingSlotUnavailableError` is
  `{error: enum ['slot is no longer available']}` with
  `additionalProperties: false`. That matches `writeError`'s
  `map[string]string{"error": msg}` (`handlers_settings.go:398-402`) and
  today's 409 text (`handlers_booking.go:303`). The 5xx envelope is unchanged
  (`BookingError`), so `TestPAC21Every5xxInTheContractIsTheJSONEnvelope`'s
  input is unchanged (test not run, see above).
- **Busy-booking set**: "pending or confirmed on any link the host owns or
  co-hosts, this link included" names the union of today's two reads
  (`storage/scheduling_links.go:404-411` non-cancelled, this link;
  `:442-467` owner or *accepted* co-host, any active or inactive link). This
  link is always covered because every accepted host of it matches the second
  read. A hostless link is refused by rule 6.
- **Grid/window**: `CollectiveSlots` steps 15 minutes from the window start
  while start + duration ≤ window end (`engine/booking.go:101-110`, loop at `:104`). The
  contract's "counted from `window_start_time` … ends no later than
  `window_end_time`" is the same rule. Windows are validated `start < end`
  within a day (`handlers_scheduling_links.go:238-248`), so a slot never
  crosses a UTC date and the per-date horizon is well defined.
- **Buffers**: the contract says buffers widen calendar events only, and events
  are read only for the window. This matches `hostBusy`
  (`engine/booking.go:122-148`) and spec §5.
- **Listing**: both keys are still always present and required. No status was
  added or removed (the bundle lists 200/400/404/410/429/500). An explicit date
  outside the horizon gives 200 `[]`, not a new 400. The 500 with a date and
  the omitted day without one reuse the existing answers
  (`handlers_booking.go:181-183`, `:200-204`).
- **Responses list**: the bundle has `createPublicBooking` →
  201/400/404/409/410/429/500 (422 gone), and the 429 description's status
  list no longer names 422. The `start` field says "else 409".
- **Manifest**: `acceptanceTests` cover every spec AC (AC-1 to AC-23, including
  2b, 4b, 17a-d, 21a-c). The only rewording is the AC-19 register status
  (non-blocking 7). `contractHash` matches the committed bundle.
  `resolvesUncertain: []` is correct.
- **Rejected alternatives** (§2.1, §2.2, §2.4, §2.6) were rejected for stated,
  checkable reasons. One example: a seconds-forbidding `pattern` would split
  AC-9/AC-21a across 400 and 409.
- **Security conventions**: no `security` key changed on the public operations.
  The refusal discloses no cause (D-5). No field reveals host calendar facts.

## Revision — 2026-10-28

Answers the challenge above (accept-with-changes, 1 blocking). Contract
revision 2. `.claude/factory/owner-decisions.md` still holds no PAC-72 answer,
so every product choice stays "assumed by the factory, pending owner
confirmation" in the spec's Decisions.

### Blocking

1. **`SchedulingLink` description corrupted.** Fixed. `booking.yaml` reads
   "`window_start` vs `window_start_time`" again (DTO name against storage
   tag, as on `main`). Bundle and `backend/api/gen/paceday.gen.go` regenerated;
   `git diff origin/main` on both no longer touches that sentence.
   `contractHash` updated to the new bundle (`3badfa9d…94874011`) and
   `contractRevision` to 2.

### Non-blocking

1. **`0`/`null` lengths.** Accepted as description-only. `end`, `duration` and
   `duration_minutes` now say the requested length is `duration` when present
   and not null, else `duration_minutes`, and that a requested length of 0 or
   null counts as not sent: with `end` no comparison is made (so `end` =
   start + 30 with `"duration":0` is not a "duration does not match end time"
   400), and without `end` the fallback (durations[0], else 30) applies. The
   `duration` text spells out the consequence of the precedence:
   `"duration":0` with `"duration_minutes":60` books the fallback, not 60.
   This is today's handler (`handlers_booking.go:259-285`); no behaviour
   change, so no spec amendment.
2. **Landing order.** Accepted. `mergeAfter` now says the contract lands on
   `main` together with, or immediately before, the PAC-72 backend PR and its
   paired web type regeneration, never ahead of it on its own, and that the
   register's "server pending" wording covers only the window between the two
   merges.
3. **"(formerly 422 …)" in the 409 text.** Dropped from `booking.yaml`; the
   history lives here (§2.1) and in the spec. `createPublicBooking`'s text in
   the bundle no longer contains "422" anywhere. The manifest's AC-19 line also
   says the AC-19 test checks response keys and status lists, not text.
4. **e2e collisions under rule 3.** Checked statically and recorded in the
   manifest's e2e consumer entry: each seeded booking link has only its owner
   as host; the primary user is booked at 09:00-11:00 (uiBooking) and 16:00
   (scheduling-links' created link), the second user at 13:00 and 14:00 on two
   different days (the duplicate POST at 13:00 expects 409 by design), the
   seam user at 16:00, and time-format uses a fresh user. No two journeys book
   the same host at the same instant. This is a reading of the specs, not an
   e2e run; the build stage re-confirms it with the e2e gate after AC-20 is
   added.
5. **API-094 row "No PAC issue yet".** Replaced with "Opened as PAC-72."
6. **Field names.** Rule (2) now names `days_of_week`, `window_start_time` and
   `window_end_time` once as the create/update request and storage names and
   says the owner's `SchedulingLink` read model calls them `days`,
   `window_start` and `window_end`.
7. **AC-19 "API-094 marked fixed".** Kept on the backend PR, now stated as an
   obligation in the manifest (backend `allowedPaths` and the AC-19 line: the
   backend PR MUST set "Fixed (PAC-72)").
8. **Demo-mode `bookSlot`** (`smart-calendar-flow/src/api/schedulingLinks.ts:743-750`,
   422 for the notice, any start accepted). Acknowledged, not changed: demo
   only, and spec §5 rules out web changes. It is not the contract; a follow-up
   to align the demo is a triage item, not part of PAC-72.
9. **OWNER items (challenge non-blocking 8-13).** No owner answer recorded for
   PAC-72, so each stays as the spec records it, assumed by the factory pending
   owner confirmation, and is listed for the owner in spec OQ-1, OQ-2 and OQ-6:
   D-1 (fixed 60-day horizon for both listing modes and booking; a date past
   it answers 200 with no slots; no per-link horizon exists), D-2 (inside the
   minimum notice answers 409, not 422), D-3 (an unoffered duration stays 400),
   D-4 (link days of the week hide days the listing shows today), D-6 / OQ-2
   (an unreadable external calendar is treated as free in both paths), D-7 to
   D-11 / OQ-6 (storage failures fail closed; exact interval; UTC; hostless
   link offers nothing; pending bookings are busy). None of them deletes data
   or widens who sees data.

### Checks run (revision 2, worktree of `factory/pac-72-contract`)

```
python scripts/openapi_assemble.py                   wrote bundle (99 paths, 120 operations)  rc=0
python scripts/openapi_migration_report.py           120 operations (4 generated)             rc=0
bash scripts/openapi_gen_go.sh                       wrote backend/api/gen/paceday.gen.go     rc=0
python scripts/openapi_assemble.py --check           OK: bundle is in sync (99 paths)         rc=0
python scripts/openapi_migration_report.py --check   OK: MIGRATION.md covers all 120 operations (4 generated)  rc=0
bash scripts/openapi_gen_go.sh --check               OK: backend/api/gen matches the contract rc=0
cd backend && go build ./...                         rc=0
sha256sum contracts/openapi/openapi.yaml             3badfa9d0853a2a1245570b333648940277dee42f816e2afba5ac10294874011 (= manifest contractHash)
```

Not run: `api-gate.sh`, `web-gate.sh` and `e2e-gate.sh` (build stage; the host
api and web checkouts are on PAC-22 branches of another stage and were left
there), the web half of `make openapi-check` (expected drift until the paired
web regeneration), and `go test` (needs the test Postgres).
