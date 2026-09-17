# Domain notes: manager workflows & formal teams

Companion to `teams.yaml`. 26 operations, 36 schemas. Everything below is traceable to
`backend/api/handlers_manager.go`, `backend/api/handlers_teams.go`,
`backend/storage/manager.go`, `backend/storage/teams.go`, `backend/engine/manager.go`,
`backend/engine/team_availability.go`, the migrations under `backend/storage/migrations/`,
and the four contract-test files (`api/manager_contract_test.go`,
`storage/manager_contract_test.go`, `api/handlers_teams_test.go`,
`api/handlers_manager_test.go`).

---

## (a) Backend / frontend mismatches

### A1. Against the strict zod schemas in `src/contracts/managerTeam.ts`

The zod schemas reject unknown fields (`.strict()`). Field-by-field comparison:

| Schema | Verdict |
| --- | --- |
| `managerRosterSchema` vs `GET /api/manager/team` | **Exact match.** Both are `{team_id, members}` and the handler's anonymous `memberResp` has exactly the ten keys zod knows. |
| `managerMemberSchema` vs `memberResp` | **Exact match**, including `source` accepting the synthetic `"formal"` value. |
| `memberWeekSchema` vs `engine.MemberWeekStats` | **Exact match** (4 keys). |
| `scanPreviewSchema` vs `POST /api/manager/detect` | **Exact match** (7 keys); `assigned: z.literal(0)` holds because the handler hard-codes `"assigned": 0`. |
| `scanCandidateSchema` vs the handler's `candidateResponse` | **Exact match** (3 keys). |
| `scanConfirmationSchema` vs `POST /api/manager/detect/confirm` | **Exact match** (4 keys). |

**No strict-zod violation exists today.** This is the hardened surface; the bugs are all on
routes that zod does not cover. Two *latent* strictness hazards, however:

- **A1a (latent).** `managerMemberSchema.cadence` is `cadenceSchema` (5 values). For the
  team-scoped roster, `cadence` comes from `manager_team_member_assignments.cadence`, which
  is `NOT NULL DEFAULT 'none'` with a matching CHECK, so it is safe. For the *global*
  (`team_id`-less) roster it comes from `manager_team_members.cadence`, also CHECKed. Safe,
  but any future nullable cadence column breaks the Go `Scan` into `string` before zod ever
  sees it.
- **A1b (latent).** `managerMemberSchema.cadence_custom_days` is `.min(1).max(365)`. The
  team-scoped assignment CHECK is only `cadence = 'custom' AND cadence_custom_days IS NOT
  NULL` — it does **not** re-assert the 1..365 range on the assignment row (migration 023
  line ~164), whereas the column-level CHECK added at line ~5 does. Both are present, so
  the range is in fact enforced. Flagging because the two CHECKs are redundant and a
  rollback of one would silently widen the wire contract past zod.

### A2. `GET /api/teams/invites/{token}` sends almost nothing the frontend asks for

`src/api/teams.ts::normalizeInvite` reads `teamId`/`team_id`, `token`, `expiresAt`/
`expires_at` and `inviterEmail`/`inviter_email`. The backend returns **only**
`{teamName, inviterName, email}`. Consequences, all real:

- `invite.team_id` is always `""`.
- `invite.expires_at` is always `undefined` — the UI cannot show "expires in N days"
  even though the invite email body promises "This link expires in 7 days".
- `invite.inviter_email` is always `undefined`; the backend sends a **display name** under
  `inviterName`, which no branch of `normalizeInvite` reads. The inviter is silently dropped.
- `src/api/teamInvites.test.ts` asserts against a hand-written `{teamId, teamName, email}`
  fixture that the backend never produces, so the test passes while the integration is broken.

### A3. `POST /api/manager/profile` and `POST /api/manager/team/members` emit PascalCase

`storage.UserProfile` and `storage.ManagerTeamMember` have **no json tags** (see
`backend/storage/manager.go:13` and `:57`). Their bodies are therefore
`{"UserID":…,"IsManager":…,"DetectedAt":…,"AnalyticsSharedWithManager":…,"UpdatedAt":…}` and
`{"ID":…,"ManagerUserID":…,"MemberEmail":…,…}` — PascalCase, in a domain that is otherwise
uniformly snake_case, and on the *same path* whose GET is snake_case. `PATCH
/api/manager/team/members/{email}` has the same body.

The frontend accidentally dodges this: `setProfile` ignores the POST response entirely, and
`addMember`/`updateMember` discard the body and re-fetch `GET /api/manager/team`. So this is
a latent contract bug, not a live break — but any client that trusts the documented response
gets nothing usable.

Worse, the 201 add body is the **pre-insert in-memory struct**: `ID` is the zero UUID and
`CreatedAt`/`UpdatedAt` are `"0001-01-01T00:00:00Z"`, because the handler never reads the row
back after `Upsert…`. `AnalyticsSharedWithManager`/`UpdatedAt` on the profile POST are
likewise the values read *before* the upsert.

### A4. `PATCH` responses can legitimately be the JSON literal `null`

- `patchMember`: `updated, _ := storage.GetManagerTeamMemberByEmail…` — error discarded, then
  encoded. A failed re-read yields `200` with body `null`.
- `patchTeam`: `team, _ := storage.GetTeam(...)` — same pattern, `200` with `null` after a
  successful rename.

`src/api/teams.ts::renameTeam` ignores the body and re-fetches, so it survives; a stricter
client would not.

### A5. `null` where the frontend and zod-style consumers expect `[]`

Three arrays in this domain are `nil`-encoded (no `omitempty`, no pre-initialisation):

- `GET /api/teams/{id}` → `members: null` for an empty team (`members, _ :=
  storage.ListTeamMembers`). Frontend absorbs it with `detail.members ?? []`.
- `GET /api/teams/{id}/analytics` → `member_breakdown: null` for an empty team.
  Frontend uses `raw.member_breakdown?.find(...)`, so it survives.
- The `Team.orgId` field is `omitempty` on a `*uuid.UUID`, so the key is **absent**, not
  `null`, for a team with no org.

By contrast `members` (manager roster), `gaps`, `candidates`, `slots`, the zones array and the
teams array are all explicitly normalised to `[]`. The inconsistency is the bug.

### A6. 500s are `text/plain`, and the frontend treats non-JSON as "backend unreachable"

`writeError` (`handlers_settings.go:206`) sets `Content-Type: application/json` and writes
`{"error": …}`. `http.Error` does not — it writes `text/plain; charset=utf-8`. Every 500 in
this domain, plus the two `404 "not found"` paths in `patchMember`/`scheduleMember`, uses
`http.Error`.

`src/api/client.ts::requestApi` (line ~371) throws `ApiUnreachableError` for any non-JSON
2xx, and for a non-2xx it attempts `res.json()` and passes `undefined` to `ApiHttpError` when
that fails. So a manager-domain 500 reaches the UI as an `ApiHttpError` with **no message**,
and the 404 on `patchMember` is indistinguishable from any other bodyless failure. The
`{"error": …}` envelope documented for 4xx is real; the 500 envelope is not.

### A7. `GET /api/manager/analytics` sends less than the frontend normalizer expects

`src/api/manager.ts::normalizeAnalytics` reads per-member `is_paceday_user`,
`data_available` and `one_on_ones`. The handler's `memberAnalytics` emits only
`{email, display_name, weeks}`. So:

- `is_paceday_user` always defaults to `false` in the UI, even for Paceday members whose
  roster row says otherwise.
- `data_available` defaults to **`true`** (`m.data_available ?? true`) — the exact opposite
  of the truth when analytics are degraded. The backend *does* send `data_available` per
  week bucket, but the normalizer's `weeks` mapper drops it (see `RawAnalyticsMember.weeks`,
  which has no `data_available` field). **The degraded-analytics signal is silently lost on
  this endpoint.** This is the most consequential mismatch in the domain: the manager
  analytics chart renders zeroes as if they were measured.
- `one_on_ones` is always `[]`.
- `display_name` is sent and never read.

### A8. `GET /api/manager/profile` sends `team_member_count`, which nothing reads

The frontend types the response as `{is_manager?, detected_at?}` and derives
`onboarding_profile_selected` from `Boolean(detected_at)`. `team_member_count` is unused —
and is a *global* roster count, so it does not correspond to any team the UI is showing.

### A9. Weekday and time-format asymmetries on no-meeting zones

- Wire convention is Monday=1..Sunday=7; storage is Go's Sunday=0..Saturday=6. The handler
  converts on both edges and `TestNoMeetingZone_ValidatesAndConvertsWeekdayAtBoundary`
  pins it. Correct, but note `listZones` orders by the **stored** `day_of_week`, so Sunday
  sorts first in the response even though it is emitted as `7`.
- Requests must be `HH:MM`; responses come from a Postgres `TIME` column scanned into a Go
  `string`, i.e. almost certainly `"HH:MM:SS"`. The frontend's `minutesFromTime` splits on
  `:` and reads `[0]`/`[1]`, so it tolerates both. No test pins the response format.

### A10. `PATCH` on a zone is a full replace

No merge against the stored row: every field comes from the body. An omitted `dayOfWeek`
decodes to `0` and fails validation with `422 "dayOfWeek must be between 1 and 7"`. The
frontend compensates by GETting the zone and re-sending all fields; a naive partial PATCH
always 422s.

### A11. Trailing-slash routing

The frontend calls `GET /api/teams/` and `POST /api/teams/` (with the slash) while the
backend registers `r.Route("/api/teams")` + `r.Get("/")`. chi's `Route` mounts both
`/api/teams` and `/api/teams/*`, so both spellings work. Documented so the spec's
slash-less paths are not "fixed" into a break.

### A12. `GET /api/teams/invites/{token}` is documented public but mounted protected

The handler comment says `// GET /api/teams/invites/:token (public)`, but the route is
registered inside the `requireAuth` group in `routes.go:224`. An unauthenticated invite
preview returns `401`. The frontend comment in `teams.ts` correctly assumes authentication
("Invite consumption is authenticated"), so the code comment is the thing that is wrong —
but any doc generated from that comment will be wrong too.

### A13. `team_id` requirement is asymmetric across the manager routes

Not a frontend mismatch (the frontend always sends `team_id`), but a real API-shape
inconsistency worth pinning:

| Route | `team_id` | Role required when present |
| --- | --- | --- |
| `POST /api/manager/detect` | **required** | owner |
| `POST /api/manager/detect/confirm` | **required** | owner |
| `GET /api/manager/team` | **required** | member |
| `POST /api/manager/team/members` | optional | owner |
| `DELETE /api/manager/team/members/{email}` | optional | owner |
| `PATCH /api/manager/team/members/{email}` | optional | owner |
| `GET /api/manager/gaps` | optional | member |
| `POST /api/manager/team/members/{email}/schedule` | optional | member |
| `GET /api/manager/analytics` | optional | member |
| `GET /api/manager/profile`, `POST /api/manager/profile` | n/a | n/a |

Reading a roster demands a team, but mutating one does not. Every `team_id`-less call
operates on the manager's global roster with **no authorization beyond JWT**.

### A14. `DELETE /api/teams/{id}/members/{userId}` has no last-owner guard

Self-removal skips `requireOwner` entirely, so a sole owner can leave and strand the team.
The owner-protection rule (`422 "the team owner cannot be removed"`) exists only on
`DELETE /api/manager/team/members/{email}`. Two routes remove team members with different
safety rules.

---

## (b) Invariants (written to be turned into tests)

### I1. Team scoping — a roster read for team A never contains team B's members

For manager `M` owning teams `A` and `B`, and member email `e`:

1. `GET /api/manager/team?team_id=A` returns `e` **iff** either
   (a) a row exists in `manager_team_member_assignments(manager_user_id=M, team_id=A,
   member_email=lower(e))`, or (b) `e` resolves to a Paceday user that is a row in
   `team_members(team_id=A)` and that user is not `M`.
2. The response's `team_id` equals the requested `team_id`, byte for byte.
3. `GET …?team_id=B` for the same `M` returns a roster whose member set is disjoint from
   A's unless `e` is independently assigned/formal on B.
4. The caller itself is **always** excluded from the synthesized formal half.
5. Members are sorted ascending by `strings.ToLower(display_name)`.

Existing coverage: `TestManagerTeam_SelectedFormalTeamsHaveDistinctMembers`,
`TestPACE023RealEnginePreservesExternalRosterWhenFreeBusyUnavailable` (asserts team B stays
empty), `TestPACE023ConfirmAssignmentsIsIdempotentAndTeamIsolated`.

### I2. Team scoping — team-scoped preferences never leak into the global identity

`PATCH /api/manager/team/members/{email}?team_id=A` with `display_name="X"`, `cadence="c"`:

1. `manager_team_member_assignments(M, A, e).display_name_override = "X"` (or `NULL` when
   `"X"` equals the canonical name), `.cadence = "c"`.
2. `manager_team_members(M, e).display_name` and `.cadence` are **unchanged**.
3. `GET …?team_id=B` for the same `e` still reports the canonical name and B's own cadence.
4. The same PATCH without `team_id` *does* write the global row (different storage function).

Pinned by `TestPACE023ConfirmAssignmentsIsIdempotentAndTeamIsolated` (asserts
`a.DisplayName=="Team A Name"`, `b.DisplayName=="Canonical Person"`, `global.DisplayName`
unchanged).

### I3. Team scoping — a scoped delete unassigns, it does not erase history

`DELETE /api/manager/team/members/{email}?team_id=A`:

1. The `(M, A, e)` assignment row is gone.
2. Any `(M, B, e)` assignment row **survives**.
3. `manager_team_members(M, e)` **survives**, so `one_on_one_occurrences` and
   `team_analytics_cache` history remain queryable.
4. Pending `team_invites` for `(A, e)` are deleted; non-pending ones are not.
5. If `e` maps to a Paceday user with `team_members(A, e).role == "owner"`, the call returns
   `422` and changes **nothing** (checked before any delete).
6. Without `team_id`, the global `manager_team_members` row **is** deleted.

Pinned by `TestManagerTeam_ScopedDeletePreservesOtherAssignmentAndHistory`.

### I4. Team scoping — authorization

1. Non-member of `team_id` → `403 {"error":"forbidden"}` on every scoped route.
2. Member-but-not-owner → `403` on `detect`, `detect/confirm`, and the three
   `team/members` mutations; `200` on `GET team`, `gaps`, `analytics`, `schedule`.
3. A syntactically valid UUID that names no team → `403`, not `404` (the membership lookup
   error and the role mismatch share one branch).
4. Unparseable `team_id` → `400 "team_id must be a valid UUID"`, checked **before** any DB
   access.

Pinned by `TestManagerTeam_ScopeAuthorizationAndValidation`.

### I5. Preview-then-confirm — preview assigns nothing

`POST /api/manager/detect?team_id=A`:

1. Response `assigned == 0`, always, unconditionally (literal in the handler).
2. Assignment-row count for `(M, A)` is **unchanged** by the call.
3. Assignment-row count for every **other** team of `M` is unchanged.
4. `detected == len(candidates)`; `skipped == detected - eligible`;
   `eligible == count(candidates where already_assigned == false)`.
5. `already_assigned` per candidate is true iff `(M, A, lower(email))` already exists.
6. Preview **does** mutate the global side: `DetectTeam` upserts
   `manager_team_members` rows and `one_on_one_occurrences`, and sets
   `user_profiles.is_manager = true` / `detected_at = now()` when it added or updated ≥1
   member. Preview is *assignment*-free, not side-effect-free.
7. Omitting `team_id` → `400 "team_id is required"` before the engine is invoked.

Pinned by `TestManagerDetect_PreviewDoesNotAssignEitherTeam`,
`TestPACE023PreviewThenConfirmWireContract` (which also asserts the missing-`team_id` 400).

### I6. Preview-then-confirm — confirm is explicit, atomic and idempotent

`POST /api/manager/detect/confirm?team_id=A` with `{"emails":[…]}`:

1. Only the emails **named in the body** are assigned. There is no "assign all detected"
   path anywhere in the codebase.
2. Emails are normalised (`lower(trim(e))`) before storage; `" ALPHA@example.com "` and
   `"alpha@example.com"` are the same member.
3. An email with no existing global `manager_team_members` row is `skipped`, never created —
   confirm cannot invent members.
4. A duplicate within one request is counted once as `assigned` and once as `skipped`.
5. Re-confirming an already-assigned email is `skipped`, not `assigned`; `total` is stable.
6. The whole loop runs in one transaction: if any insert fails, **zero** assignment rows are
   committed.
7. `total` is the post-commit `COUNT(*)` of assignments for `(M, A)`.
8. Missing or JSON-`null` `emails` → `422 "emails is required"`; an empty array is accepted
   and returns `assigned=0, skipped=0, total=<current>`.
9. Any unknown top-level field → `400 "invalid JSON"` (`DisallowUnknownFields`).

Pinned by `TestPACE023PreviewThenConfirmWireContract` (idempotent repeat + unknown email),
`TestPACE023ConfirmAssignmentsIsIdempotentAndTeamIsolated`,
`TestPACE023ConfirmAssignmentsRollsBackAtomically`.

### I7. Degraded analytics — unavailable FreeBusy must not fabricate data

For an **external** member (`member_user_id IS NULL`), `engine.GetMemberWeek`:

1. Sets `data_available = true` **only** when the FreeBusy query succeeds and the result's
   `Coverage == "known"`. Any error, any empty result, or any other coverage value leaves
   `data_available = false`.
2. When `data_available == false`, all three minute counters stay at their zero value. A
   caller must therefore read `data_available` and must never interpret `0` as "measured
   zero".
3. `focus_minutes` is **always** 0 for an external member — FreeBusy carries no focus signal
   — even when `data_available == true`.
4. `free_minutes = max(0, 2400 - busy_minutes)` where 2400 = 5 days × 8 h.
5. The degraded result is still written to `team_analytics_cache` with
   `data_available = false` and is served from cache for 4 hours.
6. The roster **keeps every member** when FreeBusy is unavailable: a 10-member external
   roster returns 10 members, each with `this_week.data_available == false` and
   `last_week.data_available == false`. Degradation never shortens the roster.

For a **Paceday** member (`member_user_id` set):

7. `user_profiles.analytics_shared_with_manager` is consulted first
   (`COALESCE(..., true)`). When consent is false, no `analytics_weeks` read happens and
   `data_available` stays false — consent withdrawal is indistinguishable on the wire from
   missing data, by design.
8. `data_available = true` only when the `analytics_weeks` row for that exact
   `week_start` exists.

Handler-level:

9. `GET /api/manager/team` substitutes a zero-valued `MemberWeekStats` (not `null`) when the
   engine returns `nil` or an error — the engine error is **discarded**, so an engine outage
   is reported as `data_available: false`, never as a 5xx.
10. `focus_trend_pct` is always finite: clamped to `[-200, 200]`, rounded to one decimal,
    `200` when prior is 0 and current is non-zero, `0` when both are 0.
11. `GET /api/manager/analytics` returns exactly 12 week buckets per member regardless of
    availability, newest first, each carrying its own `data_available`.

Pinned by `TestPACE023RealEnginePreservesExternalRosterWhenFreeBusyUnavailable` (invariant 6
and the `data_available == false` half of 1–2). Invariants 3, 7, 8, 11 and the
`focus_trend_pct` clamp are **not currently covered by a contract test** — good candidates
for new ones.

### I8. Weekday conversion is a bijection on the 1..7 wire range

`POST`/`PATCH` a zone with wire `dayOfWeek = d` (1..7) stores `d == 7 ? 0 : d` and reads it
back as `stored == 0 ? 7 : stored`. Wire `0` and `8` are rejected `422`.
`FindSlots` compares against `int(day.Weekday())`, i.e. the *stored* convention, so zone
matching is consistent with storage, not with the wire.

Pinned by `TestNoMeetingZone_ValidatesAndConvertsWeekdayAtBoundary`.

### I9. Formal-team invite lifecycle

1. `ExpireOldInvites` runs before **both** the invite preview and the accept, so an invite
   past `expires_at` is reported `410 "invite is expired"`, never `200`.
2. Accept requires `strings.EqualFold(user.email, invite.invitee_email)`; otherwise `403`.
3. Accept inserts membership with role `"member"` via `ON CONFLICT DO NOTHING`, so
   re-accepting cannot demote an existing owner.
4. Invite creation never checks for an existing pending invite for the same email — there is
   no uniqueness constraint on `(team_id, invitee_email)`, so duplicates accumulate.
5. Adding a manager roster member never creates an invite
   (`TestManagerTeam_AddMemberCreatesOnlySelectedTeamAssignment` asserts `invites == 0`).

### I10. Email path parameters

`{email}` segments carry raw email addresses.

1. Clients MUST percent-encode: `@` → `%40` (both `src/api/manager.ts`
   (`encodeURIComponent`) and the Go tests use `report%40example.com`).
2. The handler applies `url.PathUnescape` to the chi param and falls back to the raw value
   when unescaping fails (a lone `%` does not 400).
3. The handler does **not** lowercase the path email; storage does
   (`lower(trim(email))` in every `…ByEmail` function), so lookup is case-insensitive.
4. A `+` in the local part is preserved by `PathUnescape` (path semantics, not query
   semantics), so `a+b@example.com` works unencoded — but `%2B` also works.
5. A `/` in an email would break routing entirely; `%2F` is rejected by Go's mux before the
   handler sees it. Not reachable for RFC-valid addresses.

### I11. Email normalisation and validation

`normalizeManagerEmail` = `lower(trim(raw))`, then `net/mail.ParseAddress`, then a strict
equality check `parsed.Address == email`. Therefore `"Sam <sam@co.com>"` is **rejected**
(422) even though it parses — only bare addresses are accepted. The DB backs this with
`CHECK (member_email = lower(btrim(member_email)))` on both
`manager_team_members` and `manager_team_member_assignments` (migration 023).

---

## (c) `x-uncertain` list

Two `x-uncertain` markers are in `teams.yaml`, plus the open questions below.

1. **`NoMeetingZone.startTime` / `endTime` response format** (`x-uncertain` on both fields).
   Requests are validated strictly as `HH:MM`. Responses come from a Postgres `TIME` column
   scanned into a Go `string`; the driver's text form is expected to be `"HH:MM:SS"`, but no
   test asserts the response string and I did not run the DB. Request and response formats
   may legitimately differ.

2. **`GET /api/teams/{id}` → `404 "not found"` reachability** (`x-uncertain` on the response).
   `requireMember` runs before `GetTeam`, and a `team_members` row cannot outlive its team
   (FK + `ON DELETE CASCADE`), so a missing team should always produce `403` first. The
   `404` branch is documented because the code exists, but I believe it is dead.

3. **`x-uncertain` candidates I chose to document as fact instead** — flagging here so a
   reviewer can push back:
   - `ManagerTeamMemberGoStruct.ID`/`CreatedAt`/`UpdatedAt` being Go zero values on the 201
     add response. This follows from the handler encoding the same `*ManagerTeamMember` it
     passed to `Upsert…` (which never writes back into the struct), but no test decodes that
     body.
   - `TeamDetail.members` and `TeamAnalytics.member_breakdown` being JSON `null` rather than
     `[]`. Follows from nil slices without `omitempty`; no test asserts the raw JSON.
   - `TeamMember.name`/`email` being **absent** (not `""`) for a nameless user, due to
     `omitempty` on a `string`.

4. **Not determinable from this repo**: whether `engine.FreeBusyService.Query` can return
   `Coverage` values other than `"known"` / unknown-ish, and therefore whether invariant
   I7.1 has more than two branches. Owned by the freebusy domain.

5. **`GET /api/manager/gaps` team filtering** uses the in-place slice trick
   `filtered := gaps[:0]`, which aliases and overwrites the engine's backing array. Correct
   here because the engine result is not reused, but fragile. Not a wire concern; noted for
   whoever writes the test.

6. **`months := 3` / `_ = months`** in `getAnalytics` is dead-ish: `numWeeks := months * 4`
   uses it, so the window is 12 weeks, but the `_ = months` line suggests an abandoned
   `months` query parameter. No such parameter is parsed today; the spec documents 12 as
   fixed.
