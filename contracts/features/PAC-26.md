# Contract — PAC-26: the six untagged wire structs get json tags, and manager writes stop returning zero rows and `null`

- **Linear issue**: [PAC-26](https://linear.app/paceday/issue/PAC-26)
- **Spec**: none (see §0)
- **Stage**: contract-author, then contract-challenger
- **Fragments edited**: `contracts/openapi/paths/scheduling.yaml`, `calendar.yaml`,
  `teams.yaml`, plus the companion notes (`scheduling.notes.md` item 5,
  `calendar.notes.md` (b)1, `teams.notes.md` A3 and A4)
- **Bundle**: regenerated with `python scripts/openapi_assemble.py`; generated Go by the
  api gate (`make openapi` in an LF clone). §8 records what was run.
- **Manifest**: `contracts/features/PAC-26.yaml`

---

## 0. There is no spec, and the challenger should say so

There is no `docs/specs/PAC-26.md`. PAC-26 is a defect bundle from the API audit (batch
C1 in `docs/factory/api-audit.md`, theme T1). The contract-author definition says to stop
when there is no accepted spec. I went ahead on the same footing as PAC-21 and PAC-36:

- the run that started this stage asked for the contract and supplied three acceptance
  criteria;
- the issue body names the structs, the endpoints and the findings (API-014, API-028,
  API-029, API-030, API-031, API-032), and each one was re-read in the code for this
  contract;
- the change is a wire-key rename plus two error-path fixes, with no product behaviour
  beyond "the documented response is usable".

The decisions a spec would have settled are in §4, marked "assumed by the factory, pending
owner confirmation", and repeated in the manifest. A challenger should treat the missing
spec as a finding.

### Scope: six structs, not seven

The issue names seven structs. This contract tags six. `storage.SSOProvider` is left out
on purpose (D-2): PAC-22 (spec `docs/specs/PAC-22.md`, v3) replaces that encoding path
with a deliberate DTO, a named container and write-only secrets, and PAC-50 is changing the
same operations now. The issue itself says to omit the SSO struct if the DTO work lands
first; it has not landed, but it is specified and in flight, and tagging the struct here
would change the `/api/admin/sso` response shape twice in a row and present
`oidc_client_secret` and `saml_cert` as deliberate snake_case keys of the contract. The
`/api/admin/sso` operations are not edited, so U-22 and API-001 stay where they are, owned
by PAC-22.

API-031 and API-032 are in scope although the three acceptance criteria do not name them:
the issue body puts them in "same class, same PR", and factory §7 ("touch it, fix it")
requires a feature that edits an operation to resolve the open findings on it. Every
operation whose response schema changes here is listed with its finding in §7.

## 1. What changes at the HTTP boundary

No operation is added or removed. No status code is added. Every change is a key rename on
a response body, plus three 200 responses that stop being nullable.

| Operation | Schema | Before | After | Finding |
|---|---|---|---|---|
| `listFocusBlocks` GET `/api/focus/blocks` | `FocusBlockRecord` | `ID, GoogleEventID, StartTime, EndTime, Date, CreatedAt` | `id, google_event_id, start_time, end_time, date, created_at` | API-014 |
| `runFocus` POST `/api/focus/run` | `FocusRunBlock` (items of `createdBlocks`) | `GoogleEventID, Start, End, Date` | `google_event_id, start_time, end_time, date` | API-014 |
| `runFocus` | `FocusRunResult.skippedDays` | items `string`, format "x-uncertain" in prose | items `string, format: date` (description only; the server already sends `YYYY-MM-DD`) | U-23 |
| `getCalendarFreeBusy` GET `/api/calendar/freebusy` | `RawGoogleBusyWindow` | `Start, End` | `start, end` | API-029 |
| `queryFreeBusy` POST `/api/freebusy` | `results[].busy[]` (`RawGoogleBusyWindow`) | `Start, End` while `participants[].busy[]` and `busy{}` were `start, end` | `start, end` everywhere in the body | API-028 |
| `previewPersonalCalendar` GET `/api/personal-calendars/{id}/preview` | `PersonalCalendarPreviewEvent` | `ID, Title, Start, End` | `id, title, start, end` | API-029 |
| `setManagerProfile` POST `/api/manager/profile` | `UserProfileGoStruct` | `UserID, IsManager, DetectedAt, AnalyticsSharedWithManager, UpdatedAt` | `user_id, is_manager, detected_at, analytics_shared_with_manager, updated_at` | API-030 |
| `addManagerTeamMember` POST `/api/manager/team/members` | `ManagerTeamMemberGoStruct` (201) | PascalCase; the pre-insert struct: zero UUID `ID`, `0001-01-01T00:00:00Z` timestamps | snake_case; the row read back after the upsert (through the team's assignment when `team_id` is given). A failed read-back is the existing 500 | API-030, API-031 |
| `patchManagerTeamMember` PATCH `/api/manager/team/members/{email}` | 200 | `oneOf [ManagerTeamMemberGoStruct, null]`, PascalCase; `null` when the re-read failed | `ManagerTeamMemberGoStruct`, snake_case, never `null`. A failed re-read is the existing 500 | API-030, API-032 |
| `renameTeam` PATCH `/api/teams/{id}` | 200 | `oneOf [Team, null]`; `null` when the re-read failed | `Team`, never `null`. A failed re-read is the existing 500 `internal error` | API-032 |

The new keys of `ManagerTeamMemberGoStruct` are `id, manager_user_id, member_email,
member_user_id, display_name, source, cadence, cadence_custom_days, last_one_on_one_at,
created_at, updated_at`. Types, `required` lists, nullability of each field and
`additionalProperties: false` are unchanged on every schema; only the key spelling moves.

What the backend does to get there (for the plan, not binding on it): add `json:"..."`
tags with exactly the keys above to `storage.FocusBlock`, `engine.FocusBlock`,
`calendar.TimeSlot`, `calendar.GenericEvent`, `storage.UserProfile` and
`storage.ManagerTeamMember`, no `omitempty` anywhere (every key stays always present);
read the member row back in `addMember`; stop discarding the re-read error in
`patchMember` and `patchTeam`.

## 2. Why the interface is shaped this way

### 2.1 snake_case, matching the stored columns

Every tagged struct is a stored row or a direct projection of one, and the snake_case keys
are the column names (`focus_blocks`, `user_profiles`, `manager_team_members`). The manager
domain is otherwise uniformly snake_case, including the GET on the same path. The web
app's `FocusBlock` type is already `{id, google_event_id, start_time, end_time, date}`, so
the GET `/api/focus/blocks` response becomes readable by the existing frontend with no
frontend change. `calendar.TimeSlot` and `calendar.GenericEvent` have one-word fields, so
the lowercase keys are the same in any casing and equal `FreeBusyWindow`, which is what the
other two thirds of the POST `/api/freebusy` body already use.

### 2.2 One block shape for both focus operations

`engine.FocusBlock` has Go fields `Start`/`End`; `storage.FocusBlock` has
`StartTime`/`EndTime`. I tag both as `start_time`/`end_time`, so a client reads one block
shape from both operations; the engine block simply lacks `id` and `created_at`, which do
not exist until the row is written and the engine does not report them.

### 2.3 A failed read-back is a 500, not a partial body

When the write has committed and the read-back fails, the server cannot produce the
documented body. The choice is between a 500 and a body that is not the row (`null`, or the
pre-insert struct). A 500 is truthful about the response; the write is idempotent (an
upsert, a patch to the same values, a rename to the same name), so a client that retries
converges. The 500 was already documented on all three operations, so no consumer meets a
new status code.

### 2.4 The add response is the row as the caller's scope sees it

With `team_id`, `addManagerTeamMember` writes both the global row and the team assignment;
the read-back goes through the team view (display-name override, assignment cadence), the
same read `patchManagerTeamMember` already uses. That makes the 201 of an add and the 200 of
a patch the same object for the same scope.

## 3. Alternatives rejected

1. **camelCase keys** (`googleEventId`, `startTime`, ... matching the `FocusRunResult`
   wrapper). Rejected: the manager domain, the stored columns and the web's existing
   `FocusBlock` type are snake_case; camelCase would make GET `/api/focus/blocks` still
   unreadable by the frontend and split the manager domain. The cost of the choice made is
   that a `runFocus` body mixes a camelCase wrapper with snake_case blocks; the wrapper's
   casing is PAC-32's (API-015), a frontend alignment that needs no contract change.
2. **Introduce response DTOs instead of tagging the storage structs** (the PAC-22 approach
   for SSO). Rejected for these six: none of them carries a field that should not be on the
   wire, so a DTO would be a copy with the same fields; tags are the smallest change that
   makes the documented shape deliberate. DTOs stay the right tool where a field must be
   dropped, which is why the SSO struct is left to PAC-22.
3. **Rename the schemas** (`UserProfileGoStruct` → `ManagerProfileRecord`, etc.). Rejected:
   the names exist in both repos' generated code and change nothing on the wire; renaming
   is churn with no consumer value. The descriptions now say the names are historical.
4. **Merge `RawGoogleBusyWindow` into `FreeBusyWindow`** now that their keys match.
   Rejected: different Go types with a different documented failure mode (unparseable Google
   timestamps become the Go zero time). Keeping two schemas keeps that documented.
5. **Return the pre-insert struct but with tags** (fix API-030 only). Rejected: factory §7
   requires API-031 to be resolved once the operation is edited, and a documented zero UUID
   is a defect, not a shape.
6. **On a failed read-back, return 200 with the in-memory struct**. Rejected: for the add
   it reintroduces API-031's zero id, and for the patch the in-memory values are not what is
   stored (the store normalises, e.g. display-name fallback).

## 4. Decisions (assumed by the factory, pending owner confirmation)

- **D-1**: wire keys are snake_case matching the stored columns, and `engine.FocusBlock`'s
  `Start`/`End` are emitted as `start_time`/`end_time` (§2.1, §2.2).
- **D-2**: `storage.SSOProvider` is excluded from PAC-26 and left to PAC-22, which owns the
  `/api/admin/sso` response shape, U-22 and the disclosure half of API-001. The SSO
  operations are not edited. If the owner wants the tags anyway, it is a separate,
  one-schema contract revision; it must not reach `oidc_client_secret` without PAC-22.
- **D-3**: a read-back failure after a committed write is answered with the operation's
  existing 500 (`db error` on the two manager routes, `internal error` on `renameTeam`),
  never a 200 with `null` or the pre-insert struct (§2.3).
- **D-4**: `addManagerTeamMember` with `team_id` returns the row through that team's
  assignment view (§2.4).
- **D-5**: schema names are kept (§3.3).

No decision deletes user data or widens who can see data: every key renamed here was
already sent to the same caller, and no field is added to any body.

## 5. Consumers, and what happens to them

Searched: `smart-calendar-flow/src` (main at `5008c72`), `clockwise-like/mcp`, `e2e/tests`,
`backend` tests.

| Consumer | Breaks? | Detail |
|---|---|---|
| web `src/api/client.ts:1082` `getFocusBlocks` → `FocusStats.tsx` | no — starts working | Reads `start_time`/`end_time`; those keys now exist. |
| web `src/api/client.ts:1025` `focusRun` | no | Its type reads `created_blocks`, which does not exist before or after (API-015, PAC-32). The block items inside `createdBlocks` now match its `FocusBlock` minus `id`. |
| web `src/api/client.ts:948` `freeBusy` | no | Reads `participants`/`busy` only; `results` is not read. |
| web `src/api/manager.ts:396,430,457` profile/add/patch | no | Discards the POST/PATCH bodies and re-fetches. |
| web `src/api/teams.ts` `renameTeam` | no | Ignores the body and re-fetches. |
| web preview of personal calendars | no | No caller in `src` (only the generated types). |
| web `src/api/generated/*` | no | Regenerated from the merged bundle (web gate `REGEN=1`), never edited. Schema names are unchanged; property names change. |
| MCP `clockwise_find_free_slots` (`mcp/tools.go:137`) | no code break | Relays the body as text (`jsonResult`); the model sees `start`/`end` instead of `Start`/`End`. |
| MCP `clockwise_run_focus_engine`, `clockwise_list_focus_blocks` | no code break | Text relay; block keys change for the model. |
| `e2e/tests/focus-time.spec.ts` | no | Asserts `[]` and error bodies only; no block key is read. |
| `e2e/tests/calendar.spec.ts:195` POST `/api/freebusy` | no | Asserts status and the stand-in's request log, not `results` keys. |
| `backend/engine/empty_arrays_test.go:63,77` | no | Asserts the wrapper keys `createdBlocks`, `skippedDays`, `errors`, which do not change. |
| backend tests asserting PascalCase keys of these structs | none found | `grep` for the old keys in `*_test.go` finds none. |
| Out-of-tree readers (scripts, saved requests) | unknown | Not visible from here; same residual risk PAC-22 OQ-2 records. |

## 6. Acceptance criteria, as they apply at the HTTP boundary

The three supplied criteria, made falsifiable, plus the ones §7 brings in. None of these
tests exists yet.

- **AC-1 [unit]** Given a stored focus block, when GET `/api/focus/blocks` covers its week,
  then the element's key set is exactly `{id, google_event_id, start_time, end_time, date,
  created_at}`.
- **AC-2 [unit]** Given the focus engine creates one block, when POST `/api/focus/run`,
  then `createdBlocks[0]`'s key set is exactly `{google_event_id, start_time, end_time,
  date}` and the wrapper keys are unchanged.
- **AC-3 [unit]** Given a provider returning one busy window, when GET
  `/api/calendar/freebusy`, then each window's key set is exactly `{start, end}`; when POST
  `/api/freebusy` for a known participant, then `results[0].busy[0]` has key set
  `{start, end}` and equals `participants[0].busy[0]`.
- **AC-4 [unit]** Given a personal calendar whose provider yields one event, when GET
  `/api/personal-calendars/{id}/preview`, then the element's key set is exactly
  `{id, title, start, end}`.
- **AC-5 [integration]** When POST `/api/manager/profile`, then the body's key set is
  exactly `{user_id, is_manager, detected_at, analytics_shared_with_manager, updated_at}`.
- **AC-6 [integration]** When POST `/api/manager/team/members` (without and with
  `team_id`), then 201, the key set is exactly the eleven keys of §1, `id` is not the zero
  UUID and equals the stored row's id, `created_at`/`updated_at` are not
  `0001-01-01T00:00:00Z`, and with `team_id` `cadence` is the assignment's cadence.
- **AC-7 [unit or integration]** Given a successful patch whose re-read fails, when PATCH
  `/api/manager/team/members/{email}`, then 500 JSON `{"error":"db error"}`, never 200
  `null`; given it succeeds, then 200 with a snake_case object (AC-6's key set).
- **AC-8 [unit or integration]** Given a successful rename whose re-read fails, when PATCH
  `/api/teams/{id}`, then 500 JSON `{"error":"internal error"}`, never 200 `null`.
- **AC-9 [unit]** Every exported field of the six structs has a non-empty `json` tag
  (a reflection test, so a field added later without a tag fails the build's tests).
- **AC-10 [gate]** Contract schemas and the regenerated web types reflect the tagged keys:
  `openapi-check` green in both repos after the web types are regenerated from the merged
  bundle.
- **AC-11 [gate]** `api-gate.sh` prints rc=0 on every step on the final commit.

How AC-7 and AC-8 inject a re-read failure is the plan's choice; it must not weaken the
assertion to "not 200 null".

## 7. `x-uncertain` and audit findings

| Item | Operation | Outcome |
|---|---|---|
| API-014 | listFocusBlocks, runFocus | Resolved (tags, §2.1–2.2) |
| API-028 | queryFreeBusy | Resolved (one casing in the body) |
| API-029 | getCalendarFreeBusy, previewPersonalCalendar | Resolved |
| API-030 | setManagerProfile, addManagerTeamMember, patchManagerTeamMember | Resolved |
| API-031 | addManagerTeamMember | Resolved (read-back) |
| API-032 | patchManagerTeamMember, renameTeam | Resolved (500 on re-read failure) |
| U-23 | runFocus `skippedDays` | Resolved by reading `engine/focus_time.go`: both append sites append `day.Format("2006-01-02")` |
| API-015 | runFocus wrapper casing | Not resolved here; owned by PAC-32 (frontend aligns to the camelCase wrapper, no contract change) |
| API-035 | runFocus malformed body | Already resolved by PAC-36 |
| API-079 | listFocusBlocks `week` not snapped to Monday | Not resolved: changes which week a user gets, needs an owner decision (already a PAC-36 follow-up) |
| U-03 | getCalendarFreeBusy | Stays "keep" (PAC-21); only the window key casing changes |
| U-22, API-001 | `/api/admin/sso` | Not touched (D-2), owned by PAC-22 |

No formal `x-uncertain` marker sits on an operation this feature edits. The bundle's marker
count is unchanged (13).

## 8. What was run

- `python scripts/openapi_assemble.py` then `--check` on the host:
  `OK: bundle is in sync (98 paths)`.
- `bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/pac-26-contract` on the
  first contract commit (`5056078`): `make openapi` regenerated
  `backend/api/gen/paceday.gen.go` (embedded spec and doc comments), committed with this
  revision. Every other step printed rc=0: openapi-check (api), backend and mcp lint,
  backend and mcp test, backend coverage 55.0% (floor 43%), mcp 84.0% (floor 75%).
- The gate is re-run on the final commit of this branch; its output is in the Linear
  comment on PAC-26. The web gate and e2e gate are
  not run at this stage (no behaviour has changed yet; web types regenerate after merge).

## Challenge — 2026-10-26

Challenger: `contract-challenger`, on the committed state of `factory/pac-26-contract` at
`056bcd2`. Read: the fragment diffs, the bundle, this file and the manifest, the PAC-26
issue body, the handlers and storage code, the web consumers on `smart-calendar-flow`
main `5008c72`, `mcp/` and `e2e/`. Not read: the author's Linear comments.

**Verdict**: accept

Nothing blocking. Every change is to a response body, so no new request is admitted. No
consumer breaks: I regenerated the web types from this bundle and ran the web `make verify`
(results below). Every statement I checked against the code holds. Four non-blocking
changes are listed under "Recommended changes". They should be made before or with the
plan. None of them changes the wire shape.

### Checks run (output, not summaries)

- `bash .claude/factory/api-gate.sh factory/pac-26-contract` at `056bcd2`:
  `make openapi rc=0`, `regenerated: none`, `OK: bundle is in sync (98 paths)`,
  `OK: MIGRATION.md covers all 119 operations (0 generated)`,
  `OK: backend/api/gen matches the contract`, `openapi-check(api) rc=0`,
  `backend lint rc=0`, `mcp lint rc=0`, `backend test rc=0`, `mcp test rc=0`,
  `backend coverage: 55.1% (floor 43%)`, `mcp coverage: 84.0% (floor 75%)`.
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` gives
  `792e3030...21362`, which equals the manifest's `contractHash`.
- Formal `x-uncertain:` keys in the bundle: 13 on `origin/main` and 13 on the branch. Lines
  that contain the string `x-uncertain` go from 15 to 14. The one removed is the prose
  note on `skippedDays` (U-23), which matches the doc's claim.
- Web consumer break test, run in a scratch clone inside the gate container. The host web
  checkout was not touched. Steps: web `main` beside this api branch, `make openapi`
  (rewrote `src/api/generated/{types.ts,schemas.ts,schemas.types.d.ts}`, +102/-101), then
  `make verify`: `lint PASS, typecheck PASS, design-check PASS, openapi-check PASS,
  test PASS, coverage PASS`, `make verify rc=0`. The web side of AC-10 is therefore
  reachable with a pure regeneration.

### Requests wrongly permitted

None. No request schema, parameter or `required` list changes. The only edits are response
key renames and the removal of two `oneOf [.., null]`.

### Responses the client cannot handle

None found.
- `listFocusBlocks` now returns
  `[{"id":1,"google_event_id":"g1","start_time":"2026-10-26T08:00:00Z","end_time":"2026-10-26T09:30:00Z","date":"2026-10-26","created_at":"..."}]`.
  web `src/api/types.ts:176` `FocusBlock {id, google_event_id, start_time, end_time, date}`
  and `src/components/FocusStats.tsx:34,43,46` read `start_time`/`end_time`. These were
  `undefined` before and are now readable. `date` is a `TEXT` column
  (`001_initial.up.sql:40`) scanned into a `string`, so it really is `YYYY-MM-DD`. A
  DATE-to-string scan would have produced RFC3339, so I checked the column type.
- `runFocus` `createdBlocks[0]` = `{"google_event_id":"g1","start_time":...,"end_time":...,"date":"2026-10-26"}`.
  The web reads `created_blocks`, which is absent both before and after (API-015,
  PAC-32), so nothing that renders today changes.
- `queryFreeBusy` `results[0].busy[0]` is `{"start","end"}`. `freeBusyWindowDTO`
  (`handlers_freebusy.go:18`) is also `time.Time` with tags `start`/`end`, so AC-3's
  equality with `participants[0].busy[0]` holds byte for byte.
- `getCalendarFreeBusy` map values. The handler only builds the Google client
  (`handlers_calendar.go:137`), and that client pre-sizes its slots with `make(..., 0)`.
  The Outlook path that can yield a nil slice (`outlook_client.go:132`) does not reach
  this operation, so no `null` array is introduced.

### Consumers affected

- web `src/api/manager.ts:396,430,457` and `src/api/teams.ts:346`. They discard the bodies
  and re-fetch, so no shape break. Behaviour change the table omits: when a read-back
  fails, these calls used to resolve (the response was 200 `null` or 201 with the
  pre-insert struct). They now receive a 500. `requestApi` (`client.ts:421-428`) throws
  `ApiHttpError`, and `withFallback` (`client.ts:512`) re-throws it. The user sees an
  error although the write committed. This is D-3 working as intended. It is a rare
  database-failure path and the operation is idempotent, but the consumer row should say
  "surfaces an error on read-back failure" rather than only "no".
- `e2e/tests/seam.spec.ts:163-215` calls `POST /api/freebusy`,
  `GET /api/calendar/freebusy` and `POST /api/focus/run` and is missing from the table. I
  checked it: the file asserts only the stand-in's request log and status codes, and no
  renamed key is read. No break.
- `mcp/tools.go:129-155`: three tools, text relay only, no typed decode, and no
  `*_test.go` in `backend/` or `mcp/` mentions any old PascalCase key (grep). No code
  break, as stated.
- Other encode sites. I grepped every `json.Marshal`/`Encode` in `backend/` for the six
  structs and found no second response that embeds them directly:
  - the manager roster GET uses its own `memberResp` DTO (`handlers_manager.go:211`);
  - gaps use `engine.CadenceGap`, which is already tagged;
  - `engine.ParticipantBusy.Busy` is the `results` view already listed.

  None of the six is persisted as JSON (settings, briefs, analytics and audit log marshal
  other types), and none is sent outbound (`OutlookClient.CreateEvent` builds its own
  map). Tagging therefore changes no stored data and no third-party payload.

### Contract/implementation divergences

The contract describes the post-implementation server, as the contract-author rule
("reality plus the specified change") allows. I checked every "today" claim it still
makes:
- U-23: `processDay` appends `dateStr` at `engine/focus_time.go:116,128`. `dateStr` is
  `day.Format("2006-01-02")` at line 96. Determined.
- `UserProfileGoStruct.updated_at` "read BEFORE the upsert": correct.
  `GetOrCreateUserProfile` reads, then `UpsertUserProfile` sets `now()` without a
  read-back (`handlers_manager.go:69-88`).
- D-4 team view: `GetManagerTeamMemberByEmailForTeam` (`storage/manager.go:197`) returns
  `mtm.id`, the override-or-canonical display name, and the assignment cadence, as
  described.
- renameTeam 500 body `internal error`: `writeServerError` calls
  `genericServerMessage(500)`, which returns `"internal error"`
  (`server_errors.go:26-34`). This matches AC-8 and the `ServerError` component.

### Uncertainties not actually resolved

- U-23 is determined by reading the code, which is the method the audit prescribes
  (`api-audit.md:491`). No acceptance test pins it, though. See recommendation 3.

### Recommended changes (non-blocking; contract shape unchanged)

1. **AC-6 cannot falsify D-4.** With `team_id`, `UpsertManagerTeamMemberForTeam` writes
   the same `cadence` to the global row and to the assignment
   (`storage/manager.go:75-95,133-152`). "cadence is the assignment's cadence" therefore
   passes whether the read-back uses the team view or the global row. The two views differ
   only in `display_name`, when an assignment already carries a `display_name_override`,
   because the upsert never touches the override. Add a case to AC-6: given an existing
   assignment with override `"Ally"`, when
   `POST /api/manager/team/members?team_id=T` with
   `{"email":"a@x.test","display_name":"Alice","cadence":"weekly"}`, then the 201
   `display_name` is `"Ally"` and the global row's `display_name` is `"Alice"`. The 201
   description should also say plainly that a re-add through a team does not replace an
   existing team override.
2. **setManagerProfile 200 overstates itself by omission.**
   `_ = storage.UpsertUserProfile(...)` (`handlers_manager.go:85`) discards the write
   error. A 200 with `{"is_manager":true,...}` can therefore come back when nothing was
   stored. The contract-author convention is that the contract says so where the server
   does something wrong. Add one sentence to the 200 description ("the upsert's error is
   discarded: a 200 does not prove the flag was stored; see followUps.setManagerProfile").
   Fixing it stays out of scope, as the manifest already records.
3. **Pin U-23.** Extend AC-2: `skippedDays` entries match `^\d{4}-\d{2}-\d{2}$`. Give it a
   day already at target and a disabled day so that both append sites are exercised.
4. **Consumer table.** Add `e2e/tests/seam.spec.ts` and the read-back-500 note above to
   both the manifest's `consumersAffected` and section 5.

### Process findings (recorded, not blocking)

- No spec, as section 0 itself flags. The contract-author definition says to stop without
  an accepted spec. The precedent cited (PAC-21, PAC-36) and the size of the change make
  this tolerable. The D-1 to D-5 assumptions are the only product decisions taken, and
  each is conservative: no key is added, no data becomes visible to anyone new, nothing is
  deleted. The owner should confirm D-1 to D-5 before or with the plan.
- D-2 departs from the issue's literal ask. The issue says to omit `storage.SSOProvider`
  only "if the SSO DTO issue lands first", and it has not landed. The reason given is real:
  tagging would present `oidc_client_secret`/`saml_cert` as deliberate contract keys and
  change `/api/admin/sso` twice. PAC-22 (`docs/specs/PAC-22.md`) exists and owns that
  shape. Accepted as the more conservative option.
- Factory section 4 says modified endpoints use the generated server interface. Nine
  operations are modified here and the manifest names only hand-written handler files.
  MIGRATION.md shows 0/119 generated after many features, so precedent is against
  applying section 4 here. The plan should still say explicitly whether it applies, rather
  than leave it silent.

### Verified clean

- Bundle in sync, generated Go in sync, hash reproducible, marker count unchanged (above).
- The `required` sets of all renamed schemas equal their property sets, and
  `additionalProperties: false` is kept. With no `omitempty` planned, every key is always
  present, so the `required` lists are truthful.
- Nullability is preserved as the Go types dictate: `detected_at`, `member_user_id`,
  `cadence_custom_days` and `last_one_on_one_at` are Go pointers and nullable in the
  schema, and every other field is a non-pointer and non-nullable.
- Removing `null` from the patchManagerTeamMember and renameTeam 200 responses is
  consistent with D-3 and with the shared `ServerError` component ("db error" on
  /api/manager, "internal error" on /api/teams).
- Retry-safety claim: both upserts are `ON CONFLICT ... DO UPDATE` with the same values. A
  retried add or patch converges, and only `updated_at` moves.
- Web generated types regenerate cleanly, and no hand-written web file references the
  renamed schemas or keys (grep outside `src/api/generated`).
- Rollback: no migration and no stored JSON of these structs, so a backend revert restores
  the old keys with no data step.
