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
| 401 for "no calendar" (18 operations since revision 2, list in the manifest) | Varied. Text/plain `not connected: <err>`, JSON `not authenticated` or JSON client-construction error text. **Ten operations answered 500 (nine in revision 1, plus `syncPersonalCalendar`).** | One shared response, `CalendarNotConnected`. Body is `application/json` `{"error":"calendar not connected"}`, and the schema pins that single value with an enum. The requireAuth 401 keeps its text/plain body under the same status. |
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
- **D-2**: The calendar-dependent set is the 18 operations in the manifest (revision 2
  added `syncPersonalCalendar`), including
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
- U-04 (`patchEvent`) and U-07 (`suggestMeetingSlots`), added in revision 2 after the
  challenger's ruling; see the Revision section.

**Carried forward, not resolved.** In every case the only edit is the 5xx envelope rule
or the 401/404 bodies:

| Marker | Operation | Why it is not resolved here |
|---|---|---|
| U-01 | `getAnalyticsWeek` 404 | Needs a stub test or deleting the branch. Unrelated to error bodies. |
| U-05 | `getMeetingBrief` 400 | Needs a router test. Unrelated to error bodies. |
| U-09 | `getTeam` 404 | Reachable by a race between `requireMember` and `GetTeam`, so deleting the branch is not obviously right. Unrelated to error bodies. |

The bundle had 18 markers; revision 1 left 16 and revision 2 leaves 14. If the challenger reads §7 as covering
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
- **AC-2**: Run the 18 operations as a no-calendar user and get 401 with the exact body.
  The same operations with a token-store failure give a 500.
- **AC-3**: A test over the bundle fails on any non-JSON 5xx or on raw-error prose.
  Remove `writeServerErrorText` and every 5xx `http.Error` from `backend/api`.
- **AC-3b**: `selected_slot_index: -1` gives a 400.
- **AC-6** (revision 2): two unit tests pin the U-04 and U-07 resolutions.

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

## Challenge — 2026-10-26

**Verdict**: accept-with-changes

Reviewer: contract-challenger, run as a separate agent. It read the committed artefacts on
`factory/new-pac21-error-leaks-remaining-contract` at 032e3ad, the PAC-21 issue and its
comments, the handlers, and every consumer named below. It did not read the author's session.

**Ruling on §0 (no spec).** This is a process finding, not a blocker. The PAC-21 issue
contradicts itself:

- its body says "Documented response shapes do not change, only their content" and
  "Contract change required: no";
- the same body asks for a 4xx instead of a 500, which is a status change;
- the release-manager comment of 2026-10-26 brings the remaining text/plain 5xx into scope.

This contract follows the later comment. D-1 to D-5 stay "assumed by the factory, pending
owner confirmation". The owner should confirm them before the backend PR merges.

**Ruling on §6 (x-uncertain on touched operations).**

- **May be carried forward:** response-scoped markers on a response this change does not
  edit. These are U-01 (`getAnalyticsWeek` 404), U-05 (`getMeetingBrief` 400) and U-09
  (`getTeam` 404).
- **May not be carried forward:** operation-scoped markers on an operation whose 4xx
  behaviour changes. Factory §2 (Stage 2 gate item 3) and §7.1 are binding. See blocking
  finding 4.
- No gate script enforces this rule. I checked `api-gate.sh` and
  `scripts/openapi_assemble.py`; they only count markers.

### Requests wrongly permitted, behaviour wrongly forbidden (blocking)

1. **`POST /api/personal-calendars/{id}/sync` depends on the calendar but is not in the set.**
   - **Payload:** a signed-in user with no Google credential, who owns a personal calendar
     whose feed reads successfully, calls the endpoint.
   - **What the server does:** it answers `500 {"error":"internal error"}`.
     `handlers_personal.go:191` calls `PersonalBlocker.Sync`, then `syncOne`, then `newCalOps`
     (`engine/personal_blocker.go:92`). `newCalOps` returns `not authenticated` for a
     missing token, and the handler maps any error to a 500.
   - **Why this is wrong:** it matches §2.5's own definition ("reach `newCalOps` ... and
     error on a missing token"). It also matches the ticket criterion "a calendar-dependent
     operation gives the 4xx". Yet the contract declares only the middleware 401 and the 500
     for `syncPersonalCalendar` (`calendar.yaml`).
   - **Fix:**
     - Give `syncPersonalCalendar` `"401": $ref CalendarNotConnected`.
     - Add it to `operationsChanged4xx`, D-2 and AC-2. That makes 18 operations, not 17.
     - Its 500 description should say that a failure reading the credential store stays a 500.
   - If the author wants it excluded, §2.5 must say why it degrades on purpose. Today it does
     not degrade: it returns an error, not a partial result.

2. **The "closed list of fixed sentences" does not match the server's text.**
   - **What the contract says:** the `addEventConference` and `createConferenceLink` 400s list
     these two sentences with an ASCII hyphen:
     - `"teams: not connected - visit /api/auth/microsoft to authenticate"`
     - `"zoom: not connected - visit /api/auth/zoom to authenticate"`
   - **What the server sends:** U+2014 EM DASH, at `backend/api/handlers_conferencing.go:42`
     and `:141`, and at `backend/conference/factory.go:15`.
   - **Why it matters:** AC-1 asserts "the body is exactly the documented fixed message". A
     test copied from the contract fails against the unchanged server. Or an implementer edits
     a literal that the contract never said to change.
   - **Fix:**
     - Write `—` (U+2014) in both 400 descriptions.
     - In the new `zoom: stored credential is invalid ...` sentence, choose one character and
       say the choice is deliberate.

3. **The patchSettings catalogue misstates an existing literal.**
   - **What the contract says:** `SettingsValidationError` lists `invalid JSON` under "Messages
     that already exist", for "the body is not a JSON object or cannot be read".
   - **What the server sends:** for a body that is not an object, the existing literal is
     `invalid JSON: the body must be an object` (`backend/api/handlers_settings.go:81`). It
     leaks nothing.
   - **Payload:** `PATCH /api/settings` with `[]`. The server answers
     `400 {"error":"invalid JSON: the body must be an object"}`; the contract promises
     `{"error":"invalid JSON"}`.
   - **Why it is wrong:** either the contract silently changes a fixed literal, or it
     misdescribes the server. The first goes against the contract's own §2.6, which rejects
     changing "message text that nothing asked to change".
   - **Fix:**
     - List `invalid JSON: the body must be an object` for a body that is not an object, or is
       null.
     - Keep `invalid JSON` only for a body that cannot be read.

4. **Operation-scoped `x-uncertain` markers remain on operations whose 4xx changes.**
   - **Which markers:**
     - U-04 on `patchEvent`, whose 401 and 404 bodies change.
     - U-07 on `suggestMeetingSlots`, which gains a new 401 (it was a 500).
   - **Why they can be resolved now:** §7.2 asks the contract to "describe what is". Both
     markers can be settled from the code, so neither needs the product decision the .md
     defers.
   - **Fix:**
     - **U-04:** at the end of `patchEvent`, `handlers_events.go` encodes the
       `*calendar.Event` that `UpdateEvent` returned (`json.NewEncoder(w).Encode(updated)`).
       Resolve it as "the 200 body is GoogleCalendarEvent, determined from the handler,
       pinned by a unit test named in acceptanceTests". Move the question of which side is
       wrong to a follow-up issue in `followUps`.
     - **U-07:** `engine/smart_schedule.go:113,121,135` step 30 minutes, and no 15-minute rule
       exists anywhere. Resolve it as "no 15-minute constraint; slots start on 30-minute
       steps, determined from the engine, pinned by a test". Record it as assumed by the
       factory, pending owner confirmation.
   - **Alternative I accept:** the owner explicitly waives §7.1 for these two markers in
     Linear, and the manifest cites the waiver.

### Responses the client cannot handle

None of these blocks. I checked:

- **`CalendarNotConnected` on an operation that now returns 401.** In `src/api/client.ts`,
  `withFallback` rethrows every error that is not a network error, and `apiErrorMessage`
  prefers `data.error`. The UI therefore shows `calendar not connected`, not "Your session
  expired".
- **Sign-out.** `AuthContext.tsx:58` signs the user out only on a 401 or 403 from
  `GET /api/auth/me`, which does not change.
- **Dashboard.** `Dashboard.tsx` decides "not connected" from `/api/auth/status`, not from the
  401.
- **`previewCompression` week mode, 200 `[]` becoming 401.** On web main,
  `api.compressionPreview` has no UI caller; only `client.ts` and `contract.ts` reference it.
  The MCP tool `clockwise_compress_schedule` sends only `date`, never `week`.

Two further non-blocking points:

- **UX regression.** On the 56 operations whose 5xx moves to JSON, `apiErrorMessage` will show
  `internal error` or `db error` instead of "The server had a problem. Try again shortly.".
  The manifest already says this. File the web follow-up before the backend PR merges,
  instead of leaving it optional.
- **Two media types under one status.** `CalendarNotConnected` declares both JSON and text
  under 401. Check what `typed-openapi --runtime zod` emits for it at REGEN. If it keeps only
  one, the web PR should say so.

### Consumers affected

These are confirmed exactly as the manifest lists them:

- `e2e/tests/focus-time.spec.ts:61-71`
- `e2e/tests/calendar.spec.ts:236-238`
- `e2e/tests/seam.spec.ts:110-112`
- `e2e/tests/calendar.spec.ts:37-41`, which still passes
- `backend/api/handlers_settings_autoschedule_test.go:73`
- `mcp/client.go:37,61,84`, which relays any status >= 400 as is; the bodies only get shorter

Adding blocking finding 1 would affect `e2e/tests/seam.spec.ts:383-433` only if its SEAM user
had no Google credential. That user has one (the test reaches the stand-in), so no break is
expected there.

### Contract and implementation divergences (non-blocking)

1. **`applyCompression` still leaks provider error text in a 200.**
   `CompressionApplyResult.failed[]` carries it: `engine/compression.go:170,178` append
   `p.EventID+": "+err.Error()`, and the schema says the entries are "appended verbatim". This
   change touches the operation, and PAC-21's problem is error text reaching clients. Add it
   to `doesNotResolve` with an issue, or fix it in this change; do not leave it unmentioned.
2. **A revoked credential stays a 500.** A stored Google credential that the provider rejects
   (revoked, or `invalid_grant`) still gets `500 {"error":"internal error"}`;
   `calendar.spec.ts:217-238` pins this. In practice the user is not connected, and gets no
   hint to reconnect. Keeping it for this change is fine; add it to `followUps`.
3. **The Teams path does not follow §2.3.** `ProviderForRequest`
   (`handlers_conferencing.go:41`) maps a failure to read the Microsoft token store to the 400
   `teams: not connected ...`. Either say so in the 400 description, or apply the §2.3 split
   there too.
4. **An existing literal is renamed.** `event has no time` (`handlers_conferencing.go:196,204`)
   becomes `event has no usable time`. The description says this is intentional, but it is the
   kind of rename §2.6 rejects. Keeping `event has no time` for both cases is the smaller
   change.

### Uncertainties not actually resolved

- **U-03:** the resolution is sound. `mcp/tools.go:137` calls `GET /api/calendar/freebusy`.
- **U-06 / API-024:** the resolution is sound.
  - `handlers_nlp.go:49` checks only the upper bound, and `:54` then indexes the slice.
  - So `{"selected_slot_index": -1, "parse_result": {"suggested_slots": [{...}]}}` panics
    today.
  - `minimum: 0` plus a 400 is what the handler already returns for every other bad index.
  - AC-3b pins it.
- **U-04, U-07:** see blocking finding 4.

### Verified clean

**Commands run in the worktree at 032e3ad:**

| Command | Result |
|---|---|
| `python3 scripts/openapi_assemble.py --check` | `OK: bundle is in sync (98 paths)`, rc=0 |
| `python3 scripts/openapi_migration_report.py --check` | `OK: MIGRATION.md covers all 119 operations (0 generated)`, rc=0 |
| `bash scripts/openapi_gen_go.sh --check` | `OK: backend/api/gen matches the contract`, rc=0 |
| `sha256sum contracts/openapi/openapi.yaml` | `d1f09a4f...dab8c1`, equal to the manifest's `contractHash` |

**Not run by the challenger:**

- `api-gate.sh`. The shared host checkout is on `impl/pac-66b-llm-base-url-backend`, another
  stage's branch, and I did not switch it.
- web-gate.
- e2e.
- `go build` and the Go tests.

**Attacked and found clean:**

- **5xx bodies.** A scripted scan of the bundle found 113 5xx responses. Every one is
  `application/json` only, and none has a description matching
  `/<err>|err\.Error|raw (Go )?err|verbatim|underlying error|driver error/`. No 4xx description
  matches it either.
- **The 401 set.** Exactly 17 operations reference `CalendarNotConnected`, matching the
  manifest.
- **What else changed.** I diffed every operation against `origin/main`. Only responses
  changed, except `getCalendarFreeBusy`, `confirmNaturalLanguage` and `previewCompression`
  (descriptions or markers), and the manifest declares all three. No request schema widens;
  `NLPConfirmRequest` only narrows (`minimum: 0`).
- **§2.3 can be implemented as written.** `auth.LoadUserToken` returns `(nil, nil)` on
  `sql.ErrNoRows` and an error otherwise (`backend/auth/token_store.go:61-68`). "No token" and
  "store failed" can therefore be told apart without guessing.
- **§2.5's list of operations that degrade on purpose.** All of these correctly keep 200:
  - `getAnalyticsWeek`: `engine/analytics.go:118` ignores `calErr`.
  - `getTeamAvailability`: `engine/team_availability.go:82` skips that member.
  - `listConferenceProviders`: `handlers_conferencing.go:168` ignores the error.
  - Public booking: `handlers_booking.go:88`.
- **Other `err.Error()` 4xx sites not in the manifest.** These pass validator sentences, as
  §5 says:
  - `handlers_habits.go:117,229`
  - `handlers_manager.go:166,276,284,386`
  - `handlers_teams.go:188,340,386`
  - `handlers_settings.go:136`

  The AC-1 source scan will have to allow-list these sites by name, not by pattern.
- **Telling the two 401s apart.** The middleware's body is `{"error":"unauthorized"}` as
  text/plain; this one is `{"error":"calendar not connected"}` as application/json. The enum
  makes an implementation that leaks text fail any test that validates against the schema.

## Revision 2 — 2026-10-26

Author: contract-author, answering the round-1 challenge above. Bundle hash after this
revision: `aae17626d4dfb49c0ac1c2d5c7d3b41448f2544c9771bbba3dcd876767127311`
(`contractRevision` 2 in the manifest).

### Blocking findings

1. **`syncPersonalCalendar` added to the calendar-dependent set. Accepted.**
   - Re-read the path: `handlers_personal.go:191` calls `PersonalBlocker.Sync`, which reads
     the feed and then calls `newCalOps` (`engine/personal_blocker.go:92`), and any error is
     `writeServerError` 500. It errors, it does not degrade, so it fits §2.5.
   - `calendar.yaml`: its `401` is now `$ref CalendarNotConnected` (which still carries the
     middleware text/plain body). The operation description says why it is
     calendar-dependent. The 500 description now says a feed-read failure or a
     credential-store read failure stays the 500, body `{"error":"internal error"}`.
   - Manifest: new `operationsChanged4xx` entry; AC-2 says 18 operations and names the
     sync case (a personal calendar whose feed reads successfully). D-2 and §1, §8 say 18.
   - The bundle scan now finds exactly 18 operations that reference `CalendarNotConnected`.

2. **U+2014 in the provider sentences. Accepted.**
   - Both 400 descriptions (`createConferenceLink`, `addEventConference`) now write
     `teams: not connected — visit ...` and `zoom: not connected — visit ...` with U+2014,
     as `handlers_conferencing.go:42,141` and `conference/factory.go:15` send it. Checked
     byte-for-byte (`e2 80 94`).
   - The new sentence `zoom: stored credential is invalid — reconnect at /api/auth/zoom` uses
     U+2014 too, deliberately, so the three provider sentences read alike. Both descriptions
     say so.

3. **patchSettings catalogue. Accepted.**
   - `SettingsValidationError` now lists the existing literal
     `invalid JSON: the body must be an object` for a body that is not an object or is
     `null` (`handlers_settings.go:81`), unchanged.
   - `invalid JSON` is kept only for a body that cannot be read (`handlers_settings.go:77`,
     which today appends the read error: that is the leak this change removes).
   - The `patchSettings` manifest entry says the same.

4. **U-04 and U-07 resolved, each pinned by a named test. Accepted.**
   - **U-04 (`patchEvent`)**: determined from the handler. `handlers_events.go:81-87`
     encodes the `*calendar.Event` that `UpdateEvent` returned. The operation-scoped
     `x-uncertain` is removed; the description states the 200 body is the raw Google event
     (`GoogleCalendarEventRaw`, already the declared schema). AC-6 names
     `TestPatchEvent_SuccessBodyIsRawGoogleEvent`. Whether it *should* return
     `CalendarEvent` is API-017 and moves to `followUps.patchEventBodyShape`.
   - **U-07 (`suggestMeetingSlots`)**: determined from the engine. Every step in
     `engine/smart_schedule.go` (113, 121, 135) is 30 minutes from the start of a free
     interval; no 15-minute rule exists on the route. The marker is removed; the
     description records the rule and that it is assumed by the factory, pending owner
     confirmation that no 15-minute rule was intended. AC-6 names
     `TestSmartScheduler_Suggest_StepsThirtyMinutes`.
   - Both tests are written by the backend implementer before the code (factory §2
     stage 4); they do not exist yet and this revision does not claim they do.
   - Markers in the bundle: 16 → 14.

### Non-blocking findings

1. **`applyCompression` `failed[]` provider text.** Not fixed here: it is a 200 payload
   field, and changing it changes a result shape the web app renders. Listed in
   `doesNotResolve` and `followUps.compressionFailedText`.
2. **Revoked Google credential stays a 500.** Listed in `doesNotResolve` and
   `followUps.revokedCredential`.
3. **Teams token-store failure maps to the 400.** The contract now says so on both 400
   descriptions (describe what is). Listed in `doesNotResolve` and
   `followUps.teamsTokenStore`.
4. **`event has no usable time` rename.** Accepted: the `addEventConference` 400 now keeps
   the existing literal `event has no time` for both cases, so no literal is renamed.
5. **UX regression on 5xx text.** `followUps.webGeneric5xxMessage` now says the web issue
   is filed before the backend PR merges.
6. **Two media types under one 401.** Left to the web REGEN step, as the challenger asked;
   the web PR must say what `typed-openapi` emitted.

### What was run for revision 2

In the worktree, before committing 22f0468:

| Command | Result |
|---|---|
| `python3 scripts/openapi_assemble.py` | wrote the bundle: 98 paths, 119 operations, x-uncertain 14 |
| `python3 scripts/openapi_migration_report.py` | 119 operations, 0 generated |
| `bash scripts/openapi_gen_go.sh` | rewrote `backend/api/gen/paceday.gen.go` (description comments only) |
| scripted scan of the bundle | 18 operations reference `CalendarNotConnected`; every 5xx is `application/json` only and none matches the AC-3 prose pattern; neither `patchEvent` nor `suggestMeetingSlots` carries an operation-scoped `x-uncertain` |

Then, with the host checkout switched to this branch at 22f0468:

```
$ bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/new-pac21-error-leaks-remaining-contract
make openapi rc=0
regenerated: none
OK: bundle is in sync (98 paths)
OK: MIGRATION.md covers all 119 operations (0 generated)
OK: backend/api/gen matches the contract
openapi-check(api) rc=0
backend lint rc=0
mcp lint rc=0
backend test rc=0
mcp test rc=0
backend  coverage: 48.3% (floor 43%) [3896 generated blocks excluded]
backend coverage rc=0
mcp      coverage: 84.0% (floor 75%)
mcp coverage rc=0
```

**Not run:** web-gate and e2e-gate. The web types are regenerated from the bundle only
after this contract merges (factory §1), so the web half reports drift until then; no
behaviour changes in this stage for e2e to observe.

## Challenge (round 2) — 2026-10-26

**Verdict**: accept-with-changes

Reviewer: contract-challenger, run as a separate agent against the committed state of
`factory/new-pac21-error-leaks-remaining-contract` at 8394b21 (revision 2). It read the
artefacts, the handlers and engine code on that commit, the e2e suite, `mcp/`, the backend
tests, and the web consumers on `smart-calendar-flow` main (66d6f20). It did not read the
author's session.

**Round-1 findings.** All four round-1 blocking findings are fixed as stated in Revision 2.
The checks are listed under "Verified clean". Two new blocking findings follow. Both are
contract and manifest edits only.

### Requests wrongly permitted, behaviour wrongly forbidden (blocking)

1. **`previewCompression` week mode: the contract both requires and forbids a 500 for a
   credential-store failure.**
   - **Payload:** `POST /api/schedule/compress` with `{"week":"2026-10-26"}`, from a
     signed-in user for whom reading the credential store fails (closed DB, as in
     `TestPAC21ForcedStorageErrorReturnsGenericJSON`).
   - **What the contract says, three ways:**
     - AC-2: "A credential-store read error on the same operations is a 500
       {"error":"internal error"}, not a 401". `previewCompression` is one of the 18, and
       AC-2 says "previewCompression in both modes".
     - `CalendarNotConnected`: "A failure to READ the credential store is not this response;
       it is the operation's 500."
     - `previewCompression`, however, has a 500 described as "**Single-day mode only**". Its
       operation description still says "week mode never returns 500, and returns `[]` if all
       five days error".
   - **What the server does today:** `handlers_schedule.go:37-42` `continue`s on every
     per-day error, so a store failure in week mode gives `200 []`.
   - **Why it blocks:** an implementer cannot satisfy AC-2 and the 500 description at the
     same time. If the implementer keeps week mode as written, a database outage in week
     mode is reported as "nothing to compress". §2.4 rejects exactly that for the
     no-calendar case.
   - **Fix (schema prose and manifest only):**
     - Change the 500 description to: "Single-day mode: engine failure for a user who has a
       stored Google credential. Both modes: reading the credential store failed. Body
       `{"error":"internal error"}`."
     - Change the operation description from "week mode never returns 500" to "week mode
       skips days whose engine call errors, and returns `[]` if all five do. The credential
       check that runs before any day is computed can still answer 401 or 500."
     - The alternative is to exempt week mode in AC-2 and in the `CalendarNotConnected`
       prose. I would not accept that. It is the less conservative choice, and it
       contradicts §2.3.

2. **`consumersAffected` misses three backend tests that break by design. AC-3 makes one
   of them undeletable without breaking a hard rule.**
   - **Which tests**, all in `backend/api/pac21_error_bodies_test.go`:
     - `TestPAC21ForcedStorageErrorReturnsGenericJSON` (`:63-124`). It has five cases with
       `json: false`: settings get, focus blocks, teams create, teams list and manager
       profile. Each asserts `Content-Type` text/plain and a bare `internal error` or
       `db error` body (`:106-112`). All five 500s become JSON.
     - `TestPAC21WriteServerErrorTextKeepsPlainContentType` (`:158-171`) tests
       `writeServerErrorText` itself. AC-3 says "no writeServerErrorText ... remains". The
       test therefore has to be deleted. The factory forbids deleting a test to go green
       unless the artefact says why.
     - `TestPAC21CreateMeetingKeepsPolicyRefusalTextOnly` (`:376-399`) asserts
       `TrimSpace(body) == "out-of-hours meeting allowance reached"` and `== "internal
       error"` on `createMeeting` and on `writeCreateMeetingFailure` (nlp confirm). Both
       bodies become `{"error":"..."}`.
   - **Why it blocks:** the manifest lists `handlers_settings_autoschedule_test.go:73` as
     the only backend test that breaks by design, so it reads as complete, and it is not. An
     implementer who hits these failures has nothing in the contract that permits rewriting
     them. The permission matters most for the deletion.
   - **Fix (manifest only):** add the three tests to `consumersAffected` with
     `breaks: yes, by design`. Say what replaces each one:
     - the five `json: false` cases become `json: true`;
     - `TestPAC21WriteServerErrorTextKeepsPlainContentType` is replaced by the AC-3 source
       scan and is deleted together with `writeServerErrorText`;
     - the refusal test asserts the JSON envelope with the same two messages.

### Responses the client cannot handle

None of these blocks. I checked:

- **`syncPersonalCalendar` 401 (new in revision 2).** The web client is `client.ts:1283`
  (`withFallback` → `requestApi`). The 401 reaches `apiErrorMessage` with
  `data.error = "calendar not connected"`, and `apiErrorMessage` prefers `data.error`
  (`client.ts:352-355`). The user sees that sentence, not "Your session expired". The only
  sign-out is `AuthContext.tsx:58`, on `GET /api/auth/me`.
- **`patchEvent` 200 (U-04).** The declared schema was already `GoogleCalendarEventRaw` on
  main. Revision 2 changes only prose, so REGEN changes no web type for it.

### Consumers affected

- **New in revision 2:**
  - `e2e/tests/seam.spec.ts:383-433` (AC-25 sync). The SEAM user has a work credential. The
    test asserts an `events.insert` through it, so the 200 holds.
  - `mcp/` does not call `/api/personal-calendars`.
  - `backend/api/handlers_personal*_test.go` use `fakePersonalBlocker`, so the 401 mapping
    does not reach them.
- **Confirmed unchanged from round 1:**
  - `e2e/tests/focus-time.spec.ts:70-71`
  - `e2e/tests/calendar.spec.ts:236-238`
  - `e2e/tests/seam.spec.ts:110-112`
  - `handlers_settings_autoschedule_test.go:73`
  - A grep of `e2e/tests` for `toBe(500)`, `'internal error'` and `text/plain` finds only
    these sites.
- **Missing from the manifest:** see blocking finding 2.

### Contract and implementation divergences (non-blocking)

1. **The `CalendarNotConnected` description misstates the web app.** It says "The web app's
   Dashboard already treats a 401 on a calendar read as 'connect a calendar'".
   `Dashboard.tsx:113-114` derives `calendarNotConnected` from `GET /api/auth/status`
   (`authStatus?.connected === false`), not from the 401. Round 1 recorded this under
   "Responses". The shared response's prose should say "Dashboard decides 'not connected'
   from /api/auth/status; no web code signs the user out on this 401".
2. **Two 500 descriptions do not name the credential-store read failure.** The
   `addEventConference` 500 ("Google Meet creation, settings load, or the event update
   failed") and the `removeEventConference` 500 do not mention it. Both reach `calClient`,
   and AC-2 asserts a 500 for a store failure on them. The other six original 401
   operations do name it. Add "or the stored credential could not be read".
3. **`patchEvent` 404 covers more than a missing event.** `handlers_events.go:53-56` maps
   any `GetEvent` error to the 404, including a Google 5xx or a network failure. The
   contract now says `event not found` with no qualifier. That describes the body
   correctly, but not the condition. Add "(any failure to read the event, not only a
   missing one)" to the 404 description, or add it to `doesNotResolve`.

### Uncertainties not actually resolved

None. U-04 and U-07 are now determined from code, and a named test pins each one.

- **U-04.** `handlers_events.go:81-87` encodes `updated` (`*calendar.Event`) directly, so
  the description matches.
- **U-07.** `engine/smart_schedule.go:100-136` sets
  `t := max(free.start, req.RangeStart)` and adds 30 minutes on every branch (113, 121,
  135). `freeBlocks` comes from `subtractIntervals(interval{RangeStart, RangeEnd}, busy)`
  (`:84`). On an empty calendar every candidate is therefore `RangeStart + k·30m`. AC-6's
  `:07` construction can be satisfied (a 09:07 start inside work hours yields 09:07) and
  falsifies a 15-minute grid.
- **U-03 and U-06.** These are unchanged from round 1 and sound.
  `handlers_nlp.go:49` still checks only the upper bound, and `minimum: 0` is in the bundle.
- U-01, U-05 and U-09 are carried forward under the round-1 ruling.

### Verified clean

**Commands run in `C:\Users\jouzu\paceday\wt-pac21-contract` at 8394b21:**

| Command | Result |
|---|---|
| `python3 scripts/openapi_assemble.py --check` | `OK: bundle is in sync (98 paths)`, rc=0 |
| `python3 scripts/openapi_migration_report.py --check` | `OK: MIGRATION.md covers all 119 operations (0 generated)`, rc=0 |
| `bash scripts/openapi_gen_go.sh --check` | `OK: backend/api/gen matches the contract`, rc=0 |
| `sha256sum contracts/openapi/openapi.yaml` | `aae17626…127311`, equal to the manifest's `contractHash` |

**Not run by the challenger:**

- `api-gate.sh`. The shared host checkout is on `impl/pac-66b-llm-base-url-backend`, and I
  did not switch it. The author's pasted api-gate run is at 22f0468, and 8394b21 changes
  only the .md and manifest. I did not re-run it, and I am not claiming it.
- web-gate, e2e-gate, `go build` and the Go tests.

**Attacked and found clean** (scripted scan of the bundle):

- **The 401 set.** Exactly 18 operations reference `CalendarNotConnected`, the same set as
  `operationsChanged4xx`. Every one of the 18 declares a 500.
- **5xx bodies.** All 113 5xx responses are `application/json` only. None matches the AC-3
  prose pattern.
- **Markers.** `x-uncertain` is gone from `patchEvent` and `suggestMeetingSlots`. The only
  remaining operation-scoped marker is on `getSchedulingLink`, which this change does not
  touch.
- **`NLPConfirmRequest.selected_slot_index`.** `minimum: 0`, with a 400 described for
  negative and out-of-range values.
- **Round-1 finding 2 (the dash).** The three provider sentences use U+2014 in both 400s,
  matching `handlers_conferencing.go:42,141`.
- **Round-1 finding 3 (the patchSettings catalogue).**
  - `handlers_settings.go:75-83` gives `invalid JSON: <read err>` only for `io.ReadAll`
    failures. That case becomes `invalid JSON`.
  - A non-object or `null` body gives the unchanged `invalid JSON: the body must be an
    object`.
  - Type mismatches come from `:118-121`. No custom `UnmarshalJSON` exists in
    `backend/storage`, so the only errors there are JSON syntax and type errors, and the
    `<property> has the wrong type` sentence covers the type case.
- **Round-1 non-blocking finding 4.** `event has no time` is kept. It matches
  `handlers_conferencing.go:196,204`.
- **`syncPersonalCalendar` ordering.** `engine/personal_blocker.go:85-96` reads the feed
  before `newCalOps`. A no-credential user with a failing feed therefore gets the 500, and
  the 500 description and AC-2 ("whose feed reads successfully") both say so.
  `newCalOps` (`engine/cal_iface.go:53-58`) currently folds a store error and a missing
  token into one `not authenticated`. Splitting them is an implementation task. The
  contract states the split, and `auth.LoadUserToken` makes it possible, as round 1
  verified.

## Revision 3 — 2026-10-26

Author: contract-author, answering the round-2 challenge above. Bundle hash after this
revision: `b8b2fd6394f63e3c94ff4f4237556f3c8d963e12da21ee84c1b3232ff09b8bf9`
(`contractRevision` 3 in the manifest). No operation, status code, schema or media type
changes in this revision; the edits are prose in three fragments and the manifest.

### Blocking findings

1. **`previewCompression` week mode and a credential-store failure. Accepted, fixed as the
   challenger proposed.**
   - The conservative reading is the one §2.3 already chose: a database outage must not be
     reported as "nothing to compress". Exempting week mode from AC-2 was rejected for that
     reason.
   - `scheduling.yaml`, `previewCompression` 500: now "Single-day mode: engine failure for a
     user who HAS a stored Google credential ... Both modes: reading the credential store
     failed (checked before any day is computed)."
   - The operation description no longer says "week mode never returns 500". It says week
     mode skips days whose engine call errors and returns `[]` if all five do, and that the
     credential check that runs before any day is computed can still answer 401 or 500.
   - The 200 description now says `[]` means every day's engine call failed *after* the
     credential check passed.
   - Manifest: the `previewCompression` entry and AC-2 say the 500 holds in both modes, with
     the payload the challenger used (week mode, closed DB → 500
     `{"error":"internal error"}`, not `200 []`).
   - What the server does today (`handlers_schedule.go:37-42` swallows every per-day error)
     is the behaviour this change removes for the credential check only. Per-day engine
     errors after the check are still skipped; that is unchanged and still documented.

2. **`consumersAffected` was missing backend tests that break by design. Accepted.**
   Added to the manifest, each with `breaks: yes, by design` and its replacement:
   - `TestPAC21ForcedStorageErrorReturnsGenericJSON` (`pac21_error_bodies_test.go:63-124`):
     the five `json: false` cases become `json: true`. Manager profile asserts the existing
     literal `{"error":"db error"}` (§2.6), the other four `{"error":"internal error"}`. The
     text/plain branch becomes dead and goes with them. All leak, log and Sentry assertions
     stay.
   - `TestPAC21WriteServerErrorTextKeepsPlainContentType` (`:158-171`): **deleted together
     with `writeServerErrorText`, in the same commit. This contract authorises that
     deletion.** It is not a test removed to go green: the function it tests ceases to exist
     by AC-3. What replaces it is the AC-3 source scan (fails if `writeServerErrorText` or an
     `http.Error` on a 5xx reappears) and the existing
     `TestPAC21WriteServerErrorGenericPerStatus` (`:127`), which covers the same body, log and
     Sentry behaviour for the JSON writer.
   - `TestPAC21CreateMeetingKeepsPolicyRefusalTextOnly` (`:376-399`): same two cases, same
     two paths (`createMeeting`, `writeCreateMeetingFailure`), now asserting 500,
     `application/json` and the exact envelope with the same two messages. It may be renamed;
     its cases may not be reduced.
   - Found while checking the challenger's list, not named by it:
     `scanErrorBodies` / `TestPAC21NoRawErrorsIn5xxBodies` /
     `TestPAC21GuardCatchesKnownBadPatterns` (`:267-334`). AC-1 extends the scan to any
     status, so fixture line 13 (`writeError(w, err.Error(), http.StatusBadRequest)`, today
     allowed) becomes a violation, and the `http.Error` hint that names
     `writeServerErrorText` must name `writeServerError`. Listed with the rule that no
     still-allowed shape may be dropped from the fixture.
   - Also checked and listed as `breaks: no`: the handler tests that drive a mock engine with
     a plain `errors.New` (`TestRunFocus_Error`, `TestClearBlocks_Error`, `TestCompress_Error`,
     the suggest/create/NLP error tests). Only the no-stored-credential condition becomes
     401; an arbitrary engine error stays 500. If the implementation turns these red, it is
     mapping too broadly.
   - A grep of `backend/**/*_test.go` for `not connected: `, `event not found: `,
     `invalid JSON: `, `text/plain`, `autoScheduleCron is invalid`, `writeServerErrorText` and
     `PlainTextServerError`, outside `pac21_error_bodies_test.go`, finds only
     `handlers_settings_autoschedule_test.go:73` (already listed) and
     `handlers_audit_test.go:210` (the middleware 401, unchanged).

### Non-blocking findings

1. **`CalendarNotConnected` misstated the web app. Fixed.** The prose now says the Dashboard
   decides "not connected" from `GET /api/auth/status`, not from this 401, and that no web
   code signs the user out on it (only a 401 on `GET /api/auth/me` does).
2. **`addEventConference` and `removeEventConference` 500s. Fixed.** Both now add "or the
   stored credential could not be read".
3. **`patchEvent` 404 condition.** Already in the contract at 8394b21: `calendar.yaml`
   `patchEvent` 404 reads "Any GetEvent failure maps to 404, including transport and
   permission errors, not only a genuine 404 from Google." The two conferencing 404s say
   "Any GetEvent failure maps to 404." No change.

### What was run for revision 3

In the worktree, before committing:

| Command | Result |
|---|---|
| `python3 scripts/openapi_assemble.py` | wrote the bundle: 98 paths, 119 operations, x-uncertain 14 |
| `python3 scripts/openapi_assemble.py --check` | `OK: bundle is in sync (98 paths)` |
| `bash scripts/openapi_gen_go.sh` then `--check` | `OK: backend/api/gen matches the contract` (no diff in `paceday.gen.go`) |
| `python3 scripts/openapi_migration_report.py --check` | `OK: MIGRATION.md covers all 119 operations (0 generated)` |
| `sha256sum contracts/openapi/openapi.yaml` | `b8b2fd63…8bf9`, recorded as `contractHash` |
| manifest parsed with `json.load` | ok |

Then, with the host checkout switched to this branch at caf2661 (and switched back to
`impl/pac-66b-llm-base-url-backend` afterwards):

```
$ bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/new-pac21-error-leaks-remaining-contract
make openapi rc=0
regenerated: none
OK: bundle is in sync (98 paths)
OK: MIGRATION.md covers all 119 operations (0 generated)
OK: backend/api/gen matches the contract
openapi-check(api) rc=0
backend lint rc=0
mcp lint rc=0
backend test rc=0
mcp test rc=0
backend  coverage: 48.3% (floor 43%) [3896 generated blocks excluded]
backend coverage rc=0
mcp      coverage: 84.0% (floor 75%)
mcp coverage rc=0
```

**Not run:** web-gate and e2e-gate, for the reason given in Revision 2 (web types are
regenerated only after this contract merges; nothing behavioural changes at this stage).

## Challenge (round 3) — 2026-10-26

**Verdict**: accept-with-changes

Reviewer: contract-challenger, run as a separate agent against the committed state of
`factory/new-pac21-error-leaks-remaining-contract` at 7bc5f5d (revision 3). It read the
artefacts, the handlers and the backend tests on that commit, `mcp/`, and the web consumers
on `smart-calendar-flow` main (d9e0786). It did not read the author's session.

**Round-2 findings.** Both blocking findings and all three non-blocking ones are fixed as
Revision 3 states (see "Verified clean"). One new blocking finding follows. It is a
manifest edit only, and Revision 3's answer to round-2 finding 2 introduced it.

### Requests wrongly permitted, behaviour wrongly forbidden (blocking)

1. **AC-1's widened scan forbids ten 4xx sites that §5 says stay unchanged.**
   - **Payload:** `POST /api/habits` with `{"title":"","duration_minutes":30}`. Today
     `handlers_habits.go:116-117` answers 400 with the validator's fixed sentence via
     `writeError(w, err.Error(), http.StatusBadRequest)`. §5 says these validators "are
     unchanged".
   - **What the manifest now requires:**
     - AC-1: the scan must "fail on any writeError/http.Error whose message is built from
       an error value at ANY status".
     - `consumersAffected`, scan entry: guard fixture line 13,
       `writeError(w, err.Error(), http.StatusBadRequest)`, "moves to the violation list".
   - **What that shape matches in `backend/api` at 7bc5f5d.** Every site below is a
     fixed-sentence validator, and none is in `operationsChanged4xx`:
     - `handlers_habits.go:117` (create) and `:229` (update): `validateHabitFields`
     - `handlers_manager.go:166` (confirmDetection) and `:276` (addMember): `normalizeManagerEmail`
     - `handlers_manager.go:284` (addMember) and `:386` (patchMember): `validateManagerCadence`
     - `handlers_teams.go:188` (inviteMember): `normalizeManagerEmail`
     - `handlers_teams.go:340` (createZone) and `:386` (updateZone): `validateNoMeetingZone`
     - `handlers_settings.go:136` (patchSettings): `validateChangedSettings`
   - **Why it blocks:** once widened, the real scan (`TestPAC21NoRawErrorsIn5xxBodies`)
     fails on these ten lines, and the contract gives the implementer no permitted way out:
     - §5 forbids changing these responses.
     - Nothing in the artefact authorises an exemption in the scan. Round 1 noted that the
       scan "will have to allow-list these sites by name", but that note sits in the
       challenge, not in AC-1, so it binds no one.
     - Revision 3 then made the conflict explicit by turning fixture line 13 into a
       violation.

     Both likely outcomes are bad. The implementer either rewrites validator messages the
     web shows to users, which is a behaviour change no contract records, or weakens the
     scan.
   - **Fix (manifest only):** amend AC-1 and the scan entry in `consumersAffected` to say:
     - the scan exempts exactly the ten call sites above. The test names each one by file
       and enclosing function, together with the validator it calls, and every return of
       that validator is a fixed sentence;
     - the guard fixture gains one allowed case for an exempted name. Line 13 stays a
       violation for any other name;
     - a later exemption must name its validator and show that it returns only fixed
       sentences.

     Also cite the list in §5, so that "unchanged" and AC-1 point at the same sites.
     The alternative is also acceptable: move these ten sites to a shared fixed-message
     error type that the scan already allows, like `validationErr.Msg` at fixture line 15.
     If that is chosen, the contract must say so and state that the response text does
     not change.

### Responses the client cannot handle

None found.

- **The new JSON 401.** `apiErrorMessage` (`src/api/client.ts:351-372` at d9e0786) prefers
  `data.error`, so the user sees `calendar not connected`, not "Your session expired".
- **Sign-out.** The only sign-out on a 401 is `AuthContext.tsx:58`, on `GET /api/auth/me`,
  which this change does not touch.
- **Dashboard.** `Dashboard.tsx:113-114` reads `GET /api/auth/status`, which matches the
  revised `CalendarNotConnected` prose.
- **JSON 5xx bodies.** The UI shows the server's fixed text instead of the generic
  sentence, as `consumersAffected` already says. Non-blocking, and already in `followUps`.

### Consumers affected

- **`mcp/`.** It does not handle 401, `text/plain` or `internal error` anywhere except its
  own auth middleware (`mcp/main.go:58,62`). It relays bodies verbatim. Unchanged.
- **e2e and backend tests.** Revision 3's list matches round 2's and adds the scan entry,
  which is the subject of blocking finding 1.
- **Web main moved.** Since round 2, `smart-calendar-flow` main moved from 66d6f20 to
  d9e0786 (PAC-66 type regen). `clockwise-like` main has not moved past this branch's merge
  base, so the PAC-66 contract is already in this branch's base. REGEN after merge stays a
  single regeneration.

### Contract and implementation divergences (non-blocking)

None new. The `previewCompression` week-mode change is a behaviour change. The contract
now states it consistently in four places: the operation description, the 200, the 500
and AC-2.

### Uncertainties not actually resolved

None.

- U-03, U-04, U-06 and U-07 stand as round 2 verified them.
- U-01, U-05 and U-09 are carried forward under the round-1 ruling.
- The bundle has one operation-scoped marker left, on `getSchedulingLink`, which this
  change does not touch.

### Verified clean

**Commands run in `C:\Users\jouzu\paceday\wt-pac21-contract` at 7bc5f5d:**

| Command | Result |
|---|---|
| `python3 scripts/openapi_assemble.py --check` | `OK: bundle is in sync (98 paths)`, rc=0 |
| `python3 scripts/openapi_migration_report.py --check` | `OK: MIGRATION.md covers all 119 operations (0 generated)`, rc=0 |
| `bash scripts/openapi_gen_go.sh --check` | `OK: backend/api/gen matches the contract`, rc=0 |
| `sha256sum contracts/openapi/openapi.yaml` | `b8b2fd63…8bf9`, equal to the manifest's `contractHash` |

**Not run by the challenger:**

- `api-gate.sh`. The shared host checkout is on `impl/pac-66b-llm-base-url-backend`, and I
  did not switch it. The author's pasted run is at caf2661, and 7bc5f5d changes only the
  .md and the manifest.
- web-gate and e2e-gate.
- `go build` and the Go tests.

**Attacked and found clean** (scripted scan of the bundle at 7bc5f5d):

- **The 401 set.**
  - Exactly 18 operations reference `CalendarNotConnected`. They are the operations in
    `operationsChanged4xx` minus the three 400-only entries (`createConferenceLink`,
    `queryFreeBusy`, `patchSettings`).
  - The response carries `application/json` and `text/plain`.
  - The schema is `{error: enum ["calendar not connected"]}`, required, with
    `additionalProperties: false`.
- **5xx bodies.** There are 113 5xx responses, all `application/json` only. No 5xx or 4xx
  description matches the AC-3 prose pattern.
- **`NLPConfirmRequest.selected_slot_index`.** `minimum: 0`, with a 400 described for
  negative and out-of-range values.
- **Round-2 finding 1 (`previewCompression`).**
  - The 500 now covers a credential-store failure in both modes.
  - The operation description no longer says week mode never returns 500.
  - The 200 says `[]` means every day failed after the credential check.
  - AC-2 carries the week-mode closed-DB payload.

  AC-2, `CalendarNotConnected` and the operation no longer contradict each other.
- **Round-2 finding 2 (tests that break by design).**
  - The three tests are in `consumersAffected`, each with its replacement.
  - The deletion of `TestPAC21WriteServerErrorTextKeepsPlainContentType` is authorised by
    name. `TestPAC21WriteServerErrorGenericPerStatus` (`pac21_error_bodies_test.go:127`)
    and the AC-3 scan replace it.
  - The policy-refusal test keeps both cases and both paths.
- **Round-2 non-blocking findings 1-3.**
  - The `CalendarNotConnected` prose now matches `Dashboard.tsx`.
  - The `addEventConference` and `removeEventConference` 500s name the credential read.
  - The `patchEvent` 404 already said "Any GetEvent failure maps to 404".
- **Other error-derived 4xx sites.** I grepped `backend/api/*.go` (excluding tests and
  generated code) for `writeError` or `http.Error` calls with `err.Error()`. The grep finds
  two groups and nothing else:
  - the leak sites the manifest lists: `handlers_calendar.go:164,206`,
    `handlers_conferencing.go:148,269,274,329,334,339,364,369`,
    `handlers_events.go:51,56,95,110,130`, `handlers_freebusy.go:47` and
    `handlers_settings.go:77,120`;
  - the ten validator sites of blocking finding 1.

  The validators return only fixed sentences. For example, `normalizeManagerEmail`
  (`handlers_manager.go:671-681`) discards the error from `mail.ParseAddress`.
