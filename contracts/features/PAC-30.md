# Contract — PAC-30: co-host accept, decline and leave stop reporting success for a non-host

- **Linear issue**: [PAC-30](https://linear.app/paceday/issue/PAC-30)
- **Spec**: none (see §0)
- **Stage**: contract-author, then contract-challenger
- **Fragment edited**: `contracts/openapi/paths/booking.yaml` (plus its companion
  `booking.notes.md`, "Accept/decline are silently no-op on a miss")
- **Bundle**: regenerated with `python scripts/openapi_assemble.py`; generated Go and
  `MIGRATION.md` by the api gate (`make openapi` in an LF clone). §8 records what was
  run and what it printed.
- **Manifest**: `contracts/features/PAC-30.yaml`

---

## 0. There is no spec, and the challenger should say so

There is no `docs/specs/PAC-30.md`. PAC-30 is a defect from the API audit (finding
API-020, batch C5 in `docs/factory/api-audit.md`). The contract-author definition
says to stop when there is no accepted spec. I went ahead on the precedent of PAC-21
and PAC-36 (`contracts/features/PAC-36.md` §0):

- the run that started this stage asked for the contract;
- it supplied acceptance criteria that are already Given/When/Then and falsifiable;
- each criterion maps to one handler branch whose current behaviour I read.

This is a deviation from the factory, recorded here rather than hidden. The decisions
in §4 are what a spec would have had to settle. With no spec file to hold a Decisions
section, they are recorded in §4 and in the manifest as "assumed by the factory,
pending owner confirmation".

### Scope: one finding, not three

The issue body names three findings and three pieces of work. The criteria this stage
was given cover one of them:

| Finding | State on `main` | Here? |
|---|---|---|
| API-020 (no `RowsAffected` check; accept/decline/leave succeed for a non-host) | Confirmed | **Yes** |
| API-048 (`POST /{id}/hosts` demotes an accepted host to pending) | Fixed by PAC-61 | Already done; the contract already says so |
| API-069 (duplicate `/host-invites/{id}/accept\|decline` spellings) | Confirmed | No. See §3 and D-4 |

## 1. What changes at the HTTP boundary

| Operation | Before | After |
|---|---|---|
| `acceptSchedulingLinkHostInvite` `POST /api/scheduling-links/host-invites/{id}/accept` | `UPDATE ... WHERE link_id=$1 AND user_id=$2`; zero rows is not checked. A caller with no host row gets `200 {"status":"accepted"}` and nothing changes. | Zero rows updated is **404 `{"error":"not found"}`** and nothing is written. Any existing host row still gives 200. |
| `declineSchedulingLinkHostInvite` `.../host-invites/{id}/decline` | Same, `200 {"status":"declined"}`. | Same 404 rule. |
| `acceptSchedulingLinkInviteByLink` `POST /api/scheduling-links/{id}/accept` | Same handler as the first row. | Same 404 rule. |
| `declineSchedulingLinkInviteByLink` `POST /api/scheduling-links/{id}/decline` | Same handler as the second row. | Same 404 rule. |
| `leaveSchedulingLink` `POST /api/scheduling-links/{id}/leave` | `DELETE ... WHERE`; zero rows is not checked. A non-host non-owner gets 204. | Zero rows deleted is **404 `{"error":"not found"}`**. The earlier branches are unchanged: 400 invalid id, 404 no link, 400 owner cannot leave link. |

No operation is added or removed, no request body changes, no route moves. The only
schema touched is `SchedulingLinkInviteResponse`, description only ("the only 200
body", where it said "the only body").

## 2. Why the interface is shaped this way

**404, not 403.** The caller is authenticated and the operation is keyed on their own
user id; the thing that is missing is the resource "my host row on this link". 403
would say "it exists and you may not", which is false for a random UUID and would
also confirm that a link id exists. The rest of the fragment already uses
`404 {"error":"not found"}` for a missing link, so the client needs no new error
shape.

**One 404 for "no link" and "no host row" on accept/decline (D-1).** The cheapest
correct implementation is the existing UPDATE plus a `RowsAffected` check, which
cannot tell the two apart. Telling them apart would need an extra lookup and would
let any authenticated user probe whether a link UUID exists. That widens what a
caller can learn, so the conservative choice is not to. `leaveSchedulingLink` already
distinguishes "no link" (404) from "you are the owner" (400) before the DELETE; that
order is kept (D-3), and both of its 404s use the same body, so leave still reveals
nothing beyond what it did before. In fact it reveals less: before, a non-host got
204 for an existing link and 404 for a missing one.

**Any existing row matches, whatever its status (D-2).** The criteria ask that a
non-host is refused and that an invited host can accept. They do not ask to tighten
transitions, and doing so would change behaviour for users who are hosts today (an
accepted host re-accepting from a second tab, a declined host changing their mind).
The contract therefore says outright that accepting twice is 200 both times and that
a declined host may still accept. Whether that is right is a product question, filed
as a follow-up.

**The owner is a host row too.** The owner is inserted as an `accepted` host when the
link is created, so the owner's accept/decline still matches a row and returns 200.
That is today's behaviour and the criteria do not touch it; the contract states it
so nobody mistakes it for a PAC-30 regression. The consequence (an owner who
declines drops out of the accepted-hosts set) is a follow-up, not a quiet addition.

**"No change" is part of the contract, not just the status.** Each 404 description
says "nothing is written" / "nothing is deleted". AC-1 asserts the rows of the link
are byte-for-byte the same after the call, so an implementation that wrote first and
then answered 404 would fail.

## 3. Alternatives rejected

- **Keep 200/204 and document it.** That is the defect: the web shows "Invite
  accepted" while the user has joined nothing. Rejected by the criteria.
- **403 for a non-host, 404 for a missing link.** Lets any signed-in user enumerate
  link ids, and needs an extra read the fix does not otherwise need. Rejected (D-1).
- **409 for "already accepted" / "already declined".** Tightens transitions no
  criterion asks for and turns today's harmless repeated accept into an error toast.
  Rejected (D-2).
- **Deprecate the `/host-invites/{id}/accept|decline` spelling in the same contract**
  (the issue body and audit batch C5 suggest it, API-069). The spelling the web uses
  is exactly that one (`src/api/schedulingLinks.ts`). Deprecating it pushes a web
  change no criterion asks for; deprecating the other spelling is the opposite of what
  the audit wants. Either is an owner decision. The user harm API-069 causes (a wrong
  id silently "succeeds") is removed by the 404 this contract adds, so the decision
  can wait without leaving the harm in place. Deferred (D-4).
- **A `{status}` field or the updated host row in the 404 body.** The shared
  `BookingError` shape is what every other 404 in the fragment uses; a bespoke body
  would be a new shape for the client to render for no reader. Rejected.

## 4. Decisions (assumed by the factory, pending owner confirmation)

- **D-1** On accept/decline, "no such link" and "no host row on the link" give the
  same `404 {"error":"not found"}`. Assumed by the factory, pending owner confirmation.
- **D-2** Any existing host row matches accept/decline, whatever its status. No
  transition is refused. Assumed by the factory, pending owner confirmation.
- **D-3** Leave keeps its check order and its 400 for the owner; only the non-host
  case changes, from 204 to 404. Assumed by the factory, pending owner confirmation.
- **D-4** PAC-30 resolves API-020 only. API-069 is a follow-up; API-048 was fixed by
  PAC-61. Assumed by the factory, pending owner confirmation.
- **D-5** A deactivated link is not "no link". `DELETE /api/scheduling-links/{id}` is a
  soft delete (`active=false`) that keeps the host rows, and neither the invite list nor
  accept/decline/leave filter on `active`. So an invite on a deleted link is still listed
  and accept/decline on it return 200 and update the row, as before PAC-30. PAC-30 does
  not add a 404 for inactive links: no criterion asks for it and no owner has answered
  (follow-up `inactive-link-invites`). Assumed by the factory, pending owner confirmation.

None of these deletes user data or widens who can see data. D-1 narrows what leave
reveals.

## 5. Consumers, and what happens to them

Searched: `smart-calendar-flow/src`, `clockwise-like/mcp`, `clockwise-like/e2e/tests`,
`clockwise-like/backend`.

| Consumer | Breaks? | What happens |
|---|---|---|
| web `src/api/schedulingLinks.ts` `acceptInvite` / `declineInvite` | No | Called with `invite.link_id` from the invite list, which always has a host row: 200 as today. A stale invite whose host row is gone (the owner removed the host, or the caller already left) now rejects with `ApiHttpError` 404; `withFallback` rethrows it and `src/pages/Links.tsx` shows `toast.error` instead of a false "Invite accepted". That is the fix. An invite on a link the owner deleted is **not** covered: the delete is soft, the host row survives, the invite is still listed and accept/decline return 200 ("Invite accepted"), unchanged (D-5). |
| web `src/api/schedulingLinks.ts` `leaveLink` | No | Only offered for links in the `shared` list (caller is an accepted host). A repeated click or stale list now shows an error toast instead of "You left the link". |
| web `src/api/generated` | No | Five operations gain a 404 response. Regenerated after merge. |
| backend `handlers_scheduling_links_test.go` flow test | No | Invites, accepts and leaves as a real host (200, 204), owner leave 400: all unchanged. |
| backend tests calling `storage.RespondToHostInvite` directly (`handlers_scheduling_links_test.go:334,369`, `storage/scheduling_links_test.go:160`) | Compile only | If the plan makes the storage call report whether a row matched, these call sites are updated, never deleted. |
| `mcp/`, `e2e/tests` | No | No caller of these operations. |

## 6. Acceptance criteria, as they apply at the HTTP boundary

- **AC-1** Given a user with no host row on an existing link, When they accept,
  decline (both spellings of each) or leave, Then 404, `Content-Type:
  application/json`, body exactly `{"error":"not found"}`, and the link's
  `scheduling_link_hosts` rows are unchanged (same rows, same `status`, same
  `responded_at`). Also covered: leaving twice (second call 404, no change), and the
  invitee accepting with the host-row `id` from `POST /{id}/hosts` instead of the link
  id (404, their pending row unchanged) — the exact mistake API-020 describes.
- **AC-2** Given an invited host (pending row from `POST /{id}/hosts`), When they
  accept through `/host-invites/{linkId}/accept` (and, separately, `/{linkId}/accept`),
  Then 200, body exactly `{"status":"accepted"}`, and their row is `accepted` with
  `responded_at` set.
- **AC-3** api-gate prints rc=0 on the final commit.

All three are expressible at the boundary. AC-1 and AC-2 are handler tests against
the real test database (testcontainers), because "no change" has to be read back from
the table. No e2e scenario is required: the web journey that reaches these routes
only ever sends a valid link id for a real host, so its happy path is unchanged.

## 7. `x-uncertain` and audit findings

- The five operations carry no `x-uncertain`. Nothing to resolve.
- `getSchedulingLink` (same fragment) carries an `x-uncertain` on its missing
  authorization. It is not edited by this contract, so factory §7 rule 1 does not
  apply.
- Finding resolved by this contract: **API-020**. The implementation PR updates its
  status in `docs/factory/api-audit.md`.

## 8. What was run

- `python scripts/openapi_assemble.py` then `--check` on the host:
  `OK: bundle is in sync (98 paths)`, 119 operations, `x-uncertain: 13`.
- `api-gate.sh factory/pac-30-contract` on the first contract commit (`b443519`):
  `make openapi` regenerated `backend/api/gen/paceday.gen.go` (the embedded spec,
  one schema comment and five strict-server `...404JSONResponse` types), committed
  with this revision. Every other step printed rc=0: openapi-check (bundle in sync,
  MIGRATION.md covers 119 operations, gen matches), backend and mcp lint, backend and
  mcp test, backend coverage 54.9% against a 43% floor, mcp 84.0% against 75%.
- The gate is re-run on the final commit of this branch. Its output is in the
  Linear comment on PAC-30.

---

## Challenge — 2026-10-26

Reviewed by `contract-challenger` on the committed state of `factory/pac-30-contract`
(`a546531`). Inputs: the fragment diff, the bundle, this file and the manifest, the
PAC-30 Linear issue body, API-020/API-048/API-069 in `docs/factory/api-audit.md`, the
handlers and storage functions, and the web consumers. Not read: the author's reasoning
beyond this file.

**Verdict**: accept-with-changes (one blocking finding, B-1: a consumer statement that
the code and the fragment contradict)

### Requests wrongly permitted

None found at the schema level. No request body is involved; `{id}` is the shared
`SchedulingLinkIdPath` (uuid), and the invalid-id 400 is unchanged and still matches
`uuid.Parse` at `handlers_scheduling_links.go:661` and `:683`.

### Responses the client cannot handle

None. The new 404 is `BookingError` `{"error":"not found"}` with
`Content-Type: application/json` (`writeError`, `handlers_settings.go:388-391`).
In the web, `requestApi` turns a non-2xx into `ApiHttpError` (`src/api/client.ts:428`),
`withFallback` rethrows it because it is not `ApiUnreachableError`
(`client.ts:512-524`), and `apiErrorMessage` renders `data.error`, so the toast reads
"not found" (`client.ts:372-377`; `src/pages/Links.tsx:318,327,345`). Nothing maps
over or dereferences the 404 body.

### Consumers affected

- smart-calendar-flow `src/api/schedulingLinks.ts:483-497` (`acceptInvite`,
  `declineInvite`) and `:606-612` (`leaveLink`): error toast instead of a false
  success on a miss. No break.
- clockwise-like `mcp/`, `e2e/tests`: grepped for `host-invites`, `/accept`,
  `/decline`, `/leave`; no caller (the only hit is inside `e2e/node_modules`). No break.
- **B-1 (blocking): the stated effect on the web consumer is wrong for a deleted link.**
  The manifest (`consumersAffected[0].detail`) and section 5 of this file say: "A stale
  invite (link deleted, or the owner removed the host) now rejects with ApiHttpError
  404". For "link deleted" this is false:
  - `DELETE /api/scheduling-links/{id}` is a soft delete,
    `UPDATE scheduling_links SET active = false` (`storage/scheduling_links.go:159-163`),
    and this fragment's own `deleteSchedulingLink` says so (`booking.yaml:606-613`).
    The `scheduling_link_hosts` row survives (the `ON DELETE CASCADE` at
    `migrations/009_scheduling_links.up.sql:18` never fires).
  - `GET /api/scheduling-links/host-invites` still lists that pending invite:
    `GetPendingInvitesForUser` has no `active` filter (`storage/scheduling_links.go:300-304`)
    and neither has `GetSchedulingLinkByID` (`:108-117`), so `listHostInvites`
    (`handlers_scheduling_links.go:635-655`) emits it.
  - Payload: owner creates link L, invites B (pending row), owner
    `DELETE /api/scheduling-links/L` -> 204. B loads `/links`, the banner shows the
    invite, B clicks Accept (`Links.tsx:256`) ->
    `POST /api/scheduling-links/host-invites/L/accept`. Under this contract the UPDATE
    matches B's row: **200 `{"status":"accepted"}`**, toast "Invite accepted", and B is
    now an accepted host of a deactivated link. Not 404.

  The plan and the implementer will read the manifest as a promise that this case is
  fixed. Fix (contract text only, no shape change):
  1. In the manifest `consumersAffected[0].detail` and in section 5, drop "link deleted"
     from the 404 case and state that an invite on a deactivated link is still listed
     and still accepts/declines with 200.
  2. In the accept/decline descriptions (both spellings) add one sentence: "A
     deactivated link (`active=false`, see `deleteSchedulingLink`) keeps its host rows,
     so accept/decline on it match and return 200." The 404 wording "no link has that
     id" is then unambiguous.
  3. Record that as a Decision (D-5, assumed by the factory, pending owner confirmation)
     and add a follow-up (the invite list and accept/decline ignore `active`). Do not
     widen PAC-30 to a 404 on inactive links without an owner answer: no criterion
     asks for it.

### Contract/implementation divergences

None besides B-1. The 404s are the behaviour the implementation PR adds; the "Before"
column of section 1 matches the handlers today (`handlers_scheduling_links.go:659-706`;
no `RowsAffected` check in `storage/scheduling_links.go:320-333`). The documented leave
order (400 invalid id, 404 no link, 400 owner, then the DELETE) matches `:681-706`.

### Uncertainties not actually resolved

None claimed (`resolvesUncertain: []`). The two `x-uncertain` markers in `booking.yaml`
(`getSchedulingLink` at :515, a conference schema at :1831) are on an operation and a
schema this feature does not edit, so factory section 7 rule 1 does not apply.

### Non-blocking

- N-1 There is no `docs/specs/PAC-30.md`. The author says so in section 0; recorded
  here as the finding section 0 invites. The acceptance criteria live only in the
  manifest and this file.
- N-2 Scope is narrower than the issue body. PAC-30's "Work" names three items; the
  contract does API-020 only, API-048 is already fixed by PAC-61 (the contract says
  so), and API-069 is deferred (D-4). The deferral is explicit, not silent, but the
  parent issue cannot be closed by this stream; API-069 needs its own Linear issue.
- N-3 D-1 ("does not reveal whether a link exists") holds for these five operations,
  but `getSchedulingLink` already answers 200/404 to any authenticated caller (its
  `x-uncertain`, `booking.yaml:510-517`). Better wording: "these operations add no new
  existence oracle".
- N-4 The owner can decline their own host row and gets 200 (follow-up
  `owner-decline`). Stated in the contract, not a PAC-30 regression; agreed it stays
  out of scope.

### Verified clean

- `python scripts/openapi_assemble.py --check` -> `OK: bundle is in sync (98 paths)`, rc=0.
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` ->
  `14399e6ae15234fc8a0151c90197a8227013d4f29b9a65b74cce668b958908ed`, equal to
  `contractHash` in the manifest.
- `api-gate.sh factory/pac-30-contract` on `a546531`: `make openapi rc=0`,
  `regenerated: none`, `openapi-check(api) rc=0` (bundle in sync, MIGRATION.md covers
  119 operations, gen matches), backend lint/test rc=0, mcp lint/test rc=0, backend
  coverage 54.9% (floor 43%) rc=0, mcp coverage 84.0% (floor 75%) rc=0.
- The API-020 mistake: `POST /api/scheduling-links/host-invites/<host-row id>/accept`
  -> UPDATE `WHERE link_id=<host-row id>` matches nothing -> 404, nothing written.
  Correct.
- "Removed by the owner": `RemoveLinkHostsExcept` hard-deletes the row
  (`storage/scheduling_links.go:248-258`), so a later accept/decline/leave is 404.
  Correct.
- Leave as owner (400 before any DELETE), leave twice (second DELETE removes 0 rows ->
  404), leave on a random UUID (404 from the lookup): all as documented. Leave no
  longer distinguishes "existing link, not a host" from "no link" (both 404), so the
  claim that leave reveals less than before holds.
- Cross-user write: both statements key on `user_id = $2` from the session; the 404
  does not change that. Nobody sees more data, nothing is deleted that was not deleted
  before.
- Accept twice, or declined then accept (D-2): the UPDATE matches any status, so 200
  as the contract says.
- AC-1..AC-3 match the criteria in section 6 and each is falsifiable (the row-level
  "unchanged" read-back rules out write-then-404).
- `SchedulingLinkInviteResponse` change is description only; `required` and
  `additionalProperties` are unchanged.

---

## Revision 2 — 2026-10-26 (answers challenge round 1)

### B-1: a deleted link does not give 404 — accepted, fixed in the text

The challenger is right. `DELETE /api/scheduling-links/{id}` is a soft delete
(`UPDATE scheduling_links SET active = false`, `backend/storage/scheduling_links.go`
`DeleteSchedulingLink`; `booking.yaml` `deleteSchedulingLink` says the same). The host
rows survive. `GetPendingInvitesForUser` filters on `user_id` and `status = 'pending'`
only, and `RespondToHostInvite` keys on `link_id` and `user_id` only, so neither looks at
`active`. An invite on a deleted link is still listed and accept/decline on it update
the row and return 200. Re-read on this branch before writing this answer.

Changes (text only, no shape change, no behaviour change beyond revision 1):

1. Manifest `consumersAffected[0].detail` and section 5: "link deleted" removed from
   the 404 case. The 404 case is now "the host row is gone (the owner removed the host,
   or the caller already left)". Both now say explicitly that an invite on a deleted
   link is still listed and accept/decline return 200 ("Invite accepted"), unchanged.
2. `booking.yaml`: the descriptions of `acceptSchedulingLinkHostInvite`,
   `declineSchedulingLinkHostInvite`, `acceptSchedulingLinkInviteByLink` and
   `declineSchedulingLinkInviteByLink` each gain: a deactivated link (`active=false`,
   see `deleteSchedulingLink`) keeps its host rows, so accept/decline on it match and
   return 200 (PAC-30 D-5). The 404 wording "no link has that id" therefore means no
   row at all.
3. **D-5** added to section 4 and to the manifest `decisionsAssumed`, assumed by the
   factory, pending owner confirmation. Follow-up `inactive-link-invites` added to the
   manifest: the invite list and accept/decline/leave ignore `active`; whether they
   should hide the invite or answer 404 needs an owner decision. PAC-30 is not widened
   to a 404 on inactive links: no acceptance criterion asks for it, and D-5 neither
   deletes user data nor widens who can see data.

`contractRevision` is now `2` and `contractHash` is the hash of the reassembled bundle.

### Non-blocking findings

N-1..N-4 are noted and not changed in this revision. N-2 (API-069 needs its own Linear
issue) and N-3 (D-1 wording: "these operations add no new existence oracle") are left
for the owner and the plan stage; D-1's substance is unchanged.

### What was run for revision 2

- `python scripts/openapi_assemble.py` -> wrote the bundle, 98 paths, 119 operations,
  `x-uncertain: 13`, rc=0; `--check` -> `OK: bundle is in sync (98 paths)`, rc=0.
- `api-gate.sh factory/pac-30-contract` on `aecab1e` (the fragment change):
  `make openapi rc=0`, `regenerated: none`, `openapi-check(api) rc=0` (bundle in sync,
  MIGRATION.md covers all 119 operations, `backend/api/gen` matches the contract),
  backend lint rc=0, mcp lint rc=0, backend test rc=0, mcp test rc=0, backend coverage
  54.9% (floor 43%) rc=0, mcp coverage 84.0% (floor 75%) rc=0.
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` ->
  `0c1cfaa0b9011d1c9ccf5b08e5919ed579ec918700dd368b2a4aa06c40bf8097`.

---

## Challenge (round 2) — 2026-10-26

Reviewed by `contract-challenger` on the committed state of `factory/pac-30-contract`
(`260fd62`, contract revision 2). Inputs: the fragment diff against `origin/main`, the
bundle, this file and the manifest, the handlers (`backend/api/handlers_scheduling_links.go`
`listHostInvites`, `respondToInvite`, `leaveLink`), the storage functions, `routes.go`, and
the web consumers. Not read: the author's reasoning beyond this file.

**Verdict**: accept (B-1 is resolved; no new blocking finding; three non-blocking notes)

### Requests wrongly permitted

None. Attacked:

- `POST /api/scheduling-links/host-invites/L/accept` by invitee B after the owner ran
  `DELETE /api/scheduling-links/L` (L now `active=false`). `RespondToHostInvite` keys on
  `link_id` and `user_id` only (`storage/scheduling_links.go`, `UPDATE scheduling_link_hosts
  ... WHERE link_id = $1 AND user_id = $2`), so B's row matches -> 200
  `{"status":"accepted"}`. The revised descriptions (all four spellings) now say exactly
  that (D-5), and the 404 text "no link has that id" is qualified as "no row at all, not an
  inactive one". Contract and handler agree.
- Same as B (accepted host) on a deactivated link, then `POST /api/scheduling-links/L/accept`
  again -> the row matches whatever its status; PostgreSQL reports a matched row as affected
  even when `status` is unchanged, and `responded_at = NOW()` changes anyway, so the 200 of
  D-2 holds under a `RowsAffected` check.
- `POST /api/scheduling-links/<random uuid>/decline` -> UPDATE matches nothing -> 404
  `{"error":"not found"}`, nothing written. As documented.

### Responses the client cannot handle

None. The only new response is `404 BookingError`. Re-checked on web `main` (`3bbbe04`):
`withFallback` rethrows every non-unreachable error (`src/api/client.ts:510-524`),
`apiErrorMessage` renders `data.error` (`client.ts:372-377`), and the three mutations in
`src/pages/Links.tsx:312-346` route it to `toast.error`. Nothing dereferences the 404 body.

### Consumers affected

- smart-calendar-flow `src/api/schedulingLinks.ts:483-497,606-612` via `src/pages/Links.tsx`:
  error toast instead of a false success on a miss. No break. The revised manifest
  `consumersAffected[0].detail` now correctly excludes the deleted-link case.
- clockwise-like `mcp/`, `e2e/` (excluding `node_modules`): grepped for `host-invites`,
  `/accept`, `/decline`, `/leave` on scheduling-link paths; no caller. No break.

### Contract/implementation divergences

None. Revision 2 changes descriptions only; the shapes, status codes and leave check order
are as reviewed in round 1 and still match `leaveLink` (400 invalid id, 404 no link, 400
owner, then DELETE).

### Uncertainties not actually resolved

None claimed (`resolvesUncertain: []`); unchanged since round 1.

### Non-blocking

- N-5 `leaveSchedulingLink` does not mention D-5, although section 4 says leave also
  ignores `active`. `leaveLink` looks the link up with `GetSchedulingLinkByID`, which has no
  `active` filter, so a host leaving a deactivated link gets 204, not the "no link" 404.
  Suggested text for the leave description: "A deactivated link (`active=false`) is still a
  link here: a host can leave it (204)." The manifest's D-5 wording names accept/decline
  only; aligning it with section 4 would remove the ambiguity.
- N-6 `contracts/openapi/paths/booking.notes.md` still labels its PAC-30 note
  "contract revision 1" and does not mention D-5. Not part of the bundle; update with the
  next revision or the implementation PR.
- N-7 (web, outside this contract) `Links.tsx` `onError` does not call `invalidateAll()`,
  so after a 404 the stale invite stays in the banner until the next refetch, and a second
  click gives the same error toast. Not a contract defect; worth one line in the plan.
- N-1..N-4 from round 1 stand as recorded (no spec file, API-069 needs its own issue, D-1
  wording, owner can decline their own row).

### Verified clean

- `python scripts/openapi_assemble.py --check` -> `OK: bundle is in sync (98 paths)`, rc=0.
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` ->
  `0c1cfaa0b9011d1c9ccf5b08e5919ed579ec918700dd368b2a4aa06c40bf8097`, equal to the
  manifest's `contractHash` for revision 2.
- `api-gate.sh factory/pac-30-contract` on `260fd62` (the final commit before this
  section): `make openapi rc=0`, `regenerated: none`, `openapi-check(api) rc=0` (bundle in
  sync, MIGRATION.md covers all 119 operations, `backend/api/gen matches the contract`),
  backend lint rc=0, mcp lint rc=0, backend test rc=0, mcp test rc=0, backend coverage
  54.9% (floor 43%) rc=0, mcp coverage 84.0% (floor 75%) rc=0, exit 0.
- B-1 fix: all four accept/decline descriptions, manifest `consumersAffected[0]`, section 5
  and D-5 now agree with `DeleteSchedulingLink` (soft delete), `GetPendingInvitesForUser`
  (no `active` filter) and `listHostInvites` (looks the link up with
  `GetSchedulingLinkByID`, also no `active` filter). D-5 is the conservative choice: a
  deactivated link can be reactivated by the owner (`PATCH ... {active:true}`), and D-5
  neither deletes user data nor widens who can see data.
- Routing: `/host-invites/{id}/accept|decline` (routes.go:160-161) and `/{id}/accept|decline`
  (:167-168) both bind `respondToInvite`, so the four spellings share one 404 rule, as
  the contract says.
