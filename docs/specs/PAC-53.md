# Spec — PAC-53: the activity-log endpoint silently accepts a `limit` it cannot read

- **Linear**: https://linear.app/paceday/issue/PAC-53/audithandler-discards-strconvatois-error-so-two-servers-in-this-repo
- **Status**: draft
- **Author**: spec-author
- **Track**: full process (contract change: a new error response on `listAuditEntries`)
- **Decision taken**: the ticket's option 1 ("validate in the hand-written handler, declare the 400 in the contract in the same change"), with the JSON error encoding. Option 2 (leave it) is rejected in §9.
- **Inherited scope**:
  - `docs/factory/api-audit.md`: the two findings on `GET /api/audit` are both closed — API-005 (`api-audit.md:275`, Fixed PAC-23) and API-074 (`api-audit.md:344`, Fixed PAC-23). No open finding.
  - `contracts/openapi/paths/calendar.yaml`: no `x-uncertain` on `listAuditEntries` (the operation spans `calendar.yaml:959-1334`; `grep -n x-uncertain` hits only `:1516` and `:2179`, both elsewhere and both already resolved).
  - `contracts/openapi/MIGRATION.md:82` — the operation's `notes` cell states the precondition this ticket discharges; it must be rewritten in the same change (§3.4).
  - PAC-23 rejected alternative J and challenge findings R3-F1/R3-F3 (`contracts/features/PAC-23.md`), which named this ticket as the owner of the decision.
- **Verified against**: `clockwise-like` `origin/main` at `f074b4d`; `smart-calendar-flow` `origin/main` at `36bdb0e`, read with `git show` / `git grep`, no checkout. **No code was run** for this spec: every behaviour claim below is from reading source, and the two "established by execution" claims are quoted from the ticket and the PAC-23 challenge, not re-run here.

## 1. Problem

The activity log page asks the server for "the last N entries". When the number
it sends is not a number — or is two numbers, or is too large to be a number the
server can hold — the server does not say so. It quietly substitutes a number of
its own choosing and answers success. The caller cannot tell "you got 50 because
you asked for 50" from "you got 50 because I could not read what you asked for".

That costs three things:

1. **A client bug becomes invisible.** A caller that builds the query wrongly
   (a `NaN`, a stringified object, a doubled parameter from a merge of two query
   builders) gets a plausible-looking page instead of an error, and nobody finds
   out.
2. **The repository holds two servers for this operation that disagree.** The
   hand-written handler that is actually mounted answers 200; the generated server
   built from the same contract answers 400 for the same request. Whoever moves this
   operation onto the generated server changes its observable behaviour without
   touching handler logic, and the contract today has to carry a long paragraph
   warning them (`calendar.yaml:1218-1270`).
3. **The contract has to describe a five-case resolution table** (`calendar.yaml:1157-1216`)
   whose subtlety has already cost three contract revisions (PAC-23 R1–R3), one of
   which missed a whole input class (`?limit=%zz&limit=10`). A rule of the form
   "a number from 1 to 500, or nothing" needs no such table.

Who is affected: today, nobody visibly — the shipped web client only ever sends
`50` or `10` (§2.4). This is a correctness and contract-honesty fix, and its
user-visible risk is exactly "a caller that sends junk today and will get a 400
tomorrow", which §2.4–§2.5 show is no shipped caller.

## 2. Current behaviour

### 2.1 The handler

`backend/api/handlers_audit.go:13-32` (`auditHandler`):

- `:15-20` — no user in context → `401` `{"error":"unauthorized"}`. (Behind
  `requireAuth`, `backend/api/routes.go:216` mounts it inside the authenticated
  group, so in practice the middleware's text/plain 401 —
  `calendar.yaml:1321-1326`, `MiddlewareUnauthorized` — answers first.)
- `:21-23` — `limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))`, with a
  comment stating the discard is deliberate.
- `:24` — `storage.ListAuditLog(db, userID, limit)`.

`backend/storage/audit_log.go:12-13` define `AuditDefaultLimit = 50`,
`AuditMaxLimit = 500`; `:27-32` map `limit <= 0` to 50 and `limit > 500` to 500.

### 2.2 What each input resolves to today

From the code above plus Go's documented `net/url` and `strconv` behaviour (not
re-executed for this spec; the contract text at `calendar.yaml:1168-1216` states the
same and PAC-23's revision-3 challenge executed the last two rows):

| Request | Text read | Today |
|---|---|---|
| no `limit` | `""` | 200, 50 entries max |
| `?limit=` / `?limit` | `""` | 200, 50 |
| `?limit=abc`, `?limit=50.5`, `?limit=1e2` | as sent | 200, 50 (Atoi error → 0) |
| `?limit=+50` | `" 50"` (`+` decodes to space) | 200, 50 |
| `?limit=%2B50` | `"+50"` | 200, **50** — Atoi accepts a leading sign, so this is the value 50, not the default |
| `?limit=%2050`, `?limit=50%20` | leading/trailing space | 200, 50 (Atoi error) |
| `?limit=0`, `?limit=-5` | integer ≤ 0 | 200, 50 |
| `?limit=1` … `?limit=500` | in range | 200, that many max |
| `?limit=501`, `?limit=10000` | integer > 500 | 200, 500 |
| `?limit=99999999999999999999` | overflows int64 | 200, 500 (Atoi saturates to MaxInt64 with `ErrRange`) |
| `?limit=10&limit=500` | first value, `10` | 200, 10 |
| `?limit=%zz` | pair dropped by `url.ParseQuery` → `""` | 200, 50 |
| `?limit=%zz&limit=10` | `%zz` pair dropped → `10` | 200, **10** |
| `?limit=10;x=1` | pair dropped by `url.ParseQuery` (semicolon separator is an error since Go 1.17) → `""` | 200, 50 |

### 2.3 The generated server (not mounted)

`backend/api/gen/paceday.gen.go`, `ServerInterfaceWrapper.ListAuditEntries`:
binds `limit` with `runtime.BindQueryParameter("form", true, false, "limit", r.URL.Query(), &params.Limit)`
(`oapi-codegen/runtime v1.1.1`, `backend/go.mod:15`) and on error calls the
default `ErrorHandlerFunc` → `http.Error(..., 400)`, **text/plain**. Per the
contract's own analysis (`calendar.yaml:1219-1254`, executed during PAC-23 R3):
empty/bare, non-integer, int64-overflowing and multiply-supplied decodable values
→ 400; `0`, negatives and `> 500` reach the handler (the binder does not enforce
`minimum`/`maximum`); undecodable pairs are dropped before the binder sees them,
so `?limit=%zz&limit=10` resolves to 10 on both servers. Nothing imports
`backend/api/gen` from mounted code (`calendar.yaml:1144-1148` gives the
falsifying grep).

### 2.4 The web caller (`smart-calendar-flow` @ `36bdb0e`)

- `src/api/client.ts:656` `DEFAULT_AUDIT_LIMIT = 50`; `:1171-1174`
  `getAudit(limit = DEFAULT_AUDIT_LIMIT)` → `requestApi("GET", "/audit", undefined, { limit: String(limit) })`.
- `requestApi` (`client.ts:600-614`) builds the URL with `URL.searchParams.set`,
  which sets **one** value, percent-encodes it, and skips `undefined`/`""`. So the
  web can never send an empty, repeated or undecodable `limit`.
- Call sites: `src/pages/Audit.tsx:21` `useAudit(DEFAULT_AUDIT_LIMIT)` (50) and
  `src/components/QuickActions.tsx:27` `useAudit(10, showAudit)` (10), through
  `src/hooks/useAudit.ts:16-19`. Both are integer literals in range.
- Demo mode (`src/demo/clientMocks.ts:644`) never reaches the server.
- The only way the web could send a newly-rejected value is `getAudit(x)` with a
  non-integer `x` (`NaN`, `1.5`, `Infinity`); no call site does. **No shipped web
  call newly 400s.**
- The web already handles a JSON error from this endpoint: `src/api/audit.test.ts:64-69`
  feeds a `{ error: "audit unavailable" }` body with status 401, 403 and 500 and
  expects an `ApiHttpError` whose `apiErrorMessage` is that text. 400 is not in
  that list; from reading `requestApi` it takes the same non-2xx path, but no test
  pins it (the frontend may add 400 to that `it.each`; not a gate here).

### 2.5 MCP and other callers

- `mcp/` has no tool that calls `/api/audit` (`grep -rni audit mcp` returns
  nothing). **No MCP caller.**
- In this repo: `e2e/tests/audit.spec.ts:58-59` sends `limit` ∈ {absent, 1, 500,
  10000} and expects 200; `:116-125` expects `?limit=10000` → 500 entries
  (PAC-23 AC-11). `backend/api/handlers_audit_test.go:106`, `:179`, `:194` pin
  200 for `?limit=-1`, `abc`, `10&limit=500`, empty, `0`, `-5`, `+50`, `501`,
  `10000`, `99999999999999999999`. All of those are the PAC-23 behaviour this
  ticket changes; see §6.

### 2.6 How sibling handlers parse integer query parameters

`grep -rn "Atoi\|ParseInt\|ParseUint\|ParseFloat\|ParseBool" backend/api` (non-test, non-gen):

| Site | Parameter | Behaviour on bad input |
|---|---|---|
| `handlers_audit.go:23` | `limit` | **error discarded** — this ticket |
| `handlers_analytics.go:55-59` | `GET /api/analytics/trends?weeks` | error **swallowed**: anything not an integer in [1,52] silently becomes 8; the contract documents it (`habits.yaml:381-391`, "There is no 400") |
| `handlers_teams.go:623-630` | `GET /api/teams/{id}/availability?duration` | absent or empty → default 60; non-integer, ≤0 or >1440 → **400 JSON** `writeError` |
| `handlers_booking.go:248-254` | `GET /api/book/{slug}/slots?duration` | absent or empty → default; non-integer or not an allowed option → **400 JSON** |
| `handlers_personal.go:102,130,159,183`, `handlers_org_ai.go:245` | path id / body | errors checked |

The other `x, _ := ...Parse...` sites in `backend/api` (`handlers_habits.go:58-59`,
`handlers_settings.go:457-458`) parse values already validated a line earlier and
are not this defect. So the only other member of this class in `backend/api` is
`analytics/trends?weeks` — follow-up F-1, out of scope (§5).

`writeError` (`backend/api/handlers_settings.go:479-483`) writes
`Content-Type: application/json`, the status, and `{"error": msg}`; the contract
models that body as `ErrorResponse` (used by this operation's 500 at
`calendar.yaml:1327-1334`).

## 3. Desired behaviour

### 3.1 The rule

A request for the activity log either says nothing about how many entries it
wants, or says it exactly once, as a whole number from 1 to the published maximum.
The first gets the published default; the second gets at most that many. **Every
other request is refused as a bad request and returns no entries** — the server
never substitutes a number the caller did not ask for.

### 3.2 Every case

"The count" is the activity-log quantity parameter. "Refused" means: status 400,
the API's standard JSON error body (the same shape the 500 on this operation and
the sibling validation refusals in §2.6 already use), with a message that names the
parameter and the accepted range, and no entries.

| # | Input | Outcome |
|---|---|---|
| C-1 | count absent | default (50) |
| C-2 | count present with an empty value, or bare (no `=`) | **refused** |
| C-3 | not a base-10 integer: letters, decimals, exponents, `0x…`, underscores, internal or surrounding whitespace (including a raw `+`, which decodes to a space) | **refused** |
| C-4 | an optional single leading `+` or `-` sign followed by ASCII digits, leading zeros allowed (`%2B50`, `050`) | parsed as that integer, then C-5…C-7 |
| C-5 | integer < 1 (`0`, `-0`, `-5`) | **refused** |
| C-6 | integer from 1 to 500 inclusive | at most that many entries |
| C-7 | integer > 500, whether or not it fits in 64 bits (`501`, `10000`, `99999999999999999999`) | **refused** (see OQ-1 for the alternative) |
| C-8 | the count supplied more than once, even with identical values (`limit=10&limit=10`) | **refused** |
| C-9 | the query string contains any pair that cannot be percent-decoded, or uses `;` as a separator — whatever that pair's name — (`%zz`, `limit=%zz&limit=10`, `x=%zz&limit=10`, `limit=10;x=1`) | **refused** |
| C-10 | other parameters, including a differently-cased `LIMIT` | ignored, as today |

Precedence: an unauthenticated request is refused as unauthenticated (unchanged)
before the count is examined. C-9 is examined before C-1…C-8, so
`?limit=%zz` is refused, not treated as absent.

Why C-9 refuses the whole query rather than just ignoring the bad pair: the server
cannot tell whether an undecodable pair was the count (`%6Cimit` is `limit`; a
broken encoder produces `limit=%zz`), so ignoring it is exactly the silent
substitution this ticket removes. It is scoped to this operation; other endpoints
keep today's behaviour (§5).

### 3.3 Agreement with the generated server

After this change, for every row of §3.2, the hand-written server and a correctly
configured generated server give the same status. The generated binder already
refuses C-2, C-3, C-7-overflow and C-8; the handler logic (which the generated
server would also run) refuses C-5, C-7-in-range and C-9. Two residual differences
are recorded in the contract, not left implicit:

- the generated wrapper's **default** error handler writes text/plain, not the JSON
  body; adopting the wrapper must install a JSON error handler (or the contract
  change at that time must say otherwise);
- C-9 is not visible to the binder (undecodable pairs are dropped before it), so a
  generated-server implementation must make that check itself.

### 3.4 Contract

The contract change declares the 400 with the JSON error body on this operation,
replaces the resolution procedure and the "ON MIGRATION TO THE GENERATED SERVER"
paragraph with the rule in §3.2 and the two residual differences in §3.3, and
rewrites the operation's `notes` cell in the migration register so that moving the
row is no longer described as a contract change for this reason. `minimum`,
`maximum` and `default` keep their values.

## 4. Acceptance criteria

Each is falsifiable today: every "refused" criterion currently returns 200.

AC-1. Given an authenticated user with 120 entries, when they request the log with no count, then they receive 200 and exactly 50 entries.  [unit]

AC-2. Given that user, when they request with a count of 1, 10 and 500 (in turn), then they receive 200 and at most 1, 10, and 120 entries respectively; and a count written `%2B10` or `010` returns 10 entries.  [unit]

AC-3. Given an authenticated user, when they request with each of: an empty count, a bare count, `abc`, `50.5`, `1e2`, `0x10`, `1_0`, a raw `+50`, `%2050`, `50%20`, then each response is 400 with a JSON body matching the API's error shape whose message names the parameter, and the body contains no entries.  [unit]

AC-4. Given an authenticated user, when they request with a count of `0`, `-0`, `-5`, `501`, `10000`, `9223372036854775807` and `99999999999999999999`, then each response is 400 with the JSON error body.  [unit]

AC-5. Given an authenticated user, when the count is supplied twice (`10&limit=500`, and `10&limit=10`), then the response is 400 with the JSON error body.  [unit]

AC-6. Given an authenticated user, when the query string is `limit=%zz`, `limit=%zz&limit=10`, `x=%zz&limit=10`, or `limit=10;x=1`, then the response is 400 with the JSON error body — in particular `limit=%zz&limit=10` no longer returns 10 entries.  [unit]

AC-7. Given an unauthenticated request carrying an invalid count (`abc`), when it is sent through the real router, then the response is the existing unauthenticated refusal, not 400.  [unit]

AC-8. Given a 400 from this operation, then its status, content type and body validate against the response the contract declares for 400 on this operation; and every request in AC-1…AC-6 gets a status the contract declares.  [contract]

AC-9. Given the full stack and the shipped web client, when a user opens the activity page and the quick-actions audit preview, then the requests carry `limit=50` and `limit=10` and both return 200 with entries rendered (no regression for the only real caller).  [e2e]

AC-10. Given the full stack, when an authenticated API caller requests `?limit=10000` and `?limit=abc`, then each returns 400 with a JSON body (replaces PAC-23's e2e expectation that 10000 returns 500 entries).  [e2e]

AC-11. Given the generated server wrapper for this operation (built from the new bundle, not mounted), when it is driven with one request from each of C-2, C-3, C-7-overflow and C-8, then it returns 400 — i.e. the binder still agrees with the declared contract on the cases it can see. (Pins §3.3 so a codegen bump that relaxes the binder is noticed.)  [unit]

## 5. Explicitly out of scope

- **`GET /api/analytics/trends?weeks`** (follow-up F-1). Same class — bad input
  silently becomes 8 — but a different operation with its own contract text
  (`habits.yaml:381-391`) and web caller; changing it is its own contract change.
- **Empty-value handling in `teams/{id}/availability?duration` and
  `book/{slug}/slots?duration`** (follow-up F-2). Both treat `?duration=` as
  absent, while this spec refuses an empty count (C-2) to agree with the generated
  binder. Both already refuse junk with 400 JSON, so they are not this defect;
  whether to align their empty case is a separate, tiny contract change.
- **Malformed query strings on any other endpoint** (C-9 is this operation only).
  Making it global would be a middleware change touching ~119 operations.
- **Migrating `listAuditEntries` to the generated server.** This makes the
  migration behaviour-neutral (bar §3.3); it does not perform it.
- **The storage-layer clamp.** Whether the storage function keeps its own
  defensive defaulting is the plan's business; it must not become reachable with a
  value the handler would have refused.
- **Web changes.** None required (§2.4). Hardening `getAudit` against a
  non-integer argument is optional and not a gate for this ticket.

## 6. What must not change

- Absent count → 50, and 1–500 → that many, newest first, scoped to the caller
  (PAC-23 AC-1…AC-10). Protected by `TestListAuditEntries_ReturnsOnlyCallersEntries`,
  `_DefaultLimitMatchesContract` (the `""` and `?limit=10` cases),
  `_ForeignTitlesAndPromptsNeverAppear` (valid cases), `_ResultDependsOnCaller`,
  `_PreMigrationRowsReturnedToNobody`, `_EmptyLogIsEmptyArrayNotError`
  (`backend/api/handlers_audit_test.go`), and e2e `audit.spec.ts` AC-1/AC-2/AC-12.
- The 200 body is still always an array, never `null`.
- Unauthenticated → existing refusal (`_UnauthenticatedIsRefused`,
  `_NoUserInContextIsRefused`, e2e AC-12).
- 500 body still `{"error":"internal error"}` (`pac21_error_bodies_test.go:76`).

**Tests that pin the behaviour being removed and must be rewritten, not deleted**:
`handlers_audit_test.go:106` (invalid queries in the cross-user loop must expect
400 and still assert no foreign text), `:179` (`""` stays 50; the other five move
to 400 assertions), `:191-200` `_OverMaximumLimitCaps` (becomes a 400 test, under
OQ-1's default), and `e2e/tests/audit.spec.ts:58` (drop 10000 from the 200 loop)
and `:116-125` (AC-10 above). **This supersedes PAC-23 AC-11** ("asks for more than
the maximum → receives at most the maximum") under OQ-1's default; the PAC-23 spec
should get a one-line pointer to PAC-53.

## 7. Rollback

No migration, no data. Reverting the backend change restores today's silent
resolution; the reverted contract goes back to the PAC-23 text. Order: backend and
contract merge together (one PR in `clockwise-like`); the web needs no change, so
there is no cross-repo ordering and no old-web compatibility window — every request
the shipped web sends is a C-6 request, valid before and after. The web's
regenerated types gain a 400 response variant; that is additive and does not need
to ship before or after the backend.

## 8. Open questions

**OQ-1 — integers above the maximum: refuse (spec default) or keep capping to 500?**
Owner: product owner (Nicolas). PAC-23 AC-11 made "above the maximum → the maximum"
an accepted requirement and e2e pins it; this spec overturns it.
*Recommendation: refuse.* (a) Capping keeps a seam the binder forces anyway:
`9223372036854775807` would cap to 500 while `9223372036854775808` must 400 — the
kind of hidden case boundary that cost PAC-23 three revisions. (b) The only sibling
with a declared maximum on an integer query parameter, `teams/{id}/availability`,
refuses above it. (c) No shipped caller sends > 500. If the owner prefers capping,
C-7 becomes "1–int64-max → at most 500; overflow → refused", AC-4 drops `501`,
`10000`, `9223372036854775807`, and §6's PAC-23 tests stay as they are.

**OQ-2 — refuse the whole request on any undecodable pair (C-9), or only ignore it?**
Owner: contract-author / challenger. *Recommendation: refuse* (§3.2 rationale).
The cost is a check the generated binder cannot do (§3.3). Resolve at stage 2.

**OQ-3 — empty count (`?limit=`): refuse (C-2) or treat as absent?**
Owner: contract-author. Siblings (§2.6) treat empty as absent; the generated
binder refuses it. *Recommendation: refuse*, because this ticket's purpose is
agreement with the binder and no caller sends it (`requestApi` drops empty values);
align the siblings in F-2 rather than diverging here.

**OQ-4 — exact error message text.** Owner: contract-author. Recommendation: one
fixed message for every refusal, e.g. "limit must be an integer from 1 to 500",
without echoing the input (the input is attacker-controlled text and the cases are
not worth distinguishing to a caller).

## 9. Decisions

- **D-1 Option 1 over option 2.** Option 2 keeps a contract that describes silent
  substitution as correct and leaves the generated-server migration as a trap.
  Option 1's only cost is a 400 for callers that send junk, and §2.4–§2.5 found none.
- **D-2 JSON, not text/plain.** Every other refusal this handler and its siblings
  produce is the JSON error body; the web's error path already reads it
  (`audit.test.ts:64-69`, for 401/403/500). The generated wrapper's text/plain default is an
  artefact of codegen defaults, not a product decision.
- **D-3 Leading `+`/`-` sign and leading zeros accepted (C-4)** because the
  generated binder accepts them and rejecting them would open a new divergence for
  no user benefit; a negative then fails C-5.
- **D-4 Repeated count refused even when the values agree (C-8)**, matching the
  binder; "which one wins" is not a question a caller should be able to ask.

### Follow-ups to file (not in scope)

- **F-1** `GET /api/analytics/trends?weeks`: bad input silently becomes 8
  (`handlers_analytics.go:55-59`, contract `habits.yaml:381-391`). Same decision
  as this ticket, separate contract change.
- **F-2** `?duration=` (empty) treated as absent in
  `handlers_teams.go:623-624` and `handlers_booking.go:248`, while the generated
  binder refuses it. Align or document.
