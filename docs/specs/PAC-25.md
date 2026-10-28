# Spec — PAC-25: A scheduling link's host roster is visible only to its hosts, and a team cannot lose its last owner

- **Linear**: https://linear.app/paceday/issue/PAC-25/add-the-missing-owner-check-to-get-apischeduling-linksid-and-a-last
- **Status**: challenged — revised (Revision 1 answers the 2026-10-27 challenge); decisions assumed by the factory, pending owner confirmation — see "Decisions"
- **Author**: spec-author
- **Inherited scope**:
  - **Resolved by this spec**: API-006, API-023, `x-uncertain` U-08 (the
    "missing authorization — bug or deliberate?" marker on the single-link read;
    the audit already decided "it is a bug", `docs/factory/api-audit.md:476`).
  - **U-08 is resolved *differently* from its recorded resolution.** The audit
    row says "Add the owner check and a `403` to the spec"
    (`docs/factory/api-audit.md:476`), batch B4 says "new 403/422 responses"
    (`:549`), and the issue says "Adds a 403 and a 422 to the spec". This spec
    instead answers a refused read with not-found (no `403`) and admits accepted
    co-hosts as well as the owner (D-1, D-2). Whichever stage marks U-08
    resolved must rewrite that audit row (and the B4 note) to the resolution
    actually shipped, not leave it saying `403`. If the owner overrules D-1 or
    D-2 (OQ-6, OQ-7), the row follows the owner's answer instead.
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
  (`contracts/openapi/paths/booking.yaml:508-549`): responses 200, 400, 401,
  404, 500 — no 403.
- The issue and API-006 (`docs/factory/api-audit.md:264`) cite the handler as
  `handlers_scheduling_links.go:414-431`; that range is stale. The handler is at
  `:391-407` on `6d751f9`.
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
  wording for this rule. It is a *role* check — it refuses removing any member
  whose role is owner — not a count check. The caller must itself be an owner of
  the team (`managerTeamScope` called with `requireOwner=true`,
  `backend/api/handlers_manager.go:347`, enforced at `:613-616`), so with one
  owner per team the refusal is reachable today only by the owner naming their
  own email. **No test exercises it**: the message appears only at
  `handlers_manager.go:359`, and the only `422` assertion in
  `backend/api/handlers_manager_test.go` (`:101`) is for invalid add-member
  input.
- Owner-only team operations that become unreachable once no owner exists:
  delete team (`backend/api/handlers_teams.go:161`), invite, remove others,
  no-meeting zones (each via `requireOwner`).
- The contract documents the missing guard
  (`contracts/openapi/paths/teams.yaml:700-727`): responses 204, 400, 403, 500 —
  no 422, and no `x-uncertain` marker.
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

That includes something §1a names as the harm: an accepted co-host keeps seeing
the email addresses of pending and declined invitees, through the list today
and through the detail afterwards. This is a conscious choice, not an
oversight: this spec closes access for people with no relationship to the link
and does not narrow what the link's working hosts see (§5, D-2).

### 3b. Leaving a team

Removing a membership that would leave the team with no owner is refused, the
membership is unchanged, and the caller is told why in the same words the
manager screen already uses. Everything else about removing members is
unchanged: the owner removes members; a member removes themself; a non-owner
cannot remove someone else; removing someone who is not in the team still
succeeds and changes nothing.

The order of refusals is fixed: a member who is not an owner and tries to
remove someone else is told they are not allowed, before any check on how many
owners would remain. So a member cannot learn from the answer whether the person
they named is the owner.

No interleaving of removal requests may leave a team with zero owners: two
owners removing themselves at the same time end with at least one of them still
owner (D-6). This is reachable only with two owners, which the product cannot
create today, but the requirement is stated so that it holds when it can.

The manager screen's own refusal to remove an owner keeps working exactly as
today, and is now protected by a test.

An owner who wants to be rid of a team they are alone in can still delete the
team; this spec does not offer a "transfer ownership" path (§5).

---

## 4. Acceptance criteria

The status codes below are the factory's assumed choices (Decisions D-1, D-3);
the contract stage may revisit them only by changing those decisions.

**Which criteria show the change.** Fail today and pass afterwards: AC-1, AC-2,
AC-3, AC-6, AC-7, AC-8, the second half of AC-13, AC-14, AC-15, AC-19. Pass
today and pin behaviour that must survive (each is untested today, so the pin is
itself the protection): AC-4, AC-5, AC-9, AC-10, AC-11, AC-12, the first half
of AC-13, AC-17, AC-18. AC-16 is a gate run, not a behaviour.

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

AC-4. Given link L owned by A with three co-host invitations — B accepted, C
pending, D declined — when B reads L's detail, then the answer is a success in
which B is not marked as owner (today `is_owner=false`), B's own host status is
reported as accepted (today `my_status="accepted"`), the roster lists every
host row on L — B, C and D, each with its status, email, name and avatar — and
the link's own fields equal what was stored. (pins existing behaviour)  [unit]

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

AC-15. Given the published contract bundle, then, as checkable facts:
(a) `getSchedulingLink` carries no `x-uncertain` and declares no `403`;
(b) `removeTeamMember` declares a `422` whose body is
`{"error":"the team owner cannot be removed"}`. Part (a)'s `x-uncertain` half is
enforced by the contract gate's rule; the rest of (a) and all of (b) are facts
of the bundle that a contract test asserts. That each operation's prose now says
who may read the link, that everyone else gets the not-found answer, and when
the removal is refused, is a **reviewer check, not a gate check** — no gate can
detect prose that still describes old behaviour.  [contract]

AC-16. Given the regenerated web types, when the web gate runs, then it is green
with no hand edits to generated files.  [contract — web gate]

**Added in Revision 1**

AC-17. Given team T with owner O and member M, when M removes O, then the answer
is `403` `{"error":"forbidden"}` — not `422` — and O is still a member of T with
the owner role (read back from storage). This fixes the order: the not-allowed
refusal comes before the last-owner refusal, so the answer does not reveal who
the owner is. (pins existing behaviour)  [unit]

AC-18. Given team T whose only owner is O, when O removes their own email
through the manager screen's member removal scoped to T, then the answer is
`422` `{"error":"the team owner cannot be removed"}` and O's membership and role
in T are unchanged (read back from storage). (pins existing behaviour of the
manager rule, untested today)  [unit]

AC-19. Given team T with two owners O1 and O2 (constructed directly in storage)
and no other owner, when O1 and O2 each remove themselves concurrently —
repeated enough times that both orders of completion and true overlap occur —
then after every attempt T has at least one owner, and at most one of the two
requests answered `204` (the other answered as in AC-7). Fails against a
check-then-delete guard with no serialisation.  [unit — integration, real
database]

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
  list — including the emails of pending and declined invitees, which is the
  harm §1a names. Keeping that visible to accepted co-hosts is a **conscious
  choice** (§3a, D-2): changing it would narrow a view working hosts use
  through the product, which is a product decision, not an access-control bug.
  A separate issue may take it up.
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
- Member removal: AC-9..AC-12 and AC-17 pin five existing behaviours, none of
  which is tested today.
- The manager-domain owner-protection rule (`handlers_manager.go:358-361`):
  role-based, refuses removing any owner. **No test protects it today** (the
  earlier draft of this spec claimed tests in `handlers_manager_test.go`; there
  are none — see §2b). AC-18 adds the pin. Its role-based rule is not changed to
  match D-4's count-based guard (D-3).
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
- **OQ-5 — Concurrent self-removal by two co-owners.** *Closed in Revision 1*:
  now a requirement, not an option — no interleaving may leave zero owners
  (D-6, AC-19). How to meet it is the plan's choice.
- **OQ-6 — OWNER: should a refused link read answer not-found (D-1) or the
  `403` the issue and U-08 ask for?** A product call on whether the existence of
  a link id is sensitive; the answer also decides OQ-2. Owner: product owner.
  Resolve by confirming D-1, or by choosing `403` (then AC-1..AC-3, AC-6 and
  AC-15(a) change, and the U-08 audit row keeps its recorded resolution).
- **OQ-7 — OWNER: should the link detail be owner-only, as the issue title
  says, or owner plus accepted co-hosts (D-2)?** Neither breaks a consumer: the
  web reads a link by id only in owner flows
  (`smart-calendar-flow/src/api/schedulingLinks.ts:514,544,574`), and edit and
  toggle are gated on `is_owner` (`src/pages/Links.tsx:144,155`). Owner: product
  owner. Resolve by confirming D-2, or by choosing owner-only (then AC-4 becomes
  a refusal).
- **OQ-8 — OWNER: confirm D-5** — a sole owner alone in the team is refused
  rather than the team being deleted for them. Owner: product owner.

---

## Decisions

*Assumed by the factory, pending owner confirmation (2026-10-27). Each is the
most conservative option consistent with PRODUCT.md ("Privacy is not a setting
to trade away") and earlier decisions in `docs/specs/*.md`. None deletes user
data or widens who can see data.*

- **D-1 — Refused link reads answer not-found, not forbidden. This departs
  from the issue and from U-08.** The issue says "Adds a 403 and a 422 to the
  spec"; U-08's recorded resolution says "Add the owner check and a `403` to the
  spec" (`docs/factory/api-audit.md:476`); batch B4 says "new 403/422
  responses" (`:549`). The departure is deliberate: a `403` tells a stranger
  that the identifier they hold names a real link, which is the API-056 leak;
  not-found reveals nothing, and PAC-30 already chose not-found for the same
  resource family for exactly that reason (`handlers_scheduling_links.go:671-674`).
  Both answers refuse the same people; not-found discloses strictly less, so it
  is the more conservative choice under "privacy is not a setting to trade
  away". Cost: inconsistent with the four owner-only siblings (OQ-2), and the
  U-08 audit row must be rewritten (Inherited scope). Assumed by the factory,
  pending owner confirmation (OQ-6).
- **D-2 — Readers are the owner and accepted co-hosts. This departs from the
  issue title, which asks for an *owner* check, and from U-08's "owner check".**
  Reason: an accepted co-host already receives the identical record, roster
  included, through the link list (`backend/storage/scheduling_links.go:130`,
  `backend/api/handlers_scheduling_links.go:370-387`). Refusing them the detail
  would protect no data — the same record stays one request away — while making
  the two reads disagree about who may see a link. The change therefore removes
  access only from people who had no product path to the record, and neither
  widens nor narrows what working hosts see. Pending and declined invitees are
  refused (OQ-1). Assumed by the factory, pending owner confirmation (OQ-7).
- **D-3 — The last-owner refusal reuses the manager screen's *wording***:
  unprocessable (`422`), message "the team owner cannot be removed". One
  wording across both removal paths — **not one rule**. The manager path
  refuses removing *any* owner (role check, `handlers_manager.go:358`); this
  spec's guard refuses only a removal that leaves zero owners (D-4). In a team
  with two owners the paths would disagree: the manager path refuses removing
  O1, the member path allows it. The product cannot create such a team today,
  so the divergence is **accepted** and recorded rather than resolved; the
  manager rule stays as it is (§6, AC-18). Assumed by the factory, pending
  owner confirmation.
- **D-4 — The guard is "would leave zero owners", not "caller is an owner".**
  Equivalent today (one owner per team), and it is the narrower refusal if
  multi-owner teams ever arrive: an owner with a co-owner may still leave
  (AC-13). Assumed by the factory, pending owner confirmation.
- **D-5 — A sole owner alone in the team is also refused** (AC-8) rather than
  having the team deleted for them. Deleting a team is a data-destroying action
  the user must choose explicitly; it stays available as its own operation.
  Assumed by the factory, pending owner confirmation (OQ-8).
- **D-6 — No interleaving of removals may leave a team with zero owners**
  (AC-19). The alternative, accepting the race between two co-owners, would
  leave the guard defeatable by timing; the stronger property costs nothing a
  user sees, and the state the guard exists to prevent then never occurs. How
  it is met (a single conditional statement, a lock, a transaction) is the
  plan's choice. Assumed by the factory, pending owner confirmation.
- **C-1, C-2 — contract-stage decisions** (recorded in full in
  `contracts/features/PAC-25.md` §4). C-1: the `422` body reuses the shared error
  shape with an example rather than a schema fixing the message. C-2: on the link
  detail read, a storage failure on any read the answer depends on (the link, the
  caller's host status, the roster) answers an internal error, never not-found and
  never a success with the roster emptied; this keeps AC-4's promise and does not
  fail open. Neither deletes user data or widens who can see data. Assumed by the
  factory, pending owner confirmation.

---

## Challenge — 2026-10-27

*spec-challenger, against `factory/pac-25-spec` at `08667202`. Every citation
below was re-read on that commit (api) and on `smart-calendar-flow` `origin/main`
`ad52160` (web). Nothing was established by running a request or a test.*

**Verdict**: accept-with-changes

§2 is accurate almost line for line: every handler, storage, route, contract and
web citation I checked holds, including the inferences (no web path reads a link
the caller does not own; the product cannot create a second owner — the only
`AddTeamMember(..., "owner")` call and the only `CreateTeam` call are
`handlers_teams.go:48,54`). The scope is right and the split seam is clean. Two
statements are contradicted by the record and must be corrected before the
contract stage builds on them.

### Blocking

1. **D-1 says "The ticket allows either" (403 or 404). It does not.** The PAC-25
   issue text says "Adds a 403 and a 422 to the spec", and the very U-08
   resolution this spec claims to resolve says "Add the owner check and a `403`
   to the spec" (`docs/factory/api-audit.md:476`); batch B4 says "new 403/422
   responses" (`:549`). The issue title also asks for an *owner* check, while
   D-2 admits accepted co-hosts. Choosing not-found and owner + accepted
   co-hosts may well be the right call, but the spec presents it as within the
   ticket when it departs from both the ticket and the audit's recorded
   resolution of U-08. The contract stage and the audit update will inherit the
   misstatement (U-08 would be marked resolved with an answer its own
   resolution text contradicts). **Resolves when**: D-1 and D-2 state that the
   issue and U-08 prescribe owner-only + `403`, that this spec departs from them
   and why, and "Inherited scope" says U-08 is resolved *differently* from its
   recorded resolution (so the audit row is updated to match, not left saying
   `403`).

2. **§6 cites tests that do not exist.** "The manager-domain owner-protection
   rule (`handlers_manager.go:358-361`) and its tests in
   `backend/api/handlers_manager_test.go`": no test in that file (or anywhere in
   `backend/`) exercises that refusal. The string "the team owner cannot be
   removed" appears only at `handlers_manager.go:359`, and the file's only
   `422` assertion (`handlers_manager_test.go:101`) is for invalid add-member
   input. The rule D-3 reuses, and which §6 says must not change, is
   unprotected, exactly as the spec correctly says of the other two operations.
   **Resolves when**: §6 says the manager rule has no test, and either an AC
   pins it (an owner removed through the manager path answers `422` and the row
   is unchanged) or the spec records that it stays unpinned.

### Non-blocking

1. **D-3 "one rule, one wording, across both removal paths" is not true after
   D-4.** The manager path refuses removing *any* owner (role check,
   `handlers_manager.go:358`); this spec's guard refuses only a removal that
   leaves zero owners (D-4, AC-13), and §6 freezes the manager rule. In a
   two-owner team the two paths would disagree. Unreachable today (one owner
   per team), so not blocking; reword D-3 to "one wording" or record the
   divergence as accepted.
2. **AC-4 "byte-identical to what B receives today"** cannot be checked once
   "today" is gone. Restate as observable fields: `is_owner=false`,
   `my_status="accepted"`, `hosts` lists every host row including pending and
   declined ones with email/name/avatar, link fields as stored.
3. **AC-15's last sentence overstates the gate.** `make contract` mechanically
   checks the x-uncertain rule (README §2, gate 3); it cannot detect prose that
   "still describes today's behaviour", and `removeTeamMember` carries no
   `x-uncertain` at all (`contracts/openapi/paths/teams.yaml:700-727`). Restate
   as checkable facts: `getSchedulingLink` has no `x-uncertain` and no `403`,
   and its `404` names the refused population; `removeTeamMember` declares a
   `422` with body `{"error":"the team owner cannot be removed"}`. The prose
   halves are reviewer checks, not gate checks.
4. **AC-4, AC-5, AC-9..AC-12 and the first half of AC-13 pass today.** They are
   correctly labelled as pins and are useful, but none can demonstrate the
   change. The failing-today criteria are AC-1..AC-3, AC-6, AC-7, AC-8, AC-13
   (second half), AC-14 and AC-15.
5. **OQ-5 hands the plan an option, not a requirement.** The spec should state
   the requirement ("no interleaving of removals may leave a team with zero
   owners", or explicitly "a race between two co-owners is accepted") and let
   the plan choose how. As written, neither outcome is falsifiable.
6. **Missing pin: a non-owner removing the owner.** M removes O (caller is not
   target, M is not an owner) must stay `403` with O still owner. This is the
   path where the new refusal and the existing `403` could be ordered either
   way; one AC fixes the order (`403` first, so a member cannot learn who the
   owner is from the answer).
7. **Accepted co-hosts keep seeing pending and declined invitees' emails.** §1a
   argues those emails are the harm, yet §3a/§5 keep them visible to accepted
   co-hosts (through the list today, and the detail). Recorded as out of scope,
   which is acceptable; flagged so it is a conscious choice.
8. **Minor citation drift.** `booking.yaml:508-546` runs to `:549`;
   `teams.yaml:700-724` runs to `:727`. The issue and API-006 cite
   `handlers_scheduling_links.go:414-431`, which is stale; the spec's
   `:391-407` is correct.
9. **OWNER: should refused link reads answer `404` (D-1) or the `403` the issue
   and U-08 asked for?** A product call on whether a link id's existence is
   sensitive; it also decides OQ-2.
10. **OWNER: owner-only (as the issue title says) or owner + accepted co-hosts
    (D-2)?** No current web screen reads a link by id as a co-host
    (`smart-calendar-flow/src/api/schedulingLinks.ts:514,544,574` are owner
    flows; edit and toggle are gated on `is_owner`, `src/pages/Links.tsx:144,155`),
    so either choice breaks no consumer.
11. **OWNER: D-5, a sole owner alone in the team is refused rather than the
    team being deleted for them.** Conservative and consistent with not
    deleting data; needs confirmation.

### Criteria I could not falsify

- AC-4 (as written): "byte-identical to what B receives today" has no reference
  once the change lands (non-blocking 2).
- AC-15 (last sentence): no gate can fail on prose that describes old behaviour
  (non-blocking 3).

### Citations I checked and found wrong

- §6 "its tests in `backend/api/handlers_manager_test.go`": no such test
  exists; the refusal at `handlers_manager.go:359` is untested (blocking 2).
- D-1 "The ticket allows either": the issue says "Adds a 403"; U-08's
  resolution says "a `403`" (`docs/factory/api-audit.md:476`) (blocking 1).
- D-3 "One rule ... across both removal paths": the manager path's rule is
  role-based (`handlers_manager.go:358`), D-4's is count-based (non-blocking 1).

### Consumers this breaks

- None found. Checked: web `src/api/schedulingLinks.ts:514,544,574` (single-link
  reads, owner-only flows after create/update); web `src/api/teams.ts:380-391`
  and `src/components/team/ProtectedHoursTab.tsx:708` (remove control shown only
  to the owner and never on their own row; errors toasted at `:668`);
  `mcp/tools.go` (calls neither operation); e2e `tests/scheduling-links.spec.ts`
  (never reads a link by id; reads `/bookings` and the list as the owner). The
  web's generated types gain a `422` on `removeTeamMember`, which no
  hand-written web code narrows on.

---

## Revision 1 — 2026-10-27

*spec-author, answering the challenge above (against `1b27892`). Every new
citation was re-read on that commit; nothing was established by running a
request or a test. The PAC-25 issue text was re-read in Linear.*

### Blocking

1. **D-1 "The ticket allows either" — corrected.** The issue says "Adds a 403
   and a 422 to the spec" and its title asks for an owner check; U-08's
   resolution says owner check + `403` (`api-audit.md:476`, B4 at `:549`). D-1
   and D-2 now say plainly that they depart from the issue and from U-08, and
   why (D-1: a `403` confirms the id is real, not-found discloses strictly less,
   PAC-30 precedent; D-2: accepted co-hosts already get the identical record
   through the list, so refusing them the detail protects nothing).
   "Inherited scope" now says U-08 is resolved *differently* from its recorded
   resolution and that the audit row and B4 note must be rewritten to the
   shipped resolution. The owner choice is open as OQ-6 and OQ-7.
2. **§6 cited tests that do not exist — corrected.** §2b and §6 now say the
   manager rule (`handlers_manager.go:358-361`) is untested (the only `422`
   assertion in `handlers_manager_test.go`, `:101`, is for invalid add-member
   input). New AC-18 pins it: the sole owner removing their own email through
   the manager removal scoped to the team answers `422` with the existing
   message and the row is unchanged. That is the only way to reach the refusal
   today, because the manager path requires the caller to be an owner
   (`handlers_manager.go:613-616`).

### Non-blocking

1. **D-3 "one rule"** — reworded to "one wording, not one rule"; the role-based
   vs count-based divergence in a two-owner team is recorded as accepted.
2. **AC-4 "byte-identical"** — restated as observable fields: B not owner
   (`is_owner=false`), B's status accepted (`my_status="accepted"`), the roster
   lists every host row including pending and declined ones with status, email,
   name and avatar, link fields as stored. The Given now constructs a pending
   and a declined host so the roster claim can be tested.
3. **AC-15 overstated the gate** — restated as checkable facts
   (`getSchedulingLink`: no `x-uncertain`, no `403`; `removeTeamMember`: a `422`
   with body `{"error":"the team owner cannot be removed"}`); the prose halves
   are labelled reviewer checks.
4. **Pins vs change** — §4 now lists which criteria fail today and which are
   pins.
5. **OQ-5 was an option** — replaced by a requirement, D-6: no interleaving of
   removals may leave zero owners; AC-19 makes it falsifiable. OQ-5 is closed.
6. **Missing pin, non-owner removing the owner** — added as AC-17 (`403`, O
   still owner); §3b states the order (not-allowed before last-owner).
7. **Accepted co-hosts still see pending/declined emails** — recorded as a
   conscious choice in §3a, §5 and D-2.
8. **Citation drift** — `booking.yaml:508-549` and `teams.yaml:700-727`
   corrected; §2a notes that the issue/API-006 range `:414-431` is stale and
   `:391-407` is current.
9. **OWNER: 404 vs 403** — open as OQ-6 (it decides OQ-2 too).
10. **OWNER: owner-only vs owner + accepted co-hosts** — open as OQ-7, with the
    challenger's consumer evidence.
11. **OWNER: D-5** — open as OQ-8.

Ready for re-challenge. The owner questions (OQ-1, OQ-6, OQ-7, OQ-8) do not
block the contract stage: the factory's assumed answers are recorded under
"Decisions" as the conservative ones, pending owner confirmation.
