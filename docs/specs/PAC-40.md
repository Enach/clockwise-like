# Spec — PAC-40: A deleted scheduling link does not go away, the same request means different things on create and edit, and cancelling a booking re-opens a used-up link

- **Linear**: https://linear.app/paceday/issue/PAC-40
- **Status**: challenged
- **Author**: spec-author
- **Revision**: 6, written against `origin/main` at `68540f6` (web
  `smart-calendar-flow` `origin/main` at `ae72939`). Every citation in §2 is to
  those commits. Revisions 1–5 cited `6d751f9`; since then PAC-25 (`69ffa92`),
  PAC-72 (`bd4b8b0`), PAC-84 (`32ecacf`), PAC-71 (`80f68a4`, `2f0ff45`), PAC-88
  (`a331c15`), PAC-90 (`bc7b144`) and NEW-booking-rate-limit changed the files
  this spec touches. Nothing was run for this revision.
- **Inherited scope**:
  - **API-050** (soft delete visible in two of three read paths), **API-049**
    (alias precedence inverted between create and edit), **API-051**
    (`uses_count` recomputed from live bookings) — named by the issue, all
    still "Reported" (`docs/factory/api-audit.md:307-309`).
  - **PAC-30 D-5 follow-up `inactive-link-invites`**
    (`contracts/features/PAC-30.md:124-129`) — only for *deleted* links (AC-5,
    D-4). The paused-link half stays as PAC-30 left it.
  - **Owner decision PAC-40, 2026-10-27** (`.claude/factory/owner-decisions.md`,
    "PAC-40, deleted links"): an archive of a deleted link's past bookings, and
    erasure of bookers' names and emails 90 days after the link's last booking
    date. This replaces revisions 1–5's D-4 (owner gets "not found" on a
    deleted link's bookings) and OQ-1/OQ-2/D-2 (keep everything indefinitely).
  - **No longer inherited, fixed on main:**
    - **API-073** (dead code in public booking create) — "Fixed
      (NEW-booking-rate-limit)" (`api-audit.md:331`); the two dead checks are
      gone from `createBooking` (`backend/api/handlers_booking.go:324-486`).
      Revisions 1–5's §3.5, AC-20 and AC-20a existed only to protect that
      removal and are retired.
    - **API-006** and the `x-uncertain` on `getSchedulingLink` — "Fixed
      (PAC-25)" (`api-audit.md:264`, `:478`). Main answers every caller who is
      not the owner or an accepted co-host with the unknown-id `404
      {"error":"not found"}` (`handlers_scheduling_links.go:488-502`;
      `contracts/openapi/paths/booking.yaml:869-893`). AC-19 now pins that
      answer instead of asking for a refusal; revisions 1–5's "403" reading
      (OQ-5, and contract C-1 on `factory/pac-40-contract`) is withdrawn.
  - Not inherited: **API-053** (`days` alias drops unrecognised day names) is
    on the same create operation but is a validation-message defect, not a
    lifecycle one; it stays open (§5).

## 0. The owner's answer and how this spec reads it

The owner's decision, as recorded on 2026-10-27: "the owner can see a deleted
link's past bookings in an archive; bookers' names and emails are erased 90
days after the link's last booking date." The record carries the caveat that
the owner replied "okay" to a proposed option and that the reading should be
confirmed. The factory orchestrator reported on 2026-11-13, when it
commissioned this revision, that the owner confirmed the recorded answer; this
spec treats it as confirmed. The caveat is still in
`owner-decisions.md` and in no Linear comment; whoever holds that file should
replace it with the confirmation (challenge round 6, non-blocking 1).

This spec reads the answer as follows. Each reading is the plainest one the
words allow; where the words leave a choice, the choice is a factory decision
listed under Decisions and open to correction by the owner without blocking
this spec (OQ-8, OQ-9, OQ-10).

- **R-1 "the owner"** — the link's owner only. Accepted co-hosts do not get
  the archive: the owner's sentence names the owner, and today only the owner
  can read a link's bookings at all (`handlers_scheduling_links.go:694-697`).
- **R-2 "a deleted link's past bookings"** — every booking the link received
  before it stopped taking bookings, whatever its status and whether its
  meeting time is before or after the moment the owner looks. A deleted link
  takes no new bookings, so all of its bookings are "past bookings" of the
  link. (Reading "past" as "whose meeting has already happened" would hide a
  meeting that is still going to happen, from the one person who has to
  attend it.)
- **R-3 "in an archive"** — a separate, read-only place where the owner finds
  their deleted links and their bookings. Deleted links do not come back into
  the owner's normal list of links, and nothing in the archive changes a link.
- **R-4 "bookers' names and emails are erased"** — the name and the email
  address each booker typed are removed from Paceday's database, for every
  booking of that link. What else is kept or erased is in §3.5 (D-11, OQ-8).
- **R-5 "90 days after the link's last booking date"** — the link's last
  booking date is the latest end time of any booking on the link, whatever its
  status (D-12, OQ-10). Erasure becomes due at that instant plus 90 × 24
  hours. It applies only to **deleted** links: the decision is about deleted
  links, and live or paused links keep their bookers' details as today (OQ-9).
  If a link is deleted when its last booking date is already more than 90
  days past, erasure is due at once and happens at the next run.

## 1. Problem

A Paceday user shares scheduling links with external bookers. Three things about
a link's life go wrong today, and one thing the owner has decided is missing.

**Deleting a link does not delete it.** The owner deletes a link, the public
page stops taking bookings, and the link stays in the owner's list — forever,
with nothing to show it was deleted beyond a switch reading "Paused". Its
co-hosts keep it in their list too. Pressing the switch brings a "deleted" link
back to life. Anyone who created a link by mistake has no way to make it go away,
which PAC-59's challenge already identified as a blocker for one-press link
creation (`docs/specs/PAC-59.md:353-358`).

**A deleted link's bookings have no home, and their bookers' details are kept
forever.** Once a deleted link leaves the owner's list, the owner loses the
only way to see who booked it. Keeping it in the list is the defect above. And
the names and email addresses of the people who booked it stay in the database
indefinitely, with no rule for when they go. The owner decided both: the
bookings stay visible in an archive, and the bookers' names and emails are
erased 90 days after the link's last booking (§0).

**The same request means different things depending on whether the link is new.**
The API accepts two spellings for the meeting durations and for the bookable
days. When a client sends both, creating a link keeps one spelling and editing a
link keeps the other. Paceday's own web app sends only one spelling, so no user
hits this today; a third-party or MCP client that sends both gets a link that
changes meaning the first time it is saved.

**A single-use link can be used twice.** "Single use" and "limit N bookings" are
promises the owner makes to themselves: this link will not take more than N
meetings. The count behind that promise is recomputed from live bookings on
every read, so a cancelled booking hands its use back and the link silently
re-opens to whoever still has the URL. Nothing in Paceday cancels a booking yet
(cancelling the calendar event leaves the booking as it was), so this is latent
— it becomes a real defect the day cancellation ships.

Who pays: owners (both seats in PRODUCT.md) who cannot clean up their links or
find a deleted link's bookings; co-hosts who see links that no longer exist;
bookers whose details outlive any use; and the owner's calendar, which can take
a meeting from a link the owner believed was used up.

## 2. Current behaviour

Citations are to `clockwise-like` at `68540f6` and `smart-calendar-flow` at
`ae72939` unless stated otherwise. `handlers_scheduling_links.go` and
`handlers_booking.go` are in `backend/api/`; `scheduling_links.go` is
`backend/storage/scheduling_links.go`.

### 2.1 "Deleted" and "paused" are the same state

- One stored flag means both. The table has a single boolean `active`, default
  true (`backend/storage/migrations/009_scheduling_links.up.sql:12`). No later
  migration adds a lifecycle column or a deletion time (migrations 010–036; the
  only later change to `scheduling_links` is `032_scheduling_link_horizon_refusal`).
- Deleting sets that flag false and nothing else (`scheduling_links.go:186-190`,
  run under the PAC-71 session re-check by `DeleteSchedulingLinkCtx`,
  `backend/storage/guarded_writes.go:142-148`), called from `deleteLink`
  (`handlers_scheduling_links.go:649-675`), which answers 404 for an unknown
  id (`:662-665`), 403 `forbidden` for anyone but the owner (`:666-669`) and
  204 for the owner (`:674`).
- Editing a link can set the same flag either way
  (`handlers_scheduling_links.go:589-591`, persisted as `active = $13` at
  `scheduling_links.go:171`). The contract says so: "Setting false is the same
  soft-deactivation that DELETE performs" (`contracts/openapi/paths/booking.yaml:2542`).
- The web app uses the flag as a **pause** switch. Each owned card shows
  "Active" or "Paused" and a toggle (`src/pages/Links.tsx:145-150`) that sends
  an edit with the flag (`Links.tsx:352-355`, wired at `:443`); Delete is a
  separate action behind a confirmation that reads "People with this link will
  no longer be able to book." (`Links.tsx:376-379`, mutation `:333-339`). So in
  the product, `active = false` already means two different things the server
  cannot tell apart.

### 2.2 The read paths disagree about the flag (API-050)

| Read path | Filters on the flag? | Citation |
|---|---|---|
| Public link by slug (info, slots, book) | yes — `AND active = true`, so 404 | `scheduling_links.go:118-128` (filter `:125`); used at `handlers_booking.go:123`, `:193`, `:333` |
| Owner's / co-host's link list | no | `scheduling_links.go:142-160`; `handlers_scheduling_links.go:441-470` |
| One link by id | no — 200 with `active: false` to the owner and accepted co-hosts; 404 to everyone else (PAC-25) | `scheduling_links.go:130-140`; `handlers_scheduling_links.go:473-515` |
| Edit, delete, list bookings, invite co-host, leave | no (same by-id lookup) | `handlers_scheduling_links.go:525`, `:657`, `:685`, `:718`, `:820` |
| Co-host invite list | no | `scheduling_links.go:343-361`; `handlers_scheduling_links.go:760-781` (link looked up at `:769`) |
| Accept / decline | no — the host row is updated whatever the link's state; PAC-30 D-5 kept it | `scheduling_links.go:381-395`; `handlers_scheduling_links.go:784-804`; `contracts/features/PAC-30.md:124-129` |

Consequences, all reachable today: a deleted link stays in its owner's list and
in its accepted co-hosts' shared list; it can be fetched by them, edited
(including back to active), and have co-hosts invited to it; a pending invite
to it is still listed and can be accepted. On a deleted link a pending
invitee's leave answers 204 and deletes their host row, and the owner's leave
answers 400 `owner cannot leave link` (`handlers_scheduling_links.go:829-843`;
challenge round 5 probe).

- A deleted link's slug stays reserved: the slug check counts every row
  regardless of the flag (`scheduling_links.go:521-526`; used at
  `handlers_scheduling_links.go:410`, `:614`).
- Bookings on a deleted link still make their hosts busy, because the busy-time
  query does not look at the flag (`scheduling_links.go:469-495`, PAC-64,
  PAC-72 D-11).
- On the public page a paused link and a deleted link are indistinguishable:
  both are 404 "not found". The booker page's "used up or paused" copy is shown
  for the 410 case only (`src/pages/PublicBooking.tsx:393-399`), so a paused
  link actually reads as "Link not found".

### 2.3 A link's bookings and who can read them

- A booking row holds the booker's name and email as typed, the meeting's start
  and end, a status (`pending`, `confirmed`, `cancelled`), free-text notes and
  a creation time (`migrations/009_scheduling_links.up.sql:26-36`; name and
  email are `NOT NULL`, `:29-30`). Rows are written once by the public booking
  (`backend/engine/booking.go:484-491`, `scheduling_links.go:422-429`); nothing
  updates them afterwards (grep of `backend/` and `mcp/` for `UPDATE bookings`:
  none).
- The owner reads a link's bookings through one operation, owner-only, 403 for
  anyone else (`handlers_scheduling_links.go:677-708`; contract
  `booking.yaml:1050-1080`, "Bookings made on a link (owner only)"). It returns
  every row of the link, newest meeting first, with name and email
  (`scheduling_links.go:440-461`). It does not look at the flag, so today the
  owner can still read a deleted link's bookings — but only by knowing its id,
  because the link is reached through the list.
- The web app shows them in a bookings dialog opened from a link card
  (`src/components/links/LinkBookingsDialog.tsx`, booker shown at `:81`;
  opened from `Links.tsx:467`; API call `src/api/schedulingLinks.ts:400-407`).
  There is no view of deleted links.
- The booker's name and email also leave the database at booking time, to
  places Paceday cannot later change: each host's calendar event lists the
  booker as an attendee and carries the notes as its description
  (`engine/booking.go:642`, `:658-660`); the confirmation email and its ICS go
  to the booker (`engine/booking.go:729`, `:766-831`). A failed confirmation
  email is logged with the booking id and with addresses redacted
  (`engine/booking.go:683-686`), so the logs do not carry them.

### 2.4 Retention, backups and encryption of bookers' details today

- **No retention.** Nothing erases or expires a booking or a link. The only
  removal is the account cascade: a link's rows go when its owner's account is
  deleted (`migrations/009_scheduling_links.up.sql:3`, `:28`). The e2e suite
  hard-deletes link rows in test teardown only
  (`e2e/tests/scheduling-links.spec.ts:122`, `:201`;
  `e2e/tests/public-booking.spec.ts:698`; `e2e/tests/pac25-access.spec.ts:74`).
- **Not encrypted.** PAC-84 encrypts 18 secret columns at rest; none is on the
  bookings table (`contracts/features/PAC-84.md:96-113`, "Inventory: the 18
  secret columns"). A column joins that inventory only through a later
  contract (`PAC-84.md:98-99`). Bookers' names and emails are therefore stored
  and dumped in plain text. PAC-84 also binds every encrypted value to its
  table, column and row, so a later migration cannot move or copy a secret in
  SQL (`migrations/034_encryption_at_rest.up.sql:6-10`).
- **Backups.** A daily dump is kept 14 days by default
  (`docs/runbook.md:265-296`; `scripts/ops/backup.sh`, `--keep-days`). A dump
  holds every booker's name and email in plain text. A restore brings back
  whatever the dump held (`docs/runbook.md:328-365`).
- **Account data inventory.** PAC-71's emptiness check counts any link the
  user owns and any booking on such a link as account data
  (`backend/storage/emptiness.go:39-41`).

### 2.5 Background jobs today

- Scheduled work runs inside the backend process. `main.go` registers
  `robfig/cron` jobs (auto-decline every 15 minutes, `backend/main.go:224-239`;
  daily recap every minute, `:241-251`; manager detection weekly, `:253-272`)
  and the focus-time loop (`:193-194`). Each cron function is wrapped by
  `cronJob` as a tracked unit that the PAC-81 shutdown drain awaits and that
  never starts once shutdown has begun (`main.go:45-58`).
- The focus-time loop is the one job with an injectable clock: `now` and
  `logf` are fields that tests replace (`backend/scheduler/cron.go:36-38`,
  set at `:57-58`).
- One backend runs per deployment and a deploy is not zero-downtime
  (`docs/runbook.md:238`).
- Writes made from a request run under the PAC-71 session re-check when the
  request carries a session guard; without one (scheduled jobs) the plain
  function runs (`backend/storage/guarded.go:10-17`).
- The owner's activity log is a per-user audit trail the owner can read
  (`backend/storage/audit_log.go:23-48`, served as `GET /api/audit`); entries
  are written with `WriteAuditLog` (`backend/storage/focus_blocks.go:79-87`).

### 2.6 Alias precedence (API-049)

Create and edit accept `duration_options` or its alias `durations`, and
`days_of_week` or its alias `days` (`handlers_scheduling_links.go:82-85`,
`:104-107`).

- **Create** picks the canonical spelling when it is not null, the alias
  otherwise (`handlers_scheduling_links.go:172-190`).
- **Edit** applies the canonical spelling, then the alias over it
  (`handlers_scheduling_links.go:552-563`), so the **alias** wins.
- The window start/end aliases agree between the verbs only when the canonical
  value is non-empty. Create treats an empty-string canonical window as absent
  and falls back to the alias (`:191-204`, `if start == ""`); edit treats any
  non-null canonical value as carried, so an empty one wins over the alias
  (`:564-573`, `patch.WindowStart != nil`) and validation then rejects it
  (`parseClock`, `:224-232`). Challenge round 5's probe: canonical `""` beside
  alias `10:00`/`16:00` gives 201 with a 10:00-16:00 window on create and 422
  `{"error":"window times must use HH:MM"}` on edit. Out of scope here (§5).
- The web app sends only the canonical spellings
  (`src/api/schedulingLinks.ts:159-174`, body at `:166-167`). No caller in
  `mcp/` builds a link body (grep of `mcp/` for the four field names: no hits).
- The only test that sends an alias on edit sends aliases alone, never both
  (`backend/api/handlers_scheduling_links_test.go:128-157`).

### 2.7 How "uses" are counted (API-051)

- There is no stored count. Every read computes it as the number of the link's
  bookings whose status is not `cancelled` (`scheduling_links.go:109`, `:123`,
  `:135`, `:148`, `:178`). The contract documents this as "Count of
  non-cancelled bookings, computed by subquery at read time"
  (`booking.yaml:2300`).
- A link is exhausted when single-use and the count is ≥ 1, or limited
  ("recurring") and the count is ≥ the cap (`handlers_booking.go:80-88`).
  Exhaustion answers 410 on link info, slots and booking
  (`handlers_booking.go:132-133`, `:202-203`, `:342-343`).
- A booking row allows status `cancelled` (`migrations/009_scheduling_links.up.sql:33`),
  but **no code path in the backend, MCP server or web app ever sets it**
  (grep for writes to booking status: none; the `"cancelled"` checks in
  `handlers_events.go:264` and `engine/auto_decline.go:167` are on calendar
  events). So today a cancelled booking exists only if someone edits the
  database. The re-opening is real but latent.
- The e2e seed depends on the derivation: the exhausted link is seeded by
  inserting a booking, not by setting a count (`e2e/seed/seed.sql:270`,
  `:287-290`, `:334-340`), and `e2e/tests/public-booking.spec.ts:91-106`
  asserts its 410 and its "Link no longer available" page.
- Exhaustion is checked before the booking is written, with no lock between
  the check and the insert (`handlers_booking.go:342`, then `ConfirmBooking`
  at `:467`). Two simultaneous bookings on a single-use link can both succeed.
  Not in the audit; see §5 and OQ-4.

### 2.8 Fetching one link (API-006, fixed by PAC-25)

`getLink` returns the link with its full roster to the owner and to accepted
co-hosts, and the unknown-id `404 {"error":"not found"}` to every other
signed-in caller, including pending and declined invitees
(`handlers_scheduling_links.go:473-515`, check at `:491-502`). A storage error
on the host-status read or the roster is 500, never 404 or a degraded 200
(`:493-497`, `:508-512`). The contract states the check order
(`booking.yaml:869-893`). Pinned by `TestPAC25GetLink*`
(`handlers_scheduling_links_test.go:683-828`) and
`e2e/tests/pac25-access.spec.ts`. The owner-only siblings (edit, delete,
bookings, invite) still answer 403 to a non-owner on a live link
(`:534-537`, `:666-669`, `:694-697`, `:727-730`).

### 2.9 Public booking create (API-073, fixed)

The two dead checks API-073 named are gone: `createBooking` checks exhaustion
once (`handlers_booking.go:342-345`) and has no second end-time error
(`:324-486`). "end must be after start" (`:411-414`) remains reachable only
through an overflowing duration option (revision 2; the contract challenge of
`factory/pac-40-contract` corrected the range: options of 153,722,868 to
307,445,734 minutes wrap to an end before the start, while 307,445,735 to
461,168,601 wrap to an end after it). No test pins that answer today; it is
OQ-6's subject and outside this spec.

## 3. Desired behaviour

### 3.1 Paused and deleted are different states

- **Paused** is what the owner's switch does today, unchanged: the link stays in
  the owner's list and in accepted co-hosts' lists, can be fetched and edited by
  its owner, and can be resumed. Its public page does not take bookings, as
  today. A paused link is not in the archive.
- **Deleted** is what Delete does. From the moment of deletion the link is gone
  from every live surface, for everyone: not in any list of links, not
  fetchable, not editable, not resumable, not bookable, not offered as a
  co-host invite, and not acceptable, declinable or leavable as one. Each of
  those paths answers the same "not found" it gives for an id or slug that
  never existed — to the owner, to co-hosts and to everyone else (D-9). The
  one exception is the owner's archive (§3.2).
- Deleting erases nothing at the time of deletion. The link's record, its
  bookings and its host rows are kept (D-2). Its bookings keep making their
  hosts busy (the meetings still happen). Its slug stays reserved, so a new
  link can never answer at an address an external booker was given for the old
  one (D-3). The moment of deletion is recorded, because the archive shows it.
- Pausing cannot reach a deleted link, and resuming cannot bring one back.
  There is no un-delete.
- These guarantees hold for every request that starts after the delete has
  completed. A booking or an edit already in progress when the delete lands is
  not covered by this spec (§5, OQ-4), except that the erasure rule (§3.5)
  covers any booking that ends up stored on a deleted link, however it got
  there.
- Links that are inactive **before** this change are treated as paused, not
  deleted, because the server cannot tell which they were (D-1). They stay
  visible to their owner exactly as today, are not in the archive, and are not
  subject to erasure. An owner who meant to delete one deletes it again.

### 3.2 The owner's archive of deleted links

- The owner has an archive of the links they deleted (R-1, R-3). It is
  separate from their list of links, read-only, and empty for an owner who has
  deleted nothing.
- Each archived link shows its title, the address it had, when it was deleted,
  how many bookings it has, and either the date on which its bookers' names and
  emails will be erased or the date on which they were erased. A deleted link
  with no bookings says there is nothing to erase. Most recently deleted links
  come first.
- For each archived link the owner can see all of its bookings (R-2), with the
  same details the owner sees for a live link's bookings today: meeting start
  and end, status, notes, booker name and email, and when the booking was made.
  After erasure (§3.5) every other detail is still shown and the name and email
  are shown as erased — an explicit state the owner can tell apart from a
  value, never a blank or the old value.
- Nobody but the link's owner sees an archived link or its bookings: not its
  accepted co-hosts, not its pending invitees, not anyone else. To all of them
  an attempt to read an archived link or its bookings answers exactly as for a
  link that never existed.
- Nothing in the archive changes a link or a booking: no edit, resume, invite,
  restore or delete.
- When the owner deletes a link, the confirmation they are asked for says that
  people with the link will no longer be able to book, that the link and its
  bookings move to the archive, and that bookers' names and emails will be
  erased 90 days after the link's last booking.

### 3.3 Both spellings, one meaning

This rule covers two settings only: the meeting durations and the bookable
days, the two the issue and API-049 name. When a create or an edit carries both
the canonical spelling and the alias for either of them, the canonical spelling
wins and the alias is ignored, on both verbs (D-5). Sending one spelling alone
behaves as today. For these two settings a spelling counts as carried when its
value is present and not null; an empty list counts as carried, so a canonical
empty list beside a non-empty alias is rejected for having no values, on both
verbs, as create already does.

The bookable-hours window (start and end time) is not covered by this rule and
keeps today's behaviour on both verbs, including the divergence for an empty
canonical value described in §2.6 (§5, D-10).

### 3.4 A use, once spent, stays spent

A booking counts against a link's limit from the moment it is made, and
cancelling it does not give the use back (D-6). A single-use link that has been
booked once stays used up whatever happens to that booking; a link limited to N
stays used up after N bookings have been made. The "uses" figure the owner sees
counts bookings made, including cancelled ones. An owner who wants the link to
take more bookings changes its limit, as today. Bookings made before this change
count under the same rule, including any that were cancelled before it (D-8).
Erasing bookers' details (§3.5) removes no booking, so it never changes a count.

### 3.5 Erasing bookers' names and emails from deleted links

**What becomes due, and when.** For each deleted link that has at least one
booking, the link's last booking date is the latest meeting end of any of its
bookings, whatever their status (R-5, D-12). Its bookers' names and emails are
due for erasure once 90 × 24 hours have passed since that instant. Live links,
paused links, and links that were inactive before this change are never
erased by this rule (R-5, D-1, OQ-9).

**What is erased.** For every booking of a due link: the booker's name and the
booker's email address. Afterwards no copy of either remains in any row of
Paceday's database that the application maintains — not in the booking, not
in the audit entry, not in any other table the erasure writes. The erased
state is recorded on the booking so the archive can show it (§3.2).

**What is kept.** Everything else: the link and its deletion time, its host
rows, each booking's existence, meeting start and end, status, notes (D-11,
OQ-8), creation time, and the references to the calendar events it created.
So the link keeps its uses count (§3.4), its hosts stay busy at the booked
times (§3.1), the slug stays reserved, and the owner's archive still shows
each booking.

**What it cannot reach, stated rather than hidden.** Copies outside the
database stay as they are: the attendee and description on each host's
calendar event, the confirmation email and invitation the booker received,
and database backups taken before the erasure, which keep the values until
the backup set is deleted by its retention (14 days by default). A database
page that held an erased value is reclaimed by Postgres' routine cleanup, not
at the moment of erasure. The runbook says all of this (below).

**When it runs.** Automatically, inside the backend, with no operator action:
once shortly after the backend starts and then at least once every 24 hours
(D-13). While the backend is running, a link is erased no later than 24 hours
after it becomes due; a link that became due while the backend was down is
erased by the first run after it starts. An operator can also see which
deleted links are due or overdue, and when the last run happened, without
seeing any booker's name or email.

**Idempotence and safety.**

- A run that finds nothing due changes nothing and records nothing.
- Running again after a successful erasure changes no stored value and writes
  no further audit entry. A booking once erased stays erased.
- Each link is erased as one unit: either all of its due bookings are erased
  and its audit entry is written, or nothing about that link changes. A run
  interrupted by shutdown (the PAC-81 drain) or by a failure leaves every link
  in one of those two states, and the next run finishes the job.
- A failure on one link does not stop the run: it is logged and reported
  without any booker's name or email, and the other due links are still
  erased.
- Two runs at the same time against the same database (a second backend
  started by mistake, or a restart during a run) erase each booking once and
  write exactly one audit entry per link per erasure.
- A booking that lands on a deleted link after an earlier erasure (the
  in-flight race of OQ-4) moves the link's last booking date. It is erased
  90 days after its own end; the earlier bookings stay erased. That second
  erasure writes its own audit entry.
- After a restore from a backup taken before an erasure, the erased values
  are back in the database; the next run, which happens at start-up, erases
  them again and writes a new audit entry.

**Audit.** Each erasure of a link writes one entry in the link owner's activity
log, saying which link (title and id), how many bookings were erased, the last
booking date that made it due, and when it ran. Each run writes one
operator-visible log line with the number of links examined, links erased,
bookings erased and failures. No audit entry, log line or error report carries
a booker's name or email, before or after erasure.

**Encryption (PAC-84).** Bookers' names and emails are not among the secrets
PAC-84 encrypts (§2.4), so erasure removes plain-text values. It does not need
the encryption key, works the same on a deployment with or without encryption
configured, and does not fail because some unrelated stored secret cannot be
decrypted. If a later contract adds these fields to PAC-84's inventory, the
erased state must still be recognisable without the key and erasure must still
remove the value rather than re-seal it; that contract must say how.

**Runbook.** The operations runbook gains a section that says: what the
erasure does and does not reach (the list above); that it runs on its own at
start-up and at least daily, and which log line shows that a run happened; how
to list deleted links that are due or overdue without seeing personal data;
that backups keep erased values until their retention deletes them, so the
backup retention bounds how long an erased value survives anywhere Paceday
controls; that after restoring an older backup the operator should let the
backend start (which runs the erasure) before anyone uses the restored data,
and check the next run's log line; and what to do if the log shows failures.

### 3.6 Fetching one link

Fetching one link returns it to its owner and to its accepted co-hosts — the
same people who see it in their list — and answers everyone else exactly as for
an id that never existed (D-7). This is main's behaviour since PAC-25; this
spec keeps it and adds only that a deleted link answers the owner and the
co-hosts that way too (§3.1).

## 4. Acceptance criteria

Throughout, "not found" means the answer the same path gives today for an id or
slug that has never existed — same status and same body. "Erasure runs at T"
means one run of the erasure with its clock set to the instant T; every
erasure criterion is proven with that clock injected, never by waiting.

**Deleted links (API-050)**

AC-1. Given an owner with a deleted link and an active link, when the owner
lists their links, then only the active link is returned. Given the same
deleted link with an accepted co-host, when the co-host lists their links, then
the deleted link is in neither the owned nor the shared group.  [contract]

AC-2. Given a deleted link, when its owner or an accepted co-host fetches it by
id, then the answer is not found.  [contract] *(fails today: both get 200)*

AC-3. Given a deleted link, when its owner later edits it — with any body,
including one that sets it active — then the answer is not found, and
afterwards the link is still deleted (AC-1, AC-2 and AC-4 still hold).  [contract]

AC-4. Given a deleted link, when anyone later requests its public info, its
slots, or books it, then each answers not found and no booking is created.
[contract]

AC-5. Given a deleted link with a pending co-host invite, when the invitee lists
their invites, then the invite is absent; and when the invitee accepts or
declines it, then the answer is not found and the invite's status is
unchanged. Given a deleted link with an accepted co-host, when the co-host
leaves it, then the answer is not found. Given a deleted link with a pending
invitee, when the pending invitee leaves it, then the answer is not found and
their invite still exists in storage with its status unchanged. Given a deleted
link, when its owner leaves it, then the answer is not found (not the refusal
an owner gets for leaving a live link) and nothing changes.  [contract]
*(the pending-invitee and owner cases fail today: 204 with the host row
removed, and 400 "owner cannot leave link")*

AC-6. Given a deleted link, when its owner invites a co-host to it or deletes
it again, then each answers not found and nothing changes. The owner reads its
bookings through the archive (AC-22); whether the operation that lists a live
link's bookings also serves a deleted link to its owner is the contract's
choice, and the contract states it.  [contract]

AC-6a. Given a deleted link and a signed-in user who is neither its owner nor
an accepted co-host, when that user fetches it by id, edits it, deletes it,
lists its bookings, reads it or its bookings through the archive, or invites a
co-host to it, then each answer is identical in status and body to the answer
the same user gets for an id that has never existed — not the 403 a live link
gives them on edit, delete, bookings and invite — and nothing changes.
[contract] *(fails today: edit, delete, bookings and invite answer 403; fetch
already answers not found since PAC-25)*

AC-7. Given a deleted link that has a confirmed booking in the future, when a
host's busy time is computed for that period, then the booked time is still
busy — before and after that link's bookers' details are erased.  [unit]
*(passes today; pins busy time once a deleted state and erasure exist)*

AC-8. Given a deleted link with slug S, when anyone creates or edits another link
to use slug S, then it is rejected as taken, as for any existing slug — before
and after erasure.  [contract] *(passes today; pins slug reservation)*

AC-9. Given a link with bookings and co-hosts, when its owner deletes it, then
its bookings and host records still exist in storage afterwards, each booking
with its booker's name and email unchanged, and the deletion time is recorded.
[unit] *(the first half passes today; the deletion time fails today)*

AC-10. Given a signed-in owner on the Links page with a link, when they delete
it and reload the page, then the link is not shown; and the link's public
booking page shows the not-found state.  [e2e]

**Paused links stay as they are**

AC-11. Given a link its owner has paused, when the owner lists links or fetches
it, then it is returned with the paused state and it is not in the archive;
when the owner resumes it, then its public page takes bookings again.
[contract] *(the list, fetch and resume half passes today and pins pause and
resume once a deleted state exists)*

AC-12. Given a link that was inactive before this change was deployed, when its
owner lists their links, then it is listed as paused, it is not in the
archive, and the owner can resume or delete it.  [contract] (data-migration
test) *(the listing half passes today; pins D-1 across the migration)*

**Archive (owner decision, 2026-10-27)**

AC-21. Given an owner with one deleted link that has two bookings — one whose
meeting is over and one whose meeting is still ahead — plus a live link and a
paused link, when the owner reads their archive, then it contains exactly the
deleted link, with its title, its former address, its deletion time, its
number of bookings (2) and the instant at which its bookers' names and emails
become due for erasure (90 × 24 hours after the later booking's end).
[contract] *(fails today: no archive)*

AC-22. Given the AC-21 link, when its owner reads its bookings through the
archive, then both bookings are returned — the one still ahead included — each
with meeting start and end, status, notes, creation time, booker name and
booker email equal to what the owner's bookings view returned for the link
before it was deleted.  [contract] *(fails today: no archive)*

AC-23. Given the AC-21 link with an accepted co-host and a pending invitee,
when the co-host, the invitee or another signed-in user reads their own
archive, then the link is not in it; and when any of them asks for the link or
its bookings through the archive by the link's id, then the answer is
identical to the one for an id that never existed.  [contract]

AC-24. Given a signed-in user who has never deleted a link, when they read their
archive, then it is empty and the answer is a success, not an error.  [contract]

AC-25. Given a signed-in owner on the Links page with a link that has one
booking, when they choose Delete, then the confirmation says the link and its
bookings move to the archive and that bookers' names and emails will be erased
90 days after the link's last booking; when they confirm, then the link leaves
the Links list, and the archive shows it with its booking, the booker's name
and email, and the erasure date; and the archive offers no control that edits,
resumes, restores or deletes the link.  [e2e] *(fails today)*

**Erasure (owner decision, 2026-10-27)**

AC-26. Given a deleted link whose bookings end at T1 < T2 (the later one
cancelled), when erasure runs at T2 + 90 days − 1 second, then nothing is
stored differently and no audit entry or log line about the link is written
(only the run's summary line); and T1 + 90 days having passed does not make
the link due.  [unit]

AC-27. Given the AC-26 link, when erasure runs at T2 + 90 days, then for both
bookings the booker's name and email are no longer stored anywhere in the
database, and each booking's start, end, status, notes, creation time and
calendar-event references, and the link's record, deletion time, host rows and
uses count, are exactly as before.  [unit]

AC-28. Given the state after AC-27, when erasure runs again at T2 + 90 days and
at T2 + 400 days, then no stored value changes and no audit entry is written.
Given two erasure runs started together against the same database with the
AC-26 link due, then afterwards both bookings are erased and exactly one audit
entry exists for that erasure.  [unit]

AC-29. Given a live link, a paused link and a link that was inactive before this
change (D-1), each with a booking that ended 400 days before the run, when
erasure runs, then none of those bookings changes and no audit entry is
written.  [unit]

AC-30. Given a link deleted at D whose only booking ended at E with
E + 90 days < D, when erasure runs at D + 1 second, then that booking's name
and email are erased.  [unit]

AC-31. Given an erasure of a deleted link with three bookings, then the owner's
activity log gains exactly one entry naming the link's title and id, the count
3, the last booking date used and the run time; and neither that entry, nor
the run's log output, nor any error report raised during the run contains any
of the three names or email addresses.  [unit]

AC-32. Given the state after AC-27, when the owner reads the archive and the
link's bookings in it, then the link shows when it was erased and each booking
shows its start, end, status, notes and creation time, with name and email in
an explicit erased state that is distinguishable from any value a booker can
submit (a booker cannot submit an empty name or email,
`handlers_booking.go:359-363`).  [contract]

AC-33. Given two due deleted links A and B where storing A's erasure fails,
when erasure runs, then B is erased with its audit entry; A is wholly
unchanged with no audit entry; the failure is logged and reported without
personal data; and when erasure runs again with the failure removed, then A
is erased. Given a run cancelled (by the shutdown drain's context) between
two links, then each link is either fully erased with its entry or untouched.
[unit]

AC-34. Given a deleted link whose bookings were erased at run R1, and a booking
inserted on it afterwards (simulating OQ-4's race) ending at T3, when erasure
runs at T3 + 90 days − 1 second, then the new booking is untouched; at
T3 + 90 days, then it is erased, the earlier bookings are unchanged, and a
second audit entry is written for this erasure.  [unit]

AC-35. Given the backend starts with a deleted link overdue for erasure, then
the erasure runs without any operator action shortly after start and the link
is erased; and given a clock advanced 24 hours from the last run, then another
run happens. The run is a tracked unit of the PAC-81 drain and does not start
once shutdown has begun.  [unit] (injectable clock and trigger)

AC-36. Given AC-26 and AC-27 run once on a deployment with at-rest encryption
configured and once with none configured, then both give the same result.
[unit]

AC-37. The operations runbook has a section covering every item listed under
"Runbook" in §3.5, and the operator can list due and overdue deleted links
without any booker's name or email in the output.  [review for the prose;
unit for the listing, which must contain no name or email]

**Alias precedence (API-049)**

AC-13. Given a create request carrying both spellings with different values for
durations, and both with different values for days, then the stored link has
the canonical spelling's durations and days.  [contract] *(passes today; pins
create)*

AC-14. Given an existing link, when an edit carries both spellings with different
values for durations, and both for days, then the link has the canonical
spelling's durations and days — the same result AC-13 gives for the same
values.  [contract] *(fails today: the alias wins)*

AC-14a. On both create and edit: given a request whose canonical durations
spelling is null and whose alias carries a non-empty list, then the alias's
durations are used; given a request whose canonical durations spelling is an
empty list and whose alias carries a non-empty list, then the request is
rejected exactly as create rejects an empty durations list today and, on edit,
the link is unchanged.  [contract] *(create passes today; edit fails today on
the empty-list case: the alias wins)*

**Counting uses (API-051)**

AC-15. Given a single-use link that has been booked once, when that booking is
cancelled, then the link's public info, slots and booking all still answer
"no longer accepting bookings", and the owner sees one use.  [contract]

AC-16. Given a link limited to 2 bookings with two bookings made, when one is
cancelled, then the link still answers "no longer accepting bookings" and the
owner sees two uses; when the owner raises the limit to 3, then it takes one
more booking.  [contract]

AC-17. Given a link limited to 2 with one cancelled booking and no other, then
the owner sees one use and the link takes exactly one more booking.  [contract]

AC-17a. Given a link limited to 1 whose only booking was made and then
cancelled before this change was deployed, when the change is deployed, then
the link answers "no longer accepting bookings" and the owner sees one use.
[contract] (data-migration test if the count becomes stored) *(fails today: the
link is open and shows zero uses)*

AC-18. Given the e2e stack with a single-use link that has been booked once,
when that booking's status is set to cancelled directly in the database, then
the public booking page still shows "link no longer available".  [e2e] The
link must be seeded for this test alone, not the shared exhausted link that
the existing used-up test depends on.

**Fetching one link (API-006, fixed by PAC-25)**

AC-19. Given an active link with an accepted co-host and a pending invitee, when
its owner or the accepted co-host fetches it by id, then it is returned with
its hosts; when the pending invitee or any other signed-in user fetches it,
then the answer is identical in status and body to the one for an id that
never existed.  [contract] *(passes today since PAC-25 —
`TestPAC25GetLink*`; pins it across this change)*

AC-20, AC-20a — retired in revision 6: they protected the API-073 removal,
which NEW-booking-rate-limit has shipped (§2.9).

## 5. Explicitly out of scope

- **Un-delete / restore.** The archive is read-only. A way back is a product
  decision and a new surface.
- **Retention of live and paused links' bookings.** The owner's decision
  covers deleted links. Bookers' details on links that are never deleted stay
  as today (OQ-9).
- **Erasing anything beyond bookers' names and emails.** Notes stay (D-11,
  OQ-8). The link record, host rows and the bookings themselves are never
  erased by this spec.
- **Copies outside Paceday's database.** Hosts' calendar events, emails
  already sent, and existing backups are not reached (§3.5). Purging backups
  early is not offered.
- **A booker asking for their own details to be erased.** No such request
  surface exists; adding one is a separate privacy feature.
- **Co-hosts' view of deleted links' bookings.** Only the owner has the
  archive (R-1).
- **A booking-cancel feature.** This spec decides what a cancellation does to
  the count; it does not add a way to cancel. ACs 15–18 create the cancelled
  state directly.
- **Requests in flight around a delete, and two simultaneous bookings on a
  single-use link** (§2.7, last bullet; OQ-4). Real, related, and not in the
  audit or the issue. The erasure rule nevertheless covers any booking that
  ends up on a deleted link (AC-34).
- **What a paused link's public page says.** It stays "Link not found" as
  today; the booker copy that mentions "paused" (`PublicBooking.tsx:397`) is
  reached only by "used up". Changing it is a frontend copy decision (OQ-3).
- **Accept / decline / leave on a paused link** — unchanged, PAC-30 D-5 stands
  for paused links.
- **Unbounded duration options and the "end must be after start" answer**
  (§2.9). OQ-6.
- **The bookable-hours window's two spellings** (§2.6, fourth bullet). On
  create an empty canonical window falls back to the alias; on edit it wins and
  is rejected. Neither the issue nor API-049 names the window, and every fix
  turns some request that succeeds today into an error or changes what it
  stores (D-10). OQ-7.
- **API-053** (`days` alias silently drops unrecognised day names), **API-069**
  (duplicate invite routes), **API-072** (ambiguous 404 on invite), **API-019**
  (bookings list shape) — same operations, different defects, not lifecycle.
- **The 403 a non-owner gets on a live link's edit, delete, bookings and
  invite.** Unchanged (`booking.yaml:880-881` records it as out of PAC-25's
  scope too); only deleted links change for those callers (AC-6a).

## 6. What must not change

- **Pause/resume from the Links page** — `Links.tsx:145-150`, `:352-355`. No
  frontend test toggles a link against the real API; no e2e test covers it.
  *Finding:* unprotected today; AC-11 adds the backend half.
- **PAC-25's fetch rule and check order** — `TestPAC25GetLink*`
  (`handlers_scheduling_links_test.go:683-828`), `pac25_contract_test.go`, and
  `e2e/tests/pac25-access.spec.ts`; contract `booking.yaml:869-893`. A storage
  error on the reader check or roster stays 500 (C-2). AC-19 pins the 404.
- **Owner-only edit, delete, bookings list and invite, with 403 for others on a
  live link** — `TestSchedulingLinksOwnerOnlyBookingsAndDelete`
  (`handlers_scheduling_links_test.go:213-268`) and its siblings.
- **The bookable-hours window aliases behave as today on both verbs**,
  including an empty canonical value beside an alias (201 on create, 422 on
  edit; §2.6, D-10). *Finding:* no test covers the empty-string case; the plan
  should pin today's answers.
- **Sending only an alias still works on both verbs** —
  `TestSchedulingLinksPatchUsesFrontendAliases`
  (`handlers_scheduling_links_test.go:128-157`) and
  `TestSchedulingLinksCreateAndListContract` (`:69-112`).
- **A booked single-use link answers 410 and the booker sees "link no longer
  available"** — `e2e/tests/public-booking.spec.ts:91-106`. The seed creates
  that state by inserting a booking and relies on the count being derived
  (`e2e/seed/seed.sql:270`, `:334-340`); if the counting changes how it is
  stored, the seed must still produce an exhausted link.
- **Public booking answers** — `TestPublicBookingErrorsContract` and its
  siblings (`handlers_booking_test.go:119-266`). This spec changes only the
  not-found case for deleted links (AC-4) and the count rule (AC-15–17a).
- **Bookings make hosts busy whether or not a calendar event was written** —
  `TestGetBusyBookingsForUser_CountsHostedLinksWithoutCalendarEvents`
  (`backend/storage/booking_busy_test.go:25`). AC-7 extends it to deleted and
  erased links.
- **PAC-30's 404 rules for accept/decline/leave without a host row** —
  `TestSchedulingLinksRespondAndLeaveWithoutHostRowIs404` and siblings
  (`handlers_scheduling_links_test.go:431-581`).
- **A repeated delete changes from 204 to not found** — intended (AC-6).
  `TestSchedulingLinksOwnerOnlyBookingsAndDelete` ends with the owner's 204
  (`handlers_scheduling_links_test.go:265-267`) and still passes; the web app
  confirms before deleting (`Links.tsx:376-379`).
- **PAC-71's account-data inventory** — deleted links and their bookings,
  erased or not, still count as account data (`emptiness.go:39-41`). No test
  pins deleted or erased rows there today; the plan should add one.
- **Booking confirmation logs carry no address** — `engine/booking.go:683-686`
  (`outbound.RedactAddresses`). The erasure's logging must hold to the same
  rule (AC-31).
- **PAC-84's rule that no migration copies a secret in SQL**
  (`migrations/034_encryption_at_rest.up.sql:6-10`). Erasure touches no
  inventory column.
- **The e2e cleanup hard-deletes link rows** (§2.4). That is test teardown,
  not a product path, and is no precedent against D-2.
- **Slug uniqueness across all links** — `TestSlugExists`
  (`backend/storage/scheduling_links_test.go:256`).

## 7. Rollback

Reverting the code restores today's behaviour for links: deleted links reappear
in lists and become fetchable and resumable again (as paused), the archive
disappears, aliases win on edit, cancelled bookings give their use back, and
the erasure stops running.

**Erasure cannot be rolled back, by design.** Names and emails erased before
the revert stay erased; the only copies are in backups taken before each
erasure, kept for the backup retention, and restoring them to undo an erasure
would defeat the owner's decision. The runbook says so.

If the implementation stores anything new — a deletion marker or time, an
erased state on bookings, or a stored use count — its down migration loses
that information:

- **Deletion marker and time:** after the down migration every deleted link is
  indistinguishable from a paused one and reappears in its owner's list, as it
  does today. No booking, link or host row is lost.
- **Erased state:** after the down migration an erased booking must still be a
  valid row for today's code, which requires a non-null name and email
  (`migrations/009_scheduling_links.up.sql:29-30`) and shows both to the owner
  (`LinkBookingsDialog.tsx:81`). The plan must state what an erased booking
  looks like to the reverted code and show that the down migration cannot fail
  on erased rows and cannot bring back any erased value.
- **Stored use count:** after the down migration the count is derived again from
  non-cancelled bookings, so any link whose bookings were cancelled in the
  meantime re-opens. No booking is lost.

The plan must state which of these it introduces and confirm the down migration
matches this section.

## 8. Open questions

- **OQ-1 — Can the owner see bookings of a deleted link?** *Resolved by the
  owner, 2026-10-27:* yes, in an archive (§0, §3.2). Replaces revisions 1–5's
  D-4 answer ("not found").
- **OQ-2 — Should deleted links' data ever be purged?** *Resolved by the
  owner, 2026-10-27:* bookers' names and emails are erased 90 days after the
  link's last booking date (§0, §3.5). The link, its host rows and its
  bookings otherwise stay (D-2).
- **OQ-3 — Should a paused link's public page say "paused" rather than "not
  found"?** Unchanged from revision 5. *Owner:* product owner with
  frontend-implementer. *Resolve:* a separate copy/contract issue if wanted.
- **OQ-4 — Requests in flight.** Two simultaneous requests can both pass the
  single-use check (§2.7); a booking that loaded the link before a delete can
  be written after it; an edit that loaded the link before a delete can write
  it back live. *Owner:* triage, to file one follow-up issue covering the
  three. The erasure covers the second case's data (AC-34).
- **OQ-5 — Does the refusal in AC-19 say "forbidden" or "not found"?**
  *Resolved on main by PAC-25 (`69ffa92`):* not found, identical to an unknown
  id (§2.8). PAC-25 records that as its D-1, pending the owner's confirmation
  under PAC-25 OQ-6/OQ-7; if the owner overturns it there, AC-19 follows.
  The PAC-40 contract's C-1 (403) is withdrawn.
- **OQ-6 — Should duration options have a maximum?** Today any positive
  integer is accepted, and options of 153,722,868 to 307,445,734 minutes make
  the computed end wrap to before the start (§2.9). *Owner:* triage, to file a
  follow-up issue that also pins today's "end must be after start" answer.
- **OQ-7 — Should an empty canonical window beside an alias mean the same on
  create and edit?** Unchanged. *Owner:* triage.
- **OQ-8 — Are booking notes erased with names and emails?** The owner named
  names and emails only, so this spec keeps notes (D-11). Notes are free text
  a booker types and can contain a name, phone number or address. *Owner:*
  product owner. *Resolve:* a one-line answer in chat or Linear; if "yes",
  notes join the erased set in §3.5, AC-27 and AC-32 and nothing else changes.
  Not blocking: either answer is buildable and the literal reading is the
  default.
- **OQ-9 — Is there a retention rule for bookings on links that are never
  deleted?** None is decided (§5). *Owner:* product owner. *Resolve:* a
  separate issue if wanted.
- **OQ-10 — Is "the link's last booking date" the latest meeting end?** This
  spec reads it so (D-12), counting cancelled bookings. The alternative —
  when the last booking was made — would erase the email of a booker whose
  meeting is still ahead. *Owner:* product owner, only if this reading is
  wrong. Not blocking.

## Decisions

Decisions marked **owner, 2026-10-27** come from the owner's recorded answer
(§0). All others are **assumed by the factory, pending owner confirmation**.
Only the owner-decided erasure deletes data; no decision widens who can see
data.

- **D-1 — Existing inactive links are paused, not deleted.** The server cannot
  tell which inactive links were deleted and which were paused, because both
  wrote the same flag. Treating them as deleted would make every paused link
  vanish from the list and — under §3.5 — eventually lose its bookers' details,
  with no way to resume it; treating them as paused leaves them exactly as
  visible as today. The cost is that links deleted before the change must be
  deleted again before they reach the archive or the erasure.
- **D-2 — Delete stays a soft delete; erasure is the one removal (owner,
  2026-10-27, for the erasure).** The link record, bookings and host rows are
  kept. Bookers' names and emails on a deleted link are erased by §3.5's rule.
  Revisions 1–5 kept everything indefinitely; the owner's answer replaces that.
- **D-3 — A deleted link's slug stays reserved**, before and after erasure.
  Releasing it would let a new link — possibly another user's — receive
  bookings from people holding the old URL. Unchanged from today.
- **D-4 — "Not found" covers every path that names a deleted link by id or
  slug, except the owner's archive (owner, 2026-10-27, for the archive).** That
  includes the co-host invite list and accept/decline/leave. For paused links
  PAC-30 D-5 is unchanged. Busy-time computation is not a "path that names the
  link" and keeps counting its bookings (AC-7). Revisions 1–5 also made the
  owner's bookings read not found; the archive replaces that.
- **D-5 — The canonical spelling wins on both verbs**, for durations and days.
  It is what create does today and the only spelling the web app sends, so no
  Paceday client changes behaviour. Rejecting a request that carries both with
  different values would turn requests that succeed today into errors.
- **D-6 — A use is spent when a booking is made, and cancelling does not return
  it.** "Single use" and "limit N" bound how many meetings a link can put on the
  owner's calendar; re-opening on cancel lets anyone still holding the URL book
  again without the owner deciding so. The owner can always raise the limit.
- **D-7 — Fetching one link is limited to the owner and accepted co-hosts**,
  everyone else getting the unknown-id answer. Shipped by PAC-25; kept.
- **D-8 — Bookings that existed before the change count under D-6**, cancelled
  ones included. It can close a link that is open today, but only one whose
  bookings were cancelled by a database edit (§2.7), and closing never widens
  who can book.
- **D-9 — A deleted link answers every caller as an id that never existed**,
  including callers with no role on it (AC-6a), with the owner's archive the
  only exception. Answering a stranger with the refusal a live link gives would
  tell them the id once existed.
- **D-10 — The window aliases are left as they are.** §3.3's rule is limited to
  durations and days (§5, OQ-7).
- **D-11 — Notes are kept at erasure.** The owner named names and emails;
  erasing notes would remove from the archive something the owner did not ask
  to lose, and keeping them is reversible later while erasing is not. Open to
  the owner (OQ-8).
- **D-12 — "Last booking date" is the latest meeting end of any booking on the
  link, cancelled ones included.** A meeting's date is when it takes place;
  using its end means no booker's details are erased while their meeting is
  still ahead or under way. Counting cancelled bookings keeps the rule a
  function of what is stored, with no status exceptions, and only ever delays
  an erasure. Open to the owner (OQ-10).
- **D-13 — The erasure runs at start-up and at least every 24 hours.** The
  owner gave a day count, so a lateness of at most one day while the backend
  runs keeps "90 days" true to the day; running at start-up bounds the
  lateness after downtime and re-erases restored data at once (§3.5).

## Challenge — 2026-10-27

Reviewed the committed spec at `d69a457` against source on `main` at
`6d751f9`, the Linear issue text, `docs/factory/api-audit.md`,
`contracts/openapi/paths/booking.yaml` and `contracts/features/PAC-30.md`.
Nothing was run; every finding below is from reading code.

**Verdict**: accept-with-changes

The scope matches the issue (API-049, -050, -051, -073), the API-006 pull-in is
justified by touch-it-fix-it on `getSchedulingLink`, and the decisions are
conservative (no data deleted, no visibility widened: D-7 only narrows who can
fetch a link, and accepted co-hosts already receive the same host details
through the list). Nearly every citation in §2 checks out (list below). Two
statements are contradicted by the code; both are small fixes.

### Blocking
1. **AC-20 lists a rejection that cannot be produced today, so its regression
   test cannot be written.** "end not after start" (`handlers_booking.go:285-288`)
   is unreachable: when `end` is sent, `actualDuration = int(end-start minutes)`
   and any `end <= start` gives a duration `<= 0`, which `durationAllowed`
   rejects first with "invalid duration" (`handlers_booking.go:125-127`, checked
   at `:277`); when `end` is omitted, `end = start + requestedDuration` and
   `requestedDuration` has already passed `durationAllowed`, so it is positive.
   This is a third dead branch in the same handler, of the same kind API-073
   names, and §2.5 / §3.5 do not mention it. *Resolve:* remove "end not after
   start" from AC-20's list of reachable rejections, and state in §2.5 that
   `:285-288` is also unreachable, saying whether it is removed under API-073 or
   left (either is consistent with §3.5, which requires identical answers).
2. **§1 says the use-count re-opening "already happens to any booking cancelled
   outside the app"; the code contradicts it.** Nothing in `backend/`, `mcp/` or
   the web app writes booking status (no `UPDATE bookings`, no write of
   `'cancelled'`; the only `status <> 'cancelled'` uses are reads at
   `storage/scheduling_links.go:87,101,113,126,151,392`). Cancelling the calendar
   event a booking created does not touch the `bookings` row, so the link does
   not re-open. §2.4 states this correctly ("a cancelled booking exists only if
   someone edits the database"), so the spec contradicts itself. *Resolve:* drop
   the clause from §1 so the problem statement matches §2.4; D-6 stands on the
   issue's own framing and does not need it.

### Non-blocking
1. **"Not found for anyone" (§3.1) is only tested for the owner.** AC-3 and AC-6
   cover the owner editing, deleting, listing bookings and inviting on a deleted
   link. Today a non-owner gets 403 on those paths
   (`handlers_scheduling_links.go:426`, `:543`, `:571`, `:604`), so an
   implementation that filters deleted links only after the owner check would
   still answer 403 to a stranger and so reveal that the id exists. §3.1 forbids
   this, but no AC would catch it. Add a non-owner case to AC-3/AC-6, and to
   AC-2/AC-19 for fetch (a non-host fetching a deleted link gets not found, not
   the AC-19 refusal). This interacts with OQ-5: if the contract picks 403 for
   AC-19, deleted vs live becomes distinguishable to strangers unless AC-2
   explicitly wins.
2. **Criteria that pass today are not labelled.** AC-13 and AC-20 say so; AC-7,
   AC-8, AC-9, AC-11 and AC-12 also pass on current code (busy time ignores
   `active`, `SlugExists` ignores `active`, delete only flips a flag, pause and
   resume already work, inactive links are already listed). They are valid
   pins, and they only become falsifiable once a deleted marker exists. Label
   them "(passes today; pins …)" so the plan does not expect them red.
3. **"Carries both spellings" is undefined for `null` and `[]`.** Create treats
   `"duration_options": null` as absent and uses the alias; `[]` counts as
   present and fails validation (`handlers_scheduling_links.go:160`, `:217`).
   Edit behaves the same way on the `!= nil` test. AC-13/14 only use two
   non-empty values. State that "present" means a non-null value, so both verbs
   are pinned on the same rule.
4. **Delete racing a booking.** A public booking that has already loaded the
   link by slug (`handlers_booking.go:212`) can insert after the owner's delete
   commits, which gives a booking on a deleted link. AC-4 says "no booking is
   created" and does not say whether that covers requests in flight. This
   is in the same class as OQ-4. Either scope AC-4 to requests started after the
   delete, or include it in the OQ-4 follow-up.
5. **OQ-4 judgement (asked of this role):** keep it out of scope. The race
   predates this spec, the issue does not name it, and D-6 does not widen it
   (the count read is the same read). Triage should file the follow-up, covering
   item 4 above as well.
6. **AC-18 must not use the shared seed's exhausted link.** Cancelling that
   link's booking in the database would race `public-booking.spec.ts:79-97`,
   which needs it exhausted. The criterion is written generically, but the plan
   needs its own seeded link (the seed comment at `seed.sql:219-223` also has
   to change if counting changes).
7. **AC-6 "deletes it again → not found" turns DELETE from 204 into 404 on a
   repeat.** No current consumer repeats it (the web confirms first,
   `Links.tsx:373-375`). This is intended. It should be named in §6 next to the
   owner-only test, which today ends with an owner 204
   (`handlers_scheduling_links_test.go:261-267`).

### Criteria I could not falsify
- AC-20 "end not after start" sub-case: no request reaches it (Blocking 1).

All other criteria have a test that is red today, or that pins current
behaviour as the spec says (see Non-blocking 2).

### Citations I checked and found wrong
- §1 "already happens to any booking cancelled outside the app": no code path
  sets a booking to `cancelled` (Blocking 2).
- §5 "the Links page already removes a deleted link from its list
  optimistically and refetches": it does not do so optimistically. The delete
  mutation only invalidates and refetches on success
  (`smart-calendar-flow/src/pages/Links.tsx:330-337`). The only removal is in the
  offline mock (`src/api/schedulingLinks.ts:601-603`). The conclusion still holds
  (no web change is needed, because the refetch will agree once the server
  filters), but the stated mechanism is wrong.

Checked and correct: `storage/scheduling_links.go:87-163, 300-318, 426-452,
478-483`; `handlers_scheduling_links.go:73-76, 92-95, 157-188, 370-408,
426, 444-465, 481-483, 527-553, 562, 571, 595, 604, 635-656, 695`;
`handlers_booking.go:65-73, 107, 116, 145, 154, 212, 221, 281-284, 293-296`;
`migrations/009_scheduling_links.up.sql:12, 33`; `booking.yaml:520-522, 539,
615-618, 1473-1474, 1677-1679`; `api-audit.md:476, 549-551`;
`PAC-30.md:124-129`; `PAC-59.md:6-12, 350-358`; `seed.sql:219-223`;
`public-booking.spec.ts:79-97`; `PublicBooking.tsx:197`;
`schedulingLinks.ts:118-131, 514, 544, 574`; the six test names in §6; and that
`mcp/` has no scheduling-link caller.

### Consumers this breaks
- clockwise-like/backend/api/handlers_scheduling_links_test.go:213-268
  (`TestSchedulingLinksOwnerOnlyBookingsAndDelete`): still passes, but anything
  added after its final owner DELETE that re-reads the link would change from
  200 to 404. This is intended.
- clockwise-like/e2e/seed/seed.sql:219-223 and its comment: it relies on the
  derived count. It keeps working under D-6 only if a non-cancelled seeded
  booking still counts, which it does. The comment will be stale if the count
  becomes stored.
- smart-calendar-flow/src/api/schedulingLinks.ts:544 (`updateLink` GETs the link
  before PATCHing it): a stale tab that toggles or edits a link deleted
  elsewhere now gets 404 on that GET instead of 200, and the web shows the
  error toast (`Links.tsx:355`). This is acceptable and consistent with §3.1, but
  it is a user-visible change that §5's "no web changes" should acknowledge.
- No consumer in `mcp/` (verified by grep).

## Revision — 2026-10-27 (answers challenge round 1)

Both blocking findings were checked against source at `6d751f9` and accepted.
No decision changed. The non-blocking findings are not addressed in this round.

1. **Blocking 1, AC-20 lists an unreachable rejection.** Confirmed:
   `durationAllowed` returns false for any duration of 0 or less
   (`handlers_booking.go:125-127`) and runs at `:277`, before the "end must be
   after start" check at `:285-288`. With an end sent, the duration is the whole
   minutes from start to end (`:262`), so an end at or before start is rejected
   as "invalid duration". Without one, the end is start plus a duration that has
   already passed that check. *Changed:* "end not after start" is removed from
   AC-20's list, and AC-20 now says such a request falls under "invalid
   duration". §2.5 names `:285-288` as a third unreachable branch, and says it is
   removed under API-073 together with the other two. §3.5 now names both
   unreachable checks and says a request with its end at or before its start
   keeps today's "invalid duration" answer.
   *[Superseded by Revision 2 item 1: the check is reachable.]*
2. **Blocking 2, §1 contradicts §2.4 about cancellations outside the app.**
   Confirmed: no code in `backend/`, `mcp/` or the web app writes a booking's
   status. The only `'cancelled'` uses on bookings are reads
   (`storage/scheduling_links.go:87, 101, 113, 126, 151, 392`) and the column
   check (`migrations/009_scheduling_links.up.sql:33`). *Changed:* §1 no longer
   says the re-opening already happens. It now says nothing in Paceday cancels a
   booking yet, so the defect is latent, which matches §2.4. D-6 is unchanged.

Status moved from draft to challenged. The spec goes back to the challenger for
round 2.

## Challenge — 2026-10-27 (round 2)

Reviewed the committed spec at `e1f5335` (revision 1) against source on `main`
at `6d751f9` (still the tip of `origin/main`), the Linear issue text,
`docs/factory/api-audit.md`, `contracts/openapi/paths/booking.yaml` and the web
repo at `origin/main`. One probe was run (below); everything else is from
reading code.

**Verdict**: accept-with-changes

The revision fixed round 1's Blocking 2 correctly. It also applied round 1's
Blocking 1, but that finding was wrong, and the spec now contains a false
statement that permits a behaviour change. That is the only blocking item.
The scope still matches the issue, and the decisions are still conservative.

### Blocking
1. **"End must be after start" can be reached, so §2.5 (third bullet), §3.5 and
   AC-20 are contradicted by the code. As written, the spec permits removing a
   check that real requests reach.** Link validation only requires each
   duration option to be positive (`handlers_scheduling_links.go:219-223`). The
   contract sets no maximum (`booking.yaml` duration items: `type: integer`, at
   most `minimum: 1`), and the column is `INTEGER[]`
   (`009_scheduling_links.up.sql:6`). When a booking request sends no end, the
   handler computes `end = start.Add(time.Duration(requestedDuration) *
   time.Minute)` (`handlers_booking.go:275`). For any option of 153,722,868
   minutes or more, that multiplication overflows int64 nanoseconds and wraps,
   so `end` comes out before `start`. `durationAllowed` still passes, because
   the option is in the list, and the request reaches `:285-288`. Round 1
   checked only the end-sent path, where `end.Sub(start)` saturates and the
   "invalid duration" argument holds.
   *Probe, run 2026-10-27 against a throwaway Postgres 16 with a temporary
   test file that was then deleted (not committed).* A reusable link with
   `duration_options: [200000000]`, min notice 0, and `validateSchedulingLink`
   returns nil for it. `POST /api/book/{slug}` with
   `{"name":"A","email":"a@example.com","start":<future>}` returned:
   ```
   zz_pac40_probe_test.go:29: status=400 body={"error":"end must be after start"}
   --- PASS: TestPAC40ProbeEndAfterStartReachable (0.02s)
   ```
   A standalone Go check gives the same arithmetic: 153722867 min → end in
   2322, after start; 153722868 min → end in 1737, `After(start)=false`.
   If the branch is removed as §3.5 now directs, this request stops getting
   that 400 and continues into the notice, overlap and confirm steps. That
   breaks §3.5's own rule ("rejects exactly the same requests with exactly the
   same answers"), and AC-20's claim that an end at or before start gets
   "invalid duration" is false for this request.
   *Resolve:* restore "end not after start" as a reachable rejection. Remove
   the third bullet's unreachability claim from §2.5. In §3.5, name only the
   two unreachable items the issue names: the repeated end-time error at
   `:281-284` and the duplicated exhaustion test at `:293-296`. Add to AC-20 a
   case that reaches the check through an overflowing duration option, with
   today's status and body. Whether such duration options should be accepted
   at all is a separate defect (Non-blocking 3), not part of API-073.

### Non-blocking
1. **Round 1's non-blocking items 1–7 are still open.** The revision says so
   explicitly, and that is acceptable. Item 1 is the one most likely to matter
   at contract stage: no AC checks that a *non-owner* gets "not found" rather
   than 403 on a deleted link's edit, delete, bookings, invite or fetch.
   Combined with OQ-5, a stranger could tell a deleted id from a live one. The
   contract author needs that settled before choosing AC-19's status.
2. **An edit in flight can undo a delete.** Edit is a read-modify-write: the
   link is loaded by id (`handlers_scheduling_links.go:417`) and every column,
   including `active`, is written back from that copy
   (`storage/scheduling_links.go:140-157`). If an owner's edit loads the link
   just before a delete commits in another tab, its write lands afterwards and
   can make the link live again. §3.1 says flatly that "resuming cannot bring
   one back", and AC-3 covers only the sequential case. Either state that the
   guarantee covers requests started after the delete, as with round 1's
   item 4, or require that a delete is never undone by a concurrent edit. In
   both cases, add it to the OQ-4 follow-up.
3. **Unbounded duration options are a defect not in the audit.** They are the
   root of Blocking 1. A link can be created whose only duration lasts
   centuries, and the slot and booking paths then do overflowing arithmetic.
   This is out of this spec's scope. Triage should file it alongside OQ-4.
4. **Starting uses for existing links are unspecified.** If uses become a
   stored figure (§7 allows that), the spec does not say whether existing
   cancelled bookings count. Under D-6 they would count, and that could close
   a link that is open today, at deploy time and without the owner acting.
   Under today's derivation they would not. Cancelled rows can exist today only
   through database edits (§2.4), so the impact is small. AC-17 pins the
   behaviour after the change but not the first value. State which rule
   applies to bookings that existed before the change.

### Criteria I could not falsify
- None. AC-20's "end at or before start → invalid duration" can be falsified,
  and the probe above falsifies it (Blocking 1).

### Citations I checked and found wrong
- §2.5 third bullet and Revision 1 item 1 say "The 'end must be after start'
  check at `:285-288` cannot fire either". It fires for an overflowing duration
  option with no end sent (`handlers_booking.go:269-275`, probe output above).
  The same error appears in round 1's Blocking 1, which this round withdraws.
- §3.5 says "without the two unreachable checks (the repeated end-time error
  and 'end must be after start')". Only the first of the two is unreachable.

Checked and correct this round: `handlers_booking.go:65-73, 107-121, 124-136,
139-141, 211-304` (the `:281-284` re-check is unreachable: every assignment to
`err` before it returns on error; the `:293-296` re-check reads the same
in-memory link as `:221`); `handlers_scheduling_links.go:157-175, 212-260,
444-449`; `storage/scheduling_links.go:87-163, 426-452, 478-483`;
`009_scheduling_links.up.sql:6, 12, 33`; the issue text in Linear (scope matches
API-049/050/051/073); no SQL on `scheduling_links`/`bookings` outside
`storage/scheduling_links.go` and generated code; no scheduling-link caller in
`mcp/`; §1 now agrees with §2.4.

### Consumers this breaks
- Round 1's list stands. Checked again: in the web repo, `withFallback`
  (`smart-calendar-flow/src/api/client.ts:548-561`) rethrows HTTP errors and
  falls back to mock data only when the API is unreachable. A 404 for a deleted
  link therefore surfaces as an error and does not show stale mock data.
- smart-calendar-flow/src/api/schedulingLinks.ts:352-355 (`isLinkExhausted`)
  recomputes "used up" on the client from `uses_count`. It stays consistent
  under D-6 because it reads the same figure the server will gate on. Not a
  break; listed so the contract stage keeps the field's meaning and its name
  aligned.

## Revision — 2026-10-27 (answers challenge round 2)

The single blocking finding was checked against source at `6d751f9` and
accepted. No decision changed. Non-blocking items 1, 2 and 4 of round 2 and
round 1's non-blocking items stay open for the next stage; item 3 is now
recorded (§5, OQ-6).

1. **Blocking 1, "end must be after start" is reachable.** Confirmed by reading
   source: `validateSchedulingLink` requires only that each duration option is
   positive (`handlers_scheduling_links.go:219-223`), and when no end is sent
   the handler computes `end = start.Add(time.Duration(requestedDuration) *
   time.Minute)` (`handlers_booking.go:275`), which overflows for large options.
   I also ran a standalone Go check of that expression (not the handler, no
   database) from a start of 2026-11-02T10:00Z: 153,722,867 min gives
   2319-02-12, `After(start)=true`; 153,722,868 min gives 1734-07-24,
   `After(start)=false`; 200,000,000 min gives 1822-07-20, `After(start)=false`.
   I did not re-run the challenger's HTTP probe; the 400 body cited in AC-20a is
   the challenger's recorded output. Round 1's Blocking 1, and Revision 1
   item 1 which applied it, were wrong; Revision 1 item 1 is marked superseded.
   *Changed:*
   - §2.5 third bullet now says the check is reachable, explains the overflow
     path, and keeps it out of API-073.
   - §3.5 names only the two unreachable items: the repeated end-time error
     (`:281-284`) and the duplicated exhaustion test (`:293-296`). It states
     that the overflowing-duration request keeps "end must be after start".
   - AC-20 lists "end not after start" again as a reachable rejection, and
     restricts "end at or before start → invalid duration" to requests that
     send an end.
   - New AC-20a pins the overflow case: a link whose only option is 200,000,000
     minutes, booked with no end, answers 400 `{"error":"end must be after
     start"}` before and after the change.
   - §5 lists unbounded duration options as out of scope, and §8 adds OQ-6 for
     triage to file it with OQ-4. §6 notes AC-20a as new coverage.

Status stays challenged. The spec goes back to the challenger for round 3.

## Challenge — 2026-10-27 (round 3)

I reviewed the committed spec at `7f19704` (revision 2). I checked it against
source on `main` at `6d751f9`, which is still the tip of `origin/main`, the
web repo at `origin/main` `ad52160`, and the Linear issue text. I ran two
probes and one arithmetic check. Each used a temporary test file that I deleted
afterwards, against a throwaway Postgres 16 container that I then removed.
Nothing was committed apart from this section.

**Verdict**: accept-with-changes

Revision 2 fixed round 2's blocking item correctly, and I confirmed that fix
independently (probe 1 below). One sentence that revision 1 introduced is still
contradicted by the code, and AC-20 inherits it. That is the only blocking
item. The scope matches the issue. The decisions are still conservative: none
deletes data, and none widens who can see data.

### Blocking
1. **§3.5 and AC-20 say that a request whose end is at or before its start
   gets "invalid duration". The code contradicts this when the request also
   sends a non-zero `duration` or `duration_minutes`.** When an end is sent,
   the mismatch check at `handlers_booking.go:262-266`
   (`requestedDuration != 0 && requestedDuration != actualDuration`) runs
   before `durationAllowed` at `:277`. A request with an end of start−30 min
   and `duration_minutes: 30` is therefore answered "duration does not match
   end time", not "invalid duration" (probe 2). Today's behaviour is fine: the
   answer stays the same before and after the change. The spec's description of
   that behaviour is wrong, though, and an AC-20 case written from §3.5's
   sentence would fail on current code. That is the same failure mode as
   rounds 1 and 2.
   *Resolve:* in §3.5 and AC-20, limit "end at or before start → invalid
   duration" to requests that send no duration or a duration of 0. State that
   when such a request also sends a non-zero duration, the answer is "duration
   does not match end time".

### Non-blocking
1. **Open items carried forward.** Round 1 items 1, 3, 4, 6 and 7 and round 2
   items 2 and 4 are still open, and the revisions say so. Two of them are
   still worth settling before the contract stage:
   - Round 1 item 1 (non-owner on a deleted link). §3.1 already decides it: a
     non-owner gets the not-found answer it would get for an id that never
     existed, not 403. No AC tests it, though, and OQ-5 depends on it.
   - Round 2 item 4 (the starting value if uses become a stored count). §3.3's
     definition ("counts bookings made, including cancelled ones") already
     answers it: bookings that existed before the change count. The spec should
     say that explicitly, and the plan should not pick another rule.
2. **AC-20's list of rejections leaves out "invalid request body"**
   (`handlers_booking.go:234-237`, malformed JSON → 400). The list is presented
   as "each rejection the public booking create can make today". Add it, or say
   the list is not exhaustive.
3. **e2e cleanup hard-deletes links.** `e2e/tests/scheduling-links.spec.ts:110`
   and `:188` delete rows from `scheduling_links` directly. This is test-only
   and not a product path, and D-2 does not apply to it. I list it so that the
   plan does not take it as a precedent for a hard delete.

### Criteria I could not falsify
- None. AC-20 is a regression net and AC-20a is a pin, and both are labelled as
  such. Every other AC has a test that is red today, or one that pins current
  behaviour (round 1, non-blocking item 2).

### Citations I checked and found wrong
- §3.5, "A request that sends an end at or before its start keeps getting the
  answer it gets today, 'invalid duration'", and the matching clause in AC-20.
  This holds only when no non-zero duration is sent (Blocking 1, probe 2).

Checked and correct this round:
- `handlers_booking.go:65-73, 107-136, 211-304`. `:281-284` is unreachable.
  `:293-296` repeats `:221` against the same in-memory link. `:285-288` is
  reachable through overflow.
- `handlers_scheduling_links.go:212-260` (no upper bound on durations), and
  `:426, :543, :571, :604` (403 for non-owners).
- `booking.yaml`: the only `maximum` values are on days (`:1549`, `:1634`), so
  durations have no maximum.
- `TestPublicBookingErrorsContract` (`handlers_booking_test.go:178-213`) has
  five cases. No backend test sends 404, 409, 410, the end/duration mismatch or
  "end must be after start" to the booking create, so §6's "the others have no
  test today" holds.
- Web: `Links.tsx:146, 330-336, 349-355, 375`; `schedulingLinks.ts:118-131, 514,
  544, 574`; `PublicBooking.tsx:193-199` (404 renders "Link not found", 410
  renders the "used up or paused" copy); `client.ts:548-561`.
- `mcp/` has no caller of `/api/scheduling-links` or `/api/book`.
- No other backend code calls the link lookups outside
  `handlers_scheduling_links.go` and `handlers_booking.go`, except
  `engine/booking.go:161`, which is busy time (AC-7).

**Probe 1 (AC-20a, re-run independently).** A link with
`DurationOptions: [200000000]` and min notice 0, for which
`validateSchedulingLink` returns nil, was booked with no end:
```
zz_pac40_r3_probe_test.go:19: validate=<nil>
zz_pac40_r3_probe_test.go:24: status=400 resp={"error":"end must be after start"}
```
Arithmetic from 2026-11-02T10:00Z: 153722867 min → 2319-02-12 (`After`=true);
153722868 min → 1734-07-24 (false); 200000000 min → 1822-07-20 (false). This
matches revision 2.

**Probe 2 (Blocking 1).** A reusable link with options [30, 60], start
2026-10-30T10:00Z:
```
end 09:30, no duration            -> 400 {"error":"invalid duration"}
end 09:30, duration_minutes 30    -> 400 {"error":"duration does not match end time"}
end 10:00, duration_minutes 0     -> 400 {"error":"invalid duration"}
end 10:00, duration 30            -> 400 {"error":"duration does not match end time"}
```

### Consumers this breaks
- The lists from rounds 1 and 2 still stand, and I found nothing new. When the
  web app toggles pause, it sends a full-body PATCH that uses only the canonical
  spellings (`schedulingLinks.ts:544-559`, body built at `:118-131`), so D-5
  does not change it. The response normaliser reads `durations ?? duration_options`
  (`:98`), but that applies to response fields only, which this spec does not
  change.

## Revision — 2026-10-27 (answers challenge round 3)

The single blocking finding was checked against source at `6d751f9` (still the
tip of `origin/main`) and accepted. No decision changed. Non-blocking item 2 of
round 3 is applied; non-blocking items 1 and 3, and the earlier open items,
stay open for the next stage.

1. **Blocking 1, "end at or before start → invalid duration" is only true
   without a duration.** Confirmed by reading source: when an end is sent, the
   handler computes the actual duration from start and end and, if the request
   carries a non-zero `duration` or `duration_minutes` that differs from it,
   answers 400 "duration does not match end time"
   (`handlers_booking.go:263-266`). `durationAllowed` (`:277-280`, which rejects
   any duration `<= 0` at `:125-127`) only runs after that. So an end at or
   before the start gets "invalid duration" only when no duration or a duration
   of 0 is sent (or, from reading source only, a negative duration exactly equal
   to end minus start, which passes the mismatch check and then fails
   `durationAllowed`). I did not re-run the challenger's probe 2; the four
   answers it recorded agree with this reading.
   *Changed:*
   - §3.5 limits "invalid duration" to an end at or before the start sent with
     no duration or a duration of 0, and names "duration does not match end
     time" for the same request with a non-zero, differing duration.
   - AC-20 lists both sub-cases with their exact 400 answers.
2. **Non-blocking 2, "invalid request body" missing from AC-20.** Confirmed
   (`handlers_booking.go:234-237`). Added to AC-20's list.

Status stays challenged. The spec goes back to the challenger for round 4.

## Challenge — 2026-10-27 (round 4)

I reviewed the committed spec at `1b05a05` (revision 3) against source on
`main` at `6d751f9`, which is still the tip of `origin/main`. I ran one probe:
a temporary test file, since deleted, against a throwaway Postgres 16
container, since removed. Nothing was committed apart from this section.

**Verdict**: accept-with-changes

Revision 3 fixed §3.5 correctly. AC-20 was rewritten in the same revision but
does not say what §3.5 says, and one of its sub-cases is contradicted by the
code. That is the only blocking item. The scope and decisions are unchanged from
round 3, and they remain conservative.

### Blocking
1. **AC-20 says that an end at or before start "together with a non-zero
   duration" is answered "duration does not match end time". That is false
   when the duration equals end minus start, which makes it negative.** The
   mismatch check (`handlers_booking.go:263-266`) fires only when the requested
   duration *differs* from `actualDuration`. A request with an end of start−30
   min and `duration: -30` (or `duration_minutes: -30`) passes that check. It
   then fails `durationAllowed` (`:125-127`, `:277-280`) and gets
   "invalid duration" (probe below). Revision 3 item 1 notes this case "from
   reading source only", and §3.5 handles it correctly ("a non-zero duration
   that differs from the time between start and end"). AC-20 drops "that
   differs", so an AC-20 case written from its text fails on current code. That
   is the failure mode of rounds 1–3.
   *Resolve:* in AC-20, change "together with a non-zero duration" to match
   §3.5 ("a non-zero duration that differs from the time between start and
   end"). State that an end at or before start whose duration equals end minus
   start is answered 400 "invalid duration", or say that AC-20's sub-cases are
   examples and not a partition.

### Non-blocking
1. **"Sends no duration (or a duration of 0)" does not say which field.**
   `duration` overrides `duration_minutes` whenever it is present, even when it
   is 0 (`handlers_booking.go:249-255`). A request with `duration_minutes: 30`,
   `duration: 0` and an end at or before start does send a non-zero duration,
   yet it is answered "invalid duration", not "duration does not match end
   time". This is from reading source, not the probe. When fixing Blocking 1,
   define "the duration" as `duration` if present, else `duration_minutes`, in
   §3.5 and AC-20.
2. **Open items carried forward.** These are unchanged from round 3's
   non-blocking item 1: a non-owner on a deleted link gets not-found (no AC),
   and the starting value if uses become stored. Round 1 items 3, 4, 6 and 7
   and round 2 item 2 also remain open. None blocks the contract stage, but
   the first one must be settled before OQ-5 is.

### Criteria I could not falsify
- None. AC-20's mismatch sub-case can be falsified, and the probe falsifies it
  (Blocking 1).

### Citations I checked and found wrong
- AC-20, "an end at or before its start together with a non-zero duration,
  answered 400 'duration does not match end time'". This holds only when the
  duration differs from end minus start (Blocking 1).

Checked and correct this round: `handlers_booking.go:125-127, 234-237, 249-255,
259, 262-266, 275-288` (revision 3's citations `:234-237`, `:263-266`,
`:277-280` are accurate); §3.5 as revised; and that revision 3 changed nothing
outside §3.5, AC-20 and its own section (`git diff 120e5f0 1b05a05`).

**Probe (Blocking 1).** The link was `createPublicBookingLink` with options
[30, 60], reusable, and min notice 0. The start was `futureBookingStart()`, and
the end was start−30 min:
```
zz_pac40_r4_probe_test.go:27: end-30, duration -30           -> 400 {"error":"invalid duration"}
zz_pac40_r4_probe_test.go:27: end-30, duration_minutes -30   -> 400 {"error":"invalid duration"}
zz_pac40_r4_probe_test.go:27: end-30, duration 30            -> 400 {"error":"duration does not match end time"}
--- PASS: TestPAC40R4Probe (0.02s)
```

### Consumers this breaks
- Nothing new. The lists from rounds 1–3 stand. Revision 3 touched only spec
  text about public booking error answers, and those answers do not change.

## Revision — 2026-10-27 (answers challenge round 4)

Checked against source at `6d751f9`, still the tip of `origin/main`
(`handlers_booking.go:124-127, 230-300`; `handlers_scheduling_links.go:157-175,
212-225, 440-455`; `handlers_scheduling_links_test.go:258-268`;
`e2e/tests/scheduling-links.spec.ts:110, 188`). Nothing was run. I did not
re-run the challenger's probe; the answers in AC-20 (c) and (d) come from
reading source and from the round 4 probe output. No comment from the product
owner on the issue answers an open question, so every decision stays assumed.

1. **Blocking 1, AC-20 contradicted §3.5 for a negative stated duration.**
   Accepted. `:263` fires only on a non-zero, *differing* duration; a stated
   duration equal to `actualDuration` (`:262`, whole minutes truncated toward
   zero) passes it and then fails `durationAllowed` (`:125-127`).
   *Changed:* §3.5 now states a two-case partition for an end at or before the
   start; AC-20 refers to it and names four required cases (a)–(d), including
   the negative-equal case answered "invalid duration".
2. **Non-blocking 1, which field is "the duration".** Accepted (`:249-255`:
   `duration` overrides `duration_minutes` even at 0). *Changed:* §2.5 gains a
   bullet naming the fields and their order; §3.5 defines "the stated duration"
   without naming fields (sections 3–5 rule); AC-20 case (c) pins a primary
   duration of 0 beside an alternative of 30.
3. **Non-blocking 2, items carried forward from rounds 1–3.** All are now
   addressed, so none is left for the contract stage:
   - Non-owner on a deleted link (round 1 item 1): new AC-6a and decision D-9;
     OQ-5 notes that its precondition is settled.
   - Pins not labelled (round 1 item 2): AC-7, AC-8, AC-9, AC-11 and AC-12 now
     carry "(passes today; pins …)".
   - "Carries both" undefined for null and `[]` (round 1 item 3): defined in
     §3.2 from `handlers_scheduling_links.go:159-163` and `:444-449`; new
     AC-14a pins both verbs.
   - In-flight booking and in-flight edit around a delete (round 1 item 4,
     round 2 item 2): §3.1 scopes its guarantees to requests started after the
     delete; AC-3 and AC-4 say "later"; OQ-4 now gathers all three races into
     one triage follow-up, per round 1 item 5.
   - AC-18's link (round 1 item 6): it must be seeded for that test alone.
   - Repeated delete 204 → not found (round 1 item 7) and the e2e hard-delete
     cleanup (round 3 item 3): named in §6.
   - §5's "optimistically" (round 1 citation) and the stale-tab not-found
     (round 1 consumers): §5 rewritten.
   - Starting value of the use count (round 2 item 4): §3.3 states it, D-8
     records it, AC-17a pins it.

New decisions D-8 and D-9 are assumed by the factory, pending owner
confirmation. Neither deletes data or widens who can see data: D-8 can only
close a link, and D-9 only narrows what a stranger learns.

Status stays challenged. The spec goes back to the challenger for round 5.

## Challenge — 2026-10-27 (round 5)

I reviewed the committed spec at `040419f` (revision 4) against source on
`main` at `6d751f9`, which is still the tip of `origin/main`, the web repo at
`origin/main` `ad52160`, and the Linear issue text. I ran one probe: a
temporary test file, since deleted, against a throwaway Postgres 16 container,
since removed. Nothing was committed apart from this section.

**Verdict**: accept-with-changes

Revision 4 closes round 4's blocking item. §3.5's partition and AC-20 (a)–(d)
now agree with each other and with the code (`handlers_booking.go:249-288`).
The carried-forward items are also addressed: AC-6a/D-9, the pin labels,
AC-14a, AC-17a/D-8, and the §5/§6 notes. I checked each against source.
Revision 4 introduced one new defect. §3.2 states a general "carried" rule for
every aliased setting, and §2.3 claims the window aliases are already
consistent between verbs. The code contradicts that claim for an empty
canonical window. That is the only blocking item. The decisions are still
conservative: none deletes data, and none widens who can see data.

### Blocking
1. **§2.3 says "the window start/end aliases do *not* have this defect: both
   verbs prefer the canonical spelling". This is false when the canonical
   window is an empty string. §3.2's new definition of "carried" then also
   covers the window settings and requires create to change, with no AC.**
   Create treats an empty canonical window as absent and falls through to the
   alias (`handlers_scheduling_links.go:176-188`, `if start == ""`). Edit
   treats any non-null canonical value as carried, so an empty one wins
   (`:456-465`, `patch.WindowStart != nil`), and validation then rejects it.
   The same body therefore means different things on the two verbs. That is
   the issue's own problem statement, applied to a setting the spec says is
   already fine. Probe output:
   ```
   create window canonical "" + alias 10:00/16:00 -> 201 {... "window_start":"10:00:00","window_end":"16:00:00" ...}
   edit   window canonical "" + alias 10:00/16:00 -> 422 {"error":"window times must use HH:MM"}
   ```
   §3.2 now says that "when a create or an edit carries both the canonical
   spelling and the alias for the same setting, the canonical spelling wins",
   and that "a spelling counts as carried when its value is present and not
   null". Read as written, this covers the window settings. It makes the empty
   canonical window win on create too, so the 201 above would have to change.
   It also forbids the create behaviour that §2.3 describes as correct. No AC
   pins either outcome, so the plan would have to guess.
   *Resolve:* correct §2.3's third bullet so that it states the empty-string
   divergence. Then do one of two things in §3.2: limit the "carried" rule to
   durations and days (the settings API-049 and the issue name), and record the
   window divergence as out of scope in §5; or state which answer both verbs
   give for an empty canonical window beside a non-empty alias, and add an AC
   that pins it on both verbs. Either is consistent with the issue. The second
   changes a create that succeeds today, which D-5's reasoning argues against
   unless it is recorded as a decision.

### Non-blocking
1. **A pending invitee can still leave a deleted link, and that deletes their
   host row.** The same probe deleted a link that had a pending invitee. Then
   `POST /{id}/leave` returned 204 for the invitee and 400
   `{"error":"owner cannot leave link"}` for the owner. §3.1 ("every one of
   those paths answers … not found") already forbids both, but AC-5 covers
   leave only for an *accepted* co-host, and no AC covers the owner. Extend
   AC-5 to cover a pending invitee and the owner. Today the invitee's leave
   also changes stored data, which "nothing changes" in AC-6/AC-6a is meant
   to prevent.
2. **"The API's primary spelling" (§3.5) is defined only by inference.** §2.5
   says `duration` overrides `duration_minutes`, and the contract calls
   `duration` the one that "takes precedence" (`booking.yaml:1328`). However,
   the response field is `duration_minutes` (`:1343`, `:1362`), so a reader
   could take that one as primary. That reading reverses AC-20 (c). One clause
   saying "the primary spelling is the one that takes precedence when both are
   sent" removes the ambiguity without naming fields.

### Criteria I could not falsify
- None. AC-14a's edit case fails today: the alias overrides the empty canonical
  list at `:444-449`. The create case passes today: the probe returned 422
  `{"error":"at least one duration is required"}` for `duration_options: []`
  beside `durations: [30]`. AC-17a fails today. AC-6a fails today: the
  non-owner paths return 403 at `:426, :543, :571, :604`, and fetch returns 200
  at `:391-408`. AC-20 (a)–(d) match round 3 and round 4 probe output and the
  code at `handlers_booking.go:249-288`.

### Citations I checked and found wrong
- §2.3, third bullet: "The window start/end aliases do *not* have this defect:
  both verbs prefer the canonical spelling". For an empty canonical value,
  create prefers the alias and edit prefers the canonical (Blocking 1, probe).

Checked and correct this round: `handlers_booking.go:124-127, 234-237, 249-255,
262, 263-266, 275-288`; `handlers_scheduling_links.go:157-175, 219-223,
391-408, 417-426, 444-455, 527-553, 562-571, 595-604, 635-656, 695`
(`json` decodes `[]` as a non-nil empty slice and `null` as nil, so §3.2's
null/`[]` rule holds for durations and days on both verbs). Web:
`schedulingLinks.ts:118-131` (canonical spellings only, including windows) and
`:544`; `Links.tsx:349-355`. The issue text: its alias paragraph names only
durations and days.

### Consumers this breaks
- Nothing new. The lists from rounds 1–4 still stand. The web app sends
  non-empty canonical windows only (`schedulingLinks.ts:125`), so neither
  resolution of Blocking 1 changes it. `mcp/` has no scheduling-link caller.

## Revision — 2026-10-27 (answers challenge round 5)

Checked against source at `6d751f9`, still the tip of `origin/main`
(`handlers_scheduling_links.go:157-190, 440-470, 690-720`;
`contracts/openapi/paths/booking.yaml:1322-1332`). Nothing was run. I did not
re-run the challenger's probe; the window and leave answers below come from
reading source and from the round 5 probe output. No comment from the product
owner answers an open question, so every decision stays assumed.

1. **Blocking 1, §2.3 wrong about the window aliases; §3.2 read as covering
   them.** Accepted. Create falls back to the alias when the canonical window is
   `""` (`:176-188`); edit takes any non-null canonical value (`:456-465`).
   *Changed:* §2.3's window bullet now states the empty-string divergence with
   the probe answers. §3.2 is limited to durations and days and says the
   window keeps today's behaviour on both verbs. §5 lists the window
   divergence as out of scope; §6 lists today's window answers as behaviour to
   preserve, noting no test covers them; new OQ-7 asks triage for a follow-up.
   D-5 now says "for durations and days" and no longer claims the window
   aliases are consistent; new D-10 records the choice. I took the first
   resolution the challenge offered because it changes no request that
   succeeds today, consistent with D-5's reasoning.
2. **Non-blocking 1, leave on a deleted link by a pending invitee and by the
   owner.** Accepted (`:695-718`: the owner check at `:704` answers 400 before any host
   row lookup; `RemoveLinkHost` removes the pending row and the handler
   answers 204). *Changed:* AC-5 now covers both: the pending invitee gets not
   found and their invite is still stored, unchanged; the owner gets not found
   and nothing changes. Both cases are marked as failing today.
3. **Non-blocking 2, "primary spelling" defined only by inference.** Accepted
   (`booking.yaml:1328`: the field "Takes precedence over `duration_minutes`
   when both are sent"). *Changed:* §3.5 defines the primary spelling as the
   one that takes precedence when both are sent, and says it need not be the
   spelling the response uses. AC-20 (c) is unchanged and now reads one way.
4. **Non-blocking 3, revision 4's other changes check out.** Nothing to change.

New decision D-10 is assumed by the factory, pending owner confirmation. It
deletes no data and widens no one's view of data; it leaves today's behaviour
in place.

Status stays challenged. The spec goes back to the challenger for round 6.

## Revision 6 — 2026-11-13 (owner decision; rebased on main `68540f6`)

The spec body above was rewritten against `origin/main` at `68540f6` (web
`ae72939`) after `origin/main` was merged into this branch. Every §2 citation
was re-read at that commit; nothing was run. Challenge history (rounds 1–5 and
revisions 1–5) is kept below the body unchanged, and its line numbers refer to
`6d751f9`.

1. **Owner decision PAC-40, 2026-10-27, applied.** New §0 states the recorded
   answer, the confirmation reported by the orchestrator, and readings R-1–R-5.
   New §3.2 (archive) and §3.5 (erasure: due rule, erased vs kept, what is not
   reached, schedule, idempotence, atomicity, concurrency, late bookings,
   restore, audit, PAC-84, runbook). New AC-21–AC-37. OQ-1 and OQ-2 resolved.
   D-2 and D-4 rewritten; new D-11 (notes kept), D-12 (last booking date =
   latest meeting end), D-13 (schedule). AC-6 no longer makes the owner's
   bookings read not found; AC-6a adds the archive to the paths a stranger
   gets not found on. §5, §6 and §7 updated: erasure is irreversible, and the
   down migration must cope with erased rows.
2. **PAC-25's 404 for AC-19.** AC-19 now pins the unknown-id answer that main
   gives (`handlers_scheduling_links.go:488-502`) and is labelled "passes
   today". OQ-5 resolved; the PAC-40 contract's C-1 (403) is withdrawn. API-006
   leaves the inherited scope.
3. **API-073 shipped by NEW-booking-rate-limit.** §3.5 of revision 5, AC-20 and
   AC-20a retired; §2.9 records the state, with the overflow range corrected as
   the contract challenge asked (only 153,722,868–307,445,734 minutes wrap
   before the start). The "end must be after start" pin moves to OQ-6's
   follow-up.
4. **Citations re-done for PAC-71/72/84/88/90.** §2 is renumbered (2.3–2.5 are
   new: bookings, retention/backups/encryption, background jobs). Notable
   changes since `6d751f9`: the edit handler's window aliases now use
   `else if` (`:564-573`) with the same result; deletion runs under the PAC-71
   session re-check (`guarded_writes.go:142-148`); public booking create
   follows PAC-72's offerable-start rule (`handlers_booking.go:415-464`).

Status stays challenged until challenge round 6.
