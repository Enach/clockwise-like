# Spec — PAC-97: Sign-in by email sends Google Workspace domains with a Microsoft tenant to Microsoft

- **Linear**: https://linear.app/paceday/issue/PAC-97/sign-in-by-email-sends-google-workspace-domains-with-a-microsoft
- **Status**: accepted with changes (spec-challenger, 2026-11-13; see "Challenge log" at the end)
- **Author**: spec-author
- **Priority**: High (owner-reported, 2026-11-13)
- **Inherited scope**:
  - `contracts/openapi/paths/auth.yaml` carries no `x-uncertain` marker on the detection operation (`auth.yaml:833-880`) or its answer schema (`auth.yaml:2693-2720`).
  - API-062 (`docs/factory/api-audit.md:320`, partly fixed by PAC-39): the detection rate limit is per process, so 20/min is 20/min x replicas. Not fixed here; see §5 and OQ-3.
  - API-010 (`api-audit.md:268`) is still marked "Confirmed", but the web already follows the SSO answer's redirect path (`AuthDialog.tsx:130-134` on `smart-calendar-flow` `origin/main`). The audit row is stale; the implementer updates it to "Fixed (PAC-35)" while touching this endpoint.
  - API-092 (`api-audit.md:350`): every detection outcome added here gets a Go assertion.
- **Verified against**: `clockwise-like` `origin/main` at `bc7b144`; `smart-calendar-flow` `origin/main` at `54397a5` (read with `git show`, no checkout). No code was run, and no DNS or HTTP lookup was made against gorgias.com while writing this spec. The gorgias.com facts below come from the issue.
- **Challenge, 2026-11-13**: the challenger re-read every citation against the same SHAs and ran public MX lookups (gorgias.com, google.com, microsoft.com, github.com, unilever.com, stripe.com, shopify.com, accenture.com, mimecast.com, proofpoint.com, outlook.com) and an NS/SOA lookup of `mx.microsoft`. Results are in the Challenge log; the body below already carries the fixes.

## 1. Problem

A person who types their work email into Paceday's sign-in dialog is sent
straight to an identity provider chosen by Paceday. For gorgias.com, a company
whose mail and accounts are on Google Workspace, Paceday chooses Microsoft. The
user lands on a Microsoft sign-in page for an account that does not exist or
that they cannot use. Nothing on that page tells them to go back and pick
Google. The only workaround is to ignore the email field and press "Continue
with Google", which a user has no reason to know.

The cause is a weak signal used as if it were strong. Paceday asks Microsoft
whether an Entra tenant exists for the domain. Many Google Workspace companies
have one anyway: from an Azure subscription, an Office trial, or federation
through an identity broker (gorgias.com federates through Rippling). Wherever
that is true, Paceday sends the whole company to the wrong provider.

The cost: users of affected companies cannot sign in by email at all, and the
first impression of the product is a dead end at someone else's login page.

## 2. Current behaviour

### 2.1 The decision (`backend/auth/sso_detect.go`, `origin/main`)

`DetectAuthProvider` (`sso_detect.go:26-57`) decides in this order:

1. No domain extractable from the input → `generic` (`:27-30`).
   `domain.ExtractDomain` splits on the first `@`, lowercases and trims
   (`backend/domain/domain.go:16-22`).
2. A consumer domain on the fixed list → `generic` (`sso_detect.go:32-35`).
   The list is `backend/domain/domain.go:5-14` (gmail.com, googlemail.com,
   outlook.com, hotmail.com, live.com, msn.com, yahoo.*, icloud.com, me.com,
   mac.com, protonmail.com, proton.me, aol.com, mail.com, gmx.com, yandex.*,
   zoho.com, fastmail.com); `IsGenericDomain` is an exact map lookup (`:24-26`).
3. An SSO provider row for the domain → `sso` with the provider's name and the
   path `/api/auth/sso/<domain>` (`sso_detect.go:37-48`). A database error is
   returned to the handler (`:39-41`), which answers 500.
4. **Microsoft tenant heuristic** (`sso_detect.go:50-54`): `isMicrosoftTenant`
   is called and its error discarded (`isMicrosoft, _ :=`). True → `microsoft`.
5. Anything else → `google` (`sso_detect.go:56`).

There is no "unknown" outcome for a company domain. Every non-consumer domain
without SSO is answered `google` or `microsoft`.

### 2.2 The Microsoft heuristic and its cache

`isMicrosoftTenant` (`sso_detect.go:73-115`):

- GETs `https://login.microsoftonline.com/<domain>/.well-known/openid-configuration`
  (`:81-82`) with a 2 s context timeout (`:83`) through
  `outbound.IdentityClient()` (`:90`; a 10 s client timeout,
  `backend/internal/outbound/outbound.go:88`, asserted in
  `outbound_test.go:23`). The domain is interpolated into the URL path
  unescaped.
- Non-200 → false, cached (`:97-100`). Transport error → false, cached, error
  returned (`:91-93`) and then discarded by the caller, so **a timeout or a
  Microsoft outage is cached for an hour as "not Microsoft" and answered
  `google`**.
- 200 → decodes `issuer`; a tenant is any non-empty issuer not containing
  `/common/` or `/organizations/` (`:102-114`). A JSON decode error returns
  false **uncached** (`:105-107`).
- The cache is a package-level `map[string]tenantEntry` behind an RWMutex
  (`:61-71`), entries expire after one hour (`cacheTenant`, `:117-121`), and
  **nothing ever deletes an entry**: every distinct domain ever typed stays in
  memory until restart. The rate limit (§2.3) bounds the growth rate per IP,
  not the total.

For gorgias.com the endpoint answers 200 with a tenant issuer (issue), so the
answer is `microsoft`.

### 2.3 The handler and its rate limit

- Route: `r.Post("/detect", ssoh.detect)` under `/api/auth`
  (`backend/api/routes.go:63`), public.
- Handler `ssoHandlers.detect` (`backend/api/handlers_sso.go:30-52`): rate
  check first (`:31-34`, 429 "rate limit exceeded"), then decodes `{email}`
  (`:36-42`, 400 "email required" when missing or empty), calls
  `auth.DetectAuthProvider` (`:44`), 500 on error (`:45-48`), else encodes the
  result (`:50-51`).
- Rate limit `allowDetect` (`backend/api/ratelimit_detect.go:38-64`): 20
  requests per fixed one-minute window per client IP (`detectRateLimit`,
  `:36`), in-process map, swept at most once a minute (`:43-52`); client IP
  from the socket peer, or `X-Real-IP` only from a `TRUSTED_PROXIES` peer
  (`clientIP`, `:96`; `parseTrustedProxies`, `:68`; rationale comment `:12-20`). Tests:
  `backend/api/ratelimit_detect_test.go:47-150`.
- `/api/manager/detect` (`routes.go:241-242`) is a different feature (team
  detection) and does not call `DetectAuthProvider`.

### 2.4 The contract

- `POST /api/auth/detect`, `operationId: detectAuthProvider`
  (`contracts/openapi/paths/auth.yaml:833-880`): 200, 400, 429, 500.
- `SsoDetectResponse` (`auth.yaml:2693-2720`): `type` is required, enum
  `[google, microsoft, sso, generic]` (`:2698-2700`); the description documents
  the Microsoft probe ("a live tenant probe against login.microsoftonline.com
  (2s timeout, 1h cache), else google", `:2701-2705`). `provider_name` and
  `redirect_url` only when `type == "sso"`.
- Generated Go: `SsoDetectResponseType` constants
  (`backend/api/gen/paceday.gen.go:580-585`), `SsoDetectResponse`
  (`:3542-3568`). The handler encodes the hand-written `auth.DetectResult`
  (`sso_detect.go:18-23`), not the generated type.
- Generated TS on the web: `src/api/generated/types.ts` and
  `schemas.types.d.ts` (`smart-calendar-flow` `origin/main`).

### 2.5 Callers

- **Web** — `src/components/auth/AuthDialog.tsx` (`smart-calendar-flow`
  `origin/main` `54397a5`), the only consumer:
  - The dialog has two steps, `discover` and `generic` (`:72`).
  - `onEmailContinue` (`:106-147`) validates the email (`:109-113`), posts it
    to detection (`:118-121`) and then:
    - `google` → **navigates immediately** to the Google sign-in start with
      `login_hint=<email>` (`:122-125`);
    - `microsoft` → **navigates immediately** to the Microsoft start with
      the hint (`:126-129`);
    - `sso` with a redirect path → navigates to it with the hint (`:130-134`);
    - anything else, **including any value the web does not know**, → the
      password step (`:136-138`).
  - The provider buttons at the top of the discover step (`:247-264`) are
    shown only when the provider is available (`:186-187`) and navigate
    **without** a login hint (`onSocial`, `:94-104`).
  - The password step (`GenericStep`, `:332-496`) offers password sign-in and
    registration by posting to `/api/auth/login` and `/api/auth/register`
    (`:359`, `:364`). **Neither route exists on the backend** (no such route
    in `backend/api/routes.go`, no operation in `auth.yaml`, no match for
    either path anywhere in `clockwise-like` `origin/main`): every attempt
    ends in a 404, which the step shows as "No account found — try creating
    one." The password step is therefore a dead end for **every** address,
    consumer ones included (challenge finding B-1; filed separately, see §5).
    For a company domain it is doubly wrong: even a working password form is
    not how those users sign in.
  - `apiFetch` (`src/lib/api.ts:84-104`) does not validate the answer against
    the generated zod schema, so an answer value the web does not know is not
    an error: it falls through to the password step (`:136-138`).
  - Tests: `AuthDialog.test.tsx` — SSO navigation (`:76-87`), Google navigation
    with hint (`:89-94`), PAC-71 AC-32 provider buttons (`:96-113`), password
    step for `generic` (`:115-121`).
- **After a failed provider sign-in** the user returns to the landing page
  with `?error=auth_failed` or `?error=signin_refused`
  (`src/pages/Index.tsx:570-573`); the notice's retry clears the parameter and
  opens the dialog empty (`:580-583`). The typed email is not remembered, so a
  retry with the other provider loses the hint.
- **Server forwarding of the hint**: `loginHintFrom`
  (`backend/api/handlers_auth.go:66-72`) forwards a valid hint on the Google
  start (`:40`), the Microsoft start (`handlers_microsoft_auth.go:36`) and the
  SSO start (`handlers_sso.go:101`).
- **MCP** (`mcp/`): no reference to detection or `login_hint` (grep over
  `mcp/` finds nothing). No other caller of `DetectAuthProvider` exists outside
  tests (`grep` over `backend/`).

### 2.6 Tests today

- `backend/auth/sso_detect_test.go`: generic (`:10-19`), invalid email
  (`:21-30`), SSO row (`:32-59`), and
  `TestDetectAuthProvider_CustomDomain_DefaultsGoogle` (`:61-73`), which
  **makes a live HTTPS call to login.microsoftonline.com** and accepts either
  answer. `TestIsMicrosoftTenant_Cached` (`:75-86`) seeds the package cache
  and never cleans it up.
- `backend/api/pac35_auth_flows_test.go:856-…`
  (`TestPAC35_Detect_EveryDocumentedStatus`): 200 sso, 200 generic, 400, and
  the other documented statuses through the real router.
- There is no injectable resolver or HTTP client in detection today. The repo
  already has an in-process DNS double for another package
  (`backend/calendar/dnsdouble_test.go:15-60`, a UDP DNS responder on
  127.0.0.1 that a `*net.Resolver` is pointed at) and a resolver test seam in
  `backend/calendar/guard.go:19-27`.
- **e2e**: the backend's `extra_hosts` send `login.microsoftonline.com` to the
  sink (`e2e/docker-compose.e2e.yml:159-161`), so in the e2e stack every
  company domain is answered `google` today (and the hermeticity check,
  `tests/hermeticity.spec.ts`, would fail the run if any journey typed one,
  because the probe connects to the sink). There is no DNS stand-in, and
  `e2e/README.md:315-320` says sign-in journeys reaching a real provider are
  `test.fixme`. The only e2e journey that posts to detection today
  (`e2e/tests/sso-stepup.spec.ts`) uses an SSO-row domain, which never reaches
  the probe. **DNS is not sinked**: the backend container resolves through
  Docker's embedded DNS, which forwards to the host's resolver, and the sink
  only records TCP connections. Once detection uses DNS, an e2e journey typing
  a company domain would query the real internet without any check noticing,
  unless the stack gives detection a stand-in resolver (AC-27, D-9).

## 3. Desired behaviour

When a person types a work email:

1. If the domain has company SSO configured in Paceday, they go to it, as
   today.
2. Otherwise Paceday looks at where the domain's mail is hosted. Mail hosted
   by Google Workspace means Google; mail hosted by Microsoft 365 means
   Microsoft. Only then does Paceday send the person to that provider
   directly, carrying the typed email as the login hint.
3. In every other case — mail behind a third-party gateway, mixed or migrating
   mail, no mail records, a lookup that fails or is slow — Paceday does not
   guess. The dialog shows both "Continue with Google" and "Continue with
   Microsoft" (only those the deployment offers), each carrying the typed
   email as the hint, and a way back to edit the email. It never shows the
   password form for a company domain.
4. The person can always pick the other provider: on the choice step, and
   after coming back from a failed or refused provider sign-in, where the
   notice offers both providers with the email they typed earlier in this tab.
5. Consumer addresses (gmail.com and the rest of the list) behave exactly as
   today.

Privacy: the lookup runs on Paceday's server, uses only the domain part of the
address, and goes only to the resolver the server's host is configured with. No third-party HTTP call
is made to decide the provider, and the local part of the address is never
sent anywhere or logged by detection.

## 4. Acceptance criteria

Test levels: **[unit]** Go unit test with a fake resolver (no live DNS, no live
HTTP); **[api]** Go test through the real router; **[contract]** schema and
generated types; **[vitest]** web component test; **[e2e]** Playwright.

Fixtures (used by name below; values are DNS answers served by the fake):

| Fixture | MX records (preference host) | Expected |
|---|---|---|
| `gorgias.com` | 1 aspmx.l.google.com, 5 alt1.aspmx.l.google.com, 5 alt2.aspmx.l.google.com, 10 alt3.aspmx.l.google.com, 10 alt4.aspmx.l.google.com — and a fake Microsoft endpoint that would report a tenant and "Federated" if asked | google |
| `google-new.test` | 1 smtp.google.com | google |
| `googlemail-legacy.test` | 10 aspmx2.googlemail.com, 10 aspmx3.googlemail.com | google |
| `contoso.test` | 0 contoso-test.mail.protection.outlook.com | microsoft |
| `dane.test` | 0 dane-test.o-v1.mx.microsoft | microsoft |
| `eo-legacy.test` | 10 eo-legacy-test.mail.eo.outlook.com | microsoft |
| `olc.test` | 10 olc-test.olc.protection.outlook.com (Outlook.com consumer custom domain, personal Microsoft accounts) | choose |
| `sovereign.test` | 0 sovereign-test.mail.protection.office365.us | choose |
| `mimecast.test` | 10 eu-smtp-inbound-1.mimecast.com, 10 eu-smtp-inbound-2.mimecast.com | choose |
| `proofpoint.test` | 10 mx0a-001.pphosted.com, 10 mx0b-001.pphosted.com | choose |
| `migrating.test` | 1 aspmx.l.google.com, 5 migrating-test.mail.protection.outlook.com | choose |
| `backup.test` | 1 aspmx.l.google.com, 20 mx.backup-provider.example | google |
| `gateway-first.test` | 1 mx.gateway.example, 20 aspmx.l.google.com | choose |
| `lookalike.test` | 10 mail.evilgoogle.com, 10 aspmx.l.google.com.evil.example | choose |
| `nullmx.test` | 0 . (RFC 7505 null MX) | choose |
| `nxdomain.test` | NXDOMAIN | choose |
| `nomx.test` | no MX records (an A record exists) | choose |
| `slow.test` | resolver never answers | choose |
| `servfail.test` | SERVFAIL | choose |
| `GOOGLE-CASE.TEST` | 1 ASPMX.L.GOOGLE.COM. (upper case, trailing dot) | google |
| `stripe-like.test` | 10 aspmx.l.google.com, 20 alt1.aspmx.l.google.com, 30 aspmx2.googlemail.com | google |
| `partial.test` | the resolver returns 10 aspmx.l.google.com **together with an error** (one record had an invalid name) | choose |
| `nullmix.test` | 0 . and 10 aspmx.l.google.com (malformed: a null MX must be alone) | choose |
| `intranet` (single label) | (never queried) | choose |

The `gorgias.com`, `google-new.test` (google.com), `contoso.test`
(microsoft.com, github.com, unilever.com), `proofpoint.test` (accenture.com,
proofpoint.com), `mimecast.test` (mimecast.com) and `stripe-like.test`
(stripe.com) shapes were checked against live public MX answers on 2026-11-13
(Challenge log C-1). The fixtures stay synthetic; no test reads live DNS.

"choose" is the new answer of §9 D-4.

### Server decision

- **AC-1** [unit] Given the `gorgias.com` fixture, when detection runs for
  `alice@gorgias.com`, then the answer is google, and the fake Microsoft
  endpoint records zero requests. *(Fails today: answer is microsoft.)*
- **AC-2** [unit] Given each row of the fixture table, when detection runs for
  `user@<fixture>`, then the answer equals the "Expected" column.
  Table-driven; one sub-test per row.
- **AC-3** [unit] Given a domain with an SSO provider row and the `contoso.test`
  MX answer, when detection runs, then the answer is sso with today's provider
  name and redirect path, and the fake resolver records zero queries.
- **AC-4** [unit] Given a consumer domain from the list (gmail.com,
  outlook.com), and an invalid email (`not-an-email`), when detection runs,
  then the answer is generic and the fake resolver records zero queries.
- **AC-5** [unit] Given the `slow.test` fixture, when detection runs with a
  caller context that has no deadline, then it returns choose within the
  lookup budget plus 500 ms (§9 D-7), and returns no error.
- **AC-6** [unit] Given a caller context cancelled before or during
  detection, when detection runs for a company domain without SSO, then that
  caller gets choose within 100 ms of the cancellation, and the cancellation
  is **not** cached (a following detection for the same domain with a live
  context queries again and gets the fake's real answer). Given a second
  caller waiting on the same shared lookup (AC-11), then the first caller's
  cancellation does not cancel the second caller's answer. Every lookup the
  fake sees carries a deadline no later than the D-7 budget.
- **AC-7** [unit] Given the `servfail.test` and `slow.test` fixtures, when
  detection runs, then no error reaches the handler: the HTTP answer is 200
  choose, never 500 and never google or microsoft. (Only an SSO-row database
  error remains a 500.)
- **AC-8** [unit] Given the `servfail.test` fixture answered once, and the fake
  then switched to Google MX for the same domain, when detection runs again
  after the failure-cache period (§9 D-8) using an injected clock, then the
  answer is google. And when it runs again *before* that period, the fake
  records zero more queries and the answer is choose (failures are not
  hammered).
- **AC-9** [unit] Given `contoso.test` answered once, when detection runs again
  within the result cache period, then the fake records no second query; and
  after the period (injected clock), it records exactly one more.
- **AC-10** [unit] Given the cache bound N (§9 D-8; the test may construct
  detection with a small N), when N+1 distinct domains are detected, then the
  cache holds at most N entries and the first domain detected is the one
  evicted (detecting it again records one more query); and a production
  construction uses N = 10 000. *(Fails today:
  `tenantCache` never deletes, `sso_detect.go:61-71`.)*
- **AC-11** [unit] Given 50 concurrent detections of the same uncached domain,
  when they complete, then the fake records exactly one query and all 50 get
  the same answer. Run under `-race`.
- **AC-12** [unit] Privacy. Given detection for `secret.local.part@contoso.test`,
  then the fake resolver's only recorded query name is `contoso.test` (lower
  case, no local part); a recording HTTP transport installed for the test
  records zero requests; and the captured log output of the detection path
  does not contain `secret.local.part`.
- **AC-13** [unit] Given a domain that is not a valid multi-label ASCII DNS
  name (for example `user@exa mple.com`, `user@-bad-.test`, a 64-character
  label, a name over 253 characters, a single label such as `user@intranet`,
  or a non-ASCII name such as `user@exämple.com`), when detection runs, then
  the answer is choose and the resolver records zero queries. Given
  `user@gorgias.com` (valid), then the name the fake receives is the
  fully-qualified `gorgias.com.` (trailing dot), so no resolver search-list
  expansion can apply.
- **AC-14** [unit] Hermeticity. Given the detection package's tests and the
  `backend/api` tests that reach detection (PAC-35, rate limit, AC-16/AC-17),
  when they run with the production resolver replaced by one that fails the
  test on any use, then every test passes. `TestDetectAuthProvider_CustomDomain_DefaultsGoogle`
  (live call, `sso_detect_test.go:61-73`) and the cache-seeding
  `TestIsMicrosoftTenant_Cached` (`:75-86`) are replaced by AC-1/AC-2/AC-9.
- **AC-15** [unit] Given a non-test build, when detection is constructed
  without an injected resolver, then it uses the system resolver; the test
  seam cannot be set from configuration in a production build (the e2e
  stand-in of AC-27 is honoured only together with the existing stand-in
  guard `PACEDAY_ALLOW_STANDINS`, AC-28).

### API

- **AC-16** [api] Given the real router with a fake resolver serving
  `mimecast.test`, when `alice@mimecast.test` is posted, then the status is 200
  and the body decodes, with unknown fields disallowed, into the generated
  answer type with the new value, and carries neither provider name nor
  redirect path.
- **AC-17** [api] Given the same router, then the existing
  `TestPAC35_Detect_EveryDocumentedStatus` sub-tests (200 sso, 200 generic,
  400, 429, 500) still pass unchanged, and the 21st request in a minute from
  one client IP is still 429.

### Contract

- **AC-18** [contract] The answer schema's type enumerates the new value; its
  description documents the MX rule (§9 D-2/D-3) and no longer mentions the
  Microsoft tenant probe; the description of "generic" says it is only for
  consumer and unparseable addresses. `make openapi-check` passes with the
  regenerated Go and TS types, and the TS union on the web includes the new
  value.

### Web

- **AC-19** [vitest] Given detection answers the new value and both providers
  are available, when `alice@mimecast.test` is submitted, then the dialog
  shows the Google and Microsoft provider buttons ("Continue with …", or
  "Sign up with …" in sign-up mode, as the discover step labels them today)
  for that email, no password field, no navigation happens; clicking Google navigates to the
  Google start with `login_hint=alice%40mimecast.test`, and clicking Microsoft
  navigates to the Microsoft start with the same hint.
- **AC-20** [vitest] Given the new value and only Microsoft available, then
  only the Microsoft button is shown, and still nothing navigates until it is
  clicked. Given neither available, the step says sign-in with a provider is
  not available on this deployment and offers going back.
- **AC-21** [vitest] Given the choose step, when the user activates "back" /
  "use a different email", then the discover step returns with the email
  still filled in.
- **AC-22** [vitest] Given detection answers a type the web does not know (for
  example `"future"`), then the dialog shows the choose step, not the password
  step. *(Fails today: `AuthDialog.tsx:136-138` falls through to the password
  step.)*
- **AC-23** [vitest] Given detection answers google (or microsoft), then the
  dialog navigates to that provider's start with the hint, as today
  (`AuthDialog.test.tsx:89-94` unchanged).
- **AC-24** [vitest] Given detection answers generic for `carol@gmail.com`,
  then the password step is shown, as today (`AuthDialog.test.tsx:115-121`
  unchanged).
- **AC-25** [vitest] Given the user typed `alice@gorgias.com` in this tab, was
  sent to a provider, and came back with `?error=auth_failed` (and separately
  `?error=signin_refused`), when they use the notice's retry, then the dialog
  offers both available providers for `alice@gorgias.com`, and each navigates
  with `login_hint=alice%40gorgias.com`. Given a fresh tab with no email typed,
  the retry opens the dialog as today. *(Fails today: `Index.tsx:580-583`
  opens it empty.)*
- **AC-26** [vitest] Given the remembered email of AC-25, when a sign-in
  completes (the app loads with a signed-in user), and separately when the
  user signs out, then the remembered email is gone (a subsequent failure
  notice offers no prefilled email). It is never written to the URL other than
  as the provider start's `login_hint`, never to `localStorage`, IndexedDB or
  a cookie, and never sent to the Paceday API by the remembering code. When
  `sessionStorage` throws (blocked storage), the dialog and the notice still
  work, without the prefill.

### End to end

- **AC-27** [e2e] Given the e2e stack with a DNS stand-in (D-9) answering the
  `gorgias.com` MX fixture for a seeded test domain (and the Microsoft names
  still sinked), when a user types an address at that domain and continues,
  then the browser's next request is the API's Google sign-in start
  (`/api/auth/google`) with `login_hint` equal to the typed address (the
  journey stops there: Google itself is sinked); and for a domain served the
  `mimecast.test` answer, both provider buttons are shown, no navigation
  happens, and clicking Microsoft requests `/api/auth/microsoft` with the
  hint. The run's hermeticity check still passes. **Built in this ticket**
  (OQ-2 resolved by the challenger): without the stand-in the e2e backend's
  detection would query the real internet unseen (§2.6).
- **AC-28** [unit] Given a production configuration (no
  `PACEDAY_ALLOW_STANDINS`) that sets the DNS stand-in variable (D-9), then
  startup refuses to start (as the calendar stand-in does today); given
  `PACEDAY_ALLOW_STANDINS=true` and a stand-in address outside the permitted
  set (`config.PermittedPeer`), then startup refuses; given both valid, then
  detection queries only the stand-in and a startup notice names it.

## 5. Explicitly out of scope

- **Consumer addresses.** gmail.com, outlook.com and the rest keep the
  password step. Sending gmail.com users to Google would be a separate product
  change, and personal Microsoft accounts are not supported until PAC-77
  (owner, 2026-11-01). Noted as a possible follow-up (§8 OQ-5).
- **The provider's own page.** Once the user is at Google or Microsoft,
  Paceday cannot add controls there. "Switch provider" is offered before the
  redirect (choose step) and after the return (failure notice).
- **Sovereign Microsoft clouds** (GCC High, 21Vianet). Their MX names are not
  treated as Microsoft: Paceday signs in only through the global Microsoft
  endpoints, so routing those users there would fail. They get the choice.
- **Remembering a choice per domain** (learning from the provider a user
  actually signed in with). Useful later; it stores data about domains and
  needs its own privacy decision.
- **API-062** (per-replica rate limit): fixing it needs shared state across
  replicas, unrelated to this bug. Detection's cost per request goes down here
  (one DNS query instead of an HTTPS call), so the open finding does not get
  worse. Accepted by the challenger (OQ-3): the touch-it-fix-it rule covers
  findings on the code a ticket changes, and this ticket changes neither the
  limiter nor its keying. One new exposure is recorded instead of fixed: a
  caller can now make the server's resolver query a domain the caller
  controls, which tells the caller which resolver the server uses (it learns
  nothing about other users). Bounded by the same 20/min per IP and the 2 s
  budget; the cache bound (D-8) caps memory.
- **The password step's missing server routes** (challenge B-1, §2.5). The web
  posts to `/api/auth/login` and `/api/auth/register`, which do not exist.
  This ticket stops sending company domains there; the consumer path stays as
  it is today and the gap goes to its own ticket, filed by the orchestrator (owner question: build
  password sign-in, or send consumer addresses to the provider choice). OQ-5
  is folded into that ticket.
- **Custom SSO precedence and the SSO start** are unchanged.

## 6. What must not change

| Behaviour | Protected by |
|---|---|
| SSO row wins over everything, answer shape unchanged | `sso_detect_test.go:32-59`; `TestPAC35_Detect_EveryDocumentedStatus` (`pac35_auth_flows_test.go:856-`) |
| Consumer and invalid addresses answer generic | `sso_detect_test.go:10-30`; PAC-35 "200 not sso" |
| 20/min per client IP, socket-peer keying, 429 text | `ratelimit_detect_test.go:47-150` |
| A database error on the SSO lookup is a 500 with the PAC-21 body | PAC-35 detect sub-tests |
| The hint is forwarded by the Google, Microsoft and SSO starts only when valid | `handlers_auth.go:66-72` and the PAC-35 login-hint tests |
| Strong-signal answers navigate with the hint | `AuthDialog.test.tsx:89-94` |
| Top provider buttons start the public sign-in, never a connect (PAC-71 AC-32) | `AuthDialog.test.tsx:96-113` |
| generic → password step | `AuthDialog.test.tsx:115-121` |
| SSO answer → API-origin redirect path with hint | `AuthDialog.test.tsx:76-87` |
| Failure notices accept only fixed values, never echo the URL | `Index.authFailed.test.tsx`, `pac71Landing.test.tsx` |
| No demo after a sign-in failure (PAC-83) | `AuthDialog.pac83.test.tsx` |

## 7. Rollback

No migration and no stored data. Reverting is a code revert in both repos.

Order matters because of the new answer value:

- **Deploy**: web first (it must understand the new value, AC-19 and AC-22),
  then the backend. A backend deployed first would send the new value to the
  current web, which shows the password step for it (`AuthDialog.tsx:136-138`;
  `apiFetch` does not reject the unknown value, §2.5) — not a misroute, but
  the dead end of B-1 for the affected users.
- **Residual after a correct order**: a browser tab that loaded the old web
  before the web deploy and is still open after the backend deploy gets the
  same dead end until it reloads. Accepted: it is bounded by the tab's
  lifetime, it is no worse than today's consumer path, and it never routes to
  the wrong provider. The ordering cannot be removed without a client opt-in
  on the request that the server would have to honour forever; the contract
  stage records that alternative as rejected (`contracts/features/PAC-97.md`).
- **Merging order**: the web PR may merge first and is safe against today's
  backend (it never receives the new value). The backend PR merges only after
  the web PR is deployed. The orchestrator records the web deploy before
  merging the backend (OQ-4).
- **Rollback**: backend first, then web. The new web handles the old backend's
  answers unchanged (AC-23, AC-24), so a backend-only rollback is safe; it
  brings back the gorgias.com bug and nothing else.

## 8. Open questions

- **OQ-1 — resolved (challenger, 2026-11-13).** `mx.microsoft` is real:
  Microsoft Learn, "How SMTP DANE works" (Exchange Online, updated
  2026-07-21), gives the DNSSEC MX form `<MX token>.<subdomain>.mx.microsoft`
  (example `contosotest-com.o-v1.mx.microsoft`), and the zone `mx.microsoft`
  is delegated (NS `ns1-81.azure-dns.com` and three others, SOA
  `azuredns-hostmaster.microsoft.com`). The same page names the legacy
  global names `mail.protection.outlook.com` and `mail.eo.outlook.com`; both
  are in D-2. `mail.protection.outlook.de` (Microsoft Cloud Deutschland,
  sovereign) is not.
- **OQ-2 — resolved (challenger): build the stand-in.** DNS is not sinked in
  the e2e stack (§2.6), so without a stand-in the change silently opens a
  path to the real internet that the hermeticity check cannot see. AC-27 and
  AC-28.
- **OQ-3 — resolved (challenger): defer API-062**, reasoning in §5.
- **OQ-4 (orchestrator).** Deploy order across the two repos (§7): the web PR
  deploys before the backend PR merges.
- **OQ-5 — moved** to the follow-up ticket for B-1 (§5): with the password
  routes missing, "should consumer addresses go to a provider" is no longer a
  nice-to-have and is that ticket's owner question.

No other owner question: the issue fixes the expected behaviour (mail host
decides; ambiguous → user picks; switching always possible).

## 9. Decisions

Each is **factory-assumed** unless marked **owner**.

- **D-1 Order** — **owner** (issue, 2026-11-13, and caller's brief):
  (1) invalid or consumer → generic (unchanged); (2) SSO row → sso
  (unchanged); (3) MX lookup → google or microsoft on a strong match;
  (4) anything else → choose. No silent redirect on a weak signal.
- **D-2 Strong match** — factory-assumed (hosts from the issue, refined):
  - Google: MX host equal to `google.com`/`googlemail.com` or ending in
    `.google.com` / `.googlemail.com` (covers `aspmx.l.google.com`,
    `alt*.aspmx.l.google.com`, `smtp.google.com`, `aspmx*.googlemail.com`).
  - Microsoft: MX host ending in `.mail.protection.outlook.com`,
    `.mail.eo.outlook.com` (legacy Exchange Online) or `.mx.microsoft`
    (Exchange Online with inbound DNSSEC/DANE; OQ-1, confirmed).
  - Not Microsoft: `*.olc.protection.outlook.com` (Outlook.com consumer
    custom domains, i.e. personal Microsoft accounts, which cannot sign in
    until PAC-77; fixture `olc.test`), and the sovereign-cloud names
    (`*.mail.protection.office365.us`, `*.mail.protection.partner.outlook.cn`,
    `*.mail.protection.outlook.de`; fixture `sovereign.test`).
  - Matching is on label boundaries, after lower-casing and removing one
    trailing dot (fixtures `lookalike.test`, `GOOGLE-CASE.TEST`).
  - Everything else, including Mimecast, Proofpoint, Barracuda, Cisco and
    sovereign-cloud Microsoft names, is a non-match.
- **D-3 Which records count** — factory-assumed. Take the records with the
  lowest preference value (the primary ones). The answer is provider P when
  every primary record matches P **and** no record at any preference matches
  the other provider. Otherwise choose. A third-party backup at a higher
  preference does not block a match (`backup.test`); a gateway in front does
  (`gateway-first.test`); any Google+Microsoft mix is a migration and gets the
  choice (`migrating.test`). Null MX, NXDOMAIN, no MX → choose; no fallback to
  A records. "Lowest preference" is the numerically smallest value among
  the records returned (RFC 5321 §5.1: lower is preferred; records of equal
  preference are equals, so all of them must match). Duplicate records are
  harmless. A null MX (`.`) anywhere in the answer → choose.
- **D-4 Contract: a new answer value, not a changed "generic"** —
  factory-assumed. "generic" keeps its meaning (consumer or unparseable
  address → the password step), because changing what consumer addresses get
  is not this ticket's decision (§5, B-1) and the password step is wrong for
  a company domain whatever happens to it. A distinct value means "ask the
  user to pick a provider". Name: `choose` (contract stage, 2026-11-13). It carries no provider name and no redirect path. The web treats
  any value it does not know as this one (AC-22), so a future value never
  lands on the password form.
- **D-5 getuserrealm: not used** — factory-assumed. Reasons: (a) it is an
  undocumented Microsoft endpoint with no stability guarantee; (b) it needs a
  login name, so it either sends the user's address to Microsoft or a made-up
  one; (c) "Managed" only says an Entra tenant manages the domain, which a
  Google Workspace company with an Azure subscription can also have — exactly
  the class of mistake this bug is; (d) the case where it would add
  information (a gateway MX in front of Microsoft 365) is the case where the
  rule forbids a silent redirect anyway, and the choice costs one click.
  The openid-configuration probe is **removed** too: gorgias.com shows it is
  not a reliable signal, and with it gone detection makes no third-party HTTP
  call at all.
- **D-6 Web on a strong match** — factory-assumed, following the issue:
  navigate immediately with the hint, as today. Switching is offered on the
  choose step and after a failed return (AC-25), not by an extra click on
  every sign-in.
- **D-7 Lookup budget** — factory-assumed: one MX query for the
  fully-qualified name (trailing dot, so no search-list expansion), 2 s total
  including retries (today's probe budget, `sso_detect.go:83`). The shared
  lookup (D-8) runs under its own 2 s budget, not under one caller's context;
  each caller stops waiting when its own context ends. Timeout, any resolver
  error, or a partial answer returned together with an error (Go's
  `LookupMX` does this when some records carry invalid names) → choose,
  never an error and never a provider. Only a syntactically valid,
  multi-label ASCII domain is queried (AC-13); others → choose without a
  query.
- **D-8 Caching** — factory-assumed: definite answers from a real DNS reply
  (google, microsoft, choose from NXDOMAIN / no MX / non-matching MX) are
  cached for 1 hour; failures (timeout, SERVFAIL, refused) for 1 minute, so a
  blip neither sticks for an hour (today's bug, `sso_detect.go:91-93`) nor
  sends a query per keystroke. The cache is bounded (N = 10 000 entries,
  oldest evicted first) and concurrent lookups for one domain share a single
  query. In-process, per replica.
- **D-9 Test seam** — factory-assumed: detection takes its resolver as an
  injected dependency (an interface answering MX queries); production uses
  the system resolver; unit tests use an in-memory fake with per-domain
  answers, delays and errors, plus a query recorder. No test in the package
  touches live DNS or HTTP (AC-14). The e2e stand-in (AC-27, AC-28) is one
  environment variable naming a DNS server address (`ip:port`) that detection
  queries instead of the system resolver. It follows the existing stand-in
  rule (`backend/config/endpoints.go`): refused at startup unless
  `PACEDAY_ALLOW_STANDINS` is declared, the address must be a
  `config.PermittedPeer`, and a startup notice names it. The e2e stack runs a
  small DNS responder (for example inside `e2e/fake-google`, modelled on
  `backend/calendar/dnsdouble_test.go`) on the pinned network.
- **D-10 Remembering the email for the switch after a failure** —
  factory-assumed, challenger-checked: the web keeps the last email typed in
  the dialog in `sessionStorage` only (tab-scoped, gone when the tab closes,
  never sent anywhere by the remembering code), clears it on a completed
  sign-in and on sign-out, and never puts it in a URL other than as the
  provider start's login hint (AC-25, AC-26). This is compatible with PAC-83:
  PAC-83's storage rules concern the demo flag (`paceday:demo`) and the old
  mock-mode flag, and the app already keeps tab-scoped data in
  `sessionStorage` (`src/api/coverageCache.ts`). The value is the user's own
  address in their own tab, shown only to them; the failure notices still
  read nothing from the URL (§6). The server is unchanged here; it does not
  echo the email in the failure redirect.
- **D-11 Privacy** — **owner-aligned** (caller's brief): lookups run
  server-side, carry only the domain, go only to the server's configured
  resolver; detection logs never contain the local part (AC-12).
- **D-12 PAC-71 interplay** — factory-assumed, challenger-checked: none.
  Nothing else reads the tenant probe: `isMicrosoftTenant` and `tenantCache`
  have no caller outside `sso_detect.go` and its test, and PAC-71 takes the
  Microsoft tenant from the signed ID token (`backend/auth/ms_idtoken.go`),
  never from a domain lookup. PAC-77 (personal Microsoft accounts) is not
  affected: consumer Microsoft domains are on the generic list, and the
  Outlook.com consumer MX is a non-match (D-2), so detection never sends a
  personal-account domain to Microsoft directly. Detection only picks
  which sign-in start to offer; account binding to the provider identity
  (PAC-71) happens after the provider answers and is untouched. Picking the
  "wrong" provider can no longer reach the wrong account, only fail sign-in,
  which the switch then recovers.

## Challenge log

### Challenge — 2026-11-13 (spec-challenger)

**Verdict**: accept-with-changes. Every change below is already applied to the
body; the spec is accepted as it now stands.

#### Live checks (public domains only, 2026-11-13, resolver 1.1.1.1)

| Domain | Lowest-preference MX | Rule gives |
|---|---|---|
| gorgias.com | 1 aspmx.l.google.com (then alt1-4 at 5/10) | google |
| shopify.com | same shape as gorgias.com | google |
| stripe.com | 10 aspmx.l.google.com (20 alt1/alt2, 30 aspmx2/3.googlemail.com) | google |
| google.com | 10 smtp.google.com | google |
| microsoft.com | 10 microsoft-com.mail.protection.outlook.com | microsoft |
| github.com | 0 github-com.mail.protection.outlook.com | microsoft |
| unilever.com | 10 unilever-com.mail.protection.outlook.com | microsoft |
| accenture.com | 10 mx0a/mx0b-001dcc01.pphosted.com (Proofpoint gateway) | choose |
| proofpoint.com | 10 mxa/mxb-…gslb.pphosted.com | choose |
| mimecast.com | 10 service-alpha-inbound-a/b.mimecast.com | choose |
| outlook.com | 5 outlook-com.olc.protection.outlook.com | (generic list; MX shape is the consumer one, see C-3) |
| `mx.microsoft` (NS/SOA) | delegated to Azure DNS, SOA azuredns-hostmaster.microsoft.com | zone exists (OQ-1) |

C-1. The issue's facts for gorgias.com hold, and the D-2/D-3 rule gives the
expected answer for every live sample. Gateways in front of Microsoft 365
(accenture.com) get `choose`, as intended: one extra click, never a misroute.

#### Blocking (fixed in the body)

1. **B-1 (high) The password step is a dead end for everyone.** §2.5 said the
   step "is right for consumer addresses that use a Paceday password". The web
   posts to `/api/auth/login` and `/api/auth/register`
   (`AuthDialog.tsx:359,364`), and neither route exists anywhere in
   `clockwise-like` `origin/main` (not in `routes.go`, not in `auth.yaml`).
   This changes the cost assessment of §7 (an old web receiving `choose` lands
   on a form that cannot work) and turns OQ-5 into a real product gap.
   Resolved: §2.5 corrected; §5 sends the gap to its own ticket (orchestrator
   files it; owner question there); D-4 no longer justifies "generic" by the
   step being right.
2. **B-2 (medium) DNS is not sinked in the e2e stack.** `extra_hosts` sinks
   TCP to provider names; the backend resolves through Docker DNS to the real
   internet, and `hermeticity.spec.ts` reads only the TCP sink. The spec's
   default (`test.fixme`, no stand-in) would have opened an unseen path to
   the internet for any future journey typing a company address. Resolved:
   OQ-2 decided "build"; AC-27 rewritten, AC-28 added; D-9 names the guard
   rules (ALLOW_STANDINS + `PermittedPeer` + startup notice).
3. **B-3 (medium) AC-6 contradicted AC-11.** With one shared lookup per
   domain, "the resolver is not left running" when one caller cancels would
   cancel the others' answer, and nothing stopped a cancellation from being
   cached as a failure. Resolved: D-7 gives the shared lookup its own 2 s
   budget; AC-6 now requires per-caller release, no caching of a
   cancellation, and no cross-caller cancellation.

#### Non-blocking (fixed in the body)

1. OQ-1 resolved: `.mx.microsoft` confirmed (Microsoft Learn, DANE page,
   form `<MX token>.<subdomain>.mx.microsoft`); `.mail.eo.outlook.com` added
   (the same page lists it as a legacy global name).
2. `*.olc.protection.outlook.com` is the Outlook.com consumer host (live
   outlook.com MX). A custom domain on it means personal Microsoft accounts,
   refused until PAC-77; it is now an explicit non-match (fixture `olc.test`).
   Sovereign names got a fixture.
3. Query hygiene: the query is for the FQDN (no search-list expansion); a
   single-label, non-ASCII or over-long name is never queried (AC-13).
4. Go's `LookupMX` can return valid records together with an error; that is
   now `choose` (D-7, fixture `partial.test`). Null MX mixed with records →
   `choose` (fixture `nullmix.test`). "Lowest preference" is defined (D-3).
5. AC-8 accepted "at most one more query" before the failure period ends;
   tightened to zero. AC-10 now fixes the eviction order and the production N.
6. AC-19 named the sign-in labels only; the dialog shows "Sign up with …" in
   sign-up mode.
7. D-10 / AC-26: `sessionStorage` is consistent with PAC-83 (its rules are
   about the demo and mock-mode flags; `coverageCache.ts` already uses
   `sessionStorage`). Added clearing on sign-out, a "storage throws" case,
   and "never sent to the API".
8. §7: `apiFetch` does not validate against the zod schema, so the old web
   does not error on `choose`; it shows B-1's dead end. The residual (stale
   tabs open across both deploys) is recorded and accepted. Avoiding the
   order needs a client opt-in honoured forever; rejected at the contract
   stage.
9. OQ-3: API-062 deferral accepted (this ticket does not touch the limiter).
   New exposure recorded in §5: a caller can make the server query a domain
   it controls and so learn the server's resolver.
10. AC-14 now covers the `backend/api` tests that reach detection too.

#### Criteria I could not falsify

- None after the changes. (AC-6's earlier "the resolver is not left
  running" could not be distinguished from "a shared lookup outlives one
  caller"; replaced.)

#### Citations I checked and found wrong

- `ratelimit_detect.go:12-64` / `:14-21` for `allowDetect` and the client
  IP — actually `allowDetect` is `:38-64`, `clientIP` is `:96`,
  `parseTrustedProxies` `:68`; `:12-20` is the rationale comment.
- §2.5 "password step is right for consumer addresses" — the routes it
  calls do not exist (B-1).
- `dnsdouble_test.go:20-60`, "a `*net.Resolver` with a custom `Dial`" — it
  is a UDP DNS responder (`:15-60`) that a resolver is pointed at.
- Everything else in §2 checked and correct at `bc7b144` / `54397a5`
  (`sso_detect.go` lines and behaviour, `domain.go`, handler, routes,
  outbound 10 s, tests, contract lines, AuthDialog lines, Index lines).

#### Consumers this breaks

- `smart-calendar-flow/src/components/auth/AuthDialog.tsx:136-138` — an
  unchanged web shows the password step for `choose` (§7 order).
- `smart-calendar-flow/src/api/generated/{types.ts,schemas.ts,schemas.types.d.ts}`
  — the enum gains `choose`; regenerated from the bundle.
- `clockwise-like/backend/api/gen/paceday.gen.go` — new
  `SsoDetectResponseType` constant; regenerated.
- `clockwise-like/backend/auth/sso_detect_test.go:61-86` — the live-call and
  cache-seeding tests are replaced (AC-14).
- No MCP consumer (`mcp/` has no reference to detection).
