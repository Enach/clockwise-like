# Contract — PAC-25: a scheduling link's detail is readable only by its hosts, and a team cannot lose its last owner

- **Linear issue**: [PAC-25](https://linear.app/paceday/issue/PAC-25/add-the-missing-owner-check-to-get-apischeduling-linksid-and-a-last)
- **Spec**: `docs/specs/PAC-25.md`, revision 1 (carried on this branch from
  `factory/pac-25-spec` at `da6905f`; see §0)
- **Stage**: contract-author, then contract-challenger
- **Fragments edited**: `contracts/openapi/paths/booking.yaml` (`getSchedulingLink`),
  `contracts/openapi/paths/teams.yaml` (`removeTeamMember`), and their companions
  `booking.notes.md` (x-uncertain list, item 1) and `teams.notes.md` (A14)
- **Audit**: `docs/factory/api-audit.md` U-08 row and batch B4 note rewritten to the
  resolution this contract ships (the spec's "Inherited scope" requires it)
- **Bundle**: regenerated with `python scripts/openapi_assemble.py`
- **Manifest**: `contracts/features/PAC-25.yaml`

---

## 0. State of the spec

The spec was challenged on 2026-10-27 (verdict accept-with-changes, two blocking
findings) and revision 1 answers every finding. It has not been re-challenged. I
treat it as accepted because the challenge verdict was "accept" with changes and
each change is visibly made in revision 1; a challenger who disagrees should say so
here. The spec is not yet on `main`, so this branch is `main` plus the three spec
commits (a fast-forward from `factory/pac-25-spec`), and the contract commits on top.

## 1. What changes at the HTTP boundary

| Operation | Before | After |
|---|---|---|
| `getSchedulingLink` `GET /api/scheduling-links/{id}` | Any authenticated caller holding the id gets `200` with the full host roster (emails, names, avatars of accepted, pending and declined hosts). Marked `x-uncertain` (U-08). | Only the owner and accepted co-hosts get `200`, body unchanged. Everyone else gets the existing `404 {"error":"not found"}`, identical to an unknown id. No `403`. Marker removed. |
| `removeTeamMember` `DELETE /api/teams/{id}/members/{userId}` | The owner can remove themself; the team is left with no owner. `204`. | **`422 {"error":"the team owner cannot be removed"}`** when the removal would leave zero owners; nothing is written. All other answers unchanged. |

No operation is added or removed, no request shape changes, no schema changes. The
only new declared response is the `422` on `removeTeamMember`.

## 2. Why the interface is shaped this way

**The refused read reuses the existing 404, not a new 403 (spec D-1).** The issue and
U-08 asked for a `403`. A `403` tells a stranger that the id they hold names a real
link; `404` refuses exactly the same people and discloses strictly less. PAC-30 made
accept/decline/leave answer the same not-found for the same resource family for the
same reason. Contract consequence: `getSchedulingLink` keeps the same set of declared
responses (200, 400, 401, 404, 500); only who reaches which changes. The generated
types for this operation change only in descriptions, so the web needs no new branch.

**Readers are the owner and accepted co-hosts (spec D-2).** An accepted co-host
already receives this exact record, roster included, from `GET /api/scheduling-links`
(`storage/scheduling_links.go:130`). Refusing them the detail would protect nothing
and make two reads disagree about who may see a link. The contract says so in the
operation description, and also says the uncomfortable consequence out loud: an
accepted co-host keeps seeing pending and declined invitees' emails.

**The 200 description names who receives it.** It says `is_owner: true` for the
owner, `is_owner: false` with `my_status: accepted` for a co-host. A client can
therefore never receive a 200 with `my_status` absent and `is_owner: false`; before
PAC-25 that was what a stranger saw.

**The check order is part of the contract.** For `getSchedulingLink`: 400, lookup
(500), 404, 200. The reader check has to come after the lookup, so a storage error
still answers 500 rather than a misleading 404. For `removeTeamMember`: 400, then
403 when caller is not target and not an owner, then 422, then 204. The 403 must come
first (spec AC-17): if the 422 came first, a member could name each teammate in turn
and learn who the owner is from which one answers 422.

**The guard counts owners, not roles (spec D-4).** "Would leave zero owners" refuses
the sole owner and allows one of two co-owners to leave. Today the product creates at
most one owner per team, so the two rules are equivalent in practice; the counting rule
is the narrower refusal when that changes. The description states that concurrent
removals must not leave zero owners (D-6) so the plan cannot meet it with a
check-then-delete race.

**The 422 reuses the manager route's wording (spec D-3), not its rule.** One message
for "you cannot remove the owner" across both removal paths. The manager route keeps
its role-based refusal; the description of `removeTeamMember` names the divergence.

**The 422 uses the shared `ErrorResponse` with an `example`, not a new schema (C-1).**
Every other error in `teams.yaml` is `ErrorResponse` with the message in the
description. A dedicated schema with `error: enum: [...]` would make the message
machine-checkable, but it would be the first such schema in the fragment, it would
generate a distinct web type for one string, and the factory has no consumer that
narrows on error text. The `example` puts the exact body in the bundle where AC-15(b)
can assert it.

## 3. Alternatives rejected

- **`403` for a refused link read** (the issue's wording and U-08's recorded
  resolution). Confirms that the id exists. Rejected (D-1); open to the owner as spec
  OQ-6. If the owner chooses it, the contract adds a `403` to `getSchedulingLink` and
  AC-1..AC-3, AC-6 and AC-15(a) change.
- **Owner-only read** (the issue title). Refuses accepted co-hosts a record they get
  from the list anyway. Rejected (D-2); open as spec OQ-7.
- **`409 Conflict` for the last-owner refusal.** 409 describes a conflict with the
  current state that the client can resolve and retry (another owner appears). The
  product has no path to a second owner, so there is nothing to resolve; and the
  manager route already answers 422 for the same refusal, so 409 would give one rule
  two status codes. Rejected.
- **Deleting the team when its sole member-owner leaves.** Data-destroying; the user
  must choose it explicitly through `DELETE /api/teams/{id}`. Rejected (D-5).
- **Making the four owner-only link siblings answer 404 in the same change.**
  Consistent, but changes four documented answers the issue does not mention.
  Deferred (spec OQ-2).
- **Changing the manager route to the counting rule.** Out of scope; the manager
  rule is pinned by AC-18 instead (D-3).

## 4. Decisions (assumed by the factory, pending owner confirmation)

D-1 to D-6 are the spec's, unchanged, and are listed in the manifest. The contract adds
one:

- **C-1** The `422` body reuses `ErrorResponse` with an `example`, not a new
  enum-constrained schema (§2). Assumed by the factory, pending owner confirmation.

None deletes user data or widens who can see data. D-1 and D-2 together narrow who can
read a link's roster.

## 5. Consumers, and what happens to them

Searched: `smart-calendar-flow/src` at `origin/main` `ad52160`, `clockwise-like/mcp`,
`clockwise-like/e2e/tests`, `clockwise-like/backend`.

| Consumer | Breaks? | What happens |
|---|---|---|
| web `src/api/schedulingLinks.ts:514,544,574` | No | All three single-link reads are owner flows (after create; read-before-PATCH in update; after update). Edit and toggle are gated on `is_owner` (`src/pages/Links.tsx:144,155`). A co-host who somehow reached update would get 200 on the read and 403 on the PATCH, as today. |
| web `src/api/teams.ts:388`, `src/components/team/ProtectedHoursTab.tsx:709` | No | The remove control is never shown on the owner's own row, so the web never sends the refused request. A 422 would show the existing error toast (`:668`). |
| web `src/api/generated` | No | `removeTeamMember` gains a `422`; descriptions change on both operations. Regenerated by the paired web PR (REGEN=1 web gate), never by hand. |
| `e2e/tests/scheduling-links.spec.ts`, `e2e/tests/team-invite-analytics.spec.ts` | No | Neither reads a link by id as another user nor calls member removal. |
| `mcp/` | No | Calls neither operation. |

## 6. Acceptance criteria at the HTTP boundary

AC-1..AC-19 are transcribed in the manifest. At this boundary: AC-1..AC-3 and AC-6 are
the `404` description of `getSchedulingLink`; AC-4/AC-5 its `200`; AC-7, AC-8, AC-13,
AC-14 and AC-19 the `422` of `removeTeamMember`; AC-9..AC-12 and AC-17 its unchanged
`204`/`403` and the check order; AC-15 is checkable in the bundle (no `x-uncertain` and
no `403` on `getSchedulingLink`; a `422` with the example body on `removeTeamMember`).
AC-18 concerns `DELETE /api/manager/team/members/{email}`, which this contract does not
edit. AC-19 (concurrency) cannot be expressed in OpenAPI; it is stated in prose and
belongs to an integration test.

## 7. `x-uncertain` and audit findings

- **U-08** resolved: it was a bug. Resolved differently from its recorded resolution
  (owner + accepted co-hosts, refusal `404`); the audit row and batch B4 are rewritten
  on this branch. The bundle's `x-uncertain` count drops from 12 to 11.
- **API-006** and **API-023**: contracted here; their audit status moves to Fixed in the
  implementation PR, when the server behaves as contracted.
- **API-050** (soft-deleted link still readable) stays open; the `404` description
  states it.
- Neither operation carried another `x-uncertain` or open finding.

## 8. What was run

- `python scripts/openapi_assemble.py` then `--check` on the host:
  `OK: bundle is in sync (98 paths)`, 119 operations, `x-uncertain: 11` (12 on `main`).
- `api-gate.sh factory/pac-25-contract` on the first contract commit (`37a30a0`):
  `make openapi` regenerated `backend/api/gen/paceday.gen.go` (embedded spec and the
  strict-server response types for the new `422`), committed as `26269ee`. Every step
  printed rc=0: openapi-check (bundle in sync, MIGRATION.md covers 119 operations,
  gen matches), backend and mcp lint, backend and mcp test, backend coverage 56.8%
  against a 43% floor, mcp 84.0% against 75%.
- The api gate is re-run on the final commit of this branch; its output is in the
  Linear comment on PAC-25.
- **Not run at this stage**: the web gate and the e2e gate. The paired web type
  regeneration (`REGEN=1 web-gate.sh`) is opened by the build stage against this
  branch; no server behaviour has changed yet, so there is nothing for e2e to show.

---

## Challenge — 2026-10-27

Challenger: `contract-challenger`, against `factory/pac-25-contract` at `f056f58`
(committed state only). Read: this file, the manifest, the fragment diff against
`main`, spec revision 1, the handlers and storage named below, the web consumers at
`smart-calendar-flow` `origin/main` `ad52160`, `mcp/` and `e2e/tests`.

**Verdict**: accept-with-changes (one blocking finding, a description change only;
no schema change)

### What was run

- `make openapi-check WEB=/nonexistent` in `golang:1.25-bookworm`, on a clean clone
  checked out at `f056f58`: `OK: bundle is in sync (98 paths)`, `OK: MIGRATION.md
  covers all 119 operations (0 generated)`, `OK: backend/api/gen matches the
  contract`, `SKIPPED: web generated types (no repo at /nonexistent)`,
  `openapi-check(api) rc=0`. The web half was not run (see non-blocking 1).
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` =
  `af80e829…bf32a`, equal to the manifest's `contractHash`.
- Nothing else. No backend test, lint, web gate or e2e gate was run by this review.

### Requests wrongly permitted

None found. Attempts are under "Verified clean".

### Responses the client cannot handle

None found. Owner flows in the web (`src/api/schedulingLinks.ts:514,544,574`) still
get `200`; the only new declared response, the `422` on `removeTeamMember`, is never
sent by any web screen (`ProtectedHoursTab.tsx:709` hides the control on the owner's
own row) and would reach the existing toast at `:668`.

### Contract/implementation divergences

1. **(blocking) `getSchedulingLink`: the check order stops at the link lookup, and
   the `200` promise is contradicted by the roster read the handler already does.**
   The description's order is "`400`, then the link lookup (`500` on a storage
   error), then `404` for no link or a caller who may not read it, then `200`", and
   the `200` says an accepted co-host receives `is_owner: false, my_status:
   accepted` (§2 here: "a client can therefore never receive a 200 with `my_status`
   absent and `is_owner: false`"). Deciding "may read" needs a second read (the
   caller's host row) that the contract gives no storage-error answer for, and the
   roster read the `200` is built from swallows its error:
   `backend/api/handlers_scheduling_links.go:287` `hosts, _ :=
   storage.GetLinkHosts(db, link.ID)`, then `:300-302` substitutes `[]`.
   Constructed case: accepted co-host B sends `GET /api/scheduling-links/{L}`; the
   `scheduling_links` row reads fine and the `scheduling_link_hosts` read fails.
   Through today's `writeLink` the answer is
   `200 {"id":"L",…,"is_owner":false,"hosts":[]}` with no `my_status`, which this
   contract says cannot happen. And because the order names no answer for a failed
   host-status read, an implementation may legally map it to `404` (an outage
   reported to a legitimate host as "not found", indistinguishable from a refusal by
   design) or to the permissive branch (`200` to a stranger: fail-open on the very
   check this feature adds). The contract must pick.
   **Fix (description only)**: in `getSchedulingLink`, make the order "`400`, then
   the link lookup, then the caller's host-status lookup (each `500`
   `{"error":"internal error"}` on a storage error — never `404`, never `200`), then
   `404`, then `200`", and state in the `200` that a failure to read the roster
   answers `500` rather than `200` with `hosts: []`. If the author instead keeps
   today's swallow, the `200` description and §2 must stop promising `my_status:
   accepted` to co-hosts and say that `hosts` may be `[]` on a storage error; the
   first option is the one consistent with the reader rule.

### Consumers affected

All checked; none breaks. Each manifest line reference was opened and matches:

- web `src/api/schedulingLinks.ts:514` (after create), `:544` (read-before-PATCH),
  `:574` (after update): owner-only paths; `src/pages/Links.tsx:144,155` gate
  toggle/edit on `is_owner`.
- web `src/api/teams.ts:380-390` and `src/components/team/ProtectedHoursTab.tsx:709`
  (`isOwner && m.email !== me`): never sends the owner's own removal.
- web `src/api/generated/types.ts` on `origin/main` lists `403` and `500` for
  `removeTeamMember` and no `422`: stale until the paired regeneration
  (non-blocking 1).
- `mcp/`: no reference to `scheduling-links` or `/members/` (grep).
- `e2e/tests`: the only by-id link path is the owner's `/bookings` read in
  `scheduling-links.spec.ts`; nothing calls member removal.

### Uncertainties not actually resolved

1. **U-08** is removed on a factory decision (D-1, D-2), pending owner confirmation
   (OQ-6, OQ-7), not on new evidence about the server. That is acceptable only
   because AC-1..AC-6 pin the chosen behaviour once implemented; until the backend
   PR lands those tests, the marker's removal rests on the decision alone. The audit
   row says so ("pending owner confirmation"), which is the honest state.

### Non-blocking

1. **The paired web regeneration does not exist yet.** No `pac-25` branch on
   `smart-calendar-flow` (`git branch -r`), and web `main`'s `types.ts` has no `422`
   on `removeTeamMember`, so factory §2 gate 4 ("generated code is in sync in both
   repos") is half-checked, as §8 here says. The factory rule is that a contract
   change ships with its web regeneration in the same build; it must exist and
   `REGEN=1 web-gate.sh` must be green before review, not only before land.
2. **AC-15(b) is narrowed to an `example`.** The spec says the `422` *body is*
   `{"error":"the team owner cannot be removed"}`; the bundle carries that string
   only as a non-normative `example` on `ErrorResponse` (C-1), and neither generated
   type constrains it (`RemoveTeamMember422JSONResponse ErrorResponse`). Fine if the
   contract test asserts the example and the Go handler test asserts the body
   (AC-7), which the manifest's AC-15 wording allows; flagged so the plan names that
   test.
3. **Spec revision 1 has not been re-challenged** (§0 says so). Not a contract
   defect; the spec stage's gate, not this one.
4. `toSchedulingLinkDTO` (`:291-293`) leaves a host's `email`/`name` empty when
   `GetUserByID` fails, while `SchedulingLinkHost.email` is `required`. Pre-existing,
   not touched by this contract; mentioned so the blocking fix's author sees it next
   to the roster read.
5. OWNER: D-1 — confirm that a refused link read answers `404` rather than the `403`
   the issue and U-08 asked for (spec OQ-6).
6. OWNER: D-2 — confirm accepted co-hosts may read the detail, including pending and
   declined invitees' emails (spec OQ-7).
7. OWNER: D-5 — confirm a sole owner alone in a team is refused rather than having
   the team deleted for them (spec OQ-8).
8. OWNER: C-1 — confirm the `422` reuses `ErrorResponse` with an example rather than
   an enum-constrained message schema.

### Verified clean

- **`GET /api/scheduling-links/{L}` as a pending invitee, a decliner, a stranger, a
  co-host who left** (`POST …/leave` deletes the row, `storage/scheduling_links.go:324`):
  the contract answers `404 {"error":"not found"}` for all, identical to an unknown
  id, whose body today is exactly that (`handlers_scheduling_links.go:403`). No case
  leaks title, slug or roster.
- **D-2 "same set as the list"**: `ListSchedulingLinksByUser`
  (`storage/scheduling_links.go:130`) is `owner_user_id = caller OR (host row
  accepted)` with no `active` filter, so the detail reader set equals the list set
  including soft-deleted links; the `404` text's API-050 note is accurate.
- **Owner reads**: `createLink` inserts the owner as an `accepted` host
  (`handlers_scheduling_links.go:353`) and `RemoveLinkHostsExcept` never deletes the
  owner's row, so the owner's `200` is unchanged (AC-5).
- **`DELETE /api/teams/{T}/members/{O}` by member M** (AC-17): `403` comes from
  `handlers_teams.go:331-335` before any owner count, so a member cannot probe who
  owns the team; the contract's order matches.
- **Self-removal as a non-owner, removal of a non-member, an owner removing another
  owner of a two-owner team**: `204` in each, consistent with the "would leave zero
  owners" wording.
- **Concurrent O1→O2 and O2→O1 removals**: with D-6's serialisation the second
  answers `403` (caller no longer an owner) or `422`; never zero owners. The
  contract does not over-specify which.
- **Other paths to zero owners**: the only `DELETE FROM team_members` is
  `storage/teams.go:130`; the manager route refuses any owner
  (`handlers_manager.go:358-361`) and its scope requires the caller be an owner
  (`:613-616`), so AC-18 is reachable and D-3's description is accurate. No
  `UPDATE team_members` or user deletion exists. Creation (OQ-4) is declared out.
- **D-5 escape route** exists: `DELETE /api/teams/{id}` is owner-only
  (`handlers_teams.go:154-170`).
- **Manifest vs spec**: AC-1..AC-19 transcribed one to one; no criterion dropped or
  narrowed except AC-15(b) above.
- **Audit**: API-006 and API-023 are the only findings on the two operations;
  API-050 (on DELETE) and API-056 (habits) are correctly listed as not resolved.
