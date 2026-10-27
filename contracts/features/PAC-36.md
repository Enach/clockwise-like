# Contract — PAC-36: malformed JSON is a 400 on the two lenient POSTs, and week-mode compression stops dropping failed days

- **Linear issue**: [PAC-36](https://linear.app/paceday/issue/PAC-36)
- **Spec**: none (see §0)
- **Stage**: contract-author, then contract-challenger
- **Fragment edited**: `contracts/openapi/paths/scheduling.yaml` (plus its companion
  `scheduling.notes.md`, items 4 and 5)
- **Bundle**: regenerated with `python scripts/openapi_assemble.py`; generated Go and
  `MIGRATION.md` by the api gate (`make openapi` in an LF clone). §8 records what was
  run and what it printed.
- **Manifest**: `contracts/features/PAC-36.yaml`

---

## 0. There is no spec, and the challenger should say so

There is no `docs/specs/PAC-36.md`. The issue is a defect bundle from the API audit
(epic E1 in `docs/factory/api-audit.md`). The contract-author definition says to stop
when there is no accepted spec. I went ahead for the same reasons PAC-21 did
(`contracts/features/NEW-pac21-error-leaks-remaining.md` §0):

- the run that started this stage asked for the contract;
- the run supplied four acceptance criteria that are already Given/When/Then and can
  be falsified;
- each criterion maps to one audit finding whose current behaviour I read in the
  handler.

This contract is written against those four criteria, the PAC-36 issue body and
findings API-024, API-034 and API-035. This is a deviation from the factory. I am
recording it here, not hiding it. The decisions in §4 are what a spec would have had
to settle. The hard rule says to record factory-assumed decisions in the spec's
Decisions section. With no spec file, they are recorded in §4 and in the manifest,
marked "assumed by the factory, pending owner confirmation".

### Scope: three findings, not nine

The issue body lists nine findings. The acceptance criteria this stage was given
cover three of them. The other six are out of scope here, and the contract does not
change them:

| Finding | State on `main` | Why not here |
|---|---|---|
| API-025 (`workingHours.days` casing) | Fixed by PAC-24 | Already done |
| API-053 (`days` alias empties the list) | Reported | Booking domain. No acceptance criterion |
| API-077 (`/api/freebusy` accepts end <= start) | Reported | Calendar domain. No acceptance criterion |
| API-059 (no-meeting-zone PATCH is a full replace) | Reported | Teams domain. No acceptance criterion. Changes PATCH semantics, which needs an owner decision |
| API-079 (focus routes do not snap to Monday) | Reported | No acceptance criterion. Changes which week the user gets |
| API-080 (zero `range_start`/`range_end` serialised) | Reported | No acceptance criterion |

Putting them into this contract would change behaviour that no criterion asks for
(contract-author rule 1). The manifest's `followUps` names them so triage can file
them separately or widen PAC-36 on purpose.

## 1. What changes at the HTTP boundary

| Operation | Before | After | Finding |
|---|---|---|---|
| `runFocus` `POST /api/focus/run` | A body that does not decode is silently treated as `{}`. The answer is 200 for the current week. | A present, non-`null` body that does not decode into `FocusRunRequest` gets a **400 `invalid JSON`**. Absent, empty, whitespace-only and `null` bodies are still `{}`. | API-035 |
| `previewCompression` `POST /api/schedule/compress` | The decode error is discarded (`_ = json.NewDecoder(...)`). The answer is 200 for today. | Same rule as `runFocus`: **400 `invalid JSON`**. | API-035 |
| `previewCompression`, week mode | Every day whose engine call fails is skipped. The answer is 200 with the remaining days, or `[]` if all five failed. | If **any** day fails, the whole request fails the way single-day mode fails: **500 `internal error`**, or 401 `calendar not connected` if that day's error is "no calendar". No per-day results are returned. A week-mode 200 always carries **exactly five** entries, Monday to Friday. | API-034 |
| `previewCompression` 200 schema | `type: array` | `type: array, minItems: 1, maxItems: 5`. Single-day mode returns exactly 1 entry and week mode exactly 5. `[]` can no longer be expressed. | API-034 |
| `confirmNaturalLanguage` `POST /api/nlp/confirm` | Already fixed on `main` by PAC-21 (#220): a negative index gets a 400 `invalid selected_slot_index`, and the schema has `minimum: 0`. | **Unchanged.** AC-1 is already met. The plan pins it with the existing test and adds nothing. | API-024 / U-06 (resolved by PAC-21) |

No operation is added or removed. No new schema is added. Response schemas change
only through the new `minItems`/`maxItems` on the `previewCompression` 200. The
`FocusRunRequest` and `CompressionRequest` descriptions change. Their shapes do not
(`additionalProperties: true` is kept, see §3.4).

### What "does not decode" means, exactly

The server uses Go's `json.Decoder.Decode` into the request struct, as every sibling
endpoint does. A body is malformed, and gets the 400, when Decode returns an error
other than `io.EOF`:

| Body | Result |
|---|---|
| absent, zero bytes, only whitespace | `io.EOF` → treated as `{}` → 200 path |
| `null` | decodes without error into a zero struct → 200 path |
| `{}`, `{"week":"2030-01-07"}`, an object with unknown members | 200 path (unknown members ignored, as today) |
| `{`, `not json`, `{"week":` | syntax error → **400 `invalid JSON`** |
| `[]`, `"2030-01-07"`, `5`, `true` | cannot unmarshal into an object → **400 `invalid JSON`** |
| `{"week":5}`, `{"date":true}` | wrong member type → **400 `invalid JSON`** |
| `{"week":"bad"}` | decodes, then fails the date parse → 400 `invalid week format…`, unchanged |
| `{} trailing garbage` | only the first value is read → 200 path, unchanged (see §3.3) |

The message is exactly `invalid JSON`. It is the literal `applyCompression`,
`confirmNaturalLanguage` and `createScheduledMeeting` already use. PAC-21 removed
decoder text from every 4xx body, and this keeps to that.

## 2. Why the interface is shaped this way

### 2.1 Absent and `null` bodies stay valid

`requestBody.required` is `false` on both operations and stays `false`. Two existing
consumers depend on it:

- **MCP `clockwise_run_focus_engine`** (`mcp/tools.go:146`) posts `nil`, which
  `json.Marshal` turns into the literal body `null`. If `null` became a 400, the MCP
  tool would break.
- **The web client** (`smart-calendar-flow/src/api/client.ts:1025`) posts `{ week }`.
  When `week` is undefined, `JSON.stringify` writes `{}`.

The criterion is about *malformed* JSON. An absent body and `null` are well formed,
and they mean "no options". So they keep their meaning.

### 2.2 A failing week day fails the whole request (500), not a partial 200

The criterion says the error must be "reported in the response, not dropped". I
considered four ways to report it:

1. **Chosen: fail the request the way single-day mode fails.** Single-day mode
   already answers 500 `internal error`, or 401 for no calendar, when its one day
   fails. Week mode now does the same when any of its five days fails. The response
   schemas do not change (the 401 and 500 are already documented for this
   operation), and consumers already handle these errors. The operation is a
   read-only preview, so failing it destroys nothing: the user can retry or preview
   one day at a time.
2. **Rejected: 200 with a per-day error entry** (for example a `CompressionResult`
   with an `error` member, or `oneOf` result|error). This changes the 200 item
   schema that the web's `CompressionResult[]` and the MCP tool's pass-through rely
   on. A client that ignores the new member would show a failed day as "nothing to
   compress", which is the defect being fixed. It would also need a fixed per-day
   error vocabulary that nobody has specified.
3. **Rejected: 207 Multi-Status.** It is a WebDAV status with no standard JSON shape.
   Generated clients (`openapi-typescript`, `typed-openapi`) treat it as an untyped
   2xx, so the failure is easy to miss again.
4. **Rejected: a header naming the failed days** (for example
   `X-Paceday-Failed-Days`). Generated types do not surface it, and no current
   consumer reads headers. That is "dropped" with extra steps.

Option 1 loses the days that did succeed. I accepted that because the alternatives
either break the 200 shape or hide the failure again. Recorded as decision D-1 (§4).

### 2.3 Exactly five entries, and the schema says so

Before this change, a week-mode 200 with fewer than five entries meant "some days
failed". After it, a 200 always has five, because `SuggestForDay` returns an entry
with no proposals for a non-working day (`engine/compression.go`, the
`!workEnabled` branch), not an error. Writing `minItems: 1` / `maxItems: 5` makes a
shortened array impossible to express. That follows the contract-author rule:
"prefer making the wrong request impossible to express". The schema cannot tie 1 to
single-day mode and 5 to week mode, so the description says it.

### 2.4 First failure stops the loop

The plan may stop at the first failing day or compute all five and then fail. The
contract cannot tell the two apart, because neither returns per-day data. The
preview has no side effects either way. I chose not to pin it.

## 3. Alternatives rejected for the malformed-JSON half

### 3.1 Treat an empty body as malformed too

Rejected. It breaks the MCP tool (§2.1), and it contradicts `required: false`, which
consumers have relied on since the contract was reverse-engineered.

### 3.2 A body-specific message such as `invalid JSON: week must be a string`

Rejected. PAC-21 does this only for `patchSettings`, whose body has 40+ members. Here
each body has one or two optional string members. A fixed `invalid JSON` is the
sibling convention, and it leaks no decoder text.

### 3.3 Reject trailing data (`{} garbage`)

Rejected for this ticket. No sibling endpoint rejects trailing data. Changing it on
two endpoints alone would make them stricter than the other ~40 POST/PATCH bodies,
and no criterion asks for it. If wanted, it is a codebase-wide decision for its own
issue.

### 3.4 `additionalProperties: false` on the two request bodies

Rejected. The server ignores unknown members, and the contract describes what is
(factory §7 rule 2). Rejecting unknown members would be a separate behaviour change
that no criterion asks for. The web's `{ week }` would survive it, but other callers
might not.

## 4. Decisions (assumed by the factory, pending owner confirmation)

- **D-1.** A week-mode compression preview in which any day fails is answered as the
  single-day failure: 500 `internal error`, or 401 `calendar not connected`. The days
  that succeeded are not returned. This is the most conservative option. It deletes
  no data, widens no visibility, changes no 200 schema, and matches the existing
  single-day behaviour. *Assumed by the factory, pending owner confirmation.*
- **D-2.** An absent, empty or `null` body on `runFocus` and `previewCompression`
  stays valid and means `{}`. Only a present, non-`null`, non-decodable body is the
  400. *Assumed by the factory, pending owner confirmation.*
- **D-3.** PAC-36 is scoped to API-024 (already fixed), API-034 and API-035. The
  other six findings in the issue body are follow-ups (§0). *Assumed by the factory,
  pending owner confirmation.*

## 5. Consumers, and what happens to them

| Consumer | Effect |
|---|---|
| `smart-calendar-flow` `client.ts:1025` `focusRun` | Always sends a well-formed object. No change. |
| `smart-calendar-flow` `client.ts:1161` `compressionPreview` | Sends `{date?, week?}`, always well formed. In week mode a failing day now throws `ApiHttpError` (500) where it used to resolve with a shorter array. `withFallback` rethrows non-unreachable errors, so the caller sees the error and no mock data. No UI component calls `compressionPreview` today (only `contract.ts:73` declares it). |
| `smart-calendar-flow` `src/api/generated` | Descriptions and `minItems`/`maxItems` change. Regenerate after this contract merges (factory §1). No hand edit. |
| `mcp/tools.go:146` `clockwise_run_focus_engine` | Posts `null`. Still 200 (D-2). |
| `mcp/tools.go:177` `clockwise_compress_schedule` | Posts `{}` or `{date}`. Single-day only. No change. |
| `e2e/tests/focus-time.spec.ts`, `seam.spec.ts` | Post `{ week }` / `{ date }`, well formed. No change. |
| `backend/api/handlers_schedule_test.go` `TestCompress_Week` | The mock returns a result for every day. Still 200, now with exactly 5 entries. |
| `backend/api/pac21_calendar_not_connected_test.go` | Asserts a 401/500 before any day is computed. Unchanged. |
| `backend/api/empty_arrays_test.go:35` `TestCompress_WeekEveryDayFails_EncodesEmptyArray` (PAC-28) | **Breaks.** It pins week-mode 200 `[]` when every day fails. The same input (`{"week":"2025-01-06"}`, `mockCompressor{err: ...}`) now gets 500 `{"error":"internal error"}` (AC-3). The implementer re-asserts it as that 500; it is not deleted or skipped. The file is in `backend.allowedPaths` for this reason only; the other PAC-28 empty-array tests in it are unchanged. |

No consumer sends a malformed body on purpose, so nothing breaks there. The only
observable change for a well-formed caller is a week-mode preview with a failing day:
it now gets an error instead of a short 200. One existing test pins the old
behaviour (`TestCompress_WeekEveryDayFails_EncodesEmptyArray`, above) and is
re-asserted, not removed.

## 6. Acceptance criteria, as they apply at the HTTP boundary

The four criteria this stage was given, with the boundary detail added:

- **AC-1** Given `POST /api/nlp/confirm` with `selected_slot_index: -1`, then 400
  `{"error":"invalid selected_slot_index"}` and no panic. *Already true on `main`.*
  Pinned by `TestPAC21ConfirmNegativeSlotIndexIs400` (`backend/api/pac21_4xx_bodies_test.go`).
  The contract is unchanged.
- **AC-2** Given a malformed body (each row marked 400 in the §1 table) on `runFocus`
  and on `previewCompression`, then 400, `Content-Type: application/json`, body
  exactly `{"error":"invalid JSON"}`. Given an absent, empty, whitespace-only or
  `null` body, then the 200 path (not a 400).
- **AC-3** Given week-mode `previewCompression` where one day's engine call fails
  with a generic error, then 500 `{"error":"internal error"}`, not 200. Where it fails
  with `ErrCalendarNotConnected`, then 401 `calendar not connected`. Given no day
  fails, then 200 with exactly five entries whose `date` values are Monday to Friday
  of the requested week, in order.
- **AC-4** api-gate prints rc=0 on the final commit.

All four can be expressed at the boundary. AC-2 and AC-3 are handler unit tests
against the existing `mockCompressor` / `mockFocusEngine` seams (the plan names
them). No e2e scenario is required: the e2e stand-in cannot make one weekday fail on
its own, and the well-formed journeys are unchanged.

## 7. `x-uncertain` and audit findings

- `runFocus`, `previewCompression`: carry no `x-uncertain`. Nothing to resolve.
- `confirmNaturalLanguage` is not edited by this contract. Its 200 references
  `GoogleCalendarEvent`, which still carries U-14. That marker is not this feature's
  (factory §7 rule 1 applies to operations a feature modifies).
- Findings resolved by this contract: **API-034** and **API-035**. **API-024** was
  resolved by PAC-21. The implementation PR updates their status in
  `docs/factory/api-audit.md`, as PAC-21 did.

## 8. What was run

- `python scripts/openapi_assemble.py --check` on the host: `OK: bundle is in sync (98 paths)`.
- `api-gate.sh factory/pac-36-contract` on the first contract commit: `make openapi`
  regenerated `backend/api/gen/paceday.gen.go` (the embedded spec only), which is
  committed with this revision. Every other step printed rc=0 (openapi-check,
  backend and mcp lint, backend and mcp test, backend coverage 54.9% against a 43%
  floor, mcp 84.0% against 75%).
- The gate is re-run on the final commit of this branch. Its output is in the
  Linear comment on PAC-36 and in the contract PR body.

## Challenge — 2026-10-26

**Verdict**: accept-with-changes

Reviewed on `factory/pac-36-contract` at `26a3b8a`, committed state only, against
`handlers_focus.go`, `handlers_schedule.go`, `engine/compression.go`, the tests in
`backend/api`, `mcp/`, `e2e/tests` and `smart-calendar-flow` `main` at `8179a33`.
The schema change itself holds. The one blocking finding is a consumer the manifest
does not list.

### Requests wrongly permitted

None found. Every row of the §1 "does not decode" table was run through
`json.NewDecoder(...).Decode` into the handlers' own body struct (a Go scratch
program using the same decoder settings as both handlers):

| Body | Decode result | Contract says |
|---|---|---|
| empty, `   \n` | `io.EOF` | `{}`, 200 path. Matches |
| `null`, `{}`, `{"x":1}`, `{"week":null}`, `{} garbage` | no error | 200 path. Matches |
| `{`, `{"week":`, `nul` | `unexpected EOF` (not `io.EOF`) | 400. Matches |
| `not json` | syntax error | 400. Matches |
| `[]`, `"2030-01-07"`, `5`, `true` | cannot unmarshal into struct | 400. Matches |
| `{"week":5}`, `{"date":true}` | `UnmarshalTypeError` | 400. Matches |
| `{"week":"a","week":5}` | `UnmarshalTypeError` | 400. Consistent with the stated rule |

### Responses the client cannot handle

None blocking.

1. (non-blocking) The `previewCompression` mock fallback in `smart-calendar-flow`
   `src/api/client.ts:1162` returns `[]` in mock mode. The new `minItems: 1` means
   the server can no longer send that. Nothing breaks today: no UI component calls
   `compressionPreview`, and only `src/api/contract.ts:73` declares it. But a future
   screen written against the regenerated types may treat `[]` as impossible while
   mock mode still produces it. This is a follow-up for the frontend stage, not a
   contract change.
2. (non-blocking) A week-mode 500 from `previewCompression` now says nothing about
   which day failed and carries no per-day data. D-1 accepts this. It is recorded
   here so the owner who confirms D-1 sees the cost: one transient provider error on
   any of the five days loses the whole preview.

### Consumers affected

- **BLOCKING**: `clockwise-like/backend/api/empty_arrays_test.go:35`
  `TestCompress_WeekEveryDayFails_EncodesEmptyArray`. It asserts **200 with body
  `[]`** in week mode when every day's engine call fails (`mockCompressor{err: ...}`,
  `{"week":"2025-01-06"}`). This contract removes exactly that response: the 200
  schema is now `minItems: 1`, and the 500 description now covers week-mode engine
  failures. The manifest's `consumersAffected` lists `TestCompress_Week` and
  `pac21_calendar_not_connected_test.go` but not this test. `allowedPaths` covers
  only "tests beside the two handlers", and this file is not one of them. If this is
  left as is, the implementer must either change a PAC-28 test the plan did not
  authorise or appear to delete one to go green (factory hard rule).
  **Fix (manifest and doc only, no schema change)**: add to `consumersAffected`
  `{ "consumer": "clockwise-like backend/api/empty_arrays_test.go:35 TestCompress_WeekEveryDayFails_EncodesEmptyArray", "breaks": "yes", "detail": "Pins week-mode 200 [] (PAC-28). Re-asserted, not deleted: the same input becomes 500 {\"error\":\"internal error\"} per AC-3." }`.
  Add `backend/api/empty_arrays_test.go` to `backend.allowedPaths`, and add the row
  to the §5 consumer table.
- (non-blocking) `mockCompressor` in `backend/api/handlers_schedule_test.go:28`
  returns the same `(result, err)` for every `SuggestForDay` call, so it cannot
  express AC-3's "one weekday's engine call fails". When unset (`mockCompressor{}`)
  it also returns a `nil` result, which the handler would encode as a `null` array
  item that `CompressionResult` does not permit. The plan must name a per-day seam.
  The contract does not need to change.
- Checked and not affected:
  - `mcp/tools.go:146` posts `nil`, which `mcp/client.go:69` marshals to `null`. It
    still gets 200.
  - `mcp/tools.go:177` posts `{}` or `{date}`.
  - `e2e/tests/focus-time.spec.ts:61,120,189` and `seam.spec.ts:198,211,226` post
    well-formed objects and use single-day compression only.
  - Web `client.ts:1025` sends `{ week }`, and `JSON.stringify` drops an undefined
    `week`. Web `client.ts:1161` is also unaffected.
  - `QuickActions.tsx:35` and `Dashboard.tsx:179` call `runFocus` with a string or
    undefined.

### Contract/implementation divergences

The contract describes behaviour after implementation. On this branch the handlers
still behave the old way:

- `handlers_focus.go:23-25` resets `week` on a decode error.
- `handlers_schedule.go:25` discards the decode error.
- `handlers_schedule.go:62-64` `continue`s past a failing day.

That is the expected stage-2 state, not a finding. But until the backend PR merges,
the bundle on `main` describes behaviour the server does not have yet. The contract
PR and the backend PR should therefore merge close together (factory §7 rule 2).

There is no divergence in the parts the contract claims are unchanged:

- `SuggestForDay` (`engine/compression.go:74-77`) returns a non-nil result with
  `Date = day.Format("2006-01-02")`, and empty proposals for a non-working day. So
  "exactly five, Mon..Fri, in order" is achievable. `startOfWeek`
  (`handlers_schedule.go:128-134`) maps Sunday to the previous Monday and works in
  the parsed (UTC) location.
- `writeError` (`handlers_settings.go:388-392`) sets `application/json`, so AC-2's
  Content-Type assertion can pass.
- AC-1 is already met:
  - `TestPAC21ConfirmNegativeSlotIndexIs400` exists
    (`backend/api/pac21_4xx_bodies_test.go:268`).
  - `NLPConfirmRequest.selected_slot_index` has `minimum: 0`.
  - The handler answers `invalid selected_slot_index` (`handlers_nlp.go:52`).

### Uncertainties not actually resolved

None claimed (`resolvesUncertain: []`). Neither edited operation carries an
`x-uncertain`. U-14 correctly stays with `confirmNaturalLanguage`, which is not
edited.

### Process findings (non-blocking)

1. There is no `docs/specs/PAC-36.md`, so stage 1 never ran (factory §2). D-1 to D-3
   therefore live in this document instead of a spec's Decisions section. All three
   are the conservative choice: no data is deleted, no visibility is widened, and no
   200 schema changes. Before the plan stage, one of these should happen:
   - the factory creates the spec with these decisions under "Decisions" ("assumed
     by the factory, pending owner confirmation"), or
   - the owner confirms them on PAC-36.
2. The server has some tolerances the contract does not mention. All of them exist
   already, and PAC-36 does not change them:
   - member names match case-insensitively (`{"Week":"2030-01-07"}` takes the 200
     path);
   - `{"week":null}` is accepted, though `week` is not nullable;
   - a UTF-8 BOM before `{` gets a 400.
   None of them needs a change here.

### Verified clean

- `bash .claude/factory/api-gate.sh factory/pac-36-contract` on `26a3b8a`, as printed:
  - `make openapi rc=0`, `regenerated: none`
  - `OK: bundle is in sync (98 paths)`
  - `OK: MIGRATION.md covers all 119 operations (0 generated)`
  - `OK: backend/api/gen matches the contract`, `openapi-check(api) rc=0`
  - `backend lint rc=0`, `mcp lint rc=0`
  - `backend test rc=0`, `mcp test rc=0`
  - backend coverage 54.9% (floor 43%) `rc=0`; mcp coverage 84.0% (floor 75%)
    `rc=0`

  The web half of openapi-check was not run, because the web types regenerate after
  merge (§1).
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` gives
  `d048a3c937fdb68ad930d2558ec70592815f78c17c9bf941a41c0373a4b90d0b`, which matches
  the manifest's `contractHash`.
- The only schema change in the bundle diff is `minItems: 1` and `maxItems: 5`.
  Everything else is descriptions. Both operations stay `handwritten` in
  `MIGRATION.md`, and `backend/` has no request-validation middleware, so the new
  bounds constrain nothing at runtime.
- Both operations keep `requestBody.required: false`, and both request schemas keep
  `additionalProperties: true`. No well-formed caller found in web, MCP or e2e is
  newly rejected.
- The four acceptance criteria in the manifest match §6 with no narrowing. AC-2's
  malformed list is a subset of the §1 table, and every entry was decoded above.
- The rejected alternatives were rejected for reasons that hold on the code. §2.2
  option 2 would change the 200 item that MCP passes through and that the web types
  describe. §3.1 would break `mcp/tools.go:146`.

## Revision — 2026-10-26 (answers challenge round 1)

The bundle (`contracts/openapi/openapi.yaml`) is unchanged by this revision, so
`contractRevision` stays `1` and `contractHash` stays
`d048a3c937fdb68ad930d2558ec70592815f78c17c9bf941a41c0373a4b90d0b`. Only the
manifest and this document change.

### Blocking finding 1: unlisted consumer `TestCompress_WeekEveryDayFails_EncodesEmptyArray`

**Accepted.** The challenger is right: `backend/api/empty_arrays_test.go:35`
(PAC-28) asserts week-mode 200 `[]` when every day fails, which is exactly the
response this contract removes (200 is now `minItems: 1`; a failing week day is a
500). It was missed because the consumer search covered tests beside the two
handlers and not the cross-cutting PAC-28 file.

Fixed, manifest and doc only, no schema change:

- `consumersAffected` gains
  `backend/api/empty_arrays_test.go:35 TestCompress_WeekEveryDayFails_EncodesEmptyArray`
  with `breaks: yes`: re-asserted as 500 `{"error":"internal error"}` per AC-3,
  never deleted or skipped.
- `repositories.backend.allowedPaths` gains `backend/api/empty_arrays_test.go`,
  scoped to re-asserting that one test.
- §5 gains the row, and its closing paragraph names the break.

### Non-blocking findings

- `mockCompressor` cannot fail one weekday, and an unset mock returns a `nil`
  result. Noted for the plan stage, which must name a per-day seam for AC-3. No
  contract change.
- Missing `docs/specs/PAC-36.md` (process finding 1): unchanged by this revision.
  D-1 to D-3 stay "assumed by the factory, pending owner confirmation" in §4 and the
  manifest. Either the spec is created or the owner confirms on PAC-36 before the
  plan stage.
- Existing server tolerances (process finding 2): out of scope, unchanged.

## Challenge — 2026-10-26 (round 2)

**Verdict**: accept

Reviewed on `factory/pac-36-contract` at `c79a8dc`, committed state only. Checked
against `backend/api/handlers_focus.go`, `handlers_schedule.go`, `server_errors.go`,
`engine/compression.go`, the tests in `backend/api`, `mcp/tools.go`, `e2e/tests`,
and `smart-calendar-flow` `origin/main` at `8179a33`. The round-1 blocking finding
is fixed. No new blocking finding.

### Round-1 blocking finding: closed

`backend/api/empty_arrays_test.go:35`
`TestCompress_WeekEveryDayFails_EncodesEmptyArray` is now in `consumersAffected`
with `breaks: yes`. It is re-asserted as 500 `{"error":"internal error"}`, not
deleted. The path is in `backend.allowedPaths`, and §5 has the row. A repo-wide grep
of `/api/schedule/compress` and `/api/focus/run` across `backend/`, `mcp/` and `e2e/`
finds no other test that pins a week-mode 200 with fewer than five entries, or a
malformed body answered 200. Every existing test posts a well-formed body:

- `handlers_focus_test.go:41,61,76,91`
- `handlers_schedule_test.go:58,73,87,102,116`
- `interfaces_contract_test.go:98`
- `pac21_calendar_not_connected_test.go:92-95,212`

### Requests wrongly permitted

None. I ran each body through `json.NewDecoder(...).Decode` into the
`previewCompression` body struct (`{Date, Week string}`), in an independent Go
scratch program. The rule is: error other than `io.EOF` gives 400, otherwise the
200 path.

| Body | Decode | Contract |
|---|---|---|
| empty, `"  \n"` | `io.EOF` | `{}`. Matches |
| `null`, `{}`, `{"x":1}`, `{"week":null}`, `{} garbage`, `{"Week":"2030-01-07"}` | nil | 200 path. Matches (the last is an existing case-insensitive tolerance) |
| `{`, `{"week":` | `unexpected EOF` | 400. Matches |
| `not json` | syntax error | 400. Matches |
| `[]`, `"2030-01-07"`, `5`, `true` | cannot unmarshal into struct | 400. Matches |
| `{"week":5}`, `{"date":true}` | `UnmarshalTypeError` | 400. Matches |
| `{"date":"2030-01-07","week":5}` | `UnmarshalTypeError`. Go still fills `date` | 400. Matches, *provided the handler checks the error before reading `date`* |
| `{"week":"bad"}` | nil | 400 `invalid week format`, unchanged. Matches |
| UTF-8 BOM + `{}` | invalid character | 400. Consistent with the rule. Today a BOM body gets 200, so this is new, but the rule covers it |

### Responses the client cannot handle

None blocking.

- `previewCompression` 200 `minItems: 1, maxItems: 5`. I generated the web
  artefacts with the web repo's pinned generators (`openapi-typescript` 7.4.4,
  `typed-openapi` 4.1.0 `--runtime zod --schemas-only`, the flags in
  `scripts/openapi-gen.sh`), from both bundles, in a scratch directory:
  - The main bundle reproduces the committed `src/api/generated/types.ts`
    byte for byte.
  - From the branch bundle, `types.ts` changes in JSDoc descriptions only. There
    are no tuple types, because the script passes no `--array-length`.
  - `schemas.ts` is unchanged apart from the output-file import name. The 200 is
    an inline array, so no zod schema carries the new bounds.

  So the post-merge regeneration cannot break a type check in the web repo.
- The real `SuggestForDay` (`engine/compression.go:56-120`) returns either an
  error or a non-nil result. A non-working day is a result with empty
  `proposals`, not an error. So `[null]` and a short array cannot come from the
  production engine. They can come only from the test double `mockCompressor{}`
  (round 1, non-blocking).
- Web `client.ts:1162` mock fallback `() => []` is now outside the 200 schema.
  It is mock mode only, and no screen calls `compressionPreview`. Round-1
  non-blocking item, still open for the frontend stage.

### Consumers affected

No change to the manifest's list. Re-verified:

- `mcp/tools.go:146` posts `nil`, which becomes the literal `null`. It stays on the
  200 path (D-2).
- `mcp/tools.go:169-178` posts `{}` or `{"date": <user string>}`. The value is
  always a string, so the request is never a decode failure. A bad date is still
  the 400 `invalid date format`, unchanged.
- `mcp/tools.go:308` `decodeCompressionProposals` reads a single-day array and is
  unaffected by the bounds.
- `e2e/tests/seam.spec.ts:226` uses single-day compression.
  `focus-time.spec.ts:61,120,189` and `seam.spec.ts:198,211` post `{ week }`.
- Web `client.ts:1025` (`{ week }`) and `:1161` (`{date?, week?}`) send well-formed
  objects. `client.ts` validates no compression response with zod. Its only
  generated-schema import is `CalendarDisconnectUnsupported` / `SettingsUpdate`
  (`client.ts:2`).

### Contract/implementation divergences

None in what the contract claims is unchanged:

- `writeCalendarFailure` (`server_errors.go:75-81`) maps only
  `ErrCalendarNotConnected` (via `errors.Is`) to the 401. Everything else becomes
  the 500 `internal error`. So "401 or 500, nothing else" is exactly what the
  week-mode failure path can return.
- The handler's behaviour changes (`handlers_focus.go:23-25`,
  `handlers_schedule.go:25` and `:62-64`) are the expected stage-2 gap, as recorded
  in round 1.

One non-blocking inaccuracy in history text: the `previewCompression` description
and `CompressionRequest` say a non-decodable body "answered 200 for today". That
is not always true, because Go's decoder keeps filling the other members after an
`UnmarshalTypeError`:

- `{"date":"2030-01-07","week":5}` was answered 200 for **2030-01-07**;
- `{"week":"2030-01-13","date":true}` was answered 200 in **week mode** for that
  week.

The post-PAC-36 behaviour (400) is stated correctly. Only the "before" clause is
too broad. Optional wording fix, no schema change.

### Uncertainties not actually resolved

None claimed. One note for completeness: `runFocus`'s 200 (`FocusRunResult`,
`scheduling.yaml:1136-1140`) carries a prose "x-uncertain at field level" on
`skippedDays`. It is not an `x-uncertain` key, so the contract gate does not see
it, and PAC-36 does not change that response. §7's "carry no `x-uncertain`" is
therefore accurate for the key. A strict reading of factory §7 rule 1 ("touch it,
fix it") could ask for it to be resolved. It is non-blocking here because this
contract touches only the request side and the error responses of `runFocus`.
Worth a line in the follow-ups if the owner reads the rule strictly.

### Process findings (non-blocking, carried from round 1)

- There is still no `docs/specs/PAC-36.md`. D-1 to D-3 are recorded only in §4
  and in the manifest. Before the plan stage, either the spec is created or the
  owner confirms on PAC-36.
- An observation for the owner who confirms D-1: `SuggestForDay` builds
  `dayStart` from the UTC date components in the user's location. It reads the
  working window from `date.In(loc)`, which for a negative UTC offset is the
  previous calendar day. This is a pre-existing engine behaviour, outside this
  contract. It can make a working Monday look like a non-working day (an empty
  entry), which a five-entry 200 now presents as a computed result. Suggest a
  triage follow-up. It does not affect the contract shape.

### Verified clean

- `bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/pac-36-contract`
  on `c79a8dc` printed, verbatim:
  - `make openapi rc=0`, `regenerated: none`
  - `OK: bundle is in sync (98 paths)`
  - `OK: MIGRATION.md covers all 119 operations (0 generated)`
  - `OK: backend/api/gen matches the contract`, `openapi-check(api) rc=0`
  - `backend lint rc=0`, `mcp lint rc=0`
  - `backend test rc=0`, `mcp test rc=0`
  - `backend coverage: 54.9% (floor 43%)`, `backend coverage rc=0`
  - `mcp coverage: 84.0% (floor 75%)`, `mcp coverage rc=0`

  Script exit 0. The web gate and the e2e gate were not run. The manifest assigns
  them to after the merge, and the generated web diff was checked by hand above
  instead.
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` gives
  `d048a3c937fdb68ad930d2558ec70592815f78c17c9bf941a41c0373a4b90d0b`, which matches
  `contractHash`.
- The fragment diff against `origin/main` (`scheduling.yaml`,
  `scheduling.notes.md`) and the regenerated `backend/api/gen/paceday.gen.go` were
  read in full. The only structural change is `minItems: 1` / `maxItems: 5`.
  `requestBody.required: false` and `additionalProperties: true` are kept on both
  operations.
- `API-034` and `API-035` (`docs/factory/api-audit.md:292-293`) each name exactly
  the operations this contract changes, so `resolvesFindings` does not overclaim.
  `API-024` is already `Fixed (PAC-21)`.
- The manifest `acceptanceTests` AC-1 to AC-4 match §6 with no narrowing.
