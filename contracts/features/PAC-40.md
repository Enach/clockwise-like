# PAC-40 contract: scheduling-link lifecycle

Spec: `docs/specs/PAC-40.md` on `factory/pac-40-spec` at `f621674` (revision 5).
Manifest: `contracts/features/PAC-40.yaml`. Fragment: `contracts/openapi/paths/booking.yaml`.
The source was read on `main` at `6d751f9`.

## 0. Status of the input

The spec has not been accepted yet. Its status line reads "challenged". Round 5
returned "accept-with-changes" with one blocking item, and revision 5 answers
that item. Round 6 has not run, and the spec is not merged to `main`. The
factory has drafted contracts against specs in this state before (PAC-35,
PAC-55 and PAC-66 are all `draft` against "challenged" specs). This contract
is also a `draft`. The challenger should check two things:

- Revision 5 still holds.
- If round 6 changes an acceptance criterion, this contract follows the change.

Do not merge this contract before the spec is accepted.

## 1. What the contract says now that it did not say before

| Area | Before | After |
|---|---|---|
| Deleted vs paused | "Setting `active:false` is the same soft-deactivation that DELETE performs"; a deleted link is listed, fetchable, editable and invitable | Two states. Paused = `active:false`, visible as today. Deleted is invisible: every by-id or by-slug path answers the never-existed 404 to every caller, and lists and invite lists omit it |
| `getSchedulingLink` | No authorization, `x-uncertain` | Owner and accepted co-hosts get 200. Deleted link: 404 for everyone. Anyone else: 403 `{"error":"forbidden"}` |
| PATCH aliases | Alias wins (`durations` over `duration_options`, `days` over `days_of_week`) | The canonical spelling wins whenever it is carried (present and not null; `[]` counts), the same as create |
| `uses_count` | "Count of non-cancelled bookings" | Bookings ever made, cancelled ones included. The 410 is judged on this figure |
| `createPublicBooking` 400 | Six messages; "end must be after start" missing | Seven messages, plus the partition for end ≤ start and the overflow path |

Every response shape is unchanged. The only structural addition is a `403` on
`getSchedulingLink` and `minimum: 0` on `uses_count`. Everything else is a
description, so the regenerated web types gain one response and some comments.

## 2. Ordering of checks for a deleted link (C-4)

For every operation that names a link by id, "deleted" is checked at the same
point as "no such link": after the UUID parse, before the owner or host check,
and before the request body is read. Two consequences:

- A deleted link answers the same thing as a random UUID, including to a
  stranger. A malformed body sent to a deleted link is therefore a 404, not a
  400 (spec AC-6a, D-9).
- `leaveSchedulingLink` on a deleted link is a 404 for the owner. Today the
  owner gets the 400 "owner cannot leave link". That 400 comes after the
  existence check, so this is the same rule (AC-5).

Accept and decline do not load the link today. They update the caller's host
row by link id. The contract requires 404 and an unchanged row when the link is
deleted. How the handler finds that out is left to the plan.

## 3. Decision C-1: 403 rather than 404 for a stranger on a live link (spec OQ-5)

**Chosen:** a caller who is neither the owner nor an accepted co-host gets
`403 {"error":"forbidden"}` on a live or paused link. A deleted link always
gets 404 (D-9, fixed by the spec).

**Rejected: 404 for strangers on live links too.** It would hide whether a
link id exists, but it hides nothing in practice:

- `updateSchedulingLink`, `deleteSchedulingLink`,
  `listSchedulingLinkBookings` and `inviteSchedulingLinkHost` already answer
  403 to the same stranger on the same live id. Anyone holding the id can
  learn that it exists with a single PATCH.
- Making all five answer 404 would change four operations outside this
  spec's scope.
- The audit already decided the answer: U-08 reads "Add the owner check and a
  `403`" (`docs/factory/api-audit.md:476`).

403 keeps the five owner operations consistent.

**Rejected: 403 for a pending invitee replaced by a reduced view.** A pending
invitee already gets the title and owner from the invite list. A "preview"
body would be a new shape and would widen what an invitee sees. The spec
(AC-19) requires that none of the link's fields are returned.

## 4. Decision C-2: no "deleted" field on the wire

**Chosen:** `active` keeps its name, type and values, and now means
"paused". No response ever contains a deleted link, so no field needs to
describe one.

**Rejected: replace `active` with `status: active|paused|deleted`.**

- It breaks the web, which reads and writes `active` (`schedulingLinks.ts:111`,
  `:130`; `Links.tsx:146-148`, `:349-352`).
- Its `deleted` value could never appear in a response.
- PATCH could then express "set status deleted", which would duplicate
  DELETE. That is a request the server should not have to reject, and
  leaving it out of the shape makes it impossible to send.

**Rejected: a `deleted_at` timestamp in responses.** For the same reason: the
field would always be null.

## 5. Decision C-3: `uses_count` meaning, not mechanism

The contract fixes what the number means: bookings ever made, cancelled ones
included, starting from existing data (D-6, D-8). It also fixes that the 410
is judged on that number. It does not fix whether the number is stored or
computed from all rows, because that is the plan's choice (spec §7 allows
either).

**Rejected: a separate `bookings_made` field next to an unchanged
`uses_count`.** The web computes "used up" from `uses_count`
(`isLinkExhausted`, `schedulingLinks.ts:352-355`). If two fields existed and
the server gated on the new one, the client and server would disagree about
whether a link is used up.

## 6. Aliases (API-049)

The rule now reads the same on both request schemas: for durations and days,
the canonical spelling wins when it is carried, meaning present and not null.
An empty list counts as carried and fails 422. The window aliases are
deliberately left as they are, with the divergence documented (D-10, OQ-7).

**Rejected: rejecting a body that carries both spellings** (for example with
`not: {required: [duration_options, durations]}`). This would make the
ambiguous request impossible to express, which is normally preferred. But it
turns requests that succeed today on create into errors, and spec D-5 rejects
that explicitly.

**Not done: `minItems: 1` on `duration_options` / `days_of_week`.** It would
describe the existing 422, but the server answers it with specific messages
that a schema validator would not produce. No criterion asks for it.

## 7. What a challenger should try to break

- **A request the contract permits that the server should reject.** Send PATCH
  `{"active": true}` to a deleted link. The contract says 404 regardless of
  the body.
- **A response the client cannot render.** The new 403 on GET. The web calls
  GET only as the owner right after create or edit, so it never receives the
  403. If it did, `withFallback` would rethrow it as an error toast.
- **The x-uncertain gate.** The bundle's `x-uncertain` count drops from 12 to
  11. `ConferenceMeetingDetails` keeps its marker, and no operation PAC-40
  touches still carries one.

## 8. Rollback

See the manifest. No response shape is removed, so a backend revert alone
restores today's answers. The contract revision stays immutable.

## Challenge — 2026-10-27

Reviewed the committed contract at `08ae45e` (`contracts/openapi/paths/booking.yaml`,
the bundle, `contracts/features/PAC-40.yaml`, this file) against the spec on
`factory/pac-40-spec` at `f621674` (revision 5, still the tip of that branch), source
on `main` at `6d751f9` (still the tip of `origin/main`), and the web repo at
`origin/main` `ad52160`. The author's reasoning beyond these artefacts was not read.

What was run (output quoted where it matters):

- `make openapi-check WEB=/nonexistent` in a `golang:1.25-bookworm` LF clone of
  `factory/pac-40-contract`: `OK: bundle is in sync (98 paths)`, `OK: MIGRATION.md
  covers all 119 operations (0 generated)`, `OK: backend/api/gen matches the contract`,
  `openapi-check(api) rc=0`; `go build ./...` rc=0; `go vet ./api/...` rc=0.
- Web half of gate 4, in a separate `node:24-bookworm` container: web `origin/main`
  `ad52160`, `npm ci`, `make openapi-check CONTRACT=<this bundle>` gave
  `make: *** [Makefile:80: openapi-check] Error 1`, `rc=2`. Regenerating changes
  `types.ts` (131 lines), `schemas.types.d.ts` (67) and `schemas.ts` (2). That is
  expected for a contract with no paired web branch yet (none exists on either
  remote), but gate 4 is not green today.
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` gave
  `86fc227f…ccb3a7`, equal to the manifest's `contractHash`.
- `x-uncertain:` keys in the bundle: 12 on `main`, 11 here. The one left in
  `booking.yaml` (line 1966) is on `ConferenceMeetingDetails`. None of the 17
  operations in `operationsChanged` carries one (checked by walking the bundle).
- A standalone Go program evaluating `start.Add(time.Duration(n) * time.Minute)`
  exactly as `handlers_booking.go:275` does, from 2026-11-02T10:00Z (divergence 1).
- The full `api-gate.sh` was **not** run: it needs the host checkout on this branch,
  and the host checkout is on `impl/new-booking-rate-limit-backend`. This stage did
  not switch it. No HTTP probe was run.

**Verdict**: accept-with-changes

The lifecycle shape is right. Every by-id path gives one 404 for "never existed" and
"deleted", checked before the owner check and the body, so AC-6a holds. Paused stays
`active:false`, no wire field describes "deleted", and the new 403 on
`getSchedulingLink` sits after the 404. Two statements in the contract are
contradicted by the code or by the spec it implements. Both are description or
keyword fixes; no shape needs redesigning.

### Requests wrongly permitted / forbidden

1. **The contract forbids the `null` request that AC-14a requires to succeed.**
   `PATCH /api/scheduling-links/{id}` (and `POST /api/scheduling-links`) with
   ```json
   {"duration_options": null, "durations": [45]}
   ```
   Spec AC-14a (manifest AC-14a) says "duration_options null + durations [45] ->
   durations [45] on both verbs". The new descriptions say the canonical spelling
   wins "whenever it is carried (present and not null)" and that the alias applies
   "when duration_options is absent or null". But `duration_options`, `durations`,
   `days_of_week` and `days` are plain `type: array` with no `nullable: true` on both
   `CreateSchedulingLinkRequest` (`booking.yaml` ~1620-1650) and
   `UpdateSchedulingLinkRequest` (~1721-1750). Sibling fields on the same PATCH schema
   (`title`, `window_start`, …) do carry `nullable: true`. So the schema rejects the
   request that the text and AC-14a describe. The generated web zod schema already
   shows it: `UpdateSchedulingLinkRequest = z.strictObject({ … duration_options:
   z.array(z.number().int()), durations: z.array(z.number().int()), … })`
   (`smart-calendar-flow/src/api/generated/schemas.ts:437`; create at `:104`), which
   fails on `null`. The server accepts the request today: `schedulingLinkPatch` slices
   decode `null` to nil (`handlers_scheduling_links.go:444-455`), and the PATCH
   schema's own description says "an absent key and an explicit `null` are
   indistinguishable". The new create-side window text has the same problem. It says
   the alias is "used only when window_start_time is absent, null or the empty
   string", but `window_start_time` and `window_end_time` on create are non-nullable
   `type: string`.
   *Fix:* add `nullable: true` to `duration_options`, `durations`, `days_of_week` and
   `days` on both request schemas (the repo's existing spelling for nullable fields).
   On create, either add `nullable: true` to the four window fields or drop "null"
   from their descriptions.

### Responses the client cannot handle

- None found. `getSchedulingLink` gains a 403, but the web calls it only as the
  owner: right after create (`src/api/schedulingLinks.ts:514`) and around its own
  PATCH (`:544`, `:574`). The pause toggle and edit are rendered only for
  `link.is_owner` (`src/pages/Links.tsx:144`, `:155`). A 403 or 404 there is rethrown
  by `withFallback` and shown as the existing toast (`Links.tsx:355`). `uses_count`
  gains `minimum: 0`, and the web reads it as `raw.uses_count ?? 0`
  (`schedulingLinks.ts:110`).

### Consumers affected

- smart-calendar-flow `src/api/generated/*`: drifts from this bundle
  (`openapi-check rc=2`, above). The manifest defers regeneration to "the build stage
  that ships this contract". The factory's rule is that a contract change ships with
  its paired web PR (REGEN=1 web-gate), so that both gates are green before review.
  This is not a shape defect; it is listed so it is not forgotten.
- clockwise-like `mcp/`: no caller of `/api/scheduling-links` or `/api/book` (grep:
  0 files). Unaffected.
- clockwise-like `e2e/tests/scheduling-links.spec.ts`, `public-booking.spec.ts`,
  `seam.spec.ts` and `time-format.spec.ts`: all use reusable links except the seeded
  exhausted one (`e2e/seed/seed.sql:239-242`, `:264-268`). Its seeded booking is
  `confirmed` and still counts. `seam.spec.ts:297` and `public-booking.spec.ts:187`,
  `:278` hard-delete bookings in teardown. That is harmless on reusable links; see
  Non-blocking 3.
- clockwise-like backend tests: no test GETs a link by id as a non-owner (grep of
  `api/*_test.go`), so the new 403 breaks none.

### Contract/implementation divergences

1. **The "end must be after start" description is false for part of its stated
   range.** `createPublicBooking` 400 says "'end must be after start' is reached when
   no `end` is sent and the link's duration is so large (>= 153,722,868 minutes) that
   start + duration overflows to before start". `time.Duration(n) * time.Minute` wraps
   modulo 2^64, so the sign of the wrapped value alternates. Probe output:
   ```
   153722868 min -> end=1734-07-24T10:13:26Z after=false
   307445734 min -> end=2026-11-02T09:59:26Z after=false sub=-33.709551616s
   307445735 min -> end=2026-11-02T10:00:26Z after=true  sub=26.290448384s
   400000000 min -> end=2202-10-25T05:05:26Z after=true
   461168602 min -> end=1734-07-24T10:12:52Z after=false
   ```
   Take `POST /api/book/{slug}` with `{"name":"A","email":"a@example.com",
   "start":"<future>"}` on a reusable link whose only option is `307445735`. It is not
   answered "end must be after start", because `!end.After(start)` is false at
   `handlers_booking.go:285`. Reading the code, the request goes on to the notice,
   overlap and confirm steps with an end 26 seconds after the start. The column is
   `INTEGER[]` and validation only requires `> 0`
   (`handlers_scheduling_links.go:219-223`), so such options can be stored. Spec §2.5
   has the same over-general wording. AC-20a's 200,000,000 falls inside a negative
   band and is correct.
   *Fix:* reword to: "reached when no `end` is sent and start + the duration
   overflows to at or before start. That happens for some duration options of
   153,722,868 minutes or more (for example 153,722,868–307,445,734) but not all:
   307,445,735–461,168,601, for example, wrap to an end after start and are not
   rejected by this check". No AC changes. Spec §2.5 should get the same correction in
   round 6.

### Uncertainties not actually resolved

- None. The only marker removed is the one on `getSchedulingLink`. Audit decision
  U-08 settles it (`docs/factory/api-audit.md:476`, "it is a bug … add the owner
  check and a 403"), and so does spec D-7. AC-19 pins it (200 for the owner and
  accepted co-hosts, 403 `{"error":"forbidden"}` for a pending invitee or a
  stranger), and AC-2/AC-6a pin the 404 for a deleted link.

### Non-blocking

1. `deleteSchedulingLink` says "every operation that names it by id or slug answers
   the same 404 … for every caller". But create and PATCH naming its slug answer 409
   "slug already exists" (D-3, stated in both 409 descriptions), which tells any
   signed-in user that the slug was once used. Say "every operation that names it by
   id, and every public operation that names it by slug".
2. `createPublicBooking` 400 defines "the stated duration" as "`duration` if present,
   even 0, else `duration_minutes`". In code, `duration: null` decodes to a nil
   pointer and falls through to `duration_minutes` (`handlers_booking.go:231`,
   `:249-255`). "Present" should read "present and not null", matching the "carried"
   wording used for links.
3. `uses_count` is "bookings ever made", and "whether it is stored or derived is not
   part of the contract". The two diverge when a booking row is hard-deleted: a
   derived `COUNT(*)` goes down and a stored counter does not. No product path deletes
   booking rows; only e2e teardown does (`e2e/fixtures.ts:132`), plus the
   `ON DELETE CASCADE` from a hard-deleted link. So this is harmless today. One
   sentence ("booking rows present for the link, cancelled included"), or a note that
   hard-deleted rows are out of scope, would stop the plan having to guess.
4. The spec is still "challenged" (round 6 has not run). This contract must follow
   any AC change from round 6, including the §2.5 correction above.

### Verified clean

- **Deleted and never-existed look the same to every caller.** For each by-id
  operation, the contract checks "deleted" at the existing `link == nil` point: after
  the UUID parse, before the owner check and before the body decode (`updateLink`
  decodes at `:431`, after the owner check at `:426`; `inviteHost` decodes at `:611`,
  after `:604`). Cases tried against the contract text:
  - a stranger's `PATCH {id-of-deleted}` with body `{not json` → 404, the same as a
    random UUID, which today 404s at `:422` before the decode;
  - the owner's `PATCH {"active":true}` on a deleted link → 404;
  - the owner's `DELETE`, twice → 204, then 404;
  - the owner's `POST /{id}/leave` on a deleted link → 404, not 400;
  - a pending invitee's leave on a deleted link → 404, and the host row is kept;
  - accept or decline on a deleted link, on all four paths → 404, with the same body
    as PAC-30's no-host-row 404.

  No status or body tells deleted apart from never-existed, so AC-6a holds as
  written.
- **The 403 on `getSchedulingLink` leaks nothing.** Its body is the shared
  `BookingError`, described as exactly `{"error":"forbidden"}`. The 404 comes first,
  so a stranger cannot tell a deleted link from one that never existed. Declined
  hosts are refused too. That matches `ListSchedulingLinksByUser`
  (`storage/scheduling_links.go:120-138`: owner, or `status = 'accepted'`), which the
  contract names as the set of readers.
- **Alias precedence on PATCH, non-null values.** For `{"duration_options":[30],
  "durations":[45],"days_of_week":[1],"days":["fri"]}` the contract says durations
  [30] and days [1], the same as create (`:157-175`). For `{"duration_options":[],
  "durations":[45]}` it says 422 "at least one duration is required"
  (`validateSchedulingLink`, `:217`) and the link is unchanged. The text matches
  AC-14 and AC-14a; the null case is the wrongly forbidden request 1 above.
- **The window aliases** are described as they behave today on both verbs (create
  `:176-188`, PATCH `:456-465`), which is consistent with D-10.
- **`uses_count` and the 410.**
  - The 410 text matches `linkExhausted` (`handlers_booking.go:65-73`): single_use at
    `>= 1`, recurring with a non-null `max_uses`.
  - Conflict checks still exclude cancelled bookings (`HasOverlappingBooking`,
    `storage/scheduling_links.go:388-395`).
  - The bookings list includes cancelled rows (`GetBookingsByLink` has no status
    filter, `:397-401`).
  - Busy time ignores `active` (`:426-452`).
  - The web's `isLinkExhausted` (`schedulingLinks.ts:352-355`) reads the same field
    with the same rule, so C-3's rejection of a second field holds.
- **C-2** (no `status` or `deleted_at` field): a `deleted` value could never appear
  in a response, and PATCH could then express a second way to delete. The rejection
  has a real reason.
- **Manifest `acceptanceTests` against spec §4.** AC-1 to AC-20a are all present and
  none is narrowed. AC-5 covers both spellings of the invite list, all four
  accept/decline paths, and leave by an accepted co-host, a pending invitee and the
  owner. AC-19 covers a pending invitee and a stranger.
- **Findings the manifest claims to resolve.** Each has a matching description or
  response in the fragment:
  - API-006: the 403, with the x-uncertain removed;
  - API-049: the PATCH descriptions;
  - API-050: lists, by-id paths and invites;
  - API-051: `uses_count`;
  - API-073: the 400 list, unchanged.
- **Generated Go.** The only shape change in `backend/api/gen` is
  `GetSchedulingLink403JSONResponse`. It builds and passes vet, and no handler
  implements the generated interface yet (MIGRATION.md: 0 generated).

### Owner questions (not blocking)

- OWNER: C-1. A stranger who holds a live link's UUID gets 403, and so learns that it
  exists, rather than 404. This is consistent with PATCH/DELETE today and with U-08,
  and is recorded as assumed by the factory.
- OWNER: OQ-1. The owner can no longer reach a deleted link's bookings through the
  API.
- OWNER: OQ-2. Deleted links and their bookers' data are kept indefinitely.
