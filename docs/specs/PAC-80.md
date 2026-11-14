# Spec — PAC-80: PAC-71 follow-ups (test hook, profile default row, stale-session renewal, loss on aborted requests, panic in the Microsoft key fetch)

- **Linear**: https://linear.app/paceday/issue/PAC-80
- **Status**: accepted (self-challenged 2026-11-14, accept-with-changes; changes applied in place, see the Challenge log at the end)
- **Author**: spec-author
- **Base**: origin/main `d520775` (PAC-71, PAC-89 and PAC-101 merged; PAC-40 Part B merged)
- **Parent**: PAC-71 backend review rounds (Enach/clockwise-like#242); item 5 is the
  issue's comment of 2026-11-01 (PAC-71 tidy-round review).
- **Inherited scope**:
  - **None.** The only HTTP operations whose handlers this work changes are
    `GET` and `POST /api/manager/profile` (item 2). Their requests, responses and
    status sets do not change: a request whose session goes stale mid-request is
    already answered 401 by the session middleware (`api/middleware.go:150-196`).
    Only the storage step behind them changes, so the operations are not
    "touched" in the touch-it-fix-it sense (`docs/factory/README.md:280-282`),
    following the precedent of PAC-101 (`docs/specs/PAC-101.md`, "Not inherited").
    **Not inherited:** API-086 (low, Reported; `docs/factory/api-audit.md:344`,
    the global `team_member_count` on `GET /api/manager/profile`). No `x-uncertain`
    marker sits on that operation (the six markers in the bundle are elsewhere;
    `contracts/openapi/openapi.yaml:5864,10377,12716,12724,12832`).
  - No contract change in any item.

## 0. Item table (verification against current main)

| # | Item | Still valid on `d520775`? | Fix (summary) | Size |
|---|------|---------------------------|---------------|------|
| 1 | Test hook is an exported settable global in a production package | **Yes.** `storage/session.go:263-267` declares it; read unsynchronised at `:164-165` and `:185-186`; assigned by tests in another package (`api/pac71_midhandler_test.go:26,33,66,78`, `api/pac101_test.go:321,328,431,438`) | Make the seam race-free and unreachable as a plain assignable production global | XS |
| 2 | Profile get-or-create is unguarded | **Partly.** The "count the default row as empty, with a test" branch is already done: `storage/emptiness.go:43-46` ignores a default row and `storage/pac71_test.go:57-60` pins it. The write itself is still unguarded at `api/handlers_manager.go:61,88` and `engine/manager.go:217` (via `storage/manager.go:21-29`). The default row is also **not** fully equivalent to no row (see §2.2) | Run the default-row creation under the in-request re-check on request paths | S |
| 3 | Renewal ignores a stale session from its own reload and refreshes anyway | **Yes.** `auth/credential_lifecycle.go:135-155`: only a decrypt failure returns early (`:136`); a stale-session error falls through to the provider call at `:155` | Stop before any provider call when the reload says the session is stale or its generation unreadable | XS |
| 4 | Credential loss recording uses the request context; a hang-up loses it | **Yes, and wider than stated.** `RecordLoss` (`:65-74`) and the reload that gates it (`:162`) both run on the request context; the save of a successful renewal (`:171`) has the same exposure, which with a rotating provider can turn into a false "connection lost" | Finish the loss decision, the loss record and the renewal save even when the requester has gone, under a short bound, keeping the re-check | S |
| 5 | A panic in the Microsoft key fetch leaves the single-flight registered | **Yes.** `auth/ms_idtoken.go:353-371`: cleanup is straight-line code after `fetch` (`:359`); a panic skips `delete` and `close(f.done)` (`:369-370`); waiters block on `:340` until their own context ends | Always clear the flight; a panic becomes an error for the leader and every waiter | XS |

**Split proposal.** Nothing here is large; one ticket, one PR is coherent (all five
are PAC-71 hardening in `storage`/`auth`/the manager profile path). One **new**
finding is split out (§5, F-A): with no profile row a member's analytics are
treated as not shared with their manager, with a default row they are shared. That
is a pre-existing product question (consent default), not PAC-71 hardening.

## 1. Problem

PAC-71 bound accounts to provider identities and added a "session generation"
re-check before every in-request read or write of account data, so that a request
started before a binding (or a sign-out-everywhere) cannot act on the account
after it. The review of that work left five small gaps. None is exploitable
today, but each weakens a guarantee PAC-71 relies on:

1. The seam tests use to inject a binding mid-request is a writable global in
   production code, read without synchronisation. A future race-enabled run, or
   background work outliving a test, makes it a data race; and any production
   code could set it.
2. One write on the request path — creating the default manager-profile row —
   skips the re-check, so a request whose session went stale mid-flight still
   writes into the account. PAC-71's invariant ("every request-path write of
   account data runs under the re-check") has a known exception.
3. When a stored calendar credential needs renewal and the re-check says the
   session is stale, the renewal still calls the provider with the credential
   it read earlier. The result cannot be saved (the save is re-checked), so the
   call is wasted, and with a provider that rotates refresh tokens the stored
   refresh token can be burned, breaking the owner's connection.
4. When a provider permanently refuses a credential but the requester hangs up
   at that moment, the loss is not recorded (the user is not told their
   connection is lost until a later use). Worse, if the requester hangs up just
   after a successful renewal with a rotating provider, the new refresh token is
   never stored; the next renewal is refused and the user is told the
   connection is lost although it was fine.
5. If the Microsoft signing-key fetch ever panicked, every later Microsoft
   sign-in for that tenant would hang until its own timeout, until a restart.

## 2. Current behaviour (origin/main `d520775`)

### 2.1 Item 1 — the test seam

- `storage/session.go:263-267`: `var GuardedCallHook func(ctx context.Context)`,
  exported, documented "Production leaves it nil".
- Read without synchronisation before every guarded call:
  `WithSessionGeneration` (`storage/session.go:164-166`) and `withLinkWriteTx`
  (`:184-187`).
- Assigned and cleared by tests in package `api` (so a `_test.go`-only seam in
  `storage` would not be visible to them): `api/pac71_midhandler_test.go:26,33,66,78`,
  `api/pac101_test.go:321,328,431,438`.
- Guarded calls run in spawned background work too (`api/handlers_manager.go:98-105`,
  `lifecycle.GoFrom` keeps the session guard), so a test's `t.Cleanup` that sets it
  back to nil can race a background reader still in flight.
- The gate does not run the race detector: `scripts/test-backend.sh` runs
  `go test -p 1 ./...` in `golang:1.25-alpine`, which has no C toolchain, so
  `-race` is not available there. No `-race` in `Makefile`, `backend/Makefile` or
  `.github/workflows/`.

### 2.2 Item 2 — the profile default row

- `storage/manager.go:21-29`: `GetOrCreateUserProfile` inserts
  `user_profiles (user_id)` with `ON CONFLICT DO NOTHING`, then reads it, on the
  plain handle — no re-check.
- Request-path callers: `getProfile` (`api/handlers_manager.go:61`), `postProfile`
  (`:88`), and `DetectTeam` (`engine/manager.go:217`), which runs both inside a
  request (`api/handlers_manager.go:126`, under the request's guard) and spawned
  from `postProfile` (`:98-105`, guard kept), and from the scheduled job
  (`main.go:289`, no guard).
- `postProfile`'s own update is guarded (`storage/guarded_writes.go:472-478`),
  and the response becomes 401 when any guarded call found the session stale
  (`api/middleware.go:150-196`). So the visible effect of the gap is only the
  default row.
- Emptiness: a row at its defaults does not make an account non-empty
  (`storage/emptiness.go:43-46`, "manager_profile" counts only `is_manager`,
  `detected_at` set, or sharing turned off); pinned by `storage/pac71_test.go:57-60`.
  The issue's second option is therefore already met.
- **The default row is not fully equivalent to no row.** The schema default is
  "shared with manager" (`storage/migrations/016_manager.up.sql:5`,
  `DEFAULT true`). The team-analytics read (`engine/manager.go:461-465`) selects
  `COALESCE(analytics_shared_with_manager, true)` but ignores `sql.ErrNoRows`, so
  with no row the local variable stays `false`: **no row = not shared, default
  row = shared**. For an account that a manager's roster points at, a stray
  default row therefore flips whether its weekly analytics are shown to that
  manager. (An *empty* account cannot be on a roster at binding time —
  `storage/emptiness.go:48`, "roster_entry_pointing_at_account" — so this is
  not a PAC-71 leak, but it is why "benign" is not quite true.)
- The existing mid-handler test tolerates the default row explicitly: it counts
  only profile rows with `is_manager OR detected_at IS NOT NULL`
  (`api/pac71_midhandler_test.go:114-118`), and it binds at the **first** guarded
  call (`:94`, `bindAtGuardedCall(t, 1, d)`), which today is `postProfile`'s
  update, reached *after* the detection was spawned (`api/handlers_manager.go:97-107`).

### 2.3 Item 3 — renewal under a stale session

`auth/credential_lifecycle.go` `renewingSource.Token()`:

- `:125-127` returns the in-memory token when still usable (no load, no call).
- `:135` reloads the stored credential through the guarded loaders
  (`googleStore.load` → `auth/session_guarded.go:16-25`; Microsoft `:28-34`; Zoom
  `storage/guarded.go:89-95`), which return `storage.ErrStaleSession` (or an error
  wrapping `ErrGenerationUnavailable`) from `WithSessionGeneration`.
- `:136-140` returns early only for a decrypt failure (PAC-84).
- Otherwise a load error is ignored: `:141` skips the update, and `:147-155` renews
  with `s.cur` — the token read when the source was built — at the provider.
- `:171-173`: the save is guarded (`googleStore.save` `:225-227`;
  `storage.UpdateMicrosoftCredentialCtx`, `storage.UpdateZoomCredentialCtx`
  `storage/guarded_writes.go:397-402`), so under a stale session it fails and is
  only logged; the renewed token is returned to the caller, whose next guarded
  call (or the middleware) turns the response into 401.
- Zoom sources treat a missing expiry as expired (`:202`, `zeroExpiryIsExpired`),
  and Zoom rotates refresh tokens, so a renewal that is not saved leaves a stored
  refresh token the provider no longer accepts.
- Test coverage: `auth/pac71_guarded_test.go:17` checks the loaders refuse a
  stale session; no test drives `Token()` with a stale guard.

### 2.4 Item 4 — loss recording and renewal save on an aborted request

- `RecordLoss` (`auth/credential_lifecycle.go:65-74`) calls
  `storage.RecordCredentialLossCtx(ctx, …)` (`storage/guarded_writes.go:217-224`)
  then `WriteAuditLogCtx`; both go through `WithSessionGeneration`, which either
  begins a transaction on `ctx` (`storage/session.go:209`) or binds `ctx`
  (`:162`). A cancelled `ctx` fails both; the failure is only logged (`:67-69`).
- Its only caller (`:162-164`) first reloads the stored credential on `s.ctx`;
  on a cancelled context that reload fails, so `RecordLoss` is not even reached
  and the result is `LostError{Recorded: false}`.
- The provider call itself (`:155`) runs on `s.ctx`; a hang-up *before* the
  provider answers ends it with a context error, which is a temporary failure
  (`IsPermanentRefusal`, `:55-58`) and records nothing. That case is fine: the
  next use renews again.
- The renewal save (`:171`) runs on `s.ctx` too. A hang-up after the provider
  returned a rotated refresh token loses it.
- Precedents for finishing a write after the requester left, with the
  request's values (and so the session guard) kept: `api/handlers_sso_stepup.go:691`,
  `engine/booking.go:688`, `engine/personal_blocker.go:103`,
  `engine/booker_erasure.go:89` (the last bounded by a timeout).
- PAC-81 AC-A9: the database pool wait ends with the context
  (`storage/session.go:161-162`), so any detached work must carry its own bound.

### 2.5 Item 5 — Microsoft key fetch single-flight

`auth/ms_idtoken.go` `key()`:

- `:353-355` registers `f` in `v.flights[addr]` and releases the lock.
- `:359` calls `v.fetch(context.WithoutCancel(ctx), addr)` — not under the lock.
- `:361-371` re-locks, stores, sets `f.keys/f.err`, deletes the flight and closes
  `f.done` — straight-line code, no `defer`.
- A waiter (`:337-344`) selects on `f.done` or its own context.
- If `fetch` panics (for instance a custom `HTTPClient` transport that panics,
  `:445-449`), the leader's goroutine unwinds (net/http recovers it per request),
  the flight stays registered forever, and every later call for that key address
  waits until its own context ends, then fails, until a restart. No current path
  in `fetch` panics.
- `fetch` takes and releases `v.mu` at `:436-438` only to count; nothing else in
  it holds the lock.
- Other single-flights in the codebase run the work in a bare goroutine
  (`auth/sso_detect.go:228-229`, `api/readiness.go:51-53`): a panic there crashes
  the process, so they cannot wedge; not in scope.

## 3. Desired behaviour

1. The mid-request test seam cannot be changed by production code by plain
   assignment, and setting, clearing and firing it from different goroutines is
   free of data races. Tests that inject a binding mid-request keep working the
   same way.
2. On a request with a session, the default manager-profile row is created only
   when the session's re-check passes, like every other request-path write. A
   request whose session goes stale before that point leaves no profile row in
   the account. Scheduled work without a session behaves as today.
3. Renewing a stored credential on behalf of a request whose session is stale,
   or whose generation cannot be read, makes no call to the provider, records
   no loss and changes nothing stored; the request is refused as stale (401) or
   unavailable (500) exactly as any other guarded call would.
4. Once the provider has answered a renewal, the server finishes acting on that
   answer even if the requester has gone: a permanent refusal of the credential
   still stored is recorded as a loss (with its one activity entry), and a
   successful renewal is stored. This finishing step keeps the session re-check
   (a stale session still records and stores nothing) and is bounded in time so
   a stuck database cannot hold it beyond a few seconds.
5. A failure of any kind in fetching a Microsoft tenant's signing keys, a panic
   included, ends the fetch for everyone waiting on it with an error, leaves
   nothing cached, and lets the next sign-in for that tenant start a fresh fetch.

## 4. Acceptance criteria

Test levels: `[unit]` = Go test in the backend packages (most need the test
database, as today's tests in the same files do). No `[contract]` or `[e2e]`
criterion: nothing on the wire changes.

### Item 1

AC-1. Given the backend's non-test production source, when it is scanned for
package-level, exported, assignable variables of function type in the storage
package, then none exists (today: the test seam is one). [unit — source scan,
fails on `d520775`]

AC-2. Given a test that, from several goroutines concurrently, installs the
mid-request seam, clears it, and passes through guarded calls (with and without a
link-write transaction) 1 000 times, when the backend test suite is run with
the race detector enabled, then the race detector reports nothing for it.
[unit — run with `-race` on a host with a C toolchain; recorded in the PR body.
Not enforceable by the gate today, see D-2]

AC-3. Given every existing test that injects a binding mid-request
(`api/pac71_midhandler_test.go`, `api/pac101_test.go`), when the suite runs, then
each still passes with the seam's new form and no test leaves the seam installed
after it ends (a later test that fires a guarded call observes no hook).
[unit]

### Item 2

AC-4. Given an account with no profile row and a session whose generation is
advanced (a binding commits) after authentication but before the handler's first
storage call, when `GET /api/manager/profile` is sent, then the answer is 401
and the account still has **no** profile row. [unit — handler test with the
mid-request seam; fails on `d520775`, the row is created]

AC-5. Same as AC-4 for `POST /api/manager/profile` with `{"is_manager": true}`:
answer 401, no profile row, no roster row, no team-detection provider call with
the newly bound credential. [unit; fails on `d520775` (row created)]

AC-6. Given team detection running for a request whose session is stale at the
detection's profile step, when it reaches that step, then it stops with the
stale-session error and writes no profile row. [unit]

AC-7. Given team detection run by the scheduled job (no session), when it runs
for a manager with no profile row, then it creates the default row and proceeds
exactly as today. [unit]

AC-8. Given a current session, when `GET` and `POST /api/manager/profile` are
sent, then responses and stored rows are byte-for-byte what they are today
(existing tests `api/handlers_manager_test.go`, `api/pac26_wire_keys_test.go:268`,
`api/pac21_error_bodies_test.go:81` pass unchanged). [unit]

AC-9. The existing `TestPAC71_MidHandler_SpawnedDetectionKeepsTheSessionGuard`
still proves its intent — a detection spawned by the handler carries the session
guard and makes no provider call with the newly bound credential and writes no
manager rows — with the binding injected at a guarded call reached **after** the
detection was spawned; and its row assertion is tightened to "no profile row at
all" for an account that had none. [unit — amended test; see D-4]

### Item 3

AC-10. Given a stored Google credential whose access token has expired and has a
refresh token, a renewing source built from it for a session, and the session's
generation advanced before the source's reload, when the caller asks for a token,
then the provider's token endpoint receives **zero** requests, the error matches
the stale-session error, no loss is recorded, no activity entry is written, and
the stored credential is unchanged. [unit — counting fake token endpoint; fails
on `d520775`, one request is made]

AC-11. AC-10 for the Microsoft and Zoom sources (Zoom with a credential that has
no stored expiry). [unit]

AC-12. Given the same as AC-10 but the generation cannot be read (database
refuses the re-check), when the caller asks for a token, then the provider
receives zero requests and the error matches the generation-unavailable error.
[unit]

AC-13. Given a current session or no session, every existing renewal behaviour
holds (`auth/credential_lifecycle_test.go`, all 13 tests, unchanged). [unit]

### Item 4

AC-14. Given a stored credential, a session that is current, and a provider token
endpoint that cancels the caller's context and then answers `invalid_grant`, when
the caller asks for a token, then exactly one loss is recorded for the owner and
provider, exactly one `connection_lost` activity entry is written, and the error
is a lost-connection error marked recorded. [unit — fails on `d520775`: nothing
recorded, error marked not recorded]

AC-15. AC-14 with a background context (no session) that is cancelled the same
way: one loss, one entry. [unit]

AC-16. Given the same as AC-14 but the session's generation is advanced before
the provider answers, when the caller asks for a token, then no loss and no entry
are recorded (the re-check still applies to the finishing step). [unit]

AC-17. Given a rotating provider whose token endpoint cancels the caller's
context and then returns a new access token and a **new** refresh token, when the
caller asks for a token, then the stored credential afterwards holds the new
refresh token. [unit — fails on `d520775`: the old one stays]

AC-18. Given the finishing step (AC-14 or AC-17) and a database that never
answers, when the caller asks for a token, then the call returns within the
finishing bound plus one second, and the failure is logged without token
material. [unit — fake database handle that blocks until its context ends]

AC-19. Given a requester that hangs up **before** the provider answers, when the
provider call ends with the context error, then nothing is recorded (temporary
failure, as today; `TestTemporaryFailure_RecordsNothing` keeps passing). [unit]

### Item 5

AC-20. Given a Microsoft verifier whose HTTP client panics on the key fetch, when
one verification for tenant T starts the fetch and a second verification for T
waits on it, then both return an error within one second (neither waits for its
own context deadline), neither call panics out of the verifier, and nothing is
cached for T. [unit — fails on `d520775`: the leader panics, the waiter blocks
until its deadline]

AC-21. Given AC-20 has happened, when the HTTP client is replaced by a working one
and a third verification for T runs, then it starts a new fetch (the fetch count
increases by one) and succeeds. [unit — fails on `d520775`: it waits on the
stale flight]

AC-22. The recovered panic is logged once with the tenant's key address and the
panic value, never with token material. [unit]

AC-23. Existing verifier behaviour holds: single flight on rotation, waiter
honouring its own context, refetch rate limit, LRU (`auth/ms_idtoken_test.go`
tests at `:104,:142,:184,:233,:254,:283,:332` and `auth/pac89_stepup_idtoken_test.go`
pass unchanged). [unit]

## 5. Explicitly out of scope

- **F-A (split out, new ticket proposed): no profile row vs. default row for
  analytics sharing.** With no row a member's analytics are treated as not shared
  with their manager; the schema default and the `COALESCE` say shared
  (`engine/manager.go:461-465`, `storage/migrations/016_manager.up.sql:5`).
  Which one is right is a product (consent) question. PAC-80 does not change it;
  AC-4/AC-5 only ensure a stale request no longer creates a row.
- **Enabling the race detector in the gate.** It needs a C toolchain in the test
  container (`golang:1.25-alpine` has none) and roughly doubles test time; a
  gate change is its own ticket (D-2).
- **A binding that lands between a renewal's reload and its save** (item 3's
  residual window): the provider call has already happened; the save is refused
  by the re-check as today. Closing it would need the provider call under the
  re-check transaction, which holds a database transaction across a network
  call. Not worth it for a window of one round trip.
- **Load errors other than stale/unavailable on a session-less renewal** (plain
  database errors on scheduled work) keep today's behaviour (renew with the
  token in memory).
- **Panics elsewhere in the verifier** (inside the cache update under the lock).
  No input reaches them; the single-flight fix covers the fetch, which is the
  only call into external code.
- **Other single-flights** (`auth/sso_detect.go`, `api/readiness.go`): their work
  runs in a bare goroutine, so a panic crashes the process instead of wedging it.
- **OQ-1 of PAC-71 (F1, adding a sign-in method)** and **personal Microsoft
  accounts (PAC-77)**: nothing here binds, adds or removes an identity or changes
  which accounts can sign in. The owner's default for OQ-1 ("only through an
  explicit step") is untouched.
- API-086 (see Inherited scope).

## 6. What must not change

- Every PAC-71 mid-request test: `api/pac71_midhandler_test.go` (three tests; one
  amended per AC-9 — its premise that the handler's first re-check comes after the
  spawn changes, because the profile step becomes the first re-check), the
  PAC-101 mid-request tests in `api/pac101_test.go`.
- `storage/pac71_guarded_writes_test.go` (every guarded writer refuses a stale
  session) — extended with the profile get-or-create, not weakened.
- The emptiness rule, including that a default profile row is empty
  (`storage/pac71_test.go:57-60`).
- Renewal: `auth/credential_lifecycle_test.go` (13 tests), `auth/pac71_guarded_test.go`,
  `auth/pac81_outbound_test.go`; the PAC-84 decrypt early return (`:136-140`).
- Microsoft verifier: `auth/ms_idtoken_test.go`, `auth/pac89_stepup_idtoken_test.go`.
- Manager profile wire shape: `api/pac26_wire_keys_test.go:268`,
  `api/pac21_error_bodies_test.go:81`.
- The scheduled detection job (`main.go:289`) runs without a session and still
  creates the default row when missing (AC-7). No test pins this today — a finding;
  AC-7 adds one.

## 7. Rollback

Code only: no migration, no contract or generated-code change. Reverting the PR
restores today's behaviour exactly. Rows written while the change was live are
ordinary rows the old code also writes.

## 8. Open questions

None blocking. Every decision below is technical and factory-assumed; nothing in
PAC-80 needs a product answer.

- F-A (§5) carries one product question for its own ticket: when a member's
  manager-profile row does not exist, are their weekly analytics shared with
  their manager (as the schema default says) or not (as the code does today)?
  **Owner:** Nicolas Hochart. **Resolve by:** before F-A's spec. Not blocking PAC-80.

## Decisions

| ID | Decision | Source |
|----|----------|--------|
| D-1 | Item 1: the seam becomes race-free and not a plain assignable exported production global; the exact mechanism (synchronised holder with set/clear, or an internal test-support package) is the plan's choice, as long as tests in other packages can still install it and AC-1/AC-2/AC-3 hold. | factory-assumed |
| D-2 | The race detector is not added to the gate in PAC-80; AC-2 is run by the implementer with `-race` on a host with a C toolchain and the output pasted in the PR. A gate ticket may follow. | factory-assumed |
| D-3 | Item 2: take the issue's **first** option (guard the write), even though the second (count the default row as empty, with a test) is already met on main, because (a) it removes the last known exception to PAC-71's request-path write invariant and (b) the default row is not behaviourally identical to no row (§2.2). | factory-assumed |
| D-4 | The existing mid-handler detection test is amended (binding injected at a later guarded call, row assertion tightened), not deleted; its intent is preserved (AC-9). | factory-assumed |
| D-5 | Item 3: both stale and unreadable-generation reloads stop the renewal before the provider call. Other load errors keep today's behaviour (§5). | factory-assumed |
| D-6 | Item 4: the issue asks "accept or fix"; fix. The loss decision (reload + record + activity entry) and the renewal save finish after a hang-up, keeping the request's values (so the re-check), under a short fixed bound of **5 seconds**, well inside the 30 s drain unit (`api/handlers_manager.go:101-102` pattern). Widening to the renewal save is added scope from §2.4 (false loss on rotating providers). | factory-assumed |
| D-7 | Item 5: a panic in the fetch is recovered and turned into the fetch's error for leader and waiters (not re-panicked), logged once (AC-22). The issue comment proposes exactly this. | factory-assumed (issue comment) |
| D-8 | PAC-71 OQ-1 stays open with its default (adding a sign-in method only through an explicit step); PAC-80 neither depends on nor changes it. Personal Microsoft accounts remain with PAC-77. | owner, 2026-11-01 (recorded in `.claude/factory/owner-decisions.md`) |
| D-9 | No inherited audit scope: the manager-profile operations are not modified on the wire (PAC-101 precedent). | factory-assumed |

## Challenge — 2026-11-14

**Verdict**: accept-with-changes (changes applied in the body above; nothing blocking remains)

Challenger pass by the same factory run, adversarial, against the first draft.

### Blocking (resolved in place)
1. **First draft's item 2 fix silently broke an existing test.** Guarding the
   profile step makes it the handler's first re-check, before the detection is
   spawned; `TestPAC71_MidHandler_SpawnedDetectionKeepsTheSessionGuard` binds at
   call 1 (`api/pac71_midhandler_test.go:94`) and waits for a second re-check from
   the detection, which would never come (the handler now returns before spawning).
   The draft listed it as "must not change". **Resolved:** §2.2 states the
   premise, AC-9 and D-4 require the test's amendment with its intent preserved.
2. **First draft's item 4 AC only covered `RecordLoss`.** The reload that gates
   it (`auth/credential_lifecycle.go:162`) runs on the cancelled context first, so
   making only `RecordLoss` survive a hang-up would still record nothing — the AC
   would pass a fix that does not work only if the test happened to cancel later.
   **Resolved:** AC-14 cancels before the provider answers, so the reload is
   covered; §3 item 4 names the whole finishing step.
3. **First draft's item 1 AC ("passes under -race") could not be falsified by
   the gate**: the test container has no C toolchain. **Resolved:** AC-1 is a
   source-scan test the gate runs and that fails today; AC-2 keeps the race run as
   an implementer-run check, stated honestly (D-2).

### Non-blocking
1. Item 4 had a sharper consequence than the issue said: a hang-up after a
   successful rotating renewal loses the new refresh token and later produces a
   false "connection lost". Added as AC-17 (scope widened, D-6).
2. Item 2's "benign" claim is not quite true (§2.2: no row = not shared, default
   row = shared). Not a PAC-71 leak because an empty account cannot be on a roster;
   split out as F-A.
3. Item 3's residual window (binding between reload and save) is acknowledged
   out of scope with the reason.
4. AC-18's bound is a number the plan may tune; the spec pins 5 s (D-6) so the
   test is concrete.
5. Sections 3–5 mention the manager profile endpoint path in ACs. It is not
   changed, only used to drive the test; the identifiers of functions and files
   appear only in §2, §5 cross-references and §6 test names.

### Criteria I could not falsify
- AC-2 inside the gate (by design, D-2). Every other AC names a test that fails on
  `d520775` or pins existing behaviour that a regression would break.

### Citations I checked and found wrong
- Issue: "`handlers_manager.go` ~87" — the `postProfile` call is at `:88`; there is
  also `getProfile` at `:61`, which the issue does not name. Covered.
- Issue: "`key()` closes `f.done` only when `fetch` returns normally" — correct
  (`auth/ms_idtoken.go:359-371`). Issue's "until its own request context ended" —
  correct (`:338-342`).
- None in §2 after re-verification.

### Consumers this breaks
- `api/pac71_midhandler_test.go:89-124` — amended per AC-9 (see Blocking 1).
- Tests that assign the seam directly (`api/pac71_midhandler_test.go`,
  `api/pac101_test.go`) — must move to the seam's new form (AC-3).
- No web consumer: no wire change. `smart-calendar-flow` reads neither the
  profile default row nor any of these internals.
