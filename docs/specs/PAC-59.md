# Spec — PAC-59: Creating a scheduling link starts with an 11-control form, and the co-host differentiator is buried

- **Linear**: https://linear.app/paceday/issue/PAC-59
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**: **none expected.** This spec asks for no change to the
  HTTP interface (see §3.4); everything it needs is already returned today. If
  the contract stage nevertheless modifies an operation, these come with it under
  touch-it-fix-it: create/update link → **API-049**; invite co-host →
  **API-048**, **API-072**; fetch one link → **API-006** and the `x-uncertain`
  at `contracts/openapi/paths/booking.yaml:473`. None of these is claimed by any
  other spec or feature manifest today (grep of `docs/specs`, `contracts/features`).
  Written from reading source; nothing was run.

## 1. Problem

Sharing a scheduling link is the most common reason anyone visits the Links
page, and today it costs a form. "New link" opens an 11-control drawer; the
Create button stays disabled with no stated reason until a title is typed; on a
768px-tall screen the button is below the fold. Paceday's one differentiator
here — co-hosts, where slots appear only when *everyone* is free — sits last, in
a muted box, and the warning that a co-host's calendar will *not* be counted is
visible only on hover. Three mutually exclusive "link types" (Reusable /
Recurring / Single use) encode one idea, a booking cap, and "Recurring" reads as
"recurring meeting". The critique scored the flow 24/40 with 4 of 8
cognitive-load checks failing.

It costs both seats (PRODUCT.md: equally primary). An IC who wants to send "a
link" faces a configuration task. A manager who adds teammates as co-hosts can
publish a link that offers slots when a teammate is busy, and is told so only by
a tooltip — or, as §2.4 shows, is told the opposite. PRODUCT.md positions Paceday
on simplicity ("prefer a good default to a setting; one obvious path"); this
screen is the opposite.

## 2. Current behaviour

### 2.1 Entry points
- "New link" (`smart-calendar-flow/src/pages/Links.tsx:400-402`) and the empty
  state's "Create your first link" (`:278-280`) both call `openCreate`
  (`:364-366`), which opens the drawer with no link (`:464`). Nothing is created
  until the drawer is submitted.

### 2.2 The drawer (`src/components/links/LinkEditDrawer.tsx`)
- Controls, top to bottom: title (`:314-323`), durations multi-toggle 15/30/45/60
  (`:326-348`), seven day toggles (`:351-373`), start and end time (`:376-385`),
  buffer before / after (`:388-415`), minimum notice (`:418-433`), link type with
  three cards and a conditional "Allow up to N bookings" input (`:436-485`),
  read-only public URL (`:487-497`), co-host email + Add (`:501-524`). That is 11
  groups before Cancel / Create.
- Pre-filled defaults for a new link (`:135-147`): 30 min; Mon–Fri; 09:00–18:00;
  5 min buffers either side; 1 hour notice; reusable; no co-hosts. These differ
  from the server's own defaults when a field is omitted (`clockwise-like/backend/api/handlers_scheduling_links.go:158-189`:
  09:00–17:00, buffers and notice 0).
- The footer with Create sits at the end of the scrolling content
  (`:557-565` inside `SheetContent ... overflow-y-auto`, `:306`), so it scrolls
  off-screen on a short viewport.
- Create is disabled whenever client validation fails (`:301`, `:561`); the
  validation message (`src/api/schedulingLinks.ts:374-392`, first rule "Give the
  link a title.") is shown only after a submit attempt (`:260-265`, `:551-555`),
  which the disabled button prevents. So the reason is never shown.
- The server also rejects an empty title (`handlers_scheduling_links.go:213-215`).

### 2.3 Booking limit ("link type")
- Three options with descriptions (`LinkEditDrawer.tsx:56-76`); "Recurring"
  means "bookable N times, then auto-disables". The server accepts
  `reusable | recurring | single_use` and requires a positive cap only for
  recurring (`handlers_scheduling_links.go:254-268`).
- A single-use link and a recurring link capped at 1 are exhausted under the
  same condition (`smart-calendar-flow/src/api/schedulingLinks.ts:344-345` in
  demo data; server exhaustion is the shared `linkExhausted` path noted in
  audit API-073).
- The list card shows "Single use", "k/N used" (or "Recurring"), or "Reusable"
  (`Links.tsx:71-95`).

### 2.4 Co-hosts, and what the page claims about their calendars
- Collective slots count **only accepted hosts** (`clockwise-like/backend/engine/booking.go:74-98`,
  `GetAcceptedHosts`). A pending co-host's calendar is ignored until they accept.
- A co-host must already have a Paceday account. On create, unknown emails are
  silently skipped (`handlers_scheduling_links.go:357-363`); the invite operation
  returns 404 "user not found" (`:604-609`). The web client sends co-hosts as one
  invite call each and discards every failure (`schedulingLinks.ts:500-502`, 
  `Promise.allSettled`), then re-adds the dropped emails locally as hosts
  (`:508-509`). So a non-Paceday co-host appears on the link in the UI but does
  not exist on the server and never affects slots.
- The drawer badges each co-host from a free/busy lookup
  (`LinkEditDrawer.tsx:159-196`, `CoverageBadge.tsx`). "Availability synced"
  carries a tooltip saying slots "will already exclude their busy time — no need
  to invite them to Paceday" (`CoverageBadge.tsx:72-90`). Per the two bullets
  above that is false: a non-Paceday person is dropped, and a Paceday person
  counts only after accepting. "Availability unknown" explains the risk only in a
  hover tooltip (`:93-107`).
- The list card shows "· awaiting acceptance" when any host is pending
  (`Links.tsx:136-139`); nothing says what that means for slots.

### 2.5 Public URL
- The drawer previews a URL derived from the title (`LinkEditDrawer.tsx:151-154`,
  `:487-496`, copy: "generated from the title"). But the client omits the slug on
  create (`schedulingLinks.ts:108-121`, pinned by `schedulingLinks.test.ts:257-273`)
  and the server generates it from the owner's first name plus the first duration
  (`backend/engine/booking.go:39-57`, e.g. `nicolas-30min`, random suffix on
  collision). The preview is therefore wrong for every new link.
- `GenerateSlug` indexes `strings.Fields(ownerName)[0]` (`booking.go:40`) and
  panics on an empty owner name. This is reachable today; it is not introduced
  here but an instant-create button makes it one click away (§8 OQ-5).

### 2.6 Tests that exist
- Web: client mapping and validation in `src/api/schedulingLinks.test.ts`
  (create body names `:71`, slug omitted `:257`, 422 surfaced `:275`, list-refetch
  resilience `:283`, demo fallback `:156`). No component test for the drawer,
  the Links page or `CoverageBadge`.
- Backend/e2e: API-level create-and-book round trip
  (`clockwise-like/e2e/tests/scheduling-links.spec.ts`), no UI journey for
  creating a link.

## 3. Desired behaviour

### 3.1 One click makes a working link
"New link" (and the empty-state button) immediately creates a link with
sensible defaults, puts its public address on the clipboard, and confirms:
"Link created and copied" with an **Edit** action. The new link appears at the
top of the list with its real address. No form stands between the user and a
shareable link. The defaults are the ones the form pre-fills today (30 min,
weekdays, 09:00–18:00, short buffers, 1 hour notice, no booking limit, no
co-hosts) and the title names the duration (e.g. "30-minute meeting"). If the
clipboard is unavailable, the link is still created and the confirmation shows
the address with a copy button instead of claiming it was copied. If creation
fails, nothing is copied and the error is shown.

### 3.2 Editing is short, with essentials first
Opening a link (Edit, or Edit from the confirmation) shows only the essentials:
**title, duration, co-hosts**, and the real public address with a copy button.
Everything else — days and hours, buffers, minimum notice, booking limit — sits
behind a single "More options" disclosure, closed by default, open by default
only if the link already deviates from the defaults in one of those settings.
The Save action is always on screen (it does not scroll away), and whenever
saving is not possible the reason is written next to it, not withheld.

### 3.3 Booking limit is one optional setting
Reusable / Recurring / Single use become a single optional "Limit bookings"
setting: off means unlimited; on takes a number N ≥ 1, after which the link stops
taking bookings. The word "Recurring" disappears from link creation, editing and
the list. Existing links read back as their equivalent: reusable → off; single
use → limit 1; recurring with cap N → limit N. The list card shows nothing extra for an
unlimited link, or "k of N booked" (and "· full" when exhausted).

### 3.4 Co-host coverage tells the truth, visibly
The page states, in visible text next to each co-host (never only on hover),
whether that person's calendar **will count** for slots, using what the server
actually does:
- on Paceday and accepted → counts;
- on Paceday, invite pending → "won't count until they accept";
- no Paceday account → cannot be added as a co-host; the page says so at the
  moment of adding and does not show them as a host afterwards.

When at least one co-host does not count yet, the editor shows one plain
summary line above Save ("Slots ignore 1 co-host's calendar until they accept"),
and the list card for that link says the same in visible text instead of only
"awaiting acceptance". The page never claims a calendar is excluded from slots
when the server will not exclude it. Free/busy shown here is existence of
coverage only — no event titles — consistent with PRODUCT.md privacy.

This is achievable with what the interface already returns (host status, and the
invite operation's rejection of unknown accounts). Making non-Paceday co-hosts
actually count (via their externally readable free/busy) is a product decision
and a server change: out of scope, see §5 and OQ-1.

### 3.5 Proposed split
This spec is buildable as one frontend-led change with no contract change. If
the challenger judges it too large, split along §3.1+§3.2 (creation and editor
layout) / §3.3 (booking limit) / §3.4 (co-host honesty); §3.4 is the one with
user-visible correctness at stake and should not be the one deferred.

## 4. Acceptance criteria

AC-1. Given a signed-in user on the Links page, when they press "New link", then
without any further input a new link exists on the server, appears first in the
list with a non-empty title naming its duration, and its public booking page
returns slots for its default window on a free weekday.  [e2e]

AC-2. Given clipboard access is granted, when "New link" succeeds, then the
clipboard contains exactly the address shown on the new link's card, and a
confirmation offering "Edit" is shown.  [e2e]

AC-3. Given clipboard writing fails, when "New link" succeeds, then the
confirmation does not say "copied", shows the address, and offers a copy
control; the link still exists.  [unit]

AC-4. Given the server rejects creation, when "New link" is pressed, then no link
is added to the list, the clipboard is not written, and the server's error
message is shown.  [unit]

AC-5. Given an empty link list, when the empty-state create button is pressed,
then the behaviour of AC-1 and AC-2 applies.  [e2e]

AC-6. Given a link with all-default settings, when its editor opens, then exactly
title, duration, co-hosts and public address (with copy) are visible, and days,
hours, buffers, notice and booking limit are hidden behind one closed "More
options" control.  [unit]

AC-7. Given a link whose minimum notice differs from the default, when its editor
opens, then "More options" is open.  [unit]

AC-8. Given the editor open in a 1280×768 viewport with "More options" expanded
and five co-hosts, when nothing is scrolled, then the Save control is fully
visible.  [e2e]

AC-9. Given the editor with an empty title, when the user looks at Save, then a
visible message next to it states the reason (e.g. the title is required), and
the same holds for every other client-side validation failure.  [unit]

AC-10. Given the editor for any link, then the public address shown equals the
address under which the public booking page serves that link (never a
title-derived guess).  [e2e]

AC-11. Given the editor, when "Limit bookings" is off and the link is saved,
then the link accepts bookings without a cap; when it is on with N, the
(N+1)th booking attempt on the public page is refused as full.  [e2e]

AC-12. Given existing links of each former type (reusable; single use;
recurring with cap 4), when their editors open, then "Limit bookings" reads
off; on with 1; on with 4 respectively, and saving without changes leaves their
booking behaviour unchanged.  [unit]

AC-13. Given the Links page and the link editor, then the word "Recurring" does
not appear anywhere in them.  [unit]

AC-14. Given a link capped at N with k bookings, then its card shows "k of N
booked", and "full" when k = N.  [unit]

AC-15. Given the owner adds as co-host an email with no Paceday account, when
they add it, then a visible message says this person cannot be a co-host because
they are not on Paceday, and after saving the link's host list (in the UI and on
the server) does not include them.  [e2e]

AC-16. Given a link with a co-host whose invite is pending, then the editor
shows, in visible text without hover, that this co-host's calendar won't count
until they accept, and a summary line above Save states how many co-hosts are
not counted.  [unit]

AC-17. Given that same link, then its card in the list states in visible text
that slots ignore that co-host until they accept.  [unit]

AC-18. Given any co-host state, then no text on the Links page or the editor
asserts that a co-host's busy time is excluded from slots unless that co-host is
an accepted Paceday host.  [unit]

AC-19. Given the user is in demo mode, when they press "New link", then a link is
created in the demo data and the confirmation appears, with no network call to
the real server.  [unit]

## 5. Explicitly out of scope

- **Counting non-Paceday co-hosts' calendars in slots** (via external free/busy).
  It is the real fix for "invite a teammate who isn't on Paceday", but it changes
  slot computation and the contract and needs a product decision (OQ-1).
- **Choosing a custom address.** The server-generated address stays; editing it
  is not added here (the current drawer does not offer it either).
- **Per-user default link settings** (e.g. deriving the default window from the
  user's working hours) — see OQ-2; a fixed default is the simpler first step.
- **Undo on instant create.** Deletion is a soft delete that leaves the link in
  the owner's list (API-050); an undo built on it would leave a ghost. Fixing
  API-050 belongs to its own lifecycle work (audit plan E5).
- **Re-inviting an accepted co-host demoting them to pending** (API-048), and the
  ambiguous 404 on invite (API-072) — only in scope if the contract stage touches
  that operation.
- Bookings list display defects (API-019), cancellations re-opening a full link
  (API-051).

## 6. What must not change

- A link's booking behaviour: who can book, which slots appear (accepted hosts
  only), and when a capped link becomes full. Protected by
  `e2e/tests/scheduling-links.spec.ts` (create → book round trip) and
  `e2e/tests/public-booking.spec.ts`; nothing protects the cap path end to end
  (AC-11 adds it).
- The client keeps letting the server generate the address
  (`schedulingLinks.test.ts:257`) and keeps surfacing 4xx errors instead of
  falling back to demo data (`:136`, `:275`).
- A created link stays visible if the list refetch fails (`:283`).
- Demo mode keeps working with no account (`:156`, `:458`).
- Co-host limit of five (`LinkEditDrawer.tsx:289-291`); invites to Paceday users
  still create pending hosts who accept from the invites list.
- Editing an existing link keeps every setting it has today, reachable under
  "More options"; nothing is removed, only regrouped.
- Privacy: no co-host event titles are displayed anywhere on this page.

## 7. Rollback

Frontend-only revert of the web PR restores the drawer flow. No migration and,
if §3.4 holds, no contract change. Links created by instant create are ordinary
links and remain valid after a revert. Links saved with "Limit bookings" are
stored as one of the three existing types, so a revert reads them correctly.

## 8. Open questions

- **OQ-1** — Should a co-host without a Paceday account be able to count, using
  their externally readable free/busy (the lookup the drawer already performs)?
  Today the server drops them. This spec's answer is "say plainly they can't be
  added"; the alternative is a server and contract change. *Owner: product owner.
  Resolve: decision on Linear, then a separate issue if yes.*
- **OQ-2** — Should instant-create defaults come from the user's working hours
  instead of the fixed 09:00–18:00 the form pre-fills today (and which already
  disagrees with the server's own 09:00–17:00)? *Owner: product owner. Resolve:
  pick one; the spec defaults to today's form values.*
- **OQ-3** — Exact default title wording (e.g. "30-minute meeting" vs. "Meet
  with <first name>"), since it is what external bookers see first. *Owner:
  product owner.*
- **OQ-4** — Should the Edit action from the confirmation open the editor
  immediately, or should instant create also offer an "add co-hosts" shortcut,
  given co-hosts are the differentiator? The spec assumes Edit is enough.
  *Owner: product owner.*
- **OQ-5** — Owner with an empty display name: server slug generation panics
  (`backend/engine/booking.go:40`). Does that surface as a 500 the client shows,
  or does the request die without a body? *Owner: contract-author / plan-author.
  Resolve: one test against the create operation with a nameless user; if it
  crashes, fix within this work since instant create makes it one click away.*
- **OQ-6** — Is "Limit bookings" with N = 1 to be stored as single use or as a
  cap of 1? Behaviour is the same (§2.3); the choice matters only for older
  clients and reports. *Owner: contract-author.*

## Challenge — 2026-10-11

**Verdict**: accept-with-changes

Both repos were read at their current heads (web `main` @ `a3a0f76`). Nothing was
run. The spec answers all three of the issue's questions, and nearly every
§2 citation checks out. Two gaps would let the feature ship while it still breaks
the co-host promise that §3.4 and §6 make.

### Blocking
1. **Saving a link today demotes its accepted co-hosts to pending. §2 does not
   say so, and §5 scopes the cause out.** On every save of an existing link the
   web client re-invites *every* listed co-host
   (`smart-calendar-flow/src/api/schedulingLinks.ts:552-556`; the drawer passes
   the full list, `LinkEditDrawer.tsx:216-222`). The invite operation upserts
   status `pending` (`clockwise-like/backend/storage/scheduling_links.go:215-223`,
   `ON CONFLICT … DO UPDATE SET status = EXCLUDED.status`, which is audit API-048),
   and collective slots ignore non-accepted hosts (`engine/booking.go:81`). So
   renaming a link silently drops a teammate's calendar from its slots, and it
   also re-invites anyone who declined. This contradicts §6 ("which slots appear
   … must not change"), AC-12 ("saving without changes leaves their booking
   behaviour unchanged") and §3.4's "accepted → counts". §3.2 promotes co-hosts
   into the essentials editor, so every quick edit hits this path.
   *Resolve:* describe this in §2.4. Add to §3/§6 the requirement that saving a
   link never changes an existing co-host's invite status (accepted, pending or
   declined), and add an AC: given an accepted co-host, when the owner edits only
   the title and saves, then that co-host is still accepted and still counts. Move
   API-048 out of §5's "only if the contract stage touches it". The spec must
   require the behaviour whichever repo fixes it.
2. **Instant create has no rule against duplicates, and duplicates cannot be
   cleaned up.** Today a form stands between the press and the create request. After
   §3.1 a double press or a repeat press creates more links. Deleting is a soft
   delete that leaves the link in the owner's list forever (API-050, which §5
   cites only against undo). §4 covers success, clipboard failure and server
   failure, but not repeated presses. *Resolve:* add an AC that one press creates
   at most one link, and that the control cannot start a second create while one
   is in flight. Also state in §5 that a link created by mistake and then deleted
   stays in the owner's list until API-050 is fixed, or pull API-050 into scope.

### Non-blocking
1. **Proportionality ("go for simplicity").** The spec is proportionate to the
   issue overall: each section maps to one of the issue's three questions. Some
   parts add work the issue does not need:
   - §3.2/AC-7's rule that "More options" opens by default when settings deviate
     needs a definition of "deviate", and links created through the API carry
     the server's 09:00–17:00 and 0 defaults, so it is ambiguous for them.
     "Always closed" is simpler and still satisfies "nothing removed, only
     regrouped".
   - OQ-5's slug panic is a pre-existing server defect. Keep it as a test
     obligation and do not let it grow the scope.
   - §3.5's split is unnecessary. Ship it as one change, but blocking 1 must ship
     with or before §3.2.
2. **Resolve OQ-6 in the spec. Do not leave it to the contract author.** The
   public page shows "This is a one-time link" only for `single_use`
   (`smart-calendar-flow/src/pages/PublicBooking.tsx:366-370`). If limit 1 is
   stored as `recurring` with a cap of 1, that notice disappears, and AC-12's
   "booking behaviour unchanged" becomes arguable. Storing limit 1 as single use
   is the simpler answer.
3. **§3.4 leaves out declined co-hosts.** The server keeps them with status
   `declined` (`storage/scheduling_links.go:291-298`). Say what the editor and
   the card show for them.
4. **§2.4 leaves out a second false badge.** A co-host who was just added and has
   a Paceday account gets "On Paceday" (`LinkEditDrawer.tsx:193`, which
   `CoverageBadge.tsx:54-60` renders), although they will be *pending* after
   save. AC-16 covers only links that are already saved. Extend it to co-hosts
   added before save.
5. **Removing a co-host in the editor does nothing on the server.** No host
   removal operation exists, and save only sends invites
   (`schedulingLinks.ts:552-564`). The removed host comes back after the refetch.
   The issue does not ask for this, so it stays out of scope, but §5 should list
   it so that §6's "keeps every setting" is not read as covering it.
6. **The e2e ACs (AC-1, 2, 5, 8, 10, 11, 15) depend on test ids that do not exist
   yet.** `e2e/tests/scheduling-links.spec.ts:144-152` is a `test.fixme` for
   exactly this journey, blocked on test ids (TESTIDS-REQUIRED.md #2 / PAC-44).
   Name that dependency in the spec.
7. **Sequencing with PAC-55.** PAC-55 edits the drawer's start/end time inputs
   (`LinkEditDrawer.tsx:379,383`), and this spec moves them behind "More
   options". Whichever lands second must re-apply the other's change.

### Criteria I could not falsify
- None outright. AC-18 is a universal negative over page text. It can be
  falsified today through the "Availability synced" tooltip
  (`CoverageBadge.tsx:86`), but it needs an enumerated list of co-host states
  and strings to test against. Otherwise a pass proves only that the one known
  string is gone.

### Citations I checked and found wrong
- §2.2 "That is 11 groups": the listed controls make **9** groups (title,
  durations, days, time window, buffers, notice, link type, public URL,
  co-hosts). The count is 11 only if start/end and before/after are counted
  separately. The issue's "11-control" is the right term.
- §2.4 "On create, unknown emails are silently skipped
  (`handlers_scheduling_links.go:357-363`)": the server code is cited correctly,
  but the web never takes this path. The create body has no `co_host_emails`
  (`schedulingLinks.ts:109-123`), and co-hosts go only through the invite calls.
- §2.6 "no UI journey for creating a link": this is incomplete. One exists as a
  `test.fixme` at `e2e/tests/scheduling-links.spec.ts:144-152`.
- All other citations were verified as stated: Links.tsx 71-95, 136-139,
  278-280, 364-366, 400-402, 464; LinkEditDrawer 56-76, 135-147, 151-154,
  159-196, 260-265, 289-291, 301, 306, 314-565; CoverageBadge 72-107;
  schedulingLinks.ts 108-121, 344-345, 374-392, 500-509; test lines 71, 136,
  156, 257, 275, 283, 458; backend handlers 158-189, 213-215, 254-268, 604-609;
  booking.go 39-57, 74-98; booking.yaml:473 `x-uncertain`.

### Consumers this breaks
- smart-calendar-flow/src/pages/PublicBooking.tsx:366 reads `usage_type ===
  "single_use"`. It breaks if limit 1 is stored as a cap (OQ-6).
- smart-calendar-flow/src/pages/Links.tsx:71-95 (`UsageBadge`) is replaced by
  §3.3, and `schedulingLinks.ts:343-347` (demo `isLinkExhausted`) must keep
  agreeing with it.
- smart-calendar-flow/src/api/schedulingLinks.test.ts:257 and :283 are kept by
  §6. AC-1 changes the create trigger, not the body.
- clockwise-like/e2e/tests/scheduling-links.spec.ts:144 (`test.fixme`) is
  superseded by AC-1/AC-5.
- clockwise-like/docs/specs/PAC-55.md edits the same drawer inputs (see
  non-blocking 7).
