# Contract — PAC-21, remaining half: no error text in 4xx bodies, a 4xx for "no calendar", JSON for every 5xx

- **Linear issue**: [PAC-21](https://linear.app/paceday/issue/PAC-21) (stays In Progress until the implementation merges)
- **Factory ticket**: `NEW-pac21-error-leaks-remaining`
- **Spec**: none (see §0)
- **Stage**: contract-author, then contract-challenger
- **Fragments edited**: all six of `contracts/openapi/paths/*.yaml`
- **Bundle and generated Go**: regenerated with `scripts/openapi_assemble.py`,
  `scripts/openapi_migration_report.py` and `scripts/openapi_gen_go.sh`. See §9 for
  what was run.
- **Manifest**: `contracts/features/NEW-pac21-error-leaks-remaining.yaml`

---

## 0. There is no spec, and the challenger should say so

PAC-21 was raised as a solution-shaped issue with "Contract change required: no". It
never had a `docs/specs/PAC-21.md`. Its first half (#217, 5xx bodies made generic) was
implemented directly. The issue's release-manager comment of 2026-10-26 lists what is
left, and the factory ticket turns that into five acceptance criteria. This contract is
written against:

- those five criteria;
- the PAC-21 issue body;
- finding API-054 in `docs/factory/api-audit.md`.

The contract-author definition says to stop when there is no accepted spec. I went
ahead because the run that started this stage asked for the contract to be produced
end to end, and because the criteria are already Given/When/Then and falsifiable. That
is a deviation from the factory, recorded here rather than hidden. If the challenger
wants a spec first, the decisions in §3 are the content it would need.

## 1. What changes at the boundary

| Area | Before | After |
|---|---|---|
| 401 for "no calendar" (17 operations, list in the manifest) | Varied. Text/plain `not connected: <err>`, JSON `not authenticated` or JSON client-construction error text. **Nine operations declared no 401 and answered 500.** | One shared response, `CalendarNotConnected`. Body is `application/json` `{"error":"calendar not connected"}`, and the schema pins that single value with an enum. The requireAuth 401 keeps its text/plain body under the same status. |
| `event not found: <err>` (3 ops, 404) | Google's error text appended | `{"error":"event not found"}` |
| `invalid JSON: <err>` (queryFreeBusy, patchSettings) | Go decoder text, which names Go types | `invalid JSON`. For a type mismatch on patchSettings: `invalid JSON: <property> has the wrong type`, where `<property>` is the JSON path from the request |
| Other err-derived 4xx | `zoom: invalid token: <json err>`, time.Parse text, `autoScheduleCron is invalid: <cron err>` | Fixed sentences, listed per operation |
| Every 5xx (113 responses) | 60 were `text/plain` (56 operations). Many descriptions said the body carried `<err>` or the raw error | All are `application/json` envelopes. Every description names a fixed message, and none says the body carries an error |
| `confirmNaturalLanguage` negative index | Panic: net/http aborts with no response | 400 `invalid selected_slot_index`; request schema `minimum: 0` |

Three schemas change: `CalendarNotConnected` (response) and `CalendarNotConnectedError`
are new; `ServerError` replaces the removed `PlainTextServerError` (teams fragment).
The shared `ErrorResponse` changes only in prose, so the assembler's structural check
still passes.

## 2. Why the interface is shaped this way

### 2.1 401 for "no calendar connected", not 409, 412, 424 or 428

The criterion asks for "the documented 4xx". Eight operations already documented a
401 for exactly this condition:

- `listCalendarEvents`
- `getCalendarFreeBusy`
- `patchEvent`
- `deleteEvent`
- `listRooms`
- `suggestAttendees`
- `addEventConference`
- `removeEventConference`

The web app already handles it. `Dashboard.tsx` treats a 401 from the events read as
"connect a calendar". `AuthContext` signs the user out only on a 401 from
`GET /api/auth/me`, and that operation is untouched. The e2e suite asserts this
401 (`calendar.spec.ts:37-41`).

**Rejected: 409/424/428.** These are semantically better, because a 401 conflates "no
session" with "no calendar". But each one is a status that no consumer handles today.
The criterion says *the documented* 4xx, and introducing one would make the nine
operations that move from 500 behave differently from the eight that already answer 401
for the same condition.

The conflation is handled two ways:
- The two 401s are told apart by Content-Type: the middleware's is text/plain, this
  one is JSON.
- They are also told apart by a body that is a single enumerated value.

### 2.2 One fixed message, enumerated

`calendar not connected` contains `not connected`, so the existing e2e assertion still
holds. Pinning it with `enum` makes a leaking implementation fail the contract tests
rather than pass them, and gives clients a value they can branch on.

**Rejected: keep `not authenticated`.** It is what four of them said, but it reads
as a session problem. It is also the exact text the PAC-21 issue complains about
having to interpret.

### 2.3 A credential-store failure is a 500, not the 401

`getCalendarClient` and `eventHandlers.calClient` currently map *any* token-load error
to 401. The contract separates the two cases:

- no stored token → 401;
- a failure to read the token store → 500.

Reporting a database outage as "connect your calendar" would send the user to re-run
OAuth for nothing.

### 2.4 previewCompression: 401 in both modes

In week mode a no-calendar user got `200 []`. A client cannot tell that apart from
"nothing to compress". Criterion 2 says a calendar-dependent operation gives the 4xx.
Answering 401 in single-day mode and 200 in week mode for the same user would be
incoherent.

**Rejected: leave week mode alone.** It is smaller, but it leaves one calendar-dependent
path that silently reports "no calendar" as success.

### 2.5 Which operations are "calendar-dependent"

An operation counts as calendar-dependent when it *cannot do its job* without the
calendar and fails today when there is none. These are the operations that reach
`newCalOps`, `calOpsForUser` (analytics meetings) or `DetectTeam`, or that build a client
directly, and error on a missing token.

The following degrade on purpose and keep answering 200. Changing them would remove
data users see today:

- `getAnalyticsWeek` and `listAnalyticsTrends` compute without calendar minutes;
- `queryFreeBusy` returns `unknown`;
- habit re-optimisation;
- public booking with a host who has no calendar.

### 2.6 The 5xx envelope keeps existing literal messages

Bodies like `db error`, `internal`, `save user failed` and `slug check failed` are
already fixed strings and leak nothing. The contract changes their Content-Type, not
their words.

**Rejected: normalise every 500 to `internal error`.** It is tidier, but it changes
message text that nothing asked to change. The rule the contract states is the
invariant that matters: `error` is a fixed, server-chosen string, never derived from an
error value.

### 2.7 patchSettings type mismatch names the property

PAC-55's contract promised that a non-string `timeFormat` is a 400 "naming
`timeFormat`". The old message did that by accident, through Go's decoder text, which
also names Go struct and type names. The new message keeps the promise using the JSON
path from the request, which is the client's own vocabulary.

**Rejected: plain `invalid JSON`.** Simpler, but it would silently break that PAC-55
promise.

### 2.8 ServerError replaces PlainTextServerError instead of being renamed in place

Keeping the name `PlainTextServerError` for a JSON response would make the contract
lie. 23 teams and manager operations reference it, and they are updated in the same
change.

## 3. Decisions (assumed by the factory, pending owner confirmation)

PAC-21 has no spec, so there is no Decisions section to record these in. They are
recorded here instead. Each is the most conservative option consistent with PRODUCT.md
and earlier decisions. None deletes user data or widens who can see data.

- **D-1**: The no-calendar response is a 401 with `{"error":"calendar not connected"}`
  (§2.1, §2.2).
- **D-2**: The calendar-dependent set is the 17 operations in the manifest, including
  previewCompression week mode (§2.4, §2.5).
- **D-3**: Existing fixed 5xx literals are kept (§2.6).
- **D-4**: U-03 is resolved as **keep `GET /api/calendar/freebusy` unchanged**. It is
  not dead: the MCP tool `clockwise_find_free_slots` (`mcp/tools.go`) calls it. Deleting
  it would break that consumer. Aligning its shape with `POST /api/freebusy` would also
  break it, and belongs to its own issue.
- **D-5**: U-06 / API-024 is resolved as a **400 for a negative
  `selected_slot_index`**. This is the response the handler already gives every other
  out-of-range index, and the action the audit register prescribes ("fix the code, then
  document 400").

## 4. Consumers

The full list is in the manifest, `consumersAffected`. Three consumers break by design
and must change in the backend PR:

- `e2e/tests/focus-time.spec.ts:62-76`: was 500, becomes 401 JSON.
- `e2e/tests/calendar.spec.ts:236-238` and `e2e/tests/seam.spec.ts:110-112`: assert a
  bare `internal error` text body, which becomes the JSON envelope.
- `backend/api/handlers_settings_autoschedule_test.go:73`: asserts the cron prefix.

The web app needs only regenerated types. One visible effect: a 5xx that used to be
text/plain now shows the server's fixed text (`internal error`) instead of the
client's generic sentence. That is what the 47 operations whose 500 was already JSON
do today. A web follow-up is optional.

## 5. Out of scope

These are deliberately not changed here:

- **4xx bodies written with `http.Error` stay text/plain.** Examples are
  `listCalendarEvents` 400, the daily-recap 400s and the teams `not found`. Their text
  is fixed and leaks nothing. Unifying them on the envelope is the separate
  error-envelope issue.
- **Validation errors whose text is a fixed server sentence are not leaks.**
  Examples are the habits, manager, teams and settings validators. They are unchanged.
- **The out-of-hours allowance refusal stays a 500.** It is now JSON.

## 6. The `x-uncertain` markers on touched operations: a deviation the challenger must rule on

Factory §7 says that a feature which modifies an operation carrying an `x-uncertain`
must resolve it. Criterion 3 forces an edit to the 5xx of almost every operation, so
read literally the rule pulls every open marker into this change.

**Resolved here**, because this change edits those operations' semantics:

- U-03 (`getCalendarFreeBusy`)
- U-06 (`confirmNaturalLanguage`)

**Carried forward, not resolved.** In every case the only edit is the 5xx envelope rule
or the 401/404 bodies:

| Marker | Operation | Why it is not resolved here |
|---|---|---|
| U-01 | `getAnalyticsWeek` 404 | Needs a stub test or deleting the branch. Unrelated to error bodies. |
| U-05 | `getMeetingBrief` 400 | Needs a router test. Unrelated to error bodies. |
| U-09 | `getTeam` 404 | Reachable by a race between `requireMember` and `GetTeam`, so deleting the branch is not obviously right. Unrelated to error bodies. |
| U-04 | `patchEvent` 200 body | A product decision: which side is wrong. |
| U-07 | `suggestMeetingSlots` 15-minute rule | A question for the brief's author. |

The bundle had 18 markers and now has 16. If the challenger reads §7 as covering
envelope-only edits, the alternative is to split criterion 3 by domain so each slice
can carry its own resolutions. That costs six contract revisions for one invariant.

## 7. Rollback

Revert the implementation PRs and keep this contract revision immutable. There is no
migration and nothing stored is touched. A revert restores the following, and no data
is affected:

- text/plain 5xx bodies, which have been generic since #217;
- the 500 for users with no calendar.

## 8. What the implementation must prove

These map to the acceptance criteria in the manifest:

- **AC-1**: Inject a marker string into every former 4xx leak site and assert it never
  reaches the body. Widen the #217 source scan from 5xx to every status.
- **AC-2**: Run the 17 operations as a no-calendar user and get 401 with the exact body.
  The same operations with a token-store failure give a 500.
- **AC-3**: A test over the bundle fails on any non-JSON 5xx or on raw-error prose.
  Remove `writeServerErrorText` and every 5xx `http.Error` from `backend/api`.
- **AC-3b**: `selected_slot_index: -1` gives a 400.

## 9. What was run (factory §3: nothing claimed that was not run)

The checks ran in a worktree of this branch (`C:\Users\jouzu\paceday\wt-pac21-contract`),
on Windows with Git Bash. `make` is not installed on the host, so I ran the scripts that
`make openapi` and `make openapi-check` call one at a time:

| Command | Result |
|---|---|
| `python3 scripts/openapi_assemble.py` | wrote the bundle: 98 paths, 119 operations, x-uncertain 16 |
| `python3 scripts/openapi_migration_report.py` | 119 operations, 0 generated |
| `bash scripts/openapi_gen_go.sh` | wrote `backend/api/gen/paceday.gen.go` |
| `python3 scripts/openapi_assemble.py --check` | `OK: bundle is in sync (98 paths)`, rc=0 |
| `python3 scripts/openapi_migration_report.py --check` | `OK: MIGRATION.md covers all 119 operations (0 generated)`, rc=0 |
| `bash scripts/openapi_gen_go.sh --check` | `OK: backend/api/gen matches the contract`, rc=0 |
| `cd backend && go build ./...` | rc=0 |
| `cd backend && go vet ./api/...` | rc=0 |
| `cd backend && golangci-lint run --config ../.golangci.yml ./...` | rc=0 |
| `PACEDAY_TEST_DB_PORT=15499 ./scripts/test-backend.sh` | rc=0, every package ok |

**Not run:**

- **`api-gate.sh`.** It requires the shared host checkout to be on this branch, and that
  checkout was on another stage's branch (`impl/pac-66b-llm-base-url-backend`), which I
  did not switch. The gate's docker LF-clone regeneration did not run either. The
  `--check` steps above passed on the worktree, whose generated files `.gitattributes`
  pins to LF.
- **Coverage.** Only generated code changed, and the coverage gate excludes it.
- **Web openapi-check and web-gate.** Web types are regenerated after this contract
  merges (factory §1), so the web half reports drift until then.
- **e2e.**
