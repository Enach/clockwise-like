# Contract: PAC-83 (web client timeouts, no invented data after failures)

- **Linear**: PAC-83, https://linear.app/paceday/issue/PAC-83/web-client-4-second-abort-falls-back-to-preview-data-on-slow-but
- **Spec**: `docs/specs/PAC-83.md`, revision 2 (challenge 2026-11-01
  accept-with-changes at `c14a968`; revision 2 applies the owner's answer to
  OQ-1, 2026-11-01, and routes OQ-4 to this contract).
- **Stage**: contract-author, revision 1, 2026-11-01; contract-challenger
  2026-11-02, accept-with-changes applied as revision 1.1 (see the
  challenge section at the end). OpenAPI unchanged by the challenge.
- **Manifest**: `contracts/features/PAC-83.yaml`.
- **Shape of this ticket**: the product change is in the web
  (Enach/smart-calendar-flow). The HTTP interface it relies on already exists
  (PAC-81 Part A, PAC-21, PAC-72); this contract **pins** it for the web, adds
  **one description paragraph** to the shared budget 503, and fixes the
  web-side interfaces that two repos and the e2e suite must agree on: the
  client error taxonomy, the operation registry AC-7 walks, the test ids the
  e2e guard reads, and the build-time demo flag (owner OQ-1 (c)).

Verification (LF clone in `golang:1.25-bookworm`, the api-gate pattern):
`make openapi` rc 0 (only `contracts/openapi/openapi.yaml` regenerated,
generated Go unchanged), `make openapi-check` rc 0 (`OK: bundle is in sync
(106 paths)`, `OK: MIGRATION.md covers all 128 operations (10 generated)`,
`OK: backend/api/gen matches the contract`; web half SKIPPED, no web repo in
the clone), `go build ./...` and `go vet ./api/...` rc 0. Bundle sha256
`c477dbe3b05ddc81ec73698ef566ba511c090550665e8fee3768d4f954259346`.

## 1. What changed in the OpenAPI contract

| Where | Change |
|---|---|
| `components.responses.RequestBudgetExpired` (`paths/ops.yaml`) | New closing paragraph: on every operation that writes, other than `createPublicBooking`, the budget 503 does **not** prove that nothing was written; the client re-reads. Description only. |

Nothing else. No operation, schema, status or body changes. `backend/api/gen`
is byte-identical. The web's `src/api/generated/types.ts` carries this
description as a JSDoc comment, so the web PR **must regenerate** it
(`REGEN=1` web-gate) or its `openapi-check` fails against this bundle.

### Why the sentence is needed (verified in the backend)

Spec §3.4 and AC-17 tell the user "refresh to check whether it was applied"
after a budget 503 on any write except booking create. That is only honest if
the server really may have written something. Verified on this branch
(`main` @ `953f96e` + spec):

- `backend/internal/budget/budget.go:178-205`: at 90 s the middleware marks
  the request expired, cancels the handler's context and writes the 503. It
  does not roll anything back. Work already finished (a Google/Outlook event
  written, a row inserted) stays.
- `budget.go:10-13`: "The handler goroutine may outlive the response (a
  database call made without a context is not interrupted)". So a write can
  even complete *after* the 503 was sent.
- Only `createPublicBooking` uses `budget.Commit` to make the 503 mean
  "nothing saved" (`handlers_booking.go:397`, contract text at
  `paths/booking.yaml` 503). `syncPersonalCalendar` already says it keeps
  created blockers.

The bundle said what a 503 proves for three operations and was silent for
every other write. Silence would let a client infer "nothing happened". The
sentence makes the contract state the rule the web implements. It is in the
shared response, once, for the same reason PAC-81 put the budget rule there
(see §6, alternative 1).

## 2. Server responses the web relies on (pinned, unchanged)

The web may rely on exactly these facts, and on nothing else about 5xx/413.

### 2.1 The budget 503 and how it is recognised

- **Shape**: status `503`, `Content-Type: application/json`, body
  `{"error":"request timed out"}` plus a trailing newline
  (`budget.WriteExpired`, `budget.go:45-51`). Schema
  `RequestBudgetExpiredError`: `required: [error]`, `error` enum
  `['request timed out']`, `additionalProperties: false`
  (`paths/ops.yaml`). Already generated in the web as
  `RequestBudgetExpiredError` (`src/api/generated/schemas.ts`, zod
  `z.literal("request timed out")`).
- **Recognition rule (binding for the web)**: a response is the budget
  answer **iff** `status === 503` **and** the body parses as JSON **and**
  `RequestBudgetExpiredError.safeParse(body).success`. Never by status alone:
  other JSON 503s exist (`{"error":"Zoom OAuth not configured"}`, Microsoft
  OAuth / step-up / Slack / Notion "not configured", readiness
  `{"error":"not ready"}`) and a proxy may send an HTML 503. Those are not
  this answer.
- **No `Retry-After`** header; nothing else in headers identifies it.
- **What it proves, per operation** (contract text, now complete):
  `createPublicBooking`: no booking saved (only sent before the insert);
  reads (`getPublicBookingSlots`, `getTeamAvailability`,
  `parseNaturalLanguage`, any GET): nothing stored; `syncPersonalCalendar`:
  blockers already created are kept, `last_synced_at` not stamped; **every
  other write: unknown** (new paragraph). The web's "nothing written" set is
  therefore exactly `{createPublicBooking}`.
- **When**: at the 90 s budget, before the 120 s write backstop
  (`lifecycle.go:207`), before nginx's 130 s (`nginx/nginx.conf:66-67`). Any
  operation can return it (pool exhaustion), not only the five that list it.

### 2.2 The 413

- Status `413`, body `{"error":"request body too large"}`, for a request body
  over 1 MiB, before any handler runs: the backend (`bodyCapMiddleware`,
  `backend/api/middleware.go:187-206`) and nginx
  (`location @api_body_too_large`, `nginx.conf:108-116`, JSON with
  `default_type application/json`) send the same body. Stated once in the
  API `info.description` (`scripts/openapi_assemble.py`), not per operation.
- **Recognition rule**: status `413` alone. No other 413 exists in the stack,
  and an operator proxy's own (HTML) 413 means the same thing; the web shows
  "too large" for either and does not need the body.

### 2.3 Gateway errors: a 5xx whose body is not JSON

- Every 5xx the backend writes is the PAC-21 JSON envelope `{"error": ...}`
  with `Content-Type: application/json` (contract-wide rule, enforced by
  `TestPAC21Every5xxInTheContractIsTheJSONEnvelope`). nginx has no
  `error_page` for 502/504: its 502 (backend closed the connection without an
  answer, e.g. a write past the 120 s backstop) and 504 (nothing by 130 s)
  are nginx's default **HTML** pages. Operator proxies' 502/503/504/52x are
  HTML too.
- **Rule (binding for the web)**: a 5xx response is a *gateway error* iff its
  `Content-Type` does not contain `application/json` **or** its body does not
  parse as JSON. A gateway error never proves anything about what the server
  did.
- Other non-JSON 5xx sources, same rule (challenge 2026-11-02): a handler
  **panic** is re-raised past every middleware (`sentryMiddleware` and
  `loggingMiddleware` re-panic; there is no recoverer), so `net/http` closes
  the connection without an answer and the browser gets nginx's 502 HTML —
  after a booking insert that panic means "may have been saved", which the
  rule gives. Under the Vite dev server (Lovable preview, local dev) a
  backend that is down or closes the connection yields the dev proxy's
  `text/plain` 500, also a gateway error.
- A proxy or API gateway may answer a 5xx **with a JSON body of its own**
  (e.g. Kong's `{"message": ...}` 502/504). That is not a gateway error by
  this rule; it is an `ApiHttpError`. For reads and non-booking writes it
  gets today's mapping; for booking create every 5xx other than the budget
  503 is "may have been saved" (§2.4, §3.3), so no false "not made" can
  come from it.

### 2.4 Booking create (`POST /api/book/{slug}`, `createPublicBooking`)

| Answer | Meaning the web shows |
|---|---|
| `201` + `BookingResponse` | saved (body unchanged by PAC-83) |
| `400` / `404` / `409` / `410` / `422` / `429` | today's handling (`PublicBooking.tsx:183-196`), unchanged |
| `500` JSON (`BookingError`) | **outcome unknown** ("may have been saved"): the contract says a 500 does not prove nothing was saved |
| `503` + `RequestBudgetExpiredError` | **not made**: the only answer that proves no booking was saved; the booker may press Book again |
| any other 5xx with a JSON body (`503` that is not the budget body, a proxy's JSON `502`/`504`) | outcome unknown (not the budget answer) |
| gateway error (§2.3), no answer within the client bound, network failure, non-JSON 2xx | outcome unknown |

After the insert is sent the server waits for it with no bound of its own
and answers 201 or 500, possibly after 90 s; past ~120 s only nginx's HTML
502/504 can arrive (spec §2.7). The client bound of 135 s (spec D-1) is
therefore later than every answer the shipped stack can give.

### 2.5 Timeline the client bound rests on

budget 90 s (`budget.Default`) < write backstop 120 s (silent cut) < nginx
130 s (HTML 504) < **client 135 s** < (operator proxy must exceed 130 s,
`docs/operator/proxy.md`). If any of the server numbers changes, this
contract and the client constant change with it.

## 3. Web client error taxonomy (binding for the web PR)

All in `src/api/client.ts` unless noted; `src/lib/api.ts` (`apiFetch`) throws
the same classes.

### 3.1 Bounds (exported constants)

| Constant | Value | Used by |
|---|---|---|
| `CLIENT_REQUEST_BOUND_MS` | `135_000` | every `requestApi` call and every `apiFetch` call (sign-in discovery, password sign-in/register, `/auth/me`, logout) |
| `HEALTH_PROBE_BOUND_MS` | `10_000` | `api.health` and `checkApiAvailability` (the pre-sign-in check) |
| `STILL_WORKING_AFTER_MS` | `8_000` | the "still working" notice on user-started calls (D-3) |

`NETWORK_TIMEOUT_MS` (4 s) and the 2 s bound in `checkApiAvailability` are
removed. No per-operation shorter deadline exists.

How the bounds are applied (binding, challenge 2026-11-02):

- Each bound is armed with `setTimeout` (never `AbortSignal.timeout()`), so
  vitest fake timers (AC-3, AC-4) and Playwright's `page.clock` (§5.3)
  control it; `AbortSignal.timeout` is driven by neither.
- The bound covers the **whole exchange**, headers and body: the timer is
  cleared only after the body has been read and parsed. Today's
  `requestApi` clears it when `fetch` resolves on the headers
  (`client.ts:604-606`), leaving the body read unbounded. A 2xx whose body
  cannot be read or parsed as JSON within the bound is
  `ApiUnreachableError("non-json")` (or `"timeout"`), never a raw
  `SyntaxError`.
- `apiFetch` (`src/lib/api.ts`) throws the same classes as `requestApi`
  and keeps its two special cases: `204` resolves to `undefined`; and every
  error class it throws for an HTTP answer carries `.status` (and `.data`),
  because `AuthContext.refresh` decides "signed out" on `status` 401/403
  (`AuthContext.tsx:63-77`). A non-JSON 2xx other than 204 now throws
  `ApiUnreachableError("non-json")`: today it resolves to `undefined`
  (`api.ts:95`), which `refresh` turns into `null` and so **signs the user
  out** when `/api/auth/me` is answered by the SPA's `index.html` — the
  opposite of spec §6.
- `checkApiAvailability` does not cache a failed result: today the first
  answer is cached for the page (`api.ts:25-29`), so after one failure the
  sign-in "retry" of spec §3.4 could never succeed. A success may stay
  cached; a failure is re-probed on the next call.

### 3.2 Classes

| Class | Thrown when | Notes |
|---|---|---|
| `ApiHttpError(status, data)` | a response with status ≥ 400 whose body is JSON; and a 4xx whose body is not JSON (`data` undefined) | unchanged class and fields |
| `ApiGatewayError(status)` **new** | a response with status ≥ 500 that is a gateway error (§2.3) | never an `ApiHttpError`, so today's "The server had a problem" mapping never sees it |
| `ApiUnreachableError(reason)` | `reason: "network"` fetch rejected; `"timeout"` no answer within the bound; `"non-json"` a 2xx that is not JSON | no longer switches anything; message never mentions preview/demo |
| `ApiRequestCancelledError` | rejection while the page is being left (`pagehide`) | unchanged |

Predicates (exported): `isBudgetExpired(e)` (§2.1 rule on an
`ApiHttpError`), `isRequestTooLarge(e)` (`ApiHttpError` with status 413),
`isGatewayError(e)`, `isAnswerLost(e)` (= `ApiUnreachableError` or
`ApiGatewayError`), plus the existing `isApiHttpError`,
`isApiUnreachableError`.

### 3.3 Classification and messages

`classifyApiFailure(e): "cancelled" | "budget" | "too-large" | "lost" | "http"`
— in that precedence. `apiFailureMessage(e, effect)` with
`effect: "read" | "write" | "booking"` returns `{ message, retry }`:

| kind | read | write | booking (`createPublicBooking`) |
|---|---|---|---|
| cancelled | nothing shown, nothing recorded | same | same |
| budget | "This took too long and was stopped. Try again." `retry: true` | same text + " Refresh to check whether it was applied." `retry: false` | "Your booking was not made because the service was too slow. You can try again." `retry: false` (the booker presses Book) |
| too-large | n/a | "This is too large to send." + what to shorten (caller-supplied) `retry: false` | same |
| lost | "Can't reach Paceday right now. Check your connection and try again." `retry: true` | "Paceday didn't confirm this. It may or may not have been applied — refresh to check before trying again." `retry: false` | "We couldn't confirm your booking. It may have been saved — check your email for a confirmation before booking again." `retry: false` |
| http | today's `apiErrorMessage` mapping (unchanged) | same | **every 5xx** (500, a non-budget JSON 503, a proxy's JSON 502/504) → booking "lost" text; 4xx today's handling |

**The effect is per operation, never derived from the HTTP method**
(challenge 2026-11-02). Several reads are POSTs: `api.freebusy`,
`api.scheduleSuggest`, `api.compressionPreview`, `api.nlpParse`,
`api.llmTest` (a provider probe). Their budget 503 is the read
text with retry (contract: "a read (slot listing, team availability, parse)
stored nothing"); deriving the effect from `POST` would tell the user to
"refresh to check whether it was applied" after a parse. The effect of each
operation is recorded next to its AC-7 sample (§3.5), so a new operation
cannot be added without choosing one.

The English strings are the spec's (§3.4, §3.5) and are exported as
constants so tests and e2e read them from one place. `apiErrorMessage` never
returns "showing preview data" again. Retry for reads is a manual control
only (D-7): nothing retries on its own, booking create is never re-sent by
code.

### 3.4 Removed web interfaces

`isUsingMocks`, `subscribeMockMode`, `setMockMode`, both `withFallback`
helpers (`client.ts`, `manager.ts`), the mock branches of `demoOrReal`
outside a demo, and the "Backend not reachable — showing demo data" /
"Backend unreachable — showing preview data" strings. The session-storage key
`clockwise:mock-mode` is read once at start-up only to be removed (AC-9).

### 3.5 The operation registry AC-7 walks

`src/api/operations.ts` exports:

```ts
export const API_OPERATIONS = {
  api, schedulingLinksApi, teamInvitesApi,
  teams: teamsApi.remote, manager: managerApi.remote,
  standalone: { fetchSsoStepupOptions, fetchOrganizationAI, patchOrganizationAI,
                chooseOrganizationAIChoice, confirmOrganizationAI },
} as const;
```

Why `teamsApi.remote` / `managerApi.remote` and not the objects themselves
(challenge 2026-11-02): on `origin/main` the top level of `teamsApi`
(`teams.ts:487`) and `managerApi` (`manager.ts:616`) is a **synchronous
local-storage store** (`paceday:teams:v1`, `paceday:manager:v1`) that makes
no request; the server calls are under `.remote` (`teamsRemote`,
`managerRemote`). Walking the top level would find one object member and a
dozen methods with nothing to intercept. The store is also **not demo
code**: production pages read it directly — `teamsApi.currentUserEmail()`
(`FindATimeTab.tsx:64`, `ProtectedHoursTab.tsx:664`) and
`managerApi.getProfile()/setProfile()` (`Onboarding.tsx:33`,
`Settings.tsx:812-820`). It stays outside `src/demo/` (§4.2); only the seeds
(`SEED_MEMBERS`, seeded teams) and the mock branches move. Its methods are
listed in `NOT_OPERATIONS` with the reason "local store, no request".
Members of `api` that build a URL and make no request (`authConnectUrl`,
`zoomConnectUrl`) are in `NOT_OPERATIONS` too; `api.health` is excluded from
AC-7 and covered by AC-8 (its failure drives the banner, it has no caller
data to invent). `manager.ts` re-exports `api` (`manager.ts:757`): the
coverage scan dedupes exports by identity, so the re-export is not a second,
unregistered `api`.

AC-7's test (`src/api/noInventedData.test.ts`) iterates
`Object.entries` of every group, so the operation list is derived, not
hand-kept. It needs sample arguments per operation (`OPERATION_SAMPLES`,
keyed `"<group>.<name>"`, each with its `effect`: `read` | `write` |
`booking`); the test fails when an operation has no sample
**and** when a sample names an operation that no longer exists. A second
check scans the module namespaces of `src/api/client.ts`,
`schedulingLinks.ts`, `teams.ts` and `manager.ts`: every exported function,
and every function-valued member (one level down, and through `.remote`) of
every exported object named `api` or `*Api`, must be either registered in
`API_OPERATIONS` or listed in `NOT_OPERATIONS` (pure helpers such as
`apiErrorMessage`, `validateTeamName`, the local-store methods above); an
unlisted one fails the test. `requestApi` itself is in `NOT_OPERATIONS`
(it is the transport the operations share; AC-3 tests it directly).
For every operation and each of: fetch rejection, no answer within
`CLIENT_REQUEST_BOUND_MS` (fake timers), non-JSON 2xx — the call rejects with
`ApiUnreachableError`, no mock value is returned, and a second call reaches
the fake server.

## 4. The demo build flag (owner OQ-1 (c))

### 4.1 The flag

- Name: `__PACEDAY_DEMO_BUILD__` (boolean), declared in `src/vite-env.d.ts`
  (`declare const __PACEDAY_DEMO_BUILD__: boolean;`).
- Defined in `vite.config.ts` `define` as
  `JSON.stringify(mode !== "production")`, and in `vitest.config.ts` as
  `true` (unit tests run the non-production behaviour).
- Values: `vite` dev server (Lovable preview, local dev) → `true`;
  `vite build --mode development` (`npm run build:dev`) → `true`;
  `vite build` / `npm run build` (the shipped image,
  `docker/frontend.dockerfile`, the e2e stack) → `false`.
- Evidence and its limit (challenge 2026-11-02). That the Lovable preview is
  the dev server in mode `development` is supported by the repo: the
  `vite.config.ts` server block (`host "::"`, port 8080) and
  `componentTagger()` enabled only when `mode === "development"` are
  Lovable's preview conventions. What a Lovable **publish** runs is not
  visible from either repo (third-party write-ups say `npm run build`, no
  first-party source found). It does not matter for the owner's rule: the
  only Paceday production artifact is the image built by
  `docker/frontend.dockerfile:11` (`npm run build`), which AC-30 checks; a
  Lovable-hosted publish is not a Paceday deployment (it has no `/api`).
- In vitest the flag is **a runtime global, not a compile-time
  replacement** (verified with vitest 4.1.12: a `define` in
  `vitest.config.ts` is read through `globalThis`, and
  `vi.stubGlobal("__PACEDAY_DEMO_BUILD__", false)` flips it for code
  imported before the stub). So production-path unit tests are possible and
  required (spec AC-15d): they stub the flag to `false` and must not rely on
  a module-level copy of it (§4.2 already forbids one).
- No runtime input changes it: no env var read at runtime, no
  `window.__…__` injected by the fileserver, no URL parameter, no storage key.

### 4.2 Code layout rule

- Everything demo-only lives under `src/demo/`: the seeded data now in
  `client.ts` (`mockState`, `MOCK_ROOMS`, `MOCK_DIRECTORY`, `_mockHelpers`),
  the seeded links in `schedulingLinks.ts`, the teams mocks in `teams.ts`,
  `SEED_MEMBERS` in `manager.ts`, `src/lib/demoData.ts`, `mockZoomConnect`,
  `loginDemo`, the demo label and the entry control.
- Outside `src/demo/`, demo code is reached **only** by a dynamic
  `import("@/demo/…")` in a branch whose condition is the literal flag:
  `if (__PACEDAY_DEMO_BUILD__) { … }`, an early
  `if (!__PACEDAY_DEMO_BUILD__) return …;`, `__PACEDAY_DEMO_BUILD__ && …`
  or `__PACEDAY_DEMO_BUILD__ ? lazy(() => import(…)) : null` (for a React
  component). Never through a wrapper function or a constant holding the
  flag. Verified on the repo's toolchain (Vite 8.1.6, which bundles with
  Rolldown 1.1.5, not Rollup): each literal form above removes the branch
  and the demo chunk is **not emitted** in mode `production`; a wrapper
  (`const isDemoBuild = () => __PACEDAY_DEMO_BUILD__`) **leaks the whole
  chunk** into the production output. The bundle check (§4.3) is what
  catches a leak; the form rule is how not to cause one.
- Strings that are demo-only but live in shared components (the
  "Zoom connected (demo)" toast in `ConferencingSection.tsx`, the
  `paceday:demo` key now exported as `DEMO_SESSION_KEY` from `client.ts:91`,
  `isDemoSession`) move under `src/demo/` or sit inside a literal-flag
  branch; otherwise they are markers in the production bundle and §4.3
  fails.
- Lint: `eslint.config.js` `no-restricted-imports` forbids static imports of
  `@/demo/*` and `src/demo/*` from any file outside `src/demo/`.
- `src/demo/index.ts` exports `DEMO_BUNDLE_MARKER = "paceday-demo-bundle-v1"`,
  and the demo label renders it (`data-demo-bundle={DEMO_BUNDLE_MARKER}`).
  It must be **used in rendered output**, not merely exported or imported:
  the development build is minified and tree-shaken like the production one,
  so an unused export disappears from it and the positive control fails.
- The demo data path is keyed on the demo session (`paceday:demo`) alone
  (spec D-9); a production build neither reads nor sets that key.

### 4.3 The bundle check (AC-30)

`scripts/check-demo-absent.mjs`, run by a new web Makefile target
`demo-check`, added to `make verify` after `test`:

1. `vite build --outDir <tmp>/prod --emptyOutDir` (mode production) and
   `vite build --mode development --outDir <tmp>/dev --emptyOutDir`
   (`--emptyOutDir` because the directories are outside the project root,
   where Vite otherwise refuses to clear them).
2. Search every file under `<tmp>/prod` for these markers; any hit fails:
   `paceday-demo-bundle-v1`, `paceday:demo`, `data-testid="demo-entry"` /
   `"demo-entry"`, `"demo-label"`, `intro-chat`, `product-deepdive`,
   `Sarah Chen`, `Miguel Alvarez`, `Priya Nair`, `Zoom connected (demo)`.
3. Positive control: **every** marker of step 2 must be found under
   `<tmp>/dev`; any marker absent there → fail. Checking only the sentinel
   would let a stale marker (renamed seed, reworded toast) pass vacuously in
   step 2 forever.

`clockwise:mock-mode` is deliberately **not** a marker: the production build
must contain it to clear a leftover flag (AC-9).

### 4.4 The entry control

- One control, `data-testid="demo-entry"`, label "Explore a demo — made-up
  data", rendered on the sign-in dialog's **initial view** only, and only in
  a demo build.
- The dialog's error view (any AC-14 failure) contains no `demo-entry`
  element and no text mentioning a demo (AC-15c). No code path reachable from
  an error sets `paceday:demo`.

## 5. Test ids and the e2e side (clockwise-like)

### 5.1 Test ids the web renders (binding)

| Test id | Element | Where |
|---|---|---|
| `demo-entry` | the demo entry control | sign-in initial view, demo builds only |
| `demo-label` | the non-dismissible demo label | every signed-in page during a demo; demo builds only |
| `server-unreachable-banner` | the persistent "can't reach Paceday" banner (replaces `MockBanner`'s unreachable state) | Dashboard, Settings, Audit, when the health probe fails |
| `server-unreachable-retry` | its retry button | inside the banner |
| `api-error` | an inline failure message; attributes `data-error-kind` (`budget`, `too-large`, `lost`, `http`) and `data-effect` (`read`, `write`, `booking`) | where the action happened |
| `api-error-retry` | the manual retry control | inside `api-error`, only when `retry: true` (§3.3) |
| `still-working` | the "still working, may take up to about a minute and a half" notice | on a user-started call after 8 s |
| `ai-progress` | the natural-language "asking the AI, may take up to a minute" message | from submit until answer |

Retired: `demo-banner` and `mock-banner` (`e2e/TESTIDS-REQUIRED.md` §5; the
latter was never rendered). `e2e/TESTIDS-REQUIRED.md` §5 is updated in the
same clockwise-like PR.

### 5.2 `assertNotDemo` (AC-28)

`e2e/fixtures.ts` `assertNotDemo(page)` asserts count 0 for
`getByTestId('demo-label')`, `getByTestId('server-unreachable-banner')`, the
old sentence `/Backend not reachable — showing demo data/i` (kept so the
guard works against the old and the new web while the two merges are not
simultaneous, spec §7), the manager seed names, and (challenge
2026-11-02) `[data-testid="api-error"][data-error-kind="lost"]`: with the
fallback gone, an unreachable backend no longer shows invented data but an
inline "can't reach" error, and a test that only asserts something is
*absent* (an empty list, no conflict) would otherwise pass against a dead
backend. Tests that provoke a lost answer on purpose (AC-10) do not call the
guard while it is on screen. A guard self-test
(`e2e/tests/guard-self-test.spec.ts`) uses `page.setContent` with each marker
in turn (no web needed) and expects `assertNotDemo` to fail for each, and to
pass on a page with none. AC-30's e2e half: on the sign-in screen served by
the e2e stack (the production image), `getByTestId('demo-entry')` has count 0.

### 5.3 OQ-4: making the browser wait or fail (resolved)

Mechanism: Playwright request interception in the test, against the real
compose stack. No test hook in the server, nginx or fake-google.

What this does and does not test (challenge 2026-11-02): holding an answer
in the browser proves the web **does not give up early** (the 4 s abort is
gone) and renders a late answer correctly. It cannot prove the 135 s bound
itself in real time (a test would wait over two minutes, past the suite's
60 s `timeout`, `e2e/playwright.config.ts:26`); the bound's value is AC-3's,
with vitest fake timers. Two rules make the e2e scenarios deterministic and
short:

- The route handler holds the answer on a **test-controlled promise**
  (released by the test), not on a fixed `setTimeout` in the handler, so the
  test decides when the answer arrives after asserting the waiting state.
- The page's own clock may be advanced instead of waiting in real time:
  `page.clock.install()` before `page.goto`, then
  `page.clock.fastForward(9_000)` to show `still-working` and
  `page.clock.fastForward(131_000)` (past nginx's 130 s, below the 135 s
  bound) to show the page is still waiting, then release the answer. This
  works only because §3.1 requires the bounds to be `setTimeout`s. The
  scenario's real duration is then seconds; the ACs' "after 20 s / 30 s"
  are page time. A real-time hold is also acceptable if it fits the test's
  raised `test.setTimeout`.
- `route.fetch()` sends the real request from Playwright, not from the
  page: `page.on('request')` still sees the browser's request exactly once,
  which is what AC-24 counts.

- **AC-5** (slots after 20 s): `page.route` on `GET **/api/book/<slug>/slots*`
  → `const res = await route.fetch()` (the real server answers), hold until
  the test releases (≥ 20 s of page time), `route.fulfill({ response: res })`. Assert during the wait: no
  "no available times", `still-working` visible after 8 s; after: the real
  slots. `test.setTimeout` raised (default is 60 s).
- **AC-10** (link info fails once): `page.route` on `GET **/api/book/<slug>`
  (method checked — the same path takes the booking POST) →
  `route.abort('failed')` the first time, `route.continue()` after. Assert
  `api-error[data-error-kind=lost]` and no "Link not found"; press
  `api-error-retry` → the real link; reload → the real link.
- **AC-24** (201 after 30 s): `page.route` on `POST **/api/book/<slug>` →
  `route.fetch()` sends the booking once, hold (≥ 30 s of page time),
  fulfill with the real 201. Count booking POSTs seen by `page.on('request')` = 1, then
  `e2e/db.ts` `queryOne` on the bookings table = exactly one row for the link.

**Gap, stated**: the e2e ACs exercise the browser receiving real answers
late or not at all; they do not make the *server* slow. Server-side slowness
(budget 503, per-host 15 s bounds) is covered by PAC-81 Part A's backend tests,
and the web's handling of the 503/413/502/504 answers by the `[unit]` ACs.

## 6. Alternatives rejected

1. **A "nothing written / may have been written" sentence on each write's
   503.** About 60 write operations, none of which lists a 503 today; adding
   a 503 entry to each would be ~60 identical edits that drift. PAC-81 chose
   one shared statement for the same reason. The shared response already
   says "what was done is stated per operation"; the new paragraph closes
   the silent default instead.
2. **Recognising the budget answer by a header** (e.g. `X-Paceday-Budget:
   expired`). The body is already pinned by an enum and generated in the
   web; a header is new server surface, can be stripped by a proxy, and two
   signals for one fact can disagree.
3. **Recognising the budget answer by status 503 alone.** Wrong for the other
   JSON 503s and for an operator proxy's HTML 503 — the booker would be told
   "not made" for a booking that may exist (spec C-3).
4. **A 413 response entry on every operation with a body.** Same argument as
   1; and the web needs only the status.
5. **A runtime demo switch** (an env var injected into `window` by
   `docker/fileserver`, or a URL flag). The production bundle would still
   carry the demo data and the code that shows it, which the owner excluded,
   and "no demo in production" could only be tested by configuration, not by
   the shipped files.
6. **`import.meta.env.MODE` checks scattered through the code instead of one
   `define` flag.** Works for dead-code elimination, but gives no single name
   to lint, grep or reason about, and `MODE` is also used for Sentry's
   environment; a dedicated flag says what it is for.
7. **OQ-4 via a fake-google latency control.** Slot listing and booking read
   each host's calendar with a 15 s per-host bound, and since PAC-72 D-6 an
   unread host is busy: a provider delay long enough to test (20 s) yields
   "no slots", not a slow answer; a delay under 15 s tests less than the
   spec asks. It also cannot produce AC-10's network-level failure.
8. **OQ-4 via an nginx test config or a delaying proxy container.** The e2e
   stack mounts the production `nginx.conf` "unmodified — if this file
   breaks, the suite must break" (`e2e/docker-compose.e2e.yml`); a test
   variant weakens that, and a proxy container adds a moving part for what
   the browser layer does exactly.
9. **Moving AC-5/AC-10/AC-24 to `[unit]` only.** Rejected because AC-24's
   "exactly one booking on the server" needs the real database, and
   interception gives real-stack coverage at no product cost.

## 7. Consumers of the contract change

| Consumer | Breaks? | Detail |
|---|---|---|
| `backend/api/gen` | no | byte-identical (`openapi_gen_go.sh --check` OK) |
| smart-calendar-flow `src/api/generated/types.ts` | drift until regenerated | JSDoc of `RequestBudgetExpired` gains the paragraph; the web PR regenerates (`REGEN=1`). No type changes shape. |
| `mcp/` | no | own client, no generated types from the bundle |
| e2e | no | no assertion on response descriptions |

## 8. Merge order

1. This branch (`factory/pac-83-spec`: spec rev 2 + this contract) after the
   contract-challenger accepts.
   **Together with it** (challenge 2026-11-02): a web PR that only
   regenerates `src/api/generated` from this bundle (`make openapi
   CONTRACT=…`, the PAC-42/PAC-67 "web-types" pattern). The new paragraph is
   emitted into `types.ts` as JSDoc (the existing paragraph already is,
   `types.ts:6580`), and both gates compare against the sibling checkout:
   the web's `make openapi-check` against `../clockwise-like`, and the api's
   `make openapi-check` web half against `../smart-calendar-flow`. Merging
   this branch first and regenerating later would turn every web PR's
   `make verify` red in between; merging the types first would turn the
   api's red. Both merges go in the same window.
2. The clockwise-like e2e PR (guard + self-test + TESTIDS + AC-5/10/24/30
   scenarios + `docs/operator/proxy.md` sentence) and the web PR, in either
   order: the guard accepts both webs (§5.2). The AC-5/10/24/30 scenarios
   pass only against the new web, so they land with or after it (or are
   marked `test.fixme` naming PAC-83 until the web image is published).

## Challenge — 2026-11-02

Contract-challenger, against `factory/pac-83-spec` @ `47af532`,
clockwise-like `origin/main` @ `953f96e`, smart-calendar-flow `origin/main`
@ `8966115` (read with `git show`). **Verdict: accept-with-changes**, with
the changes applied above as revision 1.1 (contract prose, manifest, spec).
The OpenAPI fragments are unchanged, so `contractHash` stays `c477dbe3…`
(recomputed from the committed bundle).

### Responses the client cannot handle (as the contract stood)

1. `createPublicBooking` answered by an API gateway in front of nginx with
   `502 Content-Type: application/json {"message":"An invalid response was received from the upstream server"}`
   (Kong's default). This is not a gateway error by §2.3 (it is JSON), not the
   budget body, and not a 500. §3.3's booking `http` cell named only "500 → lost
   text", so a JSON 502/504 fell to an unspecified path. Fix applied: §2.4 /
   §3.3 now say every 5xx on booking create other than the budget 503 is "may have
   been saved". Spec AC-22b widened.
2. `GET /api/auth/me` answered `200 Content-Type: text/html` (`index.html`,
   a misrouted proxy): `apiFetch` returns `undefined` (`src/lib/api.ts:95`),
   `AuthContext.refresh` maps it to `null` (`AuthContext.tsx:57`) and signs
   the user out, which contradicts spec §6. The contract said `apiFetch`
   "throws the same classes" without saying a non-JSON 2xx is one of them.
   Fix applied: §3.1 pins `ApiUnreachableError("non-json")` for it, leaves
   204 unchanged, and keeps `.status` on HTTP errors.
3. Any 2xx whose headers arrive within the bound and whose body then stalls:
   `requestApi` clears the timer when `fetch` resolves on headers
   (`client.ts:604-606`), so `res.json()` waits forever. Spec §3.1's promise
   that "a dead connection does not spin forever" is false for the body. A
   truncated JSON body throws a bare `SyntaxError` that no predicate
   classifies. Fix applied: §3.1 makes the bound cover the body; an
   unreadable 2xx body is `ApiUnreachableError`.
4. Budget 503 on `POST /api/nlp/parse` (a read that is a POST): with the
   effect left implicit, an implementation keyed on the method shows "Refresh
   to check whether it was applied" after a parse. Fix applied: §3.3 makes the
   effect per operation, recorded with the AC-7 sample, and lists the POST reads.

### Interfaces that could not be built as written

1. **AC-7 registry**: `API_OPERATIONS = { …, teamsApi, managerApi }` walks
   the local-storage stores (`teams.ts:487`, `manager.ts:616`: `list()`,
   `getProfile()`, … — no request) and finds the real calls only under
   `.remote`. Fix applied: §3.5 registers `teamsApi.remote` and
   `managerApi.remote`; lists store methods, URL builders and `requestApi`
   in `NOT_OPERATIONS`; moves `api.health` to AC-8; dedupes the re-export
   (`manager.ts:757`).
2. **Demo layout**: §4.2 moved "the teams mocks" and the manager store
   under `src/demo/`, but production pages read the stores directly
   (`FindATimeTab.tsx:64`, `ProtectedHoursTab.tsx:664`, `Onboarding.tsx:33`,
   `Settings.tsx:812-820`). A static import is lint-forbidden and a dynamic
   one would make these synchronous reads async. Fix applied: §3.5/§4.2 keep
   the store; seeds and mock branches move.
3. **Bundle check positive control** looked only for the sentinel. A
   renamed seed name or reworded toast leaves a marker that is never present
   anywhere, so step 2 passes vacuously for it. A sentinel that is only
   exported is also tree-shaken out of the (minified) development build. Fix
   applied: §4.3 requires every marker to appear in the dev build; §4.2 has
   the sentinel rendered.
4. **Guard forms**: "inside `if (…)` with the literal flag" excluded the
   forms a React component needs (`flag ? lazy(…) : null`, `flag && …`).
   Measured on Vite 8.1.6 / Rolldown 1.1.5 in a scratch project: `if`,
   early-return, `&&`, ternary and `import.meta.env.MODE !==
   "production"` all drop the dynamic chunk in production. A wrapper
   function emits the chunk with the seed names in it. §4.2 now lists the
   allowed forms with that evidence.
5. **Production path untested in unit tests**: vitest defines the flag
   `true`, and only the bundle grep and one e2e assertion ran the `false`
   path. Measured: under vitest 4.1.12 a `define` is a `globalThis` value
   and `vi.stubGlobal(…, false)` flips it. Fix applied: §4.1 + spec AC-15d.
6. **e2e timing (OQ-4)**: "wait 20 s / 30 s in the route handler" proves
   the 4 s abort is gone, never the 135 s bound (that would take two minutes
   of real time, over the 60 s test timeout). Fix applied: §5.3 states the
   limit, holds the answer on a test-controlled promise, and allows
   `page.clock.fastForward` to cross 8 s and 131 s of page time. §3.1
   requires bounds armed with `setTimeout`, not `AbortSignal.timeout`,
   because neither vitest's fake timers nor `page.clock` drive the latter.
7. **Sign-in retry could never succeed**: `checkApiAvailability` caches its
   first result for the page (`src/lib/api.ts:25-29`). Fix applied: §3.1
   stops caching a failure.

### Consumers affected (missed)

- smart-calendar-flow `docs/lovable-prompts/10-qa-hardening.md:35` ("Only
  an unreachable backend may use the existing preview fallback") and
  `.lovable/plan.md:4` ("graceful mock-data fallback so the Lovable preview
  is fully interactive") also tell the Lovable agent to keep the
  fallback. Spec §2.6/AC-29 named neither. Added to the spec and to the
  manifest's allowed paths.
- e2e `assertNotDemo`: once the fallback is gone, an unreachable backend
  shows an inline "can't reach" error instead of invented data, and a test
  that asserts only an absence would pass against it. §5.2 adds the `lost`
  `api-error` to the guard.

### Merge order (blocking for the merge, not the design)

The description paragraph lands in the web's `types.ts` JSDoc
(`types.ts:6580` carries its sibling paragraph today). Both repos'
`openapi-check` compare against the sibling checkout's main, so "contract
first, web regenerates later" makes the web gate red in between. §8 now
requires a types-only web PR merged in the same window.

### Contract/implementation divergences

None in the server claims. Checked:

- **Booking budget 503 = not saved**: `budget.Commit` (`budget.go:73-85`)
  returns false once the middleware has set `expired`. `ConfirmBooking`
  acquires the insert's connection under the budget (`booking.go:389-394`;
  a pool wait ends in `ErrExpired`), commits (`:396`), and only then sends
  the insert on `context.WithoutCancel` (`:400`). The handler writes the
  budget 503 only for an `ErrExpired` raised before commit
  (`handlers_booking.go:371`, `:396`). After commit the middleware waits
  (`budget.go:200-204`) and the answer is 201 or 500. Before commit the
  only thing written is, at most, a host's refreshed OAuth token, never a
  booking.
- **Ladder**: `budget.Default = 90 * time.Second` (`budget.go:32`);
  `WriteTimeout 120 s`, `ReadTimeout 30 s` (`lifecycle.go:207`; the fileserver
  has the same, `docker/fileserver/main.go:40-43`); nginx
  `proxy_read_timeout 130s` (`nginx.conf:66-67`). The upstreams have a
  single server each (`paceday-render.sh:151-156`), so nginx neither
  retries nor answers "no live upstreams" 502. Measured with Go 1.25.5:
  `ReadTimeout` does **not** cancel a handler's context after the request
  body is read (a 4 s handler under a 2 s ReadTimeout finished normally),
  so the 30 s does not cut long requests short.
- **Long operations**: NL parse (2 × 30 s AI) + suggest, team
  availability, collective slots, personal sync and focus runs all run
  under the 90 s budget and end in the budget 503 at worst. Only two
  answers can come after 90 s: a committed booking (201/500) and the
  step-up callback (`handlers_sso_stepup.go:662`), which is a browser
  navigation answered by redirect, not a `fetch`. Nothing a `fetch` waits
  for runs past 130 s; nginx's 504 answers it first.
- **413**: backend `bodyCapMiddleware` (`middleware.go:191-219`) and nginx
  `@api_body_too_large` (`nginx.conf`, `default_type application/json`,
  body `{"error":"request body too large"}\n`) agree. An operator proxy's or
  Cloudflare's HTML 413 still has status 413, so it shows "too large".
  Recognising by status alone is correct.
- **Other JSON 503s** (Zoom/Microsoft/step-up/Slack/Notion "not
  configured", readiness `not ready`) all fail `RequestBudgetExpiredError`
  (enum `request timed out`), so body recognition cannot misfire.

### Verified clean

- `contractHash`: `git show 47af532:contracts/openapi/openapi.yaml |
  sha256sum` = `c477dbe3b05ddc81ec73698ef566ba511c090550665e8fee3768d4f954259346`.
  The challenge changes no fragment, so no regeneration was needed.
- Manifest conventions match PAC-72 / PAC-81 Part A (featureId, hash and
  `contractHashOf`, repositories with allowed/forbidden paths, gates,
  acceptanceTests, consumersAffected, mergeConditions, rollback).
  `backend/api/gen` is unaffected (description only), and so is MIGRATION.md
  coverage (no operation added or changed).
- Alternatives 2 (header), 3 (status alone), 5 (runtime switch) and 7–8
  (fake-google / nginx test hooks) are rejected for real reasons. I could
  not construct a case where status-alone recognition of 413 misleads.
- The e2e stack builds the web from `docker/frontend.dockerfile`
  (`e2e/docker-compose.e2e.yml:212-214`, `npm run build`, mode
  `production`), so AC-30's e2e half observes a production build.
- MCP (`mcp/client.go`) has its own client and is unaffected.
