# Contract: PAC-83 (web client timeouts, no invented data after failures)

- **Linear**: PAC-83, https://linear.app/paceday/issue/PAC-83/web-client-4-second-abort-falls-back-to-preview-data-on-slow-but
- **Spec**: `docs/specs/PAC-83.md`, revision 2 (challenge 2026-11-01
  accept-with-changes at `c14a968`; revision 2 applies the owner's answer to
  OQ-1, 2026-11-01, and routes OQ-4 to this contract).
- **Stage**: contract-author, revision 1, 2026-11-01. Next: contract-challenger.
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

### 2.4 Booking create (`POST /api/book/{slug}`, `createPublicBooking`)

| Answer | Meaning the web shows |
|---|---|
| `201` + `BookingResponse` | saved (body unchanged by PAC-83) |
| `400` / `404` / `409` / `410` / `422` / `429` | today's handling (`PublicBooking.tsx:183-196`), unchanged |
| `500` JSON (`BookingError`) | **outcome unknown** ("may have been saved"): the contract says a 500 does not prove nothing was saved |
| `503` + `RequestBudgetExpiredError` | **not made**: the only answer that proves no booking was saved; the booker may press Book again |
| `503` with any other JSON body | outcome unknown (not the budget answer) |
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
| http | today's `apiErrorMessage` mapping (unchanged), except a booking 500 → the booking "lost" text | same | 500 → booking "lost" text; 4xx today's handling |

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
  api, schedulingLinksApi, teamsApi, teamInvitesApi, managerApi,
  standalone: { fetchSsoStepupOptions, fetchOrganizationAI, patchOrganizationAI,
                chooseOrganizationAIChoice, confirmOrganizationAI },
} as const;
```

AC-7's test (`src/api/noInventedData.test.ts`) iterates
`Object.entries` of every group, so the operation list is derived, not
hand-kept. It needs sample arguments per operation (`OPERATION_SAMPLES`,
keyed `"<group>.<name>"`); the test fails when an operation has no sample
**and** when a sample names an operation that no longer exists. A second
check scans the module namespaces of `src/api/client.ts`,
`schedulingLinks.ts`, `teams.ts` and `manager.ts`: every exported function
and every exported object named `api` or `*Api` must be either registered in
`API_OPERATIONS` or listed in `NOT_OPERATIONS` (pure helpers such as
`apiErrorMessage`, `validateTeamName`); an unlisted export fails the test.
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
  `docker/frontend.dockerfile`, the e2e stack, a Lovable publish) → `false`.
- No runtime input changes it: no env var read at runtime, no
  `window.__…__` injected by the fileserver, no URL parameter, no storage key.

### 4.2 Code layout rule

- Everything demo-only lives under `src/demo/`: the seeded data now in
  `client.ts` (`mockState`, `MOCK_ROOMS`, `MOCK_DIRECTORY`, `_mockHelpers`),
  the seeded links in `schedulingLinks.ts`, the teams mocks in `teams.ts`,
  `SEED_MEMBERS` in `manager.ts`, `src/lib/demoData.ts`, `mockZoomConnect`,
  `loginDemo`, the demo label and the entry control.
- Outside `src/demo/`, demo code is reached **only** by a dynamic
  `import("@/demo/…")` inside `if (__PACEDAY_DEMO_BUILD__) { … }` with the
  literal flag in the condition (not through a wrapper function), so the
  production build eliminates the branch and never emits the chunk.
- Lint: `eslint.config.js` `no-restricted-imports` forbids static imports of
  `@/demo/*` and `src/demo/*` from any file outside `src/demo/`.
- `src/demo/index.ts` exports `DEMO_BUNDLE_MARKER = "paceday-demo-bundle-v1"`,
  referenced by the demo entry so the marker is in every demo chunk.
- The demo data path is keyed on the demo session (`paceday:demo`) alone
  (spec D-9); a production build neither reads nor sets that key.

### 4.3 The bundle check (AC-30)

`scripts/check-demo-absent.mjs`, run by a new web Makefile target
`demo-check`, added to `make verify` after `test`:

1. `vite build --outDir <tmp>/prod` (mode production) and
   `vite build --mode development --outDir <tmp>/dev`.
2. Search every file under `<tmp>/prod` for these markers; any hit fails:
   `paceday-demo-bundle-v1`, `paceday:demo`, `data-testid="demo-entry"` /
   `"demo-entry"`, `"demo-label"`, `intro-chat`, `product-deepdive`,
   `Sarah Chen`, `Miguel Alvarez`, `Priya Nair`, `Zoom connected (demo)`.
3. Positive control: `paceday-demo-bundle-v1` must be found under
   `<tmp>/dev`; absent → fail (the check would be vacuous).

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
simultaneous, spec §7), and the manager seed names. A guard self-test
(`e2e/tests/guard-self-test.spec.ts`) uses `page.setContent` with each marker
in turn (no web needed) and expects `assertNotDemo` to fail for each, and to
pass on a page with none. AC-30's e2e half: on the sign-in screen served by
the e2e stack (the production image), `getByTestId('demo-entry')` has count 0.

### 5.3 OQ-4: making the browser wait or fail (resolved)

Mechanism: Playwright request interception in the test, against the real
compose stack. No test hook in the server, nginx or fake-google.

- **AC-5** (slots after 20 s): `page.route` on `GET **/api/book/<slug>/slots*`
  → `const res = await route.fetch()` (the real server answers), wait 20 s,
  `route.fulfill({ response: res })`. Assert during the wait: no
  "no available times", `still-working` visible after 8 s; after: the real
  slots. `test.setTimeout` raised (default is 60 s).
- **AC-10** (link info fails once): `page.route` on `GET **/api/book/<slug>`
  (method checked — the same path takes the booking POST) →
  `route.abort('failed')` the first time, `route.continue()` after. Assert
  `api-error[data-error-kind=lost]` and no "Link not found"; press
  `api-error-retry` → the real link; reload → the real link.
- **AC-24** (201 after 30 s): `page.route` on `POST **/api/book/<slug>` →
  `route.fetch()` sends the booking once, wait 30 s, fulfill with the real
  201. Count booking POSTs seen by `page.on('request')` = 1, then
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
2. The clockwise-like e2e PR (guard + self-test + TESTIDS + AC-5/10/24/30
   scenarios + `docs/operator/proxy.md` sentence) and the web PR, in either
   order: the guard accepts both webs (§5.2). The AC-5/10/24/30 scenarios
   pass only against the new web, so they land with or after it (or are
   marked `test.fixme` naming PAC-83 until the web image is published).
3. The web PR regenerates `src/api/generated` from this bundle.
