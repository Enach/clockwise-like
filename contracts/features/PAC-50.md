# Contract — PAC-50: SSO writes need an IdP-admin step-up, and the domain rule is exact

- **Spec**: `docs/specs/PAC-50.md` v2.2 (v2.1 challenge answered; §9 Decisions
  added at this stage)
- **Manifest**: `contracts/features/PAC-50.yaml`
- **Fragment**: `contracts/openapi/paths/auth.yaml` (plus its notes, section (d))
- **Absorbs**: PAC-46 (the exact-domain rule)
- **Revision**: 5, 2026-10-28 (1 and 2 on 2026-10-25, 3 and 4 on 2026-10-27; see the Revision sections at the end). Revision 5 records the owner decisions of 2026-10-27: sections 1 and 2 below describe revisions 1-4 where they say `provider_domain_mismatch` is a 400 or that the client navigates on a step-up 403; "Revision 5" supersedes both.

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

*Superseded in revision 5 by the owner decision of 2026-10-27 (spec §9 D-10): it is a 403. Kept as the record of revisions 1-4.*

Spec §4.2 step 4 (decided 2026-10-22, AC-16b) makes a provider domain other
than the org's a **400**. It replaces both today's suffix rule and its 403. The
reasoning: the caller's identity is not at issue, because the domain check runs
*before* authorization and fails for an admin too. What is wrong is the
request, which names a target outside the caller's org. Running it before the
grant consume is also what lets AC-3c hold: a typo in the domain does not burn
a ten-minute grant.

**The Linear acceptance text relayed to this stage says 403** for the
subdomain case. The contract follows the accepted spec. The difference is
recorded in spec §9 (Note) for the owner, labelled *assumed by the factory,
pending owner confirmation* since revision 3. Either way the request is refused
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
- migration 028 (027 before revision 3; spec §9 D-7) and the `lower(domain)` uniqueness (AC-20);
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

## Revision 3 — 2026-10-27 (finish ticket: rebase onto main, migration 028)

Written by `contract-author` in a local session for the PAC-50 finish ticket
(rebase #212, renumber its migration, ship the paired web types). Contract
revision 3; the bundle hashes to
`aa76ab724618241ad1a3db176bc618d35bcea52607de233e58e74ce08c35ee8f`
(`git show HEAD:contracts/openapi/openapi.yaml | sha256sum`), recorded in the
manifest. The shape of every request and response the generated types carry is
unchanged from revision 2 except one added `500` on `startSsoAdminStepup`.

### What changed

1. **Main merged in, not rebased.** This branch was 15 commits behind `main`
   (PAC-21, PAC-23, PAC-29, PAC-34, PAC-49, PAC-55, PAC-66 among them). It is
   published, so `main` was merged into it rather than the branch being
   rewritten. The only textual conflicts were the two `500` descriptions in
   `paths/auth.yaml`; the bundle, `MIGRATION.md` and `backend/api/gen` were
   regenerated from the fragments, not hand-merged.
2. **PAC-21 bodies on every PAC-50 `500`.** `main` now answers every 5xx
   from these handlers with a fixed body (`writeServerError`,
   `backend/api/server_errors.go`; the detail goes to the log and Sentry).
   `createSsoProvider` and `deleteSsoProvider` keep `main`'s
   `{"error":"internal error"}` text, plus PAC-50's sentence that a consumed
   grant stays consumed, and now say the `500` also covers the grant lookup
   (the stage-4 code answers 500 there). `ssoAdminStepupCallback`'s D-4 `500`
   names the same body.
3. **`startSsoAdminStepup` gains a `500`.** The stage-4 code (#212 at
   `d8ec55a`) answers 500 when the state value cannot be generated, and the
   contract did not list it. The contract describes what is (factory §7), so
   it is added, with the PAC-21 body and "no state cookie is set".
4. **Migration 028 (spec §9 D-7).** `main` already has 025 (PAC-55), 026
   (`026_audit_log_user_id`, PAC-23, #213) and 027
   (`027_settings_auto_schedule_last_run`, PAC-49, #218). PAC-50's migration is
   `028_sso_admin_stepup`. This restores spec §4.9's intent (PAC-50 after
   PAC-23 and PAC-49) with the numbers that actually landed. AC-20a now also
   requires up → down → up on top of 027. Neither 026 nor 027 touches a table
   028 alters.
5. **The 400 `provider_domain_mismatch` Note** in spec §9 is labelled
   *assumed by the factory, pending owner confirmation* (the same label review
   finding N1 asked for on #212).

### Alternatives rejected

- **Rebasing this branch onto `main`.** Cleaner history, but it rewrites
  published commits that #212 also carries (`e865c60..e19f3c8`). The factory
  forbids rewriting published history; #212's own rebase is the build stage's,
  on its own branch.
- **Keeping 026 and renumbering PAC-23's migration.** PAC-23's 026 is merged
  and applied wherever `main` is deployed; golang-migrate would never apply a
  second 026, and renaming an applied migration breaks every database that ran
  it.
- **Leaving the stage-4 strings (`grant check failed`, `state generation
  failed`, `step-up could not be recorded`) in the contract.** They leak
  nothing, but `main`'s PAC-21 rule is one fixed body per status, and two
  conventions on the same fragment would make a client branch on 500 text.
  The contract follows `main`; #212 must switch to `writeServerError` when it
  rebases (manifest `mergeConditions`).
- **Switching the domain mismatch to 403** to match the Linear text. The
  accepted spec says 400 and the owner has not answered; changing it here
  would be a behaviour change the spec does not ask for.

### Consumers affected (re-searched 2026-10-27)

`grep -rln "admin/sso"` over `clockwise-like/mcp`, `clockwise-like/e2e` (on
`main`) and `smart-calendar-flow/src`: the web hits are the three generated
files only; `mcp` none; `e2e` none on `main` (#212 adds `sso-stepup.spec.ts`).
Unchanged from revision 1.

### Gates run for revision 3

`bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/pac-50-contract`
on `f4afd20` (the first run, on the merge commit `41d9b34`, regenerated
`backend/api/gen/paceday.gen.go`, committed as `0dc81e2`; this run regenerated
nothing). `git show f4afd20:contracts/openapi/openapi.yaml | sha256sum` printed
the manifest's `contractHash`.

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
backend  coverage: 56.8% (floor 43%) [3723 generated blocks excluded]
backend coverage rc=0
mcp      coverage: 84.0% (floor 75%)
mcp coverage rc=0
```

`REGEN=1 bash /c/Users/jouzu/paceday/.claude/factory/web-gate.sh main factory/pac-50-contract`
(run on `41d9b34`; the bundle is byte-identical at `f4afd20`):

```
regenerated: src/api/generated/schemas.ts
regenerated: src/api/generated/schemas.types.d.ts
regenerated: src/api/generated/types.ts
      Tests  297 passed (297)
lint           PASS
typecheck      PASS
design-check   PASS (impeccable, ratcheted)
openapi-check  PASS
test           PASS
coverage       PASS (ratchet floor in vitest.config.ts, target 70%)
make verify rc=0
```

This shows the bundle generates and type-checks against web `main`. The
regenerated files (+702/-36 lines) were **not** committed: contract-author
opens no web PR, and the host web checkout was restored to `main`. The paired
web PR is the build stage's, against #212's rebased branch.

Not run: e2e. This branch carries no handler code, so no runtime behaviour
changes here; the e2e gate belongs to #212 after its rebase.

## Challenge — 2026-10-27 (round 3, contract revision 3)

Reviewed by `contract-challenger` against the committed state of
`factory/pac-50-contract` at `5eb62e0`. Inputs: spec v2.2 with §9, this file,
the manifest, `paths/auth.yaml`, `auth.notes.md`, the bundle and `main`'s
handlers. I did not read the author's reasoning beyond what these artefacts
say. For question 4, the only code for the four PAC-50 operations is the
stage-4 branch `impl/pac-50-sso-write-gate-backend` at `d8ec55a` (#212,
which GitHub reports as `CONFLICTING`). I read it for that purpose; it is not
under review here. This round attacks the revision-3 delta (the merge of
main, the PAC-21 500 bodies, the start's 500 and migration 028). It also
re-attacks the whole contract against `main` as it now stands, because PAC-20
and PAC-21 have landed since round 2.

**Verdict**: accept-with-changes. There is one blocking finding, and it is a
one-line description fix. Nothing else in the delta breaks.

### Blocking

1. **The contract still names the wrong migration.**
   `SsoProviderResponse.Domain` (`paths/auth.yaml:1423`) says "migration 027
   lower-cases existing rows". That text is carried into the bundle and into
   the doc comments of the regenerated web types. On `main`,
   `027_settings_auto_schedule_last_run.up.sql` belongs to PAC-49: it only adds
   `settings.auto_schedule_last_run` and touches no `sso_providers` row. The
   manifest (`migration.up`) and spec §9 D-7 both say the backfill is
   `028_sso_admin_stepup`. Revision 3 exists to make this renumbering, yet the
   one place where the contract names the migration contradicts its own
   manifest. A reader of Swagger UI or of the generated `types.ts` would look
   for the backfill in the wrong file. **Fix**: change the description to
   "migration 028 (`028_sso_admin_stepup`) lower-cases existing rows", then run
   `make openapi` and record the new `contractHash`.

### Requests wrongly permitted

None in the contract. What I attacked:

- `POST /api/admin/sso` with
  `{"domain":" acme.com ","provider_name":"x","provider_type":"oidc"}` for org
  `acme.com`. The contract requires the domain to be equal, "exact and
  case-insensitive", so this request should get 400 `provider_domain_mismatch`.
  The stage-4 `domain.DomainMatchesOrg` applies `TrimSpace` to both sides,
  accepts the request and stores `acme.com`. `DELETE /api/admin/sso/%20acme.com`
  gets 204 or 404 for the same reason, where the contract and round 2's
  "Verified clean" say 400. The contract is the stricter side and is correct.
  The fault is in stage 4: either drop the trim, or have the contract say
  "after trimming surrounding whitespace". It is listed under divergences
  below, not as a contract defect.

### Responses the client cannot handle

None. Revision 3 changes no 2xx or 4xx response shape. The added `500`s use
the shared `ErrorResponse`, which `apiErrorMessage`
(`smart-calendar-flow/src/api/client.ts:385-401`) renders through `data.error`.
For a 500 it shows the literal text "internal error". Every PAC-21 operation
behaves the same way, so this is not PAC-50's to change.

### Consumers affected (re-searched on this commit)

- smart-calendar-flow `main` (`ad52160`): `grep -rln "admin/sso\|stepup" src`
  finds only `src/api/generated/types.ts` and
  `src/api/generated/schemas.types.d.ts`. There is no hand-written caller.
- `clockwise-like/mcp` and `clockwise-like/e2e`: no match on this branch.
- Web `client.ts` still renders a 403 that has no `error` as a generic
  message (`:393-394`). Every `SsoWriteForbiddenResponse` variant carries
  `error`, so the prompt is not swallowed.

### Contract/implementation divergences (non-blocking, inputs to #212's rebase)

1. **More PAC-21 leaks exist than the manifest lists.** `mergeConditions`
   names `grant check failed`, `state generation failed` and
   `step-up could not be recorded`. At `d8ec55a` the branch also returns raw
   driver text on three 500s:
   - `writeError(w, "save failed: "+err.Error(), 500)` on create;
   - `writeError(w, err.Error(), 500)` on delete;
   - `err.Error()` on `listSsoProviders`.

   `main` already answers all three with `writeServerError` (`handlers_sso.go`
   on `main`). If the rebase keeps the branch side of the conflict, PAC-21
   regresses on create, delete and list. The universal condition ("every
   PAC-50 500") covers create and delete, but the manifest should name every
   case so the rebase cannot miss one. It should name `listSsoProviders`
   explicitly, because that is not a PAC-50 operation.
2. **The state cookie does not follow PAC-20.** The contract's "`Secure` in
   production (a deployment property)" agrees with `main`'s rule:
   `config.DecideSecureCookies` keeps Secure unless
   `PACEDAY_INSECURE_COOKIES=true`, and `setCookie` in `backend/api/cookies.go`
   applies it. The stage-4 code calls `http.SetCookie` directly and sets Secure
   only when `APP_URL` is `https`. That bypasses the helper, which
   `TestPAC20_NoCookieBypassesSetCookieHelper`
   (`backend/api/pac20_cookies_test.go:134`) forbids. It is also a different
   rule from the one the contract describes.
3. **The callback's clearing `Set-Cookie` is documented wrongly.**
   `paths/auth.yaml:1038` documents `Max-Age=-1` with no `Secure`. Go's
   `net/http` serialises `MaxAge: -1` as `Max-Age=0`, and under PAC-20 the
   deletion carries `Secure` whenever cookies are Secure. The logout cookie
   (`:615`) uses the same `-1` convention, so this is house style rather than
   a new error, but a contract test that asserts the literal would fail.
   Suggested text: "`Max-Age=0` (Go `MaxAge: -1`), plus `Secure` under the
   same deployment rule as the start".
4. **The callback's 401 has two forms.** When the session's user row is gone,
   stage 4 answers `writeError(w, "unauthorized", 401)`, which is JSON. The
   contract's 401 is `text/plain`, from requireAuth only. Either add the JSON
   401 to the contract or make stage 4 use the plain-text form.
5. **Carried from rounds 1 and 2, unchanged and re-checked:**
   - `APP_URL` is still in no compose file (grep). Stage 4 treats an empty
     `APP_URL` as "provider not configured", so on the shipped prod compose
     every start would answer 503. D-2's description only says "no OAuth
     client". Either the plan adds `APP_URL` or the contract says so.
   - The generated wrapper answers a missing `provider` with a plain-text
     `RequiredParamError` (`backend/api/gen/paceday.gen.go:6200`), and `main`
     installs no `ErrorHandlerFunc`.
   - `DELETE /api/admin/sso/stepup` falls to `deleteSsoProvider`.
   - A break-glass caller who holds a live grant consumes it first, because
     `authorizeWrite` consumes before it checks the list.

### Uncertainties not actually resolved

None new. U-21 and U-22 stand as round 2 left them. The plan should still pin
the 201 body's thirteen keys with a committed test.

### Other non-blocking

1. Spec §4.3 says "the existing session cookie sets no `Secure` at all". Since
   PAC-20 that is false on `main`: `issueJWT` goes through `setCookie`. The
   spec body is frozen and D-7 does not cover this, so §9 should record it the
   next time the spec is touched.
2. **OWNER:** D-1 to D-5, D-7 and the 400-vs-403 Note are still *assumed by the
   factory, pending owner confirmation*. The Note is the most likely to be
   reversed: the Linear acceptance text says 403 for `sub.example.com`, while
   the contract says 400 `provider_domain_mismatch`. Either way, nothing is
   written.

### Verified clean

- **Migration 028.** On this branch (the same set as `main`),
  `ls backend/storage/migrations` ends at
  `027_settings_auto_schedule_last_run`. Migration 026 alters only
  `audit_log`, and 027 alters only `settings`. The stage-4 SQL (named `026_` at
  `d8ec55a`, to be renamed) creates `sso_stepup_grants` and `sso_events` and
  alters `sso_providers` and `organizations`. The sets do not overlap, so
  up → down → up on top of 027 has nothing to collide with. The down
  migration restores `sso_providers_domain_key` and `idx_sso_providers_domain`.
- **PAC-21 500 bodies.** On create, delete, start and callback, the 500 is
  `ErrorResponse` with `{"error":"internal error"}`, which equals
  `genericServerMessage(500)` (`backend/api/server_errors.go`). The statement
  that the 500 also covers the grant lookup matches stage 4's
  `ConsumeSSOStepupGrant` error branch. Create's 500 says the grant stays
  consumed, and the 400s say it is not consumed. Stage 4 keeps that order:
  `decodeSSOProviderBody` and the domain check both run before
  `authorizeWrite`.
- **Start 500.** Stage 4 answers 500 only when `rand.Read` fails, and it does
  so before `setStateCookie`, so "no state cookie is set" holds.
- **Start 403 message.** Stage 4 answers `org membership required` both when
  the caller has no org and when the org row is gone. The contract names only
  that text, which settles round 2's item 2.
- **Undeclared create property, case variants.** In stage 4, both
  `{"Domain":"acme.com",...}` and `{"domain":"acme.com","org_id":"x",...}` get
  400 `unknown property: <name>`. Its key check is an exact map lookup, not
  Go's case-insensitive struct match, so `additionalProperties: false` is
  enforced as the schema reads.
- **Callback reason order.** Contract steps 0 to 4 match stage 4 for both
  providers:
  - Google: hd → email/email_verified → admin.
  - Microsoft: tid/verified → tid_changed → email → wids/roles.

  The eight reasons in the `Location` pattern are the eight constants in
  stage 4.
- **Session cookie on the callback.** `auth_token` is still `SameSite=Lax` on
  `main` (`handlers_auth.go:207-214`). The top-level navigation back from the
  IdP therefore carries it, as the callback description says.
- **Bundle integrity.** `git show HEAD:contracts/openapi/openapi.yaml |
  sha256sum` printed
  `aa76ab724618241ad1a3db176bc618d35bcea52607de233e58e74ce08c35ee8f`, which
  equals the manifest's `contractHash`.

### Gates run by the challenger

`bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/pac-50-contract`
on `5eb62e0`. The working tree was clean before and after, and nothing was
regenerated:

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
backend  coverage: 56.8% (floor 43%) [3723 generated blocks excluded]
backend coverage rc=0
mcp      coverage: 84.0% (floor 75%)
mcp coverage rc=0
rc=0
```

Not run:

- **The web gate.** `REGEN=1` writes into the shared host web checkout, and a
  check-only run against web `main` fails `openapi-check` by construction. The
  revision-3 `REGEN=1` run (rc=0) was the author's, not mine.
- **e2e.** There is no handler code on this branch.

## Revision 4 — 2026-10-27 (response to the round-3 challenge)

Written by `contract-author` in a local session. Contract revision 4; the
bundle hashes to
`4664eda8dd1626db768c076dc968772873614ac29dbd6fa0d0031e20523db5cd`, recorded
in the manifest. Every change is a description, except three added response
content types (start `400` text/plain, callback `401` application/json) that
document what the code already answers. No request shape changes and no
status code is added or removed.

### B1 (round 3) — accepted, fixed as proposed

`SsoProviderResponse.Domain` now reads "migration 028 (`028_sso_admin_stepup`)
lower-cases existing rows", which agrees with the manifest's `migration.up` and
spec §9 D-7. The bundle was reassembled from the fragments, and the new
`contractHash` is in the manifest (`contractRevision` 4, `revisions[3]`).

### Non-blocking findings

1. **More PAC-21 leaks than the manifest named — accepted.** `mergeConditions`
   now names every case the rebase must resolve to `main`'s `writeServerError`:
   create (`save failed: `+err.Error()), delete (err.Error()),
   `listSsoProviders` (err.Error(), named explicitly because it is not a PAC-50
   operation), the grant lookup, the start and the callback. It also requires a
   test asserting the fixed body on each. The contract's `500` descriptions
   already say `{"error":"internal error"}` on all six, so the contract itself
   is unchanged here.
2. **`TrimSpace` in the stage-4 domain comparison — the contract keeps the
   stricter rule; stage 4 drops the trim.** Spec §4.7 says "exact", and the
   contract already reads that way. Accepting `" acme.com "` would only widen
   what passes the gate, so I kept the stricter reading. The create gate
   (step 3), the `domain` property and the delete description now say
   explicitly that nothing is trimmed. They give `" acme.com "` and
   `/%20acme.com` as 400 `provider_domain_mismatch`. `mergeConditions`
   requires stage 4 to drop the trim on these paths. Round 2's "verified
   clean" for `/%20acme.com` held against the contract, not against #212's
   code. The round-3 challenge is right that the code did not match, and this
   revision says so instead of editing round 2's record. Recorded as spec §9
   D-9 (an interface detail, not a product decision).
3. **State cookie bypasses PAC-20's `setCookie` — accepted.** The start's
   `Set-Cookie` now names the PAC-20 rule (`Secure` unless
   `PACEDAY_INSECURE_COOKIES=true`, `config.DecideSecureCookies`) in place of
   "in production". `mergeConditions` requires the cookie to be set and
   cleared through `backend/api/cookies.go`'s helper, with no direct
   `http.SetCookie` and no `Secure` derived from `APP_URL`, so
   `TestPAC20_NoCookieBypassesSetCookieHelper` stays green.
4. **Clearing cookie literal — accepted.** The callback's `Set-Cookie` is now
   `Max-Age=0` (Go `MaxAge: -1`), plus `Secure` under the same rule as the
   start. Logout's `Max-Age=-1` (`auth.yaml` `logout`) has the same
   inaccuracy, but logout is not a PAC-50 operation. Changing it here would be
   an adjacent edit the spec does not ask for, so I left it as a follow-up
   rather than fixing it quietly.
5. **Callback JSON 401 — documented.** When the JWT is valid but the user row
   is gone, the callback answers `{"error":"unauthorized"}` as JSON. That is
   `main`'s existing convention for the same case (`handlers_me.go`,
   `handlers_org.go`, `middleware.go`), so the contract describes it rather
   than asking stage 4 to diverge from the house form. The callback is reached
   by top-level navigation, never by `fetch`, so no client parses either body.
   Spec §9 D-9.
6. **Carried items from rounds 1 and 2 — each answered:**
   - *`APP_URL` in no compose file.* The start's `503` now says it is also
     the answer for every provider when `APP_URL` is unset. `mergeConditions`
     makes setting it wherever AC-23 runs, and listing it in the release
     notes, a build-stage condition. It is not a secret. Compose files are not
     this stage's to edit.
   - *Plain-text `RequiredParamError`.* The start's `400` now documents both
     forms. A missing or empty `provider` is text/plain
     `Query argument provider is required, but not found`, from the generated
     wrapper's default handler (`paceday.gen.go`; `main` installs no
     `ErrorHandlerFunc`). An unknown value is the JSON message. Installing a
     JSON error handler would change every generated operation, which is not
     PAC-50's to decide.
   - *`DELETE /api/admin/sso/stepup` falls to `deleteSsoProvider`.* This is
     documented on the delete. It is the same gate with `{domain}` =
     `stepup`, so an org member gets 400 `provider_domain_mismatch` and no
     grant is consumed. No separate response is needed.
   - *A break-glass caller holding a live grant consumes it first.* This is
     documented in step 4 of the create gate, which the delete inherits, and
     in the manifest. Spec §4.2 lists the grant first (5a) and break-glass
     second (5b), so this order matches the spec rather than departing from
     it.
7. **Spec §4.3's stale `Secure` remark — recorded** as spec §9 D-8. The spec
   body is unchanged.
8. **OWNER: D-1 to D-5 and D-7** remain *assumed by the factory, pending owner
   confirmation*. They are listed as owner questions in this stage's report.
9. **OWNER: 400 vs 403 for `sub.example.com`.** Unchanged: the contract
   follows the spec (400 `provider_domain_mismatch`) and the §9 Note stays
   labelled as assumed. This stage report asks the owner which code is wanted.

### Alternatives rejected

- **Making the contract say "after trimming surrounding whitespace".** It
  would make the contract match #212 sooner, but it loosens §4.7's "exact" to
  fit code that is not merged. The contract leads stage 4, not the reverse.
- **Asking stage 4 to answer a text/plain 401 on the callback.** That would
  make PAC-50 the one handler that answers a missing user row differently
  from `main`'s other handlers, for a body no client reads.
- **Installing a JSON `ErrorHandlerFunc` for the generated wrapper.** It
  touches every generated operation and is a cross-cutting change. It needs
  its own issue.
- **Fixing logout's `Max-Age=-1` here.** It is outside PAC-50's operations.

### Gates run for revision 4

`bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/pac-50-contract`
was run on `f15273c`. It regenerated `backend/api/gen/paceday.gen.go` (the
added content types), which was committed as `5bc0eb0`. Rerun on `5bc0eb0`
(script exit 0):

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
backend  coverage: 56.8% (floor 43%) [3725 generated blocks excluded]
backend coverage rc=0
mcp      coverage: 84.0% (floor 75%)
mcp coverage rc=0
```

`git show f15273c:contracts/openapi/openapi.yaml | sha256sum` printed the
manifest's `contractHash`. The bundle is unchanged in `5bc0eb0` and after.

`REGEN=1 bash /c/Users/jouzu/paceday/.claude/factory/web-gate.sh main factory/pac-50-contract`
regenerated `src/api/generated/{schemas.ts,schemas.types.d.ts,types.ts}`
(+741/-36 lines), with `make verify rc=0`. Unlike revision 3, the regenerated
files are committed in the paired web PR,
https://github.com/Enach/smart-calendar-flow/pull/119
(`chore/regen-types-pac-50` at `51d1eb7`).
`bash /c/Users/jouzu/paceday/.claude/factory/web-gate.sh chore/regen-types-pac-50 factory/pac-50-contract`
was run on that commit:

```
lint           PASS
typecheck      PASS
design-check   PASS (impeccable, ratcheted)
openapi-check  PASS
test           PASS
coverage       PASS (ratchet floor in vitest.config.ts, target 70%)
make verify rc=0
```

Not run: e2e. This branch carries no handler code, so it changes no runtime
behaviour. The e2e gate belongs to #212 after its rebase.

## Revision 5 — 2026-10-28 (finish ticket: owner decisions, migration 029, main merged)

Base: `origin/main` at `69ffa92` merged into this branch as `9cc28cb` (a merge,
not a rebase; the only conflict was the `MIGRATION.md` count, resolved to 122
operations, 4 generated, 118 handwritten, which `openapi_migration_report.py
--check` confirms). The owner answered two of this contract's open questions
on 2026-10-27 (`.claude/factory/owner-decisions.md`); both are recorded in spec
§9 as *owner, 2026-10-27* and replace the factory's assumptions.

### What changed

1. **`provider_domain_mismatch` is a 403** (spec §9 D-10). The owner chose
   RFC 9110 §15.5.4: the server understood the request and the caller is not
   permitted to configure that domain. On both writes the 400 response no
   longer lists `provider_domain_mismatch` (on `deleteSsoProvider` the 400 is
   gone entirely, since it had no other case), and `SsoWriteForbiddenResponse`
   gains a fourth disjoint variant, `SsoWriteDomainMismatch`
   (`error: const provider_domain_mismatch`). The gate order does not change:
   membership, body shape, domain, then authorization. So the domain 403 still
   comes before the grant is consumed, and AC-3c holds as before: a typo does
   not burn a grant. `" acme.com "`, `sub.acme.com` and `other.com`, and the
   `DELETE` forms `/%20acme.com`, `/sub.acme.com` and `/other.com`, are each
   this 403 (AC-16b). This also brings the contract into line with the Linear
   acceptance text ("Then 403"), so the revision-3 *Note* is withdrawn.
2. **The step-up is optional and is explained before any redirect** (spec §9
   D-11, owner answer to PAC-68 OQ-2 / PAC-50 OQ-2). `SsoWriteStepupRequired`
   gains two required properties:
   - `stepup_scopes: {google: string[], microsoft: string[]}` lists what the
     step-up asks each provider for, beyond the identity scopes;
   - `without_stepup: string[]`, an open enum, today only
     `sso_configuration_writes`, says what the user cannot do if they
     decline (only an operator on the break-glass list can write SSO
     configuration without it).
   `startSsoAdminStepup` now says that sign-in never requests these scopes,
   that declining costs nothing else, and that the step-up can be started again
   from Settings. AC-22 changes: the web client no longer navigates on its own.
   It yields a typed step-up-required outcome, and the screen that made the
   write explains it before any navigation. New AC-24 pins that the sign-in
   authorize URLs carry no new scope.
3. **Migration 029** (spec §9 D-7). `main` took 028 for
   `028_credential_losses` (PAC-67, #232). `SsoProviderResponse.Domain` now
   names `029_sso_admin_stepup`, and so do the manifest's `migration` and
   AC-20a.
4. **Order with PAC-22 reversed** (spec §9 D-12). PAC-22's implementation
   (#237) is ready to merge before #212, so PAC-50 lands after it. Once PAC-22
   is on `main`, `main` is merged into this branch and into #212, and a
   revision 6 describes PAC-22's provider response on these operations. The
   manifest condition "PAC-22 lands after PAC-50 and rebases onto it" is
   replaced by that order.

The `mergeConditions` are rewritten as the checklist stage 4 must meet and
assert by test: PAC-21 bodies on all six 500s, including `listSsoProviders`;
no TrimSpace on the domain paths; the `setCookie` helper for
`sso_stepup_state`; `APP_URL` set in e2e compose and documented for release;
`stepup_scopes`/`without_stepup` matching what the start requests; AC-24; web
#119 regenerated against this `contractHash`; the PAC-22 order; and all three
gates at rc=0.

### Alternatives rejected

- **A 422 for the domain mismatch.** The body is well-formed and the target
  is understood. The owner chose 403 because the refusal is about permission,
  and 422 would also have been a fourth status a client must map.
- **The 403 with a free-text `error` (today's `can only configure SSO for your
  own domain`).** A client could not tell it apart from the other three 403s
  without matching on prose. A `const` keeps the oneOf disjoint on `error`.
- **Putting the explanation text itself in the 403 (`message: "..."`).** It
  would fix English prose in the API and make the server the copywriter. The
  machine-readable lists let the client word and translate the explanation,
  and a test can compare `stepup_scopes` to what the start really requests.
- **Listing `verified_domain_org_membership` in `without_stepup` now.** The
  owner gave automatic org membership as an example of what extended access
  provides, but PAC-50's step-up discards its tokens after the admin check and
  grants one write. It does not provide membership, and the contract describes
  what is (factory §7 rule 2). The enum is open, so PAC-68 can add the value if
  it reuses this step-up. Recorded as assumed, pending owner confirmation
  (spec §9 D-11).
- **Keeping AC-22's automatic navigation and explaining on the IdP side.** That
  explanation would come after the redirect. The owner asked for it before.
- **Waiting for PAC-22 to land before cutting this revision.** The owner
  decisions and the 029 renumber do not depend on PAC-22. Holding them back
  would leave #212's tests written against a 400 the owner has rejected.

### Consumers affected (re-searched 2026-10-28)

- `smart-calendar-flow`: only `src/api/generated/*` mention `/api/admin/sso`
  or these schemas. #119 must be regenerated against this bundle. No
  hand-written code reads `provider_domain_mismatch`, and `client.ts` has no
  step-up handling yet (AC-22 is stage-4 web work).
- `clockwise-like` #212: `backend/api/pac50_sso_gate_test.go`,
  `backend/api/pac50_merge_conditions_test.go` and
  `e2e/tests/sso-stepup.spec.ts` mention `provider_domain_mismatch`. Where
  they assert a 400, stage 4 changes them to 403 (manifest
  `consumersAffected`). The step-up 403 body in #212 must gain the two new
  properties.
- `mcp/`: no reference to `/api/admin/sso`.
- PAC-22 (#237): its contract already answers `setSsoProviderState`'s domain
  mismatch with 403 `provider_domain_mismatch` ("follows PAC-50 as merged on
  rebase"), which is consistent with this revision. Its manifest's
  `rebaseOntoPac50` describes the opposite order from spec §9 D-12, so its
  contract-author has to reconcile it.

### Gates run for revision 5

`bash /c/Users/jouzu/paceday/.claude/factory/api-gate.sh factory/pac-50-contract`
on `a7ce915` (host api checkout on this branch; script exit 0). The bundle and
server code had already been regenerated and committed in `591a025`:

```
make openapi rc=0
regenerated: none
OK: bundle is in sync (101 paths)
OK: MIGRATION.md covers all 122 operations (4 generated)
OK: backend/api/gen matches the contract
openapi-check(api) rc=0
backend lint rc=0
mcp lint rc=0
backend test rc=0
mcp test rc=0
backend  coverage: 63.1% (floor 57%) [3753 generated blocks excluded]
backend coverage rc=0
mcp      coverage: 84.0% (floor 75%)
mcp coverage rc=0
```

`git show HEAD:contracts/openapi/openapi.yaml | sha256sum` on `591a025` printed
`8b9321ae60fca5e73eaa4623ad672469d654a78d3eb21d7ac011e0b3ab9478ad`, which is the
manifest's `contractHash`. The commits after `591a025` do not touch the bundle.

Web: `origin/main` (`aa5c52e`) was merged into `chore/regen-types-pac-50`.
Then `REGEN=1 bash /c/Users/jouzu/paceday/.claude/factory/web-gate.sh chore/regen-types-pac-50 factory/pac-50-contract`
regenerated `src/api/generated/{schemas.ts,schemas.types.d.ts,types.ts}`
(+187/-83) and ended with `make verify rc=0`. The output was committed as
`fdfb434` on https://github.com/Enach/smart-calendar-flow/pull/119. The gate
was then rerun without REGEN on `fdfb434`:

```
      Tests  315 passed (315)
lint           PASS
typecheck      PASS
design-check   PASS (impeccable, ratcheted)
openapi-check  PASS
test           PASS
coverage       PASS (ratchet floor in vitest.config.ts, target 70%)
make verify rc=0
```

Not run: e2e. This branch carries no handler code. e2e-gate, including
`e2e/tests/sso-stepup.spec.ts`, is a merge condition on #212 once #212 has
merged this revision and moved its tests to 403.

## Challenge, revision 5

*Pending.* A separate contract-challenger round reviews this final revision,
covering the 403 `provider_domain_mismatch`, `stepup_scopes`/`without_stepup`,
the 029 renumber and the PAC-22 order. It records here the requests and
responses it tried to break, and its verdict. Until then the manifest's first
`mergeConditions` item is open.
