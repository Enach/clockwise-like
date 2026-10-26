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

