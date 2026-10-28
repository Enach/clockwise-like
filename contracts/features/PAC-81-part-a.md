# Contract: PAC-81 Part A (backend process hardening)

- **Linear**: PAC-81, https://linear.app/paceday/issue/PAC-81
- **Spec**: `docs/specs/NEW-deploy-hardening.md`, revision 3, challenge
  revision 3 verdict **accept** (branch `factory/deploy-hardening-spec` at
  `beeedc7`, merged into this branch).
- **Stage**: contract-author, revision 2, 2026-10-28 (answers "Challenge,
  revision 1", verdict reject; see "Revision 2" at the end). Next:
  contract-challenger.
- **Manifest**: `contracts/features/PAC-81-part-a.yaml`.
- **Scope**: the five Part A stage-2 items the spec lists in "Revision 3"
  (readiness AC-A11, PAC-14 §2 Sentry default AC-A14, shared budget 503 AC-A2,
  `createPublicBooking` 201 description AC-A19, "treated as free" AC-A18),
  plus two things AC-A2 and AC-A12 imply at the HTTP boundary: the redirect
  rule for sign-in callbacks (spec challenge rev 0 N5) and the backend's JSON
  413. Parts B, C and D have no contract.

Verification: the assembler, migration register and Go generator checks were
run (rc 0, see the manifest's `gates`), and `go build ./...` and `go vet ./...`
pass. No backend behaviour was implemented or observed: every statement about
the server after PAC-81 is what stage 4 must build.

## 1. What changed

| Operation | Change |
|---|---|
| `getReadiness` `GET /api/ready` | **New**, in a new fragment `paths/ops.yaml`. 200 `{"status":"ready"}` / 503 `{"error":"not ready"}` (rev 2), single-flight database ping |
| `createPublicBooking` | 201 description (AC-A19); 503 only before the booking is saved |
| `getPublicBookingSlots` | per-host read rule, own-limit timeout treated as free; 503 |
| `getTeamAvailability` | per-member read rule, own-limit timeout treated as free; 503 |
| `syncPersonalCalendar` | 503 on budget stop (AC-A17); ICS limits named in the 500 |
| `parseNaturalLanguage` | 503; an LLM call's own 30 s timeout is a provider failure (500) |
| API `info.description` | budget rule for every operation, callback redirect exceptions, 413 |
| `PAC-14.md` §2 | amendment: no backend default DSN (AC-A14) |

`getHealth` and `auth.yaml` are untouched. The bundle diff has no hunk in
`/api/health` or `HealthResponse`.

## 2. Why the interface is shaped this way

### Readiness is a separate operation, and liveness does not move

`/api/health` has two consumers that must keep working
(`e2e/global-setup.ts:61-70`; the web's `checkApiAvailability`,
`src/lib/api.ts:28-39`, which switches the web into preview mode on a
non-OK answer). If `/api/health` became readiness, a database outage or the
5 s unready notice of every deploy would put signed-in users into preview
data. So liveness keeps its exact bytes and readiness is a new path.

The path is `/api/ready` so that it sits under nginx's existing `/api/`
proxy rule: the operator's proxy can probe it through nginx (AC-D1) with no
new nginx location. The container check does not use HTTP from outside; it
runs the binary's health mode, which calls readiness on the local port
(AC-A16).

It lives in a new fragment, `ops.yaml`, not next to `getHealth` in
`auth.yaml`. Nothing about readiness is authentication, and PAC-71 (#242)
rewrites `auth.yaml` heavily; a new file cannot conflict with it.

The body is a fixed `status` with no cause. The endpoint is public. Saying
"database unreachable" to anyone who asks tells an attacker when a
database-exhausting attack has worked, and no consumer branches on the
cause: a health check needs only the status code. The cause goes to the log.

*Rev 2.* The 503 body is the PAC-21 error envelope `{"error":"not ready"}`,
because every 5xx in the contract must be that envelope (PAC-21 AC-3, checked
by `TestPAC21Every5xxInTheContractIsTheJSONEnvelope`). A probe reads only the
status code, so nothing depends on the 503's body. Readiness is public and
outside every limit, so a flood of probes must not be able to drain the
database pool: concurrent calls share one ping (single flight), the 2 s
includes waiting for a pooled connection, and readiness never holds more than
one connection. `docs/security.md` (B8) tells operators they may restrict
`/api/ready` at their proxy.

### One shared 503 with one fixed message

AC-A2 asks for "a complete JSON error response with status 503". The
contract pins the message `request timed out` as an enum, the same way
`BookingRateLimitError` pins `rate limit exceeded`, so a client can tell a
budget expiry apart from the other 503s that exist today (`Zoom OAuth not
configured`, `Microsoft OAuth not configured`). The schema keeps the
`{"error": ...}` envelope every JSON error uses, so the web's existing
error mapping handles it unchanged (`client.ts:577`: 5xx shows "The server
had a problem. Try again shortly.").

The response component is defined once, in `ops.yaml`, and referenced from
the other fragments. Cross-fragment references are already the norm
(`CalendarNotConnected` is referenced from four fragments and defined in
one). Where an operation has something specific to say about what was kept
(booking create, personal sync, parse), the operation uses its own
description with the shared schema instead of the shared response.

No `Retry-After`. The budget is per request, not a rate. A retry is as likely
to succeed immediately as later, and the header would invite clients to wait
for no reason.

### Booking create: 503 only when nothing was saved

The challenge (rev 3, non-blocking 1 and 2) asked for one sentence the
implementation can be held to: a booking request answers 503 only when no
booking was saved for it. *Rev 2* makes that testable at the one point where
the outcome changes, the sending of the insert. Before the insert is sent,
budget expiry answers 503 and nothing was saved. Once it is sent, the
handler waits for the insert's own result, which the budget does not bound.
It then answers 201 if the row was saved, or 500 if the insert failed. A lost
database connection leaves the outcome unknown, so the 500 says it does not
prove that nothing was saved. Only the 503 proves it.

The 201 description says what is actually guaranteed: the row is saved,
and host events and the email belong to a unit that may finish after the
response. The old text ("host calendar events written") was already false
(the operation's own description says event creation is best effort), so
this replaces a false sentence rather than weakening a true one. No status
was added, as the spec requires (F-12).

### "Treated as free" names the new timeout case

Team availability already documented that a member whose calendar call fails
is treated as free. Slot listing did not, although the code does it
(`backend/engine/booking.go:94-96`, and `hostBusy` ignores a client or list
error). Both now say it. Both also say what changes for users: with the
per-call 15 s limit, a slow calendar now looks free, where before the request
waited. They also draw the line AC-A18 draws. A call that fails on its own
limit counts as free. A call never started, or cut by the budget, is a 503,
never a 200 without that item.

### The budget rule is stated once for the whole API

Through the pool wait (AC-A9), any operation that uses the database can in
principle answer the budget 503. The spec names the five operations that
reach it in normal use, and those carry the 503 explicitly. For the rest,
one paragraph in `info.description` states the rule and its exceptions:
- the sign-in branches of the Google, Microsoft and SSO callbacks keep
  their failure redirect (`<FRONTEND_URL>/?error=auth_failed`);
- (rev 2) a Google or Microsoft callback carrying a PAC-71 calendar-connect
  state answers that branch's NOT CREDITED redirect
  (`<FRONTEND_URL>/?connect=not_credited`) with the connect state cookie
  expired. Budget expiry never answers `?error=signin_refused`, which would
  claim a refusal that did not happen;
- step-up answers its denied redirect: `state_mismatch` when the budget runs
  out before check 0 completes, otherwise the reason of the check that was
  running. Step 5 (issuing the grant), once started, is waited for like the
  booking insert, so the answer is granted or the existing 500;
- a booking whose insert was sent answers 201 or 500, never 503;
- the backend answers 413 `{"error":"request body too large"}` for bodies
  over 1 MiB. Part B's nginx 413 is bound to the same body by a merge
  condition. No Part B plan or branch exists on the remote yet.

That paragraph is generated from `scripts/openapi_assemble.py` (`INFO`),
which is where the bundle's info text is defined.

### PAC-14 is amended, not rewritten

PAC-14 §2 made the hard-coded backend DSN the documented default. AC-A14
removes that default. The amendment is a dated block at the top of the "DSN
handling" item, and the original text is left as approved. That keeps the
history readable. The frontend's fallback is out of scope (WEB-sentry-default).

## 3. Alternatives rejected

1. **Make `/api/health` the readiness probe** (with the database check).
   Rejected: it would break the web's preview-mode decision and the meaning
   the e2e global setup relies on, and AC-A11 requires liveness unchanged.
2. **`GET /api/health?ready=1`** (one path, two meanings). Rejected: one
   operation with two contracts. The generated types would merge both
   answers, and a query parameter a proxy drops would turn readiness into
   liveness without anyone noticing.
3. **List the 503 on all 128 operations.** Rejected for this revision: it
   would touch every fragment, including `auth.yaml` under PAC-71's rewrite.
   The extra entries would say nothing beyond the one API-level paragraph,
   for a response that only pool exhaustion can cause on those operations.
   Revisit if a client ever needs the 503 per operation in its generated
   types.
4. **A `reason` enum on the readiness 503** (`database`, `shutting_down`).
   Rejected: see §2. It is public information for no consumer.
5. **A new status (e.g. 202) when the budget expires after a booking is
   saved.** Rejected by the spec (F-12). It would also break the web booking
   page, which treats only 201 as success.
6. **Write the callback redirect rule into each callback's 302 description
   now.** Rejected for now: those four descriptions are being rewritten by
   PAC-71. The rule is in `info.description`, and the manifest records a
   prose-only follow-up to fold it in after PAC-71 merges.

## 4. Merge interplay

- **PAC-71 (#242)**: no textual conflict in `auth.yaml`, because this
  contract does not edit it. There is a semantic one (challenge B4).
  PAC-71's connect branch allows only `connect=credited` or
  `connect=not_credited` and requires the connect state cookie to be
  expired. Rev 2 states the budget answer on that branch to fit:
  `/?connect=not_credited`, cookie expired.
  - If PAC-71 lands first: this branch rebuilds the bundle on top of it,
    and the info paragraph already matches.
  - If this lands first: PAC-71's stage-4 PR implements and tests the
    budget answer on the connect branch, and the prose follow-up folds the
    rule into the four 302 descriptions.

  In `teams.yaml` and `scheduling.yaml`, PAC-71 adds a 401 at the top of the
  responses and this contract adds a 503 at the end, so those should merge
  cleanly. The generated files (bundle, `MIGRATION.md`, `paceday.gen.go`)
  conflict whichever lands second. Regenerate them, never merge them by hand.
- **PAC-72**: rewrites `createPublicBooking`'s description, its 201 and its
  429 status list, and `getPublicBookingSlots`' description. A hand merge is
  unavoidable: keep both texts (manifest `mergeInterplay`). If PAC-72 lands
  first, its pre-insert per-host read is a per-item read under AC-A18.

## 5. Challenge record

Revision 1 was rejected ("Challenge, revision 1" below). Revision 2 answers it
("Revision 2" at the end).

## Challenge, revision 1 — 2026-10-28

Challenger: contract-challenger (factory stage 2 gate). Reviewed
`git diff origin/main...d568474` on `factory/pac-81-part-a-contract`
(bundle sha256 `8fe98789…5c14`, equal to the manifest's `contractHash`), the
spec `docs/specs/NEW-deploy-hardening.md` rev 3, the handlers on this branch,
`Enach/smart-calendar-flow` `origin/main`, `mcp/`, `e2e/`, and PAC-71's
`origin/impl/pac-71-identity-binding-backend` (459bc02). No Part B branch
exists on the remote.

**Verdict**: reject (four blocking findings; each fix is fragment prose or
one schema change plus regeneration, no reshaping of the feature)

### Blocking

**B1. The `getReadiness` 503 breaks the PAC-21 bundle rule; the PAC-21
contract test fails on this bundle.**
`TestPAC21Every5xxInTheContractIsTheJSONEnvelope`
(`backend/api/pac21_contract_5xx_test.go:195`) requires every 5xx in the
bundle to be an `{error: string}` envelope. Run against this bundle (test file
copied to a scratch module with the bundle, no database needed):

```
--- FAIL: TestPAC21Every5xxInTheContractIsTheJSONEnvelope
    1 of 126 5xx responses break the PAC-21 envelope rule:
    get /api/ready 503: schema ReadinessNotReady is not an {error: string} envelope
```

The same test on `origin/main`'s bundle: `ok`. So `make test` fails on the
stage-4 PR, and the implementer can fix it only by editing the contract
(forbidden at stage 4) or a PAC-21 test (silently narrowing PAC-21 AC-3).
Permitted response: `GET /api/ready` → `503 {"status":"not_ready"}`.
Fix, one of: (a) preferred: `ReadinessNotReady` becomes the envelope,
`{error: enum ['not ready']}`, `required: [error]`,
`additionalProperties: false` (probes read only the status code; nothing
reads this body), with the manifest recording the AC-A11 body change; or
(b) keep `{status: not_ready}` and add to the manifest an explicit PAC-21
AC-3 exemption for exactly `get /api/ready 503`, with an `acceptanceTests`
line requiring the stage-4 PR to add that one exemption to the test, and a
note in `contracts/features/NEW-pac21-error-leaks-remaining.md`.

**B2. Readiness is public, outside every limit, and does one database ping
per call: a request flood holds pooled connections.** `/api/ready` is under
nginx's `location /api/` (`nginx/nginx.conf:5`), so anyone can call it. The
contract makes each call ping the database with a 2 s timeout and puts it
"outside every abuse limit". With the spec's pool cap of 20 (spec line 457),
200 concurrent `GET /api/ready` while the database is slow hold all 20
connections, so every other request waits for the pool and ends in the
budget 503; with a healthy database every anonymous GET is still a database
round trip. Nothing bounds concurrent pings. Fix (prose on `getReadiness`
plus an `acceptanceTests` line; not an abuse limit, so AC-A11 still holds):
"Concurrent readiness calls share one database ping: at most one ping is in
flight per process, and a call arriving while it runs gets its result.
Readiness never holds more than one pooled connection. The 2 s includes the
wait for a pooled connection." Test: 50 concurrent calls against a stub
that counts pings → at most 1 in flight.

**B3. `createPublicBooking`: the answer when the budget expires while the
booking insert is in flight is undefined, and the shared rule says 503.**
The save is one autocommit `INSERT … RETURNING`
(`backend/storage/scheduling_links.go:395-401`, called at
`backend/engine/booking.go:229`). `POST /api/book/intro` with
`{"name":"A","email":"a@x.test","start":"2026-11-02T09:00:00Z","duration":30}`:
pool wait and conflict check take 89.9 s, the INSERT is sent at 89.9 s and
commits at 90.2 s. `RequestBudgetExpired` says "When the handler has not
finished by then … the answer is this 503", and `info.description`'s only
exception is "a `createPublicBooking` whose booking was saved answers 201";
at 90.0 s nothing is saved yet, so both texts permit
`503 {"error":"request timed out"}`, whose description tells the client "a
client may retry it: nothing was written" while the row exists. The retry
answers 409 against the booker's own booking. The rationale (§2 "Booking
create") says the server "must find out whether the row exists", but that is
not in the bundle, and a lost database connection makes the outcome
unknowable. Fix (bundle prose on `createPublicBooking` 503 and 500, on
`RequestBudgetExpired`, and in the `INFO` exception): "From the moment the
booking insert is sent, this request is not answered with the budget 503:
the server waits for the insert's own result (bounded by the insert's own
duration, not by the budget) and answers 201 if the row was saved, or the
500 if the insert failed. A 500 from this operation does not prove that no
booking was saved (a lost database connection leaves the outcome unknown);
only the 503 does." Add an `acceptanceTests` line: insert held by a stub
past the budget → 201, never 503.

**B4. The callback exception contradicts PAC-71's connect branch.**
`info.description` says `googleOAuthCallback` and `microsoftOAuthCallback`
answer `Location <FRONTEND_URL>/?error=auth_failed` on budget expiry. On
`origin/impl/pac-71-identity-binding-backend`, `contracts/openapi/paths/auth.yaml`
gives both a CONNECT branch with exactly two destinations
(`/auth/callback?provider=google&connect=credited` or
`/?connect=not_credited`, auth.yaml:235-249 there; Outlook :550-562), a
`Location` pattern allowing nothing else, and requires every connect-branch
response to expire the connect state cookie.
`GET /api/auth/google/callback?state=<connect state>&code=c` that waits for
the pool past 90 s would, by this contract, get
`302 Location: <FRONTEND_URL>/?error=auth_failed`: a destination PAC-71's
pattern forbids, with the connect state left live. PAC-71 is Urgent and may
merge first; `mergeInterplay` says there is no conflict. Fix (one sentence in
the `INFO` paragraph, and correct `mergeInterplay`): "On a callback carrying a
calendar-connect state (PAC-71), budget expiry answers that branch's NOT
CREDITED redirect (`<FRONTEND_URL>/?connect=not_credited`) with the connect
state cookie expired; budget expiry never answers `?error=signin_refused`."

### Requests wrongly permitted

1. A create whose insert is in flight at the budget (B3).

### Responses the client cannot handle

1. `getReadiness` `503 {"status":"not_ready"}` is rejected by the PAC-21
   rule and its test (B1). No web or MCP code calls `/api/ready`.
2. Not new, but the manifest's web claims are moot: `requestApi` aborts every
   call at 4 s (`smart-calendar-flow/src/api/client.ts:318`,
   `NETWORK_TIMEOUT_MS = 4000`), so the web never receives a budget 503 (all
   fire at 80–90 s). `getPublicSlots` then goes through `withFallback`
   (`client.ts:784-797`) and shows preview data; `bookSlot`
   (`src/api/schedulingLinks.ts:718-733`) reports "unreachable" for a booking
   that may be saved. Today's behaviour, owned by follow-up WEB-timeouts. The
   `consumersAffected` entry citing `client.ts:577` should say so (and
   `apiErrorMessage` shows `data.error`, "request timed out", before the
   generic 5xx line).

### Consumers affected

- `backend/api/pac21_contract_5xx_test.go:195`: fails (B1).
- `smart-calendar-flow/src/api/client.ts:318,784`: 4 s abort, see above.
- `mcp/client.go:19,61`: `http.Client{}` with no timeout waits the budget and
  returns `backend 503: {"error":"request timed out"}`. Fine.
- `e2e/global-setup.ts`: keeps `/api/health`. Fine.
- `src/api/generated`: `const` and one-value `enum` already generate
  (`z.literal`, e.g. `BookingRateLimitError`). Fine.

### Contract/implementation divergences

1. The manifest says the 413 body is the "same body as the Part B plan's
   nginx 413 … (Part B plan already does)". No Part B plan or branch exists on
   the remote and `request body too large` appears nowhere else in the
   repository; spec AC-B8 says only "the backend's error shape". Keep the
   mergeCondition (it binds Part B); drop the "already does" claim.

### Non-blocking

1. **Shortened deadline: free or 503?** Each read gets
   `min(15 s, remaining − 10 s)`. A read whose deadline came from the budget
   term (remaining 18 s → 8 s) and hits it: "its own time limit" (free, 200)
   or "cut short by the budget" (503)? The same stub gives either. Say on
   `getPublicBookingSlots` and `getTeamAvailability`: "a read that hits a
   deadline shortened by the budget counts as cut short by the budget (503)."
2. `ssoAdminStepupCallback`: "the reason of the check that was running" has
   no value before check 0 (pool wait) or during step 5 (grant issue). Name
   the reason for those cases, or say step 5 completes.
3. "Connection refused" while migrating or after the notice holds only
   directly; through nginx (AC-D1's probe path) it is nginx's 502. Say so.
4. Readiness latency and its 503 window reveal database health and deploy
   timing to anyone. Acceptable for a probe; worth a line in
   `docs/security.md` (AC-B10) that operators may restrict `/api/ready` at
   their proxy.
5. `info.description` says "any other JSON operation"; the manifest puts
   `handleZoomOAuthCallback` (text/plain 400s, 302) under the same JSON 503.
   Say "any other operation".

### Uncertainties not actually resolved

None claimed (`resolvesUncertain` and `resolvesFindings` are empty).

### Verified clean

- Gates: `py -3 scripts/openapi_assemble.py --check` rc 0 (106 paths);
  `py -3 scripts/openapi_migration_report.py --check` rc 0 (128 operations,
  9 generated); `bash scripts/openapi_gen_go.sh --check` rc 0;
  `cd backend && go build ./...` rc 0, `go vet ./...` rc 0. Bundle sha256
  equals `contractHash`.
- Listing the 503 on five operations while declaring it API-wide is
  consistent with PAC-21: its rule (AC-3, the bundle walk) is that every
  documented 5xx is the JSON envelope, not that every reachable 5xx is
  documented per operation. All four `RequestBudgetExpiredError` uses pass
  the walk.
- `getHealth` and `HealthResponse` bytes unchanged; `auth.yaml` untouched.
- `/api/ready` is reachable through nginx's `/api/` prefix and collides with
  no existing path; no body names a cause.
- No operation accepts a body near 1 MiB (no multipart or upload in handlers
  or bundle), so the 413 cap breaks nothing.
- Treated-as-free on slot listing does not contradict
  `PublicLinkCoverage.checked`, which already counts stored tokens, not
  successful reads (PAC-67 D-14).
- MCP calls only `parseNaturalLanguage` among the changed operations; any
  status >= 400 already becomes an error string.
- MIGRATION.md has the `getReadiness` row (handwritten, stage-4 flip
  stated); the register check passes.
- PAC-14 §2 amendment is an additive block; the approved text is kept.

## Revision 2 — 2026-10-28

Author: contract-author. Answers "Challenge, revision 1" with the
coordinator's decisions. The bundle sha256 is
`ffcc55cc0a8fa14179f506e929749bff936cf10f208b94fdc350713ad035e70e` (commit
`5a25c59`).

Checks run, all rc 0:
- `py -3 scripts/openapi_assemble.py --check`
- `py -3 scripts/openapi_migration_report.py --check`
- `bash scripts/openapi_gen_go.sh --check`
- `go build ./...` and `go vet ./...`
- the PAC-21 bundle walk. The test file was copied into a scratch module
  next to the new bundle: `TestPAC21Every5xxInTheContractIsTheJSONEnvelope`
  and `TestPAC21ContractCheckCatchesBadShapes` both PASS.

| # | Finding | Outcome | Where / why |
|---|---|---|---|
| B1 | Readiness 503 breaks the PAC-21 envelope rule | **Applied** (preferred fix a) | `ReadinessNotReady` is now `{error: enum ['not ready']}`, required, `additionalProperties: false`. The manifest records the AC-A11 body change. The PAC-21 walk passes on this bundle (rc 0). |
| B2 | A readiness flood holds pooled connections | **Applied** | `getReadiness`: single flight (at most one ping in flight per process, concurrent calls share its result); the 2 s includes the pool wait; readiness never holds more than one connection; shutting down answers without a ping. Manifest: a 50-concurrent-calls test in `acceptanceTests` and `mergeConditions`. |
| B3 | Booking insert in flight at the budget | **Applied** | `createPublicBooking` 503: never answered once the insert is sent. The handler waits for the insert's own result: 201 if saved, 500 if failed. 500: does not prove that nothing was saved (lost connection). Only the 503 proves it. Same rule in `RequestBudgetExpired` and the info paragraph. Test: insert held past the budget answers 201, never 503. |
| B4 | Callback exception contradicts PAC-71's connect branch | **Applied** | Info paragraph: connect-state callbacks answer `/?connect=not_credited` with the connect cookie expired; never `signin_refused`; sign-in branches keep `/?error=auth_failed`. `mergeInterplay` now states the semantic conflict and how the PR that lands second reconciles it. |
| Wrongly permitted 1 | Create with the insert in flight | **Applied** | See B3. |
| Client cannot handle 1 | Readiness 503 body | **Applied** | See B1. |
| Client cannot handle 2 | The web never sees the 503 (4 s abort) | **Applied** | The web entry in `consumersAffected` says the 503 is not visible to the web until PAC-83 (WEB-timeouts) ships. It names today's preview fallback and the "unreachable" result on a booking that may be saved, and says `apiErrorMessage` would show `request timed out`. |
| Divergence 1 | False "Part B plan already does" 413 claim | **Applied** | Claim removed from the manifest and §2. The merge condition binding Part B to `{"error":"request body too large"}` is kept. |
| NB1 | A budget-shortened deadline: free or 503? | **Applied** | Slot listing and team availability: a read that hits a deadline shortened by the budget counts as cut by the budget (503). Also in `RequestBudgetExpired`. Test line added. |
| NB2 | Step-up reason before check 0 or during step 5 | **Applied** | Before check 0 completes: `state_mismatch`. Step 5, once started, is waited for: granted, or the existing 500. |
| NB3 | nginx 502 instead of connection refused | **Applied** | `getReadiness`: through nginx, migrations or the period after the notice give nginx's 502; a probe treats any non-200 as not ready. |
| NB4 | Readiness reveals database health and deploy timing | **Applied** | `docs/security.md` §2, new boundary B8: public by design, single flight, operators may restrict `/api/ready` at their proxy. Also stated on `getReadiness`. |
| NB5 | "any other JSON operation" | **Applied** | Now "any other operation". |

Nothing in "Uncertainties not actually resolved" needed action: none were
claimed.

## Challenge, revision 2 — 2026-10-28

Challenger: contract-challenger (factory stage 2 gate), confirming round.
Reviewed `git diff 0a9a88f 3268531` on `factory/pac-81-part-a-contract`
(bundle sha256 `ffcc55cc…e70e`, equal to the manifest's `contractHash` and to
`git show 5a25c59:contracts/openapi/openapi.yaml`), the "Revision 2" table,
PAC-71's `origin/impl/pac-71-identity-binding-backend` (459bc02)
`contracts/openapi/paths/auth.yaml`, and spec AC-B10. No Part B branch
(`impl/pac-81-part-b-*`) exists on the remote.

**Verdict**: accept. No blocking finding. The contract is ready for Part A
implementation (stage 3/4). The non-blocking items below are for the stage-4
PR, or for whichever of PAC-71 and PAC-81 lands second.

### Revision 1 blocking findings, re-checked

- **B1, fixed.** `ReadinessNotReady` is `{error: enum ['not ready']}`,
  `required: [error]`, `additionalProperties: false`. The PAC-21 test file
  `backend/api/pac21_contract_5xx_test.go` was copied into a scratch module
  next to this bundle and run with `go test -count=1`: rc 0.
  `TestPAC21Every5xxInTheContractIsTheJSONEnvelope` PASS,
  `TestPAC21ContractCheckCatchesBadShapes` PASS. The revision 1 run on
  `d568474` failed with `get /api/ready 503: schema ReadinessNotReady is not
  an {error: string} envelope`.
  `GET /api/ready` → `503 {"status":"not_ready"}` is no longer permitted.
  `503 {"error":"not ready"}` is the only 503 body.
- **B2, fixed.** `getReadiness` states single flight (at most one ping in
  flight per process; joiners get its result), at most one pooled
  connection, a 2 s timeout that includes the pool wait, and no ping after
  shutdown has begun. It is implementable with `net/http` and
  `database/sql`. `DB.PingContext(ctx)` acquires its connection through
  `db.conn(ctx, …)`, which waits for a free connection in a `select` on
  `ctx.Done()`, so a `context.WithTimeout(…, 2s)` bounds the pool wait as well
  as the driver ping. A retry on `driver.ErrBadConn` is sequential, so it
  still holds one connection. A shared ping needs a context that does not
  come from one caller's request, or one caller's disconnect would cancel
  the others. That is an implementation detail; the contract does not
  forbid it. "Answers 503 without pinging once shutdown began" holds if the
  handler reads the shutdown flag before it joins or starts a ping. The
  manifest's `acceptanceTests` (AC-A11) and `mergeConditions` require the
  50-concurrent-calls test.
  Attack: 200 × `GET /api/ready` while the database stalls. The contract
  now permits exactly one ping and one connection, and every call answers
  `503 {"error":"not ready"}` within 3 s. The other 19 pool connections stay
  available.
- **B3, fixed and consistent.** The same rule appears in every place:
  `createPublicBooking` 503 ("BEFORE the booking insert was sent … from the
  moment the insert is sent … never answered with this 503"), its 500 ("does
  NOT prove that no booking was saved … Only the 503 proves"),
  `RequestBudgetExpired` (exception paragraph and per-operation line), the
  `INFO` exception (4), and the manifest's `operationsChanged` and AC-A19
  test line ("insert held by a stub past the budget -> 201, never 503;
  insert failing after the budget -> 500, never 503").
  Revision 1 payload `POST /api/book/intro`
  `{"name":"A","email":"a@x.test","start":"2026-11-02T09:00:00Z","duration":30}`,
  with the INSERT sent at 89.9 s and committed at 90.2 s, now has exactly one
  permitted answer, 201.
- **B4, fixed for the case it named.** `INFO` exception (2): a Google or
  Microsoft callback that carries a connect state answers
  `<FRONTEND_URL>/?connect=not_credited` with the connect cookie expired, and
  never `signin_refused`. Both values are in PAC-71's Location patterns
  (auth.yaml:249 and :562 on 459bc02). PAC-71 requires every connect outcome
  to expire `google_connect_state` / `ms_connect_state` (:192, :263, :576),
  which matches. Sign-in branches keep `?error=auth_failed`, which PAC-71
  allows with "what was saved before that point is kept" (:229-234).
  `mergeInterplay` now names the semantic conflict and says how it is
  reconciled. See non-blocking 1 for a narrower race in the same place.

Revision 1 non-blocking items NB1 to NB5 are applied as the table says: the
budget-shortened deadline gives 503 on both reads and on
`RequestBudgetExpired`; step-up names `state_mismatch` and handles step 5;
nginx's 502 is stated; the security note is in B8; the text says "any other
operation".

### Requests wrongly permitted

None blocking. See non-blocking 1.

### Responses the client cannot handle

None new. The readiness 503 body has no reader: probes read the status code,
and `checkApiAvailability` calls `/api/health`.

### Consumers affected

No change from revision 1. MCP (`mcp/`) and `e2e/` do not call `/api/ready`.
`e2e/global-setup.ts` still polls `/api/health`, whose bytes are unchanged.

### Contract/implementation divergences

None. No handler exists yet, and every statement is a stage-4 obligation that
has a test line.

### Non-blocking

1. **Connect step 6 in flight at the budget (the B3 race on PAC-71's connect
   branch).** PAC-71 defines NOT CREDITED as "nothing stored on any account"
   (auth.yaml:240-241 on 459bc02), and step 6 stores the token in one
   transaction. Take `GET /api/auth/google/callback?state=<connect state>&code=c`
   whose step-6 commit is sent at 89.9 s and lands at 90.2 s. Exception (2)
   allows `302 Location: <FRONTEND_URL>/?connect=not_credited` while the token
   is stored. The harm is small: the user is told the calendar was not
   connected, a retry credits it again, and nothing is lost. The rule also
   cannot be implemented on `main` until PAC-71 merges. Fix this when the two
   are reconciled, in the same way as step-up step 5 and the booking insert:
   "once the step-6 transaction has been sent it is waited for, and the
   answer is CREDITED if it stored, NOT CREDITED if the generation moved, or
   the 500". Add that sentence to `followUps.callbackProseAfterPac71` or to
   `mergeInterplay`.
2. **Readiness: shutdown that begins during a shared ping.** The 200
   condition "the process has not begun shutting down" does not say when it
   is read. A call that joined a ping before SIGTERM may answer 200 just
   after the notice starts. A probe re-polls inside the 5 s notice, so this
   is harmless. If tightening it is cheap, say "the shutdown flag is read
   when the answer is chosen".
3. **`docs/security.md` line endings.** Commit 5a25c59 rewrote the whole
   file from CRLF to LF and dropped three stray CRs (`\r\r\n`). The real
   change is one line, the B8 entry, but `git diff` shows 243 lines. Part B
   (AC-B10) must edit this same file, so the stage-4 or Part B PR will get a
   whole-file conflict. The spec's line references (`docs/security.md:52`,
   `:53`, `:56`, `:81`, `:85`) also shift by one after B8. Restore CRLF
   (keep only the B8 line), or tell Part B to rebase on this branch. The B8
   content fits AC-B10's checklist: it is the operator's proxy, not the
   shipped nginx, that may restrict `/api/ready`, and abuse protection stays
   at application level. B8 has no STRIDE row in §3. Part B can add one.
4. The rationale's "What changed" table (§1, `createPublicBooking` row) still
   says "503 only before the booking is saved". The bundle says "before the
   insert is sent". Change the rationale to match.
5. An insert that runs past the 120 s write backstop (no statement timeout,
   spec §3 N4) gives the client a dropped connection or a proxy 504, not a
   201 or 500. The spec accepts this. The "client receives it, not a 504"
   promise is made only for the 503, so nothing contradicts it.

### Uncertainties not actually resolved

None claimed (`resolvesUncertain` and `resolvesFindings` are empty).

### Verified clean

- Gates, run on 3268531: `py -3 scripts/openapi_assemble.py --check` rc 0
  (106 paths); `py -3 scripts/openapi_migration_report.py --check` rc 0
  (128 operations, 9 generated); `bash scripts/openapi_gen_go.sh --check`
  rc 0; `cd backend && go build ./...` rc 0, `go vet ./...` rc 0; PAC-21
  bundle walk in a scratch module `go test -count=1` rc 0. Bundle sha256
  `ffcc55cc0a8fa14179f506e929749bff936cf10f208b94fdc350713ad035e70e` equals
  `contractHash`.
- A budget-shortened read (remaining 18 s, deadline 8 s, stub answers at
  9 s) now has exactly one answer, the 503, on `getPublicBookingSlots`,
  `getTeamAvailability` and in `RequestBudgetExpired`, and it has a test line.
- Step-up at the budget: before check 0 the answer is `state_mismatch`;
  during step 5 the server waits, and the answer is granted or the existing
  500. No case is left without an answer.
- `getHealth`, `HealthResponse` and `auth.yaml` are still untouched by this
  branch.
- The B8 text and the `getReadiness` description agree (single flight, one
  connection, operators may restrict at their proxy, liveness stays open).
