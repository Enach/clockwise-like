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

The public operations by slug already filter on `active = true`. Delete also
sets `active = false` (§4), so the public 404 needs no new check. The slug
check (`SlugExists`) still counts deleted rows (D-3, AC-8).

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
  code revert turn every deleted link into a paused one (spec §7). Recommended:
  `CHECK (deleted_at IS NULL OR active = false)`. With that check, a PATCH
  already in flight that writes `active = true` onto a deleted link fails
  instead of reviving it (OQ-4; the race itself stays out of scope).
- **Binding:** `bookings.booker_erased_at TIMESTAMPTZ NULL`. An erased booking
  stores `booker_name = ''` and `booker_email = ''`, enforced by
  `CHECK (booker_erased_at IS NULL OR (booker_name = '' AND booker_email = ''))`.
  `NOT NULL` stays on both columns. On the wire, `''` with `booker_erased_at`
  set is returned as null (§2.2). It is never returned as `""`.
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
error: a `pq` constraint error's Detail repeats the failing row, and that row
would contain the name and email. The same rule applies to Sentry. Send a
sanitised error, or none, and never the raw database error. No line names a
booker.

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

It exits 0, and 1 with `--check` when any link is overdue. Its output never
contains a name, an email or a title. The last run's time is the newest
`booker-erasure: run` log line. The runbook says so.

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
  "invalid id" (the request reaches `getLink`). The web treats any non-200 as
  "archive unavailable", shows no archive, and promises no erasure. The web
  can therefore deploy first.
- **Rollback of A.** Code revert plus `037` down: deleted links reappear as
  paused. No row is lost.
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
  `07f912d`, where the enum is unchanged.
