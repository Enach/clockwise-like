# Contract — PAC-50: SSO writes need an IdP-admin step-up, and the domain rule is exact

- **Spec**: `docs/specs/PAC-50.md` v2.2 (v2.1 challenge answered; §9 Decisions
  added at this stage)
- **Manifest**: `contracts/features/PAC-50.yaml`
- **Fragment**: `contracts/openapi/paths/auth.yaml` (plus its notes, section (d))
- **Absorbs**: PAC-46 (the exact-domain rule)
- **Revision**: 1, 2026-10-25

## 1. The change

Two operations change and two are added. Nothing else in the contract moves.

| Operation | Change |
|---|---|
| `createSsoProvider` `POST /api/admin/sso` | Documents the write gate of spec §4.2 in order. **400** gains `unknown property: <name>` and `provider_domain_mismatch`. **403** becomes `SsoWriteForbiddenResponse` (three variants, below). The domain rule becomes exact and case-insensitive: `sub.example.com` from org `example.com` is refused. |
| `deleteSsoProvider` `DELETE /api/admin/sso/{domain}` | Same gate. The `{domain}` segment is lower-cased. **400** `provider_domain_mismatch` replaces the old 403. **403** becomes `SsoWriteForbiddenResponse`. **404** `sso provider not found` is new (API-067). **204** now means a row really was deleted. |
| `startSsoAdminStepup` `GET /api/admin/sso/stepup?provider=google\|microsoft` | **New.** Behind the session. Answers 302 to the IdP and sets the `sso_stepup_state` cookie. Errors: 400 (provider missing or unknown), 401, 403 (no org, D-1), 503 (provider not configured, D-2). |
| `ssoAdminStepupCallback` `GET /api/admin/sso/stepup/callback` | **New.** Behind the session. Answers 302 to `<FRONTEND_URL>/settings?stepup=granted` or `?stepup=denied&reason=<enum>`; the `Location` header carries a `pattern` that admits exactly those nine forms. Errors: 401 (no session), 500 (storage, D-4). |

Schemas:

- `SsoWriteForbiddenResponse` = `oneOf` three disjoint objects:
  - `SsoWriteMembershipRequired`: `error` ∈ {`org membership required`, `org not found`}.
  - `SsoWriteStepupRequired`: `error` = `sso_write_requires_admin_stepup`, plus
    `stepup_url` = `/api/admin/sso/stepup` (required).
  - `SsoWriteFreshLoginRequired`: `error` = `sso_write_requires_fresh_login`.
  All three are `additionalProperties: false`, so `stepup_url` appears on the
  step-up variant and on no other.
- `SsoStepupProvider`: enum `google`, `microsoft`.
- `SsoProviderCreateRequest`: `minLength: 1` on `domain` and `provider_name`.
  This describes what the handler already enforces. The `x-uncertain` marker
  (U-21) is replaced by the determined answer.
- `SsoProviderResponse`: no shape change. The `x-uncertain` marker (U-22) is
  replaced by the observed answer, and the secret leak is linked to API-001 and
  PAC-22.

`listSsoProviders` is not touched. Read authorization and the secret projection
belong to PAC-22, which lands after PAC-50 and rebases onto it (spec §4.9).

## 2. Why this shape

### The 403 is one status with three disjoint bodies

The spec needs three different refusals at the same status code. The orgless
caller has nothing to step up for. The member without a grant must be sent to
step-up. The break-glass admin with a stale session must sign in again.

- **Chosen:** a `oneOf` of three closed objects, discriminated by `error`. A
  client can branch on `error` alone. The schema makes two wrong responses
  impossible to express: a step-up refusal without `stepup_url`, and a
  `stepup_url` on any other refusal. Every variant keeps the shared
  `{error: string}` envelope. That matters because the web client turns a 403
  that has no `error` into a generic "You don't have permission" message
  (`smart-calendar-flow/src/api/client.ts`), which would hide the prompt.
- **Rejected: one object with an optional `stepup_url` and `error: string`.**
  It is simpler for the generators, but it permits
  `{"error":"sso_write_requires_fresh_login","stepup_url":"…"}` and a step-up
  refusal without a URL. The contract would then lean on prose, which is what
  the conventions say to avoid.
- **Rejected: 401 for the stale break-glass session.** The session is valid:
  `requireAuth` accepted it, and every other operation in the group serves it.
  The web client also treats 401 on `/api/auth/me` as "logged out". A 401 here
  would tell the user they have no session, which is false. Spec §4.2 says 403
  for both privilege cases.
- **Rejected: 428 Precondition Required** for the step-up. It is technically
  apt, but no client in either repo handles it, and the spec fixed 403.

`oneOf` already appears in the contract (`teams.yaml`). oapi-codegen v2.4.1
generates it as a union type with typed `As…`/`From…` accessors. The generated
Go compiles (§6).

### `provider_domain_mismatch` is a 400, not a 403

Spec §4.2 step 4 (decided 2026-10-22, AC-16b) makes a provider domain other
than the org's a **400**. It replaces both today's suffix rule and its 403. The
reasoning: the caller's identity is not at issue, because the domain check runs
*before* authorization and fails for an admin too. What is wrong is the
request, which names a target outside the caller's org. Running it before the
grant consume is also what lets AC-3c hold: a typo in the domain does not burn
a ten-minute grant.

**The Linear acceptance text relayed to this stage says 403** for the
subdomain case. The contract follows the accepted spec. The difference is
recorded in spec §9 (Note) for the owner. Either way the request is refused
and no row changes.

### No grant id in any request

The write bodies and paths are unchanged apart from the new 400. Spec §7
already rejected carrying a grant id: the server knows the caller and the org,
and the select-and-consume statement needs nothing more. Adding an id would put
a field into two contracts that nothing could use to widen access, but that a
client could get wrong.

### `stepup_url` is a constant path on the API's origin

- **Chosen:** `const: /api/admin/sso/stepup`, without `provider`. This closes
  spec OQ-8. The value is a path on the API's origin, of the same form as
  every operation path in the document, and a client resolves it the way it
  resolves those: against the origin of its configured API base, never
  against the page's origin (Revision 2, B1). The web app and the API do not
  always share an origin. The web takes its API base at runtime from
  `window.__API_BASE_URL__`, which `docker/fileserver/main.go` injects from
  `API_BASE_URL` (passed through by `docker-compose.yml` and
  `docker-compose.prod.yml`), or at build time from `VITE_API_BASE_URL`
  (`smart-calendar-flow/Dockerfile.local` bakes a separate API host). The
  session cookie is host-only on the API host, so on a split-host deployment
  the page's origin has no session. The web already sends every top-level
  navigation to the API through its API base (`apiUrl(...)` in
  `src/components/auth/AuthDialog.tsx`), and AC-22's navigation does the
  same. Making it a `const` means the web can generate a literal type and the
  server cannot drift.
- **Rejected: an absolute URL built from `APP_URL`.** It would put deployment
  configuration into every 403. A wrongly set `APP_URL` would then send users
  to the wrong host in the middle of an admin flow. It would also break the
  Vite dev proxy setup, where the page and the API share an origin but
  `APP_URL` points somewhere else.
- **Rejected: including `?provider=`.** The provider is the user's answer to
  "which account administers your organisation?" (spec §4.3, OQ-3). The server
  cannot know it at 403 time.

### The step-up start is a GET that redirects, not a POST that returns JSON

This was decided in the spec (B2, 2026-10-22), and the contract encodes it. It
is the shape of the existing sign-in starts (`startGoogleOAuth`,
`startSsoLogin`), so the browser can be navigated straight to `stepup_url`. The
`provider` parameter references the `SsoStepupProvider` enum, so the generated
web type cannot send `okta`. The server still answers 400 for one (AC-21b),
because a URL can be typed by hand.

### The callback answers 302 for every outcome, including denials

The callback is the end of a browser navigation that started at the IdP. A
JSON 400 there is a dead page with raw text on it. So every outcome with a
session is a redirect to the settings page, with the outcome in the query
string (spec §4.3). Only two outcomes are not redirects:

- **401**: there is no session, so the request is refused before the handler
  runs, as for every operation in the group (AC-13c).
- **500**: storage failed (D-4). A `stepup=granted` redirect would claim a
  grant that might not exist, and the reason enum has no "internal" value.

The outcome and reason are encoded as a `pattern` on the `Location` header. A
first draft used two component enums, `SsoStepupOutcome` and
`SsoStepupDenialReason`. **Rejected:** nothing in the document can reference
them, because a query string on a redirect target is not a parameter of any
operation. The assembler flagged both as orphans. The pattern sits on the
response that actually carries the values, and a contract test can match a real
`Location` against it.

### Callback query parameters are optional

`state`, `code` and `error` are `required: false`. They are written by the IdP,
not by our client. A missing one is a *denial with a reason*
(`state_mismatch`, `token_invalid`), not a malformed request. Marking them
required would tell a generated validator to answer 400, a dead page, where
the spec wants a redirect.

### The state cookie is scoped to the step-up path

`sso_stepup_state; Path=/api/admin/sso/stepup; Max-Age=600; HttpOnly;
SameSite=Lax`. Because of the path, the cookie is only ever sent to the start
and the callback. `Max-Age` matches the grant's ten minutes. `Secure` is a
deployment property (spec §4.3, NB6), so the contract says "in production" and
does not tie it to a per-request observation the Go process cannot make. The
name is fixed because e2e (AC-23) and the callback's clearing `Set-Cookie`
must agree on it. No client reads it.

### Undeclared body properties are a 400, with the PAC-24 message

The create schema has said `additionalProperties: false` since the contract was
written, but the handler ignored extra properties. The spec (§4.2 step 3,
AC-16a) makes the server agree with the schema. The message reuses
`unknown property: <name>` from `PATCH /api/settings`, so one client-side
mapping covers both.

### What the contract deliberately does not express

These properties are real and tested, but they are not visible at the HTTP
boundary as a schema can state it. They are named in the descriptions and
proven by the spec's `[unit]` ACs:

- the grant is consumed atomically, is single-use and lasts ten minutes
  (AC-2, AC-3, AC-4);
- the break-glass list and the fresh-session rule (AC-14);
- the state cookie's binding to user, org, provider and nonce (AC-13b, AC-21a);
- the SSO event table (AC-12). It is not exposed by any operation, by spec
  §4.6. The contract mentions it only to say no operation exposes it;
- migration 027 and the `lower(domain)` uniqueness (AC-20);
- "no settings or token row changes" (AC-17).

## 3. What this resolves, and what it does not

| Item | Disposition |
|---|---|
| U-21 (`SsoProviderCreateRequest`) | **Resolved by determination.** Reading `handlers_sso.go` `createSSOProvider` shows exactly three non-empty checks and the enum, and nothing else. The schema says so (`minLength: 1`); the empty-OIDC-field behaviour is documented and linked to API-060. Requiring the OIDC/SAML trios would change behaviour, which the spec rules out ("adds no field validation beyond §4.2 step 3"). |
| U-22 (`SsoProviderResponse`) | **Resolved by observation.** A temporary test in `backend/storage` marshalled `storage.SSOProvider{}` with `encoding/json`. It printed `{"ID":…,"Domain":…,"ProviderName":…,"ProviderType":…,"Enabled":…,"OIDCIssuer":…,"OIDCClientID":…,"OIDCClientSecret":…,"SAMLEntryPoint":…,"SAMLIssuer":…,"SAMLCert":…,"CreatedAt":…,"UpdatedAt":…}`, exactly the thirteen keys the schema requires. The file was deleted and nothing was committed. The secret in the response is API-001; PAC-22 fixes it (spec header, §4.9). |
| API-067 | **Fixed in the contract**: 404 on an absent row; `{domain}` lower-cased. Implemented at stage 4 (AC-16c, AC-16d). |
| API-092 | **Closed at stage 4** by the spec's `[unit]` ACs on all three admin write paths. Nothing for the contract to say. |
| PAC-46 | **Absorbed**: the exact rule is in both write operations' descriptions and the `domain` property. |
| API-060, API-066 | **Not changed** (spec header). Still documented on the operation and linked. |
| API-001 | **Not changed**; PAC-22's. Linked on `SsoProviderResponse.OIDCClientSecret`. |

`x-uncertain` count in the bundle: 18 → 16.

## 4. Consumers affected

Searched on 2026-10-25 with `grep -rln "admin/sso"` over
`clockwise-like/mcp`, `clockwise-like/e2e` and `smart-calendar-flow/src`:

- **smart-calendar-flow**: only `src/api/generated/types.ts` and
  `src/api/generated/schemas.types.d.ts`. They are regenerated from the bundle
  after this contract merges (factory §1 ordering). No hand-written code calls
  these operations, so no screen changes behaviour. The one web requirement
  (AC-22, navigate on a 403 that carries `stepup_url`) is new code at stage 4,
  not a break.
- **mcp**: no reference.
- **e2e**: no reference today. AC-23 adds the API-level journey at stage 5.
- **Breaking for whom?** Any external caller that wrote SSO configuration as a
  plain member now gets 403. That is the point of the feature (spec §1). It
  hits zero production users today (spec §1, 2026-10-21).

Deployment note for stage 7, not a contract change: both IdP app registrations
must list `<APP_URL>/api/admin/sso/stepup/callback` as a redirect URI.

## 5. What a challenger should attack

- **Route overlap.** `/api/admin/sso/stepup` and `/api/admin/sso/{domain}`
  overlap at `DELETE /api/admin/sso/stepup`. OpenAPI matches the concrete path
  first, and it has no DELETE, so the request falls to `deleteSsoProvider`
  with `{domain}` = `stepup`. That is a 400 `provider_domain_mismatch` for
  every org. Is that acceptable, or does the plan need a router test?
- **Gate order leaks.** Membership → 400s → authorization means an
  unauthorized member learns whether a body is well-formed and whether the
  domain is their org's. Both facts are their own. Is there any request where
  the order discloses something about *another* org?
- **D-5.** A 404 delete consumes the grant. Is a client better served by the
  grant surviving a 404? The spec's "consume is the last step before the
  write" implies it does not survive.
- **The `Location` pattern.** Does it admit anything the server must never
  send, or forbid a legitimate URL-encoding of the query?
- **`oneOf` in the generated TypeScript.** typed-openapi and
  openapi-typescript must produce a discriminable union. The web gate runs at
  regeneration, after merge. See §6 for what was run here.

## 6. Gates run for this revision

Run on 2026-10-25 against commit `e865c60` (this revision's contract; the
bundle is byte-identical in the commit that adds this section).

- `python scripts/openapi_assemble.py --check`: `OK: bundle is in sync (100 paths)`.
- `bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/pac-50-contract`:

  ```
  make openapi rc=0
  regenerated: none
  OK: bundle is in sync (100 paths)
  OK: MIGRATION.md covers all 121 operations (0 generated)
  OK: backend/api/gen matches the contract
  openapi-check(api) rc=0
  backend lint rc=0
  mcp lint rc=0
  backend test rc=0
  mcp test rc=0
  backend  coverage: 43.3% (floor 43%) [3946 generated blocks excluded]
  backend coverage rc=0
  mcp      coverage: 84.0% (floor 75%)
  mcp coverage rc=0
  ```

- Web generation, as a check only, with nothing copied back. In a throwaway
  `node:24-bookworm` container, against clones of `smart-calendar-flow@main`
  and this branch: `make openapi` rc=0 (it rewrote `src/api/generated/{types.ts,schemas.ts,schemas.types.d.ts}`),
  `npx tsc --noEmit -p tsconfig.app.json` rc=0, then `make openapi-check`
  printed `OK: src/api/generated matches the contract` (rc=0).
  openapi-typescript emits `SsoWriteForbiddenResponse` as a union of the three
  variants, discriminated by the `error` literal. The committed web types
  regenerate in the frontend regeneration PR after this contract merges. The
  full web `make verify` was **not** run for this revision.

## Challenge — 2026-10-25

Reviewed by `contract-challenger` against the committed state of
`factory/pac-50-contract` at `9c604c2` (spec v2.2 and its §9, this file, the
manifest, `paths/auth.yaml`, the bundle). The author's reasoning was not read
beyond what these artefacts say.

**Verdict**: accept-with-changes. One blocking finding (B1); the rest does
not block stage 3.

### Blocking

**B1. `stepup_url` is said to resolve against the page's origin "in every
deployment". The code supports, and ships, deployments where that is false.**
`SsoWriteStepupRequired.stepup_url` is `const: /api/admin/sso/stepup`, and its
description (and §2 "stepup_url is a constant root-relative path") says "the
web app and the API share an origin behind nginx, so the browser resolves it
against the page's origin". The code says otherwise:
- `docker/fileserver/main.go:20-24` injects `window.__API_BASE_URL__` from
  `API_BASE_URL`, and both `docker-compose.yml:61` and
  `docker-compose.prod.yml:89` pass `API_BASE_URL` through. The web reads it
  in `smart-calendar-flow/src/api/client.ts:282-286` and `src/lib/api.ts:19-23`.
- `smart-calendar-flow/Dockerfile.local:4-7` bakes
  `VITE_API_BASE_URL=https://paceday-api.nhochart.ovh/api`: a real build in
  which the API is on its own host.
- The web never navigates to an API path raw. Every top-level navigation to
  the API goes through its API base: `src/components/auth/AuthDialog.tsx:85,104,108,112`
  wrap `/api/auth/...` in `apiUrl(...)`. That includes the `redirect_url`
  precedent cited for this choice: line 112 builds its own URL and does not
  follow `SsoDetectResponse.redirect_url` as given.

Constructed failure: page on `https://paceday.example.com`,
`API_BASE_URL=https://api.example.com/api`. The member's
`POST https://api.example.com/api/admin/sso` gets
`403 {"error":"sso_write_requires_admin_stepup","stepup_url":"/api/admin/sso/stepup"}`.
A client that does what the contract says (AC-22: navigate to `stepup_url`
resolved against the page) lands on
`https://paceday.example.com/api/admin/sso/stepup?provider=google`. The
`auth_token` cookie is host-only on `api.example.com` (`handlers_auth.go:153-160`,
no `Domain`), so that request is a 401, or the SPA's index if that host does
not proxy `/api/`. The step-up cannot start.

**Fix (contract shape only):** keep the `const`. Change the `stepup_url`
description, and §2 of this file, to say that it is a path on the API's
origin, which a client resolves the way it resolves every other API path (its
configured API base origin), and never against the page's origin. Delete the
"share an origin in every deployment" claim and the `redirect_url`-precedent
justification. The other option is an absolute URL, which this file rejected
for reasons that still hold, so the description change is the smaller fix.
AC-22's wording ("navigates the browser to that URL") is then read against the
API base. The plan should name that test.

### Requests wrongly permitted

None found that the schema should reject and does not. See "Verified clean".

### Responses the client cannot handle

1. The B1 case above: `createSsoProvider`/`deleteSsoProvider` 403
   `SsoWriteStepupRequired`. If the client follows the contract literally,
   in a split-host deployment it navigates to a URL where it has no session.

### Consumers affected

- smart-calendar-flow: `src/api/generated/types.ts` and
  `src/api/generated/schemas.types.d.ts` only (grep `admin/sso|stepup` over
  `src/`). No hand-written caller. The future AC-22 code in `src/api/client.ts`
  is affected by B1.
- mcp/: no reference (grep `admin/sso|stepup`). e2e/: no reference.
  `e2e/docker-compose.e2e.yml:189` sets `API_BASE_URL: ""`, so the AC-23
  journey runs same-origin and **will not** catch B1.

### Contract/implementation divergences (non-blocking)

1. **`APP_URL` named as the step-up redirect base.** The start's `Location`
   says redirect_uri is `<APP_URL>/api/admin/sso/stepup/callback`. `APP_URL` is
   read only by the OIDC sign-in (`handlers_sso.go:104,155`). It appears in
   neither `docker-compose.yml` nor `docker-compose.prod.yml`. The Google and
   Microsoft clients take their redirect from `GOOGLE_REDIRECT_URL` and
   `MICROSOFT_REDIRECT_URL` (`docker-compose.prod.yml:58,64`). With the prod
   compose as written, the redirect_uri would be the relative string
   `/api/admin/sso/stepup/callback`, which the IdPs refuse. Fix: describe it
   as "the deployment's configured step-up callback URL", leave the variable
   to the plan, and correct §4's deployment note to match.
2. **Orgless caller at the callback.** The callback says "every outcome with
   a session ... is recorded" and is a 302. A signed-in user with no org can
   `GET /api/admin/sso/stepup/callback` directly. That outcome is
   `state_mismatch`, but its `sso_stepup` row has no `org_id`, and spec §4.8
   has `org_id` reference `organizations`. As written, the contract permits
   either a 302 or (through D-4) a 500 for the same request. Fix: say that an
   orgless caller gets 302 `stepup=denied&reason=state_mismatch` with no
   record (mirroring D-1), or say that the event's `org_id` is nullable.

### Uncertainties not actually resolved (non-blocking)

1. **U-22**: the evidence is a temporary test that was deleted, so nothing
   pins it. The conclusion is still sound: `storage.SSOProvider`
   (`backend/storage/sso_providers.go:12`) has no json tags and no
   `MarshalJSON`, so `encoding/json` semantics settle the keys. Ask the plan
   for a committed assertion on the 201 body's key set, because AC-18 only
   reads the bundle. The U-21 and U-22 rows in
   `docs/factory/api-audit.md:465-466` still read as open. Annotate them when
   this merges.
2. **U-21**: the audit row recommended "decide and encode"
   (`dependentRequired`). The contract documents the status quo instead, as
   spec v2.2 directs (no new validation, API-060 stays open). That is
   acceptable as determination, but the decision belongs to the spec, not the
   contract. It is recorded here so that nobody reads U-21 as "decided".

### Other non-blocking

1. **The step-up does not require a fresh IdP sign-in.** Spec §2 says "a fresh
   re-authentication". Nothing at the boundary asks for it: the start's
   `Location` names no `prompt=login`/`max_age=0`, and the callback names no
   `auth_time` check. The start is a GET, and a cross-site top-level
   navigation sends the Lax `auth_token`. So a page can send a signed-in
   Workspace admin through `/api/admin/sso/stepup?provider=google` and get
   them a grant with no interaction. It does not escalate: the grant serves
   only that user's own session, and a cross-site POST does not carry the Lax
   cookie. State it in the start's `Location` description, or have the plan
   pin it.
2. **Linear scope item 3 is unaddressed.** The issue requires that
   "configurations already stored for a domain that is nobody's exact
   organisation domain ... stop minting sessions" when the exact rule lands.
   Neither spec v2.2 nor this contract does this: `ssoOidcCallback` and
   `startSsoLogin` are unchanged, so a pre-existing `eu.acme.com` row stays
   live. Production has zero users, so such rows are unlikely, but the owner
   should confirm that it was dropped deliberately. The 403-vs-400 difference
   is already recorded in spec §9.
3. **Break-glass caller who also holds a live grant.** Order 4a-then-4b
   implies that the grant is consumed and the event says `via: stepup`. Say
   so, so that AC-12/AC-14 tests cannot pick either.
4. **`DELETE /api/admin/sso/stepup`.** chi falls through to `/{domain}`, which
   gives 400 `provider_domain_mismatch`. A generated OpenAPI router may answer
   405. Both refuse. The plan should include a router test, as §5 of this
   file asks.

### Verified clean

- **`oneOf` disjointness.**
  `{"error":"sso_write_requires_fresh_login","stepup_url":"/api/admin/sso/stepup"}`
  matches no variant (`additionalProperties: false`).
  `{"error":"sso_write_requires_admin_stepup"}` matches none (`stepup_url`
  required). `{"error":"org not found"}` matches exactly one. The `error`
  values are disjoint across variants.
- **Web rendering of all three 403s.** `apiErrorMessage` reads `data.error`
  first (`smart-calendar-flow/src/api/client.ts:355`), so no variant falls to
  the generic 403 text.
- **Session on the callback.** `auth_token` is `Path=/` and `SameSite=Lax`
  (`handlers_auth.go:153-160`), and `requireAuth` accepts the cookie
  (`middleware.go:28`), so the IdP's top-level GET back carries the session.
  The state cookie's `Path=/api/admin/sso/stepup` path-matches `/callback`.
  The callback is declared GET only, which rules out Entra `form_post`, a
  cross-site POST that would drop both Lax cookies.
- **`Location` pattern.** `FRONTEND_URL` defaults to empty in
  `docker-compose.prod.yml:49`, so the server would send
  `/settings?stepup=granted`, which the unanchored pattern still matches. The
  eight reasons equal the §4.6 enum plus the D-3 mapping, with no extra value.
  `/settings` exists in the web (`src/App.tsx:86`).
- **Gate order and cross-org disclosure.** A member of `acme.com` sending
  `POST {"domain":"eu.acme.com","provider_name":"x","provider_type":"oidc"}`
  and `DELETE /api/admin/sso/other.com` gets the same 400
  `provider_domain_mismatch` whether or not that domain has a row. The
  response says nothing about another org. Membership runs first, so an
  orgless break-glass email gets the membership 403 (AC-1b). The domain check
  runs before the consume (AC-3c).
- **Unknown property message format.** `unknown property: <name>` as a JSON
  `{error}` matches `handlers_settings.go:91`. The text/plain variant at
  `handlers_daily_recap.go:70` is not the one cited.
- **Current-handler statements.** The membership, `org not found` and
  suffix-rule 403 strings quoted as "before PAC-50" match `handlers_sso.go`
  `createSSOProvider` and `deleteSSOProvider`. 201 on upsert matches. Delete
  has no rows-affected check (API-067).
- **Bookkeeping.** `x-uncertain` count 18 → 16 (grep on the `origin/main` and
  branch bundles). MIGRATION.md 119 → 121. The manifest `acceptanceTests`
  map one-to-one onto spec §5 AC-1a..AC-23 (AC-19 withdrawn), with no
  narrowing found.

### Gates run by the challenger

`bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/pac-50-contract`
on `9c604c2`:

```
make openapi rc=0
regenerated: none
OK: bundle is in sync (100 paths)
OK: MIGRATION.md covers all 121 operations (0 generated)
OK: backend/api/gen matches the contract
openapi-check(api) rc=0
backend lint rc=0
mcp lint rc=0
backend test rc=0
mcp test rc=0
backend  coverage: 43.3% (floor 43%) [3946 generated blocks excluded]
backend coverage rc=0
mcp      coverage: 84.0% (floor 75%)
mcp coverage rc=0
```

Not run by the challenger: the web gate, because web types regenerate after
the merge (factory §1) and a check-only run against web `main` would fail
`openapi-check` by construction; and e2e, because no runtime behaviour
changes at stage 2.

## Revision 2 — 2026-10-25 (response to the round-1 challenge)

Written by `contract-author` in a local session. Contract revision 2; the
bundle at `ded7aad` hashes to
`7860bcdc8511ee612c180a5ff3c5167f93386e2bb83ea44b4ba7897e562156cf`
(`git show ded7aad:contracts/openapi/openapi.yaml | sha256sum`), recorded in
the manifest.

### B1 — accepted, fixed as proposed

The finding is correct. Checked before changing anything:
`docker/fileserver/main.go:20-24` injects `window.__API_BASE_URL__` from
`API_BASE_URL`; `docker-compose.yml:61` and `docker-compose.prod.yml:89` pass
it through; `smart-calendar-flow/Dockerfile.local:4-7` bakes
`VITE_API_BASE_URL=https://paceday-api.nhochart.ovh/api`; the web's two API
bases (`src/lib/api.ts:19-23`, `src/api/client.ts:284-287`) read those; and
`src/components/auth/AuthDialog.tsx:85,104,108,112` send every top-level API
navigation through `apiUrl(...)`, including the SSO start that the
`redirect_url` "precedent" was supposed to show. The claim that the web app
and the API share an origin in every deployment was false.

Changes, contract shape untouched (the value stays `const:
/api/admin/sso/stepup`, so the generated web type is the same literal):

- `SsoWriteStepupRequired.stepup_url` (`paths/auth.yaml`): the description now
  says it is a path on the API's origin, of the same form as every operation
  path, resolved against the origin of the client's configured API base and
  never against the page's origin, and says why (runtime/build-time API base,
  host-only session cookie). The "share an origin behind nginx" sentence and
  the `SsoDetectResponse.redirect_url` precedent are gone.
- `startSsoAdminStepup` description: the navigation sentence says the client
  resolves the path against its configured API base's origin, not the page's.
- §2 of this file: the heading is now "a constant path on the API's origin";
  the "every deployment" claim and the precedent are removed and replaced by
  the evidence above. The rejection of an absolute `APP_URL`-built URL stands
  for its original reasons.
- Spec §9 D-6: the OQ-8 sentence is corrected the same way and says AC-22's
  "navigates the browser to that URL" is read against the API base.
- Manifest: `contractRevision` 2, the new `contractHash`, and AC-22's line
  states the resolution rule so that stage 3 names a test for it (a 403
  carrying `stepup_url` with a non-empty, cross-origin API base must navigate
  to `<API base origin>/api/admin/sso/stepup?provider=…`).

The challenger noted that the e2e stack sets `API_BASE_URL: ""` and so AC-23
cannot catch this; that is why the rule is pinned at AC-22 (web unit), where a
split-host API base is a test input.

### Non-blocking findings

Not changed in this revision; carried to stage 3 as the challenger asked.
Divergence 1 (`APP_URL` named as the step-up redirect base) and divergence 2
(orgless caller at the callback), the U-21/U-22 notes, and other items 1-4
are inputs the plan must resolve or pin. None alters the shape of a request
or response the generated types carry.

### Gates run for revision 2

`bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/pac-50-contract`
on `3fb469c` (the first run, on the source commit, regenerated
`backend/api/gen/paceday.gen.go` and `contracts/openapi/openapi.yaml`, which
were committed as `ded7aad`):

```
make openapi rc=0
regenerated: none
OK: bundle is in sync (100 paths)
OK: MIGRATION.md covers all 121 operations (0 generated)
OK: backend/api/gen matches the contract
openapi-check(api) rc=0
backend lint rc=0
mcp lint rc=0
backend test rc=0
mcp test rc=0
backend  coverage: 43.3% (floor 43%) [3946 generated blocks excluded]
backend coverage rc=0
mcp      coverage: 84.0% (floor 75%)
mcp coverage rc=0
```

Not run: the web gate, for the reason the challenger gave (web types
regenerate after the merge, factory §1); and e2e, because no runtime behaviour
changes at stage 2.

## Challenge — 2026-10-25 (round 2, contract revision 2)

Reviewed by `contract-challenger` against the committed state of
`factory/pac-50-contract` at `328461b` (spec v2.2 and its §9, this file, the
manifest, `paths/auth.yaml`, the bundle and the regenerated server code). The
author's reasoning was not read beyond what these artefacts say. This round
checks the B1 fix, then attacks the whole contract again, not only the diff.

**Verdict**: accept. B1 is resolved for every deployment the code ships. No
new blocking finding. The non-blocking items below, and round 1's carried
items, are inputs stage 3 must pin.

### B1 (round 1): resolved

`git diff e391bc6..328461b -- contracts/openapi/paths/` changes only two
descriptions: `SsoWriteStepupRequired.stepup_url` and the navigation sentence
of `startSsoAdminStepup`. The value stays `const: /api/admin/sso/stepup`, so
the generated types do not change shape. Spec §9 D-6 and the manifest's AC-22
line were corrected to match.

Round 1's constructed failure, replayed with the new rule: page on
`https://paceday.example.com`, `API_BASE_URL=https://api.example.com/api`. The
403 carries `"stepup_url":"/api/admin/sso/stepup"`. Resolved against the API
base's origin, that gives
`https://api.example.com/api/admin/sso/stepup?provider=google`. That host
holds the host-only `auth_token` (`handlers_auth.go:146-161`), so the step-up
starts. The same holds for `Dockerfile.local`
(`VITE_API_BASE_URL=https://paceday-api.nhochart.ovh/api`) and for the
same-origin e2e stack (`e2e/docker-compose.e2e.yml:189`, `API_BASE_URL: ""`).
The "share an origin in every deployment" claim and the `redirect_url`
precedent are gone from the fragment, from §2 and from D-6.

### Requests wrongly permitted

None found. See "Verified clean".

### Responses the client cannot handle

None found. Revision 2 changed no response shape.

### Consumers affected

- smart-calendar-flow: only `src/api/generated/types.ts` and
  `src/api/generated/schemas.types.d.ts` reference `admin/sso|stepup` (grep
  over `src/`). The AC-22 code in `src/api/client.ts` does not exist yet.
- mcp/: no reference. e2e/: no reference (grep `admin/sso|stepup`).

### Contract/implementation divergences (non-blocking)

1. **The B1 rule has two halves that disagree for an API base with a path
   prefix.** The `stepup_url` description says a client resolves the path
   "against the origin of its configured API base (for the web app, the base
   it already uses for `/api/auth/...` navigations)". The web's navigation
   base does not resolve against an origin. `apiUrl`
   (`src/lib/api.ts:100-102`) is `API_BASE + path`, where `API_BASE` is the
   injected base with its trailing `/api` removed (`src/lib/api.ts:19-23`).
   For `API_BASE_URL=https://host/paceday/api`, `/api/auth/google` goes to
   `https://host/paceday/api/auth/google`. The origin rule would send
   `stepup_url` to `https://host/api/admin/sso/stepup`, a different resource.
   No shipped configuration has such a prefix (`docker-compose*.yml`,
   `Dockerfile.local`, `e2e/docker-compose.e2e.yml`), so nothing breaks
   today. Fix (description only): say "the configured API base with its
   trailing `/api` removed, followed by the path". That equals the origin
   rule for every base of the form `<origin>/api`.

   The web also has **two** API bases. `src/lib/api.ts` reads
   `__API_BASE_URL__` or `VITE_API_URL`. `src/api/client.ts:284-287` reads
   `__API_BASE_URL__`, `VITE_API_BASE_URL` or `/api`. The description names
   the build-time variable of the second base (`VITE_API_BASE_URL`) and the
   navigation behaviour of the first. The two agree in `Dockerfile.local`,
   but not under `.env.example`, which sets only `VITE_API_URL`. The plan's
   AC-22 test should name which base it uses.
2. **A missing `provider` under the generated server wrapper is not the
   documented 400.** The contract gives a single JSON 400,
   `provider must be 'google' or 'microsoft'`, for a missing `provider` and
   for an unknown one. Factory §4 says new endpoints use the generated
   interface. In `backend/api/gen/paceday.gen.go`,
   `ServerInterfaceWrapper.StartSsoAdminStepup` answers a missing `provider`
   itself, through `ErrorHandlerFunc` with `RequiredParamError`. The default
   handler is plain-text `http.Error`, the hazard PAC-23 recorded in the same
   file. An unknown value (`okta`) binds into the string enum and reaches the
   handler. So `GET /api/admin/sso/stepup` with no query would get a
   text/plain 400 with a different message, while `?provider=okta` gets the
   JSON one. That contradicts the 400's `application/json` /
   `ErrorResponse`. The contract is correct as a statement of required
   behaviour. AC-21b should assert the content type and the message for both
   inputs, so that whichever route stage 4 takes is held to it.
   (`MIGRATION.md` lists both new operations as `handwritten`, which is
   accurate before stage 4.)
3. **Round 1 divergences 1 (`APP_URL` as the step-up `redirect_uri` base)
   and 2 (orgless caller at the callback) are unchanged**, as Revision 2
   says. Re-checked: neither compose file passes `APP_URL` (grep). The
   callback still says "every outcome with a session ... is recorded" and
   has no rule for a user with no org. Both stay inputs to stage 3. The plan
   must either add `APP_URL` to both compose files or change the
   description, and must pick one of the two orgless outcomes round 1 named.

### Uncertainties not actually resolved (non-blocking)

1. U-21 / U-22: round 1's notes stand, and nothing in revision 2 touches
   them. The plan should commit an assertion on the 201 body's thirteen
   keys. `docs/factory/api-audit.md:465-466` should be annotated when this
   merges.

### Other non-blocking

1. **Stale bookkeeping.** This file's header still says "Revision: 1". The
   manifest's `gates.api-gate` still records the revision-1 run on
   `e865c60`, although `contractRevision` is 2 and §"Gates run for
   revision 2" records `3fb469c`. Update both so the manifest stands alone
   (factory §8).
2. **Start op, orgless caller vs. org row gone.** D-1's 403 names only
   `org membership required`, but the write operations also answer
   `org not found` when the org row is gone. The start's 403 is a plain
   `ErrorResponse`, so either text validates. Say which one the start uses,
   or say "the same two messages as the writes".
3. Round 1's other items 1-4 are unchanged and carried to stage 3, as
   Revision 2 says: no fresh IdP sign-in at the boundary, Linear scope
   item 3, a break-glass caller who holds a live grant, and
   `DELETE /api/admin/sso/stepup`.

### Verified clean

- **Callback return leg in a split-host deployment** (the mirror of B1). The
  callback's 302 is `<FRONTEND_URL>/settings?...`. The three existing
  sign-in callbacks redirect exactly the same way: `handlers_auth.go:79`,
  `handlers_microsoft_auth.go:79` and `handlers_sso.go:194`, all
  `frontendURL + path`. So any deployment where sign-in lands on the web app
  also lands the step-up there. The step-up adds no new failure.
- **Break-glass freshness cannot be refreshed without a sign-in.** The only
  JWT mint is `issueJWT` (`handlers_auth.go:146-161`, the single
  `auth.GenerateToken` call outside tests), reached only from the sign-in
  callbacks, and it stamps `iat` from `time.Now()` (`auth/jwt.go:23`). No
  refresh or re-issue path exists, so "issued less than 10 minutes ago"
  does mean "signed in less than 10 minutes ago".
- **Cross-user callback replay.** Suppose an attacker sends a signed-in
  victim to
  `/api/admin/sso/stepup/callback?code=<attacker's>&state=<attacker's>`. The
  victim's `sso_stepup_state` is either absent or holds the victim's own
  nonce, so the outcome is `state_mismatch` at step 0, before any code
  exchange. Even with a valid state, step 3 compares the token email to the
  victim's session email.
- **Domain-segment variants on delete.** For org `acme.com`,
  `DELETE /api/admin/sso/ACME.com` is lower-cased to a match. The segments
  `/acme.com.`, `/%20acme.com` and `/eu.acme.com` are each a 400
  `provider_domain_mismatch` before the consume, and none discloses
  anything about another org.
- **Create body.**
  `{"domain":"acme.com","provider_name":"x","provider_type":"oidc","org_id":"x"}`
  is a 400 `unknown property: org_id`, from the step-2 shape check, before
  the domain and authorization checks.
  `{"domain":"","provider_name":"x","provider_type":"oidc"}` is excluded by
  `minLength: 1` and by the handler's existing non-empty check
  (`handlers_sso.go:223-226`).
- **Gate order against the current handler.** Today `org not found` is
  checked after the body is decoded (`handlers_sso.go:232-236`). The
  contract moves it into step 1, before the body. That is a stated PAC-50
  change, consistent with spec §4.2 step 2, not a misdescription.
- **Bundle integrity.**
  `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` printed
  `7860bcdc8511ee612c180a5ff3c5167f93386e2bb83ea44b4ba7897e562156cf`,
  which equals the manifest's `contractHash`.

### Gates run by the challenger

`bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/pac-50-contract`
on `328461b` (working tree clean before and after; nothing regenerated):

```
make openapi rc=0
regenerated: none
OK: bundle is in sync (100 paths)
OK: MIGRATION.md covers all 121 operations (0 generated)
OK: backend/api/gen matches the contract
openapi-check(api) rc=0
backend lint rc=0
mcp lint rc=0
backend test rc=0
mcp test rc=0
backend  coverage: 43.3% (floor 43%) [3946 generated blocks excluded]
backend coverage rc=0
mcp      coverage: 84.0% (floor 75%)
mcp coverage rc=0
```

Not run: the web gate, because the web types regenerate after the merge
(factory §1) and a check-only run against web `main` fails `openapi-check`
by construction; and e2e, because no runtime behaviour changes at stage 2.
