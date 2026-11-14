# Contract: PAC-40, scheduling-link lifecycle, with an owner-only archive and 90-day erasure of bookers' details

- **Spec**: `docs/specs/PAC-40.md`, revision 6.2 on `factory/pac-40-spec`. It was
  accepted in challenge round 6 after revision 6.1. Revision 6.2 corrects only §0:
  the readings R-1 to R-5 are assumed by the factory and await the owner's
  confirmation.
- **Manifest**: `contracts/features/PAC-40.yaml`.
- **Fragments**: `contracts/openapi/paths/booking.yaml` (part A) and
  `contracts/openapi/paths/calendar.yaml` (part B, one audit action).
- **Base**: `origin/main` `68540f6`, which `factory/pac-40-spec` contains.
- **Revision**: 3. This replaces revision 2 on `factory/pac-40-contract`, which
  is superseded. Revision 2's C-1 answered 403 to a stranger on
  `getSchedulingLink`. Main has answered 404 since PAC-25, and the spec's AC-19
  pins that 404. Revision 2 also had no archive and no erasure.
  clockwise-like#234 and smart-calendar-flow#126 were built on revision 2 and are
  superseded too.

## 0. The owner's decision and what this contract assumes

The owner's decision of 2026-10-27 reads: "the owner can see a deleted link's
past bookings in an archive; bookers' names and emails are erased 90 days after
the link's last booking date". This contract implements the factory's readings
of that decision (spec §0, R-1 to R-5). The owner has **not** confirmed those
readings. Spec revision 6.2 corrects §0 to say so.

If the owner answers one of the open questions differently, this is what changes:

- **OQ-8** (erase notes too?): `ArchivedSchedulingLinkBooking.notes` becomes
  erasable, with the same null-and-`booker_erased_at` rule. That is a contract
  revision.
- **OQ-9** (retention for live links): a new ticket. Nothing in this contract
  changes.
- **OQ-10** (last booking date = latest meeting end): only the erasure job's
  due rule and the description of `last_booking_end_at` change.
- **R-1** (only the owner sees the archive): if co-hosts were given the archive
  too, both archive operations would need a new access rule. That is a contract
  revision.

## 1. What changes at the HTTP boundary

| Area | Before (main `68540f6`) | After |
|---|---|---|
| Deleted vs paused | One flag: `active=false` meant both, and a deleted link stayed listed, fetchable, editable and invitable (API-050) | Two states. **Paused** (`active=false`) behaves as today. **Deleted** is gone from every live path: for every caller, owner included, every by-id or by-slug operation answers the 404 for an id or slug that never existed, and both lists leave it out |
| Archive | none | `GET /api/scheduling-links/archive` and `GET /api/scheduling-links/archive/{id}`, owner only and read-only. They carry the erasure instant and the erased state |
| `getSchedulingLink` | PAC-25: 404 to anyone who is not the owner or an accepted co-host | Unchanged, plus a deleted link answers 404 to the owner and co-hosts too (AC-2, AC-19). **No 403** |
| PATCH aliases | The alias wins (API-049) | The canonical spelling wins whenever it is carried (present and not null; `[]` counts as carried), as on create. The four array fields are `nullable` on both request schemas, so the null case of AC-14a can be sent |
| `uses_count` | Counts non-cancelled bookings (API-051) | Counts every booking row, cancelled ones included. The 410 is judged on this figure. Erasure never changes it |
| `deleteSchedulingLink` | 204. A repeated delete also answered 204 | 204, empty body. A repeated delete answers 404. The description states the archive and erasure behaviour (§2.4) |
| Audit | 10 actions | 11 actions (part B): `scheduling_link_bookers_erased` |

No status code or response shape of an existing operation changes. On the
existing operations, the only changes are descriptions, `nullable` on the
request arrays and on the create window fields, and `minimum: 0` on
`uses_count`. Two operations are added, along with five schemas:
`BookerErasurePolicy`, `ArchivedSchedulingLink`,
`ArchivedSchedulingLinkListResponse`, `ArchivedSchedulingLinkBooking` and
`ArchivedSchedulingLinkDetail`.

## 2. Decisions

### 2.1 Two archive operations, separate from the live ones (spec §3.2, D-9)

**Chosen:** `GET /api/scheduling-links/archive` lists the caller's deleted links
with no bookings. `GET /api/scheduling-links/archive/{id}` returns one archived
link with **all** of its bookings. Both are filtered on "owner = caller AND
deleted" inside a single lookup. As a result, the 404 on the detail operation
is the same for every case the spec lists (AC-23, AC-6a): an id that never
existed, a live or paused link (even for its owner), and a deleted link owned
by someone else. Neither operation ever answers 403.

**Rejected: serve deleted links to the owner from the live operations.** The
alternatives were a `?include_deleted=true` filter on `listSchedulingLinks`, or
letting `listSchedulingLinkBookings` keep answering the owner for a deleted
link. Both reopen what the spec closes. D-9 says every path except the archive
answers not found, and AC-6 makes the live bookings read a 404 for the owner.
Both would also put a deleted link back on operations whose descriptions say
"edit, resume, invite". The archive's read-only promise ("nothing in the archive
changes a link") is easiest to keep when the archive is its own path prefix with
only `GET`.

**Rejected: one operation that nests every booking inside the list.** The
payload would grow with every booking of every deleted link. Each list read
would also carry every booker's name and email when the page only needs titles
and dates.

**Rejected: `/api/scheduling-links/{id}/archive`.** Placing the archive under
the live `{id}` would suggest it is a sub-resource of a link that, for the live
operations, does not exist. The static `archive` segment also matches before
`/{id}` in chi. Today `GET /api/scheduling-links/archive` reaches `getLink` and
answers 400 "invalid id", and no consumer calls that.

### 2.2 The erased state is explicit: null plus a timestamp, never `""`

`ArchivedSchedulingLinkBooking` has `booker_name` and `booker_email` as
`nullable` strings and a `booker_erased_at` that is set exactly when both are
null. Spec AC-32 requires a state "distinguishable from any value a booker can
submit". The booking handler trims both fields and requires them
(`handlers_booking.go:359-363`), so a booker cannot produce null, or produce an
empty string either.

**Rejected: return `""` for an erased value.** It looks like a bug in the web
("why is the name blank?"), and the spec forbids a blank. The **database** still
stores `''` (§4). That keeps `NOT NULL` valid for the reverted code (spec §7).
The empty string is never put on the wire.

**Rejected: a placeholder string such as `"[erased]"`.** A booker could type
it. It would also be a translatable UI string baked into the API.

**Rejected: reuse `SchedulingLinkBookingRecord` with nullable fields.** That
would weaken the live operation's type for every consumer, although a live or
paused link never holds an erased booking. The archive gets its own schema.
The live schema stays `required` strings.

### 2.3 The archive entry's erasure fields

`ArchivedSchedulingLink` has these fields:

- `last_booking_end_at`: the latest `end_time` of all bookings, every status
  included (D-12).
- `erasure_due_at`: that time plus 90 × 24 h.
- `erased_at`: the latest `booker_erased_at`.
- `erasure_state`, one of:
  - `no_bookings`: nothing to erase. The spec says "a deleted link with no
    bookings says there is nothing to erase".
  - `awaiting_erasure`: at least one booking still holds a name and email.
  - `erased`: every booking has been erased.

This covers all four of §3.2's display states, including the AC-34 race, in
which a booking lands after an erasure: the state is `awaiting_erasure` with a
new due date, and `erased_at` keeps the earlier instant.

The value is `awaiting_erasure` rather than `pending` for a mechanical reason.
oapi-codegen v2.4.1 resolves enum-constant name clashes in an order-dependent
way. Adding a fourth `pending` enum made it rename main's existing `gen.Ready`
constant, and `backend/api/readiness.go:80` then failed to build. That was
observed in the LF clone during this revision. A distinct value avoids it. For
the same reason `retention_days` is bounded `minimum: 90, maximum: 90` instead
of `enum: [90]`, because an integer enum generated an `N90` constant with the
same side effect.

### 2.4 The delete answer and the copy hook (spec §3.2 last bullet, AC-25; PR split)

**Delete stays `204` with an empty body.** The web calls it and expects no body
(`schedulingLinks.ts:395`). The confirmation copy is shown **before** the
request, so a body after the delete cannot supply it.

**Rejected: `200` with the `ArchivedSchedulingLink`.** That would change the
status of an operation with an existing consumer. It would also duplicate the
archive read, which is where the web shows the due date anyway.

**The copy hook is `BookerErasurePolicy`** = `{enabled, retention_days: 90}`,
carried by `ArchivedSchedulingLinkListResponse.booker_erasure`. The web's delete
confirmation works as follows:

- When `enabled` is true, the confirmation says that people with the link will
  no longer be able to book. It also says that the link and its bookings move to
  the archive, and that bookers' names and emails are erased `retention_days`
  days after the link's last booking (AC-25).
- When `enabled` is false, or the policy has not loaded or failed to load, the
  confirmation says everything except the erasure sentence. It **never promises
  erasure** in that case.

`enabled` is a property of the server build, not an operator setting: no
environment variable changes it. It is false only in the part-A build (§6).
This is what lets the web change merge once, before or with part A, without
ever promising erasure before the job ships (spec challenge round 6,
non-blocking 7). While `enabled` is false, every `erasure_due_at` is null, so
the archive shows no date either.

**Rejected: put the policy on `SchedulingLinkListResponse`.** The Links page
already loads that response, which made it the obvious place. But backend
tests decode it as a map of arrays: `decodeOwnedShared`
(`handlers_scheduling_links_test.go:60-67`, `map[string][]schedulingLinkDTO`,
also used by `pac72_rev2_test.go:521`) and `:643`
(`map[string][]json.RawMessage`). A third-party client may do the same. A
non-array member breaks every one of those decoders. A new operation has no
consumers yet, so the hook costs nothing there. The web reads it when the
delete dialog opens, or prefetches it with the page.

**Rejected: hard-code "90 days" in the web.** Then the copy could not tell
whether the job exists. That is the failure the PR split must prevent.

### 2.5 `getSchedulingLink`: PAC-25's 404 kept (spec AC-19, OQ-5)

Revision 2 chose 403 for a stranger on a live link (its C-1). Main has answered
the unknown-id 404 since PAC-25 (`handlers_scheduling_links.go:488-502`), the
spec pins that 404 (AC-19), and this revision adds no 403. A deleted link is a
404 at the lookup step, before the host-status read. Main's check order is
otherwise unchanged. The owner-only siblings (PATCH, DELETE, `/bookings`,
`/hosts`) keep their 403 on a **live** link (spec §5 last bullet). On a
**deleted** link they answer 404 before the owner check, so that no caller
learns the id once existed (AC-6a).

### 2.6 No "deleted" field on live responses (unchanged from revision 2, C-2)

`active` keeps its name and type and means "paused". No live response ever
contains a deleted link, so no field there needs to describe one.

**Rejected: `status: active|paused|deleted`.** It breaks the web, which reads
and writes `active`. Its `deleted` value could never appear, and it would let
PATCH express "delete", which duplicates DELETE.

`deleted_at` appears only on the archive entry, which by definition describes a
deleted link.

### 2.7 `uses_count`: meaning, not mechanism (revision 2's C-3, restated)

`uses_count` is the number of booking rows stored for the link, whatever their
status, and the 410 is judged on that number. Whether the server stores it or
derives it is left to the plan. The plan is recommended to derive it with
`COUNT(*)` and no status filter. That needs no migration, and it makes AC-17a
(cancelled before the deploy) hold automatically. A row removed outside the
product (e2e teardown, a cascade) is no longer counted.

**Rejected: a separate `bookings_made` field.** The web judges "used up" from
`uses_count`.

**Rejected: "never decreases, even on a removed row".** It would force a stored
counter that the e2e seed would have to reset on every run.

### 2.8 Aliases (API-049; unchanged from revision 2)

The canonical spelling wins on both verbs for durations and days. An empty list
counts as carried and fails 422. The window aliases are documented as they are
(D-10, OQ-7).

**Rejected:** a `not: {required: [...]}` rule that forbids sending both
spellings. Requests that succeed today on create would then fail (D-5).

## 3. Ordering of checks for a deleted link

For every live operation that names a link by id, "deleted" is checked at the
same point as "no such link": after the UUID parse, and before the owner or
host check and before the request body is read. This means:

- A malformed PATCH body sent to a deleted link is a 404, not a 400.
- The owner's `leave` on a deleted link is a 404, not 400 "owner cannot leave
  link" (AC-5).

Accept and decline do not load the link today: they update the caller's host
row by link id (`scheduling_links.go:381-395`). The contract requires a 404
and an unchanged row when the link is deleted. How the handler finds out is
left to the plan; for example, the `UPDATE` can join the link and require
`deleted_at IS NULL`.

The public operations by slug already filter on `active = true`, and delete
also sets `active = false` (§4). **Binding (challenge K4-1):** the public slug
lookups filter on `deleted_at IS NULL` as well, so a deleted link is never
bookable even if a row ever held `deleted_at` set with `active = true`. The slug
check (`SlugExists`) still counts deleted rows (D-3, AC-8).

**Writes by id re-check "not deleted" in the write itself (binding, challenge
K4-1, K4-2).** Every handler reads the link first and writes later, with other
reads in between (`updateLink` reads the zone and the slug). A delete that
commits in that gap must not be undone or moved:

- `UpdateSchedulingLink` writes `active = $13` from the value it read. Its
  `UPDATE` carries `AND deleted_at IS NULL`; zero rows is the 404 of §1 and
  nothing is written (no co-host change either). Without this, a PATCH that
  read the link before a concurrent DELETE would write `active = true` back
  onto a deleted link, which every live path hides from its owner while the
  public page (before K4-1's filter) books it.
- The delete is `UPDATE … SET deleted_at = now(), active = false WHERE id = $1
  AND deleted_at IS NULL`; zero rows is the 404. Two concurrent deletes give
  one 204 and one 404, and `deleted_at` is never moved forward by a repeat
  (it is the instant the archive and the operator listing show).
- `scheduling_links_deleted_inactive CHECK (deleted_at IS NULL OR active = false)`
  is binding (§4), as the last line of defence for any writer not listed here.

## 4. Storage and migration 037 (binding where marked)

The next free number is **037**. Main's latest is `036_llm_org_ai_consent_period`,
and no remote branch carries a 037 or higher; this was checked against every
`origin/*` branch on 2026-11-13. If another ticket takes 037 first, the
implementer renumbers to the next free number and records it here and in the
manifest. That is not a contract revision.

`037_scheduling_link_lifecycle.up.sql`:

- **Binding:** `scheduling_links.deleted_at TIMESTAMPTZ NULL`, null for every
  existing row. Every link that is inactive before this migration therefore
  stays **paused** (D-1, AC-12). Delete sets **both** `deleted_at = now()` and
  `active = false`. Because of the `active = false`, the down migration and a
  code revert turn every deleted link into a paused one (spec §7). **Binding
  (challenge K4-1; was "recommended" in revision 3):**
  `CHECK (deleted_at IS NULL OR active = false)`. With it, no writer can leave
  a deleted link publicly bookable; the request-path writers are also required
  to carry `deleted_at IS NULL` (§3), so the check is never what a caller sees.
- **Binding:** `bookings.booker_erased_at TIMESTAMPTZ NULL`. An erased booking
  stores `booker_name = ''` and `booker_email = ''`, enforced by
  `CHECK (booker_erased_at IS NULL OR (booker_name = '' AND booker_email = ''))`.
  `NOT NULL` stays on both columns. On the wire, `''` with `booker_erased_at`
  set is returned as null (§2.2). It is never returned as `""` for an erased
  booking. (A non-erased row is returned as stored. The API has refused an
  empty name or email since before `b438e05`, so `""` there means a row written
  outside the product, such as an e2e seed.) Adding the constraint to an
  existing table is a full scan, and every row passes it because
  `booker_erased_at` is null. The plan may use `NOT VALID` then `VALIDATE`, as
  `026` did.
- **Recommended:** a partial index for the archive and the erasure scan:
  `ON scheduling_links (owner_user_id, deleted_at DESC) WHERE deleted_at IS NOT NULL`.

`037_scheduling_link_lifecycle.down.sql` (**binding** behaviour, spec §7):

- It drops the two columns and the constraints. It cannot fail on erased rows,
  because they are valid `NOT NULL` rows, and it cannot bring back an erased
  value, because the value is gone.
- To the reverted code, an erased booking is a row with an empty name and an
  empty email. `LinkBookingsDialog.tsx:81` would show it blank. Deleted links
  reappear as paused. No row is lost.

PAC-84: neither column is a secret column. Erasure overwrites plain text and
needs no key (spec §3.5, AC-36). PAC-71: `emptiness.go:39-41` still counts
deleted and erased links and bookings as account data. The plan adds a test
for that (spec §6).

The live storage struct and its JSON: `listSchedulingLinkBookings` and
`SchedulingLinkBookingRecord` have `additionalProperties: false`. If the Go
`storage.Booking` gains the erased timestamp, it must not gain a JSON key on the
live path. Use a separate archive DTO.

## 5. The erasure job (non-HTTP interface; part B)

**Where it lives.** A new engine type is recommended:
`backend/engine/booker_erasure.go`, with its `_test.go`. It must have an
injectable clock and logger, like the focus-time loop (`scheduler/cron.go:36-38`):

```go
type BookerErasure struct {
    DB   *sql.DB
    Now  func() time.Time                  // injected in tests (AC-26..AC-35)
    Logf func(format string, args ...any)  // injected in tests (AC-27, AC-31, AC-33)
}
type BookerErasureSummary struct{ LinksExamined, LinksErased, BookingsErased, Failures int }
func (e *BookerErasure) Run(ctx context.Context) BookerErasureSummary
```

The names are recommended. The behaviour below is binding.

**Due rule (binding).** A link is due at instant `now` when all three hold:

- `deleted_at IS NOT NULL`;
- it has at least one booking with `booker_erased_at IS NULL`;
- `max(end_time) over ALL its bookings, every status` + `interval '90 days'`
  ≤ `now`.

The interval is exactly 90 × 24 h; compute it as `now - 90*24h`, never as 90
calendar days across a DST change. Live and paused links are never examined,
and neither are links that were inactive before 037 (AC-29).

**One link = one transaction (binding, AC-28, AC-33):**

1. Lock the link row with `SELECT … FOR UPDATE`.
2. Re-evaluate the due rule inside the transaction.
3. Run `UPDATE bookings SET booker_name = '', booker_email = '', booker_erased_at = $now
   WHERE link_id = $1 AND booker_erased_at IS NULL`.
4. If at least one row changed, insert the audit entry in the **same**
   transaction.
5. Commit.

**The lock and the erasing statement (binding, challenge K4-3).**

- The lock in step 1 is `FOR UPDATE`, not `FOR NO KEY UPDATE` or `FOR SHARE`.
  A booking insert checks its foreign key with `FOR KEY SHARE` on the link
  row, and only `FOR UPDATE` conflicts with that. While a link's erasure
  transaction is open, a booking insert on that link waits for it. It never
  interleaves.
- The `UPDATE` in step 3 carries the due rule in the same statement, as
  `… AND (SELECT max(end_time) FROM bookings WHERE link_id = $1) <= $now - interval '90 days'`.
  It must not rely only on the re-check in step 2. Then even a booking that
  became visible between steps 2 and 3 cannot be erased before it is due.
- **Lock order:** the link row, then that link's bookings rows, then the
  audit insert, which takes `FOR KEY SHARE` on the owner's `users` row. Today
  no path holds a `users` row lock and then waits on a link lock: booking
  creation is, since PAC-101, one transaction that locks only the link
  (`FOR SHARE`, under the 10 s link-lock bound) and then inserts the booking,
  taking no `users` row lock (`contracts/features/PAC-101.md` §4.1; on a
  deleted link it finds no row, so it never interleaves with this
  `FOR UPDATE`); `FreeAddress` and `reassign` touch no link, and no product
  path deletes a user. So there is no lock cycle. A
  future path that does lock a user and then a link (an account deletion
  cascade) would get a Postgres deadlock error. The job would see that as
  failure class `40P01` for that link, roll the link back, and erase it on
  the next run. That is the acceptable outcome. Nothing may hold a link lock
  across a network call.

Two concurrent runs serialise on the row lock. The second finds zero rows and
writes no entry, so there is exactly one entry per erasure. A failure rolls the
whole link back, and the run then moves on to the next link. A cancelled `ctx`
(the PAC-81 drain) stops the run between links, never inside one.

**Audit entry (binding, AC-31; part B).** The entry goes in the owner's
activity log as action `scheduling_link_bookers_erased`, with details
`{"link_id","link_title","bookings_erased","last_booking_end_at","ran_at"}`
written by `encoding/json` (contract: `AuditEntry.action` and `details` in
`calendar.yaml`).

`storage.WriteAuditLog` logs insert failures and discards them
(`focus_blocks.go:79-87`), so it cannot give atomicity. The plan therefore
adds a transactional sibling that returns the error. Recommended:
`storage.WriteAuditLogTx(tx, userID, action, details) error`, called with a
literal action. `TestPAC67_AuditEnumMatchesTheWriters`
(`backend/api/pac67_surfaces_test.go:466`) is then amended in two places:

- its `call` and `literal` patterns also match the transactional writer;
- `len(enum) != 10` becomes 11.

**Log line (binding, AC-26, AC-31, AC-35, AC-37).** Every run writes exactly
one summary line, including a run that erases nothing:

```
booker-erasure: run at=<RFC3339 UTC> links_examined=<n> links_erased=<n> bookings_erased=<n> failures=<n>
```

A failed link adds one line:

```
booker-erasure: link <uuid> failed: <class>
```

`<class>` is an error class with no row data, for example the Postgres
SQLSTATE code or `context canceled`. Never log `err.Error()` of a database
error, and never wrap a database error with `%w` or `%v` into a logged or
reported error. The backend's driver is pgx through `database/sql`
(`storage/db.go`, `stdlib.OpenDB`), so the error is a `*pgconn.PgError`. Its
`Error()` prints `Message` but not `Detail`. Even so, `Message` repeats the
input for some classes (`22P02 invalid input syntax … "<value>"`), and
`Detail` ("Failing row contains (…)") holds the row, name and email included.
Take the class with `errors.As` and the `Code` only (challenge K4-4). The
same rule applies to Sentry. Send a sanitised error, or none, and never the
raw database error. AC-33's injected fault is a `*pgconn.PgError` whose
`Message` and `Detail` both contain the booker's name and email; `*pq.Error`
may be tested too. No line names a booker.

**Schedule (binding, D-13, AC-35).**

- The first run happens once as part of start-up, before the first periodic
  run.
- After that, `@every 24h` on a `robfig/cron` registered as the existing jobs
  are.
- Both runs are wrapped by `cronJob` (`main.go:45-58`). The PAC-81 drain
  therefore awaits a run in progress, and no run starts once shutdown has begun.

The start-up run must not delay readiness indefinitely. The plan may run it in
the tracked unit right after start rather than before the listener opens,
provided that a test with a periodic trigger that never fires still sees
exactly one run (AC-35).

**Operator listing (binding, AC-37).** The command is
`paceday-operator booker-erasure status`. It needs no `--operator`/`--reason`
and is read-only. It prints one line per deleted link that is due, overdue, or
due within 7 days:

- link id
- owner user id
- `deleted_at`
- `last_booking_end_at`
- `erasure_due_at`
- the number of bookings awaiting erasure
- `overdue` when due more than 24 h ago

Its output never contains a name, an email or a title. The last run's time is
the newest `booker-erasure: run` log line. The runbook says so.

**Command conventions (binding, challenge K4-5, matching PAC-84's
`encryption status`, `cmd/paceday-operator/encryption.go`):**

- `main` dispatches `booker-erasure` before the PAC-71 path, as it already
  does for `encryption`. The PAC-71 path opens with `storage.OpenAs`, which
  **applies migrations**, and a read-only listing must never do that. The
  command opens with `storage.OpenNoMigrate` and
  `application_name = "paceday-operator booker-erasure"`.
- It never applies a migration. When the schema is below 037, or is dirty,
  it prints that it cannot list and changes nothing. That happens after a
  rollback of A, or before the first start of this release.
- Exit codes follow PAC-84's numbering:
  - 0: the listing was printed;
  - 1: failure, such as an unreachable database, a schema below 037 or
    dirty, or a query error;
  - 2: usage;
  - 3: with `--check` only, when at least one link is overdue.
- Revision 3 used exit 1 for "overdue". That would make a monitoring check
  unable to tell an overdue erasure from an unreachable database.
- `--check` and `-check` are both accepted (Go `flag`). There is no
  `--operator` and no `--reason`. The run is not written to
  `operator_audit`, as for `encryption status`.

**Runbook (binding, AC-37).** Add a section "Booker erasure (PAC-40)" to
`docs/runbook.md` covering every item in spec §3.5 "Runbook":

- what erasure reaches and what it does not (calendar events, sent emails,
  backups kept 14 days);
- that it runs at start-up and daily, and the log line that shows a run;
- the status command;
- that backup retention bounds how long an erased value survives;
- after a restore, start the backend and check the next `run` line before the
  restored data is used;
- what to do when `failures>0`.

The section also states that erasure cannot be undone, including by a code
revert (spec §7).

## 6. PR split

| PR | Repo | Contains | Bundle |
|---|---|---|---|
| **A-api** | clockwise-like | Migration 037. The deleted state on every path (AC-1–AC-12). Both archive operations with `booker_erasure.enabled = false`, `erasure_due_at` always null and no booking ever erased (AC-21–AC-24; AC-21's due instant is asserted in B). Aliases (AC-13–AC-14a). `uses_count` (AC-15–AC-18). AC-19 kept. No erasure job, no audit action | `07f912d`: `contracts/openapi/openapi.yaml` sha256 `d5cf6be9…` (part A, `booking.yaml` only) |
| **A-web** | smart-calendar-flow | Generated types from bundle A. Archive view: list, detail dialog, explicit "erased" rendering, no edit/resume/restore/delete control. Delete confirmation built from `booker_erasure` (§2.4). Demo source for the archive (non-production builds only, PAC-83) | pairs with A-api |
| **B-api** | clockwise-like | The erasure job (§5), `WriteAuditLogTx`, the amended PAC-67 enum test, the operator status command, the runbook section, `booker_erasure.enabled = true`, `erasure_due_at` filled in. AC-7 (second half), AC-21 (due instant), AC-25–AC-37 | `1c04056`: sha256 `9c2c7cbc…` = `contractHash` |
| **B-web** | smart-calendar-flow | Regenerated types only (`AuditEntry.action` gains one value). Optionally a label on the activity page for the new action. No copy change: the confirmation follows `enabled` | pairs with B-api |

Shipping A and B together, as one api PR and one web PR, is also allowed. In
that case use bundle B only. What is **not** allowed is any build where the web
promises erasure while the server's `enabled` is false. §2.4 makes that build
impossible to produce, as long as the web never hard-codes the promise.

**How A-api takes bundle A (binding, challenge K4-6).** The tip of
`factory/pac-40-spec` carries bundle **B**: `calendar.yaml` gains
`scheduling_link_bookers_erased`, and the regenerated `openapi.yaml` and
`backend/api/gen` follow it. On that bundle,
`TestPAC67_AuditEnumMatchesTheWriters` fails until B adds the writer. So
A-api must not build on the branch tip. It starts from `origin/main` and takes
`contracts/openapi/`, `backend/api/gen/` and the docs from `07f912d`, for
example with `git checkout 07f912d -- contracts/openapi backend/api/gen`.
Before it opens, it checks that its `openapi.yaml` sha256 is `d5cf6be9…` and
that `git diff origin/main -- contracts/openapi/paths/calendar.yaml` is empty.
A-web generates from the same bundle A. A combined A+B PR uses bundle B and
contains the writer and the amended test.

A without B does not hide bookings the owner chose to keep, because the
archive ships with the deleted state. Revision 6.1's round 6 non-blocking 7
asked for exactly that.

## 7. Compatibility and rollback

- **Older web build against new api (A).** A deleted link disappears from the
  list. The old dialog's copy ("People with this link will no longer be able
  to book.") stays true and promises nothing more. A stale tab that edits a
  link deleted elsewhere gets 404 on its pre-edit `GET` (`schedulingLinks.ts:351`)
  and shows an error. That is the intended outcome.
- **New web against old api.** `GET /api/scheduling-links/archive` answers 400
  "invalid id" (the request reaches `getLink`). The web treats as "archive
  unavailable" any non-200, **and also a 200 whose body fails its generated
  schema** (challenge K4-7). The generated object schemas are strict, because
  `additionalProperties: false` becomes `.strict()`. So an A-web build reading
  a later server that adds a field must degrade the same way, never throw
  into the page. In that case the web shows no archive and promises no
  erasure. The web can therefore deploy first.
- **Older web builds and strict parsing (verified).** The shipped web does not
  zod-parse any scheduling-link or audit response. `normalizeSchedulingLink`
  and `normalizeAuditEntries` in `src/api/client.ts` are tolerant, and
  `describeAuditEntry` passes unknown actions through. The new enum value and
  the description-only changes therefore break no shipped build.
- **Rollback of A.** Code revert plus `037` down: deleted links reappear as
  paused. No row is lost. **The deletion is lost, though (challenge K4-8).**
  After the down step, every deleted link is an ordinary paused link that its
  owner can resume. Upgrading again does not bring the deletion back, because
  `deleted_at` was dropped. Those links are then outside the archive and
  outside erasure. Their bookers' names and emails are kept until the owner
  deletes the link again. B-api's runbook section says this. It also tells
  the operator to record the ids of the deleted links before a rollback of A
  (the status command and `SELECT id FROM scheduling_links WHERE deleted_at IS
  NOT NULL`). Whether the down step should keep `deleted_at`, as PAC-71's
  down step keeps identity bindings, is open for the owner. It would change
  spec §7.
- **Rollback of B.** Code revert: the job stops. Erasures already done
  **stay**, by design (spec §7). The archive keeps rendering erased bookings,
  because A still knows `booker_erased_at`. Reverting A after B shows erased
  bookings blank (§4).
- **The audit enum.** After a rollback of B, existing
  `scheduling_link_bookers_erased` rows are outside the enum of bundle A. The
  `AuditEntry.action` description already prescribes rendering an unknown
  value rather than discarding the response. The shipped web passes non-empty
  unknown strings through.

## 8. Consumers

- **smart-calendar-flow** `src/pages/Links.tsx:376-379`: the delete
  confirmation copy changes, built from the policy. A new archive view.
  `src/api/schedulingLinks.ts` gains `archive` and `archivedLink`, both behind
  `demoOrReal`. `src/api/generated` is regenerated (A, then B).
- **smart-calendar-flow** `LinkBookingsDialog.tsx`: unchanged. The live
  bookings path never returns erased rows.
- **clockwise-like backend tests**: `TestSchedulingLinksOwnerOnlyBookingsAndDelete`
  still passes. A repeated delete becomes 404, and no test repeats it.
  `decodeOwnedShared` is unaffected, because the list shape is unchanged
  (§2.4). `TestPAC67_AuditEnumMatchesTheWriters` is amended in B (§5).
- **e2e**: `e2e/seed/seed.sql:270`'s comment ("COUNT … status <> 'cancelled'")
  goes stale and is corrected. The seeded exhausted link stays exhausted.
  AC-18 seeds its own link. `scheduling-links.spec.ts` and
  `pac25-access.spec.ts` are unchanged.
- **mcp**: no scheduling-link caller (grep). Not affected.

## 9. Findings and uncertainties

- **Resolves:**
  - API-049 (aliases): becomes "Fixed (PAC-40)" in `docs/factory/api-audit.md`.
  - API-050 (soft delete in two of three read paths): "Fixed (PAC-40)".
  - API-051 (uses count): "Fixed (PAC-40)".
- **Already fixed on main:** API-006 / U-08 (PAC-25) and API-073
  (NEW-booking-rate-limit).
- **Not resolved:** API-053, API-069, API-072 and API-019 (spec §5); the OQ-4
  races; OQ-6 (duration overflow; `createPublicBooking` keeps main's 400 text);
  OQ-7 (window aliases).
- **`x-uncertain`:** 6 in the bundle before and after. No PAC-40 operation
  carries one.

## 10. What a challenger should try to break

- A non-owner sends `GET /api/scheduling-links/archive/{id}` for a deleted id
  and for a random id. The two answers must be byte-identical.
- The owner sends `GET /api/scheduling-links/archive/{id}` for their own
  **paused** link. It must be 404, because the link is not in the archive.
- PATCH `{"active": true}` on a deleted link must be 404 and leave it deleted.
- A malformed body on a deleted link must be 404, not 400.
- A booker name equal to `""` or `"null"`: `""` cannot be submitted, and
  `"null"` is a string, distinct from JSON null.
- Bundle A: `erasure_due_at` must be null on every entry, and the web must not
  render a date.
- The erasure's failure path with a `pq` error whose Detail holds the row: no
  log line, no Sentry event and no audit entry may contain it.
- Two concurrent runs: exactly one audit entry.

## 11. Gate results (contract stage)

The bundle was regenerated in an LF clone (`golang:1.25-bookworm`, oapi-codegen
v2.4.1, following the api-gate pattern, with the host repo mounted read-only),
2026-11-13:

- **Part A** (`07f912d`): `make openapi` rc 0, 132 operations (12 generated);
  regenerated `openapi.yaml`, `MIGRATION.md` and `backend/api/gen/paceday.gen.go`.
  `make openapi-check WEB=/nonexistent` rc 0: "bundle is in sync (110 paths)",
  "MIGRATION.md covers all 132 operations", "backend/api/gen matches the
  contract"; the web half was SKIPPED by design. `cd backend && go build ./...`
  ok; `go vet ./api/` ok. sha256 `d5cf6be903c182c18bc98a8b0f259c649dbe521982698e0de7ec68b4518ff9d9`.
- **Part B** (`1c04056`): the same checks, all rc 0, build ok. sha256
  `9c2c7cbc3abe8ae082b0b920cd39e0e6383fc175de6c6c1dfcd59c1a77426e4a` = `contractHash`.
- **Main `68540f6`** bundle, for reference: `5367da004130aa9037752baa1e17be951f9d4130d46e0cf0ce6e467975603328`.
- **Not run:** the test suites, lint and coverage. This is the contract stage
  and no handler changed. At `1c04056` `TestPAC67_AuditEnumMatchesTheWriters`
  is **expected to fail**: the enum has 11 values and the new one has no writer
  until B-api adds it, together with the test amendment. A-api builds on
  `07f912d`, where the enum is unchanged. §6 says how.

## Challenge — 2026-11-13 (contract-challenger, round K4, revision 3)

**Verdict**: accept-with-changes. All changes are applied in this commit, to
this document and to the manifest. No fragment changed, so the bundles and
both hashes are unchanged: A is `d5cf6be9…` at `07f912d`, and the full
contract is `9c2c7cbc…` at `1c04056`. I re-checked both with
`git show <c>:contracts/openapi/openapi.yaml | sha256sum`.
`scripts/openapi_assemble.py --check` reports "OK: bundle is in sync (110 paths)".

### Requests wrongly permitted

1. **K4-1 (high). A concurrent PATCH puts a deleted link back on the public
   page.**
   - The sequence:
     1. The owner sends `PATCH /api/scheduling-links/{id}` with
        `{"title":"x"}`.
     2. `updateLink` reads the link, which is live with `active=true`
        (`handlers_scheduling_links.go:525`), then reads the zone and the slug.
     3. Meanwhile the owner's `DELETE` commits `deleted_at=now(), active=false`.
     4. `UpdateSchedulingLink` then writes `active = $13 = true`
        (`scheduling_links.go:165-185`). Its `UPDATE` has no `deleted_at`
        condition.
   - The result is a row with `deleted_at` set and `active=true`:
     - every live path answers the owner with 404;
     - the public lookup (`slug = $1 AND active = true`, `:118-128`) still
       books the link.
   - Revision 3 said the public path "needs no new check" and only
     recommended the CHECK that guards this.
   - Fix: §3 and §4 now bind three rules:
     - the CHECK `deleted_at IS NULL OR active = false`;
     - `deleted_at IS NULL` in the by-id PATCH and DELETE writes, with zero
       rows answering 404;
     - `deleted_at IS NULL` in the public slug lookups.
2. **K4-2 (medium). A repeated or concurrent DELETE moves `deleted_at`.**
   - Revision 3's delete is a lookup followed by an unconditional update.
   - Two DELETE requests that pass the lookup together both answer 204, and
     the second one overwrites the instant the archive shows.
   - Fix: the delete runs `UPDATE … WHERE deleted_at IS NULL`, and zero rows
     answers 404 (§3).
3. **K4-5 (medium). The read-only operator status would apply migrations.**
   - `paceday-operator booker-erasure status` would take the PAC-71 path in
     `cmd/paceday-operator/main.go:36-52`. That path opens with
     `storage.OpenAs`, which runs `runMigrations` (`storage/db.go:28-38`).
     After a rollback of A, a listing would therefore re-apply 037 without
     saying so.
   - Its `--check` exit 1 also clashes with PAC-84's "failed" exit
     (`encryption.go:23-31`).
   - Fix: §5 now binds the PAC-84 conventions:
     - it is dispatched like `encryption` and opens with `OpenNoMigrate`;
     - it never migrates;
     - it exits 0, 1 or 2, and 3 when a link is overdue under `--check`;
     - it is not audited.

### Responses the client cannot handle

1. **K4-7 (low). A valid 200 can fail an older web build's parse.**
   - `listArchivedSchedulingLinks` may answer 200 with a member that a strict
     generated schema in an older A-web build rejects, for example a later
     additive field.
   - The manifest said only "any non-200 = no archive".
   - Fix: §7 now also treats a body that fails to parse as "archive
     unavailable".

### Consumers affected

- **`backend/storage/guarded_writes.go:142-148` and `:352`.** The PAC-71
  `…Ctx` wrappers of `DeleteSchedulingLink` and `UpdateSchedulingLink` must
  follow any signature change made for K4-1 or K4-2, such as a `found` result.
  The file was missing from `allowedPaths` and is added.
- **`e2e/db.ts` and `e2e/fixtures.ts`.**
  - The helper that AC-18 uses to set a booking cancelled lives there, so
    both files are added to `allowedPaths`.
  - `fixtures.ts:225` deletes bookings by `booker_email LIKE 'e2e.booker.%'`,
    and an erased row (`''`) would escape that cleanup. No e2e test runs
    erasure, so nothing breaks today.
- **smart-calendar-flow `src/api/types.ts`.** The web's hand-written types live
  here (`AuditEntry` at `:249`). Added to `allowedPaths` for the archive types.
- **smart-calendar-flow `src/api/operations.ts`.** The PAC-83 registry is
  derived from `schedulingLinksApi`, so new members are covered without
  editing this file (verified).
- **mcp.** It has no booking or scheduling-link caller (`mcp/tools.go`,
  verified).

### Contract/implementation divergences

1. **K4-4 (medium). The hygiene rule names the wrong driver's error.**
   - Revision 3 framed log and Sentry hygiene around a `pq` error's Detail.
     The driver is pgx through `database/sql` (`storage/db.go:113-125`), so
     a constraint failure is a `*pgconn.PgError`.
   - That type's `Error()` leaves out `Detail` but keeps `Message`, and for
     some error classes `Message` repeats the input.
   - An AC-33 test that injects only a `*pq.Error` would not exercise the
     type that production returns.
   - Fix: §5 now binds three rules:
     - the class is taken with `errors.As`, plus `Code`;
     - the database error is never wrapped into a logged or reported error;
     - AC-33 injects a `*pgconn.PgError` with the name and email in both
       `Message` and `Detail`.
2. **K4-3 (medium). The lock must be `FOR UPDATE`, and the erasing `UPDATE`
   must carry the due rule.**
   - The failure, with `FOR NO KEY UPDATE`, which an implementer might choose
     as "enough to serialise runs":
     - a booking insert's `FOR KEY SHARE` foreign-key check does not wait for
       that lock;
     - in READ COMMITTED, a booking committed between the step-2 re-check and
       the step-3 `UPDATE` is visible to the `UPDATE`;
     - so a booking for a future meeting would be erased as soon as it is
       inserted.
   - Fix: §5 now binds both points and states the lock order.
   - Deadlock analysis: there is no lock cycle today.
     - Booking creation is one autocommitted insert (true at K4; since
       PAC-101 it is one transaction holding the link `FOR SHARE`, still
       with no `users` row lock: §5).
     - No product path locks a `users` row and then a link.
     - A future account-deletion cascade would show up as `40P01` on one link,
       and the next run retries that link.
3. **K4-6 (medium). A-api could build on the wrong bundle.**
   - The manifest says A-api "builds on 07f912d", but the branch tip carries
     bundle B. An implementer who branches from the tip would get:
     - a bundle hash that is not A's;
     - a failing `TestPAC67_AuditEnumMatchesTheWriters`, whose
       `WriteAuditLog(Ctx)?\(` pattern does not even match `WriteAuditLogTx(`
       (`pac67_surfaces_test.go:471`).
   - Fix: §6 now gives the procedure, plus a check that `calendar.yaml` is
     unchanged.

### Uncertainties not actually resolved

1. **K4-8 (medium, owner question). A rollback of A loses the deletion.**
   - Revision 3 says a rollback of A loses no row. That is true, but the
     deletion itself is lost:
     - deleted links come back as paused links that their owner can resume;
     - upgrading again cannot restore them;
     - so they are never archived and never erased.
   - What changed: §7 now says this, and the B runbook must say it too.
   - Open for the owner: whether the down step should keep `deleted_at`
     (this changes spec §7).
2. **OQ-4 (medium, already a follow-up). A booking can land on a link deleted
   mid-request.**
   - `createBooking` looks the link up without a lock
     (`handlers_booking.go:333`) and inserts much later, after reading the
     host calendars (`engine/booking.go:431-500`).
   - A booking can therefore land on a link deleted in between. It gets a
     201, plus the events and the email from the PAC-90 completion unit, and
     its owner sees it only in the archive.
   - Erasure handles it (AC-34): the meeting is still ahead, so the link is
     not due, and K4-3 stops an early erasure.
   - File the follow-up before B ships.
3. **K4-9 (low). The archive list can return 500 on legacy dates.**
   - A booking stored before PAC-72's horizon could end near the year 9999.
     Adding 90 days to `last_booking_end_at` then goes past what Go's
     `time.MarshalJSON` accepts, and the archive list answers 500.
   - The erasure scan is safe, because it compares
     `max(end_time) <= now - 90 days`.
   - The plan should check the data for such rows. No contract change.

### Verified clean

- **Route ordering.**
  - chi v5.4.0 tries static children before parameters (`tree.go:410-540`).
    So `GET /archive` and `GET /archive/{id}` never reach `getLink`.
  - On a method miss, chi backtracks to `/{id}`. PATCH or DELETE on
    `/archive`, and POST on `/archive/accept`, reach the live handlers with
    `id="archive"`. They answer 400 "invalid id", as on main, and write
    nothing.
  - `GET /archive/{uuid}/bookings` is chi's 404.
  - A new web against an old API gets 400, as §7 says.
- **404 uniformity and timing.**
  - Archive detail is one lookup filtered on owner and deleted. Ids that
    never existed, live, paused and not-owned ids each cost one query.
  - On a deleted link, `getLink`, PATCH, DELETE, `/bookings`, `/hosts` and
    `leave` answer 404 at the lookup. That is the same single query as an
    unknown id.
  - A stranger on a live link costs one extra host-status query. That comes
    from PAC-25 and is unchanged.
  - The body is `writeError`'s `{"error":"not found"}` in every case.
- **The public path and other tickets.**
  - PAC-88 slot listing, PAC-72 horizon and checks, and the 410 all go
    through `GetSchedulingLinkBySlug`.
  - Busy time (`GetBusyBookingsForUser`, `:465-494`) has no deleted filter.
    AC-7 therefore holds, and erasure changes no time.
  - PAC-84: `bookings` is not in its inventory, and erasure needs no key
    (AC-36).
  - PAC-71: `emptiness.go:41-42` counts bookings with no status or deleted
    filter.
- **Readers of booker data.**
  - Besides the live bookings read, three places read it, all only when a
    booking is created:
    - the public confirmation DTO (`handlers_booking.go:112`);
    - the host event attendee (`engine/booking.go:642`);
    - the email and ICS (`:729`).
  - A calendar event's `Summary` is the link title and its `Description` is
    the notes. Events therefore hold emails but not names, and the runbook
    covers that.
  - No analytics, recap, MCP or web path reads `bookings`.
  - The completion unit cannot race an erasure: a link with a booking still
    ahead is never due.
- **`''` versus NULL.** `NOT NULL` is kept and the CHECK is sound. The down
  step drops the CHECK without failing. No reader other than the archive sees
  `''`.
- **Audit in the same transaction.** `WriteAuditLog` swallows its errors
  (`focus_blocks.go:79-87`), so a `…Tx` sibling that returns its error is
  needed. `NOW()` in that sibling is the transaction's time.
- **Schedule.** `cronJob` (`main.go:45-58`) gives AC-35 the drain behaviour
  and the rule that no run starts once shutdown has begun.
- **The oapi-codegen workaround is sound.**
  - `gen.Ready` survives at both `07f912d` and `1c04056`
    (`readiness.go:80`).
  - The new bare constants `AwaitingErasure`, `Erased` and `NoBookings`
    clash with nothing.
  - `minimum: 90, maximum: 90` acts as a constant on the wire and in zod.
  - The approach is fragile: a future enum value with the same name would
    re-trigger the renaming. §2.3 records this.
- **Older web builds.** No shipped build zod-parses a scheduling-link or
  audit response (§7).
- **Migration number.** 037 is still free. Main is at `68540f6`, and no
  `origin/*` branch carries 037 or higher (checked 2026-11-13).
  `idx_bookings_link` exists for the scan.
- **PATCH spelling.** The web sends only the canonical spelling on PATCH
  (`backendLinkBody`). API-049's flip changes nothing for it.
- **Acceptance tests.** The manifest's acceptance tests match spec AC-1 to
  AC-37. AC-20 and AC-20a were retired in spec revision 6.
