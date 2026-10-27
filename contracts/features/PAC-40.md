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
