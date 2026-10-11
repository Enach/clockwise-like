# Spec — PAC-58: One team roster, no per-person focus ranking, and a plain "what your manager sees"

- **Linear**: https://linear.app/paceday/issue/PAC-58
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**: Part A touches the roster, manager-analytics and
  formal-team-analytics read operations, so it inherits **API-022** (manager
  analytics: missing per-member `data_available` defaulted to "available"),
  **API-033** (formal-team analytics breakdown encodes `null` instead of `[]`,
  T5) and **API-088** (`team_id` optional on manager analytics, required on the
  roster). Part B touches team membership and adds/removes members, so it
  inherits **API-023** (owner can remove themselves, no last-owner guard) and
  **API-031 / API-032** (add/patch member response bodies). No `x-uncertain`
  sits on the Part A operations; the getTeam `404` marker
  (`contracts/openapi/paths/teams.yaml:578`, audit U-09) is inherited by Part B
  only if Part B changes that operation. Written from reading source, nothing was run.

## 0. Proposed split

This issue asks three questions. Two have a clearly simpler answer consistent
with PRODUCT.md, and one needs data-model work and owner decisions. To keep each
piece buildable:

- **Part A: visibility and honesty.** No per-person score, ranking or good/bad
  colouring. Per-person minutes go only to the team's manager. Every team member
  sees a plain statement of what the manager sees. This needs a small contract
  change (who may read per-person numbers) and no data migration. It ships first.
- **Part B: one roster.** The page shows one list of people per team, with one
  way to add them. The backend has two membership stores today, so this needs a
  migration plan and answers to OQ-3 to OQ-5. It goes through contract and plan
  separately, after Part A, as a sub-issue the owner opens (PAC-58 "[spec] part B").

Both parts are specified below so that one document holds the intent. ACs are
prefixed A or B.

## 1. Problem

The team page is where a manager and their reports meet. Today it confuses
both of them, and it undermines Paceday's main privacy promise.

- **Two rosters that disagree.** The "Team" tab lists people for 1:1 tracking,
  added through "Add person" or a calendar scan. The "Protected hours" tab lists
  the formal team, added through "Invite" or "Manage members". They hold
  different people and count differently. In the e2e seed the two tabs show 1
  and 2 for the same team. A new manager cannot tell "Add person" from "Invite",
  or which one makes someone "on the team".
- **A per-person focus score that judges people.** The Analytics tab gives
  each person a 0–100 "Focus score", sorts the table worst-first, tints rows red
  or green, and calls out a named person ("X had over 25 hours in meetings").
  PRODUCT.md promises that managers see aggregate focus minutes only. A ranked
  league table of scores is surveillance in tone even without titles, and it
  turns "both seats are first-class" into "one seat grades the other".
- **Nobody is told what is visible.** An IC has no screen that says what their
  manager can see. In fact any member of a formal team can read every other
  member's weekly minutes (§2.3), which is wider than "managers" and wider than
  anyone would guess.

Cost: managers mistrust the page, ICs have reason to mistrust Paceday, and the
privacy fact PRODUCT.md calls non-negotiable is not true as stated.

## 2. Current behaviour

### 2.1 The page and its two rosters (web)

- Four tabs: Team, Protected hours, Find a time, Analytics
  (`smart-calendar-flow/src/pages/Team.tsx:90-97`).
- **Team tab** roster comes from the manager roster read, scoped to the active
  formal team (`Team.tsx:433-443`). Members are added with "Add person"
  (`:609-611`, dialog `:1115-1268`, copy "We will track their cadence and focus
  trends" `:1206`) or by a calendar scan (`:470-490`, `:755-796`). Each row shows
  a source label: "Auto-detected", "Formal team member" or "Added manually"
  (`:955-961`).
- **Protected hours tab** shows the formal team's active members and a pending
  count (`components/team/ProtectedHoursTab.tsx:98-131`). It has its own "Invite"
  (`:140-143`, dialog `:565-646`: "They will appear as Pending until they accept",
  `:610`) and "Manage members" sheet (`:649-720`).
- Backend: the roster is the union of the manager's own 1:1 assignments for the
  team and the formal members not yet assigned, **excluding the caller**
  (`clockwise-like/backend/api/handlers_manager.go:581-627`; contract
  `contracts/openapi/paths/teams.yaml:152-160`). The formal-team count on
  Protected hours **includes** the owner. The 1:1 store can hold anyone by email,
  including people who are not on Paceday and not on the formal team
  (`backend/storage/migrations/022_manager_team_assignments.up.sql:1-10`).
  Pending invitees are formal-team people who are not in the roster.
- E2e seed: team Alpha has owner + one member formally (`e2e/seed/seed.sql:121-125`)
  and one 1:1 assignment (`:139-145`). Team tab shows 1, Protected hours shows
  "2 active members".
- Add and Invite are both owner-only on the server (`handlers_manager.go:255`
  scope with owner required; team invite requires owner, `handlers_teams.go:166`),
  but the web shows both buttons to every member (`ProtectedHoursTab.tsx:133-143`,
  `Team.tsx:609`).

### 2.2 "Manager mode" gate

- The Team tab is gated on a manager flag read synchronously from browser
  storage (`Team.tsx:122`, `:159`; storage read `src/api/manager.ts:144-151`,
  `:614-616`). When it is off, a "Manager mode is off" screen offers "Enable
  Manager mode" (`Team.tsx:159-212`). Enabling it fires the server write without
  awaiting it, then re-reads local storage (`:186-187`).
- The server never checks this flag on roster or analytics reads. Membership of
  the team is enough (`handlers_manager.go:183`, `:563-579`, owner not required).

### 2.3 Who can see per-person minutes today

- **Roster read** (`handlers_manager.go:181-251`): any member of the team gets
  this-week and last-week focus, meeting and free minutes for every other
  member. For a non-owner, the formal members (owner included) are synthesised
  in (`:594-614`). So in the seed, the Alpha report who turns on "Manager mode"
  sees the owner's minutes.
- **Manager analytics** (`handlers_manager.go:488-552`): 12 weeks per person,
  same membership-only scope.
- **Formal-team analytics** (`handlers_teams.go:491-523`,
  `engine/team_availability.go:165-197`): any member gets a per-member
  breakdown with focus score, and **no consent check**. The contract says so
  (`teams.yaml:871-877`).
- Consent: the manager engine honours a per-user "shared with manager" flag
  that defaults to true (`engine/manager.go:436-454`). Nothing can set it. No
  endpoint writes it (generated-code comment `backend/api/gen/paceday.gen.go:2464`)
  and the web has no control for it. PRODUCT.md's "Analytics sharing can be
  turned off" is not true today.
- Titles: no team read returns event titles. The web renders 1:1 history with
  a fixed "1:1" label (`AnalyticsTab.tsx:480`), and a unit test pins that
  (`AnalyticsTab.test.tsx:61-62`).

### 2.4 The per-person score and ranking (web)

- Analytics "This week" computes its own score per person, focus ÷ (focus +
  meetings) × 100 (`components/team/AnalyticsTab.tsx:159`), sorts ascending
  (worst first, `:162`), tints rows red/green (`:193-199`), colours the score
  pill red/amber/green (`:221-226`) under a "Focus score" column (`:189`), and
  emits insights that name a person (`:170`). The server also sends its own
  `focus_score` per member (`team_availability.go:182`;
  schema `teams.yaml:1575-1586`), which the web ignores.
- Trends shows per-person up/down arrows in green/red (`AnalyticsTab.tsx:269-365`).
  The Team-tab row repeats this with a coloured trend arrow (`Team.tsx:1026`,
  `:1054-1074`).
- **Attribution bug.** Formal-team analytics rows are matched to people by
  display name, falling back to **list position** (`src/api/teams.ts:474-476`).
  The web drops the owner from its list (`:472`), but the server's list starts
  with the owner (ordered by join date, `storage/teams.go:149-156`). When a name
  does not match (e.g. an empty name, which the schema allows,
  `teams.yaml:1577-1581`), a person is shown someone else's minutes.
- The Analytics tab gives only one week to its Trends view (`teams.ts:484`).

### 2.5 Tests that exist

- e2e `e2e/tests/manager-team.spec.ts:30-135`: roster scoping per team (API and
  browser). Backend `handlers_manager_test.go:194-332`: scoping, scoped add,
  delete, patch. Web `AnalyticsTab.test.tsx:33-82`: real data, no titles.
- **None** asserts who may read per-person minutes, the consent flag, the score,
  or that the two rosters agree.

## 3. Desired behaviour

### Part A: visibility and honesty

1. **The manager of a team is its owner.** Per-person numbers for a team are
   visible to the owner only. Any other member sees team totals (sum or average
   of minutes) and their own numbers, and never another person's. The page stops
   depending on a browser-stored "Manager mode" switch. What a person sees
   follows from their role on the team, which the server enforces.
2. **Minutes, not scores.** The manager sees each person's weekly focus and
   meeting minutes and 1:1 dates. Nothing ranks, scores or colours a person as
   good or bad. There is no per-person score, no worst-first ordering, no red or
   green row tint or trend colour, and no insight that names an individual.
   People are listed alphabetically. Team-level observations ("the team spent
   more time in meetings than focus") may stay.
3. **Consent is honoured everywhere.** A person whose sharing flag is off
   contributes no per-person numbers to any view. Their row says "Not shared",
   and team totals leave them out. Today only one of three reads checks it.
4. **Numbers belong to the right person.** Every per-person figure shown is
   that person's own, whatever their display name.
5. **"What your manager sees" is stated plainly**, on the team page, to both
   seats. To members it reads in the second person ("Your manager sees…"). To the
   owner it reads as what they can see and what they cannot. It says: weekly
   totals of focus and meeting minutes, and the dates of your 1:1s. It never
   includes event titles, descriptions, attendees or email. It names who the
   manager is (the team owner). It does not claim an off switch until one exists
   (OQ-2).
6. **Only the owner sees owner actions.** Members do not see Add, Invite,
   Remove or Scan controls the server would refuse.

### Part B: one roster (separate delivery, see §0)

7. **One list of people per team**, shown once and used by every tab: 1:1
   cadence, protected hours, find a time and analytics. One member count, the
   same everywhere.
8. **One way to add someone**: the owner adds a person by email, with an
   optional 1:1 cadence. If the person is not yet on the team, they are invited
   and listed as "Invited" until they accept. A calendar scan only *suggests*
   people. Confirming suggestions goes through the same add path, and the
   confirmation says that invitations will be sent.
9. **One way to remove someone**: removing a person takes them off the team
   for every tab. Their past 1:1s stay on the owner's calendar.

## 4. Acceptance criteria

### Part A

AC-A1. Given a team with an owner and two members who all have weekly
analytics, when a non-owner member reads the team's per-person roster or
per-person analytics, then the response has no other member's minutes. It has
only team totals and the caller's own figures.  [contract]

AC-A2. Given the same team, when the owner reads them, then each other
member's weekly focus and meeting minutes are present.  [contract]

AC-A3. Given a member whose sharing flag is off (set directly in the database),
when the owner reads any per-person analytics for the team (roster,
multi-week, or formal-team week view), then that member has no minutes in any of
them, is marked as not shared, and is left out of the team totals.  [contract]

AC-A4. Given any team analytics response, then it contains no per-person score
field.  [contract]

AC-A5. Given the owner opens the Analytics tab with three members whose focus
ratios differ, then the page shows no "Focus score" column and no 0–100 value
per person. Rows are in alphabetical order by name, not by any metric, and
no row, trend arrow or figure is styled differently according to its value.  [unit]

AC-A6. Given a member with more than 25 hours of meetings in the week, when the
owner views Analytics, then no insight text contains that member's name.  [unit]

AC-A7. Given a team whose members have empty or duplicate display names, when
the owner views the week's per-person figures, then each person's displayed
minutes equal that person's stored minutes (verified by seeding distinct values
per person).  [unit]

AC-A8. Given a signed-in member who is not the owner, when they open the team
page, then they see a statement naming the team owner as the person who can see
their weekly focus and meeting minutes and 1:1 dates, and stating that event
titles, descriptions, attendees and email are never shown. The statement
contains no mention of turning sharing off.  [e2e]

AC-A9. Given the team owner opens the team page, then the same facts appear,
worded as what they can and cannot see.  [e2e]

AC-A10. Given a member who is not the owner and whose browser storage says
"manager", when they open the team page, then they see no other member's
minutes and no Add, Invite, Remove or Scan control. Given an owner whose browser
storage is empty, they see the owner view without enabling anything.  [e2e]

AC-A11. (API-022) Given a member with no data for a week, when the owner views
that week, then the page shows "Data unavailable" for them, not zero minutes.  [unit]

AC-A12. (API-033, API-088) Given an empty team, the formal-team analytics
breakdown is an empty list, not null. The manager per-person analytics read
behaves the same with and without a team identifier as the contract states,
with the inconsistency resolved one way and documented.  [contract]

### Part B

AC-B1. Given the e2e seed's team Alpha, when the owner looks at every tab, then
the member count shown is the same number on each.  [e2e]

AC-B2. Given the owner adds by email a person who is not on the team, then that
person appears once in the single list as "Invited", is selectable for 1:1
cadence, and appears in protected hours and find-a-time membership.  [e2e]

AC-B3. Given the team page, then there is exactly one control for adding a
person (no separate "Add person" and "Invite").  [e2e]

AC-B4. Given a scan proposes two people, when the owner confirms one, then only
that person is added (as Invited if not yet a member). The confirmation stated
that an invitation would be sent before it was confirmed.  [e2e]

AC-B5. Given the owner removes a person, then the person is absent from every
tab and from the team's analytics, and the owner's past 1:1 events are
unchanged.  [contract]

AC-B6. (API-023) Given a team with a single owner, when the owner tries to
remove themselves, then the request is refused.  [contract]

## 5. Explicitly out of scope

- **An off switch for sharing.** Building the IC-side control is a separate
  issue (OQ-2). Part A only makes every read honour the flag that already exists.
- **Event titles.** Already not exposed (§2.3). This spec preserves that and
  adds no title detail.
- **PAC-54** (gaps omit never-met members). It is related, but it is a cadence-
  gap correctness fix and stays its own issue.
- **Redesigning Find a time or Protected hours** beyond Part B's shared list.
- **The sign-up IC/Manager profile choice.** It keeps whatever onboarding use it
  has. Part A only stops the team page depending on it.
- **Reducing the number of places per-person minutes appear** (Team row,
  Analytics week, Trends, Members). This is worth doing for simplicity but is a
  layout decision for the plan/design stage, not a behaviour change.

## 6. What must not change

- Roster scoping per team: `e2e/tests/manager-team.spec.ts:30-135`,
  `backend/api/handlers_manager_test.go:194-332`. The owner's view of their own
  team must still show exactly that team's people.
- No event titles in any team view: `AnalyticsTab.test.tsx:61-62`. **No backend
  test** asserts this for the team reads. That is a finding, and an AC-A1/A3-style
  contract test should also assert no title-like field.
- Protected hours, Find a time and 1:1 scheduling keep working for the owner. No
  test covers protected-hours membership display, which is a finding.
- Members who are not on Paceday keep 1:1 cadence tracking in Part A.

## 7. Rollback

Part A has no migration. Reverting the backend restores the wider membership
scope, and reverting the web restores the score UI. Both are clean, but the
backend revert re-opens the privacy gap, so roll back the web first only if the
backend is kept. Part B will need a migration that merges the two membership
stores. Its plan must say whether the down migration can split them again
without losing 1:1 people who are not team members. That is the main reason Part B
is separate.

## 8. Open questions

- **OQ-1: Is "manager = team owner" right?** An IC who creates a team for
  shared protected hours would then see peers' minutes. The alternative is
  team totals only for everyone, with no per-person numbers at all. That reading
  of "aggregate minutes only" is simpler and stricter, but drops a listed
  capability ("focus analytics for team members"). Owner: product owner. Resolve
  before the Part A contract.
- **OQ-2: Should ICs get an off switch now?** PRODUCT.md says "Analytics
  sharing can be turned off", and today it cannot be (§2.3). Either open an
  issue to build it, or correct PRODUCT.md. Owner: product owner.
- **OQ-3: Can people who are not on Paceday stay on a team?** In Part B they
  would sit as "Invited" indefinitely. Today the manager also sees an *estimate*
  of their busy minutes from free/busy lookups (`engine/manager.go:456-470`).
  Keep that estimate, or show nothing until they join? Owner: product owner.
- **OQ-4: Should a scan confirmation send invitations automatically** (AC-B4),
  or add people as 1:1-only until invited? Owner: product owner.
- **OQ-5: Does the owner appear in the single list** (and so in the count)?
  The proposed default is yes, marked "You", with no 1:1 cadence. Owner: product owner.
- **OQ-6:** Does any other client (MCP server, Slack recap) read these
  per-person endpoints and depend on the wider scope? Not checked. Owner:
  contract-author, by searching both repos and the MCP server before stage 2.

## Challenge — 2026-10-11

**Verdict**: accept-with-changes

The spec gets the issue's three questions right and its current-behaviour section
is mostly accurate. It has three problems. The ACs assume an answer to OQ-1 that
nobody has given yet. It misses one way per-person minutes leak today. And it
bundles a Part B that cannot pass this gate. The product owner asked for
simplicity, so proportionality is judged below as well.

### Blocking
1. **The Part A ACs assume an answer to OQ-1.** AC-A1, A2, A3, A7, A9 and A11
   are written for "the owner sees per-person minutes". OQ-1 (owner only, or team
   totals for everyone) is still open and is marked "resolve before the Part A
   contract". The issue asks this question outright ("should a per-person focus
   score exist under the aggregate-only promise?"). The PO also asked for the
   simpler option, and the spec itself calls the stricter reading simpler. If
   the stricter reading wins, AC-A2, A7 and A11 and the owner wording in A8/A9
   go away. *Resolves when* the PO answers OQ-1 and the ACs are rewritten to
   match that answer, before this spec leaves stage 1.
2. **A per-person leak that §2.3 does not list and no AC covers.** The manager
   1:1 store accepts any email (`POST /api/manager/team/members`). When the
   request has no `team_id`, the scope check is skipped
   (`handlers_manager.go:254`, `managerTeamScope` returns early at `:567-569`). If
   the email belongs to a Paceday user, the server links that user's id
   (`:293-296`). After that, `GET /api/manager/analytics` without `team_id`
   (`:487-490`, `listManagerMembers` nil branch `:582-584`) returns 12 weeks of
   that user's real `analytics_weeks` minutes. Their consent flag defaults to true
   and nothing can set it (`engine/manager.go:436-442`). With `team_id`, an owner
   can do the same to any Paceday user who is not on the team. So any signed-in
   user can read any other Paceday user's weekly focus and meeting minutes if
   they know the email. AC-A1/A2 cover only "the team's" reads, so Part A as
   written could ship with this still open. API-088 in the audit calls the
   no-`team_id` path "self-scoped, not a vulnerability", which is wrong for this
   reason. *Resolves when* §3 says whose per-person minutes a caller may ever see
   (for example: only people on a team the caller owns, whatever the request
   path or `team_id`), and an AC covers adding a non-member Paceday user by email
   and reading analytics with and without `team_id`.
3. **Team totals expose individuals in small teams.** §3.1 gives non-owners
   team totals plus their own figures. On a two-person team (the e2e seed's
   Alpha), total minus own equals the owner's exact minutes. On three, a member
   can work out the sum for the other two. Under §3.3 the same applies when all
   but one person has "Not shared". *Resolves when* §3 says what a non-owner sees
   when the totals would identify one person, and an AC tests the two-person case.
4. **Part B cannot pass a stage-1 gate in this document.** AC-B2 and AC-B4
   depend on OQ-3, OQ-4 and OQ-5, which are open. Part B needs a migration whose
   rollback the spec cannot describe yet (§7). The spec already says Part B is a
   separate sub-issue. Keeping its ACs here means this gate either approves
   criteria that cannot be built or blocks Part A on them. *Resolves when* Part B
   moves to its own spec under its own sub-issue. This spec keeps one paragraph
   saying the roster problem, the issue's headline item, stays open on PAC-58
   until that sub-issue ships.

### Non-blocking
1. **Proportionality (PO asked for simplicity).** Three items in Part A go
   beyond what the issue needs:
   - **AC-A3**: wiring consent into three reads for a flag no user can change.
     This is only useful once OQ-2 delivers the switch.
   - **AC-A4**: dropping a server `focus_score` that the web already ignores.
     That is a breaking contract change, and the issue is about what the page
     shows.
   - **The owner-only per-person view, AC-A7 and AC-A11 together.** These go away
     under the stricter OQ-1 reading.

   §7 "touch it, fix it" still forces API-022/033/088 only for operations Part A
   actually changes. If OQ-1 removes per-person reads, the inherited list gets
   shorter too. The spec should say which ACs fall away under each OQ-1 answer.
2. AC-A1 says "team totals (sum or average)". Pick one, or a test cannot check a
   value.
3. §3.2 bans trend colours everywhere, but AC-A5 covers only the Analytics tab.
   No AC covers the Team-tab trend arrow (`Team.tsx:1054-1074`, green/red) or the
   Trends sub-view arrows (`AnalyticsTab.tsx:269-365`).
4. AC-A11 does not name the view. The Team-tab row already shows "Data
   unavailable" when `data_available` is false (`Team.tsx:1031`), so a test
   there could pass today. The API-022 defect is in the multi-week analytics
   (Analytics > Members). Name that view.
5. When a consent change takes effect is not stated. `GetMemberWeek` serves a
   cached per-(manager, member, week) row for 4h before it checks the flag
   (`engine/manager.go:421-429`). Once OQ-2 ships, turning sharing off would not
   take effect for up to 4h. Say whether that is acceptable.
6. OQ-6 is answerable now. `mcp/` has no reference to the manager or
   formal-team reads. The only other `focus_score` producer is personal
   analytics (`handlers_analytics.go:150`), which AC-A4 does not touch and
   should say so.
7. §2.5/§6: the e2e browser roster test depends on the localStorage manager
   flag being seeded (`e2e/auth.ts:115-133`). Part A changes that, so the
   fixture is a consumer.

### Criteria I could not falsify
- AC-A12, second sentence: "behaves the same with and without a team
  identifier as the contract states, with the inconsistency resolved one way and
  documented". Which way is not specified, so no test can tell pass from fail.
  State the required behaviour.

### Citations I checked and found wrong
- "The web drops the owner from its list (`teams.ts:472`)". Actually at
  `src/api/teams.ts:475`. The name/position fallback is at `:477`, not `:474-476`.
- "Protected hours tab shows … a pending count" and "Pending invitees are
  formal-team people who are not in the roster". Actually, invitees live in
  `team_invites` (`handlers_teams.go:183`) and are never in `team_members`. With
  the real backend, `normalizeTeam` marks every member `active`
  (`src/api/teams.ts:136-140`), and there is no read that lists invites. So the
  pending count, and "Pending until they accept" (`ProtectedHoursTab.tsx:610`),
  only ever show in the local fallback (`teams.ts:377`, `:579`). This matters for
  AC-B2 ("listed as Invited"), which needs a read that does not exist.
- §2.3 "For a non-owner, the formal members (owner included) are synthesised
  in". Synthesis runs for every caller, the owner included. Only the caller is
  skipped (`handlers_manager.go:599`). Not wrong in effect, but the condition is
  not "non-owner".
- All other citations checked (Team.tsx, ProtectedHoursTab.tsx, AnalyticsTab.tsx,
  manager.ts, handlers_manager.go, handlers_teams.go, team_availability.go,
  storage/teams.go, migration 022, seed.sql, teams.yaml, paceday.gen.go:2464,
  the tests) match the code within a line or two.

### Consumers this breaks
- smart-calendar-flow/src/pages/Settings.tsx:748-793: the Manager-mode toggle
  and its "Manager mode enabled" toast stop controlling the team page. §5
  excludes only the sign-up choice, so say what Settings shows.
- smart-calendar-flow/src/pages/Onboarding.tsx:33,102: writes `is_manager`,
  which the team page would no longer read.
- clockwise-like/e2e/auth.ts:115-133: seeds `is_manager` in localStorage for
  the browser roster test (`e2e/tests/manager-team.spec.ts:104`).
- smart-calendar-flow/src/api/generated/{types,schemas}.ts and `src/api/teams.ts`
  formal-team analytics mapping: AC-A4 removes `focus_score` from
  `MemberAnalyticsSummary` (`teams.yaml:1586`).
- smart-calendar-flow/src/components/team/AnalyticsTab.tsx:381 (Members view)
  and `Team.tsx:433-443`: for non-owners, AC-A1 removes the per-person data
  these render.
