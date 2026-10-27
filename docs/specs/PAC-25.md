# Spec — PAC-25: A scheduling link's host roster is visible only to its hosts, and a team cannot lose its last owner

- **Linear**: https://linear.app/paceday/issue/PAC-25/add-the-missing-owner-check-to-get-apischeduling-linksid-and-a-last
- **Status**: draft (decisions assumed by the factory, pending owner confirmation — see "Decisions")
- **Author**: spec-author
- **Inherited scope**:
  - **Resolved by this spec**: API-006, API-023, `x-uncertain` U-08 (the
    "missing authorization — bug or deliberate?" marker on the single-link read;
    the audit already decided "it is a bug", `docs/factory/api-audit.md:476`).
  - **Inherited, explicitly not resolved**: API-050 (a soft-deleted link is still
    readable by its owner) and API-056 (habit ownership answers `403` and so
    confirms existence). Both touch the same question of what a refusal reveals;
    neither is in this issue's problem. See §5.

> **Evidence note.** Every claim in §2 comes from reading source on `main` at
> `6d751f9` and carries a `file:line`. Nothing here was established by running
> a request. Two claims are inferences from reading and are marked as such.

---

## 0. Scope: why this is one spec

The issue bundles two independent broken access controls. They share no code
and no data, and either could ship alone. They are kept together because the
issue (and audit batch B4, `docs/factory/api-audit.md:549`) groups them as one
review conversation, each is small, and both change the documented answers of
an operation, so they share a contract stage and one paired web type
regeneration. If the challenger prefers a split, the seam is clean: §§1a/2a/3a
and AC-1..AC-6 versus §§1b/2b/3b and AC-7..AC-13.

---

## 1. Problem

### 1a. Anyone who knows a link's id can read who hosts it

A scheduling link is a booking page a Paceday user publishes, optionally shared
with co-hosts. Its detail record carries the full host roster: each host's email
address, name and avatar, and whether each has accepted, is pending or declined.

Every other owner-side operation on a link refuses a caller who is not the
owner. Reading a single link does not: any signed-in Paceday account that holds
the link's internal identifier receives the whole record, roster included. The
people exposed are the link's owner and co-hosts — including invitees who never
accepted, whose email address is disclosed to a stranger merely because someone
once invited them.

The internal identifier is not printed on the public booking page (that page is
addressed by a human-readable slug), so the exposure needs the identifier to
leak first — through a shared screenshot of a URL, a log line, a support
conversation, a browser history on a shared machine. That narrow acquisition
path is why the audit rates it high rather than critical. It is still a
disclosure of personal contact data to someone with no relationship to it,
which PRODUCT.md's fixed point "privacy is not a setting to trade away" rules
out.

### 1b. A team's only owner can walk away and leave it unmanageable

A team has one owner, who alone can invite people, remove people, set protected
hours and delete the team. The owner can remove *themself* from the team, and
nothing stops it. The team then has members and no owner: nobody can invite,
remove, change protected hours, or delete it. The audit records that recovery
needs direct database access (`docs/factory/api-audit.md:281`). Members are
stuck in a team nobody administers, and their protected hours keep applying to
them with nobody able to change them.

The product already has this rule in one place — the manager screen refuses to
remove a team's owner — but the general team-membership operation does not.

---

## 2. Current behaviour

### 2a. Reading one scheduling link

- The handler parses the id, loads the link, answers `404 not found` when there
  is no row, and otherwise writes it — with no comparison between caller and
  owner (`backend/api/handlers_scheduling_links.go:391-407`).
- Writing the link always includes hosts: `writeLink` calls
  `toSchedulingLinkDTO(..., true)` (`:306-309`), which loads every host row
  regardless of status and, for each, the user's email, name and avatar
  (`:286-299`). `is_owner` is computed against the caller (`:281`) and
  `my_status` is set only when the caller is one of the hosts (`:295-297`).
- The sibling owner-side operations all refuse a non-owner with
  `403 {"error":"forbidden"}`: update (`:426`), delete (`:543`, inside
  `deleteLink` at `:527`), list bookings (`:571`, `listBookings` at `:555`),
  invite host (`:604`, `inviteHost` at `:588`).
- The link list returns links the caller owns or co-hosts **with an accepted
  status** — `WHERE sl.owner_user_id = $1 OR (slh.user_id = $1 AND slh.status =
  'accepted')` (`backend/storage/scheduling_links.go:130`) — and includes the
  same host roster for each (`backend/api/handlers_scheduling_links.go:370-387`).
  So an accepted co-host already sees the roster today through the list; a
  pending or declined invitee does not.
- A pending invitee sees the link's id, title, and the owner's name and email
  through the pending-invites list, never the roster
  (`backend/api/handlers_scheduling_links.go:634-656`).
- Precedent on what a refusal reveals: PAC-30 made accept/decline/leave answer
  the same `404 not found` for "no such link" and "you have no host row on it",
  with the explicit reason "so the answer does not reveal whether a link exists"
  (`backend/api/handlers_scheduling_links.go:671-674`). The owner-only siblings
  above still answer `403`, which does reveal existence.
- The contract documents the missing check and carries U-08
  (`contracts/openapi/paths/booking.yaml:508-546`): responses 200, 400, 401,
  404, 500 — no 403.
- The route is registered at `backend/api/routes.go:162`.
- Tests: no Go test issues a GET on a single link (the only GETs in
  `backend/api/handlers_scheduling_links_test.go` are on `/bookings`, `:230`,
  `:245`). The e2e journey `e2e/tests/scheduling-links.spec.ts` creates and books
  a link but does not read it by id as another user. **Nothing protects either
  today's behaviour or the desired one.**
- Web consumer (inference from reading): the web app reads a single link only
  right after creating or updating it, as its owner
  (`smart-calendar-flow/src/api/schedulingLinks.ts:514`, `:544`, `:574` on
  `origin/main` `ad52160`). No web path reads a link the caller does not own.

### 2b. Removing a team member

- The handler parses team id and target user id; **only when caller ≠ target**
  does it require the caller to be an owner; it then deletes the membership row
  unconditionally and answers `204` (`backend/api/handlers_teams.go:317-342`,
  the condition at `:330-336`, the delete at `:337`). A self-removal by the owner
  therefore succeeds.
- `RemoveTeamMember` is a plain `DELETE ... WHERE team_id=$1 AND user_id=$2`
  with no row-count check (`backend/storage/teams.go:129-132`), so removing a
  non-member also answers `204` — the contract documents this as idempotent
  (`contracts/openapi/paths/teams.yaml:717-718`).
- `requireOwner` treats "not a member" and "member but not owner" alike
  (`backend/api/handlers_teams.go:571-577`).
- Roles are `owner` or `member` (`backend/storage/migrations/013_teams.up.sql:12`).
  The only code path that writes `owner` is team creation
  (`backend/api/handlers_teams.go:54`); invite acceptance writes `member`
  (`:305`). There is no operation that changes a role. So (inference) a team
  created through the product has at most one owner, and "the last owner" is
  "the owner". More than one owner can exist only through direct database
  writes, and the e2e seed does not create such a team
  (`e2e/seed/seed.sql:166-172`).
- Team creation discards the error from adding the creator as owner
  (`backend/api/handlers_teams.go:54`), so a team with zero owners can already
  be born. Out of scope (§5), noted because it is the same end state.
- The manager-domain remove refuses to remove a team owner with
  `422 {"error":"the team owner cannot be removed"}`
  (`backend/api/handlers_manager.go:358-361`). That is the existing product
  wording for this rule.
- Owner-only team operations that become unreachable once no owner exists:
  delete team (`backend/api/handlers_teams.go:161`), invite, remove others,
  no-meeting zones (each via `requireOwner`).
- The contract documents the missing guard
  (`contracts/openapi/paths/teams.yaml:700-724`): responses 204, 400, 403, 500 —
  no 422.
- Web consumer: the members sheet shows the remove control only to the owner
  and never on the owner's own row
  (`smart-calendar-flow/src/components/team/ProtectedHoursTab.tsx:709`), and
  surfaces a server error as a toast (`:668`). So no current web screen issues
  the request this spec starts refusing.
- Tests: `backend/api/handlers_teams_test.go` registers the route (`:33`) but
  no test in that file exercises it. **Nothing protects today's behaviour of
  this operation** — including the parts that must not change (§6).

---

## 3. Desired behaviour

### 3a. Scheduling link detail

A link's full detail, roster included, is readable by the people who already
have a working relationship with it: its owner and its co-hosts who have
accepted. That is the same set of people who see the link in their own list
today, so no one loses a view they currently use through the product.

Anyone else — a stranger, a pending invitee, an invitee who declined, a co-host
who has left — gets the same answer they would get for a link that does not
exist, with no part of the link in the response. They cannot tell from the
answer whether the identifier they hold refers to a real link.

What the owner and accepted co-hosts receive is unchanged.

### 3b. Leaving a team

Removing a membership that would leave the team with no owner is refused, the
membership is unchanged, and the caller is told why in the same words the
manager screen already uses. Everything else about removing members is
unchanged: the owner removes members; a member removes themself; a non-owner
cannot remove someone else; removing someone who is not in the team still
succeeds and changes nothing.

An owner who wants to be rid of a team they are alone in can still delete the
team; this spec does not offer a "transfer ownership" path (§5).

---

## 4. Acceptance criteria

The status codes below are the factory's assumed choices (Decisions D-1, D-3);
the contract stage may revisit them only by changing those decisions.

**Scheduling link detail**

AC-1. Given user B has no host row on link L owned by A, when B reads L's
detail, then the answer is the same status and the same body as for a
well-formed identifier that matches no link (today: `404`,
`{"error":"not found"}`), and the body contains no host, no email, no title and
no slug. Fails today (200 with the roster).  [unit — Go handler test]

AC-2. Given user B is invited to co-host L and has not yet answered, when B
reads L's detail, then the answer is as in AC-1.  [unit]

AC-3. Given user B declined the invitation to co-host L, when B reads L's
detail, then the answer is as in AC-1.  [unit]

AC-4. Given user B accepted the invitation to co-host L, when B reads L's
detail, then the answer is a success carrying the full roster, with B's own
status reported as accepted and B not marked as owner — byte-identical to what
B receives today.  [unit]

AC-5. Given A owns L, when A reads L's detail, then the answer is a success
identical to today's, roster included.  [unit]

AC-6. Given two users and one link owned by the first, when the second user —
signed in through the full stack — reads the link by its identifier, then the
answer is as in AC-1; and the owner's read in the same run succeeds.  [e2e]

**Team membership**

AC-7. Given team T whose only owner is O, when O removes themself from T, then
the request is refused as unprocessable (`422`) with the message "the team
owner cannot be removed", and O's membership and role in T are unchanged
afterwards (read back from storage). Fails today (204, row gone).  [unit — Go
handler test]

AC-8. Given team T whose only owner is O and only member is O, when O removes
themself, then the answer is as in AC-7 and the team still exists with O as
owner.  [unit]

AC-9. Given team T with owner O and member M, when M removes themself, then the
answer is `204` and M is no longer a member.  [unit] (pins existing behaviour)

AC-10. Given team T with owner O and member M, when O removes M, then the answer
is `204` and M is no longer a member.  [unit] (pins existing behaviour)

AC-11. Given team T with owner O and members M and N, when M removes N, then the
answer is `403` and N is still a member.  [unit] (pins existing behaviour)

AC-12. Given team T with owner O, when O removes a user who is not a member of
T, then the answer is `204` and T's membership is unchanged.  [unit] (pins
existing behaviour)

AC-13. Given team T with two owners O1 and O2 (constructed directly in storage,
since the product has no way to create one), when O1 removes themself, then the
answer is `204` and O2 remains owner; and when O2 then removes themself, the
answer is as in AC-7.  [unit]

AC-14. Given a fresh team created through the full stack by user O, when O
removes themself through the full stack, then the answer is `422` and O is
still listed as the team's owner on a subsequent team read.  [e2e]

**Contract and consumers**

AC-15. Given the published contract, when a reader looks up the single-link
read, then it documents who may read it (owner and accepted co-hosts), documents
that everyone else gets the not-found answer, and carries no `x-uncertain`
marker; and the member-removal operation documents the refusal and its message.
The contract gate fails if either operation still describes today's behaviour.
[contract]

AC-16. Given the regenerated web types, when the web gate runs, then it is green
with no hand edits to generated files.  [contract — web gate]

---

## 5. Explicitly out of scope

- **Making the four owner-only link siblings answer not-found instead of
  forbidden.** Consistency argues for it and API-056 records the same leak
  elsewhere; but it changes four documented answers this issue does not
  mention, and the web app may branch on them. Open question OQ-2.
- **Ownership transfer / promoting a member to owner.** It is the real remedy
  for "the owner wants to leave"; it is a feature, with its own UI, and this
  issue asks only for the guard.
- **Reconciling soft-deleted links (API-050).** The owner can still read a
  deactivated link; unchanged.
- **The team-creation path that can produce a zero-owner team** (owner insert
  error discarded, §2b). Same end state, different cause; should be its own
  issue (OQ-4).
- **Recovering teams already stranded.** Data repair on production is not a
  factory action; OQ-3 asks the owner whether any exist.
- **Narrowing what accepted co-hosts see.** They see the roster today via the
  list; changing that is a product decision, not an access-control bug.
- **Migrating these operations to the generated server interface** — follows
  the normal "touch it" rule in `contracts/openapi/MIGRATION.md`; the plan
  decides.

## 6. What must not change

- Owner and accepted-co-host reads of a link: same status, same body (AC-4,
  AC-5). No existing test protects this; AC-4/AC-5 add the protection.
- `400` for a malformed link id, `401` when unauthenticated, `404` for an
  unknown id, `500` on a storage error on the single-link read.
- The link list, the pending-invites list, accept/decline/leave, and the public
  booking page by slug — not touched. The e2e journey
  `e2e/tests/scheduling-links.spec.ts` must stay green.
- Member removal: AC-9..AC-12 pin the four existing behaviours, none of which is
  tested today.
- The manager-domain owner-protection rule (`handlers_manager.go:358-361`) and
  its tests in `backend/api/handlers_manager_test.go`.
- Team deletion by the owner stays available.

## 7. Rollback

No migration and no stored-data change: both fixes add a refusal before an
existing read or delete. Reverting the backend PR restores today's answers
exactly; reverting the contract PR and its paired web regeneration restores the
documented answers. Nothing is lost by rolling back except the protection. A
partial rollback (backend reverted, contract kept) would make the contract
describe refusals the server no longer gives; revert them together.

## 8. Open questions

- **OQ-1 — Should a pending invitee be able to read the link before
  answering?** Assumed no (D-2): the pending-invites list already gives them the
  title and owner, and the roster includes other people's emails. Owner: product
  owner. Resolve by confirming D-2 or naming what the invitee must see.
- **OQ-2 — Unify the owner-only siblings on not-found?** Today they say
  forbidden; after this spec the read says not-found. Owner: product owner, then
  a separate issue. Resolve by deciding whether existence of a link id is
  itself sensitive.
- **OQ-3 — Are there teams with no owner in production today?** If so they stay
  stranded after this ships. Owner: product owner / release-manager (read-only
  count query at release time). Not resolvable by the factory, which does not
  touch production data.
- **OQ-4 — File the zero-owner-at-creation path as its own issue?** Owner:
  triage.
- **OQ-5 — Concurrent self-removal by two co-owners.** Only reachable with two
  owners, which the product cannot create; the guard is check-then-delete and
  two simultaneous requests could both pass. Owner: plan-author — decide whether
  the guard is a single conditional statement (closing the race for free) or
  accept the race and record it.

---

## Decisions

*Assumed by the factory, pending owner confirmation (2026-10-27). Each is the
most conservative option consistent with PRODUCT.md ("Privacy is not a setting
to trade away") and earlier decisions in `docs/specs/*.md`. None deletes user
data or widens who can see data.*

- **D-1 — Refused link reads answer not-found, not forbidden.** The ticket
  allows either. Not-found reveals nothing about whether the identifier is
  real, matches the PAC-30 precedent for the same resource family, and avoids
  adding a new instance of the API-056 leak. Cost: inconsistent with the four
  owner-only siblings, recorded as OQ-2. Assumed by the factory, pending owner
  confirmation.
- **D-2 — Readers are the owner and accepted co-hosts only.** This is exactly
  the population that sees the link in their list today, so the change removes
  access only from people who had no product path to it. Pending and declined
  invitees are refused (OQ-1). Assumed by the factory, pending owner
  confirmation.
- **D-3 — The last-owner refusal reuses the manager screen's answer**:
  unprocessable (`422`), message "the team owner cannot be removed". One rule,
  one wording, across both removal paths. Assumed by the factory, pending owner
  confirmation.
- **D-4 — The guard is "would leave zero owners", not "caller is an owner".**
  Equivalent today (one owner per team), and it is the narrower refusal if
  multi-owner teams ever arrive: an owner with a co-owner may still leave
  (AC-13). Assumed by the factory, pending owner confirmation.
- **D-5 — A sole owner alone in the team is also refused** (AC-8) rather than
  having the team deleted for them. Deleting a team is a data-destroying action
  the user must choose explicitly; it stays available as its own operation.
  Assumed by the factory, pending owner confirmation.
