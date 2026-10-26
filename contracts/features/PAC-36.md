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

No consumer sends a malformed body on purpose, so nothing breaks there. The only
observable change for a well-formed caller is a week-mode preview with a failing day:
it now gets an error instead of a short 200.

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

See the manifest's `gates` block and the commit that records the api-gate run.
`python scripts/openapi_assemble.py --check` printed `OK: bundle is in sync (98 paths)`
on the host before commit.
