# Spec — PAC-40: A deleted scheduling link does not go away, the same request means different things on create and edit, and cancelling a booking re-opens a used-up link

- **Linear**: https://linear.app/paceday/issue/PAC-40
- **Status**: challenged
- **Author**: spec-author
- **Inherited scope**:
  - **API-050** (soft delete visible in two of three read paths), **API-049**
    (alias precedence inverted between create and edit), **API-051**
    (`uses_count` recomputed from live bookings), **API-073** (dead re-check in
    public booking create) — named by the issue.
  - **API-006** and the `x-uncertain` on `getSchedulingLink`
    (`contracts/openapi/paths/booking.yaml:520-522`). This spec changes what
    fetching one link returns (AC-2), so touch-it-fix-it (README §7 rule 1)
    pulls the missing authorization in. No other spec or feature manifest claims
    it: PAC-59 lists it only conditionally ("if the contract stage nevertheless
    modifies an operation", `docs/specs/PAC-59.md:6-12`); audit plan B4
    (`docs/factory/api-audit.md:549-551`) has no Linear issue linked in the repo.
  - **PAC-30 D-5 follow-up `inactive-link-invites`**
    (`contracts/features/PAC-30.md:124-129`) — only for *deleted* links (AC-5,
    D-4). The paused-link half stays as PAC-30 left it.
  - Not inherited: **API-053** (`days` alias drops unrecognised day names) is on
    the same create operation but is a validation-message defect, not a
    lifecycle one; it stays open (§5).
  - Written from reading source on `main` at `6d751f9`; nothing was run.

## 1. Problem

A Paceday user shares scheduling links with external bookers. Three things about
a link's life go wrong today.

**Deleting a link does not delete it.** The owner deletes a link, the public
page stops taking bookings, and the link stays in the owner's list — forever,
with nothing to show it was deleted beyond a switch reading "Paused". Its
co-hosts keep it in their list too. Pressing the switch brings a "deleted" link
back to life. Anyone who created a link by mistake has no way to make it go away,
which PAC-59's challenge already identified as a blocker for one-press link
creation (`docs/specs/PAC-59.md:350-358`).

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

**Anyone signed in can read any link by id.** Fetching one link has no
ownership check, unlike every other owner operation on links, and returns every
host's email, name and avatar (API-006). This spec changes that same read, so
it fixes it.

Who pays: owners (both seats in PRODUCT.md) who cannot clean up their links;
co-hosts who see links that no longer exist; and the owner's calendar, which can
take a meeting from a link the owner believed was used up.

## 2. Current behaviour

### 2.1 "Deleted" and "paused" are the same state

- One stored flag means both. The table has a single boolean `active`, default
  true (`backend/storage/migrations/009_scheduling_links.up.sql:12`). There is no
  other lifecycle column.
- Deleting sets that flag false and nothing else
  (`backend/storage/scheduling_links.go:159-163`, called from
  `backend/api/handlers_scheduling_links.go:527-553`, which answers 204 for the
  owner and 403 for anyone else).
- Editing a link can set the same flag either way
  (`handlers_scheduling_links.go:481-483`, persisted at
  `storage/scheduling_links.go:146`). The contract says so: "Setting false is
  the same soft-deactivation that DELETE performs"
  (`contracts/openapi/paths/booking.yaml:1677-1679`).
- The web app uses the flag as a **pause** switch. Each card shows "Active" or
  "Paused" and a toggle (`smart-calendar-flow/src/pages/Links.tsx:146-148`) that
  sends an edit with the flag (`Links.tsx:349-352`); Delete is a separate action
  (`Links.tsx:330-331`, `:375`). So in the product, `active = false` already
  means two different things the server cannot tell apart.

### 2.2 The read paths disagree about the flag (API-050)

| Read path | Filters on the flag? | Citation |
|---|---|---|
| Public link by slug (info, slots, book) | yes — `AND active = true`, so 404 | `storage/scheduling_links.go:96-106` (filter `:103`); used at `handlers_booking.go:107`, `:145`, `:212` |
| Owner's / co-host's link list | no | `storage/scheduling_links.go:120-138`; `handlers_scheduling_links.go:370-388` |
| One link by id | no — 200 with `active: false` | `storage/scheduling_links.go:108-118`; `handlers_scheduling_links.go:391-408`; contract admits it at `booking.yaml:539` and `:615-618` |
| Edit, delete, list bookings, invite co-host, leave | no (same by-id lookup) | `handlers_scheduling_links.go:417`, `:534`, `:562`, `:595`, `:695` |
| Co-host invite list | no | `storage/scheduling_links.go:300-318`; `handlers_scheduling_links.go:635-656` |
| Accept / decline / leave | no — PAC-30 D-5 kept it | `contracts/features/PAC-30.md:124-129` |

Consequences, all reachable today: a deleted link stays in its owner's list and
in its accepted co-hosts' shared list; it can be fetched, edited (including back
to active), and have co-hosts invited to it; a pending invite to it is still
listed and can be accepted.

- A deleted link's slug stays reserved: the slug check counts every row
  regardless of the flag (`storage/scheduling_links.go:478-483`).
- Bookings on a deleted link still make their hosts busy, because the busy-time
  query does not look at the flag (`storage/scheduling_links.go:426-452`, PAC-64).
- On the public page a paused link and a deleted link are indistinguishable:
  both are 404 "not found". The booker page's "used up or paused" copy is shown
  for the 410 case only (`smart-calendar-flow/src/pages/PublicBooking.tsx:197`),
  so a paused link actually reads as "not found".

### 2.3 Alias precedence (API-049)

Create and edit accept `duration_options` or its alias `durations`, and
`days_of_week` or its alias `days` (`handlers_scheduling_links.go:73-76`,
`:92-95`).

- **Create** picks the canonical spelling when present, the alias otherwise
  (`handlers_scheduling_links.go:157-175`).
- **Edit** applies the canonical spelling, then the alias over it
  (`handlers_scheduling_links.go:444-455`), so the **alias** wins.
- The window start/end aliases do *not* have this defect: both verbs prefer the
  canonical spelling (create `:177-188`, edit `:456-465`).
- The web app sends only the canonical spellings
  (`smart-calendar-flow/src/api/schedulingLinks.ts:118-131`). No caller in
  `mcp/` builds a link body (grep of `mcp/` for the four field names: no hits).
- The only test that sends an alias on edit sends aliases alone, never both
  (`backend/api/handlers_scheduling_links_test.go:128-157`).

### 2.4 How "uses" are counted (API-051)

- There is no stored count. Every read computes it as the number of the link's
  bookings whose status is not `cancelled`
  (`storage/scheduling_links.go:87`, `:101`, `:113`, `:126`, `:151`). The
  contract documents this as "Count of non-cancelled bookings, computed by
  subquery at read time" (`booking.yaml:1473-1474`).
- A link is exhausted when single-use and the count is ≥ 1, or limited
  ("recurring") and the count is ≥ the cap
  (`backend/api/handlers_booking.go:65-73`). Exhaustion answers 410 on link info,
  slots and booking (`handlers_booking.go:116`, `:154`, `:221`).
- A booking row allows status `cancelled`
  (`migrations/009_scheduling_links.up.sql:33`), but **no code path in the
  backend, MCP server or web app ever sets it** (grep of the repo for writes to
  booking status: none). So today a cancelled booking exists only if someone
  edits the database. The re-opening is real but latent.
- The e2e seed depends on the derivation: the exhausted link is seeded by
  inserting a booking, not by setting a count (`e2e/seed/seed.sql:219-223`),
  and `e2e/tests/public-booking.spec.ts:79-85` asserts its 410.
- Exhaustion is checked before the booking is written, with no lock between
  the check and the insert (`handlers_booking.go:221`, `:293`, then
  `ConfirmBooking`). Two simultaneous bookings on a single-use link can both
  succeed. Not in the audit; see §5 and OQ-4.

### 2.5 Dead code in public booking create (API-073)

- After the duration branch, `if err != nil { … "invalid end time" … }` at
  `handlers_booking.go:281-284` cannot fire: every assignment to `err` before it
  already returned on error.
- The exhaustion test runs twice for one request: once through `linkExhausted`
  at `:221`, again inline at `:293-296`, with the same message and status. The
  inline copy can only differ from the first if the link changed in between,
  and it reads the same in-memory copy, so it never does.
- The "end must be after start" check at `:285-288` **is reachable** and is
  not part of API-073. When the request sends an end, the duration is the whole
  minutes between start and end (`:262`), so an end at or before start gives a
  duration of 0 or less, which `durationAllowed` rejects first as "invalid
  duration" (`:125-127`, called at `:277`). When the request sends no end,
  the end is computed as `start.Add(time.Duration(requestedDuration) *
  time.Minute)` (`:275`). Link validation requires only that each duration
  option is positive (`handlers_scheduling_links.go:219-223`); neither the
  contract nor the `INTEGER[]` column sets a maximum. For an option of
  153,722,868 minutes or more the multiplication overflows int64 nanoseconds
  and wraps, so the end lands before the start, the duration still passes
  `durationAllowed` (it is in the list), and the request is answered 400
  `{"error":"end must be after start"}`. The challenge (round 2) reproduced this
  with a probe; see Revision 2. Whether such duration options should be accepted
  at all is a separate defect (§5, OQ-6).

### 2.6 Fetching one link has no authorization (API-006)

`getLink` (`handlers_scheduling_links.go:391-408`) returns the full link with
hosts to any authenticated caller who knows the id. Edit, delete, bookings list
and invite all require the owner (`:426`, `:543`, `:571`, `:604`). The
contract marks it `x-uncertain` (`booking.yaml:520-522`); the audit's decision
U-08 is "it is a bug" (`docs/factory/api-audit.md:476`). The web app fetches a
link by id only right after the owner creates or edits it
(`smart-calendar-flow/src/api/schedulingLinks.ts:514`, `:544`, `:574`).

## 3. Desired behaviour

### 3.1 Paused and deleted are different states

- **Paused** is what the owner's switch does today, unchanged: the link stays in
  the owner's list and in accepted co-hosts' lists, can be fetched and edited by
  its owner, and can be resumed. Its public page does not take bookings, as
  today.
- **Deleted** is what Delete does. From the moment of deletion, the link does not
  exist for anyone through the product: not in any list, not fetchable, not
  editable, not resumable, not bookable, not offered as a co-host invite, and
  not acceptable as one. Every one of those paths answers the same "not found"
  it gives for an id or slug that never existed.
- Deleting does not erase anything. The link's record, its bookings and its host
  rows are kept (D-2). Its bookings keep making their hosts busy (the meetings
  still happen). Its slug stays reserved, so a new link can never answer at an
  address an external booker was given for the old one (D-3).
- Pausing cannot reach a deleted link, and resuming cannot bring one back. There
  is no un-delete in this spec.
- Links that are inactive **before** this change are treated as paused, not
  deleted, because the server cannot tell which they were (D-1). They stay
  visible to their owner exactly as today; an owner who meant to delete one
  deletes it again.

### 3.2 Both spellings, one meaning

When a create or an edit carries both the canonical spelling and the alias for
the same setting, the canonical spelling wins and the alias is ignored, on both
verbs (D-5). Sending one spelling alone behaves as today.

### 3.3 A use, once spent, stays spent

A booking counts against a link's limit from the moment it is made, and
cancelling it does not give the use back (D-6). A single-use link that has been
booked once stays used up whatever happens to that booking; a link limited to N
stays used up after N bookings have been made. The "uses" figure the owner sees
counts bookings made, including cancelled ones. An owner who wants the link to
take more bookings changes its limit, as today.

### 3.4 Only the people who can see a link in their list can fetch it

Fetching one link returns it to its owner and to its accepted co-hosts — the
same people who see it in their list — and refuses everyone else without
returning any of the link's details or its hosts (D-7).

### 3.5 Public booking create

The public booking create path rejects exactly the same requests with exactly the
same answers as today, without the two unreachable items: the repeated
end-time error and the duplicated exhaustion test. A request that sends an end
at or before its start keeps getting the answer it gets today, "invalid
duration". A request that sends no end, on a link whose duration is so long
that the computed end comes out before the start, keeps getting the answer it
gets today, "end must be after start".

## 4. Acceptance criteria

Throughout, "not found" means the answer the same path gives today for an id or
slug that has never existed — same status and same body.

**Deleted links (API-050)**

AC-1. Given an owner with a deleted link and an active link, when the owner
lists their links, then only the active link is returned. Given the same
deleted link with an accepted co-host, when the co-host lists their links, then
the deleted link is in neither the owned nor the shared group.  [contract]

AC-2. Given a deleted link, when its owner or an accepted co-host fetches it by
id, then the answer is not found.  [contract]

AC-3. Given a deleted link, when its owner edits it — with any body, including
one that sets it active — then the answer is not found, and afterwards the link
is still deleted (AC-1, AC-2 and AC-4 still hold).  [contract]

AC-4. Given a deleted link, when anyone requests its public info, its slots, or
books it, then each answers not found and no booking is created.  [contract]

AC-5. Given a deleted link with a pending co-host invite, when the invitee lists
their invites, then the invite is absent; and when the invitee accepts or
declines it, then the answer is not found and the invite's status is
unchanged. Given a deleted link with an accepted co-host, when the co-host
leaves it, then the answer is not found.  [contract]

AC-6. Given a deleted link, when its owner lists its bookings, invites a co-host
to it, or deletes it again, then each answers not found and nothing
changes.  [contract]

AC-7. Given a deleted link that has a confirmed booking in the future, when a
host's busy time is computed for that period, then the booked time is still
busy.  [unit]

AC-8. Given a deleted link with slug S, when anyone creates or edits another link
to use slug S, then it is rejected as taken, as for any existing slug.  [contract]

AC-9. Given a link with bookings and co-hosts, when its owner deletes it, then
its bookings and host records still exist in storage afterwards.  [unit]

AC-10. Given a signed-in owner on the Links page with a link, when they delete
it and reload the page, then the link is not shown; and the link's public
booking page shows the not-found state.  [e2e]

**Paused links stay as they are**

AC-11. Given a link its owner has paused, when the owner lists links or fetches
it, then it is returned with the paused state; when the owner resumes it, then
its public page takes bookings again.  [contract]

AC-12. Given a link that was inactive before this change was deployed, when its
owner lists their links, then it is listed as paused, and the owner can resume
or delete it.  [contract] (data-migration test)

**Alias precedence (API-049)**

AC-13. Given a create request carrying both spellings with different values for
durations, and both with different values for days, then the stored link has
the canonical spelling's durations and days.  [contract] *(passes today; pins
create)*

AC-14. Given an existing link, when an edit carries both spellings with different
values for durations, and both for days, then the link has the canonical
spelling's durations and days — the same result AC-13 gives for the same
values.  [contract] *(fails today: the alias wins)*

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

AC-18. Given the e2e stack with a single-use link that has been booked once,
when that booking's status is set to cancelled directly in the database, then
the public booking page still shows "link no longer available".  [e2e]

**Fetch authorization (API-006)**

AC-19. Given an active link with an accepted co-host and a pending invitee, when
its owner or the accepted co-host fetches it by id, then it is returned with its
hosts; when the pending invitee or any other signed-in user fetches it, then
the request is refused and the response contains none of the link's fields or
host details.  [contract]

**Public booking create (API-073)**

AC-20. Given each rejection the public booking create can make today (missing
fields, bad start, bad end, end/duration mismatch, invalid duration — including
a request that sends an end at or before its start — end not after start, too
soon, used up, overlapping slot, unknown slug), when the same request is sent
after the change, then the status and body are identical.  [contract] — This
criterion is the regression net, not a failing test: removing unreachable code
changes no observable behaviour, so the removal itself is proven by review of
the diff. That is stated rather than hidden.

AC-20a. Given an active reusable link with no minimum notice whose only
duration option is 200,000,000 minutes (long enough that the start plus that
duration wraps to a time before the start), when a booker requests a future
start on it without sending an end, then the answer is status 400 with body
`{"error":"end must be after start"}`, both before and after the change, and no
booking is created.  [contract] — Passes today (challenge round 2 probe); it
pins the "end not after start" rejection so that the API-073 removal cannot
take it out.

## 5. Explicitly out of scope

- **Un-delete / trash.** Deleted links are kept but there is no way back in the
  product. Adding one is a product decision and a new surface.
- **Hard delete or retention of deleted links.** Nothing is erased here
  (D-2). When deleted links' data is purged — PRODUCT.md's "deleted within 30
  days of cancelling" is about accounts, not links — is OQ-2.
- **A booking-cancel feature.** This spec decides what a cancellation does to the
  count; it does not add a way to cancel. ACs 15–18 create the cancelled state
  directly.
- **Two simultaneous bookings on a single-use link** (§2.4, last bullet). Real,
  related, and not in the audit or the issue. Raised as OQ-4 with a proposed
  follow-up rather than folded in, so this spec stays the size the issue asked
  for.
- **What a paused link's public page says.** It stays "not found" as today; the
  booker copy that mentions "paused" (`PublicBooking.tsx:197`) is reached only
  by "used up". Changing it is a frontend copy decision.
- **Accept / decline / leave on a paused link** — unchanged, PAC-30 D-5 stands
  for paused links.
- **Unbounded duration options.** A link may be created with a duration option
  so large that booking arithmetic overflows (§2.5). Bounding it would turn
  link creates and edits that succeed today into errors and is not in the audit
  or the issue; it is OQ-6. Until then, AC-20a keeps today's answer.
- **API-053** (`days` alias silently drops unrecognised day names), **API-069**
  (duplicate invite routes), **API-072** (ambiguous 404 on invite), **API-019**
  (bookings list shape) — same operations, different defects, not lifecycle.
- **Web app changes.** None are required: the Links page already removes a
  deleted link from its list optimistically and refetches; after this change the
  refetch agrees. The regenerated types will carry new descriptions only, unless
  the contract stage adds a field.

## 6. What must not change

- **Pause/resume from the Links page** — `Links.tsx:146-148`, `:349-352`. No
  frontend test toggles a link against the real API; no e2e test covers it.
  *Finding:* unprotected today; AC-11 adds the backend half.
- **Owner-only edit, delete, bookings list and invite, with 403 for others** —
  `TestSchedulingLinksOwnerOnlyBookingsAndDelete`
  (`handlers_scheduling_links_test.go:213-268`) and its siblings. For a
  *deleted* link the answer becomes not found (AC-3, AC-6); for a live link the
  403 stays.
- **Sending only an alias still works on both verbs** —
  `TestSchedulingLinksPatchUsesFrontendAliases`
  (`handlers_scheduling_links_test.go:128-157`) and
  `TestSchedulingLinksCreateAndListContract` (`:69-112`).
- **A booked single-use link answers 410 and the booker sees "link no longer
  available"** — `e2e/tests/public-booking.spec.ts:79-97`. Note for the plan:
  the seed creates that state by inserting a booking and relies on the count
  being derived (`e2e/seed/seed.sql:219-223`); if the counting changes how it
  is stored, the seed must still produce an exhausted link.
- **All public booking error answers** — `TestPublicBookingErrorsContract`
  (`handlers_booking_test.go:178-213`) covers five; the others in AC-20 have no
  test today, including "end must be after start". *Finding:* AC-20 and AC-20a
  add them.
- **Bookings make hosts busy whether or not a calendar event was written** —
  `TestGetConfirmedBookingsForUser_CountsHostedLinksWithoutCalendarEvents`
  (`storage/booking_busy_test.go:23`). AC-7 extends it to deleted links.
- **PAC-30's 404 rules for accept/decline/leave without a host row** —
  `TestSchedulingLinksRespondAndLeaveWithoutHostRowIs404` and siblings
  (`handlers_scheduling_links_test.go:431-581`).
- **Slug uniqueness across all links** — `TestSlugExists`
  (`storage/scheduling_links_test.go:256`).

## 7. Rollback

Reverting the code restores today's behaviour: deleted links reappear in lists
and become fetchable and resumable again (as paused), aliases win on edit, and
cancelled bookings give their use back.

If the implementation stores anything new — a marker separating deleted from
paused, or a stored use count — its down migration loses that information:

- **Deleted/paused marker:** after the down migration every deleted link is
  indistinguishable from a paused one and reappears in its owner's list, as it
  does today. No booking, link or host row is lost.
- **Stored use count:** after the down migration the count is derived again from
  non-cancelled bookings, so any link whose bookings were cancelled in the
  meantime re-opens. No booking is lost.

Neither direction deletes user data. The plan must state which of these it
introduces and confirm the down migration matches this section.

## 8. Open questions

- **OQ-1 — Can the owner see bookings of a deleted link?** AC-6 makes the
  bookings list not found, consistent with "deleted means gone from the
  product". The meetings themselves remain on the hosts' calendars and keep them
  busy (AC-7). If the owner wants a history of bookings on deleted links, that is
  a new surface. *Owner:* product owner (Nicolas Hochart). *Resolve:* a Linear
  comment; until then D-4 applies.
- **OQ-2 — Should deleted links ever be purged?** This spec keeps them
  indefinitely (D-2), as today. Bookers' names and emails stay stored on them.
  *Owner:* product owner. *Resolve:* a retention decision, likely its own issue
  alongside account-deletion retention.
- **OQ-3 — Should a paused link's public page say "paused" rather than "not
  found"?** Today it is 404, and this spec keeps that. *Owner:* product owner
  with frontend-implementer. *Resolve:* a separate copy/contract issue if wanted.
- **OQ-4 — Concurrent bookings on a single-use link** (§2.4). Two simultaneous
  requests can both pass the exhaustion check. *Owner:* spec-challenger to judge
  whether it must be in scope; otherwise triage files a follow-up issue.
  *Resolve:* challenge round.
- **OQ-5 — Does the refusal in AC-19 say "forbidden" or "not found"?** U-08 says
  add a 403 (`api-audit.md:476`), matching edit/delete. A not-found answer would
  hide whether the id exists. The spec requires only that no link data is
  returned; the contract stage chooses and records why. *Owner:*
  contract-author.
- **OQ-6 — Should duration options have a maximum?** Today any positive integer
  is accepted, and options of 153,722,868 minutes or more make booking
  arithmetic overflow (§2.5, AC-20a). *Owner:* triage, to file a follow-up issue
  alongside OQ-4. *Resolve:* a separate spec; this one preserves today's
  answer.

## Decisions

All decisions below are **assumed by the factory, pending owner confirmation**.
None deletes user data or widens who can see data.

- **D-1 — Existing inactive links are paused, not deleted.** The server cannot
  tell which inactive links were deleted and which were paused, because both
  wrote the same flag. Treating them as deleted would make every paused link
  vanish with no way to resume it; treating them as paused leaves them exactly
  as visible as today. The cost is that links deleted before the change must be
  deleted again.
- **D-2 — Delete stays a soft delete.** The link record, bookings and host rows
  are kept. Hard-deleting would erase booking history (booker names, emails,
  times) and is irreversible; the issue asks only for consistent visibility.
- **D-3 — A deleted link's slug stays reserved.** Releasing it would let a new
  link — possibly another user's — receive bookings from people holding the old
  URL. Unchanged from today.
- **D-4 — "Not found" covers every path that names the link by id or slug**,
  including the owner's bookings list for it, the co-host invite list and
  accept/decline. The issue's acceptance names list, get and public slug; the
  others are the same lookup and leaving them open would keep the ghost reachable
  (the invite list would still name a link the owner deleted). For paused links
  PAC-30 D-5 is unchanged. Busy-time computation is not a "path that names the
  link" and keeps counting its bookings (AC-7).
- **D-5 — The canonical spelling wins on both verbs.** It is what create does
  today, what both verbs already do for the window aliases, and the only spelling
  the web app sends, so no Paceday client changes behaviour. The alternative —
  rejecting a request that carries both with different values — is stricter but
  would turn requests that succeed today into errors; it can be revisited if a
  client is found sending conflicting values.
- **D-6 — A use is spent when a booking is made, and cancelling does not return
  it.** "Single use" and "limit N" bound how many meetings a link can put on the
  owner's calendar; re-opening on cancel lets anyone still holding the URL book
  again without the owner deciding so, which widens who can book. The owner can
  always raise the limit. The audit lists the re-opening as the defect
  (API-051), and the issue title frames recomputing from live bookings as the
  bug.
- **D-7 — Fetching one link is limited to the owner and accepted co-hosts.**
  Those are the people who see the link in their list, so nothing they can do
  today is lost; the only callers losing access are users with no role on the
  link, who should not have had it (U-08, API-006). Pending invitees already get
  the link's title and owner from the invite list.

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
   (`handlers_booking.go:236-239`, malformed JSON → 400). The list is presented
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
