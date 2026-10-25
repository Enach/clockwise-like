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

### `stepup_url` is a constant root-relative path

- **Chosen:** `const: /api/admin/sso/stepup`, without `provider`. This closes
  spec OQ-8. In every deployment the web app and the API share one origin:
  nginx proxies `/api/` to the backend (`nginx/nginx.conf`), and the web
  client's default `API_BASE` is `/api`. So the browser resolves the path
  against the page's own origin. It follows the precedent of
  `SsoDetectResponse.redirect_url`. Making it a `const` means the web can
  generate a literal type and the server cannot drift.
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
